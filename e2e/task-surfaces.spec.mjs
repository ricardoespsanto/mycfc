import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run against the deterministic UI-review dataset.');

const password = 'correct horse 7';

async function login(page) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('review-admin@example.test');
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

test('member creation side sheet contains focus, restores its opener and protects dirty work', async ({ page }) => {
  await login(page);
  await page.goto('/admin/membros');

  const primaryActions = page.locator('.page-header .action--primary');
  await expect(primaryActions).toHaveCount(1);
  const opener = page.getByRole('link', { name: 'Criar conta', exact: true });
  await expect(opener).toHaveAttribute('href', '/admin/membros/criar');
  await expect(opener).toHaveAttribute('aria-haspopup', 'dialog');

  await opener.click();
  const task = page.getByRole('dialog', { name: 'Criar conta' });
  await expect(task).toBeVisible();
  await expect(page).toHaveURL(/\/admin\/membros\/criar$/);
  await expect(task.getByLabel('Nome')).toBeFocused();
  await expect(page.locator('body')).toHaveClass(/task-surface-open/);
  await expect(task).toHaveJSProperty('open', true);

  const lastAction = task.getByRole('button', { name: 'Criar conta' });
  await lastAction.focus();
  await page.keyboard.press('Tab');
  await expect(task.getByRole('button', { name: 'Fechar' })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(lastAction).toBeFocused();

  await task.getByLabel('Nome').fill('Alteração por guardar');
  await task.getByLabel('Email').fill('alteracao@example.test');
  await task.locator('#member-password').fill('segredo temporário 7');
  await task.locator('#member-password-confirmation').fill('segredo temporário 7');
  await task.getByLabel('Email').focus();
  await page.keyboard.press('Escape');
  const confirmation = page.getByRole('dialog', { name: 'Descartar alterações?' });
  await expect(confirmation).toBeVisible();
  const discard = confirmation.getByRole('button', { name: 'Descartar alterações' });
  const keepEditing = confirmation.getByRole('button', { name: 'Continuar a editar' });
  await discard.focus();
  await page.keyboard.press('Tab');
  await expect(keepEditing).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(discard).toBeFocused();
  await keepEditing.click();
  await expect(confirmation).toBeHidden();
  await expect(task.getByLabel('Email')).toBeFocused();

  await task.getByRole('link', { name: 'Cancelar' }).click();
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole('button', { name: 'Descartar alterações' }).click();
  await expect(task).toBeHidden();
  await expect(page).toHaveURL(/\/admin\/membros$/);
  await expect(opener).toBeFocused();
  await expect(page.locator('body')).not.toHaveClass(/task-surface-open/);

  await opener.click();
  await expect(task).toBeVisible();
  await expect(task.getByLabel('Nome')).toHaveValue('');
  await expect(task.getByLabel('Email')).toHaveValue('');
  await expect(task.locator('#member-password')).toHaveValue('');
  await expect(task.locator('#member-password-confirmation')).toHaveValue('');
  await expect(task.getByRole('radio', { name: 'Adulto', exact: true })).toBeChecked();
  await expect(task.locator('[data-account-fields="dependent"]')).toBeHidden();
  await page.goBack();
  await expect(task).toBeHidden();
  await expect(opener).toBeFocused();

  await opener.click();
  await task.getByLabel('Nome').fill('Outra alteração');
  await page.goBack();
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole('button', { name: 'Continuar a editar' }).click();
  await expect(page).toHaveURL(/\/admin\/membros\/criar$/);
  await expect(task).toBeVisible();

  const violations = (await new AxeBuilder({ page }).include('#criar-conta').analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations).toEqual([]);

  await page.goBack();
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole('button', { name: 'Descartar alterações' }).click();
  await expect(task).toBeHidden();
  await expect(page).toHaveURL(/\/admin\/membros$/);
  await expect(opener).toBeFocused();
});

test('member task is full height at compact sizes and exposes visible recoverable pending state', async ({ page }) => {
  await login(page);
  await page.setViewportSize({ width: 320, height: 480 });
  await page.goto('/admin/membros');
  await page.getByRole('link', { name: 'Criar conta', exact: true }).click();

  const task = page.getByRole('dialog', { name: 'Criar conta' });
  const geometry = await task.evaluate((dialog) => {
    const header = dialog.querySelector('.task-surface__header').getBoundingClientRect();
    const actions = dialog.querySelector('.task-surface__actions').getBoundingClientRect();
    const rect = dialog.getBoundingClientRect();
    return { dialogTop: rect.top, dialogBottom: rect.bottom, headerTop: header.top, actionsBottom: actions.bottom, width: rect.width };
  });
  expect(geometry.dialogTop).toBe(0);
  expect(geometry.dialogBottom).toBeLessThanOrEqual(480);
  expect(geometry.headerTop).toBeGreaterThanOrEqual(0);
  expect(geometry.actionsBottom).toBeLessThanOrEqual(480);
  expect(geometry.width).toBeLessThanOrEqual(320);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);

  await page.evaluate(() => {
    window.taskSubmitDefaultPrevented = [];
    document.addEventListener('submit', (event) => {
      if (!event.target.matches('[data-task-form]')) return;
      window.taskSubmitDefaultPrevented.push(event.defaultPrevented);
      event.preventDefault();
    });
  });
  await task.locator('#member-password').fill('segredo temporário 7');
  await task.locator('#member-password-confirmation').fill('segredo temporário 7');
  const submit = task.getByRole('button', { name: 'Criar conta' });
  await submit.click();
  await expect(task.locator('form')).toHaveAttribute('aria-busy', 'true');
  await expect(task.getByRole('button', { name: 'A processar…' })).toBeDisabled();
  await expect(task.getByRole('status')).toHaveText('A processar pedido.');
  await task.locator('form').evaluate((form) => form.requestSubmit());
  expect(await page.evaluate(() => window.taskSubmitDefaultPrevented)).toEqual([false, true]);

  await page.evaluate(() => window.dispatchEvent(new window.Event('pageshow')));
  await expect(task.locator('form')).not.toHaveAttribute('aria-busy', 'true');
  await expect(task.getByRole('button', { name: 'Criar conta' })).toBeEnabled();
  await expect(task.locator('#member-password')).toHaveValue('');
  await expect(task.locator('#member-password-confirmation')).toHaveValue('');
  await task.locator('form').evaluate((form) => form.requestSubmit());
  expect(await page.evaluate(() => window.taskSubmitDefaultPrevented)).toEqual([false, true, false]);
});

test('guarded task route survives JavaScript asset failure and returns actionable 422', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await login(page);
  await page.goto('/admin/membros');
  await page.getByRole('link', { name: 'Criar conta', exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/membros\/criar$/);
  await expect(page.getByRole('heading', { name: 'Criar conta', level: 1 })).toBeVisible();
  await expect(page.getByLabel('Email (obrigatório para adultos)', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Tutor (obrigatório para menores)', { exact: true })).toBeVisible();

  await page.getByLabel('Nome').fill('A');
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => candidate.url().endsWith('/admin/membros/criar') && candidate.request().method() === 'POST'),
    page.getByRole('button', { name: 'Criar conta' }).click(),
  ]);
  expect(response.status()).toBe(422);
  await expect(page.locator('.error-summary')).toBeFocused();
  await expect(page.getByLabel('Nome')).toHaveValue('A');
  await expect(page.getByText('Acesso recusado.')).toHaveCount(0);
  await context.close();
});
