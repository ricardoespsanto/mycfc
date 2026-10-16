import base64, http.server, json, os, pathlib, re, secrets, subprocess, threading, time, urllib.parse, uuid
root=pathlib.Path(__file__).resolve().parents[1]
scratch=pathlib.Path(os.environ.get('TMPDIR',str(pathlib.Path.home()/'.cache/mycfc-tests')))
scratch.mkdir(mode=0o700,parents=True,exist_ok=True)
name='mycfc-fresh-test-'+uuid.uuid4().hex[:9]
password=secrets.token_urlsafe(22)
app=secrets.token_urlsafe(24); migration=secrets.token_urlsafe(24); media=secrets.token_urlsafe(24); retention=secrets.token_urlsafe(24)
def postgres_url(user,secret,port,db='mycfc'):
 return 'postgres://'+urllib.parse.quote(user,safe='')+':'+urllib.parse.quote(secret,safe='')+'@127.0.0.1:'+str(port)+'/'+urllib.parse.quote(db,safe='')+'?sslmode=disable'
# Assert both credential components round-trip without exposing their values.
reserved_user='account:@/?#% &'
reserved_secret='password:@/?#% &'
reserved_url=urllib.parse.urlsplit(postgres_url(reserved_user,reserved_secret,5432,'fresh_contract'))
assert reserved_url.username is not None and reserved_url.password is not None
assert urllib.parse.unquote(reserved_url.username)==reserved_user
assert urllib.parse.unquote(reserved_url.password)==reserved_secret
assert reserved_url.hostname=='127.0.0.1' and reserved_url.port==5432
assert reserved_url.path=='/fresh_contract' and reserved_url.query=='sslmode=disable'
def run(args,env=None,check=True,**kw):
 p=subprocess.run(args,cwd=root,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,**kw)
 if check and p.returncode: raise RuntimeError(f'command {args[:2]} exit={p.returncode}: '+p.stderr[:500])
 return p
try:
 run(['docker','run','-d','--rm','--name',name,'-e','POSTGRES_PASSWORD='+password,'-P','postgres:16.9'])
 for attempt in range(40):
  ready=run(['docker','exec',name,'pg_isready','-U','postgres'],check=False)
  if ready.returncode==0:break
  time.sleep(.5)
 else:raise RuntimeError('disposable PG did not start')
 port=run(['docker','port',name,'5432/tcp']).stdout.strip().split(':')[-1]
 run(['docker','exec',name,'createdb','-U','postgres','mycfc'])
 url=lambda user,secret,db='mycfc': postgres_url(user,secret,port,db)
 # The stamped integration test creates its own isolated PG container named
 # mycfc; never initialize the release harness's mycfc before this sequence.
 testenv=dict(os.environ,TEST_DATABASE_URL=url('postgres',password))
 testenv.pop('TEST_FRESH_INIT_URL',None)
 print(run(['go','test','-tags=integration','./cmd/server','-run','^TestExplicitFreshInitRealPostgres$','-count=1'],env=testenv).stdout.strip())
 candidate='fedc71658fde1a193685332c19d954c51dcb10e6'
 version='v2.0.1'
 server=scratch/'mycfc-fresh-server'
 ld='-X github.com/cfcoimbra/mycfc/internal/releasecontract.Version='+version+' -X github.com/cfcoimbra/mycfc/internal/releasecontract.Candidate='+candidate
 run(['go','build','-ldflags',ld,'-o',str(server),'./cmd/server'])

 base=dict(os.environ,APP_ENV='local',APP_VERSION=version,GIT_SHA=candidate,DATABASE_URL=url('postgres',password),APP_DB_USER='mycfc_app',APP_DB_PASSWORD=app,MIGRATION_DB_USER='mycfc_migrate',MIGRATION_DB_PASSWORD=migration,MEDIA_CLEANUP_DB_USER='mycfc_media_cleanup_login',MEDIA_CLEANUP_DB_PASSWORD=media,DATA_RETENTION_DB_USER='mycfc_data_retention_login',DATA_RETENTION_DB_PASSWORD=retention)
 def execute(argv,env=base,success=True,phrase=None):
  p=run(argv,env=env,check=False)
  if success != (p.returncode==0) or (phrase and phrase not in (p.stdout+p.stderr)):
   raise RuntimeError(f'wrong outcome {argv[1:]} exit={p.returncode}; '+(p.stdout+p.stderr)[:350])
  return p
 execute([str(server),'fresh-init-db'],success=False,phrase='--confirm-empty')
 missing=base.copy();missing.pop('MEDIA_CLEANUP_DB_PASSWORD')
 execute([str(server),'fresh-init-db','--confirm-empty'],env=missing,success=False,phrase='must be configured together')
 assert run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT to_regclass('public.users') IS NULL"]).stdout.strip()=='t'
 execute([str(server),'bootstrap-db'],success=False,phrase='disposable')
 execute([str(server),'fresh-init-db','--confirm-empty'])
 digest=execute([str(server),'schema-digest']).stdout.strip()
 ledger=run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT count(*) FROM mycfc_meta.schema_migrations"]).stdout.strip()
 assert ledger=='77',ledger
 assert digest=='41eca3f0ca37f6e6ba5279fe1d931ff589de72b2a8586a77e31445e8a8b29ed7',digest
 execute([str(server),'fresh-init-db','--confirm-empty'],success=False,phrase='requires the named empty database')
 execute([str(server),'bootstrap-db'])
 migr=base.copy();migr['DATABASE_URL']=url('mycfc_migrate',migration)
 execute([str(server),'migrate'],env=migr)
 run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-c',"INSERT INTO mycfc_meta.schema_migrations(version) VALUES('tampered-test-marker')"])
 execute([str(server),'bootstrap-db'],success=False,phrase='fresh initialization receipt inspection failed')
 execute([str(server),'migrate'],env=migr,success=False,phrase='fresh initialization receipt inspection failed')
 run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-c',"DELETE FROM mycfc_meta.schema_migrations WHERE version='tampered-test-marker'"])
 execute([str(server),'migrate'],env=migr)
 roles=run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT rolname||':'||rolcanlogin FROM pg_roles WHERE rolname IN ('mycfc_migrate','mycfc_disposable_web_20261001','mycfc_media_cleanup_login','mycfc_data_retention_login') ORDER BY rolname"]).stdout.strip().splitlines()
 assert len(roles)==4 and all(x.endswith(':true') for x in roles),roles
 for statement in ["SET ROLE mycfc_migrate; DELETE FROM mycfc_fresh_init.receipt", "SET ROLE mycfc_disposable_web_20261001; SELECT * FROM mycfc_fresh_init.receipt"]:
  assert run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-v','ON_ERROR_STOP=1','-c',statement],check=False).returncode!=0,statement
 assert run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT count(*) FROM mycfc_fresh_init.receipt"]).stdout.strip()=='1'
 assert run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT to_regclass('mycfc_disposable_release.completion') IS NULL"]).stdout.strip()=='t'
 # Existing operator CLI; only synthetic test identity and ephemeral 0600 password file.
 adminpass='Aa8!'+secrets.token_urlsafe(24);pw=scratch/(name+'-admin-password');pw.write_text(adminpass);pw.chmod(0o600)
 admin_env=base.copy();admin_env.update(DATABASE_URL=url('mycfc_disposable_web_20261001',app),MYCFC_ADMIN_PASSWORD_FILE=str(pw),BASE_URL='http://localhost:8080',CSRF_AUTH_KEY_B64=base64.b64encode(secrets.token_bytes(32)).decode(),EMAIL_VERIFICATION_HMAC_KEY_B64=base64.b64encode(secrets.token_bytes(32)).decode(),SMTP_HOST='localhost',SMTP_PORT='1025',SMTP_FROM_ADDRESS='mycfc@example.test',SMTP_FROM_NAME='MyCFCoimbra',SMTP_TLS_MODE='none',AWS_REGION='eu-west-1',S3_BUCKET_NAME='mycfc-local',S3_ENDPOINT='http://localhost:9000',S3_FORCE_PATH_STYLE='true',GALLERY_URL='https://example.invalid/gallery',CONSENT_TERMS_VERSION='dev-v1',CONSENT_TERMS_SHA256='0'*64,CONSENT_TERMS_URL='http://localhost:8080/legal/termos',CONSENT_IMAGE_VERSION='dev-v1',CONSENT_IMAGE_SHA256='0'*64,CONSENT_IMAGE_URL='http://localhost:8080/legal/imagem',CONSENT_MINOR_VERSION='dev-v1',CONSENT_MINOR_SHA256='0'*64,CONSENT_MINOR_URL='http://localhost:8080/legal/menores',PRIVACY_NOTICE_URL='http://localhost:8080/legal/privacidade',COOKIE_NOTICE_URL='http://localhost:8080/legal/cookies',DATA_RIGHTS_CONTACT='privacy@example.test')
 image='mycfc-fresh-admin-test:'+name
 run(['docker','build','--quiet','--build-arg','RELEASE_VERSION='+version,'--build-arg','GIT_SHA='+candidate,'-t',image,'.'])
 config_source=(root/'internal/config/config.go').read_text()
 parameter_names=dict(re.findall(r'"([A-Z0-9_]+)":\s+productionParameterPrefix \+ "(/[^"]+)"',config_source))
 fixture=(root/'internal/config/config_test.go').read_text().split('func validProductionParameters()',1)[1].split('func validProductionSecrets()',1)[0]
 fixture_values=dict(re.findall(r'"([A-Z0-9_]+)":\s+"([^"]*)"',fixture))
 for field,letter in [('CONSENT_TERMS_SHA256','a'),('CONSENT_IMAGE_SHA256','b'),('CONSENT_MINOR_SHA256','c')]:fixture_values[field]=letter*64
 fixture_values['DB_HOST']='127.0.0.1';fixture_values['DB_PORT']=port;fixture_values['DB_NAME']='mycfc';fixture_values['DB_USER']='mycfc_app';fixture_values['MIGRATION_DB_USER']='mycfc_migrate'
 for field in ['POLAR_CLIENT_ID','ACTIVITY_CREDENTIAL_KEY_ID']:fixture_values[field]=''
 assert set(parameter_names)-set(fixture_values)==set(),set(parameter_names)-set(fixture_values)
 stub_secret={'APP_DB_PASSWORD':app,'CSRF_AUTH_KEY_B64':admin_env['CSRF_AUTH_KEY_B64'],'EMAIL_VERIFICATION_HMAC_KEY_B64':admin_env['EMAIL_VERIFICATION_HMAC_KEY_B64'],'TURNSTILE_SECRET_KEY':'test-turnstile-secret','SMTP_USERNAME':'test-smtp-user','SMTP_PASSWORD':'test-smtp-secret'}
 class StubAWS(http.server.BaseHTTPRequestHandler):
  def do_POST(self):
   data=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))))
   target=self.headers.get('X-Amz-Target','')
   if target.endswith('.GetParameters'):
    result={'Parameters':[{'Name':key,'Type':'String','Value':fixture_values[next(field for field,path in parameter_names.items() if '/mycfc/production'+path==key)]} for key in data['Names']]}
   elif target.endswith('.GetSecretValue'):result={'SecretString':json.dumps(stub_secret)}
   else:raise AssertionError('unexpected AWS API target '+target)
   body=json.dumps(result).encode();self.send_response(200);self.send_header('Content-Type','application/x-amz-json-1.1');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
  def log_message(self,*args):pass
 aws_stub=http.server.ThreadingHTTPServer(('127.0.0.1',0),StubAWS)
 aws_thread=threading.Thread(target=aws_stub.serve_forever,daemon=True);aws_thread.start()
 endpoint='http://127.0.0.1:'+str(aws_stub.server_port)
 admin_env.update(APP_ENV='production',DATABASE_URL=url('postgres',password),AWS_ACCESS_KEY_ID='test-only',AWS_SECRET_ACCESS_KEY='test-only',AWS_ENDPOINT_URL_SSM=endpoint,AWS_ENDPOINT_URL_SECRETS_MANAGER=endpoint)
 keys=('DATABASE_URL','APP_ENV','APP_VERSION','GIT_SHA','AWS_REGION','AWS_ACCESS_KEY_ID','AWS_SECRET_ACCESS_KEY','AWS_ENDPOINT_URL_SSM','AWS_ENDPOINT_URL_SECRETS_MANAGER')
 image_env=[item for key in keys for item in ('-e',key)]
 image_command=['docker','run','--rm','--network','host','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user',str(os.getuid())+':'+str(os.getgid()),'--entrypoint','/app/admin',*image_env,'-e','MYCFC_ADMIN_PASSWORD_FILE=/run/secrets/admin-password','-v',str(pw)+':/run/secrets/admin-password:ro',image]
 result=execute([*image_command,'create','--email','test-admin@example.test','--name','Test Administrator','--date-of-birth','1980-01-01'],env=admin_env)
 assert 'administrator created' in result.stdout,result.stdout
 wrong_release=admin_env.copy();wrong_release['GIT_SHA']='b'*40
 execute([*image_command,'create','--email','wrong-release@example.test','--name','Wrong Release','--date-of-birth','1980-01-01'],env=wrong_release,success=False,phrase='disposable web release identity mismatch')
 assert 'administrator already exists' in execute([*image_command,'create','--email','test-admin@example.test','--name','Test Administrator','--date-of-birth','1980-01-01'],env=admin_env).stdout
 without_password=[*image_command[:-1],'-e','MYCFC_ADMIN_PASSWORD_FILE=',image]
 execute([*without_password,'create','--email','no-password@example.test','--name','No Password','--date-of-birth','1980-01-01'],env=admin_env,success=False,phrase='refusing password input from a non-terminal')
 login_env=dict(os.environ,TEST_FRESH_ADMIN_URL=url('mycfc_disposable_web_20261001',app),TEST_FRESH_ADMIN_EMAIL='test-admin@example.test',TEST_FRESH_ADMIN_PASSWORD_FILE=str(pw))
 print(run(['go','test','-tags=integration','./cmd/admin','-run','^TestFreshImageAdminLogin$','-count=1'],env=login_env).stdout.strip())
 row=run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT count(*) FROM users u JOIN user_platform_roles upr ON upr.user_id=u.id JOIN platform_roles r ON r.id=upr.role_id WHERE u.email='test-admin@example.test' AND r.code='ADMIN' AND u.password_hash LIKE '$2%' AND u.is_active"]).stdout.strip()
 assert row=='1',row
 # Existing DB negative control; no relation/ledger/identity mutation.
 before=run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT count(*) FROM users"]).stdout.strip()
 execute([str(server),'fresh-init-db','--confirm-empty'],success=False)
 assert run(['docker','exec',name,'psql','-U','postgres','-d','mycfc','-At','-c',"SELECT count(*) FROM users"]).stdout.strip()==before
 print('PASS real disposable PG: explicit flag, missing credentials, empty/predecessor refusal, 77 ledger/schema, role login, stamped bootstrap+migrate, production-mode image admin via remote DB role (poisoned DATABASE_URL ignored), release mismatch/no-password denials, HTTP login, existing DB no-change')
 print('inventory_digest='+digest+' ledger_count='+ledger+' admin_count='+row)
finally:
 if 'pw' in locals():pw.unlink(missing_ok=True)
 if 'aws_stub' in locals():aws_stub.shutdown();aws_stub.server_close()
 subprocess.run(['docker','stop',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 if 'image' in locals():subprocess.run(['docker','image','rm',image],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
