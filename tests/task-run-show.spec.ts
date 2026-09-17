import { test, expect, Page, APIRequestContext } from '@playwright/test';

/**
 * task-run-show: a running task is visible on the map, and its result arrives
 * as a parcel (tasks/prd-task-run-show.md).
 *
 * Not part of CI. It needs a real server whose task runs play the dev-only
 * script instead of calling a model, on a fresh sandbox:
 *
 *   ORI_DEV_SCRIPTED_TASK_RUNS=1 ./scripts/demo-server.sh 8934
 *   ./scripts/e2e.sh --port 8934 tests/task-run-show.spec.ts -- --workers=1
 *
 * A scripted run takes about twelve seconds: started, thinking, two tool calls
 * with their results, thinking, completed — a real event on the bus for each.
 */

const RUN_TIMEOUT = 40_000;

// A scripted run plus its "Done." and parcel landing outlasts the default 30s.
test.describe.configure({ timeout: 120_000 });

type Seeded = { id: string; slug: string; name: string; agent: string };

async function seedWorkspace(request: APIRequestContext, label: string): Promise<Seeded> {
  await request.post('/api/onboarding/complete', { data: {} }).catch(() => {});
  const tag = `${label} ${String(Date.now()).slice(-6)}`;
  const agent = `Theo ${tag}`;
  const created = await request.post('/api/agents', {
    data: { name: agent, catalog_role: 'researcher' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const res = await request.post('/api/workspaces', {
    data: { name: `Lab ${tag}`, entry_agent_name: agent }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const folder = (await res.json()).folder;
  return { id: folder.id, slug: folder.folder_slug, name: folder.name, agent };
}

async function createTask(request: APIRequestContext, workspaceId: string, description: string) {
  const res = await request.post('/api/orchestration/tasks', {
    data: { workspace_id: workspaceId, description, priority: 2 }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  return (await res.json()).task.id as string;
}

async function startRun(request: APIRequestContext, workspaceId: string, description: string) {
  const taskId = await createTask(request, workspaceId, description);
  const res = await request.post('/api/orchestration/tasks/execute', { data: { task_id: taskId } });
  expect(res.ok(), await res.text()).toBeTruthy();
  return taskId;
}

// Count the page's activity streams, so "one connection per page" is a fact the
// test reads rather than infers.
async function countActivityStreams(page: Page) {
  await page.addInitScript(() => {
    const Native = window.EventSource;
    const opened: string[] = [];
    (window as unknown as { __activityStreams: string[] }).__activityStreams = opened;
    window.EventSource = class extends Native {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        if (String(url).includes('/api/workspace-map/activity/stream')) opened.push(String(url));
      }
    } as typeof EventSource;
  });
}

async function openHome(page: Page, workspace: Seeded) {
  await page.goto('/');
  const tile = page.locator(`.ws-map-tile[data-ws-id="${workspace.id}"]`);
  await tile.waitFor({ timeout: 30_000 });
  return tile;
}

// The bubble lines a building showed while a run played, in order.
async function watchBubble(page: Page, workspaceId: string, until: string) {
  return page.evaluate(
    ({ id, last, timeout }) =>
      new Promise<string[]>((resolve, reject) => {
        const seen: string[] = [];
        const started = Date.now();
        const timer = setInterval(() => {
          const line = document.querySelector(
            `.ws-map-tile[data-ws-id="${id}"] .ws-map-activity-line`
          );
          const text = line ? String(line.textContent) : '';
          if (text && seen[seen.length - 1] !== text) seen.push(text);
          if (text === last) {
            clearInterval(timer);
            resolve(seen);
          } else if (Date.now() - started > timeout) {
            clearInterval(timer);
            reject(new Error('bubble never said ' + last + '; saw ' + seen.join(' → ')));
          }
        }, 100);
      }),
    { id: workspaceId, last: until, timeout: RUN_TIMEOUT }
  );
}

test('a started run lights its building and speaks, over one activity stream', async ({ page }) => {
  const ws = await seedWorkspace(page.request, 'Lights');
  await countActivityStreams(page);
  const tile = await openHome(page, ws);
  await expect(tile.locator('[data-map-activity]')).toHaveCount(0);

  await startRun(page.request, ws.id, 'Compare the launch notes');
  await expect(tile.locator('.ws-map-activity.is-lit')).toBeVisible({ timeout: 10_000 });
  await expect(tile.locator('.ws-map-tile-flag')).toContainText('Working');

  const lines = await watchBubble(page, ws.id, 'Done.');
  expect(lines.length, lines.join(' → ')).toBeGreaterThanOrEqual(3);
  expect(lines).toContain('On it.');
  await expect(tile.locator('.ws-map-activity.is-lit')).toHaveCount(0, { timeout: 10_000 });

  const streams = await page.evaluate(
    () => (window as unknown as { __activityStreams: string[] }).__activityStreams
  );
  expect(streams).toHaveLength(1);
});

test('a finished run lands a parcel whose card shows its result and rewards', async ({ page }) => {
  const ws = await seedWorkspace(page.request, 'Parcel');
  const tile = await openHome(page, ws);
  await startRun(page.request, ws.id, 'Summarize the field notes');
  await watchBubble(page, ws.id, 'Done.');

  const pile = tile.locator('[data-harvest-pile]');
  await expect(pile).toHaveText('+1', { timeout: 15_000 });
  await pile.click();
  const row = page.locator('[data-parcel-open]').first();
  await expect(row).toBeVisible();
  await row.click();

  const card = page.locator('[data-result-card]');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Summarize the field notes');
  await expect(card).toContainText('Done');
  await expect(card.locator('[data-result-rewards]')).toContainText(/\+\d+ (XP|Craft)/);
  await page.keyboard.press('Escape');
  await expect(card).toHaveCount(0);
  await expect(pile).toHaveCount(0, { timeout: 10_000 });
});

test('a run that fails and is marked failed says it needs a look', async ({ page }) => {
  const ws = await seedWorkspace(page.request, 'Fail');
  const tile = await openHome(page, ws);
  const taskId = await startRun(page.request, ws.id, 'Import the venue list [fail]');

  // A manual run that errors is blocked, waiting on the user (PRD note 1).
  await expect(tile.locator('.ws-map-activity-bubble.is-blocked')).toContainText(
    'I need your input.',
    { timeout: RUN_TIMEOUT }
  );
  const marked = await page.request.post(`/api/orchestration/tasks/${taskId}/assist`, {
    data: { action: 'mark_failed' }
  });
  expect(marked.ok(), await marked.text()).toBeTruthy();

  const pile = tile.locator('[data-harvest-pile]');
  await expect(pile).toHaveText('+1', { timeout: 20_000 });
  await pile.click();
  const row = page.locator('[data-parcel-open]').first();
  await expect(row.locator('xpath=ancestor::*[contains(@class,"ws-map-parcel")]')).toHaveClass(
    /is-attention/
  );
  await row.click();
  const card = page.locator('[data-result-card]');
  await expect(card).toContainText('Needs a look');
  await expect(card.locator('[data-result-primary]')).toHaveText('Open task');
});

test('Give a task… from Home creates and starts the task, and the building lights', async ({
  page
}) => {
  const ws = await seedWorkspace(page.request, 'Give');
  const tile = await openHome(page, ws);

  await tile.click({ button: 'right' });
  await page.locator('[data-menu-action="give-task"]').click();
  const composer = page.locator('[data-ws-map-composer]');
  await expect(composer).toBeVisible();
  await expect(composer.locator('[data-composer-input]')).toBeFocused();
  await composer.locator('[data-composer-input]').fill('Draft the launch agenda');
  await composer.locator('[data-composer-start]').click();

  await expect(composer).toHaveCount(0);
  await expect(tile.locator('.ws-map-activity.is-lit')).toBeVisible({ timeout: 10_000 });
  const tasks = await (
    await page.request.get(`/api/orchestration/tasks?workspace_id=${ws.id}`)
  ).json();
  const list = Array.isArray(tasks) ? tasks : tasks.tasks;
  expect(list.map((t: { description: string }) => t.description)).toContain(
    'Draft the launch agenda'
  );
  await watchBubble(page, ws.id, 'Done.');
});

test('the Operations map steps the working agent forward and brings the parcel to its feet', async ({
  page
}) => {
  const ws = await seedWorkspace(page.request, 'Ops');
  await page.goto(`/workspaces/${ws.slug}?mode=map`);
  const unit = page.locator(
    `.ws-cmd-map-agent[data-cmd-map-select-agent="${encodeURIComponent(ws.agent)}"]`
  );
  await unit.waitFor({ timeout: 30_000 });

  await startRun(page.request, ws.id, 'Check the venue booking');
  await expect(unit).toHaveClass(/is-activity-working/, { timeout: 10_000 });
  const bubble = page.locator('.ws-cmd-map-world [data-cmd-activity-bubble]');
  await expect(bubble).toBeVisible();
  await expect(bubble).toContainText('Done.', { timeout: RUN_TIMEOUT });
  await expect(unit).not.toHaveClass(/is-activity-working/, { timeout: 10_000 });
  await expect(page.locator('.ws-cmd-map-world [data-cmd-map-parcel]')).toHaveText('+1', {
    timeout: 15_000
  });
});

test('with reduced motion, neither map runs an animation while work is shown', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const ws = await seedWorkspace(page.request, 'Still');
  const tile = await openHome(page, ws);
  await startRun(page.request, ws.id, 'Sort the inbox');
  await expect(tile.locator('.ws-map-activity.is-lit')).toBeVisible({ timeout: 10_000 });
  await expect(tile.locator('.ws-map-activity-line')).toBeVisible();

  const runningOn = (root: string) =>
    page.evaluate(
      selector =>
        document
          .getAnimations()
          .filter(animation => animation.playState === 'running')
          .map(animation => (animation as CSSAnimation).effect)
          .filter(effect => {
            const target = (effect as KeyframeEffect | null)?.target as Element | null;
            return !!target && !!target.closest(selector);
          }).length,
      root
    );
  expect(await runningOn('.ws-map-canvas')).toBe(0);
  await watchBubble(page, ws.id, 'Done.');

  await page.goto(`/workspaces/${ws.slug}?mode=map`);
  const unit = page.locator('.ws-cmd-map-agent').first();
  await unit.waitFor({ timeout: 30_000 });
  await startRun(page.request, ws.id, 'Sort the inbox again');
  await expect(unit).toHaveClass(/is-activity-working/, { timeout: 10_000 });
  await expect(page.locator('[data-cmd-activity-bubble]')).toBeVisible();
  expect(await runningOn('.ws-cmd-opmap')).toBe(0);
});

test('the activity stream never carries arguments, results or descriptions', async ({ page }) => {
  const ws = await seedWorkspace(page.request, 'Private');
  await page.goto('/');
  // Read the raw stream until this workspace's run finishes. One read at a
  // time: a read abandoned by a timeout would swallow the chunk it receives.
  const captured = page.evaluate(async workspaceId => {
    const response = await fetch('/api/workspace-map/activity/stream');
    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    const giveUp = setTimeout(() => void reader.cancel(), 60_000);
    const finished = `"phase":"finished","activity_id":"task:${workspaceId}:`;
    let text = '';
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      text += decoder.decode(value, { stream: true });
      if (text.includes(finished)) break;
    }
    clearTimeout(giveUp);
    await reader.cancel();
    return text;
  }, ws.id);
  await startRun(page.request, ws.id, 'SECRET launch figures for the board');
  const text = await captured;

  expect(text).toContain('event: activity');
  expect(text).toContain(`"phase":"finished","activity_id":"task:${ws.id}:`);
  expect(text).not.toContain('"arguments"');
  expect(text).not.toContain('"result"');
  expect(text).not.toContain('"description"');
});
