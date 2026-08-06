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

test('selBarHTML offers group, delete, and clear actions for the multi-select set', () => {
  const { selBarHTML } = loadOriWorkspaceMap();
  const html = selBarHTML();
  assert.match(html, /data-ws-selbar-group/);
  assert.match(html, /data-ws-selbar-delete/);
  assert.match(html, /data-ws-selbar-clear/);
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

function loadMapWithFetch(fetchImpl, windowExtras = {}) {
  const window = { addEventListener() {}, ...windowExtras };
  vm.runInNewContext(
    source,
    {
      window,
      document: { getElementById: () => null },
      setTimeout,
      clearTimeout,
      fetch: fetchImpl
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

test('zoom is clamped to the usable 50%–200% range (FR-38)', () => {
  const cam = loadCamera();
  assert.equal(cam.limits.min, 0.5);
  assert.equal(cam.limits.max, 2);
  assert.equal(cam.clampZoom(50), 2);
  assert.equal(cam.clampZoom(0.01), 0.5);
  assert.equal(cam.clampZoom(Number.NaN), 1, 'a corrupt zoom opens at 100%, not at nothing');
  assert.equal(cam.zoomAroundCenter({ centerX: 0, centerY: 0, zoom: 2 }, 4).zoom, 2);
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
      setAttribute: () => {},
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
      controls[name] = {
        disabled: false,
        hidden: true,
        textContent: '',
        attrs,
        style: {},
        setAttribute: (k, v) => (attrs[k] = String(v)),
        getAttribute: k => (k in attrs ? attrs[k] : null),
        focus: () => {},
        addEventListener: (type, fn) => {
          if (type === 'click') controls[name].click = fn;
        }
      };
    }
    return controls[name];
  }

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
      const tileMatch = sel.match(/ws-map-tile\[data-ws-id="([^"]+)"\]/);
      if (tileMatch) return tileEls.find(el => el.id === tileMatch[1]) || null;
      const districtMatch = sel.match(/ws-map-district\[data-group-id="([^"]+)"\]/);
      if (districtMatch) return districtEls.find(el => el.id === districtMatch[1]) || null;
      if (sel.includes('data-map-')) return control(sel);
      return null;
    }
  };
  // Writing innerHTML replaces the canvas in a real DOM, which drops every
  // listener bound to the old one. The stub reuses one canvas object, so it has
  // to drop them explicitly — otherwise a second mount would double every
  // gesture and the harness, not the map, would be what the test measured.
  let html = '';
  Object.defineProperty(container, 'innerHTML', {
    get: () => html,
    set: value => {
      html = value;
      Object.keys(listeners).forEach(type => delete listeners[type]);
    }
  });

  return {
    container,
    world,
    styleProps,
    classes,
    control,
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

test('an ordinary empty-space click creates nothing outside Build mode (FR-48)', async () => {
  const { map, harness, modalCalls } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.fire('pointerdown', pointerEvent(500, 300));
  harness.fire('pointerup', pointerEvent(500, 300));
  assert.equal(modalCalls.length, 0, 'clicking the background must not start creating a workspace');
});

test('Build mode captures one empty-space point and hands off to the existing modal (FR-51)', async () => {
  const { map, harness, modalCalls, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.control('[data-map-build]').click();
  assert.equal(harness.control('[data-map-build-banner]').hidden, false, 'the mode is visible');

  harness.fire('pointerdown', pointerEvent(500, 300));
  harness.fire('pointerup', pointerEvent(500, 300));

  assert.equal(modalCalls.length, 1, 'exactly one create flow opened');
  assert.equal(modalCalls[0].mapOrigin, true);
  // FR-62: a point inside a district is still just a point. Nothing about the
  // handoff can carry a parent.
  assert.ok(!('parentId' in modalCalls[0]) && !('parent_id' in modalCalls[0]));
  assert.equal(patches.length, 0, 'nothing is saved for a workspace that does not exist yet');
  assert.equal(map.hasPendingBuild(), true);
  assert.equal(harness.control('[data-map-build-banner]').hidden, true, 'the mode ended');
});

test('a click on a building in Build mode does not place a workspace beneath it (FR-52)', async () => {
  const { map, harness, modalCalls } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();
  harness.control('[data-map-build]').click();

  const onTile = { target: { closest: sel => (sel.includes('ws-map-tile') ? {} : null) } };
  harness.fire('pointerdown', pointerEvent(500, 300, onTile));
  harness.fire('pointerup', pointerEvent(500, 300, onTile));
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

test('keyboard placement moves by a step, commits on Enter, and cancels on Escape (FR-60)', async () => {
  const { map, harness, modalCalls } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.control('[data-map-build]').click();
  const coords = harness.control('[data-map-build-coords]');
  const startText = coords.textContent;
  assert.match(startText, /snapped/, 'the candidate reports its snap state (FR-50)');

  harness.fire('keydown', keyEvent('ArrowRight'));
  const afterOne = coords.textContent;
  assert.notEqual(afterOne, startText, 'the arrow moved the candidate');

  harness.fire('keydown', keyEvent('ArrowRight', { shiftKey: true }));
  const startX = Number(startText.split(',')[0]);
  const shiftX = Number(coords.textContent.split(',')[0]);
  assert.equal(shiftX - startX, 38 + 380, 'Shift uses the documented larger step');

  harness.fire('keydown', keyEvent('Enter'));
  assert.equal(modalCalls.length, 1, 'Enter chose the site');

  // Escape before the modal opens cancels without creating anything.
  harness.control('[data-map-build]').click();
  harness.fire('keydown', keyEvent('Escape'));
  assert.equal(modalCalls.length, 1, 'Escape opened nothing');
  assert.equal(harness.control('[data-map-build-banner]').hidden, true);
});

test('a successful create saves the chosen coordinate exactly once (FR-53)', async () => {
  const { map, harness, patches } = buildHarness();
  mountWithCamera(map, harness, [{ id: 'ws-1', name: 'Alpha' }]);
  await flush();

  harness.control('[data-map-build]').click();
  harness.fire('pointerdown', pointerEvent(600, 200));
  harness.fire('pointerup', pointerEvent(600, 200));

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

  harness.control('[data-map-build]').click();
  harness.fire('pointerdown', pointerEvent(600, 200));
  harness.fire('pointerup', pointerEvent(600, 200));
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

  harness.control('[data-map-build]').click();
  harness.fire('pointerdown', pointerEvent(600, 200));
  harness.fire('pointerup', pointerEvent(600, 200));
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
  // world delta and the expected numbers stay readable.
  harness.control('[data-map-reset-view]').click();
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
