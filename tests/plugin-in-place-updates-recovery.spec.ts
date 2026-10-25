import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { join } from 'node:path';

import { HOME_PLUGIN, NEW_VERSION, seed, writeHomeRelease } from './fixtures/in-place-upgrade.mjs';

// What the in-place update does when things go wrong, against a real isolated
// server and the Ori-owned fixture. Faults are injected only in this test's
// own browser routing (a dropped answer, a request that never leaves), never
// in the server:
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX \
//     tests/plugin-in-place-updates-recovery.spec.ts
//
// The last test mocks one status answer to show the reconcile-required hold;
// it is presentation only. The server's own recovery of an interrupted
// operation is covered in internal/homeupgrade (TestRecoverySettlesACrashAtEveryStep,
// TestAttemptOfAnExpiredOrInterruptedReview).

const sandbox = process.env.ORI_IN_PLACE_UPGRADE_SANDBOX || '';
const fixture = join(sandbox, 'fixture');
const COMMIT = `**/api/plugins/${HOME_PLUGIN}/home-upgrade/commit`;

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function installedVersion(request: APIRequestContext) {
  const { plugins } = await ok(await request.get('/api/plugins'));
  return plugins.find((plugin: { name: string }) => plugin.name === HOME_PLUGIN)?.version;
}

async function openUpdate(page: Page) {
  await page.goto('/plugins');
  const update = page.locator(`[data-plugin-action="update"][data-plugin-name="${HOME_PLUGIN}"]`);
  await expect(update).toBeVisible({ timeout: 30_000 });
  await update.click();
  const dialog = page.locator('dialog.home-package-upgrade');
  return { update, dialog, heading: dialog.getByRole('heading', { level: 2 }) };
}

test.describe.configure({ mode: 'serial' });

test.describe('in-place update recovery', () => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_IN_PLACE_UPGRADE_SANDBOX');

  test('a stale review is reviewed again, and a lost answer after success resolves from its own operation', async ({
    page,
    request,
    baseURL
  }) => {
    test.setTimeout(180_000);
    await seed({ baseURL, sandbox: fixture });
    await writeHomeRelease(fixture, NEW_VERSION);
    const commits: string[] = [];
    page.on('request', item => {
      if (item.url().endsWith('/home-upgrade/commit')) commits.push(item.postData() || '');
    });

    const { dialog, heading } = await openUpdate(page);
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to ${NEW_VERSION}?`, { timeout: 30_000 });
    // The release changes after the review: confirming that review is refused.
    await writeHomeRelease(fixture, '0.2.1');
    await dialog.getByRole('button', { name: 'Confirm update' }).click();
    await expect(dialog.locator('[role="alert"]')).toContainText('Review the upgrade again');
    expect(await installedVersion(request)).toBe('0.1.0');
    await dialog.getByRole('button', { name: 'Review again' }).click();
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to 0.2.1?`, { timeout: 30_000 });

    // The server applies the commit; its answer never reaches the page.
    await page.route(
      COMMIT,
      async route => {
        await route.fetch();
        await route.abort('connectionreset');
      },
      { times: 1 }
    );
    await dialog.getByRole('button', { name: 'Confirm update' }).click();
    await expect(heading).toHaveText(`${HOME_PLUGIN} is updated to 0.2.1`, { timeout: 60_000 });
    expect(await installedVersion(request)).toBe('0.2.1');
    // Two confirmations, two different reviews; nothing was committed again.
    expect(commits).toHaveLength(2);
    expect(new Set(commits).size).toBe(2);
    const status = await ok(await request.get(`/api/plugins/${HOME_PLUGIN}/home-upgrade`));
    await expect(dialog).toContainText(`Operation ${status.last_operation.id}`);
    await dialog.getByRole('button', { name: 'Close' }).click();
  });

  test('a commit that never arrived is not mistaken for the earlier success, and reopening reviews afresh', async ({
    page,
    request
  }) => {
    test.setTimeout(180_000);
    await writeHomeRelease(fixture, '0.3.0');
    const before = await ok(await request.get(`/api/plugins/${HOME_PLUGIN}/home-upgrade`));
    expect(before.last_operation).toMatchObject({ status: 'succeeded', to_version: '0.2.1' });
    let sent = 0;
    const { update, dialog, heading } = await openUpdate(page);
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to 0.3.0?`, { timeout: 30_000 });
    await page.route(
      COMMIT,
      async route => {
        sent += 1;
        await route.abort('connectionreset');
      },
      { times: 1 }
    );
    await dialog.getByRole('button', { name: 'Confirm update' }).click();
    await expect(heading).toHaveText('The outcome is not known yet', { timeout: 60_000 });
    await expect(dialog).toContainText('no record that this update started');
    await expect(dialog).not.toContainText('is updated to');
    await expect(dialog.getByRole('button', { name: 'Check again' })).toBeVisible();
    expect(sent).toBe(1);
    expect(await installedVersion(request)).toBe('0.2.1');

    // Closing promised nothing; reopening reads status, then offers a fresh
    // review that commits once.
    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();
    await expect(update).toBeFocused();
    await update.click();
    await expect(heading).toHaveText(`Update ${HOME_PLUGIN} to 0.3.0?`, { timeout: 30_000 });
    await dialog.getByRole('button', { name: 'Confirm update' }).click();
    await expect(heading).toHaveText(`${HOME_PLUGIN} is updated to 0.3.0`, { timeout: 60_000 });
    expect(await installedVersion(request)).toBe('0.3.0');

    // After a reload nothing newer remains: Update gives the ordinary
    // already-current reply, and no review or commit is offered.
    await dialog.getByRole('button', { name: 'Close' }).click();
    await page.reload();
    const commitsAfterReload: string[] = [];
    page.on('request', item => {
      if (/home-upgrade\/(review|commit)$/.test(item.url())) commitsAfterReload.push(item.url());
    });
    const again = await openUpdate(page);
    await expect(page.locator('.toast', { hasText: 'is already up to date' })).toBeVisible({
      timeout: 30_000
    });
    await expect(again.dialog).toHaveCount(0);
    expect(commitsAfterReload).toEqual([]);
    expect(await installedVersion(request)).toBe('0.3.0');
  });

  test('presentation only: an operation needing reconciliation holds the row and the dialog', async ({
    page
  }) => {
    test.setTimeout(120_000);
    await writeHomeRelease(fixture, '0.4.0');
    const held = {
      plugin_id: HOME_PLUGIN,
      installed_version: '0.3.0',
      available: false,
      homes: [{ name: 'North Studio' }],
      operation: {
        id: 'op-mocked',
        status: 'reconcile_required',
        from_version: '0.3.0',
        to_version: '0.4.0',
        reason: 'The plugin replacement stopped partway.'
      }
    };
    // Mocked status answer: the server is not in this state.
    await page.route(`**/api/plugins/${HOME_PLUGIN}/home-upgrade`, route =>
      route.request().method() === 'GET' ? route.fulfill({ json: held }) : route.continue()
    );
    const reviews: string[] = [];
    page.on('request', item => {
      if (/home-upgrade\/(review|commit)$/.test(item.url())) reviews.push(item.url());
    });
    await page.goto('/plugins');
    const row = page.locator('[data-plugin-home-upgrade="attention"]');
    await expect(row).toContainText('needs attention (operation op-mocked)', { timeout: 30_000 });
    const { dialog, heading } = await openUpdate(page);
    await expect(heading).toHaveText('This update needs attention', { timeout: 30_000 });
    await expect(dialog).toContainText('Nothing is chosen automatically');
    await expect(dialog.getByRole('button', { name: 'Check again' })).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Confirm update' })).toHaveCount(0);
    expect(reviews).toEqual([]);
  });
});
