#!/usr/bin/env python3
"""Opt-in real routes on a fresh synthetic PG16.9 database; no shared services.
Usage: python3 e2e/run-seasons.py /tmp/new-private-evidence [--app-source /path/to/integrated/source]
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
name='mycfc-seasons-'+uuid.uuid4().hex[:12]
database='mycfc_seasons_test'
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
    titles={'empty':'empty database','create':'native no-JavaScript','capture':'real signed preview','select':'preview requires','validation':'invalid missing','stale':'concurrent previews','tamper':'tampered preview','returns':'selection returns','denied':'coach cannot','csrf':'genuine cross-origin','disabled':'shared season','accessibility':'mobile desktop','expiry':'unaltered real'}
    command=['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}',
      '-e','HOME=/tmp','-e','E2E_BASE_URL='+base,'-e','E2E_SEASONS=1','-e','E2E_SEASONS_PHASE='+phase,
      '-e','E2E_SEASONS_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/'+phase+'.json',
      '-v',str(root)+':/src:ro','-v',str(out/'dependencies/node_modules')+':/src/node_modules:ro','-v',str(out)+':/evidence',
      '-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble','node','node_modules/@playwright/test/cli.js','test',
      'e2e/seasons.spec.mjs','--grep',titles[phase],'--workers=1','--retries=0','--output=/evidence/'+phase+'-artifacts']
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
    (out/'clean-seed.log').write_text(sql((root/'e2e/seasons-seed.sql').read_text()))
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
    test_manifest={filename:hashlib.sha256((root/filename).read_bytes()).hexdigest() for filename in ['e2e/run-seasons.py','e2e/seasons.spec.mjs','e2e/seasons-seed.sql','e2e/seasons-history-seed.sql','package-lock.json']}
    (out/'test-manifest.json').write_text(json.dumps(test_manifest,sort_keys=True,indent=2))
    metadata={'source':str(source),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip(),
      'source_diff_sha256':hashlib.sha256(subprocess.check_output(['git','diff','HEAD'],cwd=source)).hexdigest(),
      'source_manifest_sha256':hashlib.sha256((out/'source-manifest.json').read_bytes()).hexdigest(),
      'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'postgres':'16.9','timezone':sql('SHOW timezone').strip(),
      'migration_ledger_count':len(set(migrations)),'browser':'1.63.0','synthetic_only':True}
    (out/'metadata.json').write_text(json.dumps(metadata,indent=2))
    fixture=json.loads(sql("SELECT json_build_object('starts_on',CURRENT_DATE-30,'ends_on',CURRENT_DATE+60);"))
    (out/'fixture.json').write_text(json.dumps(fixture))
    before_empty=sql("SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY id),'[]'::jsonb) FROM seasons s;")
    browser('empty')
    assert sql("SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY id),'[]'::jsonb) FROM seasons s;")==before_empty, 'GET silently provisioned a season'
    browser('create')
    fixture.update(json.loads((out/'created-season.json').read_text()))
    (out/'fixture.json').write_text(json.dumps(fixture))
    assert int(sql('SELECT count(*) FROM seasons'))==1
    assert int(sql('SELECT count(*) FROM seasons WHERE is_current'))==0, 'creation selected a season'
    browser('capture')
    assert int(sql('SELECT count(*) FROM seasons WHERE is_current'))==0, 'preview selected a season'
    browser('select')
    assert sql('SELECT id FROM seasons WHERE is_current').strip()==fixture['season_a']
    (out/'history-seed.log').write_text(sql((root/'e2e/seasons-history-seed.sql').read_text()))
    browser('validation')
    fixture['season_b']=sql("SELECT id FROM seasons WHERE code='SYN_B'").strip()
    fixture['season_c']=sql("SELECT id FROM seasons WHERE code='SYN_C'").strip()
    # A genuine second loopback origin serves only an attacker form, never files.
    class AttackOrigin(http.server.BaseHTTPRequestHandler):
        def log_message(self,*unused):pass
        def do_GET(self):
            body=('<!doctype html><html lang="en"><title>Synthetic cross-origin probe</title><h1>Synthetic cross-origin probe</h1>'
              '<form method="post" action="'+html.escape(base+'/admin/epocas/criar',quote=True)+'">'
              '<input name="code" value="SYN_ATTACK"><input name="name" value="Criação indevida">'
              '<input name="starts_on" value="2024-02-29"><input name="ends_on" value="2024-02-29">'
              '<button type="submit">Submit cross-origin form</button></form></html>').encode()
            self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.end_headers();self.wfile.write(body)
    attack_server=http.server.ThreadingHTTPServer(('127.0.0.1',attackport),AttackOrigin)
    threading.Thread(target=attack_server.serve_forever,daemon=True).start()
    fixture['attack_origin']=f'http://127.0.0.1:{attackport}'
    (out/'fixture.json').write_text(json.dumps(fixture))
    tables=['seasons','user_memberships','training_groups','training_group_members','training_plans','training_sessions','training_plan_publications','training_prescriptions']
    def retained():
        result={}
        for table in tables:
            row="to_jsonb(s)-'is_current'" if table=='seasons' else 'to_jsonb(s)'
            result[table]=json.loads(sql("SELECT COALESCE(jsonb_agg("+row+" ORDER BY ("+row+")::text),'[]'::jsonb) FROM "+table+" s;"))
        return result
    before=retained();(out/'retained-before.json').write_text(json.dumps(before,sort_keys=True,indent=2))
    sql("UPDATE feature_flags SET mode='ENABLED' WHERE feature_key='structured_training_planning';")
    for phase in ['stale','tamper','returns','denied','csrf','accessibility']:
        browser(phase)
        assert retained()==before, phase+' changed stored history or season metadata'
    sql("UPDATE feature_flags SET mode='DISABLED' WHERE feature_key='structured_training_planning';")
    browser('disabled')
    captured=json.loads((out/'expiry-preview.json').read_text())
    while time.time()<=captured['expires']:
        remaining=captured['expires']-time.time()+1
        status={'waiting_for_real_token_expiry':True,'expiry_utc_epoch':captured['expires'],'remaining_seconds':round(remaining),'process_running':app.poll() is None}
        (out/'expiry-wait.json').write_text(json.dumps(status,indent=2))
        print(json.dumps(status),flush=True)
        time.sleep(min(30,max(1,remaining)))
    browser('expiry')
    assert retained()==before, 'expiry recovery changed historical records or dates'
    (out/'retained-after.json').write_text(json.dumps(retained(),sort_keys=True,indent=2))
    checks={'season_count':int(sql('SELECT count(*) FROM seasons')),'current_count':int(sql('SELECT count(*) FROM seasons WHERE is_current')),
      'current_id':sql('SELECT id FROM seasons WHERE is_current').strip(),'history_equal':retained()==before,
      'prescription_count':int(sql('SELECT count(*) FROM training_prescriptions')),'token_expired_naturally':time.time()>captured['expires']}
    assert checks==dict(season_count=3,current_count=1,current_id=fixture['season_a'],history_equal=True,prescription_count=1,token_expired_naturally=True),checks
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
