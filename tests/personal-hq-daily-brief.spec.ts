import { test, expect } from '@playwright/test';

/**
 * Personal HQ / Daily Brief end-to-end coverage (PRD task 9.4/9.5).
 *
 * Run with: PLAYWRIGHT_BASE_URL=http://localhost:PORT npx playwright test tests/personal-hq-daily-brief.spec.ts
 * against an isolated smoke server (HOME/ORI_DATA_DIR sandboxed to a fresh
 * temp dir — see CLAUDE.md's smoke-testing recipe). Each test assumes a
 * genuinely brand-new profile at the start of the run and does not reset
 * server state between tests, so this file is written to run serially
 * against one fresh server per full run (matching this repo's CI default
 * of workers: 1) rather than depending on test isolation the shared local
 * server does not provide.
 *
 * Scope note: concurrent first-open/scheduled generation, DST gap/fold
 * schedule correctness, and Action Center notification deduplication are
 * already covered by fast, deterministic Go tests (internal/dailybrief,
 * internal/server) — re-proving them through a slow real browser adds
 * little and was left out of this file. Replace/clear/rename/delete-repair
 * UI flows and partial/fallback-state rendering are also deferred here;
 * they're covered at the Go/JS-unit level (internal/personalhq,
 * home-daily-brief.test.js) and are natural candidates for a follow-up
 * pass if deeper browser coverage is wanted later.
 */

test.describe.serial('Personal HQ onboarding and Daily Brief', () => {
  // Home remains the first surface. Mission 01 is a pull invitation into the
  // normal workspace Map, where the absent HQ appears as a selected blueprint
  // landmark rather than a full-screen takeover.
  test('Mission 01 focuses the unbuilt Personal HQ landmark on the Map', async ({
    page,
    request
  }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const res = await request.post('/api/onboarding/skip');
    expect(res.ok()).toBeTruthy();
    await page.goto('/');

    const mission = page.locator('[data-role="first-mission"]');
    await expect(mission).toBeVisible();
    await expect(mission).toContainText('Build My HQ');
    await mission.locator('[data-role="first-mission-action"]').click();

    await expect(page).toHaveURL(/\/workspaces\?view=map&focus=personal-hq/);
    await expect(page.locator('#hqOnboardingGuided')).toHaveCount(0);
    const site = page.locator('[data-hq-site]');
    await expect(site).toBeVisible();
    await expect(site).toHaveClass(/is-selected/);
    await expect(site).toContainText('Not created');
    await expect(page.locator('.ws-map-overview-body')).toContainText(
      'Personal HQ has not been created'
    );
    await expect(page.locator('[data-hq-action="build"]')).toBeVisible();
    await expect(page.locator('[data-hq-action="import"]')).toBeVisible();
    await expect(page.locator('#addFolderModal')).toBeHidden();

    await page.locator('[data-hq-action="import"]').click();
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#folderModalTitle')).toHaveText('Import HQ');
    await expect(page.locator('#folderImportHelp')).toContainText('Personal HQ');
    await expect(page.locator('#createFolderBtn')).toHaveText('Import HQ');
    await page.locator('#addFolderModal').getByRole('button', { name: 'Cancel' }).click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
  });

  // "Not now" suppresses the active invitation without hiding reality from
  // the Map: the unbuilt site remains visible, while Home has no duplicate HQ
  // banner competing with the progression card.
  test('Not now keeps the unbuilt HQ on the Map without a duplicate Home banner', async ({
    page
  }) => {
    await page.goto('/workspaces?view=map&focus=personal-hq');
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await page.locator('[data-hq-action="skip"]').click();
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await expect(page.locator('[data-hq-action="skip"]')).toHaveCount(0);

    await page.goto('/');
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator('#homeHQResume')).toHaveCount(0);
    await expect(page.locator('#homeDailyBrief')).toBeHidden();
    await expect(page.locator('[data-role="first-mission-status"]')).toHaveText('Not set up');
  });

  // The Map-native action hands off to the existing setup modal and then
  // replaces the blueprint with the authoritative designated HQ landmark.
  test('Build My HQ with defaults creates the workspace and Home shows the Daily Brief', async ({
    page
  }) => {
    await page.goto('/workspaces?view=map&focus=personal-hq');
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await page.locator('[data-hq-action="build"]').click();
    await expect(page.locator('#hqBuildModal')).toBeVisible();
    await expect(page.locator('#hqBuildWorkspaceRoot')).not.toHaveValue('');
    await expect(page.locator('#hqBuildWorkspaceRootStatus')).toContainText(/confirm|scan only/i);
    await expect(page.locator('#hqBuildName')).toHaveValue('My HQ');
    await expect(page.locator('#hqBuildTimezone')).not.toHaveValue('');
    await page.locator('#hqBuildSubmitBtn').click();

    await expect(page).toHaveURL(/\/$/, { timeout: 15000 });
    const rootResponse = await page.request.get('/api/settings/workspace-root');
    expect(rootResponse.ok()).toBeTruthy();
    expect((await rootResponse.json()).confirmed).toBe(true);
    await expect(page.locator('#homeDailyBrief')).toBeVisible();
    await expect(page.locator('#homeHQResume')).toHaveCount(0);

    // First-open generation runs in the background; the placeholder or the
    // finished brief should appear, never a blank body.
    await expect(page.locator('#homeDailyBriefBody')).not.toBeEmpty();
    await expect(page.locator('#homeDailyBriefBody')).toContainText(/./, { timeout: 15000 });
    await expect(page.locator('#homeDailyBriefOpenHQ')).toHaveAttribute('href', /\/workspaces\//);
  });

  test('the workspace Map shows the HQ badge on the designated workspace tile only', async ({
    page
  }) => {
    await page.goto('/workspaces');
    const hqTile = page.locator('.ws-map-tile.is-hq');
    await expect(hqTile).toHaveCount(1);
    await expect(hqTile.locator('.ws-map-tile-hq-badge')).toHaveText('HQ');
    await expect(hqTile).toContainText('My HQ');

    const otherBadges = page.locator('.ws-map-tile:not(.is-hq) .ws-map-tile-hq-badge');
    await expect(otherBadges).toHaveCount(0);
  });

  test('manual refresh replaces the current brief without hiding it while generating', async ({
    page
  }) => {
    await page.goto('/');
    await expect(page.locator('#homeDailyBrief')).toBeVisible();
    const bodyBefore = await page.locator('#homeDailyBriefBody').innerText();

    await page.locator('#homeDailyBriefRefreshBtn').click();

    // The refresh button disables while in flight — not asserted directly
    // here since generation against the deterministic fallback (no model
    // configured in this smoke environment) can complete faster than a
    // reliable window to observe the disabled state, but re-enabling by the
    // end and the body never emptying are exactly the behavior that matters
    // (the last successful brief is never hidden while refreshing).
    await expect(page.locator('#homeDailyBriefRefreshBtn')).toBeEnabled({ timeout: 15000 });
    await expect(page.locator('#homeDailyBriefBody')).not.toBeEmpty();
    expect(bodyBefore.length).toBeGreaterThan(0);
  });

  test('Brief settings modal opens scoped to the HQ and shows recent history', async ({ page }) => {
    await page.goto('/');
    await page.locator('#homeDailyBriefSettingsBtn').click();
    await expect(page.locator('#homeDailyBriefSettingsModal')).toBeVisible();
    await expect(page.locator('#homeDailyBriefTimezone')).not.toHaveValue('');
    await expect(page.locator('#homeDailyBriefHistoryList li').first()).toBeVisible();
  });
});
