#!/usr/bin/env python3
"""Offline tests: no host access or real credential values."""

import importlib.util
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('prepare-installed-compose-db-compat.py')
spec = importlib.util.spec_from_file_location('compose_compat', SCRIPT)
assert spec is not None and spec.loader is not None
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)

BASE = '''name: mycfc-production
services:
  postgres:
    volumes:
      - pgdata:/var/lib/postgresql/data
  app-green:
    environment:
      NOT_A_PRIVILEGED_PASSWORD: present
''' + compat.OLD_BOOTSTRAP + '\n' + compat.OLD_MIGRATE + '\nvolumes:\n  pgdata:\n'


class CompatibilityTest(unittest.TestCase):
    def test_only_two_release_services_are_changed(self):
        candidate = compat.render(BASE)
        self.assertEqual(candidate.count('\n      POSTGRES_PASSWORD:'), 1)
        self.assertEqual(candidate.count('\n      MIGRATION_DB_PASSWORD:'), 2)
        self.assertEqual(candidate.count('database-maintenance.env'), 2)
        before, bootstrap_and_after = candidate.split('  db-bootstrap:', 1)
        self.assertEqual(before, BASE.split('  db-bootstrap:', 1)[0])
        self.assertNotIn('POSTGRES_PASSWORD:', candidate.split('  migrate:', 1)[1])
        self.assertIn('app-green:', before)
        self.assertEqual(candidate.split('volumes:\n  pgdata:\n')[-1], '')
        self.assertEqual(bootstrap_and_after.count('profiles: [release]'), 2)

    def test_drift_or_already_patched_refuses(self):
        for source in (BASE.replace('bootstrap-db', 'bootstrap-unexpected'),
                       BASE.replace('name: mycfc-production', 'name: other'),
                       BASE.replace('pgdata:/var/lib/postgresql/data', 'other:/var/lib/postgresql/data'),
                       compat.render(BASE)):
            with self.subTest(source=source[:60]), self.assertRaises(ValueError):
                compat.render(source)

    def test_cli_preserves_source_and_exclusive_candidate(self):
        with tempfile.TemporaryDirectory() as directory:
            original = Path(directory) / 'installed.yaml'
            candidate = Path(directory) / 'candidate.yaml'
            original.write_text(BASE)
            command = [sys.executable, str(SCRIPT), str(original), str(candidate)]
            first = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(first.returncode, 0, first.stderr)
            self.assertEqual(original.read_text(), BASE)
            self.assertEqual(candidate.read_text(), compat.render(BASE))
            second = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(second.returncode, 0)
            self.assertEqual(candidate.read_text(), compat.render(BASE))
    def test_compose_render_with_synthetic_credentials(self):
        fixture = os.environ.get('MYCFC_INSTALLED_COMPOSE_FIXTURE')
        if not fixture or not shutil.which('docker'):
            self.skipTest('installed Compose fixture or local Docker Compose unavailable')
        source = Path(fixture).read_text()
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            candidate = base / 'compose.yaml'
            candidate.write_text(compat.render(source))
            maintenance = base / 'maintenance.env'
            maintenance.write_text('MEDIA_CLEANUP_DB_USER=synthetic_media\n'
                                   'MEDIA_CLEANUP_DB_PASSWORD=synthetic_only\n'
                                   'DATA_RETENTION_DB_USER=synthetic_retention\n'
                                   'DATA_RETENTION_DB_PASSWORD=synthetic_only\n')
            variables = set(re.findall(r'\$\{([A-Z][A-Z_0-9]*):\?', candidate.read_text()))
            env = base / 'compose.env'
            env.write_text(''.join(f'{name}=synthetic_only\n' for name in sorted(variables)) +
                           f'DATABASE_MAINTENANCE_ENV_FILE={maintenance}\n')
            result = subprocess.run(
                ['docker', 'compose', '--env-file', str(env), '--profile', 'release',
                 '-f', str(candidate), 'config', '--quiet'],
                capture_output=True, text=True,
                env={'PATH': os.environ.get('PATH', '')},
            )
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == '__main__':
    unittest.main()
