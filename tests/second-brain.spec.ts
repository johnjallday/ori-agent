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
  await expect(card(page)).toContainText(
    'Do you keep a second brain? I found your Obsidian vault "Second Brain".'
  );
  // The ask is the conversation's first message, and the folder hand-over is
  // not shown beside it.
  await expect(page.locator('#personalAssistantThread > :first-child')).toHaveId(
    'personalAssistantSecondBrain'
  );
  await expect(page.getByText("Now let's explore a folder you're working in.")).toHaveCount(0);
  await expect(action(page, 'read')).toHaveText('Read Second Brain');
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('shown');

  await action(page, 'later').click();
  await expect(card(page)).toContainText(
    "Okay. Connect your notes from Home whenever you're ready."
  );
  await expect(card(page).locator('button')).toHaveCount(0);
  expect((await secondBrain(request)).ask.state).toBe('deferred');

  await openDrawer(page);
  await expect(card(page)).toBeHidden();
});

test("I don't keep notes declines the ask and skips the mission", async ({ page, request }) => {
  await resetGettingStarted(request);
  await openDrawer(page);
  await action(page, 'none').click();
  await expect(card(page)).toContainText("Got it. I won't ask about notes again.");
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
  await expect(card(page)).toContainText(
    "Do you keep a second brain, notes in Obsidian or a folder of Markdown? Point me at it and I'll tell you what's open."
  );
  await expect(card(page).locator('button')).toHaveText([
    'Choose folder…',
    'Not now',
    "I don't keep notes"
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
  await expect(card(page)).toContainText('Do you keep a second brain');
});

test('read the vault: consent, one headline, add the open items once, and Look again waits', async ({
  page,
  request
}) => {
  await resetGettingStarted(request);
  await openDrawer(page);
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
  await expect(card(page)).toContainText('Studio Website has 3 open items untouched since June');
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
    /Added 8 follow-ups to Personal HQ\.|Those are already in your follow-ups\./
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
