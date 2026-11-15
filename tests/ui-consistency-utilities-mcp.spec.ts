import { test, expect, type Page, type TestInfo } from '@playwright/test';
import {
  assertSandbox,
  preparePage,
  guardReadTour,
  capture as captureSurface,
  reachable,
  contrast
} from './helpers/ui-consistency';
import {
  mcpScene,
  mcpRegistryScene,
  mcpSourcesScene,
  themes
} from './fixtures/ui-consistency-scenes';

async function capture(page: Page, info: TestInfo, name: string) {
  await captureSurface(
    page,
    info,
    name,
    { fixture: 'fictional MCP reads; no connect, start, refresh, install or configuration writes' },
    { fullPage: false }
  );
}

// Deny unknown MCP reads too: /details?start=true is a GET with execution effects.
async function mcpReads(page: Page, state = () => 'populated') {
  const unexpected: string[] = [];
  await page.route('**/api/mcp/**', async route => {
    const request = route.request();
    if (request.method() !== 'GET') return route.fallback();
    const url = new URL(request.url());
    const path = url.pathname;
    const name = mcpScene.servers[0].name;
    const responses: Record<string, unknown> = {
      '/api/mcp/servers': state() === 'empty' ? { servers: [] } : mcpScene,
      '/api/mcp/marketplace': { servers: mcpRegistryScene },
      '/api/mcp/search': mcpRegistryScene,
      '/api/mcp/registry-sources': mcpSourcesScene,
      [`/api/mcp/servers/${encodeURIComponent(name)}/details`]: {
        ...mcpScene.servers[0],
        server: name,
        status: 'stopped',
        tools: [],
        env_keys: ['FICTIONAL_LAYOUT_OPTION'],
        readme: {
          markdown:
            '# Fictional documentation\n\nNo server was started.\n\n```text\nlong-unbroken-example-for-a-bounded-documentation-code-block-without-running-anything\n```',
          source_url: 'https://example.test/fictional-readme',
          source: 'fictional fixture'
        }
      }
    };
    if (url.search || !(path in responses)) {
      unexpected.push(path + url.search);
      return route.abort('blockedbyclient');
    }
    if (state() === 'error' && ['/api/mcp/servers', '/api/mcp/registry-sources'].includes(path)) {
      return route.fulfill({ status: 503, json: { error: 'Fictional unavailable MCP source' } });
    }
    return route.fulfill({ json: responses[path] });
  });
  return unexpected;
}

test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});

for (const theme of themes) {
  for (const width of [320, 1440]) {
    test(`MCP controls, read-only details and keyboard Sources: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const unexpected = await mcpReads(page);
      await page.goto('/mcp');
      await expect(page.locator('.ui-page-header h1')).toHaveText('MCP Servers');
      const card = page.locator('.mcp-server-card').first();
      await expect(card).toContainText(mcpScene.servers[0].name);
      await expect(card).toContainText('Error');
      await expect(card).toContainText('Disabled globally');
      await expect(
        page.locator('.mcp-server-card').last().getByRole('button', { name: 'Remove', exact: true })
      ).toHaveCount(0);
      for (const button of await card.getByRole('button').all())
        await reachable(button, width < 992 ? 44 : 32);
      expect((await contrast(card.locator('.mcp-metadata'))).ratio).toBeGreaterThanOrEqual(4.5);
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'mcp-servers');

      const details = card.getByRole('button', { name: 'Details', exact: true });
      await details.focus();
      await page.evaluate(() => (window as any).loadServers());
      await expect(details).toBeFocused();
      await details.click();
      const detailsModal = page.locator('#serverDetailsModal');
      await expect(
        detailsModal.getByRole('button', { name: 'Start server & load tools' })
      ).toBeVisible();
      await detailsModal.getByRole('tab', { name: 'Info', exact: true }).click();
      await expect(page.locator('#detailInfoPane')).toContainText('FICTIONAL_LAYOUT_OPTION');
      await expect(page.locator('#detailInfoPane')).toHaveCSS('opacity', '1');
      for (const term of await page.locator('.mcp-info-list dt').all()) {
        const size = await term.evaluate(node => {
          // Grid cells stretch to their value's height; measure the label's ink.
          const text = document.createRange();
          text.selectNodeContents(node);
          return {
            height: text.getBoundingClientRect().height,
            line: parseFloat(getComputedStyle(node).lineHeight)
          };
        });
        expect(size.height).toBeLessThanOrEqual(size.line + 1);
      }
      await capture(page, info, 'mcp-details');
      await detailsModal.getByRole('tab', { name: 'README', exact: true }).click();
      await expect(detailsModal.locator('.mcp-markdown')).toContainText('No server was started.');
      await expect(
        detailsModal.getByRole('link', { name: 'View source (fictional fixture)' })
      ).toHaveAttribute('rel', 'noopener noreferrer');
      // A status poll must not invalidate the dialog's return-focus target.
      await page.evaluate(() => (window as any).loadServers());
      await detailsModal.getByRole('button', { name: 'Close', exact: true }).click();
      await expect(details).toBeFocused();
      const remove = card.getByRole('button', { name: 'Remove', exact: true });
      await remove.click();
      await expect(page.locator('#removeServerName')).toHaveText(mcpScene.servers[0].name);
      await page
        .locator('#removeServerModal')
        .getByRole('button', { name: 'Cancel', exact: true })
        .click();
      await expect(remove).toBeFocused();

      const add = page.locator('#addServerBtn');
      await add.click();
      await reachable(page.locator('#addServerModal .install-btn'), width < 992 ? 44 : 32);
      await capture(page, info, 'mcp-marketplace');
      await page.locator('#import-tab').click();
      await page.locator('#importFileInput').setInputFiles({
        name: 'fictional-mcp.json',
        mimeType: 'application/json',
        buffer: Buffer.from(
          JSON.stringify({
            mcpServers: {
              'fictional-preview-only': { command: 'fictional-not-executable', args: [] }
            }
          })
        )
      });
      await expect(page.locator('#importPreviewContent')).toContainText('fictional-preview-only');
      await reachable(page.locator('#importBtn'), width < 992 ? 44 : 40);
      await capture(page, info, 'mcp-import-preview');
      await page.locator('#manual-tab').click();
      await expect(page.locator('#manual')).toHaveCSS('opacity', '1');
      await expect(page.locator('#addServerModal')).toHaveCSS('opacity', '1');
      const manual = page.locator('#manualName');
      expect(await manual.evaluate(node => node.matches(':user-invalid, .is-invalid'))).toBe(false);
      expect((await contrast(manual, '::placeholder')).ratio).toBeGreaterThanOrEqual(4.5);
      await manual.fill('fictional-unsaved-server');
      await page.locator('#manualCommand').fill('fictional-not-executable');
      await page.locator('#manualArgs').fill('{not valid json}');
      await page.locator('#manualConfigForm button[type="submit"]').click();
      await expect(page.locator('#addServerModal .toast-message')).toContainText('JSON');
      await reachable(
        page.locator('#addServerModal').getByRole('button', { name: 'Cancel', exact: true }),
        width < 992 ? 44 : 40
      );
      await capture(page, info, 'mcp-manual');
      await page
        .locator('#addServerModal')
        .getByRole('button', { name: 'Cancel', exact: true })
        .click();
      await expect(add).toBeFocused();
      await add.click();
      await expect(manual).toHaveValue('fictional-unsaved-server');
      await expect(page.locator('#manualArgs')).toHaveValue('{not valid json}');
      await page
        .locator('#addServerModal')
        .getByRole('button', { name: 'Cancel', exact: true })
        .click();

      await page.locator('#browse-tab').click();
      await expect(page.locator('#browseResults')).toContainText(mcpRegistryScene[0].name);
      const category = page.locator('[data-category="database"]');
      await category.focus();
      await category.press('Enter');
      await expect(category).toHaveAttribute('aria-pressed', 'true');
      await expect(category).toBeFocused();
      await expect(page.locator('#browseResults')).toContainText('No servers found');
      expect((await contrast(category)).ratio).toBeGreaterThanOrEqual(4.5);
      await page.locator('[data-category="all"]').click();
      const toggle = page.locator('#mcpSourcesToggle');
      const panel = page.locator('#sourcesPanel');
      await reachable(toggle, width < 992 ? 44 : 40);
      await toggle.focus();
      await toggle.press('Space');
      await expect(toggle).toHaveAttribute('aria-expanded', 'true');
      await expect(panel).toBeVisible();
      await expect(panel).toContainText(mcpSourcesScene[1].url!);
      expect((await contrast(panel.locator('small').last())).ratio).toBeGreaterThanOrEqual(4.5);
      await panel.scrollIntoViewIfNeeded();
      await capture(page, info, 'mcp-sources');
      await panel.getByRole('button', { name: 'Remove', exact: true }).focus();
      await page.keyboard.press('Escape');
      await expect(panel).toBeHidden();
      await expect(toggle).toHaveAttribute('aria-expanded', 'false');
      await expect(toggle).toBeFocused();
      await toggle.press('Enter');
      await expect(panel).toBeVisible();

      await page.locator('#addSourceBtn').click();
      await expect(page.locator('#sourceNameInput')).toBeFocused();
      await page
        .locator('#addSourceModal')
        .getByRole('button', { name: 'Add Source', exact: true })
        .click();
      await expect(page.locator('#addSourceModal .toast-message')).toContainText('required');
      await capture(page, info, 'mcp-source-dialog');
      await page
        .locator('#addSourceModal')
        .getByRole('button', { name: 'Cancel', exact: true })
        .click();
      await expect(page.locator('#addSourceBtn')).toBeFocused();
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width + 1
      );
      expect(writes).toEqual([]);
      expect(unexpected).toEqual([]);
    });
  }

  test(`MCP empty and unavailable reads remain distinct and retryable: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 320);
    const writes = await guardReadTour(page);
    let state = 'empty';
    const unexpected = await mcpReads(page, () => state);
    await page.goto('/mcp');
    await expect(page.locator('#emptyState')).toBeVisible();
    await capture(page, info, 'mcp-empty');
    state = 'error';
    await page.reload();
    await expect(page.locator('#mcpServersList [role="alert"]')).toContainText(
      'Failed to load servers'
    );
    await expect(page.locator('#emptyState')).toBeHidden();
    await capture(page, info, 'mcp-error');
    await page.locator('#browse-tab').click();
    await page.locator('#mcpSourcesToggle').click();
    await expect(page.locator('#sourcesList [role="alert"]')).toContainText(
      'Could not load registry sources'
    );
    state = 'populated';
    await page.locator('#sourcesList').getByRole('button', { name: 'Try again' }).click();
    await expect(page.locator('#sourcesList')).toContainText(mcpSourcesScene[0].name);
    await page.locator('#my-servers-tab').click();
    await page.locator('#mcpServersList').getByRole('button', { name: 'Try again' }).click();
    await expect(page.locator('.mcp-server-card')).toHaveCount(2);
    expect(writes).toEqual([]);
    expect(unexpected).toEqual([]);
  });
}
