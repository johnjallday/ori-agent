import { test, expect, Page, APIRequestContext } from '@playwright/test';

/**
 * workspace-parent-group-navigation: a workspace that belongs to a Group links
 * one level up to that Group from the Command header, right after Home.
 *
 * Not part of CI. Run against the isolated demo server:
 *   ./scripts/e2e.sh --port 8931 tests/workspace-parent-group-navigation.spec.ts -- --workers=1
 *
 * Every successful navigation here uses real groups, real membership, and the
 * real tree and routes. Only the failure case stubs a read.
 */

type Workspace = { id: string; name: string; folder_slug: string; parent_id?: string };

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
async function createGroup(request: APIRequestContext, name: string): Promise<Workspace> {
  const plan = await (
    await request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: name }
    })
  ).json();
  const manager = plan.agents[0];
  const res = await request.post('/api/workspaces', {
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
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return body.folder ?? body.workspace ?? body;
}

// A blank workspace with its default Workspace Manager, as Create makes one.
// Without an agent the page opens a blocking "Create a Commander" dialog,
// which is unrelated to this feature (see tests/file-janitor.spec.ts).
async function createWorkspace(
  request: APIRequestContext,
  name: string,
  parentId = ''
): Promise<Workspace> {
  const res = await request.post('/api/workspaces', {
    data: { name, parent_id: parentId, blank: true, create_template_agents: true }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return body.folder ?? body.workspace ?? body;
}

async function patchWorkspace(request: APIRequestContext, id: string, data: object) {
  const res = await request.patch(`/api/workspaces/${encodeURIComponent(id)}`, { data });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return (body.folder ?? body.workspace ?? body) as Workspace;
}

async function setParent(request: APIRequestContext, id: string, parentId: string) {
  return patchWorkspace(request, id, { parent_id: parentId });
}

type TreeNode = Workspace & { children?: TreeNode[] };

// The slug as the tree now reports it (a rename or a move can change it).
async function slugOf(request: APIRequestContext, id: string) {
  const tree = await (await request.get('/api/workspaces?tree=true')).json();
  const find = (nodes: TreeNode[] = []): TreeNode | null => {
    for (const node of nodes) {
      if (node.id === id) return node;
      const found = find(node.children);
      if (found) return found;
    }
    return null;
  };
  return find(tree.folders)?.folder_slug || '';
}

const groupLink = (page: Page) => page.locator('.ws-cmd-nav [data-cmd-parent-group]');

async function openWorkspace(page: Page, slug: string, search = '') {
  await page.goto(`/workspaces/${slug}${search}`);
  await page.locator('.ws-cmd-nav [data-cmd-nav-home]').waitFor({ timeout: 20000 });
}

// The page's own refresh path: the same workspace + tree read a load performs.
async function refreshInPlace(page: Page) {
  await page.evaluate(async () => {
    await (
      window as unknown as { workspaceDetail: { loadWorkspace(): Promise<void> } }
    ).workspaceDetail.loadWorkspace();
  });
}

const tag = () => String(Date.now()).slice(-6);

test.beforeEach(async ({ page }) => {
  await skipOnboarding(page);
});

test('a member opened directly links to its group right after Home, and Back returns', async ({
  page
}) => {
  const id = tag();
  const group = await createGroup(page.request, `Studio ${id}`);
  const member = await createWorkspace(page.request, `Drums ${id}`, group.id);

  await openWorkspace(page, member.folder_slug, '?mode=map#take-3');
  const link = groupLink(page);
  await expect(link).toHaveAttribute('href', `/workspaces/${group.folder_slug}`);
  await expect(page.getByRole('link', { name: `Group: ${group.name}` })).toBeVisible();
  await expect(link).toHaveAttribute('title', `Open the group ${group.name}`);
  // Immediately after Home, before Canvas.
  const order = await page.evaluate(() => {
    const link = document.querySelector('.ws-cmd-nav [data-cmd-parent-group]');
    return [
      link?.previousElementSibling?.hasAttribute('data-cmd-nav-home'),
      link?.nextElementSibling?.textContent?.trim()
    ];
  });
  expect(order).toEqual([true, 'Canvas']);

  await link.click();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${group.folder_slug}$`));
  await page.locator('.ws-cmd-nav [data-cmd-nav-home]').waitFor();
  await expect(groupLink(page), 'a top-level group has no group above it').toHaveCount(0);

  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${member.folder_slug}\\?mode=map#take-3$`));
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${group.folder_slug}`);

  await page.reload();
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${group.folder_slug}`);
});

test('the group link opens in a new tab like any link', async ({ page, context }) => {
  const id = tag();
  const group = await createGroup(page.request, `Mixing ${id}`);
  const member = await createWorkspace(page.request, `Vocals ${id}`, group.id);

  await openWorkspace(page, member.folder_slug);
  const [opened] = await Promise.all([
    context.waitForEvent('page'),
    groupLink(page).click({ modifiers: ['ControlOrMeta'] })
  ]);
  await opened.waitForURL(new RegExp(`/workspaces/${group.folder_slug}$`));
  expect(new URL(page.url()).pathname).toBe(`/workspaces/${member.folder_slug}`);
  await opened.close();
});

test('a nested group and its member each go one level up', async ({ page }) => {
  const id = tag();
  const outer = await createGroup(page.request, `Label ${id}`);
  const inner = await createGroup(page.request, `Album ${id}`);
  await setParent(page.request, inner.id, outer.id);
  const innerSlug = await slugOf(page.request, inner.id);
  const track = await createWorkspace(page.request, `Track ${id}`, inner.id);

  await openWorkspace(page, track.folder_slug);
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${innerSlug}`);
  await groupLink(page).click();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${innerSlug}$`));
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${outer.folder_slug}`);
  await groupLink(page).click();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${outer.folder_slug}$`));
  await page.locator('.ws-cmd-nav [data-cmd-nav-home]').waitFor();
  await expect(groupLink(page)).toHaveCount(0);
});

test('a workspace outside any group keeps Home and gets no group link', async ({ page }) => {
  const solo = await createWorkspace(page.request, `Solo ${tag()}`);
  await openWorkspace(page, solo.folder_slug);
  await expect(page.locator('.ws-cmd-nav [data-cmd-nav-home]')).toBeVisible();
  await expect(groupLink(page)).toHaveCount(0);
  await expect(page.locator('.ws-cmd-nav a[href="#"]')).toHaveCount(0);
});

test('the link follows a move, a parent rename, removal, and a deleted group', async ({ page }) => {
  const id = tag();
  const groupA = await createGroup(page.request, `Group A ${id}`);
  const groupB = await createGroup(page.request, `Group B ${id}`);
  const member = await createWorkspace(page.request, `Mover ${id}`, groupA.id);

  await openWorkspace(page, member.folder_slug, '?mode=details');
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${groupA.folder_slug}`);

  // Moved to B: the next refresh swaps the link without a page load.
  await setParent(page.request, member.id, groupB.id);
  await refreshInPlace(page);
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${groupB.folder_slug}`);
  await expect(groupLink(page)).toContainText(groupB.name);

  // B renamed, folder slug included.
  const renamed = `Renamed B ${id}`;
  await patchWorkspace(page.request, groupB.id, { name: renamed });
  const renamedSlug = await slugOf(page.request, groupB.id);
  expect(renamedSlug).not.toBe(groupB.folder_slug);
  await refreshInPlace(page);
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${renamedSlug}`);
  await expect(page.getByRole('link', { name: `Group: ${renamed}` })).toBeVisible();

  // Taken out of the group: the link goes; Home stays.
  await setParent(page.request, member.id, '');
  await refreshInPlace(page);
  await expect(groupLink(page)).toHaveCount(0);
  await expect(page.locator('.ws-cmd-nav [data-cmd-nav-home]')).toBeVisible();

  // Back into A, then only the group is deleted: its members return to root.
  await setParent(page.request, member.id, groupA.id);
  await refreshInPlace(page);
  await expect(groupLink(page)).toHaveAttribute('href', `/workspaces/${groupA.folder_slug}`);
  const deleted = await page.request.delete(
    `/api/workspaces/${encodeURIComponent(groupA.id)}?confirm=true&delete_mode=group_only`
  );
  expect(deleted.ok(), await deleted.text()).toBeTruthy();
  await refreshInPlace(page);
  await expect(groupLink(page)).toHaveCount(0);

  // The selected mode survived every refresh.
  await expect(page.locator('[data-cmd-view-mode="details"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
});

test('a failed tree read leaves Home and no group link', async ({ page }) => {
  const id = tag();
  const group = await createGroup(page.request, `Offline ${id}`);
  const member = await createWorkspace(page.request, `Stranded ${id}`, group.id);
  await page.route('**/api/workspaces?tree=true', route =>
    route.fulfill({ status: 500, contentType: 'application/json', body: '{}' })
  );
  // The page's own tree read must have failed before absence means anything.
  const failedRead = page.waitForResponse(
    response => response.url().endsWith('/api/workspaces?tree=true') && response.status() === 500
  );
  await openWorkspace(page, member.folder_slug);
  await failedRead;
  await expect(page.locator('.ws-cmd-nav [data-cmd-nav-home]')).toBeVisible();
  await expect(groupLink(page)).toHaveCount(0);
  await expect(page.locator('.ws-cmd-nav a[href="#"]')).toHaveCount(0);
});

for (const viewport of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'phone', width: 375, height: 812 }
]) {
  test(`a long group name stays bounded and fully named at ${viewport.name} width`, async ({
    page
  }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    const id = tag();
    const longName = `Spring Release Campaign Mastering Sessions And Stems Archive ${id}`;
    const group = await createGroup(page.request, longName);
    const member = await createWorkspace(page.request, `Stems ${id}`, group.id);

    await openWorkspace(page, member.folder_slug);
    const link = page.getByRole('link', { name: `Group: ${longName}` });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute('title', `Open the group ${longName}`);

    const metrics = await page.evaluate(() => {
      const link = document.querySelector('.ws-cmd-nav [data-cmd-parent-group]') as HTMLElement;
      const label = link.querySelector('.ws-cmd-nav-parent-label') as HTMLElement;
      const nav = document.querySelector('.ws-cmd-nav') as HTMLElement;
      const bar = document.querySelector('.ws-cmd-topbar') as HTMLElement;
      const home = document.querySelector('.ws-cmd-nav [data-cmd-nav-home]') as HTMLElement;
      // Page width with and without the link, in one synchronous read. Other
      // header content (the late "Ready to move" chip, a Dashboard tab) can
      // widen a phone page on its own; only the link's share is this feature's.
      const pageWidth = document.documentElement.scrollWidth;
      link.style.display = 'none';
      const pageWidthWithoutLink = document.documentElement.scrollWidth;
      link.style.display = '';
      const barStyle = getComputedStyle(bar);
      return {
        linkWidth: link.getBoundingClientRect().width,
        linkHeight: link.getBoundingClientRect().height,
        homeHeight: home.getBoundingClientRect().height,
        navRight: nav.getBoundingClientRect().right,
        barContentRight: bar.getBoundingClientRect().right - parseFloat(barStyle.paddingRight),
        linkRight: link.getBoundingClientRect().right,
        truncated: label.scrollWidth > label.clientWidth,
        pageWidth,
        pageWidthWithoutLink
      };
    });
    expect(metrics.truncated, 'the long name is cut short visually').toBe(true);
    expect(metrics.linkWidth).toBeLessThanOrEqual(320);
    expect(metrics.linkRight).toBeLessThanOrEqual(metrics.navRight + 0.5);
    expect(metrics.navRight, 'the nav row stays inside its bar').toBeLessThanOrEqual(
      metrics.barContentRight + 0.5
    );
    expect(metrics.linkHeight, 'one line, like its neighbours').toBeLessThanOrEqual(
      metrics.homeHeight + 1
    );
    expect(metrics.pageWidth, 'the link adds no page width').toBe(metrics.pageWidthWithoutLink);

    // Keyboard: Home, then the group link; Enter follows it.
    await page.locator('.ws-cmd-nav [data-cmd-nav-home]').focus();
    await page.keyboard.press('Tab');
    await expect(groupLink(page)).toBeFocused();
    // The ring must be drawn inside the border box: the nav buttons' clip-path
    // cuts away anything outside it. Polled past their 0.15s transition.
    await expect
      .poll(() =>
        groupLink(page).evaluate(el => {
          const style = getComputedStyle(el);
          return [style.outlineStyle, style.outlineWidth, parseFloat(style.outlineOffset) < 0];
        })
      )
      .toEqual(['solid', '2px', true]);
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(`/workspaces/${group.folder_slug}$`));
  });
}
