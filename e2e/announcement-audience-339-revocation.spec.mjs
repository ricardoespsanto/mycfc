import { test } from '@playwright/test';
import { evidence, surfaces, detail } from './announcement-audience-339-helpers.mjs';
test.skip(!evidence, 'Requires run-announcement-audience-339.py revocation phase');
test.use({ storageState: evidence ? `${evidence}/guardian-cookies.json` : undefined });
test('existing authenticated guardian loses revoked child audience on next requests while own OR audience remains', async ({ page }) => {
  await surfaces(page, [7,8]);
  await detail(page, 2, 404);
  await detail(page, 9, 404);
  await detail(page, 10, 404);
  await detail(page, 11, 404);
});
