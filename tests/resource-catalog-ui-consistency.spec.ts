import { test, expect } from '@playwright/test';
import { capture, preparePage, guardReadTour, contrast } from './helpers/ui-consistency';
import {
  assertCanvasAxe,
  boundedCanvasControl as bounded,
  tabToCanvasControl
} from './helpers/ui-consistency-canvas';
import {
  retainedResourceFixture,
  resourceApiSnapshot,
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';

let fixture: Awaited<ReturnType<typeof retainedResourceFixture>>;
let api: Record<string, unknown>;
let files: ReturnType<typeof resourcePreservationSnapshot>;
let violations: string[];
let snapshotReady = false;
test.beforeEach(async ({ request }, info) => {
  snapshotReady = false;
  violations = [];
  fixture = await retainedResourceFixture(request);
  api = await resourceApiSnapshot(request, fixture);
  files = resourcePreservationSnapshot(fixture.workspaceRoot);
  journalResourceRead(info, 'before', { root: fixture.workspaceRoot, files, api });
  snapshotReady = true;
});
test.afterEach(async ({ page, request }, info) => {
  await page.goto('about:blank');
  if (!snapshotReady) return;
  const afterApi = await resourceApiSnapshot(request, fixture);
  const afterFiles = resourcePreservationSnapshot(fixture.workspaceRoot);
  journalResourceRead(info, 'after', {
    root: fixture.workspaceRoot,
    files: afterFiles,
    api: afterApi
  });
  expect(violations).toEqual([]);
  expect(
    afterApi,
    'Catalog read tour must preserve real definitions, catalog and annotations'
  ).toEqual(api);
  expect(
    afterFiles.comparable,
    'preserve complete file inventory and bytes except verified checkpoint publication time/identity/sequence'
  ).toEqual(files.comparable);
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`populated Catalog shared owners and native folder cancellation ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      violations = await guardReadTour(page);
      await page.goto(`/workspaces/${fixture.studio.folder_slug}/catalog`);
      await expect(page.getByRole('heading', { name: 'REAPER Studio', exact: true })).toBeVisible();
      await expect(page.locator('#resourceCatalogPage')).toHaveAttribute('aria-busy', 'false');
      await expect(page.locator('#resourceCatalogList .resource-catalog-row')).toHaveCount(5);
      await capture(
        page,
        info,
        'workspace-catalog-populated',
        {
          workspaceId: fixture.studio.id,
          fixture:
            'Real retained repository fixture catalog; no service invocation, approvals or writes during this tour'
        },
        { fullPage: false }
      );
      const templates = page.getByRole('tab', { name: /^Templates/ });
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
        .toBe(true);
      await tabToCanvasControl(templates);
      await bounded(templates);
      await templates.press('ArrowRight');
      await expect(page.getByRole('tab', { name: /^Scripts/ })).toBeFocused();
      await expect(page.getByRole('tab', { name: /^Scripts/ })).toHaveAttribute(
        'aria-selected',
        'true'
      );
      await page.keyboard.press('End');
      await expect(page.getByRole('tab', { name: /^Audio Plugins/ })).toBeFocused();
      await expect(page.locator('#resourceCatalogEmpty')).toContainText(
        'Needs the application open'
      );
      await page.keyboard.press('Home');
      await expect(templates).toBeFocused();
      await expect(page.locator('#resourceCatalogList .resource-catalog-row')).toHaveCount(5);
      await bounded(page.locator('#resourceCatalogSource'));
      const entry = page.locator('.resource-catalog-row').filter({ hasText: 'Band/Vocals.RPP' });
      await bounded(entry);
      await entry.press('Enter');
      const detail = page.locator('#resourceCatalogDetail');
      await expect(detail.getByRole('heading', { name: 'Vocals', exact: true })).toBeFocused();
      await expect(entry).toHaveAttribute('aria-current', 'true');
      expect(
        (await contrast(entry.locator('.resource-catalog-row-name'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        (await contrast(entry.locator('.resource-catalog-row-context'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await expect(detail).toContainText('Identity: Band/Vocals.RPP');
      const note = detail.getByLabel('Note', { exact: true });
      await expect(note).toHaveValue('Main band vocal setup.');
      await bounded(note);
      await bounded(detail.getByRole('button', { name: 'Save notes', exact: true }));
      await bounded(page.locator('#resourceCatalogSources .modern-btn').last());
      const draft = 'Main band vocal setup.\nUnsent Catalog layout draft — never submit.';
      await note.fill(draft);
      const folderDoor = page.getByRole('button', { name: 'Add a folder', exact: true });
      // Use an actual mouse click, not pre-focus: Safari need not focus buttons.
      await folderDoor.click();
      const folder = page.getByRole('dialog', { name: 'Add a folder', exact: true });
      await expect(folder).toBeVisible();
      await expect(folder.getByLabel('Folder path')).toBeFocused();
      await folder.getByLabel('Folder path').fill('~/Unsent fixture-only folder');
      const radio = folder.getByRole('radio', { name: 'Templates', exact: true });
      const box = (await radio.boundingBox())!;
      expect(box.width).toBe(16);
      expect(box.height).toBe(16);
      const cancel = folder.getByRole('button', { name: 'Cancel', exact: true });
      await tabToCanvasControl(cancel);
      await bounded(cancel);
      await assertCanvasAxe(page, '#resourceCatalogFolder');
      await capture(
        page,
        info,
        'workspace-catalog-folder-draft',
        {
          workspaceId: fixture.studio.id,
          fixture: 'Real native Add folder dialog; path unsent, no folder picker or commit invoked'
        },
        { fullPage: false }
      );
      await cancel.press('Enter');
      await expect(folder).toBeHidden();
      await expect(folderDoor).toBeFocused();
      await expect(note).toHaveValue(draft);
      await folderDoor.click();
      await expect(folder.getByLabel('Folder path')).toHaveValue('');
      await folder.getByLabel('Folder path').fill('~/Second unsent draft');
      await page.keyboard.press('Escape');
      await expect(folder).toBeHidden();
      await expect(folderDoor).toBeFocused();
      await expect(note).toHaveValue(draft);
      await note.fill('Main band vocal setup.');
      await bounded(entry);
      await assertCanvasAxe(page, '#resourceCatalogContent');
      await capture(
        page,
        info,
        'workspace-catalog-selected-entry',
        {
          workspaceId: fixture.studio.id,
          fixture:
            'Real retained selected entry and unsaved-draft cancellation; exact API/file readback follows'
        },
        { fullPage: false }
      );
    });

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440])
    for (const kind of ['overview', 'entries'])
      test(`Catalog ${kind} unavailable GET presentation preserves the workspace door ${theme} ${width}`, async ({
        page
      }, info) => {
        await preparePage(page, theme, width);
        violations = await guardReadTour(page);
        const suffix = kind === 'overview' ? '' : '/entries?*';
        await page.route(
          `**/api/workspaces/${fixture.studio.id}/resource-catalog${suffix}`,
          async route => {
            if (route.request().method() !== 'GET') return route.fallback();
            await route.fulfill({
              status: 503,
              contentType: 'application/json',
              body: '{"error":"Presentation-only GET failure projection"}'
            });
          }
        );
        await page.goto(`/workspaces/${fixture.studio.folder_slug}/catalog`);
        if (kind === 'overview') {
          await expect(page.locator('#resourceCatalogStatus')).toHaveText(
            'This workspace has no catalog, or it is unavailable.'
          );
          await expect(page.locator('#resourceCatalogContent')).toBeHidden();
          await expect(page.getByRole('heading', { name: 'Catalog', exact: true })).toBeVisible();
        } else {
          await expect(page.locator('#resourceCatalogEmpty')).toHaveText(
            'Entries could not be loaded. Try selecting the category again.'
          );
          await expect(page.locator('#resourceCatalogList .resource-catalog-row')).toHaveCount(0);
          await expect(
            page.getByRole('heading', { name: 'REAPER Studio', exact: true })
          ).toBeVisible();
        }
        const back = page.locator('#resourceCatalogWorkspaceLink');
        await bounded(back);
        await expect(back).toHaveAttribute('href', `/workspaces/${fixture.studio.folder_slug}`);
        await assertCanvasAxe(page, '#resourceCatalogPage');
        await capture(
          page,
          info,
          `workspace-catalog-${kind}-unavailable`,
          {
            workspaceId: fixture.studio.id,
            fixture: `Presentation-only failed ${kind} GET; real fixture remains available and unchanged. No fabricated mutation.`
          },
          { fullPage: false }
        );
      });
