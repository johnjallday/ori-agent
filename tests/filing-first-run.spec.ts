import { expect, test } from '@playwright/test';
import { mkdir, utimes, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Run only against an isolated demo HOME, never the user's folders.
const sandbox = process.env.ORI_FILING_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-filing-first-run');

test('a fed Downloads folder offers Filing and asks permission for that folder once hired', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(120_000);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  const root = join(sandbox, 'Downloads');
  await mkdir(root, { recursive: true, mode: 0o750 });
  const old = new Date(Date.now() - 3_600_000);
  for (let n = 0; n < 36; n++) {
    const path = join(root, `filing-${n}.${['pdf', 'png', 'zip', 'csv', 'txt', 'qqq'][n % 6]}`);
    await writeFile(path, 'synthetic fixture', { mode: 0o600 });
    await utimes(path, old, old);
  }
  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (!assistant.hired) {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'filing-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me understand my folders.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    assistant = (await hire.json()).personal_assistant;
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'filing-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await expect(entry).toBeVisible();
  await entry.locator('input').fill(root);
  await entry.getByRole('button').click();
  const hire = page.locator('#personalAssistantFolderAgent');
  await expect(hire).toContainText('Hire an agent for Downloads');
  await hire.locator('[data-folder-agent-action="start"]').click();
  const get = async () =>
    (await (await request.get(endpoint)).json()).folder_agents.find(
      (view: { folder_name: string }) => view.folder_name === 'Downloads'
    );
  await expect.poll(async () => (await get())?.state).toBe('reported');
  const view = await get();
  await page.goto(`${view.route}?view=details`);
  const closeAssistant = page.getByRole('button', {
    name: 'Close personal assistant',
    exact: true
  });
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  await expect(report).toContainText('File loose files in Downloads (you approve every move)');
  await expect(report).toContainText('Adds filing to Downloads');
  await page.screenshot({ path: join(evidence, '01-downloads-hire.png'), fullPage: true });
  await report.getByRole('button', { name: 'Set it up', exact: true }).click();
  await expect.poll(async () => (await get())?.setup?.status).toBe('done');
  await page.reload();
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  const summary = page.locator('#fileJanitorMount');
  await expect(summary.locator('.fj-card-title')).toHaveText('Filing');
  await expect(summary).not.toContainText('File depot');
  await expect(page.locator('#workspaceNextAction')).toHaveText('Review permission');
  await expect(
    page.getByText('Let Downloads Manager file Downloads', { exact: true })
  ).toBeVisible();
  await page.screenshot({ path: join(evidence, '04-named-folder-ask.png'), fullPage: true });
  const mutations: string[] = [];
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      /file-janitor\/(scan|preview|apply|setup|prepare-review)/.test(req.url())
    )
      mutations.push(req.url());
  });
  await page.locator('#workspaceNextAction').click();
  const console = page.getByRole('dialog', { name: 'Filing', exact: true });
  await expect(console).toBeVisible();
  await expect(console.locator('.fj-console-title')).toHaveText('Filing');
  const permissions = page.getByRole('region', { name: 'Review Filing permissions' });
  await expect(permissions.getByRole('heading')).toHaveText('Allow filing in Downloads');
  await page.screenshot({ path: join(evidence, '05-named-folder-consent.png'), fullPage: true });
  const status = async () =>
    (await (await request.get(`/api/workspaces/${view.workspace_id}/file-janitor`)).json()).status;
  expect((await status()).settings.root_path || '').toBe('');
  expect(mutations).toEqual([]);
  await permissions.getByRole('button', { name: 'Allow filing in Downloads', exact: true }).click();
  await expect(console).toContainText('Your first review is ready. Nothing was moved.');
  expect((await status()).settings.root_path).toBe(root);
  expect(mutations.filter(url => /\/(apply|preview|scan|setup)$/.test(url))).toEqual([]);
  expect(mutations.filter(url => url.endsWith('/prepare-review'))).toHaveLength(1);
  await page.screenshot({ path: join(evidence, '06-first-review.png'), fullPage: true });
});

test('capability-only filing asks for a folder and never prefills access', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const created = await request.post('/api/workspaces', {
    data: { name: 'Filing without a folder' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await created.json()).folder;
  const installed = await request.post(
    `/api/workspaces/${workspace.id}/capabilities/file-janitor/install`,
    { data: {} }
  );
  expect(installed.ok(), await installed.text()).toBeTruthy();
  await page.goto(`/workspaces/${workspace.folder_slug}?view=details`);
  const gate = page.locator('#workspace-detail-task-confirm-modal');
  await expect(gate).toBeVisible();
  await gate.getByRole('button', { name: 'Not now' }).click();
  await expect(gate).toBeHidden();
  await expect(page.getByText('Choose the folder to file', { exact: true })).toBeVisible();
  await page.locator('#workspaceNextAction').click();
  await expect(page.locator('#fileJanitorPath')).toHaveValue('');
  await expect(page.getByLabel('Folder to file', { exact: true })).toBeVisible();
  await page.screenshot({ path: join(evidence, '07-no-attached-folder.png'), fullPage: true });
});
