import { test, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Production browser, Route/Ask, Ollama adapter, brokered readers and canonical
// stores under `wt demo`. The loopback provider is a deterministic stand-in for
// a tool-using model: it chooses readers from the user's words and repeats only
// what a reader returned. It holds no workspace data, so every fact in a reply
// came through Ori's real readers. This is not vendor-model behavior evidence.
test('wt demo: notes and tasks are read, cited, re-read when they change, and never invented', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_SOURCES_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --sources');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });

  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-sources-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'workspace-sources-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const create = async (name: string) => {
    const response = await request.post('/api/workspaces', { data: { name } });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await workspaces()).find((row: any) => row.name === name);
  };
  const note = async (workspaceId: string, name: string, content: string) => {
    const response = await request.post(`/api/workspaces/${workspaceId}/notes`, {
      data: { name, content }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const body = await response.json();
    return body.note?.id || body.id;
  };
  const project = await create('Album-1 sources fixture');
  const other = await create('Other sources fixture');
  // The sentence that matters sits well past any list preview.
  const opening = 'Master the single before Friday';
  const noteId = await note(
    project.id,
    'Release notes',
    `${opening}. ${'Background that makes this note longer than a preview. '.repeat(6)}Then finish the artwork.`
  );
  const foreign = 'FOREIGN_NOTE_BODY_MUST_NOT_BE_READ';
  await note(other.id, 'Release notes', `${foreign}. This note belongs to another workspace.`);
  const ticket = await request.post(`/api/workspaces/${project.id}/tickets`, {
    data: {
      title: 'Master the single',
      description: 'Send the stems to mastering.',
      state: 'backlog'
    }
  });
  expect(ticket.status(), await ticket.text()).toBe(201);
  const ticketBody = await ticket.json();
  const ticketId = ticketBody.ticket?.id || ticketBody.id;
  expect(noteId && ticketId).toBeTruthy();
  const notesOf = async (workspaceId: string) => {
    const body = await (await request.get(`/api/workspaces/${workspaceId}/notes`)).json();
    return body.notes || body;
  };
  const ticketsOf = async (workspaceId: string) => {
    const body = await (await request.get(`/api/workspaces/${workspaceId}/tickets`)).json();
    return body.tickets || body;
  };
  const before = {
    workspaces: (await workspaces()).length,
    notes: (await notesOf(project.id)).length,
    tickets: (await ticketsOf(project.id)).length
  };

  const label = page.locator('#personalAssistantWorkspaceContext');
  const answers = page.locator('#homeAssistantConversation [data-message-role="assistant"]');
  const openDrawer = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
  };
  const send = async (prompt: string) => {
    await page.locator('#personalAssistantInput').fill(prompt);
    const response = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const body = await (await response).json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(body.conversation.stored).toBe(true);
    return body;
  };
  const shot = (name: string) =>
    page.locator('#personalAssistantPanel').screenshot({ path: join(evidence, name) });

  await page.goto(`/workspaces/${project.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toHaveText('Context · Project: Album-1 sources fixture');

  // 1. A greeting on a populated project reads nothing and claims no source.
  const greeting = await send('Hello there');
  expect(greeting.workspace_context.subject.id).toBe(project.id);
  expect(greeting.workspace_context.sources).toBeUndefined();
  expect(greeting.folder_setup_suggestion).toBeUndefined();
  await expect(answers.last().locator('.personal-assistant-message__sources')).toHaveCount(0);

  // 2. A substantive question reads the whole note and the task. The reply's
  // markers are checked: the one for something never read is removed.
  const advice = await send('Based on my release notes and open tasks, what should I do next?');
  expect(advice.response).toContain(`${opening}. [S1]`);
  expect(advice.response).toContain('“Master the single” is');
  expect(advice.response).toContain('[S2]');
  expect(advice.response).toContain('not a recorded fact');
  expect(advice.response).not.toContain('[S9]');
  expect(advice.response).not.toContain(foreign);
  const [noteSource, taskSource] = advice.workspace_context.sources;
  expect(advice.workspace_context.sources).toHaveLength(2);
  expect(noteSource).toMatchObject({
    key: 'S1',
    kind: 'note',
    id: noteId,
    label: 'Release notes',
    workspace: 'Album-1 sources fixture',
    coverage: 'full',
    cited: true,
    href: `/workspaces/${project.folder_slug}/notes/${noteId}`
  });
  expect(taskSource).toMatchObject({
    key: 'S2',
    kind: 'task',
    id: ticketId,
    cited: true,
    href: `/workspaces/${project.folder_slug}/task/${ticketId}`
  });
  // References only: what was read is not copied into the saved reference.
  expect(JSON.stringify(advice.workspace_context)).not.toContain('Background that makes');

  // The disclosure is reachable and operable from the keyboard.
  const sources = answers.last().locator('.personal-assistant-message__sources');
  const summary = sources.locator('summary');
  await expect(summary).toHaveText('Sources used (2)');
  await summary.focus();
  await page.keyboard.press('Enter');
  await expect(sources).toHaveAttribute('open', '');
  const noteLink = sources.getByRole('link', { name: 'Note: Release notes', exact: true });
  const taskLink = sources.getByRole('link', { name: /^Task: .*Master the single$/ });
  await expect(noteLink).toHaveAttribute('href', noteSource.href);
  await expect(taskLink).toHaveAttribute('href', taskSource.href);
  await expect(sources).toContainText('Album-1 sources fixture · read in full');
  await page.keyboard.press('Tab');
  await expect(noteLink).toBeFocused();
  await sources.scrollIntoViewIfNeeded();
  await shot('sources-used.png');
  // Both links are real pages in the app.
  for (const href of [noteSource.href, taskSource.href])
    expect((await request.get(href)).status(), href).toBe(200);
  await noteLink.click();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${project.folder_slug}/notes/${noteId}$`));
  await page.goto(`/workspaces/${project.folder_slug}/canvas`);
  await openDrawer();

  // 3. The records change. The next reply reads them as they are now.
  const changed = await request.put(`/api/notes/${noteId}`, {
    data: {
      name: 'Release notes',
      content: 'Plan changed: finish the artwork first. Mastering waits.'
    }
  });
  expect(changed.ok(), await changed.text()).toBeTruthy();
  const moved = await request.post(`/api/workspaces/${project.id}/tickets/${ticketId}/transition`, {
    data: { to: 'ready' }
  });
  expect(moved.ok(), await moved.text()).toBeTruthy();
  const updated = await send('Based on my release notes and open tasks, what should I do next?');
  expect(updated.response).toContain('Plan changed: finish the artwork first. [S1]');
  expect(updated.response).toContain('“Master the single” is Ready [S2]');
  expect(updated.response).not.toContain(opening);
  expect(updated.workspace_context.sources[0].version).not.toBe(noteSource.version);
  expect(updated.workspace_context.sources[1].version).not.toBe(taskSource.version);
  await answers.last().locator('.personal-assistant-message__sources summary').click();
  await answers.last().scrollIntoViewIfNeeded();
  await shot('sources-after-change.png');

  // 4. A note that is not there is reported as not read, never as empty.
  const missing = await send('Read the note Missing plan');
  expect(missing.response).toContain(
    'could not read a note titled Missing plan (note_not_found_in_this_workspace)'
  );
  expect(missing.response).toContain('not saying this workspace has no such plan');
  expect(missing.workspace_context.sources).toBeUndefined();
  await expect(answers.last().locator('.personal-assistant-message__sources')).toHaveCount(0);
  await answers.last().scrollIntoViewIfNeeded();
  await shot('sources-missing-note.png');
  // One source that could not be read does not stop the conversation: the next
  // question reads and cites again.
  const retry = await send('Based on my release notes and open tasks, what should I do next?');
  expect(retry.workspace_context.sources).toHaveLength(2);
  expect(retry.response).toContain('Plan changed: finish the artwork first. [S1]');

  // 5. Reload: each saved reply still shows what it read then, as history, and
  // the earlier reply is not rewritten into the later read.
  const conversationId = advice.conversation.id;
  await page.reload();
  await openDrawer();
  const savedSources = page.locator(
    '#homeAssistantConversation .personal-assistant-message__sources'
  );
  await expect(savedSources).toHaveCount(3);
  await savedSources.first().locator('summary').click();
  await expect(savedSources.first()).toContainText(
    'What this reply read at the time. It is not a fresh read'
  );
  const saved = await (
    await request.get(`/api/home-assistant/conversations/${conversationId}`)
  ).json();
  const savedAnswers = saved.messages.filter((row: any) => row.role === 'assistant');
  expect(savedAnswers.map((row: any) => row.workspace_context.sources?.length || 0)).toEqual([
    0, 2, 2, 0, 2
  ]);
  expect(savedAnswers[1].workspace_context.sources[0].version).toBe(noteSource.version);
  expect(savedAnswers[2].workspace_context.sources[0].version).toBe(
    updated.workspace_context.sources[0].version
  );
  expect(savedAnswers.every((row: any) => row.workspace_context.historical)).toBe(true);
  expect(JSON.stringify(saved)).not.toContain(foreign);
  await savedSources.first().scrollIntoViewIfNeeded();
  await shot('sources-saved-history.png');

  // Reading wrote nothing.
  expect((await workspaces()).length).toBe(before.workspaces);
  expect((await notesOf(project.id)).length).toBe(before.notes);
  expect((await ticketsOf(project.id)).length).toBe(before.tickets);

  await writeFile(
    join(evidence, 'sources-demo.json'),
    JSON.stringify(
      {
        evidence:
          'wt demo real host + production Ollama adapter + loopback deterministic tool-using provider; no vendor model',
        greeting: { sources: 0 },
        advice: {
          sources: advice.workspace_context.sources.map((source: any) => ({
            key: source.key,
            kind: source.kind,
            label: source.label,
            coverage: source.coverage,
            cited: source.cited,
            href: source.href
          })),
          unreadMarkerRemoved: !advice.response.includes('[S9]')
        },
        afterChange: {
          noteVersionChanged: updated.workspace_context.sources[0].version !== noteSource.version,
          taskStateNow: 'Ready'
        },
        missingNote: 'reported as not read (note_not_found_in_this_workspace), not as empty',
        savedHistory: savedAnswers.map((row: any) => row.workspace_context.sources?.length || 0),
        otherWorkspaceNoteRead: false,
        writes: { workspaces: 0, notes: 0, tickets: 0 }
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
