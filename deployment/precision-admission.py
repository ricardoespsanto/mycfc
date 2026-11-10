#!/usr/bin/env python3
"""Root-only host admission checkpoint. No SQL, Docker, network or secret reads.

The caller must authenticate release manifests/images and externally fence every
restore. Matching copied database facts alone can never create admission.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import sys

OWNER = 0
TRUST_ROOT = Path('/')
LIMIT = 65536
ADMISSION = 'mycfc/training-schedule-admission/v1'
STATUS = 'mycfc/training-schedule-status/v1'
AUTHORITY = 'mycfc/training-schedule-host/v1'
PROOF = 'mycfc/training-schedule-reconciliation/v1'
CHECKS = ['controller-fenced', 'pre-c-processes-stopped', 'restore-reconciled',
          'schema-ledger-verified', 'guardian-release-binding-preserved',
          'privacy-retired', 'fallback-runtime-only']
HEX = re.compile(r'[0-9a-f]{64}\Z')
UUID = re.compile(r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\Z')
VERSION = re.compile(r'v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?\Z')


class Refused(ValueError):
    pass


def require(condition, reason='invalid_contract'):
    if not condition:
        raise Refused(reason)


def keys(value, expected):
    require(type(value) is dict and set(value) == set(expected))


def integer(value, minimum=0, maximum=2**63-1):
    require(type(value) is int and minimum <= value <= maximum)


def digest(value, image=False):
    require(type(value) is str and bool(HEX.fullmatch(value[7:] if image and value.startswith('sha256:') else value)))
    if image:
        require(value.startswith('sha256:'))


def uuid(value):
    require(type(value) is str and bool(UUID.fullmatch(value)))


def floor(value):
    ident, sha = value['floor_commit_id'], value['floor_manifest_sha256']
    require((ident is None) == (sha is None))
    if ident is not None:
        uuid(ident)
        digest(sha)


def capabilities(value):
    keys(value, ['protocol', 'version', 'candidate', 'reader_generation', 'writer_generation',
                 'snapshot_read', 'schema_read', 'write'])
    require(type(value['protocol']) is int and value['protocol'] == 1)
    require(type(value['version']) is str and bool(VERSION.fullmatch(value['version'])))
    require(type(value['candidate']) is str and bool(re.fullmatch('[0-9a-f]{40}', value['candidate'])))
    integer(value['reader_generation'], 1, 2**31-1)
    integer(value['writer_generation'], 1, 2**31-1)
    for key, allowed in [('snapshot_read', [1, 2]), ('schema_read', ['schedule-c1', 'schedule-p1']),
                         ('write', ['INTERVAL', 'START_ONLY', 'DAY', 'BAND', 'snapshot-v1', 'snapshot-v2'])]:
        actual = value[key]
        require(type(actual) is list and 0 < len(actual) <= len(allowed))
        require(all(type(x) is type(allowed[0]) and x in allowed for x in actual))
        require(actual == [x for x in allowed if x in actual])
    return value


def status(value):
    keys(value, ['contract', 'database_incarnation', 'schema_contract', 'state_epoch',
                 'required_reader', 'floor_commit_id', 'floor_manifest_sha256',
                 'partial_v2_enabled', 'ever_partial_v2_enabled'])
    require(value['contract'] == STATUS)
    uuid(value['database_incarnation'])
    require(value['schema_contract'] in ['schedule-c1', 'schedule-p1'])
    integer(value['state_epoch'])
    integer(value['required_reader'], 1, 2**31-1)
    floor(value)
    for name in ['partial_v2_enabled', 'ever_partial_v2_enabled']:
        require(type(value[name]) is bool)
    if value['floor_commit_id'] is not None:
        require(value['required_reader'] >= 2)
    else:
        require(value['required_reader'] == 1)
    if value['partial_v2_enabled'] or value['ever_partial_v2_enabled']:
        require(value['schema_contract'] == 'schedule-p1' and value['floor_commit_id'] is not None)
    if value['partial_v2_enabled']:
        require(value['ever_partial_v2_enabled'])
    return value


def projection(value):
    keys(value, ['contract', 'admission', 'restore_generation', 'reconciled_restore_generation',
                 'database_incarnation', 'floor_commit_id', 'floor_manifest_sha256',
                 'minimum_state_epoch', 'minimum_reader', 'images'])
    require(value['contract'] == ADMISSION and value['admission'] in ['blocked', 'admitted'])
    integer(value['restore_generation'], 1)
    integer(value['reconciled_restore_generation'])
    require(value['reconciled_restore_generation'] <= value['restore_generation'])
    uuid(value['database_incarnation'])
    integer(value['minimum_state_epoch'])
    integer(value['minimum_reader'], 1, 2**31-1)
    floor(value)
    if value['floor_commit_id'] is not None:
        require(value['minimum_reader'] >= 2)
    else:
        require(value['minimum_reader'] == 1)
    require(type(value['images']) is list and len(value['images']) <= 8)
    seen, identities = set(), set()
    for image in value['images']:
        keys(image, ['image_digest', 'capabilities'])
        digest(image['image_digest'], True)
        cap = capabilities(image['capabilities'])
        identity = (cap['version'], cap['candidate'])
        require(image['image_digest'] not in seen and identity not in identities)
        seen.add(image['image_digest'])
        identities.add(identity)
    if value['admission'] == 'admitted':
        require(value['images'] and value['restore_generation'] == value['reconciled_restore_generation'])
    return value


def pairs(items):
    result = {}
    for key, value in items:
        require(key not in result, 'duplicate_key')
        result[key] = value
    return result


def decode(data):
    require(len(data) <= LIMIT, 'document_too_large')
    def no_float(_):
        raise Refused('noninteger_number')
    try:
        return json.loads(data.decode('utf-8'), object_pairs_hook=pairs,
                          parse_float=no_float, parse_constant=no_float)
    except Refused:
        raise
    except (UnicodeError, ValueError, RecursionError) as exc:
        raise Refused('invalid_json') from exc


@contextlib.contextmanager
def directory(path):
    path = Path(path)
    require(path.is_absolute(), 'absolute_path_required')
    require('..' not in path.parts, 'path_traversal')
    try:
        relative = path.relative_to(TRUST_ROOT)
    except ValueError as exc:
        raise Refused('untrusted_path') from exc
    fd = os.open(TRUST_ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for component in [None, *relative.parts]:
            if component is not None:
                new = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                os.close(fd)
                fd = new
            info = os.fstat(fd)
            require(info.st_uid == OWNER and stat.S_ISDIR(info.st_mode) and info.st_mode & 0o022 == 0,
                    'unprotected_directory')
        yield fd
    finally:
        os.close(fd)


def read(path, modes=(0o600,)):
    path = Path(path)
    with directory(path.parent) as parent:
        fd = os.open(path.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        with os.fdopen(fd, 'rb') as stream:
            info = os.fstat(stream.fileno())
            require(info.st_uid == OWNER and stat.S_ISREG(info.st_mode) and
                    stat.S_IMODE(info.st_mode) in modes and info.st_size <= LIMIT, 'unprotected_file')
            data = stream.read(LIMIT+1)
            require(len(data) <= LIMIT, 'document_too_large')
            return data


def load(path, modes=(0o600,)):
    return decode(read(path, modes))


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode() + b'\n'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def atomic(path, value, mode):
    path = Path(path)
    data = canonical(value)
    require(len(data) <= LIMIT)
    with directory(path.parent) as parent:
        try:
            info = os.stat(path.name, dir_fd=parent, follow_symlinks=False)
            require(info.st_uid == OWNER and stat.S_ISREG(info.st_mode) and
                    stat.S_IMODE(info.st_mode) == mode, 'unprotected_destination')
        except FileNotFoundError:
            pass
        temporary = '.precision-' + secrets.token_hex(16)
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode, dir_fd=parent)
        try:
            os.fchmod(fd, mode)
            with os.fdopen(fd, 'wb') as stream:
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, path.name, src_dir_fd=parent, dst_dir_fd=parent)
            os.fsync(parent)
        finally:
            try:
                os.unlink(temporary, dir_fd=parent)
            except FileNotFoundError:
                pass


def host(path):
    value = load(path)
    keys(value, ['contract', 'projection', 'controller_sha256', 'proof_sha256', 'pending_floor'])
    require(value['contract'] == AUTHORITY)
    projection(value['projection'])
    for field in ['controller_sha256', 'proof_sha256']:
        if value[field] is not None:
            digest(value[field])
    if value['projection']['admission'] == 'admitted':
        require(value['controller_sha256'] is not None and value['proof_sha256'] is not None)
    if value['pending_floor'] is not None:
        pending(value['pending_floor'])
    return value


def publish(authority, target, record):
    projection(record['projection'])
    with directory(Path(target).parent) as parent:
        require(stat.S_IMODE(os.fstat(parent).st_mode) == 0o755, 'projection_directory_mode')
    # A blocked projection is the first durable write. A crash cannot leave an
    # admitted runtime behind a successfully completed restore-fence operation.
    if record['projection']['admission'] == 'blocked':
        atomic(target, record['projection'], 0o444)
        atomic(authority, record, 0o600)
    else:
        atomic(authority, record, 0o600)
        atomic(target, record['projection'], 0o444)


def block(authority, target, current, expected_generation):
    try:
        record = host(authority)
        value = record['projection']
        require(value['restore_generation'] == expected_generation, 'generation_cas_failed')
        require(record['pending_floor'] is None, 'floor_reconciliation_required')
        integer(expected_generation+1, 1)
        status(current)
        if current['database_incarnation'] == value['database_incarnation']:
            value['minimum_state_epoch'] = max(value['minimum_state_epoch'], current['state_epoch'])
        value['restore_generation'] += 1
        value['admission'] = 'blocked'
    except FileNotFoundError:
        require(expected_generation == 0, 'generation_cas_failed')
        status(current)
        value = {'contract': ADMISSION, 'admission': 'blocked', 'restore_generation': 1,
                 'reconciled_restore_generation': 0, 'database_incarnation': current['database_incarnation'],
                 'floor_commit_id': current['floor_commit_id'], 'floor_manifest_sha256': current['floor_manifest_sha256'],
                 'minimum_state_epoch': current['state_epoch'], 'minimum_reader': current['required_reader'], 'images': []}
        record = {'contract': AUTHORITY, 'projection': value, 'controller_sha256': None,
                  'proof_sha256': None, 'pending_floor': None}
    publish(authority, target, record)


def manifest_image(manifest, cap):
    keys(manifest, ['ci_run_id', 'contract', 'expected_gates', 'git_sha', 'git_tree_sha',
                    'image', 'issues', 'published_at', 'release_tag', 'schema', 'version'])
    require(manifest['contract'] == 'mycfc/release-publication/v1')
    require(manifest['version'] == cap['version'] and manifest['git_sha'] == cap['candidate'], 'image_stamp_mismatch')
    keys(manifest['image'], ['repository', 'digest'])
    digest(manifest['image']['digest'], True)
    require(type(manifest['image']['repository']) is str and len(manifest['image']['repository']) <= 512)
    keys(manifest['schema'], ['migration_digest', 'ordered_migrations'])
    inventory = manifest['schema']['ordered_migrations']
    require(type(inventory) is list and inventory and all(type(x) is str and bool(re.fullmatch(r'(reset-baseline-v1|[0-9]{3,}_[A-Za-z0-9_-]+)', x)) for x in inventory))
    require(inventory == sorted(set(inventory)) and inventory.count('reset-baseline-v1') == 1)
    require(sha('\n'.join(inventory).encode()) == manifest['schema']['migration_digest'], 'inventory_mismatch')
    keys(manifest['expected_gates'], ['guardian_intake', 'privacy_worker'])
    require(type(manifest['expected_gates']['guardian_intake']) is bool and manifest['expected_gates']['privacy_worker'] is False,
            'retired_privacy_gate')
    return {'image_digest': manifest['image']['digest'], 'capabilities': cap}


def normal_contract(value, manifest):
    keys(manifest, ['ci_run_id', 'contract', 'expected_gates', 'git_sha', 'git_tree_sha',
                    'image', 'issues', 'published_at', 'release_tag', 'schema', 'version'])
    require(manifest['contract'] == 'mycfc/release-publication/v1')
    keys(manifest['schema'], ['migration_digest', 'ordered_migrations'])
    keys(value, ['contract', 'version', 'candidate', 'database', 'parent_digest', 'final_digest'])
    require(value['contract'] == 'mycfc/normal-release/v1' and value['database'] == 'mycfc')
    require(type(value['version']) is str and bool(VERSION.fullmatch(value['version'])))
    require(type(value['candidate']) is str and bool(re.fullmatch('[0-9a-f]{40}', value['candidate'])))
    digest(value['parent_digest'])
    digest(value['final_digest'])
    require(value['version'] == manifest['version'] and value['candidate'] == manifest['git_sha'] and
            value['final_digest'] == manifest['schema']['migration_digest'], 'normal_contract_manifest_mismatch')
    require(value['parent_digest'] != value['final_digest'], 'normal_contract_no_forward_parent')


def reconcile(authority, target, current, evidence_path, controller_path, releases):
    record = host(authority)
    value = record['projection']
    status(current)
    require(record['pending_floor'] is None, 'floor_reconciliation_required')
    evidence_bytes = read(evidence_path)
    evidence = decode(evidence_bytes)
    keys(evidence, ['contract', 'approval_reference', 'restore_generation', 'status_sha256',
                    'controller_sha256', 'manifest_sha256', 'checked_image_digests', 'checks'])
    require(evidence['contract'] == PROOF and evidence['restore_generation'] == value['restore_generation'])
    integer(evidence['restore_generation'], 1)
    require(type(evidence['approval_reference']) is str and bool(re.fullmatch(r'[A-Za-z0-9._/-]{1,100}', evidence['approval_reference'])))
    require(evidence['checks'] == CHECKS, 'reconciliation_not_proved')
    require(evidence['status_sha256'] == sha(canonical(current)), 'status_proof_mismatch')
    controller_hash = sha(read(controller_path, (0o600, 0o644, 0o755)))
    require(evidence['controller_sha256'] == controller_hash, 'controller_hash_mismatch')
    images, manifest_hashes = [], []
    for manifest_path, capability_path in releases:
        raw = read(manifest_path, (0o600, 0o644))
        images.append(manifest_image(decode(raw), capabilities(load(capability_path))))
        manifest_hashes.append(sha(raw))
    require(1 <= len(images) <= 8)
    require(evidence['manifest_sha256'] == manifest_hashes and
            evidence['checked_image_digests'] == [x['image_digest'] for x in images], 'image_evidence_mismatch')
    require(current['required_reader'] >= value['minimum_reader'], 'reader_floor_rewind')
    if current['database_incarnation'] != value['database_incarnation']:
        require(value['admission'] == 'blocked' and value['restore_generation'] > value['reconciled_restore_generation'],
                'restore_not_externally_fenced')
    if current['database_incarnation'] == value['database_incarnation']:
        require(current['state_epoch'] >= value['minimum_state_epoch'], 'epoch_rewind')
    if value['floor_commit_id'] is not None:
        require(current['floor_commit_id'] is not None, 'reader_floor_rewind')
    for image in images:
        cap = image['capabilities']
        require(cap['reader_generation'] >= current['required_reader'] and current['schema_contract'] in cap['schema_read'],
                'incompatible_image')
    value.update(admission='admitted', reconciled_restore_generation=value['restore_generation'],
                 database_incarnation=current['database_incarnation'], floor_commit_id=current['floor_commit_id'],
                 floor_manifest_sha256=current['floor_manifest_sha256'], minimum_state_epoch=current['state_epoch'],
                 minimum_reader=current['required_reader'], images=images)
    record.update(controller_sha256=controller_hash, proof_sha256=sha(evidence_bytes))
    publish(authority, target, record)


def admission_check(record, projected, current, controller_hash, image=None):
    value = projection(projected)
    status(current)
    require(record['pending_floor'] is None and value == record['projection'], 'host_projection_mismatch')
    require(record['controller_sha256'] == controller_hash, 'controller_hash_mismatch')
    require(value['admission'] == 'admitted', 'serving_blocked')
    require(value['restore_generation'] == value['reconciled_restore_generation'], 'restore_unreconciled')
    for name in ['database_incarnation', 'floor_commit_id', 'floor_manifest_sha256']:
        require(value[name] == current[name], 'database_binding_mismatch')
    require(current['state_epoch'] >= value['minimum_state_epoch'], 'epoch_rewind')
    require(current['required_reader'] >= value['minimum_reader'], 'reader_floor_rewind')
    if image is not None:
        require(image in value['images'], 'image_not_admitted')
        cap = image['capabilities']
        require(cap['reader_generation'] >= current['required_reader'] and current['schema_contract'] in cap['schema_read'],
                'incompatible_image')


def pending(value):
    keys(value, ['contract', 'database_incarnation', 'expected_state_epoch', 'floor_commit_id',
                 'floor_manifest_sha256', 'minimum_reader'])
    require(value['contract'] == 'mycfc/training-schedule-floor-request/v1')
    uuid(value['database_incarnation'])
    uuid(value['floor_commit_id'])
    digest(value['floor_manifest_sha256'])
    integer(value['expected_state_epoch'], 0, 2**63-2)
    integer(value['minimum_reader'], 2, 2**31-1)


def prepare_floor(authority, current, ident, manifest_sha, minimum):
    record = host(authority)
    value = record['projection']
    admission_check(record, value, current, record['controller_sha256'])
    request = {'contract': 'mycfc/training-schedule-floor-request/v1',
               'database_incarnation': current['database_incarnation'], 'expected_state_epoch': current['state_epoch'],
               'floor_commit_id': ident, 'floor_manifest_sha256': manifest_sha, 'minimum_reader': minimum}
    pending(request)
    require(minimum >= current['required_reader'] and all(x['capabilities']['reader_generation'] >= minimum for x in value['images']),
            'fallback_below_new_floor')
    record['pending_floor'] = request
    atomic(authority, record, 0o600)
    return request


def complete_floor(authority, target, current):
    record = host(authority)
    request = record['pending_floor']
    require(request is not None, 'no_pending_floor')
    status(current)
    for name in ['database_incarnation', 'floor_commit_id', 'floor_manifest_sha256']:
        require(current[name] == request[name], 'floor_cas_not_committed')
    require(current['required_reader'] == request['minimum_reader'] and
            current['state_epoch'] == request['expected_state_epoch']+1 and not current['partial_v2_enabled'],
            'floor_cas_not_committed')
    value = record['projection']
    value.update(floor_commit_id=current['floor_commit_id'], floor_manifest_sha256=current['floor_manifest_sha256'],
                 minimum_reader=current['required_reader'], minimum_state_epoch=current['state_epoch'])
    record['pending_floor'] = None
    publish(authority, target, record)


@contextlib.contextmanager
def controller_lock(path, inherited=None):
    path = Path(path)
    with directory(path.parent) as parent:
        if inherited is None:
            fd = os.open(path.name, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600, dir_fd=parent)
        else:
            fd = os.dup(inherited)
        try:
            info = os.fstat(fd)
            require(info.st_uid == OWNER and stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) in (0o600, 0o644),
                    'unsafe_controller_lock')
            linked = os.stat(path.name, dir_fd=parent, follow_symlinks=False)
            require(stat.S_ISREG(linked.st_mode) and (info.st_dev, info.st_ino) == (linked.st_dev, linked.st_ino),
                    'wrong_controller_lock')
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            yield
        finally:
            os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['block', 'reconcile', 'check', 'prepare-floor', 'complete-floor', 'normal-contract'])
    parser.add_argument('--authority', default='/etc/mycfc/deployment/training-schedule-host.json')
    parser.add_argument('--projection', default='/etc/mycfc/training-schedule-admission/admission.json')
    parser.add_argument('--lock-file', default='/run/mycfc-pull-release.lock')
    parser.add_argument('--lock-fd', type=int)
    parser.add_argument('--status')
    parser.add_argument('--normal-contract')
    parser.add_argument('--manifest')
    parser.add_argument('--controller')
    parser.add_argument('--evidence')
    parser.add_argument('--release', nargs=2, action='append', default=[], metavar=('MANIFEST', 'CAPABILITIES'))
    parser.add_argument('--expected-generation', type=int)
    parser.add_argument('--floor-id')
    parser.add_argument('--manifest-sha256')
    parser.add_argument('--minimum-reader', type=int)
    args = parser.parse_args()
    try:
        require(os.geteuid() == 0, 'root_required')
        with controller_lock(args.lock_file, args.lock_fd):
            if args.action == 'normal-contract':
                require(args.normal_contract and args.manifest)
                normal_contract(load(args.normal_contract), load(args.manifest, (0o600, 0o644)))
                print('precision_normal_contract=accepted')
                return 0
            require(args.status)
            current = status(load(args.status))
            if args.action == 'block':
                require(args.expected_generation is not None)
                block(args.authority, args.projection, current, args.expected_generation)
            elif args.action == 'reconcile':
                require(args.controller and args.evidence and args.release)
                reconcile(args.authority, args.projection, current, args.evidence, args.controller, args.release)
            elif args.action == 'prepare-floor':
                require(args.controller)
                admission_check(host(args.authority), load(args.projection, (0o444,)), current,
                                sha(read(args.controller, (0o600, 0o644, 0o755))))
                request = prepare_floor(args.authority, current, args.floor_id, args.manifest_sha256, args.minimum_reader)
                sys.stdout.buffer.write(canonical(request))
                return 0
            elif args.action == 'complete-floor':
                require(args.controller)
                require(host(args.authority)['controller_sha256'] == sha(read(args.controller, (0o600, 0o644, 0o755))),
                        'controller_hash_mismatch')
                complete_floor(args.authority, args.projection, current)
            else:
                require(args.controller and len(args.release) == 1)
                manifest_path, capability_path = args.release[0]
                image = manifest_image(load(manifest_path, (0o600, 0o644)), capabilities(load(capability_path)))
                admission_check(host(args.authority), load(args.projection, (0o444,)), current,
                                sha(read(args.controller, (0o600, 0o644, 0o755))), image)
        print('precision_admission=' + ('admitted' if args.action == 'check' else 'updated'))
        return 0
    except (OSError, Refused, TypeError, KeyError, OverflowError) as exc:
        reason = str(exc) if isinstance(exc, Refused) else 'unsafe_or_missing_input'
        print('precision_admission=refused reason=' + reason, file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
