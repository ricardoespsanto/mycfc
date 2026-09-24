#!/usr/bin/env python3
"""Value-redaction checks for the protected bootstrap inspection."""

import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest


source = pathlib.Path(__file__).with_name("terraform-v2-bootstrap-inspect.py")
spec = importlib.util.spec_from_file_location("bootstrap_inspect", source)
inspect = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inspect)


def plan_with_drift():
    resources = []
    for address in sorted(inspect.ROLE_ADDRESSES):
        resources.append({
            "address": address + "[0]", "change": {"actions": ["update"],
                "before": {"assume_role_policy": json.dumps({"Statement": [{
                    "Effect": "Allow", "Action": "sts:AssumeRole", "Principal": "secret-principal",
                }]}), "permissions_boundary": "secret-boundary"},
                "after": {"assume_role_policy": json.dumps({"Statement": []}),
                          "permissions_boundary": "secret-boundary"}},
        })
    resources.append({
        "address": inspect.SECRET_VERSION_ADDRESS,
        "change": {"actions": ["update"],
                   "before": {"version_id": "secret-old-version", "version_stages": ["AWSCURRENT"],
                              "secret_string": "secret-old-value"},
                   "after": {"version_id": "secret-new-version", "version_stages": ["AWSPREVIOUS"],
                             "secret_string": "secret-new-value"}},
    })
    return {"format_version": "1.2", "errored": False, "resource_drift": resources}


class BootstrapInspectTest(unittest.TestCase):
    def test_drift_reports_only_allowlisted_metadata(self):
        report = inspect.inspect_drift(plan_with_drift())
        self.assertIn("`assume_role_policy`", report)
        self.assertIn("state yes; live no", report)
        self.assertIn("AWSCURRENT stage present: state True; live False", report)
        self.assertIn("Version ID changed: True", report)
        for secret in ("secret-principal", "secret-boundary", "secret-old-version",
                       "secret-new-version", "secret-old-value", "secret-new-value"):
            self.assertNotIn(secret, report)

    def test_unexpected_or_injected_drift_fails_closed(self):
        plan = plan_with_drift()
        plan["resource_drift"][0]["address"] = "aws_iam_role.other[0]"
        with self.assertRaises(SystemExit):
            inspect.inspect_drift(plan)
        plan = plan_with_drift()
        plan["resource_drift"][0]["address"] += "\n::warning::injected"
        with self.assertRaises(SystemExit) as caught:
            inspect.inspect_drift(plan)
        self.assertNotIn("injected", str(caught.exception))

    def test_exact_secret_continuity_reports_only_field_names(self):
        proposed = {key: "same" for key in inspect.SECRET_FIELDS}
        plan = {"resource_changes": [{
            "address": "aws_secretsmanager_secret_version.app_runtime",
            "change": {"after": {"secret_string": json.dumps(proposed)}, "after_unknown": {}},
        }]}
        self.assertTrue(inspect.compare_secret(plan, json.dumps(proposed)))

        live = proposed.copy()
        live["POLAR_CLIENT_SECRET"] = "secret-different"
        del live["ACTIVITY_CREDENTIAL_KEYS_JSON"]
        self.assertFalse(inspect.compare_secret(plan, json.dumps(live)))

    def test_secret_parse_failure_never_echoes_values(self):
        plan = {"resource_changes": [{
            "address": "aws_secretsmanager_secret_version.app_runtime",
            "change": {"after": {"secret_string": "not-json-secret"}, "after_unknown": {}},
        }]}
        with self.assertRaises(SystemExit) as caught:
            inspect.compare_secret(plan, "live-secret")
        self.assertNotIn("not-json-secret", str(caught.exception))
        self.assertNotIn("live-secret", str(caught.exception))

    def test_cli_mismatch_exits_without_printing_values(self):
        proposed = {key: "private-proposed-value" for key in inspect.SECRET_FIELDS}
        live = proposed.copy()
        live["APP_DB_PASSWORD"] = "private-live-value"
        plan = {"format_version": "1.2", "errored": False, "resource_changes": [{
            "address": "aws_secretsmanager_secret_version.app_runtime",
            "change": {"after": {"secret_string": json.dumps(proposed)}, "after_unknown": {}},
        }]}
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            result = subprocess.run(
                ["python3", str(source), "continuity", str(path)],
                input=json.dumps(live), text=True, capture_output=True, check=False,
            )
        self.assertEqual(result.returncode, 1)
        self.assertIn("All nine v2 runtime fields match live legacy values: **no**", result.stdout)
        self.assertNotIn("private-proposed-value", result.stdout + result.stderr)
        self.assertNotIn("private-live-value", result.stdout + result.stderr)

    def test_cli_rejects_malformed_plan_without_echo(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            path.write_text("not-json-private-value", encoding="utf-8")
            result = subprocess.run(
                ["python3", str(source), "drift", str(path)],
                text=True, capture_output=True, check=False,
            )
        self.assertEqual(result.returncode, 1)
        self.assertNotIn("not-json-private-value", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
