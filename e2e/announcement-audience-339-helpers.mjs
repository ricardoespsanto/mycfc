import { expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
export const evidence = process.env.E2E_ANNOUNCEMENT_339_EVIDENCE;
export const id = n => `33933900-0000-0000-0000-${String(n).padStart(12, '0')}`;
export const notice = n => id(200 + n);
export async function login(page, actor) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(`announcement-${actor}@example.test`);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}
export async function accessible(page) {
  const violations = (await new AxeBuilder({ page }).analyze()).violations.filter(v => ['serious', 'critical'].includes(v.impact));
  expect(violations, JSON.stringify(violations)).toEqual([]);
}
export async function correct(page, subject, kind, codes) {
  await page.goto(`/equipa/classificacao/${id(subject)}`);
  const form = page.locator('form').filter({ has: page.locator(`input[name="kind"][value="${kind}"]`) });
  for (const input of await form.locator('input[name="codes"]').all()) {
    await input.setChecked(codes.includes(await input.getAttribute('value')));
  }
  await form.locator('[name="reason"]').fill('Correção sintética de audiência 339');
  const [response] = await Promise.all([
    page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/selecoes')),
    form.getByRole('button').click(),
  ]);
  expect(response.status()).toBe(200);
  await page.waitForLoadState('networkidle');
  await expect(page.locator('#error-summary')).toHaveCount(0);
  await expect(page.getByRole('status')).toContainText('Seleções desportivas atualizadas');
}
// Every call navigates: the panel intentionally caches only for the current page lifetime.
export async function surfaces(page, expected, unread = expected.length) {
  const actual = [];
  await page.goto('/announcements');
  while (true) {
    actual.push(...await page.locator('main .record-item a[href^="/announcements/"]').evaluateAll(els => els.map(e => e.getAttribute('href').split('/').at(-1))));
    const next = page.getByRole('link', { name: 'Seguinte', exact: true });
    if (!await next.count()) break;
    await next.click();
  }
  expect(actual).toEqual(expected.map(notice));
  expect(new Set(actual).size).toBe(actual.length);
  await page.goto('/announcements');
  const trigger = page.locator('[data-announcement-trigger]:visible').first();
  await trigger.focus();
  await page.keyboard.press('Enter');
  const panel = page.locator('[data-announcement-panel]');
  await expect(panel.locator('[data-announcement-count]')).toHaveAttribute('data-announcement-count', String(unread));
  await expect(panel.locator('li a')).toHaveCount(Math.min(6, expected.length));
  expect(await panel.locator('li a').evaluateAll(els => els.map(e => e.getAttribute('href').split('/').at(-1)))).toEqual(expected.slice(0, 6).map(notice));
  await expect(panel.locator('[data-announcement-close]')).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(trigger).toBeFocused();
  const response = await page.request.get('/announcements/panel');
  expect(response.status()).toBe(200);
  expect(await response.text()).toContain(`data-announcement-count="${unread}"`);
}
export async function detail(page, n, status) {
  const response = await page.goto(`/announcements/${notice(n)}`);
  expect(response.status()).toBe(status);
  if (status === 200) await expect(page.getByRole('heading', { level: 1 })).toContainText(`Aviso ${String(n).padStart(2, '0')}`);
}
export async function saveGuardian(page) {
  await page.context().storageState({ path: `${evidence}/guardian-cookies.json` });
  fs.chmodSync(`${evidence}/guardian-cookies.json`, 0o600);
}
