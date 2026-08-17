import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 group-6 demo: curated accents and district themes.
 *
 * Chooses presets from the Home rail, checks the district wears them as bounded
 * classes in both light and dark mode, collapses to confirm the summary keeps
 * the same appearance, reloads, and resets to the default without disturbing
 * geometry.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server:
 *   ./scripts/demo-346.sh 8957 --fresh
 *   ./scripts/e2e.sh --port 8957 tests/district-appearance-demo.spec.ts
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function seedFixture(page: Page, tag: string) {
  const make = async (name: string, kind = 'workspace') => {
    const res = await page.request.post('/api/workspaces', { data: { name, kind } });
    return (await res.json()).folder.id;
  };
  const group = await make(`Look Group ${tag}`, 'group');
  const a = await make(`Look A ${tag}`);
  const b = await make(`Look B ${tag}`);
  for (const id of [a, b]) {
    await page.request.put(`/api/workspaces/${id}`, { data: { parent_id: group } });
  }
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        { op: 'set_positions', positions: { [a]: { x: 380, y: 380 }, [b]: { x: 608, y: 380 } } },
        {
          op: 'set_group_frame',
          group_id: group,
          frame: { x: 340, y: 340, width: 500, height: 260 }
        }
      ]
    }
  });
  return { group };
}

async function frameAndSelect(page: Page, district: Locator) {
  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);
  await expect(async () => {
    await district.locator('.ws-map-district-tag').click({ force: true });
    await expect(page.locator('[data-rail-map-layout]')).toBeVisible({ timeout: 1500 });
  }).toPass({ timeout: 20000 });
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Center selected' }).click();
  await page.waitForTimeout(300);
}

async function layoutOf(page: Page) {
  return (await (await page.request.get('/api/workspace-map/layout')).json()).layout;
}

test('#346 a district wears a curated accent and theme, keeps them collapsed, and resets cleanly', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Look A ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { group } = await seedFixture(page, tag);

  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
  await district.waitFor({ timeout: 15000 });
  await frameAndSelect(page, district);

  // FR-127: an un-customized district starts on the default pair.
  await expect(district).toHaveClass(/ws-map-accent-default/);
  await expect(district).toHaveClass(/ws-map-theme-default/);
  await expect(page.locator('[data-cockpit-group-appearance-reset]')).toHaveCount(0);

  const geometryBefore = await layoutOf(page);

  // FR-121/FR-130: choose named presets from the rail.
  await page.locator('[data-cockpit-group-accent][value="orchid"]').check();
  await page.waitForTimeout(500);
  await page.locator('[data-cockpit-group-theme][value="blueprint"]').check();
  await page.waitForTimeout(500);

  await expect(district).toHaveClass(/ws-map-accent-orchid/);
  await expect(district).toHaveClass(/ws-map-theme-blueprint/);
  const saved = await layoutOf(page);
  expect(saved.groups[group].accent).toBe('orchid');
  expect(saved.groups[group].theme).toBe('blueprint');
  // FR-138/FR-137: appearance never disturbs geometry.
  expect(saved.groups[group].frame).toEqual(geometryBefore.groups[group].frame);
  expect(saved.positions).toEqual(geometryBefore.positions);

  await page.screenshot({ path: 'test-results/346-appearance-light.png' });

  // FR-131: the same presets in dark mode.
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.waitForTimeout(300);
  await page.screenshot({ path: 'test-results/346-appearance-dark.png' });
  await page.emulateMedia({ colorScheme: 'light' });

  // FR-117: the collapsed summary wears the same appearance.
  await district.locator('[data-group-collapse]').click();
  await page.waitForTimeout(600);
  await expect(district).toHaveClass(/is-collapsed/);
  await expect(district).toHaveClass(/ws-map-accent-orchid/);
  await expect(district).toHaveClass(/ws-map-theme-blueprint/);
  await page.screenshot({ path: 'test-results/346-appearance-collapsed.png' });

  // FR-126: it survives a reload.
  await page.reload();
  await district.waitFor({ timeout: 15000 });
  await expect(district).toHaveClass(/ws-map-accent-orchid/);

  // FR-137: Use default appearance restores the presets and nothing else.
  await frameAndSelect(page, district);
  const beforeReset = await layoutOf(page);
  await page.locator('[data-cockpit-group-appearance-reset]').click();
  await page.waitForTimeout(600);

  await expect(district).toHaveClass(/ws-map-accent-default/);
  await expect(district).toHaveClass(/ws-map-theme-default/);
  const afterReset = await layoutOf(page);
  expect(afterReset.groups[group].accent).toBe('default');
  expect(afterReset.groups[group].theme).toBe('default');
  expect(afterReset.groups[group].collapsed).toBe(
    beforeReset.groups[group].collapsed,
    'the collapse state is untouched'
  );
  expect(afterReset.groups[group].frame).toEqual(
    beforeReset.groups[group].frame,
    'and so is the frame'
  );
  expect(afterReset.positions).toEqual(beforeReset.positions);

  await page.screenshot({ path: 'test-results/346-appearance-reset.png' });
});
