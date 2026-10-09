import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.E2E_CLASSIFICATION_338 !== '1', 'Requires a fresh disposable classification-338 fixture.');

const route = '/equipa/escaloes';
const task = '/equipa/classificacao/33800000-0000-0000-0000-000000000002';
const password = 'correct horse 7'; // Synthetic repository fixture only.

async function login(page, email) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function accessible(page, label) {
  expect(await page.locator('html').getAttribute('lang')).toBe('pt-PT');
  await expect(page.locator('main h1')).toHaveCount(1);
  const violations = (await new AxeBuilder({ page }).analyze()).violations
    .filter(({ impact }) => ['serious', 'critical'].includes(impact));
  expect(violations, `${label}: ${JSON.stringify(violations, null, 2)}`).toEqual([]);
}

async function submit(page) {
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => candidate.request().method() === 'POST' && candidate.url().includes(route)),
    page.getByRole('button', { name: 'Criar escalão' }).click(),
  ]);
  return response.status();
}

test('admin discovers and creates a definition; 320px, zoom, keyboard, axe and duplicate recovery', async ({ page }) => {
  await login(page, 'classification-admin@example.test');
  await page.setViewportSize({ width: 320, height: 640 });
  await page.goto(task);
  const link = page.getByRole('link', { name: 'Gerir escalões da época' });
  await expect(link).toBeVisible();
  await link.click();
  await expect(page).toHaveURL(new RegExp(`${route}$`));
  await expect(page.locator('#heading')).toBeFocused();
  await accessible(page, 'empty definitions 320px');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await page.locator('#scope').focus();
  await page.keyboard.press('Tab');
  await expect(page.locator('#code')).toBeFocused();
  await page.setViewportSize({ width: 640, height: 640 });
  await page.evaluate(() => { document.documentElement.style.zoom = '200%'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(640);
  await accessible(page, 'definitions simulated 200% zoom');
  await page.locator('#scope').selectOption(await page.locator('#scope option').filter({ hasText: 'Competição' }).getAttribute('value'));
  await page.locator('#code').fill('E2E335');
  await page.locator('#name').fill('Jovens teste');
  await page.locator('#birth_date_from').fill('2009-01-01');
  await page.locator('#birth_date_to').fill('2010-12-31');
  expect(await submit(page)).toBe(303);
  await expect(page.getByText(/Jovens teste \(E2E335\)/)).toBeVisible();
  await accessible(page, 'created definition');
  await page.locator('#scope').selectOption(await page.locator('#scope option').filter({ hasText: 'Competição' }).getAttribute('value'));
  await page.locator('#code').fill('E2E335');
  await page.locator('#name').fill('Jovens teste');
  await page.locator('#birth_date_from').fill('2009-01-01');
  await page.locator('#birth_date_to').fill('2010-12-31');
  expect(await submit(page)).toBe(409);
  await expect(page.locator('#errors')).toBeFocused();
  await expect(page.locator('#errors a')).toHaveAttribute('href', '#code');
  await expect(page.locator('#code')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#name')).toHaveValue('Jovens teste');
  await expect(page.locator('#birth_date_from')).toHaveValue('2009-01-01');
  await expect(page.locator('#birth_date_to')).toHaveValue('2010-12-31');
  await accessible(page, 'duplicate error');
  await page.locator('#errors a').click();
  await expect(page.locator('#code')).toBeFocused();
});

test('invalid inclusive date interval retains fields, focuses error, axe', async ({ page }) => {
  await login(page, 'classification-admin@example.test');
  await page.goto(route);
  await page.locator('#scope').selectOption(await page.locator('#scope option').filter({ hasText: 'Iniciação' }).getAttribute('value'));
  await page.locator('#code').fill('E2E335BAD');
  await page.locator('#name').fill('Datas trocadas');
  await page.locator('#birth_date_from').fill('2012-01-01');
  await page.locator('#birth_date_to').fill('2011-01-01');
  expect(await submit(page)).toBe(422);
  await expect(page.locator('#errors')).toBeFocused();
  await expect(page.locator('#birth_date_from')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#scope')).toHaveValue(/:/);
  await expect(page.locator('#name')).toHaveValue('Datas trocadas');
  await accessible(page, 'invalid interval');
});

test('anonymous is redirected and cross-origin definition POST is rejected', async ({ page }) => {
  await page.goto(route);
  await expect(page).toHaveURL(/\/login\?next=/);
  await login(page, 'classification-admin@example.test');
  const response = await page.request.post(route, {
    form: { scope: 'forged', code: 'FORGED', name: 'Falso' },
    headers: { 'Sec-Fetch-Site': 'cross-site', Origin: 'https://example.org' },
    maxRedirects: 0,
  });
  expect(response.status()).toBe(403);
});

test('coach only sees authorised programme; leisure-only and outsider have no link or direct access', async ({ browser }) => {
  for (const [email, status, options] of [
    ['definitions-coach@example.test', 200, 1],
    ['classification-coach@example.test', 404, 0],
    ['classification-revoked@example.test', 404, 0],
    ['classification-outsider@example.test', 404, 0],
  ]) {
    const context = await browser.newContext();
    const page = await context.newPage();
    await login(page, email);
    if (email === 'classification-coach@example.test') {
      await page.goto(task);
      await expect(page.getByRole('link', { name: 'Gerir escalões da época' })).toHaveCount(0);
    }
    expect((await page.goto(route))?.status(), email).toBe(status);
    if (status === 200) {
      await expect(page.locator('#scope option')).toHaveCount(options + 1);
      await expect(page.locator('#scope')).toContainText('Competição');
      await expect(page.locator('#scope')).not.toContainText('Iniciação');
      await accessible(page, 'coach scoped definition');
    }
    await context.close();
  }
});
