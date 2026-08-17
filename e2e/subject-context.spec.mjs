import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run against the deterministic UI-review dataset.');

const password = 'correct horse 7';
const tutorEmail = 'review-tutor@example.test';
const coachEmail = 'review-coach@example.test';
const eventID = '70000000-0000-0000-0000-000000000002';
const leonorID = '10000000-0000-0000-0000-000000000011';
const foreignID = '99999999-9999-9999-9999-999999999999';

async function login(page, email = tutorEmail) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

test('tutor selects a dependant, responds and keeps the event subject through PRG and reload', async ({ page }) => {
  await login(page);
  await page.goto(`/events/${eventID}`);

  const switcher = page.getByLabel('Participação de');
  await expect(switcher).toContainText('Marta Tutora da Família Rodrigues e Albuquerque (eu)');
  await expect(switcher).toContainText('Leonor Rodrigues e Albuquerque');
  await expect(page.getByText('A minha participação', { exact: true })).toHaveCount(0);

  await switcher.selectOption(leonorID);
  await page.getByRole('button', { name: 'Ver participação' }).click();
  await expect(page).toHaveURL(new RegExp(`/events/${eventID}\\?subject_user_id=${leonorID}$`));
  await expect(page.locator('.subject-context')).toContainText('Leonor Rodrigues e Albuquerque');
  await expect(page.getByText('A responder por').locator('..')).toContainText('Leonor Rodrigues e Albuquerque');
  await expect(switcher).toHaveValue(leonorID);

  await page.getByRole('button', { name: 'Vou', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/events/${eventID}\\?subject_user_id=${leonorID}$`));
  await expect(page.getByText('Estado: Vou')).toBeVisible();
  await page.reload();
  await expect(switcher).toHaveValue(leonorID);
  await expect(page.getByText('Estado: Vou')).toBeVisible();

  const violations = (await new AxeBuilder({ page }).include('#conteudo-principal').analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);

  const response = await page.goto(`/events/${eventID}?subject_user_id=${foreignID}`);
  expect(response.status()).toBe(404);
});

test('event subject switch remains usable without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 375, height: 640 } });
  const page = await context.newPage();
  await login(page);
  await page.goto(`/events/${eventID}`);
  await page.getByLabel('Participação de').selectOption(leonorID);
  await page.getByRole('button', { name: 'Ver participação' }).click();
  await expect(page).toHaveURL(new RegExp(`subject_user_id=${leonorID}$`));
  await expect(page.locator('.subject-context')).toContainText('Leonor Rodrigues e Albuquerque');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375);
  await context.close();
});

test('structured training reader filters authorized people and stays read-only', async ({ page }) => {
  await login(page);
  await page.goto('/treinos/estruturados');

  const subjectNavigation = page.getByRole('navigation', { name: 'Pessoa no planeamento' });
	const main = page.locator('#conteudo-principal');
  await expect(subjectNavigation.getByRole('link', { name: 'Todas as pessoas' })).toHaveAttribute('aria-current', 'page');
  await expect(subjectNavigation.getByRole('link', { name: /Marta Tutora.*\(eu\)/ })).toBeVisible();
  const leonor = subjectNavigation.getByRole('link', { name: 'Leonor Rodrigues e Albuquerque' });
  await expect(main.getByText('Marta Tutora da Família Rodrigues e Albuquerque', { exact: true })).toBeVisible();
  await expect(main.locator('#training-plan .eyebrow').getByText('Leonor Rodrigues e Albuquerque', { exact: true })).toBeVisible();

  await leonor.click();
  await expect(page).toHaveURL(new RegExp(`subject_user_id=${leonorID}$`));
  await expect(page.locator('.subject-context')).toContainText('Leonor Rodrigues e Albuquerque');
  await expect(main.getByText('Marta Tutora da Família Rodrigues e Albuquerque', { exact: true })).toHaveCount(0);
  await expect(main.locator('#training-plan .eyebrow').getByText('Leonor Rodrigues e Albuquerque', { exact: true })).toBeVisible();
  await expect(page.locator('#conteudo-principal form[method="post"]')).toHaveCount(0);
  await page.reload();
  await expect(subjectNavigation.getByRole('link', { name: 'Leonor Rodrigues e Albuquerque' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.subject-context')).toContainText('Leonor Rodrigues e Albuquerque');
});

test('managed event deep link uses coordination labels and breadcrumb', async ({ page }) => {
  await login(page, coachEmail);
  await page.goto('/admin/eventos/70000000-0000-0000-0000-000000000001');
  await expect(page.getByRole('navigation', { name: 'Localização atual' }).getByRole('link', { name: 'Gerir eventos' })).toHaveAttribute('href', '/admin/eventos');
  await expect(page.getByText('Coordenação · Eventos', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Gerir eventos', exact: true }).first()).toHaveAttribute('aria-current', 'page');
});
