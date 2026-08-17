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
  test.describe.configure({ mode: 'parallel' });

  test('renders an accessible public landing page with working calls to action', async ({ page }) => {
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

  });

  test('requests password recovery and handles invalid reset links accessibly', async ({ page }) => {
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

    await page.goto('/recuperar-palavra-passe');
    await page.getByLabel('Correio eletrónico').fill('CFC-AB12CD34');
    await page.getByRole('button', { name: 'Enviar instruções' }).click();
    await expect(page.getByRole('heading', { name: 'Consulte o seu email' })).toBeVisible();
  });

  test.describe('member registration and repair journey', () => {
    test.describe.configure({ mode: 'serial' });

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

  test('logs in and navigates', async ({ browser }) => {
    const context = await browser.newContext({ baseURL });
    const page = await context.newPage();
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(email);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: /^Avisos/ }).click();
    await expect(page.getByRole('dialog', { name: 'Avisos' })).toBeVisible();
    await page.goto('/today');
    await page.getByRole('link', { name: 'Frota', exact: true }).click();
    await expect(page).toHaveURL('/fleet');

    await page.getByRole('link', { name: 'Reportar avaria' }).click();
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

  });

  test('member submits a private suggestion and receives the administrator response', async ({ page }) => {
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

  test('creates a dependent', async ({ page, browser }) => {
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

    const context = await browser.newContext({ baseURL });
    const interactivePage = await context.newPage();
    await interactivePage.goto('/login');
    await interactivePage.getByLabel('Correio eletrónico').fill(guardianEmail);
    await interactivePage.getByLabel('Palavra-passe').fill(password);
    await interactivePage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await interactivePage.getByRole('link', { name: 'Menores a cargo' }).click();
    await expect(interactivePage).toHaveURL('/dashboard/guardian');
    await interactivePage.getByRole('link', { name: 'Adicionar menor' }).click();

    await interactivePage.getByLabel('Nome').fill('X');
    await interactivePage.getByLabel('Data de nascimento').fill('2014-01-01');
    await interactivePage.getByLabel(/Aceito a responsabilidade pelo menor a cargo/).check();
    await interactivePage.getByRole('button', { name: 'Adicionar menor' }).click();
    await expect(interactivePage.locator('.error-summary')).toBeVisible();
    await expect(interactivePage.getByLabel('Nome')).toHaveValue('X');

    await interactivePage.getByLabel('Nome').fill('Menor de teste');
    await interactivePage.getByLabel('Data de nascimento').fill('2014-01-01');
    await interactivePage.getByLabel(/Aceito a responsabilidade pelo menor a cargo/).check();
    await interactivePage.getByRole('button', { name: 'Adicionar menor' }).click();

    await expect(interactivePage).toHaveURL('/dashboard/guardian');
    const dependentLink = interactivePage.getByRole('link', { name: 'Menor de teste' });
    await expect(dependentLink).toBeVisible();
    await dependentLink.click();
    await interactivePage.locator('#profile-emergency-name').fill('Guardião de teste');
    await interactivePage.locator('#profile-emergency-relationship').fill('Tutor');
    await interactivePage.locator('#profile-emergency-phone').fill('+351 930 000 000');
    await interactivePage.locator('#profile-medical-declaration').selectOption('NONE_KNOWN');
    await interactivePage.getByRole('button', { name: 'Guardar perfil' }).click();
    await expect(interactivePage.getByText('Perfil atualizado.')).toBeVisible();
    await interactivePage.getByRole('link', { name: 'Voltar aos menores' }).click();
    await expect(interactivePage.getByText(/Perfil incompleto/)).toHaveCount(0);
    await interactivePage.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(interactivePage);
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

  test('administrator manages equipment lifecycle and audit history', async ({ browser }) => {
    test.setTimeout(120000);
    const context = await browser.newContext({ baseURL });
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

    await page.getByRole('link', { name: 'Adicionar equipamento' }).click();
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

    await page.getByRole('link', { name: 'Agendar manutenção' }).click();
    const maintenance = page.locator('#maintenance-form');
    await maintenance.getByLabel('Equipamento').selectOption({ label: `${updatedTag} - Pagaia E2E atualizada` });
    await maintenance.getByLabel('Data e hora').fill(new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 16));
    await maintenance.getByLabel('Descrição').fill(maintenanceDescription);
    await maintenance.getByRole('button', { name: 'Agendar manutenção' }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await page.getByRole('tab', { name: /Manutenção/ }).click();
    await expect(page.getByText(maintenanceDescription)).toBeVisible();
    await page.locator('#maintenance-form').getByRole('button', { name: 'Fechar' }).click();
    await page.getByRole('tab', { name: 'Equipamentos', exact: true }).click();

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

  test.describe('athlete operations journey', () => {
    test.describe.configure({ mode: 'serial' });

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
    await athleteSession.getByLabel('Distância real (km)').fill('12.34');
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

  test('administrator authors a responsive hybrid training week', async ({ browser }) => {
    test.setTimeout(600000);
    const context = await browser.newContext({ baseURL });
    const page = await context.newPage();
    const suffix = Date.now();
    const structuredAthleteName = `Atleta estruturado E2E ${suffix}`;
    const structuredAthleteEmail = `e2e-structured-${suffix}@example.test`;
    const groupName = `Grupo estruturado E2E ${suffix}`;
    const intensityProfileName = `Perfil kayak E2E ${suffix}`;
    const weekTitle = `Microciclo E2E ${suffix}`;
    const sessionTitle = `Ginásio + água E2E ${suffix}`;
    const today = new Date();
    const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
    const daysSinceMonday = (monday.getDay() + 6) % 7;
    monday.setDate(monday.getDate() - daysSinceMonday);
    const formatDate = (value) => [value.getFullYear(), String(value.getMonth() + 1).padStart(2, '0'), String(value.getDate()).padStart(2, '0')].join('-');
    const startsAt = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 1, 17, 0);
    const endsAt = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 1, 19, 0);
    const nextMonday = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 7);
    const formatDateTime = (value) => `${formatDate(value)}T${String(value.getHours()).padStart(2, '0')}:${String(value.getMinutes()).padStart(2, '0')}`;

    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/membros');
    await page.getByRole('link', { name: 'Criar conta' }).click();
    await page.locator('#member-name').fill(structuredAthleteName);
    await page.locator('#member-email').fill(structuredAthleteEmail);
    await page.locator('#member-birth').fill('2005-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page.getByText('Conta criada.', { exact: true })).toBeVisible();
    await page.getByLabel('Pesquisar membros').fill(structuredAthleteName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: structuredAthleteName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const structuredMembershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await structuredMembershipForm.getByLabel('Competição').check();
    await structuredMembershipForm.getByRole('button', { name: 'Guardar' }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    await expect(page.locator('form').filter({ has: page.getByLabel('Competição') }).getByLabel('Competição')).toBeChecked();
    await page.goto('/admin/treinos/estruturados');

    await expect(page.getByRole('heading', { name: 'Planeamento semanal', level: 1 })).toBeVisible();
    await page.getByRole('tab', { name: 'Perfis de intensidade' }).click();
    await page.getByRole('button', { name: 'Criar perfil ou nova revisão' }).click();
    const profileForm = page.locator('form[action="/admin/treinos/estruturados/agua/perfis"]');
    await profileForm.getByLabel('Nome').fill(intensityProfileName);
    await profileForm.getByLabel('Embarcação').selectOption('KAYAK');
    await profileForm.getByLabel('Notas (opcional)').fill('Perfil do clube; R5 por confirmar');
    await profileForm.getByRole('button', { name: 'Guardar perfil' }).click();
    await expect(page.getByRole('status')).toHaveText('Nova revisão do perfil de intensidade criada.');
    await page.getByRole('tab', { name: 'Perfis de intensidade' }).click();
    const profileCard = page.getByRole('heading', { name: intensityProfileName }).locator('xpath=ancestor::article[1]');
    await profileCard.getByRole('button', { name: 'Adicionar ou atualizar zona' }).click();
    const zoneForm = profileCard.locator('form[action$="/zonas"]');
    await zoneForm.getByLabel('Código curto').fill('R7');
    await zoneForm.getByLabel('Nome completo').fill('Ritmo de prova');
    await zoneForm.getByLabel('Significado para o atleta').fill('Ritmo sustentável para a duração ou distância prescrita');
    await zoneForm.getByRole('button', { name: 'Guardar zona' }).click();
    await expect(page.getByRole('status')).toHaveText('Zona de intensidade adicionada.');
    await page.getByRole('tab', { name: 'Plano semanal' }).click();
    await page.getByRole('link', { name: 'Criar grupo' }).click();
    const groupForm = page.locator('form[action="/admin/treinos/estruturados/grupos"]');
    await groupForm.getByLabel('Nome').fill(groupName);
    await groupForm.getByLabel('Programa').selectOption({ label: 'Competição' });
    await groupForm.getByLabel(new RegExp(structuredAthleteName)).check();
    await groupForm.getByRole('button', { name: 'Criar grupo' }).click();
    await expect(page.getByRole('status')).toHaveText('Grupo de treino criado.');

    await page.getByRole('link', { name: 'Criar semana' }).click();
    const weekForm = page.locator('form[action="/admin/treinos/estruturados/semanas"]');
    await weekForm.getByLabel('Grupo').selectOption({ label: groupName });
    await weekForm.getByLabel('Título').fill(weekTitle);
    await weekForm.getByLabel('Segunda-feira da semana').fill(formatDate(monday));
    await weekForm.getByRole('button', { name: 'Criar semana' }).click();
    await expect(page.getByRole('status')).toHaveText('Semana de treino criada.');
    const week = page.getByRole('heading', { name: weekTitle }).locator('xpath=ancestor::section[1]');
    await expect(week).toContainText(`Época ${today.getFullYear()}`);

    await page.getByRole('link', { name: 'Criar sessão' }).click();
    const sessionForm = page.locator('form[action="/admin/treinos/estruturados/sessoes"]');
    const weekOption = sessionForm.getByLabel('Semana').locator('option').filter({ hasText: weekTitle });
    await sessionForm.getByLabel('Semana').selectOption(await weekOption.getAttribute('value'));
    await sessionForm.getByLabel('Título').fill(sessionTitle);
    await sessionForm.getByLabel('Início').fill(formatDateTime(startsAt));
    await sessionForm.getByLabel('Fim').fill(formatDateTime(endsAt));
    await sessionForm.getByRole('button', { name: 'Criar sessão' }).click();
    await expect(page.getByRole('status')).toHaveText('Sessão estruturada criada.');

    let session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    await session.getByRole('button', { name: 'Adicionar segmento' }).click();
    let segmentForm = session.locator('form[action$="/segmentos"]');
    await segmentForm.getByLabel('Modalidade').selectOption('GYM');
    await segmentForm.getByLabel('Título (opcional)').fill('Mobilidade');
    await segmentForm.getByLabel('Duração prevista em minutos (opcional)').fill('30');
    await segmentForm.getByLabel('Início previsto, em minutos após o início da sessão (opcional)').fill('0');
    await segmentForm.getByLabel('Material e preparação (opcional)').fill('Elásticos e halteres');
    await segmentForm.getByLabel('Objetivo do primeiro bloco').selectOption('WARM_UP');
    await segmentForm.getByLabel('Instruções').fill('5 min de mobilidade articular');
    await segmentForm.getByRole('button', { name: 'Adicionar segmento' }).click();
    await expect(page.getByRole('status')).toHaveText('Segmento adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    const gymSegment = session.getByRole('heading', { name: /Ginásio · Mobilidade/ }).locator('xpath=ancestor::section[1]');
    await expect(gymSegment).toContainText('Material: Elásticos e halteres');
    await gymSegment.getByText('Ações do segmento', { exact: true }).click();
    await gymSegment.getByRole('button', { name: 'Adicionar treino de ginásio' }).click();
    const gymBlockForm = gymSegment.locator('form[action$="/ginasio"]');
    await gymBlockForm.locator('select[name="purpose"]').selectOption('WARM_UP');
    await gymBlockForm.locator('input[name="title"]').fill('Supersérie de ativação');
    await gymBlockForm.locator('textarea[name="instructions"]').fill('Três voltas com execução cuidada');
    await gymBlockForm.locator('select[name="structure"]').selectOption('SUPERSET');
    await gymBlockForm.locator('select[name="objective"]').selectOption('ACTIVATION');
    await gymBlockForm.locator('input[name="rounds"]').fill('3');
    await gymBlockForm.locator('input[name="round_recovery_seconds"]').fill('120');
    await gymBlockForm.locator('input[name="exercise_name"]').fill('Supino');
    await gymBlockForm.locator('input[name="sets"]').fill('3');
    await gymBlockForm.locator('input[name="repetitions"]').fill('5');
    await gymBlockForm.locator('select[name="resistance_kind"]').selectOption('PERCENT_1RM');
    await gymBlockForm.locator('input[name="resistance_value"]').fill('75');
    await gymBlockForm.locator('select[name="execution_intent"]').selectOption('EXPLOSIVE');
    await gymBlockForm.locator('input[name="tempo"]').fill('2-0-X-1');
    await gymBlockForm.getByRole('button', { name: 'Adicionar bloco de ginásio' }).click();
    await expect(page.getByRole('status')).toHaveText('Bloco de ginásio adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    const gymBlock = session.getByRole('button', { name: 'Adicionar exercício' }).first().locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await expect(gymBlock).toContainText('Supersérie · Ativação · 3 voltas');
    await expect(gymBlock).toContainText('Supino');
    await expect(gymBlock).toContainText('75% de 1RM');
    await gymBlock.getByRole('button', { name: 'Adicionar exercício' }).click();
    const exerciseForm = gymBlock.locator('form[action$="/exercicios"]');
    await exerciseForm.locator('input[name="exercise_name"]').fill('Prancha');
    await exerciseForm.locator('input[name="duration_seconds"]').fill('45');
    await exerciseForm.locator('select[name="resistance_kind"]').selectOption('BODY_WEIGHT');
    await exerciseForm.locator('select[name="execution_intent"]').selectOption('ISOMETRIC');
    await exerciseForm.getByRole('button', { name: 'Adicionar exercício' }).click();
    await expect(page.getByRole('status')).toHaveText('Exercício adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    await session.getByRole('button', { name: 'Adicionar segmento' }).click();
    segmentForm = session.locator('form[action$="/segmentos"]');
    await segmentForm.getByLabel('Modalidade').selectOption('WATER');
    await segmentForm.getByLabel('Título (opcional)').fill('Ataque 3 e defesa 2-2');
    await segmentForm.getByLabel('Início previsto, em minutos após o início da sessão (opcional)').fill('35');
    await segmentForm.getByLabel('Transição antes deste segmento, em minutos (opcional)').fill('5');
    await segmentForm.getByLabel('Objetivo do primeiro bloco').selectOption('MAIN');
    await segmentForm.getByLabel('Instruções').fill("2x7' jogo · HxH · GR e pivot");
    await segmentForm.getByRole('button', { name: 'Adicionar segmento' }).click();
    await expect(page.getByRole('status')).toHaveText('Segmento adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    await expect(session).toContainText('Ginásio + Água');
    await expect(session).toContainText('Prancha');

    let structuredWaterSegment = session.getByRole('heading', { name: /Água · Ataque 3 e defesa 2-2/ }).locator('xpath=ancestor::section[1]');
    await structuredWaterSegment.getByText('Ações do segmento', { exact: true }).click();
    await structuredWaterSegment.getByRole('button', { name: 'Adicionar treino de água' }).click();
    const waterBlockForm = structuredWaterSegment.locator('form[action$="/agua"]');
    await waterBlockForm.locator('select[name="purpose"]').selectOption('MAIN');
    await waterBlockForm.locator('input[name="title"]').fill('Série intervalada');
    await waterBlockForm.locator('textarea[name="instructions"]').fill('Estrutura aninhada com recuperação ao nível certo');
    await waterBlockForm.locator('select[name="method"]').selectOption('INTERVALS');
    await waterBlockForm.getByLabel('Perfil de intensidade (opcional)').selectOption({ label: `${intensityProfileName} · Kayak · revisão 1` });
    await waterBlockForm.getByLabel('Distância total alvo da sessão (m)').fill('12000');
    await waterBlockForm.locator('select[name="target_distance_certainty"]').selectOption('ESTIMATED');
    await waterBlockForm.getByLabel('Tipo').selectOption('REPEAT_GROUP');
    await waterBlockForm.getByLabel('Nome').fill('Série exterior');
    await waterBlockForm.getByLabel('Repetições (para grupo)').fill('3');
    await waterBlockForm.getByLabel('Recuperação (s)').fill('180');
    await waterBlockForm.getByRole('button', { name: 'Adicionar bloco de água' }).click();
    await expect(page.getByRole('status')).toHaveText('Bloco de água adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    let waterBlock = session.getByRole('button', { name: 'Adicionar passo' }).last().locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await waterBlock.getByRole('button', { name: 'Adicionar passo' }).click();
    let waterStepForm = waterBlock.locator('form[action$="/agua/passos"]');
    await waterStepForm.getByLabel('Dentro de um grupo (opcional)').selectOption({ label: 'Série exterior' });
    await waterStepForm.getByLabel('Tipo').selectOption('REPEAT_GROUP');
    await waterStepForm.getByLabel('Nome').fill('Série interior');
    await waterStepForm.getByLabel('Repetições (para grupo)').fill('3');
    await waterStepForm.getByLabel('Recuperação (s)').fill('60');
    await waterStepForm.getByRole('button', { name: 'Adicionar passo' }).click();
    await expect(page.getByRole('status')).toHaveText('Passo de água adicionado.');

    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    waterBlock = session.getByRole('button', { name: 'Adicionar passo' }).last().locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await waterBlock.getByRole('button', { name: 'Adicionar passo' }).click();
    waterStepForm = waterBlock.locator('form[action$="/agua/passos"]');
    await waterStepForm.getByLabel('Dentro de um grupo (opcional)').selectOption({ label: 'Série interior' });
    await waterStepForm.getByLabel('Tipo').selectOption('EFFORT');
    await waterStepForm.getByLabel('Nome').fill('Três minutos R7');
    await waterStepForm.getByLabel('Duração (s)').fill('180');
    await waterStepForm.getByLabel('Precisão da duração').selectOption('EXACT');
    await waterStepForm.getByLabel('Distância (m)').fill('500');
    await waterStepForm.getByLabel('Precisão da distância').selectOption('ESTIMATED');
    await waterStepForm.getByLabel('Intensidade').fill('R7');
    await waterStepForm.getByRole('button', { name: 'Adicionar passo' }).click();
    await expect(page.getByRole('status')).toHaveText('Passo de água adicionado.');
    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    waterBlock = session.getByRole('button', { name: 'Adicionar passo' }).last().locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await expect(waterBlock).toContainText('27 min');
    await expect(waterBlock).toContainText('12 min');
    await expect(waterBlock).toContainText('4,5 km');
    await expect(waterBlock).toContainText('Ritmo sustentável para a duração ou distância prescrita');

    const reusableBlockName = `Ativação reutilizável ${suffix}`;
    let reusableGymBlock = session.getByRole('button', { name: 'Adicionar exercício' }).first().locator('xpath=ancestor::div[contains(@class,"nested-record")][1]');
    await reusableGymBlock.getByText('Ações do bloco', { exact: true }).click();
    await reusableGymBlock.getByRole('button', { name: 'Guardar como rotina' }).click();
    const saveRoutineForm = reusableGymBlock.locator('form[action="/admin/treinos/estruturados/rotinas"]');
    await saveRoutineForm.getByLabel('Nome da rotina').fill(reusableBlockName);
    await saveRoutineForm.getByLabel('Método (opcional)').fill('Ativação pré-água');
    await saveRoutineForm.getByLabel('Etiquetas separadas por vírgula (opcional)').fill('ginásio, aquecimento');
    await saveRoutineForm.getByRole('button', { name: 'Guardar rotina' }).click();
    await expect(page.getByRole('status')).toHaveText('Rotina guardada como cópia independente.');

    await page.getByRole('tab', { name: 'Rotinas' }).click();
    const routineLibrary = page.getByRole('heading', { name: 'Biblioteca de rotinas' }).locator('xpath=ancestor::section[1]');
    await routineLibrary.getByLabel('Etiqueta').fill('ginásio');
    await routineLibrary.getByRole('button', { name: 'Filtrar rotinas' }).click();
    const routineCard = page.getByRole('heading', { name: reusableBlockName }).locator('xpath=ancestor::article[1]');
    await expect(routineCard).toContainText('Pré-visualização: Supersérie de ativação');
    await routineCard.getByRole('button', { name: 'Usar rotina' }).click();
    const insertRoutineForm = routineCard.locator('form[action$="/inserir"]');
    const gymTarget = insertRoutineForm.getByLabel('Segmento de destino').locator('option').filter({ hasText: `${sessionTitle} · Ginásio · Mobilidade` });
    await insertRoutineForm.getByLabel('Segmento de destino').selectOption(await gymTarget.getAttribute('value'));
    await insertRoutineForm.getByRole('button', { name: 'Inserir cópia independente' }).click();
    await expect(page.getByRole('status')).toHaveText('Rotina inserida como cópia independente.');
    session = page.getByRole('heading', { name: sessionTitle }).locator('xpath=ancestor::article[1]');
    await expect(session.getByText('Supino', { exact: true })).toHaveCount(2);

    const copiedWeekTitle = `Microciclo copiado E2E ${suffix}`;
    const sourceWeek = page.getByRole('heading', { name: weekTitle }).locator('xpath=ancestor::section[1]');
    await sourceWeek.getByRole('button', { name: 'Copiar semana' }).click();
    const copyWeekForm = sourceWeek.locator('input[name="week_start"]').locator('xpath=ancestor::form[1]');
    await copyWeekForm.getByLabel('Título da nova semana').fill(copiedWeekTitle);
    await copyWeekForm.getByLabel('Nova segunda-feira').fill(formatDate(nextMonday));
    await copyWeekForm.getByRole('button', { name: 'Criar cópia da semana' }).click();
    await expect(page.getByRole('status')).toHaveText('Semana copiada como novo rascunho independente.');
    const copiedWeek = page.getByRole('heading', { name: copiedWeekTitle }).locator('xpath=ancestor::section[1]');
    await expect(copiedWeek).toContainText(sessionTitle);
    await expect(copiedWeek).toContainText('Prancha');

    const sourceWeekToggle = sourceWeek.locator(':scope > .training-card__header').getByRole('button');
    if ((await sourceWeekToggle.getAttribute('aria-expanded')) === 'false') await sourceWeekToggle.click();
    session = sourceWeek.locator('article.training-card--session').filter({ hasText: sessionTitle });
    const sourceSessionToggle = session.locator(':scope > .training-card__header').getByRole('button');
    if ((await sourceSessionToggle.getAttribute('aria-expanded')) === 'false') await sourceSessionToggle.click();
    await expect(session.getByRole('heading', { name: /Ginásio · Mobilidade/ })).toBeVisible();
    const waterSegment = session.getByRole('heading', { name: /Água · Ataque 3 e defesa 2-2/ }).locator('xpath=ancestor::section[1]');
    await expect(waterSegment).toContainText("2x7' jogo · HxH · GR e pivot");
    await waterSegment.getByText('Ações do segmento', { exact: true }).click();
    await waterSegment.locator('.record-item__actions').last().getByRole('button', { name: 'Subir segmento' }).click();
    session = sourceWeek.locator('article.training-card--session').filter({ hasText: sessionTitle });
    await expect(session.getByRole('heading', { name: /Água · Ataque 3 e defesa 2-2/ })).toBeVisible();

    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
    await expect(sourceWeek.getByRole('heading', { name: sessionTitle })).toBeVisible();
    await context.close();

    const accessibilityContext = await browser.newContext({ baseURL });
    const accessibilityPage = await accessibilityContext.newPage();
    await accessibilityPage.goto('/login');
    await accessibilityPage.getByLabel('Correio eletrónico').fill(adminEmail);
    await accessibilityPage.getByLabel('Palavra-passe').fill(password);
    await accessibilityPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await accessibilityPage.goto('/admin/treinos/estruturados');
    const accessibleSourceWeek = accessibilityPage.getByRole('heading', { name: weekTitle }).locator('xpath=ancestor::section[1]');
    const accessibleWeekToggle = accessibleSourceWeek.getByRole('button', { name: 'Mostrar' }).first();
    if (await accessibleWeekToggle.isVisible()) await accessibleWeekToggle.click();
    await expect(accessibleSourceWeek.getByRole('heading', { name: sessionTitle })).toBeVisible();
    await expectNoSeriousAxeViolations(accessibilityPage);
    await accessibilityContext.close();
  });

  test('administrator publishes an immutable private training prescription', async ({ page }) => {
    test.setTimeout(600000);
    const suffix = Date.now();
    const athleteName = `Atleta publicação E2E ${suffix}`;
    const athleteEmail = `e2e-publication-${suffix}@example.test`;
    const groupName = `Grupo publicação E2E ${suffix}`;
    const weekTitle = `Semana publicação E2E ${suffix}`;
    const sessionTitle = `Sessão publicação E2E ${suffix}`;
    const today = new Date();
    const monday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
    monday.setDate(monday.getDate() - ((monday.getDay() + 6) % 7));
    monday.setDate(monday.getDate() + 7);
    const formatDate = (value) => [value.getFullYear(), String(value.getMonth() + 1).padStart(2, '0'), String(value.getDate()).padStart(2, '0')].join('-');
    const startsAt = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 1, 17, 0);
    const endsAt = new Date(startsAt.getTime() + 60 * 60 * 1000);
    const formatDateTime = (value) => `${formatDate(value)}T${String(value.getHours()).padStart(2, '0')}:${String(value.getMinutes()).padStart(2, '0')}`;

    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/admin/membros');
    await page.getByRole('link', { name: 'Criar conta', exact: true }).click();
    await page.locator('#member-name').fill(athleteName);
    await page.locator('#member-email').fill(athleteEmail);
    await page.locator('#member-birth').fill('2005-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Pesquisar membros').fill(athleteName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: athleteName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await membershipForm.getByLabel('Competição').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();

    await page.goto('/admin/treinos/estruturados');
    await page.getByRole('link', { name: 'Criar grupo' }).click();
    const groupForm = page.locator('form[action="/admin/treinos/estruturados/grupos"]');
    await groupForm.getByLabel('Nome').fill(groupName);
    await groupForm.getByLabel('Programa').selectOption({ label: 'Competição' });
    await groupForm.getByLabel(new RegExp(athleteName)).check();
    await groupForm.getByRole('button', { name: 'Criar grupo' }).click();
    await page.getByRole('link', { name: 'Criar semana' }).click();
    const weekForm = page.locator('form[action="/admin/treinos/estruturados/semanas"]');
    await weekForm.getByLabel('Grupo').selectOption({ label: groupName });
    await weekForm.getByLabel('Título').fill(weekTitle);
    await weekForm.getByLabel('Segunda-feira da semana').fill(formatDate(monday));
    await weekForm.getByRole('button', { name: 'Criar semana' }).click();
    await page.getByRole('link', { name: 'Criar sessão' }).click();
    const sessionForm = page.locator('form[action="/admin/treinos/estruturados/sessoes"]');
    const weekOption = sessionForm.getByLabel('Semana').locator('option').filter({ hasText: weekTitle });
    await sessionForm.getByLabel('Semana').selectOption(await weekOption.getAttribute('value'));
    await sessionForm.getByLabel('Título').fill(sessionTitle);
    await sessionForm.getByLabel('Início').fill(formatDateTime(startsAt));
    await sessionForm.getByLabel('Fim').fill(formatDateTime(endsAt));
    await sessionForm.getByRole('button', { name: 'Criar sessão' }).click();

    await page.getByRole('tab', { name: 'Publicar' }).click();
    let publicationCard = page.getByRole('heading', { name: weekTitle, exact: true }).locator('xpath=ancestor::article[1]');
    await expect(publicationCard).toContainText('Pré-visualização: 1 novas · 0 alteradas · 0 removidas · 0 sem alterações');
    await publicationCard.getByLabel('Resumo desta revisão').fill('Primeira publicação E2E');
    await publicationCard.getByRole('button', { name: 'Pré-visualizado · publicar revisão' }).click();
    await expect(page.getByRole('status')).toHaveText('Revisão 1 publicada para 1 prescrições privadas.');
    await page.getByRole('tab', { name: 'Publicar' }).click();
    publicationCard = page.getByRole('heading', { name: weekTitle, exact: true }).locator('xpath=ancestor::article[1]');
    await expect(publicationCard).toContainText('Publicado · sem alterações pendentes');
    await expect(publicationCard).toContainText('0 novas · 0 alteradas · 0 removidas · 1 sem alterações');

    await page.goto('/treinos/estruturados');
    const publishedSession = page.getByRole('heading', { name: sessionTitle, exact: true }).locator('xpath=ancestor::article[1]');
    await expect(page.getByText(athleteName, { exact: true })).toBeVisible();
    await publishedSession.getByRole('link', { name: 'Abrir esta revisão da prescrição' }).click();
    const prescriptionURL = page.url();
    await expect(page.getByText('Prescrição publicada · revisão 1', { exact: false })).toBeVisible();
    await expectNoSeriousAxeViolations(page);
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
    await expect(page.getByRole('heading', { name: sessionTitle })).toBeVisible();

    await page.setViewportSize({ width: 1280, height: 720 });
    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/treinos');
    let athleteSession = page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await athleteSession.locator('summary').filter({ hasText: 'Marcar concluída' }).click();
    await athleteSession.getByLabel('Duração real (minutos)').fill('58');
    await athleteSession.getByLabel('Distância real (km)').fill('8.2');
    await athleteSession.getByLabel('Esforço percebido (0–10)').selectOption('7');
    await athleteSession.getByLabel('Como se sentiu no conjunto? (1–5)').selectOption('4');
    await athleteSession.getByLabel('Nota sobre a sessão (opcional)').fill('Boa sessão, com vento lateral.');
    await athleteSession.getByRole('button', { name: 'Concluir sessão' }).click();
    await expect(page.getByText('Resultado registado.', { exact: true })).toBeVisible();
    athleteSession = page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await expect(athleteSession).toContainText('Duração real: 58 min');
    await expect(athleteSession).toContainText('Distância real: 8,2 km');
    await expect(athleteSession).toContainText('Esforço percebido: 7/10');
    await expect(athleteSession).toContainText('Boa sessão, com vento lateral.');
    await athleteSession.locator('summary').filter({ hasText: 'Registar ou corrigir dados reais e perceção' }).click();
    await athleteSession.getByLabel('Duração real (minutos)').fill('61');
    await athleteSession.getByLabel('Esforço percebido (0–10)').selectOption('8');
    await athleteSession.getByLabel('Nota sobre a sessão (opcional)').fill('Correção: regresso contra a corrente.');
    await athleteSession.getByRole('button', { name: 'Guardar dados reais e perceção' }).click();
    await expect(page.getByText('Dados reais e perceção atualizados.', { exact: true })).toBeVisible();
    athleteSession = page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await expect(athleteSession).toContainText('Duração real: 61 min');
    await expect(athleteSession).toContainText('Esforço percebido: 8/10');
    await expect(athleteSession).toContainText('Correção: regresso contra a corrente.');
    await expectNoSeriousAxeViolations(page);

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto(prescriptionURL);
    await expect(page.getByText('Resposta do atleta', { exact: false })).toBeVisible();
    await expect(page.getByText('Correção: regresso contra a corrente.')).toBeVisible();
    await expect(page.getByText('Esforço percebido: 8/10')).toBeVisible();
    await expectNoSeriousAxeViolations(page);
  });

  test('administrator creates a private album and the scoped athlete can open it', async ({ page, browser }) => {
    test.setTimeout(120000);
    const title = `Álbum competição E2E ${Date.now()}`;
    const context = await browser.newContext({ baseURL });
    const adminPage = await context.newPage();
    await adminPage.goto('/login');
    await adminPage.getByLabel('Correio eletrónico').fill(adminEmail);
    await adminPage.getByLabel('Palavra-passe').fill(password);
    await adminPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await adminPage.goto('/admin/albuns');
    await adminPage.getByRole('link', { name: 'Novo álbum', exact: true }).click();
    await adminPage.locator('#photo-album-title').fill(title);
    await adminPage.locator('#photo-album-description').fill('Espaço privado da equipa de competição.');
    await adminPage.locator('#photo-album-audience').getByLabel('Competição').check();
    await adminPage.getByRole('button', { name: 'Criar álbum' }).click();
    await expect(adminPage.getByRole('status')).toHaveText('Álbum privado criado.');
    await adminPage.getByRole('link', { name: title }).click();
    const adminDetailURL = adminPage.url();
    await expect(adminPage.getByRole('heading', { name: title })).toBeVisible();
    await expect(adminPage.getByText('Nenhuma fotografia publicada')).toBeVisible();
    await expect(adminPage.locator('input[type="file"]')).toHaveCount(0);

    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/albuns');
    await expect(page.getByRole('link', { name: title })).toBeVisible();
    await page.getByRole('link', { name: title }).click();
    await expect(page.getByText('Programas: Competição')).toBeVisible();
    await expect(page.locator('input[type="file"]')).toHaveCount(0);
    await expectNoSeriousAxeViolations(page);

    await adminPage.goto(adminDetailURL);
    await adminPage.getByRole('button', { name: 'Arquivar álbum' }).click();
    await expect(adminPage.getByRole('status')).toHaveText('Álbum arquivado.');
    await page.goto('/albuns');
    await expect(page.getByRole('link', { name: title })).toHaveCount(0);
    await context.close();
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

  });

  test('administrator publishes news and confirms member deactivation', async ({ page, browser }) => {
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

    const context = await browser.newContext({ baseURL });
    const interactivePage = await context.newPage();
    await interactivePage.goto('/login');
    await interactivePage.getByLabel('Correio eletrónico').fill(adminEmail);
    await interactivePage.getByLabel('Palavra-passe').fill(password);
    await interactivePage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await interactivePage.getByRole('link', { name: 'Membros', exact: true }).click();
    await interactivePage.getByLabel('Pesquisar membros').fill(memberName);
    await interactivePage.getByRole('button', { name: 'Procurar' }).click();
    await interactivePage.getByRole('link', { name: memberName }).click();
    await interactivePage.getByText('Desativar conta', { exact: true }).click();
    const deactivate = interactivePage.getByRole('button', { name: `Desativar conta de ${memberName}` });
    await expect(deactivate).toBeVisible();
    await expect(interactivePage.getByRole('link', { name: 'Cancelar' }).last()).toHaveAttribute('href', /\/admin\/membros\//);
    await interactivePage.getByLabel(`Confirmo que pretendo desativar a conta de ${memberName}.`).check();
    await deactivate.click();
    await expect(interactivePage.getByRole('status')).toHaveText('Conta desativada.');
    const accountModule = interactivePage.locator('.module').filter({ has: interactivePage.getByRole('heading', { name: 'Identidade e acesso' }) });
    await expect(accountModule.getByText('Desativada', { exact: true })).toBeVisible();
    await context.close();
  });
});
