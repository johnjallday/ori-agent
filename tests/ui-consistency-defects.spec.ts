import { test, expect, type APIRequestContext } from '@playwright/test';
import {
  assertSandbox,
  capture,
  contrast,
  createWorkspace,
  removeOwnedWorkspace,
  guardReadTour,
  preparePage,
  reachable,
  readFixture
} from './helpers/ui-consistency';
import { longModelName, modelProviders, themes } from './fixtures/ui-consistency-scenes';

import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';

const ownedStrictSeeds = new Map<string, WorkspaceSeed>();
const ownedWorkspaces = new Map<string, Awaited<ReturnType<typeof createWorkspace>>>();
async function createDefectWorkspace(request: APIRequestContext, testId: string) {
  const workspace = await createWorkspace(request);
  ownedWorkspaces.set(testId, workspace);
  return workspace;
}

test.afterEach(async ({ page, request }, info) => {
  info.setTimeout(30_000);
  const strictSeed = ownedStrictSeeds.get(info.testId);
  if (strictSeed) {
    info.setTimeout(120_000);
    if (!page.isClosed()) await page.close();
    await retireWorkspaceSeed(request, strictSeed);
    ownedStrictSeeds.delete(info.testId);
    return;
  }
  const workspace = ownedWorkspaces.get(info.testId);
  if (!workspace) return;
  if (!page.isClosed()) await page.close();
  await removeOwnedWorkspace(request, workspace);
  ownedWorkspaces.delete(info.testId);
});

test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  // Established-user defect tour. Use the real, explicit skip endpoint; never
  // hide first-use DOM or fake a successful onboarding mutation.
  const skipped = await request.post('/api/onboarding/skip');
  expect(skipped.ok(), await skipped.text()).toBeTruthy();
});

test.beforeEach(async ({ request }) => {
  await assertSandbox(request);
});

for (const theme of themes) {
  test(`Diagnostics readable health and result pairs: ${theme}`, async ({ page }, info) => {
    const seed = await createWorkspaceSeed(page.request);
    ownedStrictSeeds.set(info.testId, seed);
    await freezeWorkspaceSeed(page.request, seed);
    const ws = seed.workspace;
    await preparePage(page, theme);
    // ui_smoke.go runs only internal GET checks; explicit POST, no execution.
    const writes = await guardReadTour(page, ['/api/diagnostics/ui-smoke-test']);
    await page.goto(`/workspaces/${ws.folder_slug}/diagnostics`);
    await expect(page.locator('#workspace-detail-health-badge')).not.toHaveText('Checking');
    await expect(page.locator('#workspace-detail-health-list')).not.toContainText(
      'Checking workspace'
    );
    await capture(page, info, 'diagnostics', { workspace: ws.id, source: 'real local health' });
    const ratios: Record<string, unknown> = {};
    for (const selector of [
      '.workspace-detail-health-title',
      '.workspace-detail-health-summary',
      '.workspace-detail-health-kicker',
      '.workspace-detail-health-badge',
      '.workspace-detail-health-empty-title',
      '.workspace-detail-health-empty-copy'
    ]) {
      const result = await contrast(page.locator(selector));
      ratios[selector] = result;
      expect.soft(result.ratio, selector).toBeGreaterThanOrEqual(4.5);
    }
    await page.locator('#workspace-detail-ui-smoke-test').click();
    await expect(page.locator('.workspace-detail-ui-smoke-check')).toHaveCount(5);
    for (const selector of [
      '.workspace-detail-ui-smoke-summary',
      '.workspace-detail-ui-smoke-status',
      '.workspace-detail-ui-smoke-check-detail',
      '.workspace-detail-ui-smoke-check-meta'
    ]) {
      const result = await contrast(page.locator(selector).first());
      ratios[selector] = result;
      expect.soft(result.ratio, selector).toBeGreaterThanOrEqual(4.5);
    }
    await capture(page, info, 'diagnostics', {
      workspace: ws.id,
      source: 'real local health and internal route checks',
      ratios
    });
    expect(writes).toEqual([]);
  });
}

test('dark Settings search and placeholder use legible theme pairs', async ({ page }, info) => {
  await preparePage(page, 'dark');
  const writes = await guardReadTour(page);
  await page.goto('/settings');
  const search = page.locator('#settingsSearch');
  await expect(search).toBeVisible();
  await capture(page, info, 'settings-search');
  const text = await contrast(search);
  const placeholder = await contrast(search, '::placeholder');
  expect(text.ratio).toBeGreaterThanOrEqual(4.5);
  expect(placeholder.ratio).toBeGreaterThanOrEqual(4.5);
  expect(text.background[0]).toBeLessThan(65);
  await search.focus();
  const focused = await contrast(search, '::placeholder');
  expect(focused.ratio).toBeGreaterThanOrEqual(4.5);
  await capture(page, info, 'settings-search', { text, placeholder, focused });
  expect(writes).toEqual([]);
});

// 720px is the equivalent CSS viewport of 1440px at 200% zoom, not deviceScaleFactor.
for (const width of [320, 390, 720]) {
  test(`Review actions and filters remain reachable at ${width}px`, async ({ page }, info) => {
    await preparePage(page, 'light', width);
    const writes = await guardReadTour(page);
    await page.goto('/review');
    await expect(page.locator('#issuesTable .spinner-border')).toHaveCount(0);
    await expect(page.locator('#runsTable .spinner-border')).toHaveCount(0);
    await capture(page, info, 'review');
    const exportToggle = page.locator('#reviewExportMenu > summary');
    await reachable(exportToggle, 44);
    await exportToggle.click();
    const geometry: Record<string, unknown> = {};
    for (const selector of [
      '#triggerBtn',
      '#filterAgent',
      '#filterType',
      'button[onclick="refreshData()"]',
      '#reviewExportJSON',
      '#reviewExportJSONL'
    ]) {
      geometry[selector] = await reachable(page.locator(selector), 44);
    }
    await capture(page, info, 'review', { geometry });
    expect(writes).toEqual([]);
  });

  test(`Models long identifiers and checkbox targets at ${width}px`, async ({ page }, info) => {
    await preparePage(page, 'light', width);
    const writes = await guardReadTour(page);
    await readFixture(page, '**/api/providers', modelProviders);
    await page.goto('/models');
    await expect(page.locator('#modelsContent')).toBeVisible();
    await expect(page.locator('.model-name')).toHaveText(longModelName);
    await capture(page, info, 'models', {
      source: 'fictional provider GET; no credentials or execution'
    });
    for (const selector of [
      '#manageCategoriesBtn',
      '#refreshModelsBtn',
      '#providerFilter',
      '#categoryFilter',
      '#searchFilter',
      '.model-name'
    ]) {
      await reachable(page.locator(selector));
    }
    const glyph = await page.locator('#showLegacyFilter').boundingBox();
    expect(glyph?.width).toBeLessThanOrEqual(20);
    expect(glyph?.height).toBeLessThanOrEqual(20);
    const labelTarget = await reachable(page.locator('label[for="showLegacyFilter"]'), 44);
    const before = await page.locator('#showLegacyFilter').isChecked();
    await page.locator('label[for="showLegacyFilter"]').click();
    expect(await page.locator('#showLegacyFilter').isChecked()).toBe(!before);
    await capture(page, info, 'models', { glyph, labelTarget, source: 'fictional provider GET' });
    expect(writes).toEqual([]);
  });
}

test('shared labelled radio/checkbox targets preserve normal artwork and range sizing', async ({
  page
}, info) => {
  await preparePage(page, 'light', 390);
  const writes = await guardReadTour(page);
  await page.goto('/agents/create');
  const radio = page.locator('.appearance-editor__sources input[type="radio"]').first();
  await expect(radio).toBeAttached();
  const label = radio.locator('..');
  await reachable(label, 44);
  const glyph = await radio.boundingBox();
  expect(glyph?.width).toBeLessThanOrEqual(20);
  expect(glyph?.height).toBeLessThanOrEqual(20);
  await capture(page, info, 'agent-radio');
  const range = await page.locator('#temperature').boundingBox();
  expect(range?.height).toBeLessThan(44);
  await page.goto('/settings#display-density');
  await expect(page.locator('#uiDensityAuto')).toBeVisible();
  await reachable(page.locator('label[for="uiDensityAuto"]'), 44);
  const density = await page.locator('#uiDensityAuto').boundingBox();
  expect(density?.height).toBeLessThanOrEqual(20);
  await capture(page, info, 'settings-radio');
  // Shared agent modal is also mounted on Models: open via its existing controller,
  // without replacing markup or removing overlays to make the geometry pass.
  await page.goto('/models');
  await page.evaluate(() => {
    const win = window as unknown as {
      bootstrap: { Modal: new (element: Element) => { show(): void } };
    };
    new win.bootstrap.Modal(document.querySelector('#addAgentModal')!).show();
  });
  const checkbox = page.locator('#agentAllowWebSearch');
  await expect(checkbox).toBeVisible();
  await reachable(page.locator('label[for="agentAllowWebSearch"]'), 44);
  expect((await checkbox.boundingBox())?.height).toBeLessThanOrEqual(20);
  await capture(page, info, 'shared-agent-dialog');
  expect(writes).toEqual([]);
});

for (const [theme, width] of [
  ['light', 390],
  ['dark', 1440]
] as const) {
  test(`shared-style sentinel ${theme} ${width}px`, async ({ page }, info) => {
    const ws = await createDefectWorkspace(page.request, info.testId);
    await preparePage(page, theme, width);
    const stored = await page.request.get(`/api/orchestration/workspace?id=${ws.id}`);
    expect(stored.ok()).toBeTruthy();
    expect((await stored.json()).tasks || []).toEqual([]);
    // First-open always asks this endpoint; on this verified empty workspace it
    // is a real no-op. Do not generalize this allowance to template workspaces.
    const setupPath = `/api/workspaces/${ws.id}/template-setup/start`;
    const writes = await guardReadTour(page, [setupPath]);
    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');
    // Existing Home toggle is 32px; group 4 owns its target migration.
    await reachable(page.locator('#cockpitViewTree'));
    await page.locator('#cockpitViewTree').click();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', 'tree');
    await page.locator('#cockpitViewMap').click();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', 'map');
    await capture(page, info, 'sentinel-home');
    const agentsLink = page.locator('.ori-navbar-links a[href="/agents"]');
    await reachable(agentsLink);
    await agentsLink.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/agents$/);
    await expect(page.locator('#rosterList')).toBeVisible();
    await reachable(page.locator('#rosterSearch'));
    await capture(page, info, 'sentinel-agents');
    const setupCheck = page.waitForResponse(
      response => new URL(response.url()).pathname === setupPath
    );
    await page.goto(`/workspaces/${ws.folder_slug}`);
    expect((await (await setupCheck).json()).started).toBe(false);
    await expect(page.locator('[data-cmd-give-work]')).toBeVisible();
    await page.waitForFunction(
      () =>
        (window as unknown as { workspaceDetail?: { initialListsLoaded: boolean } }).workspaceDetail
          ?.initialListsLoaded
    );
    for (const mode of ['tickets', 'map', 'details']) {
      await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
      await expect(page.locator(`[data-cmd-view-mode="${mode}"]`)).toHaveAttribute(
        'aria-pressed',
        'true'
      );
    }
    await page.locator('[data-cmd-give-work]').click();
    const dialog = page.locator('dialog.give-work');
    await expect(dialog).toBeVisible();
    await dialog.locator('[name="what"]').fill('Unsaved fictional draft');
    await reachable(dialog.getByRole('button', { name: 'Cancel', exact: true }));
    await capture(page, info, 'sentinel-give-work');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.locator('[data-cmd-give-work]')).toBeFocused();
    await page.goto('/settings#display-density');
    await expect(page.locator('#uiDensityAuto')).toBeVisible();
    await capture(page, info, 'sentinel-settings');
    expect(writes).toEqual([]);
  });
}
