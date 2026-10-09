import importlib.util
import json
import pathlib
import subprocess
import unittest
from unittest.mock import patch

source = pathlib.Path(__file__).with_name("verify-exact-main-ci.py")
spec = importlib.util.spec_from_file_location("exact_main_ci", source)
assert spec is not None and spec.loader is not None
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

SHA = "a" * 40
OTHER = "b" * 40


def run(sha: object = SHA, **changes: object) -> dict[str, object]:
    return {"id": 12345, "head_sha": sha, "head_branch": "main",
            "event": "push", "status": "completed", "conclusion": "success"} | changes


class ExactMainCITest(unittest.TestCase):
    def test_exact_head_sha_success_and_unrelated_runs(self):
        result = {"total_count": 2, "workflow_runs": [run(OTHER), run()]}
        self.assertEqual(module.find_run_id(result, SHA), 12345)
        for key, value in (("head_sha", OTHER), ("head_branch", "feature"),
                           ("event", "workflow_dispatch"), ("status", "queued"),
                           ("conclusion", "failure"), ("id", True)):
            with self.subTest(key=key), self.assertRaises(SystemExit):
                module.find_run_id({"total_count": 1, "workflow_runs": [run(**{key: value})]}, SHA)

    def test_missing_malformed_or_truncated_pages_fail_closed(self):
        for response in ({"total_count": 2, "workflow_runs": [run()]},
                         {"total_count": True, "workflow_runs": [run()]},
                         {"total_count": 0, "workflow_runs": []},
                         {"workflow_runs": [run()]},
                         {"total_count": 1, "workflow_runs": "not a list"},
                         [run()]):
            with self.subTest(response=response), self.assertRaises(SystemExit):
                module.find_run_id(response, SHA)

    def test_exact_sha_api_request_and_no_raw_error(self):
        response = json.dumps({"total_count": 1, "workflow_runs": [run()]})
        with patch.object(module.sys, "argv", [str(source), SHA]), \
             patch.dict(module.os.environ, {"GITHUB_REPOSITORY": module.REPOSITORY}), \
             patch.object(module.subprocess, "run", return_value=subprocess.CompletedProcess(
                 args=[], returncode=0, stdout=response, stderr="")) as api, \
             patch("builtins.print") as output:
            module.main()
        self.assertEqual(api.call_args.args[0], ["gh", "api",
            f"/repos/{module.REPOSITORY}/actions/workflows/ci.yml/runs?head_sha={SHA}&per_page=100"])
        output.assert_called_once_with(12345)
        with patch.object(module.sys, "argv", [str(source), SHA]), \
             patch.dict(module.os.environ, {"GITHUB_REPOSITORY": module.REPOSITORY}), \
             patch.object(module.subprocess, "run", return_value=subprocess.CompletedProcess(
                 args=[], returncode=1, stdout="private-data", stderr="private-token")), \
             self.assertRaises(SystemExit) as caught:
            module.main()
        self.assertNotIn("private", str(caught.exception))

    def test_workflow_keeps_signature_branch_and_exact_ci_gate(self):
        workflow = (source.parent.parent / ".github/workflows/terraform-production-v2-bootstrap.yml").read_text()
        self.assertIn('verified_sha=$(gh api', workflow)
        self.assertIn('test "$REQUESTED_SHA" = "$main_sha"', workflow)
        self.assertIn('ci_run=$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")', workflow)
        self.assertIn('test -n "$ci_run" || preflight_fail green-main-ci', workflow)


if __name__ == "__main__":
    unittest.main()
