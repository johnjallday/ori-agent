import { expect, test, type APIRequestContext } from '@playwright/test';
import { existsSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the isolated goal fixture wrapper');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBe(true);
  return response.json();
}
const updates = (request: APIRequestContext, id: string) =>
  request.get(`/api/workspaces/${id}/updates`).then(ok);
function calls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path) ? readFileSync(path, 'utf8').split('\n').filter(Boolean).length : 0;
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
  await page.setViewportSize({ width: 1280, height: 900 });
});

test('Updates guards: a real Review modal blocks transcript exposure until it closes', async ({
  page,
  request
}, info) => {
  const ws = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'Modal exposure guard', entry_agent_name: 'Ask Ori' }
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
      data: { agent_instance_id: ws.agent_instances[0].id }
    })
  );
  const root = mkdtempSync(join(sandbox, 'modal-inbox-'));
  writeFileSync(join(root, 'report.pdf'), 'synthetic modal fixture', { mode: 0o600 });
  const old = new Date(Date.now() - 3_600_000);
  utimesSync(join(root, 'report.pdf'), old, old);
  await ok(
    await request.post(`/api/workspaces/${ws.id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    })
  );
  await ok(await request.post(`/api/workspaces/${ws.id}/file-janitor/scan`, { data: {} }));
  await expect
    .poll(async () => (await updates(request, ws.id)).welcome.messages.length, { timeout: 45_000 })
    .toBe(2);
  const saved = (await updates(request, ws.id)).welcome;
  const modelCalls = calls();
  let cover = false;
  let seenWrites = 0;
  page.on('request', req => {
    if (req.url().endsWith('/updates/seen')) seenWrites++;
  });
  await page.route(`**/api/sessions/${saved.session_id}`, async route => {
    const response = await route.fetch();
    if (cover) {
      cover = false;
      // Explicit navigation through the real console owner while hydration is
      // in flight. No guard/state DTO is mocked and no file action is invoked.
      await page.evaluate(() => (window as any).FileJanitorConsole.openLatestReview());
      await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible();
    }
    await route.fulfill({ response });
  });
  await page.goto(`/workspaces/${ws.folder_slug}`);
  await expect(page.locator('#personalAssistantUnread')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Nothing moves without your explicit approval'
  );
  cover = true;
  expect(
    await page.evaluate(
      id => (window as any).PersonalAssistantPanel.openWorkspaceWelcome(id),
      ws.id
    )
  ).toBe(true);
  await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Nothing moves without your explicit approval'
  );
  // Flush exposure scheduling repeatedly with actual transcript rows behind
  // the modal. Negative assertions must allow the observer to run.
  for (let i = 0; i < 3; i++) {
    await page.evaluate(() =>
      document.dispatchEvent(new CustomEvent('personal-assistant:exposure'))
    );
    await page.waitForTimeout(150);
  }
  expect(seenWrites).toBe(0);
  expect((await updates(request, ws.id)).welcome.unread_count).toBe(2);
  await page.screenshot({
    path: info.outputPath('review-modal-blocks-exposure.png'),
    fullPage: true
  });
  await page.locator('[data-fj-console-close]').click();
  await page.locator('#personalAssistantInput').focus();
  await page.locator('#personalAssistantScroll').hover();
  await page.mouse.wheel(0, -800);
  await expect.poll(async () => (await updates(request, ws.id)).welcome.unread_count).toBe(0);
  expect((await updates(request, ws.id)).review.pending_count).toBe(1);
  expect(calls()).toBe(modelCalls);
});

test('Updates guards: explicit New, a live user reply and an unsent draft cannot be replaced by a welcome', async ({
  page,
  request
}, info) => {
  const ws = (
    await ok(
      await request.post('/api/workspaces', {
        data: { name: 'New and busy conversation guard', entry_agent_name: 'Ask Ori' }
      })
    )
  ).folder;
  const welcome = (await updates(request, ws.id)).welcome;
  const modelCalls = calls();
  await page.addInitScript(
    id => sessionStorage.setItem(`ori.personalAssistant.recipientSession.${id}`, '-'),
    ws.id
  );
  let release = () => {};
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  let replying = false;
  let chatSession = '';
  await page.route('**/api/chat', async route => {
    chatSession = route.request().headers()['x-session-id'];
    replying = true;
    const response = await route.fetch(); // one real deterministic user reply
    await held;
    await route.fulfill({ response });
  });
  try {
    await page.goto(`/workspaces/${ws.folder_slug}`);
    await expect(page.locator('#personalAssistantUnread')).toBeVisible();
    // The ordinary panel opening seam honors the user's persisted New choice;
    // this is deliberately not the explicit saved-message launcher shortcut.
    await page.evaluate(() => (window as any).PersonalAssistantPanel.open());
    await expect(page.locator('#personalAssistantEntrySessionTitle')).toHaveText(
      'New conversation'
    );
    await expect(page.locator('#homeAssistantConversation')).not.toContainText(
      'I don’t have a saved assessment'
    );
    expect((await updates(request, ws.id)).welcome.unread_count).toBe(1);
    await page
      .locator('#personalAssistantInput')
      .fill('A synthetic explicit question in my new conversation.');
    await page.locator('#personalAssistantSend').click();
    await expect.poll(() => replying).toBe(true);
    expect(chatSession).toBeTruthy();
    expect(chatSession).not.toBe(welcome.session_id);
    const opened = await page.evaluate(
      id => (window as any).PersonalAssistantPanel.openWorkspaceWelcome(id),
      ws.id
    );
    expect(opened).toBe(false);
    expect(await page.evaluate(() => (window as any).PersonalAssistantRecipient.sessionId())).toBe(
      chatSession
    );
    await expect(page.locator('#homeAssistantConversation')).not.toContainText(
      'I don’t have a saved assessment'
    );
    expect((await updates(request, ws.id)).welcome.unread_count).toBe(1);
    release();
    await expect(page.locator('#homeAssistantConversation')).toContainText('Fixture model reply', {
      timeout: 30_000
    });
    await page.locator('#personalAssistantInput').fill('Keep this unsent follow-up');
    expect(
      await page.evaluate(
        id => (window as any).PersonalAssistantPanel.openWorkspaceWelcome(id),
        ws.id
      )
    ).toBe(false);
    await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this unsent follow-up');
    expect(await page.evaluate(() => (window as any).PersonalAssistantRecipient.sessionId())).toBe(
      chatSession
    );
    expect((await updates(request, ws.id)).welcome.unread_count).toBe(1);
    expect(calls()).toBe(modelCalls + 1);
    await page.screenshot({
      path: info.outputPath('new-reply-draft-preserved.png'),
      fullPage: true
    });
  } finally {
    release();
  }
});
