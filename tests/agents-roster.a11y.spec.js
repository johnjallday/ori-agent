import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Accessibility regression for the Agents Gallery/Inspector, in both themes.
// Mirrors tests/workspace-detail.a11y.spec.js: axe-core is loaded from a CDN and
// scoped to the roster layout so pre-existing chrome (navbar/sidebar) doesn't
// mask regressions in this component.
const baseUrl = process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';

async function runAxe(page, target) {
  return page.evaluate(async selector => {
    const root = selector ? document.querySelector(selector) : document;
    return await window.axe.run(root, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
  }, target);
}

for (const theme of ['light', 'dark']) {
  test(`agents roster accessibility (${theme})`, async ({ page, request }) => {
    const name = `PW A11y ${theme} ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      await installLocalCdn(page);
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

      await page.setViewportSize({ width: 1440, height: 950 });
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

      const results = await runAxe(page, '.roster-layout');
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);

      // Walk all four Inspector tabs; each panel becomes visible and gets its
      // own axe pass, catching a violation confined to one tab's content.
      const tabs = ['tab-overview', 'tab-prompt', 'tab-workspaces', 'tab-toolbox'];
      for (const tabId of tabs) {
        await page.locator(`#${tabId}`).click();
        await expect(page.locator(`#${tabId}`)).toHaveAttribute('aria-selected', 'true');
        const panelId = await page.locator(`#${tabId}`).getAttribute('aria-controls');
        await expect(page.locator(`#${panelId}`)).toBeVisible();
        const tabResults = await runAxe(page, '#inspector');
        expect(
          tabResults.violations,
          `${tabId}: ` + JSON.stringify(tabResults.violations, null, 2)
        ).toEqual([]);
      }

      // Roving tab stop: only the selected tab is in the Tab order.
      for (const tabId of tabs) {
        const expectedTi =
          (await page.locator(`#${tabId}`).getAttribute('aria-selected')) === 'true' ? '0' : '-1';
        await expect(page.locator(`#${tabId}`)).toHaveAttribute('tabindex', expectedTi);
      }

      // Left/Right/Home/End keyboard navigation over the tablist.
      await page.locator('#tab-toolbox').focus();
      await page.keyboard.press('ArrowLeft');
      await expect(page.locator('#tab-workspaces')).toHaveAttribute('aria-selected', 'true');
      await page.keyboard.press('Home');
      await expect(page.locator('#tab-overview')).toHaveAttribute('aria-selected', 'true');
      await page.keyboard.press('End');
      await expect(page.locator('#tab-toolbox')).toHaveAttribute('aria-selected', 'true');

      // Also scan the bulk-delete confirmation dialog (focus-trapped modal).
      await page.locator('#bulkDelete').click();
      await expect(page.locator('#bulkDeleteDialog')).toBeVisible();
      const dialogResults = await runAxe(page, '#bulkDeleteDialog');
      expect(dialogResults.violations, JSON.stringify(dialogResults.violations, null, 2)).toEqual(
        []
      );
      await page.locator('#bulkDeleteCancel').click();
      await expect(page.locator('#bulkDeleteDialog')).toBeHidden();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test(`agents mobile inspector sheet accessibility (${theme})`, async ({ page, request }) => {
    const name = `PW A11y Sheet ${theme} ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      await installLocalCdn(page);
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

      await page.setViewportSize({ width: 375, height: 812 });
      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();
      // Nothing opened yet: the sheet must not be covering the collection at
      // rest, and it is the resting collection that gets scanned first.
      await expect(page.locator('#inspector')).toBeHidden();

      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const restResults = await runAxe(page, '.roster-layout');
      expect(restResults.violations, JSON.stringify(restResults.violations, null, 2)).toEqual([]);

      await page.locator('#rosterSearch').fill(name);
      const opener = page.locator(`.roster-card[data-name="${name}"] .roster-card__open`);
      await opener.click();

      const inspector = page.locator('#inspector');
      await expect(inspector).toBeVisible();
      await expect(inspector).toHaveAttribute('role', 'dialog');
      await expect(inspector).toHaveAttribute('aria-modal', 'true');

      const sheetResults = await runAxe(page, '#inspector');
      expect(sheetResults.violations, JSON.stringify(sheetResults.violations, null, 2)).toEqual([]);

      // Escape closes the sheet before anything else can act on it, and focus
      // returns to the exact control that opened it.
      await page.keyboard.press('Escape');
      await expect(inspector).toBeHidden();
      await expect(opener).toBeFocused();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  // The Appearance editor is the newest interactive surface in the Inspector,
  // and the one most able to regress into something a keyboard cannot drive:
  // three choices, a colour swatch, a file input, and a dialog-opening button
  // (unified-agent-appearance FR-96 through FR-100).
  test(`appearance editor accessibility (${theme})`, async ({ page, request }) => {
    const name = `PW A11y Appearance ${theme} ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      await installLocalCdn(page);
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
      await page.setViewportSize({ width: 1440, height: 950 });
      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded'
      });
      await expect(page.locator('#ov-appearance-root')).toBeVisible();

      // The three choices are one programmatically-labelled group, not three
      // loose radios (FR-96).
      const group = page.locator('#ov-appearance-root [role="radiogroup"]');
      await expect(group).toHaveAttribute('aria-label', 'Appearance source');
      await expect(group.locator('input[type="radio"]')).toHaveCount(3);
      await expect(page.locator('#ov-appearance-root legend')).toHaveText('Appearance');

      // Unavailable sources say why in text, so the state does not depend on
      // the disabled attribute or a colour alone (FR-100).
      await expect(page.locator('#ov-appearance-root')).toContainText(
        'Choose a character to use this source.'
      );
      await expect(page.locator('#ov-appearance-root')).toContainText(
        'Upload an image to use this source.'
      );

      // Every control is reachable and operable from the keyboard (FR-97).
      await page.locator('#ov-appearance-mode-generated').focus();
      await expect(page.locator('#ov-appearance-mode-generated')).toBeFocused();
      for (const id of ['ov-appearance-color', 'ov-appearance-character-choose']) {
        await page.locator(`#${id}`).focus();
        await expect(page.locator(`#${id}`)).toBeFocused();
      }

      // The preview is a standalone image, so it names its source rather than
      // being decorative like an avatar sitting beside a name (FR-99).
      const preview = page.locator('#ov-appearance-root [role="img"]');
      await expect(preview).toHaveAttribute('aria-label', /^Preview: /);
      // The portrait inside it stays decorative, so the source is announced
      // once rather than twice.
      await expect(preview.locator('.agent-avatar')).toHaveAttribute('aria-hidden', 'true');

      // Status is a live region, so a save or a failure is announced.
      await expect(page.locator('#ov-appearance-status')).toHaveAttribute('role', 'status');
      await expect(page.locator('#ov-appearance-status')).toHaveAttribute('aria-live', 'polite');

      // axe over the editor itself, which includes colour-contrast for the
      // cards, the selected state, and the unavailable copy (FR-101).
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const results = await runAxe(page, '#ov-appearance-root');
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);

      // Opening the picker from the editor and cancelling returns focus to the
      // control that opened it (FR-98).
      await page.locator('#ov-appearance-character-choose').click();
      await expect(page.locator('#charPicker')).toBeVisible();
      const pickerResults = await runAxe(page, '#charPicker');
      expect(pickerResults.violations, JSON.stringify(pickerResults.violations, null, 2)).toEqual(
        []
      );
      await page.keyboard.press('Escape');
      await expect(page.locator('#charPicker')).toBeHidden();
      await expect(page.locator('#ov-appearance-character-choose')).toBeFocused();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });
}
