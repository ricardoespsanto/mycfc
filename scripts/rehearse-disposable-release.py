#!/usr/bin/env python3
"""Local-only counterexample: disposable reset works; tagged rollback is unsafe.

No release capability is installed. Explicit local SQL reset is NOT a candidate
migration hook. Remote discovery/provenance and guardian binding are excluded.
All credentials, identities, databases and containers here are synthetic.
"""
import json
import os
import pathlib
import re
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parents[1]
DB = 'disposable_rehearsal'
PG = 'postgres:16.9-alpine3.21'


def validate_local_target(container, database):
    if not re.fullmatch(r'mycfc-disposable-local-[a-f0-9]+-pg', container) or database != DB:
        raise ValueError('only the newly created disposable rehearsal target is allowed')


def command(*args, data=None, cwd=ROOT, check=True) -> Any:
    result = subprocess.run(args, input=data, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode:
        raise RuntimeError('command failed: ' + ' '.join(args) + '\n' + result.stderr.decode())
    return result.stdout if check else result


def wait(fn):
    deadline = time.monotonic() + 45
    while True:
        try:
            return fn()
        except Exception:
            if time.monotonic() >= deadline:
                raise
            time.sleep(.25)


def main():
    folder = pathlib.Path(tempfile.mkdtemp(prefix='mycfc-disposable-evidence-', dir=os.environ['TMPDIR']))
    prefix = 'mycfc-disposable-local-' + uuid.uuid4().hex[:10]
    pg, old = prefix + '-pg', prefix + '-old'
    validate_local_target(pg, DB)
    images = []
    state = folder / 'state'
    state.mkdir()
    compose = folder / 'compose.yaml'
    report: dict[str, Any] = {'remote_discovery_exercised': False, 'release_path_implemented': False}

    def sql(text, role='postgres', check=True):
        return command('docker', 'exec', '-i', pg, 'psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', DB,
                       data=('SET ROLE ' + role + ';' + text).encode(), check=check)

    def scalar(text):
        return sql(text).decode().strip().removeprefix('SET\n')

    def http(name, path='/health/ready'):
        port = command('docker', 'port', name, '8080/tcp').decode().strip().rsplit(':', 1)[1]
        with urllib.request.urlopen('http://127.0.0.1:' + port + path, timeout=5) as response:
            assert response.status == 200
            return response.read()

    command('docker', 'network', 'create', prefix)
    try:
        # Compile exact tagged predecessor, without touching another checkout.
        predecessor = folder / 'predecessor'
        predecessor.mkdir()
        archive = folder / 'predecessor.tar'
        archive.write_bytes(command('git', 'archive', 'v1.25.9'))
        with tarfile.open(archive) as source:
            source.extractall(predecessor, filter='data')
        for label, checkout in [('old', predecessor), ('candidate', ROOT)]:
            context = folder / label
            context.mkdir()
            command('env', 'CGO_ENABLED=0', 'go', 'build', '-trimpath', '-o', str(context / 'server'), './cmd/server', cwd=checkout)
            if label == 'candidate':
                command('env', 'CGO_ENABLED=0', 'go', 'build', '-trimpath', '-o', str(context / 'admin'), './cmd/admin', cwd=checkout)
            (context / 'Dockerfile').write_text('FROM gcr.io/distroless/static-debian12:nonroot\nCOPY server /app/server\n' + ('COPY admin /app/admin\n' if label == 'candidate' else '') + 'ENTRYPOINT ["/app/server"]\n')
            image = prefix + '-' + label
            command('docker', 'build', '-q', '-t', image, str(context))
            images.append(image)
        old_image, candidate = images
        command('docker', 'run', '-d', '--name', pg, '--network', prefix, '-e', 'POSTGRES_PASSWORD=synthetic', PG)
        wait(lambda: command('docker', 'exec', pg, 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres'))
        command('docker', 'exec', pg, 'createdb', '-U', 'postgres', DB)
        common = 'APP_ENV=test\nAPP_DB_USER=mycfc_app\nAPP_DB_PASSWORD=synthetic\nMIGRATION_DB_USER=mycfc_migration\nMIGRATION_DB_PASSWORD=synthetic\n'
        for label, role in [('bootstrap', 'postgres'), ('migration', 'mycfc_migration')]:
            (folder / (label + '.env')).write_text(common + 'DATABASE_URL=postgres://' + role + ':synthetic@' + pg + ':5432/' + DB + '?sslmode=disable\n')
        command('docker', 'run', '--rm', '--network', prefix, '--env-file', str(folder / 'bootstrap.env'), old_image, 'bootstrap-db')
        sql((predecessor / 'internal/db/schema.sql').read_text(), 'mycfc_migration')
        inventory = sorted(['reset-baseline-v1'] + [p.stem for p in (predecessor / 'internal/db/migrations').glob('*.sql')])
        assert len(inventory) == 66, inventory
        sql('CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES ' + ','.join("('" + v + "')" for v in inventory) + '; CREATE TABLE public.synthetic_disposable_history(value text); INSERT INTO public.synthetic_disposable_history VALUES (\'discard me\');', 'mycfc_migration')
        env = [line for line in (ROOT / '.env.example').read_text().splitlines() if line and not line.startswith(('#', 'DATABASE_URL=', 'APP_ENV='))]
        env += ['APP_ENV=test', 'DATABASE_URL=postgres://mycfc_app:synthetic@' + pg + ':5432/' + DB + '?sslmode=disable']
        (folder / 'app.env').write_text('\n'.join(env) + '\n')
        command('docker', 'run', '-d', '--name', old, '--network', prefix, '-p', '127.0.0.1::8080', '--env-file', str(folder / 'app.env'), old_image)
        wait(lambda: http(old))
        assert scalar('SELECT count(*) FROM mycfc_meta.schema_migrations') == '66'
        # Preflight runs before ANY destruction, under the actual migration role.
        unsupported = sql("DO $$ BEGIN IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname=current_user) THEN RAISE EXCEPTION 'reset preflight refuses unsupported privilege'; END IF; END $$; DROP SCHEMA public CASCADE;", 'mycfc_migration', False)
        assert unsupported.returncode and b'reset preflight refuses unsupported privilege' in unsupported.stderr
        assert scalar('SELECT count(*) FROM synthetic_disposable_history') == '1'
        report['unsupported_privilege_refused_before_destruction'] = True
        report['bootstrap_role_capabilities'] = json.loads(scalar("SELECT json_build_object('superuser',rolsuper,'createdb',rolcreatedb,'createrole',rolcreaterole) FROM pg_roles WHERE rolname='postgres'"))
        report['migration_role_capabilities'] = json.loads(scalar("SELECT json_build_object('superuser',rolsuper,'createdb',rolcreatedb,'createrole',rolcreaterole) FROM pg_roles WHERE rolname='mycfc_migration'"))
        # Deliberately explicit LOCAL reset. Not shipped in bootstrap/migrations.
        namespaces = scalar("SELECT nspname FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname <> 'information_schema' ORDER BY nspname").splitlines()
        sql('BEGIN; DO $$ BEGIN IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname=current_user) THEN RAISE EXCEPTION \'unsupported privilege\'; END IF; END $$;' + ''.join('DROP SCHEMA "' + n + '" CASCADE;' for n in namespaces) + 'CREATE SCHEMA public; COMMIT;')
        # Extract unchanged installed poller DB phases and EXIT recovery.
        poller = command('git', 'show', 'v1.25.9:deployment/pull-release.sh').decode()
        functions = poller[poller.index('log() {'):poller.index('if [ "$(id -u)" -ne 0 ]')]
        phases = poller[poller.index('run_phase database_bootstrap '):poller.index('# Idempotent defence in depth;')]
        assert phases.count('run_phase ') == 2
        compose.write_text('name: ' + prefix + '\nservices:\n  db-bootstrap:\n    image: ' + candidate + '\n    command: [bootstrap-db]\n    env_file: [' + str(folder / 'bootstrap.env') + ']\n    networks: [local]\n    profiles: [release]\n  migrate:\n    image: ' + candidate + '\n    command: [migrate]\n    env_file: [' + str(folder / 'migration.env') + ']\n    networks: [local]\n    profiles: [release]\n  app-green:\n    image: ' + candidate + '\n    env_file: [' + str(folder / 'app.env') + ']\n    networks: [local]\n    profiles: [green]\n    ports: ["127.0.0.1::8080"]\nnetworks:\n  local:\n    external: true\n    name: ' + prefix + '\n')
        previous, current = folder / 'env.previous', folder / 'env'
        previous.write_text('LOCAL_RELEASE=predecessor\n')
        current.write_text('LOCAL_RELEASE=candidate\n')
        header = '#!/bin/sh\nset -eu\n' + '\n'.join([
            'env_file=' + str(current), 'compose_file=' + str(compose), 'state_dir=' + str(state), 'runtime_dir=' + str(folder),
            'backup_file=' + str(previous), 'route_backup=', 'route_switched=false', 'traffic_switched=false',
            'candidate_started=false', 'candidate_slot=', 'release_updated=true', 'privacy_worker_stopped=false',
            'release_digest=', 'release_version=', 'sha=', 'current_phase=initialization']) + '\n'
        script = folder / 'tagged-phases.sh'
        script.write_text(header + functions + '\ntrap rollback EXIT HUP INT TERM\n' + phases + '\nrelease_updated=false\ntrap - EXIT HUP INT TERM\n')
        command('sh', str(script))
        final_inventory = json.loads(command('docker', 'run', '--rm', candidate, 'schema-inventory'))
        assert scalar('SELECT version FROM mycfc_meta.schema_migrations ORDER BY version').splitlines() == final_inventory
        assert scalar("SELECT to_regclass('public.synthetic_disposable_history') IS NULL") == 't'
        report['explicit_local_reset_then_tagged_candidate_bootstrap_migrate'] = True
        report['predecessor_markers'], report['final_markers'] = len(inventory), len(final_inventory)
        # Established admin command with a local synthetic password file only.
        password = folder / 'synthetic-admin-password'
        password.write_text('Synthetic-Rehearsal-Password-Only!\n')
        password.chmod(0o644)
        command('docker', 'run', '--rm', '--network', prefix, '--env-file', str(folder / 'app.env'),
                '-e', 'MYCFC_ADMIN_PASSWORD_FILE=/synthetic-password', '-v', str(password) + ':/synthetic-password:ro',
                '--entrypoint', '/app/admin', candidate, 'create', '--email', 'admin@example.test', '--name', 'Synthetic Admin', '--date-of-birth', '1990-01-01')
        assert scalar("SELECT count(*) FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id WHERE u.is_active AND r.code='ADMIN'") == '1'
        sql("CREATE TABLE public.synthetic_candidate_data(value text); INSERT INTO public.synthetic_candidate_data VALUES ('must survive retry');", 'mycfc_migration')
        command('sh', str(script))
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        assert scalar("SELECT NOT EXISTS(SELECT 1 FROM privacy_request_activation WHERE enabled OR fulfilment_ready) AND (SELECT engaged FROM privacy_worker_kill_switch WHERE singleton) AND NOT EXISTS(SELECT 1 FROM guardian_application_intake_release WHERE enabled)") == 't'
        assert scalar("SELECT has_table_privilege('mycfc_app','public.users','SELECT') AND NOT has_table_privilege('mycfc_app','public.privacy_worker_kill_switch','UPDATE') AND NOT has_schema_privilege('mycfc_app','classification_erasure','USAGE')") == 't'
        report['fresh_web_acl_and_retired_privacy_guardian_gates_closed'] = True
        report['tagged_database_phase_retry_does_not_wipe'] = True
        # Actual candidate process starts and serves fresh schema before failure.
        command('docker', 'compose', '-f', str(compose), '--profile', 'green', 'up', '-d', '--no-deps', 'app-green')
        green = command('docker', 'compose', '-f', str(compose), 'ps', '-q', 'app-green').decode().strip()
        wait(lambda: http(green))
        assert b'/assets/app-' in http(green, '/login')
        report['fresh_candidate_ready_and_login'] = True
        # Real fault: stop the validated candidate, then run exact tagged EXIT
        # recovery from the pre-switch candidate_validation state.
        command('docker', 'stop', green)
        script.write_text(header + functions + '\ntrap rollback EXIT HUP INT TERM\ncandidate_slot=green\ncandidate_started=true\ncurrent_phase=candidate_validation\nexit 1\n')
        failed = command('sh', str(script), check=False)
        (folder / 'tagged-failure.log').write_bytes(failed.stdout + failed.stderr)
        assert failed.returncode != 0
        assert current.read_bytes() == previous.read_bytes()
        assert json.loads(command('docker', 'inspect', old))[0]['State']['Running']
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        wait(lambda: http(old))
        report['tagged_candidate_failure_restores_old_config_not_database'] = True
        report['old_app_still_ready_against_replaced_schema'] = True
        # The exact old membership writer's conflict arbiter is absent. Readiness
        # returning 200 is not fail-closed schema compatibility.
        query = (predecessor / 'internal/db/queries/memberships.sql').read_text()
        assert 'ON CONFLICT (user_id, season_id, programme_id)' in query
        assert scalar("SELECT count(*) FROM pg_constraint WHERE conname='user_memberships_user_season_programme_unique'") == '0'
        probe = sql('EXPLAIN INSERT INTO user_memberships(user_id,season_id,programme_id) VALUES (gen_random_uuid(),gen_random_uuid(),gen_random_uuid()) ON CONFLICT (user_id,season_id,programme_id) DO NOTHING;', 'mycfc_app', False)
        assert probe.returncode and b'no unique or exclusion constraint' in probe.stderr
        report['old_membership_writer_is_incompatible'] = True
        report['old_fallback_is_fail_closed'] = False
        report['admin_reseed_local_cli_only_not_installed_release_automation'] = True
        # Concrete no-host-upgrade option: a distinct candidate web principal,
        # using the same existing secret, can leave the predecessor fail-closed.
        # This is an explicit local experiment, NOT an installed release path.
        candidate_role = 'mycfc_candidate_web'
        for label in ['bootstrap', 'migration']:
            path = folder / (label + '.env')
            path.write_text(path.read_text().replace('APP_DB_USER=mycfc_app', 'APP_DB_USER=' + candidate_role))
        app_env = folder / 'app.env'
        app_env.write_text(app_env.read_text().replace('APP_DB_USER=mycfc_app', 'APP_DB_USER=' + candidate_role).replace('postgres://mycfc_app:', 'postgres://' + candidate_role + ':'))
        sql("ALTER ROLE mycfc_app NOLOGIN; REVOKE CONNECT ON DATABASE disposable_rehearsal FROM mycfc_app; SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='mycfc_app' AND pid<>pg_backend_pid();")
        script.write_text(header + functions + '\ntrap rollback EXIT HUP INT TERM\n' + phases + '\nrelease_updated=false\ntrap - EXIT HUP INT TERM\n')
        command('sh', str(script))
        assert scalar("SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'") == 'f'
        command('docker', 'compose', '-f', str(compose), '--profile', 'green', 'up', '-d', '--no-deps', '--force-recreate', 'app-green')
        green = command('docker', 'compose', '-f', str(compose), 'ps', '-q', 'app-green').decode().strip()
        wait(lambda: http(green))
        def old_denied():
            try:
                http(old)
            except urllib.error.HTTPError as error:
                assert error.code == 503
                return
            raise AssertionError('old web principal still ready')
        wait(old_denied)
        report['local_distinct_web_role_option_new_candidate_ready_old_ready_503'] = True
        report['role_handoff_installed_release_guard_admin_carryover_not_implemented'] = True
        (folder / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report, indent=2))
    finally:
        subprocess.run(['docker', 'compose', '-f', str(compose), '--profile', 'green', 'down'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for name in [old, pg]:
            subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['docker', 'network', 'rm', prefix], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for image in images:
            subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        print('Isolated resources cleaned; evidence:', folder, flush=True)


if __name__ == '__main__':
    main()
