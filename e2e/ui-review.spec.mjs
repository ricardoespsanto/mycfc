import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run through make ui-review-screenshots.');

const password = 'correct horse 7';
const outputRoot = path.resolve('artifacts/ui-review');
const personas = [
  { key: 'member', email: 'review-member@example.test', routes: ['/today', '/events', '/announcements'] },
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
const migratedRoutes = new Set(['/events', '/announcements', '/treinos', '/dashboard/guardian', '/dashboard/competition', '/dashboard/leisure', '/admin/noticias', '/admin/fleet']);

async function expectMigratedResponsiveContract(page, route, viewport) {
  if (!migratedRoutes.has(route)) return;
  await page.setViewportSize({ width: 320, height: 720 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
  await page.setViewportSize({ width: viewport.width, height: viewport.height });
  await page.evaluate(() => { document.documentElement.style.zoom = '2'; });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
    await page.evaluate(() => document.documentElement.clientWidth),
  );
  await page.evaluate(() => { document.documentElement.style.zoom = ''; });
  await page.keyboard.press('Tab');
  await expect(page.locator(':focus')).toBeVisible();
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
    for (const viewport of viewports) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } });
      const page = await context.newPage();
      await login(page, persona.email);
      for (const route of persona.routes) {
        await page.goto(route);
        await expect(page.locator('main h1')).toBeVisible();
		if (route === '/today') {
		  await expectTodayComposition(page, persona);
		  await page.setViewportSize({ width: 320, height: 720 });
		  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
		  await page.setViewportSize({ width: viewport.width, height: viewport.height });
		}
        await expectMigratedResponsiveContract(page, route, viewport);
        const violations = (await new AxeBuilder({ page }).analyze()).violations
          .filter(({ impact }) => impact === 'serious' || impact === 'critical');
        expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
        const directory = path.join(outputRoot, persona.key, viewport.key);
        await mkdir(directory, { recursive: true });
        const slug = route === '/today' ? 'today' : route.replace(/^\//, '').replaceAll('/', '--');
        await page.screenshot({ path: path.join(directory, `${slug}.png`), fullPage: true });
      }
      await context.close();
    }
  });
}
