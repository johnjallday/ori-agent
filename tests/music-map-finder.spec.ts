import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { libraryJSON } from './helpers/project-library-browser-fixture';
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
  const root = join(realpathSync(sandbox), 'finder-fixture');
  if (!existsSync(root))
    execFileSync(
      'go',
      ['test', './internal/projectlibrary', '-count=1', '-run', '^TestExportMapFinderFixture$'],
      { env: process.env }
    );
  if (!(await request.get('/api/workspaces/finder-home')).ok()) {
    await libraryJSON(await request.post('/api/onboarding/skip'));
    for (const name of ['music-production-home', 'ep-workspace'])
      await libraryJSON(
        await request.post('/api/workspaces/import', {
          data: { path: join(root, 'exports', name) }
        })
      );
  }
  if ((await libraryJSON(await request.get('/api/workspaces/finder-child'))).parent_id) return;
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
  const dialog = await search(page);
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
