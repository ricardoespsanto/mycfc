import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import { axe, evidence, fixture, id, login, publish } from './crew-transition-helpers.mjs';
test.skip(process.env.E2E_CREW_TRANSITION !== '1', 'Disposable crew-transition runner required.');

test('effective-date member retains archive and prescription, loses former future direct URLs, reaches new scope', async ({ browser }) => {
  const admin = await browser.newPage();
  await login(admin, 'admin');
  await publish(admin, 301); // Real publication AFTER effective seeded boundary.
  await admin.close();
  const control = await browser.newPage();
  await login(control, 'delta');
  expect((await control.goto(`/events/${id(601)}`)).status()).toBe(200);
  await expect(control.getByRole('heading', { name: 'Evento futuro antigo âmbito', exact: true })).toBeVisible();
  expect((await control.goto(`/treinos/prescricoes/sessoes/${id(401)}`)).status()).toBe(200);
  await expect(control.getByRole('heading', { name: 'Treino futuro antigo âmbito', exact: true })).toBeVisible();
  const futurePath = new URL(control.url()).pathname;
  fs.writeFileSync(`${evidence}/future-path.json`, JSON.stringify({ futurePath }));
  await control.close();
  const memberContext = await browser.newContext();
  const page = await memberContext.newPage();
  await login(page, 'alfa');
  expect((await page.goto('/dashboard/initiation')).status()).toBe(200);
  expect((await page.goto('/dashboard/leisure')).status()).toBe(403);
  await page.goto('/events?view=past');
  await page.getByRole('link', { name: /Evento histórico preservado/ }).click();
  await expect(page.getByRole('heading', { name: 'Evento histórico preservado', exact: true })).toBeVisible();
  await expect(page.getByText('Vou', { exact: true })).toBeVisible();
  await page.goto(`/events/${id(600)}?view=past`);
  await expect(page.getByRole('heading', { name: 'Evento histórico preservado', exact: true })).toBeVisible();
  for (const path of [`/events/${id(601)}`,futurePath,`/treinos/prescricoes/sessoes/${id(401)}`]) {
    expect((await page.goto(path)).status(), path).toBe(404);
    await expect(page.locator('main')).not.toContainText(/Evento futuro antigo âmbito|Treino futuro antigo âmbito/);
  }
  expect((await page.goto(`/events/${id(602)}`)).status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Evento futuro novo âmbito', exact: true })).toBeVisible();
  await page.goto('/treinos/estruturados');
  await expect(page.locator('main')).toContainText('Treino histórico preservado');
  await expect(page.locator('main')).not.toContainText('Treino futuro antigo âmbito');
  const prescription = fixture().prescriptions[`${id(11)}:${id(400)}`];
  expect(prescription).toBeTruthy();
  expect((await page.goto(`/treinos/prescricoes/${prescription}`)).status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Treino histórico preservado', exact: true })).toBeVisible();
  await expect(page.getByText('Prescrição sintética preservada', { exact: true })).toBeVisible();
  await axe(page, 'historical prescription after effective transition');
  await memberContext.close();
});

test('current guardian retains dependent archive and published training after transition, never former future audience', async ({ page, context }) => {
  await login(page, 'guardian');
  expect((await page.goto(`/events/${id(600)}?view=past&subject_user_id=${id(17)}`)).status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Evento histórico preservado', exact: true })).toBeVisible();
  expect((await page.goto(`/events/${id(601)}?subject_user_id=${id(17)}`)).status()).toBe(404);
  await expect(page.locator('main')).not.toContainText('Evento futuro antigo âmbito');
  const prescription = fixture().prescriptions[`${id(17)}:${id(400)}`];
  expect(prescription).toBeTruthy();
  expect((await page.goto(`/treinos/prescricoes/${prescription}`)).status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Treino histórico preservado', exact: true })).toBeVisible();
  await axe(page, 'guardian historical prescription');
  await context.storageState({ path: `${evidence}/guardian-session.json` });
});

test('independent coach grant still reaches old scope after athlete moves and session is retained for revocation', async ({ page, context }) => {
  await login(page, 'coach');
  expect((await page.goto('/admin/treinos/estruturados#training-variations')).status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Tripulação K4 sobreposta', exact: true })).toBeVisible();
  await context.storageState({ path: `${evidence}/coach-session.json` });
});
