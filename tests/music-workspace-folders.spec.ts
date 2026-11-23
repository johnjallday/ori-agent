import { expect, test, type Page } from '@playwright/test';
import { join } from 'node:path';
import { musicHomeSnapshot } from './helpers/music-home-placement';
import { installLocalCdn } from './helpers/offline-cdn';

const evidence = 'tasks/evidence-music-workspace-folder-context';
test.skip(!process.env.ORI_MUSIC_FOLDER_MAP, 'requires the isolated seven-project Music Home');

async function groupFixture(page: Page) {
  await installLocalCdn(page);
  const snapshot = await musicHomeSnapshot(page.request);
  const home = snapshot.workspaces.find(
    w => w.group_template?.plugin_id === 'music-project-management'
  );
  const group = snapshot.workspaces.find(
    w => w.parent_id === home?.id && w.name === 'Ori Music Demo'
  );
  expect(group).toBeTruthy();
  return { snapshot, group: group!, home: home! };
}

test('group Overview keeps source folders with their real member rows', async ({ page }) => {
  const { group } = await groupFixture(page);
  await page.goto(`/workspaces/${group.folder_slug}?mode=details`);
  const region = page.getByRole('region', { name: 'Linked workspaces', exact: true });
  for (const [name, label] of [
    ['Track-1', 'Picker Songs'],
    ['Album-1', 'Albums']
  ]) {
    const open = region.getByRole('button', { name: `Open ${name}`, exact: true });
    await expect(open).toHaveAccessibleDescription(`Source folder: ${label}`);
    await expect(open.locator('[data-member-folder]')).toBeVisible();
  }
  await expect(region.locator('[data-member-id]')).toHaveCount(7);
  const tree = await (await page.request.get('/api/workspaces?tree=true')).json();
  const rows = (nodes: any[]): any => {
    for (const node of nodes) {
      if (node.id === group.id) return node.children;
      const found = rows(node.children || []);
      if (found) return found;
    }
  };
  expect(
    await region
      .locator('[data-member-id]')
      .evaluateAll(els => els.map(e => e.getAttribute('data-member-id')))
  ).toEqual(rows(tree.workspaces || tree.folders).map((r: any) => r.id));
  await region.screenshot({ path: join(evidence, 'group-overview.png') });
});
