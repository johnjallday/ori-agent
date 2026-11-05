import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { basename } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Notes-first onboarding across surfaces (tasks/tasks-notes-first-onboarding.md).
// The personal notes ask belongs to the Personal Assistant: on a workspace page
// whose drawer talks to that workspace's Commander it is not shown, it is not
// recorded as shown, and it comes back when the user switches to the assistant.
// An explicit Notes entry hands the drawer to the assistant through the same
// guarded switch the header uses, keeping the draft and the Commander's session.
//
// Real host, real hire and Personal HQ; /api/chat is answered in the browser so
// no model runs. A hire cannot be undone, so the file runs in order against one
// fresh sandbox:
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_NOTES_FIRST_ONBOARDING_SANDBOX \
//     tests/notes-first-onboarding.spec.ts -- --project=chromium --workers=1

test.describe.configure({ mode: 'serial' });

const BRAIN = '/api/personal-assistant/second-brain';

type Ref = { id: string; slug: string };
let commander: Ref;

async function ok(response: Awaited<ReturnType<APIRequestContext['post']>>) {
  expect(response.ok(), `${response.url()} → ${response.status()} ${await response.text()}`).toBe(
    true
  );
  return response.json();
}

async function workspaceRef(request: APIRequestContext, id: string): Promise<Ref> {
  const workspace = await (await request.get(`/api/workspaces/${id}`)).json();
  return { id, slug: String(workspace.folder_slug || workspace.slug || id) };
}

async function secondBrain(request: APIRequestContext) {
  return (await (await request.get(BRAIN)).json()).second_brain as {
    status: string;
    version: number;
    ask: { state: string };
  };
}

test.beforeAll(async ({ request }) => {
  const sandbox = process.env.ORI_NOTES_FIRST_ONBOARDING_SANDBOX;
  test.skip(!sandbox, 'Use e2e-fresh.sh --sandbox-env ORI_NOTES_FIRST_ONBOARDING_SANDBOX');
  expect(basename(sandbox!)).toMatch(/^ori-e2e\./);
  await request.post('/api/onboarding/skip');
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  assistant = (
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'notes-first-hire',
          if_version: assistant.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help me plan.',
          focus_areas: ['plan_my_day']
        }
      })
    )
  ).personal_assistant;
  await ok(
    await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'notes-first-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    })
  );
  assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(assistant.state).toBe('active');
  await ok(
    await request.post('/api/agents', {
      data: { name: 'Portfolio Manager', catalog_role: 'orchestrator' }
    })
  );
  const created = await ok(
    await request.post('/api/workspaces', {
      data: { name: 'Music Production Home', entry_agent_name: 'Portfolio Manager' }
    })
  );
  const id = String(created.id || created.workspace?.id || created.folder?.id || '');
  expect(id, JSON.stringify(created)).not.toBe('');
  commander = await workspaceRef(request, id);
  // The ask is waiting and has never been shown.
  const notes = await secondBrain(request);
  expect(notes.status).toBe('not_connected');
  expect(notes.ask.state).toBe('pending');
});

// Arrival delivery and the Commander's welcome have their own journeys; these
// cases exercise an otherwise empty conversation.
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/lead-conversation/open', route =>
    route.fulfill({ json: { changed: false } })
  );
});

type Traffic = { prompted: number; chats: number; release: () => void; hold: boolean };

/** Counts /prompted posts and answers /api/chat without a model. */
async function watch(page: Page): Promise<Traffic> {
  const traffic: Traffic = { prompted: 0, chats: 0, release: () => {}, hold: false };
  page.on('request', req => {
    if (req.method() === 'POST' && new URL(req.url()).pathname === `${BRAIN}/prompted`)
      traffic.prompted += 1;
  });
  await page.route('**/api/chat', async route => {
    traffic.chats += 1;
    if (traffic.hold) {
      await new Promise<void>(resolve => {
        traffic.release = resolve;
      });
    }
    await route.fulfill({ json: { response: 'Fixture reply from the manager.' } });
  });
  return traffic;
}

async function openDrawer(page: Page) {
  const panel = page.locator('#personalAssistantPanel');
  const launcher = page.locator('#personalAssistantLauncher');
  await expect
    .poll(async () => (await panel.isVisible()) || (await launcher.isVisible()))
    .toBe(true);
  if (!(await panel.isVisible())) await launcher.click();
  await expect(panel).toBeVisible();
}

const card = (page: Page) => page.locator('#personalAssistantSecondBrain');
const title = (page: Page) => page.locator('#personalAssistantPanelTitle');
const input = (page: Page) => page.locator('#personalAssistantInput');

async function choose(page: Page, name: RegExp) {
  await page.locator('#personalAssistantRecipientButton').click();
  const menu = page.locator('#personalAssistantRecipientMenu');
  await menu.getByRole('menuitemradio', { name }).click();
  await expect(menu).toBeHidden();
}

test('a workspace manager never presents the personal notes ask as its own reply', async ({
  page,
  request
}) => {
  const traffic = await watch(page);
  await page.goto(`/workspaces/${commander.slug}?mode=map`);
  await openDrawer(page);
  await expect(title(page)).toHaveText('Portfolio Manager');
  // Give the status and the ask every chance to arrive.
  await page.waitForFunction(() =>
    Boolean(
      (
        window as unknown as { PersonalAssistantSecondBrain?: { current?: () => unknown } }
      ).PersonalAssistantSecondBrain?.current?.()
    )
  );
  await expect(card(page)).toBeHidden();
  await expect(page.locator('#personalAssistantThread')).not.toContainText('Set up your notes');
  // Hidden is not shown: nothing was recorded, deferred or declined.
  expect(traffic.prompted).toBe(0);
  expect((await secondBrain(request)).ask.state).toBe('pending');
});

test('switching to the assistant shows the ask once, keeps the draft, and switching back hides it', async ({
  page,
  request
}) => {
  const traffic = await watch(page);
  await page.goto(`/workspaces/${commander.slug}?mode=map`);
  await openDrawer(page);
  await expect(title(page)).toHaveText('Portfolio Manager');
  const draft = '  half-typed mix note → “reverb” 🎛️\nsecond line ';
  await input(page).fill(draft);
  await input(page).focus();

  await choose(page, /Atlas/);
  await expect(title(page)).toHaveText('Atlas');
  await expect(card(page)).toBeVisible();
  await expect(card(page)).toContainText('Personal notes · Personal HQ');
  await expect(card(page).getByRole('heading', { name: 'Set up your notes' })).toBeVisible();
  await expect(page.locator('#personalAssistantSecondBrain')).toHaveCount(1);
  expect(await input(page).inputValue()).toBe(draft);
  // Shown now, under the assistant, and only once.
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('shown');
  await expect.poll(() => traffic.prompted).toBe(1);

  await choose(page, /Portfolio Manager/);
  await expect(title(page)).toHaveText('Portfolio Manager');
  await expect(card(page)).toBeHidden();
  expect(await input(page).inputValue()).toBe(draft);
  expect((await secondBrain(request)).ask.state).toBe('shown');

  // And back again: the same card, not a second one, and no second receipt.
  await choose(page, /Atlas/);
  await expect(card(page)).toBeVisible();
  await expect(page.locator('#personalAssistantSecondBrain')).toHaveCount(1);
  expect(traffic.prompted).toBe(1);
  // Restoring the card never takes focus from the composer.
  await expect(card(page).locator(':focus')).toHaveCount(0);
});

test('an explicit Notes entry hands the drawer to the assistant and keeps the manager session', async ({
  page
}) => {
  const traffic = await watch(page);
  await page.goto(`/workspaces/${commander.slug}?mode=map`);
  await openDrawer(page);
  // The previous case left Atlas picked for this workspace in this tab; start
  // from the manager with a real session of its own.
  if ((await title(page).textContent())?.trim() !== 'Portfolio Manager')
    await choose(page, /Portfolio Manager/);
  await input(page).fill('What should I mix first?');
  await page.locator('#personalAssistantSend').click();
  await expect(
    page.locator('#homeAssistantConversation [data-message-author="Portfolio Manager"]')
  ).toContainText('Fixture reply from the manager.');
  expect(traffic.chats).toBe(1);
  const sessionKey = `ori.personalAssistant.recipientSession.${commander.id}`;
  const session = await page.evaluate(key => sessionStorage.getItem(key), sessionKey);
  expect(session).toBeTruthy();

  const draft = 'unsent thought';
  await input(page).fill(draft);
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openManage: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openManage()
  );
  await expect(title(page)).toHaveText('Atlas');
  await expect(card(page)).toBeVisible();
  await expect(card(page).getByRole('heading', { name: 'Set up your notes' })).toBeVisible();
  expect(await input(page).inputValue()).toBe(draft);
  expect(await page.evaluate(key => sessionStorage.getItem(key), sessionKey)).toBe(session);

  // Back to the manager: still the same session, the draft still there, and
  // the notes card out of its conversation. (/api/chat is answered in the
  // browser, so the server session holds no reply to re-hydrate here.)
  await choose(page, /Portfolio Manager/);
  await expect(title(page)).toHaveText('Portfolio Manager');
  await expect(card(page)).toBeHidden();
  expect(await input(page).inputValue()).toBe(draft);
  expect(await page.evaluate(key => sessionStorage.getItem(key), sessionKey)).toBe(session);
  expect(traffic.chats).toBe(1);
});

test('a Notes entry while the manager is replying changes nothing and says how to retry', async ({
  page,
  request
}) => {
  const traffic = await watch(page);
  await page.goto(`/workspaces/${commander.slug}?mode=map`);
  await openDrawer(page);
  if ((await title(page).textContent())?.trim() !== 'Portfolio Manager')
    await choose(page, /Portfolio Manager/);
  traffic.hold = true;
  await input(page).fill('Long question for the manager');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => traffic.chats).toBe(1);
  const before = await secondBrain(request);

  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openSetup: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openSetup()
  );
  await expect(title(page)).toHaveText('Portfolio Manager');
  await expect(card(page)).toBeHidden();
  await expect(page.locator('#personalAssistantPanelStatus')).toContainText(
    'Wait for Portfolio Manager to reply before switching.'
  );
  await expect(page.locator('#personalAssistantPanelStatus')).toContainText(
    'Then open Notes again.'
  );
  const after = await secondBrain(request);
  expect(after.ask.state).toBe(before.ask.state);
  expect(after.status).toBe('not_connected');

  traffic.release();
  await expect(
    page.locator('#homeAssistantConversation [data-message-author="Portfolio Manager"]')
  ).toContainText('Fixture reply from the manager.');
  // Once the reply lands, the same entry goes through.
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openSetup: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openSetup()
  );
  await expect(title(page)).toHaveText('Atlas');
  await expect(card(page)).toContainText("What's the main thing you're working on right now?");
});
