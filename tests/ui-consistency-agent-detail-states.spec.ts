import { expect, test } from '@playwright/test';
import { assertSandbox, capture, preparePage, reachable } from './helpers/ui-consistency';
import { guardAgentCreationReads } from './fixtures/ui-consistency-agent-create';
import { detailReads, detailAgents, detailAgentName } from './fixtures/ui-consistency-agent-detail';

for (const theme of ['light', 'dark'] as const) {
  test(`Delayed character catalog refreshes the header without stealing a profile draft: ${theme}`, async ({
    page,
    request
  }) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const unexpected = await guardAgentCreationReads(page);
    await detailReads(page);
    let release!: () => void;
    const gate = new Promise<void>(resolve => {
      release = resolve;
    });
    await page.route('**/api/characters', async route => {
      if (route.request().method() !== 'GET') return route.fallback();
      await gate;
      await route.fulfill({ response: await route.fetch() });
    });
    try {
      await page.goto('/agents/' + encodeURIComponent(detailAgentName));
      await expect(page.locator('#agentName')).toHaveText(detailAgentName);
      await expect(page.locator('#agentAvatar .agent-avatar--character')).toHaveCount(0);
      await page.locator('#editProfileButton').click();
      const name = page.locator('#profileAgentNameInput');
      await name.fill('Unsent profile during character hydration');
      await name.evaluate(node => node.setSelectionRange(3, 7));
      release();
      await expect(page.locator('#agentAvatar .agent-avatar--character')).toBeVisible();
      await expect(name).toBeFocused();
      await expect(name).toHaveValue('Unsent profile during character hydration');
      expect(await name.evaluate(node => [node.selectionStart, node.selectionEnd])).toEqual([3, 7]);
      expect(unexpected).toEqual([]);
    } finally {
      release();
    }
  });
  for (const variant of ['claude', 'codex'] as const)
    for (const state of ['unavailable', 'not-found', 'load-error'] as const) {
      test(`Native detail distinguishes unavailable and failed reads: ${variant} ${state} ${theme}`, async ({
        page,
        request
      }, info) => {
        await assertSandbox(request);
        await preparePage(page, theme, 390);
        const unexpected = await guardAgentCreationReads(page);
        await detailReads(page);
        const name = variant === 'claude' ? 'Claude Code' : 'Codex';
        const record = detailAgents.find(agent => agent.name === name)!;
        await page.route(`**/api/agents/${encodeURIComponent(name)}/detail`, route => {
          if (route.request().method() !== 'GET') return route.fallback();
          return route.fulfill({
            status: state === 'not-found' ? 404 : state === 'load-error' ? 503 : 200,
            json:
              state === 'unavailable'
                ? { ...record, [`${variant}_sync`]: null }
                : { error: 'Fictional read failure' }
          });
        });
        await page.goto('/agents/' + encodeURIComponent(name));
        await expect(page.locator(`#${variant}DetailTitle`)).toHaveText(name);
        if (state === 'unavailable')
          await expect(page.locator(`.${variant}-sync-empty`)).toContainText('Not available');
        else
          await expect(page.locator(`#${variant}DetailMissing`)).toContainText(
            state === 'not-found' ? 'not found' : 'Failed to load'
          );
        await reachable(page.locator(`.${variant}-detail-back`), 44);
        await capture(
          page,
          info,
          `agent-detail-${variant}-${state}`,
          {
            fixture: 'GET-only native availability/error presentation, not an empty successful sync'
          },
          { fullPage: false }
        );
        expect(unexpected).toEqual([]);
      });
    }

  test(`Ordinary detail load failure retries a read without claiming a save: ${theme}`, async ({
    page,
    request
  }, info) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const unexpected = await guardAgentCreationReads(page);
    await detailReads(page);
    let ready = false;
    await page.route(`**/api/agents/${encodeURIComponent(detailAgentName)}/detail`, route => {
      if (route.request().method() !== 'GET') return route.fallback();
      return route.fulfill({
        status: ready ? 200 : 503,
        json: ready ? detailAgents[0] : { error: 'Fictional failed read' }
      });
    });
    await page.goto('/agents/' + encodeURIComponent(detailAgentName));
    await expect(page.locator('#agentLoadFailure')).toContainText('Nothing was changed');
    await expect(page.locator('#agentLoadRetry')).toBeFocused();
    await reachable(page.locator('#agentLoadRetry'), 44);
    await capture(
      page,
      info,
      'agent-detail-read-error',
      { fixture: 'GET-only error then GET recovery; no persistence assertion' },
      { fullPage: false }
    );
    ready = true;
    await page.locator('#agentLoadRetry').click();
    await expect(page.locator('#agentName')).toHaveText(detailAgentName);
    await expect(page.locator('#agentLoadFailure')).toHaveCount(0);
    expect(unexpected).toEqual([]);
  });
}
