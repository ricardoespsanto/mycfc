import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';

test.skip(process.env.E2E_RECIPIENT_PUBLICATION !== '1', 'Use isolated recipient runner.');
const evidence = process.env.E2E_RECIPIENT_EVIDENCE;
const id = n => `39139100-0000-0000-0000-${String(n).padStart(12, '0')}`;
const fixture = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const confirmForm = page => page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="confirm"]') });
const previewForm = page => page.locator('form').filter({ has: page.locator('[name="publication_intent"][value="preview"]') });
const metadataNames = ['source_updated_at', 'review_token', 'reviewed_latest_id', 'reviewed_latest_revision', 'return_group', 'return_week', 'return_session', 'return_day'];
async function metadata(form) { const result = {}; for (const name of metadataNames) result[name] = await form.locator(`[name="${name}"]`).inputValue(); return result; }
async function post(page, button, status, keyboard = false) {
  const response = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/publicar'));
  if (keyboard) { await button.focus(); await expect(button).toBeFocused(); await page.keyboard.press('Enter'); } else await button.click();
  const result = await response; expect(result.status()).toBe(status); await page.waitForLoadState('networkidle'); return result;
}
async function facts(label) {
  fs.writeFileSync(`${evidence}/${label}-ready.json`, '{}');
  await expect.poll(() => fs.existsSync(`${evidence}/${label}-done.json`)).toBe(true);
  return JSON.parse(fs.readFileSync(`${evidence}/${label}-done.json`, 'utf8'));
}
async function inspectLayout(page, name, js) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (js) {
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    fs.writeFileSync(`${evidence}/axe-${name}.json`, JSON.stringify(result));
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  }
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
for (const js of [true, false]) test(`native recipient review and stale recovery ${js ? 'JS' : 'no-JS 320px'}`, async ({ browser }) => {
  const mode = js ? 'js' : 'native', plan = js ? 300 : 301;
  const context = await browser.newContext({ javaScriptEnabled: js, viewport: { width: js ? 1280 : 320, height: 1000 } });
  const page = await context.newPage();
  try {
    await page.goto('/login'); await page.getByLabel('Correio eletrónico ou identificador CFC').fill('recipient-coach@example.test');
    await page.getByLabel('Palavra-passe').fill('correct horse 7'); await page.getByRole('button', { name: 'Iniciar sessão', exact: true }).click(); await expect(page).toHaveURL(/\/today$/);
    const day = fixture().days[plan - 300], action = `/admin/treinos/estruturados/semanas/${id(plan)}/publicar`;
    const planner = `/admin/treinos/estruturados?group_id=${id(200)}&week_id=${id(plan)}&session_id=${id(plan + 100)}&day=${day}`;
    await page.goto(planner); await page.getByRole('link', { name: 'Rever publicação', exact: true }).click();
    let form = page.locator(`form[action="${action}"]`);
    const rawSummary = 'Primeira linha sintética\nSegunda\tlinha preservada';
    await form.locator('[name="change_summary"]').fill(rawSummary);
    const before = (await facts(`${mode}-before`))[id(plan)]; expect(before.publications).toEqual([]);
    // Legacy missing-intent requests must still only preview; submit a real native form.
    await form.locator('[name="publication_intent"]').evaluate(el => el.remove());
    if (!js) {
      await form.locator('[name="change_summary"]').focus(); await page.keyboard.press('Tab');
      await expect(form.getByRole('button', { name: 'Rever destinatários', exact: true })).toBeFocused();
    }
    const preview = await post(page, form.getByRole('button', { name: 'Rever destinatários', exact: true }), 200, !js);
    expect(preview.headers()['cache-control']).toContain('no-store'); expect(preview.headers()['referrer-policy']).toBe('no-referrer');
    await expect(page.locator('.task-form')).toContainText('Destinatário sintético elegível'); await expect(page.locator('.task-form')).not.toContainText('DEPENDENTE_EXCLUIDO_SINTETICO');
    await expect(page.getByRole('heading', { name: 'Destinatários desta revisão', exact: true })).toBeVisible();
    let confirm = confirmForm(page); const original = await metadata(confirm);
    expect(original.reviewed_latest_revision).toBe('0'); expect(original.reviewed_latest_id).toBe(''); expect(original.review_token).not.toBe('');
    expect(original.return_group).toBe(id(200)); expect(original.return_week).toBe(id(plan)); expect(original.return_session).toBe(id(plan + 100)); expect(original.return_day).toBe(day);
    expect((await facts(`${mode}-preview`))[id(plan)]).toEqual(before);
    await inspectLayout(page, `${mode}-preview`, js);
    const committed = await post(page, confirm.getByRole('button', { name: 'Publicar revisão', exact: true }), 303, !js);
    const sent = new URLSearchParams(committed.request().postData()); for (const [key, value] of Object.entries(original)) expect(sent.get(key)).toBe(value);
    expect(sent.get('publication_intent')).toBe('confirm'); expect(sent.get('confirm')).toBe('yes'); expect(sent.get('change_summary')).toBe(rawSummary.replaceAll('\n', '\r\n'));
    await expect(page).toHaveURL(new RegExp(`week_id=${id(plan)}`)); expect(new URL(page.url()).searchParams.get('day')).toBe(day); expect(new URL(page.url()).searchParams.get('session_id')).toBe(id(plan + 100));
    const first = (await facts(`${mode}-first`))[id(plan)]; expect(first.publications).toHaveLength(1); expect(first.prescriptions).toHaveLength(1); expect(first.publications[0].change_summary).toBe(sent.get('change_summary'));
    await page.getByRole('link', { name: 'Rever publicação', exact: true }).click(); form = page.locator(`form[action="${action}"]`); await form.locator('[name="change_summary"]').fill('Resumo de revisão sem alterações');
    await post(page, form.getByRole('button', { name: 'Rever destinatários', exact: true }), 200, !js); await expect(page.locator('.task-form')).toContainText('o resumo introduzido não será guardado');
    await post(page, confirmForm(page).getByRole('button', { name: 'Concluir revisão', exact: true }), 303, !js);
    expect((await facts(`${mode}-noop`))[id(plan)]).toEqual(first);
    await page.getByRole('link', { name: 'Rever publicação', exact: true }).click(); form = page.locator(`form[action="${action}"]`); await form.locator('[name="change_summary"]').fill('Proposta obsoleta\ncom\ttabulação');
    await post(page, form.getByRole('button', { name: 'Rever destinatários', exact: true }), 200, !js); confirm = confirmForm(page); const stale = await metadata(confirm);
    const editor = await context.newPage(); await editor.goto(`/admin/treinos/sessoes/${id(plan + 100)}/editar`);
    const edit = editor.locator(`form[action="/admin/treinos/sessoes/${id(plan + 100)}"]`); await edit.locator('[name="title"]').fill('Título alterado pela interface sintética');
    const savedEdit = editor.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/' + id(plan + 100))); await edit.getByRole('button', { name: 'Guardar alterações', exact: true }).click(); expect((await savedEdit).status()).toBe(303); await editor.close();
    await post(page, confirm.getByRole('button', { name: 'Concluir revisão', exact: true }), 409, !js);
    await expect(page.locator('.error-summary')).toBeFocused(); await expect(page.locator('.task-form')).not.toContainText('Destinatário sintético elegível'); await expect(confirmForm(page)).toHaveCount(0);
    const retry = previewForm(page); for (const [key, value] of Object.entries(stale)) await expect(retry.locator(`[name="${key}"]`)).toHaveValue(value);
    await expect(retry.locator('[name="change_summary"]')).toHaveValue('Proposta obsoleta\ncom\ttabulação'); const unchanged=(await facts(`${mode}-stale`))[id(plan)];expect(unchanged.publications).toEqual(first.publications);expect(unchanged.prescriptions).toEqual(first.prescriptions);
    await inspectLayout(page, `${mode}-stale`, js);
    await post(page, retry.getByRole('button', { name: 'Rever destinatários', exact: true }), 200, !js); expect((await metadata(confirmForm(page))).review_token).not.toBe(stale.review_token);
    await expect(page.locator('.task-form')).toContainText('Destinatário sintético elegível'); await inspectLayout(page, `${mode}-fresh`, js);
    await post(page, confirmForm(page).getByRole('button', { name: 'Publicar revisão', exact: true }), 303, !js);
    const final = (await facts(`${mode}-final`))[id(plan)]; expect(final.publications).toHaveLength(2); expect(final.prescriptions).toHaveLength(2);
    expect(final.publications[0]).toEqual(first.publications[0]); expect(final.prescriptions[0]).toEqual(first.prescriptions[0]); expect(final.publications[1].supersedes_id).toBe(first.publications[0].id);
    const history = await page.request.get(`/treinos/prescricoes/${first.prescriptions[0].id}`); expect(history.status()).toBe(200); expect(await history.text()).toContain('Instruções sintéticas preservadas');
  } finally { await context.close(); }
});
