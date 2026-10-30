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

test('creation opens the lead conversation, then brings its first assessment once', async ({
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
  await installLocalCdn(page);
  await page.goto(`/workspaces/${slug}`);
  const panel = page.locator('#personalAssistantPanel');
  await expect(panel).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Ask Ori');
  const messages = page.locator('#homeAssistantConversation');
  await expect(messages).toContainText('Hi, I’m Ask Ori');
  await expect(page.locator('#personalAssistantWorkspaceContext')).toHaveText(
    'Context · Newsletter Desk'
  );
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
  const reopened = other.waitForResponse(response =>
    response.url().endsWith('/lead-conversation/open')
  );
  await other.goto(`/workspaces/${slug}`);
  expect((await (await reopened).json()).changed).toBe(false);
  await expect(other.locator('#personalAssistantPanel')).toBeHidden();
  await other.close();
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

test('the Curator offers filing choices without waiting for a goal assessment', async ({
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
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      /\/file-janitor\/(scan|prepare-review|decisions|preview|apply)$/.test(
        new URL(req.url()).pathname
      )
    )
      automaticActions++;
  });
  await installLocalCdn(page);
  await page.goto(`/workspaces/${slug}`);
  await expect(page.locator('#personalAssistantPanel')).toBeVisible({ timeout: 30000 });
  const messages = page.locator('#homeAssistantConversation');
  await expect(messages).toContainText('1. Review proposed filing');
  await expect(messages).toContainText('2. Discuss uncertain files');
  await expect(messages).toContainText('3. Understand the filing rules');
  await expect(messages).toContainText('2 items waiting for review');
  await expect(messages).toContainText('I’d start with the proposed filing');
  await expect(messages).not.toContainText('saved assessment');
  await expect(messages).not.toContainText('Goal settings');
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
    path: testInfo.outputPath('curator-welcome-choices.png'),
    fullPage: true
  });
  await page.locator('#personalAssistantInput').fill('2');
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
});

test('a draft is preserved, and a workspace without a review gets an honest introduction', async ({
  page,
  request
}) => {
  const before = modelCalls().length;
  const created = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Unreviewed Desk', entry_agent_name: 'Ask Ori' }
    })
  );
  const { id, folder_slug: slug } = created.folder;
  await installLocalCdn(page);
  await page.addInitScript(() =>
    sessionStorage.setItem('ori.personalAssistant.panelDraft', 'My unsent question')
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
  await page.locator('#personalAssistantInput').fill('');
  await page.locator('#personalAssistantPanelTitle').click();
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
  expect((await ok(await request.get(`/api/sessions?folder_id=${id}`))).sessions).toHaveLength(1);
});
