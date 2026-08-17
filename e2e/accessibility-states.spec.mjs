import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const adminEmail = 'e2e-admin@example.test';
const password = 'correct horse 7';

async function expectAccessiblePageState(page, state) {
  const structure = await page.evaluate(() => {
    const headings = [...document.querySelectorAll('h1, h2, h3, h4, h5, h6')]
      .filter((heading) => heading.getClientRects().length > 0)
      .map((heading) => ({ level: Number(heading.tagName.slice(1)), text: heading.textContent.trim() }));
    const skippedHeading = headings.find((heading, index) => index > 0 && heading.level > headings[index - 1].level + 1);
    const unresolvedReferences = [...document.querySelectorAll('[aria-describedby], [aria-labelledby]')]
      .flatMap((element) => [...element.attributes]
        .filter(({ name }) => name === 'aria-describedby' || name === 'aria-labelledby')
        .flatMap(({ name, value }) => value.split(/\s+/)
          .filter((id) => id && !document.getElementById(id))
          .map((id) => ({ element: element.tagName, attribute: name, id }))));
    return {
      title: document.title.trim(),
      language: document.documentElement.lang,
      visibleH1s: headings.filter(({ level }) => level === 1).length,
      mainLandmarks: document.querySelectorAll('main').length,
      skippedHeading,
      unresolvedReferences,
    };
  });

  expect(structure.title, `${state}: document title`).not.toBe('');
  expect(structure.language, `${state}: document language`).toBe('pt-PT');
  expect(structure.visibleH1s, `${state}: visible H1 count`).toBe(1);
  expect(structure.mainLandmarks, `${state}: main landmark count`).toBe(1);
  expect(structure.skippedHeading, `${state}: heading order`).toBeUndefined();
  expect(structure.unresolvedReferences, `${state}: ARIA references`).toEqual([]);

  const violations = (await new AxeBuilder({ page }).analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, `${state}: ${JSON.stringify(violations, null, 2)}`).toEqual([]);
}

async function login(page, identifier) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(identifier);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function waitUntilRegistrationCanSubmit(page) {
  const token = await page.locator('input[name="registration_token"]').inputValue();
  const renderedAtNanoseconds = Buffer.from(token, 'base64url').readBigUInt64BE();
  const renderedAtMilliseconds = Number(renderedAtNanoseconds / 1_000_000n);
  const remainingMilliseconds = renderedAtMilliseconds + 2_000 - Date.now();
  if (remainingMilliseconds > 0) {
    await page.waitForTimeout(remainingMilliseconds);
  }
}

test.describe('representative page-state accessibility', () => {
  test.describe.configure({ mode: 'parallel' });

  test('covers public empty and validation-error states', async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto('/login');
    await expectAccessiblePageState(page, 'GET /login empty');

    await page.getByLabel('Correio eletrónico ou identificador CFC').fill('unknown@example.test');
    await page.getByLabel('Palavra-passe').fill('incorrect password');
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(page.locator('.error-summary')).toBeFocused();
    await expectAccessiblePageState(page, 'POST /login invalid credentials');

    await page.goto('/registo');
    await page.getByLabel('Nome').fill('M');
    await page.getByLabel('Correio eletrónico').fill(`e2e-invalid-registration-${Date.now()}@example.test`);
    await page.getByLabel('Data de nascimento').fill('2014-01-01');
    await page.locator('#password').fill('short');
    await page.getByLabel('Confirmar palavra-passe').fill('different');
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await waitUntilRegistrationCanSubmit(page);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page.locator('.error-summary')).toBeFocused();
    await expectAccessiblePageState(page, 'POST /registo validation errors');
  });

  test('covers member empty collections and system error states', async ({ page }) => {
    test.setTimeout(120_000);
    const email = `e2e-accessibility-${Date.now()}@example.test`;
    await page.goto('/registo');
    await page.getByLabel('Nome').fill('Estado acessível');
    await page.getByLabel('Correio eletrónico').fill(email);
    await page.getByLabel('Data de nascimento').fill('1990-01-01');
    await page.locator('#password').fill(password);
    await page.getByLabel('Confirmar palavra-passe').fill(password);
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await waitUntilRegistrationCanSubmit(page);
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page).toHaveURL('/today');

    for (const [route, emptyState] of [
      ['/treinos', 'Sem sessões disponíveis'],
      ['/sugestoes', 'Ainda sem sugestões'],
      ['/albuns', 'Ainda sem álbuns'],
    ]) {
      await page.goto(route);
      await expect(page.getByText(emptyState, { exact: true })).toBeVisible();
      await expectAccessiblePageState(page, `GET ${route} member empty`);
    }

    const forbidden = await page.goto('/admin/fleet');
    expect(forbidden?.status()).toBe(403);
    await expectAccessiblePageState(page, 'GET /admin/fleet forbidden');

    const missing = await page.goto('/missing');
    expect(missing?.status()).toBe(404);
    await expectAccessiblePageState(page, 'GET /missing authenticated not found');
  });

  test('covers administrator populated collections and authoring disclosures', async ({ page }) => {
    test.setTimeout(240_000);
    await login(page, adminEmail);

    for (const [route, populatedSelector] of [
      ['/admin/membros', 'tbody tr'],
      ['/admin/fleet#equipment-inventory', '#equipment-inventory tbody tr'],
    ]) {
      await page.goto(route);
      await expect(page.locator(populatedSelector).first()).toBeVisible();
      await expectAccessiblePageState(page, `GET ${route} populated`);
    }

    for (const [route, panelID] of [
      ['/admin/eventos', 'criar-evento'],
      ['/admin/albuns', 'novo-album'],
    ]) {
      await page.goto(route);
      const panel = page.locator(`#${panelID}`);
      await expect(panel).not.toHaveAttribute('open', '');
      await expectAccessiblePageState(page, `GET ${route} authoring closed`);
      await page.locator(`a[href="#${panelID}"]`).click();
      await expect(panel).toHaveAttribute('open', '');
      await expectAccessiblePageState(page, `GET ${route} authoring open`);
    }
  });
});
