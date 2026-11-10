#!/usr/bin/env python3
"""Opt-in real routes on a fresh synthetic PG16.9 database; no shared services.
Usage: python3 e2e/run-setup-links.py /tmp/new-private-evidence [--app-source /path/to/integrated/source]
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
name='mycfc-setup-links-'+uuid.uuid4().hex[:12]
database='mycfc_setup_links_test'
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
    titles={'ready':'ready coach','states':'optional help','missing-group':'missing-current-season','season-classification':'season handoff returns to classification','season-definitions':'season handoff returns to definitions','definitions':'existing definition task','guidance':'configured category','search':'classification name search','dated-mac':'origin MAC','assignment':'native assignment','correction-mac':'correction MAC','forged':'unauthorized origins','legacy':'legacy origin-free','revoked':'revoked origin','csrf':'actual cross-origin','accessibility':'actual setup pages'}
    command=['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}',
      '-e','HOME=/tmp','-e','E2E_BASE_URL='+base,'-e','E2E_SETUP_LINKS=1','-e','E2E_SETUP_LINKS_PHASE='+phase,
      '-e','E2E_SETUP_LINKS_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/'+phase+'.json',
      '-v',str(root)+':/src:ro','-v',str(out/'dependencies/node_modules')+':/src/node_modules:ro','-v',str(out)+':/evidence',
      '-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble','node','node_modules/@playwright/test/cli.js','test',
      'e2e/setup-links.spec.mjs','--grep',titles[phase],'--workers=1','--retries=0','--output=/evidence/'+phase+'-artifacts']
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
    (out/'clean-seed.log').write_text(sql((root/'e2e/setup-links-seed.sql').read_text()))
    assert int(sql('SELECT count(*) FROM seasons'))==1
    assert int(sql('SELECT count(*) FROM user_memberships'))==2
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
    test_manifest={filename:hashlib.sha256((root/filename).read_bytes()).hexdigest() for filename in ['e2e/run-setup-links.py','e2e/setup-links.spec.mjs','e2e/setup-links-seed.sql','package-lock.json']}
    (out/'test-manifest.json').write_text(json.dumps(test_manifest,sort_keys=True,indent=2))
    metadata={'source':str(source),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip(),
      'source_diff_sha256':hashlib.sha256(subprocess.check_output(['git','diff','HEAD'],cwd=source)).hexdigest(),
      'source_manifest_sha256':hashlib.sha256((out/'source-manifest.json').read_bytes()).hexdigest(),
      'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'postgres':'16.9','timezone':sql('SHOW timezone').strip(),
      'migration_ledger_count':len(set(migrations)),'browser':'1.63.0','synthetic_only':True}
    (out/'metadata.json').write_text(json.dumps(metadata,indent=2))
    fixture=json.loads(sql("SELECT json_build_object('today',CURRENT_DATE,'day',date_trunc('week',CURRENT_DATE)::date+4,'competition_programme',(SELECT id FROM programmes WHERE code='Competition'));"))
    (out/'fixture.json').write_text(json.dumps(fixture))
    def rows(table,omit_current=False):
        row="to_jsonb(r)-'is_current'" if omit_current else 'to_jsonb(r)'
        return json.loads(sql("SELECT COALESCE(jsonb_agg("+row+" ORDER BY ("+row+")::text),'[]'::jsonb) FROM "+table+" r;"))
    def retained():return {table:rows(table,table=='seasons') for table in ['seasons','training_groups','training_group_members','training_plans','training_sessions','training_plan_publications','training_prescriptions','training_session_outcomes']}
    before=retained();(out/'retained-before.json').write_text(json.dumps(before,sort_keys=True,indent=2))
    for phase in ['ready','states']:browser(phase)
    # All three handoffs deliberately select the already registered plan season.
    for phase in ['missing-group','season-classification','season-definitions']:
        sql('UPDATE seasons SET is_current=false WHERE is_current;')
        browser(phase)
        assert sql('SELECT id FROM seasons WHERE is_current').strip()=='39500000-0000-0000-0000-000000000100'
        assert retained()==before,phase+' changed stored seasons or planning history'
    browser('definitions')
    fixture['adult_category']=sql("SELECT id FROM competition_categories WHERE code='SYN_ADULT'").strip()
    assert fixture['adult_category']
    (out/'fixture.json').write_text(json.dumps(fixture))
    for phase in ['guidance','search']:browser(phase)
    memberships_before=rows('user_memberships')
    browser('dated-mac')
    assert rows('user_memberships')==memberships_before,'origin token tamper assigned participation'
    browser('assignment')
    assert int(sql("SELECT count(*) FROM user_memberships WHERE user_id='39500000-0000-0000-0000-000000000012' AND programme_id='"+fixture['competition_programme']+"'"))==1
    assert int(sql("SELECT count(*) FROM person_sporting_modalities WHERE user_id='39500000-0000-0000-0000-000000000012' AND modality_code='CANOEING'"))==1
    selected_before=rows('person_sporting_modalities');events_before=rows('person_sport_assignment_events')
    browser('correction-mac')
    assert rows('person_sporting_modalities')==selected_before and rows('person_sport_assignment_events')==events_before,'origin MAC tamper changed sporting selections'
    for phase in ['forged','legacy']:browser(phase)
    stopped=threading.Event()
    def revoke():
        while not stopped.wait(0.05):
            if (out/'revoke-ready.json').exists():
                try:
                    sql("UPDATE staff_grants SET revoked_at=now(),revoked_by_id='39500000-0000-0000-0000-000000000001',revoke_reason='Synthetic origin grant revocation' WHERE user_id='39500000-0000-0000-0000-000000000004' AND programme_id='"+fixture['competition_programme']+"';")
                    (out/'revoked-applied.json').write_text(json.dumps({'ok':True}));return
                except Exception as error:(out/'revoked-applied.json').write_text(json.dumps({'ok':False,'error':str(error)}));return
    worker=threading.Thread(target=revoke,daemon=True);worker.start()
    try:browser('revoked')
    finally:stopped.set();worker.join(timeout=2)
    assert rows('person_sporting_modalities')==selected_before and rows('person_sport_assignment_events')==events_before,'revoked-origin submission changed selections'
    class AttackOrigin(http.server.BaseHTTPRequestHandler):
        def log_message(self,*unused):pass
        def do_GET(self):
            body=('<!doctype html><html lang="en"><title>Synthetic cross-origin probe</title><h1>Synthetic cross-origin probe</h1>'
              '<form method="post" action="'+html.escape(base+'/equipa/escaloes',quote=True)+'">'
              '<input name="scope" value="39500000-0000-0000-0000-000000000100:'+fixture['competition_programme']+'">'
              '<input name="code" value="ORIGIN_ATTACK"><input name="name" value="Definição indevida">'
              '<input name="origin_group" value="39500000-0000-0000-0000-000000000201"><input name="origin_week" value="39500000-0000-0000-0000-000000000301"><input name="origin_day" value="'+fixture['day']+'">'
              '<button type="submit">Submit cross-origin form</button></form></html>').encode()
            self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.end_headers();self.wfile.write(body)
    attack_server=http.server.ThreadingHTTPServer(('127.0.0.1',attackport),AttackOrigin)
    threading.Thread(target=attack_server.serve_forever,daemon=True).start();fixture['attack_origin']=f'http://127.0.0.1:{attackport}';(out/'fixture.json').write_text(json.dumps(fixture))
    for phase in ['csrf','accessibility']:browser(phase)
    assert retained()==before,'setup handoffs altered stored planning history or auto-added/published'
    assert int(sql("SELECT count(*) FROM competition_categories WHERE code IN ('NOT_SAVED','ERROR_ONLY','ORIGIN_ATTACK')"))==0
    checks={'season_count':int(sql('SELECT count(*) FROM seasons')),'current_season':sql('SELECT id FROM seasons WHERE is_current').strip(),
      'membership_count':int(sql('SELECT count(*) FROM user_memberships')),'group_members':int(sql('SELECT count(*) FROM training_group_members')),
      'category_count':int(sql('SELECT count(*) FROM competition_categories')),'publication_count':int(sql('SELECT count(*) FROM training_plan_publications')),
      'prescription_count':int(sql('SELECT count(*) FROM training_prescriptions')),'planning_history_equal':retained()==before}
    assert checks==dict(season_count=1,current_season='39500000-0000-0000-0000-000000000100',membership_count=3,group_members=1,category_count=3,publication_count=0,prescription_count=0,planning_history_equal=True),checks
    (out/'retained-after.json').write_text(json.dumps(retained(),sort_keys=True,indent=2))
    final_manifest={filename:hashlib.sha256((root/filename).read_bytes()).hexdigest() for filename in test_manifest};(out/'test-manifest-final.json').write_text(json.dumps(final_manifest,sort_keys=True,indent=2))
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
