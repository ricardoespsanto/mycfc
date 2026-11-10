import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_WORKOUT_EDIT_AUTHORING !== '1', 'Use the isolated saved-workout EDIT runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const editor = page => page.locator('form').filter({ has: page.locator('textarea[name="description"]') });
const confirm = page => page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
const field = (page, name) => editor(page).locator(`[name="${name}"]`);
const ap = activity => `activities[${activity}]`;
const pp = (activity, phase) => `${ap(activity)}.phases[${phase}]`;
const np = (activity, phase, node) => `${pp(activity, phase)}.water.nodes[${node}]`;
const ep = (activity, phase, exercise) => `${pp(activity, phase)}.gym.exercises[${exercise}]`;
const ownWords = 'Alteração exclusivamente sintética <copiar>\nSegunda\tlinha preservada';
const route = (plan, session, action = 'editar') => `/admin/treinos/estruturados/semanas/${id(plan)}/treinos/${id(session)}/${action}`;
const getSession = (facts, plan, session) => facts.plans[id(plan)].sessions.find(s => s.id === (typeof session === 'number' ? id(session) : session));
const addDay = (day, count) => { const date = new Date(`${day}T12:00:00Z`); date.setUTCDate(date.getUTCDate() + count); return date.toISOString().slice(0, 10); };
async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const value = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8'));
  expect(value.error).toBeUndefined(); return value;
}
function unchangedHistory(before, after) {
  for (const key of ['publications', 'prescriptions', 'outcomes', 'routines', 'copy_events']) {
    expect(before[key].length, `Nonempty ${key} sentinel required`).toBeGreaterThan(0);
    expect(after[key], `Editing must preserve ${key}`).toEqual(before[key]);
  }
  for (const key of ['profiles', 'zones']) expect(after[key]).toEqual(before[key]);
}
async function reveal(control) {
  await expect(control).toHaveCount(1);
  for (let count = 0; count < 12 && !(await control.isVisible()); count++) {
    const closed = control.locator('xpath=ancestor::details[not(@open)][last()]');
    await expect(closed).toHaveCount(1); await closed.locator(':scope > summary').click();
  }
  await expect(control).toBeVisible();
}
async function set(page, name, value) {
  const control = field(page, name); await reveal(control);
  if (await control.evaluate(el => el.tagName === 'SELECT')) await control.selectOption(value);
  else await control.fill(value);
}
async function post(page, button, suffix, status = 200) {
  await reveal(button);
  const invalid = await button.evaluate(el => el.formNoValidate ? [] : [...el.form.elements].filter(c => c.willValidate && !c.validity.valid).map(c => c.name));
  expect(invalid, 'Real native submission must satisfy browser constraints').toEqual([]);
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix), { timeout: 15_000 });
  await button.click(); const response = await pending; expect(response.status()).toBe(status);
  await page.waitForLoadState('networkidle'); return response;
}
async function command(page, value) { return post(page, editor(page).locator(`button[name="command"][value="${value}"]`), '/editar/rascunho'); }
async function preview(page, status = 200) { return post(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/editar/prever', status); }
async function save(page, status = 303) { return post(page, confirm(page).getByRole('button', { name: 'Guardar alterações', exact: true }), '/editar/confirmar', status); }
async function proposal(page) { return JSON.parse(await confirm(page).locator('[name="proposal"]').inputValue()); }
async function login(context) {
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('water-coach@example.test');
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click();
  await expect(page).toHaveURL(/\/today$/); return page;
}
async function openFromWeek(page, plan, session) {
  const day = fixture().days[plan - 300];
  await page.goto(`/admin/treinos/estruturados?group_id=${id(200)}&week_id=${id(plan)}&day=${day}`);
  const link = page.locator(`a[href="${route(plan, session)}"]`);
  await expect(link).toHaveCount(1); await expect(link).toHaveAttribute('aria-label', /Alterar.*sintétic/i);
  const parent = link.locator('xpath=..');
  // Copy/routine/adapt may add siblings; the original task links stay exact.
  await expect(parent.locator(`:scope > a[href="${route(plan, session)}"]`)).toHaveCount(1);
  await expect(parent.locator(`:scope > a[href="${route(plan, session, 'cancelar')}"]`)).toHaveCount(1);
  await expect(page.locator(`a[href="${route(plan, session, 'cancelar')}"]`)).toHaveCount(1);
  const detail = parent.locator(':scope > a').first();
  const detailURL = new URL(await detail.getAttribute('href'), 'http://fixture.test');
  expect(detailURL.pathname).toBe('/admin/treinos/estruturados');
  expect(detailURL.searchParams.get('group_id')).toBe(id(200));
  expect(detailURL.searchParams.get('week_id')).toBe(id(plan));
  expect(detailURL.searchParams.get('session_id')).toBe(id(session));
  await expect(parent.locator('a a')).toHaveCount(0);
  await link.click(); await expect(page).toHaveURL(route(plan, session));
  await expect(editor(page)).toHaveCount(1); await expect(field(page, 'session_id')).toHaveValue(id(session));
  await expect(field(page, 'schedule.date')).not.toHaveAttribute('readonly', '');
  await expect(field(page, 'entry_kind')).toHaveAttribute('type', 'hidden');
  return day;
}
async function keysByIDs(page, sourceIDs) {
  const out = {};
  for (const n of sourceIDs) {
    const input = editor(page).locator(`input[name$=".id"][value="${id(n)}"]`); await expect(input).toHaveCount(1);
    const name = await input.getAttribute('name');
    out[n] = name.match(/^activities\[([^\]]+)\](?:\.phases\[([^\]]+)\])?(?:\.(?:water\.nodes|gym\.exercises)\[([^\]]+)\])?\.id$/)?.slice(1);
    expect(out[n], `Source ID ${n} has canonical editor key`).toBeDefined();
  }
  return out;
}
async function layout(page, name, axe = true) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(nodes => nodes.map(n => n.id)); expect(new Set(ids).size).toBe(ids.length);
  if (axe) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result));
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
function contentIDs(session) {
  return session.segments.flatMap(s => [s.id, ...s.blocks.flatMap(b => [b.id, ...b.water_steps.map(n => n.id), ...b.gym_exercises.map(e => e.id)])]);
}
function preserveOwner(row, before) {
  for (const name of ['id', 'session_id', 'segment_id', 'block_id', 'parent_step_id', 'created_at']) if (name in before) expect(row[name], name).toEqual(before[name]);
}
function checkOnlyStatus(before, after, reason) {
  const ignored = new Set(['status', 'cancelled_at', 'cancelled_by_id', 'cancellation_reason', 'updated_at']);
  for (const [key, value] of Object.entries(before)) if (!ignored.has(key)) expect(after[key], key).toEqual(value);
  expect(after.status).toBe('CANCELLED'); expect(after.cancelled_by_id).toBe(id(1));
  expect(after.cancelled_at).not.toBeNull(); expect(after.cancellation_reason).toBe(reason);
}
async function cancellation(page, plan, session, reason, status = 303) {
  await page.getByRole('textbox', { name: 'Motivo (obrigatório)', exact: true }).fill(reason);
  await page.getByLabel(/Confirmo que pretendo cancelar definitivamente/).check();
  return post(page, page.getByRole('button', { name: 'Cancelar sessão', exact: true }), `/treinos/${id(session)}/cancelar`, status);
}

test('native no-JS320 edits one complete mixed workout retaining IDs, source pin and detached history', async ({ browser }) => {
  test.setTimeout(90_000);
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } });
  const page = await login(context);
  try {
    const day = await openFromWeek(page, 306, 1000), before = await facts('edit-complete-before');
    const original = getSession(before, 306, 1000), originalIDs = contentIDs(original);
    const keys = await keysByIDs(page, [1010, 1020, 1100, 1200, 1110, 1111, 1112, 1113, 1114, 1201, 1202]);
    const [water] = keys[1010], [, waterPhase] = keys[1100], [gym] = keys[1020], [, gymPhase] = keys[1200];
    const profile = field(page, `${pp(water, waterPhase)}.water.profile_id`);
    await expect(profile).toHaveValue(id(950)); await expect(profile.locator(`option[value="${id(950)}"]`)).toContainText(/SYNTHETIC PIN ONLY.*inativ/i);
    await set(page, 'description', ownWords); await set(page, 'title', 'x');
    await preview(page, 422); await expect(page.locator('.error-summary')).toBeFocused();
    await expect(page.locator('.error-summary a[href="#workout-title"]')).toBeVisible(); await expect(field(page, 'description')).toHaveValue(ownWords);
    expect(await facts('edit-complete-invalid')).toEqual(before);
    await set(page, 'title', 'Treino misto alterado exclusivamente sintético');
    await set(page, 'schedule.date', addDay(day, 1)); await set(page, 'schedule.end_date', addDay(day, 1));
    await set(page, 'schedule.start_time', '15:00'); await set(page, 'schedule.end_time', '16:00');
    await set(page, `${np(water, waterPhase, keys[1111][2])}.instructions`, ownWords);
    await command(page, `water_remove:${water}:${waterPhase}:${keys[1113][2]}`);
    await expect(page.getByRole('heading', { name: 'Remoções propostas', exact: true })).toBeVisible();
    await expect(editor(page).locator(`input[name$=".id"][value="${id(1114)}"]`)).toHaveCount(0);
    await command(page, `water_up:${water}:${waterPhase}:${keys[1112][2]}`);
    await command(page, `gym_up:${gym}:${gymPhase}:${keys[1202][2]}`);
    const oldExercises = await editor(page).locator(`[name="${pp(gym, gymPhase)}.gym.exercise_order"]`).evaluateAll(nodes => nodes.map(n => n.value));
    await command(page, `gym_add:${gym}:${gymPhase}`);
    const added = (await editor(page).locator(`[name="${pp(gym, gymPhase)}.gym.exercise_order"]`).evaluateAll(nodes => nodes.map(n => n.value))).filter(key => !oldExercises.includes(key));
    expect(added).toHaveLength(1);
    for (const [name, value] of Object.entries({ name: 'Agachamento novo exclusivamente sintético', sets: '2', repetitions: '6', resistance_kind: 'BODY_WEIGHT', notes: ownWords })) await set(page, `${ep(gym, gymPhase, added[0])}.${name}`, value);
    await command(page, `activity_up:${gym}`); await layout(page, 'edit-complete-native320', false);
    await preview(page); const initial = await proposal(page); expect(initial.proposal.session_id).toBe(id(1000));
    await expect(page.locator('.task-form')).toContainText(/publica/i); expect(await facts('edit-complete-preview')).toEqual(before);
    await post(page, page.getByRole('button', { name: 'Continuar a editar', exact: true }), '/editar/rascunho');
    await expect(field(page, 'prior_review_token')).not.toHaveValue(''); await expect(field(page, 'description')).toHaveValue(ownWords);
    await preview(page); const finalProposal = await proposal(page);
    expect(finalProposal.proposal.session_id).toBe(id(1000));
    expect(finalProposal.proposal.activities.map(a => a.id)).toEqual([id(1020), id(1010)]);
    await layout(page, 'edit-complete-preview320', false); await save(page);
    await expect(page).toHaveURL(url => url.pathname === '/admin/treinos/estruturados' && url.hash === '#training-plan'
      && [...url.searchParams.keys()].sort().join(',') === 'day,group_id,session_id,week_id'
      && url.searchParams.get('group_id') === id(200) && url.searchParams.get('week_id') === id(306)
      && url.searchParams.get('session_id') === id(1000) && url.searchParams.get('day') === addDay(day, 1));
    const after = await facts('edit-complete-saved'), saved = getSession(after, 306, 1000);
    unchangedHistory(before, after); expect(getSession(after, 306, 1001)).toEqual(getSession(before, 306, 1001));
    for (const key of ['id', 'plan_id', 'entry_kind', 'modality_id', 'created_by_id', 'created_at', 'status']) expect(saved[key], key).toEqual(original[key]);
    expect(saved.description.replaceAll('\r\n', '\n')).toBe(ownWords);
    expect(saved.scheduled_on).toBe(addDay(day, 1)); expect(saved.local_start).toBe(`${addDay(day, 1)} 15:00`); expect(saved.local_end).toBe(`${addDay(day, 1)} 16:00`);
    const remaining = contentIDs(saved); expect(remaining).not.toContain(id(1113)); expect(remaining).not.toContain(id(1114));
    for (const value of originalIDs.filter(value => ![id(1113), id(1114)].includes(value))) expect(remaining).toContain(value);
    expect(remaining.filter(value => !originalIDs.includes(value))).toHaveLength(1);
    expect(saved.segments.map(s => s.id)).toEqual([id(1020), id(1010)]);
    for (const segment of saved.segments) {
      preserveOwner(segment, original.segments.find(s => s.id === segment.id));
      for (const block of segment.blocks) preserveOwner(block, original.segments.flatMap(s => s.blocks).find(b => b.id === block.id));
    }
    const waterBlock = saved.segments[1].blocks[0], gymBlock = saved.segments[0].blocks[0];
    expect(waterBlock.water_prescription.intensity_profile_id).toBe(id(950));
    expect(waterBlock.water_steps.filter(n => n.parent_step_id === null).map(n => n.id)).toEqual([id(1112), id(1110)]);
    for (const node of waterBlock.water_steps) preserveOwner(node, original.segments[0].blocks[0].water_steps.find(n => n.id === node.id));
    expect(waterBlock.water_steps.find(n => n.id === id(1111)).instructions.replaceAll('\r\n', '\n')).toBe(ownWords);
    expect(gymBlock.gym_exercises.slice(0, 2).map(e => e.id)).toEqual([id(1202), id(1201)]);
    for (const exercise of gymBlock.gym_exercises.slice(0, 2)) preserveOwner(exercise, original.segments[1].blocks[0].gym_exercises.find(e => e.id === exercise.id));
    expect(gymBlock.gym_exercises[2]).toMatchObject({ name: 'Agachamento novo exclusivamente sintético', sets: 2, repetitions: 6, resistance_kind: 'BODY_WEIGHT', resistance_value: null });
  } finally { await context.close(); }
});

test('source drift requires explicit reload while other-session plan drift allows deliberate re-preview', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } }); const page = await login(context);
  try {
    await openFromWeek(page, 307, 1002); const before = await facts('edit-stale-before');
    await set(page, 'description', ownWords); const oldDigest = await field(page, 'source_digest').inputValue(); await preview(page);
    const oldProof = await confirm(page).locator('[name="proposal"]').inputValue(), oldToken = await confirm(page).locator('[name="review_token"]').inputValue();
    const competitor = await context.newPage(); await competitor.goto(route(307, 1002)); await set(competitor, 'description', 'Alteração concorrente real da mesma fonte'); await preview(competitor); await save(competitor); await competitor.close();
    const concurrent = await facts('edit-stale-concurrent'); await save(page, 409);
    await expect(field(page, 'description')).toHaveValue(ownWords); await expect(field(page, 'source_digest')).toHaveValue(oldDigest);
    await expect(field(page, 'prior_proposal')).toHaveValue(oldProof); await expect(field(page, 'prior_review_token')).toHaveValue(oldToken);
    await expect(page.getByRole('button', { name: 'Pré-visualizar treino', exact: true })).toHaveCount(0); await expect(page.getByRole('button', { name: 'Guardar alterações', exact: true })).toHaveCount(0);
    expect(await facts('edit-stale-refused')).toEqual(concurrent); await layout(page, 'edit-own-source-conflict');
    await page.getByRole('link', { name: 'Recarregar treino atual', exact: true }).click();
    await expect(field(page, 'description')).toHaveValue('Alteração concorrente real da mesma fonte'); await expect(field(page, 'source_digest')).not.toHaveValue(oldDigest);
    unchangedHistory(before, concurrent);
    await openFromWeek(page, 306, 1000);
    // Axe needs JavaScript timers. Scan the complete editor here; the earlier
    // native no-JS journey retains layout/screenshots and every save assertion.
    await page.setViewportSize({ width: 320, height: 1000 });
    for (let opened = 0; opened < 32; opened++) {
      const closed = editor(page).locator('details:not([open])');
      if (!(await closed.count())) break;
      const summary = closed.first().locator(':scope > summary'); await reveal(summary); await summary.click();
    }
    await expect(editor(page).locator('details:not([open])')).toHaveCount(0);
    await layout(page, 'edit-complete-mixed-editor320');
    await page.setViewportSize({ width: 1280, height: 1000 });
    await set(page, 'description', 'Proposta explícita após mudança de outro treino'); await preview(page); const initial = await proposal(page);
    const other = await context.newPage(); await other.goto(route(306, 1001)); await set(other, 'description', 'Outra sessão alterada sem rebase da fonte'); await preview(other); await save(other); await other.close();
    const planChange = await facts('edit-plan-concurrent'); await save(page, 409);
    await expect(field(page, 'description')).toHaveValue('Proposta explícita após mudança de outro treino'); await expect(page.getByRole('button', { name: 'Pré-visualizar treino', exact: true })).toBeVisible();
    expect(await facts('edit-plan-refused')).toEqual(planChange); await preview(page); const renewed = await proposal(page);
    expect(renewed.proposal.session_id).toBe(initial.proposal.session_id); expect(renewed.proposal.activities).toEqual(initial.proposal.activities);
    await save(page); const final = await facts('edit-plan-saved'); unchangedHistory(planChange, final);
    expect(getSession(final, 306, 1001)).toEqual(getSession(planChange, 306, 1001));
    expect(getSession(final, 306, 1000).description).toBe('Proposta explícita após mudança de outro treino');
  } finally { await context.close(); }
});

test('civil schedules stay honest and cancellation is scoped, status-only, explicit and irreversible', async ({ browser }) => {
  test.setTimeout(90_000);
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }); const page = await login(context);
  try {
    const before = await facts('edit-civil-before');
    for (const [session, mode, band] of [[1003, 'DAY', null], [1004, 'BAND', 'AFTERNOON']]) {
      await page.goto(route(308, session)); await expect(field(page, 'schedule.mode')).toHaveValue(mode);
      for (const name of ['schedule.start_time', 'schedule.end_time']) {
        const control = field(page, name); if (await control.count()) await expect(control).toHaveValue('');
      }
      await set(page, 'description', `Notas civis alteradas ${mode} exclusivamente sintéticas`); await preview(page); await save(page);
      const current = await facts(`edit-civil-${mode.toLowerCase()}-saved`), saved = getSession(current, 308, session);
      expect(saved.starts_at).toBeNull(); expect(saved.ends_at).toBeNull(); expect(saved.schedule_precision).toBe(mode); expect(saved.day_band).toBe(band);
      unchangedHistory(before, current);
    }
    await page.goto(route(308, 1003, 'cancelar')); await page.getByRole('textbox', { name: 'Motivo (obrigatório)', exact: true }).fill(ownWords);
    const originalVersion = await page.locator('[name="expected_updated_at"]').inputValue();
    const other = await context.newPage(); await other.goto(route(308, 1003)); await set(other, 'description', 'Descanso alterado antes da confirmação'); await preview(other); await save(other); await other.close();
    const concurrent = await facts('edit-cancel-concurrent'); await cancellation(page, 308, 1003, ownWords, 409);
    await expect(page.getByRole('textbox', { name: 'Motivo (obrigatório)', exact: true })).toHaveValue(ownWords); await expect(page.locator('[name="expected_updated_at"]')).toHaveValue(originalVersion);
    await expect(page.getByRole('button', { name: 'Cancelar sessão', exact: true })).toHaveCount(0);
    expect(await facts('edit-cancel-stale')).toEqual(concurrent); await layout(page, 'edit-cancel-conflict320');
    await page.getByRole('link', { name: 'Recarregar sessão atual', exact: true }).click(); await expect(page.locator('[name="expected_updated_at"]')).not.toHaveValue(originalVersion);
    const reason = 'Cancelamento futuro exclusivamente sintético'; await cancellation(page, 308, 1003, reason);
    const cancelled = await facts('edit-cancel-saved'); checkOnlyStatus(getSession(concurrent, 308, 1003), getSession(cancelled, 308, 1003), reason); unchangedHistory(concurrent, cancelled);
    await page.goto(route(308, 1003, 'cancelar')); await expect(page.getByRole('button', { name: 'Cancelar sessão', exact: true })).toHaveCount(0);
    await page.goto(route(308, 1005)); await expect(page.getByRole('heading', { name: 'Treino disponível para consulta', exact: true })).toBeVisible(); await expect(editor(page)).toHaveCount(0);
    await page.goto(route(308, 1005, 'cancelar')); await cancellation(page, 308, 1005, reason);
    const seconds = await facts('edit-cancel-seconds'); checkOnlyStatus(getSession(cancelled, 308, 1005), getSession(seconds, 308, 1005), reason); unchangedHistory(cancelled, seconds);
    await page.goto(route(309, 1006)); await expect(page.getByRole('heading', { name: 'Treino disponível para consulta', exact: true })).toBeVisible(); await expect(editor(page)).toHaveCount(0);
    await page.goto(route(309, 1006, 'cancelar')); await expect(page.getByRole('button', { name: 'Cancelar sessão', exact: true })).toHaveCount(0);
    const today = await facts('edit-today-readonly'); expect(today).toEqual(seconds);
  } finally { await context.close(); }
});
