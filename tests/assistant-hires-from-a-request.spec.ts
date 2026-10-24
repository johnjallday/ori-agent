import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { chooseAttach } from './helpers/assistant-menus';

// The assistant hires from a plain request (assistant-hires-from-a-request):
// the real built drawer, Route -> Ask -> the loopback Ollama stand-in and
// canonical Sessions, against a fresh isolated sandbox:
//
//   python3 scripts/assistant-workspace-demo.py --hire-request
//
// Only the stand-in model is deterministic: it proposes a hire for a request
// that opens with "help me compare" / "help me plan", answers in prose once
// Ori lists that work as put aside, and offers nothing where Ori withheld the
// tool. This proves reachability, the card, the hire, the decline memory, the
// folder precedence and the reload, not conversational reasoning.
test.describe.configure({ mode: 'serial' });

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function conversation(request: APIRequestContext, id: string) {
  return json(await request.get(`/api/home-assistant/conversations/${encodeURIComponent(id)}`));
}

async function workspaces(request: APIRequestContext) {
  const body = await json(await request.get('/api/workspaces'));
  return (body.folders || body.workspaces || []) as any[];
}

async function seedAssistant(request: APIRequestContext) {
  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hire-request-spec-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Keep my projects moving.',
      focus_areas: ['plan_my_day', 'keep_projects_moving']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'hire-request-spec-hq',
      if_version: assistant.state_version,
      name: 'Fictional HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

// A code folder Ori recognizes, inside the sandbox's Documents.
async function stageCodeFolder(sandbox: string) {
  const folder = join(sandbox, 'Documents', 'widget');
  await mkdir(join(folder, 'src'), { recursive: true, mode: 0o750 });
  await writeFile(
    join(folder, 'package.json'),
    JSON.stringify({ name: 'widget', version: '0.1.0', main: 'src/index.js' }),
    { mode: 0o600 }
  );
  await writeFile(join(folder, 'src', 'index.js'), 'export function render() {}\n', {
    mode: 0o600
  });
}

test('wt demo: a request becomes the hire card, Hire and start hires, Not now is remembered, a recognized folder keeps its card, a reload redraws it', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_HIREREQUEST_SANDBOX;
  test.skip(!sandbox, 'Use scripts/assistant-workspace-demo.py --hire-request');
  test.setTimeout(240_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence-assistant-hires-from-a-request');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await seedAssistant(request);
  await stageCodeFolder(sandbox!);
  const before = (await workspaces(request)).length;

  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  const cards = drawer.locator('.personal-assistant-hire');
  const open = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
    if (!(await drawer.isVisible())) await page.locator('#personalAssistantLauncher').click();
    await expect(input).toBeEnabled();
  };
  const send = async (prompt: string) => {
    await input.fill(prompt);
    const askWait = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const ask = await askWait;
    expect(ask.status(), await ask.text()).toBe(200);
    const reply = await ask.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    return reply;
  };
  const shot = (name: string) => drawer.screenshot({ path: join(evidence, name) });

  await page.goto('/');
  await open();

  // 1. The request becomes the hire card: one line of prose, then the card
  // with the role, the brief in the user's words, the workspace and the plan.
  const first = await send('help me compare the three CRMs we are evaluating and recommend one');
  expect(first.confirmation?.action_type).toBe('hire_agent');
  expect(first.confirmation.arguments.role).toBe('researcher');
  expect(first.confirmation.arguments.brief).toContain('Compare the three CRMs');
  const id = first.conversation.id as string;
  const card = cards.last();
  await expect(card).toHaveAttribute('data-state', 'offered');
  await expect(card.locator('.personal-assistant-hire__headline')).toContainText(
    'Hire Ruth for Compare the three CRMs'
  );
  await expect(card).toContainText('A Research Project workspace named Compare the three CRMs');
  await expect(card).toContainText('Other roles stay open to fill later');
  await expect(card).toContainText('First look: read the brief and suggest the first task');
  // The reply shows its lead line, not a second description of the card.
  await expect(drawer).not.toContainText('Offer: hire');
  const brief = card.locator('textarea');
  await expect(brief).toHaveValue(first.confirmation.arguments.brief);
  await expect(card.getByRole('button', { name: 'Hire and start', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Not now', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Adjust', exact: true })).toBeVisible();
  expect((await conversation(request, id)).hire_proposals[0].state).toBe('offered');
  await card.scrollIntoViewIfNeeded();
  await shot('spec-01-hire-card.png');

  // 2. A role change updates the blueprint line and the name; the brief stays.
  await card.getByRole('radio', { name: /Writer/ }).click();
  await expect(card).toContainText('A Writing Project workspace named Compare the three CRMs');
  await expect(card.locator('.personal-assistant-hire__headline')).toContainText('Hire Writer for');
  await expect(brief).toHaveValue(first.confirmation.arguments.brief);
  await card.getByRole('radio', { name: /Researcher/ }).click();
  await expect(card).toContainText('A Research Project workspace named Compare the three CRMs');
  await expect(card.locator('.personal-assistant-hire__headline')).toContainText('Hire Ruth for');

  // 3. Hire and start: one POST /api/agents/hire with the offer's id; the
  // workspace exists with the brief as its goal and Ruth as its entry agent.
  const hireWait = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/agents/hire'
  );
  await card.getByRole('button', { name: 'Hire and start', exact: true }).click();
  const hire = await hireWait;
  expect(hire.status(), await hire.text()).toBe(201);
  const hired = await hire.json();
  expect(hired.agent_name).toBe('Ruth');
  await expect(card).toHaveAttribute('data-state', 'hired');
  await expect(card).toContainText('Hired. Ruth is taking a first look');
  await expect(card.getByRole('link', { name: /Open Compare the three CRMs/ })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Hire and start', exact: true })).toHaveCount(0);
  const after = await workspaces(request);
  expect(after.length).toBe(before + 1);
  const created = await json(await request.get(`/api/workspaces/${hired.workspace_id}`));
  const workspace = created.workspace || created;
  expect(workspace.name).toBe('Compare the three CRMs');
  // The brief is the workspace's goal (its mission, read back under mission_state).
  expect(workspace.mission_state?.mission ?? workspace.mission).toBe(
    first.confirmation.arguments.brief
  );
  expect(workspace.agents || workspace.agent_names || []).toContain('Ruth');
  expect(workspace.entry_agent_name ?? 'Ruth').toBe('Ruth');
  const saved = await conversation(request, id);
  expect(saved.hire_proposals[0]).toMatchObject({
    state: 'hired',
    hire_id: hired.hire_id,
    agent_name: 'Ruth',
    workspace_id: hired.workspace_id
  });
  await shot('spec-02-hired.png');

  // 4. A second offer put aside with Not now: the card keeps its headline and
  // loses its buttons; the next request about that work gets prose, no card.
  const second = await send('help me plan the launch party for the new release');
  expect(second.confirmation?.action_type).toBe('hire_agent');
  await expect(cards).toHaveCount(2);
  const party = cards.last();
  await party.getByRole('button', { name: 'Not now', exact: true }).click();
  await expect(party).toHaveAttribute('data-state', 'declined');
  await expect(party).toContainText('Put aside.');
  await expect(party.getByRole('button')).toHaveCount(0);
  expect((await conversation(request, id)).hire_proposals[1].state).toBe('declined');
  const third = await send('help me plan the invitations for that party');
  expect(third.confirmation).toBeUndefined();
  await expect(cards).toHaveCount(2);
  expect((await conversation(request, id)).hire_proposals).toHaveLength(2);
  expect((await workspaces(request)).length).toBe(before + 1);
  await shot('spec-03-put-aside-then-prose.png');

  // 5. A recognized folder attached to the turn keeps the folder's own card:
  // the request's hire tool is withheld and no hire card is drawn.
  await chooseAttach(page, '#personalAssistantFolderChip');
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
  // Attaching a recognized project opens the folder's own card at once.
  await expect(drawer).toContainText('widget looks like a Node.js package');
  await expect(drawer.getByRole('button', { name: 'Review setup', exact: true })).toBeVisible();
  const folderTurn = await send('help me plan the next release of this project');
  expect(folderTurn.confirmation?.action_type).not.toBe('hire_agent');
  expect(folderTurn.folder_context).toBeTruthy();
  await expect(cards).toHaveCount(2);
  await expect(drawer.getByRole('button', { name: 'Review setup', exact: true })).toBeVisible();
  await shot('spec-04-folder-keeps-its-card.png');

  // 6. A reload draws the saved cards again, in their states.
  await page.reload();
  await open();
  await expect
    .poll(async () =>
      page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())
    )
    .toBe(id);
  await expect(cards).toHaveCount(2);
  const restored = cards.first();
  await expect(restored).toHaveAttribute('data-state', 'hired');
  await expect(restored).toContainText('Hired. Ruth is taking a first look');
  await expect(restored.getByRole('link', { name: /Open Compare the three CRMs/ })).toBeVisible();
  await expect(cards.last()).toHaveAttribute('data-state', 'declined');
  await expect(drawer).not.toContainText('Offer: hire');
  await restored.scrollIntoViewIfNeeded();
  await shot('spec-05-reload-hired.png');
});
