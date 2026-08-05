import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run through make ui-review-screenshots.');

const password = 'correct horse 7';
const outputRoot = path.resolve('artifacts/ui-review');
const personas = [
  { key: 'member', email: 'review-member@example.test', routes: ['/today', '/events', '/announcements', '/admin/fleet', '/missing'] },
  { key: 'tutor', email: 'review-tutor@example.test', routes: ['/today', '/dashboard/guardian'] },
  { key: 'athlete', email: 'review-athlete@example.test', programmeShortcut: 'Competição', routes: ['/today', '/dashboard/competition', '/treinos'] },
  { key: 'coach', email: 'review-coach@example.test', routes: ['/today', '/events', '/treinos', '/admin/eventos', '/admin/avisos', '/admin/treinos'] },
  { key: 'admin', email: 'review-admin@example.test', routes: ['/today', '/admin/membros', '/admin/eventos', '/admin/avisos', '/admin/treinos', '/admin/noticias', '/admin/fleet'] },
  { key: 'multi', email: 'review-multi@example.test', programmeShortcut: 'Lazer', routes: ['/today', '/dashboard/leisure', '/dashboard/competition', '/events'] },
];
const viewports = [
  { key: 'desktop', width: 1440, height: 900 },
  { key: 'mobile', width: 375, height: 812 },
];
async function expectResponsiveContract(page, route, viewport) {
  const initialHeadingSize = await page.locator('main h1').evaluate((heading) => Number.parseFloat(getComputedStyle(heading).fontSize));
  const maximumHeadingSize = viewport.width <= 640 ? 32 : 48;
  expect(initialHeadingSize, `${route} uses an oversized primary heading`).toBeLessThanOrEqual(maximumHeadingSize);
  const oversizedSectionHeadings = await page.locator('main h2').evaluateAll((headings) => headings
    .filter((heading) => {
      const rect = heading.getBoundingClientRect();
      return heading.getClientRects().length > 0 && rect.width > 4 && rect.height > 4 && Number.parseFloat(getComputedStyle(heading).fontSize) > 22;
    })
    .map((heading) => ({ text: heading.textContent.trim(), size: getComputedStyle(heading).fontSize })));
  expect(oversizedSectionHeadings, `${route} uses oversized section headings`).toEqual([]);
  const boxedModules = await page.locator('main .module').evaluateAll((modules) => modules
    .filter((module) => module.getClientRects().length > 0 && !module.matches('[data-create-panel][open]'))
    .map((module) => ({ module, style: getComputedStyle(module) }))
    .filter(({ style }) => style.backgroundColor !== 'rgba(0, 0, 0, 0)'
      || style.borderRadius !== '0px'
      || style.boxShadow !== 'none'
      || Number.parseFloat(style.borderInlineStartWidth) > 0
      || Number.parseFloat(style.borderInlineEndWidth) > 0)
    .map(({ module, style }) => ({
      className: module.className,
      background: style.backgroundColor,
      borderRadius: style.borderRadius,
      boxShadow: style.boxShadow,
    })));
  expect(boxedModules, `${route} wraps modules in rounded white containers`).toEqual([]);
  const weaklySeparatedModules = await page.locator('main .module').evaluateAll((modules) => modules
    .filter((module) => module.getClientRects().length > 0 && !module.matches('[data-create-panel]'))
    .filter((module) => Number.parseFloat(getComputedStyle(module).borderBlockStartWidth) < 3)
    .map((module) => ({ className: module.className, title: module.querySelector('h2,summary')?.textContent.trim().slice(0, 80) })));
  expect(weaklySeparatedModules, `${route} has modules without a clear section divider`).toEqual([]);
  const unaffordedTaskActions = await page.locator('main a, main button, main summary').evaluateAll((controls) => controls
    .filter((control) => control.getClientRects().length > 0
      && !control.closest('.auth-alternate')
      && !control.matches('.module.disclosure > summary')
      && /^(Criar|Adicionar|Agendar|Publicar|Registar|Gerir)/.test(control.textContent.trim()))
    .map((control) => ({ control, style: getComputedStyle(control) }))
    .filter(({ style }) => style.borderTopStyle === 'none' && style.backgroundColor === 'rgba(0, 0, 0, 0)')
    .map(({ control }) => ({ tag: control.tagName, text: control.textContent.trim().replace(/\s+/g, ' ').slice(0, 80), className: control.className })));
  expect(unaffordedTaskActions, `${route} presents task actions as unstyled links`).toEqual([]);

  for (const width of [320, 375, 768]) {
    await page.setViewportSize({ width, height: 720 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} overflows at ${width}px`).toBeLessThanOrEqual(width);
  }

  // A 640 CSS-pixel layout is the effective viewport at 200% browser zoom
  // from the agreed 1280-pixel desktop baseline.
  await page.setViewportSize({ width: 640, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} overflows at 200% zoom`).toBeLessThanOrEqual(640);

  await page.setViewportSize({ width: 375, height: 720 });
  const mobileHeadingSize = await page.locator('main h1').evaluate((heading) => Number.parseFloat(getComputedStyle(heading).fontSize));
  expect(mobileHeadingSize, `${route} uses an oversized mobile primary heading`).toBeLessThanOrEqual(32);
  const touchTargetSelector = 'button, input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]), select, textarea, summary, label:has(> input[type="checkbox"]), label:has(> input[type="radio"]), .action, .site-nav a, .admin-subnav a, .pagination a';
  const undersizedTargets = await page.evaluate((selector) => [...document.querySelectorAll(selector)]
    .filter((element) => element.getClientRects().length > 0)
    .map((element) => ({ element, rect: element.getBoundingClientRect() }))
    .filter(({ rect }) => rect.width < 44 || rect.height < 44)
    .map(({ element, rect }) => ({ tag: element.tagName, text: element.textContent.trim().slice(0, 60), width: Math.round(rect.width), height: Math.round(rect.height) })), touchTargetSelector);
  expect(undersizedTargets, `${route} has undersized frequent touch targets`).toEqual([]);

  const oversizedBadges = await page.locator('.badge').evaluateAll((badges) => badges
    .filter((badge) => badge.getClientRects().length > 0 && badge.getBoundingClientRect().width > 200)
    .map((badge) => ({ text: badge.textContent.trim(), width: Math.round(badge.getBoundingClientRect().width) })));
  expect(oversizedBadges, `${route} stretches status badges into full-width pills`).toEqual([]);

  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} clips enlarged text`).toBeLessThanOrEqual(375);
  await page.evaluate(() => { document.documentElement.style.fontSize = ''; });

  await page.locator('main details:not([data-create-panel])').evaluateAll((details) => details.forEach((detail) => {
    detail.dataset.responsiveWasOpen = String(detail.open);
    detail.open = true;
  }));
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} overflows with disclosures expanded`).toBeLessThanOrEqual(320);
  const expandedTargets = await page.evaluate((selector) => [...document.querySelectorAll(selector)]
    .filter((element) => element.getClientRects().length > 0)
    .map((element) => ({ element, rect: element.getBoundingClientRect() }))
    .filter(({ rect }) => rect.width < 44 || rect.height < 44)
    .map(({ element, rect }) => ({ tag: element.tagName, text: element.textContent.trim().slice(0, 60), width: Math.round(rect.width), height: Math.round(rect.height) })), touchTargetSelector);
  expect(expandedTargets, `${route} has undersized targets in expanded workflows`).toEqual([]);
  await page.setViewportSize({ width: 375, height: 720 });
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} clips enlarged expanded workflows`).toBeLessThanOrEqual(375);
  await page.evaluate(() => {
    document.documentElement.style.fontSize = '';
    document.querySelectorAll('main details:not([data-create-panel])').forEach((detail) => {
      detail.open = detail.dataset.responsiveWasOpen === 'true';
      delete detail.dataset.responsiveWasOpen;
    });
  });

  await page.setViewportSize({ width: 375, height: 400 });
  const focusTarget = page.locator('main input:not([type="hidden"]), main select, main textarea, main button, main summary, main a').filter({ visible: true }).first();
  if (await focusTarget.count()) {
    await focusTarget.focus();
    const focusedRect = await focusTarget.boundingBox();
    expect(focusedRect.y, `${route} focus is obscured above the compact viewport`).toBeGreaterThanOrEqual(0);
    expect(Math.floor(focusedRect.y + focusedRect.height), `${route} focus is obscured below the compact viewport`).toBeLessThanOrEqual(400);
  }

  await page.setViewportSize({ width: 320, height: 720 });
  const narrowLayout = await page.evaluate(() => ({
    width: document.documentElement.scrollWidth,
    overflowers: [...document.querySelectorAll('*')]
      .filter((element) => element.getBoundingClientRect().right > 320 || element.scrollWidth > element.clientWidth)
      .slice(0, 12)
      .map((element) => ({ tag: element.tagName, className: element.className, clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, right: Math.round(element.getBoundingClientRect().right) })),
  }));
  expect(narrowLayout.width, JSON.stringify(narrowLayout.overflowers, null, 2)).toBeLessThanOrEqual(320);
  await page.setViewportSize({ width: viewport.width, height: viewport.height });
  await page.evaluate(() => document.activeElement?.blur());
}

async function expectAccessibilityContract(page, route) {
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
      h1Count: headings.filter(({ level }) => level === 1).length,
      mainCount: document.querySelectorAll('main').length,
      skippedHeading,
      unresolvedReferences,
      htmlLanguage: document.documentElement.lang,
    };
  });
  expect(structure.title, `${route} has no document title`).not.toBe('');
  expect(structure.htmlLanguage).toBe('pt-PT');
  expect(structure.h1Count, `${route} must have one visible h1`).toBe(1);
  expect(structure.mainCount, `${route} must have one main landmark`).toBe(1);
  expect(structure.skippedHeading, `${route} skips a heading level`).toBeUndefined();
  expect(structure.unresolvedReferences, `${route} has unresolved ARIA references`).toEqual([]);

  const visibleBrandMarks = await page.locator('.auth-brand img, .public-brand img, .site-brand img, .mobile-app-brand img').evaluateAll((images) => images
    .filter((image) => image.getClientRects().length > 0)
    .map((image) => {
      const style = getComputedStyle(image);
      const rect = image.getBoundingClientRect();
      return {
        className: image.closest('a')?.className ?? '',
        filter: style.filter,
        height: Math.round(rect.height),
        width: Math.round(rect.width),
      };
    }));
  expect(visibleBrandMarks.length, `${route} has no visible brand mark`).toBeGreaterThan(0);
  expect(
    visibleBrandMarks.filter(({ filter, height, width }) => filter === 'none' || height < 32 || width < 32),
    `${route} has a dark-background brand mark without the light treatment or at an unreadable size`,
  ).toEqual([]);

  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press('Tab');
  const skipLink = page.locator('.skip-link');
  await expect(skipLink, `${route} must expose the skip link first`).toBeFocused();
  await expect(skipLink).toHaveAttribute('href', '#conteudo-principal');
  const focusStyle = await skipLink.evaluate((element) => {
    const style = getComputedStyle(element);
    return { width: parseFloat(style.outlineWidth), style: style.outlineStyle };
  });
  expect(focusStyle.width, `${route} focus indicator has no thickness`).toBeGreaterThanOrEqual(2);
  expect(focusStyle.style, `${route} focus indicator is not visible`).not.toBe('none');

  await page.emulateMedia({ forcedColors: 'active', reducedMotion: 'reduce' });
  expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches), `${route} forced colours were not applied`).toBe(true);
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches), `${route} reduced motion was not applied`).toBe(true);
  const forcedFocusStyle = await skipLink.evaluate((element) => {
    const style = getComputedStyle(element);
    return { width: parseFloat(style.outlineWidth), style: style.outlineStyle };
  });
  expect(forcedFocusStyle.width, `${route} loses focus thickness in forced colours`).toBeGreaterThanOrEqual(2);
  expect(forcedFocusStyle.style, `${route} loses focus visibility in forced colours`).not.toBe('none');
  await page.emulateMedia({ forcedColors: 'none', reducedMotion: 'no-preference' });
  await page.evaluate(() => document.activeElement?.blur());
}

async function login(page, email) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

async function expectTodayComposition(page, persona) {
  await expect(page.getByRole('heading', { name: /^Olá,/ })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Agenda de hoje' })).toBeVisible();
  await expect(page.getByRole('link', { name: /^Avisos/ })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Classificação do clube' })).toBeVisible();
  const moduleWidths = await page.locator('.today-grid > .today-module').evaluateAll((modules) => modules
    .filter((module) => module.getClientRects().length > 0)
    .map((module) => Math.round(module.getBoundingClientRect().width)));
  expect(new Set(moduleWidths).size, 'Today modules use inconsistent content widths').toBe(1);
  await expect(page.getByRole('navigation', { name: 'Período da classificação' }).getByRole('link', { name: 'Semana' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: 'Continuar no MyCFC' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Eventos Agenda e respostas/ })).toBeVisible();
  if (persona.key === 'tutor') {
    await expect(page.getByRole('heading', { name: 'Menores a cargo' })).toBeVisible();
  }
  if (persona.programmeShortcut) {
    await expect(page.getByRole('link', { name: `${persona.programmeShortcut} Abrir o meu espaço` })).toBeVisible();
    await expect(page.getByLabel('Mostrar os meus quilómetros na classificação')).toBeChecked();
  }
  if (persona.key === 'admin') {
    await expect(page.getByRole('heading', { name: 'Requer atenção' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Abrir frota' })).toBeVisible();
    expect(await page.locator('.today-module--agenda a[href^="/admin/eventos/"]').count(), 'Admin agenda links bypass the management detail page').toBeGreaterThan(0);
  }
}

async function expectCollectionWorkflow(page, route) {
  await expect(page.locator('.collection-page')).toBeVisible();
  if (route === '/admin/fleet') {
    const tabs = page.getByRole('tablist', { name: 'Áreas da frota' });
    const equipmentTab = tabs.getByRole('tab', { name: /Equipamentos/ });
    const repairTab = tabs.getByRole('tab', { name: /Reparações/ });
    await expect(equipmentTab).toHaveAttribute('aria-selected', 'true');
    await equipmentTab.focus();
    await page.keyboard.press('ArrowRight');
    await expect(repairTab).toBeFocused();
    await expect(repairTab).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('#repair-requests')).toBeVisible();
    await expect(page.locator('#equipment-inventory')).toBeHidden();
    await equipmentTab.click();
  }

  const actionNames = {
    '/admin/membros': 'Criar conta',
    '/admin/eventos': 'Criar evento',
    '/admin/fleet': 'Adicionar equipamento',
  };
  const actionName = actionNames[route];
  const trigger = page.getByRole('link', { name: actionName, exact: true });
  if (await trigger.count() === 0) return;
  const targetID = (await trigger.getAttribute('href')).slice(1);
  const panel = page.locator(`#${targetID}`);
  await expect(panel).not.toHaveAttribute('open', '');
  await trigger.click();
  await expect(panel).toHaveAttribute('open', '');
  await expect(panel.getByRole('button', { name: 'Fechar' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(panel).not.toHaveAttribute('open', '');
  await expect(trigger).toBeFocused();
  await page.evaluate(() => document.activeElement?.blur());
}

test('captures River Clubhouse authentication states', async ({ browser }) => {
  test.setTimeout(180_000);
  for (const viewport of viewports) {
    const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
    const page = await context.newPage();
    const directory = path.join(outputRoot, 'authentication', viewport.key);
    await mkdir(directory, { recursive: true });

    await page.goto('/');
    const publicBrandSize = await page.locator('.public-brand img').evaluate((image) => {
      const rect = image.getBoundingClientRect();
      return { height: Math.round(rect.height), width: Math.round(rect.width) };
    });
    expect(publicBrandSize.width, 'The public crest is compressed in the header').toBeGreaterThanOrEqual(40);
    expect(publicBrandSize.height, 'The public crest is compressed in the header').toBeGreaterThanOrEqual(40);

    await page.goto('/login');
    await expect(page.locator('body')).toHaveClass('auth-body');
    await expect(page.getByRole('link', { name: 'MyCFC, voltar à página inicial' })).toHaveAttribute('href', '/');
    await expect(page.getByRole('navigation', { name: 'Navegação principal' })).toHaveCount(0);
    await expectAccessibilityContract(page, '/login');
    await expectResponsiveContract(page, '/login', viewport);
    await expect((await new AxeBuilder({ page }).analyze()).violations.filter(({ impact }) => impact === 'serious' || impact === 'critical')).toEqual([]);
    await page.screenshot({ path: path.join(directory, 'login-empty.png'), fullPage: true });

    await page.getByLabel('Correio eletrónico ou identificador CFC').fill('review-member@example.test');
    await page.getByLabel('Palavra-passe').fill('palavra-passe-incorreta');
    await page.getByRole('button', { name: 'Iniciar sessão' }).click();
    await expect(page.locator('.error-summary')).toBeFocused();
    await expect(page.getByLabel('Correio eletrónico ou identificador CFC')).toHaveValue('review-member@example.test');
    await expect(page.getByLabel('Correio eletrónico ou identificador CFC')).toHaveAttribute('aria-invalid', 'true');
    await page.screenshot({ path: path.join(directory, 'login-invalid.png'), fullPage: true });

    await page.goto('/registo');
    await expectAccessibilityContract(page, '/registo');
    await expectResponsiveContract(page, '/registo', viewport);
    await expect((await new AxeBuilder({ page }).analyze()).violations.filter(({ impact }) => impact === 'serious' || impact === 'critical')).toEqual([]);
    await page.screenshot({ path: path.join(directory, 'registration-empty.png'), fullPage: true });

    await page.getByLabel('Nome').fill('M');
    await page.getByLabel('Correio eletrónico').fill('pessoa@example.test');
    await page.getByLabel('Data de nascimento').fill('2014-01-01');
    await page.locator('#password').fill('curta');
    await page.getByLabel('Confirmar palavra-passe').fill('diferente');
    await page.getByLabel(/Aceito os termos gerais/).check();
    await page.getByLabel(/Aceito a autorização de uso de imagem/).check();
    await page.getByRole('button', { name: 'Criar conta' }).click();
    await expect(page.locator('.error-summary')).toBeFocused();
    await expect(page.getByLabel('Correio eletrónico')).toHaveValue('pessoa@example.test');
    await expect(page.getByLabel(/Aceito a autorização de uso de imagem/)).toBeChecked();
    await expect(page.locator('#password')).toHaveValue('');
    await page.screenshot({ path: path.join(directory, 'registration-invalid.png'), fullPage: true });
    await context.close();
  }
});

test('checks compact collection and creation workflows', async ({ browser }) => {
  test.setTimeout(180_000);
  for (const viewport of viewports) {
    const adminContext = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
    const adminPage = await adminContext.newPage();
    await login(adminPage, 'review-admin@example.test');
    for (const route of ['/admin/membros', '/admin/fleet']) {
      await adminPage.goto(route);
      await expectCollectionWorkflow(adminPage, route);
      expect(await adminPage.evaluate(() => document.documentElement.scrollWidth), `${route} overflows at ${viewport.width}px`).toBeLessThanOrEqual(viewport.width);
    }
    await adminContext.close();

    const coachContext = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
    const coachPage = await coachContext.newPage();
    await login(coachPage, 'review-coach@example.test');
    await coachPage.goto('/admin/eventos');
    await expectCollectionWorkflow(coachPage, '/admin/eventos');
    expect(await coachPage.evaluate(() => document.documentElement.scrollWidth), `/admin/eventos overflows at ${viewport.width}px`).toBeLessThanOrEqual(viewport.width);
    await coachContext.close();
  }
});

test('keeps collection creation usable without JavaScript', async ({ browser }) => {
  test.setTimeout(90_000);
  const context = await browser.newContext({ viewport: { width: 375, height: 812 }, javaScriptEnabled: false });
  const page = await context.newPage();
  await login(page, 'review-admin@example.test');
  const mobileMenu = page.locator('.mobile-app-menu');
  await mobileMenu.locator('summary').click();
  await expect(mobileMenu).toHaveAttribute('open', '');
  await expect(mobileMenu.getByRole('navigation', { name: 'Navegação principal' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth), 'mobile navigation fallback overflows').toBeLessThanOrEqual(375);
  await mobileMenu.locator('summary').click();
  for (const [route, panelID, summaryName] of [
    ['/admin/membros', 'criar-conta', 'Criar conta'],
    ['/admin/eventos', 'criar-evento', 'Criar evento'],
    ['/admin/fleet', 'equipment-form', 'Adicionar equipamento'],
  ]) {
    await page.goto(route);
    const panel = page.locator(`#${panelID}`);
    await expect(panel).toBeVisible();
    await expect(panel).not.toHaveAttribute('open', '');
    await panel.locator('summary').getByText(summaryName, { exact: true }).click();
    await expect(panel).toHaveAttribute('open', '');
    await expect(panel.locator('form')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} no-JavaScript fallback overflows`).toBeLessThanOrEqual(375);
  }
  await page.goto('/admin/fleet');
  await expect(page.locator('#equipment-inventory')).toBeVisible();
  await expect(page.locator('#repair-requests')).toBeVisible();
  await expect(page.locator('#maintenance-schedule')).toBeVisible();
  await context.close();
});

for (const persona of personas) {
  test(`captures ${persona.key} desktop and mobile journeys`, async ({ browser }) => {
    test.setTimeout(300_000);
    for (const viewport of viewports) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
      const page = await context.newPage();
      const seenTitles = new Map();
      await login(page, persona.email);
      for (const route of persona.routes) {
        const response = await page.goto(route);
        if (route === '/missing') expect(response.status()).toBe(404);
        if (persona.key === 'member' && route === '/admin/fleet') expect(response.status()).toBe(403);
        await expect(page.locator('main h1')).toBeVisible();
		if (route === '/today') {
		  await expectTodayComposition(page, persona);
		  await page.setViewportSize({ width: 320, height: 720 });
		  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
		  await page.setViewportSize({ width: viewport.width, height: viewport.height });
        }
		if (route === '/dashboard/guardian') {
		  await expect(page.getByRole('heading', { level: 2, name: 'Dependentes associados' })).toBeVisible();
		  await expect(page.getByRole('heading', { level: 2, name: 'Menores a cargo' })).toHaveCount(0);
		}
		if (persona.key === 'coach') {
			await expect(page.getByText('Treinador', { exact: true })).toBeVisible();
			await expect(page.getByRole('link', { name: 'Treinador', exact: true })).toHaveCount(0);
		}
		if (persona.key === 'multi') {
			await expect(page.getByText('Moderador', { exact: true })).toBeVisible();
			await expect(page.getByRole('link', { name: 'Moderador', exact: true })).toHaveCount(0);
		}
		await expectAccessibilityContract(page, route);
		const routeTitle = await page.title();
		expect(seenTitles.has(routeTitle), `${route} shares its document title with ${seenTitles.get(routeTitle)}`).toBe(false);
		seenTitles.set(routeTitle, route);
		await expectResponsiveContract(page, route, viewport);
        const violations = (await new AxeBuilder({ page }).analyze()).violations
          .filter(({ impact }) => impact === 'serious' || impact === 'critical');
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
		if (response.ok() && ['/admin/eventos', '/admin/membros', '/admin/fleet'].includes(route)) {
		  await expectCollectionWorkflow(page, route);
		}
        const directory = path.join(outputRoot, persona.key, viewport.key);
        await mkdir(directory, { recursive: true });
        const slug = route === '/today' ? 'today' : route.replace(/^\//, '').replaceAll('/', '--');
        await page.screenshot({ path: path.join(directory, `${slug}.png`), fullPage: true });
        if (route === '/admin/membros') {
          const detailHref = await page.locator('tbody th a').first().getAttribute('href');
          expect(detailHref).toMatch(/^\/admin\/membros\/[0-9a-f-]+$/);
          await page.goto(detailHref);
          await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
          await expect(page.getByRole('heading', { name: 'Identidade e acesso' })).toBeVisible();
          await expectAccessibilityContract(page, detailHref);
          const detailTitle = await page.title();
          expect(seenTitles.has(detailTitle), `${detailHref} shares its document title with ${seenTitles.get(detailTitle)}`).toBe(false);
          seenTitles.set(detailTitle, detailHref);
          await expectResponsiveContract(page, detailHref, viewport);
          const detailViolations = (await new AxeBuilder({ page }).analyze()).violations
            .filter(({ impact }) => impact === 'serious' || impact === 'critical');
          expect(detailViolations, JSON.stringify(detailViolations, null, 2)).toEqual([]);
          await page.screenshot({ path: path.join(directory, 'admin--membro-detail.png'), fullPage: true });
        }
      }
      await context.close();
    }
  });
}
