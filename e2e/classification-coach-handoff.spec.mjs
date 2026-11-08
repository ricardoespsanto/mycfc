import {expect,test} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
test.skip(process.env.E2E_CLASSIFICATION_338 !== '1','Requires isolated classification preview fixtures.');
const date=(offset=0)=>{const d=new Date();d.setDate(d.getDate()+offset);return new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Lisbon'}).format(d);};
async function login(page,email){
 await page.goto('/login');
 await page.getByLabel('Correio eletrónico ou identificador CFC').fill(email);
 await page.getByLabel('Palavra-passe').fill('correct horse 7');
 await page.getByRole('button',{name:'Iniciar sessão'}).click();
 await expect(page).toHaveURL(/\/today$/);
}
async function propose(page,subject,programme){
 await page.goto(`/equipa/classificacao/${subject}`);
 const scope=await page.locator('#scope option').filter({hasText:programme}).getAttribute('value');
 const previous=await page.locator('#previous_id option').nth(1).getAttribute('value');
 await page.locator('#scope').selectOption(scope);
 await page.locator('#starts_on').fill(date(1));
 await page.locator('#previous_id').selectOption(previous);
 await page.getByRole('button',{name:'Guardar participação',exact:true}).click();
 return {scope,previous};
}
test('dual-grant coach sees administrator handoff; same-programme coach and admin save',async({browser})=>{
 const coachContext=await browser.newContext();
 try {
  const coach=await coachContext.newPage();await login(coach,'dual-coach@example.test');
  const {scope,previous}=await propose(coach,'33800000-0000-0000-0000-000000000073','Iniciação');
  await expect(coach.locator('#error-summary')).toBeFocused();
  await expect(coach.locator('#error-summary')).toContainText('Peça a um administrador');
  await expect(coach.locator('#scope')).toHaveValue(scope);
  await expect(coach.locator('#previous_id')).toHaveValue(previous);
  await expect(coach.locator('#starts_on')).toHaveValue(date(1));
  await expect(coach.getByRole('button',{name:'Confirmar participação'})).toHaveCount(0);
  await expect(coach.getByRole('region',{name:'Histórico de participação'}).locator('li')).toHaveCount(1);
  const violations=(await new AxeBuilder({page:coach}).analyze()).violations.filter(v=>['serious','critical'].includes(v.impact));
  expect(violations).toEqual([]);
  await propose(coach,'33800000-0000-0000-0000-000000000074','Lazer');
  await expect(coach.locator('#preview-heading')).toBeFocused();
  await coach.getByRole('button',{name:'Confirmar participação',exact:true}).click();
  await expect(coach.getByRole('status')).toContainText('Guardada: Leisure');
 } finally {await coachContext.close();}
 const adminContext=await browser.newContext();
 try {
  const admin=await adminContext.newPage();await login(admin,'classification-admin@example.test');
  await propose(admin,'33800000-0000-0000-0000-000000000073','Iniciação');
  await expect(admin.locator('#preview-heading')).toBeFocused();
  await admin.getByRole('button',{name:'Confirmar participação',exact:true}).click();
  await expect(admin.getByRole('status')).toContainText('Guardada: Initiation');
  await expect(admin.getByRole('region',{name:'Histórico de participação'}).locator('li')).toHaveCount(2);
 } finally {await adminContext.close();}
});
