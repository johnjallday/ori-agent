import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

// Home's "Explore a folder" starts an agent on the folder: one card, Start,
// a receipt, then a report the host wrote from its own walk of the folder.
// The sandbox has no model, so the report is the host's alone and says so.
// Nothing here is stubbed: the folder on disk, the anchored workspace, the
// agent, the walk, the report and Needs you are all real.
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_FOLDER_AGENT_SANDBOX tests/folder-agent-explore.spec.ts

const sandbox = process.env.ORI_FOLDER_AGENT_SANDBOX || '';

test('Explore a folder starts an agent and reports what it found without a model', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_FOLDER_AGENT_SANDBOX');
  // Missing known folders must not block this ordinary folder. The sandbox
  // never borrows the developer's real Downloads directory.
  const folder = join(sandbox, 'Music', 'One Song');
  await mkdir(join(folder, 'Audio'), { recursive: true, mode: 0o750 });
  await writeFile(join(folder, 'Song.rpp'), '<REAPER_PROJECT\n>\n', { mode: 0o600 });
  await writeFile(join(folder, 'Audio', 'kick.wav'), 'x', { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-agent-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me with my songs.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-agent-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const reusable = await request.post('/api/agents', {
    data: { name: 'Folder Reader', catalog_role: 'orchestrator' }
  });
  expect(reusable.ok(), await reusable.text()).toBeTruthy();

  const starts: string[] = [];
  page.on('request', req => {
    if (req.url().includes('/folder-digest/scan')) throw new Error('the digest must not be called');
    if (req.url().endsWith('/folder-agent/start')) starts.push(req.postData() || '');
  });

  if (process.env.ORI_HIRE_CARD_EVIDENCE)
    await page.setViewportSize({ width: 1440, height: 1200 });
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await expect(entry).toBeVisible();
  await entry.locator('input').fill(folder);
  await entry.getByRole('button').click();

  const host = page.locator('#personalAssistantFolderAgent');
  await expect(host.locator('.folder-agent-card__title')).toHaveText('Hire an agent for One Song');
  await expect(host.locator('.folder-agent-card__brief')).toHaveValue(
    "Find out what's here and set it up"
  );
  await expect(host).toContainText('Nothing is built until you hire.');
  // The Hire step: who is hired, before anything exists.
  await expect(host.locator('.folder-agent-card__who-title')).toHaveText('Hire');
  await expect(host.locator('.folder-agent-card__who-name')).toHaveValue('One Song Manager');
  const start = host.locator('[data-folder-agent-action="start"]');
  await expect(start).toHaveText('Hire & build');
  if (process.env.ORI_HIRE_CARD_EVIDENCE)
    await host.screenshot({ path: `${process.env.ORI_HIRE_CARD_EVIDENCE}/words-new.png` });
  const reuse = host.getByRole('radio', { name: /^Folder Reader/ });
  await reuse.focus();
  await page.keyboard.press('Space');
  await expect(reuse).toBeChecked();
  await expect(start).toHaveText('Assign & build');
  if (process.env.ORI_HIRE_CARD_EVIDENCE)
    await host.screenshot({ path: `${process.env.ORI_HIRE_CARD_EVIDENCE}/words-reuse.png` });
  await host.locator('.folder-agent-card__who-name').click();
  await expect(start).toHaveText('Hire & build');
  const before = await page.request.get('/api/personal-assistant/folder-agent');
  expect((await before.json()).folder_agents).toHaveLength(0);
  await host.locator('.folder-agent-card__brief').fill('Help me finish this song');
  // Role waits behind Customize until it has text.
  await host.locator('.folder-agent-card__who-customize-toggle').click();
  await host.locator('.folder-agent-card__who-role').fill('Help me finish songs');
  await host.locator('[data-folder-agent-action="start"]').click();

  // Hire & build sends the server's choice token, the brief and the hire,
  // never the path.
  await expect.poll(() => starts.length).toBe(1);
  expect(starts[0]).not.toContain(folder);
  const sent = JSON.parse(starts[0]);
  expect(sent.brief).toBe('Help me finish this song');
  expect(sent.hire).toMatchObject({ name: 'One Song Manager', mandate: 'Help me finish songs' });

  // Current Home presents the full report in the Commander's map balloon;
  // the drawer carries only its Show on map line (#801).
  await expect(host.getByRole('button', { name: 'Show on map' })).toBeVisible();
  await host.getByRole('button', { name: 'Show on map' }).click();
  const report = page.locator('.folder-agent-card--report');
  await expect(report).toBeVisible({ timeout: 20_000 });
  await expect(report.locator('.folder-agent-card__facts')).toHaveText('1 REAPER song');
  // The step list replaces Ori's one-line "Set it up: …" summary.
  await expect(report.locator('.folder-agent-card__stage')).toHaveCount(0);
  await expect(report).toContainText('One Song Manager could not look itself');
  await expect(report).toContainText('Search for more tools needs a model');
  // Without a model the steps are Ori's default: the one REAPER step, ticked,
  // and no reason line (there was no agent plan to fall back from).
  await expect(
    report.locator('.folder-agent-card__run').getByRole('heading', { name: 'Steps', exact: true })
  ).toHaveText('Steps');
  await expect(
    report.locator('[data-step="reaper-tools"] .folder-agent-card__line-name').first()
  ).toHaveText(/REAPER tools (to|and connect them to) One Song$/);
  await expect(report.locator('[data-step="reaper-tools"] input')).toBeChecked();
  await expect(report.locator('.folder-agent-card__run-head')).toHaveText('Plan · 1 step');
  if (process.env.ORI_HIRE_CARD_EVIDENCE)
    await report.screenshot({ path: `${process.env.ORI_HIRE_CARD_EVIDENCE}/words-report.png` });
  await expect(report.locator('.folder-agent-card__run')).not.toContainText("Ori's default plan");
  // The primary button counts the plugins it installs: one while the REAPER
  // tools are not in place (the sandbox's usual state).
  await expect(report.locator('[data-folder-agent-action]')).toHaveText([
    /^(Install 1 & set up|Set it up)$/,
    'Keep the folder as it is',
    'Look further'
  ]);

  // The workspace is anchored on the folder with its one agent, and the
  // first ticket carries the host's facts.
  const agents = (await (await request.get('/api/personal-assistant/folder-agent')).json())
    .folder_agents;
  expect(agents).toHaveLength(1);
  expect(agents[0].state).toBe('reported');
  expect(agents[0].agent_name).toBe('One Song Manager');
  expect(agents[0].brief).toBe('Help me finish this song');
  const workspace = (await (await request.get('/api/workspaces')).json()).folders.find(
    (row: { id: string }) => row.id === agents[0].workspace_id
  );
  expect(workspace?.shape).toBe('anchored');

  // Needs you carries the same report.
  const today = (await (await request.get('/api/personal-assistant/today')).json()).today;
  expect(
    today.needs_you.items.some(
      (item: { kind: string; workspace: string }) =>
        item.kind === 'folder_agent_report' && item.workspace === 'One Song'
    )
  ).toBeTruthy();

  // Keep the folder as it is: the report is put aside, Set it up stays.
  await report.getByRole('button', { name: 'Keep the folder as it is' }).click();
  await expect(report.locator('[data-folder-agent-action]')).toHaveText([
    /^(Install 1 & set up|Set it up)$/,
    'Look further'
  ]);
});
