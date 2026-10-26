import { expect, test, type Page } from '@playwright/test';
import {
  existsSync,
  readFileSync,
  readdirSync,
  realpathSync,
  mkdirSync,
  writeFileSync,
  utimesSync
} from 'node:fs';
import { join, sep } from 'node:path';
import { tmpdir } from 'node:os';

// Real, no-provider host; never route/mock create, grant, scan or layout APIs.
// Use a FRESH scripts/demo-inbox-folder.sh sandbox, separate from its legacy
// serial compatibility story. No paid model or real user directory is used.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
test.skip(!sandbox, 'requires an isolated demo-inbox-folder.sh sandbox');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
test.beforeEach(async ({ page }) => {
  page.setDefaultTimeout(15_000);
});
const evidence = 'tasks/evidence-assisted-workspace-placement/group-2';

async function json(page: Page, path: string) {
  const response = await page.request.get(path);
  expect(response.ok(), path).toBeTruthy();
  const body = await response.json();
  // Roster map iteration order is not a resource/configuration change.
  if (path === '/api/agents') body.agents.sort((a: any, b: any) => a.name.localeCompare(b.name));
  return body;
}
async function attach(page: Page, folder: string, map = false) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.close));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  if (map) await page.locator('[data-cockpit-view="map"]').click();
  await page.locator('#cockpitPrimaryActionBtn').click();
  await page.locator('.personal-assistant-panel__chip', { hasText: folder }).first().click();
  await expect(page.getByRole('button', { name: 'Place & review', exact: true })).toBeVisible();
}

test.beforeAll(async ({ request }) => {
  expect(realpathSync(sandbox).startsWith(realpathSync(tmpdir()) + sep)).toBeTruthy();
  expect(sandbox).toContain('ori-inbox-demo-');
  expect(realpathSync(join(sandbox, 'Downloads'))).toBe(join(realpathSync(sandbox), 'Downloads'));
  mkdirSync(evidence, { recursive: true });
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  const readiness = await request.get('/api/workspaces/build-sessions/availability');
  expect(await readiness.json()).toMatchObject({ available: false, reason: 'no_model' });
});

test('suggested setup opens a canonical draft and cancellation creates no domain resources', async ({
  page
}) => {
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  await attach(page, 'Desktop');
  await page.getByRole('button', { name: 'Use suggested setup', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await expect(
    sheet.getByRole('button', { name: 'Create workspace & scan', exact: true })
  ).toBeEnabled({ timeout: 30_000 });
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Desktop');
  await expect(sheet.getByLabel(/Also watch/)).not.toBeChecked();
  await expect(sheet).toContainText('File Curator');
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  await page.screenshot({ path: `${evidence}/suggested-review.png`, fullPage: true });
  await sheet
    .getByRole('button', { name: 'Create workspace & scan', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${evidence}/suggested-confirmation.png`, fullPage: true });
  const buildID = await page.evaluate(
    () => (window as any).sessionManager.workspaceBuild.session.id
  );
  await sheet.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(sheet).toBeHidden();
  await page.reload();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.open());
  await page.getByRole('button', { name: 'Return to workspace draft', exact: true }).click();
  await expect(sheet).toBeVisible();
  expect(await page.evaluate(() => (window as any).sessionManager.workspaceBuild.session.id)).toBe(
    buildID
  );
  await sheet.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(sheet).toBeHidden();
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  await page.screenshot({ path: `${evidence}/cancel.png`, fullPage: true });
});

test('map selection is inert; separate confirmation produces one actual workspace and first result', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const source = join(sandbox, 'Downloads');
  const bytes = Object.fromEntries(
    readdirSync(source).map(name => [name, readFileSync(join(source, name)).toString('base64')])
  );
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  const inference: string[] = [];
  page.on('request', request => {
    if (request.method() === 'POST' && /\/(turns|ask)$/.test(new URL(request.url()).pathname))
      inference.push(request.url());
  });
  await attach(page, 'Downloads', true);
  await page.getByRole('button', { name: 'Place & review', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await expect(sheet).toHaveClass(/assisted-placement-host--map/, { timeout: 30_000 });
  const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
  await canvas.focus();
  await canvas.press('ArrowRight');
  await canvas.press('Enter');
  await expect(sheet).toContainText('Requested position:');
  const candidate = await page.evaluate(() => ({
    ...(window as any).sessionManager.workspaceBuild.placement.candidate
  }));
  const confirm = sheet.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(source, 'Filed'))).toBe(false);
  await page.screenshot({ path: `${evidence}/map-review.png`, fullPage: true });
  await confirm.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${evidence}/map-confirmation.png`, fullPage: true });
  await confirm.click();
  await expect(sheet.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 60_000
  });
  const workspaces = (await json(page, '/api/workspaces')).folders;
  expect(workspaces.length).toBe(before.folders.length + 1);
  const created = workspaces.find((workspace: any) => workspace.name === 'Downloads');
  expect(created.parent_id || '').toBe('');
  const review = await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`);
  expect(review.review).toMatchObject({
    state: 'prepared',
    monitoring: false,
    terms: { privacy_mode: 'metadata_only' },
    result: { outcome: 'batch', eligible_count: 25 }
  });
  const batches = await json(page, `/api/workspaces/${created.id}/file-janitor/batches`);
  expect(batches.batches).toHaveLength(1);
  expect(batches.batches[0].id).toBe(review.review.result.batch_id);
  const readiness = await json(page, `/api/workspaces/${created.id}/file-janitor/readiness`);
  expect(readiness.readiness.paused).toBe(true);
  expect(created.agents).toEqual(['File Curator']);
  expect(
    created.directory_references.filter((reference: any) => reference.path === source)
  ).toHaveLength(1);
  expect(
    created.mcp_bindings.find((binding: any) => binding.alias === 'downloads_janitor_root')
      .allowed_tools
  ).not.toContain('read_file');
  const layout = await json(page, '/api/workspace-map/layout');
  expect(layout.layout.positions[created.id]).toMatchObject(candidate);
  expect(existsSync(join(source, 'Filed'))).toBe(true);
  for (const [name, content] of Object.entries(bytes))
    expect(readFileSync(join(source, name)).toString('base64')).toBe(content);
  expect(inference).toEqual([]);
  await expect(sheet).toContainText('requested position is saved');
  await page.screenshot({ path: `${evidence}/map-result.png`, fullPage: true });
});

test('editable slots stage a new agent, assign a saved agent and replace the folder without a model', async ({
  page
}) => {
  const editingEvidence = 'tasks/evidence-assisted-workspace-placement/group-3';
  mkdirSync(editingEvidence, { recursive: true });
  const inference: string[] = [];
  const edits: string[] = [];
  page.on('request', request => {
    if (/\/(turns|ask)(?:\?|$)/.test(request.url())) inference.push(request.url());
    if (request.method() !== 'GET' && /\/(draft|folder|janitor-prepare)$/.test(request.url()))
      edits.push(request.url());
  });
  const folder = join(sandbox, 'placement-replacement');
  mkdirSync(folder, { mode: 0o750 });
  const incompatible = join(sandbox, 'placement-code-project');
  mkdirSync(incompatible, { mode: 0o750 });
  writeFileSync(join(incompatible, 'go.mod'), 'module example.com/synthetic\n', { mode: 0o600 });
  const file = join(folder, 'receipt.pdf');
  writeFileSync(file, 'synthetic receipt, unchanged', { mode: 0o600 });
  const old = new Date(Date.now() - 3_600_000);
  utimesSync(file, old, old);
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  const saved = await json(page, '/api/agents/Journal/detail');
  await attach(page, 'Desktop');
  await page.getByRole('button', { name: 'Use suggested setup', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await sheet.getByRole('button', { name: 'Choose agent', exact: true }).click();
  const roster = page.locator('#workspaceRoleRoster');
  await roster.getByRole('button', { name: 'Clear File Curator', exact: true }).click();
  await roster
    .getByRole('button', { name: 'Create an agent for File Curator', exact: true })
    .click();
  const form = page.locator('#addAgentModal');
  await expect(form).toBeVisible();
  await form.locator('[data-agent-create-field=name]').fill('Staged Curator');
  await form.getByRole('button', { name: 'Add to team', exact: true }).click();
  await expect(form).toBeHidden();
  await page.getByRole('button', { name: 'Return to workspace review', exact: true }).click();
  await expect(sheet).toContainText('Staged Curator');
  expect(await json(page, '/api/agents')).toEqual(profiles);
  await page.screenshot({ path: `${editingEvidence}/staged-agent.png`, fullPage: true });
  await sheet.getByRole('button', { name: 'Browse setup cards', exact: true }).click();
  const journal = sheet
    .getByRole('list', { name: 'Available setup cards' })
    .getByRole('button', { name: /^Journal / });
  await expect(journal).toBeEnabled();
  const staleKey = await journal.getAttribute('data-placement-card');
  // Both ends must be visible before pointer-down; scrolling between down and
  // move cancels Chromium's native drag rather than exercising a drop.
  await journal.evaluate(element => element.scrollIntoView({ block: 'center' }));
  await journal.dragTo(sheet.getByRole('region', { name: 'File Curator role', exact: true }));
  await expect(sheet.getByRole('region', { name: 'File Curator role', exact: true })).toContainText(
    'Journal · saved agent'
  );
  await expect(
    sheet.getByRole('button', { name: 'Create workspace & scan', exact: true })
  ).toBeEnabled({ timeout: 30_000 });
  for (const payload of [staleKey, '{"agent":"Journal","path":"/arbitrary"}']) {
    const beforeEdits = edits.length;
    const data = await page.evaluateHandle(value => {
      const data = new DataTransfer();
      data.setData('application/x-ori-placement-card', value!);
      return data;
    }, payload);
    await sheet
      .getByRole('region', { name: 'File Curator role', exact: true })
      .dispatchEvent('drop', { dataTransfer: data });
    await expect(sheet.getByRole('status')).toContainText('Use a current setup card');
    expect(edits.length).toBe(beforeEdits);
  }
  // The ordinary no-drag role controls reach the same canonical assignment.
  await sheet.getByRole('button', { name: 'Choose agent', exact: true }).click();
  await roster.getByRole('button', { name: 'Clear File Curator', exact: true }).click();
  await roster
    .getByRole('button', { name: 'Assign an agent to File Curator', exact: true })
    .click();
  await page.getByRole('button', { name: 'Assign Journal to File Curator', exact: true }).click();
  await page.getByRole('button', { name: 'Return to workspace review', exact: true }).click();
  await expect(sheet).toContainText('Journal · saved agent');
  await sheet.getByRole('button', { name: 'Place on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await sheet.getByLabel('Workspace name').fill('Filing desk');
  await sheet.getByLabel('Goal', { exact: true }).fill('음악 — retain this exact goal.');
  await sheet.getByLabel('Goal', { exact: true }).press('Tab');
  await expect(page.locator('.ws-map-placement-preview-name')).toHaveText('Draft · Filing desk');
  await sheet.getByRole('button', { name: 'Use suggested setup', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  await sheet.getByRole('button', { name: 'Choose folder', exact: true }).click();
  const chooser = page.locator('#workspaceBuildPane');
  await chooser.getByLabel('or paste a folder path', { exact: true }).fill(folder);
  await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText(folder);
  await sheet.getByRole('button', { name: 'Choose folder', exact: true }).click();
  await chooser.getByLabel('or paste a folder path', { exact: true }).fill(incompatible);
  await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
  await expect(sheet.getByRole('status')).toContainText('different kind of project');
  // Refusal returns the retained canonical draft. Reopen and cancel the in-app
  // chooser separately; this is not a native dialog observation.
  await sheet.getByRole('button', { name: 'Choose folder', exact: true }).click();
  await chooser.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(sheet.getByRole('region', { name: 'Linked folder', exact: true })).toContainText(
    folder
  );
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Filing desk');
  await sheet.getByRole('status').scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${editingEvidence}/refused-folder.png`, fullPage: true });
  const cards = sheet.getByRole('list', { name: 'Available setup cards' });
  const desktop = cards.getByRole('button', { name: /^Desktop / });
  await expect(desktop).toBeEnabled();
  await desktop.evaluate(element => element.scrollIntoView({ block: 'center' }));
  await desktop.dragTo(sheet.getByRole('region', { name: 'Linked folder', exact: true }));
  await expect(sheet.getByRole('region', { name: 'Linked folder', exact: true })).toContainText(
    join(sandbox, 'Desktop')
  );
  await cards.getByRole('button', { name: /^placement-replacement / }).click();
  await expect(
    sheet.getByRole('button', { name: 'Use selected folder', exact: true })
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(sheet.getByRole('region', { name: 'Linked folder', exact: true })).toContainText(
    folder
  );
  await sheet.getByRole('button', { name: 'Hide setup cards', exact: true }).click();
  await sheet.getByLabel(/Also watch/).check();
  const confirm = sheet.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  await expect(sheet).toContainText('daily catch-up at 09:00');
  await expect(sheet.getByRole('region', { name: 'Before you confirm' })).toContainText(
    '음악 — retain this exact goal.'
  );
  await confirm.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${editingEvidence}/edited-confirmation.png`, fullPage: true });
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(folder, 'Filed'))).toBe(false);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  await confirm.click();
  await expect(sheet.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 60_000
  });
  const workspaces = (await json(page, '/api/workspaces')).folders;
  const created = workspaces.find((workspace: any) => workspace.name === 'Filing desk');
  expect(workspaces.length).toBe(before.folders.length + 1);
  expect(created.agents).toEqual(['Journal']);
  expect(created.directory_references.some((reference: any) => reference.path === folder)).toBe(
    true
  );
  const review = await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`);
  expect(review.review).toMatchObject({
    state: 'prepared',
    monitoring: true,
    result: { eligible_count: 1 }
  });
  const after = await json(page, '/api/agents/Journal/detail');
  for (const field of ['model', 'temperature', 'system_prompt', 'allow_web_search', 'appearance'])
    expect(after[field]).toEqual(saved[field]);
  expect(readFileSync(file, 'utf8')).toBe('synthetic receipt, unchanged');
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  const detail = await json(page, `/api/workspaces/${created.id}`);
  expect(detail.shared_data.workspace_bootstrap.goal).toBe('음악 — retain this exact goal.');
  expect(inference).toEqual([]);
  // The roster's file-watched origin projection trails the real assignment.
  // Let that pre-existing result settle before the next cancellation snapshot;
  // retain every field in the subsequent no-mutation comparison.
  await expect
    .poll(
      async () => {
        const roster = await json(page, '/api/agents');
        return roster.agents
          .find((agent: any) => agent.name === 'Journal')
          ?.origin?.customised_in?.some((workspace: any) => workspace.id === created.id);
      },
      { timeout: 10_000 }
    )
    .toBe(true);
  await page.screenshot({ path: `${editingEvidence}/edited-result.png`, fullPage: true });
});

test('cancelling on the map closes the hidden editor without reopening it or creating resources', async ({
  page
}) => {
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  await attach(page, 'Desktop', true);
  await page.getByRole('button', { name: 'Place & review', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await sheet.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(sheet).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).sessionManager.workspaceBuild)).toBeNull();
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
});
