import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run against the deterministic UI-review dataset.');

const password = 'correct horse 7';

async function login(page) {
  await page.goto('/login', { waitUntil: 'domcontentloaded' });
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('review-admin@example.test');
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

test('Members keeps real search, comparison semantics and page-row return context', async ({ page }) => {
  await login(page);
  await page.goto('/admin/membros?q=Rodrigues', { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('search')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Limpar pesquisa' })).toHaveAttribute('href', '/admin/membros');
  const region = page.getByRole('region', { name: 'Lista de membros' });
  await expect(region).toHaveAttribute('aria-describedby', 'members-comparison-help');
  await expect(region.locator('thead th[scope="col"]')).toHaveCount(5);
  await page.getByRole('link', { name: 'Limpar pesquisa' }).click();
  await expect(page).toHaveURL(/\/admin\/membros$/);

  await page.goto('/admin/membros?page=2', { waitUntil: 'domcontentloaded' });
  const row = page.locator('tbody tr[id^="member-"]').first();
  const rowID = await row.getAttribute('id');
  const open = row.getByRole('link', { name: 'Abrir' });
  const detailHref = await open.getAttribute('href');
  expect(decodeURIComponent(detailHref)).toContain(`/admin/membros?page=2#${rowID}`);
  await open.click();
  const back = page.getByRole('link', { name: 'Voltar aos membros' });
  await expect(back).toHaveAttribute('href', `/admin/membros?page=2#${rowID}`);
  await back.click();
  await expect(page).toHaveURL(new RegExp(`/admin/membros\\?page=2#${rowID}$`));
  await expect(page.locator(`#${rowID}`)).toBeVisible();
});

test('Fleet prioritises work queues and row action menus restore focus predictably', async ({ page }) => {
  await login(page);
  await page.goto('/admin/fleet', { waitUntil: 'domcontentloaded' });
  const tabs = page.getByRole('tablist', { name: 'Áreas da frota' }).getByRole('tab');
  await expect(tabs).toHaveCount(3);
  await expect(tabs.nth(0)).toContainText('Reparações');
  await expect(tabs.nth(1)).toContainText('Manutenção');
  await expect(tabs.nth(2)).toContainText('Equipamentos');
  await expect(tabs.nth(0)).toHaveAttribute('aria-selected', 'true');
  await tabs.nth(2).click();

  const tableRegion = page.getByRole('region', { name: 'Inventário da frota' });
  await expect(tableRegion).toHaveAttribute('aria-describedby', 'fleet-comparison-help');
  await expect(tableRegion.locator('thead th[scope="col"]')).toHaveCount(4);
  const menus = tableRegion.locator('details[data-row-action-menu]');
  expect(await menus.count()).toBeGreaterThan(1);
  const firstSummary = menus.nth(0).locator('summary');
  await firstSummary.focus();
  await page.keyboard.press('ArrowDown');
  await expect(menus.nth(0)).toHaveAttribute('open', '');
  await expect(menus.nth(0).getByRole('link', { name: 'Retirar da frota' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(menus.nth(0)).not.toHaveAttribute('open', '');
  await expect(firstSummary).toBeFocused();

  await firstSummary.click();
  await menus.nth(1).locator('summary').click();
  await expect(menus.nth(0)).not.toHaveAttribute('open', '');
  await expect(menus.nth(1)).toHaveAttribute('open', '');
  await page.getByRole('heading', { name: 'Equipamentos' }).click();
  await expect(menus.nth(1)).not.toHaveAttribute('open', '');

  const rowID = await tableRegion.locator('tbody tr[id^="equipment-"]').first().getAttribute('id');
  await page.goto(`/admin/fleet#${rowID}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('tab', { name: 'Equipamentos', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator(`#${rowID}`)).toBeVisible();

  const returnedMenu = page.locator(`#${rowID}`).locator('details[data-row-action-menu]');
  const returnedSummary = returnedMenu.locator('summary');
  await returnedSummary.click();
  await returnedMenu.getByRole('link', { name: 'Retirar da frota' }).focus();
  await page.keyboard.press('Tab');
  await expect(returnedMenu).not.toHaveAttribute('open', '');
});

test('Fleet local mutation has visible pending state, suppresses duplicates and refreshes local status', async ({ page }) => {
  await login(page);
  await page.goto('/admin/fleet#repair-requests', { waitUntil: 'domcontentloaded' });
  const repair = page.locator('li[id^="repair-"]').first();
  const form = repair.locator('form[data-local-mutation]');
  const submit = form.getByRole('button');
  const startsAnalysis = (await submit.textContent()).includes('Iniciar');
  const expectedMessage = startsAnalysis ? 'Pedido de reparação em análise.' : 'Pedido de reparação resolvido.';
  const expectedBadge = startsAnalysis ? 'Em análise' : 'Concluído';
  let requests = 0;
  await page.route('**/admin/repairs/status?**', async (route) => {
    requests += 1;
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  await submit.evaluate((button) => button.click());
  await expect(form.getByRole('button', { name: 'A processar…' })).toBeDisabled();
  await form.evaluate((element) => element.requestSubmit());
  await expect(repair.getByRole('status')).toContainText(expectedMessage);
  expect(requests).toBe(1);
  await expect(repair.locator('[data-local-status]')).toContainText(expectedBadge);
});

test('local mutation keeps 422 and 409 feedback retryable without corrupting the badge', async ({ page }) => {
  await login(page);
  await page.goto('/admin/fleet#repair-requests', { waitUntil: 'domcontentloaded' });
  const repair = page.locator('li[id^="repair-"]').first();
  const form = repair.locator('form[data-local-mutation]');
  const targetID = await form.evaluate((element) => element.closest('[data-local-action]').id);
  const statusID = targetID.replace('-action-', '-status-');
  const initialBadge = await repair.locator('[data-local-status]').textContent();
  let requests = 0;
  await page.route('**/admin/repairs/status?**', async (route) => {
    requests += 1;
    const status = requests === 1 ? 422 : 409;
    await route.fulfill({
      status,
      contentType: 'text/html; charset=utf-8',
      body: `<div id="${targetID}" data-local-action><div data-local-response-feedback><p role="alert" tabindex="-1">${status === 422 ? 'A alteração não é válida.' : 'O pedido já foi atualizado.'}</p><a href="/admin/fleet#repair-requests">Atualizar frota</a></div></div>`,
    });
  });
  await form.getByRole('button').click();
  await expect(repair.getByRole('alert')).toBeFocused();
  await expect(repair.locator('[data-local-status]')).toHaveText(initialBadge);
  await expect(form.getByRole('button')).toBeEnabled();
  await form.getByRole('button').click();
  await expect(repair.getByRole('alert')).toContainText('já foi atualizado');
  await expect(repair.getByRole('alert')).toBeFocused();
  await expect(repair.getByRole('link', { name: 'Atualizar frota' })).toBeVisible();
  await expect(repair.locator(`#${statusID}`)).toHaveText(initialBadge);
  await expect(form.getByRole('button')).toBeEnabled();
  expect(requests).toBe(2);
});

test('local mutation rejects network and full-page responses with focusable recovery', async ({ page }) => {
  await login(page);
  await page.goto('/admin/fleet#repair-requests', { waitUntil: 'domcontentloaded' });
  const repair = page.locator('li[id^="repair-"]').first();
  const form = repair.locator('form[data-local-mutation]');
  const button = form.getByRole('button');
  await page.route('**/admin/repairs/status?**', (route) => route.abort('failed'));
  await button.click();
  const networkAlert = repair.locator('[data-local-network-error]');
  await expect(networkAlert).toHaveAttribute('tabindex', '-1');
  await expect(networkAlert).toBeFocused();
  await expect(button).toBeEnabled();

  await page.unroute('**/admin/repairs/status?**');
  const targetID = await form.evaluate((element) => element.closest('[data-local-action]').id);
  await page.route('**/admin/repairs/status?**', (route) => route.fulfill({
    status: 422,
    contentType: 'text/html; charset=utf-8',
    body: `<div id="${targetID}" data-local-action><div data-local-response-feedback><p role="alert" tabindex="-1">A alteração não é válida.</p></div></div>`,
  }));
  await button.click();
  const responseAlert = repair.locator('[data-local-response-feedback] [role="alert"]');
  await expect(responseAlert).toBeFocused();
  await expect(repair.locator('[data-local-network-error]')).toHaveCount(0);
  await expect(repair.getByRole('alert')).toHaveCount(1);
  await expect(button).toBeEnabled();

  await page.unroute('**/admin/repairs/status?**');
  await page.route('**/admin/repairs/status?**', (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html><html><body><main>Iniciar sessão</main></body></html>' }));
  await button.click();
  const unexpectedResponseAlert = repair.locator('[data-local-network-error]');
  await expect(unexpectedResponseAlert).toContainText('resposta não pôde ser aplicada');
  await expect(unexpectedResponseAlert).toBeFocused();
  await expect(repair.locator('[data-local-response-feedback]')).toHaveCount(0);
  await expect(repair.getByRole('alert')).toHaveCount(1);
  await expect(repair.locator('html')).toHaveCount(0);
  await expect(form).toBeVisible();
  await expect(button).toBeEnabled();

  let crossOriginRequests = 0;
  await page.route('https://example.invalid/**', (route) => {
    crossOriginRequests += 1;
    return route.abort();
  });
  const currentURL = page.url();
  await form.evaluate((element) => element.setAttribute('hx-post', 'https://example.invalid/admin/repairs/status'));
  await button.click();
  const originAlert = repair.locator('[data-local-network-error]');
  await expect(originAlert).toContainText('ação não pôde ser validada');
  await expect(originAlert).toBeFocused();
  await expect(repair.getByRole('alert')).toHaveCount(1);
  await expect(button).toBeEnabled();
  expect(crossOriginRequests).toBe(0);
  expect(page.url()).toBe(currentURL);
});

test('collection create surfaces preserve their validated invoking context', async ({ page }) => {
  await login(page);
  await page.goto('/admin/membros?page=2', { waitUntil: 'domcontentloaded' });
  await page.getByRole('link', { name: 'Criar conta' }).click();
  const memberTask = page.locator('dialog[data-task-surface]');
  await expect(memberTask).toBeVisible();
  await expect(memberTask.locator('form')).toHaveAttribute('action', /return_to=.*page%3D2/);

  await page.goto('/admin/fleet?equipment_page=2&repairs_page=2&maintenance_page=2#equipment-inventory', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#equipment-form form')).toHaveAttribute('action', /equipment_page%3D2.*maintenance_page%3D2.*repairs_page%3D2.*equipment-inventory/);
  await expect(page.locator('#equipment-form a', { hasText: 'Cancelar' })).toHaveAttribute('href', /equipment_page=2.*maintenance_page=2.*repairs_page=2.*equipment-inventory/);
  await expect(page.locator('#maintenance-form form')).toHaveAttribute('action', /maintenance-schedule/);
  await expect(page.locator('#repair-form input[name="return_to"]')).toHaveValue(/repair-requests/);
});

test('retirement uses a guarded preview and stale confirmation returns structured conflict', async ({ page }) => {
  await login(page);
  await page.goto('/admin/fleet#equipment-inventory', { waitUntil: 'domcontentloaded' });
  const row = page.locator('tr[id^="equipment-"]').first();
  await row.locator('summary').click();
  await row.getByRole('link', { name: 'Retirar da frota' }).click();
  await expect(page.getByRole('heading', { name: 'Retirar equipamento da frota' })).toBeVisible();
  const form = page.locator('form[data-task-form]');
  const concurrentStatus = await form.evaluate(async (element) => {
    const retireURL = new URL(element.action);
    const equipmentID = retireURL.pathname.split('/').at(-2);
    const editResponse = await fetch(`/admin/fleet/equipment/${equipmentID}/edit`);
    const editDocument = new window.DOMParser().parseFromString(await editResponse.text(), 'text/html');
    const editForm = editDocument.querySelector(`form[action*="/admin/fleet/equipment/${equipmentID}"]`);
    const body = new window.URLSearchParams(new FormData(editForm));
    body.set('notes', `${body.get('notes')} Alteração concorrente`.trim());
    const response = await fetch(editForm.action, { method: 'POST', body });
    return response.status;
  });
  expect(concurrentStatus).toBe(200);
  await page.getByLabel(/Confirmo que pretendo retirar/).check();
  await form.getByRole('button', { name: 'Retirar equipamento da frota' }).click();
  await expect(page).toHaveURL(/\/retire\?/);
  await expect(page.locator('.task-conflict')).toBeFocused();
  await expect(page.locator('.task-conflict')).toContainText('alterado entretanto');
  await expect(page.locator('.task-conflict').getByRole('link', { name: 'Voltar à frota' })).toBeVisible();
});

test('collections retain table relationships without document overflow at 320px and pass axe', async ({ page }) => {
  await login(page);
  await page.setViewportSize({ width: 320, height: 720 });
  for (const path of ['/admin/membros', '/admin/fleet#equipment-inventory']) {
    await page.goto(path, { waitUntil: 'domcontentloaded' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
    const region = page.locator('.data-region').filter({ has: page.locator('table.data-table') }).first();
    await expect(region).toBeVisible();
    const geometry = await region.evaluate((element) => ({ client: element.clientWidth, scroll: element.scrollWidth }));
    expect(geometry.scroll).toBeGreaterThan(geometry.client);
    const violations = (await new AxeBuilder({ page }).analyze()).violations
      .filter(({ impact }) => impact === 'serious' || impact === 'critical');
    expect(violations).toEqual([]);
  }
});

test('Fleet collection and action disclosure remain meaningful without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await login(page);
  await page.goto('/admin/fleet', { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Pedidos de reparação pendentes' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Próximas manutenções' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Equipamentos' })).toBeVisible();
  const menu = page.locator('details[data-row-action-menu]').first();
  await menu.locator('summary').click();
  await expect(menu.getByRole('link', { name: 'Retirar da frota' })).toBeVisible();
  await context.close();
});
