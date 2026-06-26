/**
 * Templates page controller (/templates).
 *
 * Owns the dedicated project-template library page: master list (search + tag
 * filter), detail Overview (edit name/description/tags), and lifecycle actions
 * (create blank, import folder, duplicate, delete, reveal). The Files and
 * Onboarding tabs are placeholders here — they are filled in by later seams.
 *
 * The library is disk-first (a template is a folder), so every action is a thin
 * veneer over the project-templates HTTP API. Tag editing/filtering reuse the
 * shared widgets exposed on window by tag-input.js / tag-filter-bar.js (loaded
 * globally in head.tmpl).
 */

const tplState = {
  templates: [],
  root: '',
  selectedId: '',
  search: '',
  activeTags: [],
  tagsWidget: null,
  filterBar: null,
  nameAction: null // { title, label, confirm, initial, run(name) }
};

function tplToast(message, kind) {
  if (typeof window.showToast === 'function') {
    window.showToast(message, kind || 'info');
  } else if (kind === 'error') {
    console.error(message);
  }
}

function tplEl(id) {
  return document.getElementById(id);
}

async function tplFetchJSON(url, options) {
  const response = await fetch(url, options);
  const result = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(result.message || result.error || `Request failed (${response.status})`);
  }
  return result;
}

function tplShowError(message) {
  const box = tplEl('tplError');
  if (!box) return;
  if (!message) {
    box.classList.add('d-none');
    box.textContent = '';
    return;
  }
  box.textContent = message;
  box.classList.remove('d-none');
}

function tplSelected() {
  return tplState.templates.find((t) => t.id === tplState.selectedId) || null;
}

// --- Loading & list rendering ---

async function tplRefresh(selectId) {
  try {
    const data = await tplFetchJSON('/api/project-templates');
    tplState.templates = Array.isArray(data.templates) ? data.templates : [];
    tplState.root = data.templates_root || '';
    tplShowError('');
  } catch (error) {
    console.error('Failed to load templates:', error);
    tplShowError('Could not load the template library.');
    tplState.templates = [];
  }

  const rootPath = tplEl('tplRootPath');
  if (rootPath) rootPath.textContent = tplState.root;

  // Keep selection valid; default to the requested or first template.
  if (selectId && tplState.templates.some((t) => t.id === selectId)) {
    tplState.selectedId = selectId;
  } else if (!tplState.templates.some((t) => t.id === tplState.selectedId)) {
    tplState.selectedId = '';
  }

  tplSyncFilterTags();
  tplRenderList();
  tplRenderDetail();
}

function tplVisibleTemplates() {
  let items = tplState.templates;
  const fb = window.OriTagFilterBar;
  if (fb && tplState.activeTags.length) {
    items = fb.filterItems(items, tplState.activeTags, (t) => t.tags || []);
  }
  const q = tplState.search.trim().toLowerCase();
  if (q) {
    items = items.filter((t) =>
      [t.name, t.id, t.description].filter(Boolean).some((v) => v.toLowerCase().includes(q))
    );
  }
  return items;
}

function tplRenderList() {
  const list = tplEl('tplList');
  const empty = tplEl('tplEmpty');
  const count = tplEl('tplCount');
  if (!list) return;

  const visible = tplVisibleTemplates();
  if (count) count.textContent = String(visible.length);
  if (empty) empty.hidden = tplState.templates.length > 0;

  list.innerHTML = '';
  if (tplState.templates.length === 0) return;
  if (visible.length === 0) {
    const none = document.createElement('div');
    none.className = 'text-center py-3';
    none.style.cssText = 'color: var(--text-secondary); font-size: 13px;';
    none.textContent = 'No templates match the current filters.';
    list.appendChild(none);
    return;
  }

  for (const template of visible) {
    list.appendChild(tplBuildRow(template));
  }
}

function tplBuildRow(template) {
  const row = document.createElement('button');
  row.type = 'button';
  row.className = 'modern-card p-2 text-start w-100';
  row.setAttribute('role', 'listitem');
  row.style.cssText =
    'border: 1px solid var(--border-color); background: var(--bg-secondary);' +
    (template.id === tplState.selectedId ? ' outline: 2px solid var(--accent-color, #6366f1);' : '');
  row.addEventListener('click', () => {
    if (tplState.selectedId === template.id) return;
    tplState.selectedId = template.id;
    tplRenderList();
    tplRenderDetail();
  });

  const title = document.createElement('div');
  title.style.cssText = 'color: var(--text-primary); font-size: 14px; font-weight: 600; word-break: break-word;';
  title.textContent = template.name || template.id;
  row.appendChild(title);

  const id = document.createElement('div');
  id.style.cssText = 'font-size: 11px; color: var(--text-secondary); font-family: var(--font-mono, monospace);';
  id.textContent = template.id;
  row.appendChild(id);

  if (template.description) {
    const desc = document.createElement('div');
    desc.style.cssText =
      'font-size: 12px; color: var(--text-secondary); margin-top: 2px; overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;';
    desc.textContent = template.description;
    row.appendChild(desc);
  }

  if (Array.isArray(template.tags) && template.tags.length) {
    const tags = document.createElement('div');
    tags.className = 'd-flex gap-1 flex-wrap mt-1';
    for (const tag of template.tags) {
      const chip = document.createElement('span');
      chip.className = 'badge';
      chip.style.cssText = 'background: var(--bg-tertiary); color: var(--text-secondary); font-weight: 500; font-size: 10px;';
      chip.textContent = tag;
      tags.appendChild(chip);
    }
    row.appendChild(tags);
  }

  return row;
}

// --- Tag filter bar ---

function tplEnsureFilterBar() {
  if (tplState.filterBar || !window.OriTagFilterBar) return;
  const container = tplEl('tplTagFilter');
  if (!container) return;
  tplState.filterBar = window.OriTagFilterBar.createTagFilterBar({
    container,
    label: 'Tags',
    onChange: (active) => {
      tplState.activeTags = active;
      tplRenderList();
    }
  });
}

function tplSyncFilterTags() {
  tplEnsureFilterBar();
  if (!tplState.filterBar || !window.OriTagFilterBar) return;
  const available = window.OriTagFilterBar.collectTags(tplState.templates, (t) => t.tags || []);
  tplState.filterBar.setAvailableTags(available);
  tplState.activeTags = tplState.filterBar.getActiveTags();
}

// --- Detail / Overview ---

function tplEnsureTagsWidget() {
  if (tplState.tagsWidget || !window.OriTagInput) return;
  const container = tplEl('tplEditTags');
  if (!container) return;
  tplState.tagsWidget = window.OriTagInput.createTagInput({
    container,
    placeholder: 'Add a tag…',
    onChange: () => {}
  });
}

function tplRenderDetail() {
  const detail = tplEl('tplDetail');
  const emptyDetail = tplEl('tplDetailEmpty');
  const template = tplSelected();

  if (!template) {
    if (detail) detail.hidden = true;
    if (emptyDetail) emptyDetail.hidden = false;
    return;
  }
  if (emptyDetail) emptyDetail.hidden = true;
  if (detail) detail.hidden = false;

  const nameHeading = tplEl('tplDetailName');
  if (nameHeading) nameHeading.textContent = template.name || template.id;
  const idLabel = tplEl('tplDetailId');
  if (idLabel) idLabel.textContent = template.id;

  tplResetOverviewFields();
}

function tplResetOverviewFields() {
  const template = tplSelected();
  if (!template) return;
  const nameInput = tplEl('tplEditName');
  const descInput = tplEl('tplEditDescription');
  // A name equal to the id is a folder-name fallback, not an explicit name.
  if (nameInput) nameInput.value = template.name && template.name !== template.id ? template.name : '';
  if (descInput) descInput.value = template.description || '';
  tplEnsureTagsWidget();
  if (tplState.tagsWidget) tplState.tagsWidget.setTags(template.tags || []);
}

async function tplSaveOverview() {
  const template = tplSelected();
  if (!template) return;
  const nameInput = tplEl('tplEditName');
  const descInput = tplEl('tplEditDescription');
  const tags = tplState.tagsWidget ? tplState.tagsWidget.getTags() : (template.tags || []);
  try {
    await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: nameInput ? nameInput.value.trim() : '',
        description: descInput ? descInput.value.trim() : '',
        tags
      })
    });
    tplToast('Template saved.', 'success');
    await tplRefresh(template.id);
  } catch (error) {
    tplToast(error.message || 'Failed to save template', 'error');
  }
}

// --- Lifecycle ---

function tplOpenNameModal(action) {
  tplState.nameAction = action;
  const titleEl = tplEl('tplNameModalTitle');
  const labelEl = tplEl('tplNameModalLabel');
  const confirmEl = tplEl('tplNameModalConfirm');
  const input = tplEl('tplNameModalInput');
  if (titleEl) titleEl.textContent = action.title;
  if (labelEl) labelEl.textContent = action.label;
  if (confirmEl) confirmEl.textContent = action.confirm;
  if (input) input.value = action.initial || '';
  const modalEl = tplEl('tplNameModal');
  if (!modalEl) return;
  const modal = bootstrap.Modal.getOrCreateInstance(modalEl);
  modal.show();
  if (input) setTimeout(() => input.focus(), 200);
}

async function tplRunNameAction() {
  const action = tplState.nameAction;
  const input = tplEl('tplNameModalInput');
  if (!action || !input) return;
  const name = input.value.trim();
  try {
    await action.run(name);
    const modalEl = tplEl('tplNameModal');
    if (modalEl) bootstrap.Modal.getOrCreateInstance(modalEl).hide();
  } catch (error) {
    tplToast(error.message || 'Action failed', 'error');
  }
}

function tplCreate() {
  tplOpenNameModal({
    title: 'New Template',
    label: 'Template name',
    confirm: 'Create',
    initial: '',
    run: async (name) => {
      if (!name) throw new Error('Please enter a name.');
      const result = await tplFetchJSON('/api/project-templates', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name })
      });
      const created = result.template || {};
      tplToast(`Created "${created.name || created.id || name}".`, 'success');
      await tplRefresh(created.id);
    }
  });
}

function tplDuplicate() {
  const template = tplSelected();
  if (!template) return;
  tplOpenNameModal({
    title: 'Duplicate Template',
    label: 'New template name',
    confirm: 'Duplicate',
    initial: `${template.name || template.id} copy`,
    run: async (name) => {
      const result = await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}/duplicate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name })
      });
      const created = result.template || {};
      tplToast(`Duplicated to "${created.name || created.id}".`, 'success');
      await tplRefresh(created.id);
    }
  });
}

async function tplImport() {
  try {
    const picked = await tplFetchJSON('/api/folder-picker/select-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Select a Folder to Import as a Template' })
    });
    if (!picked.selected || !picked.path) return;
    const result = await tplFetchJSON('/api/project-templates/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: picked.path })
    });
    const imported = result.template || {};
    tplToast(`Imported "${imported.name || imported.id || 'template'}".`, 'success');
    await tplRefresh(imported.id);
  } catch (error) {
    tplToast(error.message || 'Failed to import folder', 'error');
  }
}

async function tplDelete() {
  const template = tplSelected();
  if (!template) return;
  const label = template.name || template.id;
  if (!window.confirm(`Delete the template "${label}"? It will be moved to the Trash when possible.`)) {
    return;
  }
  try {
    const result = await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, { method: 'DELETE' });
    tplToast(result.trashed ? `"${label}" moved to Trash.` : `"${label}" deleted.`, 'success');
    tplState.selectedId = '';
    await tplRefresh();
  } catch (error) {
    tplToast(error.message || 'Failed to delete template', 'error');
  }
}

async function tplReveal(id) {
  try {
    await tplFetchJSON('/api/project-templates/reveal', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: id || '' })
    });
  } catch (error) {
    tplToast(error.message || 'Failed to open folder', 'error');
  }
}

// --- Init ---

function tplInit() {
  const page = tplEl('templatesPage');
  if (!page) return;

  tplEl('tplCreateBtn')?.addEventListener('click', tplCreate);
  tplEl('tplImportBtn')?.addEventListener('click', () => void tplImport());
  tplEl('tplOpenLibraryBtn')?.addEventListener('click', () => void tplReveal(''));
  tplEl('tplRefreshBtn')?.addEventListener('click', () => void tplRefresh(tplState.selectedId));

  tplEl('tplDuplicateBtn')?.addEventListener('click', tplDuplicate);
  tplEl('tplRevealBtn')?.addEventListener('click', () => {
    const t = tplSelected();
    if (t) void tplReveal(t.id);
  });
  tplEl('tplDeleteBtn')?.addEventListener('click', () => void tplDelete());

  tplEl('tplSaveBtn')?.addEventListener('click', () => void tplSaveOverview());
  tplEl('tplResetBtn')?.addEventListener('click', tplResetOverviewFields);

  const search = tplEl('tplSearch');
  if (search) {
    search.addEventListener('input', () => {
      tplState.search = search.value;
      tplRenderList();
    });
  }

  tplEl('tplNameModalConfirm')?.addEventListener('click', () => void tplRunNameAction());
  tplEl('tplNameModalInput')?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      void tplRunNameAction();
    }
  });

  void tplRefresh();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', tplInit);
} else {
  tplInit();
}
