import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

// Connect your second brain, in the browser, against the sandbox that
// scripts/second-brain-demo.sh prints:
//
//   ./scripts/second-brain-demo.sh 8947
//   PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_VAULT="<VAULT from READY>" \
//     npx playwright test tests/second-brain.spec.ts --project=chromium --workers=1
//
// The script hires the assistant, builds Personal HQ and writes a fixture vault
// with Obsidian's vault list under the sandbox HOME, so the ask names "Second
// Brain". Reset Getting Started re-arms the ask between cases; the journey runs
// last because it connects the vault. Re-running disconnects first.

const vault = process.env.SECOND_BRAIN_VAULT || '';
// The sandbox from the READY line, so a case can age the ask (second-brain-age.sh --ask).
const sandbox = process.env.SECOND_BRAIN_SANDBOX || '';

test.describe.configure({ mode: 'serial' });
test.skip(
  !vault,
  'Run against scripts/second-brain-demo.sh: set PLAYWRIGHT_BASE_URL and SECOND_BRAIN_VAULT from its READY line.'
);

const ENDPOINT = '/api/personal-assistant/second-brain';

type SecondBrain = {
  status: string;
  version: number;
  ask: { state: string };
  detected: { id: string; name: string }[];
};

async function secondBrain(request: APIRequestContext): Promise<SecondBrain> {
  const response = await request.get(ENDPOINT);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).second_brain;
}

async function resetGettingStarted(request: APIRequestContext) {
  const response = await request.post('/api/progression/reset', { data: {} });
  expect(response.ok(), await response.text()).toBeTruthy();
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('pending');
}

// openDrawer loads Home and opens the assistant drawer, which stays open
// across a reload once opened.
async function openDrawer(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() =>
    Boolean(
      (
        window as unknown as {
          PersonalAssistantPanel?: { _state?: { personalAssistant?: unknown } };
        }
      ).PersonalAssistantPanel?._state?.personalAssistant
    )
  );
  const open = await page.evaluate(() =>
    Boolean(
      (
        window as unknown as { PersonalAssistantPanel?: { isOpen?: () => boolean } }
      ).PersonalAssistantPanel?.isOpen?.()
    )
  );
  if (!open) await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
}

const card = (page: Page) => page.locator('#personalAssistantSecondBrain');
const action = (page: Page, id: string) => card(page).locator(`[data-second-brain-action="${id}"]`);
// The read-only alternative sits behind "Already have notes?"; opening it
// reads and connects nothing.
const existing = (page: Page) => card(page).locator('[data-second-brain-existing]');
async function expandExisting(page: Page) {
  if ((await existing(page).getAttribute('open')) === null)
    await existing(page).locator('summary').click();
  await expect(existing(page)).toHaveAttribute('open', '');
}
// The choice's primary row: Set up my notes, Not now (and Don't ask again).
const primaryRow = (page: Page) => card(page).locator('.pa-second-brain__primary button');

test.beforeAll(async ({ request }) => {
  const current = await secondBrain(request);
  if (current.status !== 'not_connected') {
    const response = await request.post(`${ENDPOINT}/disconnect`, {
      data: { request_id: `spec-disconnect-${Date.now()}`, if_version: current.version }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
  }
});

test('the ask opens the conversation, names the vault, and Not now ends the turn', async ({
  page,
  request
}) => {
  await resetGettingStarted(request);
  await openDrawer(page);
  await expect(card(page)).toBeVisible();
  // Notes-first even with a vault detected: setup is the one primary action and
  // the vault is supporting detail under "Already have notes?".
  await expect(card(page).getByRole('heading', { name: 'Set up your notes' })).toBeVisible();
  await expect(card(page)).toContainText('Personal notes · Personal HQ');
  await expect(primaryRow(page)).toHaveText(['Set up my notes', 'Not now']);
  await expect(card(page).locator('.btn-primary')).toHaveText(['Set up my notes']);
  await expect(existing(page).locator('summary')).toContainText(
    'Obsidian vault detected: “Second Brain”'
  );
  // The ask is the conversation's first message, and the folder hand-over is
  // not shown beside it.
  await expect(page.locator('#personalAssistantThread > :first-child')).toHaveId(
    'personalAssistantSecondBrain'
  );
  await expect(page.getByText("Now let's explore a folder you're working in.")).toHaveCount(0);
  await expandExisting(page);
  await expect(action(page, 'read')).toHaveText('Read “Second Brain”');
  await expect(existing(page)).toContainText("It won't write to it.");
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('shown');
  // Opening the alternative connected nothing.
  expect((await secondBrain(request)).status).toBe('not_connected');

  await action(page, 'later').click();
  await expect(card(page)).toContainText(
    "Okay. The Notes building on your HQ map sets this up whenever you're ready."
  );
  await expect(card(page).locator('button')).toHaveCount(0);
  expect((await secondBrain(request)).ask.state).toBe('deferred');

  await openDrawer(page);
  await expect(card(page)).toBeHidden();
});

// A pinned card sits under Needs you, which the drawer folds behind its strip.
async function unfoldNeedsYou(page: Page) {
  const strip = page.locator('#personalAssistantTodayStripToggle');
  await expect(strip).toBeVisible();
  if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
}

async function todayNeeds(request: APIRequestContext) {
  const today = (await (await request.get('/api/personal-assistant/today')).json()).today;
  const needs = today?.needs_you?.items || [];
  return needs as { kind: string; title: string; route?: string }[];
}

test('shown is not answered: after a reload the ask waits under Needs you and on Today', async ({
  page,
  request
}) => {
  await resetGettingStarted(request);
  await openDrawer(page);
  await expect(page.locator('#personalAssistantThread > :first-child')).toHaveId(
    'personalAssistantSecondBrain'
  );
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('shown');

  await page.reload();
  await openDrawer(page);
  // The same card, now a pinned Needs you card with the same actions.
  await expect(
    page.locator('#personalAssistantNeedsYouCards #personalAssistantSecondBrain')
  ).toHaveCount(1);
  await expect(card(page)).toContainText('Set up your notes');
  await expect(action(page, 'setup')).toHaveCount(1);
  await expect(action(page, 'later')).toHaveCount(1);
  await expect
    .poll(async () => (await todayNeeds(request)).find(row => row.kind === 'second_brain_ask'))
    .toMatchObject({ title: 'Set up your notes', route: '/?quest=second-brain' });
});

test("Not now comes back on day 7 with Don't ask again, which ends it", async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'Needs SECOND_BRAIN_SANDBOX to age the ask.');
  await resetGettingStarted(request);
  const response = await request.post(`${ENDPOINT}/ask`, {
    data: { decision: 'later', request_id: `spec-later-${Date.now()}` }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  await openDrawer(page);
  await expect(card(page)).toBeHidden();
  expect((await todayNeeds(request)).some(row => row.kind === 'second_brain_ask')).toBe(false);

  execFileSync(resolve('scripts/second-brain-age.sh'), ['--ask', sandbox, '7']);
  await page.reload();
  await openDrawer(page);
  await unfoldNeedsYou(page);
  await expect(card(page)).toContainText('Still want to set up your notes?');
  await expect(primaryRow(page)).toHaveText(['Set up my notes', 'Not now', "Don't ask again"]);
  await expect(card(page).locator('.btn-primary')).toHaveText(['Set up my notes']);
  await expect
    .poll(
      async () => (await todayNeeds(request)).find(row => row.kind === 'second_brain_ask')?.title
    )
    .toBe('Still want to set up your notes?');

  await action(page, 'none').click();
  await expect(card(page)).toContainText("Got it. I won't ask about notes again.");
  expect((await secondBrain(request)).ask.state).toBe('declined');
  const progression = await (await request.get('/api/progression')).json();
  const mission = progression.missions.find(
    (entry: { id: string }) => entry.id === 'pa-second-brain'
  );
  expect(mission?.status).toBe('skipped');
  expect((await todayNeeds(request)).some(row => row.kind === 'second_brain_ask')).toBe(false);
});

test("the ask offers Set up my notes instead of I don't keep notes", async ({ page, request }) => {
  await resetGettingStarted(request);
  await openDrawer(page);
  await expandExisting(page);
  await expect(card(page).locator('[data-second-brain-action]')).toHaveText([
    'Set up my notes',
    'Not now',
    'Read “Second Brain”',
    'Another folder…'
  ]);
  await expect(action(page, 'none')).toHaveCount(0);
  await expect(card(page)).not.toContainText('of my own');
  // A decline already on record still declines the ask and skips the mission.
  const response = await request.post(`${ENDPOINT}/ask`, {
    data: { decision: 'none', request_id: `spec-decline-${Date.now()}` }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  expect((await secondBrain(request)).ask.state).toBe('declined');
  const progression = await (await request.get('/api/progression')).json();
  const mission = progression.missions.find(
    (entry: { id: string }) => entry.id === 'pa-second-brain'
  );
  expect(mission?.status).toBe('skipped');
});

test('with nothing detected the ask points at a folder, and the chooser takes a pasted path', async ({
  page,
  request
}) => {
  await resetGettingStarted(request);
  // Detection is a server read cached for five minutes; the copy for an
  // empty vault list is checked by emptying it on the way to the page.
  await page.route(`**${ENDPOINT}**`, async route => {
    const response = await route.fetch();
    const body = await response.json().catch(() => null);
    if (!body?.second_brain) return route.fulfill({ response });
    body.second_brain.detected = [];
    await route.fulfill({ response, json: body });
  });
  await openDrawer(page);
  await expect(card(page).getByRole('heading', { name: 'Set up your notes' })).toBeVisible();
  await expect(existing(page).locator('.pa-second-brain__detected')).toHaveCount(0);
  await expandExisting(page);
  await expect(card(page).locator('button')).toHaveText([
    'Set up my notes',
    'Not now',
    'Read a folder I have…'
  ]);

  await action(page, 'choose').click();
  await expect(card(page)).toContainText('Where do you keep your notes?');
  await card(page).getByLabel('or paste a folder path').fill(vault);
  await card(page).getByRole('button', { name: 'Use this path' }).click();
  await expect(card(page)).toContainText('Read Second Brain?');
  await expect(card(page)).toContainText('Read-only. Nothing is moved, edited, or deleted.');
  await action(page, 'back').click();
  await expect(card(page)).toContainText('Where do you keep your notes?');
  await action(page, 'back').click();
  await expect(card(page)).toContainText('Set up your notes');
  // Back returns to the choice with the alternative still open.
  await expect(existing(page)).toHaveAttribute('open', '');
});

test('read the vault: consent, one headline, add the open items once, and Look again waits', async ({
  page,
  request
}) => {
  await resetGettingStarted(request);
  await openDrawer(page);
  await expandExisting(page);
  await action(page, 'read').click();
  await expect(card(page)).toContainText('Read Second Brain?');
  await expect(card(page)).toContainText(vault);
  await expect(card(page)).toContainText(
    /Short excerpts go to your model, .+\/.+, to write the summary\.|No model is set, so the summary is counts only\./
  );

  await action(page, 'connect').click();
  // A rules-only headline on the fixture vault, or the model's when one is set.
  await expect(action(page, 'add')).toHaveText('Add 8 as follow-ups', { timeout: 75_000 });
  await expect(card(page).locator('.pa-specialist-offer__headline')).toHaveText(/^12 open items/);
  // The fixture's stalest project was last touched 120 days ago, so the month
  // moves with the date the suite runs.
  const staleMonth = new Date(Date.now() - 120 * 86_400_000).toLocaleString('en-US', {
    month: 'long'
  });
  await expect(card(page)).toContainText(
    `Studio Website has 3 open items untouched since ${staleMonth}`
  );
  await card(page).getByText('Show everything (12)').click();
  const heading = card(page).locator('.pa-second-brain__open', {
    hasText: 'Projects/Album Release'
  });
  await expect(heading).toHaveAttribute(
    'href',
    'obsidian://open?vault=Second%20Brain&file=Projects%2FAlbum%20Release'
  );
  await expect(card(page)).not.toContainText('[[');

  await action(page, 'add').click();
  // A re-run reconnects the same vault: its items keep their ids, so the
  // follow-ups made the first time are found instead of made again.
  await expect(card(page)).toContainText(
    /Added 8 follow-ups to Personal HQ\.|You already added those\./
  );
  const look = (await (await request.get(`${ENDPOINT}/look`)).json()).look;
  const again = await request.post(`${ENDPOINT}/look/${look.look_id}/follow-ups`, {
    data: { items: 'suggested', request_id: `spec-accept-${Date.now()}` }
  });
  expect((await again.json()).follow_ups).toMatchObject({ created: 0, skipped: 8 });
  const followups = (await (await request.get('/api/personal-hq/followups')).json()).followups;
  expect(
    followups.filter((entry: { source: { type: string } }) => entry.source.type === 'note_item')
  ).toHaveLength(8);

  // Look again is offered, and waits ten minutes after a look.
  await action(page, 'again').click();
  await expect(card(page)).toContainText('Ori looked a moment ago. Look again in a few minutes.');
  expect((await secondBrain(request)).status).toBe('connected');
});
