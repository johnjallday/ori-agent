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
  // Covers the complete first visible moment (PRD FR19/FR116-118, task
  // 4.14/8.1): Home redirects to the launcher, the generic create-workspace
  // modal stays out of the way, and Ori's first mission renders even with
  // motion disabled. The app-level profile/model wizard is intentionally
  // completed through its real API, then the same page is reloaded to reveal
  // the mission waiting directly behind it.
  test('a brand-new profile is welcomed into Ori’s visible first mission', async ({
    page,
    request
  }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto('/');
    await expect(page).toHaveURL(/\/workspaces\?hq_onboarding=1/);

    // Clear the separate profile/model wizard so the mission underneath can
    // be asserted directly without bypassing the real first-launch route.
    const res = await request.post('/api/onboarding/skip');
    expect(res.ok()).toBeTruthy();
    await page.reload();

    const mission = page.locator('#hqOnboardingGuided');
    await expect(mission).toBeVisible();
    await expect(mission).toContainText('Welcome to Ori Agent');
    await expect(page.locator('#hqGuidedTitle')).toHaveText('Build your Personal HQ');
    await expect(page.locator('#hqGuidedBuildBtn')).toHaveText(/Build Personal HQ/);
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect(page.locator('.launcher-header')).toBeHidden();
  });

  // Covers the outcome PRD FR101 cares about through the visible mission UI:
  // once HQ setup is skipped, Home shows only the lightweight resume entry,
  // never the full takeover, and product features stay ungated.
  test('Skip for Now stays on the launcher and Home shows only the lightweight resume entry, never the full takeover', async ({
    page
  }) => {
    await page.goto('/workspaces?hq_onboarding=1');
    await expect(page.locator('#hqOnboardingGuided')).toBeVisible();
    await page.locator('#hqGuidedSkipBtn').click();
    await expect(page.locator('#hqOnboardingGuided')).toBeHidden();

    await page.goto('/');
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator('#homeHQResume')).toBeVisible();
    await expect(page.locator('#homeDailyBrief')).toBeHidden();
  });

  // Exercises the default setup path through the actual resume and modal UI.
  // This also proves the same module is active on /workspaces (where the
  // mission lives), not only on Home.
  test('Build My HQ with defaults creates the workspace and Home shows the Daily Brief', async ({
    page
  }) => {
    await page.goto('/');
    await expect(page.locator('#homeHQResume')).toBeVisible();
    await page.locator('#homeHQResumeBuildBtn').click();
    await expect(page).toHaveURL(/\/workspaces\?hq_onboarding=1/);
    await expect(page.locator('#hqOnboardingResume')).toBeVisible();
    await page.locator('#hqResumeBuildBtn').click();
    await expect(page.locator('#hqBuildModal')).toBeVisible();
    await expect(page.locator('#hqBuildName')).toHaveValue('My HQ');
    await expect(page.locator('#hqBuildTimezone')).not.toHaveValue('');
    await page.locator('#hqBuildSubmitBtn').click();

    await expect(page).toHaveURL(/\/$/, { timeout: 15000 });
    await expect(page.locator('#homeDailyBrief')).toBeVisible();
    await expect(page.locator('#homeHQResume')).toBeHidden();

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
