import {
  test,
  expect,
  type Page,
  type APIRequestContext,
  type APIResponse
} from '@playwright/test';
import { mkdirSync, mkdtempSync, renameSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { installLocalCdn } from './helpers/offline-cdn';

// Real records, credential-free, isolated demo only. Failure/latency and song
// presentation fixtures are explicitly labelled; none are persisted.
const evidence = 'tasks/evidence-map-work-folder-marker';
async function json(response: APIResponse) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}
async function createFixture(request: APIRequestContext) {
  mkdirSync('.tmp', { recursive: true });
  mkdirSync(evidence, { recursive: true });
  const root = mkdtempSync(resolve('.tmp/map-folder-browser-'));
  const suffix = randomUUID().slice(0, 6);
  const name = `Folder map ${suffix}`;
  const plan = await json(
    await request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: name }
    })
  );
  const manager = plan.agents[0];
  const group = (
    await json(
      await request.post('/api/workspaces', {
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
    )
  ).folder;
  const create = async (name: string, extra = {}) =>
    (
      await json(
        await request.post('/api/workspaces', {
          data: {
            name: `${name} ${suffix}`,
            parent_id: group.id,
            blank: true,
            create_template_agents: false,
            ...extra
          }
        })
      )
    ).folder;
  for (const folder of ['anchor', 'reference', 'imported']) mkdirSync(join(root, folder));
  const a = await create('A Managed');
  const b = await create('B Anchored with a deliberately long name', {
    anchor_path: join(root, 'anchor')
  });
  const c = await create('C Managed primary link');
  const reference = (
    await json(
      await request.post(`/api/workspaces/${c.id}/directories`, {
        data: { name: 'Reference <notes>', path: join(root, 'reference') }
      })
    )
  ).directory;
  await json(
    await request.put(`/api/workspaces/${c.id}`, { data: { primary_directory_id: reference.id } })
  );
  const d = (
    await json(
      await request.post('/api/workspaces/import', {
        data: {
          name: `D Imported ${suffix}`,
          folder_slug: `imported-${suffix}`,
          path: join(root, 'imported'),
          parent_id: group.id
        }
      })
    )
  ).folder;
  const selected = await Promise.all(
    [a, b, c, d].map(ws => request.get(`/api/workspaces/${ws.id}`).then(json))
  );
  expect(selected.map(ws => ws.shape)).toEqual(['managed', 'anchored', 'managed', 'imported']);
  expect(selected[2].primary_directory_id).toBe(reference.id);
  const tree = await json(await request.get('/api/workspaces?tree=true'));
  const keys = [
    'id',
    'name',
    'kind',
    'parent_id',
    'shape',
    'primary_directory_id',
    'anchor',
    'sidecar_path',
    'home_directory',
    'directory_references'
  ];
  const needed = (ws: any) =>
    Object.fromEntries(keys.filter(key => key in ws).map(key => [key, ws[key]]));
  const children = tree.workspaces.find((ws: any) => ws.id === group.id).children;
  writeFileSync(
    `${evidence}/final-folder-fields.json`,
    JSON.stringify({ nested_rows: children.map(needed), details: selected.map(needed) }, null, 2)
  );
  return { root, group, a, b, c, d };
}
type Fixture = Awaited<ReturnType<typeof createFixture>>;
type Host = 'home' | 'group';
const tile = (page: Page, id: string) => page.locator(`.ws-map-tile[data-ws-id="${id}"]`);
const details = (page: Page, host: Host) =>
  page.locator(host === 'home' ? '[data-map-folder-details]' : '[data-cmd-member-folders]');
async function open(page: Page, host: Host, fixture: Fixture) {
  await page.goto(
    host === 'home' ? '/?view=map' : `/workspaces/${fixture.group.folder_slug}?mode=map`
  );
  await expect(tile(page, fixture.b.id)).toBeVisible();
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await close.isVisible()) await close.click();
  await page.waitForFunction(() => (window as any).OriWorkspaceMap?.getCamera().zoom > 0);
}
async function dismissHomeContext(page: Page) {
  const requested = await page.evaluate(
    () => (window as any).OriHomeCockpit?.getState().modalRequested
  );
  if (!requested) return;
  // Loading text enters the DOM before Bootstrap's backdrop has finished
  // opening. Wait for shown before using its close control.
  await page.waitForFunction(() => (window as any).OriHomeCockpit.getState().modalVisible);
  await page.locator('#cockpitContextModal .btn-close').click();
  await page.waitForFunction(() => !(window as any).OriHomeCockpit.getState().modalVisible);
  await expect(page.locator('#cockpitContextModal')).toBeHidden();
}
async function choose(page: Page, host: Host, id: string, touch = false) {
  if (host === 'home') await dismissHomeContext(page);
  await centerTile(page, id);
  if (touch) await tile(page, id).tap();
  else await tile(page, id).click();
}
async function centerTile(page: Page, id: string) {
  // Pan without preselecting: a background mount must not mistake a renderer-
  // only selection for a new Home selection while the test is reaching a tile.
  await page.locator('[data-ws-map-viewport]').evaluate((el, id) => {
    const box = el.getBoundingClientRect();
    const tile = el.querySelector(`[data-ws-id="${id}"].ws-map-tile`)!.getBoundingClientRect();
    el.dispatchEvent(
      new WheelEvent('wheel', {
        bubbles: true,
        cancelable: true,
        deltaX: tile.x + tile.width / 2 - box.x - box.width / 2,
        deltaY: tile.y + tile.height / 2 - box.y - box.height / 2
      })
    );
  }, id);
}
async function capture(page: Page, name: string) {
  await page.waitForTimeout(300); // Let the existing modal transition finish.
  await page.screenshot({ path: `${evidence}/${name}.png`, animations: 'disabled' });
}
async function zoom(page: Page, target: number) {
  await page.locator('[data-ws-map-viewport]').evaluate((el, target) => {
    const box = el.getBoundingClientRect();
    el.dispatchEvent(
      new WheelEvent('wheel', {
        bubbles: true,
        cancelable: true,
        ctrlKey: true,
        clientX: box.x + box.width / 2,
        clientY: box.y + box.height / 2,
        deltaY: -Math.log(target / (window as any).OriWorkspaceMap.getCamera().zoom) / 0.0022
      })
    );
  }, target);
}

test.describe.serial('work folder markers', () => {
  test.use({ viewport: { width: 1440, height: 1100 }, hasTouch: true, reducedMotion: 'reduce' });
  let f: Fixture;
  test.beforeAll(async ({ request }) => {
    f = await createFixture(request);
  });
  test.afterAll(async ({ request }) => {
    if (!f) return;
    if (process.env.ORI_MAP_FOLDER_KEEP_FIXTURE === '1') {
      console.log(
        `Retained isolated fixture: ${f.group.name} — /workspaces/${f.group.folder_slug}?mode=map`
      );
      return;
    }
    // Only records this test created in the disposable server. Outside import
    // folders are unregistered by the normal delete API, never removed here.
    for (const ws of [f.a, f.b, f.c, f.d, f.group]) {
      const result = await request.delete(`/api/workspaces/${ws.id}?delete_sessions=true`);
      expect(result.ok(), await result.text()).toBeTruthy();
    }
  });
  test.beforeEach(async ({ page }) => {
    await installLocalCdn(page);
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
    );
  });

  test('real A–D on both hosts: selection/hover read only, exact folder facts and reload', async ({
    page
  }) => {
    for (const host of ['home', 'group'] as const) {
      const reads: string[] = [];
      page.on('request', req => {
        if (req.method() === 'GET') reads.push(req.url());
      });
      await open(page, host, f);
      expect(
        reads.filter(url =>
          [f.a, f.b, f.c, f.d].some(ws => url.endsWith(`/api/workspaces/${ws.id}`))
        )
      ).toEqual([]);
      for (const [key, marked] of [
        ['a', false],
        ['b', true],
        ['c', false],
        ['d', true]
      ] as const) {
        const ws = f[key];
        await expect(tile(page, ws.id).locator('.ws-map-work-folder')).toHaveCount(marked ? 1 : 0);
        if (host === 'home') await dismissHomeContext(page);
        await centerTile(page, ws.id);
        await page.waitForTimeout(900); // Exclude the preceding navigation camera save.
        const writes: string[] = [];
        const record = (req: any) => {
          if (
            req.method() !== 'GET' &&
            /\/api\/(workspaces|workspace-map\/layout)/.test(req.url()) &&
            !req.url().includes('/lead-conversation/')
          )
            writes.push(req.method() + ' ' + req.url());
        };
        page.on('request', record);
        await tile(page, ws.id).hover();
        await tile(page, ws.id).click();
        const slot = details(page, host);
        await expect(slot.locator('.ws-map-folder-details')).toBeVisible();
        await expect(slot).toContainText(
          key === 'b' ? 'Works in' : key === 'd' ? 'Imported folder' : 'Ori workspace folder'
        );
        if (key === 'b') await expect(slot).toContainText('Ori’s notes and settings');
        if (key === 'c') {
          await expect(slot).toContainText('Linked folders');
          await expect(slot).toContainText('Reference <notes>');
        }
        if (key === 'd') {
          await expect(slot.locator('.ws-map-folder-detail-path')).toHaveCount(1);
          await expect(slot).toContainText('Ori keeps its notes and settings here.');
        }
        await capture(page, `real-${host}-${key}`);
        page.off('request', record);
        expect(writes).toEqual([]);
      }
      await page.reload();
      await expect(tile(page, f.d.id).locator('.ws-map-work-folder')).toHaveCount(1);
      await choose(page, host, f.d.id);
      await expect(details(page, host).locator('.ws-map-folder-detail-path')).toHaveCount(1);
    }
  });

  test('real missing anchor keeps marker; Details relink preserves tool authority', async ({
    page,
    request
  }) => {
    const before = await json(await request.get(`/api/workspaces/${f.b.id}`));
    const moved = join(f.root, 'anchor-moved');
    renameSync(join(f.root, 'anchor'), moved);
    for (const host of ['home', 'group'] as const) {
      await open(page, host, f);
      await choose(page, host, f.b.id);
      await expect(details(page, host)).toContainText('Folder not found');
      await expect(tile(page, f.b.id).locator('.ws-map-work-folder')).toHaveCount(1);
      await capture(page, `real-${host}-missing`);
    }
    await page.goto(`/workspaces/${f.b.folder_slug}?mode=details`);
    await page
      .getByRole('dialog', { name: 'Hire a Commander for this workspace?' })
      .getByRole('button', { name: 'Not now' })
      .click();
    await page
      .locator('.ws-cmd-anchor-details')
      .getByRole('button', { name: 'Relink', exact: true })
      .click();
    const picker = page.getByRole('dialog', { name: 'Relink anchored folder' });
    await picker.getByLabel('or paste a folder path', { exact: true }).fill(moved);
    await picker.getByRole('button', { name: 'Use this path', exact: true }).click();
    await expect(picker).toHaveCount(0);
    const after = await json(await request.get(`/api/workspaces/${f.b.id}`));
    expect(after.anchor.path).toBe(moved);
    expect(after.anchor.directory_reference_id).toBe(before.anchor.directory_reference_id);
    expect(after.mcp_bindings.map((b: any) => [b.id, b.allowed_tools])).toEqual(
      before.mcp_bindings.map((b: any) => [b.id, b.allowed_tools])
    );
    for (const host of ['home', 'group'] as const) {
      await open(page, host, f);
      await choose(page, host, f.b.id);
      await expect(details(page, host)).toContainText(moved);
      await expect(details(page, host)).not.toContainText('Folder not found');
      await capture(page, `real-${host}-relinked`);
    }
  });

  test('injected delayed A and failed C never replace current B or prevent opening', async ({
    page
  }) => {
    for (const host of ['home', 'group'] as const) {
      let release!: () => void;
      const held = new Promise<void>(resolve => {
        release = resolve;
      });
      await page.route(`**/api/workspaces/${f.a.id}`, async route => {
        const response = await route.fetch();
        await held;
        await route.fulfill({ response });
      });
      await page.route(`**/api/workspaces/${f.c.id}`, route =>
        route.fulfill({ status: 503, json: { error: 'Injected detail failure' } })
      );
      await open(page, host, f);
      await choose(page, host, f.a.id);
      await expect(details(page, host)).toContainText('Loading folder details');
      await choose(page, host, f.b.id);
      await expect(details(page, host)).toContainText('Works in');
      release();
      await page.waitForTimeout(200);
      await expect(details(page, host)).toContainText('Works in');
      await choose(page, host, f.c.id);
      await expect(details(page, host)).toContainText('Folder details unavailable');
      await expect(page.getByRole('link', { name: 'Open Workspace', exact: true })).toBeVisible();
      await capture(page, `injected-${host}-failed-detail`);
      await choose(page, host, f.d.id);
      await expect(details(page, host)).toContainText('Imported folder');
      await page.unroute(`**/api/workspaces/${f.a.id}`);
      await page.unroute(`**/api/workspaces/${f.c.id}`);
    }
  });

  test('injected folder-only tree refresh preserves tile identity, focus, camera and spoken activity', async ({
    page
  }) => {
    for (const host of ['home', 'group'] as const) {
      await open(page, host, f);
      await centerTile(page, f.b.id);
      await tile(page, f.b.id).focus();
      await tile(page, f.b.id).evaluate(el => ((el as any).folderIdentity = 'kept'));
      const before = await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
      let shape = 'managed';
      await page.route('**/api/workspaces?tree=true', async route => {
        const response = await route.fetch();
        const body = await response.json();
        const patch = (rows: any[]) =>
          rows?.forEach(row => {
            if (row.id === f.b.id) row.shape = shape;
            patch(row.children);
          });
        patch(body.folders);
        patch(body.workspaces);
        await route.fulfill({ response, json: body });
      });
      const refresh = () =>
        page.evaluate(async host => {
          const w = window as any;
          if (host === 'home') await w.OriHomeCockpit.refreshQuietly();
          else {
            await w.workspaceDetail.membersPanel.reload();
            w.workspaceCommand.syncDetachmentMap();
          }
        }, host);
      await refresh();
      await expect(tile(page, f.b.id).locator('.ws-map-work-folder')).toHaveCount(0);
      shape = 'anchored';
      await refresh();
      await expect(tile(page, f.b.id).locator('.ws-map-work-folder')).toHaveCount(1);
      expect(await tile(page, f.b.id).evaluate(el => (el as any).folderIdentity)).toBe('kept');
      await expect(tile(page, f.b.id)).toBeFocused();
      expect(await page.evaluate(() => (window as any).OriWorkspaceMap.getCamera())).toEqual(
        before
      );
      await page.evaluate(id => {
        const w = window as any;
        const host =
          document.querySelector('.ws-cmd-detachment-host') ||
          document.querySelector('#cockpitMap');
        w.OriWorkspaceMap.folderAgentLines.update(host, { [id]: 'Checking files' });
      }, f.b.id);
      await expect(tile(page, f.b.id)).toHaveAttribute(
        'aria-label',
        /Checking files.*Uses an existing work folder/
      );
      expect(
        ((await tile(page, f.b.id).getAttribute('aria-label')) || '').split(
          'Uses an existing work folder'
        )
      ).toHaveLength(2);
      await page.unroute('**/api/workspaces?tree=true');
    }
  });

  test('injected song presentation coexists with ordinary folder facts and both Hide actions', async ({
    page
  }) => {
    await page.route(`**/api/workspaces/${f.b.id}/song-card`, route =>
      route.fulfill({
        json: {
          linked: true,
          home_name: 'Demo studio',
          title: 'Demo song',
          facts_state: 'on',
          facts: { tempo_bpm: 92, length_seconds: 221, track_count: 14 },
          keeper_agent_name: 'Demo keeper',
          status_line: '14 tracks · 92 BPM · 3:41; no open work',
          words: {
            source: 'package',
            noun: 'Song',
            noun_plural: 'Songs',
            roster_title: 'Songs and assistants',
            facts: [],
            fields: []
          }
        }
      })
    );
    await open(page, 'group', f);
    await page.evaluate(() => {
      const command = (window as any).workspaceCommand;
      command.groupTemplateStatus = { kind: 'managed_home' };
      command.render();
    });
    await choose(page, 'group', f.b.id);
    await expect(details(page, 'group')).toContainText('Works in');
    await expect(page.locator('[data-cmd-song-card-slot]')).toContainText('Demo song');
    await capture(page, 'injected-group-song-coexistence');
    await page.getByRole('button', { name: 'Hide song card' }).click();
    await expect(page.locator('[data-cmd-song-card-slot]')).toBeHidden();
    await expect(details(page, 'group')).toBeVisible();
    await page.getByRole('button', { name: 'Hide folder details' }).click();
    await expect(details(page, 'group')).toBeHidden();
    await choose(page, 'group', f.b.id);
    await expect(page.locator('[data-cmd-song-card-slot]')).toBeVisible();
    await page.locator('.ws-map-unit.is-commander').click();
    await expect(details(page, 'group')).toBeHidden();
    await expect(page.locator('[data-cmd-song-card-slot]')).toBeHidden();
    await page.getByRole('button', { name: 'Close map window', exact: true }).click();
    await page.evaluate(() => {
      const command = (window as any).workspaceCommand;
      command.detachmentMountedKey = '';
      command.syncDetachmentMap();
    });
    await expect(details(page, 'group')).toBeHidden();
  });

  test('dragging from the marked name in Move mode still moves the building', async ({ page }) => {
    await open(page, 'group', f);
    await zoom(page, 1);
    await centerTile(page, f.b.id);
    await page.locator('[data-map-drag]').click();
    await expect(page.locator('[data-map-drag]')).toHaveAttribute('aria-pressed', 'true');
    const building = tile(page, f.b.id);
    const before = await building.evaluate(el => (el as HTMLElement).style.left);
    const label = await building.locator('.ws-map-tile-name-text').boundingBox();
    await page.mouse.move(label!.x + label!.width / 2, label!.y + label!.height / 2);
    await page.mouse.down();
    await page.mouse.move(label!.x + label!.width / 2 + 210, label!.y + label!.height / 2, {
      steps: 12
    });
    await page.mouse.up();
    await expect
      .poll(() => building.evaluate(el => (el as HTMLElement).style.left))
      .not.toBe(before);
    await expect(building.locator('.ws-map-work-folder')).toHaveCount(1);
    await capture(page, 'real-group-drag-from-name');
  });

  test('keyboard and touch, themes, zoom and narrow selected details', async ({ page }) => {
    for (const host of ['home', 'group'] as const) {
      await open(page, host, f);
      for (const theme of ['light', 'dark']) {
        await page.evaluate(
          theme => document.documentElement.setAttribute('data-bs-theme', theme),
          theme
        );
        for (const scale of [0.5, 1]) {
          await zoom(page, scale);
          await centerTile(page, f.b.id);
          await page.keyboard.press('Tab');
          await tile(page, f.b.id).focus();
          await expect(tile(page, f.b.id).locator('.ws-map-work-folder-hint')).toBeVisible();
          await capture(page, `real-${host}-${theme}-${scale}-focus`);
        }
      }
      await tile(page, f.b.id).press('Space');
      await expect(details(page, host)).toContainText('Works in');
      if (host === 'home') await dismissHomeContext(page);
      else await page.getByRole('button', { name: 'Hide folder details' }).click();
      await page.setViewportSize({ width: 390, height: 844 });
      await zoom(page, 0.5);
      await choose(page, host, f.b.id, true);
      await expect(details(page, host)).toContainText('Works in');
      await expect(details(page, host)).toBeVisible();
      const box = await details(page, host).boundingBox();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(390);
      if (host === 'group')
        await details(page, host).evaluate(el => el.scrollIntoView({ block: 'center' }));
      await capture(page, `real-${host}-narrow-touch`);
      if (host === 'home') {
        await page.keyboard.press('Escape');
        await page.evaluate(() => (window as any).OriHomeCockpit.clearSelection());
        await expect(page.locator('.cockpit-context-modal')).not.toBeVisible();
      } else {
        await page.getByRole('button', { name: 'Hide folder details' }).click();
        await expect(details(page, host)).toBeHidden();
        await choose(page, host, f.b.id, true);
        await expect(details(page, host)).toBeVisible();
      }
      await page.setViewportSize({ width: 1440, height: 1100 });
      if (host === 'group') {
        await page.locator('.ws-map-unit.is-commander').click();
        await expect(details(page, host)).toBeHidden();
      }
    }
  });
});
