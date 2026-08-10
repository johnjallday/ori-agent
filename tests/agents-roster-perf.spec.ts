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

// The fixture used to be 100 identical fallback avatars, which is the cheapest
// possible roster to render and therefore the least informative one. A real
// collection mixes curated portraits (an <img> per card), uploaded avatars, and
// generated fallbacks, so the fixture does too (PRD FR-89–FR-101, FR-120–FR-124).
const CHARACTER_IDS = [
  'research-archivist',
  'product-builder',
  'team-caretaker',
  'operations-keeper',
  'automation-specialist',
  'decision-strategist',
  'project-coordinator',
  'insight-researcher'
];

// Set PERF_PLAIN_IDENTITIES=1 to build the pre-feature fixture instead: 100
// agents on the deterministic fallback, with no portraits to fetch. That is the
// before-side of the interactive-readiness comparison, and keeping it in the
// same file means the two runs differ only in the fixture (Success Metric 10).
const PLAIN_IDENTITIES = process.env.PERF_PLAIN_IDENTITIES === '1';

// Every third agent gets a character portrait; the rest stay generated. The
// fixture uses the canonical appearance contract, so it measures what the app
// actually serves rather than a shape the API no longer accepts.
//
// Uploads are deliberately absent here: an upload has to be POSTed per agent
// after creation, which would make seeding 100 agents an order of magnitude
// slower without changing what this benchmark measures — the catalog fetch and
// the portrait-loading behaviour. Upload rendering is covered in
// tests/unified-agent-appearance.spec.ts.
function identityFor(i: number) {
  if (PLAIN_IDENTITIES) return {};
  if (i % 3 === 0) {
    return {
      appearance: {
        mode: 'character',
        character: { catalog_id: CHARACTER_IDS[i % CHARACTER_IDS.length] }
      }
    };
  }
  return {};
}

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
    const created = await Promise.all(
      names.map((n, i) =>
        request.post(`${baseUrl}/api/agents`, {
          data: {
            name: n,
            type: 'tool-calling',
            model: 'gpt-4o-mini',
            tags: ['perf-fixture'],
            ...identityFor(i)
          }
        })
      )
    );
    // Checked rather than assumed: a rejected identity payload used to shrink
    // the fixture silently, and the benchmark then measured a smaller roster
    // than it claimed to.
    const rejected = created.filter(r => !r.ok());
    expect(rejected.length, `${rejected.length} fixture agents were rejected`).toBe(0);

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
      const catalogRequests: string[] = [];
      page.on('request', r => {
        const u = r.url();
        if (u.includes('/api/agents/dashboard/list')) listRequests.push(u);
        if (/\/api\/agents\/[^/]+\/detail/.test(u)) detailRequests.push(u);
        if (u.includes('/api/characters')) catalogRequests.push(u);
      });

      // Character art is served slowly and one asset is failed outright, so the
      // benchmark measures the roster under the conditions that actually hurt:
      // noncritical images still in flight, and one that will never arrive
      // (FR-101/FR-124).
      let slowedAssets = 0;
      await page.route('**/characters/*/*.svg', async route => {
        slowedAssets++;
        if (slowedAssets % 9 === 0) return route.abort('failed');
        await new Promise(r => setTimeout(r, 120));
        return route.continue();
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

      // Names, status, and the management controls are usable before the
      // portraits settle — the point of the reserved geometry and the lazy
      // decode (FR-101/FR-120–FR-124).
      await time('management usable before art settles', async () => {
        await expect(page.locator('.roster-card').first().locator('.agent-card__name')).toHaveText(
          /\S/
        );
        await page.locator('.roster-card').first().locator('.roster-card__check').check();
        await expect(page.locator('#bulkBar')).toBeVisible();
        await page.locator('#rosterClearSelection').click();
        await expect(page.locator('#bulkBar')).toBeHidden();
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

      // One catalog fetch for the whole page, however many agents have a
      // character. A per-agent fetch is the failure this guards: it would be
      // invisible on a three-agent roster and crippling on a hundred (FR-104).
      expect(
        catalogRequests.length,
        `expected at most one catalog request, got ${catalogRequests.length}`
      ).toBeLessThanOrEqual(1);

      // Every avatar reserves its box before its bitmap arrives, so a slow or
      // failed portrait cannot reflow the collection (FR-103).
      const unsizedImages = await page
        .locator('.roster-card .agent-avatar img')
        .evaluateAll(
          imgs =>
            imgs.filter(img => !img.getAttribute('width') || !img.getAttribute('height')).length
        );
      expect(unsizedImages, 'every avatar image must reserve its box').toBe(0);

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

      // A failed portrait degrades to the deterministic fallback in place; it
      // never leaves a broken-image glyph and never blocks the row (FR-74).
      const brokenImages = await page.locator('.agent-avatar__portrait').evaluateAll(
        imgs =>
          imgs.filter(img => {
            const i = img as HTMLImageElement;
            return i.complete && i.naturalWidth === 0;
          }).length
      );
      expect(brokenImages, 'a failed portrait must be replaced, not left broken').toBe(0);

      console.log(
        'PERF_100_AGENTS ' +
          JSON.stringify(
            {
              fixtureCount: FIXTURE_COUNT,
              timings,
              detailRequests: detailRequests.length,
              slowedAssets
            },
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
