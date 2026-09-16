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
  const zone = page.locator('[data-map-zone="detachment"]');
  await zone.waitFor({ timeout: 20000 });
  await zone.locator('.ws-map-district').waitFor({ timeout: 20000 });
  return zone;
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

  // FR-1/FR-3: the zone is the shared Map, scoped to this group.
  await expect(zone).toHaveAttribute('aria-label', 'Detachment map');
  await expect(zone.locator('.ws-cmd-map-zone-count')).toHaveText('2');
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
  expect(layoutRequests).toEqual([]);
});
