/**
 * Group Details Page
 *
 * Renders the dedicated details view for a group workspace at /workspaces/{id}.
 * The page is client-rendered: it derives the group ID from the URL, loads the
 * workspace tree, and renders the group header plus (in later tasks) member,
 * task, and notes/files sections. Shows a not-found state when the id is missing,
 * trashed, or not a group.
 *
 * @module group-detail
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
  return String(kind || '').trim().toLowerCase() === 'group' ? 'group' : 'workspace';
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
  const walk = (list) => (list || []).forEach((node) => {
    if (!node || !node.id) return;
    out.push(node);
    walk(node.children);
  });
  walk(nodes);
  return out;
}

export function collectDescendantIds(node) {
  const ids = new Set();
  const walk = (n) => (n && Array.isArray(n.children) ? n.children : []).forEach((child) => {
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
  return flattenTree(tree).filter((node) => !excluded.has(node.id));
}

export function formatMemberCount(count) {
  return `${count} member${count === 1 ? '' : 's'}`;
}

// Color choices for group metadata editing ('' = no color).
export const GROUP_COLOR_PRESETS = ['', '#ef4444', '#f59e0b', '#10b981', '#3b82f6', '#8b5cf6', '#ec4899', '#6b7280'];

// Decide which save calls an edit needs: the name uses the rename endpoint,
// description/color use PATCH. Returns flags so callers only hit changed paths.
export function metadataChanges(group, next) {
  const cur = group || {};
  return {
    nameChanged: String(next.name ?? '') !== String(cur.name ?? ''),
    metaChanged:
      String(next.description ?? '') !== String(cur.description ?? '') ||
      String(next.color ?? '') !== String(cur.color ?? ''),
  };
}

// --- Task roll-up helpers ------------------------------------------------------

export const OPEN_TASK_STATUSES = new Set(['pending', 'assigned', 'in_progress', 'waiting_for_choice']);

export function normalizeStatus(status) {
  return String(status || '').trim().toLowerCase();
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

  if (filters.member && filters.member !== 'all' && (task.__workspaceId || task.workspace_id) !== filters.member) {
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
  return [...(tasks || [])].sort((a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0));
}

// --- Notes/files helpers -------------------------------------------------------

// A workspace canvas attachment counts as a "file" when it carries file metadata.
export function isFileAttachment(attachment) {
  const meta = attachment && attachment.file_meta;
  return !!(meta && (meta.name || meta.url));
}

export function extractFileItems(attachments) {
  return (attachments || []).filter(isFileAttachment).map((attachment) => ({
    id: attachment.id,
    title: attachment.title || attachment.file_meta.name || 'File',
    url: attachment.file_meta.url || '',
  }));
}

// --- Page controller -----------------------------------------------------------

export class GroupDetailPage {
  constructor(groupId) {
    this.groupId = groupId;
    this.group = null;
    this.tree = [];
    this.els = {};
    this.selectedColor = '';
    this.allTasks = [];
    this.taskLoadFailures = 0;
    this.taskFilters = { status: 'default', member: 'all', dateRange: 'any' };
  }

  async init() {
    this.cacheElements();
    this.bindEditForm();
    this.bindMemberControls();
    await this.load();
  }

  cacheElements() {
    this.els = {
      view: document.getElementById('group-detail-view'),
      notFound: document.getElementById('group-not-found'),
      title: document.getElementById('group-title'),
      breadcrumbName: document.getElementById('group-breadcrumb-name'),
      parentCrumb: document.getElementById('group-parent-crumb'),
      parentLink: document.getElementById('group-parent-link'),
      colorSwatch: document.getElementById('group-color-swatch'),
      memberCount: document.getElementById('group-member-count'),
      description: document.getElementById('group-description'),
      membersList: document.getElementById('group-members-list'),
      membersError: document.getElementById('group-members-error'),
      addMemberBtn: document.getElementById('group-add-member-btn'),
      createMemberBtn: document.getElementById('group-create-member-btn'),
      addPicker: document.getElementById('group-add-member-picker'),
      createForm: document.getElementById('group-create-member-form'),
      createName: document.getElementById('group-create-name'),
      createDescription: document.getElementById('group-create-description'),
      createCancel: document.getElementById('group-create-cancel'),
      tasksList: document.getElementById('group-tasks-list'),
      notesFilesList: document.getElementById('group-notes-files-list'),
      editBtn: document.getElementById('group-edit-btn'),
      editForm: document.getElementById('group-edit-form'),
      editName: document.getElementById('group-edit-name'),
      editDescription: document.getElementById('group-edit-description'),
      editColors: document.getElementById('group-edit-colors'),
      editError: document.getElementById('group-edit-error'),
      editSave: document.getElementById('group-edit-save'),
      editCancel: document.getElementById('group-edit-cancel'),
    };
  }

  async load() {
    try {
      const res = await fetch('/api/workspaces?tree=true');
      if (!res.ok) throw new Error(`tree fetch failed: ${res.status}`);
      const data = await res.json();
      this.tree = data.workspaces || data.folders || [];

      const node = findWorkspaceNode(this.tree, this.groupId);
      if (!isGroupNode(node)) {
        this.showNotFound();
        return;
      }

      this.group = node;
      document.title = `${node.name || 'Group'} - Ori Agent`;
      this.renderHeader();
      this.renderMembers();
      this.renderSectionPlaceholders();
      void this.loadTasks();
      void this.loadNotesFiles();
    } catch (err) {
      console.error('Failed to load group:', err);
      this.showNotFound();
    }
  }

  showNotFound() {
    if (this.els.view) this.els.view.hidden = true;
    if (this.els.notFound) this.els.notFound.hidden = false;
  }

  renderHeader() {
    const g = this.group;
    const members = directMembers(g);
    const name = g.name || 'Untitled Group';

    if (this.els.title) this.els.title.textContent = name;
    if (this.els.breadcrumbName) this.els.breadcrumbName.textContent = name;
    if (this.els.memberCount) this.els.memberCount.textContent = formatMemberCount(members.length);
    if (this.els.description) {
      this.els.description.textContent = g.description || 'No description';
    }
    if (this.els.colorSwatch) {
      this.els.colorSwatch.style.setProperty('--accent', g.color || '#6c757d');
    }

    // Parent-group breadcrumb (only when nested).
    if (this.els.parentCrumb && this.els.parentLink) {
      if (g.parent_id) {
        const parent = findWorkspaceNode(this.tree, g.parent_id);
        this.els.parentLink.href = `/workspaces/${encodeURIComponent(g.parent_id)}`;
        this.els.parentLink.textContent = (parent && parent.name) ? parent.name : 'Parent group';
        this.els.parentCrumb.hidden = false;
      } else {
        this.els.parentCrumb.hidden = true;
      }
    }

    if (this.els.editBtn) this.els.editBtn.hidden = false;
  }

  // --- Metadata editing (name via rename endpoint; description/color via PATCH)

  bindEditForm() {
    const { editBtn, editForm, editCancel } = this.els;
    if (editBtn) editBtn.addEventListener('click', () => this.openEditForm());
    if (editCancel) editCancel.addEventListener('click', () => this.closeEditForm());
    if (editForm) {
      editForm.addEventListener('submit', (event) => {
        event.preventDefault();
        void this.saveMetadata();
      });
    }
  }

  openEditForm() {
    if (!this.group || !this.els.editForm) return;
    this.selectedColor = this.group.color || '';
    if (this.els.editName) this.els.editName.value = this.group.name || '';
    if (this.els.editDescription) this.els.editDescription.value = this.group.description || '';
    this.renderColorSwatches();
    this.hideEditError();
    this.els.editForm.hidden = false;
    if (this.els.editName) this.els.editName.focus();
  }

  closeEditForm() {
    if (this.els.editForm) this.els.editForm.hidden = true;
    this.hideEditError();
  }

  renderColorSwatches() {
    const container = this.els.editColors;
    if (!container) return;
    container.innerHTML = '';
    GROUP_COLOR_PRESETS.forEach((color) => {
      const isSelected = color === this.selectedColor;
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
      btn.addEventListener('click', () => {
        this.selectedColor = color;
        this.renderColorSwatches();
      });
      container.appendChild(btn);
    });
  }

  showEditError(message) {
    if (!this.els.editError) return;
    this.els.editError.textContent = message;
    this.els.editError.hidden = false;
  }

  hideEditError() {
    if (this.els.editError) this.els.editError.hidden = true;
  }

  setEditBusy(busy) {
    if (this.els.editSave) {
      this.els.editSave.disabled = busy;
      this.els.editSave.textContent = busy ? 'Saving...' : 'Save';
    }
  }

  async saveMetadata() {
    if (!this.group) return;

    const name = (this.els.editName?.value || '').trim();
    const description = this.els.editDescription?.value || '';
    const color = this.selectedColor || '';

    if (!name) {
      this.showEditError('Name is required.');
      this.els.editName?.focus();
      return;
    }

    const { nameChanged, metaChanged } = metadataChanges(this.group, { name, description, color });
    if (!nameChanged && !metaChanged) {
      this.closeEditForm();
      return;
    }

    this.setEditBusy(true);
    try {
      if (nameChanged) {
        await this.putJson(`/api/workspaces/${encodeURIComponent(this.groupId)}/rename`, 'POST', { name }, 'Failed to rename group');
      }
      if (metaChanged) {
        await this.putJson(`/api/workspaces/${encodeURIComponent(this.groupId)}`, 'PATCH', { description, color }, 'Failed to update group');
      }
      this.closeEditForm();
      await this.load();
    } catch (err) {
      console.error('Failed to save group metadata:', err);
      this.showEditError(err.message || 'Failed to save changes.');
    } finally {
      this.setEditBusy(false);
    }
  }

  async putJson(url, method, body, failMessage) {
    const res = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(text || failMessage);
    }
    return res;
  }

  // --- Members (list, open, add, remove, reorder) ------------------------------

  bindMemberControls() {
    const { addMemberBtn, createMemberBtn, createForm, createCancel } = this.els;
    if (addMemberBtn) addMemberBtn.addEventListener('click', () => this.openAddPicker());
    if (createMemberBtn) createMemberBtn.addEventListener('click', () => this.openCreateMember());
    if (createCancel) createCancel.addEventListener('click', () => this.closeCreateMember());
    if (createForm) {
      createForm.addEventListener('submit', (event) => {
        event.preventDefault();
        void this.createMember();
      });
    }
  }

  renderMembers() {
    const container = this.els.membersList;
    if (!container) return;

    if (this.els.addMemberBtn) this.els.addMemberBtn.hidden = false;
    if (this.els.createMemberBtn) this.els.createMemberBtn.hidden = false;
    this.hideMembersError();

    const members = directMembers(this.group);
    if (members.length === 0) {
      container.innerHTML = '<div class="group-detail-empty">No members yet. Add an existing workspace or create a new one.</div>';
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
      : (member.status ? `Status: ${member.status}` : '');
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
    const container = this.els.membersList;
    if (!container) return;
    container.querySelectorAll('[data-member-id]').forEach((btn) => {
      btn.addEventListener('click', () => {
        window.location.href = `/workspaces/${encodeURIComponent(btn.getAttribute('data-member-id'))}`;
      });
    });
    container.querySelectorAll('[data-member-remove]').forEach((btn) => {
      btn.addEventListener('click', () => void this.removeMember(btn.getAttribute('data-member-remove')));
    });
    container.querySelectorAll('[data-member-up]').forEach((btn) => {
      btn.addEventListener('click', () => void this.moveMember(btn.getAttribute('data-member-up'), 'up'));
    });
    container.querySelectorAll('[data-member-down]').forEach((btn) => {
      btn.addEventListener('click', () => void this.moveMember(btn.getAttribute('data-member-down'), 'down'));
    });
  }

  async removeMember(memberId) {
    if (!memberId) return;
    // Removal moves the member to this group's parent (root if top-level); it
    // never deletes the workspace/group.
    const newParent = this.group.parent_id || '';
    try {
      await this.putJson(`/api/workspaces/${encodeURIComponent(memberId)}`, 'PATCH', { parent_id: newParent }, 'Failed to remove member');
      await this.load();
    } catch (err) {
      console.error('Failed to remove member:', err);
      this.showMembersError(err.message || 'Failed to remove member.');
    }
  }

  async moveMember(memberId, direction) {
    const members = directMembers(this.group).slice();
    const i = members.findIndex((m) => m && m.id === memberId);
    const j = direction === 'up' ? i - 1 : i + 1;
    if (i < 0 || j < 0 || j >= members.length) return;
    [members[i], members[j]] = [members[j], members[i]];

    try {
      // Persist a sequential order_index for the new order.
      for (let k = 0; k < members.length; k += 1) {
        await this.putJson(`/api/workspaces/${encodeURIComponent(members[k].id)}`, 'PATCH', { order_index: k + 1 }, 'Failed to reorder members');
      }
      await this.load();
    } catch (err) {
      console.error('Failed to reorder members:', err);
      this.showMembersError(err.message || 'Failed to reorder members.');
      // Reload so the UI matches the persisted state after a partial failure.
      await this.load();
    }
  }

  openAddPicker() {
    const picker = this.els.addPicker;
    if (!picker) return;
    const targets = eligibleAddTargets(this.tree, this.group);
    if (targets.length === 0) {
      picker.innerHTML = '<div class="group-detail-empty">No eligible workspaces to add.</div>';
      picker.hidden = false;
      return;
    }
    const options = targets
      .map((t) => `<option value="${escapeHtml(t.id)}">${escapeHtml(t.name || t.id)}${isGroupNode(t) ? ' (group)' : ''}</option>`)
      .join('');
    picker.innerHTML = `
      <div class="d-flex gap-2 align-items-center">
        <select id="group-add-select" class="form-select form-select-sm" aria-label="Workspace to add">${options}</select>
        <button id="group-add-confirm" type="button" class="modern-btn modern-btn-primary">Add</button>
        <button id="group-add-cancel" type="button" class="modern-btn modern-btn-secondary">Cancel</button>
      </div>`;
    picker.hidden = false;
    picker.querySelector('#group-add-confirm').addEventListener('click', () => {
      const sel = picker.querySelector('#group-add-select');
      void this.addMember(sel && sel.value);
    });
    picker.querySelector('#group-add-cancel').addEventListener('click', () => {
      picker.hidden = true;
    });
  }

  async addMember(memberId) {
    if (!memberId) return;
    try {
      await this.putJson(`/api/workspaces/${encodeURIComponent(memberId)}`, 'PATCH', { parent_id: this.groupId }, 'Failed to add member');
      if (this.els.addPicker) this.els.addPicker.hidden = true;
      await this.load();
    } catch (err) {
      console.error('Failed to add member:', err);
      this.showMembersError(err.message || 'Failed to add member.');
    }
  }

  // Create a new workspace directly into this group. Uses a self-contained
  // inline form that POSTs to the same /api/workspaces endpoint with parent_id
  // set to this group, so the new workspace lands as a direct member.
  openCreateMember() {
    if (this.els.addPicker) this.els.addPicker.hidden = true;
    if (!this.els.createForm) return;
    if (this.els.createName) this.els.createName.value = '';
    if (this.els.createDescription) this.els.createDescription.value = '';
    this.hideMembersError();
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
      this.showMembersError('Workspace name is required.');
      this.els.createName?.focus();
      return;
    }
    try {
      await this.putJson('/api/workspaces', 'POST', { name, description, parent_id: this.groupId }, 'Failed to create workspace');
      this.closeCreateMember();
      await this.load();
    } catch (err) {
      console.error('Failed to create workspace:', err);
      this.showMembersError(err.message || 'Failed to create workspace.');
    }
  }

  showMembersError(message) {
    if (!this.els.membersError) return;
    this.els.membersError.textContent = message;
    this.els.membersError.hidden = false;
  }

  hideMembersError() {
    if (this.els.membersError) this.els.membersError.hidden = true;
  }

  renderSectionPlaceholders() {
    if (this.els.tasksList) this.els.tasksList.innerHTML = '<div class="group-detail-empty">Loading tasks…</div>';
    if (this.els.notesFilesList) this.els.notesFilesList.innerHTML = '<div class="group-detail-empty">Loading notes &amp; files…</div>';
  }

  // --- Notes & files roll-up (direct concrete members only) --------------------

  async loadNotesFiles() {
    if (!this.els.notesFilesList || !this.group) return;
    const members = directMembers(this.group).filter((m) => !isGroupNode(m));

    const noteResults = await Promise.allSettled(members.map(async (member) => {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(member.id)}/notes`);
      if (!res.ok) throw new Error(`notes fetch failed: ${res.status}`);
      const data = await res.json();
      return (data.notes || []).map((note) => ({ ...note, __wsId: member.id, __wsName: member.name || member.id }));
    }));

    const fileResults = await Promise.allSettled(members.map(async (member) => {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(member.id)}`);
      if (!res.ok) throw new Error(`files fetch failed: ${res.status}`);
      const data = await res.json();
      const attachments = data.attachments || (data.workspace && data.workspace.attachments) || [];
      return extractFileItems(attachments).map((file) => ({ ...file, __wsId: member.id, __wsName: member.name || member.id }));
    }));

    const notes = [];
    const files = [];
    let failures = 0;
    noteResults.forEach((r) => (r.status === 'fulfilled' ? notes.push(...r.value) : (failures += 1)));
    fileResults.forEach((r) => (r.status === 'fulfilled' ? files.push(...r.value) : (failures += 1)));

    this.renderNotesFiles(notes, files, failures);
  }

  renderNotesFiles(notes, files, failures) {
    const container = this.els.notesFilesList;
    if (!container) return;
    const hasSubgroups = directMembers(this.group).some(isGroupNode);

    const notesHtml = notes.length === 0
      ? '<div class="group-detail-empty">No notes.</div>'
      : `<div class="group-task-list" role="list">${notes.map((note) => this.noteRowHtml(note)).join('')}</div>`;
    const filesHtml = files.length === 0
      ? '<div class="group-detail-empty">No files.</div>'
      : `<div class="group-task-list" role="list">${files.map((file) => this.fileRowHtml(file)).join('')}</div>`;

    container.innerHTML = `
      ${failures > 0 ? '<div class="text-warning small mb-2" role="alert">Some members’ notes or files could not be loaded.</div>' : ''}
      ${hasSubgroups ? '<div class="group-detail-hint">Notes and files from member sub-groups are not included.</div>' : ''}
      <h3 class="group-detail-subtitle">Notes</h3>
      ${notesHtml}
      <h3 class="group-detail-subtitle mt-3">Files</h3>
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
    if (!this.els.tasksList || !this.group) return;

    // Direct concrete workspace members only; sub-group tasks are excluded in v1.
    const members = directMembers(this.group).filter((m) => !isGroupNode(m));
    const results = await Promise.allSettled(members.map(async (member) => {
      const res = await fetch(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(member.id)}`);
      if (!res.ok) throw new Error(`tasks fetch failed: ${res.status}`);
      const data = await res.json();
      return (data.tasks || []).map((task) => ({
        ...task,
        __workspaceId: member.id,
        __workspaceName: member.name || member.id,
      }));
    }));

    const all = [];
    let failures = 0;
    results.forEach((r) => {
      if (r.status === 'fulfilled') all.push(...r.value);
      else failures += 1;
    });
    this.allTasks = all;
    this.taskLoadFailures = failures;
    this.renderTasks();
  }

  renderTasks() {
    const container = this.els.tasksList;
    if (!container) return;

    const filtered = sortTasksForRollup(this.allTasks.filter((task) => taskMatchesFilters(task, this.taskFilters)));
    const hasSubgroups = directMembers(this.group).some(isGroupNode);

    container.innerHTML = `
      ${this.taskFiltersHtml()}
      ${this.taskLoadFailures > 0 ? '<div class="text-warning small mb-2" role="alert">Some members’ tasks could not be loaded.</div>' : ''}
      ${hasSubgroups ? '<div class="group-detail-hint">Tasks from member sub-groups are not included.</div>' : ''}
      ${filtered.length === 0
        ? '<div class="group-detail-empty">No tasks match the current filters.</div>'
        : `<div class="group-task-list" role="list">${filtered.map((task) => this.taskRowHtml(task)).join('')}</div>`}
    `;
    this.bindTaskFilters();
  }

  taskFiltersHtml() {
    const members = directMembers(this.group).filter((m) => !isGroupNode(m));
    const statuses = ['pending', 'assigned', 'in_progress', 'waiting_for_choice', 'completed', 'failed', 'cancelled'];
    const sel = (cond) => (cond ? ' selected' : '');
    const statusOptions = [
      `<option value="default"${sel(this.taskFilters.status === 'default')}>Open + scheduled</option>`,
      `<option value="all"${sel(this.taskFilters.status === 'all')}>All statuses</option>`,
      ...statuses.map((s) => `<option value="${s}"${sel(this.taskFilters.status === s)}>${s}</option>`),
    ].join('');
    const memberOptions = [
      `<option value="all"${sel(this.taskFilters.member === 'all')}>All members</option>`,
      ...members.map((m) => `<option value="${escapeHtml(m.id)}"${sel(this.taskFilters.member === m.id)}>${escapeHtml(m.name || m.id)}</option>`),
    ].join('');
    const dates = [['any', 'Any time'], ['today', 'Created today'], ['7d', 'Created last 7 days'], ['30d', 'Created last 30 days']];
    const dateOptions = dates.map(([v, l]) => `<option value="${v}"${sel(this.taskFilters.dateRange === v)}>${l}</option>`).join('');

    return `
      <div class="group-task-filters d-flex gap-2 flex-wrap mb-2">
        <select id="group-task-status" class="form-select form-select-sm" aria-label="Filter tasks by status">${statusOptions}</select>
        <select id="group-task-member" class="form-select form-select-sm" aria-label="Filter tasks by member workspace">${memberOptions}</select>
        <select id="group-task-date" class="form-select form-select-sm" aria-label="Filter tasks by created date">${dateOptions}</select>
      </div>`;
  }

  bindTaskFilters() {
    const container = this.els.tasksList;
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
    bind('group-task-status', 'status');
    bind('group-task-member', 'member');
    bind('group-task-date', 'dateRange');
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
}
