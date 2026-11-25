import { test, expect, type Page, type TestInfo } from '@playwright/test';
import { mkdir, readdir, realpath } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';
let base: string;
test.describe.configure({ mode: 'serial' });
test.beforeAll(async ({ request }) => {
  test.skip(!process.env.ORI_HQ_FIRST_VISIT_SANDBOX, 'requires isolated HOME');
  const home = await realpath(process.env.ORI_HQ_FIRST_VISIT_SANDBOX!);
  expect(home).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
  // A busy port can answer from a different installation. Prove the server's
  // HOME matches the fixture before freshGuidedHQ makes its first write.
  const root = (await (await request.get('/api/settings/workspace-root')).json())
    .default_workspace_root;
  expect(basename(root)).toBe('Ori Workspaces');
  expect(await realpath(dirname(root))).toBe(home);
  base = await freshGuidedHQ(request);
});

test('Documents workspace wizard reviews only on activation and resets on cancel', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox, 'requires an isolated home');
  const home = await realpath(sandbox!);
  expect(home).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
  const docs = join(home, 'Documents');
  await installLocalCdn(page);
  const button = page.locator('#cockpitCreateWorkspaceBtn');
  const modal = page.locator('#addFolderModal');
  const path = modal.locator('#workspaceAnchorPath');
  const checks: string[] = [];
  page.on('request', r => {
    if (r.url().includes('/anchor/check?known=')) checks.push(r.url());
  });
  async function start(repeatActivation = false) {
    await page.goto(base + '?mode=details');
    const card = page.locator('[data-hq-firstvisit]');
    if (checks.length) await page.getByRole('button', { name: 'Things to try' }).click();
    const suggestion = card.getByRole('button', { name: /Create a workspace from Documents/ });
    await expect(suggestion).not.toContainText('Completed');
    await suggestion.click();
    const count = checks.length;
    await card.getByRole('link', { name: 'Go to Home' }).click();
    await expect(button).toBeFocused();
    expect(checks).toHaveLength(count);
    if (repeatActivation) {
      await button.evaluate(el => {
        el.click();
        el.click();
      });
    } else await button.click();
    await expect(page.locator('#hqWorkspaceHint')).toBeHidden();
    await expect(button).toHaveCSS('animation-name', 'none');
    await expect(page.locator('.modal-backdrop')).toHaveCount(1);
    await expect(modal.locator('#workspaceFolderModeAnchored')).toBeChecked();
    await expect(modal.locator('#folderNameInput')).toBeEditable();
  }
  await start();
  await expect(modal.locator('#workspaceAnchorStatus')).toContainText(
    'Documents could not be located'
  );
  await expect(modal.locator('#workspaceAnchorChoose')).toBeEnabled();
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
  await mkdir(docs, { mode: 0o750 });
  // Simulate Files being unavailable; all workspace reads/writes stay real.
  await page.route('**/api/file-search/**', route => route.fulfill({ status: 503 }));
  await start(true);
  await expect(path).toHaveValue(docs);
  await expect(path).toBeEditable();
  await expect(modal.locator('#workspaceSearchConsent')).not.toBeChecked();
  await expect(modal).toHaveCSS('opacity', '1');
  await path.scrollIntoViewIfNeeded();
  await page.locator('#wizardNextBtn').click();
  const role = modal.locator('#workspaceRoleRoster [data-role-id="manager"]');
  await expect(role.locator('.ws-role-row__label')).toHaveText('Documents Manager');
  await page.locator('#wizardNextBtn').click();
  const review = modal.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Documents Manager');
  await expect(review).toContainText(docs);
  await page.screenshot({ path: info.outputPath('documents-review.png'), animations: 'disabled' });
  await review.getByRole('button', { name: 'Edit team' }).click();
  await modal.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  await expect(review).toContainText('No agents will be created or attached.');
  await page.screenshot({ path: info.outputPath('documents-opt-out.png') });
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
  await button.click();
  await expect(modal.locator('#workspaceFolderModeManaged')).toBeChecked();
  await expect(path).toHaveValue('');
  await page.locator('#wizardNextBtn').click();
  await modal.locator('#folderNameInput').fill('Documents');
  await page.locator('#wizardNextBtn').click();
  await expect(role.locator('.ws-role-row__label')).toHaveText('Manager');
  await expect(role).toContainText('Documents Manager');
  await page.screenshot({ path: info.outputPath('ordinary-blank.png') });
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
  const create = await request.post('/api/workspaces', {
    data: { name: 'Document shelves', anchor_path: docs }
  });
  expect(create.ok(), await create.text()).toBeTruthy();
  await start();
  await expect(modal.locator('#workspaceAnchorOpenExisting')).toBeVisible();
  await expect(path).toHaveValue(docs);
  expect(checks).toHaveLength(3);
  const existingId = (await create.json()).folder.id;
  await modal.locator('#workspaceAnchorOpenExisting').click();
  await assertVisited(page, existingId, info);
  expect(await readdir(docs)).toEqual([]);
});

async function assertVisited(page: Page, id: string, info: TestInfo) {
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', id);
  await expect
    .poll(() =>
      page.evaluate(() =>
        Object.keys(localStorage).some(k => k.startsWith('ori.hq.workspaceVisit.'))
      )
    )
    .toBe(true);
  await page.screenshot({ path: info.outputPath('actual-workspace.png'), animations: 'disabled' });
  await page.goto(base + '?mode=details');
  await page.reload();
  await page.getByRole('button', { name: 'Things to try' }).click();
  await expect(
    page.getByRole('button', { name: /Create a workspace from Documents/ })
  ).toContainText('You opened your chosen workspace');
  await expect(page.locator('[data-guided-receipt]')).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('completed.png'), animations: 'disabled' });
}

test('guided creation opens the reviewed replacement folder and confirms arrival', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox, 'requires isolated HOME');
  const home = await realpath(sandbox!);
  expect(home).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
  await mkdir(join(home, 'Documents'), { recursive: true, mode: 0o750 });
  const other = join(home, 'Reviewed folder');
  await mkdir(other, { mode: 0o750 });
  await installLocalCdn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  const implicit: string[] = [];
  page.on('request', r => {
    // Home remembers Map/Files display state via POST; neither is an index.
    if (
      r.method() === 'POST' &&
      /folder-agent\/|\/file-search\/(?!map-place|files-place|desk-place)|\/hire$/.test(r.url())
    )
      implicit.push(r.url());
  });
  await page.goto(base + '?mode=details');
  const card = page.locator('[data-hq-firstvisit]');
  await card.getByRole('button', { name: /Create a workspace from Documents/ }).click();
  await card.getByRole('link', { name: 'Go to Home' }).click();
  const button = page.locator('#cockpitCreateWorkspaceBtn');
  await expect(button).toBeFocused();
  await page.screenshot({ path: info.outputPath('hint.png') });
  await button.press('Enter');
  const path = page.locator('#workspaceAnchorPath');
  await expect(path).toHaveValue(join(home, 'Documents'));
  await path.fill(other);
  await path.blur();
  await expect(page.locator('#workspaceAnchorStatus')).toBeHidden();
  await page.locator('#folderNameInput').fill('Reviewed shelves');
  await page.screenshot({ path: info.outputPath('replacement-review.png') });
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  const open = review.getByRole('button', { name: 'Open workspace', exact: true });
  await expect(open).toBeVisible();
  const id = await page.evaluate(
    () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
  );
  const ws = await (await request.get(`/api/workspaces/${id}`)).json();
  expect(ws.anchor.path).toBe(other);
  expect(await readdir(other)).toEqual([]);
  expect(
    await page.evaluate(() =>
      Object.keys(localStorage).some(k => k.startsWith('ori.hq.workspaceVisit.'))
    )
  ).toBe(false);
  // Recover this exact confirmed operation, without submitting a second create.
  await page.reload();
  await button.click();
  await open.click();
  await assertVisited(page, id, info);
  expect(implicit).toEqual([]);
  const sources = await request.get('/api/file-search/sources', {
    headers: { 'X-Ori-Search': '1' }
  });
  if (sources.ok()) expect((await sources.json()) || []).toEqual([]);
  else expect([404, 503]).toContain(sources.status());
});
