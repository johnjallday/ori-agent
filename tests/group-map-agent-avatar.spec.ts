import { test, expect, type Page, type APIResponse } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { mkdirSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// Run only against an isolated demo. Group/manager creation uses real reviewed
// APIs; character/upload reads are labelled presentation fixtures, never saved.
async function json(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function group(page: Page) {
  const name = `Avatar Group ${randomUUID().slice(0, 8)}`;
  const plan = await json(
    await page.request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: name }
    })
  );
  const manager = plan.agents[0];
  const created = await json(
    await page.request.post('/api/workspaces', {
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
    })
  );
  const workspace = created.folder || created.workspace;
  await json(
    await page.request.post('/api/workspaces', {
      data: {
        name: `Album notes ${workspace.id.slice(0, 8)}`,
        parent_id: workspace.id,
        blank: true,
        create_template_agents: false
      }
    })
  );
  return { workspace, manager: manager.name as string };
}

test('group map uses canonical generated, character and scoped-upload avatars without changing the saved definition', async ({
  page
}, testInfo) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      json: { needs_onboarding: false, completed: true, skipped: true }
    })
  );
  await page.route('**/api/workspace-map/layout', route =>
    route.fulfill({
      json: { layout: { schema_version: 1, revision: 1, positions: {}, snap_to_grid: true } }
    })
  );
  const { workspace, manager } = await group(page);
  const saved = async () => {
    const body = await json(await page.request.get('/api/agents/dashboard/list'));
    const profile = body.agents.find((agent: { name: string }) => agent.name === manager);
    expect(profile, 'the reviewed group created its real manager definition').toBeTruthy();
    return profile;
  };
  const original = await saved();
  expect(original.appearance?.mode || 'generated').toBe('generated');
  const evidence = 'tasks/evidence-one-face-everywhere';
  mkdirSync(evidence, { recursive: true });

  let mode = 'generated';
  const appearances: Record<string, object> = {
    character: { mode: 'character', character: { catalog_id: 'project-coordinator' } },
    uploaded: { mode: 'uploaded', uploaded: { image: 'group-portrait.png' } }
  };
  await page.route(`**/api/workspaces/${workspace.id}/agents`, async route => {
    const response = await route.fetch();
    const body = await response.json();
    if (mode !== 'generated') {
      body.agents = [
        ...body.agents.filter((agent: { name: string }) => agent.name !== manager),
        {
          ...original,
          source: 'workspace',
          appearance: appearances[mode],
          appearance_workspace_id: mode === 'uploaded' ? workspace.id : ''
        }
      ];
    }
    await route.fulfill({ response, json: body });
  });
  await page.route('**/avatars/group-portrait.png*', route =>
    route.fulfill({
      path: 'internal/web/static/characters/project-coordinator/portrait.svg',
      contentType: 'image/svg+xml'
    })
  );

  let unitID: string | null = null;
  for (mode of ['generated', 'character', 'uploaded']) {
    await page.goto(`/workspaces/${workspace.folder_slug}?mode=map`);
    await page.waitForFunction(() => (window as any).workspaceDetail?.initialListsLoaded === true);
    const drawerClose = page.getByRole('button', { name: 'Close personal assistant', exact: true });
    if (await drawerClose.isVisible()) await drawerClose.click();
    const zone = page.getByRole('region', { name: 'Group map', exact: true });
    const commander = zone.locator('.ws-map-unit.is-commander');
    await expect(commander).toBeVisible();
    const face = commander.locator(`.agent-avatar--${mode === 'uploaded' ? 'image' : mode}`);
    await expect(face).toBeVisible();
    await expect(face).toHaveCSS('width', '76px');
    await expect(commander.locator('.ws-map-av-figure')).toHaveCount(0);
    await expect(commander.locator('.ws-map-unit-crest')).toHaveText('★');
    await expect(commander).toContainText('Commander');
    const id = await commander.getAttribute('data-unit-id');
    if (unitID) expect(id).toBe(unitID);
    unitID = id;
    if (mode === 'generated') {
      await expect(face.locator('.agent-avatar__initials')).not.toBeEmpty();
      await commander.click();
      await expect(page.locator('.ws-cmd-map-window')).toContainText('Unit Sheet');
      await page.getByRole('button', { name: 'Close map window', exact: true }).click();
      await expect(page.locator('.ws-cmd-map-window')).toHaveCount(0);
    } else {
      const img = face.locator('img');
      await expect
        .poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0))
        .toBe(true);
      if (mode === 'uploaded') {
        const src = new URL((await img.getAttribute('src'))!, page.url());
        expect(src.searchParams.get('studio_id')).toBe(workspace.id);
        expect(src.searchParams.get('agent')).toBe(manager);
      }
    }
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({
      path: `${evidence}/group7-${mode}-${testInfo.project.name}.png`,
      animations: 'disabled'
    });
  }
  expect((await saved()).appearance).toEqual(original.appearance);
});
