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
import { workspacePageURL, workspaceRootURL } from './workspace-routes.js';
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
// Tickets is a first-class view, not a panel inside one
// (tasks/prd-workspace-ticket-management.md FR-65): it is the destination
// where durable work is managed, so it sits beside Details and Map rather than
// one click deeper inside a modal.
const COMMAND_VIEW_MODES = ['details', 'map', 'tickets'];

function escapeHtml(value) {
  return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

function renderCapabilityMarkdown(value) {
  const markdown = String(value || '');
  if (!markdown) return '';
  try {
    if (
      typeof window !== 'undefined' &&
      window.marked &&
      typeof window.marked.parse === 'function' &&
      window.DOMPurify &&
      typeof window.DOMPurify.sanitize === 'function'
    ) {
      return window.DOMPurify.sanitize(window.marked.parse(markdown, { breaks: true, gfm: true }));
    }
  } catch (error) {
    console.error('Capability Markdown render failed:', error);
  }
  return '<pre>' + escapeHtml(markdown) + '</pre>';
}

// Clamp an HQ station's fractional map coordinate into [0,1]. Non-finite input
// (a corrupt saved value) collapses to 0 so a station can never render or
// persist off-field (FR11/FR13).
function clampFraction(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0;
  return Math.min(1, Math.max(0, n));
}

// Formats a Calendar Ops portal event's start_time as a short clock label
// ("2:30 PM") for the HQ station/panel. Returns '' for a missing/unparsable
// value rather than throwing.
function calendarOpsMeetingTimeLabel(evt) {
  const start = Date.parse(String((evt && evt.start_time) || ''));
  if (!Number.isFinite(start)) return '';
  try {
    return new Date(start).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  } catch (_) {
    return '';
  }
}

// Pointer travel (px) from the press origin past which a press on a station
// becomes a drag rather than a click (FR9).
const STATION_DRAG_THRESHOLD_PX = 5;

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
    this._urlBootState =
      typeof window !== 'undefined' ? parseWorkspaceURLState(window.location.search) : null;
    this._urlStateApplied = false;
    this._urlSyncEnabled = false;
    this._lastSyncedURLState = null;
    this.viewMode = resolveEffectiveMode(
      this._urlBootState && this._urlBootState.mode,
      this.readCommandViewModePreference()
    );
    this.activeRailSection = '';
    // Whether the Tickets view has fetched since it was last entered. Reset on
    // leaving, so re-entry always shows current data without every unrelated
    // re-render re-fetching. See syncTicketsView.
    this.ticketsViewLoaded = false;
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
    // Persistent, non-modal Backlog drawer (PRD workspace-backlog FR30, 35-37)
    // — the shared full interaction surface opened from both the Details rail
    // panel and the Map Quest Board (group 5). Backlog is presented
    // separately from Tasks: capture stays non-executable until an explicit
    // Promote to Ready action (FR19, 39).
    this.backlogDrawerOpen = false;
    this.backlogDrawerEl = null;
    this.backlogDrawerTrigger = null;
    this.backlogDrawerSelectedId = '';
    this.backlogQuickCaptureOpen = false;
    this.backlogQuickCaptureDraft = '';
    this.backlogQuickCaptureDetailsDraft = '';
    this.backlogQuickCaptureError = '';
    this.backlogQuickCaptureSubmitting = false;
    this.backlogPromoteConfirmId = '';
    this.backlogPromoteBusy = false;
    // Map Quest Board "Accept Quest" (group 5) — id of the item currently
    // being promoted directly from the Map window, separate from the
    // drawer's own confirm/busy state above.
    this.mapAcceptQuestBusyId = '';
    // Post-creation supported-field editing (FR20: details/tags/priority/
    // reference URL may be added or edited before or after creation, not
    // only at quick-capture time).
    this.backlogEditItemId = '';
    this.backlogEditDraft = null;
    this.backlogEditError = '';
    this.backlogEditSubmitting = false;
    this._backlogDrawerAnnounce = '';
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
    // Composer commitment choice (FR56): 'ready' creates a direct, unassigned
    // Ready task (existing default behavior); 'backlog' routes through the
    // same createBacklogItem() the Backlog panel/drawer use. Resets to
    // 'ready' each time the composer opens (openTaskComposer).
    this.taskComposerIntent = 'ready';
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
    this.loadoutAddOpen = ''; // '' | 'skill' | 'mcp' — which Add modal is open
    this.loadoutAddAgent = '';
    this.loadoutAddOptions = [];
    this.loadoutAddLoading = false;
    this.loadoutAddRequestId = 0;
    this.pendingLoadoutAddFocus = '';
    this.loadoutBusyKey = ''; // "<kind>:<bindingId>" or "<kind>:add:<name>" while mutating
    this.loadoutError = '';
    this.capabilityInspector = this.emptyCapabilityInspectorState();
    this.capabilityInspectorRequestId = 0;
    this.pendingCapabilityInspectorFocus = '';
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
    if (nextMode !== 'map' && this.capabilityInspector?.open) {
      this.resetCapabilityInspector();
    }
    if (this.loadoutAddOpen) this.resetLoadoutPicker();
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
    this.ensureCapabilityStations();
    this.ensureSurfaceStations();
    this.applyBootURLState();
  }

  // Installed capabilities drive Map stations, so the catalog has to be loaded
  // before the map can render one — and re-rendered when an install changes it,
  // in place rather than through a page reload (FR-99).
  ensureCapabilityStations() {
    const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
    if (!catalog || typeof catalog.load !== 'function') return;

    if (!this.boundCapabilitiesChanged) {
      this.boundCapabilitiesChanged = () => {
        if (this.active) this.render();
      };
      if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
        document.addEventListener('ori:capabilities-changed', this.boundCapabilitiesChanged);
      }
    }

    if (this.capabilityCatalogRequested) return;
    this.capabilityCatalogRequested = true;
    void Promise.resolve(catalog.load()).then(() => {
      if (this.active) this.render();
    });
  }

  // Plugin-contributed stations arrive through one generic authenticated
  // catalog. The command view knows no plugin/capability names: it asks the
  // surface host for sanitized station descriptors and re-renders when their
  // catalog changes.
  ensureSurfaceStations() {
    const host = typeof window === 'undefined' ? null : window.WorkspaceSurfaceHost;
    if (!host || typeof host.loadCatalog !== 'function') return;

    if (!this.boundWorkspaceSurfacesChanged) {
      this.boundWorkspaceSurfacesChanged = () => {
        if (this.active) this.render();
      };
      if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
        document.addEventListener(
          'ori:workspace-surfaces-changed',
          this.boundWorkspaceSurfacesChanged
        );
      }
    }

    if (this.workspaceSurfaceCatalogRequested) return;
    this.workspaceSurfaceCatalogRequested = true;
    void Promise.resolve(host.loadCatalog()).then(() => {
      if (this.active) this.render();
    });
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
    const backlogItems = Array.isArray(page.backlogItems) ? page.backlogItems : [];
    const validBacklogIds = backlogItems
      .map(it => String((it && (it.task || it).id) || ''))
      .filter(Boolean);
    let validAgentKeys = [];
    try {
      validAgentKeys = this.agentGroups().agents.map(g => this.normalizeAgentKey(g.key || g.name));
    } catch (_error) {
      validAgentKeys = [];
    }
    return { validTaskIds, validAgentKeys, validRunTaskIds: validTaskIds, validBacklogIds };
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
  /**
   * Switches to the Tickets view when the URL carries `?ticket=<stable id>`
   * (tasks/prd-workspace-ticket-management.md FR-83, FR-84).
   *
   * Tickets is not the default view mode, so a deep link that only told the
   * module which ticket to show would be a silent no-op — the surface it
   * renders into is hidden. Switching the view is what makes the link
   * actually land somewhere.
   *
   * The ticket module itself reads the same parameter and opens detail once
   * its surface is mounted.
   */
  applyTicketDeepLink() {
    if (this._ticketDeepLinkApplied || typeof window === 'undefined') return;
    let params;
    try {
      params = new URLSearchParams(window.location.search);
    } catch {
      return;
    }
    // Either shape opens the destination: `?ticket=<id>` for one ticket,
    // `?tickets=<state>` for a filtered list behind a count.
    if (!params.get('ticket') && !params.get('tickets')) return;

    this._ticketDeepLinkApplied = true;
    this.setCommandViewMode('tickets', { focus: false });
  }

  /**
   * Opens the Tickets destination filtered to one canonical state (FR-65,
   * FR-80, FR-81).
   *
   * This is what a count shortcut does: a panel that shows "Backlog 3" hands
   * the user the three tickets behind the number, in the surface where they
   * are actually managed — rather than being a third place to manage them.
   */
  openTicketsFiltered(state) {
    const tickets = typeof window === 'undefined' ? null : window.WorkspaceHubTickets;
    this.setCommandViewMode('tickets', { focus: false });
    if (!tickets || typeof tickets.setFilterState !== 'function') return;
    tickets.setFilterState(state);
  }

  applyBootURLState() {
    this.applyTicketDeepLink();
    if (this._urlStateApplied || !this._urlBootState) {
      this._urlSyncEnabled = true;
      return;
    }
    const page = this.page || {};
    if (!Array.isArray(page.tasks)) return;

    const context = this.urlStateContext();
    const boot = this._urlBootState;
    const bootWantsBacklogTask = Boolean(boot.task) && boot.panel === 'backlog';
    const bootWantsTaskDrawerTask = Boolean(boot.task) && boot.panel !== 'backlog';
    const dataLooksUnready =
      (bootWantsTaskDrawerTask && context.validTaskIds.length === 0) ||
      (bootWantsBacklogTask && context.validBacklogIds.length === 0) ||
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
    if (state.panel === 'settings') {
      // Held until after render() below: the Manager Settings modal mounts the
      // shared config surface into the container, and opening it here would
      // mount into markup that render() is about to replace.
      this._pendingSettingsPanel = true;
    } else if (state.panel === 'tasks') {
      this.taskDrawerOpen = true;
      if (state.task) this.taskDrawerSelectedId = state.task;
    } else if (state.panel === 'backlog') {
      // Global Workspace Map deep link (FR59): opens the shared Backlog
      // drawer, never mutates/promotes/deletes directly.
      this.backlogDrawerOpen = true;
      if (state.task) this.backlogDrawerSelectedId = state.task;
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
    if (this.backlogDrawerOpen) {
      const el = this.ensureBacklogDrawer();
      if (el) {
        el.hidden = false;
        this.renderBacklogDrawerBody();
      }
    }
    if (this._pendingSettingsPanel) {
      this._pendingSettingsPanel = false;
      // Opened after render() so the shared config surface mounts into the
      // container that actually survives.
      this.openStatModal('settings', null);
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
      // `settings` is last: it names an open modal, so a drawer underneath it
      // still owns the URL. It is reported at all so that the Plans page's
      // "Open Workspace Settings" link survives a reload instead of
      // normalizing itself away the moment it arrives.
      panel: this.taskDrawerOpen
        ? 'tasks'
        : this.backlogDrawerOpen
          ? 'backlog'
          : this.statModalSection === 'settings'
            ? 'settings'
            : '',
      task: this.taskDrawerOpen
        ? this.taskDrawerSelectedId
        : this.backlogDrawerOpen
          ? this.backlogDrawerSelectedId
          : '',
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
    for (const attr of [
      'data-cmd-map-select-agent',
      'data-cmd-drawer-select',
      'data-cmd-open-task-drawer',
      'data-cmd-backlog-select',
      'data-cmd-open-backlog-drawer'
    ]) {
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
    const { state } = sanitizeWorkspaceURLState(
      parseWorkspaceURLState(window.location.search),
      context
    );
    this._lastSyncedURLState = state;

    const effectiveMode = resolveEffectiveMode(state.mode, this.viewMode, this.viewMode);
    if (
      (effectiveMode !== 'map' || (state.agent && state.agent !== this.selectedAgentKey)) &&
      this.capabilityInspector?.open
    ) {
      this.resetCapabilityInspector();
    }
    if (this.loadoutAddOpen) this.resetLoadoutPicker();
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
      if (list && typeof historyState.drawerScroll === 'number')
        list.scrollTop = historyState.drawerScroll;
    }
    if (
      historyState.focusSelector &&
      this.container &&
      typeof this.container.querySelector === 'function'
    ) {
      const target = this.container.querySelector(historyState.focusSelector);
      if (target && typeof target.focus === 'function') target.focus({ preventScroll: true });
    }
  }

  /** A safe, same-origin `Open Full Task` href carrying a validated return target (FR92). */
  taskHrefWithReturn(taskId) {
    const workspaceSlug = this.workspaceSlug();
    const base = workspacePageURL(workspaceSlug, ['task', taskId]);
    const returnTarget = buildReturnTarget(workspaceSlug, this.currentURLState());
    return returnTarget ? base + '?return=' + encodeURIComponent(returnTarget) : base;
  }

  handleGlobalKeydown(event) {
    if (!this.active || !event || event.key !== 'Escape') return;
    if (this.statModalSection || this.identityEditMode) return;
    if (this.backlogDrawerOpen) {
      this.closeBacklogDrawer();
      return;
    }
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
    if (this.loadoutAddOpen) {
      this.closeLoadoutPicker();
      return;
    }
    if (
      this.viewMode === 'map' &&
      this.activeMapWindow === 'inspector' &&
      this.capabilityInspector?.open
    ) {
      this.closeCapabilityInspector();
      return;
    }
    if (this.viewMode === 'map' && this.activeMapWindow) {
      this.resetCapabilityInspector();
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

  workspaceSlug() {
    const page = this.page || {};
    return String(
      page.workspaceSlug || (page.workspace && page.workspace.folder_slug) || page.workspaceId || ''
    ).trim();
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
    const slug = this.workspaceSlug();
    return slug ? workspaceRootURL(slug) + suffix : '#';
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
      { key: 'map', label: 'Map' },
      { key: 'tickets', label: 'Tickets' }
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
      this.plansLinkHTML() +
      '</div>'
    );
  }

  // Plans is a LINK, not a fourth view mode.
  //
  // The other three swap what this page renders. Plans has its own canonical
  // pages — /workspaces/{slug}/plans and .../plans/{planId} — because a plan is
  // reviewed, edited, and approved on exactly one surface, and giving it an
  // in-page mode here would be the second surface this feature exists to
  // prevent (FR-145, FR-148, FR-149).
  //
  // It sits in the same switch so it is where someone looks for a workspace
  // destination, and it is an <a> so middle-click, cmd-click, and "copy link
  // address" all behave the way a link should.
  plansLinkHTML() {
    const workspaceSlug = this.workspaceSlug();
    if (!workspaceSlug) return '';

    return (
      '<a class="ws-cmd-view-btn ws-cmd-view-link" href="' +
      escapeHtml(workspacePageURL(workspaceSlug, ['plans'])) +
      '">' +
      escapeHtml('Plans') +
      '</a>'
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

  // Single source of HQ-ness for the view (FR8): reads the workspace payload's
  // designation field rather than correlating window.OriHQEmailSetup.isHQ,
  // which only reflects one HQ feature (email) and only after it loads.
  isPersonalHQ() {
    const ws = (this.page && this.page.workspace) || {};
    return (
      String(ws.designation || '')
        .trim()
        .toLowerCase() === 'personal_hq'
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
    // Both Command modes now carry HQ surface (Details gains a Stations rail
    // panel), so the Personal HQ badge shows in Map and Details alike (FR15) —
    // never for non-HQ workspaces. Echoes the ws-map-tile-hq-badge treatment
    // from the base map.
    const hqBadge = this.isPersonalHQ()
      ? '<span class="ws-cmd-hq-badge" title="Personal HQ">Personal HQ</span>'
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
      // Back action from a workspace-scoped page points at Home, the canonical
      // workspace overview, and says so (PRD FR11/FR12). It used to say
      // "Workspaces" and rely on the /workspaces compatibility redirect.
      '<a class="ws-cmd-nav-btn" href="/" aria-label="Back to the workspace map on Home">Home</a>' +
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
      hqBadge +
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
      // Tickets is the canonical destination for durable workspace work. The
      // count is loaded asynchronously (see refreshTicketCount) rather than
      // read from page.tasks, because Ticket state is server-authoritative and
      // deriving a count from legacy task status would be the exact inference
      // this feature exists to remove (FR-7, FR-101).
      this.statBoxHTML(
        this.ticketCountLabel(),
        'Tickets',
        'tickets',
        'View tickets: backlog, ready, in progress, review, and done'
      ) +
      '</div>' +
      '</header>'
    );
  }

  /** The Tickets stat value; an em dash until the real count arrives. */
  ticketCountLabel() {
    return typeof this.ticketCount === 'number' ? String(this.ticketCount) : '–';
  }

  /**
   * Fetches the workspace's ticket count and paints it into the stat tile in
   * place. It updates only the tile's value node rather than re-rendering the
   * command view, so an in-flight count can never clobber an open modal or
   * reset the user's scroll position.
   */
  async refreshTicketCount() {
    // While the Tickets view is open, the view itself is the count: it shows
    // "N tickets" from the fetch it just made. Counting again here would mean
    // two list requests per render for one number the user can already read.
    // Leaving the view re-renders, which refreshes the tile.
    if (this.viewMode === 'tickets') return;
    const tickets = typeof window !== 'undefined' ? window.WorkspaceHubTickets : null;
    const studioId = this.workspaceId();
    if (!tickets || !studioId) return;
    // render() runs many times while a workspace page boots as data arrives,
    // and each pass lands here. Without this, one page load issues a dozen
    // identical list requests for a single number. One in flight is enough;
    // the next render paints whatever it resolves to.
    if (this.ticketCountInFlight) return;
    this.ticketCountInFlight = true;
    try {
      const result = await tickets.api.list(studioId);
      this.ticketCount = result.count;
    } catch (_error) {
      // A failed count leaves the placeholder rather than showing a wrong
      // number; the Tickets view surfaces the real error when opened.
      return;
    } finally {
      // Always clear, including on the failure path — a stuck flag would
      // silently retire the count for the rest of the session.
      this.ticketCountInFlight = false;
    }
    if (!this.container || typeof this.container.querySelector !== 'function') return;
    const tile = this.container.querySelector('[data-cmd-section="tickets"] .ws-v');
    if (tile) tile.textContent = this.ticketCountLabel();
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
    const surfaceHost = typeof window === 'undefined' ? null : window.WorkspaceSurfaceHost;
    if (surfaceHost && typeof surfaceHost.setMapVisible === 'function') {
      surfaceHost.setMapVisible(this.active && this.viewMode === 'map');
    }
    if (!this.container) return;
    // An active station drag owns the DOM: render() rebuilds it wholesale,
    // which would tear the dragged element out mid-gesture. Skip background
    // re-renders while dragging; the drop path re-renders once the gesture
    // ends, picking up any data that changed in the meantime (FR11).
    if (this._stationDragActive) return;
    this.rememberCapabilityInspectorFocus();
    this.rememberLoadoutAddFocus();
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

    // Tickets renders NOTHING inside this container. Its surface is real DOM
    // with bound listeners living beside the container, and this container is
    // rebuilt with innerHTML on every render — which is exactly why the Board
    // and config panels have to be relocated in and out. Keeping Tickets
    // outside avoids that dance entirely, and with it the orphaning risk of
    // forgetting to restore a moved node.
    const body =
      this.viewMode === 'tickets'
        ? ''
        : this.viewMode === 'map'
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

    this.container.innerHTML =
      this.commandBarHTML(ws, name, mode, stats) +
      body +
      this.loadoutAddModalHTML(this.loadoutAddAgent);

    this.bindIdentityControls();
    this.bindReadout();
    this.bindMissionPanel();
    if (this.viewMode === 'map') {
      this.bindOperationsMap();
    } else if (this.viewMode !== 'tickets') {
      this.bindGarrison();
      this.bindRail();
    }
    this.bindLoadoutAddModal();
    this.syncTicketsView();
    this.mountCommandTagInput();
    this.syncMissionPanel();
    this.syncSharedSurfaces();
    this.mountNoteFilterBar();
    this.restoreAgentDeckViewState();
    this.restoreCapabilityInspectorFocus();
    this.restoreLoadoutAddFocus();
    this.hydrateActiveAgentPrompt();
    // The Workshop host only exists once the Toolbox tab has rendered, so it is
    // mounted here rather than at page load. Idempotent per instance.
    this.mountToolboxWorkshop();

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

    // Paint the real ticket count over the placeholder. Fire-and-forget: the
    // count is a readout, so a slow or failed fetch must never delay or break
    // the rest of the command view.
    Promise.resolve(this.refreshTicketCount()).catch(() => {});

    // The Backlog drawer survives full re-renders too — same pattern.
    if (this.backlogDrawerEl && this.container && this.container.appendChild) {
      this.container.appendChild(this.backlogDrawerEl);
      if (this.backlogDrawerOpen) {
        this.backlogDrawerEl.hidden = false;
        this.renderBacklogDrawerBody();
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
      const section = sectionBtn.getAttribute('data-cmd-section');
      // Tickets is a view mode, not a stat modal, so its tile switches the
      // view rather than opening a panel over it.
      if (section === 'tickets') {
        this.setCommandViewMode('tickets', { focus: false });
        return;
      }
      this.openStatModal(section, sectionBtn);
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
      case 'watchtower':
        return { title: 'Watchtower', addLabel: '' };
      case 'calendar-ops':
        return { title: 'Calendar Ops', addLabel: '' };
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

  /**
   * Shows or hides the Tickets view.
   *
   * Tickets is a view MODE, not a relocated panel: its surface is real DOM
   * with bound listeners that lives beside the command container and is simply
   * shown or hidden. Nothing is moved, so nothing can be orphaned by a missed
   * restore — the failure this page's shared-surface pattern invites.
   */
  syncTicketsView() {
    if (typeof document === 'undefined') return;
    const surface = document.getElementById('workspace-detail-tickets-surface');
    if (!surface) return;

    const active = this.viewMode === 'tickets';
    surface.hidden = !active;
    if (surface.style) surface.style.display = active ? '' : 'none';
    if (!active) {
      this.ticketsViewLoaded = false;
      return;
    }

    // Load on ENTRY only. This runs from render(), which fires again on every
    // workspace refresh — including the one a ticket transition triggers — so
    // reloading unconditionally turned a single board move into five list
    // fetches, throwing away the user's scroll and filter work each time. The
    // Tickets module refreshes itself after its own mutations; entering the
    // view is the only moment this owns.
    if (this.ticketsViewLoaded) return;
    const tickets = typeof window !== 'undefined' ? window.WorkspaceHubTickets : null;
    if (!tickets) return;
    this.ticketsViewLoaded = true;
    tickets.init();
    Promise.resolve(tickets.load()).catch(() => {
      /* the module renders its own error state */
    });
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
    if (tab.host === 'capabilities') {
      this.restoreSharedSurface('config');
      this.restoreSharedSurface('tools');
      if (!this.mountCapabilitiesSurface(host, 'ws-cmd-modal-empty')) return;
      return;
    }
    if (tab.host === 'tools') {
      this.restoreSharedSurface('config');
      this.restoreSharedSurface('capabilities');
      const tools = this.mountSharedSurface('tools', '#workspace-detail-tools-card', host);
      if (!tools) {
        host.innerHTML = '<div class="ws-cmd-modal-empty">Find Tools is unavailable.</div>';
        return;
      }
      if (tools.style) tools.style.display = '';
      return;
    }
    this.restoreSharedSurface('tools');
    this.restoreSharedSurface('capabilities');
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

  // Mount the one Capabilities catalog node into whichever surface asked for it
  // and make sure it has fresh data. Both the Tools modal and the Systems rail
  // call this, so there is exactly one catalog implementation (FR-17, FR-100).
  mountCapabilitiesSurface(host, emptyClass) {
    const node = this.mountSharedSurface(
      'capabilities',
      '#workspace-detail-capabilities-card',
      host
    );
    if (!node) {
      host.innerHTML = '<div class="' + emptyClass + '">Capabilities are unavailable.</div>';
      return null;
    }
    if (node.style) node.style.display = '';
    const catalog = typeof window === 'undefined' ? null : window.WorkspaceCapabilities;
    if (catalog) {
      const list = node.querySelector ? node.querySelector('#workspace-capabilities-list') : null;
      if (list && typeof catalog.bindHost === 'function') catalog.bindHost(list);
      if (typeof catalog.load === 'function') {
        void Promise.resolve(catalog.load()).then(() => {
          if (list && typeof catalog.renderInto === 'function') catalog.renderInto(list);
        });
      }
    }
    return node;
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
    if (section === 'watchtower') return this.watchtowerPanelHTML();
    if (section === 'calendar-ops') return this.calendarOpsPanelHTML();
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
          : { initials: name.slice(0, 2).toUpperCase() };
        const status = page.getAgentRosterStatus
          ? page.getAgentRosterStatus(name)
          : { key: 'idle', label: 'Idle' };
        const tone = this.statusTone(status.key, status.label);
        const profile = page.getAgentProfile ? page.getAgentProfile(name) : null;
        let modelLabel = '';
        if (profile && page.getAgentModelPresentation) {
          const m = page.getAgentModelPresentation(profile);
          modelLabel = m && !m.empty ? m.model : '';
        }
        const commanderLbl = keeper ? this.commanderLabel(profile && profile.role) : '';
        let skillCount = 0;
        if (page.getAgentSkillSummary) {
          const sk = page.getAgentSkillSummary(name);
          skillCount = (sk && sk.count) || 0;
        }
        const chips =
          (keeper
            ? '<span class="ws-cmd-mchip is-keeper">★ ' + escapeHtml(commanderLbl) + '</span>'
            : '') +
          '<span class="ws-cmd-mchip">' +
          escapeHtml(modelLabel || '—') +
          '</span>' +
          '<span class="ws-cmd-mchip">Skills · ' +
          skillCount +
          '</span>';
        const removeCtl = keeper
          ? '<span class="ws-cmd-lock" title="' +
            escapeHtml(commanderLbl) +
            ' — can\'t be removed">🔒</span>'
          : '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
            escapeHtml(encoded) +
            '" title="Remove agent" aria-label="Remove ' +
            escapeHtml(name) +
            '">✕</button>';
        // Same shared identity as the roster card behind this modal, rendered
        // into this row's own hexagon tile. Falls back to initials when the
        // shared renderer has not loaded.
        const avatarHTML =
          (avatar.markup && avatar.markup('ws-cmd-mrow-av')) ||
          '<span class="ws-cmd-mrow-av is-initials">' +
            escapeHtml(avatar.initials || 'A') +
            '</span>';
        return (
          '<div class="ws-cmd-mrow">' +
          avatarHTML +
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
    if (a === 'refresh-watchtower' && section === 'watchtower') {
      this.requestWatchtowerData(true);
      this.renderStatModalBody();
      return;
    }
    if (a === 'open-watchtower-workspace' && section === 'watchtower') {
      const workspaceSlug = String(id || '').trim();
      if (!workspaceSlug) return;
      this.closeStatModal();
      const target = '/workspaces/' + encodeURIComponent(workspaceSlug);
      if (typeof window !== 'undefined' && window.location) {
        if (typeof window.location.assign === 'function') window.location.assign(target);
        else window.location.href = target;
      }
      return;
    }
    if (a === 'refresh-calendar-ops' && section === 'calendar-ops') {
      this.requestCalendarOpsPortalData(true);
      this.renderStatModalBody();
      return;
    }
    if (a === 'calendar-ops-setup' && section === 'calendar-ops') {
      this.closeStatModal();
      this.openCalendarOpsConstruct();
      return;
    }
    if (a === 'calendar-ops-open' && section === 'calendar-ops') {
      const workspaceSlug = String(
        this.calendarOpsPortalState().calendarWorkspaceSlug || ''
      ).trim();
      if (!workspaceSlug) return;
      this.closeStatModal();
      const target = '/workspaces/' + encodeURIComponent(workspaceSlug) + '?panel=calendar';
      if (typeof window !== 'undefined' && window.location) {
        if (typeof window.location.assign === 'function') window.location.assign(target);
        else window.location.href = target;
      }
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

  // Commander-slot display label (PRD FR21/FR22): the agent holding the
  // entry-agent slot shows as "Commander" when its role is orchestrator, or
  // "Acting Commander" otherwise (e.g. a solo Specialist holding the slot).
  // Display only — entry-agent mechanics, storage keys (entry_agent_name),
  // and coordinator resolution are unchanged.
  commanderLabel(role) {
    return String(role || '')
      .trim()
      .toLowerCase() === 'orchestrator'
      ? 'Commander'
      : 'Acting Commander';
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

  // An agent's face in Command view.
  //
  // This used to be a procedurally generated robot SVG keyed on a hash of the
  // agent's name — a THIRD private avatar, alongside the roster card's hue tile
  // and the Agents page's real one. The result was that a character a user
  // deliberately chose was invisible on the page where they actually work with
  // that agent, and the same agent had a different face on every surface.
  //
  // It now renders through the shared Avatar Identity resolver, so an uploaded
  // avatar, a curated character, or the deterministic fallback all appear here
  // exactly as they do on /agents and Home (PRD FR-99).
  //
  // The status tone class is still applied to the frame: operational state
  // stays on the frame and the LED beside it, never baked into the art (FR-96).
  agentCharacterHTML(agent, variant = 'roster') {
    if (!agent) return '';
    const page = this.page || {};
    const variantClass = variant === 'stage' ? ' is-stage' : ' is-roster';
    const className = 'ws-cmd-character' + variantClass + ' ' + escapeHtml(agent.tone || 'idle');

    const avatar = page.getAgentAvatarPresentation
      ? page.getAgentAvatarPresentation(agent.name)
      : null;
    const html = avatar && avatar.markup ? avatar.markup(className) : '';
    if (html) return html;

    // The shared renderer is a deferred script. If it has not loaded, the
    // roster still needs a face rather than an empty slot.
    const initials =
      (avatar && avatar.initials) ||
      String(agent.name || 'A')
        .slice(0, 2)
        .toUpperCase();
    return (
      '<span class="' +
      className +
      ' is-initials" aria-hidden="true">' +
      escapeHtml(initials) +
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
    // Compact corner badge: fixed-width "CMD", full label in the title so
    // Acting-Commander distinction is still available on hover/AT.
    const entry = agent.entry
      ? '<span class="ws-cmd-roster-entry" title="' +
        escapeHtml(this.commanderLabel(agent.profile && agent.profile.role)) +
        '">CMD</span>'
      : '';
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
    const commanderLbl = agent.entry
      ? this.commanderLabel(agent.profile && agent.profile.role)
      : '';
    const entry = agent.entry
      ? '<span class="ws-cmd-badge is-keeper">★ ' + escapeHtml(commanderLbl) + '</span>'
      : '';
    const remove = agent.entry
      ? '<span class="ws-cmd-agent-lock" title="The ' +
        escapeHtml(commanderLbl.toLowerCase()) +
        ' cannot be removed">' +
        escapeHtml(commanderLbl) +
        ' locked</span>'
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
      // Model, provider, and prompt are shown here as read-only context and
      // edited through their own controls. A Toolbox never changes them
      // (PRD FR-53, FR-54).
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

  // The Workshop panel: named, versioned toolboxes for this agent instance.
  // workspace-toolbox.js owns everything inside the host; this only places it
  // and tells it which stable instance is selected (PRD FR-16, FR-37).
  toolboxWorkshopHTML(agent) {
    const instanceId = this.agentInstanceIdFor(agent);
    return (
      '<section class="ws-cmd-loadout-card is-workshop">' +
      '<header><span class="ws-cmd-loadout-kicker">Workshop</span></header>' +
      '<div id="workspace-toolbox-panel" class="ws-toolbox-panel" data-agent-instance-id="' +
      escapeHtml(instanceId) +
      '"></div>' +
      '</section>'
    );
  }

  // Resolve the stable AgentInstance.ID for the selected agent. Two instances of
  // one reusable agent share a name, so the node ID is what distinguishes them
  // — falling back to the first instance of that name only when the agent card
  // carries no node (PRD FR-16).
  agentInstanceIdFor(agent) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const instances = Array.isArray(page?.workspace?.agent_instances)
      ? page.workspace.agent_instances
      : [];
    if (!instances.length || !agent) return '';
    const nodeId = String(agent.nodeId || agent.node_id || '').trim();
    if (nodeId) {
      const byNode = instances.find(instance => String(instance?.node_id || '') === nodeId);
      if (byNode) return String(byNode.id || '');
    }
    const name = String(agent.name || '')
      .trim()
      .toLowerCase();
    const byName = instances.find(
      instance =>
        String(instance?.name || '')
          .trim()
          .toLowerCase() === name
    );
    return byName ? String(byName.id || '') : '';
  }

  // Mount the Workshop after the panel exists in the DOM. Called from the same
  // place that renders the agent tabs; a missing host or module is a no-op, so
  // the tab still works when the Workshop script has not loaded.
  mountToolboxWorkshop() {
    if (typeof document === 'undefined' || typeof window === 'undefined') return;
    const host = document.getElementById('workspace-toolbox-panel');
    const workshop = window.WorkspaceToolbox;
    if (!host || !workshop) return;
    const instanceId = host.getAttribute('data-agent-instance-id') || '';
    if (host.dataset && host.dataset.toolboxMountedFor === instanceId) return;
    if (host.dataset) host.dataset.toolboxMountedFor = instanceId;
    workshop.bindHost(host);
    void workshop.init({ agentInstanceId: instanceId });
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
      // The tab KEY stays `loadout` — it is persisted in URL state and read by
      // the tab tests — while the visible label uses the cozy vocabulary
      // (PRD FR-168: Loadout → Toolbox).
      { key: 'loadout', label: 'Toolbox' },
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
      loadout: this.toolboxWorkshopHTML(agent) + this.loadoutTabHTML(agent),
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
      activityLabel: activity?.label || '',
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
    // every reference to them elsewhere keeps working. 'backlog' carries
    // "Backlog" in its own accessible name/title (not just "Quest Board") so
    // the tool-belt button never obscures the real Backlog lifecycle (FR50).
    return [
      { key: 'objective', label: 'Workspace Mission', icon: 'bi-bullseye' },
      { key: 'objectives', label: 'Tasks', icon: 'bi-list-check' },
      { key: 'backlog', label: 'Backlog · Quest Board', icon: 'bi-journal-bookmark' },
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
            return '<div class="ws-cmd-map-task-row-wrap">' + openButton + startButton + '</div>';
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

  // Map Backlog window — presented as the Quest Board (FR50-57). Reuses the
  // exact same backlogItems()/backlogSync() state and shared drawer as the
  // Details panel (group 4): no separate list state, no duplicate records.
  renderMapBacklogPanel() {
    const page = this.page || {};
    const items = this.backlogItems();
    const count = items.length;
    const shown = items.slice(0, 4);
    const sync = this.backlogSync();
    let rows;
    if (page.backlogLoading && !count) {
      rows =
        '<div class="ws-cmd-map-empty is-quest-empty"><strong>Loading…</strong><span>Fetching the backlog.</span></div>';
    } else if (page.backlogLoadFailed) {
      rows =
        '<div class="ws-cmd-map-empty is-quest-empty is-error"><strong>Couldn’t load the backlog</strong><span>Try Sync Now or Open Backlog.</span></div>';
    } else if (count) {
      rows = shown
        .map((item, index) => {
          const task = (item && item.task) || item || {};
          const id = String(task.id || '');
          const label = String(task.description || 'Untitled idea');
          const questNumber = String(index + 1).padStart(2, '0');
          const busy = this.mapAcceptQuestBusyId === id;
          const openButton =
            '<button type="button" class="ws-cmd-map-task-row is-idea" data-cmd-map-open-backlog="' +
            escapeHtml(id) +
            '"><span class="ws-cmd-map-task-marker">Idea ' +
            escapeHtml(questNumber) +
            '</span><span class="ws-cmd-map-task-name">' +
            escapeHtml(label) +
            '</span></button>';
          // "Accept Quest" is the presentation label; the accessible name spells
          // out Promote to Ready so the control never obscures the real action (FR53).
          const acceptButton =
            '<button type="button" class="ws-cmd-map-task-start" data-cmd-map-accept-quest="' +
            escapeHtml(id) +
            '" aria-label="Accept Quest — Promote ' +
            escapeHtml(label) +
            ' to Ready"' +
            (busy ? ' disabled' : '') +
            '><i class="bi bi-check2" aria-hidden="true"></i><span>' +
            (busy ? 'Accepting…' : 'Accept Quest') +
            '</span></button>';
          return '<div class="ws-cmd-map-task-row-wrap">' + openButton + acceptButton + '</div>';
        })
        .join('');
    } else {
      rows =
        '<div class="ws-cmd-map-empty is-quest-empty"><strong>Quest Board clear</strong><span>Nothing saved for later. Add an idea without committing it.</span></div>';
    }
    return (
      '<div class="ws-cmd-map-window-section is-backlog">' +
      this.mapZoneHeaderHTML('Backlog', 'Quest Board', count) +
      this.backlogSyncBadgeHTML(sync) +
      '<div class="ws-cmd-map-task-list">' +
      rows +
      '</div>' +
      '<div class="ws-cmd-map-zone-actions">' +
      '<button type="button" class="ws-cmd-map-zone-action" data-cmd-open-backlog-drawer>Open Backlog</button>' +
      '</div></div>'
    );
  }

  async runMapAcceptQuest(itemId) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(itemId || '').trim();
    if (!page || !id || typeof page.promoteBacklogItem !== 'function') return;
    this.mapAcceptQuestBusyId = id;
    this.render();
    const promotedTask = await page.promoteBacklogItem(id, this.backlogItemOwner(id));
    this.mapAcceptQuestBusyId = '';
    // page.promoteBacklogItem() reloads backlog+tasks and calls refresh(),
    // which repaints the Quest Board and Active Tasks together (FR54).
    if (promotedTask) {
      this.activeMapWindow = '';
      this.render();
      this.openPromotedTaskModal(promotedTask);
    }
  }

  // After Promote to Ready (which never assigns or schedules on its own,
  // FR9-12), open the real Task modal on the now-Ready task so the user can
  // pick an agent/schedule right away if they want to. Cancelling the modal
  // leaves the task promoted-but-unassigned — the promotion already
  // happened and is not gated on this modal being saved. `item` is the
  // BacklogItemView shape promoteBacklogItem resolves with — {task, owning_workspace_id,
  // owning_workspace_name} — same convention as every other backlog item in this file.
  openPromotedTaskModal(item) {
    const task = (item && item.task) || item;
    if (!task) return;
    if (
      typeof window === 'undefined' ||
      !window.taskModalController ||
      typeof window.taskModalController.openForEdit !== 'function'
    ) {
      return;
    }
    const page = this.page || window.workspaceDetail || null;
    window.taskModalController.openForEdit(task, () => {
      if (page && typeof page.loadTasks === 'function') page.loadTasks();
    });
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
    // The two persistent drawers share the same screen position; only one may
    // be open at a time (reachable via Details→Map view switches, or a
    // ?panel=backlog deep link landing while Tasks was already open).
    if (this.backlogDrawerOpen) {
      this.backlogDrawerOpen = false;
      if (this.backlogDrawerEl) this.backlogDrawerEl.hidden = true;
    }
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
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
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
    const stillHere = this.drawerTasks().some(
      t => String(t.id || '') === this.taskDrawerSelectedId
    );
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

  // ---------- Backlog panel + drawer (PRD workspace-backlog) ----------
  //
  // Backlog is a dedicated, separate surface from executable Tasks (FR32-33):
  // a compact rail panel (count, top previews, Add to Backlog, Open Backlog,
  // compact sync status) plus a persistent non-modal drawer — the same
  // pattern as the Task drawer above — for the full list, editing, ordering,
  // promotion, and sync/conflict controls (FR34-37). The drawer is the single
  // shared interaction surface opened from both Details and the Map Quest
  // Board (group 5); there is exactly one drawer instance and one client
  // record cache (page.backlogItems), never a per-entry-point copy.

  backlogItems() {
    const page = this.page || {};
    return Array.isArray(page.backlogItems) ? page.backlogItems : [];
  }

  backlogSync() {
    const page = this.page || {};
    return page.backlogSync || null;
  }

  backlogIncludeDescendants() {
    const page = this.page || {};
    return Boolean(page.backlogIncludeDescendants);
  }

  // Toggling re-fetches from the server with/without the roll-up so ownership
  // badges and owning-workspace routing always reflect real data, never a
  // client-side guess (FR62-66).
  toggleBacklogDescendants() {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page) return;
    page.backlogIncludeDescendants = !page.backlogIncludeDescendants;
    if (typeof page.loadBacklog === 'function') page.loadBacklog();
  }

  renderBacklogPanel() {
    const page = this.page || {};
    const items = this.backlogItems();
    const count = items.length;
    const preview = items.slice(0, 5);
    const sync = this.backlogSync();
    // Distinct loading/error/empty states (FR38): none of these implies
    // Tasks is empty — they describe the Backlog panel's own fetch state.
    let body;
    if (page.backlogLoading && !count) {
      body = '<div class="ws-cmd-rail-empty">Loading backlog…</div>';
    } else if (page.backlogLoadFailed) {
      body =
        '<div class="ws-cmd-rail-empty is-error">Couldn’t load the backlog. Try again shortly.</div>';
    } else if (count) {
      const rows = preview.map(item => {
        const task = (item && item.task) || item || {};
        const id = String(task.id || '');
        const title = String(task.description || 'Untitled idea');
        return (
          '<button type="button" class="ws-cmd-rail-item" data-cmd-open-backlog-drawer data-cmd-backlog-select="' +
          escapeHtml(id) +
          '"><span class="ws-cmd-rail-t">' +
          escapeHtml(title) +
          '</span></button>'
        );
      });
      body =
        rows.join('') +
        (count > preview.length
          ? '<button type="button" class="ws-cmd-rail-more" data-cmd-open-backlog-drawer>+ ' +
            (count - preview.length) +
            ' more</button>'
          : '');
    } else {
      body =
        '<div class="ws-cmd-rail-empty">Nothing saved for later. Add an idea without committing it to an agent.</div>';
    }
    return (
      '<section class="ws-cmd-panel ws-cmd-panel-backlog' +
      (count ? '' : ' is-empty') +
      '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Backlog</h4><span class="ws-cmd-panel-count">' +
      count +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action is-icon-only" data-cmd-backlog-add aria-label="Add to Backlog" title="Add to Backlog">+</button>' +
      // Shortcut into the canonical destination, filtered to Backlog
      // (tasks/prd-workspace-ticket-management.md FR-65, FR-80, FR-81).
      // This panel is a COUNT and a way in; the Tickets destination is where
      // backlog work is actually managed. Two editable backlog surfaces is
      // how the Backlog and Task views drifted apart in the first place.
      '<button type="button" class="ws-cmd-panel-action is-icon-only" data-cmd-open-tickets="backlog" aria-label="View backlog in Tickets" title="View backlog in Tickets">◉</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-open-backlog-drawer aria-label="Open Backlog" title="Open Backlog">▸</button>' +
      '</div></div>' +
      '<div class="ws-cmd-panel-body">' +
      body +
      '</div>' +
      this.backlogSyncBadgeHTML(sync) +
      '</section>'
    );
  }

  backlogSyncBadgeHTML(sync) {
    if (!sync) return '';
    const hasIssue = Boolean(sync.warning) || Boolean(sync.conflict);
    const label = hasIssue
      ? sync.conflict
        ? 'Sync conflict'
        : 'Sync warning'
      : sync.last_synced_at
        ? 'Synced'
        : 'Not yet synced';
    return (
      '<div class="ws-cmd-panel-sync' +
      (hasIssue ? ' is-warning' : '') +
      '" title="' +
      escapeHtml(sync.warning || '') +
      '">' +
      escapeHtml(label) +
      '</div>'
    );
  }

  // All Backlog items for this workspace (and, opt-in, its descendants),
  // ordered by persistent rank as returned by the server (FR43, 61, 76).
  backlogDrawerItems() {
    return this.backlogItems();
  }

  backlogDrawerSelectedItem() {
    const id = this.backlogDrawerSelectedId;
    return this.backlogDrawerItems().find(it => String((it.task || it).id || '') === id) || null;
  }

  /**
   * RETIRED: the Backlog drawer was the second editable Backlog surface, and
   * every way in now lands on the canonical Tickets destination instead
   * (tasks/prd-workspace-ticket-management.md FR-81, FR-82).
   *
   * The redirect lives here rather than at each call site on purpose: every
   * opener — the panel's ▸ button, a rail item, the Map's zone action, the
   * "+N more" link, quick capture — already funnels through this method, so
   * one change retires all of them and none can be missed.
   *
   * Its rendering and mutation methods below are now unreachable. They are
   * left in place rather than deleted because removing the legacy Backlog
   * surface wholesale is the separate breaking-change project the PRD
   * describes; this change removes it from the user's reach, which is what
   * FR-82 actually asks for.
   */
  openBacklogDrawer(trigger, opts) {
    const options = opts || {};
    // Selecting a specific item opens that ticket; otherwise open the Backlog
    // list. Either way the user ends up in the one place backlog work is
    // managed.
    const tickets = typeof window === 'undefined' ? null : window.WorkspaceHubTickets;
    if (tickets && typeof tickets.setFilterState === 'function') {
      this.setCommandViewMode('tickets', { focus: false });
      const selectId = options.selectId ? String(options.selectId) : '';
      void Promise.resolve(tickets.setFilterState('backlog'))
        .then(() => {
          if (selectId && typeof tickets.openDetail === 'function') {
            return tickets.openDetail(this.workspaceId(), selectId);
          }
          // An "Add to Backlog" affordance asked to CAPTURE, so land the user
          // in the create form already typing rather than in a list.
          if (options.openCapture && typeof tickets.focusCreate === 'function') {
            tickets.focusCreate('backlog');
          }
          return undefined;
        })
        .catch(() => {
          /* the tickets surface renders its own error state */
        });
      return;
    }

    // No canonical surface available (the module failed to load): fall through
    // to the legacy drawer rather than leaving the button dead.
    this.openLegacyBacklogDrawer(trigger, options);
  }

  /** The pre-Ticket Backlog drawer. Reachable only as a fallback; see above. */
  openLegacyBacklogDrawer(trigger, opts) {
    const options = opts || {};
    this.backlogDrawerTrigger = trigger || null;
    // Close the Map's Quest Board window so the drawer never opens beneath it
    // (mirrors openTaskDrawer's Objectives-window handling).
    if (this.activeMapWindow) this.activeMapWindow = '';
    // Only one persistent drawer may be open at a time (see openTaskDrawer).
    if (this.taskDrawerOpen) {
      this.taskDrawerOpen = false;
      if (this.taskDrawerEl) this.taskDrawerEl.hidden = true;
    }
    this.backlogDrawerOpen = true;
    if (options.selectId) {
      this.backlogDrawerSelectedId = String(options.selectId);
    } else if (!this.backlogDrawerSelectedId) {
      const first = this.backlogDrawerItems()[0];
      this.backlogDrawerSelectedId = first ? String((first.task || first).id || '') : '';
    }
    if (options.openCapture) {
      this.backlogQuickCaptureOpen = true;
    }
    this.render();
    const el = this.ensureBacklogDrawer();
    if (el) {
      el.hidden = false;
      this.renderBacklogDrawerBody();
      const focusTarget = options.openCapture
        ? el.querySelector('[data-cmd-backlog-quick-input]')
        : el.querySelector('.ws-cmd-drawer-title');
      if (focusTarget && typeof focusTarget.focus === 'function') {
        try {
          focusTarget.focus({ preventScroll: true });
        } catch (_e) {
          focusTarget.focus();
        }
      }
    }
    this.syncURLState();
  }

  closeBacklogDrawer() {
    const trigger = this.backlogDrawerTrigger;
    this.backlogDrawerOpen = false;
    this.backlogDrawerTrigger = null;
    this.backlogQuickCaptureOpen = false;
    this.backlogQuickCaptureError = '';
    this.backlogPromoteConfirmId = '';
    if (this.backlogDrawerEl) this.backlogDrawerEl.hidden = true;
    let target = trigger && typeof trigger.focus === 'function' ? trigger : null;
    if (!target && this.container) {
      const fallback = this.container.querySelector('[data-cmd-open-backlog-drawer]');
      if (fallback && typeof fallback.focus === 'function') target = fallback;
    }
    if (target) target.focus();
    this.syncURLState();
  }

  selectBacklogDrawerItem(itemId) {
    const id = String(itemId || '').trim();
    if (!id) return;
    this.backlogDrawerSelectedId = id;
    this.backlogPromoteConfirmId = '';
    this.renderBacklogDrawerBody();
    this.syncURLState();
  }

  toggleBacklogQuickCapture(open) {
    this.backlogQuickCaptureOpen = open != null ? Boolean(open) : !this.backlogQuickCaptureOpen;
    if (!this.backlogQuickCaptureOpen) {
      this.backlogQuickCaptureDraft = '';
      this.backlogQuickCaptureDetailsDraft = '';
      this.backlogQuickCaptureError = '';
    }
    this.renderBacklogDrawerBody();
    if (this.backlogQuickCaptureOpen && this.backlogDrawerEl) {
      const input = this.backlogDrawerEl.querySelector('[data-cmd-backlog-quick-input]');
      if (input && typeof input.focus === 'function') input.focus();
    }
  }

  async submitBacklogQuickCapture() {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const title = String(this.backlogQuickCaptureDraft || '').trim();
    if (!title) {
      this.backlogQuickCaptureError = 'Title is required.';
      this.renderBacklogDrawerBody();
      return;
    }
    if (!page || typeof page.createBacklogItem !== 'function') return;
    this.backlogQuickCaptureSubmitting = true;
    this.backlogQuickCaptureError = '';
    this.renderBacklogDrawerBody();
    const created = await page.createBacklogItem({
      description: title,
      details: String(this.backlogQuickCaptureDetailsDraft || '').trim()
    });
    this.backlogQuickCaptureSubmitting = false;
    if (created) {
      this.backlogQuickCaptureOpen = false;
      this.backlogQuickCaptureDraft = '';
      this.backlogQuickCaptureDetailsDraft = '';
      this.backlogDrawerSelectedId = String(created.id || created.task?.id || '');
    } else {
      this.backlogQuickCaptureError = 'Failed to add to backlog.';
    }
    // page.createBacklogItem() already reloads and calls refresh(), but that
    // refresh fires while backlogQuickCaptureOpen was still true (it runs
    // inside the awaited call, before the flag flips above) — so its render
    // still shows the form open. Render again now that the flag is correct,
    // or the form visually never closes despite the item having saved.
    this.renderBacklogDrawerBody();
  }

  // Toggle the post-creation supported-field editor for a backlog item
  // (FR6, 20: title/details/tags/priority/reference URL, editable before or
  // after creation — quick capture only covers the title at creation time).
  openBacklogEdit(itemId) {
    const id = String(itemId || '').trim();
    if (!id) return;
    const item = this.backlogDrawerItems().find(it => String((it.task || it).id || '') === id);
    const task = item ? item.task || item : null;
    if (!task) return;
    this.backlogEditItemId = id;
    this.backlogEditDraft = {
      description: String(task.description || ''),
      details: String(task.details || ''),
      tags: Array.isArray(task.tags) ? task.tags.join(', ') : '',
      priority: Number.isFinite(Number(task.priority)) ? Number(task.priority) : 3,
      referenceUrl: String(task.reference_url || '')
    };
    this.backlogEditError = '';
    this.renderBacklogDrawerBody();
  }

  closeBacklogEdit() {
    this.backlogEditItemId = '';
    this.backlogEditDraft = null;
    this.backlogEditError = '';
    this.renderBacklogDrawerBody();
  }

  updateBacklogEditField(field, value) {
    if (!this.backlogEditDraft) return;
    this.backlogEditDraft[field] = value;
  }

  // The owning workspace ID for a Backlog item, from the roll-up view's own
  // record — never inferred from the current page (FR48-50, 60, 63-65). A
  // roll-up card always carries its own owning_workspace_id; mutations must
  // route there, not to whichever workspace happens to be open.
  backlogItemOwner(itemId) {
    const id = String(itemId || '').trim();
    const found = this.backlogDrawerItems().find(it => String((it.task || it).id || '') === id);
    return (found && found.owning_workspace_id) || this.workspaceId();
  }

  backlogItemIsLocal(itemId) {
    return this.backlogItemOwner(itemId) === this.workspaceId();
  }

  async submitBacklogEdit() {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = this.backlogEditItemId;
    const draft = this.backlogEditDraft;
    if (!page || !id || !draft) return;
    const description = String(draft.description || '').trim();
    if (!description) {
      this.backlogEditError = 'Title is required.';
      this.renderBacklogDrawerBody();
      return;
    }
    this.backlogEditSubmitting = true;
    this.renderBacklogDrawerBody();
    const ok = await page.updateBacklogItem(
      id,
      {
        description,
        details: String(draft.details || ''),
        tags: String(draft.tags || '')
          .split(',')
          .map(t => t.trim())
          .filter(Boolean),
        priority: Number(draft.priority) || 3,
        referenceUrl: String(draft.referenceUrl || '')
      },
      this.backlogItemOwner(id)
    );
    this.backlogEditSubmitting = false;
    if (ok) {
      this.closeBacklogEdit();
    } else {
      this.backlogEditError = 'Failed to save changes.';
      this.renderBacklogDrawerBody();
    }
  }

  confirmBacklogPromote(itemId) {
    this.backlogPromoteConfirmId = String(itemId || '');
    this.renderBacklogDrawerBody();
  }

  cancelBacklogPromote() {
    this.backlogPromoteConfirmId = '';
    this.renderBacklogDrawerBody();
  }

  async runBacklogPromote(itemId) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(itemId || '').trim();
    if (!page || !id || typeof page.promoteBacklogItem !== 'function') return;
    this.backlogPromoteBusy = true;
    this.renderBacklogDrawerBody();
    const promotedTask = await page.promoteBacklogItem(id, this.backlogItemOwner(id));
    this.backlogPromoteBusy = false;
    this.backlogPromoteConfirmId = '';
    // page.promoteBacklogItem() reloads backlog+tasks and calls refresh(),
    // which repaints the drawer body for us.
    if (promotedTask) {
      this.closeBacklogDrawer();
      this.openPromotedTaskModal(promotedTask);
    }
  }

  async runBacklogDelete(itemId) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(itemId || '').trim();
    if (!page || !id || typeof page.deleteBacklogItem !== 'function') return;
    await page.deleteBacklogItem(id, this.backlogItemOwner(id));
  }

  async runBacklogSyncNow() {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.syncBacklogNow !== 'function') return;
    await page.syncBacklogNow();
  }

  async runBacklogResolveConflict(itemId, useFile) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(itemId || '').trim();
    if (!page || !id || typeof page.resolveBacklogConflict !== 'function') return;
    await page.resolveBacklogConflict(id, useFile);
  }

  // Non-drag reordering (FR18, 20, 37, 57): move the selected item one slot
  // up/down within the current rank order and persist the full new order.
  // Restricted to this workspace's own items: BacklogRank is a per-workspace
  // rank space, and a descendant roll-up's combined list has no single
  // coherent order to reorder across workspaces (FR65 — roll-up must not
  // apply this workspace's structure to a child record). moveBacklogItem
  // reorders only among the local items, ignoring rolled-up rows entirely.
  async moveBacklogItem(itemId, direction) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const id = String(itemId || '').trim();
    if (!page || !id || typeof page.reorderBacklog !== 'function') return;
    if (!this.backlogItemIsLocal(id)) return;
    const ids = this.backlogDrawerItems()
      .filter(it => this.backlogItemIsLocal(String((it.task || it).id || '')))
      .map(it => String((it.task || it).id || ''));
    const idx = ids.indexOf(id);
    if (idx === -1) return;
    const swapWith = direction === 'up' ? idx - 1 : idx + 1;
    if (swapWith < 0 || swapWith >= ids.length) return;
    [ids[idx], ids[swapWith]] = [ids[swapWith], ids[idx]];
    await page.reorderBacklog(ids);
  }

  // If the selected item vanished on a live refresh (promoted/deleted), pick
  // the next item and announce it, mirroring reconcileDrawerSelection.
  reconcileBacklogDrawerSelection() {
    this._backlogDrawerAnnounce = '';
    if (!this.backlogDrawerSelectedId) return;
    const stillHere = this.backlogDrawerItems().some(
      it => String((it.task || it).id || '') === this.backlogDrawerSelectedId
    );
    if (stillHere) return;
    this._backlogDrawerAnnounce = 'The selected backlog item is no longer available.';
    const next = this.backlogDrawerItems()[0];
    this.backlogDrawerSelectedId = next ? String((next.task || next).id || '') : '';
  }

  ensureBacklogDrawer() {
    if (this.backlogDrawerEl) return this.backlogDrawerEl;
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
    const el = document.createElement('aside');
    el.className = 'ws-cmd-drawer ws-cmd-drawer-backlog';
    el.setAttribute('role', 'region');
    el.setAttribute('aria-label', 'Backlog');
    el.hidden = true;
    el.style.zIndex = 'var(--wsx-layer-drawer)';
    el.addEventListener('click', event => {
      if (event.target.closest('[data-cmd-drawer-close]')) {
        this.closeBacklogDrawer();
        return;
      }
      if (event.target.closest('[data-cmd-backlog-quick-cancel]')) {
        this.toggleBacklogQuickCapture(false);
        return;
      }
      if (event.target.closest('[data-cmd-backlog-quick-open]')) {
        this.toggleBacklogQuickCapture(true);
        return;
      }
      if (event.target.closest('[data-cmd-backlog-descendants-toggle]')) {
        this.toggleBacklogDescendants();
        return;
      }
      if (event.target.closest('[data-cmd-backlog-sync-now]')) {
        this.runBacklogSyncNow();
        return;
      }
      const conflictBtn = event.target.closest('[data-cmd-backlog-resolve-conflict]');
      if (conflictBtn) {
        this.runBacklogResolveConflict(
          conflictBtn.getAttribute('data-cmd-backlog-item'),
          conflictBtn.getAttribute('data-cmd-backlog-resolve-conflict') === 'file'
        );
        return;
      }
      const moveBtn = event.target.closest('[data-cmd-backlog-move]');
      if (moveBtn) {
        this.moveBacklogItem(
          moveBtn.getAttribute('data-cmd-backlog-item'),
          moveBtn.getAttribute('data-cmd-backlog-move')
        );
        return;
      }
      const promoteBtn = event.target.closest('[data-cmd-backlog-promote]');
      if (promoteBtn) {
        this.confirmBacklogPromote(promoteBtn.getAttribute('data-cmd-backlog-item'));
        return;
      }
      if (event.target.closest('[data-cmd-backlog-promote-confirm]')) {
        this.runBacklogPromote(this.backlogPromoteConfirmId);
        return;
      }
      if (event.target.closest('[data-cmd-backlog-promote-cancel]')) {
        this.cancelBacklogPromote();
        return;
      }
      const deleteBtn = event.target.closest('[data-cmd-backlog-delete]');
      if (deleteBtn) {
        this.runBacklogDelete(deleteBtn.getAttribute('data-cmd-backlog-item'));
        return;
      }
      const editBtn = event.target.closest('[data-cmd-backlog-edit]');
      if (editBtn) {
        this.openBacklogEdit(editBtn.getAttribute('data-cmd-backlog-item'));
        return;
      }
      if (event.target.closest('[data-cmd-backlog-edit-cancel]')) {
        this.closeBacklogEdit();
        return;
      }
      const row = event.target.closest('[data-cmd-backlog-select]');
      if (row) this.selectBacklogDrawerItem(row.getAttribute('data-cmd-backlog-select'));
    });
    el.addEventListener('submit', event => {
      if (event.target.closest('[data-cmd-backlog-quick-form]')) {
        event.preventDefault();
        this.submitBacklogQuickCapture();
        return;
      }
      if (event.target.closest('[data-cmd-backlog-edit-form]')) {
        event.preventDefault();
        this.submitBacklogEdit();
      }
    });
    const captureFieldInput = event => {
      const quickInput = event.target.closest('[data-cmd-backlog-quick-input]');
      if (quickInput) {
        this.backlogQuickCaptureDraft = quickInput.value;
        return;
      }
      const quickDetails = event.target.closest('[data-cmd-backlog-quick-details]');
      if (quickDetails) {
        this.backlogQuickCaptureDetailsDraft = quickDetails.value;
        return;
      }
      const editField = event.target.closest('[data-cmd-backlog-edit-field]');
      if (editField) {
        this.updateBacklogEditField(
          editField.getAttribute('data-cmd-backlog-edit-field'),
          editField.value
        );
      }
    };
    el.addEventListener('input', captureFieldInput);
    // <select> reliably fires 'change' across browsers; 'input' support for
    // select elements is inconsistent, so listen for both.
    el.addEventListener('change', captureFieldInput);
    this.backlogDrawerEl = el;
    if (this.container && this.container.appendChild) this.container.appendChild(el);
    return el;
  }

  renderBacklogDrawerBody() {
    const el = this.ensureBacklogDrawer();
    if (!el || el.hidden) return;
    this.reconcileBacklogDrawerSelection();
    const prevList = el.querySelector('.ws-cmd-drawer-list');
    const prevScroll = prevList ? prevList.scrollTop : 0;
    const activeInput = el.querySelector('[data-cmd-backlog-quick-input]');
    const hadFocus = activeInput === document.activeElement;
    el.innerHTML = this.backlogDrawerHTML();
    const nextList = el.querySelector('.ws-cmd-drawer-list');
    if (nextList) nextList.scrollTop = prevScroll;
    if (hadFocus) {
      const nextInput = el.querySelector('[data-cmd-backlog-quick-input]');
      if (nextInput && typeof nextInput.focus === 'function') nextInput.focus();
    }
  }

  backlogDrawerHTML() {
    const items = this.backlogDrawerItems();
    const includeDescendants = this.backlogIncludeDescendants();
    return (
      '<header class="ws-cmd-drawer-head">' +
      '<h2 class="ws-cmd-drawer-title" tabindex="-1">Backlog</h2>' +
      '<div class="ws-cmd-drawer-head-actions">' +
      '<button type="button" class="ws-cmd-drawer-add is-icon-only" data-cmd-backlog-quick-open aria-label="Add to backlog" title="Add to backlog">+</button>' +
      '<button type="button" class="ws-cmd-drawer-close" data-cmd-drawer-close aria-label="Close backlog">×</button>' +
      '</div>' +
      '</header>' +
      '<div class="ws-cmd-drawer-live sr-only" role="status" aria-live="polite" aria-atomic="true">' +
      escapeHtml(this._backlogDrawerAnnounce || '') +
      '</div>' +
      (this.backlogQuickCaptureOpen ? this.backlogQuickCaptureHTML() : '') +
      '<label class="ws-cmd-backlog-descendants">' +
      '<input type="checkbox" data-cmd-backlog-descendants-toggle' +
      (includeDescendants ? ' checked' : '') +
      ' /> Include descendant workspaces</label>' +
      '<div class="ws-cmd-drawer-list" role="list">' +
      this.backlogDrawerListHTML(items) +
      '</div>' +
      '<div class="ws-cmd-drawer-preview">' +
      this.backlogDrawerPreviewHTML() +
      '</div>' +
      this.backlogSyncPanelHTML()
    );
  }

  backlogQuickCaptureHTML() {
    return (
      '<form class="ws-cmd-backlog-quick" data-cmd-backlog-quick-form>' +
      '<label class="sr-only" for="ws-cmd-backlog-quick-input">Backlog item title</label>' +
      '<input id="ws-cmd-backlog-quick-input" type="text" class="ws-cmd-backlog-quick-input" placeholder="Add an idea…" value="' +
      escapeHtml(this.backlogQuickCaptureDraft) +
      '" data-cmd-backlog-quick-input />' +
      '<label class="sr-only" for="ws-cmd-backlog-quick-details">Details (optional)</label>' +
      '<textarea id="ws-cmd-backlog-quick-details" class="ws-cmd-backlog-quick-details" placeholder="Details (optional)" rows="2" data-cmd-backlog-quick-details>' +
      escapeHtml(this.backlogQuickCaptureDetailsDraft) +
      '</textarea>' +
      '<button type="submit" class="ws-cmd-backlog-quick-submit"' +
      (this.backlogQuickCaptureSubmitting ? ' disabled' : '') +
      '>Add</button>' +
      '<button type="button" class="ws-cmd-backlog-quick-cancel" data-cmd-backlog-quick-cancel aria-label="Cancel">×</button>' +
      (this.backlogQuickCaptureError
        ? '<div class="ws-cmd-backlog-quick-error" role="alert">' +
          escapeHtml(this.backlogQuickCaptureError) +
          '</div>'
        : '') +
      '<p class="ws-cmd-backlog-quick-hint">Saved without an agent or schedule — promote it to Ready when you decide to do it.</p>' +
      '</form>'
    );
  }

  backlogDrawerListHTML(items) {
    if (!items.length) {
      return (
        '<div class="ws-cmd-drawer-empty"><strong>Nothing saved for later</strong>' +
        '<span>Add an idea without committing it to an agent.</span></div>'
      );
    }
    return items
      .map(item => {
        const task = (item && item.task) || item || {};
        const id = String(task.id || '');
        const title = String(task.description || 'Untitled idea');
        const selected = id === this.backlogDrawerSelectedId;
        const owner = item && item.owning_workspace_name;
        const ownerBadge =
          owner && this.backlogIncludeDescendants()
            ? '<span class="ws-cmd-drawer-row-owner">' + escapeHtml(owner) + '</span>'
            : '';
        return (
          '<button type="button" role="listitem" class="ws-cmd-drawer-row' +
          (selected ? ' is-selected' : '') +
          '" data-cmd-backlog-select="' +
          escapeHtml(id) +
          '" aria-current="' +
          (selected ? 'true' : 'false') +
          '">' +
          ownerBadge +
          '<span class="ws-cmd-drawer-row-main">' +
          '<span class="ws-cmd-drawer-row-title">' +
          escapeHtml(title) +
          '</span>' +
          '</span></button>'
        );
      })
      .join('');
  }

  backlogDrawerPreviewHTML() {
    const item = this.backlogDrawerSelectedItem();
    if (!item) {
      return '<div class="ws-cmd-drawer-preview-empty">Select a backlog item to see details.</div>';
    }
    const task = item.task || item;
    const id = String(task.id || '');
    const title = String(task.description || 'Untitled idea');
    const details = String(task.details || '').trim();
    const isOwnedElsewhere =
      item.owning_workspace_id && item.owning_workspace_id !== this.workspaceId();
    const ownerSlug = String(item.owning_workspace_slug || '').trim();
    const ownerLink =
      isOwnedElsewhere && ownerSlug
        ? '<a class="ws-cmd-drawer-row-owner-link" href="/workspaces/' +
          encodeURIComponent(ownerSlug) +
          '">' +
          escapeHtml(item.owning_workspace_name || 'Owning workspace') +
          '</a>'
        : '';
    if (this.backlogEditItemId === id && this.backlogEditDraft) {
      return this.backlogEditFormHTML(id);
    }
    const confirming = this.backlogPromoteConfirmId === id;
    const promoteControl = confirming
      ? '<div class="ws-cmd-backlog-promote-confirm">' +
        '<p>Turn into a task? It becomes eligible for assignment and execution (promoted to Ready), but nothing runs automatically — you’ll get a chance to assign it next.</p>' +
        '<button type="button" class="ws-cmd-drawer-action" data-cmd-backlog-promote-confirm' +
        (this.backlogPromoteBusy ? ' disabled' : '') +
        '>Confirm — Turn into Task</button>' +
        '<button type="button" class="ws-cmd-backlog-quick-cancel" data-cmd-backlog-promote-cancel>Cancel</button>' +
        '</div>'
      : '<button type="button" class="ws-cmd-drawer-action" data-cmd-backlog-promote data-cmd-backlog-item="' +
        escapeHtml(id) +
        '">Turn into Task</button>';
    // Reordering only has a coherent meaning within this workspace's own
    // rank space (FR65) — a rolled-up descendant item hides the move
    // controls rather than reordering across unrelated workspaces.
    const moveControls = this.backlogItemIsLocal(id)
      ? '<button type="button" class="ws-cmd-backlog-move" data-cmd-backlog-move="up" data-cmd-backlog-item="' +
        escapeHtml(id) +
        '" aria-label="Move up">▲</button>' +
        '<button type="button" class="ws-cmd-backlog-move" data-cmd-backlog-move="down" data-cmd-backlog-item="' +
        escapeHtml(id) +
        '" aria-label="Move down">▼</button>'
      : '';
    return (
      '<div class="ws-cmd-drawer-preview-head">' +
      '<span class="ws-cmd-drawer-preview-state tone-neutral">Backlog</span>' +
      '<h3 class="ws-cmd-drawer-preview-title">' +
      escapeHtml(title) +
      '</h3>' +
      ownerLink +
      '</div>' +
      (details
        ? '<p class="ws-cmd-drawer-preview-brief">' + escapeHtml(details.slice(0, 400)) + '</p>'
        : '') +
      '<div class="ws-cmd-drawer-preview-actions">' +
      promoteControl +
      moveControls +
      '<button type="button" class="ws-cmd-backlog-move" data-cmd-backlog-edit data-cmd-backlog-item="' +
      escapeHtml(id) +
      '">Edit</button>' +
      '<button type="button" class="ws-cmd-drawer-action is-danger" data-cmd-backlog-delete data-cmd-backlog-item="' +
      escapeHtml(id) +
      '">Delete</button>' +
      '</div>'
    );
  }

  // Post-creation supported-field editor (FR6, 20): title, details, tags,
  // priority, reference URL — the same fields quick capture accepts, editable
  // any time afterward. No lifecycle/ownership/provenance/id field appears
  // here, matching the service's BacklogUpdateInput contract (FR6).
  backlogEditFormHTML(id) {
    const draft = this.backlogEditDraft || {};
    const priorityOptions = [
      { value: 1, label: 'High' },
      { value: 3, label: 'Medium' },
      { value: 5, label: 'Low' }
    ]
      .map(
        opt =>
          '<option value="' +
          opt.value +
          '"' +
          (Number(draft.priority) === opt.value ? ' selected' : '') +
          '>' +
          opt.label +
          '</option>'
      )
      .join('');
    return (
      '<form class="ws-cmd-backlog-edit" data-cmd-backlog-edit-form data-cmd-backlog-item="' +
      escapeHtml(id) +
      '">' +
      '<label class="ws-cmd-backlog-edit-label" for="ws-cmd-backlog-edit-title">Title</label>' +
      '<input id="ws-cmd-backlog-edit-title" type="text" class="ws-cmd-backlog-edit-input" data-cmd-backlog-edit-field="description" value="' +
      escapeHtml(draft.description || '') +
      '" />' +
      '<label class="ws-cmd-backlog-edit-label" for="ws-cmd-backlog-edit-details">Details</label>' +
      '<textarea id="ws-cmd-backlog-edit-details" class="ws-cmd-backlog-edit-textarea" data-cmd-backlog-edit-field="details">' +
      escapeHtml(draft.details || '') +
      '</textarea>' +
      '<label class="ws-cmd-backlog-edit-label" for="ws-cmd-backlog-edit-tags">Tags (comma-separated)</label>' +
      '<input id="ws-cmd-backlog-edit-tags" type="text" class="ws-cmd-backlog-edit-input" data-cmd-backlog-edit-field="tags" value="' +
      escapeHtml(draft.tags || '') +
      '" />' +
      '<label class="ws-cmd-backlog-edit-label" for="ws-cmd-backlog-edit-priority">Priority</label>' +
      '<select id="ws-cmd-backlog-edit-priority" class="ws-cmd-backlog-edit-input" data-cmd-backlog-edit-field="priority">' +
      priorityOptions +
      '</select>' +
      '<label class="ws-cmd-backlog-edit-label" for="ws-cmd-backlog-edit-url">Reference URL</label>' +
      '<input id="ws-cmd-backlog-edit-url" type="url" class="ws-cmd-backlog-edit-input" data-cmd-backlog-edit-field="referenceUrl" value="' +
      escapeHtml(draft.referenceUrl || '') +
      '" />' +
      (this.backlogEditError
        ? '<div class="ws-cmd-backlog-quick-error" role="alert">' +
          escapeHtml(this.backlogEditError) +
          '</div>'
        : '') +
      '<div class="ws-cmd-backlog-edit-actions">' +
      '<button type="submit" class="ws-cmd-drawer-action"' +
      (this.backlogEditSubmitting ? ' disabled' : '') +
      '>Save</button>' +
      '<button type="button" class="ws-cmd-backlog-quick-cancel" data-cmd-backlog-edit-cancel aria-label="Cancel edit">×</button>' +
      '</div>' +
      '</form>'
    );
  }

  // Conflict resolution (Use Ori / Use File) is deferred to a follow-up: the
  // backend (BacklogService.Conflicts/ResolveConflict, runBacklogResolveConflict
  // above) is already wired, but no UI yet lists individual conflicts here —
  // sync.conflict/sync.warning below still surface that one exists.
  backlogSyncPanelHTML() {
    const sync = this.backlogSync();
    const lastSynced =
      sync && sync.last_synced_at ? new Date(sync.last_synced_at).toLocaleString() : 'Never';
    const warning =
      sync && sync.warning
        ? '<div class="ws-cmd-backlog-sync-warning" role="alert">' +
          escapeHtml(sync.warning) +
          '</div>'
        : '';
    return (
      '<div class="ws-cmd-backlog-sync">' +
      '<span>Last synced: ' +
      escapeHtml(lastSynced) +
      '</span>' +
      '<button type="button" class="ws-cmd-backlog-sync-now" data-cmd-backlog-sync-now>Sync Now</button>' +
      warning +
      '</div>'
    );
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
        ? (workspaceId, handler) =>
            window.workspaceRealtime.subscribeToWorkspace(workspaceId, handler)
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
    if (typeof document === 'undefined' || typeof document.createElement !== 'function')
      return null;
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
    const end =
      run.phase === RUN_PHASE.SETTLED && run.lastActivityAt ? run.lastActivityAt : Date.now();
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
      fetch('/api/orchestration/tasks/' + encodeURIComponent(id) + '/cancel', {
        method: 'POST'
      }).catch(() => {});
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
    const lastActivity =
      run.activity && run.activity.length ? run.activity[run.activity.length - 1] : null;
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
          ? '<div class="ws-cmd-tray-attention">' +
            escapeHtml(String(attention)) +
            ' need attention</div>'
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
            .map(
              a =>
                '<div class="ws-cmd-tray-log-line">' +
                escapeHtml(String(a.label || a.state || '')) +
                '</div>'
            )
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
        (this._trayCancelArmed === run.taskId
          ? 'Confirm cancel “' + escapeHtml(title.slice(0, 20)) + '”'
          : 'Cancel') +
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
    // HQ stations no longer live here: they render as structures on the map
    // world surface (renderMapHQStations). The panel is back to exactly its
    // base MCP + Skills / Systems entries — one home per surface (FR6).
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

  // Data-driven HQ station registry (FR9): an ordered list of descriptors.
  // Adding a future station (Daily Brief, Follow-ups, Journal) means adding
  // one entry here — no new rendering plumbing. Watchtower comes first so the
  // HQ's cross-workspace attention signal is the most visible station.
  hqStationRegistry() {
    return [
      {
        key: 'watchtower',
        label: 'Watchtower',
        icon: 'bi-binoculars',
        state: () => this.hqWatchtowerStationState(),
        action: trigger => this.openWatchtowerPanel(trigger)
      },
      {
        key: 'email',
        label: 'Email',
        icon: 'bi-envelope',
        state: () => this.hqEmailStationState(),
        action: () => this.openHQEmailSetup()
      },
      {
        key: 'calendar-ops',
        label: 'Calendar Ops',
        icon: 'bi-calendar-check',
        state: () => this.hqCalendarOpsStationState(),
        action: trigger => this.openCalendarOpsPanel(trigger)
      }
    ];
  }

  // Email station is a portal (Mail spin-off FR15) with three states, decided at
  // render time (FR17):
  //   (c) legacy — this HQ has its own in-place email binding: keep the existing
  //       in-HQ setup behavior (existing HQs are never disturbed).
  //   (b) an Email Ops workspace exists: show its open follow-up count and
  //       navigate there on activation.
  //   (a) none: a "Set up Email Ops" CTA that deep-links the Construct wizard.
  hqEmailStationState() {
    if (this.hqHasInPlaceEmail()) {
      const hqEmail = window.OriHQEmailSetup;
      return {
        value: hqEmail.address || 'Connected',
        description: hqEmail.address || 'connected',
        tone: 'clear'
      };
    }
    const state = this.emailOpsState();
    if (state.status === 'idle' && this.active) this.requestEmailOpsData();
    switch (state.status) {
      case 'loading':
        return { value: '—', description: 'checking Email Ops', tone: 'loading' };
      case 'error':
        return { value: 'Email Ops', description: 'status unavailable', tone: 'degraded' };
      case 'ready':
        if (state.exists) {
          const count = state.openFollowupCount;
          return count
            ? {
                value: count + (count === 1 ? ' follow-up' : ' follow-ups'),
                description: state.workspaceName || 'open Email Ops',
                tone: 'attention'
              }
            : {
                value: state.workspaceName || 'Email Ops',
                description: 'open Email Ops',
                tone: 'clear'
              };
        }
        return { value: 'Set up Email Ops', description: 'no Email Ops workspace yet' };
      default:
        return { value: '—', description: 'checking Email Ops', tone: 'loading' };
    }
  }

  // hqHasInPlaceEmail reports whether this HQ itself has a connected email
  // binding (the pre-spin-off legacy setup). Only such an HQ keeps the in-HQ
  // email behavior; new HQs never do.
  hqHasInPlaceEmail() {
    const hqEmail = typeof window !== 'undefined' ? window.OriHQEmailSetup : null;
    return !!(hqEmail && hqEmail.connected);
  }

  // emailOpsState is the lazy per-HQ cache for the portal's Email Ops status.
  emailOpsState() {
    const hqWorkspaceID = this.watchtowerWorkspaceID();
    if (!this._emailOps || this._emailOps.hqWorkspaceID !== hqWorkspaceID) {
      this._emailOps = {
        hqWorkspaceID,
        status: 'idle',
        exists: false,
        workspaceID: '',
        workspaceSlug: '',
        workspaceName: '',
        openFollowupCount: 0,
        error: ''
      };
    }
    return this._emailOps;
  }

  // Fetch the user's Email Ops status once for the active HQ. A request token
  // ignores slow stale responses after a workspace switch. A failed fetch
  // degrades the badge but never breaks map rendering.
  requestEmailOpsData(force = false) {
    if (!this.isPersonalHQ()) return;
    const state = this.emailOpsState();
    if (state.status === 'loading') return;
    if (!force && state.status === 'ready') return;
    if (typeof fetch !== 'function') {
      state.status = 'error';
      state.error = 'Email Ops status is unavailable in this browser.';
      return;
    }
    state.status = 'loading';
    const requestID = (this._emailOpsRequestID || 0) + 1;
    this._emailOpsRequestID = requestID;

    Promise.resolve(fetch('/api/personal-hq/email-ops'))
      .then(async response => {
        if (!response || !response.ok) {
          throw new Error('Email Ops request failed' + (response ? ': ' + response.status : ''));
        }
        return response.json();
      })
      .then(payload => {
        if (this._emailOpsRequestID !== requestID) return;
        const st = (payload && payload.status) || {};
        const current = this.emailOpsState();
        if (current.hqWorkspaceID !== state.hqWorkspaceID) return;
        current.status = 'ready';
        current.exists = !!st.exists;
        current.workspaceID = String(st.workspace_id || '');
        current.workspaceSlug = String(st.workspace_slug || '');
        current.workspaceName = String(st.workspace_name || '');
        current.openFollowupCount = Number(st.open_followup_count || 0);
        current.error = '';
        this.refreshEmailStationSurface();
      })
      .catch(error => {
        if (this._emailOpsRequestID !== requestID) return;
        const current = this.emailOpsState();
        if (current.hqWorkspaceID !== state.hqWorkspaceID) return;
        current.status = 'error';
        current.error = (error && error.message) || 'Email Ops status unavailable';
        this.refreshEmailStationSurface();
      });
  }

  refreshEmailStationSurface() {
    if (this.active) this.render();
  }

  // Returns the active HQ workspace id without trusting a stale page-level
  // workspaceId. The Watchtower API uses this id as a server-side HQ gate.
  watchtowerWorkspaceID() {
    const ws = (this.page && this.page.workspace) || {};
    return String(ws.id || (this.page && this.page.workspaceId) || '').trim();
  }

  // Normalized in-memory state for the Watchtower's one read-only endpoint.
  // It stays scoped to the current HQ id so a page reuse can never briefly
  // render another workspace's attention queue.
  watchtowerState() {
    const workspaceID = this.watchtowerWorkspaceID();
    if (!this._watchtower || this._watchtower.workspaceID !== workspaceID) {
      this._watchtower = {
        workspaceID,
        status: 'idle',
        items: [],
        gaps: [],
        error: ''
      };
    }
    return this._watchtower;
  }

  // The station's compact state is rendered on both the Map structure and the
  // Details Stations rail. Start the fetch only while the command view is
  // active; this keeps pure render helpers side-effect free in tests.
  hqWatchtowerStationState() {
    const state = this.watchtowerState();
    if (state.status === 'idle' && this.active) this.requestWatchtowerData();
    switch (state.status) {
      case 'loading':
        return { value: 'Scanning…', description: 'loading attention queue', tone: 'loading' };
      case 'error':
        return {
          value: 'Unavailable',
          description: 'attention queue unavailable',
          tone: 'degraded'
        };
      case 'ready': {
        const count = state.items.length;
        return count
          ? {
              value: count + (count === 1 ? ' signal' : ' signals'),
              description:
                count + (count === 1 ? ' item needs attention' : ' items need attention'),
              tone: 'attention'
            }
          : {
              value: 'All clear',
              description: 'no cross-workspace attention items',
              tone: 'clear'
            };
      }
      default:
        return { value: 'Scanning…', description: 'loading attention queue', tone: 'loading' };
    }
  }

  // Fetch the bounded Watchtower projection once for the active HQ. A request
  // token ignores slow stale responses after a workspace switch or retry.
  requestWatchtowerData(force = false) {
    if (!this.isPersonalHQ()) return;
    const state = this.watchtowerState();
    if (!state.workspaceID) return;
    if (state.status === 'loading') return;
    if (!force && state.status === 'ready') return;
    if (force) {
      state.items = [];
      state.gaps = [];
      state.error = '';
    }
    if (typeof fetch !== 'function') {
      state.status = 'error';
      state.error = 'Watchtower is unavailable in this browser.';
      return;
    }

    state.status = 'loading';
    const requestID = (this._watchtowerRequestID || 0) + 1;
    this._watchtowerRequestID = requestID;
    const endpoint =
      '/api/personal-hq/watchtower?workspace_id=' + encodeURIComponent(state.workspaceID);

    Promise.resolve(fetch(endpoint))
      .then(async response => {
        if (!response || !response.ok) {
          throw new Error('Watchtower request failed' + (response ? ': ' + response.status : ''));
        }
        return response.json();
      })
      .then(payload => {
        if (this._watchtowerRequestID !== requestID) return;
        const current = this.watchtowerState();
        if (current.workspaceID !== state.workspaceID) return;
        current.status = 'ready';
        current.items = Array.isArray(payload && payload.items) ? payload.items : [];
        current.gaps = Array.isArray(payload && payload.gaps) ? payload.gaps : [];
        current.error = '';
        this.refreshWatchtowerSurface();
      })
      .catch(error => {
        if (this._watchtowerRequestID !== requestID) return;
        const current = this.watchtowerState();
        if (current.workspaceID !== state.workspaceID) return;
        current.status = 'error';
        current.error = error && error.message ? error.message : 'Watchtower could not be loaded.';
        this.refreshWatchtowerSurface();
      });
  }

  refreshWatchtowerSurface() {
    if (this.active) {
      this.render();
    } else if (this.statModalSection === 'watchtower') {
      this.renderStatModalBody();
    }
  }

  openWatchtowerPanel(trigger) {
    const state = this.watchtowerState();
    this.requestWatchtowerData(state.status === 'error');
    this.openStatModal('watchtower', trigger);
  }

  // Calendar Ops station (task 7.4): same bounded-summary contract as the
  // Home portal (FR50/FR51), rendered here as an HQ Map station + Stations
  // rail entry. Scoped per-HQ like Watchtower so a workspace switch never
  // briefly shows another HQ's calendar summary.
  calendarOpsPortalState() {
    const workspaceID = this.watchtowerWorkspaceID();
    if (!this._calendarOpsPortal || this._calendarOpsPortal.workspaceID !== workspaceID) {
      this._calendarOpsPortal = {
        workspaceID,
        status: 'idle',
        hasWorkspace: false,
        calendarWorkspaceID: '',
        calendarWorkspaceSlug: '',
        state: '',
        nextMeeting: null,
        eventCount: 0,
        conflictCount: 0,
        dataGap: false,
        error: ''
      };
    }
    return this._calendarOpsPortal;
  }

  hqCalendarOpsStationState() {
    const state = this.calendarOpsPortalState();
    if (state.status === 'idle' && this.active) this.requestCalendarOpsPortalData();
    switch (state.status) {
      case 'loading':
        return { value: 'Loading…', description: 'loading calendar summary', tone: 'loading' };
      case 'error':
        return {
          value: 'Unavailable',
          description: 'calendar summary unavailable',
          tone: 'degraded'
        };
      case 'ready':
        return this.calendarOpsStationReadyState(state);
      default:
        return { value: 'Loading…', description: 'loading calendar summary', tone: 'loading' };
    }
  }

  calendarOpsStationReadyState(state) {
    if (!state.hasWorkspace) {
      return { value: 'Set up', description: 'Calendar Ops is not set up yet', tone: 'attention' };
    }
    if (state.state !== 'ready') {
      return {
        value: 'Finish setup',
        description: 'calendar connector needs attention',
        tone: 'attention'
      };
    }
    if (state.nextMeeting && state.nextMeeting.title) {
      const conflictNote = state.conflictCount
        ? ', ' + state.conflictCount + (state.conflictCount === 1 ? ' conflict' : ' conflicts')
        : '';
      return {
        value: state.nextMeeting.title,
        description: calendarOpsMeetingTimeLabel(state.nextMeeting) + conflictNote,
        tone: state.dataGap || state.conflictCount ? 'attention' : 'clear'
      };
    }
    return {
      value: state.dataGap ? 'Degraded' : 'Clear',
      description: state.eventCount ? state.eventCount + ' events today' : 'no events today',
      tone: state.dataGap ? 'degraded' : 'clear'
    };
  }

  requestCalendarOpsPortalData(force = false) {
    if (!this.isPersonalHQ()) return;
    const state = this.calendarOpsPortalState();
    if (!state.workspaceID) return;
    if (state.status === 'loading') return;
    if (!force && state.status === 'ready') return;
    if (typeof fetch !== 'function') {
      state.status = 'error';
      state.error = 'Calendar Ops portal is unavailable in this browser.';
      return;
    }

    state.status = 'loading';
    const requestID = (this._calendarOpsPortalRequestID || 0) + 1;
    this._calendarOpsPortalRequestID = requestID;

    Promise.resolve(fetch('/api/calendar-ops/home-portal-summary'))
      .then(async response => {
        if (!response || !response.ok) {
          throw new Error(
            'Calendar Ops portal request failed' + (response ? ': ' + response.status : '')
          );
        }
        return response.json();
      })
      .then(payload => {
        if (this._calendarOpsPortalRequestID !== requestID) return;
        const current = this.calendarOpsPortalState();
        if (current.workspaceID !== state.workspaceID) return;
        current.status = 'ready';
        current.hasWorkspace = !!(payload && payload.has_workspace);
        current.calendarWorkspaceID = String((payload && payload.workspace_id) || '');
        current.calendarWorkspaceSlug = String((payload && payload.workspace_slug) || '');
        current.state = String((payload && payload.state) || '');
        current.nextMeeting = (payload && payload.next_meeting) || null;
        current.eventCount = Number((payload && payload.event_count) || 0);
        current.conflictCount = Number((payload && payload.conflict_count) || 0);
        current.dataGap = !!(payload && payload.data_gap);
        current.error = '';
        this.refreshCalendarOpsPortalSurface();
      })
      .catch(error => {
        if (this._calendarOpsPortalRequestID !== requestID) return;
        const current = this.calendarOpsPortalState();
        if (current.workspaceID !== state.workspaceID) return;
        current.status = 'error';
        current.error =
          error && error.message ? error.message : 'Calendar Ops portal could not be loaded.';
        this.refreshCalendarOpsPortalSurface();
      });
  }

  refreshCalendarOpsPortalSurface() {
    if (this.active) {
      this.render();
    } else if (this.statModalSection === 'calendar-ops') {
      this.renderStatModalBody();
    }
  }

  openCalendarOpsPanel(trigger) {
    const state = this.calendarOpsPortalState();
    this.requestCalendarOpsPortalData(state.status === 'error');
    this.openStatModal('calendar-ops', trigger);
  }

  // Routes the "no Calendar Ops workspace yet" CTA into the existing
  // Construct wizard with the Calendar Ops blueprint preselected (FR52/task
  // 7.5) -- sessionManager.showAddWorkspaceModal is the one workspace-
  // creation entry point; this never creates a workspace inline.
  openCalendarOpsConstruct() {
    if (
      typeof window === 'undefined' ||
      !window.sessionManager ||
      typeof window.sessionManager.showAddWorkspaceModal !== 'function'
    ) {
      return;
    }
    window.sessionManager.showAddWorkspaceModal({
      entryPoint: 'calendar_ops_portal',
      blueprint: 'calendar-ops'
    });
  }

  calendarOpsPanelHTML() {
    const state = this.calendarOpsPortalState();
    let content = '';
    if (state.status === 'loading' || state.status === 'idle') {
      content = this.modalEmptyHTML('Loading your calendar summary…');
    } else if (state.status === 'error') {
      content =
        '<div class="ws-cmd-watchtower-degraded" role="status"><i class="bi bi-exclamation-diamond" aria-hidden="true"></i><p>Calendar Ops summary could not refresh.</p><button type="button" data-cmd-modal-action="refresh-calendar-ops">Retry</button></div>';
    } else if (!state.hasWorkspace) {
      content =
        '<div class="ws-cmd-watchtower-all-clear"><i class="bi bi-calendar-plus" aria-hidden="true"></i><strong>Set up Calendar Ops</strong><span>Connect a calendar to see your day at a glance.</span><button type="button" data-cmd-modal-action="calendar-ops-setup">Set up Calendar Ops</button></div>';
    } else if (state.state !== 'ready') {
      content =
        '<div class="ws-cmd-watchtower-degraded" role="status"><i class="bi bi-exclamation-diamond" aria-hidden="true"></i><p>Calendar Ops setup needs attention.</p><button type="button" data-cmd-modal-action="calendar-ops-open">Finish setup</button></div>';
    } else {
      const meetingHTML =
        state.nextMeeting && state.nextMeeting.title
          ? '<div class="ws-cmd-calendar-ops-next"><strong>' +
            escapeHtml(state.nextMeeting.title) +
            '</strong><span>' +
            escapeHtml(calendarOpsMeetingTimeLabel(state.nextMeeting)) +
            '</span></div>'
          : '<div class="ws-cmd-calendar-ops-next"><strong>No more meetings today</strong></div>';
      const gapHTML = state.dataGap
        ? '<aside class="ws-cmd-watchtower-gaps" role="status"><strong>Partial signal</strong><span>Some calendars could not be read.</span></aside>'
        : '';
      content =
        meetingHTML +
        '<div class="ws-cmd-calendar-ops-stats"><span>' +
        state.eventCount +
        (state.eventCount === 1 ? ' event today' : ' events today') +
        '</span><span>' +
        state.conflictCount +
        (state.conflictCount === 1 ? ' conflict' : ' conflicts') +
        '</span></div>' +
        gapHTML +
        '<button type="button" data-cmd-modal-action="calendar-ops-open">Open Calendar Ops</button>';
    }
    return (
      '<header class="ws-cmd-modal-head"><div><h3 class="ws-cmd-modal-title">Calendar Ops</h3></div><div class="ws-cmd-modal-head-actions"><button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close Calendar Ops">×</button></div></header><div class="ws-cmd-modal-body">' +
      content +
      '</div>'
    );
  }

  watchtowerRelativeTime(timestamp) {
    const when = Date.parse(String(timestamp || ''));
    if (!Number.isFinite(when)) return 'time unknown';
    const seconds = Math.max(0, Math.floor((Date.now() - when) / 1000));
    if (seconds < 60) return 'just now';
    if (seconds < 3600) return Math.floor(seconds / 60) + 'm ago';
    if (seconds < 86400) return Math.floor(seconds / 3600) + 'h ago';
    if (seconds < 604800) return Math.floor(seconds / 86400) + 'd ago';
    return new Date(when).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }

  watchtowerSeverityIcon(severity) {
    switch (String(severity || '').toLowerCase()) {
      case 'failed':
      case 'timeout':
        return 'bi-x-octagon-fill';
      case 'critical':
        return 'bi-exclamation-triangle-fill';
      case 'waiting_for_choice':
        return 'bi-question-diamond-fill';
      case 'scheduled_failure':
        return 'bi-clock-history';
      case 'high':
        return 'bi-arrow-up-circle-fill';
      default:
        return 'bi-dot';
    }
  }

  watchtowerGroups(items) {
    const groups = [];
    const byWorkspace = new Map();
    (Array.isArray(items) ? items : []).forEach(item => {
      const workspaceID = String(item && item.workspace_id ? item.workspace_id : '').trim();
      const workspaceName =
        String((item && item.workspace_name) || 'Workspace').trim() || 'Workspace';
      const key = workspaceID || workspaceName;
      if (!byWorkspace.has(key)) {
        const group = { workspaceID, workspaceName, items: [] };
        byWorkspace.set(key, group);
        groups.push(group);
      }
      byWorkspace.get(key).items.push(item || {});
    });
    return groups;
  }

  watchtowerItemHTML(item) {
    const workspaceSlug = String(item.workspace_slug || '').trim();
    const severity = String(item.severity || 'attention').trim();
    const title = String(item.title || 'Attention item').trim() || 'Attention item';
    const description = String(item.description || '').trim();
    const descriptionHTML =
      description && description !== title
        ? '<span class="ws-cmd-watchtower-item-detail">' + escapeHtml(description) + '</span>'
        : '';
    return (
      '<button type="button" class="ws-cmd-watchtower-item is-' +
      escapeHtml(severity.toLowerCase()) +
      '" data-cmd-modal-action="open-watchtower-workspace" data-cmd-id="' +
      escapeHtml(workspaceSlug) +
      '" aria-label="Open ' +
      escapeHtml(title) +
      ' in ' +
      escapeHtml(String(item.workspace_name || 'workspace')) +
      '"><span class="ws-cmd-watchtower-item-icon"><i class="bi ' +
      escapeHtml(this.watchtowerSeverityIcon(severity)) +
      '" aria-hidden="true"></i></span><span class="ws-cmd-watchtower-item-main"><span class="ws-cmd-watchtower-item-title">' +
      escapeHtml(title) +
      '</span>' +
      descriptionHTML +
      '</span><time class="ws-cmd-watchtower-item-time" datetime="' +
      escapeHtml(String(item.timestamp || '')) +
      '">' +
      escapeHtml(this.watchtowerRelativeTime(item.timestamp)) +
      '</time></button>'
    );
  }

  watchtowerPanelHTML() {
    const state = this.watchtowerState();
    let content = '';
    if (state.status === 'loading' || state.status === 'idle') {
      content = this.modalEmptyHTML('Scanning workspaces for attention signals…');
    } else if (state.status === 'error') {
      content =
        '<div class="ws-cmd-watchtower-degraded" role="status"><i class="bi bi-exclamation-diamond" aria-hidden="true"></i><p>Watchtower could not refresh. The map remains available.</p><button type="button" data-cmd-modal-action="refresh-watchtower">Retry scan</button></div>';
    } else if (!state.items.length) {
      content =
        '<div class="ws-cmd-watchtower-all-clear"><i class="bi bi-shield-check" aria-hidden="true"></i><strong>All clear</strong><span>No waiting decisions, failed runs, or high-priority findings across your workspaces.</span></div>';
    } else {
      content = this.watchtowerGroups(state.items)
        .map(
          group =>
            '<section class="ws-cmd-watchtower-group"><header><span>' +
            escapeHtml(group.workspaceName) +
            '</span><strong>' +
            group.items.length +
            '</strong></header><div class="ws-cmd-watchtower-items">' +
            group.items.map(item => this.watchtowerItemHTML(item)).join('') +
            '</div></section>'
        )
        .join('');
    }
    const gaps = state.gaps.length
      ? '<aside class="ws-cmd-watchtower-gaps" role="status"><strong>Partial signal</strong><ul>' +
        state.gaps.map(gap => '<li>' + escapeHtml(gap) + '</li>').join('') +
        '</ul></aside>'
      : '';
    return (
      '<header class="ws-cmd-modal-head is-watchtower"><div><h3 class="ws-cmd-modal-title">Watchtower</h3><span class="ws-cmd-watchtower-kicker">Cross-workspace attention queue</span></div><span class="ws-cmd-modal-count">' +
      (state.status === 'ready' ? state.items.length : '—') +
      '</span><div class="ws-cmd-modal-head-actions"><button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close Watchtower">×</button></div></header><div class="ws-cmd-modal-body ws-cmd-watchtower-body">' +
      gaps +
      content +
      '</div>'
    );
  }

  // Default map slot for a station with no saved position (FR7): a
  // deterministic stack down the field's right edge, in registry order. Values
  // are fractional (0–1) so first render needs no persistence and survives any
  // viewport size; the structure is centered on the point in CSS
  // (translate(-50%,-50%)), so x≈0.9 keeps it clear of the right padding.
  hqStationDefaultPosition(index) {
    const slot = index < 0 ? 0 : index;
    return { x: 0.9, y: clampFraction(0.22 + slot * 0.17) };
  }

  // Resolved fractional position for a station (FR11): the saved position from
  // the workspace layout, clamped to [0,1] so a stale/corrupt value can never
  // render a station off-field, else the registry-order default slot. Unknown
  // or non-finite saved values fall back to the default (FR13).
  hqStationPosition(key) {
    const registry = this.mapStationRegistry();
    const index = registry.findIndex(entry => entry.key === key);
    const fallback = this.hqStationDefaultPosition(index);
    const layout = (this.page && this.page.workspace && this.page.workspace.layout) || null;
    const saved = layout && layout.station_positions ? layout.station_positions[key] : null;
    if (!saved) return fallback;
    if (!Number.isFinite(Number(saved.x)) || !Number.isFinite(Number(saved.y))) {
      return fallback;
    }
    return { x: clampFraction(saved.x), y: clampFraction(saved.y) };
  }

  // Renders HQ stations as freestanding structures on the command map's world
  // surface (FR6, FR8), replacing their old home inside the Stations panel.
  // Each is a real <button> (keyboard-focusable, state-bearing aria-label,
  // FR8) absolutely positioned from its fractional coordinate via the
  // --station-x/--station-y custom props. Only the designated HQ renders
  // these; non-HQ workspaces get an empty string (FR16).
  renderMapHQStations() {
    return this.mapStationRegistry()
      .map(station => {
        const state = station.state() || {};
        const pos = this.hqStationPosition(station.key);
        const icon = station.icon
          ? '<i class="bi ' + escapeHtml(station.icon) + '" aria-hidden="true"></i>'
          : '';
        return (
          '<button type="button" class="ws-cmd-map-hq-station' +
          (state.tone ? ' is-' + escapeHtml(state.tone) : '') +
          '" data-cmd-hq-station="' +
          escapeHtml(station.key) +
          '" style="--station-x:' +
          (pos.x * 100).toFixed(2) +
          '%;--station-y:' +
          (pos.y * 100).toFixed(2) +
          '%" aria-label="' +
          escapeHtml(station.label) +
          ' station, ' +
          escapeHtml(state.description || '') +
          '"><span class="ws-cmd-map-hq-station-icon">' +
          icon +
          '</span><span class="ws-cmd-map-hq-station-label">' +
          escapeHtml(station.label) +
          '</span><span class="ws-cmd-map-hq-station-state">' +
          escapeHtml(state.value || '') +
          '</span></button>'
        );
      })
      .join('');
  }

  // Stations a workspace earns from what it *is*, rather than from being the
  // Personal HQ. A File Janitor workspace awaiting setup is otherwise
  // indistinguishable from a finished one on the map: its setup card lives in
  // the page body, so someone working on the map surface has no way to know a
  // folder is still needed.
  //
  // Presence comes from the persisted install record via the capability
  // catalog — never from the workspace's name, template, folder, or agents
  // (FR-93, FR-94). Status text comes from the catalog's derived health, so the
  // station and the catalog can never disagree.
  workspaceStationRegistry() {
    const stations = [];
    const context = { page: this.page, workspace: (this.page && this.page.workspace) || {} };
    const adapters =
      typeof window === 'undefined' || !Array.isArray(window.WorkspaceBuiltinStationAdapters)
        ? []
        : window.WorkspaceBuiltinStationAdapters;
    adapters.forEach(adapter => {
      if (!adapter || typeof adapter.station !== 'function') return;
      const station = adapter.station(context);
      if (station && typeof station.key === 'string') stations.push(station);
    });

    const surfaceHost = typeof window === 'undefined' ? null : window.WorkspaceSurfaceHost;
    if (surfaceHost && typeof surfaceHost.stations === 'function') {
      const contributed = surfaceHost.stations();
      if (Array.isArray(contributed)) stations.push(...contributed);
    }
    return stations;
  }

  // Every station on this workspace's map, whatever its source. Keyed lookups
  // (position, dispatch) go through here so a workspace station behaves exactly
  // like an HQ one without duplicating the rendering plumbing.
  mapStationRegistry() {
    return [
      ...(this.isPersonalHQ() ? this.hqStationRegistry() : []),
      ...this.workspaceStationRegistry()
    ];
  }

  // Dispatches a click on a station button to its registry action.
  runHQStationAction(stationKey, trigger) {
    const station = this.mapStationRegistry().find(entry => entry.key === stationKey);
    if (station && typeof station.action === 'function') station.action(trigger);
  }

  // HQ-gated "Stations" rail panel for Details mode (FR14): one row per
  // registry entry (label + live state meta from the same state fn as the map
  // structure), plus a primary action that runs the first station's action.
  // Rows and the primary button carry data-cmd-hq-station so the rail click
  // delegation dispatches through runHQStationAction — the same registry
  // action() as the map surface. Non-HQ workspaces render nothing (FR16).
  renderStationsRailPanel() {
    const registry = this.mapStationRegistry();
    if (!registry.length) return '';
    const first = registry[0];
    const firstState = (first.state && first.state()) || {};
    const rows = registry
      .map(station => {
        const state = (station.state && station.state()) || {};
        return (
          '<button type="button" class="ws-cmd-rail-item" data-cmd-hq-station="' +
          escapeHtml(station.key) +
          '" aria-label="' +
          escapeHtml(station.label) +
          ' station, ' +
          escapeHtml(state.description || '') +
          '"><span class="ws-cmd-rail-t">' +
          escapeHtml(station.label) +
          '</span><span class="ws-cmd-rail-m">' +
          escapeHtml(state.value || '') +
          '</span></button>'
        );
      })
      .join('');
    return (
      '<section class="ws-cmd-panel is-hq-stations">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>Stations</h4><span class="ws-cmd-panel-count">' +
      registry.length +
      '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-hq-station="' +
      escapeHtml(first.key) +
      '">' +
      escapeHtml(firstState.value || 'Open') +
      '</button>' +
      '</div>' +
      '</div>' +
      '<div class="ws-cmd-panel-body">' +
      rows +
      '</div>' +
      '</section>'
    );
  }

  // --- Station drag-to-place (group 3) --------------------------------------
  // Pure drag math, kept as methods so they are directly unit-testable.

  // Whether pointer travel (dx,dy) from the press origin is far enough to
  // count as a drag rather than a click (FR9).
  stationDragExceedsThreshold(dx, dy) {
    return Math.hypot(Number(dx) || 0, Number(dy) || 0) > STATION_DRAG_THRESHOLD_PX;
  }

  // Convert a client point to a fractional (0–1) coordinate inside a field
  // rect, clamped so a drop can never land a station off-field (FR10/FR11).
  stationPointToFraction(clientX, clientY, rect) {
    if (!rect || !rect.width || !rect.height) return { x: 0, y: 0 };
    return {
      x: clampFraction((clientX - rect.left) / rect.width),
      y: clampFraction((clientY - rect.top) / rect.height)
    };
  }

  // Pointer-event drag on the HQ station structures (FR9–FR11). Bound once per
  // map render on the map shell root; the per-gesture move/up listeners live
  // on window so a fast drag that outruns the element still tracks. No
  // render() runs during an active drag — the drop path persists then
  // re-renders (see persistStationPosition + the render() guard).
  bindStationDrag(root) {
    root.addEventListener('pointerdown', event => {
      if (event.button != null && event.button !== 0) return;
      const el = event.target.closest('[data-cmd-hq-station]');
      if (!el || !root.contains(el)) return;
      // Fresh interaction: clear any stale click-suppression from a prior drag.
      this._suppressStationClick = false;
      const drag = {
        key: el.getAttribute('data-cmd-hq-station'),
        el,
        startX: event.clientX,
        startY: event.clientY,
        pointerId: event.pointerId,
        moved: false,
        lastFraction: null
      };
      this._stationDrag = drag;

      const onMove = moveEvent => this.handleStationPointerMove(moveEvent);
      const onUp = upEvent => {
        window.removeEventListener('pointermove', onMove);
        window.removeEventListener('pointerup', onUp);
        window.removeEventListener('pointercancel', onUp);
        this.handleStationPointerUp(upEvent);
      };
      window.addEventListener('pointermove', onMove);
      window.addEventListener('pointerup', onUp);
      window.addEventListener('pointercancel', onUp);
    });
  }

  handleStationPointerMove(event) {
    const drag = this._stationDrag;
    if (!drag) return;
    if (!drag.moved) {
      if (
        !this.stationDragExceedsThreshold(event.clientX - drag.startX, event.clientY - drag.startY)
      ) {
        return;
      }
      // Cross the threshold once: enter drag mode. The .is-dragging class is a
      // static style swap (raised z-index, grabbing cursor, lift shadow) so it
      // is already prefers-reduced-motion safe.
      drag.moved = true;
      this._stationDragActive = true;
      drag.el.classList.add('is-dragging');
      try {
        drag.el.setPointerCapture(drag.pointerId);
      } catch (_err) {
        /* capture is best-effort */
      }
    }
    const world = this.container && this.container.querySelector('.ws-cmd-map-world');
    if (!world || typeof world.getBoundingClientRect !== 'function') return;
    const frac = this.stationPointToFraction(
      event.clientX,
      event.clientY,
      world.getBoundingClientRect()
    );
    drag.lastFraction = frac;
    // Move the structure directly via its custom props — no re-render.
    drag.el.style.setProperty('--station-x', (frac.x * 100).toFixed(2) + '%');
    drag.el.style.setProperty('--station-y', (frac.y * 100).toFixed(2) + '%');
  }

  handleStationPointerUp(event) {
    const drag = this._stationDrag;
    this._stationDrag = null;
    if (!drag) return;
    if (!drag.moved) {
      // Under-threshold press: not a drag — let the click handler open the
      // station's action (FR9).
      return;
    }
    this._stationDragActive = false;
    // Suppress the click the browser dispatches after this pointerup so the
    // gesture never fires the station action (FR9).
    this._suppressStationClick = true;
    drag.el.classList.remove('is-dragging');
    try {
      drag.el.releasePointerCapture(drag.pointerId);
    } catch (_err) {
      /* capture may already be gone */
    }
    if (event && event.type === 'pointercancel') {
      // Interrupted mid-drag: discard the in-progress move and restore the
      // committed position by re-rendering.
      this.render();
      return;
    }
    if (!drag.lastFraction) {
      this.render();
      return;
    }
    this.persistStationPosition(drag.key, drag.lastFraction);
  }

  // Optimistically commit a station's new position to the local model and
  // re-render, then persist it through the scoped station-layout endpoint. A
  // failed save keeps the session-local position and warns (FR10).
  async persistStationPosition(key, fraction) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const ws = (page && page.workspace) || null;
    if (!ws) {
      this.render();
      return;
    }
    const workspaceId = ws.id || (page && page.workspaceId) || '';
    if (!ws.layout) ws.layout = {};
    if (!ws.layout.station_positions) ws.layout.station_positions = {};
    ws.layout.station_positions[key] = { x: fraction.x, y: fraction.y };
    // Re-render now that the model carries the new position (this also clears
    // the drag-active guard's effect for subsequent renders).
    this.render();

    try {
      const resp = await fetch('/api/orchestration/workspace/station-layout', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: workspaceId,
          station_positions: ws.layout.station_positions
        })
      });
      if (!resp || !resp.ok) {
        throw new Error('station-layout save failed: ' + (resp ? resp.status : 'no response'));
      }
    } catch (_err) {
      if (typeof window !== 'undefined' && window.Toast) {
        window.Toast.error('Could not save station position; it will reset on reload.');
      }
    }
  }

  // A single agent "unit" card. `commandNode` renders the entry agent as the
  // larger, role-framed command node (FR66-67, FR70); otherwise a standard
  // specialist card. Runtime tone classes (working/waiting/needs-input/done)
  // apply identically to both, so status color never depends on role (FR40).
  renderMapAgentUnit(agent, index, commandNode) {
    const selected = agent.key === this.selectedAgentKey;
    const destination = agent.destination || 'hub';
    const statusLabel = agent.status?.label || 'Idle';
    const commanderLbl = agent.entry
      ? this.commanderLabel(agent.profile && agent.profile.role)
      : '';
    // Station title (design consideration): pair role with the workspace it's
    // stationed in, e.g. "Commander · Production" — this is how
    // domain-specialized identity is displayed without existing in the catalog.
    const wsName = String(
      (this.page && this.page.workspace && this.page.workspace.name) || ''
    ).trim();
    const stationTitle = commanderLbl && wsName ? commanderLbl + ' · ' + wsName : commanderLbl;
    const entryBadge =
      agent.entry && !commandNode
        ? '<span class="ws-cmd-map-entry-badge" title="' +
          escapeHtml(stationTitle) +
          '"><i class="bi bi-star-fill" aria-hidden="true"></i><span>' +
          escapeHtml(commanderLbl) +
          '</span></span>'
        : '';
    const roleLine = commandNode
      ? '<span class="ws-cmd-map-command-role" title="' +
        escapeHtml(stationTitle) +
        '">' +
        escapeHtml(commanderLbl) +
        '</span>'
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
      (agent.entry ? escapeHtml(stationTitle) + '. ' : '') +
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
    return (
      'Routes work to ' + specialistCount + ' specialist agent' + (specialistCount === 1 ? '' : 's')
    );
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
        '<strong>No Commander</strong>' +
        '<span>Chats, routing, and task orchestration need a Commander.</span>' +
        '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-agent>Create Commander</button>' +
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
      this.renderMapHQStations() +
      '</section>'
    );
  }

  emptyCapabilityInspectorState() {
    return {
      open: false,
      kind: '',
      bindingId: '',
      name: '',
      agent: '',
      encodedAgent: '',
      activeTab: 'overview',
      status: 'idle',
      data: null,
      error: '',
      assigned: false,
      locked: false,
      originKey: ''
    };
  }

  capabilityOriginKey(kind, bindingId, encodedAgent) {
    return encodeURIComponent(
      [String(kind || ''), String(bindingId || ''), String(encodedAgent || '')].join(':')
    );
  }

  resetCapabilityInspector() {
    this.capabilityInspectorRequestId = Number(this.capabilityInspectorRequestId || 0) + 1;
    this.capabilityInspector = this.emptyCapabilityInspectorState();
    this.pendingCapabilityInspectorFocus = '';
  }

  capabilityInspectorMatches(kind, bindingId, encodedAgent) {
    const inspector = this.capabilityInspector || {};
    return (
      inspector.open === true &&
      inspector.kind === kind &&
      inspector.bindingId === String(bindingId || '') &&
      inspector.encodedAgent === String(encodedAgent || '')
    );
  }

  rememberCapabilityInspectorFocus() {
    if (this.pendingCapabilityInspectorFocus || !this.capabilityInspector?.open) return;
    const activeElement = typeof document === 'undefined' ? null : document.activeElement;
    if (!activeElement || typeof activeElement.closest !== 'function') return;
    const inspector = activeElement.closest('[data-cmd-capability-inspector]');
    if (!inspector) return;
    if (activeElement.closest('[data-cmd-capability-back]')) {
      this.pendingCapabilityInspectorFocus = 'back';
      return;
    }
    const tab = activeElement.closest('[data-cmd-capability-tab]');
    if (tab) {
      this.pendingCapabilityInspectorFocus =
        'tab:' + String(tab.getAttribute('data-cmd-capability-tab') || 'overview');
      return;
    }
    if (activeElement.closest('[data-cmd-capability-retry]')) {
      this.pendingCapabilityInspectorFocus = 'retry';
      return;
    }
    if (activeElement.closest('[data-cmd-capability-start]')) {
      this.pendingCapabilityInspectorFocus = 'start';
    }
  }

  restoreCapabilityInspectorFocus() {
    const targetKey = String(this.pendingCapabilityInspectorFocus || '');
    if (!targetKey || !this.container || typeof this.container.querySelector !== 'function') return;
    this.pendingCapabilityInspectorFocus = '';
    let selector = '';
    if (targetKey === 'back') selector = '[data-cmd-capability-back]';
    else if (targetKey === 'retry') selector = '[data-cmd-capability-retry]';
    else if (targetKey === 'start') selector = '[data-cmd-capability-start]';
    else if (targetKey.startsWith('tab:')) {
      selector = '[data-cmd-capability-tab="' + targetKey.slice(4) + '"]';
    } else if (targetKey.startsWith('origin:')) {
      selector = '[data-cmd-capability-origin="' + targetKey.slice(7) + '"]';
    } else if (targetKey.startsWith('switch:')) {
      selector = '[data-cmd-capability-switch="' + targetKey.slice(7) + '"]';
    }
    const target = selector ? this.container.querySelector(selector) : null;
    if (target && typeof target.focus === 'function') {
      try {
        target.focus({ preventScroll: true });
      } catch (_error) {
        target.focus();
      }
    }
  }

  // The method name, the CSS class, and the tab key stay "loadout" — they are
  // internal identifiers, and renaming them would churn selectors and saved tab
  // state for no user-visible gain. Only the label a person reads changes
  // (FR-168).
  renderMapAgentLoadout(agent) {
    return (
      '<section class="ws-cmd-rpg-loadout is-editable"><span>Toolbox</span>' +
      this.renderLoadoutEditor(agent) +
      '</section>'
    );
  }

  renderMapAgentModelCard(agent) {
    const page = this.page || {};
    const modelLabel = agent?.model?.empty
      ? 'Model not set'
      : agent?.model?.label || agent?.model?.model || 'Model not set';
    const editable =
      typeof page.agentAllowsModelEditing === 'function' &&
      page.agentAllowsModelEditing(agent?.profile);
    const contents =
      '<span>Model</span><strong translate="no">' +
      escapeHtml(modelLabel) +
      '</strong>' +
      (editable
        ? '<small aria-hidden="true"><i class="bi bi-pencil" aria-hidden="true"></i>' +
          (agent?.model?.empty ? 'Set model' : 'Change') +
          '</small>'
        : '');

    if (!editable) {
      return '<div class="ws-cmd-rpg-class-card">' + contents + '</div>';
    }

    const actionLabel = agent?.model?.empty
      ? 'Set model for ' + agent.name
      : 'Change model for ' + agent.name + '. Current model: ' + modelLabel;
    return (
      '<button type="button" class="ws-cmd-rpg-class-card is-editable" data-cmd-edit-model="' +
      escapeHtml(agent.encodedName) +
      '" aria-label="' +
      escapeHtml(actionLabel) +
      '">' +
      contents +
      '</button>'
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
      (this.loadoutError && !this.loadoutAddOpen
        ? '<p class="ws-cmd-loadout-editor-error" role="alert">' +
          escapeHtml(this.loadoutError) +
          '</p>'
        : '') +
      '</div>'
    );
  }

  loadoutSectionHTML(kind, label, items, encodedAgent) {
    const list = Array.isArray(items) ? items : [];
    const capabilityType = kind === 'mcp' ? 'MCP server' : 'skill';
    const rows = list.length
      ? list
          .map(item => {
            const bindingId = String(item?.bindingId || '');
            const name = String(item?.name || '').trim();
            const originKey = this.capabilityOriginKey(kind, bindingId, encodedAgent);
            const busy = this.loadoutBusyKey === kind + ':' + bindingId;
            const selected = this.capabilityInspectorMatches(kind, bindingId, encodedAgent);
            const inspectLabel = `Inspect ${capabilityType} ${name}`;
            const assignment = item.locked
              ? '<span class="ws-cmd-loadout-lock" title="Always available in this workspace"><i class="bi bi-lock-fill" aria-hidden="true"></i> Locked</span>'
              : '<button type="button" class="ws-cmd-loadout-switch ' +
                (item.enabled ? 'is-on' : 'is-off') +
                (busy ? ' is-busy' : '') +
                '" role="switch" aria-checked="' +
                (item.enabled ? 'true' : 'false') +
                '" aria-label="' +
                escapeHtml(
                  `${item.enabled ? 'Remove' : 'Assign'} ${capabilityType} ${name} ${
                    item.enabled ? 'from' : 'to'
                  } ${this.decodeAgentName(encodedAgent)}`
                ) +
                '" data-cmd-loadout-toggle="' +
                escapeHtml(kind) +
                '" data-cmd-loadout-binding="' +
                escapeHtml(bindingId) +
                '" data-cmd-loadout-name="' +
                escapeHtml(name) +
                '" data-cmd-loadout-agent="' +
                escapeHtml(encodedAgent) +
                '" data-cmd-capability-switch="' +
                escapeHtml(originKey) +
                '"' +
                (busy ? ' disabled' : '') +
                '><span aria-hidden="true"></span></button>';
            return (
              '<div class="ws-cmd-loadout-row' +
              (selected ? ' is-selected' : '') +
              (item.locked ? ' is-locked' : '') +
              '">' +
              '<button type="button" class="ws-cmd-loadout-inspect" data-cmd-capability-inspect="' +
              escapeHtml(kind) +
              '" data-cmd-loadout-binding="' +
              escapeHtml(bindingId) +
              '" data-cmd-loadout-name="' +
              escapeHtml(name) +
              '" data-cmd-loadout-agent="' +
              escapeHtml(encodedAgent) +
              '" data-cmd-loadout-enabled="' +
              (item.enabled ? 'true' : 'false') +
              '" data-cmd-loadout-locked="' +
              (item.locked ? 'true' : 'false') +
              '" data-cmd-capability-origin="' +
              escapeHtml(originKey) +
              '" aria-label="' +
              escapeHtml(`${inspectLabel} for ${this.decodeAgentName(encodedAgent)}`) +
              '"><span class="ws-cmd-loadout-row-icon"><i class="bi ' +
              (kind === 'mcp' ? 'bi-hdd-network' : 'bi-stars') +
              '" aria-hidden="true"></i></span><span class="ws-cmd-loadout-row-copy"><strong>' +
              escapeHtml(name) +
              '</strong><small>' +
              escapeHtml(capabilityType) +
              ' · View details</small></span></button>' +
              assignment +
              '</div>'
            );
          })
          .join('')
      : '<span class="ws-cmd-loadout-empty">None bound to this workspace.</span>';
    const addOpen = this.loadoutAddOpen === kind && this.loadoutAddAgent === encodedAgent;
    const originKey = this.loadoutAddOriginKey(kind, encodedAgent);
    return (
      '<section class="ws-cmd-loadout-editor-section">' +
      '<header><span class="ws-cmd-loadout-kicker">' +
      escapeHtml(kind === 'mcp' ? 'MCP Servers' : label) +
      '</span><button type="button" class="ws-cmd-loadout-add-btn' +
      (addOpen ? ' is-open' : '') +
      '" data-cmd-loadout-add="' +
      escapeHtml(kind) +
      '" data-cmd-loadout-agent="' +
      escapeHtml(encodedAgent) +
      '" data-cmd-loadout-add-origin="' +
      escapeHtml(originKey) +
      '" aria-haspopup="dialog" aria-expanded="' +
      (addOpen ? 'true' : 'false') +
      '">' +
      (kind === 'mcp' ? 'Add Tool' : 'Add Skill') +
      '</button></header>' +
      '<div class="ws-cmd-loadout-rows">' +
      rows +
      '</div></section>'
    );
  }

  loadoutAddModalHTML(encodedAgent) {
    const kind = this.loadoutAddOpen;
    if (!['skill', 'mcp'].includes(kind) || this.loadoutAddAgent !== encodedAgent) return '';
    const isMCP = kind === 'mcp';
    const noun = isMCP ? 'Tool' : 'Skill';
    const agentName = this.decodeAgentName(encodedAgent);
    const titleId = 'ws-cmd-loadout-add-title';
    const descriptionId = 'ws-cmd-loadout-add-description';
    let content = '';
    if (this.loadoutAddLoading) {
      content =
        '<div class="ws-cmd-loadout-picker-state" role="status" aria-live="polite"><span aria-hidden="true"></span>Loading available ' +
        (isMCP ? 'tools' : 'skills') +
        '…</div>';
    } else {
      const options = Array.isArray(this.loadoutAddOptions) ? this.loadoutAddOptions : [];
      content = options.length
        ? '<div class="ws-cmd-loadout-picker" role="list" aria-label="Available ' +
          (isMCP ? 'tools' : 'skills') +
          '">' +
          options
            .map(name => {
              const busy = this.loadoutBusyKey === kind + ':add:' + name;
              return (
                '<button type="button" class="ws-cmd-loadout-picker-item' +
                (busy ? ' is-busy' : '') +
                '" data-cmd-loadout-bind="' +
                escapeHtml(kind) +
                '" data-cmd-loadout-name="' +
                escapeHtml(name) +
                '" data-cmd-loadout-agent="' +
                escapeHtml(encodedAgent) +
                '"' +
                (busy ? ' disabled' : '') +
                '><span class="ws-cmd-loadout-picker-item-icon"><i class="bi ' +
                (isMCP ? 'bi-hdd-network' : 'bi-stars') +
                '" aria-hidden="true"></i></span><span><strong>' +
                escapeHtml(name) +
                '</strong><small>Add to ' +
                escapeHtml(agentName) +
                '</small></span><i class="bi bi-plus-lg" aria-hidden="true"></i></button>'
              );
            })
            .join('') +
          '</div>'
        : '<div class="ws-cmd-loadout-picker-empty">Nothing new to add.</div>';
    }
    return (
      '<div class="ws-cmd-loadout-add-modal" data-cmd-loadout-add-modal>' +
      '<div class="ws-cmd-loadout-add-backdrop" data-cmd-loadout-add-close aria-hidden="true"></div><section class="ws-cmd-loadout-add-dialog" role="dialog" aria-modal="true" aria-labelledby="' +
      titleId +
      '" aria-describedby="' +
      descriptionId +
      '" tabindex="-1"><header><div><span class="ws-cmd-loadout-kicker">Toolbox</span><h3 id="' +
      titleId +
      '">Add ' +
      noun +
      '</h3></div><button type="button" class="ws-cmd-loadout-add-close" data-cmd-loadout-add-close aria-label="Close Add ' +
      noun +
      '">×</button></header><p id="' +
      descriptionId +
      '">Choose a workspace ' +
      (isMCP ? 'tool' : 'skill') +
      ' to assign to <strong>' +
      escapeHtml(agentName) +
      '</strong>.</p>' +
      (this.loadoutError
        ? '<div class="ws-cmd-loadout-add-error" role="alert">' +
          escapeHtml(this.loadoutError) +
          '</div>'
        : '') +
      '<div class="ws-cmd-loadout-add-body">' +
      content +
      '</div></section></div>'
    );
  }

  loadoutAddOriginKey(kind, encodedAgent) {
    return encodeURIComponent([String(kind || ''), String(encodedAgent || '')].join(':'));
  }

  resetLoadoutPicker({ restoreFocus = false } = {}) {
    const originKey = restoreFocus
      ? this.loadoutAddOriginKey(this.loadoutAddOpen, this.loadoutAddAgent)
      : '';
    this.loadoutAddRequestId = Number(this.loadoutAddRequestId || 0) + 1;
    this.loadoutAddOpen = '';
    this.loadoutAddAgent = '';
    this.loadoutAddOptions = [];
    this.loadoutAddLoading = false;
    this.loadoutError = '';
    this.pendingLoadoutAddFocus = originKey ? 'origin:' + originKey : '';
  }

  closeLoadoutPicker() {
    if (!this.loadoutAddOpen) return;
    this.resetLoadoutPicker({ restoreFocus: true });
    this.render();
  }

  rememberLoadoutAddFocus() {
    if (this.pendingLoadoutAddFocus || !this.loadoutAddOpen) return;
    const activeElement = typeof document === 'undefined' ? null : document.activeElement;
    if (!activeElement || typeof activeElement.closest !== 'function') return;
    if (!activeElement.closest('[data-cmd-loadout-add-modal]')) return;
    const option = activeElement.closest('[data-cmd-loadout-bind]');
    if (option) {
      this.pendingLoadoutAddFocus =
        'option:' + String(option.getAttribute('data-cmd-loadout-name') || '');
      return;
    }
    if (activeElement.closest('.ws-cmd-loadout-add-close')) {
      this.pendingLoadoutAddFocus = 'close';
      return;
    }
    this.pendingLoadoutAddFocus = 'dialog';
  }

  restoreLoadoutAddFocus() {
    const targetKey = String(this.pendingLoadoutAddFocus || '');
    if (!targetKey || !this.container || typeof this.container.querySelector !== 'function') return;
    this.pendingLoadoutAddFocus = '';
    let selector = '';
    let target = null;
    if (targetKey === 'close') selector = '.ws-cmd-loadout-add-close';
    else if (targetKey === 'dialog') selector = '.ws-cmd-loadout-add-dialog';
    else if (targetKey.startsWith('option:')) {
      const name = targetKey.slice(7);
      if (typeof this.container.querySelectorAll === 'function') {
        target = Array.from(this.container.querySelectorAll('[data-cmd-loadout-bind]')).find(
          option => String(option.getAttribute('data-cmd-loadout-name') || '') === name
        );
      }
    } else if (targetKey.startsWith('origin:')) {
      selector = '[data-cmd-loadout-add-origin="' + targetKey.slice(7) + '"]';
    }
    if (!target && selector) target = this.container.querySelector(selector);
    if (target && typeof target.focus === 'function') {
      try {
        target.focus({ preventScroll: true });
      } catch (_error) {
        target.focus();
      }
    }
  }

  handleLoadoutAddKeydown(event) {
    if (!this.loadoutAddOpen) return false;
    const modal = event?.target?.closest?.('[data-cmd-loadout-add-modal]');
    if (!modal) return false;
    if (event.key === 'Escape') {
      event.preventDefault();
      if (typeof event.stopPropagation === 'function') event.stopPropagation();
      this.closeLoadoutPicker();
      return true;
    }
    if (event.key !== 'Tab' || typeof modal.querySelectorAll !== 'function') return false;
    const focusable = Array.from(
      modal.querySelectorAll('button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])')
    );
    if (!focusable.length) return false;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const activeElement = typeof document === 'undefined' ? null : document.activeElement;
    if (event.shiftKey && activeElement === first) {
      event.preventDefault();
      last.focus();
      return true;
    }
    if (!event.shiftKey && activeElement === last) {
      event.preventDefault();
      first.focus();
      return true;
    }
    return false;
  }

  decodeAgentName(encodedName) {
    try {
      return decodeURIComponent(String(encodedName || '')).trim();
    } catch (_error) {
      return String(encodedName || '').trim();
    }
  }

  async openCapabilityInspector(trigger) {
    if (!trigger || typeof trigger.getAttribute !== 'function') return;
    const kind = String(trigger.getAttribute('data-cmd-capability-inspect') || '').trim();
    const bindingId = String(trigger.getAttribute('data-cmd-loadout-binding') || '').trim();
    const name = String(trigger.getAttribute('data-cmd-loadout-name') || '').trim();
    const encodedAgent = String(trigger.getAttribute('data-cmd-loadout-agent') || '').trim();
    if (!['skill', 'mcp'].includes(kind) || !bindingId || !name || !encodedAgent) return;

    const agent = this.decodeAgentName(encodedAgent);
    const requestId = Number(this.capabilityInspectorRequestId || 0) + 1;
    this.capabilityInspectorRequestId = requestId;
    this.capabilityInspector = {
      open: true,
      kind,
      bindingId,
      name,
      agent,
      encodedAgent,
      activeTab: 'overview',
      status: 'loading',
      data: null,
      error: '',
      assigned: trigger.getAttribute('data-cmd-loadout-enabled') === 'true',
      locked: trigger.getAttribute('data-cmd-loadout-locked') === 'true',
      originKey:
        trigger.getAttribute('data-cmd-capability-origin') ||
        this.capabilityOriginKey(kind, bindingId, encodedAgent)
    };
    this.resetLoadoutPicker();
    if (this.viewMode !== 'map') {
      this.viewMode = 'map';
      this.persistCommandViewMode('map');
    }
    this.activeMapWindow = 'inspector';
    this.pendingCapabilityInspectorFocus = 'back';
    this.render();
    await this.loadCapabilityInspectorDetails({ requestId });
  }

  async loadCapabilityInspectorDetails(options = {}) {
    const inspector = this.capabilityInspector || {};
    if (!inspector.open) return;
    const requestId = options.requestId || Number(this.capabilityInspectorRequestId || 0) + 1;
    this.capabilityInspectorRequestId = requestId;
    const identity = [
      inspector.kind,
      inspector.bindingId,
      inspector.name,
      inspector.agent,
      inspector.encodedAgent
    ].join('\u0000');
    inspector.status = 'loading';
    inspector.error = '';
    this.pendingCapabilityInspectorFocus = this.pendingCapabilityInspectorFocus || 'back';
    this.render();

    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    try {
      let data;
      if (inspector.kind === 'skill') {
        if (!page || typeof page.loadWorkspaceSkillDetails !== 'function') {
          throw new Error('Skill details are unavailable');
        }
        data = await page.loadWorkspaceSkillDetails(inspector.agent, inspector.name, {
          force: options.force === true
        });
      } else {
        if (!page || typeof page.loadWorkspaceMCPDetails !== 'function') {
          throw new Error('MCP details are unavailable');
        }
        data = await page.loadWorkspaceMCPDetails(inspector.bindingId, inspector.name, {
          force: options.force === true,
          start: options.start === true
        });
      }

      const current = this.capabilityInspector || {};
      const currentIdentity = [
        current.kind,
        current.bindingId,
        current.name,
        current.agent,
        current.encodedAgent
      ].join('\u0000');
      if (
        requestId !== this.capabilityInspectorRequestId ||
        !current.open ||
        identity !== currentIdentity
      ) {
        return;
      }
      this.rememberCapabilityInspectorFocus();
      current.status = 'loaded';
      current.data = data;
      current.error = '';
      if (data?.workspace_binding?.locked === true) current.locked = true;
      this.render();
    } catch (error) {
      const current = this.capabilityInspector || {};
      const currentIdentity = [
        current.kind,
        current.bindingId,
        current.name,
        current.agent,
        current.encodedAgent
      ].join('\u0000');
      if (
        requestId !== this.capabilityInspectorRequestId ||
        !current.open ||
        identity !== currentIdentity
      ) {
        return;
      }
      this.rememberCapabilityInspectorFocus();
      current.status = 'error';
      current.error = error?.message || 'Details could not be loaded';
      this.render();
    }
  }

  closeCapabilityInspector() {
    const originKey = String(this.capabilityInspector?.originKey || '');
    this.resetCapabilityInspector();
    this.pendingCapabilityInspectorFocus = originKey ? 'origin:' + originKey : '';
    this.render();
  }

  setCapabilityInspectorTab(tab) {
    const inspector = this.capabilityInspector || {};
    if (!inspector.open) return;
    const allowed =
      inspector.kind === 'mcp' ? ['overview', 'tools', 'docs'] : ['overview', 'instructions'];
    const normalized = String(tab || '')
      .trim()
      .toLowerCase();
    if (!allowed.includes(normalized)) return;
    inspector.activeTab = normalized;
    this.pendingCapabilityInspectorFocus = 'tab:' + normalized;
    this.render();
  }

  retryCapabilityInspector() {
    if (!this.capabilityInspector?.open) return;
    return this.loadCapabilityInspectorDetails({ force: true });
  }

  startCapabilityMCP() {
    if (!this.capabilityInspector?.open || this.capabilityInspector.kind !== 'mcp') return;
    return this.loadCapabilityInspectorDetails({ force: true, start: true });
  }

  handleCapabilityInspectorKeydown(event) {
    const current = event?.target?.closest?.('[data-cmd-capability-tab]');
    if (!current) return;
    const tablist = current.closest?.('[role="tablist"]');
    const tabs =
      tablist && typeof tablist.querySelectorAll === 'function'
        ? Array.from(tablist.querySelectorAll('[data-cmd-capability-tab]'))
        : [];
    if (!tabs.length) return;
    const index = tabs.indexOf(current);
    let nextIndex = index;
    if (event.key === 'ArrowRight') nextIndex = (index + 1) % tabs.length;
    else if (event.key === 'ArrowLeft') nextIndex = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === 'Home') nextIndex = 0;
    else if (event.key === 'End') nextIndex = tabs.length - 1;
    else return;
    event.preventDefault();
    this.setCapabilityInspectorTab(tabs[nextIndex].getAttribute('data-cmd-capability-tab'));
  }

  // Delegated handler for loadout capability rows, assignment switches, Add buttons,
  // and picker items. Returns true when shared map/garrison listeners should stop.
  bindLoadoutAddModal() {
    const modal =
      this.container && this.container.querySelector
        ? this.container.querySelector('[data-cmd-loadout-add-modal]')
        : null;
    if (!modal) return;
    if (this.container.children) {
      Array.from(this.container.children).forEach(child => {
        if (child !== modal) child.inert = true;
      });
    }
    modal.addEventListener('click', event => this.handleLoadoutClick(event));
    modal.addEventListener('keydown', event => this.handleLoadoutAddKeydown(event));
  }

  handleLoadoutClick(event) {
    const closeAdd = event.target.closest('[data-cmd-loadout-add-close]');
    if (closeAdd) {
      this.closeLoadoutPicker();
      return true;
    }
    const inspect = event.target.closest('[data-cmd-capability-inspect]');
    if (inspect) {
      this.openCapabilityInspector(inspect);
      return true;
    }
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
    const originKey = this.capabilityOriginKey(kind, bindingId, encodedAgent);
    this.loadoutError = '';
    this.loadoutBusyKey = kind + ':' + bindingId;
    this.pendingCapabilityInspectorFocus = 'switch:' + originKey;
    this.render();
    try {
      await page.setAgentWorkspaceCapabilityEnabled(kind, agentName, bindingId, enable);
      if (this.capabilityInspectorMatches(kind, bindingId, encodedAgent)) {
        this.capabilityInspector.assigned = enable;
      }
    } catch (error) {
      this.loadoutError =
        (kind === 'mcp' ? 'Tool' : 'Skill') + ' update failed: ' + (error?.message || 'error');
      if (window.Toast) window.Toast.error(this.loadoutError);
    } finally {
      this.loadoutBusyKey = '';
      this.pendingCapabilityInspectorFocus = 'switch:' + originKey;
      this.render();
    }
  }

  async openLoadoutPicker(kind, encodedAgent) {
    const normalizedKind = String(kind || '').trim();
    const agent = String(encodedAgent || '').trim();
    if (!['skill', 'mcp'].includes(normalizedKind) || !agent) return;
    if (this.loadoutAddOpen === normalizedKind && this.loadoutAddAgent === agent) {
      this.closeLoadoutPicker();
      return;
    }
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    const requestId = Number(this.loadoutAddRequestId || 0) + 1;
    this.loadoutAddRequestId = requestId;
    this.loadoutAddOpen = normalizedKind;
    this.loadoutAddAgent = agent;
    this.loadoutAddLoading = true;
    this.loadoutAddOptions = [];
    this.loadoutError = '';
    this.pendingLoadoutAddFocus = 'close';
    this.render();
    try {
      const options =
        page && typeof page.listAgentLoadoutAdditions === 'function'
          ? await page.listAgentLoadoutAdditions(normalizedKind)
          : [];
      if (
        requestId !== this.loadoutAddRequestId ||
        this.loadoutAddOpen !== normalizedKind ||
        this.loadoutAddAgent !== agent
      ) {
        return;
      }
      this.loadoutAddOptions = options;
    } catch (error) {
      if (
        requestId !== this.loadoutAddRequestId ||
        this.loadoutAddOpen !== normalizedKind ||
        this.loadoutAddAgent !== agent
      ) {
        return;
      }
      this.loadoutAddOptions = [];
      this.loadoutError =
        'Could not load available ' +
        (normalizedKind === 'mcp' ? 'tools' : 'skills') +
        ': ' +
        (error?.message || 'error');
    } finally {
      if (
        requestId === this.loadoutAddRequestId &&
        this.loadoutAddOpen === normalizedKind &&
        this.loadoutAddAgent === agent
      ) {
        this.loadoutAddLoading = false;
        this.render();
      }
    }
  }

  async bindLoadoutCapability(kind, encodedAgent, name) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
    if (!page || typeof page.addAgentWorkspaceCapability !== 'function') return;
    const agentName = this.decodeAgentName(encodedAgent);
    const capName = String(name || '').trim();
    this.loadoutError = '';
    this.loadoutBusyKey = kind + ':add:' + capName;
    this.pendingLoadoutAddFocus = 'option:' + capName;
    this.render();
    try {
      await page.addAgentWorkspaceCapability(kind, agentName, capName);
      const originKey = this.loadoutAddOriginKey(kind, encodedAgent);
      this.resetLoadoutPicker();
      this.pendingLoadoutAddFocus = 'origin:' + originKey;
      if (window.Toast) {
        window.Toast.success((kind === 'mcp' ? 'Tool' : 'Skill') + ' "' + capName + '" added');
      }
    } catch (error) {
      this.loadoutError = 'Could not add ' + capName + ': ' + (error?.message || 'error');
      this.pendingLoadoutAddFocus = 'option:' + capName;
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

  capabilityValueListHTML(label, values, emptyText = 'None') {
    const list = Array.isArray(values)
      ? values.map(value => String(value || '').trim()).filter(Boolean)
      : [];
    return (
      '<div class="ws-cmd-capability-fact"><dt>' +
      escapeHtml(label) +
      '</dt><dd>' +
      (list.length
        ? '<span class="ws-cmd-capability-tags">' +
          list.map(value => '<code>' + escapeHtml(value) + '</code>').join('') +
          '</span>'
        : escapeHtml(emptyText)) +
      '</dd></div>'
    );
  }

  capabilityWarningsHTML(data) {
    const warnings = [];
    const validationErrors = Array.isArray(data?.validation_errors)
      ? data.validation_errors.map(value => String(value || '').trim()).filter(Boolean)
      : [];
    if (data?.has_scripts && !data?.trusted) {
      warnings.push('This skill contains scripts and is not trusted.');
    } else if (data?.has_scripts) {
      warnings.push('This skill contains scripts and is trusted.');
    }
    validationErrors.forEach(error => warnings.push(error));
    if (!warnings.length) return '';
    return (
      '<div class="ws-cmd-capability-warnings" role="note"><strong>Attention</strong><ul>' +
      warnings.map(warning => '<li>' + escapeHtml(warning) + '</li>').join('') +
      '</ul></div>'
    );
  }

  renderSkillCapabilityPane(inspector) {
    const data = inspector.data || {};
    if (inspector.activeTab === 'instructions') {
      return (
        '<section class="ws-cmd-capability-pane" role="tabpanel" id="ws-cmd-capability-panel-instructions" aria-labelledby="ws-cmd-capability-tab-instructions">' +
        '<h4>Prompt / Instructions</h4>' +
        (String(data.prompt || '').trim()
          ? '<pre class="ws-cmd-capability-prompt">' + escapeHtml(data.prompt) + '</pre>'
          : '<p class="ws-cmd-capability-empty">No prompt or instructions are available.</p>') +
        '</section>'
      );
    }

    return (
      '<section class="ws-cmd-capability-pane" role="tabpanel" id="ws-cmd-capability-panel-overview" aria-labelledby="ws-cmd-capability-tab-overview">' +
      '<p class="ws-cmd-capability-description">' +
      escapeHtml(data.description || 'No description is available for this skill.') +
      '</p>' +
      this.capabilityWarningsHTML(data) +
      '<dl class="ws-cmd-capability-facts">' +
      '<div class="ws-cmd-capability-fact"><dt>Source</dt><dd>' +
      escapeHtml(data.source || 'Unknown') +
      '</dd></div>' +
      (data.path
        ? '<div class="ws-cmd-capability-fact"><dt>Path</dt><dd><code>' +
          escapeHtml(data.path) +
          '</code></dd></div>'
        : '') +
      '<div class="ws-cmd-capability-fact"><dt>Model</dt><dd>' +
      escapeHtml(data.model || 'Agent default') +
      '</dd></div>' +
      '<div class="ws-cmd-capability-fact"><dt>Trust</dt><dd>' +
      escapeHtml(
        data.has_scripts ? (data.trusted ? 'Trusted scripts' : 'Untrusted scripts') : 'No scripts'
      ) +
      '</dd></div>' +
      this.capabilityValueListHTML('Required MCP servers', data.required_mcp_servers) +
      this.capabilityValueListHTML('Allowed tools', data.allowed_tools, 'Agent policy') +
      this.capabilityValueListHTML('Disallowed tools', data.disallowed_tools) +
      '</dl></section>'
    );
  }

  renderMCPToolSchemaHTML(tool) {
    const schema =
      tool?.inputSchema && typeof tool.inputSchema === 'object' ? tool.inputSchema : {};
    const properties =
      schema.properties && typeof schema.properties === 'object' ? schema.properties : {};
    const required = new Set(Array.isArray(schema.required) ? schema.required : []);
    const names = Object.keys(properties);
    if (!names.length) {
      return '<p class="ws-cmd-capability-tool-empty">No parameters.</p>';
    }
    return (
      '<dl class="ws-cmd-capability-params">' +
      names
        .map(name => {
          const property = properties[name] || {};
          const rawType = property.type || (Array.isArray(property.enum) ? 'enum' : 'value');
          const type = Array.isArray(rawType) ? rawType.join(' | ') : String(rawType || 'value');
          return (
            '<div><dt><code>' +
            escapeHtml(name) +
            '</code><span>' +
            escapeHtml(type) +
            '</span><em>' +
            (required.has(name) ? 'Required' : 'Optional') +
            '</em></dt>' +
            (property.description ? '<dd>' + escapeHtml(property.description) + '</dd>' : '') +
            '</div>'
          );
        })
        .join('') +
      '</dl>'
    );
  }

  renderMCPStartActionHTML(data, inspector) {
    const tools = Array.isArray(data?.tools) ? data.tools : [];
    const status = String(data?.status || '')
      .trim()
      .toLowerCase();
    if (data?.synthesized || tools.length || status === 'running') return '';
    return (
      (data?.start_error
        ? '<p class="ws-cmd-capability-start-error" role="alert">Server start failed: ' +
          escapeHtml(data.start_error) +
          '</p>'
        : '<p class="ws-cmd-capability-empty">This server is not running. Passive inspection does not start it.</p>') +
      '<button type="button" class="ws-cmd-capability-action is-primary" data-cmd-capability-start aria-label="Start MCP server ' +
      escapeHtml(inspector.name) +
      ' and load tools">Start server &amp; load tools</button>'
    );
  }

  renderMCPOverviewHTML(data, inspector) {
    const binding = data?.workspace_binding || {};
    const scope = binding.scope && typeof binding.scope === 'object' ? binding.scope : {};
    const roots = Array.isArray(scope.roots)
      ? scope.roots.map(value => String(value || '').trim()).filter(Boolean)
      : [];
    const args = Array.isArray(data?.args) ? data.args : [];
    const info = data?.server_info && typeof data.server_info === 'object' ? data.server_info : {};
    return (
      '<section class="ws-cmd-capability-pane" role="tabpanel" id="ws-cmd-capability-panel-overview" aria-labelledby="ws-cmd-capability-tab-overview">' +
      (data?.synthesized
        ? '<div class="ws-cmd-capability-notice"><strong>Workspace-native capability</strong><p>This locked binding is synthesized from approved workspace directories. It does not use or start a global MCP server.</p></div>'
        : '') +
      '<dl class="ws-cmd-capability-facts">' +
      '<div class="ws-cmd-capability-fact"><dt>Status</dt><dd>' +
      escapeHtml(data?.status || 'Unknown') +
      '</dd></div>' +
      '<div class="ws-cmd-capability-fact"><dt>Workspace binding</dt><dd>' +
      escapeHtml(binding.source || (data?.synthesized ? 'Synthesized' : 'Explicit')) +
      (binding.alias ? ' · ' + escapeHtml(binding.alias) : '') +
      '</dd></div>' +
      (roots.length ? this.capabilityValueListHTML('Approved roots', roots) : '') +
      (Object.keys(scope).length && !roots.length
        ? '<div class="ws-cmd-capability-fact"><dt>Scope</dt><dd><pre>' +
          escapeHtml(JSON.stringify(scope, null, 2)) +
          '</pre></dd></div>'
        : '') +
      '<div class="ws-cmd-capability-fact"><dt>Transport</dt><dd>' +
      escapeHtml(data?.transport || 'stdio') +
      '</dd></div>' +
      (data?.command
        ? '<div class="ws-cmd-capability-fact"><dt>Command</dt><dd><code>' +
          escapeHtml([data.command, ...args].filter(Boolean).join(' ')) +
          '</code></dd></div>'
        : '') +
      (info.name || info.title
        ? '<div class="ws-cmd-capability-fact"><dt>Reported server</dt><dd>' +
          escapeHtml(info.title || info.name) +
          (info.version ? ' · ' + escapeHtml(info.version) : '') +
          '</dd></div>'
        : '') +
      this.capabilityValueListHTML('Environment keys', data?.env_keys, 'None reported') +
      '</dl>' +
      this.renderMCPStartActionHTML(data, inspector) +
      '</section>'
    );
  }

  renderMCPToolsHTML(data, inspector) {
    const tools = Array.isArray(data?.tools) ? data.tools : [];
    return (
      '<section class="ws-cmd-capability-pane" role="tabpanel" id="ws-cmd-capability-panel-tools" aria-labelledby="ws-cmd-capability-tab-tools">' +
      '<div class="ws-cmd-capability-pane-title"><h4>Callable tools</h4><span>' +
      escapeHtml(tools.length) +
      '</span></div>' +
      (tools.length
        ? '<div class="ws-cmd-capability-tools">' +
          tools
            .map(
              tool =>
                '<article class="ws-cmd-capability-tool"><header><code>' +
                escapeHtml(tool?.name || 'Unnamed tool') +
                '</code>' +
                (tool?.title && tool.title !== tool.name
                  ? '<span>' + escapeHtml(tool.title) + '</span>'
                  : '') +
                '</header>' +
                (tool?.description ? '<p>' + escapeHtml(tool.description) + '</p>' : '') +
                this.renderMCPToolSchemaHTML(tool) +
                '</article>'
            )
            .join('') +
          '</div>'
        : this.renderMCPStartActionHTML(data, inspector) ||
          '<p class="ws-cmd-capability-empty">This running server reported no tools.</p>') +
      '</section>'
    );
  }

  safeCapabilitySourceURL(value) {
    const raw = String(value || '').trim();
    if (!raw) return '';
    try {
      const parsed = new URL(raw);
      return ['http:', 'https:'].includes(parsed.protocol) ? parsed.href : '';
    } catch (_error) {
      return '';
    }
  }

  renderMCPDocsHTML(data) {
    const instructions = String(data?.instructions || '').trim();
    const readme = data?.readme && typeof data.readme === 'object' ? data.readme : {};
    const markdown = String(readme.markdown || '').trim();
    const sourceURL = this.safeCapabilitySourceURL(readme.source_url);
    return (
      '<section class="ws-cmd-capability-pane" role="tabpanel" id="ws-cmd-capability-panel-docs" aria-labelledby="ws-cmd-capability-tab-docs">' +
      (instructions
        ? '<div class="ws-cmd-capability-doc"><h4>Server instructions</h4><div class="ws-cmd-capability-markdown">' +
          renderCapabilityMarkdown(instructions) +
          '</div></div>'
        : '') +
      (markdown
        ? '<div class="ws-cmd-capability-doc"><div class="ws-cmd-capability-pane-title"><h4>README</h4>' +
          (sourceURL
            ? '<a href="' +
              escapeHtml(sourceURL) +
              '" target="_blank" rel="noopener noreferrer">View source</a>'
            : '') +
          '</div><div class="ws-cmd-capability-markdown">' +
          renderCapabilityMarkdown(markdown) +
          '</div></div>'
        : '') +
      (!instructions && !markdown
        ? '<p class="ws-cmd-capability-empty">No README or server instructions are available.</p>'
        : '') +
      '</section>'
    );
  }

  renderMCPCapabilityPane(inspector) {
    const data = inspector.data || {};
    if (inspector.activeTab === 'tools') return this.renderMCPToolsHTML(data, inspector);
    if (inspector.activeTab === 'docs') return this.renderMCPDocsHTML(data);
    return this.renderMCPOverviewHTML(data, inspector);
  }

  renderCapabilityInspector(agent) {
    const inspector = this.capabilityInspector || {};
    if (
      !inspector.open ||
      this.normalizeAgentKey(inspector.agent) !== this.normalizeAgentKey(agent?.key || agent?.name)
    ) {
      return '';
    }
    const isMCP = inspector.kind === 'mcp';
    const tabs = isMCP ? ['overview', 'tools', 'docs'] : ['overview', 'instructions'];
    const assignmentLabel = inspector.locked
      ? 'Always available · Locked'
      : inspector.assigned
        ? 'Assigned to ' + inspector.agent
        : 'Not assigned to ' + inspector.agent;
    let content = '';
    if (inspector.status === 'loading') {
      content =
        '<div role="tabpanel" id="ws-cmd-capability-panel-' +
        inspector.activeTab +
        '" aria-labelledby="ws-cmd-capability-tab-' +
        inspector.activeTab +
        '"><div class="ws-cmd-capability-loading" role="status" aria-live="polite"><span aria-hidden="true"></span><strong>' +
        (isMCP ? 'Loading server details…' : 'Loading skill details…') +
        '</strong><small>Assignment remains unchanged.</small></div></div>';
    } else if (inspector.status === 'error') {
      content =
        '<div role="tabpanel" id="ws-cmd-capability-panel-' +
        inspector.activeTab +
        '" aria-labelledby="ws-cmd-capability-tab-' +
        inspector.activeTab +
        '"><div class="ws-cmd-capability-error" role="alert"><strong>Details unavailable</strong><p>' +
        escapeHtml(inspector.error || 'The capability could not be loaded.') +
        '</p><button type="button" class="ws-cmd-capability-action" data-cmd-capability-retry>Retry</button></div></div>';
    } else {
      content = isMCP
        ? this.renderMCPCapabilityPane(inspector)
        : this.renderSkillCapabilityPane(inspector);
    }
    return (
      '<section class="ws-cmd-capability-inspector" data-cmd-capability-inspector role="region" aria-labelledby="ws-cmd-capability-title" aria-busy="' +
      (inspector.status === 'loading' ? 'true' : 'false') +
      '">' +
      '<header class="ws-cmd-capability-head"><button type="button" class="ws-cmd-capability-back" data-cmd-capability-back aria-label="Back to Command Menu and ' +
      escapeHtml(inspector.name) +
      ' row"><i class="bi bi-arrow-left" aria-hidden="true"></i> Back</button><div><span>' +
      (isMCP ? 'MCP Server' : 'Skill') +
      '</span><h3 id="ws-cmd-capability-title" tabindex="-1">' +
      escapeHtml(inspector.name) +
      '</h3><small>' +
      escapeHtml(assignmentLabel) +
      '</small></div></header>' +
      '<div class="ws-cmd-capability-tabs" role="tablist" aria-label="' +
      escapeHtml(inspector.name) +
      ' details">' +
      tabs
        .map(
          tab =>
            '<button type="button" role="tab" id="ws-cmd-capability-tab-' +
            tab +
            '" data-cmd-capability-tab="' +
            tab +
            '" aria-selected="' +
            (inspector.activeTab === tab ? 'true' : 'false') +
            '" aria-controls="ws-cmd-capability-panel-' +
            tab +
            '" tabindex="' +
            (inspector.activeTab === tab ? '0' : '-1') +
            '">' +
            escapeHtml(tab.charAt(0).toUpperCase() + tab.slice(1)) +
            '</button>'
        )
        .join('') +
      '</div><div class="ws-cmd-capability-body">' +
      content +
      '</div><footer class="ws-cmd-capability-footer"><span>Read-only details</span><a href="' +
      (isMCP ? '/mcp' : '/skills?agent=' + encodeURIComponent(inspector.agent)) +
      '">Manage on the ' +
      (isMCP ? 'MCP' : 'Skills') +
      ' page <i class="bi bi-box-arrow-up-right" aria-hidden="true"></i></a></footer></section>'
    );
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
        label: 'Open the Workshop',
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
    const profileRole = String(agent.profile?.role || agent.role?.roles?.[0] || '').trim();
    const roleLabel = agent.entry
      ? this.commanderLabel(profileRole)
      : agent.role?.detail || agent.role?.label || 'Agent';
    const questStatusLabel = summary?.statusLabel || agent.status?.label || 'Idle';
    const questActivityLabel = String(summary?.activityLabel || '').trim();
    const questMeta = [
      questStatusLabel,
      questActivityLabel.toLowerCase() === String(questStatusLabel).trim().toLowerCase()
        ? ''
        : questActivityLabel,
      summary?.whenLabel || ''
    ]
      .filter(Boolean)
      .join(' · ');
    const recentActivityMeta = questActivityLabel
      ? [questActivityLabel, summary?.whenLabel || ''].filter(Boolean).join(' · ')
      : 'No recorded activity';
    const statCards = [
      { label: 'Quests', value: openTaskCount },
      { label: 'Skills', value: Number(agent.skills?.count || 0) },
      { label: 'Tools', value: agent.mcpNames.length },
      { label: 'Units', value: agent.instanceCount }
    ];
    return (
      '<div class="ws-cmd-map-inspector-card ' +
      escapeHtml(agent.tone) +
      (this.capabilityInspector?.open ? ' is-capability-open' : '') +
      '" aria-label="' +
      escapeHtml(agent.name) +
      ' sheet">' +
      '<div class="ws-cmd-map-agent-sheet-head">' +
      this.agentCharacterHTML(agent, 'roster') +
      '<div class="ws-cmd-map-agent-sheet-title"><strong>' +
      escapeHtml(agent.name) +
      '</strong></div></div>' +
      '<div class="ws-cmd-rpg-sheet">' +
      '<div class="ws-cmd-rpg-class-grid">' +
      '<div class="ws-cmd-rpg-class-card"><span>Role</span><strong>' +
      escapeHtml(roleLabel) +
      '</strong></div>' +
      this.renderMapAgentModelCard(agent) +
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
      escapeHtml(questMeta) +
      '</p></section>' +
      this.renderMapAgentLoadout(agent) +
      '<div class="ws-cmd-rpg-sheet-row"><span>Recent Activity</span><strong>' +
      escapeHtml(recentActivityMeta) +
      '</strong></div></div>' +
      (this.renderCapabilityInspector(agent) ||
        this.renderMapAgentCommandMenu(agent, detailTarget)) +
      '</div>'
    );
  }

  mapInventoryGroups() {
    const page = this.page || {};
    const folders = this.folderRowData();
    const files = this.fileRowData();
    const groups = [
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
    return groups;
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
      backlog: () => this.renderMapBacklogPanel(),
      inventory: () => this.renderMapInventory(),
      stations: () => this.renderMapStationsPanel(),
      inspector: () => this.renderMapInspector(selectedAgent)
    }[key];
    if (!body) return '';
    return (
      '<div class="ws-cmd-map-window-backdrop" data-cmd-map-window-backdrop>' +
      '<section class="ws-cmd-map-window ws-cmd-map-window-' +
      escapeHtml(key) +
      (key === 'inspector' && this.capabilityInspector?.open ? ' has-capability-inspector' : '') +
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
    // is-hq scopes the citadel gold accent to the Stations panel (FR15) —
    // no whole-frame tint, layout/density unchanged.
    return (
      '<div class="ws-cmd-map-shell' +
      (this.isPersonalHQ() ? ' is-hq' : '') +
      '">' +
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
    const intent = this.taskComposerIntent === 'backlog' ? 'backlog' : 'ready';
    const draft = escapeHtml(this.taskComposerDraft || '');
    const error = this.taskComposerError
      ? '<p class="ws-cmd-map-quest-error" role="alert">' +
        escapeHtml(this.taskComposerError) +
        '</p>'
      : '';
    const disabledAttr = submitting ? ' disabled' : '';
    // FR56: distinguish direct Ready creation from Add to Backlog, and show
    // the commitment consequence before saving.
    const intentToggle =
      '<div class="ws-cmd-map-quest-intent" role="group" aria-label="Quest commitment">' +
      '<button type="button" class="ws-cmd-map-quest-intent-btn' +
      (intent === 'ready' ? ' is-active' : '') +
      '" data-cmd-map-quest-intent="ready" aria-pressed="' +
      (intent === 'ready' ? 'true' : 'false') +
      '">Ready Quest</button>' +
      '<button type="button" class="ws-cmd-map-quest-intent-btn' +
      (intent === 'backlog' ? ' is-active' : '') +
      '" data-cmd-map-quest-intent="backlog" aria-pressed="' +
      (intent === 'backlog' ? 'true' : 'false') +
      '">Add to Backlog</button></div>';
    const consequence =
      '<p class="ws-cmd-map-quest-consequence">' +
      (intent === 'backlog'
        ? 'Saves the idea without committing it — nothing is assigned, scheduled, or run until you promote it to Ready.'
        : 'Commits now — creates an unassigned Ready quest that waits for an explicit assign, run, or schedule.') +
      '</p>';
    const placeholder =
      intent === 'backlog'
        ? 'Describe the idea…'
        : 'Describe the quest… (assigned to the Commander)';
    const primaryLabel = submitting
      ? intent === 'backlog'
        ? 'Adding…'
        : 'Creating…'
      : intent === 'backlog'
        ? 'Add to Backlog'
        : 'Create';
    const hint =
      intent === 'backlog'
        ? 'Enter to add to the backlog'
        : 'Enter to create · ⌘/Ctrl+Enter to create &amp; start';
    return (
      '<div class="ws-cmd-map-quest-dock is-open">' +
      button +
      '<section class="ws-cmd-map-quest-composer" role="dialog" aria-modal="false" aria-label="New quest">' +
      '<header class="ws-cmd-map-quest-head"><span>New Quest</span>' +
      '<button type="button" class="ws-cmd-map-quest-close" data-cmd-map-quest-cancel aria-label="Close new quest">×</button></header>' +
      intentToggle +
      '<textarea class="ws-cmd-map-quest-input" data-cmd-map-quest-input rows="2" ' +
      'placeholder="' +
      escapeHtml(placeholder) +
      '"' +
      disabledAttr +
      '>' +
      draft +
      '</textarea>' +
      consequence +
      error +
      '<div class="ws-cmd-map-quest-actions">' +
      '<button type="button" class="ws-cmd-map-quest-btn is-primary" data-cmd-map-quest-create' +
      disabledAttr +
      '>' +
      primaryLabel +
      '</button>' +
      (intent === 'ready'
        ? '<button type="button" class="ws-cmd-map-quest-btn" data-cmd-map-quest-start' +
          disabledAttr +
          '>Create &amp; Start</button>'
        : '') +
      '</div>' +
      '<p class="ws-cmd-map-quest-hint">' +
      hint +
      '</p>' +
      '</section></div>'
    );
  }

  openTaskComposer() {
    this.taskComposerOpen = true;
    this.taskComposerError = '';
    this.taskComposerIntent = 'ready';
    this.render();
  }

  closeTaskComposer({ clearDraft = true } = {}) {
    this.taskComposerOpen = false;
    this.taskComposerError = '';
    this.taskComposerSubmitting = false;
    if (clearDraft) this.taskComposerDraft = '';
    this.render();
  }

  setTaskComposerIntent(intent) {
    const next = intent === 'backlog' ? 'backlog' : 'ready';
    if (this.taskComposerIntent === next) return;
    this.taskComposerIntent = next;
    this.taskComposerError = '';
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
    if (!page) return;

    if (this.taskComposerIntent === 'backlog') {
      if (typeof page.createBacklogItem !== 'function') return;
      this.taskComposerSubmitting = true;
      this.taskComposerError = '';
      this.render();
      const created = await page.createBacklogItem({ description });
      this.taskComposerSubmitting = false;
      if (!created) {
        this.taskComposerError = 'Could not add to the backlog. Try again.';
        this.render();
        return;
      }
      // page.createBacklogItem() already toasts "Added to backlog" and
      // refreshes the Backlog panel/Quest Board via loadBacklog().
      this.taskComposerDraft = '';
      this.taskComposerOpen = false;
      this.taskComposerError = '';
      this.render();
      return;
    }

    if (typeof page.createTask !== 'function') return;

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
      const target = assignee || 'the Commander';
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

  // Activating the email station routes by portal state (Mail spin-off FR15):
  //   (c) legacy in-HQ email → open the in-HQ email modal.
  //   (b) an Email Ops workspace exists → navigate to it.
  //   (a) none → deep-link the Construct wizard with the email-ops blueprint
  //       preselected (one creation path; the CTA never creates directly).
  openHQEmailSetup() {
    if (this.hqHasInPlaceEmail()) {
      const hqEmail = window.OriHQEmailSetup;
      if (hqEmail && typeof hqEmail.open === 'function') hqEmail.open();
      return;
    }
    const state = this.emailOpsState();
    if (state.status === 'ready' && state.exists && state.workspaceSlug) {
      this.navigateTo('/workspaces/' + encodeURIComponent(state.workspaceSlug));
      return;
    }
    this.navigateTo('/workspaces?create=1&blueprint=email-ops');
  }

  // navigateTo performs a full navigation, tolerant of environments (tests)
  // without window.location.assign.
  navigateTo(target) {
    if (typeof window === 'undefined' || !window.location) return;
    if (typeof window.location.assign === 'function') window.location.assign(target);
    else window.location.href = target;
  }

  bindOperationsMap() {
    const root = this.container && this.container.querySelector('.ws-cmd-map-shell');
    if (!root) return;
    this.bindStationDrag(root);
    root.addEventListener('click', event => {
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
      if (this.handleLoadoutClick(event)) return;
      if (event.target.closest('[data-cmd-capability-back]')) {
        this.closeCapabilityInspector();
        return;
      }
      const capabilityTab = event.target.closest('[data-cmd-capability-tab]');
      if (capabilityTab) {
        this.setCapabilityInspectorTab(capabilityTab.getAttribute('data-cmd-capability-tab'));
        return;
      }
      if (event.target.closest('[data-cmd-capability-retry]')) {
        this.retryCapabilityInspector();
        return;
      }
      if (event.target.closest('[data-cmd-capability-start]')) {
        this.startCapabilityMCP();
        return;
      }
      const modelBtn = event.target.closest('[data-cmd-edit-model]');
      if (modelBtn && page && typeof page.openAgentModelModal === 'function') {
        page.openAgentModelModal(modelBtn.getAttribute('data-cmd-edit-model'));
        return;
      }
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
      const intentBtn = event.target.closest('[data-cmd-map-quest-intent]');
      if (intentBtn) {
        this.setTaskComposerIntent(intentBtn.getAttribute('data-cmd-map-quest-intent'));
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
        this.resetCapabilityInspector();
        this.resetLoadoutPicker();
        this.render();
        return;
      }
      const backdrop = event.target.closest('[data-cmd-map-window-backdrop]');
      if (backdrop && event.target === backdrop) {
        this.activeMapWindow = '';
        this.resetCapabilityInspector();
        this.resetLoadoutPicker();
        this.render();
        return;
      }
      const windowBtn = event.target.closest('[data-cmd-map-window]');
      if (windowBtn) {
        const nextWindow = windowBtn.getAttribute('data-cmd-map-window');
        this.activeMapWindow = this.activeMapWindow === nextWindow ? '' : nextWindow;
        if (this.activeMapWindow !== 'inspector') this.resetCapabilityInspector();
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
      // Quest Board → shared Backlog drawer handoff (FR52): the drawer opener
      // itself closes this Map window before opening, so the two layers never
      // compete.
      const backlogAddBtn = event.target.closest('[data-cmd-backlog-add]');
      if (backlogAddBtn) {
        this.openBacklogDrawer(backlogAddBtn, { openCapture: true });
        return;
      }
      // Map-window count shortcuts into the Tickets destination. The Details
      // rail has its own handler in bindRail; both route through the same
      // openTicketsFiltered so the two entry points cannot diverge.
      const mapOpenTickets = event.target.closest('[data-cmd-open-tickets]');
      if (mapOpenTickets) {
        this.openTicketsFiltered(mapOpenTickets.getAttribute('data-cmd-open-tickets'));
        return;
      }
      const backlogDrawerBtn = event.target.closest('[data-cmd-open-backlog-drawer]');
      if (backlogDrawerBtn) {
        this.openBacklogDrawer(backlogDrawerBtn);
        return;
      }
      const mapOpenBacklogBtn = event.target.closest('[data-cmd-map-open-backlog]');
      if (mapOpenBacklogBtn) {
        this.openBacklogDrawer(mapOpenBacklogBtn, {
          selectId: mapOpenBacklogBtn.getAttribute('data-cmd-map-open-backlog')
        });
        return;
      }
      const acceptQuestBtn = event.target.closest('[data-cmd-map-accept-quest]');
      if (acceptQuestBtn) {
        this.runMapAcceptQuest(acceptQuestBtn.getAttribute('data-cmd-map-accept-quest'));
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
      const hqStation = event.target.closest('[data-cmd-hq-station]');
      if (hqStation) {
        // A completed drag synthesizes a trailing click on the (now detached)
        // station element; swallow it so dragging never fires the action
        // (FR9). An under-threshold press leaves the flag clear and opens it.
        if (this._suppressStationClick) {
          this._suppressStationClick = false;
          return;
        }
        this.runHQStationAction(hqStation.getAttribute('data-cmd-hq-station'), hqStation);
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
    root.addEventListener('keydown', event => {
      if (this.handleLoadoutAddKeydown(event)) return;
      this.handleCapabilityInspectorKeydown(event);
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
        const start = this.taskComposerIntent !== 'backlog' && (event.metaKey || event.ctrlKey);
        this.submitTaskComposer({ start });
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
    if (this.selectedAgentKey && this.selectedAgentKey !== key) {
      if (this.capabilityInspector?.open) this.resetCapabilityInspector();
      if (this.loadoutAddOpen) this.resetLoadoutPicker();
    }
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
    root.addEventListener('keydown', event => {
      if (this.handleLoadoutAddKeydown(event)) return;
      this.handleAgentTabKeydown(event);
    });
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
      },
      // Same catalog node the Tools modal mounts — one implementation reached
      // from both entry points (FR-17, FR-100).
      { key: 'capabilities', label: 'Capabilities', tabId: '', host: 'capabilities' }
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
      { key: 'find', label: 'Find Tools', tabId: '', host: 'tools' },
      // Built-in capabilities are listed separately from MCP/Skills/Plugins on
      // purpose (FR-16): they are part of Ori rather than something the user
      // connects, and they carry an install lifecycle those providers do not.
      { key: 'capabilities', label: 'Capabilities', tabId: '', host: 'capabilities' },
      {
        key: 'calendar',
        label: 'Calendar',
        tabId: 'workspace-detail-config-calendar-tab',
        host: 'config'
      }
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
      case 'calendar':
        return 'workspace-detail-config-calendar-tab';
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
      this.renderBacklogPanel() +
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
      this.renderSystemsPanel(systemsExpanded) +
      this.renderStationsRailPanel()
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
    if (tab.host === 'capabilities') {
      this.restoreSharedSurface('config');
      this.restoreSharedSurface('tools');
      this.mountCapabilitiesSurface(host, 'ws-cmd-rail-empty');
      return;
    }
    if (tab.host === 'tools') {
      this.restoreSharedSurface('config');
      this.restoreSharedSurface('capabilities');
      const tools = this.mountSharedSurface('tools', '#workspace-detail-tools-card', host);
      if (!tools)
        host.innerHTML = '<div class="ws-cmd-rail-empty">Find Tools is unavailable.</div>';
      return;
    }

    this.restoreSharedSurface('tools');
    this.restoreSharedSurface('capabilities');
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
      // Count shortcuts into the canonical Tickets destination, filtered to
      // one state (FR-65, FR-80, FR-81). Checked before the Backlog handlers
      // below so a shortcut inside the Backlog panel is not swallowed by them.
      const openTickets = event.target.closest('[data-cmd-open-tickets]');
      if (openTickets) {
        this.openTicketsFiltered(openTickets.getAttribute('data-cmd-open-tickets'));
        return;
      }
      const backlogAdd = event.target.closest('[data-cmd-backlog-add]');
      if (backlogAdd) {
        this.openBacklogDrawer(backlogAdd, { openCapture: true });
        return;
      }
      const openBacklogDrawer = event.target.closest('[data-cmd-open-backlog-drawer]');
      if (openBacklogDrawer) {
        const selectId = openBacklogDrawer.getAttribute('data-cmd-backlog-select') || '';
        this.openBacklogDrawer(openBacklogDrawer, selectId ? { selectId } : {});
        return;
      }
      // HQ station rows + the panel's primary action dispatch through the same
      // registry action as the map structures (FR14). Checked before the
      // generic section buttons since these carry no data-cmd-*-section attr.
      const hqStation = event.target.closest('[data-cmd-hq-station]');
      if (hqStation) {
        this.runHQStationAction(hqStation.getAttribute('data-cmd-hq-station'), hqStation);
        return;
      }
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
