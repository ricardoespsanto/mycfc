#!/usr/bin/env python3
"""Only new acceptance specs, fresh disposable PG, isolated ports and pinned browser."""
import json, os, pathlib, socket, subprocess, sys, time, urllib.request
root = pathlib.Path(__file__).resolve().parents[1]
out = pathlib.Path(sys.argv[1]).resolve()
out.mkdir(parents=True, exist_ok=False, mode=0o700)
name = 'mycfc-crew-transition-' + str(os.getpid())
app = None
ports = []
def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]
def run(args, log, **kwargs):
    with (out / log).open('w') as f:
        subprocess.run(args, cwd=root, stdout=f, stderr=subprocess.STDOUT, check=True, **kwargs)
def sql(text):
    return subprocess.check_output(['docker','exec','-i',name,'psql','-U','acceptance','-d','mycfc_crew_transition_test','-v','ON_ERROR_STOP=1','-At'],input=text,text=True)
def fixture():
    dates=json.loads(sql("SELECT json_build_object('today',CURRENT_DATE,'yesterday',CURRENT_DATE-1,'tomorrow',CURRENT_DATE+1);"))
    rows=sql("SELECT COALESCE(json_object_agg(athlete_user_id::text||':'||session_id::text,id::text),'{}'::json) FROM training_prescriptions;")
    dates['prescriptions']=json.loads(rows)
    (out/'fixture.json').write_text(json.dumps(dates,indent=2))
def browser(spec, phase):
    fixture()
    run(['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}',
         '-e','E2E_BASE_URL='+base,'-e','E2E_CREW_TRANSITION=1','-e','E2E_CREW_TRANSITION_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/'+phase+'.json',
         '-v',str(root)+':/src:ro','-v',str(out)+':/evidence','-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble',
         'npx','playwright','test',spec,'--workers=1','--retries=0','--output=/evidence/'+phase+'-artifacts'],phase+'.log')
def retained():
    return json.loads(sql("SELECT json_build_object('events',(SELECT json_agg(row_to_json(e) ORDER BY id) FROM events e),'responses',(SELECT json_agg(row_to_json(r) ORDER BY user_id) FROM event_responses r),'outcomes',(SELECT json_agg(row_to_json(o) ORDER BY user_id) FROM training_session_outcomes o),'sessions',(SELECT json_agg(row_to_json(s) ORDER BY id) FROM training_sessions s),'prescriptions',(SELECT json_agg(row_to_json(p) ORDER BY id) FROM training_prescriptions p WHERE session_id='33733900-0000-0000-0000-000000000400'));"))
try:
    ports = [free_port(), free_port()]
    pgport, appport = ports
    run(['git','rev-parse','HEAD'],'checkpoint.txt')
    run(['docker','run','-d','--name',name,'-e','POSTGRES_USER=acceptance','-e','POSTGRES_PASSWORD=disposable-only','-e','POSTGRES_DB=mycfc_crew_transition_test','-p',f'127.0.0.1:{pgport}:5432','postgres:16','-c','timezone=Europe/Lisbon'],'container.log')
    for _ in range(60):
        if subprocess.run(['docker','exec',name,'pg_isready','-U','acceptance'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0: break
        time.sleep(1)
    else: raise RuntimeError('PG not ready')
    run(['docker','exec','-i',name,'psql','-U','acceptance','-d','mycfc_crew_transition_test','-v','ON_ERROR_STOP=1'],'baseline.log',input=(root/'internal/db/schema.sql').read_text(),text=True)
    migrations=sorted(p.stem for p in (root/'internal/db/migrations').glob('*.sql'))
    sql('CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES '+','.join("('"+m+"')" for m in migrations)+' ON CONFLICT DO NOTHING;')
    (out/'seed.log').write_text(sql((root/'e2e/crew-transition-seed.sql').read_text()))
    run(['go','build','-o',str(out/'server'),'./cmd/server'],'build.log')
    env=dict(os.environ)
    for line in (root/'.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            k,v=line.split('=',1);env[k]=v.strip('"')
    base=f'http://127.0.0.1:{appport}'
    env.update(APP_ENV='test',PORT=str(appport),BASE_URL=base,DATABASE_URL=f'postgres://acceptance:disposable-only@127.0.0.1:{pgport}/mycfc_crew_transition_test?sslmode=disable',AWS_EC2_METADATA_DISABLED='true',SMTP_HOST='127.0.0.1',S3_ENDPOINT='http://127.0.0.1:1')
    for k in ['CONSENT_TERMS_URL','CONSENT_IMAGE_URL','CONSENT_MINOR_URL','PRIVACY_NOTICE_URL','COOKIE_NOTICE_URL','GALLERY_URL']: env[k]=env[k].replace('http://localhost:8080',base)
    with (out/'app.log').open('w') as log:
        app=subprocess.Popen([str(out/'server')],cwd=root,env=env,stdout=log,stderr=subprocess.STDOUT)
    for _ in range(60):
        try:
            if urllib.request.urlopen(base+'/health/ready').status==200:break
        except Exception: time.sleep(1)
    else: raise RuntimeError('app not ready')
    browser('e2e/crew-transition-publish.spec.mjs','publish')
    sql("INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres) SELECT session_id,athlete_user_id,id,'COMPLETED',1000 FROM training_prescriptions WHERE athlete_user_id IN ('33733900-0000-0000-0000-000000000011','33733900-0000-0000-0000-000000000017');")
    before=retained();(out/'retained-before.json').write_text(json.dumps(before,indent=2))
    browser('e2e/crew-transition-submit.spec.mjs','submit')
    assert retained()==before, 'crew edits/variation resolution/UI transition mutated historical records'
    # Separate subjects, dates fixture-seeded; never alter immutable starts or advance server clock.
    transition="""BEGIN;
UPDATE user_memberships SET ends_on=CURRENT_DATE-1 WHERE user_id IN ('33733900-0000-0000-0000-000000000011','33733900-0000-0000-0000-000000000017');
INSERT INTO user_memberships(user_id,season_id,programme_id,competition_category_id,starts_on)
SELECT u.id,'33733900-0000-0000-0000-000000000100',p.id,'33733900-0000-0000-0000-000000000101',CURRENT_DATE FROM users u CROSS JOIN programmes p WHERE u.id IN ('33733900-0000-0000-0000-000000000011','33733900-0000-0000-0000-000000000017') AND p.code='Initiation';
COMMIT;"""
    (out/'effective-state.log').write_text(sql(transition))
    assert retained()==before, 'historical records changed at effective transition'
    browser('e2e/crew-transition-history.spec.mjs','history')
    assert retained()==before, 'historical records/snapshots changed after future publication'
    sql("UPDATE guardian_authority_relationships SET state='EXPIRED',version=version+1,conflict=false,conflict_actor_ref=NULL,updated_at=now() WHERE guardian_user_id='33733900-0000-0000-0000-000000000016'; UPDATE staff_grants SET revoked_at=now(),revoked_by_id='33733900-0000-0000-0000-000000000001',revoke_reason='Synthetic browser revocation' WHERE user_id='33733900-0000-0000-0000-000000000002';")
    browser('e2e/crew-transition-revocation.spec.mjs','revocation')
    assert retained()==before, 'revocation changed historical material'
    (out/'retained-after.json').write_text(json.dumps(retained(),indent=2))
    (out/'immutability.log').write_text(sql("""DO $$ BEGIN
      BEGIN UPDATE training_prescriptions SET snapshot=snapshot||'{"tampered":true}'::jsonb;
        RAISE EXCEPTION 'mutation was accepted';
      EXCEPTION WHEN raise_exception THEN IF SQLERRM <> 'training publications and prescriptions are immutable' THEN RAISE; END IF; END;
      BEGIN UPDATE training_plan_publications SET change_summary='tampered';
        RAISE EXCEPTION 'mutation was accepted';
      EXCEPTION WHEN raise_exception THEN IF SQLERRM <> 'training publications and prescriptions are immutable' THEN RAISE; END IF; END;
    END $$;"""))
    assert retained()==before, 'immutability attempts changed history'
    checks={
      'historical_prescriptions':int(sql("SELECT count(*) FROM training_prescriptions WHERE session_id='33733900-0000-0000-0000-000000000400';")),
      'future_prescriptions':int(sql("SELECT count(*) FROM training_prescriptions WHERE session_id='33733900-0000-0000-0000-000000000401';")),
      'crew_count':int(sql('SELECT count(*) FROM training_variation_groups;')),
      'overlap_beta':int(sql("SELECT count(*) FROM training_variation_group_members WHERE membership_id='33733900-0000-0000-0000-000000000112';")),
      'ui_transition_intervals':int(sql("SELECT count(*) FROM user_memberships WHERE user_id='33733900-0000-0000-0000-000000000015';")),
      'future_former_recipients':int(sql("SELECT count(*) FROM training_prescriptions WHERE session_id='33733900-0000-0000-0000-000000000401' AND athlete_user_id IN ('33733900-0000-0000-0000-000000000011','33733900-0000-0000-0000-000000000017');")),
      'max_active_participation':int(sql('SELECT max(n) FROM (SELECT count(*) n FROM user_memberships WHERE starts_on<=CURRENT_DATE AND (ends_on IS NULL OR ends_on>=CURRENT_DATE) GROUP BY user_id) q;'))}
    assert checks==dict(historical_prescriptions=6,future_prescriptions=3,crew_count=3,overlap_beta=3,ui_transition_intervals=2,future_former_recipients=0,max_active_participation=1),checks
    cases=[]
    def walk(s):
        for spec in s.get('specs',[]):
            for t in spec['tests']: cases.append({'title':spec['title'],'status':t['status'],'attempts':[r['status'] for r in t['results']]})
        for child in s.get('suites',[]):walk(child)
    for phase in ['publish','submit','history','revocation']:walk(json.loads((out/(phase+'.json')).read_text()))
    assert len(cases)==10 and all(c['status']=='expected' and c['attempts']==['passed'] for c in cases)
    (out/'summary.json').write_text(json.dumps({'cases':cases,'checks':checks,'historical_records_equal':True},indent=2))
    print(json.dumps({'cases':cases,'checks':checks},indent=2))
finally:
    if app:
        app.terminate()
        try: app.wait(timeout=20)
        except subprocess.TimeoutExpired: app.kill();app.wait()
    subprocess.run(['docker','rm','-f',name,name+'-browser'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    cleanup={'app_stopped':app is None or app.poll() is not None,'containers':subprocess.check_output(['docker','ps','-a','--filter','name='+name,'--format','{{.Names}}'],text=True).strip()}
    for port in ports:
        with socket.socket() as s:cleanup[str(port)+'_closed']=s.connect_ex(('127.0.0.1',port))!=0
    (out/'cleanup.json').write_text(json.dumps(cleanup,indent=2))
    print('CLEANUP',json.dumps(cleanup))
