import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { WorkspaceMemoryManager } from './workspace-detail-memory.js';
import { tacticalTermsIn } from './hq-labels.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function makeView(over = {}) {
  const view = Object.create(WorkspaceCommandView.prototype);
  Object.assign(
    view,
    {
      active: true,
      statModalSection: '',
      identityEditMode: false,
      taskDrawerOpen: false,
      backlogDrawerOpen: false,
      trayOpen: false,
      trayCollapsed: false,
      taskComposerOpen: false,
      viewMode: 'map',
      activeMapWindow: '',
      mapInventoryOpen: false,
      activeRailSection: '',
      render() {},
      renderTrayBody() {},
      closeTaskDrawer() {
        this.taskDrawerOpen = false;
      },
      closeBacklogDrawer() {
        this.backlogDrawerOpen = false;
      },
      captureHistoryPresentationState() {}
    },
    over
  );
  return view;
}

function esc(view) {
  view.handleGlobalKeydown({ key: 'Escape' });
}

test('terminology: Map window labels are presentation-only Mission/Tickets, keys unchanged (FR3-5, FR5a)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const options = view.mapWindowOptions();
  const objective = options.find(o => o.key === 'objective');
  const objectives = options.find(o => o.key === 'objectives');
  assert.equal(objective.label, 'Workspace Goal');
  assert.equal(objectives.label, 'Tickets');
  // Panel keys are untouched — activeMapWindow state and every reference to
  // them elsewhere keeps working without a rename.
  assert.equal(objective.key, 'objective');
  assert.equal(objectives.key, 'objectives');
});

test('terminology: the Map Backlog tool-belt entry is named Backlog (FR50, 53, 57; one-vocabulary FR4)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const options = view.mapWindowOptions();
  const backlog = options.find(o => o.key === 'backlog');
  assert.ok(backlog, 'a dedicated Backlog map window option exists');
  assert.equal(
    backlog.label,
    'Backlog',
    'accessible name/title is the Backlog state, never a quest'
  );
});

test('terminology: the Backlog window reads Backlog over Saved for later in every branch (FR50; one-vocabulary FR5)', () => {
  const view = makeView({
    page: {
      backlogItems: [],
      backlogSync: null,
      backlogIncludeDescendants: false,
      workspaceId: 'w1'
    }
  });
  const html = view.renderMapBacklogPanel();
  assert.match(html, /<strong>Saved for later<\/strong>/, 'title says what the state means');
  assert.match(html, /<span>Backlog<\/span>/, 'kicker names the real Backlog lifecycle');
  assert.doesNotMatch(html, /Quest/);
});

test('terminology: Start always spells out moving the ticket into work in its accessible name (FR9, 31, 53)', () => {
  const view = makeView({
    page: {
      backlogItems: [{ task: { id: 'b1', description: 'Ship it' } }],
      backlogSync: null,
      backlogIncludeDescendants: false,
      workspaceId: 'w1'
    }
  });
  const html = view.renderMapBacklogPanel();
  assert.match(html, /<span>Start<\/span>/, 'visible label is Start');
  assert.match(
    html,
    /aria-label="Start — move Ship it into work"/,
    'accessible name spells out the real action'
  );
});

// ---------- HQ map plain names (PRD 767 FR17, FR18) ----------

function hqMapView(over = {}) {
  return makeView({
    selectedAgentKey: '',
    page: {
      workspaceId: 'hq',
      workspace: { id: 'hq', name: 'Personal HQ', designation: 'personal_hq' },
      tasks: [],
      backlogItems: [],
      backlogSync: null,
      buildAgentGroups: () => [
        { name: 'Atlas', isWorkspaceAgent: true, isUnassigned: false, tasks: [] },
        { name: 'Bo', isWorkspaceAgent: true, isUnassigned: false, tasks: [] }
      ],
      isWorkspaceEntryAgent: name => name === 'Atlas',
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' })
    },
    ...over
  });
}

// Every visible word and every accessible name in a piece of markup.
function wordsOf(html) {
  const attributes = [...html.matchAll(/(?:aria-label|title)="([^"]*)"/g)].map(m => m[1]);
  return attributes.join(' | ') + ' | ' + html.replace(/<[^>]+>/g, ' ');
}

test('terminology: the HQ map belt reads Workspace goal, Tickets, Backlog, Inventory, Stations', () => {
  const view = hqMapView();
  assert.deepEqual(
    view.mapWindowOptions().map(option => [option.key, option.label]),
    [
      ['objective', 'Workspace goal'],
      ['objectives', 'Tickets'],
      ['backlog', 'Backlog'],
      ['inventory', 'Inventory'],
      ['stations', 'Stations']
    ]
  );
  view.mapArrangement = 'free';
  view.mapArrangementFor = 'hq';
  const belt = view.renderMapToolTray();
  for (const label of ['Workspace goal', 'Tickets', 'Backlog', 'Inventory', 'Stations']) {
    assert.match(belt, new RegExp('aria-label="' + label + '"'));
    assert.match(belt, new RegExp('title="' + label + '"'));
  }
});

test('terminology: no tactical word on the HQ map in Flow or in Free', () => {
  const view = hqMapView();
  const flow = view.renderOperationsMap();
  assert.match(flow, /is-flow/);
  view.mapArrangement = 'free';
  view.mapArrangementFor = 'hq';
  const free = view.renderOperationsMap();
  assert.match(free, /ws-cmd-map-belt/);
  // The lead unit says Lead.
  assert.match(free, /aria-label="Select Atlas, Lead · Personal HQ\. /);
  const windows = ['objective', 'objectives', 'backlog', 'inventory', 'stations'].map(key => {
    view.activeMapWindow = key;
    return view.renderOperationsMap();
  });
  view.activeMapWindow = '';
  // Without a lead, the repair card asks for a lead.
  const leaderless = hqMapView();
  leaderless.page.isWorkspaceEntryAgent = () => false;
  leaderless.mapArrangement = 'free';
  leaderless.mapArrangementFor = 'hq';
  const repair = leaderless.renderOperationsMap();
  assert.match(repair, /No lead/);
  assert.match(repair, />Hire a lead</);
  for (const [name, html] of [
    ['flow', flow],
    ['free', free],
    ['repair', repair],
    ...windows.map((html, i) => ['window ' + i, html])
  ]) {
    assert.deepEqual(tacticalTermsIn(wordsOf(html)), [], name);
  }
});

test('terminology: off the map, the HQ says Your HQ and Lead (eyebrow, Agents window, roster)', () => {
  const words = workspace => {
    const view = hqMapView();
    view.page.workspace = workspace;
    view.page.getAgentProfile = () => ({ role: 'orchestrator' });
    const bar = view.commandBarHTML(workspace, workspace.name, 'Guided', view.computeStats());
    const agents = view.agentRowsHTML();
    const roster = view.rosterItemHTML({
      key: 'atlas',
      name: 'Atlas',
      encodedName: 'Atlas',
      entry: true,
      tone: 'idle',
      status: { label: 'Idle' },
      profile: { role: 'orchestrator' },
      role: { label: 'Lead' }
    });
    return { bar, agents, roster };
  };

  const hq = words({ id: 'hq', name: 'Personal HQ', designation: 'personal_hq' });
  assert.match(hq.bar, />Your HQ</);
  assert.doesNotMatch(hq.bar, /Outpost/);
  assert.match(hq.agents, /★ Lead</);
  assert.match(hq.agents, /title="Lead — can&#39;t be removed"|title="Lead — can't be removed"/);
  assert.match(hq.roster, /title="Lead">Lead</);
  for (const html of Object.values(hq)) assert.deepEqual(tacticalTermsIn(wordsOf(html)), []);

  // An ordinary workspace keeps its words.
  const plain = words({ id: 'w', name: 'Studio' });
  assert.match(plain.bar, /Outpost · Command/);
  assert.match(plain.agents, /★ Commander</);
  assert.match(plain.roster, />CMD</);
});

test('terminology: an ordinary workspace map keeps its own words', () => {
  const view = makeView({
    selectedAgentKey: '',
    page: {
      workspace: { id: 'w', name: 'Studio' },
      tasks: [],
      buildAgentGroups: () => [
        { name: 'Atlas', isWorkspaceAgent: true, isUnassigned: false, tasks: [] }
      ],
      isWorkspaceEntryAgent: name => name === 'Atlas',
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' })
    }
  });
  const html = view.renderOperationsMap();
  assert.match(html, /aria-label="Backlog"/);
  assert.match(html, /aria-label="Workspace Goal"/);
  assert.match(html, /Acting Commander/);
  assert.doesNotMatch(html, /ws-cmd-map-arrangement|is-flow/);
});

test('Escape prioritizes the Backlog drawer over the Tasks drawer (only one drawer is ever open, but Escape must still close it first)', () => {
  const view = makeView({
    backlogDrawerOpen: true,
    taskDrawerOpen: false,
    trayOpen: true,
    trayCollapsed: false
  });
  esc(view);
  assert.equal(view.backlogDrawerOpen, false);
  assert.equal(
    view.trayCollapsed,
    false,
    'tray is untouched once the Backlog drawer handled Escape'
  );
});

test('Escape collapses an expanded tray rather than closing it (FR123)', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: false });
  esc(view);
  assert.equal(view.trayCollapsed, true, 'tray collapses');
  assert.equal(view.trayOpen, true, 'tray is not closed/hidden by Escape');
});

test('Escape on an already-collapsed tray falls through to the next priority tier', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: true, activeMapWindow: 'objectives' });
  esc(view);
  assert.equal(view.activeMapWindow, '', 'falls through to close the map window');
});

test('Escape no-ops while a stat modal or identity edit is active (dialog tier takes precedence)', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: false, statModalSection: 'tasks' });
  esc(view);
  assert.equal(view.trayCollapsed, false, 'the tray is not touched while a modal is open');
});

test('trayAnnounceText only changes on a real state transition, not on every render (FR127)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const run = (taskId, state) => ({
    taskId,
    task: { description: 'Export the master' },
    presentation: { state, label: state === 'running' ? 'Running' : 'Completed' }
  });

  const first = view.trayAnnounceText(run('t1', 'running'));
  assert.match(first, /Export the master: Running/);

  // Same state again (e.g. a poll tick with no change) — same text, not a
  // fresh announcement each render.
  const second = view.trayAnnounceText(run('t1', 'running'));
  assert.equal(second, first);

  // A genuine transition updates the text.
  const third = view.trayAnnounceText(run('t1', 'completed'));
  assert.match(third, /Export the master: Completed/);
  assert.notEqual(third, first);
});

test('trayHTML never marks the raw activity log as a live region (FR127)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.execController = {
    getSelected: () => ({
      taskId: 't1',
      task: { description: 'Export the master' },
      presentation: { state: 'running', label: 'Running', tone: 'active', primaryAction: null },
      activity: [{ label: 'Running' }],
      startedAt: Date.now()
    }),
    getRuns: () => [],
    getAttentionRuns: () => []
  };
  view.trayCollapsed = false;
  view.trayElapsedLabel = () => '5s';
  const html = view.trayHTML();
  assert.ok(
    !html.includes('class="ws-cmd-tray-log" role="log" aria-live'),
    'the raw log is not a live region'
  );
  assert.match(
    html,
    /ws-cmd-tray-live sr-only.*aria-live="polite"/s,
    'a small dedicated live region exists instead'
  );
});

// ---------- Memory station (PRD memory-station FR23) ----------

function memoryManager() {
  return new WorkspaceMemoryManager({
    workspaceId: 'ws-1',
    workspaceSlug: 'album-three',
    escapeHtml: text =>
      String(text ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
  });
}

test('terminology: the Memory station is named "Memory" on the map, in the rail, and on its panel', () => {
  const view = makeView({
    page: { workspace: { id: 'ws-1' }, tasks: [] },
    _memoryStation: {
      workspaceID: 'ws-1',
      status: 'ready',
      absent: false,
      summary: { count: 14, watching: 2, dropped: 0, overBudget: false }
    }
  });
  const station = view.mapStationRegistry().find(entry => entry.key === 'memory');
  assert.equal(station.label, 'Memory');
  assert.deepEqual(view.statSectionMeta('memory'), { title: 'Memory', addLabel: '' });
  assert.match(view.renderMapHQStations(), /aria-label="Memory station, 14 entries, 2 watching"/);
  // Systems no longer names Memory: it is a station, not configuration.
  assert.deepEqual(
    view.systemTabs().map(tab => tab.label),
    ['Triggers', 'Capabilities']
  );
});

test('a11y: the budget meter is a labelled meter and its percentage says what it measures', () => {
  const m = memoryManager();
  const meta = { innerHTML: '', querySelector: () => null };
  m.elements = { meta };
  m.meta = { rawSize: 9000, charBudget: 8000, overBudget: true };
  m.entries = [{ type: 'fact', text: 'old', in_prompt: false }];
  m.renderMeta();
  assert.match(meta.innerHTML, /role="meter"/);
  assert.match(meta.innerHTML, /aria-label="Share of the memory agents are shown"/);
  assert.match(meta.innerHTML, /aria-valuemin="0" aria-valuemax="100" aria-valuenow="100"/);
  // Never a bare percentage, which would read as disk usage.
  assert.match(meta.innerHTML, /100% of what agents are shown/);
  assert.match(meta.innerHTML, /Over budget: 1 entry is not shown to agents/);
});

test('a11y: dropped entries say so in text, and the panel sections are real headings', () => {
  const m = memoryManager();
  m.globalMemory = { available: true };
  m.elements = { list: { innerHTML: '' } };
  m.entries = [
    { type: 'watch', date: '2026-10-18', provenance: 'user', text: 'kept' },
    { type: 'fact', date: '2026-01-01', provenance: 'user', text: 'old', in_prompt: false }
  ];
  m.renderList();
  const html = m.elements.list.innerHTML;
  assert.match(html, /<span class="workspace-detail-memory-dropped">not shown to agents<\/span>/);
  assert.match(html, /<h4 class="workspace-detail-memory-heading"[^>]*>Watching /);
  assert.match(html, /<h4 class="workspace-detail-memory-heading"[^>]*>Facts and feedback /);
  assert.match(html, /<section class="workspace-detail-memory-section"[^>]*aria-labelledby=/);
});

test('terminology: the learnings strip is "What recent runs learned" with remembered/forgot counts', () => {
  const m = memoryManager();
  const strip = { hidden: true, innerHTML: '' };
  m.elements = { learnings: strip };
  m.learnings = [{ id: 'run-1', at: '2026-10-18T09:00:00Z', added: 3, removed: 1 }];
  m.renderLearnings();
  assert.match(strip.innerHTML, /<h4[^>]*>What recent runs learned<\/h4>/);
  assert.match(strip.innerHTML, /remembered 3 · forgot 1/);
  assert.match(strip.innerHTML, /href="\/workspaces\/album-three\/runs\/run-1"/);
});
