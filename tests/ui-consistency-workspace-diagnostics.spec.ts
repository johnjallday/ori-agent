import { test, expect } from '@playwright/test';
import { capture, preparePage, contrast } from './helpers/ui-consistency';
import { assertCanvasAxe, boundedCanvasControl as bounded } from './helpers/ui-consistency-canvas';
import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`Diagnostics shared shell and real internal route checks ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      let smokeRequests = 0;
      // Server.runUISmokeTest only issues internal GETs to health, JS/CSS and
      // this owned workspace's two HTML routes. No agents or providers run.
      await page.route('**/api/diagnostics/ui-smoke-test', async route => {
        if (route.request().method() !== 'POST') return route.fallback();
        expect(route.request().postDataJSON()).toEqual({ workspace_id: seed!.workspace.id });
        smokeRequests++;
        await route.continue();
      });
      await page.goto(`/workspaces/${seed.workspace.folder_slug}/diagnostics`);
      await expect(page.getByRole('heading', { name: 'Diagnostics', exact: true })).toBeVisible();
      await expect(page.locator('#workspace-detail-health-badge')).not.toHaveText('Checking');
      await capture(
        page,
        info,
        'workspace-diagnostics-header',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real owned workspace health; shared header, semantic tokens and unchanged no-execution boundary'
        },
        { fullPage: false }
      );
      const refresh = page.locator('#workspace-detail-health-refresh');
      await bounded(refresh);
      await refresh.press('Enter');
      await expect(refresh).toBeEnabled();
      await expect(page.locator('#workspace-detail-health-badge')).not.toHaveText('Checking');
      await expect(refresh).toBeFocused();
      const smoke = page.locator('#workspace-detail-ui-smoke-test');
      await bounded(smoke);
      const response = page.waitForResponse(
        response =>
          new URL(response.url()).pathname === '/api/diagnostics/ui-smoke-test' &&
          response.request().method() === 'POST'
      );
      await smoke.press('Enter');
      const actual = await response;
      expect(actual.ok()).toBe(true);
      const result = await actual.json();
      expect(result.checks).toHaveLength(5);
      expect(result.status, JSON.stringify(result.checks)).toBe('passed');
      await expect(page.locator('.workspace-detail-ui-smoke-status')).toHaveText(result.status);
      await expect(page.locator('.workspace-detail-ui-smoke-check')).toHaveCount(5);
      await expect(smoke).toBeFocused();
      for (const selector of [
        '.workspace-detail-health-title',
        '.workspace-detail-health-summary',
        '.workspace-detail-health-kicker',
        '.workspace-detail-health-badge',
        '.workspace-detail-ui-smoke-summary',
        '.workspace-detail-ui-smoke-status',
        '.workspace-detail-ui-smoke-check-detail',
        '.workspace-detail-ui-smoke-check-meta'
      ]) {
        expect(
          (await contrast(page.locator(selector).first())).ratio,
          selector
        ).toBeGreaterThanOrEqual(4.5);
      }
      await assertCanvasAxe(page, '#workspace-detail-health-panel');
      const output = page.getByRole('region', { name: 'UI route check results', exact: true });
      await bounded(output);
      await expect(output).toBeFocused();
      expect(await output.evaluate(node => node.clientHeight <= 384 + 2)).toBe(true);
      if (await output.evaluate(node => node.scrollHeight > node.clientHeight)) {
        await output.press('End');
        await expect.poll(() => output.evaluate(node => node.scrollTop)).toBeGreaterThan(0);
      }
      await capture(
        page,
        info,
        'workspace-diagnostics-route-results',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real owned health reads and POST internal GET-only UI route checks; no simulated success or execution',
          status: result.status
        },
        { fullPage: false }
      );
      expect(smokeRequests).toBe(1);
      await check();
    });
