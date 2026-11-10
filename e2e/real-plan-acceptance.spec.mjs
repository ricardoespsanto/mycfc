import { test, expect, chromium } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

// Explicit opt-in, NEW fixture, actual application/session/CSRF. No target SQL repair.
test.skip(process.env.E2E_REAL_PLANS !== '1', 'Use the owned real-plan acceptance runner.');
// Clear inherited DesktopChrome emulation so the genuine browser-zoom context can use viewport:null.
test.use({ deviceScaleFactor: undefined, isMobile: undefined });
const evidence = process.env.E2E_FOCUSED_WATER_EVIDENCE;
const input = JSON.parse(fs.readFileSync(new URL('./real-plan-input.json', import.meta.url), 'utf8'));
const id = n => `39140400-0000-0000-0000-${String(n).padStart(12, '0')}`;
const stateFile = `${evidence}/real-plan-state.json`;
const state = () => JSON.parse(fs.readFileSync(stateFile, 'utf8'));
const writeState = value => fs.writeFileSync(stateFile, JSON.stringify(value, null, 2));
const dayPlus = (date, n) => { const d = new Date(`${date}T12:00:00Z`); d.setUTCDate(d.getUTCDate() + n); return d.toISOString().slice(0, 10); };
const today = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8')).synthetic_dates.fixture_date;
const root = '/admin/treinos/estruturados';
const editor = page => page.locator('form').filter({ has: page.locator('[name="command"][value="preview"]') });
const saveForm = page => page.locator('form').filter({ has: page.locator('[name="intent"][value="confirm"]') });
const pubForm = (page, intent) => page.locator('form').filter({ has: page.locator(`[name="publication_intent"][value="${intent}"]`) });
const titleFor = entry => `${entry.sourceKind === 'REST' ? 'Descanso' : entry.sourceKind === 'GYM' ? 'Ginásio' : 'Água'} · ${entry.replayDate} · ${entry.sourceStartLocal || (entry.sourceBand === 'MORNING' ? 'Manhã' : 'Tarde')}`;
const literal = entry => entry.observedLines.join('\n');
const personNames = { 8100: 'Referência um K menor sintética', 8101: 'Referência um C sintética', 8102: 'Referência um KC sintética', 8103: 'Referência dois K sintética', 8104: 'Referência dois C sintética', 8105: 'Referência dois KC sintética' };
const ownRows = (facts, person) => facts.prescriptions.filter(row => row.athlete_user_id === id(person));
const livePlacements = (facts, person) => facts.placements.filter(row => row.subject_user_id === id(person) && !row.cancelled_by_change_id && !row.superseded_by_change_id);
async function facts(name) {
  // Repeated reviews of one plan require NEW reads, never the earlier callback result.
  let label = name, attempt = 1;
  while (fs.existsSync(`${evidence}/${label}-ready.json`) || fs.existsSync(`${evidence}/${label}-done.json`)) label = `${name}-read-${attempt++}`;
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  const value = JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8')); expect(value.error).toBeUndefined(); return value;
}
async function login(context, email = 'real-plan-admin@example.test') {
  context.setDefaultTimeout(15_000); context.setDefaultNavigationTimeout(15_000);
  const page = await context.newPage(); await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click(); await expect(page).toHaveURL(/\/today$/); return page;
}
async function reveal(control) {
  await expect(control).toHaveCount(1);
  for (let i = 0; i < 24 && !(await control.isVisible()); i++) {
    const details = control.locator('xpath=ancestor::details[not(@open)][last()]');
    await expect(details).toHaveCount(1); await details.locator(':scope > summary').click();
  }
  await expect(control).toBeVisible();
}
async function setField(page, name, value) {
  const field = editor(page).locator(`[name="${name}"]`); await reveal(field);
  if (await field.evaluate(el => el.tagName === 'SELECT')) await field.selectOption(value); else await field.fill(value);
}
async function post(page, button, suffix, status = 200, keyboard = false) {
  await reveal(button);
  expect(await button.evaluate(el => el.formNoValidate ? [] : [...el.form.elements].filter(c => c.willValidate && !c.validity.valid).map(c => c.name)), 'Native validity before real POST').toEqual([]);
  const pending = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith(suffix), { timeout: 20_000 });
  if (keyboard) { await button.focus(); await expect(button).toBeFocused(); await page.keyboard.press('Enter'); } else await button.click();
  const response = await pending; expect(response.status()).toBe(status); await page.waitForLoadState('networkidle'); return response;
}
async function command(page, value) { return post(page, editor(page).locator(`button[name="command"][value="${value}"]`), '/rascunho'); }
async function screenshot(page, name, { axe = true, fullPage = true } = {}) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  const ids = await page.locator('[id]').evaluateAll(elements => elements.map(el => el.id)); expect(new Set(ids).size).toBe(ids.length);
  if (axe) {
    const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(scan));
    expect(scan.violations.filter(item => ['serious', 'critical'].includes(item.impact))).toEqual([]);
  }
  fs.writeFileSync(`${evidence}/${name}.html`, await page.content()); await page.screenshot({ path: `${evidence}/${name}.png`, fullPage });
}
async function planner(page, group, week = '', day = input.replayWeek[0], session = '') {
  const query = new URLSearchParams({ group_id: group, day }); if (week) query.set('week_id', week); if (session) query.set('session_id', session);
  await page.goto(`${root}?${query}`);
}
function notesFor(plan) {
  const totals = Object.entries(plan.referenceSupplement.referenceTotals).map(([code, value]) => `${code}: ${value === null ? 'sem valor indicado' : value}`).join('; ');
  return [`Referência fornecida: ${input.header.microcycleLabel} · ${input.header.referenceWeek} · ${input.header.coachLabel} · ${input.header.seasonLabel}.`,
    `Reprodução sintética autorizada: ${input.replayWeek.join(' — ')}. Datas reais da época desconhecidas/configuráveis.`,
    `Valores literais da folha: ${totals}.`, ...plan.referenceSupplement.objectives,
    `Escala de carga apresentada: ${input.loadScaleLegend.join(', ')}. Nenhuma percentagem selecionada foi inferida.`, ...plan.referenceSupplement.columnAnnotations].join('\n');
}
async function createGroup(page, name) {
  await page.goto(`${root}/grupos/criar`); await page.getByLabel('Nome do grupo', { exact: false }).fill(name);
  await page.getByLabel('Participação do grupo', { exact: false }).selectOption({ label: 'Competição' });
  await post(page, page.getByRole('button', { name: 'Criar grupo', exact: true }), '/grupos/criar', 303, true);
  await expect(page.getByText('Ainda sem atletas', { exact: true })).toBeVisible(); return new URL(page.url()).pathname.split('/').at(-1);
}
async function addAthletes(page, group, persons, season) {
  await page.goto(`${root}/grupos/${group}/atletas`);
  await page.getByLabel('Época', { exact: false }).selectOption(season); await page.getByLabel('Data da colocação', { exact: false }).fill(today());
  await page.getByLabel('Nome do atleta', { exact: true }).fill('Referência'); await page.getByRole('button', { name: 'Pesquisar', exact: true }).click();
  for (const person of persons) {
    const link = page.locator(`a[href*="person_id=${id(person)}"]`).filter({ hasText: 'Preparar colocação' }); await expect(link).toHaveCount(1);
    await page.getByRole('checkbox', { name: personNames[person], exact: true }).check();
  }
  await post(page, page.getByRole('button', { name: 'Adicionar atletas', exact: true }), `/grupos/${group}/atletas`, 303, true);
}
async function createWeek(page, group, title, date, description = '') {
  await page.goto(`${root}/grupos/${group}`); await page.getByRole('link', { name: 'Criar semana', exact: true }).click();
  const form = page.locator(`form[action="${root}/semanas"]`); await reveal(form.locator('[name="title"]'));
  await form.locator('[name="group_id"]').selectOption(group); await form.locator('[name="title"]').fill(title); await form.locator('[name="week_start"]').fill(date);
  await form.locator('[name="description"]').fill(description); await expect(form.locator('[name="planned_load_percentage"]')).toHaveValue('');
  await post(page, form.getByRole('button', { name: 'Criar semana', exact: true }), '/semanas', 303);
  return new URL(page.url()).searchParams.get('week_id');
}
async function profileZone(page, group, profile, code, value, noteSuffix = '') {
  await planner(page, group); const open = page.locator(`button[data-dialog-open="add-water-zone-${profile}"]`); await reveal(open); await open.click();
  const form = page.locator(`form[action="${root}/agua/perfis/${profile}/zonas"]`);
  await form.locator('[name="code"]').fill(code); await form.locator('[name="label"]').fill(`${code} — ${value}`);
  const bounds = value.includes('–') ? value.split('–') : ['', ''];
  await form.locator('[name="cadence_min"]').fill(bounds[0]); await form.locator('[name="cadence_max"]').fill(bounds[1]);
  await form.locator('[name="meaning"]').fill(value + noteSuffix);
  await post(page, form.getByRole('button', { name: 'Guardar zona', exact: true }), '/zonas', 303);
}
async function createProfile(page, group, name, craft, values) {
  await planner(page, group); const button = page.locator('button[data-dialog-open="add-water-profile"]'); await reveal(button); await button.click();
  const form = page.locator(`form[action="${root}/agua/perfis"]`);
  await expect(form.locator('[name="craft"]')).toHaveValue(''); await form.locator('[name="craft"]').selectOption(craft); await form.locator('[name="name"]').fill(name);
  await form.locator('[name="notes"]').fill('Valores de referência fornecidos e aprovados. Sobreposições intencionais; sem normalização nem inferência de idade ou embarcação.');
  await post(page, form.getByRole('button', { name: 'Guardar perfil', exact: true }), '/agua/perfis', 303);
  const result = await facts(`profile-${craft}-${name.includes('mais novos') ? 'young' : name.includes('mais velhos') ? 'older' : 'shared'}`);
  const profile = result.profiles.find(p => p.name === name); expect(profile.craft).toBe(craft);
  for (const [code, value] of Object.entries(values)) await profileZone(page, group, profile.id, code, value);
  return profile.id;
}
async function classify(page, person, craftCodes, season) {
  await page.goto(`/equipa/classificacao/${id(person)}`);
  const participation = page.locator('form').first();
  const value = await participation.locator('#scope option').filter({ hasText: 'Competição' }).filter({ hasText: 'Época sintética de aceitação' }).getAttribute('value');
  expect(value.startsWith(`${season}:`)).toBe(true); await participation.locator('#scope').selectOption(value);
  await participation.locator('#starts_on').fill(today());
  await participation.locator('#category_id').selectOption({ label: 'Escalão sintético abrangente (elegível)' });
  await post(page, participation.getByRole('button', { name: 'Guardar participação', exact: true }), `/classificacao/${id(person)}`);
  await post(page, page.getByRole('button', { name: 'Confirmar participação', exact: true }), `/classificacao/${id(person)}`);
  const sports = page.locator('form').filter({ has: page.locator('[name="kind"][value="SPORT"]') });
  await sports.locator('[value="CANOEING"]').check(); await sports.locator('[name="reason"]').fill('Seleção sintética explícita para a aceitação autorizada.');
  await post(page, sports.getByRole('button', { name: 'Guardar conjunto de modalidades', exact: true }), new URL(await sports.getAttribute('action'), page.url()).pathname);
  const crafts = page.locator('form').filter({ has: page.locator('[name="kind"][value="CRAFT"]') });
  for (const code of craftCodes) await crafts.locator(`[name="codes"][value="${code}"]`).check();
  await crafts.locator('[name="reason"]').fill('Embarcações escolhidas explicitamente no ensaio sintético.');
  await post(page, crafts.getByRole('button', { name: 'Guardar conjunto de embarcações', exact: true }), new URL(await crafts.getAttribute('action'), page.url()).pathname);
}

test('real-plan clean target setup creates groups season eligibility and all approved rhythm references through UI', async ({ browser }) => {
  test.setTimeout(900_000); const started = Date.now(); const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }), page = await login(context);
  try {
    const initial = await facts('real-plan-initial'); expect(initial.groups).toEqual([]); expect(initial.profiles).toEqual([]);
    expect(initial.memberships).toHaveLength(0); expect(initial.publications).toHaveLength(0); expect(initial.prescriptions).toHaveLength(0); expect(initial.outcomes).toHaveLength(0);
    const value = { groups: [], plans: [], profiles: {}, entries: {}, setupStarted: started };
    for (const plan of input.plans) value.groups.push(await createGroup(page, `${plan.observedGroupLabel} · aceitação sintética`));
    await page.goto('/admin/epocas/criar'); await expect(page.locator('#season-start')).toHaveValue(''); await expect(page.locator('#season-end')).toHaveValue('');
    await page.locator('#season-code').focus(); await page.keyboard.type('SYN404'); await page.keyboard.press('Tab'); await expect(page.locator('#season-name')).toBeFocused();
    await page.locator('#season-name').fill('Época sintética de aceitação'); await page.locator('#season-start').fill(input.syntheticSeason.startsOn); await page.locator('#season-end').fill(input.syntheticSeason.endsOn);
    await post(page, page.getByRole('button', { name: 'Criar época', exact: true }), '/admin/epocas/criar', 303, true);
    value.season = new URL(page.url()).pathname.split('/').at(-2);
    await post(page, page.getByRole('button', { name: 'Pré-visualizar alteração', exact: true }), '/selecionar');
    await post(page, page.getByRole('button', { name: 'Confirmar época atual', exact: true }), '/selecionar', 303, true);
    await page.goto('/equipa/escaloes');
    await page.locator('#scope').selectOption(await page.locator('#scope option').filter({ hasText: 'Competição' }).getAttribute('value'));
    await page.locator('#code').fill('SYN404'); await page.locator('#name').fill('Escalão sintético abrangente'); await page.locator('#birth_date_from').fill('1980-01-01'); await page.locator('#birth_date_to').fill('2015-12-31');
    await post(page, page.getByRole('button', { name: 'Criar escalão', exact: true }), '/equipa/escaloes', 303);
    for (const [person, craft] of [[8100, ['K1']], [8101, ['C1']], [8102, ['K1', 'C1']], [8103, ['K1']], [8104, ['C1']], [8105, ['K1', 'C1']]]) await classify(page, person, craft, value.season);
    await addAthletes(page, value.groups[0], [8100, 8101, 8102], value.season); await addAthletes(page, value.groups[1], [8103, 8104, 8105], value.season);
    value.profiles.kayak = await createProfile(page, value.groups[0], 'Kayak comum aprovado M11', 'KAYAK', input.plans[0].guidance.kayak);
    value.profiles.youngCanoe = await createProfile(page, value.groups[0], 'Canoa mais novos aprovada M11', 'CANOE', input.plans[0].guidance.canoe);
    value.profiles.olderCanoe = await createProfile(page, value.groups[1], 'Canoa mais velhos aprovada M11', 'CANOE', input.plans[1].guidance.canoe);
    for (const [index, plan] of input.plans.entries()) value.plans.push(await createWeek(page, value.groups[index], `${input.header.microcycleLabel} · ${plan.observedGroupLabel}`, input.replayWeek[0], notesFor(plan)));
    const configured = await facts('real-plan-configured');
    expect(configured.profiles).toHaveLength(3); expect(configured.zones).toHaveLength(27); expect(configured.crafts).toHaveLength(8); expect(configured.sports).toHaveLength(6);
    for (const [key, expected] of [['kayak', input.plans[0].guidance.kayak], ['youngCanoe', input.plans[0].guidance.canoe], ['olderCanoe', input.plans[1].guidance.canoe]]) {
      for (const [code, literalValue] of Object.entries(expected)) {
        const zone = configured.zones.find(row => row.profile_id === value.profiles[key] && row.code === code); expect(zone.meaning).toBe(literalValue);
        if (!literalValue.includes('–')) { expect(zone.cadence_min).toBeNull(); expect(zone.cadence_max).toBeNull(); }
      }
    }
    value.memberships = Object.fromEntries(configured.memberships.filter(m => m.user_id.startsWith('39140400')).map(m => [m.user_id, m.id]));
    value.setupCompleted = Date.now(); writeState(value); await planner(page, value.groups[0], value.plans[0]); await screenshot(page, 'real-plan-setup320');
    fs.writeFileSync(`${evidence}/real-plan-setup-timing.json`, JSON.stringify({ milliseconds: value.setupCompleted - started, operator: 'automated UI acceptance agent, not coach-observed usability', inventedSLA: false }));
  } finally { await context.close(); }
});

async function addEffort(page, prefix, entry) {
  const activity = prefix.match(/^activities\[([^\]]+)\]/)[1], phase = prefix.match(/\.phases\[([^\]]+)\]/)[1];
  await command(page, `water_effort_add:${activity}:${phase}:_root`);
  const name = await editor(page).locator(`input[name^="${prefix}.water.nodes["][name$=".name"]`).last().getAttribute('name'); const node = name.replace(/\.name$/, '');
  await setField(page, `${node}.name`, entry.explicitIntensity ? `Trabalho ${entry.explicitIntensity} — literal fornecido` : 'Trabalho pelas palavras fornecidas');
  await setField(page, `${node}.instructions`, literal(entry));
  if (entry.explicitIntensity) await setField(page, `${node}.intensity_code`, entry.explicitIntensity);
  // Effort durations/recoveries remain unknown; no parser invents semantics from shorthand.
}
async function saveEntry(page, value, index, entry, { profile = value.profiles.kayak } = {}) {
  const started = Date.now(), planID = value.plans[index], group = value.groups[index];
  await planner(page, group, planID, entry.replayDate);
  const link = page.locator(`#week-day-${entry.replayDate}[data-selected="true"] a.week-day__add[href="${root}/semanas/${planID}/treinos/criar?day=${entry.replayDate}"]`).filter({ hasText: /treino/i }); await expect(link).toHaveCount(1); await link.click();
  await setField(page, 'title', titleFor(entry)); await setField(page, 'description', literal(entry));
  await setField(page, 'entry_kind', entry.sourceKind === 'REST' ? 'REST' : 'TRAINING');
  if (entry.sourceKind === 'REST') { await command(page, 'refresh'); await setField(page, 'schedule.mode', 'BAND'); await command(page, 'refresh'); await setField(page, 'schedule.band', entry.sourceBand); }
  else {
    await setField(page, 'schedule.start_time', entry.sourceStartLocal); await setField(page, 'activities[a_1].modality', entry.sourceKind);
    await setField(page, 'activities[a_1].phases[p_2].instructions', literal(entry));
    if (entry.sourceKind === 'WATER') {
      const prefix = 'activities[a_1].phases[p_2]'; await setField(page, `${prefix}.detail_mode`, 'WATER'); await command(page, 'refresh');
      await setField(page, `${prefix}.water.method`, 'CUSTOM'); await expect(editor(page).locator(`[name="${prefix}.water.profile_id"]`)).toHaveValue('');
      await setField(page, `${prefix}.water.profile_id`, profile);
      if (entry.explicitDistanceTargetMetres) { await setField(page, `${prefix}.water.target_distance_metres`, String(entry.explicitDistanceTargetMetres)); await setField(page, `${prefix}.water.target_distance_certainty`, 'EXACT'); }
      await addEffort(page, prefix, entry);
    }
  }
  await post(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/prever');
  const proposal = JSON.parse(await saveForm(page).locator('[name="proposal"]').inputValue()).proposal;
  if (entry.sourceKind === 'REST') { expect(proposal.schedule.precision).toBe('BAND'); expect(proposal.schedule.starts_at).toBeNull(); }
  await post(page, saveForm(page).getByRole('button', { name: 'Guardar rascunho', exact: true }), '/confirmar', 303);
  const after = await facts(`entry-${entry.id}-saved`), stored = after.plans[planID].sessions.find(s => s.id === proposal.session_id);
  expect(stored.description.replaceAll('\r\n', '\n')).toBe(literal(entry)); expect(stored.entry_kind).toBe(entry.sourceKind === 'REST' ? 'REST' : 'TRAINING');
  expect(stored.scheduled_on).toBe(entry.replayDate); expect(stored.schedule_precision).toBe(entry.sourcePrecision); expect(stored.ends_at).toBeNull(); expect(stored.local_end).toBeNull();
  if (entry.sourceKind === 'REST') { expect(stored.starts_at).toBeNull(); expect(stored.day_band).toBe(entry.sourceBand); expect(stored.segments).toEqual([]); }
  else { expect(stored.local_start).toBe(`${entry.replayDate} ${entry.sourceStartLocal}`); expect(stored.segments).toHaveLength(1); expect(stored.segments[0].modality).toBe(entry.sourceKind); }
  const blocks = stored.segments.flatMap(s => s.blocks);
  if (entry.sourceKind !== 'REST') {
    expect(blocks).toHaveLength(1); expect(blocks[0].instructions.replaceAll('\r\n', '\n')).toBe(literal(entry));
    expect(stored.segments[0].planned_duration_minutes).toBeNull();
  }
  if (entry.sourceKind === 'WATER') {
    expect(blocks[0].water_prescription.intensity_profile_id).toBe(profile || null); expect(blocks[0].water_steps).toHaveLength(1);
    const step = blocks[0].water_steps[0]; expect(step.instructions.replaceAll('\r\n', '\n')).toBe(literal(entry));
    expect(step.intensity_code).toBe(entry.explicitIntensity || null); expect(step.duration_seconds).toBeNull(); expect(step.recovery_seconds).toBeNull();
    if (entry.explicitDistanceTargetMetres) { expect(blocks[0].water_prescription.target_distance_metres).toBe(entry.explicitDistanceTargetMetres); expect(blocks[0].water_prescription.target_distance_certainty).toBe('EXACT'); }
  }
  value.entries[entry.id] = { session: stored.id, blocks: blocks.map(b => b.id), plan: planID, group, source: entry, elapsedMilliseconds: Date.now() - started };
  fs.writeFileSync(`${evidence}/entry-${entry.id}-proof.json`, JSON.stringify({ proposal, actualStored: stored, source: entry }, null, 2)); writeState(value); return stored;
}
async function reviewPublication(page, group, plan, day = input.replayWeek[0], status = 200, summary = 'Revisão explícita da folha autorizada') {
  await planner(page, group, plan, day); await page.getByRole('link', { name: 'Rever publicação', exact: true }).click();
  await pubForm(page, 'preview').locator('[name="change_summary"]').fill(summary);
  const before = await facts(`publication-preview-${plan}-${status}-before`);
  await post(page, pubForm(page, 'preview').getByRole('button', { name: 'Rever destinatários', exact: true }), '/publicar', status);
  expect(await facts(`publication-preview-${plan}-${status}-after`)).toEqual(before);
  if (status !== 200) { await expect(pubForm(page, 'confirm')).toHaveCount(0); await expect(page.locator('main')).toContainText(/referência|embarcação|ritmo/i); }
}
async function adapt(page, value, index, entry, person, profile, toggle = false) {
  await planner(page, value.groups[index], value.plans[index], entry.replayDate, value.entries[entry.id].session);
  const session = value.entries[entry.id].session;
  const links = await page.locator('a[href]').evaluateAll(elements => elements.map(el => el.getAttribute('href')).filter(href => { const u = new URL(href, location.origin); return u.searchParams.get('task') === 'variation' && u.hash === '#training-variations'; }));
  const href = links.find(href => new URL(href, 'http://fixture').searchParams.get('session_id') === session); expect(href).toBeDefined(); await page.locator(`a[href="${href}"]`).click();
  const form = page.locator(`form[action="${root}/variacoes"]`); await reveal(form.locator('[name="target"]'));
  await form.locator('[name="target"]').selectOption(`ATHLETE:${value.memberships[id(person)]}`); await form.locator('[name="subject"]').selectOption(`BLOCK:${value.entries[entry.id].blocks[0]}`);
  await form.locator('[name="operation"]').selectOption('OVERRIDE'); const selection = form.locator('[name="intensity_profile_id"]'); await expect(selection).toHaveValue('');
  const tokens = await form.locator('input[type="hidden"]').evaluateAll(nodes => nodes.filter(n => /csrf/i.test(n.name)).map(n => ({ name: n.name, value: n.value, disabled: n.disabled })));
  expect(tokens.length).toBeGreaterThan(0); expect(tokens.every(t => t.value && !t.disabled)).toBe(true);
  if (toggle) {
    await form.locator('[name="operation"]').selectOption('OMIT'); await expect(selection).toBeDisabled();
    expect(await form.locator('input[type="hidden"]').evaluateAll(nodes => nodes.filter(n => /csrf/i.test(n.name)).map(n => ({ name: n.name, value: n.value, disabled: n.disabled })))).toEqual(tokens);
    await form.locator('[name="operation"]').selectOption('OVERRIDE'); await expect(selection).toBeEnabled();
  }
  await selection.selectOption(profile); await form.locator('[name="change_summary"]').fill('Mesmas instruções; referência de canoa escolhida explicitamente pelo treinador.');
  await post(page, form.getByRole('button', { name: 'Guardar variação', exact: true }), '/variacoes', 303);
}
function collectIntensities(node, out = []) {
  if (!node || typeof node !== 'object') return out;
  for (const [key, value] of Object.entries(node)) { if (/^Intensity$/i.test(key) && typeof value === 'string') out.push(value); if (value && typeof value === 'object') collectIntensities(value, out); }
  return out;
}
function commonWork(node) {
  if (Array.isArray(node)) return node.map(commonWork);
  if (!node || typeof node !== 'object') return node;
  return Object.fromEntries(Object.entries(node).filter(([key]) => !/^Intensity$|^WaterProfile$|^Changes$|WaterReference|ProfileID|ProfileName|ProfileRevision|ProfileCraft|SelectedProfile|ResolvedProfile/i.test(key)).map(([key, value]) => [key, commonWork(value)]));
}
async function inspectRecipient(page, person, session, expected) {
  const form = pubForm(page, 'inspect'); await form.locator('[name="instruction_key"]').selectOption(`${id(person)}/${session}`);
  await post(page, form.getByRole('button', { name: 'Ver instruções', exact: true }), '/publicar');
  await expect(page.getByRole('region', { name: 'Instruções revistas', exact: true })).toContainText(expected);
}
async function publish(page, value, index, expectedSessions, label) {
  await reviewPublication(page, value.groups[index], value.plans[index]);
  const stats = page.locator('dl[aria-label="Revisão dos destinatários"]');
  await expect(stats.locator('div').filter({ has: page.getByText('Atletas abrangidos', { exact: true }) }).locator('dd')).toHaveText('3');
  await expect(stats.locator('div').filter({ has: page.getByText('Prescrições previstas', { exact: true }) }).locator('dd')).toHaveText(String(expectedSessions * 3));
  const r2 = input.plans[index].entries.find(entry => entry.explicitIntensity === 'R2');
  for (const [person, cadence] of (index === 0 ? [[8100, '70–80'], [8101, '36–42'], [8102, '36–42']] : [[8103, '70–80'], [8104, '40–45'], [8105, '40–45']])) await inspectRecipient(page, person, value.entries[r2.id].session, cadence);
  await screenshot(page, `${label}-resolved-review320`); await post(page, pubForm(page, 'confirm').getByRole('button', { name: 'Publicar revisão', exact: true }), '/publicar', 303, true);
  const result = await facts(`${label}-published`), publication = result.publications.filter(p => p.plan_id === value.plans[index]).sort((a, b) => b.revision - a.revision)[0];
  expect(publication.revision).toBe(1);
  const rows = result.prescriptions.filter(p => p.publication_id === publication.id); expect(rows).toHaveLength(expectedSessions * 3);
  for (const entry of input.plans[index].entries) {
    const persons = rows.filter(row => row.session_id === value.entries[entry.id].session); expect(persons).toHaveLength(3);
    expect(persons.map(p => commonWork(p.snapshot.session))).toEqual([commonWork(persons[0].snapshot.session), commonWork(persons[0].snapshot.session), commonWork(persons[0].snapshot.session)]);
    for (const row of persons) for (const line of entry.observedLines) expect(JSON.stringify(row.snapshot)).toContain(line);
    if (entry.explicitIntensity === 'R2') for (const row of persons) expect(collectIntensities(row.snapshot).join('\n')).toContain(row.athlete_user_id === id(index === 0 ? 8100 : 8103) ? '70–80' : index === 0 ? '36–42' : '40–45');
  }
  value.publications ||= {}; value.publications[index] = publication.id; writeState(value); return result;
}

test('all33 literal reference entries are saved reviewed for explicit K and C guidance and deliberately published', async ({ browser }) => {
  test.setTimeout(1_200_000); const started = Date.now(), value = state();
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }), page = await login(context);
  try {
    await facts('real-plans-before-authoring');
    for (const [index, plan] of input.plans.entries()) {
      const unpublishedBaseline = await facts(`real-plan-${index + 1}-draft-baseline`);
      for (const entry of plan.entries) {
        await saveEntry(page, value, index, entry);

      }
      for (const entry of plan.entries.filter(e => e.sourceKind === 'WATER')) {
        const canoes = index === 0 ? [8101, 8102] : [8104, 8105], profile = value.profiles[index === 0 ? 'youngCanoe' : 'olderCanoe'];
        for (const person of canoes) await adapt(page, value, index, entry, person, profile, entry.explicitIntensity === 'R2' && person === canoes[0]);
      }
      const stored = await facts(`real-plan-${index + 1}-all-saved`);
      expect(stored.publications).toEqual(unpublishedBaseline.publications); expect(stored.prescriptions).toEqual(unpublishedBaseline.prescriptions);
      expect(stored.plans[value.plans[index]].sessions).toHaveLength(plan.entries.length);
      expect(stored.plans[value.plans[index]].season_id).toBe(value.season);
      for (const original of plan.entries.filter(e => e.sourceKind === 'WATER')) {
        const current = stored.plans[value.plans[index]].sessions.find(s => s.id === value.entries[original.id].session);
        expect(current.segments[0].blocks[0].water_prescription.intensity_profile_id).toBe(value.profiles.kayak);
      }
      expect(stored.plans[value.plans[index]].description.replace(/\r\n/g, '\n')).toBe(notesFor(plan)); expect(stored.plans[value.plans[index]].planned_load_percentage).toBeNull();
      await publish(page, value, index, plan.entries.length, `real-plan-${index + 1}`);
    }
    const final = await facts('real-plans-both-published');
    for (const [key, expected] of [['kayak', input.plans[0].guidance.kayak], ['youngCanoe', input.plans[0].guidance.canoe], ['olderCanoe', input.plans[1].guidance.canoe]]) {
      expect(final.zones.filter(z => z.profile_id === value.profiles[key])).toHaveLength(9);
      for (const [code, approved] of Object.entries(expected)) {
        const zone = final.zones.find(z => z.profile_id === value.profiles[key] && z.code === code); expect(zone.meaning).toBe(approved);
        const bounds = approved.includes('–') ? approved.split('–').map(Number) : [null, null]; expect([zone.cadence_min, zone.cadence_max]).toEqual(bounds);
      }
    }
    const mapped = input.plans.flatMap(plan => plan.entries.map(entry => ({ entry: entry.id, sourceDate: entry.sourceDay, replayDate: entry.replayDate, session: value.entries[entry.id].session, observedLines: entry.observedLines, precision: entry.sourcePrecision, publication: value.publications[input.plans.indexOf(plan)], actualUI: true, publishedRecipients: 3 })));
    expect(mapped).toHaveLength(33); expect(new Set(mapped.map(e => e.session)).size).toBe(33);
    value.authoringCompleted = Date.now(); writeState(value);
    fs.writeFileSync(`${evidence}/real-plan-entry-acceptance.json`, JSON.stringify({ mapped, sourceInput: 'e2e/real-plan-input.json', actualClubSeasonDates: 'unknown/configurable', coachGuidanceApproved: true, directSQLTargetRepair: false, milliseconds: value.authoringCompleted - started, coachObserved: false }, null, 2));
  } finally { await context.close(); }
});

async function copyAndEditWeek(page, value) {
  await planner(page, value.groups[0], value.plans[0]);
  const link = page.locator(`a[href^="${root}/reutilizar"]`).filter({ hasText: 'Copiar semana' }); await expect(link).toHaveCount(1); await link.click();
  const draft = page.locator(`form[action="${root}/reutilizar/rascunho"]`).filter({ has: page.locator('[name="target_group_id"]:not([type="hidden"])') });
  const setTarget = async (name, next) => {
    const field = draft.locator(`[name="${name}"]`); await reveal(field);
    if (await field.evaluate(el => el.tagName === 'SELECT')) await field.selectOption(next); else await field.fill(next);
  };
  await setTarget('target_group_id', value.groups[0]);
  await post(page, draft.getByRole('button', { name: 'Atualizar opções sem perder os dados', exact: true }), '/reutilizar/rascunho');
  await setTarget('season_id', value.season); await setTarget('week_start', dayPlus(input.replayWeek[0], 7));
  await setTarget('week_title', 'Cópia sintética para revisão independente');
  await post(page, draft.getByRole('button', { name: 'Atualizar opções sem perder os dados', exact: true }), '/reutilizar/rascunho');
  await post(page, draft.getByRole('button', { name: 'Pré-visualizar cópia', exact: true }), '/reutilizar/prever');
  const confirm = page.locator(`form[action="${root}/reutilizar/confirmar"]`).filter({ has: page.locator('[name="intent"][value="confirm"]') });
  const proposal = JSON.parse(await confirm.locator('[name="proposal"]').inputValue()).proposal;
  expect(proposal.destinations).toHaveLength(16); const before = await facts('real-copy-before');
  await post(page, confirm.getByRole('button', { name: 'Confirmar cópia em rascunho', exact: true }), '/reutilizar/confirmar', 303);
  const copied = await facts('real-copy-created'); expect(copied.publications).toEqual(before.publications); expect(copied.prescriptions).toEqual(before.prescriptions);
  expect(copied.plans[value.plans[0]]).toEqual(before.plans[value.plans[0]]);
  const plan = proposal.target_plan_id, session = proposal.destinations.find(d => d.graph.entry_kind === 'TRAINING')?.graph || proposal.destinations[0].graph;
  await page.goto(`${root}/semanas/${plan}/treinos/${session.session_id}/editar`); const title = await editor(page).locator('[name="title"]').inputValue();
  await editor(page).locator('[name="title"]').fill(`${title} · cópia revista`);
  await post(page, editor(page).getByRole('button', { name: 'Pré-visualizar treino', exact: true }), '/editar/prever');
  await post(page, saveForm(page).getByRole('button', { name: 'Guardar alterações', exact: true }), '/editar/confirmar', 303);
  const edited = await facts('real-copy-one-title-edited');
  expect(edited.publications).toEqual(before.publications); expect(edited.prescriptions).toEqual(before.prescriptions); expect(edited.plans[value.plans[0]]).toEqual(before.plans[value.plans[0]]);
  expect(edited.plans[plan].sessions).toHaveLength(16);
  for (const old of copied.plans[plan].sessions) {
    const current = edited.plans[plan].sessions.find(s => s.id === old.id);
    if (old.id !== session.session_id) expect(current).toEqual(old);
    else {
      expect(current.title).toBe(`${old.title} · cópia revista`);
      // Saving the actual graph touches descendant audit timestamps; every content field and identity stays exact.
      const content = item => Array.isArray(item) ? item.map(content) : item && typeof item === 'object' ? Object.fromEntries(Object.entries(item).filter(([key]) => key !== 'updated_at').map(([key, child]) => [key, content(child)])) : item;
      for (const key of ['description', 'starts_at', 'ends_at', 'schedule_precision', 'scheduled_on', 'day_band', 'segments']) expect(content(current[key])).toEqual(content(old[key]));
    }
  }
  fs.writeFileSync(`${evidence}/real-week-copy-edit-proof.json`, JSON.stringify({ proposal, targetPlan: plan, editedSession: session.session_id, sourceUnchanged: true, targetRemainsDraft: true }, null, 2));
}
async function directRead(browser, email, prescription, status, expected = '', forbidden = '') {
  const context = await browser.newContext({ viewport: { width: 320, height: 1000 } }), page = await login(context, email);
  try {
    const response = await page.goto(`/treinos/prescricoes/${prescription}`); expect(response.status()).toBe(status);
    if (expected) await expect(page.locator('main')).toContainText(expected); if (forbidden) await expect(page.locator('body')).not.toContainText(forbidden);
  } finally { await context.close(); }
}
async function temporaryMove(page, value, destination, referenceRows) {
  const person = 8102, date = today(), end = dayPlus(date, 1), after = dayPlus(date, 2), before = await facts('real-temp-before');
  const original = livePlacements(before, person).find(p => p.group_id === value.groups[0]); expect(original).toBeDefined();
  const fields = new URLSearchParams({ group_id: value.groups[0], person_id: id(person), season_id: value.season, effective_on: date });
  await page.goto(`${root}/colocacoes?${fields}`); const form = page.locator(`form[action="${root}/colocacoes/prever"]`);
  await form.locator('[name="kind"]').selectOption('TEMPORARY'); await form.locator('[name="source_placement_id"]').selectOption(original.id);
  await form.locator('[name="target_group_id"]').selectOption(destination); await form.locator('[name="temporary_until"]').fill(end);
  await post(page, form.getByRole('button', { name: 'Rever alteração', exact: true }), '/colocacoes/prever');
  await expect(page.getByRole('region', { name: 'Depois', exact: true })).toContainText(after);
  await expect(page.getByRole('region', { name: 'Depois', exact: true })).toContainText('Destino temporário de aceitação');
  expect(await facts('real-temp-preview-no-write')).toEqual(before);
  await directRead(page.context().browser(), 'real-plan-one-both@example.test', referenceRows[0].id, 200);
  await screenshot(page, 'real-temp-reviewed320');
  await post(page, page.locator(`form[action="${root}/colocacoes/confirmar"]`).getByRole('button', { name: 'Confirmar alteração', exact: true }), '/colocacoes/confirmar', 303, true);
  const moved = await facts('real-temp-effective');
  const legs = livePlacements(moved, person); expect(legs.find(p => p.group_id === destination)).toMatchObject({ starts_on: date, ends_on: end });
  expect(legs.find(p => p.group_id === value.groups[0] && p.id !== original.id)).toMatchObject({ starts_on: after, ends_on: null });
  for (const row of referenceRows) await directRead(page.context().browser(), 'real-plan-one-both@example.test', row.id, 404);
  return { original, legs, date, end, after, moved };
}

test('real-plan week reuse current-effective temporary withdrawal explicit destination and return publication enforce scoped access', async ({ browser }) => {
  test.setTimeout(900_000); const value = state(), context = await browser.newContext({ viewport: { width: 320, height: 1000 } }), page = await login(context);
  try {
    await facts('real-cross-journey-before');
    await copyAndEditWeek(page, value);
    const published = await facts('real-cross-before-temp'), old = ownRows(published, 8102).filter(p => p.publication_id === value.publications[0]); expect(old).toHaveLength(16);
    const childR2 = ownRows(published, 8100).find(p => p.session_id === value.entries['plan-1-appointment-02'].session);
    const olderR2 = ownRows(published, 8103).find(p => p.session_id === value.entries['plan-2-appointment-03'].session);
    await directRead(browser, 'real-plan-guardian@example.test', childR2.id, 200, '70–80');
    await directRead(browser, 'real-plan-guardian@example.test', olderR2.id, 404);
    await directRead(browser, 'real-plan-suspended@example.test', childR2.id, 404);
    await directRead(browser, 'real-plan-revoked@example.test', childR2.id, 404);
    await directRead(browser, 'real-plan-two-k@example.test', olderR2.id, 200, '70–80');
    await directRead(browser, 'real-plan-unrelated@example.test', childR2.id, 404);
    const deniedContext = await browser.newContext(), revoked = await login(deniedContext, 'real-plan-revoked@example.test');
    try { const response = await revoked.goto(`${root}/grupos/${value.groups[0]}`); expect([403, 404]).toContain(response.status()); await expect(revoked.locator('body')).not.toContainText('Referência um K menor sintética'); } finally { await deniedContext.close(); }
    const destination = await createGroup(page, 'Destino temporário de aceitação');
    const moved = await temporaryMove(page, value, destination, old);
    // Separate future supplemental workout inside B's temporary date range; never part of the33 references.
    const weekday = new Date(`${today()}T12:00Z`).getUTCDay(), monday = dayPlus(today(), weekday === 0 ? -6 : 1 - weekday);
    const supplementaryPlan = await createWeek(page, destination, 'Suplemento sintético para a mudança temporária', monday, 'Caso suplementar; não pertence às33 entradas das folhas.');
    const supplementary = { id: 'supplementary-temporary-B', sourceKind: 'WATER', sourcePrecision: 'START_ONLY', sourceStartLocal: '16:00', sourceBand: null, replayDate: moved.end, observedLines: ['Trabalho R2 sintético suplementar, fora das folhas de referência.'], explicitIntensity: 'R2' };
    const extra = { ...value, groups: [destination], plans: [supplementaryPlan], entries: value.entries };
    await saveEntry(page, extra, 0, supplementary); value.entries = extra.entries;
    await adapt(page, extra, 0, supplementary, 8102, value.profiles.youngCanoe);
    const destinationDraft = await facts('real-temp-destination-still-draft');
    expect(destinationDraft.publications).toEqual(moved.moved.publications); expect(destinationDraft.prescriptions).toEqual(moved.moved.prescriptions);
    await reviewPublication(page, destination, supplementaryPlan, moved.end);
    await inspectRecipient(page, 8102, extra.entries[supplementary.id].session, '36–42');
    await post(page, pubForm(page, 'confirm').getByRole('button', { name: 'Publicar revisão', exact: true }), '/publicar', 303);
    const destinationPublished = await facts('real-temp-destination-explicit-published');
    const rowB = ownRows(destinationPublished, 8102).find(p => p.session_id === extra.entries[supplementary.id].session); expect(rowB).toBeDefined();
    await directRead(browser, 'real-plan-one-both@example.test', rowB.id, 200, '36–42');
    // A2 is a new actual-session-date placement; explicit future publication is allowed now.
    await reviewPublication(page, value.groups[0], value.plans[0]);
    const stats = page.locator('dl[aria-label="Revisão dos destinatários"]');
    await expect(stats.locator('div').filter({ has: page.getByText('Alteradas', { exact: true }) }).locator('dd')).toHaveText('16');
    await expect(stats.locator('div').filter({ has: page.getByText('Sem alterações', { exact: true }) }).locator('dd')).toHaveText('32');
    await post(page, pubForm(page, 'confirm').getByRole('button', { name: 'Publicar revisão', exact: true }), '/publicar', 303);
    const returned = await facts('real-temp-A2-explicit-published');
    const latestPub = returned.publications.filter(p => p.plan_id === value.plans[0]).sort((a, b) => b.revision - a.revision)[0];
    for (const previous of old) {
      const replacement = ownRows(returned, 8102).find(p => p.publication_id === latestPub.id && p.session_id === previous.session_id);
      expect(replacement.placement_id).not.toBe(previous.placement_id); expect(replacement.membership_id).toBe(previous.membership_id); expect(replacement.snapshot_sha256).toBe(previous.snapshot_sha256); expect(replacement.snapshot).toEqual(previous.snapshot);
    }
    await directRead(browser, 'real-plan-one-both@example.test', old[0].id, 404);
    const newRow = ownRows(returned, 8102).find(p => p.publication_id === latestPub.id && p.session_id === value.entries['plan-1-appointment-02'].session);
    await directRead(browser, 'real-plan-one-both@example.test', newRow.id, 200, '36–42');
    expect(await facts('real-private-reads-no-write')).toEqual(returned);
    await planner(page, value.groups[0], value.plans[0]); await page.emulateMedia({ forcedColors: 'active' });
    expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(true);
    const focus = page.getByRole('link', { name: 'Rever publicação', exact: true }); await focus.focus(); await page.keyboard.press('Tab'); await page.keyboard.press('Shift+Tab'); await expect(focus).toBeFocused();
    expect(await focus.evaluate(el => getComputedStyle(el).outlineStyle)).not.toBe('none'); await screenshot(page, 'real-plan-forced-colours320'); await page.emulateMedia({ forcedColors: 'none' });
    value.temporaryMove = { ...moved, destination, supplementaryPlan, supplementarySession: extra.entries[supplementary.id].session, originalPrescriptionIDs: old.map(p => p.id), returnPublication: latestPub.id, actualClockTodayObserved: true, naturalMidnightObserved: false };
    writeState(value);
    fs.writeFileSync(`${evidence}/real-plan-cross-journey-acceptance.json`, JSON.stringify({ currentEffectiveTemporaryMove: value.temporaryMove, copiedWeek: true, editedOneCopiedTitle: true, historyPreservationRequired: false, guardianOnlyOwnMinor: true, manualScreenReaderObserved: false, coachObservedUsability: false, naturalMidnightObserved: false }, null, 2));
  } finally { await context.close(); }
});

test('genuine browser zoom via temporary Chromium extension keeps the real published week usable', async () => {
  test.setTimeout(120_000); const value = state(), extension = `${evidence}/zoom-extension`, profile = `${evidence}/zoom-browser-profile`;
  fs.mkdirSync(extension); fs.writeFileSync(`${extension}/manifest.json`, JSON.stringify({ manifest_version: 3, name: 'Synthetic acceptance browser zoom', version: '1.0', permissions: ['tabs'], host_permissions: [`${process.env.E2E_BASE_URL}/*`], background: { service_worker: 'worker.js' } }));
  fs.writeFileSync(`${extension}/worker.js`, 'chrome.runtime.onInstalled.addListener(() => {});');
  const context = await chromium.launchPersistentContext(profile, { channel: 'chromium', headless: true, viewport: null, deviceScaleFactor: undefined, isMobile: undefined, args: ['--window-size=1280,1000', `--disable-extensions-except=${extension}`, `--load-extension=${extension}`] });
  try {
    const page = await login(context); await planner(page, value.groups[0], value.plans[0]);
    const worker = context.serviceWorkers()[0] || await context.waitForEvent('serviceworker');
    const target = page.url(), originalCSSWidth = await page.evaluate(() => innerWidth);
    const observed = await worker.evaluate(async target => {
      const { chrome } = globalThis; const tabs = await chrome.tabs.query({}); const tab = tabs.find(t => t.url === target); if (!tab) throw new Error('actual app tab not found');
      const before = await chrome.tabs.getZoom(tab.id); await chrome.tabs.setZoom(tab.id, 2); const after = await chrome.tabs.getZoom(tab.id); return { before, after, tabURL: tab.url, tabID: tab.id };
    }, target);
    expect(observed.after).toBe(2); expect(observed.before).toBe(1);
    await expect.poll(() => page.evaluate(() => innerWidth)).toBeLessThanOrEqual(Math.floor(originalCSSWidth / 2) + 1);
    expect(await page.evaluate(() => document.documentElement.style.zoom)).toBe('');
    await screenshot(page, 'real-plan-genuine-browser-zoom200', { fullPage: false });
    const link = page.getByRole('link', { name: 'Rever publicação', exact: true }); await link.focus(); await expect(link).toBeFocused(); await page.keyboard.press('Enter');
    await expect(pubForm(page, 'preview').getByRole('button', { name: 'Rever destinatários', exact: true })).toBeVisible();
    fs.writeFileSync(`${evidence}/real-browser-zoom-proof.json`, JSON.stringify({ ...observed, actualCSSViewport: await page.evaluate(() => innerWidth), method: 'Chrome extension chrome.tabs.setZoom; no CSS zoom/scale/device emulation', observed: true }, null, 2));
  } finally { await context.close(); }
});
