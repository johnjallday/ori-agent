import { test, expect, type Page, type TestInfo } from '@playwright/test';
import { mkdir, readdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { confirmReviewedCreation } from './helpers/confirm-reviewed-creation';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { choiceInventory, arrivalState } from './helpers/workspace-choice-state';
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
  test.setTimeout(60_000);
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
  async function start(repeatActivation = false, returning = false) {
    await page.goto(base + '?mode=details');
    const card = page.locator('[data-hq-firstvisit]');
    if (checks.length) await page.getByRole('button', { name: 'Things to try' }).click();
    const suggestion = card.getByRole('button', { name: /Create a workspace from Documents/ });
    if (returning) await expect(suggestion).toContainText('Completed');
    else await expect(suggestion).not.toContainText('Completed');
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
  await start();
  await expect(path).toHaveValue(docs);
  await modal.locator('#folderNameInput').fill('Document shelves');
  await modal.locator('#wizardNextBtn').click();
  await expect(role.locator('.ws-role-tag__name')).toHaveText('Documents Manager');
  await modal.locator('#wizardNextBtn').click();
  const confirmed = page.waitForResponse(
    r => r.request().method() === 'POST' && /create-operations\/[^/]+\/confirm$/.test(r.url())
  );
  await confirmReviewedCreation(page);
  const result = await confirmed;
  expect(result.ok(), await result.text()).toBe(true);
  const { id: existingId, folder_slug: slug } = (await result.json()).folder;
  await review.getByRole('button', { name: 'Open workspace', exact: true }).click();
  const current = async () => (await request.get(`/api/workspaces/${existingId}`)).json();
  const ws = await current();
  expect(ws.name).toBe('Document shelves');
  expect(ws.anchor.path).toBe(docs);
  expect(ws.agent_instances).toEqual([
    expect.objectContaining({
      name: 'Documents Manager',
      role_id: 'manager',
      role_source: 'created',
      entry_point: true
    })
  ]);
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', existingId);
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Documents Manager');
  await expect(page.locator('#personalAssistantModelSettings')).toHaveAttribute(
    'href',
    `/workspaces/${slug}?editAgentModel=Documents+Manager`
  );
  await page.screenshot({ path: info.outputPath('manager-chat.png') });
  await page.locator('#personalAssistantClose').click();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await start(false, true);
  await expect(modal.locator('#workspaceAnchorOpenExisting')).toBeVisible();
  await expect(path).toHaveValue(docs);
  expect(checks).toHaveLength(4);
  await modal.locator('#workspaceAnchorOpenExisting').click();
  await assertVisited(page, existingId, info);
  expect((await current()).agent_instances).toEqual(ws.agent_instances);
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

for (const staffed of [false, true]) {
  test(`guided ${staffed ? 'staffed' : 'agentless'} creation recovers the reviewed replacement folder`, async ({
    page,
    request
  }, info) => {
    const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
    test.skip(!sandbox, 'requires isolated HOME');
    const home = await realpath(sandbox!);
    expect(home).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
    await mkdir(join(home, 'Documents'), { recursive: true, mode: 0o750 });
    const name = staffed ? 'Reviewed letters' : 'Reviewed shelves';
    const other = join(home, name);
    await mkdir(other, { mode: 0o750 });
    await writeFile(join(other, 'letter.txt'), 'Original letter.\n', { mode: 0o600 });
    const before = await choiceInventory(request, home);
    await installLocalCdn(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    const implicit: string[] = [],
      confirms: string[] = [];
    page.on('request', r => {
      if (!['POST', 'PUT', 'PATCH', 'DELETE'].includes(r.method())) return;
      const path = new URL(r.url()).pathname;
      if (/\/create-operations\/[^/]+\/confirm$/.test(path)) confirms.push(path);
      if (
        /\/folder-agent(?:\/|$)|\/file-search\/(sources|scan)|\/hire$|\/api\/chat$|\/mission\/run|\/(tool-grants|mcp-bindings)(?:\/|$)/.test(
          path
        )
      )
        implicit.push(path);
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
    await page.locator('#folderNameInput').fill(name);
    await page.screenshot({ path: info.outputPath('replacement-review.png') });
    await page.locator('#wizardNextBtn').click();
    if (!staffed) await page.getByRole('checkbox', { name: 'Create without agents' }).check();
    await page.locator('#wizardNextBtn').click();
    const review = page.locator('#workspaceCreationReviewPanel');
    await expect(review).toContainText(other);
    await expect(review).toContainText(
      staffed ? 'Documents Manager' : 'No agents will be created or attached.'
    );
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
    const open = review.getByRole('button', { name: 'Open workspace', exact: true });
    await expect(open).toBeVisible();
    const id = await page.evaluate(
      () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
    );
    const ws = await (await request.get(`/api/workspaces/${id}`)).json();
    expect(ws.anchor.path).toBe(other);
    const bindings = ws.agent_instances || [];
    expect(bindings).toHaveLength(staffed ? 1 : 0);
    if (staffed)
      expect(bindings[0]).toMatchObject({
        role_id: 'manager',
        role_source: 'created',
        entry_point: true
      });
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
    expect(confirms).toHaveLength(1);
    const after = await choiceInventory(request, home);
    expect(after.hires).toEqual(before.hires);
    expect(after.sources).toEqual(before.sources);
    expect(after.workspaces).toEqual([...before.workspaces, id].sort());
    const reopened = await (await request.get(`/api/workspaces/${id}`)).json();
    expect(reopened.anchor).toEqual(ws.anchor);
    expect(reopened.agent_instances || []).toEqual(bindings);
    expect(await arrivalState(request, id)).toMatchObject({
      roles: 1,
      filled: staffed ? 1 : 0,
      goalRuns: 0,
      pending: false,
      inFlight: false
    });
    expect(await readdir(other)).toEqual(['letter.txt']);
    expect(await readFile(join(other, 'letter.txt'), 'utf8')).toBe('Original letter.\n');
  });
}
