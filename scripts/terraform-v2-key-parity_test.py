#!/usr/bin/env python3
"""Synthetic parity checks and source-level safety contracts."""

import importlib.util
import io
import os
from pathlib import Path
import re
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/terraform-v2-key-parity.py"
WORKFLOW = ROOT / ".github/workflows/terraform-production-v2-key-parity.yml"
spec = importlib.util.spec_from_file_location("v2_key_parity", SCRIPT)
assert spec is not None and spec.loader is not None
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = "a" * 40
KEY_A = "a" * 64
KEY_B = "b" * 64


class ParityTest(unittest.TestCase):
    def assert_protected_jobs(self, text):
        # Parse only active YAML job blocks and active run lines. Comments must
        # never satisfy a protected environment or preflight assertion. The
        # preflight is intentionally checked as a complete ordered shell block:
        # a mentioned command is not evidence that it will execute.
        jobs = re.findall(r"(?ms)^  (production_plan|production):\n(.*?)(?=^  [a-z_]+:|\Z)", text)
        self.assertEqual(["production_plan", "production"], [name for name, _ in jobs])
        for name, body in jobs:
            expected = "production-plan" if name == "production_plan" else "production"
            environments = re.findall(r"(?m)^    environment: ([a-z-]+)\s*$", body)
            self.assertEqual([expected], environments)
            runs = re.findall(r"(?ms)^        run: \|\n(.*?)(?=^      - |\Z)", body)
            self.assertEqual(2, len(runs))
            preflight = [line.strip() for line in runs[0].splitlines()
                         if line.strip() and not line.lstrip().startswith("#")]
            self.assertEqual([
                "set -Eeuo pipefail",
                'fail() { echo "::error::Key parity preflight $1 refused" >&2; exit 1; }',
                '[[ "$REQUESTED_SHA" =~ ^[0-9a-f]{40}$ ]] || fail sha-format',
                'test "$GITHUB_REF" = refs/heads/main || fail dispatch-ref',
                'test "$REQUESTED_SHA" = "$GITHUB_SHA" || fail dispatch-sha',
                'test "$REQUESTED_SHA" = "$(git rev-parse HEAD)" || fail checkout-sha',
                'test "$REQUESTED_SHA" = "$(gh api "/repos/${{ github.repository }}/git/ref/heads/main" --jq \'.object.sha\' 2>/dev/null)" || fail current-main',
                'test "$REQUESTED_SHA" = "$(gh api "/repos/${{ github.repository }}/commits/$REQUESTED_SHA" --jq \'select(.commit.verification.verified == true and .commit.verification.reason == "valid") | .sha\' 2>/dev/null)" || fail signed-main',
                'test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")" || fail main-ci',
            ], preflight)
        self.assertRegex(jobs[1][1], r"(?m)^    needs: production_plan$")

    def invoke(self, mode, key, expected=None):
        env = {"TF_PLAN_HMAC_KEY": key}
        if expected is not None:
            env["EXPECTED_PARITY_PROBE"] = expected
        out, err = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, env, clear=True), patch.object(sys, "argv", [str(SCRIPT), mode, SHA]), redirect_stdout(out), redirect_stderr(err):
            status = module.main()
        return status, out.getvalue(), err.getvalue()

    def test_synthetic_equal_and_unequal_keys(self):
        probe = module.parity_probe(KEY_A, SHA)
        self.assertEqual((0, "key_parity=match\n", ""), self.invoke("compare", KEY_A, probe))
        self.assertEqual((1, "key_parity=mismatch\n", ""), self.invoke("compare", KEY_B, probe))
        self.assertNotIn(probe, str(self.invoke("compare", KEY_B, probe)))
        self.assertNotIn(KEY_B, str(self.invoke("compare", KEY_B, probe)))

    def test_invalid_or_absent_inputs_fail_closed_without_values(self):
        for key in ("", "wrong", "A" * 64):
            status, out, err = self.invoke("fingerprint", key)
            self.assertEqual(1, status)
            self.assertEqual("", out)
            self.assertEqual("key-parity configuration refused\n", err)
        self.assertEqual((1, "", "key-parity binding refused\n"), self.invoke("compare", KEY_A, "invalid"))
        with self.assertRaises(ValueError):
            module.parity_probe(KEY_A, "bad-sha")
        self.assertNotEqual(module.parity_probe(KEY_A, SHA), module.parity_probe(KEY_A, "b" * 40))

    def test_workflow_is_diagnostic_only_with_distinct_human_gates(self):
        text = WORKFLOW.read_text()
        self.assert_protected_jobs(text)
        self.assertIn("group: mycfc-production-infra", text)
        self.assertEqual(2, text.count("verify-exact-main-ci.py"))
        self.assertEqual(2, text.count("secrets.TF_PLAN_HMAC_KEY"))
        self.assertIn("EXPECTED_PARITY_PROBE: ${{ needs.production_plan.outputs.parity_probe }}", text)
        self.assertIn("ref: ${{ inputs.sha }}", text)
        self.assertIn("persist-credentials: false", text)
        for forbidden in ("id-token: write", "configure-aws-credentials", "terraform-stack.sh", "tf state", "tf untaint", "tf apply", "aws secretsmanager", "TF_VARS", "CLOUDFLARE_API_TOKEN", "pull_request", "upload-artifact", "contents: write"):
            self.assertNotIn(forbidden, text)

    def test_negative_controls_reject_removed_preflights_and_wrong_gate(self):
        text = WORKFLOW.read_text()
        no_ci = text.replace('test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")"',
                             '# test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")"')
        self.assertNotEqual(text, no_ci)
        with self.assertRaises(AssertionError):
            self.assert_protected_jobs(no_ci)
        mentioned_but_bypassed = text.replace(
            'test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")" || fail main-ci',
            'true # test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")" || fail main-ci')
        self.assertNotEqual(text, mentioned_but_bypassed)
        with self.assertRaises(AssertionError):
            self.assert_protected_jobs(mentioned_but_bypassed)
        early_success = text.replace('          set -Eeuo pipefail\n', '          set -Eeuo pipefail\n          exit 0\n')
        self.assertNotEqual(text, early_success)
        with self.assertRaises(AssertionError):
            self.assert_protected_jobs(early_success)
        wrong_gate = text.replace("    environment: production\n", "    environment: production-plan\n    # environment: production\n")
        self.assertNotEqual(text, wrong_gate)
        with self.assertRaises(AssertionError):
            self.assert_protected_jobs(wrong_gate)


if __name__ == "__main__":
    unittest.main()
