import { expect, test } from '@playwright/test';
import { assertSandbox, capture, preparePage } from './helpers/ui-consistency';
import { agentCreateReads, guardAgentCreationReads } from './fixtures/ui-consistency-agent-create';
import { longModelName, modelProviders } from './fixtures/ui-consistency-scenes';

for (const theme of ['light', 'dark'] as const) {
  test(`Create metadata loading/error preserves an unrelated draft and caret: ${theme}`, async ({
    page,
    request
  }, info) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const unexpected = await guardAgentCreationReads(page);
    await agentCreateReads(page);
    let release!: () => void;
    const gate = new Promise<void>(resolve => {
      release = resolve;
    });
    await page.route('**/api/providers', async route => {
      if (route.request().method() !== 'GET') return route.fallback();
      await gate;
      await route.fulfill({ status: 200, json: modelProviders });
    });
    await page.route('**/api/agents/catalog', async route => {
      if (route.request().method() !== 'GET') return route.fallback();
      await gate;
      await route.fulfill({ status: 503, json: { error: 'Fictional catalog unavailable' } });
    });
    try {
      await page.goto('/agents/create');
      await expect(page.locator('#hireCardHost [role="status"]')).toContainText('Loading roles');
      await expect(page.locator('#hireStartBtn')).toBeDisabled();
      await capture(
        page,
        info,
        'agent-create-loading',
        { fixture: 'GET metadata held; no execution or writes' },
        { fullPage: false }
      );
      await page.locator('#customTabBtn').click();
      const name = page.locator('#agentName');
      await name.fill('A draft before metadata arrives');
      await name.evaluate(node => node.setSelectionRange(2, 9));
      release();
      await expect(page.locator('#llmModel')).toHaveValue(longModelName);
      await expect(name).toBeFocused();
      await expect(name).toHaveValue('A draft before metadata arrives');
      expect(await name.evaluate(node => [node.selectionStart, node.selectionEnd])).toEqual([2, 9]);
      await page.locator('#catalogTabBtn').click();
      await expect(page.locator('#hireCardHost [role="alert"]')).toContainText(
        'Could not load the roles'
      );
      await expect(page.locator('#hireStartBtn')).toBeDisabled();
      await capture(
        page,
        info,
        'agent-create-catalog-error',
        { fixture: 'GET-only 503, not an empty catalog or a failed hire' },
        { fullPage: false }
      );
      await page.locator('#customTabBtn').click();
      await expect(name).toHaveValue('A draft before metadata arrives');
      expect(unexpected).toEqual([]);
    } finally {
      release();
    }
  });
}
