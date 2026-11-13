import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import { basename, join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// axe-core comes from jsdelivr like the other a11y specs; AXE_CORE_FILE serves
// a local copy of the same version where that CDN is unreachable.
const AXE_URL = 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js';
const EVIDENCE = 'tasks/evidence-notes-first-onboarding';

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
  const axeFile = process.env.AXE_CORE_FILE || '';
  if (axeFile && existsSync(axeFile)) {
    await page.route(AXE_URL, route =>
      route.fulfill({
        status: 200,
        contentType: 'text/javascript',
        body: readFileSync(axeFile, 'utf8')
      })
    );
  }
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
  // The deterministic manager welcome and its reply are separate messages.
  // Assert the reply's actual author without assuming only one message exists.
  await expect(
    page
      .locator('#homeAssistantConversation [data-message-author="Portfolio Manager"]')
      .filter({ hasText: 'Fixture reply from the manager.' })
  ).toHaveCount(1);
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
    page
      .locator('#homeAssistantConversation [data-message-author="Portfolio Manager"]')
      .filter({ hasText: 'Fixture reply from the manager.' })
  ).toHaveCount(1);
  // Once the reply lands, the same entry goes through.
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openSetup: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openSetup()
  );
  await expect(title(page)).toHaveText('Atlas');
  // Setup opens on where the notes live, with no project question first.
  await expect(card(page).locator('[data-second-brain-stage="place"]')).toBeVisible();
});

const action = (page: Page, id: string) => card(page).locator(`[data-second-brain-action="${id}"]`);

/** Moves focus with Tab alone until the target has it. */
async function tabTo(page: Page, target: Locator, max = 25) {
  for (let i = 0; i < max; i += 1) {
    if (await target.evaluate(node => node === document.activeElement)) return;
    await page.keyboard.press('Tab');
  }
  await expect(target).toBeFocused();
}

async function openHome(page: Page, theme = 'light') {
  await page.emulateMedia({ colorScheme: theme as 'light' | 'dark', reducedMotion: 'reduce' });
  await page.addInitScript(value => {
    try {
      localStorage.setItem('ori-theme', value);
    } catch (_) {
      // The page falls back to its default theme.
    }
  }, theme);
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
  await openDrawer(page);
}

async function openAsk(page: Page) {
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openAsk: () => Promise<boolean> } }
    ).PersonalAssistantSecondBrain.openAsk()
  );
  await expect(card(page).getByRole('heading', { name: 'Set up your notes' })).toBeVisible();
}

test('the card appearing by itself never takes focus from the composer', async ({ page }) => {
  await watch(page);
  await page.goto(`/workspaces/${commander.slug}?mode=map`);
  await openDrawer(page);
  if ((await title(page).textContent())?.trim() !== 'Portfolio Manager')
    await choose(page, /Portfolio Manager/);
  await input(page).fill('still typing');
  await input(page).focus();
  // The assistant becomes the recipient without a click in the drawer.
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantPanel: { choosePersonal: () => boolean } }
    ).PersonalAssistantPanel.choosePersonal()
  );
  await expect(title(page)).toHaveText('Atlas');
  await expect(card(page)).toBeVisible();
  await expect(input(page)).toBeFocused();
  expect(await input(page).inputValue()).toBe('still typing');
});

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 768, 390]) {
    test(`the Notes-first choice at ${width}px (${theme}) fits, wraps long names, and axe finds no A/AA violations`, async ({
      page
    }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      // A long, hostile vault name in the alternative must wrap, not overflow.
      await page.route('**/api/personal-assistant/second-brain', async route => {
        const response = await route.fetch();
        const body = await response.json().catch(() => null);
        if (!body?.second_brain) return route.fulfill({ response });
        body.second_brain.detected = [
          {
            id: 'v_long',
            name: '<b>Very</b> long vault name '.repeat(6).trim(),
            parent: 'Documents',
            path: '/tmp/never-read'
          }
        ];
        await route.fulfill({ response, json: body });
      });
      await openHome(page, theme);
      await openAsk(page);
      await card(page).locator('[data-second-brain-existing] > summary').click();
      await expect(action(page, 'read')).toBeVisible();
      const fits = await card(page).evaluate(node => ({
        fits: node.scrollWidth <= node.clientWidth + 1,
        right: node.getBoundingClientRect().right
      }));
      expect(fits.fits).toBeTruthy();
      expect(fits.right).toBeLessThanOrEqual(width + 1);
      // The hostile name is text, never markup.
      await expect(card(page).locator('b')).toHaveCount(0);
      await page.addScriptTag({ url: AXE_URL });
      const violations = await page.evaluate(async () =>
        (
          await (
            window as unknown as {
              axe: {
                run: (
                  selector: string,
                  options: unknown
                ) => Promise<{ violations: { id: string; nodes: { target: string[] }[] }[] }>;
              };
            }
          ).axe.run('#personalAssistantSecondBrain', {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations.map(v => ({ id: v.id, targets: v.nodes.map(n => n.target.join(' ')) }))
      );
      expect(violations).toEqual([]);
      mkdirSync(EVIDENCE, { recursive: true });
      await card(page).screenshot({ path: join(EVIDENCE, `spec-choice-${width}-${theme}.png`) });
      // Other status reads (the HQ card's too) may still be in flight.
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    });
  }
}

test('Notes card styles stay on the Notes card', async ({ page }) => {
  await openHome(page);
  // An unrelated specialist offer keeps Bootstrap's own link and outline look.
  const colors = await page.evaluate(() => {
    const offer = document.createElement('section');
    offer.className = 'pa-specialist-offer';
    offer.innerHTML =
      '<button class="btn btn-sm btn-link">Other</button><button class="btn btn-sm btn-outline-secondary">Other</button>';
    document.body.append(offer);
    const notes = document.querySelector('#personalAssistantSecondBrain');
    const probe = document.createElement('button');
    probe.className = 'btn btn-sm btn-link';
    notes?.append(probe);
    const read = (el: Element | null | undefined) => (el ? getComputedStyle(el).color : '');
    const out = {
      otherLink: read(offer.children[0]),
      otherOutline: read(offer.children[1]),
      notesLink: read(probe)
    };
    offer.remove();
    probe.remove();
    return out;
  });
  expect(colors.notesLink).not.toBe(colors.otherLink);
  expect(colors.otherLink).toBe('rgb(13, 110, 253)');
});

test('keyboard alone: set up with no project question, then a reviewed disconnect', async ({
  page,
  request
}) => {
  await openHome(page);
  await openAsk(page);
  await expect(action(page, 'setup')).toBeFocused();
  // Every Notes control, the disclosure included, shows the same ink ring.
  const summary = card(page).locator('[data-second-brain-existing] > summary');
  await tabTo(page, summary);
  const ring = await summary.evaluate(node => {
    const style = getComputedStyle(node);
    return { outline: style.outlineColor, width: style.outlineWidth, ink: style.color };
  });
  expect(ring.width).toBe('2px');
  expect(ring.outline).toBe(ring.ink);
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Shift+Tab');
  await expect(action(page, 'setup')).toBeFocused();
  await page.keyboard.press('Enter');
  // Setup goes straight to where the notes live (assistant-hq-onboarding 4.4).
  await expect(card(page).locator('[data-second-brain-stage="place"]')).toBeVisible();
  await expect(card(page).locator('[data-project-answer]')).toHaveCount(0);
  await tabTo(page, action(page, 'create'));
  await page.keyboard.press('Enter');
  await expect
    .poll(async () => (await secondBrain(request)).status, { timeout: 75_000 })
    .toBe('connected');
  // The project question is a later, explicit choice now (4.4); the first-week
  // and setup specs drive it from its own link.

  // Disconnect, reviewed, from the keyboard: the safe way out has focus.
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openManage: () => Promise<boolean> } }
    ).PersonalAssistantSecondBrain.openManage()
  );
  await tabTo(page, action(page, 'disconnect'), 40);
  await page.keyboard.press('Enter');
  await expect(action(page, 'cancel')).toBeFocused();
  await page.keyboard.press('Enter');
  // Cancel puts focus back on the Disconnect it came from.
  await expect(action(page, 'disconnect')).toBeFocused();
  expect((await secondBrain(request)).status).toBe('connected');
});
