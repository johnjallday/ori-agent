import { expect, test } from '@playwright/test';
import {
  assertSandbox,
  capture,
  contrast,
  guardReadTour,
  preparePage,
  reachable
} from './helpers/ui-consistency';
import {
  collectionAgents,
  collectionAgentName,
  collectionReads
} from './fixtures/ui-consistency-agents';

async function clearOfLauncher(control) {
  const overlap = await control.evaluate(node => {
    const launcher = document.querySelector('#personalAssistantLauncher');
    if (!launcher || !launcher.getClientRects().length) return false;
    const a = node.getBoundingClientRect(),
      b = launcher.getBoundingClientRect();
    return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
  });
  expect(overlap, 'the complete control, not only its center, must clear the assistant').toBe(
    false
  );
}

async function collectionA11y(page, selector: string) {
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

async function openCollection(page, request, theme, width, options = {}) {
  await assertSandbox(request);
  await preparePage(page, theme, width);
  const writes = await guardReadTour(page);
  await collectionReads(page, options);
  await page.goto('/agents');
  await expect(page.locator('#rosterLoading')).toBeHidden();
  return writes;
}

for (const theme of ['light', 'dark'] as const) {
  for (const width of [320, 1440]) {
    test(`Agents collection shared controls, first-visit guidance and long inspector: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      const writes = await openCollection(page, request, theme, width);
      await expect(page.locator('.roster-card')).toHaveCount(3);
      for (const agent of collectionAgents) {
        const card = page.locator(`.roster-card[data-name="${agent.name}"]`);
        for (const selector of ['.agent-card__ident .agent-card__class', '.agent-card__status']) {
          expect(
            (await contrast(card.locator(selector))).ratio,
            `${agent.name}: ${selector}`
          ).toBeGreaterThanOrEqual(4.5);
        }
      }
      // Do not suppress the coachmark or assistant to make geometry pass.
      await expect(page.locator('#rosterCoachmark')).toBeVisible();
      await capture(
        page,
        info,
        'agents-first-visit',
        { fixture: 'GET-only identities and relationship; guidance retained' },
        { fullPage: false }
      );
      if (width === 320) {
        const guidance = await page.locator('#rosterCoachmarkCopy').evaluate(node => ({
          top: node.getBoundingClientRect().top,
          nav: document.querySelector('.ori-navbar')!.getBoundingClientRect().bottom
        }));
        expect(guidance.top).toBeGreaterThanOrEqual(guidance.nav);
      }
      await collectionA11y(page, '.roster-layout');
      await page.locator('#rosterSearch').focus();
      for (const selector of [
        '#rosterSearch',
        '#newAgentBtn',
        '#viewGallery',
        '#viewList',
        '#viewMap',
        '#rosterSort',
        '#rosterGroup',
        '#filtersButton'
      ]) {
        await reachable(page.locator(selector), width < 769 ? 44 : 40);
        await clearOfLauncher(page.locator(selector));
      }
      expect((await page.locator('#rosterSearch').boundingBox())!.width).toBeGreaterThanOrEqual(
        Math.min(250, width - 64)
      );
      expect((await contrast(page.locator('#rosterSearch'))).ratio).toBeGreaterThanOrEqual(4.5);
      await capture(page, info, 'agents-toolbar', {}, { fullPage: false });
      const card = page.locator(`.roster-card[data-name="${collectionAgentName}"]`);
      if (width < 769) {
        await reachable(card.locator('.roster-card__checkwrap'), 44);
        expect(
          (await card.locator('.roster-card__check').boundingBox())!.height
        ).toBeLessThanOrEqual(18);
      }
      await card.locator('.roster-card__open').click();
      expect((await contrast(card, '::after')).ratio).toBeGreaterThanOrEqual(3);
      await expect(page.locator('#stageName')).toHaveText(collectionAgentName);
      await expect(page.locator('#overviewFacts')).toContainText('Literal <angles>');
      await expect(page.locator('#overviewFacts')).toContainText(collectionAgents[0].model);
      const nameBounds = await page.locator('#stageName').evaluate(node => ({
        width: node.scrollWidth,
        client: node.clientWidth,
        parent: node.parentElement!.getBoundingClientRect().width
      }));
      expect(nameBounds.width).toBeLessThanOrEqual(nameBounds.parent + 1);
      for (const selector of [
        '#stageFullPage',
        '#stageFavoriteToggle',
        '#stageDelete',
        '#inspectorClose'
      ]) {
        await reachable(page.locator(selector), width < 769 ? 44 : 40);
        if (width === 1440) await clearOfLauncher(page.locator(selector));
      }
      if (width === 1440) {
        await expect
          .poll(async () =>
            page
              .locator('.inspector-scroll')
              .evaluate(
                node =>
                  node.getBoundingClientRect().bottom -
                  document.querySelector('#personalAssistantLauncher')!.getBoundingClientRect().top
              )
          )
          .toBeLessThanOrEqual(0);
      }
      expect((await contrast(page.locator('#stageFullPage'))).ratio).toBeGreaterThanOrEqual(4.5);
      await collectionA11y(page, '#inspector');
      await capture(page, info, 'agents-inspector', {}, { fullPage: false });
      await page.locator('#tab-prompt').click();
      await expect(page.locator('#panel-prompt pre')).toContainText(
        'Literal <markup> is not HTML.'
      );
      await expect(page.locator('#panel-prompt markup')).toHaveCount(0);
      await expect(page.locator('#tab-prompt')).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('#tab-prompt')).toHaveClass(/(?:^|\s)active(?:\s|$)/);
      await expect(page.locator('#tab-overview')).not.toHaveClass(/(?:^|\s)active(?:\s|$)/);
      await page.locator('#inspectorClose').click();
      await expect(card.locator('.roster-card__open')).toBeFocused();
      expect(writes).toEqual([]);
    });
  }

  for (const width of [390, 768]) {
    test(`Agents inspector sheet traps focus and restores the real card: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      const writes = await openCollection(page, request, theme, width);
      const card = page.locator(
        `.roster-card[data-name="${collectionAgentName}"] .roster-card__open`
      );
      await card.click();
      await expect(page.locator('#inspector')).toHaveAttribute('aria-modal', 'true');
      await expect(page.locator('.collection-pane')).toHaveJSProperty('inert', true);
      const close = page.locator('#inspectorClose');
      await expect(close).toBeFocused();
      await close.press('Shift+Tab');
      expect(
        await page.evaluate(() =>
          document.querySelector('#inspector')!.contains(document.activeElement)
        )
      ).toBe(true);
      await page.locator('#rosterSearch').evaluate(node => (node as HTMLInputElement).focus());
      expect(
        await page.evaluate(() =>
          document.querySelector('#inspector')!.contains(document.activeElement)
        )
      ).toBe(true);
      if (width === 768) {
        await page.setViewportSize({ width: 1440, height: 900 });
        await expect(page.locator('#inspector')).not.toHaveAttribute('aria-modal', 'true');
        await expect(page.locator('.collection-pane')).toHaveJSProperty('inert', false);
        await page.locator('#rosterSearch').focus();
        await page.setViewportSize({ width: 768, height: 900 });
        await expect(page.locator('#stageName')).toHaveText(collectionAgentName);
        await expect(close).toBeFocused();
      }
      await page.keyboard.press('Escape');
      await expect(page.locator('#inspector')).toBeHidden();
      await expect(page.locator('.collection-pane')).toHaveJSProperty('inert', false);
      await expect(card).toBeFocused();
      await capture(page, info, 'agents-inspector-restored', {}, { fullPage: false });
      expect(writes).toEqual([]);
    });
  }

  test(`Agents filters fit 200% reflow and retain status-button focus: ${theme}`, async ({
    page,
    request
  }, info) => {
    const writes = await openCollection(page, request, theme, 720);
    await page.setViewportSize({ width: 720, height: 450 });
    const health = page.locator('.roster-stat[data-health="needs"]');
    await health.focus();
    await health.press('Enter');
    await expect(health).toHaveAttribute('aria-pressed', 'true');
    await expect(health).toBeFocused();
    await page.locator('.collection-chip[data-quick="all"]').click();
    const filters = page.locator('#filtersButton');
    await filters.click();
    const panel = page.locator('#filtersPanel');
    const bounds = await panel.boundingBox();
    expect(bounds!.y).toBeGreaterThanOrEqual(0);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(451);
    await reachable(page.locator('#filterRole'), 44);
    await page.locator('#filterRole').selectOption('researcher');
    await reachable(page.locator('#filtersDone'), 44);
    await capture(
      page,
      info,
      'agents-filters',
      { zoomEquivalent: '1440×900 at 200%; 720×450 CSS viewport' },
      { fullPage: false }
    );
    await collectionA11y(page, '#filtersPanel');
    await page.keyboard.press('Escape');
    await expect(filters).toBeFocused();
    await expect(page.locator('#rosterCount')).toContainText('1');
    expect(writes).toEqual([]);
  });

  test(`Agents native bulk dialogs preserve selection and cancel without writes: ${theme}`, async ({
    page,
    request
  }, info) => {
    const writes = await openCollection(page, request, theme, 320);
    const card = page.locator(`.roster-card[data-name="${collectionAgentName}"]`);
    await card.locator('.roster-card__check').check();
    await expect(page.locator('#bulkCount')).toContainText('1 selected');
    for (const [trigger, dialog, cancel] of [
      ['#bulkDelete', '#bulkDeleteDialog', '#bulkDeleteCancel'],
      ['#bulkSetRole', '#bulkTagsDialog', '#bulkTagsCancel'],
      ['#bulkAddTags', '#bulkTagsDialog', '#bulkTagsCancel']
    ]) {
      await reachable(page.locator(trigger), 44);
      await clearOfLauncher(page.locator(trigger));
      await page.locator(trigger).click();
      await expect(page.locator(dialog)).toBeVisible();
      await reachable(page.locator(cancel), 44);
      await capture(
        page,
        info,
        trigger === '#bulkDelete' ? 'agents-delete-cancel' : 'agents-bulk-fields',
        { trigger, fixture: 'preview/cancel only' },
        { fullPage: false }
      );
      await collectionA11y(page, dialog);
      await page.keyboard.press('Escape');
      await expect(page.locator(trigger)).toBeFocused();
      await expect(card.locator('.roster-card__check')).toBeChecked();
    }
    expect(writes).toEqual([]);
  });

  test(`Agents empty/error/retry and native list/map views stay explicit: ${theme}`, async ({
    page,
    request
  }, info) => {
    const options = { fail: true, empty: false };
    const writes = await openCollection(page, request, theme, 390, options);
    await expect(page.locator('#rosterError')).toBeVisible();
    await expect(page.locator('#rosterCount')).toHaveText('Agents could not be loaded.');
    options.fail = false;
    options.empty = true;
    await page.locator('#rosterRetry').click();
    await expect(page.locator('#rosterEmpty')).toBeVisible();
    await capture(page, info, 'agents-empty', {}, { fullPage: false });
    // A new declared GET scene, not a successful mutation, supplies the records.
    await collectionReads(page);
    await page.reload();
    await expect(page.locator('.roster-card')).toHaveCount(3);
    await page.locator('#viewList').click();
    await expect(page.locator('#rosterList')).toHaveClass(/is-list/);
    await page.locator('#viewMap').click();
    await expect(page.locator('#rosterMap')).toBeVisible();
    await expect(page.locator('#viewMap')).toHaveAttribute('aria-pressed', 'true');
    await capture(
      page,
      info,
      'agents-map',
      { fixture: 'same GET identities; no membership drag/drop' },
      { fullPage: false }
    );
    expect(writes).toEqual([]);
  });
}
