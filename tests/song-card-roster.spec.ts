import { expect, test, type APIRequestContext } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

// Song card and Home roster journey (tasks/prd-song-card-and-home-roster.md):
// map tile facts → selected-context card → Workspace Details card → Home
// Details roster. It needs a seeded Music Production Home with opened songs
// and project details on (see tasks/test-guide-song-card-and-home-roster.md):
//
//   ORI_SONG_CARD_ACCEPTANCE=1 PLAYWRIGHT_BASE_URL=http://localhost:8952 \
//     npx playwright test tests/song-card-roster.spec.ts --project=chromium --workers=1
//
// Not part of CI: a seeded sandbox takes the REAPER and Music candidates.
const ENABLED = process.env.ORI_SONG_CARD_ACCEPTANCE === '1';
const EVIDENCE = process.env.ORI_SONG_CARD_EVIDENCE_DIR || '';

test.skip(!ENABLED, 'requires a seeded Music Production Home sandbox (ORI_SONG_CARD_ACCEPTANCE=1)');

type RosterRow = {
  workspace_id: string;
  title: string;
  keeper_agent_name?: string;
  facts?: { tempo_bpm?: number; length_seconds?: number } | null;
  workspace_route?: string;
};

type TreeNode = {
  id: string;
  name: string;
  folder_slug?: string;
  group_template?: { kind?: string };
  children?: TreeNode[];
};

async function musicHome(request: APIRequestContext) {
  const tree = await (await request.get('/api/workspaces?tree=true')).json();
  const nodes: TreeNode[] = [];
  const walk = (list: TreeNode[]) =>
    list.forEach(node => {
      nodes.push(node);
      walk(node.children || []);
    });
  walk(tree.workspaces || tree.folders || []);
  const home = nodes.find(node => node.group_template?.kind === 'managed_home');
  expect(home, 'a managed Music Production Home').toBeTruthy();
  const roster = await (
    await request.get(`/api/workspaces/${home!.id}/assistant-program/library/roster`)
  ).json();
  return { home: home!, rows: (roster.rows || []) as RosterRow[] };
}

async function shot(page: import('@playwright/test').Page, name: string) {
  if (!EVIDENCE) return;
  mkdirSync(EVIDENCE, { recursive: true });
  await page.screenshot({ path: join(EVIDENCE, `${name}.png`), fullPage: false });
}

test('a song reads the same on the map, its Details page and the Home roster', async ({
  page,
  request
}) => {
  const { home, rows } = await musicHome(request);
  const song = rows.find(row => row.facts?.tempo_bpm);
  expect(song, 'a connected song with project details').toBeTruthy();
  const tempo = `${Math.round(song!.facts!.tempo_bpm! * 10) / 10} BPM`;

  // 1. Map tile: facts appended to the meta line.
  await page.goto('/?view=map');
  const tile = page.locator(`.ws-map-tile[data-ws-id="${song!.workspace_id}"]`);
  await expect(tile).toBeVisible({ timeout: 20_000 });
  await expect(tile.locator('.ws-map-tile-meta.has-song-facts')).toContainText(tempo, {
    timeout: 20_000
  });

  // 2. Selected context: the shared card at the top.
  await tile.click();
  const contextCard = page.locator('[data-song-card-mount] .song-card');
  await expect(contextCard).toBeVisible({ timeout: 10_000 });
  await expect(contextCard.locator('.song-card-title')).toHaveText(song!.title);
  await expect(contextCard.locator('.song-card-facts')).toContainText(tempo);
  await expect(contextCard.locator('button')).toHaveCount(0);
  await shot(page, 'journey-01-map-card');

  // 3. Workspace Details: the same card above the Command view.
  await page.goto(song!.workspace_route || `/workspaces/${song!.workspace_id}`);
  const detailsCard = page.locator('#workspaceSongCardMount .song-card');
  await expect(detailsCard).toBeVisible({ timeout: 20_000 });
  await expect(detailsCard.locator('.song-card-title')).toHaveText(song!.title);
  await expect(detailsCard.locator('.song-card-home-link')).toHaveText(home.name);
  await expect(detailsCard.locator('.song-card-open')).toHaveText('Open in Home');
  if (song!.keeper_agent_name) {
    await expect(detailsCard.locator('.song-card-keeper')).toHaveText(
      `Kept by ${song!.keeper_agent_name}`
    );
  }
  await expect(detailsCard.locator('.song-card-status')).toContainText(tempo);
  await shot(page, 'journey-02-details-card');

  // 4. Home Details: every connected song, one row each, title opens the song.
  await page.goto(`/workspaces/${home.folder_slug || home.id}`);
  const roster = page.locator('#workspace-detail-song-roster .song-roster');
  await expect(roster).toBeVisible({ timeout: 20_000 });
  await expect(roster.locator('.song-roster-row')).toHaveCount(Math.min(rows.length, 10));
  await expect(roster.locator('.song-roster-song').first()).toHaveText(rows[0].title);
  await shot(page, 'journey-03-home-roster');
  await roster.locator('a.song-roster-song', { hasText: song!.title }).click();
  await expect(page).toHaveURL(new RegExp(`${song!.workspace_route || song!.workspace_id}$`));
});
