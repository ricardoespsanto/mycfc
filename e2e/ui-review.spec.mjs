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
  { key: 'athlete', email: 'review-athlete@example.test', programmeShortcut: 'Atleta de competição', routes: ['/today', '/dashboard/competition', '/treinos'] },
  { key: 'coach', email: 'review-coach@example.test', routes: ['/today', '/events', '/treinos'] },
  { key: 'admin', email: 'review-admin@example.test', routes: ['/today', '/admin/membros', '/admin/noticias', '/admin/fleet'] },
  { key: 'multi', email: 'review-multi@example.test', programmeShortcut: 'Lazer', routes: ['/today', '/dashboard/leisure', '/dashboard/competition', '/events'] },
];
const viewports = [
  { key: 'desktop', width: 1440, height: 900 },
  { key: 'mobile', width: 375, height: 812 },
];
async function expectResponsiveContract(page, route, viewport) {
  for (const width of [320, 375, 768]) {
    await page.setViewportSize({ width, height: 720 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} overflows at ${width}px`).toBeLessThanOrEqual(width);
  }

  // A 640 CSS-pixel layout is the effective viewport at 200% browser zoom
  // from the agreed 1280-pixel desktop baseline.
  await page.setViewportSize({ width: 640, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} overflows at 200% zoom`).toBeLessThanOrEqual(640);

  await page.setViewportSize({ width: 375, height: 720 });
  const touchTargetSelector = 'button, input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]), select, textarea, summary, label:has(> input[type="checkbox"]), label:has(> input[type="radio"]), .action, .site-nav a, .admin-subnav a, .pagination a';
  const undersizedTargets = await page.evaluate((selector) => [...document.querySelectorAll(selector)]
    .filter((element) => element.getClientRects().length > 0)
    .map((element) => ({ element, rect: element.getBoundingClientRect() }))
    .filter(({ rect }) => rect.width < 44 || rect.height < 44)
    .map(({ element, rect }) => ({ tag: element.tagName, text: element.textContent.trim().slice(0, 60), width: Math.round(rect.width), height: Math.round(rect.height) })), touchTargetSelector);
  expect(undersizedTargets, `${route} has undersized frequent touch targets`).toEqual([]);

  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), `${route} clips enlarged text`).toBeLessThanOrEqual(375);
  await page.evaluate(() => { document.documentElement.style.fontSize = ''; });

  await page.locator('main details').evaluateAll((details) => details.forEach((detail) => {
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
    document.querySelectorAll('main details').forEach((detail) => {
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
    expect(focusedRect.y + focusedRect.height, `${route} focus is obscured below the compact viewport`).toBeLessThanOrEqual(400);
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
  await expect(page.getByRole('heading', { name: 'Avisos recentes' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Continuar no MyCFC' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Eventos Agenda e respostas/ })).toBeVisible();
  if (persona.key === 'tutor') {
    await expect(page.getByRole('heading', { name: 'Menores a cargo' })).toBeVisible();
  }
  if (persona.programmeShortcut) {
    await expect(page.getByRole('link', { name: `${persona.programmeShortcut} Abrir o meu espaço` })).toBeVisible();
  }
  if (persona.key === 'admin') {
    await expect(page.getByRole('heading', { name: 'Requer atenção' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Abrir frota' })).toBeVisible();
  }
}

for (const persona of personas) {
  test(`captures ${persona.key} desktop and mobile journeys`, async ({ browser }) => {
    test.setTimeout(180_000);
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
		await expectAccessibilityContract(page, route);
		const routeTitle = await page.title();
		expect(seenTitles.has(routeTitle), `${route} shares its document title with ${seenTitles.get(routeTitle)}`).toBe(false);
		seenTitles.set(routeTitle, route);
		await expectResponsiveContract(page, route, viewport);
        const violations = (await new AxeBuilder({ page }).analyze()).violations
          .filter(({ impact }) => impact === 'serious' || impact === 'critical');
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
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
