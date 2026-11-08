import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Home's one primary action follows the assistant relationship, end to end in a
// real browser against the real server and store (home-hire-first-actions
// FR26): "Meet your assistant" before the hire, "Build My HQ" after it, and
// "Hire an agent" once the HQ exists. New Workspace stays beside it as the
// secondary and opens the Create Workspace modal in every state.
//
// On an empty map before the HQ exists, the first-run welcome card carries the
// same step instead, and the header primary steps aside so there is exactly one
// such button (assistant-hq-onboarding 1.3–1.5). Tree, Explore first, a
// populated Home and an explicit deep link all fall back to the header entry.
//
// A hire is durable and cannot be undone, so the ladder needs a server of its
// own that has never hired:
//
//   ./scripts/e2e-fresh.sh tests/home-hire-first-ladder.spec.ts
//
// No model is configured and none is needed: every step is the server's own.
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

async function relationship(request: APIRequestContext) {
  return (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
}

// expectPrimary loads Home and checks the creation group in one ladder state:
// the primary first with its label, New Workspace second as the secondary, and
// no create affordance on the Map to tell a different story.
async function expectPrimary(page: Page, label: string) {
  await page.goto('/');
  const primary = page.locator('#cockpitPrimaryActionBtn');
  await expect(primary).toBeVisible();
  await expect(primary).toHaveText(label);
  await expect(primary).toHaveClass(/modern-btn-primary/);
  const create = page.locator('#cockpitCreateWorkspaceBtn');
  await expect(create).toBeVisible();
  await expect(create).toHaveClass(/modern-btn-secondary/);
  const order = await page
    .locator('.cockpit-area-actions__create > button')
    .evaluateAll(buttons =>
      buttons.filter(button => !(button as HTMLElement).hidden).map(button => button.id)
    );
  expect(order).toEqual(['cockpitPrimaryActionBtn', 'cockpitCreateWorkspaceBtn']);
  await expect(page.locator('#cockpitMap')).toBeVisible();
  await expect(
    page.locator('#cockpitMap [data-ws-map-create], #cockpitMap .ws-map-pad')
  ).toHaveCount(0);
  return primary;
}

// expectWelcome loads Home on an empty profile before the HQ exists and checks
// the first-run welcome (assistant-hq-onboarding 1.3): the card sits in the map
// area with one dominant action, the header primary has stepped aside so there
// is exactly one focusable button for that action, and New Workspace stays.
async function expectWelcome(page: Page, title: string, action: string) {
  await page.goto('/');
  const card = page.locator('#cockpitWelcome [data-home-welcome]');
  await expect(card).toBeVisible();
  await expect(card.getByRole('heading', { level: 3 })).toHaveText(title);
  await expect(page.getByRole('button', { name: action, exact: false })).toHaveCount(1);
  await expect(card.locator('[data-home-welcome-action="primary"]')).toContainText(action);
  await expect(page.locator('#cockpitPrimaryActionBtn')).toBeHidden();
  await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeVisible();
  await expect(page.locator('#cockpitMap')).toBeVisible();
  await expect(page.locator('[data-hq-site]')).toBeVisible();
  await expect(page.locator('#homeCockpit')).toHaveAttribute('data-first-run', 'welcome');
  return card;
}

// Meet lands in the personal-assistant configuration itself (group 2): the
// Agents page with its hire preset already open, name focused, model visible,
// and no walkthrough callout over it.
async function expectAssistantConfiguration(page: Page) {
  await page.waitForURL(url => url.pathname === '/agents');
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('#addAgentModal')).toHaveClass(/is-assistant-hire/);
  await expect(page.locator('#cr-name')).toBeFocused();
  await expect(page.locator('#cr-model-group')).toBeVisible();
  await expect(page.locator('#cr-more')).not.toHaveAttribute('open', '');
  await expect(page.locator('#oriSpotlight .ori-spotlight__callout')).toHaveCount(0);
}

// Every request this page makes that could change something. Showing the
// welcome, choosing Explore first and reloading must make none.
function recordWrites(page: Page) {
  const writes: string[] = [];
  page.on('request', request => {
    if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method())) {
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  return writes;
}

// New Workspace opens the same Create Workspace modal in every state.
async function expectNewWorkspaceOpens(page: Page) {
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  await expect(modal).toHaveAttribute('data-import-mode', 'false');
  expect(await page.evaluate(() => (window as any).sessionManager?.importEntryPoint)).toBe(
    'home_cockpit_create'
  );
  await modal.locator('[data-bs-dismiss="modal"]').first().click();
  await expect(modal).toBeHidden();
}

// The drawer's card and chooser open through the deep link's own guard, which
// waits for their controllers; a press before they mount does nothing.
async function waitForDrawerControllers(page: Page) {
  await page.waitForFunction(() =>
    Boolean(
      (window as any).PersonalAssistantHQCard?.show &&
      (window as any).PersonalAssistantFolder?.openChooser
    )
  );
}

test.describe('Home leads with the next hire-first step', () => {
  test.beforeAll(async ({ request }) => {
    await request.post('/api/onboarding/skip');
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    const initial = await relationship(request);
    test.skip(
      initial.state !== 'needs_hire',
      `needs a server that has never hired (found "${initial.state}"); run it with ./scripts/e2e-fresh.sh`
    );
  });

  test('before the hire: the empty map welcomes with Meet your assistant', async ({ page }) => {
    const writes = recordWrites(page);
    const card = await expectWelcome(page, 'Meet your assistant.', 'Meet your assistant');
    await expect(card.locator('[aria-current="step"]')).toContainText('Meet');
    await expect(page.locator('[data-hq-site]')).toHaveAttribute(
      'aria-label',
      /Your Personal HQ starts here/
    );
    // Rendering moved no focus onto the card.
    expect(await card.evaluate(el => el.contains(document.activeElement))).toBe(false);
    expect(writes).toEqual([]);
    await expectNewWorkspaceOpens(page);
    // The Create Workspace modal previews a plan with POSTs of its own; count
    // only what the welcome, Tree, Explore first and the reload do.
    writes.length = 0;

    // Tree reaches the same step through the compact header entry.
    await page.locator('#cockpitViewTree').click();
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toBeVisible();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toHaveText('Meet your assistant');
    await page.locator('#cockpitViewMap').click();
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toBeHidden();

    // Explore first: presentation only, keeps a compact way back, survives a
    // reload, and makes no request that could record a decision.
    await card.locator('[data-home-welcome-action="explore"]').click();
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    const primary = page.locator('#cockpitPrimaryActionBtn');
    await expect(primary).toBeVisible();
    await expect(primary).toBeFocused();
    await expect(page.locator('#homeCockpit')).not.toHaveAttribute('data-first-run', 'welcome');
    await page.reload();
    await expect(primary).toBeVisible();
    await expect(primary).toHaveText('Meet your assistant');
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    await expect(page.locator('#oriSpotlight')).toBeHidden();
    expect(writes).toEqual([]);

    // The compact entry opens the same configuration the card would.
    await primary.click();
    await expectAssistantConfiguration(page);
  });

  test('before the hire: the card itself opens the assistant configuration', async ({
    page,
    request
  }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.removeItem('ori.home.welcome.v1'));
    const card = await expectWelcome(page, 'Meet your assistant.', 'Meet your assistant');
    await card.locator('[data-home-welcome-action="primary"]').click();
    await expectAssistantConfiguration(page);
    // Opening it created nothing. Cancel leaves the relationship as it was.
    await page.locator('#addAgentModal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator('#addAgentModal')).toBeHidden();
    expect((await relationship(request)).state).toBe('needs_hire');
    // Older links still reach it: Home's own Mission 01 link goes straight there.
    await page.goto('/?quest=meet-assistant');
    await expectAssistantConfiguration(page);
    await page.goto('/?quest=meet-assistant&briefing=1');
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();
    await expect(page).toHaveURL(url => url.search === '');
    await expect(page.locator('#oriSpotlight')).toHaveCount(0);
  });

  test('before the hire: unknown, failed or explicit states never show the welcome', async ({
    page,
    request
  }) => {
    await page.goto('/');
    await page.evaluate(() => localStorage.removeItem('ori.home.welcome.v1'));

    // A failed relationship read is not an empty account: no card, no primary.
    await page.route(/\/api\/personal-assistant(\?.*)?$/, route => route.abort());
    await page.goto('/');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('[data-hq-site]')).toBeVisible();
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toBeHidden();
    await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeVisible();
    await page.unroute(/\/api\/personal-assistant(\?.*)?$/);

    // A slow read: nothing until it answers, then the card.
    await page.route(/\/api\/personal-assistant(\?.*)?$/, async route => {
      await new Promise(resolve => setTimeout(resolve, 1500));
      await route.continue();
    });
    await page.goto('/');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();
    await page.unroute(/\/api\/personal-assistant(\?.*)?$/);

    // An explicit Map focus wins over the unsolicited welcome.
    await page.goto('/?focus=personal-hq');
    await expect(page.locator('[data-hq-site]')).toHaveClass(/is-selected/);
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    // Before a hire the empty site builds nothing: pressing it goes straight to
    // meeting the assistant, and its menu offers only that.
    await page.goto('/');
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();
    await page.locator('[data-hq-site]').click({ button: 'right' });
    await expect(
      page.locator('[data-ws-map-menu] [role="menuitem"], .ws-map-menu [role="menuitem"]')
    ).toHaveText(['Meet your assistant']);
    await page.keyboard.press('Escape');
    await page.locator('[data-hq-site]').click();
    await expectAssistantConfiguration(page);

    // Existing workspaces are never covered by the full card: compact only.
    const created = await ok(
      await request.post('/api/workspaces', { data: { name: 'Welcome Ladder Existing' } })
    );
    try {
      await page.goto('/');
      const primary = page.locator('#cockpitPrimaryActionBtn');
      await expect(primary).toBeVisible();
      await expect(primary).toHaveText('Meet your assistant');
      await expect(page.locator('#cockpitWelcome')).toBeHidden();
    } finally {
      await ok(
        await request.delete(
          `/api/workspaces/${created.folder.id}?confirm=true&delete_sessions=true`
        )
      );
    }
    await page.goto('/');
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();
  });

  test('after the hire: "Build My HQ" on the welcome opens the HQ card', async ({
    page,
    request
  }) => {
    const initial = await relationship(request);
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'hire-first-ladder-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my projects moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    expect((await relationship(request)).state).toBe('needs_hq');

    // A stage change is a new invitation even after Explore first at Meet.
    await page.goto('/');
    await page.evaluate(() =>
      localStorage.setItem('ori.home.welcome.v1', JSON.stringify({ explored: 'meet' }))
    );
    const card = await expectWelcome(page, 'Give your work a home.', 'Build My HQ');
    await expect(card).toContainText('Atlas is ready');
    await expect(card.locator('[aria-current="step"]')).toContainText('Build your HQ');
    await expectNewWorkspaceOpens(page);
    await waitForDrawerControllers(page);
    await card.locator('[data-home-welcome-action="primary"]').click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#personalAssistantHQCard')).toBeVisible();
    // The drawer is now the active surface; the card behind it steps aside,
    // and comes back when the drawer closes.
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    // Opening the HQ card made no HQ: the relationship is unchanged.
    expect((await relationship(request)).state).toBe('needs_hq');
    await page.locator('#personalAssistantClose').click();
    await expect(page.locator('#cockpitWelcome [data-home-welcome]')).toBeVisible();

    // Explore first at this stage leaves the compact Build My HQ entry.
    await page.reload();
    await page.locator('#cockpitWelcome [data-home-welcome-action="explore"]').click();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toHaveText('Build My HQ');
    await page.reload();
    await expect(page.locator('#cockpitWelcome')).toBeHidden();
    await expect(page.locator('#cockpitPrimaryActionBtn')).toBeVisible();
  });

  test('after the HQ: "Hire an agent" opens the folder chooser', async ({ page, request }) => {
    const hired = await relationship(request);
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'hire-first-ladder-hq',
          if_version: hired.state_version,
          name: 'My HQ',
          timezone: 'UTC'
        }
      })
    );
    expect((await relationship(request)).state).toBe('active');

    const primary = await expectPrimary(page, 'Hire an agent');
    await expectNewWorkspaceOpens(page);
    await waitForDrawerControllers(page);
    await primary.click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#personalAssistantContextChooser')).toBeVisible();
    await expect(
      page.locator('#personalAssistantFolderChoices').getByRole('button').first()
    ).toBeVisible();
  });

  test('at phone width both header buttons fit without horizontal scroll', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await expectPrimary(page, 'Hire an agent');
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth
    );
    expect(overflow).toBeLessThanOrEqual(1);
  });
});
