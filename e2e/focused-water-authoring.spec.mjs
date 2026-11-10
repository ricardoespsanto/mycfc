import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

// Opt in only through the new-owned-resource runner; never use a developer DB.
test.skip(process.env.E2E_FOCUSED_WATER_AUTHORING !== '1', 'Use isolated focused-water runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const editor = page => page.locator('form').filter({ has: page.locator('[name="command"][value="preview"]') });
const confirm = page => page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
const editBack = page => page.locator('form').filter({ has: page.locator('[name="command"][value="edit"]') });
const ownText = 'Primeira linha sintética <água>\nSegunda\tlinha sem ritmo inventado';
function submittedInstructions(response) {
  const fields = [...new URLSearchParams(response.request().postData())];
  return fields.filter(([name, value]) => /^activities\[[^\]]+\]\.phases\[[^\]]+\]\.instructions$/.test(name) && value.trim() !== '').map(([, value]) => value.trim());
}
function submittedDescription(response) { return new URLSearchParams(response.request().postData()).get('description').trim(); }
async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const result = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8'));
  expect(result.error).toBeUndefined(); return result;
}
async function submit(page, button, suffix, expected) {
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix));
  await button.click(); const response = await pending;
  expect(response.status()).toBe(expected); await page.waitForLoadState('networkidle'); return response;
}
async function login(context) {
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill('water-coach@example.test');
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click();
  await expect(page).toHaveURL(/\/today$/); return page;
}
async function openFromWeek(page, plan) {
  const day = fixture().days[plan - 300];
  await page.goto(`/admin/treinos/estruturados?group_id=${id(200)}&week_id=${id(plan)}&day=${day}`);
  const link = page.getByRole('link', { name: new RegExp(`^Treino de água · .*${day}$`) });
  await expect(link).toHaveCount(1); await link.click();
  await expect(page).toHaveURL(new RegExp(`/semanas/${id(plan)}/treinos/criar\\?day=${day}$`));
  await expect(editor(page).locator('[name="schedule.date"]')).toHaveValue(day);
  await expect(editor(page).locator('[name="schedule.date"]')).toHaveAttribute('readonly', '');
  for (const name of ['schedule.start_time', 'schedule.end_date', 'schedule.end_time']) await expect(editor(page).locator(`[name="${name}"]`)).toHaveValue('');
  return day;
}
async function phaseFields(page) {
  return editor(page).locator('textarea[name$=".instructions"]').evaluateAll(nodes => nodes.map(node => node.name));
}
async function fillBase(page, title) {
  const form = editor(page); await form.locator('[name="title"]').fill(title);
  await form.locator('[name="schedule.start_time"]').fill('16:00');
  await form.locator('[name="description"]').fill('Instruções gerais sintéticas\nSegunda\tlinha');
  const fields = await phaseFields(page); expect(fields).toHaveLength(3);
  await form.locator(`[name="${fields[0]}"]`).fill(ownText);
  await form.locator(`[name="${fields[1]}"]`).fill('Trabalho principal pelas próprias palavras');
  return fields;
}
async function layout(page, name, axe = true) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(nodes => nodes.map(node => node.id)); expect(new Set(ids).size).toBe(ids.length);
  if (axe) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result));
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
function graphFor(snapshot, plan) { return snapshot.plans[id(plan)]; }
function assertPlainGraph(graph, { sessionId, title, day, precision, end, instructions, description }) {
  const sessions = graph.sessions.filter(s => s.id === sessionId); expect(sessions).toHaveLength(1);
  const session = sessions[0]; expect(session.title).toBe(title); expect(session.description).toBe(description); expect(session.entry_kind).toBe('TRAINING');
  expect(session.schedule_precision).toBe(precision); expect(session.scheduled_on).toBe(day);
  expect(session.local_start).toBe(`${day} 16:00`); expect(session.local_end).toBe(end);
  if (end === null) expect(session.ends_at).toBeNull();
  expect(session.segments).toHaveLength(1); const segment = session.segments[0];
  expect(segment.modality).toBe('WATER'); expect(segment.position).toBe(1);
  for (const key of ['planned_duration_minutes', 'planned_start_offset_minutes', 'transition_duration_minutes']) expect(segment[key]).toBeNull();
  expect(segment.blocks.map(b => b.position)).toEqual(instructions.map((_, i) => i + 1));
  expect(segment.blocks.map(b => b.instructions)).toEqual(instructions);
  expect(segment.blocks.every(b => b.water_prescription === null && b.gym_prescription === null)).toBe(true);
}
async function reviewedSessionId(page) {
  const raw = await confirm(page).locator('[name="proposal"]').inputValue();
  const value = JSON.parse(raw).proposal.session_id;
  // New-task editable session_id remains blank; only verified proof allocates identity.
  await expect(editBack(page).locator('[name="session_id"]')).toHaveValue('');
  expect(value).toMatch(/^[0-9a-f-]{36}$/); return value;
}

test('real native START_ONLY preview, explicit save and duplicate confirmation', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  const page = await login(context); const plan = 300;
  try {
    const day = await openFromWeek(page, plan), before = await facts('start-before');
    expect(graphFor(before, plan).sessions).toEqual([]); await fillBase(page, 'Água sintética sem fim');
    const preview = await submit(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    expect(submittedInstructions(preview)[0]).toBe(ownText.replaceAll('\n', '\r\n'));
    expect(preview.headers()['cache-control']).toContain('no-store'); expect(preview.headers()['referrer-policy']).toBe('no-referrer');
    await expect(page.locator('.task-form')).toContainText(/fim por definir/i); await expect(page.locator('.task-form')).toContainText('instruções de base');
    expect(await facts('start-preview')).toEqual(before); await layout(page, 'start-preview');
    const sessionId = await reviewedSessionId(page);
    const saved = await submit(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303);
    const requestBody = saved.request().postData(); expect(new URLSearchParams(requestBody).get('review_token')).not.toBe('');
    const first = await facts('start-saved'); expect(first.publications).toEqual(before.publications); expect(first.prescriptions).toEqual(before.prescriptions);
    const graph = graphFor(first, plan); expect(graph.sessions).toHaveLength(1);
    assertPlainGraph(graph, { sessionId, title: 'Água sintética sem fim', day, precision: 'START_ONLY', end: null, instructions: submittedInstructions(preview), description: submittedDescription(preview) });
    // Same original signed HTTP command, same actual authenticated session and CSRF.
    const replay = await page.request.post(saved.url(), { data: requestBody, headers: { 'content-type': 'application/x-www-form-urlencoded' }, maxRedirects: 0 });
    expect([200, 303, 409]).toContain(replay.status());
    expect(await facts('start-replayed')).toEqual(first);
    fs.writeFileSync(`${evidence}/start-replay.json`, JSON.stringify({ status: replay.status(), sameSessionId: sessionId, noGraphOrPublicationChange: true }));
  } finally { await context.close(); }
});

test('native no-JS 320px preserves draft commands and saves an explicit interval', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } });
  const page = await login(context); const plan = 301;
  try {
    const day = await openFromWeek(page, plan), before = await facts('native-before');
    const original = await fillBase(page, 'x');
    await submit(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 422);
    await expect(page.locator('.error-summary')).toBeFocused(); await expect(page.locator('.error-summary a[href="#workout-title"]')).toBeVisible();
    await editor(page).locator('[name="title"]').fill(''); await editor(page).locator('[name="schedule.start_time"]').fill('');
    const activity = original[0].match(/^activities\[([^\]]+)\]/)[1], phase = original[1].match(/\.phases\[([^\]]+)\]/)[1];
    const command = async value => {
      await submit(page, editor(page).locator(`button[name="command"][value="${value}"]`), '/rascunho', 200);
      await expect(editor(page).locator('[name="title"]')).toHaveValue(''); await expect(editor(page).locator('[name="schedule.start_time"]')).toHaveValue('');
      await expect(editor(page).locator(`[name="${original[0]}"]`)).toHaveValue(ownText);
    };
    await command(`phase_add:${activity}`); expect(await phaseFields(page)).toHaveLength(4);
    await command(`phase_remove:${activity}:${phase}`); expect(await phaseFields(page)).toHaveLength(3);
    const restore = await editor(page).locator('button[name="command"][value^="restore:"]').getAttribute('value'); await command(restore);
    await expect(editor(page).locator(`[name="${original[1]}"]`)).toHaveValue('Trabalho principal pelas próprias palavras');
    const order = await phaseFields(page); await command(`phase_up:${activity}:${phase}`);
    expect((await phaseFields(page)).indexOf(original[1])).toBe(order.indexOf(original[1]) - 1);
    await command(`phase_down:${activity}:${phase}`); expect(await phaseFields(page)).toEqual(order);
    expect(await facts('native-commands')).toEqual(before); await layout(page, 'native-incomplete', false);
    const form = editor(page); await form.locator('[name="title"]').fill('Água sintética com fim explícito');
    await form.locator('[name="schedule.start_time"]').fill('16:00'); await form.locator('[name="schedule.end_date"]').fill(day); await form.locator('[name="schedule.end_time"]').fill('16:55');
    await submit(page, form.getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    const sessionId = await reviewedSessionId(page); expect(await facts('native-preview')).toEqual(before);
    await submit(page, editBack(page).getByRole('button', { name: 'Continuar a editar', exact: true }), '/rascunho', 200);
    await expect(editor(page).locator('[name="prior_review_token"]')).not.toHaveValue('');
    await editor(page).locator(`[name="${original[1]}"]`).fill('Trabalho revisto depois da pré-visualização');
    const refreshed = await submit(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    expect(await reviewedSessionId(page)).toBe(sessionId); await layout(page, 'native-interval-preview', false);
    await submit(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303);
    const final = await facts('native-saved'); expect(final.publications).toEqual(before.publications); expect(final.prescriptions).toEqual(before.prescriptions);
    expect(graphFor(final, plan).sessions).toHaveLength(1);
    assertPlainGraph(graphFor(final, plan), { sessionId, title: 'Água sintética com fim explícito', day, precision: 'INTERVAL', end: `${day} 16:55`, instructions: submittedInstructions(refreshed), description: submittedDescription(refreshed) });
  } finally { await context.close(); }
});

test('stale confirmation refuses then deliberate re-preview retains signed create identity', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  const page = await login(context); const plan = 302;
  try {
    const day = await openFromWeek(page, plan), before = await facts('stale-before');
    await fillBase(page, 'Rascunho sintético preservado');
    await submit(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    const sessionId = await reviewedSessionId(page), originalToken = await confirm(page).locator('[name="review_token"]').inputValue();
    const other = await context.newPage(); await openFromWeek(other, plan); await fillBase(other, 'Outro treino sintético concorrente');
    await submit(other, editor(other).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    const otherId = await reviewedSessionId(other); expect(otherId).not.toBe(sessionId);
    await submit(other, confirm(other).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303); await other.close();
    const concurrent = await facts('stale-concurrent'); expect(graphFor(concurrent, plan).sessions).toHaveLength(1);
    await submit(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 409);
    await expect(page.locator('.error-summary')).toBeFocused(); expect(await facts('stale-refused')).toEqual(concurrent);
    await expect(editor(page).locator('[name="title"]')).toHaveValue('Rascunho sintético preservado');
    const fields = await phaseFields(page); await expect(editor(page).locator(`[name="${fields[0]}"]`)).toHaveValue(ownText);
    const refreshed = await submit(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', 200);
    expect(await reviewedSessionId(page)).toBe(sessionId); await expect(confirm(page).locator('[name="review_token"]')).not.toHaveValue(originalToken);
    await layout(page, 'stale-repreview');
    await submit(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303);
    const final = await facts('stale-saved'); expect(graphFor(final, plan).sessions).toHaveLength(2);
    expect(graphFor(final, plan).sessions.find(s => s.id === otherId)).toEqual(graphFor(concurrent, plan).sessions[0]);
    assertPlainGraph(graphFor(final, plan), { sessionId, title: 'Rascunho sintético preservado', day, precision: 'START_ONLY', end: null, instructions: submittedInstructions(refreshed), description: submittedDescription(refreshed) });
    expect(final.publications).toEqual(before.publications); expect(final.prescriptions).toEqual(before.prescriptions);
  } finally { await context.close(); }
});
