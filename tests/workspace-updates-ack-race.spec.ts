import { expect, test, type APIRequestContext } from '@playwright/test';
import { existsSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Actual saved assessment races a delayed exact exposure request, not a mock DTO.
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the isolated goal fixture wrapper');
test.setTimeout(120_000);
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

test('Updates acknowledgement race: an exposed introduction cannot mark a later saved assessment seen', async ({
  page,
  request
}, info) => {
  await installLocalCdn(page);
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
  const ws = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Delayed exposure receipt', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  const before = await updates(request, ws.id);
  expect(before.welcome.messages).toHaveLength(1);
  const intro = (await ok(await request.get(`/api/sessions/${before.welcome.session_id}`)))
    .messages[0];
  const calls = modelCalls();
  const navigationWrites: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (
      req.method() !== 'GET' &&
      /\/file-janitor\/(?:setup|scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
        path
      )
    )
      navigationWrites.push(path);
  });
  let release = () => {};
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  let captured: { session_id: string; message_id: string; revision: number } | null = null;
  let acknowledged: { welcome: { unread_count: number } } | null = null;
  await page.route(`**/api/workspaces/${ws.id}/updates/seen`, async route => {
    captured = route.request().postDataJSON();
    await held;
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    acknowledged = await response.json();
    await route.fulfill({ response });
  });
  try {
    await page.goto(`/workspaces/${ws.folder_slug}`);
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await page
      .locator('#workspaceNextActionRegion')
      .getByRole('button', { name: 'Open message', exact: true })
      .click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect.poll(() => captured).not.toBeNull();
    expect(captured).toEqual({
      session_id: before.welcome.session_id,
      message_id: before.welcome.messages[0].message_id,
      revision: 1
    });
    await page.locator('#personalAssistantClose').click();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    expect((await updates(request, ws.id)).welcome.unread_count).toBe(1);

    // Explicit preparation by the fixture, while only the older exposure is
    // held in transit. This does not open chat or authorize any file moves.
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
    const root = mkdtempSync(join(sandbox, 'ack-race-inbox-'));
    writeFileSync(join(root, 'report.pdf'), 'synthetic delayed ack fixture', { mode: 0o600 });
    const old = new Date(Date.now() - 3_600_000);
    utimesSync(join(root, 'report.pdf'), old, old);
    await ok(
      await request.post(`/api/workspaces/${ws.id}/file-janitor/setup`, {
        data: { path: root, paused: true }
      })
    );
    await ok(await request.post(`/api/workspaces/${ws.id}/file-janitor/scan`, { data: {} }));
    await expect
      .poll(async () => (await updates(request, ws.id)).welcome.messages.length, {
        timeout: 45_000
      })
      .toBe(2);
    expect((await updates(request, ws.id)).welcome.unread_count).toBe(2);
    release();
    await expect.poll(() => acknowledged?.welcome.unread_count).toBe(1);
    const after = await updates(request, ws.id);
    expect(after.welcome.messages[0].seen_at).toBeTruthy();
    expect(after.welcome.messages[1].seen_at).toBeFalsy();
    expect(after.review.pending_count).toBe(1);
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('#personalAssistantUnread')).toBeVisible();
    const saved = await ok(await request.get(`/api/sessions/${before.welcome.session_id}`));
    expect(saved.messages).toHaveLength(2);
    expect(saved.messages[0]).toEqual(intro);
    expect((await ok(await request.get(`/api/sessions?folder_id=${ws.id}`))).sessions).toHaveLength(
      1
    );
    expect(modelCalls()).toBe(calls);
    expect(navigationWrites).toEqual([]);
    expect(readFileSync(join(root, 'report.pdf'), 'utf8')).toBe('synthetic delayed ack fixture');
    await page.screenshot({
      path: info.outputPath('later-message-still-unread.png'),
      fullPage: true
    });
    writeFileSync(
      info.outputPath('exact-ack-race.json'),
      JSON.stringify(
        {
          acknowledged_revision: captured!.revision,
          later_revision: after.welcome.messages[1].revision,
          unread_remaining: after.welcome.unread_count,
          pending_files: after.review.pending_count,
          session_count: 1,
          original_message_unchanged: true,
          provider_calls_before: calls,
          provider_calls_after: modelCalls()
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
  } finally {
    release();
  }
});
