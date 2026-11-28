import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { musicHomeSnapshot } from './helpers/music-home-placement';
import { libraryJSON, sourceHashes } from './helpers/project-library-browser-fixture';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_MUSIC_FINDER_SANDBOX || '';
const evidence = 'tasks/evidence-music-map-find-and-resume';
const base = '/api/workspaces/finder-home/assistant-program/library';
const home = '/workspaces/music-production-home?mode=map';
test.skip(!sandbox, 'Requires a disposable, worktree-owned Music + REAPER sandbox');
test.beforeAll(async ({ request }) => {
  expect(readFileSync(join(sandbox, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  const installed = await libraryJSON(await request.get('/api/plugins'));
  const music = installed.plugins.find((p: any) => p.name === 'music-project-management');
  expect(realpathSync(music.install_dir)).toBe(
    join(realpathSync(sandbox), 'plugin-source', 'music-project-management')
  );
  expect((await libraryJSON(await request.get('/api/settings/system-model'))).configured).toBe(
    false
  );
  const root = join(realpathSync(sandbox), 'finder-fixture');
  if (!existsSync(root))
    execFileSync(
      'go',
      ['test', './internal/projectlibrary', '-count=1', '-run', '^TestExportMapFinderFixture$'],
      { env: process.env }
    );
  if (!(await request.get('/api/workspaces/finder-home')).ok()) {
    await libraryJSON(await request.post('/api/onboarding/skip'));
    for (const name of ['music-production-home', 'ep-workspace', 'old-workspace'])
      await libraryJSON(
        await request.post('/api/workspaces/import', {
          data: { path: join(root, 'exports', name) }
        })
      );
  }
  if ((await libraryJSON(await request.get('/api/workspaces/finder-child'))).parent_id) return;
  await libraryJSON(
    await request.patch('/api/workspaces/finder-stale', { data: { parent_id: 'finder-home' } })
  );
  const plan = await libraryJSON(
    await request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: 'Records' }
    })
  );
  const manager = plan.agents[0];
  const body = await libraryJSON(
    await request.post('/api/workspaces', {
      data: {
        name: 'Records',
        kind: 'group',
        parent_id: 'finder-home',
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
  await libraryJSON(
    await request.patch('/api/workspaces/finder-child', {
      data: { parent_id: (body.folder || body.workspace || body).id }
    })
  );
});
const find = (page: Page) => page.getByRole('button', { name: 'Find a project', exact: true });
async function search(page: Page) {
  await find(page).click();
  const dialog = page.getByRole('dialog', { name: 'Find a project' });
  await dialog.getByRole('searchbox').fill('Night Drive');
  await expect(dialog.getByRole('button', { name: /Night Drive/ })).toHaveCount(2);
  return dialog;
}
test('find a late-page song, distinguish duplicate names and reveal the existing nested building', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  const first = await libraryJSON(await request.get(`${base}/projects`));
  expect(first.next_cursor).toBeTruthy();
  expect(first.rows.map((r: any) => r.name)).not.toContain('Night Drive');
  await page.goto(home);
  await find(page).click();
  const dialog = page.getByRole('dialog', { name: 'Find a project' });
  await expect(dialog.locator('li')).toHaveCount(25);
  await dialog.getByRole('button', { name: 'More projects' }).press('Enter');
  await expect(dialog.getByRole('button', { name: /^Night Drive/ })).toHaveCount(2);
  await dialog.getByRole('searchbox').fill('Night Drive');
  await expect(dialog.locator('li')).toHaveCount(2);
  await dialog.screenshot({ path: join(evidence, 'finder.png') });
  await dialog.getByLabel('Recorded stage').selectOption('mixing');
  const ep = dialog.getByRole('button', { name: /Night Drive.*On map.*EP/ });
  await expect(dialog.locator('li')).toHaveCount(1);
  await ep.press('Enter');
  const card = page.locator('[data-cmd-song-card-slot]');
  await expect(card.getByRole('heading', { name: 'Night Drive' })).toBeVisible();
  await expect(page.locator('.ws-map-tile[data-ws-id="finder-child"]')).toHaveAttribute(
    'aria-pressed',
    'true'
  );
  await page.getByRole('button', { name: 'Group projects by folder' }).click();
  const region = page.getByRole('region', { name: 'Workspaces by folder' });
  const tile = region.locator('[data-folder-workspace="finder-child"]');
  await tile.locator('xpath=ancestor::details/summary').click();
  await find(page).click();
  await ep.press('Enter');
  await expect(tile).toBeVisible();
  await expect(tile).toBeFocused();
});

test('read the recap, visit its notes and workspace, and return to the same selection and camera', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.goto(home);
  const dialog = await search(page);
  await dialog.getByRole('button', { name: /Night Drive.*On map.*EP/ }).click();
  const card = page.locator('[data-cmd-song-card-slot]');
  const resume = card.locator('.music-map-resume');
  await expect(resume).toContainText('Kept take 3');
  await expect(resume).toContainText('Comp the bridge.');
  await expect(resume).toContainText('Observed file save:');
  await card.screenshot({ path: join(evidence, 'resume-card.png') });
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  // Not just the auto-framed viewport: the producer panned after selection.
  await page.evaluate(() => (window as any).OriWorkspaceMap.panBy(45, 30));
  const before = await camera();
  await resume.getByRole('button', { name: 'View session notes' }).click();
  await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Night Drive');
  await expect(page.locator('.project-library-session-history')).toContainText('Kept take 3');
  await page.goBack();
  await expect(resume).toContainText('Kept take 3');
  expect(await camera()).toEqual(before);
  await resume.getByRole('button', { name: 'Open workspace' }).click();
  await expect(page).toHaveURL(/\/workspaces\/ep-workspace(?:[?#]|$)/);
  await page.goBack();
  await expect(resume).toContainText('Kept take 3');
  expect(await camera()).toEqual(before);
  await find(page).click();
  await expect(dialog.getByRole('searchbox')).toHaveValue('Night Drive');
});

test('inspect the demo, cancel at explicit file choice, and explain blocked previews', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await page.goto(home);
  const dialog = await search(page);
  const snapshot = async () =>
    libraryJSON(await request.get(`${base}/projects?folder_sections=1&text=Night`));
  const before = await snapshot();
  await dialog.getByRole('button', { name: /Night Drive.*Library only.*Demo/ }).click();
  const preview = dialog.getByRole('region', { name: 'Project preview' });
  await preview.screenshot({ path: join(evidence, 'demo-preview.png') });
  await preview.getByRole('button', { name: 'Set up on map' }).click();
  const sheet = page.locator('[data-library-sheet="finder-home"]');
  const radios = sheet.getByRole('radiogroup', { name: 'Project file for Night Drive' });
  await expect(radios.getByRole('radio')).toHaveCount(2);
  await expect(radios.locator(':checked')).toHaveCount(0);
  await sheet.screenshot({ path: join(evidence, 'file-choice.png') });
  await sheet.getByRole('button', { name: 'Cancel' }).click();
  await expect(sheet).toHaveCount(0);
  expect(await snapshot()).toEqual(before);
  await find(page).click();
  for (const [name, reason] of [
    ['Other format', '(ableton)'],
    ['Offline song', 'last scan did not find']
  ]) {
    await dialog.getByRole('searchbox').fill(name);
    await dialog.getByRole('button', { name: new RegExp(name) }).click();
    await expect(preview).toContainText(reason);
    await expect(preview.getByRole('button', { name: 'Set up on map' })).toHaveCount(0);
  }
});

test('a narrow keyboard round trip and file-choice cancellation leave no work or layout writes', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
  const capture = async () => {
    const { workspaces, agents, positions } = await musicHomeSnapshot(request);
    const list = await libraryJSON(
      await request.get(`${base}/projects?text=Night&folder_sections=1`)
    );
    return {
      positions,
      projects: list.rows.map((r: any) => [r.id, r.workspace_id, r.connection]),
      revision: list.revision,
      tickets: workspaces.map((w: any) => [w.id, w.parent_id, w.ticket_counts, w.agents]),
      agents: agents.map((a: any) => a.name),
      sources: sourceHashes(join(sandbox, 'finder-fixture', 'Songs'))
    };
  };
  const before = await capture();
  expect(before.projects).toEqual([
    ['Demo', undefined, 'catalog_only'],
    ['EP', 'finder-child', 'connected']
  ]);
  const writes: string[] = [];
  page.on('request', r => {
    const p = new URL(r.url()).pathname;
    if (
      !['GET', 'HEAD', 'OPTIONS'].includes(r.method()) &&
      /(?:workspace-map|library|tasks|tickets|chat|completions|activation|scans)/.test(p)
    )
      writes.push(`${r.method()} ${p}`);
  });
  await page.goto(home);
  await page.getByRole('button', { name: 'Group projects by folder' }).click();
  await find(page).press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Find a project' });
  await expect(dialog.getByRole('searchbox')).toBeFocused();
  await dialog.getByRole('searchbox').fill('Night Drive');
  const ep = dialog.getByRole('button', { name: /Night Drive.*On map/ });
  await ep.press('Enter');
  const resume = page.locator('.music-map-resume');
  await expect(resume).toContainText('Kept take 3');
  await resume.getByRole('button', { name: 'Open workspace' }).press('Enter');
  await expect(page).toHaveURL(/\/ep-workspace/);
  await page.goBack();
  await expect(page.getByRole('region', { name: 'Workspaces by folder' })).toBeVisible();
  await expect(resume).toContainText('Comp the bridge.');
  await find(page).press('Enter');
  await expect(dialog.getByRole('searchbox')).toHaveValue('Night Drive');
  await dialog.getByRole('button', { name: /Night Drive.*Library only/ }).press('Enter');
  const setup = dialog.getByRole('button', { name: 'Set up on map' });
  await expect(setup).toBeInViewport();
  await dialog.screenshot({ path: join(evidence, 'narrow-preview.png') });
  await setup.press('Enter');
  const sheet = page.locator('[data-library-sheet]');
  await expect(sheet.getByRole('radio')).toHaveCount(2);
  await sheet.getByRole('button', { name: 'Cancel' }).press('Enter');
  await expect(sheet).toHaveCount(0);
  expect(await capture()).toEqual(before);
  expect(writes).toEqual([]);
});

test('search unavailable is not no matches; a reviewed disconnect invalidates both search and return', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await page.goto(home);
  const dialog = await search(page);
  const input = dialog.getByRole('searchbox');
  await input.fill('not in the catalog');
  await expect(dialog.getByRole('status')).toHaveText('No matching projects.');
  await page.route(
    '**/library/projects?*',
    r => r.fulfill({ status: 503, json: { message: 'Read unavailable' } }),
    { times: 1 }
  );
  await dialog.getByRole('button', { name: 'Search', exact: true }).press('Enter');
  await expect(dialog.getByRole('status')).toContainText('Search unavailable');
  await input.fill('Old mix');
  const old = dialog.getByRole('button', { name: /Old mix.*On map/ });
  await old.click();
  const resume = page.locator('.music-map-resume');
  await expect(resume).toContainText('No session note yet');
  await resume.getByRole('button', { name: 'Open workspace' }).click();
  await expect(page).toHaveURL(/old-workspace/);
  const second = await page.context().newPage();
  await installLocalCdn(second);
  await second.goto(home);
  await find(second).click();
  const waiting = second.getByRole('dialog', { name: 'Find a project' });
  await waiting.getByRole('searchbox').fill('Old mix');
  const stale = waiting.getByRole('button', { name: /Old mix.*On map/ });
  await expect(stale).toBeVisible();
  // A separate operator changes the link, after both real search reads. Keep
  // the workspace itself; never bypass the Home's required disconnect review.
  const program = '/api/workspaces/finder-home/assistant-program';
  const { state_revision } = await libraryJSON(await request.get(program));
  const review = await libraryJSON(
    await request.post(`${program}/disconnect/review`, {
      data: { project_workspace_id: 'finder-stale', state_revision }
    })
  );
  await libraryJSON(
    await request.post(`${program}/disconnect/commit`, {
      data: { token: review.token, idempotency_key: 'fixture-disconnect' }
    })
  );
  await stale.press('Enter');
  await expect(waiting.getByRole('status')).toContainText('link changed');
  await expect(second.locator('.music-map-resume')).toHaveCount(0);
  await expect(waiting.getByRole('button', { name: 'Set up on map' })).toHaveCount(0);
  await waiting.screenshot({ path: join(evidence, 'stale-link.png') });
  await second.close();
  await page.goBack();
  await expect(page.locator('[data-music-map-notice]')).toContainText(
    'previous selection is unavailable'
  );
  await expect(resume).toHaveCount(0);
  await find(page).press('Enter');
  await expect(input).toHaveValue('Old mix');
});
