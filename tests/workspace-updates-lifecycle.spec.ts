import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real tabs, BroadcastChannel, exact acknowledgements and explicit local filing.
// ./scripts/e2e-goal-suggestions.sh --lead-welcome -- tests/workspace-updates-lifecycle.spec.ts --grep 'Updates lifecycle:'
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the isolated goal fixture wrapper');
test.setTimeout(120_000);
test.describe.configure({ mode: 'serial' });
async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBe(true);
  return response.json();
}
const updates = (request: APIRequestContext, id: string) =>
  request.get(`/api/workspaces/${id}/updates`).then(ok);
function modelCalls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path) ? readFileSync(path, 'utf8').split('\n').filter(Boolean).length : 0;
}
function operations(page: Page) {
  const writes: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (
      req.method() !== 'GET' &&
      /\/file-janitor\/(?:setup|scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
        path
      )
    )
      writes.push(`${req.method()} ${path}`);
  });
  return writes;
}
async function home(page: Page) {
  await page.goto('/');
  await expect(page.locator('[data-cockpit-workspace-update-badge]')).toBeVisible();
  await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
}
test.beforeAll(async ({ request }) => {
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
});
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
});

test('Updates lifecycle: real tabs reconcile exact exposure and partial filing without sharing authority', async ({
  page,
  context,
  request
}, info) => {
  const ws = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Two tabs · Filing', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  await ok(
    await request.post(`/api/workspaces/${ws.id}/capabilities/file-janitor/install`, {
      data: { source: 'in-place' }
    })
  );
  await ok(
    await request.post(`/api/workspaces/${ws.id}/capabilities/file-janitor/companion`, {
      data: {
        agent_instance_id: ws.agent_instances.find(
          (agent: { name: string }) => agent.name === 'Ask Ori'
        ).id
      }
    })
  );
  const root = mkdtempSync(join(sandbox, 'tabs-inbox-'));
  for (const name of ['report.pdf', 'uncertain.qqq']) {
    writeFileSync(join(root, name), 'synthetic two-tab fixture', { mode: 0o600 });
    const old = new Date(Date.now() - 3_600_000);
    utimesSync(join(root, name), old, old);
  }
  await ok(
    await request.post(`/api/workspaces/${ws.id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    })
  );
  await ok(await request.post(`/api/workspaces/${ws.id}/file-janitor/scan`, { data: {} }));
  await expect
    .poll(async () => (await updates(request, ws.id)).welcome.messages.length, { timeout: 45_000 })
    .toBe(2);
  const calls = modelCalls();
  const observer = await context.newPage();
  await installLocalCdn(observer);
  await observer.setViewportSize({ width: 1440, height: 1000 });
  const observerWrites = operations(observer);
  const writerWrites = operations(page);
  try {
    await home(observer);
    const row = observer.locator(`[data-update-row="${ws.id}"]`);
    await expect(row).toContainText('New message');
    await expect(row).toContainText('2 files to review');
    await observer.evaluate(() => {
      window.__updateHints = [];
      window.__hintObserver = new BroadcastChannel('ori.workspace-updates');
      window.__hintObserver.onmessage = event => window.__updateHints.push(event.data);
    });
    await page.goto(`/workspaces/${ws.folder_slug}`);
    await page.bringToFront();
    await expect(page.locator('#workspaceNextActionRegion')).toContainText(
      '2 files ready for review'
    );
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    const hintsBefore = await observer.evaluate(() => window.__updateHints.length);
    await page
      .locator('#workspaceNextActionRegion')
      .getByRole('button', { name: /Ask Ask Ori/ })
      .click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await page.locator('#personalAssistantScroll').hover();
    await page.mouse.wheel(0, -800);
    await expect.poll(async () => (await updates(request, ws.id)).welcome.unread_count).toBe(0);
    await expect
      .poll(() => observer.evaluate(() => window.__updateHints.length))
      .toBeGreaterThan(hintsBefore);
    await observer.bringToFront(); // hidden tabs reconcile on return, not by trusting the hint
    await expect(row).not.toContainText('New message');
    await expect(row).toContainText('2 files to review');
    expect(writerWrites).toEqual([]);
    expect(observerWrites).toEqual([]);
    await observer.screenshot({
      path: info.outputPath('two-tabs-seen-review-remains.png'),
      fullPage: true
    });
    const savedSessionId = (await updates(request, ws.id)).welcome.session_id;
    const saved = await ok(await request.get(`/api/sessions/${savedSessionId}`));

    await page.bringToFront();
    await page.locator('#personalAssistantClose').click();
    await page
      .locator('#workspaceNextActionRegion')
      .getByRole('button', { name: 'Review files', exact: true })
      .click();
    await page
      .locator('#fileJanitorConsoleBody .fj-row-item')
      .filter({ hasText: 'report.pdf' })
      .locator('.fj-select')
      .check();
    await page.locator('#fileJanitorApprove').click();
    await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
    await page.locator('#fileJanitorConfirmApply').click();
    await expect.poll(() => existsSync(join(root, 'Filed', 'Documents', 'report.pdf'))).toBe(true);
    await expect.poll(async () => (await updates(request, ws.id)).review.pending_count).toBe(1);
    await observer.bringToFront();
    await expect(row).toContainText('1 file to review');
    await expect(row).not.toContainText('New message');
    expect(writerWrites).toEqual([
      `POST /api/workspaces/${ws.id}/file-janitor/preview`,
      `POST /api/workspaces/${ws.id}/file-janitor/apply`
    ]);
    expect(observerWrites).toEqual([]);
    expect((await ok(await request.get(`/api/sessions/${savedSessionId}`))).messages).toEqual(
      saved.messages
    );
    expect(modelCalls()).toBe(calls);
    const hints = await observer.evaluate(() => window.__updateHints);
    for (const hint of hints) {
      expect(Object.keys(hint).sort()).toEqual(['kind', 'source', 'workspace_id']);
      expect(hint.workspace_id).toBe(ws.id);
    }
    await observer.screenshot({
      path: info.outputPath('two-tabs-partial-filing.png'),
      fullPage: true
    });
    writeFileSync(
      info.outputPath('two-tabs-operations.json'),
      JSON.stringify(
        {
          canonical_seen: true,
          remaining_files: 1,
          writer_operations: writerWrites,
          observer_operations: observerWrites,
          provider_calls_before: calls,
          provider_calls_after: modelCalls(),
          hint_keys: Object.keys(hints[0]).sort(),
          received_hints: hints.length
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
    const previousBatch = (await updates(request, ws.id)).review.batch_id;
    writeFileSync(join(root, 'next-report.pdf'), 'synthetic later batch', { mode: 0o600 });
    const old = new Date(Date.now() - 3_600_000);
    utimesSync(join(root, 'next-report.pdf'), old, old);
    // A separately authorized new scan, deliberately without a browser hint.
    await ok(await request.post(`/api/workspaces/${ws.id}/file-janitor/scan`, { data: {} }));
    const later = await updates(request, ws.id);
    expect(later.review.batch_id).not.toBe(previousBatch);
    expect(later.review.pending_count).toBeGreaterThan(0);
    await observer.locator('[data-workspace-updates-refresh]').click();
    await expect(row).toContainText(
      `${later.review.pending_count} ${later.review.pending_count === 1 ? 'file' : 'files'} to review`
    );
    await expect(row).not.toContainText('New message');
    await page.bringToFront();
    await page.locator('[data-fj-console-close]').click();
    await expect(page.locator('#workspaceNextActionRegion')).toContainText(
      `${later.review.pending_count} ${later.review.pending_count === 1 ? 'file' : 'files'} ready for review`
    );
    expect((await ok(await request.get(`/api/sessions/${savedSessionId}`))).messages).toEqual(
      saved.messages
    );
    expect(modelCalls()).toBe(calls);
    await page.screenshot({
      path: info.outputPath('later-batch-history-unchanged.png'),
      fullPage: true
    });
  } finally {
    await observer.close();
  }
});

test('Updates lifecycle: a deleted saved conversation makes an old destination unavailable without resurrection', async ({
  page,
  request
}, info) => {
  const ws = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Deleted saved thread', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  await expect
    .poll(async () => (await updates(request, ws.id)).welcome.session_id || '')
    .not.toBe('');
  const before = await updates(request, ws.id);
  const calls = modelCalls();
  const writes = operations(page);
  await home(page);
  const destination = await page
    .locator(`[data-update-row="${ws.id}"] [data-workspace-update="welcome"]`)
    .getAttribute('href');
  expect(destination).toContain('update=welcome');
  expect((await request.delete(`/api/sessions/${before.welcome.session_id}`)).status()).toBe(204);
  await page.goto(destination!);
  await expect(page.locator('#workspaceNextActionRegion')).toContainText(
    'This update is unavailable or its destination changed'
  );
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect(new URL(page.url()).searchParams.has('update')).toBe(false);
  expect((await ok(await request.get(`/api/sessions?folder_id=${ws.id}`))).sessions).toHaveLength(
    0
  );
  expect((await updates(request, ws.id)).welcome.state).toBe('unavailable');
  expect(writes).toEqual([]);
  expect(modelCalls()).toBe(calls);
  await page.screenshot({ path: info.outputPath('deleted-destination.png'), fullPage: true });
  await page.reload();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect((await ok(await request.get(`/api/sessions?folder_id=${ws.id}`))).sessions).toHaveLength(
    0
  );
});
