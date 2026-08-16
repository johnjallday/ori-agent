import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Accessibility regression for the Ori Guide launcher and panel
// (PRD cozy-character-experience FR-115–FR-123, FR-127–FR-128).
//
// Not part of CI — run against an isolated demo server:
//   source scripts/wt.sh; wt demo 8931
//   ./scripts/e2e.sh --port 8931 tests/ori-guide.a11y.spec.js
//
// axe is scoped to the guide's own root so pre-existing chrome (navbar,
// sidebar) cannot mask a regression introduced here — the same approach the
// roster suite uses.
//
// These run against /agents rather than Home. Home hides the floating launcher
// and uses the map character as its single entry point (#332), so asserting on
// #oriGuideLauncher there had been failing since that change; /agents is an
// authenticated page where the shared launcher itself is the way in.

const baseUrl = process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';

async function runAxe(page, target) {
  return page.evaluate(async selector => {
    const root = selector ? document.querySelector(selector) : document;
    return await window.axe.run(root, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
  }, target);
}

async function preparePage(page, { theme = 'light', reducedMotion = 'reduce' } = {}) {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion });
  await page.addInitScript(selectedTheme => {
    window.localStorage.setItem('ori-theme', selectedTheme);
  }, theme);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
}

async function openAndAsk(page, question) {
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  // Let the panel's own greeting request land first; otherwise its late
  // response can satisfy the wait below before the question is even sent.
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', /.+/);
  await page
    .locator('#oriGuideReply')
    .evaluate(el => el.setAttribute('data-status', 'awaiting-test'));
  await page.locator('#oriGuideInput').fill(question);
  await page.locator('#oriGuideSend').click();
  await expect(page.locator('#oriGuideReply')).not.toHaveAttribute('data-status', 'awaiting-test');
}

for (const theme of ['light', 'dark']) {
  test(`ori guide accessibility (${theme})`, async ({ page }) => {
    await preparePage(page, { theme });
    await page.setViewportSize({ width: 1440, height: 950 });
    await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#oriGuideLauncher')).toBeVisible();

    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });

    // Closed: the launcher alone must be clean.
    const closed = await runAxe(page, '#oriGuideRoot');
    expect(closed.violations, JSON.stringify(closed.violations, null, 2)).toEqual([]);

    // Open with an answer, actions, and topic chips all rendered — the state a
    // user actually reads.
    await openAndAsk(page, 'what is a vault');
    const open = await runAxe(page, '#oriGuideRoot');
    expect(open.violations, JSON.stringify(open.violations, null, 2)).toEqual([]);

    // The honest-miss state has different content and its own chip list.
    await page
      .locator('#oriGuideReply')
      .evaluate(el => el.setAttribute('data-status', 'awaiting-test'));
    await page.locator('#oriGuideInput').fill('zzz not a real topic');
    await page.locator('#oriGuideSend').click();
    await expect(page.locator('#oriGuideReply')).not.toHaveAttribute(
      'data-status',
      'awaiting-test'
    );
    const unknown = await runAxe(page, '#oriGuideRoot');
    expect(unknown.violations, JSON.stringify(unknown.violations, null, 2)).toEqual([]);
  });
}

test('the guide is fully operable without a pointer', async ({ page }) => {
  await preparePage(page);
  await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#oriGuideLauncher')).toBeVisible();

  // Open, ask, and close using only the keyboard (FR-115).
  await page.locator('#oriGuideLauncher').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#oriGuideInput')).toBeFocused();

  await page.keyboard.type('what is a workspace');
  await page.keyboard.press('Enter');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');

  await page.keyboard.press('Escape');
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect(page.locator('#oriGuideLauncher')).toBeFocused();
});

test('no guide control is pointer-only', async ({ page }) => {
  await preparePage(page);
  await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
  await openAndAsk(page, 'what is an agent');

  // Every interactive element inside the panel must be focusable. Walking Tab
  // is order-dependent and brittle; what actually matters is that nothing here
  // is reachable only by pointer (FR-115).
  const unreachable = await page.evaluate(() => {
    const panel = document.getElementById('oriGuidePanel');
    const controls = panel.querySelectorAll('button, a[href], input, [tabindex]');
    const bad = [];
    for (const el of controls) {
      if (el.disabled) continue;
      // Only controls the user is actually being offered. The panel hosts the
      // work-activity region, whose controls stay hidden until there is work to
      // show; an unrendered button is not a pointer-only control.
      if (el.hidden || el.closest('[hidden]') || el.offsetParent === null) continue;
      if (el.getAttribute('tabindex') === '-1') {
        bad.push(el.id || el.className);
        continue;
      }
      el.focus();
      if (document.activeElement !== el) bad.push(el.id || el.className);
    }
    return bad;
  });

  expect(unreachable, `pointer-only controls: ${unreachable.join(', ')}`).toEqual([]);

  // And the ones the flow depends on are actually present.
  await expect(page.locator('#oriGuideSend')).toBeVisible();
  await expect(page.locator('.ori-guide__action').first()).toBeVisible();
});

test('reduced motion removes the coachmark animation but keeps the marking', async ({ page }) => {
  await preparePage(page, { reducedMotion: 'reduce' });
  await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
  await openAndAsk(page, 'what is an agent');

  await page.locator('.ori-guide__action', { hasText: 'Show me where' }).click();
  const target = page.locator('#newAgentBtn');
  await expect(target).toHaveClass(/is-ori-coachmark/);

  // Read the class and the resolved style together and poll: the class lands
  // before the style recalc, so sampling them separately races.
  await expect
    .poll(() =>
      target.evaluate(el => {
        const cs = getComputedStyle(el);
        return {
          marked: el.classList.contains('is-ori-coachmark'),
          outline: parseFloat(cs.outlineWidth) || 0,
          animation: cs.animationName
        };
      })
    )
    // The outline still identifies the control; only the pulse is dropped, so
    // no meaning is lost (FR-120).
    .toEqual({ marked: true, outline: 3, animation: 'none' });
});

test('the panel is usable at 200% zoom without horizontal page scroll', async ({ page }) => {
  await preparePage(page);
  // 200% zoom at a 1280 logical width behaves like a 640px viewport.
  await page.setViewportSize({ width: 640, height: 720 });
  await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
  await openAndAsk(page, 'what is a workspace');

  await expect(page.locator('#oriGuideSend')).toBeVisible();
  await expect(page.locator('.ori-guide__answer')).toBeVisible();

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth
  );
  expect(overflow).toBeLessThanOrEqual(1);
});

test('the narrow sheet keeps its actions reachable', async ({ page }) => {
  await preparePage(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
  await openAndAsk(page, 'what is a vault');

  const action = page.locator('.ori-guide__action').first();
  await expect(action).toBeVisible();

  // Fully inside the viewport, not clipped off the right edge (FR-13/FR-119).
  const box = await action.boundingBox();
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390 + 1);
});
