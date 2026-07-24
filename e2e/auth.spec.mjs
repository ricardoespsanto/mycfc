import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const email = `e2e-${Date.now()}@example.test`;
const password = 'correct horse 7';
const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:8080';

async function expectNoSeriousAxeViolations(page) {
  const results = await new AxeBuilder({ page }).analyze();
  const serious = results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

test.describe('authentication', () => {
  test.describe.configure({ mode: 'serial' });

  test('registers a competitor, reaches the dashboard, and has no serious accessibility violations', async ({ page }) => {
    await page.goto('/registo');
    await page.getByLabel('Nome').fill('Pessoa de teste');
    await page.getByLabel('Correio eletrónico').fill(email);
    await page.getByLabel('Data de nascimento').fill('1990-01-01');
    await page.getByLabel('Tipo de conta').selectOption('Competitor');
    await page.getByLabel('Categoria de equipa (apenas competidor)').selectOption('Iniciante');
    await page.getByLabel('Palavra-passe', { exact: true }).fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.getByRole('button', { name: 'Criar conta' }).click();

    await expect(page).toHaveURL('/dashboard/competitor');
    await expect(page.getByRole('heading', { name: 'Painel de competidor' })).toBeVisible();
    await expectNoSeriousAxeViolations(page);

    await page.setViewportSize({ width: 320, height: 720 });
    const overflowing = await page.locator('*').evaluateAll((elements) => elements
      .filter((element) => element.scrollWidth > element.clientWidth + 1)
      .map((element) => ({ tag: element.tagName, className: element.className, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth })));
    expect(overflowing).toEqual([]);

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await expect(page).toHaveURL('/login');
  });

  test('logs in and navigates with JavaScript disabled', async ({ browser }) => {
    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const page = await context.newPage();
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(email);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/dashboard/competitor');
    await expect(page.getByText('Consulte os calendários públicos:')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Treinos' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Competições' })).toBeVisible();
    await expect(page.locator('a[aria-current="page"]')).toHaveText('Competidor');
    await context.close();
  });
});
