/*
 * workspace-command.js — Workspace Command view (the workspace detail page)
 *
 * The tactical layout that IS the workspace detail page. It reuses the live
 * WorkspaceDetailPage instance (a headless data/action layer plus shared
 * modals and hidden shared hosts — see #workspace-detail-shared-hosts) and
 * renders into #workspaceCommandView.
 */
// Shared task-presentation model — single source of truth for task status
// predicates (and, in later groups, labels/counts/actions). The Map's status
// predicates delegate here so no surface keeps a parallel copy (FR29).
import {
  isBlockedTask as sharedIsBlockedTask,
  isNeedsInputTask as sharedIsNeedsInputTask,
  isWorkingTask as sharedIsWorkingTask,
  isQueuedTask as sharedIsQueuedTask,
  resolveTaskPresentation,
  resolveTaskCounts,
  sortTasksForDrawer,
  taskMatchesFilter,
  taskShortId,
  FILTER
} from './task-presentation.js';
import { WorkspaceExecutionController, RUN_PHASE } from './workspace-execution-controller.js';
import {
  parseWorkspaceURLState,
  sanitizeWorkspaceURLState,
  statesEqual as urlStatesEqual,
  resolveEffectiveMode,
  buildReturnTarget,
  buildWorkspaceURL
} from './workspace-url-state.js';
// Legacy view preference from the deleted Detailed/Command toggle; cleared on boot.
const LEGACY_STORAGE_KEY = 'oriWorkspaceDetailView';
const VIEW_MODE_STORAGE_KEY = 'oriWorkspaceCommandViewMode';
const AGENT_TAB_KEYS = ['overview', 'tasks', 'loadout', 'recent'];
const COMMAND_VIEW_MODES = ['details', 'map'];

function escapeHtml(value) {
  return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

export class WorkspaceCommandView {
  /**
   * @param {object} page - the live WorkspaceDetailPage instance (window.workspaceDetail).
   */
  constructor(page) {
    this.page = page || null;
    this.container = document.getElementById('workspaceCommandView');
    this.active = false;
    // URL/history restoration (group 6): read the URL once at boot. `mode`
    // wins over the localStorage preference (FR85); panel/task/agent/run are
    // held pending until page data is available to sanitize against (FR91).
    this._urlBootState = typeof window !== 'undefined' ? parseWorkspaceURLState(window.location.search) : null;
    this._urlStateApplied = false;
    this._urlSyncEnabled = false;
    this._lastSyncedURLState = null;
    this.viewMode = resolveEffectiveMode(
      this._urlBootState && this._urlBootState.mode,
      this.readCommandViewModePreference()
    );
    this.activeRailSection = '';
    this.activeMapWindow = '';
    this.mapInventoryOpen = false;
    this.mapInventorySection = '';
    this.statModalSection = '';
    this.statModalEl = null;
    this.statModalTrigger = null;
    this.taskModalShowAll = false;
    this.taskModalBoardMode = false;
    // Persistent, non-modal task drawer (group 3) — replaces the Objectives→Open
    // Tasks modal stack. Rendered as a side panel that survives full re-renders.
    this.taskDrawerOpen = false;
    this.taskDrawerEl = null;
    this.taskDrawerTrigger = null;
    this.taskDrawerFilter = FILTER.ACTIONABLE;
    this.taskDrawerSelectedId = '';
    this._drawerAnnounce = '';
    // Sticky execution tray (group 4) — a collapsible mini-player that renders
    // from the workspace-scoped execution controller. Monitoring survives
    // collapse and any view change (it lives in the controller, not a modal).
    this.execController = null;
    this.trayEl = null;
    this.trayOpen = false;
    this.trayCollapsed = false;
    // Quick "New Quest" composer on the operations map (create task → entry-agent default).
    this.taskComposerOpen = false;
    this.taskComposerDraft = '';
    this.taskComposerError = '';
    this.taskComposerSubmitting = false;
    this.identityExpanded = false;
    this.identityEditMode = '';
    this.identitySaving = false;
    this.commandTagInput = null;
    this.commandTagDraft = [];
    this.commandTagError = '';
    this.activeSystemTab = 'memory';
    this.activeToolsTab = 'mcp';
    this.selectedAgentKey = '';
    this.agentSelectionInitialized = false;
    this.activeAgentTab = 'overview';
    this.agentRosterScroll = { top: 0, left: 0 };
    this.agentOverviewScroll = 0;
    this.pendingAgentFocusKey = '';
    this.pendingAgentTabFocus = '';
    this.agentPromptLoadingKey = '';
    this.lastAnnouncedAgentStatus = '';
    // Interactive loadout editing (shared by map Unit Sheet + details Loadout tab).
    this.loadoutAddOpen = ''; // '' | 'skill' | 'mcp' — which "Add" picker is open
    this.loadoutAddOptions = [];
    this.loadoutAddLoading = false;
    this.loadoutBusyKey = ''; // "<kind>:<bindingId>" or "<kind>:add:<name>" while mutating
    this.loadoutError = '';
    this.sharedSurfaceAnchors = {};
    this.boundGlobalKeydown = event => this.handleGlobalKeydown(event);
    this.boundPopState = event => this.handlePopState(event);
    this.setup();
  }

  setup() {
    if (!this.container) return;
    this.retireLegacyViewPreference();
    this.activate();
  }

  normalizeCommandViewMode(mode) {
    const normalized = String(mode || '')
      .trim()
      .toLowerCase();
    return COMMAND_VIEW_MODES.includes(normalized) ? normalized : 'details';
  }

  readCommandViewModePreference() {
    if (typeof localStorage === 'undefined') return 'details';
    try {
      return this.normalizeCommandViewMode(localStorage.getItem(VIEW_MODE_STORAGE_KEY));
    } catch (_error) {
      return 'details';
    }
  }

  persistCommandViewMode(mode) {
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(VIEW_MODE_STORAGE_KEY, this.normalizeCommandViewMode(mode));
    } catch (_error) {
      // Storage is best effort; the current render can continue without persistence.
    }
  }

  setCommandViewMode(mode, { focus = true } = {}) {
    const nextMode = this.normalizeCommandViewMode(mode);
    if (nextMode === this.viewMode) return;
    this.viewMode = nextMode;
    this.persistCommandViewMode(nextMode);
    this.activeRailSection = '';
    if (nextMode === 'details') {
      this.activeMapWindow = '';
      this.mapInventoryOpen = false;
      this.mapInventorySection = '';
    }
    this.restoreSharedSurfaces();
    this.render();
    this.syncURLState();
    if (!focus || !this.container || typeof this.container.querySelector !== 'function') return;
    const btn = this.container.querySelector('[data-cmd-view-mode="' + nextMode + '"]');
    if (btn && typeof btn.focus === 'function') btn.focus();
  }

  // Drop the stale preference and URL param from the old deleted Detailed/Command toggle.
  retireLegacyViewPreference() {
    try {
      localStorage.removeItem(LEGACY_STORAGE_KEY);
    } catch (err) {
      /* storage may be unavailable */
    }
    try {
      const params = new URLSearchParams(window.location.search);
      if (params.has('view')) {
        params.delete('view');
        const query = params.toString();
        const nextUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
        window.history.replaceState(null, '', nextUrl);
      }
    } catch (err) {
      // no-op: history API may be unavailable in some embedding contexts
    }
  }

  activate() {
    this.active = true;
    this.render();
    if (this.container) this.container.hidden = false;
    if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
      document.addEventListener('keydown', this.boundGlobalKeydown);
    }
    if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
      window.addEventListener('popstate', this.boundPopState);
    }
    this.applyBootURLState();
  }

  /** Re-render if active — called by the page after its data loads/refreshes. */
  refresh() {
    if (this.active) this.render();
    else if (this.statModalSection) this.renderStatModalBody();
    // Live-refresh the drawer body in place (preserves selection + scroll, FR25).
    if (this.taskDrawerOpen) this.renderTaskDrawerBody();
    // Data may not have been ready the first time activate() ran; retry once
    // page.tasks/agents are actually populated (FR90: refresh restores from URL).
    this.applyBootURLState();
  }

  // ---------- URL / history restoration (group 6) ----------
  //
  // Canonical params: mode, panel, task, agent, run (FR80). URL wins over the
  // localStorage view-mode preference (FR85, applied in the constructor).
  // Meaningful transitions (mode/drawer/task/agent/run changes) push or
  // replace history; presentational changes (scroll, tray collapse) never
  // touch the URL and are instead captured into history.state (FR86-FR87).

  /** Build the validation context sanitizeWorkspaceURLState needs (FR91). */
  urlStateContext() {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const validTaskIds = tasks.map(t => String(t?.id || '')).filter(Boolean);
    let validAgentKeys = [];
    try {
      validAgentKeys = this.agentGroups().agents.map(g => this.normalizeAgentKey(g.key || g.name));
    } catch (_error) {
      validAgentKeys = [];
    }
    return { validTaskIds, validAgentKeys, validRunTaskIds: validTaskIds };
  }

  /**
   * Apply the URL state captured at construction, once page data exists to
   * sanitize against. Idempotent — safe to call from both activate() and
   * refresh(); only the first call with usable context takes effect.
   *
   * WorkspaceCommandView is constructed synchronously right after the page
   * kicks off its (async) initial data load, so `page.tasks`/agent groups are
   * still empty on the very first call — merely checking `Array.isArray` is
   * not enough to tell "no data yet" apart from "genuinely zero tasks." If the
   * boot URL names a task/agent/run but the currently-known valid set is
   * completely empty, treat that as "not loaded yet" and retry on the next
   * refresh() (which the page calls once real data arrives), bounded so a
   * truly stale link still eventually gets dropped per FR91.
   */
  applyBootURLState() {
    if (this._urlStateApplied || !this._urlBootState) {
      this._urlSyncEnabled = true;
      return;
    }
    const page = this.page || {};
    if (!Array.isArray(page.tasks)) return;

    const context = this.urlStateContext();
    const boot = this._urlBootState;
    const dataLooksUnready =
      (boot.task && context.validTaskIds.length === 0) ||
      (boot.agent && context.validAgentKeys.length === 0) ||
      (boot.run && (context.validRunTaskIds || context.validTaskIds).length === 0);
    this._bootApplyAttempts = (this._bootApplyAttempts || 0) + 1;
    const MAX_BOOT_ATTEMPTS = 20;
    if (dataLooksUnready && this._bootApplyAttempts < MAX_BOOT_ATTEMPTS) return;

    const { state, dropped } = sanitizeWorkspaceURLState(boot, context);
    this._urlStateApplied = true;

    if (dropped.length && typeof window !== 'undefined' && window.Toast) {
      window.Toast.info('Some link details were out of date and were ignored.');
    }

    const effectiveMode = resolveEffectiveMode(state.mode, this.viewMode, this.viewMode);
    if (effectiveMode !== this.viewMode) {
      this.viewMode = effectiveMode;
      this.persistCommandViewMode(effectiveMode);
    }
    if (state.agent) {
      this.selectedAgentKey = state.agent;
      this.agentSelectionInitialized = true;
      this.persistAgentKey(state.agent);
    }
    if (state.panel === 'tasks') {
      this.taskDrawerOpen = true;
      if (state.task) this.taskDrawerSelectedId = state.task;
    }
    if (state.run) this.trackAndShowTray(state.run);

    this._urlSyncEnabled = true;
    this.render();
    if (this.taskDrawerOpen) {
      const el = this.ensureTaskDrawer();
      if (el) {
        el.hidden = false;
        this.renderTaskDrawerBody();
      }
    }
    // Normalize the URL to the sanitized state without adding a history entry.
    this.syncURLState({ replace: true });
  }

  /** Current URL-relevant state derived from live view state. */
  currentURLState() {
    return {
      // 'details' is the historical default; omit it so a details-mode URL
      // stays clean (no ?mode=details noise) and only an explicit `map` needs
      // to survive reload/sharing.
      mode: this.viewMode === 'map' ? 'map' : null,
      panel: this.taskDrawerOpen ? 'tasks' : '',
      task: this.taskDrawerOpen ? this.taskDrawerSelectedId : '',
      agent: this.selectedAgentKey || '',
      run:
        this.execController && typeof this.execController.getSelectedTaskId === 'function'
          ? this.execController.getSelectedTaskId()
          : ''
    };
  }

  /** Best-effort selector for the last meaningfully focused control (FR89). */
  currentFocusSelector() {
    if (typeof document === 'undefined') return null;
    const active = document.activeElement;
    if (!active || typeof active.getAttribute !== 'function') return null;
    for (const attr of ['data-cmd-map-select-agent', 'data-cmd-drawer-select', 'data-cmd-open-task-drawer']) {
      const value = active.getAttribute(attr);
      if (value) return '[' + attr + '="' + value.replace(/"/g, '\\"') + '"]';
    }
    return null;
  }

  /** Snapshot scroll/focus into the CURRENT history entry before navigating away (FR89). */
  captureHistoryPresentationState() {
    if (typeof window === 'undefined' || !window.history) return;
    const current = window.history.state || {};
    const drawerList =
      this.taskDrawerEl && typeof this.taskDrawerEl.querySelector === 'function'
        ? this.taskDrawerEl.querySelector('.ws-cmd-drawer-list')
        : null;
    const drawerScroll = drawerList ? drawerList.scrollTop : current.drawerScroll || 0;
    window.history.replaceState(
      {
        ...current,
        drawerScroll,
        focusSelector: this.currentFocusSelector() || current.focusSelector || null,
        trayCollapsed: this.trayCollapsed
      },
      '',
      window.location.href
    );
  }

  /**
   * Push or replace history for the current view state (FR86-FR88). No-op
   * transitions never create a duplicate entry; the caller decides push vs
   * replace (replace for normalization, push for a genuine user navigation).
   */
  syncURLState({ replace = false } = {}) {
    if (!this._urlSyncEnabled || typeof window === 'undefined' || !window.history) return;
    const nextState = this.currentURLState();
    if (urlStatesEqual(nextState, this._lastSyncedURLState)) return;
    const url = buildWorkspaceURL(window.location.pathname, nextState);
    // Nothing to rewrite if the target URL already matches the address bar —
    // avoids an unnecessary history.replaceState on an already-clean URL.
    const currentUrl = window.location.pathname + (window.location.search || '');
    if (url === currentUrl) {
      this._lastSyncedURLState = nextState;
      return;
    }
    if (!replace) this.captureHistoryPresentationState();
    const historyEntryState = replace
      ? { ...(window.history.state || {}) }
      : { drawerScroll: 0, focusSelector: null, trayCollapsed: this.trayCollapsed };
    if (replace) window.history.replaceState(historyEntryState, '', url);
    else window.history.pushState(historyEntryState, '', url);
    this._lastSyncedURLState = nextState;
  }

  /** Restore view state from Back/Forward navigation (FR88-FR89). */
  handlePopState(event) {
    if (!this._urlSyncEnabled) return;
    const context = this.urlStateContext();
    const { state } = sanitizeWorkspaceURLState(parseWorkspaceURLState(window.location.search), context);
    this._lastSyncedURLState = state;

    const effectiveMode = resolveEffectiveMode(state.mode, this.viewMode, this.viewMode);
    this.viewMode = effectiveMode;
    if (state.agent) {
      this.selectedAgentKey = state.agent;
      this.agentSelectionInitialized = true;
    }
    this.taskDrawerOpen = state.panel === 'tasks';
    if (this.taskDrawerOpen && state.task) this.taskDrawerSelectedId = state.task;
    const historyState = (event && event.state) || {};
    this.trayCollapsed = Boolean(historyState.trayCollapsed);
    if (state.run) this.trackAndShowTray(state.run);

    this.render();
    const el = this.taskDrawerOpen ? this.ensureTaskDrawer() : null;
    if (el) {
      el.hidden = false;
      this.renderTaskDrawerBody();
      const list = el.querySelector('.ws-cmd-drawer-list');
      if (list && typeof historyState.drawerScroll === 'number') list.scrollTop = historyState.drawerScroll;
    }
    if (historyState.focusSelector && this.container && typeof this.container.querySelector === 'function') {
      const target = this.container.querySelector(historyState.focusSelector);
      if (target && typeof target.focus === 'function') target.focus({ preventScroll: true });
    }
  }

  /** A safe, same-origin `Open Full Task` href carrying a validated return target (FR92). */
  taskHrefWithReturn(taskId) {
    const wsId = typeof this.workspaceId === 'function' ? this.workspaceId() : '';
    const base = '/workspaces/' + encodeURIComponent(wsId) + '/task/' + encodeURIComponent(taskId);
    const returnTarget = buildReturnTarget(wsId, this.currentURLState());
    return returnTarget ? base + '?return=' + encodeURIComponent(returnTarget) : base;
  }

  handleGlobalKeydown(event) {
    if (!this.active || !event || event.key !== 'Escape') return;
    if (this.statModalSection || this.identityEditMode) return;
    if (this.taskDrawerOpen) {
      this.closeTaskDrawer();
      return;
    }
    // Collapse (not close) an expanded tray on Escape — collapsing is
    // reversible and never stops monitoring, unlike closing (FR52, FR123).
    if (this.trayOpen && !this.trayCollapsed) {
      this.toggleTrayCollapsed();
      return;
    }
    if (this.viewMode === 'map' && this.taskComposerOpen) {
      this.closeTaskComposer();
      return;
    }
    if (this.viewMode === 'map' && this.activeMapWindow) {
      this.activeMapWindow = '';
      this.render();
      return;
    }
    if (this.viewMode === 'map' && this.mapInventoryOpen) {
      this.mapInventoryOpen = false;
      this.render();
      return;
    }
    if (!this.activeRailSection) return;
    this.activeRailSection = '';
    this.render();
  }

  computeStats() {
    const page = this.page || {};
    const ws = page.workspace || {};
    let agents = 0;
    try {
      agents = (page.buildAgentGroups() || []).filter(
        g => g.isWorkspaceAgent && !g.isUnassigned
      ).length;
    } catch (err) {
      agents = Array.isArray(ws.agent_instances) ? ws.agent_instances.length : 0;
    }
    // Canonical "open" = pending + in-progress, matching the server-side
    // open_task_count Map/Cards/Tree all read (see workspace.ComputeMapSummaryFields).
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const openTasks = tasks.filter(t => {
      const s = String((t && t.status) || '').toLowerCase();
      return s === 'pending' || s === 'in_progress';
    }).length;
    const mcp = Array.isArray(ws.mcp_bindings) ? ws.mcp_bindings.length : 0;
    const skills = Array.isArray(ws.skill_bindings) ? ws.skill_bindings.length : 0;
    // "Tools" groups the capability providers (MCP + Skills + Plugins). Plugins install
    // themselves as MCP/skill bindings, so they are already reflected in this count.
    return { agents, openTasks, mcp, skills, tools: mcp + skills };
  }

  opsModeLabel() {
    const ws = (this.page && this.page.workspace) || {};
    const settings = ws.workspace_settings || {};
    const mode = String((settings.workflow && settings.workflow.mode) || '').toLowerCase();
    switch (mode) {
      case 'guided':
        return 'Guided';
      case 'direct':
        return 'Direct';
      case 'plan_then_execute':
        return 'Autonomous';
      case '':
        return '';
      default:
        return mode.charAt(0).toUpperCase() + mode.slice(1);
    }
  }

  missionSummary() {
    const mission = typeof window !== 'undefined' ? window.workspaceMission : null;
    if (mission && typeof mission.getSummary === 'function') {
      try {
        return mission.getSummary() || {};
      } catch (err) {
        return {};
      }
    }
    return {};
  }

  commandSubtitle(mode) {
    const workflow = mode ? 'Workflow · ' + mode : '';
    const mission = this.missionSummary();
    const missionLabel = mission && mission.label ? 'Mission · ' + mission.label : '';
    return [workflow, missionLabel].filter(Boolean).join(' · ');
  }

  workspaceId() {
    const page = this.page || {};
    return String(page.workspaceId || (page.workspace && page.workspace.id) || '').trim();
  }

  workspaceTags() {
    const page = this.page || {};
    if (typeof page.getWorkspaceTags === 'function') {
      try {
        return page.getWorkspaceTags();
      } catch (err) {
        return [];
      }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.tags)
      ? ws.tags.map(tag => String(tag || '').trim()).filter(Boolean)
      : [];
  }

  workflowHref() {
    const page = this.page || {};
    if (
      typeof page.collectWorkspaceWorkflowReferences === 'function' &&
      typeof page.buildBehaviorStudioHref === 'function'
    ) {
      try {
        const refs = page.collectWorkspaceWorkflowReferences();
        if (Array.isArray(refs) && refs.length === 1) {
          return page.buildBehaviorStudioHref(refs[0].templateId);
        }
      } catch (err) {
        return '/workflows';
      }
    }
    return '/workflows';
  }

  workspaceRoute(suffix = '') {
    const id = this.workspaceId();
    return id ? '/workspaces/' + encodeURIComponent(id) + suffix : '#';
  }

  hasAttentionTasks() {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    return tasks.some(task => {
      const status = String((task && task.status) || '').toLowerCase();
      return status === 'failed' || status === 'blocked';
    });
  }

  statBoxHTML(value, label, sectionKey, ariaLabel, extraClass = '') {
    const className = ('ws-cmd-stat ' + String(extraClass || '')).trim();
    return (
      '<button type="button" class="' +
      escapeHtml(className) +
      '" data-cmd-section="' +
      escapeHtml(sectionKey) +
      '" aria-label="' +
      escapeHtml(ariaLabel) +
      '"><div class="ws-v">' +
      escapeHtml(value) +
      '</div><div class="ws-l">' +
      escapeHtml(label) +
      '</div></button>'
    );
  }

  commandViewSwitchHTML() {
    const modes = [
      { key: 'details', label: 'Details' },
      { key: 'map', label: 'Map' }
    ];
    return (
      '<div class="ws-cmd-view-switch" role="group" aria-label="Workspace view">' +
      modes
        .map(mode => {
          const active = this.viewMode === mode.key;
          return (
            '<button type="button" class="ws-cmd-view-btn' +
            (active ? ' is-active' : '') +
            '" data-cmd-view-mode="' +
            escapeHtml(mode.key) +
            '" aria-pressed="' +
            (active ? 'true' : 'false') +
            '">' +
            escapeHtml(mode.label) +
            '</button>'
          );
        })
        .join('') +
      '</div>'
    );
  }

  projectOpenActionHTML() {
    const page = this.page || {};
    if (typeof page.hasProjectEntry !== 'function' || !page.hasProjectEntry()) return '';

    const busy = page.projectOpenBusy === true;
    return (
      '<button type="button" class="ws-cmd-nav-btn" data-cmd-open-project ' +
      'aria-label="Open project using the system default application" aria-busy="' +
      (busy ? 'true' : 'false') +
      '"' +
      (busy ? ' disabled' : '') +
      '>' +
      (busy ? 'Opening Project...' : 'Open Project') +
      '</button>'
    );
  }

  isGroupWorkspace() {
    const ws = (this.page && this.page.workspace) || {};
    return (
      String(ws.kind || '')
        .trim()
        .toLowerCase() === 'group'
    );
  }

  hexToRgba(hex, alpha) {
    if (!hex || typeof hex !== 'string') return '';
    const cleaned = hex.replace('#', '').trim();
    if (cleaned.length !== 6) return '';
    const r = parseInt(cleaned.slice(0, 2), 16);
    const g = parseInt(cleaned.slice(2, 4), 16);
    const b = parseInt(cleaned.slice(4, 6), 16);
    if ([r, g, b].some(Number.isNaN)) return '';
    return 'rgba(' + r + ', ' + g + ', ' + b + ', ' + alpha + ')';
  }

  groupColor(ws) {
    // The detail workspace object carries no color; the tree node loaded by the
    // members panel is the reliable source. Fall back to any color on ws.
    const panelGroup = this.page && this.page.membersPanel && this.page.membersPanel.group;
    const fromPanel = panelGroup ? panelGroup.color : '';
    return String(fromPanel || (ws && ws.color) || '').trim();
  }

  groupAccentStyle(ws) {
    if (!this.isGroupWorkspace()) return '';
    const color = this.groupColor(ws);
    if (!color) return '';
    const soft = this.hexToRgba(color, 0.14);
    const line = this.hexToRgba(color, 0.5);
    const vars = ['--ws-group-accent: ' + color];
    if (soft) vars.push('--ws-group-accent-soft: ' + soft);
    if (line) vars.push('--ws-group-accent-line: ' + line);
    return ' style="' + escapeHtml(vars.join('; ')) + '"';
  }

  commandBarHTML(ws, name, mode, stats) {
    const description = String((ws && ws.description) || '').trim();
    const isGroup = this.isGroupWorkspace();
    const kicker = isGroup ? 'Detachment · Command' : 'Outpost · Command';
    const groupBadge = isGroup
      ? '<span class="ws-cmd-group-badge" title="Group workspace">Group</span>'
      : '';
    const tags = this.workspaceTags();
    const isLongDescription = Array.from(description).length > 150;
    const descriptionClass =
      'ws-cmd-description' +
      (!description ? ' is-empty' : '') +
      (isLongDescription && !this.identityExpanded ? ' is-collapsed' : '');
    const descriptionText = description || 'No description';
    const workflowHref = this.workflowHref();
    const workflowLabel = mode ? 'Workflow · ' + mode : '';
    const subtitle = this.commandSubtitle(mode);

    return (
      '<header class="ws-cmd-topbar' +
      (isGroup ? ' is-group' : '') +
      '"' +
      this.groupAccentStyle(ws) +
      '>' +
      '<div class="ws-cmd-nav">' +
      '<a class="ws-cmd-nav-btn" href="/workspaces" aria-label="Back to workspaces">Workspaces</a>' +
      '<a class="ws-cmd-nav-btn" href="' +
      escapeHtml(this.workspaceRoute('/canvas')) +
      '">Canvas</a>' +
      '<a class="ws-cmd-nav-btn" href="' +
      escapeHtml(this.workspaceRoute('/diagnostics')) +
      '">Diagnostics</a>' +
      '<a class="ws-cmd-nav-btn" href="' +
      escapeHtml(workflowHref) +
      '">Orchestration Skills</a>' +
      this.projectOpenActionHTML() +
      this.commandViewSwitchHTML() +
      '</div>' +
      '<div class="ws-cmd-crest">' +
      '<svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">' +
      '<path d="M3 21V9l9-6 9 6v12"/><path d="M9 21v-6h6v6"/><path d="M3 21h18"/></svg>' +
      '</div>' +
      '<div class="ws-cmd-title">' +
      '<div class="ws-kicker"><span class="ws-dot"></span><span class="ws-tick">' +
      escapeHtml(kicker) +
      '</span></div>' +
      '<div class="ws-cmd-title-row">' +
      '<h2>' +
      escapeHtml(name) +
      '</h2>' +
      groupBadge +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="name" aria-label="Edit workspace name">Edit</button>' +
      '</div>' +
      '<div class="ws-sub" id="workspace-command-subtitle" data-workflow-label="' +
      escapeHtml(workflowLabel) +
      '"' +
      (subtitle ? '' : ' hidden') +
      '>' +
      escapeHtml(subtitle) +
      '</div>' +
      '<div class="ws-cmd-description-row">' +
      '<p class="' +
      descriptionClass +
      '">' +
      escapeHtml(descriptionText) +
      '</p>' +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="description" aria-label="Edit workspace description and intent">Edit</button>' +
      (isLongDescription
        ? '<button type="button" class="ws-cmd-mini-btn" data-cmd-toggle-description aria-expanded="' +
          (this.identityExpanded ? 'true' : 'false') +
          '">' +
          (this.identityExpanded ? 'Less' : 'More') +
          '</button>'
        : '') +
      '</div>' +
      '<div class="ws-cmd-tags-row">' +
      '<div class="ws-cmd-tags">' +
      this.commandTagsHTML(tags) +
      '</div>' +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="tags" aria-label="Edit workspace tags">Edit</button>' +
      '</div>' +
      this.identityEditorHTML(name, description) +
      '</div>' +
      '<div class="ws-cmd-readout">' +
      this.statBoxHTML(stats.agents, 'Agents', 'agents', 'View agents') +
      this.statBoxHTML(
        stats.openTasks,
        'Open Tasks',
        'tasks',
        'View open tasks',
        this.hasAttentionTasks() ? 'is-alert' : ''
      ) +
      this.statBoxHTML(
        stats.tools,
        'Tools',
        'tools',
        'Open tools: MCP, skills, plugins, and find tools'
      ) +
      '</div>' +
      '</header>'
    );
  }

  renderMissionPanel() {
    const summary = this.missionSummary();
    const missionText = String(summary.mission || '').trim();
    const title = summary.title || (missionText ? 'Current goal' : 'Workspace goal');
    const text = summary.text || missionText || 'Loading workspace goal...';
    const statusLabel = summary.label || 'Loading';
    const statusClass = summary.className || 'is-loading';
    const cadence = summary.cadenceLabel || 'Cadence: loading';
    const nextRun = summary.nextLabel || 'Next: loading';
    const lastRun = summary.lastLabel || 'Last: loading';
    const findingsHref =
      summary.findingsHref ||
      (this.workspaceId()
        ? '/action-center?workspace=' + encodeURIComponent(this.workspaceId())
        : '/action-center');
    const findingsLabel = summary.findingsLabel || 'Findings';
    const runDisabled = summary.canRun === true ? '' : ' disabled';
    const runTitle = summary.runTitle || 'Set a goal before running';
    const actionStatus = summary.actionStatus || '';

    const editLabel = missionText ? 'Edit Goal' : 'Set Goal';
    const missionClass = 'ws-cmd-mission' + (missionText ? '' : ' is-empty');

    return (
      '<section class="' +
      missionClass +
      '" id="workspace-command-mission-card" aria-labelledby="workspace-command-mission-title">' +
      '<div class="ws-cmd-mission-main">' +
      '<div class="ws-cmd-mission-head">' +
      '<span class="ws-cmd-mission-kicker">Mission</span>' +
      '<span class="ws-cmd-mission-status ' +
      escapeHtml(statusClass) +
      '" id="workspace-command-mission-status">' +
      escapeHtml(statusLabel) +
      '</span>' +
      '</div>' +
      '<h3 id="workspace-command-mission-title" class="ws-cmd-mission-title">' +
      escapeHtml(title) +
      '</h3>' +
      '<p id="workspace-command-mission-text" class="ws-cmd-mission-text' +
      (missionText ? '' : ' is-empty') +
      '">' +
      escapeHtml(text) +
      '</p>' +
      '<div class="ws-cmd-mission-meta" aria-label="Mission automation timing">' +
      '<span id="workspace-command-mission-cadence">' +
      escapeHtml(cadence) +
      '</span>' +
      '<span id="workspace-command-mission-next-run">' +
      escapeHtml(nextRun) +
      '</span>' +
      '<span id="workspace-command-mission-last-run">' +
      escapeHtml(lastRun) +
      '</span>' +
      '</div>' +
      '<div class="ws-cmd-mission-action-status" id="workspace-command-mission-action-status" aria-live="polite">' +
      escapeHtml(actionStatus) +
      '</div>' +
      '</div>' +
      '<div class="ws-cmd-mission-actions">' +
      '<button type="button" class="ws-cmd-mission-btn" id="workspace-command-mission-edit" data-cmd-mission-action="edit">' +
      editLabel +
      '</button>' +
      '<button type="button" class="ws-cmd-mission-btn is-primary" id="workspace-command-mission-run" data-cmd-mission-action="run"' +
      runDisabled +
      ' title="' +
      escapeHtml(runTitle) +
      '">Run now</button>' +
      '<a class="ws-cmd-mission-btn" id="workspace-command-mission-findings" href="' +
      escapeHtml(findingsHref) +
      '">' +
      escapeHtml(findingsLabel) +
      '</a>' +
      '</div>' +
      '</section>'
    );
  }

  commandTagsHTML(tags) {
    const arr = Array.isArray(tags) ? tags : [];
    if (!arr.length) return '<span class="ws-cmd-tag-empty">No tags</span>';
    const limit = 8;
    const shown = arr
      .slice(0, limit)
      .map(tag => '<span class="ws-cmd-tag">' + escapeHtml(tag) + '</span>')
      .join('');
    const more =
      arr.length > limit
        ? '<span class="ws-cmd-tag is-more">+' + (arr.length - limit) + '</span>'
        : '';
    return shown + more;
  }

  identityEditorHTML(name, description) {
    const mode = this.identityEditMode;
    if (!mode) return '';
    if (mode === 'name' || mode === 'description') {
      const isDescription = mode === 'description';
      const value = isDescription ? description : name;
      const field = isDescription
        ? '<textarea class="ws-cmd-identity-field" data-cmd-identity-input rows="2">' +
          escapeHtml(value) +
          '</textarea>'
        : '<input class="ws-cmd-identity-field" data-cmd-identity-input type="text" value="' +
          escapeHtml(value) +
          '">';
      return (
        '<form class="ws-cmd-identity-editor" data-cmd-identity-form="' +
        escapeHtml(mode) +
        '">' +
        field +
        '<div class="ws-cmd-identity-actions">' +
        '<button type="submit" class="ws-cmd-identity-save"' +
        (this.identitySaving ? ' disabled' : '') +
        '>Save</button>' +
        '<button type="button" class="ws-cmd-identity-cancel" data-cmd-cancel-identity>Cancel</button>' +
        '</div>' +
        '</form>'
      );
    }
    if (mode === 'tags') {
      return (
        '<div class="ws-cmd-identity-editor" data-cmd-identity-form="tags">' +
        '<div class="ws-cmd-tags-editor-mount" data-cmd-tags-mount></div>' +
        (this.commandTagError
          ? '<div class="ws-cmd-identity-error" role="alert">' +
            escapeHtml(this.commandTagError) +
            '</div>'
          : '') +
        '<div class="ws-cmd-identity-actions">' +
        '<button type="button" class="ws-cmd-identity-save" data-cmd-save-tags' +
        (this.identitySaving ? ' disabled' : '') +
        '>Save</button>' +
        '<button type="button" class="ws-cmd-identity-cancel" data-cmd-cancel-identity>Cancel</button>' +
        '</div>' +
        '</div>'
      );
    }
    return '';
  }

  render() {
    if (!this.container) return;
    this.captureAgentDeckViewState();
    if (this.commandTagInput) {
      try {
        this.commandTagDraft = this.commandTagInput.getTags();
      } catch (err) {
        /* keep existing draft */
      }
      this.destroyCommandTagInput();
    }
    const ws = (this.page && this.page.workspace) || {};
    const name = String(ws.name || 'Workspace');
    const mode = this.opsModeLabel();
    const stats = this.computeStats();

    const body =
      this.viewMode === 'map'
        ? this.renderOperationsMap()
        : '<div class="ws-cmd-layout">' +
          '<main class="ws-cmd-main">' +
          this.renderMissionPanel() +
          '<section class="ws-cmd-garrison">' +
          this.renderGarrison() +
          '</section>' +
          '</main>' +
          '<aside class="ws-cmd-rail">' +
          this.renderRail() +
          '</aside>' +
          '</div>';

    this.container.innerHTML = this.commandBarHTML(ws, name, mode, stats) + body;

    this.bindIdentityControls();
    this.bindReadout();
    this.bindMissionPanel();
    if (this.viewMode === 'map') {
      this.bindOperationsMap();
    } else {
      this.bindGarrison();
      this.bindRail();
    }
    this.mountCommandTagInput();
    this.syncMissionPanel();
    this.syncSharedSurfaces();
    this.mountNoteFilterBar();
    this.restoreAgentDeckViewState();
    this.hydrateActiveAgentPrompt();

    // The stat manager modal lives inside the .ws-cmd container (so it inherits
    // the tactical tokens) but survives full re-renders: re-attach + repaint it.
    if (this.statModalEl && this.container && this.container.appendChild) {
      this.container.appendChild(this.statModalEl);
      if (this.statModalSection) {
        this.renderStatModalBody();
        this.setCommandBackgroundInert(true);
      }
    }

    // The task drawer likewise survives full re-renders: re-attach + repaint it.
    if (this.taskDrawerEl && this.container && this.container.appendChild) {
      this.container.appendChild(this.taskDrawerEl);
      if (this.taskDrawerOpen) {
        this.taskDrawerEl.hidden = false;
        this.renderTaskDrawerBody();
      }
    }

    // The sticky execution tray survives full re-renders too — monitoring never
    // depends on the DOM being present (FR50-FR51).
    if (this.trayEl && this.container && this.container.appendChild) {
      this.container.appendChild(this.trayEl);
      if (this.trayOpen) {
        this.trayEl.hidden = false;
        this.renderTrayBody();
      }
    }
  }

  bindIdentityControls() {
    const root = this.container && this.container.querySelector('.ws-cmd-topbar');
    if (!root) return;

    root.addEventListener('click', event => {
      const openProjectBtn = event.target.closest('[data-cmd-open-project]');
      if (openProjectBtn) {
        const page = this.page || {};
        if (!openProjectBtn.disabled && typeof page.openProject === 'function') {
          void page.openProject();
        }
        return;
      }
      const viewBtn = event.target.closest('[data-cmd-view-mode]');
      if (viewBtn) {
        this.setCommandViewMode(viewBtn.getAttribute('data-cmd-view-mode'), { focus: false });
        return;
      }
      const editBtn = event.target.closest('[data-cmd-edit-identity]');
      if (editBtn) {
        const field = editBtn.getAttribute('data-cmd-edit-identity');
        // "Edit description" now opens the full Workspace Intent editor (description +
        // systems + capabilities + key context), which replaced the Intent & Setup tab.
        // Name and tags keep their lightweight inline editors.
        if (field === 'description') {
          this.openStatModal('intent', editBtn);
        } else {
          this.startIdentityEdit(field);
        }
        return;
      }
      const toggleBtn = event.target.closest('[data-cmd-toggle-description]');
      if (toggleBtn) {
        this.identityExpanded = !this.identityExpanded;
        this.render();
        return;
      }
      const cancelBtn = event.target.closest('[data-cmd-cancel-identity]');
      if (cancelBtn) {
        this.cancelIdentityEdit();
        return;
      }
      const saveTagsBtn = event.target.closest('[data-cmd-save-tags]');
      if (saveTagsBtn) {
        this.saveCommandTags();
      }
    });

    root.addEventListener('submit', event => {
      const form = event.target.closest('[data-cmd-identity-form]');
      if (!form) return;
      event.preventDefault();
      const mode = form.getAttribute('data-cmd-identity-form');
      if (mode === 'name' || mode === 'description') {
        const input = form.querySelector('[data-cmd-identity-input]');
        this.saveIdentityField(mode, input ? input.value : '');
      }
    });

    root.addEventListener('keydown', event => {
      if (event.key === 'Escape' && event.target.closest('[data-cmd-identity-form]')) {
        event.preventDefault();
        this.cancelIdentityEdit();
      }
      if (
        event.key === 'Enter' &&
        event.target.matches('textarea[data-cmd-identity-input]') &&
        (event.metaKey || event.ctrlKey)
      ) {
        event.preventDefault();
        this.saveIdentityField('description', event.target.value);
      }
    });
  }

  startIdentityEdit(mode) {
    const nextMode = String(mode || '').trim();
    if (!['name', 'description', 'tags'].includes(nextMode)) return;
    this.identityEditMode = nextMode;
    this.commandTagError = '';
    if (nextMode === 'tags') this.commandTagDraft = this.workspaceTags();
    this.render();
    const input = this.container && this.container.querySelector('[data-cmd-identity-input]');
    if (input && typeof input.focus === 'function') {
      try {
        input.focus({ preventScroll: true });
      } catch (err) {
        input.focus();
      }
      if (typeof input.select === 'function') input.select();
    }
  }

  cancelIdentityEdit() {
    this.identityEditMode = '';
    this.identitySaving = false;
    this.commandTagError = '';
    this.commandTagDraft = [];
    this.destroyCommandTagInput();
    this.render();
  }

  async saveIdentityField(mode, value) {
    const page = this.page || {};
    if (typeof page.saveWorkspaceIdentityField !== 'function') return;
    const ws = page.workspace || {};
    const currentValue =
      mode === 'description' ? String(ws.description || '') : String(ws.name || '');
    this.identitySaving = true;
    const result = await page.saveWorkspaceIdentityField(mode, value, { currentValue });
    this.identitySaving = false;
    if (result && (result.error || result.invalid)) {
      this.render();
      return;
    }
    this.identityEditMode = '';
    this.render();
  }

  mountCommandTagInput() {
    if (this.identityEditMode !== 'tags' || this.commandTagInput) return;
    const mount = this.container && this.container.querySelector('[data-cmd-tags-mount]');
    if (!mount || !window.OriTagInput?.createTagInput) return;
    this.commandTagInput = window.OriTagInput.createTagInput({
      container: mount,
      initialTags: this.commandTagDraft,
      onChange: tags => {
        this.commandTagDraft = tags;
        this.commandTagError = '';
      }
    });
    this.commandTagInput?.focus?.();
  }

  destroyCommandTagInput() {
    if (!this.commandTagInput) return;
    try {
      this.commandTagInput.destroy?.();
    } catch (err) {
      /* no-op */
    }
    this.commandTagInput = null;
  }

  async saveCommandTags() {
    const page = this.page || {};
    if (typeof page.saveWorkspaceTagList !== 'function') return;
    const tags = this.commandTagInput ? this.commandTagInput.getTags() : this.commandTagDraft;
    this.identitySaving = true;
    this.commandTagError = '';
    try {
      await page.saveWorkspaceTagList(tags);
      this.identityEditMode = '';
      this.commandTagDraft = [];
      this.destroyCommandTagInput();
    } catch (error) {
      this.commandTagError = error.message || 'Failed to update tags';
    } finally {
      this.identitySaving = false;
      this.render();
    }
  }

  bindReadout() {
    const root = this.container && this.container.querySelector('.ws-cmd-readout');
    if (!root) return;
    root.addEventListener('click', event => {
      const sectionBtn = event.target.closest('[data-cmd-section]');
      if (!sectionBtn) return;
      this.openStatModal(sectionBtn.getAttribute('data-cmd-section'), sectionBtn);
    });
  }

  bindMissionPanel() {
    const root = this.container && this.container.querySelector('.ws-cmd-mission');
    if (!root) return;
    root.addEventListener('click', event => {
      const btn = event.target.closest('[data-cmd-mission-action]');
      if (!btn) return;
      const action = btn.getAttribute('data-cmd-mission-action');
      const mission = typeof window !== 'undefined' ? window.workspaceMission : null;
      if (action === 'edit' && mission && typeof mission.openGoalModal === 'function') {
        mission.openGoalModal();
      } else if (action === 'run' && mission && typeof mission.runNow === 'function') {
        mission.runNow(btn);
      }
    });
  }

  syncMissionPanel() {
    const mission = typeof window !== 'undefined' ? window.workspaceMission : null;
    if (!mission) return;
    if (typeof mission.renderCommandSurfaces === 'function') {
      mission.renderCommandSurfaces();
    }
    // The Command view is the primary mission surface, but the mission module's
    // initial auto-load is gated on the legacy Detailed goal card
    // (#workspace-detail-goal-card), which no longer exists — so without this the
    // panel sits at "Loading" until the user opens the Goal Settings tab. Kick
    // off the first load ourselves when no state has been fetched yet.
    if (
      !this._missionLoadKicked &&
      typeof mission.getState === 'function' &&
      !mission.getState() &&
      typeof mission.reload === 'function'
    ) {
      this._missionLoadKicked = true;
      Promise.resolve(mission.reload()).catch(() => {
        // Let a later render retry if the first load failed.
        this._missionLoadKicked = false;
      });
    }
  }

  // ---------- stat manager modal (agents / tasks / mcp / skills) ----------

  statSectionMeta(section) {
    switch (String(section || '')) {
      case 'agents':
        return { title: 'Agents', addLabel: '＋ Add Agent' };
      case 'tasks':
        return { title: this.taskModalShowAll ? 'Tasks' : 'Open Tasks', addLabel: '＋ Add Task' };
      case 'mcp':
        return { title: 'MCP Servers', addLabel: '＋ Add MCP' };
      case 'skills':
        return { title: 'Skills', addLabel: '＋ Add Skill' };
      case 'tools':
        return { title: 'Tools', addLabel: '' };
      case 'settings':
        return { title: 'Manager Settings', addLabel: '' };
      case 'mission':
        return { title: 'Goal Settings', addLabel: '' };
      case 'intent':
        return { title: 'Workspace Intent', addLabel: '' };
      default:
        return null;
    }
  }

  openStatModal(sectionKey, trigger) {
    const section = String(sectionKey || '').trim();
    if (!this.statSectionMeta(section)) return;
    this.statModalSection = section;
    if (section === 'tasks') {
      this.taskModalShowAll = false;
      this.taskModalBoardMode = false;
    }
    if (section === 'tools') {
      this.activeToolsTab = this.toolsTab(this.activeToolsTab).key;
    }
    this.statModalTrigger = trigger || null;
    const el = this.ensureStatModal();
    if (!el) return;
    // Un-hide before rendering the body: mounting the live config surface into the
    // MCP/Skills modal is gated on the modal being visible (syncConfigModalSurface).
    el.hidden = false;
    this.renderStatModalBody();
    this.setCommandBackgroundInert(true);
    const panel = el.querySelector('.ws-cmd-modal-panel');
    if (panel && typeof panel.focus === 'function') {
      try {
        panel.focus({ preventScroll: true });
      } catch (err) {
        panel.focus();
      }
    }
  }

  closeStatModal() {
    const trigger = this.statModalTrigger;
    const wasBoard = this.taskModalBoardMode;
    const wasConfig = this.sectionUsesConfigSurface(this.statModalSection);
    const wasTools = this.statModalSection === 'tools';
    this.statModalSection = '';
    this.statModalTrigger = null;
    this.taskModalBoardMode = false;
    // Hand the live board node back to its Detailed home before hiding.
    if (wasBoard) {
      this.restoreSharedSurface('board');
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
      if (page && typeof page.setView === 'function') page.setView('list');
    }
    // Hand the live config surface back, then re-offer it to the Systems rail if it is
    // still expanded on a config-host tab (single-node arbitration — see PRD FR6.26).
    if (wasConfig) {
      this.releaseConfigModalSurface();
    }
    if (wasTools) {
      this.releaseToolsModalSurface();
    }
    if (this.statModalEl) this.statModalEl.hidden = true;
    this.setCommandBackgroundInert(false);
    if (trigger && typeof trigger.focus === 'function') trigger.focus();
  }

  setCommandBackgroundInert(isInert) {
    if (!this.container || !this.container.children) return;
    Array.from(this.container.children).forEach(child => {
      if (child === this.statModalEl) return;
      if (isInert) {
        child.setAttribute('aria-hidden', 'true');
        try {
          child.inert = true;
        } catch (err) {
          /* inert may be readonly in tests */
        }
      } else {
        child.removeAttribute('aria-hidden');
        try {
          child.inert = false;
        } catch (err) {
          /* inert may be readonly in tests */
        }
      }
    });
  }

  modalFocusableElements() {
    const panel = this.statModalEl && this.statModalEl.querySelector('.ws-cmd-modal-panel');
    if (!panel || typeof panel.querySelectorAll !== 'function') return [];
    return Array.from(
      panel.querySelectorAll(
        'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
      )
    ).filter(el => el && !el.hidden && typeof el.focus === 'function');
  }

  trapStatModalFocus(event) {
    if (!event || event.key !== 'Tab') return;
    const focusable = this.modalFocusableElements();
    if (!focusable.length) {
      event.preventDefault();
      const panel = this.statModalEl && this.statModalEl.querySelector('.ws-cmd-modal-panel');
      if (panel && typeof panel.focus === 'function') panel.focus();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const activeEl = typeof document !== 'undefined' ? document.activeElement : null;
    if (event.shiftKey && activeEl === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && activeEl === last) {
      event.preventDefault();
      first.focus();
    }
  }

  ensureStatModal() {
    if (this.statModalEl) return this.statModalEl;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
    const el = document.createElement('div');
    el.className = 'ws-cmd-modal';
    el.hidden = true;
    el.innerHTML =
      '<div class="ws-cmd-modal-backdrop" data-cmd-modal-action="close"></div>' +
      '<div class="ws-cmd-modal-panel" role="dialog" aria-modal="true" tabindex="-1"></div>';
    el.addEventListener('click', event => {
      const toolsTab = event.target.closest('[data-cmd-tools-tab]');
      if (toolsTab) {
        this.setToolsTab(toolsTab.getAttribute('data-cmd-tools-tab'));
        return;
      }
      const btn = event.target.closest('[data-cmd-modal-action]');
      if (!btn) return;
      this.handleStatModalAction(
        btn.getAttribute('data-cmd-modal-action'),
        btn.getAttribute('data-cmd-id')
      );
    });
    el.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        this.closeStatModal();
        return;
      }
      this.trapStatModalFocus(event);
    });
    this.statModalEl = el;
    if (this.container && this.container.appendChild) this.container.appendChild(el);
    return el;
  }

  renderStatModalBody() {
    const el = this.statModalEl;
    if (!el || typeof el.querySelector !== 'function') return;
    const panel = el.querySelector('.ws-cmd-modal-panel');
    if (!panel) return;
    const meta = this.statSectionMeta(this.statModalSection);
    if (meta) panel.setAttribute('aria-label', meta.title);
    const boardMode = this.statModalSection === 'tasks' && this.taskModalBoardMode;
    if (panel.classList) panel.classList.toggle('is-board', boardMode);
    // Config-surface and Tools modals get the wide panel treatment.
    const isConfig = this.statModalHoldsSharedSurface();
    if (panel.classList) panel.classList.toggle('is-config', isConfig);
    panel.innerHTML = this.statModalHTML(this.statModalSection);
    this.syncBoardSurface();
    this.syncConfigModalSurface();
    this.syncToolsModalSurface();
    this.mountTaskFilterBar();
  }

  // Mount the live Workspace config surface into the open MCP/Skills stat modal, showing
  // only the relevant manager pane. Mirrors syncBoardSurface. The single config node is
  // pulled from wherever it currently lives (hidden host or the Systems rail).
  syncConfigModalSurface() {
    const section = this.statModalSection;
    const open = this.statModalEl && !this.statModalEl.hidden;
    if (!this.sectionUsesConfigSurface(section) || !open) return;
    const host = this.statModalEl.querySelector('[data-cmd-config-host]');
    if (!host) return;
    const config = this.mountSharedSurface('config', '#workspace-detail-settings-panel', host);
    if (!config) {
      host.innerHTML =
        '<div class="ws-cmd-modal-empty">Workspace configuration is unavailable.</div>';
      return;
    }
    // is-command-modal hides the config header/tab strip so only the active pane shows.
    if (config.classList) config.classList.add('is-command-modal');
    const tabId = this.configTabIdFor(section);
    if (tabId) this.showConfigTab({ tabId });
    this.refreshConfigData(section);
    // expandMountedConfig must run LAST: refreshConfigData re-collapses the config
    // content, so the rail (syncSystemsSurface) also expands last — mirror that order.
    this.expandMountedConfig(config);
  }

  // Hand the config surface back to its hidden home and, if the Systems rail is still
  // expanded on a config-host tab, re-mount it there so Plugins/Memory/Triggers is not
  // left blank (single-node arbitration).
  releaseConfigModalSurface() {
    const record = this.sharedSurfaceAnchors && this.sharedSurfaceAnchors.config;
    if (record && record.node && record.node.classList) {
      record.node.classList.remove('is-command-modal');
    }
    this.restoreSharedSurface('config');
    this.syncSystemsSurface();
  }

  // Mount the active Tools sub-tab into the Tools modal: MCP/Skills/Plugins mount the
  // config surface (showing that pane); Find Tools mounts the tools card. The two shared
  // surfaces ('config' and 'tools') are mutually exclusive inside the modal.
  syncToolsModalSurface() {
    if (this.statModalSection !== 'tools') return;
    const open = this.statModalEl && !this.statModalEl.hidden;
    if (!open) return;
    const host = this.statModalEl.querySelector('[data-cmd-tools-host]');
    if (!host) return;
    const tab = this.toolsTab(this.activeToolsTab);
    if (tab.host === 'tools') {
      this.restoreSharedSurface('config');
      const tools = this.mountSharedSurface('tools', '#workspace-detail-tools-card', host);
      if (!tools) {
        host.innerHTML = '<div class="ws-cmd-modal-empty">Find Tools is unavailable.</div>';
        return;
      }
      if (tools.style) tools.style.display = '';
      return;
    }
    this.restoreSharedSurface('tools');
    const config = this.mountSharedSurface('config', '#workspace-detail-settings-panel', host);
    if (!config) {
      host.innerHTML =
        '<div class="ws-cmd-modal-empty">Workspace configuration is unavailable.</div>';
      return;
    }
    if (config.classList) config.classList.add('is-command-modal');
    const tabId = this.configTabIdFor(tab.key);
    if (tabId) this.showConfigTab({ tabId });
    this.refreshConfigData(tab.key);
    this.expandMountedConfig(config);
  }

  setToolsTab(key) {
    this.activeToolsTab = this.toolsTab(key).key;
    this.renderStatModalBody();
  }

  // Close-path for the Tools modal: hand both shared surfaces home and re-offer config to
  // the Systems rail if it is still expanded (single-node arbitration).
  releaseToolsModalSurface() {
    const record = this.sharedSurfaceAnchors && this.sharedSurfaceAnchors.config;
    if (record && record.node && record.node.classList) {
      record.node.classList.remove('is-command-modal');
    }
    this.restoreSharedSurface('config');
    this.restoreSharedSurface('tools');
    this.syncSystemsSurface();
  }

  // A config-surface or the Tools modal currently holds a shared surface on loan.
  statModalHoldsSharedSurface() {
    return (
      this.sectionUsesConfigSurface(this.statModalSection) || this.statModalSection === 'tools'
    );
  }

  ensureTaskFilterBar() {
    if (this.taskFilterBar) return this.taskFilterBar;
    const helper = this.tagFilterHelper();
    if (!helper || typeof helper.createTagFilterBar !== 'function') return null;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
    const holder = document.createElement('div');
    this.taskFilterBar = helper.createTagFilterBar({
      container: holder,
      label: 'Tags',
      onChange: () => this.renderStatModalBody()
    });
    return this.taskFilterBar;
  }

  mountTaskFilterBar() {
    if (this.statModalSection !== 'tasks' || this.taskModalBoardMode) return;
    const panel = this.statModalEl && this.statModalEl.querySelector('.ws-cmd-modal-panel');
    const host = panel && panel.querySelector('[data-cmd-task-filter]');
    if (!host) return;
    const bar = this.ensureTaskFilterBar();
    if (!bar || !bar.element) return;
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const helper = this.tagFilterHelper();
    if (
      helper &&
      typeof helper.collectTags === 'function' &&
      typeof bar.setAvailableTags === 'function'
    ) {
      bar.setAvailableTags(helper.collectTags(tasks));
    }
    host.innerHTML = '';
    host.appendChild(bar.element);
  }

  syncBoardSurface({ load = false } = {}) {
    const inBoard =
      this.statModalSection === 'tasks' &&
      this.taskModalBoardMode &&
      this.statModalEl &&
      !this.statModalEl.hidden;
    if (!inBoard) {
      this.restoreSharedSurface('board');
      return;
    }
    const host = this.statModalEl.querySelector('[data-cmd-board-host]');
    if (!host) return;
    const board = this.mountSharedSurface('board', '#workspace-detail-tasks-board', host);
    if (!board) {
      host.innerHTML = '<div class="ws-cmd-modal-empty">Board is unavailable.</div>';
      return;
    }
    if (board.style) board.style.display = '';
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (load && page && typeof page.setView === 'function') page.setView('board');
  }

  statModalHTML(section) {
    const meta = this.statSectionMeta(section);
    if (!meta) return '';
    // The Tools modal groups every capability provider (MCP, Skills, Plugins) plus the
    // Find Tools discovery flow behind one tabbed surface.
    if (section === 'tools') {
      const tabs = this.toolsTabs();
      const active = this.toolsTab(this.activeToolsTab);
      const tabButtons = tabs
        .map(
          tab =>
            '<button type="button" class="ws-cmd-system-tab' +
            (tab.key === active.key ? ' is-active' : '') +
            '" data-cmd-tools-tab="' +
            escapeHtml(tab.key) +
            '" role="tab" aria-selected="' +
            (tab.key === active.key ? 'true' : 'false') +
            '">' +
            escapeHtml(tab.label) +
            '</button>'
        )
        .join('');
      return (
        '<header class="ws-cmd-modal-head">' +
        '<h3 class="ws-cmd-modal-title">' +
        escapeHtml(meta.title) +
        '</h3>' +
        '<span class="ws-cmd-modal-count">' +
        this.statModalCount('tools') +
        '</span>' +
        '<div class="ws-cmd-modal-head-actions">' +
        '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close manager">×</button>' +
        '</div>' +
        '</header>' +
        '<div class="ws-cmd-modal-body ws-cmd-modal-config-body">' +
        '<div class="ws-cmd-system-tabs" role="tablist" aria-label="Workspace tools">' +
        tabButtons +
        '</div>' +
        '<div class="ws-cmd-config-host" data-cmd-tools-host>' +
        '<div class="ws-cmd-modal-empty">Loading ' +
        escapeHtml(active.label) +
        '...</div>' +
        '</div></div>'
      );
    }
    // MCP/Skills mount the live Workspace config surface (full manager) instead of a
    // summary list. The mounted panel carries its own add/edit/delete controls, so the
    // modal header only needs title + count + close (no Add button, no footer link).
    if (this.sectionUsesConfigSurface(section)) {
      // Manager/Goal Settings have no list count; MCP/Skills show their binding count.
      const countChip = this.sectionHasNoCount(section)
        ? ''
        : '<span class="ws-cmd-modal-count">' + this.statModalCount(section) + '</span>';
      return (
        '<header class="ws-cmd-modal-head">' +
        '<h3 class="ws-cmd-modal-title">' +
        escapeHtml(meta.title) +
        '</h3>' +
        countChip +
        '<div class="ws-cmd-modal-head-actions">' +
        '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close manager">×</button>' +
        '</div>' +
        '</header>' +
        '<div class="ws-cmd-modal-body ws-cmd-modal-config-body">' +
        '<div class="ws-cmd-config-host" data-cmd-config-host>' +
        '<div class="ws-cmd-modal-empty">Loading ' +
        escapeHtml(meta.title) +
        '...</div>' +
        '</div></div>'
      );
    }
    const boardMode = section === 'tasks' && this.taskModalBoardMode;
    const taskFilterHost =
      section === 'tasks'
        ? '<div class="ws-cmd-modal-note-filter" data-cmd-task-filter></div>'
        : '';
    const body = boardMode
      ? '<div class="ws-cmd-modal-body ws-cmd-modal-board-body">' +
        '<div class="ws-cmd-board-host" data-cmd-board-host>' +
        '<div class="ws-cmd-modal-empty">Loading board...</div>' +
        '</div></div>'
      : '<div class="ws-cmd-modal-body">' + taskFilterHost + this.statModalRows(section) + '</div>';
    return (
      '<header class="ws-cmd-modal-head">' +
      '<h3 class="ws-cmd-modal-title">' +
      escapeHtml(meta.title) +
      '</h3>' +
      '<span class="ws-cmd-modal-count">' +
      this.statModalCount(section) +
      '</span>' +
      '<div class="ws-cmd-modal-head-actions">' +
      this.taskViewToggleHTML(section) +
      (boardMode ? '' : this.statModalFilterToggleHTML(section)) +
      '<button type="button" class="ws-cmd-modal-add" data-cmd-modal-action="add">' +
      escapeHtml(meta.addLabel) +
      '</button>' +
      '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close manager">×</button>' +
      '</div>' +
      '</header>' +
      body
    );
  }

  // These stat sections host the live Workspace config surface in the modal:
  // MCP and Skills (header stat boxes), Manager Settings (entry-agent card gear),
  // and Goal Settings (the goal modal's "Advanced settings" button).
  sectionUsesConfigSurface(section) {
    const key = String(section || '');
    return (
      key === 'mcp' ||
      key === 'skills' ||
      key === 'settings' ||
      key === 'mission' ||
      key === 'intent'
    );
  }

  // Config sections that carry no list count in the modal header.
  sectionHasNoCount(section) {
    const key = String(section || '');
    return key === 'settings' || key === 'mission' || key === 'intent';
  }

  taskViewToggleHTML(section) {
    if (String(section || '') !== 'tasks') return '';
    const board = this.taskModalBoardMode;
    return (
      '<div class="ws-cmd-modal-viewtoggle" role="tablist" aria-label="Task view">' +
      '<button type="button" class="ws-cmd-modal-view' +
      (board ? '' : ' is-active') +
      '" data-cmd-modal-action="view-list" aria-pressed="' +
      (board ? 'false' : 'true') +
      '">List</button>' +
      '<button type="button" class="ws-cmd-modal-view' +
      (board ? ' is-active' : '') +
      '" data-cmd-modal-action="view-board" aria-pressed="' +
      (board ? 'true' : 'false') +
      '">Board</button>' +
      '</div>'
    );
  }

  statModalFilterToggleHTML(section) {
    if (String(section || '') !== 'tasks') return '';
    const label = this.taskModalShowAll ? 'Show open' : 'Show all';
    const pressed = this.taskModalShowAll ? 'true' : 'false';
    return (
      '<button type="button" class="ws-cmd-modal-filter" data-cmd-modal-action="toggle-task-filter" aria-pressed="' +
      pressed +
      '">' +
      escapeHtml(label) +
      '</button>'
    );
  }

  statModalCount(section) {
    switch (String(section || '')) {
      case 'agents':
        return this.agentRowData().length;
      case 'tasks':
        return this.taskRowData({ includeAll: this.taskModalShowAll }).length;
      case 'mcp':
        return this.mcpRowData().length;
      case 'skills':
        return this.skillRowData().length;
      case 'tools':
        return this.mcpRowData().length + this.skillRowData().length;
      default:
        return 0;
    }
  }

  statModalRows(section) {
    switch (String(section || '')) {
      case 'agents':
        return this.agentRowsHTML();
      case 'tasks':
        return this.taskRowsHTML();
      default:
        return '';
    }
  }

  modalEmptyHTML(text) {
    return '<div class="ws-cmd-modal-empty">' + escapeHtml(text) + '</div>';
  }

  agentRowData() {
    const page = this.page || {};
    let groups = [];
    try {
      groups = page.buildAgentGroups() || [];
    } catch (err) {
      groups = [];
    }
    return groups.filter(g => g && g.isWorkspaceAgent && !g.isUnassigned);
  }

  agentRowsHTML() {
    const page = this.page || {};
    const groups = this.agentRowData();
    if (!groups.length) return this.modalEmptyHTML('No agents yet. Add one to build the roster.');
    return groups
      .map(group => {
        const name = String(group.name || 'Agent');
        const encoded = encodeURIComponent(name);
        const keeper = page.isWorkspaceEntryAgent ? page.isWorkspaceEntryAgent(name) : false;
        const avatar = page.getAgentAvatarPresentation
          ? page.getAgentAvatarPresentation(name)
          : { initials: name.slice(0, 2).toUpperCase(), style: '' };
        const status = page.getAgentRosterStatus
          ? page.getAgentRosterStatus(name)
          : { key: 'idle', label: 'Idle' };
        const tone = this.statusTone(status.key, status.label);
        let modelLabel = '';
        if (page.getAgentProfile && page.getAgentModelPresentation) {
          const m = page.getAgentModelPresentation(page.getAgentProfile(name));
          modelLabel = m && !m.empty ? m.model : '';
        }
        let skillCount = 0;
        if (page.getAgentSkillSummary) {
          const sk = page.getAgentSkillSummary(name);
          skillCount = (sk && sk.count) || 0;
        }
        const chips =
          (keeper ? '<span class="ws-cmd-mchip is-keeper">★ Entry Agent</span>' : '') +
          '<span class="ws-cmd-mchip">' +
          escapeHtml(modelLabel || '—') +
          '</span>' +
          '<span class="ws-cmd-mchip">Skills · ' +
          skillCount +
          '</span>';
        const removeCtl = keeper
          ? '<span class="ws-cmd-lock" title="Entry agent — can\'t be removed">🔒</span>'
          : '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
            escapeHtml(encoded) +
            '" title="Remove agent" aria-label="Remove ' +
            escapeHtml(name) +
            '">✕</button>';
        return (
          '<div class="ws-cmd-mrow">' +
          '<span class="ws-cmd-mrow-av" style="' +
          escapeHtml(avatar.style || '') +
          '">' +
          escapeHtml(avatar.initials) +
          '</span>' +
          '<div class="ws-cmd-mrow-main">' +
          '<div class="ws-cmd-mrow-name"><span class="ws-cmd-led ' +
          tone +
          '"></span>' +
          escapeHtml(name) +
          '</div>' +
          '<div class="ws-cmd-mrow-chips">' +
          chips +
          '</div>' +
          '</div>' +
          '<div class="ws-cmd-mrow-actions">' +
          '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="edit" data-cmd-id="' +
          escapeHtml(encoded) +
          '" title="Edit model" aria-label="Edit model for ' +
          escapeHtml(name) +
          '">Model</button>' +
          removeCtl +
          '</div>' +
          '</div>'
        );
      })
      .join('');
  }

  isOpenTask(task) {
    const status = String((task && task.status) || '').toLowerCase();
    return status === 'pending' || status === 'in_progress';
  }

  taskFilterActiveTags() {
    const bar = this.taskFilterBar;
    return bar && typeof bar.getActiveTags === 'function' ? bar.getActiveTags() : [];
  }

  applyTaskTagFilter(tasks) {
    const all = Array.isArray(tasks) ? tasks : [];
    const active = this.taskFilterActiveTags();
    const helper = this.tagFilterHelper();
    if (!active.length || !helper || typeof helper.filterItems !== 'function') return all;
    return helper.filterItems(all, active);
  }

  taskRowData({ includeAll = false } = {}) {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const base = includeAll ? tasks : tasks.filter(task => this.isOpenTask(task));
    return this.applyTaskTagFilter(base);
  }

  taskRowsHTML() {
    const tasks = this.taskRowData({ includeAll: this.taskModalShowAll });
    if (!tasks.length) {
      return this.modalEmptyHTML(
        this.taskModalShowAll
          ? 'No tasks yet. Add one to get started.'
          : 'No open tasks. Use Show all to view completed tasks.'
      );
    }
    return tasks
      .map(t => {
        const id = String(t.id || '');
        const label = String(t.description || t.name || t.title || 'Task');
        const assignee = String(t.to || t.agent_name || t.assigned_to || '');
        const tone = this.taskTone(t.status);
        const statusText = String(t.status || 'pending').replace('_', ' ');
        return (
          '<div class="ws-cmd-mrow">' +
          '<div class="ws-cmd-mrow-main">' +
          '<div class="ws-cmd-mrow-name">' +
          escapeHtml(label) +
          '</div>' +
          '<div class="ws-cmd-mrow-chips">' +
          '<span class="ws-cmd-mchip ws-cmd-q-status ' +
          tone +
          '">' +
          escapeHtml(statusText) +
          '</span>' +
          (assignee ? '<span class="ws-cmd-mchip">' + escapeHtml(assignee) + '</span>' : '') +
          '</div>' +
          '</div>' +
          '<div class="ws-cmd-mrow-actions">' +
          '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="run" data-cmd-id="' +
          escapeHtml(id) +
          '" title="Run" aria-label="Run task ' +
          escapeHtml(label) +
          '">▶</button>' +
          '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="open" data-cmd-id="' +
          escapeHtml(id) +
          '" title="Open" aria-label="Open task ' +
          escapeHtml(label) +
          '">↗</button>' +
          '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
          escapeHtml(id) +
          '" title="Delete" aria-label="Delete task ' +
          escapeHtml(label) +
          '">✕</button>' +
          '</div>' +
          '</div>'
        );
      })
      .join('');
  }

  mcpRowData() {
    const page = this.page || {};
    if (typeof page.getWorkspaceMCPBindings === 'function') {
      try {
        return page.getWorkspaceMCPBindings({ includeDisabled: true }) || [];
      } catch (err) {
        /* fall through */
      }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.mcp_bindings) ? ws.mcp_bindings : [];
  }

  skillRowData() {
    const page = this.page || {};
    if (typeof page.getWorkspaceSkillBindings === 'function') {
      try {
        return page.getWorkspaceSkillBindings({ includeDisabled: true }) || [];
      } catch (err) {
        /* fall through */
      }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.skill_bindings) ? ws.skill_bindings : [];
  }

  handleStatModalAction(action, id) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null) || {};
    const section = this.statModalSection;
    const a = String(action || '');
    if (a === 'close') {
      this.closeStatModal();
      return;
    }
    if (a === 'toggle-task-filter' && section === 'tasks') {
      this.taskModalShowAll = !this.taskModalShowAll;
      this.renderStatModalBody();
      return;
    }
    if ((a === 'view-board' || a === 'view-list') && section === 'tasks') {
      const nextBoard = a === 'view-board';
      if (this.taskModalBoardMode === nextBoard) return;
      this.taskModalBoardMode = nextBoard;
      if (!nextBoard) {
        this.restoreSharedSurface('board');
        if (typeof page.setView === 'function') page.setView('list');
        this.renderStatModalBody();
      } else {
        this.renderStatModalBody();
        this.syncBoardSurface({ load: true });
      }
      return;
    }
    // Add/Edit hand off to an existing modal, so close ours first (avoids stacked
    // overlays). Run/Delete stay in place; the page reload triggers refresh().
    // Note: mcp/skills mount the live manager surface (with its own controls), so their
    // add/edit/delete arrive through that panel's handlers, not this switch.
    switch (section) {
      case 'agents':
        if (a === 'add') {
          this.closeStatModal();
          if (typeof page.openAddAgentModal === 'function') page.openAddAgentModal();
        } else if (a === 'edit') {
          this.closeStatModal();
          if (typeof page.openAgentModelModal === 'function') page.openAgentModelModal(id);
        } else if (a === 'delete') {
          if (typeof page.removeAgentFromWorkspace === 'function')
            page.removeAgentFromWorkspace(id);
        }
        break;
      case 'tasks':
        if (a === 'add') {
          this.closeStatModal();
          if (typeof page.showAddTaskModal === 'function') page.showAddTaskModal();
        } else if (a === 'run') {
          if (typeof page.executeTask === 'function') page.executeTask(id);
        } else if (a === 'open') {
          if (typeof page.openTask === 'function') page.openTask(id);
        } else if (a === 'delete') {
          if (typeof page.deleteTask === 'function') page.deleteTask(id);
        }
        break;
      default:
        break;
    }
  }

  toggleRailManager(sectionKey) {
    const section = String(sectionKey || '').trim();
    if (!section) return;
    this.activeRailSection = this.activeRailSection === section ? '' : section;
    this.render();
  }

  handleNoteAction(action) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page) return;
    switch (String(action || '')) {
      case 'select-all':
        if (typeof page.toggleSelectAllNotes === 'function') page.toggleSelectAllNotes();
        this.render();
        break;
      case 'copy':
        if (typeof page.copySelectedNotesToClipboard === 'function')
          page.copySelectedNotesToClipboard();
        break;
      case 'delete':
        if (typeof page.deleteSelectedNotes === 'function') page.deleteSelectedNotes();
        break;
      default:
        break;
    }
  }

  runRailPrimaryAction(sectionKey, triggerButton) {
    const page = this.page || window.workspaceDetail;
    switch (String(sectionKey || '')) {
      case 'notes':
        if (page && typeof page.showNoteModal === 'function') page.showNoteModal();
        break;
      case 'schedules':
        if (page && typeof page.showSchedulesModal === 'function') page.showSchedulesModal();
        break;
      case 'sessions':
        if (page && typeof page.createNewSession === 'function') page.createNewSession();
        break;
      case 'folders':
        if (page && typeof page.showAddDirectoryModal === 'function')
          page.showAddDirectoryModal(triggerButton);
        break;
      case 'files':
        if (page && typeof page.showFileModal === 'function') page.showFileModal();
        break;
      case 'systems':
        this.openSystemTab(this.activeSystemTab || 'memory');
        break;
      case 'members':
        if (this.activeRailSection !== 'members') {
          this.activeRailSection = 'members';
          this.render();
        }
        if (page && page.membersPanel && typeof page.membersPanel.openAddPicker === 'function') {
          page.membersPanel.openAddPicker();
        }
        break;
      default:
        break;
    }
  }

  openRailItem(sectionKey, id, source) {
    const page = this.page || window.workspaceDetail;
    if (!page || !id) return;

    switch (String(sectionKey || '')) {
      case 'notes': {
        const note = (Array.isArray(page.notes) ? page.notes : []).find(
          item => String(item.id || '') === id
        );
        if (note && typeof page.showNoteModal === 'function') page.showNoteModal(note);
        break;
      }
      case 'schedules':
        if (typeof page.openSchedule === 'function') page.openSchedule(id);
        break;
      case 'sessions':
        if (typeof page.openSession === 'function') page.openSession(id);
        break;
      case 'folders':
        if (typeof page.openDirectoryExplorer === 'function') {
          page.openDirectoryExplorer(id, source || 'reference');
        }
        break;
      case 'files':
        if (typeof page.openWorkspaceFilesExplorer === 'function') {
          page.openWorkspaceFilesExplorer();
        }
        break;
      default:
        break;
    }
  }

  // ---------- agent command deck ----------

  statusTone(statusKey, statusLabel) {
    const s = (String(statusKey || '') + ' ' + String(statusLabel || '')).toLowerCase();
    if (/error|fail|blocked/.test(s)) return 'alert';
    if (/needs?[-_\s]?input|choice|human/.test(s)) return 'needs-input';
    if (/work|run|busy|active|progress/.test(s)) return 'working';
    if (/wait|queue|pending|scheduled/.test(s)) return 'waiting';
    if (/complete|done|success/.test(s)) return 'done';
    return 'idle';
  }

  taskTone(status) {
    switch (String(status || '').toLowerCase()) {
      case 'in_progress':
        return 'working';
      case 'failed':
      case 'cancelled':
        return 'alert';
      case 'completed':
      case 'done':
        return 'done';
      default:
        return 'pending';
    }
  }

  normalizeAgentKey(name) {
    const page = this.page || {};
    if (typeof page.normalizeAgentName === 'function') {
      return String(page.normalizeAgentName(name) || '');
    }
    return String(name || '')
      .trim()
      .toLowerCase();
  }

  agentSelectionStorageKey() {
    const workspaceId = this.workspaceId();
    return workspaceId ? `ori-workspace-command-agent:${workspaceId}` : '';
  }

  readPersistedAgentKey() {
    const storageKey = this.agentSelectionStorageKey();
    if (!storageKey || typeof localStorage === 'undefined') return '';
    try {
      return this.normalizeAgentKey(localStorage.getItem(storageKey));
    } catch (_error) {
      return '';
    }
  }

  persistAgentKey(key) {
    const storageKey = this.agentSelectionStorageKey();
    const normalized = this.normalizeAgentKey(key);
    if (!storageKey || !normalized || typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(storageKey, normalized);
    } catch (_error) {
      // Storage is best effort; rendering and selection must continue without it.
    }
  }

  agentGroups() {
    const page = this.page || {};
    let rawGroups = [];
    try {
      rawGroups = typeof page.buildAgentGroups === 'function' ? page.buildAgentGroups() || [] : [];
    } catch (_error) {
      rawGroups = [];
    }

    const agents = [];
    const seen = new Set();
    let unassigned = null;
    rawGroups.forEach(group => {
      if (!group) return;
      if (group.isUnassigned) {
        if (!unassigned && Array.isArray(group.tasks) && group.tasks.length > 0) {
          unassigned = group;
        }
        return;
      }
      if (!group.isWorkspaceAgent) return;
      const key = this.normalizeAgentKey(group.key || group.name);
      if (!key || seen.has(key)) return;
      seen.add(key);
      agents.push(group);
    });

    agents.sort((left, right) => {
      const leftEntry = this.isEntryAgentGroup(left);
      const rightEntry = this.isEntryAgentGroup(right);
      if (leftEntry === rightEntry) return 0;
      return leftEntry ? -1 : 1;
    });
    return { agents, unassigned };
  }

  isEntryAgentGroup(group) {
    const page = this.page || {};
    return Boolean(
      group &&
      typeof page.isWorkspaceEntryAgent === 'function' &&
      page.isWorkspaceEntryAgent(group.name)
    );
  }

  reconcileAgentSelection(groups) {
    const agents = Array.isArray(groups) ? groups : [];
    const keys = new Set(agents.map(group => this.normalizeAgentKey(group.key || group.name)));
    let selected = this.normalizeAgentKey(this.selectedAgentKey);

    if (!this.agentSelectionInitialized) {
      const persisted = this.readPersistedAgentKey();
      if (persisted && keys.has(persisted)) selected = persisted;
      this.agentSelectionInitialized = true;
    }

    if (!selected || !keys.has(selected)) {
      const entry = agents.find(group => this.isEntryAgentGroup(group));
      selected = this.normalizeAgentKey(
        entry?.key || entry?.name || agents[0]?.key || agents[0]?.name
      );
    }

    this.selectedAgentKey = selected;
    if (selected) this.persistAgentKey(selected);
    return selected;
  }

  selectedAgentGroup(groups) {
    const agents = Array.isArray(groups) ? groups : [];
    const selected = this.reconcileAgentSelection(agents);
    return (
      agents.find(group => this.normalizeAgentKey(group.key || group.name) === selected) || null
    );
  }

  agentRolePresentation(group) {
    const page = this.page || {};
    if (typeof page.getAgentGroupRolePresentation === 'function') {
      try {
        return (
          page.getAgentGroupRolePresentation(group) || {
            label: 'Agent',
            detail: 'Agent',
            roles: []
          }
        );
      } catch (_error) {
        // Fall through to the group roles.
      }
    }
    const roles = Array.from(
      new Set(
        (Array.isArray(group?.roles) ? group.roles : [])
          .map(role => String(role || '').trim())
          .filter(Boolean)
      )
    );
    return {
      label: roles.length > 1 ? 'Multiple roles' : roles[0] || 'Agent',
      detail: roles.join(', ') || 'Agent',
      roles
    };
  }

  agentViewModel(group) {
    if (!group) return null;
    const page = this.page || {};
    const name = String(group.name || 'Agent');
    const key = this.normalizeAgentKey(group.key || name);
    const status =
      typeof page.getAgentRosterStatus === 'function'
        ? page.getAgentRosterStatus(name)
        : { key: 'idle', label: 'Idle', detail: 'No active tasks' };
    const role = this.agentRolePresentation(group);
    const skills =
      typeof page.getAgentSkillSummary === 'function'
        ? page.getAgentSkillSummary(name)
        : { count: 0, names: [] };
    const mcpNames =
      typeof page.getEffectiveWorkspaceMCPServerNames === 'function'
        ? page.getEffectiveWorkspaceMCPServerNames(name)
        : [];
    const profile = typeof page.getAgentProfile === 'function' ? page.getAgentProfile(name) : null;
    const model =
      typeof page.getAgentModelPresentation === 'function'
        ? page.getAgentModelPresentation(profile)
        : { model: '', label: 'Model not set', empty: true };
    const tasks = Array.isArray(group.tasks) ? group.tasks : [];
    const currentTask =
      tasks.find(task => String(task?.status || '').toLowerCase() === 'in_progress') || null;

    return {
      group,
      key,
      name,
      encodedName: encodeURIComponent(name),
      entry: this.isEntryAgentGroup(group),
      instanceCount: Math.max(1, Number(group.instanceCount || 1)),
      role,
      status,
      tone: this.statusTone(status?.key, status?.label),
      skills,
      mcpNames: Array.isArray(mcpNames) ? mcpNames : [],
      profile,
      model,
      tasks,
      currentTask
    };
  }

  agentCharacterHTML(agent, variant = 'roster') {
    if (!agent) return '';
    const key = String(agent.key || agent.name || 'agent');
    let hash = 0;
    for (let index = 0; index < key.length; index += 1) {
      hash = (hash * 33 + key.charCodeAt(index)) >>> 0;
    }
    const hue = hash % 360;
    const visor = 28 + (hash % 3) * 5;
    const antenna =
      hash % 2 === 0
        ? '<path d="M50 24V12M50 12L57 7" class="ws-cmd-character-line"/>'
        : '<path d="M50 24V10M44 8H56" class="ws-cmd-character-line"/>';
    const emblem =
      hash % 3 === 0
        ? '<path d="M50 63L57 70L50 77L43 70Z" class="ws-cmd-character-emblem"/>'
        : hash % 3 === 1
          ? '<circle cx="50" cy="69" r="7" class="ws-cmd-character-emblem"/>'
          : '<path d="M42 75L50 61L58 75Z" class="ws-cmd-character-emblem"/>';
    const variantClass = variant === 'stage' ? ' is-stage' : ' is-roster';

    return (
      '<span class="ws-cmd-character' +
      variantClass +
      ' ' +
      escapeHtml(agent.tone || 'idle') +
      '" style="--agent-character-hue:' +
      hue +
      '" aria-hidden="true">' +
      '<svg viewBox="0 0 100 118" focusable="false">' +
      '<path d="M18 92L28 56L38 47H62L72 56L82 92L69 108H31Z" class="ws-cmd-character-body"/>' +
      '<path d="M32 30L42 21H58L68 30V49L59 58H41L32 49Z" class="ws-cmd-character-head"/>' +
      '<path d="M' +
      visor +
      ' 34H' +
      (100 - visor) +
      'V45H' +
      visor +
      'Z" class="ws-cmd-character-visor"/>' +
      '<path d="M18 92L5 83L12 66L28 59M82 92L95 83L88 66L72 59" class="ws-cmd-character-shoulders"/>' +
      antenna +
      emblem +
      '<path d="M28 91H72" class="ws-cmd-character-line is-soft"/>' +
      '</svg>' +
      '</span>'
    );
  }

  rosterItemHTML(agent) {
    const selected = agent.key === this.selectedAgentKey;
    const count =
      agent.instanceCount > 1
        ? '<span class="ws-cmd-roster-count" aria-label="' +
          agent.instanceCount +
          ' instances">' +
          agent.instanceCount +
          '×</span>'
        : '';
    const entry = agent.entry ? '<span class="ws-cmd-roster-entry">Entry</span>' : '';
    return (
      '<button type="button" class="ws-cmd-roster-item' +
      (selected ? ' is-selected' : '') +
      (agent.entry ? ' is-entry' : '') +
      '" data-cmd-select-agent="' +
      escapeHtml(agent.encodedName) +
      '" data-agent-key="' +
      escapeHtml(agent.key) +
      '" aria-pressed="' +
      (selected ? 'true' : 'false') +
      '" aria-label="Select ' +
      escapeHtml(agent.name) +
      ', ' +
      escapeHtml(agent.status?.label || 'Idle') +
      (agent.instanceCount > 1 ? ', ' + agent.instanceCount + ' instances' : '') +
      '">' +
      this.agentCharacterHTML(agent, 'roster') +
      '<span class="ws-cmd-roster-copy">' +
      '<span class="ws-cmd-roster-name">' +
      escapeHtml(agent.name) +
      '</span>' +
      '<span class="ws-cmd-roster-role">' +
      escapeHtml(agent.role?.label || 'Agent') +
      '</span>' +
      '<span class="ws-cmd-state"><span class="ws-cmd-led ' +
      escapeHtml(agent.tone) +
      '"></span>' +
      escapeHtml(agent.status?.label || 'Idle') +
      '</span>' +
      '</span>' +
      entry +
      count +
      '</button>'
    );
  }

  agentDetailTarget(agent) {
    const page = this.page || {};
    if (!agent || typeof page.getAgentDetailTarget !== 'function') return null;
    try {
      const target = page.getAgentDetailTarget(agent.name);
      return target && target.interactive && target.href ? target : null;
    } catch (_error) {
      return null;
    }
  }

  agentStageHTML(agent) {
    const target = this.agentDetailTarget(agent);
    const detailAction = target
      ? '<a class="ws-cmd-agent-action" href="' + escapeHtml(target.href) + '">Open Agent</a>'
      : '';
    const entry = agent.entry ? '<span class="ws-cmd-badge is-keeper">★ Entry Agent</span>' : '';
    const remove = agent.entry
      ? '<span class="ws-cmd-agent-lock" title="The entry agent cannot be removed">Entry agent locked</span>'
      : '<button type="button" class="ws-cmd-agent-action is-danger" data-cmd-remove-agent="' +
        escapeHtml(agent.encodedName) +
        '">Remove</button>';
    const instance =
      agent.instanceCount > 1
        ? '<span class="ws-cmd-badge">' + agent.instanceCount + ' instances</span>'
        : '';

    return (
      '<section class="ws-cmd-agent-stage ' +
      escapeHtml(agent.tone) +
      '" aria-labelledby="ws-cmd-selected-agent-name">' +
      '<div class="ws-cmd-stage-grid" aria-hidden="true"></div>' +
      '<div class="ws-cmd-stage-character">' +
      this.agentCharacterHTML(agent, 'stage') +
      '</div>' +
      '<div class="ws-cmd-stage-copy">' +
      '<span class="ws-cmd-stage-kicker">Selected Agent</span>' +
      '<h3 id="ws-cmd-selected-agent-name">' +
      escapeHtml(agent.name) +
      '</h3>' +
      '<div class="ws-cmd-stage-badges">' +
      entry +
      instance +
      '</div>' +
      '<p class="ws-cmd-stage-role">' +
      escapeHtml(agent.role?.detail || 'Agent') +
      '</p>' +
      '<p class="ws-cmd-stage-status"><span class="ws-cmd-led ' +
      escapeHtml(agent.tone) +
      '"></span><strong>' +
      escapeHtml(agent.status?.label || 'Idle') +
      '</strong><span>' +
      escapeHtml(agent.status?.detail || 'No active tasks') +
      '</span></p>' +
      '</div>' +
      '<div class="ws-cmd-stage-actions">' +
      '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-task="' +
      escapeHtml(agent.encodedName) +
      '">Give Task</button>' +
      detailAction +
      (agent.entry
        ? '<button type="button" class="ws-cmd-agent-action" data-cmd-manager-settings="1">Manager Settings</button>'
        : '') +
      remove +
      '</div>' +
      '</section>'
    );
  }

  questLogHTML(group, encoded) {
    const tasks = this.applyTaskTagFilter(Array.isArray(group.tasks) ? group.tasks : []);
    const add = group.isUnassigned
      ? ''
      : '<button type="button" class="ws-cmd-icon-btn sm" data-cmd-add-task="' +
        escapeHtml(encoded) +
        '" aria-label="Add task">＋</button>';
    const head =
      '<div class="ws-cmd-ql-head"><span class="ws-cmd-ql-t">Tasks · ' +
      tasks.length +
      '</span>' +
      add +
      '</div>';
    if (!tasks.length) {
      const emptyText = this.taskFilterActiveTags().length
        ? '— no tasks match the tag filter —'
        : '— no tasks yet —';
      return (
        '<div class="ws-cmd-questlog">' +
        head +
        '<div class="ws-cmd-ql-empty">' +
        emptyText +
        '</div></div>'
      );
    }
    const items = tasks
      .map(t => {
        const label = String(t.description || t.name || t.title || 'Task');
        const tone = this.taskTone(t.status);
        const statusText = String(t.status || 'pending').replace('_', ' ');
        const taskId = String(t.id || '');
        return (
          '<div class="ws-cmd-quest">' +
          '<span class="ws-cmd-q-glyph">&bull;</span>' +
          '<button type="button" class="ws-cmd-q-name" data-cmd-open-task="' +
          escapeHtml(taskId) +
          '" aria-label="Open task ' +
          escapeHtml(label) +
          '">' +
          escapeHtml(label) +
          '</button>' +
          '<span class="ws-cmd-q-status ' +
          tone +
          '">' +
          escapeHtml(statusText) +
          '</span>' +
          '<button type="button" class="ws-cmd-q-run" data-cmd-run-task="' +
          escapeHtml(taskId) +
          '" title="Run" aria-label="Run task ' +
          escapeHtml(label) +
          '">▶</button>' +
          '</div>'
        );
      })
      .join('');
    return '<div class="ws-cmd-questlog">' + head + items + '</div>';
  }

  overviewTabHTML(agent) {
    const currentTask = agent.currentTask
      ? escapeHtml(agent.currentTask.description || agent.currentTask.name || 'Current task')
      : 'No task in progress';
    const currentDetail = agent.currentTask
      ? 'This agent is actively executing assigned work.'
      : 'Ready for a new assignment.';
    return (
      '<div class="ws-cmd-overview-hero">' +
      '<div><span class="ws-cmd-overview-label">Current assignment</span>' +
      '<strong>' +
      currentTask +
      '</strong><p>' +
      currentDetail +
      '</p></div>' +
      '<span class="ws-cmd-overview-signal ' +
      escapeHtml(agent.tone) +
      '">' +
      escapeHtml(agent.status?.label || 'Idle') +
      '</span></div>' +
      '<div class="ws-cmd-overview-stats">' +
      '<div><span>Tasks</span><strong>' +
      agent.tasks.length +
      '</strong></div>' +
      '<div><span>Skills</span><strong>' +
      Number(agent.skills?.count || 0) +
      '</strong></div>' +
      '<div><span>MCP tools</span><strong>' +
      agent.mcpNames.length +
      '</strong></div>' +
      '<div><span>Instances</span><strong>' +
      agent.instanceCount +
      '</strong></div>' +
      '</div>' +
      '<div class="ws-cmd-overview-brief">' +
      '<span class="ws-cmd-overview-label">Role profile</span>' +
      '<p>' +
      escapeHtml(agent.role?.detail || 'Agent') +
      '</p>' +
      '<span class="ws-cmd-overview-label">Status detail</span>' +
      '<p>' +
      escapeHtml(agent.status?.detail || 'No active tasks') +
      '</p>' +
      '</div>'
    );
  }

  tasksTabHTML(agent) {
    const page = this.page || {};
    if (typeof page.renderAgentTasksContent === 'function') {
      try {
        return (
          '<div class="ws-cmd-agent-task-list">' +
          page.renderAgentTasksContent(agent.tasks) +
          '</div>'
        );
      } catch (_error) {
        // Fall back to the compact command task list.
      }
    }
    return this.questLogHTML(agent.group, agent.encodedName);
  }

  promptCacheEntry(agent) {
    const cache = this.page && this.page.agentPromptCache;
    if (!cache || typeof cache.get !== 'function' || !agent) return null;
    return cache.get(agent.key) || null;
  }

  promptLoadoutHTML(agent) {
    const page = this.page || {};
    const cached = this.promptCacheEntry(agent);
    const loading = this.agentPromptLoadingKey === agent.key;
    if (loading) {
      return (
        '<div class="ws-cmd-loadout-prompt is-loading" aria-live="polite">' +
        '<div><span class="ws-cmd-loadout-title">System prompt</span>' +
        '<p>Loading effective prompt…</p></div></div>'
      );
    }
    if (cached && cached.error) {
      return (
        '<div class="ws-cmd-loadout-prompt is-error">' +
        '<div><span class="ws-cmd-loadout-title">System prompt unavailable</span>' +
        '<p>The effective prompt could not be loaded.</p></div>' +
        '<button type="button" class="ws-cmd-agent-action" data-cmd-retry-prompt="' +
        escapeHtml(agent.encodedName) +
        '">Retry</button></div>'
      );
    }
    if (cached && !cached.error) {
      const preview =
        typeof page.buildAgentPromptPreview === 'function'
          ? page.buildAgentPromptPreview(cached)
          : String(cached.effective_prompt || cached.base_system_prompt || '');
      return (
        '<div class="ws-cmd-loadout-prompt">' +
        '<div><span class="ws-cmd-loadout-title">System prompt</span><p>' +
        escapeHtml(preview || 'No system prompt set for this agent.') +
        '</p></div>' +
        '<button type="button" class="ws-cmd-agent-action" data-cmd-view-prompt="' +
        escapeHtml(agent.encodedName) +
        '">View full</button></div>'
      );
    }
    return (
      '<div class="ws-cmd-loadout-prompt is-loading" aria-live="polite">' +
      '<div><span class="ws-cmd-loadout-title">System prompt</span>' +
      '<p>Preparing effective prompt…</p></div></div>'
    );
  }

  loadoutTabHTML(agent) {
    const page = this.page || {};
    const editable =
      typeof page.agentAllowsModelEditing === 'function'
        ? page.agentAllowsModelEditing(agent.profile)
        : false;
    const model =
      agent.model && !agent.model.empty ? agent.model.label || agent.model.model : 'Model not set';
    const modelAction = editable
      ? '<button type="button" class="ws-cmd-loadout-edit" data-cmd-edit-model="' +
        escapeHtml(agent.encodedName) +
        '">Change</button>'
      : '<span class="ws-cmd-loadout-readonly">Read only</span>';
    return (
      '<div class="ws-cmd-loadout-grid">' +
      '<section class="ws-cmd-loadout-card"><header><span class="ws-cmd-loadout-kicker">Model</span>' +
      modelAction +
      '</header><strong>' +
      escapeHtml(model) +
      '</strong></section>' +
      '<section class="ws-cmd-loadout-card is-editor">' +
      this.renderLoadoutEditor(agent) +
      '</section>' +
      '</div>' +
      this.promptLoadoutHTML(agent)
    );
  }

  recentActivityItems(agent) {
    if (!agent) return [];
    const page = this.page || {};
    const key = agent.key;
    const tasks = Array.isArray(page.tasks) ? page.tasks : agent.tasks;
    const sessions = Array.isArray(page.sessions) ? page.sessions : [];
    const items = [];

    tasks.forEach(task => {
      if (this.normalizeAgentKey(task?.to) !== key) return;
      const timestamp = task?.updated_at || task?.created_at;
      const time = Date.parse(timestamp || '');
      if (!Number.isFinite(time)) return;
      items.push({
        id: String(task.id || ''),
        kind: 'Task',
        title: String(task.description || task.name || task.title || 'Untitled task'),
        status: String(task.status || 'pending').replaceAll('_', ' '),
        timestamp: String(timestamp),
        time
      });
    });
    sessions.forEach(session => {
      if (this.normalizeAgentKey(session?.agent_name) !== key) return;
      const timestamp = session?.updated_at || session?.created_at;
      const time = Date.parse(timestamp || '');
      if (!Number.isFinite(time)) return;
      items.push({
        id: String(session.id || ''),
        kind: 'Session',
        title: String(session.title || session.name || 'Untitled session'),
        status: 'Workspace session',
        timestamp: String(timestamp),
        time
      });
    });

    return items.sort((left, right) => right.time - left.time);
  }

  formatActivityTimestamp(timestamp) {
    const date = new Date(timestamp);
    return Number.isNaN(date.getTime())
      ? ''
      : date.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }

  recentActivityTabHTML(agent) {
    const items = this.recentActivityItems(agent);
    if (!items.length) {
      return (
        '<div class="ws-cmd-tab-empty"><strong>No recent activity</strong>' +
        '<span>Tasks and sessions attributed to this agent will appear here.</span></div>'
      );
    }
    return (
      '<div class="ws-cmd-activity-list">' +
      items
        .map(
          item =>
            '<button type="button" class="ws-cmd-activity-item" data-cmd-' +
            (item.kind === 'Task' ? 'open-task' : 'open-session') +
            '="' +
            escapeHtml(item.id) +
            '">' +
            '<span class="ws-cmd-activity-kind">' +
            escapeHtml(item.kind) +
            '</span>' +
            '<span class="ws-cmd-activity-copy"><strong>' +
            escapeHtml(item.title) +
            '</strong>' +
            '<span>' +
            escapeHtml(item.status) +
            '</span></span>' +
            '<time datetime="' +
            escapeHtml(item.timestamp) +
            '">' +
            escapeHtml(this.formatActivityTimestamp(item.timestamp)) +
            '</time></button>'
        )
        .join('') +
      '</div>'
    );
  }

  unassignedQueueHTML(group) {
    const tasks = Array.isArray(group?.tasks) ? group.tasks : [];
    if (!tasks.length) return '';
    return (
      '<section class="ws-cmd-unassigned" aria-labelledby="ws-cmd-unassigned-title">' +
      '<div><span class="ws-cmd-unassigned-kicker">Dispatch queue</span>' +
      '<strong id="ws-cmd-unassigned-title">Unassigned tasks</strong></div>' +
      '<span class="ws-cmd-unassigned-count">' +
      tasks.length +
      '</span>' +
      '<button type="button" class="ws-cmd-agent-action" data-cmd-open-agent-manager="tasks">Review</button>' +
      '</section>'
    );
  }

  agentTabsHTML(agent) {
    const tabs = [
      { key: 'overview', label: 'Overview' },
      { key: 'tasks', label: 'Tasks' },
      { key: 'loadout', label: 'Loadout' },
      { key: 'recent', label: 'Recent Activity' }
    ];
    const safeKey = agent.key.replace(/[^a-z0-9_-]+/gi, '-');
    const tabList = tabs
      .map(tab => {
        const active = tab.key === this.activeAgentTab;
        return (
          '<button type="button" role="tab" class="ws-cmd-agent-tab' +
          (active ? ' is-active' : '') +
          '" id="ws-cmd-agent-tab-' +
          safeKey +
          '-' +
          tab.key +
          '" aria-selected="' +
          (active ? 'true' : 'false') +
          '" aria-controls="ws-cmd-agent-panel-' +
          safeKey +
          '-' +
          tab.key +
          '" tabindex="' +
          (active ? '0' : '-1') +
          '" data-cmd-agent-tab="' +
          tab.key +
          '">' +
          tab.label +
          '</button>'
        );
      })
      .join('');
    const content = {
      overview: this.overviewTabHTML(agent),
      tasks: this.tasksTabHTML(agent),
      loadout: this.loadoutTabHTML(agent),
      recent: this.recentActivityTabHTML(agent)
    };
    const panels = tabs
      .map(tab => {
        const active = tab.key === this.activeAgentTab;
        return (
          '<section role="tabpanel" class="ws-cmd-agent-tabpanel' +
          (active ? ' is-active' : '') +
          '" id="ws-cmd-agent-panel-' +
          safeKey +
          '-' +
          tab.key +
          '" aria-labelledby="ws-cmd-agent-tab-' +
          safeKey +
          '-' +
          tab.key +
          '"' +
          (active ? '' : ' hidden') +
          '>' +
          content[tab.key] +
          '</section>'
        );
      })
      .join('');
    return (
      '<section class="ws-cmd-agent-overview" aria-label="' +
      escapeHtml(agent.name) +
      ' overview">' +
      '<div class="ws-cmd-agent-tabs" role="tablist" aria-label="Agent overview sections">' +
      tabList +
      '</div>' +
      '<div class="ws-cmd-agent-panel-body">' +
      panels +
      '</div>' +
      '</section>'
    );
  }

  renderGarrison() {
    if (!AGENT_TAB_KEYS.includes(this.activeAgentTab)) this.activeAgentTab = 'overview';
    const groups = this.agentGroups();
    const selectedGroup = this.selectedAgentGroup(groups.agents);
    if (!selectedGroup) {
      this.selectedAgentKey = '';
      return (
        '<div class="ws-cmd-deck-empty"><div class="ws-cmd-deck-empty-glyph">◇</div>' +
        '<strong>No agents in this workspace</strong>' +
        '<span>Add an agent to begin assigning work.</span>' +
        '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-agent>Add Agent</button></div>'
      );
    }
    const agents = groups.agents.map(group => this.agentViewModel(group)).filter(Boolean);
    const selected = agents.find(agent => agent.key === this.selectedAgentKey) || agents[0];
    const roster = agents.map(agent => this.rosterItemHTML(agent)).join('');
    const statusAnnouncement = selected.name + ' selected. ' + (selected.status?.label || 'Idle');
    const announce = statusAnnouncement === this.lastAnnouncedAgentStatus ? '' : statusAnnouncement;
    this.lastAnnouncedAgentStatus = statusAnnouncement;
    return (
      '<div class="ws-cmd-deck">' +
      '<aside class="ws-cmd-roster" aria-label="Workspace agents">' +
      '<header class="ws-cmd-roster-head"><div><span>Agent roster</span><strong>' +
      agents.length +
      '</strong></div><button type="button" class="ws-cmd-icon-btn" data-cmd-add-agent' +
      ' aria-label="Add agent">＋</button></header>' +
      '<div class="ws-cmd-roster-list">' +
      roster +
      '</div>' +
      this.unassignedQueueHTML(groups.unassigned) +
      '</aside>' +
      this.agentStageHTML(selected) +
      this.agentTabsHTML(selected) +
      '<div class="ws-cmd-agent-live sr-only" aria-live="polite" aria-atomic="true">' +
      escapeHtml(announce) +
      '</div>' +
      '</div>'
    );
  }

  // ---------- Operations Map mode ----------

  taskStatusLabel(status) {
    const normalized = String(status || 'pending')
      .trim()
      .replaceAll('_', ' ');
    return normalized ? normalized.charAt(0).toUpperCase() + normalized.slice(1) : 'Pending';
  }

  taskUpdatedTime(task) {
    const raw = task?.updated_at || task?.created_at || '';
    const time = Date.parse(raw);
    return Number.isFinite(time) ? time : 0;
  }

  taskHumanLoopState(task) {
    return String(task?.context?.human_loop?.state || '')
      .trim()
      .toLowerCase();
  }

  // These delegate to the shared task-presentation predicates so the Map no
  // longer maintains its own copy. Behaviour is byte-identical to the prior
  // inline logic (FR29, inert landing).
  isBlockedTask(task) {
    return sharedIsBlockedTask(task);
  }

  isNeedsInputTask(task) {
    return sharedIsNeedsInputTask(task);
  }

  isWorkingTask(task) {
    return sharedIsWorkingTask(task);
  }

  isQueuedTask(task) {
    return sharedIsQueuedTask(task);
  }

  taskPriority(task) {
    if (this.isBlockedTask(task)) return 0;
    if (this.isNeedsInputTask(task)) return 1;
    if (this.isWorkingTask(task)) return 2;
    if (this.isQueuedTask(task)) return 3;
    return 4;
  }

  mapAgentStatus(agent) {
    const tasks = Array.isArray(agent?.tasks) ? agent.tasks : [];
    if (tasks.some(task => this.isBlockedTask(task))) {
      return {
        key: 'blocked',
        label: 'Blocked',
        detail: 'Needs attention before work can continue'
      };
    }
    if (tasks.some(task => this.isNeedsInputTask(task))) {
      return { key: 'needs-input', label: 'Needs input', detail: 'Waiting for user input' };
    }
    if (tasks.some(task => this.isWorkingTask(task))) {
      return { key: 'working', label: 'Working', detail: 'Task in progress' };
    }
    if (tasks.some(task => this.isQueuedTask(task))) {
      return { key: 'waiting', label: 'Waiting', detail: 'Task queued' };
    }
    return agent?.status || { key: 'idle', label: 'Idle', detail: 'No active tasks' };
  }

  mapAgentViewModels() {
    const groups = this.agentGroups();
    const selectedGroup = this.selectedAgentGroup(groups.agents);
    if (!selectedGroup) {
      this.selectedAgentKey = '';
      return { agents: [], unassigned: groups.unassigned, selected: null };
    }
    const agents = groups.agents
      .map(group => this.agentViewModel(group))
      .filter(Boolean)
      .map(agent => {
        const status = this.mapAgentStatus(agent);
        return {
          ...agent,
          status,
          tone: this.statusTone(status.key, status.label),
          destination: this.agentMapDestination(agent, status)
        };
      });
    const selected = agents.find(agent => agent.key === this.selectedAgentKey) || agents[0] || null;
    if (selected) this.selectedAgentKey = selected.key;
    return { agents, unassigned: groups.unassigned, selected };
  }

  priorityTaskForAgent(agent) {
    const tasks = Array.isArray(agent?.tasks) ? agent.tasks.slice() : [];
    if (!tasks.length) return null;
    return tasks.sort((left, right) => {
      const priorityDelta = this.taskPriority(left) - this.taskPriority(right);
      if (priorityDelta !== 0) return priorityDelta;
      return this.taskUpdatedTime(right) - this.taskUpdatedTime(left);
    })[0];
  }

  latestTaskActivity(task) {
    const taskId = String(task?.id || '');
    const map = this.page && this.page._taskActivity;
    if (!taskId || !map || typeof map.get !== 'function') return null;
    return map.get(taskId) || null;
  }

  formatRelativeTime(value) {
    const time = typeof value === 'number' ? value : Date.parse(value || '');
    if (!Number.isFinite(time) || time <= 0) return '';
    const diff = Date.now() - time;
    const abs = Math.abs(diff);
    const ahead = diff < 0;
    if (abs < 60000) return 'just now';
    const mins = Math.round(abs / 60000);
    if (mins < 60) return ahead ? 'in ' + mins + 'm' : mins + 'm ago';
    const hours = Math.round(abs / 3600000);
    if (hours < 24) return ahead ? 'in ' + hours + 'h' : hours + 'h ago';
    const days = Math.round(abs / 86400000);
    if (days < 7) return ahead ? 'in ' + days + 'd' : days + 'd ago';
    return new Date(time).toLocaleDateString();
  }

  agentActivitySummary(agent) {
    const task = this.priorityTaskForAgent(agent) || agent?.currentTask;
    if (!task) {
      const recent = this.recentActivityItems(agent)[0];
      if (!recent) return null;
      return {
        taskLabel: recent.title,
        statusLabel: recent.status,
        activityLabel: recent.kind + ' updated',
        whenLabel: this.formatRelativeTime(recent.time),
        timestamp: recent.timestamp
      };
    }
    const activity = this.latestTaskActivity(task);
    const timestamp = activity?.at || this.taskUpdatedTime(task);
    return {
      taskLabel: String(task.description || task.name || task.title || 'Untitled task'),
      statusLabel: this.taskStatusLabel(task.status),
      activityLabel: activity?.label || this.taskStatusLabel(task.status),
      whenLabel: this.formatRelativeTime(timestamp),
      timestamp: task.updated_at || task.created_at || ''
    };
  }

  agentMapDestination(agent, status = agent?.status) {
    const key = String(status?.key || '')
      .trim()
      .toLowerCase();
    if (key === 'blocked' || key === 'needs-input') return 'tasks';
    if (key === 'working') {
      const summary = this.agentActivitySummary(agent);
      return /tool|→/.test(String(summary?.activityLabel || '').toLowerCase()) ? 'tools' : 'tasks';
    }
    if (key === 'waiting') return 'hub';
    return 'hub';
  }

  openMapTasks() {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const terminal = new Set(['completed', 'cancelled', 'timeout']);
    return tasks
      .filter(task => !task?.parent_task_id)
      .filter(task => !terminal.has(String(task?.status || '').toLowerCase()))
      .sort((left, right) => {
        const priorityDelta = this.taskPriority(left) - this.taskPriority(right);
        if (priorityDelta !== 0) return priorityDelta;
        return this.taskUpdatedTime(right) - this.taskUpdatedTime(left);
      });
  }

  mapZoneHeaderHTML(kicker, title, count = '') {
    return (
      '<header class="ws-cmd-map-zone-head"><div><span>' +
      escapeHtml(kicker) +
      '</span><strong>' +
      escapeHtml(title) +
      '</strong></div>' +
      (count !== '' ? '<span class="ws-cmd-map-zone-count">' + escapeHtml(count) + '</span>' : '') +
      '</header>'
    );
  }

  mapWindowOptions() {
    // Labels are presentation-only (FR3-5): the underlying panel keys
    // ('objective'/'objectives') are unchanged so activeMapWindow state and
    // every reference to them elsewhere keeps working.
    return [
      { key: 'objective', label: 'Workspace Mission', icon: 'bi-bullseye' },
      { key: 'objectives', label: 'Tasks', icon: 'bi-list-check' },
      { key: 'inventory', label: 'Inventory', icon: 'bi-box-seam' },
      { key: 'stations', label: 'Stations', icon: 'bi-cpu' }
    ];
  }

  renderMapToolTray() {
    return (
      '<nav class="ws-cmd-map-belt" aria-label="Map windows">' +
      this.mapWindowOptions()
        .map(item => {
          const active = this.activeMapWindow === item.key;
          return (
            '<button type="button" class="ws-cmd-map-belt-btn' +
            (active ? ' is-active' : '') +
            '" data-cmd-map-window="' +
            escapeHtml(item.key) +
            '" aria-label="' +
            escapeHtml(item.label) +
            '" aria-pressed="' +
            (active ? 'true' : 'false') +
            '" title="' +
            escapeHtml(item.label) +
            '"><i class="bi ' +
            escapeHtml(item.icon) +
            '" aria-hidden="true"></i><span class="sr-only">' +
            escapeHtml(item.label) +
            '</span></button>'
          );
        })
        .join('') +
      '</nav>'
    );
  }

  renderMapMissionPanel() {
    return (
      '<div class="ws-cmd-map-window-section is-objective">' +
      '<div class="ws-cmd-map-quest-frame">' +
      '<div class="ws-cmd-map-quest-frame-head"><span>Main Quest</span><strong>Workspace Mission</strong></div>' +
      this.renderMissionPanel() +
      '</div></div>'
    );
  }

  renderMapTasksPanel() {
    const tasks = this.openMapTasks();
    const shown = tasks.slice(0, 4);
    const rows = shown.length
      ? shown
          .map((task, index) => {
            const id = String(task.id || '');
            const label = String(task.description || task.name || task.title || 'Untitled task');
            const status = String(task.status || 'pending')
              .trim()
              .toLowerCase();
            const questNumber = String(index + 1).padStart(2, '0');
            const startable = this.isQueuedTask(task);
            const openButton =
              '<button type="button" class="ws-cmd-map-task-row ' +
              escapeHtml(this.taskTone(status)) +
              '" data-cmd-open-task="' +
              escapeHtml(id) +
              '">' +
              '<span class="ws-cmd-map-task-marker">Quest ' +
              escapeHtml(questNumber) +
              '</span><span class="ws-cmd-map-task-name">' +
              escapeHtml(label) +
              '</span><span class="ws-cmd-map-task-status">' +
              escapeHtml(this.taskStatusLabel(status)) +
              '</span></button>';
            const startButton = startable
              ? '<button type="button" class="ws-cmd-map-task-start" data-cmd-map-start-task="' +
                escapeHtml(id) +
                '" aria-label="Start ' +
                escapeHtml(label) +
                '"><i class="bi bi-play-fill" aria-hidden="true"></i><span>Start</span></button>'
              : '';
            return (
              '<div class="ws-cmd-map-task-row-wrap">' + openButton + startButton + '</div>'
            );
          })
          .join('')
      : '<div class="ws-cmd-map-empty is-quest-empty"><strong>Quest log clear</strong><span>No active objectives assigned.</span></div>';
    return (
      '<div class="ws-cmd-map-window-section is-objectives">' +
      this.mapZoneHeaderHTML('Tasks', 'Active Tasks', tasks.length) +
      '<div class="ws-cmd-map-task-list">' +
      rows +
      '</div>' +
      '<button type="button" class="ws-cmd-map-zone-action" data-cmd-open-task-drawer>Open Tasks</button>' +
      '</div>'
    );
  }

  // ---------- Task drawer (group 3) ----------
  //
  // A persistent, NON-modal side panel that replaces the Objectives → Open Tasks
  // modal stack (the reported bug: the tasks modal opened underneath the Map
  // window that launched it). Opening the drawer closes the Objectives Map window
  // in the same transition so the two task surfaces never coexist (FR9-FR11). The
  // element lives inside .ws-cmd (inherits tactical tokens) and survives full
  // re-renders so selection and scroll are preserved on live refresh (FR25).

  // All non-subtask tasks for this workspace, resolver-sorted (FR24).
  drawerTasks() {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    return sortTasksForDrawer(tasks.filter(task => !task?.parent_task_id));
  }

  drawerFilteredTasks() {
    return this.drawerTasks().filter(task => taskMatchesFilter(task, this.taskDrawerFilter));
  }

  openTaskDrawer(trigger) {
    this.taskDrawerTrigger = trigger || null;
    // Close the Objectives Map window so the drawer never opens beneath it (FR11).
    if (this.activeMapWindow) this.activeMapWindow = '';
    // The floating "New Quest" composer sits in the same bottom-right corner
    // as the drawer and would otherwise be hidden behind it, unreachable and
    // uneditable — close it too (its own Add Task equivalent lives in the
    // drawer header). Keep the draft text so it isn't lost, only auto-closed.
    if (this.taskComposerOpen) this.closeTaskComposer({ clearDraft: false });
    this.taskDrawerOpen = true;
    if (!this.taskDrawerSelectedId) {
      const first = this.drawerFilteredTasks()[0] || this.drawerTasks()[0];
      this.taskDrawerSelectedId = first ? String(first.id || '') : '';
    }
    this.render();
    const el = this.ensureTaskDrawer();
    if (el) {
      el.hidden = false;
      this.renderTaskDrawerBody();
      const heading = el.querySelector('.ws-cmd-drawer-title');
      if (heading && typeof heading.focus === 'function') {
        try {
          heading.focus({ preventScroll: true });
        } catch (_e) {
          heading.focus();
        }
      }
    }
    this.syncURLState();
  }

  closeTaskDrawer() {
    const trigger = this.taskDrawerTrigger;
    this.taskDrawerOpen = false;
    this.taskDrawerTrigger = null;
    if (this.taskDrawerEl) this.taskDrawerEl.hidden = true;
    // Return focus to the control that opened the drawer, or its Map equivalent (FR28).
    let target = trigger && typeof trigger.focus === 'function' ? trigger : null;
    if (!target && this.container) {
      const fallback = this.container.querySelector('[data-cmd-open-task-drawer]');
      if (fallback && typeof fallback.focus === 'function') target = fallback;
    }
    if (target) target.focus();
    this.syncURLState();
  }

  setDrawerFilter(filter) {
    const next = String(filter || '').trim();
    if (!next || next === this.taskDrawerFilter) return;
    this.taskDrawerFilter = next;
    // Keep the selected task visible even if it no longer matches the filter
    // (FR26): only reselect when the current selection is gone entirely.
    if (!this.drawerTasks().some(t => String(t.id || '') === this.taskDrawerSelectedId)) {
      const first = this.drawerFilteredTasks()[0];
      this.taskDrawerSelectedId = first ? String(first.id || '') : '';
    }
    this.renderTaskDrawerBody();
  }

  selectDrawerTask(taskId) {
    const id = String(taskId || '').trim();
    if (!id) return;
    this.taskDrawerSelectedId = id;
    this.renderTaskDrawerBody();
    this.syncURLState();
  }

  drawerSelectedTask() {
    const id = this.taskDrawerSelectedId;
    return this.drawerTasks().find(t => String(t.id || '') === id) || null;
  }

  ensureTaskDrawer() {
    if (this.taskDrawerEl) return this.taskDrawerEl;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function') return null;
    const el = document.createElement('aside');
    el.className = 'ws-cmd-drawer';
    el.setAttribute('role', 'region');
    el.setAttribute('aria-label', 'Tasks');
    el.hidden = true;
    el.style.zIndex = 'var(--wsx-layer-drawer)';
    // The drawer lives outside the map-shell delegate, so it owns its clicks.
    el.addEventListener('click', event => {
      if (event.target.closest('[data-cmd-drawer-close]')) {
        this.closeTaskDrawer();
        return;
      }
      if (event.target.closest('[data-cmd-drawer-add]')) {
        // Reuse the Map's New Quest composer — create a task without leaving
        // Map mode (FR23). The composer floats over the Map beside the drawer.
        if (typeof this.openTaskComposer === 'function') this.openTaskComposer();
        return;
      }
      const filterBtn = event.target.closest('[data-cmd-drawer-filter]');
      if (filterBtn) {
        this.setDrawerFilter(filterBtn.getAttribute('data-cmd-drawer-filter'));
        return;
      }
      const actionBtn = event.target.closest('[data-cmd-drawer-action]');
      if (actionBtn) {
        this.runDrawerAction(
          actionBtn.getAttribute('data-cmd-drawer-action'),
          actionBtn.getAttribute('data-cmd-drawer-task')
        );
        return;
      }
      const row = event.target.closest('[data-cmd-drawer-select]');
      if (row) this.selectDrawerTask(row.getAttribute('data-cmd-drawer-select'));
    });
    this.taskDrawerEl = el;
    if (this.container && this.container.appendChild) this.container.appendChild(el);
    return el;
  }

  // If the selected task vanished on a live refresh (deleted/became unavailable),
  // announce it and pick the next task by deterministic order (FR27).
  reconcileDrawerSelection() {
    this._drawerAnnounce = '';
    if (!this.taskDrawerSelectedId) return;
    const stillHere = this.drawerTasks().some(t => String(t.id || '') === this.taskDrawerSelectedId);
    if (stillHere) return;
    this._drawerAnnounce = 'The selected task is no longer available.';
    const next = this.drawerFilteredTasks()[0] || this.drawerTasks()[0];
    this.taskDrawerSelectedId = next ? String(next.id || '') : '';
  }

  renderTaskDrawerBody() {
    const el = this.ensureTaskDrawer();
    if (!el || el.hidden) return;
    this.reconcileDrawerSelection();
    // Preserve list scroll across body repaints (live refresh — FR25).
    const prevList = el.querySelector('.ws-cmd-drawer-list');
    const prevScroll = prevList ? prevList.scrollTop : 0;
    el.innerHTML = this.taskDrawerHTML();
    const nextList = el.querySelector('.ws-cmd-drawer-list');
    if (nextList) nextList.scrollTop = prevScroll;
  }

  taskDrawerHTML() {
    const counts = resolveTaskCounts(this.drawerTasks());
    const filters = [
      { key: FILTER.ACTIONABLE, label: 'Actionable' },
      { key: FILTER.ACTIVE, label: 'Active' },
      { key: FILTER.NEEDS_ATTENTION, label: 'Needs Attention' },
      { key: FILTER.COMPLETED, label: 'Completed' },
      { key: FILTER.ALL, label: 'All' }
    ]
      .map(f => {
        const active = this.taskDrawerFilter === f.key;
        const count = counts[f.key] || 0;
        return (
          '<button type="button" class="ws-cmd-drawer-filter' +
          (active ? ' is-active' : '') +
          '" data-cmd-drawer-filter="' +
          escapeHtml(f.key) +
          '" aria-pressed="' +
          (active ? 'true' : 'false') +
          '">' +
          escapeHtml(f.label) +
          '<span class="ws-cmd-drawer-filter-count">' +
          escapeHtml(String(count)) +
          '</span></button>'
        );
      })
      .join('');
    return (
      '<header class="ws-cmd-drawer-head">' +
      '<h2 class="ws-cmd-drawer-title" tabindex="-1">Tasks</h2>' +
      '<div class="ws-cmd-drawer-head-actions">' +
      '<button type="button" class="ws-cmd-drawer-add" data-cmd-drawer-add aria-label="Add task">＋ Add Task</button>' +
      '<button type="button" class="ws-cmd-drawer-close" data-cmd-drawer-close aria-label="Close tasks">×</button>' +
      '</div>' +
      '</header>' +
      '<div class="ws-cmd-drawer-live sr-only" role="status" aria-live="polite" aria-atomic="true">' +
      escapeHtml(this._drawerAnnounce || '') +
      '</div>' +
      '<div class="ws-cmd-drawer-filters" role="group" aria-label="Filter tasks">' +
      filters +
      '</div>' +
      '<div class="ws-cmd-drawer-list" role="list">' +
      this.drawerListHTML() +
      '</div>' +
      '<div class="ws-cmd-drawer-preview">' +
      this.drawerPreviewHTML() +
      '</div>'
    );
  }

  drawerListHTML() {
    const tasks = this.drawerFilteredTasks();
    if (!tasks.length) {
      return (
        '<div class="ws-cmd-drawer-empty"><strong>No tasks here</strong>' +
        '<span>Nothing matches the ' +
        escapeHtml(this.taskDrawerFilter) +
        ' filter.</span></div>'
      );
    }
    return tasks
      .map(task => {
        const id = String(task.id || '');
        const pres = resolveTaskPresentation(task);
        const title = String(task.description || task.name || task.title || 'Untitled task');
        const selected = id === this.taskDrawerSelectedId;
        const assignee = pres.assignee || 'Unassigned';
        return (
          '<button type="button" role="listitem" class="ws-cmd-drawer-row' +
          (selected ? ' is-selected' : '') +
          ' tone-' +
          escapeHtml(pres.tone) +
          '" data-cmd-drawer-select="' +
          escapeHtml(id) +
          '" aria-current="' +
          (selected ? 'true' : 'false') +
          '">' +
          '<span class="ws-cmd-drawer-row-main">' +
          '<span class="ws-cmd-drawer-row-title">' +
          escapeHtml(title) +
          '</span>' +
          '<span class="ws-cmd-drawer-row-meta">' +
          escapeHtml(taskShortId(task)) +
          ' · ' +
          escapeHtml(assignee) +
          '</span></span>' +
          '<span class="ws-cmd-drawer-row-state tone-' +
          escapeHtml(pres.tone) +
          '">' +
          escapeHtml(pres.label) +
          '</span></button>'
        );
      })
      .join('');
  }

  drawerPreviewHTML() {
    const task = this.drawerSelectedTask();
    if (!task) {
      return '<div class="ws-cmd-drawer-preview-empty">Select a task to see details.</div>';
    }
    const id = String(task.id || '');
    const pres = resolveTaskPresentation(task);
    const title = String(task.description || task.name || task.title || 'Untitled task');
    const brief = String(task.details || task.brief || task.prompt || '').trim();
    const assignee = pres.assignee || 'Unassigned';
    // A safe, same-origin href carrying a validated `return` target so Back To
    // Workspace (group 7) can restore this exact Map/drawer/task/agent/run
    // context (FR92-93).
    const fullHref = this.taskHrefWithReturn(id);
    // Off-filter notice: the selected task is still shown even if it left the
    // current filter mid-interaction (FR26).
    const offFilter = !taskMatchesFilter(task, this.taskDrawerFilter);
    const primary = pres.primaryAction
      ? '<button type="button" class="ws-cmd-drawer-action" data-cmd-drawer-action="' +
        escapeHtml(pres.primaryAction.id) +
        '" data-cmd-drawer-task="' +
        escapeHtml(id) +
        '">' +
        escapeHtml(pres.primaryAction.label) +
        '</button>'
      : '';
    return (
      (offFilter
        ? '<div class="ws-cmd-drawer-offfilter">This task moved out of the ' +
          escapeHtml(this.taskDrawerFilter) +
          ' filter. <button type="button" class="ws-cmd-drawer-reveal" data-cmd-drawer-filter="' +
          escapeHtml(FILTER.ALL) +
          '">Show in All</button></div>'
        : '') +
      '<div class="ws-cmd-drawer-preview-head">' +
      '<span class="ws-cmd-drawer-preview-state tone-' +
      escapeHtml(pres.tone) +
      '">' +
      escapeHtml(pres.label) +
      '</span>' +
      '<h3 class="ws-cmd-drawer-preview-title">' +
      escapeHtml(title) +
      '</h3>' +
      '<span class="ws-cmd-drawer-preview-assignee">' +
      escapeHtml(assignee) +
      '</span></div>' +
      (brief
        ? '<p class="ws-cmd-drawer-preview-brief">' + escapeHtml(brief.slice(0, 400)) + '</p>'
        : '') +
      '<div class="ws-cmd-drawer-preview-actions">' +
      primary +
      '<a class="ws-cmd-drawer-openfull" href="' +
      escapeHtml(fullHref) +
      '">Open Full Task</a>' +
      '</div>'
    );
  }

  // Route a drawer primary action to the page's existing handlers (group 3 wires
  // to what already works; the sticky tray in group 4 upgrades where runs show).
  runDrawerAction(actionId, taskId) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(taskId || '').trim();
    if (!page || !id) return;
    switch (actionId) {
      case 'start':
      case 'assign_start':
      case 'retry':
        // skipModal: the sticky tray owns monitoring, so suppress the legacy
        // execution modal (FR46). Monitoring is task-ID-keyed and continues
        // regardless of drawer/tray view state.
        if (typeof page.executeTask === 'function')
          page.executeTask(id, { skipConfirm: true, skipModal: true });
        this.trackAndShowTray(id);
        break;
      case 'view_result':
        if (typeof page.showTaskResult === 'function') page.showTaskResult(id);
        else if (typeof page.openTask === 'function') page.openTask(id);
        break;
      case 'track':
      case 'respond':
      case 'inspect':
      default:
        if (typeof page.openTask === 'function') page.openTask(id);
        break;
    }
  }

  // ---------- Sticky execution tray (group 4) ----------
  //
  // A collapsible mini-player rendered from the workspace-scoped execution
  // controller. Starting/retrying a task tracks it in the controller and opens
  // the tray; collapsing or navigating never stops monitoring, because the
  // monitor lives in the controller, not in any modal (FR46-FR52).

  ensureExecController() {
    if (this.execController) return this.execController;
    const wsId = typeof this.workspaceId === 'function' ? this.workspaceId() : '';
    const realtime =
      typeof window !== 'undefined' &&
      window.workspaceRealtime &&
      typeof window.workspaceRealtime.subscribeToWorkspace === 'function'
        ? (workspaceId, handler) => window.workspaceRealtime.subscribeToWorkspace(workspaceId, handler)
        : null;
    this.execController = new WorkspaceExecutionController({
      workspaceId: wsId,
      fetchTask: async id => {
        const r = await fetch('/api/orchestration/tasks?id=' + encodeURIComponent(id));
        return r.ok ? await r.json() : null;
      },
      subscribeRealtime: realtime
    });
    // Repaint the tray on any controller state change (poll/realtime/terminal).
    this.execController.subscribe(() => this.renderTrayBody());
    return this.execController;
  }

  // Begin monitoring a task and surface it in the tray (FR46). Requires a real
  // DOM: without one (headless tests) there is nothing to render and no reason
  // to spin a polling monitor, so this is inert.
  trackAndShowTray(taskId) {
    const id = String(taskId || '').trim();
    if (!id) return;
    const el = this.ensureTray();
    if (!el) return;
    this.ensureExecController().track(id);
    this.trayOpen = true;
    this.trayCollapsed = false;
    this._trayCancelArmed = '';
    el.hidden = false;
    this.renderTrayBody();
    this.syncURLState();
  }

  closeTray() {
    // Closing the tray only hides the launcher — it never stops monitoring
    // (FR52). Runs keep running in the controller; reopening restores them.
    this.trayOpen = false;
    if (this.trayEl) this.trayEl.hidden = true;
    this.syncURLState();
  }

  // Collapsing/expanding is presentational (FR87): it never touches the URL,
  // but is still captured into the current history entry so Back/Forward can
  // restore it (FR88).
  toggleTrayCollapsed() {
    this.trayCollapsed = !this.trayCollapsed;
    this.renderTrayBody();
    this.captureHistoryPresentationState();
  }

  selectTrayRun(taskId) {
    if (this.execController) this.execController.select(taskId);
    this._trayCancelArmed = '';
    this.renderTrayBody();
    this.syncURLState();
  }

  ensureTray() {
    if (this.trayEl) return this.trayEl;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function') return null;
    const el = document.createElement('section');
    el.className = 'ws-cmd-tray';
    el.setAttribute('role', 'region');
    el.setAttribute('aria-label', 'Active execution');
    el.hidden = true;
    el.style.zIndex = 'var(--wsx-layer-tray)';
    el.addEventListener('click', event => {
      if (event.target.closest('[data-cmd-tray-collapse]')) {
        this.toggleTrayCollapsed();
        return;
      }
      if (event.target.closest('[data-cmd-tray-close]')) {
        this.closeTray();
        return;
      }
      const runChip = event.target.closest('[data-cmd-tray-run]');
      if (runChip) {
        this.selectTrayRun(runChip.getAttribute('data-cmd-tray-run'));
        return;
      }
      const cancelBtn = event.target.closest('[data-cmd-tray-cancel]');
      if (cancelBtn) {
        this.trayCancel(cancelBtn.getAttribute('data-cmd-tray-cancel'));
        return;
      }
      const actionBtn = event.target.closest('[data-cmd-tray-action]');
      if (actionBtn) {
        this.runDrawerAction(
          actionBtn.getAttribute('data-cmd-tray-action'),
          actionBtn.getAttribute('data-cmd-tray-task')
        );
        return;
      }
    });
    this.trayEl = el;
    if (this.container && this.container.appendChild) this.container.appendChild(el);
    return el;
  }

  renderTrayBody() {
    const el = this.trayEl;
    if (!el || !this.trayOpen) return;
    el.hidden = false;
    el.classList.toggle('is-collapsed', this.trayCollapsed);
    el.innerHTML = this.trayHTML();
  }

  trayElapsedLabel(run) {
    if (!run || !run.startedAt) return '';
    const end = run.phase === RUN_PHASE.SETTLED && run.lastActivityAt ? run.lastActivityAt : Date.now();
    const secs = Math.max(0, Math.round((end - run.startedAt) / 1000));
    if (secs < 60) return secs + 's';
    const mins = Math.floor(secs / 60);
    return mins + 'm ' + (secs % 60) + 's';
  }

  // Cancel from the tray requires an explicit, task-named confirmation and is
  // never triggered by dismissing the tray (FR63).
  trayCancel(taskId) {
    const id = String(taskId || '').trim();
    if (this._trayCancelArmed !== id) {
      this._trayCancelArmed = id;
      this.renderTrayBody();
      return;
    }
    this._trayCancelArmed = '';
    this.cancelRun(id);
    this.renderTrayBody();
  }

  // Cancel a running task: prefer a page-provided handler, else POST the
  // authoritative orchestration cancel endpoint. The controller's next poll
  // reconciles the resulting state.
  cancelRun(taskId) {
    const id = String(taskId || '').trim();
    if (!id) return;
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (page && typeof page.cancelTask === 'function') {
      page.cancelTask(id);
      return;
    }
    if (typeof fetch === 'function') {
      fetch('/api/orchestration/tasks/' + encodeURIComponent(id) + '/cancel', { method: 'POST' }).catch(
        () => {}
      );
    }
  }

  // Announce only when the selected run's state actually changes (not on
  // every poll tick / activity-log line) — a concise polite live region,
  // distinct from the raw (non-live) activity log below (FR127).
  trayAnnounceText(run) {
    if (!run || !run.presentation) return this._trayAnnounceText || '';
    if (!this._trayAnnouncedStateByTask) this._trayAnnouncedStateByTask = {};
    const state = run.presentation.state;
    if (this._trayAnnouncedStateByTask[run.taskId] === state) return this._trayAnnounceText || '';
    this._trayAnnouncedStateByTask[run.taskId] = state;
    const title = String((run.task && (run.task.description || run.task.name)) || run.taskId);
    this._trayAnnounceText = title + ': ' + (run.presentation.label || state);
    return this._trayAnnounceText;
  }

  trayHTML() {
    const c = this.execController;
    const run = c ? c.getSelected() : null;
    if (!run) {
      return '<div class="ws-cmd-tray-empty">No active run.</div>';
    }
    const pres = run.presentation || {};
    const task = run.task || {};
    const title = String(task.description || task.name || task.title || run.taskId);
    const assignee = pres.assignee || 'Unassigned';
    const elapsed = this.trayElapsedLabel(run);
    const lastActivity = run.activity && run.activity.length ? run.activity[run.activity.length - 1] : null;
    const activityText = lastActivity ? String(lastActivity.label || lastActivity.state || '') : '';
    const others = c.getRuns().filter(r => r.taskId !== run.taskId);
    const attention = c.getAttentionRuns().length;

    const header =
      '<div class="ws-cmd-tray-head tone-' +
      escapeHtml(pres.tone || 'neutral') +
      '">' +
      '<span class="ws-cmd-tray-state">' +
      escapeHtml(pres.label || 'Starting') +
      '</span>' +
      '<span class="ws-cmd-tray-title">' +
      escapeHtml(title) +
      '</span>' +
      '<span class="ws-cmd-tray-meta">' +
      escapeHtml(assignee) +
      (elapsed ? ' · ' + escapeHtml(elapsed) : '') +
      '</span>' +
      '<div class="ws-cmd-tray-controls">' +
      '<button type="button" class="ws-cmd-tray-btn" data-cmd-tray-collapse aria-expanded="' +
      (this.trayCollapsed ? 'false' : 'true') +
      '" aria-label="' +
      (this.trayCollapsed ? 'Expand execution tray' : 'Collapse execution tray') +
      '">' +
      (this.trayCollapsed ? '▴' : '▾') +
      '</button>' +
      '<button type="button" class="ws-cmd-tray-btn" data-cmd-tray-close aria-label="Hide execution tray">×</button>' +
      '</div></div>' +
      '<div class="ws-cmd-tray-live sr-only" role="status" aria-live="polite" aria-atomic="true">' +
      escapeHtml(this.trayAnnounceText(run)) +
      '</div>';

    if (this.trayCollapsed) {
      return (
        header +
        (activityText
          ? '<div class="ws-cmd-tray-collapsed-activity">' + escapeHtml(activityText) + '</div>'
          : '') +
        (attention
          ? '<div class="ws-cmd-tray-attention">' + escapeHtml(String(attention)) + ' need attention</div>'
          : '')
      );
    }

    // Run switcher across concurrent runs (FR53).
    const switcher = others.length
      ? '<div class="ws-cmd-tray-switcher" role="group" aria-label="Active runs">' +
        c
          .getRuns()
          .map(r => {
            const rp = r.presentation || {};
            const rt = String((r.task && (r.task.description || r.task.name)) || r.taskId);
            const selected = r.taskId === run.taskId;
            return (
              '<button type="button" class="ws-cmd-tray-run tone-' +
              escapeHtml(rp.tone || 'neutral') +
              (selected ? ' is-selected' : '') +
              '" data-cmd-tray-run="' +
              escapeHtml(r.taskId) +
              '" aria-current="' +
              (selected ? 'true' : 'false') +
              '">' +
              escapeHtml(rt.slice(0, 24)) +
              '</button>'
            );
          })
          .join('') +
        '</div>'
      : '';

    // Not a live region (FR127): raw streaming activity must not be announced
    // line by line. State-transition announcements are handled separately by
    // the small polite region below, which updates only on a state change.
    const activityLog =
      '<div class="ws-cmd-tray-log" role="log">' +
      (run.activity && run.activity.length
        ? run.activity
            .slice(-8)
            .map(a => '<div class="ws-cmd-tray-log-line">' + escapeHtml(String(a.label || a.state || '')) + '</div>')
            .join('')
        : '<div class="ws-cmd-tray-log-line is-muted">Starting…</div>') +
      '</div>';

    // Actions: terminal/needs-input reuse the shared primary action; a running
    // task additionally offers a confirmed Cancel.
    const isRunning = pres.state === 'running';
    const primary = pres.primaryAction
      ? '<button type="button" class="ws-cmd-tray-action" data-cmd-tray-action="' +
        escapeHtml(pres.primaryAction.id) +
        '" data-cmd-tray-task="' +
        escapeHtml(run.taskId) +
        '">' +
        escapeHtml(pres.primaryAction.label) +
        '</button>'
      : '';
    const cancel = isRunning
      ? '<button type="button" class="ws-cmd-tray-cancel' +
        (this._trayCancelArmed === run.taskId ? ' is-armed' : '') +
        '" data-cmd-tray-cancel="' +
        escapeHtml(run.taskId) +
        '">' +
        (this._trayCancelArmed === run.taskId ? 'Confirm cancel “' + escapeHtml(title.slice(0, 20)) + '”' : 'Cancel') +
        '</button>'
      : '';

    return (
      header +
      switcher +
      activityLog +
      '<div class="ws-cmd-tray-actions">' +
      primary +
      cancel +
      '</div>'
    );
  }

  renderMapStationsPanel() {
    const stats = this.computeStats();
    const systemsCount = this.systemTabs().length;
    return (
      '<div class="ws-cmd-map-window-section is-stations">' +
      this.mapZoneHeaderHTML('Stations', 'Tools & Systems', stats.tools + systemsCount) +
      '<div class="ws-cmd-map-station-grid">' +
      '<button type="button" class="ws-cmd-map-station" data-cmd-map-open-modal="tools"><span>MCP + Skills</span><strong>' +
      escapeHtml(stats.tools) +
      '</strong></button>' +
      '<button type="button" class="ws-cmd-map-station" data-cmd-map-inventory-action="systems"><span>Systems</span><strong>' +
      escapeHtml(systemsCount) +
      '</strong></button>' +
      '</div>' +
      '</div>'
    );
  }

  // A single agent "unit" card. `commandNode` renders the entry agent as the
  // larger, role-framed command node (FR66-67, FR70); otherwise a standard
  // specialist card. Runtime tone classes (working/waiting/needs-input/done)
  // apply identically to both, so status color never depends on role (FR40).
  renderMapAgentUnit(agent, index, commandNode) {
    const selected = agent.key === this.selectedAgentKey;
    const destination = agent.destination || 'hub';
    const statusLabel = agent.status?.label || 'Idle';
    const entryBadge =
      agent.entry && !commandNode
        ? '<span class="ws-cmd-map-entry-badge" title="Entry Agent"><i class="bi bi-star-fill" aria-hidden="true"></i><span>Entry</span></span>'
        : '';
    const roleLine = commandNode
      ? '<span class="ws-cmd-map-command-role">Entry Agent</span>'
      : '';
    const orchestrationCopy =
      commandNode && this._commandNodeOrchestrationCopy
        ? '<span class="ws-cmd-map-command-copy">' +
          escapeHtml(this._commandNodeOrchestrationCopy) +
          '</span>'
        : '';
    const accessibleName =
      'Select ' +
      escapeHtml(agent.name) +
      ', ' +
      (agent.entry ? 'Entry Agent. ' : '') +
      (commandNode && this._commandNodeOrchestrationCopy
        ? escapeHtml(this._commandNodeOrchestrationCopy) + '. '
        : '') +
      escapeHtml(statusLabel);
    return (
      '<button type="button" class="ws-cmd-map-agent ' +
      escapeHtml(agent.tone) +
      (selected ? ' is-selected' : '') +
      (commandNode ? ' is-command-node' : '') +
      ' toward-' +
      escapeHtml(destination) +
      '" data-cmd-map-select-agent="' +
      escapeHtml(agent.encodedName) +
      '" data-agent-key="' +
      escapeHtml(agent.key) +
      '" style="--agent-map-index:' +
      index +
      '" aria-pressed="' +
      (selected ? 'true' : 'false') +
      '" aria-label="' +
      accessibleName +
      '">' +
      '<span class="ws-cmd-map-agent-path" aria-hidden="true"></span>' +
      '<span class="ws-cmd-map-agent-status" aria-hidden="true" title="' +
      escapeHtml(statusLabel) +
      '"><span class="ws-cmd-led ' +
      escapeHtml(agent.tone) +
      '"></span></span>' +
      entryBadge +
      this.agentCharacterHTML(agent, 'roster') +
      roleLine +
      '<span class="ws-cmd-map-agent-copy"><strong>' +
      escapeHtml(agent.name) +
      '</strong><span>' +
      escapeHtml(agent.role?.label || 'Agent') +
      '</span><em>' +
      escapeHtml(statusLabel) +
      '</em></span>' +
      orchestrationCopy +
      '</button>'
    );
  }

  // Specialist count for the command node's orchestration copy: the current
  // roster excluding the entry agent, unassigned placeholders, and duplicate
  // records — agentGroups() already dedupes and drops unassigned (FR69).
  commandNodeOrchestrationCopy(specialistCount) {
    if (specialistCount <= 0) return 'No specialist agents yet';
    return 'Routes work to ' + specialistCount + ' specialist agent' + (specialistCount === 1 ? '' : 's');
  }

  renderMapAgentUnits(agents) {
    if (!agents.length) {
      return (
        '<div class="ws-cmd-map-empty is-agent-empty">' +
        '<strong>No agents in this workspace</strong>' +
        '<span>Add an agent to begin assigning work.</span>' +
        '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-agent>Add Agent</button>' +
        '</div>'
      );
    }

    const entry = agents.find(a => a.entry);
    const specialists = agents.filter(a => a !== entry);

    // No valid entry agent: show a repair state at the command position rather
    // than promoting an arbitrary specialist visually (FR77). Backend routing
    // and assignment rules are untouched (FR78) — this is presentation only.
    if (!entry) {
      return (
        '<div class="ws-cmd-map-command-repair">' +
        '<strong>No entry agent</strong>' +
        '<span>Chats, routing, and task orchestration need an entry agent.</span>' +
        '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-agent>Create Entry Agent</button>' +
        '</div>' +
        '<div class="ws-cmd-map-agent-field">' +
        specialists.map((agent, index) => this.renderMapAgentUnit(agent, index, false)).join('') +
        '</div>'
      );
    }

    this._commandNodeOrchestrationCopy = this.commandNodeOrchestrationCopy(specialists.length);
    return (
      '<div class="ws-cmd-map-command-row">' +
      this.renderMapAgentUnit(entry, 0, true) +
      '</div>' +
      '<div class="ws-cmd-map-agent-field">' +
      specialists.map((agent, index) => this.renderMapAgentUnit(agent, index, false)).join('') +
      '</div>'
    );
  }

  renderMapAgentsZone(agents) {
    return (
      '<section class="ws-cmd-map-world" data-map-zone="agents" aria-label="Agent units">' +
      '<div class="ws-cmd-map-floor" aria-hidden="true"></div>' +
      this.renderMapAgentUnits(agents) +
      '</section>'
    );
  }

  renderMapAgentLoadout(agent) {
    const modelLine =
      !agent?.model?.empty && agent?.model?.label
        ? '<div class="ws-cmd-rpg-loadout-model"><small>Model</small>' +
          escapeHtml(agent.model.label) +
          '</div>'
        : '';
    return (
      '<section class="ws-cmd-rpg-loadout is-editable"><span>Loadout</span>' +
      modelLine +
      this.renderLoadoutEditor(agent) +
      '</section>'
    );
  }

  // Interactive Skills + MCP editor shared by the map Unit Sheet and the
  // details-mode Loadout tab, so both surfaces agree on what is editable.
  renderLoadoutEditor(agent) {
    if (!agent) return '';
    const page = this.page || {};
    const enc = agent.encodedName;
    const skills =
      typeof page.getAgentWorkspaceSkillLoadout === 'function'
        ? page.getAgentWorkspaceSkillLoadout(agent.name)
        : [];
    const mcps =
      typeof page.getAgentWorkspaceMCPLoadout === 'function'
        ? page.getAgentWorkspaceMCPLoadout(agent.name)
        : [];
    return (
      '<div class="ws-cmd-loadout-editor">' +
      this.loadoutSectionHTML('skill', 'Skills', skills, enc) +
      this.loadoutSectionHTML('mcp', 'MCP Tools', mcps, enc) +
      (this.loadoutError
        ? '<p class="ws-cmd-loadout-editor-error" role="alert">' +
          escapeHtml(this.loadoutError) +
          '</p>'
        : '') +
      '</div>'
    );
  }

  loadoutSectionHTML(kind, label, items, encodedAgent) {
    const list = Array.isArray(items) ? items : [];
    const chips = list.length
      ? list
          .map(item => {
            if (item.locked) {
              return (
                '<span class="ws-cmd-loadout-chip is-locked" title="Always available in this workspace">' +
                escapeHtml(item.name) +
                '</span>'
              );
            }
            const busy = this.loadoutBusyKey === kind + ':' + item.bindingId;
            return (
              '<button type="button" class="ws-cmd-loadout-chip is-toggle ' +
              (item.enabled ? 'is-on' : 'is-off') +
              (busy ? ' is-busy' : '') +
              '" role="switch" aria-checked="' +
              (item.enabled ? 'true' : 'false') +
              '" data-cmd-loadout-toggle="' +
              escapeHtml(kind) +
              '" data-cmd-loadout-binding="' +
              escapeHtml(item.bindingId) +
              '" data-cmd-loadout-agent="' +
              escapeHtml(encodedAgent) +
              '"' +
              (busy ? ' disabled' : '') +
              '><span class="ws-cmd-loadout-chip-mark" aria-hidden="true">' +
              (item.enabled ? '✓' : '+') +
              '</span>' +
              escapeHtml(item.name) +
              '</button>'
            );
          })
          .join('')
      : '<span class="ws-cmd-loadout-empty">None bound to this workspace.</span>';
    const addOpen = this.loadoutAddOpen === kind;
    return (
      '<section class="ws-cmd-loadout-editor-section">' +
      '<header><span class="ws-cmd-loadout-kicker">' +
      escapeHtml(label) +
      '</span><button type="button" class="ws-cmd-loadout-add-btn' +
      (addOpen ? ' is-open' : '') +
      '" data-cmd-loadout-add="' +
      escapeHtml(kind) +
      '" data-cmd-loadout-agent="' +
      escapeHtml(encodedAgent) +
      '" aria-expanded="' +
      (addOpen ? 'true' : 'false') +
      '">' +
      (kind === 'mcp' ? 'Add Tool' : 'Add Skill') +
      '</button></header>' +
      '<div class="ws-cmd-loadout-chips">' +
      chips +
      '</div>' +
      (addOpen ? this.loadoutPickerHTML(kind, encodedAgent) : '') +
      '</section>'
    );
  }

  loadoutPickerHTML(kind, encodedAgent) {
    if (this.loadoutAddLoading) {
      return '<div class="ws-cmd-loadout-picker is-loading" aria-live="polite">Loading…</div>';
    }
    const options = Array.isArray(this.loadoutAddOptions) ? this.loadoutAddOptions : [];
    if (!options.length) {
      return '<div class="ws-cmd-loadout-picker is-empty">Nothing new to add.</div>';
    }
    return (
      '<div class="ws-cmd-loadout-picker" role="listbox" aria-label="Add ' +
      (kind === 'mcp' ? 'tool' : 'skill') +
      '">' +
      options
        .map(name => {
          const busy = this.loadoutBusyKey === kind + ':add:' + name;
          return (
            '<button type="button" class="ws-cmd-loadout-picker-item' +
            (busy ? ' is-busy' : '') +
            '" role="option" data-cmd-loadout-bind="' +
            escapeHtml(kind) +
            '" data-cmd-loadout-name="' +
            escapeHtml(name) +
            '" data-cmd-loadout-agent="' +
            escapeHtml(encodedAgent) +
            '"' +
            (busy ? ' disabled' : '') +
            '><i class="bi bi-plus-lg" aria-hidden="true"></i>' +
            escapeHtml(name) +
            '</button>'
          );
        })
        .join('') +
      '</div>'
    );
  }

  decodeAgentName(encodedName) {
    try {
      return decodeURIComponent(String(encodedName || '')).trim();
    } catch (_error) {
      return String(encodedName || '').trim();
    }
  }

  // Delegated handler for loadout chip toggles, Add buttons, and picker items.
  // Returns true if it handled the event (shared by map + garrison listeners).
  handleLoadoutClick(event) {
    const toggle = event.target.closest('[data-cmd-loadout-toggle]');
    if (toggle) {
      this.toggleLoadoutBinding(
        toggle.getAttribute('data-cmd-loadout-toggle'),
        toggle.getAttribute('data-cmd-loadout-agent'),
        toggle.getAttribute('data-cmd-loadout-binding'),
        toggle.getAttribute('aria-checked') !== 'true'
      );
      return true;
    }
    const add = event.target.closest('[data-cmd-loadout-add]');
    if (add) {
      this.openLoadoutPicker(
        add.getAttribute('data-cmd-loadout-add'),
        add.getAttribute('data-cmd-loadout-agent')
      );
      return true;
    }
    const bind = event.target.closest('[data-cmd-loadout-bind]');
    if (bind) {
      this.bindLoadoutCapability(
        bind.getAttribute('data-cmd-loadout-bind'),
        bind.getAttribute('data-cmd-loadout-agent'),
        bind.getAttribute('data-cmd-loadout-name')
      );
      return true;
    }
    return false;
  }

  async toggleLoadoutBinding(kind, encodedAgent, bindingId, enable) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.setAgentWorkspaceCapabilityEnabled !== 'function') return;
    const agentName = this.decodeAgentName(encodedAgent);
    this.loadoutError = '';
    this.loadoutBusyKey = kind + ':' + bindingId;
    this.render();
    try {
      await page.setAgentWorkspaceCapabilityEnabled(kind, agentName, bindingId, enable);
    } catch (error) {
      this.loadoutError =
        (kind === 'mcp' ? 'Tool' : 'Skill') + ' update failed: ' + (error?.message || 'error');
      if (window.Toast) window.Toast.error(this.loadoutError);
    } finally {
      this.loadoutBusyKey = '';
      this.render();
    }
  }

  async openLoadoutPicker(kind, _encodedAgent) {
    if (this.loadoutAddOpen === kind) {
      this.loadoutAddOpen = '';
      this.loadoutAddOptions = [];
      this.render();
      return;
    }
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    this.loadoutAddOpen = kind;
    this.loadoutAddLoading = true;
    this.loadoutAddOptions = [];
    this.loadoutError = '';
    this.render();
    try {
      this.loadoutAddOptions =
        page && typeof page.listAgentLoadoutAdditions === 'function'
          ? await page.listAgentLoadoutAdditions(kind)
          : [];
    } catch (_error) {
      this.loadoutAddOptions = [];
    } finally {
      this.loadoutAddLoading = false;
      // Only re-render if this picker is still the open one.
      if (this.loadoutAddOpen === kind) this.render();
    }
  }

  async bindLoadoutCapability(kind, encodedAgent, name) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.addAgentWorkspaceCapability !== 'function') return;
    const agentName = this.decodeAgentName(encodedAgent);
    const capName = String(name || '').trim();
    this.loadoutError = '';
    this.loadoutBusyKey = kind + ':add:' + capName;
    this.render();
    try {
      await page.addAgentWorkspaceCapability(kind, agentName, capName);
      this.loadoutAddOpen = '';
      this.loadoutAddOptions = [];
      if (window.Toast) {
        window.Toast.success((kind === 'mcp' ? 'Tool' : 'Skill') + ' "' + capName + '" added');
      }
    } catch (error) {
      this.loadoutError = 'Could not add ' + capName + ': ' + (error?.message || 'error');
      if (window.Toast) window.Toast.error(this.loadoutError);
    } finally {
      this.loadoutBusyKey = '';
      this.render();
    }
  }

  latestAgentSession(agent) {
    return this.recentActivityItems(agent).find(item => item.kind === 'Session') || null;
  }

  mapTaskCommandLabel(task) {
    if (!task) return '';
    const status = String(task?.status || '')
      .trim()
      .toLowerCase();
    if (this.isBlockedTask(task) || this.isNeedsInputTask(task)) return 'Resolve Quest';
    if (this.isWorkingTask(task)) return 'Track Quest';
    if (this.isQueuedTask(task)) return 'Open Queue';
    if (status === 'completed' || status === 'done') return 'Review Output';
    return 'Open Quest';
  }

  // Dispatch a pending quest in place. executeTask owns the live-execution
  // monitor and already guards against already-running tasks; we add a
  // deterministic raced-state check so a quest that stopped being pending
  // (started elsewhere, deleted) reports cleanly and refreshes.
  async startMapQuest(taskId) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.executeTask !== 'function') return;
    const id = String(taskId || '').trim();
    if (!id) return;
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const task = tasks.find(item => String(item?.id || '') === id);
    if (!task || !this.isQueuedTask(task)) {
      if (window.Toast) window.Toast.info('That quest is no longer pending.');
      if (typeof page.loadTasks === 'function') await page.loadTasks();
      return;
    }
    try {
      await page.executeTask(id, { skipConfirm: true, skipModal: true });
      this.trackAndShowTray(id);
    } catch (_error) {
      if (window.Toast) window.Toast.error('Could not start the quest.');
      if (typeof page.loadTasks === 'function') await page.loadTasks();
    }
  }

  renderMapAgentCommandMenu(agent, detailTarget) {
    const task = this.priorityTaskForAgent(agent) || agent?.currentTask;
    const taskId = String(task?.id || '').trim();
    const taskLabel = taskId ? this.mapTaskCommandLabel(task) : '';
    const taskTone = task ? this.taskTone(task.status) : '';
    const taskDetail = task
      ? this.taskStatusLabel(task.status || agent?.status?.label || 'pending')
      : 'No quest selected';
    const session = this.latestAgentSession(agent);
    const sessionId = String(session?.id || '').trim();
    const loadoutDetail =
      Number(agent?.skills?.count || 0) +
      ' skills / ' +
      Number(agent?.mcpNames?.length || 0) +
      ' tools';
    const action = ({ label, detail, icon, className = '', attrs = '', href = '' }) => {
      const content =
        '<i class="bi ' +
        escapeHtml(icon) +
        '" aria-hidden="true"></i><span><strong>' +
        escapeHtml(label) +
        '</strong><small>' +
        escapeHtml(detail) +
        '</small></span>';
      if (href) {
        return (
          '<a class="ws-cmd-rpg-command ' +
          escapeHtml(className) +
          '" href="' +
          escapeHtml(href) +
          '">' +
          content +
          '</a>'
        );
      }
      return (
        '<button type="button" class="ws-cmd-rpg-command ' +
        escapeHtml(className) +
        '" ' +
        attrs +
        '>' +
        content +
        '</button>'
      );
    };
    const actions = [];
    if (taskId) {
      const urgent = this.isBlockedTask(task) || this.isNeedsInputTask(task);
      const startable = this.isQueuedTask(task);
      if (startable) {
        // A pending quest can be dispatched in place instead of only opened.
        actions.push(
          action({
            label: 'Start Quest',
            detail: taskDetail,
            icon: 'bi-play-fill',
            className: 'is-primary is-' + taskTone,
            attrs: 'data-cmd-map-start-task="' + escapeHtml(taskId) + '"'
          })
        );
      } else {
        actions.push(
          action({
            label: taskLabel,
            detail: taskDetail,
            icon: urgent ? 'bi-exclamation-triangle' : 'bi-journal-check',
            className: urgent ? 'is-primary is-' + taskTone : 'is-' + taskTone,
            attrs: 'data-cmd-open-task="' + escapeHtml(taskId) + '"'
          })
        );
      }
    }
    actions.push(
      action({
        label: 'Give Task',
        detail: taskId ? 'New assignment' : 'Assign first quest',
        icon: 'bi-plus-lg',
        className: taskId ? '' : 'is-primary',
        attrs: 'data-cmd-add-task="' + escapeHtml(agent.encodedName) + '"'
      })
    );
    actions.push(
      sessionId
        ? action({
            label: 'Continue Session',
            detail: session.title || 'Open agent chat',
            icon: 'bi-chat-dots',
            attrs: 'data-cmd-open-session="' + escapeHtml(sessionId) + '"'
          })
        : action({
            label: 'Start Session',
            detail: 'Open agent chat',
            icon: 'bi-chat-dots',
            attrs: 'data-cmd-map-new-session="' + escapeHtml(agent.encodedName) + '"'
          })
    );
    actions.push(
      action({
        label: 'Configure Loadout',
        detail: loadoutDetail,
        icon: 'bi-sliders',
        attrs:
          'data-cmd-map-agent-tab="loadout" data-cmd-agent-name="' +
          escapeHtml(agent.encodedName) +
          '"'
      })
    );
    if (detailTarget?.href) {
      actions.push(
        action({
          label: 'Open Agent',
          detail: 'Full profile',
          icon: 'bi-person-vcard',
          href: detailTarget.href
        })
      );
    }
    return (
      '<section class="ws-cmd-rpg-command-panel"><header><span>Command Menu</span><strong>' +
      escapeHtml(agent.status?.label || 'Ready') +
      '</strong></header><div class="ws-cmd-rpg-command-grid">' +
      actions.join('') +
      '</div></section>'
    );
  }

  renderMapInspector(agent) {
    if (!agent) {
      return '<div class="ws-cmd-map-empty">Select an agent to inspect assignment, activity, and loadout.</div>';
    }
    const summary = this.agentActivitySummary(agent);
    const tasks = Array.isArray(agent.tasks) ? agent.tasks : [];
    const openTaskCount = tasks.filter(task => {
      const status = String(task?.status || '').toLowerCase();
      return !['completed', 'cancelled', 'timeout'].includes(status);
    }).length;
    const detailTarget = this.agentDetailTarget(agent);
    const classLabel = agent.role?.detail || agent.role?.label || 'Agent';
    const modelLabel = agent.model?.empty ? 'Model not set' : agent.model?.label || 'Model not set';
    const statCards = [
      { label: 'Quests', value: openTaskCount },
      { label: 'Skills', value: Number(agent.skills?.count || 0) },
      { label: 'Tools', value: agent.mcpNames.length },
      { label: 'Units', value: agent.instanceCount }
    ];
    const statusEffects = [
      agent.entry ? 'Entry Agent' : '',
      agent.status?.label || 'Idle',
      agent.model?.empty ? '' : agent.model?.label || ''
    ].filter(Boolean);
    return (
      '<div class="ws-cmd-map-inspector-card ' +
      escapeHtml(agent.tone) +
      '" aria-label="' +
      escapeHtml(agent.name) +
      ' sheet">' +
      '<div class="ws-cmd-map-agent-sheet-head">' +
      this.agentCharacterHTML(agent, 'roster') +
      '<div class="ws-cmd-map-agent-sheet-title"><span>Unit Sheet</span><strong>' +
      escapeHtml(agent.name) +
      '</strong><p>' +
      escapeHtml(agent.role?.label || 'Agent') +
      '</p></div></div>' +
      '<div class="ws-cmd-rpg-sheet">' +
      '<div class="ws-cmd-rpg-class-grid">' +
      '<div class="ws-cmd-rpg-class-card"><span>Class</span><strong>' +
      escapeHtml(classLabel) +
      '</strong></div>' +
      '<div class="ws-cmd-rpg-class-card"><span>Model</span><strong>' +
      escapeHtml(modelLabel) +
      '</strong></div>' +
      '</div>' +
      '<div class="ws-cmd-rpg-status-strip"><span class="ws-cmd-led ' +
      escapeHtml(agent.tone) +
      '"></span><strong>' +
      escapeHtml(agent.status?.label || 'Idle') +
      '</strong><span>' +
      escapeHtml(agent.status?.detail || 'No active tasks') +
      '</span></div>' +
      '<div class="ws-cmd-rpg-stat-grid">' +
      statCards
        .map(
          stat =>
            '<div class="ws-cmd-rpg-stat"><span>' +
            escapeHtml(stat.label) +
            '</span><strong>' +
            escapeHtml(stat.value) +
            '</strong></div>'
        )
        .join('') +
      '</div>' +
      '<section class="ws-cmd-rpg-quest-card"><span>Current Quest</span><strong>' +
      escapeHtml(summary?.taskLabel || 'No task in progress') +
      '</strong><p>' +
      escapeHtml(summary?.statusLabel || agent.status?.label || 'Idle') +
      (summary?.activityLabel ? ' · ' + escapeHtml(summary.activityLabel) : '') +
      (summary?.whenLabel ? ' · ' + escapeHtml(summary.whenLabel) : '') +
      '</p></section>' +
      this.renderMapAgentLoadout(agent) +
      '<div class="ws-cmd-rpg-sheet-row"><span>Recent Activity</span><strong>' +
      escapeHtml(summary?.activityLabel || 'No recent activity') +
      (summary?.whenLabel ? ' · ' + escapeHtml(summary.whenLabel) : '') +
      '</strong></div>' +
      '<div class="ws-cmd-rpg-effects">' +
      statusEffects.map(effect => '<span>' + escapeHtml(effect) + '</span>').join('') +
      '</div></div>' +
      this.renderMapAgentCommandMenu(agent, detailTarget) +
      '</div>'
    );
  }

  mapInventoryGroups() {
    const page = this.page || {};
    const folders = this.folderRowData();
    const files = this.fileRowData();
    return [
      {
        key: 'notes',
        label: 'Notes',
        icon: 'bi-journal-text',
        count: Array.isArray(page.notes) ? page.notes.length : 0,
        action: 'New Note',
        slotLabel: 'Note',
        items: (Array.isArray(page.notes) ? page.notes : []).map(note => ({
          label: note.name || note.title || 'Untitled Note',
          meta: '',
          section: 'notes',
          id: String(note.id || '')
        }))
      },
      {
        key: 'schedules',
        label: 'Schedules',
        icon: 'bi-calendar2-week',
        count: Array.isArray(page.schedules) ? page.schedules.length : 0,
        action: 'Open Schedules',
        slotLabel: 'Schedule',
        items: (Array.isArray(page.schedules) ? page.schedules : []).map(schedule => ({
          label: schedule.name || schedule.task_description || 'Unnamed Schedule',
          meta: '',
          section: 'schedules',
          id: String(schedule.id || '')
        }))
      },
      {
        key: 'sessions',
        label: 'Sessions',
        icon: 'bi-chat-dots',
        count: Array.isArray(page.sessions) ? page.sessions.length : 0,
        action: 'New Session',
        slotLabel: 'Session',
        items: (Array.isArray(page.sessions) ? page.sessions : []).map(session => ({
          label: session.title || session.name || 'Untitled Session',
          meta: session.agent_name || '',
          section: 'sessions',
          id: String(session.id || '')
        }))
      },
      {
        key: 'folders',
        label: 'Linked Folders',
        icon: 'bi-folder2-open',
        count: folders.length,
        action: 'Link Folder',
        slotLabel: 'Folder',
        items: folders.map(folder => ({
          label: folder.title || folder.name || folder.path || 'Unnamed Directory',
          meta: folder.path || '',
          section: 'folders',
          id: String(folder.id || ''),
          source: String(folder.source || 'reference')
        }))
      },
      {
        key: 'files',
        label: 'Files',
        icon: 'bi-file-earmark-text',
        count: files.length,
        action: 'Upload',
        slotLabel: 'File',
        items: files.map(file => ({
          label: this.fileTitle(file),
          meta: this.fileMeta(file),
          section: 'files',
          id: String(file?.id || file?.file_meta?.name || this.fileTitle(file))
        }))
      },
      {
        key: 'systems',
        label: 'Systems',
        icon: 'bi-cpu',
        count: this.systemTabs().length,
        action: 'Open Systems',
        slotLabel: 'System',
        items: this.systemTabs().map(tab => ({
          label: tab.label,
          meta: 'Workspace system',
          section: 'systems',
          id: tab.key
        }))
      }
    ];
  }

  renderMapInventoryItems(group) {
    const items = Array.isArray(group?.items) ? group.items : [];
    const groupKey = String(group?.key || 'item')
      .trim()
      .toLowerCase();
    const slotType = String(group?.slotLabel || group?.label || 'Item').trim();
    const visibleItems = items.slice(0, 11);
    const slotCount = Math.max(
      8,
      Math.min(12, visibleItems.length + (visibleItems.length ? 1 : 0))
    );
    const slots = visibleItems.map(item => {
      const attrs =
        item.section === 'systems'
          ? 'data-cmd-map-system-tab="' + escapeHtml(item.id) + '"'
          : 'data-cmd-open-section="' +
            escapeHtml(item.section) +
            '" data-cmd-item-id="' +
            escapeHtml(item.id) +
            '" data-cmd-item-source="' +
            escapeHtml(item.source || '') +
            '"';
      return (
        '<button type="button" class="ws-cmd-map-inventory-slot is-' +
        escapeHtml(groupKey) +
        '" ' +
        attrs +
        ' aria-label="Open ' +
        escapeHtml(item.label) +
        '"><span class="ws-cmd-map-slot-type">' +
        escapeHtml(slotType) +
        '</span><span class="ws-cmd-map-slot-icon"><i class="bi ' +
        escapeHtml(group?.icon || 'bi-box') +
        '" aria-hidden="true"></i></span><span class="ws-cmd-map-slot-name">' +
        escapeHtml(item.label) +
        '</span>' +
        (item.meta
          ? '<em>' + escapeHtml(item.meta) + '</em>'
          : '<em>' + escapeHtml(group?.label || 'Item') + '</em>') +
        '</button>'
      );
    });
    while (slots.length < slotCount) {
      const emptyLabel = slots.length === 0 ? 'Open Slot' : 'Empty Slot';
      slots.push(
        '<div class="ws-cmd-map-inventory-slot is-empty is-' +
          escapeHtml(groupKey) +
          '"><span class="ws-cmd-map-slot-type">' +
          escapeHtml(slotType) +
          '</span><span class="ws-cmd-map-slot-icon"><i class="bi bi-plus-lg" aria-hidden="true"></i></span><span class="ws-cmd-map-slot-name">' +
          escapeHtml(emptyLabel) +
          '</span><em>' +
          escapeHtml(group?.label || 'Item') +
          '</em></div>'
      );
    }
    return '<div class="ws-cmd-map-inventory-grid">' + slots.join('') + '</div>';
  }

  renderMapInventory() {
    const groups = this.mapInventoryGroups();
    const total = groups.reduce((sum, group) => sum + Number(group.count || 0), 0);
    const activeKey = this.mapInventorySection || groups[0]?.key || '';
    if (!this.mapInventorySection) this.mapInventorySection = activeKey;
    return (
      '<div class="ws-cmd-map-inventory-box">' +
      '<div class="ws-cmd-map-inventory-summary">' +
      '<div class="ws-cmd-map-inventory-total"><span>Items</span><strong>' +
      escapeHtml(total) +
      '</strong></div>' +
      '<div class="ws-cmd-map-inventory-badges">' +
      groups
        .map(
          group =>
            '<button type="button" class="ws-cmd-map-inventory-badge' +
            (activeKey === group.key ? ' is-active' : '') +
            '" data-cmd-map-inventory-section="' +
            escapeHtml(group.key) +
            '"><i class="bi ' +
            escapeHtml(group.icon || 'bi-box') +
            '" aria-hidden="true"></i><span>' +
            escapeHtml(group.label) +
            '</span><strong>' +
            escapeHtml(group.count) +
            '</strong></button>'
        )
        .join('') +
      '</div></div>' +
      '<div class="ws-cmd-map-inventory-drawer">' +
      groups
        .map(
          group =>
            '<section class="ws-cmd-map-inventory-group' +
            (activeKey === group.key ? ' is-active' : '') +
            '">' +
            '<header><div><span>' +
            escapeHtml(group.label) +
            '</span><strong>' +
            escapeHtml(group.count) +
            '</strong></div>' +
            '<button type="button" data-cmd-map-inventory-action="' +
            escapeHtml(group.key) +
            '">' +
            escapeHtml(group.action) +
            '</button></header>' +
            '<div class="ws-cmd-map-inventory-list">' +
            this.renderMapInventoryItems(group) +
            '</div></section>'
        )
        .join('') +
      '</div></div>'
    );
  }

  renderMapWindow(selectedAgent) {
    const key = String(this.activeMapWindow || '').trim();
    if (!key) return '';
    const option =
      this.mapWindowOptions().find(item => item.key === key) ||
      (key === 'inspector'
        ? { key: 'inspector', label: 'Unit Sheet', icon: 'bi-person-vcard' }
        : null);
    if (!option) return '';
    const body = {
      objective: () => this.renderMapMissionPanel(),
      objectives: () => this.renderMapTasksPanel(),
      inventory: () => this.renderMapInventory(),
      stations: () => this.renderMapStationsPanel(),
      inspector: () => this.renderMapInspector(selectedAgent)
    }[key];
    if (!body) return '';
    return (
      '<div class="ws-cmd-map-window-backdrop" data-cmd-map-window-backdrop>' +
      '<section class="ws-cmd-map-window ws-cmd-map-window-' +
      escapeHtml(key) +
      '" role="dialog" aria-modal="true" aria-label="' +
      escapeHtml(option.label) +
      '">' +
      '<header class="ws-cmd-map-window-head"><div><i class="bi ' +
      escapeHtml(option.icon) +
      '" aria-hidden="true"></i><span>' +
      escapeHtml(option.label) +
      '</span></div>' +
      '<button type="button" class="ws-cmd-map-window-close" data-cmd-map-window-close aria-label="Close map window">×</button></header>' +
      '<div class="ws-cmd-map-window-body">' +
      body() +
      '</div></section></div>'
    );
  }

  renderOperationsMap() {
    const { agents, selected } = this.mapAgentViewModels();
    return (
      '<div class="ws-cmd-map-shell">' +
      '<div class="ws-cmd-opmap" role="region" aria-label="Workspace operations map">' +
      this.renderMapAgentsZone(agents) +
      this.renderMapToolTray() +
      '</div>' +
      this.renderMapQuickTask() +
      this.renderMapWindow(selected) +
      '</div>'
    );
  }

  // Floating "New Quest" button + inline composer. Creating with no assignee lets the
  // server stamp the task onto the workspace's entry agent (entry_agent_default).
  renderMapQuickTask() {
    // The task drawer docks to the same bottom-right corner and closes this
    // composer on open (openTaskDrawer); don't render a dead, invisible FAB
    // underneath it — the drawer's own "+ Add Task" covers the same need.
    if (this.taskDrawerOpen) return '';
    const open = this.taskComposerOpen;
    const submitting = this.taskComposerSubmitting;
    const button =
      '<button type="button" class="ws-cmd-map-quest-fab' +
      (open ? ' is-open' : '') +
      '" data-cmd-map-quest-toggle aria-haspopup="dialog" aria-expanded="' +
      (open ? 'true' : 'false') +
      '" aria-label="New quest"><i class="bi bi-plus-lg" aria-hidden="true"></i><span>New Quest</span></button>';
    if (!open) {
      return '<div class="ws-cmd-map-quest-dock">' + button + '</div>';
    }
    const draft = escapeHtml(this.taskComposerDraft || '');
    const error = this.taskComposerError
      ? '<p class="ws-cmd-map-quest-error" role="alert">' +
        escapeHtml(this.taskComposerError) +
        '</p>'
      : '';
    const disabledAttr = submitting ? ' disabled' : '';
    return (
      '<div class="ws-cmd-map-quest-dock is-open">' +
      button +
      '<section class="ws-cmd-map-quest-composer" role="dialog" aria-modal="false" aria-label="New quest">' +
      '<header class="ws-cmd-map-quest-head"><span>New Quest</span>' +
      '<button type="button" class="ws-cmd-map-quest-close" data-cmd-map-quest-cancel aria-label="Close new quest">×</button></header>' +
      '<textarea class="ws-cmd-map-quest-input" data-cmd-map-quest-input rows="2" ' +
      'placeholder="Describe the quest… (assigned to the entry agent)"' +
      disabledAttr +
      '>' +
      draft +
      '</textarea>' +
      error +
      '<div class="ws-cmd-map-quest-actions">' +
      '<button type="button" class="ws-cmd-map-quest-btn is-primary" data-cmd-map-quest-create' +
      disabledAttr +
      '>' +
      (submitting ? 'Creating…' : 'Create') +
      '</button>' +
      '<button type="button" class="ws-cmd-map-quest-btn" data-cmd-map-quest-start' +
      disabledAttr +
      '>Create &amp; Start</button>' +
      '</div>' +
      '<p class="ws-cmd-map-quest-hint">Enter to create · ⌘/Ctrl+Enter to create &amp; start</p>' +
      '</section></div>'
    );
  }

  openTaskComposer() {
    this.taskComposerOpen = true;
    this.taskComposerError = '';
    this.render();
  }

  closeTaskComposer({ clearDraft = true } = {}) {
    this.taskComposerOpen = false;
    this.taskComposerError = '';
    this.taskComposerSubmitting = false;
    if (clearDraft) this.taskComposerDraft = '';
    this.render();
  }

  async submitTaskComposer({ start = false } = {}) {
    if (this.taskComposerSubmitting) return;
    const description = String(this.taskComposerDraft || '').trim();
    if (!description) {
      this.taskComposerError = 'Enter a quest description.';
      this.render();
      return;
    }
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.createTask !== 'function') return;

    this.taskComposerSubmitting = true;
    this.taskComposerError = '';
    this.render();

    let created = null;
    try {
      // No assignee → server applies the entry-agent default; suppress the generic
      // "Task created" toast so we can name the resolved assignee instead.
      created = await page.createTask(description, '', '', { successToast: false });
    } catch (_error) {
      created = null;
    }

    if (!created || !created.id) {
      this.taskComposerSubmitting = false;
      this.taskComposerError = 'Could not create the quest. Try again.';
      this.render();
      return;
    }

    const assignee = String(created.to || '').trim();
    if (start && typeof page.executeTask === 'function') {
      try {
        await page.executeTask(created.id, { skipConfirm: true });
      } catch (_error) {
        /* execution failures surface via executeTask's own toast */
      }
    }
    if (window.Toast) {
      const target = assignee || 'the entry agent';
      window.Toast.success(
        (start ? 'Quest started · assigned to ' : 'Quest assigned to ') + target
      );
    }
    // loadTasks() (via createTask/executeTask) already refreshed the view; close cleanly.
    this.taskComposerDraft = '';
    this.taskComposerOpen = false;
    this.taskComposerSubmitting = false;
    this.taskComposerError = '';
    this.render();
  }

  runMapInventoryAction(sectionKey, triggerButton) {
    const section = String(sectionKey || '').trim();
    if (section === 'systems') {
      this.setCommandViewMode('details', { focus: false });
      this.openSystemTab(this.activeSystemTab || 'memory');
      return;
    }
    this.runRailPrimaryAction(section, triggerButton);
  }

  bindOperationsMap() {
    const root = this.container && this.container.querySelector('.ws-cmd-map-shell');
    if (!root) return;
    root.addEventListener('click', event => {
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
      if (this.handleLoadoutClick(event)) return;
      const questToggle = event.target.closest('[data-cmd-map-quest-toggle]');
      if (questToggle) {
        if (this.taskComposerOpen) this.closeTaskComposer();
        else this.openTaskComposer();
        return;
      }
      if (event.target.closest('[data-cmd-map-quest-cancel]')) {
        this.closeTaskComposer();
        return;
      }
      if (event.target.closest('[data-cmd-map-quest-create]')) {
        this.submitTaskComposer({ start: false });
        return;
      }
      if (event.target.closest('[data-cmd-map-quest-start]')) {
        this.submitTaskComposer({ start: true });
        return;
      }
      // Click outside the composer (but still on the map) dismisses it, keeping the draft.
      if (this.taskComposerOpen && !event.target.closest('.ws-cmd-map-quest-dock')) {
        this.closeTaskComposer({ clearDraft: false });
        return;
      }
      const closeWindow = event.target.closest('[data-cmd-map-window-close]');
      if (closeWindow) {
        this.activeMapWindow = '';
        this.render();
        return;
      }
      const backdrop = event.target.closest('[data-cmd-map-window-backdrop]');
      if (backdrop && event.target === backdrop) {
        this.activeMapWindow = '';
        this.render();
        return;
      }
      const windowBtn = event.target.closest('[data-cmd-map-window]');
      if (windowBtn) {
        const nextWindow = windowBtn.getAttribute('data-cmd-map-window');
        this.activeMapWindow = this.activeMapWindow === nextWindow ? '' : nextWindow;
        if (this.activeMapWindow === 'inventory' && !this.mapInventorySection) {
          this.mapInventorySection = 'notes';
        }
        this.render();
        return;
      }
      const selectBtn = event.target.closest('[data-cmd-map-select-agent]');
      if (selectBtn) {
        this.activeMapWindow = 'inspector';
        this.selectAgent(selectBtn.getAttribute('data-cmd-map-select-agent'), { focus: false });
        return;
      }
      const addAgentBtn = event.target.closest('[data-cmd-add-agent]');
      if (addAgentBtn && page && typeof page.openAddAgentModal === 'function') {
        page.openAddAgentModal();
        return;
      }
      const addTaskBtn = event.target.closest('[data-cmd-add-task]');
      if (addTaskBtn && page && typeof page.showAddTaskModalForAgent === 'function') {
        page.showAddTaskModalForAgent(addTaskBtn.getAttribute('data-cmd-add-task'));
        return;
      }
      const newSessionBtn = event.target.closest('[data-cmd-map-new-session]');
      if (newSessionBtn && page) {
        if (typeof page.createNewSessionForAgent === 'function') {
          page.createNewSessionForAgent(newSessionBtn.getAttribute('data-cmd-map-new-session'));
        } else if (typeof page.createNewSession === 'function') {
          page.createNewSession();
        }
        return;
      }
      const startTaskBtn = event.target.closest('[data-cmd-map-start-task]');
      if (startTaskBtn) {
        this.startMapQuest(startTaskBtn.getAttribute('data-cmd-map-start-task'));
        return;
      }
      const openTaskBtn = event.target.closest('[data-cmd-open-task]');
      if (openTaskBtn && page && typeof page.openTask === 'function') {
        page.openTask(openTaskBtn.getAttribute('data-cmd-open-task'));
        return;
      }
      const openSessionBtn = event.target.closest('[data-cmd-open-session]');
      if (openSessionBtn && page && typeof page.openSession === 'function') {
        page.openSession(openSessionBtn.getAttribute('data-cmd-open-session'));
        return;
      }
      const agentTabBtn = event.target.closest('[data-cmd-map-agent-tab]');
      if (agentTabBtn) {
        const encodedName = agentTabBtn.getAttribute('data-cmd-agent-name');
        if (encodedName) this.selectAgent(encodedName, { focus: false });
        this.setCommandViewMode('details', { focus: false });
        this.setActiveAgentTab(agentTabBtn.getAttribute('data-cmd-map-agent-tab'));
        return;
      }
      const drawerBtn = event.target.closest('[data-cmd-open-task-drawer]');
      if (drawerBtn) {
        this.openTaskDrawer(drawerBtn);
        return;
      }
      const modalBtn = event.target.closest('[data-cmd-map-open-modal]');
      if (modalBtn) {
        this.openStatModal(modalBtn.getAttribute('data-cmd-map-open-modal'), modalBtn);
        return;
      }
      const inventoryToggle = event.target.closest('[data-cmd-map-inventory-toggle]');
      if (inventoryToggle) {
        this.activeMapWindow = this.activeMapWindow === 'inventory' ? '' : 'inventory';
        this.mapInventoryOpen = this.activeMapWindow === 'inventory';
        if (!this.mapInventorySection) this.mapInventorySection = 'notes';
        this.render();
        return;
      }
      const inventorySection = event.target.closest('[data-cmd-map-inventory-section]');
      if (inventorySection) {
        this.activeMapWindow = 'inventory';
        this.mapInventoryOpen = true;
        this.mapInventorySection = inventorySection.getAttribute('data-cmd-map-inventory-section');
        this.render();
        return;
      }
      const inventoryAction = event.target.closest('[data-cmd-map-inventory-action]');
      if (inventoryAction) {
        this.runMapInventoryAction(
          inventoryAction.getAttribute('data-cmd-map-inventory-action'),
          inventoryAction
        );
        return;
      }
      const systemTab = event.target.closest('[data-cmd-map-system-tab]');
      if (systemTab) {
        this.setCommandViewMode('details', { focus: false });
        this.openSystemTab(systemTab.getAttribute('data-cmd-map-system-tab'));
        return;
      }
      const itemBtn = event.target.closest('[data-cmd-open-section][data-cmd-item-id]');
      if (itemBtn) {
        this.openRailItem(
          itemBtn.getAttribute('data-cmd-open-section'),
          itemBtn.getAttribute('data-cmd-item-id'),
          itemBtn.getAttribute('data-cmd-item-source')
        );
      }
    });
    this.bindTaskComposer(root);
  }

  // Wire the quest composer's textarea: track the draft and handle keyboard submits.
  // The composer only exists in the DOM while open, so this re-binds on each render.
  bindTaskComposer(root) {
    if (!root || !this.taskComposerOpen) return;
    const input = root.querySelector('[data-cmd-map-quest-input]');
    if (!input) return;
    input.addEventListener('input', event => {
      this.taskComposerDraft = event.target.value;
      if (this.taskComposerError) this.taskComposerError = '';
    });
    input.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        this.closeTaskComposer();
        return;
      }
      if (event.key === 'Enter') {
        // Newlines are not needed for a one-line quest; Enter submits.
        event.preventDefault();
        this.taskComposerDraft = event.target.value;
        this.submitTaskComposer({ start: event.metaKey || event.ctrlKey });
      }
    });
    // Restore focus + caret after the re-render that opened/updated the composer.
    if (!this.taskComposerSubmitting && typeof input.focus === 'function') {
      input.focus();
      const end = input.value.length;
      if (typeof input.setSelectionRange === 'function') {
        try {
          input.setSelectionRange(end, end);
        } catch (_error) {
          /* setSelectionRange unsupported on some inputs */
        }
      }
    }
  }

  captureAgentDeckViewState() {
    if (!this.container || typeof this.container.querySelector !== 'function') return;
    const roster = this.container.querySelector('.ws-cmd-roster-list');
    const overview = this.container.querySelector('.ws-cmd-agent-panel-body');
    if (roster) {
      this.agentRosterScroll = {
        top: Number(roster.scrollTop || 0),
        left: Number(roster.scrollLeft || 0)
      };
    }
    if (overview) this.agentOverviewScroll = Number(overview.scrollTop || 0);
  }

  restoreAgentDeckViewState() {
    if (!this.container || typeof this.container.querySelector !== 'function') return;
    const roster = this.container.querySelector('.ws-cmd-roster-list');
    const overview = this.container.querySelector('.ws-cmd-agent-panel-body');
    if (roster) {
      roster.scrollTop = Number(this.agentRosterScroll?.top || 0);
      roster.scrollLeft = Number(this.agentRosterScroll?.left || 0);
    }
    if (overview) overview.scrollTop = this.agentOverviewScroll || 0;

    if (this.pendingAgentFocusKey) {
      const target = this.container.querySelector(
        '[data-agent-key="' + this.pendingAgentFocusKey + '"]'
      );
      if (target && typeof target.focus === 'function') target.focus();
      this.pendingAgentFocusKey = '';
    }
    if (this.pendingAgentTabFocus) {
      const target = this.container.querySelector(
        '[data-cmd-agent-tab="' + this.pendingAgentTabFocus + '"]'
      );
      if (target && typeof target.focus === 'function') target.focus();
      this.pendingAgentTabFocus = '';
    }
  }

  selectAgent(encodedName, { focus = true } = {}) {
    let name = '';
    try {
      name = decodeURIComponent(String(encodedName || ''));
    } catch (_error) {
      name = String(encodedName || '');
    }
    const key = this.normalizeAgentKey(name);
    if (!key) return;
    this.selectedAgentKey = key;
    this.agentSelectionInitialized = true;
    this.persistAgentKey(key);
    this.agentOverviewScroll = 0;
    if (focus) this.pendingAgentFocusKey = key;
    this.render();
    this.syncURLState();
  }

  setActiveAgentTab(key, { focus = true } = {}) {
    const normalized = String(key || '').toLowerCase();
    if (!AGENT_TAB_KEYS.includes(normalized)) return;
    this.activeAgentTab = normalized;
    this.agentOverviewScroll = 0;
    if (focus) this.pendingAgentTabFocus = normalized;
    this.render();
  }

  hydrateActiveAgentPrompt({ force = false } = {}) {
    if (this.activeAgentTab !== 'loadout') return;
    const page = this.page || {};
    if (typeof page.ensureAgentPromptData !== 'function') return;
    const groups = this.agentGroups();
    const group = groups.agents.find(
      item => this.normalizeAgentKey(item.key || item.name) === this.selectedAgentKey
    );
    const agent = this.agentViewModel(group);
    if (!agent) return;
    const cached = this.promptCacheEntry(agent);
    if (!force && cached) return;
    if (this.agentPromptLoadingKey === agent.key) return;

    this.agentPromptLoadingKey = agent.key;
    Promise.resolve(page.ensureAgentPromptData(agent.name, force ? { force: true } : {}))
      .catch(() => ({ error: true }))
      .finally(() => {
        if (this.agentPromptLoadingKey === agent.key) this.agentPromptLoadingKey = '';
        if (
          this.active &&
          this.selectedAgentKey === agent.key &&
          this.activeAgentTab === 'loadout'
        ) {
          this.render();
        }
      });
  }

  handleAgentTabKeydown(event) {
    const current = event?.target?.closest?.('[data-cmd-agent-tab]');
    if (!current) return;
    const root = this.container?.querySelector?.('.ws-cmd-agent-tabs');
    const tabs =
      root && typeof root.querySelectorAll === 'function'
        ? Array.from(root.querySelectorAll('[data-cmd-agent-tab]'))
        : [];
    if (!tabs.length) return;
    const index = tabs.indexOf(current);
    let next = index;
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
    else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = tabs.length - 1;
    else return;
    event.preventDefault();
    this.setActiveAgentTab(tabs[next].getAttribute('data-cmd-agent-tab'));
  }

  bindGarrison() {
    const root = this.container && this.container.querySelector('.ws-cmd-garrison');
    if (!root) return;
    root.addEventListener('keydown', event => this.handleAgentTabKeydown(event));
    root.addEventListener('click', event => {
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
      if (this.handleLoadoutClick(event)) return;
      const selectBtn = event.target.closest('[data-cmd-select-agent]');
      if (selectBtn) {
        this.selectAgent(selectBtn.getAttribute('data-cmd-select-agent'));
        return;
      }
      const tabBtn = event.target.closest('[data-cmd-agent-tab]');
      if (tabBtn) {
        this.setActiveAgentTab(tabBtn.getAttribute('data-cmd-agent-tab'), { focus: false });
        return;
      }
      const settingsBtn = event.target.closest('[data-cmd-manager-settings]');
      if (settingsBtn) {
        this.openStatModal('settings', settingsBtn);
        return;
      }
      const addAgentBtn = event.target.closest('[data-cmd-add-agent]');
      if (addAgentBtn && page && typeof page.openAddAgentModal === 'function') {
        page.openAddAgentModal();
        return;
      }
      const promptBtn = event.target.closest('[data-cmd-view-prompt]');
      if (promptBtn && page && typeof page.openAgentPromptModal === 'function') {
        page.openAgentPromptModal(promptBtn.getAttribute('data-cmd-view-prompt'));
        return;
      }
      const retryBtn = event.target.closest('[data-cmd-retry-prompt]');
      if (retryBtn) {
        this.hydrateActiveAgentPrompt({ force: true });
        return;
      }
      const modelBtn = event.target.closest('[data-cmd-edit-model]');
      if (modelBtn && page && typeof page.openAgentModelModal === 'function') {
        page.openAgentModelModal(modelBtn.getAttribute('data-cmd-edit-model'));
        return;
      }
      const removeBtn = event.target.closest('[data-cmd-remove-agent]');
      if (removeBtn && page && typeof page.removeAgentFromWorkspace === 'function') {
        page.removeAgentFromWorkspace(removeBtn.getAttribute('data-cmd-remove-agent'));
        return;
      }
      const addBtn = event.target.closest('[data-cmd-add-task]');
      if (addBtn && page && page.showAddTaskModalForAgent) {
        page.showAddTaskModalForAgent(addBtn.getAttribute('data-cmd-add-task'));
        return;
      }
      const runBtn = event.target.closest('[data-cmd-run-task]');
      if (runBtn && page && page.executeTask) {
        page.executeTask(runBtn.getAttribute('data-cmd-run-task'));
        return;
      }
      const openBtn = event.target.closest('[data-cmd-open-task]');
      if (openBtn && page && page.openTask) {
        page.openTask(openBtn.getAttribute('data-cmd-open-task'));
        return;
      }
      const sessionBtn = event.target.closest('[data-cmd-open-session]');
      if (sessionBtn && page && page.openSession) {
        page.openSession(sessionBtn.getAttribute('data-cmd-open-session'));
        return;
      }
      const managerBtn = event.target.closest('[data-cmd-open-agent-manager]');
      if (managerBtn)
        this.openStatModal(managerBtn.getAttribute('data-cmd-open-agent-manager'), managerBtn);
    });
  }

  // ---------- right rail ----------

  railPanelHTML(sectionKey, title, items, count, emptyText, primaryLabel) {
    const isManaging = this.activeRailSection === sectionKey;
    const hasItems = items.length > 0;
    const shouldShowBody = hasItems || isManaging;
    const body = hasItems
      ? items.join('')
      : '<div class="ws-cmd-rail-empty">' + escapeHtml(emptyText) + '</div>';
    return (
      '<section class="ws-cmd-panel' +
      (isManaging ? ' is-managing' : '') +
      (!hasItems ? ' is-empty' : '') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>' +
      escapeHtml(title) +
      '</h4><span class="ws-cmd-panel-count">' +
      count +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="' +
      escapeHtml(sectionKey) +
      '">' +
      escapeHtml(primaryLabel) +
      '</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="' +
      escapeHtml(sectionKey) +
      '" aria-expanded="' +
      (isManaging ? 'true' : 'false') +
      '" title="' +
      (isManaging ? 'Close Command manager' : 'Manage in Command view') +
      '" aria-label="' +
      (isManaging ? 'Close ' : 'Manage ') +
      escapeHtml(title) +
      ' in Command view">' +
      (isManaging ? '×' : '▸') +
      '</button>' +
      '</div>' +
      '</div>' +
      (shouldShowBody ? '<div class="ws-cmd-panel-body">' + body + '</div>' : '') +
      '</section>'
    );
  }

  railItems(list, labelOf, opts) {
    const arr = Array.isArray(list) ? list : [];
    const attr = opts || {};
    const limit = attr.expanded ? arr.length : 5;
    const shown = arr.slice(0, limit);
    const items = shown.map(it => {
      const label = escapeHtml(labelOf(it));
      const meta = attr.metaOf ? escapeHtml(attr.metaOf(it)) : '';
      const inner =
        '<span class="ws-cmd-rail-t">' +
        label +
        '</span>' +
        (meta ? '<span class="ws-cmd-rail-m">' + meta + '</span>' : '');
      if (attr.href) {
        return (
          '<a class="ws-cmd-rail-item" href="' + escapeHtml(attr.href(it)) + '">' + inner + '</a>'
        );
      }
      if (attr.action) {
        return (
          '<button type="button" class="ws-cmd-rail-item" ' +
          attr.action(it) +
          '>' +
          inner +
          '</button>'
        );
      }
      return '<div class="ws-cmd-rail-item is-static">' + inner + '</div>';
    });
    if (arr.length > shown.length) {
      items.push(
        '<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="' +
          escapeHtml(attr.sectionKey || '') +
          '">+ ' +
          (arr.length - shown.length) +
          ' more</button>'
      );
    }
    return items;
  }

  getWorkspaceProjectPath() {
    const page = this.page || {};
    if (typeof page.getWorkspaceProjectPath === 'function') {
      try {
        return String(page.getWorkspaceProjectPath() || '').trim();
      } catch (err) {
        return '';
      }
    }
    return String((page.workspace && page.workspace.project_path) || '').trim();
  }

  folderDisplayName(path) {
    const normalized = String(path || '').replace(/[\\/]+$/, '');
    const parts = normalized.split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] || normalized || 'Project Folder';
  }

  folderRowData() {
    const page = this.page || {};
    const dirs = Array.isArray(page.directories) ? page.directories : [];
    if (dirs.length) return dirs;
    const projectPath = this.getWorkspaceProjectPath();
    if (!projectPath) return [];
    return [
      {
        id: '__project_path__',
        name: this.folderDisplayName(projectPath),
        path: projectPath,
        source: 'project_path',
        isProjectPathOnly: true
      }
    ];
  }

  folderRole(dir) {
    const page = this.page || {};
    if (dir && dir.isProjectPathOnly) return { label: 'Project Folder', className: 'is-project' };
    let primaryDirectoryId = '';
    try {
      primaryDirectoryId =
        typeof page.getPrimaryDirectoryId === 'function'
          ? String(page.getPrimaryDirectoryId() || '')
          : '';
    } catch (err) {
      primaryDirectoryId = '';
    }
    let isProject = false;
    try {
      isProject =
        typeof page.isProjectDirectory === 'function'
          ? Boolean(page.isProjectDirectory(dir))
          : false;
    } catch (err) {
      isProject = false;
    }
    if (isProject || (dir && primaryDirectoryId && String(dir.id || '') === primaryDirectoryId)) {
      return { label: 'Project Folder', className: 'is-project' };
    }
    return { label: 'Reference', className: 'is-reference' };
  }

  folderRailItems(rows, expanded) {
    const arr = Array.isArray(rows) ? rows : [];
    const limit = expanded ? arr.length : 5;
    const shown = arr.slice(0, limit);
    const items = shown.map(dir => {
      const id = String(dir.id || '');
      const role = this.folderRole(dir);
      const name = dir.title || dir.name || dir.path || 'Unnamed Directory';
      const path = String(dir.path || '');
      const source = String(dir.source || 'reference');
      const inner =
        '<span class="ws-cmd-rail-line"><span class="ws-cmd-rail-t">' +
        escapeHtml(name) +
        '</span>' +
        '<span class="ws-cmd-rail-role ' +
        escapeHtml(role.className) +
        '">' +
        escapeHtml(role.label) +
        '</span></span>' +
        (path ? '<span class="ws-cmd-rail-m">' + escapeHtml(path) + '</span>' : '');
      if (dir.isProjectPathOnly) {
        return '<div class="ws-cmd-rail-item is-static">' + inner + '</div>';
      }
      return (
        '<button type="button" class="ws-cmd-rail-item" data-cmd-open-section="folders" data-cmd-item-id="' +
        escapeHtml(id) +
        '" data-cmd-item-source="' +
        escapeHtml(source) +
        '">' +
        inner +
        '</button>'
      );
    });
    if (arr.length > shown.length) {
      items.push(
        '<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="folders">+ ' +
          (arr.length - shown.length) +
          ' more</button>'
      );
    }
    return items;
  }

  fileRowData() {
    const page = this.page || {};
    return Array.isArray(page.files) ? page.files : [];
  }

  fileTitle(file) {
    return String(file?.title || file?.file_meta?.name || file?.name || 'Untitled File');
  }

  formatFileDate(value) {
    const raw = String(value || '').trim();
    if (!raw) return '';
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
  }

  fileMeta(file) {
    const page = this.page || {};
    const parts = [];
    const size = Number(file?.file_meta?.size || file?.size || 0);
    if (size > 0 && typeof page.formatFileSize === 'function') {
      try {
        parts.push(page.formatFileSize(size));
      } catch (err) {
        /* keep going */
      }
    }
    const folder = String(file?.file_meta?.relative_path || file?.relative_path || '').trim();
    if (folder) parts.push(folder);
    const when = this.formatFileDate(file?.created_at || file?.updated_at);
    if (when) parts.push(when);
    if (file?.file_meta?.status === 'missing') parts.unshift('Missing');
    return parts.join(' · ');
  }

  fileRailItems(files, expanded) {
    const arr = Array.isArray(files) ? files : [];
    const limit = expanded ? arr.length : 5;
    const shown = arr.slice(0, limit);
    const items = shown.map(file => {
      const id = String(file?.id || file?.file_meta?.name || this.fileTitle(file));
      const title = this.fileTitle(file);
      const meta = this.fileMeta(file);
      const missingClass = file?.file_meta?.status === 'missing' ? ' is-missing' : '';
      return (
        '<button type="button" class="ws-cmd-rail-item' +
        missingClass +
        '" data-cmd-open-section="files" data-cmd-item-id="' +
        escapeHtml(id) +
        '">' +
        '<span class="ws-cmd-rail-t">' +
        escapeHtml(title) +
        '</span>' +
        (meta ? '<span class="ws-cmd-rail-m">' + escapeHtml(meta) + '</span>' : '') +
        '</button>'
      );
    });
    if (arr.length > shown.length) {
      items.push(
        '<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="files">+ ' +
          (arr.length - shown.length) +
          ' more</button>'
      );
    }
    return items;
  }

  renderFilesPanel(files, expanded) {
    const items = this.fileRailItems(files, expanded);
    const count = Array.isArray(files) ? files.length : 0;
    const bodyRows = items.length
      ? items.join('')
      : '<div class="ws-cmd-rail-empty">No files yet.</div>';
    return (
      '<section class="ws-cmd-panel ws-cmd-files-panel' +
      (expanded ? ' is-managing' : '') +
      (count ? '' : ' is-empty') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Files</h4><span class="ws-cmd-panel-count">' +
      count +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="files">Upload</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="files" aria-expanded="' +
      (expanded ? 'true' : 'false') +
      '" title="' +
      (expanded ? 'Close Files manager' : 'Manage Files in Command view') +
      '" aria-label="' +
      (expanded ? 'Close Files manager' : 'Manage Files in Command view') +
      '">' +
      (expanded ? '×' : '▸') +
      '</button>' +
      '</div>' +
      '</div>' +
      '<div class="ws-cmd-panel-body">' +
      (expanded
        ? '<button type="button" class="ws-cmd-files-drop" data-cmd-file-drop>Drop files here or click Upload</button>' +
          '<button type="button" class="ws-cmd-files-browse" data-cmd-open-section="files" data-cmd-item-id="__workspace_files__">Browse workspace files</button>'
        : '') +
      bodyRows +
      '</div>' +
      '</section>'
    );
  }

  systemTabs() {
    // Capability providers (MCP/Skills/Plugins) + Find Tools now live in the header Tools
    // stat-box manager; Manager Settings → entry-agent modal; Goal Settings → "Set Goal";
    // Intent & Setup → header description edit. Systems keeps only workspace state/automation.
    return [
      {
        key: 'memory',
        label: 'Memory',
        tabId: 'workspace-detail-config-memory-tab',
        host: 'config'
      },
      {
        key: 'triggers',
        label: 'Triggers',
        tabId: 'workspace-detail-config-triggers-tab',
        host: 'config'
      }
    ];
  }

  // Sub-tabs of the header Tools stat-box modal: the three capability providers plus the
  // Find Tools discovery surface. MCP/Skills/Plugins mount the config panel; Find Tools
  // mounts the tools card.
  toolsTabs() {
    return [
      { key: 'mcp', label: 'MCP', tabId: 'workspace-detail-config-mcp-tab', host: 'config' },
      {
        key: 'skills',
        label: 'Skills',
        tabId: 'workspace-detail-config-skills-tab',
        host: 'config'
      },
      {
        key: 'plugins',
        label: 'Plugins',
        tabId: 'workspace-detail-config-plugins-tab',
        host: 'config'
      },
      { key: 'find', label: 'Find Tools', tabId: '', host: 'tools' }
    ];
  }

  toolsTab(key) {
    const normalized = String(key || '').trim();
    return this.toolsTabs().find(tab => tab.key === normalized) || this.toolsTabs()[0];
  }

  // Config-pane tab ids for surfaces that are reached from outside the Systems rail
  // (header stat-box MCP/Skills managers, the entry-agent Manager Settings modal, and
  // the header intent editor). Kept separate from systemTabs() on purpose.
  configTabIdFor(key) {
    switch (String(key || '')) {
      case 'mcp':
        return 'workspace-detail-config-mcp-tab';
      case 'skills':
        return 'workspace-detail-config-skills-tab';
      case 'plugins':
        return 'workspace-detail-config-plugins-tab';
      case 'settings':
        return 'workspace-detail-config-settings-tab';
      case 'mission':
        return 'workspace-detail-config-mission-tab';
      case 'intent':
        return 'workspace-detail-config-intent-tab';
      default:
        return '';
    }
  }

  systemTab(key) {
    const normalized = String(key || '').trim();
    return this.systemTabs().find(tab => tab.key === normalized) || this.systemTabs()[0];
  }

  renderSystemsPanel(expanded) {
    const tabs = this.systemTabs();
    const active = this.systemTab(this.activeSystemTab);
    const tabButtons = tabs
      .map(
        tab =>
          '<button type="button" class="ws-cmd-system-tab' +
          (tab.key === active.key ? ' is-active' : '') +
          '" data-cmd-system-tab="' +
          escapeHtml(tab.key) +
          '" aria-selected="' +
          (tab.key === active.key ? 'true' : 'false') +
          '">' +
          escapeHtml(tab.label) +
          '</button>'
      )
      .join('');
    return (
      '<section class="ws-cmd-panel ws-cmd-systems-panel' +
      (expanded ? ' is-managing' : '') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Systems</h4><span class="ws-cmd-panel-count">' +
      tabs.length +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="systems">Open</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="systems" aria-expanded="' +
      (expanded ? 'true' : 'false') +
      '" title="' +
      (expanded ? 'Close Systems manager' : 'Manage Systems in Command view') +
      '" aria-label="' +
      (expanded ? 'Close Systems manager' : 'Manage Systems in Command view') +
      '">' +
      (expanded ? '×' : '▸') +
      '</button>' +
      '</div>' +
      '</div>' +
      (expanded
        ? '<div class="ws-cmd-panel-body ws-cmd-systems-body">' +
          '<div class="ws-cmd-system-tabs" role="tablist" aria-label="Workspace systems">' +
          tabButtons +
          '</div>' +
          '<div class="ws-cmd-system-host" data-cmd-system-host>' +
          '<div class="ws-cmd-rail-empty">Loading ' +
          escapeHtml(active.label) +
          '...</div>' +
          '</div>' +
          '</div>'
        : '') +
      '</section>'
    );
  }

  detachmentMemberCount() {
    const panel = this.page && this.page.membersPanel;
    const group = panel && panel.group;
    if (!group || !Array.isArray(group.children)) return 0;
    return group.children.length;
  }

  renderDetachmentPanel(expanded) {
    if (!this.isGroupWorkspace()) return '';
    const count = this.detachmentMemberCount();
    return (
      '<section class="ws-cmd-panel ws-cmd-detachment-panel' +
      (expanded ? ' is-managing' : '') +
      (count ? '' : ' is-empty') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Detachment</h4><span class="ws-cmd-panel-count">' +
      count +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="members">Add Member</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="members" aria-expanded="' +
      (expanded ? 'true' : 'false') +
      '" title="' +
      (expanded ? 'Close Detachment manager' : 'Manage members in Command view') +
      '" aria-label="' +
      (expanded ? 'Close Detachment manager' : 'Manage members in Command view') +
      '">' +
      (expanded ? '×' : '▸') +
      '</button>' +
      '</div>' +
      '</div>' +
      (expanded
        ? '<div class="ws-cmd-panel-body ws-cmd-detachment-body">' +
          '<div class="ws-cmd-members-host" data-cmd-members-host>' +
          '<div class="ws-cmd-rail-empty">Loading detachment...</div>' +
          '</div>' +
          '</div>'
        : '') +
      '</section>'
    );
  }

  // ---------- notes panel (tag filter + multi-select) ----------

  tagFilterHelper() {
    return (typeof window !== 'undefined' && window.OriTagFilterBar) || null;
  }

  noteFilterActiveTags() {
    const bar = this.noteFilterBar;
    return bar && typeof bar.getActiveTags === 'function' ? bar.getActiveTags() : [];
  }

  visibleNotes(notes) {
    const all = Array.isArray(notes) ? notes : [];
    const active = this.noteFilterActiveTags();
    const helper = this.tagFilterHelper();
    if (!active.length || !helper || typeof helper.filterItems !== 'function') return all;
    return helper.filterItems(all, active);
  }

  isNoteSelected(id) {
    const set = this.page && this.page.selectedNoteIds;
    return set && typeof set.has === 'function' ? set.has(String(id)) : false;
  }

  noteRowsHTML(list, expanded, total) {
    const arr = Array.isArray(list) ? list : [];
    const limit = expanded ? arr.length : 5;
    const shown = arr.slice(0, limit);
    const rows = shown.map(note => {
      const id = String(note.id || '');
      const label = escapeHtml(note.name || note.title || 'Untitled Note');
      const checkbox = expanded
        ? '<input type="checkbox" class="ws-cmd-note-check" data-cmd-note-select="' +
          escapeHtml(id) +
          '"' +
          (this.isNoteSelected(id) ? ' checked' : '') +
          ' aria-label="Select ' +
          label +
          '">'
        : '';
      return (
        '<div class="ws-cmd-rail-item ws-cmd-note-row">' +
        checkbox +
        '<button type="button" class="ws-cmd-note-open" data-cmd-open-section="notes" data-cmd-item-id="' +
        escapeHtml(id) +
        '"><span class="ws-cmd-rail-t">' +
        label +
        '</span></button>' +
        '</div>'
      );
    });
    if (arr.length > shown.length) {
      rows.push(
        '<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="notes">+ ' +
          (arr.length - shown.length) +
          ' more</button>'
      );
    }
    if (expanded && !arr.length) {
      rows.push(
        '<div class="ws-cmd-rail-empty">' +
          (total ? 'No notes match the active tag filter.' : 'No notes yet.') +
          '</div>'
      );
    }
    return rows;
  }

  noteMultiSelectToolbarHTML() {
    return (
      '<div class="ws-cmd-note-tools" role="group" aria-label="Note bulk actions">' +
      '<button type="button" class="ws-cmd-note-tool" data-cmd-note-action="select-all">Select all</button>' +
      '<button type="button" class="ws-cmd-note-tool" data-cmd-note-action="copy">Copy</button>' +
      '<button type="button" class="ws-cmd-note-tool is-danger" data-cmd-note-action="delete">Delete</button>' +
      '<a class="ws-cmd-note-tool" href="' +
      escapeHtml(this.workspaceRoute('/notes')) +
      '">View all</a>' +
      '</div>'
    );
  }

  renderNotesPanel(notes, expanded) {
    const all = Array.isArray(notes) ? notes : [];
    const visible = this.visibleNotes(all);
    const count = all.length;
    const rows = this.noteRowsHTML(visible, expanded, count).join('');
    const hasBody = expanded || visible.length > 0;
    const body = expanded
      ? '<div class="ws-cmd-note-filter" data-cmd-note-filter></div>' +
        this.noteMultiSelectToolbarHTML() +
        rows
      : visible.length
        ? rows
        : '<div class="ws-cmd-rail-empty">No notes yet.</div>';
    return (
      '<section class="ws-cmd-panel ws-cmd-notes-panel' +
      (expanded ? ' is-managing' : '') +
      (count ? '' : ' is-empty') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Notes</h4><span class="ws-cmd-panel-count">' +
      count +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="notes">New Note</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="notes" aria-expanded="' +
      (expanded ? 'true' : 'false') +
      '" title="' +
      (expanded ? 'Close Notes manager' : 'Manage Notes in Command view') +
      '" aria-label="' +
      (expanded ? 'Close Notes manager' : 'Manage Notes in Command view') +
      '">' +
      (expanded ? '×' : '▸') +
      '</button>' +
      '</div>' +
      '</div>' +
      (hasBody ? '<div class="ws-cmd-panel-body">' + body + '</div>' : '') +
      '</section>'
    );
  }

  ensureNoteFilterBar() {
    if (this.noteFilterBar) return this.noteFilterBar;
    const helper = this.tagFilterHelper();
    if (!helper || typeof helper.createTagFilterBar !== 'function') return null;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
    const holder = document.createElement('div');
    this.noteFilterBar = helper.createTagFilterBar({
      container: holder,
      label: 'Tags',
      onChange: () => this.render()
    });
    return this.noteFilterBar;
  }

  mountNoteFilterBar() {
    if (!this.container) return;
    const host = this.container.querySelector('[data-cmd-note-filter]');
    if (!host) return;
    const bar = this.ensureNoteFilterBar();
    if (!bar || !bar.element) return;
    const notes = Array.isArray(this.page && this.page.notes) ? this.page.notes : [];
    const helper = this.tagFilterHelper();
    if (
      helper &&
      typeof helper.collectTags === 'function' &&
      typeof bar.setAvailableTags === 'function'
    ) {
      bar.setAvailableTags(helper.collectTags(notes));
    }
    host.innerHTML = '';
    host.appendChild(bar.element);
  }

  renderRail() {
    const page = this.page || {};
    const notes = Array.isArray(page.notes) ? page.notes : [];
    const schedules = Array.isArray(page.schedules) ? page.schedules : [];
    const sessions = Array.isArray(page.sessions) ? page.sessions : [];
    const dirs = this.folderRowData();
    const files = this.fileRowData();

    const notesExpanded = this.activeRailSection === 'notes';
    const schedulesExpanded = this.activeRailSection === 'schedules';
    const sessionsExpanded = this.activeRailSection === 'sessions';
    const foldersExpanded = this.activeRailSection === 'folders';
    const filesExpanded = this.activeRailSection === 'files';
    const systemsExpanded = this.activeRailSection === 'systems';
    const detachmentExpanded = this.activeRailSection === 'members';

    const scheduleItems = this.railItems(
      schedules,
      s => s.name || s.task_description || 'Unnamed Schedule',
      {
        sectionKey: 'schedules',
        expanded: schedulesExpanded,
        action: s =>
          'data-cmd-open-section="schedules" data-cmd-item-id="' +
          escapeHtml(String(s.id || '')) +
          '"'
      }
    );
    const sessionItems = this.railItems(sessions, s => s.title || s.name || 'Untitled Session', {
      sectionKey: 'sessions',
      expanded: sessionsExpanded,
      action: s =>
        'data-cmd-open-section="sessions" data-cmd-item-id="' +
        escapeHtml(String(s.id || '')) +
        '"',
      metaOf: s => s.agent_name || ''
    });
    const folderItems = this.folderRailItems(dirs, foldersExpanded);

    return (
      this.renderNotesPanel(notes, notesExpanded) +
      this.railPanelHTML(
        'schedules',
        'Schedules',
        scheduleItems,
        schedules.length,
        'No schedules yet.',
        'Open Schedules'
      ) +
      this.railPanelHTML(
        'sessions',
        'Sessions',
        sessionItems,
        sessions.length,
        'No sessions yet.',
        'New Session'
      ) +
      this.railPanelHTML(
        'folders',
        'Linked Folders',
        folderItems,
        dirs.length,
        'No linked folders yet.',
        'Link Folder'
      ) +
      this.renderDetachmentPanel(detachmentExpanded) +
      this.renderFilesPanel(files, filesExpanded) +
      this.renderSystemsPanel(systemsExpanded)
    );
  }

  normalizeSystemTab(key) {
    return this.systemTab(key).key;
  }

  openSystemTab(key = 'memory') {
    this.activeRailSection = 'systems';
    this.activeSystemTab = this.normalizeSystemTab(key);
    this.render();
  }

  ensureSharedSurfaceAnchor(key, selector) {
    if (!this.sharedSurfaceAnchors) this.sharedSurfaceAnchors = {};
    const existing = this.sharedSurfaceAnchors[key];
    if (existing && existing.node) return existing;
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function')
      return null;
    const node = document.querySelector ? document.querySelector(selector) : null;
    if (!node || !node.parentNode) return null;
    const anchor =
      typeof document.createComment === 'function'
        ? document.createComment('workspace-command-' + key + '-anchor')
        : null;
    if (anchor && node.parentNode) {
      node.parentNode.insertBefore(anchor, node);
    }
    const record = { node, anchor, parent: node.parentNode };
    this.sharedSurfaceAnchors[key] = record;
    return record;
  }

  mountSharedSurface(key, selector, host) {
    const record = this.ensureSharedSurfaceAnchor(key, selector);
    if (!record || !record.node || !host || typeof host.appendChild !== 'function') return null;
    host.innerHTML = '';
    host.appendChild(record.node);
    record.node.hidden = false;
    return record.node;
  }

  restoreSharedSurface(key) {
    const record = this.sharedSurfaceAnchors && this.sharedSurfaceAnchors[key];
    if (!record || !record.node) return;
    const parent =
      record.anchor && record.anchor.parentNode ? record.anchor.parentNode : record.parent;
    if (!parent || typeof parent.insertBefore !== 'function') return;
    if (record.node.parentNode === parent) return;
    parent.insertBefore(record.node, record.anchor ? record.anchor.nextSibling : null);
  }

  restoreSharedSurfaces() {
    this.restoreSharedSurface('config');
    this.restoreSharedSurface('tools');
    this.restoreSharedSurface('members');
    this.restoreSharedSurface('board');
  }

  showConfigTab(tab) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (page && typeof page.setWorkspaceConfigExpanded === 'function') {
      page.setWorkspaceConfigExpanded(true);
    }
    if (!tab || !tab.tabId) return;
    const tabBtn = typeof document !== 'undefined' ? document.getElementById(tab.tabId) : null;
    if (!tabBtn) return;

    let usedBootstrap = false;
    if (
      typeof window !== 'undefined' &&
      window.bootstrap &&
      window.bootstrap.Tab &&
      typeof window.bootstrap.Tab.getOrCreateInstance === 'function'
    ) {
      window.bootstrap.Tab.getOrCreateInstance(tabBtn).show();
      usedBootstrap = true;
    } else if (page && typeof page.activateWorkspaceConfigTab === 'function') {
      page.activateWorkspaceConfigTab(tab.tabId);
      usedBootstrap = true;
    } else if (typeof tabBtn.click === 'function') {
      tabBtn.click();
    }

    if (
      !usedBootstrap &&
      typeof Event === 'function' &&
      typeof tabBtn.dispatchEvent === 'function'
    ) {
      tabBtn.dispatchEvent(new Event('shown.bs.tab', { bubbles: true }));
    }
  }

  expandMountedConfig(configNode) {
    if (!configNode || typeof configNode.querySelector !== 'function') return;
    configNode.classList?.remove('is-collapsed');
    const content = configNode.querySelector('#workspace-detail-config-content');
    if (content) {
      content.hidden = false;
      if (typeof content.removeAttribute === 'function') content.removeAttribute('hidden');
    }
    const toggle = configNode.querySelector('#workspace-detail-config-toggle');
    if (toggle && typeof toggle.setAttribute === 'function') {
      toggle.setAttribute('aria-expanded', 'true');
    }
    const label = configNode.querySelector('#workspace-detail-config-toggle-label');
    if (label) label.textContent = 'Hide Configuration';
  }

  refreshSystemTabData(tab) {
    if (!tab) return;
    this.refreshConfigData(tab.key);
  }

  // Re-run a config pane's data load. Shared by the Systems rail (Plugins/Memory/Triggers)
  // and by the relocated surfaces that mount the same config node into a modal
  // (mcp/skills stat boxes, entry-agent settings), so there is one source of truth.
  refreshConfigData(key) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page) return;
    switch (String(key || '')) {
      case 'mcp':
        if (typeof page.renderWorkspaceMCPBindings === 'function')
          page.renderWorkspaceMCPBindings();
        if (page.nativeMCPManager && typeof page.nativeMCPManager.load === 'function') {
          page.nativeMCPManager.load();
        }
        break;
      case 'skills':
        if (typeof page.renderWorkspaceSkillBindings === 'function')
          page.renderWorkspaceSkillBindings();
        break;
      case 'plugins':
        if (typeof page.renderWorkspacePluginBindings === 'function')
          page.renderWorkspacePluginBindings();
        break;
      case 'memory':
        if (page.memoryManager && typeof page.memoryManager.load === 'function')
          page.memoryManager.load();
        break;
      case 'settings':
        if (typeof page.renderWorkspaceSettings === 'function') page.renderWorkspaceSettings();
        break;
      case 'intent':
        if (typeof page.renderWorkspaceIntent === 'function') page.renderWorkspaceIntent();
        break;
      default:
        break;
    }
  }

  syncSharedSurfaces() {
    this.syncSystemsSurface();
    this.syncDetachmentSurface();
  }

  syncSystemsSurface() {
    // The config/tools nodes may be on loan to an open stat modal (MCP/Skills/Manager
    // Settings or the Tools modal) — don't steal them back mid-view on a background
    // re-render (single-node arbitration, PRD FR6.26).
    const modalHoldsSurface =
      this.statModalHoldsSharedSurface() && this.statModalEl && !this.statModalEl.hidden;
    if (modalHoldsSurface) return;

    const host = this.container && this.container.querySelector('[data-cmd-system-host]');
    if (!this.active || this.activeRailSection !== 'systems' || !host) {
      this.restoreSharedSurface('config');
      this.restoreSharedSurface('tools');
      return;
    }

    const tab = this.systemTab(this.activeSystemTab);
    if (tab.host === 'tools') {
      this.restoreSharedSurface('config');
      const tools = this.mountSharedSurface('tools', '#workspace-detail-tools-card', host);
      if (!tools)
        host.innerHTML = '<div class="ws-cmd-rail-empty">Find Tools is unavailable.</div>';
      return;
    }

    this.restoreSharedSurface('tools');
    const config = this.mountSharedSurface('config', '#workspace-detail-settings-panel', host);
    if (!config) {
      host.innerHTML =
        '<div class="ws-cmd-rail-empty">Workspace configuration is unavailable.</div>';
      return;
    }
    // Shed any modal-only chrome hiding if this node was last used inside a stat modal.
    if (config.classList) config.classList.remove('is-command-modal');
    this.showConfigTab(tab);
    this.refreshSystemTabData(tab);
    this.expandMountedConfig(config);
  }

  async syncDetachmentSurface() {
    if (!this.active || this.activeRailSection !== 'members') {
      this.restoreSharedSurface('members');
      return;
    }
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (page && page.membersPanel && typeof page.membersPanel.syncWorkspace === 'function') {
      try {
        await page.membersPanel.syncWorkspace(page.workspace);
      } catch (err) {
        /* keep going */
      }
    }
    // Re-query after the await: a later render may have replaced the host.
    const host = this.container && this.container.querySelector('[data-cmd-members-host]');
    if (!host) return;
    const members = this.mountSharedSurface('members', '#workspace-detail-members-panel', host);
    if (!members) {
      host.innerHTML = '<div class="ws-cmd-rail-empty">Members are unavailable.</div>';
      return;
    }
    // The Detailed panel-expansion machinery that lazily loaded rollups is
    // gone; the mounted panel is always "expanded" here, so load directly.
    if (page && page.membersPanel && typeof page.membersPanel.loadRollups === 'function') {
      try {
        page.membersPanel.rollupsLoaded = true;
        void page.membersPanel.loadRollups();
      } catch (err) {
        /* keep going */
      }
    }
  }

  setFileDropActive(dropZone, isActive) {
    if (!dropZone || !dropZone.classList) return;
    dropZone.classList.toggle('is-active', Boolean(isActive));
  }

  async uploadDroppedFiles(event) {
    if (!event || !event.dataTransfer || !event.dataTransfer.files) return;
    const files = event.dataTransfer.files;
    if (!files.length) return;
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.uploadFiles !== 'function') return;
    await page.uploadFiles(files);
  }

  bindRail() {
    const root = this.container && this.container.querySelector('.ws-cmd-rail');
    if (!root) return;
    root.addEventListener('click', event => {
      const systemTab = event.target.closest('[data-cmd-system-tab]');
      if (systemTab) {
        this.openSystemTab(systemTab.getAttribute('data-cmd-system-tab'));
        return;
      }
      const fileDrop = event.target.closest('[data-cmd-file-drop]');
      if (fileDrop) {
        this.runRailPrimaryAction('files', fileDrop);
        return;
      }
      const primaryBtn = event.target.closest('[data-cmd-primary-section]');
      if (primaryBtn) {
        this.runRailPrimaryAction(primaryBtn.getAttribute('data-cmd-primary-section'), primaryBtn);
        return;
      }
      const manageBtn = event.target.closest('[data-cmd-manage-section]');
      if (manageBtn) {
        this.toggleRailManager(manageBtn.getAttribute('data-cmd-manage-section'));
        return;
      }
      const noteAction = event.target.closest('[data-cmd-note-action]');
      if (noteAction) {
        this.handleNoteAction(noteAction.getAttribute('data-cmd-note-action'));
        return;
      }
      const itemBtn = event.target.closest('[data-cmd-open-section][data-cmd-item-id]');
      if (itemBtn) {
        this.openRailItem(
          itemBtn.getAttribute('data-cmd-open-section'),
          itemBtn.getAttribute('data-cmd-item-id'),
          itemBtn.getAttribute('data-cmd-item-source')
        );
      }
    });
    root.addEventListener('change', event => {
      const cb = event.target.closest('[data-cmd-note-select]');
      if (!cb) return;
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
      if (page && typeof page.toggleNoteSelection === 'function') {
        page.toggleNoteSelection(cb.getAttribute('data-cmd-note-select'), cb.checked);
      }
    });
    root.addEventListener('dragover', event => {
      const dropZone = event.target.closest('[data-cmd-file-drop]');
      if (!dropZone) return;
      event.preventDefault();
      this.setFileDropActive(dropZone, true);
    });
    root.addEventListener('dragleave', event => {
      const dropZone = event.target.closest('[data-cmd-file-drop]');
      if (!dropZone) return;
      this.setFileDropActive(dropZone, false);
    });
    root.addEventListener('drop', event => {
      const dropZone = event.target.closest('[data-cmd-file-drop]');
      if (!dropZone) return;
      event.preventDefault();
      event.stopPropagation();
      this.setFileDropActive(dropZone, false);
      this.uploadDroppedFiles(event);
    });
  }
}
