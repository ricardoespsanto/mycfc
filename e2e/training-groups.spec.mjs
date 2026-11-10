import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
import { URLSearchParams } from 'node:url';

test.skip(process.env.E2E_TRAINING_GROUPS !== '1', 'Use the isolated training-groups runner.');
const phase = process.env.E2E_TRAINING_GROUPS_PHASE;
const evidence = process.env.E2E_TRAINING_GROUPS_EVIDENCE;
const root = '/admin/treinos/estruturados/grupos';
const id = n => `39139300-0000-0000-0000-${String(n).padStart(12, '0')}`;
const state = () => JSON.parse(fs.readFileSync(`${evidence}/fixture.json`, 'utf8'));
const groupPath = () => `${root}/${state().group}`;
async function login(page, actor='coach') {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(`groups-${actor}@example.test`);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');
  await page.getByRole('button', {name:'Iniciar sessão'}).click();
  await expect(page).toHaveURL(/\/today$/);
}
async function submit(page, button, status=303) {
  const [response] = await Promise.all([page.waitForResponse(r => r.request().method()==='POST'), button.click()]);
  expect(response.status()).toBe(status);
  await page.waitForLoadState('networkidle');
  return response;
}
async function audit(page, label) {
  const result = await new AxeBuilder({page}).analyze();
  const serious = result.violations.filter(v=>['serious','critical'].includes(v.impact));
  fs.writeFileSync(`${evidence}/axe-${label}.json`, JSON.stringify(result,null,2));
  await page.screenshot({path:`${evidence}/${label}.png`,fullPage:true});
  expect(serious, JSON.stringify(serious)).toEqual([]);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=globalThis.innerWidth)).toBe(true);
}
async function privateNamesAbsent(page) {
  const html=await page.content();
  for(const text of ['Pessoa não procurada','Pesquisa Outra participação','Pesquisa Inativa','athlete-11@example.test','1990-01-01']) expect(html).not.toContain(text);
  await expect(page.locator('main a[href*="/membros/"], main img')).toHaveCount(0);
}
async function hiddenMembers(page, ids) {
  // Adversarial input in the actual rendered native form, submitted through its button.
  // No HTTP client, mocked routes, cookies or injected authentication.
  await page.locator('form[data-task-form]').evaluate((form, values)=>{
    form.querySelectorAll('[name="membership_id"]').forEach(el=>el.remove());
    for(const value of values){const el=document.createElement('input');el.type='hidden';el.name='membership_id';el.value=value;form.append(el);}
  },ids);
}
async function search(page, query='Pesquisa') {
  await page.getByLabel('Nome do atleta', {exact:true}).fill(query);
  await page.getByRole('button',{name:'Pesquisar',exact:true}).click();
}
async function denied(page, path) {
  const response=await page.goto(path);
  expect([403,404]).toContain(response.status());
  await expect(page.getByRole('heading',{name:'Grupo sem época',exact:true})).toHaveCount(0);
}

test('clean configured programme permits an empty group without season or athletes', async ({browser})=>{
  test.skip(phase!=='clean');
  const context=await browser.newContext({viewport:{width:320,height:740}});
  const page=await context.newPage(); await login(page);
  await page.goto(root);
  await expect(page.getByText('Ainda sem grupos de treino', {exact:true})).toBeVisible();
  await audit(page,'empty-320');
  await page.getByRole('link',{name:'Criar grupo',exact:true}).click();
  await expect(page.locator('[name="scope"] option')).toHaveText(['Escolha uma participação','Lazer']);
  await expect(page.locator('[name="season_id"], [name="membership_id"]')).toHaveCount(0);
  await page.getByLabel('Nome do grupo', {exact:false}).fill('Grupo sem época');
  await page.getByLabel('Participação do grupo', {exact:false}).selectOption({label:'Lazer'});
  await audit(page,'create-320');
  await page.getByRole('button',{name:'Criar grupo',exact:true}).focus();
  await expect(page.getByRole('button',{name:'Criar grupo',exact:true})).toBeFocused();
  const [response]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'),page.keyboard.press('Enter')]);
  expect(response.status()).toBe(303); await page.waitForLoadState('networkidle');
  await expect(page.getByRole('heading',{name:'Grupo sem época',exact:true})).toBeVisible();
  await expect(page.getByText('Ainda sem atletas', {exact:true})).toBeVisible();
  await expect(page.getByRole('link',{name:'Criar semana',exact:true})).toBeVisible();
  for(const action of await page.locator('.training-group-header .page-actions .action').all()){const box=await action.boundingBox();expect(box.width).toBeGreaterThanOrEqual(120);expect(box.height).toBeLessThanOrEqual(80);}
  const group=new URL(page.url()).pathname.split('/').pop();
  fs.writeFileSync(`${evidence}/created-group.json`,JSON.stringify({group}));
  await audit(page,'detail-empty-320'); await context.close();
});

test('name-only scoped search adds multiple athletes and roster warns to review publication', async ({browser})=>{
  test.skip(phase!=='roster');
  const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:320,height:740}});
  const page=await context.newPage(); await login(page);
  await page.goto(root+'/criar');
  await page.getByLabel('Nome do grupo',{exact:false}).fill('Grupo criado sem JavaScript');
  await page.getByLabel('Participação do grupo',{exact:false}).selectOption({label:'Lazer'});
  await submit(page,page.getByRole('button',{name:'Criar grupo',exact:true}));
  await expect(page.getByRole('heading',{name:'Grupo criado sem JavaScript',exact:true})).toBeVisible();
  await page.goto(groupPath());
  await page.getByRole('link',{name:'Adicionar atletas',exact:true}).click();
  await expect(page.locator('[name="membership_id"]')).toHaveCount(0);
  await privateNamesAbsent(page);
  await search(page,'%');await expect(page.locator('[name="membership_id"]')).toHaveCount(0);
  await search(page);
  await privateNamesAbsent(page);
  await page.getByRole('checkbox',{name:'Pesquisa Alfa',exact:true}).check();
  await page.getByRole('checkbox',{name:'Pesquisa Beta',exact:true}).check();
  await submit(page,page.getByRole('button',{name:'Adicionar atletas',exact:true}));
  await expect(page).toHaveURL(new RegExp(`${state().group}$`));
  await expect(page.getByText('Pesquisa Alfa',{exact:true})).toBeVisible();
  await expect(page.getByText('Pesquisa Beta',{exact:true})).toBeVisible();
  await expect(page.getByRole('status')).toContainText('2 atletas adicionados');
  await expect(page.getByRole('status')).toContainText('as adições não publicam instruções');
  await page.getByRole('link',{name:'Alterar nome',exact:true}).click();
  await page.getByLabel('Nome do grupo',{exact:false}).fill('Grupo nativo');
  await submit(page,page.getByRole('button',{name:'Guardar nome',exact:true}));
  await expect(page.getByRole('heading',{name:'Grupo nativo',exact:true})).toBeVisible();
  await context.close();
});

test('duplicate and wrong-programme or occupied-group batches do not partially add', async ({page})=>{
  test.skip(phase!=='batches'); await login(page);
  for(const ids of [[id(1019),id(1019)],[id(1019),id(1013)],[id(1019),id(1018)]]) {
    await page.goto(groupPath()+'/atletas?q=Pesquisa'); await hiddenMembers(page,ids);
    await submit(page,page.getByRole('button',{name:'Adicionar atletas',exact:true}),422);
    await expect(page.getByRole('alert').first()).toBeVisible();
  }
  await page.goto(groupPath()); await expect(page.getByText('Pesquisa Lote válido',{exact:true})).toHaveCount(0);
  await page.goto(groupPath()+'/atletas?q=Pesquisa');await hiddenMembers(page,[id(1011),id(1012)]);
  await submit(page,page.getByRole('button',{name:'Adicionar atletas',exact:true}));
  await expect(page.getByRole('status')).toContainText('Nenhuma adição foi necessária');
});

test('stale add and rename preserve input and require review; week context survives rename', async ({browser})=>{
  test.skip(phase!=='stale'); const context=await browser.newContext();
  const page=await context.newPage(); await login(page);
  const stale=await context.newPage(); await stale.goto(groupPath()+'/atletas?q=Pesquisa');
  await stale.getByRole('checkbox',{name:'Pesquisa Lote válido',exact:true}).check();
  const rename=await context.newPage(); await rename.goto(groupPath()+'/nome');
  await rename.getByLabel('Nome do grupo',{exact:false}).fill('Nome retido em conflito');
  await page.goto(groupPath()+'/nome'); await page.getByLabel('Nome do grupo',{exact:false}).fill('Grupo atualizado');
  await submit(page,page.getByRole('button',{name:'Guardar nome',exact:true}));
  await submit(stale,stale.getByRole('button',{name:'Adicionar atletas',exact:true}),409);
  await expect(stale.getByRole('checkbox',{name:'Pesquisa Lote válido',exact:true})).toBeChecked();
  await expect(stale.getByRole('link',{name:'Recarregar e rever o grupo'})).toBeVisible();
  await audit(stale,'stale-add');
  await submit(rename,rename.getByRole('button',{name:'Guardar nome',exact:true}),409);
  await expect(rename.getByLabel('Nome do grupo',{exact:false})).toHaveValue('Nome retido em conflito');
  await audit(rename,'stale-rename'); await context.close();
});

test('create week through planner and preserve exact week while renaming', async ({browser})=>{
  test.skip(phase!=='week'); const context=await browser.newContext(); const page=await context.newPage(); await login(page);
  await page.goto(groupPath()); await page.getByRole('link',{name:'Criar semana',exact:true}).click();
  await expect(page).toHaveURL(new RegExp(`group_id=${state().group}`));
  await page.getByRole('link',{name:'Criar semana',exact:true}).click();
  const form=page.locator('form[action="/admin/treinos/estruturados/semanas"]');
  await form.locator('[name="group_id"]').selectOption(state().group);
  await form.locator('[name="title"]').fill('Semana sintética preservada');
  await form.locator('[name="week_start"]').fill(state().monday);
  await submit(page,form.getByRole('button',{name:'Criar semana',exact:true}));
  await page.goto(groupPath()); const href=await page.getByRole('link',{name:'Abrir semana',exact:true}).getAttribute('href');
  const week=new URL(href,'http://synthetic.test').searchParams.get('week_id'); expect(week).toBeTruthy();
  await page.goto(groupPath()+`?week_id=${week}`); await page.getByRole('link',{name:'Alterar nome',exact:true}).click();
  await expect(page.locator('[name="week_id"]')).toHaveValue(week);
  await page.getByLabel('Nome do grupo',{exact:false}).fill('Grupo com semana preservada');
  await submit(page,page.getByRole('button',{name:'Guardar nome',exact:true}));
  expect(new URL(page.url()).searchParams.get('week_id')).toBe(week);
  await expect(page.getByRole('link',{name:'Abrir semana',exact:true})).toHaveAttribute('href',new RegExp(`week_id=${week}`));
  await page.getByRole('link',{name:'Abrir semana',exact:true}).click(); await expect(page.getByRole('heading',{name:'Semana sintética preservada',exact:true})).toBeVisible();
  await context.close();
});

test('programme grant cannot discover or open exact team or other-programme groups', async ({page})=>{
  test.skip(phase!=='scopes'); await login(page); await page.goto(root);
  for(const name of ['Grupo outra participação','Grupo equipa A','Grupo equipa B']) await expect(page.getByText(name,{exact:true})).toHaveCount(0);
  for(const n of [302,303,304]) for(const suffix of ['','/atletas?q=Pesquisa','/nome']) await denied(page,`${root}/${id(n)}${suffix}`);
});

test('exact team grant searches only its team and rejects a mixed team batch', async ({page})=>{
  test.skip(phase!=='scopes'); await login(page,'team'); await page.goto(`${root}/${id(303)}/atletas?q=Pesquisa`);
  await expect(page.getByRole('checkbox',{name:'Pesquisa Equipa A',exact:true})).toBeVisible();
  await expect(page.getByRole('checkbox',{name:'Pesquisa Equipa B',exact:true})).toHaveCount(0);
  await hiddenMembers(page,[id(1016),id(1017)]); await submit(page,page.getByRole('button',{name:'Adicionar atletas',exact:true}),422);
  await denied(page,`${root}/${id(304)}`); await denied(page,groupPath());
});

test('unscoped and revoked actors are denied without roster disclosure', async ({browser})=>{
  test.skip(phase!=='denied');
  for(const actor of ['unscoped','revoked']){const context=await browser.newContext();const page=await context.newPage();await login(page,actor); await denied(page,groupPath());await privateNamesAbsent(page);await context.close();}
});

test('flag admin-only denies coach and permits administrator; disabled denies both', async ({browser})=>{
  test.skip(!['admin-only','disabled'].includes(phase));
  for(const actor of ['coach','admin']){const context=await browser.newContext();const page=await context.newPage();await login(page,actor);
    if(phase==='admin-only'&&actor==='admin'){expect((await page.goto(root)).status()).toBe(200);await expect(page.getByRole('heading',{name:'Grupos de treino',exact:true})).toBeVisible();}
    else await denied(page,root);
    await context.close();}
});

test('keyboard create and validation, responsive route states, and axe at 320 and desktop', async ({browser})=>{
  test.skip(phase!=='accessibility');
  for(const width of [320,1280]) {const context=await browser.newContext({viewport:{width,height:900}});const page=await context.newPage();await login(page);
    for(const [suffix,label] of [['','roster'],['/atletas','search-empty'],['/atletas?q=Pesquisa','search-results'],['/nome','rename']]){await page.goto(groupPath()+suffix);await audit(page,`${label}-${width}`);}
    await page.goto(root+'/criar'); await audit(page,`create-${width}`);
    const name=page.getByLabel('Nome do grupo',{exact:false});await name.focus();await page.keyboard.type('Grupo teclado');await page.keyboard.press('Tab');await expect(page.locator('[name="scope"]')).toBeFocused();
    // Bypass native constraint validation only to verify server validation and focus handling.
    await page.locator('form[data-task-form]').evaluate(form=>{form.noValidate=true;form.elements.name.value='';});
    await submit(page,page.getByRole('button',{name:'Criar grupo',exact:true}),422);
    await expect(name).toHaveAttribute('aria-invalid','true'); await expect(page.getByRole('alert').first()).toBeVisible();
    await expect(page.getByRole('alert').first()).toBeFocused();
    await audit(page,`validation-${width}`);await context.close();
  }
});

test('native mutation form from a different origin is denied', async ({page})=>{
  test.skip(phase!=='csrf');await login(page);await page.goto(groupPath()+'/nome');
  const version=await page.locator('[name="version"]').inputValue();
  const original=new URL(page.url()).origin;
  await page.goto(state().attack_origin+'/?'+new URLSearchParams({version}));
  expect(new URL(page.url()).origin).not.toBe(original);
  const response=await submit(page,page.getByRole('button',{name:'Submit cross-origin form',exact:true}),403);
  const headers=await response.request().allHeaders();
  expect(headers.origin).toBe(state().attack_origin);
  expect(headers['sec-fetch-site']).toBe('same-site');
  expect(headers.cookie).toContain('mycfc_session=');
  await page.goto(groupPath());await expect(page.getByRole('heading',{name:'Grupo com semana preservada',exact:true})).toBeVisible();
});
