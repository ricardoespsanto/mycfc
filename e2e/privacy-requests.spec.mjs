import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const password = 'correct horse 7';
const reviewer = 'e2e-privacy-reviewer@example.test';
const executor = 'e2e-privacy-alternate@example.test';
const guardian = 'e2e-privacy-guardian@example.test';
const minorID = '11000000-0000-0000-0000-000000000003';
const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080';
const receipt = (page) => page.getByRole('region', { name: 'Recibo do pedido' });

async function login(page, identifier) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(identifier);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function register(page, tag) {
  const email = `e2e-privacy-${tag}-${Date.now()}-${Math.random().toString(16).slice(2)}@example.test`;
  await page.goto('/registo');
  await page.getByLabel('Nome').fill('Pessoa sintética de privacidade');
  await page.getByLabel('Correio eletrónico').fill(email);
  await page.getByLabel('Data de nascimento').fill('1990-01-01');
  await page.locator('#password').fill(password);
  await page.getByLabel('Confirmar palavra-passe').fill(password);
  await page.getByLabel(/Aceito os termos gerais/).check();
  // The real registration endpoint enforces its anti-bot minimum dwell time.
  await page.waitForTimeout(2100);
  await page.getByRole('button', { name: 'Criar conta', exact: true }).click();
  await expect(page).toHaveURL(/\/today$/);
  return email;
}

async function newRequest(page, { closure = false, categories = ['identity-core'], subject = null } = {}) {
  await page.goto('/perfil/privacidade/novo');
  await expect(page.locator('#policy_version')).toHaveValue('e2e-privacy-v1');
  if (subject) await page.getByLabel('Pessoa').selectOption(subject);
  await page.getByLabel('Âmbito').selectOption(closure ? 'ACCOUNT_CLOSURE' : 'CATEGORIES');
  if (!closure) for (const category of categories) await page.locator(`#category-${category}`).check();
  await page.getByLabel('Palavra-passe atual').fill(password);
}

async function submit(page) {
  await page.getByRole('button', { name: 'Enviar pedido de apagamento' }).click();
  await expect(page).toHaveURL(/\/perfil\/privacidade\/[a-f0-9-]{36}(?:\?.*)?$/);
  await expect(receipt(page)).toContainText('Recebido');
  return new URL(page.url()).pathname.split('/').pop();
}

async function review(browser, ref) {
  const context = await browser.newContext({ baseURL });
  const page = await context.newPage();
  await login(page, reviewer);
  await page.goto(`/admin/privacidade/${ref}`);
  await page.getByRole('button', { name: 'Assumir análise' }).click();
  await expect(receipt(page)).toContainText('Em análise');
  return { context, page };
}

async function verify(page, representative = false) {
  await page.getByLabel('Método de verificação', { exact: true }).selectOption('IN_PERSON');
  await page.getByLabel('Identidade verificada', { exact: true }).check();
  if (representative) {
    await page.getByLabel('Método de verificação da representação').selectOption('IN_PERSON');
    await page.getByLabel('Autoridade de representação verificada para este pedido').check();
  }
  await page.getByRole('button', { name: 'Guardar verificação' }).click();
  await expect(page.getByRole('heading', { name: 'Decisão', exact: true })).toBeVisible();
}

async function accessibleAt320(page) {
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => Math.max(document.body.scrollWidth, document.documentElement.scrollWidth)))
    .toBeLessThanOrEqual(321);
  const violations = (await new AxeBuilder({ page }).analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

async function privacyMessages(recipient, count) {
  const api = process.env.MAILPIT_API_BASE || 'http://127.0.0.1:8025';
  let messages = [];
  await expect.poll(async () => {
    const listing = await fetch(`${api}/api/v1/messages`).then((r) => r.json());
    const summaries = listing.messages.filter((m) => m.To.some(({ Address }) => Address === recipient));
    messages = await Promise.all(summaries.map((m) => fetch(`${api}/api/v1/message/${m.ID}`).then((r) => r.json())));
    messages = messages.filter((m) => /privacidade|apagamento/i.test(`${m.Subject} ${m.Text}`) && !/verificar-email\?/.test(m.Text));
    return messages.length;
  }, { timeout: 30000 }).toBe(count);
  return messages;
}

function genericMessages(messages, ref, explanation = '') {
  for (const message of messages) {
    const body = `${message.Subject}\n${message.Text}\n${message.HTML || ''}`;
    expect(body).not.toContain(ref);
    expect(body).not.toContain('Pessoa sintética de privacidade');
    expect(body).not.toContain('Categoria sintética');
    expect(body).not.toContain('e2e-privacy-v1');
    if (explanation) expect(body).not.toContain(explanation);
    expect(body).toContain('/legal/direitos');
  }
}

test('completion detail link is public, generic when unavailable, private and accessible at 320px', async ({ browser }) => {
  const context = await browser.newContext({ baseURL, viewport: { width: 320, height: 720 } });
  const page = await context.newPage();
  try {
    const token = 'must-never-appear-in-the-rendered-page';
    const response = await page.goto(`/privacidade/conclusao/${token}`);
    expect(response.status()).toBe(404);
    expect(response.headers()['cache-control']).toBe('no-store');
    expect(response.headers()['referrer-policy']).toBe('no-referrer');
    expect(response.headers()['x-robots-tag']).toBe('noindex, nofollow, noarchive');
    await expect(page.getByRole('heading', { name: 'Ligação indisponível', exact: true })).toBeVisible();
    await expect(page.getByText('Esta ligação é inválida, já foi utilizada ou expirou.')).toBeVisible();
    await expect(page.locator('body')).not.toContainText(token);
    await accessibleAt320(page);
  } finally { await context.close(); }
});

test('privacy execution controls expose bounded evidence with keyboard focus, reflow and no-JavaScript lookup', async ({ browser }) => {
  const context = await browser.newContext({ baseURL, viewport: { width: 320, height: 720 } });
  const page = await context.newPage();
  try {
    await login(page, executor);
    await page.goto('/admin/privacidade/controlo');
    await page.getByLabel('Referência do pedido').fill('not-a-reference');
    await page.getByRole('button', { name: 'Consultar estado técnico' }).focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('.error-summary')).toBeFocused();
    await expect(page.getByText('Introduza uma referência de pedido válida.').first()).toBeVisible();
    await accessibleAt320(page);

    await page.goto('/admin/privacidade/ativacao');
    await expect(page.getByRole('heading', { name: 'Ativação do processamento', exact: true })).toBeVisible();
    await expect(page.getByText('Restauro isolado', { exact: true })).toBeVisible();
    await expect(page.getByText('Infraestrutura', { exact: true })).toBeVisible();
    await expect(page.getByText('Destinatários externos', { exact: true })).toBeVisible();
    await expect(page.getByText('Esquema de dados', { exact: true })).toBeVisible();
    await expect(page.locator('input[type="file"], textarea')).toHaveCount(0);
    await accessibleAt320(page);
  } finally { await context.close(); }

  const noScriptContext = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const noScript = await noScriptContext.newPage();
  try {
    await login(noScript, executor);
    await noScript.goto('/admin/privacidade/controlo');
    await noScript.getByLabel('Referência do pedido').fill('still-not-a-reference');
    await noScript.getByRole('button', { name: 'Consultar estado técnico' }).click();
    await expect(noScript.getByText('Introduza uma referência de pedido válida.').first()).toBeVisible();
  } finally { await noScriptContext.close(); }
});

test('adult request validates with keyboard focus, deduplicates, sends a generic receipt and cancels without losing login', async ({ page }) => {
  test.setTimeout(120000);
  const email = await register(page, 'receipt');
  await page.goto('/perfil');
  await expect(page.getByRole('link', { name: /Pedidos de apagamento/ })).toBeVisible();
  await newRequest(page);
  await accessibleAt320(page);
  await page.locator('#category-identity-core').uncheck();
  await page.getByRole('button', { name: 'Enviar pedido de apagamento' }).click();
  await expect(page.locator('.error-summary')).toBeFocused();
  await page.locator('.error-summary a[href="#categories"]').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#categories')).toBeFocused();
  await page.locator('#category-identity-core').check();
  await page.getByLabel('Palavra-passe atual').fill('incorrect password');
  await page.getByRole('button', { name: 'Enviar pedido de apagamento' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.error-summary')).toBeFocused();
  await expect(page.locator('#category-identity-core')).toBeChecked();
  const passwordError = page.locator('.error-summary a[href="#password"]');
  await passwordError.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Palavra-passe atual')).toBeFocused();
  await accessibleAt320(page);
  await page.getByLabel('Palavra-passe atual').fill(password);
  const requestKey = await page.locator('#request_key').inputValue();
  const ref = await submit(page);
  await accessibleAt320(page);
  genericMessages(await privacyMessages(email, 1), ref);
  await newRequest(page);
  // Replaying the original native form key models a retry after a lost response.
  await page.locator('#request_key').evaluate((input, key) => { input.value = key; }, requestKey);
  const duplicate = await submit(page);
  expect(duplicate).toBe(ref);
  await newRequest(page);
  const activeConflict = page.waitForResponse((r) => r.request().method() === 'POST' && r.url().endsWith('/perfil/privacidade/novo'));
  await page.getByRole('button', { name: 'Enviar pedido de apagamento' }).click();
  expect((await activeConflict).status()).toBe(422);
  await expect(page.locator('.error-summary')).toContainText('Já existe um pedido ativo.');
  genericMessages(await privacyMessages(email, 1), ref);
  await page.goto('/perfil/privacidade');
  await expect(page.locator('a', { hasText: ref })).toHaveCount(1);
  await page.getByRole('link', { name: ref, exact: true }).click();
  await page.getByRole('button', { name: 'Cancelar pedido', exact: true }).click();
  await expect(receipt(page)).toContainText('Cancelado');
  await page.goto('/today');
  await expect(page).toHaveURL(/\/today$/);
  await page.context().clearCookies();
  await login(page, email);
});

test('full closure is claimed, verified and approved while preserving access and rejecting stale reviewer forms', async ({ page, browser }) => {
  test.setTimeout(120000);
  const email = await register(page, 'full');
  await newRequest(page, { closure: true });
  const ref = await submit(page);
  const staff = await review(browser, ref);
  try {
    await verify(staff.page);
    const stale = await staff.context.newPage();
    await stale.goto(`/admin/privacidade/${ref}`);
    await staff.page.getByLabel('Meses adicionais').selectOption('1');
    await staff.page.getByLabel('Motivo a comunicar ao requerente').selectOption('COMPLEXITY');
    await staff.page.getByRole('button', { name: 'Prorrogar e comunicar' }).click();
    for (const outcome of await stale.locator('select[id^="outcome_"]').all()) await outcome.selectOption('APPROVE');
    await stale.getByLabel('Explicação para o requerente').fill('Decisão obsoleta de teste');
    const rejected = stale.waitForResponse((r) => r.request().method() === 'POST' && r.url().includes(`/admin/privacidade/${ref}`));
    await stale.getByRole('button', { name: 'Aprovar — aguardar execução', exact: true }).click();
    expect((await rejected).status()).toBe(409);
    await expect(receipt(stale)).toContainText('Em análise');
    await stale.close();
    for (const outcome of await staff.page.locator('select[id^="outcome_"]').all()) await outcome.selectOption('APPROVE');
    const explanation = 'As categorias do pedido aguardam execução separada.';
    await staff.page.getByLabel('Explicação para o requerente').fill(explanation);
    await accessibleAt320(staff.page);
    await staff.page.getByRole('button', { name: 'Aprovar — aguardar execução', exact: true }).click();
    await expect(receipt(staff.page)).toContainText('Aprovado — a aguardar execução');
    await page.reload();
    await expect(page.getByText('Os dados ainda não foram apagados.')).toBeVisible();
    await expect(page.getByText(explanation, { exact: true }).first()).toBeVisible();
    await accessibleAt320(page);
    genericMessages(await privacyMessages(email, 3), ref, explanation);
    await page.context().clearCookies();
    await login(page, email);
  } finally { await staff.context.close(); }
});

test('independent executor reviews blockers and starts category processing with keyboard and no JavaScript', async ({ page, browser }) => {
  test.setTimeout(120000);
  const email = await register(page, 'execution');
  await newRequest(page, { categories: ['identity-core'] });
  const ref = await submit(page);
  const staff = await review(browser, ref);
  try {
    await verify(staff.page);
    await staff.page.locator('#outcome_identity-core').selectOption('APPROVE');
    await staff.page.getByLabel('Explicação para o requerente').fill('Execução sintética aprovada.');
    await staff.page.getByRole('button', { name: 'Aprovar — aguardar execução', exact: true }).click();
    await expect(receipt(staff.page)).toContainText('Aprovado — a aguardar execução');
  } finally { await staff.context.close(); }

  const previewContext = await browser.newContext({ baseURL, viewport: { width: 320, height: 720 } });
  const preview = await previewContext.newPage();
  try {
    await login(preview, executor);
    await preview.goto(`/admin/privacidade/${ref}`);
    await expect(preview.getByRole('heading', { name: 'Plano de execução aprovado' })).toBeVisible();
    await expect(preview.getByRole('heading', { name: 'Bloqueios atuais' })).toBeVisible();
    await expect(preview.getByText(/Nenhum bloqueio atual foi detetado/)).toBeVisible();
    await expect(preview.getByRole('button', { name: 'Iniciar processamento' })).toBeVisible();
    await accessibleAt320(preview);
  } finally { await previewContext.close(); }

  const noScriptContext = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const noScript = await noScriptContext.newPage();
  try {
    await login(noScript, executor);
    await noScript.goto(`/admin/privacidade/${ref}`);
    const confirmation = noScript.getByLabel(/Confirmo que revi o plano/);
    await confirmation.focus();
    await noScript.keyboard.press('Space');
    await noScript.getByRole('button', { name: 'Iniciar processamento' }).focus();
    await noScript.keyboard.press('Enter');
    await expect(receipt(noScript)).toContainText('Em processamento');
    await expect(noScript.getByText(/ainda não está concluído/)).toBeVisible();
  } finally { await noScriptContext.close(); }

  await page.reload();
  await expect(receipt(page)).toContainText('Em processamento');
  await page.goto('/today');
  await expect(page).toHaveURL(/\/today$/);
  genericMessages(await privacyMessages(email, 3), ref, 'Execução sintética aprovada.');
});

for (const partial of [true, false]) {
  test(`${partial ? 'partial approval' : 'refusal'} records category grounds and a readable decision`, async ({ page, browser }) => {
    test.setTimeout(90000);
    const email = await register(page, partial ? 'partial' : 'refusal');
    await newRequest(page, { categories: partial ? ['identity-core', 'profile-core'] : ['identity-core'] });
    const ref = await submit(page);
    const staff = await review(browser, ref);
    try {
      await verify(staff.page);
      await staff.page.locator('#outcome_identity-core').selectOption(partial ? 'APPROVE' : 'RETAIN');
      const retained = partial ? 'profile-core' : 'identity-core';
      if (partial) await staff.page.locator('#outcome_profile-core').selectOption('RETAIN');
      await staff.page.locator(`#ground_${retained}`).selectOption('LEGAL_HOLD');
      const explanation = partial ? 'Conservação parcial exclusivamente sintética.' : 'Recusa exclusivamente sintética e fundamentada.';
      await staff.page.getByLabel('Explicação para o requerente').fill(explanation);
      await staff.page.getByRole('button', { name: partial ? 'Aprovar parcialmente' : 'Recusar com explicação', exact: true }).click();
      await page.reload();
      await expect(receipt(page)).toContainText(partial ? 'Parcialmente aprovado — a aguardar execução' : 'Recusado');
      await expect(page.getByText(explanation, { exact: true }).first()).toBeVisible();
      await expect(page.getByText(/Conservação legal sintética/)).toBeVisible();
      genericMessages(await privacyMessages(email, 2), ref, explanation);
    } finally { await staff.context.close(); }
  });
}

test('ordinary administrator and unrelated member cannot review or read a private request', async ({ page, browser }) => {
  await register(page, 'owner');
  await newRequest(page);
  const ref = await submit(page);
  const context = await browser.newContext({ baseURL });
  const other = await context.newPage();
  try {
    await login(other, 'e2e-admin@example.test');
    for (const path of ['/admin/privacidade', `/admin/privacidade/${ref}`, `/perfil/privacidade/${ref}`]) {
      const response = await other.goto(path);
      expect([403, 404]).toContain(response.status());
      await expect(other.getByText(ref, { exact: true })).toHaveCount(0);
    }
    await context.clearCookies();
    await register(other, 'unrelated');
    const response = await other.goto(`/perfil/privacidade/${ref}`);
    expect(response.status()).toBe(404);
    await expect(other.getByText('Categoria sintética alfa')).toHaveCount(0);
  } finally { await context.close(); }
});

test('guardian receives only a redacted receipt until representation is verified; minor sees rights and no submission controls', async ({ page, browser }) => {
  test.setTimeout(120000);
  await login(page, guardian);
  await newRequest(page, { subject: minorID });
  const ref = await submit(page);
  await expect(page.getByRole('region', { name: 'Detalhes do pedido' })).toHaveCount(0);
  await expect(page.getByText('Categoria sintética alfa')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Histórico', exact: true })).toHaveCount(0);
  await accessibleAt320(page);
  const staff = await review(browser, ref);
  try {
    await verify(staff.page, true);
    await page.reload();
    await expect(page.getByRole('region', { name: 'Detalhes do pedido' })).toBeVisible();
    await expect(page.getByText('Identidade da conta', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Cancelar pedido', exact: true }).click();
    await expect(receipt(page)).toContainText('Cancelado');
  } finally { await staff.context.close(); }
  await page.context().clearCookies();
  await login(page, 'CFC-EE110003');
  await page.goto('/perfil/privacidade');
  await expect(page.getByRole('link', { name: /aviso de privacidade para menores/ })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Novo pedido', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Enviar pedido de apagamento' })).toHaveCount(0);
  await accessibleAt320(page);
  const response = await page.goto('/perfil/privacidade/novo');
  expect([403, 404]).toContain(response.status());
});

test('native forms submit and cancel at 320px with JavaScript disabled', async ({ browser }) => {
  test.setTimeout(90000);
  const context = await browser.newContext({ baseURL, javaScriptEnabled: false, viewport: { width: 320, height: 720 } });
  const page = await context.newPage();
  try {
    const email = await register(page, 'native');
    await newRequest(page);
    await submit(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(321);
    await page.getByRole('button', { name: 'Cancelar pedido', exact: true }).click();
    await expect(receipt(page)).toContainText('Cancelado');
    await context.clearCookies();
    await login(page, email);
  } finally { await context.close(); }
});
