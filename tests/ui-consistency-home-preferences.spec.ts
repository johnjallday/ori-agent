import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { assertSandbox, capture, reachable } from './helpers/ui-consistency';
import { guardAgentCreationReads } from './fixtures/ui-consistency-agent-create';
import { detailReads, detailAgentName } from './fixtures/ui-consistency-agent-detail';
import { homeReads } from './fixtures/ui-consistency-home';

for (const theme of ['light', 'dark'] as const)
  for (const density of ['compact', 'roomy'] as const) {
    test(`Real local theme/density preference survives Settings, Agents, detail and Home: ${theme} ${density}`, async ({
      page,
      request
    }, info) => {
      await assertSandbox(request);
      await installLocalCdn(page);
      if (process.env.ORI_UI_FONTS === 'real') {
        await page.unroute('https://fonts.googleapis.com/**');
        await page.unroute('https://fonts.gstatic.com/**');
      }
      await page.setViewportSize({ width: 320, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      // Seed once, not on every document: subsequent changes must really persist.
      await page.addInitScript(theme => {
        if (!sessionStorage.getItem('ui-consistency-preference-seed')) {
          localStorage.setItem('ori-theme', theme);
          sessionStorage.setItem('ui-consistency-preference-seed', 'done');
        }
      }, theme);
      const unexpected = await guardAgentCreationReads(page);
      await detailReads(page);
      await page.goto('/settings#display-density');
      const target = density === 'compact' ? 'uiDensityCompact' : 'uiDensityRoomy';
      await page.locator(`label[for="${target}"]`).click();
      await expect(page.locator('html')).toHaveAttribute('data-ui-density', density);
      await page.locator('#darkModeToggle').click();
      const changedTheme = theme === 'light' ? 'dark' : 'light';
      const assertPreferences = async () => {
        // Font swaps can move the newly navigated navbar/toolbar in WebKit.
        // Measure only after fonts and the resulting resize/layout frame settle;
        // capture still independently requires the intended faces to be loaded.
        await page.evaluate(async () => {
          await document.fonts.ready;
          await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
        });
        await expect(page.locator('html')).toHaveAttribute('data-bs-theme', changedTheme);
        await expect(page.locator('html')).toHaveAttribute('data-ui-density', density);
        expect(
          await page.evaluate(() => [
            localStorage.getItem('ori-theme'),
            localStorage.getItem('ori-ui-density')
          ])
        ).toEqual([changedTheme, density]);
      };
      await assertPreferences();
      await page.locator('.ori-navbar a[href="/agents"]').click();
      await expect(page.locator('#newAgentBtn')).toBeVisible();
      await assertPreferences();
      await reachable(page.locator('#newAgentBtn'), 44);
      await page.goto('/agents/' + encodeURIComponent(detailAgentName));
      await expect(page.locator('#agentAvatar .agent-avatar--character')).toBeVisible();
      await assertPreferences();
      await reachable(page.locator('#editProfileButton'), 44);
      await page.reload();
      await expect(page.locator('#agentName')).toHaveText(detailAgentName);
      await assertPreferences();
      await homeReads(page);
      await page.goto('/');
      await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'ready');
      await assertPreferences();
      await reachable(page.locator('#cockpitViewMap'), 44);
      await reachable(page.locator('#cockpitViewTree'), 44);
      await page.locator('#cockpitViewTree').click();
      await expect(page.locator('#cockpitTree')).toBeVisible();
      await page.locator('#cockpitViewMap').click();
      await expect(page.locator('#cockpitMap')).toBeVisible();
      await capture(
        page,
        info,
        `home-preferences-${density}`,
        {
          fixture:
            'GET-only page data; real UI localStorage theme/density changes survive native navigation and reload'
        },
        { fullPage: false }
      );
      expect(unexpected).toEqual([]);
    });
  }
