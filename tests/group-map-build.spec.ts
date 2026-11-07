import { test, expect, Page } from '@playwright/test';

/**
 * group-map-build: a group's own page draws its district and builds into it.
 *
 * Not part of CI. Run against the isolated demo server:
 *   ./scripts/e2e.sh --workers=1 tests/group-map-build.spec.ts
 */

type OriWindow = { OriWorkspaceMap: { hasPendingBuild(): boolean } };

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

/**
 * Create a group the way the wizard does: the reviewed Group Manager roster.
 * A plain POST with kind=group is refused by the server.
 */
async function createGroup(page: Page, name: string) {
  const plan = await (
    await page.request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: name }
    })
  ).json();
  const manager = plan.agents[0];
  const res = await page.request.post('/api/workspaces', {
    data: {
      name,
      kind: 'group',
      group_roster: true,
      create_template_agents: true,
      template_agent_review: {
        version: 1,
        plan_revision: plan.revision,
        expectations: [{ index: 0, name: manager.name, action: manager.action }]
      }
    }
  });
  const body = await res.json();
  return body.folder ?? body.workspace ?? body;
}

async function createMember(page: Page, name: string, parentId: string) {
  const res = await page.request.post('/api/workspaces', {
    data: { name, parent_id: parentId, blank: true, create_template_agents: false }
  });
  const body = await res.json();
  return body.folder ?? body.workspace ?? body;
}

async function openGroupMap(page: Page, slug: string) {
  await page.goto(`/workspaces/${slug}?mode=map`);
  // PRD §10: a group's Map mode is one combined surface.
  const map = page.locator('.ws-cmd-opmap.is-combined');
  await map.waitFor({ timeout: 20000 });
  await map.locator('[data-ws-map-viewport]').waitFor({ timeout: 20000 });
  await expect(page.getByRole('region', { name: 'Next action', exact: true })).toHaveCount(0);
  await page.evaluate(async () => {
    await document.fonts.ready;
    const root = document.querySelector('.ws-cmd-opmap.is-combined');
    await Promise.all(
      (root?.getAnimations({ subtree: true }) || [])
        .filter(animation => animation.effect?.getTiming().iterations !== Infinity)
        .map(animation => animation.finished.catch(() => {}))
    );
  });
  return map;
}

type Box = { x: number; y: number; width: number; height: number };

function boxesOverlap(a: Box | null, b: Box | null, epsilon = 0) {
  if (!a || !b) return false;
  return (
    a.x < b.x + b.width - epsilon &&
    a.x + a.width > b.x + epsilon &&
    a.y < b.y + b.height - epsilon &&
    a.y + a.height > b.y + epsilon
  );
}

test('a group page draws its own district, and its coordinates are Home’s', async ({
  page
}, testInfo) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Map Group ${tag}`);
  const member = await createMember(page, `Member ${tag}`, group.id);
  await createMember(page, `Other ${tag}`, group.id);
  // A workspace outside the group, which the scoped map must never draw.
  const outsiderRes = await page.request.post('/api/workspaces', {
    data: { name: `Outsider ${tag}`, blank: true, create_template_agents: false }
  });
  const outsider = (await outsiderRes.json()).folder;

  const zone = await openGroupMap(page, group.folder_slug);

  // FR-1/FR-3 (PRD §10): one map — the shared Map scoped to this group, with
  // its agents standing on the ground and the Detachment toolbar riding on it.
  await expect(zone).toHaveAttribute('aria-label', 'Group map');
  await expect(page.locator('.ws-cmd-opmap')).toHaveCount(1);
  await expect(zone.locator('.ws-map-unit.is-commander')).toBeVisible();
  await expect(zone.locator('.ws-cmd-map-command-post')).toHaveCount(0);
  const toolbar = page.locator('[data-map-zone="detachment"]');
  await expect(toolbar).toHaveAttribute('role', 'toolbar');
  await expect(toolbar.locator('.ws-cmd-map-zone-count')).toHaveText('2');
  await expect(zone.locator(`.ws-map-tile[data-ws-id="${member.id}"]`)).toBeVisible();
  // FR-7: nothing outside the group, and no reserved Personal HQ landmark.
  await expect(zone.locator(`.ws-map-tile[data-ws-id="${outsider.id}"]`)).toHaveCount(0);
  await expect(zone.locator('[data-hq-site]')).toHaveCount(0);
  // PRD §10: the whole map is the group, so no district frame is drawn here
  // and nothing of its header (name tag, collapse, ⤧, ⋯) either.
  await expect(zone.locator('.ws-map-district')).toHaveCount(0);
  await expect(zone.locator('[data-group-collapse]')).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath('next-action-group-exclusion.png'),
    fullPage: true,
    animations: 'disabled'
  });

  // FR-9: one layout, so the coordinate the group page draws is the coordinate
  // Home draws — no second store and no per-page offset.
  const moved = { x: 4120, y: 2360 };
  await page.request.patch('/api/workspace-map/layout', {
    data: { operations: [{ op: 'set_positions', positions: { [member.id]: moved } }] }
  });
  const saved = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(saved.layout.positions[member.id]).toEqual(moved);

  await page.reload();
  await zone.locator(`.ws-map-tile[data-ws-id="${member.id}"]`).waitFor({ timeout: 20000 });
  const onGroupPage = await zone
    .locator(`.ws-map-tile[data-ws-id="${member.id}"]`)
    .evaluate(el => ({
      left: parseFloat((el as HTMLElement).style.left),
      top: parseFloat((el as HTMLElement).style.top)
    }));

  await page.goto('/');
  const homeTile = page.locator(`.ws-map-tile[data-ws-id="${member.id}"]`);
  await homeTile.waitFor({ timeout: 20000 });
  const onHome = await homeTile.evaluate(el => ({
    left: parseFloat((el as HTMLElement).style.left),
    top: parseFloat((el as HTMLElement).style.top)
  }));
  expect(onHome).toEqual(onGroupPage);
});

test('a group’s Commander stands on its map, opens its Unit Sheet, and keeps where it is moved', async ({
  page
}) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Unit Group ${tag}`);
  await createMember(page, `Post ${tag}`, group.id);

  const zone = await openGroupMap(page, group.folder_slug);
  const commander = zone.locator('.ws-map-unit.is-commander');
  await commander.waitFor({ timeout: 20000 });
  const unitId = await commander.getAttribute('data-unit-id');
  expect(unitId).toMatch(new RegExp(`^agent:${group.id}:`));

  // Activating the unit opens the same Unit Sheet the roster card did.
  await commander.click();
  await expect(page.locator('.ws-cmd-map-window')).toContainText('Unit Sheet');
  await page.evaluate(() => {
    const close = Array.from(document.querySelectorAll('.ws-cmd-map-window button')).find(
      button => button.textContent?.trim() === '×'
    ) as HTMLButtonElement | undefined;
    close?.click();
  });
  await expect(page.locator('.ws-cmd-map-window')).toHaveCount(0);

  // With Move on it drags like a building, and the anchor is saved in the
  // one shared layout under its agent id.
  await page.evaluate(() => {
    const move = document.querySelector(
      '.ws-cmd-detachment-host [data-map-drag]'
    ) as HTMLButtonElement | null;
    if (move && move.getAttribute('aria-pressed') !== 'true') move.click();
  });
  const before = await commander.evaluate(el => (el as HTMLElement).style.left);
  const box = await commander.boundingBox();
  expect(box).not.toBeNull();
  const start = { x: box!.x + box!.width / 2, y: box!.y + box!.height / 3 };
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  for (let step = 1; step <= 8; step += 1) {
    await page.mouse.move(start.x - step * 12, start.y + step * 6);
  }
  await page.mouse.up();
  await expect
    .poll(async () => {
      const layout = await (await page.request.get('/api/workspace-map/layout')).json();
      return layout.layout.positions[unitId!] || null;
    })
    .not.toBeNull();
  const saved = (await (await page.request.get('/api/workspace-map/layout')).json()).layout
    .positions[unitId!];

  await page.reload();
  const again = zone.locator(`.ws-map-unit[data-unit-id="${unitId}"]`);
  await again.waitFor({ timeout: 20000 });
  const after = await again.evaluate(el => ({
    x: parseFloat((el as HTMLElement).style.left),
    y: parseFloat((el as HTMLElement).style.top)
  }));
  expect(after).toEqual(saved);
  expect(`${after.x}px`).not.toBe(before);

  // Home never draws a group's agents.
  await page.goto('/');
  await page.locator('.ws-map-tile').first().waitFor({ timeout: 20000 });
  await expect(page.locator('.ws-map-unit')).toHaveCount(0);
});

test('Build from a group page opens the shared wizard with the parent locked', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Build Group ${tag}`);
  await createMember(page, `Seed ${tag}`, group.id);

  const zone = await openGroupMap(page, group.folder_slug);
  await page.locator('[data-cmd-add-workspace] > summary').click();
  await page.locator('[data-cmd-group-create]').click();

  // FR-15/FR-17: the whole wizard, with this group fixed as the destination.
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  await expect(modal.locator('#wizardStep1Title')).toBeVisible();
  const parent = modal.locator('#folderParentSelect');
  await expect(parent).toBeDisabled();
  await expect(parent).toHaveValue(group.id);
  await expect(modal.locator('#folderParentHelp')).toHaveText(`Building into ${group.name}`);
  // FR-16: the placement question never appears.
  await expect(modal).not.toContainText('Choose grouped or standalone placement');

  // FR-20: cancelling leaves no pending build site behind.
  await modal.getByRole('button', { name: 'Cancel' }).first().click();
  await expect(modal).toBeHidden();
  await expect(page.locator('[data-cmd-add-workspace] > summary')).toBeFocused();
  expect(
    await page.evaluate(() => (window as unknown as OriWindow).OriWorkspaceMap.hasPendingBuild())
  ).toBe(false);
});

test('the Overview Add workspace menu opens the same locked creator', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Details Group ${tag}`);

  await page.goto(`/workspaces/${group.folder_slug}?mode=details`);
  await page.locator('[data-cmd-add-workspace] > summary').click();
  const create = page.locator('[data-cmd-group-create]');
  // FR-22: the bare name+description form is gone from the page.
  await expect(page.locator('#workspace-detail-member-create-form')).toHaveCount(0);

  await create.click();
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  await expect(modal.locator('#folderParentSelect')).toHaveValue(group.id);
  await expect(modal.locator('#folderParentHelp')).toHaveText(`Building into ${group.name}`);
});

test('an ordinary workspace’s Map mode has no zone and asks for no layout', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const res = await page.request.post('/api/workspaces', {
    data: { name: `Plain ${tag}`, blank: true, create_template_agents: false }
  });
  const workspace = (await res.json()).folder;

  const layoutRequests: string[] = [];
  page.on('request', request => {
    if (request.url().includes('/api/workspace-map/layout')) layoutRequests.push(request.url());
  });

  await page.goto(`/workspaces/${workspace.folder_slug}?mode=map`);
  await page.locator('.ws-cmd-map-shell').waitFor({ timeout: 20000 });
  await expect(page.locator('[data-map-zone="detachment"]')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-detachment-host')).toHaveCount(0);
  await expect(page.locator('.ws-cmd-opmap.is-combined')).toHaveCount(0);
  expect(layoutRequests).toEqual([]);
});

// PRD §10: the overlays share one surface with the map, so at every supported
// width no control may sit on another, and none may cover a member building.
for (const viewport of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 768, height: 900 },
  { name: 'phone', width: 390, height: 844 },
  { name: 'small phone', width: 320, height: 900 }
]) {
  test(`the combined group map keeps its controls apart at ${viewport.name} width`, async ({
    page
  }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    await skipOnboarding(page);
    const tag = String(Date.now()).slice(-5);
    const group = await createGroup(page, `Layout Group ${tag}`);
    const first = await createMember(page, `North ${tag}`, group.id);
    await createMember(page, `South ${tag}`, group.id);

    const map = await openGroupMap(page, group.folder_slug);
    await map.locator(`.ws-map-tile[data-ws-id="${first.id}"]`).waitFor({ timeout: 20000 });
    // The Command view rebuilds its markup while the page settles, so scroll in
    // one atomic step rather than through a handle that may be replaced.
    await page.evaluate(() =>
      document.querySelector('.ws-cmd-opmap.is-combined')?.scrollIntoView({ block: 'start' })
    );

    // Measure every box in one atomic read. The Command view rebuilds its
    // markup while the page settles, so separate element handles can go stale
    // between measurements; poll until one read sees the whole surface.
    const measure = () =>
      page.evaluate(() => {
        const box = (el: Element | null) => {
          if (!el) return null;
          const r = el.getBoundingClientRect();
          return r.width && r.height
            ? { x: r.left, y: r.top, width: r.width, height: r.height }
            : null;
        };
        const root = document.querySelector('.ws-cmd-opmap.is-combined');
        return {
          overlays: {
            toolbar: box(
              root?.closest('.ws-cmd-map-shell')?.querySelector('.ws-cmd-map-group-bar') ?? null
            ),
            controls: box(root && root.querySelector('.ws-map-control-dock'))
          },
          // Page-wide floating widgets pinned to the viewport's corner.
          floating: box(document.querySelector('#personalAssistantLauncher')),
          // Buildings and the agents standing beside them: everything the
          // district opens framed on.
          tiles: Array.from(
            (root &&
              root.querySelectorAll(
                '.ws-cmd-detachment-host .ws-map-tile, .ws-cmd-detachment-host .ws-map-unit'
              )) ||
              []
          ).map(box),
          units:
            (root && root.querySelectorAll('.ws-cmd-detachment-host .ws-map-unit').length) || 0,
          stations: Array.from(root?.querySelectorAll('.ws-cmd-map-hq-station') || []).map(box)
        };
      });
    let snapshot = await measure();
    await expect
      .poll(async () => {
        snapshot = await measure();
        return (
          Object.values(snapshot.overlays).every(Boolean) &&
          snapshot.units >= 1 &&
          snapshot.tiles.length === 2 + snapshot.units &&
          snapshot.tiles.every(Boolean)
        );
      })
      .toBe(true);
    const overlays = snapshot.overlays;
    const names = Object.keys(overlays) as (keyof typeof overlays)[];
    for (let i = 0; i < names.length; i += 1) {
      for (const other of names.slice(i + 1)) {
        expect(
          boxesOverlap(overlays[names[i]], overlays[other]),
          `${names[i]} and ${other} overlap`
        ).toBe(false);
      }
    }

    // The compact dock must clear the real global assistant on phones too.
    if (snapshot.floating) {
      expect(
        boxesOverlap(overlays.controls, snapshot.floating),
        'map controls sit under the global assistant'
      ).toBe(false);
    }

    for (const station of snapshot.stations) {
      expect(
        boxesOverlap(station, overlays.toolbar),
        'a saved station sits under the toolbar'
      ).toBe(false);
      for (const tile of snapshot.tiles) {
        expect(boxesOverlap(station, tile, 0.5), 'a member or agent is obscured by a station').toBe(
          false
        );
      }
    }
    // The district opens framed in the space the controls leave clear, and no
    // agent stands on a building.
    snapshot.tiles.forEach((box, index) => {
      for (const name of names) {
        expect(boxesOverlap(box, overlays[name]), `a building or agent sits under ${name}`).toBe(
          false
        );
      }
      for (const other of snapshot.tiles.slice(index + 1)) {
        // The 170px world pitch meets a 170.25px label box with these fonts;
        // ignore only subpixel box rounding, not an obscured label or control.
        expect(
          boxesOverlap(box, other, 0.5),
          `two things stand on one spot: ${JSON.stringify([box, other])}`
        ).toBe(false);
      }
    });

    // One map wide, never wider than the page.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth
    );
    expect(overflow).toBe(false);
  });
}
