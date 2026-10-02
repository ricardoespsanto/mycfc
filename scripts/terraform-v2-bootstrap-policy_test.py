#!/usr/bin/env python3
"""Focused fixtures for the protected v2 bootstrap plan gate."""

import importlib.util
import json
import pathlib
import re
import tempfile
import unittest
from unittest.mock import patch

source = pathlib.Path(__file__).with_name("terraform-v2-bootstrap-policy.py")
spec = importlib.util.spec_from_file_location("bootstrap_policy", source)
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


def change(address, actions, previous=None):
    result = {
        "address": address,
        "mode": "managed",
        "change": {"actions": actions},
    }
    if previous:
        result["previous_address"] = previous
    return result


def phase_a():
    plan = {
        "configuration": {"root_module": {"resources": [{
            "address": "aws_secretsmanager_secret_version.app_runtime",
            "mode": "managed",
            "expressions": {"secret_id": {"references": [
                "aws_secretsmanager_secret.app_runtime.id",
                "aws_secretsmanager_secret.app_runtime",
            ]}},
        }]}},
        "resource_changes": [
            change("aws_secretsmanager_secret.legacy_runtime", ["no-op"], "aws_secretsmanager_secret.runtime"),
            change("aws_secretsmanager_secret_version.legacy_runtime", ["no-op"], "aws_secretsmanager_secret_version.runtime"),
            change("aws_secretsmanager_secret.app_runtime", ["create"]),
            change("aws_secretsmanager_secret_version.app_runtime", ["create"]),
        ]
    }
    plan["resource_changes"][2]["change"]["after"] = {
        "name": "/mycfc/production/app-runtime-secrets-v2",
        "description": "MyCFC production web-runtime secrets v2",
        "recovery_window_in_days": 30,
        "kms_key_id": None,
        "force_overwrite_replica_secret": False,
    }
    plan["resource_changes"][3]["change"]["after"] = {
        "secret_string": json.dumps({key: "fixture" for key in policy.SECRET_FIELDS}),
        "version_stages": ["AWSCURRENT"],
    }
    return plan


def phase_b():
    old_arn = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:/mycfc/production/app-secrets-old"
    new_arn = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:/mycfc/production/app-runtime-secrets-v2-new"
    def document(arns):
        return json.dumps({"Version": "2012-10-17", "Statement": [
            {"Sid": "ReadRuntimeSecret", "Effect": "Allow",
             "Action": "secretsmanager:GetSecretValue", "Resource": arns},
            {"Sid": "ReadRuntimeParameters", "Effect": "Allow",
             "Action": "ssm:GetParameter", "Resource": "*"},
        ]})
    host = change("aws_iam_user_policy.host_runtime", ["update"])
    host["change"]["before"] = {"policy": document(old_arn)}
    host["change"]["after"] = {"policy": document([old_arn, new_arn])}
    old = change("aws_secretsmanager_secret.legacy_runtime", ["no-op"])
    old["change"]["after"] = {"arn": old_arn}
    new = change("aws_secretsmanager_secret.app_runtime", ["no-op"])
    new["change"]["after"] = {"arn": new_arn}
    return {"resource_changes": [host, old, new]}


def phase_a_resumed():
    plan = phase_a()
    arn = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:/mycfc/production/app-secrets-Ab12Cd"
    version_id = "11111111-2222-3333-4444-555555555555"
    identities = [
        {"name": "/mycfc/production/app-secrets", "arn": arn, "id": arn},
        {"secret_id": arn, "version_id": version_id, "id": f"{arn}|{version_id}",
         "secret_string": "legacy-fixture-do-not-log", "version_stages": ["AWSCURRENT"]},
    ]
    for resource, identity in zip(plan["resource_changes"], identities):
        resource.pop("previous_address")
        resource["change"].update(before=identity.copy(), after=identity.copy())
    return plan


class BootstrapPolicyTest(unittest.TestCase):
    def test_phase_a_requires_two_creates_and_both_noop_moves(self):
        policy.validate("secret", phase_a())

    def test_phase_a_resumes_after_both_lineage_moves_committed(self):
        policy.validate("secret", phase_a_resumed())

    def test_resume_rejects_partial_or_missing_lineage(self):
        for index in (0, 1):
            for partial_move in (True, False):
                with self.subTest(index=index, partial_move=partial_move):
                    plan = phase_a_resumed()
                    if partial_move:
                        resource = plan["resource_changes"][index]
                        resource["previous_address"] = policy.MOVES[resource["address"]]
                    else:
                        plan["resource_changes"].pop(index)
                    with self.assertRaises(SystemExit):
                        policy.validate("secret", plan)

    def test_resume_rejects_old_duplicate_or_conflicting_lineage(self):
        for index in (0, 1):
            for conflict in ("old", "duplicate", "previous", "data"):
                with self.subTest(index=index, conflict=conflict):
                    plan = phase_a_resumed()
                    resource = plan["resource_changes"][index]
                    if conflict == "old":
                        plan["resource_changes"].append(change(policy.MOVES[resource["address"]], ["no-op"]))
                    elif conflict == "duplicate":
                        plan["resource_changes"].append(resource.copy())
                    elif conflict == "previous":
                        resource["previous_address"] = "aws_secretsmanager_secret.other"
                    else:
                        resource["mode"] = "data"
                    with self.assertRaises(SystemExit):
                        policy.validate("secret", plan)

    def test_resume_rejects_legacy_mutation_or_import(self):
        for index in (0, 1):
            for actions in (["update"], ["delete"], ["delete", "create"], ["create"], ["read"], ["forget"]):
                with self.subTest(index=index, actions=actions):
                    plan = phase_a_resumed()
                    plan["resource_changes"][index]["change"]["actions"] = actions
                    with self.assertRaises(SystemExit):
                        policy.validate("secret", plan)
            plan = phase_a_resumed()
            plan["resource_changes"][index]["change"]["importing"] = {"id": "fixture"}
            with self.assertRaises(SystemExit):
                policy.validate("secret", plan)

    def test_resume_rejects_missing_changed_or_unknown_legacy_values_privately(self):
        for index in (0, 1):
            for invalid in ("before", "after", "changed", "unknown", "nested-unknown"):
                with self.subTest(index=index, invalid=invalid):
                    plan = phase_a_resumed()
                    legacy = plan["resource_changes"][index]["change"]
                    if invalid in ("before", "after"):
                        legacy.pop(invalid)
                    elif invalid == "changed":
                        legacy["after"]["secret_string"] = "changed-do-not-log"
                    else:
                        legacy["after_unknown"] = {"identity": True if invalid == "unknown" else [False, True]}
                    with self.assertRaises(SystemExit) as caught:
                        policy.validate("secret", plan)
                    for private in ("legacy-fixture-do-not-log", "changed-do-not-log", "Ab12Cd"):
                        self.assertNotIn(private, str(caught.exception))

    def test_resume_rejects_wrong_legacy_identity_even_when_unchanged(self):
        for index, field, value in (
            (0, "name", "/mycfc/production/app-runtime-secrets-v2"),
            (0, "arn", "arn:aws:secretsmanager:eu-west-1:123456789012:secret:/mycfc/production/other-Ab12Cd"),
            (0, "id", "different-container"),
            (1, "secret_id", "different-container"),
            (1, "version_id", ""),
            (1, "id", "different-version"),
        ):
            with self.subTest(index=index, field=field):
                plan = phase_a_resumed()
                for side in ("before", "after"):
                    plan["resource_changes"][index]["change"][side][field] = value
                with self.assertRaises(SystemExit):
                    policy.validate("secret", plan)

    def test_resume_accepts_provider_known_false_trees(self):
        plan = phase_a_resumed()
        for resource in plan["resource_changes"][:2]:
            resource["change"]["after_unknown"] = {"id": False, "version_stages": [False], "tags": {}}
        policy.validate("secret", plan)

    def test_resume_still_requires_exact_two_v2_creates(self):
        for index in (2, 3):
            plan = phase_a_resumed()
            plan["resource_changes"][index]["change"]["actions"] = ["no-op"]
            with self.assertRaises(SystemExit):
                policy.validate("secret", plan)

    def test_phase_b_requires_only_host_policy_update(self):
        policy.validate("host-policy", phase_b())

    def test_phase_b_rejects_legacy_deny(self):
        plan = phase_b()
        after = json.loads(plan["resource_changes"][0]["change"]["after"]["policy"])
        after["Statement"].append({
            "Sid": "DenyLegacy", "Effect": "Deny",
            "Action": "secretsmanager:GetSecretValue", "Resource": "*",
        })
        plan["resource_changes"][0]["change"]["after"]["policy"] = json.dumps(after)
        with self.assertRaises(SystemExit):
            policy.validate("host-policy", plan)

    def test_legacy_update_rejected(self):
        plan = phase_a()
        plan["resource_changes"][0]["change"]["actions"] = ["update"]
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_missing_move_rejected(self):
        plan = phase_a()
        plan["resource_changes"].pop(0)
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_wrong_or_duplicate_move_rejected(self):
        plan = phase_a()
        plan["resource_changes"][0]["previous_address"] = "aws_secretsmanager_secret.other"
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)
        plan = phase_a()
        plan["resource_changes"].append(plan["resource_changes"][0].copy())
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_collateral_change_rejected(self):
        plan = phase_a()
        plan["resource_changes"].append(change("aws_s3_bucket.privacy_activation_audit[0]", ["delete"]))
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_import_rejected(self):
        plan = phase_a()
        plan["resource_changes"][2]["change"]["importing"] = {"id": "secret"}
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_version_cannot_point_to_legacy_secret(self):
        plan = phase_a()
        plan["configuration"]["root_module"]["resources"][0]["expressions"]["secret_id"]["references"] = [
            "aws_secretsmanager_secret.legacy_runtime.id",
            "aws_secretsmanager_secret.legacy_runtime",
        ]
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_create_cannot_change_kms_or_version_stages(self):
        plan = phase_a()
        plan["resource_changes"][2]["change"]["after"]["kms_key_id"] = "unreviewed-key"
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)
        plan = phase_a()
        plan["resource_changes"][3]["change"]["after"]["version_stages"] = ["AWSPENDING"]
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_version_stage_known_elements_from_provider_plan(self):
        # AWS provider 6.56.0 represents a known list element as [false]
        # in after_unknown; the list itself is truthy in Python.
        plan = phase_a()
        version = plan["resource_changes"][3]["change"]
        version["after_unknown"] = {"version_stages": [False]}
        policy.validate("secret", plan)
        for unknown in ([True], [False, True], True, [0], {"0": False}):
            with self.subTest(unknown=unknown):
                version["after_unknown"]["version_stages"] = unknown
                with self.assertRaises(SystemExit):
                    policy.validate("secret", plan)

    def test_residual_ignores_only_phase_targets(self):
        manifest = json.loads(source.with_name("terraform-v2-bootstrap-residual.json").read_text())
        baseline = [change(address, actions) for address, actions in manifest["actions"]]
        before = {"resource_changes": baseline + phase_a()["resource_changes"]}
        after = {"resource_changes": baseline}
        self.assertEqual(policy.residual("secret", before), policy.residual("secret", after))
        after["resource_changes"].append(change("aws_iam_policy.other", ["delete"]))
        with self.assertRaises(SystemExit):
            policy.residual("secret", after)

    def test_phase_b_rejects_unrelated_resource_attribute_change(self):
        plan = phase_b()
        plan["resource_changes"][0]["change"]["after"]["name"] = "changed"
        with self.assertRaises(SystemExit):
            policy.validate("host-policy", plan)

    def test_load_rejects_outputs_deferrals_and_drift(self):
        base = {
            "format_version": "1.2", "errored": False,
            "complete": False, "applyable": True,
            "configuration": {"root_module": {"resources": []}},
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            for addition in (
                {"output_changes": {"runtime_secret_arn": {"actions": ["update"]}}},
                {"deferred_changes": [{"reason": "unknown"}]},
                {"resource_drift": [change("aws_s3_bucket.other", ["update"])]},
                {"applyable": False},
            ):
                path.write_text(json.dumps(base | addition), encoding="utf-8")
                with self.assertRaises(SystemExit):
                    policy.load(str(path), targeted=True)

    def test_drift_failure_reports_only_address_and_actions(self):
        plan = {
            "format_version": "1.2", "errored": False,
            "complete": True, "applyable": True,
            "configuration": {"root_module": {"resources": []}},
            "resource_drift": [{
                "address": 'module.runtime["secret-instance-key"].aws_secretsmanager_secret_version.app_runtime',
                "change": {"actions": ["update"], "before": {"secret_string": "secret-before"},
                           "after": {"secret_string": "secret-after"}},
            }],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            with self.assertRaises(SystemExit) as caught:
                policy.load(str(path), targeted=False)
        message = str(caught.exception)
        self.assertIn('module.runtime.aws_secretsmanager_secret_version.app_runtime', message)
        self.assertIn('"actions":["update"]', message)
        for secret in ("secret-instance-key", "secret-before", "secret-after"):
            self.assertNotIn(secret, message)

    def test_drift_failure_rejects_log_injection(self):
        plan = {
            "format_version": "1.2", "errored": False,
            "complete": True, "applyable": True,
            "configuration": {"root_module": {"resources": []}},
            "resource_drift": [{
                "address": "aws_s3_bucket.other\n::warning::injected",
                "change": {"actions": ["update"], "before": {"secret": "do-not-print"}},
            }],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            with self.assertRaises(SystemExit) as caught:
                policy.load(str(path), targeted=False)
        self.assertEqual(str(caught.exception),
                         "v2 bootstrap policy: resource drift is not permitted (invalid drift entry)")

    def test_drift_failure_rejects_oversized_inventory(self):
        plan = {
            "format_version": "1.2", "errored": False,
            "complete": True, "applyable": True,
            "configuration": {"root_module": {"resources": []}},
            "resource_drift": [change("aws_s3_bucket.other", ["update"])] * 101,
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            with self.assertRaises(SystemExit) as caught:
                policy.load(str(path), targeted=False)
        self.assertEqual(str(caught.exception),
                         "v2 bootstrap policy: resource drift is not permitted (invalid drift inventory)")

    def test_target_manifest_is_bound_to_phase_and_backend(self):
        environment = {
            "TF_PLAN_HMAC_KEY": "a" * 64,
            "TF_BACKEND_BUCKET": "reviewed-state-bucket",
            "TF_BACKEND_KEY": "mycfc/production/terraform.tfstate",
            "AWS_REGION": "eu-west-1",
        }
        with patch.dict("os.environ", environment):
            digest = policy.manifest_hmac("secret", policy.PHASE_TARGETS["secret"])
            self.assertEqual(len(digest), 64)
            with self.assertRaises(SystemExit):
                policy.manifest_hmac("secret", policy.PHASE_TARGETS["host-policy"])
            self.assertNotEqual(digest, policy.manifest_hmac(
                "host-policy", policy.PHASE_TARGETS["host-policy"]))

    def test_workflow_plan_and_apply_keep_fixed_lineage_targets(self):
        workflow = source.parent.parent / ".github/workflows/terraform-production-v2-bootstrap.yml"
        text = workflow.read_text(encoding="utf-8")
        blocks = re.findall(r"targets=\((.*?)\)", text, re.DOTALL)
        self.assertEqual([block.split() for block in blocks], [
            policy.PHASE_TARGETS["secret"], policy.PHASE_TARGETS["host-policy"],
            policy.PHASE_TARGETS["secret"], policy.PHASE_TARGETS["host-policy"],
        ])

    def test_v2_field_inventory_is_exact(self):
        policy.validate_secret_values(json.dumps({key: "fixture" for key in policy.SECRET_FIELDS}))
        with self.assertRaises(SystemExit):
            policy.validate_secret_values(json.dumps({"APP_DB_PASSWORD": "fixture"}))


if __name__ == "__main__":
    unittest.main()
