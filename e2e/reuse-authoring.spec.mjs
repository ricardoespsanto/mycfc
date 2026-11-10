import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_REUSE_AUTHORING !== '1', 'Use the isolated contextual reuse runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8')).reuse;
const base = '/admin/treinos/estruturados/reutilizar';
const editor = page => page.locator(`form[action="${base}/rascunho"]`).filter({ has: page.locator('[name="target_group_id"]:not([type="hidden"])') });
const confirm = page => page.locator(`form[action="${base}/confirmar"]`).filter({ has: page.locator('[name="intent"][value="confirm"]') });
const field = (page, name) => editor(page).locator(`[name="${name}"]`);
const ownWords = 'Destino exclusivamente sintético <copiar>\nSegunda\tlinha preservada';
const plan = (facts, planID) => facts.plans[typeof planID === 'number' ? id(planID) : planID];
const session = (facts, planID, sessionID) => plan(facts, planID).sessions.find(s => s.id === (typeof sessionID === 'number' ? id(sessionID) : sessionID));
const plus = (day, count) => { const d = new Date(`${day}T12:00:00Z`); d.setUTCDate(d.getUTCDate() + count); return d.toISOString().slice(0, 10); };
async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const value = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8'));
  expect(value.error).toBeUndefined(); return value;
}
async function login(context, email = 'water-coach@example.test') {
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click(); await expect(page).toHaveURL(/\/today$/); return page;
}
async function reveal(control) {
  await expect(control).toHaveCount(1);
  for (let opened = 0; opened < 24 && !(await control.isVisible()); opened++) {
    const outer = control.locator('xpath=ancestor::details[not(@open)][last()]'); await expect(outer).toHaveCount(1);
    await outer.locator(':scope > summary').click();
  }
  await expect(control).toBeVisible();
}
async function set(page, name, value) {
  const control = field(page, name); await reveal(control);
  if (await control.evaluate(el => el.tagName === 'SELECT')) await control.selectOption(value); else await control.fill(value);
}
async function post(page, button, suffix, status = 200) {
  await reveal(button);
  expect(await button.evaluate(el => el.formNoValidate ? [] : [...el.form.elements].filter(c => c.willValidate && !c.validity.valid).map(c => c.name)), 'Native constraints must permit the actual form submission').toEqual([]);
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix), { timeout: 15_000 });
  await button.click(); const response = await pending; expect(response.status()).toBe(status);
  await page.waitForLoadState('networkidle'); return response;
}
async function refresh(page) { return post(page, editor(page).getByRole('button', { name: 'Atualizar opções sem perder os dados', exact: true }), '/reutilizar/rascunho'); }
async function preview(page, status = 200) { return post(page, editor(page).getByRole('button', { name: 'Pré-visualizar cópia', exact: true }), '/reutilizar/prever', status); }
async function save(page, status = 303) { return post(page, confirm(page).getByRole('button', { name: 'Confirmar cópia em rascunho', exact: true }), '/reutilizar/confirmar', status); }
async function reviewed(page) { return JSON.parse(await confirm(page).locator('[name="proposal"]').inputValue()); }
async function contextualLink(page, wanted, text) {
  const links = page.locator('a[href^="/admin/treinos/estruturados/reutilizar"]'); const matches = [];
  for (let i = 0; i < await links.count(); i++) {
    const link = links.nth(i), href = await link.getAttribute('href'), url = new URL(href, 'http://fixture.test');
    if (Object.entries(wanted).every(([name, value]) => url.searchParams.get(name) === value) && (!text || (await link.textContent()).trim() === text)) matches.push(i);
  }
  expect(matches.length, 'A contextual native reuse entry is required').toBeGreaterThan(0);
  await links.nth(matches[0]).click(); await expect(page).toHaveURL(url => url.pathname === base && Object.entries(wanted).every(([name, value]) => url.searchParams.get(name) === value));
}
async function openWeek(page, group, week, day, selected = '') {
  const query = new URLSearchParams({ group_id: id(group), week_id: id(week), day }); if (selected) query.set('session_id', id(selected));
  await page.goto(`/admin/treinos/estruturados?${query}`);
}
async function chooseNewTarget(page, day, title) {
  await set(page, 'target_group_id', id(201)); await refresh(page);
  await set(page, 'season_id', id(101)); await set(page, 'week_start', day); await set(page, 'week_title', title);
  await set(page, 'week_description', ownWords); await refresh(page);
}
async function chooseExistingTarget(page, targetSession = '', targetSegment = '') {
  await set(page, 'target_group_id', id(201)); await refresh(page);
  await set(page, 'season_id', id(101)); await set(page, 'week_start', fixture().routine_week); await refresh(page);
  if (targetSession) { await set(page, 'target_session_id', id(targetSession)); await refresh(page); }
  if (targetSegment) { await set(page, 'target_segment_id', id(targetSegment)); await refresh(page); }
}
async function mappedKeys(page) { return editor(page).locator('[name="mapping_order"]').evaluateAll(nodes => nodes.map(n => n.value)); }
async function layout(page, name, axe = true) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(nodes => nodes.map(n => n.id)); expect(new Set(ids).size).toBe(ids.length);
  if (axe) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result)); expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
const stripped = (value, names = []) => Object.fromEntries(Object.entries(value).filter(([key]) => !['id', 'session_id', 'segment_id', 'block_id', 'created_at', 'updated_at', ...names].includes(key)));
function semanticGraph(s) {
  return { title: s.title, description: s.description, entry_kind: s.entry_kind, modality_id: s.modality_id, schedule_precision: s.schedule_precision, day_band: s.day_band,
    segments: s.segments.map(g => ({ ...stripped(g, ['blocks']), blocks: g.blocks.map(b => {
      const nodes = new Map(b.water_steps.map(n => [n.id, n]));
      const path = n => n.parent_step_id === null ? `${n.position}` : `${path(nodes.get(n.parent_step_id))}/${n.position}`;
      return { ...stripped(b, ['water_prescription', 'water_steps', 'gym_prescription', 'gym_exercises']),
        water_prescription: b.water_prescription === null ? null : stripped(b.water_prescription),
        gym_prescription: b.gym_prescription === null ? null : stripped(b.gym_prescription),
        water_steps: b.water_steps.map(n => ({ ...stripped(n, ['parent_step_id']), path: path(n) })).sort((a, b) => a.path.localeCompare(b.path, undefined, { numeric: true })),
        gym_exercises: b.gym_exercises.map(e => stripped(e)) };
    }) })) };
}
function IDs(s) { return [s.id, ...s.segments.flatMap(g => [g.id, ...g.blocks.flatMap(b => [b.id, ...b.water_steps.map(n => n.id), ...b.gym_exercises.map(e => e.id)])])]; }
function reviewedIDs(p) { return { plan: p.proposal.target_plan_id, sessions: p.proposal.destinations.map(d => d.graph.session_id), children: p.proposal.destinations.flatMap(d => d.graph.activities.flatMap(a => [a.id, ...a.phases.flatMap(b => [b.id, ...(b.water?.nodes || []).map(n => n.id), ...(b.gym?.exercises || []).map(e => e.id)])])), events: p.proposal.events.map(e => e.id) }; }
function unchangedProtected(before, after) {
  for (const key of ['publications', 'prescriptions', 'outcomes', 'routines']) { expect(before[key].length).toBeGreaterThan(0); expect(after[key], key).toEqual(before[key]); }
  for (const key of ['profiles', 'zones', 'memberships', 'group_members', 'variations', 'variation_groups', 'variation_group_members']) expect(after[key], key).toEqual(before[key]);
  for (const old of before.copy_events) expect(after.copy_events.find(e => e.id === old.id)).toEqual(old);
}
function newEvents(before, after, p) {
  const added = after.copy_events.filter(e => !before.copy_events.some(old => old.id === e.id));
  expect(added.map(e => e.id).sort()).toEqual(p.proposal.events.map(e => e.id).sort());
  for (const expected of p.proposal.events) expect(added.find(e => e.id === expected.id)).toMatchObject({ source_kind: expected.source_kind, source_id: expected.source_id, destination_kind: expected.destination_kind, destination_id: expected.destination_id, copied_by_id: id(1) });
}
async function destination(page, p) {
  const first = p.proposal.destinations[0].graph;
  await expect(page).toHaveURL(url => url.pathname === '/admin/treinos/estruturados' && url.hash === '#training-plan' && url.searchParams.get('group_id') === id(201)
    && url.searchParams.get('week_id') === p.proposal.target_plan_id && url.searchParams.get('session_id') === first.session_id && url.searchParams.get('day') === first.schedule.date);
}
async function editCopied(page, p, notes) {
  const graph = p.proposal.destinations[0].graph, path = `/admin/treinos/estruturados/semanas/${p.proposal.target_plan_id}/treinos/${graph.session_id}/editar`;
  await page.locator(`a[href="${path}"]`).click();
  const form = page.locator('form').filter({ has: page.locator('textarea[name="description"]') }); await form.locator('[name="description"]').fill(notes);
  await post(page, form.getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/editar/prever');
  const confirmation = page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
  await post(page, confirmation.getByRole('button', { name: 'Guardar alterações', exact: true }), '/editar/confirmar', 303);
}

test('native no-JS320 copies all four schedule shapes into an independent explicitly configured week', async ({ browser }) => {
  test.setTimeout(120_000);
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } }); const page = await login(context);
  try {
    await openWeek(page, 200, 310, fixture().source_week); await contextualLink(page, { source_kind: 'WEEK', source_plan_id: id(310) }, 'Copiar semana');
    const before = await facts('reuse-week-before'); await chooseNewTarget(page, fixture().copy_week, '');
    expect(await mappedKeys(page)).toHaveLength(4); await preview(page, 422); await expect(page.locator('.error-summary')).toBeFocused();
    await expect(field(page, 'week_description')).toHaveValue(ownWords); expect(await facts('reuse-week-invalid')).toEqual(before);
    await set(page, 'week_title', 'Semana independente exclusivamente sintética'); await refresh(page);
    const source = [...plan(before, 310).sessions].sort((a, b) => a.scheduled_on.localeCompare(b.scheduled_on)); const keys = await mappedKeys(page);
    for (let i = 0; i < keys.length; i++) {
      const prefix = `mappings[${keys[i]}]`, s = source[i]; await expect(field(page, `${prefix}.date`)).toHaveValue(plus(fixture().copy_week, i));
      if (s.starts_at === null) for (const name of ['start_time', 'end_date', 'end_time']) { const c = field(page, `${prefix}.${name}`); if (await c.count()) await expect(c).toHaveValue(''); }
      if (s.schedule_precision === 'START_ONLY') { const c = field(page, `${prefix}.end_time`); if (await c.count()) await expect(c).toHaveValue(''); }
    }
    await preview(page); const initial = await reviewed(page), identity = reviewedIDs(initial);
    expect(initial.proposal.destinations).toHaveLength(4); expect(initial.proposal.new_week).toBe(true);
    const proposed = page.getByRole('region', { name: 'Instruções propostas no destino', exact: true });
    for (const text of ['Misto quatro horários', 'Início sem fim', 'Descanso sem horas', 'Logística à tarde', 'SYNTHETIC PIN ONLY', '40.5']) await expect(proposed).toContainText(text);
    expect(await facts('reuse-week-preview')).toEqual(before); await layout(page, 'reuse-week-native-preview320', false);
    await post(page, page.getByRole('button', { name: 'Continuar a editar', exact: true }), '/reutilizar/rascunho');
    await expect(field(page, 'prior_review_token')).not.toHaveValue(''); await expect(field(page, 'week_description')).toHaveValue(ownWords);
    await preview(page); const finalProof = await reviewed(page); expect(reviewedIDs(finalProof)).toEqual(identity);
    const result = await save(page); await destination(page, finalProof);
    const after = await facts('reuse-week-copied'); unchangedProtected(before, after); expect(plan(after, 310)).toEqual(plan(before, 310));
    expect(Object.keys(after.plans).filter(key => !before.plans[key])).toEqual([identity.plan]);
    expect(plan(after, identity.plan)).toMatchObject({ training_group_id: id(201), season_id: id(101), week_start: fixture().copy_week, title: 'Semana independente exclusivamente sintética' });
    expect(plan(after, identity.plan).sessions).toHaveLength(4); newEvents(before, after, finalProof);
    const allSourceIDs = source.flatMap(IDs);
    for (let i = 0; i < finalProof.proposal.destinations.length; i++) {
      const d = finalProof.proposal.destinations[i], copied = session(after, identity.plan, d.graph.session_id), s = source[i];
      expect(semanticGraph(copied)).toEqual(semanticGraph(s)); expect(IDs(copied).some(value => allSourceIDs.includes(value))).toBe(false);
      expect(copied.scheduled_on).toBe(plus(fixture().copy_week, i));
      if (s.starts_at === null) expect(copied.starts_at).toBeNull(); else expect(copied.local_start.slice(11)).toBe(s.local_start.slice(11));
      if (s.ends_at === null) expect(copied.ends_at).toBeNull(); else expect(copied.local_end.slice(11)).toBe(s.local_end.slice(11));
    }
    const replay = await page.request.post(result.url(), { data: result.request().postData(), headers: { 'content-type': 'application/x-www-form-urlencoded' }, maxRedirects: 0 });
    expect(replay.status()).toBe(303); expect(replay.headers().location).toBe(result.headers().location); expect(await facts('reuse-week-replayed')).toEqual(after);
    await editCopied(page, finalProof, 'Alteração só do destino exclusivamente sintético');
    const edited = await facts('reuse-week-edited'); unchangedProtected(after, edited); expect(plan(edited, 310)).toEqual(plan(before, 310));
    expect(session(edited, identity.plan, finalProof.proposal.destinations[0].graph.session_id).description).toBe('Alteração só do destino exclusivamente sintético');
    expect(edited.copy_events).toEqual(after.copy_events);
  } finally { await context.close(); }
});

test('configured SESSION SEGMENT BLOCK routines retain target context and existing Adapt form protections', async ({ browser }) => {
  test.setTimeout(120_000);
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }); const page = await login(context);
  try {
    const before = await facts('reuse-routines-before'); await openWeek(page, 201, 311, fixture().routine_week);
    await contextualLink(page, { source_kind: 'ROUTINE', target_group_id: id(201), week_start: fixture().routine_week }, 'Usar rotina');
    const library = page.locator('form[method="get"]').filter({ has: page.locator('[name="routine_query"]') });
    await library.locator('[name="routine_query"]').fill('Sem resultado exclusivamente sintético'); await library.getByRole('button', { name: 'Pesquisar', exact: true }).click();
    await expect(page.getByText('Sem rotinas visíveis para estes filtros.', { exact: false })).toBeVisible();
    await page.locator('[name="routine_query"]').fill('Força'); await page.getByRole('button', { name: 'Pesquisar', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Força A sessão exclusivamente sintética', exact: true })).toBeVisible();
    for (const label of ['Força B atividade exclusivamente sintética', 'Força B bloco exclusivamente sintético']) await expect(page.getByRole('heading', { name: label, exact: true })).toBeVisible();
    await layout(page, 'reuse-library320');
    await page.getByRole('link', { name: 'Usar Força A sessão exclusivamente sintética', exact: true }).click();
    await chooseExistingTarget(page); await preview(page); const sessionProof = await reviewed(page); await layout(page, 'reuse-session-routine-preview320'); await save(page); await destination(page, sessionProof);
    let current = await facts('reuse-session-routine-saved'); unchangedProtected(before, current); expect(plan(current, 311).title).toBe(plan(before, 311).title); newEvents(before, current, sessionProof);
    const storedRoutine = before.routines.find(r => r.id === id(4000)), liveSource = session(before, 310, 2000);
    expect(liveSource.modality_id).not.toBeNull(); expect(storedRoutine.snapshot).not.toHaveProperty('modality_id');
    const routineCopy = session(current, 311, sessionProof.proposal.destinations[0].graph.session_id);
    expect(routineCopy.modality_id).toBeNull(); expect(session(current, 310, 2000).modality_id).toBe(liveSource.modality_id);
    // The configured snapshot omits this native legacy field; do not invent it.
    expect(semanticGraph(routineCopy)).toEqual({ ...semanticGraph(liveSource), modality_id: null });
    for (const [routineID, label, segmentID] of [[4001, 'Força B atividade exclusivamente sintética', ''], [4002, 'Força B bloco exclusivamente sintético', 3010]]) {
      await openWeek(page, 201, 311, fixture().routine_week, 3000);
      await contextualLink(page, { source_kind: 'ROUTINE', target_group_id: id(201), target_session_id: id(3000) }, 'Usar rotina');
      await page.getByRole('link', { name: `Usar ${label}`, exact: true }).click();
      await expect(field(page, 'routine_id')).toHaveValue(id(routineID));
      await chooseExistingTarget(page, 3000, segmentID); expect(await mappedKeys(page)).toEqual([]);
      await preview(page); const p = await reviewed(page);
      await expect(page.getByRole('region', { name: 'Instruções de origem', exact: true })).toContainText('Supino retido sintético');
      await save(page); await destination(page, p); const after = await facts(`reuse-fragment-${routineID}-saved`); unchangedProtected(current, after); newEvents(current, after, p);
      const oldTarget = session(current, 311, 3000), newTarget = session(after, 311, 3000);
      for (const key of ['id', 'title', 'description', 'starts_at', 'ends_at', 'schedule_precision', 'scheduled_on', 'created_at', 'created_by_id']) expect(newTarget[key], key).toEqual(oldTarget[key]);
      const retainedBefore = { ...oldTarget.segments.find(g => g.id === id(3010)).blocks.find(b => b.id === id(3100)) };
      const retainedAfter = { ...newTarget.segments.find(g => g.id === id(3010)).blocks.find(b => b.id === id(3100)) };
      // An aggregate append versions retained rows; all other native facts stay.
      delete retainedBefore.updated_at; delete retainedAfter.updated_at;
      expect(retainedAfter).toEqual(retainedBefore);
      if (routineID === 4001) expect(newTarget.segments).toHaveLength(oldTarget.segments.length + 1); else expect(newTarget.segments[0].blocks).toHaveLength(oldTarget.segments[0].blocks.length + 1);
      expect(plan(after, 310)).toEqual(plan(before, 310)); current = after;
    }
    await openWeek(page, 200, 310, fixture().source_week, 2000);
    const adaptHrefs = await page.locator('a[href*="task=variation"]').evaluateAll(links => links.map(link => link.getAttribute('href')));
    const sessionAdapt = adaptHrefs.filter(href => {
      const url = new URL(href, 'http://fixture.test');
      return url.pathname === '/admin/treinos/estruturados' && url.searchParams.get('group_id') === id(200)
        && url.searchParams.get('week_id') === id(310) && url.searchParams.get('session_id') === id(2000)
        && url.searchParams.get('day') === fixture().source_week && url.searchParams.get('task') === 'variation' && url.hash === '#training-variations';
    });
    expect(sessionAdapt).toHaveLength(1); await page.locator(`a[href="${sessionAdapt[0]}"]`).click();
    await expect(page).toHaveURL(url => url.pathname === '/admin/treinos/estruturados' && url.searchParams.get('week_id') === id(310) && url.searchParams.get('session_id') === id(2000) && url.searchParams.get('task') === 'variation' && url.hash === '#training-variations');
    await expect(page.locator('#training-variations')).toContainText('Duas variações do mesmo nível');
    const form = page.locator('form[action="/admin/treinos/estruturados/variacoes"]'); await reveal(form.locator('[name="operation"]'));
    await expect(form.locator('[name="plan_id"]')).toHaveValue(id(310));
    await form.locator('[name="change_summary"]').fill(ownWords); await form.locator('[name="instructions"]').fill('Patch sintético preservado');
    const baseline = await form.evaluate(el => Object.fromEntries(new FormData(el)));
    await form.locator('[name="operation"]').selectOption('OMIT');
    const omitted = await form.evaluate(el => Object.fromEntries(new FormData(el)));
    for (const name of ['gorilla.csrf.Token', 'return_to', 'plan_id', 'target', 'subject', 'change_summary']) expect(omitted[name], name).toBe(baseline[name]);
    expect(omitted['gorilla.csrf.Token']).not.toBe(''); expect(omitted.change_summary.replaceAll('\r\n', '\n')).toBe(ownWords);
    for (const name of ['title', 'modality', 'instructions', 'repeats', 'duration_seconds', 'distance_metres', 'recovery_seconds', 'intensity_code', 'sets', 'exercise_repetitions', 'resistance']) { await expect(form.locator(`[name="${name}"]`)).toBeDisabled(); expect(omitted[name]).toBeUndefined(); }
    const returned = new URL(omitted.return_to, 'http://fixture.test'); expect(returned.searchParams.get('week_id')).toBe(id(310)); expect(returned.hash).toBe('#training-plan');
    await form.locator('[name="operation"]').selectOption('OVERRIDE'); await expect(form.locator('[name="instructions"]')).toBeEnabled(); await expect(form.locator('[name="instructions"]')).toHaveValue('Patch sintético preservado');
    await layout(page, 'reuse-adapt-native-form320'); expect(await facts('reuse-adapt-readonly')).toEqual(current);
  } finally { await context.close(); }
});

test('stale reuse proof retains own raw values and unrelated scope remains non-disclosing', async ({ browser }) => {
  test.setTimeout(90_000);
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } }); const page = await login(context);
  try {
    await openWeek(page, 200, 310, plus(fixture().source_week, 1), 2001);
    await contextualLink(page, { source_kind: 'SESSION', source_plan_id: id(310), source_session_id: id(2001) }, 'Copiar sessão');
    await chooseNewTarget(page, fixture().stale_week, 'Proposta obsoleta exclusivamente sintética'); await preview(page);
    const before = await facts('reuse-stale-before'), oldProof = await confirm(page).locator('[name="proposal"]').inputValue(), oldToken = await confirm(page).locator('[name="review_token"]').inputValue();
    const other = await context.newPage(); await other.goto(`/admin/treinos/estruturados/semanas/${id(310)}/treinos/${id(2001)}/editar`);
    const edit = other.locator('form').filter({ has: other.locator('textarea[name="description"]') }); await edit.locator('[name="description"]').fill('Fonte alterada por outra confirmação real');
    await post(other, edit.getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/editar/prever');
    await post(other, other.getByRole('button', { name: 'Guardar alterações', exact: true }), '/editar/confirmar', 303); await other.close();
    const concurrent = await facts('reuse-stale-concurrent'); const refused = await save(page, 409);
    const submitted = new URLSearchParams(refused.request().postData()); expect(submitted.get('proposal')).toBe(oldProof); expect(submitted.get('review_token')).toBe(oldToken);
    await expect(field(page, 'week_description')).toHaveValue(ownWords); await expect(field(page, 'prior_proposal')).toHaveValue(oldProof); await expect(field(page, 'prior_review_token')).toHaveValue(oldToken);
    await expect(page.getByRole('button', { name: 'Pré-visualizar cópia', exact: true })).toHaveCount(0); await expect(page.getByRole('button', { name: 'Confirmar cópia em rascunho', exact: true })).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Recarregar contexto atual', exact: true })).toBeVisible(); expect(await facts('reuse-stale-refused')).toEqual(concurrent); unchangedProtected(before, concurrent); await layout(page, 'reuse-stale-own-raw');
    const unrelated = await browser.newContext(); const stranger = await login(unrelated, 'unrelated-coach@example.test');
    try {
      const response = await stranger.goto(`${base}?source_kind=WEEK&source_plan_id=${id(310)}`); expect(response.status()).toBe(404);
      await expect(stranger.locator('body')).not.toContainText('Misto quatro horários'); await expect(stranger.locator('body')).not.toContainText('Força A sessão');
      const ownSource = new URLSearchParams({ source_kind: 'SESSION', source_plan_id: id(312), source_session_id: id(5001), target_group_id: id(203), season_id: id(100), week_start: plus(fixture().stale_week, 28) });
      const allowed = await stranger.goto(`${base}?${ownSource}`); expect(allowed.status()).toBe(200);
      await expect(stranger.locator('body')).toContainText('Treino do âmbito autorizado sintético');
      const otherTarget = new URLSearchParams({ source_kind: 'SESSION', source_plan_id: id(312), source_session_id: id(5001), target_group_id: id(201), season_id: id(101), week_start: fixture().routine_week });
      const target = await stranger.goto(`${base}?${otherTarget}`); expect(target.status()).toBe(404);
      await expect(stranger.locator('body')).not.toContainText('Destino de reutilização exclusivamente sintético');
      await expect(stranger.locator('body')).not.toContainText('Destino existente para rotinas sintéticas');
      expect(await facts('reuse-unrelated-refused')).toEqual(concurrent);
    } finally { await unrelated.close(); }
  } finally { await context.close(); }
});
