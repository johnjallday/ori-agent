import { expect, test } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Read-only replay: real page, member tree, waits, camera and roster. See
// the manual guide for the preconditions. Does not scan folders or call a model.
const route = process.env.ORI_MAP_TEAM_HOME || '';
test.skip(!route, 'requires the prepared, isolated music Home');
for (const width of [1440, 390]) {
  for (const theme of ['dark', 'light']) {
    test(`a question and a shared agent on a ${width}px ${theme} map`, async ({ page }) => {
      await installLocalCdn(page);
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
      await page.goto(route);
      const tab = page.locator('.map-team-tab');
      const deck = page.getByRole('region', { name: 'Map team' });
      const close = page.locator('#personalAssistantClose');
      if (await close.isVisible()) await close.click();
      await tab.focus();
      await page.keyboard.press('Enter');
      const waiting = deck.getByRole('button', { name: /Has a question for you/ });
      await expect(waiting).toBeVisible();
      await expect(
        deck.getByRole('button', { name: /Songkeeper.*2 (songs|workspaces)/ })
      ).toBeVisible();
      await page.screenshot({ path: `tasks/evidence-map-agent-deck/team-${width}-${theme}.png` });
      await waiting.focus();
      await page.keyboard.press('Enter');
      await expect(deck).toBeHidden();
      const line = page.locator('.commander-balloon-line:visible');
      await expect(line).toHaveCount(1);
      await page.screenshot({
        path: `tasks/evidence-map-agent-deck/question-${width}-${theme}.png`
      });
      await tab.click();
      await deck.getByRole('button', { name: /Songkeeper/ }).click();
      const place = deck.getByRole('button', { name: 'After Hours', exact: true });
      await expect(place).toBeVisible();
      await place.click();
      await expect(deck).toBeHidden();
      await expect(page.locator('.ws-map-tile[aria-pressed="true"]')).toContainText('After Hours');
      expect(new URL(page.url()).pathname).toBe(new URL(route, 'http://localhost').pathname);
    });
  }
}
