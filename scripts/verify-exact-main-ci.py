#!/usr/bin/env python3
"""Require a completed, successful main push CI run for one exact commit."""

import json
import os
import re
import subprocess
import sys
from typing import NoReturn

REPOSITORY = "ricardoespsanto/mycfc"


def fail() -> NoReturn:
    raise SystemExit("exact-main-ci: no verified successful main push CI run for requested SHA")


def find_run_id(response: object, sha: str) -> int:
    if not isinstance(response, dict):
        fail()
    if not all(isinstance(key, str) for key in response):
        fail()
    runs = response.get("workflow_runs")
    total = response.get("total_count")
    # Never silently accept a truncated API page or a malformed assertion.
    if (not isinstance(runs, list) or type(total) is not int
            or total != len(runs) or total > 100):
        fail()
    matches = [run["id"] for run in runs if isinstance(run, dict)
               and run.get("head_sha") == sha
               and run.get("head_branch") == "main"
               and run.get("event") == "push"
               and run.get("status") == "completed"
               and run.get("conclusion") == "success"
               and type(run.get("id")) is int and run["id"] > 0]
    if not matches:
        fail()
    return matches[0]


def main() -> None:
    if len(sys.argv) != 2 or not re.fullmatch(r"[0-9a-f]{40}", sys.argv[1]):
        fail()
    if os.environ.get("GITHUB_REPOSITORY") != REPOSITORY:
        fail()
    sha = sys.argv[1]
    endpoint = f"/repos/{REPOSITORY}/actions/workflows/ci.yml/runs?head_sha={sha}&per_page=100"
    response: object
    try:
        result = subprocess.run(["gh", "api", endpoint], capture_output=True,
                                text=True, check=False, timeout=30)
        if result.returncode != 0:
            fail()
        response = json.loads(result.stdout)
    except (OSError, subprocess.TimeoutExpired, UnicodeError, json.JSONDecodeError):
        fail()
    print(find_run_id(response, sha))


if __name__ == "__main__":
    main()
