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

/** Linked-folder badge data for a workspace, matching the Map's expectations. */
export function folderDisplayFor(workspace) {
  const directories = Array.isArray(workspace && workspace.directories)
    ? workspace.directories.filter(Boolean)
    : [];
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

// ---------------------------------------------------------------------------
// Rail view models + rendering
// ---------------------------------------------------------------------------

export const RAIL_TODAY = 'today';
export const RAIL_WORKSPACE = 'workspace';
export const RAIL_GROUP = 'group';
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
    roster: agentRoster(ws)
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

function rosterHTML(roster) {
  if (!roster.length) {
    return '<p class="cockpit-rail-empty">No agents attached yet.</p>';
  }
  return (
    '<ul class="cockpit-rail-roster">' +
    roster
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
    '<span aria-hidden="true">&#8592;</span> Today</button>' +
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
    rosterHTML(view.roster) +
    '</section>' +
    '</div>'
  );
}

// ---------------------------------------------------------------------------
// Workspace-area states (FR110, FR112, FR113, FR114, FR120)
// ---------------------------------------------------------------------------

/**
 * Decide what the workspace area should show for a given load outcome.
 *
 * Loading, error, and empty are distinct states with distinct affordances —
 * FR120 forbids collapsing them, and FR113/FR114 require Retry and the
 * onboarding actions respectively.
 */
export function workspaceAreaState({ loading, error, workspaces }) {
  if (loading) return { state: 'loading', message: 'Loading workspaces…', canRetry: false };
  if (error) {
    return {
      state: 'error',
      message: 'Workspaces could not be loaded.',
      detail: String(error.message || error || '').slice(0, 200),
      canRetry: true
    };
  }
  if (!Array.isArray(workspaces) || workspaces.length === 0) {
    return { state: 'empty', message: 'No workspaces yet.', canRetry: false };
  }
  return { state: 'ready', message: '', canRetry: false };
}

export function renderWorkspaceAreaStatusHTML(status) {
  if (!status || status.state === 'ready') return '';
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
  // Empty: never render a misleading operational map (FR114).
  return (
    '<div class="cockpit-area-message" data-state="empty">' +
    `<p class="cockpit-area-message-text">${escapeHtml(status.message)}</p>` +
    '<p class="cockpit-area-message-detail">Create your first workspace, or import a folder you already work in.</p>' +
    '<div class="cockpit-area-message-actions">' +
    '<button type="button" class="modern-btn modern-btn-primary" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="false" data-workspace-entry-point="home_cockpit_create">New Workspace</button>' +
    '<button type="button" class="modern-btn modern-btn-secondary" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="true" data-workspace-entry-point="home_cockpit_import">Import Folder</button>' +
    '</div>' +
    '</div>'
  );
}

// ---------------------------------------------------------------------------
// DOM wiring — no-ops without a document, so the exports above stay testable.
// ---------------------------------------------------------------------------

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
    railToday: document.getElementById('cockpitRailToday'),
    railContext: document.getElementById('cockpitRailContext'),
    railLive: document.getElementById('cockpitRailLive')
  };

  // ---- shared state (the single source of truth for every cockpit view) ----
  const state = {
    tree: [],
    flattened: [],
    metadata: { folderDisplayById: {}, tagsById: {}, groupPreviewById: {} },
    view: parseViewFromQuery(window.location.search),
    selectedId: '',
    railState: RAIL_TODAY,
    hqSiteView: null,
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
      workspaces: state.flattened
    });
    if (els.areaStatus) {
      els.areaStatus.innerHTML = renderWorkspaceAreaStatusHTML(status);
      const retry = els.areaStatus.querySelector('[data-cockpit-retry]');
      if (retry) retry.addEventListener('click', () => refresh());
    }
    root.dataset.state = status.state;
    // The map is only meaningful once there is something truthful to draw.
    if (els.map) els.map.hidden = state.view !== VIEW_MAP || status.state !== 'ready';
    return status;
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
      onSelect: id => selectWorkspace(id, { fromMap: true }),
      onOpen: id => openWorkspace(id),
      onSelectHQSite: view => selectHQSite(view)
    });
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
    state.selectedId = resolved || '';
    renderRail({ announceChange: false });
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
    if (pushUrl) {
      // replaceState, never pushState: a view toggle must not add a history
      // entry (FR13/FR26).
      const next = window.location.pathname + searchForView(window.location.search, state.view);
      window.history.replaceState(window.history.state, '', next);
    }
    renderAreaStatus();
    mountMap();
  }

  // ---- selection + rail ----

  function selectWorkspace(id, { fromMap = false } = {}) {
    const next = id || '';
    const changed = next !== state.selectedId;
    state.selectedId = next;
    state.railState = next ? RAIL_WORKSPACE : RAIL_TODAY;
    state.hqSiteView = null;
    if (!fromMap && els.map && window.OriWorkspaceMap && window.OriWorkspaceMap.setSelectedId) {
      window.OriWorkspaceMap.setSelectedId(els.map, state.flattened, next, {
        metadata: state.metadata
      });
    }
    renderRail({ announceChange: changed });
    if (changed && next) fireTTFA('map-select');
  }

  function clearSelection() {
    if (!state.selectedId && state.railState === RAIL_TODAY) return;
    state.hqSiteView = null;
    selectWorkspace('');
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

  function renderHQSiteRail() {
    if (!els.railContext || !window.OriWorkspaceMap) return false;
    const html = window.OriWorkspaceMap.hqOverviewHTML(state.hqSiteView);
    els.railContext.innerHTML =
      '<div class="cockpit-rail-panel" data-rail-panel="personal-hq">' +
      '<header class="cockpit-rail-head">' +
      '<button type="button" class="cockpit-rail-back" data-cockpit-rail-back>' +
      '<span aria-hidden="true">&#8592;</span> Today</button>' +
      '</header>' +
      html +
      '</div>';
    els.railContext.querySelectorAll('[data-hq-action]').forEach(el =>
      el.addEventListener('click', () =>
        window.dispatchEvent(
          new CustomEvent('ori:personal-hq-action', {
            detail: { action: el.getAttribute('data-hq-action') }
          })
        )
      )
    );
    const back = els.railContext.querySelector('[data-cockpit-rail-back]');
    if (back) back.addEventListener('click', () => clearSelection());
    return true;
  }

  function renderRail({ announceChange = true } = {}) {
    // The reserved HQ site is a landmark, not a workspace, so it is resolved
    // before the workspace lookup.
    if (state.selectedId === HQ_SITE_ID && state.railState !== RAIL_TODAY) {
      if (els.railToday) els.railToday.hidden = true;
      if (els.railContext) els.railContext.hidden = false;
      renderHQSiteRail();
      if (announceChange) announce('Personal HQ site selected.');
      return;
    }

    const workspace = findWorkspace(state.flattened, state.selectedId);
    const showContext = state.railState !== RAIL_TODAY && workspace;
    if (els.railToday) els.railToday.hidden = !!showContext;
    if (els.railContext) {
      els.railContext.hidden = !showContext;
      els.railContext.innerHTML = showContext
        ? renderWorkspaceRailHTML(workspaceRailView(workspace))
        : '';
      const back = els.railContext.querySelector('[data-cockpit-rail-back]');
      if (back) back.addEventListener('click', () => clearSelection());
      const open = els.railContext.querySelector('[data-cockpit-rail-open]');
      if (open) open.addEventListener('click', () => fireTTFA('open-workspace'));
    }
    if (!showContext && state.selectedId) {
      // The selected item vanished under us (deleted or now inaccessible):
      // return to Today and say so rather than showing a stale rail (FR73).
      state.selectedId = '';
      state.railState = RAIL_TODAY;
      announce('The selected workspace is no longer available. Showing Today.');
      return;
    }
    if (announceChange) {
      announce(showContext ? `${workspace.name} selected.` : 'Showing Today.');
    }
  }

  function openWorkspace(id) {
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

  async function refresh() {
    if (state.inFlight) return state.inFlight;
    state.loading = true;
    state.error = null;
    renderAreaStatus();

    // FR111: one fetch per refresh cycle, shared by Map, Tree, Summary, and the
    // selection context — no per-view fetching.
    state.inFlight = (async () => {
      try {
        const response = await fetch('/api/workspaces?tree=true');
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const data = await response.json();
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
        mountMap();
        renderRail({ announceChange: false });
      }
    })();
    return state.inFlight;
  }

  // ---- wiring ----

  els.viewButtons.forEach(btn => {
    btn.addEventListener('click', () => {
      applyView(btn.getAttribute('data-cockpit-view'));
      fireTTFA('view-toggle');
    });
  });

  document.addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    // Escape only reaches the rail when nothing higher-priority owns it (FR128).
    if (document.querySelector('.modal.show')) return;
    const target = e.target;
    if (
      target &&
      (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
    ) {
      return;
    }
    clearSelection();
  });

  // Expose a narrow seam so later cockpit surfaces (Tree, Today, Ask Ori) and
  // realtime refreshes drive the SAME state rather than forking their own.
  window.OriHomeCockpit = {
    refresh,
    getState: () => state,
    setView: applyView,
    select: selectWorkspace,
    clearSelection
  };

  applyView(state.view, { pushUrl: false });
  refresh();
})();
