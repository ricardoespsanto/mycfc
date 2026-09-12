import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const adminEmail = 'e2e-admin@example.test';
const guardianEmail = 'e2e-privacy-guardian@example.test';
const password = 'correct horse 7';
const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080';

async function login(page, email) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function expectNoSeriousAxeViolations(page, include) {
  let builder = new AxeBuilder({ page });
  if (include) builder = builder.include(include);
  const violations = (await builder.analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations).toEqual([]);
}

async function issueInvitation(page) {
  await page.goto('/admin/representacoes/convites');
  const create = page.locator('section[aria-labelledby="guardian-invitation-create-title"]');
  await create.getByLabel('Email do adulto').fill(guardianEmail);
  await create.getByLabel('Palavra-passe atual').fill(password);
  await create.getByRole('button', { name: 'Criar convite' }).click();
  await expect(page.getByText('Convite criado.', { exact: false })).toBeVisible();
  return page.locator('code').textContent();
}

async function logout(page) {
  await page.context().clearCookies();
  await page.goto('/login');
  await expect(page).toHaveURL(/\/login$/);
}

test('admin invitation management is discoverable, accessible, keyboard-safe and mobile-safe', async ({ page }) => {
  await login(page, adminEmail);
  const navigationLink = page.getByRole('link', { name: 'Convites de representação', exact: true });
  await expect(navigationLink).toBeVisible();
  await navigationLink.focus();
  await expect(navigationLink).toBeFocused();
  await navigationLink.click();
  await expect(page).toHaveURL('/admin/representacoes/convites');
  await expect(page.getByRole('heading', { name: 'Convites para associação', level: 1 })).toBeVisible();
  await expectNoSeriousAxeViolations(page);

  await page.setViewportSize({ width: 320, height: 720 });
  await expect(page.getByLabel('Email do adulto')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await expectNoSeriousAxeViolations(page);

  const email = page.getByLabel('Email do adulto');
  await email.focus();
  await page.keyboard.press('Tab');
  await expect(page.getByLabel('Palavra-passe atual').first()).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Criar convite' })).toBeFocused();
});

test('stale guardian authentication prompts, rejects, then rotates and submits', async ({ page }) => {
  await login(page, adminEmail);
  const token = await issueInvitation(page);
  await logout(page);
  await login(page, guardianEmail);
  await page.goto('/dashboard/guardian');
  await page.getByRole('link', { name: 'Pedir associação', exact: true }).click();

  const task = page.getByRole('dialog', { name: 'Pedir associação' });
  await expect(task.getByLabel('Palavra-passe atual')).toBeVisible();
  await task.getByLabel('Nome').fill(`Menor E2E ${Date.now()}`);
  await task.getByLabel('Data de nascimento').fill('2015-01-01');
  await task.getByLabel('Código de convite do clube').fill(await token);
  await task.getByLabel('Palavra-passe atual').fill('wrong password');
  await task.getByLabel(/Aceito a responsabilidade/).check();
  await task.getByRole('button', { name: 'Enviar pedido' }).click();
  await expect(page.locator('#password-error')).toHaveText('Não foi possível confirmar a palavra-passe atual.');
  await expectNoSeriousAxeViolations(page, '#pedir-associacao');

  const reopened = page.getByRole('dialog', { name: 'Pedir associação' });
  await reopened.getByLabel('Código de convite do clube').fill(await token);
  await reopened.getByLabel('Palavra-passe atual').fill(password);
  await reopened.getByRole('button', { name: 'Enviar pedido' }).click();
  await expect(page).toHaveURL('/dashboard/guardian');
  await expect(page.getByText('Pedido recebido.', { exact: false })).toBeVisible();
});

test('invitation issue and stale guardian confirmation work without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const page = await context.newPage();
  await login(page, adminEmail);
  const token = await issueInvitation(page);
  await logout(page);
  await login(page, guardianEmail);
  await page.goto('/dashboard/guardian');
  const task = page.getByRole('dialog', { name: 'Pedir associação' });
  await expect(task).toBeVisible();
  await task.getByLabel('Nome').fill(`Menor sem JS ${Date.now()}`);
  await task.getByLabel('Data de nascimento').fill('2016-01-01');
  await task.getByLabel('Código de convite do clube').fill(await token);
  await task.getByLabel('Palavra-passe atual').fill(password);
  await task.getByLabel(/Aceito a responsabilidade/).check();
  await task.getByRole('button', { name: 'Enviar pedido' }).click();
  await expect(page).toHaveURL('/dashboard/guardian');
  await expect(page.getByText('Pedido recebido.', { exact: false })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await context.close();
});

test('guardian process disclosure has an equivalent accessible mobile and no-JS explanation', async ({ page, browser }) => {
  await login(page, guardianEmail);
  await page.goto('/dashboard/guardian');
  const disclosure = page.locator('details').filter({ hasText: 'Como funciona a representação de um menor' });
  const summary = disclosure.locator('summary');
  await summary.focus();
  await expect(summary).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(disclosure).toHaveAttribute('open', '');
  const image = disclosure.getByRole('img', { name: /Resumo visual das etapas/ });
  await expect(image).toHaveAttribute('width', '1024');
  await expect(image).toHaveAttribute('height', '1536');
  await expect(image).toHaveAttribute('loading', 'lazy');
  await expect(image).toHaveAttribute('decoding', 'async');
  await expect(disclosure.locator('ol > li')).toHaveCount(6);
  await expect(disclosure.getByText('a cada 12 meses', { exact: false })).toBeVisible();
  await expect(disclosure.getByText('Aos 18 anos.', { exact: false })).toBeVisible();
  await expect(disclosure.getByText('Separação de funções:', { exact: false })).toBeVisible();
  await expect(disclosure.getByText('Minimização:', { exact: false })).toBeVisible();
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await expectNoSeriousAxeViolations(page, '#guardian-content');

  const noJS = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const noJSPage = await noJS.newPage();
  await login(noJSPage, guardianEmail);
  await noJSPage.goto('/dashboard/guardian');
  const noJSDisclosure = noJSPage.locator('details').filter({ hasText: 'Como funciona a representação de um menor' });
  await noJSDisclosure.locator('summary').click();
  await expect(noJSDisclosure.locator('ol > li')).toHaveCount(6);
  await expect(noJSDisclosure.getByText('a cada 12 meses', { exact: false })).toBeVisible();
  await expect(noJSDisclosure.getByText('Aos 18 anos.', { exact: false })).toBeVisible();
  await expect(noJSDisclosure.getByText('Minimização:', { exact: false })).toBeVisible();
  await noJS.close();
});

test('annual renewal is accessible, mobile-safe, keyboard-safe and works without JavaScript', async ({ page, browser }) => {
  await page.setViewportSize({ width: 320, height: 720 });
  await login(page, guardianEmail);
  await page.goto('/dashboard/guardian');
  const accessibleRelationship = page.getByRole('listitem').filter({ hasText: 'Menor de privacidade de teste' });
  await expect(accessibleRelationship).toContainText('Renovação disponível');
  await expect(accessibleRelationship).toContainText(/Validade atual:\s\d{2}\/\d{2}\/\d{4}/);
  const accessibleUnchanged = accessibleRelationship.getByLabel('Nada mudou');
  await accessibleUnchanged.focus();
  await expect(accessibleUnchanged).toBeFocused();
  await page.keyboard.press('Space');
  await expect(accessibleUnchanged).toBeChecked();
  await expectNoSeriousAxeViolations(page, '#guardian-content');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);

  const context = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const noJSPage = await context.newPage();
  await login(noJSPage, guardianEmail);
  await noJSPage.goto('/dashboard/guardian');
  const relationship = noJSPage.getByRole('listitem').filter({ hasText: 'Menor de privacidade de teste' });
  await expect(relationship).toContainText('Renovação disponível');
  await expect(relationship).toContainText(/Validade atual:\s\d{2}\/\d{2}\/\d{4}/);
  const unchanged = relationship.getByLabel('Nada mudou');
  await unchanged.focus();
  await expect(unchanged).toBeFocused();
  await noJSPage.keyboard.press('Space');
  await expect(unchanged).toBeChecked();
  expect(await noJSPage.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await relationship.getByRole('button', { name: 'Enviar para revisão' }).click();
  await expect(noJSPage).toHaveURL('/dashboard/guardian');
  await expect(noJSPage.getByText('Informação de renovação enviada para revisão pelo clube.')).toBeVisible();
  await expect(noJSPage.getByText('Renovação enviada', { exact: true })).toBeVisible();
  await expect(noJSPage.getByText('não foi prolongado automaticamente', { exact: false })).toBeVisible();
  await context.close();
});

test('age-18 transition is accessible, mobile-safe, keyboard-safe and works without JavaScript', async ({ page, browser }) => {
  await page.setViewportSize({ width: 320, height: 720 });
  await login(page, 'CFC-EE110007');
  await page.goto('/transicao-18');
  await expect(page.getByRole('heading', { name: 'Transição aos 18 anos', level: 1 })).toBeVisible();
  await expect(page.getByText('Prepare o acesso adulto na mesma conta', { exact: false })).toBeVisible();
  await expect(page.locator('main ol > li')).toHaveCount(3);
  await expect(page.getByText('Aviso de 30 dias', { exact: true })).toBeVisible();
  const personalEmail = page.getByRole('textbox', { name: /Email pessoal/ });
  await personalEmail.focus();
  await expect(personalEmail).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Enviar link de confirmação' })).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await expectNoSeriousAxeViolations(page);

  await page.locator('form[action="/transicao-18/email"]').evaluate((form) => form.submit());
  const youngSummaryLink = page.locator('.error-summary a[href="#email"]');
  await expect(youngSummaryLink).toBeVisible();
  await expect(page.locator('#email[aria-describedby~="email-error"]')).toHaveCount(1);
  await expectNoSeriousAxeViolations(page);

  const noJS = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const noJSPage = await noJS.newPage();
  await login(noJSPage, 'CFC-EE110007');
  await noJSPage.goto('/transicao-18');
  await expect(noJSPage.getByRole('heading', { name: 'Transição aos 18 anos', level: 1 })).toBeVisible();
  await expect(noJSPage.locator('main ol > li')).toHaveCount(3);
  await expect(noJSPage.getByRole('textbox', { name: /Email pessoal/ })).toBeVisible();
  expect(await noJSPage.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await noJS.close();

  await logout(page);
  await login(page, adminEmail);
  await page.goto('/admin/transicoes-18');
  await expect(page.getByRole('heading', { name: 'Transições aos 18 anos', level: 1 })).toBeVisible();
  const item = page.getByRole('listitem').filter({ hasText: 'Jovem em transição de teste' });
  await item.getByRole('link').click();
  await expect(page.getByRole('heading', { name: 'Confirmar identidade', level: 2 })).toBeVisible();
  await expect(page.getByText('Não carregue documentos nem registe números ou texto livre.', { exact: false })).toBeVisible();
  await expect(page.getByLabel('Palavra-passe atual')).toBeVisible();
  await expectNoSeriousAxeViolations(page);
  await page.locator('form[action$="/confirmar"]').evaluate((form) => form.submit());
  const adminSummaryLink = page.locator('.error-summary a[href="#confirmation"]');
  await expect(adminSummaryLink).toBeVisible();
  await expect(page.locator('#confirmation[aria-describedby~="confirmation-error"]')).toHaveCount(1);
  await expectNoSeriousAxeViolations(page);
});
