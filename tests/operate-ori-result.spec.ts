import { test, expect } from '@playwright/test';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import {
  FIXTURE_MODEL,
  seedHiredAssistant,
  seedWorkspace,
  drawer,
  readOperation
} from './helpers/operate-ori';

// Operate Ori, group 4: the result of the request's own run, a rerun that is
// not attributed to it, a run that stops for the user, the recap, leaving the
// workspace page, and a run interrupted by restarting the host.
//
// Production browser, routes, stores, task form, create handler and execution
// path under `wt demo`. The runner runs this file twice on the SAME isolated
// data: once as phase "seed", then, after stopping and restarting that host,
// as phase "reopen". FIXTURES, and only these: the assistant's model and the
// agent's model are the loopback stand-ins `--operate-result` starts. They
// decide nothing about whether or how often a task runs, and they are not
// evidence of how a vendor model behaves.
const phase = process.env.ORI_OPERATE_RESULT_PHASE;
const sandbox = process.env.ORI_WORKSPACE_OPERATERESULT_SANDBOX;
const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
const usage = 'Use python3 scripts/assistant-workspace-demo.py --operate-result';
const evidence = join(process.cwd(), 'tasks', 'evidence', 'operate-ori', 'group-4');
const statePath = () => join(provider!, 'operate-result-state.json');

const kinds = (operation: any) => (operation.receipts || []).map((r: any) => r.kind);
const count = (operation: any, kind: string) =>
  kinds(operation).filter((k: string) => k === kind).length;

test('wt demo (seed): results belong to the request own run; reruns, input and leaving are said as they are', async ({
  page,
  request
}) => {
  test.skip(!sandbox || !provider || phase !== 'seed', usage);
  test.setTimeout(360_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name), fullPage: false });
  const facts: Record<string, unknown> = {};

  await seedHiredAssistant(request, 'operate-result');
  const album = await seedWorkspace(
    request,
    'Album-1 result fixture',
    ['Mixer', 'Engineer'],
    FIXTURE_MODEL
  );
  const albumPath = `/workspaces/${album.folder_slug}`;
  const tasks = async () =>
    (
      await (
        await request.get(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(album.id)}`)
      ).json()
    ).tasks || [];
  const task = async (id: string) =>
    (await request.get(`/api/orchestration/tasks?id=${encodeURIComponent(id)}`)).json();
  const form = { root: page.locator('#taskModal'), save: page.locator('#taskModalSave') };
  const tray = page.locator('.ws-cmd-tray');
  const result = {
    modal: page.locator('#workspace-detail-task-result-modal'),
    title: page.locator('#workspace-detail-task-result-title'),
    body: page.locator('#workspace-detail-task-result-body')
  };
  const recap = {
    root: page.locator('#personalAssistantOperationRecap'),
    summary: page.locator('#personalAssistantOperationRecap > summary'),
    items: page.locator('#personalAssistantOperationRecapList > li')
  };
  const here = drawer(page);
  const startButton = here.actions.getByRole('button', { name: 'Start task' });
  const openResult = here.actions.getByRole('button', { name: 'Open result' });

  /** Ask for a draft, save the form unchanged and return the operation and task. */
  const prepareAndSave = async (ask: string) => {
    const reply = await here.send(ask);
    expect(reply.operation).toMatchObject({ kind: 'task', status: 'prepared' });
    await expect(form.root).toBeVisible({ timeout: 30_000 });
    const created = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === '/api/orchestration/tasks' &&
        response.request().method() === 'POST'
    );
    await form.save.click();
    const saved = (await (await created).json()).task;
    await expect(form.root).toBeHidden();
    await here.open();
    await expect(startButton).toBeVisible();
    return { operationId: reply.operation.id as string, taskId: saved.id as string };
  };

  // --- 1. Instant completion: the result is reachable at once, and it is this run's.
  await page.goto('/');
  await here.open();
  await here.setWatch(true);
  const firstReply = here.send(
    'Create a task for Engineer in the Album-1 result fixture workspace to bounce the stems'
  );
  await page.waitForURL(url => url.pathname === albumPath);
  expect((await firstReply).operation.status).toBe('prepared');
  await expect(form.root).toBeVisible({ timeout: 30_000 });
  const firstCreated = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/orchestration/tasks' &&
      response.request().method() === 'POST'
  );
  await form.save.click();
  const first = {
    operationId: (await firstReply).operation.id as string,
    taskId: (await (await firstCreated).json()).task.id as string
  };
  await here.open();
  const tab = await here.tabState();
  // Keyboard only: Start is a real button, and the choices that replace it
  // keep the focus among themselves instead of dropping it on the page.
  await startButton.focus();
  await page.keyboard.press('Enter');
  await expect(here.status).toHaveText('Engineer finished “Bounce the stems”.', {
    timeout: 60_000
  });
  await expect(openResult).toBeVisible({ timeout: 20_000 });
  expect(
    await page.evaluate(() =>
      document.getElementById('personalAssistantOperationActions')!.contains(document.activeElement)
    )
  ).toBe(true);
  let recorded = (await readOperation(request, first.operationId, tab.tab)).body.operation;
  expect(recorded.outcome).toEqual({ run: 'succeeded', result: 'current' });
  const firstRun = recorded.task.run_id;
  expect(firstRun).toBeTruthy();
  facts.instant = { outcome: recorded.outcome, task: recorded.task, receipts: kinds(recorded) };

  // The recap: recorded facts, each with its source. Opening and closing it
  // sends nothing.
  await expect(recap.root).toBeVisible();
  const sent: string[] = [];
  const watch = (candidate: any) => {
    const url = new URL(candidate.url());
    if (candidate.method() !== 'GET' || url.pathname.includes('/home-assistant/operations/'))
      sent.push(`${candidate.method()} ${url.pathname}`);
  };
  page.on('request', watch);
  await recap.summary.focus();
  await page.keyboard.press('Enter'); // keyboard-only: the recap is a native disclosure
  await expect(recap.items.first()).toBeVisible();
  const lines = await recap.items.allInnerTexts();
  expect(lines).toEqual([
    'Recorded by Ori: A draft was prepared for Engineer.',
    'Seen in this tab: Album-1 result fixture was opened.',
    'Seen in this tab: The draft was shown in the task form.',
    'Recorded by Ori: You saved the form, and the task was created.',
    'Recorded by Ori: You pressed Start, and it was accepted.',
    'From the task: The task reported that it had started.',
    'From the task: It finished.'
  ]);
  await shot('01-finished-open-result-and-recap.png');
  await page.keyboard.press('Enter'); // close
  await page.keyboard.press('Enter'); // replay
  await expect(recap.items.first()).toBeVisible();
  await page.keyboard.press('Enter');
  page.off('request', watch);
  expect(sent).toEqual([]);
  facts.recap = lines;

  // Open the actual result in the page's own result view, from the keyboard.
  await openResult.focus();
  await page.keyboard.press('Enter');
  await expect(result.modal).toBeVisible({ timeout: 30_000 });
  await expect(result.title).toHaveText('Bounce the stems');
  await expect(result.body).toContainText('Fixture worker result for: Bounce the stems');
  await shot('02-the-actual-result.png');
  // The dialog's own Close (Escape works once its opening transition is over).
  await result.modal.locator('button', { hasText: 'Close' }).click();
  await expect(result.modal).toBeHidden();

  // --- 2. The user reruns the task from its own controls. The later run's
  // result is not this request's.
  const rerun = await request.post('/api/orchestration/tasks/execute', {
    data: { task_id: first.taskId }
  });
  expect(rerun.status()).toBe(202);
  await expect
    .poll(async () => ((await task(first.taskId)).execution_history || []).length, {
      timeout: 60_000
    })
    .toBe(2);
  await expect.poll(async () => (await task(first.taskId)).status).toBe('completed');
  await page.goto(albumPath);
  await here.open();
  await expect(here.status).toHaveText(
    "Engineer finished “Bounce the stems”. The task has run again since, so what it shows now is a later run's result. This request's result is in the task's run history."
  );
  await expect(openResult).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open the task' })).toBeVisible();
  recorded = (await readOperation(request, first.operationId, tab.tab)).body.operation;
  expect(recorded.outcome).toEqual({ run: 'succeeded', result: 'history', later_runs: 1 });
  expect(recorded.task.run_id).toBe(firstRun); // the request keeps its own run
  expect(count(recorded, 'start_accepted')).toBe(1);
  expect(count(recorded, 'run_established')).toBe(1);
  facts.afterRerun = { outcome: recorded.outcome, task: recorded.task };
  await shot('03-rerun-is-not-this-request-result.png');

  // --- 3. A run that stops for the user.
  await writeFile(join(provider!, 'worker-needs-input'), '');
  const second = await prepareAndSave('Create a task for Engineer to summarize the session notes');
  await startButton.click();
  const waitingText =
    /needs your input to continue “Summarize the session notes”|“Summarize the session notes” is blocked/;
  await expect(here.status).toHaveText(waitingText, { timeout: 60_000 });
  await expect(startButton).toHaveCount(0);
  await expect(openResult).toHaveCount(0);
  // Stopping here is not cancelling there.
  const stop = here.actions.getByRole('button', { name: 'Stop following' });
  await expect(stop).toHaveAttribute(
    'title',
    /The task keeps going; cancel it from the task itself/
  );
  recorded = (await readOperation(request, second.operationId, tab.tab)).body.operation;
  expect(recorded.status).toBe('started');
  expect(recorded.outcome.run).toBe('waiting');
  expect(recorded.outcome.result).toBe('none');
  const waitingTask = await task(second.taskId);
  expect(waitingTask.status).toBe('waiting_for_choice');
  facts.needsInput = {
    outcome: recorded.outcome,
    taskStatus: waitingTask.status,
    drawer: await here.status.innerText()
  };
  await shot('04-stopped-for-the-user.png');
  await rm(join(provider!, 'worker-needs-input'), { force: true });

  // --- 4. Leaving the workspace: the drawer still follows the task, opens
  // nothing, and the task is one explicit link away.
  await page.goto('/');
  await here.open();
  await expect(here.status).toHaveText(waitingText, { timeout: 30_000 });
  await expect(tray).toHaveCount(0);
  await shot('05-followed-from-another-page.png');
  await page.getByRole('link', { name: 'Open the task' }).click();
  await page.waitForURL(
    url =>
      url.pathname === albumPath &&
      url.searchParams.get('panel') === 'tasks' &&
      url.searchParams.get('task') === second.taskId
  );

  // --- 5. A run in progress when the host stops. The worker is held; the
  // runner now stops this host and starts it again on the same data.
  await writeFile(join(provider!, 'worker-hold'), '');
  await here.open();
  const third = await prepareAndSave('Create a task for Engineer to export the session');
  await startButton.click();
  await expect(here.status).toHaveText('Engineer is working on “Export the session”.', {
    timeout: 60_000
  });
  expect((await task(third.taskId)).status).toBe('in_progress');
  recorded = (await readOperation(request, third.operationId, tab.tab)).body.operation;
  expect(recorded.outcome).toEqual({ run: 'running', result: 'none' });
  const all = await tasks();
  expect(all).toHaveLength(3);
  facts.beforeRestart = { outcome: recorded.outcome, task: recorded.task, tasks: all.length };
  await writeFile(
    statePath(),
    JSON.stringify({
      albumPath,
      albumId: album.id,
      tab: tab.tab,
      first,
      second,
      third,
      firstRun,
      thirdRun: recorded.task.run_id || '',
      workerRequests: (await readFile(join(provider!, 'worker-requests.log'), 'utf8')).length,
      facts
    })
  );
});

test('wt demo (reopen): after the host restarts, an interrupted run is said as interrupted and nothing reruns', async ({
  page,
  request
}) => {
  test.skip(!sandbox || !provider || phase !== 'reopen', usage);
  test.setTimeout(240_000);
  const saved = JSON.parse(await readFile(statePath(), 'utf8'));
  const shot = (name: string) => page.screenshot({ path: join(evidence, name), fullPage: false });
  // Let the held answer go: the host that asked for it is gone.
  await rm(join(provider!, 'worker-hold'), { force: true });
  await writeFile(join(provider!, 'worker-release'), '');
  const task = async (id: string) =>
    (await request.get(`/api/orchestration/tasks?id=${encodeURIComponent(id)}`)).json();

  // The same tab comes back: its own storage survived the host's restart.
  await page.addInitScript(
    ([tab, operation]) => {
      window.sessionStorage.setItem('ori.personalAssistant.tab', tab);
      window.sessionStorage.setItem('ori.personalAssistant.watch', '1');
      window.sessionStorage.setItem(
        'ori.personalAssistant.operation',
        JSON.stringify({ id: operation, auto_until: 0 })
      );
    },
    [saved.tab, saved.third.operationId]
  );
  const here = drawer(page);
  const interrupted =
    'The run of “Export the session” was interrupted before it finished, and nothing was started again. Run it from the task when you are ready.';
  await page.goto(saved.albumPath);
  await here.open();
  await expect(here.status).toHaveText(interrupted, { timeout: 30_000 });
  await expect(here.actions.getByRole('button', { name: /Start/ })).toHaveCount(0);
  await expect(here.actions.getByRole('button', { name: 'Open result' })).toHaveCount(0);
  await expect(page.locator('.ws-cmd-tray')).toBeHidden();
  await shot('06-interrupted-after-restart.png');

  // The server's own records: the operation survived, the run did not finish,
  // and nothing ran again by itself.
  const after = (await readOperation(request, saved.third.operationId, saved.tab)).body.operation;
  expect(after.status).toBe('started');
  expect(after.outcome).toEqual({ run: 'interrupted', result: 'none' });
  expect(after.receipts.filter((r: any) => r.kind === 'start_accepted')).toHaveLength(1);
  const orphan = await task(saved.third.taskId);
  expect(orphan.status).toBe('pending');
  expect(orphan.execution_history || []).toHaveLength(0);
  await page.waitForTimeout(3000);
  expect((await task(saved.third.taskId)).status).toBe('pending');
  expect((await readFile(join(provider!, 'worker-requests.log'), 'utf8')).length).toBe(
    saved.workerRequests
  );
  // A late Start through the same operation is not a restart either.
  const late = await request.post(
    `/api/home-assistant/operations/${saved.third.operationId}/start`,
    { data: { tab_id: saved.tab } }
  );
  expect(late.status()).toBe(200);
  expect((await late.json()).operation.status).toBe('started');
  expect((await task(saved.third.taskId)).status).toBe('pending');

  // What happened before the restart is still as it was recorded.
  const first = (await readOperation(request, saved.first.operationId, saved.tab)).body.operation;
  expect(first.outcome).toEqual({ run: 'succeeded', result: 'history', later_runs: 1 });
  expect(first.task.run_id).toBe(saved.firstRun);
  expect((await task(saved.first.taskId)).execution_history).toHaveLength(2);
  const all = (
    await (
      await request.get(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(saved.albumId)}`
      )
    ).json()
  ).tasks;
  expect(all).toHaveLength(3);

  // Dark, narrow and reduced motion: the same sentence and choices, inside the
  // screen, with nothing scrolling sideways.
  await page.locator('#darkModeToggle').click();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(saved.albumPath);
  await here.open();
  await expect(here.status).toHaveText(interrupted, { timeout: 30_000 });
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
  const fits = await page.evaluate(() => {
    const panel = document.getElementById('personalAssistantPanel')!.getBoundingClientRect();
    const section = document.getElementById('personalAssistantOperation')!;
    return {
      inside: panel.left >= 0 && panel.right <= window.innerWidth + 1,
      sideways: section.scrollWidth > section.clientWidth + 1,
      page: document.documentElement.scrollWidth > window.innerWidth + 1
    };
  });
  expect(fits).toEqual({ inside: true, sideways: false, page: false });
  // The recap opens from the keyboard here too.
  const summary = page.locator('#personalAssistantOperationRecap > summary');
  await summary.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#personalAssistantOperationRecapList > li').last()).toHaveText(
    'From the task: It was interrupted before it finished.'
  );
  // The Watch chip is the touch target; its box stays a box at this width.
  const chip = await page.evaluate(() => {
    const box = document.getElementById('personalAssistantWatch')!.getBoundingClientRect();
    const label = document.getElementById('personalAssistantWatchRow')!.getBoundingClientRect();
    return { box: Math.round(box.width), boxHeight: Math.round(box.height), label: label.height };
  });
  expect(chip.box).toBeLessThanOrEqual(20);
  expect(chip.boxHeight).toBeLessThanOrEqual(20);
  expect(chip.label).toBeGreaterThanOrEqual(44);
  await shot('07-dark-narrow-reduced-motion.png');

  await writeFile(
    join(evidence, 'server-receipts.json'),
    JSON.stringify(
      {
        ...saved.facts,
        afterRestart: {
          outcome: after.outcome,
          task: after.task,
          taskStatus: orphan.status,
          executions: (orphan.execution_history || []).length,
          workerRequestsUnchanged: true
        },
        totals: { tasks: all.length }
      },
      null,
      2
    ) + '\n'
  );
});
