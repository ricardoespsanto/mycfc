import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const email = `e2e-${Date.now()}@example.test`;
const guardianEmail = `e2e-guardian-${Date.now()}@example.test`;
const leisureEmail = `e2e-leisure-${Date.now()}@example.test`;
const adminEmail = 'e2e-admin@example.test';
const password = 'correct horse 7';
const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:8080';
const validPNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2h38AAAAASUVORK5CYII=', 'base64');

async function expectNoSeriousAxeViolations(page) {
  const results = await new AxeBuilder({ page }).analyze();
  const serious = results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

async function expectNoHorizontalOverflow(page) {
  const overflowing = await page.locator('*').evaluateAll((elements) => elements
    .filter((element) => element.scrollWidth > element.clientWidth + 1)
    .map((element) => ({ tag: element.tagName, className: element.className, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth })));
  expect(overflowing).toEqual([]);
}

test.describe('authentication', () => {
  test.describe.configure({ mode: 'serial' });

  test('renders an accessible public landing page with working calls to action', async ({ page, browser }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'A promover a canoagem, dentro e fora de água.' })).toBeVisible();
    await expect(page.getByAltText('Instalações do Clube Fluvial de Coimbra junto ao rio')).toBeVisible();
    const register = page.getByRole('link', { name: 'Criar conta MyCFC' });
    await expect(register).toHaveAttribute('href', '/registo');
    await expect(page.getByRole('link', { name: 'Iniciar sessão' }).first()).toHaveAttribute('href', '/login');
    await expect(page.getByRole('link', { name: 'Ver as novidades do clube' })).toHaveAttribute('href', 'https://cfcoimbra.com/noticias/');
    await expect(page.locator('a[href="tel:+351912626410"]').first()).toHaveText('+351 912 626 410');
    await register.focus();
    await expect(register).toBeFocused();
    await expectNoSeriousAxeViolations(page);
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/');
    await expect(noJavaScriptPage.getByRole('heading', { name: 'A promover a canoagem, dentro e fora de água.' })).toBeVisible();
    await context.close();
  });

  test('registers a member, reaches the account dashboard, and has no serious accessibility violations', async ({ page }) => {
    await page.goto('/registo');
    await expectNoSeriousAxeViolations(page);
    const name = page.getByLabel('Nome');
    const emailField = page.getByLabel('Correio eletrónico');
    await name.focus();
    await page.keyboard.press('Tab');
    await expect(emailField).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(name).toBeFocused();
    await name.fill('Pessoa de teste');
    await emailField.fill(email);
    await page.getByLabel('Data de nascimento').fill('1990-01-01');
    await page.getByLabel('Palavra-passe', { exact: true }).fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.getByRole('button', { name: 'Criar conta' }).click();

    await expect(page).toHaveURL('/dashboard/member');
    await expect(page.getByRole('heading', { name: 'Área de membro' })).toBeVisible();
    await expectNoSeriousAxeViolations(page);

    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);

    const logout = page.getByRole('button', { name: 'Terminar sessão' });
    await logout.focus();
    await expect(logout).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL('/login');
  });

  test('logs in and navigates with JavaScript disabled', async ({ browser }) => {
    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const page = await context.newPage();
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(email);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/dashboard/member');

    const idempotencyKey = await page.locator('input[name="idempotency_key"]').inputValue();
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByLabel('Fotografia (opcional)').setInputFiles({ name: 'avaria.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/dashboard/member');
    const success = page.getByText(/Avaria reportada\. Referência:/);
    await expect(success).toBeVisible();
    const firstReference = await success.textContent();

    await page.locator('input[name="idempotency_key"]').evaluate((input, value) => { input.value = value; }, idempotencyKey);
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/dashboard/member');
    await expect(page.getByText(/Avaria reportada\. Referência:/)).toHaveText(firstReference ?? '');
    await context.close();
  });

  test('creates a dependent with JavaScript disabled', async ({ page, browser }) => {
    await page.goto('/registo');
    await page.getByLabel('Nome').fill('Guardião de teste');
    await page.getByLabel('Correio eletrónico').fill(guardianEmail);
    await page.getByLabel('Data de nascimento').fill('1980-01-01');
    await page.getByLabel('Palavra-passe', { exact: true }).fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.getByRole('button', { name: 'Criar conta' }).click();
	await expect(page).toHaveURL('/dashboard/member');
    await expectNoSeriousAxeViolations(page);
    await page.getByRole('button', { name: 'Terminar sessão' }).click();

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/login');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill(guardianEmail);
    await noJavaScriptPage.getByLabel('Palavra-passe').fill(password);
    await noJavaScriptPage.getByRole('button', { name: 'Iniciar sessão' }).click();
	await noJavaScriptPage.getByRole('link', { name: 'Menores a cargo' }).click();
	await expect(noJavaScriptPage).toHaveURL('/dashboard/guardian');

    await noJavaScriptPage.getByLabel('Nome').fill('Menor de teste');
    await noJavaScriptPage.getByLabel('Data de nascimento').fill('2014-01-01');
    await noJavaScriptPage.getByLabel(/Aceito a responsabilidade pelo menor a cargo/).check();
    await noJavaScriptPage.getByRole('button', { name: 'Adicionar menor' }).click();

    await expect(noJavaScriptPage).toHaveURL('/dashboard/guardian');
    await expect(noJavaScriptPage.getByText('Menor a cargo adicionado.')).toBeVisible();
    await expect(noJavaScriptPage.getByText('Menor de teste')).toBeVisible();
    await noJavaScriptPage.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(noJavaScriptPage);
    await context.close();
  });

  test('logs in as an administrator and views the fleet', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('heading', { name: 'Frota', exact: true })).toBeVisible();
    await expect(page.getByLabel('Equipamento')).toBeVisible();
    await expectNoSeriousAxeViolations(page);
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
  });
});
