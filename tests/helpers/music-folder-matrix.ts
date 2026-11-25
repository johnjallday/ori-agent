import { expect, type Page } from '@playwright/test';
import { join } from 'node:path';
import { withOwnedBrowserZoom } from './owned-browser-zoom';
import { installLocalCdn } from './offline-cdn';

// All cases are navigation and disclosure checks: never confirm setup here.
export async function verifyMusicFolderViews(page: Page, route: string, part: 'sheet' | 'map') {
  const base = new URL(page.url()).origin;
  async function check(p: Page, variant: string) {
    await installLocalCdn(p);
    await p.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    await p.goto(route);
    await expect(p.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
    const selector = part === 'sheet' ? '[data-library-sheet]' : '.music-folder-view';
    if (part === 'map') await p.getByRole('button', { name: 'Group projects by folder', exact: true }).click();
    const surface = p.locator(selector);
    const control =
      part === 'map'
        ? surface.locator('summary').filter({ hasText: 'Albums · 5' })
        : surface.getByRole('checkbox', {
            name: /Select projects with a chosen file in .*Picker Songs/
          });
    await expect(control).toBeVisible();
    await control.focus();
    await p.keyboard.press('Space');
    if (part === 'map') await expect(control.locator('..')).not.toHaveAttribute('open', '');
    else
      await expect(
        surface.getByRole('button', { name: 'Put 2 on the map', exact: true })
      ).toBeEnabled();
    await expect(control).toBeFocused();
    const within = await surface.evaluate(e => {
      const b = e.getBoundingClientRect();
      return { left: b.left, right: b.right, width: innerWidth };
    });
    expect(within.left).toBeGreaterThanOrEqual(-1);
    expect(within.right).toBeLessThanOrEqual(within.width + 1);
    await surface.screenshot({
      path: join('tasks/evidence-music-folder-hierarchy', `${part}-${variant}.png`)
    });
  }
  const narrow = await page.context().newPage();
  try {
    await narrow.setViewportSize({ width: 390, height: 844 });
    await check(narrow, 'dark-narrow');
  } finally {
    await narrow.close();
  }
  await withOwnedBrowserZoom(base, 2, async p => check(p, 'dark-zoom'));
}
