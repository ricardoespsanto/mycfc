import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_DATED_PLACEMENTS !== '1', 'Use the new-owned-resource dated-placement runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const base = '/admin/treinos/estruturados/colocacoes';
const groups = '/admin/treinos/estruturados/grupos';
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const plus = (date, n) => { const value = new Date(`${date}T12:00:00Z`); value.setUTCDate(value.getUTCDate() + n); return value.toISOString().slice(0, 10); };
const today = () => fixture().synthetic_dates.fixture_date;
const future = () => plus(fixture().synthetic_dates.fixture_week, 1);
const sourceName = 'Grupo de água exclusivamente sintético';
const targetName = 'Destino temporário sintético';
const form = (page, suffix) => page.locator(`form[action="${base}${suffix}"]`);
const live = rows => rows.filter(row => !row.cancelled_by_change_id && !row.superseded_by_change_id);
const personPlacements = (facts, person) => facts.placements.filter(row => row.subject_user_id === id(person));
const ownRows = (facts, person) => facts.prescriptions.filter(row => row.athlete_user_id === id(person));

async function facts(name) {
  fs.writeFileSync(`${evidence}/${name}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${name}-done.json`)).toBe(true);
  const result = JSON.parse(fs.readFileSync(`${evidence}/${name}-done.json`, 'utf8'));
  expect(result.error).toBeUndefined(); return result;
}
async function login(context, email = 'water-coach@example.test') {
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click();
  await expect(page).toHaveURL(/\/today$/); return page;
}
async function submit(page, button, suffix, status = 200, keyboard = false) {
  expect(await button.evaluate(el => el.formNoValidate ? [] : [...el.form.elements].filter(c => c.willValidate && !c.validity.valid).map(c => c.name))).toEqual([]);
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix));
  if (keyboard) { await button.focus(); await expect(button).toBeFocused(); await page.keyboard.press('Enter'); } else await button.click();
  const response = await pending; expect(response.status()).toBe(status); await page.waitForLoadState('networkidle');
  expect(response.headers()['cache-control']).toContain('no-store'); expect(response.headers()['referrer-policy']).toBe('no-referrer');
  return response;
}
async function layout(page, name, js) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const references = await page.evaluate(() => {
    const ids = [...document.querySelectorAll('[id]')].map(el => el.id);
    const invalidReferences = [...document.querySelectorAll('[aria-describedby], [aria-labelledby], label[for]')].flatMap(el => ['aria-describedby', 'aria-labelledby', 'for'].flatMap(attribute => (el.getAttribute(attribute) || '').split(/\s+/).filter(Boolean).filter(target => !document.getElementById(target))));
    return { duplicate: ids.filter((value, index) => ids.indexOf(value) !== index), invalidReferences };
  });
  expect(references).toEqual({ duplicate: [], invalidReferences: [] });
  if (js) {
    const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(scan));
    expect(scan.violations.filter(item => ['serious', 'critical'].includes(item.impact))).toEqual([]);
  }
  fs.writeFileSync(`${evidence}/${name}.html`, await page.content());
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
async function search(page, person, date = today()) {
  await page.goto(`${groups}/${id(200)}/atletas`);
  await expect(page.getByLabel('Época', { exact: false })).toHaveValue('');
  await expect(page.getByLabel('Data da colocação', { exact: false })).toHaveValue('');
  await expect(page.locator('input[name="membership_id"]')).toHaveCount(0);
  await page.getByLabel('Época', { exact: false }).selectOption(id(100));
  await page.getByLabel('Data da colocação', { exact: false }).fill(date);
  await page.getByLabel('Nome do atleta', { exact: false }).fill(person === 7000 ? 'Colocação nativa' : 'Colocação imediata');
  await page.getByRole('button', { name: 'Pesquisar', exact: true }).click();
  const proof = page.locator('form[method="post"] input[name="selection_proof"]');
  await expect(proof).not.toHaveValue(''); expect(new URL(page.url()).searchParams.has('selection_proof')).toBe(false);
  for (const name of ['Colocação inativa sintética', 'Colocação outra participação secreta', 'placement-native@example.test', '1990-01-01']) await expect(page.locator('main')).not.toContainText(name);
}
async function join(page, person, js) {
  await search(page, person); await layout(page, `placement-${person}-search320`, js);
  const body = page.locator('form[method="post"]').filter({ has: page.locator('input[name="selection_proof"]') });
  await body.locator(`[name="membership_id"][value="${id(person + 100)}"]`).check();
  const before = await facts(`placement-${person}-before-join`);
  await submit(page, body.getByRole('button', { name: 'Adicionar atletas', exact: true }), `/grupos/${id(200)}/atletas`, 303, true);
  const after = await facts(`placement-${person}-joined`);
  expect(live(personPlacements(after, person))).toHaveLength(1);
  expect(live(personPlacements(after, person))[0]).toMatchObject({ group_id: id(200), starts_on: today(), ends_on: null });
  expect(after.publications).toEqual(before.publications); expect(after.prescriptions).toEqual(before.prescriptions);
  return after;
}
async function editor(page, person, date = future(), source = '') {
  // Discover the person through the scoped search; the link supplies identity.
  await search(page, person, date);
  await page.getByRole('link', { name: 'Preparar colocação', exact: true }).click();
  const url = new URL(page.url()); expect(url.pathname).toBe(base); expect(url.searchParams.get('person_id')).toBe(id(person));
  await expect(page.getByLabel('Época', { exact: false })).toHaveValue(id(100));
  if (source) await page.getByLabel('Colocação de origem', { exact: false }).selectOption(source);
  return form(page, '/prever');
}
async function previewMove(page, person, source, { kind = 'TEMPORARY', date = future(), end = plus(future(), 2), target = id(201), js = false, label = 'move', invalidFirst = false, back = false } = {}) {
  const edit = await editor(page, person, date, source);
  await edit.locator('[name="kind"]').selectOption(kind); await edit.locator('[name="target_group_id"]').selectOption(target);
  await edit.locator('[name="effective_on"]').fill(date); await edit.locator('[name="temporary_until"]').fill(kind === 'TEMPORARY' ? end : '');
  const before = await facts(`${label}-before-preview`), command = await edit.locator('[name="command_id"]').inputValue();
  if (invalidFirst) {
    await edit.locator('[name="temporary_until"]').fill(plus(date, -1));
    await submit(page, edit.getByRole('button', { name: 'Rever alteração', exact: true }), '/prever', 422);
    await expect(page.locator('.error-summary')).toBeFocused();
    await expect(form(page, '/prever').locator('[name="command_id"]')).toHaveValue(command);
    await expect(page.getByLabel('Último dia da colocação temporária', { exact: false })).toHaveValue(plus(date, -1));
    expect(await facts(`${label}-invalid-no-write`)).toEqual(before);
    await page.getByLabel('Último dia da colocação temporária', { exact: false }).fill(end);
  }
  await submit(page, form(page, '/prever').getByRole('button', { name: 'Rever alteração', exact: true }), '/prever');
  let proof = { proposal: await form(page, '/confirmar').locator('[name="proposal"]').inputValue(), review_token: await form(page, '/confirmar').locator('[name="review_token"]').inputValue() };
  expect(proof.proposal).not.toBe(''); expect(proof.review_token).not.toBe(''); expect(await facts(`${label}-preview-no-write`)).toEqual(before);
  expect(new URL(page.url()).searchParams.has('review_token')).toBe(false);
  if (kind === 'TEMPORARY') {
    await expect(page.getByRole('heading', { name: 'Regresso ao grupo anterior', exact: true })).toBeVisible();
    const review = page.locator('section[aria-labelledby="placement-reviewed-title"]');
    await expect(review).toContainText(sourceName); await expect(review).toContainText(plus(end, 1));
    await expect(page.locator('#placement-after')).toContainText(plus(date, -1));
    await expect(page.locator('#placement-after')).toContainText(targetName); await expect(page.locator('#placement-after')).toContainText(end);
  }
  await layout(page, `${label}-review320`, js);
  if (back) {
    const response = await submit(page, page.getByRole('button', { name: 'Continuar a editar', exact: true }), base);
    const sent = new URLSearchParams(response.request().postData());
    expect(sent.get('proposal')).toBe(proof.proposal); expect(sent.get('review_token')).toBe(proof.review_token);
    await expect(form(page, '/prever').locator('[name="command_id"]')).toHaveValue(command);
    await expect(form(page, '/prever').locator('[name="temporary_until"]')).toHaveValue(end);
    expect(await facts(`${label}-back-no-write`)).toEqual(before);
    await submit(page, form(page, '/prever').getByRole('button', { name: 'Rever alteração', exact: true }), '/prever');
    proof = { proposal: await form(page, '/confirmar').locator('[name="proposal"]').inputValue(), review_token: await form(page, '/confirmar').locator('[name="review_token"]').inputValue() };
    expect(await facts(`${label}-repreview-no-write`)).toEqual(before);
  }
  return { proof, command, before };
}
async function confirmMove(page) { return submit(page, form(page, '/confirmar').getByRole('button', { name: 'Confirmar alteração', exact: true }), '/confirmar', 303, true); }
async function exactReplay(page, response, before, label) {
  // Exact native request replay in the same authenticated CSRF session; no mock or injected authority.
  const result = await page.request.post(response.url(), { data: response.request().postData(), headers: { 'content-type': 'application/x-www-form-urlencoded' }, maxRedirects: 0 });
  expect(result.status()).toBe(303); expect(result.headers().location).toBe(response.headers().location);
  expect(await facts(label)).toEqual(before);
}
function history(before, after) {
  for (const key of ['publications', 'prescriptions', 'outcomes']) for (const row of before[key]) expect(after[key].find(next => next.id ? next.id === row.id : next.session_id === row.session_id && next.user_id === row.user_id)).toEqual(row);
  expect(after.memberships).toEqual(before.memberships);
}

test('native no-JS320 dated JOIN temporary exact return stale proof cancellation and replay', async ({ browser }) => {
  test.setTimeout(180_000);
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } });
  const page = await login(context);
  try {
    const joined = await join(page, 7000, false), original = live(personPlacements(joined, 7000))[0];
    const reviewed = await previewMove(page, 7000, original.id, { label: 'temporary-native', invalidFirst: true, back: true });
    const stale = await context.newPage();
    const staleEnd = plus(future(), 4), staleReview = await previewMove(stale, 7000, original.id, { end: staleEnd, label: 'temporary-competing' });
    const response = await confirmMove(page), saved = await facts('temporary-native-saved'); history(joined, saved);
    expect(live(personPlacements(saved, 7000)).map(row => ({ group: row.group_id, start: row.starts_on, end: row.ends_on })).sort((a, b) => a.start.localeCompare(b.start))).toEqual([
      { group: id(200), start: today(), end: plus(future(), -1) },
      { group: id(201), start: future(), end: plus(future(), 2) },
      { group: id(200), start: plus(future(), 3), end: null },
    ]);
    await exactReplay(page, response, saved, 'temporary-native-exact-replay');
    await submit(stale, form(stale, '/confirmar').getByRole('button', { name: 'Confirmar alteração', exact: true }), '/confirmar', 409);
    await expect(stale.locator('section[aria-labelledby="placement-recovery-title"]')).toBeFocused();
    await expect(stale.locator('[name="command_id"]')).toHaveValue(staleReview.command);
    await expect(stale.getByLabel('Último dia da colocação temporária', { exact: false })).toHaveValue(staleEnd);
    await expect(stale.locator('#placement-retained-proposal')).toHaveValue(staleReview.proof.proposal);
    await expect(stale.locator('#placement-retained-token')).toHaveValue(staleReview.proof.review_token);
    expect(await facts('temporary-native-stale-no-write')).toEqual(saved); await layout(stale, 'temporary-native-stale320', false);
    await expect(page.getByRole('status')).toContainText('Pedido registado');
    const temporaryChange = saved.placement_changes.find(row => row.subject_user_id === id(7000) && row.kind === 'TEMPORARY');
    const cancelLinks = page.locator(`a[href*="change_id=${temporaryChange.id}"]`).filter({ hasText: 'Rever cancelamento desta alteração' });
    expect(await cancelLinks.count()).toBeGreaterThan(0); await cancelLinks.first().click();
    await submit(page, page.getByRole('button', { name: 'Rever cancelamento', exact: true }), '/cancelar/prever');
    const cancelBefore = await facts('temporary-native-cancel-preview'); expect(cancelBefore).toEqual(saved);
    await layout(page, 'temporary-native-cancel-review320', false);
    const cancelled = await submit(page, form(page, '/cancelar/confirmar').getByRole('button', { name: 'Confirmar alteração', exact: true }), '/cancelar/confirmar', 303, true);
    const final = await facts('temporary-native-cancelled'); history(joined, final);
    expect(live(personPlacements(final, 7000))).toHaveLength(1);
    expect(live(personPlacements(final, 7000))[0]).toMatchObject({ id: original.id, starts_on: today(), ends_on: null });
    await exactReplay(page, cancelled, final, 'temporary-native-cancel-replay');
    fs.writeFileSync(`${evidence}/native-placement-proof.json`, JSON.stringify({ reviewed, staleReview, exactReturnVerified: true, beforeEffectCancellation: true, clockBoundaryObserved: false }, null, 2));
  } finally { await context.close(); }
});

async function createWater(page, group, plan, title) {
  const day = future();
  await page.goto(`/admin/treinos/estruturados/semanas/${id(plan)}/treinos/criar?day=${day}`);
  const edit = page.locator('form').filter({ has: page.locator('[name="command"][value="preview"]') });
  await edit.locator('[name="title"]').fill(title); await edit.locator('[name="schedule.start_time"]').fill('16:00');
  await edit.locator('textarea[name$=".instructions"]').first().fill('Instrução sintética igual antes e depois do regresso.');
  await submit(page, edit.getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever');
  const save = page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
  const session = JSON.parse(await save.locator('[name="proposal"]').inputValue()).proposal.session_id;
  await submit(page, save.getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303);
  expect(new URL(page.url()).searchParams.get('group_id')).toBe(id(group)); return session;
}
async function publish(page, group, plan, session, name, expectedCounts = {}) {
  await page.goto(`/admin/treinos/estruturados?group_id=${id(group)}&week_id=${id(plan)}&session_id=${session}&day=${future()}`);
  await page.getByRole('link', { name: 'Rever publicação', exact: true }).click();
  const preview = page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="preview"]') });
  await preview.locator('[name="change_summary"]').fill(name);
  await submit(page, preview.getByRole('button', { name: 'Rever destinatários', exact: true }), '/publicar');
  const summary = page.locator('dl[aria-label="Revisão dos destinatários"]');
  for (const [label, count] of Object.entries(expectedCounts)) await expect(summary.locator('div').filter({ has: page.getByText(label, { exact: true }) }).locator('dd')).toHaveText(String(count));
  await layout(page, `${name}-review320`, true);
  const confirm = page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="confirm"]') });
  await submit(page, confirm.getByRole('button', { name: 'Publicar revisão', exact: true }), '/publicar', 303);
  return facts(`${name}-published`);
}
async function directRead(browser, email, prescription, status, forbidden = '') {
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } });
  const page = await login(context, email);
  try {
    const response = await page.goto(`/treinos/prescricoes/${prescription}`); expect(response.status()).toBe(status);
    if (forbidden) await expect(page.locator('body')).not.toContainText(forbidden);
  } finally { await context.close(); }
}

test('current-effective A to B to A2 withdraws old future instructions and requires new explicit publication', async ({ browser }) => {
  test.setTimeout(240_000);
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }), page = await login(context);
  try {
    await join(page, 7001, true);
    const sourceSession = await createWater(page, 200, 300, 'Instrução futura original do grupo A');
    const first = await publish(page, 200, 300, sourceSession, 'placement-A-first', { 'Atletas abrangidos': 2, Novas: 2 });
    const old = ownRows(first, 7001).find(row => row.session_id === sourceSession), other = ownRows(first, 7000).find(row => row.session_id === sourceSession);
    expect(old.placement_id).toBeTruthy(); expect(first.publications.length).toBeGreaterThan(0); expect(first.prescriptions.length).toBeGreaterThan(0);
    await directRead(browser, 'placement-now@example.test', old.id, 200);
    const source = live(personPlacements(first, 7001))[0];
    await previewMove(page, 7001, source.id, { kind: 'MOVE', date: today(), js: true, label: 'placement-move-to-B' });
    await expect(page.locator('#placement-instructions-title')).toBeVisible(); await confirmMove(page);
    const moved = await facts('placement-now-in-B'); history(first, moved);
    expect(moved.withdrawals.some(row => row.prescription_id === old.id && row.effective_on === today())).toBe(true);
    await directRead(browser, 'placement-now@example.test', old.id, 404, 'Instrução futura original do grupo A');
    await directRead(browser, 'placement-native@example.test', other.id, 200);
    const destinationSession = await createWater(page, 201, 350, 'Instrução futura explícita do grupo B');
    const inB = await publish(page, 201, 350, destinationSession, 'placement-B-first', { 'Atletas abrangidos': 1, Novas: 1 }); history(first, inB);
    const destination = ownRows(inB, 7001).find(row => row.session_id === destinationSession); expect(destination).toBeDefined();
    await directRead(browser, 'placement-now@example.test', destination.id, 200);
    const placementB = live(personPlacements(inB, 7001)).find(row => row.group_id === id(201));
    await previewMove(page, 7001, placementB.id, { kind: 'MOVE', date: today(), target: id(200), js: true, label: 'placement-return-to-A2' });
    await confirmMove(page); const returned = await facts('placement-now-returned'); history(inB, returned);
    await directRead(browser, 'placement-now@example.test', old.id, 404, 'Instrução futura original do grupo A');
    await directRead(browser, 'placement-now@example.test', destination.id, 404, 'Instrução futura explícita do grupo B');
    const final = await publish(page, 200, 300, sourceSession, 'placement-A-explicit-return', { 'Atletas abrangidos': 2, Alteradas: 1, 'Sem alterações': 1 }); history(inB, final);
    const latestPublication = final.publications.filter(row => row.plan_id === id(300)).sort((a, b) => b.revision - a.revision)[0];
    const latest = ownRows(final, 7001).find(row => row.publication_id === latestPublication.id && row.session_id === sourceSession);
    expect(latest.id).not.toBe(old.id); expect(latest.placement_id).not.toBe(old.placement_id);
    expect(latest.membership_id).toBe(old.membership_id); expect(latest.session_id).toBe(old.session_id);
    expect(latest.snapshot_sha256).toBe(old.snapshot_sha256); expect(latest.snapshot).toEqual(old.snapshot);
    await directRead(browser, 'placement-now@example.test', latest.id, 200);
    await directRead(browser, 'placement-now@example.test', old.id, 404);
    await directRead(browser, 'placement-unrelated@example.test', latest.id, 404, 'Instrução futura original do grupo A');
    for (const email of ['placement-unscoped@example.test', 'placement-revoked@example.test']) {
      const deniedContext = await browser.newContext(), denied = await login(deniedContext, email);
      try {
        const response = await denied.goto(`${base}?group_id=${id(200)}&person_id=${id(7001)}&season_id=${id(100)}&effective_on=${today()}`);
        expect([403, 404]).toContain(response.status()); await expect(denied.locator('body')).not.toContainText('Colocação imediata sintética');
      } finally { await deniedContext.close(); }
    }
    expect(await facts('placement-private-reads-no-write')).toEqual(final);
    fs.writeFileSync(`${evidence}/placement-lineage-proof.json`, JSON.stringify({ old, destination, latest, sameBodyAndMembershipNewPlacement: true, oldRowsPreserved: true, clockBoundaryObserved: false }, null, 2));
  } finally { await context.close(); }
});
