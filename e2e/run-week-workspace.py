#!/usr/bin/env python3
"""Opt-in real routes on a fresh synthetic PG16.9 database; no shared services.
Usage: python3 e2e/run-week-workspace.py /tmp/new-private-evidence [--app-source /path/to/integrated/source]
"""
import argparse, hashlib, html, http.server, json, os, pathlib, shutil, socket, subprocess, threading, time, urllib.parse, urllib.request, uuid
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('evidence',type=pathlib.Path)
parser.add_argument('--app-source',type=pathlib.Path)
args=parser.parse_args()
root=pathlib.Path(__file__).resolve().parents[1]
source=(args.app_source or root).resolve()
out=args.evidence.resolve()
if out==root or root in out.parents or out==source or source in out.parents:
    raise SystemExit('Evidence must be outside source checkouts')
out.mkdir(mode=0o700,parents=True,exist_ok=False)
os.chmod(out,0o700)
name='mycfc-week-workspace-'+uuid.uuid4().hex[:12]
database='mycfc_week_workspace_test'
app=None
attack_server=None
ports=[]
phase_results=[]
def run(command,log,**kwargs):
    with (out/log).open('w') as handle:
        subprocess.run(command,cwd=source,stdout=handle,stderr=subprocess.STDOUT,check=True,**kwargs)
def sql(query):
    return subprocess.check_output(['docker','exec','-i',name,'psql','-U','acceptance','-d',database,'-v','ON_ERROR_STOP=1','-At'],input=query,text=True)
def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0));return sock.getsockname()[1]
def browser(phase):
    titles={'missing':'missing season','overview':'desktop full week','mobile':'native mobile day','draft':'empty roster drafts','routines':'routine filters','publish':'real engine publishes','changes':'published snapshots','forged':'forged group','unavailable':'publication failure','revoked':'fresh revoked','csrf':'feature disabled','disabled':'feature disabled','accessibility':'keyboard focus validation'}
    command=['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}',
      '-e','HOME=/tmp','-e','E2E_BASE_URL='+base,'-e','E2E_WEEK_WORKSPACE=1','-e','E2E_WEEK_WORKSPACE_PHASE='+phase,
      '-e','E2E_WEEK_WORKSPACE_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/'+phase+'.json',
      '-v',str(root)+':/src:ro','-v',str(out/'dependencies/node_modules')+':/src/node_modules:ro','-v',str(out)+':/evidence',
      '-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble','node','node_modules/@playwright/test/cli.js','test',
      'e2e/week-workspace.spec.mjs','--grep',titles[phase],'--workers=1','--retries=0','--output=/evidence/'+phase+'-artifacts']
    run(command,phase+'.log')
    report=json.loads((out/(phase+'.json')).read_text())
    def walk(suite):
        for spec in suite.get('specs',[]):
            for case in spec['tests']:
                if case['status']!='skipped':
                    item={'phase':phase,'title':spec['title'],'status':case['status'],'attempts':[r['status'] for r in case['results']]}
                    phase_results.append(item)
                    assert item['status']=='expected' and item['attempts']==['passed'],item
        for child in suite.get('suites',[]):walk(child)
    for suite in report['suites']:walk(suite)
try:
    # Read-only source mount needs an existing ignored nested mountpoint.
    (root/'node_modules').mkdir(exist_ok=True)
    deps=out/'dependencies';deps.mkdir()
    for filename in ['package.json','package-lock.json']:shutil.copyfile(root/filename,deps/filename)
    run(['npm','ci','--ignore-scripts','--prefix',str(deps)],'dependencies.log')
    pgport,appport,attackport=port(),port(),port();ports=[pgport,appport,attackport]
    run(['docker','run','-d','--name',name,'-e','POSTGRES_USER=acceptance','-e','POSTGRES_PASSWORD=disposable-only',
       '-e','POSTGRES_DB='+database,'-p',f'127.0.0.1:{pgport}:5432','postgres:16.9','-c','timezone=Europe/Lisbon'],'container.log')
    for _ in range(60):
        if subprocess.run(['docker','exec',name,'pg_isready','-h','127.0.0.1','-U','acceptance','-d',database],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0:break
        time.sleep(1)
    else:raise RuntimeError('fresh PostgreSQL not ready')
    (out/'baseline.log').write_text(sql((source/'internal/db/schema.sql').read_text()))
    migrations=sorted(p.stem for p in (source/'internal/db/migrations').glob('*.sql'))+['reset-baseline-v1']
    sql('CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES '+','.join("('"+m+"')" for m in migrations)+' ON CONFLICT DO NOTHING;')
    assert int(sql('SELECT count(*) FROM mycfc_meta.schema_migrations'))==len(set(migrations))
    (out/'clean-seed.log').write_text(sql((root/'e2e/week-workspace-seed.sql').read_text()))
    assert int(sql('SELECT count(*) FROM seasons'))==0
    assert int(sql('SELECT count(*) FROM user_memberships'))==0
    run(['go','build','-o',str(out/'server'),'./cmd/server'],'build.log')
    # Do not inherit application or cloud credentials from the caller.
    env={k:v for k,v in os.environ.items() if k in ['PATH','HOME','TMPDIR','LANG','LC_ALL','TZ']}
    for line in (source/'.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            key,value=line.split('=',1);env[key]=value.strip('"')
    base=f'http://127.0.0.1:{appport}'
    env.update(APP_ENV='test',PORT=str(appport),BASE_URL=base,DATABASE_URL=f'postgres://acceptance:disposable-only@127.0.0.1:{pgport}/{database}?sslmode=disable',
       AWS_EC2_METADATA_DISABLED='true',SMTP_HOST='127.0.0.1',SMTP_PORT='1',S3_ENDPOINT='http://127.0.0.1:1',DATA_RIGHTS_CONTACT='privacy@example.test')
    for key in ['CONSENT_TERMS_URL','CONSENT_IMAGE_URL','CONSENT_MINOR_URL','PRIVACY_NOTICE_URL','COOKIE_NOTICE_URL','GALLERY_URL']:env[key]=env[key].replace('http://localhost:8080',base)
    with (out/'app.log').open('w') as log:app=subprocess.Popen([str(out/'server')],cwd=source,env=env,stdout=log,stderr=subprocess.STDOUT)
    for _ in range(60):
        if app.poll() is not None:raise RuntimeError('application exited; see app.log')
        try:
            if urllib.request.urlopen(base+'/health/ready').status==200:break
        except Exception:time.sleep(1)
    else:raise RuntimeError('application not ready')
    source_manifest={}
    for filename in subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z'],cwd=source).decode().split('\0'):
        path=source/filename
        if filename and path.is_file():source_manifest[filename]=hashlib.sha256(path.read_bytes()).hexdigest()
    (out/'source-manifest.json').write_text(json.dumps(source_manifest,sort_keys=True,indent=2))
    test_manifest={filename:hashlib.sha256((root/filename).read_bytes()).hexdigest() for filename in ['e2e/run-week-workspace.py','e2e/week-workspace.spec.mjs','e2e/week-workspace-seed.sql','e2e/week-workspace-plans-seed.sql','package-lock.json']}
    (out/'test-manifest.json').write_text(json.dumps(test_manifest,sort_keys=True,indent=2))
    metadata={'source':str(source),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip(),
      'source_diff_sha256':hashlib.sha256(subprocess.check_output(['git','diff','HEAD'],cwd=source)).hexdigest(),
      'source_manifest_sha256':hashlib.sha256((out/'source-manifest.json').read_bytes()).hexdigest(),
      'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'postgres':'16.9','timezone':sql('SHOW timezone').strip(),
      'migration_ledger_count':len(set(migrations)),'browser':'1.63.0','synthetic_only':True}
    (out/'metadata.json').write_text(json.dumps(metadata,indent=2))
    fixture=json.loads(sql("SELECT json_build_object('days',(SELECT json_agg((date_trunc('week',CURRENT_DATE)::date+n)::text ORDER BY n) FROM generate_series(0,6)n),'next_days',(SELECT json_agg((date_trunc('week',CURRENT_DATE)::date+7+n)::text ORDER BY n) FROM generate_series(0,6)n));"))
    (out/'fixture.json').write_text(json.dumps(fixture))
    browser('missing')
    assert int(sql('SELECT count(*) FROM seasons'))==0
    assert int(sql('SELECT count(*) FROM training_plans'))==0
    (out/'plans-seed.log').write_text(sql((root/'e2e/week-workspace-plans-seed.sql').read_text()))
    def rows(table):
        return json.loads(sql("SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM "+table+" r;"))
    def history():return {table:rows(table) for table in ['training_plan_publications','training_prescriptions','training_session_outcomes']}
    navigation_before={table:rows(table) for table in ['training_plans','training_sessions','training_session_segments','training_segment_blocks']}
    for phase in ['overview','mobile','routines','forged']:
        browser(phase)
        assert navigation_before=={table:rows(table) for table in navigation_before},phase+' mutated stored planning data'
    browser('draft')
    draft=json.loads((out/'created-draft.json').read_text())
    assert int(sql("SELECT count(*) FROM training_plans WHERE id='"+draft['week']+"' AND training_group_id='39800000-0000-0000-0000-000000000202'"))==1
    assert int(sql("SELECT count(*) FROM training_sessions WHERE id='"+draft['session']+"' AND plan_id='"+draft['week']+"'"))==1
    alternate=json.loads((out/'created-alternate.json').read_text())
    assert int(sql("SELECT count(*) FROM training_sessions WHERE id='"+alternate['session']+"' AND plan_id='"+alternate['week']+"' AND (starts_at AT TIME ZONE 'Europe/Lisbon')::date='"+alternate['day']+"'"))==1
    browser('publish')
    assert int(sql("SELECT count(*) FROM training_plan_publications WHERE plan_id='39800000-0000-0000-0000-000000000300'"))==1
    assert int(sql("SELECT count(*) FROM training_plan_publications WHERE plan_id<>'39800000-0000-0000-0000-000000000300'"))==0
    # Outcome is an explicitly seeded preservation fixture after real UI publication.
    sql("INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status,distance_metres) SELECT session_id,athlete_user_id,id,'COMPLETED',1234 FROM training_prescriptions WHERE session_id='39800000-0000-0000-0000-000000000400' AND athlete_user_id='39800000-0000-0000-0000-000000000011';")
    before=history();(out/'retained-before.json').write_text(json.dumps(before,sort_keys=True,indent=2))
    browser('changes')
    assert history()==before,'draft editing/stale publication changed snapshots or outcomes'
    # Documented DB fault injection only. No application mock/test switch.
    # Rename is reverted before any history comparison and in finally on errors.
    sql('ALTER TABLE training_plan_publications RENAME TO week_probe_publications_unavailable;')
    try:browser('unavailable')
    finally:sql('ALTER TABLE week_probe_publications_unavailable RENAME TO training_plan_publications;')
    assert history()==before,'publication read-failure probe changed history'
    class AttackOrigin(http.server.BaseHTTPRequestHandler):
        def log_message(self,*unused):pass
        def do_GET(self):
            body=('<!doctype html><html lang="en"><title>Synthetic cross-origin probe</title><h1>Synthetic cross-origin probe</h1>'
              '<form method="post" action="'+html.escape(base+'/admin/treinos/estruturados/sessoes',quote=True)+'">'
              '<input name="plan_id" value="39800000-0000-0000-0000-000000000300"><input name="title" value="Mutação de origem indevida">'
              '<input name="entry_kind" value="TRAINING"><input name="starts_at" value="'+fixture['days'][2]+'T21:10"><input name="ends_at" value="'+fixture['days'][2]+'T21:40">'
              '<button type="submit">Submit cross-origin form</button></form></html>').encode()
            self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.end_headers();self.wfile.write(body)
    attack_server=http.server.ThreadingHTTPServer(('127.0.0.1',attackport),AttackOrigin)
    threading.Thread(target=attack_server.serve_forever,daemon=True).start()
    fixture['attack_origin']=f'http://127.0.0.1:{attackport}';(out/'fixture.json').write_text(json.dumps(fixture))
    browser('csrf')
    # Handshake applies revocation only after the browser holds the real native form.
    stopped=threading.Event()
    def revoke():
        while not stopped.wait(0.05):
            if (out/'revoke-ready.json').exists():
                try:
                    sql("UPDATE staff_grants SET revoked_at=now(),revoked_by_id='39800000-0000-0000-0000-000000000001',revoke_reason='Synthetic browser grant revocation' WHERE user_id='39800000-0000-0000-0000-000000000003';")
                    (out/'revoked-applied.json').write_text(json.dumps({'ok':True}));return
                except Exception as error:
                    (out/'revoked-applied.json').write_text(json.dumps({'ok':False,'error':str(error)}));return
    worker=threading.Thread(target=revoke,daemon=True);worker.start()
    try:browser('revoked')
    finally:stopped.set();worker.join(timeout=2)
    browser('accessibility')
    assert history()==before,'navigation/denied mutations/accessibility checks changed history'
    assert int(sql("SELECT count(*) FROM training_sessions WHERE title IN ('Mutação revogada não guardada','Mutação de origem indevida')"))==0
    sql("UPDATE feature_flags SET mode='DISABLED' WHERE feature_key='structured_training_planning';")
    browser('disabled')
    (out/'retained-after.json').write_text(json.dumps(history(),sort_keys=True,indent=2))
    checks={'history_equal':history()==before,'publication_count':int(sql('SELECT count(*) FROM training_plan_publications')),
      'outcome_count':int(sql('SELECT count(*) FROM training_session_outcomes')),'draft_week_id':draft['week'],'draft_session_id':draft['session']}
    assert checks['history_equal'] and checks['publication_count']==1 and checks['outcome_count']==1,checks
    final_manifest={filename:hashlib.sha256((root/filename).read_bytes()).hexdigest() for filename in test_manifest}
    (out/'test-manifest-final.json').write_text(json.dumps(final_manifest,sort_keys=True,indent=2))
    (out/'summary.json').write_text(json.dumps({'cases':phase_results,'checks':checks,'metadata':metadata,'test_source_unchanged':test_manifest==final_manifest},indent=2))
    print(json.dumps({'passed':len(phase_results),'checks':checks,'evidence':str(out)},indent=2))
finally:
    if attack_server:
        attack_server.shutdown();attack_server.server_close()
    if app:
        app.terminate()
        try:app.wait(timeout=15)
        except subprocess.TimeoutExpired:app.kill();app.wait()
    subprocess.run(['docker','rm','-f','-v',name+'-browser',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    cleanup={'app_stopped':app is None or app.poll() is not None,'owned_containers_remaining':subprocess.check_output(['docker','ps','-a','--filter','name='+name,'--format','{{.Names}}'],text=True).strip()}
    for value in ports:
        with socket.socket() as sock:cleanup[str(value)+'_closed']=sock.connect_ex(('127.0.0.1',value))!=0
    (out/'cleanup.json').write_text(json.dumps(cleanup,indent=2));print('CLEANUP',json.dumps(cleanup))
