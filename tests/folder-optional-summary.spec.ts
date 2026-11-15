import { test, expect } from '@playwright/test';
import { createServer } from 'node:http';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { stageReviewedFolderPackages } from './helpers/reviewed-folder-packages';
import { installLocalCdn } from './helpers/offline-cdn';
import {
  chooseRetainedHomeTeam,
  openRetainedSongFromHome,
  resumeRetainedMusicReview,
  retainedMusicOptions,
  returnToRetainedMusicReview,
  seedRetainedMusicReview
} from './helpers/retained-music-review';

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
  const upgrade = process.env.ORI_FOLDER_SUMMARY_UPGRADE === '1';
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
    if (upgrade) {
      // Released historical package, staged only via the real installer in the
      // disposable sandbox; no installed checkout or metadata is edited.
      const source =
        'https://github.com/johnjallday/reaper-plugin#sha=3e3234bfae3465f909fe2aa5189f685a41c7a2ed';
      expect((await json('/api/plugins/install', { source })).installed).toBe(false);
      const installed = await json('/api/plugins/install', { source, confirm: true });
      expect(installed.plugin.version).toBe('0.8.0');
      console.log(
        'Historical sandbox package before canonical update:',
        installed.plugin.version,
        source
      );
    }
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
    await installLocalCdn(page);
    // Package preparation uses the real installer, including an actual update
    // from the historical package in --upgrade mode. It is not source-review
    // consent or an edited manifest/floor.
    if (upgrade) {
      const music =
        'https://github.com/johnjallday/music-project-management#sha=c420507da24c1fc007d794f3e8974231dd2c5d4a';
      await json('/api/plugins/install', { source: music });
      await json('/api/plugins/install', { source: music, confirm: true });
      await json('/api/plugins/music-project-management/enable', {});
      // Updating an installed package uses its own reviewed replacement lane,
      // not a second same-name install over a live component registration.
      expect((await json('/api/plugins/reaper-plugin/update', {})).updated).toBe(false);
      const updated = await json('/api/plugins/reaper-plugin/update', { confirm: true });
      expect(updated.updated).toBe(true);
      expect(updated.plugin.version).toBe('0.12.0');
      await json('/api/plugins/reaper-plugin/enable', {});
    } else await stageReviewedFolderPackages(request);
    const reviewed = await seedRetainedMusicReview(request, folder);
    const conversation = reviewed.conversation.id;
    await resumeRetainedMusicReview(page, conversation);
    await chooseRetainedHomeTeam(page, { provider: 'lmstudio', model: 'summary-fixture' });
    const review = await returnToRetainedMusicReview(page);
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
    await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
      timeout: 120_000
    });
    const home = (await json('/api/workspaces')).folders.find(
      (workspace: any) => workspace.kind === 'group'
    );
    expect(home).toBeTruthy();
    expect(
      (await json('/api/workspaces')).folders.filter(
        (workspace: any) => workspace.parent_id === home.id
      )
    ).toHaveLength(0);
    expect(summaryCalls().length).toBe(before);
    await page.getByRole('button', { name: 'Close setup review', exact: true }).click();
    await openRetainedSongFromHome(page, home, 'Desktop');
    await resumeRetainedMusicReview(page, conversation);
    const card = await retainedMusicOptions(page);
    console.log('Unrelated scripted background calls:', calls.length - summaryCalls().length);
    const offer = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    try {
      await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText(
        'is ready',
        { timeout: 60_000 }
      );
    } catch (error) {
      const canonical = (
        await json(`/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(offer.id)}`)
      ).folder_digest?.offer;
      const shown = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
      console.log(
        'Setup timeout diagnostic:',
        JSON.stringify({
          host_status: canonical?.status,
          host_setup: canonical?.setup,
          shown_status: shown?.status,
          shown_setup: shown?.setup
        })
      );
      throw error;
    }
    const optional = card.locator('#personalAssistantFolderOptionalTask');
    await expect(optional).toBeHidden();
    await expect(card.getByRole('button', { name: 'Start summary', exact: true })).toHaveCount(0);
    expect(summaryCalls().length).toBe(before); // A seed is not a summary provider call.
    expect(await readFile(file, 'utf8')).toBe(source);
    await card.locator('#personalAssistantFolderOfferHeadline').scrollIntoViewIfNeeded();
    await capture('summary-workspace-ready.png');
    const ready = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    const workspaceId = ready.outcome.workspace_id;
    const taskPath = `/api/workspaces/${workspaceId}/folder-first-task`;
    const task = (await json(taskPath)).first_task;
    expect(ready.outcome.first_task_id).toBe(task.task_id);
    expect(ready.outcome.first_look).toBeUndefined();
    const open = card.getByRole('link', { name: 'Open workspace', exact: true });
    const route = await open.getAttribute('href');
    await Promise.all([page.waitForURL(url => url.pathname === route), open.click()]);
    await page.waitForFunction(
      () => (window as any).workspaceDetail?.initialListsLoaded,
      undefined,
      { timeout: 15_000 }
    );
    const banner = page.locator('#workspaceFirstTaskBanner');
    if (await page.locator('#personalAssistantPanel').isVisible())
      await page.locator('#personalAssistantClose').click();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(banner).toContainText('Optional project ticket');
    await expect(banner).toContainText('tracks, overview and one next step');
    await expect(banner).toContainText('summary-fixture');
    await expect(banner).toContainText('model tokens');
    await expect(banner).toContainText('source-file changes or external actions');
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
    await resumeRetainedMusicReview(page, conversation);
    await retainedMusicOptions(page);
    await expect(card.locator('#personalAssistantFolderOptionalTask')).toBeVisible();
    await expect(card.getByRole('button', { name: 'Start summary', exact: true })).toHaveCount(0);
    await expect(card).toContainText('Review the vocal arrangement');
    expect(summaryCalls().length).toBe(before + 2);
    expect(await readFile(file, 'utf8')).toBe(source);
    const today = (await json('/api/personal-assistant/today')).today;
    expect(JSON.stringify(today.needs_you)).toContain('start_this');
    const savedReport = card.getByRole('link', { name: 'Open summary', exact: true });
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
