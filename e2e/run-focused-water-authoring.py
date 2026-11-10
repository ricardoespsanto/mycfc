#!/usr/bin/env python3
"""Run three focused water browser cases against a genuinely new owned local DB.
Real bootstrap/migrate/fresh authoring preparation/hardening, real app/session/CSRF.
Never adopts, resets, repairs or upgrades an existing database or Docker resource.
"""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import re
import shutil
import socket
import subprocess
import threading
import time
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('evidence', type=pathlib.Path)
parser.add_argument('--name', required=True)
parser.add_argument('--pg-port', type=int, required=True)
parser.add_argument('--app-port', type=int, required=True)
parser.add_argument('--runtime-manifest', type=pathlib.Path, action='append', required=True)
parser.add_argument('--fixture-date', type=datetime.date.fromisoformat, default=datetime.date.today())
parser.add_argument('--full-create', action='store_true', help='Also run the three generic full-CREATE cases on extended synthetic fixtures')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[1]
out = args.evidence.resolve()
if not re.fullmatch(r'mycfc-authoring-browser-[a-z0-9]{12,32}', args.name):
    parser.error('new unique focused-authoring fixture name required')
if out == root or root in out.parents:
    parser.error('evidence must be outside checkout')
if args.pg_port == args.app_port or any(port < 1024 or port > 65535 for port in [args.pg_port, args.app_port]):
    parser.error('distinct unprivileged TCP ports required')
name, volume, browser = args.name, args.name + '-pgdata', args.name + '-playwright'
label = 'mycfc.focused-water-browser-owner'
resources = [('container', name), ('container', browser), ('volume', volume)]
owned = {}
app = worker = None
stop = threading.Event()


def docker_info():
    subprocess.run(['docker', 'info', '--format', '{{.ServerVersion}}'], check=True, stdout=subprocess.DEVNULL)


def inspect(kind, item):
    result = subprocess.run(['docker', kind, 'inspect', item], capture_output=True, text=True)
    if result.returncode == 0:
        return json.loads(result.stdout)[0]
    if 'no such' in result.stderr.lower() and result.returncode == 1:
        return None
    raise RuntimeError('could not establish Docker resource state: ' + item + ': ' + result.stderr)


docker_info()
for kind, item in resources:
    if inspect(kind, item) is not None:
        raise SystemExit('refusing existing resource ' + item)
for port in [args.pg_port, args.app_port]:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', port))
out.mkdir(mode=0o700, parents=True, exist_ok=False)
(out / 'absence.json').write_text(json.dumps({'resources': resources, 'absent_before_creation': True, 'ports_free': [args.pg_port, args.app_port]}))


def run(command, log, **kw):
    with (out / log).open('w') as stream:
        subprocess.run(command, cwd=root, stdout=stream, stderr=subprocess.STDOUT, check=True, **kw)


def sql(query, variables=()):
    return subprocess.check_output(['docker', 'exec', '-i', name, 'psql', '-U', 'authoring_bootstrap', '-d', 'mycfc', '-v', 'ON_ERROR_STOP=1', '-At', *variables], input=query, text=True)


def facts():
    # One read-only SQL statement gives a consistent snapshot. No mutation endpoint.
    return json.loads(sql("""
SELECT jsonb_build_object(
 'plans',(SELECT jsonb_object_agg(p.id::text,jsonb_build_object('updated_at',p.updated_at,'xmin',p.xmin::text,'sessions',
   COALESCE((SELECT jsonb_agg(to_jsonb(s)||jsonb_build_object(
     'local_start',to_char(s.starts_at AT TIME ZONE 'Europe/Lisbon','YYYY-MM-DD HH24:MI'),
     'local_end',to_char(s.ends_at AT TIME ZONE 'Europe/Lisbon','YYYY-MM-DD HH24:MI'),
     'segments',COALESCE((SELECT jsonb_agg(to_jsonb(g)||jsonb_build_object('blocks',
       COALESCE((SELECT jsonb_agg(to_jsonb(b)||jsonb_build_object(
          'water_prescription',(SELECT to_jsonb(w) FROM water_block_prescriptions w WHERE w.block_id=b.id),
          'gym_prescription',(SELECT to_jsonb(w) FROM gym_block_prescriptions w WHERE w.block_id=b.id),
          'water_steps',COALESCE((SELECT jsonb_agg(to_jsonb(w) ORDER BY w.parent_step_id NULLS FIRST,w.position,w.id) FROM water_work_steps w WHERE w.block_id=b.id),'[]'::jsonb),
          'gym_exercises',COALESCE((SELECT jsonb_agg(to_jsonb(e) ORDER BY e.position,e.id) FROM gym_exercises e WHERE e.block_id=b.id),'[]'::jsonb))
         ORDER BY b.position,b.id) FROM training_segment_blocks b WHERE b.segment_id=g.id),'[]'::jsonb))
       ORDER BY g.position,g.id) FROM training_session_segments g WHERE g.session_id=s.id),'[]'::jsonb))
     ORDER BY s.id) FROM training_sessions s WHERE s.plan_id=p.id),'[]'::jsonb))) FROM training_plans p),
 'publications',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM training_plan_publications p),'[]'::jsonb),
 'prescriptions',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM training_prescriptions p),'[]'::jsonb),
 'outcomes',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY o.session_id,o.user_id) FROM training_session_outcomes o),'[]'::jsonb),
 'profiles',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM water_intensity_profiles p),'[]'::jsonb),
 'zones',COALESCE((SELECT jsonb_agg(to_jsonb(z) ORDER BY z.profile_id,z.position,z.id) FROM water_intensity_zones z),'[]'::jsonb));
"""))


def runtime_hashes():
    manifest = {}
    for path in args.runtime_manifest:
        document = json.loads(path.read_text())
        entries = document.get('files', document.get('minimum_files', {}))
        if 'minimum_Makefile_sha256' in document:
            entries = dict(entries, Makefile=document['minimum_Makefile_sha256'])
        for relative, digest in entries.items():
            if not isinstance(relative, str) or pathlib.PurePosixPath(relative).is_absolute() or '..' in pathlib.PurePosixPath(relative).parts:
                raise RuntimeError('unsafe runtime manifest filename')
            if relative in manifest and manifest[relative] != digest:
                raise RuntimeError('conflicting runtime manifest filename ' + relative)
            manifest[relative] = digest
    if not manifest:
        raise RuntimeError('runtime hash inventory empty')
    actual = {f: hashlib.sha256((root / f).read_bytes()).hexdigest() for f in manifest}
    if actual != manifest:
        raise RuntimeError('frozen runtime changed')
    return actual


def test_hashes():
    return {f: hashlib.sha256((root / f).read_bytes()).hexdigest() for f in [
        'e2e/focused-water-authoring.spec.mjs', 'e2e/focused-water-authoring-seed.sql', 'e2e/run-focused-water-authoring.py',
        'package.json', 'package-lock.json', 'playwright.config.mjs', *([
            'e2e/full-workout-authoring.spec.mjs', 'e2e/full-workout-authoring-seed.sql', 'e2e/run-full-workout-authoring.py'
        ] if args.full_create else [])]}


try:
    runtime, tests = runtime_hashes(), test_hashes()
    (out / 'source-manifest.json').write_text(json.dumps(runtime, indent=2))
    (out / 'test-manifest.json').write_text(json.dumps(tests, indent=2))
    run(['docker', 'volume', 'create', '--label', label + '=' + name, volume], 'volume.log')
    owned[volume] = volume
    run(['docker', 'run', '-d', '--name', name, '--label', label + '=' + name,
         '-e', 'POSTGRES_USER=authoring_bootstrap', '-e', 'POSTGRES_PASSWORD=synthetic-bootstrap', '-e', 'POSTGRES_DB=mycfc',
         '-p', f'127.0.0.1:{args.pg_port}:5432', '-v', volume + ':/var/lib/postgresql/data',
         'postgres:16.9', '-c', 'timezone=Europe/Lisbon'], 'container.log')
    bound = inspect('container', name)
    owned[name] = bound['Id']
    assert bound['Config']['Labels'][label] == name
    assert bound['NetworkSettings']['Ports']['5432/tcp'] == [{'HostIp': '127.0.0.1', 'HostPort': str(args.pg_port)}]
    assert any(m.get('Name') == volume for m in bound['Mounts'])
    assert inspect('volume', volume)['Labels'][label] == name
    for _ in range(100):
        # The image's temporary init server accepts Unix sockets before creating POSTGRES_DB.
        # Require the final TCP server and the actual configured database, not pg_isready.
        if subprocess.run(['docker', 'exec', '-e', 'PGPASSWORD=synthetic-bootstrap', name, 'psql', '-w', '-h', '127.0.0.1', '-U', 'authoring_bootstrap', '-d', 'mycfc', '-v', 'ON_ERROR_STOP=1', '-Atqc', 'SELECT 1'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            break
        time.sleep(.2)
    else:
        raise RuntimeError('owned PostgreSQL not ready')
    binding = json.loads(sql("SELECT json_build_object('system_identifier',system_identifier::text,'database',current_database(),'bootstrap',current_user) FROM pg_control_system()"))
    binding.update(container_id=bound['Id'], volume=volume, endpoint=f'127.0.0.1:{args.pg_port}', owner=name)
    (out / 'target-binding.json').write_text(json.dumps(binding, indent=2))
    run(['go', 'build', '-o', str(out / 'server'), './cmd/server'], 'build.log')
    env = {k: v for k, v in os.environ.items() if k in ['PATH', 'HOME', 'TMPDIR', 'LANG', 'LC_ALL', 'TZ']}
    for line in (root / '.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            env[key] = value.strip('"')
    base = f'http://127.0.0.1:{args.app_port}'
    env.update(APP_ENV='test', APP_VERSION='dev', GIT_SHA='0' * 40, PORT=str(args.app_port), BASE_URL=base,
               POSTGRES_USER='authoring_bootstrap', POSTGRES_PASSWORD='synthetic-bootstrap', APP_DB_USER='authoring_web', APP_DB_PASSWORD='synthetic-web',
               MIGRATION_DB_USER='authoring_migration', MIGRATION_DB_PASSWORD='synthetic-migration', MEDIA_CLEANUP_DB_USER='authoring_media', MEDIA_CLEANUP_DB_PASSWORD='synthetic-media',
               DATA_RETENTION_DB_USER='authoring_retention', DATA_RETENTION_DB_PASSWORD='synthetic-retention', AWS_EC2_METADATA_DISABLED='true', SMTP_HOST='127.0.0.1', SMTP_PORT='1',
               S3_ENDPOINT='http://127.0.0.1:1', DATA_RIGHTS_CONTACT='privacy@example.test', TRAINING_SCHEDULE_ADMISSION_FILE='')
    for key in ['CONSENT_TERMS_URL', 'CONSENT_IMAGE_URL', 'CONSENT_MINOR_URL', 'PRIVACY_NOTICE_URL', 'COOKIE_NOTICE_URL', 'GALLERY_URL']:
        env[key] = env[key].replace('http://localhost:8080', base)
    dsn = lambda user, password: f'postgres://{user}:{password}@127.0.0.1:{args.pg_port}/mycfc?sslmode=disable'
    for command, user, password in [('bootstrap-db', 'authoring_bootstrap', 'synthetic-bootstrap'), ('migrate', 'authoring_migration', 'synthetic-migration'),
                                    ('prepare-local-recipient-floor', 'authoring_migration', 'synthetic-migration'), ('harden-db', 'authoring_bootstrap', 'synthetic-bootstrap')]:
        run([str(out / 'server'), command], command + '.log', env=dict(env, DATABASE_URL=dsn(user, password)))
    week = args.fixture_date + datetime.timedelta(days=7 - args.fixture_date.weekday())
    params = {'fixture_date': args.fixture_date.isoformat(), 'fixture_start': (args.fixture_date - datetime.timedelta(days=365)).isoformat(),
              'fixture_end': (args.fixture_date + datetime.timedelta(days=400)).isoformat(), 'fixture_week': week.isoformat()}
    variables = [v for key, value in params.items() for v in ['-v', key + '=' + value]]
    seed = (root / 'e2e/focused-water-authoring-seed.sql').read_text()
    if args.full_create:
        seed += '\n' + (root / 'e2e/full-workout-authoring-seed.sql').read_text()
    (out / 'seed.log').write_text(sql(seed, variables))
    (out / 'fixture.json').write_text(json.dumps({'days': [(week + datetime.timedelta(days=i * 7)).isoformat() for i in range(6 if args.full_create else 3)], 'synthetic_dates': params, 'actual_club_season_dates': 'unknown, configurable', 'full_create': args.full_create, 'synthetic_profile': '39139900-0000-0000-0000-000000000900'}))
    state = json.loads(sql("SELECT json_build_object('ledger_count',(SELECT count(*) FROM mycfc_meta.schema_migrations),'ledger_versions',(SELECT json_agg(version ORDER BY version) FROM mycfc_meta.schema_migrations),'floor',(SELECT required_reader FROM training_schedule_control.state),'schema',(SELECT schema_contract FROM training_schedule_control.state),'partial_enabled',(SELECT partial_v2_enabled FROM training_schedule_control.state),'database',current_database())"))
    # Match actual source go:embed migrations/*.sql plus the explicit baseline marker.
    expected_versions = sorted(['reset-baseline-v1', *[path.stem for path in (root / 'internal/db/migrations').glob('*.sql')]])
    assert state['floor'] == 3 and state['schema'] == 'schedule-p1' and state['partial_enabled'] is True
    assert state['ledger_count'] == len(expected_versions) and state['ledger_versions'] == expected_versions
    state['expected_source_inventory'] = expected_versions
    (out / 'prepared-state.json').write_text(json.dumps(state))
    env['DATABASE_URL'] = dsn('authoring_web', 'synthetic-web')
    with (out / 'app.log').open('w') as stream:
        app = subprocess.Popen([str(out / 'server')], cwd=root, env=env, stdout=stream, stderr=subprocess.STDOUT)
    for _ in range(100):
        if app.poll() is not None:
            raise RuntimeError('app exited; see app.log')
        try:
            if urllib.request.urlopen(base + '/health/ready').status == 200:
                break
        except Exception:
            time.sleep(.2)
    else:
        raise RuntimeError('app readiness failed')

    def control():
        done = set()
        while not stop.wait(.05):
            for request in out.glob('*-ready.json'):
                stem = request.name.removesuffix('-ready.json')
                if stem not in done:
                    try:
                        result = facts()
                    except Exception as error:
                        result = {'error': str(error)}
                    (out / (stem + '-done.json')).write_text(json.dumps(result))
                    done.add(stem)

    worker = threading.Thread(target=control, daemon=True)
    worker.start()
    # Browser executes exact test/config copies with pinned lockfile in private scratch;
    # never installs dependencies or creates node_modules in a collaborator checkout.
    source = out / 'browser-source'
    (source / 'e2e').mkdir(parents=True)
    for filename in ['package.json', 'package-lock.json', 'playwright.config.mjs']:
        shutil.copyfile(root / filename, source / filename)
    specs = ['e2e/focused-water-authoring.spec.mjs']
    if args.full_create:
        specs.append('e2e/full-workout-authoring.spec.mjs')
    for relative in specs:
        shutil.copyfile(root / relative, source / relative)
    run(['npm', 'ci', '--ignore-scripts', '--prefix', str(source)], 'dependencies.log')
    run(['docker', 'run', '--rm', '--name', browser, '--cidfile', str(out / 'browser.cid'), '--label', label + '=' + name, '--network', 'host',
         '--user', f'{os.getuid()}:{os.getgid()}', '-e', 'HOME=/tmp', '-e', 'E2E_BASE_URL=' + base, '-e', 'E2E_FOCUSED_WATER_AUTHORING=1',
         '-e', 'E2E_FOCUSED_WATER_EVIDENCE=/evidence', '-e', 'E2E_FULL_WORKOUT_AUTHORING=' + ('1' if args.full_create else '0'), '-e', 'E2E_JSON_OUTPUT=/evidence/browser.json',
         '-v', str(source) + ':/src:ro', '-v', str(out) + ':/evidence', '-w', '/src', 'mcr.microsoft.com/playwright:v1.63.0-noble',
         'node', 'node_modules/@playwright/test/cli.js', 'test', *specs, '--workers=1', '--retries=0', '--output=/evidence/artifacts'], 'browser.log')
    assert runtime == runtime_hashes()
    assert tests == test_hashes()
    (out / 'final-facts.json').write_text(json.dumps(facts(), indent=2))
    (out / 'summary.json').write_text(json.dumps({'browser_passed': True, 'runtime_unchanged': True, 'test_source_unchanged': True,
          'binary_sha256': hashlib.sha256((out / 'server').read_bytes()).hexdigest(), 'runtime_role': 'authoring_web',
          'routes': 'actual app.New SCS CSRF SQL; synthetic seed only', 'manual_acceptance': False}, indent=2))
    print(out)
except Exception as error:
    (out / 'attempt-failed.json').write_text(json.dumps({'error': repr(error), 'preserved_logs': True}, indent=2))
    raise
finally:
    stop.set()
    if worker:
        worker.join(timeout=2)
    if app:
        app.terminate()
        try:
            app.wait(timeout=10)
        except subprocess.TimeoutExpired:
            app.kill()
            app.wait(timeout=5)
    cleanup = {'app_stopped': app is None or app.poll() is not None, 'resources': {}, 'errors': []}
    try:
        docker_info()
        for kind, item in resources:
            data = inspect(kind, item)
            if data is not None:
                labels = data['Config']['Labels'] if kind == 'container' else data['Labels']
                expected_id = owned.get(item)
                if item == browser and (out / 'browser.cid').exists():
                    expected_id = (out / 'browser.cid').read_text().strip()
                actual_id = data['Id'] if kind == 'container' else data['Name']
                if labels.get(label) != name or not expected_id or actual_id != expected_id:
                    raise RuntimeError('refuse cleanup of mismatched/unowned resource ' + item)
                if item == name:
                    with (out / 'postgres.log').open('w') as stream:
                        subprocess.run(['docker', 'logs', item], stdout=stream, stderr=subprocess.STDOUT, check=True)
                subprocess.run(['docker', kind, 'rm', *(['-f'] if kind == 'container' else []), item], check=True, stdout=subprocess.DEVNULL)
            cleanup['resources'][item] = {'absent': inspect(kind, item) is None}
    except Exception as error:
        cleanup['errors'].append(str(error))
    (out / 'cleanup.json').write_text(json.dumps(cleanup, indent=2))
    if cleanup['errors'] or not cleanup['app_stopped'] or not all(value['absent'] for value in cleanup['resources'].values()) or len(cleanup['resources']) != 3:
        raise RuntimeError('cleanup not verified; see cleanup.json')
