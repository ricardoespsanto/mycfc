#!/usr/bin/env python3
"""Synthetic parity checks and source-level safety contracts."""

import importlib.util
import io
import os
from pathlib import Path
import subprocess
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
        self.assertIn("environment: production-plan", text)
        self.assertIn("environment: production\n", text)
        self.assertIn("needs: production_plan", text)
        self.assertIn("group: mycfc-production-infra", text)
        self.assertEqual(2, text.count("verify-exact-main-ci.py"))
        self.assertEqual(2, text.count("secrets.TF_PLAN_HMAC_KEY"))
        self.assertIn("EXPECTED_PARITY_PROBE: ${{ needs.production_plan.outputs.parity_probe }}", text)
        self.assertIn("ref: ${{ inputs.sha }}", text)
        self.assertIn("persist-credentials: false", text)
        for forbidden in ("id-token: write", "configure-aws-credentials", "terraform-stack.sh", "tf state", "tf untaint", "tf apply", "aws secretsmanager", "TF_VARS", "CLOUDFLARE_API_TOKEN", "pull_request", "upload-artifact", "contents: write"):
            self.assertNotIn(forbidden, text)


if __name__ == "__main__":
    unittest.main()
