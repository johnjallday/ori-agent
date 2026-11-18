import { test, expect } from '@playwright/test';
import { preparePage, capture, guardReadTour, contrast } from './helpers/ui-consistency';
import {
  boundedCanvasControl as bounded,
  assertCanvasAxe,
  tabToCanvasControl
} from './helpers/ui-consistency-canvas';
import {
  retainedResourceFixture,
  resourceApiSnapshot,
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';

let fixture: Awaited<ReturnType<typeof retainedResourceFixture>>;
let beforeApi: Record<string, unknown>;
let beforeFiles: ReturnType<typeof resourcePreservationSnapshot>;
let violations: string[];
let ready = false;
async function snapshot(request: any) {
  const records = await resourceApiSnapshot(request, fixture);
  for (const suffix of ['', '/library/roots']) {
    const path = `/api/workspaces/${fixture.home.id}/assistant-program${suffix}`;
    const response = await request.get(path);
    expect(response.ok()).toBe(true);
    records[path] = await response.json();
  }
  return records;
}
test.beforeEach(async ({ request }, info) => {
  ready = false;
  violations = [];
  fixture = await retainedResourceFixture(request);
  beforeApi = await snapshot(request);
  beforeFiles = resourcePreservationSnapshot(fixture.workspaceRoot);
  journalResourceRead(info, 'before', {
    root: fixture.workspaceRoot,
    api: beforeApi,
    files: beforeFiles
  });
  ready = true;
});
test.afterEach(async ({ page, request }, info) => {
  await page.goto('about:blank');
  if (!ready) return;
  const api = await snapshot(request),
    files = resourcePreservationSnapshot(fixture.workspaceRoot);
  journalResourceRead(info, 'after', { root: fixture.workspaceRoot, api, files });
  expect(violations).toEqual([]);
  expect(api).toEqual(beforeApi);
  expect(files.comparable).toEqual(beforeFiles.comparable);
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`Team Home real declaration roster and child navigation ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      violations = await guardReadTour(page);
      await page.goto(`/workspaces/${fixture.home.folder_slug}/assistant`);
      const program: any = beforeApi[`/api/workspaces/${fixture.home.id}/assistant-program`];
      await expect(page.locator('#assistantProgramPage')).toHaveAttribute('aria-busy', 'false');
      await expect(page.locator('#assistantProgramTitle')).toHaveText(
        program.declaration.station_name
      );
      const roles = page.locator('#assistantProgramRoster .assistant-program-role');
      await expect(roles).toHaveCount(1);
      await capture(
        page,
        info,
        'team-home-header',
        {
          workspaceId: fixture.home.id,
          fixture:
            'Real repository Music Home declaration and staffed coordinator; no external provider or execution'
        },
        { fullPage: false }
      );
      expect(
        await page
          .locator('#assistantProgramTitle')
          .evaluate(node => parseFloat(getComputedStyle(node).fontSize))
      ).toBeLessThanOrEqual(32);
      expect(
        await page
          .locator('#assistantProgramTitle')
          .evaluate(node => getComputedStyle(node).fontFamily)
      ).toContain('Fraunces');
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
        .toBe(true);
      const back = page.locator('#assistantProgramWorkspaceLink');
      await tabToCanvasControl(back);
      await bounded(back);
      await expect(back).toHaveAttribute('href', `/workspaces/${fixture.home.folder_slug}`);
      const agent = roles.getByRole('link');
      await bounded(agent);
      expect((await contrast(roles.locator('p'))).ratio).toBeGreaterThanOrEqual(4.5);
      expect((await contrast(agent)).ratio).toBeGreaterThanOrEqual(4.5);
      await expect(page.locator('#projectLibraryPanel')).toBeVisible();
      await expect(page.locator('#projectLibraryStatus')).toHaveText(
        'No library yet. Review the saved project links before choosing a folder.'
      );
      const library = page.locator('#projectLibraryInitialize');
      await bounded(library);
      await capture(
        page,
        info,
        'team-home-roster-library-setup',
        {
          workspaceId: fixture.home.id,
          fixture:
            'Real staffed roster and uninitialized library, not populated-library evidence; no review/initialization/scan invoked'
        },
        { fullPage: false }
      );
      const catalog = page
        .locator('#homeResourceWorkspaces')
        .getByRole('link', { name: 'Open REAPER Studio', exact: true });
      await bounded(catalog);
      await expect(catalog).toHaveAttribute(
        'href',
        `/workspaces/${fixture.studio.folder_slug}/catalog`
      );
      await assertCanvasAxe(page, '#assistantProgramContent');
      await catalog.press('Enter');
      await expect(page.locator('#resourceCatalogTitle')).toHaveText('REAPER Studio');
      await page.goBack();
      await expect(page.locator('#assistantProgramTitle')).toHaveText(
        program.declaration.station_name
      );
      await expect(page.locator('#projectLibraryStatus')).toHaveText(
        'No library yet. Review the saved project links before choosing a folder.'
      );
    });

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440])
    test(`Team Home unavailable GET presentation keeps a truthful workspace door ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      violations = await guardReadTour(page);
      await page.route(`**/api/workspaces/${fixture.home.id}/assistant-program`, async route => {
        if (route.request().method() !== 'GET') return route.fallback();
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: '{"error":"Presentation-only unavailable Home GET"}'
        });
      });
      await page.goto(`/workspaces/${fixture.home.folder_slug}/assistant`);
      const error = page.locator('#assistantProgramError');
      await expect(error).toBeVisible();
      await expect(error).toContainText('Presentation-only unavailable Home GET');
      await expect(page.locator('#assistantProgramContent')).toBeHidden();
      const back = error.getByRole('link', { name: 'Back to workspace', exact: true });
      await bounded(back);
      await expect(back).toHaveAttribute('href', `/workspaces/${fixture.home.folder_slug}`);
      await assertCanvasAxe(page, '#assistantProgramError');
      await capture(
        page,
        info,
        'team-home-unavailable',
        {
          workspaceId: fixture.home.id,
          fixture:
            'Presentation-only failed Home GET; actual owned declaration remains available and API/file readback unchanged. No fabricated writes.'
        },
        { fullPage: false }
      );
    });
