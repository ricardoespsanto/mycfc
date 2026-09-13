#!/usr/bin/env python3
"""Emit a keyed integrity digest for deterministic Terraform plan semantics."""

import hashlib
import hmac
import json
import os
import re
import sys
from pathlib import Path

CALLER_IDENTITY_ADDRESS = "data.aws_caller_identity.current"
GITHUB_INFRA_ROLE_ARN = re.compile(
    r"^arn:(aws[a-zA-Z-]*):sts::([0-9]{12}):assumed-role/"
    r"github-infra-(?:plan|apply)/([^/]+)$"
)
ROLE_USER_ID = re.compile(r"^[A-Z0-9]+:(.+)$")


class JSONNumber(str):
    """A validated JSON number retained with its exact source representation."""


def reject_nonstandard_number(value: str) -> None:
    raise ValueError(f"non-standard JSON number: {value}")


def load_plan(source: object) -> object:
    return json.load(
        source,
        parse_float=JSONNumber,
        parse_int=JSONNumber,
        parse_constant=reject_nonstandard_number,
    )


def canonical_json(value: object) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, JSONNumber):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, list):
        return "[" + ",".join(canonical_json(item) for item in value) + "]"
    if isinstance(value, dict):
        return "{" + ",".join(
            json.dumps(key, ensure_ascii=False) + ":" + canonical_json(value[key])
            for key in sorted(value)
        ) + "}"
    raise TypeError(f"unsupported JSON value: {type(value).__name__}")


def normalize_caller_identity_record(record: object) -> None:
    """Normalize one caller data-source record at a verified Terraform JSON path."""
    if not isinstance(record, dict) or not (
        record.get("address") == CALLER_IDENTITY_ADDRESS
        and record.get("mode") == "data"
        and record.get("type") == "aws_caller_identity"
        and record.get("name") == "current"
    ):
        return

    candidates = []
    values = record.get("values")
    if isinstance(values, dict):
        candidates.append(values)
    change = record.get("change")
    if isinstance(change, dict):
        for phase in ("before", "after"):
            phase_values = change.get(phase)
            if isinstance(phase_values, dict):
                candidates.append(phase_values)
    for identity in candidates:
        arn = identity.get("arn")
        if not isinstance(arn, str):
            continue
        role_match = GITHUB_INFRA_ROLE_ARN.fullmatch(arn)
        if not role_match:
            continue
        partition, account_id, session_name = role_match.groups()
        identity["arn"] = (
            f"arn:{partition}:sts::{account_id}:assumed-role/"
            f"github-infra-<normalized>/{session_name}"
        )
        user_id = identity.get("user_id")
        if isinstance(user_id, str):
            user_id_match = ROLE_USER_ID.fullmatch(user_id)
            if user_id_match:
                identity["user_id"] = (
                    f"<normalized-caller-role-user-id>:{user_id_match.group(1)}"
                )


def normalize_module(module: object) -> None:
    if not isinstance(module, dict):
        return
    resources = module.get("resources")
    if isinstance(resources, list):
        for resource in resources:
            normalize_caller_identity_record(resource)
    child_modules = module.get("child_modules")
    if isinstance(child_modules, list):
        for child_module in child_modules:
            normalize_module(child_module)


def normalize_expected_identity_variation(plan: dict[str, object]) -> dict[str, object]:
    """Normalize caller identity only in Terraform's defined resource collections."""
    planned_values = plan.get("planned_values")
    if isinstance(planned_values, dict):
        normalize_module(planned_values.get("root_module"))

    prior_state = plan.get("prior_state")
    if isinstance(prior_state, dict):
        prior_values = prior_state.get("values")
        if isinstance(prior_values, dict):
            normalize_module(prior_values.get("root_module"))

    for collection_name in ("resource_changes", "resource_drift"):
        collection = plan.get(collection_name)
        if isinstance(collection, list):
            for resource in collection:
                normalize_caller_identity_record(resource)

    deferred_changes = plan.get("deferred_changes")
    if isinstance(deferred_changes, list):
        for deferred in deferred_changes:
            if isinstance(deferred, dict):
                normalize_caller_identity_record(deferred.get("resource_change"))
    return plan


def keyed_digest(key: bytes, value: object) -> str:
    return hmac.new(key, canonical_json(value).encode("utf-8"), hashlib.sha256).hexdigest()


def main() -> int:
    components = len(sys.argv) == 3 and sys.argv[1] == "--components"
    if len(sys.argv) != 2 and not components:
        raise SystemExit(
            "usage: terraform-plan-hmac.py [--components] PLAN_JSON|-"
        )

    key_text = os.environ.get("TF_PLAN_HMAC_KEY", "")
    if not re.fullmatch(r"[0-9a-f]{64}", key_text):
        raise SystemExit(
            "TF_PLAN_HMAC_KEY must be exactly 32 bytes encoded as lowercase hexadecimal"
        )
    backend = {
        "bucket": os.environ.get("TF_BACKEND_BUCKET", ""),
        "key": os.environ.get("TF_BACKEND_KEY", ""),
        "region": os.environ.get("AWS_REGION", ""),
    }
    if not all(backend.values()):
        raise SystemExit("TF_BACKEND_BUCKET, TF_BACKEND_KEY, and AWS_REGION must be set")

    plan_path = sys.argv[2] if components else sys.argv[1]
    if plan_path == "-":
        plan = load_plan(sys.stdin)
    else:
        with Path(plan_path).open(encoding="utf-8") as plan_file:
            plan = load_plan(plan_file)
    if not isinstance(plan, dict):
        raise SystemExit("Terraform plan JSON must be an object")

    # Terraform's plan timestamp changes between equivalent runs. The protected
    # plan and apply roles also produce different caller ARN/user IDs; neither is
    # referenced by this root. Account IDs and every other field remain bound.
    plan.pop("timestamp", None)
    plan = normalize_expected_identity_variation(plan)
    key = bytes.fromhex(key_text)
    if components:
        print(json.dumps({
            name: keyed_digest(
                key,
                {"backend": backend, "component": name, "value": plan[name]},
            )
            for name in sorted(plan)
        }, sort_keys=True, separators=(",", ":")))
        return 0
    print(keyed_digest(key, {"backend": backend, "plan": plan}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
