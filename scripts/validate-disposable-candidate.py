#!/usr/bin/env python3
"""Local validation of the candidate binary and exact tagged poller DB/EXIT phases.
Remote discovery/provenance are excluded; credentials and identities synthetic.
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
import urllib.parse
from http.cookiejar import CookieJar
import html
import argparse
import uuid
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parents[1]
DB = 'mycfc'
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
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release-image',action='store_true',help='exercise the actual multi-stage production Dockerfile')
    options = parser.parse_args()
    folder = pathlib.Path(tempfile.mkdtemp(prefix='mycfc-disposable-evidence-', dir=os.environ['TMPDIR']))
    prefix = 'mycfc-disposable-local-' + uuid.uuid4().hex[:10]
    pg, old = prefix + '-pg', prefix + '-old'
    validate_local_target(pg, DB)
    images = []
    state = folder / 'state'
    state.mkdir()
    compose = folder / 'compose.yaml'
    report: dict[str, Any] = {'remote_discovery_exercised': False, 'release_path_implemented': True}

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

    def hidden_field(page, name):
        match = re.search(r'name="'+re.escape(name)+r'" value="([^"]+)"',page)
        if match is None:
            raise AssertionError('missing supported form field '+name)
        return html.unescape(match.group(1))

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
            if label == 'candidate' and options.release_image:
                image = prefix+'-candidate'
                command('docker','build','-q','-t',image,'--build-arg','RELEASE_VERSION=v0.0.0-rehearsal','--build-arg','GIT_SHA='+'a'*40,str(ROOT))
                images.append(image)
                report['actual_production_dockerfile_exercised'] = True
                continue
            context = folder / label
            context.mkdir()
            flags = ['-ldflags=-X github.com/cfcoimbra/mycfc/internal/releasecontract.Version=v0.0.0-rehearsal -X github.com/cfcoimbra/mycfc/internal/releasecontract.Candidate=' + 'a'*40] if label == 'candidate' else []
            command('env', 'CGO_ENABLED=0', 'go', 'build', '-trimpath', *flags, '-o', str(context / 'server'), './cmd/server', cwd=checkout)
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
        common = 'APP_VERSION=v0.0.0-rehearsal\nGIT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAPP_ENV=test\nAPP_DB_USER=mycfc_app\nAPP_DB_PASSWORD=synthetic\nMIGRATION_DB_USER=mycfc_migration\nMIGRATION_DB_PASSWORD=synthetic\n'
        for label, role in [('bootstrap', 'postgres'), ('migration', 'mycfc_migration')]:
            (folder / (label + '.env')).write_text(common + 'DATABASE_URL=postgres://' + role + ':synthetic@' + pg + ':5432/' + DB + '?sslmode=disable\n')
        command('docker', 'run', '--rm', '--network', prefix, '--env-file', str(folder / 'bootstrap.env'), old_image, 'bootstrap-db')
        sql((predecessor / 'internal/db/schema.sql').read_text(), 'mycfc_migration')
        inventory = sorted(['reset-baseline-v1'] + [p.stem for p in (predecessor / 'internal/db/migrations').glob('*.sql')])
        assert len(inventory) == 66, inventory
        sql('CREATE TABLE mycfc_meta.schema_migrations(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES ' + ','.join("('" + v + "')" for v in inventory) + '; CREATE TABLE public.synthetic_disposable_history(value text); INSERT INTO public.synthetic_disposable_history VALUES (\'discard me\');', 'mycfc_migration')
        sql("INSERT INTO public.users(name,email,email_verified_at,password_hash,date_of_birth) VALUES ('Retained Admin','admin@example.test',now(),crypt('Synthetic-Rehearsal-Password-Only!',gen_salt('bf',12)),'1990-01-01'); INSERT INTO public.user_platform_roles(user_id,role_id) SELECT u.id,r.id FROM public.users u CROSS JOIN public.platform_roles r WHERE u.email='admin@example.test' AND r.code='ADMIN';")
        retained_id = scalar("SELECT id FROM public.users WHERE email='admin@example.test'")
        sql("INSERT INTO public.users(name,email,password_hash,date_of_birth) VALUES ('Retained Unverified Admin','unverified-admin@example.test',crypt('Synthetic-Rehearsal-Password-Only!',gen_salt('bf',12)),'1990-01-01'); INSERT INTO public.user_platform_roles(user_id,role_id) SELECT u.id,r.id FROM public.users u CROSS JOIN public.platform_roles r WHERE u.email='unverified-admin@example.test' AND r.code='ADMIN';")
        auth_fingerprint_query = "SELECT md5(string_agg(row(u.id,u.name,u.email::text,u.email_verified_at,u.password_hash,u.credential_version,u.date_of_birth,u.created_at,u.updated_at)::text,E'\\n' ORDER BY u.id)) FROM public.users u JOIN public.user_platform_roles a ON a.user_id=u.id JOIN public.platform_roles r ON r.id=a.role_id WHERE r.code='ADMIN'"
        retained_auth_fingerprint = scalar(auth_fingerprint_query)
        runtime_digest = 'sha256:'+'c'*64
        guardian_url = 'postgres://mycfc_guardian_release_bind:synthetic@'+pg+':5432/'+DB+'?sslmode=disable'
        command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'bootstrap.env'),'-e','GUARDIAN_RELEASE_BIND_DATABASE_URL='+guardian_url,old_image,'provision-guardian-release-bind')
        (folder/'guardian.env').write_text('GUARDIAN_RELEASE_BIND_DATABASE_URL='+guardian_url+'\nGUARDIAN_RELEASE_BIND_EXPECTED_DATABASE='+DB+'\nGUARDIAN_RUNTIME_IMAGE_DIGEST='+runtime_digest+'\n')
        command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'bootstrap.env'),old_image,'harden-db')
        env = [line for line in (ROOT / '.env.example').read_text().splitlines() if line and not line.startswith(('#', 'DATABASE_URL=', 'APP_ENV='))]
        env += ['APP_VERSION=v0.0.0-rehearsal','GIT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','APP_ENV=test', 'DATABASE_URL=postgres://mycfc_app:synthetic@' + pg + ':5432/' + DB + '?sslmode=disable']
        (folder / 'app.env').write_text('\n'.join(env) + '\n')
        command('docker', 'run', '-d', '--name', old, '--network', prefix, '--network-alias', 'app-blue', '-p', '127.0.0.1::8080', '--env-file', str(folder / 'app.env'), old_image)
        wait(lambda: http(old))
        old_port = command('docker','port',old,'8080/tcp').decode().strip().rsplit(':',1)[1]
        old_base = 'http://127.0.0.1:'+old_port
        old_client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        page = old_client.open(old_base+'/login').read().decode()
        payload = urllib.parse.urlencode({'gorilla.csrf.Token':hidden_field(page,'gorilla.csrf.Token'),'identifier':'admin@example.test','password':'Synthetic-Rehearsal-Password-Only!','next':'/admin/membros'}).encode()
        assert '/login' not in old_client.open(urllib.request.Request(old_base+'/login',data=payload,headers={'Referer':old_base+'/login'})).url
        assert old_client.open(old_base+'/admin/membros').status == 200
        assert scalar('SELECT count(*) FROM mycfc_meta.schema_migrations') == '66'
        premature = command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'migration.env'),candidate,'migrate',check=False)
        assert premature.returncode and b'disposable completion required' in premature.stderr
        assert scalar('SELECT count(*) FROM mycfc_meta.schema_migrations') == '66'
        def bootstrap(check=True, *overrides):
            return command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'bootstrap.env'), *overrides, candidate,'bootstrap-db',check=check)
        # Actual candidate command, not a SQL surrogate, must reject migration privilege.
        unsupported = command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'migration.env'),candidate,'bootstrap-db',check=False)
        assert unsupported.returncode and b'requires bootstrap superuser' in unsupported.stderr
        assert scalar("SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'") == 't'
        bad_migration = bootstrap(False,'-e','MIGRATION_DB_USER=postgres')
        assert bad_migration.returncode and b'migration role ownership/capability preflight rejected' in bad_migration.stderr
        assert scalar("SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'") == 't'
        wrong = bootstrap(False,'-e','APP_VERSION=v1.25.9')
        assert wrong.returncode and b'identity mismatch' in wrong.stderr
        sql("INSERT INTO mycfc_meta.schema_migrations(version) VALUES ('unknown-partial-state')",'mycfc_migration')
        partial = bootstrap(False)
        assert partial.returncode and b'exact v1.25.9 predecessor' in partial.stderr
        sql("DELETE FROM mycfc_meta.schema_migrations WHERE version='unknown-partial-state'",'mycfc_migration')
        sql('CREATE SCHEMA unrelated_tenant')
        unrelated = bootstrap(False)
        assert unrelated.returncode and b'preflight rejected' in unrelated.stderr
        sql('DROP SCHEMA unrelated_tenant')
        report['wrong_identity_partial_ledger_unrelated_namespace_refused'] = True
        assert scalar('SELECT count(*) FROM synthetic_disposable_history') == '1'
        report['unsupported_privilege_refused_before_destruction'] = True
        report['bootstrap_role_capabilities'] = json.loads(scalar("SELECT json_build_object('superuser',rolsuper,'createdb',rolcreatedb,'createrole',rolcreaterole) FROM pg_roles WHERE rolname='postgres'"))
        report['migration_role_capabilities'] = json.loads(scalar("SELECT json_build_object('superuser',rolsuper,'createdb',rolcreatedb,'createrole',rolcreaterole) FROM pg_roles WHERE rolname='mycfc_migration'"))
        # Extract unchanged installed poller DB phases and EXIT recovery.
        poller = command('git', 'show', 'v1.25.9:deployment/pull-release.sh').decode()
        functions = poller[poller.index('log() {'):poller.index('if [ "$(id -u)" -ne 0 ]')]
        phases = poller[poller.index('run_phase database_bootstrap '):poller.index('record_timeline_milestone migration-completed')]
        assert phases.count('run_phase ') == 5
        upstream = state/'caddy-upstream.caddy'
        upstream.write_text('reverse_proxy app-blue:8080\n')
        old_route = upstream.read_bytes()
        (folder/'Caddyfile').write_text(':80 {\n import /etc/mycfc/deployment/caddy-upstream.caddy\n}\n')
        compose.write_text('name: ' + prefix + '\nservices:\n  db-bootstrap:\n    image: ' + candidate + '\n    command: [bootstrap-db]\n    env_file: [' + str(folder / 'bootstrap.env') + ']\n    networks: [local]\n    profiles: [release]\n  migrate:\n    image: ' + candidate + '\n    command: [migrate]\n    env_file: [' + str(folder / 'migration.env') + ']\n    networks: [local]\n    profiles: [release]\n  app-green:\n    image: ' + candidate + '\n    env_file: [' + str(folder / 'app.env') + ']\n    networks: [local]\n    profiles: [green]\n    ports: ["127.0.0.1::8080"]\n  guardian-release-bind:\n    image: ' + candidate + '\n    command: [bind-guardian-release]\n    env_file: [' + str(folder/'guardian.env') + ']\n    networks: [local]\n    profiles: [release]\n  caddy:\n    image: caddy:2.10.2-alpine\n    ports: ["127.0.0.1::80"]\n    volumes: [' + str(folder/'Caddyfile') + ':/etc/caddy/Caddyfile:ro, ' + str(state) + ':/etc/mycfc/deployment:ro]\n    networks: [local]\nnetworks:\n  local:\n    external: true\n    name: ' + prefix + '\n')
        previous, current = folder / 'env.previous', folder / 'env'
        previous.write_text('LOCAL_RELEASE=predecessor\n')
        current.write_text('LOCAL_RELEASE=candidate\n')
        header = '#!/bin/sh\nset -eu\n' + '\n'.join([
            'env_file=' + str(current), 'compose_file=' + str(compose), 'state_dir=' + str(state), 'runtime_dir=' + str(folder),
            'backup_file=' + str(previous), 'route_backup=', 'route_switched=false', 'traffic_switched=false',
            'candidate_started=false', 'candidate_slot=', 'release_updated=true', 'privacy_worker_stopped=false',
            'release_digest=', 'release_version=', 'sha=', 'expected_guardian_intake=false', 'upstream_file='+str(upstream), 'current_phase=initialization']) + '\n'
        script = folder / 'tagged-phases.sh'
        script.write_text(header + functions + '\ntrap rollback EXIT HUP INT TERM\n' + phases + '\nrelease_updated=false\ntrap - EXIT HUP INT TERM\n')
        # Fail during destructive DDL using a real PostgreSQL event trigger.
        # The whole reset rolls back, but the already-committed old-role fence stays.
        sql("CREATE FUNCTION public.fail_disposable_ddl() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic destructive DDL failure'; END $$; CREATE EVENT TRIGGER fail_disposable_reset ON sql_drop EXECUTE FUNCTION public.fail_disposable_ddl();")
        precompletion_failure = command('sh',str(script),check=False)
        (folder/'before-completion-failure.log').write_bytes(precompletion_failure.stdout+precompletion_failure.stderr)
        assert precompletion_failure.returncode
        assert current.read_bytes() == previous.read_bytes()
        assert scalar('SELECT count(*) FROM mycfc_meta.schema_migrations') == '66'
        assert scalar('SELECT count(*) FROM synthetic_disposable_history') == '1'
        assert scalar("SELECT id FROM public.users WHERE email='admin@example.test'") == retained_id
        assert scalar("SELECT to_regnamespace('mycfc_disposable_release') IS NULL") == 't'
        assert scalar("SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'") == 'f'
        sql('ALTER EVENT TRIGGER fail_disposable_reset DISABLE; DROP EVENT TRIGGER fail_disposable_reset; DROP FUNCTION public.fail_disposable_ddl();')
        command('sh', str(script))
        report['destructive_failure_rolled_back_and_retry_completed'] = True
        final_inventory = json.loads(command('docker', 'run', '--rm', candidate, 'schema-inventory'))
        assert scalar('SELECT version FROM mycfc_meta.schema_migrations ORDER BY version').splitlines() == final_inventory
        assert scalar("SELECT to_regclass('public.synthetic_disposable_history') IS NULL") == 't'
        # Reset capability must already be retired before any public process starts.
        assert scalar("SELECT reset_retired_at IS NOT NULL AND reset_retired_at<=completed_at FROM mycfc_disposable_release.completion") == 't'
        report['reset_retired_atomically_before_public_traffic'] = True
        # Simulate an old/partial receipt without retirement: never assume reset.
        sql('ALTER TABLE mycfc_disposable_release.completion RENAME reset_retired_at TO missing_retirement')
        refused = bootstrap(False)
        assert refused.returncode and b'completion mismatched' in refused.stderr
        refused = command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'migration.env'),candidate,'migrate',check=False)
        assert refused.returncode and b'disposable completion required' in refused.stderr
        sql('ALTER TABLE mycfc_disposable_release.completion RENAME missing_retirement TO reset_retired_at')
        report['partial_completion_without_retirement_refused'] = True
        report['candidate_bootstrap_reset_then_tagged_migrate'] = True
        report['predecessor_markers'], report['final_markers'] = len(inventory), len(final_inventory)
        assert scalar("SELECT id FROM public.users WHERE email='admin@example.test'") == retained_id
        assert scalar("SELECT password_hash=crypt('Synthetic-Rehearsal-Password-Only!',password_hash) AND email_verified_at IS NOT NULL FROM public.users WHERE email='admin@example.test'") == 't'
        assert scalar("SELECT count(*) FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id WHERE u.is_active AND r.code='ADMIN'") == '2'
        assert scalar("SELECT password_hash=crypt('Synthetic-Rehearsal-Password-Only!',password_hash) AND email_verified_at IS NULL FROM public.users WHERE email='unverified-admin@example.test'") == 't'
        assert scalar(auth_fingerprint_query) == retained_auth_fingerprint
        report['existing_admin_identity_and_credential_retained'] = True
        sql("CREATE TABLE public.synthetic_candidate_data(value text); INSERT INTO public.synthetic_candidate_data VALUES ('must survive retry');", 'mycfc_migration')
        command('sh', str(script))
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        assert scalar("SELECT NOT EXISTS(SELECT 1 FROM privacy_request_activation WHERE enabled OR fulfilment_ready) AND (SELECT engaged FROM privacy_worker_kill_switch WHERE singleton) AND NOT EXISTS(SELECT 1 FROM guardian_application_intake_release WHERE enabled)") == 't'
        assert scalar("SELECT has_table_privilege('mycfc_disposable_web_20261001','public.users','SELECT') AND NOT has_table_privilege('mycfc_disposable_web_20261001','public.privacy_worker_kill_switch','UPDATE') AND NOT has_schema_privilege('mycfc_disposable_web_20261001','classification_erasure','USAGE') AND NOT has_schema_privilege('mycfc_disposable_web_20261001','mycfc_disposable_release','USAGE') AND NOT has_function_privilege('mycfc_disposable_web_20261001','mycfc_disposable_release.matches(text,text,text,text)','EXECUTE') AND NOT has_table_privilege('mycfc_migration','mycfc_disposable_release.completion','UPDATE')") == 't'
        report['fresh_web_acl_and_retired_privacy_guardian_gates_closed'] = True
        report['tagged_database_phase_retry_does_not_wipe'] = True
        # Actual candidate process starts and serves fresh schema before failure.
        command('docker', 'compose', '-f', str(compose), '--profile', 'green', 'up', '-d', '--no-deps', 'app-green')
        green = command('docker', 'compose', '-f', str(compose), 'ps', '-q', 'app-green').decode().strip()
        wait(lambda: http(green))
        validation = poller[poller.index('current_phase=candidate_validation\n'):poller.index('current_phase=traffic_switch\n')]
        validation_script = folder/'tagged-candidate-validation.sh'
        validation_script.write_text(header+functions+'\ncandidate_slot=green\ncandidate_container='+green+'\n'+validation)
        command('env','NO_PROXY=*','no_proxy=*','sh',str(validation_script))
        report['actual_tagged_candidate_health_login_asset_validation_passed'] = True
        assert b'/assets/app-' in http(green, '/login')
        port = command('docker','port',green,'8080/tcp').decode().strip().rsplit(':',1)[1]
        base = 'http://127.0.0.1:'+port
        jar = CookieJar()
        client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
        page = client.open(base+'/login').read().decode()
        csrf = hidden_field(page,'gorilla.csrf.Token')
        payload = urllib.parse.urlencode({'gorilla.csrf.Token':csrf,'identifier':'admin@example.test','password':'Synthetic-Rehearsal-Password-Only!','next':'/admin/membros'}).encode()
        response = client.open(urllib.request.Request(base+'/login',data=payload,headers={'Referer':base+'/login'}))
        assert response.status == 200 and '/login' not in response.url
        assert client.open(base+'/admin/membros').status == 200
        report['retained_credentials_authenticate_real_admin_session'] = True
        registration_client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        page = registration_client.open(base+'/registo').read().decode()
        csrf = hidden_field(page,'gorilla.csrf.Token')
        token = hidden_field(page,'registration_token')
        time.sleep(2.1)  # honour the actual supported anti-bot minimum render age
        payload = urllib.parse.urlencode({'gorilla.csrf.Token':csrf,'registration_token':token,'name':'Registered Candidate User','email':'registered@example.test','date_of_birth':'1990-01-01','password':'Synthetic-Rehearsal-Password-Only!','password_confirmation':'Synthetic-Rehearsal-Password-Only!','accept_terms':'on'}).encode()
        response = registration_client.open(urllib.request.Request(base+'/registo',data=payload,headers={'Referer':base+'/registo'}))
        assert response.status==200 and '/registo' not in response.url
        assert scalar("SELECT count(*) FROM public.users WHERE email='registered@example.test' AND is_active AND email_verified_at IS NULL") == '1'
        login_client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        page = login_client.open(base+'/login').read().decode()
        csrf = hidden_field(page,'gorilla.csrf.Token')
        payload = urllib.parse.urlencode({'gorilla.csrf.Token':csrf,'identifier':'registered@example.test','password':'Synthetic-Rehearsal-Password-Only!'}).encode()
        response = login_client.open(urllib.request.Request(base+'/login',data=payload,headers={'Referer':base+'/login'}))
        assert response.status==200 and '/login' not in response.url
        report['real_registration_and_new_account_login'] = True
        # No lifecycle marking is needed: first completion already closed reset.
        assert scalar('SELECT real_data_started_at IS NULL AND reset_retired_at IS NOT NULL FROM mycfc_disposable_release.completion') == 't'
        bootstrap()
        assert scalar("SELECT count(*) FROM public.users WHERE email='registered@example.test'") == '1'
        for statement in ['UPDATE mycfc_disposable_release.completion SET reset_retired_at=NULL', 'UPDATE mycfc_disposable_release.completion SET reset_retired_at=now()', 'DELETE FROM mycfc_disposable_release.completion', 'TRUNCATE mycfc_disposable_release.completion']:
            denied = sql(statement,check=False)
            assert denied.returncode and b'reset retirement is permanent' in denied.stderr
        for role in ['mycfc_migration','mycfc_disposable_web_20261001']:
            assert sql('DELETE FROM mycfc_disposable_release.completion',role,check=False).returncode
        # Even missing completion cannot make the final schema reset-eligible.
        sql('ALTER SCHEMA mycfc_disposable_release RENAME TO hidden_completion')
        refused = bootstrap(False)
        assert refused.returncode and b'exact v1.25.9 predecessor' in refused.stderr
        assert scalar("SELECT count(*) FROM users WHERE email='registered@example.test'") == '1'
        sql('ALTER SCHEMA hidden_completion RENAME TO mycfc_disposable_release')
        report['missing_completion_after_registration_refuses_reset'] = True
        report['registration_then_reset_attempt_preserves_real_user_without_manual_marker'] = True
        report['completion_retirement_update_delete_truncate_denied'] = True
        # Password authentication over TCP, not SET ROLE or trusted local socket.
        for role,password,allowed in [('mycfc_disposable_web_20261001','synthetic',True),('mycfc_disposable_web_20261001','incorrect-synthetic',False),('mycfc_app','synthetic',False)]:
            probe = command('docker','exec','-e','PGPASSWORD='+password,pg,'psql','-X','-h',pg,'-U',role,'-d',DB,'-c','SELECT current_user',check=False)
            assert (probe.returncode==0)==allowed
        assert scalar("SELECT NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolbypassrls FROM pg_roles WHERE rolname='mycfc_disposable_web_20261001'") == 't'
        report['new_role_existing_password_true_tcp_boundary'] = True
        report['fresh_candidate_ready_and_login'] = True
        command('docker','compose','-f',str(compose),'up','-d','--no-deps','caddy')
        caddy = command('docker','compose','-f',str(compose),'ps','-q','caddy').decode().strip()
        route_script = folder/'route.sh'
        route_script.write_text(header+functions+'\nwrite_upstream green\nreload_caddy\n')
        command('sh',str(route_script))
        def proxy_ready():
            port = command('docker','port',caddy,'80/tcp').decode().strip().rsplit(':',1)[1]
            with urllib.request.urlopen('http://127.0.0.1:'+port+'/health/ready') as response:
                assert response.status==200
        wait(proxy_ready)
        report['tagged_write_upstream_reload_candidate_proxy_ready'] = True
        # Real fault: stop the validated candidate, then run exact tagged EXIT
        # recovery from the post-switch validation state, including real Caddy.
        command('docker', 'stop', green)
        backup_route = folder/'previous-upstream.caddy'
        backup_route.write_bytes(old_route)
        script.write_text(header + functions + '\ntrap rollback EXIT HUP INT TERM\ncandidate_slot=green\ncandidate_started=true\nroute_backup='+str(backup_route)+'\nroute_switched=true\ntraffic_switched=true\ncurrent_phase=post_switch_validation\nexit 1\n')
        failed = command('sh', str(script), check=False)
        (folder / 'tagged-failure.log').write_bytes(failed.stdout + failed.stderr)
        assert failed.returncode != 0
        assert current.read_bytes() == previous.read_bytes()
        assert upstream.read_bytes() == old_route
        assert b'Caddy upstream rollback failed' not in failed.stdout+failed.stderr
        assert json.loads(command('docker', 'inspect', old))[0]['State']['Running']
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        def old_denied():
            try:
                http(old)
            except urllib.error.HTTPError as error:
                assert error.code == 503
                return
            raise AssertionError('old web principal still ready')
        wait(old_denied)
        port = command('docker','port',caddy,'80/tcp').decode().strip().rsplit(':',1)[1]
        try:
            urllib.request.urlopen('http://127.0.0.1:'+port+'/health/ready')
            raise AssertionError('restored predecessor proxy is not fail closed')
        except urllib.error.HTTPError as error:
            assert error.code==503
        report['tagged_postswitch_route_restore_old_proxy_readiness_503'] = True
        proxy_base = 'http://127.0.0.1:'+port
        def fetch(client,url,data=None,referer=None):
            request = urllib.request.Request(url,data=data,headers={'Referer':referer} if referer else {})
            try:
                response = client.open(request,timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            return response.code,response.read().decode(),response.geturl()
        routes = {}
        for label,endpoint in [('direct',old_base),('restored_proxy',proxy_base)]:
            client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
            routes[label] = {}
            for path,expected in [('/health/live',200),('/health/ready',503),('/login',200),('/registo',200)]:
                status,body,_ = fetch(client,endpoint+path)
                assert status==expected,(label,path,status)
                routes[label]['GET '+path] = status
                assert 'admin@example.test' not in body and 'Registered Candidate User' not in body
            status,page,_ = fetch(client,endpoint+'/login')
            payload = urllib.parse.urlencode({'gorilla.csrf.Token':hidden_field(page,'gorilla.csrf.Token'),'identifier':'admin@example.test','password':'Synthetic-Rehearsal-Password-Only!'}).encode()
            status,body,_ = fetch(client,endpoint+'/login',payload,endpoint+'/login')
            assert status==500,(label,'login POST',status)
            routes[label]['POST /login'] = status
            status,page,_ = fetch(client,endpoint+'/registo')
            time.sleep(2.1)
            payload = urllib.parse.urlencode({'gorilla.csrf.Token':hidden_field(page,'gorilla.csrf.Token'),'registration_token':hidden_field(page,'registration_token'),'name':'Denied Fallback User','email':'denied-'+label+'@example.test','date_of_birth':'1990-01-01','password':'Synthetic-Rehearsal-Password-Only!','password_confirmation':'Synthetic-Rehearsal-Password-Only!','accept_terms':'on'}).encode()
            status,body,_ = fetch(client,endpoint+'/registo',payload,endpoint+'/registo')
            assert status==500,(label,'registration POST',status)
            routes[label]['POST /registo'] = status
            assert scalar("SELECT count(*) FROM public.users WHERE email LIKE 'denied-%@example.test'") == '0'
        # A previously authenticated old session must not disclose member data.
        status,body,_ = fetch(old_client,old_base+'/admin/membros')
        assert status>=400 and 'admin@example.test' not in body and 'Registered Candidate User' not in body,(status,body[:200])
        routes['direct']['GET /admin/membros (old authenticated cookie)'] = status
        status,body,_ = fetch(old_client,proxy_base+'/admin/membros')
        assert status==500 and 'admin@example.test' not in body and 'Registered Candidate User' not in body
        routes['restored_proxy']['GET /admin/membros (old authenticated cookie)'] = status
        assert scalar("SELECT NOT has_schema_privilege('mycfc_app','public','USAGE') AND NOT has_table_privilege('mycfc_app','public.users','SELECT') AND NOT has_table_privilege('mycfc_app','public.users','INSERT')") == 't'
        for query in ['SELECT email FROM public.users','UPDATE public.users SET name=name']:
            denied = sql(query,'mycfc_app',check=False)
            assert denied.returncode and b'permission denied' in denied.stderr
        report['old_principal_select_and_write_acl_denied'] = True
        report['old_fallback_routes'] = routes
        report['old_fallback_forms_no_authentication_no_registration_no_pii'] = True
        assert scalar("SELECT rolcanlogin FROM pg_roles WHERE rolname='mycfc_app'") == 'f'
        assert scalar("SELECT count(*) FROM pg_stat_activity WHERE usename='mycfc_app'") == '0'
        report['tagged_failure_restores_old_config_but_old_readiness_503'] = True
        report['predecessor_sessions_terminated'] = True
        report['old_fallback_db_access_is_fail_closed_not_blanket_http_503'] = True
        # Retry after actual candidate failure also preserves post-reset writes.
        script.write_text(header+functions+'\ntrap rollback EXIT HUP INT TERM\n'+phases+'\nrelease_updated=false\ntrap - EXIT HUP INT TERM\n')
        command('sh',str(script))
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        assert scalar("SELECT count(*) FROM public.users WHERE email='registered@example.test'") == '1'
        command('docker','compose','-f',str(compose),'--profile','green','up','-d','--no-deps','app-green')
        wait(lambda: http(green))
        command('env','NO_PROXY=*','no_proxy=*','sh',str(validation_script))
        command('sh',str(route_script))
        wait(proxy_ready)
        sql("UPDATE mycfc_disposable_release.completion SET real_data_started_at=now()")
        bootstrap()  # real-data metadata never prevents safe non-destructive resume
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        assert scalar("SELECT count(*) FROM users WHERE email='registered@example.test'") == '1'
        report['marked_real_data_retry_is_nondestructive'] = True
        sql("INSERT INTO mycfc_meta.schema_migrations(version) VALUES ('unknown-postcompletion')")
        unknown_migration = command('docker','run','--rm','--network',prefix,'--env-file',str(folder/'migration.env'),candidate,'migrate',check=False)
        assert unknown_migration.returncode and b'disposable completion required' in unknown_migration.stderr
        refused = bootstrap(False)
        assert refused.returncode and b'completion mismatched' in refused.stderr
        assert scalar('SELECT count(*) FROM synthetic_candidate_data') == '1'
        sql("DELETE FROM mycfc_meta.schema_migrations WHERE version='unknown-postcompletion'")
        sql("UPDATE mycfc_disposable_release.completion SET candidate=repeat('b',40)")
        refused = bootstrap(False)
        assert refused.returncode and b'completion mismatched' in refused.stderr
        sql("UPDATE mycfc_disposable_release.completion SET candidate=repeat('a',40)")
        # Restored candidate still authenticates both retained and newly written data.
        for email,next_path in [('admin@example.test','/admin/membros'),('registered@example.test','/dashboard')]:
            client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
            page = client.open(proxy_base+'/login').read().decode()
            payload = urllib.parse.urlencode({'gorilla.csrf.Token':hidden_field(page,'gorilla.csrf.Token'),'identifier':email,'password':'Synthetic-Rehearsal-Password-Only!','next':next_path}).encode()
            response = client.open(urllib.request.Request(proxy_base+'/login',data=payload,headers={'Referer':proxy_base+'/login'}))
            assert response.status==200 and '/login' not in response.url
            assert client.open(proxy_base+next_path).status==200
        report['retained_admin_and_registered_user_authenticate_after_marked_retry'] = True
        report['postcompletion_failure_retry_data_survives'] = True
        report['mismatched_completion_refused'] = True
        (folder / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report, indent=2))
    finally:
        subprocess.run(['docker', 'compose', '-f', str(compose), '--profile', 'green', 'down', '--volumes'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for name in [old, pg]:
            subprocess.run(['docker', 'rm', '-fv', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['docker', 'network', 'rm', prefix], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for image in images:
            subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        print('Isolated resources cleaned; evidence:', folder, flush=True)


if __name__ == '__main__':
    main()
