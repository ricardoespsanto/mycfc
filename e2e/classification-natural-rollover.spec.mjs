import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import { axe, id, login, publish } from './crew-transition-helpers.mjs';
const phase = process.env.E2E_NATURAL_ROLLOVER_PHASE;
test.skip(!['publish', 'before', 'after'].includes(phase), 'Natural-clock isolated runner required.');
const out = process.env.E2E_CREW_TRANSITION_EVIDENCE;
const fixture = () => JSON.parse(fs.readFileSync(`${out}/fixture.json`, 'utf8'));
const shot = (page, name) => page.screenshot({path: `${out}/${name}.png`, fullPage: true});
async function memberLogin(page) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('CFC-ACCE0017');
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', {name: 'Iniciar sessão'}).click();
  await expect(page).toHaveURL(/\/today$/);
}

test('same-person classification on the natural Lisbon clock', async ({browser}) => {
  if (phase === 'publish') {
    const adminContext = await browser.newContext({viewport: {width: 390, height: 844}});
    const admin = await adminContext.newPage();
    await login(admin, 'admin');
    await publish(admin, 300);
    await adminContext.close();
    return;
  }
  if (phase === 'before') {
    const coachContext = await browser.newContext({viewport: {width: 390, height: 844}});
    const coach = await coachContext.newPage();
    await login(coach, 'coach');
    await coach.goto('/equipa/classificacao');
    await expect(coach.getByLabel('Nome da pessoa')).toBeFocused();
    await coach.getByLabel('Nome da pessoa').fill('Dependente aceitação');
    await coach.getByRole('button', {name: 'Pesquisar', exact: true}).click();
    await coach.getByRole('link', {name: 'Selecionar Dependente aceitação'}).click();
    await expect(coach.getByRole('heading', {name: /Alterar participação: Dependente aceitação/})).toBeVisible();
    await axe(coach, 'selected same person');
    await shot(coach, 'before-selected-person');
    await coachContext.close();
    const writerContext = await browser.newContext({viewport: {width: 390, height: 844}});
    const writer = await writerContext.newPage();
    await login(writer, 'admin');
    await writer.goto(`/equipa/classificacao/${id(17)}`);
    const option = writer.locator('#scope option').filter({hasText: 'Iniciação'});
    await writer.locator('#scope').selectOption(await option.getAttribute('value'));
    await writer.locator('[name="category_id"]').selectOption(id(101));
    await writer.locator('#starts_on').fill(fixture().tomorrow);
    await writer.locator('#previous_id').selectOption(id(117));
    await writer.getByRole('button', {name: 'Guardar participação', exact: true}).click();
    await expect(writer.locator('#preview-heading')).toBeFocused();
    const preview = writer.getByRole('region', {name: /Rever antes de confirmar/});
    for (const text of ['Lazer', 'Iniciação', 'Escalão aceitação', 'Dependente aceitação']) await expect(preview).toContainText(text);
    await shot(writer, 'before-preview');
    await writer.getByRole('button', {name: 'Confirmar participação', exact: true}).click();
    await expect(writer.getByRole('region', {name: 'Histórico de participação'})).toContainText('Escalão aceitação');
    await shot(writer, 'before-confirmed');
    await writerContext.close();
  } else {
    const adminContext = await browser.newContext();
    const admin = await adminContext.newPage();
    await login(admin, 'admin');
    await publish(admin, 301);
    await adminContext.close();
    const controlContext = await browser.newContext();
    const control = await controlContext.newPage();
    await login(control, 'delta');
    expect((await control.goto(`/events/${id(601)}`)).status()).toBe(200);
    expect((await control.goto(`/treinos/prescricoes/sessoes/${id(401)}`)).status()).toBe(200);
    await controlContext.close();
  }
  const guardianContext = await browser.newContext({viewport: {width: 390, height: 844}});
  const guardian = await guardianContext.newPage();
  await login(guardian, 'guardian');
  expect((await guardian.goto(`/events/${id(600)}?view=past&subject_user_id=${id(17)}`)).status()).toBe(200);
  expect((await guardian.goto(`/events/${id(601)}?subject_user_id=${id(17)}`)).status()).toBe(phase === 'before' ? 200 : 404);
  expect((await guardian.goto(`/events/${id(602)}?subject_user_id=${id(17)}`)).status()).toBe(phase === 'before' ? 404 : 200);
  if (phase === 'after') {
    expect((await guardian.goto(`/treinos/prescricoes/sessoes/${id(401)}`)).status()).toBe(404);
    const prescription = fixture().prescriptions[`${id(17)}:${id(400)}`];
    expect(prescription).toBeTruthy();
    expect((await guardian.goto(`/treinos/prescricoes/${prescription}`)).status()).toBe(200);
    await expect(guardian.getByRole('heading', {name: 'Treino histórico preservado', exact: true})).toBeVisible();
    await axe(guardian, 'guardian retained history');
  }
  await shot(guardian, `${phase}-guardian`);
  await guardianContext.close();
  const memberContext = await browser.newContext({viewport: {width: 390, height: 844}});
  const member = await memberContext.newPage();
  await memberLogin(member);
  expect((await member.goto('/dashboard/initiation')).status()).toBe(phase === 'before' ? 403 : 200);
  expect((await member.goto('/dashboard/leisure')).status()).toBe(phase === 'before' ? 200 : 403);
  expect((await member.goto(`/events/${id(600)}?view=past`)).status()).toBe(200);
  expect((await member.goto(`/events/${id(601)}`)).status()).toBe(phase === 'before' ? 200 : 404);
  expect((await member.goto(`/events/${id(602)}`)).status()).toBe(phase === 'before' ? 404 : 200);
  if (phase === 'after') {
    expect((await member.goto(`/treinos/prescricoes/sessoes/${id(401)}`)).status()).toBe(404);
    const prescription = fixture().prescriptions[`${id(17)}:${id(400)}`];
    expect((await member.goto(`/treinos/prescricoes/${prescription}`)).status()).toBe(200);
    await expect(member.getByRole('heading', {name: 'Treino histórico preservado', exact: true})).toBeVisible();
    await member.goto('/treinos/estruturados');
    await expect(member.locator('main')).toContainText('Treino histórico preservado');
    await expect(member.locator('main')).not.toContainText('Treino futuro antigo âmbito');
    await axe(member, 'member retained history');
  }
  await shot(member, `${phase}-member`);
  await memberContext.close();
});
