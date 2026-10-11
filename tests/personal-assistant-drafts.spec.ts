import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

// The hired assistant's conversation: draft, save to the HQ backlog, resume and
// update, and Remember….
//
// Runs against the isolated real server started by `scripts/e2e-fresh.sh` (or
// `wt demo`). The file has two groups, and they prove different things:
//
//   Real persistence   No mocks. Conversations are real canonical Sessions in
//                      Personal HQ, seeded through the Session routes exactly as
//                      an answered turn stores them, so no model is needed.
//                      Tickets and remembered facts are the real ones.
//   Mocked replies     Only the model's reply is mocked (`/api/home-assistant/ask`
//                      and, for one case, the memory save). These check what the
//                      panel does with a reply; they prove nothing about storage.
//
// All names and dates are fictional.
test.describe.configure({ mode: 'serial' });

const ENGLISH = 'Happy birthday, Mina! Wishing you a wonderful day.';
const WARMER = 'Happy birthday, dear Mina! I hope today is full of everything you love.';
const KOREAN = '미나야, 생일 정말 축하해! 🎂\n오늘 하루가 기쁨으로 가득하길 바라.';
const SHORTER = '미나야, 생일 축하해! 🎂';
const FACT = "Mina's birthday is 3 March";

type Assistant = {
  state: string;
  state_version: number;
  hq_workspace_id: string;
  global_agent_profile_name: string;
  availability?: { model?: { available?: boolean } };
};

async function readAssistant(request: APIRequestContext): Promise<Assistant> {
  const response = await request.get('/api/personal-assistant');
  expect(response.status(), await response.text()).toBe(200);
  return (await response.json()).personal_assistant;
}

// A hired assistant with a Personal HQ, made through the real routes. Safe to
// run against a server that already has one.
async function ensureAssistant(request: APIRequestContext): Promise<Assistant> {
  await request.post('/api/onboarding/skip');
  let assistant = await readAssistant(request);
  if (assistant.state === 'active' || assistant.state === 'paused') return assistant;
  const root = await request.post('/api/settings/workspace-root', {
    data: { workspace_root: '' }
  });
  expect(root.status(), await root.text()).toBe(200);
  if (assistant.state !== 'needs_hq') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'drafts-spec-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    assistant = (await hire.json()).personal_assistant;
  }
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'drafts-spec-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  assistant = await readAssistant(request);
  expect(assistant.state).toBe('active');
  return assistant;
}

type Turn = ['user' | 'assistant', string];

async function addMessages(
  request: APIRequestContext,
  conversationId: string,
  turns: Turn[]
): Promise<string[]> {
  const ids: string[] = [];
  for (const [role, content] of turns) {
    const added = await request.post(`/api/sessions/${conversationId}/messages`, {
      data: { role, content }
    });
    expect(added.status(), await added.text()).toBe(201);
    ids.push((await added.json()).message.id);
  }
  return ids;
}

// A conversation as an answered turn stores it: a canonical Session in Personal
// HQ, bound to the hired assistant's profile.
async function seedConversation(
  request: APIRequestContext,
  assistant: Assistant,
  title: string,
  turns: Turn[]
): Promise<{ id: string; messageIds: string[] }> {
  const created = await request.post('/api/sessions', {
    data: {
      title,
      agent_name: assistant.global_agent_profile_name,
      folder_id: assistant.hq_workspace_id
    }
  });
  expect(created.status(), await created.text()).toBe(201);
  const id = (await created.json()).session.id;
  return { id, messageIds: await addMessages(request, id, turns) };
}

async function openAsk(page: Page): Promise<void> {
  await page.goto('/');
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher).toBeVisible({ timeout: 20000 });
  if (await page.locator('#personalAssistantPanel').isHidden()) await launcher.click();
  await expect(page.locator('#personalAssistantConversationBar')).toBeVisible();
}

const messageRows = (page: Page) => page.locator('#homeAssistantConversation [data-message-id]');
const messageRow = (page: Page, id: string) =>
  page.locator(`#homeAssistantConversation [data-message-id="${id}"]`);

async function continueConversation(page: Page, id: string, messages: number): Promise<void> {
  await page.locator('#personalAssistantConversationContinue').click();
  await page.locator(`#personalAssistantConversationList [data-conversation-id="${id}"]`).click();
  await expect(messageRows(page)).toHaveCount(messages);
}

async function draftTickets(request: APIRequestContext, hq: string) {
  const response = await request.get(
    `/api/workspaces/${hq}/tickets?source=assistant&archive=all&limit=200`
  );
  expect(response.status()).toBe(200);
  const tickets = (await response.json()).tickets || [];
  return tickets.filter((ticket: { source_id?: string }) =>
    String(ticket.source_id || '').startsWith('assistant-draft:')
  );
}

async function readTicket(request: APIRequestContext, hq: string, id: string) {
  const response = await request.get(`/api/workspaces/${hq}/tickets/${id}`);
  expect(response.status(), await response.text()).toBe(200);
  const body = await response.json();
  return body.ticket || body;
}

async function approvedFacts(request: APIRequestContext) {
  const response = await request.get('/api/personal-assistant/knowledge');
  expect(response.status(), await response.text()).toBe(200);
  const items = (await response.json()).items || [];
  return items.filter((item: { state: string }) => item.state === 'approved');
}

test.describe('Assistant drafts — real persistence', () => {
  let assistant: Assistant;
  let conversation: { id: string; messageIds: string[] };
  let ticketId = '';
  // The Ticket's display number as the UI shows it, e.g. "#1".
  let ticketNumber = '';

  test.beforeAll(async ({ request }) => {
    assistant = await ensureAssistant(request);
    conversation = await seedConversation(request, assistant, 'Birthday greeting for Mina', [
      ['user', 'Write a short birthday greeting for my friend Mina.'],
      ['assistant', ENGLISH],
      ['user', 'make it warmer'],
      ['assistant', WARMER],
      ['user', 'give it to me in Korean'],
      ['assistant', KOREAN]
    ]);
  });

  test('A8: with no model, a request is refused honestly and the text is kept', async ({
    page,
    request
  }) => {
    test.skip(
      assistant.availability?.model?.available === true,
      'this server has a model configured'
    );
    const listed = async () =>
      ((await (await request.get('/api/home-assistant/conversations')).json()).conversations || [])
        .length;
    const before = await listed();
    await openAsk(page);
    const typed = 'Write a two-line thank-you note for my neighbour Jun.';
    await page.locator('#personalAssistantInput').fill(typed);
    await page.locator('#personalAssistantSend').click();
    // The text comes back to the composer instead of being lost.
    await expect(page.locator('#personalAssistantInput')).toHaveValue(typed, { timeout: 30000 });
    await expect(messageRows(page)).toHaveCount(0);
    expect(await listed()).toBe(before);
  });

  test('A1/A4: a stored conversation reopens with every turn and the right actions', async ({
    page
  }) => {
    await openAsk(page);
    await continueConversation(page, conversation.id, 6);
    await expect(page.locator('#personalAssistantConversationTitle')).toHaveText(
      'Birthday greeting for Mina'
    );
    // The Korean reply is shown exactly, line break and emoji included.
    const korean = messageRow(page, conversation.messageIds[5]);
    await expect(korean).toContainText('미나야, 생일 정말 축하해! 🎂');
    await expect(korean).toContainText('오늘 하루가 기쁨으로 가득하길 바라.');
    // Every stored message offers Remember…; only replies can be saved as drafts.
    await expect(
      page.locator('#homeAssistantConversation [data-message-action="remember"]')
    ).toHaveCount(6);
    await expect(
      page.locator('#homeAssistantConversation [data-message-action="save-draft"]')
    ).toHaveCount(3);
    await expect(
      messageRow(page, conversation.messageIds[0]).locator('[data-message-action="save-draft"]')
    ).toHaveCount(0);

    await expect(korean.locator('[data-message-action="save-draft"]')).toBeHidden();
    await expect(korean.locator('summary')).toHaveText('Message actions');
    await expect(page.locator('#homeAssistantThinkingModalLabel')).toBeHidden();
    await expect(page.locator('.home-assistant-conversation-section-header')).toBeHidden();
    await expect(page.locator('#personalAssistantConversationNote')).toBeEmpty();
    expect(
      await page
        .locator('#homeAssistantConversation')
        .evaluate(el => getComputedStyle(el).maxHeight)
    ).toBe('none');
    // Reload: the same tab keeps the same conversation.
    await page.reload();
    await expect(messageRows(page)).toHaveCount(6);
  });

  test('A2/A3: the chosen version is saved once, exactly, as an unassigned Backlog item', async ({
    page,
    request
  }) => {
    const hq = assistant.hq_workspace_id;
    const before = (await draftTickets(request, hq)).length;
    await openAsk(page);
    await continueConversation(page, conversation.id, 6);
    const korean = messageRow(page, conversation.messageIds[5]);

    // Opening the menu writes nothing; Escape closes it before the drawer.
    const menu = korean.locator('summary');
    await menu.press('Enter');
    expect((await draftTickets(request, hq)).length).toBe(before);
    // Escape has menu precedence even after focus leaves it via keyboard.
    await page.locator('#personalAssistantInput').focus();
    await page.keyboard.press('Escape');
    await expect(menu).toBeFocused();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(korean.locator('[data-message-action="save-draft"]')).toBeHidden();
    // Opening the review writes nothing, and cancelling it writes nothing.
    await menu.click();
    await korean.locator('[data-message-action="save-draft"]').click();
    const review = page.locator('#personalAssistantDraftReview');
    await expect(review).toBeVisible();
    await expect(page.locator('#personalAssistantDraftBody')).toHaveValue(KOREAN);
    await expect(page.locator('#personalAssistantDraftTarget')).toContainText('My HQ');
    await page.keyboard.press('Escape');
    await expect(review).toBeHidden();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    expect((await draftTickets(request, hq)).length).toBe(before);

    await expect(menu).toBeFocused();
    // Save it. A double click is one save.
    await menu.click();
    await korean.locator('[data-message-action="save-draft"]').click();
    await expect(review).toBeVisible();
    await page.locator('#personalAssistantDraftTitle').fill('Birthday greeting for Mina (Korean)');
    const payload = await page.evaluate(() => {
      const state = (window as any).PersonalAssistantDrafts._state.review;
      return {
        operation_id: state.operation_id,
        target_workspace_id: state.target.workspace_id,
        source: state.source
      };
    });
    await page.locator('#personalAssistantDraftSave').dblclick();
    await expect(review.locator('[data-draft-view="receipt"]')).toBeVisible({ timeout: 30000 });

    const saved = await draftTickets(request, hq);
    expect(saved.length).toBe(before + 1);
    const ticket = saved.find((item: { source_id: string }) =>
      item.source_id.includes(conversation.messageIds[5])
    );
    expect(ticket).toBeTruthy();
    expect(ticket.title).toBe('Birthday greeting for Mina (Korean)');
    expect(ticket.description).toBe(KOREAN);
    expect(ticket.state).toBe('backlog');
    expect(ticket.assignee || '').toBe('');
    expect(Boolean(ticket.schedule_enabled || ticket.due_date)).toBe(false);
    ticketId = ticket.id;
    ticketNumber = ticket.display_number;
    expect(ticketNumber).toMatch(/^#\d+$/);
    await expect(page.locator('#personalAssistantDraftReceipt')).toContainText(ticketNumber);
    await expect(korean.locator('.personal-assistant-message__saved')).toContainText(ticketNumber);

    // The same save again — a retry after a lost response — is the same Ticket.
    const same = {
      ...payload,
      title: 'Birthday greeting for Mina (Korean)',
      body: KOREAN
    };
    const retry = await request.post('/api/home-assistant/drafts/save', { data: same });
    expect(retry.status(), await retry.text()).toBe(200);
    const replay = (await retry.json()).receipt;
    expect(replay.created).toBe(false);
    expect(replay.ticket_id).toBe(ticketId);

    // The same approval with different text is refused, and nothing is written.
    const changed = await request.post('/api/home-assistant/drafts/save', {
      data: { ...same, body: `${KOREAN} (changed)` }
    });
    expect(changed.status()).toBe(409);
    expect((await draftTickets(request, hq)).length).toBe(before + 1);
    expect((await readTicket(request, hq, ticketId)).description).toBe(KOREAN);
  });

  test('A4: the saved draft is resumed and updated; an outside edit is not overwritten', async ({
    page,
    request
  }) => {
    const hq = assistant.hq_workspace_id;
    const [, shorterId] = await addMessages(request, conversation.id, [
      ['user', 'make it one line'],
      ['assistant', SHORTER]
    ]);
    const before = await readTicket(request, hq, ticketId);

    // A fresh page: the conversation shows what was saved from it.
    await openAsk(page);
    await continueConversation(page, conversation.id, 8);
    await expect(
      messageRow(page, conversation.messageIds[5]).locator('.personal-assistant-message__saved')
    ).toContainText(ticketNumber);
    await expect(page.locator('#personalAssistantSavedDraft')).toBeVisible();
    // Reopening is a read.
    expect((await readTicket(request, hq, ticketId)).version).toBe(before.version);

    // The update review shows the saved text beside the proposal.
    const shorter = messageRow(page, shorterId);
    await shorter.locator('summary').click();
    await shorter.locator('[data-message-action="update-draft"]').click();
    const review = page.locator('#personalAssistantDraftReview');
    await expect(review).toBeVisible();
    await expect(page.locator('#personalAssistantDraftCurrentText')).toHaveText(KOREAN);
    await expect(page.locator('#personalAssistantDraftBody')).toHaveValue(SHORTER);
    expect((await readTicket(request, hq, ticketId)).version).toBe(before.version);

    // Someone edits the Ticket in Personal HQ while the review is open.
    const outside = 'Edited directly in Personal HQ while the review was open.';
    const patched = await request.patch(`/api/workspaces/${hq}/tickets/${ticketId}`, {
      data: { description: outside, version: before.version }
    });
    expect(patched.status(), await patched.text()).toBe(200);

    await page.locator('#personalAssistantDraftSave').click();
    await expect(page.locator('#personalAssistantDraftStatus')).toContainText(
      'Nothing was overwritten',
      { timeout: 30000 }
    );
    // The outside edit is what is saved; the proposal is still in the form.
    expect((await readTicket(request, hq, ticketId)).description).toBe(outside);
    await expect(page.locator('#personalAssistantDraftCurrentText')).toHaveText(outside);
    await expect(page.locator('#personalAssistantDraftBody')).toHaveValue(SHORTER);

    // A second outside edit, through the old task route, which rewrites the
    // title without moving the Ticket's version. The review is stale again: it
    // is checked against the content that was reviewed, not only the version.
    const beforeLegacy = await readTicket(request, hq, ticketId);
    const renamed = 'Renamed in the old task panel';
    const legacy = await request.put(`/api/orchestration/tasks/${ticketId}`, {
      data: { description: renamed }
    });
    expect(legacy.status(), await legacy.text()).toBe(200);
    const afterLegacy = await readTicket(request, hq, ticketId);
    expect(afterLegacy.title).toBe(renamed);
    expect(afterLegacy.version).toBe(beforeLegacy.version);

    await page.locator('#personalAssistantDraftSave').click();
    await expect(page.locator('#personalAssistantDraftCurrentLabel')).toContainText(renamed, {
      timeout: 30000
    });
    const kept = await readTicket(request, hq, ticketId);
    expect(kept.title).toBe(renamed);
    expect(kept.description).toBe(outside);
    await expect(page.locator('#personalAssistantDraftBody')).toHaveValue(SHORTER);

    // Reviewed against what is saved now, the update applies to the same Ticket.
    await page.locator('#personalAssistantDraftSave').click();
    await expect(review.locator('[data-draft-view="receipt"]')).toBeVisible({ timeout: 30000 });
    const after = await readTicket(request, hq, ticketId);
    expect(after.id).toBe(ticketId);
    expect(after.display_number).toBe(ticketNumber);
    expect(after.description).toBe(SHORTER);
    expect(after.title).toBe('Birthday greeting for Mina (Korean)');
    expect(after.state).toBe('backlog');
    expect(
      (await draftTickets(request, hq)).filter((item: { id: string }) => item.id === ticketId)
        .length
    ).toBe(1);
  });

  test('A5/A6: Remember… is a separate reviewed save that guesses nothing', async ({
    page,
    request
  }) => {
    const hq = assistant.hq_workspace_id;
    const factsBefore = (await approvedFacts(request)).length;
    const ticketsBefore = (await draftTickets(request, hq)).length;
    await openAsk(page);
    await continueConversation(page, conversation.id, 8);

    // On a reply the assistant wrote: the review starts empty and saves nothing.
    const greeting = messageRow(page, conversation.messageIds[1]);
    const action = greeting.locator('[data-message-action="remember"]');
    const menu = greeting.locator('summary');
    await menu.click();
    await action.click();
    const review = page.locator('#personalAssistantMemoryReview');
    const field = page.locator('#personalAssistantMemoryText');
    await expect(review).toBeVisible();
    await expect(field).toHaveValue('');
    await expect(field).toBeFocused();
    await expect(page.locator('#personalAssistantMemorySave')).toBeDisabled();
    await expect(page.locator('#personalAssistantMemoryHint')).toContainText(
      'does not say the date'
    );
    await expect(page.locator('#personalAssistantDraftReview')).toBeHidden();
    expect((await approvedFacts(request)).length).toBe(factsBefore);

    // Escape cancels only the review, and focus returns to the action.
    await page.keyboard.press('Escape');
    await expect(review).toBeHidden();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(menu).toBeFocused();
    await expect(action).toBeHidden();

    // Over the limit: refused in place, never cut.
    await menu.click();
    await action.click();
    await field.fill('가'.repeat(167));
    await expect(page.locator('#personalAssistantMemoryLimit')).toHaveText('501 / 500 UTF-8 bytes');
    await expect(page.locator('#personalAssistantMemorySave')).toBeDisabled();
    await expect(field).toHaveValue('가'.repeat(167));

    // The user's own wording, saved from the keyboard.
    await field.fill(FACT);
    await field.press('Enter');
    await expect(review.locator('[data-memory-view="receipt"]')).toBeVisible({ timeout: 30000 });
    await expect(page.locator('#personalAssistantMemoryReceipt')).toContainText(FACT);
    const facts = await approvedFacts(request);
    expect(facts.length).toBe(factsBefore + 1);
    const remembered = facts.find((item: { text: string }) => item.text === FACT);
    expect(remembered.category).toBe('people');
    expect(remembered.source_kind).toBe('explicit');
    // A fact is not a draft and not a turn.
    expect((await draftTickets(request, hq)).length).toBe(ticketsBefore);
    await expect(messageRows(page)).toHaveCount(8);
    await page.locator('#personalAssistantMemoryDone').click();

    // "remember this" names no fact: the review opens empty, with no model call
    // and nothing added to the conversation.
    await page.locator('#personalAssistantInput').fill('remember this');
    await page.locator('#personalAssistantSend').click();
    await expect(review).toBeVisible({ timeout: 30000 });
    await expect(field).toHaveValue('');
    await page.locator('#personalAssistantMemoryCancel').click();
    await expect(messageRows(page)).toHaveCount(8);

    // A clear statement reaches the same review as editable text — still unsaved.
    await page.locator('#personalAssistantInput').fill('remember that Mina likes jasmine tea');
    await page.locator('#personalAssistantSend').click();
    await expect(review).toBeVisible({ timeout: 30000 });
    await expect(field).toHaveValue('Mina likes jasmine tea');
    await page.locator('#personalAssistantMemoryCancel').click();
    expect((await approvedFacts(request)).length).toBe(factsBefore + 1);

    // Navigation intentionally preserves the drawer. Close it as a user would
    // before editing Profile, rather than click through the covering dialog.
    page.on('dialog', dialog => void dialog.accept());
    await page.goto('/profile#personalHQKnowledge');
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await page.locator('#personalAssistantClose').click();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    const card = page.locator('#personalHQKnowledge .reviewed-knowledge-item', { hasText: FACT });
    await expect(card).toBeVisible({ timeout: 30000 });
    await expect(card).toContainText('You told Ori');
    const edited = "Mina's birthday is 4 March";
    await card.getByRole('button', { name: 'Edit', exact: true }).click();
    await card.locator('.reviewed-knowledge-edit textarea').fill(edited);
    await card.locator('.reviewed-knowledge-edit button[type="submit"]').click();
    const editedCard = page.locator('#personalHQKnowledge .reviewed-knowledge-item', {
      hasText: edited
    });
    await expect(editedCard).toBeVisible({ timeout: 30000 });
    await editedCard.getByRole('button', { name: 'Forget', exact: true }).click();
    await expect(editedCard).toHaveCount(0, { timeout: 30000 });
    expect((await approvedFacts(request)).length).toBe(factsBefore);
  });

  test('A8: a change to the assistant while Remember… is open is noticed before saving', async ({
    page,
    request
  }) => {
    const before = (await approvedFacts(request)).length;
    await openAsk(page);
    await continueConversation(page, conversation.id, 8);
    await messageRow(page, conversation.messageIds[1]).locator('summary').click();
    await messageRow(page, conversation.messageIds[1])
      .locator('[data-message-action="remember"]')
      .click();
    const field = page.locator('#personalAssistantMemoryText');
    const fact = 'Mina likes jasmine tea';
    await field.fill(fact);

    // The assistant is paused from somewhere else while the review is open.
    const opened = await readAssistant(request);
    const paused = await request.post('/api/personal-assistant/pause', {
      data: { if_version: opened.state_version }
    });
    expect(paused.status(), await paused.text()).toBe(200);
    try {
      await field.press('Enter');
      await expect(page.locator('#personalAssistantMemoryStatus')).toContainText(
        'setup changed while this was open',
        { timeout: 30000 }
      );
      await expect(field).toHaveValue(fact);
      expect((await approvedFacts(request)).length).toBe(before);

      // Saving again is the user's choice against the current state.
      await field.press('Enter');
      await expect(
        page.locator('#personalAssistantMemoryReview [data-memory-view="receipt"]')
      ).toBeVisible({ timeout: 30000 });
      const facts = await approvedFacts(request);
      expect(facts.length).toBe(before + 1);
      const item = facts.find((entry: { text: string }) => entry.text === fact);
      const forgotten = await request.post(`/api/personal-assistant/knowledge/${item.id}/forget`, {
        data: { version: item.version, request_id: 'drafts-spec-forget-tea' }
      });
      expect(forgotten.status(), await forgotten.text()).toBe(200);
    } finally {
      const now = await readAssistant(request);
      if (now.state === 'paused') {
        await request.post('/api/personal-assistant/resume', {
          data: { if_version: now.state_version }
        });
      }
    }
    expect((await readAssistant(request)).state).toBe('active');
  });

  test('A7: a session that is not the assistant’s conversation is refused', async ({ request }) => {
    // Same profile, but not in Personal HQ.
    const foreign = await request.post('/api/sessions', {
      data: { title: 'Elsewhere', agent_name: assistant.global_agent_profile_name }
    });
    expect(foreign.status(), await foreign.text()).toBe(201);
    const foreignId = (await foreign.json()).session.id;
    await addMessages(request, foreignId, [['assistant', 'Not an assistant conversation.']]);

    const listed = await (await request.get('/api/home-assistant/conversations')).json();
    expect((listed.conversations || []).map((item: { id: string }) => item.id)).not.toContain(
      foreignId
    );
    const read = await request.get(`/api/home-assistant/conversations/${foreignId}`);
    expect(read.status()).toBeGreaterThanOrEqual(400);

    // Nothing is opened, reviewed, or answered against it.
    const asked = await request.post('/api/home-assistant/ask', {
      data: {
        prompt: 'remember that Mina likes jasmine tea',
        intent: 'assistant_conversation',
        conversation: { id: foreignId }
      }
    });
    const body = await asked.json();
    expect(body.memory_review).toBeUndefined();
    expect(body.draft_review).toBeUndefined();
    expect(String(body.conversation?.error || '')).toMatch(/^conversation_/);

    const unknown = await request.post('/api/home-assistant/drafts/review', {
      data: { conversation_id: foreignId, message_id: 'nope' }
    });
    expect(unknown.status()).toBeGreaterThanOrEqual(400);
  });

  test('A4: after the chat is deleted the saved Ticket still opens and offers a new one', async ({
    page,
    request
  }) => {
    const hq = assistant.hq_workspace_id;
    const saved = await readTicket(request, hq, ticketId);
    const removed = await request.delete(`/api/sessions/${conversation.id}`);
    expect(removed.status()).toBeLessThan(300);

    // The Ticket's own link opens the panel by itself.
    await page.goto(`/?assistant_draft=${ticketId}`);
    await expect(page.locator('#personalAssistantSavedDraft')).toBeVisible({ timeout: 30000 });
    await expect(page.locator('#personalAssistantSavedDraftText')).toContainText('gone');
    await expect(page.locator('#personalAssistantSavedDraftStart')).toBeVisible();
    // The link's parameter is consumed, and nothing was recreated or changed.
    await expect(page).not.toHaveURL(/assistant_draft=/);
    const listed = await (await request.get('/api/home-assistant/conversations')).json();
    expect((listed.conversations || []).map((item: { id: string }) => item.id)).not.toContain(
      conversation.id
    );
    const after = await readTicket(request, hq, ticketId);
    expect(after.version).toBe(saved.version);
    expect(after.description).toBe(saved.description);
  });
});

test.describe('Assistant drafts — mocked replies (panel behavior only)', () => {
  test.beforeAll(async ({ request }) => {
    await ensureAssistant(request);
  });

  test('A1: three turns stay in one conversation and each stored row is tagged', async ({
    page,
    request
  }) => {
    // Only replies/row IDs are mocked here. Route independently validates
    // canonical ownership, so do not claim a nonexistent browser-only thread.
    const owned = await seedConversation(
      request,
      await readAssistant(request),
      'Birthday greeting for Mina',
      []
    );
    const replies = [ENGLISH, WARMER, KOREAN];
    const sent: Array<{ prompt: string; conversation?: { id?: string } }> = [];
    await page.route('**/api/home-assistant/ask', async route => {
      const body = route.request().postDataJSON();
      sent.push(body);
      const turn = sent.length;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: replies[turn - 1],
          intent: 'assistant_conversation',
          conversation: {
            id: owned.id,
            title: 'Birthday greeting for Mina',
            started: turn === 1,
            stored: true,
            user_message_id: `mock-user-${turn}`,
            assistant_message_id: `mock-assistant-${turn}`
          }
        })
      });
    });

    // A new page has no conversation yet, so there is none to leave.
    await openAsk(page);
    await expect(page.locator('#personalAssistantConversationNew')).toBeDisabled();
    const prompts = [
      'Write a short birthday greeting for my friend Mina.',
      'make it warmer',
      'give it to me in Korean'
    ];
    for (const [index, prompt] of prompts.entries()) {
      await page.locator('#personalAssistantInput').fill(prompt);
      await page.locator('#personalAssistantSend').click();
      await expect(messageRow(page, `mock-assistant-${index + 1}`)).toBeVisible({
        timeout: 30000
      });
      await expect(page.locator('#personalAssistantInput')).toHaveValue('');
    }
    expect(sent.map(body => body.prompt)).toEqual(prompts);
    // The first turn starts the conversation; the follow-ups name it.
    expect(sent[0].conversation?.id || '').toBe('');
    expect(sent[1].conversation?.id).toBe(owned.id);
    expect(sent[2].conversation?.id).toBe(owned.id);
    await expect(messageRows(page)).toHaveCount(6);
    await expect(messageRow(page, 'mock-assistant-3')).toContainText('미나야');
    const lastReply = messageRow(page, 'mock-assistant-3');
    await expect(lastReply.locator('[data-message-action="save-draft"]')).toBeHidden();
    await lastReply.locator('summary').press('Enter');
    await expect(lastReply.locator('[data-message-action="save-draft"]')).toBeVisible();
  });

  test('A8: a failed turn puts the typed text back', async ({ page }) => {
    await page.route('**/api/home-assistant/ask', route =>
      route.fulfill({ status: 500, contentType: 'application/json', body: '{}' })
    );
    await openAsk(page);
    const typed = 'Write a short birthday greeting for my friend Mina.';
    await page.locator('#personalAssistantInput').fill(typed);
    await page.locator('#personalAssistantSend').click();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(typed, { timeout: 30000 });
    await expect(messageRows(page)).toHaveCount(0);
  });

  test('A8: a lost memory save is reported as unconfirmed and retried under one key', async ({
    page,
    request
  }) => {
    const saves: Array<{ request_id: string; text: string }> = [];
    // The mocked list reports the assistant's real state version: the review is
    // bound to the state the panel loaded, and this test is not about a change.
    const stateVersion = (await readAssistant(request)).state_version;
    await page.route(/\/api\/personal-assistant\/knowledge$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ state_version: stateVersion, items: [] })
      })
    );
    await page.route('**/api/personal-assistant/knowledge/explicit', async route => {
      saves.push(route.request().postDataJSON());
      if (saves.length === 1) {
        await route.abort('connectionreset');
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          item: { id: 'mock-fact', state: 'approved', text: FACT, category: 'people' }
        })
      });
    });

    await openAsk(page);
    await page.evaluate(() => (window as any).PersonalAssistantMemory.open({ text: '' }));
    const field = page.locator('#personalAssistantMemoryText');
    await field.fill(FACT);
    await field.press('Enter');
    // Not "saved", not "failed": unconfirmed, with the wording kept.
    await expect(page.locator('#personalAssistantMemoryStatus')).toContainText(
      'Could not confirm whether this was remembered',
      { timeout: 30000 }
    );
    await expect(field).toHaveValue(FACT);
    await field.press('Enter');
    await expect(page.locator('#personalAssistantMemoryReceipt')).toContainText(FACT, {
      timeout: 30000
    });
    expect(saves).toHaveLength(2);
    expect(saves[1].request_id).toBe(saves[0].request_id);
    expect(saves[1].text).toBe(FACT);
  });
});
