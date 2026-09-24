#!/usr/bin/env python3
"""Fail-closed policy for the one-time, targeted production v2 secret bootstrap."""

import hashlib
import hmac
import json
import os
import re
import sys
from pathlib import Path

MOVES = {
    "aws_secretsmanager_secret.legacy_runtime": "aws_secretsmanager_secret.runtime",
    "aws_secretsmanager_secret_version.legacy_runtime": "aws_secretsmanager_secret_version.runtime",
}
SECRET_FIELDS = {
    "APP_DB_PASSWORD", "CSRF_AUTH_KEY_B64", "EMAIL_VERIFICATION_HMAC_KEY_B64",
    "TURNSTILE_SECRET_KEY", "SMTP_USERNAME", "SMTP_PASSWORD",
    "GOOGLE_CALENDAR_API_KEY", "POLAR_CLIENT_SECRET", "ACTIVITY_CREDENTIAL_KEYS_JSON",
}
REQUIRED_SECRET_FIELDS = {
    "APP_DB_PASSWORD", "CSRF_AUTH_KEY_B64", "EMAIL_VERIFICATION_HMAC_KEY_B64",
    "TURNSTILE_SECRET_KEY", "SMTP_USERNAME", "SMTP_PASSWORD",
    "POLAR_CLIENT_SECRET", "ACTIVITY_CREDENTIAL_KEYS_JSON",
}
PHASE_CHANGES = {
    "secret": {
        "aws_secretsmanager_secret.app_runtime": ["create"],
        "aws_secretsmanager_secret_version.app_runtime": ["create"],
    },
    "host-policy": {"aws_iam_user_policy.host_runtime": ["update"]},
}
PHASE_TARGETS = {
    "secret": [
        "-target=aws_secretsmanager_secret.runtime",
        "-target=aws_secretsmanager_secret.legacy_runtime",
        "-target=aws_secretsmanager_secret_version.runtime",
        "-target=aws_secretsmanager_secret_version.legacy_runtime",
        "-target=aws_secretsmanager_secret.app_runtime",
        "-target=aws_secretsmanager_secret_version.app_runtime",
    ],
    "host-policy": ["-target=aws_iam_user_policy.host_runtime"],
}


def fail(message: str) -> None:
    raise SystemExit(f"v2 bootstrap policy: {message}")


def resources(module: object):
    if not isinstance(module, dict):
        fail("missing root module configuration")
    for resource in module.get("resources", []):
        if not isinstance(resource, dict):
            fail("invalid resource configuration")
        yield resource
    for child in module.get("module_calls", {}).values():
        if not isinstance(child, dict):
            fail("invalid child module configuration")
        yield from resources(child.get("module"))


def load(path: str, targeted: bool) -> dict:
    with Path(path).open(encoding="utf-8") as source:
        plan = json.load(source)
    if not isinstance(plan, dict):
        fail("plan JSON is not an object")
    if plan.get("format_version") != "1.2" or plan.get("errored") is not False:
        fail("unexpected plan format or errored plan")
    if plan.get("complete") is not (not targeted):
        fail("unexpected plan completeness")
    if plan.get("applyable") is not True:
        fail("expected an applyable plan")
    for field in ("deferred_changes", "action_invocations", "deferred_action_invocations"):
        if plan.get(field) not in (None, []):
            fail(f"{field} is not empty")
    configuration = plan.get("configuration")
    if not isinstance(configuration, dict):
        fail("configuration missing")
    if any(resource.get("provisioners") for resource in resources(configuration.get("root_module"))):
        fail("provisioners are not permitted")
    drift = plan.get("resource_drift")
    if drift not in (None, []):
        # Drift is a hard stop. Only report the resource address without its
        # instance key and the action names; before/after can contain secrets.
        if not isinstance(drift, list) or len(drift) > 100:
            fail("resource drift is not permitted (invalid drift inventory)")
        summaries = []
        for resource in drift:
            if not isinstance(resource, dict):
                fail("resource drift is not permitted (invalid drift entry)")
            address = resource.get("address")
            change = resource.get("change")
            actions = change.get("actions") if isinstance(change, dict) else None
            if not isinstance(address, str):
                fail("resource drift is not permitted (invalid drift entry)")
            address_without_keys = re.sub(r"\[[^\[\]\r\n]*\]", "", address)
            if (not re.fullmatch(r"[A-Za-z0-9_.-]+", address_without_keys)
                    or not isinstance(actions, list)
                    or not actions or any(action not in ("no-op", "create", "read", "update", "delete")
                                           for action in actions)):
                fail("resource drift is not permitted (invalid drift entry)")
            summaries.append({"address": address_without_keys, "actions": actions})
        fail("resource drift is not permitted; address/action summary: "
             + json.dumps(summaries, sort_keys=True, separators=(",", ":")))
    if targeted:
        for name, output in plan.get("output_changes", {}).items():
            if not isinstance(output, dict) or output.get("actions") != ["no-op"]:
                fail(f"output change is forbidden: {name}")
    return plan


def validate_secret_values(secret_string: object) -> None:
    if not isinstance(secret_string, str):
        fail("v2 secret value must be known")
    try:
        secret = json.loads(secret_string)
    except json.JSONDecodeError:
        fail("v2 secret value is not JSON")
    if not isinstance(secret, dict) or set(secret) != SECRET_FIELDS:
        fail("v2 secret field inventory differs from web-runtime contract")
    if any(not isinstance(secret.get(key), str) or not secret[key].strip()
           for key in REQUIRED_SECRET_FIELDS):
        fail("required v2 runtime secret field is empty")


def manifest_hmac(phase: str, targets: list[str]) -> str:
    if targets != PHASE_TARGETS[phase]:
        fail("actual target list differs from the fixed phase manifest")
    key_text = os.environ.get("TF_PLAN_HMAC_KEY", "")
    if not re.fullmatch(r"[0-9a-f]{64}", key_text):
        fail("invalid HMAC key configuration")
    backend = {
        "bucket": os.environ.get("TF_BACKEND_BUCKET", ""),
        "key": os.environ.get("TF_BACKEND_KEY", ""),
        "region": os.environ.get("AWS_REGION", ""),
    }
    if not all(backend.values()):
        fail("backend identity incomplete")
    payload = json.dumps(
        {"purpose": "mycfc-v2-bootstrap-targets-v1", "backend": backend,
         "phase": phase, "targets": targets},
        sort_keys=True, separators=(",", ":"),
    ).encode()
    return hmac.new(bytes.fromhex(key_text), payload, hashlib.sha256).hexdigest()


def validate(phase: str, plan: dict) -> None:
    expected = PHASE_CHANGES[phase]
    changed = {}
    seen_moves = {}
    for resource in plan.get("resource_changes", []):
        if not isinstance(resource, dict):
            fail("invalid resource change")
        address = resource.get("address")
        change = resource.get("change")
        if not isinstance(address, str) or not isinstance(change, dict):
            fail("resource change lacks address or change")
        if change.get("importing") is not None:
            fail(f"import is forbidden: {address}")
        actions = change.get("actions")
        if not isinstance(actions, list):
            fail(f"invalid actions: {address}")
        previous = resource.get("previous_address")
        if previous is not None:
            if phase != "secret" or MOVES.get(address) != previous:
                fail(f"unexpected state move: {address}")
            if address in seen_moves:
                fail(f"duplicate state move: {address}")
            if actions != ["no-op"]:
                fail(f"legacy move would mutate AWS: {address}")
            seen_moves[address] = previous
        elif address in MOVES and phase == "secret" and actions != ["no-op"]:
            fail(f"legacy resource would mutate AWS: {address}")
        if resource.get("mode") == "data":
            if actions not in (["no-op"], ["read"]):
                fail(f"data source mutation: {address}")
            continue
        if resource.get("mode") != "managed":
            fail(f"unexpected resource mode: {address}")
        if actions == ["no-op"]:
            continue
        if address in changed:
            fail(f"duplicate changed address: {address}")
        changed[address] = actions
    if changed != expected:
        fail(f"changed addresses/actions differ from the fixed {phase} manifest")
    if phase == "secret" and seen_moves != MOVES:
        fail("both exact no-op legacy secret state moves are required")
    if phase == "host-policy" and seen_moves:
        fail("legacy state moves must have completed in phase A")
    if phase == "secret":
        configuration = plan.get("configuration", {}).get("root_module")
        version_blocks = [
            resource for resource in resources(configuration)
            if resource.get("address") == "aws_secretsmanager_secret_version.app_runtime"
            and resource.get("mode") == "managed"
        ]
        if len(version_blocks) != 1:
            fail("exactly one v2 secret-version configuration is required")
        references = version_blocks[0].get("expressions", {}).get("secret_id", {}).get("references")
        allowed_references = {
            "aws_secretsmanager_secret.app_runtime.id",
            "aws_secretsmanager_secret.app_runtime",
        }
        if (not isinstance(references, list)
                or "aws_secretsmanager_secret.app_runtime.id" not in references
                or not set(references) <= allowed_references):
            fail("v2 secret version must reference only the new v2 secret ID")
        container = next(resource for resource in plan["resource_changes"]
                         if resource["address"] == "aws_secretsmanager_secret.app_runtime")
        version = next(resource for resource in plan["resource_changes"]
                       if resource["address"] == "aws_secretsmanager_secret_version.app_runtime")
        if container["change"].get("after", {}).get("name") != "/mycfc/production/app-runtime-secrets-v2":
            fail("unexpected v2 secret name")
        container_after = container["change"].get("after", {})
        container_unknown = container["change"].get("after_unknown", {})
        expected_container = {
            "description": "MyCFC production web-runtime secrets v2",
            "recovery_window_in_days": 30,
            "kms_key_id": None,
            "force_overwrite_replica_secret": False,
        }
        for attribute, expected_value in expected_container.items():
            if container_unknown.get(attribute) or container_after.get(attribute) != expected_value:
                fail(f"unexpected v2 secret {attribute}")
        if container_after.get("replica") not in (None, []):
            fail("v2 secret replicas are not allowed in bootstrap")
        if container_after.get("name_prefix") not in (None, ""):
            fail("v2 secret name prefix is not allowed")
        version_after = version["change"].get("after", {})
        version_unknown = version["change"].get("after_unknown", {})
        if version_unknown.get("version_stages") or version_after.get("version_stages") != ["AWSCURRENT"]:
            fail("v2 secret version must be AWSCURRENT only")
        for attribute in ("secret_binary", "secret_string_wo", "secret_string_wo_version"):
            if version_unknown.get(attribute) or version_after.get(attribute) not in (None, ""):
                fail(f"unexpected v2 secret version {attribute}")
        secret_string = version["change"].get("after", {}).get("secret_string")
        if version["change"].get("after_unknown", {}).get("secret_string"):
            fail("v2 secret value must be fully known at approval")
        validate_secret_values(secret_string)
    else:
        host = next(resource for resource in plan["resource_changes"]
                    if resource["address"] == "aws_iam_user_policy.host_runtime")
        old = next((resource for resource in plan["resource_changes"]
                    if resource["address"] == "aws_secretsmanager_secret.legacy_runtime"), None)
        new = next((resource for resource in plan["resource_changes"]
                    if resource["address"] == "aws_secretsmanager_secret.app_runtime"), None)
        old_arn = old.get("change", {}).get("after", {}).get("arn") if old else None
        new_arn = new.get("change", {}).get("after", {}).get("arn") if new else None
        if not all(isinstance(arn, str) and arn for arn in (old_arn, new_arn)):
            fail("both runtime secret ARNs must be known in phase B")
        try:
            before = json.loads(host["change"]["before"]["policy"])
            after = json.loads(host["change"]["after"]["policy"])
        except (KeyError, TypeError, json.JSONDecodeError):
            fail("host policy before/after must be known JSON")
        if not isinstance(before, dict) or not isinstance(after, dict):
            fail("host policy JSON must be objects")
        before_attrs = {key: value for key, value in host["change"]["before"].items() if key != "policy"}
        after_attrs = {key: value for key, value in host["change"]["after"].items() if key != "policy"}
        if before_attrs != after_attrs:
            fail("host policy resource attributes changed beyond policy JSON")
        before_statements = before.get("Statement")
        after_statements = after.get("Statement")
        if not isinstance(before_statements, list) or not isinstance(after_statements, list):
            fail("host policy statements missing")
        def secret_grant(statements):
            matches = [item for item in statements if item.get("Sid") == "ReadRuntimeSecret"]
            if len(matches) != 1:
                fail("expected one runtime secret grant")
            return matches[0]
        before_grant = secret_grant(before_statements)
        after_grant = secret_grant(after_statements)
        def as_list(value):
            return [value] if isinstance(value, str) else value
        for grant, arns in ((before_grant, [old_arn]), (after_grant, [old_arn, new_arn])):
            if (grant.get("Sid") != "ReadRuntimeSecret" or grant.get("Effect") != "Allow"
                    or as_list(grant.get("Action")) != ["secretsmanager:GetSecretValue"]
                    or set(grant) != {"Sid", "Effect", "Action", "Resource"}):
                fail("runtime secret grant changed beyond its resource list")
            if sorted(as_list(grant.get("Resource"))) != sorted(arns):
                fail("runtime secret grant must transition from old-only to exact old+v2")
        before["Statement"] = [item for item in before_statements if item is not before_grant]
        after["Statement"] = [item for item in after_statements if item is not after_grant]
        if before != after:
            fail("host policy has unrelated changes")


def residual_entries(phase: str, plan: dict) -> list[tuple[str, list[str]]]:
    # The full, untargeted plan is intentionally NEVER applyable here. Compare
    # its non-target managed action/address inventory before and after each
    # targeted apply, without printing the plan or secret values.
    excluded = set(PHASE_CHANGES[phase])
    entries = []
    for resource in plan.get("resource_changes", []):
        if not isinstance(resource, dict) or resource.get("mode") != "managed":
            continue
        address = resource.get("address")
        actions = resource.get("change", {}).get("actions")
        if not isinstance(address, str) or not isinstance(actions, list):
            fail("invalid full-plan resource change")
        if address not in excluded and actions != ["no-op"]:
            entries.append((address, actions))
    entries = sorted(entries)
    with Path(__file__).with_name("terraform-v2-bootstrap-residual.json").open(encoding="utf-8") as source:
        manifest = json.load(source)
    if manifest.get("source_run") != "https://github.com/ricardoespsanto/mycfc/actions/runs/35872168119":
        fail("unexpected residual baseline source")
    expected = [tuple(item) for item in manifest.get("actions", [])]
    if phase == "host-policy":
        expected = [item for item in expected if item[0] != "aws_iam_user_policy.host_runtime"]
    if entries != sorted(expected):
        fail("full residual address/action inventory differs from fixed reviewed baseline")
    return entries


def residual(phase: str, plan: dict) -> str:
    encoded = json.dumps(residual_entries(phase, plan), separators=(",", ":"), ensure_ascii=False)
    return hashlib.sha256(encoded.encode()).hexdigest()


def main() -> None:
    if len(sys.argv) == 2 and sys.argv[1] == "live-secret-keys":
        validate_secret_values(sys.stdin.read())
        return
    if len(sys.argv) >= 4 and sys.argv[1] == "manifest-hmac" and sys.argv[2] in PHASE_CHANGES:
        print(manifest_hmac(sys.argv[2], sys.argv[3:]))
        return
    if len(sys.argv) != 4 or sys.argv[1] not in ("validate", "residual", "residual-list") or sys.argv[2] not in PHASE_CHANGES:
        fail("usage: terraform-v2-bootstrap-policy.py validate|residual|manifest-hmac PHASE PLAN_JSON|TARGETS")
    command, phase, path = sys.argv[1:]
    plan = load(path, command == "validate")
    if command == "validate":
        validate(phase, plan)
    elif command == "residual-list":
        for address, actions in residual_entries(phase, plan):
            print(f"- `{address}`: `{' → '.join(actions)}`")
    else:
        print(residual(phase, plan))


if __name__ == "__main__":
    main()
