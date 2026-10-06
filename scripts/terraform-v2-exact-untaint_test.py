import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SOURCE = Path(__file__).with_name('terraform-v2-exact-untaint.py')
SPEC = importlib.util.spec_from_file_location('exact_untaint', SOURCE)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
OLD = 'arn:aws:secretsmanager:eu-west-1:334960985019:secret:/mycfc/production/app-secrets-AbC123'


def resource(kind, name, attributes, status=None):
    item = {'schema_version': 0, 'attributes': attributes}
    if status is not None:
        item['status'] = status
    return {'mode': 'managed', 'type': kind, 'name': name, 'provider': 'provider["registry.terraform.io/hashicorp/aws"]', 'instances': [item]}


def state():
    return {'version': 4, 'terraform_version': '1.15.8', 'serial': 38,
            'lineage': '11111111-2222-3333-4444-555555555555',
            'resources': [
                resource('aws_secretsmanager_secret', 'app_runtime', {
                    'name': MODULE.NAME, 'id': MODULE.ARN, 'arn': MODULE.ARN,
                    'description': 'MyCFC production web-runtime secrets v2',
                    'recovery_window_in_days': 30, 'force_overwrite_replica_secret': False,
                    'kms_key_id': None, 'replica': [], 'name_prefix': None}, 'tainted'),
                resource('aws_secretsmanager_secret', 'legacy_runtime', {
                    'name': MODULE.LEGACY_NAME, 'id': OLD, 'arn': OLD}),
                resource('aws_secretsmanager_secret_version', 'legacy_runtime', {
                    'secret_id': OLD, 'version_id': 'synthetic-version',
                    'id': OLD + '|synthetic-version', 'secret_string': 'PRIVATE-SYNTHETIC'})]}


def metadata():
    return ({'Name': MODULE.NAME, 'ARN': MODULE.ARN,
             'Description': 'MyCFC production web-runtime secrets v2',
             'RotationEnabled': False},
            {'Name': MODULE.NAME, 'ARN': MODULE.ARN, 'Versions': []})


class ExactUntaintTest(unittest.TestCase):
    def test_valid_state_metadata_hmac_and_only_taint_removed(self):
        before = state()
        desc, versions = metadata()
        self.assertEqual(MODULE.state_identity(before, tainted=True), 38)
        MODULE.live_identity(desc, versions)
        digest = MODULE.state_hmac(before, b'k' * 32)
        self.assertEqual(len(digest), 64)
        self.assertNotIn('PRIVATE', digest)
        after = copy.deepcopy(before)
        after['serial'] += 1
        del after['resources'][0]['instances'][0]['status']
        MODULE.compare(before, after)
        after['resources'][1]['instances'][0]['attributes']['secret_string'] = 'leak'
        with self.assertRaises(MODULE.Refused):
            MODULE.compare(before, after)

    def test_wrong_tainted_deposed_duplicate_or_existing_version_refused(self):
        for label in ('untainted', 'wrong-name', 'wrong-id', 'wrong-arn', 'description',
                      'recovery', 'kms', 'replica', 'deposed', 'duplicate', 'version',
                      'old-address', 'index', 'module', 'missing'):
            with self.subTest(label=label):
                doc = state()
                target = doc['resources'][0]
                attrs = target['instances'][0]['attributes']
                if label == 'untainted': del target['instances'][0]['status']
                elif label == 'wrong-name': attrs['name'] = '/wrong'
                elif label == 'wrong-id': attrs['id'] = '/wrong'
                elif label == 'wrong-arn': attrs['arn'] = '/wrong'
                elif label == 'description': attrs['description'] = '/wrong'
                elif label == 'recovery': attrs['recovery_window_in_days'] = 0
                elif label == 'kms': attrs['kms_key_id'] = 'other'
                elif label == 'replica': attrs['replica'] = ['other']
                elif label == 'deposed': target['instances'][0]['deposed'] = 'abc'
                elif label == 'duplicate': target['instances'].append(copy.deepcopy(target['instances'][0]))
                elif label == 'version': doc['resources'].append(resource('aws_secretsmanager_secret_version', 'app_runtime', {'secret_id': MODULE.ARN}))
                elif label == 'old-address': doc['resources'].append(resource('aws_secretsmanager_secret', 'runtime', {}))
                elif label == 'index': target['instances'][0]['index_key'] = 0
                elif label == 'module': target['module'] = 'module.other'
                elif label == 'missing': doc['resources'].remove(target)
                with self.assertRaises(MODULE.Refused):
                    MODULE.state_identity(doc, tainted=True)

    def test_legacy_lineage_and_serial_reject_drift(self):
        for label in ('legacy-name', 'legacy-id', 'legacy-version', 'legacy-tainted',
                      'legacy-duplicate', 'lineage', 'serial', 'state-version'):
            with self.subTest(label=label):
                doc = state()
                if label == 'legacy-name': doc['resources'][1]['instances'][0]['attributes']['name'] = 'wrong'
                elif label == 'legacy-id': doc['resources'][1]['instances'][0]['attributes']['id'] = 'wrong'
                elif label == 'legacy-version': doc['resources'][2]['instances'][0]['attributes']['secret_id'] = 'wrong'
                elif label == 'legacy-tainted': doc['resources'][1]['instances'][0]['status'] = 'tainted'
                elif label == 'legacy-duplicate': doc['resources'].append(copy.deepcopy(doc['resources'][1]))
                elif label == 'lineage': doc['lineage'] = 'wrong'
                elif label == 'serial': doc['serial'] = True
                elif label == 'state-version': doc['version'] = 3
                with self.assertRaises(MODULE.Refused):
                    MODULE.state_identity(doc, tainted=True)
        before = state()
        after = copy.deepcopy(before)
        after['serial'] += 2
        del after['resources'][0]['instances'][0]['status']
        with self.assertRaises(MODULE.Refused): MODULE.compare(before, after)
        after['serial'] -= 1
        after['lineage'] = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee'
        with self.assertRaises(MODULE.Refused): MODULE.compare(before, after)

    def test_metadata_pagination_nonempty_deleted_replica_denial(self):
        for label in ('wrong-name', 'wrong-arn', 'nonempty', 'pagination', 'deleted',
                      'replica', 'kms', 'rotation', 'description', 'missing-versions'):
            with self.subTest(label=label):
                desc, versions = metadata()
                if label == 'wrong-name': versions['Name'] = 'wrong'
                elif label == 'wrong-arn': desc['ARN'] = 'wrong'
                elif label == 'nonempty': versions['Versions'] = [{'VersionId': 'synthetic'}]
                elif label == 'pagination': versions['NextToken'] = 'synthetic'
                elif label == 'deleted': desc['DeletedDate'] = 'synthetic'
                elif label == 'replica': desc['ReplicationStatus'] = [{'Region': 'synthetic'}]
                elif label == 'kms': desc['KmsKeyId'] = 'synthetic'
                elif label == 'rotation': desc['RotationEnabled'] = True
                elif label == 'description': desc['Description'] = 'wrong'
                elif label == 'missing-versions': del versions['Versions']
                with self.assertRaises(MODULE.Refused): MODULE.live_identity(desc, versions)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'state').write_text(json.dumps(state()))
            (root / 'denied').write_text('AccessDenied PRIVATE-SYNTHETIC')
            result = subprocess.run([sys.executable, str(SOURCE), 'inspect', str(root / 'state'),
                                     str(root / 'denied'), str(root / 'denied')],
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn('PRIVATE', result.stdout + result.stderr)
            self.assertNotIn('AccessDenied', result.stdout + result.stderr)

    def test_cli_receipt_and_serial_hmac_bindings_without_state_disclosure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            before = state()
            desc, versions = metadata()
            for name, value in [('before', before), ('describe', desc), ('versions', versions)]:
                (root / name).write_text(json.dumps(value))
            env = {**os.environ, 'TF_PLAN_HMAC_KEY': '11' * 32}
            args = [sys.executable, str(SOURCE), 'inspect', *(str(root / name)
                    for name in ('before', 'describe', 'versions'))]
            result = subprocess.run(args, capture_output=True, text=True, env=env)
            self.assertEqual(result.returncode, 0)
            receipt = json.loads(result.stdout)
            self.assertEqual(receipt['serial'], 38)
            self.assertEqual(receipt['state_hmac'], MODULE.state_hmac(before, bytes.fromhex('11' * 32)))
            self.assertNotIn('PRIVATE', result.stdout + result.stderr)
            before['resources'][2]['instances'][0]['attributes']['secret_string'] = 'PRIVATE-CHANGED'
            (root / 'before').write_text(json.dumps(before))
            changed = subprocess.run(args, capture_output=True, text=True, env=env)
            self.assertEqual(changed.returncode, 0)
            self.assertNotEqual(json.loads(changed.stdout)['state_hmac'], receipt['state_hmac'])
            self.assertNotIn('PRIVATE', changed.stdout + changed.stderr)

    def test_workflow_scope_and_only_locked_untaint_after_gates(self):
        text = (SOURCE.parent.parent / '.github/workflows/terraform-production-v2-exact-untaint.yml').read_text()
        self.assertIn('environment: production-plan', text)
        self.assertIn('environment: production\n', text)
        self.assertIn('group: mycfc-production-infra', text)
        self.assertIn('test -n "$(python3 scripts/verify-exact-main-ci.py "$REQUESTED_SHA")"', text)
        self.assertIn('TARGET_ADDRESS: aws_secretsmanager_secret.app_runtime', text)
        self.assertNotIn('inputs.address', text)
        self.assertEqual(text.count('tf untaint -lock-timeout=5m "$TARGET_ADDRESS"'), 1)
        self.assertIn('test "$EXPECTED_HMAC" = "$(jq -er', text)
        self.assertIn('scripts/terraform-v2-exact-untaint.py compare', text)
        self.assertIn('--include-deprecated --no-paginate', text)
        self.assertNotIn('tf apply', text)
        self.assertNotIn('state rm', text)
        self.assertNotIn('upload-artifact', text)
        self.assertNotIn('get-secret-value', text)
        self.assertNotIn('terraform_stack_prepare', text)


if __name__ == '__main__':
    unittest.main()
