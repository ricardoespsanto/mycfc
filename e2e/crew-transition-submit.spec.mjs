import { expect, test } from '@playwright/test';
import { URLSearchParams } from 'node:url';
import { axe, fixture, id, login, postUI, submitTomorrowTransition } from './crew-transition-helpers.mjs';
test.skip(process.env.E2E_CREW_TRANSITION !== '1', 'Disposable crew-transition runner required.');

test('scoped coach creates overlapping C2 K2 K4 with keyboard focus, dates, composition and axe', async ({ page }) => {
  await login(page, 'coach');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/admin/treinos/estruturados#training-variations');
  for (const [craft, members] of [['C2', [111,112]], ['K2', [112,113]], ['K4', [111,112,113,114]]]) {
    const trigger = page.getByRole('button', { name: 'Criar subgrupo ou tripulação' });
    await trigger.focus();
    await page.keyboard.press('Enter');
    const dialog = page.getByRole('dialog', { name: 'Novo alvo de variação' });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('#variation-group-name')).toBeFocused();
    await dialog.locator('#variation-group-base').focus();
    await page.keyboard.press('Tab');
    await expect(dialog.locator('#variation-group-kind')).toBeFocused();
    expect(await dialog.locator('#variation-group-base option').allTextContents()).not.toContain('Grupo fora do âmbito');
    await dialog.locator('#variation-group-base').selectOption(id(200));
    await dialog.locator('#variation-group-kind').selectOption('CREW');
    await dialog.locator('#variation-group-name').fill(`Tripulação ${craft} sobreposta`);
    await dialog.locator('#variation-group-craft').selectOption(craft);
    await dialog.locator('#variation-effective-from').fill(fixture().yesterday);
    await dialog.locator('#variation-effective-until').fill(fixture().tomorrow);
    for (const member of members) {
      const checkbox = dialog.locator(`input[value="${id(member)}"]`);
      await checkbox.focus();
      await page.keyboard.press('Space');
      await expect(checkbox).toBeChecked();
      await expect(checkbox).toBeFocused();
    }
    await axe(page, `${craft} authorized native selection at 390px`);
    await page.screenshot({ path: `${process.env.E2E_CREW_TRANSITION_EVIDENCE}/${craft}-selection.png`, fullPage: true });
    await postUI(page, dialog.getByRole('button', { name: 'Guardar alvo' }), '/variacoes/grupos');
    const record = page.locator('article').filter({ has: page.getByRole('heading', { name: `Tripulação ${craft} sobreposta`, exact: true }) });
    await expect(record).toContainText(craft);
    await expect(record).toContainText('Atleta Beta');
    await expect(record).toContainText(fixture().yesterday.split('-').reverse().join('/'));
    await expect(record).toContainText(fixture().tomorrow.split('-').reverse().join('/'));
    if (craft === 'K4') for (const name of ['Alfa', 'Beta', 'Gama', 'Delta']) await expect(record).toContainText(`Atleta ${name}`);
  }
  await axe(page, 'overlapping persisted crews');
});

test('crew HTTP rejects forged scope, malformed IDs, duplicate IDs, invalid craft and cardinality', async ({ page }) => {
  await login(page, 'coach');
  await page.goto('/admin/treinos/estruturados#training-variations');
  const base = { training_group_id: id(200), kind: 'CREW', name: 'Tentativa recusada', craft_code: 'C2', effective_from: fixture().yesterday, effective_until: fixture().tomorrow };
  const cases = [
    [{ ...base, membership_id: [id(111), id(112)], training_group_id: id(201) },403],
    [{ ...base, membership_id: [id(111), 'invalid-id'] },403],
    [{ ...base, membership_id: [id(111), id(111)] },403],
    [{ ...base, membership_id: [id(111), id(112)], craft_code: 'C4' },403],
    [{ ...base, membership_id: [id(111)] },403],
    [{ ...base, membership_id: [id(111), id(999)] },403],
  ];
  for (const [values,status] of cases) {
    const body = new URLSearchParams();
    for (const [key,value] of Object.entries(values)) for (const item of [].concat(value)) body.append(key,item);
    const response = await page.request.post('/admin/treinos/estruturados/variacoes/grupos', { data: body.toString(), headers: { 'Content-Type': 'application/x-www-form-urlencoded', Origin: new URL(page.url()).origin, 'Sec-Fetch-Site': 'same-origin' }, maxRedirects: 0 });
    expect(response.status(), JSON.stringify(values)).toBe(status);
    expect(await response.text()).toContain(values.training_group_id === id(201) ? 'Acesso recusado' : 'Pedido recusado');
  }
});

test('equal-priority overlapping crew variations block publication until explicitly retired', async ({ page }) => {
  await login(page, 'coach');
  await page.goto('/admin/treinos/estruturados#training-variations');
  for (const craft of ['C2','K2']) {
    await page.getByRole('button', { name: 'Adicionar variação', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Adicionar variação', exact: true });
    await dialog.locator('#variation-plan').selectOption(id(300));
    const target = await dialog.locator('#variation-target option').filter({ hasText: `Tripulação ${craft} sobreposta` }).getAttribute('value');
    await dialog.locator('#variation-target').selectOption(target);
    await dialog.locator('#variation-subject').selectOption(`SEGMENT:${id(500)}`);
    await dialog.locator('#variation-summary').fill(`Alteração ${craft} sintética`);
    await dialog.locator('#variation-title').fill(`Água ${craft}`);
    await postUI(page, dialog.getByRole('button', { name: 'Guardar variação' }), '/variacoes');
  }
  const conflict = page.locator('#training-variations article.notice--warning').filter({ hasText: 'Atleta Beta' });
  await expect(conflict).toHaveCount(1);
  await expect(conflict).toContainText('em conflito');
  await axe(page, 'explicit overlapping crew conflict');
  await page.getByRole('tab', { name: 'Publicar', exact: true }).click();
  await expect(page.locator('#training-publication')).toContainText('conflitos de variação impedem a publicação');
  await expect(page.locator(`form[action$="/semanas/${id(300)}/publicar"]`)).toHaveCount(0);
  await page.getByRole('tab', { name: 'Variações', exact: true }).click();
  const conflictingRule = page.locator('#training-variations article.notice--warning').filter({ hasText: 'Atleta Beta' }).locator('li').filter({ hasText: 'Alteração K2 sintética' });
  await postUI(page, conflictingRule.getByRole('button', { name: 'Retirar' }), '/retirar');
  await expect(page.locator('#training-variations article.notice--warning')).toHaveCount(0);
  await page.getByRole('tab', { name: 'Publicar', exact: true }).click();
  await expect(page.locator(`form[action$="/semanas/${id(300)}/publicar"]`)).toBeVisible();
});

test('administrator submits genuine next-day participation transition without claiming clock advancement', async ({ page }) => {
  await login(page, 'admin');
  await submitTomorrowTransition(page);
  await expect(page.getByRole('region', { name: 'Histórico de participação' })).toContainText('Escalão aceitação');
  await axe(page, 'submitted next-day transition');
});
