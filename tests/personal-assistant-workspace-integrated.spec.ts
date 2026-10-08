import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// One conversation, start to finish, on the production browser and host under
// `wt demo`: a folder is attached and reviewed on a group's page, the user moves
// to a project inside it, asks about its notes and tasks, asks about its files,
// navigates while a reply is being written, loses access to a linked folder,
// and only then confirms the review. Each earlier demo covers one of these in
// its own sandbox; this checks that they hold together in a single session.
// The loopback provider is the same deterministic stand-in: it is not a vendor
// model, and nothing here opens REAPER or edits a file.
test('wt demo: one conversation from setup review through notes, files, a delayed reply and removed access to confirmation', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_INTEGRATED_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --integrated');
  test.setTimeout(240_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });

  // The folder that is attached and reviewed, never linked before confirmation.
  const attachedBody = 'ATTACHED_ALBUM_5_BODY_MUST_NOT_REACH_THE_MODEL';
  const album5 = join(sandbox!, 'Documents', 'Album-5 integrated fixture');
  await mkdir(album5, { recursive: true, mode: 0o750 });
  await writeFile(join(album5, 'notes.txt'), attachedBody, { mode: 0o600 });
  // Album-1's own linked folder.
  const assets = join(sandbox!, 'Music', 'Album-1 assets');
  await mkdir(join(assets, 'lyrics'), { recursive: true, mode: 0o750 });
  await writeFile(join(assets, 'lyrics', 'bridge.txt'), 'The bridge is in D minor.', {
    mode: 0o600
  });
  await writeFile(join(provider!, 'sentinels.json'), JSON.stringify([attachedBody]), {
    mode: 0o600
  });
  await writeFile(
    join(provider!, 'log-watch.json'),
    JSON.stringify(['The bridge is in D minor.', 'Master the single before Friday', assets]),
    { mode: 0o600 }
  );

  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-integrated-hire',
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
      request_id: 'workspace-integrated-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const create = async (name: string, extra: Record<string, unknown> = {}) => {
    if (extra.kind === 'group') {
      const reviewed = await request.post('/api/workspaces/template-agent-plan', {
        data: { group_roster: true, group_name: name }
      });
      expect(reviewed.ok(), await reviewed.text()).toBeTruthy();
      const plan = await reviewed.json();
      extra = {
        ...extra,
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: plan.revision,
          expectations: plan.agents.map((agent: any, index: number) => ({
            index,
            name: agent.name,
            action: agent.action
          }))
        }
      };
    }
    const response = await request.post('/api/workspaces', { data: { name, ...extra } });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await workspaces()).find((row: any) => row.name === name);
  };
  const group = await create('Music portfolio integrated fixture', { kind: 'group' });
  const album1 = await create('Album-1 integrated fixture', { parent_id: group.id });
  const noteResponse = await request.post(`/api/workspaces/${album1.id}/notes`, {
    data: {
      name: 'Release notes',
      content: `Master the single before Friday. ${'Background for a longer note. '.repeat(8)}`
    }
  });
  expect(noteResponse.ok(), await noteResponse.text()).toBeTruthy();
  const ticket = await request.post(`/api/workspaces/${album1.id}/tickets`, {
    data: {
      title: 'Master the single',
      description: 'Send the stems to mastering.',
      state: 'backlog'
    }
  });
  expect(ticket.status(), await ticket.text()).toBe(201);
  const linked = await request.post(`/api/workspaces/${album1.id}/directories`, {
    data: { name: 'Album-1 assets', path: assets }
  });
  expect(linked.ok(), await linked.text()).toBeTruthy();
  const directoryId = (await linked.json()).directory.id as string;
  const count = async (kind: 'notes' | 'tickets') => {
    const body = await (await request.get(`/api/workspaces/${album1.id}/${kind}`)).json();
    return (body[kind] || body).length as number;
  };
  const original = await (await request.get(`/api/workspaces/${album1.id}`)).json();
  const before = {
    workspaces: (await workspaces()).length,
    notes: await count('notes'),
    tickets: await count('tickets')
  };

  const label = page.locator('#personalAssistantWorkspaceContext');
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  const answers = page.locator('#homeAssistantConversation [data-message-role="assistant"]');
  const input = page.locator('#personalAssistantInput');
  const openDrawer = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
  };
  const send = async (prompt: string) => {
    await input.fill(prompt);
    const response = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const body = await (await response).json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(body.conversation.stored).toBe(true);
    return body;
  };
  const current = () =>
    page.evaluate(() => (window as any).PersonalAssistantFolderContext.current());
  const history = async (id: string) =>
    (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  const shot = (name: string) =>
    page.locator('#personalAssistantPanel').screenshot({ path: join(evidence, name) });
  const noLeak = (value: unknown, what: string) => {
    const text = JSON.stringify(value);
    for (const forbidden of [attachedBody, sandbox!, assets])
      expect(text, `${what} carried ${forbidden}`).not.toContain(forbidden);
  };

  // 1. On the group's page: attach Album-5, ask to add it, and review. The card
  // names the group as the destination. Nothing is created.
  await page.goto(`/workspaces/${group.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toHaveText('Context · Group: Music portfolio integrated fixture');
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderPreview')).toBeVisible();
  const asked = await send('Add this to my workspace');
  expect(asked.workspace_context.subject.id).toBe(group.id);
  await page.getByRole('button', { name: 'Optional: review setup', exact: true }).click();
  await page
    .locator('#personalAssistantFolderSetupCandidate')
    .selectOption({ label: 'Album-5 integrated fixture' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  await expect(card).toContainText(
    'Destination: Group “Music portfolio integrated fixture” · separate project.'
  );
  const review = await current();
  const pending = (await history(review.conversationId)).folder_reviews[review.offerId];
  expect(pending).toMatchObject({ status: 'pending', operation: 'create_project_workspace' });
  expect(pending.destination).toMatchObject({ workspace_id: group.id, kind: 'group' });
  expect(await workspaces()).toHaveLength(before.workspaces);

  // 2. Move to Album-1. The same conversation and its review come along; the
  // review still names the group, not the page now on screen.
  await page.goto(`/workspaces/${album1.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toContainText('Project: Album-1 integrated fixture');
  await expect(card).toContainText(
    'Destination: Group “Music portfolio integrated fixture” · separate project.'
  );

  // 3. Notes and tasks, in that same conversation. The reply reads Album-1's
  // records and cites them; the model is still shown the pending review; the
  // review itself is untouched.
  const advice = await send('Based on my release notes and open tasks, what should I do next?');
  expect(advice.conversation.id).toBe(review.conversationId);
  expect(advice.workspace_context.subject.id).toBe(album1.id);
  expect(advice.response).toContain('Master the single before Friday. [S1]');
  expect(advice.response).toContain('[S2]');
  expect(advice.response).not.toContain('[S9]');
  expect(advice.workspace_context.sources.map((source: any) => source.kind)).toEqual([
    'note',
    'task'
  ]);
  expect(advice.folder_review_context).toMatchObject({
    status: 'awaiting_confirmation',
    destination_name: 'Music portfolio integrated fixture'
  });
  expect((await history(review.conversationId)).folder_reviews[review.offerId]).toEqual(pending);
  noLeak(advice, 'the notes and tasks reply');
  const adviceSources = answers.last().locator('.personal-assistant-message__sources');
  await adviceSources.locator('summary').click();
  await expect(adviceSources.getByRole('link', { name: 'Note: Release notes' })).toBeVisible();
  await adviceSources.scrollIntoViewIfNeeded();
  await shot('integrated-review-and-sources.png');

  // 4. A file Album-1 holds is read and attributed. The attached Album-5 folder
  // is still only metadata: its file never reaches the model.
  const lyrics = await send('Summarize the lyrics file in Album-1 assets');
  expect(lyrics.response).toContain('“The bridge is in D minor.” [S1]');
  expect(lyrics.workspace_context.sources).toHaveLength(1);
  expect(lyrics.workspace_context.sources[0]).toMatchObject({
    kind: 'file',
    label: 'bridge.txt',
    detail: 'Linked folder “Album-1 assets” · lyrics/bridge.txt',
    coverage: 'full'
  });
  noLeak(lyrics, 'the file reply');

  // 5. A reply is held at the provider while the user goes back to the group.
  // It arrives labeled with Album-1, where it was asked; the context line shows
  // the group; the draft typed meanwhile is kept.
  await input.fill('Hold this workspace reply');
  const delayed = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask'
  );
  await page.locator('#personalAssistantSend').click();
  await expect
    .poll(async () => {
      try {
        return JSON.parse(await readFile(join(provider!, 'accepted.json'), 'utf8')).subject_id;
      } catch {
        return '';
      }
    })
    .toBe(album1.id);
  await input.fill('A draft typed while the reply was being written');
  await page.evaluate(slug => {
    history.pushState({}, '', `/workspaces/${slug}/canvas`);
    dispatchEvent(new PopStateEvent('popstate'));
  }, group.folder_slug);
  await expect(label).toHaveText('Context · Group: Music portfolio integrated fixture');
  await writeFile(join(provider!, 'release'), '', { mode: 0o600 });
  const held = await (await delayed).json();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(held.conversation.stored).toBe(true);
  expect(held.workspace_context.subject.id).toBe(album1.id);
  await expect(page.locator('.personal-assistant-message__context').last()).toContainText(
    'Project: Album-1 integrated fixture'
  );
  await expect(label).toHaveText('Context · Group: Music portfolio integrated fixture');
  await expect(input).toHaveValue('A draft typed while the reply was being written');

  // 6. Album-1's linked folder is removed. The next request cannot read it; the
  // earlier reply keeps the source it read then.
  await page.goto(`/workspaces/${album1.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toContainText('Project: Album-1 integrated fixture');
  const removed = await request.delete(`/api/workspaces/${album1.id}/directories/${directoryId}`);
  expect(removed.ok(), await removed.text()).toBeTruthy();
  const after = await send('Summarize the lyrics file in Album-1 assets');
  expect(after.response).not.toContain('D minor');
  expect(
    (after.workspace_context.sources ?? []).filter((source: any) => source.label === 'bridge.txt')
  ).toHaveLength(0);
  noLeak(after, 'the reply after access was removed');

  // 7. Only now is the review confirmed, on its card. One project is created in
  // the group it named all along; Album-1 and both folders are unchanged.
  await expect(card).toContainText(
    'Destination: Group “Music portfolio integrated fixture” · separate project.'
  );
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect
    .poll(async () => (await history(review.conversationId)).folder_review_context?.status, {
      timeout: 60_000
    })
    .toBe('completed');
  const completed = (await history(review.conversationId)).folder_reviews[review.offerId];
  expect(completed.outcome.parent).toMatchObject({ workspace_id: group.id });
  const afterCreate = await workspaces();
  expect(afterCreate).toHaveLength(before.workspaces + 1);
  const created = afterCreate.find((row: any) => row.id === completed.outcome.workspace_id);
  expect(created.parent_id).toBe(group.id);
  expect(created.id).not.toBe(album1.id);
  const album1Now = await (await request.get(`/api/workspaces/${album1.id}`)).json();
  for (const key of ['name', 'parent_id', 'agent_instances', 'tasks'])
    expect(album1Now[key]).toEqual(original[key]);
  expect(await count('notes')).toBe(before.notes);
  expect(await count('tickets')).toBe(before.tickets);
  expect(await readFile(join(album5, 'notes.txt'), 'utf8')).toBe(attachedBody);
  expect(await readFile(join(assets, 'lyrics', 'bridge.txt'), 'utf8')).toBe(
    'The bridge is in D minor.'
  );
  await shot('integrated-after-confirmation.png');

  // 8. Reload. The whole conversation is there with each turn's workspace and
  // the sources it read at the time; nothing reached the model that should not.
  await page.reload();
  await openDrawer();
  await expect(answers.first()).toBeVisible();
  const saved = await history(review.conversationId);
  const replies = saved.messages.filter((row: any) => row.role === 'assistant');
  expect(replies.length).toBeGreaterThanOrEqual(5);
  const subjects = replies.map((row: any) => row.workspace_context?.subject?.id);
  expect(subjects[0]).toBe(group.id);
  expect(subjects.slice(1)).toEqual(subjects.slice(1).map(() => album1.id));
  const kinds = replies.map((row: any) =>
    (row.workspace_context?.sources ?? []).map((source: any) => source.kind).join('+')
  );
  expect(kinds).toContain('note+task');
  expect(kinds).toContain('file');
  noLeak(saved, 'the saved conversation');
  await expect(
    page.locator('.personal-assistant-message__sources summary', { hasText: 'Sources used (2)' })
  ).toHaveCount(1);
  const audit = JSON.parse(await readFile(join(provider!, 'provider-audit.json'), 'utf8'));
  expect(audit.hits, 'the attached folder’s file reached the provider').toEqual([]);

  await writeFile(
    join(evidence, 'integrated-demo.json'),
    JSON.stringify(
      {
        boundary:
          'wt demo + production Ollama adapter + deterministic loopback provider, one conversation in one sandbox. Not a vendor model; no REAPER, no audio, no file edited',
        conversation: review.conversationId,
        steps: [
          'review prepared on the group page; nothing created',
          'moved to Album-1; the review still names the group',
          'notes and tasks read and cited; the review unchanged and still shown to the model',
          'Album-1 file read and attributed; the attached folder stayed metadata',
          'reply held during navigation; labeled Album-1 on arrival; draft kept',
          'linked folder removed; the next request read nothing from it',
          'review confirmed on its card; one project created in the group',
          'reload: every turn keeps its workspace and sources'
        ],
        replySubjects: subjects.map((id: string) => (id === group.id ? 'group' : 'Album-1')),
        replySources: kinds,
        workspacesCreated: afterCreate.length - before.workspaces,
        album1Unchanged: true,
        notesAndTicketsUnchanged: true,
        providerRequests: audit.requests,
        attachedFolderBodyReachedProvider: audit.hits.length > 0
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
