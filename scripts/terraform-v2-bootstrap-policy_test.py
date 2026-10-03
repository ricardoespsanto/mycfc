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


def phase_a_empty_container():
    plan = phase_a_resumed()
    container = plan["resource_changes"][2]["change"]
    identity = container["after"] | {
        "arn": "arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-runtime-secrets-v2-sVoQ2n",
        "id": "arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-runtime-secrets-v2-sVoQ2n",
    }
    container.update(actions=["no-op"], before=identity.copy(), after=identity.copy())
    plan["resource_changes"][3]["change"].update(before=None)
    plan["resource_changes"][3]["change"]["after"]["secret_id"] = identity["id"]
    return plan


class BootstrapPolicyTest(unittest.TestCase):
    def test_resume_empty_managed_v2_container_creates_only_version(self):
        policy.validate("secret", phase_a_empty_container())

    def test_empty_container_metadata_preflight_requires_complete_empty_inventory(self):
        from types import SimpleNamespace
        response = {"ARN": policy.V2_RECOVERY_ARN, "Name": policy.V2_NAME, "Versions": []}
        with patch.object(policy.subprocess, "run", return_value=SimpleNamespace(
                returncode=0, stdout=json.dumps(response))) as run:
            policy.preflight_empty(phase_a_empty_container())
        self.assertEqual(run.call_args.args[0], [
            "aws", "secretsmanager", "list-secret-version-ids", "--secret-id", policy.V2_RECOVERY_ARN,
            "--include-deprecated", "--no-paginate", "--output", "json", "--no-cli-pager",
        ])
        for invalid in (response | {"Versions": [{"VersionId": "private-version", "VersionStages": []}]},
                        response | {"NextToken": "private-token"}, response | {"ARN": "other"},
                        response | {"Name": "other"}, {"Versions": []}, response | {"Versions": None}):
            with self.subTest(invalid=invalid), patch.object(policy.subprocess, "run", return_value=SimpleNamespace(
                    returncode=0, stdout=json.dumps(invalid))):
                with self.assertRaises(SystemExit) as caught:
                    policy.preflight_empty(phase_a_empty_container())
                self.assertNotIn("private-", str(caught.exception))

    def test_empty_container_preflight_fails_closed_on_aws_or_json_failure(self):
        from types import SimpleNamespace
        for result in (SimpleNamespace(returncode=1, stdout="private-error"),
                       SimpleNamespace(returncode=0, stdout="private-invalid-json"),
                       SimpleNamespace(returncode=0, stdout="[]")):
            with patch.object(policy.subprocess, "run", return_value=result):
                with self.assertRaises(SystemExit) as caught:
                    policy.preflight_empty(phase_a_empty_container())
                self.assertNotIn("private-", str(caught.exception))
        with patch.object(policy.subprocess, "run", side_effect=OSError("private-os-error")):
            with self.assertRaises(SystemExit):
                policy.preflight_empty(phase_a_empty_container())

    def test_empty_container_preflight_validates_plan_before_any_aws_read(self):
        plan = phase_a_empty_container()
        plan["resource_changes"][3]["change"]["actions"] = ["update"]
        with patch.object(policy.subprocess, "run") as run:
            with self.assertRaises(SystemExit):
                policy.preflight_empty(plan)
            run.assert_not_called()
        with patch.object(policy.subprocess, "run") as run:
            policy.preflight_empty(phase_a())
            run.assert_not_called()

    def test_empty_container_rejects_unknown_mutation_import_taint_and_wrong_identity(self):
        for invalid in ("before", "after", "unknown", "mutation", "import", "taint", "duplicate", "name", "arn", "id"):
            with self.subTest(invalid=invalid):
                plan = phase_a_empty_container()
                resource = plan["resource_changes"][2]
                item = resource["change"]
                if invalid in ("before", "after"):
                    item.pop(invalid)
                elif invalid == "unknown":
                    item["after_unknown"] = {"tags": {"private-tag": True}}
                elif invalid == "mutation":
                    item["after"]["description"] = "private-mutation"
                elif invalid == "import":
                    item["importing"] = {"id": policy.V2_RECOVERY_ARN}
                elif invalid == "taint":
                    resource["action_reason"] = "replace_because_tainted"
                elif invalid == "duplicate":
                    plan["resource_changes"].append(resource.copy())
                else:
                    for side in ("before", "after"):
                        item[side][invalid] = "private-wrong-identity"
                with self.assertRaises(SystemExit) as caught:
                    policy.validate("secret", plan)
                self.assertNotIn("private-", str(caught.exception))

    def test_empty_container_rejects_existing_or_unbound_version_and_extra_actions(self):
        for invalid in ("before", "secret_id", "unknown", "no-op", "update", "replace", "extra"):
            with self.subTest(invalid=invalid):
                plan = phase_a_empty_container()
                version = plan["resource_changes"][3]["change"]
                if invalid == "before":
                    version["before"] = {"version_id": "existing-private"}
                elif invalid == "secret_id":
                    version["after"]["secret_id"] = "other"
                elif invalid == "unknown":
                    version["after_unknown"] = {"secret_id": True}
                elif invalid == "extra":
                    plan["resource_changes"].append(change("aws_iam_user_policy.other", ["update"]))
                else:
                    version["actions"] = ["delete", "create"] if invalid == "replace" else [invalid]
                with self.assertRaises(SystemExit):
                    policy.validate("secret", plan)

    def test_empty_container_requires_completed_legacy_lineage(self):
        plan = phase_a_empty_container()
        for resource in plan["resource_changes"][:2]:
            resource["previous_address"] = policy.MOVES[resource["address"]]
        with self.assertRaises(SystemExit):
            policy.validate("secret", plan)

    def test_empty_metadata_cli_rejects_live_version_without_disclosing_response(self):
        import os
        import subprocess
        import sys
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            aws = root / "aws"
            aws.write_text('#!/usr/bin/env python3\nimport os, sys\nsys.stdout.write(os.environ["FIXTURE_METADATA"])\n', encoding="utf-8")
            aws.chmod(0o700)
            plan = phase_a_empty_container() | {
                "format_version": "1.2", "errored": False, "complete": False, "applyable": True,
            }
            path = root / "plan.json"
            path.write_text(json.dumps(plan), encoding="utf-8")
            metadata = {"ARN": policy.V2_RECOVERY_ARN, "Name": policy.V2_NAME, "Versions": []}
            env = os.environ | {"PATH": f"{directory}:{os.environ['PATH']}", "FIXTURE_METADATA": json.dumps(metadata)}
            command = [sys.executable, str(source), "preflight-empty", "secret", str(path)]
            success = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
            self.assertEqual(success.returncode, 0, success.stderr)
            self.assertIn("complete empty version inventory verified", success.stdout)
            metadata["Versions"] = [{"VersionId": "private-unlabeled-version", "VersionStages": []}]
            env["FIXTURE_METADATA"] = json.dumps(metadata)
            denied = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
            self.assertNotEqual(denied.returncode, 0)
            self.assertEqual(denied.stdout, "")
            self.assertNotIn("private-unlabeled-version", denied.stderr)
            self.assertNotIn(policy.V2_RECOVERY_ARN, success.stdout + denied.stderr)

    def test_empty_container_keeps_version_contract_and_configuration_binding(self):
        for invalid in ("stages", "fields", "references", "value-unknown", "kms"):
            with self.subTest(invalid=invalid):
                plan = phase_a_empty_container()
                version = plan["resource_changes"][3]["change"]
                if invalid == "stages":
                    version["after"]["version_stages"] = ["AWSCURRENT", "AWSPREVIOUS"]
                elif invalid == "fields":
                    version["after"]["secret_string"] = json.dumps({"APP_DB_PASSWORD": "fixture"})
                elif invalid == "references":
                    plan["configuration"]["root_module"]["resources"][0]["expressions"]["secret_id"]["references"] = [
                        "aws_secretsmanager_secret.legacy_runtime.id"]
                elif invalid == "value-unknown":
                    version["after_unknown"] = {"secret_string": True}
                else:
                    for side in ("before", "after"):
                        plan["resource_changes"][2]["change"][side]["kms_key_id"] = "other"
                with self.assertRaises(SystemExit):
                    policy.validate("secret", plan)
        plan = phase_a_empty_container()
        plan["resource_changes"][2]["change"]["after_unknown"] = {"tags": {}, "arn": False}
        plan["resource_changes"][3]["change"]["after_unknown"] = {"version_stages": [False]}
        policy.validate("secret", plan)

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

    def test_workflow_checks_empty_metadata_in_both_protected_jobs_before_apply(self):
        text = (source.parent.parent / ".github/workflows/terraform-production-v2-bootstrap.yml").read_text()
        plan_job, apply_job = text.split("\n  apply:", 1)
        command = 'python3 scripts/terraform-v2-bootstrap-policy.py preflight-empty secret "$TF_ROOT/production.tfplan.json"'
        for job in (plan_job, apply_job):
            self.assertEqual(job.count(command), 1)
            self.assertIn('if [[ "$REQUESTED_PHASE" == secret ]]; then', job[:job.index(command)])
            self.assertLess(job.index('terraform-v2-bootstrap-policy.py validate'), job.index(command))
        self.assertLess(plan_job.index(command), plan_job.index('>> "$GITHUB_OUTPUT"'))
        self.assertLess(apply_job.index('test "$EXPECTED_SEMANTIC_HMAC"'), apply_job.index(command))
        self.assertLess(apply_job.index(command), apply_job.index('if ! tf apply'))
        self.assertIn('terraform-v2-bootstrap-inspect.py continuity "$TF_ROOT/production.tfplan.json"', apply_job)

    def test_complete_residual_baseline_preserved_across_continuation_and_host_phase(self):
        manifest = json.loads(source.with_name("terraform-v2-bootstrap-residual.json").read_text())
        baseline = [change(address, actions) for address, actions in manifest["actions"]]
        before = {"resource_changes": baseline + phase_a_empty_container()["resource_changes"]}
        self.assertEqual(policy.residual("secret", before), policy.residual("secret", {"resource_changes": baseline}))
        host_baseline = [item for item in baseline if item["address"] != "aws_iam_user_policy.host_runtime"]
        host_before = {"resource_changes": host_baseline + phase_b()["resource_changes"]}
        self.assertEqual(policy.residual("host-policy", host_before),
                         policy.residual("host-policy", {"resource_changes": host_baseline}))
        # A changed non-target v2 version in phase B is not silently excluded.
        host_baseline.append(change(policy.V2_VERSION, ["update"]))
        with self.assertRaises(SystemExit):
            policy.residual("host-policy", {"resource_changes": host_baseline})

    def test_v2_field_inventory_is_exact(self):
        policy.validate_secret_values(json.dumps({key: "fixture" for key in policy.SECRET_FIELDS}))
        with self.assertRaises(SystemExit):
            policy.validate_secret_values(json.dumps({"APP_DB_PASSWORD": "fixture"}))


if __name__ == "__main__":
    unittest.main()
