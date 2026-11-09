#!/usr/bin/env python3
"""Offline phase gates: no Docker, browser, application or database writes."""
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('rollover', Path(__file__).with_name('run-classification-natural-rollover.py'))
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)


class SafetyTests(unittest.TestCase):
    def test_finish_requires_exact_real_day_not_day_before_or_late(self):
        state = {'phase': 'awaiting-rollover', 'starts_on': '2026-11-10'}
        for day in ('2026-11-09', '2026-11-11'):
            with self.assertRaises(ValueError):
                r.require_finish(state, day)
        r.require_finish(state, '2026-11-10')

    def test_incomplete_and_repeat_finish_refused(self):
        for phase in ('starting', 'begin-failed', 'finishing', 'passed', 'cleaned'):
            with self.assertRaises(ValueError):
                r.require_finish({'phase': phase, 'starts_on': '2026-11-10'}, '2026-11-10')

    def test_early_finish_does_not_launch_or_write(self):
        state = {'phase': 'awaiting-rollover', 'starts_on': '2026-11-10'}
        with patch.object(r, 'owned_container'), patch.object(r, 'clock', return_value={'lisbon_date': '2026-11-09'}), \
             patch.object(r, 'application') as app, patch.object(r, 'browser') as browser, \
             patch.object(r, 'save') as save, patch.object(r, 'sql') as sql, self.assertRaises(ValueError):
            r.finish(Path('/unused'), state)
        app.assert_not_called()
        browser.assert_not_called()
        save.assert_not_called()
        sql.assert_not_called()

    def test_wrong_source_cannot_launch_or_publish(self):
        state = {'phase': 'awaiting-rollover', 'starts_on': '2026-11-10', 'app_sources': {'old': 'hash'}}
        with patch.object(r, 'owned_container'), patch.object(r, 'clock', return_value={'lisbon_date': '2026-11-10'}), \
             patch.object(r, 'source_manifest', return_value={'new': 'hash'}), \
             patch.object(r, 'browser') as browser, patch.object(r, 'save') as save, self.assertRaises(ValueError):
            r.finish(Path('/unused'), state)
        browser.assert_not_called()
        save.assert_not_called()

    def test_cleanup_rejects_arbitrary_resource_names(self):
        with patch.object(r.subprocess, 'check_output') as invoke, self.assertRaises(ValueError):
            r.owned_container({'container': 'production', 'token': 'anything'})
        invoke.assert_not_called()

    def test_cleanup_requires_exact_ownership_token(self):
        state = {'container': r.PREFIX + 'a' * 12, 'token': 'ours'}
        with patch.object(r.subprocess, 'check_output', return_value='[{"Config":{"Labels":{"mycfc.natural-rollover":"other"}}}]'), self.assertRaises(ValueError):
            r.owned_container(state)

    def test_changed_intervals_or_history_refuse_before_browser_and_save(self):
        state = {'phase': 'awaiting-rollover', 'starts_on': '2026-11-10',
                 'app_sources': {}, 'binary_sha256': 'hash',
                 'browser_files': {f: 'hash' for f in r.FILES}, 'intervals': []}
        for rows, history in (([{'changed': True}], {}), ([], {'changed': True})):
            with patch.object(r, 'owned_container'), \
                 patch.object(r, 'clock', return_value={'lisbon_date': '2026-11-10'}), \
                 patch.object(r, 'source_manifest', return_value={}), \
                 patch.object(r, 'digest', return_value='hash'), \
                 patch.object(r, 'intervals', return_value=rows), \
                 patch.object(r, 'retained', return_value=history), \
                 patch.object(Path, 'read_text', return_value='{}'), \
                 patch.object(r, 'application') as app, patch.object(r, 'browser') as browser, \
                 patch.object(r, 'save') as save, self.assertRaises(ValueError):
                r.finish(Path('/unused'), state)
            app.assert_not_called()
            browser.assert_not_called()
            save.assert_not_called()


if __name__ == '__main__':
    unittest.main()
