import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const adminEmail = 'e2e-admin@example.test';
const password = 'correct horse 7';

async function loginAsAdministrator(page) {
  await page.goto('/login');
  await page.getByLabel('Correio eletrónico ou identificador CFC').fill(adminEmail);
  await page.getByLabel('Palavra-passe').fill(password);
  await page.getByRole('button', { name: 'Iniciar sessão' }).click();
  await expect(page).toHaveURL(/\/today$/);
}

function photoFeature(page) {
  return page.locator('article').filter({ has: page.getByRole('heading', { name: 'Envio de fotografias para álbuns' }) });
}

async function setPhotoAvailability(page, label, expectedBadge) {
  const feature = photoFeature(page);
  await feature.getByLabel(label, { exact: true }).check();
  await feature.getByRole('button', { name: 'Guardar Envio de fotografias para álbuns' }).click();
  await expect(page).toHaveURL(/\/admin\/sistema$/);
  await expect(page.getByRole('status')).toHaveText('Disponibilidade atualizada.');
  await expect(photoFeature(page).getByLabel(label, { exact: true })).toBeChecked();
  await expect(photoFeature(page).locator('.badge')).toHaveText(expectedBadge);
}

test('administrator controls registered feature availability with an audited accessible form', async ({ page }) => {
  await loginAsAdministrator(page);
  await page.goto('/admin/sistema');

  await expect(page.getByRole('heading', { name: 'Sistema', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Funcionalidades', level: 2 })).toBeVisible();
  await expect(photoFeature(page).getByLabel('Desativada', { exact: true })).toBeChecked();

  try {
    await setPhotoAvailability(page, 'Só administradores', 'Só administradores');
    await setPhotoAvailability(page, 'Ativa', 'Ativa');

    const audit = page.getByRole('list', { name: 'Alterações recentes às funcionalidades' });
    await expect(audit.getByText('Desativada → Só administradores')).toBeVisible();
    await expect(audit.getByText('Só administradores → Ativa')).toBeVisible();
    const violations = (await new AxeBuilder({ page }).analyze()).violations
      .filter(({ impact }) => impact === 'serious' || impact === 'critical');
    expect(violations).toEqual([]);
  } finally {
    await page.goto('/admin/sistema');
    if (await photoFeature(page).getByLabel('Desativada', { exact: true }).isChecked() === false) {
      await setPhotoAvailability(page, 'Desativada', 'Desativada');
    }
  }
});
