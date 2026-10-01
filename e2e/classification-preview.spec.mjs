import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
test.skip(process.env.E2E_CLASSIFICATION_338 !== '1', 'Requires isolated classification preview fixtures.');
async function login(page) {
 await page.goto('/login');
 await page.getByLabel('Correio eletrónico ou identificador CFC').fill('classification-admin@example.test');
 await page.getByLabel('Palavra-passe').fill('correct horse 7');
 await page.getByRole('button', {name:'Iniciar sessão'}).click();
 await expect(page).toHaveURL(/\/today$/);
}
async function axe(page) {
 const violations=(await new AxeBuilder({page}).analyze()).violations.filter(v=>['serious','critical'].includes(v.impact));
 expect(violations).toEqual([]);
}
const date=(offset=0)=> { const d=new Date();d.setDate(d.getDate()+offset);return new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Lisbon'}).format(d); };
async function transition(page) {
 await page.locator('#scope').selectOption(await page.locator('#scope option').filter({hasText:'Equipa Polo partilhada'}).getAttribute('value'));
 await page.locator('#starts_on').fill(date(1));
 await page.locator('#previous_id').selectOption({index:1});
 await page.getByRole('button',{name:'Guardar participação',exact:true}).click();
 await expect(page.locator('#preview-heading')).toBeFocused();
}

test('dated old/new Polo preview, keyboard forced colours, stale confirmation and retained values',async({page},testInfo)=>{
 await login(page);await page.setViewportSize({width:320,height:720});await page.emulateMedia({forcedColors:'active'});
 const route='/equipa/classificacao/33800000-0000-0000-0000-000000000071';
 await page.goto(route);await transition(page);
 const preview=page.getByRole('region',{name:'Rever antes de confirmar: Atleta pré-visualização'});
 await expect(preview).toContainText('Intervalo anterior');await expect(preview).toContainText('Lazer');
 await expect(preview).toContainText('Intervalo novo');await expect(preview).toContainText('Equipa Polo partilhada (equipa partilhada)');
 await expect(preview).toContainText(date());await expect(preview).toContainText(date(1));await expect(preview).toContainText(`até ${date(30)} (fim da época)`);
 await expect(page.getByRole('region',{name:'Histórico de participação'}).locator('li')).toHaveCount(1);
 await axe(page);expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
 await page.keyboard.press('Tab');await expect(page.locator('#scope')).toBeFocused();
 await page.screenshot({path:testInfo.outputPath('preview-forced-colours-mobile.png'),fullPage:true});
 const other=await page.context().newPage();await other.goto(route);await transition(other);
 await other.getByRole('button',{name:'Confirmar participação',exact:true}).click();
 await expect(other.getByRole('status')).toContainText('Guardada: Kayak_Polo');
 await page.getByRole('button',{name:'Confirmar participação',exact:true}).focus();await page.keyboard.press('Enter');
 await expect(page.locator('#error-summary')).toBeFocused();await expect(page.locator('#error-summary')).toContainText('pré-visualização expirou ou a participação mudou');
 await expect(page.locator('#starts_on')).toHaveValue(date(1));
 await expect(page.locator('#scope option:checked')).toContainText('Kayak Polo');
 await expect(page.getByRole('region',{name:'Histórico de participação'}).locator('li')).toHaveCount(2);
 await expect(page.getByRole('button',{name:'Confirmar participação',exact:true})).toHaveCount(0);
 await axe(page);await other.close();
});

test('stale full-set correction cannot overwrite, retry stays stale, explicit reload and forged token refusal',async({page})=>{
 await login(page);const route='/equipa/classificacao/33800000-0000-0000-0000-000000000072';await page.goto(route);
 const sport=page.locator('form').nth(1);await sport.locator('input[value="CANOEING"]').check();await sport.locator('[name="reason"]').fill('Pedido retido para revisão');
 const original=await sport.locator('[name="original_token"]').inputValue();
 const other=await page.context().newPage();await other.goto(route);
 await other.locator('form').nth(1).locator('input[value="SUP"]').check();await other.locator('#sport-reason').fill('Seleção entretanto confirmada');
 await other.getByRole('button',{name:'Guardar conjunto de modalidades',exact:true}).click();await expect(other.getByText('Modalidade: Stand up paddle')).toBeVisible();
 for(let attempt=0;attempt<2;attempt++) {
  await page.getByRole('button',{name:'Guardar conjunto de modalidades',exact:true}).click();
  await expect(page.locator('#error-summary')).toBeFocused();await expect(page.locator('#error-summary')).toContainText('Recarregue a página');
  await expect(page.locator('#sport-reason')).toHaveValue('Pedido retido para revisão');await expect(page.locator('form').nth(1).locator('input[value="CANOEING"]')).toBeChecked();
  await expect(page.locator('form').nth(1).locator('[name="original_token"]')).toHaveValue(original);
  await expect(page.getByText('Modalidade: Stand up paddle')).toBeVisible();await expect(page.getByText('Modalidade: Canoagem',{exact:true})).toHaveCount(0);
 }
 await axe(page);await page.goto(route);
 await expect(page.locator('form').nth(1).locator('input[value="SUP"]')).toBeChecked();
 await page.locator('form').nth(1).locator('[name="original_token"]').evaluate(el=>el.value+='x');
 await page.locator('#sport-reason').fill('Tentativa com token alterado');await page.getByRole('button',{name:'Guardar conjunto de modalidades',exact:true}).click();
 await expect(page.locator('#error-summary')).toContainText('não é válida');await page.goto(route);await expect(page.locator('form').nth(1).locator('input[value="SUP"]')).toBeChecked();await other.close();
});
