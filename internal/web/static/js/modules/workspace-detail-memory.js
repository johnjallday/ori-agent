/**
 * Workspace Memory tab manager.
 *
 * Owns the Memory tab on the workspace detail page: loads MEMORY.md entries
 * from the memory API, renders them with provenance, and handles add / inline
 * edit / delete. MEMORY.md on disk is canonical; this is a thin CRUD surface
 * over /api/workspaces/{id}/memory.
 *
 * Instantiated by WorkspaceDetailPage, which provides workspaceId and
 * escapeHtml through the host. Lazy-loads the first time its tab is shown.
 *
 * @module workspace-detail-memory
 */

const MEMORY_TYPES = ['fact', 'feedback', 'decision', 'dead-end', 'watch', 'thread'];

export class WorkspaceMemoryManager {
  constructor(host) {
    this.host = host;
    this.loaded = false;
    this.entries = [];
    this.unstructured = [];
    this.editingIndex = -1;
    this.elements = {};
  }

  get workspaceId() {
    return this.host?.workspaceId || '';
  }

  esc(text) {
    return this.host?.escapeHtml ? this.host.escapeHtml(text) : String(text ?? '');
  }

  bindEvents() {
    this.elements = {
      tab: document.getElementById('workspace-detail-config-memory-tab'),
      meta: document.getElementById('workspace-detail-memory-meta'),
      list: document.getElementById('workspace-detail-memory-list'),
      addForm: document.getElementById('workspace-detail-memory-add-form'),
      addType: document.getElementById('workspace-detail-memory-add-type'),
      addText: document.getElementById('workspace-detail-memory-add-text')
    };

    // Lazy-load the first time the tab is shown (Bootstrap fires shown.bs.tab).
    this.elements.tab?.addEventListener('shown.bs.tab', () => {
      if (!this.loaded) this.load();
    });

    this.elements.addForm?.addEventListener('submit', event => {
      event.preventDefault();
      this.addEntry();
    });

    // Event delegation for edit/delete/save/cancel on the list.
    this.elements.list?.addEventListener('click', event => this.handleListClick(event));
  }

  async load() {
    if (!this.workspaceId || !this.elements.list) return;
    try {
      const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory`);
      if (!response.ok) throw new Error('Failed to load workspace memory');
      const payload = await response.json();
      this.entries = Array.isArray(payload?.entries) ? payload.entries : [];
      this.unstructured = Array.isArray(payload?.unstructured) ? payload.unstructured : [];
      this.meta = {
        rawSize: Number(payload?.raw_size) || 0,
        charBudget: Number(payload?.char_budget) || 0,
        overBudget: Boolean(payload?.over_budget)
      };
      this.loaded = true;
      this.editingIndex = -1;
      this.render();
    } catch (error) {
      if (window.Toast) window.Toast.error(error.message || 'Failed to load workspace memory');
    }
  }

  render() {
    this.renderMeta();
    this.renderList();
  }

  renderMeta() {
    if (!this.elements.meta) return;
    const { rawSize = 0, charBudget = 0, overBudget = false } = this.meta || {};
    if (!charBudget) {
      this.elements.meta.innerHTML = '';
      return;
    }
    const cls = overBudget ? 'is-over-budget' : '';
    const warn = overBudget
      ? ' — over the injection budget; oldest non-watch/thread entries are dropped from prompts. Consider pruning.'
      : '';
    this.elements.meta.innerHTML =
      `<span class="workspace-detail-memory-size ${cls}">${rawSize} / ${charBudget} chars${this.esc(warn)}</span>`;
  }

  renderList() {
    const list = this.elements.list;
    if (!list) return;

    if (!this.entries.length && !this.unstructured.length) {
      list.innerHTML = `
        <div class="workspace-detail-memory-empty">
          <p>No memory yet. As missions and chats run in this workspace, agents record durable facts here — or add one yourself.</p>
          <p class="workspace-detail-memory-empty-example">Example: <code>[watch] build baseline is ~7 min; flag if &gt;10</code></p>
        </div>`;
      return;
    }

    const entriesHtml = this.entries
      .map((entry, index) => (index === this.editingIndex ? this.editRowHtml(entry, index) : this.entryRowHtml(entry, index)))
      .join('');

    let unstructuredHtml = '';
    if (this.unstructured.length) {
      const items = this.unstructured
        .map(line => `<li class="workspace-detail-memory-unstructured-line">${this.esc(line)}</li>`)
        .join('');
      unstructuredHtml = `
        <div class="workspace-detail-memory-unstructured">
          <div class="workspace-detail-memory-unstructured-label">Unstructured lines (not injected as entries)</div>
          <ul class="workspace-detail-memory-unstructured-list">${items}</ul>
        </div>`;
    }

    list.innerHTML = entriesHtml + unstructuredHtml;
  }

  entryRowHtml(entry, index) {
    const type = this.esc(entry?.type || 'fact');
    const meta = [entry?.provenance, entry?.date].filter(Boolean).map(v => this.esc(v)).join(' · ');
    return `
      <article class="workspace-detail-memory-entry" data-index="${index}">
        <div class="workspace-detail-memory-entry-main">
          <span class="workspace-detail-memory-badge workspace-detail-memory-badge-${type}">${type}</span>
          <span class="workspace-detail-memory-text">${this.esc(entry?.text || '')}</span>
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">${meta}</span>
          <span class="workspace-detail-memory-actions">
            <button type="button" class="workspace-detail-memory-action" data-action="edit" data-index="${index}">Edit</button>
            <button type="button" class="workspace-detail-memory-action is-danger" data-action="delete" data-index="${index}">Delete</button>
          </span>
        </div>
      </article>`;
  }

  editRowHtml(entry, index) {
    const options = MEMORY_TYPES
      .map(t => `<option value="${t}"${t === (entry?.type || 'fact') ? ' selected' : ''}>${t}</option>`)
      .join('');
    return `
      <article class="workspace-detail-memory-entry is-editing" data-index="${index}">
        <div class="workspace-detail-memory-edit">
          <select class="form-select workspace-detail-memory-type" data-edit-type="${index}" aria-label="Entry type">${options}</select>
          <input type="text" class="form-control workspace-detail-memory-input" data-edit-text="${index}" maxlength="500" value="${this.esc(entry?.text || '')}" aria-label="Entry text">
        </div>
        <div class="workspace-detail-memory-entry-foot">
          <span class="workspace-detail-memory-prov">${this.esc(entry?.provenance || '')}</span>
          <span class="workspace-detail-memory-actions">
            <button type="button" class="workspace-detail-memory-action" data-action="save" data-index="${index}">Save</button>
            <button type="button" class="workspace-detail-memory-action" data-action="cancel" data-index="${index}">Cancel</button>
          </span>
        </div>
      </article>`;
  }

  handleListClick(event) {
    const button = event.target.closest('[data-action]');
    if (!button) return;
    const index = Number(button.dataset.index);
    if (!Number.isInteger(index)) return;
    switch (button.dataset.action) {
      case 'edit':
        this.editingIndex = index;
        this.renderList();
        break;
      case 'cancel':
        this.editingIndex = -1;
        this.renderList();
        break;
      case 'save':
        this.saveEdit(index);
        break;
      case 'delete':
        this.deleteEntry(index);
        break;
      default:
        break;
    }
  }

  async addEntry() {
    const text = (this.elements.addText?.value || '').trim();
    const type = this.elements.addType?.value || 'fact';
    if (!text) return;
    await this.mutate('POST', `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries`, { text, type }, () => {
      if (this.elements.addText) this.elements.addText.value = '';
      if (window.Toast) window.Toast.success('Saved to workspace memory');
    });
  }

  async saveEdit(index) {
    const typeEl = this.elements.list.querySelector(`[data-edit-type="${index}"]`);
    const textEl = this.elements.list.querySelector(`[data-edit-text="${index}"]`);
    const text = (textEl?.value || '').trim();
    const type = typeEl?.value || 'fact';
    if (!text) return;
    await this.mutate('PUT', `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries/${index}`, { text, type }, () => {
      this.editingIndex = -1;
      if (window.Toast) window.Toast.success('Memory entry updated');
    });
  }

  async deleteEntry(index) {
    await this.mutate('DELETE', `/api/workspaces/${encodeURIComponent(this.workspaceId)}/memory/entries/${index}`, null, () => {
      if (window.Toast) window.Toast.info('Memory entry removed');
    });
  }

  // mutate performs a memory mutation, refreshes from the authoritative
  // response, and re-renders. onSuccess runs before re-render.
  async mutate(method, url, body, onSuccess) {
    try {
      const options = { method, headers: { 'Content-Type': 'application/json' } };
      if (body) options.body = JSON.stringify(body);
      const response = await fetch(url, options);
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload?.error?.message || payload?.message || `Request failed (${response.status})`);
      }
      const payload = await response.json();
      this.entries = Array.isArray(payload?.entries) ? payload.entries : [];
      this.unstructured = Array.isArray(payload?.unstructured) ? payload.unstructured : [];
      this.meta = {
        rawSize: Number(payload?.raw_size) || 0,
        charBudget: Number(payload?.char_budget) || 0,
        overBudget: Boolean(payload?.over_budget)
      };
      if (typeof onSuccess === 'function') onSuccess();
      this.render();
    } catch (error) {
      if (window.Toast) window.Toast.error(error.message || 'Memory update failed');
    }
  }
}
