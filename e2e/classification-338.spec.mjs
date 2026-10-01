import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.E2E_CLASSIFICATION_338 !== '1', 'Requires the disposable e2e/classification-338-seed.sql database.');

const subject = '33800000-0000-0000-0000-000000000002';
const route = `/equipa/classificacao/${subject}`;
const password = 'correct horse 7'; // Existing synthetic E2E hash, never a live credential.

async function login(page, email) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function accessible(page, state) {
  expect(await page.locator('html').getAttribute('lang')).toBe('pt-PT');
  await expect(page.locator('main h1')).toHaveCount(1);
  const violations = (await new AxeBuilder({ page }).analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, `${state}: ${JSON.stringify(violations, null, 2)}`).toEqual([]);
}

async function submit(form) {
  const [response] = await Promise.all([
    form.page().waitForResponse((candidate) => candidate.request().method() === 'POST' && candidate.url().includes(route)),
    form.getByRole('button', { name: /Guardar/ }).click(),
  ]);
  return response.status();
}

test('administrator direct task, compact keyboard and zoom, invalid date and axe', async ({ page }) => {
  await login(page, 'classification-admin@example.test');
  await page.setViewportSize({ width: 320, height: 640 });
  const response = await page.goto(route);
  expect(response?.status()).toBe(200);
  expect(response?.headers()['content-security-policy']).toContain("script-src 'self'");
  await expect(page.locator('script:not([src])')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: /Alterar participação: Atleta classificação/ })).toBeVisible();
  await expect(page.locator('#task-heading')).toBeFocused();
  await expect(page.getByRole('region', { name: 'Histórico de participação' })).toContainText('Época de classificação E2E');
  await accessible(page, 'admin normal 320px');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await page.locator('#scope').focus();
  await page.keyboard.press('Tab');
  await expect(page.locator('#starts_on')).toBeFocused();
  // 640 physical pixels at 200% renders at approximately 320 CSS pixels.
  await page.setViewportSize({ width: 640, height: 640 });
  await page.evaluate(() => { document.documentElement.style.zoom = '200%'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(640);
  await accessible(page, 'admin 200% zoom at 640px');

  const dated = page.locator('form').first();
  await dated.locator('#scope').selectOption({ index: 1 });
  await dated.locator('#starts_on').fill('2020-01-01');
  expect(await submit(dated)).toBe(422);
  await expect(page.locator('#error-summary')).toBeVisible();
  await expect(page.locator('#error-summary')).toBeFocused();
  await expect(page.locator('#starts_on')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#starts_on')).toHaveValue('2020-01-01');
  await accessible(page, 'invalid dated participation');
});

test('administrator reason validation and real canoe craft cascade', async ({ page }) => {
  await login(page, 'classification-admin@example.test');
  await page.goto(route);
  const craft = page.locator('form').nth(2);
  await craft.locator('input[value="K1"]').check();
  await craft.locator('[name="reason"]').fill('Teste de correção de embarcação');
  expect(await submit(craft)).toBe(422);
  await expect(page.locator('#error-summary')).toBeVisible();
  await expect(page.locator('#error-summary')).toBeFocused();
  await expect(page.locator('#craft-reason')).toHaveAttribute('aria-invalid', 'true');
  await accessible(page, 'craft without Canoagem');

  const sport = page.locator('form').nth(1);
  await sport.locator('input[value="CANOEING"]').check();
  await sport.locator('[name="reason"]').fill('');
  // Native required validation must prevent a POST; enter a real reason for the write.
  await expect(sport.locator('[name="reason"]')).toHaveAttribute('required', '');
  await sport.locator('[name="reason"]').fill('Adicionar Canoagem para treino');
  expect(await submit(sport)).toBe(200);
  await expect(page.locator('#task-heading')).toBeFocused();
  await expect(page.getByRole('status')).toContainText('motivo registado');
  await expect(page.getByText('Modalidade: Canoagem')).toBeVisible();
  await page.locator('form').nth(2).locator('input[value="K1"]').check();
  await page.locator('form').nth(2).locator('[name="reason"]').fill('Corrigir classe K1');
  expect(await submit(page.locator('form').nth(2))).toBe(200);
  await expect(page.getByText(/Embarcação: K1/)).toBeVisible();
  await page.locator('form').nth(1).locator('input[value="CANOEING"]').uncheck();
  await page.locator('form').nth(1).locator('[name="reason"]').fill('Remover Canoagem e embarcações');
  expect(await submit(page.locator('form').nth(1))).toBe(200);
  await expect(page.getByText('Sem modalidades ou embarcações registadas.')).toBeVisible();
  await accessible(page, 'cascade after correction');
});

test('direct URL requires a session and cross-site POST is refused', async ({ page }) => {
  const anonymous = await page.goto(route);
  expect(anonymous?.status()).toBe(200); // Followed the redirect to the login page.
  await expect(page).toHaveURL(/\/login\?next=/);
  await login(page, 'classification-admin@example.test');
  // This app's CSRF library checks Fetch Metadata/Origin, not a form token.
  const response = await page.request.post(`${route}/selecoes`, {
    form: { kind: 'SPORT', reason: 'Pedido externo' },
    headers: { 'Sec-Fetch-Site': 'cross-site', Origin: 'https://example.org' },
    maxRedirects: 0,
  });
  expect(response.status()).toBe(403);
});

test('out-of-scope member and revoked coach cannot open direct URL; scoped coach can', async ({ browser }) => {
  for (const [email, expected] of [
    ['classification-outsider@example.test', 404],
    ['classification-revoked@example.test', 404],
    ['classification-coach@example.test', 200],
  ]) {
    const context = await browser.newContext();
    const page = await context.newPage();
    await login(page, email);
    expect((await page.goto(route))?.status(), email).toBe(expected);
    if (expected === 200) await expect(page.getByRole('heading', { name: /Alterar participação/ })).toBeVisible();
    await context.close();
  }
});
