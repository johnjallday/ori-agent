import { test, expect, Page } from '@playwright/test';

/**
 * Ori Guide — end-to-end behaviour and safety boundary
 * (PRD cozy-character-experience FR-20–FR-50, FR-115–FR-128).
 *
 * Not part of CI — run against an isolated demo server:
 *   source scripts/wt.sh; wt demo 8931
 *   ./scripts/e2e.sh --port 8931 tests/ori-guide.spec.ts
 *
 * The unit tests already prove the guide's action type cannot express a
 * mutation. What only a browser can show is the part that matters to a user:
 * that a coachmark marks a control without pressing it, that a work request
 * fills the command box without sending it, and that dismissing the guide
 * leaves the page exactly as it was.
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function openGuide(page: Page) {
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  // Opening fires its own greeting request. Wait for it to land before asking
  // anything: otherwise its late response can satisfy the next wait, and the
  // assertions run against the greeting instead of the answer.
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', /.+/);
}

async function ask(page: Page, question: string) {
  // Opening the panel already fires its own request and stamps a status, so
  // "wait for a status" would pass instantly against the *previous* answer.
  // Clear it first, then wait for this question's reply to land.
  await page
    .locator('#oriGuideReply')
    .evaluate(el => el.setAttribute('data-status', 'awaiting-test'));

  await page.locator('#oriGuideInput').fill(question);
  await page.locator('#oriGuideSend').click();

  await expect(page.locator('#oriGuideReply')).not.toHaveAttribute('data-status', 'awaiting-test');
}

async function gotoPage(page: Page, route: string) {
  await skipOnboarding(page);
  await page.goto(route, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#oriGuideLauncher')).toBeVisible();
}

test.describe('Ori Guide identity and boundary', () => {
  test('the launcher names Ori as a guide before anything is asked', async ({ page }) => {
    await gotoPage(page, '/');

    // The boundary has to be legible on first exposure, not discovered after
    // mistaking Ori for a work agent (FR-22/FR-27).
    const launcher = page.locator('#oriGuideLauncher');
    await expect(launcher).toContainText('Ask Ori');
    await expect(launcher).toContainText('App Guide');
    await expect(launcher).toHaveAttribute('aria-expanded', 'false');

    await openGuide(page);
    const boundary = page.locator('.ori-guide__boundary');
    await expect(boundary).toContainText('not a work agent');
    await expect(boundary).toContainText('Workspace Manager');
  });

  test('opening the guide reports the current location and offers approved topics', async ({
    page
  }) => {
    await gotoPage(page, '/');
    await openGuide(page);

    await expect(page.locator('.ori-guide__location')).toContainText('Home');
    await expect(page.locator('.ori-guide__topic').first()).toBeVisible();
  });

  test('the location follows the route the user is actually on', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await expect(page.locator('.ori-guide__location')).toContainText('Agents');
  });

  test('exactly one guide instance exists per page', async ({ page }) => {
    for (const route of ['/', '/agents', '/vaults', '/settings']) {
      await gotoPage(page, route);
      await expect(page.locator('#oriGuideLauncher')).toHaveCount(1);
      await expect(page.locator('#oriGuidePanel')).toHaveCount(1);
    }
  });
});

test.describe('Ori Guide explanations and destinations', () => {
  test('a known concept is explained and offers its real destination', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'what is a vault');

    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
    await expect(page.locator('.ori-guide__answer')).toContainText('write-only');

    const action = page.locator('.ori-guide__action', { hasText: 'Vaults' });
    await expect(action).toHaveAttribute('href', '/vaults');
  });

  test('a vault explanation states the boundary and reveals no secret', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'where are my stored credentials');

    const answer = await page.locator('#oriGuideReply').innerText();
    expect(answer.toLowerCase()).toContain('cannot read them');
    expect(answer).not.toMatch(/sk-[A-Za-z0-9]/);
  });

  test('a destination action is a real link, so page guards still apply', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'what is an agent');

    const action = page.locator('.ori-guide__action', { hasText: 'Agents' });
    // An anchor rather than a scripted jump: middle-click, new tab, and
    // before-unload warnings all keep working (FR-36/FR-49).
    await expect(action).toHaveJSProperty('tagName', 'A');
    await action.click();
    await expect(page).toHaveURL(/\/agents$/);
  });

  test('an unknown question says so and offers approved topics instead', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'what is the airspeed velocity of an unladen swallow');

    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'unknown');
    await expect(page.locator('.ori-guide__topic').first()).toBeVisible();
    // An honest miss must not manufacture somewhere to go (FR-48).
    await expect(page.locator('.ori-guide__action')).toHaveCount(0);
  });

  test('a suggested topic can be asked by clicking it', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);

    const topic = page.locator('.ori-guide__topic', { hasText: 'Agent' }).first();
    await topic.click();
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  });
});

test.describe('Ori Guide dynamic destinations', () => {
  test('naming a real workspace offers to open that workspace', async ({ page, request }) => {
    const name = `PW Guide WS ${Date.now()}`;
    const created = await request.post(`/api/workspaces`, {
      data: { name, workspace_preset: 'general' }
    });
    expect(created.ok()).toBeTruthy();
    const id = (await created.json())?.folder?.id;
    expect(id).toBeTruthy();

    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, `where is my ${name} workspace`);

    const action = page.locator('.ori-guide__action', { hasText: name });
    await expect(action).toHaveAttribute('href', `/workspace/${id}`);
  });

  test('a workspace that does not exist yields no destination', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'open my Nonexistent Zeta Workspace');

    const hrefs = await page
      .locator('.ori-guide__action')
      .evaluateAll(els => els.map(el => el.getAttribute('href') || ''));
    for (const href of hrefs) {
      expect(href).not.toMatch(/^\/workspace\//);
    }
  });
});

test.describe('Ori Guide coachmarks', () => {
  test('a coachmark marks and focuses the control without activating it', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await ask(page, 'what is an agent');

    const coachmark = page.locator('.ori-guide__action', { hasText: 'Show me where' });
    await expect(coachmark).toBeVisible();
    await coachmark.click();

    const target = page.locator('#newAgentBtn');
    await expect(target).toHaveClass(/is-ori-coachmark/);
    await expect(target).toBeFocused();

    // The whole point: pointing at New Agent must not open the create panel
    // (FR-42).
    await expect(page.locator('#createPanel')).toBeHidden();
  });

  test('a coachmark is not offered from a route that does not own the control', async ({
    page
  }) => {
    await gotoPage(page, '/vaults');
    await openGuide(page);
    await ask(page, 'what is an agent');

    // No coachmark here, but the canonical destination is still offered (FR-43).
    await expect(page.locator('.ori-guide__action', { hasText: 'Show me where' })).toHaveCount(0);
    await expect(page.locator('.ori-guide__action', { hasText: 'Agents' })).toBeVisible();
  });

  test('Escape clears the coachmark first, then closes the guide', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await ask(page, 'what is an agent');
    await page.locator('.ori-guide__action', { hasText: 'Show me where' }).click();
    await expect(page.locator('#newAgentBtn')).toHaveClass(/is-ori-coachmark/);

    await page.keyboard.press('Escape');
    await expect(page.locator('#newAgentBtn')).not.toHaveClass(/is-ori-coachmark/);
    // Dismissing guidance must not also lose the panel being read (FR-24).
    await expect(page.locator('#oriGuidePanel')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
  });

  test('closing the guide leaves no mark behind on the page', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await ask(page, 'what is an agent');
    await page.locator('.ori-guide__action', { hasText: 'Show me where' }).click();

    await page.locator('#oriGuideClose').click();
    await expect(page.locator('#newAgentBtn')).not.toHaveClass(/is-ori-coachmark/);
  });
});

test.describe('Ori Guide work handoff', () => {
  test('a work request is handed to Workspace Manager, prefilled and unsent', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);

    const request = 'send an email to the whole team';
    await ask(page, request);

    await expect(page.locator('.ori-guide__answer')).toContainText('working agent');
    const handoff = page.locator('.ori-guide__action', { hasText: 'Workspace Manager' });
    await expect(handoff).toBeVisible();
    await handoff.click();

    const input = page.locator('#homeAssistantInput');
    await expect(input).toHaveValue(request);
    await expect(input).toBeFocused();
    // The guide steps out of the way; the user decides whether to send (FR-84).
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
  });

  test('the guide never claims to have done the work', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'delete the launch workspace');

    const answer = (await page.locator('.ori-guide__answer').innerText()).toLowerCase();
    for (const claim of ['i deleted', "i've deleted", 'done', 'taken care of']) {
      expect(answer).not.toContain(claim);
    }
  });

  test('a work request produces no destructive control', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'delete all my agents');

    // Only ever an explanation plus a handoff — never a confirm/delete control.
    const labels = await page.locator('.ori-guide__action').allInnerTexts();
    for (const label of labels) {
      expect(label.toLowerCase()).not.toMatch(/delete|confirm|remove|run|execute/);
    }
  });
});

test.describe('Ori Guide keyboard and focus', () => {
  test('the guide is fully operable from the keyboard', async ({ page }) => {
    await gotoPage(page, '/');

    await page.locator('#oriGuideLauncher').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    // Opening focuses the input, because that is what the user came to use.
    await expect(page.locator('#oriGuideInput')).toBeFocused();

    await page.keyboard.type('what is a workspace');
    await page.keyboard.press('Enter');
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  });

  test('closing returns focus to the launcher', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await page.locator('#oriGuideClose').click();
    await expect(page.locator('#oriGuideLauncher')).toBeFocused();
  });

  test('the panel exposes dialog semantics and a live region', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);

    const panel = page.locator('#oriGuidePanel');
    await expect(panel).toHaveAttribute('role', 'dialog');
    await expect(panel).toHaveAttribute('aria-labelledby', 'oriGuideTitle');
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('aria-live', 'polite');
    await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'true');
  });
});

test.describe('Ori Guide does not disturb the page', () => {
  test('opening and closing preserves the Agents collection state', async ({ page }) => {
    await gotoPage(page, '/agents');

    await page.locator('#rosterSearch').fill('a');
    const before = await page.locator('.roster-card').count();

    await openGuide(page);
    await ask(page, 'what is a toolbox');
    await page.locator('#oriGuideClose').click();

    // Filters, results, and the search box all survive (FR-25).
    await expect(page.locator('#rosterSearch')).toHaveValue('a');
    await expect(page.locator('.roster-card')).toHaveCount(before);
  });

  test('an unreachable guide leaves the page underneath usable', async ({ page }) => {
    await gotoPage(page, '/agents');
    await page.route('**/api/ori-guide', route => route.abort());

    await openGuide(page);
    await page.locator('#oriGuideInput').fill('what is an agent');
    await page.locator('#oriGuideSend').click();

    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'unavailable');
    await expect(page.locator('.ori-guide__answer')).toContainText('still works');
    // The send control must not stay stuck disabled after a failure.
    await expect(page.locator('#oriGuideSend')).toBeEnabled();

    // ...and the page behind it is genuinely still working.
    await page.locator('#oriGuideClose').click();
    await page.locator('#rosterSearch').fill('zzz');
    await expect(page.locator('#rosterSearch')).toHaveValue('zzz');
  });
});

test.describe('Workspace Manager keeps its own identity', () => {
  test('the Home command surface is Workspace Manager, not the guide', async ({ page }) => {
    await gotoPage(page, '/');

    const card = page.locator('#homeAssistantCard');
    await expect(card).toHaveAttribute('aria-label', 'Workspace Manager');
    await expect(card).toBeVisible();
    // Two distinct surfaces, both present, not one pretending to be the other.
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
  });

  test('Cmd/Ctrl+J still focuses the work surface, not the guide', async ({ page }) => {
    await gotoPage(page, '/');

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.keyboard.press(`${modifier}+j`);

    await expect(page.locator('#homeAssistantInput')).toBeFocused();
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
  });
});

test.describe('narrow layout', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('the guide is reachable and usable on a narrow screen', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await ask(page, 'what is a workspace');

    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');

    // A floating panel that pushed the page sideways would be worse than none.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth
    );
    expect(overflow).toBeLessThanOrEqual(1);
  });
});
