import { expect, test, type APIRequestContext } from '@playwright/test';
import { join } from 'node:path';

import {
  EDITED_PROMPT,
  HOME_PLUGIN,
  HOME_PROGRAMS,
  NEW_VERSION,
  OLD_VERSION,
  rolePrompt,
  seed,
  writeHomeRelease
} from './fixtures/in-place-upgrade.mjs';
import { contrastOfText } from './helpers/contrast';

// Update on the Plugins page reviews and applies the reviewed Home package
// upgrade in place, against a real isolated server, the real plugin manager
// and its replacement guard, with Ori-owned disposable packages:
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX \
//     tests/plugin-in-place-updates.spec.ts
//
// The Home-started journey is tests/plugin-in-place-updates-home.spec.ts, so
// each journey gets its own fresh server.

const sandbox = process.env.ORI_IN_PLACE_UPGRADE_SANDBOX || '';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function installedVersion(request: APIRequestContext) {
  const { plugins } = await ok(await request.get('/api/plugins'));
  return plugins.find((plugin: { name: string }) => plugin.name === HOME_PLUGIN)?.version;
}

test.describe('in-place plugin update', () => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX');

  test('Update reviews and applies the Home upgrade without leaving Plugins', async ({
    page,
    request,
    baseURL
  }) => {
    test.setTimeout(180_000);
    const fixture = join(sandbox, 'fixture');
    const seeded = await seed({ baseURL, sandbox: fixture });
    const [north, south] = seeded.homes;
    await writeHomeRelease(fixture, NEW_VERSION);

    await page.goto('/plugins');
    const update = page.locator(`[data-plugin-action="update"][data-plugin-name="${HOME_PLUGIN}"]`);
    await expect(update).toBeVisible({ timeout: 30_000 });
    await update.click();

    // The review opens here, for every Home the plugin backs.
    const dialog = page.locator('dialog.home-package-upgrade');
    const heading = dialog.getByRole('heading', { level: 2 });
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to ${NEW_VERSION}?`, {
      timeout: 30_000
    });
    await expect(dialog).toContainText(
      `2 Homes and their linked projects move together from ${OLD_VERSION} to ${NEW_VERSION}`
    );
    await expect(dialog).toContainText('Adds a Your studio card to this Home.');
    await expect(dialog).toContainText(`${north.name}: 1 linked project (${seeded.song})`);
    await expect(dialog).toContainText(`${south.name}: no linked projects`);
    await expect(dialog).toContainText(`${north.manager} (Studio Manager): gets the new guidance`);
    // Only South Manager's profile was edited: its Home copy still matches the
    // old role prompt, so the two copies are treated, and said, differently.
    await expect(dialog).toContainText(
      `${south.manager} (Label Manager): keeps your edited profile prompt; its Home copy gets the new guidance`
    );
    await expect(page.locator('.toast', { hasText: 'Open Home' })).toHaveCount(0);
    expect(new URL(page.url()).pathname).toBe('/plugins');
    expect(await installedVersion(request)).toBe(OLD_VERSION);

    // The review is read from its heading (Enter cannot confirm it unread).
    await expect(heading).toBeFocused();
    await expect(dialog).toHaveAttribute('aria-labelledby', (await heading.getAttribute('id'))!);
    // It fits a phone without sideways scrolling, and its text stays readable
    // in both themes.
    await page.setViewportSize({ width: 375, height: 740 });
    const fit = await dialog.evaluate(node => ({
      overflow: node.scrollWidth - node.clientWidth,
      right: node.getBoundingClientRect().right
    }));
    expect(fit.overflow).toBeLessThanOrEqual(0);
    expect(fit.right).toBeLessThanOrEqual(375);
    await page.setViewportSize({ width: 1280, height: 800 });
    for (const theme of ['dark', 'light']) {
      await page.evaluate(value => document.documentElement.setAttribute('data-bs-theme', value), theme);
      expect(await dialog.evaluate(contrastOfText), theme).toBeGreaterThanOrEqual(4.5);
    }

    // Escape installs nothing and returns to the row.
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(update).toBeFocused();
    expect(await installedVersion(request)).toBe(OLD_VERSION);

    // Review again, confirm from the keyboard, and stay on the page.
    await update.click();
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to ${NEW_VERSION}?`, {
      timeout: 30_000
    });
    await dialog.getByRole('button', { name: 'Confirm update' }).focus();
    await page.keyboard.press('Enter');
    await expect(heading).toHaveText(`${HOME_PLUGIN} is updated to ${NEW_VERSION}`, {
      timeout: 90_000
    });
    expect(new URL(page.url()).pathname).toBe('/plugins');
    expect(await installedVersion(request)).toBe(NEW_VERSION);
    await expect(dialog).toContainText(`${north.manager} (Studio Manager): got the new guidance`);
    await expect(dialog).toContainText(
      `${south.manager} (Label Manager): kept your edited profile prompt; its Home copy got the new guidance`
    );
    // Both Homes are named, each with its own page; neither is chosen for you.
    await expect(dialog.getByRole('link', { name: `Open ${north.name}` })).toHaveAttribute(
      'href',
      `/workspaces/${north.slug}/assistant`
    );
    await expect(dialog.getByRole('link', { name: `Open ${south.name}` })).toHaveAttribute(
      'href',
      `/workspaces/${south.slug}/assistant`
    );

    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();
    await expect(update).toBeFocused();
    await expect(page.locator('#pluginList > div').filter({ has: update })).toContainText(
      NEW_VERSION
    );
    expect(new URL(page.url()).pathname).toBe('/plugins');

    // What was applied: both Homes on the new release, the song still linked,
    // the unedited prompt replaced and the edited one kept, and the added
    // profile card present but never read.
    for (const home of seeded.homes) {
      const program = await ok(await request.get(`/api/workspaces/${home.id}/assistant-program`));
      expect(program.provider_upgrade?.installed_version, home.name).toBe(NEW_VERSION);
    }
    const song = (await ok(await request.get('/api/workspaces'))).folders.find(
      (folder: { name: string }) => folder.name === seeded.song
    );
    expect(song?.parent_id).toBe(north.id);
    const songRecord = await ok(await request.get(`/api/workspaces/${song.id}`));
    expect(songRecord.assistant_program?.link?.home_provider?.plugin_version).toBe(NEW_VERSION);
    const northManager = await ok(await request.get(`/api/agents/${encodeURIComponent(north.manager)}`));
    expect(northManager.system_prompt).toBe(rolePrompt(HOME_PROGRAMS[0], NEW_VERSION));
    const southManager = await ok(await request.get(`/api/agents/${encodeURIComponent(south.manager)}`));
    expect(southManager.system_prompt).toBe(EDITED_PROMPT);
    const profile = await ok(await request.get(`/api/workspaces/${north.id}/assistant-program/profile`));
    expect(profile).toMatchObject({ available: true, profile: null, revision: 0 });

    // The finished operation is history, not a pending update.
    const status = await ok(await request.get(`/api/plugins/${HOME_PLUGIN}/home-upgrade`));
    expect(status.operation).toBeUndefined();
    expect(status.last_operation).toMatchObject({ status: 'succeeded', to_version: NEW_VERSION });
  });
});
