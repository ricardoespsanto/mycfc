import { test, expect } from '@playwright/test';
import { evidence, login, correct, surfaces, detail, accessible, saveGuardian } from './announcement-audience-339-helpers.mjs';
test.skip(!evidence, 'Requires run-announcement-audience-339.py disposable fixture');

test('real full-set corrections update list, panel, detail, pagination and preserve read receipt through cascade', async ({ page, browser }, testInfo) => {
  await login(page, 'athlete');
  await surfaces(page, [8]); // stale membership K1 grants nothing; programme OR remains meaningful.
  await detail(page, 1, 404);
  const staff = await browser.newPage();
  await login(staff, 'admin');
  await correct(staff, 10, 'SPORT', ['CANOEING', 'SUP']);
  await correct(staff, 10, 'CRAFT', ['K1', 'K2', 'K4', 'C1', 'C2', 'C4']);
  await surfaces(page, [1,2,3,4,5,6,7,8], 7);
  // Listing and bell do not mark read; only the direct detail does.
  await surfaces(page, [1,2,3,4,5,6,7,8], 7);
  await detail(page, 2, 200);
  await surfaces(page, [1,2,3,4,5,6,7,8], 6);
  await correct(staff, 10, 'CRAFT', ['K2']);
  await surfaces(page, [2,7,8], 2);
  await detail(page, 1, 404);
  await correct(staff, 10, 'CRAFT', ['K1','K2']);
  await surfaces(page, [1,2,7,8], 2);
  await correct(staff, 10, 'SPORT', ['SUP']); // Canoagem removal cascades all craft.
  await surfaces(page, [7,8]);
  await detail(page, 2, 404);
  await correct(staff, 10, 'SPORT', []);
  await surfaces(page, [8]);
  await detail(page, 7, 404);
  await correct(staff, 10, 'SPORT', ['CANOEING']);
  await surfaces(page, [8]); // crafts must not resurrect when Canoagem is re-added.
  await correct(staff, 10, 'CRAFT', ['K1']);
  await surfaces(page, [1,8], 1);
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto('/announcements');
  await accessible(page);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  const trigger = page.locator('[data-announcement-trigger]:visible').first();
  await trigger.focus();
  expect(await trigger.evaluate(el => getComputedStyle(el).outlineStyle)).not.toBe('none');
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-announcement-close]')).toBeFocused();
  await accessible(page);
  await page.screenshot({ path: testInfo.outputPath('320px-bell.png'), fullPage: true });
  await page.keyboard.press('Escape');
  await expect(trigger).toBeFocused();
  await staff.close();
});

test('guardian same-child positive, own SUP and inactive child cannot satisfy guardian SUP restriction', async ({ page }) => {
  await login(page, 'guardian');
  await surfaces(page, [2,7,8,9,11]);
  await detail(page, 10, 404);
  await detail(page, 11, 200);
  await surfaces(page, [2,7,8,9,11], 4);
  await saveGuardian(page);
});

test('sport selection without current participation grants no targeted surface', async ({ page }) => {
  await login(page, 'unanchored');
  await surfaces(page, []);
  await detail(page, 7, 404);
  await detail(page, 8, 404);
});
