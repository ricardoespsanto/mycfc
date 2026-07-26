import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:8080';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  reporter: process.env.CI ? [['github'], ['line']] : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  ...(!process.env.E2E_BASE_URL && {
    webServer: {
      command: 'set -a; . ./.env; set +a; CONSENT_TERMS_URL="${CONSENT_TERMS_URL:-http://127.0.0.1:8080/legal/termos-gerais}" CONSENT_IMAGE_URL="${CONSENT_IMAGE_URL:-http://127.0.0.1:8080/legal/uso-imagem}" CONSENT_MINOR_URL="${CONSENT_MINOR_URL:-http://127.0.0.1:8080/legal/responsabilidade-menor}" go run ./cmd/server',
      cwd: '.',
      url: 'http://127.0.0.1:8080/health/ready',
      reuseExistingServer: !process.env.CI,
      timeout: 600_000,
    },
  }),
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
