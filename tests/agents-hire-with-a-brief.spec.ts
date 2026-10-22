import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// Hire an agent with a brief from the Agents page (agents-hire-with-a-brief).
//
// Run it against a FRESH sandboxed demo server, serially:
//
//   ./scripts/demo-server.sh 8931            # a new sandbox
//   ./scripts/e2e.sh --port 8931 tests/agents-hire-with-a-brief.spec.ts
//
// It never runs a model. On a sandbox without one, a hire's first look cannot
// start, and the hire says so with Retry; that is what the always-run tests
// see. The report's Start this needs a look that really finished, so like the
// folder hire's spec it runs only against a sandbox where the live acceptance
// already hired an agent whose look finished:
//
//   ./scripts/e2e.sh --port <port> --env ORI_AGENT_HIRE_REPORTS=1 tests/agents-hire-with-a-brief.spec.ts
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

type Item = {
  kind: string;
  state?: string;
  title: string;
  detail?: string;
  attribution?: string;
  suggestion?: { text: string };
  actions?: string[];
  ref: { entity_id: string; workspace_id: string };
};

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function assistant(request: APIRequestContext) {
  return (await json(await request.get('/api/personal-assistant'))).personal_assistant;
}

async function today(request: APIRequestContext) {
  return (await json(await request.get('/api/personal-assistant/today'))).today;
}

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function openDrawer(page: Page) {
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher')
    )
  );
}

async function workspaceCount(request: APIRequestContext) {
  const body = await json(await request.get('/api/workspaces'));
  return (body.workspaces || body).length;
}

test('before the assistant is hired, Hire opens Mission 01 and offers no Hire tab', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const state = (await assistant(request)).state;
  test.skip(state !== 'needs_hire', `the sandbox's assistant is already ${state}`);
  await skipOnboarding(page);
  await page.goto('/agents', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#newAgentBtn')).toContainText('Hire');
  await page.locator('#newAgentBtn').click();
  await expect(page.locator('#addAgentModalTitleText')).toHaveText('Hire your personal assistant');
  await expect(page.locator('#agentCreateTabs')).toBeHidden();
  await expect(page.locator('#hireAgentBtn')).toBeHidden();
  // Nothing was hired: the preset only opened.
  expect((await assistant(request)).state).toBe('needs_hire');
});

let hired: { name: string; hireId: string; workspaceId: string; brief: string } | undefined;

test('Hire and start creates a Researcher with a Research Project whose goal is the brief', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  let current = await assistant(request);
  if (current.state === 'needs_hire') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'spec-hire-assistant',
        if_version: current.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me with my work.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    current = (await hire.json()).personal_assistant;
  }
  if (current.state === 'needs_hq') {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'spec-hire-hq',
        if_version: current.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }

  const name = `Rosa${Date.now()}`;
  const brief = "Compare the three note-taking apps I'm considering and recommend one";
  const before = await workspaceCount(request);
  await skipOnboarding(page);
  await page.goto('/agents', { waitUntil: 'domcontentloaded' });
  await page.locator('#newAgentBtn').click();

  // The card: Hire leads, Advanced is one tab away.
  await expect(page.locator('#agentCreateHireTab')).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#addAgentModalTitleText')).toHaveText('Hire an agent');
  const card = page.locator('#agentHirePanel [data-hire-card]');
  await card.locator('.catalog-card[data-slug="researcher"]').click();
  await expect(card.locator('#agentHireBrief')).toHaveValue(
    "Find and summarize what's known about this topic, with sources."
  );
  await card.locator('#agentHireName').fill(name);
  await card.locator('#agentHireBrief').fill(brief);
  // The workspace name follows the brief, as the server derives it.
  await expect(card.locator('#agentHireWorkspaceName')).toHaveValue(
    'Compare the Three Note-taking Apps'
  );
  const lines = card.locator('[data-hire-lines] li');
  await expect(lines).toHaveCount(4);
  await expect(lines.nth(0)).toContainText(`Hire ${name} (Researcher)`);
  await expect(lines.nth(1)).toContainText('A Research Project workspace named');
  await expect(lines.nth(1)).toContainText(`Goal: ${brief}`);
  await expect(lines.nth(2)).toContainText('Other roles stay open to fill later');
  await expect(lines.nth(3)).toContainText('First look: read the brief and suggest the first task');

  const response = page.waitForResponse(
    r => r.url().endsWith('/api/agents/hire') && r.request().method() === 'POST'
  );
  await page.locator('#hireAgentBtn').click();
  const result = await (await response).json();
  expect(result.agent_name).toBe(name);
  expect(result.blueprint).toBe('research-project');

  // The modal closes and the roster selects the hired agent.
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#stageName')).toHaveText(name);
  expect(await workspaceCount(request)).toBe(before + 1);

  // The workspace: the brief is its goal and the hired agent leads it, with
  // the blueprint's other two roles open.
  const ws = await json(await request.get(`/api/workspaces/${result.workspace_id}`));
  const workspace = ws.workspace || ws;
  expect(workspace.entry_agent_name).toBe(name);
  expect(workspace.mission_state?.mission).toBe(brief);
  const roles = (await json(await request.get(`/api/workspaces/${result.workspace_id}/roles`)))
    .roles;
  const byID = Object.fromEntries(roles.roles.map((role: any) => [role.role_id, role]));
  expect(byID['research-lead'].state).toBe('filled');
  expect(byID['research-lead'].agent?.name).toBe(name);
  expect(byID['source-scout'].state).toBe('empty');
  expect(byID['synthesis-writer'].state).toBe('empty');

  hired = { name, hireId: result.hire_id, workspaceId: result.workspace_id, brief };
  // Without a model the look cannot start; with one it is looking.
  expect(['looking', 'failed']).toContain(result.look_state);
});

test('a first look that could not start is a Needs you card with Retry', async ({
  page,
  request
}) => {
  test.skip(!hired, 'no hire from the previous test');
  const item = ((await today(request)).needs_you?.items || []).find(
    (candidate: Item) => candidate.ref?.entity_id === hired!.hireId
  );
  test.skip(!item, 'the look is still running here (a model is configured)');
  expect(item.kind).toBe('hire_first_look_report');
  expect(item.state).toBe('failed');
  expect(item.actions).toEqual(['retry']);

  await skipOnboarding(page);
  await page.goto('/agents', { waitUntil: 'domcontentloaded' });
  await openDrawer(page);
  const report = page.locator(
    `#personalAssistantNeedsReports article[data-look-id="${hired!.hireId}"]`
  );
  await expect(report).toBeVisible({ timeout: 30_000 });
  await expect(report).toHaveAttribute('data-report-kind', 'hire');
  await expect(report).toContainText('First look did not finish');
  await expect(report.getByRole('button', { name: 'Retry' })).toBeVisible();
  await expect(report.getByRole('link', { name: 'Open workspace' })).toBeVisible();
  await expect(report.getByRole('button', { name: 'Start this' })).toHaveCount(0);
});

test('Advanced still creates a bare agent and nothing else', async ({ page, request }) => {
  const state = (await assistant(request)).state;
  test.skip(state === 'needs_hire', 'the Hire tab needs a hired assistant');
  const name = `Bare${Date.now()}`;
  const before = await workspaceCount(request);
  await skipOnboarding(page);
  await page.goto('/agents', { waitUntil: 'domcontentloaded' });
  await page.locator('#newAgentBtn').click();
  await page.locator('#agentCreateAdvancedTab').click();
  await expect(page.locator('#addAgentModalTitleText')).toHaveText('Create New Agent');
  await expect(page.locator('#hireAgentBtn')).toBeHidden();
  await page.locator('#agentName').fill(name);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#stageName')).toHaveText(name);
  expect(await workspaceCount(request)).toBe(before);
  await request.delete(`/api/agents?name=${encodeURIComponent(name)}`).catch(() => undefined);
});

test('Start this on a finished look starts a Ready task assigned to the hired agent', async ({
  page,
  request
}) => {
  test.skip(
    !process.env.ORI_AGENT_HIRE_REPORTS,
    'requires a sandbox with a finished hire look (ORI_AGENT_HIRE_REPORTS)'
  );
  const report: Item | undefined = ((await today(request)).needs_you?.items || []).find(
    (item: Item) =>
      item.kind === 'hire_first_look_report' && item.state === 'finished' && item.suggestion?.text
  );
  test.skip(!report, 'no finished hire look with a suggestion in this sandbox');

  await skipOnboarding(page);
  await page.goto('/agents', { waitUntil: 'domcontentloaded' });
  await openDrawer(page);
  const card = page.locator(
    `#personalAssistantNeedsReports article[data-look-id="${report!.ref.entity_id}"]`
  );
  await expect(card).toBeVisible({ timeout: 30_000 });
  await expect(card).toContainText('Suggests first');
  await expect(card).toContainText(report!.suggestion!.text);
  const started = page.waitForResponse(
    r =>
      r.url().endsWith(`/hires/${report!.ref.entity_id}/start-suggestion`) &&
      r.request().method() === 'POST'
  );
  await card.getByRole('button', { name: 'Start this' }).click();
  const body = await (await started).json();
  const taskID = body.task?.task_id;
  expect(taskID).toBeTruthy();
  await expect(card).toHaveCount(0, { timeout: 30_000 });

  const ws = await json(await request.get(`/api/workspaces/${report!.ref.workspace_id}`));
  const task = ((ws.workspace || ws).tasks || []).find((t: any) => t.id === taskID);
  expect(task, 'the started task is in the hire workspace').toBeTruthy();
  expect(task.to).toBe(report!.attribution);
  expect(['ready', 'in_progress', 'review', 'done']).toContain(task.ticket_state);
});
