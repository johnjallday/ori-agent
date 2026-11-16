import { expect, test } from '@playwright/test';
import { assertSandbox, capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import { agentCreateReads, guardAgentCreationReads } from './fixtures/ui-consistency-agent-create';

async function createA11y(page, selector: string) {
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
    test(`Hire/Create shared controls, role focus, drafts and validation: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      await assertSandbox(request);
      await preparePage(page, theme, width);
      const unexpected = await guardAgentCreationReads(page);
      await agentCreateReads(page);
      await page.goto('/agents/create');
      const role = page.locator('#hireCardHost [data-slug="researcher"]');
      await role.click();
      await expect(role).toBeFocused();
      await expect(role).toHaveAttribute('aria-checked', 'true');
      await page.keyboard.press('ArrowRight');
      await expect(page.locator('#hireCardHost [data-slug="analyzer"]')).toBeFocused();
      await page.locator('#pageHireName').fill('UI draft <literal>');
      await page.locator('#pageHireBrief').fill('Keep this brief while changing roles.');
      await page
        .locator('#hireCardHost')
        .getByRole('radio', { name: /^Writer / })
        .click();
      await expect(page.locator('#pageHireName')).toHaveValue('UI draft <literal>');
      await expect(page.locator('#pageHireBrief')).toHaveValue(
        'Keep this brief while changing roles.'
      );
      await capture(
        page,
        info,
        'agent-create-hire',
        { fixture: 'GET-only; no Hire/start or auto-configuration' },
        { fullPage: false }
      );
      await page.locator('#customTabBtn').click();
      const name = page.locator('#agentName');
      await expect(name).not.toHaveAttribute('aria-invalid', 'true');
      expect((await contrast(page.locator('label[for="agentName"]'))).ratio).toBeGreaterThanOrEqual(
        4.5
      );
      expect(
        await page.locator('label[for="createConfigModeManual"]').evaluate(node => {
          const range = document.createRange();
          range.selectNodeContents(node);
          return range.getClientRects().length;
        }),
        'the short Manual label must not split within its word'
      ).toBe(1);
      await name.fill('UI draft retained');
      await page.locator('#systemPrompt').fill('Literal <angles> remain instructions, not HTML.');
      await page.locator('#catalogTabBtn').click();
      await page.locator('#customTabBtn').click();
      await expect(name).toHaveValue('UI draft retained');
      await expect(page.locator('#systemPrompt')).toHaveValue(
        'Literal <angles> remain instructions, not HTML.'
      );
      const choose = page.locator('#createAppearance-character-choose');
      await reachable(choose, width < 769 ? 44 : 40);
      await choose.click();
      await expect(page.locator('#charPicker')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(choose).toBeFocused();
      const tag = 'Literal<angles>' + '-long-label'.repeat(12);
      await page.locator('#tagsInput').fill(tag);
      await page.locator('#tagsInput').press('Enter');
      const removeTag = page.getByRole('button', { name: `Remove tag ${tag}`, exact: true });
      // This is an ordinary scrollable form, not a fixed footer: reveal the
      // mid-page chip without dismissing the real assistant over its lower edge.
      await removeTag.evaluate(node =>
        node.scrollIntoView({ block: 'center', behavior: 'instant' })
      );
      await reachable(removeTag, width < 769 ? 44 : 32);
      await removeTag.click();
      await expect(page.locator('#tagsInput')).toBeFocused();
      await name.fill('');
      await page.locator('#createBtn').click();
      await expect(name).toBeFocused();
      await expect(name).toHaveAttribute('aria-invalid', 'true');
      await expect(page.locator('#agentNameError')).toContainText('Enter an agent name');
      await capture(page, info, 'agent-create-invalid', {}, { fullPage: false });
      if (width === 320 || width === 1440) await createA11y(page, '#customTabPanel');
      await page.goto('/agents');
      await expect(page.locator('.roster-card')).toHaveCount(3);
      await page.locator('#newAgentBtn').click();
      await expect(page.locator('#agentHirePanel .agent-hire-card')).toBeVisible();
      await page.locator('#agentCreateAdvancedTab').click();
      const modalName = page.locator('#addAgentForm #agentName');
      await expect(modalName).not.toHaveAttribute('aria-invalid', 'true');
      await modalName.fill('Modal draft retained');
      await page
        .locator('#agentSystemPrompt')
        .fill('Do not discard these instructions on a tab switch.');
      await page.locator('#agentCreateHireTab').click();
      await page.locator('#agentCreateAdvancedTab').click();
      await expect(modalName).toHaveValue('Modal draft retained');
      await expect(page.locator('#agentSystemPrompt')).toHaveValue(
        'Do not discard these instructions on a tab switch.'
      );
      await capture(page, info, 'agent-create-modal-advanced', {}, { fullPage: false });
      if (width === 320 || width === 1440) await createA11y(page, '#addAgentModal');
      await page.keyboard.press('Escape');
      await expect(page.locator('#addAgentModal')).toBeHidden();
      await expect(page.locator('#newAgentBtn')).toBeFocused();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true
      );
      expect(unexpected).toEqual([]);
    });

    test(`First hire model and nested appearance picker stay presentation-only: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      await assertSandbox(request);
      await preparePage(page, theme, width);
      const unexpected = await guardAgentCreationReads(page);
      await agentCreateReads(page, { unhired: true });
      await page.goto('/agents');
      await page.locator('#newAgentBtn').click();
      await expect(page.locator('#cr-name')).toBeVisible();
      await expect(page.locator('#cr-model')).toBeEnabled();
      await page.locator('#cr-name').fill('Atlas <literal>');
      await expect(page.locator('#cr-model-heading')).toContainText('Atlas <literal>');
      await expect(page.locator('#cr-model-scope')).toContainText('default for new agents');
      await capture(
        page,
        info,
        'agent-first-hire-model',
        { fixture: 'GET-only fictional model metadata; no default-model save, hire or HQ setup' },
        { fullPage: false }
      );
      await reachable(page.locator('#cr-more summary'), width < 769 ? 44 : 40);
      await page.locator('#cr-more summary').click();
      const choose = page.locator('#cr-appearance-character-choose');
      await choose.click();
      await expect(page.locator('#charPicker')).toBeVisible();
      await expect(page.locator('#charPickerSearch')).toBeFocused();
      expect(await page.locator('#charPicker').evaluate(node => node.matches(':modal'))).toBe(true);
      await page.locator('#cr-name').evaluate(node => node.focus());
      await expect(page.locator('#charPickerSearch')).toBeFocused();
      await reachable(page.locator('#charPickerSearch'), width < 769 ? 44 : 40);
      for (const selector of ['#charPickerClose', '#charPickerCancel', '#charPickerConfirm']) {
        await reachable(page.locator(selector), width < 769 ? 44 : 40);
      }
      for (const filter of await page.locator('.char-picker__family').all()) {
        await reachable(filter, width < 769 ? 44 : 32);
      }
      await page.locator('#charPickerClose').focus();
      await page.keyboard.press('Shift+Tab');
      await expect(page.locator('#charPickerConfirm')).toBeFocused();
      await page.keyboard.press('Tab');
      expect(
        await page.locator('#charPicker').evaluate(node => node.contains(document.activeElement))
      ).toBe(true);
      const radios = page.locator('#charPicker [data-char-id]');
      await radios.first().focus();
      await page.keyboard.press('Home');
      await expect(radios.first()).toBeFocused();
      await page.keyboard.press('ArrowDown');
      await expect(radios.nth(1)).toBeFocused();
      await expect(radios.nth(1)).toHaveAttribute('aria-checked', 'true');
      await expect(page.locator('#charPicker [data-char-id][tabindex="0"]')).toHaveCount(1);
      const character = page.locator('#charPicker [data-char-id="research-archivist"]');
      const characterName = await character.locator('.char-card__name').innerText();
      await character.click();
      await expect(character).toBeFocused();
      await expect(character).toHaveAttribute('aria-checked', 'true');
      for (const card of await radios.all()) {
        const contentFits = await card.evaluate(node => {
          const bounds = node.getBoundingClientRect();
          return [...node.querySelectorAll('.char-card__art, .char-card__body')].every(child => {
            const part = child.getBoundingClientRect();
            return (
              part.top >= bounds.top &&
              part.bottom <= bounds.bottom + 1 &&
              part.right <= bounds.right + 1
            );
          });
        });
        expect(contentFits, 'each complete portrait and description must fit its own card').toBe(
          true
        );
      }
      if (width === 320 || width === 1440) await createA11y(page, '#charPicker');
      await capture(page, info, 'agent-first-hire-picker', {}, { fullPage: false });
      await page.locator('#charPickerConfirm').click();
      await expect(page.locator('#charPicker')).toBeHidden();
      await expect(choose).toBeFocused();
      await expect(page.locator('#cr-appearance-host')).toContainText(characterName);
      await choose.click();
      await page.locator('#charPickerSearch').fill('No character matches this literal <filter>');
      await expect(page.locator('#charPickerGrid')).toContainText('No character matches');
      await page.keyboard.press('Escape');
      await expect(page.locator('#charPicker')).toBeHidden();
      await expect(page.locator('#addAgentModal')).toBeVisible();
      await expect(choose).toBeFocused();
      await expect(page.locator('#cr-name')).toHaveValue('Atlas <literal>');
      if (width === 320 || width === 1440) await createA11y(page, '#addAgentModal');
      await page.keyboard.press('Escape');
      await expect(page.locator('#addAgentModal')).toBeHidden();
      await expect(page.locator('#newAgentBtn')).toBeFocused();
      expect(unexpected).toEqual([]);
    });
  }
