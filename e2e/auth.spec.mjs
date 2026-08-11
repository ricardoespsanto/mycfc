import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const email = `e2e-${Date.now()}@example.test`;
const recoveryEmail = `e2e-recovery-${Date.now()}@example.test`;
const guardianEmail = `e2e-guardian-${Date.now()}@example.test`;
const leisureEmail = `e2e-leisure-${Date.now()}@example.test`;
const athleteEmail = `e2e-athlete-${Date.now()}@example.test`;
const waitlistedEmail = `e2e-waitlisted-${Date.now()}@example.test`;
const adminEmail = 'e2e-admin@example.test';
const password = 'correct horse 7';
const baseURL = process.env.E2E_BASE_URL || 'http://127.0.0.1:18080';
const validPNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2h38AAAAASUVORK5CYII=', 'base64');

async function expectNoSeriousAxeViolations(page) {
  const results = await new AxeBuilder({ page }).analyze();
  const serious = results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

async function expectNoSeriousFormAxeViolations(page) {
  const results = await new AxeBuilder({ page })
    .include('main')
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze();
  const serious = results.violations.filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

async function expectNoHorizontalOverflow(page) {
  const dimensions = await page.evaluate(() => ({
    bodyScrollWidth: document.body.scrollWidth,
    documentScrollWidth: document.documentElement.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
  }));
  expect(Math.max(dimensions.bodyScrollWidth, dimensions.documentScrollWidth), dimensions).toBeLessThanOrEqual(dimensions.viewportWidth + 1);
}

async function emulateBrowserZoom(page, zoom, viewport = { width: 1280, height: 720 }) {
  await page.setViewportSize({ width: viewport.width / zoom, height: viewport.height / zoom });
}

async function verificationLinkFor(recipient) {
  const apiBase = process.env.MAILPIT_API_BASE || 'http://127.0.0.1:8025';
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const listing = await fetch(`${apiBase}/api/v1/messages`).then((response) => response.json());
    const summary = listing.messages.find((message) => message.To.some(({ Address }) => Address === recipient));
    if (summary) {
      const message = await fetch(`${apiBase}/api/v1/message/${summary.ID}`).then((response) => response.json());
      const match = message.Text.match(/https?:\/\/[^\s]+\/verificar-email\?[^\s]+/);
      if (match) {
        const delivered = new URL(match[0]);
        const testOrigin = new URL(baseURL);
        delivered.protocol = testOrigin.protocol;
        delivered.host = testOrigin.host;
        return delivered.toString();
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`verification email for ${recipient} was not delivered`);
}

async function recoveryLinkFor(recipient) {
  const apiBase = process.env.MAILPIT_API_BASE || 'http://127.0.0.1:8025';
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const listing = await fetch(`${apiBase}/api/v1/messages`).then((response) => response.json());
    for (const summary of listing.messages.filter((message) => message.To.some(({ Address }) => Address === recipient))) {
      const message = await fetch(`${apiBase}/api/v1/message/${summary.ID}`).then((response) => response.json());
      const match = message.Text.match(/https?:\/\/[^\s]+\/recuperar-palavra-passe\/repor\?token=[^\s]+/);
      if (match) {
        const delivered = new URL(match[0]);
        const testOrigin = new URL(baseURL);
        delivered.protocol = testOrigin.protocol;
        delivered.host = testOrigin.host;
        return delivered.toString();
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`password recovery email for ${recipient} was not delivered`);
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

  test('requests password recovery and handles invalid reset links accessibly', async ({ page, browser }) => {
    test.setTimeout(120_000);
    await page.goto('/login');
    const recoveryLink = page.getByRole('link', { name: 'Recuperar palavra-passe' });
    await expect(recoveryLink).toHaveAttribute('href', '/recuperar-palavra-passe');
    await recoveryLink.click();

    await expect(page.getByRole('heading', { name: 'Recuperar palavra-passe' })).toBeVisible();
    const identifier = page.getByLabel('Correio eletrónico');
    await identifier.focus();
    await expect(identifier).toBeFocused();
    await expectNoSeriousAxeViolations(page);
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
    await identifier.fill('unknown-recovery@example.test');
    await page.getByRole('button', { name: 'Enviar instruções' }).click();
    await expect(page.getByRole('heading', { name: 'Consulte o seu email' })).toBeVisible();
    await expect(page.getByText(/Se os dados corresponderem a uma conta adulta elegível/)).toBeVisible();
    await expect(page.getByText('unknown-recovery@example.test')).toHaveCount(0);
    await expectNoSeriousAxeViolations(page);

    const invalidResponse = await page.goto('/recuperar-palavra-passe/repor?token=e2e-invalid-token');
    expect(invalidResponse?.status()).toBe(422);
    expect(invalidResponse?.headers()['cache-control']).toContain('no-store');
    expect(invalidResponse?.headers()['referrer-policy']).toBe('no-referrer');
    await expect(page.getByRole('heading', { name: 'Este link já não está disponível' })).toBeVisible();
    await expect(page.getByText('e2e-invalid-token')).toHaveCount(0);
    await expect(page.locator('a[href^="http"]')).toHaveCount(0);
    await expectNoSeriousAxeViolations(page);

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/recuperar-palavra-passe');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill('another-unknown@example.test');
    await noJavaScriptPage.getByRole('button', { name: 'Enviar instruções' }).click();
    await expect(noJavaScriptPage.getByRole('heading', { name: 'Consulte o seu email' })).toBeVisible();
    await noJavaScriptPage.goto('/recuperar-palavra-passe');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill('CFC-AB12CD34');
    await noJavaScriptPage.getByRole('button', { name: 'Enviar instruções' }).click();
    await expect(noJavaScriptPage.getByRole('heading', { name: 'Consulte o seu email' })).toBeVisible();
    await context.close();
  });

  test('registers a member, reaches the today view, and has no serious accessibility violations', async ({ page }) => {
    test.setTimeout(90_000);
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
    await page.locator('#password').fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.waitForTimeout(2100);
    await page.getByRole('button', { name: 'Criar conta' }).click();

    await expect(page).toHaveURL('/today');
    await expect(page.getByRole('heading', { name: 'Olá, Pessoa', exact: true })).toBeVisible();
    await expect(page.locator('.app-rail .email-verification-cue')).toHaveText('Email por verificar');
    await expectNoSeriousAxeViolations(page);

    await page.goto(await verificationLinkFor(email));
    await expect(page).toHaveURL('/perfil');
    await expect(page.getByText('Email confirmado.')).toBeVisible();
    await expect(page.locator('.email-verification-cue')).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Perfil', exact: true })).toBeVisible();
    await expect(page.getByText(/Complete o contacto de emergência/)).toBeVisible();
    await page.getByLabel('Telefone', { exact: true }).fill('+351 910 000 000');
    await page.locator('#profile-emergency-name').fill('Contacto E2E');
    await page.locator('#profile-emergency-relationship').fill('Família');
    await page.locator('#profile-emergency-phone').fill('+351 920 000 000');
    await page.locator('#profile-medical-declaration').selectOption('NONE_KNOWN');
    await page.getByRole('button', { name: 'Guardar perfil' }).click();
    await expect(page).toHaveURL('/perfil');
    await expect(page.getByText('Perfil atualizado.')).toBeVisible();
    await expect(page.getByText(/Complete o contacto de emergência/)).toHaveCount(0);
    await page.getByLabel('Fotografia JPEG, PNG ou WebP').setInputFiles({ name: 'perfil.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Carregar fotografia' }).click();
    await expect(page.getByText('Fotografia atualizada.')).toBeVisible();
    await expect(page.getByAltText('Fotografia de Pessoa de teste')).toBeVisible();
    await expectNoSeriousAxeViolations(page);

    await page.goto('/dashboard/member?from=legacy');
    await expect(page).toHaveURL('/today?from=legacy');

    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);

    const mobileMenu = page.locator('.mobile-app-menu');
    await mobileMenu.locator('summary').click();
    const logout = mobileMenu.getByRole('button', { name: 'Terminar sessão' });
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

    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Avisos', exact: true }).click();
    await expect(page).toHaveURL('/announcements');
    await page.goto('/today');
    await page.getByRole('link', { name: 'Frota', exact: true }).click();
    await expect(page).toHaveURL('/fleet');

    await page.locator('#repair-form > summary').click();
    const idempotencyKey = await page.locator('input[name="idempotency_key"]').inputValue();
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByLabel('Fotografia (opcional)').setInputFiles({ name: 'avaria.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/fleet');
    const success = page.getByText(/Avaria reportada\. Referência:/);
    await expect(success).toBeVisible();
    await expect(page.getByText('Avaria de teste com fotografia.').first()).toBeVisible();
    await expect(page.getByText('Reportada por si').first()).toBeVisible();
    const firstReference = await success.textContent();

    await page.locator('input[name="idempotency_key"]').evaluate((input, value) => { input.value = value; }, idempotencyKey);
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/fleet');
    await expect(page.getByText(/Avaria reportada\. Referência:/)).toHaveText(firstReference ?? '');
    await context.close();
  });

  test('member submits a private suggestion and receives the administrator response', async ({ page, browser }) => {
    test.setTimeout(180_000);
    const subject = `Ideia E2E ${Date.now()}`;
    const suggestionMemberEmail = `e2e-suggestion-${Date.now()}@example.test`;
    await page.goto('/registo');
    await page.getByLabel('Nome').fill('Membro com sugestão');
    await page.getByLabel('Correio eletrónico').fill(suggestionMemberEmail);
    await page.getByLabel('Data de nascimento').fill('1990-01-01');
    await page.locator('#password').fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.waitForTimeout(2100);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page).toHaveURL('/today');
    await page.goto('/sugestoes');
    await expect(page.getByRole('heading', { name: 'Sugestões', exact: true })).toBeVisible();
    await expectNoSeriousAxeViolations(page);
    await page.getByRole('link', { name: 'Nova sugestão' }).click();
    await expect(page.locator('#nova-sugestao')).toHaveAttribute('open', '');
    await page.getByLabel('Categoria').selectOption('FACILITIES');
    await page.getByLabel('Assunto').fill(subject);
    await page.locator('#suggestion-description').fill('Adicionar mais bancos junto à zona de embarque para apoiar atletas e famílias.');
    await page.getByRole('button', { name: 'Enviar sugestão' }).click();
    await expect(page).toHaveURL('/sugestoes');
    await expect(page.getByText('Sugestão enviada. Pode acompanhar o estado nesta página.')).toBeVisible();
    await expect(page.getByRole('heading', { name: subject })).toBeVisible();
    await page.locator('.app-rail form[action="/logout"] button').click();

    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/sugestoes');
    const suggestion = page.locator('.record-item').filter({ has: page.getByRole('heading', { name: subject }) });
    await expect(suggestion.getByText('Enviada por:')).toBeVisible();
    await suggestion.getByLabel('Estado').selectOption('PLANNED');
    await suggestion.getByLabel('Resposta ao membro').fill('A direção vai avaliar esta melhoria no próximo plano de instalações.');
    await suggestion.getByRole('button', { name: 'Guardar triagem' }).click();
    await expect(page.getByText('Sugestão atualizada.')).toBeVisible();
    await page.locator('.app-rail form[action="/logout"] button').click();

    await page.getByLabel('Correio eletrónico').fill(suggestionMemberEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/sugestoes');
    const updated = page.locator('.record-item').filter({ has: page.getByRole('heading', { name: subject }) });
    await expect(updated.getByText('Planeada')).toBeVisible();
    await expect(updated.getByText('A direção vai avaliar esta melhoria no próximo plano de instalações.')).toBeVisible();

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/login');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill(suggestionMemberEmail);
    await noJavaScriptPage.getByLabel('Palavra-passe').fill(password);
    await noJavaScriptPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await noJavaScriptPage.goto('/sugestoes');
    await noJavaScriptPage.locator('#nova-sugestao > summary').click();
    await expect(noJavaScriptPage.getByRole('button', { name: 'Enviar sugestão' })).toBeVisible();
    await context.close();
  });

  test('resets a password from Mailpit and revokes every older session', async ({ browser }) => {
    test.setTimeout(300_000);
    const newPassword = 'updated password 8';

    const oldSessionContext = await browser.newContext({ baseURL });
    const oldSessionPage = await oldSessionContext.newPage();
    await oldSessionPage.goto('/registo');
    await oldSessionPage.getByLabel('Nome').fill('Pessoa de recuperação');
    await oldSessionPage.getByLabel('Correio eletrónico').fill(recoveryEmail);
    await oldSessionPage.getByLabel('Data de nascimento').fill('1990-01-01');
    await oldSessionPage.locator('#password').fill(password);
    await oldSessionPage.getByLabel('Confirmar palavra-passe').fill(password);
    await oldSessionPage.getByLabel(/Aceito os termos gerais/).check();
    await oldSessionPage.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await oldSessionPage.waitForTimeout(2100);
    await oldSessionPage.getByRole('button', { name: 'Criar conta' }).click();
    await expect(oldSessionPage).toHaveURL('/today');

    const requestContext = await browser.newContext({ baseURL, viewport: { width: 320, height: 720 } });
    const requestPage = await requestContext.newPage();
    await requestPage.goto('/login');
    await requestPage.getByRole('link', { name: 'Recuperar palavra-passe' }).click();
    await requestPage.getByLabel('Correio eletrónico').fill(recoveryEmail);
    await requestPage.getByRole('button', { name: 'Enviar instruções' }).click();
    await expect(requestPage.getByRole('heading', { name: 'Consulte o seu email' })).toBeVisible();

    const recoveryLink = await recoveryLinkFor(recoveryEmail);
    const resetPage = await requestContext.newPage();
    await resetPage.route('**/assets/*.js', (route) => route.abort());
    await resetPage.goto(recoveryLink);
    await expect(resetPage.getByRole('heading', { name: 'Definir nova palavra-passe' })).toBeVisible();
    await expectNoSeriousFormAxeViolations(resetPage);
    await resetPage.locator('#password').fill('short');
    await resetPage.getByLabel('Confirmar nova palavra-passe').fill('different password 9');
    await resetPage.getByRole('button', { name: 'Alterar palavra-passe' }).click();
    await expect(resetPage.locator('.error-summary')).toBeFocused();
    await expect(resetPage.locator('#password')).toHaveValue('');
    await expect(resetPage.getByLabel('Confirmar nova palavra-passe')).toHaveValue('');
    await resetPage.locator('#password').fill(newPassword);
    await resetPage.getByLabel('Confirmar nova palavra-passe').fill(newPassword);
    await resetPage.getByRole('button', { name: 'Alterar palavra-passe' }).click();
    await expect(resetPage).toHaveURL('/login');
    await expect(resetPage.getByRole('status')).toHaveText('A palavra-passe foi alterada. Já pode iniciar sessão.');

    await oldSessionPage.goto('/today');
    await expect(oldSessionPage).toHaveURL(/\/login\?next=%2Ftoday$/);

    await resetPage.getByLabel('Correio eletrónico').fill(recoveryEmail);
    await resetPage.getByLabel('Palavra-passe').fill(newPassword);
    await resetPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(resetPage).toHaveURL('/today');

    const rejectedContext = await browser.newContext({ baseURL });
    const rejectedPage = await rejectedContext.newPage();
    await rejectedPage.goto('/login');
    await rejectedPage.getByLabel('Correio eletrónico').fill(recoveryEmail);
    await rejectedPage.getByLabel('Palavra-passe').fill(password);
    await rejectedPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(rejectedPage).toHaveURL('/login');
    await expect(rejectedPage.locator('.error-summary')).toBeFocused();
    const replayResponse = await rejectedPage.goto(recoveryLink);
    expect(replayResponse?.status()).toBe(422);
    await expect(rejectedPage.getByRole('heading', { name: 'Este link já não está disponível' })).toBeVisible();

    await rejectedContext.close();
    await requestContext.close();
    await oldSessionContext.close();
  });

  test('creates a dependent with JavaScript disabled', async ({ page, browser }) => {
    await page.goto('/registo');
    await page.getByLabel('Nome').fill('Guardião de teste');
    await page.getByLabel('Correio eletrónico').fill(guardianEmail);
    await page.getByLabel('Data de nascimento').fill('1980-01-01');
    await page.locator('#password').fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.waitForTimeout(2100);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page).toHaveURL('/today');
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
    await noJavaScriptPage.locator('#adicionar-menor > summary').click();

    await noJavaScriptPage.getByLabel('Nome').fill('X');
    await noJavaScriptPage.getByLabel('Data de nascimento').fill('2014-01-01');
    await noJavaScriptPage.getByLabel(/Aceito a responsabilidade pelo menor a cargo/).check();
    await noJavaScriptPage.getByRole('button', { name: 'Adicionar menor' }).click();
    await expect(noJavaScriptPage.locator('.error-summary')).toBeVisible();
    await expect(noJavaScriptPage.getByLabel('Nome')).toHaveValue('X');

    await noJavaScriptPage.getByLabel('Nome').fill('Menor de teste');
    await noJavaScriptPage.getByLabel('Data de nascimento').fill('2014-01-01');
    await noJavaScriptPage.getByLabel(/Aceito a responsabilidade pelo menor a cargo/).check();
    await noJavaScriptPage.getByRole('button', { name: 'Adicionar menor' }).click();

    await expect(noJavaScriptPage).toHaveURL('/dashboard/guardian');
    await expect(noJavaScriptPage.getByText('Menor a cargo adicionado.')).toBeVisible();
	const dependentLink = noJavaScriptPage.getByRole('link', { name: 'Menor de teste' });
	await expect(dependentLink).toBeVisible();
	await dependentLink.click();
	await noJavaScriptPage.locator('#profile-emergency-name').fill('Guardião de teste');
	await noJavaScriptPage.locator('#profile-emergency-relationship').fill('Tutor');
	await noJavaScriptPage.locator('#profile-emergency-phone').fill('+351 930 000 000');
	await noJavaScriptPage.locator('#profile-medical-declaration').selectOption('NONE_KNOWN');
	await noJavaScriptPage.getByRole('button', { name: 'Guardar perfil' }).click();
	await expect(noJavaScriptPage.getByText('Perfil atualizado.')).toBeVisible();
	await noJavaScriptPage.getByRole('link', { name: 'Voltar aos menores' }).click();
	await expect(noJavaScriptPage.getByText(/Perfil incompleto/)).toHaveCount(0);
    await noJavaScriptPage.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(noJavaScriptPage);
    await context.close();
  });

  test('administrator validates, schedules, and completes maintenance with keyboard and zoom coverage', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Frota', exact: true }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('heading', { name: 'Frota', exact: true })).toBeVisible();
    await page.getByRole('link', { name: 'Agendar manutenção', exact: true }).click();
    await expect(page.locator('#maintenance-equipment')).toBeVisible();
    await expectNoSeriousAxeViolations(page);
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);

    await emulateBrowserZoom(page, 2);
    await expectNoHorizontalOverflow(page);

    const maintenanceForm = page.locator('#maintenance-form');
    const scheduledFor = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16);
    await maintenanceForm.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await maintenanceForm.getByLabel('Data e hora').fill(scheduledFor);
    await maintenanceForm.getByLabel('Descrição').fill('curta');
    await maintenanceForm.getByRole('button', { name: 'Agendar manutenção' }).click();
    await expect(maintenanceForm.locator('#description-error')).toHaveText(
      'A descrição deve ter entre 10 e 2000 caracteres.',
    );
    await expect(maintenanceForm.locator('.error-summary')).toBeFocused();
    await expect(maintenanceForm.getByLabel('Equipamento')).not.toHaveValue('');
    await expect(maintenanceForm.getByLabel('Data e hora')).toHaveValue(scheduledFor);

    const description = `Manutenção e2e ${Date.now()}`;
    await maintenanceForm.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await maintenanceForm.getByLabel('Data e hora').fill(scheduledFor);
    await maintenanceForm.getByLabel('Descrição').fill(description);
    const schedule = maintenanceForm.getByRole('button', { name: 'Agendar manutenção' });
    await schedule.focus();
    await expect(schedule).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(maintenanceForm.getByRole('status')).toHaveText('Manutenção agendada.');

    await page.reload();
    await page.getByRole('tab', { name: /Manutenção/ }).click();
    const task = page.locator('li', { hasText: description });
    const complete = task.getByRole('button', { name: 'Concluir manutenção' });
    await complete.focus();
    await expect(complete).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('status')).toHaveText('Manutenção concluída.');
  });

  test('administrator manages equipment lifecycle and audit history without JavaScript', async ({ browser }) => {
    test.setTimeout(120000);
    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const page = await context.newPage();
    const suffix = Date.now();
    const assetTag = `E2E-${suffix}`;
    const updatedTag = `${assetTag}-U`;
    const maintenanceDescription = `Revisão antes da retirada ${suffix}`;

    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Frota', exact: true }).click();

    await page.locator('#equipment-form > summary').click();
    const create = page.locator('#equipment-form');
    await create.getByLabel('Identificador').fill('X');
    await create.getByLabel('Nome').fill('X');
    await create.getByRole('button', { name: 'Adicionar equipamento' }).click();
    await expect(create.locator('.error-summary')).toBeFocused();
    await expect(create.getByLabel('Identificador')).toHaveValue('X');

    await create.getByLabel('Identificador').fill(assetTag);
    await create.getByLabel('Nome').fill('Embarcação E2E');
    await create.getByLabel('Tipo').selectOption('Boat');
    await create.getByLabel('Estado').selectOption('Operational');
    await create.getByLabel('Notas (opcional)').fill('Registo inicial');
    await create.getByLabel('Fotografia (opcional)').setInputFiles({ name: 'embarcacao.png', mimeType: 'image/png', buffer: validPNG });
    await create.getByRole('button', { name: 'Adicionar equipamento' }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('status')).toHaveText('Equipamento adicionado.');

    let equipment = page.getByRole('row', { name: new RegExp(assetTag) });
    await equipment.getByText('Ações').click();
    await equipment.getByRole('link', { name: 'Editar' }).click();
    const equipmentEditURL = page.url();
    await page.getByLabel('Identificador').fill(updatedTag);
    await page.getByLabel('Nome').fill('Pagaia E2E atualizada');
    await page.getByLabel('Tipo').selectOption('Paddle');
    await page.getByLabel('Estado').selectOption('Maintenance');
    await page.getByLabel('Notas (opcional)').fill('Notas atualizadas');
    await page.getByLabel('Substituir fotografia (opcional)').setInputFiles({ name: 'pagaia.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Guardar alterações' }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('status')).toHaveText('Equipamento atualizado.');

    equipment = page.getByRole('row', { name: new RegExp(updatedTag) });
    await equipment.getByText('Ações').click();
    await equipment.getByRole('link', { name: 'Editar' }).click();
    await expect(page.getByRole('img', { name: 'Fotografia de Pagaia E2E atualizada' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Histórico do equipamento' })).toBeVisible();
    await expect(page.getByText('Equipamento atualizado')).toBeVisible();
    await expect(page.getByText('Fotografia atualizada')).toBeVisible();
    await expect(page.getByText('Equipamento criado')).toBeVisible();
    await page.getByRole('link', { name: 'Voltar à frota' }).click();

    await page.locator('#maintenance-form > summary').click();
    const maintenance = page.locator('#maintenance-form');
    await maintenance.getByLabel('Equipamento').selectOption({ label: `${updatedTag} - Pagaia E2E atualizada` });
    await maintenance.getByLabel('Data e hora').fill(new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
    await maintenance.getByLabel('Descrição').fill(maintenanceDescription);
    await maintenance.getByRole('button', { name: 'Agendar manutenção' }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByText(maintenanceDescription)).toBeVisible();

    equipment = page.getByRole('row', { name: new RegExp(updatedTag) });
    await equipment.getByText('Ações').click();
    await equipment.getByRole('button', { name: 'Retirar da frota' }).click();
    await expect(page.getByRole('status')).toContainText('Equipamento retirado da frota');
    await expect(page.getByText(maintenanceDescription)).toHaveCount(0);

    await page.goto(equipmentEditURL);
    await expect(page.getByText('Equipamento retirado')).toBeVisible();
    await expect(page.getByText('1 tarefa de manutenção ativa foi cancelada.')).toBeVisible();
    await page.getByRole('link', { name: 'Voltar à frota' }).click();

    for (let equipmentPage = 0; equipmentPage < 50; equipmentPage += 1) {
      equipment = page.getByRole('row', { name: new RegExp(updatedTag) });
      if (await equipment.count()) break;
      const next = page.getByRole('navigation', { name: 'Paginação de equipamentos' }).getByRole('link', { name: 'Seguinte' });
      await expect(next, `equipment ${updatedTag} was not found in the paginated inventory`).toBeVisible();
      await next.click();
    }
    await equipment.getByText('Ações').click();
    await equipment.getByRole('button', { name: 'Reativar' }).click();
    await expect(page.getByRole('status')).toHaveText('Equipamento reativado como operacional.');
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
    await context.close();
  });

  test('administrator assigns a competition membership that unlocks the athlete workspace', async ({ page }) => {
    test.setTimeout(240000);
    const athleteName = `Atleta E2E ${Date.now()}`;
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await page.getByRole('link', { name: 'Membros', exact: true }).click();
    await expect(page).toHaveURL('/admin/membros');
    await page.getByRole('link', { name: 'Criar conta', exact: true }).click();
    await page.locator('#member-name').fill(athleteName);
    await page.locator('#member-email').fill(athleteEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page.getByText('Conta criada.', { exact: true })).toBeVisible();
    await page.getByLabel('Pesquisar membros').fill(athleteName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await expect(page).toHaveURL(new RegExp('/admin/membros\\?q='));

    await page.getByRole('link', { name: athleteName }).click();
    const location = page.getByRole('navigation', { name: 'Localização atual' });
    await expect(location.getByRole('link', { name: 'Membros' })).toHaveAttribute('href', '/admin/membros');
    await expect(location.getByText('Detalhe do membro')).toHaveAttribute('aria-current', 'page');
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await membershipForm.getByLabel('Competição').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();
    await expect(membershipForm.getByLabel('Competição')).toBeChecked();

    await page.getByRole('link', { name: 'Abrir perfil', exact: true }).click();
    await page.getByLabel('Número de atleta FPC').fill('27044');
    await page.getByRole('button', { name: 'Guardar perfil' }).click();
    await expect(page.getByText('Perfil atualizado.', { exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Ver histórico nacional na FPC' })).toHaveAttribute('href', 'https://www.fpcanoagem.pt/resultados/verhistorico/27044/');
    await expect(page.getByRole('link', { name: 'Ver histórico internacional na FPC' })).toHaveAttribute('href', 'https://www.fpcanoagem.pt/resultados/verhistoricointernational/27044/');
    await expectNoSeriousAxeViolations(page);

    const planTitle = `Plano classificação E2E ${Date.now()}`;
    const sessionTitle = `Sessão classificação E2E ${Date.now()}`;
    const cancelledSessionTitle = `Sessão cancelada E2E ${Date.now()}`;
    const editedCancelledSessionTitle = `${cancelledSessionTitle} editada`;
    const cancellationReason = 'Alteração do calendário de treinos E2E.';
    await page.goto('/admin/treinos');
    await expect(page.getByRole('link', { name: 'Publicar documento de competição', exact: true })).toHaveCount(0);
    await expect(page.locator('#criar-plano')).not.toHaveAttribute('open', '');
    await page.getByRole('link', { name: 'Criar plano', exact: true }).click();
    const planForm = page.locator('form[action="/admin/treinos/planos"]');
    await planForm.getByLabel('Título').fill(planTitle);
    await planForm.getByLabel('Programa').selectOption({ label: 'Competição' });
    await planForm.getByRole('button', { name: 'Criar plano' }).click();
    await expect(page.getByText('Plano criado.', { exact: true })).toBeVisible();

    await page.getByRole('link', { name: 'Criar sessão', exact: true }).click();
    const sessionForm = page.locator('form[action="/admin/treinos/sessoes"]');
    const sessionStart = new Date(Date.now() - 6 * 60 * 60 * 1000);
    const sessionEnd = new Date(Date.now() - 5 * 60 * 60 * 1000);
    await sessionForm.getByLabel('Plano').selectOption({ label: planTitle });
    await sessionForm.getByLabel('Título').fill(sessionTitle);
    await sessionForm.getByLabel('Início').fill(sessionStart.toISOString().slice(0, 16));
    await sessionForm.getByLabel('Fim').fill(sessionEnd.toISOString().slice(0, 16));
    await sessionForm.getByRole('button', { name: 'Criar sessão' }).click();
    await expect(page.getByText('Sessão criada.', { exact: true })).toBeVisible();

    await page.getByRole('link', { name: 'Criar sessão', exact: true }).click();
    const futureStart = new Date(Date.now() + 48 * 60 * 60 * 1000);
    const futureEnd = new Date(futureStart.getTime() + 90 * 60 * 1000);
    await sessionForm.getByLabel('Plano').selectOption({ label: planTitle });
    await sessionForm.getByLabel('Título').fill(cancelledSessionTitle);
    await sessionForm.getByLabel('Início').fill(futureStart.toISOString().slice(0, 16));
    await sessionForm.getByLabel('Fim').fill(futureEnd.toISOString().slice(0, 16));
    await sessionForm.getByRole('button', { name: 'Criar sessão' }).click();
    const managedFutureSession = page.getByRole('heading', { name: cancelledSessionTitle }).locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await managedFutureSession.getByRole('link', { name: 'Editar sessão' }).click();
    await expectNoSeriousFormAxeViolations(page);
    await page.getByLabel('Título').fill(editedCancelledSessionTitle);
    await page.getByRole('button', { name: 'Guardar alterações' }).click();
    await expect(page.getByText('Sessão atualizada.', { exact: true })).toBeVisible();
    await page.getByRole('heading', { name: editedCancelledSessionTitle }).locator('xpath=ancestor::div[contains(@class,"nested-record")][1]').getByRole('link', { name: 'Editar sessão' }).click();
    await page.getByLabel('Motivo').fill(cancellationReason);
    await page.getByLabel('Confirmo que pretendo cancelar esta sessão definitivamente.').check();
    await page.getByRole('button', { name: 'Cancelar sessão' }).click();
    await expect(page.getByText('Sessão cancelada.', { exact: true })).toBeVisible();
    const cancelledManagedSession = page.getByRole('heading', { name: editedCancelledSessionTitle }).locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await expect(cancelledManagedSession).toContainText('Sessão cancelada');
    await expect(cancelledManagedSession).toContainText(cancellationReason);

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await expect(page).toHaveURL('/login');
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Competição', exact: true }).click();
    await expect(page).toHaveURL('/dashboard/competition');
    await expect(page.getByRole('heading', { name: 'Competição', exact: true })).toBeVisible();

    await page.goto('/perfil');
    const nationalFPCLink = page.getByRole('link', { name: 'Ver histórico nacional na FPC' });
    const internationalFPCLink = page.getByRole('link', { name: 'Ver histórico internacional na FPC' });
    await expect(nationalFPCLink).toHaveAttribute('href', 'https://www.fpcanoagem.pt/resultados/verhistorico/27044/');
    await expect(internationalFPCLink).toHaveAttribute('href', 'https://www.fpcanoagem.pt/resultados/verhistoricointernational/27044/');
    await expect(nationalFPCLink).not.toHaveAttribute('target', '_blank');
    await expect(internationalFPCLink).not.toHaveAttribute('target', '_blank');

    await page.goto('/treinos');
    const cancelledAthleteSession = page.getByRole('heading', { name: new RegExp(`${editedCancelledSessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await expect(cancelledAthleteSession).toContainText('Cancelada');
    await expect(cancelledAthleteSession).toContainText(cancellationReason);
    await expect(cancelledAthleteSession.getByRole('button')).toHaveCount(0);
    const athleteSession = page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await athleteSession.locator('summary').filter({ hasText: 'Marcar concluída' }).click();
    await athleteSession.getByLabel('Distância (km)').fill('12.34');
    await athleteSession.getByRole('button', { name: 'Concluir sessão' }).click();
    await expect(page.getByText('Resultado registado.', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]')).toContainText('12,34 km');

    await page.goto('/today?leaderboard_period=all');
    const leaderboard = page.locator('#leaderboard');
    await expect(leaderboard).toContainText(athleteName);
    await expect(leaderboard).toContainText('12,34 km');
    await page.getByLabel('Mostrar os meus quilómetros na classificação').uncheck();
    await page.getByRole('button', { name: 'Guardar privacidade' }).click();
    await expect(page.getByText('Privacidade da classificação atualizada.', { exact: true })).toBeVisible();
    await expect(leaderboard).toContainText('Os seus totais estão privados');
    await expect(leaderboard).not.toContainText(athleteName);
  });

  test('administrator manages event capacity, waitlist confirmation, and check-in', async ({ page }) => {
    test.setTimeout(240000);
    const waitlistedName = `Lista de espera E2E ${Date.now()}`;
    const futureTitle = `Evento com lotação E2E ${Date.now()}`;
    const editedDescription = 'Evento editado antes das respostas, mantendo o documento oficial.';
    const cancellationReason = 'Cancelado pelo teste de ciclo de vida E2E.';
    const pastTitle = `Evento com presença E2E ${Date.now()}`;
    const futureStart = new Date(Date.now() + 48 * 60 * 60 * 1000);
    const futureEnd = new Date(futureStart.getTime() + 2 * 60 * 60 * 1000);
    const pastStart = new Date(Date.now() - 48 * 60 * 60 * 1000);
    const pastEnd = new Date(pastStart.getTime() + 2 * 60 * 60 * 1000);
    const asDateTimeLocal = (value) => value.toISOString().slice(0, 16);

    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Membros', exact: true }).click();
    await page.getByRole('link', { name: 'Criar conta', exact: true }).click();
    await page.locator('#member-name').fill(waitlistedName);
    await page.locator('#member-email').fill(waitlistedEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Pesquisar membros').fill(waitlistedName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: waitlistedName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await membershipForm.getByLabel('Competição').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();

    await page.goto('/admin/eventos');
    await page.getByRole('link', { name: 'Criar evento', exact: true }).click();
    await page.locator('#event-title').fill('E');
    await page.locator('#event-description').fill('Valores seguros devem permanecer após validação.');
    await page.locator('#event-type').selectOption('COMPETITION');
    await page.locator('#event-starts-at').fill(asDateTimeLocal(futureEnd));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(futureStart));
    await page.getByText('Destinatários (opcional)', { exact: true }).click();
    await page.getByLabel('Competição').check();
    await page.getByRole('button', { name: 'Criar evento' }).click();
    await expect(page.locator('.error-summary')).toBeFocused();
    await expect(page.locator('#event-title')).toHaveValue('E');
    await expect(page.locator('#event-description')).toHaveValue('Valores seguros devem permanecer após validação.');
    await expect(page.getByLabel('Competição')).toBeChecked();

    await page.locator('#event-title').fill(futureTitle);
    await page.locator('#event-description').fill('Evento de teste para confirmar lotação e lista de espera.');
    await page.locator('#event-type').selectOption('COMPETITION');
    await page.locator('#event-starts-at').fill(asDateTimeLocal(futureStart));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(futureEnd));
    await page.locator('#event-capacity').fill('1');
    await page.getByText('Documento de competição (opcional)', { exact: true }).click();
    await page.locator('#event-document-title').fill('Caderno de prova E2E');
    await page.locator('#event-document-url').fill('https://example.test/caderno-e2e.pdf');
    await page.locator('#event-document-source').fill('Federação Portuguesa de Canoagem');
    await page.locator('#event-document-reviewed-on').fill(new Date().toISOString().slice(0, 10));
    await page.getByRole('button', { name: 'Criar evento' }).click();
    await expect(page.getByRole('status')).toHaveText('Evento criado.');
    await page.getByRole('link', { name: futureTitle, exact: true }).click();
    await expect(page.getByRole('navigation', { name: 'Localização atual' }).getByRole('link', { name: 'Eventos' })).toHaveAttribute('href', '/admin/eventos');
    const futureAdminEventURL = page.url();
    const futureMemberEventURL = futureAdminEventURL.replace('/admin/eventos/', '/events/');
    await page.getByRole('link', { name: 'Editar evento', exact: true }).click();
    await page.locator('#event-description').fill(editedDescription);
    await page.getByRole('button', { name: 'Guardar alterações' }).click();
    await expect(page.getByRole('status')).toHaveText('Evento atualizado.');
    await expect(page.getByText(editedDescription)).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await expect(page.getByRole('heading', { name: 'Cadernos de prova e documentos oficiais' })).toBeVisible();
    await page.getByRole('link', { name: futureTitle, exact: true }).click();
    await expect(page.getByRole('link', { name: 'Caderno de prova E2E' })).toBeVisible();
    await page.getByRole('button', { name: 'Vou', exact: true }).click();
    await expect(page.getByText('Estado: Vou')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(waitlistedEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle, exact: true }).click();
    await page.getByRole('button', { name: 'Vou', exact: true }).click();
    await expect(page.getByText('Estado: Em lista de espera')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle, exact: true }).click();
    await page.getByRole('button', { name: 'Não vou' }).click();
    await expect(page.getByText('Estado: Não vou')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/eventos');
    await page.getByRole('link', { name: futureTitle, exact: true }).click();
    await page.locator('li', { hasText: waitlistedName }).getByText('Ações', { exact: true }).click();
    await page.getByRole('button', { name: 'Confirmar vaga' }).click();
    await expect(page.locator('li', { hasText: waitlistedName })).toContainText('Vou');
    await page.getByRole('link', { name: 'Editar evento', exact: true }).click();
    await page.locator('#event-cancellation-reason').fill(cancellationReason);
    await page.locator('#confirm-event-cancellation').check();
    await page.getByRole('button', { name: 'Cancelar evento' }).click();
    await expect(page.locator('.status-message')).toHaveText('Evento cancelado.');
    await expect(page.getByText('Cancelado', { exact: true })).toBeVisible();
    await expect(page.getByText(cancellationReason)).toBeVisible();

    await page.goto('/admin/eventos');
    await page.getByRole('link', { name: 'Criar evento', exact: true }).click();
    await page.locator('#event-title').fill(pastTitle);
    await page.locator('#event-description').fill('Evento de teste para registar uma presença após o início.');
    await page.locator('#event-starts-at').fill(asDateTimeLocal(pastStart));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(pastEnd));
    await page.getByRole('button', { name: 'Criar evento' }).click();
    while (await page.getByRole('link', { name: pastTitle, exact: true }).count() === 0) {
      await page.getByRole('link', { name: 'Seguinte' }).click();
    }
    const pastAdminEventURL = await page.getByRole('link', { name: pastTitle, exact: true }).getAttribute('href');
    expect(pastAdminEventURL).not.toBeNull();
    const pastMemberEventURL = pastAdminEventURL?.replace('/admin/eventos/', '/events/');

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(waitlistedEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto(futureMemberEventURL);
    await expect(page.getByText('Cancelado', { exact: true })).toBeVisible();
    await expect(page.getByText(cancellationReason)).toBeVisible();
    await expect(page.getByRole('button', { name: 'Vou', exact: true })).toHaveCount(0);
    await page.goto(pastMemberEventURL ?? '/events');
    await page.getByRole('button', { name: 'Vou', exact: true }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto(pastAdminEventURL);
    await page.locator('li', { hasText: waitlistedName }).getByText('Ações', { exact: true }).click();
    await page.getByRole('button', { name: 'Registar presença' }).click();
    await expect(page.locator('li', { hasText: waitlistedName })).toContainText('Presença:');
  });

  test('administrator publishes a targeted announcement and the athlete reads it', async ({ page }) => {
    test.setTimeout(180000);
    const title = `Aviso de competição E2E ${Date.now()}`;
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/avisos');
    await page.getByRole('link', { name: 'Criar aviso', exact: true }).click();
    await page.locator('#announcement-title').fill(title);
    await page.locator('#announcement-body').fill('Aviso dirigido aos atletas de competição.');
    await page.locator('summary').filter({ hasText: 'Contexto e destinatários' }).click();
    await page.getByLabel('Competição').check();
    await page.getByRole('button', { name: 'Publicar' }).click();
    await expect(page.getByText(title)).toContainText('PUBLISHED');

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    const bell = page.getByRole('link', { name: /^Avisos, \d+ avisos? por ler$/ });
    await expect(bell).toBeVisible();
    await bell.click();
    await expect(bell).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('Escape');
    await expect(bell).toBeFocused();
    await expect(bell).toHaveAttribute('aria-expanded', 'false');
    await bell.click();
    const announcement = page.getByRole('dialog', { name: 'Avisos' }).locator('li', { hasText: title });
    await expect(announcement).toContainText('Por ler');
    await announcement.getByRole('link', { name: title }).click();
    await expect(page.getByRole('heading', { name: title })).toBeVisible();

    await page.goto('/today');
    await page.getByRole('link', { name: /^Avisos/ }).click();
    const readPanel = page.getByRole('dialog', { name: 'Avisos' });
    await expect(readPanel.locator('li', { hasText: title })).not.toContainText('Por ler');
    await readPanel.getByRole('button', { name: 'Fechar avisos' }).click();
    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/avisos');
    await page.locator('li', { hasText: title }).getByText('Ações', { exact: true }).click();
    await page.locator('li', { hasText: title }).getByRole('button', { name: 'Expirar' }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: /^Avisos/ }).click();
    await expect(page.getByRole('dialog', { name: 'Avisos' }).getByRole('link', { name: title })).toHaveCount(0);
  });

  test('administrator publishes news and confirms member deactivation without JavaScript', async ({ page, browser }) => {
    test.setTimeout(120000);
    const memberName = `Lazer E2E ${Date.now()}`;
    const title = `Notícia E2E ${Date.now()}`;
    const publishedAt = new Date(Date.now() - 60 * 60 * 1000).toISOString().slice(0, 16);
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Membros', exact: true }).click();
    await page.getByRole('link', { name: 'Criar conta', exact: true }).click();
    await page.locator('#member-name').fill(memberName);
    await page.locator('#member-email').fill(leisureEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Pesquisar membros').fill(memberName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: memberName }).click();
    const memberships = page.locator('details').filter({ has: page.getByText('Inscrições ativas', { exact: false }) });
    await memberships.locator('summary').click();
    const membershipForm = memberships.locator('form').filter({
      has: page.getByRole('checkbox', { name: 'Lazer', exact: true }),
    });
    await membershipForm.getByRole('checkbox', { name: 'Lazer', exact: true }).check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('link', { name: 'Notícias' }).click();
    await page.getByRole('link', { name: 'Criar notícia', exact: true }).click();
    await page.locator('#news-title').fill(title);
    await page.locator('#news-summary').fill('Notícia publicada no painel de lazer.');
    await page.locator('#news-published-at').fill(publishedAt);
    await page.getByRole('button', { name: 'Guardar rascunho' }).click();
    const item = page.locator('li', { hasText: title });
    await item.getByRole('button', { name: 'Publicar' }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(leisureEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Lazer', exact: true }).click();
    await expect(page.getByText(title)).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Notícias' }).click();
    await page.locator('li', { hasText: title }).getByRole('button', { name: 'Expirar' }).click();

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/login');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill(adminEmail);
    await noJavaScriptPage.getByLabel('Palavra-passe').fill(password);
    await noJavaScriptPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await noJavaScriptPage.getByRole('link', { name: 'Membros', exact: true }).click();
    await noJavaScriptPage.getByLabel('Pesquisar membros').fill(memberName);
    await noJavaScriptPage.getByRole('button', { name: 'Procurar' }).click();
    await noJavaScriptPage.getByRole('link', { name: memberName }).click();
    await noJavaScriptPage.getByText('Desativar conta', { exact: true }).click();
    const deactivate = noJavaScriptPage.getByRole('button', { name: `Desativar conta de ${memberName}` });
    await expect(deactivate).toBeVisible();
    await expect(noJavaScriptPage.getByRole('link', { name: 'Cancelar' }).last()).toHaveAttribute('href', /\/admin\/membros\//);
    await noJavaScriptPage.getByLabel(`Confirmo que pretendo desativar a conta de ${memberName}.`).check();
    await deactivate.click();
    await expect(noJavaScriptPage.getByRole('status')).toHaveText('Conta desativada.');
    const accountModule = noJavaScriptPage.locator('.module').filter({ has: noJavaScriptPage.getByRole('heading', { name: 'Identidade e acesso' }) });
    await expect(accountModule.getByText('Desativada', { exact: true })).toBeVisible();
    await context.close();
  });
});
