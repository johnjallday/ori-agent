import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_PROJECT_HOLDERS_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-one-agent-per-folder');

test('a known folder points at a saved holder without creating anything until its own hire', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(120_000);
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const root = join(sandbox, 'Downloads');
  for (const [kind, count, marker] of [
    ['Software', 7, 'go.mod'],
    ['Songs', 5, 'Song.rpp'],
    ['Writing', 3, 'outline.md']
  ] as const) {
    for (let n = 0; n < count; n++) {
      const folder = join(root, 'Projects', kind, `Project-${n}`);
      await mkdir(folder, { recursive: true, mode: 0o750 });
      await writeFile(join(folder, marker), 'fixture', { mode: 0o600 });
    }
  }
  await request.post('/api/onboarding/skip');
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'holder-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'holder-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await entry.locator('input').fill(root);
  await entry.getByRole('button').click();
  await page
    .locator('#personalAssistantFolderAgent')
    .getByRole('button', { name: 'Hire & start', exact: true })
    .click();
  const get = async () => (await (await request.get(endpoint)).json()).folder_agents;
  await expect.poll(async () => (await get())[0]?.state).toBe('reported');
  const parent = (await get())[0];
  const before = await (await request.get(`/api/workspaces/${parent.workspace_id}`)).json();
  expect(parent.project_holders.rows).toHaveLength(1);
  expect(parent.project_holders.rows[0].kinds).toBe('7 software projects, 5 songs, 3 manuscripts');
  await page.goto(parent.route);
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await close.isVisible()) await close.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  const holders = report.locator('.folder-agent-card__project-holders');
  await expect(holders).toContainText('Projects inside');
  const setup = holders.getByRole('button', { name: 'Set up Projects', exact: true });
  await setup.click();
  await expect(holders).toContainText('Start an agent on Projects');
  expect(await get()).toHaveLength(1);
  await holders.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(setup).toBeFocused();
  await expect(holders).not.toContainText('Start an agent on Projects');
  expect(await get()).toHaveLength(1);
  await page.screenshot({ path: join(evidence, 'project-holder-cancelled.png'), fullPage: true });
  await setup.click();
  await holders.getByRole('button', { name: 'Hire & start', exact: true }).click();
  await expect.poll(async () => (await get()).length).toBe(2);
  const after = await (await request.get(`/api/workspaces/${parent.workspace_id}`)).json();
  expect(after.agent_instances).toEqual(before.agent_instances);
  expect(after.directory_references).toEqual(before.directory_references);
  expect(after.parent_id).toBe(before.parent_id);
  await expect
    .poll(
      async () =>
        (await get()).find((v: { folder_name: string }) => v.folder_name === 'Projects')?.state
    )
    .toBe('reported');
  await page.screenshot({ path: join(evidence, 'project-holder-hired.png'), fullPage: true });
});
