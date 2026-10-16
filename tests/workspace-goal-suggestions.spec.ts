import {
  expect,
  test,
  type APIRequestContext,
  type APIResponse,
  type Page
} from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { isAbsolute, join, relative } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// A workspace goal, from the moment it is written to the work its agents
// suggest for it, in a real browser against the real server and store.
//
// Run it with its own wrapper, which starts an isolated demo server backed by
// the fixture model (scripts/fixture-ollama.py) on a sandbox of its own:
//
//   ./scripts/e2e-goal-suggestions.sh
//
// The fixture answers every goal run with the sandbox file
// mission-response.txt, which these tests rewrite between runs, and holds the
// answer for a few seconds so a run is visibly in flight. No real model is
// called. Each test builds on the one before, so they run in order.
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the sandbox that scripts/e2e-goal-suggestions.sh provides');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

const GOAL = 'Keep the brand voice consistent';
const IN_FLIGHT = 'Looking for things to work on…';

const THREE_TASKS_TWO_FINDINGS = JSON.stringify({
  items: [
    {
      kind: 'task',
      title: 'Draft a brand voice guide',
      why: 'Recent posts drift between a formal and a playful tone.',
      evidence: 'No style or voice note exists in this workspace yet.',
      priority: 'high',
      confidence: 'medium',
      effort: 'small',
      suggested_agent: 'Ask Ori',
      first_step: 'Collect five posts that sound right.'
    },
    {
      kind: 'task',
      title: "Plan next week's posts",
      why: 'The goal needs a steady publishing rhythm.',
      evidence: 'The workspace has no open tasks or schedules.',
      priority: 'medium',
      confidence: 'high',
      effort: 'medium',
      first_step: 'List three topics.'
    },
    {
      kind: 'task',
      title: 'Write alt text for the hero images',
      why: 'Accessible images are part of a consistent brand.',
      evidence: 'Hero images are referenced without descriptions.',
      priority: 'low',
      confidence: 'medium',
      effort: 'small',
      first_step: 'List every hero image in use.'
    },
    {
      kind: 'finding',
      title: 'Brand voice drift across channels',
      why: 'Three recent posts read as if written by different brands.',
      evidence: 'Newsletter is formal, social posts are casual.',
      priority: 'high',
      confidence: 'medium',
      recommended_action: 'Review the last three posts against one agreed tone.'
    },
    {
      kind: 'finding',
      title: 'Logo files use the old palette',
      why: 'Mixed palettes undercut a consistent brand.',
      evidence: 'assets/logo-2023.svg is still the newest logo file.',
      priority: 'medium',
      confidence: 'high',
      recommended_action: 'Replace the logo files with the current palette.'
    }
  ]
});
const NOT_JSON = 'I looked around and <everything> seems fine to me.';

function answerGoalRunsWith(text: string) {
  writeFileSync(join(sandbox, 'mission-response.txt'), text);
}

async function ok(response: APIResponse) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

type MissionRun = {
  origin?: string;
  succeeded: boolean;
  parse_status?: string;
  tasks_created: number;
  findings_created: number;
  duplicates_skipped: number;
};
type MissionState = {
  mission: string;
  run_in_flight: boolean;
  needs_attention?: string;
  first_run_pending: boolean;
  mission_runs: MissionRun[];
};

async function missionState(request: APIRequestContext, id: string): Promise<MissionState> {
  return ok(await request.get(`/api/workspaces/${id}/mission`));
}

// waitForRuns waits until the workspace has `count` finished goal runs and
// none in flight, and returns the newest.
async function waitForRuns(request: APIRequestContext, id: string, count: number) {
  await expect
    .poll(
      async () => {
        const state = await missionState(request, id);
        return !state.run_in_flight && state.mission_runs.length >= count;
      },
      { timeout: 60_000 }
    )
    .toBe(true);
  return (await missionState(request, id)).mission_runs[0];
}

async function suggestedTitles(request: APIRequestContext, id: string) {
  const body = await ok(await request.get(`/api/orchestration/backlog?workspace_id=${id}`));
  return (body.items as { task: { description: string; source_type?: string } }[])
    .filter(item => item.task.source_type === 'goal_suggestion')
    .map(item => item.task.description)
    .sort();
}

function goalRunPrompts() {
  const log = join(sandbox, 'fixture-requests.jsonl');
  if (!existsSync(log)) return [];
  return readFileSync(log, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map(line => JSON.parse(line) as { kind: string; messages: { content?: string }[] })
    .filter(entry => entry.kind === 'mission')
    .map(entry => entry.messages.map(message => message.content || '').join('\n'));
}

function cardByLabel(page: Page, label: string) {
  return page.locator('#templatePicker .workspace-template-card').filter({
    has: page.locator('.workspace-template-card-label', { hasText: new RegExp(`^${label}$`) })
  });
}

let workspaceId = '';
let workspaceSlug = '';

test.beforeAll(async ({ request }) => {
  await request.post('/api/onboarding/skip');
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
});

test('a workspace created from the modal takes its description as its goal and runs only when first opened (SM2, FR25)', async ({
  page,
  request
}) => {
  answerGoalRunsWith('{"items": []}');
  await page.goto('/workspaces');
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(cardByLabel(page, 'Blank')).toBeVisible();
  await cardByLabel(page, 'Blank').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.fill('#folderNameInput', 'Modal Goal Desk');
  await page.fill('#folderDescriptionInput', GOAL);
  // The untouched Goal field mirrors the description.
  await expect(page.locator('#folderGoalInput')).toHaveValue(GOAL);

  // Team: the blank blueprint's Ask Ori role is filled with the saved Ask Ori.
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await page.getByRole('button', { name: 'Use Ask Ori for Ask Ori' }).click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();

  // The page moves into the new workspace as soon as it exists, so the create
  // answer is read on its way through rather than from the page afterwards,
  // and the first open's request is held until the state at create is read.
  let resolveCreated: (body: { folder: { id: string; folder_slug: string } }) => void = () => {};
  const created = new Promise<{ folder: { id: string; folder_slug: string } }>(resolve => {
    resolveCreated = resolve;
  });
  await page.route('**/api/workspaces', async route => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    resolveCreated(await ok(response));
    await route.fulfill({ response });
  });
  let releaseFirstOpen: () => void = () => {};
  const firstOpenHeld = new Promise<void>(resolve => {
    releaseFirstOpen = resolve;
  });
  await page.route('**/mission/first-open', async route => {
    await firstOpenHeld;
    await route.continue().catch(() => {});
  });
  await page.locator('#createFolderBtn').click();
  const placeHere = page.getByRole('button', { name: 'Create workspace here' });
  await placeHere.click({ timeout: 5_000 }).catch(() => {});
  const { folder } = await created;

  // Nothing runs at create: the workspace has its goal and its first run
  // waits for the first open.
  const atCreate = await missionState(request, folder.id);
  expect(atCreate.mission).toBe(GOAL);
  expect(atCreate.first_run_pending).toBe(true);
  expect(atCreate.run_in_flight).toBe(false);
  expect(atCreate.mission_runs).toHaveLength(0);
  expect(goalRunPrompts()).toHaveLength(0);

  releaseFirstOpen();
  await page.unroute('**/mission/first-open');
  const id = folder.id;
  await page.goto(`/workspaces/${folder.folder_slug}`);
  await expect
    .poll(async () => (await missionState(request, id)).first_run_pending, { timeout: 15_000 })
    .toBe(false);
  const run = await waitForRuns(request, id, 1);
  expect(run.origin).toBe('first_goal');
  expect(run.succeeded).toBe(true);
});

test('saving the first goal starts a run whose suggestions reach the Backlog drawer (SM6, FR24, FR27)', async ({
  context,
  page,
  request
}) => {
  answerGoalRunsWith(THREE_TASKS_TWO_FINDINGS);
  const body = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Goal E2E Desk', entry_agent_name: 'Ask Ori' }
    })
  );
  workspaceId = body.folder.id;
  workspaceSlug = body.folder.folder_slug;

  await page.goto(`/workspaces/${workspaceSlug}`);
  await expect(page.locator('#workspace-command-mission-title')).toHaveText('No goal yet');
  await page.locator('#workspace-command-mission-edit').click();
  await expect(page.locator('#workspace-detail-goal-modal')).toBeVisible();
  await page.fill('#workspace-detail-goal-modal-text', GOAL);
  await page.locator('#workspace-detail-goal-modal-save').click();
  await expect(page.locator('#workspace-detail-goal-modal')).toBeHidden();

  const status = page.locator('#workspace-command-mission-action-status');
  await expect(status).toHaveText(IN_FLIGHT);
  await expect(page.locator('#workspace-command-mission-run')).toBeDisabled();

  // A second start while the first is running is refused, not queued.
  const second = await request.post(`/api/workspaces/${workspaceId}/mission/trigger`);
  expect(second.status()).toBe(409);
  expect((await second.json()).code).toBe('goal_run_in_progress');

  // A second tab opened on the Backlog drawer follows the run it did not
  // start: it says a run is looking, then lists the suggestions by itself.
  const drawerTab = await context.newPage();
  await installLocalCdn(drawerTab);
  await drawerTab.goto(`/workspaces/${workspaceSlug}?panel=backlog`);
  await expect(drawerTab.locator('.ws-cmd-drawer-goal-run')).toHaveText(IN_FLIGHT);
  const suggested = drawerTab.locator('.ws-cmd-drawer-row.is-suggested');
  await expect(suggested).toHaveCount(3, { timeout: 60_000 });
  await expect(drawerTab.locator('.ws-cmd-drawer-goal-run')).toHaveCount(0);
  await expect(suggested.first().locator('.ws-cmd-drawer-row-suggested')).toHaveText('Suggested');

  // The card that started it reports the run's counts, and they stay.
  await expect(status).toHaveText('Run complete — 3 tasks suggested · 2 findings.');
  await page.waitForTimeout(3_000);
  await expect(status).toHaveText('Run complete — 3 tasks suggested · 2 findings.');
  await expect(page.locator('#workspace-command-mission-run')).toBeEnabled();
  await drawerTab.close();

  const run = await waitForRuns(request, workspaceId, 1);
  expect(run).toMatchObject({ origin: 'first_goal', succeeded: true, tasks_created: 3 });
  expect(run.findings_created).toBe(2);
  expect(goalRunPrompts().at(-1)).toContain(GOAL);
});

test('Not useful removes a suggestion and the next run is told never to suggest it (SM8)', async ({
  page,
  request
}) => {
  await page.goto(`/workspaces/${workspaceSlug}?panel=backlog`);
  const row = page.locator('.ws-cmd-drawer-row', { hasText: 'Draft a brand voice guide' });
  await row.click();
  await expect(page.locator('.ws-cmd-suggestion-why')).toContainText('formal and a playful');
  await page.locator('[data-cmd-backlog-not-useful]').click();
  await page.fill('[data-cmd-backlog-not-useful-reason]', 'We already have one');
  await page.getByRole('button', { name: 'Remove suggestion' }).click();
  await expect(row).toHaveCount(0);
  await expect(page.locator('.ws-cmd-drawer-row.is-suggested')).toHaveCount(2);

  // The ledger travels with the workspace folder, in its workspace.json.
  const record = ['workspace-staging', 'Ori Workspaces']
    .map(root => join(sandbox, root, workspaceSlug, 'workspace.json'))
    .find(path => existsSync(path));
  expect(record, 'workspace.json in the sandbox').toBeTruthy();
  const ledger = JSON.parse(readFileSync(record as string, 'utf8')).dismissed_suggestions as {
    normalized_title: string;
    reason: string;
  }[];
  expect(ledger[0]).toMatchObject({
    normalized_title: 'draft a brand voice guide',
    reason: 'We already have one'
  });

  // The same answer again adds nothing: two are open, one was dismissed.
  const started = await request.post(`/api/workspaces/${workspaceId}/mission/trigger`);
  expect(started.status()).toBe(202);
  const run = await waitForRuns(request, workspaceId, 2);
  expect(run).toMatchObject({ origin: 'manual', tasks_created: 0, duplicates_skipped: 3 });
  expect(await suggestedTitles(request, workspaceId)).toEqual([
    "Plan next week's posts",
    'Write alt text for the hero images'
  ]);
  const prompt = goalRunPrompts().at(-1) || '';
  expect(prompt).toContain('never suggest them again');
  expect(prompt).toContain('Draft a brand voice guide');
});

test('a run whose answer is not JSON is recorded as failed with its raw output (SM7)', async ({
  page,
  request
}) => {
  answerGoalRunsWith(NOT_JSON);
  await page.goto(`/workspaces/${workspaceSlug}`);
  await page.locator('#workspace-command-mission-run').click();
  const status = page.locator('#workspace-command-mission-action-status');
  await expect(status).toHaveText(IN_FLIGHT);
  await expect(status).toHaveText(/^Run failed: .+\. See Goal Settings for details\.$/, {
    timeout: 60_000
  });

  const run = await waitForRuns(request, workspaceId, 3);
  expect(run).toMatchObject({ succeeded: false, parse_status: 'failed', tasks_created: 0 });

  // Goal Settings (quick goal modal → Advanced settings) keeps the raw answer.
  await page.locator('#workspace-command-mission-edit').click();
  await page.locator('#workspace-detail-goal-modal-advanced').click();
  const history = page.locator('.workspace-mission-history:visible');
  await expect(history.locator('.workspace-mission-runs > li').first()).toContainText('Failed');
  await history.locator('summary', { hasText: 'Show output' }).click();
  await expect(history.locator('pre')).toHaveText(NOT_JSON);
});

test('Action Center shows the suggestion summary and each finding with its evidence (FR16, FR22, FR29)', async ({
  page
}) => {
  await page.goto(`/action-center?workspace=${workspaceId}`);
  await expect(page.getByText('3 suggested tasks in Goal E2E Desk')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Backlog' }).first()).toHaveAttribute(
    'href',
    new RegExp(`/workspaces/${workspaceSlug}\\?panel=backlog`)
  );
  await expect(page.getByText('Goal run answer could not be read')).toBeVisible();

  const finding = page.locator('details', {
    has: page.locator('summary', { hasText: 'Evidence and recommended action' })
  });
  const drift = finding.filter({ hasText: 'Newsletter is formal' });
  await drift.locator('summary').click();
  await expect(drift).toContainText('Review the last three posts against one agreed tone.');
});

test('stock defaults: Writing and Code prefill from the real library and retain distinct first-open behavior', async ({
  page,
  request
}, testInfo) => {
  answerGoalRunsWith('{"items": []}');
  const before = goalRunPrompts().length;
  const cases = [
    {
      id: 'writing-project',
      name: 'Writing Project',
      goal: 'Develop this manuscript from outline into reviewed chapters with coherent structure and consistent voice.'
    },
    {
      id: 'code-project',
      name: 'Code Project',
      goal: 'Understand this project, identify evidence-backed risks, and propose prioritized improvements before changing code.'
    }
  ];
  for (const item of cases) {
    await page.goto('/workspaces');
    await page.evaluate(() => {
      // @ts-expect-error bootstrap is a page global
      window.bootstrap.Modal.getOrCreateInstance(document.getElementById('addFolderModal')).show();
    });
    await cardByLabel(page, item.name).click();
    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#folderGoalInput')).toHaveValue(item.goal);
    await page.fill('#folderNameInput', `Stock ${item.name}`);
    await page.locator('#folderGoalInput').focus();
    await page.locator('#folderGoalInput').scrollIntoViewIfNeeded();
    await page.screenshot({
      path: testInfo.outputPath(`${item.id}-prefilled.png`),
      fullPage: true
    });
    // The API omits mission altogether: this proves the shipped default also
    // reaches callers that do not send back the modal's prefilled value.
    const created = await ok(
      await request.post('/api/workspaces', {
        data: { name: `Stock ${item.name}`, template_id: item.id }
      })
    );
    const { id, folder_slug: slug } = created.folder;
    const state = await missionState(request, id);
    expect(state.mission).toBe(item.goal);
    expect(state.mission_runs).toHaveLength(0);
    expect(state.run_in_flight).toBe(false);
    await page.goto(`/workspaces/${slug}`);
    await expect(page.locator('#workspace-command-mission-title')).toHaveText(item.goal);
    if (item.id === 'writing-project') {
      await expect(page.locator('#workspace-command-mission-action-status')).toHaveText(IN_FLIGHT);
      await captureGoalState(page, testInfo.outputPath('writing-first-open.png'));
      await waitForRuns(request, id, 1);
      expect(goalRunPrompts()).toHaveLength(before + 1);
      expect(goalRunPrompts().at(-1)).toContain(item.goal);
    } else {
      await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
        'Start first look'
      );
      await captureGoalState(page, testInfo.outputPath('code-explicit-action.png'));
      await page.reload();
      expect(goalRunPrompts()).toHaveLength(before + 1);
      await page.locator('#workspace-command-mission-run').click();
      await waitForRuns(request, id, 1);
      expect(goalRunPrompts()).toHaveLength(before + 2);
      expect(goalRunPrompts().at(-1)).toContain(item.goal);
    }
  }
});

async function captureGoalState(page: Page, path: string) {
  await expect(page.getByText('Loading workspace tasks…', { exact: true })).toHaveCount(0);
  await page.evaluate(async () => {
    await document.fonts.ready;
    window.scrollTo(0, 0);
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  await page.screenshot({ path, fullPage: true });
}

test('first-goal lifecycle: readiness blocks once, repair needs an explicit retry, and tabs do not replay', async ({
  page,
  request,
  context
}, testInfo) => {
  answerGoalRunsWith('{"items": []}');
  const catalog = await ok(await request.get('/api/project-templates'));
  const root = catalog.templates_root || catalog.data?.templates_root;
  expect(typeof root).toBe('string');
  const within = relative(realpathSync(sandbox), realpathSync(root));
  expect(
    within.startsWith('..') || isAbsolute(within),
    'fixture must stay in the isolated library'
  ).toBe(false);
  const templateDir = join(root, 'goal-readiness-fixture');
  mkdirSync(templateDir, { recursive: true, mode: 0o750 });
  writeFileSync(
    join(templateDir, 'template.json'),
    JSON.stringify({
      name: 'Goal Readiness Fixture',
      description: 'A deterministic reviewed setup decision.',
      goal: 'Summarize the approved project decisions.',
      setup_wizard: {
        version: 1,
        title: 'Review goal setup',
        steps: [
          { id: 'summary', kind: 'summary', required: true, title: 'Review the setup boundary' }
        ]
      }
    }),
    { mode: 0o600 }
  );
  const body = await ok(
    await request.post('/api/workspaces', {
      data: {
        name: 'Goal Readiness Desk',
        template_id: 'goal-readiness-fixture',
        entry_agent_name: 'Ask Ori',
        create_template_agents: false
      }
    })
  );
  const { id, folder_slug: slug } = body.folder;
  const before = goalRunPrompts().length;
  await page.goto(`/workspaces/${slug}`);
  await expect
    .poll(async () => (await missionState(request, id)).needs_attention || '')
    .toContain('workspace setup');
  await ok(await request.post(`/api/workspaces/${id}/setup-wizard/dismiss`));
  await page.reload();
  await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
    'workspace setup'
  );
  await captureGoalState(page, testInfo.outputPath('setup-blocked.png'));
  for (let i = 0; i < 3; i++)
    await ok(await request.post(`/api/workspaces/${id}/mission/first-open`));
  expect(goalRunPrompts()).toHaveLength(before);
  await ok(
    await request.post(`/api/workspaces/${id}/setup-wizard/steps/summary/confirm`, { data: {} })
  );
  await page.reload();
  expect(goalRunPrompts()).toHaveLength(before);
  await page.locator('#workspace-command-mission-run').click();
  await expect(page.locator('#workspace-command-mission-action-status')).toHaveText(IN_FLIGHT);
  await captureGoalState(page, testInfo.outputPath('explicit-retry-running.png'));
  const second = await context.newPage();
  await installLocalCdn(second);
  await second.goto(`/workspaces/${slug}`);
  await expect(second.locator('#workspace-command-mission-run')).toBeDisabled();
  await waitForRuns(request, id, 1);
  await second.reload();
  await page.reload();
  expect(goalRunPrompts()).toHaveLength(before + 1);
  await second.close();
});

test('first-goal lifecycle: eligible opening runs once but Code opening and saving require Run now', async ({
  page,
  request,
  context
}, testInfo) => {
  answerGoalRunsWith('{"items": []}');
  const before = goalRunPrompts().length;
  const ordinary = await ok(
    await request.post('/api/workspaces', {
      data: {
        name: 'Eligible Goal Desk',
        mission: 'Prepare reviewed chapter suggestions.',
        entry_agent_name: 'Ask Ori'
      }
    })
  );
  expect((await missionState(request, ordinary.folder.id)).mission_runs).toHaveLength(0);
  await page.goto(`/workspaces/${ordinary.folder.folder_slug}`);
  await expect(page.locator('#workspace-command-mission-action-status')).toHaveText(IN_FLIGHT);
  await captureGoalState(page, testInfo.outputPath('eligible-first-open.png'));
  await waitForRuns(request, ordinary.folder.id, 1);
  expect(goalRunPrompts()).toHaveLength(before + 1);

  const code = await ok(
    await request.post('/api/workspaces', {
      data: {
        name: 'Code Consent Desk',
        template_id: 'code-project',
        mission: 'Understand this project before proposing changes.',
        create_template_agents: true
      }
    })
  );
  const base = `/api/workspaces/${code.folder.id}/mission`;
  await page.goto(`/workspaces/${code.folder.folder_slug}`);
  await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
    'Start first look'
  );
  await captureGoalState(page, testInfo.outputPath('code-consent-required.png'));
  await ok(
    await request.put(base, {
      data: { mission: 'Identify evidence-backed project risks without changing code.' }
    })
  );
  const second = await context.newPage();
  await installLocalCdn(second);
  await second.goto(`/workspaces/${code.folder.folder_slug}`);
  await page.reload();
  expect(goalRunPrompts()).toHaveLength(before + 1);
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST') writes.push(req.url());
  });
  await page.locator('#workspace-command-mission-run').click();
  await waitForRuns(request, code.folder.id, 1);
  expect(goalRunPrompts()).toHaveLength(before + 2);
  expect(goalRunPrompts().at(-1)).toContain('Identify evidence-backed project risks');
  expect(writes.some(url => url.includes('/folder-first-task/start'))).toBe(false);
  await second.reload();
  expect(goalRunPrompts()).toHaveLength(before + 2);
  await second.close();
});
