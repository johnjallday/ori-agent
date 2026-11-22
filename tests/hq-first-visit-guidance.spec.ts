import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// HQ first-visit guidance: while the server's first_visit_state is pending, each
// part of the Personal HQ (Overview cards and the Notes panel; Map nodes) carries
// a neutral outline and a `?`, and a hover, keyboard focus or tap shows one tip.
// Credential-free, no model; needs its own fresh sandbox because it builds an HQ.
//
//   ./scripts/e2e-fresh.sh --port <free port> tests/hq-first-visit-guidance.spec.ts -- --workers=1
//
// The last case finishes the visit, so it must stay last (serial).

const BRAIN = '/api/personal-assistant/second-brain';
const FINISH = '/api/personal-hq/first-visit-state';
const AXE_URL = 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js';
let overviewPath = '';
let mapPath = '';
let hqID = '';

test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  // Tips run independently of the automatic invitation. Visit as a returning browser.
  await page.addInitScript(id => localStorage.setItem(`ori.hq.guidedOffered.${id}`, '1'), hqID);
});

async function json(
  request: APIRequestContext,
  method: 'get' | 'post',
  url: string,
  data?: unknown
) {
  const response = await request[method](url, data === undefined ? undefined : { data });
  expect(response.ok(), `${method} ${url}: ${await response.text()}`).toBeTruthy();
  return response.json();
}

const tip = (page: Page) => page.locator('#hq-part-tip');
const mark = (page: Page, key: string) => page.locator(`[data-hq-tip-mark="${key}"]`);
const part = (page: Page, key: string) => page.locator(`[data-hq-tip-part="${key}"]`);
const hint = (page: Page) => page.locator('[data-hq-flow-hint]');
const anyTipMarkup = (page: Page) =>
  page.locator(
    '[data-hq-tip-part], .ws-hq-tip-mark, .ws-hq-tip-cell, [data-hq-flow-hint], .is-tip-marked'
  );

async function sideways(page: Page) {
  return page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth
  );
}

async function accessible(page: Page, includes: string[]) {
  // The tip fades in over 120ms; axe would measure its text mid-fade and report a
  // contrast the tip never has at rest. Reduced motion removes the fade (a test
  // above checks that), so axe sees the tip as it settles.
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addScriptTag({ url: AXE_URL });
  const violations = await page.evaluate(
    async (selectors: string[]) =>
      (
        await (window as any).axe.run(
          { include: selectors.map(selector => [selector]) },
          { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] } }
        )
      ).violations,
    includes
  );
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

test.beforeAll(async ({ request }) => {
  await json(request, 'post', '/api/onboarding/skip', {});
  const root = (await json(request, 'get', '/api/settings/workspace-root')).default_workspace_root;
  await json(request, 'post', '/api/settings/workspace-root', { workspace_root: root });
  const before = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  expect(before.state, 'requires its own fresh sandbox').toBe('needs_hire');
  await json(request, 'post', '/api/personal-assistant/hire', {
    request_id: 'guidance-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  const hired = (await json(request, 'get', '/api/personal-assistant')).personal_assistant;
  const notes = (await json(request, 'get', BRAIN)).second_brain;
  await json(request, 'post', '/api/personal-assistant/hq', {
    request_id: 'guidance-build',
    if_version: hired.state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: true, witness: notes.setup_default.path }
  });
  const status = (await json(request, 'get', '/api/personal-hq/status')).status;
  expect(status.first_visit_state).toBe('pending');
  hqID = status.workspace_id;
  const base = `/workspaces/${encodeURIComponent(status.workspace.folder_slug)}`;
  overviewPath = `${base}?mode=details`;
  mapPath = `${base}?mode=map`;
});

test.describe('Overview, desktop', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('a fresh Overview marks each running part and Notes; a hover explains one and its mark goes', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await expect(page.locator('body')).toHaveAttribute('data-hq-first-visit', 'pending');
    for (const key of ['watchtower', 'daily-brief', 'memory', 'notes'])
      await expect(mark(page, key), key).toBeVisible();
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    // The folded "Not in use yet" line and the Needs you panel are not explained here.
    await expect(
      page.locator('.ws-hq-unused [data-hq-tip-mark], [data-cmd-hq-needs] [data-hq-tip-mark]')
    ).toHaveCount(0);
    // A card is still one button; its ? is beside it.
    await expect(page.locator('[data-cmd-hq-card="daily-brief"] [data-hq-tip-mark]')).toHaveCount(
      0
    );

    await part(page, 'daily-brief').hover();
    await expect(tip(page)).toBeVisible();
    await expect(tip(page)).toHaveAttribute('role', 'tooltip');
    await expect(tip(page)).toContainText('A short read of your day, on the schedule you chose.');
    await expect(tip(page)).toContainText('Atlas');
    await expect(part(page, 'daily-brief')).toHaveAttribute('aria-describedby', /hq-part-tip/);
    await expect(mark(page, 'daily-brief')).toHaveAttribute('aria-expanded', 'true');
    await page.mouse.move(2, 2);
    await expect(tip(page)).toBeHidden();
    await expect(mark(page, 'daily-brief')).toHaveCount(0);
    await expect(part(page, 'daily-brief')).not.toHaveClass(/is-tip-marked/);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(3);
  });

  test('keyboard: the ? is reachable, the tip shows on focus, Escape closes only the tip', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    await part(page, 'watchtower').focus();
    await page.keyboard.press('Tab');
    await expect(mark(page, 'watchtower')).toBeFocused();
    await expect(tip(page)).toBeVisible();
    await expect(tip(page)).toContainText('Watches your other workspaces');
    await expect(mark(page, 'watchtower')).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('Escape');
    await expect(tip(page)).toBeHidden();
    await expect(mark(page, 'watchtower'), 'Escape does not move focus').toBeFocused();
    await expect(
      page.locator('[data-hq-firstvisit]'),
      'and opens or closes nothing else'
    ).toHaveCount(0);
    // Focus moving on settles the mark that was looked at.
    await page.keyboard.press('Tab');
    await expect(mark(page, 'watchtower')).toHaveCount(0);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(3);
  });

  test('a reload keeps the looked-at parts cleared, and a looked-at card still explains itself', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await part(page, 'memory').hover();
    await expect(tip(page)).toBeVisible();
    await page.mouse.move(2, 2);
    await expect(mark(page, 'memory')).toHaveCount(0);
    await page.reload();
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(3);
    await expect(mark(page, 'memory')).toHaveCount(0);
    await expect(part(page, 'memory'), 'its tip is still there').toHaveCount(1);
    await part(page, 'memory').hover();
    await expect(tip(page)).toContainText('What agents here remember between conversations.');
    await expect(mark(page, 'memory'), 'and does not bring the mark back').toHaveCount(0);
  });

  test('the Notes panel is explained through its ?, not by hovering or typing in it', async ({
    page
  }) => {
    await page.goto(overviewPath);
    const notes = page.locator('[data-cmd-hq-host="notes"]');
    await expect(notes).toHaveAttribute('data-hq-tip-part', 'notes');
    // The ? sits beside the host (the live panel is moved into the host), top right,
    // clear of the panel's own header buttons.
    await expect(notes.locator('[data-hq-tip-mark]')).toHaveCount(0);
    const ring = await mark(page, 'notes').boundingBox();
    const connection = await page.getByRole('link', { name: 'Open My Notes' }).boundingBox();
    expect(ring!.x).toBeGreaterThanOrEqual(connection!.x + connection!.width);
    await notes.hover({ position: { x: 40, y: 30 } });
    await page.waitForTimeout(600);
    await expect(tip(page)).toBeHidden();
    await mark(page, 'notes').hover();
    await expect(tip(page)).toBeVisible();
    await expect(tip(page)).toContainText('A folder you own.');
    await page.mouse.move(2, 2);
    await expect(mark(page, 'notes')).toHaveCount(0);
    await expect(notes).not.toHaveClass(/is-tip-marked/);
  });

  test('reduced motion removes the tip’s fade', async ({ page }) => {
    for (const [reducedMotion, expected] of [
      ['reduce', 'none'],
      ['no-preference', 'ws-hq-tip-in']
    ] as const) {
      await page.emulateMedia({ reducedMotion });
      await page.goto(overviewPath);
      await page.evaluate(() => localStorage.clear());
      await page.reload();
      await mark(page, 'daily-brief').click();
      await expect(tip(page)).toBeVisible();
      expect(await tip(page).evaluate(node => getComputedStyle(node).animationName)).toBe(expected);
    }
  });

  for (const theme of ['light', 'dark']) {
    test(`axe finds nothing on the marked Overview or its open tip (${theme})`, async ({
      page
    }) => {
      await page.goto(overviewPath);
      await page.evaluate(
        value => document.documentElement.setAttribute('data-bs-theme', value),
        theme
      );
      await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
      await mark(page, 'memory').click();
      await expect(tip(page)).toBeVisible();
      await accessible(page, ['.ws-hq-main', '#hq-part-tip']);
    });
  }
});

test.describe('Overview, phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

  test('a tap on the ? shows the tip inside the screen; a tap elsewhere closes it; nothing scrolls sideways', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    const target = mark(page, 'memory');
    await target.scrollIntoViewIfNeeded();
    const hit = await target.boundingBox();
    expect(hit!.width).toBeGreaterThanOrEqual(44);
    expect(hit!.height).toBeGreaterThanOrEqual(44);
    await target.tap();
    await expect(tip(page)).toBeVisible();
    const box = (await tip(page).boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(15.5);
    expect(box.x + box.width).toBeLessThanOrEqual(390 - 15.5);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    // Touch has no hover: the tip stays until another tap.
    await page.waitForTimeout(600);
    await expect(tip(page)).toBeVisible();
    await page.getByRole('heading', { name: 'My HQ', level: 2 }).first().tap();
    await expect(tip(page)).toBeHidden();
    await expect(mark(page, 'memory')).toHaveCount(0);
  });

  test('the Notes ? and its tip fit at 390px', async ({ page }) => {
    await page.goto(overviewPath);
    // The page repaints as its Notes arrive; wait until every mark is drawn.
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    await mark(page, 'notes').tap();
    await expect(tip(page)).toBeVisible();
    const box = (await tip(page).boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(15.5);
    expect(box.x + box.width).toBeLessThanOrEqual(390 - 15.5);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    await accessible(page, ['.ws-hq-main', '#hq-part-tip']);
  });
});

test.describe('Map (Flow), desktop', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('every node keeps its size and fits with its mark; the hint rests above the lead card without moving it', async ({
    page
  }) => {
    await page.goto(mapPath);
    await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
    // This HQ was built over every workspace, so its brief reads others and the
    // Map draws Watchtower as "Other workspaces" (an HQ whose brief reads only
    // itself has no such node, and so no tip for it here).
    for (const key of [
      'notes',
      'email',
      'calendar-ops',
      'memory',
      'watchtower',
      'needs-you',
      'follow-ups',
      'daily-brief',
      'tickets'
    ])
      await expect(mark(page, key), key).toBeVisible();
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(9);
    await expect(hint(page)).toBeVisible();
    await expect(hint(page)).toContainText('Point at any part of the map');
    await expect(hint(page).getByRole('button', { name: 'Close the hint' })).toHaveText('Close');
    // The hint appearing never takes focus.
    expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true);

    const measure = () =>
      page.evaluate(() => {
        const opmap = document.querySelector('.ws-cmd-opmap')!.getBoundingClientRect();
        const nodes = Array.from(document.querySelectorAll('.ws-cmd-flow-node')).map(node => {
          const box = node.getBoundingClientRect();
          const cell = node.closest('li')!.getBoundingClientRect();
          const ring = node
            .parentElement!.querySelector('.ws-hq-tip-mark > span')
            ?.getBoundingClientRect();
          const head = node.querySelector('.ws-cmd-flow-node-head')!.getBoundingClientRect();
          return {
            key: node.getAttribute('data-hq-tip-part') || node.getAttribute('data-cmd-flow-node'),
            height: Math.round(box.height),
            fits:
              node.scrollWidth <= node.clientWidth + 1 &&
              node.scrollHeight <= node.clientHeight + 1,
            insideMap:
              box.left >= opmap.left &&
              box.right <= opmap.right &&
              box.top >= opmap.top &&
              box.bottom <= opmap.bottom,
            ringInside:
              !ring ||
              (ring.left >= cell.left &&
                ring.right <= cell.right &&
                ring.top >= cell.top &&
                ring.bottom <= cell.bottom),
            ringClearOfHead:
              !ring || ring.top >= head.bottom || ring.right <= head.left || ring.left >= head.right
          };
        });
        const lead = document.querySelector('.ws-cmd-flow-lead')!.getBoundingClientRect();
        return {
          nodes,
          lead: [Math.round(lead.top), Math.round(lead.left), Math.round(lead.height)]
        };
      });
    const before = await measure();
    expect(before.nodes.length).toBeGreaterThanOrEqual(6);
    for (const node of before.nodes) {
      expect(node.height, node.key + ' keeps the Flow node height').toBe(76);
      expect(node.fits, node.key + ' fits').toBe(true);
      expect(node.insideMap, node.key + ' is not clipped by the Map').toBe(true);
      expect(node.ringInside, node.key + ' ? stays inside its cell').toBe(true);
      expect(node.ringClearOfHead, node.key + ' ? clears the name and state').toBe(true);
    }

    // Closing the hint does not move the lead card (the connector lines meet it).
    await hint(page).getByRole('button', { name: 'Close the hint' }).click();
    await expect(hint(page)).toHaveCount(0);
    await expect(page.locator('.ws-cmd-flow-lead')).toBeFocused();
    expect((await measure()).lead).toEqual(before.lead);
  });

  test('a hover on a node explains it, takes the hint away, and clears the node’s mark', async ({
    page
  }) => {
    await page.goto(mapPath);
    await expect(hint(page)).toBeVisible();
    await part(page, 'tickets').hover();
    await expect(tip(page)).toBeVisible();
    await expect(tip(page)).toContainText(
      'Work handed to an agent. Open one to run it or review it.'
    );
    await expect(hint(page), 'a tip takes over from the hint').toHaveCount(0);
    await page.mouse.move(2, 2);
    await expect(mark(page, 'tickets')).toHaveCount(0);
    // A reload does not offer the hint again.
    await page.reload();
    await expect(page.locator('.ws-hq-tip-mark').first()).toBeVisible();
    await expect(hint(page)).toHaveCount(0);
  });

  test('Escape closes the hint when nothing else is open and moves no focus', async ({ page }) => {
    await page.goto(mapPath);
    await expect(hint(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(hint(page)).toHaveCount(0);
    expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true);
  });

  test('what was looked at on the Overview has no mark on the Map, and the Free map gets nothing', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await part(page, 'memory').hover();
    await expect(tip(page)).toBeVisible();
    await page.mouse.move(2, 2);
    await expect(mark(page, 'memory')).toHaveCount(0);

    await page.locator('[data-cmd-view-mode="map"]').click();
    await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
    await expect(part(page, 'memory'), 'the node can still explain itself').toHaveCount(1);
    await expect(mark(page, 'memory')).toHaveCount(0);
    await expect(mark(page, 'tickets')).toBeVisible();

    // Pin a tip, then switch to Free: it closes and the Free map draws nothing.
    await mark(page, 'tickets').click();
    await expect(tip(page)).toBeVisible();
    await page.locator('[data-cmd-map-arrangement="free"]').click();
    await expect(tip(page)).toBeHidden();
    await expect(anyTipMarkup(page)).toHaveCount(0);
    // The arrangement is remembered: a reload stays Free and stays quiet.
    await page.reload();
    await expect(page.locator('.ws-cmd-map-shell:not(.is-flow)')).toBeVisible();
    await expect(anyTipMarkup(page)).toHaveCount(0);
    // Back to Flow with no render() between: the marks of parts not yet looked at
    // return, and the tips work (Tickets was looked at, so it has no mark but
    // still explains itself).
    await page.locator('[data-cmd-map-arrangement="flow"]').click();
    await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
    await expect(mark(page, 'needs-you')).toBeVisible();
    await expect(mark(page, 'tickets')).toHaveCount(0);
    await expect(mark(page, 'memory')).toHaveCount(0);
    await part(page, 'tickets').hover();
    await expect(tip(page)).toBeVisible();
  });

  for (const theme of ['light', 'dark']) {
    test(`axe finds nothing on the marked Map, its hint or an open tip (${theme})`, async ({
      page
    }) => {
      await page.goto(mapPath);
      await page.evaluate(
        value => document.documentElement.setAttribute('data-bs-theme', value),
        theme
      );
      await expect(hint(page)).toBeVisible();
      await mark(page, 'needs-you').click();
      await expect(tip(page)).toBeVisible();
      await accessible(page, ['.ws-cmd-flow', '#hq-part-tip']);
    });
  }
});

test.describe('Map (Flow), narrow', () => {
  for (const width of [390, 760]) {
    test.describe(`${width}px`, () => {
      test.use({ viewport: { width, height: 844 }, hasTouch: true });

      test('the stacked Map keeps marks, hint and tip inside the screen with no sideways scroll', async ({
        page
      }) => {
        await page.goto(mapPath);
        await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
        await expect(hint(page)).toBeVisible();
        // Stacked, the hint is the line above the lead card, not an overlay on it.
        const hintBox = (await hint(page).boundingBox())!;
        const leadBox = (await page.locator('.ws-cmd-flow-lead').boundingBox())!;
        expect(hintBox.y + hintBox.height).toBeLessThanOrEqual(leadBox.y + 1);
        expect(await sideways(page)).toBeLessThanOrEqual(0);
        await mark(page, 'follow-ups').scrollIntoViewIfNeeded();
        await mark(page, 'follow-ups').tap();
        await expect(tip(page)).toBeVisible();
        const box = (await tip(page).boundingBox())!;
        expect(box.x).toBeGreaterThanOrEqual(15.5);
        expect(box.x + box.width).toBeLessThanOrEqual(width - 15.5);
        expect(await sideways(page)).toBeLessThanOrEqual(0);
        // Stacked, the hint is a line of the page: a tip showing must not remove it
        // (that would move the part the finger is on), only its own Close does.
        await expect(hint(page)).toBeVisible();
        await hint(page).scrollIntoViewIfNeeded();
        await hint(page).getByRole('button', { name: 'Close the hint' }).tap();
        await expect(hint(page)).toHaveCount(0);
      });
    });
  }
});

const panel = (page: Page) => page.locator('#personalAssistantPanel');
const greeting = (page: Page) => page.locator('#personalAssistantHQGreeting');
const choice = (page: Page, name: string) => page.locator(`[data-hq-firstvisit-action="${name}"]`);

// The drawer remembers it was open across a reload in the same tab, and then its
// launcher is not shown: open it only when it is not open already.
async function openDrawer(page: Page) {
  const launcher = page.locator('#personalAssistantLauncher');
  await expect
    .poll(async () => (await panel(page).isVisible()) || (await launcher.isVisible()))
    .toBe(true);
  if (!(await panel(page).isVisible())) await launcher.click();
  await expect(panel(page)).toBeVisible();
}

test.describe('Drawer greeting', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test('arriving with the drawer closed leaves it closed; opening it does not add a second welcome', async ({
    page
  }) => {
    await page.goto(overviewPath);
    await expect(page.locator('body')).toHaveAttribute('data-hq-first-visit', 'pending');
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    // The chooser never opens the drawer.
    await expect(panel(page)).toBeHidden();
    expect(
      await page.evaluate(() => sessionStorage.getItem('ori.personalAssistant.panelOpen') || '')
    ).toBe('');
    await openDrawer(page);
    await expect(greeting(page)).toBeHidden();
    // It never took focus; and it is a card, not a message in the conversation.
    expect(
      await page.evaluate(() =>
        Boolean(document.activeElement?.closest('#personalAssistantHQGreeting'))
      )
    ).toBe(false);
    await expect(
      page.locator('#homeAssistantConversation #personalAssistantHQGreeting')
    ).toHaveCount(0);
    await expect(page.locator('#homeAssistantConversation [data-message-role]')).toHaveCount(0);
    // It does not announce itself.
    await expect(
      greeting(page).locator('[aria-live], [role="status"], [role="alert"]')
    ).toHaveCount(0);
    // The page's own tips are untouched by the drawer being open.
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
  });

  test('Daily Brief and note capture keep their normal Overview surfaces', async ({ page }) => {
    await page.goto(overviewPath);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    await page.getByRole('button', { name: /^Daily Brief:/ }).click();
    const brief = page.locator('.ws-cmd-modal-panel.is-daily-brief');
    await expect(brief).toBeVisible();
    await expect(panel(page)).toBeHidden();
    await page.getByRole('button', { name: 'Close Daily Brief' }).click();
    await expect(brief).toBeHidden();
    await page.getByRole('textbox', { name: /Save to My Notes/ }).focus();
    await expect(panel(page)).toBeHidden();
    await expect(page.getByRole('textbox', { name: /Save to My Notes/ })).toBeFocused();
    expect(
      await page.evaluate(() => sessionStorage.getItem('ori.personalAssistant.panelOpen') || '')
    ).toBe('');
  });

  test('on a short screen the chooser title and dismissal are in view', async ({ page }) => {
    // The drawer opens pinned to the end of its content; a greeting taller than
    // the pane would lose its top. Reproduced at a 640px-tall window.
    await page.setViewportSize({ width: 1280, height: 640 });
    await page.goto(overviewPath);
    await page.getByRole('button', { name: 'Things to try' }).click();
    await expect(page.locator('#hq-suggestions-title')).toBeInViewport();
    await expect(page.getByRole('button', { name: 'Maybe later' })).toBeInViewport();
    // It did not take focus by being scrolled to.
    expect(
      await page.evaluate(() =>
        Boolean(document.activeElement?.closest('#personalAssistantHQGreeting'))
      )
    ).toBe(false);
  });

  test('returning to Overview from Map still offers note capture', async ({ page }) => {
    await page.goto(mapPath);
    await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
    await page.locator('[data-cmd-view-mode="details"]').click();
    await expect(panel(page)).toBeHidden();
    await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    await expect(page.getByRole('textbox', { name: /Save to My Notes/ })).toBeVisible();
  });

  test('a conversation does not acquire a second greeting on drawer reopen', async ({ page }) => {
    await page.route('**/api/chat', route =>
      route.fulfill({ json: { response: 'Fixture reply.' } })
    );
    await page.goto(overviewPath);
    await openDrawer(page);
    await expect(greeting(page)).toBeHidden();
    await page.locator('#personalAssistantInput').fill('What should I do first?');
    await page.locator('#personalAssistantSend').click();
    await expect(greeting(page)).toBeHidden();
    await expect(
      page.locator('#homeAssistantConversation [data-message-role]').first()
    ).toBeVisible();
    // Closing and reopening the drawer does not bring it back.
    await page.locator('#personalAssistantClose').click();
    await expect(panel(page)).toBeHidden();
    await openDrawer(page);
    await expect(greeting(page)).toBeHidden();
    // The page's tips are unaffected by a message.
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
  });

  test('on a phone chooser actions fit the sheet and never open the drawer', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(overviewPath);
    await expect(page.locator('.ws-hq-tip-mark')).toHaveCount(4);
    await page.getByRole('button', { name: 'Things to try' }).click();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    for (const name of ['folder', 'email', 'apps']) {
      const box = (await choice(page, name).boundingBox())!;
      expect(box.height, name).toBeGreaterThanOrEqual(44);
      expect(box.x + box.width, name + ' inside the screen').toBeLessThanOrEqual(390);
    }
    await page.getByRole('button', { name: 'Maybe later' }).click();
    await expect(panel(page)).toBeHidden();
  });

  for (const theme of ['light', 'dark']) {
    test(`axe finds nothing on the greeting (${theme})`, async ({ page }) => {
      await page.goto(overviewPath);
      await page.evaluate(
        value => document.documentElement.setAttribute('data-bs-theme', value),
        theme
      );
      await page.getByRole('button', { name: 'Things to try' }).click();
      await accessible(page, ['#hq-suggestions']);
    });
  }
});

test('completing the mission-driven visit retires tips without opening a second welcome', async ({
  page
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(overviewPath);
  await openDrawer(page);
  await expect(greeting(page)).toBeHidden();
  await page.evaluate(() => (window as any).workspaceCommand.firstVisit.finish());
  await expect(greeting(page)).toBeHidden();
  await expect(panel(page)).toBeVisible();
  await expect(page.locator('body')).not.toHaveAttribute('data-hq-first-visit', /.+/);
  await expect(anyTipMarkup(page)).toHaveCount(0);
  await page.reload();
  await openDrawer(page);
  await expect(greeting(page)).toBeHidden();
  await expect(greeting(page)).toHaveText('');
});

test('a done HQ shows nothing from this feature on either view, and a hover opens nothing', async ({
  page,
  request
}) => {
  await json(request, 'post', FINISH, { state: 'done' });
  await page.setViewportSize({ width: 1280, height: 900 });
  for (const path of [overviewPath, mapPath]) {
    await page.goto(path);
    await expect(page.locator('body')).not.toHaveAttribute('data-hq-first-visit', /.+/);
    await expect(page.locator('.ws-hq-team, .ws-cmd-map-shell').first()).toBeVisible();
    await expect(anyTipMarkup(page)).toHaveCount(0);
    await expect(tip(page)).toHaveCount(0);
    await page.locator('[data-cmd-hq-card], .ws-cmd-flow-node').first().hover();
    await page.waitForTimeout(600);
    await expect(tip(page)).toHaveCount(0);
  }
});
