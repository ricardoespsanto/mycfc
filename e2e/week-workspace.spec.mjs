import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
import { URLSearchParams } from 'node:url';

test.skip(process.env.E2E_WEEK_WORKSPACE !== '1','Use isolated week-workspace runner.');
const phase=process.env.E2E_WEEK_WORKSPACE_PHASE;
const evidence=process.env.E2E_WEEK_WORKSPACE_EVIDENCE;
const root='/admin/treinos/estruturados';
const id=n=>`39800000-0000-0000-0000-${String(n).padStart(12,'0')}`;
const fixture=()=>JSON.parse(fs.readFileSync(`${evidence}/fixture.json`,'utf8'));
const url=({group=id(200),week=id(300),day=fixture().days[0],session,...extra}={})=>root+'?'+new URLSearchParams({group_id:group,...(week?{week_id:week}:{}),...(day?{day}:{}),...(session?{session_id:session}:{}),...extra});
const calendar=page=>page.getByRole('region',{name:'Planeamento dos sete dias',exact:true});
const review=page=>page.locator('#training-publication');
async function login(page,actor='coach'){
  await page.goto('/login');await page.getByLabel('Correio eletrónico ou identificador CFC').fill(`week-${actor}@example.test`);
  await page.getByLabel('Palavra-passe').fill('correct horse 7');await page.getByRole('button',{name:'Iniciar sessão'}).click();await expect(page).toHaveURL(/\/today$/);
}
async function submit(page,button,status=303){const [response]=await Promise.all([page.waitForResponse(r=>r.request().method()==='POST'),button.click()]);expect(response.status()).toBe(status);await page.waitForLoadState('networkidle');return response;}
async function audit(page,label){const result=await new AxeBuilder({page}).analyze();fs.writeFileSync(`${evidence}/axe-${label}.json`,JSON.stringify(result,null,2));expect(result.violations.filter(v=>['serious','critical'].includes(v.impact))).toEqual([]);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=globalThis.innerWidth)).toBe(true);await page.screenshot({path:`${evidence}/${label}.png`,fullPage:true});await page.screenshot({path:`${evidence}/${label}-viewport.png`});}
async function exactContext(page,{group=id(200),week=id(300),day=fixture().days[0]}={}){
  await expect(page.locator('#workspace-week')).toHaveValue(week);
  const active=page.locator('.week-workspace__days [aria-current="date"]');
  expect(new URL(await active.getAttribute('href'),'http://fixture.test').searchParams.get('day')).toBe(day);
  await expect(page.locator('.week-workspace__selector [name="group_id"]')).toHaveValue(group);
}
async function openReview(page){await page.getByRole('link',{name:'Rever publicação',exact:true}).click();await expect(review(page)).toBeVisible();}
async function addSession(page,{group=id(200),week=id(300),day=fixture().days[2],title='Sessão sintética adicionada',start='20:15',end='21:05'}={}){
  await page.goto(url({group,week,day,task:'session'})+'#criar-sessao-estruturada');const form=page.locator('#criar-sessao-estruturada form');
  await expect(form.locator('[name="plan_id"]')).toHaveValue(week);await form.locator('[name="title"]').fill(title);
  await form.locator('[name="starts_at"]').fill(day+'T'+start);await form.locator('[name="ends_at"]').fill(day+'T'+end);
  await submit(page,form.getByRole('button',{name:'Criar sessão',exact:true}));return new URL(page.url()).searchParams.get('session_id');
}

test('missing season and empty roster remain explicit without creating guessed dates',async({browser})=>{
  test.skip(phase!=='missing');for(const actor of ['admin','coach']){const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:320,height:900}});const page=await context.newPage();await login(page,actor);
    await page.goto(url({group:id(202),week:'',day:''}));await expect(page.getByText('Ainda sem semanas neste grupo',{exact:true})).toBeVisible();await expect(page.getByText(/Sem destinatários\. Pode preparar/)).toBeVisible();
    await page.getByRole('link',{name:'Criar semana',exact:true}).click();const form=page.locator('#criar-semana-treino form');await expect(form.locator('[name="group_id"]')).toHaveValue(id(202));await expect(form.locator('[name="week_start"]')).toHaveValue('');
    await form.locator('[name="title"]').fill('Semana sem época');await form.locator('[name="week_start"]').fill(fixture().days[0]);await submit(page,form.getByRole('button',{name:'Criar semana',exact:true}),422);
    if(actor==='admin')await expect(page.getByRole('link',{name:'Configurar épocas',exact:true})).toBeVisible();else{await expect(page.getByText(/Peça a um administrador que registe uma época/)).toBeVisible();await expect(page.getByRole('link',{name:'Configurar épocas',exact:true})).toHaveCount(0);}
    await context.close();}
});

test('desktop full week keeps two afternoons Saturday morning rest and cancelled appointments',async({browser})=>{
  test.skip(phase!=='overview');const context=await browser.newContext({viewport:{width:1280,height:1000}});const page=await context.newPage();await login(page);await page.goto(url());
  await expect(calendar(page).locator('.week-day')).toHaveCount(7);for(const day of await calendar(page).locator('.week-day').all())await expect(day).toBeVisible();
  const monday=page.locator('#week-day-'+fixture().days[0]);await expect(monday.locator('.week-day__band').first()).toContainText('Descanso explícito sintético');await expect(monday).toContainText('07:15–07:30');
  await expect(monday.locator('.week-day__band').last().locator('.week-appointment strong')).toHaveText(['Água tarde alfa','Ginásio tarde beta']);
  await expect(page.locator('#week-day-'+fixture().days[5])).toContainText('10:30–11:20');await expect(page.locator('#week-day-'+fixture().days[1])).toContainText('Cancelada');
  await expect(page.locator('#week-day-'+fixture().days[6])).toContainText('Ainda sem planeamento');await expect(page.locator('#week-day-'+fixture().days[6])).not.toContainText('Descanso');
  await audit(page,'overview-1280');await calendar(page).getByRole('link',{name:/Água tarde alfa/}).click();await expect(calendar(page).locator('.week-appointment')).toHaveCount(5);
  await expect(calendar(page).getByRole('link',{name:/Sábado água sintética/})).toBeVisible();await expect(calendar(page).getByRole('link',{name:/Água tarde alfa/})).toHaveAttribute('aria-current','page');await context.close();
});

test('native mobile day and registered week navigation preserve group and full stored shapes',async({browser})=>{
  test.skip(phase!=='mobile');for(const width of [320,1280]){const context=await browser.newContext({javaScriptEnabled:false,viewport:{width,height:1000}});const page=await context.newPage();await login(page);await page.goto(url());
    await expect(calendar(page).locator('.week-day:visible')).toHaveCount(width===320?1:7);
    if(width===320){await expect(page.getByRole('navigation',{name:'Dias da semana'}).getByRole('link')).toHaveCount(7);await page.getByRole('navigation',{name:'Dias da semana'}).locator('a').filter({hasText:fixture().days[5]}).click();}
    else await calendar(page).getByRole('link',{name:/Sábado água sintética/}).click();
    await exactContext(page,{day:fixture().days[5]});await expect(calendar(page).getByRole('link',{name:/Sábado água sintética/})).toBeVisible();
    await page.goBack();await exactContext(page);await page.reload();await exactContext(page);
    await page.getByRole('link',{name:'Semana seguinte',exact:true}).click();await expect(page.locator('#workspace-week')).toHaveValue(id(301));await expect(calendar(page).locator('.week-appointment')).toHaveCount(3);
    await expect(page.locator('.week-workspace__selector [name="group_id"]')).toHaveValue(id(200));
    await page.locator('#workspace-week').selectOption(id(300));await page.locator('.week-workspace__selector').getByRole('button',{name:'Abrir semana',exact:true}).click();await expect(page.locator('#workspace-week')).toHaveValue(id(300));await context.close();}
});

test('empty roster drafts create exact week and session IDs with native save cancel refresh and errors',async({browser})=>{
  test.skip(phase!=='draft');const context=await browser.newContext({javaScriptEnabled:false,viewport:{width:320,height:900}});const page=await context.newPage();await login(page);await page.goto(url({group:id(202),week:'',day:''}));await page.getByRole('link',{name:'Criar semana',exact:true}).click();
  const weekForm=page.locator('#criar-semana-treino form');await weekForm.locator('[name="title"]').fill('Semana vazia criada na interface');await weekForm.locator('[name="week_start"]').fill(fixture().days[0]);
  await weekForm.evaluate(el=>{el.noValidate=true;});await weekForm.locator('[name="planned_load_percentage"]').fill('101');await submit(page,weekForm.getByRole('button',{name:'Criar semana',exact:true}),422);
  await expect(weekForm.locator('[name="planned_load_percentage"]')).toHaveAttribute('aria-invalid','true');await expect(weekForm.locator('[name="planned_load_percentage"]')).toHaveAccessibleDescription(/Indique uma percentagem entre 0 e 100/);await expect(page.getByRole('link',{name:'Indique uma percentagem entre 0 e 100 ou deixe em branco.',exact:true})).toHaveAttribute('href','#structured-week-load');
  await weekForm.locator('[name="planned_load_percentage"]').fill('');await submit(page,weekForm.getByRole('button',{name:'Criar semana',exact:true}));
  const week=new URL(page.url()).searchParams.get('week_id');expect(week).toMatch(/^[a-f0-9-]{36}$/);expect(week).not.toBe(id(300));
  await expect(page.getByText(/Sem destinatários\. Pode preparar/)).toBeVisible();const day=fixture().days[2];await page.goto(url({group:id(202),week,day}));await page.getByRole('link',{name:'Adicionar sessão',exact:true}).click();
  let form=page.locator('#criar-sessao-estruturada form');for(const name of ['starts_at','ends_at'])await expect(form.locator(`[name="${name}"]`)).toHaveValue('');
  await form.getByRole('link',{name:'Cancelar',exact:true}).click();await exactContext(page,{group:id(202),week,day});await page.goBack();form=page.locator('#criar-sessao-estruturada form');await expect(form).toBeVisible();
  await form.evaluate(el=>{el.noValidate=true;});await form.locator('[name="title"]').fill('Título conservado');await submit(page,form.getByRole('button',{name:'Criar sessão',exact:true}),422);
  await exactContext(page,{group:id(202),week,day});await expect(form.locator('[name="title"]')).toHaveValue('Título conservado');await expect(form.locator('[name="starts_at"]')).toHaveAttribute('aria-invalid','true');await expect(page.getByRole('alert').first()).toBeFocused();
  const session=await addSession(page,{group:id(202),week,day});expect(session).toMatch(/^[a-f0-9-]{36}$/);expect(new URL(page.url()).searchParams.get('day')).toBe(day);
  await page.reload();await exactContext(page,{group:id(202),week,day});await expect(calendar(page).getByRole('link',{name:/Sessão sintética adicionada/})).toBeVisible();fs.writeFileSync(`${evidence}/created-draft.json`,JSON.stringify({week,session}));await context.close();
});

test('empty roster drafts also preserve exact destination for an authorized alternate-week post',async({page})=>{
  test.skip(phase!=='draft');await login(page);await page.goto(url({task:'session'}));const form=page.locator('#criar-sessao-estruturada form');
  await expect(form.locator('[name="plan_id"] option')).toHaveCount(2); // placeholder plus canonical selected week
  await form.locator('[name="plan_id"]').evaluate((select,value)=>{const option=document.createElement('option');option.value=value;option.text='Authorized alternate target';select.append(option);select.value=value;},id(301));
  const day=fixture().next_days[3];await form.locator('[name="title"]').fill('Destino autorizado alternativo');await form.locator('[name="starts_at"]').fill(day+'T20:05');await form.locator('[name="ends_at"]').fill(day+'T20:45');
  await submit(page,form.getByRole('button',{name:'Criar sessão',exact:true}));const params=new URL(page.url()).searchParams;
  expect(params.get('group_id')).toBe(id(200));expect(params.get('week_id')).toBe(id(301));expect(params.get('day')).toBe(day);const session=params.get('session_id');expect(session).toMatch(/^[a-f0-9-]{36}$/);
  await exactContext(page,{week:id(301),day});await expect(calendar(page).getByRole('link',{name:/Destino autorizado alternativo/})).toHaveAttribute('aria-current','page');
  fs.writeFileSync(`${evidence}/created-alternate.json`,JSON.stringify({session,week:id(301),day}));
});

test('routine filters retain selected empty day and advanced contextual results remain available',async({page})=>{
  test.skip(phase!=='routines');await login(page);const day=fixture().days[6];await page.goto(url({day}));await page.getByRole('link',{name:'Mais opções de planeamento',exact:true}).click();await expect(page.locator('#workspace-details')).toHaveAttribute('open','');
  await page.locator('#routine-query').fill('Rotina busca');await page.locator('#routine-modality').selectOption('GYM');await page.getByRole('button',{name:'Filtrar rotinas',exact:true}).click();await exactContext(page,{day});
  await expect(page.locator('#training-routines').getByRole('heading',{name:'Rotina busca sintética',exact:true})).toBeVisible();await expect(page.locator('#training-cycles')).toBeVisible();
});

test('real engine publishes only selected week and shows current revision with prospective counts',async({page})=>{
  test.skip(phase!=='publish');await login(page);await page.goto(url());await expect(page.locator('.week-publication')).toContainText('Rascunho · nunca publicado');await expect(page.locator('.week-publication')).toContainText('2 destinatários · 10 prescrições previstas');
  await openReview(page);await expect(review(page).locator('article h3')).toHaveText(['Semana forma A']);const form=review(page).locator(`form[action$="/${id(300)}/publicar"]`);await form.locator('[name="change_summary"]').fill('Publicação sintética verificada');await submit(page,form.getByRole('button'));
  await exactContext(page);await expect(page.locator('.week-publication')).toContainText('Publicado · revisão 1');await audit(page,'published-current');
});

test('published snapshots and outcomes survive navigation and actual draft edit reports changes',async({browser})=>{
  test.skip(phase!=='changes');const context=await browser.newContext();const page=await context.newPage();await login(page);await page.goto(url());await expect(page.locator('.week-publication')).toContainText('Publicado · revisão 1');
  const stale=await context.newPage();await stale.goto(url());await openReview(stale);const form=review(stale).locator(`form[action$="/${id(300)}/publicar"]`);
  // A current revision has no publish mutation; get a real pending draft first.
  const session=await addSession(page,{title:'Alteração real de rascunho'});expect(session).toBeTruthy();await expect(page.locator('.week-publication')).toContainText('Alterações por publicar · última revisão 1');await expect(page.locator('.week-publication')).toContainText('Os atletas continuam a ver a versão publicada');
  await stale.reload();await openReview(stale);await expect(form).toBeVisible();await form.locator('[name="change_summary"]').fill('Revisão obsoleta recusada');
  await addSession(page,{day:fixture().days[3],title:'Segunda alteração real de rascunho',start:'19:10',end:'19:50'});await submit(stale,form.getByRole('button'),409);
  await exactContext(stale);await expect(stale.getByRole('alert').filter({hasText:/plano mudou|plano já foi/})).toBeVisible();await audit(stale,'stale-publication');await context.close();
});

test('forged group week and session combinations never expose another scope',async({page})=>{
  test.skip(phase!=='forged');await login(page);
  for(const path of [url({group:id(201),week:id(302)}),url({week:id(302)})]){const response=await page.goto(path);expect(response.status()).toBe(404);await expect(page.getByText('Conteúdo privado reservado',{exact:true})).toHaveCount(0);}
  for(const session of [id(408),id(405)]){const response=await page.goto(url({session}));expect([200,404]).toContain(response.status());if(response.status()===200){await exactContext(page);await expect(calendar(page).locator('[aria-current="page"]')).toHaveCount(0);await expect(calendar(page)).not.toContainText('Conteúdo privado reservado');await expect(calendar(page)).not.toContainText('Manhã alternativa sintética');}}
  await page.goto(url({day:'not-a-date'}));await expect(page.locator('.week-workspace__selector [name="group_id"]')).toHaveValue(id(200));await expect(calendar(page).locator('.week-day')).toHaveCount(7);
});

test('publication failure is a routed unavailable state with no draft or publish claim',async({browser})=>{
  test.skip(phase!=='unavailable');const context=await browser.newContext({viewport:{width:320,height:900}});const page=await context.newPage();await login(page);await page.goto(url());
  await expect(page.locator('.week-publication')).toContainText('Não foi possível confirmar o estado de publicação');await expect(page.locator('.week-publication')).not.toContainText('Rascunho');await expect(page.locator('.week-publication')).not.toContainText('prescrições previstas');
  await expect(page.locator('.week-publication').getByRole('link',{name:'Voltar a confirmar o estado',exact:true})).toBeVisible();
  await page.getByRole('link',{name:'Mais opções de planeamento',exact:true}).click();await expect(review(page).locator('form')).toHaveCount(0);await audit(page,'unavailable-320');await context.close();
});

test('fresh revoked coach reads and retained native mutations are denied',async({browser})=>{
  test.skip(phase!=='revoked');const context=await browser.newContext();const page=await context.newPage();await login(page,'revoked');
  await page.goto(url({task:'session'}));const form=page.locator('#criar-sessao-estruturada form');await expect(form).toBeVisible();
  await form.locator('[name="title"]').fill('Mutação revogada não guardada');await form.locator('[name="starts_at"]').fill(fixture().days[2]+'T22:10');await form.locator('[name="ends_at"]').fill(fixture().days[2]+'T22:30');
  fs.writeFileSync(`${evidence}/revoke-ready.json`,JSON.stringify({actor:id(3)}));
  await expect.poll(()=>fs.existsSync(`${evidence}/revoked-applied.json`)).toBe(true);expect(JSON.parse(fs.readFileSync(`${evidence}/revoked-applied.json`,'utf8')).ok).toBe(true);
  const rejected=await submit(page,form.getByRole('button',{name:'Criar sessão',exact:true}),403);expect(rejected.headers()['cache-control']).toContain('no-store');expect(rejected.headers()['referrer-policy']).toBe('no-referrer');
  const response=await page.goto(url());expect([403,404]).toContain(response.status());expect(response.headers()['cache-control']).toContain('no-store');expect(response.headers()['referrer-policy']).toBe('no-referrer');await expect(page.getByRole('heading',{name:'Grupo semanal sintético',exact:true})).toHaveCount(0);await context.close();
});

test('feature disabled and real authenticated cross-origin mutation retain privacy protections',async({page})=>{
  test.skip(!['csrf','disabled'].includes(phase));await login(page);
  if(phase==='disabled'){expect((await page.goto(url())).status()).toBe(404);return;}
  await page.goto(fixture().attack_origin);const response=await submit(page,page.getByRole('button',{name:'Submit cross-origin form',exact:true}),403);const headers=await response.request().allHeaders();expect(headers.origin).toBe(fixture().attack_origin);expect(headers['sec-fetch-site']).toBe('same-site');expect(headers.cookie).toContain('mycfc_session=');expect(response.headers()['cache-control']).toContain('no-store');expect(response.headers()['referrer-policy']).toBe('no-referrer');
});

test('keyboard focus validation and axe at 320 and desktop cover actual workspace states',async({browser})=>{
  test.skip(phase!=='accessibility');for(const width of [320,1280]){const context=await browser.newContext({viewport:{width,height:1000}});const page=await context.newPage();await login(page);await page.goto(url());await audit(page,`workspace-${width}`);
    const selector=page.locator('#workspace-week');await selector.focus();await expect(selector).toBeFocused();await page.keyboard.press('Tab');await expect(page.locator('.week-workspace__selector').getByRole('button',{name:'Abrir semana',exact:true})).toBeFocused();
    await page.getByRole('link',{name:'Adicionar sessão',exact:true}).click();await audit(page,`session-task-${width}`);const form=page.locator('#criar-sessao-estruturada form');await form.evaluate(el=>{el.noValidate=true;});await submit(page,form.getByRole('button',{name:'Criar sessão',exact:true}),422);await expect(page.getByRole('alert').first()).toBeFocused();await expect(form.locator('[name="title"]')).toHaveAccessibleDescription('O título deve ter entre 2 e 180 caracteres.');await audit(page,`session-error-${width}`);await context.close();}
});
