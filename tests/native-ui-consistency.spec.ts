import { test, expect, chromium } from '@playwright/test';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';
import { capture } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';
import { boundedCanvasControl as bounded } from './helpers/ui-consistency-canvas';

test.skip(
  process.env.ORI_UI_NATIVE_ZOOM !== '1',
  'Explicit owned native-browser zoom fixture only'
);
for (const theme of ['light', 'dark'] as const)
  test(`native Chrome Page zoom200 preserves representative controls ${theme}`, async ({
    request
  }, info) => {
    let seed: WorkspaceSeed | undefined;
    let context: Awaited<ReturnType<typeof chromium.launchPersistentContext>> | undefined;
    let check: (() => Promise<void>) | undefined;
    try {
      seed = await createWorkspaceSeed(request);
      const data = await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      const profile = mkdtempSync(resolve('.tmp/ui-consistency/native-browser-profile-'));
      writeFileSync(join(profile, '.ui-consistency-owner'), `worktree=${resolve('.')}\n`, {
        mode: 0o600
      });
      context = await chromium.launchPersistentContext(profile, {
        channel: 'chromium',
        headless: true,
        viewport: null,
        args: ['--window-size=1440,900']
      });
      const settings = context.pages()[0];
      await settings.goto('chrome://settings/appearance');
      const page = await context.newPage();
      await installLocalCdn(page);
      if (process.env.ORI_UI_FONTS === 'real') {
        await page.unroute('https://fonts.googleapis.com/**');
        await page.unroute('https://fonts.gstatic.com/**');
      }
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      const origin = process.env.PLAYWRIGHT_BASE_URL || '';
      expect(origin).toMatch(/^http:\/\/localhost:\d+$/);
      await page.goto(`${origin}/settings`);
      const measure = () =>
        page.evaluate(() => ({
          innerWidth,
          innerHeight,
          outerWidth,
          outerHeight,
          devicePixelRatio,
          visualScale: visualViewport!.scale,
          cssZoom: getComputedStyle(document.documentElement).zoom
        }));
      const before = await measure();
      expect(before.innerWidth).toBe(1440);
      expect(before.devicePixelRatio).toBe(1);
      // The real Chrome preference UI changes its native default page zoom.
      // No viewport/device-scale/CDP/CSS/pinch emulation is used.
      await settings.locator('select#zoomLevel').selectOption('2');
      await expect.poll(async () => (await measure()).devicePixelRatio).toBe(2);
      const after = await measure();
      expect(after.innerWidth).toBe(720);
      expect(after.outerWidth).toBe(before.outerWidth);
      expect(after.visualScale).toBe(1);
      expect(after.cssZoom).toBe('1');
      const root = `/workspaces/${seed.workspace.folder_slug}`;
      const routes = [
        ['settings', '/settings', '#settingsSearch'],
        ['agents', '/agents', '#newAgentBtn'],
        ['agent-create', '/agents/create', '#agentName'],
        ['workspace', root, '[data-cmd-give-work]'],
        ['note', `/notes/${data.noteId}`, '#noteNameInput'],
        ['plan', `${root}/plans/${data.plans.draft.id}`, '#plan-save'],
        ['canvas', `${root}/canvas`, '#canvas-add-disclosure > summary'],
        ['diagnostics', `${root}/diagnostics`, '#darkModeToggle']
      ];
      for (const [name, path, control] of routes) {
        await page.goto(origin + path);
        await page.evaluate(() => document.fonts.ready);
        const zoom = await measure();
        expect(zoom.innerWidth).toBe(720);
        expect(zoom.devicePixelRatio).toBe(2);
        expect(zoom.outerWidth).toBe(before.outerWidth);
        expect(zoom.visualScale).toBe(1);
        expect(zoom.cssZoom).toBe('1');
        await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
        await bounded(page.locator(control));
        if (name === 'workspace') {
          await page.locator(control).press('Enter');
          const dialog = page.locator('dialog.give-work');
          await expect(dialog).toBeVisible();
          await bounded(dialog.getByRole('button', { name: 'Cancel', exact: true }));
          await page.keyboard.press('Escape');
          await expect(dialog).toBeHidden();
          await expect(page.locator(control)).toBeFocused();
        }
        await capture(
          page,
          info,
          `native-zoom-${name}`,
          {
            workspaceId: seed.workspace.id,
            nativeZoom: { mechanism: 'Chrome Appearance > Page zoom 200%', before, after: zoom },
            fixture:
              'Owned real definitions and native browser setting; metadata-only read tour, no execution. Profiles retained, not shared user browser state.'
          },
          { fullPage: false, nativeWindow: true }
        );
      }
      await settings.locator('select#zoomLevel').selectOption('1');
      await page.goto('about:blank');
      await check();
    } finally {
      await context?.close();
      if (check) await check();
      if (seed) await retireWorkspaceSeed(request, seed);
    }
  });
