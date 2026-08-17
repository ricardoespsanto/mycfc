import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

test.skip(process.env.UI_REVIEW !== '1', 'Run against the deterministic UI-review dataset.');

const password = 'correct horse 7';

async function login(page, email = 'review-admin@example.test') {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

test('mobile navigation drawer contains focus and closes predictably', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 375, height: 640 } });
  const page = await context.newPage();
  await login(page);

  const trigger = page.getByRole('button', { name: 'Menu', exact: true });
  const fallback = page.locator('[data-mobile-navigation-fallback]');
  await expect(trigger).toBeVisible();
  await expect(fallback).toBeHidden();
  await expect(trigger).toHaveAttribute('aria-controls', 'mobile-navigation');

  await trigger.click();
  const drawer = page.getByRole('dialog', { name: 'Menu MyCFC' });
  await expect(drawer).toBeVisible();
  await expect(trigger).toHaveAttribute('aria-expanded', 'true');
  await expect(page.locator('body')).toHaveClass(/mobile-navigation-open/);
  await expect(drawer.getByRole('button', { name: 'Fechar' })).toBeFocused();
  await expect(drawer.getByRole('link', { name: 'Gerir eventos', exact: true })).toHaveAttribute('href', '/admin/eventos');
  await expect(drawer.getByRole('link', { name: 'Planear treinos', exact: true })).toHaveAttribute('href', '/admin/treinos');
  await expect(drawer.getByRole('link', { name: 'Gerir avisos', exact: true })).toHaveAttribute('href', '/admin/avisos');
  await expect(page.getByRole('link', { name: /^Avisos,/ })).toHaveCount(1);
  await expect(drawer.getByRole('link', { name: 'Gerir avisos', exact: true })).toHaveCount(1);

  const logout = drawer.getByRole('button', { name: 'Terminar sessão' });
  await logout.focus();
  await page.keyboard.press('Tab');
  await expect(drawer.getByRole('button', { name: 'Fechar' })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(logout).toBeFocused();

  const violations = (await new AxeBuilder({ page }).include('#mobile-navigation').analyze()).violations
    .filter(({ impact }) => impact === 'serious' || impact === 'critical');
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);

  await page.keyboard.press('Escape');
  await expect(drawer).toBeHidden();
  await expect(trigger).toBeFocused();
  await expect(page.locator('body')).not.toHaveClass(/mobile-navigation-open/);

  await trigger.click();
  await page.goBack();
  await expect(drawer).toBeHidden();
  await expect(trigger).toBeFocused();
  await expect(page).toHaveURL(/\/today$/);

  await page.setViewportSize({ width: 600, height: 640 });
  await trigger.click();
  await page.mouse.click(20, 320);
  await expect(drawer).toBeHidden();
  await expect(trigger).toBeFocused();

  await page.setViewportSize({ width: 375, height: 640 });
  await trigger.click();
  await expect(drawer).toHaveAttribute('open', '');
  await page.setViewportSize({ width: 1024, height: 700 });
  await expect(page.locator('#mobile-navigation')).not.toHaveAttribute('open', '');
  await expect(page.locator('body')).not.toHaveClass(/mobile-navigation-open/);
  await expect(page.locator('[data-mobile-navigation-open]')).toHaveAttribute('aria-expanded', 'false');
  await expect.poll(() => page.evaluate(() => window.history.state?.mycfcNavigation === true)).toBe(false);
  await expect(page).toHaveURL(/\/today$/);
  await context.close();
});

test('mobile navigation supports compact height, panel exclusivity and route selection', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 320, height: 480 } });
  const page = await context.newPage();
  await login(page);
  const trigger = page.getByRole('button', { name: 'Menu', exact: true });
  await trigger.click();
  const drawer = page.getByRole('dialog', { name: 'Menu MyCFC' });
  await expect(drawer).toBeVisible();
  const geometry = await drawer.evaluate((element) => {
    const content = element.querySelector('.mobile-navigation-drawer__content');
    return {
      height: element.getBoundingClientRect().height,
      viewport: window.innerHeight,
      overflow: getComputedStyle(content).overflowY,
    };
  });
  expect(geometry.height).toBeLessThanOrEqual(geometry.viewport);
  expect(geometry.overflow).toBe('auto');

  await page.evaluate(() => document.querySelector('[data-announcement-trigger]')?.click());
  await expect(drawer).toBeHidden();
  await expect(page.locator('[data-announcement-panel]')).toBeVisible();
  await page.evaluate(() => document.querySelector('[data-mobile-navigation-open]')?.click());
  await expect(drawer).toBeVisible();
  await expect(page.locator('[data-announcement-panel]')).toBeHidden();

  await drawer.getByRole('link', { name: 'Gerir eventos', exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/eventos$/);
  await context.close();
});

test('mobile navigation keeps a usable no-JavaScript fallback', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 375, height: 640 } });
  const page = await context.newPage();
  await login(page);

  await expect(page.locator('[data-mobile-navigation-open]')).toBeHidden();
  const fallback = page.locator('[data-mobile-navigation-fallback]');
  await expect(fallback).toBeVisible();
  await fallback.locator('summary').click();
  await expect(fallback).toHaveAttribute('open', '');
  await expect(fallback.getByRole('navigation', { name: 'Navegação principal' })).toBeVisible();
  await expect(fallback.getByRole('link', { name: 'Gerir eventos', exact: true })).toHaveAttribute('href', '/admin/eventos');
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375);
  await context.close();
});

test('mobile shell and Today leaderboard reflow at 200 percent text size', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 375, height: 720 } });
  const page = await context.newPage();
  await login(page, 'review-tutor@example.test');

  for (const route of ['/dashboard/guardian', '/today']) {
    await page.goto(route);
    await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
    const layout = await page.evaluate(() => ({
      documentWidth: document.documentElement.scrollWidth,
      headerWidth: Math.ceil(document.querySelector('.mobile-app-header').getBoundingClientRect().width),
      viewportWidth: window.innerWidth,
      overflowers: [...document.querySelectorAll('*')]
        .filter((element) => element.getBoundingClientRect().right > window.innerWidth + 1)
        .slice(0, 8)
        .map((element) => ({ tag: element.tagName, className: element.className, right: Math.ceil(element.getBoundingClientRect().right) })),
    }));
    expect(layout.documentWidth, `${route}: ${JSON.stringify(layout.overflowers)}`).toBeLessThanOrEqual(layout.viewportWidth);
    expect(layout.headerWidth).toBeLessThanOrEqual(layout.viewportWidth);
    await expect(page.getByRole('link', { name: 'MyCFC' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Menu', exact: true })).toBeVisible();
    await page.evaluate(() => { document.documentElement.style.fontSize = ''; });
  }

  await context.close();
});

test('manager routes expose their owning coordination or moderation area', async ({ page }) => {
  await login(page);
  for (const route of [
    { path: '/admin/treinos', area: 'Coordenação', current: 'Planear treinos' },
    { path: '/admin/avisos', area: 'Coordenação', current: 'Gerir avisos' },
    { path: '/admin/albuns', area: 'Moderação', current: 'Gerir álbuns' },
    { path: '/admin/sugestoes', area: 'Moderação', current: 'Triar sugestões' },
  ]) {
    await page.goto(route.path);
    await expect(page.locator('.page-header .eyebrow')).toHaveText(route.area);
    await expect(page.locator('.app-rail').getByRole('link', { name: route.current, exact: true })).toHaveAttribute('aria-current', 'page');
  }

  await page.goto('/admin/eventos');
  const calendar = page.getByRole('region', { name: /^Calendário de eventos - / });
  await expect(calendar).toHaveAttribute('tabindex', '0');
  await calendar.focus();
  await expect(calendar).toBeFocused();
});
