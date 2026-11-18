import { test, expect, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync, lstatSync, realpathSync } from 'node:fs';
import { join, relative, dirname } from 'node:path';
import { seedProjectBrowser } from './helpers/project-library-browser-fixture';
import {
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';
import { preparePage, capture, guardReadTour, contrast } from './helpers/ui-consistency';
import { boundedCanvasControl as bounded, assertCanvasAxe } from './helpers/ui-consistency-canvas';

const location = '.tmp/ui-consistency/group-5-library-host-location.json';
const receipt = '.tmp/ui-consistency/group-5-library-host-prepared.json';
function ownedRoot() {
  const root = process.env.ORI_RESOURCE_LIBRARY_UI_SANDBOX || '';
  expect(root).toBeTruthy();
  expect(realpathSync(root)).toBe(root);
  expect(lstatSync(root).isSymbolicLink()).toBe(false);
  expect(relative(realpathSync('.tmp/ui-consistency'), root).startsWith('..')).toBe(false);
  expect(JSON.parse(readFileSync(location, 'utf8')).sandbox).toBe(root);
  expect(readFileSync(join(root, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  return root;
}
if (process.env.ORI_RESOURCE_LIBRARY_PREPARE === '1') {
  test('prepare only the fresh owned inert portable-library fixture', async ({ request }, info) => {
    test.setTimeout(180_000);
    expect(info.project.name).toBe('chromium');
    const root = ownedRoot();
    const existing = await (await request.get('/api/workspaces')).json();
    expect(existing.folders).toHaveLength(0);
    writeFileSync(receipt, JSON.stringify({ root, phase: 'started' }, null, 2), { mode: 0o600 });
    const fixture = await seedProjectBrowser(request, root, { writable: true });
    const workspace = await (await request.get(`/api/workspaces/${fixture.home_id}`)).json();
    expect(relative(root, realpathSync(workspace.sidecar_path)).startsWith('..')).toBe(false);
    writeFileSync(
      receipt,
      JSON.stringify(
        { root, phase: 'prepared', fixture, sidecar: workspace.sidecar_path },
        null,
        2
      ),
      { mode: 0o600 }
    );
  });
} else {
  let fixture: any, root: string, tree: string, portable: string;
  let beforeApi: unknown;
  let beforeFiles: {
    runtime: ReturnType<typeof resourcePreservationSnapshot>;
    portable: ReturnType<typeof resourcePreservationSnapshot>;
  };
  let violations: string[],
    ready = false;
  async function snapshot(request: APIRequestContext) {
    const paths = [
      `/api/workspaces/${fixture.home_id}`,
      `/api/workspaces/${fixture.home_id}/assistant-program`,
      `${fixture.api}/roots`,
      `${fixture.api}/projects`,
      `${fixture.api}/projects/browser-000`,
      `${fixture.api}/projects/browser-000/sessions`
    ];
    const values: Record<string, unknown> = {};
    for (const path of paths) {
      const response = await request.get(path);
      expect(response.ok()).toBe(true);
      values[path] = await response.json();
    }
    return values;
  }
  test.beforeEach(async ({ request }, info) => {
    ready = false;
    violations = [];
    root = ownedRoot();
    const prepared = JSON.parse(readFileSync(receipt, 'utf8'));
    expect(prepared.root).toBe(root);
    expect(prepared.phase).toBe('prepared');
    fixture = prepared.fixture;
    expect(fixture.home_id).toBe('browser-home');
    expect(fixture.entry_count).toBe(105);
    const workspace = await (await request.get(`/api/workspaces/${fixture.home_id}`)).json();
    expect(workspace.sidecar_path).toBe(prepared.sidecar);
    expect(realpathSync(prepared.sidecar)).toBe(prepared.sidecar);
    tree = dirname(prepared.sidecar);
    expect(relative(root, tree).startsWith('..')).toBe(false);
    expect(tree).toBe(join(root, 'workspace-staging'));
    // Real import copies sidecars into staging. Preserve BOTH those definitions
    // and the original exports, inert provider and ALL fixture source files.
    portable = join(root, 'project-library-browser-fixture');
    beforeApi = await snapshot(request);
    beforeFiles = {
      runtime: resourcePreservationSnapshot(tree),
      portable: resourcePreservationSnapshot(portable)
    };
    journalResourceRead(info, 'before', { root: tree, api: beforeApi, files: beforeFiles });
    ready = true;
  });
  test.afterEach(async ({ page, request }, info) => {
    await page.goto('about:blank');
    if (!ready) return;
    const api = await snapshot(request),
      files = {
        runtime: resourcePreservationSnapshot(tree),
        portable: resourcePreservationSnapshot(portable)
      };
    journalResourceRead(info, 'after', { root: tree, api, files });
    expect(violations).toEqual([]);
    expect(api).toEqual(beforeApi);
    expect(files.runtime.comparable).toEqual(beforeFiles.runtime.comparable);
    expect(files.portable.comparable).toEqual(beforeFiles.portable.comparable);
  });
  for (const theme of ['light', 'dark'] as const)
    for (const width of [320, 390, 768, 1440])
      test(`real 105-project library shared tabs and narrow selection ${theme} ${width}`, async ({
        page
      }, info) => {
        await preparePage(page, theme, width);
        violations = await guardReadTour(page);
        await page.goto(fixture.route);
        await expect(page.locator('#projectLibraryRows tr')).toHaveCount(25);
        await capture(
          page,
          info,
          'team-home-library-populated',
          {
            workspaceId: fixture.home_id,
            fixture:
              'Real imported 105-entry portable repository fixture with inert installed provider; source files are sandbox-only, no agent/model/scan'
          },
          { fullPage: false }
        );
        await expect
          .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
          .toBe(true);
        const search = page.locator('#projectLibrarySearch');
        await bounded(search);
        await search.fill('Zebra');
        await page
          .locator('#projectLibrarySearchForm')
          .getByRole('button', { name: 'Find', exact: true })
          .click();
        await expect(page.locator('#projectLibraryRows tr')).toHaveCount(1);
        const inspect = page.getByRole('button', {
          name: 'Inspect Zebra beyond page four',
          exact: true
        });
        await bounded(inspect);
        await inspect.press('Enter');
        const pane = page.locator('#projectLibraryPane');
        await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Zebra beyond page four');
        await expect(pane).toContainText('catalog only');
        const sessions = pane.getByRole('tab', { name: 'Sessions', exact: true });
        await bounded(sessions);
        await sessions.press('Enter');
        await expect(sessions).toHaveAttribute('aria-selected', 'true');
        await expect(pane).toContainText('No saved goals yet');
        expect((await contrast(sessions)).ratio).toBeGreaterThanOrEqual(4.5);
        await sessions.press('Home');
        await expect(pane.getByRole('tab', { name: 'Overview', exact: true })).toBeFocused();
        await sessions.focus();
        await sessions.press('Enter');
        const plan = pane.getByRole('button', { name: 'Plan a session', exact: true });
        await bounded(plan);
        await page.locator('#projectLibraryPaneTitle').focus();
        await plan.click();
        const dialog = page.getByRole('dialog', { name: 'Plan a studio session', exact: true });
        await expect(dialog.getByLabel('Session goal', { exact: true })).toBeFocused();
        await dialog
          .getByLabel('Session goal', { exact: true })
          .fill('Unsent fixture-only session goal');
        await dialog.getByLabel('Desired outcome', { exact: true }).fill('Never submit this draft');
        const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
        await bounded(cancel);
        await assertCanvasAxe(page, 'dialog[open]');
        await capture(
          page,
          info,
          'team-home-library-session-draft',
          {
            workspaceId: fixture.home_id,
            fixture:
              'Real native session form, fields unsent; no Review/session save/model or source read'
          },
          { fullPage: false }
        );
        await cancel.press('Enter');
        await expect(dialog).toHaveCount(0);
        await expect(plan).toBeFocused();
        await plan.click();
        await expect(dialog.getByLabel('Session goal', { exact: true })).toHaveValue('');
        await page.keyboard.press('Escape');
        await expect(dialog).toHaveCount(0);
        await expect(plan).toBeFocused();
        await assertCanvasAxe(page, '#projectLibraryPanel');
        await capture(
          page,
          info,
          'team-home-library-selected',
          {
            workspaceId: fixture.home_id,
            fixture:
              'Real selected late-page entry and empty saved Sessions metadata; no session creation or source file reads'
          },
          { fullPage: false }
        );
        if (width <= 900) {
          const back = page.locator('#projectLibraryBack');
          await bounded(back);
          await back.press('Enter');
          await expect(inspect).toBeFocused();
          await expect(inspect).toHaveAttribute('aria-pressed', 'true');
        }
      });
}
