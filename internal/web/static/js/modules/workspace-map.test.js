import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8');

// Load the IIFE in a sandbox and grab the exposed pure layout function.
function loadComputeLayout() {
  const window = {};
  vm.runInNewContext(source, { window, document: {} }, { filename: 'workspace-map.js' });
  return window.OriWorkspaceMap.computeLayout;
}

// Load the IIFE and grab the full exposed API (used for meta-rendering tests).
function loadOriWorkspaceMap() {
  const window = {};
  vm.runInNewContext(source, { window, document: {} }, { filename: 'workspace-map.js' });
  return window.OriWorkspaceMap;
}

function posById(layout) {
  const map = {};
  layout.tiles.forEach(t => {
    map[t.id] = { col: t.col, row: t.row };
  });
  return map;
}

test('layout is deterministic regardless of input order (refresh stability)', () => {
  const computeLayout = loadComputeLayout();
  const wss = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }];
  const forward = computeLayout(wss);
  const reversed = computeLayout([...wss].reverse());
  assert.deepEqual(posById(forward), posById(reversed));
});

test('no two tiles overlap', () => {
  const computeLayout = loadComputeLayout();
  const wss = Array.from({ length: 14 }, (_, i) => ({ id: 'w' + i }));
  const layout = computeLayout(wss, { maxCols: 5 });
  const seen = new Set();
  layout.tiles.forEach(t => {
    const key = t.col + ',' + t.row;
    assert.ok(!seen.has(key), 'overlap at ' + key);
    seen.add(key);
  });
});

test('group members cluster inside their district; group is not a tile', () => {
  const computeLayout = loadComputeLayout();
  const wss = [
    { id: 'g', kind: 'group', name: 'Marketing' },
    { id: 'm1', parent_id: 'g' },
    { id: 'm2', parent_id: 'g' },
    { id: 'm3', parent_id: 'g' },
    { id: 's' } // standalone
  ];
  const layout = computeLayout(wss);
  assert.equal(layout.districts.length, 1);
  const d = layout.districts[0];
  const members = layout.tiles.filter(t => t.groupId === 'g');
  assert.equal(members.length, 3);
  members.forEach(t => {
    assert.ok(t.col >= d.col && t.col < d.col + d.w, 'member col within district');
    assert.ok(t.row >= d.row && t.row < d.row + d.h, 'member row within district');
  });
  assert.ok(
    layout.tiles.some(t => t.id === 's'),
    'standalone rendered'
  );
  assert.ok(!layout.tiles.some(t => t.id === 'g'), 'group itself is a district, not a tile');
});

test('empty input yields no tiles but a non-zero grid', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([]);
  assert.equal(layout.tiles.length, 0);
  assert.ok(layout.rows >= 1);
});

test('single workspace sits at the origin cell', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([{ id: 'only' }]);
  assert.equal(layout.tiles.length, 1);
  assert.equal(layout.tiles[0].col, 0);
  assert.equal(layout.tiles[0].row, 0);
});

test('many workspaces wrap within maxCols across rows', () => {
  const computeLayout = loadComputeLayout();
  const many = Array.from({ length: 12 }, (_, i) => ({ id: 'w' + i }));
  const layout = computeLayout(many, { maxCols: 5 });
  assert.equal(layout.tiles.length, 12);
  assert.ok(layout.cols <= 5, 'never exceeds maxCols');
  assert.ok(layout.rows >= 3, '12 tiles across 5 cols needs 3 rows');
  layout.tiles.forEach(t => assert.ok(t.col < 5, 'col within bounds'));
});

test('a member whose parent is missing degrades to a standalone tile', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([{ id: 'orphan', parent_id: 'ghost' }]);
  assert.equal(layout.tiles.length, 1);
  assert.equal(layout.tiles[0].groupId, '');
});

// ---------------------------------------------------------------------------
// Coordinate engine (#292 FR-1 – FR-30, FR-123)
//
// These replace the old responsive-grid assertions. The map no longer has a
// column count: it has world coordinates, saved anchors win over automatic
// ones, and nothing about placement may depend on how wide the window is.
// ---------------------------------------------------------------------------

function loadWorldLayout() {
  return loadOriWorkspaceMap().computeWorldLayout;
}

function anchorsById(layout) {
  const map = {};
  layout.nodes.forEach(n => {
    map[n.id] = { x: n.x, y: n.y };
  });
  return map;
}

test('a saved coordinate always wins over automatic placement (FR-17)', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'a' }, { id: 'b' }], {
    positions: { a: { x: 5000, y: -2400 } }
  });
  const anchors = anchorsById(layout);
  assert.deepEqual(anchors.a, { x: 5000, y: -2400 });
  assert.ok(anchors.b, 'the unplaced workspace still gets an anchor');
  assert.notDeepEqual(anchors.b, anchors.a);
});

test('automatic placement is identical regardless of API order (FR-19)', () => {
  const computeWorldLayout = loadWorldLayout();
  const wss = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }, { id: 'e' }];
  const forward = computeWorldLayout(wss);
  const reversed = computeWorldLayout([...wss].reverse());
  assert.deepEqual(anchorsById(forward), anchorsById(reversed));
});

test('automatic placement is identical regardless of viewport width (FR-13, FR-19)', () => {
  const computeWorldLayout = loadWorldLayout();
  const wss = Array.from({ length: 9 }, (_, i) => ({ id: 'w' + i }));
  const wide = computeWorldLayout(wss, { viewport: { width: 1600, height: 900 } });
  const narrow = computeWorldLayout(wss, { viewport: { width: 380, height: 640 } });
  assert.deepEqual(anchorsById(wide), anchorsById(narrow));
});

test('no two records share an anchor, saved or automatic (FR-20)', () => {
  const computeWorldLayout = loadWorldLayout();
  const wss = Array.from({ length: 14 }, (_, i) => ({ id: 'w' + i }));
  // Deliberately save one workspace onto the exact cell another would take.
  const seeded = computeWorldLayout(wss);
  const collidingPoint = anchorsById(seeded).w3;
  const layout = computeWorldLayout(wss, { positions: { w9: collidingPoint } });

  const seen = new Set();
  layout.nodes.forEach(n => {
    const key = n.x + ',' + n.y;
    assert.ok(!seen.has(key), 'two buildings at ' + key);
    seen.add(key);
  });
  assert.deepEqual(anchorsById(layout).w9, collidingPoint, 'the saved anchor is the one kept');
});

test('a corrupt or out-of-range saved anchor degrades that record only (FR-22)', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'good' }, { id: 'bad' }, { id: 'wild' }], {
    positions: {
      good: { x: 400, y: 400 },
      bad: { x: 'over there', y: null },
      wild: { x: 9e9, y: 0 }
    }
  });
  const anchors = anchorsById(layout);
  assert.deepEqual(anchors.good, { x: 400, y: 400 });
  assert.ok(anchors.bad && Number.isFinite(anchors.bad.x), 'bad anchor falls back, stays drawn');
  assert.ok(anchors.wild && anchors.wild.x < 1e6, 'out-of-range anchor falls back into safe space');
});

test('a coordinate for a record that is not on the map is ignored, not drawn (FR-26)', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'a' }], {
    positions: { a: { x: 100, y: 100 }, 'trashed-ws': { x: 900, y: 900 } }
  });
  assert.equal(layout.nodes.length, 1);
  assert.deepEqual(anchorsById(layout).a, { x: 100, y: 100 });
});

test('a restored record returns to its retained coordinate (FR-27)', () => {
  const computeWorldLayout = loadWorldLayout();
  const positions = { a: { x: 100, y: 100 }, b: { x: 764, y: 342 } };
  const whileTrashed = computeWorldLayout([{ id: 'a' }], { positions });
  const afterRestore = computeWorldLayout([{ id: 'a' }, { id: 'b' }], { positions });
  assert.deepEqual(anchorsById(whileTrashed).a, { x: 100, y: 100 });
  assert.deepEqual(anchorsById(afterRestore).b, { x: 764, y: 342 });
  assert.deepEqual(
    anchorsById(afterRestore).a,
    { x: 100, y: 100 },
    'restoring a sibling must not disturb anyone else'
  );
});

test('a record created outside the map appears near existing content, not at a distant origin (FR-24)', () => {
  const computeWorldLayout = loadWorldLayout();
  const placed = { a: { x: 4000, y: 4000 }, b: { x: 4176, y: 4000 } };
  const layout = computeWorldLayout([{ id: 'a' }, { id: 'b' }, { id: 'newcomer' }], {
    positions: placed
  });
  const anchors = anchorsById(layout);
  assert.ok(
    anchors.newcomer.y > 4000 && anchors.newcomer.y < 4600,
    'the new workspace lands just below the arranged cluster, not back at the origin'
  );
  assert.ok(anchors.newcomer.x >= 4000 && anchors.newcomer.x < 5000);
});

test('a legacy map with no saved anchors keeps the classic corner origin (FR-18)', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'a' }, { id: 'b' }]);
  const anchors = anchorsById(layout);
  const xs = Object.values(anchors).map(a => a.x);
  const ys = Object.values(anchors).map(a => a.y);
  assert.equal(Math.min(...xs), 26, 'first column sits at the classic 26px pad');
  assert.equal(Math.min(...ys), 26);
});

test('the reserved Personal HQ site gets an anchor but is never a persisted workspace (FR-30)', () => {
  const computeWorldLayout = loadWorldLayout();
  const withSite = computeWorldLayout([{ id: 'a' }], { hqSite: true });
  const withoutSite = computeWorldLayout([{ id: 'a' }], { hqSite: false });
  assert.ok(withSite.hqSite && Number.isFinite(withSite.hqSite.x));
  assert.equal(withoutSite.hqSite, null);
  assert.ok(
    !withSite.nodes.some(n => n.id === '__personal_hq_site__'),
    'the site is not a workspace node'
  );

  // A layout record that tries to persist the reserved id is ignored outright.
  const spoofed = computeWorldLayout([{ id: 'a' }], {
    hqSite: true,
    positions: { __personal_hq_site__: { x: 12, y: 12 } }
  });
  assert.notDeepEqual({ x: spoofed.hqSite.x, y: spoofed.hqSite.y }, { x: 12, y: 12 });
});

test('districts are elastic presentation around member anchors and never rewrite them (FR-84)', () => {
  const computeWorldLayout = loadWorldLayout();
  const wss = [
    { id: 'g', kind: 'group', name: 'Marketing' },
    { id: 'm1', parent_id: 'g' },
    { id: 'm2', parent_id: 'g' }
  ];
  const far = { x: 3000, y: 2000 };
  const layout = computeWorldLayout(wss, { positions: { m2: far } });
  const anchors = anchorsById(layout);
  assert.deepEqual(anchors.m2, far, 'the moved child keeps its exact coordinate');

  const district = layout.districts.find(d => d.id === 'g');
  assert.ok(district, 'the group still renders as a district');
  assert.ok(
    district.x <= far.x && district.y <= far.y,
    'the outline grew around the child rather than pulling it back'
  );
  assert.ok(district.width >= 3000 - district.x, 'outline spans to the moved child');
});

test('an empty group still renders as a selectable minimum-size district (FR-83)', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'g', kind: 'group', name: 'Empty' }]);
  assert.equal(layout.districts.length, 1);
  const district = layout.districts[0];
  assert.ok(district.width > 0 && district.height > 0);
  assert.equal(layout.nodes.length, 0, 'a group is a district, never a building tile');
});

test('content bounds grow around placed content with a viewport of margin (FR-10, FR-11)', () => {
  const computeWorldLayout = loadWorldLayout();
  const viewport = { width: 1000, height: 700 };
  const near = computeWorldLayout([{ id: 'a' }], { viewport });
  const far = computeWorldLayout([{ id: 'a' }], {
    positions: { a: { x: 9000, y: 9000 } },
    viewport
  });

  assert.ok(far.bounds.maxX > near.bounds.maxX, 'the world grew to include the moved building');
  assert.ok(
    far.world.width >= far.bounds.maxX - far.bounds.minX + viewport.width * 2,
    'at least one viewport of margin on each side'
  );
  assert.ok(far.world.minX <= far.bounds.minX - viewport.width);
  assert.ok(far.world.maxY >= far.bounds.maxY + viewport.height);
});

test('computeStats sums enriched agent/task counts and counts groups', () => {
  const { computeStats } = loadOriWorkspaceMap();
  const stats = computeStats([
    { id: 'a', agent_count: 2, open_task_count: 1 },
    { id: 'b', kind: 'group', agent_count: 1, open_task_count: 0 },
    { id: 'c' } // missing fields degrade to 0, not NaN
  ]);
  assert.equal(stats.workspaces, 3);
  assert.equal(stats.agents, 3);
  assert.equal(stats.openTasks, 1);
  assert.equal(stats.groups, 1);
});

test('hqSiteView exposes a missing, skipped, or broken Personal HQ site without treating a valid HQ as missing', () => {
  const { hqSiteView } = loadOriWorkspaceMap();

  const missing = hqSiteView({ valid: false, hq_onboarding_state: 'unseen' });
  assert.equal(missing.show, true);
  assert.equal(missing.repair, false);
  assert.equal(missing.statusLabel, 'Not created');
  assert.equal(missing.showSkip, true);

  const skipped = hqSiteView({ valid: false, hq_onboarding_state: 'skipped' });
  assert.equal(skipped.show, true);
  assert.equal(skipped.showSkip, false);

  const broken = hqSiteView({ valid: false, workspace_id: 'gone' });
  assert.equal(broken.show, true);
  assert.equal(broken.repair, true);
  assert.equal(broken.statusLabel, 'Needs repair');
  assert.equal(broken.showSkip, false);

  assert.equal(hqSiteView({ valid: true, workspace_id: 'hq' }).show, false);
});

test('hqSiteHTML renders a selectable landmark but no workspace or bulk-selection id', () => {
  const { hqSiteHTML, hqSiteView } = loadOriWorkspaceMap();
  const view = hqSiteView({ valid: false, hq_onboarding_state: 'unseen' });
  const html = hqSiteHTML({ col: 1, row: 2 }, '__personal_hq_site__', 3, view);

  assert.match(html, /data-hq-site/);
  assert.match(html, /ws-map-hq-site is-selected/);
  assert.match(html, /Personal HQ/);
  assert.match(html, /Not created/);
  assert.doesNotMatch(html, /data-ws-id/);
  assert.doesNotMatch(html, /data-ws-check/);
});

test('hqOverviewHTML offers setup actions, hides Not now after skip, and offers repair actions for a broken HQ', () => {
  const { hqOverviewHTML, hqSiteView } = loadOriWorkspaceMap();
  const missing = hqOverviewHTML(hqSiteView({ valid: false, hq_onboarding_state: 'unseen' }));
  assert.match(missing, /data-hq-action="build"/);
  assert.match(missing, /Build My HQ/);
  assert.match(missing, /data-hq-action="import"/);
  assert.match(missing, /Import HQ/);
  assert.match(missing, /data-hq-action="skip"/);

  const skipped = hqOverviewHTML(hqSiteView({ valid: false, hq_onboarding_state: 'skipped' }));
  assert.doesNotMatch(skipped, /data-hq-action="skip"/);

  const repair = hqOverviewHTML(hqSiteView({ valid: false, workspace_id: 'gone' }));
  assert.match(repair, /Build replacement HQ/);
  assert.match(repair, /data-hq-action="clear"/);
  assert.doesNotMatch(repair, /data-hq-action="skip"/);
});

test('the New Workspace pad gets its own anchor after every real and reserved site', () => {
  const computeWorldLayout = loadWorldLayout();
  const layout = computeWorldLayout([{ id: 'a' }, { id: 'b' }], { hqSite: true });
  const taken = new Set(layout.nodes.map(n => n.x + ',' + n.y));
  taken.add(layout.hqSite.x + ',' + layout.hqSite.y);
  assert.ok(layout.pad && Number.isFinite(layout.pad.x));
  assert.ok(!taken.has(layout.pad.x + ',' + layout.pad.y), 'the pad never sits under a building');
});

test('tileHTML meta line reflects enriched agent/task counts with correct pluralization', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const plural = tileHTML({
    ws: { id: 'a', name: 'Deep Sea Research', agent_count: 2, open_task_count: 3 },
    col: 0,
    row: 0
  });
  assert.match(plural, /ws-map-tile-meta">2 agents · 3 tasks</);

  const singular = tileHTML({
    ws: { id: 'b', name: 'Solo', agent_count: 1, open_task_count: 1 },
    col: 0,
    row: 0
  });
  assert.match(singular, /ws-map-tile-meta">1 agent · 1 task</);
});

test('tileHTML LED and entry-agent crest reflect active state and entry_agent_name', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const working = tileHTML({
    ws: { id: 'a', name: 'Deep Sea Research', active: true, entry_agent_name: 'Research Lead' },
    col: 0,
    row: 0
  });
  assert.match(working, /ws-map-led is-working/);
  assert.match(working, />Working</);
  assert.match(working, /ws-map-tile-crest/);
  assert.match(working, /entry agent Research Lead/);

  const idle = tileHTML({ ws: { id: 'b', name: 'No Keeper' }, col: 0, row: 0 });
  assert.doesNotMatch(idle, /ws-map-led is-working/);
  assert.match(idle, />Idle</);
  assert.doesNotMatch(idle, /ws-map-tile-crest/);
});

test('tileHTML advertises select-vs-open affordance via aria-pressed and aria-label', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const unselected = tileHTML({ ws: { id: 'a', name: 'Deep Sea Research' }, col: 0, row: 0 }, '');
  assert.match(unselected, /aria-pressed="false"/);
  assert.match(unselected, /Activate to select, double-click to open/);

  const selected = tileHTML({ ws: { id: 'a', name: 'Deep Sea Research' }, col: 0, row: 0 }, 'a');
  assert.match(selected, /aria-pressed="true"/);
  assert.match(selected, /Selected — activate to open/);
});

test('overviewBodyHTML renders entry agent, roster, and tool/skill counts from enriched fields', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({
    id: 'a',
    name: 'Deep Sea Research',
    ops_mode: 'guided',
    entry_agent_name: 'Research Lead',
    agents: ['Research Lead', 'Source Scout'],
    open_task_count: 2,
    mcp_count: 1,
    skill_count: 3
  });

  assert.match(html, /Research Lead/);
  assert.match(html, /Locked · can&#39;t remove/);
  assert.match(html, /Agents · 2/);
  assert.match(html, /ws-map-av/); // roster avatar chips rendered
  assert.match(html, /2 open</);
  assert.match(html, /ws-map-ov-k">Tools · MCP<\/span><span class="ws-map-ov-v">1</);
  assert.match(html, /ws-map-ov-k">Skills<\/span><span class="ws-map-ov-v">3</);
});

test('overviewBodyHTML puts the primary Open action in the hero row and drops the duplicate cog', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({
    id: 'ws-42',
    name: 'Deep Sea Research',
    entry_agent_name: 'Research Lead',
    agents: ['Research Lead']
  });

  // Open button carries the id the click binding reads, and lives inside the hero row (before the first ov-label).
  assert.match(html, /class="ws-map-ov-open" data-ws-open="ws-42"/);
  const heroEnd = html.indexOf('ws-map-ov-label');
  assert.ok(
    html.indexOf('ws-map-ov-open') < heroEnd,
    'Open button should render in the hero row, above the detail rows'
  );
  // The redundant settings cog is gone.
  assert.doesNotMatch(html, /ws-map-ov-cog/);
});

test('overviewBodyHTML falls back to empty-state copy when a workspace has no entry agent or agents', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'a', name: 'Bare Workspace' });

  assert.match(html, /No Commander/);
  assert.match(html, /No agents yet/);
});

test('overviewBodyHTML renders the select-a-workspace placeholder when nothing is selected', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  assert.match(
    overviewBodyHTML(null),
    /Select a workspace to see its agents, tasks, tools, and skills\./
  );
});

test('tileHTML renders an (unchecked) multi-select checkbox affordance', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const html = tileHTML({ ws: { id: 'w1', name: 'Ops' }, col: 0, row: 0 }, '', 0);
  assert.match(html, /class="ws-map-tile-check" data-ws-check role="checkbox"/);
  assert.match(html, /aria-checked="false"/);
  // Nothing is multi-selected by default, so the tile is not marked is-multi.
  assert.doesNotMatch(html, /class="ws-map-tile[^"]*is-multi/);
});

test('overviewBodyHTML shows the workspace description, or an empty-state note', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const withDesc = overviewBodyHTML({
    id: 'a',
    name: 'Ops',
    description: 'Coordinates field logistics.'
  });
  assert.match(withDesc, /class="ws-map-ov-desc"[^>]*>Coordinates field logistics\./);
  const noDesc = overviewBodyHTML({ id: 'b', name: 'Bare' });
  assert.match(noDesc, /class="ws-map-ov-desc is-empty">No description yet\./);
});

test('overviewBodyHTML renders linked folder state from map metadata', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML(
    { id: 'a', name: 'Ops' },
    {
      metadata: {
        folderDisplayById: {
          a: {
            linked: true,
            badgeLabel: '2 folders linked',
            badgeClass: 'is-linked',
            detail: '/tmp/ops (+1 more)',
            detailTitle: '/tmp/ops | /tmp/ops-extra'
          }
        }
      }
    }
  );

  assert.match(html, /ws-map-folder is-linked/);
  assert.match(html, />2 folders linked</);
  assert.match(html, /\/tmp\/ops \(\+1 more\)/);
  assert.match(html, /title="\/tmp\/ops \| \/tmp\/ops-extra"/);
});

test('overviewBodyHTML renders unlinked folder state from map metadata', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML(
    { id: 'a', name: 'Ops' },
    {
      metadata: {
        folderDisplayById: {
          a: {
            linked: false,
            badgeLabel: 'No folder linked',
            badgeClass: 'is-unlinked',
            detail: 'No local folder attached.'
          }
        }
      }
    }
  );

  assert.match(html, /ws-map-folder is-unlinked/);
  assert.match(html, /No folder linked/);
  assert.match(html, /No local folder attached\./);
});

test('overviewBodyHTML renders filterable and removable tag chips from map metadata', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML(
    { id: 'song', name: 'Song' },
    {
      metadata: {
        tagsById: {
          song: ['music', 'reaper', 'client:acme', 'archive', 'mix']
        }
      }
    }
  );

  assert.match(html, /class="ws-map-tags"/);
  assert.match(html, /data-ws-tag-filter="music"/);
  assert.match(html, /data-ws-tag-remove="song"/);
  assert.match(html, /data-ws-tag="reaper"/);
  assert.match(html, /\+1 more/);
});

test('overviewBodyHTML renders group child previews from map metadata', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML(
    { id: 'grp-1', name: 'Research Fleet', kind: 'group' },
    {
      metadata: {
        groupPreviewById: {
          'grp-1': {
            childCount: 4,
            previewNames: ['Alpha', 'Beta', 'Gamma'],
            overflowCount: 1
          }
        }
      }
    }
  );

  assert.match(html, /Group Preview/);
  assert.match(html, /4 workspaces/);
  assert.match(html, /Alpha · Beta · Gamma \+1 more/);
});

test('overviewBodyHTML offers a delete action carrying the workspace id', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'ws-42', name: 'Deep Sea Research' });
  assert.match(html, /class="ws-map-ov-delete" data-ws-delete="ws-42"/);
  assert.match(html, /Delete workspace/);
});

test('overviewBodyHTML labels the delete action "Delete group" for group workspaces', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'grp-1', name: 'Research Fleet', kind: 'group' });
  assert.match(html, /data-ws-delete="grp-1"[^>]*>✕ Delete group</);
});

test("overviewBodyHTML shows the selected workspace's local backlog count and an Open Backlog action (FR58)", () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'ws-42', name: 'Deep Sea Research', backlog_count: 5 });
  assert.match(html, /ws-map-ov-k">Backlog<\/span>/);
  assert.match(html, /data-ws-open-backlog="ws-42"/);
  assert.match(html, /<span class="ws-map-ov-v">5<\/span> Open Backlog/);
});

test('overviewBodyHTML shows a zero backlog count without hiding the Open Backlog action', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'ws-42', name: 'Deep Sea Research' });
  assert.match(html, /data-ws-open-backlog="ws-42"/);
  assert.match(html, /<span class="ws-map-ov-v">0<\/span> Open Backlog/);
});

test('overviewBodyHTML never renders backlog item content, edit, promote, or delete controls (FR58-59, no global aggregate/edit surface)', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({
    id: 'ws-42',
    name: 'Deep Sea Research',
    backlog_count: 3,
    backlog_items: [{ id: 'b1', description: 'should never leak onto the global map' }]
  });
  assert.ok(
    !html.includes('should never leak onto the global map'),
    'no individual backlog items rendered'
  );
  assert.ok(!/data-cmd-backlog-promote/.test(html), 'no promote control');
  assert.ok(!/data-cmd-backlog-delete/.test(html), 'no delete control');
  assert.ok(!/data-cmd-backlog-edit/.test(html), 'no edit control');
});

test('tileHTML marks the designated Personal HQ workspace with a badge, aria-label, and class', () => {
  const api = loadOriWorkspaceMap();
  api._setHQWorkspaceIdForTest('ws-hq');

  const hqTile = api.tileHTML({ ws: { id: 'ws-hq', name: 'Command Post' }, col: 0, row: 0 });
  assert.match(hqTile, /ws-map-tile is-hq/);
  assert.match(hqTile, /ws-map-tile-hq-badge/);
  assert.match(hqTile, />HQ</);
  assert.match(hqTile, /, Personal HQ/);

  const otherTile = api.tileHTML({ ws: { id: 'ws-other', name: 'Side Project' }, col: 0, row: 0 });
  assert.doesNotMatch(otherTile, /is-hq/);
  assert.doesNotMatch(otherTile, /ws-map-tile-hq-badge/);
  assert.doesNotMatch(otherTile, /, Personal HQ/);
});

test('tileHTML shows no HQ badge for any workspace when no HQ is designated', () => {
  const api = loadOriWorkspaceMap();
  api._setHQWorkspaceIdForTest(null);
  const html = api.tileHTML({ ws: { id: 'ws-anything', name: 'Untouched' }, col: 0, row: 0 });
  assert.doesNotMatch(html, /is-hq/);
  assert.doesNotMatch(html, /ws-map-tile-hq-badge/);
});

test('the selection bar is a count plus Clear — group and delete live in the context menu (#317)', () => {
  const { selBarHTML } = loadOriWorkspaceMap();
  const html = selBarHTML();
  assert.match(html, /data-ws-selbar-count/);
  assert.match(html, /data-ws-selbar-clear/);
  assert.doesNotMatch(html, /data-ws-selbar-group/, 'Group moved to the context menu');
  assert.doesNotMatch(html, /data-ws-selbar-delete/, 'Delete moved to the context menu');
});

test('the selection bar is hidden at zero and counts what is checked', () => {
  const { selBarHTML } = loadOriWorkspaceMap();
  const html = selBarHTML();
  assert.match(html, /data-ws-selbar hidden/, 'nothing checked means nothing to show');
  assert.match(html, />0 selected</);
});

// ---------------------------------------------------------------------------
// Blueprint setup status (FR54-55): the Map is an entry point into a
// workspace's own Setup Wizard, never a second copy of its state.

function loadMapWithSetup(workspaceId, status) {
  const window = {};
  vm.runInNewContext(source, { window, document: {} }, { filename: 'workspace-map.js' });
  window.OriWorkspaceMap._setSetupStatusForTest(workspaceId, status);
  return window.OriWorkspaceMap;
}

test('a workspace with no wizard shows no setup row on the Map', () => {
  const map = loadMapWithSetup('ws-1', { applicable: false, state: 'not_applicable' });
  const html = map.overviewBodyHTML({ id: 'ws-1', name: 'Plain' }, {});
  assert.doesNotMatch(html, /data-ws-open-setup/);
  assert.doesNotMatch(html, /Setup required/);
});

test('an unfinished blueprint shows Setup required with a continue action', () => {
  const map = loadMapWithSetup('ws-1', { applicable: true, state: 'in_progress' });
  const html = map.overviewBodyHTML({ id: 'ws-1', name: 'Downloads' }, {});
  assert.match(html, /data-ws-open-setup="ws-1"/);
  assert.match(html, /Setup required/);
  assert.match(html, /Continue setup/);
});

test('a regressed blueprint offers repair, and a ready one offers a view', () => {
  const attention = loadMapWithSetup('ws-1', { applicable: true, state: 'needs_attention' });
  const attentionHTML = attention.overviewBodyHTML({ id: 'ws-1', name: 'Calendar' }, {});
  assert.match(attentionHTML, /Needs attention/);
  assert.match(attentionHTML, /Repair setup/);

  const ready = loadMapWithSetup('ws-2', { applicable: true, state: 'ready' });
  const readyHTML = ready.overviewBodyHTML({ id: 'ws-2', name: 'Calendar' }, {});
  assert.match(readyHTML, /Ready/);
  assert.match(readyHTML, /View setup/);
});

test('the setup row states its workspace and action for a screen reader', () => {
  const map = loadMapWithSetup('ws-1', { applicable: true, state: 'in_progress' });
  const html = map.overviewBodyHTML({ id: 'ws-1', name: 'Downloads Janitor' }, {});
  assert.match(html, /aria-label="Setup required — Continue setup for Downloads Janitor"/);
});

// ---------------------------------------------------------------------------
// Cockpit mount contract (PRD FR15, FR29, FR35, FR36, FR125)
//
// The Home cockpit mounts this same production Map with select-only pointer
// semantics and without the map's own topbar/overview chrome, because the
// workspace-area header and the persistent context rail own those. The legacy
// /workspaces launcher keeps its original behavior until it redirects to Home.
// ---------------------------------------------------------------------------

// A DOM stub just rich enough for mount(): it captures the rendered HTML and
// the listeners bound to each selectable site, so click/keyboard semantics can
// be exercised without a real browser.
function createMapHarness({ tiles = [] } = {}) {
  const bound = new Map();

  function makeEl(attrs = {}, classes = []) {
    const classSet = new Set(classes);
    const listeners = {};
    const el = {
      attrs: { ...attrs },
      listeners,
      classList: {
        contains: c => classSet.has(c),
        toggle: (c, on) => (on ? classSet.add(c) : classSet.delete(c)),
        add: c => classSet.add(c),
        remove: c => classSet.delete(c)
      },
      dataset: {},
      getAttribute: name => (name in el.attrs ? el.attrs[name] : null),
      setAttribute: (name, value) => {
        el.attrs[name] = String(value);
      },
      hasAttribute: name => name in el.attrs,
      addEventListener: (type, fn) => {
        (listeners[type] = listeners[type] || []).push(fn);
      },
      fire: (type, event = {}) => (listeners[type] || []).forEach(fn => fn(event)),
      querySelectorAll: () => [],
      querySelector: () => null
    };
    return el;
  }

  const siteEls = tiles.map(id => {
    const el = makeEl({ 'data-ws-id': id }, ['ws-map-tile']);
    bound.set(id, el);
    return el;
  });

  const container = {
    innerHTML: '',
    clientWidth: 1200,
    hidden: false,
    isConnected: true,
    querySelectorAll: selector => {
      if (selector.includes('data-ws-id')) return siteEls;
      if (selector.includes('.ws-map-tile')) return siteEls;
      return [];
    },
    querySelector: () => null
  };

  return { container, site: id => bound.get(id) };
}

function loadMapForMount() {
  const window = { addEventListener() {} };
  // A selection lazily reads the workspace's setup status. Stub it to a never-
  // settling promise: these tests assert the synchronous mount contract, and a
  // real fetch is neither available nor relevant here.
  const fetch = () => new Promise(() => {});
  vm.runInNewContext(
    source,
    { window, document: { getElementById: () => null }, setTimeout, clearTimeout, fetch },
    { filename: 'workspace-map.js' }
  );
  return window.OriWorkspaceMap;
}

test('cockpit mode renders no second topbar or overview panel (FR15)', () => {
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  assert.doesNotMatch(container.innerHTML, /ws-map-topbar/);
  assert.doesNotMatch(container.innerHTML, /ws-map-overview/);
  // Only the TOPBAR's create button is suppressed (the cockpit header carries
  // it). The in-canvas New Workspace pad stays — FR29 requires the Map itself
  // to offer an obvious New Workspace site.
  assert.doesNotMatch(container.innerHTML, /class="ws-map-create"/);
  assert.match(container.innerHTML, /ws-map-pad[^>]*data-ws-map-create/);
  // The theatre — the part that actually draws the sites — is still there.
  assert.match(container.innerHTML, /ws-map-theatre/);
  assert.match(container.innerHTML, /is-cockpit/);
});

test('the launcher keeps its topbar and overview when chrome is not suppressed', () => {
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  assert.match(container.innerHTML, /ws-map-topbar/);
  assert.match(container.innerHTML, /ws-map-overview/);
});

test('cockpit mode makes no selection on a bare load (FR74)', () => {
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1', 'ws-2'] });
  map.mount(container, {
    workspaces: [
      { id: 'ws-1', name: 'Alpha' },
      { id: 'ws-2', name: 'Beta' }
    ],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  assert.equal(map.getSelectedId(), '');
});

test('the launcher still picks a default selection when auto-select is not suppressed', () => {
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  assert.equal(map.getSelectedId(), 'ws-1');
});

test('select-only: a pointer click selects and reports, and NEVER opens (FR35)', () => {
  const map = loadMapForMount();
  const harness = createMapHarness({ tiles: ['ws-1'] });
  const selected = [];
  const opened = [];
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    onSelect: id => selected.push(id),
    onOpen: id => opened.push(id)
  });
  harness.site('ws-1').fire('click', { target: null });
  assert.deepEqual(selected, ['ws-1']);
  assert.deepEqual(opened, []);
  assert.equal(map.getSelectedId(), 'ws-1');
});

test('select-only: repeated clicks on the SAME site still only select (FR36)', () => {
  const map = loadMapForMount();
  const harness = createMapHarness({ tiles: ['ws-1'] });
  const selected = [];
  const opened = [];
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    onSelect: id => selected.push(id),
    onOpen: id => opened.push(id)
  });
  harness.site('ws-1').fire('click', { target: null });
  harness.site('ws-1').fire('click', { target: null });
  harness.site('ws-1').fire('click', { target: null });
  assert.deepEqual(selected, ['ws-1', 'ws-1', 'ws-1']);
  assert.deepEqual(opened, [], 'a repeat click must not become a hidden open');
});

test('select-only: no double-click opening rule is bound at all (FR36)', () => {
  const map = loadMapForMount();
  const harness = createMapHarness({ tiles: ['ws-1'] });
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  assert.equal(harness.site('ws-1').listeners.dblclick, undefined);
});

test('select-only: Enter on a focused site opens it explicitly (FR125)', () => {
  const map = loadMapForMount();
  const harness = createMapHarness({ tiles: ['ws-1'] });
  const opened = [];
  let defaultPrevented = false;
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    onOpen: id => opened.push(id)
  });
  harness.site('ws-1').fire('keydown', {
    key: 'Enter',
    preventDefault: () => {
      defaultPrevented = true;
    }
  });
  assert.deepEqual(opened, ['ws-1']);
  // The default must be suppressed, or the button would ALSO fire a selecting
  // click and the rail would fight the navigation.
  assert.equal(defaultPrevented, true);
});

test('select-only: keys other than Enter do not open (Space stays a select)', () => {
  const map = loadMapForMount();
  const harness = createMapHarness({ tiles: ['ws-1'] });
  const opened = [];
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    onOpen: id => opened.push(id)
  });
  ['a', 'Escape', 'Tab', ' '].forEach(key => {
    harness.site('ws-1').fire('keydown', { key, preventDefault: () => {} });
  });
  assert.deepEqual(opened, []);
});

test('the select-only tile label advertises Enter-to-open, not double-click', () => {
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  assert.match(container.innerHTML, /Enter to open/);
  assert.doesNotMatch(container.innerHTML, /double-click to open/);
});

test('tiles are spaced so a row cannot collide with the row below it', () => {
  // Tiles measure ~155px tall; the row pitch must exceed that or every meta
  // line is overlapped by the next row's status flag.
  const map = loadMapForMount();
  const { container } = createMapHarness({ tiles: ['ws-1', 'ws-2'] });
  map.mount(container, {
    workspaces: [
      { id: 'ws-1', name: 'A' },
      { id: 'ws-2', name: 'B' }
    ],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  const tops = [...container.innerHTML.matchAll(/top:(\d+)px/g)].map(m => Number(m[1]));
  const pitch = Math.min(...tops.filter(t => t > Math.min(...tops))) - Math.min(...tops);
  if (Number.isFinite(pitch) && pitch > 0) {
    assert.ok(pitch >= 160, `row pitch ${pitch}px must clear the ~155px tile height`);
  }
});

// ---------------------------------------------------------------------------
// Shared layout lifecycle (#292 FR-4, FR-16, FR-23, FR-105)
//
// One layout, loaded once, rendered identically by the Home cockpit and the
// /workspaces launcher. A response that arrives after the map went away is
// dropped rather than painted over whatever is there now.
// ---------------------------------------------------------------------------

function loadMapWithFetch(fetchImpl, windowExtras = {}, consoleImpl) {
  const window = { addEventListener() {}, ...windowExtras };
  vm.runInNewContext(
    source,
    {
      window,
      document: { getElementById: () => null },
      setTimeout,
      clearTimeout,
      fetch: fetchImpl,
      // hasHQFocusIntent parses the query string with it, for the tests that
      // pass a location through windowExtras.
      URLSearchParams,
      console: consoleImpl || { error() {}, warn() {}, log() {} }
    },
    { filename: 'workspace-map.js' }
  );
  return window.OriWorkspaceMap;
}

function jsonResponse(layout) {
  return Promise.resolve({ ok: true, json: () => Promise.resolve({ success: true, layout }) });
}

const flush = async () => {
  for (let i = 0; i < 5; i += 1) await Promise.resolve();
};

function leftTopOf(html, wsId) {
  const tile = html.split('data-ws-id="' + wsId + '"')[1] || '';
  const match = tile.match(/style="left:(-?\d+(?:\.\d+)?)px;top:(-?\d+(?:\.\d+)?)px/);
  return match ? { left: Number(match[1]), top: Number(match[2]) } : null;
}

test('the map paints saved coordinates once the layout resolves (FR-16, FR-17)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      revision: 3,
      snap_to_grid: true,
      positions: { 'ws-1': { x: 1200, y: 900 }, 'ws-2': { x: 1200, y: 1200 } }
    })
  );
  const { container } = createMapHarness({ tiles: ['ws-1', 'ws-2'] });
  const workspaces = [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ];

  map.mount(container, { workspaces, hideChrome: true, selectOnly: true, noAutoSelect: true });
  // Before the layout resolves the world is held back rather than shown at
  // fallback positions it is about to abandon.
  assert.match(container.innerHTML, /is-settling/);
  assert.match(container.innerHTML, /aria-busy="true"/);

  await flush();
  assert.doesNotMatch(container.innerHTML, /is-settling/);
  assert.equal(map.getLayoutState().status, 'ready');
  assert.equal(map.getLayoutState().revision, 3);

  // Both saved anchors keep their exact 300-unit vertical offset on screen.
  const one = leftTopOf(container.innerHTML, 'ws-1');
  const two = leftTopOf(container.innerHTML, 'ws-2');
  assert.ok(one && two);
  assert.equal(two.top - one.top, 300);
  assert.equal(two.left, one.left);
});

test('Home and the /workspaces launcher render one shared layout (FR-4)', async () => {
  const layoutRequests = [];
  const map = loadMapWithFetch(url => {
    if (String(url).includes('/api/workspace-map/layout')) layoutRequests.push(url);
    return jsonResponse({
      schema_version: 1,
      revision: 1,
      positions: { 'ws-1': { x: 500, y: 400 } }
    });
  });
  const workspaces = [{ id: 'ws-1', name: 'Alpha' }];

  const home = createMapHarness({ tiles: ['ws-1'] });
  map.mount(home.container, { workspaces, hideChrome: true, selectOnly: true, noAutoSelect: true });
  await flush();

  const launcher = createMapHarness({ tiles: ['ws-1'] });
  map.mount(launcher.container, { workspaces });
  await flush();

  assert.equal(layoutRequests.length, 1, 'the layout is fetched once and shared, not per surface');
  assert.deepEqual(
    leftTopOf(home.container.innerHTML, 'ws-1'),
    leftTopOf(launcher.container.innerHTML, 'ws-1')
  );
});

test('a layout response that lands after unmount is dropped (FR-16)', async () => {
  let resolveFetch;
  const map = loadMapWithFetch(
    () =>
      new Promise(resolve => {
        resolveFetch = resolve;
      })
  );
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  assert.notEqual(container.innerHTML, '');

  map.unmount(container);
  assert.equal(container.innerHTML, '');

  resolveFetch({ ok: true, json: () => Promise.resolve({ layout: { positions: {} } }) });
  await flush();
  assert.equal(container.innerHTML, '', 'a stale response must not repaint a map that went away');
});

test('an unavailable layout still draws a usable map, marked read-only (FR-105)', async () => {
  const map = loadMapWithFetch(() => Promise.resolve({ ok: false, status: 503 }));
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  await flush();

  assert.equal(map.getLayoutState().status, 'unavailable');
  assert.equal(map.getLayoutState().readOnly, true);
  assert.match(container.innerHTML, /is-readonly/);
  assert.doesNotMatch(container.innerHTML, /is-settling/);
  // The buildings are still drawn and still reachable.
  assert.match(container.innerHTML, /data-ws-id="ws-1"/);
});

// ---------------------------------------------------------------------------
// Camera transforms (#292 FR-31 – FR-46, FR-123)
//
// Pure in, pure out. Pointer-centred zoom in particular is the kind of maths
// that looks right in a browser until the day it drifts, so it is pinned here.
// ---------------------------------------------------------------------------

const VIEWPORT = { width: 1000, height: 600 };

function loadCamera() {
  return loadOriWorkspaceMap().camera;
}

test('screen and world coordinates round-trip through the camera (FR-31)', () => {
  const cam = loadCamera();
  const view = { centerX: 400, centerY: 250, zoom: 1.5 };
  const world = { x: 912, y: -37 };
  const screen = cam.worldToScreen(world, view, VIEWPORT);
  const back = cam.screenToWorld(screen, view, VIEWPORT);
  assert.ok(Math.abs(back.x - world.x) < 1e-9);
  assert.ok(Math.abs(back.y - world.y) < 1e-9);
});

test('the world point under the pointer stays still while zooming (FR-39)', () => {
  const cam = loadCamera();
  const before = { centerX: 300, centerY: 200, zoom: 1 };
  const pointer = { x: 820, y: 140 };
  const anchored = cam.screenToWorld(pointer, before, VIEWPORT);

  const after = cam.zoomAroundPoint(before, VIEWPORT, pointer, 1.25);
  const stillThere = cam.worldToScreen(anchored, after, VIEWPORT);
  assert.ok(Math.abs(stillThere.x - pointer.x) < 1e-6, 'x drifted');
  assert.ok(Math.abs(stillThere.y - pointer.y) < 1e-6, 'y drifted');
  assert.equal(after.zoom, 1.25);
});

test('button and keyboard zoom keep the viewport centre fixed (FR-39)', () => {
  const cam = loadCamera();
  const before = { centerX: 300, centerY: 200, zoom: 1 };
  const after = cam.zoomAroundCenter(before, 1.25);
  assert.equal(after.centerX, before.centerX);
  assert.equal(after.centerY, before.centerY);
  assert.equal(after.zoom, 1.25);
});

// --- one floor, shared by framing and gestures (#307) ----------------------
//
// The floor was 50% until Fit All had to frame a map wider than two viewports.
// Lowering it for framing alone would have left the camera somewhere the user
// could not zoom back out of by hand, so gestures reach the same 10%.

test('zoom is clamped to the usable 10%–200% range (FR-38, #307)', () => {
  const cam = loadCamera();
  assert.equal(cam.limits.min, 0.1, 'a stray coordinate must not render the map as a dot');
  assert.equal(cam.limits.max, 2);
  assert.equal(cam.clampZoom(50), 2);
  assert.equal(cam.clampZoom(0.3), 0.3, 'a fitted wide view is kept, not snapped up');
  assert.equal(cam.clampZoom(0.1), 0.1);
  assert.equal(cam.clampZoom(0.01), 0.1, 'and the floor still holds');
  assert.equal(cam.clampZoom(Number.NaN), 1, 'a corrupt zoom opens at 100%, not at nothing');
  assert.equal(cam.clampZoom(Number.POSITIVE_INFINITY), 1);
  assert.equal(cam.zoomAroundCenter({ centerX: 0, centerY: 0, zoom: 2 }, 4).zoom, 2);
  assert.equal(cam.zoomAroundCenter({ centerX: 0, centerY: 0, zoom: 0.1 }, 0.25).zoom, 0.1);
});

test('Fit All frames the whole content with padding (FR-40)', () => {
  const cam = loadCamera();
  const bounds = { minX: 0, minY: 0, maxX: 2000, maxY: 1000 };
  const fitted = cam.fitBounds(bounds, VIEWPORT, 50);
  assert.equal(fitted.centerX, 1000);
  assert.equal(fitted.centerY, 500);
  // Everything must be inside the viewport once fitted.
  const topLeft = cam.worldToScreen({ x: bounds.minX, y: bounds.minY }, fitted, VIEWPORT);
  const bottomRight = cam.worldToScreen({ x: bounds.maxX, y: bounds.maxY }, fitted, VIEWPORT);
  assert.ok(topLeft.x >= 0 && topLeft.y >= 0, 'content starts inside the viewport');
  assert.ok(bottomRight.x <= VIEWPORT.width && bottomRight.y <= VIEWPORT.height);
});

test('Fit All frames a map wider than two viewports below 50% (#307)', () => {
  const cam = loadCamera();
  // 6000 world units across a 1000px viewport: 50% shows a sixth of it.
  const bounds = { minX: 0, minY: 0, maxX: 6000, maxY: 1200 };
  const fitted = cam.fitBounds(bounds, VIEWPORT, 48);
  assert.ok(Number.isFinite(fitted.zoom), 'zoom stays a real number');
  assert.ok(fitted.zoom < 0.5, 'fitted at ' + fitted.zoom + ', which is not below the old clamp');
  assert.ok(fitted.zoom >= 0.1, 'and never past the framing floor');
  assert.equal(fitted.fitsEverything, true, 'this layout does fit — it just needed the room');

  const topLeft = cam.worldToScreen({ x: bounds.minX, y: bounds.minY }, fitted, VIEWPORT);
  const bottomRight = cam.worldToScreen({ x: bounds.maxX, y: bounds.maxY }, fitted, VIEWPORT);
  assert.ok(topLeft.x >= 0 && topLeft.y >= 0, 'the far corners are on screen');
  assert.ok(bottomRight.x <= VIEWPORT.width && bottomRight.y <= VIEWPORT.height);
});

test('an extreme layout stops at the 10% floor and reports that it did (#307)', () => {
  const cam = loadCamera();
  const fitted = cam.fitBounds({ minX: 0, minY: 0, maxX: 400000, maxY: 200 }, VIEWPORT, 48);
  assert.equal(fitted.zoom, 0.1, 'the floor holds');
  assert.equal(
    fitted.fitsEverything,
    false,
    'and Fit All knows it did not manage to show everything'
  );
});

test('a gesture can keep going out from a fitted view, down to the floor (#307)', () => {
  const cam = loadCamera();
  const fitted = { centerX: 0, centerY: 0, zoom: 0.3 };

  const outward = cam.zoomAroundCenter(fitted, 1 / 1.25);
  assert.ok(Math.abs(outward.zoom - 0.24) < 1e-9, 'Zoom Out keeps going past the fitted value');
  const inward = cam.zoomAroundCenter(fitted, 1.25);
  assert.ok(Math.abs(inward.zoom - 0.375) < 1e-9, 'and Zoom In steps inward from it');

  // The wheel obeys the same one floor, and stops there.
  assert.equal(cam.zoomAroundPoint(fitted, VIEWPORT, { x: 820, y: 140 }, 0.1).zoom, 0.1);
  assert.equal(cam.zoomAroundCenter({ centerX: 0, centerY: 0, zoom: 0.12 }, 1 / 1.25).zoom, 0.1);
});

test('a fitted sub-50% camera survives every non-zoom camera action (#307)', () => {
  const cam = loadCamera();
  const world = { minX: -8000, minY: -8000, maxX: 8000, maxY: 8000 };

  const panned = cam.clampCamera({ centerX: 1200, centerY: -640, zoom: 0.3 }, world);
  assert.equal(panned.zoom, 0.3, 'panning is not a zoom');
  assert.equal(cam.centerOn({ centerX: 0, centerY: 0, zoom: 0.3 }, { x: 900, y: 400 }).zoom, 0.3);

  // The tolerant fallbacks are unchanged for state that is not a valid camera.
  assert.equal(cam.clampCamera({ centerX: 0, centerY: 0, zoom: Number.NaN }, world).zoom, 1);
  assert.equal(cam.clampCamera({ centerX: 0, centerY: 0, zoom: 0.001 }, world).zoom, 0.1);
});

test('Center Selected moves the view, not the record (FR-41)', () => {
  const cam = loadCamera();
  const before = { centerX: 0, centerY: 0, zoom: 1.5 };
  const node = { x: 760, y: 456 };
  const after = cam.centerOn(before, node);
  assert.equal(after.zoom, before.zoom, 'centering is not a zoom');
  assert.deepEqual(node, { x: 760, y: 456 }, 'the node object is not mutated');
  // The node's centre lands on the viewport's centre.
  const screen = cam.worldToScreen({ x: node.x + 88, y: node.y + 85 }, after, VIEWPORT);
  assert.ok(Math.abs(screen.x - VIEWPORT.width / 2) < 1e-6);
  assert.ok(Math.abs(screen.y - VIEWPORT.height / 2) < 1e-6);
});

test('the camera cannot be panned outside the navigable world (FR-11)', () => {
  const cam = loadCamera();
  const world = { minX: -500, minY: -500, maxX: 1500, maxY: 1200 };
  const clamped = cam.clampCamera({ centerX: 99999, centerY: -99999, zoom: 1 }, world);
  assert.equal(clamped.centerX, 1500);
  assert.equal(clamped.centerY, -500);
  const inside = cam.clampCamera({ centerX: 10, centerY: 10, zoom: 1 }, world);
  assert.deepEqual({ ...inside }, { centerX: 10, centerY: 10, zoom: 1 });
});

// ---------------------------------------------------------------------------
// Camera behaviour at the mount seam (#292 FR-43 – FR-46, FR-106, FR-108)
// ---------------------------------------------------------------------------

// createCameraHarness is a DOM stub rich enough for the camera bindings: the
// canvas records its listeners so pointer and wheel gestures can be replayed
// without a browser, and the world layer records the transform that was applied
// to it.
function createCameraHarness({ width = 1000, height = 600, tiles = [], districts = [] } = {}) {
  const listeners = {};
  const styleProps = {};
  const classes = new Set();
  const world = { style: { transform: '' } };
  const controls = {};

  // Building stubs, rich enough for the drag state machine: their own listener
  // table, class list, and inline position. Districts and their drag handles
  // use the same shape.
  const makeNode = (id, { attribute = 'data-ws-id', classes: extraClasses = [] } = {}) => {
    const own = {};
    const tileClasses = new Set(extraClasses.length ? extraClasses : ['ws-map-tile']);
    const el = {
      style: { left: '', top: '' },
      classList: {
        add: c => tileClasses.add(c),
        remove: c => tileClasses.delete(c),
        contains: c => tileClasses.has(c),
        toggle: (c, on) => (on ? tileClasses.add(c) : tileClasses.delete(c))
      },
      getAttribute: name => (name === attribute ? id : null),
      // applyHQSelection asks every tile whether it is the reserved HQ site.
      hasAttribute: name => name === attribute,
      setAttribute: () => {},
      // updateSelBar reads each tile's corner checkbox to mirror the checked
      // state onto aria-checked.
      querySelector: () => ({ setAttribute: () => {} }),
      addEventListener: (type, fn) => {
        (own[type] = own[type] || []).push(fn);
      },
      setPointerCapture: () => {},
      releasePointerCapture: () => {},
      hasPointerCapture: () => true,
      focus: () => {
        el.focused = true;
      },
      focused: false,
      id,
      fire: (type, event = {}) => (own[type] || []).forEach(fn => fn(event)),
      // See the innerHTML setter below: a real re-mount destroys these elements
      // and their listeners, so the stub has to drop them too.
      resetListeners: () => Object.keys(own).forEach(type => delete own[type]),
      at: () => ({
        x: Number(el.style.left.replace('px', '')),
        y: Number(el.style.top.replace('px', ''))
      })
    };
    return el;
  };

  const tileEls = tiles.map(id => makeNode(id));
  const districtEls = districts.map(id =>
    makeNode(id, { attribute: 'data-group-id', classes: ['ws-map-district'] })
  );
  const handleEls = districts.map(id =>
    makeNode(id, { attribute: 'data-group-drag', classes: ['ws-map-district-handle'] })
  );

  const canvas = {
    clientWidth: width,
    clientHeight: height,
    classList: {
      add: c => classes.add(c),
      remove: c => classes.delete(c),
      contains: c => classes.has(c)
    },
    style: { setProperty: (k, v) => (styleProps[k] = v) },
    getBoundingClientRect: () => ({ left: 0, top: 0, width, height }),
    addEventListener: (type, fn) => {
      (listeners[type] = listeners[type] || []).push(fn);
    },
    setPointerCapture: () => {},
    releasePointerCapture: () => {},
    hasPointerCapture: () => true,
    querySelector: sel => (sel.includes('world') ? world : null)
  };

  function control(name) {
    if (!controls[name]) {
      const attrs = {};
      const controlClasses = new Set();
      controls[name] = {
        disabled: false,
        hidden: true,
        textContent: '',
        attrs,
        style: {},
        classList: {
          add: c => controlClasses.add(c),
          remove: c => controlClasses.delete(c),
          contains: c => controlClasses.has(c),
          toggle: (c, on) => (on ? controlClasses.add(c) : controlClasses.delete(c))
        },
        setAttribute: (k, v) => (attrs[k] = String(v)),
        getAttribute: k => (k in attrs ? attrs[k] : null),
        // The selection bar owns a live count element inside it.
        querySelector: inner => control(name + ' ' + inner),
        focus: () => {},
        addEventListener: (type, fn) => {
          if (type === 'click') controls[name].click = fn;
        }
      };
    }
    return controls[name];
  }

  // The context menu's host. The map writes menu markup into it and then works
  // with the real elements it finds there, so the stub parses what was written
  // back into element stubs — enough for clicks, roving focus, and the
  // aria-disabled contract.
  const menuHost = (() => {
    let hostHTML = '';
    let menu = null;

    const makeItem = (action, label, disabled) => {
      const attrs = { 'data-menu-action': action, tabindex: '-1' };
      if (disabled) attrs['aria-disabled'] = 'true';
      const own = {};
      const item = {
        action,
        label: label.trim(),
        attrs,
        focused: false,
        getAttribute: name => (name in attrs ? attrs[name] : null),
        setAttribute: (name, value) => {
          attrs[name] = String(value);
        },
        addEventListener: (type, fn) => {
          (own[type] = own[type] || []).push(fn);
        },
        focus: () => {
          if (menu) menu.items.forEach(other => (other.focused = false));
          item.focused = true;
        },
        fire: (type, event = {}) =>
          (own[type] || []).forEach(fn => fn({ preventDefault() {}, ...event }))
      };
      return item;
    };

    const parseItems = value => {
      const items = [];
      const pattern = /<button[^>]*data-menu-action="([^"]*)"([^>]*)>([\s\S]*?)<\/button>/g;
      let match;
      while ((match = pattern.exec(value))) {
        items.push(makeItem(match[1], match[3], /aria-disabled="true"/.test(match[2])));
      }
      return items;
    };

    const makeMenu = value => {
      const own = {};
      return {
        items: parseItems(value),
        style: {},
        focusedAction: null,
        addEventListener: (type, fn) => {
          (own[type] = own[type] || []).push(fn);
        },
        fire: (type, event = {}) =>
          (own[type] || []).forEach(fn => fn({ preventDefault() {}, ...event })),
        querySelectorAll: sel => (sel.includes('data-menu-action') ? host.menu().items : []),
        // Left unmeasured on purpose: an unlaid-out menu is exactly the case
        // the placement fallback exists for.
        getBoundingClientRect: () => ({ left: 0, top: 0, width: 0, height: 0 })
      };
    };

    const host = {
      style: {},
      querySelector: sel => (sel.includes('data-ws-map-menu') ? menu : null),
      menu: () => menu,
      items: () => (menu ? menu.items : []),
      labels: () => (menu ? menu.items.map(item => item.label) : []),
      item: action => (menu ? menu.items.find(entry => entry.action === action) || null : null),
      focused: () => (menu ? menu.items.find(entry => entry.focused) || null : null),
      isOpen: () => !!menu
    };
    Object.defineProperty(host, 'innerHTML', {
      get: () => hostHTML,
      set: value => {
        hostHTML = value;
        menu = value && value.includes('data-ws-map-menu') ? makeMenu(value) : null;
      }
    });
    return host;
  })();

  const container = {
    clientWidth: width,
    clientHeight: height,
    hidden: false,
    isConnected: true,
    querySelectorAll: sel => {
      if (sel.includes('data-group-drag')) return handleEls;
      if (sel.includes('ws-map-tile')) return tileEls;
      return [];
    },
    querySelector: sel => {
      if (sel.includes('ws-map-canvas') || sel.includes('ws-map-viewport')) return canvas;
      if (sel.includes('ws-map-world')) return world;
      if (sel.includes('data-ws-map-menu-host')) return menuHost;
      const tileMatch = sel.match(/ws-map-tile\[data-ws-id="([^"]+)"\]/);
      if (tileMatch) return tileEls.find(el => el.id === tileMatch[1]) || null;
      const districtMatch = sel.match(/ws-map-district\[data-group-id="([^"]+)"\]/);
      if (districtMatch) return districtEls.find(el => el.id === districtMatch[1]) || null;
      if (sel.includes('data-map-') || sel.includes('data-ws-selbar')) return control(sel);
      return null;
    }
  };
  // Writing innerHTML replaces the canvas in a real DOM, which drops every
  // listener bound to the old one. The stub reuses one canvas object, so it has
  // to drop them explicitly — otherwise a second mount would double every
  // gesture and the harness, not the map, would be what the test measured.
  //
  // The same is true of the tiles, districts, and drag handles: the layout fetch
  // triggers a settled re-mount, so anything bound per-mount would fire twice
  // and a paired action (like toggling a checkbox) would cancel itself out.
  let html = '';
  Object.defineProperty(container, 'innerHTML', {
    get: () => html,
    set: value => {
      html = value;
      Object.keys(listeners).forEach(type => delete listeners[type]);
      [...tileEls, ...districtEls, ...handleEls].forEach(el => el.resetListeners());
    }
  });

  return {
    container,
    world,
    styleProps,
    classes,
    control,
    menu: menuHost,
    tile: id => tileEls.find(el => el.id === id),
    district: id => districtEls.find(el => el.id === id),
    handle: id => handleEls.find(el => el.id === id),
    fire: (type, event) => (listeners[type] || []).forEach(fn => fn(event)),
    hasListener: type => !!(listeners[type] && listeners[type].length)
  };
}

const pointerEvent = (x, y, extra = {}) => ({
  button: 0,
  pointerId: 1,
  clientX: x,
  clientY: y,
  target: { closest: () => null },
  preventDefault() {},
  ...extra
});

function mountWithCamera(map, harness, workspaces) {
  map.mount(harness.container, {
    workspaces,
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
}

test('a saved camera is restored instead of refitting (FR-43, FR-45)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      revision: 2,
      positions: { 'ws-1': { x: 100, y: 100 } },
      viewport: { center_x: 640, center_y: 480, zoom: 1.5 }
    })
  );
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  assert.deepEqual({ ...map.getCamera() }, { centerX: 640, centerY: 480, zoom: 1.5 });
  assert.match(harness.world.style.transform, /scale\(1\.5\)/);
});

test('an invalid saved camera opens on a sensible view instead of nowhere (FR-45)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      positions: { 'ws-1': { x: 100, y: 100 } },
      viewport: { center_x: 'somewhere', center_y: null, zoom: 400 }
    })
  );
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  const cam = map.getCamera();
  assert.ok(Number.isFinite(cam.centerX) && Number.isFinite(cam.centerY));
  assert.ok(cam.zoom >= 0.5 && cam.zoom <= 2);
  // The building is inside the opening view rather than stranded off-screen.
  const screen = map.camera.worldToScreen({ x: 100, y: 100 }, cam, { width: 1000, height: 600 });
  assert.ok(screen.x > 0 && screen.x < 1000, 'x on screen: ' + screen.x);
  assert.ok(screen.y > 0 && screen.y < 600, 'y on screen: ' + screen.y);
});

test('a workspace refresh reconciles into the current view (FR-106)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      positions: { 'ws-1': { x: 100, y: 100 } },
      viewport: { center_x: 300, center_y: 200, zoom: 1.25 }
    })
  );
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();
  const before = map.getCamera();

  // A realtime refresh adds a workspace. The camera must not jump.
  mountWithCamera(map, harness, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();
  assert.deepEqual(map.getCamera(), before);
});

test('empty-space drag pans only after the threshold, and never from a building (FR-32 – FR-35)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({ schema_version: 1, positions: { 'ws-1': { x: 100, y: 100 } } })
  );
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();
  const start = map.getCamera();

  // A press that barely moves is a click, not a pan.
  harness.fire('pointerdown', pointerEvent(500, 300));
  harness.fire('pointermove', pointerEvent(502, 301));
  assert.deepEqual(map.getCamera(), start, 'a 2px wobble must not pan');

  // Past the threshold it pans, in world units.
  harness.fire('pointermove', pointerEvent(400, 300));
  const panned = map.getCamera();
  assert.ok(panned.centerX > start.centerX, 'dragging left moves the camera right');
  assert.ok(harness.classes.has('is-panning'), 'the grabbing cue is on');

  harness.fire('pointerup', pointerEvent(400, 300));
  assert.ok(!harness.classes.has('is-panning'), 'the gesture cleaned up on pointerup');

  // A gesture that starts on a building belongs to the building.
  const afterPan = map.getCamera();
  harness.fire(
    'pointerdown',
    pointerEvent(500, 300, {
      target: { closest: sel => (sel.includes('ws-map-tile') ? {} : null) }
    })
  );
  harness.fire('pointermove', pointerEvent(100, 300));
  assert.deepEqual(map.getCamera(), afterPan, 'a drag from a tile must not pan the map');
});

test('wheel pans and the zoom modifier zooms about the pointer (FR-36)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      positions: { 'ws-1': { x: 100, y: 100 } },
      viewport: { center_x: 300, center_y: 200, zoom: 1 }
    })
  );
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.fire('wheel', pointerEvent(500, 300, { deltaX: 40, deltaY: 80 }));
  let cam = map.getCamera();
  assert.equal(cam.zoom, 1, 'an ordinary wheel must not zoom');
  assert.equal(cam.centerX, 340);
  assert.equal(cam.centerY, 280);

  const beforeZoom = map.getCamera();
  harness.fire('wheel', pointerEvent(500, 300, { deltaY: -200, ctrlKey: true }));
  cam = map.getCamera();
  assert.ok(cam.zoom > beforeZoom.zoom, 'pinch/ctrl-wheel zooms in');
});

test('camera saves are debounced and best-effort (FR-44, FR-108)', async () => {
  const patches = [];
  const map = loadMapWithFetch((url, init) => {
    if (init && init.method === 'PATCH') {
      patches.push(JSON.parse(init.body));
      return Promise.resolve({ ok: false, status: 500 });
    }
    return jsonResponse({
      schema_version: 1,
      positions: { 'ws-1': { x: 100, y: 100 } },
      viewport: { center_x: 300, center_y: 200, zoom: 1 }
    });
  });
  const harness = createCameraHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.fire('pointerdown', pointerEvent(500, 300));
  for (let i = 0; i < 30; i += 1) harness.fire('pointermove', pointerEvent(500 - i * 5, 300));
  harness.fire('pointerup', pointerEvent(350, 300));

  assert.equal(patches.length, 0, 'pointer movement must not write (FR-69)');
  await new Promise(resolve => setTimeout(resolve, 750));
  await flush();
  assert.equal(patches.length, 1, 'a whole pan is one debounced save, not 30');
  assert.equal(patches[0].operations[0].op, 'set_viewport');

  // The save failed; the committed building position is untouched.
  const kept = map.getLayoutState().positions['ws-1'];
  assert.equal(kept.x, 100);
  assert.equal(kept.y, 100);
});

// --- a wide layout, fitted, panned, saved and reopened (#307) ---------------
//
// The pure tests above pin the maths; this one proves the whole path holds
// together, because every previous attempt to fit a wide map failed somewhere
// between the clamp, the save and the reload rather than in the arithmetic.

// A layout spread wider than two 1000px viewports, which is exactly the case
// the old 50% floor could not frame.
const WIDE_POSITIONS = {
  'ws-1': { x: 0, y: 0 },
  'ws-2': { x: 6000, y: 900 }
};

test('Fit All frames a wide layout below 50%, and it survives being used (#307)', async () => {
  const patches = [];
  const map = loadMapWithFetch((url, init) => {
    if (init && init.method === 'PATCH') {
      patches.push(JSON.parse(init.body));
      return jsonResponse({ schema_version: 1, revision: 2, positions: WIDE_POSITIONS });
    }
    return jsonResponse({ schema_version: 1, revision: 1, positions: WIDE_POSITIONS });
  });
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  mountWithCamera(map, harness, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();

  const live = harness.container.querySelector('[data-map-live]');
  harness.fire('keydown', { key: 'f', preventDefault() {} });
  const fitted = map.getCamera();
  assert.ok(fitted.zoom < 0.5, 'Fit All stopped at ' + fitted.zoom + ' instead of framing the map');
  assert.ok(fitted.zoom >= 0.1, 'but not past the floor');
  assert.equal(live.textContent, 'Showing every workspace', 'and it says what it actually did');
  assert.equal(
    harness.container.querySelector('[data-map-zoom-readout]').textContent,
    Math.round(fitted.zoom * 100) + '%',
    'the readout is truthful below 50%'
  );
  assert.equal(
    harness.container.querySelector('[data-map-zoom-out]').disabled,
    false,
    'and the user can still zoom out by hand from a fitted view'
  );

  // A non-zoom camera action must not change the fitted zoom.
  harness.fire('keydown', { key: 'ArrowRight', preventDefault() {} });
  const panned = map.getCamera();
  assert.equal(panned.zoom, fitted.zoom, 'panning kept the fitted zoom');
  assert.ok(panned.centerX > fitted.centerX, 'and did pan');

  // Zoom In steps inward from the fitted value rather than jumping to 50%.
  harness.container.querySelector('[data-map-zoom-in]').click();
  const zoomedIn = map.getCamera();
  assert.ok(zoomedIn.zoom > panned.zoom && zoomedIn.zoom < 0.5, 'in from ' + panned.zoom);

  harness.fire('keydown', { key: 'f', preventDefault() {} });
  await new Promise(resolve => setTimeout(resolve, 750));
  await flush();

  const viewportOps = patches
    .flatMap(patch => patch.operations)
    .filter(op => op.op === 'set_viewport');
  assert.ok(viewportOps.length >= 1, 'the fitted camera was saved');
  const saved = viewportOps[viewportOps.length - 1].viewport;
  assert.ok(saved.zoom < 0.5 && saved.zoom >= 0.1, 'saved zoom: ' + saved.zoom);

  // Reopening the map restores that camera rather than snapping to 50%.
  const reopened = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      revision: 3,
      positions: WIDE_POSITIONS,
      viewport: { center_x: saved.center_x, center_y: saved.center_y, zoom: saved.zoom }
    })
  );
  const second = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  mountWithCamera(reopened, second, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();
  assert.equal(reopened.getCamera().zoom, saved.zoom, 'the saved sub-50% view came back');
});

test('a layout too wide even for the floor says so instead of claiming success (#307)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      positions: { 'ws-1': { x: 0, y: 0 }, 'ws-2': { x: 400000, y: 0 } }
    })
  );
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  mountWithCamera(map, harness, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();

  harness.fire('keydown', { key: 'f', preventDefault() {} });
  assert.equal(map.getCamera().zoom, 0.1, 'it went as far out as it may');
  assert.match(
    harness.container.querySelector('[data-map-live]').textContent,
    /still off-screen/,
    'and the announcement stays honest about what is not visible'
  );
});

// --- first paint with nothing but the reserved HQ site (#329) ---------------
//
// A brand-new profile has no workspaces, so the only thing on its map is the
// reserved Personal HQ landmark. The camera's first-paint guard used to count
// only nodes and districts as content, leaving the view at its hard-coded
// default — which put the landmark low and right of centre, with its caption
// behind the control strip.

const HQ_MISSING = { valid: false, hq_onboarding_state: 'unseen' };
const DEFAULT_CAMERA = { centerX: 0, centerY: 0, zoom: 1 };
// The cockpit's map is a panel, not a page: at Home's real height the reserved
// site's anchor falls behind the control strip under the default camera, which
// is the shape of the reported bug. A taller canvas hides it by accident.
const HQ_VIEWPORT = { width: 1000, height: 460 };

// Where the reserved site actually lands, from the same placement the mount
// uses.
function hqAnchor(map) {
  return map.computeWorldLayout([], { hqSite: true }).hqSite;
}

// clearOf reports whether a cell is fully inside the part of the canvas the
// floating control strip does not cover.
function clearOf(map, cell, cam, viewport = HQ_VIEWPORT) {
  const topLeft = map.camera.worldToScreen({ x: cell.x, y: cell.y }, cam, viewport);
  const bottomRight = map.camera.worldToScreen({ x: cell.x + 176, y: cell.y + 170 }, cam, viewport);
  return (
    topLeft.x >= 0 &&
    topLeft.y >= 0 &&
    bottomRight.x <= viewport.width &&
    // 76px of floating control strip along the bottom.
    bottomRight.y <= viewport.height - 76
  );
}

const hqHarness = () => createCameraHarness(HQ_VIEWPORT);

test('a zero-workspace profile frames its reserved HQ landmark (#329)', async () => {
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }));
  map.setHQStatus(HQ_MISSING);
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();

  const cam = map.getCamera();
  assert.notDeepEqual({ ...cam }, DEFAULT_CAMERA, 'the camera never framed anything');
  assert.equal(cam.zoom, 1, 'and the opening view still does not zoom past 100%');
  assert.ok(
    clearOf(map, hqAnchor(map), cam),
    'the HQ landmark is not fully clear of the control strip: ' + JSON.stringify(cam)
  );
});

test('a map with only the New Workspace pad still waits for content (#329)', async () => {
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }));
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();

  assert.deepEqual(
    { ...map.getCamera() },
    DEFAULT_CAMERA,
    'framing the bare create pad would lock the camera onto an empty corner'
  );

  // And the one-time initialization was not spent: content arriving later
  // still gets framed.
  map.setHQStatus(HQ_MISSING);
  await flush();
  assert.notDeepEqual({ ...map.getCamera() }, DEFAULT_CAMERA, 'the late site was framed');
});

test('the HQ site arriving after the first mount frames the camera exactly once (#329)', async () => {
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }));
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();

  // personal-hq-onboarding.js resolves the status and re-mounts us.
  map.setHQStatus(HQ_MISSING);
  await flush();
  const framed = map.getCamera();
  assert.ok(clearOf(map, hqAnchor(map), framed), 'the late site was framed clear of the strip');

  // The user then moves the camera. A later status refresh must not undo that.
  harness.fire('keydown', { key: 'ArrowRight', preventDefault() {} });
  const moved = map.getCamera();
  assert.notDeepEqual({ ...moved }, { ...framed }, 'the pan moved the camera');
  map.setHQStatus({ valid: false, hq_onboarding_state: 'skipped' });
  await flush();
  assert.deepEqual({ ...map.getCamera() }, { ...moved }, 'a refresh must not refit the view');
});

test('a saved camera still wins over framing the reserved site (#329, FR-45)', async () => {
  const map = loadMapWithFetch(() =>
    jsonResponse({
      schema_version: 1,
      positions: {},
      viewport: { center_x: 512, center_y: 384, zoom: 1.25 }
    })
  );
  map.setHQStatus(HQ_MISSING);
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();

  assert.deepEqual({ ...map.getCamera() }, { centerX: 512, centerY: 384, zoom: 1.25 });
});

test('a reserved site does not frame the camera before the layout lands (#329)', async () => {
  // The layout request is left unresolved: until it answers, saved anchors may
  // still arrive and move everything, so framing now would frame the wrong
  // world and then be spent.
  let release;
  const map = loadMapWithFetch(
    () => new Promise(resolve => (release = () => resolve(jsonResponse({ positions: {} }))))
  );
  map.setHQStatus(HQ_MISSING);
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();
  assert.deepEqual({ ...map.getCamera() }, DEFAULT_CAMERA, 'framed while still loading');

  release();
  await flush();
  mountWithCamera(map, harness, []);
  await flush();
  assert.notDeepEqual({ ...map.getCamera() }, DEFAULT_CAMERA, 'and framed once it settled');
});

test('framing the reserved site changes the camera and nothing else (#329)', async () => {
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }));
  map.setHQStatus(HQ_MISSING);
  const harness = hqHarness();
  mountWithCamera(map, harness, []);
  await flush();

  // The landmark's own anchor is placement, not camera state: it is exactly
  // where it would be if the camera had never moved.
  const anchor = hqAnchor(map);
  const rendered = harness.container.innerHTML.split('data-hq-site')[1] || '';
  assert.match(
    rendered,
    new RegExp('left:' + anchor.x + 'px;top:' + anchor.y + 'px'),
    'the reserved site moved on the map, which framing must never do'
  );
  assert.deepEqual(
    Object.keys(map.getLayoutState().positions),
    [],
    'and nothing was written for it'
  );
  assert.equal(map.getSelectedId(), '', 'framing is not a selection');

  // The placement engine is untouched: same anchors, same world, whether or
  // not the camera decided to frame them.
  const layout = map.computeWorldLayout([], { hqSite: true });
  assert.deepEqual({ x: layout.hqSite.x, y: layout.hqSite.y }, { x: anchor.x, y: anchor.y });
  assert.ok(layout.pad && Number.isFinite(layout.pad.x), 'the create pad still has its anchor');
  assert.ok(
    layout.bounds.maxX > layout.bounds.minX && layout.bounds.maxY > layout.bounds.minY,
    'and the world still has real bounds'
  );
});

test('focus intent still selects and announces the reserved site it frames (#322, #329)', async () => {
  const announced = [];
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }), {
    location: { search: '?focus=personal-hq', pathname: '/' }
  });
  map.setHQStatus(HQ_MISSING);
  const harness = hqHarness();
  map.mount(harness.container, {
    workspaces: [],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    onSelectHQSite: view => announced.push(view)
  });
  await flush();

  assert.equal(map.getSelectedId(), '__personal_hq_site__', 'the focus intent still selects');
  assert.equal(announced.length, 1, 'and the host was still told exactly once');
  assert.ok(clearOf(map, hqAnchor(map), map.getCamera()), 'while the camera framed it clear');
});

// ---------------------------------------------------------------------------
// Build mode (#292 FR-47 – FR-62)
//
// An explicit, single-use placement state. Outside it, an empty-space click
// means what it always meant; inside it, exactly one selection chooses a build
// site and the mode ends.
// ---------------------------------------------------------------------------

function buildHarness({ layout, patchResponse } = {}) {
  const modalCalls = [];
  const patches = [];
  const fetchImpl = (url, init) => {
    if (init && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      patches.push(body);
      if (patchResponse === 'fail') return Promise.resolve({ ok: false, status: 500 });
      // Echo what a real server would commit, including the preference the
      // request asked for — the client reconciles against the response, so a
      // fake that always says "on" would be testing the fake.
      const preference = body.operations.find(op => op.op === 'set_preferences');
      const committed = body.operations.find(op => op.op === 'set_positions');
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            result: {
              schema_version: 1,
              revision: 9,
              positions: (committed && committed.positions) || {},
              snap_to_grid: preference ? preference.snap_to_grid : true
            }
          })
      });
    }
    return jsonResponse(
      layout || { schema_version: 1, revision: 1, positions: { 'ws-1': { x: 100, y: 100 } } }
    );
  };
  const map = loadMapWithFetch(fetchImpl, {
    sessionManager: {
      showAddWorkspaceModal: options => modalCalls.push(options)
    }
  });
  const harness = createCameraHarness();
  return { map, harness, modalCalls, patches };
}

const keyEvent = (key, extra = {}) => ({
  key,
  shiftKey: false,
  altKey: false,
  preventDefault() {},
  stopPropagation() {},
  ...extra
});

// Right-click empty ground at a screen point and choose Build. Since #317 this
// replaces the old Build *mode* — a button that armed the map, then a second
// click to pick the spot. The menu already opens at a spot, so it is the spot.
function buildFromMenu(harness, at = { x: 500, y: 300 }) {
  harness.fire('contextmenu', {
    target: { closest: sel => (sel.includes('ws-map-canvas') ? { focus() {} } : null) },
    clientX: at.x,
    clientY: at.y,
    preventDefault() {}
  });
  const item = harness.menu.item('build');
  if (item) item.fire('click');
  return item;
}

test('an ordinary empty-space click creates nothing (FR-48)', async () => {
  const { map, harness, modalCalls } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.fire('pointerdown', pointerEvent(500, 300));
  harness.fire('pointerup', pointerEvent(500, 300));
  assert.equal(modalCalls.length, 0, 'clicking the background must not start creating a workspace');
});

test('Build takes the right-clicked point and hands off to the existing modal (FR-51)', async () => {
  const { map, harness, modalCalls, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  assert.ok(buildFromMenu(harness), 'the canvas menu offers Build');

  assert.equal(modalCalls.length, 1, 'exactly one create flow opened');
  assert.equal(modalCalls[0].mapOrigin, true);
  assert.equal(modalCalls[0].entryPoint, 'workspace_map_build');
  // FR-62: a point inside a district is still just a point. Nothing about the
  // handoff can carry a parent.
  assert.ok(!('parentId' in modalCalls[0]) && !('parent_id' in modalCalls[0]));
  assert.equal(patches.length, 0, 'nothing is saved for a workspace that does not exist yet');
  assert.equal(map.hasPendingBuild(), true);
});

test('where you right-click is where it builds', async () => {
  const near = buildHarness();
  mountWithCamera(near.map, near.harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();
  buildFromMenu(near.harness, { x: 200, y: 150 });
  await near.map.completeBuild('ws-near');
  await flush();

  const far = buildHarness();
  mountWithCamera(far.map, far.harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();
  buildFromMenu(far.harness, { x: 800, y: 500 });
  await far.map.completeBuild('ws-far');
  await flush();

  const saved = ctx =>
    ctx.patches.filter(p => p.operations[0].op === 'set_positions')[0].operations[0].positions;
  const a = saved(near)['ws-near'];
  const b = saved(far)['ws-far'];
  assert.ok(b.x > a.x, 'a click further right builds further right: ' + a.x + ' vs ' + b.x);
  assert.ok(b.y > a.y, 'a click further down builds further down: ' + a.y + ' vs ' + b.y);
  // Snapped to the same grid a drop uses. (A negative multiple gives -0, which
  // is not strictly equal to 0.)
  [a, b].forEach(point => {
    assert.ok(point.x % 38 === 0, 'x is on the grid: ' + point.x);
    assert.ok(point.y % 38 === 0, 'y is on the grid: ' + point.y);
  });
});

test('right-clicking a building offers no Build — that menu is about the building', async () => {
  const { map, harness, modalCalls } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.fire('contextmenu', {
    target: {
      closest: sel =>
        sel.includes('data-ws-id') && !sel.includes('data-hq-site')
          ? { getAttribute: () => 'ws-1', focus() {} }
          : null
    },
    clientX: 500,
    clientY: 300,
    preventDefault() {}
  });
  assert.equal(harness.menu.item('build'), null, 'a building has no Build item (FR-52)');
  assert.equal(modalCalls.length, 0);
  assert.equal(map.hasPendingBuild(), false);
});

test('candidates snap to the shared grid, and Option/Alt bypasses without changing the setting (FR-58, FR-59)', () => {
  const map = loadMapWithFetch(() => jsonResponse({ schema_version: 1, positions: {} }));
  assert.equal(map.snapStep, 38);
  map._setLayoutForTest({ schema_version: 1, snap_to_grid: true, positions: {} });
  assert.deepEqual({ ...map.snapPoint({ x: 41, y: -20 }) }, { x: 38, y: -38 });
  assert.deepEqual({ ...map.snapPoint({ x: 41, y: -20 }, true) }, { x: 41, y: -20 });
  map._setLayoutForTest({ schema_version: 1, snap_to_grid: false, positions: {} });
  assert.deepEqual({ ...map.snapPoint({ x: 41, y: -20 }) }, { x: 41, y: -20 });
});

test('the Snap to Grid toggle is visible, defaults on, and persists (FR-57)', async () => {
  const { map, harness, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  assert.equal(map.getLayoutState().snapToGrid, true, 'a new layout snaps by default');
  const toggle = harness.control('[data-map-snap]');
  toggle.click();
  await flush();

  assert.equal(map.getLayoutState().snapToGrid, false);
  assert.equal(toggle.getAttribute('aria-pressed'), 'false');
  assert.match(toggle.textContent, /off/);
  assert.equal(patches.length, 1);
  assert.equal(patches[0].operations[0].op, 'set_preferences');
  assert.equal(patches[0].operations[0].snap_to_grid, false);
});

test('a keyboard-opened menu builds at the middle of what the user is looking at (FR-60)', async () => {
  const { map, harness, modalCalls, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  // Shift+F10 on the focused canvas: there is no cursor, so the coordinate
  // comes from the centre of the viewport rather than from a pointer.
  harness.fire('keydown', {
    key: 'F10',
    shiftKey: true,
    target: {
      closest: sel => (sel.includes('ws-map-canvas') ? { focus() {} } : null),
      getBoundingClientRect: () => ({ left: 0, top: 0, width: 1000, height: 600 })
    },
    preventDefault() {}
  });
  harness.menu.item('build').fire('click');
  assert.equal(modalCalls.length, 1, 'the keyboard route reaches the same create flow');

  await map.completeBuild('ws-keyboard');
  await flush();
  const saved = patches.filter(p => p.operations[0].op === 'set_positions')[0].operations[0]
    .positions['ws-keyboard'];
  const centre = map.camera.screenToWorld({ x: 500, y: 300 }, map.getCamera(), {
    width: 1000,
    height: 600
  });
  const snapped = map.snapPoint(centre);
  assert.deepEqual({ ...saved }, { ...snapped }, 'built at the centre of the view');
});

test('a successful create saves the chosen coordinate exactly once (FR-53)', async () => {
  const { map, harness, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  buildFromMenu(harness, { x: 600, y: 200 });

  await map.completeBuild('ws-new');
  await flush();

  const positionPatches = patches.filter(p => p.operations[0].op === 'set_positions');
  assert.equal(positionPatches.length, 1, 'one position write for one build');
  assert.ok(
    positionPatches[0].operations[0].positions['ws-new'],
    'saved under the new workspace id'
  );
  assert.equal(map.hasPendingBuild(), false, 'the pending coordinate is consumed exactly once');

  // A second call cannot re-save it.
  await map.completeBuild('ws-new');
  assert.equal(patches.filter(p => p.operations[0].op === 'set_positions').length, 1);
});

test('cancelling the modal leaves neither a workspace nor a position (FR-54)', async () => {
  const { map, harness, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  buildFromMenu(harness, { x: 600, y: 200 });
  assert.equal(map.hasPendingBuild(), true);

  map.cancelBuild();
  assert.equal(map.hasPendingBuild(), false);
  assert.equal(
    patches.filter(p => p.operations[0].op === 'set_positions').length,
    0,
    'a cancelled build writes nothing'
  );
});

test('a failed initial position keeps the workspace and offers a real retry (FR-56)', async () => {
  const { map, harness, patches } = buildHarness({ patchResponse: 'fail' });
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  buildFromMenu(harness, { x: 600, y: 200 });
  const chosen = { ...patches };

  const saved = await map.completeBuild('ws-new');
  await flush();
  assert.equal(saved, false, 'the failure is reported, not swallowed');
  assert.equal(chosen && true, true);

  // The retry saves the coordinate the user actually chose, not a new guess.
  await map.retryPlacement('ws-new');
  await flush();
  const attempts = patches.filter(p => p.operations[0].op === 'set_positions');
  assert.equal(attempts.length, 2, 'the retry is a real second attempt');
  assert.deepEqual(
    attempts[0].operations[0].positions['ws-new'],
    attempts[1].operations[0].positions['ws-new'],
    'the retry re-sends the original candidate'
  );
});

// ---------------------------------------------------------------------------
// Moving buildings (#292 FR-63 – FR-80)
//
// A drag is a state machine over a threshold. Below it, every existing meaning
// of a press survives; above it, exactly one save happens and only on drop.
// ---------------------------------------------------------------------------

function dragHarness({ positions, patchResponse } = {}) {
  const patches = [];
  const map = loadMapWithFetch((url, init) => {
    if (init && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      patches.push(body);
      if (patchResponse === 'fail') return Promise.resolve({ ok: false, status: 500 });
      const set = body.operations.find(op => op.op === 'set_positions');
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            result: {
              schema_version: 1,
              revision: 12,
              positions: (set && set.positions) || {},
              snap_to_grid: true
            }
          })
      });
    }
    return jsonResponse({
      schema_version: 1,
      revision: 1,
      snap_to_grid: true,
      positions: positions || { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 760, y: 228 } }
    });
  });
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  return { map, harness, patches };
}

const tilePointer = (x, y, extra = {}) => ({
  button: 0,
  pointerId: 7,
  clientX: x,
  clientY: y,
  target: { closest: () => null },
  preventDefault() {},
  stopPropagation() {},
  stopImmediatePropagation() {},
  ...extra
});

async function mountedDrag(options) {
  const ctx = dragHarness(options);
  mountWithCamera(ctx.map, ctx.harness, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();
  return ctx;
}

test('a press below the threshold is still a click, not a move (FR-64)', async () => {
  const { harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(102, 101));
  tile.fire('pointerup', tilePointer(102, 101));

  assert.equal(patches.length, 0, 'a click must not save a position');
  assert.equal(tile.classList.contains('is-dragging'), false);
});

test('pointer movement moves only the dragged building, and writes nothing (FR-65, FR-69, FR-122)', async () => {
  const { harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');
  const other = harness.tile('ws-2');
  other.style.left = '760px';
  other.style.top = '228px';

  tile.fire('pointerdown', tilePointer(100, 100));
  for (let i = 1; i <= 20; i += 1) tile.fire('pointermove', tilePointer(100 + i * 6, 100 + i * 3));

  assert.equal(patches.length, 0, 'pointer movement performs zero network writes');
  assert.ok(tile.classList.contains('is-dragging'));
  assert.notEqual(tile.style.left, '', 'the dragged building followed the pointer');
  assert.equal(other.style.left, '760px', 'no unrelated building moved (FR-74)');
});

test('a completed drop sends exactly one position update (FR-69, FR-70)', async () => {
  const { map, harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(180, 140));
  tile.fire('pointerup', tilePointer(180, 140));
  await flush();

  const saves = patches.filter(p => p.operations[0].op === 'set_positions');
  assert.equal(saves.length, 1, 'one drop, one update');
  const committed = saves[0].operations[0].positions['ws-1'];
  assert.ok(committed, 'the moved building is what was saved');
  // The server's answer becomes the local committed position.
  const state = map.getLayoutState();
  assert.equal(state.positions['ws-1'].x, committed.x);
  assert.equal(state.positions['ws-1'].y, committed.y);
});

test('the coordinate readout during a move never says "build" (FR-68)', async () => {
  const { harness } = await mountedDrag();
  const tile = harness.tile('ws-1');
  const banner = harness.control('[data-map-build-text]');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(220, 180));
  assert.doesNotMatch(banner.textContent, /build/i, 'a move is not a build: ' + banner.textContent);
  assert.match(banner.textContent, /Moving/);
  assert.equal(
    harness.control('[data-map-build-cancel]').hidden,
    true,
    'a drag is cancelled with Escape or by dropping, not by a third button'
  );
  tile.fire('pointerup', tilePointer(220, 180));
});

test('a drop snaps to the shared grid unless Option/Alt is held (FR-67)', async () => {
  const { harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(151, 133));
  tile.fire('pointerup', tilePointer(151, 133));
  await flush();
  const snapped = patches[0].operations[0].positions['ws-1'];
  assert.equal(snapped.x % 38, 0, 'x landed on the grid');
  assert.equal(snapped.y % 38, 0, 'y landed on the grid');

  tile.fire('pointerdown', tilePointer(100, 100, { altKey: true }));
  tile.fire('pointermove', tilePointer(151, 133, { altKey: true }));
  tile.fire('pointerup', tilePointer(151, 133, { altKey: true }));
  await flush();
  const free = patches[1].operations[0].positions['ws-1'];
  assert.ok(free.x % 38 !== 0 || free.y % 38 !== 0, 'Alt produced a free coordinate');
});

test('a drop onto an occupied anchor resolves to the nearest free one (FR-72)', async () => {
  // ws-1 is dragged exactly onto ws-2's committed anchor.
  const { harness, patches } = await mountedDrag({
    positions: { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 456, y: 228 } }
  });
  const tile = harness.tile('ws-1');
  const other = harness.tile('ws-2');
  other.style.left = '456px';
  other.style.top = '228px';

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(176, 100));
  tile.fire('pointerup', tilePointer(176, 100));
  await flush();

  const committed = patches[0].operations[0].positions['ws-1'];
  assert.notDeepEqual({ ...committed }, { x: 456, y: 228 }, 'two buildings cannot share an anchor');
  assert.equal(other.style.left, '456px', 'the resident building did not move to make room');
});

test('a drop one snap step from an occupied anchor is pushed clear of its footprint, not just its point (FR-72, FR-73)', async () => {
  // ws-1 is dragged to a point distinct from ws-2's anchor — one snap step
  // away — but still well inside ws-2's CELL_W-wide box, so the two would
  // render fully stacked if only exact-point equality were checked.
  const { harness, patches } = await mountedDrag({
    positions: { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 760, y: 228 } }
  });
  const tile = harness.tile('ws-1');
  const other = harness.tile('ws-2');
  other.style.left = '760px';
  other.style.top = '228px';

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(442, 100)); // world (722, 228): 38 short of ws-2
  tile.fire('pointerup', tilePointer(442, 100));
  await flush();

  const committed = patches[0].operations[0].positions['ws-1'];
  assert.ok(
    Math.abs(committed.x - 760) >= 176 || Math.abs(committed.y - 228) >= 170,
    'the resolved anchor must clear the occupied footprint, not just its point'
  );
  assert.equal(other.style.left, '760px', 'the resident building did not move to make room');
});

test('a drag over an occupied footprint shows a blocked indicator that clears on release (FR-72, FR-73, FR-120)', async () => {
  const { harness, patches } = await mountedDrag({
    positions: { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 760, y: 228 } }
  });
  const tile = harness.tile('ws-1');
  const bannerText = harness.control('[data-map-build-text]');
  const banner = harness.control('[data-map-build-banner]');
  harness.tile('ws-2').style.left = '760px';
  harness.tile('ws-2').style.top = '228px';

  tile.fire('pointerdown', tilePointer(100, 100));
  // Still clear of ws-2's footprint: no blocked state.
  tile.fire('pointermove', tilePointer(200, 100));
  assert.equal(tile.classList.contains('is-blocked'), false, 'not overlapping yet');
  assert.equal(banner.classList.contains('is-blocked'), false);
  assert.doesNotMatch(bannerText.textContent, /Occupied/);

  // 38 short of ws-2's anchor — inside its box, the exact #308 scenario.
  tile.fire('pointermove', tilePointer(442, 100));
  assert.ok(
    tile.classList.contains('is-blocked'),
    'overlapping another building is shown, not silent'
  );
  assert.ok(
    banner.classList.contains('is-blocked'),
    'the banner box itself turns red too, not just the tile'
  );
  assert.match(
    bannerText.textContent,
    /Occupied/,
    'the state is also carried by text, not colour alone'
  );

  // Move back off it: the indicator clears live, not just on drop.
  tile.fire('pointermove', tilePointer(200, 100));
  assert.equal(tile.classList.contains('is-blocked'), false, 'clears once no longer overlapping');
  assert.equal(banner.classList.contains('is-blocked'), false);

  tile.fire('pointerup', tilePointer(200, 100));
  await flush();
  assert.equal(tile.classList.contains('is-blocked'), false, 'nothing lingers after the drop');
  assert.equal(banner.classList.contains('is-blocked'), false);
  assert.equal(patches.length, 1);
});

test('a failed save puts the building back and offers a retry (FR-71)', async () => {
  const { map, harness, patches } = await mountedDrag({ patchResponse: 'fail' });
  const tile = harness.tile('ws-1');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(300, 300));
  tile.fire('pointerup', tilePointer(300, 300));
  await flush();

  assert.deepEqual({ ...tile.at() }, { x: 380, y: 228 }, 'restored to the committed position');
  assert.ok(tile.classList.contains('is-unsaved'), 'the unsaved state is visible, not silent');
  assert.ok(tile.focused, 'focus stays on the building');

  await map.retryPlacement('ws-1');
  assert.equal(patches.length, 2, 'the retry is a real second attempt');
});

test('the click after a drag is suppressed, but an ordinary click is not (FR-66, FR-80)', async () => {
  const { harness } = await mountedDrag();
  const tile = harness.tile('ws-1');
  let prevented = 0;
  const clickEvent = () => ({
    preventDefault: () => (prevented += 1),
    stopPropagation() {},
    stopImmediatePropagation() {}
  });

  // A plain click passes straight through to the existing select/open handler.
  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointerup', tilePointer(100, 100));
  tile.fire('click', clickEvent());
  assert.equal(prevented, 0, 'a click that was never a drag must keep its meaning');

  // A click synthesised after a real drag is swallowed exactly once.
  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(220, 180));
  tile.fire('pointerup', tilePointer(220, 180));
  tile.fire('click', clickEvent());
  assert.equal(prevented, 1, 'the drop did not re-select or open the workspace');
  tile.fire('click', clickEvent());
  assert.equal(prevented, 1, 'only the one synthesised click is suppressed');
});

test('bulk-selection gestures never start a spatial move (FR-75, FR-76)', async () => {
  const { harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');

  // The corner checkbox.
  const onCheckbox = { target: { closest: sel => (sel.includes('data-ws-check') ? {} : null) } };
  tile.fire('pointerdown', tilePointer(100, 100, onCheckbox));
  tile.fire('pointermove', tilePointer(300, 300, onCheckbox));
  tile.fire('pointerup', tilePointer(300, 300, onCheckbox));
  assert.equal(patches.length, 0);
  assert.equal(tile.classList.contains('is-dragging'), false);

  // A modifier-click.
  tile.fire('pointerdown', tilePointer(100, 100, { metaKey: true }));
  tile.fire('pointermove', tilePointer(300, 300, { metaKey: true }));
  tile.fire('pointerup', tilePointer(300, 300, { metaKey: true }));
  assert.equal(patches.length, 0);
});

test('Escape during a drag restores the committed position without saving (FR-79)', async () => {
  const { harness, patches } = await mountedDrag();
  const tile = harness.tile('ws-1');

  tile.fire('pointerdown', tilePointer(100, 100));
  tile.fire('pointermove', tilePointer(400, 400));
  tile.fire('keydown', { key: 'Escape' });

  assert.deepEqual({ ...tile.at() }, { x: 380, y: 228 });
  assert.equal(patches.length, 0, 'a cancelled move saves nothing');
});

test('a selected workspace can be moved by keyboard alone (FR-77 – FR-79)', async () => {
  const { map, harness, patches } = await mountedDrag();
  map.setSelectedId(null, [], 'ws-1');

  harness.control('[data-map-move]').click();
  harness.fire('keydown', keyEvent('ArrowRight'));
  harness.fire('keydown', keyEvent('ArrowDown'));
  assert.equal(patches.length, 0, 'stepping around does not save');

  harness.fire('keydown', keyEvent('Enter'));
  await flush();
  const saves = patches.filter(p => p.operations[0].op === 'set_positions');
  assert.equal(saves.length, 1, 'Enter commits once');
  assert.deepEqual({ ...saves[0].operations[0].positions['ws-1'] }, { x: 380 + 38, y: 228 + 38 });
});

test('Escape during a keyboard move restores and saves nothing (FR-79)', async () => {
  const { map, harness, patches } = await mountedDrag();
  map.setSelectedId(null, [], 'ws-1');

  harness.control('[data-map-move]').click();
  harness.fire('keydown', keyEvent('ArrowLeft'));
  harness.fire('keydown', keyEvent('Escape'));
  await flush();

  assert.equal(patches.length, 0);
  assert.deepEqual({ ...harness.tile('ws-1').at() }, { x: 380, y: 228 });
});

test('a keyboard move over an occupied footprint shows a blocked indicator too, not just pointer drags (FR-77 – FR-79, FR-120)', async () => {
  const { map, harness, patches } = await mountedDrag({
    positions: { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 760, y: 228 } }
  });
  map.setSelectedId(null, [], 'ws-1');
  const tile = harness.tile('ws-1');
  const bannerText = harness.control('[data-map-build-text]');
  const banner = harness.control('[data-map-build-banner]');
  harness.tile('ws-2').style.left = '760px';
  harness.tile('ws-2').style.top = '228px';

  harness.control('[data-map-move]').click();
  assert.equal(tile.classList.contains('is-blocked'), false, 'starts clear');
  assert.equal(banner.classList.contains('is-blocked'), false);

  // Shift+Right then Left nets +342: one snap step short of ws-2's anchor,
  // inside its footprint — the keyboard path must see the same collision the
  // pointer path does.
  harness.fire('keydown', keyEvent('ArrowRight', { shiftKey: true }));
  harness.fire('keydown', keyEvent('ArrowLeft'));
  assert.ok(tile.classList.contains('is-blocked'), 'a keyboard-driven overlap is shown live');
  assert.ok(
    banner.classList.contains('is-blocked'),
    'the banner box itself turns red too, not just the tile'
  );
  assert.match(
    bannerText.textContent,
    /Occupied/,
    'the state is also carried by text, not colour alone'
  );

  harness.fire('keydown', keyEvent('Escape'));
  await flush();
  assert.equal(tile.classList.contains('is-blocked'), false, 'nothing lingers after cancelling');
  assert.equal(banner.classList.contains('is-blocked'), false);
  assert.equal(patches.length, 0);
});

// ---------------------------------------------------------------------------
// Moving districts (#292 FR-81 – FR-94)
//
// A cluster move is one delta applied to the group and every visible
// descendant. It is presentation only: no payload it can produce contains a
// parent.
// ---------------------------------------------------------------------------

async function mountedCluster({ patchResponse } = {}) {
  const patches = [];
  const map = loadMapWithFetch((url, init) => {
    if (init && init.method === 'PATCH') {
      const body = JSON.parse(init.body);
      patches.push(body);
      if (patchResponse === 'fail') return Promise.resolve({ ok: false, status: 500 });
      const preference = body.operations.find(op => op.op === 'set_preferences');
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            result: {
              schema_version: 1,
              revision: 4,
              positions: {},
              snap_to_grid: preference ? preference.snap_to_grid : true
            }
          })
      });
    }
    return jsonResponse({
      schema_version: 1,
      revision: 1,
      snap_to_grid: true,
      positions: {
        grp: { x: 100, y: 100 },
        'child-a': { x: 152, y: 152 },
        'child-b': { x: 380, y: 152 },
        outsider: { x: 900, y: 900 }
      }
    });
  });
  const harness = createCameraHarness({
    tiles: ['child-a', 'child-b', 'outsider'],
    districts: ['grp']
  });
  map.mount(harness.container, {
    workspaces: [
      { id: 'grp', kind: 'group', name: 'Ops' },
      { id: 'child-a', parent_id: 'grp', name: 'A' },
      { id: 'child-b', parent_id: 'grp', name: 'B' },
      { id: 'outsider', name: 'Outside' }
    ],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  await flush();
  // Reset View pins the camera at 100%, so a screen delta in these tests is a
  // world delta and the expected numbers stay readable. It has no button any
  // more (#317 moved it to the canvas menu); `0` on the canvas is the same
  // action.
  harness.fire('keydown', { key: '0', preventDefault() {} });
  return { map, harness, patches };
}

test('dragging the district handle moves the whole cluster by one delta (FR-86)', async () => {
  const { harness, patches } = await mountedCluster();
  const handle = harness.handle('grp');
  const childA = harness.tile('child-a');
  const childB = harness.tile('child-b');
  const outsider = harness.tile('outsider');
  outsider.style.left = '900px';
  outsider.style.top = '900px';

  handle.fire('pointerdown', tilePointer(200, 200));
  handle.fire('pointermove', tilePointer(276, 238));

  // Relative spacing inside the cluster is preserved exactly.
  const a = childA.at();
  const b = childB.at();
  assert.equal(b.x - a.x, 380 - 152, 'members kept their relative spacing');
  assert.equal(b.y - a.y, 0);
  assert.equal(outsider.style.left, '900px', 'a workspace outside the district did not move');

  handle.fire('pointerup', tilePointer(276, 238));
  await flush();

  assert.equal(patches.length, 1, 'one cluster move, one request');
  const op = patches[0].operations[0];
  assert.equal(op.op, 'translate_group');
  assert.equal(op.group_id, 'grp');
  // The group's anchor snaps to the grid like any other placement, so the delta
  // is whatever carries (100,100) to the nearest grid point past the drag —
  // (190,152) — rather than the raw pointer distance.
  assert.deepEqual(
    { ...op.delta },
    { x: 90, y: 52 },
    'the server is sent a delta, not coordinates'
  );
  // FR-8: nothing in a cluster move can express membership.
  assert.equal(JSON.stringify(patches[0]).includes('parent'), false);
});

test('a cluster drag over an outside footprint shows a blocked indicator that clears when it moves clear (FR-88, FR-73, FR-120)', async () => {
  const { harness } = await mountedCluster();
  // Snapping off, so the drag lands exactly where the arithmetic says.
  harness.control('[data-map-snap]').click();
  await flush();

  const handle = harness.handle('grp');
  const district = harness.district('grp');
  const childA = harness.tile('child-a');
  const bannerText = harness.control('[data-map-build-text]');
  const banner = harness.control('[data-map-build-banner]');
  const outsider = harness.tile('outsider');
  outsider.style.left = '900px';
  outsider.style.top = '900px';

  handle.fire('pointerdown', tilePointer(0, 0));
  // Well clear of the outsider: no blocked state yet.
  handle.fire('pointermove', tilePointer(300, 300));
  assert.equal(district.classList.contains('is-blocked'), false, 'not overlapping yet');
  assert.equal(childA.classList.contains('is-blocked'), false);
  assert.equal(banner.classList.contains('is-blocked'), false);

  // child-a (152,152) would land at (862,900) — one snap step short of the
  // outsider's anchor (900,900), inside its footprint.
  handle.fire('pointermove', tilePointer(710, 748));
  assert.ok(
    district.classList.contains('is-blocked'),
    'a cluster overlap is shown live, not only refused after the drop'
  );
  assert.ok(
    childA.classList.contains('is-blocked'),
    'the member tile itself goes translucent too, not just the district border — that tile is what covers the outsider'
  );
  assert.ok(
    banner.classList.contains('is-blocked'),
    'the banner box itself turns red too, not just the district'
  );
  assert.match(
    bannerText.textContent,
    /Occupied/,
    'the state is also carried by text, not colour alone'
  );

  // Back to clear ground before releasing: the indicator lifts live too.
  handle.fire('pointermove', tilePointer(300, 300));
  assert.equal(
    district.classList.contains('is-blocked'),
    false,
    'clears once no longer overlapping'
  );
  assert.equal(childA.classList.contains('is-blocked'), false);
  assert.equal(banner.classList.contains('is-blocked'), false);

  handle.fire('pointerup', tilePointer(300, 300));
  await flush();
  assert.equal(district.classList.contains('is-blocked'), false, 'nothing lingers after the drop');
  assert.equal(childA.classList.contains('is-blocked'), false);
  assert.equal(banner.classList.contains('is-blocked'), false);
});

test('a cluster move that would land on an outside building is refused, not resolved (FR-88)', async () => {
  const { harness, patches } = await mountedCluster();
  // Snapping off, so the drag lands exactly where the arithmetic says.
  harness.control('[data-map-snap]').click();
  await flush();

  const handle = harness.handle('grp');
  const outsider = harness.tile('outsider');
  outsider.style.left = '900px';
  outsider.style.top = '900px';

  // Move child-a (152,152) exactly onto the outsider at (900,900).
  handle.fire('pointerdown', tilePointer(0, 0));
  handle.fire('pointermove', tilePointer(748, 748));
  handle.fire('pointerup', tilePointer(748, 748));
  await flush();

  const moves = patches.filter(p => p.operations[0].op === 'translate_group');
  assert.equal(moves.length, 0, 'the collision blocked the commit');
  assert.deepEqual(
    { ...harness.district('grp').at() },
    { x: 100, y: 100 },
    'the district returned'
  );
  assert.deepEqual({ ...harness.tile('child-a').at() }, { x: 152, y: 152 });
  assert.equal(outsider.style.left, '900px', 'the outside building was never touched');
});

test('a cluster move that would only overlap an outside building is refused too (FR-88, FR-73)', async () => {
  const { harness, patches } = await mountedCluster();
  // Snapping off, so the drag lands exactly where the arithmetic says.
  harness.control('[data-map-snap]').click();
  await flush();

  const handle = harness.handle('grp');
  const outsider = harness.tile('outsider');
  outsider.style.left = '900px';
  outsider.style.top = '900px';

  // Move child-a from (152,152) to (862,900) — one snap step short of the
  // outsider's anchor (900,900): a distinct point, but still inside its
  // CELL_W-wide footprint.
  handle.fire('pointerdown', tilePointer(0, 0));
  handle.fire('pointermove', tilePointer(710, 748));
  handle.fire('pointerup', tilePointer(710, 748));
  await flush();

  const moves = patches.filter(p => p.operations[0].op === 'translate_group');
  assert.equal(moves.length, 0, 'an overlapping-but-distinct anchor is still a collision');
  assert.deepEqual(
    { ...harness.district('grp').at() },
    { x: 100, y: 100 },
    'the district returned'
  );
  assert.deepEqual({ ...harness.tile('child-a').at() }, { x: 152, y: 152 });
  assert.equal(outsider.style.left, '900px', 'the outside building was never touched');
});

test('a failed cluster save restores every anchor together (FR-87)', async () => {
  const { harness, patches } = await mountedCluster({ patchResponse: 'fail' });
  const handle = harness.handle('grp');

  handle.fire('pointerdown', tilePointer(200, 200));
  handle.fire('pointermove', tilePointer(300, 300));
  handle.fire('pointerup', tilePointer(300, 300));
  await flush();

  assert.equal(patches.length, 1, 'it was attempted');
  assert.deepEqual({ ...harness.district('grp').at() }, { x: 100, y: 100 });
  assert.deepEqual({ ...harness.tile('child-a').at() }, { x: 152, y: 152 });
  assert.deepEqual({ ...harness.tile('child-b').at() }, { x: 380, y: 152 });
});

test('a selected district moves by keyboard with the same contract (FR-93)', async () => {
  const { map, harness, patches } = await mountedCluster();
  map.setSelectedId(null, [], 'grp');

  harness.control('[data-map-move]').click();
  harness.fire('keydown', keyEvent('ArrowRight'));
  assert.deepEqual(
    { ...harness.tile('child-a').at() },
    { x: 152 + 38, y: 152 },
    'the cluster previewed'
  );
  assert.equal(patches.length, 0, 'stepping does not save');

  harness.fire('keydown', keyEvent('Escape'));
  assert.deepEqual({ ...harness.tile('child-a').at() }, { x: 152, y: 152 }, 'Escape restored it');
  assert.equal(patches.length, 0);

  harness.control('[data-map-move]').click();
  harness.fire('keydown', keyEvent('ArrowDown'));
  harness.fire('keydown', keyEvent('Enter'));
  await flush();
  assert.equal(patches.length, 1);
  assert.equal(patches[0].operations[0].op, 'translate_group');
  assert.deepEqual({ ...patches[0].operations[0].delta }, { x: 0, y: 38 });
});

test('moving one child expands its district and moves no sibling (FR-89)', async () => {
  const { harness, patches } = await mountedCluster();
  const childA = harness.tile('child-a');
  const childB = harness.tile('child-b');
  childB.style.left = '380px';
  childB.style.top = '152px';

  childA.fire('pointerdown', tilePointer(100, 100));
  childA.fire('pointermove', tilePointer(400, 400));
  childA.fire('pointerup', tilePointer(400, 400));
  await flush();

  const op = patches[0].operations[0];
  assert.equal(
    op.op,
    'set_positions',
    'a child move is a plain position update, not a cluster move'
  );
  assert.deepEqual(Object.keys(op.positions), ['child-a'], 'only the child was saved');
  assert.equal(childB.style.left, '380px', 'the sibling stayed put');
  assert.equal(JSON.stringify(patches[0]).includes('parent'), false, 'membership is untouched');
});

test('a Tree reparent keeps absolute coordinates and only redraws the district (FR-90, FR-91)', () => {
  const computeWorldLayout = loadWorldLayout();
  const positions = {
    grp: { x: 100, y: 100 },
    other: { x: 900, y: 100 },
    mover: { x: 500, y: 400 }
  };
  const before = computeWorldLayout(
    [
      { id: 'grp', kind: 'group', name: 'Ops' },
      { id: 'other', kind: 'group', name: 'Research' },
      { id: 'mover', parent_id: 'grp', name: 'Mover' }
    ],
    { positions }
  );
  const after = computeWorldLayout(
    [
      { id: 'grp', kind: 'group', name: 'Ops' },
      { id: 'other', kind: 'group', name: 'Research' },
      { id: 'mover', parent_id: 'other', name: 'Mover' }
    ],
    { positions }
  );

  // The workspace stays exactly where it was: changing a parent is not a move,
  // and it must never snap to the destination group's anchor (FR-91).
  assert.deepEqual(anchorsById(before).mover, { x: 500, y: 400 });
  assert.deepEqual(anchorsById(after).mover, { x: 500, y: 400 });

  // Only the district presentation changed: the outline that now contains it.
  const districtAfter = after.districts.find(d => d.id === 'other');
  assert.ok(districtAfter.x <= 500 && districtAfter.y <= 400, 'the new district grew around it');
  const oldDistrictAfter = after.districts.find(d => d.id === 'grp');
  assert.ok(oldDistrictAfter.width < districtAfter.width, 'the old district shrank back');
});

// ---------------------------------------------------------------------------
// Reset Layout and Undo (#292 FR-109 – FR-112)
//
// Reset clears the user's arrangement, not their workspaces — and it is
// reversible for the session.
// ---------------------------------------------------------------------------

async function mountedReset({ confirm = true, deleteFails = false, undoFails = false } = {}) {
  const calls = [];
  const map = loadMapWithFetch(
    (url, init) => {
      const method = (init && init.method) || 'GET';
      calls.push({ method, body: init && init.body ? JSON.parse(init.body) : null });
      if (method === 'DELETE') {
        if (deleteFails) return Promise.resolve({ ok: false, status: 500 });
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              result: { schema_version: 1, revision: 7, positions: {}, snap_to_grid: false }
            })
        });
      }
      if (method === 'PATCH') {
        if (undoFails) return Promise.resolve({ ok: false, status: 500 });
        const body = JSON.parse(init.body);
        const restore = body.operations.find(op => op.op === 'restore_positions');
        return Promise.resolve({
          ok: true,
          json: () =>
            Promise.resolve({
              result: {
                schema_version: 1,
                revision: 8,
                positions: (restore && restore.positions) || {},
                snap_to_grid: false
              }
            })
        });
      }
      return jsonResponse({
        schema_version: 1,
        revision: 1,
        snap_to_grid: false,
        positions: { 'ws-1': { x: 380, y: 228 }, 'ws-2': { x: 760, y: 456 } }
      });
    },
    { confirm: () => confirm }
  );
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  mountWithCamera(map, harness, [
    { id: 'ws-1', name: 'Alpha' },
    { id: 'ws-2', name: 'Beta' }
  ]);
  await flush();
  return { map, harness, calls };
}

test('Reset Layout is confirmed, clears only positions, and keeps the snap preference (FR-110, FR-111)', async () => {
  const { map, harness, calls } = await mountedReset();
  assert.equal(map.getLayoutState().snapToGrid, false, 'the preference under test starts off');

  harness.control('[data-map-reset-layout]').click();
  await flush();

  const deletes = calls.filter(c => c.method === 'DELETE');
  assert.equal(deletes.length, 1, 'reset goes through the dedicated reset endpoint');
  assert.equal(Object.keys(map.getLayoutState().positions).length, 0, 'anchors cleared');
  assert.equal(map.getLayoutState().snapToGrid, false, 'the snap preference survived the reset');
  assert.equal(map.hasUndoableReset(), true, 'an in-session undo is offered');
});

test('a declined confirmation resets nothing (FR-110)', async () => {
  const { map, harness, calls } = await mountedReset({ confirm: false });
  harness.control('[data-map-reset-layout]').click();
  await flush();

  assert.equal(calls.filter(c => c.method === 'DELETE').length, 0);
  assert.equal(Object.keys(map.getLayoutState().positions).length, 2, 'positions untouched');
  assert.equal(map.hasUndoableReset(), false);
});

test('Undo restores the exact pre-reset position set atomically (FR-112)', async () => {
  const { map, harness, calls } = await mountedReset();
  const before = map.getLayoutState().positions;

  harness.control('[data-map-reset-layout]').click();
  await flush();
  harness.control('[data-map-undo-reset]').click();
  await flush();

  const restore = calls.find(c => c.body && c.body.operations[0].op === 'restore_positions');
  assert.ok(restore, 'undo uses the atomic exact-restore operation');
  assert.deepEqual(Object.keys(restore.body.operations[0].positions).sort(), ['ws-1', 'ws-2']);
  const after = map.getLayoutState().positions;
  assert.equal(after['ws-1'].x, before['ws-1'].x);
  assert.equal(after['ws-2'].y, before['ws-2'].y);
  assert.equal(map.hasUndoableReset(), false, 'the snapshot is consumed once');
});

test('a failed reset leaves the previous layout intact (FR-112)', async () => {
  const { map, harness } = await mountedReset({ deleteFails: true });
  harness.control('[data-map-reset-layout]').click();
  await flush();

  assert.equal(
    Object.keys(map.getLayoutState().positions).length,
    2,
    'a reset that did not happen must not look like it did'
  );
  assert.equal(map.hasUndoableReset(), false, 'nothing to undo');
});

test('a failed Undo keeps the post-reset state and keeps offering the retry (FR-112)', async () => {
  const { map, harness } = await mountedReset({ undoFails: true });
  harness.control('[data-map-reset-layout]').click();
  await flush();
  harness.control('[data-map-undo-reset]').click();
  await flush();

  assert.equal(Object.keys(map.getLayoutState().positions).length, 0, 'still reset');
  assert.equal(map.hasUndoableReset(), true, 'the undo offer survives a failed attempt');
});

test('merely reading the map never writes a coordinate (FR-23)', async () => {
  const calls = [];
  const map = loadMapWithFetch((url, init) => {
    if (String(url).includes('/api/workspace-map/layout')) {
      calls.push({ url, method: (init && init.method) || 'GET' });
    }
    return jsonResponse({ schema_version: 1, revision: 0, positions: {} });
  });
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  await flush();
  map.mount(container, { workspaces: [{ id: 'ws-1', name: 'Alpha' }] });
  await flush();

  assert.ok(
    calls.every(c => c.method === 'GET'),
    'viewing fallback placement must not write: ' + JSON.stringify(calls)
  );
});

// ---------------------------------------------------------------------------
// Bulk actions: delete + group are host-owned, and must never fail silently
//
// These are the regression tests for the shipped bug where the Map's Delete and
// Group buttons did nothing at all. The map called `window.WorkspaceHub`, a
// global carried only by the retired /workspaces launcher; on Home that global
// is undefined, so all three actions took an `if` that was never true and
// returned without a sound. Nothing here asserted the wiring, so a fully green
// suite still said the feature worked.
// ---------------------------------------------------------------------------

function bulkHarness({ handlers = {}, consoleImpl } = {}) {
  const map = loadMapWithFetch(
    () =>
      jsonResponse({
        schema_version: 1,
        revision: 1,
        snap_to_grid: true,
        positions: { 'ws-1': { x: 100, y: 100 }, 'ws-2': { x: 300, y: 100 } }
      }),
    {},
    consoleImpl
  );
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  map.mount(harness.container, {
    workspaces: [
      { id: 'ws-1', name: 'Alpha' },
      { id: 'ws-2', name: 'Beta' }
    ],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    ...handlers
  });
  return { map, harness };
}

// Check a tile the way a mouse user does: click its corner checkbox.
const checkboxClick = () => ({
  target: { closest: sel => (sel.includes('data-ws-check') ? {} : null) },
  preventDefault() {}
});

// The map builds its id arrays inside the vm sandbox, so they are Arrays from
// another realm and fail a strict deepEqual against host-built ones.
const ids = calls => calls.map(call => Array.from(call));

// Right-click a tile. Since #317 this is how the bulk actions are reached: the
// selection bar carries only the count and Clear, and Group/Delete moved into
// the menu that opens on any checked building.
const openTileMenu = (harness, id, at = { x: 200, y: 150 }) =>
  harness.fire('contextmenu', {
    target: {
      closest: sel =>
        sel.includes('data-ws-id') && !sel.includes('data-hq-site')
          ? { getAttribute: () => id, focus() {} }
          : null
    },
    clientX: at.x,
    clientY: at.y,
    preventDefault() {}
  });

test('the context menu hands the checked ids to the host (the shipped no-op)', async () => {
  const deleted = [];
  const grouped = [];
  const { harness } = bulkHarness({
    handlers: {
      onDeleteWorkspaces: ids => deleted.push(ids),
      onGroupWorkspaces: ids => grouped.push(ids)
    }
  });
  await flush();

  harness.tile('ws-1').fire('click', checkboxClick());
  harness.tile('ws-2').fire('click', checkboxClick());

  openTileMenu(harness, 'ws-1');
  harness.menu.item('delete-multi').fire('click');
  assert.deepEqual(ids(deleted), [['ws-1', 'ws-2']], 'Delete must reach the host');

  openTileMenu(harness, 'ws-1');
  harness.menu.item('group-multi').fire('click');
  assert.deepEqual(ids(grouped), [['ws-1', 'ws-2']], 'Group must reach the host');
});

test('an unwired bulk action reports the wiring bug instead of doing nothing', async () => {
  const errors = [];
  const { harness } = bulkHarness({
    consoleImpl: { error: msg => errors.push(String(msg)), warn() {}, log() {} }
  });
  await flush();

  harness.tile('ws-1').fire('click', checkboxClick());
  openTileMenu(harness, 'ws-1');
  harness.menu.item('delete-multi').fire('click');
  openTileMenu(harness, 'ws-1');
  harness.menu.item('group-multi').fire('click');

  assert.equal(errors.length, 2, 'both unwired actions must complain');
  assert.ok(
    errors.every(msg => msg.includes('workspace-map: no handler for')),
    'the message must name the missing handler: ' + JSON.stringify(errors)
  );
});

test('a modified Enter or Space toggles multi-select from the keyboard', async () => {
  const grouped = [];
  const opened = [];
  const { harness } = bulkHarness({
    handlers: {
      onGroupWorkspaces: selected => grouped.push(selected),
      onOpen: id => opened.push(id)
    }
  });
  await flush();

  const key = (k, extra) => ({ key: k, preventDefault() {}, ...extra });

  // An unmodified Enter still opens rather than selecting, so the existing
  // keyboard contract is untouched.
  harness.tile('ws-1').fire('keydown', key('Enter'));
  openTileMenu(harness, 'ws-1');
  assert.equal(
    harness.menu.item('group-multi'),
    null,
    'a bare Enter checked nothing, so the tile menu is the single-target one'
  );
  assert.deepEqual(opened, ['ws-1'], 'a bare Enter still opens');
  assert.deepEqual(grouped, [], 'a bare Enter must not check anything');

  harness.tile('ws-1').fire('keydown', key('Enter', { shiftKey: true }));
  harness.tile('ws-2').fire('keydown', key(' ', { metaKey: true }));
  openTileMenu(harness, 'ws-1');
  harness.menu.item('group-multi').fire('click');
  assert.deepEqual(ids(grouped), [['ws-1', 'ws-2']], 'modified Enter/Space must check the tile');
  assert.deepEqual(opened, ['ws-1'], 'a modified key must not also open');
});

test('clearMultiSelection empties the checked set after a group', async () => {
  const grouped = [];
  const { map, harness } = bulkHarness({
    handlers: { onGroupWorkspaces: ids => grouped.push(ids) }
  });
  await flush();

  harness.tile('ws-1').fire('click', checkboxClick());
  map.clearMultiSelection();
  openTileMenu(harness, 'ws-1');

  // With nothing checked, the tile is no longer part of a set, so it offers its
  // own single-target menu — there is no bulk Group to press.
  assert.equal(harness.menu.item('group-multi'), null);
  assert.ok(harness.menu.item('toggle-selection'), 'and offers to add itself back');
  assert.deepEqual(grouped, [], 'a cleared selection has nothing to group');
});

// ---------------------------------------------------------------------------
// Right-click context menu (#317)
//
// The menu adds no capability: every item routes to an action the map already
// had. What is new — and what these tests pin — is where the menu opens, what
// each target offers, how it is dismissed, and that it is fully operable from
// the keyboard.
// ---------------------------------------------------------------------------

// A document and window rich enough for the menu's global listeners, so the
// dismissal routes can be replayed without a browser.
function menuEnvironment() {
  const docListeners = {};
  const winListeners = {};
  const dispatched = [];

  const record = (table, type, fn) => {
    (table[type] = table[type] || []).push(fn);
  };
  const drop = (table, type, fn) => {
    table[type] = (table[type] || []).filter(entry => entry !== fn);
  };
  // A caller that hands over a real event object gets that exact object passed
  // through: listener identity checks (see the open-event guard) depend on it.
  const emit = (table, type, event) => {
    const payload =
      event && typeof event.preventDefault === 'function'
        ? event
        : { preventDefault() {}, ...event };
    (table[type] || []).slice().forEach(fn => fn(payload));
  };

  const document = {
    getElementById: () => null,
    addEventListener: (type, fn) => record(docListeners, type, fn),
    removeEventListener: (type, fn) => drop(docListeners, type, fn)
  };
  const window = {
    innerWidth: 1000,
    innerHeight: 600,
    location: { href: '', search: '' },
    addEventListener: (type, fn) => record(winListeners, type, fn),
    removeEventListener: (type, fn) => drop(winListeners, type, fn),
    dispatchEvent: event => {
      dispatched.push(event);
      return true;
    }
  };

  return {
    document,
    window,
    dispatched,
    fireDocument: (type, event) => emit(docListeners, type, event),
    fireWindow: (type, event) => emit(winListeners, type, event),
    documentListeners: type => (docListeners[type] || []).length,
    windowListeners: type => (winListeners[type] || []).length
  };
}

class TestCustomEvent {
  constructor(type, init) {
    this.type = type;
    this.detail = (init && init.detail) || null;
  }
}

function loadMapForMenu(env, positions) {
  vm.runInNewContext(
    source,
    {
      window: env.window,
      document: env.document,
      setTimeout,
      clearTimeout,
      CustomEvent: TestCustomEvent,
      URLSearchParams,
      fetch: () =>
        jsonResponse({
          schema_version: 1,
          revision: 1,
          snap_to_grid: true,
          positions: positions || { 'ws-1': { x: 100, y: 100 }, 'ws-2': { x: 400, y: 100 } }
        }),
      console: { error() {}, warn() {}, log() {} }
    },
    { filename: 'workspace-map.js' }
  );
  return env.window.OriWorkspaceMap;
}

// Mount the cockpit's map with two workspaces and whatever handlers the test
// cares about, then hand back everything needed to drive a right-click.
async function menuHarness({ handlers = {}, workspaces, tiles } = {}) {
  const env = menuEnvironment();
  const map = loadMapForMenu(env);
  const harness = createCameraHarness({ tiles: tiles || ['ws-1', 'ws-2'] });
  map.mount(harness.container, {
    workspaces: workspaces || [
      { id: 'ws-1', name: 'Alpha' },
      { id: 'ws-2', name: 'Beta' }
    ],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    ...handlers
  });
  await flush();
  return { map, harness, env };
}

// Right-click something. `closest` answers for the element the map asks about,
// which is how the delegated listener resolves its target.
function rightClick(match, { x = 200, y = 150 } = {}) {
  let prevented = false;
  const target = {
    closest: sel => match(sel)
  };
  return {
    event: {
      target,
      clientX: x,
      clientY: y,
      preventDefault: () => {
        prevented = true;
      }
    },
    prevented: () => prevented
  };
}

const tileTarget = id => sel =>
  sel.includes('data-ws-id') && !sel.includes('data-hq-site')
    ? { getAttribute: () => id, focus() {}, focused: false }
    : null;

test('contextMenuHTML renders a menu with menuitems, dividers, danger and disabled variants', () => {
  const { contextMenuHTML } = loadOriWorkspaceMap();
  const html = contextMenuHTML(
    [
      { label: 'Open workspace', action: 'open' },
      { divider: true },
      { label: 'Build', action: 'build', disabled: true },
      { label: 'Delete workspace', action: 'delete', variant: 'danger' }
    ],
    { label: 'Actions for Alpha' }
  );
  assert.match(html, /role="menu"/);
  assert.match(html, /aria-label="Actions for Alpha"/);
  assert.match(html, /role="menuitem"[^>]*data-menu-action="open"/);
  assert.match(html, /ori-context-divider[^>]*role="separator"/);
  assert.match(html, /data-menu-action="build"[^>]*aria-disabled="true"/);
  assert.match(html, /ori-context-danger[^>]*data-menu-action="delete"/);
  // Every item is reachable only through the roving tabindex.
  assert.equal((html.match(/tabindex="-1"/g) || []).length, 3);
});

test('contextMenuHTML escapes a hostile workspace name instead of rendering it', () => {
  const { contextMenuHTML } = loadOriWorkspaceMap();
  const html = contextMenuHTML([{ label: 'Delete <img src=x>', action: 'delete' }], {
    label: '<script>'
  });
  assert.ok(!html.includes('<img'), 'the label is escaped');
  assert.ok(!html.includes('<script>'), 'the menu label is escaped');
});

test('the menu flips back across the cursor at the right and bottom edges (FR-17)', () => {
  const { contextMenuPosition } = loadOriWorkspaceMap();
  const size = { width: 200, height: 180 };
  const viewport = { width: 1000, height: 600 };

  const roomy = contextMenuPosition({ x: 300, y: 200 }, size, viewport);
  assert.deepEqual({ ...roomy }, { left: 300, top: 200 }, 'with room, it opens at the cursor');

  const nearRight = contextMenuPosition({ x: 960, y: 200 }, size, viewport);
  assert.equal(nearRight.left, 760, 'flips left of the cursor');

  const nearBottom = contextMenuPosition({ x: 300, y: 580 }, size, viewport);
  assert.equal(nearBottom.top, 400, 'flips above the cursor');
});

test('the menu never renders off-screen, even in a viewport smaller than itself', () => {
  const { contextMenuPosition } = loadOriWorkspaceMap();
  const placed = contextMenuPosition(
    { x: 10, y: 10 },
    { width: 400, height: 500 },
    { width: 300, height: 200 }
  );
  assert.ok(placed.left >= 0 && placed.top >= 0, 'stays on screen: ' + JSON.stringify(placed));
});

test('a workspace tile offers open, backlog, selection, and a danger delete', () => {
  const map = loadOriWorkspaceMap();
  map._setLayoutForTest({ positions: {} }, 'ready');
  const items = map.contextMenuItemsFor({ type: 'tile', id: 'ws-1', ws: { id: 'ws-1' } });
  // The map builds its arrays inside the vm sandbox, so they need copying into
  // this realm before a strict deepEqual (see `ids` above).
  const actions = Array.from(items.filter(item => !item.divider).map(item => item.action));
  assert.deepEqual(actions, ['open', 'open-backlog', 'toggle-selection', 'delete']);
  const del = items[items.length - 1];
  assert.equal(del.variant, 'danger', 'delete is the danger item');
  assert.equal(del.label, 'Delete workspace');
  assert.equal(items.filter(item => item.divider).length, 2, 'two dividers separate the groups');
});

test('the tile menu names Delete group for a group, as the rail already does', () => {
  const map = loadOriWorkspaceMap();
  const items = map.contextMenuItemsFor({
    type: 'tile',
    id: 'grp-1',
    ws: { id: 'grp-1', kind: 'group' }
  });
  assert.equal(items[items.length - 1].label, 'Delete group');
});

test('Open → Setup appears only for a workspace whose setup status is known and applicable', () => {
  const map = loadOriWorkspaceMap();
  const without = map.contextMenuItemsFor({ type: 'tile', id: 'ws-1', ws: { id: 'ws-1' } });
  assert.ok(
    !without.some(item => item.action === 'open-setup'),
    'an unfetched status shows no Setup item, exactly as the rail shows no Setup row'
  );

  map._setSetupStatusForTest('ws-1', { applicable: true, state: 'in_progress' });
  const withSetup = map.contextMenuItemsFor({ type: 'tile', id: 'ws-1', ws: { id: 'ws-1' } });
  assert.ok(withSetup.some(item => item.action === 'open-setup'));

  map._setSetupStatusForTest('ws-2', { applicable: false, state: 'not_applicable' });
  const notApplicable = map.contextMenuItemsFor({ type: 'tile', id: 'ws-2', ws: { id: 'ws-2' } });
  assert.ok(!notApplicable.some(item => item.action === 'open-setup'));
});

test('right-clicking a tile opens the menu and suppresses the browser menu', async () => {
  const { harness } = await menuHarness();
  const click = rightClick(tileTarget('ws-1'));
  harness.fire('contextmenu', click.event);

  assert.ok(click.prevented(), 'the native menu is suppressed');
  assert.ok(harness.menu.isOpen(), 'the map menu is open');
  assert.deepEqual(harness.menu.labels(), [
    'Open workspace',
    'Open → Backlog',
    'Add to selection',
    'Delete workspace'
  ]);
});

test('the menu is positioned in viewport coordinates from the cursor', async () => {
  const { harness } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1'), { x: 240, y: 310 }).event);
  assert.equal(harness.menu.menu().style.left, '240px');
  assert.equal(harness.menu.menu().style.top, '310px');
});

test('right-clicking an unselected tile selects it first, and never opens it (FR-6, FR-9)', async () => {
  const selected = [];
  const opened = [];
  const { map, harness } = await menuHarness({
    handlers: { onSelect: id => selected.push(id), onOpen: id => opened.push(id) }
  });

  harness.fire('contextmenu', rightClick(tileTarget('ws-2')).event);

  assert.deepEqual(selected, ['ws-2'], 'the right-clicked tile becomes the selection');
  assert.equal(map.getSelectedId(), 'ws-2');
  assert.deepEqual(opened, [], 'right-click never opens a workspace');
});

test('right-click does not toggle the multi-select checkbox', async () => {
  const { harness } = await menuHarness();

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);

  // Nothing was checked, so this is the single-target menu — and it offers to
  // add the tile, which it would not if the right-click had already done so.
  assert.equal(harness.menu.item('group-multi'), null, 'the bulk set is still empty');
  assert.equal(harness.menu.item('toggle-selection').label, 'Add to selection');
  assert.equal(
    harness.container.querySelector('[data-ws-selbar]').hidden,
    true,
    'the selection bar stayed hidden'
  );
});

test('each tile item runs the action the rail runs', async () => {
  const opened = [];
  const deleted = [];
  const { map, harness, env } = await menuHarness({
    handlers: { onOpen: id => opened.push(id), onDeleteWorkspace: id => deleted.push(id) }
  });

  const open = () => harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);

  open();
  harness.menu.item('open').fire('click');
  assert.deepEqual(opened, ['ws-1'], 'Open routes to the host open');

  open();
  harness.menu.item('open-backlog').fire('click');
  assert.match(env.window.location.href, /\/workspaces\/ws-1\?panel=backlog$/);

  open();
  harness.menu.item('toggle-selection').fire('click');
  open();
  // The tile is now inside the checked set, so it acts on the set.
  assert.equal(harness.menu.item('toggle-selection'), null);
  assert.ok(harness.menu.item('delete-multi'), 'the tile joined the bulk set');
  harness.menu.item('clear-selection').fire('click');

  open();
  assert.equal(
    harness.menu.item('toggle-selection').label,
    'Add to selection',
    'and offers to join again once the set is empty'
  );
  harness.menu.item('delete').fire('click');
  assert.deepEqual(deleted, ['ws-1'], 'Delete routes to the host delete');
  assert.equal(map.getSelectedId(), 'ws-1');
});

test('choosing an item closes the menu', async () => {
  const { harness } = await menuHarness({ handlers: { onOpen() {} } });
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('open').fire('click');
  assert.equal(harness.menu.isOpen(), false);
});

test('every dismissal route closes the menu (FR-18)', async () => {
  const routes = [
    ['Escape', ({ env }) => env.fireDocument('keydown', { key: 'Escape' })],
    [
      'a click outside',
      ({ env }) => env.fireDocument('mousedown', { target: { closest: () => null } })
    ],
    [
      'a right-click outside',
      ({ env }) => env.fireDocument('contextmenu', { target: { closest: () => null } })
    ],
    ['a window resize', ({ env }) => env.fireWindow('resize', {})],
    [
      'a camera change',
      ({ harness }) => harness.container.querySelector('[data-map-zoom-in]').click()
    ]
  ];

  for (const [name, dismiss] of routes) {
    const ctx = await menuHarness();
    ctx.harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
    assert.ok(ctx.harness.menu.isOpen(), 'menu opened before ' + name);
    dismiss(ctx);
    assert.equal(ctx.harness.menu.isOpen(), false, name + ' must close the menu');
  }
});

test('a click inside the menu does not dismiss it before the item runs', async () => {
  const { harness, env } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  env.fireDocument('mousedown', {
    target: { closest: sel => (sel.includes('ws-map-menu') ? {} : null) }
  });
  assert.ok(harness.menu.isOpen(), 'a press on the menu itself is not "outside"');
});

test('the right-click that opened the menu does not also dismiss it', async () => {
  const { harness, env } = await menuHarness();
  const click = rightClick(tileTarget('ws-1'));
  harness.fire('contextmenu', click.event);
  // The opening right-click is still propagating when the dismissal listeners
  // are added, and a listener attached to a node the event has not reached yet
  // is still called for it. Without the open-event guard the menu opened and
  // closed in the same gesture — which is exactly what the browser demo showed.
  env.fireDocument('contextmenu', click.event);
  assert.ok(harness.menu.isOpen(), 'the opening gesture must not close the menu');

  const elsewhere = rightClick(() => null);
  env.fireDocument('contextmenu', elsewhere.event);
  assert.equal(harness.menu.isOpen(), false, 'a later right-click still dismisses it');
});

test('the menu unbinds its global listeners when it closes', async () => {
  const { harness, env } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  assert.ok(env.documentListeners('keydown') > 0, 'listeners are bound while open');
  env.fireDocument('keydown', { key: 'Escape' });
  assert.equal(env.documentListeners('keydown'), 0, 'and dropped when it closes');
  assert.equal(env.documentListeners('mousedown'), 0);
  assert.equal(env.windowListeners('resize'), 0);
});

test('only one menu is open at a time', async () => {
  const { harness } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  const first = harness.menu.menu();
  harness.fire('contextmenu', rightClick(tileTarget('ws-2'), { x: 500, y: 400 }).event);
  const second = harness.menu.menu();
  assert.notEqual(first, second, 'the second right-click replaced the first menu');
  assert.equal(second.style.left, '500px');
});

test('the menu opens with the first item focused and moves focus with the arrows', async () => {
  const { harness } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);

  assert.equal(harness.menu.focused().action, 'open', 'the first item takes focus (FR-22)');
  assert.equal(harness.menu.item('open').getAttribute('tabindex'), '0', 'roving tabindex');

  const menu = harness.menu.menu();
  menu.fire('keydown', { key: 'ArrowDown' });
  assert.equal(harness.menu.focused().action, 'open-backlog');
  menu.fire('keydown', { key: 'ArrowUp' });
  assert.equal(harness.menu.focused().action, 'open');
  menu.fire('keydown', { key: 'ArrowUp' });
  assert.equal(harness.menu.focused().action, 'delete', 'arrow navigation wraps at both ends');
  menu.fire('keydown', { key: 'Home' });
  assert.equal(harness.menu.focused().action, 'open');
  menu.fire('keydown', { key: 'End' });
  assert.equal(harness.menu.focused().action, 'delete');
  assert.equal(
    harness.menu.item('open').getAttribute('tabindex'),
    '-1',
    'only one item is tabbable'
  );
});

test('Enter activates the focused item and Escape closes without acting', async () => {
  const deleted = [];
  const { harness } = await menuHarness({
    handlers: { onDeleteWorkspace: id => deleted.push(id) }
  });

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.menu().fire('keydown', { key: 'End' });
  harness.menu.menu().fire('keydown', { key: 'Enter' });
  assert.deepEqual(deleted, ['ws-1'], 'Enter runs the focused item');

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.menu().fire('keydown', { key: 'Escape' });
  assert.equal(harness.menu.isOpen(), false);
  assert.deepEqual(deleted, ['ws-1'], 'Escape acts on nothing');
});

test('closing the menu returns focus to the tile it was opened from (FR-19)', async () => {
  const { harness } = await menuHarness();
  let focused = 0;
  const origin = { getAttribute: () => 'ws-1', focus: () => (focused += 1) };
  harness.fire('contextmenu', {
    target: { closest: sel => (sel.includes('data-ws-id') ? origin : null) },
    clientX: 100,
    clientY: 100,
    preventDefault() {}
  });
  harness.menu.menu().fire('keydown', { key: 'Escape' });
  assert.equal(focused, 1, 'focus went back exactly once');
});

// --- the multi-target menu (FR-7, FR-11, FR-25) ------------------------------

// Check both tiles the way a mouse user does, then right-click one of them.
async function selectedPairHarness(handlers = {}) {
  const ctx = await menuHarness({ handlers });
  ctx.harness.tile('ws-1').fire('click', checkboxClick());
  ctx.harness.tile('ws-2').fire('click', checkboxClick());
  return ctx;
}

test('right-clicking inside the checked set opens the multi-target menu with the count', async () => {
  const { harness } = await selectedPairHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);

  assert.deepEqual(harness.menu.labels(), [
    'Group 2 workspaces',
    'Clear selection',
    'Delete 2 workspaces'
  ]);
});

test('right-clicking inside the selection leaves the selection untouched (FR-7)', async () => {
  const grouped = [];
  const { harness } = await selectedPairHarness({ onGroupWorkspaces: list => grouped.push(list) });

  harness.fire('contextmenu', rightClick(tileTarget('ws-2')).event);
  harness.menu.item('group-multi').fire('click');

  assert.deepEqual(
    ids(grouped),
    [['ws-1', 'ws-2']],
    'both checked workspaces survived the right-click'
  );
});

test('right-clicking outside the checked set gives that tile its own menu', async () => {
  const { harness } = await menuHarness();
  harness.tile('ws-1').fire('click', checkboxClick());

  harness.fire('contextmenu', rightClick(tileTarget('ws-2')).event);
  assert.ok(harness.menu.item('delete'), 'ws-2 is not in the set, so it acts on itself');
  assert.equal(harness.menu.item('delete-multi'), null);
});

test('the multi-target labels count exactly what is checked', async () => {
  const { harness } = await menuHarness();
  harness.tile('ws-1').fire('click', checkboxClick());

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  assert.deepEqual(harness.menu.labels(), [
    'Group 1 workspace',
    'Clear selection',
    'Delete 1 workspace'
  ]);
});

test('the multi-target menu deletes and clears through the existing paths', async () => {
  const deleted = [];
  const { map, harness } = await selectedPairHarness({
    onDeleteWorkspaces: list => deleted.push(list)
  });

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('delete-multi').fire('click');
  assert.deepEqual(ids(deleted), [['ws-1', 'ws-2']], 'one call, with both ids, no second confirm');

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('clear-selection').fire('click');
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  assert.equal(harness.menu.item('delete-multi'), null, 'Clear selection emptied the set');
  assert.equal(map.getSelectedId(), 'ws-1', 'clearing the bulk set is not a selection change');
});

test('the selection bar still counts in place, and still hides at zero', async () => {
  const { harness } = await menuHarness();
  const bar = harness.container.querySelector('[data-ws-selbar]');
  const count = harness.container.querySelector('[data-ws-selbar] [data-ws-selbar-count]');

  harness.tile('ws-1').fire('click', checkboxClick());
  assert.equal(bar.hidden, false, 'the bar appears once something is checked');
  assert.equal(count.textContent, '1 selected');

  harness.tile('ws-2').fire('click', checkboxClick());
  assert.equal(count.textContent, '2 selected', 'updated in place, without a re-mount');

  harness.tile('ws-1').fire('click', checkboxClick());
  harness.tile('ws-2').fire('click', checkboxClick());
  assert.equal(bar.hidden, true, 'and hides again at zero');
});

// --- districts, the HQ site, and empty canvas (FR-12, FR-13, FR-14) ----------

const districtTarget = id => sel =>
  sel.includes('ws-map-district') ? { getAttribute: () => id, focus() {} } : null;

const hqTarget = () => sel => (sel.includes('data-hq-site') ? { focus() {} } : null);

const canvasTarget = () => sel => (sel.includes('ws-map-canvas') ? { focus() {} } : null);

test('a group district offers Open group and a danger Delete group', () => {
  const map = loadOriWorkspaceMap();
  map._setLayoutForTest({ positions: {} }, 'ready');
  const items = map.contextMenuItemsFor({
    type: 'district',
    id: 'grp-1',
    ws: { id: 'grp-1', kind: 'group', name: 'Ops' }
  });
  const actions = Array.from(items.filter(item => !item.divider).map(item => item.action));
  assert.deepEqual(actions, ['open', 'delete']);
  assert.equal(items[0].label, 'Open group');
  assert.equal(items[items.length - 1].label, 'Delete group');
  assert.equal(items[items.length - 1].variant, 'danger');
});

test('the HQ site mirrors the rail: build and import always, clear only when repairing', () => {
  const map = loadOriWorkspaceMap();
  const labelsFor = view =>
    Array.from(map.contextMenuItemsFor({ type: 'hq', view }).map(item => item.label));

  assert.deepEqual(labelsFor({ repair: false, showSkip: true }), [
    'Build My HQ',
    'Import HQ',
    'Not now'
  ]);
  assert.deepEqual(labelsFor({ repair: false, showSkip: false }), ['Build My HQ', 'Import HQ']);
  assert.deepEqual(labelsFor({ repair: true, showSkip: true }), [
    'Build replacement HQ',
    'Import HQ',
    'Clear broken HQ link'
  ]);
});

test('the HQ menu never offers both Clear and Not now', () => {
  const map = loadOriWorkspaceMap();
  [
    { repair: true, showSkip: true },
    { repair: true, showSkip: false },
    { repair: false, showSkip: true },
    { repair: false, showSkip: false }
  ].forEach(view => {
    const actions = map.contextMenuItemsFor({ type: 'hq', view }).map(item => item.action);
    assert.ok(
      !(actions.includes('hq-clear') && actions.includes('hq-skip')),
      'clear and skip are mutually exclusive: ' + JSON.stringify(Array.from(actions))
    );
  });
});

test('empty canvas offers create plus the framing actions, with Center disabled when nothing is selected', () => {
  const map = loadOriWorkspaceMap();
  map._setLayoutForTest({ positions: {} }, 'ready');
  const items = map.contextMenuItemsFor({ type: 'canvas' });
  const actions = Array.from(items.filter(item => !item.divider).map(item => item.action));
  assert.deepEqual(actions, ['build', 'fit', 'center', 'reset-view']);
  const center = items.find(item => item.action === 'center');
  assert.equal(center.disabled, true, 'Center Selected has nothing to centre yet');
  assert.ok(
    !actions.includes('clear-selection'),
    'no Clear selection entry while the checked set is empty'
  );
});

test('the canvas menu offers Clear selection once something is checked', async () => {
  const { map, harness } = await menuHarness();
  harness.tile('ws-1').fire('click', checkboxClick());
  const actions = map.contextMenuItemsFor({ type: 'canvas' }).map(item => item.action);
  assert.ok(Array.from(actions).includes('clear-selection'));
});

test('each of the four targets opens its own menu', async () => {
  const { harness } = await menuHarness();

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  assert.ok(harness.menu.item('open-backlog'), 'tile menu');

  harness.fire('contextmenu', rightClick(districtTarget('grp-1')).event);
  assert.ok(harness.menu.item('delete'), 'district menu');
  assert.equal(harness.menu.item('open-backlog'), null, 'a district has no backlog entry');

  harness.fire('contextmenu', rightClick(hqTarget()).event);
  assert.ok(harness.menu.item('hq-build'), 'HQ menu');

  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  assert.ok(harness.menu.item('build'), 'canvas menu');
  assert.ok(harness.menu.item('fit'), 'with the framing actions');
});

test('the HQ items dispatch the same event the rail dispatches', async () => {
  const { harness, env } = await menuHarness();
  harness.fire('contextmenu', rightClick(hqTarget()).event);
  harness.menu.item('hq-build').fire('click');

  const dispatched = env.dispatched.filter(event => event.type === 'ori:personal-hq-action');
  assert.equal(dispatched.length, 1);
  assert.deepEqual({ ...dispatched[0].detail }, { action: 'build' });
});

test('right-clicking empty canvas leaves the selection alone (FR-8)', async () => {
  const selected = [];
  const { map, harness } = await menuHarness({ handlers: { onSelect: id => selected.push(id) } });

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.tile('ws-2').fire('click', checkboxClick());
  const before = map.getSelectedId();
  selected.length = 0;

  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  assert.equal(map.getSelectedId(), before, 'the selected workspace is untouched');
  assert.deepEqual(selected, [], 'and the host was not told the selection changed');
  assert.ok(harness.menu.item('clear-selection'), 'the checked set survived too');
});

test('the canvas framing items move the camera without touching a workspace', async () => {
  const { map, harness } = await menuHarness();
  const before = map.getCamera();

  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  harness.menu.item('fit').fire('click');
  const fitted = map.getCamera();
  assert.notDeepEqual({ ...fitted }, { ...before }, 'Fit all reframed the view');

  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  harness.menu.item('reset-view').fire('click');
  assert.equal(map.getCamera().zoom, 1, 'Reset view returns to 100%');
});

// --- read-only, the keyboard open path, and announcements (FR-3, FR-15, FR-21, FR-23) ---

test('a read-only map disables exactly the mutating items, and nothing else', () => {
  const map = loadOriWorkspaceMap();
  map._setLayoutForTest({ positions: {} }, 'unavailable');

  const tile = map.contextMenuItemsFor({ type: 'tile', id: 'ws-1', ws: { id: 'ws-1' } });
  const byAction = items => {
    const table = {};
    items.forEach(item => {
      if (item.action) table[item.action] = !!item.disabled;
    });
    return table;
  };
  const tileState = byAction(tile);
  assert.equal(tileState.delete, true, 'Delete cannot run without a layout to save');
  assert.equal(tileState.open, false, 'looking around is still allowed');
  assert.equal(tileState['open-backlog'], false);
  assert.equal(tileState['toggle-selection'], false);

  const canvas = byAction(map.contextMenuItemsFor({ type: 'canvas' }));
  assert.equal(canvas.build, true, 'Build is disabled');
  assert.equal(canvas.fit, false, 'the framing actions stay enabled');
  assert.equal(canvas['reset-view'], false);

  const district = byAction(
    map.contextMenuItemsFor({ type: 'district', id: 'g', ws: { id: 'g', kind: 'group' } })
  );
  assert.equal(district.delete, true);
  assert.equal(district.open, false);
});

test('a ready map disables nothing that a read-only one would', () => {
  const map = loadOriWorkspaceMap();
  map._setLayoutForTest({ positions: {} }, 'ready');
  const items = map.contextMenuItemsFor({ type: 'tile', id: 'ws-1', ws: { id: 'ws-1' } });
  assert.ok(items.every(item => !item.disabled));
});

test('disabled items are announced as disabled and skipped by arrow navigation (FR-21)', async () => {
  const { map, harness } = await menuHarness();
  // A map whose layout could not load: Build is present but disabled.
  map._setLayoutForTest({ positions: {} }, 'unavailable');

  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  const create = harness.menu.item('build');
  assert.equal(create.getAttribute('aria-disabled'), 'true', 'still present, still announced');
  assert.notEqual(harness.menu.focused().action, 'build', 'focus starts on the first enabled item');

  const menu = harness.menu.menu();
  menu.fire('keydown', { key: 'ArrowUp' });
  assert.notEqual(harness.menu.focused().action, 'build', 'wrapping skips it too');

  // And it cannot be run.
  create.fire('click');
  assert.ok(harness.menu.isOpen(), 'a disabled item does not even close the menu');
});

test('Shift+F10 and the Context Menu key open the menu from a focused target (FR-3)', async () => {
  for (const key of [{ key: 'F10', shiftKey: true }, { key: 'ContextMenu' }]) {
    const { harness, env } = await menuHarness();
    // Tall enough that the menu opens below the element rather than flipping up
    // over it — the flip itself is covered by the placement tests.
    env.window.innerHeight = 1200;
    let prevented = false;
    harness.fire('keydown', {
      ...key,
      target: {
        closest: sel =>
          sel.includes('data-ws-id') && !sel.includes('data-hq-site')
            ? {
                getAttribute: () => 'ws-1',
                focus() {},
                getBoundingClientRect: () => ({ left: 300, top: 200, width: 176, height: 150 })
              }
            : null
      },
      preventDefault: () => {
        prevented = true;
      }
    });
    assert.ok(harness.menu.isOpen(), JSON.stringify(key) + ' opens the menu');
    assert.ok(prevented, 'and suppresses the browser default');
    // Anchored to the element, since a key press has no cursor position.
    assert.equal(harness.menu.menu().style.left, '300px');
    assert.equal(harness.menu.menu().style.top, '350px', 'below the focused element');
  }
});

test('an ordinary key press does not open the menu', async () => {
  const { harness } = await menuHarness();
  harness.fire('keydown', {
    key: 'F10',
    target: { closest: () => ({ getAttribute: () => 'ws-1' }) }
  });
  assert.equal(harness.menu.isOpen(), false, 'F10 without Shift is not the gesture');
});

test('menu actions speak through the map’s existing live region (FR-23)', async () => {
  const { harness } = await menuHarness({ handlers: { onDeleteWorkspace() {} } });
  const live = harness.container.querySelector('[data-map-live]');

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('toggle-selection').fire('click');
  assert.match(live.textContent, /Added Alpha\. 1 selected/);

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('clear-selection').fire('click');
  assert.equal(live.textContent, 'Selection cleared');

  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  harness.menu.item('delete').fire('click');
  assert.match(live.textContent, /Delete Alpha — confirm to continue/, 'it claims no outcome');
});

test('the announcement never moves focus (FR-24)', async () => {
  const { harness } = await menuHarness();
  let focusCalls = 0;
  const origin = {
    getAttribute: () => 'ws-1',
    focus: () => (focusCalls += 1)
  };
  harness.fire('contextmenu', {
    target: { closest: sel => (sel.includes('data-ws-id') ? origin : null) },
    clientX: 100,
    clientY: 100,
    preventDefault() {}
  });
  harness.menu.item('toggle-selection').fire('click');
  // Exactly one focus call: the deliberate restore on close, not the
  // announcement.
  assert.equal(focusCalls, 1);
});

test('focus returns to its origin from every target', async () => {
  const targets = [
    ['tile', tileTarget('ws-1')],
    ['district', districtTarget('grp-1')],
    ['HQ site', hqTarget()],
    ['canvas', canvasTarget()]
  ];
  for (const [name, matcher] of targets) {
    const { harness } = await menuHarness();
    let focused = 0;
    const origin = { getAttribute: () => 'grp-1', focus: () => (focused += 1) };
    harness.fire('contextmenu', {
      target: {
        closest: sel => {
          const hit = matcher(sel);
          return hit ? Object.assign({}, hit, origin) : null;
        }
      },
      clientX: 120,
      clientY: 120,
      preventDefault() {}
    });
    assert.ok(harness.menu.isOpen(), name + ' opened a menu');
    harness.menu.menu().fire('keydown', { key: 'Escape' });
    assert.equal(focused, 1, 'focus went back to the ' + name);
  }
});

test('the legacy launcher mode behaves exactly like the cockpit', async () => {
  const env = menuEnvironment();
  const map = loadMapForMenu(env);
  const harness = createCameraHarness({ tiles: ['ws-1', 'ws-2'] });
  // No selectOnly, no hideChrome: the /workspaces launcher's mount.
  map.mount(harness.container, {
    workspaces: [
      { id: 'ws-1', name: 'Alpha' },
      { id: 'ws-2', name: 'Beta' }
    ]
  });
  await flush();

  harness.fire('contextmenu', rightClick(tileTarget('ws-2')).event);
  assert.deepEqual(harness.menu.labels(), [
    'Open workspace',
    'Open → Backlog',
    'Add to selection',
    'Delete workspace'
  ]);
  assert.equal(map.getSelectedId(), 'ws-2', 'and right-click still selects first');
});

// --- the framing buttons moved into the menu (#317) --------------------------

test('the control strip keeps zoom only — Fit all, Center and Reset left it', async () => {
  const { harness } = await menuHarness();
  const html = harness.container.innerHTML;
  assert.match(html, /data-map-zoom-out/, 'zoom out stays');
  assert.match(html, /data-map-zoom-in/, 'zoom in stays');
  assert.match(html, /data-map-zoom-readout/, 'and the readout');
  assert.doesNotMatch(html, /data-map-fit/, 'Fit all is on the canvas menu now');
  assert.doesNotMatch(html, /data-map-center/, 'so is Center selected');
  assert.doesNotMatch(html, /data-map-reset-view/, 'and Reset view');
  // The placement cluster is untouched: those are not framing actions.
  assert.match(html, /data-map-build/);
  assert.match(html, /data-map-reset-layout/);
});

test('the three framing actions are still reachable, by menu and by key', async () => {
  const { map, harness } = await menuHarness();
  const live = harness.container.querySelector('[data-map-live]');

  // Menu route.
  harness.fire('contextmenu', rightClick(canvasTarget()).event);
  const actions = Array.from(harness.menu.items().map(item => item.action));
  assert.deepEqual(actions, ['build', 'fit', 'center', 'reset-view']);
  harness.menu.item('fit').fire('click');
  assert.match(live.textContent, /workspace/i, 'Fit all announced through the live region');

  // Keyboard route on the canvas, which is what the buttons used to give.
  harness.fire('keydown', { key: '0', preventDefault() {} });
  assert.equal(map.getCamera().zoom, 1, '0 resets the view');
  assert.match(live.textContent, /View reset/, 'and says so now that no button does');
});

test('the help panel does not advertise f, which the app-wide link hints take', async () => {
  const { harness } = await menuHarness();
  const help = harness.container.innerHTML;
  assert.match(help, /How the map works/, 'the help panel is rendered');
  // keyboard-navigation.js claims `f` globally in the capture phase, so the
  // map's own f-to-Fit-All handler never runs in the app. Telling users to
  // press it would be a lie; the menu and 0 are the honest routes.
  assert.doesNotMatch(help, /press f\b/i);
  assert.match(help, /0 resets the view/);
  assert.match(help, /Shift\+F10/, 'and the keyboard route to the menu is documented');
});

test('Shift+F10 on the focused canvas opens the canvas menu (the only keyboard route to Center)', async () => {
  const { harness } = await menuHarness();
  harness.fire('keydown', {
    key: 'F10',
    shiftKey: true,
    target: {
      closest: sel => (sel.includes('ws-map-canvas') ? { focus() {} } : null),
      getBoundingClientRect: () => ({ left: 0, top: 0, width: 800, height: 400 })
    },
    preventDefault() {}
  });
  assert.ok(harness.menu.isOpen(), 'the canvas menu opens from the keyboard');
  assert.ok(harness.menu.item('center'), 'and carries Center selected');
});

test('a re-mount closes an open menu instead of leaving it bound to dead DOM', async () => {
  const { map, harness } = await menuHarness();
  harness.fire('contextmenu', rightClick(tileTarget('ws-1')).event);
  assert.ok(harness.menu.isOpen());
  map.mount(harness.container, {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true
  });
  await flush();
  assert.equal(harness.menu.isOpen(), false);
});

// --- Focus-intent selection is announced to the host (#322) -----------------
//
// `?focus=personal-hq` is the one selection the map makes with no click behind
// it. Every other selection reaches the host through a handler, so the host
// hears about it; this one it can only hear about if the map says so. It cannot
// poll for it either, because HQ status arrives asynchronously and re-mounts the
// map from setHQStatus long after the host's own mount() call has returned.
//
// When this is silent the map draws a selected HQ landmark while the cockpit
// rail is still on Today — and since cockpit mode has no map-owned overview
// panel, the rail is the ONLY place the Build My HQ button exists. The result
// is a highlighted HQ with no way to act on it.

const UNBUILT_HQ = { valid: false, hq_onboarding_state: 'unseen' };

function loadMapAt(search) {
  const window = { addEventListener() {}, location: { search } };
  const fetch = () => new Promise(() => {});
  vm.runInNewContext(
    source,
    {
      window,
      document: { getElementById: () => null },
      setTimeout,
      clearTimeout,
      fetch,
      // hasHQFocusIntent parses the query string with it.
      URLSearchParams
    },
    { filename: 'workspace-map.js' }
  );
  return window.OriWorkspaceMap;
}

function cockpitState(extra) {
  return {
    workspaces: [{ id: 'ws-1', name: 'Alpha' }],
    hideChrome: true,
    selectOnly: true,
    noAutoSelect: true,
    ...extra
  };
}

test('focus intent tells the host the HQ site was auto-selected (#322)', () => {
  const map = loadMapAt('?focus=personal-hq');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  map.setHQStatus(UNBUILT_HQ);
  map.mount(container, cockpitState({ onSelectHQSite: view => seen.push(view) }));

  assert.equal(map.getSelectedId(), '__personal_hq_site__');
  assert.equal(seen.length, 1, 'the host is told exactly once');
  assert.equal(seen[0].show, true);
  assert.equal(seen[0].repair, false);
});

test('the HQ status arriving after mount still reaches the host (the #322 race)', () => {
  const map = loadMapAt('?focus=personal-hq');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  const state = cockpitState({ onSelectHQSite: view => seen.push(view) });

  // First mount happens before /api/personal-hq/status resolves, so there is no
  // site to select yet and the host is correctly told nothing.
  map.mount(container, state);
  assert.equal(seen.length, 0);
  assert.notEqual(map.getSelectedId(), '__personal_hq_site__');

  // Status lands and re-mounts the map internally. THIS is the path that used
  // to select the tile without ever telling the cockpit.
  map.setHQStatus(UNBUILT_HQ);
  assert.equal(map.getSelectedId(), '__personal_hq_site__');
  assert.equal(seen.length, 1, 'the late status still announces the selection');
});

test('later re-mounts do not re-announce the focus selection', () => {
  const map = loadMapAt('?focus=personal-hq');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  const state = cockpitState({ onSelectHQSite: view => seen.push(view) });
  map.setHQStatus(UNBUILT_HQ);
  map.mount(container, state);
  assert.equal(seen.length, 1);

  // A filter change, a workspace refresh, a Map/Tree switch: all re-mount. The
  // rail must not be rebuilt and re-announced to screen readers each time.
  map.mount(container, state);
  map.mount(container, state);
  assert.equal(seen.length, 1, 'focus intent is consumed once per page load');
});

test('a status change refreshes the selected HQ site view in the host', () => {
  const map = loadMapAt('?focus=personal-hq');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  const state = cockpitState({ onSelectHQSite: view => seen.push(view) });
  map.setHQStatus(UNBUILT_HQ);
  map.mount(container, state);
  assert.equal(seen.length, 1);
  assert.equal(seen[0].showSkip, true, 'Not now is offered before it is used');

  // Skipping the objective rewrites the site view. The host renders those
  // choices from the view it was handed, so it has to be handed the new one —
  // otherwise "Not now" survives being clicked.
  map.setHQStatus({ valid: false, hq_onboarding_state: 'skipped' });
  assert.equal(seen.length, 2, 'the host is re-notified');
  assert.equal(seen[1].showSkip, false, 'and Not now is gone');
});

test('a status change does not notify a host that has no HQ site selected', () => {
  const map = loadMapAt('');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  map.mount(container, cockpitState({ onSelectHQSite: view => seen.push(view) }));
  map.setHQStatus(UNBUILT_HQ);
  assert.equal(seen.length, 0, 'nothing was selected, so nothing is announced');
});

test('without focus intent the map announces no HQ selection', () => {
  const map = loadMapAt('');
  const { container } = createMapHarness({ tiles: ['ws-1'] });
  const seen = [];
  map.setHQStatus(UNBUILT_HQ);
  map.mount(container, cockpitState({ onSelectHQSite: view => seen.push(view) }));

  assert.equal(seen.length, 0);
  assert.notEqual(map.getSelectedId(), '__personal_hq_site__');
});

test('focus intent on a BUILT HQ selects its workspace and announces that instead', () => {
  const map = loadMapAt('?focus=personal-hq');
  const { container } = createMapHarness({ tiles: ['ws-1', 'hq-1'] });
  const hqSeen = [];
  const selected = [];
  map.setHQStatus({ valid: true, workspace_id: 'hq-1' });
  map.mount(
    container,
    cockpitState({
      workspaces: [
        { id: 'ws-1', name: 'Alpha' },
        { id: 'hq-1', name: 'My HQ' }
      ],
      onSelectHQSite: view => hqSeen.push(view),
      onSelect: (id, ws) => selected.push([id, ws && ws.name])
    })
  );

  // A built HQ has no blueprint site, so this is an ordinary workspace
  // selection — it must travel on onSelect, not the HQ-site channel.
  assert.equal(hqSeen.length, 0);
  assert.deepEqual(selected, [['hq-1', 'My HQ']]);
  assert.equal(map.getSelectedId(), 'hq-1');
});
