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

    def test_additional_role_fields_report_names_without_values(self):
        plan = plan_with_drift()
        before = plan["resource_drift"][0]["change"]["before"]
        after = plan["resource_drift"][0]["change"]["after"]
        before["inline_policy"] = [{"name": "private-old-inline-policy", "policy": "{}"}]
        after["inline_policy"] = [{"name": "private-new-inline-policy", "policy": "{}"}]
        for field in ("managed_policy_arns", "name_prefix", "role_last_used"):
            before[field] = "private-old-" + field
            after[field] = "private-new-" + field
        report = inspect.inspect_drift(plan)
        for field in ("inline_policy", "managed_policy_arns", "name_prefix", "role_last_used"):
            self.assertIn(f"`{field}`", report)
            self.assertNotIn("private-old-" + field, report)
            self.assertNotIn("private-new-" + field, report)

    def test_unknown_role_field_remains_unclassified_without_echo(self):
        plan = plan_with_drift()
        plan["resource_drift"][0]["change"]["after"]["private-injected-name"] = "private-value"
        report = inspect.inspect_drift(plan)
        self.assertIn("`other-unclassified`", report)
        self.assertNotIn("private-injected-name", report)
        self.assertNotIn("private-value", report)

    def test_inline_policy_direction_reports_only_aggregate_counts(self):
        plan = plan_with_drift()
        change = plan["resource_drift"][0]["change"]
        change["before"]["inline_policy"] = [
            {"name": "private-removed-name", "policy": '{"Statement":[{"Action":"private-removed-action"}]}'},
            {"name": "private-changed-name", "policy": '{"Statement":[{"Action":"private-old-action"}]}'},
            {"name": "private-unchanged-name", "policy": '{"Statement":[]}'},
        ]
        change["after"]["inline_policy"] = [
            {"name": "private-changed-name", "policy": '{"Statement":[{"Action":"private-new-action"}]}'},
            {"name": "private-unchanged-name", "policy": '{"Statement":[]}'},
            {"name": "private-added-name", "policy": '{"Statement":[]}'},
        ]
        report = inspect.inspect_drift(plan)
        self.assertIn("state 3; live 3; removed 1; added 1; "
                      "changed on shared names 1; unchanged on shared names 1", report)
        for private in ("private-removed-name", "private-changed-name", "private-unchanged-name",
                        "private-added-name", "private-old-action", "private-new-action",
                        "private-removed-action"):
            self.assertNotIn(private, report)

    def test_inline_policy_shape_failure_never_echoes_private_data(self):
        for invalid in ([{"name": "private-name", "policy": "private-invalid-json"}],
                        [{"name": "private-name", "policy": "{}"},
                         {"name": "private-name", "policy": "{}"}],
                        [{"name": "private-name", "policy": "{}", "private-extra": "private-value"}]):
            plan = plan_with_drift()
            plan["resource_drift"][0]["change"]["before"]["inline_policy"] = invalid
            plan["resource_drift"][0]["change"]["after"]["inline_policy"] = []
            with self.assertRaises(SystemExit) as caught:
                inspect.inspect_drift(plan)
            self.assertNotIn("private-", str(caught.exception))

    def test_inline_policy_empty_and_json_format_only_difference(self):
        self.assertIn("state 0; live 0; removed 0; added 0",
                      inspect.inline_policy_summary(None, []))
        before = [{"name": "private-name", "policy": '{"Version":"2012-10-17","Statement":[]}'}]
        after = [{"name": "private-name", "policy": '{ "Statement": [], "Version": "2012-10-17" }'}]
        self.assertIn("changed on shared names 0; unchanged on shared names 1",
                      inspect.inline_policy_summary(before, after))

    def test_secret_stages_report_only_allowlisted_labels(self):
        plan = plan_with_drift()
        plan["resource_drift"][-1]["change"]["before"]["version_stages"] = ["AWSCURRENT", "private-stage"]
        plan["resource_drift"][-1]["change"]["after"]["version_stages"] = ["AWSPREVIOUS", "private-stage"]
        report = inspect.inspect_drift(plan)
        self.assertIn("AWSPREVIOUS stage present: state False; live True", report)
        self.assertIn("Other stage counts (names withheld): state 1; live 1", report)
        self.assertNotIn("private-stage", report)

    def test_invalid_secret_stages_fail_without_echo(self):
        for invalid in (["private-stage", "private-stage"], ["AWSCURRENT", 12], "private-stage"):
            plan = plan_with_drift()
            plan["resource_drift"][-1]["change"]["after"]["version_stages"] = invalid
            with self.assertRaises(SystemExit) as caught:
                inspect.inspect_drift(plan)
            self.assertNotIn("private-stage", str(caught.exception))

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
