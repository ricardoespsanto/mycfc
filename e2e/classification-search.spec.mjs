import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.E2E_CLASSIFICATION_338 !== '1', 'Requires disposable classification and search fixtures.');

async function login(page) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('classification-coach@example.test');
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function accessible(page) {
  const violations = (await new AxeBuilder({ page }).analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

const today = () => new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Lisbon' }).format(new Date());

test('coach discovers classification, keyboard name search, explicit selection and first save on mobile', async ({ page }, testInfo) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await login(page);
  await page.getByRole('link', { name: 'Classificação', exact: true }).click();
  await page.setViewportSize({ width: 320, height: 640 });
  await expect(page.getByRole('link', { name: /^Selecionar / })).toHaveCount(0);
  await expect(page.getByLabel('Nome da pessoa')).toBeFocused();
  await accessible(page);
  await page.getByLabel('Nome da pessoa').fill('Pessoa sem participação');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Pesquisar', exact: true })).toBeFocused();
  expect(await page.getByRole('button', { name: 'Pesquisar', exact: true }).evaluate(el => getComputedStyle(el).outlineStyle)).not.toBe('none');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/equipa\/classificacao\?q=/);
  const result = page.getByRole('link', { name: 'Selecionar Pessoa sem participação', exact: true });
  await expect(result).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('mobile-search.png'), fullPage: true });
  await expect(page.getByRole('heading', { name: /Alterar participação/ })).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await accessible(page);
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  await expect(result).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#task-heading')).toBeFocused();
  await expect(page.getByRole('heading', { name: 'Alterar participação: Pessoa sem participação', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Corrigir modalidades e embarcações' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Alterar pessoa' })).toHaveAttribute('href', /q=Pessoa/);
  await accessible(page);
  const option = page.locator('#scope option').filter({ hasText: 'Lazer' });
  await page.locator('#scope').selectOption(await option.getAttribute('value'));
  await page.getByLabel('Data de início', { exact: true }).fill(today());
  await page.getByRole('button', { name: 'Guardar participação', exact: true }).click();
  await expect(page.locator('#preview-heading')).toBeFocused();
  await page.getByRole('button', { name: 'Confirmar participação', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Guardada: Leisure');
  await expect(page.getByRole('region', { name: 'Histórico de participação' })).toContainText('Lazer');
  await expect(page.locator('#task-heading')).toBeFocused();
  await accessible(page);
  await page.screenshot({ path: testInfo.outputPath('mobile-saved.png'), fullPage: true });
  expect(errors).toEqual([]);
  await page.getByRole('link', { name: 'Alterar pessoa' }).click();
  await expect(page.getByLabel('Nome da pessoa')).toHaveValue('Pessoa sem participação');
  await page.getByLabel('Nome da pessoa').fill('Pessoa inexistente E2E');
  await page.getByRole('button', { name: 'Pesquisar', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('Não foram encontradas pessoas disponíveis para classificação com este nome.');
  await expect(page.getByLabel('Nome da pessoa')).toHaveValue('Pessoa inexistente E2E');
  await accessible(page);
});

test('name-selected first Polo assignment resolves shared team', async ({ page }) => {
  await login(page);
  await page.goto('/equipa/classificacao?q=Pessoa+Polo');
  await page.getByRole('link', { name: 'Selecionar Pessoa Polo disponível' }).click();
  const option = page.locator('#scope option').filter({ hasText: 'Equipa Polo partilhada' });
  await page.locator('#scope').selectOption(await option.getAttribute('value'));
  await page.getByLabel('Data de início', { exact: true }).fill(today());
  await page.getByRole('button', { name: 'Guardar participação', exact: true }).click();
  await expect(page.locator('#preview-heading')).toBeFocused();
  await page.getByRole('button', { name: 'Confirmar participação', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Guardada: Kayak_Polo');
  await expect(page.getByRole('region', { name: 'Histórico de participação' })).toContainText('Equipa Polo partilhada');
  await accessible(page);
});
