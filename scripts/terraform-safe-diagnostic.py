#!/usr/bin/env python3
"""Emit only fixed labels for a protected Terraform plan failure.

Never print or archive the input: Terraform diagnostics can contain secret values.
These labels are signals, not a root-cause finding.
"""

import re
import sys
from pathlib import Path

MAX_BYTES = 2_000_000
PATTERNS = (
    ("aws-access-denied", rb"(?i)AccessDenied|UnauthorizedOperation|not authorized to perform"),
    ("aws-throttled", rb"(?i)Throttling(?:Exception)?|RequestLimitExceeded|TooManyRequests"),
    ("aws-session-expired", rb"(?i)ExpiredToken|security token (?:has )?expired"),
    ("state-lock-or-backend", rb"(?i)Error acquiring the state lock|Failed to (?:load|save) state|S3 Backend"),
    ("cloudflare-auth", rb"(?i)cloudflare.{0,120}(?:401|403|unauthoriz|forbidden|authenticat)"),
    ("cloudflare-rate-or-network", rb"(?i)cloudflare.{0,120}(?:429|rate.limit|timeout|connection refused|dial tcp)"),
    ("provider-crash-or-timeout", rb"(?i)Plugin did not respond|plugin process exited|provider.*(?:crash|timed out|deadline exceeded)"),
    ("terraform-config", rb"(?i)Error: (?:Invalid reference|Invalid function argument|Unsupported attribute|Missing required argument|Invalid provider configuration)"),
    ("runner-out-of-memory", rb"(?i)out of memory|signal: killed|Cannot allocate memory"),
)


def categorize(content: bytes) -> str:
    if len(content) > MAX_BYTES:
        return "unclassified"
    labels = [label for label, pattern in PATTERNS if re.search(pattern, content)]
    return ",".join(labels) if labels else "unclassified"


def main() -> int:
    if len(sys.argv) != 4 or sys.argv[2] not in {"init", "full-plan"} or not sys.argv[3].isdigit() or not 1 <= int(sys.argv[3]) <= 255:
        return 2
    path = Path(sys.argv[1])
    if not path.is_file():
        return 2
    # Read no more than the bound plus one byte; never echo captured content.
    with path.open("rb") as source:
        content = source.read(MAX_BYTES + 1)
    label = categorize(content)
    print(f"::error::Protected Terraform stage={sys.argv[2]} failed; heuristic_category={label}; terraform_exit={sys.argv[3]}. No raw diagnostics were printed or retained.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
