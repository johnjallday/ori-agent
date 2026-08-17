// home-workspace-cockpit.js — the Map-first Home cockpit coordinator.
//
// PRD: tasks/prd-home-workspace-cockpit.md. This module owns the ONE cockpit
// boundary described in PRD §7 "Recommended Module Boundary":
//
//   - the shared workspace collection (fetched once per refresh cycle, FR111);
//   - the active Map/Tree view and its `view` query state (FR25/FR26);
//   - the active workspace/group selection shared by every view (FR57);
//   - the context-rail state (Today / workspace / group / Summary / Ask Ori);
//   - the refresh + mutation lifecycle.
//
// It deliberately does NOT load workspace-hub.js: mounting the launcher
// coordinator here would be a second launcher implementation, which the PRD
// forbids. The small set of tree/group/tag helpers the cockpit needs are
// reimplemented below as exported pure functions instead — that also makes them
// unit-testable, which the launcher's closure-scoped copies are not.
//
// Loaded as type="module" AFTER the deferred classic workspace-map.js, which
// preserves the standing requirement that the map is defined before the
// coordinator calls OriWorkspaceMap.mount(...) (FR123).
//
// Following home-daily-brief.js: every decision/rendering helper is an exported
// pure function, and the DOM-wiring IIFE at the bottom no-ops when `document`
// is undefined, so home-workspace-cockpit.test.js runs under plain Node with no
// DOM and no network.

// ---------------------------------------------------------------------------
// Small shared utilities
// ---------------------------------------------------------------------------

export function escapeHtml(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]
  );
}

/**
 * Read a count that may legitimately be absent.
 *
 * FR121 is explicit that an unavailable value must not be rendered as zero, so
 * this returns `null` for missing/unparseable input and a number only when the
 * payload actually carried one. Callers render `null` as an unavailable state.
 */
export function readCount(value) {
  if (value === undefined || value === null || value === '') return null;
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return null;
  return Math.max(0, Math.trunc(parsed));
}

/** Format a possibly-unavailable count for display (FR44, FR121). */
export function formatCount(value) {
  return value === null || value === undefined ? '—' : String(value);
}

// ---------------------------------------------------------------------------
// View state (FR25, FR26, FR27, FR6)
// ---------------------------------------------------------------------------

export const VIEW_MAP = 'map';
export const VIEW_TREE = 'tree';

/**
 * Resolve the active view from a URL query string.
 *
 * The query is authoritative when it carries a valid value (FR25). Anything
 * else — missing, empty, the retired `cards` value (FR6/FR27), or garbage —
 * normalizes to Map without a redirect.
 */
export function parseViewFromQuery(search) {
  const raw = new URLSearchParams(String(search || '')).get('view');
  return String(raw || '')
    .trim()
    .toLowerCase() === VIEW_TREE
    ? VIEW_TREE
    : VIEW_MAP;
}

/**
 * Build the search string for a view change.
 *
 * Tree is explicit (`view=tree`); Map is the default and therefore REMOVES the
 * parameter rather than writing `view=map` (FR26). Every unrelated parameter is
 * preserved, so a link carrying e.g. `?create=1` survives a view toggle.
 */
export function searchForView(search, view) {
  const params = new URLSearchParams(String(search || ''));
  if (view === VIEW_TREE) params.set('view', VIEW_TREE);
  else params.delete('view');
  const next = params.toString();
  return next ? `?${next}` : '';
}

// ---------------------------------------------------------------------------
// Workspace collection shaping
// ---------------------------------------------------------------------------

export function normalizeKind(kind) {
  return String(kind || '')
    .trim()
    .toLowerCase();
}

export function isGroupWorkspace(workspace) {
  return normalizeKind(workspace && workspace.kind) === 'group';
}

/** Flatten the `?tree=true` hierarchy into the flat rows the Map consumes. */
export function flattenWorkspaceTree(nodes, depth = 0, path = [], parentId = '') {
  const rows = [];
  (Array.isArray(nodes) ? nodes : []).forEach(workspace => {
    if (!workspace) return;
    const currentPath = [...path, workspace.name || 'Untitled'];
    rows.push({
      ...workspace,
      depth,
      parent_id: workspace.parent_id || parentId || '',
      path: currentPath.join(' / ')
    });
    const children = Array.isArray(workspace.children) ? workspace.children : [];
    if (children.length > 0) {
      rows.push(...flattenWorkspaceTree(children, depth + 1, currentPath, workspace.id));
    }
  });
  return rows;
}

export function findWorkspace(flattened, id) {
  if (!id) return null;
  return (Array.isArray(flattened) ? flattened : []).find(ws => ws && ws.id === id) || null;
}

export function normalizeTags(tags) {
  if (!Array.isArray(tags)) return [];
  return tags.map(tag => String(tag || '').trim()).filter(Boolean);
}

/**
 * Build the per-id metadata bundle the Map's renderer expects.
 *
 * Mirrors the launcher's `buildLauncherMapMetadata` contract so the production
 * Map renders identically from cockpit state, without importing the launcher.
 */
export function buildMapMetadata(flattened, tree) {
  const folderDisplayById = {};
  const tagsById = {};
  const groupPreviewById = {};

  (Array.isArray(flattened) ? flattened : []).forEach(workspace => {
    if (!workspace || !workspace.id) return;
    folderDisplayById[workspace.id] = folderDisplayFor(workspace);
    tagsById[workspace.id] = normalizeTags(workspace.tags);
  });

  const visit = nodes => {
    (Array.isArray(nodes) ? nodes : []).forEach(workspace => {
      if (!workspace || !workspace.id) return;
      const children = Array.isArray(workspace.children) ? workspace.children : [];
      if (isGroupWorkspace(workspace)) {
        const previewNames = children
          .slice(0, 3)
          .map(child => String((child && (child.name || child.id)) || 'Untitled Workspace').trim())
          .filter(Boolean);
        groupPreviewById[workspace.id] = {
          childCount: children.length,
          previewNames,
          overflowCount: Math.max(0, children.length - previewNames.length)
        };
      }
      if (children.length > 0) visit(children);
    });
  };
  visit(tree);

  return { folderDisplayById, tagsById, groupPreviewById };
}

/**
 * Linked-folder badge data for a workspace, matching the Map's expectations.
 *
 * The wire field is `directory_references` (see `/api/workspaces?tree=true` and
 * the launcher's collectWorkspaceLinkedDirectories). `directories` is accepted
 * as a fallback because some callers pass an already-normalized shape.
 */
export function folderDisplayFor(workspace) {
  const refs = Array.isArray(workspace && workspace.directory_references)
    ? workspace.directory_references
    : Array.isArray(workspace && workspace.directories)
      ? workspace.directories
      : [];
  const directories = refs.filter(Boolean);
  const primary = directories.find(dir => dir && dir.is_primary) || directories[0] || null;
  const path = String((primary && (primary.path || primary.name)) || '').trim();
  if (!path) {
    return {
      linked: false,
      badgeLabel: 'No folder linked',
      badgeClass: 'is-unlinked',
      detail: 'No local folder attached.',
      detailTitle: 'This workspace is not linked to a local folder.',
      ariaLabel: 'no linked folder'
    };
  }
  const short = path.split('/').filter(Boolean).slice(-2).join('/') || path;
  return {
    linked: true,
    badgeLabel: 'Folder',
    badgeClass: 'is-linked',
    detail: short,
    detailTitle: path,
    ariaLabel: `linked folder ${short}`
  };
}

// ---------------------------------------------------------------------------
// Operational signals (FR30, FR63, FR121)
// ---------------------------------------------------------------------------

/**
 * Classify a workspace's operational state from data the payload actually
 * carries.
 *
 * The ordering is deterministic and attention-first. `unknown` exists on
 * purpose: when a workspace carries none of the signal fields, saying "Idle"
 * would be inventing an authoritative value (FR121).
 */
export function workspaceSignals(workspace) {
  const ws = workspace || {};
  const attention = readCount(ws.needs_attention_count);
  const openTasks = readCount(ws.open_task_count);
  const agents = readCount(
    ws.agent_count !== undefined
      ? ws.agent_count
      : Array.isArray(ws.agents)
        ? ws.agents.length
        : undefined
  );
  const active = ws.active === true;

  let status;
  if (attention !== null && attention > 0) status = 'attention';
  else if (active) status = 'running';
  else if (openTasks !== null && openTasks > 0) status = 'active';
  else if (attention === null && openTasks === null && !('active' in ws)) status = 'unknown';
  else status = 'idle';

  const label = {
    attention: 'Needs attention',
    running: 'Running',
    active: 'Active',
    idle: 'Idle',
    unknown: 'Status unavailable'
  }[status];

  return { status, label, attention, openTasks, agents, active };
}

// ---------------------------------------------------------------------------
// Signal filters (FR31, FR32, FR33, FR34)
//
// Field inventory behind these classifiers, taken from the real
// `/api/workspaces?tree=true` payload (Task 2.1):
//
//   active: bool                 -> Running
//   needs_attention_count: int   -> Attention
//   open_task_count: int         -> workspace context + next move
//   agent_count: int             -> roster size
//   backlog_count / session_count / mcp_count / skill_count: int
//   status / ops_mode / kind: string
//   updated_at / created_at: RFC3339 string
//
// There is NO schedule field on a workspace. "Today" is therefore a JOIN
// against `/api/orchestration/scheduled-tasks/upcoming`, which returns rows
// carrying `workspace_id` and `next_run`. When that source has not loaded or
// has failed, the Today filter reports an unavailable count rather than 0 —
// a 0 there would be a fabricated authoritative value (FR32, FR121).
// ---------------------------------------------------------------------------

export const SIGNAL_ATTENTION = 'attention';
export const SIGNAL_RUNNING = 'running';
export const SIGNAL_TODAY = 'today';
export const SIGNALS = [SIGNAL_ATTENTION, SIGNAL_RUNNING, SIGNAL_TODAY];

export const SIGNAL_LABELS = {
  [SIGNAL_ATTENTION]: 'Attention',
  [SIGNAL_RUNNING]: 'Running',
  [SIGNAL_TODAY]: 'Today'
};

/** Normalize a filter value; anything unrecognized means "no filter". */
export function parseSignal(value) {
  const signal = String(value || '')
    .trim()
    .toLowerCase();
  return SIGNALS.includes(signal) ? signal : '';
}

/**
 * Index upcoming scheduled work by workspace id.
 *
 * Returns `null` — not an empty index — when the schedule source is
 * unavailable, so callers can tell "nothing scheduled" apart from "we don't
 * know" (FR32, FR120, FR121).
 */
export function buildScheduleIndex(rows) {
  if (!Array.isArray(rows)) return null;
  const index = Object.create(null);
  rows.forEach(row => {
    const id = String((row && row.workspace_id) || '').trim();
    if (!id) return;
    (index[id] = index[id] || []).push(row);
  });
  return index;
}

/**
 * Does this workspace have scheduled work due within the local calendar day?
 *
 * `null` means the schedule source is unavailable for this workspace.
 */
export function hasWorkTodayFor(workspaceId, scheduleIndex, now = new Date()) {
  if (!scheduleIndex) return null;
  const rows = scheduleIndex[workspaceId] || [];
  const endOfDay = new Date(now);
  endOfDay.setHours(23, 59, 59, 999);
  return rows.some(row => {
    const at = new Date(String((row && (row.next_run || row.next_run_at)) || ''));
    return !Number.isNaN(at.getTime()) && at <= endOfDay;
  });
}

/**
 * Does a workspace match a signal filter?
 *
 * `null` means "cannot tell from the data we have" — such a workspace is
 * de-emphasized rather than claimed as a match or a non-match.
 */
export function matchesSignal(workspace, signal, scheduleIndex, now) {
  if (!signal) return true;
  const ws = workspace || {};
  // Groups are containers, not execution workspaces; a signal filter is about
  // where work is happening, so groups never match one (FR71).
  if (isGroupWorkspace(ws)) return false;
  const signals = workspaceSignals(ws);
  if (signal === SIGNAL_ATTENTION) {
    return signals.attention === null ? null : signals.attention > 0;
  }
  if (signal === SIGNAL_RUNNING) {
    return 'active' in ws ? signals.active : null;
  }
  return hasWorkTodayFor(String(ws.id || ''), scheduleIndex, now);
}

/**
 * Count how many workspaces match each signal.
 *
 * A count is `null` when the underlying source is unavailable, so the chip can
 * render an honest em dash instead of a fabricated zero (FR32).
 */
export function signalCounts(workspaces, scheduleIndex, now) {
  const rows = (Array.isArray(workspaces) ? workspaces : []).filter(
    ws => ws && !isGroupWorkspace(ws)
  );
  const counts = {};
  SIGNALS.forEach(signal => {
    if (signal === SIGNAL_TODAY && !scheduleIndex) {
      counts[signal] = null;
      return;
    }
    const known = rows.map(ws => matchesSignal(ws, signal, scheduleIndex, now));
    // If NOTHING could be classified, the count is unknown rather than zero.
    counts[signal] =
      known.every(v => v === null) && known.length > 0
        ? null
        : known.filter(v => v === true).length;
  });
  return counts;
}

/**
 * The Updates badge's count and visibility (FR15-FR17).
 *
 * Aggregate workspace attention only — Progression/onboarding state is never
 * a valid input here, which is what keeps the badge from drifting back into
 * meaning "something in the old Today rail wants you" (Issue #334). Hidden
 * at zero rather than showing a "0" (mirrors the old rail-toggle count).
 */
export function updatesBadgeView(flattened, scheduleIndex) {
  const counts = signalCounts(flattened, scheduleIndex);
  const attention = counts ? counts[SIGNAL_ATTENTION] : null;
  const count = typeof attention === 'number' ? attention : 0;
  return { count, visible: count > 0 };
}

/**
 * Whether the CONTEXT rail should be open, from its own state alone.
 *
 * A real selection (workspace/group/HQ site, Summary, Ask Ori) opens it; a
 * bare Today never does — not for attention, not for Progression, not for
 * anything else (FR41-FR47). Updates/Quests are separate overlays with their
 * own `panel` state (see togglePanelState) and never factor in here.
 */
export function contextRailShouldBeOpen(railState) {
  return railState !== RAIL_TODAY;
}

/** Apply the active filter, returning the ids that should stay prominent. */
export function filterWorkspaceIds(workspaces, signal, scheduleIndex, now) {
  if (!signal) return null; // null = no filter, everything is prominent
  return (Array.isArray(workspaces) ? workspaces : [])
    .filter(ws => ws && matchesSignal(ws, signal, scheduleIndex, now) === true)
    .map(ws => ws.id);
}

/** Accessible result announcement for a filter change (FR33). */
export function filterResultMessage(signal, count) {
  if (!signal) return 'Filter cleared. Showing all workspaces.';
  const label = SIGNAL_LABELS[signal] || signal;
  if (count === null || count === undefined) {
    return `${label} filter applied. The data needed for this filter is unavailable.`;
  }
  return count === 1
    ? `${label} filter applied. 1 workspace matches.`
    : `${label} filter applied. ${count} workspaces match.`;
}

export function renderSignalFiltersHTML(counts, activeSignal) {
  return SIGNALS.map(signal => {
    const isActive = signal === activeSignal;
    const count = counts ? counts[signal] : null;
    const label = SIGNAL_LABELS[signal];
    return (
      `<button type="button" class="cockpit-signal-chip" data-cockpit-signal="${escapeHtml(signal)}" ` +
      `aria-pressed="${isActive ? 'true' : 'false'}">` +
      `<span class="cockpit-signal-label">${escapeHtml(label)}</span>` +
      `<span class="cockpit-signal-count"${count === null ? ' data-unavailable="true"' : ''}>` +
      `${escapeHtml(formatCount(count))}</span>` +
      '</button>'
    );
  }).join('');
}

/**
 * Deterministic recommended next move (FR64, FR65, FR66).
 *
 * Grounded entirely in fields already present on the workspace payload — there
 * is no model call here and the copy must never imply one. When nothing
 * qualifies, the honest "No immediate action" answer is returned and the caller
 * still renders Open Workspace (FR66).
 */
export function recommendedNextMove(workspace) {
  const ws = workspace || {};
  const signals = workspaceSignals(ws);

  if (signals.attention !== null && signals.attention > 0) {
    return {
      kind: 'attention',
      label:
        signals.attention === 1
          ? 'Resolve 1 item needing attention'
          : `Resolve ${signals.attention} items needing attention`,
      detail: 'Flagged by this workspace, waiting on you.'
    };
  }
  if (ws.setup_required === true || ws.setup_pending === true) {
    return {
      kind: 'setup',
      label: 'Finish workspace setup',
      detail: 'Setup has not been completed for this workspace.'
    };
  }
  if (signals.active) {
    return {
      kind: 'running',
      label: 'Check work in progress',
      detail: 'An agent is currently working in this workspace.'
    };
  }
  const nextRun = String(ws.next_scheduled_run || ws.next_run || '').trim();
  if (nextRun) {
    return {
      kind: 'scheduled',
      label: 'Review the next scheduled run',
      detail: nextRun
    };
  }
  if (signals.openTasks !== null && signals.openTasks > 0) {
    return {
      kind: 'tasks',
      label:
        signals.openTasks === 1 ? 'Pick up 1 open task' : `Pick up ${signals.openTasks} open tasks`,
      detail: 'Open work already queued in this workspace.'
    };
  }
  if (signals.openTasks === null && signals.attention === null) {
    return {
      kind: 'unavailable',
      label: 'Next move unavailable',
      detail: 'This workspace did not report task or attention data.'
    };
  }
  return {
    kind: 'none',
    label: 'No immediate action',
    detail: 'Nothing is waiting on you in this workspace right now.'
  };
}

/** The workspace's resolved entry agent, or '' when it has none (FR68). */
export function entryAgentName(workspace) {
  return String((workspace && workspace.entry_agent_name) || '').trim();
}

/** Compact roster rows for the rail (FR69). */
export function agentRoster(workspace) {
  const agents = Array.isArray(workspace && workspace.agents) ? workspace.agents : [];
  return agents
    .map(agent => {
      if (!agent) return null;
      if (typeof agent === 'string') return { name: agent, role: '', activity: '' };
      return {
        name: String(agent.name || agent.agent_name || '').trim(),
        role: String(agent.role || '').trim(),
        activity: String(agent.activity || agent.status || '').trim()
      };
    })
    .filter(row => row && row.name);
}

/**
 * Decide what the roster section can honestly say.
 *
 * The workspace list payload carries `agent_count` but only sometimes carries
 * the `agents` array. Rendering "No agents attached yet" for a workspace that
 * reports 3 agents would be a flat lie, so the three cases are kept distinct
 * (FR69, FR121).
 */
export function rosterState(workspace) {
  const roster = agentRoster(workspace);
  const count = workspaceSignals(workspace).agents;
  if (roster.length > 0) return { state: 'listed', roster, count };
  if (count === null) return { state: 'unavailable', roster: [], count: null };
  if (count > 0) return { state: 'count-only', roster: [], count };
  return { state: 'empty', roster: [], count: 0 };
}

// ---------------------------------------------------------------------------
// Group aggregates (FR70, FR71)
// ---------------------------------------------------------------------------

/**
 * Sum a group's descendants.
 *
 * A metric is `null` when NO descendant reported it — summing absent values
 * into 0 would present missing data as an authoritative total (FR121). A
 * partially-reported metric returns the sum of what was reported plus the
 * count of workspaces that did not report, so the rail can say so.
 */
export function groupAggregates(group, flattened) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const groupId = String((group && group.id) || '');
  const descendants = [];

  const collect = parentId => {
    rows.forEach(row => {
      if (!row || row.parent_id !== parentId) return;
      descendants.push(row);
      collect(row.id);
    });
  };
  collect(groupId);

  const workspaces = descendants.filter(row => !isGroupWorkspace(row));
  const groups = descendants.filter(isGroupWorkspace);

  const sum = pick => {
    const values = workspaces.map(pick);
    // A group with no workspaces in it genuinely holds no tasks, agents, or
    // attention — that is an authoritative zero, not missing data. Only a group
    // whose workspaces exist but reported nothing is unknown (FR121).
    if (values.length === 0) return { total: 0, missing: 0 };
    const known = values.filter(v => v !== null);
    if (known.length === 0) return { total: null, missing: values.length };
    return {
      total: known.reduce((a, b) => a + b, 0),
      missing: values.length - known.length
    };
  };

  return {
    childCount: rows.filter(row => row && row.parent_id === groupId).length,
    descendantWorkspaces: workspaces.length,
    descendantGroups: groups.length,
    agents: sum(ws => workspaceSignals(ws).agents),
    openTasks: sum(ws => workspaceSignals(ws).openTasks),
    attention: sum(ws => workspaceSignals(ws).attention)
  };
}

/**
 * The rail's Map-layout section for a selected group (#346 FR-151 – FR-154).
 *
 * Returns null when the section must not be shown at all — which is the whole
 * point of the `view` argument. In Tree, a "Resize group" control would imply
 * that Tree rows resize, so Tree hides these rather than showing them dead
 * (FR-154). A group with no district drawn (Map not mounted yet) has nothing to
 * describe either.
 *
 * @param {object|null} district the Map's own snapshot, from getDistrictView
 * @param {{view?: string}} [options]
 */
export function groupMapLayoutView(district, options = {}) {
  if (!district) return null;
  if (options.view && options.view !== VIEW_MAP) return null;
  const custom = district.sizingMode === 'custom';
  const readOnly = !!district.readOnly;
  return {
    collapsed: !!district.collapsed,
    collapseLabel: district.collapsed ? 'Expand group' : 'Collapse group',
    canCollapse: !readOnly,
    sizingMode: custom ? 'custom' : 'auto',
    // The mode is stated in words, not carried by a highlighted chip alone
    // (FR-152).
    sizingLabel: custom
      ? 'Custom size — the size you chose, expanding only to keep its workspaces inside'
      : 'Automatic size — follows its workspaces',
    canResize: !readOnly && !district.collapsed,
    // Fitting a district that is already automatic would change nothing.
    canFit: !readOnly && !district.collapsed && custom,
    // Appearance is meaningful whether a district is open or closed: the
    // collapsed summary wears the same accent and theme (FR-117).
    accent: district.accent || 'default',
    theme: district.theme || 'default',
    accents: Array.isArray(district.accents) ? district.accents : [],
    themes: Array.isArray(district.themes) ? district.themes : [],
    canChangeAppearance: !readOnly,
    // Offered only when there is a customization to undo (FR-137).
    customized:
      (district.accent || 'default') !== 'default' || (district.theme || 'default') !== 'default',
    readOnly,
    readOnlyNote: readOnly ? 'Map layout cannot be saved right now.' : ''
  };
}

export function groupRailView(group, flattened, options = {}) {
  const ws = group || {};
  const aggregates = groupAggregates(ws, flattened);
  return {
    kind: RAIL_GROUP,
    id: String(ws.id || ''),
    name: String(ws.name || 'Untitled group'),
    description: String(ws.description || '').trim(),
    aggregates,
    mapLayout: groupMapLayoutView(options.district || null, { view: options.view }),
    openHref: ws.id ? `/workspaces/${encodeURIComponent(ws.id)}` : ''
  };
}

// ---------------------------------------------------------------------------
// Cross-workspace Summary (FR89, FR90)
// ---------------------------------------------------------------------------

/**
 * Cross-workspace totals, computed from the SAME shared state Map and Tree
 * render from — never from a second fetch (FR89).
 */
export function summaryView(flattened, scheduleIndex, now) {
  const rows = Array.isArray(flattened) ? flattened : [];
  const workspaces = rows.filter(ws => ws && !isGroupWorkspace(ws));
  const groups = rows.filter(isGroupWorkspace);

  const sum = pick => {
    const values = workspaces.map(pick);
    const known = values.filter(v => v !== null);
    if (known.length === 0 && values.length > 0) return null;
    return known.reduce((a, b) => a + b, 0);
  };

  // No attentionWorkspaces list: Today owns that list (see attentionItems), and
  // Summary carries attention as a count only. `attention` below is the total.
  const runningWorkspaces = workspaces.filter(ws => workspaceSignals(ws).active);

  const upcoming = scheduleIndex
    ? Object.keys(scheduleIndex).reduce((total, id) => total + scheduleIndex[id].length, 0)
    : null;

  return {
    workspaces: workspaces.length,
    groups: groups.length,
    agents: sum(ws => workspaceSignals(ws).agents),
    openTasks: sum(ws => workspaceSignals(ws).openTasks),
    attention: sum(ws => workspaceSignals(ws).attention),
    runningWorkspaces: runningWorkspaces.map(ws => ({ id: ws.id, name: ws.name })),
    upcomingCount: upcoming,
    dueToday: scheduleIndex
      ? workspaces.filter(ws => hasWorkTodayFor(String(ws.id || ''), scheduleIndex, now) === true)
          .length
      : null
  };
}

// ---------------------------------------------------------------------------
// Rail view models + rendering
// ---------------------------------------------------------------------------

export const RAIL_TODAY = 'today';
export const RAIL_WORKSPACE = 'workspace';
export const RAIL_GROUP = 'group';
export const RAIL_SUMMARY = 'summary';
export const RAIL_ASK = 'ask-ori';

// ---------------------------------------------------------------------------
// Header disclosure ("panel") state — Updates / Quests / Quick Capture
// ---------------------------------------------------------------------------
//
// One explicit state machine for the three header-owned transient
// disclosures (Issue #334). They are siblings, not independent booleans:
// opening one always closes whichever of the other two was open, so the three
// controls can never drift into contradictory `hidden`/`aria-expanded`
// states. This is deliberately separate from `railState`/contextRailShouldBeOpen()
// above, which governs the CONTEXT rail (a selected workspace/group/HQ site,
// Summary, or Ask Ori) — a real context selection and a manually opened
// flyout can be visible at the same time without disturbing each other.

export const PANEL_NONE = 'none';
export const PANEL_UPDATES = 'updates';
export const PANEL_QUESTS = 'quests';
export const PANEL_CAPTURE = 'capture';

/**
 * The next panel state for activating `requested` from `current`.
 *
 * Activating the ALREADY-open panel closes it (FR7); activating any other
 * panel replaces whichever was open, which is what gives the three triggers
 * their mutual exclusion for free — there is only ever one open value to
 * begin with (FR8-FR9).
 */
export function togglePanelState(current, requested) {
  return current === requested ? PANEL_NONE : requested;
}

/**
 * Which header trigger owns focus-restoration for a given panel (FR11).
 *
 * Closing a flyout from inside it or with Escape returns focus to the
 * element that opened it; this is the pure lookup that decision goes
 * through, kept separate from the DOM `.focus()` call so it is testable
 * without a document.
 */
export function panelTriggerId(panel) {
  if (panel === PANEL_UPDATES) return 'cockpitRailToggle';
  if (panel === PANEL_QUESTS) return 'cockpitQuestsToggle';
  if (panel === PANEL_CAPTURE) return 'cockpitCaptureBtn';
  return '';
}

/**
 * How the rail should describe Ask Ori's current target workspace (FR97).
 *
 * The wording distinguishes a workspace the USER selected, one Ori has merely
 * RECOMMENDED, and one work was actually routed to. A recommendation must never
 * read as routed work before the confirmation policy allows it, so the three
 * cases get three different verbs.
 */
export function askTargetDescription({ selected, recommended, routed } = {}) {
  const name = value => String((value && value.name) || '').trim();
  if (routed && name(routed)) {
    return { state: 'routed', text: `Working in ${name(routed)}.` };
  }
  if (recommended && name(recommended)) {
    return {
      state: 'recommended',
      text: `Suggested workspace: ${name(recommended)}. Nothing has been sent yet.`
    };
  }
  if (selected && name(selected)) {
    return {
      state: 'selected',
      text: `${name(selected)} is selected and offered as context.`
    };
  }
  return { state: 'none', text: '' };
}
// The reserved Personal HQ site — a landmark for an HQ that has not been built
// (or needs repair), not a workspace. Mirrors workspace-map.js's own id so the
// two agree on what "the HQ site is selected" means.
export const HQ_SITE_ID = '__personal_hq_site__';

/**
 * Build the workspace rail's view model.
 *
 * Kept separate from the HTML so the decisions (what is shown, what is honestly
 * unavailable, whether Ask Commander is offered) are assertable without parsing
 * markup.
 */
export function workspaceRailView(workspace) {
  const ws = workspace || {};
  const signals = workspaceSignals(ws);
  const commander = entryAgentName(ws);
  const group = isGroupWorkspace(ws);
  return {
    kind: group ? RAIL_GROUP : RAIL_WORKSPACE,
    id: String(ws.id || ''),
    name: String(ws.name || 'Untitled workspace'),
    mission: String(ws.description || ws.mission || '').trim(),
    isPersonalHQ: ws.is_personal_hq === true || ws.designation === 'personal_hq',
    status: signals,
    nextMove: recommendedNextMove(ws),
    openHref: ws.id ? `/workspaces/${encodeURIComponent(ws.id)}` : '',
    commander,
    // FR68: the action exists only with a resolved entry agent; otherwise the
    // rail explains what is missing rather than offering a dead control.
    canAskCommander: commander !== '',
    commanderUnavailableReason: commander
      ? ''
      : 'No entry agent is assigned, so there is no commander to ask.',
    roster: agentRoster(ws),
    rosterState: rosterState(ws)
  };
}

function metricHTML(label, value) {
  return (
    '<div class="cockpit-rail-metric">' +
    `<span class="cockpit-rail-metric-value"${value === null ? ' data-unavailable="true"' : ''}>` +
    escapeHtml(formatCount(value)) +
    '</span>' +
    `<span class="cockpit-rail-metric-label">${escapeHtml(label)}</span>` +
    '</div>'
  );
}

function rosterHTML(state) {
  if (!state || state.state === 'unavailable') {
    return '<p class="cockpit-rail-empty">Agent data unavailable for this workspace.</p>';
  }
  if (state.state === 'empty') {
    return '<p class="cockpit-rail-empty">No agents attached yet.</p>';
  }
  if (state.state === 'count-only') {
    // The workspace reports agents but this view's payload does not include
    // their names. Saying "no agents" here would contradict the count beside
    // it, so the rail says what it actually knows (FR121).
    return (
      '<p class="cockpit-rail-empty">' +
      escapeHtml(
        state.count === 1
          ? '1 agent attached. Open the workspace to see the roster.'
          : `${state.count} agents attached. Open the workspace to see the roster.`
      ) +
      '</p>'
    );
  }
  return (
    '<ul class="cockpit-rail-roster">' +
    state.roster
      .map(
        agent =>
          '<li class="cockpit-rail-roster-row">' +
          `<span class="cockpit-rail-roster-name">${escapeHtml(agent.name)}</span>` +
          (agent.role
            ? `<span class="cockpit-rail-roster-role">${escapeHtml(agent.role)}</span>`
            : '') +
          (agent.activity
            ? `<span class="cockpit-rail-roster-activity">${escapeHtml(agent.activity)}</span>`
            : '') +
          '</li>'
      )
      .join('') +
    '</ul>'
  );
}

/** A group aggregate reads as "12" or "12 (3 unreported)" or "—" (FR121). */
function aggregateHTML(label, aggregate) {
  const unknown = !aggregate || aggregate.total === null;
  const note =
    !unknown && aggregate.missing > 0
      ? `<span class="cockpit-rail-metric-note">${aggregate.missing} unreported</span>`
      : '';
  return (
    '<div class="cockpit-rail-metric">' +
    `<span class="cockpit-rail-metric-value"${unknown ? ' data-unavailable="true"' : ''}>` +
    escapeHtml(formatCount(unknown ? null : aggregate.total)) +
    '</span>' +
    `<span class="cockpit-rail-metric-label">${escapeHtml(label)}</span>` +
    note +
    '</div>'
  );
}

/**
 * Render the group context rail.
 *
 * A group is a container, not an execution workspace: it gets Open Group and
 * descendant aggregates, and deliberately NO next-move or Ask Commander action
 * (FR70, FR71).
 */
export function renderGroupRailHTML(view) {
  if (!view) return '';
  const a = view.aggregates;
  const childSummary =
    a.descendantGroups > 0
      ? `${a.childCount} direct · ${a.descendantWorkspaces} workspaces · ${a.descendantGroups} nested groups`
      : `${a.childCount} direct · ${a.descendantWorkspaces} workspaces`;
  return (
    '<div class="cockpit-rail-panel" data-rail-panel="group">' +
    '<header class="cockpit-rail-head">' +
    '<button type="button" class="cockpit-rail-back" data-cockpit-rail-back>' +
    '<span aria-hidden="true">&#8592;</span> Back</button>' +
    '<div class="cockpit-rail-identity">' +
    `<h3 class="cockpit-rail-title">${escapeHtml(view.name)}</h3>` +
    '<p class="cockpit-rail-badges">' +
    '<span class="cockpit-rail-tag">Group</span>' +
    `<span class="cockpit-rail-tag">${escapeHtml(childSummary)}</span>` +
    '</p>' +
    '</div>' +
    '</header>' +
    (view.description
      ? `<p class="cockpit-rail-mission">${escapeHtml(view.description)}</p>`
      : '') +
    '<div class="cockpit-rail-actions">' +
    `<a class="modern-btn modern-btn-primary cockpit-rail-open" href="${escapeHtml(view.openHref)}" data-cockpit-rail-open data-workspace-id="${escapeHtml(view.id)}">Open Group</a>` +
    '</div>' +
    '<div class="cockpit-rail-metrics" aria-label="Group totals">' +
    aggregateHTML('Open tasks', a.openTasks) +
    aggregateHTML('Agents', a.agents) +
    aggregateHTML('Attention', a.attention) +
    '</div>' +
    '<p class="cockpit-rail-note">Totals cover every workspace inside this group. ' +
    'A group holds workspaces; it does not run work itself.</p>' +
    mapLayoutSectionHTML(view.mapLayout) +
    '</div>'
  );
}

/**
 * The Map-layout section of the selected-group rail (#346 FR-151 – FR-153).
 *
 * It sits AFTER the aggregates and the Open Group action on purpose: Open Group
 * stays the primary navigation action and is not displaced by layout controls
 * (FR-155). The note is not decoration — a panel of sizing controls under a
 * group is exactly where a user might expect resizing to mean "change what is
 * in it", and it never does (FR-153).
 */
function mapLayoutSectionHTML(layout) {
  if (!layout) return '';
  const disabled = attr => (attr ? '' : ' disabled');
  return (
    '<section class="cockpit-rail-section cockpit-rail-maplayout" data-rail-map-layout>' +
    '<h4 class="cockpit-rail-section-title">Map layout</h4>' +
    `<p class="cockpit-rail-maplayout-mode" data-rail-sizing-mode="${escapeHtml(layout.sizingMode)}">${escapeHtml(layout.sizingLabel)}</p>` +
    '<div class="cockpit-rail-maplayout-actions">' +
    '<button type="button" class="modern-btn cockpit-rail-maplayout-btn" data-cockpit-group-collapse' +
    ` aria-expanded="${layout.collapsed ? 'false' : 'true'}"` +
    disabled(layout.canCollapse) +
    `>${escapeHtml(layout.collapseLabel)}</button>` +
    '<button type="button" class="modern-btn cockpit-rail-maplayout-btn" data-cockpit-group-resize' +
    disabled(layout.canResize) +
    '>Resize group</button>' +
    '<button type="button" class="modern-btn cockpit-rail-maplayout-btn" data-cockpit-group-fit' +
    disabled(layout.canFit) +
    '>Fit to contents</button>' +
    '</div>' +
    appearanceHTML(layout) +
    (layout.readOnlyNote
      ? `<p class="cockpit-rail-empty">${escapeHtml(layout.readOnlyNote)}</p>`
      : '') +
    '<p class="cockpit-rail-note">Size and appearance change only how this group looks on your ' +
    'Map. They never change which workspaces are in it.</p>' +
    '</section>'
  );
}

/**
 * Named accent and theme choices (#346 FR-121 – FR-125, FR-130).
 *
 * Radio groups rather than unlabelled colour dots: the current choice is stated
 * in text, every option has a human name a screen reader can read, and the
 * theme options carry a one-line description of the shape difference — because
 * districts must stay distinguishable without relying on colour.
 */
function appearanceHTML(layout) {
  if (!layout.accents.length && !layout.themes.length) return '';
  const disabled = layout.canChangeAppearance ? '' : ' disabled';
  const group = (name, kind, options, current) =>
    `<fieldset class="cockpit-rail-appearance" data-rail-appearance="${escapeHtml(kind)}">` +
    `<legend class="cockpit-rail-appearance-legend">${escapeHtml(name)}</legend>` +
    options
      .map(option => {
        const checked = option.id === current ? ' checked' : '';
        const hint = option.hint
          ? ` <span class="cockpit-rail-appearance-hint">${escapeHtml(option.hint)}</span>`
          : '';
        return (
          `<label class="cockpit-rail-appearance-option cockpit-rail-${escapeHtml(kind)}-${escapeHtml(option.id)}">` +
          `<input type="radio" name="cockpit-district-${escapeHtml(kind)}" value="${escapeHtml(option.id)}"` +
          `${checked}${disabled} data-cockpit-group-${escapeHtml(kind)}>` +
          `<span class="cockpit-rail-appearance-label">${escapeHtml(option.label)}</span>${hint}` +
          '</label>'
        );
      })
      .join('') +
    '</fieldset>';

  return (
    group('Accent', 'accent', layout.accents, layout.accent) +
    group('Theme', 'theme', layout.themes, layout.theme) +
    (layout.customized
      ? '<button type="button" class="modern-btn cockpit-rail-maplayout-btn" ' +
        `data-cockpit-group-appearance-reset${disabled}>Use default appearance</button>`
      : '')
  );
}

/** Render the cross-workspace Summary rail state (FR89, FR90). */
export function renderSummaryRailHTML(view) {
  if (!view) return '';
  const listHTML = (title, rows, emptyCopy) =>
    '<section class="cockpit-rail-section">' +
    `<h4 class="cockpit-rail-section-title">${escapeHtml(title)}</h4>` +
    (rows.length
      ? '<ul class="cockpit-rail-roster">' +
        rows
          .slice(0, 5)
          .map(
            row =>
              '<li class="cockpit-rail-roster-row">' +
              `<a class="cockpit-rail-roster-name" href="#" data-cockpit-summary-select="${escapeHtml(row.id)}">${escapeHtml(row.name || 'Untitled workspace')}</a>` +
              '</li>'
          )
          .join('') +
        (rows.length > 5
          ? `<li class="cockpit-rail-roster-row"><span class="cockpit-rail-roster-role">+${rows.length - 5} more</span></li>`
          : '') +
        '</ul>'
      : `<p class="cockpit-rail-empty">${escapeHtml(emptyCopy)}</p>`) +
    '</section>';

  return (
    '<div class="cockpit-rail-panel" data-rail-panel="summary">' +
    '<header class="cockpit-rail-head">' +
    '<button type="button" class="cockpit-rail-back" data-cockpit-rail-back>' +
    '<span aria-hidden="true">&#8592;</span> Back</button>' +
    '<div class="cockpit-rail-identity">' +
    '<h3 class="cockpit-rail-title">Summary</h3>' +
    '<p class="cockpit-rail-badges"><span class="cockpit-rail-tag">All workspaces</span></p>' +
    '</div>' +
    '</header>' +
    '<div class="cockpit-rail-metrics" aria-label="Cross-workspace totals">' +
    metricHTML('Workspaces', view.workspaces) +
    metricHTML('Groups', view.groups) +
    metricHTML('Agents', view.agents) +
    '</div>' +
    '<div class="cockpit-rail-metrics" aria-label="Work totals">' +
    metricHTML('Open tasks', view.openTasks) +
    metricHTML('Attention', view.attention) +
    metricHTML('Due today', view.dueToday) +
    '</div>' +
    // Deliberately no "Needs attention" LIST here. Today — the default rail
    // state, zero clicks away — already lists the same workspaces from the same
    // state. Summary owns the cross-workspace *counts* plus the one thing Today
    // does not show at all: what is running right now. The Attention metric
    // above still carries the total.
    listHTML('Running now', view.runningWorkspaces, 'No workspace is running work.') +
    (view.upcomingCount === null
      ? '<p class="cockpit-rail-empty">Scheduled-work data is unavailable.</p>'
      : `<p class="cockpit-rail-note">${escapeHtml(
          view.upcomingCount === 1
            ? '1 upcoming scheduled run.'
            : `${view.upcomingCount} upcoming scheduled runs.`
        )}</p>`) +
    '</div>'
  );
}

/**
 * Render the workspace context rail.
 *
 * Order follows PRD §6 "Context-Rail Priority": identity and operational state,
 * recommended next move, open/ask actions, metrics, then the compact roster.
 */
export function renderWorkspaceRailHTML(view) {
  if (!view) return '';
  return (
    '<div class="cockpit-rail-panel" data-rail-panel="workspace">' +
    '<header class="cockpit-rail-head">' +
    '<button type="button" class="cockpit-rail-back" data-cockpit-rail-back>' +
    '<span aria-hidden="true">&#8592;</span> Back</button>' +
    '<div class="cockpit-rail-identity">' +
    `<h3 class="cockpit-rail-title">${escapeHtml(view.name)}</h3>` +
    '<p class="cockpit-rail-badges">' +
    `<span class="cockpit-rail-status is-${escapeHtml(view.status.status)}">` +
    // Status is carried by text, not by the color chip alone (FR131).
    '<span class="cockpit-rail-status-dot" aria-hidden="true"></span>' +
    escapeHtml(view.status.label) +
    '</span>' +
    (view.isPersonalHQ ? '<span class="cockpit-rail-tag">Personal HQ</span>' : '') +
    '</p>' +
    '</div>' +
    '</header>' +
    (view.mission ? `<p class="cockpit-rail-mission">${escapeHtml(view.mission)}</p>` : '') +
    '<section class="cockpit-rail-next" aria-label="Recommended next move">' +
    '<span class="cockpit-rail-next-kicker">Next move</span>' +
    `<p class="cockpit-rail-next-label">${escapeHtml(view.nextMove.label)}</p>` +
    (view.nextMove.detail
      ? `<p class="cockpit-rail-next-detail">${escapeHtml(view.nextMove.detail)}</p>`
      : '') +
    '</section>' +
    '<div class="cockpit-rail-actions">' +
    `<a class="modern-btn modern-btn-primary cockpit-rail-open" href="${escapeHtml(view.openHref)}" data-cockpit-rail-open data-workspace-id="${escapeHtml(view.id)}">Open Workspace</a>` +
    (view.canAskCommander
      ? `<button type="button" class="modern-btn modern-btn-secondary" data-cockpit-rail-ask data-workspace-id="${escapeHtml(view.id)}">Ask ${escapeHtml(view.commander)}</button>`
      : `<p class="cockpit-rail-note">${escapeHtml(view.commanderUnavailableReason)}</p>`) +
    '</div>' +
    '<div class="cockpit-rail-metrics" aria-label="Workspace metrics">' +
    metricHTML('Open tasks', view.status.openTasks) +
    metricHTML('Agents', view.status.agents) +
    metricHTML('Attention', view.status.attention) +
    '</div>' +
    '<section class="cockpit-rail-section" aria-label="Agent roster">' +
    '<h4 class="cockpit-rail-section-title">Roster</h4>' +
    rosterHTML(view.rosterState) +
    '</section>' +
    '</div>'
  );
}

// ---------------------------------------------------------------------------
// Today: immediate work (FR75, FR87)
// ---------------------------------------------------------------------------

/**
 * The workspaces that need attention, most-urgent first.
 *
 * Computed from the same shared state Map, Tree, and Summary read — Today does
 * not fetch workspaces again (FR111).
 */
export function attentionItems(flattened) {
  return (Array.isArray(flattened) ? flattened : [])
    .filter(ws => ws && !isGroupWorkspace(ws))
    .map(ws => ({ ws, count: workspaceSignals(ws).attention || 0 }))
    .filter(row => row.count > 0)
    .sort((a, b) => b.count - a.count)
    .map(row => ({ id: row.ws.id, name: row.ws.name || 'Untitled workspace', count: row.count }));
}

/**
 * Scheduled work due by the end of today, grouped by workspace.
 *
 * Returns `null` when the schedule source is unavailable, so Today can say so
 * rather than claim an empty schedule (FR121).
 */
export function scheduledTodayItems(flattened, scheduleIndex, now = new Date()) {
  if (!scheduleIndex) return null;
  const endOfDay = new Date(now);
  endOfDay.setHours(23, 59, 59, 999);
  const out = [];
  (Array.isArray(flattened) ? flattened : []).forEach(ws => {
    if (!ws || isGroupWorkspace(ws)) return;
    (scheduleIndex[ws.id] || []).forEach(row => {
      const at = new Date(String((row && (row.next_run || row.next_run_at)) || ''));
      if (Number.isNaN(at.getTime()) || at > endOfDay) return;
      out.push({
        workspaceId: ws.id,
        workspaceName: ws.name || 'Untitled workspace',
        taskName: String((row && row.task_name) || '').trim() || '(untitled task)',
        at,
        overdue: at < now
      });
    });
  });
  return out.sort((a, b) => a.at - b.at);
}

export function renderAttentionSectionHTML(items) {
  if (!items.length) return '';
  return (
    '<h3 class="cockpit-today-title">Needs attention</h3>' +
    '<ul class="cockpit-today-list">' +
    items
      .slice(0, 6)
      .map(
        item =>
          '<li class="cockpit-today-row is-attention">' +
          `<button type="button" class="cockpit-today-link" data-cockpit-select="${escapeHtml(item.id)}">` +
          `<span class="cockpit-today-name">${escapeHtml(item.name)}</span>` +
          `<span class="cockpit-today-count">${escapeHtml(String(item.count))} needing attention</span>` +
          '</button></li>'
      )
      .join('') +
    (items.length > 6 ? `<li class="cockpit-today-more">+${items.length - 6} more</li>` : '') +
    '</ul>'
  );
}

export function renderScheduledSectionHTML(items) {
  // null = the schedule source is unavailable; [] = genuinely nothing today.
  if (items === null) {
    return (
      '<h3 class="cockpit-today-title">Scheduled today</h3>' +
      '<p class="cockpit-today-empty">Scheduled-work data is unavailable right now.</p>'
    );
  }
  if (!items.length) return '';
  const time = at => at.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  return (
    '<h3 class="cockpit-today-title">Scheduled today</h3>' +
    '<ul class="cockpit-today-list">' +
    items
      .slice(0, 6)
      .map(
        item =>
          `<li class="cockpit-today-row${item.overdue ? ' is-overdue' : ''}">` +
          `<button type="button" class="cockpit-today-link" data-cockpit-select="${escapeHtml(item.workspaceId)}">` +
          `<span class="cockpit-today-name">${escapeHtml(item.taskName)}</span>` +
          `<span class="cockpit-today-count">${escapeHtml(item.workspaceName)} · ` +
          `${escapeHtml(item.overdue ? `overdue (${time(item.at)})` : time(item.at))}</span>` +
          '</button></li>'
      )
      .join('') +
    (items.length > 6 ? `<li class="cockpit-today-more">+${items.length - 6} more</li>` : '') +
    '</ul>'
  );
}

// ---------------------------------------------------------------------------
// Quick Capture (FR102, FR103, FR104)
// ---------------------------------------------------------------------------

/**
 * Validate a capture draft before it is sent.
 *
 * A title is required; details are optional. The draft itself is never
 * discarded here — the caller keeps it so a failed save can be retried with the
 * text intact (FR103).
 */
export function validateCapture(draft) {
  const title = String((draft && draft.title) || '').trim();
  if (!title) return { ok: false, message: 'Add a title before saving.' };
  return { ok: true, title, details: String((draft && draft.details) || '').trim() };
}

/**
 * The request body for a capture, using the EXISTING backlog contract:
 * POST /api/orchestration/backlog { workspace_id, description, details }.
 * There is no second inbox model (FR102).
 */
export function captureRequestBody(hqWorkspaceId, draft) {
  const valid = validateCapture(draft);
  return {
    workspace_id: hqWorkspaceId,
    description: valid.title || '',
    details: valid.details || '',
    source_type: 'home_quick_capture'
  };
}

/** What backlog capture can do right now, given the Personal HQ status. */
export function captureAvailability(hqStatus) {
  const valid = !!(hqStatus && hqStatus.valid && hqStatus.workspace_id);
  if (valid) return { canSave: true, hqWorkspaceId: hqStatus.workspace_id, message: '' };
  return {
    canSave: false,
    hqWorkspaceId: '',
    // FR104: explain the requirement and offer the existing HQ path, without
    // discarding whatever the user already typed.
    message: 'This saves to your Personal HQ backlog, and no Personal HQ is set up yet.'
  };
}

// ---------------------------------------------------------------------------
// Workspace-area states (FR110, FR112, FR113, FR114, FR120)
// ---------------------------------------------------------------------------

export function workspaceHydrationAllowed(onboardingGate) {
  return (
    onboardingGate?.state === ONBOARDING_GATE_READY &&
    onboardingGate?.allowWorkspaceHydration === true
  );
}

/**
 * Is the reserved Personal HQ blueprint site something the map can draw?
 *
 * Mirrors hqSiteView() in workspace-map.js, which is the authority: the site
 * shows exactly while a status has loaded and it is not a valid HQ (either
 * never built, or built and since broken). No status yet means nothing to draw.
 */
export function hqSiteVisible(hqStatus) {
  return !!hqStatus && !hqStatus.valid;
}

/**
 * Decide what the workspace area should show for a given load outcome.
 *
 * Loading, error, and empty are distinct states with distinct affordances —
 * FR120 forbids collapsing them, and FR113/FR114 require Retry and the
 * onboarding actions respectively.
 */
export function workspaceAreaState({
  loading,
  error,
  workspaces,
  onboardingGate,
  hqSiteVisible: hqSite = false
}) {
  if (onboardingGate?.state === ONBOARDING_GATE_LOADING) {
    return {
      state: 'onboarding-loading',
      message: 'Preparing your workspace setup…',
      canRetry: false
    };
  }
  if (onboardingGate?.state === ONBOARDING_GATE_REQUIRED) {
    return { state: 'onboarding-required', message: onboardingGate.message, canRetry: false };
  }
  if (onboardingGate?.state === ONBOARDING_GATE_UNAVAILABLE) {
    return {
      state: 'onboarding-unavailable',
      message: onboardingGate.message,
      detail: 'Workspace data remains hidden until Ori can verify setup status.',
      canRetry: true
    };
  }
  if (loading) return { state: 'loading', message: 'Loading workspaces…', canRetry: false };
  if (error) {
    return {
      state: 'error',
      message: 'Workspaces could not be loaded.',
      detail: String(error.message || error || '').slice(0, 200),
      canRetry: true
    };
  }
  if (!Array.isArray(workspaces)) {
    return {
      state: 'error',
      message: 'Workspaces could not be loaded.',
      detail: 'The workspace response was invalid.',
      canRetry: true
    };
  }
  if (workspaces.length === 0) {
    // Issue #320 narrows FR114: an authoritative empty list may render a real,
    // buildingless canvas because there is no operational state to misread.
    // The reserved Personal HQ site remains truthful content when available.
    return { state: 'empty-map', message: '', canRetry: false, hqSiteVisible: hqSite };
  }
  return { state: 'ready', message: '', canRetry: false };
}

export function renderWorkspaceAreaStatusHTML(status) {
  if (!status || status.state === 'ready' || status.state === 'empty-map') return '';
  if (status.state === 'onboarding-loading' || status.state === 'onboarding-required') {
    return `<div class="cockpit-area-message" data-state="${status.state}">${escapeHtml(status.message)}</div>`;
  }
  if (status.state === 'onboarding-unavailable') {
    return (
      '<div class="cockpit-area-message" data-state="onboarding-unavailable">' +
      `<p class="cockpit-area-message-text">${escapeHtml(status.message)}</p>` +
      `<p class="cockpit-area-message-detail">${escapeHtml(status.detail)}</p>` +
      '<button type="button" class="modern-btn modern-btn-secondary" data-cockpit-onboarding-retry>Retry</button>' +
      '</div>'
    );
  }
  if (status.state === 'loading') {
    return `<div class="cockpit-area-message" data-state="loading">${escapeHtml(status.message)}</div>`;
  }
  if (status.state === 'error') {
    return (
      '<div class="cockpit-area-message" data-state="error">' +
      `<p class="cockpit-area-message-text">${escapeHtml(status.message)}</p>` +
      (status.detail
        ? `<p class="cockpit-area-message-detail">${escapeHtml(status.detail)}</p>`
        : '') +
      '<button type="button" class="modern-btn modern-btn-secondary" data-cockpit-retry>Retry</button>' +
      '</div>'
    );
  }
  return '';
}

// ---------------------------------------------------------------------------
// DOM wiring — no-ops without a document, so the exports above stay testable.
// ---------------------------------------------------------------------------

// Tree is a peer VIEW owned by this coordinator: it renders and handles local
// interaction, and hands every mutation back here so shared state, refresh, and
// rail context stay single-authority (FR117).
import { mountTree, ancestorIds } from './home-workspace-tree.js';
import {
  createGroupFrom as createGroupAction,
  deleteWorkspace as deleteWorkspaceAction,
  deleteWorkspaces as deleteWorkspacesAction
} from './workspace-bulk-actions.js';
import {
  ONBOARDING_GATE_LOADING,
  ONBOARDING_GATE_REQUIRED,
  ONBOARDING_GATE_READY,
  ONBOARDING_GATE_UNAVAILABLE,
  loadInitialWorkspaceTree,
  loadOnboardingStatus,
  onboardingGateDecision
} from './onboarding-gate.js';

(function () {
  if (typeof document === 'undefined' || typeof window === 'undefined') return;

  const root = document.getElementById('homeCockpit');
  if (!root) return; // Not the Home page.

  const els = {
    root,
    map: document.getElementById('cockpitMap'),
    tree: document.getElementById('cockpitTree'),
    areaTitle: document.querySelector('[data-cockpit-area-title]'),
    areaStatus: document.getElementById('cockpitWorkspaceStatus'),
    viewButtons: Array.from(document.querySelectorAll('[data-cockpit-view]')),
    filters: document.getElementById('cockpitSignalFilters'),
    rail: document.getElementById('cockpitRail'),
    railContext: document.getElementById('cockpitRailContext'),
    railLive: document.getElementById('cockpitRailLive'),
    summaryBtn: document.getElementById('cockpitSummaryBtn'),
    // Updates: the header trigger keeps the retired "Today" toggle's id for
    // compatibility (PRD FR2); its flyout and body are new (Issue #334).
    railToggle: document.getElementById('cockpitRailToggle'),
    railToggleCount: document.querySelector('[data-cockpit-rail-toggle-count]'),
    updatesFlyout: document.getElementById('cockpitUpdatesFlyout'),
    updatesBody: document.getElementById('cockpitUpdatesFlyoutBody'),
    updatesClose: document.querySelector('#cockpitUpdatesFlyout [data-cockpit-flyout-close]'),
    todayAttention: document.getElementById('cockpitTodayAttention'),
    todayScheduled: document.getElementById('cockpitTodayScheduled'),
    // Quests: the flyout host exists from Group 1 (its content is
    // progression-widget.js's own DOM); the trigger button lands in Group 2.
    questsToggle: document.getElementById('cockpitQuestsToggle'),
    questsFlyout: document.getElementById('cockpitQuestsFlyout'),
    questsClose: document.querySelector('#cockpitQuestsFlyout [data-cockpit-flyout-close]'),
    captureBtn: document.getElementById('cockpitCaptureBtn'),
    capturePanel: document.getElementById('cockpitCapturePanel'),
    captureForm: document.getElementById('cockpitCaptureForm'),
    captureTitle: document.getElementById('cockpitCaptureTitle'),
    captureDetails: document.getElementById('cockpitCaptureDetails'),
    captureSave: document.getElementById('cockpitCaptureSave'),
    captureCancel: document.getElementById('cockpitCaptureCancel'),
    captureStatus: document.getElementById('cockpitCaptureStatus'),
    askPanel: document.getElementById('homeAssistantThinkingModal'),
    askTarget: document.getElementById('cockpitAskTarget')
  };

  // ---- shared state (the single source of truth for every cockpit view) ----
  //
  // FR111/FR117: every view reads from here, and every mutation goes through
  // applyRefresh() before dependent views rerender. No view fetches for itself.
  const state = {
    tree: [],
    flattened: [],
    metadata: { folderDisplayById: {}, tagsById: {}, groupPreviewById: {} },
    // null (not {}) until the schedule source resolves, so "no scheduled work"
    // and "we don't know" stay distinguishable (FR120, FR121).
    scheduleIndex: null,
    scheduleError: null,
    view: parseViewFromQuery(window.location.search),
    signal: '',
    selectedId: '',
    railState: RAIL_TODAY,
    // Whether the CONTEXT rail is open is now fully derived from railState: a
    // real selection (workspace/group/HQ/Summary/Ask Ori) opens it, Today
    // always leaves it closed (Issue #334 — Today's content no longer lives
    // in the rail, so there is nothing left to manually show there).
    //
    // The header's own transient disclosures (Updates/Quests/Quick Capture)
    // are a SEPARATE piece of state, `panel` below — see applyPanelState()
    // and applyPanelState().
    panel: PANEL_NONE,
    hqSiteView: null,
    // The workspace/group context to restore when Summary or Ask Ori closes
    // (FR91, FR100).
    priorContext: null,
    // Tree management state. Deliberately session-scoped and deliberately NOT
    // cleared by selection changes or by returning to Today (FR55, FR61).
    collapsedGroups: new Set(),
    bulkSelection: new Set(),
    activeTags: new Set(),
    focusId: '',
    draggingId: '',
    workspaceRoot: { state: 'loading', path: '', custom: false },
    undoStack: [],
    // Personal HQ status, read once and refreshed on HQ actions. Quick Capture
    // needs it to know where a capture goes (FR102/FR104).
    hqStatus: null,
    onboardingGate: {
      state: ONBOARDING_GATE_LOADING,
      allowWorkspaceHydration: false,
      message: 'Preparing your workspace setup…'
    },
    loading: true,
    error: null,
    inFlight: null
  };

  function announce(message) {
    if (!els.railLive || !message) return;
    els.railLive.textContent = message;
  }

  // ---- workspace area ----

  function renderAreaStatus() {
    const status = workspaceAreaState({
      loading: state.loading,
      error: state.error,
      workspaces: state.flattened,
      onboardingGate: state.onboardingGate,
      hqSiteVisible: hqSiteVisible(state.hqStatus)
    });
    if (els.areaStatus) {
      els.areaStatus.innerHTML = renderWorkspaceAreaStatusHTML(status);
      const retry = els.areaStatus.querySelector('[data-cockpit-retry]');
      if (retry) retry.addEventListener('click', () => refresh());
      const onboardingRetry = els.areaStatus.querySelector('[data-cockpit-onboarding-retry]');
      if (onboardingRetry) {
        onboardingRetry.addEventListener(
          'click',
          () => void initializeWorkspaceHydration({ force: true })
        );
      }
    }
    root.dataset.state = status.state;
    // An authoritative empty list is truthful map state: render the canvas,
    // while every pending, failed, or malformed state continues to fail closed.
    const mapRenderable = status.state === 'ready' || status.state === 'empty-map';
    if (els.map) els.map.hidden = state.view !== VIEW_MAP || !mapRenderable;
    if (els.filters) els.filters.hidden = status.state !== 'ready' || state.view !== VIEW_MAP;
    return status;
  }

  // ---- signal filters (FR31-FR34) ----

  // Hiding the chip row when every count is a known zero was tried and dropped:
  // the chips are the ONLY way to reach a signal filter, so hiding them at zero
  // makes filtering unreachable in exactly the fixture where tests (and users
  // arriving at a quiet Home) go looking for it. Three compact chips in the map
  // header are a much smaller cost than a control that comes and goes.

  function renderFilters() {
    if (!els.filters) return;
    const counts = signalCounts(state.flattened, state.scheduleIndex);
    els.filters.innerHTML = renderSignalFiltersHTML(counts, state.signal);
    els.filters.querySelectorAll('[data-cockpit-signal]').forEach(btn => {
      btn.addEventListener('click', () => {
        // Single-select: clicking the active chip clears it (FR31, FR33).
        const next = btn.getAttribute('data-cockpit-signal');
        applySignal(next === state.signal ? '' : next);
      });
    });
  }

  function applySignal(signal) {
    state.signal = parseSignal(signal);
    renderFilters();
    applyFilterToMap();
    const counts = signalCounts(state.flattened, state.scheduleIndex);
    announce(filterResultMessage(state.signal, state.signal ? counts[state.signal] : null));
  }

  /**
   * De-emphasize non-matching map sites in place.
   *
   * The map is not re-mounted: filtering must not disturb selection, scroll, or
   * the site layout, so a site keeps its position and simply dims (FR33).
   */
  function applyFilterToMap() {
    if (!els.map) return;
    const matching = filterWorkspaceIds(state.flattened, state.signal, state.scheduleIndex);
    els.map.querySelectorAll('.ws-map-tile[data-ws-id]').forEach(tile => {
      const id = tile.getAttribute('data-ws-id');
      const dimmed = matching !== null && !matching.includes(id);
      tile.classList.toggle('is-filtered-out', dimmed);
      // Keep assistive technology in step with the visual de-emphasis.
      if (dimmed) tile.setAttribute('data-filtered-out', 'true');
      else tile.removeAttribute('data-filtered-out');
    });
    els.map.dataset.signal = state.signal || '';
  }

  function mountMap() {
    if (!els.map || state.view !== VIEW_MAP) return;
    if (!window.OriWorkspaceMap) return;
    window.OriWorkspaceMap.mount(els.map, {
      workspaces: state.flattened,
      tree: state.tree,
      selectedId: state.selectedId,
      metadata: state.metadata,
      // Cockpit contract (see workspace-map.js): select-only pointer semantics,
      // no internal topbar/overview chrome, and no invented default selection.
      selectOnly: true,
      hideChrome: true,
      noAutoSelect: true,
      // Explicit Home-only contract. The shared Map keeps its legacy empty
      // prompt unless its cockpit host opts into a real buildingless canvas.
      emptyPresentation: state.flattened.length === 0 ? 'canvas' : 'legacy',
      onSelect: id => selectItem(id, { fromMap: true }),
      onOpen: id => openItem(id),
      onSelectHQSite: view => selectHQSite(view),
      // Delete and group. The map owns no mutation of its own — it hands the
      // ids back here, and these run the very same workspace-bulk-actions.js
      // code Tree runs, so the two views cannot diverge.
      onDeleteWorkspace: id => void deleteWorkspaceAction(id, bulkContext()),
      onDeleteWorkspaces: ids => void deleteWorkspacesAction(ids, bulkContext()),
      onGroupWorkspaces: ids => void groupFromMap(ids),
      // A committed resize or fit changes what the rail's Map layout section
      // says about this group, so the rail re-renders from the Map's new
      // snapshot rather than keeping stale copy (#346 FR-152).
      onDistrictChanged: groupId => {
        if (groupId && groupId === state.selectedId) renderRail({ announceChange: false });
      }
    });
    // A re-mount rebuilds the tiles, so the active filter must be reapplied.
    applyFilterToMap();
    // The map resolves the effective selection during mount: it may have
    // dropped one whose workspace no longer exists, or preselected the reserved
    // HQ site because the URL carried an explicit focus intent.
    const resolved = window.OriWorkspaceMap.getSelectedId
      ? window.OriWorkspaceMap.getSelectedId()
      : state.selectedId;
    if (resolved === state.selectedId) return;
    if (resolved === HQ_SITE_ID) {
      selectHQSite(
        window.OriWorkspaceMap.getHQSiteView ? window.OriWorkspaceMap.getHQSiteView() : null
      );
      return;
    }
    if (!resolved) {
      // The map dropped our selection, which only happens when that workspace
      // no longer exists. Clearing it silently here would rob renderRail of the
      // chance to say so, leaving a screen-reader user with a stale "X
      // selected" announcement and no explanation (FR73). Leave selectedId in
      // place and let renderRail detect the missing item and announce it.
      renderRail({ announceChange: false });
      return;
    }
    // The map resolved a selection we did not ask for — the only way that
    // happens is an explicit focus intent in the URL (`?focus=personal-hq`).
    // Route it through the normal selection path so the rail actually shows
    // that workspace's context instead of staying on Today (FR107).
    selectItem(resolved, { fromMap: true });
  }

  // ---- Tree peer view ----

  function mountTreeView() {
    if (!els.tree || state.view !== VIEW_TREE) return;
    // Re-mounting replaces the rows, which would drop keyboard focus on the
    // floor mid-navigation. Restore it afterwards when it was ours to begin
    // with, and never steal it when it was not (FR129).
    const hadFocus = !!(document.activeElement && els.tree.contains(document.activeElement));
    mountTree(els.tree, state, {
      onSelect: id => selectItem(id),
      onOpen: id => openItem(id),
      onRerender: () => mountTreeView(),
      onAnnounce: message => announce(message),
      onTrashed: (id, name) => {
        state.undoStack.push({ id, name });
      },
      onUndo: () => undoLastDelete(),
      onChanged: async () => {
        await refreshQuietly();
      }
    });
    if (hadFocus) {
      const target =
        els.tree.querySelector('[data-tree-row][tabindex="0"]') ||
        els.tree.querySelector('[data-tree-row]');
      if (target) target.focus();
    }
  }

  /**
   * Host context for workspace-bulk-actions.js.
   *
   * Map has no mutation code of its own, so it calls back into here; Tree builds
   * the equivalent context itself. Both feed the same undo stack, so an item
   * trashed from either view is restorable by the same Undo (FR52).
   */
  /**
   * Group a Map selection (#346 FR-13 – FR-22, FR-28).
   *
   * The hierarchy mutation is the same shared one Tree runs — this only adds the
   * Map's before and after. Before: pin the coordinates of any selected
   * workspace that has never been placed by hand, so grouping cannot make it
   * jump to a different automatic cell. After: select and frame the district the
   * hierarchy actually produced, using the members it actually placed.
   */
  async function groupFromMap(ids) {
    const map = window.OriWorkspaceMap;
    const anchors =
      map && typeof map.captureGroupingAnchors === 'function'
        ? map.captureGroupingAnchors(ids)
        : {};

    const outcome = await createGroupAction(ids, bulkContext());
    if (!outcome || !outcome.groupId) return;

    // createGroupAction has already refreshed shared state, so the new group is
    // in state.flattened and the Map can resolve its district.
    if (map && typeof map.adoptNewGroup === 'function') {
      const pinned = {};
      // Only the members the hierarchy really placed changed islands; a member
      // that failed to move is still where it was (FR-28).
      outcome.placed.forEach(id => {
        if (anchors[id]) pinned[id] = anchors[id];
      });
      await map.adoptNewGroup(outcome.groupId, pinned);
    } else if (map && typeof map.clearMultiSelection === 'function') {
      map.clearMultiSelection();
    }

    // One selection change, through the one shared path, so the Map highlight
    // and the context rail cannot disagree (FR-21).
    selectItem(outcome.groupId);
  }

  function bulkContext() {
    return {
      rows: state.flattened,
      announce,
      onTrashed: (id, name) => {
        state.undoStack.push({ id, name });
      },
      onChanged: async () => {
        await refreshQuietly();
      }
    };
  }

  /** Restore the most recent trashed item (FR52 session Undo). */
  async function undoLastDelete() {
    const entry = state.undoStack.pop();
    if (!entry) {
      announce('Nothing to undo.');
      return;
    }
    try {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(entry.id)}/restore`, {
        method: 'POST'
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      announce(`Restored ${entry.name}.`);
    } catch (err) {
      // Put it back so the user can retry rather than silently losing the undo.
      state.undoStack.push(entry);
      announce(`Could not restore ${entry.name}.`);
      if (window.Toast) window.Toast.error(`Could not restore "${entry.name}".`);
      console.error('home-workspace-cockpit: restore failed', err);
    }
    await refreshQuietly();
  }

  /**
   * The configured workspace root, shown in the Tree toolbar (FR39).
   *
   * The endpoint reports `effective_workspace_root` (empty until the user has
   * confirmed a location), `default_workspace_root`, and a `source` of
   * `unconfirmed` / `default` / `custom`. Tree shows which of those it is
   * rather than presenting the fallback as if it were a confirmed setting.
   */
  async function refreshWorkspaceRoot() {
    if (!canHydrateWorkspaceData()) return;
    try {
      const res = await fetch('/api/settings/workspace-root');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      const effective = String(data.effective_workspace_root || '').trim();
      const fallback = String(data.default_workspace_root || '').trim();
      const source = String(data.source || '').trim();
      state.workspaceRoot = {
        state: 'ready',
        path: effective || fallback,
        custom: source === 'custom',
        confirmed: data.confirmed !== false,
        source
      };
    } catch (_) {
      // Honest unavailable state rather than an invented path (FR39/FR121).
      state.workspaceRoot = { state: 'unavailable', path: '', custom: false };
    }
    if (state.view === VIEW_TREE) mountTreeView();
  }

  // ---- view state ----

  function applyView(view, { pushUrl = true } = {}) {
    state.view = view === VIEW_TREE ? VIEW_TREE : VIEW_MAP;
    els.viewButtons.forEach(btn => {
      const isActive = btn.getAttribute('data-cockpit-view') === state.view;
      btn.setAttribute('aria-pressed', isActive ? 'true' : 'false');
      btn.classList.toggle('is-active', isActive);
    });
    if (els.areaTitle) {
      els.areaTitle.textContent = state.view === VIEW_TREE ? 'Workspace Tree' : 'Workspace Map';
    }
    // Exactly one peer view is exposed at a time (FR18/FR19).
    if (els.tree) els.tree.hidden = state.view !== VIEW_TREE;
    root.dataset.view = state.view;
    // Entering Tree with something selected on the Map must REVEAL that row by
    // expanding its ancestors — without mutating stored hierarchy (FR59).
    if (state.view === VIEW_TREE && state.selectedId) {
      ancestorIds(state.flattened, state.selectedId).forEach(id =>
        state.collapsedGroups.delete(id)
      );
      state.focusId = state.selectedId;
    }
    if (pushUrl) {
      // replaceState, never pushState: a view toggle must not add a history
      // entry (FR13/FR26).
      const next = window.location.pathname + searchForView(window.location.search, state.view);
      window.history.replaceState(window.history.state, '', next);
    }
    renderAreaStatus();
    renderFilters();
    mountMap();
    mountTreeView();
    // Some rail content belongs to ONE view. The selected group's Map layout
    // section is Map-only: left standing in Tree it would offer to resize and
    // collapse rows that do nothing of the kind (#346 FR-154). Re-rendering on
    // every view change is what keeps the rail's claims true for the view the
    // user is actually in.
    if (state.railState === RAIL_GROUP) renderRail({ announceChange: false });
    // The footer shortcut names the OTHER view, so it has to follow every view
    // change — not just the ones that re-render the rail (FR88).
    updateRailFooter();
  }

  // ---- selection + rail ----

  /**
   * Select a workspace OR a group — both share one active-item state, so Map,
   * Tree, and the rail can never disagree about what is selected (FR57, FR58).
   */
  function selectItem(id, { fromMap = false } = {}) {
    const next = id || '';
    const changed = next !== state.selectedId;
    const item = findWorkspace(state.flattened, next);
    state.selectedId = next;
    state.railState = next ? (isGroupWorkspace(item) ? RAIL_GROUP : RAIL_WORKSPACE) : RAIL_TODAY;
    state.hqSiteView = null;
    if (next) state.priorContext = { selectedId: next, railState: state.railState };
    if (!fromMap && els.map && window.OriWorkspaceMap && window.OriWorkspaceMap.setSelectedId) {
      window.OriWorkspaceMap.setSelectedId(els.map, state.flattened, next, {
        metadata: state.metadata
      });
    }
    renderRail({ announceChange: changed });
    // Both peer views must show the same active item, whichever one changed it
    // (FR57, FR58). The map is updated in place above; Tree re-renders so the
    // active row's highlight and aria-selected follow.
    if (state.view === VIEW_TREE) mountTreeView();
    publishRouteContext();
    subscribeRealtimeTo(next);
    if (changed && next) fireTTFA(state.view === VIEW_TREE ? 'tree-select' : 'map-select');
  }

  /**
   * Return to Today.
   *
   * Clearing the active item must NOT clear Map signal filters or Tree
   * management state — those are separate, deliberately sticky concerns
   * (FR61). Today has no rail content of its own now (Issue #334), so
   * returning to it always closes the context rail (FR45).
   */
  function clearSelection() {
    if (!state.selectedId && state.railState === RAIL_TODAY) return;
    state.hqSiteView = null;
    state.priorContext = null;
    selectItem('');
  }

  /**
   * Select the reserved Personal HQ site.
   *
   * The map normally renders the HQ's build/repair/skip choices inside its own
   * overview panel, which cockpit mode suppresses — so without this the site
   * would highlight with no affordance at all. The rail renders the SAME markup
   * the map produces and re-dispatches the same `ori:personal-hq-action` event
   * personal-hq-onboarding.js already listens for, so there is still exactly one
   * HQ provisioning UI (PRD FR28, FR79, FR115).
   */
  function selectHQSite(view) {
    state.selectedId = HQ_SITE_ID;
    state.railState = RAIL_WORKSPACE;
    state.hqSiteView = view || null;
    renderRail({ announceChange: true });
  }

  /** Show the cross-workspace Summary, remembering what to come back to. */
  function showSummary() {
    if (state.railState !== RAIL_SUMMARY) {
      state.priorContext =
        state.selectedId && state.railState !== RAIL_TODAY
          ? { selectedId: state.selectedId, railState: state.railState }
          : null;
    }
    state.railState = RAIL_SUMMARY;
    renderRail({ announceChange: true });
  }

  /**
   * Leave Summary.
   *
   * Restores the prior workspace/group context when it is still valid, else
   * Today — never a stale reference to something that has since been deleted
   * (FR91).
   */
  function leaveSummary() {
    const prior = state.priorContext;
    if (prior && findWorkspace(state.flattened, prior.selectedId)) {
      state.railState = prior.railState;
      state.selectedId = prior.selectedId;
      renderRail({ announceChange: true });
      return;
    }
    state.selectedId = '';
    state.railState = RAIL_TODAY;
    state.priorContext = null;
    renderRail({ announceChange: true });
  }

  /** The Map's snapshot of one district, or null when the Map cannot say. */
  function mapDistrictView(groupId) {
    const map = window.OriWorkspaceMap;
    if (!map || typeof map.getDistrictView !== 'function') return null;
    return map.getDistrictView(groupId);
  }

  /**
   * The rail's Map-layout controls (#346 FR-151, FR-156).
   *
   * They call the Map's own district action controller — the same one the
   * district context menu calls — so the rail cannot develop its own validation,
   * persistence, or failure behaviour.
   */
  function bindMapLayoutActions() {
    const actions = (window.OriWorkspaceMap && window.OriWorkspaceMap.districtActions) || null;
    if (!actions) return;
    const resize = els.railContext.querySelector('[data-cockpit-group-resize]');
    if (resize) {
      resize.addEventListener('click', () => actions.resize(state.selectedId));
    }
    const fit = els.railContext.querySelector('[data-cockpit-group-fit]');
    if (fit) {
      fit.addEventListener('click', () =>
        Promise.resolve(actions.fitToContents(state.selectedId)).then(() =>
          renderRail({ announceChange: false })
        )
      );
    }
    const collapse = els.railContext.querySelector('[data-cockpit-group-collapse]');
    if (collapse) {
      collapse.addEventListener('click', () =>
        Promise.resolve(
          actions.setCollapsed(state.selectedId, collapse.getAttribute('aria-expanded') === 'true')
        ).then(() => renderRail({ announceChange: false }))
      );
    }
    // Appearance: only an identifier from the rendered catalog can reach the
    // action, so nothing a hostile value could be smuggled through (#346
    // FR-125).
    ['accent', 'theme'].forEach(kind => {
      els.railContext.querySelectorAll(`[data-cockpit-group-${kind}]`).forEach(input =>
        input.addEventListener('change', () => {
          if (!input.checked) return;
          void Promise.resolve(
            actions.setAppearance(state.selectedId, { [kind]: input.value })
          ).then(() => renderRail({ announceChange: false }));
        })
      );
    });
    const resetAppearance = els.railContext.querySelector('[data-cockpit-group-appearance-reset]');
    if (resetAppearance) {
      resetAppearance.addEventListener('click', () =>
        Promise.resolve(actions.resetAppearance(state.selectedId)).then(() =>
          renderRail({ announceChange: false })
        )
      );
    }
  }

  function bindRailCommon() {
    if (!els.railContext) return;
    bindMapLayoutActions();
    const back = els.railContext.querySelector('[data-cockpit-rail-back]');
    if (back) {
      back.addEventListener('click', () =>
        state.railState === RAIL_SUMMARY ? leaveSummary() : clearSelection()
      );
    }
    const open = els.railContext.querySelector('[data-cockpit-rail-open]');
    if (open) open.addEventListener('click', () => fireTTFA('open-workspace'));
    els.railContext.querySelectorAll('[data-cockpit-summary-select]').forEach(link =>
      link.addEventListener('click', e => {
        e.preventDefault();
        selectItem(link.getAttribute('data-cockpit-summary-select'));
      })
    );
    els.railContext.querySelectorAll('[data-hq-action]').forEach(el =>
      el.addEventListener('click', () =>
        window.dispatchEvent(
          new CustomEvent('ori:personal-hq-action', {
            detail: { action: el.getAttribute('data-hq-action') }
          })
        )
      )
    );
  }

  // ---- rail openness (derived) ----
  //
  // The context rail is not entitled to a third of the window for saying "a
  // quiet day" (Issue #334). It earns its space for exactly one reason now:
  // something other than Today is showing — a workspace, group, the HQ site,
  // Summary, or Ask Ori. A bare Today never opens it, and neither does
  // workspace attention or Progression state; those live in Updates/Quests,
  // which are their own overlays entirely outside this rail (FR41-FR47).

  function applyRailOpen() {
    const open = contextRailShouldBeOpen(state.railState);
    root.dataset.railOpen = open ? 'true' : 'false';
    if (els.rail) els.rail.hidden = !open;
  }

  // ---- Updates badge (FR15-FR17) ----
  //
  // Purely informational: the same aggregate attention count the rail used to
  // carry, still hidden at zero, but it never opens anything on its own now —
  // opening Updates is the trigger button's job alone (applyPanelState).

  function updateUpdatesBadge() {
    if (!els.railToggleCount) return;
    const badge = updatesBadgeView(state.flattened, state.scheduleIndex);
    els.railToggleCount.textContent = badge.visible ? String(badge.count) : '';
    els.railToggleCount.hidden = !badge.visible;
  }

  // ---- header disclosure panel: Updates / Quests / Quick Capture ----
  //
  // One explicit `state.panel` value (see PANEL_* / togglePanelState above)
  // drives all three header-owned transient disclosures, so they can never
  // disagree about which one is open (FR7-FR9). Deliberately independent of
  // `railState`/contextRailShouldBeOpen(): a selected workspace and an open
  // flyout coexist without disturbing each other (FR22, FR29, FR41-FR46).

  function applyPanelState() {
    const openUpdates = state.panel === PANEL_UPDATES;
    const openQuests = state.panel === PANEL_QUESTS;
    const openCapture = state.panel === PANEL_CAPTURE;

    if (els.railToggle) {
      els.railToggle.setAttribute('aria-expanded', openUpdates ? 'true' : 'false');
      els.railToggle.setAttribute('aria-label', openUpdates ? 'Hide Updates' : 'Show Updates');
    }
    if (els.updatesFlyout) els.updatesFlyout.hidden = !openUpdates;

    if (els.questsToggle) {
      els.questsToggle.setAttribute('aria-expanded', openQuests ? 'true' : 'false');
      els.questsToggle.setAttribute('aria-label', openQuests ? 'Hide Quests' : 'Show Quests');
    }
    if (els.questsFlyout) els.questsFlyout.hidden = !openQuests;

    if (els.captureBtn)
      els.captureBtn.setAttribute('aria-expanded', openCapture ? 'true' : 'false');
    if (els.capturePanel) els.capturePanel.hidden = !openCapture;
  }

  function focusPanelTrigger(panel) {
    const id = panelTriggerId(panel);
    const trigger = id && document.getElementById(id);
    if (trigger) trigger.focus();
  }

  /** Open `target`, idempotently — calling it again while already open just refocuses. */
  function openPanel(target, { focus = true } = {}) {
    if (state.panel === target) {
      if (focus && target === PANEL_CAPTURE && els.captureTitle) els.captureTitle.focus();
      return;
    }
    state.panel = target;
    applyPanelState();
    if (target === PANEL_CAPTURE) {
      refreshCaptureAvailability();
      if (focus && els.captureTitle) els.captureTitle.focus();
    }
  }

  /** Close whichever panel is open, returning focus to its trigger. */
  function closePanel({ focus = true } = {}) {
    const prior = state.panel;
    if (prior === PANEL_NONE) return;
    state.panel = PANEL_NONE;
    applyPanelState();
    if (focus) focusPanelTrigger(prior);
  }

  /** Activating a trigger: open it, or close it if it is the one already open (FR7). */
  function togglePanel(target) {
    if (togglePanelState(state.panel, target) === PANEL_NONE) closePanel();
    else openPanel(target);
  }

  function renderRail({ announceChange = true } = {}) {
    if (!els.railContext) return;

    // Summary is a rail STATE, not a separate page or tab (FR89-FR91).
    if (state.railState === RAIL_SUMMARY) {
      showContextPanel(renderSummaryRailHTML(summaryView(state.flattened, state.scheduleIndex)));
      if (announceChange) announce('Summary of all workspaces.');
      return;
    }

    // The reserved HQ site is a landmark, not a workspace, so it is resolved
    // before the workspace lookup.
    if (state.selectedId === HQ_SITE_ID && state.railState !== RAIL_TODAY) {
      showContextPanel(
        '<div class="cockpit-rail-panel" data-rail-panel="personal-hq">' +
          '<header class="cockpit-rail-head">' +
          '<button type="button" class="cockpit-rail-back" data-cockpit-rail-back>' +
          '<span aria-hidden="true">&#8592;</span> Back</button>' +
          '</header>' +
          (window.OriWorkspaceMap ? window.OriWorkspaceMap.hqOverviewHTML(state.hqSiteView) : '') +
          '</div>'
      );
      if (announceChange) announce('Personal HQ site selected.');
      return;
    }

    const item = findWorkspace(state.flattened, state.selectedId);
    if (state.railState === RAIL_TODAY || !item) {
      if (state.selectedId) {
        // The selected item vanished under us (deleted or now inaccessible):
        // return to Today and say so rather than showing a stale rail (FR73).
        // The rail itself still closes (Today has no rail content of its own
        // now — FR42/FR45); `announce()` writes to #cockpitRailLive, which
        // lives OUTSIDE the rail specifically so this notice is still heard
        // by assistive tech even though its container just closed.
        state.selectedId = '';
        state.railState = RAIL_TODAY;
        state.priorContext = null;
        showTodayPanel();
        announce('The selected workspace is no longer available. Returned to the workspace map.');
        return;
      }
      showTodayPanel();
      if (announceChange) announce('Selection cleared. Returned to the workspace map.');
      return;
    }

    showContextPanel(
      isGroupWorkspace(item)
        ? renderGroupRailHTML(
            groupRailView(item, state.flattened, {
              view: state.view,
              // The Map owns district state; the rail reads a snapshot of it
              // rather than keeping a second copy that could drift (#346).
              district: mapDistrictView(item.id)
            })
          )
        : renderWorkspaceRailHTML(workspaceRailView(item))
    );
    if (announceChange) announce(`${item.name} selected.`);
  }

  /**
   * Swap the rail to its context panel.
   *
   * Today has no rail content of its own anymore (Issue #334 moved it into
   * Updates/Quests), so this only ever shows/hides the one context panel.
   */
  function showContextPanel(html) {
    els.railContext.hidden = false;
    els.railContext.innerHTML = html;
    bindRailCommon();
    updateRailFooter();
    applyRailOpen();
  }

  function showTodayPanel() {
    els.railContext.hidden = true;
    els.railContext.innerHTML = '';
    updateRailFooter();
    applyRailOpen();
  }

  function updateRailFooter() {
    if (els.summaryBtn) {
      const inSummary = state.railState === RAIL_SUMMARY;
      els.summaryBtn.setAttribute('aria-pressed', inSummary ? 'true' : 'false');
      els.summaryBtn.textContent = inSummary ? 'Close summary' : 'Summary';
    }
  }

  // ---- Ask Ori rail activity (FR92-FR100) ----
  //
  // dashboard.js owns the routing, planning, confirmation, and recovery logic
  // and drives the panel's `show` class directly. The cockpit only reacts to
  // that class: it hides the competing rail panels while activity is showing,
  // restores the prior context afterwards, and keeps the target-workspace line
  // honest. No routing decision is made here.

  let askWasActive = false;

  function isAskActive() {
    return !!(els.askPanel && els.askPanel.classList.contains('show'));
  }

  function syncAskActivity() {
    const active = isAskActive();
    if (active === askWasActive) {
      if (active) renderAskTarget();
      return;
    }
    askWasActive = active;
    if (active) {
      // Remember what to come back to (FR100), then yield the rail.
      if (state.railState !== RAIL_ASK) {
        state.priorContext =
          state.selectedId && state.railState !== RAIL_TODAY
            ? { selectedId: state.selectedId, railState: state.railState }
            : null;
      }
      state.railState = RAIL_ASK;
      if (els.railContext) els.railContext.hidden = true;
      // Ask Ori activity is never worth showing in a rail the user cannot see.
      applyRailOpen();
      renderAskTarget();
      announce('Ask Ori activity.');
      return;
    }
    restoreFromAsk();
  }

  /** Leaving Ask Ori returns to the previous workspace/group context or Today. */
  function restoreFromAsk() {
    const prior = state.priorContext;
    if (prior && findWorkspace(state.flattened, prior.selectedId)) {
      state.selectedId = prior.selectedId;
      state.railState = prior.railState;
    } else {
      state.railState = RAIL_TODAY;
    }
    renderRail({ announceChange: true });
  }

  function renderAskTarget() {
    if (!els.askTarget) return;
    const selected = findWorkspace(state.flattened, state.selectedId);
    // Only the selected workspace is known here; a recommendation or a routed
    // target is dashboard.js's to report through its own routing summary, which
    // renders directly beneath this line.
    const description = askTargetDescription({ selected });
    els.askTarget.textContent = description.text;
    els.askTarget.dataset.state = description.state;
  }

  /**
   * Offer the selected workspace as route CONTEXT.
   *
   * dashboard.js reads window.oriHomeRouteContext when it builds a routing
   * request. This only supplies context — it never overrides an explicit
   * destination, a routing result, or a confirmation step (FR98).
   */
  function publishRouteContext() {
    const selected = findWorkspace(state.flattened, state.selectedId);
    const workspaceId = selected && !isGroupWorkspace(selected) ? selected.id : '';
    const workspaceName = selected ? selected.name : '';

    window.oriHomeRouteContext = {
      workspace_id: workspaceId,
      workspace_name: workspaceName,
      origin: 'ask_ori'
    };

    // Home's URL never names the selected workspace, so this is the only way Ask
    // Ori can know what the user is pointing at (Issue #350 FR18). It is a hint:
    // the panel treats an explicitly named workspace as the stronger signal, and
    // the server still decides what any request may touch.
    //
    // Clearing the selection clears the hint too, rather than leaving Ask Ori
    // aimed at a workspace the user has navigated away from.
    if (window.OriGuide && typeof window.OriGuide.setContext === 'function') {
      window.OriGuide.setContext({
        workspaceId,
        label: workspaceId && workspaceName ? `Workspace: ${workspaceName}` : ''
      });
    }
  }

  // ---- Today (now Updates): immediate work (FR75, FR87) ----

  function renderToday() {
    if (els.todayAttention) {
      els.todayAttention.innerHTML = renderAttentionSectionHTML(attentionItems(state.flattened));
    }
    if (els.todayScheduled) {
      els.todayScheduled.innerHTML = renderScheduledSectionHTML(
        scheduledTodayItems(state.flattened, state.scheduleIndex)
      );
    }
    // The Updates badge carries the same attention count as the section above
    // it, so it is refreshed on the same beat rather than a separate pass.
    updateUpdatesBadge();
  }

  /**
   * An Updates action that names a workspace selects it before any
   * navigation, so the Map/Tree context follows what the user just acted on
   * (FR80).
   *
   * One delegated listener on the flyout body covers every Updates source,
   * including the ones other modules render into it (Daily Brief, Calendar
   * Ops, Activity).
   */
  function wireTodaySelection() {
    if (!els.updatesBody) return;
    els.updatesBody.addEventListener('click', e => {
      const target = e.target;
      if (!target || !target.closest) return;
      const selector = target.closest('[data-cockpit-select]');
      if (selector) {
        e.preventDefault();
        selectItem(selector.getAttribute('data-cockpit-select'));
        return;
      }
      // Links into a workspace that are NOT explicit task/note/detail deep
      // links select first, then navigate (FR80).
      const link = target.closest('a[href^="/workspaces/"]');
      if (!link) return;
      const path = link.getAttribute('href') || '';
      const match = /^\/workspaces\/([^/?#]+)$/.exec(path);
      if (!match) return; // a deeper link (task, note, detail) navigates as-is
      const id = decodeURIComponent(match[1]);
      if (!findWorkspace(state.flattened, id)) return;
      e.preventDefault();
      selectItem(id);
    });
  }

  // ---- Quick Capture (FR101-FR104) ----

  /**
   * Public Quick Capture open/close, kept for callers outside this module
   * (window.OriHomeCockpit.openCapture). Routes through the shared header
   * disclosure state so Quick Capture keeps participating in Updates/Quests
   * mutual exclusion (FR9) instead of forking its own open/closed flag.
   */
  function setCaptureOpen(open) {
    if (open) openPanel(PANEL_CAPTURE);
    else closePanel();
  }

  function refreshCaptureAvailability() {
    const availability = captureAvailability(state.hqStatus);
    if (els.captureSave) els.captureSave.disabled = !availability.canSave;
    if (!els.captureStatus) return;
    if (availability.canSave) {
      els.captureStatus.textContent = '';
      els.captureStatus.innerHTML = '';
      return;
    }
    // FR104: explain the requirement and offer the existing establish path.
    // The draft above is untouched.
    els.captureStatus.innerHTML =
      `${escapeHtml(availability.message)} ` +
      '<button type="button" class="cockpit-capture-hq" data-cockpit-capture-hq>Set up Personal HQ</button>';
    const btn = els.captureStatus.querySelector('[data-cockpit-capture-hq]');
    if (btn) {
      btn.addEventListener('click', () =>
        window.dispatchEvent(
          new CustomEvent('ori:personal-hq-action', { detail: { action: 'build' } })
        )
      );
    }
  }

  async function submitCapture(e) {
    if (e) e.preventDefault();
    const draft = {
      title: els.captureTitle ? els.captureTitle.value : '',
      details: els.captureDetails ? els.captureDetails.value : ''
    };
    const valid = validateCapture(draft);
    if (!valid.ok) {
      if (els.captureStatus) els.captureStatus.textContent = valid.message;
      if (els.captureTitle) els.captureTitle.focus();
      return;
    }
    const availability = captureAvailability(state.hqStatus);
    if (!availability.canSave) {
      refreshCaptureAvailability();
      return;
    }
    // Prevent a duplicate submission while the first is in flight (FR103).
    if (els.captureSave) els.captureSave.disabled = true;
    if (els.captureStatus) els.captureStatus.textContent = 'Saving…';
    try {
      const res = await fetch('/api/orchestration/backlog', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(captureRequestBody(availability.hqWorkspaceId, draft))
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      // Only clear the draft once it is genuinely saved.
      if (els.captureTitle) els.captureTitle.value = '';
      if (els.captureDetails) els.captureDetails.value = '';
      if (els.captureStatus) els.captureStatus.textContent = 'Captured to your HQ backlog.';
      announce('Captured to your Personal HQ backlog.');
      fireTTFA('quick-capture');
      void refreshQuietly();
    } catch (err) {
      // FR103: the draft survives a failure and the retry is obvious.
      if (els.captureStatus) {
        els.captureStatus.textContent = 'Could not save. Your text is still here — try again.';
      }
      announce('Saving to the backlog failed. Your text was kept.');
      console.error('home-workspace-cockpit: capture failed', err);
    } finally {
      if (els.captureSave) els.captureSave.disabled = false;
    }
  }

  /** Personal HQ status, used by Quick Capture. Additive and non-blocking. */
  async function refreshHQStatus() {
    if (!canHydrateWorkspaceData()) return;
    const hadSite = hqSiteVisible(state.hqStatus);
    try {
      const res = await fetch('/api/personal-hq/status');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      state.hqStatus = data && data.status ? data.status : data;
    } catch (_) {
      state.hqStatus = null;
    }
    refreshCaptureAvailability();
    // This request races the workspace load, so on a brand-new profile the area
    // has usually already settled on `empty` by the time the status lands. The
    // arriving blueprint site is drawable content, so re-decide — otherwise the
    // map stays hidden and Build My HQ is unreachable (#322). Re-mount after
    // the container is visible: a map measured while hidden has no width.
    if (hqSiteVisible(state.hqStatus) !== hadSite) {
      renderAreaStatus();
      renderFilters();
      mountMap();
    }
  }

  function openItem(id) {
    if (!id) return;
    fireTTFA('open-workspace');
    window.location.href = `/workspaces/${encodeURIComponent(id)}`;
  }

  // ---- TTfA (FR141) ----
  // home-dashboard.js owns the page-level marker; the cockpit reports its own
  // distinct sources through the same event so both stay in one contract.
  function fireTTFA(source) {
    try {
      window.dispatchEvent(new CustomEvent('ori:home-ttfa', { detail: { source } }));
    } catch (_) {
      /* ignore */
    }
  }

  // ---- data ----

  /**
   * Load the schedule source that backs the Today filter and Summary.
   *
   * Independently degradable: a failure leaves scheduleIndex null, which makes
   * the Today count render as unavailable rather than as a fabricated 0, and
   * never blocks the workspace list (FR32, FR85, FR121).
   */
  async function refreshSchedule() {
    try {
      const res = await fetch('/api/orchestration/scheduled-tasks/upcoming?limit=200');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      state.scheduleIndex = buildScheduleIndex(Array.isArray(data.upcoming) ? data.upcoming : []);
      state.scheduleError = null;
    } catch (err) {
      state.scheduleIndex = null;
      state.scheduleError = err;
    }
  }

  async function refresh() {
    if (!canHydrateWorkspaceData()) return null;
    if (state.inFlight) return state.inFlight;
    state.loading = true;
    state.error = null;
    renderAreaStatus();

    // FR111: one fetch per refresh cycle, shared by Map, Tree, Summary, and the
    // selection context — no per-view fetching.
    state.inFlight = (async () => {
      try {
        const data = await loadInitialWorkspaceTree();
        state.tree = Array.isArray(data.folders) ? data.folders : [];
        state.flattened = flattenWorkspaceTree(state.tree);
        state.metadata = buildMapMetadata(state.flattened, state.tree);
        state.error = null;
      } catch (err) {
        console.error('home-workspace-cockpit: failed to load workspaces', err);
        state.error = err;
        state.tree = [];
        state.flattened = [];
      } finally {
        state.loading = false;
        state.inFlight = null;
        renderAreaStatus();
        renderFilters();
        mountMap();
        mountTreeView();
        renderToday();
        renderRail({ announceChange: false });
      }
    })();
    // The schedule rides alongside rather than inside, so a slow or failing
    // schedule never delays the Map.
    void refreshSchedule().then(() => {
      renderFilters();
      applyFilterToMap();
      renderToday();
      if (state.railState === RAIL_SUMMARY) renderRail({ announceChange: false });
    });
    return state.inFlight;
  }

  /**
   * Refresh WITHOUT tearing down view context.
   *
   * Used by realtime events and by post-mutation reloads: counts and statuses
   * update, but the active view, selection, filter, and rail scroll survive
   * (FR119). Also idempotent — repeated calls collapse onto one in-flight
   * request rather than stacking (FR122).
   */
  async function refreshQuietly() {
    if (!canHydrateWorkspaceData()) return null;
    if (state.inFlight) return state.inFlight;
    const railScroll = els.railContext ? els.railContext.scrollTop : 0;
    const updatesScroll = els.updatesBody ? els.updatesBody.scrollTop : 0;
    state.inFlight = (async () => {
      try {
        const response = await fetch('/api/workspaces?tree=true');
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const data = await response.json();
        if (!data || !Array.isArray(data.folders)) {
          throw new Error('Workspace tree response is invalid');
        }
        state.tree = data.folders;
        state.flattened = flattenWorkspaceTree(state.tree);
        state.metadata = buildMapMetadata(state.flattened, state.tree);
        state.error = null;
      } catch (err) {
        // A failed background refresh keeps the last good data on screen rather
        // than blanking a working cockpit.
        console.warn('home-workspace-cockpit: background refresh failed', err);
      } finally {
        state.inFlight = null;
        renderAreaStatus();
        renderFilters();
        mountMap();
        mountTreeView();
        renderToday();
        renderRail({ announceChange: false });
        if (els.railContext) els.railContext.scrollTop = railScroll;
        if (els.updatesBody) els.updatesBody.scrollTop = updatesScroll;
      }
    })();
    return state.inFlight;
  }

  // ---- realtime (FR119, FR122) ----

  let realtimeTimer = null;
  function scheduleRealtimeRefresh() {
    if (!canHydrateWorkspaceData()) return;
    // Coalesce bursts: a run that emits many task events must cause one
    // refresh, not one per event.
    if (realtimeTimer) return;
    realtimeTimer = setTimeout(() => {
      realtimeTimer = null;
      void refreshQuietly();
    }, 600);
  }

  // Realtime is a PER-WORKSPACE SSE stream (workspace-realtime.js exposes only
  // subscribeToWorkspace); there is no cross-workspace feed. Opening one
  // connection per site would mean N connections from Home, so the cockpit
  // follows the launcher's existing precedent and keeps a live stream for the
  // workspace the user is actually looking at, re-pointing it as the selection
  // moves. Counts elsewhere refresh on the next mutation or reload.
  let realtimeUnsub = null;
  let realtimeWorkspaceId = '';

  function subscribeRealtimeTo(workspaceId) {
    const rt = window.workspaceRealtime;
    if (!rt || typeof rt.subscribeToWorkspace !== 'function') return;
    if (workspaceId === realtimeWorkspaceId) return;
    // Idempotent: the previous subscription is always torn down first, so
    // repeated selection changes cannot stack listeners (FR122).
    if (realtimeUnsub) {
      try {
        realtimeUnsub();
      } catch (_) {
        /* ignore */
      }
      realtimeUnsub = null;
    }
    realtimeWorkspaceId = workspaceId || '';
    if (!realtimeWorkspaceId || realtimeWorkspaceId === HQ_SITE_ID) return;
    realtimeUnsub = rt.subscribeToWorkspace(realtimeWorkspaceId, event => {
      const type = String((event && event.type) || '');
      if (!type) return;
      if (
        type.startsWith('task.') ||
        type.startsWith('workflow.') ||
        type.startsWith('step.') ||
        type === 'workspace.updated' ||
        type === 'workspace.completed'
      ) {
        scheduleRealtimeRefresh();
      }
    });
  }

  // ---- wiring ----

  els.viewButtons.forEach(btn => {
    btn.addEventListener('click', () => {
      applyView(btn.getAttribute('data-cockpit-view'));
      fireTTFA('view-toggle');
    });
  });

  // Updates / Quests / Quick Capture: three triggers sharing one panel state
  // (FR7-FR9). The Quests trigger does not exist in the template until Group
  // 2 lands; the null-guard keeps this wiring correct either way.
  if (els.railToggle) els.railToggle.addEventListener('click', () => togglePanel(PANEL_UPDATES));
  if (els.updatesClose) els.updatesClose.addEventListener('click', () => closePanel());
  if (els.questsToggle) els.questsToggle.addEventListener('click', () => togglePanel(PANEL_QUESTS));
  if (els.questsClose) els.questsClose.addEventListener('click', () => closePanel());

  if (els.summaryBtn) {
    els.summaryBtn.addEventListener('click', () =>
      state.railState === RAIL_SUMMARY ? leaveSummary() : showSummary()
    );
  }

  if (els.captureBtn) els.captureBtn.addEventListener('click', () => togglePanel(PANEL_CAPTURE));
  if (els.captureForm) els.captureForm.addEventListener('submit', submitCapture);
  if (els.captureCancel) els.captureCancel.addEventListener('click', () => closePanel());

  // Personal HQ provisioning can complete while Home is open; re-read the
  // status so Quick Capture stops explaining a requirement already met.
  window.addEventListener('ori:personal-hq-changed', () => void refreshHQStatus());

  wireTodaySelection();

  // dashboard.js toggles the panel's `show` class from many code paths
  // (submit, reopen, timeout, close). Observing the class is the one seam that
  // catches all of them without reaching into its internals.
  if (els.askPanel && typeof MutationObserver === 'function') {
    new MutationObserver(syncAskActivity).observe(els.askPanel, {
      attributes: true,
      attributeFilter: ['class']
    });
    const back = els.askPanel.querySelector('[data-cockpit-ask-back]');
    if (back) {
      back.addEventListener('click', () => {
        els.askPanel.classList.remove('show');
        syncAskActivity();
      });
    }
  }

  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    // Escape only reaches the rail/flyouts when nothing higher-priority owns
    // it (FR128).
    if (document.querySelector('.modal.show')) return;
    const target = e.target;
    // Whichever header panel is open owns Escape, INCLUDING while focus sits
    // in its own fields — otherwise the editable-field guard below would
    // swallow the key and leave the panel stuck open (FR10, FR128).
    const openPanelEl =
      state.panel === PANEL_UPDATES
        ? els.updatesFlyout
        : state.panel === PANEL_QUESTS
          ? els.questsFlyout
          : state.panel === PANEL_CAPTURE
            ? els.capturePanel
            : null;
    if (openPanelEl && (!target || !target.closest || target.closest(`#${openPanelEl.id}`))) {
      closePanel();
      return;
    }
    if (
      target &&
      (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
    ) {
      return;
    }
    if (openPanelEl) {
      closePanel();
      return;
    }
    if (state.railState === RAIL_SUMMARY) {
      leaveSummary();
      return;
    }
    clearSelection();
  });

  // Every create/import/delete/move/tag/undo path funnels through here, so one
  // authoritative reload updates Map, Tree, Summary, and the rail together
  // rather than each view refetching for itself (FR108, FR117).
  window.addEventListener('ori:workspaces-changed', () => {
    if (canHydrateWorkspaceData()) void refreshQuietly();
  });

  // Expose a narrow seam so later cockpit surfaces (Tree, Today, Ask Ori) and
  // realtime refreshes drive the SAME state rather than forking their own.
  window.OriHomeCockpit = {
    refresh,
    refreshQuietly,
    getState: () => state,
    setView: applyView,
    setSignal: applySignal,
    select: selectItem,
    clearSelection,
    showSummary,
    leaveSummary,
    openCapture: () => setCaptureOpen(true),
    // Narrow seam for progression-widget.js's own "Collapse" control to close
    // whichever header disclosure (Updates/Quests/Quick Capture) is open,
    // without either module importing the other (Issue #334).
    closeHeaderPanel: () => closePanel()
  };

  function canHydrateWorkspaceData() {
    return workspaceHydrationAllowed(state.onboardingGate);
  }

  async function initializeWorkspaceHydration({ force = false } = {}) {
    state.onboardingGate = {
      state: ONBOARDING_GATE_LOADING,
      allowWorkspaceHydration: false,
      message: 'Preparing your workspace setup…'
    };
    state.loading = true;
    state.error = null;
    renderAreaStatus();

    const status = await loadOnboardingStatus({ force });
    state.onboardingGate = onboardingGateDecision(status);
    if (!canHydrateWorkspaceData()) {
      state.loading = false;
      state.tree = [];
      state.flattened = [];
      state.metadata = { folderDisplayById: {}, tagsById: {}, groupPreviewById: {} };
      renderAreaStatus();
      renderFilters();
      mountMap();
      mountTreeView();
      renderToday();
      renderRail({ announceChange: false });
      return state.onboardingGate;
    }

    void refreshWorkspaceRoot();
    void refreshHQStatus();
    await refresh();
    return state.onboardingGate;
  }

  applyView(state.view, { pushUrl: false });
  applyPanelState();
  void initializeWorkspaceHydration();
})();
