import { expect, test, type Page } from '@playwright/test';
import { existsSync, readFileSync, readdirSync, realpathSync, mkdirSync } from 'node:fs';
import { join, sep } from 'node:path';
import { tmpdir } from 'node:os';

// Real, no-provider host; never route/mock create, grant, scan or layout APIs.
// Use a FRESH scripts/demo-inbox-folder.sh sandbox, separate from its legacy
// serial compatibility story. No paid model or real user directory is used.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
test.skip(!sandbox, 'requires an isolated demo-inbox-folder.sh sandbox');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
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
