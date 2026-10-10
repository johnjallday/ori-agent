import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// A reply can take several model calls, and a CLI provider takes ten seconds or
// more for each, so a real answer often needs longer than 30 seconds. The
// drawer used to give up at 30 seconds and show a failure named after the
// page's own agent. Here the provider holds a reply for longer than that on a
// group's page, the reply still arrives, and a failed request is named after
// the hired assistant. Production browser and host under `wt demo`; the
// loopback provider stands in for the model.
test('wt demo: a reply slower than 30 seconds still arrives, and a failure names the hired assistant', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_SLOW_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --slow-reply');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
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
      request_id: 'workspace-slow-hire',
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
      request_id: 'workspace-slow-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  // A group with its own coordinator agent: the page has an agent of its own
  // whose name must not be put on the hired assistant's reply.
  const reviewed = await request.post('/api/workspaces/template-agent-plan', {
    data: { group_roster: true, group_name: 'Music portfolio slow fixture' }
  });
  expect(reviewed.ok(), await reviewed.text()).toBeTruthy();
  const plan = await reviewed.json();
  const created = await request.post('/api/workspaces', {
    data: {
      name: 'Music portfolio slow fixture',
      kind: 'group',
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
    }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const group = (await (await request.get('/api/workspaces')).json()).folders.find(
    (row: any) => row.name === 'Music portfolio slow fixture'
  );
  const pageAgents: string[] = plan.agents.map((agent: any) => String(agent.name));
  expect(pageAgents.length).toBeGreaterThan(0);

  const panel = page.locator('#personalAssistantPanel');
  const label = page.locator('#personalAssistantWorkspaceContext');
  const input = page.locator('#personalAssistantInput');
  const answers = page.locator('#homeAssistantConversation [data-message-role="assistant"]');
  const heading = page.locator('#homeAssistantThinkingModalLabel');
  const busy = () => page.evaluate(() => Boolean((window as any).OriAskRouting.getState().busy));

  await page.goto(`/workspaces/${group.folder_slug}/canvas`);
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  if (!(await panel.isVisible())) await page.locator('#personalAssistantLauncher').click();
  await expect(label).toHaveText('Context · Group: Music portfolio slow fixture');

  // 1. The provider holds the reply to an ordinary question for longer than the
  // old 30-second limit. While it waits, the drawer names the hired assistant.
  await writeFile(join(provider!, 'hold-next'), '', { mode: 0o600 });
  await input.fill('what kind of projects do i have?');
  const delayed = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask',
    { timeout: 90_000 }
  );
  const sentAt = Date.now();
  await page.locator('#personalAssistantSend').click();
  await expect
    .poll(async () => {
      try {
        return JSON.parse(await readFile(join(provider!, 'accepted.json'), 'utf8')).subject_id;
      } catch {
        return '';
      }
    })
    .toBe(group.id);
  await page.waitForTimeout(33_000);
  // Still waiting, with no failure shown and nothing claimed.
  expect(await busy(), 'the drawer gave up before the reply arrived').toBe(true);
  await expect(panel).not.toContainText('I could not answer that right now');
  await expect(panel).not.toContainText('longer than I wait for a reply');
  for (const name of pageAgents) await expect(heading).not.toContainText(name);
  await writeFile(join(provider!, 'release'), '', { mode: 0o600 });
  const reply = await (await delayed).json();
  const waited = Math.round((Date.now() - sentAt) / 1000);
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(waited).toBeGreaterThanOrEqual(33);
  expect(reply.conversation.stored).toBe(true);
  expect(reply.workspace_context.subject.id).toBe(group.id);
  await expect(answers.last()).toContainText('Fixture reply for Music portfolio slow fixture');
  await expect(panel).not.toContainText('I could not answer that right now');
  await page.screenshot({ path: join(evidence, 'slow-reply-arrived.png') });

  // 2. A request that never reaches Ori is reported as that, under the hired
  // assistant's name, not the name of an agent that belongs to this page. The
  // unsent message is put back in the box.
  await page.route('**/api/home-assistant/ask', route => route.abort('connectionrefused'), {
    times: 1
  });
  await input.fill('what is planned here?');
  await page.locator('#personalAssistantSend').click();
  await expect(answers.last()).toContainText('I could not reach Ori to answer that');
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  await expect(panel).toContainText('Atlas is unavailable');
  await expect(panel).toContainText('Ori could not be reached.');
  for (const name of pageAgents) {
    await expect(panel).not.toContainText(`${name} is unavailable`);
    await expect(panel).not.toContainText(`${name} Failed`);
  }
  await expect(heading.first()).toContainText('Atlas');
  await expect(input).toHaveValue('what is planned here?');
  await page.screenshot({ path: join(evidence, 'slow-reply-failure-label.png') });

  // 3. The next message goes through: one failure does not wedge the drawer.
  const next = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask'
  );
  await page.locator('#personalAssistantSend').click();
  expect((await (await next).json()).conversation.stored).toBe(true);
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);

  await writeFile(
    join(evidence, 'slow-reply-demo.json'),
    JSON.stringify(
      {
        boundary:
          'wt demo + production Ollama adapter + loopback provider that held one reply. The 30-second cutoff this guards against was found with a real Codex model; this run uses no vendor model',
        heldSeconds: waited,
        replyArrivedAndWasStored: true,
        failureNamedAfter: 'Atlas (the hired assistant)',
        pageAgentsNotNamed: pageAgents,
        draftRestoredAfterFailure: true,
        nextMessageWentThrough: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
