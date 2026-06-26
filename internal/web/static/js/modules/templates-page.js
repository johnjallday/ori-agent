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

// Files-tab state. `templateId` records which template the loaded tree belongs
// to (the tree lazy-loads when the Files tab is shown). `dirty` gates navigation.
const tplFiles = {
  templateId: '',
  tree: [],
  selectedPath: '',
  loadedContent: '',
  readOnly: false,
  dirty: false,
  pending: null // callback to run once a dirty prompt resolves
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

  // If the selected template changed out from under a loaded tree, drop it so
  // the Files tab doesn't show another template's files/editor.
  if (tplFiles.templateId && tplFiles.templateId !== tplState.selectedId) {
    tplFilesReset();
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
    tplGuardDirty(() => tplSelectTemplate(template.id));
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

// --- Files tab: tree + editor ---

function tplApiBase() {
  return `/api/project-templates/${encodeURIComponent(tplState.selectedId)}`;
}

// tplSelectTemplate switches the active template and resets the Files tab so a
// stale tree/editor never leaks across templates.
function tplSelectTemplate(id) {
  tplState.selectedId = id;
  tplFilesReset();
  tplRenderList();
  tplRenderDetail();
}

function tplFilesReset() {
  tplFiles.templateId = '';
  tplFiles.selectedPath = '';
  tplFiles.loadedContent = '';
  tplFiles.readOnly = false;
  tplClearDirty();
  const editor = tplEl('tplEditor');
  const empty = tplEl('tplEditorEmpty');
  if (editor) editor.hidden = true;
  if (empty) empty.hidden = false;
}

// tplFilesEnsureTree lazy-loads the tree the first time the Files tab is shown
// for the selected template.
function tplFilesEnsureTree() {
  if (!tplState.selectedId) return;
  if (tplFiles.templateId === tplState.selectedId) return;
  void tplFilesLoadTree();
}

async function tplFilesLoadTree() {
  const tree = tplEl('tplFileTree');
  if (!tree || !tplState.selectedId) return;
  try {
    const data = await tplFetchJSON(`${tplApiBase()}/files`);
    tplFiles.templateId = tplState.selectedId;
    tplFiles.tree = Array.isArray(data.files) ? data.files : [];
    tplRenderTree();
  } catch (error) {
    tree.innerHTML = '';
    const err = document.createElement('div');
    err.className = 'text-center py-3';
    err.style.cssText = 'color: var(--text-secondary); font-size: 13px;';
    err.textContent = 'Could not load files.';
    tree.appendChild(err);
    tplToast(error.message || 'Failed to load files', 'error');
  }
}

function tplRenderTree() {
  const tree = tplEl('tplFileTree');
  if (!tree) return;
  const nodes = tplFiles.tree;
  tree.innerHTML = '';
  if (nodes.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'text-center py-3';
    empty.style.cssText = 'color: var(--text-secondary); font-size: 13px;';
    empty.textContent = 'This template is empty. Use New File or New Folder above.';
    tree.appendChild(empty);
    return;
  }
  for (const node of nodes) {
    tree.appendChild(tplBuildTreeRow(node));
  }
}

function tplBuildTreeRow(node) {
  const depth = node.path.split('/').length - 1;
  const isDir = node.type === 'dir';
  const name = node.path.split('/').pop();

  const row = document.createElement(isDir ? 'div' : 'button');
  if (!isDir) row.type = 'button';
  row.className = 'd-flex align-items-center gap-2 px-2 py-1 text-start w-100';
  row.style.cssText =
    `border: none; border-radius: 4px; font-size: 13px; padding-left: ${8 + depth * 16}px !important;` +
    (isDir ? ' background: transparent; color: var(--text-secondary);' : ' background: var(--bg-secondary); color: var(--text-primary);') +
    (node.path === tplFiles.selectedPath ? ' outline: 2px solid var(--accent-color, #6366f1);' : '');
  row.setAttribute('role', 'treeitem');

  const icon = document.createElement('span');
  icon.textContent = isDir ? '📁' : '📄';
  icon.style.fontSize = '12px';
  row.appendChild(icon);

  const label = document.createElement('span');
  label.textContent = name;
  label.style.cssText = 'overflow: hidden; text-overflow: ellipsis; white-space: nowrap;';
  row.appendChild(label);

  if (node.is_manifest) {
    const badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = 'metadata';
    badge.style.cssText = 'background: var(--bg-tertiary); color: var(--text-secondary); font-size: 9px; font-weight: 500;';
    row.appendChild(badge);
  }

  if (!isDir) {
    row.addEventListener('click', () => {
      if (node.path === tplFiles.selectedPath) return;
      tplGuardDirty(() => void tplOpenFile(node.path));
    });
  }
  return row;
}

async function tplOpenFile(path) {
  if (!tplState.selectedId) return;
  try {
    const res = await fetch(`${tplApiBase()}/files/content?path=${encodeURIComponent(path)}`);
    if (res.status === 413) {
      tplEditorShow(path, '', true, 'This file is larger than 512 KB and can’t be edited here. Use Reveal to open the template folder on disk.');
      return;
    }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      tplToast(data.message || `Failed to open file (${res.status})`, 'error');
      return;
    }
    let notice = '';
    if (data.binary) {
      notice = 'Binary file — read-only. Use Reveal to open it on disk.';
    } else if (data.read_only) {
      notice = 'template.json is managed by the Overview and Onboarding tabs — read-only here.';
    }
    tplEditorShow(data.path || path, data.content || '', Boolean(data.read_only), notice);
  } catch (error) {
    tplToast(error.message || 'Failed to open file', 'error');
  }
}

function tplEditorShow(path, content, readOnly, notice) {
  tplFiles.selectedPath = path;
  tplFiles.loadedContent = content;
  tplFiles.readOnly = readOnly;

  const editor = tplEl('tplEditor');
  const empty = tplEl('tplEditorEmpty');
  const pathEl = tplEl('tplEditorPath');
  const textarea = tplEl('tplEditorTextarea');
  const noticeEl = tplEl('tplEditorNotice');
  const saveBtn = tplEl('tplEditorSaveBtn');

  if (empty) empty.hidden = true;
  if (editor) editor.hidden = false;
  if (pathEl) pathEl.textContent = path;
  if (textarea) {
    textarea.value = content;
    textarea.readOnly = readOnly;
  }
  if (noticeEl) {
    noticeEl.textContent = notice || '';
    noticeEl.hidden = !notice;
  }
  if (saveBtn) saveBtn.disabled = readOnly;
  tplClearDirty();
  tplRenderTree(); // re-render to move the selection highlight to this file
}

async function tplEditorSave() {
  const path = tplFiles.selectedPath;
  const textarea = tplEl('tplEditorTextarea');
  if (!path || tplFiles.readOnly || !textarea) return false;
  try {
    await tplFetchJSON(`${tplApiBase()}/files/content`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content: textarea.value })
    });
    tplFiles.loadedContent = textarea.value;
    tplClearDirty();
    tplToast('File saved.', 'success');
    return true;
  } catch (error) {
    tplToast(error.message || 'Failed to save file', 'error');
    return false;
  }
}

function tplMarkDirty() {
  tplFiles.dirty = true;
  const ind = tplEl('tplEditorDirty');
  if (ind) ind.hidden = false;
}

function tplClearDirty() {
  tplFiles.dirty = false;
  const ind = tplEl('tplEditorDirty');
  if (ind) ind.hidden = true;
}

// tplGuardDirty runs `proceed` immediately unless the editor has unsaved
// changes, in which case it opens the save / discard / keep-editing prompt.
function tplGuardDirty(proceed) {
  if (!tplFiles.dirty) {
    proceed();
    return;
  }
  tplFiles.pending = proceed;
  const fileEl = tplEl('tplDirtyFile');
  if (fileEl) fileEl.textContent = tplFiles.selectedPath || '(file)';
  const modalEl = tplEl('tplDirtyModal');
  if (modalEl) bootstrap.Modal.getOrCreateInstance(modalEl).show();
}

async function tplDirtyResolve(action) {
  const modalEl = tplEl('tplDirtyModal');
  const proceed = tplFiles.pending;
  if (action === 'cancel') {
    tplFiles.pending = null;
    if (modalEl) bootstrap.Modal.getOrCreateInstance(modalEl).hide();
    return;
  }
  if (action === 'save') {
    const ok = await tplEditorSave();
    if (!ok) return; // keep the prompt open on save failure
  }
  tplClearDirty();
  tplFiles.pending = null;
  if (modalEl) bootstrap.Modal.getOrCreateInstance(modalEl).hide();
  if (typeof proceed === 'function') proceed();
}

function tplFileCreate(type) {
  if (!tplState.selectedId) return;
  tplOpenNameModal({
    title: type === 'dir' ? 'New Folder' : 'New File',
    label: 'Path (relative to the template)',
    confirm: 'Create',
    initial: '',
    run: async (path) => {
      if (!path) throw new Error('Please enter a path.');
      const result = await tplFetchJSON(`${tplApiBase()}/files`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path, type })
      });
      await tplFilesLoadTree();
      const node = result.node || {};
      if (type === 'file') void tplOpenFile(node.path || path);
    }
  });
}

function tplFileRename() {
  const path = tplFiles.selectedPath;
  if (!path) return;
  tplGuardDirty(() => {
    tplOpenNameModal({
      title: 'Rename / Move',
      label: 'New path (relative to the template)',
      confirm: 'Rename',
      initial: path,
      run: async (to) => {
        if (!to || to === path) return;
        const result = await tplFetchJSON(`${tplApiBase()}/files/rename`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ from: path, to })
        });
        await tplFilesLoadTree();
        const node = result.node || {};
        void tplOpenFile(node.path || to);
      }
    });
  });
}

async function tplFileDelete() {
  const path = tplFiles.selectedPath;
  if (!path) return;
  if (!window.confirm(`Delete "${path}" from the template? This cannot be undone from the app.`)) {
    return;
  }
  try {
    await tplFetchJSON(`${tplApiBase()}/files?path=${encodeURIComponent(path)}`, { method: 'DELETE' });
    tplToast(`Deleted "${path}".`, 'success');
    tplClearDirty();
    tplFilesReset();
    tplFiles.templateId = '';
    await tplFilesLoadTree();
  } catch (error) {
    tplToast(error.message || 'Failed to delete', 'error');
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

  // Files tab wiring.
  tplEl('tplFileNewBtn')?.addEventListener('click', () => tplFileCreate('file'));
  tplEl('tplFolderNewBtn')?.addEventListener('click', () => tplFileCreate('dir'));
  tplEl('tplFilesRefreshBtn')?.addEventListener('click', () => {
    tplFiles.templateId = '';
    void tplFilesLoadTree();
  });
  tplEl('tplFileRenameBtn')?.addEventListener('click', tplFileRename);
  tplEl('tplFileDeleteBtn')?.addEventListener('click', () => void tplFileDelete());
  tplEl('tplEditorSaveBtn')?.addEventListener('click', () => void tplEditorSave());

  const textarea = tplEl('tplEditorTextarea');
  if (textarea) {
    textarea.addEventListener('input', () => {
      if (tplFiles.readOnly) return;
      if (textarea.value === tplFiles.loadedContent) tplClearDirty();
      else tplMarkDirty();
    });
  }

  // Dirty-prompt buttons.
  tplEl('tplDirtySave')?.addEventListener('click', () => void tplDirtyResolve('save'));
  tplEl('tplDirtyDiscard')?.addEventListener('click', () => void tplDirtyResolve('discard'));
  tplEl('tplDirtyCancel')?.addEventListener('click', () => void tplDirtyResolve('cancel'));

  // Lazy-load the tree when the Files tab is shown; guard tab-switch when dirty.
  const filesTab = tplEl('tplTabFiles');
  if (filesTab) {
    filesTab.addEventListener('shown.bs.tab', () => tplFilesEnsureTree());
    filesTab.addEventListener('hide.bs.tab', (event) => {
      if (!tplFiles.dirty) return;
      const target = event.relatedTarget;
      event.preventDefault();
      tplGuardDirty(() => {
        if (target) bootstrap.Tab.getOrCreateInstance(target).show();
      });
    });
  }

  void tplRefresh();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', tplInit);
} else {
  tplInit();
}
