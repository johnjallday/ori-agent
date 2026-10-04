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
 *   save          two versions, save the Korean one to the HQ backlog, verify
 *                 the Ticket, retry the same save, and the typed request.
 *   resume-save   save one draft and record it in <outDir>/resume-state.json.
 *   resume        run after restarting the server on the same sandbox: reopen
 *                 the saved draft, update the same Ticket, hit a conflict from
 *                 an outside edit, and open it after its chat was deleted.
 *
 * Prints one JSON evidence object: conversation IDs, canonical message IDs,
 * what the sessions API holds, and any console errors or failed requests.
 */
import { chromium } from 'playwright';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
      // The reply itself, without the message actions rendered beneath it.
      text: (node.firstElementChild?.firstChild?.textContent || node.textContent).trim(),
      actions: Array.from(node.querySelectorAll('[data-message-action]')).map(
        button => button.textContent
      ),
      saved: node.querySelector('.personal-assistant-message__saved')?.textContent || ''
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

async function conversationStage() {
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
}

const review = () =>
  page.evaluate(() => {
    const form = document.getElementById('personalAssistantDraftReview');
    const state = window.PersonalAssistantDrafts._state;
    return {
      open: !form.hidden,
      target: document.getElementById('personalAssistantDraftTarget').textContent,
      title: document.getElementById('personalAssistantDraftTitle').value,
      body: document.getElementById('personalAssistantDraftBody').value,
      notes: Array.from(document.querySelectorAll('#personalAssistantDraftNotes li')).map(
        item => item.textContent
      ),
      status: document.getElementById('personalAssistantDraftStatus').textContent,
      receipt: document.getElementById('personalAssistantDraftReceipt').textContent,
      receiptVisible: !form.querySelector('[data-draft-view="receipt"]').hidden,
      openHref: document.getElementById('personalAssistantDraftOpen').getAttribute('href'),
      operationId: state.review?.operation_id || '',
      payload: state.review
        ? {
            operation_id: state.review.operation_id,
            target_workspace_id: state.review.target.workspace_id,
            source: state.review.source
          }
        : null
    };
  });

const assistantTickets = async workspaceId => {
  const result = await api(
    `/api/workspaces/${workspaceId}/tickets?source=assistant&archive=all&limit=100`
  );
  return (result.body?.tickets || []).filter(ticket =>
    String(ticket.source_id || '').startsWith('assistant-draft:')
  );
};

async function post(path, body) {
  return page.evaluate(
    async ([url, payload]) => {
      const response = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(payload)
      });
      return { status: response.status, body: await response.json().catch(() => null) };
    },
    [path, body]
  );
}

/*
 * save: two versions of a draft, choose the Korean one, review and save it to
 * the HQ backlog, verify the canonical Ticket, retry the same save, and show
 * that an older version and the typed request open the same review.
 */
async function saveStage() {
  await openAsk();
  await page
    .locator('#personalAssistantConversationNew')
    .click({ force: true })
    .catch(() => {});
  const assistant = (await api('/api/personal-assistant')).body.personal_assistant;
  const hq = assistant.hq_workspace_id;
  const ticketsBefore = (await assistantTickets(hq)).length;

  const english = await say('Write a short birthday greeting for my friend Mina.');
  const korean = await say('give it to me in Korean');
  evidence.steps.push({ step: 'two-versions', english, korean });

  // Choose the Korean version with its own action. Opening the review saves nothing.
  const koreanRow = page.locator(
    `#homeAssistantConversation [data-message-id="${korean.messageId}"]`
  );
  await koreanRow.locator('[data-message-action="save-draft"]').click();
  await page.locator('#personalAssistantDraftReview').waitFor({ state: 'visible', timeout: 20000 });
  const opened = await review();
  evidence.steps.push({
    step: 'review-opened',
    review: opened,
    bodyIsExactReply: opened.body === korean.text,
    ticketsAfterOpening: (await assistantTickets(hq)).length - ticketsBefore,
    shot: await shot('10-review')
  });

  await page.locator('#personalAssistantDraftTitle').fill('Birthday greeting for Mina (Korean)');
  const saveRequest = {
    ...opened.payload,
    title: 'Birthday greeting for Mina (Korean)',
    body: opened.body
  };
  await page.locator('#personalAssistantDraftSave').click();
  await page
    .locator('#personalAssistantDraftReview [data-draft-view="receipt"]')
    .waitFor({ state: 'visible', timeout: 30000 });
  const receipt = await review();
  const saved = await assistantTickets(hq);
  const ticket = saved.find(item => item.source_id.includes(korean.messageId));
  evidence.steps.push({
    step: 'saved',
    receipt: receipt.receipt,
    openHref: receipt.openHref,
    rowBadge: (await rows()).find(row => row.messageId === korean.messageId)?.saved,
    ticketsAdded: saved.length - ticketsBefore,
    ticket: ticket && {
      id: ticket.id,
      number: ticket.display_number,
      title: ticket.title,
      state: ticket.state,
      assignee: ticket.assignee || '',
      scheduled: Boolean(ticket.schedule_enabled || ticket.due_date),
      bodyIsExactReply: ticket.description === korean.text,
      source: ticket.source
    },
    shot: await shot('11-receipt')
  });

  // The same save again — a retry after a lost response — is the same Ticket.
  const retry = await post('/api/home-assistant/drafts/save', saveRequest);
  evidence.steps.push({
    step: 'retry-same-save',
    status: retry.status,
    created: retry.body?.receipt?.created,
    sameTicket: retry.body?.receipt?.ticket_id === ticket?.id,
    ticketsAdded: (await assistantTickets(hq)).length - ticketsBefore
  });
  // The same review with different text is refused.
  const changed = await post('/api/home-assistant/drafts/save', {
    ...saveRequest,
    body: `${opened.body} (edited)`
  });
  evidence.steps.push({
    step: 'retry-changed-payload',
    status: changed.status,
    error: changed.body?.error,
    ticketsAdded: (await assistantTickets(hq)).length - ticketsBefore
  });
  await page.locator('#personalAssistantDraftDone').click();

  // The older English version is still selectable; cancel writes nothing.
  await page
    .locator(`#homeAssistantConversation [data-message-id="${english.messageId}"]`)
    .locator('[data-message-action="save-draft"]')
    .click();
  await page.locator('#personalAssistantDraftReview').waitFor({ state: 'visible', timeout: 20000 });
  const older = await review();
  await page.keyboard.press('Escape');
  evidence.steps.push({
    step: 'older-version-then-escape',
    bodyIsOlderReply: older.body === english.text,
    reviewClosed: !(await review()).open,
    panelStillOpen: await page.locator('#personalAssistantPanel').isVisible(),
    ticketsAdded: (await assistantTickets(hq)).length - ticketsBefore
  });

  // The typed request opens the same review for the latest reply, plus the
  // reminder limitation, and saves nothing until confirmed.
  await page.locator('#personalAssistantInput').fill('save this draft and remind me tomorrow');
  await page.locator('#personalAssistantSend').click();
  await page.locator('#personalAssistantDraftReview').waitFor({ state: 'visible', timeout: 30000 });
  const typed = await review();
  evidence.steps.push({
    step: 'typed-request',
    bodyIsLatestReply: typed.body === korean.text,
    notes: typed.notes,
    lastAssistantLine: (await rows()).filter(row => row.role === 'assistant').pop()?.text,
    shot: await shot('12-typed-request-review')
  });
  await page.locator('#personalAssistantDraftCancel').click();

  await page.setViewportSize({ width: 390, height: 844 });
  await koreanRow.locator('[data-message-action="save-draft"]').click();
  await page.locator('#personalAssistantDraftReview').waitFor({ state: 'visible', timeout: 20000 });
  evidence.steps.push({ step: 'narrow-review', shot: await shot('13-narrow-review') });
  await page.locator('#personalAssistantDraftCancel').click();
  await page.setViewportSize({ width: 1440, height: 900 });

  evidence.canonical = {
    hq,
    draftTicketsAdded: (await assistantTickets(hq)).length - ticketsBefore,
    memoryAfter: (await api('/api/personal-assistant/knowledge')).status
  };
}

const resumeStateFile = join(out, 'resume-state.json');

const draftBar = () =>
  page.evaluate(() => ({
    visible: !document.getElementById('personalAssistantSavedDraft').hidden,
    text: document.getElementById('personalAssistantSavedDraftText').textContent,
    startOffered: !document.getElementById('personalAssistantSavedDraftStart').hidden,
    conversationId: window.PersonalAssistantConversation.currentId(),
    working: window.PersonalAssistantDrafts._state.working?.ticket_id || ''
  }));

const updateReview = () =>
  page.evaluate(() => ({
    heading: document.getElementById('personalAssistantDraftReviewHeading').textContent,
    target: document.getElementById('personalAssistantDraftTarget').textContent,
    currentShown: !document.getElementById('personalAssistantDraftCurrent').hidden,
    current: document.getElementById('personalAssistantDraftCurrentText').textContent,
    proposed: document.getElementById('personalAssistantDraftBody').value,
    title: document.getElementById('personalAssistantDraftTitle').value,
    status: document.getElementById('personalAssistantDraftStatus').textContent,
    receipt: document.getElementById('personalAssistantDraftReceipt').textContent,
    version: window.PersonalAssistantDrafts._state.review?.current?.version || 0
  }));

const ticketOf = async (workspaceId, ticketId) =>
  (await api(`/api/workspaces/${workspaceId}/tickets/${ticketId}`)).body?.ticket ||
  (await api(`/api/workspaces/${workspaceId}/tickets/${ticketId}`)).body;

async function waitForReview() {
  await page.locator('#personalAssistantDraftReview').waitFor({ state: 'visible', timeout: 30000 });
}

/*
 * resume-save: draft and save one item, and write what was saved to
 * <outDir>/resume-state.json. Restart the server on the same sandbox, then run
 * the resume stage.
 */
async function resumeSaveStage() {
  await openAsk();
  await page
    .locator('#personalAssistantConversationNew')
    .click({ force: true })
    .catch(() => {});
  const hq = (await api('/api/personal-assistant')).body.personal_assistant.hq_workspace_id;
  const draft = await say(
    'Write a two-sentence thank-you note to my neighbour Jun for watering my plants.'
  );
  await page
    .locator(`#homeAssistantConversation [data-message-id="${draft.messageId}"]`)
    .locator('[data-message-action="save-draft"]')
    .click();
  await waitForReview();
  await page.locator('#personalAssistantDraftTitle').fill('Thank-you note for Jun');
  await page.locator('#personalAssistantDraftSave').click();
  await page
    .locator('#personalAssistantDraftReview [data-draft-view="receipt"]')
    .waitFor({ state: 'visible', timeout: 30000 });
  const ticket = (await assistantTickets(hq)).find(item =>
    item.source_id.includes(draft.messageId)
  );
  const saved = {
    hq,
    conversationId: draft.conversationId,
    messageId: draft.messageId,
    ticketId: ticket.id,
    number: ticket.display_number,
    version: ticket.version,
    body: ticket.description
  };
  writeFileSync(resumeStateFile, JSON.stringify(saved, null, 2));
  evidence.steps.push({ step: 'saved-before-restart', saved, bar: await draftBar() });
}

/*
 * resume: after a server restart on the same data, reopen the saved draft from
 * the conversation list and from its Ticket, update the same Ticket, hit a
 * conflict from an outside edit, and open it after its chat was deleted.
 */
async function resumeStage() {
  const saved = JSON.parse(readFileSync(resumeStateFile, 'utf8'));
  await openAsk();

  // A fresh browser has no thread: reopen it from the validated list.
  await page.locator('#personalAssistantConversationContinue').click();
  const entry = page.locator(
    `#personalAssistantConversationList [data-conversation-id="${saved.conversationId}"]`
  );
  await entry.waitFor({ state: 'visible', timeout: 20000 });
  await entry.click();
  await page.waitForFunction(
    id => window.PersonalAssistantDrafts._state.working?.ticket_id === id,
    saved.ticketId,
    { timeout: 20000 }
  );
  const reopened = await rows();
  const afterRestart = await ticketOf(saved.hq, saved.ticketId);
  evidence.steps.push({
    step: 'reopened-after-restart',
    messages: reopened.length,
    savedBadge: reopened.find(row => row.messageId === saved.messageId)?.saved,
    bar: await draftBar(),
    ticketUnchanged:
      afterRestart.version === saved.version && afterRestart.description === saved.body,
    shot: await shot('20-reopened')
  });

  // Revise, then review the update of the same Ticket.
  const shorter = await say('make it one sentence');
  evidence.steps.push({ step: 'revised', actions: shorter.actions, bar: await draftBar() });
  const replyRow = page.locator(
    `#homeAssistantConversation [data-message-id="${shorter.messageId}"]`
  );
  await replyRow.locator('[data-message-action="update-draft"]').click();
  await waitForReview();
  const review = await updateReview();
  evidence.steps.push({
    step: 'update-review',
    review,
    currentIsSavedText: review.current === saved.body,
    proposedIsReply: review.proposed === shorter.text,
    ticketUntouched: (await ticketOf(saved.hq, saved.ticketId)).version === saved.version,
    shot: await shot('21-update-review')
  });
  await page.locator('#personalAssistantDraftSave').click();
  await page
    .locator('#personalAssistantDraftReview [data-draft-view="receipt"]')
    .waitFor({ state: 'visible', timeout: 30000 });
  const updated = await ticketOf(saved.hq, saved.ticketId);
  evidence.steps.push({
    step: 'updated',
    receipt: (await updateReview()).receipt,
    sameTicket: updated.id === saved.ticketId && updated.display_number === saved.number,
    bodyIsReply: updated.description === shorter.text,
    version: `${saved.version} -> ${updated.version}`,
    state: updated.state,
    assignee: updated.assignee || '',
    draftTickets: (await assistantTickets(saved.hq)).filter(item => item.id === saved.ticketId)
      .length,
    shot: await shot('22-updated')
  });
  await page.locator('#personalAssistantDraftDone').click();

  // A conflicting edit from another surface: the review goes stale and
  // nothing is overwritten.
  const again = await say('add a plant emoji at the end');
  await page
    .locator(`#homeAssistantConversation [data-message-id="${again.messageId}"]`)
    .locator('[data-message-action="update-draft"]')
    .click();
  await waitForReview();
  const outside = 'Edited directly in Personal HQ while the review was open.';
  const patch = await page.evaluate(
    async ([url, body]) => {
      const response = await fetch(url, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      return response.status;
    },
    [
      `/api/workspaces/${saved.hq}/tickets/${saved.ticketId}`,
      { description: outside, version: updated.version }
    ]
  );
  await page.locator('#personalAssistantDraftSave').click();
  await page.waitForFunction(
    () =>
      document
        .getElementById('personalAssistantDraftStatus')
        .textContent.includes('Nothing was overwritten'),
    null,
    { timeout: 30000 }
  );
  const stale = await updateReview();
  const afterConflict = await ticketOf(saved.hq, saved.ticketId);
  evidence.steps.push({
    step: 'conflict',
    outsideEditStatus: patch,
    status: stale.status,
    currentNowShowsOutsideEdit: stale.current === outside,
    proposalKept: stale.proposed === again.text,
    ticketKeptOutsideEdit: afterConflict.description === outside,
    sameTicket: afterConflict.id === saved.ticketId,
    shot: await shot('23-conflict')
  });
  await page.locator('#personalAssistantDraftCancel').click();

  // Open the saved item from its Ticket link.
  await page.goto(`${baseUrl}/?assistant_draft=${saved.ticketId}`, {
    waitUntil: 'domcontentloaded'
  });
  await page.waitForFunction(
    id => window.PersonalAssistantDrafts?._state.working?.ticket_id === id,
    saved.ticketId,
    { timeout: 30000 }
  );
  evidence.steps.push({
    step: 'opened-from-ticket',
    url: page.url(),
    bar: await draftBar(),
    panelOpen: await page.locator('#personalAssistantPanel').isVisible(),
    shot: await shot('24-from-ticket')
  });

  // Delete the chat with the existing session control, then open the Ticket again.
  const deleted = await page.evaluate(async id => {
    const response = await fetch(`/api/sessions/${id}`, { method: 'DELETE' });
    return response.status;
  }, saved.conversationId);
  await page.goto(`${baseUrl}/?assistant_draft=${saved.ticketId}`, {
    waitUntil: 'domcontentloaded'
  });
  await page
    .locator('#personalAssistantSavedDraftStart')
    .waitFor({ state: 'visible', timeout: 30000 });
  const gone = await draftBar();
  evidence.steps.push({
    step: 'chat-deleted',
    deleteStatus: deleted,
    bar: gone,
    ticketStillThere: (await ticketOf(saved.hq, saved.ticketId)).id === saved.ticketId,
    shot: await shot('25-chat-deleted')
  });
  await page.locator('#personalAssistantSavedDraftStart').click();
  const fresh = await say('rewrite my saved draft so it rhymes');
  const sessions = (await api('/api/sessions?limit=50&sort=updated_desc')).body?.sessions || [];
  evidence.steps.push({
    step: 'new-conversation-from-saved-draft',
    reply: fresh.text,
    newConversation: fresh.conversationId !== saved.conversationId,
    deletedChatRecreated: sessions.some(item => item.id === saved.conversationId),
    bar: await draftBar(),
    ticketUntouched: (await ticketOf(saved.hq, saved.ticketId)).description === outside,
    shot: await shot('26-new-from-saved')
  });

  // A review belongs to its conversation: starting a new one closes it and
  // stops working on the saved draft.
  await page
    .locator(`#homeAssistantConversation [data-message-id="${fresh.messageId}"]`)
    .locator('[data-message-action="save-draft"]')
    .click();
  await waitForReview();
  await page.locator('#personalAssistantConversationNew').click();
  evidence.steps.push({
    step: 'new-conversation-closes-review',
    reviewOpen: await page.locator('#personalAssistantDraftReview').isVisible(),
    bar: await draftBar()
  });

  // The saved Ticket in Personal HQ links back to the assistant.
  const hqSlug = (await ticketOf(saved.hq, saved.ticketId)).owning_workspace_slug;
  await page.goto(`${baseUrl}/workspaces/${hqSlug}?ticket=${saved.ticketId}`, {
    waitUntil: 'domcontentloaded'
  });
  const link = page.locator('.ticket-detail-assistant-draft');
  await link.waitFor({ state: 'visible', timeout: 30000 });
  evidence.steps.push({
    step: 'ticket-links-back',
    text: await link.textContent(),
    href: await link.getAttribute('href'),
    shot: await shot('27-ticket-link')
  });
}

try {
  if (stage === 'conversation') await conversationStage();
  else if (stage === 'save') await saveStage();
  else if (stage === 'resume-save') await resumeSaveStage();
  else if (stage === 'resume') await resumeStage();
  else throw new Error(`unknown stage: ${stage}`);
} catch (error) {
  evidence.error = String(error && error.stack ? error.stack : error);
  evidence.failureShot = await shot('failure').catch(() => '');
} finally {
  await browser.close();
}

evidence.problems = [...new Set(evidence.problems)];
console.log(JSON.stringify(evidence, null, 2));
process.exit(evidence.error ? 1 : 0);
