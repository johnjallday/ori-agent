import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { chooseAttach } from './helpers/assistant-menus';

// Real host/observation/review/packages/persistence; only the provider is scripted.
// Run scripts/e2e-folder-summary.sh. No native/live REAPER or paid model claim.
test('setup ends ready; a separately disclosed summary reads once, refuses tools, retries and persists', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_FOLDER_SUMMARY_SANDBOX;
  const port = Number(process.env.ORI_FOLDER_SUMMARY_MODEL_PORT || 0);
  test.skip(!sandbox || !port, 'requires the isolated folder-summary wrapper');
  test.setTimeout(240_000);
  const evidence = process.env.ORI_FOLDER_SUMMARY_EVIDENCE || '';
  if (evidence) await mkdir(evidence, { recursive: true, mode: 0o750 });
  const calls: any[] = [];
  const isSummary = (body: any) =>
    body.messages?.some(
      (message: any) =>
        message.role === 'system' &&
        String(message.content).includes('Summarize only the supplied project evidence')
    );
  const summaryCalls = () => calls.filter(isSummary);
  let attack = false;
  const model = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET')
      return res.end(JSON.stringify({ data: [{ id: 'summary-fixture', object: 'model' }] }));
    let data = '';
    for await (const chunk of req) data += chunk;
    const body = JSON.parse(data);
    calls.push(body);
    const message = !isSummary(body)
      ? {
          role: 'assistant',
          content: JSON.stringify({
            opening_summary: 'Scripted Daily Brief fixture; unrelated to project summary.'
          })
        }
      : attack
        ? {
            role: 'assistant',
            content: null,
            tool_calls: [
              {
                id: 'forbidden',
                type: 'function',
                function: {
                  name: 'write_file',
                  arguments: '{"path":"Session.rpp","content":"forbidden"}'
                }
              }
            ]
          }
        : {
            role: 'assistant',
            content:
              '## Tracks\nGuitar and Vocal are recorded in the saved session.\n\n## Session overview\nTwo named tracks are present; a saved file does not establish edit history or audio quality.\n\n## Suggested first task\nReview the vocal arrangement, because the saved session includes a Vocal track.\n\nNext: Review the vocal arrangement.'
          };
    res.end(
      JSON.stringify({
        id: 'scripted-summary',
        object: 'chat.completion',
        model: 'summary-fixture',
        choices: [
          { index: 0, finish_reason: attack && isSummary(body) ? 'tool_calls' : 'stop', message }
        ],
        usage: { prompt_tokens: 120, completion_tokens: 80, total_tokens: 200 }
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    model.once('error', reject);
    model.listen(port, '127.0.0.1', resolve);
  });
  const json = async (path: string, data?: object) => {
    const response = data ? await request.post(path, { data }) : await request.get(path);
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const capture = async (name: string) => {
    if (evidence) await page.screenshot({ path: join(evidence, name) });
  };
  try {
    await json('/api/onboarding/skip', {});
    await json('/api/settings/workspace-root', { workspace_root: '' });
    let pa = (await json('/api/personal-assistant')).personal_assistant;
    if (pa.state === 'needs_hire') {
      await json('/api/personal-assistant/hire', {
        request_id: 'summary-hire',
        if_version: pa.state_version,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      });
      pa = (await json('/api/personal-assistant')).personal_assistant;
    }
    if (pa.state === 'needs_hq')
      await json('/api/personal-assistant/hq', {
        request_id: 'summary-hq',
        if_version: pa.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      });
    const folder = join(sandbox!, 'Desktop');
    await mkdir(folder, { recursive: true, mode: 0o750 });
    const source =
      '<REAPER_PROJECT 0.1 "7.0"\n<TRACK\nNAME "Guitar"\n>\n<TRACK\nNAME "Vocal"\n>\n>\n';
    const file = join(folder, 'Session.rpp');
    await writeFile(file, source, { mode: 0o600 });
    await json('/api/settings/system-model', { provider: 'lmstudio', model: 'summary-fixture' });
    // Keep unrelated proactive Daily Brief work out of this summary-call audit.
    pa = (await json('/api/personal-assistant')).personal_assistant;
    if (pa.state === 'active')
      await json('/api/personal-assistant/pause', { if_version: pa.state_version });
    const before = summaryCalls().length;
    await page.setViewportSize({ width: 1440, height: 1200 });
    await page.goto('/?panel=today');
    await chooseAttach(page, '#personalAssistantFolderChip:enabled');
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Desktop', exact: true })
      .click();
    const card = page.locator('#personalAssistantFolderOffer');
    await expect(card.locator('#personalAssistantFolderFindings')).toContainText(
      '1 file observed',
      { timeout: 60_000 }
    );
    const requirements = card.locator('#personalAssistantFolderRequirements');
    for (const name of ['Music Project Management', 'REAPER']) {
      await requirements.getByRole('button', { name: `Install ${name}`, exact: true }).click();
      const confirm = requirements.getByRole('button', {
        name: 'Confirm installation',
        exact: true
      });
      await expect(confirm).toBeEnabled({ timeout: 60_000 });
      await confirm.click();
      if (name === 'REAPER') {
        await requirements
          .getByRole('button', { name: 'Enable REAPER', exact: true })
          .click({ timeout: 60_000 });
        await requirements.getByRole('button', { name: 'Confirm enablement', exact: true }).click();
      }
    }
    await expect(requirements).toContainText('Nothing to download', { timeout: 60_000 });
    console.log('Unrelated scripted background calls:', calls.length - summaryCalls().length);
    expect(summaryCalls().length).toBe(before);
    const offer = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    expect(offer.plan.optional_task).toBe(true);
    const conversation = offer.conversation_id;
    await card.getByRole('button', { name: 'Hire & set up', exact: true }).click();
    await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText(
      'Workspace ready',
      { timeout: 60_000 }
    );
    const optional = card.locator('#personalAssistantFolderOptionalTask');
    await expect(optional).toBeVisible();
    await expect(optional).toContainText('tracks, overview and one next step');
    await expect(optional).toContainText('summary-fixture');
    await expect(optional).toContainText('model tokens');
    await expect(optional).toContainText('source-file changes or external actions');
    expect(summaryCalls().length).toBe(before); // A seed is not a summary provider call.
    expect(await readFile(file, 'utf8')).toBe(source);
    await optional.scrollIntoViewIfNeeded();
    await capture('summary-workspace-ready.png');
    const ready = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    const workspaceId = ready.outcome.workspace_id;
    const taskPath = `/api/workspaces/${workspaceId}/folder-first-task`;
    const task = (await json(taskPath)).first_task;
    expect(ready.outcome.first_task_id).toBe(task.task_id);
    expect(ready.outcome.first_look).toBeUndefined();
    await card.getByRole('link', { name: 'Open workspace', exact: true }).click();
    const banner = page.locator('#workspaceFirstTaskBanner');
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await page.locator('#personalAssistantClose').click();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(banner).toContainText('Optional project task');
    await expect(banner).toContainText(task.executor.model);
    expect(summaryCalls().length).toBe(before); // Opening never starts it.
    attack = true;
    const start = banner.getByRole('button', { name: 'Start summary', exact: true });
    await start.click();
    const replay = await json(`${taskPath}/start`, { execution_digest: task.executor.digest });
    expect(replay.started).toBe(false);
    expect(replay.task_id).toBe(task.task_id);
    await expect.poll(async () => (await json(taskPath)).first_task.state).toBe('failed');
    expect(summaryCalls().length).toBe(before + 1);
    expect(await readFile(file, 'utf8')).toBe(source);
    await expect(banner).toContainText('asked to use a tool');
    await capture('summary-refused-write.png');
    attack = false;
    await banner.getByRole('button', { name: 'Try again', exact: true }).click();
    await expect.poll(async () => (await json(taskPath)).first_task.state).toBe('finished');
    await page.reload();
    await expect(banner).toContainText('summary');
    const finished = (await json(taskPath)).first_task;
    expect(finished.task_id).toBe(task.task_id);
    expect(finished.next_request).toContain('Review the vocal arrangement');
    expect(summaryCalls().length).toBe(before + 2);
    for (const call of summaryCalls().slice(before)) {
      expect(call.tools || []).toEqual([]);
      expect(JSON.stringify(call)).toContain('Session.rpp');
      expect(JSON.stringify(call)).not.toContain(sandbox!);
    }
    await page.goto(finished.report_route);
    await expect(
      page
        .getByRole('region', { name: 'Result section: Tracks', exact: true })
        .getByText('Guitar and Vocal are recorded', { exact: false })
    ).toBeVisible();
    await capture('summary-saved-report.png');
    const tasks = (
      await json(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(workspaceId)}`)
    ).tasks;
    expect(
      tasks.filter(
        (item: any) =>
          item.context?.template_id === 'folder-digest' && item.context?.template_starter_task
      )
    ).toHaveLength(1);
    expect(tasks.some((item: any) => item.context?.first_look_task_id)).toBe(false); // Suggested work is not automatically started.
    await page.goto('/?panel=today');
    await page.waitForFunction(() =>
      Boolean((window as any).PersonalAssistantConversation?.resume)
    );
    await page.evaluate(
      async id => (window as any).PersonalAssistantConversation.resume(id),
      conversation
    );
    await expect(card.locator('#personalAssistantFolderOptionalTask')).toContainText(
      'Review the vocal arrangement'
    );
    expect(summaryCalls().length).toBe(before + 2);
    expect(await readFile(file, 'utf8')).toBe(source);
    const today = (await json('/api/personal-assistant/today')).today;
    expect(JSON.stringify(today.needs_you)).toContain('start_this');
    const savedReport = card
      .locator('#personalAssistantFolderOptionalTask')
      .getByRole('link', { name: 'Open summary', exact: true });
    await savedReport.scrollIntoViewIfNeeded({ timeout: 10_000 });
    await expect(savedReport).toBeVisible();
    await capture('summary-reopened-home-report.png');
    console.log(
      'Reviewed sandbox packages:',
      JSON.stringify(
        (await json('/api/plugins')).plugins.map((plugin: any) => ({
          name: plugin.name,
          version: plugin.version,
          source: plugin.source
        }))
      )
    );
  } finally {
    await new Promise<void>(resolve => model.close(() => resolve()));
  }
});
