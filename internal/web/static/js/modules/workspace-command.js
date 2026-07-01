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

function statBox(value, label) {
  return '<div class="ws-cmd-stat"><div class="ws-v">' + escapeHtml(value) +
    '</div><div class="ws-l">' + escapeHtml(label) + '</div></div>';
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

  deactivate() {
    this.active = false;
    if (this.container) this.container.hidden = true;
    if (this.detailedView) this.detailedView.hidden = false;
    this.updateToggle();
    this.persist('detailed');
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
      (mode ? '<div class="ws-sub">Ops mode · ' + escapeHtml(mode) + '</div>' : '') +
      '</div>' +
      '<div class="ws-cmd-readout">' +
      statBox(stats.agents, 'Agents') +
      statBox(stats.openTasks, 'Open Tasks') +
      statBox(stats.mcp, 'Tools · MCP') +
      statBox(stats.skills, 'Skills') +
      '</div>' +
      '</header>' +
      '<div class="ws-cmd-layout">' +
      '<section class="ws-cmd-garrison">' + this.renderGarrison() + '</section>' +
      '<aside class="ws-cmd-rail"><div class="ws-cmd-soon">Intel · orders · comms arrive soon</div></aside>' +
      '</div>';

    const back = this.container.querySelector('[data-ws-cmd-detailed]');
    if (back) back.addEventListener('click', () => this.deactivate());
    this.bindGarrison();
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
          ? '<span class="ws-cmd-badge is-keeper">★ Keeper</span>'
          : '<span class="ws-cmd-badge">Field Unit</span>');

    const ctl = group.isUnassigned
      ? ''
      : (keeper
          ? '<span class="ws-cmd-lock" title="Entry agent — locked, can\'t be removed">🔒</span>'
          : '') +
        '<button type="button" class="ws-cmd-icon-btn" data-cmd-add-task="' + escapeHtml(encoded) +
        '" title="Add a task for ' + escapeHtml(name) + '" aria-label="Add a task for ' + escapeHtml(name) + '">＋</button>';

    const rows = group.isUnassigned ? '' :
      '<div class="ws-cmd-unit-rows">' +
      '<div class="ws-cmd-row"><span class="ws-cmd-rk">Class</span><span class="ws-cmd-rv">' +
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
    const head = '<div class="ws-cmd-ql-head"><span class="ws-cmd-ql-t">Quest Log · ' + tasks.length + '</span>' + add + '</div>';
    if (!tasks.length) {
      return '<div class="ws-cmd-questlog">' + head + '<div class="ws-cmd-ql-empty">— no orders issued —</div></div>';
    }
    const items = tasks.map((t) => {
      const label = String(t.description || t.name || t.title || 'Task');
      const tone = this.taskTone(t.status);
      const statusText = String(t.status || 'pending').replace('_', ' ');
      return (
        '<div class="ws-cmd-quest">' +
        '<span class="ws-cmd-q-glyph">✦</span>' +
        '<span class="ws-cmd-q-name">' + escapeHtml(label) + '</span>' +
        '<span class="ws-cmd-q-status ' + tone + '">' + escapeHtml(statusText) + '</span>' +
        '<button type="button" class="ws-cmd-q-run" data-cmd-run-task="' + escapeHtml(String(t.id || '')) +
        '" title="Deploy" aria-label="Run task ' + escapeHtml(label) + '">▶</button>' +
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
      return '<div class="ws-cmd-soon">No agents garrisoned yet.</div>';
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
}
