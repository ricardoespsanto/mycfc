import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_PARTIAL_PUBLICATION !== '1', 'Use the isolated partial-publication runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const plus = (day, count) => { const d = new Date(`${day}T12:00:00Z`); d.setUTCDate(d.getUTCDate() + count); return d.toISOString().slice(0, 10); };
const reuse = '/admin/treinos/estruturados/reutilizar';
const reuseForm = page => page.locator(`form[action="${reuse}/rascunho"]`).filter({ has: page.locator('[name="target_group_id"]:not([type="hidden"])') });
const reuseConfirm = page => page.locator(`form[action="${reuse}/confirmar"]`).filter({ has: page.locator('[name="intent"][value="confirm"]') });
const previewForm = page => page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="preview"]') });
const confirmForm = page => page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="confirm"]') });
const inspectForm = page => page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="inspect"]') });
const metadataNames = ['source_updated_at', 'review_token', 'reviewed_latest_id', 'reviewed_latest_revision', 'return_group', 'return_week', 'return_session', 'return_day'];
const personalTitle = 'TÍTULO PRIVADO APENAS ADULTO SINTÉTICO';
const commonTitle = 'Atividade de destino original';
const initialNotes = 'Só este destino sintético\nInstruções revistas\tantes de publicar';
const staleSummary = 'Revisão pessoal obsoleta\nResumo\tque deve permanecer';
const plan = (facts, key) => facts.plans[typeof key === 'number' ? id(key) : key];
const rowsFor = (facts, publication) => facts.prescriptions.filter(row => row.publication_id === publication.id);
const publicationsFor = (facts, planID) => facts.publications.filter(row => row.plan_id === planID).sort((a, b) => a.revision - b.revision);

async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const value = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8')); expect(value.error).toBeUndefined(); return value;
}
async function login(context, email = 'water-coach@example.test') {
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click(); await expect(page).toHaveURL(/\/today$/); return page;
}
async function reveal(control) {
  await expect(control).toHaveCount(1);
  for (let i = 0; i < 24 && !(await control.isVisible()); i++) {
    const outer = control.locator('xpath=ancestor::details[not(@open)][last()]'); await expect(outer).toHaveCount(1); await outer.locator(':scope > summary').click();
  }
  await expect(control).toBeVisible();
}
async function post(page, button, suffix, status = 200, keyboard = false) {
  await reveal(button);
  expect(await button.evaluate(el => el.formNoValidate ? [] : [...el.form.elements].filter(c => c.willValidate && !c.validity.valid).map(c => c.name)), 'Actual native constraints must permit submission').toEqual([]);
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix), { timeout: 15_000 });
  if (keyboard) { await button.focus(); await expect(button).toBeFocused(); await page.keyboard.press('Enter'); } else await button.click();
  const response = await pending; expect(response.status()).toBe(status); await page.waitForLoadState('networkidle'); return response;
}
async function layout(page, name, js) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(nodes => nodes.map(n => n.id)); expect(new Set(ids).size).toBe(ids.length);
  if (js) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result)); expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  fs.writeFileSync(`${evidence}/${name}.html`, await page.content()); await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
async function openWeek(page, group, week, day, session = '') {
  const query = new URLSearchParams({ group_id: typeof group === 'number' ? id(group) : group, week_id: typeof week === 'number' ? id(week) : week, day });
  if (session) query.set('session_id', typeof session === 'number' ? id(session) : session);
  await page.goto(`/admin/treinos/estruturados?${query}`);
}
async function contextualCopy(page, sourceSession) {
  const wanted = { source_kind: sourceSession ? 'SESSION' : 'WEEK', source_plan_id: id(310), ...(sourceSession ? { source_session_id: id(sourceSession) } : {}) };
  const links = page.locator(`a[href^="${reuse}"]`); const matches = [];
  for (let i = 0; i < await links.count(); i++) {
    const url = new URL(await links.nth(i).getAttribute('href'), 'http://fixture.test');
    if (Object.entries(wanted).every(([key, value]) => url.searchParams.get(key) === value) && (await links.nth(i).textContent()).trim() === (sourceSession ? 'Copiar sessão' : 'Copiar semana')) matches.push(i);
  }
  expect(matches.length).toBeGreaterThan(0); await links.nth(matches[0]).click();
}
async function setReuse(page, name, value) {
  const control = reuseForm(page).locator(`[name="${name}"]`); await reveal(control);
  if (await control.evaluate(el => el.tagName === 'SELECT')) await control.selectOption(value); else await control.fill(value);
}
async function refreshReuse(page) { await post(page, reuseForm(page).getByRole('button', { name: 'Atualizar opções sem perder os dados', exact: true }), '/reutilizar/rascunho'); }
async function copy(page, sourceSession, targetWeek, title = '') {
  await openWeek(page, 200, 310, plus(fixture().reuse.source_week, sourceSession ? sourceSession - 2000 : 0), sourceSession || ''); await contextualCopy(page, sourceSession);
  await setReuse(page, 'target_group_id', id(201)); await refreshReuse(page);
  await setReuse(page, 'season_id', id(101)); await setReuse(page, 'week_start', targetWeek);
  if (title) { await setReuse(page, 'week_title', title); await setReuse(page, 'week_description', 'Destino explicitamente sintético, sem datas da época do clube.'); }
  await refreshReuse(page); await post(page, reuseForm(page).getByRole('button', { name: 'Pré-visualizar cópia', exact: true }), '/reutilizar/prever');
  const proof = JSON.parse(await reuseConfirm(page).locator('[name="proposal"]').inputValue());
  fs.writeFileSync(`${evidence}/publication-copy-${sourceSession || 'week'}-proof.json`, JSON.stringify(proof, null, 2));
  await post(page, reuseConfirm(page).getByRole('button', { name: 'Confirmar cópia em rascunho', exact: true }), '/reutilizar/confirmar', 303);
  const first = proof.proposal.destinations[0].graph;
  await expect(page).toHaveURL(url => url.pathname === '/admin/treinos/estruturados' && url.searchParams.get('group_id') === id(201) && url.searchParams.get('week_id') === proof.proposal.target_plan_id && url.searchParams.get('session_id') === first.session_id && url.searchParams.get('day') === first.schedule.date && url.hash === '#training-plan');
  return proof;
}
async function edit(page, planID, sessionID, notes) {
  await page.goto(`/admin/treinos/estruturados/semanas/${planID}/treinos/${sessionID}/editar`);
  const form = page.locator('form').filter({ has: page.locator('textarea[name="description"]') }); await form.locator('[name="description"]').fill(notes);
  await post(page, form.getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/editar/prever');
  await post(page, page.getByRole('button', { name: 'Guardar alterações', exact: true }), '/editar/confirmar', 303);
}
async function metadata(form) { const value = {}; for (const name of metadataNames) value[name] = await form.locator(`[name="${name}"]`).inputValue(); return value; }
async function review(page, planID, sessionID, day, summary) {
  await openWeek(page, 201, planID, day, sessionID); await page.getByRole('link', { name: 'Rever publicação', exact: true }).click();
  await previewForm(page).locator('[name="change_summary"]').fill(summary);
  const response = await post(page, previewForm(page).getByRole('button', { name: 'Rever destinatários', exact: true }), '/publicar');
  expect(response.headers()['cache-control']).toContain('no-store'); expect(response.headers()['referrer-policy']).toBe('no-referrer');
  const choice = inspectForm(page).locator('[name="instruction_key"]');
  await expect(choice).toHaveCount(1); expect(await choice.inputValue()).toBe(await choice.locator('option').first().getAttribute('value'));
  await expect(page.getByRole('region', { name: 'Instruções revistas', exact: true })).toHaveCount(1);
  return metadata(confirmForm(page));
}
async function inspect(page, proof, summary, athlete, sessionID, expected, excluded, name, js) {
  const form = inspectForm(page), key = `${id(athlete)}/${sessionID}`;
  expect(await metadata(form)).toEqual(proof); await expect(form.locator('[name="change_summary"]')).toHaveValue(summary);
  const choice = form.locator('[name="instruction_key"]'); await expect(choice.locator(`option[value="${key}"]`)).toHaveCount(1); await choice.selectOption(key);
  const response = await post(page, form.getByRole('button', { name: 'Ver instruções', exact: true }), '/publicar', 200, !js);
  const submitted = new URLSearchParams(response.request().postData()); expect(submitted.get('publication_intent')).toBe('inspect'); expect(submitted.get('instruction_key')).toBe(key);
  for (const [field, value] of Object.entries(proof)) expect(submitted.get(field)).toBe(value);
  expect(submitted.get('change_summary')).toBe(summary.replaceAll('\n', '\r\n'));
  expect(await metadata(confirmForm(page))).toEqual(proof); expect(await metadata(inspectForm(page))).toEqual(proof);
  await expect(confirmForm(page).locator('[name="change_summary"]')).toHaveValue(summary); await expect(inspectForm(page).locator('[name="instruction_key"]')).toHaveValue(key);
  const region = page.getByRole('region', { name: 'Instruções revistas', exact: true }); await expect(region).toHaveCount(1);
  const instructions = region.getByRole('region', { name: 'Instruções do treino', exact: true }); await expect(instructions).toHaveCount(1);
  await expect(region).toContainText(athlete === 6000 ? 'Alvo publicação adulto sintético' : 'Alvo publicação menor sintético');
  for (const text of expected) await expect(instructions).toContainText(text);
  for (const text of excluded) await expect(instructions).not.toContainText(text);
  await layout(page, name, js);
}
async function counts(page, expected) {
  const dl = page.locator('dl[aria-label="Revisão dos destinatários"]');
  for (const [label, number] of Object.entries(expected)) await expect(dl.locator('div').filter({ has: page.getByText(label, { exact: true }) }).locator('dd')).toHaveText(String(number));
  const audience = page.locator('section[aria-labelledby="publication-recipient-title"]');
  await expect(audience.locator('li')).toHaveCount(2);
  for (const name of ['Alvo publicação adulto sintético', 'Alvo publicação menor sintético']) await expect(audience).toContainText(name);
  await expect(audience).not.toContainText('Dependente suspenso'); await expect(audience).not.toContainText('Atleta fonte');
}
async function publish(page, proof, summary, keyboard = false) {
  const response = await post(page, confirmForm(page).getByRole('button', { name: 'Publicar revisão', exact: true }), '/publicar', 303, keyboard);
  const sent = new URLSearchParams(response.request().postData()); for (const [name, value] of Object.entries(proof)) expect(sent.get(name)).toBe(value);
  expect(sent.get('publication_intent')).toBe('confirm'); expect(sent.get('confirm')).toBe('yes'); expect(sent.get('change_summary')).toBe(summary.replaceAll('\n', '\r\n'));
  await expect(page).toHaveURL(url => url.searchParams.get('group_id') === proof.return_group && url.searchParams.get('week_id') === proof.return_week && url.searchParams.get('session_id') === proof.return_session && url.searchParams.get('day') === proof.return_day);
}
function historical(before, after) {
  expect(before.outcomes.length).toBeGreaterThan(0); expect(after.outcomes).toEqual(before.outcomes);
  for (const row of before.publications) expect(after.publications.find(v => v.id === row.id)).toEqual(row);
  for (const row of before.prescriptions) expect(after.prescriptions.find(v => v.id === row.id)).toEqual(row);
  expect(plan(after, 310)).toEqual(plan(before, 310));
  for (const key of ['routines', 'profiles', 'zones', 'memberships', 'guardian_relationships', 'variation_groups', 'variation_group_members']) expect(after[key], key).toEqual(before[key]);
}
function revision(factsValue, planID, number) {
  const pubs = publicationsFor(factsValue, planID); expect(pubs).toHaveLength(number);
  const pub = pubs[number - 1]; expect(pub.revision).toBe(number); const rows = rowsFor(factsValue, pub); expect(rows).toHaveLength(8);
  expect([...new Set(rows.map(row => row.athlete_user_id))].sort()).toEqual([id(6000), id(6002)]);
  expect(new Set(rows.map(row => `${row.session_id}/${row.membership_id}`)).size).toBe(8);
  for (const row of rows) expect(row.snapshot_sha256).toMatch(/^[0-9a-f]{64}$/);
  return { pub, rows };
}
async function privateReads(browser, rows, targetSession, notes, prefix, personal = false) {
  for (const [email, athlete, js] of [['publication-adult@example.test', 6000, true], ['publication-guardian@example.test', 6002, false]]) {
    const context = await browser.newContext({ javaScriptEnabled: js, viewport: { width: 320, height: 1000 } }); const page = await login(context, email);
    try {
      const own = rows.filter(row => row.athlete_user_id === id(athlete)), other = rows.find(row => row.athlete_user_id !== id(athlete));
      await page.goto('/treinos/estruturados');
      for (const row of own) await expect(page.locator(`a[href="/treinos/prescricoes/${row.id}"]`)).toHaveCount(1);
      await expect(page.locator(`a[href="/treinos/prescricoes/${other.id}"]`)).toHaveCount(0);
      const selected = own.find(row => row.session_id === targetSession); expect(selected).toBeDefined();
      const response = await page.goto(`/treinos/prescricoes/${selected.id}`); expect(response.status()).toBe(200);
      await expect(page.locator('body')).toContainText(notes);
      if (personal) {
        await expect(page.locator('body')).toContainText(athlete === 6000 ? personalTitle : commonTitle);
        if (athlete !== 6000) await expect(page.locator('body')).not.toContainText(personalTitle);
      }
      await layout(page, `${prefix}-${athlete}-private320`, js);
      const resolved = await page.goto(`/treinos/prescricoes/sessoes/${targetSession}`); expect(resolved.status()).toBe(200); await expect(page.locator('body')).toContainText(notes);
      const denied = await page.goto(`/treinos/prescricoes/${other.id}`); expect(denied.status()).toBe(404); await expect(page.locator('body')).not.toContainText(personalTitle);
    } finally { await context.close(); }
  }
}

test('native no-JS320 actual roster copy edit review and explicit partial-week publication has private current readers', async ({ browser }) => {
  test.setTimeout(150_000);
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } }); const page = await login(context);
  try {
    const initial = await facts('publication-native-initial');
    expect(initial.group_members.filter(row => [id(6100), id(6102)].includes(row.membership_id))).toEqual([]);
    await page.goto(`/admin/treinos/estruturados/grupos/${id(201)}`); await page.getByRole('link', { name: 'Adicionar atletas', exact: true }).click();
    await page.getByLabel('Nome do atleta', { exact: true }).fill('Alvo publicação'); await page.getByRole('button', { name: 'Pesquisar', exact: true }).click();
    for (const membership of [6100, 6102]) await page.locator(`input[type="checkbox"][name="membership_id"][value="${id(membership)}"]`).check();
    await post(page, page.getByRole('button', { name: 'Adicionar atletas', exact: true }), `/grupos/${id(201)}/atletas`, 303, true);
    const roster = await facts('publication-native-roster'); historical(initial, roster);
    const additions = roster.group_members.filter(row => !initial.group_members.some(old => old.group_id === row.group_id && old.membership_id === row.membership_id));
    expect(additions.map(row => [row.group_id, row.membership_id]).sort()).toEqual([[id(201), id(6100)], [id(201), id(6102)]]);
    for (const membership of [6100, 6102]) expect(roster.group_members.filter(row => row.membership_id === id(membership))).toHaveLength(1);
    const copied = await copy(page, null, fixture().publication.copy_week, 'Semana de publicação exclusivamente sintética');
    const planID = copied.proposal.target_plan_id, selected = copied.proposal.destinations[0].graph;
    expect(copied.proposal.destinations).toHaveLength(4); expect(copied.proposal.new_week).toBe(true);
    await edit(page, planID, selected.session_id, initialNotes);
    const before = await facts('publication-native-before-review'); historical(roster, before);
    expect(before.publications).toEqual(roster.publications); expect(before.prescriptions).toEqual(roster.prescriptions); expect(before.variations).toEqual(roster.variations); expect(before.group_members).toEqual(roster.group_members);
    const copiedPlan = plan(before, planID); expect(copiedPlan).toMatchObject({ training_group_id: id(201), season_id: id(101), week_start: fixture().publication.copy_week });
    expect(copiedPlan.sessions.map(s => s.schedule_precision).sort()).toEqual(['BAND', 'DAY', 'INTERVAL', 'START_ONLY']);
    const summary = 'Primeira publicação sintética\nRevista\tsem copiar o elenco de origem';
    const proof = await review(page, planID, selected.session_id, selected.schedule.date, summary);
    expect(proof.reviewed_latest_revision).toBe('0'); expect(proof.reviewed_latest_id).toBe(''); expect(proof.review_token).not.toBe('');
    await counts(page, { 'Atletas abrangidos': 2, 'Prescrições previstas': 8, Novas: 8, Alteradas: 0, Removidas: 0, 'Sem alterações': 0 });
    expect(await facts('publication-native-reviewed')).toEqual(before); await layout(page, 'publication-native-review320', false);
    for (const athlete of [6000, 6002]) await inspect(page, proof, summary, athlete, selected.session_id, ['Só este destino sintético', '40.5'], [], `publication-native-instructions-${athlete}`, false);
    expect(await facts('publication-native-instructions-reviewed')).toEqual(before);
    await publish(page, proof, summary, true);
    await expect(page.locator('.week-publication')).toContainText('Publicado · revisão 1');
    const first = await facts('publication-native-published'); historical(before, first);
    expect(first.group_members).toEqual(before.group_members); expect(first.variations).toEqual(before.variations); expect(first.copy_events).toEqual(before.copy_events);
    const targetBefore = { ...plan(before, planID) }, targetAfter = { ...plan(first, planID) };
    expect(targetAfter.xmin).not.toBe(targetBefore.xmin); delete targetBefore.xmin; delete targetAfter.xmin; expect(targetAfter).toEqual(targetBefore);
    const published = revision(first, planID, 1); expect(published.pub.change_summary).toBe(summary.replaceAll('\n', '\r\n'));
    for (const row of published.rows) expect(JSON.stringify(row.snapshot)).toContain(copiedPlan.sessions.find(s => s.id === row.session_id).title);
    await privateReads(browser, published.rows, selected.session_id, 'Só este destino sintético', 'publication-native');
    for (const [email, target] of [['publication-unrelated@example.test', published.rows[0].id], ['publication-suspended@example.test', id(6205)]]) {
      const isolated = await browser.newContext(); const reader = await login(isolated, email);
      try {
        await reader.goto('/treinos/estruturados'); await expect(reader.locator(`a[href="/treinos/prescricoes/${target}"]`)).toHaveCount(0);
        const denied = await reader.goto(`/treinos/prescricoes/${target}`); expect(denied.status()).toBe(404);
        await expect(reader.locator('body')).not.toContainText('Histórico privado do dependente suspenso'); await expect(reader.locator('body')).not.toContainText('Só este destino sintético');
      } finally { await isolated.close(); }
    }
    expect(await facts('publication-native-private-reads')).toEqual(first);
  } finally { await context.close(); }
});

test('mixed target personal instructions stay isolated and stale native review cannot publish before an explicit second revision', async ({ browser }) => {
  test.setTimeout(180_000);
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }); const page = await login(context);
  try {
    const initial = await facts('publication-revision-initial'); expect(plan(initial, 311).sessions).toHaveLength(1);
    await openWeek(page, 201, 311, fixture().reuse.routine_week, 3000);
    const adaptations = await page.locator('a[href]').evaluateAll(links => links.map(link => link.getAttribute('href')).filter(href => { const url = new URL(href, location.origin); return url.searchParams.get('session_id') === '39139900-0000-0000-0000-000000003000' && url.searchParams.get('week_id') === '39139900-0000-0000-0000-000000000311' && url.searchParams.get('task') === 'variation' && url.hash === '#training-variations'; }));
    expect(adaptations).toHaveLength(1); await page.locator(`a[href="${adaptations[0]}"]`).click();
    const variation = page.locator('form[action="/admin/treinos/estruturados/variacoes"]'); await reveal(variation.locator('[name="target"]'));
    await variation.locator('[name="target"]').selectOption(`ATHLETE:${id(6100)}`); await variation.locator('[name="subject"]').selectOption(`SEGMENT:${id(3010)}`);
    await variation.locator('[name="operation"]').selectOption('OVERRIDE'); await variation.locator('[name="title"]').fill(personalTitle); await variation.locator('[name="change_summary"]').fill('Título pessoal apenas para o adulto alvo sintético');
    await post(page, variation.getByRole('button', { name: 'Guardar variação', exact: true }), '/variacoes', 303);
    const varied = await facts('publication-revision-personal-rule'); historical(initial, varied);
    expect(varied.variations.filter(row => !initial.variations.some(old => old.id === row.id))).toHaveLength(1); expect(varied.publications).toEqual(initial.publications); expect(varied.prescriptions).toEqual(initial.prescriptions);
    for (const source of [2001, 2002, 2003]) { const proof = await copy(page, source, fixture().reuse.routine_week); expect(proof.proposal.target_plan_id).toBe(id(311)); expect(proof.proposal.new_week).toBe(false); }
    const before = await facts('publication-revision-before'); historical(varied, before);
    expect(plan(before, 311).sessions).toHaveLength(4); expect(before.variations).toEqual(varied.variations); expect(before.group_members).toEqual(initial.group_members);
    const summary = 'Primeira revisão da semana mista com título pessoal';
    const proof = await review(page, id(311), id(3000), fixture().reuse.routine_week, summary);
    await counts(page, { 'Atletas abrangidos': 2, 'Prescrições previstas': 8, Novas: 8, Alteradas: 0, Removidas: 0, 'Sem alterações': 0 });
    expect(await facts('publication-revision-reviewed')).toEqual(before); await layout(page, 'publication-personal-review320', true);
    await inspect(page, proof, summary, 6000, id(3000), [personalTitle, 'Instruções originais do destino.'], [commonTitle], 'publication-personal-instructions-adult320', true);
    await inspect(page, proof, summary, 6002, id(3000), [commonTitle, 'Instruções originais do destino.'], [personalTitle], 'publication-common-instructions-minor320', true);
    expect(await facts('publication-personal-instructions-reviewed')).toEqual(before);
    await publish(page, proof, summary);
    const firstFacts = await facts('publication-revision-first'), first = revision(firstFacts, id(311), 1); historical(before, firstFacts);
    const adult = first.rows.find(row => row.session_id === id(3000) && row.athlete_user_id === id(6000)), minor = first.rows.find(row => row.session_id === id(3000) && row.athlete_user_id === id(6002));
    expect(JSON.stringify(adult.snapshot)).toContain(personalTitle); expect(JSON.stringify(minor.snapshot)).toContain(commonTitle); expect(JSON.stringify(minor.snapshot)).not.toContain(personalTitle);
    const originalNotes = plan(firstFacts, 311).sessions.find(s => s.id === id(3000)).description;
    await privateReads(browser, first.rows, id(3000), originalNotes, 'publication-personal-first', true);
    const stale = await review(page, id(311), id(3000), fixture().reuse.routine_week, staleSummary);
    await counts(page, { 'Atletas abrangidos': 2, 'Prescrições previstas': 8, Novas: 0, Alteradas: 0, Removidas: 0, 'Sem alterações': 8 });
    await expect(confirmForm(page).getByRole('button', { name: 'Concluir revisão', exact: true })).toBeVisible(); expect(await facts('publication-revision-no-change-review')).toEqual(firstFacts);
    const other = await context.newPage(); const changedNotes = 'Alteração concorrente real da descrição, só do destino'; await edit(other, id(311), id(3000), changedNotes); await other.close();
    const concurrent = await facts('publication-revision-concurrent'); historical(firstFacts, concurrent);
    await post(page, confirmForm(page).getByRole('button', { name: 'Concluir revisão', exact: true }), '/publicar', 409);
    await expect(page.locator('.error-summary')).toBeFocused(); await expect(confirmForm(page)).toHaveCount(0); await expect(page.locator('section[aria-labelledby="publication-recipient-title"]')).toHaveCount(0);
    await expect(inspectForm(page)).toHaveCount(0); await expect(page.getByRole('region', { name: 'Instruções revistas', exact: true })).toHaveCount(0);
    for (const [name, value] of Object.entries(stale)) await expect(previewForm(page).locator(`[name="${name}"]`)).toHaveValue(value);
    await expect(previewForm(page).locator('[name="change_summary"]')).toHaveValue(staleSummary); expect(await facts('publication-revision-stale-refused')).toEqual(concurrent); await layout(page, 'publication-stale-proof320', true);
    await post(page, previewForm(page).getByRole('button', { name: 'Rever destinatários', exact: true }), '/publicar');
    const fresh = await metadata(confirmForm(page)); expect(fresh.review_token).not.toBe(stale.review_token);
    expect(fresh.reviewed_latest_id).toBe(first.pub.id); expect(fresh.reviewed_latest_revision).toBe('1');
    await counts(page, { 'Atletas abrangidos': 2, 'Prescrições previstas': 8, Novas: 0, Alteradas: 2, Removidas: 0, 'Sem alterações': 6 });
    expect(await facts('publication-revision-fresh-reviewed')).toEqual(concurrent); await layout(page, 'publication-second-review320', true);
    await inspect(page, fresh, staleSummary, 6000, id(3000), [personalTitle, changedNotes], [commonTitle], 'publication-second-personal-instructions320', true);
    await inspect(page, fresh, staleSummary, 6002, id(3000), [commonTitle, changedNotes], [personalTitle], 'publication-second-common-instructions320', true);
    expect(await facts('publication-revision-second-instructions-reviewed')).toEqual(concurrent);
    await publish(page, fresh, staleSummary);
    await expect(page.locator('.week-publication')).toContainText('Publicado · revisão 2');
    const final = await facts('publication-revision-second'), second = revision(final, id(311), 2); historical(firstFacts, final);
    expect(second.pub.supersedes_id).toBe(first.pub.id); expect(rowsFor(final, first.pub)).toEqual(first.rows); expect(final.variations).toEqual(concurrent.variations); expect(final.group_members).toEqual(concurrent.group_members); expect(final.copy_events).toEqual(concurrent.copy_events);
    const targetBefore = { ...plan(concurrent, 311) }, targetAfter = { ...plan(final, 311) };
    expect(targetAfter.xmin).not.toBe(targetBefore.xmin); delete targetBefore.xmin; delete targetAfter.xmin; expect(targetAfter).toEqual(targetBefore);
    const firstByKey = new Map(first.rows.map(row => [`${row.session_id}/${row.membership_id}`, row]));
    expect(second.rows.filter(row => row.snapshot_sha256 !== firstByKey.get(`${row.session_id}/${row.membership_id}`).snapshot_sha256)).toHaveLength(2);
    await privateReads(browser, second.rows, id(3000), changedNotes, 'publication-personal-second', true);
    expect(await facts('publication-revision-final-reads')).toEqual(final);
  } finally { await context.close(); }
});
