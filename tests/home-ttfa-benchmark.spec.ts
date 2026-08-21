import { test, expect, Page, APIRequestContext } from '@playwright/test';

/**
 * Home time-to-first-meaningful-action (TTfA) benchmark — PRD Success Metric 1
 * and FR141.
 *
 * This is the SHARED harness for both measurements in the task list:
 *   - Task 1.2 captures the pre-change baseline (Operations Board Home).
 *   - Task 7.5 repeats it against the Map-first cockpit with the SAME fixture,
 *     the same first-action definition, and the same cohort size.
 *
 * It is deliberately not part of CI. Run it against an isolated demo server:
 *   wt demo 8931
 *   ./scripts/e2e.sh --port 8931 tests/home-ttfa-benchmark.spec.ts
 *
 * Write the reported median into tasks/ttfa-benchmark.md so the two runs stay
 * comparable.
 *
 * ---------------------------------------------------------------------------
 * What is measured
 * ---------------------------------------------------------------------------
 * "First meaningful action" is defined as: **from a cold Home load, act on an
 * existing workspace** — i.e. the user ends up on that workspace's detail page.
 * A benchmark cannot measure human deliberation, so this measures the thing the
 * product actually controls: the wall-clock cost of the fastest path the UI
 * permits, driven by an ideal (zero-think-time) scripted user, plus the number
 * of intermediate page loads that path requires.
 *
 * Two numbers are reported per run:
 *   - `journeyMs` — navigation start on `/` until the target workspace's detail
 *     page is interactive. This is the Success-Metric-1 number.
 *   - `ttfaMarkerMs` — the in-page `[home.ttfa]` structured console marker
 *     (FR141), i.e. Home's own instrumented time from script start to the
 *     qualifying interaction. Reported for cross-checking; it excludes the
 *     navigation that follows the action.
 *
 * The fixture is a fixed, seeded cohort so both runs see identical data.
 */

// ---------------------------------------------------------------------------
// Fixture — keep IDENTICAL between the baseline and the post-change run.
// ---------------------------------------------------------------------------

/**
 * Number of workspaces seeded before measuring.
 *
 * Deliberately larger than the Operations Board's 6-card "recent workspaces"
 * slice, so the fixture covers BOTH real situations rather than only the one
 * that flatters the current Home:
 *   - Scenario A: the target is one of the recent workspaces Home already shows.
 *   - Scenario B: the target is any other workspace, which the Operations Board
 *     can only reach by detouring through the `/workspaces` launcher.
 * Reporting both keeps the comparison honest — Scenario A is the case where the
 * baseline is strongest (a card is a single direct link).
 */
const FIXTURE_WORKSPACE_COUNT = 10;

/** How many workspaces the Operations Board shows as cards on Home. */
const HOME_RECENT_CARD_LIMIT = 6;

/** Number of timed repetitions; the median of these is the reported figure. */
const COHORT_SIZE = 5;

/** Stable prefix so a re-run can find (and reuse) the seeded cohort. */
const FIXTURE_PREFIX = 'TTfA Bench';

type SeededWorkspace = { id: string; name: string; folder_slug: string };

async function listWorkspaces(request: APIRequestContext): Promise<SeededWorkspace[]> {
  const res = await request.get('/api/workspaces');
  if (!res.ok()) throw new Error(`list workspaces failed (${res.status()})`);
  const body = (await res.json()) as {
    workspaces?: SeededWorkspace[];
    folders?: SeededWorkspace[];
  };
  return body.workspaces ?? body.folders ?? [];
}

async function createWorkspace(request: APIRequestContext, name: string): Promise<SeededWorkspace> {
  const res = await request.post('/api/workspaces', {
    data: {
      name,
      description: `Seeded fixture for the Home TTfA benchmark (${name}).`,
      workspace_preset: 'general'
    }
  });
  const body = (await res.json().catch(() => ({}))) as {
    folder?: { id?: string; folder_slug?: string };
  };
  const id = body.folder?.id;
  const folderSlug = body.folder?.folder_slug;
  if (!res.ok() || !id || !folderSlug) {
    throw new Error(
      `create workspace failed (${res.status()}): ${JSON.stringify(body).slice(0, 300)}`
    );
  }
  return { id, name, folder_slug: folderSlug };
}

/**
 * Seed the fixture cohort idempotently, so re-running the benchmark against the
 * same sandbox measures the same data rather than an ever-growing map.
 */
async function ensureFixture(request: APIRequestContext): Promise<SeededWorkspace[]> {
  const existing = (await listWorkspaces(request)).filter(ws =>
    String(ws.name || '').startsWith(FIXTURE_PREFIX)
  );
  const seeded = [...existing];
  for (let i = existing.length; i < FIXTURE_WORKSPACE_COUNT; i++) {
    seeded.push(
      await createWorkspace(request, `${FIXTURE_PREFIX} ${String(i + 1).padStart(2, '0')}`)
    );
  }
  return seeded.slice(0, FIXTURE_WORKSPACE_COUNT);
}

// Concrete workspaces are created without an entry agent by design; the detail
// page would otherwise raise a mandatory prompt that is not part of this
// journey and would pollute the timing.
async function suppressEntryAgentPrompts(page: Page, workspaces: SeededWorkspace[]) {
  await page.addInitScript(
    ids => {
      for (const id of ids as string[]) {
        window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
      }
    },
    workspaces.map(ws => ws.id)
  );
}

/** Capture the structured `[home.ttfa]` console marker (FR141). */
function captureTTFAMarker(page: Page): { value: () => number | null } {
  let ms: number | null = null;
  page.on('console', msg => {
    if (ms !== null) return;
    const text = msg.text();
    if (!text.includes('[home.ttfa]')) return;
    const match = /"?ms"?:\s*(\d+)/.exec(text);
    if (match) ms = Number(match[1]);
  });
  return { value: () => ms };
}

function median(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? Math.round((sorted[mid - 1] + sorted[mid]) / 2) : sorted[mid];
}

// ---------------------------------------------------------------------------
// The journey
// ---------------------------------------------------------------------------

/**
 * Drive the fastest path from a cold `/` to the target workspace's detail page.
 *
 * Both Home shapes are supported by the SAME routine so the two runs stay
 * comparable — it takes whichever path the build under test offers:
 *   - Map-first cockpit: select the site, then use Open Workspace in context.
 *   - Operations Board (baseline): click the workspace card, or fall through to
 *     the `/workspaces` launcher when the card is not on Home.
 * Anything it cannot find is a real finding, not a harness bug: it means that
 * build offers no Home path to that workspace at all.
 */
async function runJourney(
  page: Page,
  target: SeededWorkspace
): Promise<{ ms: number; path: string }> {
  const start = Date.now();
  await page.goto('/');

  // --- Path A: the Map-first cockpit (select, then explicit open). ---
  const mapSite = page.locator(`.ws-map-tile[data-ws-id="${target.id}"]`);
  if (await mapSite.count()) {
    await mapSite.first().click();
    const openAction = page.locator('#cockpitContextModal [data-cockpit-rail-open]');
    await expect(openAction).toBeVisible();
    await openAction.click();
    await page.waitForURL(`**/workspaces/${target.folder_slug}`);
    await expect(page.locator('#workspaceCommandView')).toBeVisible();
    return { ms: Date.now() - start, path: 'cockpit-map-select-then-open' };
  }

  // --- Path B: the Operations Board card straight to the workspace. ---
  const card = page.locator(`a.home-workspace-card[href="/workspaces/${target.folder_slug}"]`);
  if (await card.count()) {
    await card.first().click();
    await page.waitForURL(`**/workspaces/${target.folder_slug}`);
    await expect(page.locator('#workspaceCommandView')).toBeVisible();
    return { ms: Date.now() - start, path: 'home-recent-card' };
  }

  // --- Path C: Home offers no direct route; detour via the launcher. ---
  const viewAll = page.locator('[data-role="view-all"]');
  if (await viewAll.count()) {
    await viewAll.first().click();
  } else {
    await page.goto('/workspaces');
  }
  const launcherSite = page.locator(`.ws-map-tile[data-ws-id="${target.id}"]`);
  await expect(launcherSite.first()).toBeVisible();
  // The legacy Map opens on a repeat click of an already-selected tile.
  await launcherSite.first().click();
  await launcherSite.first().click();
  await page.waitForURL(`**/workspaces/${target.folder_slug}**`);
  await expect(page.locator('#workspaceCommandView')).toBeVisible();
  return { ms: Date.now() - start, path: 'launcher-detour' };
}

/**
 * Order the fixture the way Home orders its recent-workspace cards (most
 * recently updated first), so a scenario can target a workspace that is
 * definitely on the board or definitely past its slice.
 */
async function recencyOrder(
  request: APIRequestContext,
  fixture: SeededWorkspace[]
): Promise<SeededWorkspace[]> {
  const ids = new Set(fixture.map(ws => ws.id));
  const all = (await listWorkspaces(request)) as (SeededWorkspace & {
    updated_at?: string;
    created_at?: string;
  })[];
  return all
    .filter(ws => ids.has(ws.id))
    .sort(
      (a, b) =>
        new Date(b.updated_at || b.created_at || 0).getTime() -
        new Date(a.updated_at || a.created_at || 0).getTime()
    );
}

test.describe('Home time-to-first-meaningful-action benchmark', () => {
  // Serial: a shared fixture plus wall-clock timing must not contend with
  // parallel workers on the same server.
  test.describe.configure({ mode: 'serial' });

  test('median journey from cold Home to acting on an existing workspace', async ({ page }) => {
    test.setTimeout(300_000);

    // First-run onboarding is a modal over the whole app; it is not part of the
    // journey under test and would otherwise intercept every click.
    await page.request.post('/api/onboarding/skip').catch(() => {});
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      })
    );

    const fixture = await ensureFixture(page.request);
    expect(fixture.length).toBe(FIXTURE_WORKSPACE_COUNT);
    await suppressEntryAgentPrompts(page, fixture);

    // `/api/workspaces` is not ordered by recency, so ask Home's own ordering
    // rule (most recently updated first) which workspaces are on the board.
    const byRecency = await recencyOrder(page.request, fixture);
    const scenarios: { name: string; target: SeededWorkspace }[] = [
      // Scenario A — the baseline's best case: the target is already a card.
      { name: 'recent-workspace', target: byRecency[0] },
      // Scenario B — any other workspace, past the recent-card slice.
      { name: 'older-workspace', target: byRecency[byRecency.length - 1] }
    ];
    expect(byRecency.length).toBeGreaterThan(HOME_RECENT_CARD_LIMIT);

    const report: Record<string, unknown> = {
      fixtureWorkspaces: FIXTURE_WORKSPACE_COUNT,
      homeRecentCardLimit: HOME_RECENT_CARD_LIMIT,
      cohortSize: COHORT_SIZE,
      scenarios: {}
    };

    for (const scenario of scenarios) {
      const journeys: number[] = [];
      const markers: number[] = [];
      const paths: string[] = [];

      for (let i = 0; i < COHORT_SIZE; i++) {
        const marker = captureTTFAMarker(page);
        const run = await runJourney(page, scenario.target);
        journeys.push(run.ms);
        paths.push(run.path);
        const markerMs = marker.value();
        if (markerMs !== null) markers.push(markerMs);
      }

      (report.scenarios as Record<string, unknown>)[scenario.name] = {
        targetName: scenario.target.name,
        pathTaken: Array.from(new Set(paths)),
        journeyMs: journeys,
        journeyMedianMs: median(journeys),
        ttfaMarkerMs: markers,
        ttfaMarkerMedianMs: markers.length ? median(markers) : null
      };

      // Guard the harness itself: a zero median means the journey did not
      // actually run, and the recorded evidence would be worthless.
      expect(median(journeys)).toBeGreaterThan(0);
      expect(journeys.length).toBe(COHORT_SIZE);
    }

    // Reported, not asserted: the 25% improvement is a comparison BETWEEN two
    // runs of this harness, so this spec records evidence rather than gating.
    console.info('[home.ttfa.benchmark]', JSON.stringify(report));
  });
});
