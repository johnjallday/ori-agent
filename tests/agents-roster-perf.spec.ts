import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Deterministic 100-agent responsiveness benchmark (PRD FR95/FR99, Success
// Metric 8). Self-seeding: creates its own fixture agents in parallel so the
// test runs unattended in CI or a fresh dev server, rather than depending on
// an externally pre-seeded roster. Records timings to the console rather than
// gating on a fixed budget — this is a development-machine benchmark, not a
// CI perf regression gate — but a generous ceiling still catches an
// accidental O(n^2) regression outright.
const baseUrl = process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';
const FIXTURE_COUNT = 100;

test.describe('Agents collection performance (100 agents)', () => {
  test('search, filter, sort, view switch, and Inspector open stay responsive', async ({
    page,
    request
  }, testInfo) => {
    testInfo.setTimeout(120_000);
    const prefix = `PWPerf${Date.now()}`;
    const names = Array.from(
      { length: FIXTURE_COUNT },
      (_, i) => `${prefix} ${String(i).padStart(3, '0')}`
    );

    // Created in parallel: this setup cost is not part of the timed
    // collection-interaction benchmark below.
    await Promise.all(
      names.map(n =>
        request.post(`${baseUrl}/api/agents`, {
          data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini', tags: ['perf-fixture'] }
        })
      )
    );

    try {
      await installLocalCdn(page);
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );

      const listRequests: string[] = [];
      const detailRequests: string[] = [];
      page.on('request', r => {
        const u = r.url();
        if (u.includes('/api/agents/dashboard/list')) listRequests.push(u);
        if (/\/api\/agents\/[^/]+\/detail/.test(u)) detailRequests.push(u);
      });

      const timings: Record<string, number> = {};
      const time = async (label: string, fn: () => Promise<void>) => {
        const start = Date.now();
        await fn();
        timings[label] = Date.now() - start;
      };

      await time('initial render', async () => {
        await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
        await expect(page.locator('#rosterList')).toBeVisible();
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(FIXTURE_COUNT);
      await page.locator('#rosterSearch').fill('');
      await expect(page.locator('.roster-card').first()).toBeVisible();

      // Rendering the fixture must not add one detail request per card (FR95).
      // Up to one is expected: the page auto-focuses the first agent and
      // pre-renders its Overview content so reopening the Inspector is instant.
      expect(
        detailRequests.length,
        'initial render must not fetch per-card detail'
      ).toBeLessThanOrEqual(1);
      expect(listRequests.length, 'initial render must use exactly one list request').toBe(1);
      const initialDetailCount = detailRequests.length;

      await time(`search "${prefix}"`, async () => {
        await page.locator('#rosterSearch').fill(prefix);
        await expect(page.locator('.roster-card')).toHaveCount(FIXTURE_COUNT);
      });

      await time('clear search', async () => {
        await page.locator('#rosterSearch').fill('');
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await time('quick filter: built-in', async () => {
        await page.locator('[data-quick="builtin"]').click();
        await expect(page.locator('.roster-card')).toHaveCount(3);
      });

      await time('clear quick filter', async () => {
        await page.locator('[data-quick="all"]').click();
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await time('sort by level (desc)', async () => {
        await page.locator('#rosterSort').selectOption('level-desc');
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await time('sort by name (asc)', async () => {
        await page.locator('#rosterSort').selectOption('name-asc');
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await time('switch to List view', async () => {
        await page.locator('#viewList').click();
        await expect(page.locator('#rosterList')).toHaveClass(/is-list/);
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await time('switch back to Gallery view', async () => {
        await page.locator('#viewGallery').click();
        await expect(page.locator('.roster-card').first()).toBeVisible();
      });

      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(FIXTURE_COUNT);

      await time('open Inspector (Overview)', async () => {
        await page.locator('.roster-card').first().locator('.roster-card__open').click();
        await expect(page.locator('#inspector')).toBeVisible();
        await expect(page.locator('#overviewFacts .stage-form')).toBeVisible();
      });

      await time('open Toolbox tab', async () => {
        await page.locator('#tab-toolbox').click();
        await expect(page.locator('#panel-toolbox')).toBeVisible();
      });

      await time('select all visible, then clear', async () => {
        await page.locator('#rosterSelectAll').click();
        await expect(page.locator('#bulkCount')).toHaveText(`${FIXTURE_COUNT} selected`);
        await page.locator('#rosterClearSelection').click();
        await expect(page.locator('#bulkBar')).toBeHidden();
      });

      // Opening the Inspector on a DIFFERENT agent adds at most one more
      // detail request (that agent's own), however large the collection
      // (FR95/FR96) — never one per card.
      expect(detailRequests.length).toBeLessThanOrEqual(initialDetailCount + 1);
      expect(listRequests.length).toBe(1);

      console.log(
        'PERF_100_AGENTS ' +
          JSON.stringify(
            { fixtureCount: FIXTURE_COUNT, timings, detailRequests: detailRequests.length },
            null,
            1
          )
      );

      for (const [label, ms] of Object.entries(timings)) {
        expect(ms, `${label} took ${ms}ms`).toBeLessThan(5000);
      }
    } finally {
      await Promise.all(
        names.map(n =>
          request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`).catch(() => {})
        )
      );
    }
  });
});
