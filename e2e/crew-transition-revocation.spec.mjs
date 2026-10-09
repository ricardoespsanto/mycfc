import { expect, test } from '@playwright/test';
import { evidence, fixture, id, login } from './crew-transition-helpers.mjs';
test.skip(process.env.E2E_CREW_TRANSITION !== '1', 'Disposable crew-transition runner required.');

test('same guardian session loses direct historical event and prescription access immediately on revocation', async ({ browser }) => {
  const context = await browser.newContext({ storageState: `${evidence}/guardian-session.json` });
  const page = await context.newPage();
  for (const path of [`/events/${id(600)}?view=past&subject_user_id=${id(17)}`, `/treinos/prescricoes/${fixture().prescriptions[`${id(17)}:${id(400)}`]}`]) {
    expect((await page.goto(path)).status(), path).toBe(404);
    await expect(page.locator('main')).not.toContainText(/Evento histórico preservado|Treino histórico preservado|Dependente aceitação/);
  }
  await page.goto('/events?view=past');
  await expect(page.locator('main')).not.toContainText('Evento histórico preservado');
  await context.close();
});

test('same revoked coach session cannot reach crew management and unrelated member remains barred', async ({ browser }) => {
  const context = await browser.newContext({ storageState: `${evidence}/coach-session.json` });
  const page = await context.newPage();
  expect((await page.goto('/admin/treinos/estruturados')).status()).toBe(403);
  await expect(page.locator('main')).not.toContainText('Tripulação K4 sobreposta');
  await context.close();
  const member = await browser.newPage();
  await login(member, 'delta');
  expect((await member.goto('/admin/treinos/estruturados')).status()).toBe(403);
  await member.close();
});
