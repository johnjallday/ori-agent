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
  entry_id: string;
  title: string;
  keeper_agent_name?: string;
  facts?: {
    tempo_bpm?: number;
    length_seconds?: number;
    track_count?: number;
    tempo_varies?: boolean;
  } | null;
  workspace_route?: string;
};

// The exact strings the #791 card, tile and roster printed with the music words
// in Ori. With a Music release that declares `project_card` (PRD #792), the
// package words must reproduce them character for character.
const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`;
function legacyLength(seconds: number) {
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = String(total % 60).padStart(2, '0');
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${secs}` : `${minutes}:${secs}`;
}
function legacyFacts(facts: NonNullable<RosterRow['facts']>, { tile = false } = {}) {
  const parts: string[] = [];
  if (!tile && facts.track_count && facts.track_count > 0)
    parts.push(plural(facts.track_count, 'track'));
  if (facts.tempo_bpm && facts.tempo_bpm > 0) {
    const tempo = `${Math.round(facts.tempo_bpm * 10) / 10} BPM`;
    parts.push(!tile && facts.tempo_varies ? `${tempo}, varies` : tempo);
  }
  if (facts.length_seconds && Math.round(facts.length_seconds) > 0)
    parts.push(legacyLength(facts.length_seconds));
  return parts.join(' · ');
}
// With package words (ORI_SONG_CARD_PACKAGE_WORDS=1) the card must read as #791 did.
const PACKAGE_WORDS = process.env.ORI_SONG_CARD_PACKAGE_WORDS === '1';

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

// PRD #792 FR8 and success metric 1: with the Music release that declares
// `project_card`, the card, the tile, the roster and the status line read
// exactly as #791 printed them.
test('package words reproduce the #791 strings character for character', async ({
  page,
  request
}) => {
  test.skip(!PACKAGE_WORDS, 'requires a Home pinned to a Music release with project_card');
  const { home, rows } = await musicHome(request);
  const song = rows.find(row => row.facts?.tempo_bpm && row.facts?.track_count);
  expect(song, 'a connected song with tempo and track count').toBeTruthy();
  const facts = song!.facts!;

  await page.goto('/?view=map');
  const tile = page.locator(`.ws-map-tile[data-ws-id="${song!.workspace_id}"]`);
  await expect(tile.locator('.ws-map-tile-meta.has-song-facts')).toContainText(
    legacyFacts(facts, { tile: true }),
    { timeout: 20_000 }
  );

  await page.goto(song!.workspace_route || `/workspaces/${song!.workspace_id}`);
  const card = page.locator('#workspaceSongCardMount .song-card');
  await expect(card).toBeVisible({ timeout: 20_000 });
  await expect(card).toHaveAttribute('aria-label', `Song: ${song!.title}`);
  await expect(card.locator('.song-card-eyebrow')).toHaveText('Song');
  await expect(card.locator('.song-card-facts')).toHaveText(legacyFacts(facts));
  await expect(card.locator('.song-card-status')).toHaveText(
    new RegExp(`^${legacyFacts(facts).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}; saved`)
  );
  await shot(page, 'words-01-details-card');

  await page.goto(`/workspaces/${home.folder_slug || home.id}`);
  const roster = page.locator('#workspace-detail-song-roster .song-roster');
  await expect(roster.locator('.song-roster-title')).toHaveText('Songs and assistants');
  await expect(roster).toHaveAttribute('aria-label', 'Songs and assistants');
  await shot(page, 'words-02-home-roster');
});

// PRD #792 FR12, FR24: the details block reads the entry's fields, and a
// Manager suggestion saved through the direct /tool path (no model) is
// accepted on the card with a before-and-after confirmation.
test('the card reads its details and accepts a Manager suggestion', async ({ page, request }) => {
  const { home, rows } = await musicHome(request);
  const song = rows[0];
  expect(song, 'a connected song').toBeTruthy();
  const homeRecord = await (await request.get(`/api/workspaces/${home.id}`)).json();
  const manager = (homeRecord.agent_instances || []).find(
    (instance: { role_id?: string }) => instance.role_id === 'portfolio_manager'
  );
  expect(manager?.name, 'the Home Manager').toBeTruthy();
  const detail = await (
    await request.get(
      `/api/workspaces/${home.id}/assistant-program/library/projects/${song.entry_id}`
    )
  ).json();
  const next = `Print stems for the mixer ${Date.now().toString(36)}`;
  const saved = await request.post('/api/chat', {
    data: {
      agent_name: manager.name,
      route_context: { surface: 'workspace_detail', workspace_id: home.id },
      question: `/tool home_library_propose_fields ${JSON.stringify({
        entry_id: song.entry_id,
        fields_revision: detail.row.fields_revision,
        next_action: next,
        deliverables: ['Stems v2'],
        reason: '<b>Untrusted</b> journey note',
        request_key: `journey-fields-${next}`
      })}`
    }
  });
  expect(saved.ok(), await saved.text()).toBeTruthy();

  await page.goto(song.workspace_route || `/workspaces/${song.workspace_id}`);
  const card = page.locator('#workspaceSongCardMount .song-card');
  await expect(card.locator('.song-card-details')).toBeVisible({ timeout: 20_000 });
  const suggestion = card.locator('.song-card-suggestion').first();
  await expect(suggestion).toContainText(`Suggested by ${manager.name}`);
  await expect(suggestion).toContainText(`Next action → ${next}`);
  await expect(suggestion).toContainText('Reason (untrusted note): <b>Untrusted</b> journey note');
  await shot(page, 'fields-01-card-suggestion');

  await suggestion.getByRole('button', { name: 'Accept' }).click();
  const dialog = page.locator('dialog.song-card-confirm');
  await expect(dialog).toContainText(`→ ${next}`);
  await expect(dialog).toContainText('Deliverables:');
  await shot(page, 'fields-02-accept-dialog');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect(card.locator('[data-detail="next_action"]')).toContainText(next, {
    timeout: 20_000
  });
  await expect(card.locator('.song-card-suggestion')).toHaveCount(0);
  await shot(page, 'fields-03-card-after-accept');

  await page.goto(`/workspaces/${home.folder_slug || home.id}`);
  const row = page.locator('#workspace-detail-song-roster .song-roster-row', {
    hasText: song.title
  });
  await expect(row.locator('.song-roster-next')).toContainText(next.slice(0, 40), {
    timeout: 20_000
  });
});
