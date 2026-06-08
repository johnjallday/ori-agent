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

// --- Page controller -----------------------------------------------------------

export class GroupDetailPage {
  constructor(groupId) {
    this.groupId = groupId;
    this.group = null;
    this.tree = [];
    this.els = {};
    this.selectedColor = '';
  }

  async init() {
    this.cacheElements();
    this.bindEditForm();
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
      this.renderSectionPlaceholders();
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

  // Placeholder section bodies; replaced with real renderers in later tasks
  // (members: 4.0, tasks: 5.0, notes/files: 6.0).
  renderSectionPlaceholders() {
    const placeholder = '<div class="group-detail-empty">Coming soon.</div>';
    if (this.els.membersList) this.els.membersList.innerHTML = placeholder;
    if (this.els.tasksList) this.els.tasksList.innerHTML = placeholder;
    if (this.els.notesFilesList) this.els.notesFilesList.innerHTML = placeholder;
  }
}
