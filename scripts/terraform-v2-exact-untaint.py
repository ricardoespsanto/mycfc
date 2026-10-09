#!/usr/bin/env python3
"""Private exact v2-container state/metadata guard for a state-only untaint.

All inputs can contain sensitive Terraform state/provider data. Never echo them,
addresses derived from them, exceptions, or unkeyed state fingerprints.
"""

import copy
import hashlib
import hmac
import json
import os
import re
import sys
from pathlib import Path

TARGET = "aws_secretsmanager_secret.app_runtime"
NAME = "/mycfc/production/app-runtime-secrets-v2"
ARN = "arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-runtime-secrets-v2-sVoQ2n"
LEGACY_NAME = "/mycfc/production/app-secrets"
LEGACY_ARN = re.compile(r"arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-secrets-[A-Za-z0-9]{6}\Z")
MAX_FILE = 64_000_000


class Refused(Exception):
    pass


def require(condition: bool) -> None:
    if not condition:
        raise Refused()


def read_private(path: str) -> dict:
    source = Path(path)
    require(source.stat().st_size <= MAX_FILE)
    data = source.read_bytes()
    require(len(data) <= MAX_FILE)
    def unique_object(pairs: list[tuple[str, object]]) -> dict:
        result = {}
        for name, value in pairs:
            require(name not in result)
            result[name] = value
        return result

    parsed = json.loads(data, object_pairs_hook=unique_object)
    require(isinstance(parsed, dict))
    return parsed


def instance(state: dict, resource_type: str, name: str, *, required: bool = True) -> dict | None:
    resources = state.get("resources")
    require(isinstance(resources, list))
    require(all(isinstance(resource, dict) for resource in resources))
    matches = [resource for resource in resources
               if resource.get("type") == resource_type and resource.get("name") == name]
    if not required:
        require(not matches)
        return None
    require(len(matches) == 1)
    resource = matches[0]
    require(resource.get("mode") == "managed" and resource.get("module") is None)
    instances = resource.get("instances")
    require(isinstance(instances, list) and len(instances) == 1)
    item = instances[0]
    require(isinstance(item, dict) and "index_key" not in item and "deposed" not in item)
    require(isinstance(item.get("attributes"), dict))
    return item


def state_identity(state: dict, *, tainted: bool) -> int:
    require(state.get("version") == 4)
    require(isinstance(state.get("lineage"), str) and
            re.fullmatch(r"[0-9a-fA-F-]{36}", state["lineage"]) is not None)
    serial = state.get("serial")
    require(type(serial) is int and serial >= 0)
    target = instance(state, "aws_secretsmanager_secret", "app_runtime")
    require(target is not None)
    require(target.get("status") == "tainted" if tainted else target.get("status") in (None, "ready"))
    attrs = target["attributes"]
    require(attrs.get("name") == NAME and attrs.get("id") == ARN and attrs.get("arn") == ARN)
    require(attrs.get("description") == "MyCFC production web-runtime secrets v2")
    require(attrs.get("recovery_window_in_days") == 30)
    require(attrs.get("force_overwrite_replica_secret") is False)
    require(attrs.get("kms_key_id") in (None, ""))
    require(attrs.get("replica") in (None, []))
    require(attrs.get("name_prefix") in (None, ""))
    instance(state, "aws_secretsmanager_secret_version", "app_runtime", required=False)
    instance(state, "aws_secretsmanager_secret", "runtime", required=False)
    instance(state, "aws_secretsmanager_secret_version", "runtime", required=False)
    legacy = instance(state, "aws_secretsmanager_secret", "legacy_runtime")
    require(legacy is not None and legacy.get("status") in (None, "ready"))
    old = legacy["attributes"]
    require(old.get("name") == LEGACY_NAME and isinstance(old.get("arn"), str)
            and LEGACY_ARN.fullmatch(old["arn"]) is not None and old.get("id") == old["arn"])
    version = instance(state, "aws_secretsmanager_secret_version", "legacy_runtime")
    require(version is not None and version.get("status") in (None, "ready"))
    old_version = version["attributes"]
    version_id = old_version.get("version_id")
    require(old_version.get("secret_id") == old["arn"] and isinstance(version_id, str)
            and bool(version_id) and old_version.get("id") == f"{old['arn']}|{version_id}")
    return serial


def live_identity(description: dict, versions: dict) -> None:
    require(description.get("Name") == NAME and description.get("ARN") == ARN)
    require("DeletedDate" not in description and description.get("KmsKeyId") in (None, ""))
    require(description.get("Description") == "MyCFC production web-runtime secrets v2")
    require(description.get("ReplicationStatus") in (None, []) and
            description.get("PrimaryRegion") is None and
            description.get("RotationEnabled") in (None, False))
    require(versions.get("Name") == NAME and versions.get("ARN") == ARN)
    require(versions.get("Versions") == [] and "NextToken" not in versions)


def canonical_state(state: dict) -> dict:
    """Normalize only Terraform's unordered check-result identity maps.

    Preserve all fields, other arrays, and the difference between absent and
    empty check results. Duplicate or malformed identities must fail closed.
    """
    result = copy.deepcopy(state)
    if "check_results" not in result:
        return result
    checks = result["check_results"]
    if checks is None:  # Terraform 1.15.8 writes null when there are no checks.
        return result
    require(isinstance(checks, list))
    seen_checks = set()
    for check in checks:
        require(isinstance(check, dict))
        kind, address = check.get("object_kind"), check.get("config_addr")
        require(kind in ("resource", "output", "check", "var") and
                isinstance(address, str) and bool(address.strip()))
        identity = (kind, address)
        require(identity not in seen_checks)
        seen_checks.add(identity)
        require("objects" in check)
        objects = check["objects"]
        if objects is None:  # Terraform 1.15.8 writes null for zero instances.
            continue
        require(isinstance(objects, list))
        seen_objects = set()
        for item in objects:
            require(isinstance(item, dict))
            object_address = item.get("object_addr")
            require(isinstance(object_address, str) and bool(object_address.strip()) and
                    object_address not in seen_objects)
            seen_objects.add(object_address)
        check["objects"] = sorted(objects, key=lambda item: item["object_addr"])
    result["check_results"] = sorted(checks, key=lambda check: (check["object_kind"], check["config_addr"]))
    return result


def canonical_bytes(state: dict) -> bytes:
    return json.dumps(canonical_state(state), sort_keys=True,
                      separators=(",", ":"), ensure_ascii=False).encode()


def state_hmac(state: dict, key: bytes) -> str:
    require(len(key) == 32)
    return hmac.new(key, canonical_bytes(state), hashlib.sha256).hexdigest()


def compare(before: dict, after: dict) -> None:
    serial = state_identity(before, tainted=True)
    require(state_identity(after, tainted=False) == serial + 1)
    expected = copy.deepcopy(before)
    expected["serial"] = serial + 1
    target = instance(expected, "aws_secretsmanager_secret", "app_runtime")
    require(target is not None)
    target.pop("status")
    actual = copy.deepcopy(after)
    target_after = instance(actual, "aws_secretsmanager_secret", "app_runtime")
    require(target_after is not None)
    if target_after.get("status") == "ready":
        target_after.pop("status")
    require(canonical_bytes(actual) == canonical_bytes(expected))


def main() -> int:
    try:
        if len(sys.argv) == 5 and sys.argv[1] == "inspect":
            state = read_private(sys.argv[2])
            description = read_private(sys.argv[3])
            versions = read_private(sys.argv[4])
            serial = state_identity(state, tainted=True)
            live_identity(description, versions)
            key_text = os.environ.get("TF_PLAN_HMAC_KEY", "")
            require(re.fullmatch(r"[0-9a-f]{64}", key_text) is not None)
            print(json.dumps({"serial": serial, "state_hmac": state_hmac(state, bytes.fromhex(key_text))},
                             separators=(",", ":")))
        elif len(sys.argv) == 4 and sys.argv[1] == "compare":
            compare(read_private(sys.argv[2]), read_private(sys.argv[3]))
            print("Exact v2 state-only taint removal verified.")
        else:
            raise Refused()
        return 0
    except (Refused, OSError, ValueError, TypeError, KeyError, json.JSONDecodeError):
        print("Exact v2 state repair gate refused; no state or metadata values reported.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
