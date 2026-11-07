import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  utimesSync,
  writeFileSync
} from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Two invocations, different actual server PIDs, one disposable HOME/database:
// ./scripts/e2e-goal-suggestions.sh --upgrade -- tests/workspace-updates-restart.spec.ts --grep 'Updates restart:'
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
const phase = process.env.ORI_GOAL_PHASE || '';
test.skip(
  !sandbox || !['before', 'after'].includes(phase),
  'requires the isolated --upgrade wrapper'
);
test.setTimeout(120_000);

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBe(true);
  return response.json();
}
const updates = (request: APIRequestContext, id: string) =>
  request.get(`/api/workspaces/${id}/updates`).then(ok);
const savedSession = (request: APIRequestContext, id: string) =>
  request.get(`/api/sessions/${id}`).then(ok);
function modelCalls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path) ? readFileSync(path, 'utf8').split('\n').filter(Boolean).length : 0;
}
async function capture(page: Page, name: string) {
  const dir = join(sandbox, 'updates-restart-evidence');
  mkdirSync(dir, { recursive: true, mode: 0o750 });
  await page.screenshot({ path: join(dir, name), fullPage: true });
}

test('Updates restart: unread, exact seen after a lost response, and saved filing survive a new server process', async ({
  page,
  request,
  baseURL
}) => {
  await installLocalCdn(page);
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const port = new URL(baseURL!).port;
  expect(port).toMatch(/^\d+$/);
  const pid = Number(
    execFileSync('lsof', ['-nP', '-t', `-iTCP:${port}`, '-sTCP:LISTEN'], {
      encoding: 'utf8'
    }).trim()
  );
  expect(Number.isInteger(pid) && pid > 0).toBe(true);
  const statePath = join(sandbox, 'updates-restart-state.json');
  const execution: string[] = [];
  page.on('request', req => {
    if (
      req.method() !== 'GET' &&
      /\/file-janitor\/(?:setup|scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
        new URL(req.url()).pathname
      )
    )
      execution.push(req.url());
  });
  if (phase === 'before') {
    await ok(await request.post('/api/onboarding/skip'));
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    await ok(
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'fixture-model:latest' }
      })
    );
    const unread = (
      await ok(
        await request.post('/api/workspaces', {
          data: { name: 'Restart · Unread filing', entry_agent_name: 'Ask Ori' }
        })
      )
    ).folder;
    const seen = (
      await ok(
        await request.post('/api/workspaces', {
          data: { name: 'Restart · Exposed message', entry_agent_name: 'Ask Ori' }
        })
      )
    ).folder;
    const welcome = (await updates(request, seen.id)).welcome;
    expect(welcome.unread_count).toBe(1);
    let lost = 0;
    await page.route(`**/api/workspaces/${seen.id}/updates/seen`, async route => {
      const response = await route.fetch(); // the real server commits the exact exposure
      expect(response.ok()).toBe(true);
      lost++;
      await route.abort('failed'); // but the client never receives its response
    });
    await page.goto(`/workspaces/${seen.folder_slug}`);
    await expect(page.locator('#personalAssistantUnread')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await page
      .locator('#workspaceNextActionRegion')
      .getByRole('button', { name: 'Open message', exact: true })
      .click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect.poll(() => lost).toBeGreaterThan(0);
    await expect.poll(async () => (await updates(request, seen.id)).welcome.unread_count).toBe(0);
    await page.unroute(`**/api/workspaces/${seen.id}/updates/seen`);
    await page.locator('#personalAssistantClose').click();
    await page.reload(); // authoritative reread recovers the lost response
    await expect(page.locator('#workspaceNextActionRegion')).toBeVisible();
    await expect(page.locator('#personalAssistantUnread')).toBeHidden();
    const seenSummary = await updates(request, seen.id);
    expect(seenSummary.welcome.messages[0].seen_at).toBeTruthy();
    const seenMessages = (await savedSession(request, welcome.session_id)).messages;
    const deleted = (
      await ok(
        await request.post('/api/workspaces', {
          data: { name: 'Restart · Deleted history', entry_agent_name: 'Ask Ori' }
        })
      )
    ).folder;
    const deletedWelcome = (await updates(request, deleted.id)).welcome;
    expect((await request.delete(`/api/sessions/${deletedWelcome.session_id}`)).status()).toBe(204);

    // Existing folder records enter through the normal importer, not successful
    // creation/enrollment. No schema downgrade or live database edits are used.
    const legacy = ['legacy-quiet-entry', 'legacy-unvisited'].map(slug => {
      const record = {
        id: randomUUID(),
        name: slug,
        kind: 'workspace',
        folder_slug: slug,
        owner_user_id: 'local',
        status: 'active',
        version: 1,
        created_at: '2025-01-01T00:00:00Z',
        updated_at: '2025-01-01T00:00:00Z',
        agent_instances: [
          {
            id: randomUUID(),
            name: 'Ask Ori',
            instance_number: 1,
            entry_point: true,
            created_at: '2025-01-01T00:00:00Z'
          }
        ],
        shared_data: { entry_agent_name: 'Ask Ori' },
        ticket_migration_version: 1
      };
      const dir = join(sandbox, 'Ori Workspaces', slug);
      mkdirSync(dir, { mode: 0o750 });
      writeFileSync(join(dir, 'workspace.json'), JSON.stringify(record), { mode: 0o600 });
      return record;
    });

    // Save readiness last, then stop the host. Startup can recover the existing
    // eligible assessment if the ordinary periodic recovery has not run yet.
    await ok(
      await request.post(`/api/workspaces/${unread.id}/capabilities/file-janitor/install`, {
        data: { source: 'in-place' }
      })
    );
    await ok(
      await request.post(`/api/workspaces/${unread.id}/capabilities/file-janitor/companion`, {
        data: {
          agent_instance_id: unread.agent_instances.find(
            (agent: { name: string }) => agent.name === 'Ask Ori'
          ).id
        }
      })
    );
    const root = mkdtempSync(join(sandbox, 'restart-inbox-'));
    for (const name of ['report.pdf', 'uncertain.qqq']) {
      writeFileSync(join(root, name), 'synthetic restart fixture', { mode: 0o600 });
      const old = new Date(Date.now() - 3_600_000);
      utimesSync(join(root, name), old, old);
    }
    await ok(
      await request.post(`/api/workspaces/${unread.id}/file-janitor/setup`, {
        data: { path: root, paused: true }
      })
    );
    await ok(await request.post(`/api/workspaces/${unread.id}/file-janitor/scan`, { data: {} }));
    const unreadSummary = await updates(request, unread.id);
    expect(unreadSummary.welcome.unread_count).toBeGreaterThan(0);
    expect(unreadSummary.review.pending_count).toBe(2);
    const unreadMessages = (await savedSession(request, unreadSummary.welcome.session_id)).messages;
    const batches = await ok(
      await request.get(`/api/workspaces/${unread.id}/file-janitor/batches`)
    );
    writeFileSync(
      statePath,
      JSON.stringify({
        pid,
        unread,
        seen,
        root,
        unreadSummary,
        seenSummary,
        unreadMessages,
        seenMessages,
        deleted,
        legacy,
        batches,
        modelCalls: modelCalls(),
        lost
      }),
      { mode: 0o600 }
    );
    await capture(page, 'before-seen-response-recovered.png');
  } else {
    const before = JSON.parse(readFileSync(statePath, 'utf8'));
    expect(pid).not.toBe(before.pid);
    expect((await updates(request, before.deleted.id)).welcome.state).toBe('unavailable');
    expect(
      (await ok(await request.get(`/api/sessions?folder_id=${before.deleted.id}`))).sessions
    ).toHaveLength(0);
    await expect
      .poll(async () => (await updates(request, before.unread.id)).welcome.messages.length, {
        timeout: 45_000
      })
      .toBe(2);
    const unread = await updates(request, before.unread.id);
    const seen = await updates(request, before.seen.id);
    expect(unread.welcome.session_id).toBe(before.unreadSummary.welcome.session_id);
    expect(unread.welcome.unread_count).toBe(2);
    expect(unread.review.pending_count).toBe(2);
    expect(unread.welcome.messages.slice(0, before.unreadSummary.welcome.messages.length)).toEqual(
      before.unreadSummary.welcome.messages
    );
    expect(seen.welcome).toEqual(before.seenSummary.welcome);
    expect((await savedSession(request, seen.welcome.session_id)).messages).toEqual(
      before.seenMessages
    );
    const recovered = (await savedSession(request, unread.welcome.session_id)).messages;
    expect(recovered).toHaveLength(2);
    expect(recovered.slice(0, before.unreadMessages.length)).toEqual(before.unreadMessages);
    expect(recovered[1].content).toContain('2 files ready for review');
    expect(
      await ok(await request.get(`/api/workspaces/${before.unread.id}/file-janitor/batches`))
    ).toEqual(before.batches);
    for (const ws of [before.unread, before.seen])
      expect(
        (await ok(await request.get(`/api/sessions?folder_id=${ws.id}`))).sessions
      ).toHaveLength(1);
    for (const name of ['report.pdf', 'uncertain.qqq'])
      expect(readFileSync(join(before.root, name), 'utf8')).toBe('synthetic restart fixture');
    await page.goto(`/workspaces/${before.unread.folder_slug}`);
    await expect(page.locator('#workspaceNextActionRegion')).toContainText(
      '2 files ready for review'
    );
    await expect(page.locator('#personalAssistantUnread')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await capture(page, 'after-restart-quiet-unread-and-filing.png');
    await page.reload();
    expect((await updates(request, before.unread.id)).welcome).toEqual(unread.welcome);
    expect(modelCalls()).toBe(before.modelCalls);
    // Background recovery must leave all imported legacy workspaces alone.
    for (const legacy of before.legacy) {
      await expect
        .poll(async () => (await request.get(`/api/workspaces/${legacy.id}/updates`)).status())
        .toBe(200);
      expect((await updates(request, legacy.id)).welcome.unread_count).toBe(0);
      expect(
        (await ok(await request.get(`/api/sessions?folder_id=${legacy.id}`))).sessions
      ).toHaveLength(0);
    }
    const legacy = before.legacy[0];
    await page.goto(`/workspaces/${legacy.folder_slug}`);
    // The legacy fallback deliberately retries on its existing five-second
    // readiness cadence; the first entry can still be hydrating its recipient.
    await expect(page.locator('#personalAssistantUnread')).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    const legacyWelcome = (await updates(request, legacy.id)).welcome;
    expect(legacyWelcome.unread_count).toBe(1);
    await page.reload();
    await expect(page.locator('#personalAssistantUnread')).toBeVisible();
    expect((await updates(request, legacy.id)).welcome).toEqual(legacyWelcome);
    expect(
      (await ok(await request.get(`/api/sessions?folder_id=${legacy.id}`))).sessions
    ).toHaveLength(1);
    expect(
      (await ok(await request.get(`/api/sessions?folder_id=${before.legacy[1].id}`))).sessions
    ).toHaveLength(0);
    expect(modelCalls()).toBe(before.modelCalls);
    await capture(page, 'legacy-quiet-on-entry.png');
    const evidence = {
      legacy_quiet_on_entry_only: true,
      unvisited_legacy_not_seeded: true,
      before_pid: before.pid,
      after_pid: pid,
      same_sandbox: true,
      before_unread_messages: before.unreadMessages.length,
      after_unread_messages: recovered.length,
      lost_seen_responses: before.lost,
      seen_survived: true,
      deleted_history_not_resurrected: true,
      batch_unchanged: true,
      file_bytes_unchanged: true,
      navigation_execution: execution,
      provider_calls_before: before.modelCalls,
      provider_calls_after: modelCalls()
    };
    writeFileSync(
      join(sandbox, 'updates-restart-evidence', 'operations.json'),
      JSON.stringify(evidence, null, 2),
      { mode: 0o600 }
    );
  }
  expect(execution).toEqual([]);
});
