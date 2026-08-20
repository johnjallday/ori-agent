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
  RAIL_TODAY,
  RAIL_GROUP,
  RAIL_WORKSPACE,
  RAIL_SUMMARY,
  PANEL_NONE,
  PANEL_UPDATES,
  PANEL_QUESTS,
  PANEL_CAPTURE,
  togglePanelState,
  panelTriggerId,
  updatesBadgeView,
  contextModalShouldShow,
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
  hqSiteVisible,
  workspaceHydrationAllowed,
  renderWorkspaceAreaStatusHTML,
  escapeHtml,
  SIGNAL_ATTENTION,
  SIGNAL_RUNNING,
  SIGNAL_TODAY,
  parseSignal,
  buildScheduleIndex,
  hasWorkTodayFor,
  matchesSignal,
  signalCounts,
  filterWorkspaceIds,
  filterResultMessage,
  renderSignalFiltersHTML,
  rosterState,
  groupAggregates,
  groupRailView,
  groupMapLayoutView,
  renderGroupRailHTML,
  summaryView,
  renderSummaryRailHTML,
  attentionItems,
  scheduledTodayItems,
  renderAttentionSectionHTML,
  renderScheduledSectionHTML,
  validateCapture,
  captureRequestBody,
  captureAvailability,
  askTargetDescription
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

test('workspaceAreaState distinguishes loading, error, authoritative empty, and ready (FR120)', () => {
  assert.equal(workspaceAreaState({ loading: true, workspaces: [] }).state, 'loading');
  assert.equal(workspaceAreaState({ error: new Error('boom'), workspaces: [] }).state, 'error');
  assert.equal(workspaceAreaState({ workspaces: [] }).state, 'empty-map');
  assert.equal(workspaceAreaState({ workspaces: [{ id: 'a' }] }).state, 'ready');
  // Loading wins over a stale error so a refresh is never shown as broken.
  assert.equal(
    workspaceAreaState({ loading: true, error: new Error('old'), workspaces: [] }).state,
    'loading'
  );
});

// --- The unbuilt HQ landmark is drawable content (#322) ---------------------
//
// FR114 keeps a misleading OPERATIONAL map off an empty account. The reserved
// HQ blueprint is not operational data — it is the invitation to build one, and
// in cockpit mode the rail it opens is the ONLY place Build My HQ exists. When
// zero workspaces hid the map outright, a brand-new profile (exactly who
// Mission 01 targets) had no route to the button at all.

test('hqSiteVisible mirrors the map: a status that is not a valid HQ has a site to draw', () => {
  assert.equal(hqSiteVisible({ valid: false }), true, 'never built');
  assert.equal(hqSiteVisible({ valid: false, workspace_id: 'ws-1' }), true, 'built then broken');
  assert.equal(hqSiteVisible({ valid: true, workspace_id: 'ws-1' }), false, 'a working HQ');
  // No status yet is not the same as "no site": there is simply nothing to draw
  // until the request lands, and guessing would flash the wrong state.
  assert.equal(hqSiteVisible(null), false);
  assert.equal(hqSiteVisible(undefined), false);
});

test('workspaceAreaState: authoritative zero workspaces renders the map with or without the HQ site (#320)', () => {
  assert.equal(workspaceAreaState({ workspaces: [], hqSiteVisible: true }).state, 'empty-map');
  assert.equal(workspaceAreaState({ workspaces: [], hqSiteVisible: false }).state, 'empty-map');
  assert.equal(workspaceAreaState({ workspaces: [] }).state, 'empty-map');
  // A populated account is ready either way — the site never makes it emptier.
  assert.equal(
    workspaceAreaState({ workspaces: [{ id: 'a' }], hqSiteVisible: true }).state,
    'ready'
  );
  assert.equal(
    workspaceAreaState({ workspaces: [{ id: 'a' }], hqSiteVisible: false }).state,
    'ready'
  );
});

test('workspaceAreaState: the HQ site never overrides loading, error, or the onboarding gate', () => {
  // Drawing a landmark over an unresolved account would be the same lie FR114
  // exists to prevent — these states outrank it.
  assert.equal(
    workspaceAreaState({ loading: true, workspaces: [], hqSiteVisible: true }).state,
    'loading'
  );
  assert.equal(
    workspaceAreaState({ error: new Error('boom'), workspaces: [], hqSiteVisible: true }).state,
    'error'
  );
  assert.equal(
    workspaceAreaState({
      workspaces: [],
      hqSiteVisible: true,
      onboardingGate: { state: 'required', message: 'Finish setup' }
    }).state,
    'onboarding-required'
  );
});

test('workspaceAreaState keeps workspace affordances hidden while onboarding is pending', () => {
  const status = workspaceAreaState({
    loading: false,
    workspaces: [{ id: 'foreign', name: 'Foreign Workspace' }],
    onboardingGate: {
      state: 'required',
      allowWorkspaceHydration: false,
      message: 'Finish setup to load your workspaces.'
    }
  });
  assert.equal(status.state, 'onboarding-required');
  const html = renderWorkspaceAreaStatusHTML(status);
  assert.match(html, /Finish setup/);
  assert.doesNotMatch(html, /Foreign Workspace|New Workspace|Import Folder/);
});

test('workspaceHydrationAllowed fails closed unless the gate is explicitly ready', () => {
  assert.equal(workspaceHydrationAllowed(null), false);
  assert.equal(workspaceHydrationAllowed({ state: 'loading' }), false);
  assert.equal(
    workspaceHydrationAllowed({ state: 'required', allowWorkspaceHydration: false }),
    false
  );
  assert.equal(
    workspaceHydrationAllowed({ state: 'unavailable', allowWorkspaceHydration: false }),
    false
  );
  assert.equal(
    workspaceHydrationAllowed({ state: 'ready', allowWorkspaceHydration: false }),
    false
  );
  assert.equal(workspaceHydrationAllowed({ state: 'ready', allowWorkspaceHydration: true }), true);
});

test('a queued realtime refresh is blocked if consent is no longer ready when it runs', () => {
  const gate = { state: 'ready', allowWorkspaceHydration: true };
  assert.equal(workspaceHydrationAllowed(gate), true);

  // scheduleRealtimeRefresh checks once before queuing; refreshQuietly checks
  // this same predicate again inside the delayed callback.
  gate.state = 'required';
  gate.allowWorkspaceHydration = false;
  assert.equal(workspaceHydrationAllowed(gate), false);
});

test('workspaceAreaState makes an unknown onboarding status retryable without exposing data', () => {
  const status = workspaceAreaState({
    loading: false,
    workspaces: [{ id: 'foreign', name: 'Foreign Workspace' }],
    onboardingGate: {
      state: 'unavailable',
      allowWorkspaceHydration: false,
      message: 'Ori could not confirm onboarding status.'
    }
  });
  assert.equal(status.state, 'onboarding-unavailable');
  assert.equal(status.canRetry, true);
  const html = renderWorkspaceAreaStatusHTML(status);
  assert.match(html, /data-cockpit-onboarding-retry/);
  assert.match(html, /remains hidden/i);
  assert.doesNotMatch(html, /Foreign Workspace|New Workspace|Import Folder/);
});

test('workspaceAreaState treats a non-array payload as a retryable failure, never authoritative empty', () => {
  for (const workspaces of [null, undefined, {}]) {
    const status = workspaceAreaState({ workspaces });
    assert.equal(status.state, 'error');
    assert.equal(status.canRetry, true);
    assert.match(renderWorkspaceAreaStatusHTML(status), /data-cockpit-retry/);
  }
});

test('a failed workspace load offers Retry (FR113)', () => {
  const status = workspaceAreaState({ error: new Error('HTTP 500'), workspaces: [] });
  assert.equal(status.canRetry, true);
  const html = renderWorkspaceAreaStatusHTML(status);
  assert.match(html, /data-cockpit-retry/);
  assert.match(html, /Retry/);
  assert.match(html, /HTTP 500/);
});

test('the authoritative empty state leaves the status host clear for the real Map (#320)', () => {
  const status = workspaceAreaState({ workspaces: [] });
  assert.equal(status.state, 'empty-map');
  assert.equal(renderWorkspaceAreaStatusHTML(status), '');
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

// ===========================================================================
// Group 2 — signal filters, group context, and Summary
// ===========================================================================

// ---------------------------------------------------------------------------
// Signal filters (FR31-FR34)
// ---------------------------------------------------------------------------

test('parseSignal accepts only the three real signals', () => {
  assert.equal(parseSignal('attention'), SIGNAL_ATTENTION);
  assert.equal(parseSignal('RUNNING'), SIGNAL_RUNNING);
  assert.equal(parseSignal(' today '), SIGNAL_TODAY);
  assert.equal(parseSignal('cards'), '');
  assert.equal(parseSignal(''), '');
  assert.equal(parseSignal(null), '');
});

test('buildScheduleIndex returns null for an unavailable source, {} for an empty one', () => {
  // The distinction matters: null means "we do not know", {} means "nothing
  // is scheduled" (FR120/FR121).
  assert.equal(buildScheduleIndex(null), null);
  assert.equal(buildScheduleIndex(undefined), null);
  const empty = buildScheduleIndex([]);
  assert.notEqual(empty, null);
  assert.equal(Object.keys(empty).length, 0);
});

test('buildScheduleIndex groups rows by workspace and drops rows with no workspace', () => {
  const index = buildScheduleIndex([
    { workspace_id: 'a', next_run: '2026-07-31T10:00:00Z' },
    { workspace_id: 'a', next_run: '2026-07-31T18:00:00Z' },
    { workspace_id: 'b', next_run: '2026-08-05T10:00:00Z' },
    { next_run: '2026-08-05T10:00:00Z' },
    null
  ]);
  assert.equal(index.a.length, 2);
  assert.equal(index.b.length, 1);
  assert.equal(Object.keys(index).length, 2);
});

test('hasWorkTodayFor is null when the schedule source is unavailable', () => {
  assert.equal(hasWorkTodayFor('a', null), null);
});

test('hasWorkTodayFor counts work due by end of the local day, not a 24h window', () => {
  const now = new Date('2026-07-31T09:00:00');
  const laterToday = new Date('2026-07-31T22:00:00');
  const tomorrowMorning = new Date('2026-08-01T08:00:00');
  const index = {
    a: [{ next_run: laterToday.toISOString() }],
    b: [{ next_run: tomorrowMorning.toISOString() }]
  };
  assert.equal(hasWorkTodayFor('a', index, now), true);
  // Tomorrow morning is within 24h but is NOT today.
  assert.equal(hasWorkTodayFor('b', index, now), false);
  assert.equal(hasWorkTodayFor('missing', index, now), false);
});

test('hasWorkTodayFor treats overdue work as due today', () => {
  const now = new Date('2026-07-31T09:00:00');
  const index = { a: [{ next_run: new Date('2026-07-30T09:00:00').toISOString() }] };
  assert.equal(hasWorkTodayFor('a', index, now), true);
});

test('hasWorkTodayFor ignores unparseable timestamps rather than throwing', () => {
  const index = { a: [{ next_run: 'not a date' }, {}] };
  assert.equal(hasWorkTodayFor('a', index, new Date('2026-07-31T09:00:00')), false);
});

test('matchesSignal returns null when the workspace cannot be classified (FR121)', () => {
  // No attention field at all -> unknowable, not "does not match".
  assert.equal(matchesSignal({ id: 'a' }, SIGNAL_ATTENTION, {}), null);
  assert.equal(matchesSignal({ id: 'a' }, SIGNAL_RUNNING, {}), null);
  assert.equal(matchesSignal({ id: 'a' }, SIGNAL_TODAY, null), null);
});

test('matchesSignal classifies attention and running from real fields', () => {
  assert.equal(matchesSignal({ id: 'a', needs_attention_count: 2 }, SIGNAL_ATTENTION, {}), true);
  assert.equal(matchesSignal({ id: 'a', needs_attention_count: 0 }, SIGNAL_ATTENTION, {}), false);
  assert.equal(matchesSignal({ id: 'a', active: true }, SIGNAL_RUNNING, {}), true);
  assert.equal(matchesSignal({ id: 'a', active: false }, SIGNAL_RUNNING, {}), false);
});

test('matchesSignal never matches a group — a group does not run work (FR71)', () => {
  const group = { id: 'g', kind: 'group', needs_attention_count: 5, active: true };
  assert.equal(matchesSignal(group, SIGNAL_ATTENTION, {}), false);
  assert.equal(matchesSignal(group, SIGNAL_RUNNING, {}), false);
});

test('matchesSignal with no filter matches everything', () => {
  assert.equal(matchesSignal({ id: 'a' }, '', {}), true);
  assert.equal(matchesSignal({ id: 'g', kind: 'group' }, '', {}), true);
});

test('signalCounts derives counts from the same state, excluding groups (FR32)', () => {
  const rows = [
    { id: 'a', needs_attention_count: 2, active: false },
    { id: 'b', needs_attention_count: 0, active: true },
    { id: 'c', needs_attention_count: 0, active: false },
    { id: 'g', kind: 'group', needs_attention_count: 9, active: true }
  ];
  const counts = signalCounts(rows, {});
  assert.equal(counts[SIGNAL_ATTENTION], 1);
  assert.equal(counts[SIGNAL_RUNNING], 1);
  assert.equal(counts[SIGNAL_TODAY], 0);
});

test('signalCounts reports null — never 0 — when the source is unavailable (FR32/FR121)', () => {
  const rows = [{ id: 'a', needs_attention_count: 0 }];
  // Schedule source down entirely.
  assert.equal(signalCounts(rows, null)[SIGNAL_TODAY], null);
  // No workspace reported attention/active at all.
  const blind = signalCounts([{ id: 'a' }, { id: 'b' }], null);
  assert.equal(blind[SIGNAL_ATTENTION], null);
  assert.equal(blind[SIGNAL_RUNNING], null);
});

test('signalCounts on an empty workspace list is an authoritative 0, not unknown', () => {
  const counts = signalCounts([], {});
  assert.equal(counts[SIGNAL_ATTENTION], 0);
  assert.equal(counts[SIGNAL_RUNNING], 0);
});

test('filterWorkspaceIds returns null with no filter and only true matches otherwise', () => {
  const rows = [
    { id: 'a', needs_attention_count: 1 },
    { id: 'b', needs_attention_count: 0 },
    { id: 'c' } // unclassifiable
  ];
  assert.equal(filterWorkspaceIds(rows, '', {}), null);
  assert.deepEqual(filterWorkspaceIds(rows, SIGNAL_ATTENTION, {}), ['a']);
});

test('filterResultMessage is honest about an unavailable filter source', () => {
  assert.match(filterResultMessage('', null), /Filter cleared/);
  assert.match(filterResultMessage(SIGNAL_ATTENTION, 1), /1 workspace matches/);
  assert.match(filterResultMessage(SIGNAL_ATTENTION, 4), /4 workspaces match/);
  assert.match(filterResultMessage(SIGNAL_TODAY, null), /unavailable/i);
  assert.doesNotMatch(filterResultMessage(SIGNAL_TODAY, null), /0 workspaces/);
});

test('renderSignalFiltersHTML marks the active chip and flags unavailable counts', () => {
  const html = renderSignalFiltersHTML({ attention: 2, running: 0, today: null }, SIGNAL_ATTENTION);
  assert.match(html, /data-cockpit-signal="attention"[^>]*aria-pressed="true"/);
  assert.match(html, /data-cockpit-signal="running"[^>]*aria-pressed="false"/);
  assert.match(html, /data-unavailable="true"/);
  assert.match(html, /—/);
});

// ---------------------------------------------------------------------------
// Roster honesty (FR69, FR121)
// ---------------------------------------------------------------------------

test('rosterState distinguishes listed, count-only, empty, and unavailable', () => {
  assert.equal(rosterState({ agents: [{ name: 'A' }], agent_count: 1 }).state, 'listed');
  // The list payload carries agent_count but no agents array: saying "no
  // agents" would contradict the count rendered beside it.
  assert.equal(rosterState({ agent_count: 3 }).state, 'count-only');
  assert.equal(rosterState({ agent_count: 0 }).state, 'empty');
  assert.equal(rosterState({}).state, 'unavailable');
});

test('the rail never claims "no agents" for a workspace that reports agents', () => {
  const html = renderWorkspaceRailHTML(workspaceRailView({ id: 'w', name: 'W', agent_count: 3 }));
  assert.doesNotMatch(html, /No agents attached yet/);
  assert.match(html, /3 agents attached/);
});

test('the rail says agent data is unavailable when the payload carries none', () => {
  const html = renderWorkspaceRailHTML(workspaceRailView({ id: 'w', name: 'W' }));
  assert.match(html, /Agent data unavailable/);
});

test('the rail reports an authoritative zero roster plainly', () => {
  const html = renderWorkspaceRailHTML(workspaceRailView({ id: 'w', name: 'W', agent_count: 0 }));
  assert.match(html, /No agents attached yet/);
});

// ---------------------------------------------------------------------------
// Group aggregates and rail (FR70, FR71)
// ---------------------------------------------------------------------------

function groupFixture() {
  return flattenWorkspaceTree([
    {
      id: 'g1',
      name: 'Platform',
      kind: 'group',
      description: 'Everything platform.',
      children: [
        { id: 'w1', name: 'API', open_task_count: 3, agent_count: 2, needs_attention_count: 1 },
        { id: 'w2', name: 'Web', open_task_count: 1, agent_count: 1, needs_attention_count: 0 },
        {
          id: 'g2',
          name: 'Infra',
          kind: 'group',
          children: [
            { id: 'w3', name: 'DB', open_task_count: 5, agent_count: 4, needs_attention_count: 2 }
          ]
        }
      ]
    },
    { id: 'w4', name: 'Outside', open_task_count: 99, agent_count: 99 }
  ]);
}

test('groupAggregates sums the whole subtree and excludes workspaces outside it', () => {
  const rows = groupFixture();
  const a = groupAggregates({ id: 'g1' }, rows);
  assert.equal(a.childCount, 3); // w1, w2, g2
  assert.equal(a.descendantWorkspaces, 3); // w1, w2, w3
  assert.equal(a.descendantGroups, 1); // g2
  assert.equal(a.openTasks.total, 9); // 3 + 1 + 5, NOT 108
  assert.equal(a.agents.total, 7);
  assert.equal(a.attention.total, 3);
});

test('groupAggregates reports null totals when NO descendant reported a metric', () => {
  const rows = flattenWorkspaceTree([
    { id: 'g', name: 'G', kind: 'group', children: [{ id: 'w', name: 'W' }] }
  ]);
  const a = groupAggregates({ id: 'g' }, rows);
  assert.equal(a.openTasks.total, null);
  assert.equal(a.agents.total, null);
});

test('groupAggregates counts partially-reported metrics and says how many are missing', () => {
  const rows = flattenWorkspaceTree([
    {
      id: 'g',
      name: 'G',
      kind: 'group',
      children: [
        { id: 'w1', name: 'A', open_task_count: 4 },
        { id: 'w2', name: 'B' }
      ]
    }
  ]);
  const a = groupAggregates({ id: 'g' }, rows);
  assert.equal(a.openTasks.total, 4);
  assert.equal(a.openTasks.missing, 1);
});

test('an empty group aggregates to an authoritative zero, not to unknown', () => {
  // No workspaces inside means genuinely no tasks/agents/attention. Rendering
  // "—" here would claim we could not find out, which is false.
  const rows = flattenWorkspaceTree([{ id: 'g', name: 'G', kind: 'group', children: [] }]);
  const a = groupAggregates({ id: 'g' }, rows);
  assert.equal(a.childCount, 0);
  assert.equal(a.descendantWorkspaces, 0);
  assert.equal(a.openTasks.total, 0);
  assert.equal(a.agents.total, 0);
  assert.equal(a.attention.total, 0);

  const html = renderGroupRailHTML(groupRailView(findWorkspace(rows, 'g'), rows));
  assert.doesNotMatch(html, /data-unavailable="true"/);
});

test('the group rail offers Open Group and never a next move or Ask Commander (FR71)', () => {
  const rows = groupFixture();
  const html = renderGroupRailHTML(groupRailView(findWorkspace(rows, 'g1'), rows));
  assert.match(html, /Open Group/);
  assert.match(html, /data-rail-panel="group"/);
  assert.doesNotMatch(html, /Next move/);
  assert.doesNotMatch(html, /data-cockpit-rail-ask/);
  // It must not read as an execution workspace.
  assert.match(html, /does not run work itself/);
});

test('the group rail shows a missing-data note rather than a fake zero', () => {
  const rows = flattenWorkspaceTree([
    { id: 'g', name: 'G', kind: 'group', children: [{ id: 'w', name: 'W' }] }
  ]);
  const html = renderGroupRailHTML(groupRailView(findWorkspace(rows, 'g'), rows));
  assert.match(html, /data-unavailable="true"/);
});

test('the group rail escapes hostile group content', () => {
  const rows = flattenWorkspaceTree([
    {
      id: '<x>',
      name: '<img src=x>',
      kind: 'group',
      description: '<script>y()</script>',
      children: []
    }
  ]);
  const html = renderGroupRailHTML(groupRailView(findWorkspace(rows, '<x>'), rows));
  assert.doesNotMatch(html, /<script/i);
  assert.doesNotMatch(html, /<img/i);
});

// ---------------------------------------------------------------------------
// The rail's Map layout section (#346 FR-151 – FR-155)
// ---------------------------------------------------------------------------

const AUTO_DISTRICT = {
  id: 'g1',
  sizingMode: 'auto',
  collapsed: false,
  accent: 'default',
  theme: 'default',
  memberCount: 2,
  readOnly: false
};

test('the Map layout section states the sizing mode in words (#346 FR-152)', () => {
  const auto = groupMapLayoutView(AUTO_DISTRICT, { view: VIEW_MAP });
  assert.equal(auto.sizingMode, 'auto');
  assert.match(auto.sizingLabel, /Automatic size/);

  const custom = groupMapLayoutView({ ...AUTO_DISTRICT, sizingMode: 'custom' }, { view: VIEW_MAP });
  assert.equal(custom.sizingMode, 'custom');
  assert.match(custom.sizingLabel, /Custom size/);
  // Not a chip alone: the words appear in the rendered rail too.
  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, {
      view: VIEW_MAP,
      district: { ...AUTO_DISTRICT, sizingMode: 'custom' }
    })
  );
  assert.match(html, /Custom size/);
});

test('Fit to contents is offered only when there is a custom size to discard (#346 FR-148)', () => {
  assert.equal(groupMapLayoutView(AUTO_DISTRICT, { view: VIEW_MAP }).canFit, false);
  assert.equal(
    groupMapLayoutView({ ...AUTO_DISTRICT, sizingMode: 'custom' }, { view: VIEW_MAP }).canFit,
    true
  );
});

test('a read-only or collapsed district disables layout actions truthfully (#346 FR-148, FR-115)', () => {
  const readOnly = groupMapLayoutView({ ...AUTO_DISTRICT, readOnly: true }, { view: VIEW_MAP });
  assert.equal(readOnly.canResize, false);
  assert.equal(readOnly.canFit, false);
  assert.match(readOnly.readOnlyNote, /cannot be saved/);

  const collapsed = groupMapLayoutView(
    { ...AUTO_DISTRICT, collapsed: true, sizingMode: 'custom' },
    { view: VIEW_MAP }
  );
  assert.equal(collapsed.canResize, false, 'a collapsed district has no frame to size');
  assert.equal(collapsed.canFit, false);
});

test('Tree hides the Map-only layout controls rather than showing them dead (#346 FR-154)', () => {
  assert.equal(groupMapLayoutView(AUTO_DISTRICT, { view: VIEW_TREE }), null);

  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, { view: VIEW_TREE, district: AUTO_DISTRICT })
  );
  assert.doesNotMatch(html, /data-rail-map-layout/);
  assert.doesNotMatch(html, /Resize group/);
  // Open Group is untouched by any of this (FR-155).
  assert.match(html, /Open Group/);
});

test('the rail offers Collapse, then Expand, with matching aria-expanded (#346 FR-102, FR-110)', () => {
  const open = groupMapLayoutView(AUTO_DISTRICT, { view: VIEW_MAP });
  assert.equal(open.collapsed, false);
  assert.equal(open.collapseLabel, 'Collapse group');

  const shut = groupMapLayoutView({ ...AUTO_DISTRICT, collapsed: true }, { view: VIEW_MAP });
  assert.equal(shut.collapseLabel, 'Expand group');

  const rows = groupFixture();
  const html = collapsed =>
    renderGroupRailHTML(
      groupRailView(findWorkspace(rows, 'g1'), rows, {
        view: VIEW_MAP,
        district: { ...AUTO_DISTRICT, collapsed }
      })
    );
  assert.match(html(false), /data-cockpit-group-collapse aria-expanded="true"/);
  assert.match(html(false), />Collapse group</);
  assert.match(html(true), /data-cockpit-group-collapse aria-expanded="false"/);
  assert.match(html(true), />Expand group</);
});

test('a read-only map disables collapse along with the rest (#346 FR-148)', () => {
  const readOnly = groupMapLayoutView({ ...AUTO_DISTRICT, readOnly: true }, { view: VIEW_MAP });
  assert.equal(readOnly.canCollapse, false);
});

const CATALOGS = {
  accents: [
    { id: 'default', label: 'Ori green' },
    { id: 'moss', label: 'Moss green' }
  ],
  themes: [
    { id: 'default', label: 'Standard district', hint: 'Dashed outline' },
    { id: 'blueprint', label: 'Blueprint', hint: 'Solid rule with a grid hatch' }
  ]
};

test('the rail offers named accent and theme choices, not bare swatches (#346 FR-130)', () => {
  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, {
      view: VIEW_MAP,
      district: { ...AUTO_DISTRICT, ...CATALOGS }
    })
  );
  assert.match(html, /data-rail-appearance="accent"/);
  assert.match(html, /data-rail-appearance="theme"/);
  assert.match(html, />Moss green</, 'every option carries its human name');
  assert.match(html, />Blueprint</);
  assert.match(html, /Solid rule with a grid hatch/, 'and a theme says how it differs in shape');
  // The current choice is checked, so the rail states it rather than implying it.
  assert.match(html, /value="default"[^>]*checked/);
});

test('appearance choices are identifiers from the catalog only (#346 FR-125)', () => {
  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, {
      view: VIEW_MAP,
      district: {
        ...AUTO_DISTRICT,
        accents: [{ id: 'moss', label: '<img src=x onerror=alert(1)>' }],
        themes: []
      }
    })
  );
  assert.ok(!html.includes('<img'), 'a hostile label is escaped, never rendered');
  assert.match(html, /value="moss"/);
});

test('Use default appearance appears only when there is something to reset (#346 FR-137)', () => {
  const rows = groupFixture();
  const html = district =>
    renderGroupRailHTML(
      groupRailView(findWorkspace(rows, 'g1'), rows, { view: VIEW_MAP, district })
    );
  assert.doesNotMatch(html({ ...AUTO_DISTRICT, ...CATALOGS }), /appearance-reset/);
  assert.match(
    html({ ...AUTO_DISTRICT, ...CATALOGS, accent: 'moss' }),
    /data-cockpit-group-appearance-reset/
  );
  assert.match(
    html({ ...AUTO_DISTRICT, ...CATALOGS, theme: 'blueprint' }),
    /data-cockpit-group-appearance-reset/
  );
});

test('a read-only map disables appearance choices too (#346 FR-148)', () => {
  const view = groupMapLayoutView(
    { ...AUTO_DISTRICT, ...CATALOGS, readOnly: true },
    { view: VIEW_MAP }
  );
  assert.equal(view.canChangeAppearance, false);
  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, {
      view: VIEW_MAP,
      district: { ...AUTO_DISTRICT, ...CATALOGS, readOnly: true }
    })
  );
  assert.match(html, /data-cockpit-group-accent disabled|disabled data-cockpit-group-accent/);
});

test('a group with no district drawn shows no Map layout section (#346 FR-151)', () => {
  assert.equal(groupMapLayoutView(null, { view: VIEW_MAP }), null);
  const rows = groupFixture();
  const html = renderGroupRailHTML(groupRailView(findWorkspace(rows, 'g1'), rows));
  assert.doesNotMatch(html, /data-rail-map-layout/);
});

test('the Map layout section says it changes presentation, not membership (#346 FR-153)', () => {
  const rows = groupFixture();
  const html = renderGroupRailHTML(
    groupRailView(findWorkspace(rows, 'g1'), rows, { view: VIEW_MAP, district: AUTO_DISTRICT })
  );
  assert.match(html, /data-rail-map-layout/);
  assert.match(html, /never change which workspaces are in it/);
  assert.match(html, /data-cockpit-group-resize/);
  assert.match(html, /data-cockpit-group-fit/);
  // Open Group still leads the panel (FR-155).
  assert.ok(html.indexOf('Open Group') < html.indexOf('Map layout'));
});

// ---------------------------------------------------------------------------
// Summary (FR89, FR90)
// ---------------------------------------------------------------------------

test('summaryView totals come from the same shared state, groups counted separately', () => {
  const rows = groupFixture();
  const view = summaryView(rows, {});
  assert.equal(view.workspaces, 4); // w1..w4
  assert.equal(view.groups, 2); // g1, g2
  assert.equal(view.openTasks, 108); // 3 + 1 + 5 + 99
  assert.equal(view.agents, 106);
  assert.equal(view.attention, 3);
});

test('summaryView lists the running workspaces and counts attention without listing it', () => {
  const rows = flattenWorkspaceTree([
    { id: 'a', name: 'Alpha', needs_attention_count: 2 },
    { id: 'b', name: 'Beta', active: true },
    { id: 'c', name: 'Gamma' }
  ]);
  const view = summaryView(rows, {});
  assert.deepEqual(
    view.runningWorkspaces.map(w => w.name),
    ['Beta']
  );
  // Attention is a Summary metric, not a Summary list — Today owns the list, so
  // the view model must not carry a second copy of it.
  assert.equal(view.attention, 2);
  assert.equal('attentionWorkspaces' in view, false);
});

test('summaryView reports schedule figures as unavailable when the source is down', () => {
  const view = summaryView(groupFixture(), null);
  assert.equal(view.upcomingCount, null);
  assert.equal(view.dueToday, null);
});

test('summaryView counts upcoming runs when the schedule source is present', () => {
  const now = new Date('2026-07-31T09:00:00');
  const index = {
    w1: [{ next_run: new Date('2026-07-31T12:00:00').toISOString() }],
    w4: [
      { next_run: new Date('2026-08-09T12:00:00').toISOString() },
      { next_run: new Date('2026-08-10T12:00:00').toISOString() }
    ]
  };
  const view = summaryView(groupFixture(), index, now);
  assert.equal(view.upcomingCount, 3);
  assert.equal(view.dueToday, 1);
});

test('renderSummaryRailHTML shows totals, a Back action, and honest empty lists', () => {
  const html = renderSummaryRailHTML(summaryView(groupFixture(), null));
  assert.match(html, /data-rail-panel="summary"/);
  assert.match(html, /data-cockpit-rail-back/);
  assert.match(html, /Workspaces/);
  assert.match(html, /Scheduled-work data is unavailable/);
  // Due-today has no source, so it must render unavailable rather than 0.
  assert.match(html, /data-unavailable="true"/);
});

test('the Summary running list links back into selection, not to a URL', () => {
  const rows = flattenWorkspaceTree([{ id: 'a', name: 'Alpha', active: true }]);
  const html = renderSummaryRailHTML(summaryView(rows, {}));
  assert.match(html, /data-cockpit-summary-select="a"/);
});

test('renderSummaryRailHTML does not repeat the attention list Today already shows', () => {
  const rows = flattenWorkspaceTree([{ id: 'a', name: 'Alpha', needs_attention_count: 1 }]);
  const html = renderSummaryRailHTML(summaryView(rows, {}));
  // The Attention metric stays; the duplicate roster does not.
  assert.match(html, /Attention/);
  assert.equal(/data-cockpit-summary-select="a"/.test(html), false);
});

test('renderSummaryRailHTML caps long lists and says how many more there are', () => {
  const rows = flattenWorkspaceTree(
    Array.from({ length: 8 }, (_, i) => ({
      id: `w${i}`,
      name: `W${i}`,
      active: true
    }))
  );
  const html = renderSummaryRailHTML(summaryView(rows, {}));
  assert.match(html, /\+3 more/);
});

test('folderDisplayFor reads the real directory_references wire field', () => {
  const linked = folderDisplayFor({
    directory_references: [{ path: '/Users/me/Ori Workspaces/Alpha', is_primary: true }]
  });
  assert.equal(linked.linked, true);
  assert.equal(linked.detail, 'Ori Workspaces/Alpha');
});

// ===========================================================================
// Group 4 — Today's immediate work and Quick Capture
// ===========================================================================

test('attentionItems lists only workspaces with attention, most urgent first', () => {
  const rows = flattenWorkspaceTree([
    { id: 'a', name: 'Alpha', needs_attention_count: 1 },
    { id: 'b', name: 'Beta', needs_attention_count: 5 },
    { id: 'c', name: 'Gamma', needs_attention_count: 0 },
    { id: 'd', name: 'Delta' },
    { id: 'g', name: 'Group', kind: 'group', needs_attention_count: 9 }
  ]);
  assert.deepEqual(
    attentionItems(rows).map(i => i.name),
    ['Beta', 'Alpha']
  );
  assert.equal(attentionItems(rows)[0].count, 5);
});

test('attentionItems is empty rather than throwing on missing input', () => {
  assert.deepEqual(attentionItems(null), []);
  assert.deepEqual(attentionItems([]), []);
});

test('scheduledTodayItems is null when the schedule source is unavailable (FR121)', () => {
  const rows = flattenWorkspaceTree([{ id: 'a', name: 'Alpha' }]);
  assert.equal(scheduledTodayItems(rows, null), null);
});

test('scheduledTodayItems returns only work due by end of day, earliest first', () => {
  const now = new Date('2026-07-31T09:00:00');
  const rows = flattenWorkspaceTree([
    { id: 'a', name: 'Alpha' },
    { id: 'b', name: 'Beta' }
  ]);
  const index = {
    a: [
      { next_run: new Date('2026-07-31T18:00:00').toISOString(), task_name: 'Evening' },
      { next_run: new Date('2026-08-02T09:00:00').toISOString(), task_name: 'Later this week' }
    ],
    b: [{ next_run: new Date('2026-07-31T11:00:00').toISOString(), task_name: 'Late morning' }]
  };
  const items = scheduledTodayItems(rows, index, now);
  assert.deepEqual(
    items.map(i => i.taskName),
    ['Late morning', 'Evening']
  );
  assert.equal(items[0].workspaceName, 'Beta');
});

test('scheduledTodayItems flags overdue work rather than hiding it', () => {
  const now = new Date('2026-07-31T09:00:00');
  const rows = flattenWorkspaceTree([{ id: 'a', name: 'Alpha' }]);
  const items = scheduledTodayItems(
    rows,
    { a: [{ next_run: new Date('2026-07-31T07:00:00').toISOString(), task_name: 'Missed' }] },
    now
  );
  assert.equal(items.length, 1);
  assert.equal(items[0].overdue, true);
});

test('renderAttentionSectionHTML selects rather than navigates, and caps the list', () => {
  const items = Array.from({ length: 9 }, (_, i) => ({
    id: `w${i}`,
    name: `W${i}`,
    count: 9 - i
  }));
  const html = renderAttentionSectionHTML(items);
  assert.match(html, /data-cockpit-select="w0"/);
  assert.doesNotMatch(html, /<a /);
  assert.match(html, /\+3 more/);
});

test('renderAttentionSectionHTML renders nothing when nothing needs attention', () => {
  assert.equal(renderAttentionSectionHTML([]), '');
});

test('the scheduled section distinguishes "unavailable" from "nothing today"', () => {
  assert.match(renderScheduledSectionHTML(null), /unavailable/i);
  // Genuinely nothing scheduled renders no section at all rather than a
  // misleading "unavailable" note.
  assert.equal(renderScheduledSectionHTML([]), '');
});

test('the scheduled section names overdue work explicitly, not only by colour', () => {
  const html = renderScheduledSectionHTML([
    {
      workspaceId: 'a',
      workspaceName: 'Alpha',
      taskName: 'Nightly',
      at: new Date('2026-07-31T07:00:00'),
      overdue: true
    }
  ]);
  assert.match(html, /overdue/);
  assert.match(html, /is-overdue/);
});

test('Today sections escape hostile workspace and task names', () => {
  const attention = renderAttentionSectionHTML([
    { id: '<x>', name: '<img src=x onerror=y>', count: 1 }
  ]);
  assert.doesNotMatch(attention, /<img/i);
  const scheduled = renderScheduledSectionHTML([
    {
      workspaceId: '<x>',
      workspaceName: '<script>a()</script>',
      taskName: '<script>b()</script>',
      at: new Date('2026-07-31T09:00:00'),
      overdue: false
    }
  ]);
  assert.doesNotMatch(scheduled, /<script/i);
});

// --- Quick Capture ---------------------------------------------------------

test('validateCapture requires a title and allows optional details (FR103)', () => {
  assert.equal(validateCapture({ title: '' }).ok, false);
  assert.equal(validateCapture({ title: '   ' }).ok, false);
  assert.match(validateCapture({}).message, /Add a title/);
  const ok = validateCapture({ title: '  Ship it  ', details: '  later  ' });
  assert.equal(ok.ok, true);
  assert.equal(ok.title, 'Ship it');
  assert.equal(ok.details, 'later');
});

test('captureRequestBody uses the EXISTING backlog contract, not a new inbox (FR102)', () => {
  const body = captureRequestBody('hq-1', { title: 'Idea', details: 'more' });
  assert.deepEqual(body, {
    workspace_id: 'hq-1',
    description: 'Idea',
    details: 'more',
    source_type: 'home_quick_capture'
  });
});

test('captureAvailability requires a VALID Personal HQ with a workspace id (FR104)', () => {
  assert.equal(captureAvailability({ valid: true, workspace_id: 'hq-1' }).canSave, true);
  assert.equal(captureAvailability({ valid: true, workspace_id: 'hq-1' }).hqWorkspaceId, 'hq-1');
  // A "valid" flag with no workspace is not usable.
  assert.equal(captureAvailability({ valid: true }).canSave, false);
  assert.equal(captureAvailability({ valid: false, workspace_id: 'hq-1' }).canSave, false);
  assert.equal(captureAvailability(null).canSave, false);
});

test('captureAvailability explains the requirement rather than failing silently', () => {
  const availability = captureAvailability(null);
  assert.match(availability.message, /Personal HQ/);
  assert.notEqual(availability.message, '');
});

// ===========================================================================
// Group 5 — Ask Ori in the context rail
// ===========================================================================

test('askTargetDescription never presents a recommendation as routed work (FR97)', () => {
  const recommended = askTargetDescription({ recommended: { name: 'Alpha' } });
  assert.equal(recommended.state, 'recommended');
  assert.match(recommended.text, /Suggested/);
  // The crux: a suggestion must say nothing has been sent.
  assert.match(recommended.text, /Nothing has been sent yet/);
  assert.doesNotMatch(recommended.text, /Working in/);
});

test('askTargetDescription reports routed work distinctly from a suggestion', () => {
  const routed = askTargetDescription({ routed: { name: 'Alpha' } });
  assert.equal(routed.state, 'routed');
  assert.match(routed.text, /Working in Alpha/);
  assert.doesNotMatch(routed.text, /Suggested/);
});

test('askTargetDescription describes a selected workspace as context, not a destination', () => {
  const selected = askTargetDescription({ selected: { name: 'Alpha' } });
  assert.equal(selected.state, 'selected');
  assert.match(selected.text, /offered as context/);
  assert.doesNotMatch(selected.text, /Working in/);
});

test('askTargetDescription prefers routed over recommended over selected', () => {
  const all = askTargetDescription({
    selected: { name: 'Sel' },
    recommended: { name: 'Rec' },
    routed: { name: 'Routed' }
  });
  assert.equal(all.state, 'routed');
  assert.match(all.text, /Routed/);

  const noRoute = askTargetDescription({
    selected: { name: 'Sel' },
    recommended: { name: 'Rec' }
  });
  assert.equal(noRoute.state, 'recommended');
  assert.match(noRoute.text, /Rec/);
});

test('askTargetDescription says nothing when there is no target at all', () => {
  assert.deepEqual(askTargetDescription({}), { state: 'none', text: '' });
  assert.deepEqual(askTargetDescription(), { state: 'none', text: '' });
  // A nameless workspace is not a target worth announcing.
  assert.equal(askTargetDescription({ selected: { name: '  ' } }).state, 'none');
});

// ===========================================================================
// Updates badge + Quests/context-rail header disclosure state (Issue #334)
// ===========================================================================

test('updatesBadgeView hides at zero attention rather than showing a 0 (FR15)', () => {
  const flattened = [{ id: 'a', kind: 'workspace', needs_attention_count: 0 }];
  const badge = updatesBadgeView(flattened, null);
  assert.equal(badge.count, 0);
  assert.equal(badge.visible, false);
});

test('updatesBadgeView carries the real aggregate attention count when positive (FR15)', () => {
  // The count is how many WORKSPACES need attention, matching the retired
  // rail toggle's badge — not a sum of each workspace's own attention count.
  const flattened = [
    { id: 'a', kind: 'workspace', needs_attention_count: 2 },
    { id: 'b', kind: 'workspace', needs_attention_count: 1 },
    { id: 'c', kind: 'workspace', needs_attention_count: 0 }
  ];
  const badge = updatesBadgeView(flattened, null);
  assert.equal(badge.count, 2);
  assert.equal(badge.visible, true);
});

test('updatesBadgeView never lets an unavailable source read as a fabricated 0', () => {
  // No workspace here reports an attention field at all, so the underlying
  // signal is unknown — the badge must still resolve to a real (hidden) 0
  // rather than throwing, and must never invent a positive count.
  const badge = updatesBadgeView([{ id: 'a', kind: 'workspace' }], null);
  assert.equal(badge.count, 0);
  assert.equal(badge.visible, false);
});

test('contextModalShouldShow keeps context kind and requested visibility separate', () => {
  assert.equal(
    contextModalShouldShow({ railState: RAIL_WORKSPACE, requestedOpen: false }),
    false,
    'a dismissed workspace context stays selected without reopening'
  );
  assert.equal(contextModalShouldShow({ railState: RAIL_WORKSPACE, requestedOpen: true }), true);
  assert.equal(contextModalShouldShow({ railState: RAIL_GROUP, requestedOpen: true }), true);
  assert.equal(contextModalShouldShow({ railState: RAIL_SUMMARY, requestedOpen: true }), true);
});

test('contextModalShouldShow rejects bare, unavailable, and Ask Ori states', () => {
  assert.equal(contextModalShouldShow({ railState: RAIL_TODAY, requestedOpen: true }), false);
  assert.equal(contextModalShouldShow({ railState: 'ask-ori', requestedOpen: true }), false);
  assert.equal(
    contextModalShouldShow({
      railState: RAIL_WORKSPACE,
      requestedOpen: true,
      targetAvailable: false
    }),
    false
  );
});

test('togglePanelState: activating a closed trigger opens only that panel (FR7)', () => {
  assert.equal(togglePanelState(PANEL_NONE, PANEL_UPDATES), PANEL_UPDATES);
  assert.equal(togglePanelState(PANEL_NONE, PANEL_QUESTS), PANEL_QUESTS);
  assert.equal(togglePanelState(PANEL_NONE, PANEL_CAPTURE), PANEL_CAPTURE);
});

test('togglePanelState: activating the SAME open trigger closes it (FR7)', () => {
  assert.equal(togglePanelState(PANEL_UPDATES, PANEL_UPDATES), PANEL_NONE);
  assert.equal(togglePanelState(PANEL_QUESTS, PANEL_QUESTS), PANEL_NONE);
  assert.equal(togglePanelState(PANEL_CAPTURE, PANEL_CAPTURE), PANEL_NONE);
});

test('togglePanelState: activating a DIFFERENT trigger replaces whichever was open (FR8-FR9)', () => {
  assert.equal(togglePanelState(PANEL_UPDATES, PANEL_QUESTS), PANEL_QUESTS);
  assert.equal(togglePanelState(PANEL_QUESTS, PANEL_UPDATES), PANEL_UPDATES);
  assert.equal(togglePanelState(PANEL_UPDATES, PANEL_CAPTURE), PANEL_CAPTURE);
  assert.equal(togglePanelState(PANEL_CAPTURE, PANEL_QUESTS), PANEL_QUESTS);
});

test('panelTriggerId: closing a panel restores focus to the button that owns it (FR11)', () => {
  assert.equal(panelTriggerId(PANEL_UPDATES), 'cockpitRailToggle');
  assert.equal(panelTriggerId(PANEL_QUESTS), 'cockpitQuestsToggle');
  assert.equal(panelTriggerId(PANEL_CAPTURE), 'cockpitCaptureBtn');
});

test('panelTriggerId: no panel open means no focus restoration target', () => {
  assert.equal(panelTriggerId(PANEL_NONE), '');
});

// ===========================================================================
// Header panel / context-modal coordination (Issues #334 and #366)
//
// Header disclosures keep their own single-value state. Opening blocking
// context closes that state through the DOM controller, while data updates
// alone can never request modal visibility.
// ===========================================================================

test('togglePanelState: a full walk through every trigger never leaves more than one panel value at a time', () => {
  const trail = [
    PANEL_UPDATES,
    PANEL_QUESTS,
    PANEL_QUESTS,
    PANEL_CAPTURE,
    PANEL_UPDATES,
    PANEL_UPDATES
  ];
  const expected = [
    PANEL_UPDATES,
    PANEL_QUESTS,
    PANEL_NONE,
    PANEL_CAPTURE,
    PANEL_UPDATES,
    PANEL_NONE
  ];
  let panel = PANEL_NONE;
  const observed = trail.map(requested => {
    panel = togglePanelState(panel, requested);
    return panel;
  });
  assert.deepEqual(observed, expected);
});

test('context modal visibility requires an explicit request, regardless of header panel data', () => {
  for (const panel of [PANEL_NONE, PANEL_UPDATES, PANEL_QUESTS, PANEL_CAPTURE]) {
    assert.equal(
      contextModalShouldShow({ railState: RAIL_WORKSPACE, requestedOpen: false }),
      false,
      `panel=${panel}`
    );
  }
});
