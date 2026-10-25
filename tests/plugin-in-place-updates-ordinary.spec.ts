import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

import {
  HOME_PLUGIN,
  NEW_VERSION,
  OLD_VERSION,
  seed,
  writeListedRelease,
  writePlainPlugin
} from './fixtures/in-place-upgrade.mjs';

// The in-place Home upgrade must leave every other Update path as it was:
// ordinary updates with and without a component change, the already-current
// reply, and the Workspace Directory plugin list's switch, which stays refused
// for a plugin a Home uses. Real isolated server, Ori-owned packages:
//
//   ./scripts/e2e-in-place-updates.sh tests/plugin-in-place-updates-ordinary.spec.ts

const sandbox = process.env.ORI_IN_PLACE_UPGRADE_SANDBOX || '';
const fixture = join(sandbox, 'fixture');
const PLAIN = 'in-place-plain-plugin';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function version(request: APIRequestContext, name: string) {
  const { plugins } = await ok(await request.get('/api/plugins'));
  return plugins.find((plugin: { name: string }) => plugin.name === name)?.version;
}

function upgradeRequests(page: Page) {
  const seen: string[] = [];
  page.on('request', item => {
    if (item.url().includes('/home-upgrade')) seen.push(`${item.method()} ${item.url()}`);
  });
  return seen;
}

test.describe.configure({ mode: 'serial' });

test.describe('ordinary plugin updates beside the in-place Home upgrade', () => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX');

  test('a plugin no Home uses keeps the ordinary update, trust review and already-current reply', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    // A fresh sandbox would otherwise open onboarding over the page.
    await ok(await request.post('/api/onboarding/skip'));
    const source = await writePlainPlugin(fixture, PLAIN, '1.0.0', ['in-place-plain-notes']);
    await ok(
      await request.post('/api/plugins/install', {
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
        data: { source, format: 'claude', confirm: true }
      })
    );
    await ok(
      await request.post(`/api/plugins/${PLAIN}/enable`, {
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
    );
    const upgrades = upgradeRequests(page);
    await page.goto('/plugins');
    const update = page.locator(`[data-plugin-action="update"][data-plugin-name="${PLAIN}"]`);
    await expect(update).toBeVisible({ timeout: 30_000 });

    // A new skill is a component change: the ordinary trust review shows it.
    await writePlainPlugin(fixture, PLAIN, '1.1.0', ['in-place-plain-notes', 'in-place-plain-tasks']);
    await update.click();
    const trust = page.locator('#pluginUpdateModal');
    await expect(trust).toBeVisible({ timeout: 30_000 });
    await expect(page.locator('#pluginUpdateModalLabel')).toHaveText(`Update ${PLAIN}`);
    await expect(page.locator('#pluginUpdateTrustBody')).toContainText('in-place-plain-tasks');
    await expect(page.locator('dialog.home-package-upgrade')).toHaveCount(0);
    await page.locator('#pluginUpdateConfirm').click();
    await expect(trust).toBeHidden({ timeout: 30_000 });
    await expect(page.locator('.toast', { hasText: `Updated ${PLAIN} to 1.1.0` })).toBeVisible();
    expect(await version(request, PLAIN)).toBe('1.1.0');

    // The same components at a new version update without a review.
    await writePlainPlugin(fixture, PLAIN, '1.2.0', ['in-place-plain-notes', 'in-place-plain-tasks']);
    await update.click();
    await expect(page.locator('.toast', { hasText: `Updated ${PLAIN} to 1.2.0` })).toBeVisible({
      timeout: 30_000
    });
    await expect(trust).toBeHidden();
    expect(await version(request, PLAIN)).toBe('1.2.0');

    // Nothing newer: the ordinary already-current reply.
    await update.click();
    await expect(page.locator('.toast', { hasText: `${PLAIN} is already up to date` })).toBeVisible(
      { timeout: 30_000 }
    );
    expect(await version(request, PLAIN)).toBe('1.2.0');
    expect(upgrades).toEqual([]);
    expect(new URL(page.url()).pathname).toBe('/plugins');
  });

  test('a list switch of a plugin a Home uses stays refused, and is not turned into an update from another source', async ({
    page,
    request,
    baseURL
  }) => {
    test.setTimeout(180_000);
    await seed({ baseURL, sandbox: fixture });
    // The plugin list names the newer release from a different source.
    const listedSource = await writeListedRelease(fixture, NEW_VERSION);
    const listPath = await findPluginList(sandbox);
    const list = JSON.parse(await readFile(listPath, 'utf8'));
    const entry = list.plugins.find((plugin: { name: string }) => plugin.name === HOME_PLUGIN);
    expect(entry, JSON.stringify(list)).toBeTruthy();
    Object.assign(entry, { source: listedSource, version: NEW_VERSION });
    await writeFile(listPath, JSON.stringify(list, null, 2), { mode: 0o600 });

    const upgrades = upgradeRequests(page);
    await page.goto('/plugins');
    const row = page.locator(`[data-list-row="switch"][data-list-name="${HOME_PLUGIN}"]`);
    await expect(row).toContainText(`Update ${HOME_PLUGIN} from ${OLD_VERSION} to ${NEW_VERSION}`, {
      timeout: 30_000
    });
    await row.locator('[data-list-action="switch"]').click();
    await expect(
      page.locator('.toast', {
        hasText: `${HOME_PLUGIN} cannot switch to the plugin list's version while a Home uses it. Nothing was changed.`
      })
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.locator('dialog.home-package-upgrade')).toHaveCount(0);
    await expect(page.locator('#pluginUpdateModal')).toBeHidden();
    expect(new URL(page.url()).pathname).toBe('/plugins');
    expect(await version(request, HOME_PLUGIN)).toBe(OLD_VERSION);
    // No Home upgrade was reviewed or committed in its place.
    expect(upgrades.filter(item => /home-upgrade\/(review|commit)$/.test(item))).toEqual([]);
  });
});

// findPluginList finds the Workspace Directory's Plugins.json inside the
// sandbox the server runs in.
async function findPluginList(root: string): Promise<string> {
  const entries = (await readdir(root, { recursive: true })) as string[];
  const found = entries.find(
    entry => entry.endsWith('Plugins.json') && !entry.includes('in-place-upgrade')
  );
  expect(found, 'Plugins.json in the sandbox').toBeTruthy();
  return join(root, found!);
}
