/*
 * Drives the hired assistant's conversation in a real browser against a running
 * demo server, for the Demo: checkpoints in
 * tasks/tasks-assistant-draft-save-remember.md.
 *
 *   node scripts/demo-assistant-conversation.mjs <baseUrl> <outDir> [stage]
 *
 * The server must already have a hired assistant, a Personal HQ, and a system
 * model (./scripts/smoke.sh showfolder <baseUrl> hq, then ... model). Every
 * reply here comes from that model; nothing is mocked. Use fictional text only.
 *
 * Stages:
 *   conversation  draft -> "make it warmer" -> "give it to me in Korean", then
 *                 reload (same tab keeps the thread), New conversation, and
 *                 Continue back into the first one.
 *
 * Prints one JSON evidence object: conversation IDs, canonical message IDs,
 * what the sessions API holds, and any console errors or failed requests.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'conversation'] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-assistant-conversation.mjs <baseUrl> <outDir> [stage]');
  process.exit(1);
}
const out = resolve(outDir);
mkdirSync(out, { recursive: true });

const REPLY_TIMEOUT_MS = 180000;
const evidence = { stage, problems: [], steps: [] };

const browser = await chromium.launch();
// One context, so a second page is a second tab of the same browser.
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await context.newPage();
page.on('console', m => {
  if (m.type() === 'error') evidence.problems.push(`console: ${m.text()}`);
});
page.on('requestfailed', r => evidence.problems.push(`request failed: ${r.url()}`));
page.on('response', r => {
  if (r.status() >= 400) evidence.problems.push(`HTTP ${r.status()}: ${r.url()}`);
});

const shot = async name => {
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file });
  return file;
};

async function openAsk() {
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  const launcher = page.locator('#personalAssistantLauncher');
  await launcher.waitFor({ state: 'visible', timeout: 20000 });
  if (await page.locator('#personalAssistantPanel').isHidden()) await launcher.click();
  const askTab = page.locator('#personalAssistantAskTab');
  if (await askTab.count()) await askTab.click();
  await page
    .locator('#personalAssistantConversationBar')
    .waitFor({ state: 'visible', timeout: 20000 });
}

const rows = () =>
  page.locator('#homeAssistantConversation [data-message-role]').evaluateAll(nodes =>
    nodes.map(node => ({
      role: node.dataset.messageRole,
      messageId: node.dataset.messageId || '',
      conversationId: node.dataset.conversationId || '',
      text: node.textContent.trim()
    }))
  );

const panelState = () =>
  page.evaluate(() => ({
    conversationId: window.PersonalAssistantConversation.currentId(),
    storedId: window.sessionStorage.getItem('ori.personalAssistant.conversation') || '',
    title: document.getElementById('personalAssistantConversationTitle').textContent,
    note: document.getElementById('personalAssistantConversationNote').textContent,
    composer: document.getElementById('personalAssistantInput').value
  }));

async function say(text) {
  const before = (await rows()).filter(row => row.role === 'assistant').length;
  await page.locator('#personalAssistantInput').fill(text);
  await page.locator('#personalAssistantSend').click();
  await page.waitForFunction(
    count =>
      !window.OriAskRouting.getState().busy &&
      document.querySelectorAll('#homeAssistantConversation [data-message-role="assistant"]')
        .length > count,
    before,
    { timeout: REPLY_TIMEOUT_MS }
  );
  const all = await rows();
  return all[all.length - 1];
}

async function api(path) {
  return page.evaluate(async url => {
    const response = await fetch(url, { headers: { Accept: 'application/json' } });
    return { status: response.status, body: await response.json().catch(() => null) };
  }, path);
}

try {
  if (stage !== 'conversation') throw new Error(`unknown stage: ${stage}`);

  await openAsk();
  const before = await api('/api/workspaces');
  evidence.steps.push({
    step: 'opened',
    state: await panelState(),
    shot: await shot('01-ask-empty')
  });

  const draft = await say('Write a short birthday greeting for my friend Mina.');
  evidence.steps.push({ step: 'draft', reply: draft, state: await panelState() });

  const warmer = await say('make it warmer');
  evidence.steps.push({ step: 'warmer', reply: warmer, state: await panelState() });

  const korean = await say('give it to me in Korean');
  const first = await panelState();
  evidence.steps.push({
    step: 'korean',
    reply: korean,
    state: first,
    rows: await rows(),
    shot: await shot('02-three-turns')
  });

  // Same tab, after a reload: the thread comes back from the server.
  await openAsk();
  await page.waitForFunction(
    () => document.querySelectorAll('#homeAssistantConversation [data-message-id]').length >= 6,
    null,
    { timeout: 20000 }
  );
  evidence.steps.push({
    step: 'reloaded',
    state: await panelState(),
    rows: (await rows()).length,
    shot: await shot('03-after-reload')
  });

  // A deliberate new conversation starts empty and stores nothing until Send.
  await page.locator('#personalAssistantConversationNew').click();
  evidence.steps.push({
    step: 'new-conversation',
    state: await panelState(),
    rows: (await rows()).length,
    shot: await shot('04-new-conversation')
  });
  const fresh = await say('Suggest one name for a houseplant.');
  const second = await panelState();
  evidence.steps.push({ step: 'second-thread', reply: fresh, state: second });

  // Continue: pick the first conversation from the validated list.
  await page.locator('#personalAssistantConversationContinue').click();
  await page
    .locator(`#personalAssistantConversationList [data-conversation-id="${first.conversationId}"]`)
    .waitFor({ state: 'visible', timeout: 20000 });
  evidence.steps.push({ step: 'continue-list', shot: await shot('05-continue-list') });
  await page
    .locator(`#personalAssistantConversationList [data-conversation-id="${first.conversationId}"]`)
    .click();
  await page.waitForFunction(
    id =>
      window.PersonalAssistantConversation.currentId() === id &&
      document.querySelectorAll('#homeAssistantConversation [data-message-id]').length >= 6,
    first.conversationId,
    { timeout: 20000 }
  );
  evidence.steps.push({
    step: 'continued',
    state: await panelState(),
    rows: (await rows()).length,
    shot: await shot('06-continued')
  });

  // A second send while a reply is in flight is refused and its text is kept.
  const usersBefore = (await rows()).filter(row => row.role === 'user').length;
  await page.locator('#personalAssistantInput').fill('Add a second line about cake.');
  await page.locator('#personalAssistantSend').click();
  await page.locator('#personalAssistantInput').fill('this second message must wait');
  await page.locator('#personalAssistantSend').click();
  const duringReply = {
    composer: await page.locator('#personalAssistantInput').inputValue(),
    status: await page.locator('#personalAssistantPanelStatus').textContent()
  };
  await page.waitForFunction(() => !window.OriAskRouting.getState().busy, null, {
    timeout: REPLY_TIMEOUT_MS
  });
  evidence.steps.push({
    step: 'double-submit',
    duringReply,
    userRowsAdded: (await rows()).filter(row => row.role === 'user').length - usersBefore
  });
  await page.locator('#personalAssistantInput').fill('');

  // Another tab starts with no conversation of its own.
  const otherTab = await context.newPage();
  await otherTab.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await otherTab
    .locator('#personalAssistantLauncher')
    .waitFor({ state: 'visible', timeout: 20000 });
  await otherTab.waitForFunction(
    () => window.PersonalAssistantConversation?._state.hydrated,
    null,
    {
      timeout: 20000
    }
  );
  evidence.steps.push({
    step: 'second-tab',
    otherTabConversationId: await otherTab.evaluate(() =>
      window.PersonalAssistantConversation.currentId()
    ),
    thisTabConversationId: (await panelState()).conversationId
  });
  await otherTab.close();

  // Narrow layout.
  await page.setViewportSize({ width: 390, height: 844 });
  evidence.steps.push({ step: 'narrow', shot: await shot('07-narrow') });
  await page.setViewportSize({ width: 1440, height: 900 });

  // What the canonical stores hold.
  const sessions = await api('/api/sessions?limit=50&sort=updated_desc');
  const after = await api('/api/workspaces');
  const firstSession = await api(`/api/sessions/${first.conversationId}`);
  evidence.canonical = {
    separateThreads: first.conversationId !== second.conversationId,
    sessions: (sessions.body?.sessions || []).map(s => ({
      id: s.id,
      agent: s.agent_name,
      folder: s.folder_id,
      messages: s.message_count,
      title: s.title
    })),
    firstSessionMessages: (firstSession.body?.messages || []).map(m => ({
      id: m.id,
      role: m.role,
      model: m.model || ''
    })),
    workspacesBefore: Array.isArray(before.body)
      ? before.body.length
      : (before.body?.workspaces || []).length,
    workspacesAfter: Array.isArray(after.body)
      ? after.body.length
      : (after.body?.workspaces || []).length
  };
} catch (error) {
  evidence.error = String(error && error.stack ? error.stack : error);
  evidence.failureShot = await shot('failure').catch(() => '');
} finally {
  await browser.close();
}

evidence.problems = [...new Set(evidence.problems)];
console.log(JSON.stringify(evidence, null, 2));
process.exit(evidence.error ? 1 : 0);
