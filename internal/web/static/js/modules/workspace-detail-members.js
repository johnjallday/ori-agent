/**
 * Workspace Members Panel (groups only)
 *
 * Ported from the retired group-detail page: when the workspace loaded on
 * /workspaces/{id} is a group, this module shows the Members bento panel
 * (member list with open/add/create/remove/reorder) plus lazy roll-ups of the
 * direct members' open tasks, notes, and files. It also decorates the page
 * header with the group badge, color swatch (click to change color), and
 * member count. For concrete workspaces everything stays hidden.
 *
 * Roll-ups are lazy: the per-member requests fire only on the first expansion
 * of the panel, never during initial page load.
 *
 * @module workspace-detail-members
 */

// --- Pure helpers (exported for unit testing) ----------------------------------

export function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function normalizeWorkspaceKind(kind) {
  return String(kind || '')
    .trim()
    .toLowerCase() === 'group'
    ? 'group'
    : 'workspace';
}

export function isGroupNode(node) {
  return !!node && normalizeWorkspaceKind(node.kind) === 'group';
}

// Depth-first search for a node by id across a nested workspace tree.
export function findWorkspaceNode(nodes, id) {
  if (!id) return null;
  const stack = Array.isArray(nodes) ? [...nodes] : [];
  while (stack.length > 0) {
    const node = stack.shift();
    if (!node) continue;
    if (node.id === id) return node;
    if (Array.isArray(node.children) && node.children.length > 0) {
      stack.unshift(...node.children);
    }
  }
  return null;
}

export function directMembers(group) {
  return group && Array.isArray(group.children) ? group.children : [];
}

export function flattenTree(nodes) {
  const out = [];
  const walk = list =>
    (list || []).forEach(node => {
      if (!node || !node.id) return;
      out.push(node);
      walk(node.children);
    });
  walk(nodes);
  return out;
}

export function collectDescendantIds(node) {
  const ids = new Set();
  const walk = n =>
    (n && Array.isArray(n.children) ? n.children : []).forEach(child => {
      if (!child || !child.id) return;
      ids.add(child.id);
      walk(child);
    });
  walk(node);
  return ids;
}

// Workspaces/groups eligible to be added to `group`: everything except the
// group itself, its descendants (which already include current direct members).
// Move-ineligible linked workspaces are rejected server-side and surfaced then.
export function eligibleAddTargets(tree, group) {
  if (!group || !group.id) return [];
  const excluded = collectDescendantIds(group);
  excluded.add(group.id);
  return flattenTree(tree).filter(node => !excluded.has(node.id));
}

export function formatMemberCount(count) {
  return `${count} member${count === 1 ? '' : 's'}`;
}

// Color choices for group color editing ('' = no color).
export const GROUP_COLOR_PRESETS = [
  '',
  '#ef4444',
  '#f59e0b',
  '#10b981',
  '#3b82f6',
  '#8b5cf6',
  '#ec4899',
  '#6b7280'
];

// Decide which save calls a metadata edit needs: the name uses the rename
// endpoint, description/color use PATCH. Returns flags so callers only hit
// changed paths.
export function metadataChanges(group, next) {
  const cur = group || {};
  return {
    nameChanged: String(next.name ?? '') !== String(cur.name ?? ''),
    metaChanged:
      String(next.description ?? '') !== String(cur.description ?? '') ||
      String(next.color ?? '') !== String(cur.color ?? '')
  };
}

// --- Task roll-up helpers ------------------------------------------------------

export const OPEN_TASK_STATUSES = new Set([
  'pending',
  'assigned',
  'in_progress',
  'waiting_for_choice'
]);

export function normalizeStatus(status) {
  return String(status || '')
    .trim()
    .toLowerCase();
}

export function isOpenTask(task) {
  return OPEN_TASK_STATUSES.has(normalizeStatus(task && task.status));
}

export function isScheduledTask(task) {
  return !!(task && task.schedule);
}

// Earliest created_at (ms) a task may have to pass a date-range filter; null = any.
export function taskCreatedCutoff(range, now = Date.now()) {
  if (range === 'today') {
    const d = new Date(now);
    d.setHours(0, 0, 0, 0);
    return d.getTime();
  }
  if (range === '7d') return now - 7 * 24 * 60 * 60 * 1000;
  if (range === '30d') return now - 30 * 24 * 60 * 60 * 1000;
  return null;
}

// filters: { status: 'default'|'all'|<status>, member: 'all'|<id>, dateRange: 'any'|'today'|'7d'|'30d' }.
// The default status view is the open + scheduled roll-up.
export function taskMatchesFilters(task, filters = {}, now = Date.now()) {
  if (!task) return false;
  const status = filters.status || 'default';
  if (status === 'default') {
    if (!isOpenTask(task) && !isScheduledTask(task)) return false;
  } else if (status !== 'all' && normalizeStatus(task.status) !== status) {
    return false;
  }

  if (
    filters.member &&
    filters.member !== 'all' &&
    (task.__workspaceId || task.workspace_id) !== filters.member
  ) {
    return false;
  }

  const cutoff = taskCreatedCutoff(filters.dateRange || 'any', now);
  if (cutoff !== null) {
    const created = Date.parse(task.created_at);
    if (Number.isNaN(created) || created < cutoff) return false;
  }
  return true;
}

// Sort the roll-up newest-first by created_at.
export function sortTasksForRollup(tasks) {
  return [...(tasks || [])].sort(
    (a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0)
  );
}

// --- Notes/files helpers -------------------------------------------------------

// A workspace canvas attachment counts as a "file" when it carries file metadata.
export function isFileAttachment(attachment) {
  const meta = attachment && attachment.file_meta;
  return !!(meta && (meta.name || meta.url));
}

export function extractFileItems(attachments) {
  return (attachments || []).filter(isFileAttachment).map(attachment => ({
    id: attachment.id,
    title: attachment.title || attachment.file_meta.name || 'File',
    url: attachment.file_meta.url || ''
  }));
}

// --- Panel controller -----------------------------------------------------------

export class WorkspaceMembersPanel {
  constructor(workspaceId) {
    this.workspaceId = workspaceId;
    this.group = null; // tree node for this workspace (groups only)
    this.tree = [];
    this.els = {};
    this.active = false;
    this.rollupsLoaded = false;
    this.rollupObserver = null;
    this.allTasks = [];
    this.taskLoadFailures = 0;
    this.taskFilters = { status: 'default', member: 'all', dateRange: 'any' };
    this.colorPopoverOpen = false;
  }

  cacheElements() {
    this.els = {
      bento: document.querySelector('.workspace-detail-bento'),
      panel: document.getElementById('workspace-detail-members-panel'),
      list: document.getElementById('workspace-detail-members-list'),
      error: document.getElementById('workspace-detail-members-error'),
      addBtn: document.getElementById('workspace-detail-add-member-btn'),
      createBtn: document.getElementById('workspace-detail-create-member-btn'),
      picker: document.getElementById('workspace-detail-member-picker'),
      createForm: document.getElementById('workspace-detail-member-create-form'),
      createName: document.getElementById('workspace-detail-member-create-name'),
      createDescription: document.getElementById('workspace-detail-member-create-description'),
      createCancel: document.getElementById('workspace-detail-member-create-cancel'),
      rollups: document.getElementById('workspace-detail-members-rollups'),
      // Header identity
      badge: document.getElementById('workspace-group-badge'),
      swatch: document.getElementById('workspace-group-color'),
      memberStat: document.getElementById('workspace-member-stat'),
      memberCount: document.getElementById('workspace-member-count')
    };
  }

  /**
   * Called by the page after the workspace loads. Activates the panel and
   * group header identity when the workspace is a group; hides them otherwise.
   */
  async syncWorkspace(workspace) {
    this.cacheElements();
    if (!isGroupNode(workspace)) {
      this.deactivate();
      return false;
    }

    this.bindControlsOnce();
    await this.reload();
    if (!this.group) {
      // Tree did not confirm the group (e.g. trashed); keep everything hidden.
      this.deactivate();
      return false;
    }

    this.activate();
    return true;
  }

  activate() {
    this.active = true;
    if (this.els.panel) this.els.panel.hidden = false;
    if (this.els.bento) this.els.bento.classList.add('has-members-panel');
    if (this.els.badge) this.els.badge.hidden = false;
    if (this.els.memberStat) this.els.memberStat.hidden = false;
    this.applyHeaderIdentity();
    this.armLazyRollups();
  }

  deactivate() {
    this.active = false;
    if (this.els.panel) this.els.panel.hidden = true;
    if (this.els.bento) this.els.bento.classList.remove('has-members-panel');
    if (this.els.badge) this.els.badge.hidden = true;
    if (this.els.memberStat) this.els.memberStat.hidden = true;
    if (this.els.swatch) this.els.swatch.hidden = true;
  }

  // Reload the workspace tree and re-render the member list (+ rollups when
  // they were already loaded).
  async reload() {
    try {
      const res = await fetch('/api/workspaces?tree=true');
      if (!res.ok) throw new Error(`tree fetch failed: ${res.status}`);
      const data = await res.json();
      this.tree = data.workspaces || data.folders || [];
      this.group = findWorkspaceNode(this.tree, this.workspaceId);
      if (!isGroupNode(this.group)) {
        this.group = null;
        return;
      }
      this.renderMembers();
      this.applyHeaderIdentity();
      if (this.rollupsLoaded) {
        void this.loadRollups();
      }
    } catch (err) {
      console.error('Failed to load group members:', err);
      this.showError('Failed to load members.');
    }
  }

  applyHeaderIdentity() {
    if (!this.group) return;
    const members = directMembers(this.group);
    if (this.els.memberCount) this.els.memberCount.textContent = String(members.length);
    if (this.els.swatch) {
      this.els.swatch.hidden = false;
      this.els.swatch.style.setProperty('--accent', this.group.color || '#6c757d');
      this.els.swatch.title = 'Change group color';
    }
  }

  bindControlsOnce() {
    if (this.controlsBound) return;
    this.controlsBound = true;

    const { addBtn, createBtn, createForm, createCancel, swatch } = this.els;
    if (addBtn) addBtn.addEventListener('click', () => this.openAddPicker());
    if (createBtn) createBtn.addEventListener('click', () => this.openCreateMember());
    if (createCancel) createCancel.addEventListener('click', () => this.closeCreateMember());
    if (createForm) {
      createForm.addEventListener('submit', event => {
        event.preventDefault();
        void this.createMember();
      });
    }
    if (swatch) swatch.addEventListener('click', () => this.toggleColorPopover());
  }

  // --- Lazy roll-ups ------------------------------------------------------------

  // Fire the per-member roll-up requests only when the panel is first
  // expanded (initPanelExpansion flips aria-expanded to "true").
  armLazyRollups() {
    if (this.rollupObserver || !this.els.panel) return;
    if (this.els.rollups) {
      this.els.rollups.innerHTML =
        '<div class="group-detail-empty">Expand the panel to load member tasks, notes, and files.</div>';
    }
    this.rollupObserver = new MutationObserver(() => {
      if (this.els.panel.getAttribute('aria-expanded') === 'true' && !this.rollupsLoaded) {
        this.rollupsLoaded = true;
        void this.loadRollups();
      }
    });
    this.rollupObserver.observe(this.els.panel, {
      attributes: true,
      attributeFilter: ['aria-expanded']
    });
  }

  async loadRollups() {
    if (!this.els.rollups || !this.group) return;
    this.els.rollups.innerHTML =
      '<div class="group-detail-empty">Loading member tasks, notes &amp; files…</div>';
    await Promise.all([this.loadTasks(), this.loadNotesFiles()]);
    this.renderRollups();
  }

  renderRollups() {
    const container = this.els.rollups;
    if (!container) return;
    const hasSubgroups = directMembers(this.group).some(isGroupNode);

    container.innerHTML = `
      ${hasSubgroups ? '<div class="group-detail-hint">Items from member sub-groups are not included.</div>' : ''}
      <h3 class="group-detail-subtitle mt-2">Member tasks</h3>
      <div id="workspace-detail-members-tasks"></div>
      <h3 class="group-detail-subtitle mt-3">Member notes &amp; files</h3>
      <div id="workspace-detail-members-notesfiles"></div>
    `;
    this.renderTasks();
    this.renderNotesFiles();
  }

  // --- Members (list, open, add, remove, reorder) ------------------------------

  renderMembers() {
    const container = this.els.list;
    if (!container) return;
    this.hideError();

    const members = directMembers(this.group);
    if (members.length === 0) {
      container.innerHTML =
        '<div class="group-detail-empty">No members yet. Add an existing workspace or create a new one.</div>';
      return;
    }

    container.innerHTML = `<div class="group-member-list" role="list">${members.map((member, index) => this.memberRowHtml(member, index, members.length)).join('')}</div>`;
    this.bindMemberActions();
  }

  memberRowHtml(member, index, total) {
    const isGroup = isGroupNode(member);
    const name = member.name || (isGroup ? 'Untitled Group' : 'Untitled Workspace');
    const kindLabel = isGroup ? 'Group' : 'Workspace';
    const meta = isGroup
      ? formatMemberCount(directMembers(member).length)
      : member.status
        ? `Status: ${member.status}`
        : '';
    const desc = member.description || '';
    const safeId = escapeHtml(member.id);
    const safeName = escapeHtml(name);

    return `
      <div class="group-member-row" role="listitem">
        <button type="button" class="group-member-open" data-member-id="${safeId}" aria-label="Open ${safeName}">
          <span class="group-member-kind">${kindLabel}</span>
          <span class="group-member-name">${safeName}</span>
          ${desc ? `<span class="group-member-desc">${escapeHtml(desc)}</span>` : ''}
          ${meta ? `<span class="group-member-meta">${escapeHtml(meta)}</span>` : ''}
        </button>
        <div class="group-member-actions">
          <button type="button" class="group-member-move" data-member-up="${safeId}" aria-label="Move ${safeName} up"${index === 0 ? ' disabled' : ''}>&uarr;</button>
          <button type="button" class="group-member-move" data-member-down="${safeId}" aria-label="Move ${safeName} down"${index === total - 1 ? ' disabled' : ''}>&darr;</button>
          <button type="button" class="group-member-remove" data-member-remove="${safeId}" aria-label="Remove ${safeName} from group">Remove</button>
        </div>
      </div>`;
  }

  bindMemberActions() {
    const container = this.els.list;
    if (!container) return;
    container.querySelectorAll('[data-member-id]').forEach(btn => {
      btn.addEventListener('click', () => {
        window.location.href = `/workspaces/${encodeURIComponent(btn.getAttribute('data-member-id'))}`;
      });
    });
    container.querySelectorAll('[data-member-remove]').forEach(btn => {
      btn.addEventListener(
        'click',
        () => void this.removeMember(btn.getAttribute('data-member-remove'))
      );
    });
    container.querySelectorAll('[data-member-up]').forEach(btn => {
      btn.addEventListener(
        'click',
        () => void this.moveMember(btn.getAttribute('data-member-up'), 'up')
      );
    });
    container.querySelectorAll('[data-member-down]').forEach(btn => {
      btn.addEventListener(
        'click',
        () => void this.moveMember(btn.getAttribute('data-member-down'), 'down')
      );
    });
  }

  async removeMember(memberId) {
    if (!memberId) return;
    // Removal moves the member to this group's parent (root if top-level); it
    // never deletes the workspace/group.
    const newParent = (this.group && this.group.parent_id) || '';
    try {
      await this.sendJson(
        `/api/workspaces/${encodeURIComponent(memberId)}`,
        'PATCH',
        { parent_id: newParent },
        'Failed to remove member'
      );
      await this.reload();
    } catch (err) {
      console.error('Failed to remove member:', err);
      this.showError(err.message || 'Failed to remove member.');
    }
  }

  async moveMember(memberId, direction) {
    const members = directMembers(this.group).slice();
    const i = members.findIndex(m => m && m.id === memberId);
    const j = direction === 'up' ? i - 1 : i + 1;
    if (i < 0 || j < 0 || j >= members.length) return;
    [members[i], members[j]] = [members[j], members[i]];

    try {
      // Persist a sequential order_index for the new order.
      for (let k = 0; k < members.length; k += 1) {
        await this.sendJson(
          `/api/workspaces/${encodeURIComponent(members[k].id)}`,
          'PATCH',
          { order_index: k + 1 },
          'Failed to reorder members'
        );
      }
      await this.reload();
    } catch (err) {
      console.error('Failed to reorder members:', err);
      this.showError(err.message || 'Failed to reorder members.');
      // Reload so the UI matches the persisted state after a partial failure.
      await this.reload();
    }
  }

  openAddPicker() {
    const picker = this.els.picker;
    if (!picker) return;
    this.closeCreateMember();
    const targets = eligibleAddTargets(this.tree, this.group);
    if (targets.length === 0) {
      picker.innerHTML = '<div class="group-detail-empty">No eligible workspaces to add.</div>';
      picker.hidden = false;
      return;
    }
    const options = targets
      .map(
        t =>
          `<option value="${escapeHtml(t.id)}">${escapeHtml(t.name || t.id)}${isGroupNode(t) ? ' (group)' : ''}</option>`
      )
      .join('');
    picker.innerHTML = `
      <div class="d-flex gap-2 align-items-center">
        <select id="workspace-detail-member-add-select" class="form-select form-select-sm" aria-label="Workspace to add">${options}</select>
        <button id="workspace-detail-member-add-confirm" type="button" class="modern-btn modern-btn-primary">Add</button>
        <button id="workspace-detail-member-add-cancel" type="button" class="modern-btn modern-btn-secondary">Cancel</button>
      </div>`;
    picker.hidden = false;
    picker.querySelector('#workspace-detail-member-add-confirm').addEventListener('click', () => {
      const sel = picker.querySelector('#workspace-detail-member-add-select');
      void this.addMember(sel && sel.value);
    });
    picker.querySelector('#workspace-detail-member-add-cancel').addEventListener('click', () => {
      picker.hidden = true;
    });
  }

  async addMember(memberId) {
    if (!memberId) return;
    try {
      await this.sendJson(
        `/api/workspaces/${encodeURIComponent(memberId)}`,
        'PATCH',
        { parent_id: this.workspaceId },
        'Failed to add member'
      );
      if (this.els.picker) this.els.picker.hidden = true;
      await this.reload();
    } catch (err) {
      console.error('Failed to add member:', err);
      this.showError(err.message || 'Failed to add member.');
    }
  }

  // Create a new workspace directly into this group (parent_id = this group).
  openCreateMember() {
    if (this.els.picker) this.els.picker.hidden = true;
    if (!this.els.createForm) return;
    if (this.els.createName) this.els.createName.value = '';
    if (this.els.createDescription) this.els.createDescription.value = '';
    this.hideError();
    this.els.createForm.hidden = false;
    if (this.els.createName) this.els.createName.focus();
  }

  closeCreateMember() {
    if (this.els.createForm) this.els.createForm.hidden = true;
  }

  async createMember() {
    const name = (this.els.createName?.value || '').trim();
    const description = this.els.createDescription?.value || '';
    if (!name) {
      this.showError('Workspace name is required.');
      this.els.createName?.focus();
      return;
    }
    try {
      await this.sendJson(
        '/api/workspaces',
        'POST',
        { name, description, parent_id: this.workspaceId },
        'Failed to create workspace'
      );
      this.closeCreateMember();
      await this.reload();
    } catch (err) {
      console.error('Failed to create workspace:', err);
      this.showError(err.message || 'Failed to create workspace.');
    }
  }

  // --- Group color (header swatch popover) --------------------------------------

  toggleColorPopover() {
    if (!this.active || !this.els.swatch) return;
    const existing = document.getElementById('workspace-group-color-popover');
    if (existing) {
      existing.remove();
      this.colorPopoverOpen = false;
      return;
    }
    const popover = document.createElement('div');
    popover.id = 'workspace-group-color-popover';
    popover.className = 'workspace-group-color-popover';
    popover.setAttribute('role', 'group');
    popover.setAttribute('aria-label', 'Group color');
    GROUP_COLOR_PRESETS.forEach(color => {
      const isSelected = color === (this.group?.color || '');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = `group-color-btn${isSelected ? ' is-selected' : ''}${color ? '' : ' is-none'}`;
      btn.setAttribute('aria-label', color ? `Color ${color}` : 'No color');
      btn.setAttribute('aria-pressed', isSelected ? 'true' : 'false');
      if (color) {
        btn.style.background = color;
      } else {
        btn.textContent = 'None';
      }
      btn.addEventListener('click', () => void this.saveColor(color));
      popover.appendChild(btn);
    });
    this.els.swatch.insertAdjacentElement('afterend', popover);
    this.colorPopoverOpen = true;
  }

  async saveColor(color) {
    try {
      await this.sendJson(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}`,
        'PATCH',
        { color },
        'Failed to update group color'
      );
      const popover = document.getElementById('workspace-group-color-popover');
      if (popover) popover.remove();
      this.colorPopoverOpen = false;
      await this.reload();
    } catch (err) {
      console.error('Failed to update group color:', err);
      this.showError(err.message || 'Failed to update group color.');
    }
  }

  // --- Notes & files roll-up (direct concrete members only) --------------------

  async loadNotesFiles() {
    if (!this.group) return;
    const members = directMembers(this.group).filter(m => !isGroupNode(m));

    const noteResults = await Promise.allSettled(
      members.map(async member => {
        const res = await fetch(`/api/workspaces/${encodeURIComponent(member.id)}/notes`);
        if (!res.ok) throw new Error(`notes fetch failed: ${res.status}`);
        const data = await res.json();
        return (data.notes || []).map(note => ({
          ...note,
          __wsId: member.id,
          __wsName: member.name || member.id
        }));
      })
    );

    const fileResults = await Promise.allSettled(
      members.map(async member => {
        const res = await fetch(`/api/workspaces/${encodeURIComponent(member.id)}`);
        if (!res.ok) throw new Error(`files fetch failed: ${res.status}`);
        const data = await res.json();
        const attachments =
          data.attachments || (data.workspace && data.workspace.attachments) || [];
        return extractFileItems(attachments).map(file => ({
          ...file,
          __wsId: member.id,
          __wsName: member.name || member.id
        }));
      })
    );

    const notes = [];
    const files = [];
    let failures = 0;
    noteResults.forEach(r => (r.status === 'fulfilled' ? notes.push(...r.value) : (failures += 1)));
    fileResults.forEach(r => (r.status === 'fulfilled' ? files.push(...r.value) : (failures += 1)));

    this.memberNotes = notes;
    this.memberFiles = files;
    this.notesFilesFailures = failures;
  }

  renderNotesFiles() {
    const container = document.getElementById('workspace-detail-members-notesfiles');
    if (!container) return;
    const notes = this.memberNotes || [];
    const files = this.memberFiles || [];

    const notesHtml =
      notes.length === 0
        ? '<div class="group-detail-empty">No notes.</div>'
        : `<div class="group-task-list" role="list">${notes.map(note => this.noteRowHtml(note)).join('')}</div>`;
    const filesHtml =
      files.length === 0
        ? '<div class="group-detail-empty">No files.</div>'
        : `<div class="group-task-list" role="list">${files.map(file => this.fileRowHtml(file)).join('')}</div>`;

    container.innerHTML = `
      ${this.notesFilesFailures > 0 ? '<div class="text-warning small mb-2" role="alert">Some members’ notes or files could not be loaded.</div>' : ''}
      <h4 class="group-detail-subtitle">Notes</h4>
      ${notesHtml}
      <h4 class="group-detail-subtitle mt-3">Files</h4>
      ${filesHtml}
    `;
  }

  noteRowHtml(note) {
    const wsId = note.__wsId || note.workspace_id;
    const link = `/workspaces/${encodeURIComponent(wsId)}/notes/${encodeURIComponent(note.id)}`;
    return `
      <a class="group-task-row" role="listitem" href="${escapeHtml(link)}">
        <span class="group-task-desc">${escapeHtml(note.name || 'Untitled note')}</span>
        <span class="group-task-meta">${escapeHtml(note.__wsName || '')}</span>
      </a>`;
  }

  fileRowHtml(file) {
    const href = file.url || `/workspaces/${encodeURIComponent(file.__wsId)}`;
    return `
      <a class="group-task-row" role="listitem" href="${escapeHtml(href)}">
        <span class="group-task-desc">${escapeHtml(file.title || 'File')}</span>
        <span class="group-task-meta">${escapeHtml(file.__wsName || '')}</span>
      </a>`;
  }

  // --- Tasks roll-up (default open + scheduled; status/created-date/member filters)

  async loadTasks() {
    if (!this.group) return;

    // Direct concrete workspace members only; sub-group tasks are excluded.
    const members = directMembers(this.group).filter(m => !isGroupNode(m));
    const results = await Promise.allSettled(
      members.map(async member => {
        const res = await fetch(
          `/api/orchestration/tasks?workspace_id=${encodeURIComponent(member.id)}`
        );
        if (!res.ok) throw new Error(`tasks fetch failed: ${res.status}`);
        const data = await res.json();
        return (data.tasks || []).map(task => ({
          ...task,
          __workspaceId: member.id,
          __workspaceName: member.name || member.id
        }));
      })
    );

    const all = [];
    let failures = 0;
    results.forEach(r => {
      if (r.status === 'fulfilled') all.push(...r.value);
      else failures += 1;
    });
    this.allTasks = all;
    this.taskLoadFailures = failures;
  }

  renderTasks() {
    const container = document.getElementById('workspace-detail-members-tasks');
    if (!container) return;

    const filtered = sortTasksForRollup(
      this.allTasks.filter(task => taskMatchesFilters(task, this.taskFilters))
    );

    container.innerHTML = `
      ${this.taskFiltersHtml()}
      ${this.taskLoadFailures > 0 ? '<div class="text-warning small mb-2" role="alert">Some members’ tasks could not be loaded.</div>' : ''}
      ${
        filtered.length === 0
          ? '<div class="group-detail-empty">No tasks match the current filters.</div>'
          : `<div class="group-task-list" role="list">${filtered.map(task => this.taskRowHtml(task)).join('')}</div>`
      }
    `;
    this.bindTaskFilters();
  }

  taskFiltersHtml() {
    const members = directMembers(this.group).filter(m => !isGroupNode(m));
    const statuses = [
      'pending',
      'assigned',
      'in_progress',
      'waiting_for_choice',
      'completed',
      'failed',
      'cancelled'
    ];
    const sel = cond => (cond ? ' selected' : '');
    const statusOptions = [
      `<option value="default"${sel(this.taskFilters.status === 'default')}>Open + scheduled</option>`,
      `<option value="all"${sel(this.taskFilters.status === 'all')}>All statuses</option>`,
      ...statuses.map(
        s => `<option value="${s}"${sel(this.taskFilters.status === s)}>${s}</option>`
      )
    ].join('');
    const memberOptions = [
      `<option value="all"${sel(this.taskFilters.member === 'all')}>All members</option>`,
      ...members.map(
        m =>
          `<option value="${escapeHtml(m.id)}"${sel(this.taskFilters.member === m.id)}>${escapeHtml(m.name || m.id)}</option>`
      )
    ].join('');
    const dates = [
      ['any', 'Any time'],
      ['today', 'Created today'],
      ['7d', 'Created last 7 days'],
      ['30d', 'Created last 30 days']
    ];
    const dateOptions = dates
      .map(([v, l]) => `<option value="${v}"${sel(this.taskFilters.dateRange === v)}>${l}</option>`)
      .join('');

    return `
      <div class="group-task-filters d-flex gap-2 flex-wrap mb-2">
        <select id="workspace-detail-members-task-status" class="form-select form-select-sm" aria-label="Filter tasks by status">${statusOptions}</select>
        <select id="workspace-detail-members-task-member" class="form-select form-select-sm" aria-label="Filter tasks by member workspace">${memberOptions}</select>
        <select id="workspace-detail-members-task-date" class="form-select form-select-sm" aria-label="Filter tasks by created date">${dateOptions}</select>
      </div>`;
  }

  bindTaskFilters() {
    const container = document.getElementById('workspace-detail-members-tasks');
    if (!container) return;
    const bind = (id, key) => {
      const el = container.querySelector(`#${id}`);
      if (el) {
        el.addEventListener('change', () => {
          this.taskFilters[key] = el.value;
          this.renderTasks();
        });
      }
    };
    bind('workspace-detail-members-task-status', 'status');
    bind('workspace-detail-members-task-member', 'member');
    bind('workspace-detail-members-task-date', 'dateRange');
  }

  taskRowHtml(task) {
    const desc = task.description || 'Untitled task';
    const status = normalizeStatus(task.status);
    const wsName = task.__workspaceName || '';
    const scheduled = isScheduledTask(task) ? ' · scheduled' : '';
    const wsId = task.__workspaceId || task.workspace_id;
    const link = `/workspaces/${encodeURIComponent(wsId)}/task/${encodeURIComponent(task.id)}`;
    return `
      <a class="group-task-row" role="listitem" href="${escapeHtml(link)}">
        <span class="group-task-desc">${escapeHtml(desc)}</span>
        <span class="group-task-meta">${escapeHtml(wsName)} · ${escapeHtml(status)}${scheduled}</span>
      </a>`;
  }

  // --- Misc -----------------------------------------------------------------

  async sendJson(url, method, body, failMessage) {
    const res = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(text || failMessage);
    }
    return res;
  }

  showError(message) {
    if (!this.els.error) return;
    this.els.error.textContent = message;
    this.els.error.hidden = false;
  }

  hideError() {
    if (this.els.error) this.els.error.hidden = true;
  }
}
