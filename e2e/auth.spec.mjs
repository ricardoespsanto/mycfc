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
    .filter((element) => !element.classList.contains('visually-hidden') && element.scrollWidth > element.clientWidth + 1)
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
    await page.locator('#password').fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.getByRole('button', { name: 'Criar conta' }).click();

    await expect(page).toHaveURL('/today');
    await expect(page.getByRole('heading', { name: 'Olá, Pessoa', exact: true })).toBeVisible();
    await expectNoSeriousAxeViolations(page);

    await page.goto('/dashboard/member?from=legacy');
    await expect(page).toHaveURL('/today?from=legacy');

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
    await page.getByRole('link', { name: 'Menores a cargo' }).click();
    await expect(page).toHaveURL('/dashboard/guardian');

    await page.locator('#repair-form > summary').click();
    const idempotencyKey = await page.locator('input[name="idempotency_key"]').inputValue();
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByLabel('Fotografia (opcional)').setInputFiles({ name: 'avaria.png', mimeType: 'image/png', buffer: validPNG });
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Menores a cargo' }).click();
    const success = page.getByText(/Avaria reportada\. Referência:/);
    await expect(success).toBeVisible();
    const firstReference = await success.textContent();

    await page.locator('input[name="idempotency_key"]').evaluate((input, value) => { input.value = value; }, idempotencyKey);
    await page.getByLabel('Equipamento').selectOption({ label: 'E2E-REPAIR - Embarcação de teste' });
    await page.getByLabel('Descrição da avaria').fill('Avaria de teste com fotografia.');
    await page.getByRole('button', { name: 'Reportar avaria' }).click();
    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Menores a cargo' }).click();
    await expect(page.getByText(/Avaria reportada\. Referência:/)).toHaveText(firstReference ?? '');
    await context.close();
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
    await page.getByRole('link', { name: 'Frota', exact: true }).click();
    await expect(page).toHaveURL('/admin/fleet');
    await expect(page.getByRole('heading', { name: 'Frota', exact: true })).toBeVisible();
    await page.locator('#maintenance-form > summary').click();
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

    await page.locator('#equipment-inventory > summary').click();
    let equipment = page.locator('#equipment-inventory ul[aria-label="Inventário da frota"] > li', { hasText: assetTag });
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

    await page.locator('#equipment-inventory > summary').click();
    equipment = page.locator('#equipment-inventory ul[aria-label="Inventário da frota"] > li', { hasText: updatedTag });
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

    await page.locator('#equipment-inventory > summary').click();
    equipment = page.locator('#equipment-inventory ul[aria-label="Inventário da frota"] > li', { hasText: updatedTag });
    await equipment.getByText('Ações').click();
    await equipment.getByRole('button', { name: 'Retirar da frota' }).click();
    await expect(page.getByRole('status')).toContainText('Equipamento retirado da frota');
    await expect(page.getByText(maintenanceDescription)).toHaveCount(0);

    await page.goto(equipmentEditURL);
    await expect(page.getByText('Equipamento retirado')).toBeVisible();
    await expect(page.getByText('1 tarefa de manutenção ativa foi cancelada.')).toBeVisible();
    await page.getByRole('link', { name: 'Voltar à frota' }).click();

    for (let equipmentPage = 0; equipmentPage < 50; equipmentPage += 1) {
      await page.locator('#equipment-inventory > summary').click();
      equipment = page.locator('#equipment-inventory ul[aria-label="Inventário da frota"] > li', { hasText: updatedTag });
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
    test.setTimeout(120000);
    const athleteName = `Atleta E2E ${Date.now()}`;
    await page.goto('/login');
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();

    await page.getByRole('link', { name: 'Membros', exact: true }).click();
    await expect(page).toHaveURL('/admin/membros');
    await page.locator('summary').filter({ hasText: 'Criar conta' }).click();
    await page.locator('#member-name').fill(athleteName);
    await page.locator('#member-email').fill(athleteEmail);
    await page.locator('#member-birth').fill('2000-01-01');
    await page.locator('#member-password').fill(password);
    await page.locator('#member-password-confirmation').fill(password);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page.getByRole('status')).toHaveText('Conta criada.');
    await page.getByLabel('Nome, email ou identificador de menor').fill(athleteName);
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

    const planTitle = `Plano classificação E2E ${Date.now()}`;
    const sessionTitle = `Sessão classificação E2E ${Date.now()}`;
    await page.goto('/treinos');
    await page.locator('summary').filter({ hasText: 'Gerir treinos' }).click();
    await page.locator('summary').filter({ hasText: 'Criar plano' }).click();
    const planForm = page.locator('form[action="/treinos/planos"]');
    await planForm.getByLabel('Título').fill(planTitle);
    await planForm.getByLabel('Programa').selectOption({ label: 'Competição' });
    await planForm.getByRole('button', { name: 'Criar plano' }).click();
    await expect(page.getByRole('status')).toHaveText('Plano criado.');

    await page.locator('summary').filter({ hasText: 'Gerir treinos' }).click();
    await page.locator('summary').filter({ hasText: 'Criar sessão' }).click();
    const sessionForm = page.locator('form[action="/treinos/sessoes"]');
    const sessionStart = new Date(Date.now() - 6 * 60 * 60 * 1000);
    const sessionEnd = new Date(Date.now() - 5 * 60 * 60 * 1000);
    await sessionForm.getByLabel('Plano').selectOption({ label: planTitle });
    await sessionForm.getByLabel('Título').fill(sessionTitle);
    await sessionForm.getByLabel('Início').fill(sessionStart.toISOString().slice(0, 16));
    await sessionForm.getByLabel('Fim').fill(sessionEnd.toISOString().slice(0, 16));
    await sessionForm.getByRole('button', { name: 'Criar sessão' }).click();
    await expect(page.getByRole('status')).toHaveText('Sessão criada.');

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await expect(page).toHaveURL('/login');
    await page.getByLabel('Correio eletrónico').fill(athleteEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(page).toHaveURL('/today');
    await page.getByRole('link', { name: 'Competição', exact: true }).click();
    await expect(page).toHaveURL('/dashboard/competition');
    await expect(page.getByRole('heading', { name: 'Competição', exact: true })).toBeVisible();

    await page.goto('/treinos');
    const athleteSession = page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]');
    await athleteSession.locator('summary').filter({ hasText: 'Marcar concluída' }).click();
    await athleteSession.getByLabel('Distância (km)').fill('12.34');
    await athleteSession.getByRole('button', { name: 'Concluir sessão' }).click();
    await expect(page.getByRole('status')).toHaveText('Resultado registado.');
    await expect(page.getByRole('heading', { name: new RegExp(`${sessionTitle}$`) }).locator('xpath=ancestor::li[1]')).toContainText('12,34 km');

    await page.goto('/today?leaderboard_period=all');
    const leaderboard = page.locator('#leaderboard');
    await expect(leaderboard).toContainText(athleteName);
    await expect(leaderboard).toContainText('12,34 km');
    await page.getByLabel('Mostrar os meus quilómetros na classificação').uncheck();
    await page.getByRole('button', { name: 'Guardar privacidade' }).click();
    await expect(page.getByRole('status')).toHaveText('Privacidade da classificação atualizada.');
    await expect(leaderboard).toContainText('Os seus totais estão privados');
    await expect(leaderboard).not.toContainText(athleteName);
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
    await page.getByRole('link', { name: 'Membros', exact: true }).click();
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
    await page.locator('#event-title').fill('E');
    await page.locator('#event-description').fill('Valores seguros devem permanecer após validação.');
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
    await page.locator('#event-starts-at').fill(asDateTimeLocal(futureStart));
    await page.locator('#event-ends-at').fill(asDateTimeLocal(futureEnd));
    await page.locator('#event-capacity').fill('1');
    await page.getByRole('button', { name: 'Criar evento' }).click();
    await expect(page.getByRole('status')).toHaveText('Evento criado.');
    await page.getByRole('link', { name: futureTitle }).click();
    await expect(page.getByRole('navigation', { name: 'Localização atual' }).getByRole('link', { name: 'Eventos' })).toHaveAttribute('href', '/events');

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
    const memberships = page.locator('details').filter({ has: page.getByText('Inscrições ativas', { exact: false }) });
    await memberships.locator('summary').click();
    const membershipForm = memberships.locator('form').filter({
      has: page.getByRole('checkbox', { name: 'Lazer', exact: true }),
    });
    await membershipForm.getByRole('checkbox', { name: 'Lazer', exact: true }).check();
    await membershipForm.getByRole('button', { name: 'Guardar' }).click();
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('link', { name: 'Notícias' }).click();
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
    await page.getByRole('link', { name: 'Lazer', exact: true }).click();
    await expect(page.getByText(title)).toBeVisible();

    await page.getByRole('button', { name: 'Terminar sessão' }).click();
    await page.getByLabel('Correio eletrónico').fill(adminEmail);
    await page.getByLabel('Palavra-passe').fill(password);
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await page.getByRole('link', { name: 'Notícias' }).click();
    await page.locator('li', { hasText: title }).getByText('Ações', { exact: true }).click();
    await page.locator('li', { hasText: title }).getByRole('button', { name: 'Expirar' }).click();

    const context = await browser.newContext({ baseURL, javaScriptEnabled: false });
    const noJavaScriptPage = await context.newPage();
    await noJavaScriptPage.goto('/login');
    await noJavaScriptPage.getByLabel('Correio eletrónico').fill(adminEmail);
    await noJavaScriptPage.getByLabel('Palavra-passe').fill(password);
    await noJavaScriptPage.getByRole('button', { name: 'Iniciar sessão' }).click();
    await noJavaScriptPage.getByRole('link', { name: 'Membros', exact: true }).click();
    await noJavaScriptPage.getByLabel('Nome, email ou identificador de menor').fill(memberName);
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
