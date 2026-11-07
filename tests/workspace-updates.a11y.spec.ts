import { expect, test, type APIRequestContext, type Page, type Locator } from '@playwright/test';
import { existsSync, mkdtempSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Canonical host data throughout. Only CDN assets and pinned axe are supplied
// locally. Chromium reflow/keyboard/axe evidence is not native AT certification.
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX || '';
test.skip(!sandbox, 'requires the isolated goal fixture wrapper');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
let ws: { id: string; folder_slug: string; name: string };
let axe = '';
let calls = 0;
const lead = 'Alexandra Curator with a deliberately long display name';
async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()}: ${await response.text()}`).toBe(true);
  return response.json();
}
function modelCalls() {
  const path = join(sandbox, 'fixture-requests.jsonl');
  return existsSync(path) ? readFileSync(path, 'utf8').split('\n').filter(Boolean).length : 0;
}
async function fits(locator: Locator, width: number) {
  await expect(locator).toBeVisible();
  const box = await locator.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(-1);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width + 1);
  expect(await locator.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
}
async function scan(page: Page, selector: string, mapLabels = false) {
  const report = await page.evaluate(async selector => {
    const result = await (window as any).axe.run(selector, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }
    });
    return {
      violations: result.violations,
      incomplete: result.incomplete,
      contrast: result.passes.find((rule: any) => rule.id === 'color-contrast')?.nodes.length || 0
    };
  }, selector);
  expect(report.violations).toEqual([]);
  if (!mapLabels) {
    expect(report.contrast, JSON.stringify(report.incomplete)).toBeGreaterThan(0);
    return;
  }
  // Axe cannot resolve the map's decorative ancestor ::before grid even though
  // these labels have their own opaque background. Check their actual computed
  // colors rather than suppressing contrast or changing production for the scan.
  expect(
    report.incomplete.every(
      (rule: any) =>
        rule.id === 'color-contrast' &&
        rule.nodes.every((node: any) =>
          node.any.every((check: any) => check.data.messageKey === 'pseudoContent')
        )
    )
  ).toBe(true);
  const colors = await page.locator(`${selector} .workspace-update-signal`).evaluateAll(nodes =>
    nodes.map(node => {
      const style = getComputedStyle(node);
      return { foreground: style.color, background: style.backgroundColor, opacity: style.opacity };
    })
  );
  expect(colors.length).toBeGreaterThan(0);
  const luminance = (value: string) => {
    const rgb = value.match(/^rgba?\(([^)]+)\)$/);
    expect(rgb, value).not.toBeNull();
    const channels = rgb![1].split(',').map(Number);
    expect(channels[3] ?? 1).toBe(1);
    return channels
      .slice(0, 3)
      .map(n => n / 255)
      .map(n => (n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4))
      .reduce((sum, n, i) => sum + n * [0.2126, 0.7152, 0.0722][i], 0);
  };
  for (const color of colors) {
    expect(color.opacity).toBe('1');
    const a = luminance(color.foreground),
      b = luminance(color.background);
    expect((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)).toBeGreaterThanOrEqual(4.5);
  }
}
test.beforeAll(async ({ request }) => {
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  await ok(
    await request.post('/api/settings/system-model', {
      data: { provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
  await ok(
    await request.post('/api/agents', {
      data: { name: lead, type: 'general', provider: 'ollama', model: 'fixture-model:latest' }
    })
  );
  const created = (
    await ok(
      await request.post('/api/workspaces', {
        data: {
          name: 'A deliberately long workspace title for private letters, invoices and other documents awaiting review',
          entry_agent_name: lead
        }
      })
    )
  ).folder;
  ws = created;
  await ok(
    await request.post(`/api/workspaces/${ws.id}/capabilities/file-janitor/install`, {
      data: { source: 'in-place' }
    })
  );
  await ok(
    await request.post(`/api/workspaces/${ws.id}/capabilities/file-janitor/companion`, {
      data: {
        agent_instance_id: created.agent_instances.find(
          (agent: { name: string }) => agent.name === lead
        ).id
      }
    })
  );
  const root = mkdtempSync(join(sandbox, 'updates-a11y-inbox-'));
  for (const name of ['report.pdf', 'uncertain.qqq']) {
    writeFileSync(join(root, name), 'synthetic accessibility fixture', { mode: 0o600 });
    const old = new Date(Date.now() - 3_600_000);
    utimesSync(join(root, name), old, old);
  }
  await ok(
    await request.post(`/api/workspaces/${ws.id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    })
  );
  await ok(await request.post(`/api/workspaces/${ws.id}/file-janitor/scan`, { data: {} }));
  const asset = await request.get('https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js');
  expect(asset.ok()).toBe(true);
  axe = await asset.text();
  calls = modelCalls();
});

for (const theme of ['light', 'dark']) {
  for (const variant of [
    { name: 'desktop', width: 1440, height: 1000, reduced: false, scale: 1 },
    { name: 'tablet', width: 768, height: 1000, reduced: true, scale: 1 },
    { name: 'narrow', width: 390, height: 844, reduced: true, scale: 1 },
    { name: 'small-phone', width: 320, height: 844, reduced: true, scale: 1 },
    { name: 'zoom-reflow', width: 720, height: 550, reduced: true, scale: 2 }
  ]) {
    test.describe(`${theme} ${variant.name}`, () => {
      test.use({ hasTouch: variant.width <= 390 });
      test(`Updates accessibility: ${theme} ${variant.name}`, async ({ page, request }, info) => {
        await installLocalCdn(page);
        await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
        await page.setViewportSize({ width: variant.width, height: variant.height });
        if (variant.scale === 2) {
          const client = await page.context().newCDPSession(page);
          await client.send('Emulation.setDeviceMetricsOverride', {
            width: variant.width,
            height: variant.height,
            deviceScaleFactor: 2,
            mobile: false,
            screenWidth: 1440,
            screenHeight: 1100
          });
        }
        await page.emulateMedia({
          colorScheme: theme as 'light' | 'dark',
          reducedMotion: variant.reduced ? 'reduce' : 'no-preference'
        });
        const execution: string[] = [];
        page.on('request', req => {
          const path = new URL(req.url()).pathname;
          if (
            req.method() !== 'GET' &&
            /\/file-janitor\/(?:setup|scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
              path
            )
          )
            execution.push(path);
        });
        await page.goto('/');
        await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
        await expect(page.locator('#cockpitEconomy')).toBeVisible();
        await expect
          .poll(() =>
            page.evaluate(() => (window as any).OriWorkspaceMap?.getLayoutState?.().status)
          )
          .toBe('ready');
        await page.addScriptTag({ content: axe });
        const toggle = page.getByRole('button', { name: 'Show Updates', exact: true });
        await toggle.focus();
        await page.keyboard.press('Enter');
        const section = page.locator('#cockpitWorkspaceUpdates');
        await expect(section).toContainText('2 files to review');
        const pageCount = (await ok(await request.get('/api/workspace-updates'))).items.length;
        await expect(section).toContainText(
          `${pageCount} ${pageCount === 1 ? 'workspace' : 'workspaces'} in this page.`
        );
        await fits(section, variant.width);
        await scan(page, '#cockpitWorkspaceUpdates');
        const review = section.locator(
          `[data-update-row="${ws.id}"] [data-workspace-update="review"]`
        );
        await review.focus();
        await expect(review).toHaveCSS('outline-style', 'solid');
        const refresh = section.locator('[data-workspace-updates-refresh]');
        // Other specs can leave independent saved updates in this real sandbox.
        // Follow native tab order through those rows, rather than assuming ours
        // is the only (or last) workspace on the selected page.
        const controls = await section.locator('a, button').count();
        for (
          let index = 0;
          index < controls && !(await refresh.evaluate(el => el === document.activeElement));
          index++
        )
          await page.keyboard.press('Tab');
        await expect(refresh).toBeFocused();
        const live = await page.locator('#cockpitRailLive').textContent();
        await page.keyboard.press('Enter');
        await expect(section.locator('[data-workspace-updates-refresh]')).not.toHaveAttribute(
          'aria-disabled',
          'true'
        );
        await expect(section.locator('[data-workspace-updates-refresh]')).toBeFocused();
        expect(await page.locator('#cockpitRailLive').textContent()).toBe(live);
        // Oversized element/full-page captures reset Chromium touch and DPR
        // emulation. Capture the real viewport without changing input semantics.
        await page.screenshot({ path: info.outputPath('home-updates.png') });
        await page.getByRole('button', { name: 'Close Updates', exact: true }).click();
        const tile = page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${ws.id}"]`);
        await expect(tile).toHaveAttribute(
          'aria-label',
          /New message.*2 files to review.*Shift\+F10/
        );
        expect(await tile.locator('a, button').count()).toBe(0);
        await tile.focus();
        await page.keyboard.press('Shift+F10');
        await expect(
          page.getByRole('menuitem', { name: /Review.*files|files.*review/i })
        ).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(tile).toBeFocused();
        await scan(page, '#cockpitMap .ws-map-update-markers', true);
        await page.locator('[data-cockpit-view="tree"]').click();
        const row = page.locator(`#cockpitTreeNav [data-tree-row="${ws.id}"]`);
        await row.focus();
        await page.keyboard.press('Tab');
        await expect(row.locator('[data-workspace-update="welcome"]')).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(row.locator('[data-workspace-update="review"]')).toBeFocused();
        await fits(row, variant.width);
        if (variant.width <= 390) {
          expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true);
          expect(
            (await row.locator('[data-workspace-update="review"]').boundingBox())!.height
          ).toBeGreaterThanOrEqual(44);
        }
        await scan(page, '#cockpitTreeNav .workspace-update-signal');
        await page.screenshot({ path: info.outputPath('tree-updates.png') });

        await page.goto(`/workspaces/${ws.folder_slug}`);
        await page.addScriptTag({ content: axe });
        const card = page.locator('#workspaceNextActionRegion');
        await expect(card).toContainText('2 files ready for review');
        await expect(card).toContainText(lead);
        await expect(page.locator('#personalAssistantPanel')).toBeHidden();
        for (const mode of ['details', 'map']) {
          await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
          await fits(card, variant.width);
          const details = card.getByText('Source details', { exact: true });
          await details.focus();
          await page.keyboard.press('Enter');
          await expect(card).toContainText('Batch completed');
          await expect(card).toContainText('Review observed');
          await expect(card.locator('[data-cmd-update-sources]')).toHaveAttribute('open', '');
          await scan(page, '#workspaceNextActionRegion');
          await card.scrollIntoViewIfNeeded();
          await page.screenshot({ path: info.outputPath(`card-${mode}.png`) });
          await details.press('Enter');
          const open = card.getByRole('button', { name: 'Review files', exact: true });
          await open.focus();
          await page.keyboard.press('Enter');
          await expect(page.locator('#fileJanitorConsoleDialog')).toBeVisible();
          await expect
            .poll(() =>
              page
                .locator('#fileJanitorConsoleDialog')
                .evaluate(node => node.contains(document.activeElement))
            )
            .toBe(true);
          // Actual owner repaint, not a detached test-only replacement button.
          await page.evaluate(() => (window as any).workspaceCommand.render());
          await page.keyboard.press('Escape');
          await expect(page.locator('#fileJanitorConsoleDialog')).toBeHidden();
          await expect(
            card.getByRole('button', { name: 'Review files', exact: true })
          ).toBeFocused();
        }
        expect(
          (await ok(await request.get(`/api/workspaces/${ws.id}/updates`))).welcome.unread_count
        ).toBeGreaterThan(0);
        expect(execution).toEqual([]);
        expect(modelCalls()).toBe(calls);
      });
    });
  }
}
