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
  await map.locator('.ws-map-district').waitFor({ timeout: 20000 });
  return map;
}

type Box = { x: number; y: number; width: number; height: number };

function boxesOverlap(a: Box | null, b: Box | null) {
  if (!a || !b) return false;
  return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

test('a group page draws its own district, and its coordinates are Home’s', async ({ page }) => {
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
  // the agents and the Detachment toolbar riding on it.
  await expect(zone).toHaveAttribute('aria-label', 'Group map');
  await expect(page.locator('.ws-cmd-opmap')).toHaveCount(1);
  await expect(zone.locator('.ws-cmd-map-command-post')).toBeVisible();
  const toolbar = zone.locator('[data-map-zone="detachment"]');
  await expect(toolbar).toHaveAttribute('role', 'toolbar');
  await expect(toolbar.locator('.ws-cmd-map-zone-count')).toHaveText('2');
  await expect(zone.locator(`.ws-map-tile[data-ws-id="${member.id}"]`)).toBeVisible();
  // FR-7: nothing outside the group, and no reserved Personal HQ landmark.
  await expect(zone.locator(`.ws-map-tile[data-ws-id="${outsider.id}"]`)).toHaveCount(0);
  await expect(zone.locator('[data-hq-site]')).toHaveCount(0);
  // FR-12: no Collapse control, because collapsing would hide the whole zone.
  await expect(zone.locator('[data-group-collapse]')).toHaveCount(0);

  // FR-9: one layout, so the coordinate the group page draws is the coordinate
  // Home draws — no second store and no per-page offset.
  const moved = { x: 4120, y: 2360 };
  await page.request.patch('/api/workspace-map/layout', {
    data: { operations: [{ op: 'set_positions', positions: { [member.id]: moved } }] }
  });
  const saved = await (await page.request.get('/api/workspace-map/layout')).json();
  expect(saved.layout.positions[member.id]).toEqual(moved);

  await page.reload();
  await zone.locator('.ws-map-district').waitFor({ timeout: 20000 });
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

test('Build from a group page opens the shared wizard with the parent locked', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Build Group ${tag}`);
  await createMember(page, `Seed ${tag}`, group.id);

  const zone = await openGroupMap(page, group.folder_slug);
  await zone.locator('[data-cmd-detachment-build]').click();

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
  expect(
    await page.evaluate(() => (window as unknown as OriWindow).OriWorkspaceMap.hasPendingBuild())
  ).toBe(false);
});

test('the Details panel’s plus button opens the same locked creator', async ({ page }) => {
  await skipOnboarding(page);
  const tag = String(Date.now()).slice(-5);
  const group = await createGroup(page, `Details Group ${tag}`);

  await page.goto(`/workspaces/${group.folder_slug}?mode=details`);
  // Details mode reaches the Detachment panel through the Command rail, which
  // relocates the legacy members panel into its own host.
  const manage = page.locator('[data-cmd-manage-section="members"]');
  await manage.waitFor({ timeout: 20000 });
  await manage.click();
  const plus = page.locator('#workspace-detail-create-member-btn');
  await plus.waitFor({ timeout: 20000 });
  // FR-22: the bare name+description form is gone from the page.
  await expect(page.locator('#workspace-detail-member-create-form')).toHaveCount(0);

  await plus.click();
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
  { name: 'phone', width: 390, height: 844 }
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
            quest: box(document.querySelector('.ws-cmd-map-quest-fab')),
            belt: box(root && root.querySelector('.ws-cmd-map-belt')),
            toolbar: box(root && root.querySelector('.ws-cmd-map-group-bar')),
            agents: box(root && root.querySelector('.ws-cmd-map-command-post'))
          },
          tiles: Array.from(
            (root && root.querySelectorAll('.ws-cmd-detachment-host .ws-map-tile')) || []
          ).map(box)
        };
      });
    let snapshot = await measure();
    await expect
      .poll(async () => {
        snapshot = await measure();
        return (
          Object.values(snapshot.overlays).every(Boolean) &&
          snapshot.tiles.length === 2 &&
          snapshot.tiles.every(Boolean)
        );
      })
      .toBe(true);
    const overlays = snapshot.overlays;
    const names = Object.keys(overlays) as (keyof typeof overlays)[];
    for (let i = 0; i < names.length; i += 1) {
      for (const other of names.slice(i + 1)) {
        // New Quest and the belt are the agent map's own controls, unchanged
        // here; at phone width they already overlap on every workspace's map.
        if (names[i] === 'quest' && other === 'belt' && viewport.width <= 640) continue;
        expect(
          boxesOverlap(overlays[names[i]], overlays[other]),
          `${names[i]} and ${other} overlap`
        ).toBe(false);
      }
    }

    // The district opens framed in the space the overlays leave clear.
    for (const box of snapshot.tiles) {
      for (const name of names) {
        expect(boxesOverlap(box, overlays[name]), `a building sits under ${name}`).toBe(false);
      }
    }

    // One map wide, never wider than the page.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth
    );
    expect(overflow).toBe(false);
  });
}
