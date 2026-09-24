#!/usr/bin/env python3
"""Value-free diagnostics for the protected one-time v2 secret bootstrap."""

import fnmatch
import json
import re
import sys
from pathlib import Path


ROLE_ADDRESSES = {
    "aws_iam_role.privacy_activation_admin",
    "aws_iam_role.privacy_activation_coordinator",
    "aws_iam_role.privacy_activation_executor",
}
SECRET_VERSION_ADDRESS = "aws_secretsmanager_secret_version.legacy_runtime"
EXPECTED_DRIFT = ROLE_ADDRESSES | {SECRET_VERSION_ADDRESS}
SECRET_FIELDS = {
    "APP_DB_PASSWORD", "CSRF_AUTH_KEY_B64", "EMAIL_VERIFICATION_HMAC_KEY_B64",
    "TURNSTILE_SECRET_KEY", "SMTP_USERNAME", "SMTP_PASSWORD",
    "GOOGLE_CALENDAR_API_KEY", "POLAR_CLIENT_SECRET", "ACTIVITY_CREDENTIAL_KEYS_JSON",
}
ROLE_FIELDS = {
    "assume_role_policy", "permissions_boundary", "max_session_duration",
    "description", "tags", "tags_all", "force_detach_policies", "path",
    "name", "arn", "id", "unique_id", "create_date",
}
SECRET_VERSION_FIELDS = {
    "version_stages", "version_id", "secret_id", "arn", "id",
    "secret_string", "secret_binary",
}


def fail(message: str) -> None:
    raise SystemExit(f"v2 bootstrap inspection: {message}")


def load_plan(path: str) -> dict:
    try:
        with Path(path).open(encoding="utf-8") as source:
            plan = json.load(source)
    except (OSError, UnicodeError, json.JSONDecodeError):
        fail("plan JSON is unavailable or invalid")
    if not isinstance(plan, dict) or plan.get("format_version") != "1.2" or plan.get("errored") is not False:
        fail("unexpected plan format")
    return plan


def base_address(address: object) -> str:
    if not isinstance(address, str):
        fail("invalid drift address")
    base = re.sub(r"\[[^\[\]\r\n]*\]", "", address)
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", base):
        fail("invalid drift address")
    return base


def changed_fields(before: object, after: object, allowed: set[str]) -> str:
    if not isinstance(before, dict) or not isinstance(after, dict):
        fail("drift attributes are unavailable")
    changed = {key for key in before.keys() | after.keys() if before.get(key) != after.get(key)}
    known = sorted(changed & allowed)
    if changed - allowed:
        known.append("other-unclassified")
    return ", ".join(f"`{key}`" for key in known) or "none"


def has_assume_role_allow(value: object) -> str:
    if not isinstance(value, str):
        return "unknown"
    try:
        policy = json.loads(value)
    except json.JSONDecodeError:
        return "unknown"
    if not isinstance(policy, dict):
        return "unknown"
    statements = policy.get("Statement")
    if isinstance(statements, dict):
        statements = [statements]
    if not isinstance(statements, list):
        return "unknown"
    for statement in statements:
        if not isinstance(statement, dict) or statement.get("Effect") != "Allow":
            continue
        if "NotAction" in statement:
            return "unknown"
        actions = statement.get("Action")
        actions = [actions] if isinstance(actions, str) else actions
        if not isinstance(actions, list) or not all(isinstance(action, str) for action in actions):
            return "unknown"
        if any(fnmatch.fnmatchcase("sts:assumerole", action.lower()) for action in actions):
            return "yes"
    return "no"


def inspect_drift(plan: dict) -> str:
    drift = plan.get("resource_drift")
    if drift in (None, []):
        return "### Bootstrap drift inspection\n\n- No resource drift reported."
    if not isinstance(drift, list) or len(drift) != 4:
        fail("drift inventory differs from the four expected resources")
    lines = ["### Bootstrap drift inspection", "", "Only field names and booleans are shown; no values or instance keys."]
    seen = set()
    for resource in drift:
        if not isinstance(resource, dict):
            fail("invalid drift entry")
        address = base_address(resource.get("address"))
        change = resource.get("change")
        if address not in EXPECTED_DRIFT or address in seen or not isinstance(change, dict) or change.get("actions") != ["update"]:
            fail("drift inventory differs from the four expected resources")
        seen.add(address)
        before, after = change.get("before"), change.get("after")
        allowed = ROLE_FIELDS if address in ROLE_ADDRESSES else SECRET_VERSION_FIELDS
        lines.append(f"- `{address}`: changed fields: {changed_fields(before, after, allowed)}")
        if address in ROLE_ADDRESSES:
            lines.append("  - Has an Allow AssumeRole statement (not an access proof): "
                         f"state {has_assume_role_allow(before.get('assume_role_policy'))}; "
                         f"live {has_assume_role_allow(after.get('assume_role_policy'))}.")
            lines.append("  - Permissions boundary present: "
                         f"state {bool(before.get('permissions_boundary'))}; "
                         f"live {bool(after.get('permissions_boundary'))}.")
        else:
            old_stages, new_stages = before.get("version_stages"), after.get("version_stages")
            if not isinstance(old_stages, list) or not isinstance(new_stages, list):
                fail("secret version stages are unavailable")
            lines.append("  - AWSCURRENT stage present: "
                         f"state {'AWSCURRENT' in old_stages}; live {'AWSCURRENT' in new_stages}.")
            lines.append(f"  - Version ID changed: {before.get('version_id') != after.get('version_id')}.")
    if seen != EXPECTED_DRIFT:
        fail("drift inventory differs from the four expected resources")
    return "\n".join(lines)


def compare_secret(plan: dict, live_secret_string: str) -> tuple[str, bool]:
    changes = plan.get("resource_changes")
    if not isinstance(changes, list):
        fail("proposed v2 secret version is unavailable")
    versions = [resource for resource in changes
                if isinstance(resource, dict) and resource.get("address") == "aws_secretsmanager_secret_version.app_runtime"]
    if len(versions) != 1:
        fail("proposed v2 secret version is unavailable")
    change = versions[0].get("change")
    if not isinstance(change, dict) or not isinstance(change.get("after"), dict):
        fail("proposed v2 secret value is unavailable")
    after_unknown = change.get("after_unknown")
    if after_unknown is not None and not isinstance(after_unknown, dict):
        fail("proposed v2 secret value is unavailable")
    if (after_unknown or {}).get("secret_string"):
        fail("proposed v2 secret value is unavailable")
    proposed_string = change["after"].get("secret_string")
    try:
        proposed = json.loads(proposed_string)
        live = json.loads(live_secret_string)
    except (TypeError, json.JSONDecodeError):
        fail("proposed or live secret is not valid JSON")
    if not isinstance(proposed, dict) or set(proposed) != SECRET_FIELDS or not isinstance(live, dict):
        fail("proposed or live secret field inventory is invalid")
    missing = sorted(key for key in SECRET_FIELDS if key not in live)
    mismatched = sorted(key for key in SECRET_FIELDS if key in live and live[key] != proposed[key])
    result = not missing and not mismatched
    lines = ["### Legacy-to-v2 runtime-secret continuity", "",
             f"- All nine v2 runtime fields match live legacy values: **{'yes' if result else 'no'}**.",
             f"- Missing legacy fields: {', '.join(f'`{key}`' for key in missing) or 'none'}.",
             f"- Different values: {', '.join(f'`{key}`' for key in mismatched) or 'none'}.",
             "- No values, hashes, or version identifiers are emitted."]
    return "\n".join(lines), result


def main() -> None:
    if len(sys.argv) != 3 or sys.argv[1] not in ("drift", "continuity"):
        fail("usage: terraform-v2-bootstrap-inspect.py drift|continuity PLAN_JSON")
    mode, path = sys.argv[1:]
    plan = load_plan(path)
    if mode == "drift":
        print(inspect_drift(plan))
    else:
        report, matches = compare_secret(plan, sys.stdin.read())
        print(report)
        if not matches:
            raise SystemExit(1)


if __name__ == "__main__":
    main()
