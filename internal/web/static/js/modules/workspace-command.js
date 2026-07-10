/*
 * workspace-command.js — Workspace Command view (the workspace detail page)
 *
 * The tactical layout that IS the workspace detail page. It reuses the live
 * WorkspaceDetailPage instance (a headless data/action layer plus shared
 * modals and hidden shared hosts — see #workspace-detail-shared-hosts) and
 * renders into #workspaceCommandView.
 */
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
    this.viewMode = this.readCommandViewModePreference();
    this.activeRailSection = '';
    this.activeMapWindow = '';
    this.mapInventoryOpen = false;
    this.mapInventorySection = '';
    this.statModalSection = '';
    this.statModalEl = null;
    this.statModalTrigger = null;
    this.taskModalShowAll = false;
    this.taskModalBoardMode = false;
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
    this.sharedSurfaceAnchors = {};
    this.boundGlobalKeydown = event => this.handleGlobalKeydown(event);
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
  }

  /** Re-render if active — called by the page after its data loads/refreshes. */
  refresh() {
    if (this.active) this.render();
    else if (this.statModalSection) this.renderStatModalBody();
  }

  handleGlobalKeydown(event) {
    if (!this.active || !event || event.key !== 'Escape') return;
    if (this.statModalSection || this.identityEditMode) return;
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
  }

  bindIdentityControls() {
    const root = this.container && this.container.querySelector('.ws-cmd-topbar');
    if (!root) return;

    root.addEventListener('click', event => {
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

  chipListHTML(items, emptyText, tone = '') {
    const values = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!values.length) {
      return '<span class="ws-cmd-loadout-empty">' + escapeHtml(emptyText) + '</span>';
    }
    return values
      .map(
        item =>
          '<span class="ws-cmd-loadout-chip ' +
          escapeHtml(tone) +
          '">' +
          escapeHtml(item) +
          '</span>'
      )
      .join('');
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
    const skillNames = Array.isArray(agent.skills?.names) ? agent.skills.names : [];
    return (
      '<div class="ws-cmd-loadout-grid">' +
      '<section class="ws-cmd-loadout-card"><header><span class="ws-cmd-loadout-kicker">Model</span>' +
      modelAction +
      '</header><strong>' +
      escapeHtml(model) +
      '</strong></section>' +
      '<section class="ws-cmd-loadout-card"><header><span class="ws-cmd-loadout-kicker">Skills</span>' +
      '<span>' +
      skillNames.length +
      '</span></header><div class="ws-cmd-loadout-chips">' +
      this.chipListHTML(skillNames, 'No workspace skills attached.', 'skill') +
      '</div></section>' +
      '<section class="ws-cmd-loadout-card"><header><span class="ws-cmd-loadout-kicker">MCP tools</span>' +
      '<span>' +
      agent.mcpNames.length +
      '</span></header><div class="ws-cmd-loadout-chips">' +
      this.chipListHTML(agent.mcpNames, 'No MCP tools attached.', 'mcp') +
      '</div></section>' +
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

  isBlockedTask(task) {
    const status = String(task?.status || '')
      .trim()
      .toLowerCase();
    const humanLoop = this.taskHumanLoopState(task);
    return status === 'blocked' || humanLoop === 'blocked';
  }

  isNeedsInputTask(task) {
    const status = String(task?.status || '')
      .trim()
      .toLowerCase();
    const humanLoop = this.taskHumanLoopState(task);
    return (
      status === 'waiting_for_choice' ||
      humanLoop === 'waiting_for_choice' ||
      task?.context?.execution_step_waiting === true
    );
  }

  isWorkingTask(task) {
    return (
      String(task?.status || '')
        .trim()
        .toLowerCase() === 'in_progress'
    );
  }

  isQueuedTask(task) {
    return (
      String(task?.status || '')
        .trim()
        .toLowerCase() === 'pending'
    );
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
    const task = agent?.currentTask || this.priorityTaskForAgent(agent);
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
    return [
      { key: 'objective', label: 'Workspace Objective', icon: 'bi-bullseye' },
      { key: 'objectives', label: 'Objectives', icon: 'bi-list-check' },
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
      '<div class="ws-cmd-map-quest-frame-head"><span>Main Quest</span><strong>Workspace Objective</strong></div>' +
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
            return (
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
              '</span></button>'
            );
          })
          .join('')
      : '<div class="ws-cmd-map-empty is-quest-empty"><strong>Quest log clear</strong><span>No active objectives assigned.</span></div>';
    return (
      '<div class="ws-cmd-map-window-section is-objectives">' +
      this.mapZoneHeaderHTML('Objectives', 'Active Tasks', tasks.length) +
      '<div class="ws-cmd-map-task-list">' +
      rows +
      '</div>' +
      '<button type="button" class="ws-cmd-map-zone-action" data-cmd-map-open-modal="tasks">Open Tasks</button>' +
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
    return agents
      .map((agent, index) => {
        const selected = agent.key === this.selectedAgentKey;
        const destination = agent.destination || 'hub';
        const entryBadge = agent.entry
          ? '<span class="ws-cmd-map-entry-badge" title="Entry Agent"><i class="bi bi-star-fill" aria-hidden="true"></i><span>Entry</span></span>'
          : '';
        const statusLabel = agent.status?.label || 'Idle';
        return (
          '<button type="button" class="ws-cmd-map-agent ' +
          escapeHtml(agent.tone) +
          (selected ? ' is-selected' : '') +
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
          '" aria-label="Select ' +
          escapeHtml(agent.name) +
          ', ' +
          (agent.entry ? 'Entry Agent, ' : '') +
          escapeHtml(statusLabel) +
          '">' +
          '<span class="ws-cmd-map-agent-path" aria-hidden="true"></span>' +
          '<span class="ws-cmd-map-agent-status" aria-hidden="true" title="' +
          escapeHtml(statusLabel) +
          '"><span class="ws-cmd-led ' +
          escapeHtml(agent.tone) +
          '"></span></span>' +
          entryBadge +
          this.agentCharacterHTML(agent, 'roster') +
          '<span class="ws-cmd-map-agent-copy"><strong>' +
          escapeHtml(agent.name) +
          '</strong><span>' +
          escapeHtml(agent.role?.label || 'Agent') +
          '</span><em>' +
          escapeHtml(statusLabel) +
          '</em></span></button>'
        );
      })
      .join('');
  }

  renderMapAgentsZone(agents) {
    return (
      '<section class="ws-cmd-map-world" data-map-zone="agents" aria-label="Agent units">' +
      '<div class="ws-cmd-map-floor" aria-hidden="true"></div>' +
      '<div class="ws-cmd-map-agent-field">' +
      this.renderMapAgentUnits(agents) +
      '</div></section>'
    );
  }

  renderMapAgentLoadout(agent) {
    const skills = Array.isArray(agent?.skills?.names) ? agent.skills.names : [];
    const tools = Array.isArray(agent?.mcpNames) ? agent.mcpNames : [];
    const chips = [
      ...skills.map(name => ({ kind: 'Skill', label: name })),
      ...tools.map(name => ({ kind: 'Tool', label: name }))
    ];
    if (!agent?.model?.empty && agent?.model?.label) {
      chips.unshift({ kind: 'Model', label: agent.model.label });
    }
    const shown = chips
      .map(chip => ({
        kind: String(chip.kind || '').trim(),
        label: String(chip.label || '').trim()
      }))
      .filter(chip => chip.kind && chip.label)
      .slice(0, 6);
    if (!shown.length) {
      return '<section class="ws-cmd-rpg-loadout is-empty"><span>Loadout</span><strong>No skills or tools configured</strong></section>';
    }
    const remaining = Math.max(0, chips.length - shown.length);
    return (
      '<section class="ws-cmd-rpg-loadout"><span>Loadout</span><div>' +
      shown
        .map(
          chip =>
            '<em><small>' + escapeHtml(chip.kind) + '</small>' + escapeHtml(chip.label) + '</em>'
        )
        .join('') +
      (remaining ? '<em><small>More</small>+' + escapeHtml(remaining) + '</em>' : '') +
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
    const detailAction = detailTarget
      ? '<a class="ws-cmd-agent-action" href="' + escapeHtml(detailTarget.href) + '">Open Agent</a>'
      : '';
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
      '<div class="ws-cmd-map-inspector-actions">' +
      '<button type="button" class="ws-cmd-agent-action is-primary" data-cmd-add-task="' +
      escapeHtml(agent.encodedName) +
      '">Give Task</button>' +
      detailAction +
      '</div></div>'
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
      this.renderMapWindow(selected) +
      '</div>'
    );
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
      const openTaskBtn = event.target.closest('[data-cmd-open-task]');
      if (openTaskBtn && page && typeof page.openTask === 'function') {
        page.openTask(openTaskBtn.getAttribute('data-cmd-open-task'));
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
