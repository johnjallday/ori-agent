import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// ./scripts/e2e-fresh.sh --sandbox-env ORI_KNOWN_FOLDERS_SANDBOX tests/folder-agent-known-folders.spec.ts
const sandbox = process.env.ORI_KNOWN_FOLDERS_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-one-agent-per-folder');

test('Desktop and Documents know their jobs before hire and refuse whole-folder project setup', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(150_000);
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'known-folders-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'known-folders-hq',
      if_version: (await hired.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  for (const [folder, brief, purpose] of [
    ['Desktop', 'Keep the Desktop clear', 'Desktop is a workbench.'],
    ['Documents', 'Show me what I keep here', 'Documents is where you keep things.']
  ]) {
    const root = join(sandbox, folder);
    const songs = folder === 'Desktop' ? 8 : 1;
    for (let n = 1; n <= songs; n++) {
      const song =
        folder === 'Documents' ? join(root, 'Projects') : join(root, 'Projects', `Song-${n}`);
      await mkdir(song, { recursive: true, mode: 0o750 });
      await writeFile(join(song, 'Song.rpp'), '<REAPER_PROJECT\n>\n', { mode: 0o600 });
    }
    for (let n = 0; n < 12; n++)
      await writeFile(join(root, `loose-${n}.png`), 'fixture', { mode: 0o600 });
    if (folder === 'Documents')
      for (let n = 0; n < 600; n++) {
        await mkdir(join(root, `Unopened-${n}`), { mode: 0o750 });
        for (let child = 0; child < 4; child++)
          await mkdir(join(root, `Unopened-${n}`, `Child-${child}`), { mode: 0o750 });
      }
    await page.goto('/?panel=today&folder=show');
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await expect(entry).toBeVisible();
    await entry.locator('input').fill(root);
    await entry.getByRole('button').click();
    const card = page.locator('#personalAssistantFolderAgent');
    await expect(card).toContainText(purpose);
    await expect(
      card.getByRole('textbox', { name: 'What should it do?', exact: true })
    ).toHaveValue(brief);
    await expect(card).toContainText('Nothing is created until you hire.');
    await page.setViewportSize({ width: 390, height: 900 });
    expect(await card.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await card.getByRole('button', { name: 'Hire & start', exact: true }).focus();
    await expect(card.getByRole('button', { name: 'Hire & start', exact: true })).toBeFocused();
    await page.screenshot({ path: join(evidence, `${folder}-before-hire.png`), fullPage: true });
    await page.keyboard.press('Enter');
    await page.setViewportSize({ width: 1280, height: 900 });
    const get = async () =>
      (await (await request.get(endpoint)).json()).folder_agents.find(
        (view: { folder_name: string }) => view.folder_name === folder
      );
    await expect.poll(async () => (await get())?.state).toBe('reported');
    const view = await get();
    expect(view.plan.steps.map((step: { kind: string }) => step.kind)).toEqual(['file_curator']);
    expect(view.premise).toBeUndefined();
    const workspacesBefore = await (await request.get('/api/workspaces')).json();
    for (const selected of [
      ['group'],
      ['home:music_project_management'],
      ['reaper-tools'],
      ['file-curator', 'integration:ori_reaper']
    ]) {
      const refused = await request.post(`${endpoint}/${view.workspace_id}/setup`, {
        data: { plan_digest: view.plan.digest, selected }
      });
      expect(refused.status(), await refused.text()).toBe(409);
    }
    expect(await (await request.get('/api/workspaces')).json()).toEqual(workspacesBefore);
    await page.goto(view.route);
    const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
    if (await close.isVisible()) await close.click();
    await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
    const report = page.locator('.folder-agent-card--report:visible');
    const checkbox = report.getByRole('checkbox');
    await expect(checkbox).toHaveCount(1);
    await expect(report).not.toContainText('Create your Music Production Home');
    if (folder === 'Documents') {
      await expect(checkbox).not.toBeChecked();
      await expect(report).toContainText('Documents stays a folder with its agent');
      await expect(report).toContainText('folders not opened yet');
      await expect(report).toContainText('Not in any backup Ori observes');
      await expect(report.getByRole('link', { name: 'Check protection' })).toBeVisible();
      await expect(report.getByRole('button', { name: 'Set it up', exact: true })).toBeDisabled();
      await checkbox.focus();
      await page.keyboard.press('Space');
      await expect(report.getByRole('button', { name: 'Set it up', exact: true })).toBeEnabled();
      await expect(report.getByRole('checkbox')).toBeFocused();
      await page.keyboard.press('Space');
      await expect(report.getByRole('button', { name: 'Set it up', exact: true })).toBeDisabled();
    } else {
      await expect(checkbox).toBeChecked();
      await expect(report).toContainText('Filed');
    }
    await page.screenshot({ path: join(evidence, `${folder}-known-report.png`), fullPage: true });
  }
  // This sandbox deliberately has no Downloads. Missing known locations must
  // not block a normal loose-files workspace from its ordinary default setup.
  const ordinary = join(sandbox, 'Inbox');
  await mkdir(ordinary, { mode: 0o750 });
  for (let n = 0; n < 24; n++)
    await writeFile(
      join(ordinary, `file-${n}.${['png', 'pdf', 'csv', 'txt', 'zip', 'qqq'][n % 6]}`),
      'fixture',
      { mode: 0o600 }
    );
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await entry.locator('input').fill(ordinary);
  await entry.getByRole('button').click();
  const hireCard = page.locator('#personalAssistantFolderAgent');
  await expect(hireCard).toContainText('Start an agent on Inbox');
  await hireCard.getByRole('button', { name: 'Hire & start', exact: true }).click();
  const getOrdinary = async () =>
    (await (await request.get(endpoint)).json()).folder_agents.find(
      (v: { folder_name: string }) => v.folder_name === 'Inbox'
    );
  await expect.poll(async () => (await getOrdinary())?.state).toBe('reported');
  const inbox = await getOrdinary();
  expect(inbox.root_policy).toBeUndefined();
  expect(inbox.plan.runnable).toBe(true);
  const setup = await request.post(`${endpoint}/${inbox.workspace_id}/setup`, {
    data: { plan_digest: inbox.plan.digest, selected: inbox.plan.selected }
  });
  expect(setup.ok(), await setup.text()).toBe(true);
  await expect.poll(async () => (await getOrdinary()).setup?.status).toBe('done');
});
