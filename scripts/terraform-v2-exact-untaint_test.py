import copy
import importlib.util
import itertools
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


def checked_state():
    doc = state()
    doc['check_results'] = [
        {'object_kind': 'resource', 'config_addr': f'aws_secretsmanager_secret.synthetic_{i}',
         'status': 'fail', 'objects': [
             {'object_addr': f'aws_secretsmanager_secret.synthetic_{i}[{j}]',
              'status': 'fail', 'failure_messages': ['synthetic-first', 'synthetic-second']}
             for j in (0, 1)]}
        for i in range(4)
    ]
    return doc


def untainted(doc):
    result = copy.deepcopy(doc)
    result['serial'] += 1
    del MODULE.instance(result, 'aws_secretsmanager_secret', 'app_runtime')['status']
    return result


class ExactUntaintTest(unittest.TestCase):
    def test_terraform_1158_pulled_null_shapes_inspect_full_synthetic_target(self):
        # Mirrors pinned 1.15.8 state-pull shapes: no checks -> null, and a
        # check aggregate with zero instances -> objects:null. No live data.
        for shape in ('no-checks', 'zero-instances'):
            with self.subTest(shape=shape), tempfile.TemporaryDirectory() as directory:
                doc = state()
                doc['outputs'] = {}
                if shape == 'no-checks':
                    doc['check_results'] = None
                else:
                    doc['check_results'] = [{
                        'object_kind': 'resource',
                        'config_addr': 'aws_secretsmanager_secret.synthetic',
                        'status': 'unknown', 'objects': None}]
                self.assertEqual({'version', 'terraform_version', 'serial', 'lineage',
                                  'outputs', 'resources', 'check_results'}, set(doc))
                root = Path(directory)
                desc, versions = metadata()
                for name, value in (('before', doc), ('describe', desc), ('versions', versions)):
                    (root / name).write_text(json.dumps(value))
                env = {**os.environ, 'TF_PLAN_HMAC_KEY': '11' * 32}
                args = [sys.executable, str(SOURCE), 'inspect',
                        *(str(root / name) for name in ('before', 'describe', 'versions'))]
                result = subprocess.run(args, capture_output=True, text=True, env=env)
                self.assertEqual(0, result.returncode, result.stderr)
                receipt = json.loads(result.stdout)
                self.assertEqual(doc['serial'], receipt['serial'])
                self.assertEqual(MODULE.state_hmac(doc, bytes.fromhex('11' * 32)), receipt['state_hmac'])
                self.assertNotIn('PRIVATE', result.stdout + result.stderr)
                MODULE.compare(doc, untainted(doc))

    def test_null_absent_and_empty_collections_remain_distinct(self):
        no_checks = state()
        with_null = copy.deepcopy(no_checks)
        with_null['check_results'] = None
        with_empty = copy.deepcopy(no_checks)
        with_empty['check_results'] = []
        for left, right in ((no_checks, with_null), (no_checks, with_empty),
                            (with_null, with_empty)):
            self.assertNotEqual(MODULE.state_hmac(left, b'k' * 32),
                                MODULE.state_hmac(right, b'k' * 32))
            with self.assertRaises(MODULE.Refused):
                MODULE.compare(left, untainted(right))

        with_objects_null = checked_state()
        with_objects_null['check_results'][0]['objects'] = None
        without_objects = copy.deepcopy(with_objects_null)
        del without_objects['check_results'][0]['objects']
        with_objects_empty = copy.deepcopy(with_objects_null)
        with_objects_empty['check_results'][0]['objects'] = []
        self.assertEqual(None, MODULE.canonical_state(with_objects_null)['check_results'][0]['objects'])
        reordered = copy.deepcopy(with_objects_null)
        reordered['check_results'].reverse()
        self.assertEqual(MODULE.state_hmac(with_objects_null, b'k' * 32),
                         MODULE.state_hmac(reordered, b'k' * 32))
        MODULE.compare(with_objects_null, untainted(reordered))
        with self.assertRaises(MODULE.Refused):
            MODULE.state_hmac(without_objects, b'k' * 32)
        self.assertNotEqual(MODULE.state_hmac(with_objects_null, b'k' * 32),
                            MODULE.state_hmac(with_objects_empty, b'k' * 32))
        with self.assertRaises(MODULE.Refused):
            MODULE.compare(with_objects_null, untainted(with_objects_empty))

    def test_postdiff_rejects_json_type_coercions_in_retained_state(self):
        for original, changed in ((0, False), (1, True), (1, 1.0)):
            with self.subTest(original=original, changed=changed):
                before = checked_state()
                before['resources'][1]['instances'][0]['attributes']['synthetic_value'] = original
                after = untainted(before)
                after['resources'][1]['instances'][0]['attributes']['synthetic_value'] = changed
                self.assertNotEqual(MODULE.state_hmac(before, b'k' * 32),
                                    MODULE.state_hmac(after, b'k' * 32))
                with self.assertRaises(MODULE.Refused):
                    MODULE.compare(before, after)

    def test_check_result_identity_order_only_is_stable_across_24_pulls(self):
        before = checked_state()
        original = copy.deepcopy(before)
        digest = MODULE.state_hmac(before, b'k' * 32)
        for order in itertools.permutations(range(4)):
            pulled = copy.deepcopy(before)
            pulled['check_results'] = [pulled['check_results'][index] for index in order]
            for check in pulled['check_results']:
                check['objects'].reverse()
            self.assertEqual(digest, MODULE.state_hmac(pulled, b'k' * 32))
            MODULE.compare(before, untainted(pulled))
        self.assertEqual(original, before)  # Neither gate mutates its input.

    def test_check_result_changes_and_unrelated_order_still_refuse(self):
        before = checked_state()
        for label in ('status', 'message', 'message-order', 'missing-field', 'extra-field',
                      'missing-object', 'missing-check', 'resources-order', 'dependencies-order'):
            with self.subTest(label=label):
                changed = copy.deepcopy(before)
                check = changed['check_results'][0]
                obj = check['objects'][0]
                if label == 'status': obj['status'] = 'pass'
                elif label == 'message': obj['failure_messages'][0] = 'other'
                elif label == 'message-order': obj['failure_messages'].reverse()
                elif label == 'missing-field': del obj['failure_messages']
                elif label == 'extra-field': obj['extra'] = 'synthetic'
                elif label == 'missing-object': check['objects'].pop()
                elif label == 'missing-check': changed['check_results'].pop()
                elif label == 'resources-order': changed['resources'].reverse()
                elif label == 'dependencies-order':
                    changed['resources'][0]['instances'][0]['dependencies'] = ['one', 'two']
                    before_with_deps = copy.deepcopy(before)
                    before_with_deps['resources'][0]['instances'][0]['dependencies'] = ['two', 'one']
                    with self.assertRaises(MODULE.Refused):
                        MODULE.compare(before_with_deps, untainted(changed))
                    continue
                self.assertNotEqual(MODULE.state_hmac(before, b'k' * 32),
                                    MODULE.state_hmac(changed, b'k' * 32))
                with self.assertRaises(MODULE.Refused):
                    MODULE.compare(before, untainted(changed))

    def test_duplicate_or_malformed_check_identities_refuse_both_gates(self):
        before = checked_state()
        for label in ('duplicate-check', 'duplicate-object', 'missing-kind', 'invalid-kind',
                      'empty-address', 'missing-object-address', 'missing-objects', 'wrong-objects-type',
                      'wrong-checks-type'):
            with self.subTest(label=label):
                changed = copy.deepcopy(before)
                check = changed['check_results'][0]
                if label == 'duplicate-check': changed['check_results'].append(copy.deepcopy(check))
                elif label == 'duplicate-object': check['objects'].append(copy.deepcopy(check['objects'][0]))
                elif label == 'missing-kind': del check['object_kind']
                elif label == 'invalid-kind': check['object_kind'] = 'invalid'
                elif label == 'empty-address': check['config_addr'] = ' '
                elif label == 'missing-object-address': del check['objects'][0]['object_addr']
                elif label == 'missing-objects': del check['objects']
                elif label == 'wrong-objects-type': check['objects'] = {}
                elif label == 'wrong-checks-type': changed['check_results'] = {}
                with self.assertRaises(MODULE.Refused):
                    MODULE.state_hmac(changed, b'k' * 32)
                with self.assertRaises(MODULE.Refused):
                    MODULE.compare(before, untainted(changed))

    def test_duplicate_json_keys_refuse_privately_at_any_depth(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'private'
            for payload in ('{"serial":1,"serial":2}',
                            '{"check_results":[{"object_kind":"resource","object_kind":"output"}]}'):
                path.write_text(payload + 'PRIVATE-SYNTHETIC')
                result = subprocess.run([sys.executable, str(SOURCE), 'compare', str(path), str(path)],
                                        capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn('PRIVATE', result.stdout + result.stderr)
                path.write_text(payload)
                with self.assertRaises(MODULE.Refused):
                    MODULE.read_private(str(path))

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

    def test_private_inventory_stages_are_fixed_and_fail_closed(self):
        text = (SOURCE.parent.parent / '.github/workflows/terraform-production-v2-exact-untaint.yml').read_text()
        self.assertEqual(text.count('|| fail init'), 2)
        self.assertEqual(text.count('|| fail statepull'), 2)
        self.assertEqual(text.count('|| fail describe'), 2)
        self.assertEqual(text.count('|| fail list'), 2)
        self.assertEqual(text.count('|| fail inspect'), 2)
        self.assertEqual(text.count('|| fail receipt'), 2)
        self.assertEqual(text.count('|| fail serial-binding'), 1)
        self.assertEqual(text.count('|| fail hmac-binding'), 1)
        self.assertIn('init|statepull|describe|list|inspect|serial-binding|hmac-binding|main|untaint|after-state|postdiff)', text)
        self.assertEqual(text.count('|| fail untaint'), 1)
        self.assertEqual(text.count('|| fail after-state'), 1)
        self.assertEqual(text.count('|| fail postdiff'), 1)
        self.assertEqual(text.count('|| fail main\n'), 2)
        self.assertIn('"${1:-}"', text)
        self.assertIn('Exact v2 review stage=$1 refused; no state change', text)
        self.assertIn('Exact v2 clear stage=$1 failed; inspect before retry', text)
        self.assertNotIn('cat "$private/', text)
        self.assertEqual(text.count('GH_TOKEN: ${{ github.token }}'), 4)
        for step in ('Inventory exact tainted state and empty live container privately',
                     'Recheck, untaint only exact v2 address, and prove state-only diff'):
            self.assertIn('GH_TOKEN: ${{ github.token }}', text.split('- name: ' + step, 1)[1].split('run: |', 1)[0])

    def test_clear_binding_labels_match_exact_predicates_and_order(self):
        workflow = (SOURCE.parent.parent / '.github/workflows/terraform-production-v2-exact-untaint.yml').read_text()
        clear = workflow.split('- name: Recheck, untaint only exact v2 address, and prove state-only diff', 1)[1]
        serial = """test "$EXPECTED_SERIAL" = "$(jq -er '.serial' "$private/receipt" 2>"$private/err")" || fail serial-binding"""
        hmac = """test "$EXPECTED_HMAC" = "$(jq -er '.state_hmac' "$private/receipt" 2>"$private/err")" || fail hmac-binding"""
        main = '|| fail main\n'
        untaint = 'tf untaint -lock-timeout=5m "$TARGET_ADDRESS"'

        def correct_bindings(script):
            return (script.count(serial) == 1 and script.count(hmac) == 1 and
                    script.index(serial) < script.index(hmac) < script.index(main) < script.index(untaint))

        self.assertTrue(correct_bindings(clear))
        swapped = clear.replace('|| fail serial-binding', '|| fail temporary-binding').replace(
            '|| fail hmac-binding', '|| fail serial-binding').replace(
            '|| fail temporary-binding', '|| fail hmac-binding')
        self.assertFalse(correct_bindings(swapped))
        reordered = clear.replace(serial, 'temporary-binding-line').replace(hmac, serial).replace(
            'temporary-binding-line', hmac)
        self.assertFalse(correct_bindings(reordered))


if __name__ == '__main__':
    unittest.main()
