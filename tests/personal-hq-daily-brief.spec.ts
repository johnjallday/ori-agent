import { test, expect, type Page } from '@playwright/test';

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
 * THIS FILE IS NOT RUN BY CI. The only Playwright job in ci.yml is the README
 * capture; `tests/*.spec.ts` are manual smoke specs. That is how #322 shipped:
 * the whole suite had been failing since `/workspaces` started redirecting (a
 * stale URL assertion in the first test, and `describe.serial` skipping the
 * five after it), and nothing was watching. Treat a green run here as a
 * release-time check, not a safety net.
 *
 * The automated guards for the same defects, which DO run in CI, are:
 *   - internal/web/templates_test.go — Home renders the build modal at all
 *   - workspace-map.test.js          — focus intent announces the HQ selection
 *   - home-workspace-cockpit.test.js — a lone HQ site still renders the map
 * (the last two via `npm run test:modules`, ci.yml).
 *
 * `describe.serial` is kept deliberately: test 3 builds the HQ that tests 4-6
 * inspect, so running them after it fails would only produce noise.
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
  let assistantReady = false;

  test.beforeEach(async ({ page }) => {
    await page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: assistantReady
            ? {
                state: 'active',
                state_version: 1,
                assistant_id: 'daily-brief-browser-assistant',
                display_name: 'Atlas',
                hq_workspace_id: 'daily-brief-browser-hq',
                availability: { model: { status: 'available', available: true } }
              }
            : { state: 'needs_hire', next_action: 'hire', availability: {} }
        })
      })
    );
    await page.route('**/api/personal-assistant/today', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          today: {
            state: 'active',
            relationship_state: 'active',
            display_name: 'Atlas',
            model: { status: 'available', available: true },
            brief: { health: { status: 'available' }, items: [] },
            decisions: { health: { status: 'healthy_empty' }, items: [] },
            priorities: { health: { status: 'healthy_empty' }, items: [] },
            follow_ups: { health: { status: 'healthy_empty' }, items: [] },
            results: { health: { status: 'healthy_empty' }, items: [] },
            links: { advanced: '/agents' }
          }
        })
      })
    );
  });

  async function openToday(page: Page) {
    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantTodayPanel')).toBeVisible();
  }

  // The direct focus intent remains the Map-level compatibility route for an
  // unbuilt Personal HQ. The current Mission 01 uses the separate named-assistant
  // quest, which is covered by the Personal Assistant foundation journey.
  test('focus intent selects the unbuilt Personal HQ landmark on the Map', async ({
    page,
    request
  }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const res = await request.post('/api/onboarding/skip');
    expect(res.ok()).toBeTruthy();
    await page.goto('/?focus=personal-hq');
    await expect(page).toHaveURL(/\/\?.*focus=personal-hq/);
    await expect(page.locator('#hqOnboardingGuided')).toHaveCount(0);

    // This profile has ZERO workspaces. The map must still render, because the
    // unbuilt HQ landmark is the whole point of this screen — hiding it as an
    // "empty" account is how a fresh user lost the only route to Build My HQ
    // (#322).
    await expect(page.locator('#cockpitMap')).toBeVisible();
    const site = page.locator('[data-hq-site]');
    await expect(site).toBeVisible();
    await expect(site).toHaveClass(/is-selected/);
    await expect(site).toContainText('Not created');

    // ...and it must be reachable, not just present. The camera's first-paint
    // guard only counted workspaces and group districts as content, so a
    // profile whose whole map is this one landmark was left at the hard-coded
    // default view — which put the site low and right of centre with its
    // caption behind the floating control strip (#329). The strip is a real
    // element, so this compares measured boxes rather than trusting a
    // hard-coded height.
    await expect(site.locator('.ws-map-tile-name')).toHaveText('Personal HQ');
    // The camera settles on the first honest measurement of the canvas; wait
    // for the transform it writes rather than for a fixed delay.
    await expect
      .poll(async () => page.evaluate(() => window.OriWorkspaceMap?.getCamera?.()?.zoom ?? null))
      .not.toBeNull();
    const strip = page.locator('.ws-map-actions');
    await expect(strip).toBeVisible();
    const siteBox = await site.boundingBox();
    const stripBox = await strip.boundingBox();
    const canvasBox = await page.locator('.ws-map-canvas').boundingBox();
    expect(siteBox, 'the landmark has no box to measure').not.toBeNull();
    expect(stripBox, 'the control strip has no box to measure').not.toBeNull();
    expect(canvasBox).not.toBeNull();
    expect(
      siteBox!.y + siteBox!.height,
      `landmark bottom ${siteBox!.y + siteBox!.height} must clear strip top ${stripBox!.y}`
    ).toBeLessThanOrEqual(stripBox!.y);
    // And inside the canvas on every other edge, so "clear of the strip" was
    // not achieved by pushing it off-screen instead.
    expect(siteBox!.y).toBeGreaterThanOrEqual(canvasBox!.y);
    expect(siteBox!.x).toBeGreaterThanOrEqual(canvasBox!.x);
    expect(siteBox!.x + siteBox!.width).toBeLessThanOrEqual(canvasBox!.x + canvasBox!.width);

    // Cockpit mode suppresses the map's own overview panel, so the context rail
    // is where the HQ's choices live — and the ONLY place they live. It has to
    // follow the map's focus-intent selection with no click from the user (#322).
    await expect(page.locator('[data-rail-panel="personal-hq"]')).toBeVisible();
    await expect(page.locator('[data-rail-panel="personal-hq"]')).toContainText(
      'Personal HQ has not been created'
    );
    await expect(page.locator('[data-hq-action="build"]')).toBeVisible();
    await expect(page.locator('[data-hq-action="import"]')).toBeVisible();

    // The dialog those actions open must exist on THIS page. It is hidden until
    // shown, so presence — not visibility — is the assertion that matters; when
    // it was absent the button silently did nothing at all (#322).
    await expect(page.locator('#hqBuildModal')).toHaveCount(1);
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
    await page.goto('/?focus=personal-hq');
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await page.locator('[data-hq-action="skip"]').click();
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await expect(page.locator('[data-hq-action="skip"]')).toHaveCount(0);

    await page.goto('/');
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator('#homeHQResume')).toHaveCount(0);
    await expect(page.locator('#homeDailyBrief')).toBeHidden();
    await expect(page.locator('[data-role="first-mission-status"]')).toHaveText('Saved for later');
  });

  // The Map-native action hands off to the existing setup modal and then
  // replaces the blueprint with the authoritative designated HQ landmark.
  test('Build My HQ with defaults creates the workspace and Home shows the Daily Brief', async ({
    page
  }) => {
    await page.goto('/?focus=personal-hq');
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    // Reached straight from the URL — no tile click. If the rail did not follow
    // the map's selection this button would not exist (#322).
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
    assistantReady = true;
    await page.reload();
    await openToday(page);
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
    await page.goto('/');
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
    await openToday(page);
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
    await openToday(page);
    // Secondary brief actions live behind the header's overflow disclosure;
    // only Refresh keeps permanent space in Today.
    await page.locator('#homeDailyBriefMenu > summary').click();
    await page.locator('#homeDailyBriefSettingsBtn').click();
    await expect(page.locator('#homeDailyBriefSettingsModal')).toBeVisible();
    await expect(page.locator('#homeDailyBriefTimezone')).not.toHaveValue('');
    await expect(page.locator('#homeDailyBriefHistoryList li').first()).toBeVisible();
  });
});
