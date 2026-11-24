import { test, expect, Page } from '@playwright/test';
import { fileURLToPath } from 'node:url';

/**
 * Files with a search open and a scope in force is accessible: the box, the
 * scope chip, the Results tab and its groups pass axe (WCAG 2 A and AA, with
 * colour contrast actually evaluated) in light and dark, on a desktop and at
 * 390px, and a keyboard alone reaches the chip's remove button with a visible
 * focus ring.
 *
 * Not part of CI — run against an isolated server:
 *   ./scripts/e2e-fresh.sh tests/files-search.a11y.spec.ts -- --workers=1
 */

const stamp = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
const created: string[] = [];

async function seed(page: Page) {
  const name = `Harbor ${stamp}`;
  const workspace = await (await page.request.post('/api/workspaces', { data: { name } })).json();
  const id = workspace.folder.id as string;
  created.push(id);
  await page.request.post(`/api/workspaces/${id}/notes`, {
    data: { name: `Harbor note ${stamp}`, content: '# Body' }
  });
  await page.request.post(`/api/workspaces/${id}/tickets`, {
    data: { title: `Harbor ticket ${stamp}`, state: 'backlog', source: 'manual' }
  });
  await page.request.post('/api/sessions', {
    data: { title: `Harbor chat ${stamp}`, folder_id: id }
  });
  return { id, name };
}

for (const theme of ['light', 'dark']) {
  for (const viewport of [
    { label: 'desktop', width: 1280, height: 800 },
    { label: '1100px', width: 1100, height: 800 },
    { label: '390px', width: 390, height: 844 }
  ]) {
    test(`Files search with a scope is accessible (${theme}, ${viewport.label})`, async ({
      page,
      browserName
    }, info) => {
      await page.emulateMedia({ colorScheme: theme as 'light' | 'dark', reducedMotion: 'reduce' });
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
      );
      const ws = await seed(page);
      try {
        await page.goto('/?view=tree');
        const row = page.locator(`#cockpitTreeNav [data-tree-row="${ws.id}"]`);
        await row.waitFor();

        // Scope it from the row's own menu, with the keyboard.
        await row.focus();
        await page.keyboard.press('Shift+F10');
        await page.getByRole('menuitem', { name: 'Search in this workspace' }).click();
        const box = page.locator('#cockpitFilesSearchInput');
        await expect(box).toBeFocused();
        await box.fill(stamp);
        await expect(page.locator('#cockpitTreePane .cockpit-pane-tab--results')).toBeVisible();
        await expect(page.locator('#cockpitTreePane [data-results-group="notes"]')).toContainText(
          `Harbor note ${stamp}`
        );

        // A keyboard alone reaches the chip's remove button, and shows it. Safari
        // skips buttons when tabbing unless Option+Tab is used (its own setting, the
        // same for every button on the page), so WebKit is given the focus instead
        // and only the ring is checked.
        const remove = page.locator('#cockpitFilesSearchScope [data-files-scope-clear]');
        await box.focus();
        if (browserName === 'webkit') {
          await remove.focus();
        } else
          for (
            let i = 0;
            i < 4 && !(await remove.evaluate(el => el === document.activeElement));
            i++
          )
            await page.keyboard.press('Tab');
        await expect(remove).toBeFocused();
        // WebKit does not match :focus-visible for focus a script moves, and cannot
        // Tab to a button here, so the ring is checked where a keyboard really gets there.
        if (browserName !== 'webkit') await expect(remove).not.toHaveCSS('outline-style', 'none');
        await expect(remove).toHaveAccessibleName(/Search everywhere/);

        await page.addScriptTag({
          path: fileURLToPath(new URL('../node_modules/axe-core/axe.min.js', import.meta.url))
        });
        const result = await page.evaluate(async () => {
          const found = await (window as any).axe.run('#cockpitTree', {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          });
          return {
            violations: found.violations.map((v: any) => ({
              id: v.id,
              nodes: v.nodes.map((n: any) => n.target.join(' '))
            })),
            contrast: found.passes.find((r: any) => r.id === 'color-contrast')?.nodes.length || 0
          };
        });
        expect(result.violations).toEqual([]);
        expect(result.contrast).toBeGreaterThan(0);
        await page.screenshot({
          path: info.outputPath(`files-search-${theme}-${viewport.label}.png`),
          fullPage: true
        });
      } finally {
        await page.request.delete(`/api/workspaces/${ws.id}?confirm=true`);
      }
    });
  }
}
