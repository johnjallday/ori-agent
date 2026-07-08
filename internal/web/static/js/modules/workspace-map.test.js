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
  layout.tiles.forEach((t) => {
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
  layout.tiles.forEach((t) => {
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
  const members = layout.tiles.filter((t) => t.groupId === 'g');
  assert.equal(members.length, 3);
  members.forEach((t) => {
    assert.ok(t.col >= d.col && t.col < d.col + d.w, 'member col within district');
    assert.ok(t.row >= d.row && t.row < d.row + d.h, 'member row within district');
  });
  assert.ok(layout.tiles.some((t) => t.id === 's'), 'standalone rendered');
  assert.ok(!layout.tiles.some((t) => t.id === 'g'), 'group itself is a district, not a tile');
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
  layout.tiles.forEach((t) => assert.ok(t.col < 5, 'col within bounds'));
});

test('a member whose parent is missing degrades to a standalone tile', () => {
  const computeLayout = loadComputeLayout();
  const layout = computeLayout([{ id: 'orphan', parent_id: 'ghost' }]);
  assert.equal(layout.tiles.length, 1);
  assert.equal(layout.tiles[0].groupId, '');
});

test('computeMaxCols derives the column count from the theatre width', () => {
  const { computeMaxCols } = loadOriWorkspaceMap();
  // cells are 176px wide with 26px padding each side (52 total)
  assert.equal(computeMaxCols(1200), 5); // capped at the desktop max
  assert.equal(computeMaxCols(930), 4);  // (930-52)/176 = 4.98 -> 4
  assert.equal(computeMaxCols(700), 3);
  assert.equal(computeMaxCols(400), 1);
  assert.equal(computeMaxCols(100), 1);  // never below one column
  assert.equal(computeMaxCols(0), 5);    // unmeasurable (hidden) -> default
});

test('narrow column counts wrap tiles into extra rows instead of overflowing', () => {
  const computeLayout = loadComputeLayout();
  const five = Array.from({ length: 5 }, (_, i) => ({ id: 'w' + i }));
  const layout = computeLayout(five, { maxCols: 2 });
  assert.equal(layout.tiles.length, 5);
  layout.tiles.forEach((t) => assert.ok(t.col < 2, 'col stays inside the 2-wide grid'));
  assert.ok(layout.rows >= 3, '5 tiles at 2 cols need at least 3 rows');
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

test('tileHTML meta line reflects enriched agent/task counts with correct pluralization', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const plural = tileHTML({ ws: { id: 'a', name: 'Deep Sea Research', agent_count: 2, open_task_count: 3 }, col: 0, row: 0 });
  assert.match(plural, /ws-map-tile-meta">2 agents · 3 tasks</);

  const singular = tileHTML({ ws: { id: 'b', name: 'Solo', agent_count: 1, open_task_count: 1 }, col: 0, row: 0 });
  assert.match(singular, /ws-map-tile-meta">1 agent · 1 task</);
});

test('tileHTML LED and entry-agent crest reflect active state and entry_agent_name', () => {
  const { tileHTML } = loadOriWorkspaceMap();
  const working = tileHTML({ ws: { id: 'a', name: 'Deep Sea Research', active: true, entry_agent_name: 'Research Lead' }, col: 0, row: 0 });
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
  const html = overviewBodyHTML({ id: 'ws-42', name: 'Deep Sea Research', entry_agent_name: 'Research Lead', agents: ['Research Lead'] });

  // Open button carries the id the click binding reads, and lives inside the hero row (before the first ov-label).
  assert.match(html, /class="ws-map-ov-open" data-ws-open="ws-42"/);
  const heroEnd = html.indexOf('ws-map-ov-label');
  assert.ok(html.indexOf('ws-map-ov-open') < heroEnd, 'Open button should render in the hero row, above the detail rows');
  // The redundant settings cog is gone.
  assert.doesNotMatch(html, /ws-map-ov-cog/);
});

test('overviewBodyHTML falls back to empty-state copy when a workspace has no entry agent or agents', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  const html = overviewBodyHTML({ id: 'a', name: 'Bare Workspace' });

  assert.match(html, /No entry agent/);
  assert.match(html, /No agents yet/);
});

test('overviewBodyHTML renders the select-a-workspace placeholder when nothing is selected', () => {
  const { overviewBodyHTML } = loadOriWorkspaceMap();
  assert.match(overviewBodyHTML(null), /Select a workspace to see its agents, tasks, tools, and skills\./);
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
