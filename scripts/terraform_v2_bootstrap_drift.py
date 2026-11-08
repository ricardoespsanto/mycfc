"""Private, fail-closed comparison for the exact v2 secret-container refresh drift.

Never print or include plan values in exceptions. This is a read-only predicate;
it does not authorize a Terraform state or provider write.
"""

import json
from typing import NoReturn

V2_CONTAINER = "aws_secretsmanager_secret.app_runtime"
V2_VERSION = "aws_secretsmanager_secret_version.app_runtime"
V2_NAME = "/mycfc/production/app-runtime-secrets-v2"
V2_ARN = "arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-runtime-secrets-v2-sVoQ2n"


class DriftRefusal(ValueError):
    """Fixed-text refusal; do not attach sensitive plan data."""


def refuse() -> NoReturn:
    raise DriftRefusal("exact v2 container policy/tag continuity not proven")


def canonical(value: object) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)


def guarded_v2_drift(plan: dict) -> bool:
    """True only for the exact safe refresh shape; False for no drift.

    All other drift, including incomplete shapes, stops. The target plan is
    separately validated as container NO-OP/version CREATE by bootstrap policy.
    """
    drift = plan.get("resource_drift")
    if drift in (None, []):
        return False
    if not isinstance(drift, list) or len(drift) != 1:
        refuse()
    resource = drift[0]
    if (not isinstance(resource, dict) or resource.get("address") != V2_CONTAINER
            or resource.get("type") != "aws_secretsmanager_secret"
            or resource.get("mode") != "managed"):
        refuse()
    change = resource.get("change")
    if (not isinstance(change, dict) or change.get("actions") != ["update"]
            or change.get("after_unknown") not in (None, {})):
        refuse()
    before, after = change.get("before"), change.get("after")
    if not isinstance(before, dict) or not isinstance(after, dict):
        refuse()
    if not {"policy", "tags"} <= before.keys() or not {"policy", "tags"} <= after.keys():
        refuse()
    changed = {key for key in before.keys() | after.keys()
               if (key in before) != (key in after) or canonical(before.get(key)) != canonical(after.get(key))}
    if changed != {"policy", "tags"}:
        refuse()
    if "policy" not in after or after["policy"] not in (None, ""):
        refuse()
    variables = plan.get("variables")
    if not isinstance(variables, dict):
        refuse()
    project = variables.get("project_name")
    environment = variables.get("environment")
    if not isinstance(project, dict) or not isinstance(environment, dict):
        refuse()
    project, environment = project.get("value"), environment.get("value")
    if project != "mycfc" or environment != "production":
        refuse()
    expected_tags = {"Project": project, "Environment": environment,
                     "ManagedBy": "terraform", "Repository": "ricardoespsanto/mycfc"}
    if after.get("tags") not in (None, {}) or after.get("tags_all") != expected_tags:
        refuse()
    for field, expected in (("name", V2_NAME), ("id", V2_ARN), ("arn", V2_ARN)):
        if before.get(field) != expected or after.get(field) != expected:
            refuse()
    changes = plan.get("resource_changes")
    if not isinstance(changes, list):
        refuse()
    for address, action in ((V2_CONTAINER, "no-op"), (V2_VERSION, "create")):
        matches = [r for r in changes if isinstance(r, dict) and r.get("address") == address]
        if len(matches) != 1 or not isinstance(matches[0].get("change"), dict):
            refuse()
        if matches[0]["change"].get("actions") != [action]:
            refuse()
    container = next(r for r in changes if r["address"] == V2_CONTAINER)
    proposed = container["change"].get("after")
    if (not isinstance(proposed, dict) or canonical(proposed) != canonical(after)
            or container["change"].get("after_unknown") not in (None, {})):
        refuse()
    return True
