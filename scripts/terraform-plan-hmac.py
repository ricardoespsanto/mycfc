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


def normalize_expected_identity_variation(value: object) -> object:
    """Normalize only the role/session fields changed by the plan/apply identities."""
    if isinstance(value, list):
        return [normalize_expected_identity_variation(item) for item in value]
    if not isinstance(value, dict):
        return value

    normalized = {
        key: normalize_expected_identity_variation(item)
        for key, item in value.items()
    }
    if normalized.get("address") != CALLER_IDENTITY_ADDRESS:
        return normalized

    candidates = []
    values = normalized.get("values")
    if isinstance(values, dict):
        candidates.append(values)
    change = normalized.get("change")
    if isinstance(change, dict):
        for phase in ("before", "after"):
            phase_values = change.get(phase)
            if isinstance(phase_values, dict):
                candidates.append(phase_values)
    for identity in candidates:
        if "arn" in identity:
            identity["arn"] = "<normalized-caller-role-arn>"
        if "user_id" in identity:
            identity["user_id"] = "<normalized-caller-role-user-id>"
    return normalized


def main() -> int:
    if len(sys.argv) != 2:
        raise SystemExit("usage: terraform-plan-hmac.py PLAN_JSON|-")

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

    if sys.argv[1] == "-":
        plan = load_plan(sys.stdin)
    else:
        with Path(sys.argv[1]).open(encoding="utf-8") as plan_file:
            plan = load_plan(plan_file)
    if not isinstance(plan, dict):
        raise SystemExit("Terraform plan JSON must be an object")

    # Terraform's plan timestamp changes between equivalent runs. The protected
    # plan and apply roles also produce different caller ARN/user IDs; neither is
    # referenced by this root. Account IDs and every other field remain bound.
    plan.pop("timestamp", None)
    plan = normalize_expected_identity_variation(plan)
    canonical = canonical_json({"backend": backend, "plan": plan}).encode("utf-8")
    print(hmac.new(bytes.fromhex(key_text), canonical, hashlib.sha256).hexdigest())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
