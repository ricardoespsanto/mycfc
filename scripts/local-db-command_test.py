#!/usr/bin/env python3
"""Secret-safe, data-only local credential routing tests; no DB access."""
import importlib.util
import os
import json
import shutil
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

spec = importlib.util.spec_from_file_location('localdb', Path(__file__).with_name('local-db-command.py'))
local = importlib.util.module_from_spec(spec)
spec.loader.exec_module(local)


class LocalIdentityTest(unittest.TestCase):
    def setUp(self):
        self.env = {'APP_ENV': 'local', 'DATABASE_URL': 'postgres://old:old@127.0.0.1:5432/mycfc?sslmode=disable',
                    'APP_DB_USER': 'app', 'APP_DB_PASSWORD': 'p@ss:/word',
                    'MIGRATION_DB_USER': 'migration', 'MIGRATION_DB_PASSWORD': 'migration-password',
                    'POSTGRES_USER': 'bootstrap', 'POSTGRES_PASSWORD': 'bootstrap-password'}

    def test_typed_identity_preserves_endpoint_tls_and_owned_database(self):
        for kind, user in [('runtime', 'app'), ('migration', 'migration'), ('bootstrap', 'bootstrap')]:
            with self.subTest(kind=kind):
                env = local.identity(self.env, kind)
                result = urlsplit(env['DATABASE_URL'])
                self.assertEqual((result.hostname, result.port, result.path, result.query),
                                 ('127.0.0.1', 5432, '/mycfc', 'sslmode=disable'))
                self.assertEqual(result.username, user)
        self.assertIn('p%40ss%3A%2Fword', local.identity(self.env, 'runtime')['DATABASE_URL'])
        self.assertEqual(self.env['DATABASE_URL'], 'postgres://old:old@127.0.0.1:5432/mycfc?sslmode=disable')

    def test_remote_ambiguous_missing_and_unknown_databases_refuse(self):
        for changed in [dict(DATABASE_URL='postgres://a:b@production/mycfc'),
                        dict(DATABASE_URL='postgres://a:b@localhost/foreign'),
                        dict(DB_USER='other'), dict(APP_DB_PASSWORD='')]:
            with self.subTest(changed=list(changed)), self.assertRaises(local.Refused):
                local.identity(dict(self.env, **changed), 'runtime')

    def test_pgx_query_identity_and_startup_overrides_refuse(self):
        for query in ['host=remote.invalid', 'user=bootstrap', 'dbname=foreign', 'database=foreign',
                      'password=other', 'port=5433', 'service=production', 'hostaddr=10.0.0.1',
                      'options=-c+role%3Dpostgres', 'role=postgres', 'search_path=attacker',
                      'session_authorization=postgres', '%68ost=remote.invalid',
                      'sslmode=disable&%73slmode=require', 'sslmode=disable&SSLMode=require',
                      '%2568ost=remote.invalid', 'passfile=/protected', 'unknown=value']:
            with self.subTest(query=query), self.assertRaises(local.Refused):
                local.identity(dict(self.env, DATABASE_URL='postgres://app:p@localhost/mycfc?'+query), 'runtime')
        raw = 'postgres://app:p@127.0.0.1:55432/mycfc_ui_review?sslmode=verify-full&sslrootcert=%2Ffixture%2Fca.pem&application_name=local-proof&connect_timeout=5'
        target = urlsplit(local.identity(dict(self.env, DATABASE_URL=raw), 'migration')['DATABASE_URL'])
        self.assertEqual((target.hostname, target.port, target.path, target.query),
                         ('127.0.0.1', 55432, '/mycfc_ui_review', urlsplit(raw).query))

    def test_data_parser_does_not_execute_or_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'.env'
            content = 'APP_ENV=local\nDATABASE_URL="postgres://u:p@localhost/mycfc"\nLABEL=$(false);`false`\n'
            path.write_text(content)
            result = local.environment(path, {'COMPOSE_PROJECT_NAME': 'owned'})
            self.assertEqual(result['LABEL'], '$(false);`false`')
            self.assertEqual(result['COMPOSE_PROJECT_NAME'], 'owned')
            self.assertEqual(path.read_text(), content)
            for invalid in ['APP_ENV=production\n', 'APP_ENV=local\nsource secrets\n',
                            'APP_ENV=local\nTRAINING_SCHEDULE_ADMISSION_FILE=/protected\n',
                            'APP_ENV=local\nCOMPOSE_PROJECT_NAME=foreign\n',
                            'APP_ENV=local\nDOCKER_HOST=tcp://remote.invalid\n']:
                path.write_text(invalid)
                with self.assertRaises(local.Refused):
                    local.environment(path, {})

    def test_env_owner_mode_symlink_size_and_inherited_context_refuse(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'.env'
            path.write_text('APP_ENV=local\nDATABASE_URL=postgres://u:p@localhost/mycfc\n')
            for inherited in [{'APP_ENV': 'production'}, {'TRAINING_SCHEDULE_ADMISSION_FILE': '/protected'},
                              {'DATABASE_URL': 'postgres://u:p@remote.invalid/mycfc'}]:
                with self.subTest(inherited=list(inherited)), self.assertRaises(local.Refused):
                    local.environment(path, inherited)
            path.chmod(0o666)
            with self.assertRaises(local.Refused):
                local.environment(path, {})
            path.chmod(0o600)
            with patch.object(local.os, 'getuid', return_value=os.getuid()+1), self.assertRaises(local.Refused):
                local.environment(path, {})
            link = Path(tmp)/'link'
            link.symlink_to(path)
            with self.assertRaises(OSError):
                local.environment(link, {})
            path.write_bytes(b' ' * 65537)
            with self.assertRaises(local.Refused):
                local.environment(path, {})

    @unittest.skipUnless(shutil.which('docker'), 'Docker Compose CLI unavailable; no daemon is used')
    def test_actual_compose_config_pins_target_and_uses_discrete_credentials_for_both_services(self):
        repository = Path(__file__).resolve().parent.parent
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            shutil.copyfile(repository/'compose.yaml', root/'compose.yaml')
            password = 'p@remote.invalid:5432/foreign?host=x'
            user = 'user@host:5432/foreign?x=y'
            source = (repository/'.env.example').read_text()
            source = source.replace('@127.0.0.1:5432/mycfc?', '@127.0.0.1:55432/mycfc?')
            source = source.replace('POSTGRES_PORT=5432\n', '')
            source = source.replace('APP_DB_PASSWORD=mycfc_local_app_only', 'APP_DB_PASSWORD='+password)
            source = source.replace('APP_DB_USER=mycfc_app', 'APP_DB_USER='+user)
            path = root/'.env'
            path.write_text(source)
            env = local.environment(path, {'PATH': os.environ['PATH'], 'COMPOSE_PROJECT_NAME': 'chosen-project'})
            # The helper refuses targeting keys. Explicit --env-file/-f/-p also
            # stops Compose's own implicit cwd .env from redirecting this call.
            path.write_text(source+'\nCOMPOSE_FILE=foreign.yaml\nDOCKER_HOST=tcp://remote.invalid:2376\nCOMPOSE_PROJECT_NAME=foreign\n')
            with self.assertRaises(local.Refused):
                local.environment(path, {})
            result = subprocess.run(['docker', 'compose', '--env-file', '/dev/null', '--project-name', 'chosen-project',
                                     '-f', str(root/'compose.yaml'), '--profile', 'e2e', '--profile', 'ui-review',
                                     'config', '--format', 'json'], cwd=root,
                                    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15, check=False)
            self.assertEqual(result.returncode, 0, 'offline synthetic Compose config failed')
            config = json.loads(result.stdout)
            self.assertEqual(config['name'], 'chosen-project')
            self.assertEqual(config['services']['postgres']['ports'][0]['published'], '55432')
            self.assertEqual(config['services']['postgres']['ports'][0]['host_ip'], '127.0.0.1')
            for service, database in [('e2e-app', 'mycfc_test'), ('ui-review-app', 'mycfc_ui_review')]:
                values = config['services'][service]['environment']
                self.assertEqual(values['DATABASE_URL'], '')
                self.assertEqual((values['DB_HOST'], values['DB_PORT'], values['DB_NAME']), ('postgres', '5432', database))
                self.assertTrue(values['DB_USER'] == user and values['DB_PASSWORD'] == password)

    def test_local_published_port_derives_or_refuses_explicit_mismatch(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'.env'
            base = 'APP_ENV=local\nDATABASE_URL=postgres://u:p@127.0.0.1:55432/mycfc\n'
            path.write_text(base)
            self.assertEqual(local.environment(path, {})['POSTGRES_PORT'], '55432')
            for bad in ['5432', '0', '65536', '055432', 'not-a-port']:
                path.write_text(base+'POSTGRES_PORT='+bad+'\n')
                with self.subTest(port=bad), self.assertRaises(local.Refused):
                    local.environment(path, {})


if __name__ == '__main__':
    unittest.main(verbosity=2)
