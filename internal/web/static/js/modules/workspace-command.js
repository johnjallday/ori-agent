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

function statBox(value, label, sectionKey, ariaLabel) {
  return '<button type="button" class="ws-cmd-stat" data-cmd-section="' + escapeHtml(sectionKey) +
    '" aria-label="' + escapeHtml(ariaLabel) + '"><div class="ws-v">' + escapeHtml(value) +
    '</div><div class="ws-l">' + escapeHtml(label) + '</div></button>';
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
    this.setup();
  }

  setup() {
    if (!this.container || !this.toggleBtn) return;
    this.toggleBtn.hidden = false; // reveal (Phase 1)
    this.toggleBtn.addEventListener('click', () => this.toggle());
    let pref = '';
    try { pref = localStorage.getItem(STORAGE_KEY) || ''; } catch (err) { pref = ''; }
    if (pref === 'command') this.activate();
    else this.deactivate();
  }

  persist(view) {
    try { localStorage.setItem(STORAGE_KEY, view); } catch (err) { /* storage may be unavailable */ }
  }

  toggle() {
    if (this.active) this.deactivate();
    else this.activate();
  }

  activate() {
    this.active = true;
    this.render();
    if (this.container) this.container.hidden = false;
    if (this.detailedView) this.detailedView.hidden = true;
    this.updateToggle();
    this.persist('command');
  }

  deactivate({ persist = true } = {}) {
    this.active = false;
    this.closeStatModal();
    if (this.container) this.container.hidden = true;
    if (this.detailedView) this.detailedView.hidden = false;
    this.updateToggle();
    if (persist) this.persist('detailed');
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
    const tasks = Array.isArray(page.tasks) ? page.tasks : [];
    const openTasks = tasks.filter((t) => {
      const s = String((t && t.status) || '').toLowerCase();
      return s === 'pending' || s === 'in_progress' || s === 'assigned';
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

  render() {
    if (!this.container) return;
    const ws = (this.page && this.page.workspace) || {};
    const name = String(ws.name || 'Workspace');
    const mode = this.opsModeLabel();
    const stats = this.computeStats();

    this.container.innerHTML =
      '<header class="ws-cmd-topbar">' +
      '<button type="button" class="ws-cmd-back" data-ws-cmd-detailed aria-label="Back to detailed view">◂ Detailed</button>' +
      '<div class="ws-cmd-crest">' +
      '<svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4">' +
      '<path d="M3 21V9l9-6 9 6v12"/><path d="M9 21v-6h6v6"/><path d="M3 21h18"/></svg>' +
      '</div>' +
      '<div class="ws-cmd-title">' +
      '<div class="ws-kicker"><span class="ws-dot"></span><span class="ws-tick">Outpost · Command</span></div>' +
      '<h2>' + escapeHtml(name) + '</h2>' +
      (mode ? '<div class="ws-sub">Workflow · ' + escapeHtml(mode) + '</div>' : '') +
      '</div>' +
      '<div class="ws-cmd-readout">' +
      statBox(stats.agents, 'Agents', 'agents', 'View agents') +
      statBox(stats.openTasks, 'Tasks', 'tasks', 'View tasks') +
      statBox(stats.mcp, 'MCP', 'mcp', 'Open MCP settings') +
      statBox(stats.skills, 'Skills', 'skills', 'Open Skills settings') +
      '</div>' +
      '</header>' +
      '<div class="ws-cmd-layout">' +
      '<section class="ws-cmd-garrison">' + this.renderGarrison() + '</section>' +
      '<aside class="ws-cmd-rail">' + this.renderRail() + '</aside>' +
      '</div>';

    const back = this.container.querySelector('[data-ws-cmd-detailed]');
    if (back) back.addEventListener('click', () => this.deactivate());
    this.bindReadout();
    this.bindGarrison();
    this.bindRail();

    // The stat manager modal lives inside the .ws-cmd container (so it inherits
    // the tactical tokens) but survives full re-renders: re-attach + repaint it.
    if (this.statModalEl && this.container && this.container.appendChild) {
      this.container.appendChild(this.statModalEl);
      if (this.statModalSection) this.renderStatModalBody();
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
      case 'tasks': return { title: 'Tasks', addLabel: '＋ Add Task' };
      case 'mcp': return { title: 'MCP Servers', addLabel: '＋ Add MCP' };
      case 'skills': return { title: 'Skills', addLabel: '＋ Add Skill' };
      default: return null;
    }
  }

  openStatModal(sectionKey, trigger) {
    const section = String(sectionKey || '').trim();
    if (!this.statSectionMeta(section)) return;
    this.statModalSection = section;
    this.statModalTrigger = trigger || null;
    const el = this.ensureStatModal();
    if (!el) return;
    this.renderStatModalBody();
    el.hidden = false;
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
    if (trigger && typeof trigger.focus === 'function') trigger.focus();
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
      }
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

  statModalCount(section) {
    switch (String(section || '')) {
      case 'agents': return this.agentRowData().length;
      case 'tasks': return this.taskRowData().length;
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

  taskRowData() {
    const page = this.page || {};
    return Array.isArray(page.tasks) ? page.tasks : [];
  }

  taskRowsHTML() {
    const tasks = this.taskRowData();
    if (!tasks.length) return this.modalEmptyHTML('No tasks yet. Add one to get started.');
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
      return (
        '<div class="ws-cmd-quest">' +
        '<span class="ws-cmd-q-glyph">&bull;</span>' +
        '<span class="ws-cmd-q-name">' + escapeHtml(label) + '</span>' +
        '<span class="ws-cmd-q-status ' + tone + '">' + escapeHtml(statusText) + '</span>' +
        '<button type="button" class="ws-cmd-q-run" data-cmd-run-task="' + escapeHtml(String(t.id || '')) +
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
    return '<div class="ws-cmd-garrison-grid">' + units.map((g) => this.unitCardHTML(g)).join('') + '</div>';
  }

  bindGarrison() {
    const root = this.container && this.container.querySelector('.ws-cmd-garrison');
    if (!root) return;
    root.addEventListener('click', (event) => {
      const addBtn = event.target.closest('[data-cmd-add-task]');
      if (addBtn && window.workspaceDetail && window.workspaceDetail.showAddTaskModalForAgent) {
        window.workspaceDetail.showAddTaskModalForAgent(addBtn.getAttribute('data-cmd-add-task'));
        return;
      }
      const runBtn = event.target.closest('[data-cmd-run-task]');
      if (runBtn && window.workspaceDetail && window.workspaceDetail.executeTask) {
        window.workspaceDetail.executeTask(runBtn.getAttribute('data-cmd-run-task'));
      }
    });
  }

  // ---------- right rail ----------

  railPanelHTML(sectionKey, title, items, count, emptyText, primaryLabel) {
    const isManaging = this.activeRailSection === sectionKey;
    const body = items.length
      ? items.join('')
      : '<div class="ws-cmd-rail-empty">' + escapeHtml(emptyText) + '</div>';
    const actions = isManaging
      ? '<div class="ws-cmd-panel-actions"><button type="button" class="ws-cmd-panel-action" data-cmd-primary-section="' +
        escapeHtml(sectionKey) + '">' + escapeHtml(primaryLabel) + '</button></div>'
      : '';
    return (
      '<section class="ws-cmd-panel' + (isManaging ? ' is-managing' : '') + '">' +
      '<div class="ws-cmd-panel-head">' +
      '<div class="ws-cmd-panel-title"><h4>' + escapeHtml(title) + '</h4><span class="ws-cmd-panel-count">' + count + '</span></div>' +
      '<button type="button" class="ws-cmd-panel-more" data-cmd-manage-section="' + escapeHtml(sectionKey) +
      '" aria-expanded="' + (isManaging ? 'true' : 'false') +
      '" title="' + (isManaging ? 'Close Command manager' : 'Manage in Command view') +
      '" aria-label="' + (isManaging ? 'Close ' : 'Manage ') +
      escapeHtml(title) + ' in Command view">' + (isManaging ? '×' : '▸') + '</button>' +
      '</div>' +
      '<div class="ws-cmd-panel-body">' + actions + body + '</div>' +
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

  renderRail() {
    const page = this.page || {};
    const notes = Array.isArray(page.notes) ? page.notes : [];
    const schedules = Array.isArray(page.schedules) ? page.schedules : [];
    const sessions = Array.isArray(page.sessions) ? page.sessions : [];
    const dirs = Array.isArray(page.directories) ? page.directories : [];

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
    const folderItems = this.railItems(dirs, (d) => d.title || d.name || d.path || 'Unnamed Directory', {
      sectionKey: 'folders',
      expanded: foldersExpanded,
      action: (d) => 'data-cmd-open-section="folders" data-cmd-item-id="' + escapeHtml(String(d.id || '')) +
        '" data-cmd-item-source="' + escapeHtml(String(d.source || 'reference')) + '"',
      metaOf: (d) => d.path || ''
    });

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
