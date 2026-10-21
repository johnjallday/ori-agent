import { test, expect, Page } from '@playwright/test';

/**
 * The workspace Command header at phone widths: nothing runs off the page,
 * the workspace's name stays readable, and every view tab shows its whole word.
 *
 * Not part of CI. Run against the isolated demo server:
 *   ./scripts/e2e.sh --port 8931 tests/workspace-header-phone.spec.ts -- --workers=1
 *
 * The workspace is real. Two reads are stubbed so the widest header is
 * certain, not timing-dependent:
 * - the surface catalog, to add the Dashboard tab (five views: Details, Map,
 *   Tickets, Dashboard, Plans), since a dashboard otherwise needs a file in the
 *   workspace's folder on the server's disk;
 * - the continuity status, as "ready", so the chip reads "Ready to move as of
 *   <date, time>". A new workspace says "Saving latest work" until Ori prepares
 *   its folder, which can take longer than a test should wait.
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

const DASHBOARD_SURFACE = {
  key: 'user:ori.dashboard:dashboard:main',
  plugin: { id: 'ori.dashboard', version: '1', generation: '1' },
  capability_id: 'dashboard',
  surface_id: 'main',
  label: 'Dashboard',
  description: 'Your own dashboard for this workspace.',
  icon: { kind: 'host', value: 'grid' },
  placement: 'workspace_view',
  modal: { width: 1200, height: 800 },
  status: { state: 'ready', value: 'Ready', description: "This workspace's dashboard is ready." },
  available: true,
  polling: { map_seconds: 60, open_seconds: 60 },
  features: {}
};

// A blank workspace with its default Workspace Manager: without an agent the
// page opens a blocking "Create a Commander" dialog, unrelated to this layout.
async function createWorkspace(page: Page, name: string) {
  const res = await page.request.post('/api/workspaces', {
    data: { name, blank: true, create_template_agents: true }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return (body.folder ?? body.workspace ?? body) as { id: string; folder_slug: string };
}

// The server's own "ready" answer, as GET /api/workspaces/{id}/continuity
// returns it once a folder is prepared.
async function serveReadyContinuity(page: Page, workspaceId: string) {
  await page.route(`**/api/workspaces/${workspaceId}/continuity`, route =>
    route.request().method() === 'GET'
      ? route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            success: true,
            continuity: {
              workspace_id: workspaceId,
              state: 'ready',
              checkpoint_at: '2026-10-21T05:42:48.852722Z',
              attachment: 'native',
              imported: false,
              background_allowed: true,
              manual_allowed: true,
              attachment_version: 1,
              tree_ready: true
            }
          })
        })
      : route.continue()
  );
}

function measureHeader(page: Page) {
  return page.evaluate(() => {
    const rect = (selector: string) =>
      (document.querySelector(selector) as HTMLElement).getBoundingClientRect();
    const title = rect('.ws-cmd-title');
    const name = document.querySelector('.ws-cmd-title h2') as HTMLElement;
    const tabs = [...document.querySelectorAll<HTMLElement>('.ws-cmd-view-switch > *')].map(tab => {
      // The label's own width against the tab's content box.
      const style = getComputedStyle(tab);
      const range = document.createRange();
      range.selectNodeContents(tab);
      const label = range.getBoundingClientRect().width;
      const inner =
        tab.getBoundingClientRect().width -
        parseFloat(style.paddingLeft) -
        parseFloat(style.paddingRight) -
        parseFloat(style.borderLeftWidth) -
        parseFloat(style.borderRightWidth);
      return { text: tab.textContent?.trim(), fits: label <= inner + 0.5 };
    });
    return {
      pageOverflow: document.documentElement.scrollWidth - window.innerWidth,
      titleWidth: title.width,
      nameWidth: name.getBoundingClientRect().width,
      chipRight: rect('.ws-cmd-continuity-mount .workspace-continuity-chip').right,
      titleRight: title.right,
      tabs
    };
  });
}

for (const width of [375, 320]) {
  test(`at ${width}px the header fits the page and keeps the name and every tab readable`, async ({
    page
  }) => {
    await page.setViewportSize({ width, height: 812 });
    await skipOnboarding(page);
    const workspace = await createWorkspace(
      page,
      `Quarterly Campaign Planning ${String(Date.now()).slice(-5)}`
    );
    await page.route(`**/api/workspaces/${workspace.id}/surfaces`, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ surfaces: [DASHBOARD_SURFACE] })
      })
    );
    await serveReadyContinuity(page, workspace.id);

    await page.goto(`/workspaces/${workspace.folder_slug}`);
    await expect(page.locator('[data-cmd-view-mode="dashboard"]')).toBeVisible({ timeout: 20000 });
    await expect(page.locator('.ws-cmd-continuity-mount .workspace-continuity-chip')).toContainText(
      'Ready to move as of'
    );

    const header = await measureHeader(page);
    expect(header.pageOverflow, 'nothing pushes the page sideways').toBeLessThanOrEqual(0);
    expect(header.chipRight, 'the status chip stays in the title column').toBeLessThanOrEqual(
      header.titleRight + 0.5
    );
    expect(header.nameWidth, 'the name has its own line').toBeGreaterThanOrEqual(
      header.titleWidth - 0.5
    );
    expect(header.tabs.map(tab => tab.text)).toEqual([
      'Details',
      'Map',
      'Tickets',
      'Dashboard',
      'Plans'
    ]);
    for (const tab of header.tabs) expect(tab.fits, `${tab.text} shows its whole word`).toBe(true);
  });
}
