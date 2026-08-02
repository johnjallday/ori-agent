import { test, expect, type Page } from '@playwright/test';

/**
 * Accessibility coverage for the Workshop and Goal Prepare surfaces
 * (task 6.15; PRD FR-162–FR-165, FR-167).
 *
 * These check the properties a unit test cannot: that a real browser gives
 * every control a focusable, named, reachable presence, and that the page does
 * not depend on color or motion to say what is going on.
 *
 * Run against the isolated demo sandbox:
 *   ./scripts/e2e.sh tests/toolbox-workshop.a11y.spec.ts
 */

const WORKSPACE_NAME = 'Toolbox Demo';

async function openToolbox(page: Page) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );

  const listing = await page.request.get('/api/workspaces');
  const payload = await listing.json();
  const folders = payload.folders ?? payload.workspaces ?? [];
  const workspace = folders.find((entry: { name?: string }) => entry?.name === WORKSPACE_NAME);
  test.skip(!workspace, `needs a workspace named "${WORKSPACE_NAME}" in the sandbox`);

  await page.goto(`/workspaces/${workspace.id}`);
  await page.locator('[data-cmd-agent-tab="loadout"]').first().click();
  const panel = page.locator('#workspace-toolbox-panel');
  await expect(panel).toBeVisible({ timeout: 15000 });
  return panel;
}

test.describe('Workshop accessibility', () => {
  test('every control is keyboard reachable with a visible focus ring (FR-163)', async ({
    page
  }) => {
    const panel = await openToolbox(page);
    await panel.locator('[data-toolbox-edit]').click();

    const controls = panel.locator('button:not([disabled])');
    const count = await controls.count();
    expect(count).toBeGreaterThan(0);

    // Tab to each control rather than calling .focus() directly: Chromium's
    // :focus-visible heuristic only shows the ring for real keyboard-driven
    // focus, so a scripted .focus() call produces a false "no ring" reading
    // even though the CSS rule (button:focus-visible) is correct.
    await page.keyboard.press('Tab');
    for (let i = 0; i < Math.min(count, 12); i++) {
      const control = controls.nth(i);
      for (let attempts = 0; attempts < 40; attempts++) {
        const isControl = await control.evaluate(node => node === document.activeElement);
        if (isControl) break;
        await page.keyboard.press('Tab');
      }
      await expect(control).toBeFocused();

      // A focused control must be visually distinguishable. Checking the
      // computed outline catches the common regression: a reset that removes
      // the ring and leaves keyboard users with no idea where they are.
      const outline = await control.evaluate(node => {
        const style = window.getComputedStyle(node);
        return {
          width: style.outlineWidth,
          style: style.outlineStyle,
          shadow: style.boxShadow
        };
      });
      const visible =
        (outline.style !== 'none' && outline.width !== '0px') ||
        (outline.shadow && outline.shadow !== 'none');
      expect(visible, `control ${i} has no visible focus indicator`).toBeTruthy();
      await page.keyboard.press('Tab');
    }
  });

  test('status is readable without color (FR-162)', async ({ page }) => {
    const panel = await openToolbox(page);

    // Force greyscale. Anything that survives is carried by text, which is the
    // requirement — color may reinforce a state, never carry it alone.
    await page.addStyleTag({ content: 'html { filter: grayscale(1) !important; }' });

    const text = await panel.innerText();
    // Capacity, connection state, and source grouping all read as words.
    expect(text).toMatch(/skill spaces|active skills/i);
    expect(text).toMatch(/Connected|Not connected|Always on/i);
    expect(text).toMatch(/From this workspace|Core|From this agent/i);
  });

  test('exactly one live region, and it is polite until something fails (FR-164)', async ({
    page
  }) => {
    const panel = await openToolbox(page);

    const regions = panel.locator('.ws-toolbox-live');
    await expect(regions).toHaveCount(1);
    await expect(regions.first()).toHaveAttribute('aria-live', 'polite');

    // The visible copies must NOT also be live, or a result is announced twice.
    const alerts = panel.locator('.ws-toolbox-error[role="alert"]');
    await expect(alerts).toHaveCount(0);
  });

  test('reduced motion is honored (FR-165)', async ({ page }) => {
    const panel = await openToolbox(page);

    // emulateMedia set prefers-reduced-motion: reduce in openToolbox. No card
    // or control may animate under it. The site-wide reduced-motion reset
    // (common.css) collapses every transition/animation to 0.01ms rather than
    // 0s — a deliberate near-zero, not truly zero, so that transitionend /
    // animationend listeners still fire — so the threshold here is "well
    // under one visible frame," not "exactly zero."
    const NEAR_INSTANT_SECONDS = 0.05;
    const animated = await panel.evaluate((host, threshold) => {
      const offenders: string[] = [];
      for (const node of Array.from(host.querySelectorAll('*'))) {
        const style = window.getComputedStyle(node);
        const hasTransition =
          style.transitionDuration &&
          style.transitionDuration.split(',').some(value => parseFloat(value) > threshold);
        const hasAnimation =
          style.animationDuration &&
          style.animationDuration.split(',').some(value => parseFloat(value) > threshold);
        if (hasTransition || hasAnimation) offenders.push(node.className || node.tagName);
      }
      return offenders;
    }, NEAR_INSTANT_SECONDS);
    expect(animated, `these animate under reduced motion: ${animated.join(', ')}`).toEqual([]);
  });

  test('the panel never forces the page to scroll sideways', async ({ page }) => {
    await openToolbox(page);

    const overflows = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1
    );
    expect(overflows, 'the page scrolls horizontally').toBeFalsy();
  });
});
