import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_FULL_WORKOUT_AUTHORING !== '1', 'Use isolated full-CREATE runner.');
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const id = n => `39139900-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const editor = page => page.locator('form').filter({ has: page.locator('[name="command"][value="preview"]') });
const confirm = page => page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
const editBack = page => page.locator('form').filter({ has: page.locator('[name="command"][value="edit"]') });
const ap = activity => `activities[${activity}]`;
const pp = (activity, phase) => `${ap(activity)}.phases[${phase}]`;
const np = (activity, phase, node) => `${pp(activity, phase)}.water.nodes[${node}]`;
const ep = (activity, phase, exercise) => `${pp(activity, phase)}.gym.exercises[${exercise}]`;
const ownWords = 'Instruções sintéticas <seguras>\nSegunda\tlinha preservada';
const field = (page, name) => editor(page).locator(`[name="${name}"]`);
const savedPlan = (snapshot, plan) => snapshot.plans[id(plan)];
const savedSession = (snapshot, plan, sessionId) => savedPlan(snapshot, plan).sessions.find(s => s.id === sessionId);
const review = async page => JSON.parse(await confirm(page).locator('[name="proposal"]').inputValue()).proposal;
const rawSubmitted = (response, name) => new URLSearchParams(response.request().postData()).get(name).trim();
async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const value = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8'));
  expect(value.error).toBeUndefined(); return value;
}
function noPublicationChange(before, after) {
  for (const key of ['publications', 'prescriptions', 'outcomes']) expect(after[key]).toEqual(before[key]);
}
async function reveal(control) {
  await expect(control).toHaveCount(1);
  // Open native details from the outside inward; never inject values or bypass validity.
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
  expect(invalid, 'Native constraints must allow this real form submission').toEqual([]);
  const pending = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith(suffix), { timeout: 15_000 });
  await button.click(); const response = await pending; expect(response.status()).toBe(status);
  await page.waitForLoadState('networkidle'); return response;
}
async function command(page, value) { return post(page, editor(page).locator(`button[name="command"][value="${value}"]`), '/rascunho'); }
async function preview(page, status = 200) { return post(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever', status); }
async function save(page) { return post(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303); }
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
  const link = page.locator(`a[aria-label$="${day}"][href="/admin/treinos/estruturados/semanas/${id(plan)}/treinos/criar?day=${day}"]`);
  await expect(link).toHaveCount(1); await link.click();
  await expect(page).toHaveURL(new RegExp(`/semanas/${id(plan)}/treinos/criar\\?day=${day}$`));
  await expect(field(page, 'schedule.date')).toHaveValue(day); await expect(field(page, 'schedule.date')).toHaveAttribute('readonly', '');
  for (const name of ['schedule.start_time', 'schedule.end_date', 'schedule.end_time']) await expect(field(page, name)).toHaveValue('');
  return day;
}
async function keys(page, prefix, suffix, pattern) {
  const names = await editor(page).locator(`[name^="${prefix}"][name$="${suffix}"]`).evaluateAll(nodes => nodes.map(n => n.name));
  return names.map(name => name.match(pattern)[1]);
}
async function waterKeys(page, activity, phase) { return keys(page, `${pp(activity, phase)}.water.nodes[`, '.kind', /\.nodes\[([^\]]+)\]/); }
async function exerciseKeys(page, activity, phase) { return keys(page, `${pp(activity, phase)}.gym.exercises[`, '.name', /\.exercises\[([^\]]+)\]/); }
async function phaseKeys(page, activity) { return keys(page, `${ap(activity)}.phases[`, '.purpose', /\.phases\[([^\]]+)\]/); }
async function newWaterNode(page, activity, phase, parent, kind, name, repeats, recovery) {
  const before = await waterKeys(page, activity, phase);
  await command(page, `water_${kind === 'REPEAT_GROUP' ? 'group' : 'effort'}_add:${activity}:${phase}:${parent || '_root'}`);
  const added = (await waterKeys(page, activity, phase)).filter(key => !before.includes(key)); expect(added).toHaveLength(1);
  const node = added[0]; await set(page, `${np(activity, phase, node)}.name`, name);
  if (repeats !== null) await set(page, `${np(activity, phase, node)}.repeats`, String(repeats));
  if (recovery !== null) await set(page, `${np(activity, phase, node)}.recovery_seconds`, String(recovery));
  return node;
}
async function newExercise(page, activity, phase, values) {
  const before = await exerciseKeys(page, activity, phase); await command(page, `gym_add:${activity}:${phase}`);
  const added = (await exerciseKeys(page, activity, phase)).filter(key => !before.includes(key)); expect(added).toHaveLength(1);
  for (const [name, value] of Object.entries(values)) await set(page, `${ep(activity, phase, added[0])}.${name}`, String(value));
  return added[0];
}
async function layout(page, name, axe = true, fullPage = true) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(nodes => nodes.map(n => n.id)); expect(new Set(ids).size).toBe(ids.length);
  if (axe) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result));
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage });
}
async function canonicalNotes(page) { return page.locator('section[aria-label="Instruções do treino"] p.preserve-lines').allTextContents(); }
async function checkCanonicalReadback(page, sessionId, notes, names) {
  const details = page.locator('#workspace-details');
  if (await details.count() && (await details.getAttribute('open')) === null) await details.locator(':scope > summary').click();
  const content = page.locator(`#session-content-${sessionId}`); await reveal(content);
  const storedNotes = await content.locator('p.preserve-lines').allTextContents();
  expect(storedNotes.map(s => s.replaceAll('\r\n', '\n'))).toEqual(notes.map(s => s.replaceAll('\r\n', '\n')));
  for (const name of names) await expect(content).toContainText(name);
}
function assertCommonSession(session, day, kind, precision, start, end) {
  expect(session).toBeDefined(); expect(session.entry_kind).toBe(kind); expect(session.schedule_precision).toBe(precision);
  expect(session.scheduled_on).toBe(day); expect(session.local_start).toBe(start); expect(session.local_end).toBe(end);
  if (start === null) expect(session.starts_at).toBeNull(); if (end === null) expect(session.ends_at).toBeNull();
}
function retainedIds(proposal) {
  return proposal.activities.flatMap(a => [a.id, ...a.phases.flatMap(p => [p.id, ...(p.water?.nodes || []).map(n => n.id), ...(p.gym?.exercises || []).map(e => e.id)])]);
}

test('full water nested instructions preview/edit-back/stale recovery save one complete raw graph', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
  const page = await login(context); const plan = 303, activity = 'a_1', phase = 'p_2';
  try {
    const day = await openFromWeek(page, plan), before = await facts('full-water-before');
    await set(page, 'title', 'Água estruturada exclusivamente sintética'); await set(page, 'schedule.start_time', '16:00');
    await set(page, 'description', ownWords); await set(page, `${pp(activity, phase)}.instructions`, 'Continuar até 8 km, por instrução sintética');
    await set(page, `${pp(activity, phase)}.detail_mode`, 'WATER'); await command(page, 'refresh');
    await set(page, `${pp(activity, phase)}.water.method`, 'INTERVALS');
    await expect(field(page, `${pp(activity, phase)}.water.profile_id`)).toHaveValue('');
    await set(page, `${pp(activity, phase)}.water.profile_id`, id(900));
    await set(page, `${pp(activity, phase)}.water.target_distance_metres`, '8000'); await set(page, `${pp(activity, phase)}.water.target_distance_certainty`, 'EXACT');
    const chain = [];
    chain.push(await newWaterNode(page, activity, phase, '', 'REPEAT_GROUP', 'Conjunto exterior sintético', 2, 90));
    chain.push(await newWaterNode(page, activity, phase, chain[0], 'REPEAT_GROUP', 'Dez repetições sintéticas', 10, 30));
    // One accepted depth8 graph supplies a real narrow-layout observation, not a matrix.
    for (let level = 3; level <= 7; level++) chain.push(await newWaterNode(page, activity, phase, chain.at(-1), 'REPEAT_GROUP', `Grupo sintético nível ${level}`, 1, null));
    const effort = await newWaterNode(page, activity, phase, chain.at(-1), 'EFFORT', 'Quatro minutos exclusivamente sintéticos', null, 120); chain.push(effort);
    for (const [name, value] of Object.entries({ duration_seconds: '240', duration_certainty: 'EXACT', intensity_code: 'SYN2', cadence_spm: '75', instructions: ownWords })) await set(page, `${np(activity, phase, effort)}.${name}`, value);
    await set(page, `${pp(activity, 'p_3')}.purpose`, 'CUSTOM'); // unused scaffold must not require a fabricated title
    await page.setViewportSize({ width: 320, height: 1000 }); await field(page, `${np(activity, phase, effort)}.name`).scrollIntoViewIfNeeded(); await layout(page, 'full-water-depth8-320', false, false);
    await page.setViewportSize({ width: 1280, height: 1000 }); await preview(page); const initial = await review(page);
    expect(initial.activities).toHaveLength(1); expect(initial.activities[0].phases).toHaveLength(1);
    expect(await facts('full-water-preview')).toEqual(before);
    await expect(page.locator('.task-form')).toContainText(/fim por definir/i); await expect(page.locator('.task-form')).toContainText('SYNTHETIC ONLY');
    await post(page, editBack(page).getByRole('button', { name: 'Continuar a editar', exact: true }), '/rascunho');
    await expect(field(page, 'prior_review_token')).not.toHaveValue(''); await set(page, `${pp(activity, phase)}.instructions`, 'Meta sintética de 8 km revista sem fabricar uma hora de fim');
    await preview(page); const edited = await review(page); expect(edited.session_id).toBe(initial.session_id); expect(retainedIds(edited)).toEqual(retainedIds(initial));
    const competitor = await context.newPage(); await openFromWeek(competitor, plan);
    await set(competitor, 'title', 'Concorrente sintético legítimo'); await set(competitor, 'schedule.start_time', '17:00'); await set(competitor, `${pp('a_1', 'p_1')}.instructions`, 'Instruções concorrentes pelas próprias palavras');
    await preview(competitor); const otherId = (await review(competitor)).session_id; await save(competitor); await competitor.close();
    const concurrent = await facts('full-water-concurrent');
    await post(page, confirm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 409);
    await expect(page.locator('.error-summary')).toBeFocused(); expect(await facts('full-water-stale')).toEqual(concurrent);
    await expect(field(page, `${np(activity, phase, effort)}.instructions`)).toHaveValue(ownWords);
    const finalPreview = await preview(page), finalProposal = await review(page); expect(finalProposal.session_id).toBe(initial.session_id); expect(retainedIds(finalProposal)).toEqual(retainedIds(initial));
    const notes = await canonicalNotes(page); await layout(page, 'full-water-preview'); await save(page);
    const final = await facts('full-water-saved'), session = savedSession(final, plan, initial.session_id);
    noPublicationChange(before, final); expect(final.profiles).toEqual(before.profiles); expect(final.zones).toEqual(before.zones);
    expect(savedPlan(final, plan).sessions).toHaveLength(2); expect(savedSession(final, plan, otherId)).toEqual(savedSession(concurrent, plan, otherId));
    assertCommonSession(session, day, 'TRAINING', 'START_ONLY', `${day} 16:00`, null);
    expect(session.segments).toHaveLength(1); const segment = session.segments[0]; expect(segment.id).toBe(finalProposal.activities[0].id); expect(segment.modality).toBe('WATER');
    expect(segment.planned_duration_minutes).toBeNull(); expect(segment.blocks).toHaveLength(1); const block = segment.blocks[0];
    expect(block.instructions).toBe(rawSubmitted(finalPreview, `${pp(activity, phase)}.instructions`));
    expect(block.water_prescription).toMatchObject({ method: 'INTERVALS', intensity_profile_id: id(900), target_distance_metres: 8000, target_distance_certainty: 'EXACT' });
    expect(block.gym_prescription).toBeNull(); expect(block.gym_exercises).toEqual([]); expect(block.water_steps).toHaveLength(8);
    const proposedNodes = finalProposal.activities[0].phases[0].water.nodes;
    for (let index = 0; index < chain.length; index++) {
      const node = proposedNodes.find(n => n.key === chain[index]); expect(node).toBeDefined(); const stored = block.water_steps.find(n => n.id === node.id);
      const parentId = index === 0 ? null : proposedNodes.find(n => n.key === chain[index - 1]).id;
      expect(stored).toMatchObject({ parent_step_id: parentId, position: 1, kind: index === 7 ? 'EFFORT' : 'REPEAT_GROUP', repeats: index === 0 ? 2 : index === 1 ? 10 : index === 7 ? null : 1, recovery_seconds: index === 0 ? 90 : index === 1 ? 30 : index === 7 ? 120 : null });
      if (index < 7) { expect(stored.duration_seconds).toBeNull(); expect(stored.distance_metres).toBeNull(); }
      else expect(stored).toMatchObject({ duration_seconds: 240, duration_certainty: 'EXACT', distance_metres: null, intensity_code: 'SYN2', cadence_spm: 75, instructions: rawSubmitted(finalPreview, `${np(activity, phase, effort)}.instructions`) });
    }
    await checkCanonicalReadback(page, initial.session_id, notes, ['Quatro minutos exclusivamente sintéticos', 'SYNTHETIC ONLY']);
  } finally { await context.close(); }
});

test('native no-JS320 gym circuits/supersets and RUN remain one ordered explicit-interval workout', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 320, height: 1000 } });
  const page = await login(context); const plan = 304, activity = 'a_1';
  try {
    const day = await openFromWeek(page, plan), before = await facts('full-gym-before');
    await set(page, 'title', 'x'); await set(page, 'schedule.start_time', '18:00'); await set(page, `${pp(activity, 'p_2')}.instructions`, 'Força A sintética, pelas palavras do treinador');
    await preview(page, 422); await expect(page.locator('.error-summary')).toBeFocused(); await expect(page.locator('.error-summary a[href="#workout-title"]')).toBeVisible();
    await set(page, 'title', ''); await set(page, 'schedule.start_time', ''); await set(page, `${ap(activity)}.modality`, 'GYM'); await command(page, 'refresh');
    await set(page, 'description', ownWords); await set(page, `${pp(activity, 'p_2')}.detail_mode`, 'GYM'); await command(page, 'refresh');
    for (const [name, value] of Object.entries({ structure: 'CIRCUIT', objective: 'MAX_STRENGTH_HYPERTROPHY', rounds: '3', round_recovery_seconds: '60' })) await set(page, `${pp(activity, 'p_2')}.gym.${name}`, value);
    const circuitExercise = await newExercise(page, activity, 'p_2', { name: 'Supino exclusivamente sintético', sets: 4, repetitions: 12, recovery_seconds: 120, resistance_kind: 'KILOGRAMS', resistance_value: '40.5', execution_intent: 'CONTROLLED', tempo: '2-0-1', notes: ownWords });
    await set(page, `${pp(activity, 'p_3')}.purpose`, 'CUSTOM'); await set(page, `${pp(activity, 'p_3')}.title`, 'Força B sintética'); await set(page, `${pp(activity, 'p_3')}.instructions`, 'Supersérie explícita sem deduzir cargas'); await set(page, `${pp(activity, 'p_3')}.detail_mode`, 'GYM'); await command(page, 'refresh');
    for (const [name, value] of Object.entries({ structure: 'SUPERSET', objective: 'STRENGTH_ENDURANCE', rounds: '2', round_recovery_seconds: '90' })) await set(page, `${pp(activity, 'p_3')}.gym.${name}`, value);
    const first = await newExercise(page, activity, 'p_3', { name: 'Tração sintética', sets: 3, repetitions: 8, resistance_kind: 'BODY_WEIGHT' });
    const second = await newExercise(page, activity, 'p_3', { name: 'Agachamento sintético', sets: 3, repetitions: 6, resistance_kind: 'PERCENT_1RM', resistance_value: '70.5' });
    await command(page, `gym_up:${activity}:p_3:${second}`); await command(page, `gym_down:${activity}:p_3:${second}`); expect(await exerciseKeys(page, activity, 'p_3')).toEqual([first, second]);
    await command(page, `phase_add:${activity}`); // unused custom scaffold remains optional
    await expect(field(page, 'title')).toHaveValue(''); await expect(field(page, 'schedule.start_time')).toHaveValue(''); await expect(field(page, `${ep(activity, 'p_2', circuitExercise)}.notes`)).toHaveValue(ownWords);
    const beforeActivities = await keys(page, 'activities[', '.modality', /^activities\[([^\]]+)\]/); await command(page, 'activity_add');
    const run = (await keys(page, 'activities[', '.modality', /^activities\[([^\]]+)\]/)).find(key => !beforeActivities.includes(key)); expect(run).toBeDefined();
    await set(page, `${ap(run)}.modality`, 'RUN'); await command(page, 'refresh');
    if ((await phaseKeys(page, run)).length === 0) await command(page, `phase_add:${run}`);
    const runPhases = await phaseKeys(page, run); expect(runPhases.length).toBeGreaterThan(0); const runPhase = runPhases[0];
    await set(page, `${pp(run, runPhase)}.purpose`, 'MAIN'); await set(page, `${pp(run, runPhase)}.instructions`, 'Corrida pelas próprias palavras, sem fabricar duração');
    expect(await facts('full-gym-commands')).toEqual(before); await layout(page, 'full-gym-incomplete-320', false, false);
    await set(page, 'title', 'Força A e B com corrida sintética'); await set(page, 'schedule.start_time', '18:00');
    await expect(field(page, 'schedule.mode')).toHaveValue('START_ONLY'); await set(page, 'schedule.end_date', day); await set(page, 'schedule.end_time', '19:15');
    const previewResponse = await preview(page), proposal = await review(page); expect(proposal.schedule.precision).toBe('INTERVAL');
    expect(await facts('full-gym-preview')).toEqual(before); const notes = await canonicalNotes(page); await layout(page, 'full-gym-preview-320', false); await save(page);
    const final = await facts('full-gym-saved'), session = savedSession(final, plan, proposal.session_id); noPublicationChange(before, final);
    expect(savedPlan(final, plan).sessions).toHaveLength(1); assertCommonSession(session, day, 'TRAINING', 'INTERVAL', `${day} 18:00`, `${day} 19:15`);
    expect(session.segments.map(s => s.modality)).toEqual(['GYM', 'RUN']); expect(session.segments.map(s => s.position)).toEqual([1, 2]);
    const gym = session.segments[0]; expect(gym.blocks).toHaveLength(2);
    expect(gym.blocks[0].gym_prescription).toMatchObject({ structure: 'CIRCUIT', rounds: 3, round_recovery_seconds: 60 }); expect(gym.blocks[0].gym_exercises).toHaveLength(1);
    expect(gym.blocks[0].gym_exercises[0]).toMatchObject({ name: 'Supino exclusivamente sintético', sets: 4, repetitions: 12, recovery_seconds: 120, resistance_kind: 'KILOGRAMS', resistance_value: 40.5, execution_intent: 'CONTROLLED', tempo: '2-0-1', notes: rawSubmitted(previewResponse, `${ep(activity, 'p_2', circuitExercise)}.notes`) });
    expect(gym.blocks[1].gym_prescription).toMatchObject({ structure: 'SUPERSET', rounds: 2, round_recovery_seconds: 90 }); expect(gym.blocks[1].gym_exercises.map(e => e.name)).toEqual(['Tração sintética', 'Agachamento sintético']);
    expect(gym.blocks[1].gym_exercises[0]).toMatchObject({ resistance_kind: 'BODY_WEIGHT', resistance_value: null, resistance_text: null }); expect(gym.blocks[1].gym_exercises[1]).toMatchObject({ resistance_kind: 'PERCENT_1RM', resistance_value: 70.5 });
    for (const block of gym.blocks) { expect(block.water_prescription).toBeNull(); expect(block.water_steps).toEqual([]); }
    expect(session.segments[1].planned_duration_minutes).toBeNull(); expect(session.segments[1].blocks).toHaveLength(1); expect(session.segments[1].blocks[0]).toMatchObject({ instructions: rawSubmitted(previewResponse, `${pp(run, runPhase)}.instructions`), water_prescription: null, gym_prescription: null });
    expect(session.segments.map(s => s.id)).toEqual(proposal.activities.map(a => a.id));
    await checkCanonicalReadback(page, proposal.session_id, notes, ['Supino exclusivamente sintético', 'Tração sintética', 'Agachamento sintético']);
  } finally { await context.close(); }
});

test('REST DAY and LOGISTICS BAND keep notes and require explicit contradictory activity removal', async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } }); const page = await login(context); const plan = 305;
  try {
    const day = await openFromWeek(page, plan), before = await facts('full-rest-before');
    await set(page, 'title', 'Descanso exclusivamente sintético'); await set(page, 'description', ownWords);
    await set(page, `${pp('a_1', 'p_2')}.instructions`, 'Atividade contraditória que deve ser preservada');
    await set(page, 'entry_kind', 'REST'); await command(page, 'refresh'); await set(page, 'schedule.mode', 'DAY'); await command(page, 'refresh');
    await preview(page, 422); await expect(page.locator('.error-summary')).toBeFocused(); await expect(field(page, `${pp('a_1', 'p_2')}.instructions`)).toHaveValue('Atividade contraditória que deve ser preservada');
    expect(await facts('full-rest-contradiction')).toEqual(before); await command(page, 'activity_remove:a_1');
    const restPreview = await preview(page), restProposal = await review(page); expect(restProposal.activities).toEqual([]); expect(await facts('full-rest-preview')).toEqual(before); await layout(page, 'full-rest-day-preview'); await save(page);
    const first = await facts('full-rest-saved'), rest = savedSession(first, plan, restProposal.session_id); assertCommonSession(rest, day, 'REST', 'DAY', null, null);
    expect(rest.day_band).toBeNull(); expect(rest.segments).toEqual([]); expect(rest.description).toBe(rawSubmitted(restPreview, 'description')); noPublicationChange(before, first);
    await openFromWeek(page, plan); await set(page, 'title', 'Logística exclusivamente sintética'); await set(page, 'description', 'Notas logísticas sintéticas, sem esforço ou horário inventados'); await set(page, 'entry_kind', 'LOGISTICS'); await command(page, 'refresh'); await set(page, 'schedule.mode', 'BAND'); await command(page, 'refresh');
    await set(page, 'schedule.band', 'AFTERNOON');
    // Leave the genuinely unused initial activity scaffold for approved omission.
    await preview(page); const logisticsProposal = await review(page);
    expect(logisticsProposal.activities).toEqual([]); expect(await facts('full-logistics-preview')).toEqual(first); await save(page);
    const final = await facts('full-logistics-saved'), logistics = savedSession(final, plan, logisticsProposal.session_id);
    assertCommonSession(logistics, day, 'LOGISTICS', 'BAND', null, null); expect(logistics.day_band).toBe('AFTERNOON'); expect(logistics.segments).toEqual([]);
    expect(savedPlan(final, plan).sessions).toHaveLength(2); expect(savedSession(final, plan, restProposal.session_id)).toEqual(rest); noPublicationChange(before, final);
  } finally { await context.close(); }
});
