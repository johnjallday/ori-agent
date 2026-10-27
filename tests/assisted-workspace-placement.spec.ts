import { expect, test } from '@playwright/test';
import {
  attachPlacementFolder as attach,
  placementJSON as json
} from './helpers/assisted-workspace-placement';
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

// Real, no-provider host. Never fabricate create/grant/scan/layout responses.
// Recovery tests explicitly lose a REAL response or abort a layout write;
// every successful operation and recovery read still uses the actual server.
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
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
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

test('review then one map click creates one actual workspace and first result; preview alone is inert', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const source = join(sandbox, 'Downloads');
  // The demo ages files to minute precision; two can still be inside the real
  // settling window near a minute boundary. Make this golden count explicit.
  const settled = new Date(Date.now() - 60_000);
  for (const name of readdirSync(source)) utimesSync(join(source, name), settled, settled);
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
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  await expect(sheet.getByRole('region', { name: 'Before you confirm' })).toContainText(
    'Downloads'
  );
  await expect(
    sheet.getByRole('button', { name: 'Create workspace & scan', exact: true })
  ).toHaveCount(0);
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(sheet).toHaveClass(/assisted-placement-host--map/, { timeout: 30_000 });
  await expect(sheet).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  await expect(sheet.getByLabel('Workspace name')).toHaveCount(0);
  await expect(sheet.getByRole('region', { name: 'Before you confirm' })).toHaveCount(0);
  const controls = page.locator('[data-ws-map-placement-controls]');
  await expect(controls).toContainText('Click open ground or press Enter to create');
  const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
  await canvas.focus();
  await canvas.press('ArrowRight');
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(source, 'Filed'))).toBe(false);
  await page.screenshot({ path: `${evidence}/map-ready-to-create.png`, fullPage: true });
  const bounds = await canvas.boundingBox();
  await canvas.click({ position: { x: bounds!.width * 0.25, y: bounds!.height * 0.65 } });
  const candidate = await page.evaluate(() => ({
    ...(window as any).sessionManager.workspaceBuild.placement.candidate
  }));
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
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
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
  await sheet.getByLabel('Workspace name').fill('Filing desk');
  await sheet.getByLabel('Goal', { exact: true }).fill('음악 — retain this exact goal.');
  await sheet.getByLabel('Goal', { exact: true }).press('Tab');
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await expect(page.locator('.ws-map-placement-preview-name')).toHaveText('Draft · Filing desk');
  await expect(sheet).toBeHidden();
  await page.locator('[data-ws-map-placement-back]').click();
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Filing desk');
  await expect(sheet.getByLabel('Goal', { exact: true })).toHaveValue(
    '음악 — retain this exact goal.'
  );
  await sheet.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
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

test('Escape from placement returns to review; cancellation creates no resources', async ({
  page
}) => {
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  await attach(page, 'Desktop', true);
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await expect(sheet).toBeVisible();
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await expect(sheet).toBeHidden();
  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().press('Escape');
  await expect(sheet).toBeVisible();
  await sheet.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(sheet).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).sessionManager.workspaceBuild)).toBeNull();
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
});

test('two tabs preserve current consent; explicit reload, Back and map view changes retain the same draft without creating', async ({
  page,
  context
}) => {
  const recoveryEvidence = 'tasks/evidence-assisted-workspace-placement/group-4';
  mkdirSync(recoveryEvidence, { recursive: true });
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  await attach(page, 'Desktop');
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  const confirm = sheet.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  const id = await page.evaluate(() => (window as any).sessionManager.workspaceBuild.session.id);
  const conversation = await page.evaluate(
    () =>
      (window as any).sessionManager.workspaceBuild.session.placement_source.target.conversation_id
  );
  const other = await context.newPage();
  other.setDefaultTimeout(15_000);
  try {
    await other.goto('/');
    await other.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await other.locator('#personalAssistantLauncher').click();
    await other.locator('#personalAssistantConversationMenu').click();
    await other.locator('#personalAssistantConversationContinue').click();
    await other.locator(`[data-conversation-id="${conversation}"]`).click();
    await other.getByRole('button', { name: 'Return to workspace draft', exact: true }).click();
    const newer = other.locator('#assistedWorkspacePlacementPanel');
    await expect(
      newer.getByRole('button', { name: 'Create workspace & scan', exact: true })
    ).toBeEnabled({ timeout: 30_000 });
    await newer.getByLabel('Workspace name').fill('Saved in another tab');
    await newer.getByLabel('Goal', { exact: true }).fill('Keep the current goal.');
    await newer.getByLabel(/Also watch/).check();
    await expect(
      newer.getByRole('button', { name: 'Create workspace & scan', exact: true })
    ).toBeEnabled({ timeout: 30_000 });
    await sheet.getByLabel('Workspace name').fill('Local unsaved name');
    await sheet.getByLabel('Workspace name').press('Tab');
    await expect(sheet.getByRole('status')).toContainText('changed in another tab');
    await expect(confirm).toBeDisabled();
    await expect(sheet.getByLabel('Workspace name')).toHaveValue('Local unsaved name');
    const stored = await json(page, `/api/workspaces/build-sessions/${id}`);
    expect(stored.session.draft.name).toBe('Saved in another tab');
    await sheet.getByRole('status').scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${recoveryEvidence}/other-tab-conflict.png`, fullPage: true });
    await sheet
      .getByRole('button', { name: 'Discard local edits & load saved draft', exact: true })
      .click();
    await expect(sheet.getByLabel('Workspace name')).toHaveValue('Saved in another tab');
    await expect(sheet.getByLabel('Goal', { exact: true })).toHaveValue('Keep the current goal.');
    await expect(sheet.getByLabel(/Also watch/)).toBeChecked();
    await expect(confirm).toBeEnabled({ timeout: 30_000 });
  } finally {
    await other.close();
  }
  await sheet.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(sheet).toBeHidden();
  const resume = page.getByRole('button', { name: 'Return to workspace draft', exact: true });
  await expect(resume).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(sheet).toBeVisible();
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Saved in another tab');
  expect(await page.evaluate(() => (window as any).sessionManager.workspaceBuild.session.id)).toBe(
    id
  );
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
  await canvas.focus();
  await canvas.press('ArrowRight');
  const candidate = await page.evaluate(
    () => (window as any).sessionManager.workspaceBuild.placement.candidate
  );
  // Keyboard Back preserves this preview without mouse travel choosing a
  // different hover position on the way to the toolbar.
  await page.locator('[data-ws-map-placement-back]').focus();
  await page.keyboard.press('Enter');
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Saved in another tab');
  await expect(sheet.getByLabel(/Also watch/)).toBeChecked();
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceBuild.placement.candidate)
  ).toEqual(candidate);
  await page.locator('[data-cockpit-view="tree"]').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(sheet).toBeVisible();
  await expect(sheet.getByLabel('Workspace name')).toHaveValue('Saved in another tab');
  await expect(sheet.getByLabel(/Also watch/)).toBeChecked();
  await sheet.getByRole('region', { name: 'Before you confirm' }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${recoveryEvidence}/map-hidden-review.png`, fullPage: true });
  await sheet.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(sheet).toBeHidden();
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
});

test('a lost real confirmation is observed without replay; failed position repair retries only layout', async ({
  page
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const recoveryEvidence = 'tasks/evidence-assisted-workspace-placement/group-4';
  mkdirSync(recoveryEvidence, { recursive: true });
  const before = await json(page, '/api/workspaces');
  let confirmations = 0;
  let positionWrites = 0;
  const inference: string[] = [];
  page.on('request', request => {
    if (request.method() === 'POST' && /\/(turns|ask)$/.test(new URL(request.url()).pathname))
      inference.push(request.url());
  });
  await page.route(/\/api\/workspaces\/build-sessions\/[^/]+\/janitor-prepare$/, async route => {
    confirmations++;
    const real = await route.fetch();
    expect(real.ok()).toBeTruthy();
    // Effects and receipt are real; only delivery of this reply is lost.
    await route.abort('failed');
  });
  await page.route('**/api/workspace-map/layout', async route => {
    const request = route.request();
    if (
      request.method() === 'PATCH' &&
      request.postDataJSON()?.operations?.some((op: any) => op.op === 'set_positions')
    ) {
      positionWrites++;
      if (positionWrites === 1) {
        await route.abort('failed');
        return;
      }
    }
    await route.continue();
  });
  await attach(page, 'Desktop', true);
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  await sheet.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
  await canvas.focus();
  await canvas.press('ArrowRight');
  await canvas.press('Enter');
  const candidate = await page.evaluate(
    () => (window as any).sessionManager.workspaceBuild.placement.candidate
  );
  await expect(sheet.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 30_000
  });
  await expect(sheet).toContainText('Assigned: File Curator');
  await expect(sheet).toContainText('first scan found no eligible files');
  expect(confirmations).toBe(1);
  expect(positionWrites).toBe(0);
  const folders = (await json(page, '/api/workspaces')).folders;
  const created = folders.find((workspace: any) => workspace.name === 'Desktop');
  expect(folders.length).toBe(before.folders.length + 1);
  expect(created.agents).toEqual(['File Curator']);
  const setup = await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`);
  expect(setup.review).toMatchObject({
    state: 'prepared',
    monitoring: false,
    result: { outcome: 'no_eligible', eligible_count: 0 }
  });
  const retry = sheet.getByRole('button', { name: 'Retry position only', exact: true });
  await retry.click();
  await expect(sheet.getByRole('status')).toContainText('position is still not saved');
  expect(positionWrites).toBe(1);
  await page.screenshot({
    path: `${recoveryEvidence}/lost-response-position-failed.png`,
    fullPage: true
  });
  await retry.click();
  await expect(sheet.getByRole('status')).toContainText('position is now saved');
  expect(positionWrites).toBe(2);
  const layout = await json(page, '/api/workspace-map/layout');
  expect(layout.layout.positions[created.id]).toMatchObject(candidate);
  expect(await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`)).toEqual(
    setup
  );
  expect(confirmations).toBe(1);
  await page.screenshot({ path: `${recoveryEvidence}/position-repaired.png`, fullPage: true });
  await sheet.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(sheet).toBeHidden();
  const headline = page.locator('#personalAssistantFolderOfferHeadline');
  await expect(headline).toHaveText('✓ Desktop is ready');
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(
    page.getByRole('button', { name: 'Return to workspace draft', exact: true })
  ).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open workspace', exact: true })).toBeFocused();
  await page.reload();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.open());
  await expect(headline).toHaveText('✓ Desktop is ready');
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(page.locator('#personalAssistantFolderOptionalTask')).toBeHidden();
  expect(confirmations).toBe(1);
  expect(positionWrites).toBe(2);
  expect(await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`)).toEqual(
    setup
  );
  expect(inference).toEqual([]);
  await page.screenshot({
    path: `${recoveryEvidence}/source-completed-history.png`,
    fullPage: true
  });
  const filingWrites: string[] = [];
  page.on('request', request => {
    if (
      request.method() !== 'GET' &&
      new URL(request.url()).pathname.startsWith(`/api/workspaces/${created.id}/file-janitor`)
    )
      filingWrites.push(request.url());
  });
  const filing = page.getByRole('link', { name: 'Open File Janitor', exact: true });
  expect(
    new URL((await filing.getAttribute('href'))!, page.url()).searchParams.get('preparation')
  ).toBe(setup.review.operation_id);
  await filing.click();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Initial scan finished');
  await expect(page.locator('#fileJanitorConsoleBody')).not.toContainText(
    'Not scanned in this session'
  );
  await page.reload();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Initial scan finished');
  expect(await json(page, `/api/workspaces/${created.id}/file-janitor/setup-review`)).toEqual(
    setup
  );
  expect(confirmations).toBe(1);
  expect(positionWrites).toBe(2);
  expect(inference).toEqual([]);
  expect(filingWrites).toEqual([]);
  await page.screenshot({
    path: `${recoveryEvidence}/source-empty-filing-review.png`,
    fullPage: true
  });
});
