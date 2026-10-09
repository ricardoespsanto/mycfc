import { expect, test } from '@playwright/test';
import { login, publish } from './crew-transition-helpers.mjs';
test.skip(process.env.E2E_CREW_TRANSITION !== '1', 'Disposable crew-transition runner required.');
test('administrator publishes real historical prescriptions before crew edits', async ({ page }) => {
  await login(page, 'admin');
  await publish(page, 300);
  await expect(page.getByText(/revisão 1/).first()).toBeVisible();
});
