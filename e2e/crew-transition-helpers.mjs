import { expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
export const id = n => `33733900-0000-0000-0000-${String(n).padStart(12, '0')}`;
export const evidence = process.env.E2E_CREW_TRANSITION_EVIDENCE;
export const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
export async function login(page, actor) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(`acceptance-${actor}@example.test`);
  await page.getByLabel('Palavra-passe').fill('correct horse 7'); // Synthetic fixture only.
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}
export async function axe(page, state) {
  const violations = (await new AxeBuilder({ page }).analyze()).violations.filter(v => ['serious', 'critical'].includes(v.impact));
  expect(violations, `${state}: ${JSON.stringify(violations)}`).toEqual([]);
}
export async function postUI(page, button, path, expected = 303) {
  const [response] = await Promise.all([page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes(path)), button.click()]);
  expect(response.status()).toBe(expected);
  await page.waitForLoadState('networkidle');
}
export async function publish(page, plan) {
  await page.goto('/admin/treinos/estruturados#training-publication');
  const form = page.locator(`form[action$="/semanas/${id(plan)}/publicar"]`);
  await expect(form).toBeVisible();
  await form.locator('[name="change_summary"]').fill('Publicação sintética de aceitação');
  await postUI(page, form.getByRole('button'), `/semanas/${id(plan)}/publicar`);
}
// Kept apart from crew/history helpers: preview integration may change only this flow.
export async function submitTomorrowTransition(page) {
  await page.goto(`/equipa/classificacao/${id(15)}`);
  const form = page.locator('form').first();
  const scope = form.locator('#scope');
  const value = await scope.locator('option').filter({ hasText: 'Iniciação' }).getAttribute('value');
  await scope.selectOption(value);
  await form.locator('[name="category_id"]').selectOption(id(101));
  await form.locator('[name="previous_id"]').selectOption(id(115));
  await form.locator('#starts_on').fill(fixture().tomorrow);
  await postUI(page, form.getByRole('button', { name: /Guardar/ }), `/equipa/classificacao/${id(15)}`, 200);
  await postUI(page, page.getByRole('button', { name: 'Confirmar participação', exact: true }), `/equipa/classificacao/${id(15)}`, 200);
  await expect(page.getByRole('status')).toContainText(`Atleta transição UI — Guardada: Initiation a partir de ${fixture().tomorrow}.`);
}
