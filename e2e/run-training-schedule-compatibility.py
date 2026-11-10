#!/usr/bin/env python3
"""Opt-in isolated C reader coverage. Never targets shared databases or services."""
import argparse, hashlib, http.server, json, os, pathlib, shutil, socket, subprocess, threading, time, urllib.request, uuid
p=argparse.ArgumentParser(description=__doc__);p.add_argument('evidence',type=pathlib.Path);p.add_argument('--app-source',required=True,type=pathlib.Path);a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[1];source=a.app_source.resolve();out=a.evidence.resolve()
if out==root or root in out.parents or out==source or source in out.parents:raise SystemExit('Evidence must be outside checkouts')
out.mkdir(mode=0o700,parents=True,exist_ok=False)
name='mycfc-schedule-c-'+uuid.uuid4().hex[:12];database='mycfc_schedule_compatibility_test';app=None;attack=None;stop=threading.Event();worker=None
prefix='39107900-0000-0000-0000-';ident=lambda n:prefix+str(n).zfill(12)
def run(cmd,log,**kw):
 with (out/log).open('w') as f:subprocess.run(cmd,cwd=source,stdout=f,stderr=subprocess.STDOUT,check=True,**kw)
def sql(query,role='acceptance',password='disposable-only'):
 return subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD='+password,name,'psql','-h','127.0.0.1','-U',role,'-d',database,'-v','ON_ERROR_STOP=1','-At'],input=query,text=True)
def port():
 with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def rows(table):return json.loads(sql("SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM "+table+' r'))
def history():return {t:rows(t) for t in ['training_plan_publications','training_prescriptions','training_session_outcomes']}
def source_manifest():
 return {n:hashlib.sha256((source/n).read_bytes()).hexdigest() for n in subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z'],cwd=source).decode().split('\0') if n and (source/n).is_file()}
try:
 (root/'node_modules').mkdir(exist_ok=True);deps=out/'dependencies';deps.mkdir()
 for n in ['package.json','package-lock.json']:shutil.copyfile(root/n,deps/n)
 run(['npm','ci','--ignore-scripts','--prefix',str(deps)],'dependencies.log')
 pgport,appport,attackport=port(),port(),port();base=f'http://127.0.0.1:{appport}'
 run(['docker','run','-d','--rm','--name',name,'-e','POSTGRES_USER=acceptance','-e','POSTGRES_PASSWORD=disposable-only','-e','POSTGRES_DB='+database,'-p',f'127.0.0.1:{pgport}:5432','postgres:16.9','-c','timezone=Europe/Lisbon'],'container.log')
 for _ in range(100):
  if subprocess.run(['docker','exec',name,'pg_isready','-U','acceptance','-d',database],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0:break
  time.sleep(.2)
 else:raise RuntimeError('isolated database not ready')
 run(['go','build','-o',str(out/'server'),'./cmd/server'],'build.log')
 env={k:v for k,v in os.environ.items() if k in ['PATH','HOME','TMPDIR','LANG','LC_ALL','TZ']}
 for line in (source/'.env.example').read_text().splitlines():
  if line and not line.startswith('#') and '=' in line:
   k,v=line.split('=',1);env[k]=v.strip('"')
 env.update(APP_ENV='test',APP_VERSION='dev',GIT_SHA='0000000000000000000000000000000000000000',PORT=str(appport),BASE_URL=base,AWS_EC2_METADATA_DISABLED='true',AWS_REGION='eu-west-1',SMTP_HOST='127.0.0.1',SMTP_PORT='1',S3_ENDPOINT='http://127.0.0.1:1',DATA_RIGHTS_CONTACT='privacy@example.test',APP_DB_USER='schedule_web',APP_DB_PASSWORD='synthetic-web',MIGRATION_DB_USER='schedule_migration',MIGRATION_DB_PASSWORD='synthetic-migration',TRAINING_SCHEDULE_ADMISSION_FILE='')
 for k in ['CONSENT_TERMS_URL','CONSENT_IMAGE_URL','CONSENT_MINOR_URL','PRIVACY_NOTICE_URL','COOKIE_NOTICE_URL','GALLERY_URL']:env[k]=env[k].replace('http://localhost:8080',base)
 dsn=lambda user,password:f'postgres://{user}:{password}@127.0.0.1:{pgport}/{database}?sslmode=disable'
 # Actual CLI roles and hardening; runtime never connects as owner or superuser.
 for command,user,password in [('bootstrap-db','acceptance','disposable-only'),('migrate','schedule_migration','synthetic-migration'),('harden-db','acceptance','disposable-only')]:run([str(out/'server'),command],command+'.log',env=dict(env,DATABASE_URL=dsn(user,password)))
 assert sql('SELECT count(*) FROM mycfc_meta.schema_migrations').strip()=='79'
 # Explicit test-only nullable contract and operator activation in this disposable database.
 sql('BEGIN;'+(source/'internal/db/testdata/training_schedule_p_contract.sql').read_text()+'COMMIT;',role='schedule_migration',password='synthetic-migration')
 sql("SELECT training_schedule_control.commit_floor(database_incarnation,state_epoch,gen_random_uuid(),repeat('a',64),2) FROM training_schedule_control.state WHERE singleton; SELECT training_schedule_control.set_enabled(database_incarnation,state_epoch,true) FROM training_schedule_control.state WHERE singleton;",role='schedule_migration',password='synthetic-migration')
 (out/'seed.log').write_text(sql((root/'e2e/training-schedule-compatibility-seed.sql').read_text()))
 assert sql('SELECT current_user','schedule_web','synthetic-web').strip()=='schedule_web'
 facts=json.loads(sql("SELECT json_build_object('day',(CURRENT_DATE+2)::text,'next_week',(date_trunc('week',CURRENT_DATE+2)::date+7)::text,'source_updated_at',(SELECT updated_at FROM training_plans WHERE id='"+ident(300)+"'),'week',(SELECT week_start FROM training_plans WHERE id='"+ident(300)+"'),'programme',(SELECT id FROM programmes WHERE code='Initiation'),'guardian_prescription',(SELECT id FROM training_prescriptions WHERE athlete_user_id='"+ident(17)+"' AND session_id='"+ident(401)+"'))"))
 facts['attack_origin']=f'http://127.0.0.1:{attackport}';(out/'fixture.json').write_text(json.dumps(facts));before=history();(out/'history-before.json').write_text(json.dumps(before,sort_keys=True,indent=2))
 manifest=source_manifest();(out/'source-manifest.json').write_text(json.dumps(manifest,sort_keys=True,indent=2))
 testfiles=['e2e/run-training-schedule-compatibility.py','e2e/training-schedule-compatibility.spec.mjs','e2e/training-schedule-compatibility-seed.sql','package-lock.json'];testhash={n:hashlib.sha256((root/n).read_bytes()).hexdigest() for n in testfiles};(out/'test-manifest.json').write_text(json.dumps(testhash,indent=2))
 env['DATABASE_URL']=dsn('schedule_web','synthetic-web')
 with (out/'app.log').open('w') as log:app=subprocess.Popen([str(out/'server')],cwd=source,env=env,stdout=log,stderr=subprocess.STDOUT)
 for _ in range(100):
  if app.poll() is not None:raise RuntimeError('application exited; see app.log')
  try:
   if urllib.request.urlopen(base+'/health/ready').status==200:break
  except Exception:time.sleep(.2)
 else:raise RuntimeError('actual app readiness failed')
 actions={
 'planning-before-denials':None,
 'planning-after-denials':None,
 'unknown-artifacts':"BEGIN; SET LOCAL session_replication_role=replica; INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT '"+ident(405)+"',id,'Unknown artifact fixture',(CURRENT_DATE+2+time '15:00') AT TIME ZONE 'Europe/Lisbon',(CURRENT_DATE+2+time '16:00') AT TIME ZONE 'Europe/Lisbon','"+ident(1)+"' FROM training_plans WHERE id='"+ident(300)+"'; INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,snapshot,snapshot_sha256) VALUES('"+ident(709)+"','"+ident(600)+"','"+ident(405)+"','"+ident(111)+"','"+ident(11)+"',jsonb_build_object('schema_version',999),repeat('e',64)); UPDATE training_routines SET snapshot=jsonb_build_object('schema_version',999) WHERE id='"+ident(800)+"'; COMMIT;",
 'revoke-coach':"UPDATE staff_grants SET revoked_at=clock_timestamp(),revoked_by_id='"+ident(1)+"',revoke_reason='Synthetic browser revocation' WHERE user_id='"+ident(3)+"' AND revoked_at IS NULL;",
 'revoke-guardian':"UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1 WHERE guardian_user_id='"+ident(16)+"' AND subject_user_id='"+ident(17)+"';"}
 def control():
  done=set()
  while not stop.wait(.05):
   for action,statement in actions.items():
    if action not in done and (out/(action+'-ready.json')).exists():
     try:
      if action.startswith('planning-'):
       planning={t:rows(t) for t in ['training_plans','training_sessions','training_session_segments','training_segment_blocks','training_copy_events','training_routines','training_plan_publications','training_prescriptions','training_session_outcomes']}
       if action=='planning-before-denials':(out/'planning-before-denials.json').write_text(json.dumps(planning,sort_keys=True,indent=2))
       else:
        (out/'planning-after-denials.json').write_text(json.dumps(planning,sort_keys=True,indent=2));assert planning==json.loads((out/'planning-before-denials.json').read_text()),'denied writes changed planning rows'
      else:sql(statement)
      receipt={'ok':True}
     except Exception as e:receipt={'ok':False,'error':str(e)}
     (out/(action+'-done.json')).write_text(json.dumps(receipt));done.add(action)
 worker=threading.Thread(target=control,daemon=True);worker.start()
 class Attack(http.server.BaseHTTPRequestHandler):
  def log_message(self,*args):pass
  def do_GET(self):
   body=('<!doctype html><html lang="en"><title>Cross origin fixture</title><form method="post" action="'+base+'/admin/treinos/estruturados/semanas/'+ident(300)+'/copiar"><input name="title" value="Cross-origin denied"><input name="week_start" value="'+facts['week']+'"><button>Submit cross-origin</button></form></html>').encode();self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.end_headers();self.wfile.write(body)
 attack=http.server.ThreadingHTTPServer(('127.0.0.1',attackport),Attack);threading.Thread(target=attack.serve_forever,daemon=True).start()
 command=['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}','-e','HOME=/tmp','-e','E2E_BASE_URL='+base,'-e','E2E_TRAINING_SCHEDULE_COMPATIBILITY=1','-e','E2E_SCHEDULE_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/browser.json','-v',str(root)+':/src:ro','-v',str(deps/'node_modules')+':/src/node_modules:ro','-v',str(out)+':/evidence','-w','/src','mcr.microsoft.com/playwright:v1.63.0-noble','node','node_modules/@playwright/test/cli.js','test','e2e/training-schedule-compatibility.spec.mjs','--workers=1','--retries=0','--output=/evidence/artifacts']
 run(command,'browser.log')
 cases=[]
 def walk(suite):
  for spec in suite.get('specs',[]):
   for c in spec['tests']:
    item={'title':spec['title'],'status':c['status'],'attempts':[r['status'] for r in c['results']]};cases.append(item);assert item['status']=='expected' and item['attempts']==['passed'],item
  for child in suite.get('suites',[]):walk(child)
 for suite in json.loads((out/'browser.json').read_text())['suites']:walk(suite)
 assert len(cases)==10,len(cases)
 after=history();assert after['training_plan_publications']==before['training_plan_publications'];assert [r for r in after['training_prescriptions'] if r['id']!=ident(709)]==before['training_prescriptions'],'original immutable history changed';assert [r for r in after['training_session_outcomes'] if r['session_id']!=ident(401)]==before['training_session_outcomes'],'prior outcome changed'
 final=json.loads(sql("SELECT json_build_object('planned_end_null',(SELECT ends_at IS NULL FROM training_sessions WHERE id='"+ident(401)+"'),'duration',(SELECT actual_duration_minutes FROM training_session_outcomes WHERE session_id='"+ident(401)+"' AND user_id='"+ident(11)+"'),'outcome_version',(SELECT version FROM training_session_outcomes WHERE session_id='"+ident(401)+"' AND user_id='"+ident(11)+"'),'plans',(SELECT count(*) FROM training_plans),'copy_events',(SELECT count(*) FROM training_copy_events),'segments',(SELECT count(*) FROM training_session_segments),'blocks',(SELECT count(*) FROM training_segment_blocks),'routines',(SELECT count(*) FROM training_routines),'outcomes',(SELECT count(*) FROM training_session_outcomes),'sessions',(SELECT count(*) FROM training_sessions),'publications',(SELECT count(*) FROM training_plan_publications),'prescriptions',(SELECT count(*) FROM training_prescriptions))"))
 assert final=={'planned_end_null':True,'duration':45,'outcome_version':2,'plans':2,'copy_events':0,'segments':1,'blocks':1,'routines':1,'outcomes':2,'sessions':6,'publications':1,'prescriptions':9},final
 assert manifest==source_manifest(),'application source changed during browser evidence'
 assert testhash=={n:hashlib.sha256((root/n).read_bytes()).hexdigest() for n in testfiles},'test source changed during run'
 (out/'history-after.json').write_text(json.dumps(history(),sort_keys=True,indent=2))
 metadata={'source':str(source),'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=source,text=True).strip(),'source_diff_sha256':hashlib.sha256(subprocess.check_output(['git','diff','HEAD'],cwd=source)).hexdigest(),'source_manifest_sha256':hashlib.sha256((out/'source-manifest.json').read_bytes()).hexdigest(),'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'inventory_count':79,'runtime_role':'schedule_web','schema':'C baseline + test-only P shape/floor; no P migration','local_exception':'unstamped APP_ENV=test; not production admission proof','postgres':'16.9','browser':'1.63.0','timezone':sql('SHOW timezone').strip()}
 (out/'summary.json').write_text(json.dumps({'cases':cases,'checks':final,'metadata':metadata,'immutable_history_equal':True,'source_and_test_unchanged':True},indent=2));print(json.dumps({'passed':len(cases),'checks':final,'evidence':str(out)},indent=2))
finally:
 # Preserve observed history even when an assertion stops the trial before final checks.
 try:
  observed=history();(out/'history-at-cleanup.json').write_text(json.dumps(observed,sort_keys=True,indent=2))
  if 'before' in globals():
   original_prescriptions=[r for r in observed['training_prescriptions'] if r['id']!=ident(709)]
   (out/'history-cleanup-check.json').write_text(json.dumps({'original_publications_equal':observed['training_plan_publications']==before['training_plan_publications'],'original_prescriptions_equal':original_prescriptions==before['training_prescriptions'],'prior_outcome_equal':[r for r in observed['training_session_outcomes'] if r['session_id']!=ident(401)]==before['training_session_outcomes']}))
 except Exception:
  pass
 stop.set()
 if worker:worker.join(timeout=2)
 if attack:attack.shutdown();attack.server_close()
 if app:
  app.terminate()
  try:app.wait(timeout=10)
  except subprocess.TimeoutExpired:app.kill();app.wait(timeout=5)
 subprocess.run(['docker','rm','-f',name+'-browser'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 (out/'cleanup.json').write_text(json.dumps({'app_stopped':app is None or app.poll() is not None,'owned_containers_removed':subprocess.run(['docker','inspect',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode!=0}))
