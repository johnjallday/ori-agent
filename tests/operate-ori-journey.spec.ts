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

// Operate Ori, the whole journey: open an existing workspace, prepare a task
// for an existing non-entry agent, review and start it, follow real progress
// and open the result, with a second tab open the whole time.
//
// Production browser, routes, stores, task form, create handler and execution
// path under `wt demo`. FIXTURES, and only these: the assistant's model and
// the agent's model are the loopback stand-ins `--operate-journey` starts.
// They decide nothing about whether or how often a task runs, and they are
// not evidence of how a vendor model behaves.
test('wt demo: the whole journey for a non-entry agent, with a second tab open and the manual route intact', async ({
  page,
  context,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_OPERATEJOURNEY_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !sandbox || !provider,
    'Use python3 scripts/assistant-workspace-demo.py --operate-journey'
  );
  test.setTimeout(360_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence', 'operate-ori', 'group-5');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name), fullPage: false });
  const facts: Record<string, unknown> = {};

  await seedHiredAssistant(request, 'operate-journey');
  // Mixer joins first and is the entry agent. Engineer is not.
  const album = await seedWorkspace(
    request,
    'Album-1 journey fixture',
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
  const roster = async () =>
    (
      (
        await (
          await request.get(`/api/orchestration/workspace?id=${encodeURIComponent(album.id)}`)
        ).json()
      ).agent_instances || []
    ).map((agent: any) => agent.name);
  const kinds = (operation: any) => (operation.receipts || []).map((r: any) => r.kind);
  const workspacesBefore = (await listWorkspaces(request)).length;
  const rosterBefore = await roster();
  expect(rosterBefore).toEqual(['Mixer', 'Engineer']);
  expect(await tasks()).toHaveLength(0);

  // A second tab, open on Home, that also chose to watch. It is another tab:
  // nothing asked for in the first one may move it.
  const second = await context.newPage();
  const there = drawer(second);
  await second.goto('/');
  await there.open();
  await there.setWatch(true);
  const secondTab = await there.tabState();

  const form = {
    root: page.locator('#taskModal'),
    heading: page.locator('#taskModalTitle'),
    title: page.locator('#taskModalDescription'),
    agent: page.locator('#taskModalAssignment'),
    save: page.locator('#taskModalSave')
  };
  const tray = page.locator('.ws-cmd-tray');
  const result = {
    modal: page.locator('#workspace-detail-task-result-modal'),
    body: page.locator('#workspace-detail-task-result-body')
  };
  const here = drawer(page);
  const startButton = here.actions.getByRole('button', { name: 'Start task' });
  const openResult = here.actions.getByRole('button', { name: 'Open result' });

  // --- 1. Open the workspace and select the agent.
  await page.goto('/');
  await here.open();
  await here.setWatch(true);
  const firstTab = await here.tabState();
  expect(firstTab.tab).not.toBe(secondTab.tab);
  const opened = await here.send('Open the Album-1 journey fixture workspace and select Engineer');
  expect(opened.operation).toMatchObject({ kind: 'navigation', watching: true });
  await page.waitForURL(url => url.pathname === albumPath);
  await here.open();
  await expect(here.status).toHaveText('Opened Album-1 journey fixture and selected Engineer.', {
    timeout: 30_000
  });
  await shot('01-opened-and-selected.png');

  // --- 2. Prepare a task for the non-entry agent, in the app's own form.
  const prepared = await here.send('Create a task for Engineer to bounce the stems');
  expect(prepared.operation).toMatchObject({ kind: 'task', status: 'prepared' });
  const operationId = prepared.operation.id;
  await expect(form.root).toBeVisible({ timeout: 30_000 });
  await expect(form.heading).toHaveText('Review task for Engineer');
  await expect(form.agent.locator('option:checked')).toHaveText('Engineer');
  expect(await tasks()).toHaveLength(0);
  await shot('02-editable-review.png');

  // --- 3. The user's editable review, then their own Save.
  await form.title.fill('Bounce the stems at 48k');
  const created = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/orchestration/tasks' &&
      response.request().method() === 'POST'
  );
  await form.save.click();
  const saved = (await (await created).json()).task;
  await expect(form.root).toBeHidden();
  await here.open();
  await expect(here.status).toHaveText(
    'Task created for Engineer: “Bounce the stems at 48k”. It has not started.'
  );
  expect((await task(saved.id)).status).toBe('pending');

  // --- 4. The user's own Start, with the worker held so progress is observable.
  await writeFile(join(provider!, 'worker-hold'), '');
  await startButton.click();
  await expect(here.status).toHaveText('Engineer is working on “Bounce the stems at 48k”.', {
    timeout: 60_000
  });
  await expect(tray).toBeVisible();
  await shot('03-real-progress.png');

  // The second tab did not move, was handed nothing, and can still read the facts.
  expect(new URL(second.url()).pathname).toBe('/');
  await expect(second.locator('#taskModal')).toBeHidden(); // no form opened there
  await expect(second.locator('.ws-cmd-tray')).toHaveCount(0);
  const seenBySecond = (await readOperation(request, operationId, secondTab.tab)).body.operation;
  expect(seenBySecond.watching).toBe(false);
  expect((seenBySecond.commands || []).filter((c: any) => c.status === 'pending')).toEqual([]);
  expect(seenBySecond.status).toBe('started');
  await second.screenshot({ path: join(evidence, '04-second-tab-did-not-move.png') });

  // --- 5. It finishes; the result is this run's and opens at once.
  await writeFile(join(provider!, 'worker-release'), '');
  await expect(here.status).toHaveText('Engineer finished “Bounce the stems at 48k”.', {
    timeout: 60_000
  });
  await expect(openResult).toBeVisible({ timeout: 20_000 });
  await openResult.click();
  await expect(result.modal).toBeVisible({ timeout: 30_000 });
  await expect(result.body).toContainText('Fixture worker result for: Bounce the stems at 48k');
  await shot('05-result.png');
  await result.modal.locator('button', { hasText: 'Close' }).click();
  await expect(result.modal).toBeHidden();
  await rm(join(provider!, 'worker-hold'), { force: true });
  await rm(join(provider!, 'worker-release'), { force: true });

  // The server's own records for the journey.
  const done = await task(saved.id);
  expect(done).toMatchObject({
    description: 'Bounce the stems at 48k',
    to: 'Engineer',
    status: 'completed',
    assistant_operation_id: operationId,
    assignment_mode: 'manual'
  });
  expect(done.assigned_node_id).toBe(prepared.operation.draft.agent_node_id);
  expect(done.execution_history).toHaveLength(1);
  expect(done.assistant_operation_run.executed_at).toBe(done.execution_history[0].executed_at);
  const journey = (await readOperation(request, operationId, firstTab.tab)).body.operation;
  expect(kinds(journey)).toEqual([
    'draft_prepared',
    'view_opened', // this tab reported it was already on the workspace's page
    'draft_opened',
    'task_created',
    'start_accepted',
    'run_established'
  ]);
  expect(journey.outcome).toEqual({ run: 'succeeded', result: 'current' });
  expect(journey.task.task_id).toBe(saved.id);
  facts.journey = {
    task: journey.task,
    receipts: kinds(journey),
    outcome: journey.outcome,
    assignee: { to: done.to, node: done.assigned_node_id },
    executions: done.execution_history.length
  };

  // --- 6. The agent's model fails. Whatever Ori's executor makes of that (on
  // this host it stops the task and asks the user what to do), the drawer says
  // the task's own state: never working, never finished, and no result.
  await here.actions.getByRole('button', { name: 'Dismiss' }).click();
  await writeFile(join(provider!, 'worker-fail'), '');
  const failing = await here.send('Create a task for Engineer to export the session');
  await expect(form.root).toBeVisible({ timeout: 30_000 });
  const failingCreated = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/orchestration/tasks' &&
      response.request().method() === 'POST'
  );
  await form.save.click();
  const failingTask = (await (await failingCreated).json()).task;
  await here.open();
  await startButton.click();
  await expect(here.status).toHaveText(
    /“Export the session” (failed|is blocked|timed out)|needs your input to continue “Export the session”/,
    {
      timeout: 90_000
    }
  );
  await expect(openResult).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Open the task' })).toBeVisible();
  const failed = (await readOperation(request, failing.operation.id, firstTab.tab)).body.operation;
  expect(failed.status).toBe('started');
  expect(failed.outcome.result).toBe('none');
  expect(['failed', 'waiting', 'stopped']).toContain(failed.outcome.run);
  const failedTask = await task(failingTask.id);
  expect(failedTask.status).not.toBe('completed');
  facts.agentModelFailed = {
    outcome: failed.outcome,
    taskStatus: failedTask.status,
    drawer: await here.status.innerText()
  };
  await shot('06-agent-model-failed-not-success.png');
  await rm(join(provider!, 'worker-fail'), { force: true });

  // --- 7. The manual route is unchanged: an ordinary task, created and run
  // through the task API the app's own controls use, involves no operation.
  const manual = await request.post('/api/orchestration/tasks', {
    data: { workspace_id: album.id, description: 'Tidy the session', to: 'Mixer', priority: 3 }
  });
  expect(manual.status()).toBe(201);
  const manualTask = (await manual.json()).task;
  expect(manualTask.assistant_operation_id ?? '').toBe('');
  expect(
    (
      await request.post('/api/orchestration/tasks/execute', { data: { task_id: manualTask.id } })
    ).status()
  ).toBe(202);
  await expect
    .poll(async () => (await task(manualTask.id)).status, { timeout: 60_000 })
    .toBe('completed');
  expect((await task(manualTask.id)).assistant_operation_run ?? null).toBeNull();

  // --- 8. Ordinary conversation and Help are as they were.
  const chat = await here.send('What should Mixer do next?');
  expect(chat.operation).toBeUndefined();
  expect(chat.requires_confirmation).toBeFalsy();
  await page.getByRole('button', { name: 'Help' }).first().click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#oriGuideTitle')).toHaveText('Help');

  // --- 9. Nothing else was created, added or changed.
  const all = await tasks();
  expect(all.map((item: any) => item.description).sort()).toEqual([
    'Bounce the stems at 48k',
    'Export the session',
    'Tidy the session'
  ]);
  expect(await roster()).toEqual(rosterBefore);
  expect((await listWorkspaces(request)).length).toBe(workspacesBefore);
  expect(new URL(second.url()).pathname).toBe('/');
  facts.totals = { tasks: all.length, roster: rosterBefore, workspaces: workspacesBefore };

  // --- 10. What Ori's harness handed the model on an operating turn: the real
  // skill text and the real tool definitions, and nothing that starts work.
  const harness = JSON.parse(await readFile(join(provider!, 'operate-harness.json'), 'utf8'));
  expect(harness).toMatchObject({
    skill_loaded: true,
    skill_version: 3,
    skill_names_each_operation: true,
    skill_says_save_and_start_are_the_users: true,
    operations: {
      assistant_operate_open: {
        parameters: ['agent_name', 'workspace_id', 'workspace_name'],
        closed_schema: true
      },
      assistant_operate_prepare_task: {
        parameters: [
          'agent_name',
          'agent_node_id',
          'brief',
          'title',
          'workspace_id',
          'workspace_name'
        ],
        closed_schema: true
      }
    }
  });
  for (const name of harness.offered_tools as string[])
    expect(name, 'no offered tool creates, starts or runs anything').not.toMatch(
      /start|execute|run_task|create_task|save/
    );
  facts.harness = harness;
  await writeFile(join(evidence, 'server-receipts.json'), JSON.stringify(facts, null, 2) + '\n');
});
