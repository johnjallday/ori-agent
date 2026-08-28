import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

/**
 * Blueprint readiness — the demo checkpoint for the picker surfaces.
 *
 * Drives a real browser against the isolated demo sandbox seeded with one
 * blueprint in each readiness state, proves the keyboard-only path, and
 * captures the screenshots the group's demo requires.
 *
 * Run with:
 *   ./scripts/e2e.sh tests/blueprint-readiness-demo.spec.ts
 *
 * against the sandbox described in the feature's manual test guide. It reads
 * the catalog the server already serves and never writes to it.
 */

const SHOTS = 'tasks/screenshots/blueprint-readiness';

function cardByLabel(page: Page, label: string) {
  return page.locator('.workspace-template-card, .workspace-template-row').filter({
    hasText: label
  });
}

async function openCreateModal(page: Page) {
  // The demo sandbox is a fresh profile, so the onboarding modal would sit on
  // top of everything. Keep it out of the way and pin the theme for
  // deterministic screenshots.
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

async function setTheme(page: Page, theme: 'dark' | 'light') {
  await page.evaluate(t => {
    document.documentElement.setAttribute('data-bs-theme', t);
  }, theme);
}

test.beforeAll(() => {
  mkdirSync(SHOTS, { recursive: true });
});

test('every readiness state is legible on the blueprint cards', async ({ page }) => {
  await openCreateModal(page);

  // A ready built-in carries no badge: the common case looks exactly as it did.
  const ready = cardByLabel(page, 'Research Project').first();
  await expect(ready).toHaveAttribute('data-readiness-state', 'ready');
  await expect(ready.locator('.workspace-template-readiness-badge')).toHaveCount(0);

  // A blueprint whose plugin is installed but disabled: recoverable.
  const disabled = cardByLabel(page, 'Song Production').first();
  await expect(disabled).toHaveAttribute('data-readiness-state', 'action_required');
  await expect(disabled.locator('.workspace-template-readiness-badge')).toHaveText(
    /Setup required/
  );

  // A blueprint whose plugin ships nothing for this platform: unavailable.
  const unsupported = cardByLabel(page, 'Field Recorder').first();
  await expect(unsupported).toHaveAttribute('data-readiness-state', 'unavailable');
  await expect(unsupported.locator('.workspace-template-readiness-badge')).toHaveText(
    /Unavailable/
  );

  // A user template whose own manifest cannot be read: unavailable, and the
  // only state where the user is told to edit template.json.
  const broken = cardByLabel(page, 'My Broken Blueprint').first();
  await expect(broken).toHaveAttribute('data-readiness-state', 'unavailable');

  // A blueprint declaring a plugin nobody has installed: recoverable.
  const needsPlugin = cardByLabel(page, 'Needs A Plugin').first();
  await expect(needsPlugin).toHaveAttribute('data-readiness-state', 'action_required');

  for (const theme of ['dark', 'light'] as const) {
    await setTheme(page, theme);
    await page.locator('#templatePicker').screenshot({
      path: `${SHOTS}/cards-${theme}.png`
    });
  }
  await setTheme(page, 'dark');
});

test('the briefing explains each blocked state and offers one next action', async ({ page }) => {
  await openCreateModal(page);
  const panel = page.locator('#templateBriefingReadiness .workspace-blueprint-readiness');

  // Disabled plugin → Enable, and the lifecycle position stated exactly.
  await cardByLabel(page, 'Song Production').first().click();
  await expect(panel).toBeVisible();
  await expect(panel).toContainText('installed but disabled');
  await expect(panel).toContainText('studio-tools 1.4.0 — installed, still disabled');
  await expect(panel.locator('[data-readiness-action="enable_plugin"]')).toBeVisible();
  await page.locator('#templateBriefing').screenshot({ path: `${SHOTS}/briefing-disabled.png` });

  // Unsupported platform → explanation and escape routes, never a retry loop.
  await cardByLabel(page, 'Field Recorder').first().click();
  await expect(panel).toContainText('does not support this computer');
  await expect(panel.locator('[data-readiness-action="enable_plugin"]')).toHaveCount(0);
  await expect(panel.locator('[data-readiness-action="retry"]')).toHaveCount(0);
  await expect(panel.locator('[data-readiness-action="manage_plugins"]')).toBeVisible();
  await page.locator('#templateBriefing').screenshot({ path: `${SHOTS}/briefing-unsupported.png` });

  // Missing plugin → Install, with the source withheld until the trust preview.
  await cardByLabel(page, 'Needs A Plugin').first().click();
  await expect(panel).toContainText('not installed yet');
  await expect(panel).toContainText('absent-plugin — not installed');
  await expect(panel).not.toContainText('example.test');
  await expect(panel.locator('[data-readiness-action="install_plugin"]')).toBeVisible();

  // Invalid user template → the author's own diagnostic, behind a disclosure.
  await cardByLabel(page, 'My Broken Blueprint').first().click();
  await expect(panel).toContainText('could not be read');
  await expect(panel).toContainText("Fix this template's template.json");
  const diagnostic = panel.locator('.workspace-blueprint-readiness-diagnostic');
  await expect(diagnostic).toBeVisible();
  await diagnostic.locator('summary').click();
  await expect(diagnostic).toContainText('not_a_real_adapter');
  await page.locator('#templateBriefing').screenshot({ path: `${SHOTS}/briefing-invalid.png` });

  // A ready blueprint says nothing at all.
  await cardByLabel(page, 'Research Project').first().click();
  await expect(panel).toHaveCount(0);
});

test('a retired or plugin-owned blueprint is never told to edit template.json', async ({
  page
}) => {
  await openCreateModal(page);
  const panel = page.locator('#templateBriefingReadiness .workspace-blueprint-readiness');

  for (const label of ['Song Production', 'Field Recorder']) {
    await cardByLabel(page, label).first().click();
    await expect(panel).toBeVisible();
    await expect(panel).not.toContainText('template.json');
    await expect(panel.locator('.workspace-blueprint-readiness-diagnostic')).toHaveCount(0);
    await expect(panel.locator('[data-readiness-action="edit_template_manifest"]')).toHaveCount(0);
  }
});

test('the wizard refuses to advance past a blocker, by mouse and by keyboard', async ({ page }) => {
  await openCreateModal(page);
  const panel = page.locator('#templateBriefingReadiness .workspace-blueprint-readiness');

  await cardByLabel(page, 'Song Production').first().click();
  await page.locator('#wizardNextBtn').click();

  // Still on Blueprint, with the reason focused and announced.
  await expect(page.locator('#wizardStep1')).toBeVisible();
  await expect(page.locator('#wizardStep2')).toBeHidden();
  await expect(panel).toBeFocused();
  await expect(page.locator('#blueprintReadinessLive')).toContainText(
    /Cannot continue with Song Production/
  );
  await page.locator('#addFolderModal .modal-body').screenshot({ path: `${SHOTS}/blocked.png` });

  // The double-click shortcut is blocked too — it must not carry the user past
  // a problem they have not seen.
  await cardByLabel(page, 'Song Production').first().dblclick();
  await expect(page.locator('#wizardStep1')).toBeVisible();
  await expect(page.locator('#wizardStep2')).toBeHidden();

  // A ready blueprint advances normally.
  await cardByLabel(page, 'Research Project').first().click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
});

test('the blueprint grid is fully operable from the keyboard', async ({ page }) => {
  await openCreateModal(page);

  // One tab stop, on the current selection.
  const tabbable = page.locator(
    '.workspace-template-card[tabindex="0"], .workspace-template-row[tabindex="0"]'
  );
  await expect(tabbable).toHaveCount(1);

  await page.locator('.workspace-template-card').first().focus();
  // Arrow through to an unavailable blueprint: it is reachable like any other.
  for (let i = 0; i < 40; i++) {
    const label = await page.evaluate(() => document.activeElement?.textContent || '');
    if (label.includes('Field Recorder')) break;
    await page.keyboard.press('ArrowRight');
  }
  await expect(
    page.locator('.workspace-template-card:focus, .workspace-template-row:focus')
  ).toHaveAttribute('data-readiness-state', 'unavailable');
  // Selection follows focus, so the explanation is already on screen.
  await expect(
    page.locator('#templateBriefingReadiness .workspace-blueprint-readiness')
  ).toBeVisible();
  await expect(tabbable).toHaveCount(1);
});

test('Review restates a blocker rather than being the first to show it', async ({ page }) => {
  await openCreateModal(page);
  await cardByLabel(page, 'Research Project').first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('Readiness Demo');
  await page.locator('#wizardNextBtn').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();

  // A ready blueprint leaves Review clean and Create available.
  await expect(page.locator('#workspaceReviewReadiness')).toBeEmpty();
  await expect(page.locator('#createFolderBtn')).toBeEnabled();
  await page.locator('#wizardStep4').screenshot({ path: `${SHOTS}/review-ready.png` });
});
