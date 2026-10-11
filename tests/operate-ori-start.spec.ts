import { test, expect } from '@playwright/test';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import {
  FIXTURE_MODEL,
  seedHiredAssistant,
  seedWorkspace,
  listWorkspaces,
  drawer,
  readOperation
} from './helpers/operate-ori';

// Operate Ori, group 3: the reviewed save creates one task bound to its
// operation, the user's own Start is claimed once, and what the drawer says
// about execution comes from the task itself.
//
// Production browser, Route/Ask, the operation store, the real task form and
// its save owner, the canonical task create handler, and Ori's own execution
// path, under `wt demo`. Two things are fixtures, and only these:
// - PROVIDER FIXTURE (assistant): the loopback stand-in `--operate-start`
//   starts calls Ori's real prepare tool with the user's own words.
// - WORKER FIXTURE (agent): the same loopback server answers the request Ori's
//   executor builds for a task it is really running, optionally after a hold.
//   It decides nothing about whether or how often a task runs.
// Neither is evidence of how a vendor model behaves.
test('wt demo: one reviewed task, one Start, real progress, and a refusal that claims nothing', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_OPERATESTART_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !sandbox || !provider,
    'Use python3 scripts/assistant-workspace-demo.py --operate-start'
  );
  test.setTimeout(300_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence', 'operate-ori', 'group-3');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name), fullPage: false });
  const receipts: Record<string, unknown> = {};

  await seedHiredAssistant(request, 'operate-start');
  // Mixer joins first and is the entry agent. Engineer and Drummer are not.
  const album = await seedWorkspace(
    request,
    'Album-1 start fixture',
    ['Mixer', 'Engineer', 'Drummer'],
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
  const kinds = (operation: any) => (operation.receipts || []).map((r: any) => r.kind);
  const count = (operation: any, kind: string) =>
    kinds(operation).filter((k: string) => k === kind).length;
  const workspacesBefore = (await listWorkspaces(request)).length;
  expect(await tasks()).toHaveLength(0);

  const form = {
    root: page.locator('#taskModal'),
    title: page.locator('#taskModalDescription'),
    agent: page.locator('#taskModalAssignment'),
    save: page.locator('#taskModalSave')
  };
  const tray = page.locator('.ws-cmd-tray');
  const here = drawer(page);
  const startButton = here.actions.getByRole('button', { name: 'Start task' });

  // --- 1. Prepare, edit, and save through the form's own review control.
  await page.goto('/');
  await here.open();
  await here.setWatch(true);
  const prepared = await here.send(
    'Create a task for Engineer in the Album-1 start fixture workspace to bounce the stems'
  );
  expect(prepared.operation).toMatchObject({ kind: 'task', status: 'prepared' });
  const operationId = prepared.operation.id;
  await page.waitForURL(url => url.pathname === albumPath);
  await expect(form.root).toBeVisible({ timeout: 20_000 });
  await expect(form.agent.locator('option:checked')).toHaveText('Engineer');
  expect(await tasks()).toHaveLength(0);

  await form.title.fill('Bounce the stems at 48k'); // the human's edit is what is created
  const createRequest = page.waitForRequest(
    candidate =>
      new URL(candidate.url()).pathname === '/api/orchestration/tasks' &&
      candidate.method() === 'POST'
  );
  await form.save.click();
  const sent = await createRequest;
  const createResponse = await sent.response();
  expect(createResponse!.status()).toBe(201);
  const createBody = sent.postDataJSON();
  // The form is the one create owner; its request names the operation.
  expect(createBody).toMatchObject({
    description: 'Bounce the stems at 48k',
    to: 'Engineer',
    assistant_operation_id: operationId,
    assistant_draft_revision: 1
  });
  const created = (await createResponse!.json()).task;
  expect(created.assistant_operation_id).toBe(operationId);
  await expect(form.root).toBeHidden();

  const tab = await here.tabState();
  await here.open();
  await expect(here.status).toHaveText(
    'Task created for Engineer: “Bounce the stems at 48k”. It has not started.'
  );
  await expect(startButton).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the task' })).toHaveAttribute(
    'href',
    `${albumPath}?panel=tasks&task=${created.id}`
  );
  // Saving authorized creation only: one waiting task, no run, no tray.
  let saved = await tasks();
  expect(saved).toHaveLength(1);
  expect(saved[0]).toMatchObject({
    id: created.id,
    description: 'Bounce the stems at 48k',
    to: 'Engineer',
    status: 'pending'
  });
  expect(saved[0].current_run_id || '').toBe('');
  expect(saved[0].execution_history || []).toHaveLength(0);
  await expect(tray).toBeHidden();
  let recorded = (await readOperation(request, operationId, tab.tab)).body.operation;
  expect(recorded.status).toBe('reviewed');
  expect(recorded.task).toMatchObject({ task_id: created.id });
  expect(count(recorded, 'task_created')).toBe(1);
  expect(count(recorded, 'start_accepted')).toBe(0);
  receipts.afterSave = { status: recorded.status, task: recorded.task, receipts: kinds(recorded) };
  await shot('01-task-created-not-started.png');

  // --- 2. The same create request again (a lost response, a second tab): one task.
  for (let repeat = 0; repeat < 3; repeat += 1) {
    const again = await request.post('/api/orchestration/tasks', { data: createBody });
    expect(again.status()).toBe(200);
    const body = await again.json();
    expect(body.already_created).toBe(true);
    expect(body.task.id).toBe(created.id);
  }
  expect(await tasks()).toHaveLength(1);
  receipts.afterRepeatedSave = { tasks: (await tasks()).length };

  // --- 3. The user's Start. The worker is held so the run can be observed.
  await writeFile(join(provider!, 'worker-hold'), '');
  const startRequests: string[] = [];
  page.on('request', candidate => {
    if (candidate.method() === 'POST' && candidate.url().endsWith(`/${operationId}/start`))
      startRequests.push(candidate.url());
  });
  await startButton.click();
  // Real progress, from the task itself: in progress, with the page's own tray.
  await expect(here.status).toHaveText('Engineer is working on “Bounce the stems at 48k”.', {
    timeout: 30_000
  });
  await expect(startButton).toHaveCount(0);
  await expect(tray).toBeVisible();
  expect((await task(created.id)).status).toBe('in_progress');
  recorded = (await readOperation(request, operationId, tab.tab)).body.operation;
  expect(recorded.status).toBe('started');
  expect(count(recorded, 'start_accepted')).toBe(1);
  expect(count(recorded, 'run_established')).toBe(1);
  await shot('02-real-progress-while-running.png');

  // Duplicate deliveries of Start, from this tab's identity and from another.
  for (const tabId of [tab.tab, 'another-tab', '']) {
    const again = await request.post(`/api/home-assistant/operations/${operationId}/start`, {
      data: { tab_id: tabId }
    });
    expect(again.status()).toBe(200);
    expect((await again.json()).operation.status).toBe('started');
  }
  expect(startRequests).toHaveLength(1); // the page itself sent Start once

  // --- 4. Reconnect mid-run: the reload reads the same run and starts nothing.
  await page.reload();
  await here.open();
  await expect(here.status).toHaveText('Engineer is working on “Bounce the stems at 48k”.', {
    timeout: 30_000
  });
  expect(startRequests).toHaveLength(1);
  expect((await task(created.id)).status).toBe('in_progress');
  await shot('03-after-reload-same-run.png');

  // --- 5. The worker finishes: completion is the task's own state.
  await writeFile(join(provider!, 'worker-release'), '');
  await expect(here.status).toHaveText('Engineer finished “Bounce the stems at 48k”.', {
    timeout: 60_000
  });
  const finished = await task(created.id);
  expect(finished.status).toBe('completed');
  expect(finished.result).toContain('Fixture worker result for: Bounce the stems at 48k');
  expect(finished.execution_history).toHaveLength(1);
  await shot('04-finished-from-task-state.png');

  // A late Start on the finished task is not a rerun.
  for (let late = 0; late < 2; late += 1) {
    const again = await request.post(`/api/home-assistant/operations/${operationId}/start`, {
      data: { tab_id: tab.tab }
    });
    expect(again.status()).toBe(200);
  }
  await page.waitForTimeout(750);
  const afterLate = await task(created.id);
  expect(afterLate.status).toBe('completed');
  expect(afterLate.execution_history).toHaveLength(1);
  recorded = (await readOperation(request, operationId, tab.tab)).body.operation;
  expect(count(recorded, 'start_accepted')).toBe(1);
  expect(count(recorded, 'run_established')).toBe(1);
  expect(count(recorded, 'task_created')).toBe(1);
  receipts.afterRun = {
    status: recorded.status,
    task: recorded.task,
    receipts: kinds(recorded),
    taskStatus: afterLate.status,
    executions: afterLate.execution_history.length
  };
  await rm(join(provider!, 'worker-hold'), { force: true });
  await rm(join(provider!, 'worker-release'), { force: true });

  // --- 6. A refused Start: nothing is shown as working, and the task stays.
  await here.actions.getByRole('button', { name: 'Dismiss' }).click();
  const second = await here.send('Create a task for Drummer to record the fills');
  expect(second.operation).toMatchObject({ kind: 'task', status: 'prepared' });
  await expect(form.root).toBeVisible({ timeout: 20_000 });
  const secondCreate = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/orchestration/tasks' &&
      response.request().method() === 'POST'
  );
  await form.save.click();
  const secondTask = (await (await secondCreate).json()).task;
  await here.open();
  await expect(here.status).toHaveText(
    'Task created for Drummer: “Record the fills”. It has not started.'
  );
  // Drummer leaves the workspace before the user presses Start.
  const removed = await request.delete(
    `/api/orchestration/workspace/agents?workspace_id=${encodeURIComponent(album.id)}&agent_name=Drummer`
  );
  expect(removed.ok(), await removed.text()).toBeTruthy();
  const workerRequestsBefore = (await readFile(join(provider!, 'worker-requests.log'), 'utf8'))
    .length;
  await startButton.click();
  await expect(here.status).toHaveText(
    'The task for Drummer was not started. The task has no agent now. Assign it to an agent in this workspace, then start it.'
  );
  await expect(here.actions.getByRole('button', { name: 'Try starting again' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the task' })).toBeVisible();
  await shot('05-refused-start-keeps-the-task.png');
  const refusedTask = await task(secondTask.id);
  expect(refusedTask.status).toBe('pending');
  expect(refusedTask.started_at ?? null).toBeNull();
  expect(refusedTask.execution_history || []).toHaveLength(0);
  // It was not handed to the entry agent or anyone else.
  expect(refusedTask.to).toBe('unassigned');
  const refused = (await readOperation(request, second.operation.id, tab.tab)).body.operation;
  expect(refused.status).toBe('reviewed');
  expect(count(refused, 'start_refused')).toBe(1);
  expect(count(refused, 'start_accepted')).toBe(0);
  expect(count(refused, 'run_established')).toBe(0);
  // No worker was asked to do anything.
  expect((await readFile(join(provider!, 'worker-requests.log'), 'utf8')).length).toBe(
    workerRequestsBefore
  );
  receipts.afterRefusedStart = {
    status: refused.status,
    receipts: kinds(refused),
    taskStatus: refusedTask.status,
    executions: (refusedTask.execution_history || []).length
  };

  // Exactly the two tasks the user saved exist; the first ran once.
  const all = await tasks();
  expect(all).toHaveLength(2);
  expect((await task(created.id)).execution_history).toHaveLength(1);
  expect((await listWorkspaces(request)).length).toBe(workspacesBefore);
  receipts.totals = { tasks: all.length, workspaces: workspacesBefore };
  await writeFile(join(evidence, 'server-receipts.json'), JSON.stringify(receipts, null, 2) + '\n');
});
