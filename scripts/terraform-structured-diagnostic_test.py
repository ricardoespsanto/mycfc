"""Security and exit-status regressions for the protected JSON UI classifier."""

import contextlib
import hashlib
import importlib.util
import io
import json
import os
import pathlib
import shlex
import stat
import sys
import tempfile
import unittest
from unittest.mock import patch

SOURCE = pathlib.Path(__file__).with_name("terraform-structured-diagnostic.py")
SPEC = importlib.util.spec_from_file_location("structured_diagnostic", SOURCE)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
VERSION = b'{"type":"version","ui":"1.0"}\n'
PRIVATE = "private@example.invalid token=synthetic-private-value"


def diagnostic(summary: str, detail: str = "", **extra: object) -> bytes:
    return (json.dumps({"type": "diagnostic", "@level": "error",
                        "@message": PRIVATE, "diagnostic": {"severity": "error",
                        "summary": summary, "detail": detail,
                        "range": {"filename": PRIVATE, "snippet": PRIVATE}},
                        "resource": {"addr": PRIVATE}, **extra}) + "\n").encode()


def command(stdout: bytes, stderr: bytes = b"", exit_code: int = 7) -> list[str]:
    # Fixed synthetic fixture. No real Terraform/backend/credentials are used.
    return ["bash", "-c", "printf %s " + shlex.quote(stdout.decode()) +
            " && printf %s " + shlex.quote(stderr.decode()) + " >&2; exit " + str(exit_code)]


class StructuredDiagnosticTest(unittest.TestCase):
    def test_single_allowlisted_error_without_raw_field_disclosure(self):
        source = VERSION + diagnostic("Provider request failed", "Cloudflare API 403; " + PRIVATE)
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            status, category, count = MODULE.run_command(command(source), root, root)
            self.assertEqual((status, category, count), (7, "cloudflare-auth", "1"))
            self.assertEqual(list(root.iterdir()), [])  # Captures deleted, not uploaded.
            with patch.dict(os.environ, {"RUNNER_TEMP": directory}), patch.object(sys, "argv", [str(SOURCE), "run"]), patch.object(MODULE, "COMMAND", command(source)[2]):
                output = io.StringIO()
                with contextlib.redirect_stdout(output), contextlib.chdir(root):
                    self.assertEqual(MODULE.main(), 7)
                self.assertIn("heuristic_category=cloudflare-auth error_count=1 terraform_exit=7", output.getvalue())
                self.assertNotIn(PRIVATE, output.getvalue())
                self.assertEqual(list(root.iterdir()), [])

    def test_unknown_and_multiple_are_unclassified_with_capped_count(self):
        cases = ((VERSION + diagnostic("novel error " + PRIVATE), "1"),
                 (VERSION + diagnostic("AccessDenied " + PRIVATE) + diagnostic("Cloudflare 429 " + PRIVATE), "2+"),
                 (VERSION + diagnostic("AccessDenied and Cloudflare 403 " + PRIVATE), "1"),
                 (VERSION + diagnostic("AccessDenied") * 4, "2+"))
        for source, count in cases:
            with self.subTest(count=count, data_size=len(source)):
                self.assertEqual(MODULE.classify(source, b"", False), ("unclassified", count))

    def test_malformed_overflow_stderr_only_and_unstructured_error(self):
        good = VERSION + diagnostic("AccessDenied", PRIVATE)
        cases = (VERSION + b"not-json\n", good + b"not-json\n",
                 b'{"type":"version","ui":"2.0"}\n' + diagnostic("AccessDenied"),
                 VERSION + b'{"type":"log","@level":"error","@message":"' + PRIVATE.encode() + b'"}\n' + diagnostic("AccessDenied"),
                 VERSION, b"")
        for source in cases:
            with self.subTest(source_length=len(source)):
                self.assertEqual(MODULE.classify(source, b"", False)[0], "unclassified")
        self.assertEqual(MODULE.classify(good, b"secret-stderr " + PRIVATE.encode(), False)[0], "unclassified")
        self.assertEqual(MODULE.classify(good, b"", True)[0], "unclassified")
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            status, category, count = MODULE.run_command(command(b"", PRIVATE.encode(), 9), root, root)
            self.assertEqual((status, category, count), (9, "unclassified", "unknown"))
            self.assertEqual(list(root.iterdir()), [])
            status, category, count = MODULE.run_command(command(good, b"", 13), root, root, max_bytes=40)
            self.assertEqual((status, category), (13, "unclassified"))
            self.assertEqual(list(root.iterdir()), [])
            status, category, count = MODULE.run_command(command(good, b"", 11), root, root, max_lines=1)
            self.assertEqual((status, category), (11, "unclassified"))

    def test_private_capture_modes_and_nonzero_status(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "private"
            stream = MODULE.BoundedStream(path, 16, 2)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            stream.append(b"first\nsecond\nthird\n")
            self.assertTrue(stream.overflow)
            stream.close()
            self.assertEqual(path.read_bytes(), b"first\nsecond\n")
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            status, category, count = MODULE.run_command(command(VERSION + diagnostic("AccessDenied"), b"", 1), root, root)
            self.assertEqual((status, category, count), (1, "aws-access-denied", "1"))

    def test_finite_categories_from_structured_error_only(self):
        examples = (("Invalid reference", "terraform-config"),
                    ("Failed to load provider", "provider-init-or-schema"),
                    ("Plugin did not respond", "provider-crash-or-timeout"),
                    ("Error acquiring the state lock", "state-lock-or-backend"),
                    ("AccessDeniedException", "aws-access-denied"),
                    ("ThrottlingException", "aws-throttled"),
                    ("ExpiredTokenException", "aws-session-expired"),
                    ("Cloudflare API 403", "cloudflare-auth"),
                    ("Cloudflare API 429", "cloudflare-rate-or-network"),
                    ("out of memory", "runner-out-of-memory"))
        for summary, expected in examples:
            with self.subTest(summary=summary):
                self.assertEqual(MODULE.classify(VERSION + diagnostic(summary, PRIVATE), b"", False), (expected, "1"))

    def test_workflow_keeps_fixed_production_and_never_prints_raw(self):
        workflow = (SOURCE.parent.parent / ".github/workflows/terraform-production-v2-diagnostic.yml").read_text()
        self.assertIn("PRODUCTION_SOURCE_SHA: 6edbe46244207f21aad446d645752a8b15e5a7f1", workflow)
        self.assertIn(hashlib.sha256(SOURCE.read_bytes()).hexdigest(), workflow)
        self.assertEqual(MODULE.COMMAND.split("; ")[-1],
                         "tf plan -json -input=false -parallelism=1 -lock-timeout=5m -out=residual.tfplan")
        self.assertNotIn("tf apply", MODULE.COMMAND)
        self.assertIn("APPROVED_DIAGNOSTIC_SOURCE_SHA: 5af45505cefa86aa0b22ad563d0228140fda2892", workflow)
        self.assertIn("environment: production-plan", workflow)
        self.assertIn("group: mycfc-production-infra", workflow)
        self.assertIn("role-to-assume: ${{ vars.AWS_INFRA_PLAN_ROLE_ARN }}", workflow)
        self.assertIn("python3 ../diagnostic-runtime/scripts/terraform-structured-diagnostic.py run", workflow)
        self.assertIn("scripts/terraform-structured-diagnostic_test.py", workflow)
        self.assertIn("tf init -input=false", workflow)
        self.assertIn("umask 077", workflow)
        self.assertIn("rm -f \"$diagnostic_log\" \"$TF_ROOT/residual.tfplan\"; terraform_stack_cleanup", workflow)
        self.assertNotIn("tf apply", workflow)
        self.assertNotIn("upload-artifact", workflow)


if __name__ == "__main__":
    unittest.main()
