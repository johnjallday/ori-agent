import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Production browser, Route/Ask, Ollama adapter, canonical stores and hydration.
// Only the loopback provider's text/generation barrier is deterministic. This
// is NOT vendor-model behavior evidence and never confirms a setup operation.
test('wt demo: page/selection context, pinned generation, saved and legacy history, unchanged review', async ({
  page,
  request
}, testInfo) => {
  const sandbox = process.env.ORI_WORKSPACE_HISTORY_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py');
  test.setTimeout(120_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const folder = join(sandbox!, 'Documents', 'Album-5 history fixture');
  await mkdir(folder, { recursive: true, mode: 0o750 });
  const source = join(folder, 'notes.txt');
  const sentinel = 'Unlinked folder body must remain unread in this navigation demo.';
  await writeFile(source, sentinel, { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  expect(
    (
      await request.post('/api/settings/workspace-root', {
        data: { workspace_root: '' }
      })
    ).ok()
  ).toBeTruthy();
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-history-hire',
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
      request_id: 'workspace-history-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  assistant = (await hq.json()).personal_assistant;
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
    return (await (await request.get('/api/workspaces')).json()).folders.find(
      (row: any) => row.name === name
    );
  };
  const group = await create('Music portfolio history fixture', { kind: 'group' });
  const a = await create('Album-1 history fixture', { parent_id: group.id });
  const b = await create('Second project history fixture');
  const initialWorkspaces = (await (await request.get('/api/workspaces')).json()).folders;

  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  const label = page.locator('#personalAssistantWorkspaceContext');
  await expect(label).toHaveText('Context · App-wide');
  await page.waitForFunction(() => Boolean((window as any).OriHomeCockpit));
  await page.evaluate(
    id => (window as any).OriHomeCockpit.select(id, { openModal: false }),
    group.id
  );
  await expect(label).toHaveText('Context · Group: Music portfolio history fixture');
  // Groups become browsing subjects without becoming legacy execution targets.
  expect(await page.evaluate(() => (window as any).oriHomeRouteContext.workspace_id)).toBe('');
  await page.evaluate(id => (window as any).OriHomeCockpit.select(id, { openModal: false }), a.id);
  await expect(label).toHaveText(
    'Context · Project: Album-1 history fixture · Music portfolio history fixture'
  );
  await page
    .locator('#personalAssistantInput')
    .fill('Draft kept through group and project navigation');
  await page.goto(`/workspaces/${group.folder_slug}/canvas`);
  await expect(label).toHaveText('Context · Group: Music portfolio history fixture');
  await page.goto(`/workspaces/${a.folder_slug}/canvas`);
  await expect(label).toHaveText(
    'Context · Project: Album-1 history fixture · Music portfolio history fixture'
  );
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'Draft kept through group and project navigation'
  );

  // Canonical review created while looking at A; no model or approval needed.
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await page
    .locator('#personalAssistantFolderSetupCandidate')
    .selectOption({ label: 'Album-5 history fixture' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  const current = await page.evaluate(() =>
    (window as any).PersonalAssistantFolderContext.current()
  );
  const historyURL = `/api/home-assistant/conversations/${current.conversationId}`;
  const before = await (await request.get(historyURL)).json();
  expect(before.folder_reviews[current.offerId].status).toBe('pending');

  // Navigate before Send: B owns the next scope, never the HQ session.
  await page.goto(`/workspaces/${b.folder_slug}/canvas`);
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await expect(card).toBeVisible();
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
  const first = await send('Tell me about this workspace');
  expect(first.workspace_context.subject.id).toBe(b.id);
  expect(first.conversation.id).toBe(current.conversationId);
  await expect(page.locator('.personal-assistant-message__context').last()).toHaveText(
    'Project: Second project history fixture'
  );
  expect((await (await request.get(historyURL)).json()).folder_reviews[current.offerId]).toEqual(
    before.folder_reviews[current.offerId]
  );

  // Actual generation is held at the provider, not an HTTP response mock.
  await page.goto(`/workspaces/${a.folder_slug}/canvas`);
  await expect(label).toContainText('Album-1 history fixture');
  await page.locator('#personalAssistantInput').fill('Hold this workspace reply');
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
    .toBe(a.id);
  await page.locator('#personalAssistantInput').fill('New unsent draft for the second project');
  await page.evaluate(slug => {
    history.pushState({}, '', `/workspaces/${slug}/canvas`);
    dispatchEvent(new PopStateEvent('popstate'));
  }, b.folder_slug);
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await page.goBack();
  await expect(label).toContainText('Album-1 history fixture');
  await page.goForward();
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await writeFile(join(provider!, 'release'), '', { mode: 0o600 });
  const answer = await (await delayed).json();
  expect(answer.conversation.stored).toBe(true);
  expect(answer.workspace_context.subject.id).toBe(a.id);
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  await expect(page.locator('.personal-assistant-message__context').last()).toHaveText(
    'Project: Album-1 history fixture · Music portfolio history fixture'
  );
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'New unsent draft for the second project'
  );
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await page.reload();
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await expect(page.locator('.personal-assistant-message__context').last()).toHaveText(
    'Project: Album-1 history fixture · Music portfolio history fixture'
  );
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'New unsent draft for the second project'
  );
  await expect(card).toBeVisible();
  const saved = await (await request.get(historyURL)).json();
  const turns = saved.messages.filter((row: any) => row.role !== 'folder_context');
  expect(turns.map((row: any) => row.workspace_context.subject.id)).toEqual([
    b.id,
    b.id,
    a.id,
    a.id
  ]);
  expect(turns.every((row: any) => row.workspace_context.historical)).toBe(true);
  expect(saved.folder_reviews[current.offerId]).toEqual(before.folder_reviews[current.offerId]);
  const session = await (await request.get(`/api/sessions/${current.conversationId}`)).json();
  expect(session.folder_id).toBe(assistant.hq_workspace_id);
  expect(
    (await (await request.get('/api/workspaces')).json()).folders.map((row: any) => row.id).sort()
  ).toEqual(initialWorkspaces.map((row: any) => row.id).sort());
  expect(await readFile(source, 'utf8')).toBe(sentinel);
  await page.locator('.personal-assistant-message__context').last().scrollIntoViewIfNeeded();
  await testInfo.attach('saved-navigation-history.png', {
    body: await page.locator('#personalAssistantPanel').screenshot({
      path: join(evidence, 'saved-navigation-history.png')
    }),
    contentType: 'image/png'
  });

  // Generic canonical message writes cannot mint turn attribution. Legacy rows
  // are readable but must not acquire B merely because the page is showing B.
  expect(
    (
      await request.post(`/api/sessions/${current.conversationId}/messages`, {
        data: { role: 'assistant', content: 'Legacy fixture reply without workspace metadata.' }
      })
    ).status()
  ).toBe(201);
  await page.reload();
  await expect(page.locator('.personal-assistant-message__context').last()).toHaveText(
    'Earlier workspace unknown'
  );
  await expect(label).toHaveText('Context · Project: Second project history fixture');
  await page.goto('/settings');
  await expect(label).toHaveText('Context · App-wide');
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'New unsent draft for the second project'
  );
  await writeFile(
    join(evidence, 'saved-navigation-history.json'),
    JSON.stringify(
      {
        evidence:
          'wt demo real host + production Ollama adapter + loopback deterministic provider; no vendor model',
        scopes: turns.map((row: any) => ({
          role: row.role,
          subject_id: row.workspace_context.subject.id
        })),
        reviewStatus: saved.folder_reviews[current.offerId].status,
        reviewUnchanged: true,
        hqOwnerUnchanged: true,
        unlinkedBodyUnchanged: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
