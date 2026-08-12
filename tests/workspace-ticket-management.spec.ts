import { test, expect, type Page, type APIRequestContext } from '@playwright/test';

/**
 * End-to-end acceptance path for tasks/prd-workspace-ticket-management.md.
 *
 * Covers the required journey (FR-19, FR-22 to FR-33, FR-37, FR-69, FR-73 to
 * FR-79, FR-138, FR-140, FR-141): capture into Backlog, promote to Ready,
 * start, review, explicitly accept, reopen, link a Note — and verify that the
 * ticket's identity and history survive all of it.
 *
 * The lifecycle is driven through the canonical API and the UI is asserted
 * against it, because the agent-run half needs a configured LLM provider that
 * a test machine will not have. What is proven here is the contract every
 * surface depends on, plus that the destination renders it.
 *
 * Run against a server started by `wt demo 8931`:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test \
 *     tests/workspace-ticket-management.spec.ts
 */

async function createWorkspace(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post('/api/workspaces', { data: { name } });
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  return body.folder?.id || body.id;
}

async function createTicket(
  request: APIRequestContext,
  studioId: string,
  data: Record<string, unknown>
) {
  const res = await request.post(`/api/workspaces/${studioId}/tickets`, { data });
  expect(res.status(), await res.text()).toBe(201);
  return res.json();
}

async function transition(
  request: APIRequestContext,
  studioId: string,
  ticketId: string,
  to: string
) {
  return request.post(`/api/workspaces/${studioId}/tickets/${ticketId}/transition`, {
    data: { to }
  });
}

/** The workspace page auto-opens modals that swallow clicks; clear them. */
async function openTickets(page: Page, studioId: string) {
  await page.goto(`/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await page.evaluate(() => {
    document.querySelectorAll('.modal.show').forEach(modal => {
      modal.classList.remove('show');
      (modal as HTMLElement).style.display = 'none';
    });
    document.querySelectorAll('.modal-backdrop').forEach(el => el.remove());
    document.body.classList.remove('modal-open');
  });
  // Tickets is a view mode beside Details and Map, not a modal.
  await page.click('[data-cmd-view-mode="tickets"]');
  await page.waitForSelector('#hubTicketsList', { state: 'visible', timeout: 10000 });
  await page.waitForTimeout(600);
}

test.describe('Workspace Ticket Management', () => {
  test('capture to done to reopen keeps one identity and full history', async ({
    page,
    request
  }) => {
    const studioId = await createWorkspace(request, `Acceptance ${Date.now()}`);

    // FR-19: capture is an explicit choice.
    const ticket = await createTicket(request, studioId, {
      state: 'backlog',
      title: 'Investigate the cold cache',
      description: 'Fails on boot.'
    });
    expect(ticket.state).toBe('backlog');
    expect(ticket.number).toBeGreaterThan(0);
    const ticketId = ticket.id as string;
    const originalNumber = ticket.number as number;

    // FR-36: no shortcut past Ready.
    expect((await transition(request, studioId, ticketId, 'done')).status()).toBe(409);

    // FR-22: the commitment transition.
    expect((await transition(request, studioId, ticketId, 'ready')).status()).toBe(200);
    // FR-23: repeating it is idempotent, not a duplicate.
    expect((await transition(request, studioId, ticketId, 'ready')).status()).toBe(200);

    expect((await transition(request, studioId, ticketId, 'in_progress')).status()).toBe(200);
    // FR-29: work cannot go straight from In Progress to Done.
    expect((await transition(request, studioId, ticketId, 'done')).status()).toBe(409);
    expect((await transition(request, studioId, ticketId, 'review')).status()).toBe(200);

    // FR-30: acceptance is an explicit act, and it is allowed from Review.
    expect((await transition(request, studioId, ticketId, 'done')).status()).toBe(200);

    // FR-37: reopening lands in Ready, never back into execution.
    expect((await transition(request, studioId, ticketId, 'in_progress')).status()).toBe(409);
    expect((await transition(request, studioId, ticketId, 'ready')).status()).toBe(200);

    const finalRes = await request.get(`/api/workspaces/${studioId}/tickets/${ticketId}`);
    const final = await finalRes.json();

    // FR-3/FR-140: identity survived every state change.
    expect(final.id).toBe(ticketId);
    expect(final.number).toBe(originalNumber);
    expect(final.state).toBe('ready');
    // FR-40: one creation entry plus one per ACCEPTED transition — the
    // refused moves and the idempotent repeat add nothing.
    // backlog(create) → ready → in_progress → review → done → ready = 6.
    expect(final.state_history.length).toBe(6);
    expect(final.state_history[0].to).toBe('backlog');
    expect(final.state_history.at(-1).to).toBe('ready');

    // And the destination renders it.
    await openTickets(page, studioId);
    const card = page.locator(`#hubTicketsList .ticket-card[data-ticket-id="${ticketId}"]`);
    await expect(card).toBeVisible();
    await expect(card.locator('.ticket-card-number')).toHaveText(`#${originalNumber}`);
    await expect(card.locator('.ticket-state-chip')).toHaveText('Ready');
  });

  test('a note can seed a ticket without either becoming the other', async ({ page, request }) => {
    const studioId = await createWorkspace(request, `Notes ${Date.now()}`);

    const noteRes = await request.post(`/api/workspaces/${studioId}/notes`, {
      data: { name: 'Cache research', content: '# Findings\nCold on boot.' }
    });
    expect(noteRes.ok()).toBeTruthy();
    // The create route wraps its payload ({note: {...}}); the read route does
    // not. Handle both rather than assuming.
    const created = await noteRes.json();
    const noteId = (created.note?.id || created.id) as string;
    expect(noteId).toBeTruthy();

    // FR-73 to FR-75: reviewed values create the ticket and link it.
    const fromNote = await request.post(`/api/workspaces/${studioId}/notes/${noteId}/tickets`, {
      data: { state: 'backlog', title: 'Fix the cold cache', description: 'Reviewed' }
    });
    expect(fromNote.status()).toBe(201);
    const ticket = await fromNote.json();
    expect(ticket.source).toBe('note');
    expect(ticket.linked_note_ids).toContain(noteId);

    // FR-74: the note is untouched.
    const noteAfter = await (await request.get(`/api/notes/${noteId}`)).json();
    expect(noteAfter.name).toBe('Cache research');
    expect(noteAfter.content).toContain('Cold on boot.');

    // FR-76: editing the ticket never rewrites the note.
    await request.patch(`/api/workspaces/${studioId}/tickets/${ticket.id}`, {
      data: { title: 'Ticket went its own way' }
    });
    const noteStill = await (await request.get(`/api/notes/${noteId}`)).json();
    expect(noteStill.name).toBe('Cache research');

    // FR-78: the note page shows the ticket, read-only.
    await page.goto(`/workspaces/${studioId}/notes/${noteId}`, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(2500);
    const panel = page.locator('#notePageTickets');
    await expect(panel).toBeVisible();
    await expect(panel.locator('.ticket-state-chip')).toHaveText('Backlog');
    // A note gains no execution controls by being referenced.
    expect(await panel.locator('button').count()).toBe(0);

    // FR-72: deleting the ticket keeps the note.
    await request.delete(`/api/workspaces/${studioId}/tickets/${ticket.id}`);
    expect((await request.get(`/api/notes/${noteId}`)).ok()).toBeTruthy();
  });

  test('the board is fixed, server-authoritative, and keyboard-operable', async ({
    page,
    request
  }) => {
    const studioId = await createWorkspace(request, `Board ${Date.now()}`);
    await createTicket(request, studioId, { state: 'backlog', title: 'Captured' });
    await createTicket(request, studioId, { state: 'ready', title: 'Committed' });

    await openTickets(page, studioId);
    await page.click('#hubTicketsViewBoard');
    await page.waitForTimeout(700);

    // FR-43 to FR-45: five fixed columns, Cancelled excluded.
    const columns = page.locator('#hubTicketsBoard .ticket-board-column');
    await expect(columns).toHaveCount(5);
    await expect(columns.nth(0).locator('.ticket-board-column-title')).toHaveText('Backlog');
    await expect(columns.nth(4).locator('.ticket-board-column-title')).toHaveText('Done');

    // FR-52 to FR-55: no column editor on the Ticket Board.
    expect(await page.locator('#hubTicketsBoard [class*="add-column"]').count()).toBe(0);

    // FR-128: columns carry a programmatic name and count.
    await expect(columns.nth(1).locator('.ticket-board-column-body')).toHaveAttribute(
      'aria-label',
      /Ready, \d+ tickets/
    );

    // FR-36/FR-50: the keyboard menu offers only legal moves.
    const backlogMove = columns.nth(0).locator('.ticket-card-move-select').first();
    const options = await backlogMove.locator('option').allTextContents();
    expect(options).toContain('Promote to Ready');
    expect(options.join(' ')).not.toContain('In Progress');

    // FR-46 to FR-48: a keyboard move is a real transition.
    await backlogMove.selectOption('ready');
    await page.waitForTimeout(1200);
    await expect(columns.nth(0).locator('.ticket-board-column-count')).toHaveText('0');
    await expect(columns.nth(1).locator('.ticket-board-column-count')).toHaveText('2');

    // FR-129: the move is announced.
    await expect(page.locator('#hubTicketsLiveRegion')).toContainText('moved to Ready');
  });

  test('filters and search run on the server and say when nothing matched', async ({
    page,
    request
  }) => {
    const studioId = await createWorkspace(request, `Filters ${Date.now()}`);
    await createTicket(request, studioId, {
      state: 'backlog',
      title: 'Investigate flaky pipeline',
      priority: 1
    });
    await createTicket(request, studioId, { state: 'ready', title: 'Ship the cache fix' });

    await openTickets(page, studioId);
    await expect(page.locator('#hubTicketsList .ticket-card')).toHaveCount(2);

    await page.click('[data-ticket-state-filter="backlog"]');
    await page.waitForTimeout(900);
    await expect(page.locator('#hubTicketsList .ticket-card')).toHaveCount(1);
    await expect(page.locator('[data-ticket-state-filter="backlog"]')).toHaveAttribute(
      'aria-pressed',
      'true'
    );

    await page.click('[data-ticket-state-filter="all"]');
    await page.waitForTimeout(700);
    await page.fill('#hubTicketsSearch', 'flaky');
    await page.waitForTimeout(1000);
    await expect(page.locator('#hubTicketsList .ticket-card')).toHaveCount(1);

    // FR-133: "nothing matched" must not read as "nothing exists".
    await page.fill('#hubTicketsSearch', 'zzz-no-such-ticket');
    await page.waitForTimeout(1000);
    await expect(page.locator('#hubTicketsEmpty')).toContainText('No tickets match these filters');
  });

  test('a deep link opens the ticket it names', async ({ page, request }) => {
    const studioId = await createWorkspace(request, `DeepLink ${Date.now()}`);
    const ticket = await createTicket(request, studioId, {
      state: 'ready',
      title: 'Deep linked work'
    });

    await page.goto(`/workspaces/${studioId}?ticket=${ticket.id}`, {
      waitUntil: 'domcontentloaded'
    });
    await page.waitForTimeout(3500);

    await expect(page.locator('#hubTicketDetail')).toBeVisible();
    await expect(page.locator('#hubTicketDetailTitle')).toContainText('Deep linked work');
    await expect(page.locator('#hubTicketDetailTitle')).toContainText(`#${ticket.number}`);
  });
});
