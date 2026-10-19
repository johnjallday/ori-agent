import { expect, test } from '@playwright/test';
import { mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Real permission/grant/scan APIs. This complements the existing console's
// fixture-backed focus suite with actual consent and result layout in each mode.
test.describe.configure({ mode: 'serial' });
const run = Date.now().toString(36);
const variants = [
  { name: 'desktop-light', theme: 'light', width: 1280, height: 900, reduced: false },
  { name: 'desktop-dark', theme: 'dark', width: 1280, height: 900, reduced: false },
  { name: 'narrow-reduced', theme: 'light', width: 390, height: 844, reduced: true },
  { name: 'zoom-equivalent-dark', theme: 'dark', width: 640, height: 450, reduced: true }
] as const;

test('folder-choice confirmation advances with focus inside the live setup dialog', async ({
  page,
  request
}) => {
  const root = mkdtempSync(join(tmpdir(), 'ori-janitor-focus-'));
  const created = await request.post('/api/workspaces', {
    data: { name: `Janitor focus ${run}`, template_id: 'file-janitor' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await created.json()).folder;
  // Script only the native choice. Its real HTTP setup/grant and wizard
  // confirmation reproduce the focus loss after the chosen control is replaced.
  await page.route('**/api/folder-picker/select-path', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, selected: true, path: root })
    })
  );
  await page.goto(`/workspaces/${workspace.folder_slug}`);
  await page.locator('#fileJanitorWizardPick').click();
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review what runs on its own');
  await expect(page.locator('#setupWizardTitle')).toBeFocused();
  const status = (await (await request.get(`/api/workspaces/${workspace.id}/file-janitor`)).json())
    .status;
  expect(status.settings.paused).toBe(true);
  expect(status.settings.automation_approved_at || '').toMatch(/^$|^0001-/);
});

for (const variant of variants) {
  test(`real attached consent and result: ${variant.name}`, async ({ page, request }, info) => {
    await page.setViewportSize({ width: variant.width, height: variant.height });
    await page.emulateMedia({
      colorScheme: variant.theme,
      reducedMotion: variant.reduced ? 'reduce' : 'no-preference'
    });
    await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), variant.theme);
    const root = mkdtempSync(join(tmpdir(), 'ori-janitor-long-folder-' + 'folder-'.repeat(12)));
    const file = join(root, 'invoice.pdf');
    writeFileSync(file, 'synthetic matrix bytes', { mode: 0o600 });
    const old = new Date(Date.now() - 3600000);
    utimesSync(file, old, old);
    const created = await request.post('/api/workspaces', {
      data: { name: `Janitor ${variant.name} ${run}`, template_id: 'file-janitor' }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const workspace = (await created.json()).folder;
    const linked = await request.post(`/api/workspaces/${workspace.id}/directories`, {
      data: { name: 'Matrix Inbox', path: root }
    });
    expect(linked.ok(), await linked.text()).toBeTruthy();
    let pickers = 0;
    page.on('request', request => {
      if (request.url().includes('/api/folder-picker/')) pickers++;
    });
    await page.goto(`/workspaces/${workspace.folder_slug}?panel=file-janitor`);
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', variant.theme);
    const dialog = page.locator('#setupWizardDialog[open]');
    const review = dialog.locator('.fj-setup-review');
    await expect(review).toBeVisible();
    await expect(review.getByRole('checkbox')).not.toBeChecked();
    await review.getByText('Folder details', { exact: true }).click();
    await expect(review.locator('.fj-setup-path')).toHaveText(root);
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBeTruthy();
    const box = await dialog.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(variant.width + 1);
    await page.screenshot({ path: info.outputPath('consent.png') });
    const confirm = review.getByRole('button', { name: 'Use attached Matrix Inbox', exact: true });
    await confirm.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
    await expect(dialog).not.toBeVisible();
    const console = page.locator('#fileJanitorConsole');
    expect(await console.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBeTruthy();
    expect(await console.evaluate(el => el.contains(document.activeElement))).toBeTruthy();
    const status = (
      await (await request.get(`/api/workspaces/${workspace.id}/file-janitor`)).json()
    ).status;
    expect(status.settings.setup_decision.mode).toBe('manual');
    expect(status.settings.automation_approved_at || '').toMatch(/^$|^0001-/);
    expect(pickers).toBe(0);
    expect(readFileSync(file, 'utf8')).toBe('synthetic matrix bytes');
    await page.screenshot({ path: info.outputPath('review.png') });
  });
}
