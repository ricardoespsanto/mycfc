import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080';
const requestedWorkers = process.env.PLAYWRIGHT_WORKERS || '2';
const ciWorkers = Number.parseInt(requestedWorkers, 10);
const trialReport = process.env.E2E_JSON_OUTPUT;

if (!/^[1-9][0-9]*$/.test(requestedWorkers) || !Number.isSafeInteger(ciWorkers)) {
  throw new Error('PLAYWRIGHT_WORKERS must be a positive integer');
}

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: process.env.CI ? ciWorkers : 1,
  timeout: 60_000,
  reporter: trialReport
    ? [['json', { outputFile: trialReport }], ['line']]
    : process.env.CI
      ? [['github'], ['line']]
      : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  ...(!process.env.E2E_BASE_URL && {
    webServer: {
      command: 'set -a; . ./.env; set +a; PORT="18080" BASE_URL="http://127.0.0.1:18080" S3_BUCKET_NAME="mycfc-local" S3_ENDPOINT="http://127.0.0.1:9000" S3_FORCE_PATH_STYLE="true" EMAIL_VERIFICATION_HMAC_KEY_B64="${EMAIL_VERIFICATION_HMAC_KEY_B64:-MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=}" SMTP_HOST="${SMTP_HOST:-127.0.0.1}" SMTP_PORT="${SMTP_PORT:-1025}" SMTP_FROM_ADDRESS="${SMTP_FROM_ADDRESS:-mycfc@example.test}" SMTP_TLS_MODE="${SMTP_TLS_MODE:-none}" CONSENT_TERMS_URL="http://127.0.0.1:18080/legal/termos-gerais" CONSENT_IMAGE_URL="http://127.0.0.1:18080/legal/uso-imagem" CONSENT_MINOR_URL="http://127.0.0.1:18080/legal/responsabilidade-menor" go run ./cmd/server',
      cwd: '.',
      url: 'http://127.0.0.1:18080/health/ready',
      reuseExistingServer: !process.env.CI,
      timeout: 600_000,
    },
  }),
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
