import { test, expect } from '@playwright/test';
import { lstat, mkdir, realpath, symlink, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { tmpdir } from 'node:os';
import { installLocalCdn } from './helpers/offline-cdn';

test('the confirmed guided hire returns only its own Documents evidence', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox, 'requires an isolated HOME');
  const home = await realpath(sandbox!);
  expect(home.startsWith((await realpath(tmpdir())) + '/')).toBeTruthy();
  expect(basename(home)).toMatch(/^ori-(demo|e2e)\./);
  // The sandbox also serves as ORI_DATA_DIR. Stage the physical Documents
  // tree separately rather than exempting application state from the guard.
  const docs = info.outputPath('Documents');
  await mkdir(join(docs, 'Thesis'), { recursive: true });
  const empty = process.env.ORI_DOCUMENTS_EMPTY === '1';
  if (!empty)
    await writeFile(join(docs, 'Thesis', 'main.tex'), 'fixture manuscript\n', { mode: 0o600 });
  await symlink(docs, join(home, 'Documents'), 'dir');
  const post = async (url: string, data: unknown) => {
    const r = await request.post(url, { data });
    expect(r.ok(), url).toBeTruthy();
    return r.json();
  };
  await post('/api/onboarding/skip', {});
  const { default_workspace_root: root } = await (
    await request.get('/api/settings/workspace-root')
  ).json();
  await post('/api/settings/workspace-root', { workspace_root: root });
  const assistant = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const before = await assistant();
  expect(before.state).toBe('needs_hire');
  await post('/api/personal-assistant/hire', {
    request_id: 'documents-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Help with my notes.',
    focus_areas: ['plan_my_day']
  });
  await post('/api/personal-assistant/hq', {
    request_id: 'documents-hq',
    if_version: (await assistant()).state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: false }
  });
  const { status } = await (await request.get('/api/personal-hq/status')).json();
  await installLocalCdn(page);
  await page.goto(`/workspaces/${encodeURIComponent(status.workspace.folder_slug)}?mode=details`);
  const card = page.locator('[data-hq-firstvisit]');
  await card.getByRole('button', { name: /Explore Documents/ }).click();
  await card.getByRole('link', { name: 'Show me where to start' }).click();
  const attach = page.getByRole('button', { name: 'Attach', exact: true });
  await expect(attach).toBeFocused({ timeout: 20000 });
  await attach.press('ArrowDown');
  await page.getByRole('menuitem', { name: 'Add folder', exact: true }).press('Enter');
  await page.locator('#personalAssistantFolderChips [data-chip="documents"]').click();
  await page.locator('[data-folder-agent-action="start"]').click();
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant/folder-agent')).json())
          .folder_agents?.[0]?.state,
      { timeout: 30000 }
    )
    .toBe('reported');
  await page.locator('#personalAssistantPanel [data-guided-return]').click();
  const receipt = page.locator('[data-guided-receipt]');
  if (empty) {
    await expect(receipt).toContainText('first look at Documents is ready');
    await expect(receipt.locator('details')).toHaveCount(0);
    await expect(receipt.getByRole('link', { name: 'Open folder workspace' })).toBeVisible();
    await page.screenshot({ path: info.outputPath('documents-empty.png'), animations: 'disabled' });
    await expect(lstat(join(root, 'My Notes'))).rejects.toMatchObject({ code: 'ENOENT' });
    return;
  }
  await expect(receipt).toContainText('I found a manuscript in Documents/Thesis');
  await receipt.getByText('Review Documents/Thesis').click();
  await expect(receipt.getByText(/LaTeX manuscript/)).toBeVisible();
  await expect(receipt.getByRole('link', { name: 'Open folder workspace' })).toBeVisible();
  await page.screenshot({ path: info.outputPath('documents-offer.png'), animations: 'disabled' });
  await expect(lstat(join(root, 'My Notes'))).rejects.toMatchObject({ code: 'ENOENT' });
});
