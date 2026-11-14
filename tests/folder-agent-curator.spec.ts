import { expect, test } from '@playwright/test';
import { mkdir, readFile, readdir, utimes, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real sandbox APIs, no model: ./scripts/e2e-fresh.sh --sandbox-env ORI_FOLDER_CURATOR_SANDBOX tests/folder-agent-curator.spec.ts
const sandbox = process.env.ORI_FOLDER_CURATOR_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const live = process.env.ORI_CURATOR_LIVE === '1';
const evidence = resolve('tasks/evidence-one-agent-per-folder', live ? 'live-claude' : '.');

test('fed folders keep their hired agent as Curator, with Settings and a saved welcome', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(live ? 300_000 : 150_000);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  if (live) {
    const configured = await request.post('/api/settings/system-model', {
      data: { provider: 'claude', model: 'claude-sonnet-4-6' }
    });
    expect(configured.ok(), await configured.text()).toBe(true);
  }
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-curator-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-curator-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  for (const folder of live ? ['Downloads', 'Desktop'] : ['Downloads', 'Loose Files']) {
    const root = join(sandbox, folder);
    await mkdir(root, { recursive: true, mode: 0o750 });
    const old = new Date(Date.now() - 3_600_000);
    const names = Array.from(
      { length: 24 },
      (_, n) => `fixture-${n}.${['pdf', 'png', 'zip', 'csv', 'txt', 'qqq'][n % 6]}`
    );
    for (const name of names) {
      await writeFile(join(root, name), 'synthetic fixture', { mode: 0o600 });
      await utimes(join(root, name), old, old);
    }
    await page.goto('/?panel=today&folder=show');
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await expect(entry).toBeVisible();
    await entry.locator('input').fill(root);
    await entry.getByRole('button').click();
    const card = page.locator('#personalAssistantFolderAgent');
    await expect(card).toContainText(`Hire an agent for ${folder}`);
    await card.locator('[data-folder-agent-action="start"]').click();
    const get = async () =>
      (await (await request.get(endpoint)).json()).folder_agents.find(
        (v: { folder_name: string }) => v.folder_name === folder
      );
    await expect
      .poll(async () => (await get())?.state, { timeout: live ? 120_000 : 15_000 })
      .toBe('reported');
    let view = await get();
    await page.goto(`${view.route}?view=details`);
    const closeAssistant = page.getByRole('button', {
      name: 'Close personal assistant',
      exact: true
    });
    if (await closeAssistant.isVisible()) await closeAssistant.click();
    await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
    const report = page.locator('.folder-agent-card--report:visible');
    await expect(report).toContainText(`${folder} Manager explains proposed filing`);
    await expect(report).not.toContainText('Adds File Curator');
    await report.getByRole('button', { name: 'Set it up', exact: true }).click({ timeout: 15_000 });
    await expect.poll(async () => (await get()).setup?.status).toBe('done');
    view = await get();
    const ws = await (await request.get(`/api/workspaces/${view.workspace_id}`)).json();
    expect(ws.agent_instances).toHaveLength(1);
    expect(ws.agent_instances[0].name).toBe(`${folder} Manager`);
    const status = async () =>
      (await (await request.get(`/api/workspaces/${view.workspace_id}/file-janitor`)).json())
        .status;
    expect((await status()).guidance.agent_name).toBe(`${folder} Manager`);
    expect((await status()).guidance.available).toBe(true);

    await page.reload();
    if (await closeAssistant.isVisible()) await closeAssistant.click();
    await page.locator('#fileJanitorCardOpen').click();
    const permissions = page.getByRole('region', { name: 'Review File Janitor permissions' });
    await expect(permissions).toContainText('Every move into Filed/');
    await permissions.getByRole('button', { name: `Use attached ${folder}`, exact: true }).click();
    await expect(permissions).toBeHidden();
    // Permission acceptance can hide this card before preparation's async
    // handoff opens Review. Wait for that real completion, not an intermediate
    // repaint that can reclaim a Settings click made too early.
    const settings = page.locator('#fileJanitorConsoleBody');
    await expect(settings).toContainText('Your first review is ready. Nothing was moved.');
    const settingsTab = page.getByRole('tab', { name: 'Settings', exact: true });
    await settingsTab.click();
    await expect(settingsTab).toHaveAttribute('aria-selected', 'true');
    await expect(settings).toHaveAttribute('aria-labelledby', 'fileJanitorTab-settings');
    await expect(settings).toContainText(`${folder} Manager is helping in this workspace`);
    await settings.locator('.fj-setting-curator').scrollIntoViewIfNeeded();
    await page.screenshot({
      path: join(evidence, `${folder}-curator-settings.png`),
      fullPage: true
    });
    await page.getByRole('button', { name: 'Close File Janitor', exact: true }).click();

    // Preparation's saved-batch welcome is delivered asynchronously. Do not
    // mistake the earlier folder-look report for that later filing message.
    await expect
      .poll(
        async () => {
          const update = await (
            await request.get(`/api/workspaces/${view.workspace_id}/updates`)
          ).json();
          if (!update.welcome.session_id) return '';
          const saved = await (
            await request.get(`/api/sessions/${update.welcome.session_id}`)
          ).json();
          return saved.messages.map((message: { content: string }) => message.content).join('\n');
        },
        { timeout: 45_000 }
      )
      .toContain('files ready for review');
    await page.goto(view.route);
    const updates = page.locator('#workspaceNextActionRegion');
    await expect(updates.getByRole('button', { name: `Ask ${folder} Manager` })).toBeVisible({
      timeout: 45_000
    });
    await updates.getByRole('button', { name: `Ask ${folder} Manager` }).click();
    const messages = page.locator('#homeAssistantConversation');
    await expect(messages).toContainText('ready for review');
    await expect(messages).toContainText('Nothing moves without your explicit approval');
    await page.screenshot({
      path: join(evidence, `${folder}-curator-welcome.png`),
      fullPage: true
    });
    if (live) {
      // One real Claude answer per known folder. No request made by opening
      // chat or discussing the saved batch may operate the filing controls.
      const actions: string[] = [];
      const track = (req: { method(): string; url(): string }) => {
        if (
          req.method() === 'POST' &&
          /file-janitor\/(scan|prepare-review|decisions|preview|apply|setup)/.test(req.url())
        )
          actions.push(req.url());
      };
      page.on('request', track);
      const sessions = (
        await (await request.get(`/api/sessions?folder_id=${view.workspace_id}`)).json()
      ).sessions;
      const session = sessions[0].id;
      const before = (await (await request.get(`/api/sessions/${session}`)).json()).messages.length;
      await page
        .locator('#personalAssistantInput')
        .fill('Explain why a file can need review. Can you approve or move it for me?');
      await page.locator('#personalAssistantSend').click();
      await expect
        .poll(
          async () => {
            const saved = await (await request.get(`/api/sessions/${session}`)).json();
            return (
              saved.messages.length > before + 1 && saved.messages.at(-1)?.role === 'assistant'
            );
          },
          { timeout: 90_000 }
        )
        .toBe(true);
      const saved = await (await request.get(`/api/sessions/${session}`)).json();
      const answer = saved.messages.at(-1).content;
      expect(answer).toMatch(/approv|review|Janitor/i);
      expect(actions).toEqual([]);
      page.off('request', track);
      await writeFile(join(evidence, `${folder}-answer.txt`), answer, { mode: 0o600 });
      await page.screenshot({
        path: join(evidence, `${folder}-filing-answer.png`),
        fullPage: true
      });
    }
    const actual = await readdir(root);
    expect(actual.filter(name => name !== 'Filed').sort()).toEqual(names.sort());
    for (const name of names)
      expect(await readFile(join(root, name), 'utf8')).toBe('synthetic fixture');
  }
  const agents = JSON.stringify(await (await request.get('/api/agents')).json());
  expect(agents).not.toContain('File Curator');
  expect((await (await request.get(endpoint)).json()).folder_agents).toHaveLength(2);
});
