import importlib.util
import pathlib
import subprocess
import sys
import tempfile
import unittest

source = pathlib.Path(__file__).with_name('terraform-safe-diagnostic.py')
spec = importlib.util.spec_from_file_location('safe_diagnostic', source)
assert spec is not None and spec.loader is not None
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SafeDiagnosticTest(unittest.TestCase):
    def test_labels_only_fixed_categories(self):
        self.assertEqual(module.categorize(b'Error: AccessDenied in secret abc'), 'aws-access-denied')
        self.assertEqual(module.categorize(b'Error: Cloudflare API returned 429'), 'cloudflare-rate-or-network')
        self.assertEqual(module.categorize(b'Error: unexpected custom value'), 'unclassified')
        self.assertEqual(module.categorize(b'x' * (module.MAX_BYTES + 1)), 'unclassified')

    def test_never_prints_raw_diagnostic_even_on_unknown(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'diagnostic.log'
            for stage in ('init', 'full-plan'):
                for raw in (b'Error: AccessDenied for private-person@example.com',
                            b'Error: unique secret-token-plaintext private-person@example.com'):
                    path.write_bytes(raw)
                    output = subprocess.check_output([sys.executable, str(source), str(path), stage, '1'], text=True)
                    self.assertIn(f'stage={stage}', output)
                    self.assertNotIn('private-person@example.com', output)
                    self.assertNotIn('secret-token-plaintext', output)
                    self.assertNotIn('Error: ', output)
                    self.assertEqual(len(output.splitlines()), 1)

    def test_workflow_cannot_apply_and_erases_capture(self):
        text = (source.parent.parent / '.github/workflows/terraform-production-v2-diagnostic.yml').read_text()
        self.assertIn('environment: production-plan', text)
        self.assertIn('ci_run=$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")', text)
        self.assertIn('test "$REQUESTED_SHA" = "$DISPATCH_SHA"', text)
        self.assertIn('fail signed-commit', text)
        self.assertNotIn('environment: production\n', text)
        self.assertNotIn('tf apply', text)
        self.assertNotIn('upload-artifact', text)
        self.assertIn('umask 077', text)
        self.assertIn('mktemp "${RUNNER_TEMP:?}/v2-full-plan.XXXXXXXX"', text)
        self.assertIn('rm -f "$diagnostic_log" "$TF_ROOT/residual.tfplan"; terraform_stack_cleanup', text)
        self.assertIn('tf init -input=false', text)
        self.assertIn('> "$diagnostic_log" 2>&1; then', text)
        self.assertIn(': > "$diagnostic_log"', text)
        self.assertIn('scripts/terraform-safe-diagnostic.py "$diagnostic_log" init "$status"', text)
        self.assertIn('scripts/terraform-safe-diagnostic.py "$diagnostic_log" full-plan "$status"', text)
        self.assertIn('tf plan -input=false -parallelism=1 -lock-timeout=5m -out=residual.tfplan', text)
        self.assertIn('exit "$status"', text)


if __name__ == '__main__':
    unittest.main()
