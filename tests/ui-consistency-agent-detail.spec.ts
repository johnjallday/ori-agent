import { test, expect } from '@playwright/test';
import { assertSandbox, capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import { guardAgentCreationReads } from './fixtures/ui-consistency-agent-create';
import {
  detailReads,
  detailAgentName,
  alternativeDetailModel
} from './fixtures/ui-consistency-agent-detail';

async function detailA11y(page, selector: string) {
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
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

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440]) {
    for (const variant of ['ordinary', 'claude', 'codex']) {
      test(`Agent detail shared hierarchy and native boundaries: ${variant} ${theme} ${width}`, async ({
        page,
        request
      }, info) => {
        await assertSandbox(request);
        await preparePage(page, theme, width);
        const unexpected = await guardAgentCreationReads(page);
        await detailReads(page);
        const name =
          variant === 'ordinary' ? detailAgentName : variant === 'claude' ? 'Claude Code' : 'Codex';
        await page.goto('/agents/' + encodeURIComponent(name));
        if (variant === 'ordinary') {
          await expect(page.locator('#agentName')).toHaveText(name);
          await expect(page.locator('#agentAvatar .agent-avatar--character')).toBeVisible();
          expect((await contrast(page.locator('#deleteButton'))).ratio).toBeGreaterThanOrEqual(4.5);
          const panel = page.locator('.agent-appearance-panel');
          if (width === 1440) expect((await panel.boundingBox())!.height).toBeLessThanOrEqual(370);
          await capture(
            page,
            info,
            'agent-detail-header',
            { fixture: 'GET-only; no save, delete, membership or execution' },
            { fullPage: false }
          );
          await page
            .locator('#appearanceSection')
            .evaluate(node => node.scrollIntoView({ block: 'start' }));
          await expect(page.locator('#agentAppearanceHost .agent-avatar--character')).toBeVisible();
          await capture(page, info, 'agent-detail-appearance', {}, { fullPage: false });
          const upload = page.locator('#agentAppearance-file');
          await upload.evaluate(node =>
            node.scrollIntoView({ block: 'center', behavior: 'instant' })
          );
          await reachable(upload, width < 769 ? 44 : 40);
          if (width === 320 || width === 1440) await detailA11y(page, '#content');
          await page.locator('#editPromptBtn').click();
          await page
            .locator('#editSystemPrompt')
            .fill('An unsent prompt with literal <instructions>.');
          await page.locator('#editConfigBtn').click();
          await page.locator('#editTemperature').fill('0.7');
          const model = page.locator('#editModel');
          // Both engines ignore option-arrow keys even on a blank native
          // select in this host's headless runtime (retained health probe).
          // Exercise native selection/change through Playwright's select API;
          // do not add product keyboard shims or claim OS-popup certification.
          await model.selectOption(alternativeDetailModel);
          await model.focus();
          await expect(model).toBeFocused();
          await page.keyboard.press('Tab');
          await expect(page.locator('#editTemperature')).toBeFocused();
          await expect(model).toHaveValue(alternativeDetailModel);
          await page.locator('#editProfileButton').click();
          await expect(page.locator('#profileEditModal')).toBeVisible();
          await page
            .locator('#profileEditModal input[type="text"]')
            .first()
            .fill('Unsent profile draft');
          await capture(page, info, 'agent-detail-profile', {}, { fullPage: false });
          if (width === 320 || width === 1440) await detailA11y(page, '#profileEditModal');
          // Cancel with a dirty draft still belongs to the existing discard flow.
          await page
            .locator('#profileEditModal')
            .getByRole('button', { name: 'Cancel', exact: true })
            .click();
          await expect(page.locator('#profileDiscardConfirm')).toBeVisible();
          await page
            .locator('#profileDiscardConfirm')
            .getByRole('button', { name: /Keep editing/i })
            .click();
          await expect(page.locator('#profileEditModal input[type="text"]').first()).toHaveValue(
            'Unsent profile draft'
          );
          await expect(page.locator('#editSystemPrompt')).toHaveValue(
            'An unsent prompt with literal <instructions>.'
          );
          await expect(page.locator('#editTemperature')).toHaveValue('0.7');
          await expect(model).toHaveValue(alternativeDetailModel);
        } else {
          await expect(page.locator(`#${variant}DetailTitle`)).toHaveText(name);
          await expect(page.locator(`.${variant}-sync-list`)).toHaveCount(4);
          const main = page.locator(`#${variant}-detail-main`);
          await expect(main).toContainText('Fictional');
          await reachable(page.locator(`.${variant}-detail-back`), width < 769 ? 44 : 40);
          await reachable(page.locator(`.${variant}-sync-refresh`), width < 769 ? 44 : 40);
          await capture(
            page,
            info,
            `agent-detail-${variant}`,
            {
              fixture:
                'real dedicated renderer; fictional native metadata; Refresh/mirroring/account setup NOT activated'
            },
            { fullPage: false }
          );
          if (width === 320 || width === 1440) await detailA11y(page, `#${variant}-detail-main`);
        }
        const horizontal = await page.evaluate(() => ({
          width: document.documentElement.scrollWidth,
          viewport: innerWidth,
          scrollX,
          bodyWidth: document.body.scrollWidth,
          bodyClient: document.body.clientWidth,
          overflowing: [...document.querySelectorAll('body *')]
            .filter(node => node.getClientRects().length && node.scrollWidth > node.clientWidth + 2)
            .slice(0, 15)
            .map(node => ({
              id: node.id,
              classes: node.className,
              tag: node.tagName,
              client: node.clientWidth,
              scroll: node.scrollWidth,
              left: node.getBoundingClientRect().left,
              overflow: getComputedStyle(node).overflowX
            })),
          outside: [...document.querySelectorAll('body *')]
            .filter(
              node =>
                node.getClientRects().length && node.getBoundingClientRect().right > innerWidth + 1
            )
            .slice(0, 12)
            .map(node => ({
              id: node.id,
              tag: node.tagName,
              classes: node.className,
              width: node.getBoundingClientRect().width,
              right: node.getBoundingClientRect().right,
              scroll: node.scrollWidth,
              text: node.textContent?.slice(0, 60)
            }))
        }));
        expect(horizontal.width, JSON.stringify(horizontal)).toBeLessThanOrEqual(
          horizontal.viewport
        );
        expect(unexpected).toEqual([]);
      });
    }
  }
