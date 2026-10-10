import { test, expect } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Real built drawer, Route -> Ask -> Ollama adapter and canonical Sessions.
// Only the loopback model is deterministic. This proves reachability, routing
// and review boundaries, not conversational reasoning or external discovery.
test('wt demo: brainstorming and corrections stay in one conversation; creation opens an unconfirmed review', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_DISCOVERY_DISCUSSION_SANDBOX;
  test.skip(!sandbox, 'Use scripts/assistant-workspace-demo.py --discovery-discussion');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence/assistant-conversation-discovery/group-1');
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
      request_id: 'discovery-discussion-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me think through ideas.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'discovery-discussion-hq',
      if_version: assistant.state_version,
      name: 'Fictional HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const created = await request.post('/api/workspaces', {
    data: { name: 'Fictional community notes' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const project = (await workspaces()).find((row: any) => row.name === 'Fictional community notes');
  const before = (await workspaces()).length;
  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  const routes: any[] = [];
  const open = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
    if (!(await drawer.isVisible())) await page.locator('#personalAssistantLauncher').click();
    await expect(input).toBeEnabled();
  };
  const send = async (prompt: string, discussion = true) => {
    await input.fill(prompt);
    const routeWait = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/route'
    );
    const askWait = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const route = await routeWait;
    expect(route.status(), await route.text()).toBe(200);
    const routed = await route.json();
    routes.push(routed);
    expect(routed.intent).toBe(discussion ? 'assistant_conversation' : 'workspace_create');
    const answer = await askWait;
    expect(answer.status(), await answer.text()).toBe(200);
    const body = await answer.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    if (discussion) {
      expect(body.conversation.stored).toBe(true);
      expect(body.requires_confirmation).not.toBe(true);
      expect(body.response).toContain('Deterministic discussion fixture');
    }
    expect((await workspaces()).length).toBe(before);
    return body;
  };

  await page.goto('/');
  await open();
  const first = await send('Should we build a community platform for musicians?');
  const id = first.conversation.id;
  const correction = await send(
    "No, I don't want to develop anyone's talent. Let's compare community ideas."
  );
  expect(correction.conversation.id).toBe(id);
  await drawer.screenshot({ path: join(evidence, 'home-discussion-fixture.png') });

  await page.goto(`/workspaces/${project.folder_slug}/assistant`);
  await open();
  const workspace = await send('Could Ori help people build communities?');
  expect(workspace.conversation.id).toBe(id);
  const skillQuestion = await send('Is there a Telegram skill?');
  expect(skillQuestion.conversation.id).toBe(id);
  expect(skillQuestion.response).toContain('No external catalog or document was read');
  await drawer.screenshot({ path: join(evidence, 'workspace-discussion-fixture.png') });

  const review = await send('Create a workspace called Community', false);
  expect(review.requires_confirmation).toBe(true);
  expect(['create_workspace', 'build_workspace']).toContain(review.confirmation.action_type);
  await expect(drawer.getByRole('button', { name: 'Confirm', exact: true })).toBeVisible();
  await expect(
    drawer
      .locator('[data-message-role="assistant"]')
      .filter({ hasText: review.confirmation.summary })
  ).toHaveCount(1);
  await drawer.getByRole('button', { name: 'Confirm', exact: true }).scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'unconfirmed-workspace-review.png') });
  await drawer.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect((await workspaces()).length).toBe(before);
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    id
  );
  const history = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  expect(history.messages.filter((message: any) => message.role === 'user').length).toBe(4);
  expect(JSON.stringify(history.messages)).not.toContain('Create a workspace called Community');
  expect(
    routes.slice(0, -1).every(route => !route.requires_creation && !route.workspace_recommended)
  ).toBe(true);
});
