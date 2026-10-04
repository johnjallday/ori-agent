import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * The Daily Brief station in My HQ (tasks/prd-assistant-drawer-redesign.md,
 * section H): the building on the HQ map, its entry in the Stations rail, the
 * panel that shows, refreshes and configures the brief, and the direct link.
 *
 * Run against a fresh, isolated server — the first test hires an assistant and
 * builds the HQ, which cannot be undone:
 *
 *   ./scripts/e2e-fresh.sh tests/daily-brief-station.spec.ts
 *
 * The tests share that one HQ, so they run in order. No model is configured in
 * a sandbox; the brief is the deterministic fact-only one, which is enough to
 * prove the panel shows it.
 */

const STATION = '[data-cmd-hq-station="daily-brief"]';
const PANEL = '.ws-cmd-modal-panel.is-daily-brief';

async function buildHQ(request: APIRequestContext): Promise<string> {
  await request.post('/api/onboarding/skip');
  await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } });
  const before = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (before.state === 'needs_hire') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'brief-station-hire',
        if_version: before.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.ok(), await hire.text()).toBeTruthy();
  }
  const hired = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (hired.state !== 'active') {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'brief-station-hq',
        if_version: hired.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.ok(), await hq.text()).toBeTruthy();
  }
  const status = (await (await request.get('/api/personal-hq/status')).json()).status;
  expect(status.valid).toBe(true);
  return String(status.workspace.folder_slug);
}

async function openPanel(page: Page, slug: string) {
  await page.goto(`/workspaces/${slug}?mode=map`);
  await page.locator(STATION).click();
  await expect(page.locator(PANEL)).toBeVisible();
}

// The brief is on screen once the loading and generating placeholders are gone.
async function briefShown(page: Page) {
  await expect(page.locator('[data-brief="body"]')).not.toContainText(/Loading|Generating/, {
    timeout: 30000
  });
  await expect(page.locator('[data-brief="body"]')).not.toBeEmpty();
}

test.describe.serial('Daily Brief station in My HQ', () => {
  let slug = '';

  test('the station is a building in the fourth map slot and a row in the Stations rail', async ({
    page,
    request
  }) => {
    slug = await buildHQ(request);

    await page.goto(`/workspaces/${slug}?mode=map`);
    const station = page.locator(STATION);
    await expect(station).toBeVisible();
    await expect(station).toHaveAttribute('data-station-visual', 'briefing');
    await expect(station.locator('[data-building-variant="briefing"]')).toHaveCount(1);
    await expect(station).toHaveAttribute('aria-label', /^Daily Brief station, /);

    // Listed last, so the three stations that were already there keep their
    // default slots and this one takes the fourth.
    const slots = await page
      .locator('.ws-cmd-map-hq-station')
      .evaluateAll(stations =>
        stations.map(el => [
          el.getAttribute('data-cmd-hq-station'),
          (el as HTMLElement).style.getPropertyValue('--station-y')
        ])
      );
    expect(slots).toEqual([
      ['watchtower', '22.00%'],
      ['email', '39.00%'],
      ['calendar-ops', '56.00%'],
      ['daily-brief', '73.00%']
    ]);

    await page.goto(`/workspaces/${slug}`);
    const row = page.locator(`.ws-cmd-panel.is-hq-stations ${STATION}`);
    await expect(row).toBeVisible();
    await expect(row).toContainText('Daily Brief');
    await row.click();
    await expect(page.locator(PANEL)).toBeVisible();
  });

  test('the panel shows today’s brief with when it was generated and when the next is due', async ({
    page
  }) => {
    await openPanel(page, slug);
    await expect(page.locator(PANEL)).toHaveAttribute('aria-label', 'Daily Brief');
    await expect(page.locator('[data-brief="title"]')).toHaveText('Today');
    await briefShown(page);
    await expect(page.locator('[data-brief="meta"]')).toContainText(/^Generated at .+\. /);
    await expect(page.locator('[data-brief="meta"]')).toContainText(
      /Next brief .+\.|No brief scheduled\./
    );
    // The station's own status follows the brief the panel is showing.
    await expect(page.locator(`${STATION} .ws-cmd-map-hq-station-state`)).toContainText('Ready');

    await page.getByRole('button', { name: 'Close Daily Brief' }).click();
    await expect(page.locator(PANEL)).toBeHidden();
    await expect(page.locator(STATION)).toBeFocused();
  });

  test('Refresh regenerates the brief without ever emptying the panel', async ({ page }) => {
    await openPanel(page, slug);
    await briefShown(page);
    const refresh = page.getByRole('button', { name: 'Refresh' });
    await refresh.click();
    // Generation without a model can finish faster than the disabled state can
    // be observed, so what is asserted is the behaviour that matters: the
    // button comes back, and the brief was never replaced by nothing.
    await expect(refresh).toBeEnabled({ timeout: 30000 });
    await briefShown(page);
    await expect(page.locator('[data-brief="banner"]')).not.toContainText('Refreshing');
  });

  test('Brief settings opens the existing form above the panel and a saved time moves the next brief', async ({
    page,
    request
  }) => {
    await openPanel(page, slug);
    await briefShown(page);
    await page.getByRole('button', { name: 'Brief settings' }).click();
    const modal = page.locator('#homeDailyBriefSettingsModal');
    await expect(modal).toBeVisible();
    await expect(page.locator('#homeDailyBriefTimezone')).not.toHaveValue('');
    await expect(page.locator('#homeDailyBriefHistoryList li').first()).toBeVisible();

    await page.locator('#homeDailyBriefTime').fill('09:30');
    await page.locator('#homeDailyBriefScheduleEnabled').check();
    await page.locator('#homeDailyBriefSettingsForm button[type="submit"]').click();
    await expect(page.locator('[data-brief="meta"]')).toContainText(/Next brief .+ at 9:30/);
    const saved = (await (await request.get('/api/personal-hq/brief/config')).json()).config;
    expect(saved.schedule_time).toBe('09:30');

    await modal.locator('.btn-close').click();
    await expect(modal).toBeHidden();
    await expect(page.getByRole('button', { name: 'Brief settings' })).toBeFocused();
    // The panel is still there underneath.
    await expect(page.locator(PANEL)).toBeVisible();

    // Escape closes the panel and hands focus back to the station.
    await page.keyboard.press('Escape');
    await expect(page.locator(PANEL)).toBeHidden();
    await expect(page.locator(STATION)).toBeFocused();
  });

  test('the direct link opens My HQ with the panel open, then leaves the address bar', async ({
    page
  }) => {
    await page.goto(`/workspaces/${slug}?station=daily-brief`);
    await expect(page.locator(PANEL)).toBeVisible();
    await briefShown(page);
    await expect(page).not.toHaveURL(/station=/);
    // A reload therefore does not reopen it.
    await page.reload();
    await expect(page.locator('#workspaceCommandView')).toBeVisible();
    await expect(page.locator(PANEL)).toHaveCount(0);
  });

  test('a workspace that is not the Personal HQ has no Daily Brief station', async ({
    page,
    request
  }) => {
    const created = await request.post('/api/workspaces', {
      data: { name: 'Plain Workspace', description: 'Not the HQ' }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const plainSlug = String((await created.json()).folder?.folder_slug || '');
    expect(plainSlug).not.toBe('');

    await page.goto(`/workspaces/${plainSlug}?mode=map&station=daily-brief`);
    await expect(page.locator('#workspaceCommandView')).toBeVisible();
    await expect(page.locator('.ws-cmd-opmap')).toBeVisible();
    await expect(page.locator(STATION)).toHaveCount(0);
    await expect(page.locator(PANEL)).toHaveCount(0);
    await expect(page.locator('#homeDailyBriefSettingsModal')).toHaveCount(0);
    await expect(page).not.toHaveURL(/station=/);
  });

  // A workspace imported with its own brief history, while another HQ is
  // designated here, keeps its read-only history panel. Its two reads are
  // answered here because a sandbox has no imported workspace; the page, the
  // panel and its script are the real ones.
  test('a workspace with brief history that is not the HQ keeps its read-only history panel', async ({
    page,
    request
  }) => {
    const created = await request.post('/api/workspaces', {
      data: { name: 'Imported History', description: 'Owns old briefs' }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const folder = (await created.json()).folder;
    const revision = {
      id: 'imported-rev-1',
      workspace_id: folder.id,
      local_date: '2026-09-30',
      content_json: JSON.stringify({ opening_summary: 'An imported brief from before.' })
    };
    await page.route(`**/api/workspaces/${folder.id}/daily-briefs`, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          history: [
            { local_date: revision.local_date, current_revision_id: revision.id, revision_count: 1 }
          ]
        })
      })
    );
    await page.route(`**/api/workspaces/${folder.id}/daily-briefs/${revision.id}`, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ revision })
      })
    );

    await page.goto(`/workspaces/${folder.folder_slug}`);
    const history = page.locator('#workspaceBriefHistoryMount');
    await expect(history).toBeVisible();
    await expect(history.locator('.workspace-brief-history-title')).toHaveText('Daily Briefs');
    const entry = history.locator('.workspace-brief-history-entry');
    await expect(entry).toHaveCount(1);
    // A brand-new workspace has no agents, and its "Create a Commander" setup
    // dialog covers the page, so the entry is opened without a click.
    await entry.evaluate(element => {
      (element as HTMLDetailsElement).open = true;
    });
    await expect(entry).toContainText('An imported brief from before.');
    // Read-only: no station, no refresh, no settings on this page.
    await expect(page.locator(STATION)).toHaveCount(0);
    await expect(history.getByRole('button')).toHaveCount(0);
  });
});
