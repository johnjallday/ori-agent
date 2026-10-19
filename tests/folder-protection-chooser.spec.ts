import { test, expect, type Page } from '@playwright/test';
import { ensurePersonalHQ, skipOnboarding } from './helpers/folder-protection';

// Folder protection M5: "Protect your folders" is reachable from the Today line
// and the assistant's Backup & Recovery readiness card, lists Time Machine
// first on a Mac that can read it, and creates nothing until an option is
// confirmed. Opening it reads only the fixed provider options.
//
// Runs against an isolated real server (`wt demo`, `scripts/demo-server.sh`).
// It never confirms an option, so it creates no workspace.
test.describe.serial('Folder protection: the Protect your folders chooser', () => {
  async function workspaceCount(page: Page) {
    const listed = await (await page.request.get('/api/workspaces')).json();
    return (listed.workspaces || []).length;
  }

  test('the readiness card leads to the chooser while no Backup & Recovery workspace exists', async ({
    page
  }) => {
    await ensurePersonalHQ(page);
    const response = await page.request.get('/api/personal-assistant/capabilities');
    expect(response.status(), await response.text()).toBe(200);
    const cards = (await response.json()).capabilities?.cards || [];
    const card = cards.find(entry => entry.key === 'backup-recovery');
    expect(card, 'no Backup & Recovery readiness card').toBeTruthy();
    expect(card.action_route).toBe('/?protect=folders');
  });

  test('the chooser opens from its link, leads with Time Machine on a Mac, and Cancel creates nothing', async ({
    page
  }) => {
    await skipOnboarding(page);
    const options = (
      await (await page.request.get('/api/personal-assistant/protection-options')).json()
    ).protection_options;
    const before = await workspaceCount(page);
    // Nothing may create a workspace or touch backup state at any point.
    const creations: string[] = [];
    page.on('request', request => {
      const path = new URL(request.url()).pathname;
      if (request.method() !== 'GET' && (path === '/api/workspaces' || path.includes('backup')))
        creations.push(request.method() + ' ' + path);
    });
    const optionsRead = page.waitForRequest(
      request =>
        request.method() === 'GET' &&
        new URL(request.url()).pathname === '/api/personal-assistant/protection-options'
    );

    await page.goto('/?protect=folders');
    await optionsRead;
    const dialog = page.locator('#protectFoldersChooser');
    await expect(dialog).toBeVisible();
    await expect(page).not.toHaveURL(/protect=folders/);
    await expect(dialog.getByRole('heading', { name: 'Protect your folders' })).toBeVisible();
    const titles = await dialog.locator('.br-chooser-card h3').allTextContents();
    if (options.time_machine.platform_supported)
      expect(titles).toEqual([
        'Time Machine (built into this Mac)',
        'A restic repository on an external drive'
      ]);
    else expect(titles).toEqual(['A restic repository on an external drive']);
    const restic = dialog.locator('.br-chooser-card[data-provider="restic"]');
    for (const need of [
      'An external drive with free space',
      'restic 0.19.1, installed by you',
      'An encrypted repository you prepare on that drive',
      'A Vault secret that holds its password',
      'About 30 minutes'
    ])
      await expect(restic.getByText(need)).toBeVisible();
    await expect(dialog).not.toContainText(/Ori backs up|verified/i);

    // Home posts its own load-time receipts and polls; the user action under
    // test is Cancel, which must send nothing at all.
    const mutations: string[] = [];
    page.on('request', request => {
      if (request.method() !== 'GET' && request.url().includes('/api/'))
        mutations.push(request.url());
    });
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toHaveCount(0);
    expect(await workspaceCount(page), 'cancel created a workspace').toBe(before);
    expect(mutations, 'reading or cancelling the chooser sent a mutation').toEqual([]);
    expect(creations, 'the chooser created or changed something').toEqual([]);
  });
});
