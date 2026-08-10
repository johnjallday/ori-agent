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
  await guideEntryPoint(page).click();
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

/**
 * The guide's entry point for a given route.
 *
 * Every page floats the launcher bottom-right EXCEPT Home, which already names
 * Ori in the workspace-area header — two buttons driving one controller and one
 * panel was a duplicate, so Home hides the floating one. Tests therefore ask
 * the page which entry point it has rather than assuming the launcher.
 */
function guideEntryPoint(page: Page) {
  const onHome = new URL(page.url()).pathname === '/';
  return onHome ? page.locator('#oriGuideMapTrigger') : page.locator('#oriGuideLauncher');
}

async function gotoPage(page: Page, route: string) {
  await skipOnboarding(page);
  await page.goto(route, { waitUntil: 'domcontentloaded' });
  await expect(guideEntryPoint(page)).toBeVisible();
}

test.describe('Ori Guide identity and boundary', () => {
  test('the entry point names Ori as a guide before anything is asked', async ({ page }) => {
    await gotoPage(page, '/');

    // The boundary has to be legible on first exposure, not discovered after
    // mistaking Ori for a work agent (FR-22/FR-27). Whichever button a page
    // shows has to carry the name AND the role.
    const entry = guideEntryPoint(page);
    await expect(entry).toContainText('Ask Ori');
    await expect(entry).toContainText('App Guide');
    await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'false');

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

  // "It's an <a>, so guards apply" is a claim about the browser, not a
  // demonstration. This dirties a real form and proves the page's own
  // unsaved-changes guard actually fires when the guide navigates away
  // (FR-24/FR-36/FR-49).
  test('a guide destination cannot skip an unsaved-changes guard', async ({ page, request }) => {
    // A built-in agent has no editable form, so the fixture needs one of its
    // own rather than whichever card happens to sort first.
    const name = `PWGuard${Date.now()}`;
    const made = await request.post('/api/agents', {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(made.ok()).toBeTruthy();

    await skipOnboarding(page);
    await page.goto(`/agents?agent=${encodeURIComponent(name)}`, {
      waitUntil: 'domcontentloaded'
    });
    await expect(page.locator('#oriGuideLauncher')).toBeVisible();
    await expect(page.locator('#ov-description')).toBeVisible();

    await page.locator('#ov-description').fill('an edit nobody saved');
    await page.locator('#ov-description').blur();

    let beforeUnloadFired = false;
    page.on('dialog', async d => {
      if (d.type() === 'beforeunload') beforeUnloadFired = true;
      await d.dismiss();
    });

    await openGuide(page);
    await ask(page, 'what is a vault');
    await page.locator('.ori-guide__action', { hasText: 'Vaults' }).click();
    // Give the navigation attempt a moment to raise the guard.
    await page.waitForTimeout(500);

    expect(beforeUnloadFired, 'navigating from the guide bypassed the unsaved-changes guard').toBe(
      true
    );
    // Dismissed, so the user is still on the page with their edit intact.
    await expect(page).toHaveURL(/\/agents/);
    await expect(page.locator('#ov-description')).toHaveValue('an edit nobody saved');
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

test.describe('Ori on the Home map', () => {
  test('the map character drives the one shared panel and controller', async ({ page }) => {
    await gotoPage(page, '/');

    const mapOri = page.locator('#oriGuideMapTrigger');
    await expect(mapOri).toBeVisible();

    await mapOri.click();
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    // One controller, one panel: the shared launcher still tracks the state the
    // map character set, even though Home does not show it (FR-21).
    await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'true');

    await mapOri.click();
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
    await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'false');
  });

  test('Home shows exactly one entry point, and it names Ori as the guide', async ({ page }) => {
    await gotoPage(page, '/');
    await expect(page.locator('#oriGuidePanel')).toHaveCount(1);

    // The header character is the only VISIBLE way in on Home. The floating
    // launcher is still in the DOM — one controller owns both — but showing it
    // here was a second button for one thing.
    const mapOri = page.locator('#oriGuideMapTrigger');
    await expect(mapOri).toBeVisible();
    await expect(page.locator('#oriGuideLauncher')).toBeHidden();

    // Losing the launcher must not lose the role it carried: the guide/work
    // boundary has to be readable before anything is asked (FR-22/FR-27).
    await expect(mapOri).toContainText('Ask Ori');
    await expect(mapOri).toContainText('App Guide');
  });

  test('the map character does not appear away from Home', async ({ page }) => {
    await gotoPage(page, '/agents');
    await expect(page.locator('#oriGuideMapTrigger')).toHaveCount(0);
    // The shared launcher is still there, so the guide is never unreachable.
    await expect(page.locator('#oriGuideLauncher')).toBeVisible();
  });
});

test.describe('Ori Guide work handoff across pages', () => {
  test('a work request made elsewhere arrives on Home prefilled and unsent', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);

    const request = 'summarize the launch notes';
    await ask(page, request);

    await page.locator('.ori-guide__action', { hasText: 'Workspace Manager' }).click();

    // The browser navigates to Home and the request survives the trip.
    await expect(page).toHaveURL(/\/$/);
    const input = page.locator('#homeAssistantInput');
    await expect(input).toHaveValue(request);
  });

  test('the carried request never travels through the URL', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await ask(page, 'send an email to the whole team');
    await page.locator('.ori-guide__action', { hasText: 'Workspace Manager' }).click();

    await expect(page).toHaveURL(/\/$/);
    // The user's words are their own: a query parameter would put them in
    // history and the address bar.
    expect(page.url()).not.toContain('email');
    expect(page.url()).not.toContain('?');
  });

  test('a carried request is consumed once, not resurrected by a reload', async ({ page }) => {
    await gotoPage(page, '/agents');
    await openGuide(page);
    await ask(page, 'draft the release notes');
    await page.locator('.ori-guide__action', { hasText: 'Workspace Manager' }).click();
    await expect(page.locator('#homeAssistantInput')).toHaveValue('draft the release notes');

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#homeAssistantInput')).toHaveValue('');
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

    await guideEntryPoint(page).focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    // Opening focuses the input, because that is what the user came to use.
    await expect(page.locator('#oriGuideInput')).toBeFocused();

    await page.keyboard.type('what is a workspace');
    await page.keyboard.press('Enter');
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  });

  test('closing returns focus to whichever entry point opened it', async ({ page }) => {
    await gotoPage(page, '/');
    await openGuide(page);
    await page.locator('#oriGuideClose').click();
    // Focus goes back to the button the user actually pressed (FR-26), which on
    // Home is the header character rather than the suppressed launcher.
    await expect(guideEntryPoint(page)).toBeFocused();
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

    // The name has to be VISIBLE, not only in the accessible label: the guide
    // launcher names itself on every page, so a user comparing the two can only
    // tell which one does work if this one says so too (FR-27/FR-81–FR-83).
    await expect(card.locator('.home-command-kicker')).toContainText('WORKSPACE MANAGER');
    await expect(page.locator('.ori-guide__launcher-name')).toHaveText('Ask Ori');
  });

  // Fifth occurrence of this bug class in this feature, so Home asserts it too.
  // The specific collision it was written for is gone — the rail footer moved
  // into the header and Home no longer renders the fixed launcher — but the bug
  // class is "a floating element silently eats a control's clicks", which is
  // worth guarding by hit-testing rather than by geometry against one element.
  test('nothing floating covers Home’s cockpit actions', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await gotoPage(page, '/');

    // Home suppresses the floating launcher precisely so it cannot land on
    // anything; the header character is the entry point here.
    await expect(page.locator('#oriGuideLauncher')).toBeHidden();

    // Every header action must be the element that actually receives a click at
    // its own centre — the check the geometry version was approximating.
    for (const id of ['#cockpitCaptureBtn', '#cockpitSummaryBtn', '#cockpitRailToggle']) {
      const topmost = await page.evaluate(selector => {
        const el = document.querySelector(selector);
        if (!el) return 'missing';
        const r = el.getBoundingClientRect();
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return el.contains(hit) || el === hit ? 'clickable' : (hit?.className ?? 'unknown');
      }, id);
      expect(topmost, `${id} is covered by ${topmost}`).toBe('clickable');
    }
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
