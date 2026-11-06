import { expect, test, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync, mkdtempSync, utimesSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real host, sessions, review and chat. Only Ollama is a deterministic fixture.
// ./scripts/e2e-goal-suggestions.sh --lead-welcome
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the isolated goal fixture wrapper');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBeTruthy();
  return response.json();
}
function modelCalls() {
  return readFileSync(join(sandbox, 'fixture-requests.jsonl'), 'utf8')
    .trim()
    .split('\n')
    .filter(Boolean)
    .map(line => JSON.parse(line));
}

test.beforeAll(async ({ request }) => {
  await request.post('/api/onboarding/skip');
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
});

test('creation saves a quiet welcome before entry, then exposes its first assessment once', async ({
  page,
  request,
  context
}, testInfo) => {
  writeFileSync(
    join(sandbox, 'mission-response.txt'),
    JSON.stringify({
      assessment:
        'The newsletter has an outline. Editor approval is the remaining publication blocker.',
      items: [
        {
          kind: 'task',
          title: 'Get editor review of the newsletter',
          why: 'Approval must come before publication.',
          evidence: 'The outline is ready.',
          priority: 'high',
          confidence: 'high',
          effort: 'small',
          first_step: 'Review the outline with the editor.'
        }
      ]
    })
  );
  const created = await ok(
    await request.post('/api/workspaces', {
      data: {
        name: 'Newsletter Desk',
        mission: 'Publish the newsletter after editor approval.',
        entry_agent_name: 'Ask Ori'
      }
    })
  );
  const { id, folder_slug: slug } = created.folder;
  // No workspace page or drawer has been opened. Recovery is server-owned.
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count,
      { timeout: 45000 }
    )
    .toBe(1);
  await installLocalCdn(page);
  await page.goto(`/workspaces/${slug}`);
  const panel = page.locator('#personalAssistantPanel');
  await expect(page.locator('#personalAssistantUnread')).toBeVisible({ timeout: 30_000 });
  await expect(panel).toBeHidden();
  await page.screenshot({ path: testInfo.outputPath('quiet-unread-launcher.png'), fullPage: true });
  await page.locator('#personalAssistantLauncher').click();
  await expect(panel).toBeVisible();
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Ask Ori');
  const messages = page.locator('#homeAssistantConversation');
  await expect(messages).toContainText('Hi, I’m Ask Ori');
  await expect(page.locator('#personalAssistantWorkspaceContext')).toHaveText(
    'Context · Newsletter Desk'
  );
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count
    )
    .toBe(0);
  // A later host message is discovered quietly too; it does not replace the
  // currently displayed transcript or move its scroll position.
  await expect(page.locator('#personalAssistantWelcome')).toBeVisible({ timeout: 60_000 });
  await page.locator('#personalAssistantOpenWelcome').click();
  await expect(messages).toContainText('Get editor review of the newsletter', { timeout: 60_000 });
  await expect(messages).toContainText('Nothing has been started');
  await expect(messages).toContainText('Source: saved assessment completed');
  await page.screenshot({
    path: testInfo.outputPath('lead-first-conversation.png'),
    fullPage: true
  });
  const sessions = (await ok(await request.get(`/api/sessions?folder_id=${id}`))).sessions;
  expect(sessions).toHaveLength(1);
  const sessionId = sessions[0].id;
  const original = await ok(await request.get(`/api/sessions/${sessionId}`));
  expect(original.messages.every((message: { role: string }) => message.role === 'assistant')).toBe(
    true
  );
  expect(modelCalls().filter(call => call.kind === 'mission')).toHaveLength(1);
  expect(modelCalls().filter(call => call.kind === 'chat')).toHaveLength(0);

  // A second tab sees the durable conversation but does not announce it again.
  const other = await context.newPage();
  await installLocalCdn(other);
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count
    )
    .toBe(0);
  await other.goto(`/workspaces/${slug}`);
  await expect(other.locator('#personalAssistantLauncher')).toBeVisible();
  await expect(other.locator('#personalAssistantUnread')).toBeHidden();
  await expect(other.locator('#personalAssistantPanel')).toBeHidden();
  await other.close();
  await page.bringToFront();
  expect((await ok(await request.get(`/api/sessions/${sessionId}`))).messages).toHaveLength(
    original.messages.length
  );

  // The next real user turn stays in this very conversation, not a synthetic
  // user prompt or a second session created just to generate the greeting.
  const sent = page.waitForRequest(
    req => req.method() === 'POST' && new URL(req.url()).pathname === '/api/chat'
  );
  await page.locator('#personalAssistantInput').fill('Why should I focus on editor review?');
  await page.locator('#personalAssistantSend').click();
  expect((await sent).headers()['x-session-id']).toBe(sessionId);
  // This is a real host/provider round trip; allow cold startup under the CI load.
  await expect(messages).toContainText('Fixture model reply', { timeout: 30000 });
  const latest = await ok(await request.get(`/api/sessions/${sessionId}`));
  expect(latest.messages.length).toBe(original.messages.length + 2);
  await page.reload();
  await expect(messages).toContainText('Fixture model reply');
  expect((await ok(await request.get(`/api/sessions?folder_id=${id}`))).sessions).toHaveLength(1);
});

test('saved filing updates navigate independently and retain remaining review after exposure', async ({
  page,
  request
}, testInfo) => {
  const created = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Curator Choice Desk', entry_agent_name: 'Ask Ori' }
    })
  );
  const { id, folder_slug: slug, agent_instances: instances } = created.folder;
  const instance = instances.find((item: { name: string }) => item.name === 'Ask Ori');
  expect(instance).toBeTruthy();
  await ok(
    await request.post(`/api/workspaces/${id}/capabilities/file-janitor/install`, {
      data: { source: 'in-place' }
    })
  );
  await ok(
    await request.post(`/api/workspaces/${id}/capabilities/file-janitor/companion`, {
      data: { agent_instance_id: instance.id }
    })
  );
  const root = mkdtempSync(join(sandbox, 'curator-inbox-'));
  const old = new Date(Date.now() - 60 * 60 * 1000);
  for (const name of ['report.pdf', 'uncertain.qqq']) {
    writeFileSync(join(root, name), 'synthetic filing fixture', { mode: 0o600 });
    utimesSync(join(root, name), old, old);
  }
  await ok(
    await request.post(`/api/workspaces/${id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    })
  );
  await ok(await request.post(`/api/workspaces/${id}/file-janitor/scan`, { data: {} }));
  const batches = await ok(await request.get(`/api/workspaces/${id}/file-janitor/batches`));
  const calls = modelCalls().length;
  let automaticActions = 0;
  const filingOperations: string[] = [];
  page.on('request', req => {
    const operation = new URL(req.url()).pathname.match(
      /\/file-janitor\/(scan|prepare-review|decisions|preview|apply|undo)$/
    );
    if (req.method() === 'POST' && operation) {
      automaticActions++;
      filingOperations.push(operation[1]);
    }
  });
  await installLocalCdn(page);
  await expect
    .poll(
      async () => {
        const update = await ok(await request.get(`/api/workspaces/${id}/updates`));
        if (!update.welcome.session_id) return '';
        const saved = await ok(await request.get(`/api/sessions/${update.welcome.session_id}`));
        return saved.messages.map((message: { content: string }) => message.content).join('\n');
      },
      { timeout: 45000 }
    )
    .toContain('2 files ready for review');
  await page.goto(`/workspaces/${slug}`);
  const card = page.locator('#workspaceNextActionRegion');
  await expect(card).toContainText('2 files ready for review');
  await expect(page.locator('#personalAssistantUnread')).toBeVisible({ timeout: 30000 });
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  const beforeOpen = await ok(await request.get(`/api/workspaces/${id}/updates`));
  await expect(card.locator('h3')).toHaveText('2 files ready for review');
  await expect(card.getByRole('button', { name: 'Review files', exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('filing-update-card.png'), fullPage: true });
  await card.getByRole('button', { name: 'Review files', exact: true }).click();
  await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('report.pdf');
  expect((await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count).toBe(
    beforeOpen.welcome.unread_count
  );
  expect(automaticActions).toBe(0);
  await page.locator('[data-fj-console-close]').click();
  await card.getByRole('button', { name: /Ask Ask Ori/ }).click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  const messages = page.locator('#homeAssistantConversation');
  await expect(messages).toContainText('2 files ready for review');
  await expect(messages).toContainText('1 marked Needs review');
  await expect(messages).toContainText('Nothing moves without your explicit approval');
  await expect(messages).not.toContainText('reply with a number');
  // Hydration reveals the latest message, not necessarily the beginning of
  // the earlier introduction. Explicitly expose that earlier message too.
  await page.locator('#personalAssistantScroll').hover();
  await page.mouse.wheel(0, -600);
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count
    )
    .toBe(0);
  expect((await ok(await request.get(`/api/workspaces/${id}/updates`))).review.pending_count).toBe(
    2
  );
  await expect(card).toContainText('2 files ready for review');
  // An earlier generic introduction remains historical when a capability is
  // installed afterwards. The newly saved filing message is independent.
  const filingMessage = messages.locator('[data-lead-message-id]').last();
  await expect(filingMessage).not.toContainText('saved assessment');
  await expect(filingMessage).not.toContainText('Goal settings');
  expect(modelCalls().length).toBe(calls);
  expect((await ok(await request.get(`/api/workspaces/${id}/mission`))).mission_runs).toHaveLength(
    0
  );
  const sessions = (await ok(await request.get(`/api/sessions?folder_id=${id}`))).sessions;
  expect(sessions).toHaveLength(1);
  const sent = page.waitForRequest(
    req => req.method() === 'POST' && new URL(req.url()).pathname === '/api/chat'
  );
  await page.screenshot({
    path: testInfo.outputPath('curator-welcome-exposed.png'),
    fullPage: true
  });
  await page.locator('#personalAssistantInput').fill('Why are some files marked Needs review?');
  await page.locator('#personalAssistantSend').click();
  expect((await sent).headers()['x-session-id']).toBe(sessions[0].id);
  // Curator runtime/tool initialization can outlast the default UI assertion wait.
  await expect(messages).toContainText('Fixture model reply', { timeout: 30000 });
  expect(automaticActions).toBe(0);
  expect(await ok(await request.get(`/api/workspaces/${id}/file-janitor/batches`))).toEqual(
    batches
  );
  for (const name of ['report.pdf', 'uncertain.qqq']) {
    expect(existsSync(join(root, name))).toBe(true);
    expect(readFileSync(join(root, name), 'utf8')).toBe('synthetic filing fixture');
  }
  const savedBeforeFiling = await ok(await request.get(`/api/sessions/${sessions[0].id}`));
  await page.locator('#personalAssistantClose').click();
  await card.getByText('Source details', { exact: true }).click();
  await expect(card).toContainText('Batch completed');
  await expect(card).toContainText('Review observed');
  await expect(card).toContainText('Message 1 exposed');
  await page.screenshot({ path: testInfo.outputPath('filing-source-details.png'), fullPage: true });
  expect(automaticActions).toBe(0);

  // Only this explicit selection/preview/confirmation authorizes filing.
  await card.getByRole('button', { name: 'Review files', exact: true }).click();
  const row = page
    .locator('#fileJanitorConsoleBody .fj-row-item')
    .filter({ hasText: 'report.pdf' });
  await row.locator('.fj-select').check();
  await page.locator('#fileJanitorApprove').click();
  await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
  expect(existsSync(join(root, 'report.pdf'))).toBe(true);
  await page.locator('#fileJanitorConfirmApply').click();
  await expect
    .poll(() => existsSync(join(root, 'Filed', 'Documents', 'report.pdf')), { timeout: 30000 })
    .toBe(true);
  expect(existsSync(join(root, 'uncertain.qqq'))).toBe(true);
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).review.pending_count
    )
    .toBe(1);
  await page.locator('[data-fj-console-close]').click();
  await expect(card).toContainText('1 file ready for review');
  await expect(page.locator('#personalAssistantUnread')).toBeHidden();
  await page.screenshot({ path: testInfo.outputPath('filing-partial-update.png'), fullPage: true });
  expect((await ok(await request.get(`/api/sessions/${sessions[0].id}`))).messages).toEqual(
    savedBeforeFiling.messages
  );
  expect(modelCalls().length).toBe(calls + 1);
  const operations = Object.fromEntries(
    ['scan', 'prepare-review', 'decisions', 'preview', 'apply', 'undo'].map(kind => [
      kind,
      filingOperations.filter(value => value === kind).length
    ])
  );
  expect(operations.scan).toBe(0);
  expect(operations['prepare-review']).toBe(0);
  expect(operations.preview).toBe(1);
  expect(operations.apply).toBe(1);
  expect(operations.undo).toBe(0);
  writeFileSync(
    testInfo.outputPath('filing-operation-counts.json'),
    JSON.stringify(
      {
        explicit_fixture_preparation: { setup: 1, scan: 1 },
        navigation_mutations: 0,
        after_explicit_partial_filing: operations,
        update_generated_model_calls: 0,
        explicit_user_chat_calls: modelCalls().length - calls
      },
      null,
      2
    )
  );
});

test('a draft in another conversation is preserved while a quiet welcome waits', async ({
  page,
  request
}, testInfo) => {
  const before = modelCalls().length;
  const created = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Unreviewed Desk', entry_agent_name: 'Ask Ori' }
    })
  );
  const { id, folder_slug: slug } = created.folder;
  await installLocalCdn(page);
  await expect
    .poll(
      async () =>
        (await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count,
      { timeout: 45000 }
    )
    .toBe(1);
  const draftSession = await ok(
    await request.post('/api/sessions', {
      data: { title: 'Draft discussion', folder_id: id, agent_name: 'Ask Ori' }
    })
  );
  await page.addInitScript(
    ({ workspaceId, sessionId }) => {
      sessionStorage.setItem('ori.personalAssistant.panelDraft', 'My unsent question');
      sessionStorage.setItem(`ori.personalAssistant.recipientSession.${workspaceId}`, sessionId);
    },
    { workspaceId: id, sessionId: draftSession.id }
  );
  let deliveries = 0;
  page.on('request', req => {
    if (req.url().endsWith('/lead-conversation/open')) deliveries++;
  });
  await page.goto(`/workspaces/${slug}`);
  await expect(page.locator('#personalAssistantLauncher')).toContainText('Ask Ori');
  await page.waitForTimeout(5500); // one delivery polling interval: a negative assertion
  expect(deliveries).toBe(0);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('My unsent question');
  await expect(page.locator('#personalAssistantPanelStatus')).toContainText(
    'Your draft has been kept'
  );
  await expect(page.locator('#homeAssistantConversation')).not.toContainText('Hi, I’m');
  await page.screenshot({
    path: testInfo.outputPath('quiet-welcome-draft-guard.png'),
    fullPage: true
  });
  expect((await ok(await request.get(`/api/workspaces/${id}/updates`))).welcome.unread_count).toBe(
    1
  );
  await page.locator('#personalAssistantInput').fill('');
  await page.locator('#personalAssistantOpenWelcome').click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'I don’t have a saved assessment yet',
    { timeout: 15000 }
  );
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'What outcome would you like us to work toward?'
  );
  expect(modelCalls().length).toBe(before);
  expect((await ok(await request.get(`/api/workspaces/${id}/mission`))).mission_runs).toHaveLength(
    0
  );
  // One welcome plus the explicitly created draft discussion, never a third.
  expect((await ok(await request.get(`/api/sessions?folder_id=${id}`))).sessions).toHaveLength(2);
});
