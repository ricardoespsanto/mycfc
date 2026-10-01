"""Tests for the local-only rehearsal's safety boundary, not a release API."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location('rehearsal', pathlib.Path(__file__).with_name('rehearse-disposable-release.py'))
assert spec is not None and spec.loader is not None
module = importlib.util.module_from_spec(spec)
loader = spec.loader


class LocalBoundaryTest(unittest.TestCase):
    def test_rejects_non_local_container(self):
        loader.exec_module(module)
        with self.assertRaises(ValueError):
            module.validate_local_target('mycfc-production-postgres-1', 'mycfc')

    def test_rejects_non_rehearsal_database(self):
        loader.exec_module(module)
        with self.assertRaises(ValueError):
            module.validate_local_target('mycfc-disposable-local-abcd-pg', 'mycfc')

    def test_allows_owned_rehearsal_target(self):
        loader.exec_module(module)
        module.validate_local_target('mycfc-disposable-local-abcd-pg', 'disposable_rehearsal')


if __name__ == '__main__':
    unittest.main()
