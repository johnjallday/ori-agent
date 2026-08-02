import { test, expect } from '@playwright/test';

// Accessibility regression for the Agents roster/stage, in both themes.
// Mirrors tests/workspace-detail.a11y.spec.js: axe-core is loaded from a CDN and
// scoped to the roster layout so pre-existing chrome (navbar/sidebar) doesn't
// mask regressions in this component.
const baseUrl = process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';

for (const theme of ['light', 'dark']) {
  test(`agents roster accessibility (${theme})`, async ({ page, request }) => {
    const name = `PW A11y ${theme} ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      await page.emulateMedia({ reducedMotion: 'reduce' });
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

      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded'
      });
      await expect(page.locator('#stageName')).toHaveText(name);
      await expect(page.locator('#overviewFacts .stage-form')).toBeVisible();

      // Exercise the bulk surfaces so axe covers checked cards, the revealed
      // action bar, and the filter controls, not just the resting roster.
      await page.locator(`.roster-card[data-name="${name}"] .roster-card__check`).check();
      await expect(page.locator('#bulkBar')).toBeVisible();

      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const results = await page.evaluate(async () => {
        const root = document.querySelector('.roster-layout');
        return await window.axe.run(root, {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
      });
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);

      // Also scan the bulk-delete confirmation dialog (focus-trapped modal).
      await page.locator('#bulkDelete').click();
      await expect(page.locator('#bulkDeleteDialog')).toBeVisible();
      const dialogResults = await page.evaluate(async () => {
        return await window.axe.run(document.querySelector('#bulkDeleteDialog'), {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
      });
      expect(dialogResults.violations, JSON.stringify(dialogResults.violations, null, 2)).toEqual(
        []
      );
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });
}
