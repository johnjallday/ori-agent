import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import { mockHiredAssistant } from './helpers/hired-assistant';
import { installLocalCdn } from './helpers/offline-cdn';

// Prototype/design check of the real map API, not evidence of a hire flow.
for (const theme of ['light', 'dark']) {
  test(`ordinary cottage assembly frames — ${theme}`, async ({ page }) => {
    await installLocalCdn(page);
    await mockHiredAssistant(page);
    await page.request.post('/api/onboarding/skip');
    const response = await page.request.post('/api/workspaces', {
      data: { name: `Assembly cottage ${theme} ${Date.now()}` }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const id = (await response.json()).folder.id;
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/');
    const tile = page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${id}"]`);
    await expect(tile).toBeVisible();
    await page.evaluate(
      ({ id, theme }) => {
        document.documentElement.setAttribute('data-bs-theme', theme);
        const tile = document
          .querySelector(`.ws-map-tile[data-ws-id="${id}"]`)!
          .getBoundingClientRect();
        const canvas = document
          .querySelector('#cockpitMap .ws-map-canvas')!
          .getBoundingClientRect();
        (window as any).OriWorkspaceMap.panBy(
          tile.left + tile.width / 2 - (canvas.left + canvas.width / 2),
          tile.top + tile.height / 2 - (canvas.top + canvas.height / 2)
        );
      },
      { id, theme }
    );
    await page.clock.install();
    await page.clock.pauseAt(new Date());
    await page.evaluate(id => {
      (window as any).__assembly = (window as any).OriWorkspaceMap.playAssembly(id);
    }, id);
    await expect(tile).toHaveAttribute('data-build-reveal', 'playing');
    fs.mkdirSync('tasks/evidence-folder-build-animation/group-1', { recursive: true });
    for (const offset of [400, 900, 1400]) {
      await tile.locator('.ws-map-struct').evaluate((art, offset) => {
        for (const animation of art.getAnimations({ subtree: true })) {
          animation.pause();
          animation.currentTime = offset;
        }
      }, offset);
      await page.screenshot({
        path: `tasks/evidence-folder-build-animation/group-1/cottage-${theme}-${offset}.png`
      });
    }
    await page.evaluate(() => (window as any).__assembly.skip());
    await expect(tile).toHaveAttribute('data-build-reveal', 'settled');
    await page.screenshot({
      path: `tasks/evidence-folder-build-animation/group-1/cottage-${theme}-finished.png`
    });
  });
}
