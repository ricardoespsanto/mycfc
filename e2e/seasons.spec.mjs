import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
import { URLSearchParams } from 'node:url';

test.skip(process.env.E2E_SEASONS !== '1', 'Use the isolated season runner.');
const phase=process.env.E2E_SEASONS_PHASE;
const evidence=process.env.E2E_SEASONS_EVIDENCE;
const root='/admin/epocas';
const id=n=>`39400000-0000-0000-0000-${String(n).padStart(12,'0')}`;
const fixture=()=>JSON.parse(fs.readFileSync(`${evidence}/fixture.json`,'utf8'));
const selection=(season,context={})=>`${root}/${season}/selecionar?${new URLSearchParams(context)}`;
async function login(page,actor='admin'){
  await page.goto('/login');await page.getByLabel('Correio eletrónico ou identificador CFC').fill(`seasons-${actor}@example.test`);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');await page.getByRole('button',{name:'Iniciar sessão'}).click();await expect(page).toHaveURL(/\/today$/);
}
async function submit(page,button,status=303){
  const [response]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'),button.click()]);
  expect(response.status()).toBe(status);await page.waitForLoadState('networkidle');return response;
}
async function audit(page,label){
  const result=await new AxeBuilder({page}).analyze();fs.writeFileSync(`${evidence}/axe-${label}.json`,JSON.stringify(result,null,2));
  expect(result.violations.filter(v=>['serious','critical'].includes(v.impact))).toEqual([]);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=globalThis.innerWidth)).toBe(true);
  await page.screenshot({path:`${evidence}/${label}.png`,fullPage:true});
}
async function fillSeason(page,{code='SYN_A',name='Época sintética A',start=fixture().starts_on,end=fixture().ends_on}={}){
  await page.locator('#season-code').fill(code);await page.locator('#season-name').fill(name);
  await page.locator('#season-start').fill(start);await page.locator('#season-end').fill(end);
}
async function preview(page,season,context={}){
  await page.goto(selection(season,context));await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),200);
  await expect(page.getByRole('heading',{name:'Rever antes de confirmar'})).toBeVisible();
}
async function confirm(page,status=303){return submit(page,page.getByRole('button',{name:'Confirmar época atual',exact:true}),status);}
async function nativeToken(page,token){
  await page.locator('form[data-task-form]').evaluate((form,value)=>{
    let input=form.querySelector('[name="preview_token"]');if(!input){input=document.createElement('input');input.type='hidden';input.name='preview_token';form.append(input);}input.value=value;
    let yes=form.querySelector('input[name="confirm"]');if(!yes){yes=document.createElement('input');yes.type='hidden';yes.name='confirm';yes.value='yes';form.append(yes);}
  },token);
}
async function recover(page){
  await expect(page.getByRole('button',{name:'Confirmar época atual',exact:true})).toHaveCount(0);
  const link=page.getByRole('link',{name:'Recarregar e rever a seleção',exact:true}).last();await expect(link).toBeVisible();await link.click();
  await expect(page.getByRole('button',{name:'Pré-visualizar alteração',exact:true})).toBeVisible();await expect(page.locator('[name="preview_token"]')).toHaveCount(0);
}

test('empty database member reads preserve account actions and do not provision a season',async({browser})=>{
  test.skip(phase!=='empty');const context=await browser.newContext({viewport:{width:320,height:900}});const page=await context.newPage();await login(page);
  for(let i=0;i<3;i++){
    await page.goto(`/admin/membros/${id(11)}?return_to=${encodeURIComponent('/admin/membros?page=2&q=Pessoa')}`);
    await expect(page.getByText('Sem época atual',{exact:true})).toBeVisible();
    await expect(page.getByRole('link',{name:'Abrir perfil',exact:true})).toBeVisible();
    await page.getByText('Desativar conta',{exact:true}).click();await expect(page.getByRole('button',{name:'Desativar conta de Pessoa épocas sintética',exact:true})).toBeEnabled();
  }
  const setup=page.getByRole('link',{name:'Configurar épocas',exact:true});const href=await setup.getAttribute('href');
  expect(new URL(href,'http://fixture.test').searchParams.get('return_page')).toBe('2');
  expect(new URL(href,'http://fixture.test').searchParams.get('return_q')).toBe('Pessoa');
  await setup.click();await expect(page.getByText('Ainda sem épocas',{exact:true})).toBeVisible();await audit(page,'empty-list-320');
  await page.goto(`/equipa/classificacao/${id(11)}`);await expect(page.getByRole('link',{name:'Configurar épocas',exact:true})).toHaveAttribute('href',new RegExp(`return_id=${id(11)}`));
  await page.goto('/equipa/escaloes');await expect(page.getByRole('link',{name:'Configurar épocas',exact:true})).toHaveAttribute('href',/return_kind=definitions/);
  await context.close();
});

test('native no-JavaScript creation supplies explicit dates and remains noncurrent',async({browser})=>{
  test.skip(phase!=='create');const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:320,height:900}});const page=await context.newPage();await login(page);await page.goto(root+'/criar');
  await expect(page.locator('#season-start')).toHaveValue('');await expect(page.locator('#season-end')).toHaveValue('');
  await fillSeason(page,{start:fixture().ends_on,end:fixture().starts_on});
  await submit(page,page.getByRole('button',{name:'Criar época',exact:true}),422);
  await expect(page.locator('#season-end')).toHaveAttribute('aria-invalid','true');await expect(page.locator('#season-start')).toHaveAttribute('aria-invalid','false');
  await expect(page.getByRole('alert').first()).toBeFocused();await expect(page.locator('#season-code')).toHaveValue('SYN_A');
  await fillSeason(page);await page.getByRole('button',{name:'Criar época',exact:true}).focus();
  const [response]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'),page.keyboard.press('Enter')]);expect(response.status()).toBe(303);await page.waitForLoadState('networkidle');
  await expect(page.getByText('Não existe uma época atual.',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Pré-visualizar alteração',exact:true})).toBeVisible();
  fs.writeFileSync(`${evidence}/created-season.json`,JSON.stringify({season_a:new URL(page.url()).pathname.split('/').at(-2)}));await context.close();
});

test('real signed preview is captured without selecting a season for natural expiry',async({browser})=>{
  test.skip(phase!=='capture');const context=await browser.newContext();const page=await context.newPage();await login(page);await preview(page,fixture().season_a);
  const token=await page.locator('[name="preview_token"]').inputValue();const expires=Number(token.split('.')[2]);
  expect(expires).toBeGreaterThan(Date.now()/1000+890);
  fs.writeFileSync(`${evidence}/expiry-preview.json`,JSON.stringify({token,expires,season:fixture().season_a,captured_at:Date.now()}));
  await context.storageState({path:`${evidence}/expiry-storage.json`});await context.close();
});

test('preview requires explicit confirmation and list identifies the actual current season',async({browser})=>{
  test.skip(phase!=='select');const context=await browser.newContext({javaScriptEnabled:false});const page=await context.newPage();await login(page);await preview(page,fixture().season_a);
  await expect(page.getByText(/não copia escalões ou atletas/)).toBeVisible();await confirm(page);
  await expect(page).toHaveURL(/\/admin\/epocas$/);await expect(page.getByText('Atual',{exact:true})).toHaveCount(1);
  await expect(page.getByText('Época sintética A',{exact:true})).toBeVisible();await context.close();
});

test('invalid missing dates and duplicate codes retain values; equal leap endpoints are accepted',async({page})=>{
  test.skip(phase!=='validation');await login(page);await page.goto(root+'/criar');await fillSeason(page,{code:'SYN_INVALID',name:'Valores conservados',start:'',end:''});
  await submit(page,page.getByRole('button',{name:'Criar época',exact:true}),422);
  for(const field of ['#season-start','#season-end'])await expect(page.locator(field)).toHaveAttribute('aria-invalid','true');
  await expect(page.locator('#season-name')).toHaveValue('Valores conservados');await audit(page,'missing-dates');
  await fillSeason(page,{code:'SYN_INVALID'});
  await page.locator('#season-start').evaluate(el=>{el.type='text';el.value='2026-02-30';});
  await submit(page,page.getByRole('button',{name:'Criar época',exact:true}),422);await expect(page.locator('#season-start')).toHaveAttribute('aria-invalid','true');
  await fillSeason(page);await submit(page,page.getByRole('button',{name:'Criar época',exact:true}),422);await expect(page.locator('#season-code')).toHaveAttribute('aria-invalid','true');await expect(page.getByText('Já existe uma época com este código.',{exact:true}).last()).toBeVisible();
  await fillSeason(page,{code:'SYN_B',name:'Época sintética B',start:'2024-02-29',end:'2024-02-29'});await submit(page,page.getByRole('button',{name:'Criar época',exact:true}));
  await expect(page.getByText('2024-02-29',{exact:true})).toHaveCount(2);await page.goto(root+'/criar');
  await fillSeason(page,{code:'SYN_C',name:'Época sintética C'});await submit(page,page.getByRole('button',{name:'Criar época',exact:true}));
});

test('concurrent previews require fresh review and replay cannot restore a previous selection',async({browser})=>{
  test.skip(phase!=='stale');const context=await browser.newContext();const page=await context.newPage();await login(page);const old=await context.newPage();
  await preview(page,fixture().season_b);const saved=await page.locator('[name="preview_token"]').inputValue();await preview(old,fixture().season_c);await confirm(page);
  await confirm(old,409);await expect(old.getByRole('alert').first()).toBeFocused();await audit(old,'stale-selection');await recover(old);
  await submit(old,old.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),200);await confirm(old);
  await page.goto(selection(fixture().season_b));await nativeToken(page,saved);await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),409);await recover(page);
  await context.close();
});

test('tampered preview and typed return context fail safely then allow review',async({page})=>{
  test.skip(phase!=='tamper');await login(page);await preview(page,fixture().season_a);
  const token=await page.locator('[name="preview_token"]').inputValue();await page.locator('[name="preview_token"]').evaluate((el,value)=>{el.value=value+'x';},token);
  await confirm(page,422);await audit(page,'tampered-token');await recover(page);
  await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),200);
  await page.locator('[name="return_kind"]').evaluate(el=>{el.value='definitions';});await confirm(page,422);await recover(page);
});

test('selection returns to exact authorized member classification and group-week contexts',async({page})=>{
  test.skip(phase!=='returns');await login(page);
  for(const kind of ['member','classification','group','definitions']){
    const params={return_kind:kind};if(['member','classification'].includes(kind))params.return_id=id(11);
    if(kind==='member'){params.return_page='2';params.return_q='Pessoa';}
    if(kind==='group'){params.return_id=id(200);params.week_id=id(300);}
    await preview(page,fixture().season_a,params);await confirm(page);
    const url=new URL(page.url());
    if(kind==='member'){expect(url.pathname).toBe(`/admin/membros/${id(11)}`);expect(url.searchParams.get('return_to')).toBe('/admin/membros?page=2&q=Pessoa');}
    if(kind==='classification')expect(url.pathname).toBe(`/equipa/classificacao/${id(11)}`);
    if(kind==='group'){expect(url.pathname).toBe('/admin/treinos/estruturados');expect(url.searchParams.get('group_id')).toBe(id(200));expect(url.searchParams.get('week_id')).toBe(id(300));}
    if(kind==='definitions')expect(url.pathname).toBe('/equipa/escaloes');
  }
  await page.goto(selection(fixture().season_a,{return_kind:'group',return_id:id(200),week_id:id(999)}));
  await expect(page.locator('[name="week_id"]')).toHaveCount(0);await expect(page.getByRole('link',{name:'Cancelar',exact:true})).toHaveAttribute('href',`/admin/treinos/estruturados/grupos/${id(200)}`);
});

test('coach cannot submit a retained administrator form or read season details',async({browser})=>{
  test.skip(phase!=='denied');const context=await browser.newContext();const old=await context.newPage();await login(old);await old.goto(root+'/criar');await fillSeason(old,{code:'SYN_COACH',name:'Criação negada'});
  await context.clearCookies();const coach=await context.newPage();await login(coach,'coach');await submit(old,old.getByRole('button',{name:'Criar época',exact:true}),403);
  const response=await coach.goto(selection(fixture().season_a));expect(response.status()).toBe(403);expect(response.headers()['cache-control']).toContain('no-store');expect(response.headers()['referrer-policy']).toBe('no-referrer');
  await expect(coach.getByText('Época sintética A',{exact:true})).toHaveCount(0);await context.close();
});

test('genuine cross-origin authenticated season creation is rejected',async({page})=>{
  test.skip(phase!=='csrf');await login(page);await page.goto(fixture().attack_origin);
  const response=await submit(page,page.getByRole('button',{name:'Submit cross-origin form',exact:true}),403);const headers=await response.request().allHeaders();
  expect(headers.origin).toBe(fixture().attack_origin);expect(headers['sec-fetch-site']).toBe('same-site');expect(headers.cookie).toContain('mycfc_session=');
  expect(response.headers()['cache-control']).toContain('no-store');expect(response.headers()['referrer-policy']).toBe('no-referrer');
});

test('shared season setup remains usable when structured planning is disabled',async({page})=>{
  test.skip(phase!=='disabled');await login(page);expect((await page.goto(root)).status()).toBe(200);
  await expect(page.getByText('Atual',{exact:true})).toHaveCount(1);expect((await page.goto('/admin/treinos/estruturados')).status()).toBe(404);
  await page.goto(`/admin/membros/${id(11)}`);await expect(page.getByRole('heading',{name:'Pessoa épocas sintética',exact:true})).toBeVisible();
  await page.goto(`/equipa/classificacao/${id(11)}`);await expect(page.getByRole('heading',{level:1})).toBeVisible();
});

test('mobile desktop keyboard focus and serious-critical axe checks cover season task states',async({browser})=>{
  test.skip(phase!=='accessibility');for(const width of [320,1280]){const context=await browser.newContext({viewport:{width,height:900}});const page=await context.newPage();await login(page);
    await page.goto(root);await audit(page,`list-${width}`);await page.goto(root+'/criar');await audit(page,`create-${width}`);
    await page.locator('#season-code').focus();await page.keyboard.type('Teclado');await page.keyboard.press('Tab');await expect(page.locator('#season-name')).toBeFocused();
    await submit(page,page.getByRole('button',{name:'Criar época',exact:true}),422);await expect(page.getByRole('alert').first()).toBeFocused();await audit(page,`validation-${width}`);
    await page.goto(selection(fixture().season_a));await audit(page,`select-${width}`);await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),200);await audit(page,`preview-${width}`);
    await context.close();}
});

test('unaltered real preview expires naturally and recovers through fresh review',async({browser})=>{
  test.skip(phase!=='expiry');const captured=JSON.parse(fs.readFileSync(`${evidence}/expiry-preview.json`,'utf8'));
  expect(Date.now()).toBeGreaterThan(captured.expires*1000);
  const context=await browser.newContext({storageState:`${evidence}/expiry-storage.json`});const page=await context.newPage();
  await page.goto(selection(captured.season));await nativeToken(page,captured.token);
  await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),422);
  await expect(page.locator('[name="preview_token"]')).toHaveValue(captured.token);await audit(page,'expired-preview');await recover(page);
  await submit(page,page.getByRole('button',{name:'Pré-visualizar alteração',exact:true}),200);await expect(page.getByRole('button',{name:'Confirmar época atual',exact:true})).toBeVisible();
  await context.close();
});
