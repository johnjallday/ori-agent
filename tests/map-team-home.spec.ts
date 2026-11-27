import { expect, test } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Read-only replay: real page, member tree, waits, camera and roster. See
// the manual guide for the preconditions. Does not scan folders or call a model.
const route = process.env.ORI_MAP_TEAM_HOME || '';
test.skip(!route, 'requires the prepared, isolated music Home');
test('Hire uses the existing role form and the filled slot becomes a navigable unit', async ({
  page
}) => {
  test.skip(
    !process.env.ORI_MAP_TEAM_FILL_ROLE,
    'requires a fresh Archivist slot in the disposable Home'
  );
  await installLocalCdn(page);
  await page.goto(route);
  const tab = page.locator('.map-team-tab');
  const deck = page.getByRole('region', { name: 'Map team' });
  await tab.click();
  await deck.getByRole('button', { name: 'Hire for Archivist' }).click();
  const modal = page.locator('#addAgentModal');
  await expect(modal).toBeVisible();
  await expect(modal).toContainText('Create an agent for Archivist');
  await modal.locator('[data-agent-create-field="name"]').fill('Index Keeper');
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/hire-archivist.png' });
  const saved = page.waitForResponse(
    r => r.request().method() === 'PUT' && r.url().endsWith('/roles/archivist')
  );
  await modal.locator('#createAgentBtn').click();
  const res = await saved;
  expect(res.ok(), await res.text()).toBe(true);
  await expect(modal).toBeHidden();
  await tab.click();
  await expect(deck.getByRole('button', { name: 'Hire for Archivist' })).toHaveCount(0);
  const hired = deck.getByRole('button', { name: /Index Keeper/ });
  await expect(hired).toBeVisible();
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/hired.png' });
  await hired.click();
  await expect(page.locator('.ws-map-unit[aria-pressed="true"]')).toContainText('Index Keeper');
  await expect(page.locator('.ws-cmd-map-window')).toHaveCount(0);
});

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
