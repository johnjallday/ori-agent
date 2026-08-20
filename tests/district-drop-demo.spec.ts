import { test, expect, Page, Locator } from '@playwright/test';

/**
 * Issue #346 join-by-drop and Issue #374 confirmed ungroup demo.
 *
 * Drags a workspace into a district, then deliberately back onto open ground.
 * Both hierarchy changes must preview, confirm, preserve the dropped coordinate,
 * and use the same workspace PATCH contract as Tree.
 *
 * Not part of CI, and it needs a FRESH demo sandbox — one run per server:
 *   ./scripts/demo-346.sh 8981 --fresh
 *   ./scripts/e2e.sh --port 8981 tests/district-drop-demo.spec.ts
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
  const group = await make(`Drop Group ${tag}`, 'group');
  const member = await make(`Drop Member ${tag}`);
  const loose = await make(`Drop Loose ${tag}`);
  await page.request.put(`/api/workspaces/${member}`, { data: { parent_id: group } });
  await page.request.patch('/api/workspace-map/layout', {
    data: {
      operations: [
        {
          op: 'set_positions',
          positions: { [member]: { x: 380, y: 380 }, [loose]: { x: 1444, y: 380 } }
        },
        // Roomy enough that the loose workspace has somewhere to land inside.
        {
          op: 'set_group_frame',
          group_id: group,
          frame: { x: 340, y: 340, width: 640, height: 300 }
        }
      ]
    }
  });
  return { group, member, loose };
}

const parentOf = async (page: Page, id: string) => {
  const body = await (await page.request.get('/api/workspaces')).json();
  const row = (body.folders || []).find((f: { id: string }) => f.id === id);
  return row?.parent_id || '';
};

/** Drag an element by a WORLD-space delta, so the demo is zoom-independent. */
async function dragBy(page: Page, target: Locator, dx: number, dy: number, release = true) {
  const zoom = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera().zoom);
  const box = (await target.boundingBox())!;
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.mouse.move(cx + dx * zoom, cy + dy * zoom, { steps: 8 });
  if (release) await page.mouse.up();
}

test('#346 join and #374 confirmed ungroup preserve the dropped coordinate', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const existing = await (await page.request.get('/api/workspaces')).json();
  const stale = (existing.folders || []).filter((f: { name?: string }) =>
    String(f.name || '').startsWith('Drop Loose ')
  ).length;
  expect(stale, 'this demo needs a fresh sandbox: ./scripts/demo-346.sh <port> --fresh').toBe(0);
  const { group, loose } = await seedFixture(page, tag);

  await page.goto('/');
  const district = page.locator(`.ws-map-district[data-group-id="${group}"]`);
  await district.waitFor({ timeout: 15000 });

  const canvas = page.locator('[data-ws-map-viewport]');
  await canvas.click({ button: 'right', position: { x: 40, y: 40 } });
  await page.getByRole('menuitem', { name: 'Fit all' }).click();
  await page.waitForTimeout(300);

  const dragToggle = page.locator('[data-map-drag]');
  await expect(dragToggle).toHaveAttribute('aria-pressed', 'false');
  await dragToggle.click();
  await expect(dragToggle).toHaveAttribute('aria-pressed', 'true');

  expect(await parentOf(page, loose)).toBe('', 'it starts outside the group');

  const looseTile = page.locator(`.ws-map-tile[data-ws-id="${loose}"]`);

  // FR-6f: while it would land inside, the district says so — visibly and in
  // words — before anything is committed.
  await dragBy(page, looseTile, -700, 0, false);
  await expect(district).toHaveClass(/is-drop-target/);
  await expect(page.locator('[data-map-build-text]')).toContainText(
    `Release to move this workspace into Drop Group ${tag}`
  );
  await page.screenshot({ path: 'test-results/346-drop-target.png' });

  // FR-6g: releasing asks rather than commits. The panel names both sides and
  // the district it is talking about stays lit behind it.
  await page.mouse.up();
  const confirm = page.locator('[data-ws-map-drop-confirm]');
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText(`Move Drop Loose ${tag} into Drop Group ${tag}?`);
  await expect(district).toHaveClass(/is-drop-target/);
  await page.screenshot({ path: 'test-results/346-drop-confirm.png' });
  expect(await parentOf(page, loose)).toBe('', 'nothing is written while it is still asking');

  // Escape declines, and declining is not a failure: the move stands.
  await page.keyboard.press('Escape');
  await expect(confirm).toBeHidden();
  await page.waitForTimeout(400);
  expect(await parentOf(page, loose)).toBe('', 'declining changes no group');

  // Do it again and say yes.
  await dragBy(page, page.locator(`.ws-map-tile[data-ws-id="${loose}"]`), -40, 0, false);
  await page.mouse.up();
  await expect(confirm).toBeVisible();
  await confirm.getByRole('button', { name: 'Move into group' }).click();
  await page.waitForTimeout(900);

  expect(await parentOf(page, loose)).toBe(group, 'it is now in the group');
  await expect(district).not.toHaveClass(/is-drop-target/);
  await expect(district.locator('.ws-map-district-count')).toHaveText('2 workspaces');
  await page.screenshot({ path: 'test-results/346-drop-joined.png' });

  // FR-6d: moving it again INSIDE the same district is a reposition only — and
  // it must not ask, because nothing about its membership would change.
  const before = await parentOf(page, loose);
  await dragBy(page, page.locator(`.ws-map-tile[data-ws-id="${loose}"]`), 40, 40);
  await page.waitForTimeout(700);
  await expect(confirm).toBeHidden();
  expect(await parentOf(page, loose)).toBe(before);

  // Issue #374: genuinely empty ground now previews a leave, then asks. The
  // coordinate has already committed, while membership is still unchanged.
  const layoutBeforeLeave = await (await page.request.get('/api/workspace-map/layout')).json();
  await dragBy(page, page.locator(`.ws-map-tile[data-ws-id="${loose}"]`), 0, 900, false);
  await expect(page.locator('[data-map-build-text]')).toContainText(
    `Release to remove this workspace from Drop Group ${tag}`
  );
  await page.screenshot({ path: 'test-results/374-drop-leave-preview.png' });
  await page.mouse.up();
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText(`Remove Drop Loose ${tag} from Drop Group ${tag}?`);
  await expect(confirm.getByRole('button', { name: 'Remove from group' })).toBeVisible();
  await expect(confirm.getByRole('button', { name: 'Keep in group' })).toBeVisible();
  expect(await parentOf(page, loose)).toBe(group, 'release alone changes no hierarchy');
  const firstDroppedLayout = await (await page.request.get('/api/workspace-map/layout')).json();
  const firstDropped = firstDroppedLayout.layout.positions[loose];
  expect(firstDropped).not.toEqual(layoutBeforeLeave.layout.positions[loose]);

  // Declining keeps the new coordinate and the old group.
  await confirm.getByRole('button', { name: 'Keep in group' }).click();
  await page.waitForTimeout(700);
  expect(await parentOf(page, loose)).toBe(group);
  expect(
    (await (await page.request.get('/api/workspace-map/layout')).json()).layout.positions[loose]
  ).toEqual(firstDropped);

  // Repeat from the retained source and explicitly remove it.
  await dragBy(page, page.locator(`.ws-map-tile[data-ws-id="${loose}"]`), 0, 900);
  await expect(confirm).toBeVisible();
  const confirmedDrop = (await (await page.request.get('/api/workspace-map/layout')).json()).layout
    .positions[loose];
  await page.screenshot({ path: 'test-results/374-drop-leave-confirm.png' });
  await confirm.getByRole('button', { name: 'Remove from group' }).click();
  await page.waitForTimeout(900);

  expect(await parentOf(page, loose)).toBe('', 'confirmed leave moves it to top level');
  expect(
    (await (await page.request.get('/api/workspace-map/layout')).json()).layout.positions[loose]
  ).toEqual(confirmedDrop);
  await expect(district.locator('.ws-map-district-count')).toHaveText('1 workspace');
  await page.screenshot({ path: 'test-results/374-drop-left.png' });

  await page.locator('[data-cockpit-view="tree"]').click();
  await expect(page.locator(`[data-tree-row="${loose}"]`)).toHaveAttribute('aria-level', '1');
});
