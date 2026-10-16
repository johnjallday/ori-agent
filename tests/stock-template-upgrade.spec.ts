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

// scripts/e2e-goal-suggestions.sh --upgrade runs this twice with a real process
// restart in between. No installed library, credential or live model is used.
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
const phase = process.env.ORI_GOAL_PHASE || '';
test.skip(
  !sandbox || !['before', 'after'].includes(phase),
  'requires the isolated --upgrade harness'
);
test.setTimeout(120_000);

async function ok(response: APIResponse) {
  const body = await response.json();
  expect(response.ok(), JSON.stringify(body)).toBe(true);
  return body.data || body;
}
async function mission(request: APIRequestContext, id: string) {
  return ok(await request.get(`/api/workspaces/${id}/mission`));
}
function intent(state: Record<string, unknown>) {
  return Object.fromEntries(
    [
      'mission',
      'cadence',
      'autonomy_policy',
      'mission_enabled',
      'first_run_pending',
      'first_run_state',
      'last_mission_run_at',
      'next_mission_run_at',
      'mission_execution_count',
      'mission_failure_count',
      'mission_runs'
    ].map(key => [key, state[key]])
  );
}
function calls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path)
    ? readFileSync(path, 'utf8')
        .split('\n')
        .filter(Boolean)
        .map(line => JSON.parse(line))
        .filter(item => item.kind === 'mission')
    : [];
}
async function screenshot(page: Page, name: string) {
  const dir = join(sandbox, 'upgrade-evidence');
  mkdirSync(dir, { recursive: true, mode: 0o750 });
  await expect(page.getByText('Loading workspace tasks…', { exact: true })).toHaveCount(0);
  await page.evaluate(async () => {
    await document.fonts.ready;
    window.scrollTo(0, 0);
  });
  await page.screenshot({ path: join(dir, name), fullPage: true });
}

test('stock upgrade preserves existing custom, empty and old defaults; only future creation inherits the refreshed goal', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await request.post('/api/onboarding/skip');
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
  const catalog = await ok(await request.get('/api/project-templates'));
  const relativeRoot = relative(realpathSync(sandbox), realpathSync(catalog.templates_root));
  expect(relativeRoot.startsWith('..') || isAbsolute(relativeRoot)).toBe(false);
  const stock = catalog.templates.find((t: { id: string }) => t.id === 'code-project');
  const record = join(sandbox, 'upgrade-state.json');
  if (phase === 'before') {
    const manifestPath = join(catalog.templates_root, 'code-project', 'template.json');
    const stale = JSON.parse(readFileSync(manifestPath, 'utf8'));
    stale.builtin_version--;
    stale.goal = 'The older project outcome';
    writeFileSync(manifestPath, JSON.stringify(stale), { mode: 0o600 });
    const saved = [];
    for (const [label, goal] of [
      ['custom', 'My existing project outcome'],
      ['empty', ''],
      ['default', undefined]
    ] as const) {
      const body = await ok(
        await request.post('/api/workspaces', {
          data: {
            name: `Upgrade ${label}`,
            template_id: 'code-project',
            ...(goal === undefined ? {} : { mission: goal })
          }
        })
      );
      const { id, folder_slug: slug } = body.folder;
      await page.goto(`/workspaces/${slug}`);
      await expect(page.locator('#workspace-command-mission-title')).toHaveText(
        (goal ?? stale.goal) || 'No goal yet'
      );
      if (label !== 'empty') {
        await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
          'Run now'
        );
        await expect.poll(async () => (await mission(request, id)).first_run_state).toBe('blocked');
      }
      saved.push({ id, slug, label, state: intent(await mission(request, id)) });
      await screenshot(page, `before-${label}.png`);
    }
    expect(calls()).toHaveLength(0);
    writeFileSync(
      record,
      JSON.stringify({ saved, currentGoal: stock.goal, version: stock.builtin_version }),
      { mode: 0o600 }
    );
    return;
  }

  const previous = JSON.parse(readFileSync(record, 'utf8'));
  expect(stock.goal).toBe(previous.currentGoal);
  expect(stock.builtin_version).toBe(previous.version);
  for (const saved of previous.saved) {
    expect(intent(await mission(request, saved.id))).toEqual(saved.state);
    await page.goto(`/workspaces/${saved.slug}`);
    await expect(page.locator('#workspace-command-mission-title')).toHaveText(
      saved.state.mission || 'No goal yet'
    );
    await screenshot(page, `after-${saved.label}.png`);
    expect(intent(await mission(request, saved.id))).toEqual(saved.state);
  }
  const future = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Upgrade future', template_id: 'code-project' }
    })
  );
  expect((await mission(request, future.folder.id)).mission).toBe(stock.goal);
  await page.goto(`/workspaces/${future.folder.folder_slug}`);
  await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
    'Start first look'
  );
  await screenshot(page, 'future-default.png');
  expect(calls()).toHaveLength(0);

  // The guided HQ setup uses CreateFromTemplate internally, not a second goal
  // constructor. Its explicit brief setup is separate from goal cadence.
  const hq = await ok(
    await request.post('/api/personal-hq/setup', {
      data: {
        name: 'Assistant Goal HQ',
        timezone: 'UTC',
        schedule_days: ['mon'],
        schedule_time: '09:00',
        scope: 'selected',
        selected_workspace_ids: [],
        notify_on_ready: false
      }
    })
  );
  const id = hq.status.workspace_id;
  const stored = await mission(request, id);
  expect(stored.mission).toBe(
    catalog.templates.find((t: { id: string }) => t.id === 'personal-ops').goal
  );
  expect(stored.mission_runs).toHaveLength(0);
  expect(stored.mission_enabled).toBe(false);
  expect(calls()).toHaveLength(0);
  await page.goto(`/workspaces/${hq.status.workspace.folder_slug}`);
  await expect(page.locator('#workspace-command-mission-title')).toHaveText(stored.mission);
  await expect
    .poll(async () => (await mission(request, id)).mission_runs.length, { timeout: 60_000 })
    .toBe(1);
  await expect(page.locator('#workspace-command-mission-action-status')).toContainText(
    'Run complete'
  );
  await screenshot(page, 'assistant-created-goal.png');
  expect(calls()).toHaveLength(1);
});
