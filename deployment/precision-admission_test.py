#!/usr/bin/env python3
"""Synthetic host/release/restore rehearsal. Never reads an installed path."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import stat
import tempfile
import subprocess
import textwrap
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('admission', Path(__file__).with_name('precision-admission.py'))
ops = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ops)


class AdmissionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.owner = patch.object(ops, 'OWNER', os.getuid())
        self.trust = patch.object(ops, 'TRUST_ROOT', self.root)
        self.owner.start()
        self.trust.start()
        self.addCleanup(self.tmp.cleanup)
        self.addCleanup(self.owner.stop)
        self.addCleanup(self.trust.stop)
        (self.root/'projection').mkdir(mode=0o755)
        self.authority = self.root/'host.json'
        self.projection = self.root/'projection/admission.json'
        self.controller = self.root/'controller.sh'
        self.controller.write_text('#!/bin/sh\n# trusted synthetic controller\n')
        self.controller.chmod(0o755)
        self.current = {'contract': ops.STATUS, 'database_incarnation': '11111111-1111-4111-8111-111111111111',
                        'schema_contract': 'schedule-c1', 'state_epoch': 0, 'required_reader': 1,
                        'floor_commit_id': None, 'floor_manifest_sha256': None,
                        'partial_v2_enabled': False, 'ever_partial_v2_enabled': False}
        self.cap = {'protocol': 1, 'version': 'v2.0.9', 'candidate': 'a'*40, 'reader_generation': 2,
                    'writer_generation': 1, 'snapshot_read': [1, 2], 'schema_read': ['schedule-c1', 'schedule-p1'],
                    'write': ['INTERVAL', 'snapshot-v1']}
        inventory = ['001_initial', 'reset-baseline-v1']
        self.manifest = {'ci_run_id': 1, 'contract': 'mycfc/release-publication/v1',
                         'expected_gates': {'guardian_intake': False, 'privacy_worker': False},
                         'git_sha': 'a'*40, 'git_tree_sha': 'b'*40,
                         'image': {'repository': 'registry.example/mycfc', 'digest': 'sha256:'+'a'*64},
                         'issues': [391], 'published_at': '2026-11-10T00:00:00Z',
                         'release_tag': 'release-v2.0.9-'+('a'*40),
                         'schema': {'migration_digest': ops.sha('\n'.join(inventory).encode()), 'ordered_migrations': inventory},
                         'version': 'v2.0.9'}
        self.manifest_path = self.root/'manifest.json'
        self.cap_path = self.root/'capabilities.json'
        self.proof_path = self.root/'reconcile.json'
        ops.atomic(self.manifest_path, self.manifest, 0o600)
        ops.atomic(self.cap_path, self.cap, 0o600)

    def initial(self):
        ops.block(self.authority, self.projection, self.current, 0)

    def proof(self):
        record = ops.host(self.authority)
        proof = {'contract': ops.PROOF, 'approval_reference': 'local-test-391',
                 'restore_generation': record['projection']['restore_generation'],
                 'status_sha256': ops.sha(ops.canonical(self.current)),
                 'controller_sha256': ops.sha(ops.read(self.controller, (0o755,))),
                 'manifest_sha256': [ops.sha(ops.read(self.manifest_path))],
                 'checked_image_digests': [self.manifest['image']['digest']], 'checks': ops.CHECKS}
        ops.atomic(self.proof_path, proof, 0o600)

    def reconcile(self):
        self.proof()
        ops.reconcile(self.authority, self.projection, self.current, self.proof_path,
                      self.controller, [(self.manifest_path, self.cap_path)])

    def check(self, image=None):
        if image is None:
            image = ops.manifest_image(self.manifest, self.cap)
        ops.admission_check(ops.host(self.authority), ops.load(self.projection, (0o444,)), self.current,
                            ops.sha(ops.read(self.controller, (0o755,))), image)

    def test_first_install_requires_external_reconciliation(self):
        with self.assertRaises(FileNotFoundError):
            self.check()
        self.initial()
        self.assertEqual(stat.S_IMODE(self.projection.stat().st_mode), 0o444)
        self.assertEqual(stat.S_IMODE(self.authority.stat().st_mode), 0o600)
        with self.assertRaises(ops.Refused):
            self.check()
        with self.assertRaises(FileNotFoundError):
            ops.reconcile(self.authority, self.projection, self.current, self.proof_path,
                          self.controller, [(self.manifest_path, self.cap_path)])
        self.reconcile()
        self.check()

    def test_real_file_floor_restore_and_compatible_fallback_sequence(self):
        self.initial()
        self.reconcile()
        self.check()
        request = ops.prepare_floor(self.authority, self.current, '22222222-2222-4222-8222-222222222222', 'b'*64, 2)
        with self.assertRaises(ops.Refused):
            self.check()  # host-first crash/admission fence
        with self.assertRaises(ops.Refused):
            ops.complete_floor(self.authority, self.projection, self.current)
        self.current.update(floor_commit_id=request['floor_commit_id'], floor_manifest_sha256=request['floor_manifest_sha256'],
                            required_reader=2, state_epoch=1)
        ops.complete_floor(self.authority, self.projection, self.current)
        self.check()
        self.current.update(schema_contract='schedule-p1', state_epoch=2)
        self.check()  # C's exact-only writer is still a compatible reader
        self.current.update(partial_v2_enabled=True, ever_partial_v2_enabled=True, state_epoch=3)
        self.check()
        old = copy.deepcopy(ops.manifest_image(self.manifest, self.cap))
        old['capabilities']['reader_generation'] = 1
        with self.assertRaises(ops.Refused):
            self.check(old)
        self.current.update(partial_v2_enabled=False, state_epoch=4)
        self.check()  # disable keeps immutable floor while epoch advances
        ops.block(self.authority, self.projection, self.current, 1)
        with self.assertRaises(ops.Refused):
            self.check()
        saved = copy.deepcopy(self.current)
        self.current['state_epoch'] = 2
        with self.assertRaises(ops.Refused):
            self.reconcile()  # same-incarnation rewind cannot reconcile down
        self.current = saved
        self.current['database_incarnation'] = '33333333-3333-4333-8333-333333333333'
        self.reconcile()  # trusted explicit restore reconciliation, not copied-fact admission
        self.check()
        self.assertEqual(ops.host(self.authority)['projection']['restore_generation'], 2)

    def test_unknown_keys_duplicate_and_numeric_type_rejected(self):
        for data in [b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":1.0}', b'{} {}', b'"\xff"']:
            with self.subTest(data=data), self.assertRaises(ops.Refused):
                ops.decode(data)
        self.initial()
        self.reconcile()
        value = ops.load(self.projection, (0o444,))
        for key, bad in [('restore_generation', True), ('minimum_state_epoch', 2**63),
                         ('minimum_reader', 0), ('contract', 'future'), ('images', value['images']*2)]:
            changed = dict(value, **{key: bad})
            with self.subTest(key=key), self.assertRaises(ops.Refused):
                ops.projection(changed)
        with self.assertRaises(ops.Refused):
            ops.projection(dict(value, unknown=True))

    def test_symlink_mode_owner_and_oversized_inputs_refuse(self):
        self.initial()
        self.projection.chmod(0o644)
        with self.assertRaises(ops.Refused):
            ops.load(self.projection, (0o444,))
        self.projection.unlink()
        self.projection.symlink_to(self.authority)
        with self.assertRaises(OSError):
            ops.load(self.projection, (0o444,))
        with self.assertRaises(ops.Refused):
            ops.atomic(self.projection, {}, 0o444)
        with patch.object(ops, 'OWNER', os.getuid()+1), self.assertRaises(ops.Refused):
            ops.load(self.authority)
        with self.assertRaises(ops.Refused):
            ops.decode(b' '*65537)
        (self.root/'unsafe').mkdir(mode=0o777)
        (self.root/'unsafe').chmod(0o777)
        with self.assertRaises(ops.Refused):
            ops.atomic(self.root/'unsafe/file', {}, 0o600)

    def test_wrong_controller_stamps_and_proof_cannot_admit(self):
        self.initial()
        self.proof()
        proof = ops.load(self.proof_path)
        proof['checks'] = []
        ops.atomic(self.proof_path, proof, 0o600)
        with self.assertRaises(ops.Refused):
            ops.reconcile(self.authority, self.projection, self.current, self.proof_path,
                          self.controller, [(self.manifest_path, self.cap_path)])
        self.reconcile()
        self.controller.write_text('different controller')
        with self.assertRaises(ops.Refused):
            self.check()
        wrong = dict(self.cap, candidate='c'*40)
        with self.assertRaises(ops.Refused):
            ops.manifest_image(self.manifest, wrong)
        # Existing strict publication schema is unchanged.
        with self.assertRaises(ops.Refused):
            ops.manifest_image(dict(self.manifest, capabilities=self.cap), self.cap)

    def test_normal_forward_metadata_is_distinct_from_retired_reset_contract(self):
        normal = {'contract':'mycfc/normal-release/v1','version':self.cap['version'],
                  'candidate':self.cap['candidate'],'database':'mycfc','parent_digest':'c'*64,
                  'final_digest':self.manifest['schema']['migration_digest']}
        ops.normal_contract(normal,self.manifest)
        for changed in [dict(normal,baseline_digest='d'*64), dict(normal,final_digest='e'*64),
                        dict(normal,candidate='f'*40), dict(normal,version='unstamped')]:
            with self.assertRaises(ops.Refused):
                ops.normal_contract(changed,self.manifest)
        with self.assertRaises(ops.Refused):
            ops.normal_contract(normal,dict(self.manifest,capabilities=self.cap))

    def test_floor_minimum_is_monotonic_and_not_forever_two(self):
        self.initial()
        self.reconcile()
        with self.assertRaises(ops.Refused):
            ops.prepare_floor(self.authority, self.current, '2'*8+'-'+('2'*4+'-')*3+'2'*12, 'b'*64, 3)
        self.current['required_reader'] = 3
        with self.assertRaises(ops.Refused):
            self.check()

    def test_atomic_fsyncs_file_and_parent_and_preserves_on_rename_failure(self):
        target = self.root/'atomic.json'
        ops.atomic(target, {'old': 1}, 0o600)
        sync_modes = []
        actual = os.fsync
        def synced(fd):
            sync_modes.append(os.fstat(fd).st_mode)
            actual(fd)
        with patch.object(ops.os, 'fsync', side_effect=synced):
            ops.atomic(target, {'new': 2}, 0o600)
        self.assertTrue(any(stat.S_ISREG(mode) for mode in sync_modes))
        self.assertTrue(any(stat.S_ISDIR(mode) for mode in sync_modes))
        with patch.object(ops.os, 'replace', side_effect=OSError('synthetic crash')), self.assertRaises(OSError):
            ops.atomic(target, {'broken': 3}, 0o600)
        self.assertEqual(ops.load(target), {'new': 2})
        self.assertFalse(list(self.root.glob('.precision-*')))

    def test_host_projection_crash_refuses_and_block_is_written_first(self):
        self.initial()
        self.reconcile()
        real = ops.atomic
        writes = []
        def record_write(path, value, mode):
            writes.append(Path(path))
            if Path(path) == self.authority:
                raise OSError('synthetic authority write failure')
            real(path, value, mode)
        with patch.object(ops, 'atomic', side_effect=record_write), self.assertRaises(OSError):
            ops.block(self.authority, self.projection, self.current, 1)
        self.assertEqual(writes[0], self.projection)
        with self.assertRaises(ops.Refused):
            self.check()

    def test_controller_lock_serialization_and_inherited_same_description(self):
        lock = self.root/'lock'
        with ops.controller_lock(lock):
            with self.assertRaises(BlockingIOError):
                with ops.controller_lock(lock):
                    self.fail('second controller acquired lock')
        fd = os.open(lock, os.O_RDWR)
        try:
            with ops.controller_lock(lock, fd):
                pass
        finally:
            os.close(fd)

    def shell_fixture(self):
        # Test-only executable shims. Production code has no alternate UID/root
        # switches and cannot use these fixture parameters.
        bindir = self.root/'bin'
        bindir.mkdir()
        helper = Path(__file__).with_name('precision-admission.py').resolve()
        wrapper = '#!/usr/bin/python3\n' + textwrap.dedent(f'''
            import importlib.util, os, pathlib, sys
            spec=importlib.util.spec_from_file_location('ops',{str(helper)!r})
            ops=importlib.util.module_from_spec(spec)
            spec.loader.exec_module(ops)
            ops.OWNER={os.getuid()}
            ops.TRUST_ROOT=pathlib.Path({str(self.root)!r})
            ops.os.geteuid=lambda:0
            sys.argv=sys.argv[1:]
            raise SystemExit(ops.main())
        ''')
        (bindir/'python3').write_text(wrapper)
        docker = '''#!/usr/bin/python3
import json,os,sys
with open(os.environ['REHEARSAL_LOG'],'a') as f:
 f.write(json.dumps({'args':sys.argv[1:],'image':os.environ.get('MYCFC_IMAGE'),'bind':os.environ.get('GUARDIAN_RUNTIME_IMAGE_DIGEST')})+'\\n')
if 'guardian-release-status' in sys.argv: print('guardian_intake_active=false')
elif sys.argv[1:2]==['inspect']:
 print(os.environ['REHEARSAL_FALLBACK'] if '.Config.Image' in sys.argv[3] else '172.30.0.8')
'''
        (bindir/'docker').write_text(docker)
        (bindir/'curl').write_text('#!/bin/sh\nexit "${REHEARSAL_CURL_FAILURE:-0}"\n')
        for file in bindir.iterdir():
            file.chmod(0o755)
        self.env = dict(os.environ, PATH=str(bindir)+':'+os.environ['PATH'],
                        MYCFC_PRECISION_AUTHORITY=str(self.authority), MYCFC_PRECISION_PROJECTION=str(self.projection),
                        MYCFC_PRECISION_LOCK_FILE=str(self.root/'controller.lock'),
                        REHEARSAL_LOG=str(self.root/'docker.log'),
                        REHEARSAL_FALLBACK=self.manifest['image']['repository']+'@'+self.manifest['image']['digest'])
        return Path(__file__).with_name('precision-controller.sh').resolve()

    def test_guarded_shell_fallback_uses_current_binder_without_old_migration(self):
        self.initial()
        self.reconcile()
        # Add an independently authenticated P image as schema-current binder.
        pcap = dict(self.cap, version='v2.0.10', candidate='b'*40, writer_generation=2,
                    write=['INTERVAL', 'START_ONLY', 'DAY', 'BAND', 'snapshot-v1', 'snapshot-v2'])
        pmanifest = dict(self.manifest, version='v2.0.10', git_sha='b'*40,
                         image={'repository':'registry.example/mycfc', 'digest':'sha256:'+'b'*64})
        pp, cp = self.root/'pmanifest.json', self.root/'pcapabilities.json'
        ops.atomic(pp, pmanifest, 0o600)
        ops.atomic(cp, pcap, 0o600)
        self.proof()
        proof = ops.load(self.proof_path)
        proof['manifest_sha256'].append(ops.sha(ops.read(pp)))
        proof['checked_image_digests'].append(pmanifest['image']['digest'])
        ops.atomic(self.proof_path, proof, 0o600)
        ops.reconcile(self.authority, self.projection, self.current, self.proof_path,
                      self.controller, [(self.manifest_path,self.cap_path),(pp,cp)])
        self.current.update(schema_contract='schedule-p1', state_epoch=1)
        status_file = self.root/'status.json'
        ops.atomic(status_file, self.current, 0o600)
        adapter = self.shell_fixture()
        args = ['sh', str(adapter), 'fallback', str(status_file), str(self.manifest_path), str(self.cap_path),
                str(self.controller), 'green', str(pp), str(cp)]
        lock = self.root/'controller.lock'
        lock.touch(mode=0o600)
        fd = os.open(lock, os.O_RDWR)
        try:
            import fcntl
            fcntl.flock(fd, fcntl.LOCK_EX)
            env = dict(self.env, MYCFC_PRECISION_LOCK_FD=str(fd))
            result = subprocess.run(args, env=env, pass_fds=(fd,), text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
            calls = [json.loads(line) for line in (self.root/'docker.log').read_text().splitlines()]
            self.assertEqual(calls[0]['image'], 'registry.example/mycfc@sha256:'+'b'*64)
            self.assertEqual(calls[0]['bind'], 'sha256:'+'a'*64)
            self.assertIn('guardian-release-status', calls[1]['args'])
            self.assertEqual(calls[2]['image'], self.env['REHEARSAL_FALLBACK'])
            self.assertIn('app-green', calls[2]['args'])
            for call in calls:
                self.assertFalse(set(call['args']) & {'migrate', 'bootstrap-db', 'db-bootstrap', 'harden-db', 'stop'})
            self.assertIn('fallback_ready', result.stdout)
            # Missing projection denies before any binder or process action.
            (self.root/'docker.log').unlink()
            self.projection.unlink()
            result = subprocess.run(args, env=env, pass_fds=(fd,), text=True, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((self.root/'docker.log').exists())
        finally:
            os.close(fd)

    def test_shell_fallback_requires_outer_lock_and_bad_readiness_never_stops_candidate(self):
        self.initial()
        self.reconcile()
        adapter = self.shell_fixture()
        status_file = self.root/'status.json'
        ops.atomic(status_file, self.current, 0o600)
        args = ['sh',str(adapter),'fallback',str(status_file),str(self.manifest_path),str(self.cap_path),
                str(self.controller),'blue',str(self.manifest_path),str(self.cap_path)]
        refused = subprocess.run(args, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(refused.returncode,0)
        self.assertFalse((self.root/'docker.log').exists())
        lock=self.root/'controller.lock'
        lock.touch(mode=0o600)
        fd=os.open(lock,os.O_RDWR)
        try:
            result=subprocess.run(args,env=dict(self.env,MYCFC_PRECISION_LOCK_FD=str(fd),REHEARSAL_CURL_FAILURE='1'),
                                  pass_fds=(fd,),capture_output=True,text=True)
            self.assertNotEqual(result.returncode,0)
            self.assertNotIn('fallback_ready',result.stdout)
            calls=(self.root/'docker.log').read_text()
            self.assertNotIn('"stop"',calls)
            self.assertNotIn('migrate',calls)
        finally:
            os.close(fd)


if __name__ == '__main__':
    unittest.main(verbosity=2)
