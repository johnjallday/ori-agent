import { test, expect } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { basename, join } from 'node:path';
import {
  seedHiredAssistant,
  seedWorkspace,
  listWorkspaces,
  drawer,
  readOperation
} from './helpers/operate-ori';

// Operate Ori, group 2: ask the assistant to prepare a task for an agent, then
// review, change, dismiss, reopen and save it in the app's own task form.
//
// Production browser, Route/Ask, the operation tool registry, the SQLite
// operation store and the real task form with its own save owner, under
// `wt demo`. PROVIDER FIXTURE: the model is the loopback stand-in
// `--operate-task` starts. It calls Ori's real prepare tool with the user's own
// words and repeats only what the tool returned. It is not evidence of how a
// vendor model behaves.
test('wt demo: Ori prepares an editable task draft and creates nothing until the user saves', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_OPERATETASK_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !sandbox || !provider,
    'Use python3 scripts/assistant-workspace-demo.py --operate-task'
  );
  test.setTimeout(240_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence', 'operate-ori', 'group-2');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (name: string) => page.screenshot({ path: join(evidence, name), fullPage: false });

  await seedHiredAssistant(request, 'operate-task');
  // Mixer joins first and is the workspace's entry agent; Engineer is not.
  const album = await seedWorkspace(request, 'Album-1 task fixture', ['Mixer', 'Engineer']);
  const albumPath = `/workspaces/${album.folder_slug}`;
  const tasks = async () =>
    (
      await (
        await request.get(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(album.id)}`)
      ).json()
    ).tasks || [];
  const workspacesBefore = (await listWorkspaces(request)).length;
  expect(await tasks()).toHaveLength(0);

  const form = {
    root: page.locator('#taskModal'),
    heading: page.locator('#taskModalTitle'),
    title: page.locator('#taskModalDescription'),
    brief: page.locator('#taskModalDetails'),
    agent: page.locator('#taskModalAssignment'),
    save: page.locator('#taskModalSave'),
    close: page.locator('#taskModalClose')
  };
  const here = drawer(page);
  const ask =
    'Create a task for Engineer in the Album-1 task fixture workspace to bounce the stems';

  // --- 1. Watch on: the workspace opens and the real form holds the draft.
  await page.goto('/');
  await here.open();
  await here.setWatch(true);
  const prepared = await here.send(ask);
  expect(prepared.requires_confirmation).toBeFalsy(); // no one-click create beside it
  expect(prepared.operation).toMatchObject({ kind: 'task', status: 'prepared', watching: true });
  expect(prepared.operation.task).toBeUndefined();
  expect(prepared.operation.draft).toMatchObject({
    revision: 1,
    title: 'Bounce the stems',
    brief: ask,
    agent_name: 'Engineer'
  });
  const engineerNode = prepared.operation.draft.agent_node_id;
  expect(engineerNode).toBeTruthy();
  expect(prepared.response).toBe(
    'A draft for Engineer is ready in the task form: “Bounce the stems”. Nothing is created until you save it.'
  );

  await page.waitForURL(url => url.pathname === albumPath);
  await expect(form.root).toBeVisible({ timeout: 20_000 });
  await expect(form.heading).toHaveText('Review task for Engineer');
  await expect(form.title).toHaveValue('Bounce the stems');
  await expect(form.brief).toHaveValue(ask);
  // The proposed agent is the one shown: the non-entry Engineer, not the entry agent.
  await expect(form.agent).toHaveValue(`node:${engineerNode}`);
  await expect(form.agent.locator('option:checked')).toHaveText('Engineer');
  await shot('01-draft-in-the-real-task-form.png');
  // Preparing and showing the draft created nothing.
  expect(await tasks()).toHaveLength(0);

  const tab = await here.tabState();
  const recorded = await readOperation(request, prepared.operation.id, tab.tab);
  expect(recorded.body.operation.status).toBe('prepared');
  expect(recorded.body.operation.pending).toBe(0);
  expect(recorded.body.operation.receipts.map((r: any) => r.kind)).toEqual([
    'draft_prepared',
    'view_opened',
    'draft_opened'
  ]);

  // --- 2. The user edits the brief and the agent, then dismisses the form.
  await form.brief.fill('Export 24-bit stems for mastering by Friday.');
  await form.agent.selectOption({ label: 'Mixer' });
  page.once('dialog', dialog => dialog.accept()); // the form's own "Discard unsaved task changes?"
  await form.close.click();
  await expect(form.root).toBeHidden();
  expect(await tasks()).toHaveLength(0);
  await here.open();
  await expect(here.status).toHaveText(
    'Draft for Engineer: “Bounce the stems”. Review it in the task form. Nothing is created until you save it.'
  );
  await shot('02-dismissed-draft-still-offered.png');

  // --- 3. Reopening shows the proposal again and still creates nothing.
  await here.actions.getByRole('button', { name: 'Open draft again' }).click();
  await expect(form.root).toBeVisible();
  await expect(form.title).toHaveValue('Bounce the stems');
  await expect(form.brief).toHaveValue(ask); // the discarded edit is not resurrected
  await expect(form.agent).toHaveValue(`node:${engineerNode}`);
  expect(await tasks()).toHaveLength(0);
  // Reopening is a local view: the server's record did not change.
  expect(
    (await readOperation(request, prepared.operation.id, tab.tab)).body.operation.revision
  ).toBe(recorded.body.operation.revision);

  // --- 4. The user's corrections are what the form's own save creates.
  await form.title.fill('Bounce the stems for mastering');
  await form.brief.fill('Export 24-bit stems for mastering by Friday.');
  const created = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/orchestration/tasks' &&
      response.request().method() === 'POST'
  );
  await form.save.click();
  expect((await created).status()).toBe(201);
  await expect(form.root).toBeHidden();
  const saved = await tasks();
  expect(saved).toHaveLength(1);
  expect(saved[0]).toMatchObject({
    description: 'Bounce the stems for mastering',
    details: 'Export 24-bit stems for mastering by Friday.',
    to: 'Engineer',
    assigned_node_id: engineerNode
  });
  expect(saved[0].status).not.toBe('in_progress'); // saving started nothing
  await here.open();
  // The save created the task and bound it to this request. It did not start it.
  await expect(here.status).toHaveText(
    'Task created for Engineer: “Bounce the stems for mastering”. It has not started.'
  );
  await shot('03-saved-by-the-user.png');
  // The same proposal cannot be saved twice from here.
  await expect(here.actions.getByRole('button', { name: /Open draft/ })).toHaveCount(0);
  const afterSave = (await readOperation(request, prepared.operation.id, tab.tab)).body.operation;
  expect(afterSave.status).toBe('reviewed');
  expect(afterSave.task.task_id).toBe(saved[0].id);
  expect(saved[0].assistant_operation_id).toBe(prepared.operation.id);

  // --- 5. Watch off: an explicit Open draft uses the same form.
  await page.goto('/');
  await here.open();
  await here.setWatch(false);
  const offered = await here.send(
    'Create a task for Mixer in the Album-1 task fixture workspace to mix the chorus'
  );
  expect(offered.operation).toMatchObject({ kind: 'task', status: 'prepared', watching: false });
  expect(offered.operation.commands ?? []).toEqual([]);
  await expect(here.status).toHaveText(
    'Draft ready for Mixer: “Mix the chorus”. Nothing is created until you save it.'
  );
  expect(new URL(page.url()).pathname).toBe('/');
  await shot('04-watch-off-open-draft.png');
  await here.actions.getByRole('button', { name: 'Open draft' }).click();
  await page.waitForURL(url => url.pathname === albumPath);
  await expect(form.root).toBeVisible({ timeout: 20_000 });
  await expect(form.title).toHaveValue('Mix the chorus');
  await expect(form.agent.locator('option:checked')).toHaveText('Mixer');
  await form.close.click(); // unchanged, so the form closes without asking
  await expect(form.root).toBeHidden();
  expect(await tasks()).toHaveLength(1);

  // --- 6. No agent, or an agent who is not in this workspace: Ori asks, prepares nothing.
  await page.goto(albumPath);
  await here.open();
  const unnamed = await here.send('Create a task to tidy the session');
  expect(unnamed.operation).toBeUndefined();
  expect(unnamed.response).toContain('agent_not_named');
  expect(unnamed.response).toContain('Mixer, Engineer');
  const stranger = await here.send('Create a task for Editor to proofread the notes');
  expect(stranger.operation).toBeUndefined();
  expect(stranger.response).toContain('agent_not_in_workspace');
  await expect(form.root).toBeHidden();

  // --- 7. Talking about tasks is not a request to prepare one.
  const chat = await here.send('What task should Mixer do next?');
  expect(chat.operation).toBeUndefined();
  expect(chat.requires_confirmation).toBeFalsy();

  // Exactly the one task the user saved exists; nothing else was created.
  expect(await tasks()).toHaveLength(1);
  expect((await listWorkspaces(request)).length).toBe(workspacesBefore);
});
