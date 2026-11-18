import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  if (check) await check();
  if (seed) await retireWorkspaceSeed(request, seed);
  seed = undefined;
  check = undefined;
});

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440]) {
    test(`workspace chrome and staged settings ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      const base = `/workspaces/${seed.workspace.folder_slug}`;
      await page.goto(`${base}?mode=details`);
      await page.waitForFunction(
        () => (window as any).workspaceDetail?.initialListsLoaded === true
      );
      const minimum = width <= 768 ? 44 : 32;
      for (const selector of [
        '[data-cmd-nav-home]',
        '[data-cmd-settings]',
        '[data-cmd-give-work]',
        '[data-cmd-resources]',
        '[data-cmd-view-mode="details"]'
      ]) {
        const control = page.locator(selector).first();
        await reachable(control, minimum);
        const style = await control.evaluate(node => {
          const style = getComputedStyle(node);
          return {
            radius: parseFloat(style.borderRadius),
            size: parseFloat(style.fontSize),
            transform: style.textTransform,
            clip: style.clipPath
          };
        });
        expect(
          style.radius,
          `${selector} uses the canonical rounded control`
        ).toBeGreaterThanOrEqual(6);
        expect(style.size).toBe(14);
        expect(style.transform).toBe('none');
        expect(style.clip).toBe('none');
      }
      for (const mode of ['map', 'tickets', 'details']) {
        await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
        await expect(page.locator(`[data-cmd-view-mode="${mode}"]`)).toHaveAttribute(
          'aria-pressed',
          'true'
        );
      }
      const settings = page.locator('[data-cmd-settings]');
      await settings.click();
      const panel = page.locator('.ws-panels-dialog');
      await expect(panel).toBeVisible();
      await expect(panel).toHaveAttribute('aria-label', 'Workspace settings');
      await expect(panel).toHaveClass(/\bui-section\b/);
      const close = panel.locator('[data-panel-close]');
      await reachable(close, minimum);
      const closeText = await close.evaluate(node => {
        const text = document.createRange();
        text.selectNodeContents(node);
        return {
          height: text.getBoundingClientRect().height,
          lineHeight: parseFloat(getComputedStyle(node).lineHeight)
        };
      });
      expect(closeText.height, 'Close must not split into multiple lines').toBeLessThanOrEqual(
        closeText.lineHeight + 1
      );
      expect(await panel.evaluate(node => getComputedStyle(node).backgroundColor)).toMatch(
        /^rgb\(/
      );
      expect((await contrast(panel)).ratio).toBeGreaterThanOrEqual(4.5);
      for (const field of ['name', 'description', 'tags']) {
        const input = panel.locator(`#ws-panel-${field}`);
        await expect(input).toHaveClass(/\bform-control\b/);
        expect((await contrast(input)).ratio).toBeGreaterThanOrEqual(4.5);
        expect(await input.evaluate(node => getComputedStyle(node).backgroundColor)).toMatch(
          /^rgb\(/
        );
      }
      const description = panel.locator('#ws-panel-description');
      await description.fill('Unsent workspace description; no backend save is allowed.');
      await description.focus();
      await description.evaluate((node: HTMLTextAreaElement) => node.setSelectionRange(7, 16));
      // A real native discard refusal keeps the draft and its field alive.
      page.once('dialog', dialog => dialog.dismiss());
      await description.press('Escape');
      await expect(panel).toBeVisible();
      await expect(description).toHaveValue(
        'Unsent workspace description; no backend save is allowed.'
      );
      await expect(description).toBeFocused();
      expect(
        await description.evaluate((node: HTMLTextAreaElement) => [
          node.selectionStart,
          node.selectionEnd
        ])
      ).toEqual([7, 16]);
      await capture(
        page,
        info,
        'workspace-settings-draft',
        { workspaceId: seed.workspace.id, mutation: 'unsaved local draft; native discard refused' },
        { fullPage: false }
      );
      page.once('dialog', dialog => dialog.accept());
      await panel.locator('[data-panel-close]').click();
      await expect(panel).toBeHidden();
      await expect(settings).toBeFocused();
      const resources = page.locator('[data-cmd-resources]');
      await resources.click();
      await expect(panel).toHaveAttribute('aria-label', 'Resources');
      for (const section of ['files', 'memory', 'sessions', 'notes']) {
        const tab = panel.locator(`[data-panel-section="${section}"]`);
        await tab.click();
        await expect(tab).toHaveAttribute('aria-pressed', 'true');
        await reachable(tab, minimum);
      }
      await capture(
        page,
        info,
        'workspace-resources',
        { workspaceId: seed.workspace.id, records: 'real empty local workspace' },
        { fullPage: false }
      );
      await panel.locator('[data-panel-close]').press('Escape');
      await expect(panel).toBeHidden();
      await expect(resources).toBeFocused();
      await check();
    });
  }
