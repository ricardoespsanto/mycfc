import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const email = `e2e-${Date.now()}@example.test`;
const guardianEmail = `e2e-guardian-${Date.now()}@example.test`;
const leisureEmail = `e2e-leisure-${Date.now()}@example.test`;
const athleteEmail = `e2e-athlete-${Date.now()}@example.test`;
const waitlistedEmail = `e2e-waitlisted-${Date.now()}@example.test`;
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

async function emulateBrowserZoom(page, zoom, viewport = { width: 1280, height: 720 }) {
  await page.setViewportSize({ width: viewport.width / zoom, height: viewport.height / zoom });
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

  test('registers a member, reaches the today view, and has no serious accessibility violations', async ({ page }) => {
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

    await expect(page).toHaveURL('/today');
    await expect(page.getByRole('heading', { name: 'Hoje', exact: true })).toBeVisible();
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

    await expect(page).toHaveURL('/today');
    await page.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await page.getByRole('link', { name: 'Encarregado de educação' }).click();
    await expect(page).toHaveURL('/dashboard/guardian');

    await page.locator('#repair-form > summary').click();
    const idempotencyKey = await page.locator('input[name="idempotency_key"]').inputValue();
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByLabel('Fotografia (opcional)').setInputFiles({ name: 'avaria.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/today');
    await page.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await page.getByRole('link', { name: 'Encarregado de educação' }).click();
    const success = page.getByText(/Avaria reportada\. Referência:/);
    await expect(success).toBeVisible();
    const firstReference = await success.textContent();

    await page.locator('input[name="idempotency_key"]').evaluate((input, value) => { input.value = value; }, idempotencyKey);
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/today');
    await page.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await page.getByRole('link', { name: 'Encarregado de educação' }).click();
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
    await expect(page).toHaveURL('/today');
    await expectNoSeriousAxeViolations(page);
    await page.getByRole('button', { name: 'Terminar sessão' }).click();

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/login');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill(guardianEmail);
    await noJavaScriptPage.getByLabel('Palavra-passe').fill(password);
    await noJavaScriptPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await noJavaScriptPage.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await noJavaScriptPage.getByRole('link', { name: 'Encarregado de educação' }).click();
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

  test('administrator validates, schedules, and completes maintenance with keyboard and zoom coverage', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await expect(page).toHaveURL('/today');
    await page.locator('summary').filter({ hasText: 'Administração' }).click();
    await page.getByRole('link', { name: 'Frota' }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('heading', { name: 'Frota', exact: true })).toBeVisible();
    await page.locator('#maintenance-form > summary').click();
    await expect(page.getByLabel('Equipamento')).toBeVisible();
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
    await expect(maintenanceForm.getByText('A descrição deve ter entre 10 e 2000 caracteres.')).toBeVisible();

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
    const task = page.locator('li', { hasText: description });
    const complete = task.getByRole('button', { name: 'Concluir manutenção' });
    await complete.focus();
    await expect(complete).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('status')).toHaveText('Manutenção concluída.');
  });

  test('administrator assigns a competition membership that unlocks the athlete workspace', async ({ page }) => {
    const athleteName = `Atleta E2E ${Date.now()}`;
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await page.locator('summary').filter({ hasText: 'Administração' }).click();
    await page.getByRole('link', { name: 'Membros' }).click();
    await expect(page).toHaveURL('/admin/membros');
    await page.locator('summary').filter({ hasText: 'Criar conta' }).click();
    await page.locator('#member-name').fill(athleteName);
    await page.locator('#member-email').fill(athleteEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Nome, email ou identificador de menor').fill(athleteName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await expect(page).toHaveURL(new RegExp('/admin/membros\\?q='));

    await page.getByRole('link', { name: athleteName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await membershipForm.getByLabel('Competição').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();
    await expect(membershipForm.getByLabel('Competição')).toBeChecked();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await expect(page).toHaveURL('/login');
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(page).toHaveURL('/today');
    await page.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await page.getByRole('link', { name: 'Atleta de competição' }).click();
    await expect(page).toHaveURL('/dashboard/competition');
    await expect(page.getByRole('heading', { name: 'Painel de atleta de competição' })).toBeVisible();
  });

  test('administrator manages event capacity, waitlist confirmation, and check-in', async ({ page }) => {
    test.setTimeout(120000);
    const waitlistedName = `Lista de espera E2E ${Date.now()}`;
    const futureTitle = `Evento com lotação E2E ${Date.now()}`;
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
    await page.locator('summary').filter({ hasText: 'Administração' }).click();
    await page.getByRole('link', { name: 'Membros' }).click();
    await page.locator('summary').filter({ hasText: 'Criar conta' }).click();
    await page.locator('#member-name').fill(waitlistedName);
    await page.locator('#member-email').fill(waitlistedEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Nome, email ou identificador de menor').fill(waitlistedName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: waitlistedName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Competição') });
    await membershipForm.getByLabel('Competição').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();

    await page.goto('/events');
    await page.locator('summary').filter({ hasText: 'Criar evento' }).click();
    await page.locator('#event-title').fill(futureTitle);
    await page.locator('#event-description').fill('Evento de teste para confirmar lotação e lista de espera.');
    await page.locator('#event-starts-at').fill(asDateTimeLocal(futureStart));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(futureEnd));
    await page.locator('#event-capacity').fill('1');
    await page.getByText('Destinatários (opcional)', { exact: true }).click();
    await page.getByLabel('Competição').check();
    await page.getByRole('button', { name: 'Criar evento' }).click();
    await page.getByRole('link', { name: futureTitle }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle }).click();
    await page.getByRole('button', { name: 'Vou', exact: true }).click();
    await expect(page.getByText('Estado: Vou')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(waitlistedEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle }).click();
    await page.getByRole('button', { name: 'Vou', exact: true }).click();
    await expect(page.getByText('Estado: Em lista de espera')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle }).click();
    await page.getByRole('button', { name: 'Não vou' }).click();
    await expect(page.getByText('Estado: Não vou')).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/events');
    await page.getByRole('link', { name: futureTitle }).click();
    await page.locator('li', { hasText: waitlistedName }).getByText('Ações', { exact: true }).click();
    await page.getByRole('button', { name: 'Confirmar vaga' }).click();
    await expect(page.locator('li', { hasText: waitlistedName })).toContainText('Vou');

    await page.goto('/events');
    await page.locator('summary').filter({ hasText: 'Criar evento' }).click();
    await page.locator('#event-title').fill(pastTitle);
    await page.locator('#event-description').fill('Evento de teste para registar uma presença após o início.');
    await page.locator('#event-starts-at').fill(asDateTimeLocal(pastStart));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(pastEnd));
    await page.getByRole('button', { name: 'Criar evento' }).click();
    while (await page.getByRole('link', { name: pastTitle }).count() === 0) {
      await page.getByRole('link', { name: 'Seguinte' }).click();
    }
    const pastEventURL = await page.getByRole('link', { name: pastTitle }).getAttribute('href');
    expect(pastEventURL).not.toBeNull();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(waitlistedEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto(pastEventURL ?? '/events');
    await page.getByRole('button', { name: 'Vou', exact: true }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto(pastEventURL);
    await page.locator('li', { hasText: waitlistedName }).getByText('Ações', { exact: true }).click();
    await page.getByRole('button', { name: 'Registar presença' }).click();
    await expect(page.locator('li', { hasText: waitlistedName })).toContainText('Presença:');
  });

  test('administrator publishes a targeted announcement and the athlete reads it', async ({ page }) => {
    const title = `Aviso de competição E2E ${Date.now()}`;
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/announcements');
    await page.locator('summary').filter({ hasText: 'Criar aviso' }).click();
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
    await page.goto('/announcements');
    const announcement = page.locator('li', { hasText: title });
    await expect(announcement).toContainText('Não lido');
    await announcement.getByRole('link', { name: title }).click();
    await expect(page.getByRole('heading', { name: title })).toBeVisible();

    await page.goto('/announcements');
    await expect(page.locator('li', { hasText: title })).not.toContainText('Não lido');
    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/announcements');
    await page.locator('li', { hasText: title }).getByText('Ações', { exact: true }).click();
    await page.locator('li', { hasText: title }).getByRole('button', { name: 'Expirar' }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.goto('/announcements');
    await expect(page.getByRole('link', { name: title })).toHaveCount(0);
  });

  test('administrator publishes scheduled news to the leisure workspace and expires it', async ({ page }) => {
    test.setTimeout(120000);
    const memberName = `Lazer E2E ${Date.now()}`;
    const title = `Notícia E2E ${Date.now()}`;
    const publishedAt = new Date(Date.now() - 60 * 60 * 1000).toISOString().slice(0, 16);
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.locator('summary').filter({ hasText: 'Administração' }).click();
    await page.getByRole('link', { name: 'Membros' }).click();
    await page.locator('summary').filter({ hasText: 'Criar conta' }).click();
    await page.locator('#member-name').fill(memberName);
    await page.locator('#member-email').fill(leisureEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await page.getByLabel('Nome, email ou identificador de menor').fill(memberName);
    await page.getByRole('button', { name: 'Procurar' }).click();
    await page.getByRole('link', { name: memberName }).click();
    await page.locator('summary').filter({ hasText: 'Inscrições ativas' }).click();
    const membershipForm = page.locator('form').filter({ has: page.getByLabel('Lazer') });
    await membershipForm.getByLabel('Lazer').check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();
    await page.getByLabel('Secções de administração').getByRole('link', { name: 'Notícias' }).click();
    await page.locator('summary').filter({ hasText: 'Criar notícia' }).click();
    await page.locator('#news-title').fill(title);
    await page.locator('#news-summary').fill('Notícia publicada no painel de lazer.');
    await page.locator('#news-published-at').fill(publishedAt);
    await page.getByRole('button', { name: 'Guardar rascunho' }).click();
    const item = page.locator('li', { hasText: title });
    await item.getByText('Ações', { exact: true }).click();
    await item.getByRole('button', { name: 'Publicar' }).click();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(leisureEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.locator('summary').filter({ hasText: 'O meu programa' }).click();
    await page.getByRole('link', { name: 'Lazer' }).click();
    await expect(page.getByText(title)).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.locator('summary').filter({ hasText: 'Administração' }).click();
    await page.getByRole('link', { name: 'Notícias' }).click();
    await page.locator('li', { hasText: title }).getByText('Ações', { exact: true }).click();
    await page.locator('li', { hasText: title }).getByRole('button', { name: 'Expirar' }).click();
  });
});
