/*
 * workspace-command.js — Workspace Command view (interior "tactical" reskin)
 *
 * An opt-in tactical layout on the workspace detail page, beside the existing
 * detailed view. It reuses the live WorkspaceDetailPage instance (data + helpers
 * like buildAgentGroups / isWorkspaceEntryAgent) and renders into
 * #workspaceCommandView — no backend, no rewrite of the detailed page.
 *
 * Phase 1 = command bar (name, ops mode, stats) + the Detailed/Command toggle
 * with persistence. Garrison, quest logs, and the right rail land in Phase 2-3.
 */
const STORAGE_KEY = 'oriWorkspaceDetailView';

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
    this.detailedView = document.getElementById('workspace-detail-view');
    this.toggleBtn = document.getElementById('workspace-command-toggle');
    this.active = false;
    this.activeRailSection = '';
    this.statModalSection = '';
    this.statModalEl = null;
    this.statModalTrigger = null;
    this.taskModalShowAll = false;
    this.identityExpanded = false;
    this.identityEditMode = '';
    this.identitySaving = false;
    this.commandTagInput = null;
    this.commandTagDraft = [];
    this.commandTagError = '';
    this.boundGlobalKeydown = (event) => this.handleGlobalKeydown(event);
    this.setup();
  }

  setup() {
    if (!this.container || !this.toggleBtn) return;
    this.toggleBtn.hidden = false; // reveal (Phase 1)
    this.toggleBtn.addEventListener('click', () => this.toggle());
    // The URL wins over localStorage on load. Command is now the implicit
    // default; only an explicit detailed URL/preference keeps the legacy view.
    const urlView = this.getViewFromURL();
    let pref = '';
    try { pref = localStorage.getItem(STORAGE_KEY) || ''; } catch (err) { pref = ''; }
    const initialView = urlView || pref || 'command';
    if (initialView === 'command') this.activate({ syncUrl: false });
    else this.deactivate({ syncUrl: false });
  }

  persist(view) {
    try { localStorage.setItem(STORAGE_KEY, view); } catch (err) { /* storage may be unavailable */ }
  }

  getViewFromURL() {
    try {
      const raw = new URLSearchParams(window.location.search).get('view');
      if (raw === 'command') return 'command';
      if (raw === 'detailed') return 'detailed';
      return '';
    } catch (err) {
      return '';
    }
  }

  // Deep-linkable, non-spammy: command (the default) drops the param, detailed
  // writes ?view=detailed, and toggles use replaceState so history doesn't grow.
  syncURL(view) {
    try {
      const params = new URLSearchParams(window.location.search);
      if (view === 'detailed') params.set('view', 'detailed');
      else params.delete('view');
      const query = params.toString();
      const nextUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
      window.history.replaceState(null, '', nextUrl);
    } catch (err) {
      // no-op: history API may be unavailable in some embedding contexts
    }
  }

  toggle() {
    if (this.active) this.deactivate();
    else this.activate();
  }

  activate({ syncUrl = true } = {}) {
    this.active = true;
    this.render();
    if (this.container) this.container.hidden = false;
    if (this.detailedView) this.detailedView.hidden = true;
    if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
      document.addEventListener('keydown', this.boundGlobalKeydown);
    }
    this.updateToggle();
    this.persist('command');
    if (syncUrl) this.syncURL('command');
  }

  deactivate({ persist = true, syncUrl = true } = {}) {
    this.active = false;
    this.activeRailSection = '';
    this.identityEditMode = '';
    this.destroyCommandTagInput();
    this.closeStatModal();
    if (typeof document !== 'undefined' && typeof document.removeEventListener === 'function') {
      document.removeEventListener('keydown', this.boundGlobalKeydown);
    }
    if (this.container) this.container.hidden = true;
    if (this.detailedView) this.detailedView.hidden = false;
    this.updateToggle();
    if (persist) this.persist('detailed');
    if (syncUrl) this.syncURL('detailed');
  }

  updateToggle() {
    if (!this.toggleBtn) return;
    this.toggleBtn.setAttribute('aria-pressed', this.active ? 'true' : 'false');
    this.toggleBtn.classList.toggle('modern-btn-primary', this.active);
    this.toggleBtn.classList.toggle('modern-btn-secondary', !this.active);
  }

  /** Re-render if active — called by the page after its data loads/refreshes. */
  refresh() {
    if (this.active) this.render();
    else if (this.statModalSection) this.renderStatModalBody();
  }

  handleGlobalKeydown(event) {
    if (!this.active || !event || event.key !== 'Escape') return;
    if (this.statModalSection || this.identityEditMode) return;
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
        (g) => g.isWorkspaceAgent && !g.isUnassigned
      ).length;
    } catch (err) {
      agents = Array.isArray(ws.agent_instances) ? ws.agent_instances.length : 0;
    }
    // Canonical "open" = pending + in-progress, matching the server-side
    // open_task_count Map/Cards/Tree all read (see workspace.ComputeMapSummaryFields).
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const openTasks = tasks.filter((t) => {
      const s = String((t && t.status) || '').toLowerCase();
      return s === 'pending' || s === 'in_progress';
    }).length;
    const mcp = Array.isArray(ws.mcp_bindings) ? ws.mcp_bindings.length : 0;
    const skills = Array.isArray(ws.skill_bindings) ? ws.skill_bindings.length : 0;
    return { agents, openTasks, mcp, skills };
  }

  opsModeLabel() {
    const ws = (this.page && this.page.workspace) || {};
    const settings = ws.workspace_settings || {};
    const mode = String((settings.workflow && settings.workflow.mode) || '').toLowerCase();
    switch (mode) {
      case 'guided': return 'Guided';
      case 'direct': return 'Direct';
      case 'plan_then_execute': return 'Autonomous';
      case '': return '';
      default: return mode.charAt(0).toUpperCase() + mode.slice(1);
    }
  }

  missionSummary() {
    const mission = typeof window !== 'undefined' ? window.workspaceMission : null;
    if (mission && typeof mission.getSummary === 'function') {
      try { return mission.getSummary() || {}; } catch (err) { return {}; }
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
      try { return page.getWorkspaceTags(); } catch (err) { return []; }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.tags) ? ws.tags.map(tag => String(tag || '').trim()).filter(Boolean) : [];
  }

  workflowHref() {
    const page = this.page || {};
    if (typeof page.collectWorkspaceWorkflowReferences === 'function' && typeof page.buildBehaviorStudioHref === 'function') {
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
    return '<button type="button" class="' + escapeHtml(className) + '" data-cmd-section="' + escapeHtml(sectionKey) +
      '" aria-label="' + escapeHtml(ariaLabel) + '"><div class="ws-v">' + escapeHtml(value) +
      '</div><div class="ws-l">' + escapeHtml(label) + '</div></button>';
  }

  commandBarHTML(ws, name, mode, stats) {
    const description = String((ws && ws.description) || '').trim();
    const tags = this.workspaceTags();
    const isLongDescription = Array.from(description).length > 150;
    const descriptionClass = 'ws-cmd-description' +
      (!description ? ' is-empty' : '') +
      (isLongDescription && !this.identityExpanded ? ' is-collapsed' : '');
    const descriptionText = description || 'No description';
    const workflowHref = this.workflowHref();
    const workflowLabel = mode ? 'Workflow · ' + mode : '';
    const subtitle = this.commandSubtitle(mode);

    return (
      '<header class="ws-cmd-topbar">' +
      '<div class="ws-cmd-nav">' +
      '<a class="ws-cmd-nav-btn" href="/workspaces" aria-label="Back to workspaces">Workspaces</a>' +
      '<button type="button" class="ws-cmd-nav-btn" data-ws-cmd-detailed aria-label="Open detailed view">Detailed</button>' +
      '<a class="ws-cmd-nav-btn" href="' + escapeHtml(this.workspaceRoute('/canvas')) + '">Canvas</a>' +
      '<a class="ws-cmd-nav-btn" href="' + escapeHtml(this.workspaceRoute('/diagnostics')) + '">Diagnostics</a>' +
      '<a class="ws-cmd-nav-btn" href="' + escapeHtml(workflowHref) + '">Orchestration Skills</a>' +
      '</div>' +
      '<div class="ws-cmd-crest">' +
      '<svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">' +
      '<path d="M3 21V9l9-6 9 6v12"/><path d="M9 21v-6h6v6"/><path d="M3 21h18"/></svg>' +
      '</div>' +
      '<div class="ws-cmd-title">' +
      '<div class="ws-kicker"><span class="ws-dot"></span><span class="ws-tick">Outpost · Command</span></div>' +
      '<div class="ws-cmd-title-row">' +
      '<h2>' + escapeHtml(name) + '</h2>' +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="name" aria-label="Edit workspace name">Edit</button>' +
      '</div>' +
      '<div class="ws-sub" id="workspace-command-subtitle" data-workflow-label="' +
      escapeHtml(workflowLabel) + '"' + (subtitle ? '' : ' hidden') + '>' + escapeHtml(subtitle) + '</div>' +
      '<div class="ws-cmd-description-row">' +
      '<p class="' + descriptionClass + '">' + escapeHtml(descriptionText) + '</p>' +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="description" aria-label="Edit workspace description">Edit</button>' +
      (isLongDescription
        ? '<button type="button" class="ws-cmd-mini-btn" data-cmd-toggle-description aria-expanded="' +
          (this.identityExpanded ? 'true' : 'false') + '">' + (this.identityExpanded ? 'Less' : 'More') + '</button>'
        : '') +
      '</div>' +
      '<div class="ws-cmd-tags-row">' +
      '<div class="ws-cmd-tags">' + this.commandTagsHTML(tags) + '</div>' +
      '<button type="button" class="ws-cmd-mini-btn" data-cmd-edit-identity="tags" aria-label="Edit workspace tags">Edit</button>' +
      '</div>' +
      this.identityEditorHTML(name, description) +
      '</div>' +
      '<div class="ws-cmd-readout">' +
      this.statBoxHTML(stats.agents, 'Agents', 'agents', 'View agents') +
      this.statBoxHTML(stats.openTasks, 'Open Tasks', 'tasks', 'View open tasks', this.hasAttentionTasks() ? 'is-alert' : '') +
      this.statBoxHTML(stats.mcp, 'MCP', 'mcp', 'Open MCP settings') +
      this.statBoxHTML(stats.skills, 'Skills', 'skills', 'Open Skills settings') +
      '</div>' +
      '</header>'
    );
  }

  renderMissionPanel() {
    const summary = this.missionSummary();
    const missionText = String(summary.mission || '').trim();
    const title = summary.title || (missionText ? 'Current goal' : 'Workspace goal');
    const text = summary.text || (missionText || 'Loading workspace goal...');
    const statusLabel = summary.label || 'Loading';
    const statusClass = summary.className || 'is-loading';
    const cadence = summary.cadenceLabel || 'Cadence: loading';
    const nextRun = summary.nextLabel || 'Next: loading';
    const lastRun = summary.lastLabel || 'Last: loading';
    const findingsHref = summary.findingsHref || (this.workspaceId()
      ? '/action-center?workspace=' + encodeURIComponent(this.workspaceId())
      : '/action-center');
    const findingsLabel = summary.findingsLabel || 'Findings';
    const runDisabled = summary.canRun === true ? '' : ' disabled';
    const runTitle = summary.runTitle || 'Set a goal before running';
    const actionStatus = summary.actionStatus || '';

    return (
      '<section class="ws-cmd-mission" id="workspace-command-mission-card" aria-labelledby="workspace-command-mission-title">' +
      '<div class="ws-cmd-mission-main">' +
      '<div class="ws-cmd-mission-head">' +
      '<span class="ws-cmd-mission-kicker">Mission</span>' +
      '<span class="ws-cmd-mission-status ' + escapeHtml(statusClass) + '" id="workspace-command-mission-status">' +
      escapeHtml(statusLabel) + '</span>' +
      '</div>' +
      '<h3 id="workspace-command-mission-title" class="ws-cmd-mission-title">' + escapeHtml(title) + '</h3>' +
      '<p id="workspace-command-mission-text" class="ws-cmd-mission-text' + (missionText ? '' : ' is-empty') + '">' +
      escapeHtml(text) + '</p>' +
      '<div class="ws-cmd-mission-meta" aria-label="Mission automation timing">' +
      '<span id="workspace-command-mission-cadence">' + escapeHtml(cadence) + '</span>' +
      '<span id="workspace-command-mission-next-run">' + escapeHtml(nextRun) + '</span>' +
      '<span id="workspace-command-mission-last-run">' + escapeHtml(lastRun) + '</span>' +
      '</div>' +
      '<div class="ws-cmd-mission-action-status" id="workspace-command-mission-action-status" aria-live="polite">' +
      escapeHtml(actionStatus) + '</div>' +
      '</div>' +
      '<div class="ws-cmd-mission-actions">' +
      '<button type="button" class="ws-cmd-mission-btn" id="workspace-command-mission-edit" data-cmd-mission-action="edit">Set Goal</button>' +
      '<button type="button" class="ws-cmd-mission-btn is-primary" id="workspace-command-mission-run" data-cmd-mission-action="run"' +
      runDisabled + ' title="' + escapeHtml(runTitle) + '">Run now</button>' +
      '<a class="ws-cmd-mission-btn" id="workspace-command-mission-findings" href="' + escapeHtml(findingsHref) + '">' +
      escapeHtml(findingsLabel) + '</a>' +
      '</div>' +
      '</section>'
    );
  }

  commandTagsHTML(tags) {
    const arr = Array.isArray(tags) ? tags : [];
    if (!arr.length) return '<span class="ws-cmd-tag-empty">No tags</span>';
    const limit = 8;
    const shown = arr.slice(0, limit).map(tag => '<span class="ws-cmd-tag">' + escapeHtml(tag) + '</span>').join('');
    const more = arr.length > limit ? '<span class="ws-cmd-tag is-more">+' + (arr.length - limit) + '</span>' : '';
    return shown + more;
  }

  identityEditorHTML(name, description) {
    const mode = this.identityEditMode;
    if (!mode) return '';
    if (mode === 'name' || mode === 'description') {
      const isDescription = mode === 'description';
      const value = isDescription ? description : name;
      const field = isDescription
        ? '<textarea class="ws-cmd-identity-field" data-cmd-identity-input rows="2">' + escapeHtml(value) + '</textarea>'
        : '<input class="ws-cmd-identity-field" data-cmd-identity-input type="text" value="' + escapeHtml(value) + '">';
      return (
        '<form class="ws-cmd-identity-editor" data-cmd-identity-form="' + escapeHtml(mode) + '">' +
        field +
        '<div class="ws-cmd-identity-actions">' +
        '<button type="submit" class="ws-cmd-identity-save"' + (this.identitySaving ? ' disabled' : '') + '>Save</button>' +
        '<button type="button" class="ws-cmd-identity-cancel" data-cmd-cancel-identity>Cancel</button>' +
        '</div>' +
        '</form>'
      );
    }
    if (mode === 'tags') {
      return (
        '<div class="ws-cmd-identity-editor" data-cmd-identity-form="tags">' +
        '<div class="ws-cmd-tags-editor-mount" data-cmd-tags-mount></div>' +
        (this.commandTagError ? '<div class="ws-cmd-identity-error" role="alert">' + escapeHtml(this.commandTagError) + '</div>' : '') +
        '<div class="ws-cmd-identity-actions">' +
        '<button type="button" class="ws-cmd-identity-save" data-cmd-save-tags' + (this.identitySaving ? ' disabled' : '') + '>Save</button>' +
        '<button type="button" class="ws-cmd-identity-cancel" data-cmd-cancel-identity>Cancel</button>' +
        '</div>' +
        '</div>'
      );
    }
    return '';
  }

  render() {
    if (!this.container) return;
    if (this.commandTagInput) {
      try { this.commandTagDraft = this.commandTagInput.getTags(); } catch (err) { /* keep existing draft */ }
      this.destroyCommandTagInput();
    }
    const ws = (this.page && this.page.workspace) || {};
    const name = String(ws.name || 'Workspace');
    const mode = this.opsModeLabel();
    const stats = this.computeStats();

    this.container.innerHTML =
      this.commandBarHTML(ws, name, mode, stats) +
      '<div class="ws-cmd-layout">' +
      '<main class="ws-cmd-main">' +
      this.renderMissionPanel() +
      '<section class="ws-cmd-garrison">' + this.renderGarrison() + '</section>' +
      '</main>' +
      '<aside class="ws-cmd-rail">' + this.renderRail() + '</aside>' +
      '</div>';

    const back = this.container.querySelector('[data-ws-cmd-detailed]');
    if (back) back.addEventListener('click', () => this.deactivate());
    this.bindIdentityControls();
    this.bindReadout();
    this.bindMissionPanel();
    this.bindGarrison();
    this.bindRail();
    this.mountCommandTagInput();
    this.syncMissionPanel();

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

    root.addEventListener('click', (event) => {
      const editBtn = event.target.closest('[data-cmd-edit-identity]');
      if (editBtn) {
        this.startIdentityEdit(editBtn.getAttribute('data-cmd-edit-identity'));
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

    root.addEventListener('submit', (event) => {
      const form = event.target.closest('[data-cmd-identity-form]');
      if (!form) return;
      event.preventDefault();
      const mode = form.getAttribute('data-cmd-identity-form');
      if (mode === 'name' || mode === 'description') {
        const input = form.querySelector('[data-cmd-identity-input]');
        this.saveIdentityField(mode, input ? input.value : '');
      }
    });

    root.addEventListener('keydown', (event) => {
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
      try { input.focus({ preventScroll: true }); } catch (err) { input.focus(); }
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
    const currentValue = mode === 'description' ? String(ws.description || '') : String(ws.name || '');
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
    try { this.commandTagInput.destroy?.(); } catch (err) { /* no-op */ }
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
    root.addEventListener('click', (event) => {
      const sectionBtn = event.target.closest('[data-cmd-section]');
      if (!sectionBtn) return;
      this.openStatModal(sectionBtn.getAttribute('data-cmd-section'), sectionBtn);
    });
  }

  bindMissionPanel() {
    const root = this.container && this.container.querySelector('.ws-cmd-mission');
    if (!root) return;
    root.addEventListener('click', (event) => {
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
    if (mission && typeof mission.renderCommandSurfaces === 'function') {
      mission.renderCommandSurfaces();
    }
  }

  /** Escape hatch: leave the Command view for the full detailed section. */
  openDetailedSection(sectionKey) {
    const section = String(sectionKey || '').trim();
    if (!section) return;
    this.deactivate({ persist: false });
    if (this.page && typeof this.page.focusSection === 'function') {
      this.page.focusSection(section);
    }
  }

  // ---------- stat manager modal (agents / tasks / mcp / skills) ----------

  statSectionMeta(section) {
    switch (String(section || '')) {
      case 'agents': return { title: 'Agents', addLabel: '＋ Add Agent' };
      case 'tasks': return { title: this.taskModalShowAll ? 'Tasks' : 'Open Tasks', addLabel: '＋ Add Task' };
      case 'mcp': return { title: 'MCP Servers', addLabel: '＋ Add MCP' };
      case 'skills': return { title: 'Skills', addLabel: '＋ Add Skill' };
      default: return null;
    }
  }

  openStatModal(sectionKey, trigger) {
    const section = String(sectionKey || '').trim();
    if (!this.statSectionMeta(section)) return;
    this.statModalSection = section;
    if (section === 'tasks') this.taskModalShowAll = false;
    this.statModalTrigger = trigger || null;
    const el = this.ensureStatModal();
    if (!el) return;
    this.renderStatModalBody();
    el.hidden = false;
    this.setCommandBackgroundInert(true);
    const panel = el.querySelector('.ws-cmd-modal-panel');
    if (panel && typeof panel.focus === 'function') {
      try { panel.focus({ preventScroll: true }); } catch (err) { panel.focus(); }
    }
  }

  closeStatModal() {
    const trigger = this.statModalTrigger;
    this.statModalSection = '';
    this.statModalTrigger = null;
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
        try { child.inert = true; } catch (err) { /* inert may be readonly in tests */ }
      } else {
        child.removeAttribute('aria-hidden');
        try { child.inert = false; } catch (err) { /* inert may be readonly in tests */ }
      }
    });
  }

  modalFocusableElements() {
    const panel = this.statModalEl && this.statModalEl.querySelector('.ws-cmd-modal-panel');
    if (!panel || typeof panel.querySelectorAll !== 'function') return [];
    return Array.from(panel.querySelectorAll(
      'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )).filter(el => el && !el.hidden && typeof el.focus === 'function');
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
    if (typeof document === 'undefined' || typeof document.createElement !== 'function') return null;
    const el = document.createElement('div');
    el.className = 'ws-cmd-modal';
    el.hidden = true;
    el.innerHTML =
      '<div class="ws-cmd-modal-backdrop" data-cmd-modal-action="close"></div>' +
      '<div class="ws-cmd-modal-panel" role="dialog" aria-modal="true" tabindex="-1"></div>';
    el.addEventListener('click', (event) => {
      const btn = event.target.closest('[data-cmd-modal-action]');
      if (!btn) return;
      this.handleStatModalAction(btn.getAttribute('data-cmd-modal-action'), btn.getAttribute('data-cmd-id'));
    });
    el.addEventListener('keydown', (event) => {
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
    panel.innerHTML = this.statModalHTML(this.statModalSection);
  }

  statModalHTML(section) {
    const meta = this.statSectionMeta(section);
    if (!meta) return '';
    return (
      '<header class="ws-cmd-modal-head">' +
      '<h3 class="ws-cmd-modal-title">' + escapeHtml(meta.title) + '</h3>' +
      '<span class="ws-cmd-modal-count">' + this.statModalCount(section) + '</span>' +
      '<div class="ws-cmd-modal-head-actions">' +
      this.statModalFilterToggleHTML(section) +
      '<button type="button" class="ws-cmd-modal-add" data-cmd-modal-action="add">' + escapeHtml(meta.addLabel) + '</button>' +
      '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close manager">×</button>' +
      '</div>' +
      '</header>' +
      '<div class="ws-cmd-modal-body">' + this.statModalRows(section) + '</div>' +
      '<footer class="ws-cmd-modal-foot">' +
      '<button type="button" class="ws-cmd-modal-detailed" data-cmd-modal-action="detailed">Open in detailed view ▸</button>' +
      '</footer>'
    );
  }

  statModalFilterToggleHTML(section) {
    if (String(section || '') !== 'tasks') return '';
    const label = this.taskModalShowAll ? 'Show open' : 'Show all';
    const pressed = this.taskModalShowAll ? 'true' : 'false';
    return '<button type="button" class="ws-cmd-modal-filter" data-cmd-modal-action="toggle-task-filter" aria-pressed="' +
      pressed + '">' + escapeHtml(label) + '</button>';
  }

  statModalCount(section) {
    switch (String(section || '')) {
      case 'agents': return this.agentRowData().length;
      case 'tasks': return this.taskRowData({ includeAll: this.taskModalShowAll }).length;
      case 'mcp': return this.mcpRowData().length;
      case 'skills': return this.skillRowData().length;
      default: return 0;
    }
  }

  statModalRows(section) {
    switch (String(section || '')) {
      case 'agents': return this.agentRowsHTML();
      case 'tasks': return this.taskRowsHTML();
      case 'mcp': return this.mcpRowsHTML();
      case 'skills': return this.skillRowsHTML();
      default: return '';
    }
  }

  modalEmptyHTML(text) {
    return '<div class="ws-cmd-modal-empty">' + escapeHtml(text) + '</div>';
  }

  agentRowData() {
    const page = this.page || {};
    let groups = [];
    try { groups = page.buildAgentGroups() || []; } catch (err) { groups = []; }
    return groups.filter((g) => g && g.isWorkspaceAgent && !g.isUnassigned);
  }

  agentRowsHTML() {
    const page = this.page || {};
    const groups = this.agentRowData();
    if (!groups.length) return this.modalEmptyHTML('No agents yet. Add one to build the roster.');
    return groups.map((group) => {
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
        '<span class="ws-cmd-mchip">' + escapeHtml(modelLabel || '—') + '</span>' +
        '<span class="ws-cmd-mchip">Skills · ' + skillCount + '</span>';
      const removeCtl = keeper
        ? '<span class="ws-cmd-lock" title="Entry agent — can\'t be removed">🔒</span>'
        : '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
          escapeHtml(encoded) + '" title="Remove agent" aria-label="Remove ' + escapeHtml(name) + '">✕</button>';
      return (
        '<div class="ws-cmd-mrow">' +
        '<span class="ws-cmd-mrow-av" style="' + escapeHtml(avatar.style || '') + '">' + escapeHtml(avatar.initials) + '</span>' +
        '<div class="ws-cmd-mrow-main">' +
        '<div class="ws-cmd-mrow-name"><span class="ws-cmd-led ' + tone + '"></span>' + escapeHtml(name) + '</div>' +
        '<div class="ws-cmd-mrow-chips">' + chips + '</div>' +
        '</div>' +
        '<div class="ws-cmd-mrow-actions">' +
        '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="edit" data-cmd-id="' +
        escapeHtml(encoded) + '" title="Edit model" aria-label="Edit model for ' + escapeHtml(name) + '">Model</button>' +
        removeCtl +
        '</div>' +
        '</div>'
      );
    }).join('');
  }

  isOpenTask(task) {
    const status = String((task && task.status) || '').toLowerCase();
    return status === 'pending' || status === 'in_progress';
  }

  taskRowData({ includeAll = false } = {}) {
    const page = this.page || {};
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    return includeAll ? tasks : tasks.filter((task) => this.isOpenTask(task));
  }

  taskRowsHTML() {
    const tasks = this.taskRowData({ includeAll: this.taskModalShowAll });
    if (!tasks.length) {
      return this.modalEmptyHTML(
        this.taskModalShowAll ? 'No tasks yet. Add one to get started.' : 'No open tasks. Use Show all to view completed tasks.'
      );
    }
    return tasks.map((t) => {
      const id = String(t.id || '');
      const label = String(t.description || t.name || t.title || 'Task');
      const assignee = String(t.to || t.agent_name || t.assigned_to || '');
      const tone = this.taskTone(t.status);
      const statusText = String(t.status || 'pending').replace('_', ' ');
      return (
        '<div class="ws-cmd-mrow">' +
        '<div class="ws-cmd-mrow-main">' +
        '<div class="ws-cmd-mrow-name">' + escapeHtml(label) + '</div>' +
        '<div class="ws-cmd-mrow-chips">' +
        '<span class="ws-cmd-mchip ws-cmd-q-status ' + tone + '">' + escapeHtml(statusText) + '</span>' +
        (assignee ? '<span class="ws-cmd-mchip">' + escapeHtml(assignee) + '</span>' : '') +
        '</div>' +
        '</div>' +
        '<div class="ws-cmd-mrow-actions">' +
        '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="run" data-cmd-id="' +
        escapeHtml(id) + '" title="Run" aria-label="Run task ' + escapeHtml(label) + '">▶</button>' +
        '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="open" data-cmd-id="' +
        escapeHtml(id) + '" title="Open" aria-label="Open task ' + escapeHtml(label) + '">↗</button>' +
        '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
        escapeHtml(id) + '" title="Delete" aria-label="Delete task ' + escapeHtml(label) + '">✕</button>' +
        '</div>' +
        '</div>'
      );
    }).join('');
  }

  mcpRowData() {
    const page = this.page || {};
    if (typeof page.getWorkspaceMCPBindings === 'function') {
      try { return page.getWorkspaceMCPBindings({ includeDisabled: true }) || []; } catch (err) { /* fall through */ }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.mcp_bindings) ? ws.mcp_bindings : [];
  }

  mcpRowsHTML() {
    const bindings = this.mcpRowData();
    if (!bindings.length) return this.modalEmptyHTML('No MCP servers bound yet.');
    return bindings.map((b) => {
      const id = String(b.id || '');
      const serverName = String(b.serverName || b.server_name || 'unknown');
      const alias = String(b.alias || '');
      const isDisabled = b.enabled === false;
      const isSynth = b.source === 'synthesized';
      const canRemove = b.source === 'workspace';
      const chips =
        '<span class="ws-cmd-mchip ' + (isDisabled ? 'is-disabled' : 'is-on') + '">' +
        (isDisabled ? 'Disabled' : 'Enabled') + '</span>' +
        '<span class="ws-cmd-mchip">' + (isSynth ? 'Synthesized' : 'Explicit') + '</span>' +
        (alias ? '<span class="ws-cmd-mchip">' + escapeHtml(alias) + '</span>' : '');
      const removeBtn = canRemove
        ? '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
          escapeHtml(id) + '" title="Remove" aria-label="Remove ' + escapeHtml(serverName) + '">✕</button>'
        : '';
      return (
        '<div class="ws-cmd-mrow">' +
        '<div class="ws-cmd-mrow-main">' +
        '<div class="ws-cmd-mrow-name">' + escapeHtml(serverName) + '</div>' +
        '<div class="ws-cmd-mrow-chips">' + chips + '</div>' +
        '</div>' +
        '<div class="ws-cmd-mrow-actions">' +
        '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="edit" data-cmd-id="' +
        escapeHtml(id) + '" title="Edit binding" aria-label="Edit ' + escapeHtml(serverName) + '">Edit</button>' +
        removeBtn +
        '</div>' +
        '</div>'
      );
    }).join('');
  }

  skillRowData() {
    const page = this.page || {};
    if (typeof page.getWorkspaceSkillBindings === 'function') {
      try { return page.getWorkspaceSkillBindings({ includeDisabled: true }) || []; } catch (err) { /* fall through */ }
    }
    const ws = page.workspace || {};
    return Array.isArray(ws.skill_bindings) ? ws.skill_bindings : [];
  }

  skillRowsHTML() {
    const bindings = this.skillRowData();
    if (!bindings.length) return this.modalEmptyHTML('No skills bound yet.');
    return bindings.map((b) => {
      const id = String(b.id || '');
      const skillName = String(b.skillName || b.skill_name || 'unknown');
      const isDisabled = b.enabled === false;
      const isPlanning = b.planningProfile === true;
      const isTrusted = b.trusted === true;
      const chips =
        '<span class="ws-cmd-mchip ' + (isDisabled ? 'is-disabled' : 'is-on') + '">' +
        (isDisabled ? 'Disabled' : 'Enabled') + '</span>' +
        (isPlanning ? '<span class="ws-cmd-mchip">Planning</span>' : '') +
        (isTrusted ? '<span class="ws-cmd-mchip">Trusted</span>' : '');
      return (
        '<div class="ws-cmd-mrow">' +
        '<div class="ws-cmd-mrow-main">' +
        '<div class="ws-cmd-mrow-name">' + escapeHtml(skillName) + '</div>' +
        '<div class="ws-cmd-mrow-chips">' + chips + '</div>' +
        '</div>' +
        '<div class="ws-cmd-mrow-actions">' +
        '<button type="button" class="ws-cmd-mrow-btn" data-cmd-modal-action="edit" data-cmd-id="' +
        escapeHtml(id) + '" title="Edit binding" aria-label="Edit ' + escapeHtml(skillName) + '">Edit</button>' +
        '<button type="button" class="ws-cmd-mrow-btn is-danger" data-cmd-modal-action="delete" data-cmd-id="' +
        escapeHtml(id) + '" title="Remove" aria-label="Remove ' + escapeHtml(skillName) + '">✕</button>' +
        '</div>' +
        '</div>'
      );
    }).join('');
  }

  handleStatModalAction(action, id) {
    const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null) || {};
    const section = this.statModalSection;
    const a = String(action || '');
    if (a === 'close') { this.closeStatModal(); return; }
    if (a === 'toggle-task-filter' && section === 'tasks') {
      this.taskModalShowAll = !this.taskModalShowAll;
      this.renderStatModalBody();
      return;
    }
    if (a === 'detailed') {
      this.closeStatModal();
      this.openDetailedSection(section);
      return;
    }
    // Add/Edit hand off to an existing modal, so close ours first (avoids stacked
    // overlays). Run/Delete stay in place; the page reload triggers refresh().
    switch (section) {
      case 'agents':
        if (a === 'add') { this.closeStatModal(); if (typeof page.openAddAgentModal === 'function') page.openAddAgentModal(); }
        else if (a === 'edit') { this.closeStatModal(); if (typeof page.openAgentModelModal === 'function') page.openAgentModelModal(id); }
        else if (a === 'delete') { if (typeof page.removeAgentFromWorkspace === 'function') page.removeAgentFromWorkspace(id); }
        break;
      case 'tasks':
        if (a === 'add') { this.closeStatModal(); if (typeof page.showAddTaskModal === 'function') page.showAddTaskModal(); }
        else if (a === 'run') { if (typeof page.executeTask === 'function') page.executeTask(id); }
        else if (a === 'open') { if (typeof page.openTask === 'function') page.openTask(id); }
        else if (a === 'delete') { if (typeof page.deleteTask === 'function') page.deleteTask(id); }
        break;
      case 'mcp':
        if (a === 'add') { this.closeStatModal(); if (typeof page.openWorkspaceMCPModal === 'function') page.openWorkspaceMCPModal(); }
        else if (a === 'edit') { this.closeStatModal(); if (typeof page.openWorkspaceMCPModal === 'function') page.openWorkspaceMCPModal(id); }
        else if (a === 'delete') { if (typeof page.deleteWorkspaceMCPBinding === 'function') page.deleteWorkspaceMCPBinding(id); }
        break;
      case 'skills':
        if (a === 'add') { this.closeStatModal(); if (typeof page.openWorkspaceSkillModal === 'function') page.openWorkspaceSkillModal(); }
        else if (a === 'edit') { this.closeStatModal(); if (typeof page.openWorkspaceSkillModal === 'function') page.openWorkspaceSkillModal(id); }
        else if (a === 'delete') { if (typeof page.deleteWorkspaceSkillBinding === 'function') page.deleteWorkspaceSkillBinding(id); }
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
        if (page && typeof page.showAddDirectoryModal === 'function') page.showAddDirectoryModal(triggerButton);
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
        const note = (Array.isArray(page.notes) ? page.notes : []).find(item => String(item.id || '') === id);
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
      default:
        break;
    }
  }

  // ---------- garrison (agents as unit cards) ----------

  statusTone(statusKey, statusLabel) {
    const s = (String(statusKey || '') + ' ' + String(statusLabel || '')).toLowerCase();
    if (/work|run|busy|active|progress/.test(s)) return 'working';
    if (/error|fail|blocked/.test(s)) return 'alert';
    return 'idle';
  }

  taskTone(status) {
    switch (String(status || '').toLowerCase()) {
      case 'in_progress': return 'working';
      case 'failed': case 'cancelled': return 'alert';
      case 'completed': case 'done': return 'done';
      default: return 'pending';
    }
  }

  unitCardHTML(group) {
    const page = this.page;
    const name = String(group.name || 'Agent');
    const encoded = encodeURIComponent(name);
    const keeper = !group.isUnassigned && page.isWorkspaceEntryAgent
      ? page.isWorkspaceEntryAgent(name) : false;

    const avatar = page.getAgentAvatarPresentation
      ? page.getAgentAvatarPresentation(name)
      : { initials: name.slice(0, 2).toUpperCase(), style: '' };
    const status = !group.isUnassigned && page.getAgentRosterStatus
      ? page.getAgentRosterStatus(name)
      : { key: 'idle', label: 'Unassigned' };
    const tone = this.statusTone(status.key, status.label);

    let modelLabel = '';
    if (!group.isUnassigned && page.getAgentProfile && page.getAgentModelPresentation) {
      const m = page.getAgentModelPresentation(page.getAgentProfile(name));
      modelLabel = m && !m.empty ? m.model : '';
    }
    let skillCount = 0;
    if (!group.isUnassigned && page.getAgentSkillSummary) {
      const sk = page.getAgentSkillSummary(name);
      skillCount = (sk && sk.count) || 0;
    }

    const roleBadge = group.isUnassigned
      ? '<span class="ws-cmd-badge">Unassigned</span>'
      : (keeper
          ? '<span class="ws-cmd-badge is-keeper">★ Entry Agent</span>'
          : '');

    const ctl = group.isUnassigned
      ? ''
      : (keeper
          ? '<span class="ws-cmd-lock" title="Entry agent — locked, can\'t be removed">🔒</span>'
          : '') +
        '<button type="button" class="ws-cmd-icon-btn" data-cmd-add-task="' + escapeHtml(encoded) +
        '" title="Add a task for ' + escapeHtml(name) + '" aria-label="Add a task for ' + escapeHtml(name) + '">＋</button>';

    const rows = group.isUnassigned ? '' :
      '<div class="ws-cmd-unit-rows">' +
      '<div class="ws-cmd-row"><span class="ws-cmd-rk">Model</span><span class="ws-cmd-rv">' +
      escapeHtml(modelLabel || '—') + '</span></div>' +
      '<div class="ws-cmd-row"><span class="ws-cmd-rk">Skills</span><span class="ws-cmd-rv">' +
      skillCount + '</span></div>' +
      '</div>';

    return (
      '<article class="ws-cmd-unit' + (keeper ? ' is-keeper' : '') + '">' +
      '<div class="ws-cmd-unit-top">' +
      '<span class="ws-cmd-av" style="' + escapeHtml(avatar.style || '') + '">' + escapeHtml(avatar.initials) + '</span>' +
      '<div class="ws-cmd-unit-id"><div class="ws-cmd-unit-name">' + escapeHtml(name) + '</div>' +
      '<div class="ws-cmd-unit-role">' + roleBadge +
      '<span class="ws-cmd-state"><span class="ws-cmd-led ' + tone + '"></span>' + escapeHtml(status.label || 'Idle') + '</span>' +
      '</div></div>' +
      '<div class="ws-cmd-unit-ctl">' + ctl + '</div>' +
      '</div>' +
      rows +
      this.questLogHTML(group, encoded) +
      '</article>'
    );
  }

  questLogHTML(group, encoded) {
    const tasks = Array.isArray(group.tasks) ? group.tasks : [];
    const add = group.isUnassigned ? '' :
      '<button type="button" class="ws-cmd-icon-btn sm" data-cmd-add-task="' + escapeHtml(encoded) +
      '" aria-label="Add task">＋</button>';
    const head = '<div class="ws-cmd-ql-head"><span class="ws-cmd-ql-t">Tasks · ' + tasks.length + '</span>' + add + '</div>';
    if (!tasks.length) {
      return '<div class="ws-cmd-questlog">' + head + '<div class="ws-cmd-ql-empty">— no tasks yet —</div></div>';
    }
    const items = tasks.map((t) => {
      const label = String(t.description || t.name || t.title || 'Task');
      const tone = this.taskTone(t.status);
      const statusText = String(t.status || 'pending').replace('_', ' ');
      const taskId = String(t.id || '');
      return (
        '<div class="ws-cmd-quest">' +
        '<span class="ws-cmd-q-glyph">&bull;</span>' +
        '<button type="button" class="ws-cmd-q-name" data-cmd-open-task="' + escapeHtml(taskId) +
        '" aria-label="Open task ' + escapeHtml(label) + '">' + escapeHtml(label) + '</button>' +
        '<span class="ws-cmd-q-status ' + tone + '">' + escapeHtml(statusText) + '</span>' +
        '<button type="button" class="ws-cmd-q-run" data-cmd-run-task="' + escapeHtml(taskId) +
        '" title="Run" aria-label="Run task ' + escapeHtml(label) + '">▶</button>' +
        '</div>'
      );
    }).join('');
    return '<div class="ws-cmd-questlog">' + head + items + '</div>';
  }

  renderGarrison() {
    const page = this.page;
    let groups = [];
    try { groups = page.buildAgentGroups() || []; } catch (err) { groups = []; }
    const units = groups.filter((g) => g && (g.isWorkspaceAgent || (g.isUnassigned && (g.tasks || []).length)));
    if (!units.length) {
      return '<div class="ws-cmd-soon">No agents yet.</div>';
    }
    const gridClass = 'ws-cmd-garrison-grid' + (units.length > 1 && units.length % 2 === 1 ? ' is-odd' : '');
    return '<div class="' + gridClass + '">' + units.map((g) => this.unitCardHTML(g)).join('') + '</div>';
  }

  bindGarrison() {
    const root = this.container && this.container.querySelector('.ws-cmd-garrison');
    if (!root) return;
    root.addEventListener('click', (event) => {
      const page = this.page || (typeof window !== 'undefined' ? window.workspaceDetail : null);
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
      }
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
      '<section class="ws-cmd-panel' + (isManaging ? ' is-managing' : '') + (!hasItems ? ' is-empty' : '') + '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>' + escapeHtml(title) + '</h4><span class="ws-cmd-panel-count">' + count + '</span></div>' +
      '<div class="ws-cmd-panel-tools">' +
      '<button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="' +
        escapeHtml(sectionKey) + '">' + escapeHtml(primaryLabel) + '</button>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="' + escapeHtml(sectionKey) +
      '" aria-expanded="' + (isManaging ? 'true' : 'false') +
      '" title="' + (isManaging ? 'Close Command manager' : 'Manage in Command view') +
      '" aria-label="' + (isManaging ? 'Close ' : 'Manage ') +
      escapeHtml(title) + ' in Command view">' + (isManaging ? '×' : '▸') + '</button>' +
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
    const items = shown.map((it) => {
      const label = escapeHtml(labelOf(it));
      const meta = attr.metaOf ? escapeHtml(attr.metaOf(it)) : '';
      const inner = '<span class="ws-cmd-rail-t">' + label + '</span>' +
        (meta ? '<span class="ws-cmd-rail-m">' + meta + '</span>' : '');
      if (attr.href) {
        return '<a class="ws-cmd-rail-item" href="' + escapeHtml(attr.href(it)) + '">' + inner + '</a>';
      }
      if (attr.action) {
        return '<button type="button" class="ws-cmd-rail-item" ' + attr.action(it) + '>' + inner + '</button>';
      }
      return '<div class="ws-cmd-rail-item is-static">' + inner + '</div>';
    });
    if (arr.length > shown.length) {
      items.push('<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="' +
        escapeHtml(attr.sectionKey || '') + '">+ ' +
        (arr.length - shown.length) + ' more</button>');
    }
    return items;
  }

  getWorkspaceProjectPath() {
    const page = this.page || {};
    if (typeof page.getWorkspaceProjectPath === 'function') {
      try { return String(page.getWorkspaceProjectPath() || '').trim(); } catch (err) { return ''; }
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
    return [{
      id: '__project_path__',
      name: this.folderDisplayName(projectPath),
      path: projectPath,
      source: 'project_path',
      isProjectPathOnly: true
    }];
  }

  folderRole(dir) {
    const page = this.page || {};
    if (dir && dir.isProjectPathOnly) return { label: 'Project Folder', className: 'is-project' };
    let primaryDirectoryId = '';
    try {
      primaryDirectoryId = typeof page.getPrimaryDirectoryId === 'function' ? String(page.getPrimaryDirectoryId() || '') : '';
    } catch (err) {
      primaryDirectoryId = '';
    }
    let isProject = false;
    try {
      isProject = typeof page.isProjectDirectory === 'function' ? Boolean(page.isProjectDirectory(dir)) : false;
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
    const items = shown.map((dir) => {
      const id = String(dir.id || '');
      const role = this.folderRole(dir);
      const name = dir.title || dir.name || dir.path || 'Unnamed Directory';
      const path = String(dir.path || '');
      const source = String(dir.source || 'reference');
      const inner =
        '<span class="ws-cmd-rail-line"><span class="ws-cmd-rail-t">' + escapeHtml(name) + '</span>' +
        '<span class="ws-cmd-rail-role ' + escapeHtml(role.className) + '">' + escapeHtml(role.label) + '</span></span>' +
        (path ? '<span class="ws-cmd-rail-m">' + escapeHtml(path) + '</span>' : '');
      if (dir.isProjectPathOnly) {
        return '<div class="ws-cmd-rail-item is-static">' + inner + '</div>';
      }
      return '<button type="button" class="ws-cmd-rail-item" data-cmd-open-section="folders" data-cmd-item-id="' +
        escapeHtml(id) + '" data-cmd-item-source="' + escapeHtml(source) + '">' + inner + '</button>';
    });
    if (arr.length > shown.length) {
      items.push('<button type="button" class="ws-cmd-rail-more" data-cmd-manage-section="folders">+ ' +
        (arr.length - shown.length) + ' more</button>');
    }
    return items;
  }

  renderRail() {
    const page = this.page || {};
    const notes = Array.isArray(page.notes) ? page.notes : [];
    const schedules = Array.isArray(page.schedules) ? page.schedules : [];
    const sessions = Array.isArray(page.sessions) ? page.sessions : [];
    const dirs = this.folderRowData();

    const notesExpanded = this.activeRailSection === 'notes';
    const schedulesExpanded = this.activeRailSection === 'schedules';
    const sessionsExpanded = this.activeRailSection === 'sessions';
    const foldersExpanded = this.activeRailSection === 'folders';

    const notesItems = this.railItems(notes, (n) => n.name || n.title || 'Untitled Note', {
      sectionKey: 'notes',
      expanded: notesExpanded,
      action: (n) => 'data-cmd-open-section="notes" data-cmd-item-id="' + escapeHtml(String(n.id || '')) + '"'
    });
    const scheduleItems = this.railItems(schedules, (s) => s.name || s.task_description || 'Unnamed Schedule', {
      sectionKey: 'schedules',
      expanded: schedulesExpanded,
      action: (s) => 'data-cmd-open-section="schedules" data-cmd-item-id="' + escapeHtml(String(s.id || '')) + '"'
    });
    const sessionItems = this.railItems(sessions, (s) => s.title || s.name || 'Untitled Session', {
      sectionKey: 'sessions',
      expanded: sessionsExpanded,
      action: (s) => 'data-cmd-open-section="sessions" data-cmd-item-id="' + escapeHtml(String(s.id || '')) + '"',
      metaOf: (s) => s.agent_name || ''
    });
    const folderItems = this.folderRailItems(dirs, foldersExpanded);

    return (
      this.railPanelHTML('notes', 'Notes', notesItems, notes.length, 'No notes yet.', 'New Note') +
      this.railPanelHTML('schedules', 'Schedules', scheduleItems, schedules.length, 'No schedules yet.', 'Open Schedules') +
      this.railPanelHTML('sessions', 'Sessions', sessionItems, sessions.length, 'No sessions yet.', 'New Session') +
      this.railPanelHTML('folders', 'Linked Folders', folderItems, dirs.length, 'No linked folders yet.', 'Link Folder')
    );
  }

  bindRail() {
    const root = this.container && this.container.querySelector('.ws-cmd-rail');
    if (!root) return;
    root.addEventListener('click', (event) => {
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
}
