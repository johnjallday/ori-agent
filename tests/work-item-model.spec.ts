import { test, expect, type APIRequestContext } from '@playwright/test';
import { mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import { join, sep } from 'node:path';
import { tmpdir } from 'node:os';

// One work-item model across surfaces (PRD 760), against the sandbox that
// scripts/second-brain-demo.sh prints:
//
//   ./scripts/second-brain-demo.sh 8947
//   PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_VAULT="<VAULT from READY>" \
//     npx playwright test tests/work-item-model.spec.ts --project=chromium --workers=1
//
// No model is called: each start is checked up to the point the run is asked for.

const vault = process.env.SECOND_BRAIN_VAULT || '';

test.describe.configure({ mode: 'serial' });
test.skip(
  !vault,
  'Run against scripts/second-brain-demo.sh: set PLAYWRIGHT_BASE_URL and SECOND_BRAIN_VAULT from its READY line'
);

const BRAIN = '/api/personal-assistant/second-brain';
const stamp = Date.now();

async function json(
  request: APIRequestContext,
  method: 'get' | 'post',
  url: string,
  data?: object
) {
  const response = await request[method](url, data ? { data } : undefined);
  const body = await response.text();
  expect(response.ok(), `${method} ${url}: ${body}`).toBeTruthy();
  return JSON.parse(body);
}

async function workspaceWithAgent(
  request: APIRequestContext,
  name: string,
  { anchor = '', agent = '' }: { anchor?: string; agent?: string } = {}
) {
  const workspace = await json(request, 'post', '/api/workspaces', {
    name,
    ...(anchor ? { anchor_path: anchor } : {})
  });
  const id = workspace.id || workspace.folder?.id;
  expect(id, JSON.stringify(workspace)).toBeTruthy();
  if (agent) {
    await request.post('/api/agents', { data: { name: agent, type: 'orchestration' } });
    await json(request, 'post', `/api/workspaces/${id}/agents`, { agent_name: agent });
  }
  return json(request, 'get', `/api/workspaces/${id}`);
}

test('a follow-up becomes one ticket, each side shows the other, and closing one leaves the other', async ({
  page,
  request
}) => {
  const owner = await workspaceWithAgent(request, `Bridge ${stamp}`);
  const title = `Send the contract ${stamp}`;
  const made = await json(request, 'post', '/api/personal-hq/followups', {
    category: 'i_owe',
    direction: 'outbound',
    title
  });
  const followUpId = made.followup.id;

  await page.goto(`/workspaces/personal-hq?follow_up=${followUpId}`);
  const card = page.locator(`[data-followup-id="${followUpId}"]`);
  await expect(card).toBeVisible();

  // HQ holds commitments, not workspace work, so the person picks.
  await card.getByRole('button', { name: 'Make a ticket' }).click();
  const chooser = card.locator('.hq-followup-chooser');
  await expect(chooser).toBeVisible();
  await chooser.getByRole('combobox').selectOption(owner.id);
  await chooser.getByRole('button', { name: 'Make the ticket' }).click();

  const ticketLink = page.locator(`[data-followup-id="${followUpId}"] .hq-followup-ticket a`);
  await expect(ticketLink).toContainText('Backlog');
  await expect(ticketLink).toBeFocused();

  // Doing it again finds the same ticket.
  const repeat = await json(request, 'post', '/api/personal-hq/followups/ticket', {
    id: followUpId,
    workspace_id: owner.id
  });
  expect(repeat.created).toBe(false);
  const listed = await json(request, 'get', `/api/workspaces/${owner.id}/tickets`);
  expect(
    listed.tickets.filter((t: { source_id: string }) => t.source_id === followUpId)
  ).toHaveLength(1);

  // The ticket names the follow-up.
  await ticketLink.click();
  const origin = page.locator('.ticket-detail-followup');
  await expect(origin).toContainText(title);
  await expect(origin).toContainText('(open)');
  await expect(origin.getByRole('link')).toHaveAttribute('href', /follow_up=/);

  // Closing the follow-up leaves the ticket; the ticket says so.
  await json(request, 'post', '/api/personal-hq/followups/complete', { id: followUpId });
  const ticketId = repeat.ticket.id;
  const stillOpen = await json(request, 'get', `/api/workspaces/${owner.id}/tickets/${ticketId}`);
  expect(stillOpen.state).toBe('backlog');
  await page.reload();
  await expect(page.locator('.ticket-detail-followup')).toContainText('(done)');
});

test('Start this asks who when no one is set, then moves the ticket and starts a run', async ({
  page,
  request
}) => {
  const agent = `Starter ${stamp}`;
  const owner = await workspaceWithAgent(request, `Start ${stamp}`, { agent });
  const ticket = await json(request, 'post', `/api/workspaces/${owner.id}/tickets`, {
    state: 'backlog',
    title: 'Start me'
  });

  await page.goto(`/workspaces/${owner.folder_slug}?ticket=${ticket.id}`);
  const start = page.locator('[data-start-this]');
  await expect(start).toHaveText('Start this');
  await start.click();

  const who = page.locator('.ticket-start-chooser');
  await expect(who.getByLabel('Who should do this?')).toBeFocused();
  // Cancelling asks for nothing and changes nothing.
  await who.getByRole('button', { name: 'Cancel' }).click();
  await expect(start).toBeFocused();
  const untouched = await json(request, 'get', `/api/workspaces/${owner.id}/tickets/${ticket.id}`);
  expect(untouched.state).toBe('backlog');

  await start.click();
  await who.getByLabel('Who should do this?').selectOption(agent);
  const asked = page.waitForResponse(response => response.url().endsWith('/start'));
  await who.locator('[data-start-confirm]').click();
  const body = await (await asked).json();
  // With no model the start may be refused; the ticket is Ready either way.
  expect(['ready', 'in_progress', 'review']).toContain(body.state);
  expect(body.assignee).toBe(agent);
  expect(
    body.state_history.some(
      (c: { from: string; to: string }) => c.from === 'backlog' && c.to === 'ready'
    )
  ).toBe(true);
});

test('an open line in a project goes to its workspace, and every other line stays a follow-up', async ({
  page,
  request
}) => {
  expect(realpathSync(vault).startsWith(realpathSync(tmpdir()) + sep)).toBe(true);
  const project = join(realpathSync(vault), 'Projects', `Work Item ${stamp}`);
  mkdirSync(project, { recursive: true });
  const line = `Mix the single ${stamp}`;
  const note = join(project, 'plan.md');
  writeFileSync(note, `# Plan\n\n- [ ] ${line} 📅 2030-01-15\n`);
  const owner = await workspaceWithAgent(request, `Work Item ${stamp}`, { anchor: project });

  const current = await json(request, 'get', BRAIN);
  if (current.second_brain.status !== 'not_connected') {
    await json(request, 'post', `${BRAIN}/disconnect`, {
      request_id: `work-item-disconnect-${stamp}`,
      if_version: current.second_brain.version
    });
  }
  await json(request, 'post', '/api/progression/reset', {});
  await expect
    .poll(async () => (await json(request, 'get', BRAIN)).second_brain.ask.state)
    .toBe('pending');

  await page.goto('/');
  await page.waitForFunction(() =>
    Boolean((window as any).PersonalAssistantPanel?._state?.personalAssistant)
  );
  if (!(await page.evaluate(() => (window as any).PersonalAssistantPanel?.isOpen?.()))) {
    await page.locator('#personalAssistantLauncher').click();
  }
  const lookCard = page.locator('#personalAssistantSecondBrain');
  const lookAction = (id: string) => lookCard.locator(`[data-second-brain-action="${id}"]`);
  await lookAction('read').click();
  await lookAction('connect').click();
  await expect(lookAction('add')).toContainText(/ticket/, { timeout: 75_000 });

  const look = (await json(request, 'get', `${BRAIN}/look`)).look;
  const item = look.result.items.find((entry: { text: string }) => entry.text === line);
  expect(item.ticket_workspace).toBe(owner.name);
  const other = look.result.items.find(
    (entry: { ticket_workspace?: string }) => !entry.ticket_workspace
  );
  expect(other).toBeTruthy();

  // Looking writes nothing.
  let tickets = await json(request, 'get', `/api/workspaces/${owner.id}/tickets`);
  expect(tickets.tickets).toHaveLength(0);

  // The card names the destination of each group before anything is accepted.
  await lookCard.getByText(/^Show everything/).click();
  await expect(
    lookCard.locator('.pa-second-brain__destination', { hasText: `Tickets in ${owner.name}` })
  ).toBeVisible();
  await expect(
    lookCard
      .locator('.pa-second-brain__destination', { hasText: 'Follow-ups in Personal HQ' })
      .first()
  ).toBeVisible();

  const accepted = await json(request, 'post', `${BRAIN}/look/${look.look_id}/follow-ups`, {
    items: [item.id, other.id],
    request_id: `work-item-accept-${stamp}`
  });
  expect(accepted.follow_ups.tickets[owner.name]).toBe(1);

  tickets = await json(request, 'get', `/api/workspaces/${owner.id}/tickets`);
  expect(tickets.tickets).toHaveLength(1);
  expect(tickets.tickets[0]).toMatchObject({
    state: 'backlog',
    source: 'note_item',
    source_id: item.id
  });

  // Accepting again makes nothing new.
  const again = await json(request, 'post', `${BRAIN}/look/${look.look_id}/follow-ups`, {
    items: [item.id],
    request_id: `work-item-accept-again-${stamp}`
  });
  expect(again.follow_ups.tickets?.[owner.name] ?? 0).toBe(0);
  tickets = await json(request, 'get', `/api/workspaces/${owner.id}/tickets`);
  expect(tickets.tickets).toHaveLength(1);
});
