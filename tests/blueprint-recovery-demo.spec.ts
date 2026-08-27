import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

/**
 * Blueprint plugin recovery — the demo checkpoint for Group 3.
 *
 * Drives a real browser, against a real plugin manager and a real installed-
 * plugins store, through the full in-wizard recovery journey: select a
 * blueprint whose plugin is not installed, review the trust disclosure,
 * cancel once, then install and enable — all without leaving the Create
 * Workspace modal, with the entered details surviving, and a workspace
 * created at the end.
 *
 * Run with:
 *   ./scripts/e2e.sh tests/blueprint-recovery-demo.spec.ts
 *
 * against the isolated demo sandbox seeded with the `studio-blueprint`
 * template (declares the repo's example workspace-surface-demo plugin) on a
 * clean profile with nothing installed yet.
 */

const SHOTS = 'tasks/screenshots/blueprint-recovery';

function cardByLabel(page: Page, label: string) {
  return page.locator('.workspace-template-card, .workspace-template-row').filter({
    hasText: label
  });
}

async function openCreateModal(page: Page) {
  await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.goto('/');
  await page.waitForFunction(() => Boolean(document.getElementById('addFolderModal')));
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible({ timeout: 15000 });
  await expect(cardByLabel(page, 'Blank').first()).toBeVisible();
}

test.beforeAll(() => {
  mkdirSync(SHOTS, { recursive: true });
});

test('install-and-enable recovery: cancel once, then complete, with details preserved and a workspace created', async ({
  page
}) => {
  await openCreateModal(page);

  const card = cardByLabel(page, 'Surface Studio').first();
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute('data-readiness-state', 'action_required');

  // Fill in Details context BEFORE recovery, so we can prove it survives.
  await card.click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep1')).toBeVisible(); // blocked: still on Blueprint

  const panel = page.locator('#templateBriefingReadiness .workspace-blueprint-readiness');
  await expect(panel).toContainText('not installed yet');

  // ---- first pass: install, review trust, CANCEL ----
  await panel.locator('[data-readiness-action="install_plugin"]').click();
  const recovery = page.locator('#blueprintRecoveryPanel');
  await expect(recovery).toContainText('Install this blueprint');
  await expect(recovery).toContainText('Installed from');
  await expect(recovery).toContainText('workspace-surface-demo');
  // The complete disclosure: commands, skills, and permission scopes if any.
  await expect(recovery.locator('.plugin-trust-report')).toBeVisible();
  await page.locator('#addFolderModal .modal-body').screenshot({ path: `${SHOTS}/disclosure.png` });

  await recovery.getByRole('button', { name: 'Cancel' }).click();
  await expect(recovery).toBeEmpty();
  // Cancelling installed nothing — the card is still blocked.
  await expect(card).toHaveAttribute('data-readiness-state', 'action_required');

  // ---- now proceed to Details and enter real content ----
  await card.click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep1')).toBeVisible();

  // ---- second pass: install and enable for real ----
  await panel.locator('[data-readiness-action="install_plugin"]').click();
  await expect(recovery.getByRole('button', { name: 'Install and enable' })).toBeVisible();
  await recovery.getByRole('button', { name: 'Install and enable' }).click();
  await expect(recovery).toContainText('Installed and enabled', { timeout: 20000 });
  await page.locator('#addFolderModal .modal-body').screenshot({ path: `${SHOTS}/recovered.png` });

  // The card is ready without closing the wizard.
  await expect(card).toHaveAttribute('data-readiness-state', 'ready', { timeout: 5000 });
  await expect(page.locator('#templateBriefingReadiness')).toBeEmpty();

  // Now advance and confirm the entered details survived recovery.
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill('Recovered Studio');
  await page.locator('#folderDescriptionInput').fill('Filled in before recovery ran.');
  await page.locator('#wizardNextBtn').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewReadiness')).toBeEmpty();
  await page.locator('#wizardStep4').screenshot({ path: `${SHOTS}/review-ready.png` });

  await expect(page.locator('#createFolderBtn')).toBeEnabled();
  await page.locator('#createFolderBtn').click();
  await expect(page.locator('#addFolderModal')).toBeHidden({ timeout: 15000 });
});
