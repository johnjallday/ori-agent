import { expect, test } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Real page, member tree, waits, camera and roster. Only opt-in role/offer cases write. See
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
  const name = process.env.ORI_MAP_TEAM_HIRE_NAME || 'Index Keeper';
  await modal.locator('[data-agent-create-field="name"]').fill(name);
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
  const hired = deck.getByRole('button').filter({ hasText: name });
  await expect(hired).toBeVisible();
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/hired.png' });
  await hired.click();
  await expect(page.locator('.ws-map-unit[aria-pressed="true"]')).toContainText(name);
  await expect(page.locator('.ws-cmd-map-window')).toHaveCount(0);
});

test('the shared slot opens the Home offer without hiring; Not now removes it', async ({
  page
}) => {
  test.skip(
    !process.env.ORI_MAP_TEAM_SHARED_OFFER,
    'requires an unanswered offer in the disposable Home'
  );
  await installLocalCdn(page);
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/library/sharing')) writes.push(req.url());
  });
  await page.goto(route);
  await page.locator('.map-team-tab').click();
  const link = page.getByRole('link', { name: 'Hire for Song assistant' });
  await expect(link).toBeVisible();
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/shared-slot.png' });
  await link.click();
  await expect(page).toHaveURL(/\/assistant#projectLibrarySharing$/);
  await expect(page.locator('#projectLibrarySharingTitle')).toBeFocused();
  await expect(page.getByRole('button', { name: 'Add your Song assistant' })).toBeVisible();
  expect(writes).toEqual([]);
  await page.screenshot({ path: 'tasks/evidence-map-agent-deck/shared-offer.png' });
  const answered = page.waitForResponse(r => r.url().endsWith('/library/sharing/offer'));
  await page.locator('#projectLibrarySharingDismiss').click();
  expect((await answered).ok()).toBe(true);
  const summary = page.waitForResponse(r => r.url().endsWith('/library/summary'));
  await page.goto(route);
  expect((await (await summary).json()).shared_assistant_offer).toBeUndefined();
  await page.locator('.map-team-tab').click();
  await expect(page.getByRole('region', { name: 'Map team' })).not.toContainText('Song assistant');
});

test('Move mode and the folder view refuse team jumps without moving the camera', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.goto(route);
  const tab = page.locator('.map-team-tab');
  const deck = page.getByRole('region', { name: 'Map team' });
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  const move = page.locator('[data-map-drag]');
  const folders = page.locator('[data-cmd-by-folder]');
  for (const control of [move, folders]) {
    await control.click();
    const before = await camera();
    await tab.click();
    const target = deck.getByRole('button', { name: /Recordings Manager/ });
    await expect(target).toContainText('Cannot show');
    await target.click();
    await expect(deck.getByRole('status')).toContainText('Cannot show');
    expect(await camera()).toEqual(before);
    await page.keyboard.press('Escape');
    await expect(tab).toBeFocused();
    await control.click();
  }
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
      await page
        .getByRole('region', { name: 'Group map', exact: true })
        .evaluate(el => el.scrollIntoView({ block: 'center', behavior: 'instant' }));
      await tab.focus();
      await page.keyboard.press('Enter');
      await expect(deck).toBeVisible();
      const geometry = await deck.evaluate(el => {
        const surface = el.closest('[aria-label="Group map"]')!.getBoundingClientRect();
        const tab = el.previousElementSibling!.getBoundingClientRect();
        const menu = el.getBoundingClientRect();
        return {
          left: tab.left - surface.left,
          top: tab.top - surface.top,
          aligned: menu.left === tab.left,
          below: menu.top >= tab.bottom
        };
      });
      expect(geometry.left).toBeGreaterThan(0);
      expect(geometry.left).toBeLessThan(20);
      expect(geometry.top).toBeGreaterThan(0);
      expect(geometry.top).toBeLessThan(20);
      expect(geometry.aligned && geometry.below).toBe(true);
      const waiting = deck.getByRole('button', { name: /Has a question for you/ });
      await expect(waiting).toBeVisible();
      await expect(deck.getByRole('button', { name: /Songkeeper.*2 songs/ })).toBeVisible();
      await page.screenshot({ path: `tasks/evidence-map-agent-deck/team-${width}-${theme}.png` });
      await waiting.focus();
      await page.evaluate(() => (window as any).workspaceCommand.render());
      await expect(waiting).toBeFocused();
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
      await tab.click();
      // Even after a selection paints the dock, Roster must not obscure it or
      // the move/zoom strip. Inspect geometry, not just a visible CSS class.
      await expect
        .poll(() =>
          deck.evaluate(el => {
            const d = el.getBoundingClientRect();
            return [
              ...document.querySelectorAll(
                '.ws-map-control-dock,.ws-cmd-map-quest-dock > :not([hidden])'
              )
            ].every(node => {
              const r = node.getBoundingClientRect();
              return (
                !r.width ||
                !r.height ||
                d.bottom <= r.top ||
                d.top >= r.bottom ||
                d.right <= r.left ||
                d.left >= r.right
              );
            });
          })
        )
        .toBe(true);
    });
  }
}
