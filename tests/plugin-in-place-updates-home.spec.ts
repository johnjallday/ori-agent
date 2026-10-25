import { expect, test, type APIRequestContext } from '@playwright/test';
import { join } from 'node:path';

import {
  HOME_PLUGIN,
  HOME_PROGRAMS,
  NEW_VERSION,
  OLD_VERSION,
  rolePrompt,
  seed,
  writeHomeRelease
} from './fixtures/in-place-upgrade.mjs';
import { contrastOfText } from './helpers/contrast';

// The Home page's entry to the same reviewed upgrade, on its own fresh server
// (the Plugins journey is tests/plugin-in-place-updates.spec.ts):
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX \
//     tests/plugin-in-place-updates-home.spec.ts

const sandbox = process.env.ORI_IN_PLACE_UPGRADE_SANDBOX || '';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

test.describe('Home-started package update', () => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX');

  test('a Home applies the upgrade in place and its panels read again without a reload', async ({
    page,
    request,
    baseURL
  }) => {
    test.setTimeout(180_000);
    const fixture = join(sandbox, 'fixture');
    const seeded = await seed({ baseURL, sandbox: fixture });
    const [north, south] = seeded.homes;
    await writeHomeRelease(fixture, NEW_VERSION);

    // A legacy deep link opens the review and commits nothing.
    await page.goto(`/workspaces/${north.slug}/assistant?upgrade=review`);
    const dialog = page.locator('dialog.home-package-upgrade');
    const heading = dialog.getByRole('heading', { level: 2 });
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to ${NEW_VERSION}?`, {
      timeout: 30_000
    });
    expect(new URL(page.url()).search).toBe('');
    await expect(dialog).toContainText(
      `2 Homes and their linked projects move together from ${OLD_VERSION} to ${NEW_VERSION}`
    );
    // Before the upgrade this Home declares no profile card.
    await expect(page.locator('#homeProfilePanel')).toBeHidden();
    await page.evaluate(() => {
      (window as unknown as { sameDocument: boolean }).sameDocument = true;
    });

    await dialog.getByRole('button', { name: 'Confirm update' }).click();
    await expect(heading).toHaveText(`${HOME_PLUGIN} is updated to ${NEW_VERSION}`, {
      timeout: 90_000
    });
    // The other Home is offered by name; this one is not linked to itself.
    await expect(dialog.getByRole('link', { name: `Open ${south.name}` })).toHaveAttribute(
      'href',
      `/workspaces/${south.slug}/assistant`
    );
    await expect(dialog.getByRole('link', { name: `Open ${north.name}` })).toHaveCount(0);

    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();
    const card = page.locator('#assistantProgramUpgrade');
    await expect(card).toHaveAttribute('data-state', 'succeeded');
    await expect(card).toContainText(`Upgraded to ${NEW_VERSION}`);
    await expect(card).toBeFocused();
    // The card the result returns focus to stays readable in the dark theme
    // the page takes from the saved or system preference.
    await page.evaluate(() => document.documentElement.setAttribute('data-bs-theme', 'dark'));
    // An opaque surface, so the ratio below is the one a reader sees.
    expect(await card.evaluate(node => getComputedStyle(node).backgroundColor)).toMatch(
      /^rgb\(/
    );
    expect(await card.evaluate(contrastOfText)).toBeGreaterThanOrEqual(4.5);
    await page.evaluate(() => document.documentElement.setAttribute('data-bs-theme', 'light'));
    // The profile card the release adds appears on this page, unread.
    await expect(page.locator('#homeProfilePanel')).toBeVisible();
    await expect(page.locator('#homeProfileTitle')).toHaveText('Your studio');
    expect(
      await page.evaluate(() => (window as unknown as { sameDocument?: boolean }).sameDocument)
    ).toBe(true);

    const profile = await ok(await request.get(`/api/workspaces/${north.id}/assistant-program/profile`));
    expect(profile).toMatchObject({ available: true, profile: null, revision: 0 });
    const { plugins } = await ok(await request.get('/api/plugins'));
    expect(plugins.find((plugin: { name: string }) => plugin.name === HOME_PLUGIN)?.version).toBe(
      NEW_VERSION
    );
    const manager = await ok(await request.get(`/api/agents/${encodeURIComponent(north.manager)}`));
    expect(manager.system_prompt).toBe(rolePrompt(HOME_PROGRAMS[0], NEW_VERSION));
  });
});
