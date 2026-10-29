import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Backup-first suggestions: after File Curator is hired for an inbox folder
// with no observed backup, a goal run in its workspace suggests the recovery
// copy first, and the Backlog ticket's Back up opens protection. In
// a real browser against the real server and store.
//
// Run it with its own wrapper, which serves the inbox-folder sandbox
// (scripts/demo-inbox-folder.sh) with goal runs answered by the fixture model:
//
//   ./scripts/e2e-backup-first.sh
//
// The fixture answers every goal run with the sandbox file
// mission-response.txt, which these tests rewrite between runs. No real model
// is called. Each test builds on the one before, so they run in order:
// Downloads (the model's own ticket, Not useful, and nothing after it on
// either path), then Desktop (the host's ticket when the answer leaves it
// out), then the covered path.
//
// The covered path needs stored backup evidence covering a File Janitor
// folder, which cannot be fabricated through the product API (by design; see
// tests/folder-protection-coverage.spec.ts). It runs only with an
// operator-seeded sandbox described by ORI_FOLDER_PROTECTION_COVERED_CONTROL,
// a JSON file:
//   { "kind": "invented stored metadata only", "file_janitor_workspace_id": "…" }
// naming a File Janitor workspace whose managed folder that evidence covers.
const sandbox = process.env.ORI_BACKUP_FIRST_SANDBOX || '';
test.skip(!sandbox, 'requires the sandbox that scripts/e2e-backup-first.sh provides');
test.describe.configure({ mode: 'serial' });
test.setTimeout(180_000);

const panel = (page: Page) => page.locator('.personal-assistant-panel');
const backupTitle = (folder: string) => `Back up ${folder} before the first filing batch`;

type GoalSuggestion = { precondition?: string; action?: string; source?: string; why?: string };
type Task = {
  id: string;
  description: string;
  priority?: number;
  source_type?: string;
  context?: { goal_suggestion?: GoalSuggestion };
};

function answerGoalRunsWith(items: object[]) {
  writeFileSync(join(sandbox, 'mission-response.txt'), JSON.stringify({ items }));
}

const task = (title: string, extra: object = {}) => ({
  kind: 'task',
  title,
  why: `Why ${title}`,
  evidence: 'From the first batch',
  priority: 'medium',
  confidence: 'medium',
  effort: 'small',
  first_step: 'Look at it.',
  ...extra
});

async function api<T = any>(request: APIRequestContext, path: string): Promise<T> {
  const response = await request.get(path);
  expect(response.ok(), path).toBeTruthy();
  return response.json();
}

async function workspaceId(request: APIRequestContext, name: string): Promise<string> {
  const body = await api(request, '/api/workspaces');
  const found = (body.folders || []).find((w: { name: string }) => w.name === name);
  expect(found, `the ${name} workspace`).toBeTruthy();
  return found.id;
}

// The workspace's own File Curator copy is staffed with the blueprint's
// OpenAI model, which this sandbox cannot run; goal runs use the fixture.
async function useFixtureModel(request: APIRequestContext, id: string) {
  const response = await request.patch(`/api/workspaces/${id}/agents/File%20Curator`, {
    data: { llm_provider: 'ollama', model: 'fixture-model:latest' }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function suggestedTasks(request: APIRequestContext, id: string): Promise<Task[]> {
  const body = await api(request, `/api/workspaces/${id}`);
  const ws = body.workspace || body;
  return (ws.tasks || []).filter((t: Task) => t.source_type === 'goal_suggestion');
}

// waitForRuns waits until the workspace has `count` finished goal runs and
// none in flight, and returns the newest.
async function waitForRuns(request: APIRequestContext, id: string, count: number) {
  await expect
    .poll(
      async () => {
        const state = await api(request, `/api/workspaces/${id}/mission`);
        return !state.run_in_flight && (state.mission_runs || []).length >= count;
      },
      { timeout: 60_000 }
    )
    .toBe(true);
  return (await api(request, `/api/workspaces/${id}/mission`)).mission_runs[0];
}

async function runNow(request: APIRequestContext, id: string, count: number) {
  const started = await request.post(`/api/workspaces/${id}/mission/trigger`);
  expect(started.ok(), await started.text()).toBeTruthy();
  return waitForRuns(request, id, count);
}

function backlog(page: Page) {
  return page.locator('.ws-cmd-drawer-backlog .ws-cmd-drawer-list .ws-cmd-drawer-row-title');
}

async function hire(page: Page, folder: string) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.close));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  await page.locator('#cockpitPrimaryActionBtn').click();
  await page.locator('.personal-assistant-panel__chip', { hasText: folder }).first().click();
  await expect(panel(page).locator('.folder-visual__agent strong')).toHaveText('File Curator');
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const review = page.locator('#assistedWorkspacePlacementPanel');
  await review.getByRole('button', { name: 'Create workspace & scan', exact: true }).click();
  await expect(review.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 60_000
  });
  await review.getByRole('button', { name: 'Close', exact: true }).click();
}

test.beforeAll(async ({ request }) => {
  await request.post('/api/onboarding/skip');
});

let downloads = '';

test("the model's backup task leads the Backlog and its Back up opens the chooser", async ({
  page,
  request
}) => {
  await hire(page, 'Downloads');
  downloads = await workspaceId(request, 'Downloads');
  await useFixtureModel(request, downloads);
  // The answer names the backup task, but not first.
  answerGoalRunsWith([
    task('Review the installer pile'),
    task(backupTitle('Downloads'), { why: 'The model says back up first' })
  ]);

  // Opening the workspace starts its first goal run (the existing contract).
  await page.goto('/workspaces/downloads?panel=backlog');
  const run = await waitForRuns(request, downloads, 1);
  expect(run.succeeded, JSON.stringify(run)).toBe(true);
  expect(run.tasks_created).toBe(2);

  const tickets = (await suggestedTasks(request, downloads)).filter(
    t => t.description === backupTitle('Downloads')
  );
  expect(tickets).toHaveLength(1);
  expect(tickets[0].context?.goal_suggestion).toMatchObject({
    precondition: 'backup_protection',
    action: 'check_protection',
    source: 'model',
    why: 'The model says back up first'
  });

  await page.goto('/workspaces/downloads?panel=backlog');
  await expect(backlog(page).first()).toHaveText(backupTitle('Downloads'), { timeout: 30_000 });
  await backlog(page).first().click();
  const actions = page.locator('.ws-cmd-drawer-preview-actions');
  await expect(actions.locator('button').first()).toHaveText('Back up');
  // An ordinary suggestion offers no Back up.
  await backlog(page).filter({ hasText: 'Review the installer pile' }).click();
  await expect(actions.getByRole('button', { name: 'Back up', exact: true })).toHaveCount(0);

  await backlog(page).first().click();
  const posts: string[] = [];
  page.on('request', r => {
    if (r.method() !== 'GET') posts.push(r.url());
  });
  await actions.getByRole('button', { name: 'Back up', exact: true }).click();
  await expect(page.locator('#protectFoldersChooser')).toBeVisible({ timeout: 10_000 });
  expect(posts.filter(url => /backlog|tickets|first-batch/.test(url))).toEqual([]);
});

test('Not useful on the backup task holds on the model and the host path', async ({
  page,
  request
}) => {
  await page.goto('/workspaces/downloads?panel=backlog');
  await backlog(page).first().click();
  const actions = page.locator('.ws-cmd-drawer-preview-actions');
  await actions.getByRole('button', { name: 'Not useful' }).click();
  await page.locator('[data-cmd-backlog-not-useful-reason]').fill('I file by hand first');
  await page.getByRole('button', { name: 'Remove suggestion' }).click();
  await expect(backlog(page).filter({ hasText: backupTitle('Downloads') })).toHaveCount(0, {
    timeout: 30_000
  });

  // The fact is still unmet; the answer names the task again, then leaves it out.
  answerGoalRunsWith([task(backupTitle('Downloads'))]);
  let run = await runNow(request, downloads, 2);
  expect(run.succeeded, JSON.stringify(run)).toBe(true);
  answerGoalRunsWith([task('Sort screenshots by month')]);
  run = await runNow(request, downloads, 3);
  expect(run.succeeded, JSON.stringify(run)).toBe(true);
  const backups = (await suggestedTasks(request, downloads)).filter(
    t => t.description === backupTitle('Downloads')
  );
  expect(backups).toEqual([]);
  // The dismissed task is no longer asked for.
  const requests = readFileSync(join(sandbox, 'fixture-requests.jsonl'), 'utf8')
    .trim()
    .split('\n')
    .map(line => JSON.stringify(JSON.parse(line)));
  const missions = requests.filter(line => line.includes('--- MISSION CONTEXT ---'));
  expect(missions.length).toBeGreaterThanOrEqual(3);
  expect(missions[0]).toContain('## Preconditions');
  expect(missions[missions.length - 1]).not.toContain('## Preconditions');
});

test('when the answer leaves the backup task out, the host adds it first in the Backlog', async ({
  page,
  request
}) => {
  await hire(page, 'Desktop');
  const desktop = await workspaceId(request, 'Desktop');
  await useFixtureModel(request, desktop);
  answerGoalRunsWith([task('Tidy the desktop icons'), task('Archive old screenshots')]);

  await page.goto('/workspaces/desktop?panel=backlog');
  const run = await waitForRuns(request, desktop, 1);
  expect(run.succeeded, JSON.stringify(run)).toBe(true);
  expect(run.tasks_created).toBe(3);
  const host = (await suggestedTasks(request, desktop)).filter(
    t => t.description === backupTitle('Desktop')
  );
  expect(host).toHaveLength(1);
  expect(host[0].priority).toBe(2);
  expect(host[0].context?.goal_suggestion).toMatchObject({
    precondition: 'backup_protection',
    action: 'check_protection',
    source: 'host'
  });

  await page.goto('/workspaces/desktop?panel=backlog');
  await expect(backlog(page).first()).toHaveText(backupTitle('Desktop'), { timeout: 30_000 });
  await backlog(page).first().click();
  await expect(page.locator('.ws-cmd-drawer-preview-actions button').first()).toHaveText('Back up');
});

test('a covered File Janitor folder gets no backup suggestion on either surface', async ({
  request
}) => {
  test.skip(
    !process.env.ORI_FOLDER_PROTECTION_COVERED_CONTROL,
    'Needs an operator-seeded covered fixture (see the header).'
  );
  const control = JSON.parse(
    readFileSync(process.env.ORI_FOLDER_PROTECTION_COVERED_CONTROL!, 'utf8')
  );
  expect(control.kind).toBe('invented stored metadata only');
  const id = control.file_janitor_workspace_id;
  const fact = await api(request, `/api/workspaces/${id}/file-janitor/protection`);
  expect(fact.state).toBe('covered');

  const today = (await api(request, '/api/personal-assistant/today')).today;
  const cards = (today.needs_you?.items || []).filter(
    (item: { kind: string; ref?: { workspace_id?: string } }) =>
      item.kind === 'folder_first_batch' && item.ref?.workspace_id === id
  );
  for (const card of cards) {
    expect(card.actions).not.toContain('check_protection');
    expect(card.suggestion).toBeUndefined();
  }

  await useFixtureModel(request, id);
  answerGoalRunsWith([task('Review the installer pile')]);
  const before = (await api(request, `/api/workspaces/${id}/mission`)).mission_runs?.length || 0;
  const run = await runNow(request, id, before + 1);
  expect(run.succeeded, JSON.stringify(run)).toBe(true);
  const backups = (await suggestedTasks(request, id)).filter(t =>
    /^Back up .+ before the first filing batch$/.test(t.description)
  );
  expect(backups).toEqual([]);
});
