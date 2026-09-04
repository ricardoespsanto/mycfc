import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

const script = path.join(import.meta.dirname, 'summarize-playwright-json.mjs');

test('summarizes browser duration and exact test outcomes', () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'mycfc-playwright-summary-'));
  const report = path.join(directory, 'report.json');
  try {
    fs.writeFileSync(report, JSON.stringify({
      config: { projects: [{ retries: 0 }] },
      stats: { duration: 63_412.6, expected: 20, unexpected: 0, flaky: 0, skipped: 31 },
    }));
    const result = execFileSync(process.execPath, [script, report], { encoding: 'utf8' });
    assert.equal(result, '63413\t51\t20\t0\t0\t31\t0\n');
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test('rejects incomplete statistics', () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'mycfc-playwright-summary-'));
  const report = path.join(directory, 'report.json');
  try {
    fs.writeFileSync(report, JSON.stringify({ config: { projects: [{ retries: 0 }] }, stats: { duration: 1, expected: 1 } }));
    const result = spawnSync(process.execPath, [script, report], { encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /invalid Playwright unexpected statistic/);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
