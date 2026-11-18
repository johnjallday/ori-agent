import { expect, test } from '@playwright/test';
import {
  assertSandbox,
  capture,
  contrast,
  guardReadTour,
  preparePage,
  reachable
} from './helpers/ui-consistency';
import { homeLeaf, homeReads } from './fixtures/ui-consistency-home';

async function homeA11y(page, selector: string) {
  if (!(await page.evaluate(() => Boolean(window.axe)))) {
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  }
  const result = await page.evaluate(
    async selector =>
      window.axe.run(document.querySelector(selector), {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
      }),
    selector
  );
  expect(result.violations).toEqual([]);
  expect(result.passes.find(rule => rule.id === 'color-contrast')?.nodes.length).toBeGreaterThan(0);
}

for (const theme of ['light', 'dark'] as const) {
  for (const width of [320, 390, 768, 1440]) {
    test(`Home Map/Tree controls, selected portraits and retained capture draft: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      await assertSandbox(request);
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      await homeReads(page);
      await page.goto('/');
      await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');
      await expect(page.locator('#cockpitMap')).toBeVisible();
      await expect(page.locator('#cockpitEconomy')).toBeVisible();
      await expect(page.locator('#cockpitQuestsToggle')).toBeVisible();
      for (const selector of [
        '#cockpitViewMap',
        '#cockpitViewTree',
        '#cockpitCaptureBtn',
        '#cockpitSummaryBtn',
        '#cockpitPrimaryActionBtn',
        '#cockpitCreateWorkspaceBtn',
        '#cockpitRailToggle',
        '#cockpitQuestsToggle',
        '[data-economy-chip="craft"]',
        '[data-economy-chip="harvest"]',
        '[data-economy-energy]'
      ]) {
        // Home uses the shared compact (32px) variant, never a private height;
        // narrow/coarse targets retain the canonical 44px floor.
        await reachable(page.locator(selector), width < 769 ? 44 : 32);
      }
      expect(
        (await contrast(page.locator('#cockpitPrimaryActionBtn'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await homeA11y(page, '.cockpit-area-header');
      const attention = page.locator('[data-cockpit-signal="attention"]');
      await attention.focus();
      await attention.press('Enter');
      await expect(attention).toHaveAttribute('aria-pressed', 'true');
      await expect(attention).toBeFocused();
      await expect(page.locator('.ws-map-tile[data-ws-id="ui-home-reading"]')).toHaveClass(
        /is-filtered-out/
      );
      await attention.press('Enter');
      await expect(attention).toHaveAttribute('aria-pressed', 'false');
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      if (width === 320) {
        const geometry = await page.locator('#cockpitMap').evaluate(node => ({
          top: node.getBoundingClientRect().top,
          nav: document.querySelector('.ori-navbar')!.getBoundingClientRect().bottom
        }));
        expect(
          geometry.top - geometry.nav,
          'compact phone chrome leaves room for the map'
        ).toBeLessThanOrEqual(340);
      }
      await capture(
        page,
        info,
        'home-map',
        {
          fixture:
            'GET-only populated workspace tree, chosen portraits, relationship and economy; not actual hiring'
        },
        { fullPage: false }
      );
      const tile = page.locator(`.ws-map-tile[data-ws-id="${homeLeaf.id}"]`);
      await tile.click();
      const context = page.locator('#cockpitContextModal');
      await expect(context).toBeVisible();
      await expect(page.locator('#cockpitRailContext')).toContainText(homeLeaf.name);
      await expect(page.locator('#cockpitRailContext')).toContainText('literal <angles>');
      await expect(page.locator('#cockpitRailContext .agent-avatar--character')).toHaveCount(1);
      await reachable(context.locator('.btn-close'), width < 769 ? 44 : 40);
      await homeA11y(page, '#cockpitContextModal');
      await capture(page, info, 'home-context', {}, { fullPage: false });
      await page.keyboard.press('Escape');
      await expect(context).toBeHidden();
      await expect(tile).toHaveClass(/is-selected/);
      await page.locator('#cockpitViewTree').click();
      await expect(page.locator('#cockpitTree')).toBeVisible();
      await expect(page.locator('#cockpitMap')).toBeHidden();
      await expect(page.locator('#cockpitSignalFilters')).toBeHidden();
      for (const tool of await page
        .locator('.cockpit-tree-tool:not([disabled]):not([aria-disabled="true"])')
        .all()) {
        await reachable(tool, width < 769 ? 44 : 32);
      }
      await reachable(page.locator('[data-tree-filter]'), width < 769 ? 44 : 40);
      await homeA11y(page, '#cockpitTreeNav');
      await page.locator(`[data-tree-row="${homeLeaf.id}"] .cockpit-tree-name`).click();
      await expect(page.locator('#cockpitTreePane')).toContainText(homeLeaf.name);
      await reachable(page.locator('[data-pane-tab]').first(), width < 769 ? 44 : 40);
      await reachable(page.locator('[data-pane-close]').first(), width < 769 ? 44 : 32);
      await capture(
        page,
        info,
        'home-tree',
        { fixture: 'GET-only tree content and selected pane' },
        { fullPage: false }
      );
      await page.locator('#cockpitViewMap').click();
      await expect(page.locator('#cockpitMap')).toBeVisible();
      await expect(tile).toHaveClass(/is-selected/);
      await page.locator('#cockpitCaptureBtn').click();
      const title = page.locator('#cockpitCaptureTitle');
      await expect(page.locator('#cockpitCaptureModal')).toBeVisible();
      await title.fill('An unsaved fictional research ticket');
      await page.locator('#cockpitCaptureDetailsToggle').click();
      await page
        .locator('#cockpitCaptureDetails')
        .fill('A retained draft. No ticket is created by this tour.');
      await reachable(page.locator('#cockpitCaptureCancel'), width < 769 ? 44 : 40);
      await homeA11y(page, '#cockpitCaptureModal');
      await capture(
        page,
        info,
        'home-capture-draft',
        { fixture: 'local unsent draft; no backlog POST' },
        { fullPage: false }
      );
      await page.keyboard.press('Escape');
      await expect(page.locator('#cockpitCaptureModal')).toBeHidden();
      await expect(page.locator('#cockpitCaptureBtn')).toBeFocused();
      await page.locator('#cockpitCaptureBtn').click();
      await expect(title).toHaveValue('An unsaved fictional research ticket');
      await expect(page.locator('#cockpitCaptureDetails')).toHaveValue(
        'A retained draft. No ticket is created by this tour.'
      );
      await page.locator('#cockpitCaptureCancel').click();
      expect(writes).toEqual([]);
    });
  }

  test(`Home empty first-hire welcome and Tree remain truthful: ${theme}`, async ({
    page,
    request
  }, info) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    await homeReads(page, { empty: true, unhired: true });
    await page.goto('/');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('#cockpitWelcome')).toBeVisible();
    await capture(
      page,
      info,
      'home-first-hire',
      { fixture: 'GET-only needs_hire relationship; welcome retained; no hire or HQ provisioning' },
      { fullPage: false }
    );
    await page.locator('#cockpitViewTree').click();
    await expect(page.locator('#cockpitTree')).toContainText('No workspaces yet');
    await capture(page, info, 'home-empty-tree', {}, { fullPage: false });
    expect(writes).toEqual([]);
  });
}
