"""Static guardrails for the one-time, fixed-source protected diagnostic."""

import pathlib
import unittest

WORKFLOW = pathlib.Path(__file__).resolve().parent.parent / '.github/workflows/terraform-production-v2-diagnostic.yml'
PRODUCTION = '6edbe46244207f21aad446d645752a8b15e5a7f1'
DIAGNOSTIC = '5af45505cefa86aa0b22ad563d0228140fda2892'


class DiagnosticExceptionTest(unittest.TestCase):
    def test_exact_sources_and_branch_identity(self):
        text = WORKFLOW.read_text()
        self.assertIn('workflow_dispatch:', text)
        self.assertNotIn('inputs.sha', text)
        self.assertIn(f'PRODUCTION_SOURCE_SHA: {PRODUCTION}', text)
        self.assertIn(f'APPROVED_DIAGNOSTIC_SOURCE_SHA: {DIAGNOSTIC}', text)
        self.assertIn(f'ref: {PRODUCTION}\n          path: production-source', text)
        self.assertIn(f'ref: {DIAGNOSTIC}\n          path: diagnostic-source', text)
        self.assertIn('test "$DISPATCH_REF" = refs/heads/main', text)
        self.assertIn('fail current-main', text)
        self.assertIn('fail signed-source', text)
        self.assertIn('fail production-source', text)
        self.assertIn('fail diagnostic-source', text)
        self.assertIn('production-source/scripts/verify-exact-main-ci.py "$PRODUCTION_SOURCE_SHA"', text)
        self.assertNotIn('verify-exact-main-ci.py "$DISPATCH_SHA"', text)
        self.assertIn('persist-credentials: false', text)

    def test_read_only_protected_execution_and_private_diagnostics(self):
        text = WORKFLOW.read_text()
        self.assertIn('environment: production-plan', text)
        self.assertNotIn('environment: production\n', text)
        self.assertIn('role-to-assume: ${{ vars.AWS_INFRA_PLAN_ROLE_ARN }}', text)
        self.assertIn('cd production-source\n', text)
        self.assertIn('source scripts/terraform-stack.sh', text)
        self.assertIn('terraform_stack_select "$REQUESTED_STACK"', text)
        self.assertIn('python3 diagnostic-source/scripts/terraform-safe-diagnostic_test.py', text)
        self.assertNotIn('python3 scripts/terraform-safe-diagnostic_test.py', text)
        self.assertIn('umask 077', text)
        self.assertIn('mktemp "${RUNNER_TEMP:?}/v2-full-plan.XXXXXXXX"', text)
        self.assertIn('rm -f "$diagnostic_log" "$TF_ROOT/residual.tfplan"; terraform_stack_cleanup', text)
        self.assertIn('tf init -input=false', text)
        self.assertIn('python3 ../diagnostic-runtime/scripts/terraform-structured-diagnostic.py run', text)
        self.assertIn('scripts/terraform-structured-diagnostic_test.py', text)
        self.assertEqual(text.count('> "$diagnostic_log" 2>&1; then'), 1)
        self.assertIn(': > "$diagnostic_log"', text)
        self.assertIn('python3 ../diagnostic-source/scripts/terraform-safe-diagnostic.py "$diagnostic_log" init "$status"', text)
        self.assertEqual(text.count('exit "$status"'), 2)
        self.assertNotIn('tf apply', text)
        self.assertNotIn('upload-artifact', text)
        self.assertNotIn('aws secretsmanager', text)
        self.assertNotIn('aws iam', text)


if __name__ == '__main__':
    unittest.main()
