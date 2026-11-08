#!/usr/bin/env python3
"""Bound and classify private Terraform JSON UI without exposing diagnostics.

Only this script launches the fixed full-plan command. The selected production
stack, backend and credentials are prepared by the protected workflow first.
Neither JSON UI nor Terraform stderr is safe to print, store as an artifact, or
include in a GitHub summary. Categories are heuristic, never a root-cause claim.
"""

import json
import os
import re
import selectors
import subprocess
import sys
import tempfile
from pathlib import Path

MAX_BYTES = 2_000_000  # Per stream, including JSON UI with possible values.
MAX_LINES = 4096
MAX_LINE_BYTES = 256_000
COMMAND = (
    "set -Eeuo pipefail; source scripts/terraform-stack.sh; "
    "terraform_stack_select production; "
    "tf plan -json -input=false -parallelism=1 -lock-timeout=5m -out=residual.tfplan"
)
# Strict, finite heuristics; never display any matched source string.
PATTERNS = (
    ("terraform-config", re.compile(r"\b(?:Invalid reference|Invalid function argument|Unsupported attribute|Missing required argument|Invalid provider configuration|Invalid expression|Invalid value for (?:input )?variable)\b", re.I)),
    ("provider-init-or-schema", re.compile(r"\b(?:Failed to (?:query|install|load) provider|Incompatible provider version|Provider produced invalid plan|Failed to instantiate provider)\b", re.I)),
    ("provider-crash-or-timeout", re.compile(r"\b(?:Plugin did not respond|plugin process exited|provider plugin crashed|provider.*(?:timed out|deadline exceeded))\b", re.I)),
    ("state-lock-or-backend", re.compile(r"\b(?:Error acquiring the state lock|Failed to (?:load|read|save) state|Error loading state|Backend initialization required|Failed to get existing workspaces)\b", re.I)),
    ("aws-access-denied", re.compile(r"\b(?:AccessDenied(?:Exception)?|UnauthorizedOperation|not authorized to perform)\b", re.I)),
    ("aws-throttled", re.compile(r"\b(?:Throttling(?:Exception)?|RequestLimitExceeded|TooManyRequests(?:Exception)?)\b", re.I)),
    ("aws-session-expired", re.compile(r"\b(?:ExpiredToken(?:Exception)?|security token (?:has )?expired)\b", re.I)),
    ("cloudflare-auth", re.compile(r"\bcloudflare\b.{0,120}\b(?:401|403|unauthoriz\w*|forbidden|authenticat\w*)\b", re.I)),
    ("cloudflare-rate-or-network", re.compile(r"\bcloudflare\b.{0,120}\b(?:429|rate.limit|timeout|connection refused|dial tcp)\b", re.I)),
    ("runner-out-of-memory", re.compile(r"\b(?:out of memory|signal: killed|Cannot allocate memory)\b", re.I)),
)


class BoundedStream:
    def __init__(self, path: Path, max_bytes: int, max_lines: int):
        self.file = os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb")
        self.max_bytes = max_bytes
        self.max_lines = max_lines
        self.bytes = 0
        self.lines = 0
        self.overflow = False

    def append(self, data: bytes) -> None:
        # Drain the child even after the capture is full: no SIGPIPE and no
        # altered Terraform exit status. Retain at most the fixed byte/line cap.
        remaining = max(0, self.max_bytes - self.bytes)
        chunk = data[:remaining]
        if self.lines >= self.max_lines:
            chunk = b""
        elif chunk.count(b"\n") >= self.max_lines - self.lines:
            end = -1
            for _ in range(self.max_lines - self.lines):
                end = chunk.index(b"\n", end + 1)
            chunk = chunk[: end + 1]
        self.file.write(chunk)
        self.bytes += len(chunk)
        self.lines += chunk.count(b"\n")
        if len(chunk) != len(data):
            self.overflow = True

    def close(self) -> None:
        self.file.close()


def classify(stdout: bytes, stderr: bytes, overflow: bool) -> tuple[str, str]:
    if overflow or len(stdout) > MAX_BYTES or len(stderr) > MAX_BYTES:
        return "unclassified", "unknown"
    if not stdout or len(stdout.splitlines()) > MAX_LINES or len(stderr.splitlines()) > MAX_LINES:
        return "unclassified", "unknown"
    matches: list[str] = []
    malformed = False
    saw_version = False
    for raw in stdout.splitlines():
        if len(raw) > MAX_LINE_BYTES:
            malformed = True
            break
        try:
            item = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError):
            malformed = True
            break
        if not isinstance(item, dict):
            malformed = True
            break
        if item.get("type") == "version" and not saw_version:
            ui = item.get("ui")
            saw_version = isinstance(ui, str) and ui.split(".")[0] == "1"
        if item.get("@level") == "error" and item.get("type") != "diagnostic":
            malformed = True
        if item.get("type") == "diagnostic":
            diagnostic = item.get("diagnostic")
            if not isinstance(diagnostic, dict) or item.get("@level") != diagnostic.get("severity"):
                malformed = True
        if item.get("type") != "diagnostic" or item.get("@level") != "error":
            continue
        diag = item.get("diagnostic")
        if not isinstance(diag, dict) or diag.get("severity") != "error":
            malformed = True
            continue
        summary, detail = diag.get("summary"), diag.get("detail", "")
        if not isinstance(summary, str) or not isinstance(detail, str) or len(summary) > 4096 or len(detail) > 16384:
            malformed = True
            continue
        # Only these two fields are inspected privately. @message, snippets,
        # ranges, paths, addresses, providers, values and resource events are
        # neither interpreted nor emitted.
        candidate = summary + "\n" + detail
        labels = [label for label, pattern in PATTERNS if pattern.search(candidate)]
        matches.append(labels[0] if len(labels) == 1 else "unclassified")
    count = "2+" if len(matches) > 1 else str(len(matches))
    # Terraform/provider stderr is not a structured diagnostic. Even if a
    # stdout diagnostic matched, nonempty stderr could carry another error.
    if stderr or malformed or not saw_version:
        return "unclassified", "unknown"
    if len(matches) != 1:
        return "unclassified", count
    return matches[0], count


def run_command(command: list[str], cwd: Path, runner_temp: Path,
                max_bytes: int = MAX_BYTES, max_lines: int = MAX_LINES) -> tuple[int, str, str]:
    with tempfile.TemporaryDirectory(prefix="tf-private-", dir=runner_temp) as temporary:
        root = Path(temporary)
        streams = {
            "stdout": BoundedStream(root / "stdout", max_bytes, max_lines),
            "stderr": BoundedStream(root / "stderr", max_bytes, max_lines),
        }
        try:
            child = subprocess.Popen(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                with selectors.DefaultSelector() as selector:
                    assert child.stdout is not None and child.stderr is not None
                    selector.register(child.stdout, selectors.EVENT_READ, streams["stdout"])
                    selector.register(child.stderr, selectors.EVENT_READ, streams["stderr"])
                    while selector.get_map():
                        for key, _ in selector.select():
                            data = os.read(key.fd, 65536)
                            if data:
                                key.data.append(data)
                            else:
                                selector.unregister(key.fileobj)
                status = child.wait()
            except BaseException:
                # A capture failure must not leave Terraform blocked on a full
                # pipe while the protected job waits indefinitely.
                if child.poll() is None:
                    child.kill()
                child.wait()
                raise
            finally:
                if child.stdout is not None:
                    child.stdout.close()
                if child.stderr is not None:
                    child.stderr.close()
        finally:
            for stream in streams.values():
                stream.close()
        stdout = (root / "stdout").read_bytes()
        stderr = (root / "stderr").read_bytes()
        overflow = any(stream.overflow for stream in streams.values())
        category, count = classify(stdout, stderr, overflow)
    # TemporaryDirectory has now removed BOTH private captures. Caller prints
    # only fixed metadata and its EXIT trap removes plan and variable files.
    return status, category, count


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] != "run":
        return 2
    try:
        runner_temp = Path(os.environ["RUNNER_TEMP"])
        if not runner_temp.is_dir():
            raise ValueError("runner temp unavailable")
        status, category, count = run_command(["bash", "-c", COMMAND], Path.cwd(), runner_temp)
    except Exception:  # Never print an exception or path from sensitive context.
        print("::error::Protected Terraform stage=full-plan heuristic_category=unclassified error_count=0 terraform_exit=1")
        return 1
    if status == 0:
        return 0
    print(f"::error::Protected Terraform stage=full-plan heuristic_category={category} error_count={count} terraform_exit={status if status > 0 else 1}")
    return status if status > 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
