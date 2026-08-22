import fs from 'node:fs';

const reportPath = process.argv[2];
if (!reportPath) {
  throw new Error('usage: node scripts/summarize-playwright-json.mjs <report.json>');
}

const report = JSON.parse(fs.readFileSync(reportPath, 'utf8'));
const { duration, expected, unexpected, flaky, skipped } = report.stats || {};
for (const [name, value] of Object.entries({ duration, expected, unexpected, flaky, skipped })) {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) {
    throw new Error(`invalid Playwright ${name} statistic`);
  }
}

const discovered = expected + unexpected + flaky + skipped;
const retryValues = new Set((report.config?.projects || []).map((project) => project.retries));
if (retryValues.size !== 1 || !Number.isInteger([...retryValues][0]) || [...retryValues][0] < 0) {
  throw new Error('invalid Playwright retry configuration');
}
const retries = [...retryValues][0];
process.stdout.write(`${Math.round(duration)}\t${discovered}\t${expected}\t${unexpected}\t${flaky}\t${skipped}\t${retries}\n`);
