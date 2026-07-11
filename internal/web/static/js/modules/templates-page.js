/**
 * Templates page controller (/templates).
 *
 * Owns the dedicated project-template library page: master list (search + tag
 * filter), detail Overview (edit name/description/tags), and lifecycle actions
 * (create blank, import folder, duplicate, delete, reveal), plus Files,
 * Tools, and Agents tabs.
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
  const type = kind || 'info';
  if (typeof window.notifyToast === 'function') {
    window.notifyToast(message, type);
  } else if (window.Toast && typeof window.Toast.show === 'function') {
    window.Toast.show(message, type);
  } else if (typeof window.showToast === 'function') {
    window.showToast(message, type);
  } else if (type === 'error') {
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
  return tplState.templates.find(t => t.id === tplState.selectedId) || null;
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
  if (selectId && tplState.templates.some(t => t.id === selectId)) {
    tplState.selectedId = selectId;
  } else if (!tplState.templates.some(t => t.id === tplState.selectedId)) {
    tplState.selectedId = '';
  }

  // If the selected template changed out from under a loaded Files tree or
  // Tools editor, drop them so neither tab shows another template's data.
  if (
    (tplFiles.templateId && tplFiles.templateId !== tplState.selectedId) ||
    (tplTools.templateId && tplTools.templateId !== tplState.selectedId)
  ) {
    tplFilesReset();
    tplToolsReset();
  }

  tplSyncFilterTags();
  tplRenderList();
  tplRenderDetail();
}

function tplVisibleTemplates() {
  let items = tplState.templates;
  const fb = window.OriTagFilterBar;
  if (fb && tplState.activeTags.length) {
    items = fb.filterItems(items, tplState.activeTags, t => t.tags || []);
  }
  const q = tplState.search.trim().toLowerCase();
  if (q) {
    items = items.filter(t =>
      [t.name, t.id, t.description].filter(Boolean).some(v => v.toLowerCase().includes(q))
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
    (template.id === tplState.selectedId
      ? ' outline: 2px solid var(--accent-color, #6366f1);'
      : '');
  row.addEventListener('click', () => {
    if (tplState.selectedId === template.id) return;
    tplGuardDirty(() => tplSelectTemplate(template.id));
  });

  const title = document.createElement('div');
  title.style.cssText =
    'color: var(--text-primary); font-size: 14px; font-weight: 600; word-break: break-word;';
  title.textContent = (template.icon ? `${template.icon} ` : '') + (template.name || template.id);
  if (template.builtin) {
    const badge = document.createElement('span');
    badge.className = 'badge ms-1';
    badge.style.cssText =
      'background: var(--bg-tertiary); color: var(--text-secondary); font-weight: 600; font-size: 9px; vertical-align: middle;';
    badge.textContent = 'BUILT-IN';
    title.appendChild(badge);
  }
  row.appendChild(title);

  const id = document.createElement('div');
  id.style.cssText =
    'font-size: 11px; color: var(--text-secondary); font-family: var(--font-mono, monospace);';
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
      chip.style.cssText =
        'background: var(--bg-tertiary); color: var(--text-secondary); font-weight: 500; font-size: 10px;';
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
    onChange: active => {
      tplState.activeTags = active;
      tplRenderList();
    }
  });
}

function tplSyncFilterTags() {
  tplEnsureFilterBar();
  if (!tplState.filterBar || !window.OriTagFilterBar) return;
  const available = window.OriTagFilterBar.collectTags(tplState.templates, t => t.tags || []);
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
  // Reset the agents editor so it reloads for the newly-selected template.
  tplAgents.templateId = '';
  tplAgentsLoad();
  tplApplyReadOnly();
}

// --- Starter tasks row editor ---

// tplStarterTaskRow builds one editable starter-task row: description input,
// Markdown details textarea, a setup toggle (at most one across rows —
// checking one unchecks the others), and a remove button.
function tplStarterTaskRow(task) {
  const row = document.createElement('div');
  row.className = 'modern-card p-2';
  row.dataset.role = 'starter-task';

  const top = document.createElement('div');
  top.className = 'd-flex gap-2 align-items-center mb-2';
  const desc = document.createElement('input');
  desc.className = 'modern-input flex-grow-1';
  desc.placeholder = 'Task description';
  desc.dataset.k = 'description';
  desc.value = task && task.description ? task.description : '';
  top.appendChild(desc);

  const setupWrap = document.createElement('label');
  setupWrap.className = 'd-flex align-items-center gap-1 mb-0';
  setupWrap.style.cssText =
    'font-size: 12px; color: var(--text-secondary); white-space: nowrap; cursor: pointer;';
  const setup = document.createElement('input');
  setup.type = 'checkbox';
  setup.className = 'form-check-input mt-0';
  setup.dataset.k = 'setup';
  setup.checked = Boolean(task && task.setup);
  setup.title = 'Auto-starts once when the workspace is first opened';
  setup.addEventListener('change', () => {
    if (!setup.checked) return;
    // At most one setup task: checking this one unchecks the rest.
    document.querySelectorAll('#tplStarterTasksList input[data-k="setup"]').forEach(other => {
      if (other !== setup) other.checked = false;
    });
  });
  setupWrap.appendChild(setup);
  setupWrap.appendChild(document.createTextNode('Setup task'));
  top.appendChild(setupWrap);

  const removeBtn = document.createElement('button');
  removeBtn.type = 'button';
  removeBtn.className = 'modern-btn modern-btn-secondary btn-sm';
  removeBtn.style.color = '#ef4444';
  removeBtn.textContent = 'Remove';
  removeBtn.addEventListener('click', () => row.remove());
  top.appendChild(removeBtn);
  row.appendChild(top);

  const details = document.createElement('textarea');
  details.className = 'form-control';
  details.rows = 3;
  details.dataset.k = 'details';
  details.style.cssText =
    'background: var(--bg-tertiary); border: 1px solid var(--border-color); color: var(--text-primary); font-size: 0.85em; resize: vertical;';
  details.placeholder =
    'Details for the agent (Markdown). For a setup task, consider headings:\n## Created defaults\n## Questions to ask\n## Validation\n## How to apply changes';
  details.value = task && task.details ? task.details : '';
  row.appendChild(details);

  return row;
}

function tplStarterTasksRender(tasks) {
  const list = tplEl('tplStarterTasksList');
  if (!list) return;
  list.innerHTML = '';
  (Array.isArray(tasks) ? tasks : []).forEach(t => list.appendChild(tplStarterTaskRow(t)));
}

// tplStarterTasksCollect reads the rows back into starter-task objects,
// dropping rows without a description. The at-most-one setup rule is enforced
// by the row toggles; the server validates it again on save.
function tplStarterTasksCollect() {
  const list = tplEl('tplStarterTasksList');
  if (!list) return [];
  return Array.from(list.querySelectorAll('[data-role="starter-task"]'))
    .map(row => {
      const val = k => row.querySelector(`[data-k="${k}"]`);
      const description = (val('description')?.value || '').trim();
      if (!description) return null;
      const task = { description, details: (val('details')?.value || '').trim() };
      if (val('setup')?.checked) task.setup = true;
      return task;
    })
    .filter(Boolean);
}

function tplResetOverviewFields() {
  const template = tplSelected();
  if (!template) return;
  const nameInput = tplEl('tplEditName');
  const descInput = tplEl('tplEditDescription');
  // A name equal to the id is a folder-name fallback, not an explicit name.
  if (nameInput)
    nameInput.value = template.name && template.name !== template.id ? template.name : '';
  if (descInput) descInput.value = template.description || '';
  const iconInput = tplEl('tplEditIcon');
  if (iconInput) iconInput.value = template.icon || '';
  const behaviorSelect = tplEl('tplEditBehavior');
  if (behaviorSelect) behaviorSelect.value = template.behavior_profile || 'general';
  const projectEntryPath = tplEl('tplEditProjectEntryPath');
  const projectEntryDefault = tplEl('tplEditProjectEntryDefault');
  const entry =
    template.project_entry && typeof template.project_entry === 'object'
      ? template.project_entry
      : null;
  if (projectEntryPath) projectEntryPath.value = entry?.relative_path || '';
  if (projectEntryDefault) projectEntryDefault.checked = Boolean(entry?.open_after_create_default);
  tplStarterTasksRender(template.starter_tasks);
  tplEnsureTagsWidget();
  if (tplState.tagsWidget) tplState.tagsWidget.setTags(template.tags || []);
}

async function tplSaveOverview() {
  const template = tplSelected();
  if (!template) return;
  const nameInput = tplEl('tplEditName');
  const descInput = tplEl('tplEditDescription');
  const iconInput = tplEl('tplEditIcon');
  const behaviorSelect = tplEl('tplEditBehavior');
  const projectEntryPath = tplEl('tplEditProjectEntryPath');
  const projectEntryDefault = tplEl('tplEditProjectEntryDefault');
  const entryPath = projectEntryPath ? projectEntryPath.value.trim() : '';
  const tags = tplState.tagsWidget ? tplState.tagsWidget.getTags() : template.tags || [];
  try {
    await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: nameInput ? nameInput.value.trim() : '',
        description: descInput ? descInput.value.trim() : '',
        tags,
        icon: iconInput ? iconInput.value.trim() : '',
        behavior_profile: behaviorSelect ? behaviorSelect.value : 'general',
        starter_tasks: tplStarterTasksCollect(),
        project_entry: entryPath
          ? {
              relative_path: entryPath,
              open_after_create_default: Boolean(projectEntryDefault?.checked)
            }
          : null
      })
    });
    tplToast('Template saved.', 'success');
    await tplRefresh(template.id);
  } catch (error) {
    tplToast(error.message || 'Failed to save template', 'error');
  }
}

// tplApplyReadOnly toggles the built-in badge/notice and disables every
// mutating control for built-in templates (the backend also rejects mutations).
function tplApplyReadOnly() {
  const template = tplSelected();
  const builtin = Boolean(template && template.builtin);
  const badge = tplEl('tplDetailBuiltinBadge');
  if (badge) badge.hidden = !builtin;
  const notice = tplEl('tplReadOnlyNotice');
  if (notice) notice.hidden = !builtin;
  const agentsNotice = tplEl('tplAgentsReadOnlyNotice');
  if (agentsNotice) agentsNotice.hidden = !builtin;
  ['tplAgentsAddBtn', 'tplAgentsSaveBtn'].forEach(id => {
    const el = tplEl(id);
    if (el) el.hidden = builtin;
  });
  [
    'tplEditName',
    'tplEditDescription',
    'tplEditIcon',
    'tplEditBehavior',
    'tplEditProjectEntryPath',
    'tplEditProjectEntryDefault',
    'tplStarterTaskAddBtn',
    'tplSaveBtn',
    'tplResetBtn',
    'tplDeleteBtn',
    'tplFileNewBtn',
    'tplFolderNewBtn',
    'tplFileRenameBtn',
    'tplFileDeleteBtn',
    'tplEditorSaveBtn',
    'tplToolsSaveBtn',
    'tplAgentsAddBtn',
    'tplAgentsSaveBtn'
  ].forEach(id => {
    const el = tplEl(id);
    if (el) el.disabled = builtin;
  });
  // The starter-tasks row editor is built dynamically; disable its controls too.
  document
    .querySelectorAll(
      '#tplStarterTasksList input, #tplStarterTasksList textarea, #tplStarterTasksList button'
    )
    .forEach(el => {
      el.disabled = builtin;
    });
  // The tags widget is a custom component; dim + block interaction for built-ins.
  const tagsWrap = tplEl('tplEditTags');
  if (tagsWrap) {
    tagsWrap.style.pointerEvents = builtin ? 'none' : '';
    tagsWrap.style.opacity = builtin ? '0.6' : '';
  }
  // Agent cards are rendered dynamically; disable their controls + drag for built-ins.
  const agentsList = tplEl('tplAgentsList');
  if (agentsList) {
    agentsList.querySelectorAll('input, select, textarea, button').forEach(el => {
      el.disabled = builtin;
    });
    agentsList.querySelectorAll('.tpl-agent-card').forEach(card => {
      card.draggable = !builtin;
    });
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
    run: async name => {
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
    run: async name => {
      const result = await tplFetchJSON(
        `/api/project-templates/${encodeURIComponent(template.id)}/duplicate`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name })
        }
      );
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
  if (
    !window.confirm(`Delete the template "${label}"? It will be moved to the Trash when possible.`)
  ) {
    return;
  }
  try {
    const result = await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
      method: 'DELETE'
    });
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
  tplToolsReset();
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
    (isDir
      ? ' background: transparent; color: var(--text-secondary);'
      : ' background: var(--bg-secondary); color: var(--text-primary);') +
    (node.path === tplFiles.selectedPath
      ? ' outline: 2px solid var(--accent-color, #6366f1);'
      : '');
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
    badge.style.cssText =
      'background: var(--bg-tertiary); color: var(--text-secondary); font-size: 9px; font-weight: 500;';
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
      tplEditorShow(
        path,
        '',
        true,
        'This file is larger than 512 KB and can’t be edited here. Use Reveal to open the template folder on disk.'
      );
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
      notice = 'template.json is managed by the Overview, Tools, and Agents tabs — read-only here.';
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
    run: async path => {
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
      run: async to => {
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
    await tplFetchJSON(`${tplApiBase()}/files?path=${encodeURIComponent(path)}`, {
      method: 'DELETE'
    });
    tplToast(`Deleted "${path}".`, 'success');
    tplClearDirty();
    tplFilesReset();
    tplFiles.templateId = '';
    await tplFilesLoadTree();
  } catch (error) {
    tplToast(error.message || 'Failed to delete', 'error');
  }
}

// --- Tools tab: default skills / MCP servers / plugins ---

const tplTools = { templateId: '' };

function tplToolsReset() {
  tplTools.templateId = '';
  for (const id of ['tplToolsSkills', 'tplToolsMcp', 'tplToolsPlugins']) {
    const el = tplEl(id);
    if (el) el.innerHTML = '';
  }
}

function tplToolsEnsure() {
  if (!tplState.selectedId) return;
  if (tplTools.templateId === tplState.selectedId) return;
  void tplToolsLoad();
}

function tplToolsDeclared() {
  const tools = (tplSelected() || {}).tools || {};
  return {
    skills: tools.skills || [],
    mcp_servers: tools.mcp_servers || [],
    plugins: tools.plugins || []
  };
}

async function tplToolsLoad() {
  if (!tplState.selectedId) return;
  tplTools.templateId = tplState.selectedId;
  const declared = tplToolsDeclared();
  await Promise.all([
    tplToolsRenderSection('tplToolsSkills', '/api/skills', ['skills', 'items'], declared.skills),
    tplToolsRenderSection(
      'tplToolsMcp',
      '/api/mcp/servers',
      ['servers', 'items'],
      declared.mcp_servers
    ),
    tplToolsRenderSection('tplToolsPlugins', '/api/plugins', ['plugins', 'items'], declared.plugins)
  ]);
}

async function tplToolsRenderSection(containerId, url, listKeys, declaredNames) {
  const container = tplEl(containerId);
  if (!container) return;
  container.innerHTML = '';

  let installed = [];
  try {
    const data = await tplFetchJSON(url);
    installed = tplToolsExtractList(data, listKeys).map(tplToolsItemName).filter(Boolean);
  } catch (error) {
    console.warn(`Failed to load ${url}:`, error);
  }

  const declaredSet = new Set(declaredNames.map(n => n.toLowerCase()));
  const seen = new Set();
  const rows = [];
  for (const name of installed) {
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    rows.push({ name, installed: true, checked: declaredSet.has(key) });
  }
  // Keep declared names that aren't currently installed rather than dropping them.
  for (const name of declaredNames) {
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    rows.push({ name, installed: false, checked: true });
  }

  if (rows.length === 0) {
    const empty = document.createElement('div');
    empty.style.cssText = 'font-size: 12px; color: var(--text-secondary);';
    empty.textContent = 'None available.';
    container.appendChild(empty);
    return;
  }
  rows.sort((a, b) => a.name.localeCompare(b.name));
  for (const row of rows) container.appendChild(tplToolsRow(row));
}

function tplToolsRow(row) {
  const label = document.createElement('label');
  label.className = 'd-flex align-items-center gap-2';
  label.style.cssText = 'font-size: 13px; color: var(--text-primary); cursor: pointer;';
  const cb = document.createElement('input');
  cb.type = 'checkbox';
  cb.className = 'form-check-input';
  cb.checked = row.checked;
  cb.dataset.name = row.name;
  label.appendChild(cb);
  const span = document.createElement('span');
  span.textContent = row.name;
  label.appendChild(span);
  if (!row.installed) {
    const tag = document.createElement('span');
    tag.className = 'badge';
    tag.textContent = 'not installed';
    tag.style.cssText =
      'background: var(--bg-tertiary); color: var(--text-secondary); font-size: 10px;';
    label.appendChild(tag);
  }
  return label;
}

function tplToolsExtractList(data, keys) {
  if (Array.isArray(data)) return data;
  for (const k of keys) {
    if (Array.isArray(data && data[k])) return data[k];
  }
  return [];
}

function tplToolsItemName(item) {
  if (typeof item === 'string') return item.trim();
  if (!item || typeof item !== 'object') return '';
  return (item.name || item.id || item.server_name || item.skill_name || '').trim();
}

function tplToolsCollect(containerId) {
  const container = tplEl(containerId);
  if (!container) return [];
  return Array.from(container.querySelectorAll('input[type="checkbox"]:checked'))
    .map(cb => cb.dataset.name)
    .filter(Boolean);
}

async function tplToolsSave() {
  const template = tplSelected();
  if (!template) return;
  const body = {
    skills: tplToolsCollect('tplToolsSkills'),
    mcp_servers: tplToolsCollect('tplToolsMcp'),
    plugins: tplToolsCollect('tplToolsPlugins')
  };
  try {
    await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}/tools`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    tplToast('Tools saved.', 'success');
    await tplRefresh(template.id);
    tplTools.templateId = '';
    await tplToolsLoad();
  } catch (error) {
    tplToast(error.message || 'Failed to save tools', 'error');
  }
}

// --- Agents tab: roster editor ---
//
// The working roster lives in the DOM (one card per agent); edits are read back
// with tplAgentsCollect() before any structural change (add/remove/reorder) so
// in-progress input is never lost. The first card is the entry agent.

const TPL_AGENT_ROLES = [
  '',
  'orchestrator',
  'specialist',
  'researcher',
  'analyzer',
  'synthesizer',
  'validator',
  'general'
];
const TPL_AGENT_TYPES = ['', 'tool-calling', 'general', 'research'];

const tplAgents = {
  templateId: '',
  dragIndex: -1,
  existingNames: [],
  existingLoaded: false,
  promptVars: [],
  promptVarsLoaded: false
};

function tplAgentsBlank() {
  return {
    name: '',
    role: '',
    type: '',
    model: '',
    system_prompt: '',
    tools: { skills: [], mcp_servers: [] }
  };
}

function tplAgentsNormalizeList(agents) {
  return (Array.isArray(agents) ? agents : []).map(a => ({
    name: a.name || '',
    role: a.role || '',
    type: a.type || '',
    model: a.model || '',
    system_prompt: a.system_prompt || '',
    tools: {
      skills: a.tools && Array.isArray(a.tools.skills) ? a.tools.skills : [],
      mcp_servers: a.tools && Array.isArray(a.tools.mcp_servers) ? a.tools.mcp_servers : []
    }
  }));
}

// tplAgentsLoad seeds the editor from the selected template, but only when the
// template changes — so re-showing the tab keeps unsaved edits.
function tplAgentsLoad() {
  const template = tplSelected();
  if (!template) return;
  if (tplAgents.templateId === template.id) return;
  tplAgents.templateId = template.id;
  tplAgentsRender(template.agents);
}

function tplAgentsDisplayName(agent, index) {
  const name = String(agent?.name || '').trim();
  if (name) return name;
  return index === 0 ? 'Entry agent' : `Agent ${index + 1}`;
}

function tplAgentsInitials(name, index) {
  const cleaned = String(name || '').trim();
  const parts = cleaned.split(/\s+/).filter(Boolean);
  const initials = parts
    .slice(0, 2)
    .map(part => part.charAt(0).toUpperCase())
    .join('');
  return initials || String(index + 1).padStart(2, '0');
}

function tplAgentsRoleLabel(agent, index) {
  if (index === 0) return 'Entry agent';
  const role = String(agent?.role || '').trim();
  if (!role) return 'Specialist';
  return role.replace(/[_-]+/g, ' ').replace(/\b\w/g, ch => ch.toUpperCase());
}

function tplAgentsTypeLabel(type) {
  const value = String(type || '').trim();
  if (!value) return 'Default type';
  return value.replace(/[_-]+/g, ' ').replace(/\b\w/g, ch => ch.toUpperCase());
}

function tplAgentsChip(text, kind = '') {
  const chip = document.createElement('span');
  chip.className = `tpl-agent-chip${kind ? ` is-${kind}` : ''}`;
  chip.textContent = text;
  return chip;
}

function tplAgentsChipList(values, emptyText, kind = '') {
  const list = document.createElement('div');
  list.className = 'tpl-agent-chip-list';
  const items = (Array.isArray(values) ? values : [])
    .map(item => String(item || '').trim())
    .filter(Boolean);
  if (items.length === 0) {
    list.appendChild(tplAgentsChip(emptyText, 'empty'));
    return list;
  }
  items.slice(0, 3).forEach(item => list.appendChild(tplAgentsChip(item, kind)));
  if (items.length > 3) list.appendChild(tplAgentsChip(`+${items.length - 3}`, 'count'));
  return list;
}

function tplAgentsField(label, input) {
  const wrap = document.createElement('div');
  wrap.className = 'tpl-agent-field';
  const lab = document.createElement('label');
  lab.className = 'tpl-agent-field-label';
  lab.textContent = label;
  wrap.appendChild(lab);
  wrap.appendChild(input);
  return wrap;
}

function tplAgentsCol(label, input) {
  const col = document.createElement('div');
  col.className = 'tpl-agent-form-col';
  col.appendChild(tplAgentsField(label, input));
  return col;
}

function tplAgentsSelect(cls, values, selected) {
  const sel = document.createElement('select');
  sel.className = `modern-input w-100 ${cls}`;
  values.forEach(v => {
    const opt = document.createElement('option');
    opt.value = v;
    opt.textContent = v === '' ? 'Default' : v;
    if (v === selected) opt.selected = true;
    sel.appendChild(opt);
  });
  return sel;
}

function tplAgentsInput(cls, value, placeholder, listId) {
  const i = document.createElement('input');
  i.type = 'text';
  i.className = `modern-input w-100 ${cls}`;
  i.value = value || '';
  i.placeholder = placeholder || '';
  if (listId) i.setAttribute('list', listId);
  return i;
}

// tplAgentsEnsureExistingNames lazily loads the global agent names once so the
// roster name field can offer an "attach existing agent" picker (a datalist) and
// flag when a typed name will reuse an existing definition (PRD FR7 / reuse
// surfaces). Non-fatal: on failure the picker simply offers no suggestions.
async function tplAgentsEnsureExistingNames() {
  if (tplAgents.existingLoaded) return;
  tplAgents.existingLoaded = true;
  try {
    const res = await fetch('/api/agents');
    const data = await res.json().catch(() => ({}));
    tplAgents.existingNames = Array.isArray(data.agents)
      ? data.agents.map(a => (a && typeof a === 'object' ? a.name : a)).filter(Boolean)
      : [];
  } catch {
    tplAgents.existingNames = [];
  }
  tplAgentsPopulateDatalist();
}

function tplAgentsPopulateDatalist() {
  let dl = document.getElementById('tpl-existing-agents');
  if (!dl) {
    dl = document.createElement('datalist');
    dl.id = 'tpl-existing-agents';
    document.body.appendChild(dl);
  }
  dl.innerHTML = '';
  (tplAgents.existingNames || []).forEach(name => {
    const opt = document.createElement('option');
    opt.value = String(name);
    dl.appendChild(opt);
  });
}

function tplAgentsNameMatchesExisting(value) {
  const v = String(value || '')
    .trim()
    .toLowerCase();
  if (!v) return false;
  return (tplAgents.existingNames || []).some(n => String(n).toLowerCase() === v);
}

// tplAgentsEnsurePromptVars lazily loads the closed prompt-variable vocabulary
// once, for the inserter chips (PRD FR27). Non-fatal: on failure the inserter
// simply offers no chips.
async function tplAgentsEnsurePromptVars() {
  if (tplAgents.promptVarsLoaded) return tplAgents.promptVars;
  tplAgents.promptVarsLoaded = true;
  try {
    const res = await fetch('/api/prompt-variables');
    const data = await res.json().catch(() => ({}));
    tplAgents.promptVars = Array.isArray(data.variables) ? data.variables : [];
  } catch {
    tplAgents.promptVars = [];
  }
  return tplAgents.promptVars;
}

// tplInsertAtCursor inserts text at the textarea's caret (or end), keeps focus,
// and fires an input event so any dependent state updates.
function tplInsertAtCursor(textarea, text) {
  const start = Number.isInteger(textarea.selectionStart)
    ? textarea.selectionStart
    : textarea.value.length;
  const end = Number.isInteger(textarea.selectionEnd)
    ? textarea.selectionEnd
    : textarea.value.length;
  textarea.value = textarea.value.slice(0, start) + text + textarea.value.slice(end);
  const pos = start + text.length;
  textarea.selectionStart = pos;
  textarea.selectionEnd = pos;
  textarea.focus();
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}

// tplAgentsPromptTools wraps a system-prompt textarea with a variable inserter
// (clickable vocabulary chips) and a live resolved-prompt preview against a
// synthetic sample workspace (PRD FR27/FR28).
function tplAgentsPromptTools(textarea) {
  const wrap = document.createElement('div');
  wrap.className = 'tpl-prompt-tools';
  wrap.appendChild(textarea);

  const toolbar = document.createElement('div');
  toolbar.className = 'tpl-prompt-toolbar';

  const label = document.createElement('span');
  label.className = 'tpl-prompt-toolbar-label';
  label.textContent = 'Insert:';
  toolbar.appendChild(label);

  const chips = document.createElement('span');
  chips.className = 'tpl-prompt-var-chips';
  toolbar.appendChild(chips);

  const previewBtn = document.createElement('button');
  previewBtn.type = 'button';
  previewBtn.className = 'btn btn-sm btn-outline-secondary tpl-prompt-preview-btn';
  previewBtn.textContent = 'Preview';
  toolbar.appendChild(previewBtn);
  wrap.appendChild(toolbar);

  const panel = document.createElement('pre');
  panel.className = 'tpl-prompt-preview-panel';
  panel.hidden = true;
  wrap.appendChild(panel);

  tplAgentsEnsurePromptVars().then(vars => {
    chips.innerHTML = '';
    vars.forEach(v => {
      const chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'tpl-prompt-var-chip';
      chip.textContent = v.name;
      chip.title = v.description || v.name;
      chip.addEventListener('click', () => tplInsertAtCursor(textarea, `{{${v.name}}}`));
      chips.appendChild(chip);
    });
  });

  previewBtn.addEventListener('click', async () => {
    panel.hidden = false;
    panel.textContent = 'Resolving preview…';
    try {
      const res = await fetch('/api/prompt-variables/preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prompt: textarea.value })
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        panel.textContent = data.message || data.error || 'Preview failed.';
        return;
      }
      const header = data.had_variables
        ? '# Preview — synthetic sample workspace\n(Live runtime uses the real workspace and adds context automatically.)\n\n'
        : '# Preview — this prompt has no variables\n\n';
      panel.textContent = header + (data.resolved || '');
    } catch {
      panel.textContent = 'Preview failed.';
    }
  });

  return wrap;
}

function tplAgentsCard(agent, index) {
  const readOnly = Boolean(tplSelected() && tplSelected().builtin);
  const card = document.createElement('div');
  card.className = `tpl-agent-card${index === 0 ? ' is-entry' : ''}${readOnly ? ' is-readonly' : ''}`;
  card.dataset.index = String(index);
  card.draggable = !readOnly;

  const displayName = tplAgentsDisplayName(agent, index);
  const skills = agent.tools && agent.tools.skills ? agent.tools.skills : [];
  const mcpServers = agent.tools && agent.tools.mcp_servers ? agent.tools.mcp_servers : [];

  const header = document.createElement('div');
  header.className = 'tpl-agent-card-head';

  const handle = document.createElement('span');
  handle.className = 'tpl-agent-drag-handle';
  handle.textContent = 'Drag';
  handle.title = 'Drag to reorder';
  if (!readOnly) header.appendChild(handle);

  const avatar = document.createElement('span');
  avatar.className = 'tpl-agent-avatar';
  avatar.textContent = tplAgentsInitials(displayName, index);
  header.appendChild(avatar);

  const identity = document.createElement('div');
  identity.className = 'tpl-agent-identity';
  const name = document.createElement('div');
  name.className = 'tpl-agent-display-name';
  name.textContent = displayName;
  identity.appendChild(name);
  const subtitle = document.createElement('div');
  subtitle.className = 'tpl-agent-subtitle';
  subtitle.textContent =
    index === 0 ? 'Required workspace front door' : 'Seeded workspace specialist';
  identity.appendChild(subtitle);
  header.appendChild(identity);

  const remove = document.createElement('button');
  remove.type = 'button';
  remove.className = 'tpl-agent-remove modern-btn modern-btn-secondary btn-sm';
  remove.textContent = 'Remove';
  remove.setAttribute('aria-label', `Remove ${displayName}`);
  remove.addEventListener('click', () => tplAgentsRemove(index));
  if (!readOnly) header.appendChild(remove);
  card.appendChild(header);

  const summary = document.createElement('div');
  summary.className = 'tpl-agent-summary';
  summary.appendChild(
    tplAgentsChip(tplAgentsRoleLabel(agent, index), index === 0 ? 'entry' : 'role')
  );
  summary.appendChild(tplAgentsChip(tplAgentsTypeLabel(agent.type), 'type'));
  summary.appendChild(
    tplAgentsChip(agent.model ? agent.model : 'Workspace model', agent.model ? 'model' : 'empty')
  );
  card.appendChild(summary);

  const promptPreview = document.createElement('p');
  promptPreview.className = 'tpl-agent-prompt-preview';
  promptPreview.textContent = agent.system_prompt || 'No system prompt yet.';
  card.appendChild(promptPreview);

  const tools = document.createElement('div');
  tools.className = 'tpl-agent-tools';
  const skillsBlock = document.createElement('div');
  skillsBlock.className = 'tpl-agent-tool-block';
  const skillsLabel = document.createElement('div');
  skillsLabel.className = 'tpl-agent-tool-label';
  skillsLabel.textContent = 'Skills';
  skillsBlock.appendChild(skillsLabel);
  skillsBlock.appendChild(tplAgentsChipList(skills, 'No skills', 'skill'));
  tools.appendChild(skillsBlock);

  const mcpBlock = document.createElement('div');
  mcpBlock.className = 'tpl-agent-tool-block';
  const mcpLabel = document.createElement('div');
  mcpLabel.className = 'tpl-agent-tool-label';
  mcpLabel.textContent = 'MCP';
  mcpBlock.appendChild(mcpLabel);
  mcpBlock.appendChild(tplAgentsChipList(mcpServers, 'No MCP servers', 'mcp'));
  tools.appendChild(mcpBlock);
  card.appendChild(tools);

  if (!readOnly) {
    const details = document.createElement('details');
    details.className = 'tpl-agent-editor';
    details.open = !agent.name;
    const summaryToggle = document.createElement('summary');
    summaryToggle.className = 'tpl-agent-editor-summary';
    summaryToggle.textContent = 'Edit agent';
    details.appendChild(summaryToggle);

    const form = document.createElement('div');
    form.className = 'tpl-agent-form';
    const nameInput = tplAgentsInput(
      'tpl-agent-name',
      agent.name,
      'Agent name or pick an existing agent',
      'tpl-existing-agents'
    );
    const nameField = tplAgentsField('Name', nameInput);
    const reuseHint = document.createElement('div');
    reuseHint.className = 'tpl-agent-reuse-hint';
    reuseHint.textContent =
      'Matches an existing agent — its saved prompt, model, and tools will be reused.';
    const syncReuseHint = () => {
      reuseHint.hidden = !tplAgentsNameMatchesExisting(nameInput.value);
    };
    nameInput.addEventListener('input', syncReuseHint);
    syncReuseHint();
    nameField.appendChild(reuseHint);
    form.appendChild(nameField);

    const rt = document.createElement('div');
    rt.className = 'tpl-agent-form-row';
    rt.appendChild(
      tplAgentsCol('Role', tplAgentsSelect('tpl-agent-role', TPL_AGENT_ROLES, agent.role || ''))
    );
    rt.appendChild(
      tplAgentsCol('Type', tplAgentsSelect('tpl-agent-type', TPL_AGENT_TYPES, agent.type || ''))
    );
    form.appendChild(rt);

    form.appendChild(
      tplAgentsField(
        'Model',
        tplAgentsInput('tpl-agent-model', agent.model, 'Defaults to the workspace model')
      )
    );

    const prompt = document.createElement('textarea');
    prompt.className = 'form-control tpl-agent-prompt';
    prompt.rows = 2;
    prompt.value = agent.system_prompt || '';
    prompt.placeholder =
      "This agent's instructions. Use {{variables}} to weave in workspace context.";
    form.appendChild(tplAgentsField('System prompt', tplAgentsPromptTools(prompt)));

    const sm = document.createElement('div');
    sm.className = 'tpl-agent-form-row';
    const skillsVal = skills.join(', ');
    const mcpVal = mcpServers.join(', ');
    sm.appendChild(
      tplAgentsCol('Skills', tplAgentsInput('tpl-agent-skills', skillsVal, 'comma-separated'))
    );
    sm.appendChild(
      tplAgentsCol('MCP servers', tplAgentsInput('tpl-agent-mcp', mcpVal, 'comma-separated'))
    );
    form.appendChild(sm);
    details.appendChild(form);
    card.appendChild(details);
  }

  card.addEventListener('dragstart', event => {
    if (readOnly) return;
    tplAgents.dragIndex = index;
    card.classList.add('is-dragging');
    if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
  });
  card.addEventListener('dragend', () => {
    card.classList.remove('is-dragging');
  });
  card.addEventListener('dragover', event => {
    if (!readOnly) event.preventDefault();
  });
  card.addEventListener('drop', event => {
    if (readOnly) return;
    event.preventDefault();
    tplAgentsReorder(tplAgents.dragIndex, index);
  });

  return card;
}

function tplAgentsRender(agents) {
  const list = tplEl('tplAgentsList');
  const empty = tplEl('tplAgentsEmpty');
  if (!list) return;
  // Lazily load existing agent names so the roster name field can suggest /
  // flag reuse; re-syncs hints once names arrive.
  tplAgentsEnsureExistingNames().then(() => {
    list.querySelectorAll('.tpl-agent-name').forEach(input => {
      input.dispatchEvent(new Event('input'));
    });
  });
  const items = tplAgentsNormalizeList(agents);
  list.innerHTML = '';
  items.forEach((agent, idx) => list.appendChild(tplAgentsCard(agent, idx)));
  if (empty) empty.hidden = items.length > 0;
  tplApplyReadOnly();
}

function tplAgentsParseNames(value) {
  return String(value || '')
    .split(',')
    .map(s => s.trim())
    .filter(Boolean);
}

function tplAgentsCollect() {
  const list = tplEl('tplAgentsList');
  if (!list) return [];
  return Array.from(list.querySelectorAll('.tpl-agent-card')).map(card => ({
    name: (card.querySelector('.tpl-agent-name')?.value || '').trim(),
    role: card.querySelector('.tpl-agent-role')?.value || '',
    type: card.querySelector('.tpl-agent-type')?.value || '',
    model: (card.querySelector('.tpl-agent-model')?.value || '').trim(),
    system_prompt: (card.querySelector('.tpl-agent-prompt')?.value || '').trim(),
    tools: {
      skills: tplAgentsParseNames(card.querySelector('.tpl-agent-skills')?.value),
      mcp_servers: tplAgentsParseNames(card.querySelector('.tpl-agent-mcp')?.value)
    }
  }));
}

function tplAgentsAdd() {
  const list = tplAgentsCollect();
  list.push(tplAgentsBlank());
  tplAgentsRender(list);
}

function tplAgentsRemove(index) {
  const list = tplAgentsCollect();
  list.splice(index, 1);
  tplAgentsRender(list);
}

function tplAgentsReorder(from, to) {
  tplAgents.dragIndex = -1;
  if (from < 0 || to < 0 || from === to) return;
  const list = tplAgentsCollect();
  if (from >= list.length || to >= list.length) return;
  const [moved] = list.splice(from, 1);
  list.splice(to, 0, moved);
  tplAgentsRender(list);
}

async function tplAgentsSave() {
  const template = tplSelected();
  if (!template) return;
  const agents = tplAgentsCollect().filter(a => a.name);
  if (agents.length === 0) {
    tplToast(
      'A template needs at least one agent — the first is the workspace entry agent.',
      'error'
    );
    return;
  }
  try {
    await tplFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}/agents`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agents })
    });
    tplToast('Agents saved.', 'success');
    tplAgents.templateId = '';
    await tplRefresh(template.id);
    tplAgentsLoad();
  } catch (error) {
    tplToast(error.message || 'Failed to save agents', 'error');
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
  tplEl('tplStarterTaskAddBtn')?.addEventListener('click', () => {
    tplEl('tplStarterTasksList')?.appendChild(tplStarterTaskRow(null));
  });
  tplEl('tplResetBtn')?.addEventListener('click', tplResetOverviewFields);
  tplEl('tplDuplicateToCustomizeBtn')?.addEventListener('click', tplDuplicate);

  const search = tplEl('tplSearch');
  if (search) {
    search.addEventListener('input', () => {
      tplState.search = search.value;
      tplRenderList();
    });
  }

  tplEl('tplNameModalConfirm')?.addEventListener('click', () => void tplRunNameAction());
  tplEl('tplNameModalInput')?.addEventListener('keydown', event => {
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
    filesTab.addEventListener('hide.bs.tab', event => {
      if (!tplFiles.dirty) return;
      const target = event.relatedTarget;
      event.preventDefault();
      tplGuardDirty(() => {
        if (target) bootstrap.Tab.getOrCreateInstance(target).show();
      });
    });
  }

  // Tools tab wiring.
  tplEl('tplTabTools')?.addEventListener('shown.bs.tab', () => tplToolsEnsure());
  tplEl('tplToolsSaveBtn')?.addEventListener('click', () => void tplToolsSave());

  // Agents tab wiring.
  tplEl('tplTabAgents')?.addEventListener('shown.bs.tab', () => tplAgentsLoad());
  tplEl('tplAgentsAddBtn')?.addEventListener('click', tplAgentsAdd);
  tplEl('tplAgentsSaveBtn')?.addEventListener('click', () => void tplAgentsSave());
  tplEl('tplAgentsDuplicateBtn')?.addEventListener('click', tplDuplicate);

  void tplRefresh();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', tplInit);
} else {
  tplInit();
}
