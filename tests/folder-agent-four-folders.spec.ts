import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, readdir, readFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_FOUR_FOLDERS_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-one-agent-per-folder');
async function snapshot(root: string, relative = ''): Promise<string[]> {
  const rows: string[] = [];
  for (const item of await readdir(join(root, relative), { withFileTypes: true })) {
    const name = join(relative, item.name);
    if (item.isDirectory()) rows.push(...(await snapshot(root, name)));
    else
      rows.push(
        `${name}:${createHash('sha256')
          .update(await readFile(join(root, name)))
          .digest('hex')}`
      );
  }
  return rows.sort();
}

test('four synthetic fed folders run their defaults with four agents and unchanged files', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(150_000);
  execFileSync('bash', ['scripts/folder-agent-fixtures.sh', sandbox], {
    env: { ...process.env, TMPDIR: dirname(sandbox) },
    stdio: 'pipe'
  });
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'four-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'four-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  for (const folder of ['Desktop', 'Downloads', 'Documents', 'Music']) {
    const root = join(sandbox, folder);
    const before = await snapshot(root);
    await page.goto('/?panel=today&folder=show');
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await entry.locator('input').fill(root);
    await entry.getByRole('button').click();
    await page
      .locator('#personalAssistantFolderAgent')
      .getByRole('button', { name: 'Hire & start', exact: true })
      .click();
    const get = async () =>
      (await (await request.get(endpoint)).json()).folder_agents.find(
        (v: { folder_name: string }) => v.folder_name === folder
      );
    await expect.poll(async () => (await get())?.state, { timeout: 20_000 }).toBe('reported');
    const view = await get();
    if (folder === 'Documents') {
      expect(view.plan.selected || []).toEqual([]);
      const kept = await request.post(`${endpoint}/${view.workspace_id}/keep`, { data: {} });
      expect(kept.ok()).toBe(true);
    } else {
      if (folder === 'Music') {
        expect(view.root_policy).toBeUndefined();
        expect(view.plan.selected).toEqual(['group']);
      } else expect(view.plan.selected).toEqual(['file-curator']);
      const setup = await request.post(`${endpoint}/${view.workspace_id}/setup`, {
        data: { plan_digest: view.plan.digest, selected: view.plan.selected }
      });
      expect(setup.ok(), await setup.text()).toBe(true);
      await expect.poll(async () => (await get()).setup?.status).toBe('done');
    }
    const ws = await (await request.get(`/api/workspaces/${view.workspace_id}`)).json();
    expect(ws.agent_instances).toHaveLength(1);
    expect(ws.agent_instances[0].name).toBe(`${folder} Manager`);
    expect(await snapshot(root)).toEqual(before);
  }
  const views = (await (await request.get(endpoint)).json()).folder_agents;
  expect(views).toHaveLength(4);
  await page.goto('/');
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await close.isVisible()) await close.click();
  for (const folder of ['Desktop', 'Downloads', 'Documents'])
    await expect(page.getByRole('button', { name: new RegExp(`^${folder}, `) })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Music group, 2 workspaces', exact: true })
  ).toBeVisible();
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await page.screenshot({ path: join(evidence, 'four-folder-result.png'), fullPage: true });
  expect(JSON.stringify(await (await request.get('/api/agents')).json())).not.toContain(
    'File Curator'
  );
});
