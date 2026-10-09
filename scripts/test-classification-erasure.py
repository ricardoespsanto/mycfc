#!/usr/bin/env python3
"""Synthetic-only real pg_dump/restore regression, no ports or live credentials.
Run: python3 scripts/test-classification-erasure.py (Docker required).
"""
import json
import pathlib
import subprocess
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
NAME = 'mycfc-erasure-test-' + uuid.uuid4().hex[:12]
MIGRATION = '202610010001_manual_classification_erasure'
PARENT = '5619fda20e20bc56502877a5b2a24c145e1f4361'


def uid(n):
    return f'00000000-0000-0000-0000-{n:012d}'


A, T, B, C, D = (uid(n) for n in range(1, 6))
S, CAT, GRANT, TEAM = (uid(n) for n in range(10, 14))
MEMBERS = [uid(n) for n in range(20, 24)]
EVENTS = json.dumps([{'id': i, 'subject': C} for i in (4, 5, 4)])
EXCEPTIONS = json.dumps([{'id': MEMBERS[2], 'subject': C}] * 2)
ARGS = f"'{T}','{EVENTS}'::jsonb,'{EXCEPTIONS}'::jsonb"


def command(*args, input=None):
    return subprocess.run(args, input=input, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=True).stdout


def sql(text, database='fresh', role=None):
    if role:
        text = f'SET ROLE {role};\n' + text
    return command('docker', 'exec', '-i', NAME, 'psql', '-X', '-q', '-v',
                   'ON_ERROR_STOP=1', '-U', 'postgres', '-d', database,
                   '-At', input=text.encode()).decode().strip()


def denied(text, database, role='erasure_web', expected=None):
    try:
        sql(text, database, role)
    except subprocess.CalledProcessError as err:
        if expected:
            assert expected in err.stderr.decode(), err.stderr.decode()
    else:
        raise AssertionError('forbidden operation succeeded')


def fixture(database):
    sql(f"""
    INSERT INTO users(id,name,email,password_hash,date_of_birth)
    SELECT x,'Synthetic','fixture-'||x||'@example.test','hash','2010-02-28'::date
    FROM unnest(ARRAY['{A}','{T}','{B}','{C}','{D}']::uuid[]) x;
    UPDATE users SET date_of_birth='1980-01-01' WHERE id IN ('{A}','{T}');
    INSERT INTO user_platform_roles(user_id,role_id)
    SELECT x,id FROM platform_roles CROSS JOIN unnest(ARRAY['{A}','{T}']::uuid[]) x WHERE code='ADMIN';
    UPDATE seasons SET is_current=false WHERE is_current;
    INSERT INTO seasons(id,code,name,starts_on,ends_on,is_current)
    VALUES('{S}','ERASE','Synthetic season','2026-01-01','2026-12-31',true);
    INSERT INTO competition_categories(id,season_id,programme_id,code,name_pt,birth_date_from,birth_date_to,approved_by_user_id,approved_at)
    SELECT '{CAT}','{S}',id,'ERASE','Synthetic category','2009-01-01','2009-12-31','{T}',now() FROM programmes WHERE code='Competition';
    INSERT INTO teams(id,season_id,programme_id,code,name)
    SELECT '{TEAM}','{S}',id,'ERASE','Synthetic team' FROM programmes WHERE code='Competition';
    INSERT INTO staff_grants(id,user_id,capability,programme_id,granted_by_id)
    SELECT '{GRANT}','{T}','COACH',id,'{A}' FROM programmes WHERE code='Competition';
    """, database, 'erasure_migrate')
    for mid, subject, actor in zip(MEMBERS, (T, B, C, D), (T, T, A, A)):
        sql(f"""INSERT INTO user_memberships(id,user_id,season_id,programme_id,team_id,competition_category_id,starts_on,ends_on,age_exception_reason,age_exception_by_id,age_exception_at)
        SELECT '{mid}','{subject}','{S}',id,'{TEAM}','{CAT}','2026-01-01','2026-12-31','Synthetic exception','{actor}',now()
        FROM programmes WHERE code='Competition';""", database, 'erasure_migrate')
    for subject, actor, grant in ((T, A, None), (T, T, GRANT), (B, T, None), (C, A, GRANT), (C, A, None), (D, A, None)):
        grant_sql = f"'{grant}'" if grant else 'NULL'
        sql(f"""INSERT INTO person_sport_assignment_events(operation_id,subject_user_id,actor_user_id,coach_grant_id,kind,action,code,reason)
        VALUES(gen_random_uuid(),'{subject}','{actor}',{grant_sql},'SPORT','ADDED','CANOEING','Synthetic correction');""", database, 'erasure_migrate')
    sql(f"""
    INSERT INTO person_sporting_modalities(user_id,modality_code) SELECT id,'CANOEING' FROM users WHERE id IN ('{T}','{B}','{C}','{D}');
    INSERT INTO person_canoe_craft_classes(user_id,craft_code) SELECT user_id,'K1' FROM person_sporting_modalities;
    INSERT INTO events(id,title,starts_at,ends_at,created_by_id) VALUES('{uid(30)}','Synthetic event','2026-06-01 10:00Z','2026-06-01 11:00Z','{A}');
    INSERT INTO event_responses(event_id,user_id,status,responded_by_id) VALUES('{uid(30)}','{T}','Going','{A}');
    INSERT INTO training_groups(id,name,programme_id,created_by_id) SELECT '{uid(31)}','Synthetic group',id,'{A}' FROM programmes WHERE code='Competition';
    INSERT INTO training_group_members(group_id,membership_id,added_by_id) VALUES('{uid(31)}','{MEMBERS[0]}','{A}');
    INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id)
    SELECT '{uid(32)}','Synthetic plan',id,'{uid(31)}','{S}','2026-06-01','{A}' FROM programmes WHERE code='Competition';
    INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) VALUES('{uid(33)}','{uid(32)}','Synthetic training','2026-06-02 10:00Z','2026-06-02 11:00Z','{A}');
    INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id)
    SELECT '{uid(34)}',id,1,updated_at,'Synthetic publication','{A}' FROM training_plans WHERE id='{uid(32)}';
    INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256)
    VALUES('{uid(35)}','{uid(34)}','{uid(33)}','{MEMBERS[0]}','{T}','{{"synthetic":"published-history"}}',repeat('a',64));
    INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres)
    VALUES('{uid(33)}','{T}','{uid(35)}','COMPLETED',1000);
    """, database, 'erasure_migrate')


def snapshot(database):
    # All public rows, including immutable snapshots; exclude only approved deleted
    # audit rows and the three fields approved for clearing. Never prints raw data.
    tables = sql("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename", database).splitlines()
    result = {}
    for table in tables:
        if table == 'person_sport_assignment_events':
            query = f'SELECT to_jsonb(t) AS row FROM public.{table} t WHERE id=6'
        elif table == 'user_memberships':
            query = f"SELECT CASE WHEN id=ANY(ARRAY['{MEMBERS[0]}','{MEMBERS[1]}','{MEMBERS[2]}']::uuid[]) THEN to_jsonb(t)-ARRAY['age_exception_reason','age_exception_by_id','age_exception_at'] ELSE to_jsonb(t) END AS row FROM public.{table} t"
        else:
            query = f'SELECT to_jsonb(t) AS row FROM public.{table} t'
        result[table] = sql(f"SELECT md5(coalesce(string_agg(row::text,',' ORDER BY row::text),'')) FROM ({query}) data", database)
    return result


def lock_regression(database):
    def session():
        process = subprocess.Popen(['docker', 'exec', '-i', NAME, 'psql', '-X', '-q',
                                   '-v', 'ON_ERROR_STOP=1', '-U', 'postgres',
                                   '-d', database, '-At'], stdin=subprocess.PIPE,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  text=True, bufsize=1)
        assert process.stdin is not None and process.stdout is not None
        return process

    holder = session()
    assert holder.stdin is not None and holder.stdout is not None
    waiter = None
    try:
        holder.stdin.write(f"SET ROLE erasure_migrate; BEGIN; SELECT * FROM classification_erasure.erase({ARGS},false);\n\\echo HELD\n")
        holder.stdin.flush()
        assert holder.stdout.readline().strip() == '5|3'
        assert holder.stdout.readline().strip() == 'HELD'
        waiter = session()
        assert waiter.stdin is not None and waiter.stdout is not None
        waiter.stdin.write("SET application_name='synthetic_erasure_waiter'; SET ROLE erasure_web; BEGIN; INSERT INTO person_sport_assignment_events(operation_id,subject_user_id,actor_user_id,kind,action,code,reason) "
                           f"VALUES(gen_random_uuid(),'{T}','{A}','SPORT','ADDED','SUP','Synthetic waiting writer'); ROLLBACK;\n\\echo FINISHED\n")
        waiter.stdin.flush()
        deadline = time.monotonic() + 10
        while sql("SELECT count(*) FROM pg_stat_activity WHERE application_name='synthetic_erasure_waiter' AND wait_event_type='Lock' AND wait_event='relation'", database) != '1':
            assert time.monotonic() < deadline, 'writer did not wait on the erasure fence'
            time.sleep(0.05)
        holder.stdin.write('ROLLBACK;\n\\q\n')
        holder.stdin.flush()
        holder.wait(timeout=10)
        assert holder.returncode == 0
        assert waiter.stdout.readline().strip() == 'FINISHED'
        waiter.stdin.write('\\q\n')
        waiter.stdin.flush()
        waiter.wait(timeout=10)
        assert waiter.returncode == 0
        assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, 'erasure_migrate') == '5|3|1'
        print(f'PASS {database}: concurrent audited INSERT waits until erasure transaction ends')
    finally:
        for process in (holder, waiter):
            if process and process.poll() is None:
                assert process.stdin is not None
                process.stdin.write('ROLLBACK;\n\\q\n')
                process.stdin.flush()
                process.wait(timeout=10)


def exercise(database):
    fixture(database)
    assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, 'erasure_migrate') == '5|3|1'
    before = snapshot(database)
    lock_regression(database)
    for role in ('erasure_web', 'erasure_unprivileged', 'mycfc_privacy_executor'):
        denied(f'SELECT * FROM classification_erasure.erase({ARGS},false)', database, role)
        denied(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, role)
        denied(f"INSERT INTO classification_erasure.erased_memberships VALUES('{MEMBERS[3]}')", database, role)
        denied(f"INSERT INTO classification_erasure.fence VALUES(txid_current(),ARRAY[6],ARRAY['{MEMBERS[3]}']::uuid[])", database, role)
    denied("UPDATE person_sport_assignment_events SET reason='Changed' WHERE id=6", database, expected='append-only')
    denied('DELETE FROM person_sport_assignment_events WHERE id=6', database, expected='append-only')
    denied("SET mycfc.classification_erasure='on'; DELETE FROM person_sport_assignment_events WHERE id=6", database, expected='append-only')
    denied(f"UPDATE user_memberships SET age_exception_reason=NULL,age_exception_by_id=NULL,age_exception_at=NULL WHERE id='{MEMBERS[3]}'", database)
    # IDs outside approved subject scope and unknown first-run IDs fail before writes.
    for events, memberships in ((json.dumps([{'id': 4, 'subject': D}]), '[]'),
                                (json.dumps([{'id': 999, 'subject': C}]), '[]'),
                                ('[]', json.dumps([{'id': MEMBERS[2], 'subject': D}])),
                                ('null', '[]')):
        denied(f"SELECT * FROM classification_erasure.erase('{T}','{events}','{memberships}',false)", database, 'erasure_migrate')
        assert snapshot(database) == before
        assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, 'erasure_migrate') == '5|3|1'
    # Inject a failure after audit deletion: transaction must roll everything back.
    sql("CREATE FUNCTION public.synthetic_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER z_synthetic_fail BEFORE UPDATE ON user_memberships FOR EACH ROW EXECUTE FUNCTION synthetic_fail()", database)
    denied(f'SELECT * FROM classification_erasure.erase({ARGS},false)', database, 'erasure_migrate', 'synthetic failure')
    sql('DROP TRIGGER z_synthetic_fail ON user_memberships; DROP FUNCTION synthetic_fail()', database)
    assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, 'erasure_migrate') == '5|3|1'
    assert sql('SELECT count(*) FROM classification_erasure.fence', database) == '0'
    assert sql('SELECT count(*) FROM classification_erasure.erased_memberships', database) == '0'
    # Real populated pre-erasure backup, not a serialized fixture.
    backup = command('docker', 'exec', NAME, 'pg_dump', '-U', 'postgres', '-Fc', database)
    assert len(backup) > 0
    assert sql(f'SELECT * FROM classification_erasure.erase({ARGS},false)', database, 'erasure_migrate') == '5|3'
    assert sql(f'SELECT * FROM classification_erasure.erase({ARGS},true)', database, 'erasure_migrate') == '0|0'
    assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', database, 'erasure_migrate') == '0|0|1'
    assert snapshot(database) == before
    conflicting_replay = json.dumps([{'id': 4, 'subject': C}, {'id': 4, 'subject': D}])
    denied(f"SELECT * FROM classification_erasure.erase('{T}','{conflicting_replay}','[]',true)", database, 'erasure_migrate')
    assert sql('SELECT count(*) FROM classification_erasure.fence', database) == '0'
    assert sql('SELECT count(*) FROM classification_erasure.erased_memberships', database) == '3'
    # Erased mismatch survives ordinary updates, but cannot authorize a new row.
    sql(f"UPDATE user_memberships SET updated_at=updated_at WHERE id='{MEMBERS[0]}'", database, 'erasure_web')
    denied(f"UPDATE user_memberships SET age_exception_reason='New exception',age_exception_by_id='{A}',age_exception_at=now() WHERE id='{MEMBERS[0]}'", database)
    denied(f"INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on) SELECT '{T}','{S}',id,'{CAT}','2026-01-01' FROM programmes WHERE code='Competition'", database, expected='age exception reason required')
    denied(f"UPDATE user_memberships SET ends_on='2026-05-01' WHERE id='{MEMBERS[0]}'", database, expected='recorded event, training or published prescription')
    denied("UPDATE training_prescriptions SET snapshot='{}'", database)
    restored = database + '_restored'
    command('docker', 'exec', NAME, 'createdb', '-U', 'postgres', '-O', 'erasure_migrate', restored)
    command('docker', 'exec', '-i', NAME, 'pg_restore', '-U', 'postgres', '--exit-on-error', '-d', restored, input=backup)
    # Isolation is structural: this container publishes no ports and runs no app.
    assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', restored, 'erasure_migrate') == '5|3|1'
    assert snapshot(restored) == before
    assert sql(f'SELECT * FROM classification_erasure.erase({ARGS},true)', restored, 'erasure_migrate') == '5|3'
    assert sql(f'SELECT * FROM classification_erasure.check_remaining({ARGS})', restored, 'erasure_migrate') == '0|0|1'
    assert snapshot(restored) == before
    assert sql('SELECT count(*) FROM classification_erasure.fence', restored) == '0'
    assert sql("SELECT NOT rolcanlogin FROM pg_roles WHERE rolname='mycfc_privacy_executor'", restored) == 't'
    print(f'PASS {database}: forbidden roles/forgery, scope, atomic rollback, 5/3 deletion, replay 0/0, all-public-row invariance, mismatch/history guards, populated pg_dump/restore reconciliation 0/0 (shared approver links: 1)')


def main():
    try:
        command('docker', 'run', '--name', NAME, '-e',
                'POSTGRES_PASSWORD=synthetic', '-d', 'postgres:17-alpine')
        command('docker', 'exec', NAME, 'sh', '-c',
                'until pg_isready -U postgres; do sleep 1; done')
        command('docker', 'exec', NAME, 'psql', '-U', 'postgres', '-v', 'ON_ERROR_STOP=1', '-c',
                'CREATE ROLE erasure_migrate NOLOGIN; CREATE ROLE erasure_web NOLOGIN; CREATE ROLE erasure_unprivileged NOLOGIN; CREATE ROLE mycfc_privacy_executor NOLOGIN; CREATE ROLE mycfc_media_cleanup NOLOGIN; CREATE ROLE mycfc_data_retention NOLOGIN;')
        for database in ('fresh', 'forward'):
            command('docker', 'exec', NAME, 'createdb', '-U', 'postgres', '-O', 'erasure_migrate', database)
            if database == 'fresh':
                baseline = (ROOT / 'internal/db/schema.sql').read_text()
            else:
                baseline = command('git', '-C', str(ROOT), 'show', PARENT + ':internal/db/schema.sql').decode()
            sql(baseline, database, 'erasure_migrate')
            if database == 'forward':
                sql((ROOT / f'internal/db/migrations/{MIGRATION}.sql').read_text(), database, 'erasure_migrate')
            sql('GRANT USAGE ON SCHEMA public TO erasure_web; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO erasure_web; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO erasure_web', database)
            exercise(database)
    finally:
        command('docker', 'rm', '-f', NAME)
        print('Disposable PostgreSQL container and backup removed')


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as error:
        print(error.stderr.decode())  # Synthetic-only diagnostics.
        raise
