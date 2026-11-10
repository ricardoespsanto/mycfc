#!/usr/bin/env python3
"""Bounded opt-in recipient browser fixture: actual Go bootstrap and low-role app.
Never adopts an existing Docker container/volume or database. No legacy upgrade gate.
"""
import argparse, datetime, hashlib, json, os, pathlib, shutil, socket, subprocess, threading, time, urllib.request
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('evidence', type=pathlib.Path)
p.add_argument('--name', required=True)
p.add_argument('--pg-port', type=int, required=True)
p.add_argument('--app-port', type=int, required=True)
p.add_argument('--runtime-manifest', type=pathlib.Path, action='append', required=True, help='Frozen files or minimum_files SHA256 manifest; may repeat')
p.add_argument('--fixture-date', type=datetime.date.fromisoformat, default=datetime.date.today())
a = p.parse_args(); root = pathlib.Path(__file__).resolve().parents[1]; out = a.evidence.resolve()
if not a.name.startswith('mycfc-recipient-browser-') or not a.name.removeprefix('mycfc-recipient-browser-').isalnum(): p.error('unique recipient fixture name required')
if out == root or root in out.parents: p.error('evidence must be outside checkout')
if a.pg_port == a.app_port: p.error('distinct ports required')
name = a.name; volume = name + '-pgdata'; browser = name + '-playwright'; label = 'mycfc.recipient-browser-owner'; owned = set(); app = None; stop = threading.Event(); worker = None
for kind, item in [('container', name), ('container', browser), ('volume', volume)]:
 if subprocess.run(['docker', kind, 'inspect', item], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0: raise SystemExit('refusing existing resource ' + item)
for port in [a.pg_port, a.app_port]:
 with socket.socket() as s: s.bind(('127.0.0.1', port))
out.mkdir(mode=0o700, parents=True, exist_ok=False)
(out/'absence.json').write_text(json.dumps({'container':name, 'browser':browser, 'volume':volume, 'absent_before_creation':True, 'ports_free':[a.pg_port,a.app_port]}))
def run(command, log, **kw):
 with (out/log).open('w') as f: subprocess.run(command, cwd=root, stdout=f, stderr=subprocess.STDOUT, check=True, **kw)
def inspect(kind, item): return json.loads(subprocess.check_output(['docker',kind,'inspect',item],stderr=subprocess.DEVNULL))[0]
def sql(query, variables=()):
 return subprocess.check_output(['docker','exec','-i',name,'psql','-U','recipient_bootstrap','-d','mycfc','-v','ON_ERROR_STOP=1','-At',*variables], input=query,text=True)
def facts():
 return json.loads(sql("""SELECT jsonb_object_agg(id::text,jsonb_build_object('plan_xmin',xmin::text,'publications',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.revision) FROM training_plan_publications r WHERE r.plan_id=p.id),'[]'::jsonb),'prescriptions',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY r.revision,s.id) FROM training_prescriptions s JOIN training_plan_publications r ON r.id=s.publication_id WHERE r.plan_id=p.id),'[]'::jsonb))) FROM training_plans p"""))
def runtime_hashes():
 manifest={}
 for path in a.runtime_manifest:
  document=json.loads(path.read_text());manifest.update(document.get('files',document.get('minimum_files',{})))
  if 'minimum_Makefile_sha256' in document:manifest['Makefile']=document['minimum_Makefile_sha256']
 if not manifest:raise RuntimeError('runtime hash inventory empty')
 actual={f:hashlib.sha256((root/f).read_bytes()).hexdigest() for f in manifest}
 if actual != manifest: raise RuntimeError('frozen runtime changed')
 return actual
def test_hashes():
 return {f:hashlib.sha256((root/f).read_bytes()).hexdigest() for f in ['e2e/auth.spec.mjs','e2e/week-workspace.spec.mjs','e2e/crew-transition-helpers.mjs','e2e/recipient-publication.spec.mjs','e2e/recipient-publication-seed.sql','e2e/run-recipient-publication.py']}
try:
 runtime = runtime_hashes(); tests = test_hashes(); (out/'source-manifest.json').write_text(json.dumps(runtime,indent=2)); (out/'test-manifest.json').write_text(json.dumps(tests,indent=2))
 run(['docker','volume','create','--label',label+'='+name,volume],'volume.log'); owned.add(volume)
 run(['docker','run','-d','--name',name,'--label',label+'='+name,'-e','POSTGRES_USER=recipient_bootstrap','-e','POSTGRES_PASSWORD=synthetic-bootstrap','-e','POSTGRES_DB=mycfc','-p',f'127.0.0.1:{a.pg_port}:5432','-v',volume+':/var/lib/postgresql/data','postgres:16.9','-c','timezone=Europe/Lisbon'],'container.log'); owned.add(name)
 bound=inspect('container',name)
 assert bound['Config']['Labels'][label]==name
 assert bound['NetworkSettings']['Ports']['5432/tcp']==[{'HostIp':'127.0.0.1','HostPort':str(a.pg_port)}]
 assert any(m.get('Name')==volume for m in bound['Mounts'])
 assert inspect('volume',volume)['Labels'][label]==name
 for _ in range(100):
  if subprocess.run(['docker','exec',name,'pg_isready','-U','recipient_bootstrap','-d','mycfc'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0: break
  time.sleep(.2)
 else: raise RuntimeError('owned PostgreSQL not ready')
 binding=json.loads(sql("SELECT json_build_object('system_identifier',system_identifier::text,'database',current_database(),'bootstrap',current_user) FROM pg_control_system()")); binding.update(container_id=bound['Id'], volume=volume, endpoint=f'127.0.0.1:{a.pg_port}', owner=name); (out/'target-binding.json').write_text(json.dumps(binding,indent=2))
 run(['go','build','-o',str(out/'server'),'./cmd/server'],'build.log')
 env={k:v for k,v in os.environ.items() if k in ['PATH','HOME','TMPDIR','LANG','LC_ALL','TZ']}
 for line in (root/'.env.example').read_text().splitlines():
  if line and not line.startswith('#') and '=' in line:
   k,v=line.split('=',1);env[k]=v.strip('"')
 base=f'http://127.0.0.1:{a.app_port}'
 env.update(APP_ENV='test',APP_VERSION='dev',GIT_SHA='0'*40,PORT=str(a.app_port),BASE_URL=base,POSTGRES_USER='recipient_bootstrap',POSTGRES_PASSWORD='synthetic-bootstrap',APP_DB_USER='recipient_web',APP_DB_PASSWORD='synthetic-web',MIGRATION_DB_USER='recipient_migration',MIGRATION_DB_PASSWORD='synthetic-migration',MEDIA_CLEANUP_DB_USER='recipient_media',MEDIA_CLEANUP_DB_PASSWORD='synthetic-media',DATA_RETENTION_DB_USER='recipient_retention',DATA_RETENTION_DB_PASSWORD='synthetic-retention',AWS_EC2_METADATA_DISABLED='true',SMTP_HOST='127.0.0.1',SMTP_PORT='1',S3_ENDPOINT='http://127.0.0.1:1',DATA_RIGHTS_CONTACT='privacy@example.test',TRAINING_SCHEDULE_ADMISSION_FILE='')
 for key in ['CONSENT_TERMS_URL','CONSENT_IMAGE_URL','CONSENT_MINOR_URL','PRIVACY_NOTICE_URL','COOKIE_NOTICE_URL','GALLERY_URL']: env[key]=env[key].replace('http://localhost:8080',base)
 dsn=lambda user,pw:f'postgres://{user}:{pw}@127.0.0.1:{a.pg_port}/mycfc?sslmode=disable'
 for command,user,pw in [('bootstrap-db','recipient_bootstrap','synthetic-bootstrap'),('migrate','recipient_migration','synthetic-migration'),('prepare-local-recipient-floor','recipient_migration','synthetic-migration'),('harden-db','recipient_bootstrap','synthetic-bootstrap')]:run([str(out/'server'),command],command+'.log',env=dict(env,DATABASE_URL=dsn(user,pw)))
 week=a.fixture_date+datetime.timedelta(days=7-a.fixture_date.weekday()); days=[week.isoformat(),(week+datetime.timedelta(days=7)).isoformat()]
 params={'fixture_date':a.fixture_date.isoformat(),'fixture_start':(a.fixture_date-datetime.timedelta(days=365)).isoformat(),'fixture_end':(a.fixture_date+datetime.timedelta(days=400)).isoformat(),'fixture_week':week.isoformat()}
 variables=[v for k,x in params.items() for v in ['-v',k+'='+x]]
 (out/'seed.log').write_text(sql((root/'e2e/recipient-publication-seed.sql').read_text(),variables)); (out/'fixture.json').write_text(json.dumps({'days':days,'synthetic_dates':params}))
 state=json.loads(sql("SELECT json_build_object('ledger_count',(SELECT count(*) FROM mycfc_meta.schema_migrations),'floor',(SELECT required_reader FROM training_schedule_control.state),'database',current_database())"));assert state['floor']==3; (out/'prepared-state.json').write_text(json.dumps(state))
 env['DATABASE_URL']=dsn('recipient_web','synthetic-web')
 with (out/'app.log').open('w') as f: app=subprocess.Popen([str(out/'server')],cwd=root,env=env,stdout=f,stderr=subprocess.STDOUT)
 for _ in range(100):
  if app.poll() is not None: raise RuntimeError('app exited; see app.log')
  try:
   if urllib.request.urlopen(base+'/health/ready').status==200:break
  except Exception:time.sleep(.2)
 else:raise RuntimeError('app readiness failed')
 def control():
  done=set()
  while not stop.wait(.05):
   for request in out.glob('*-ready.json'):
    stem=request.name.removesuffix('-ready.json')
    if stem not in done:
     try: result=facts()
     except Exception as e: result={'error':str(e)}
     (out/(stem+'-done.json')).write_text(json.dumps(result));done.add(stem)
 worker=threading.Thread(target=control,daemon=True);worker.start()
 deps=out/'dependencies';deps.mkdir()
 for f in ['package.json','package-lock.json']:shutil.copyfile(root/f,deps/f)
 run(['npm','ci','--ignore-scripts','--prefix',str(deps)],'dependencies.log');(root/'node_modules').mkdir(exist_ok=True)
 run(['docker','run','--rm','--name',browser,'--label',label+'='+name,'--network','host','--user',f'{os.getuid()}:{os.getgid()}','-e','HOME=/tmp','-e','E2E_BASE_URL='+base,'-e','E2E_RECIPIENT_PUBLICATION=1','-e','E2E_RECIPIENT_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/browser.json','-v',str(root)+':/src:ro','-v',str(deps/'node_modules')+':/src/node_modules:ro','-v',str(out)+':/evidence','-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble','node','node_modules/@playwright/test/cli.js','test','e2e/recipient-publication.spec.mjs','--workers=1','--retries=0','--output=/evidence/artifacts'],'browser.log')
 assert runtime==runtime_hashes();assert tests==test_hashes()
 (out/'final-facts.json').write_text(json.dumps(facts(),indent=2));(out/'summary.json').write_text(json.dumps({'browser_passed':True,'runtime93_unchanged':True,'test_source_unchanged':True,'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'runtime_role':'recipient_web','routes':'actual app.New SCS CSRF SQL; synthetic seed only','manual_acceptance':False},indent=2))
 print(out)
finally:
 stop.set()
 if worker:worker.join(timeout=2)
 if app:
  app.terminate()
  try:app.wait(timeout=10)
  except subprocess.TimeoutExpired:app.kill();app.wait(timeout=5)
 for kind,item in [('container',browser),('container',name),('volume',volume)]:
  try:
   data=inspect(kind,item);labels=data['Config']['Labels'] if kind=='container' else data['Labels']
   if labels.get(label)==name and (item in owned or item==browser):subprocess.run(['docker',kind,'rm',*(['-f'] if kind=='container' else []),item],check=True,stdout=subprocess.DEVNULL)
  except subprocess.CalledProcessError:pass
 cleanup={'app_stopped':app is None or app.poll() is not None}
 for kind,item in [('container',name),('container',browser),('volume',volume)]:cleanup[item]=subprocess.run(['docker',kind,'inspect',item],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode!=0
 (out/'cleanup.json').write_text(json.dumps(cleanup,indent=2))
