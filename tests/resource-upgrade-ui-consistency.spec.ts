import { test, expect, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync, realpathSync, lstatSync } from 'node:fs';
import { join, relative } from 'node:path';
import { createHash } from 'node:crypto';
import {
  resourceFileInventory,
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';
import { preparePage, capture, guardReadTour } from './helpers/ui-consistency';
import { boundedCanvasControl as bounded, assertCanvasAxe } from './helpers/ui-consistency-canvas';

const location = '.tmp/ui-consistency/group-5-catalog-focus-regression-location.json';
const receipt = '.tmp/ui-consistency/group-5-home-upgrade-prepared.json';
const digest = (value: string) => createHash('sha256').update(value).digest('hex');
function owner() {
  const root = process.env.ORI_RESOURCE_UPGRADE_UI_SANDBOX || '';
  expect(root).toBeTruthy();
  expect(realpathSync(root)).toBe(root);
  expect(lstatSync(root).isSymbolicLink()).toBe(false);
  expect(relative(realpathSync('.tmp/ui-consistency'), root).startsWith('..')).toBe(false);
  expect(JSON.parse(readFileSync(location, 'utf8')).sandbox).toBe(root);
  expect(readFileSync(join(root, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  const inventory = JSON.parse(
    readFileSync(
      '.tmp/ui-consistency/group-5-catalog-focus-regression-retained-inventory.json',
      'utf8'
    )
  );
  expect(inventory.root).toBe(root);
  const home = inventory.workspaces.find((value: any) => value.name === 'Music Home');
  expect(home.id).toMatch(/^[0-9a-f-]{36}$/);
  const studio = inventory.workspaces.find((value: any) => value.name === 'REAPER Studio');
  expect(studio.parent_id).toBe(home.id);
  return { root, home, studio, api: `/api/workspaces/${home.id}/assistant-program` };
}
async function read(request: APIRequestContext, path: string) {
  const response = await request.get(path);
  expect(response.ok()).toBe(true);
  return response.json();
}
if (process.env.ORI_RESOURCE_UPGRADE_PREPARE === '1') {
  test('prepare an exact inert guidance-only candidate in the owned fixture copy', async ({
    request
  }) => {
    const { root, home, api } = owner();
    const source = join(root, 'plugin-source/music-project-management');
    const original = resourceFileInventory(source);
    const plans: { path: string; old: string; next: string }[] = [];
    for (const folder of ['.claude-plugin', '.ori-plugin']) {
      const path = `${folder}/plugin.json`;
      const old = readFileSync(join(source, path), 'utf8');
      expect(old).toBe(
        readFileSync(join('tests/fixtures/resource-studio/music-home', path), 'utf8')
      );
      const value = JSON.parse(old);
      expect(value.version).toBe('0.4.0-fixture');
      value.version = '0.5.0-fixture';
      if (folder === '.ori-plugin') {
        const home = value.assistant_program_homes[0];
        expect(home.version).toBe(1); // Guidance-only never changes the Home schema/version.
        expect(home.roles[0].id).toBe('portfolio_manager');
        home.roles[0].system_prompt +=
          '\nFixture-only v2 guidance: remain read-only and never execute.';
      }
      plans.push({ path, old, next: JSON.stringify(value, null, 2) + '\n' });
    }
    const before = await read(request, api);
    expect(before.provider_upgrade.available).toBe(false);
    writeFileSync(
      receipt,
      JSON.stringify(
        {
          phase: 'planned',
          root,
          home,
          original,
          changes: plans.map(plan => ({
            path: plan.path,
            before: digest(plan.old),
            after: digest(plan.next)
          }))
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
    for (const plan of plans) writeFileSync(join(source, plan.path), plan.next, { mode: 0o600 });
    const expected = { ...original };
    for (const plan of plans) expected[plan.path] = digest(plan.next);
    expect(resourceFileInventory(source)).toEqual(expected);
    // Availability is cached: restart this OWNED demo after staging so its
    // ordinary startup checker sees the candidate. GET never resolves sources.
    expect((await read(request, api)).provider_upgrade.installed_version).toBe('0.4.0-fixture');
    writeFileSync(
      receipt,
      JSON.stringify(
        {
          phase: 'candidate',
          root,
          home,
          original,
          candidate: expected,
          changes: plans.map(plan => ({
            path: plan.path,
            before: digest(plan.old),
            after: digest(plan.next)
          }))
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
  });
} else if (process.env.ORI_RESOURCE_UPGRADE_PAUSE_MODE) {
  test('checkpoint the owned Home provider pause or exact restoration', async ({ request }) => {
    const fixture = owner();
    const journal = '.tmp/ui-consistency/group-5-home-provider-pause.json';
    const getPlugin = async () =>
      (await read(request, '/api/plugins')).plugins.find(
        (plugin: any) => plugin.name === 'music-project-management'
      );
    const path = join(fixture.root, 'workspace-staging');
    const source = join(fixture.root, 'plugin-source/music-project-management');
    if (process.env.ORI_RESOURCE_UPGRADE_PAUSE_MODE === 'pause') {
      expect((await getPlugin()).enabled).toBe(true);
      const files = resourcePreservationSnapshot(path),
        candidate = resourceFileInventory(source);
      writeFileSync(
        journal,
        JSON.stringify({ root: fixture.root, phase: 'planned-pause', files, candidate }, null, 2),
        { mode: 0o600 }
      );
      const response = await request.post('/api/plugins/music-project-management/disable', {
        data: {}
      });
      expect(response.ok()).toBe(true);
      expect((await getPlugin()).enabled).toBe(false);
      expect((await read(request, fixture.api)).plugin_available).toBe(false);
      expect(resourcePreservationSnapshot(path).comparable).toEqual(files.comparable);
      writeFileSync(
        journal,
        JSON.stringify({ root: fixture.root, phase: 'paused', files, candidate }, null, 2),
        { mode: 0o600 }
      );
    } else {
      expect(process.env.ORI_RESOURCE_UPGRADE_PAUSE_MODE).toBe('resume');
      const prior = JSON.parse(readFileSync(journal, 'utf8'));
      expect(prior.root).toBe(fixture.root);
      expect(prior.phase).toBe('paused');
      // Never restore over an unexpected definition, source, inventory or state.
      expect((await getPlugin()).enabled).toBe(false);
      expect(resourcePreservationSnapshot(path).comparable).toEqual(prior.files.comparable);
      expect(resourceFileInventory(source)).toEqual(prior.candidate);
      const response = await request.post('/api/plugins/music-project-management/enable', {
        data: {}
      });
      expect(response.ok()).toBe(true);
      expect((await getPlugin()).enabled).toBe(true);
      expect((await read(request, fixture.api)).plugin_available).toBe(true);
      expect(resourcePreservationSnapshot(path).comparable).toEqual(prior.files.comparable);
      writeFileSync(journal, JSON.stringify({ ...prior, phase: 'restored' }, null, 2), {
        mode: 0o600
      });
    }
  });
} else {
  let fixture: ReturnType<typeof owner>,
    baseline: unknown,
    files: ReturnType<typeof resourcePreservationSnapshot>,
    violations: string[],
    ready = false;
  let reviews: { token: string; from: string; to: string }[];
  async function snapshot(request: APIRequestContext) {
    const values: Record<string, unknown> = {};
    for (const path of [
      `/api/workspaces/${fixture.home.id}`,
      fixture.api,
      `${fixture.api}/provider-upgrade`,
      `/api/workspaces/${fixture.studio.id}/resource-catalog`,
      '/api/agents'
    ]) {
      const value = await read(request, path);
      if (path === '/api/agents')
        value.agents.sort((a: any, b: any) => a.name.localeCompare(b.name));
      values[path] = value;
    }
    return values;
  }
  test.beforeEach(async ({ request }, info) => {
    ready = false;
    reviews = [];
    violations = [];
    fixture = owner();
    const prepared = JSON.parse(readFileSync(receipt, 'utf8'));
    expect(['candidate', 'prepared']).toContain(prepared.phase);
    expect(prepared.root).toBe(fixture.root);
    expect(
      resourceFileInventory(join(fixture.root, 'plugin-source/music-project-management'))
    ).toEqual(prepared.candidate);
    baseline = await snapshot(request);
    files = resourcePreservationSnapshot(join(fixture.root, 'workspace-staging'));
    journalResourceRead(info, 'before', { root: fixture.root, api: baseline, files });
    ready = true;
  });
  test.afterEach(async ({ page, request }, info) => {
    await page.goto('about:blank');
    if (!ready) return;
    const after = await snapshot(request),
      afterFiles = resourcePreservationSnapshot(join(fixture.root, 'workspace-staging'));
    const outcomes = [];
    for (const review of reviews) {
      const response = await request.post(`${fixture.api}/provider-upgrade/outcome`, {
        data: { token: review.token }
      });
      expect(response.ok()).toBe(true);
      const value = await response.json();
      expect(value.operation).toBeUndefined();
      expect(value.review).toBe('pending');
      outcomes.push({
        from: review.from,
        to: review.to,
        unconsumed: value.review,
        operation: null
      });
    }
    journalResourceRead(info, 'after', {
      root: fixture.root,
      api: after,
      files: afterFiles,
      reviews: outcomes
    });
    expect(violations).toEqual([]);
    expect(after).toEqual(baseline);
    expect(afterFiles.comparable).toEqual(files.comparable);
    const prepared = JSON.parse(readFileSync(receipt, 'utf8'));
    expect(
      resourceFileInventory(join(fixture.root, 'plugin-source/music-project-management'))
    ).toEqual(prepared.candidate);
  });
  for (const theme of ['light', 'dark'] as const)
    for (const width of [320, 390, 768, 1440])
      test(`${process.env.ORI_RESOURCE_UPGRADE_PAUSED === '1' ? 'real paused Home preserves independent Studio' : 'real guidance review cancellation preserves old Home and agent prompts'} ${theme} ${width}`, async ({
        page
      }, info) => {
        await preparePage(page, theme, width);
        const reviewPath = `${fixture.api}/provider-upgrade/review`;
        violations = await guardReadTour(page, [reviewPath]);
        await page.route(`**${reviewPath}`, async route => {
          expect(route.request().method()).toBe('POST');
          expect(route.request().postDataJSON()).toEqual({});
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          const value = await response.json();
          expect(value.from_version).toBe('0.4.0-fixture');
          expect(value.to_version).toBe('0.5.0-fixture');
          reviews.push({ token: value.token, from: value.from_version, to: value.to_version });
          await route.fulfill({ response }); // Real reviewed plan, never fabricated POST success.
        });
        await page.goto(`/workspaces/${fixture.home.folder_slug}/assistant`);
        if (process.env.ORI_RESOURCE_UPGRADE_PAUSED === '1') {
          await expect(page.locator('#assistantProgramDisabled')).toBeVisible();
          await expect(page.locator('#assistantProgramDisabledText')).toContainText(
            'stored coordination data remains readable'
          );
          await expect(page.locator('#projectLibraryInitialize')).toBeDisabled();
          await bounded(page.locator('#assistantProgramWorkspaceLink'));
          const studio = await read(
            page.request,
            `/api/workspaces/${fixture.studio.id}/resource-catalog`
          );
          expect(studio.provider.available).toBe(true);
          await bounded(
            page
              .locator('#homeResourceWorkspaces')
              .getByRole('link', { name: 'Open REAPER Studio', exact: true })
          );
          await assertCanvasAxe(page, '#assistantProgramContent');
          await capture(
            page,
            info,
            'team-home-provider-paused',
            {
              workspaceId: fixture.home.id,
              fixture:
                'Actual owned Music Home provider disabled by supported API, not a projected failure. No execution/approval; definitions and independent Studio provider preserved.'
            },
            { fullPage: false }
          );
          return;
        }
        const door = page.locator('#assistantProgramUpgradeReview');
        await expect(door).toBeVisible();
        await bounded(door);
        await page.locator('#assistantProgramTitle').evaluate(node => {
          (node as HTMLElement).tabIndex = -1;
          (node as HTMLElement).focus();
        });
        await door.click();
        const dialog = page.locator('dialog.home-package-upgrade');
        await expect(dialog).toContainText('0.5.0-fixture');
        const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
        await bounded(cancel);
        await assertCanvasAxe(page, 'dialog.home-package-upgrade');
        await capture(
          page,
          info,
          'team-home-upgrade-review',
          {
            workspaceId: fixture.home.id,
            fixture:
              'Real guidance-only repository fixture review POST {}; only expiring review recorded, never commit. Old pinned Home and prompt hashes compared; token kept only in memory.'
          },
          { fullPage: false }
        );
        await cancel.press('Enter');
        await expect(dialog).toHaveCount(0);
        await expect(door).toBeFocused();
        expect(reviews).toHaveLength(1);
      });
}
