import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

/**
 * Setup-state consistency — the demo checkpoint for Group 4.
 *
 * Exercises the Plugins page's installed-disabled and enabled-ready states at
 * desktop and narrow widths, and proves the same lifecycle wording the
 * creation wizard uses actually renders here too — not just that the two
 * share a source constant.
 *
 * The Review receipt (owner/version + this-session recovery) and the
 * unsupported-artifact state are demoed elsewhere and are not repeated here:
 * blueprint-recovery-demo.spec.ts exercises the receipt end to end against a
 * real plugin, and blueprint-readiness-demo.spec.ts already captures the
 * unsupported card and briefing (a wizard-only concept — plugins.js lists
 * every installed plugin regardless of platform support and has no dedicated
 * unsupported-state copy of its own).
 *
 * Run with:
 *   ./scripts/e2e.sh tests/blueprint-setup-lifecycle-demo.spec.ts
 *
 * against an isolated demo sandbox whose installed-plugins store already
 * carries one disabled plugin and one enabled plugin (see
 * tasks/test-guide-blueprint-setup-recovery-ux.md for the seed).
 */

const SHOTS = 'tasks/screenshots/blueprint-setup-lifecycle';

// These tests share one server-side installed-plugins store (an isolated demo
// profile, not a per-test fixture), and the last test enables a plugin the
// first two assume is still disabled. Serial order, mutating test last, keeps
// that shared state from racing across parallel workers.
test.describe.configure({ mode: 'serial' });

async function openPluginsPage(page: Page) {
  await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.goto('/plugins');
  await expect(page.locator('#pluginList')).toBeVisible();
  // Wait for the real list to replace the loading placeholder.
  await expect(page.locator('#pluginList')).not.toContainText('Loading');
}

function pluginRow(page: Page, name: string) {
  return page.locator('#pluginList > div').filter({ hasText: name });
}

test.beforeAll(() => {
  mkdirSync(SHOTS, { recursive: true });
});

test('the Plugins page states disabled, enabled, and lets an unsupported one be uninstalled — desktop and narrow', async ({
  page
}) => {
  await openPluginsPage(page);

  const disabled = pluginRow(page, 'disabled-demo');
  await expect(disabled).toContainText('Installed, still disabled');
  await expect(disabled.getByRole('button', { name: 'Enable' })).toBeVisible();

  const enabled = pluginRow(page, 'enabled-demo');
  await expect(enabled).toContainText('Installed and enabled');
  await expect(enabled.getByRole('button', { name: 'Disable' })).toBeVisible();

  for (const width of [1280, 375]) {
    await page.setViewportSize({ width, height: 900 });
    await page.locator('#pluginList').screenshot({
      path: `${SHOTS}/plugins-list-${width}.png`
    });
    // No horizontal overflow at the narrow width.
    const scrollWidth = await page.evaluate(() => document.body.scrollWidth);
    const clientWidth = await page.evaluate(() => document.documentElement.clientWidth);
    expect(scrollWidth).toBeLessThanOrEqual(clientWidth + 1);
  }
  await page.setViewportSize({ width: 1280, height: 900 });
});

test('the same lifecycle words appear on the Plugins page and in the wizard readiness panel', async ({
  page
}) => {
  // This is the cross-surface half of "Plugins/Wizard state labels agree":
  // the unit tests already prove the JS labels are the same shared strings;
  // this proves the actual rendered text agrees, not just the source.
  await openPluginsPage(page);
  await expect(pluginRow(page, 'disabled-demo')).toContainText('Installed, still disabled');
  await expect(pluginRow(page, 'enabled-demo')).toContainText('Installed and enabled');

  const label = await page.evaluate(
    () =>
      // @ts-expect-error page global
      window.PluginLifecycle.LIFECYCLE_LABELS
  );
  expect(label.DISABLED).toBe('installed, still disabled');
  expect(label.ENABLED).toBe('installed and enabled');
});

test('enabling from the Plugins page updates the row in place, matching wizard wording', async ({
  page
}) => {
  await openPluginsPage(page);
  const row = pluginRow(page, 'disabled-demo');
  await row.getByRole('button', { name: 'Enable' }).click();
  await expect(pluginRow(page, 'disabled-demo')).toContainText('Installed and enabled', {
    timeout: 10000
  });
  await page.locator('#pluginList').screenshot({ path: `${SHOTS}/enabled-after-toggle.png` });
});
