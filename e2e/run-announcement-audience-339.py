#!/usr/bin/env python3
"""Only new acceptance specs, fresh disposable PG, isolated ports and pinned browser."""
import hashlib, json, os, pathlib, shutil, socket, subprocess, sys, tarfile, time, urllib.request, uuid
root = pathlib.Path(__file__).resolve().parents[1]
out = pathlib.Path(sys.argv[1]).resolve()
if out.is_relative_to(root): raise ValueError('Evidence must be external to checkout')
out.mkdir(parents=True, exist_ok=False, mode=0o700)
os.chmod(out,0o700)
source_repo = pathlib.Path(sys.argv[2]).resolve()
checkpoint = subprocess.check_output(['git','rev-parse',sys.argv[3]+'^{commit}'],cwd=source_repo,text=True).strip()
source = out/'source'
source.mkdir()
archive = subprocess.check_output(['git','archive',checkpoint],cwd=source_repo)
import io
with tarfile.open(fileobj=io.BytesIO(archive)) as tar: tar.extractall(source,filter='data')
name = 'mycfc-announcement-339-' + uuid.uuid4().hex[:12]
app = None
ports = []
postgres_started = False
owned_volumes = []
summary = None
def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]
def run(args, log, **kwargs):
    with (out / log).open('w') as f:
        subprocess.run(args, cwd=source, stdout=f, stderr=subprocess.STDOUT, check=True, **kwargs)
def sql(text):
    return subprocess.check_output(['docker','exec','-i',name,'psql','-U','acceptance','-d','mycfc','-v','ON_ERROR_STOP=1','-At'],input=text,text=True)
def browser(spec, phase):
    run(['docker','run','--rm','--name',name+'-browser','--network','host','--user',f'{os.getuid()}:{os.getgid()}',
         '-e','E2E_BASE_URL='+base,'-e','E2E_ANNOUNCEMENT_339_EVIDENCE=/evidence','-e','E2E_JSON_OUTPUT=/evidence/'+phase+'.json',
         '-v',str(out)+':/evidence','-w','/evidence/deps','mcr.microsoft.com/playwright:v1.63.0-noble',
         'npx','playwright','test',spec,'--workers=1','--retries=0','--output=/evidence/'+phase+'-artifacts'],phase+'.log')
def retained():
    return json.loads(sql("SELECT json_build_object('memberships',(SELECT json_agg(row_to_json(m) ORDER BY id) FROM user_memberships m),'legacy',(SELECT json_agg(row_to_json(m) ORDER BY membership_id,modality_id) FROM membership_modalities m),'announcements',(SELECT json_agg(row_to_json(a) ORDER BY id) FROM announcements a),'targets',(SELECT json_agg(row_to_json(t) ORDER BY announcement_id,target_type,target_id) FROM announcement_targets t));"))
def hashes(directory):
    return {str(p.relative_to(directory)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(directory.rglob('*')) if p.is_file()}
def capture_postgres_volume():
    # Inspect only the uniquely named container created by this run, before deletion.
    container = json.loads(subprocess.check_output(['docker','inspect',name],text=True))[0]
    recorded_id = (out/'container.log').read_text().strip()
    if container['Id'] != recorded_id or container['Name'] != '/'+name:
        raise RuntimeError('PostgreSQL container ownership mismatch')
    volumes = [m['Name'] for m in container['Mounts'] if m['Type']=='volume' and m['Destination']=='/var/lib/postgresql/data']
    if len(volumes) != 1: raise RuntimeError('Expected exactly one attributable PostgreSQL data volume')
    (out/'postgres-container.json').write_text(json.dumps(container,indent=2))
    (out/'volumes-before.json').write_text(subprocess.check_output(['docker','volume','inspect',*volumes],text=True))
    return volumes
try:
    ports = [free_port(), free_port()]
    while ports[1]==ports[0]: ports[1]=free_port()
    pgport, appport = ports
    (out/'checkpoint.txt').write_text(checkpoint+'\n')
    (out/'source-hashes.json').write_text(json.dumps(hashes(source),indent=2))
    (out/'test-hashes.json').write_text(json.dumps({str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted([*(root/'e2e').glob('*announcement-audience-339*'),*[root/f for f in ['package.json','package-lock.json','playwright.config.mjs']]]) if p.is_file()},indent=2))
    (out/'runtime-images.json').write_text(subprocess.check_output(['docker','image','inspect','postgres:16','alpine:3.24.2','mcr.microsoft.com/playwright:v1.63.0-noble'],text=True))
    (out/'resources.json').write_text(json.dumps({'name':name,'postgres_port':pgport,'app_port':appport,'published_address':'127.0.0.1'},indent=2))
    deps=out/'deps';deps.mkdir()
    (deps/'e2e').mkdir()
    for p in (root/'e2e').glob('announcement-audience-339*.mjs'): shutil.copy2(p,deps/'e2e'/p.name)
    shutil.copy2(root/'playwright.config.mjs',deps/'playwright.config.mjs')
    for filename in ['package.json','package-lock.json']: shutil.copy2(root/filename,deps/filename)
    run(['npm','ci','--prefix',str(deps),'--ignore-scripts','--no-audit','--no-fund'],'dependencies.log')
    run(['docker','run','-d','--name',name,'-e','POSTGRES_USER=acceptance','-e','POSTGRES_PASSWORD=disposable-only','-e','POSTGRES_DB=mycfc','-p',f'127.0.0.1:{pgport}:5432','-p',f'127.0.0.1:{appport}:8080','postgres:16','-c','timezone=Europe/Lisbon'],'container.log')
    postgres_started = True
    owned_volumes = capture_postgres_volume()
    for _ in range(60):
        if subprocess.run(['docker','exec','-e','PGPASSWORD=disposable-only',name,'psql','-w','-h','127.0.0.1','-U','acceptance','-d','mycfc','-Atqc','SELECT 1'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0: break
        time.sleep(1)
    else: raise RuntimeError('PG not ready')
    with (out/'build.log').open('w') as log: subprocess.run(['go','build','-o',str(out/'server'),'./cmd/server'],cwd=source,env={**os.environ,'CGO_ENABLED':'0'},stdout=log,stderr=subprocess.STDOUT,check=True)
    (out/'binary.sha256').write_text(hashlib.sha256((out/'server').read_bytes()).hexdigest()+'\n')
    env={}
    for line in (source/'.env.example').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            k,v=line.split('=',1);env[k]=v.strip('"')
    base=f'http://127.0.0.1:{appport}'
    env.update(APP_ENV='test',APP_VERSION='dev',GIT_SHA='0'*40,PORT='8080',BASE_URL=base,
               POSTGRES_USER='acceptance',POSTGRES_PASSWORD='disposable-only',
               APP_DB_USER='announcement_web',APP_DB_PASSWORD='synthetic-web',
               MIGRATION_DB_USER='announcement_migration',MIGRATION_DB_PASSWORD='synthetic-migration',
               MEDIA_CLEANUP_DB_USER='announcement_media',MEDIA_CLEANUP_DB_PASSWORD='synthetic-media',
               DATA_RETENTION_DB_USER='announcement_retention',DATA_RETENTION_DB_PASSWORD='synthetic-retention',
               AWS_EC2_METADATA_DISABLED='true',SMTP_HOST='127.0.0.1',SMTP_PORT='1',S3_ENDPOINT='http://127.0.0.1:1',
               DATA_RIGHTS_CONTACT='privacy@example.test',TRAINING_SCHEDULE_ADMISSION_FILE='')
    for k in ['CONSENT_TERMS_URL','CONSENT_IMAGE_URL','CONSENT_MINOR_URL','PRIVACY_NOTICE_URL','COOKIE_NOTICE_URL','GALLERY_URL']: env[k]=env[k].replace('http://localhost:8080',base)
    # Use the documented unstamped local/test fixture commands on this new owned DB.
    # Each command uses its proper identity; no baseline SQL or forged ledger install.
    command_env={k:v for k,v in os.environ.items() if k in ['PATH','HOME','TMPDIR','LANG','LC_ALL','TZ']}
    command_env.update(env)
    dsn=lambda user,password,port: f'postgres://{user}:{password}@127.0.0.1:{port}/mycfc?sslmode=disable'
    run([str(out/'server'),'precision-capabilities'],'compiled-capabilities.json',env=command_env)
    capabilities=json.loads((out/'compiled-capabilities.json').read_text())
    for command,user,password in [('bootstrap-db','acceptance','disposable-only'),
                                  ('migrate','announcement_migration','synthetic-migration'),
                                  ('prepare-local-recipient-floor','announcement_migration','synthetic-migration'),
                                  ('harden-db','acceptance','disposable-only')]:
        run([str(out/'server'),command],command+'.log',env=dict(command_env,DATABASE_URL=dsn(user,password,pgport)))
    (out/'seed.log').write_text(sql((root/'e2e/announcement-audience-339-seed.sql').read_text()))
    state=json.loads(sql("SELECT row_to_json(s) FROM training_schedule_control.state s WHERE singleton"))
    versions=json.loads(sql("SELECT json_agg(version ORDER BY version) FROM mycfc_meta.schema_migrations"))
    expected_versions=sorted(['reset-baseline-v1',*[p.stem for p in (source/'internal/db/migrations').glob('*.sql')]])
    assert versions==expected_versions, 'migration ledger differs from exact candidate inventory'
    assert state['required_reader']==capabilities['reader_generation'] and state['schema_contract']=='schedule-p1'
    assert state['partial_v2_enabled'] is True and state['floor_commit_id'] and state['floor_manifest_sha256']
    (out/'prepared-state.json').write_text(json.dumps({'schedule':state,'ledger_versions':versions,'runtime_role':'announcement_web'},indent=2))
    runtime_env=dict(command_env,DATABASE_URL=dsn('announcement_web','synthetic-web',pgport))
    run([str(out/'server'),'precision-status'],'runtime-precision-status.json',env=runtime_env)
    runtime_status=json.loads((out/'runtime-precision-status.json').read_text())
    assert runtime_status['database_incarnation']==state['database_incarnation']
    assert runtime_status['required_reader']==state['required_reader'] and runtime_status['floor_commit_id']==state['floor_commit_id']
    roles=json.loads(sql("SELECT json_agg(row_to_json(r)) FROM (SELECT rolname,rolsuper,rolbypassrls FROM pg_roles WHERE rolname='announcement_web') r"))
    assert len(roles)==1 and not roles[0]['rolsuper'] and not roles[0]['rolbypassrls']
    (out/'runtime-role.json').write_text(json.dumps(roles,indent=2))
    env['DATABASE_URL']=dsn('announcement_web','synthetic-web',5432)
    env_file=out/'app.env'
    env_file.write_text('\n'.join(k+'='+v for k,v in env.items())+'\n')
    env_file.chmod(0o600)
    run(['docker','run','-d','--name',name+'-app','--network','container:'+name,'--env-file',str(env_file),'-v',str(out/'server')+':/server:ro','alpine:3.24.2','/server'],'app-container.log')
    app=True
    for _ in range(60):
        try:
            if urllib.request.urlopen(base+'/health/ready').status==200:break
        except Exception: time.sleep(1)
    else: raise RuntimeError('app not ready')
    run(['docker','exec',name+'-app','/server','precision-status'],'container-runtime-precision-status.json')
    assert json.loads((out/'container-runtime-precision-status.json').read_text())==runtime_status, 'serving-container runtime binding changed'
    receipt_query="SELECT row_to_json(d) FROM announcement_deliveries d WHERE user_id='33933900-0000-0000-0000-000000000010' AND announcement_id='33933900-0000-0000-0000-000000000201';"
    original_receipt=sql(receipt_query)
    before=retained()
    (out/'retained-before.json').write_text(json.dumps(before,indent=2))
    browser('e2e/announcement-audience-339.spec.mjs','audience')
    assert retained()==before, 'UI corrections changed historical membership or target identities'
    receipt=sql("SELECT row_to_json(d) FROM announcement_deliveries d WHERE user_id='33933900-0000-0000-0000-000000000010' AND announcement_id='33933900-0000-0000-0000-000000000201';")
    assert receipt==original_receipt, 'historical receipt timestamps changed'
    assert json.loads(receipt)['read_at'], 'detail receipt lost after hide/re-add'
    (out/'preserved-receipt.json').write_text(receipt)
    assert int(sql("SELECT count(*) FROM person_canoe_craft_classes WHERE user_id='33933900-0000-0000-0000-000000000010';"))==1
    sql("UPDATE guardian_authority_relationships SET state='EXPIRED',version=version+1,conflict=false,conflict_actor_ref=NULL,updated_at=now() WHERE guardian_user_id='33933900-0000-0000-0000-000000000012';")
    browser('e2e/announcement-audience-339-revocation.spec.mjs','revocation')
    assert retained()==before, 'revocation changed historical identities'
    assert sql("SELECT row_to_json(d) FROM announcement_deliveries d WHERE user_id='33933900-0000-0000-0000-000000000010' AND announcement_id='33933900-0000-0000-0000-000000000201';")==receipt
    (out/'retained-after.json').write_text(json.dumps(retained(),indent=2))
    cases=[]
    def walk(s):
        for spec in s.get('specs',[]):
            for t in spec['tests']: cases.append({'title':spec['title'],'status':t['status'],'attempts':[r['status'] for r in t['results']]})
        for child in s.get('suites',[]):walk(child)
    for phase in ['audience','revocation']:walk(json.loads((out/(phase+'.json')).read_text()))
    assert len(cases)==4 and all(c['status']=='expected' and c['attempts']==['passed'] for c in cases),cases
    summary={'cases':cases,'historical_records_equal':True,'receipt_preserved':True,'checkpoint':checkpoint,'binary_sha256':hashlib.sha256((out/'server').read_bytes()).hexdigest(),'runtime_role':'announcement_web'}
finally:
    if app:
        with (out/'app.log').open('w') as log: subprocess.run(['docker','logs',name+'-app'],stdout=log,stderr=subprocess.STDOUT)
    # Retry attribution before removal if startup succeeded but its initial inspect failed.
    attribution_error = None
    if postgres_started and not owned_volumes:
        try: owned_volumes = capture_postgres_volume()
        except Exception as error: attribution_error = str(error)
    # -v removes this container's anonymous volume, never unrelated or guessed volumes.
    with (out/'cleanup-removal.log').open('w') as log:
        subprocess.run(['docker','rm','-fv',name+'-app',name+'-browser',name],stdout=log,stderr=subprocess.STDOUT)
    remaining=subprocess.check_output(['docker','ps','-a','--filter','name='+name,'--format','{{.Names}}'],text=True).strip()
    existing_volumes=set(subprocess.check_output(['docker','volume','ls','--format','{{.Name}}'],text=True).splitlines())
    cleanup={'app_stopped':name+'-app' not in remaining,'containers':remaining,
             'postgres_volume_names':owned_volumes,'volume_attribution_error':attribution_error,
             'volume_identity_recorded':not postgres_started or bool(owned_volumes),
             'volumes_gone':all(volume not in existing_volumes for volume in owned_volumes)}
    for port in ports:
        with socket.socket() as s:cleanup[str(port)+'_closed']=s.connect_ex(('127.0.0.1',port))!=0
    cleanup['success']=(cleanup['app_stopped'] and not remaining and cleanup['volume_identity_recorded']
                        and cleanup['volumes_gone'] and not attribution_error
                        and all(cleanup[str(port)+'_closed'] for port in ports))
    (out/'cleanup.json').write_text(json.dumps(cleanup,indent=2))
    print('CLEANUP',json.dumps(cleanup))
    if not cleanup['success']: raise RuntimeError('Isolated resource cleanup failed; see cleanup.json; no passing summary written')
# This is deliberately after finally: neither browser nor cleanup failure can publish a pass.
if summary is None: raise RuntimeError('No completed browser verification; no passing summary written')
summary['cleanup']=cleanup
(out/'summary.json').write_text(json.dumps(summary,indent=2))
print(json.dumps(summary,indent=2))
