#!/usr/bin/env python3
"""Resumable real-clock #338 acceptance; no date rewrites or clock injection."""
import argparse
from contextlib import contextmanager
from datetime import datetime, date, timedelta
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time
import urllib.request
import uuid
from zoneinfo import ZoneInfo

ROOT = Path(__file__).resolve().parents[1]
DATABASE = 'mycfc_crew_transition_test'
SUBJECT = '33733900-0000-0000-0000-000000000017'
PREFIX = 'mycfc-338-natural-'
FILES = ['e2e/run-classification-natural-rollover.py', 'e2e/crew-transition-seed.sql', 'e2e/classification-natural-rollover.spec.mjs', 'e2e/crew-transition-helpers.mjs', 'playwright.config.mjs', 'package-lock.json']


def source_manifest():
    files = [ROOT / 'go.mod', ROOT / 'go.sum', ROOT / '.env.example']
    for folder in ('cmd', 'internal', 'ui'):
        files.extend(p for p in (ROOT / folder).rglob('*') if p.is_file())
    return {str(p.relative_to(ROOT)): digest(p) for p in sorted(files)}


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def save(out, state):
    temporary = out / 'state.tmp'
    temporary.write_text(json.dumps(state, indent=2))
    temporary.chmod(0o600)
    temporary.replace(out / 'state.json')


def run(args, out, log, **kwargs):
    with (out / log).open('w') as handle:
        subprocess.run(args, cwd=ROOT, stdout=handle, stderr=subprocess.STDOUT, check=True, **kwargs)


def free_port():
    with socket.socket() as handle:
        handle.bind(('127.0.0.1', 0))
        return handle.getsockname()[1]


def sql(state, query):
    return subprocess.check_output(['docker', 'exec', '-i', state['container'], 'psql', '-U', 'acceptance', '-d', DATABASE, '-v', 'ON_ERROR_STOP=1', '-At'], input=query, text=True)


def read_json(state, query):
    return json.loads(sql(state, query))


def clock(state):
    row = read_json(state, "SELECT json_build_object('lisbon_date',(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date,'instant',clock_timestamp(),'timezone',current_setting('TimeZone'));")
    local = datetime.now(ZoneInfo('Europe/Lisbon'))
    if row['timezone'] != 'Europe/Lisbon' or row['lisbon_date'] != local.date().isoformat():
        raise RuntimeError('Host and database Lisbon dates/timezone disagree; no mutation permitted.')
    instant = datetime.fromisoformat(row['instant'])
    if abs((instant - local).total_seconds()) > 5:
        raise RuntimeError('Host and database clocks disagree.')
    return row


def require_finish(state, today):
    if state['phase'] != 'awaiting-rollover':
        raise ValueError('finish requires a successfully completed begin; no repeat publication.')
    start = date.fromisoformat(state['starts_on'])
    if date.fromisoformat(today) != start:
        raise ValueError(f'finish requires the real Lisbon date {start}; observed {today}. No dates changed.')


def owned_container(state):
    if not re.fullmatch(PREFIX + r'[0-9a-f]{12}', state['container']):
        raise ValueError('Not a harness-owned container name.')
    result = subprocess.check_output(['docker', 'inspect', state['container']], text=True)
    info = json.loads(result)[0]
    labels = info['Config'].get('Labels') or {}
    if labels.get('mycfc.natural-rollover') != state['token']:
        raise ValueError('Container ownership token mismatch; refusing action.')


def retained(state):
    return read_json(state, """SELECT json_build_object(
      'events',(SELECT json_agg(row_to_json(e) ORDER BY id) FROM events e),
      'responses',(SELECT json_agg(row_to_json(e) ORDER BY event_id,user_id) FROM event_responses e),
      'outcomes',(SELECT json_agg(row_to_json(e) ORDER BY session_id,user_id) FROM training_session_outcomes e),
      'sessions',(SELECT json_agg(row_to_json(e) ORDER BY id) FROM training_sessions e),
      'prescriptions',(SELECT json_agg(row_to_json(e) ORDER BY id) FROM training_prescriptions e WHERE session_id='33733900-0000-0000-0000-000000000400'),
      'publications',(SELECT json_agg(row_to_json(e) ORDER BY id) FROM training_plan_publications e WHERE plan_id='33733900-0000-0000-0000-000000000300'));
    """)


def intervals(state):
    return read_json(state, f"SELECT json_agg(row_to_json(m) ORDER BY m.starts_on,m.id) FROM user_memberships m WHERE user_id='{SUBJECT}';")


def fixture(out, state):
    info = clock(state)
    info['today'] = info['lisbon_date']
    info['tomorrow'] = (date.fromisoformat(info['today']) + timedelta(days=1)).isoformat()
    info['prescriptions'] = read_json(state, "SELECT COALESCE(json_object_agg(athlete_user_id::text||':'||session_id::text,id::text),'{}'::json) FROM training_prescriptions;")
    (out / 'fixture.json').write_text(json.dumps(info, indent=2))


@contextmanager
def application(out, state):
    # App lifetime is phase-local; only PG and private evidence survive begin.
    env = dict(os.environ)
    for line in (ROOT / '.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            env[key] = value.strip('"')
    port = free_port()
    base = f'http://127.0.0.1:{port}'
    env.update(APP_ENV='test', PORT=str(port), BASE_URL=base,
               DATABASE_URL=f"postgres://acceptance:disposable-only@127.0.0.1:{state['pg_port']}/{DATABASE}?sslmode=disable",
               AWS_EC2_METADATA_DISABLED='true', SMTP_HOST='127.0.0.1', S3_ENDPOINT='http://127.0.0.1:1')
    for key in ['CONSENT_TERMS_URL', 'CONSENT_IMAGE_URL', 'CONSENT_MINOR_URL', 'PRIVACY_NOTICE_URL', 'COOKIE_NOTICE_URL', 'GALLERY_URL']:
        env[key] = env[key].replace('http://localhost:8080', base)
    with (out / f"app-{state['phase']}.log").open('w') as handle:
        app = subprocess.Popen([str(out / 'server')], cwd=ROOT, env=env, stdout=handle, stderr=subprocess.STDOUT)
        try:
            for _ in range(100):
                if app.poll() is not None:
                    raise RuntimeError('App exited; inspect app log.')
                try:
                    if urllib.request.urlopen(base + '/health/ready', timeout=1).status == 200:
                        break
                except Exception:
                    time.sleep(0.25)
            else:
                raise RuntimeError('App readiness timed out.')
            yield base
        finally:
            app.terminate()
            try:
                app.wait(timeout=15)
            except subprocess.TimeoutExpired:
                app.kill()
                app.wait()


def browser(out, state, base, phase):
    fixture(out, state)
    run(['docker', 'run', '--rm', '--name', state['container'] + '-browser', '--network', 'host',
         '--user', f'{os.getuid()}:{os.getgid()}', '-e', 'E2E_BASE_URL=' + base,
         '-e', 'E2E_NATURAL_ROLLOVER_PHASE=' + phase, '-e', 'E2E_CREW_TRANSITION_EVIDENCE=/evidence',
         '-e', f'E2E_JSON_OUTPUT=/evidence/{phase}.json',
         '-v', str(ROOT) + ':/src:ro', '-v', state['node_modules'] + ':/src/node_modules:ro',
         '-v', str(out) + ':/evidence', '-w', '/src', 'mcr.microsoft.com/playwright:v1.63.0-noble',
         'npx', 'playwright', 'test', 'e2e/classification-natural-rollover.spec.mjs',
         '--workers=1', '--retries=0', '--output=/evidence/' + phase + '-artifacts'], out, phase + '.log')
    report = json.loads((out / f'{phase}.json').read_text())
    cases = []
    def walk(suite):
        for spec in suite.get('specs', []):
            cases.extend(spec['tests'])
        for child in suite.get('suites', []):
            walk(child)
    walk(report)
    if len(cases) != 1 or cases[0]['status'] != 'expected' or [r['status'] for r in cases[0]['results']] != ['passed']:
        raise RuntimeError(f'{phase} needs exactly one passing attempt, no skips/retries.')


def begin(out):
    modules = Path(os.environ.get('MYCFC_ROLLOVER_NODE_MODULES', str(ROOT / 'node_modules'))).resolve()
    lock = json.loads((ROOT / 'package-lock.json').read_text())['packages']
    for package in ('@playwright/test', '@axe-core/playwright'):
        installed = json.loads((modules / package / 'package.json').read_text())['version']
        if installed != lock['node_modules/' + package]['version']:
            raise ValueError(f'{package} must match the exact source lockfile.')
    (ROOT / 'node_modules').mkdir(exist_ok=True)  # Docker nested bind needs a mountpoint in read-only /src.
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    token = uuid.uuid4().hex
    state = {'version': 1, 'phase': 'starting', 'token': token,
             'container': PREFIX + token[:12], 'pg_port': free_port(),
             'source': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
             'browser_files': {f: digest(ROOT / f) for f in FILES},
             'app_sources': source_manifest(), 'node_modules': str(modules)}
    save(out, state)
    try:
        run(['docker', 'run', '-d', '--name', state['container'], '--label', 'mycfc.natural-rollover=' + token,
             '-e', 'POSTGRES_USER=acceptance', '-e', 'POSTGRES_PASSWORD=disposable-only', '-e', 'POSTGRES_DB=' + DATABASE,
             '-p', f"127.0.0.1:{state['pg_port']}:5432", 'postgres:16.9', '-c', 'timezone=Europe/Lisbon'], out, 'container.log')
        # TCP avoids mistaking PostgreSQL's temporary init server for final readiness.
        for _ in range(100):
            ready = subprocess.run(['docker', 'exec', state['container'], 'pg_isready', '-h', '127.0.0.1', '-U', 'acceptance'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if ready.returncode == 0:
                break
            time.sleep(0.25)
        else:
            raise RuntimeError('PostgreSQL TCP readiness timed out.')
        state['begin_clock'] = clock(state)
        run(['docker', 'exec', '-i', state['container'], 'psql', '-U', 'acceptance', '-d', DATABASE, '-v', 'ON_ERROR_STOP=1'], out, 'baseline.log', input=(ROOT / 'internal/db/schema.sql').read_text(), text=True)
        versions = sorted(p.stem for p in (ROOT / 'internal/db/migrations').glob('*.sql'))
        sql(state, 'CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES ' + ','.join("('" + v + "')" for v in versions) + ' ON CONFLICT DO NOTHING;')
        (out / 'seed.log').write_text(sql(state, (ROOT / 'e2e/crew-transition-seed.sql').read_text()))
        # Synthetic credential state only; guardian verification/credential issuance is not this acceptance.
        sql(state, f"UPDATE users SET minor_login_id='CFC-ACCE0017',password_hash=(SELECT password_hash FROM users WHERE email='acceptance-admin@example.test') WHERE id='{SUBJECT}';")
        run(['go', 'build', '-o', str(out / 'server'), './cmd/server'], out, 'build.log')
        state['binary_sha256'] = digest(out / 'server')
        with application(out, state) as base:
            browser(out, state, base, 'publish')
            sql(state, f"INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres) SELECT session_id,athlete_user_id,id,'COMPLETED',1000 FROM training_prescriptions WHERE athlete_user_id='{SUBJECT}' AND session_id='33733900-0000-0000-0000-000000000400';")
            before = retained(state)
            (out / 'retained-before.json').write_text(json.dumps(before, indent=2))
            browser(out, state, base, 'before')
        if retained(state) != before:
            raise RuntimeError('UI transition changed historical records.')
        rows = intervals(state)
        if len(rows) != 2 or rows[0]['id'] != '33733900-0000-0000-0000-000000000117':
            raise RuntimeError('Expected original interval and one UI-created interval.')
        initial = date.fromisoformat(state['begin_clock']['lisbon_date'])
        if rows[0]['ends_on'] != initial.isoformat() or rows[1]['starts_on'] != (initial + timedelta(days=1)).isoformat():
            raise RuntimeError('UI interval dates do not match natural before-day.')
        state['intervals'] = rows
        state['starts_on'] = rows[1]['starts_on']
        state['end_before_clock'] = clock(state)
        if state['end_before_clock']['lisbon_date'] != initial.isoformat():
            raise RuntimeError('Midnight occurred during begin; evidence is not a valid before phase.')
        state['phase'] = 'awaiting-rollover'
        save(out, state)
        print(f"BEFORE passed. PG retained; app stopped. On real Lisbon date {state['starts_on']} run finish {out}")
    except Exception:
        state['phase'] = 'begin-failed'
        save(out, state)
        raise


def finish(out, state):
    # All preflight reads/gates precede publication or any other application write.
    owned_container(state)
    observed = clock(state)
    require_finish(state, observed['lisbon_date'])
    if source_manifest() != state['app_sources']:
        raise ValueError('App source changed since begin; saved binary remains pinned, but evidence mixing is refused.')
    if digest(out / 'server') != state['binary_sha256']:
        raise ValueError('Saved app binary changed; refusing evidence mixing.')
    if {f: digest(ROOT / f) for f in FILES} != state['browser_files']:
        raise ValueError('Browser source/lockfile changed since begin.')
    if intervals(state) != state['intervals']:
        raise ValueError('UI-created interval rows changed during waiting; refusing proof.')
    historical = json.loads((out / 'retained-before.json').read_text())
    if retained(state) != historical:
        raise ValueError('Historical records changed while waiting.')
    state['phase'] = 'finishing'
    state['finish_clock'] = observed
    save(out, state)  # Once publication can run, retries require review, never automatic replay.
    with application(out, state) as base:
        browser(out, state, base, 'after')
    if intervals(state) != state['intervals'] or retained(state) != historical:
        raise ValueError('Interval identity/history changed during after phase.')
    future_count = int(sql(state, f"SELECT count(*) FROM training_prescriptions WHERE session_id='33733900-0000-0000-0000-000000000401' AND athlete_user_id='{SUBJECT}';"))
    if future_count != 0:
        raise ValueError('Former-scope person received a future prescription.')
    state['phase'] = 'passed'
    state['completed_clock'] = clock(state)
    save(out, state)
    (out / 'summary.json').write_text(json.dumps({'source': state['source'], 'binary_sha256': state['binary_sha256'], 'ui_interval_id': state['intervals'][1]['id'], 'natural_boundary_observed': True, 'historical_rows_equal': True, 'interval_rows_equal': True, 'former_scope_future_prescriptions': future_count}, indent=2))
    print('PASS natural-clock same-person rollover. Run cleanup explicitly when evidence is reviewed.')


def cleanup(out, state):
    owned_container(state)
    subprocess.run(['docker', 'rm', '-fv', state['container']], check=True)
    state['phase_before_cleanup'] = state['phase']
    state['phase'] = 'cleaned'
    save(out, state)
    print('Removed only this harness PostgreSQL container and its anonymous volume; evidence retained.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=['begin', 'finish', 'cleanup'])
    parser.add_argument('evidence', type=Path)
    args = parser.parse_args()
    directory = args.evidence.resolve()
    if args.phase == 'begin':
        begin(directory)
    else:
        state = json.loads((directory / 'state.json').read_text())
        if args.phase == 'finish':
            finish(directory, state)
        else:
            cleanup(directory, state)
