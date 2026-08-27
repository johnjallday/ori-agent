import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

/**
 * Upgrade regression — the browser half of Group 5's final demo.
 *
 * internal/server's TestBlueprintUpgradeRegression_RetiredBuiltinWithLegacyPluginRecord
 * proves this scenario end to end against the real plugin.Manager. This spec
 * drives the SAME starting profile through the actual browser: a retired
 * built-in template.json still on disk, and a plugin installed before
 * install roots were absolutized (a bare relative install_dir, no
 * resolved_blueprints — it predates blueprint contribution entirely).
 *
 * Run with:
 *   ./scripts/e2e.sh tests/blueprint-upgrade-demo.spec.ts
 *
 * against that seeded upgrade profile (see
 * tasks/test-guide-blueprint-setup-recovery-ux.md for the exact fixture).
 */

const SHOTS = 'tasks/screenshots/blueprint-upgrade';

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
  await expect(page.locator('#pluginList')).not.toContainText('Loading');
}

async function openCreateModal(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean(document.getElementById('addFolderModal')));
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible({ timeout: 15000 });
}

test.beforeAll(() => {
  mkdirSync(SHOTS, { recursive: true });
});

test('a retired built-in and a legacy plugin record migrate to the trusted replacement', async ({
  page
}) => {
  // ---- before: the retired built-in is what the wizard offers ----
  await openCreateModal(page);
  const retiredCard = page
    .locator('.workspace-template-card, .workspace-template-row')
    .filter({ hasText: 'Old Demo Workspace' });
  await expect(retiredCard).toBeVisible();
  await expect(retiredCard).toHaveAttribute('data-readiness-state', 'unavailable');
  await retiredCard.click();
  const panel = page.locator('#templateBriefingReadiness .workspace-blueprint-readiness');
  await expect(panel).toContainText('no longer included in Ori');
  await expect(panel).toContainText('Its files are untouched on disk');
  await page
    .locator('#templateBriefing')
    .screenshot({ path: `${SHOTS}/1-retired-before-upgrade.png` });

  // ---- migrate: Update the legacy plugin record from the Plugins page ----
  await openPluginsPage(page);
  const row = page.locator('#pluginList > div').filter({ hasText: 'workspace-surface-demo' });
  await expect(row).toContainText('Installed, still disabled');
  await page.locator('#pluginList').screenshot({ path: `${SHOTS}/2-legacy-record-disabled.png` });

  await row.getByRole('button', { name: 'Update' }).click();
  await expect(page.locator('#pluginTrust')).toBeVisible({ timeout: 15000 });
  await page
    .locator('#pluginTrust')
    .screenshot({ path: `${SHOTS}/3-update-discloses-blueprint.png` });
  await page.locator('#pluginTrustConfirm').click();
  await expect(page.locator('#pluginTrust')).toBeHidden({ timeout: 15000 });

  // Enablement is explicit — Update alone does not switch it on.
  await expect(row).toContainText('Installed, still disabled');
  await row.getByRole('button', { name: 'Enable' }).click();
  await expect(row).toContainText('Installed and enabled', { timeout: 10000 });
  await page.locator('#pluginList').screenshot({ path: `${SHOTS}/4-enabled-after-update.png` });

  // ---- after: the trusted replacement, not the retired built-in ----
  await openCreateModal(page);
  await expect(retiredCard).toHaveCount(0);
  // The plugin's own blueprint manifest names it "Workspace Surface Demo" —
  // display text that changed completely from the retired built-in's "Old
  // Demo Workspace". The blueprint ID (demo-workspace) is what actually
  // carried over; the name never had to.
  const replacement = page
    .locator('.workspace-template-card, .workspace-template-row')
    .filter({ hasText: 'Workspace Surface Demo' });
  await expect(replacement.first()).toBeVisible();
  await expect(replacement.first()).toHaveAttribute('data-readiness-state', 'ready');
  await page.locator('#templatePicker').screenshot({ path: `${SHOTS}/5-replacement-is-ready.png` });
});
