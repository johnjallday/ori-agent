// Tests for home-workspace-cockpit.js — the Map-first Home cockpit
// coordinator's pure decision and rendering helpers.
//
// The module's DOM-wiring IIFE no-ops when `document` is undefined, so these
// run under plain Node with no DOM and no network:
//   node --test internal/web/static/js/modules/home-workspace-cockpit.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  VIEW_MAP,
  VIEW_TREE,
  RAIL_GROUP,
  RAIL_WORKSPACE,
  parseViewFromQuery,
  searchForView,
  readCount,
  formatCount,
  isGroupWorkspace,
  flattenWorkspaceTree,
  findWorkspace,
  normalizeTags,
  buildMapMetadata,
  folderDisplayFor,
  workspaceSignals,
  recommendedNextMove,
  entryAgentName,
  agentRoster,
  workspaceRailView,
  renderWorkspaceRailHTML,
  workspaceAreaState,
  renderWorkspaceAreaStatusHTML,
  escapeHtml
} from './home-workspace-cockpit.js';

// ---------------------------------------------------------------------------
// View state (FR6, FR25, FR26, FR27)
// ---------------------------------------------------------------------------

test('parseViewFromQuery: the query is authoritative when it carries a valid view', () => {
  assert.equal(parseViewFromQuery('?view=tree'), VIEW_TREE);
  assert.equal(parseViewFromQuery('view=tree'), VIEW_TREE);
  assert.equal(parseViewFromQuery('?a=1&view=tree&b=2'), VIEW_TREE);
  // Case and surrounding whitespace must not defeat a legitimate deep link.
  assert.equal(parseViewFromQuery('?view=TREE'), VIEW_TREE);
  assert.equal(parseViewFromQuery('?view=%20tree%20'), VIEW_TREE);
});

test('parseViewFromQuery: missing, empty, and invalid values normalize to Map', () => {
  assert.equal(parseViewFromQuery(''), VIEW_MAP);
  assert.equal(parseViewFromQuery('?'), VIEW_MAP);
  assert.equal(parseViewFromQuery('?view='), VIEW_MAP);
  assert.equal(parseViewFromQuery('?view=map'), VIEW_MAP);
  assert.equal(parseViewFromQuery('?view=nonsense'), VIEW_MAP);
  assert.equal(parseViewFromQuery(null), VIEW_MAP);
  assert.equal(parseViewFromQuery(undefined), VIEW_MAP);
});

test('parseViewFromQuery: the retired cards view normalizes to Map, never restored (FR6/FR27)', () => {
  assert.equal(parseViewFromQuery('?view=cards'), VIEW_MAP);
  assert.equal(parseViewFromQuery('?view=CARDS'), VIEW_MAP);
});

test('searchForView: Tree is explicit, Map REMOVES the default parameter (FR26)', () => {
  assert.equal(searchForView('', VIEW_TREE), '?view=tree');
  assert.equal(searchForView('?view=tree', VIEW_MAP), '');
  // Toggling to Map must not leave `view=map` behind.
  assert.ok(!searchForView('?view=tree', VIEW_MAP).includes('view'));
});

test('searchForView: unrelated query parameters survive a view toggle', () => {
  assert.equal(searchForView('?create=1', VIEW_TREE), '?create=1&view=tree');
  assert.equal(searchForView('?create=1&view=tree', VIEW_MAP), '?create=1');
  assert.equal(
    searchForView('?blueprint=email-ops&focus=personal-hq', VIEW_TREE),
    '?blueprint=email-ops&focus=personal-hq&view=tree'
  );
  // A legacy cards value is replaced rather than duplicated.
  assert.equal(searchForView('?view=cards', VIEW_TREE), '?view=tree');
  assert.equal(searchForView('?view=cards', VIEW_MAP), '');
});

// ---------------------------------------------------------------------------
// Unavailable-vs-zero (FR44, FR121)
// ---------------------------------------------------------------------------

test('readCount returns null for genuinely absent values, never 0 (FR121)', () => {
  assert.equal(readCount(undefined), null);
  assert.equal(readCount(null), null);
  assert.equal(readCount(''), null);
  assert.equal(readCount('not a number'), null);
  assert.equal(readCount(NaN), null);
});

test('readCount preserves an authoritative zero', () => {
  assert.equal(readCount(0), 0);
  assert.equal(readCount('0'), 0);
});

test('readCount clamps negatives and truncates, so a count is never fractional or below zero', () => {
  assert.equal(readCount(-3), 0);
  assert.equal(readCount(4.7), 4);
  assert.equal(readCount('12'), 12);
});

test('formatCount renders unavailable as an em dash and zero as zero', () => {
  assert.equal(formatCount(null), '—');
  assert.equal(formatCount(undefined), '—');
  assert.equal(formatCount(0), '0');
  assert.equal(formatCount(7), '7');
});

// ---------------------------------------------------------------------------
// Collection shaping
// ---------------------------------------------------------------------------

test('isGroupWorkspace recognizes groups regardless of case and padding', () => {
  assert.equal(isGroupWorkspace({ kind: 'group' }), true);
  assert.equal(isGroupWorkspace({ kind: ' Group ' }), true);
  assert.equal(isGroupWorkspace({ kind: 'workspace' }), false);
  assert.equal(isGroupWorkspace({}), false);
  assert.equal(isGroupWorkspace(null), false);
});

test('flattenWorkspaceTree preserves order, depth, parentage, and path', () => {
  const tree = [
    {
      id: 'g1',
      name: 'Group One',
      kind: 'group',
      children: [
        { id: 'w1', name: 'Alpha' },
        { id: 'g2', name: 'Nested', kind: 'group', children: [{ id: 'w2', name: 'Beta' }] }
      ]
    },
    { id: 'w3', name: 'Gamma' }
  ];
  const rows = flattenWorkspaceTree(tree);
  assert.deepEqual(
    rows.map(r => r.id),
    ['g1', 'w1', 'g2', 'w2', 'w3']
  );
  assert.deepEqual(
    rows.map(r => r.depth),
    [0, 1, 1, 2, 0]
  );
  assert.equal(rows.find(r => r.id === 'w2').parent_id, 'g2');
  assert.equal(rows.find(r => r.id === 'w2').path, 'Group One / Nested / Beta');
  // An explicit parent_id on the payload wins over the inferred one.
  assert.equal(rows.find(r => r.id === 'w3').parent_id, '');
});

test('flattenWorkspaceTree tolerates missing, null, and childless nodes', () => {
  assert.deepEqual(flattenWorkspaceTree(null), []);
  assert.deepEqual(flattenWorkspaceTree(undefined), []);
  assert.deepEqual(flattenWorkspaceTree([]), []);
  assert.equal(flattenWorkspaceTree([null, { id: 'a' }, undefined]).length, 1);
  assert.equal(flattenWorkspaceTree([{ id: 'a', children: null }]).length, 1);
});

test('findWorkspace returns null rather than throwing for absent ids', () => {
  const rows = flattenWorkspaceTree([{ id: 'a', name: 'A' }]);
  assert.equal(findWorkspace(rows, 'a').name, 'A');
  assert.equal(findWorkspace(rows, 'missing'), null);
  assert.equal(findWorkspace(rows, ''), null);
  assert.equal(findWorkspace(null, 'a'), null);
});

test('normalizeTags trims, drops blanks, and rejects non-arrays', () => {
  assert.deepEqual(normalizeTags([' one ', '', 'two', null, '   ']), ['one', 'two']);
  assert.deepEqual(normalizeTags(null), []);
  assert.deepEqual(normalizeTags('one'), []);
});

test('buildMapMetadata indexes folder display and tags by id, and previews only groups', () => {
  const tree = [
    {
      id: 'g1',
      name: 'Group',
      kind: 'group',
      children: [
        { id: 'w1', name: 'One', tags: ['x'] },
        { id: 'w2', name: 'Two' },
        { id: 'w3', name: 'Three' },
        { id: 'w4', name: 'Four' }
      ]
    }
  ];
  const meta = buildMapMetadata(flattenWorkspaceTree(tree), tree);
  assert.deepEqual(meta.tagsById.w1, ['x']);
  assert.deepEqual(meta.tagsById.w2, []);
  assert.ok(meta.folderDisplayById.w1);
  // Groups get a bounded preview with an accurate overflow count.
  assert.equal(meta.groupPreviewById.g1.childCount, 4);
  assert.deepEqual(meta.groupPreviewById.g1.previewNames, ['One', 'Two', 'Three']);
  assert.equal(meta.groupPreviewById.g1.overflowCount, 1);
  // A concrete workspace is never given a group preview.
  assert.equal(meta.groupPreviewById.w1, undefined);
});

test('folderDisplayFor distinguishes a linked folder from no folder at all', () => {
  const unlinked = folderDisplayFor({ id: 'w1' });
  assert.equal(unlinked.linked, false);
  assert.match(unlinked.badgeLabel, /No folder/i);

  const linked = folderDisplayFor({
    id: 'w2',
    directories: [{ path: '/Users/me/Ori Workspaces/Alpha', is_primary: true }]
  });
  assert.equal(linked.linked, true);
  assert.equal(linked.detailTitle, '/Users/me/Ori Workspaces/Alpha');
  assert.equal(linked.detail, 'Ori Workspaces/Alpha');
});

// ---------------------------------------------------------------------------
// Signals (FR30, FR63, FR121)
// ---------------------------------------------------------------------------

test('workspaceSignals: attention outranks running, which outranks open tasks', () => {
  assert.equal(
    workspaceSignals({ needs_attention_count: 2, active: true, open_task_count: 5 }).status,
    'attention'
  );
  assert.equal(
    workspaceSignals({ needs_attention_count: 0, active: true, open_task_count: 5 }).status,
    'running'
  );
  assert.equal(
    workspaceSignals({ needs_attention_count: 0, active: false, open_task_count: 5 }).status,
    'active'
  );
  assert.equal(
    workspaceSignals({ needs_attention_count: 0, active: false, open_task_count: 0 }).status,
    'idle'
  );
});

test('workspaceSignals: a payload carrying no signal fields is unknown, not idle (FR121)', () => {
  const signals = workspaceSignals({ id: 'w1', name: 'No data' });
  assert.equal(signals.status, 'unknown');
  assert.equal(signals.label, 'Status unavailable');
  assert.equal(signals.openTasks, null);
  assert.equal(signals.attention, null);
  assert.equal(signals.agents, null);
});

test('workspaceSignals derives an agent count from either the count or the roster', () => {
  assert.equal(workspaceSignals({ agent_count: 3 }).agents, 3);
  assert.equal(workspaceSignals({ agents: [{ name: 'a' }, { name: 'b' }] }).agents, 2);
  assert.equal(workspaceSignals({ agent_count: 0 }).agents, 0);
  assert.equal(workspaceSignals({}).agents, null);
});

// ---------------------------------------------------------------------------
// Recommended next move (FR64, FR65, FR66)
// ---------------------------------------------------------------------------

test('recommendedNextMove is deterministic and attention-first', () => {
  const move = recommendedNextMove({
    needs_attention_count: 3,
    open_task_count: 9,
    active: true
  });
  assert.equal(move.kind, 'attention');
  assert.match(move.label, /3 items needing attention/);
  // Same input, same answer — no randomness, no model call.
  assert.deepEqual(
    recommendedNextMove({ needs_attention_count: 3, open_task_count: 9, active: true }),
    move
  );
});

test('recommendedNextMove singularizes a single attention item', () => {
  assert.match(recommendedNextMove({ needs_attention_count: 1 }).label, /1 item needing attention/);
});

test('recommendedNextMove walks setup, running, schedule, then open tasks', () => {
  assert.equal(
    recommendedNextMove({ needs_attention_count: 0, setup_required: true }).kind,
    'setup'
  );
  assert.equal(recommendedNextMove({ needs_attention_count: 0, active: true }).kind, 'running');
  assert.equal(
    recommendedNextMove({ needs_attention_count: 0, next_scheduled_run: 'in 2 hours' }).kind,
    'scheduled'
  );
  assert.equal(recommendedNextMove({ needs_attention_count: 0, open_task_count: 4 }).kind, 'tasks');
});

test('recommendedNextMove says "No immediate action" honestly when nothing qualifies (FR66)', () => {
  const move = recommendedNextMove({ needs_attention_count: 0, open_task_count: 0 });
  assert.equal(move.kind, 'none');
  assert.equal(move.label, 'No immediate action');
});

test('recommendedNextMove reports unavailable rather than "nothing to do" when data is missing', () => {
  // A workspace that reported NO task/attention data must not be described as
  // quiet — that would be inventing an authoritative value (FR65/FR121).
  const move = recommendedNextMove({ id: 'w1', name: 'No data' });
  assert.equal(move.kind, 'unavailable');
  assert.match(move.label, /unavailable/i);
  assert.notEqual(move.label, 'No immediate action');
});

test('recommendedNextMove never claims an AI produced the recommendation (FR65)', () => {
  const inputs = [
    { needs_attention_count: 2 },
    { setup_required: true },
    { active: true },
    { next_scheduled_run: 'soon' },
    { open_task_count: 1 },
    { open_task_count: 0, needs_attention_count: 0 },
    {}
  ];
  for (const input of inputs) {
    const move = recommendedNextMove(input);
    const text = `${move.label} ${move.detail}`;
    assert.doesNotMatch(text, /\bAI\b|\bOri (thinks|suggests|recommends)|\bsuggests\b/i);
  }
});

// ---------------------------------------------------------------------------
// Rail view model (FR62-FR69)
// ---------------------------------------------------------------------------

test('entryAgentName + agentRoster read only what the payload carries (FR68/FR69)', () => {
  assert.equal(entryAgentName({ entry_agent_name: '  Scout  ' }), 'Scout');
  assert.equal(entryAgentName({}), '');
  assert.deepEqual(agentRoster({ agents: ['Solo'] }), [{ name: 'Solo', role: '', activity: '' }]);
  assert.deepEqual(agentRoster({ agents: [{ name: 'A', role: 'Commander', status: 'idle' }] }), [
    { name: 'A', role: 'Commander', activity: 'idle' }
  ]);
  // Nameless entries are dropped rather than rendered as blank rows.
  assert.deepEqual(agentRoster({ agents: [null, {}, { name: '' }] }), []);
  assert.deepEqual(agentRoster({}), []);
});

test('workspaceRailView offers Ask Commander only with a resolved entry agent (FR68)', () => {
  const withAgent = workspaceRailView({ id: 'w1', name: 'A', entry_agent_name: 'Scout' });
  assert.equal(withAgent.canAskCommander, true);
  assert.equal(withAgent.commander, 'Scout');
  assert.equal(withAgent.commanderUnavailableReason, '');

  const without = workspaceRailView({ id: 'w2', name: 'B' });
  assert.equal(without.canAskCommander, false);
  assert.match(without.commanderUnavailableReason, /no entry agent/i);
});

test('workspaceRailView builds an encoded, workspace-scoped open target (FR67)', () => {
  assert.equal(workspaceRailView({ id: 'w 1', name: 'A' }).openHref, '/workspaces/w%201');
  assert.equal(workspaceRailView({ id: 'a/b', name: 'A' }).openHref, '/workspaces/a%2Fb');
  assert.equal(workspaceRailView({ name: 'No id' }).openHref, '');
});

test('workspaceRailView flags Personal HQ and groups', () => {
  assert.equal(workspaceRailView({ id: 'w', is_personal_hq: true }).isPersonalHQ, true);
  assert.equal(workspaceRailView({ id: 'w', designation: 'personal_hq' }).isPersonalHQ, true);
  assert.equal(workspaceRailView({ id: 'w' }).isPersonalHQ, false);
  assert.equal(workspaceRailView({ id: 'g', kind: 'group' }).kind, RAIL_GROUP);
  assert.equal(workspaceRailView({ id: 'w' }).kind, RAIL_WORKSPACE);
});

test('workspaceRailView falls back to an honest name for an unnamed workspace', () => {
  assert.equal(workspaceRailView({ id: 'w' }).name, 'Untitled workspace');
});

// ---------------------------------------------------------------------------
// Rail rendering
// ---------------------------------------------------------------------------

test('renderWorkspaceRailHTML keeps Open Workspace available even with no next move (FR66)', () => {
  const html = renderWorkspaceRailHTML(
    workspaceRailView({ id: 'w1', name: 'Quiet', open_task_count: 0, needs_attention_count: 0 })
  );
  assert.match(html, /No immediate action/);
  assert.match(html, /data-cockpit-rail-open/);
  assert.match(html, /href="\/workspaces\/w1"/);
});

test('renderWorkspaceRailHTML marks unavailable metrics instead of printing 0 (FR121)', () => {
  const html = renderWorkspaceRailHTML(workspaceRailView({ id: 'w1', name: 'No data' }));
  assert.match(html, /data-unavailable="true"/);
  assert.match(html, /—/);
});

test('renderWorkspaceRailHTML prints an authoritative zero as 0, not as unavailable', () => {
  const html = renderWorkspaceRailHTML(
    workspaceRailView({
      id: 'w1',
      name: 'Empty',
      open_task_count: 0,
      agent_count: 0,
      needs_attention_count: 0
    })
  );
  assert.doesNotMatch(html, /data-unavailable="true"/);
  assert.match(html, />0</);
});

test('renderWorkspaceRailHTML carries status as text, not by color alone (FR131)', () => {
  const html = renderWorkspaceRailHTML(
    workspaceRailView({ id: 'w1', name: 'Busy', needs_attention_count: 2 })
  );
  assert.match(html, /Needs attention/);
});

test('renderWorkspaceRailHTML explains a missing commander rather than offering a dead action', () => {
  const html = renderWorkspaceRailHTML(workspaceRailView({ id: 'w1', name: 'A' }));
  assert.doesNotMatch(html, /data-cockpit-rail-ask/);
  assert.match(html, /no commander to ask/i);
});

test('renderWorkspaceRailHTML escapes hostile workspace content', () => {
  const html = renderWorkspaceRailHTML(
    workspaceRailView({
      id: '"><script>x()</script>',
      name: '<img src=x onerror=alert(1)>',
      description: '</div><script>bad()</script>',
      entry_agent_name: '<b>Scout</b>'
    })
  );
  // What matters is that no attacker-supplied text survives as live markup.
  // The literal string "onerror=" may appear as escaped TEXT, which is inert —
  // asserting on it would test the payload, not the defense. Assert instead
  // that no tag delimiter and no attribute-breaking quote survives unescaped.
  assert.doesNotMatch(html, /<script/i);
  assert.doesNotMatch(html, /<\/script/i);
  assert.doesNotMatch(html, /<img/i);
  assert.doesNotMatch(html, /<b>Scout/i);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.match(html, /&lt;script&gt;bad\(\)&lt;\/script&gt;/);
  // The id lands in both an href and an attribute; neither may break out.
  assert.match(html, /href="\/workspaces\/%22%3E%3Cscript%3E/);
  assert.match(html, /data-workspace-id="&quot;&gt;&lt;script&gt;/);
});

test('renderWorkspaceRailHTML returns empty for a missing view rather than throwing', () => {
  assert.equal(renderWorkspaceRailHTML(null), '');
  assert.equal(renderWorkspaceRailHTML(undefined), '');
});

test('escapeHtml neutralizes every HTML-significant character', () => {
  assert.equal(escapeHtml(`<>&"'`), '&lt;&gt;&amp;&quot;&#39;');
  assert.equal(escapeHtml(null), '');
  assert.equal(escapeHtml(undefined), '');
});

// ---------------------------------------------------------------------------
// Workspace-area states (FR110, FR112-FR114, FR120)
// ---------------------------------------------------------------------------

test('workspaceAreaState distinguishes loading, error, empty, and ready (FR120)', () => {
  assert.equal(workspaceAreaState({ loading: true, workspaces: [] }).state, 'loading');
  assert.equal(workspaceAreaState({ error: new Error('boom'), workspaces: [] }).state, 'error');
  assert.equal(workspaceAreaState({ workspaces: [] }).state, 'empty');
  assert.equal(workspaceAreaState({ workspaces: [{ id: 'a' }] }).state, 'ready');
  // Loading wins over a stale error so a refresh is never shown as broken.
  assert.equal(
    workspaceAreaState({ loading: true, error: new Error('old'), workspaces: [] }).state,
    'loading'
  );
});

test('workspaceAreaState treats a non-array payload as empty, never as ready', () => {
  assert.equal(workspaceAreaState({ workspaces: null }).state, 'empty');
  assert.equal(workspaceAreaState({ workspaces: undefined }).state, 'empty');
});

test('a failed workspace load offers Retry (FR113)', () => {
  const status = workspaceAreaState({ error: new Error('HTTP 500'), workspaces: [] });
  assert.equal(status.canRetry, true);
  const html = renderWorkspaceAreaStatusHTML(status);
  assert.match(html, /data-cockpit-retry/);
  assert.match(html, /Retry/);
  assert.match(html, /HTTP 500/);
});

test('the empty state offers New Workspace and Import Folder, not a bare empty map (FR114)', () => {
  const html = renderWorkspaceAreaStatusHTML(workspaceAreaState({ workspaces: [] }));
  assert.match(html, /New Workspace/);
  assert.match(html, /Import Folder/);
  // Both reuse the existing create/import modal contract (FR105).
  assert.match(html, /data-bs-target="#addFolderModal"/);
  assert.match(html, /data-workspace-import-mode="true"/);
  assert.match(html, /data-workspace-import-mode="false"/);
});

test('the ready state renders no status message at all', () => {
  assert.equal(
    renderWorkspaceAreaStatusHTML(workspaceAreaState({ workspaces: [{ id: 'a' }] })),
    ''
  );
  assert.equal(renderWorkspaceAreaStatusHTML(null), '');
});

test('an error detail is escaped and bounded so a hostile message cannot inject markup', () => {
  const html = renderWorkspaceAreaStatusHTML(
    workspaceAreaState({ error: new Error('<script>x()</script>'), workspaces: [] })
  );
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /&lt;script&gt;/);
});
