#!/usr/bin/env python3
"""Offline tests with synthetic local files only; never print credentials."""

import importlib.util
from pathlib import Path
import re
import stat
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('stage-fresh-db-maintenance-env.py')
spec = importlib.util.spec_from_file_location('maintenance_stage', SCRIPT)
assert spec is not None and spec.loader is not None
stage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stage)


class MaintenanceStageTest(unittest.TestCase):
    def test_exact_four_keys_fresh_random_values(self):
        first, second = stage.content(), stage.content()
        self.assertNotEqual(first, second)
        lines = first.splitlines()
        self.assertEqual([line.split('=', 1)[0] for line in lines], [
            'MEDIA_CLEANUP_DB_USER', 'MEDIA_CLEANUP_DB_PASSWORD',
            'DATA_RETENTION_DB_USER', 'DATA_RETENTION_DB_PASSWORD'])
        self.assertEqual(lines[0].split('=', 1)[1], stage.MEDIA_ROLE)
        self.assertEqual(lines[2].split('=', 1)[1], stage.RETENTION_ROLE)
        for line in (lines[1], lines[3]):
            password = line.split('=', 1)[1]
            self.assertTrue(re.fullmatch(r'[A-Za-z0-9_-]{60,}', password))
        self.assertNotEqual(lines[1], lines[3])

    def test_reject_bad_role_names(self):
        for role in ('postgres;bad', '', 'UPPER', stage.RETENTION_ROLE):
            with self.subTest(role=role), self.assertRaises(ValueError):
                stage.content(role, stage.RETENTION_ROLE)

    def test_exclusive_regular_file_0600_and_no_replace(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'maintenance.env'
            payload = stage.content()
            stage.create(target, payload)
            self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
            self.assertTrue(target.is_file())
            self.assertEqual(target.read_text(), payload)
            with self.assertRaises(FileExistsError):
                stage.create(target, stage.content())
            self.assertEqual(target.read_text(), payload)
            link = Path(directory) / 'symlink.env'
            link.symlink_to(target)
            with self.assertRaises(FileExistsError):
                stage.create(link, stage.content())
            self.assertEqual(target.read_text(), payload)

    def test_cli_requires_explicit_create_flag(self):
        # Main's missing flag returns before any path or credential operation.
        old = sys.argv
        try:
            sys.argv = [str(SCRIPT)]
            self.assertEqual(stage.main(), 2)
        finally:
            sys.argv = old


if __name__ == '__main__':
    unittest.main()
