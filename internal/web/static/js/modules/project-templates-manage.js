/**
 * Project Templates management module.
 *
 * Owns the "Project Templates" manage modal (list / import / edit metadata /
 * delete / reveal) and, when present on the page, the Settings "Project
 * Templates" section (templates_root configuration). The library itself is
 * disk-first: a template is a folder, so every action here is a thin veneer
 * over the filesystem.
 */

const projectTemplatesManageState = {
  templates: [],
  root: '',
  onChanged: null,
  editingId: ''
};

function ptmToast(message, kind) {
  if (typeof window.showToast === 'function') {
    window.showToast(message, kind || 'info');
  } else if (kind === 'error') {
    alert(message);
  }
}

async function ptmFetchJSON(url, options) {
  const response = await fetch(url, options);
  const result = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(result.error || `Request failed (${response.status})`);
  }
  return result;
}

function ptmNotifyChanged() {
  if (typeof projectTemplatesManageState.onChanged === 'function') {
    try {
      projectTemplatesManageState.onChanged();
    } catch (error) {
      console.warn('Project templates onChanged callback failed:', error);
    }
  }
}

async function ptmRefresh() {
  const list = document.getElementById('ptmList');
  const empty = document.getElementById('ptmEmpty');
  const rootPath = document.getElementById('ptmRootPath');
  if (!list) return;

  try {
    const data = await ptmFetchJSON('/api/project-templates');
    projectTemplatesManageState.templates = Array.isArray(data.templates) ? data.templates : [];
    projectTemplatesManageState.root = data.templates_root || '';
  } catch (error) {
    console.error('Failed to load project templates:', error);
    ptmToast('Could not load the template library.', 'error');
    projectTemplatesManageState.templates = [];
  }

  if (rootPath) rootPath.textContent = projectTemplatesManageState.root;
  if (empty) empty.hidden = projectTemplatesManageState.templates.length > 0;
  ptmRenderList();
}

function ptmRenderList() {
  const list = document.getElementById('ptmList');
  if (!list) return;
  list.innerHTML = '';
  for (const template of projectTemplatesManageState.templates) {
    list.appendChild(
      projectTemplatesManageState.editingId === template.id
        ? ptmBuildEditRow(template)
        : ptmBuildRow(template)
    );
  }
}

function ptmRowShell() {
  const row = document.createElement('div');
  row.className = 'modern-card p-2 d-flex flex-column flex-sm-row align-items-sm-center gap-2';
  row.setAttribute('role', 'listitem');
  return row;
}

function ptmBuildRow(template) {
  const row = ptmRowShell();

  const info = document.createElement('div');
  info.className = 'flex-grow-1';
  info.style.minWidth = '0';

  const title = document.createElement('div');
  title.style.color = 'var(--text-primary)';
  title.style.fontSize = '14px';
  const nameSpan = document.createElement('strong');
  nameSpan.textContent = template.name || template.id;
  title.appendChild(nameSpan);
  const idSpan = document.createElement('span');
  idSpan.textContent = ` ${template.id}`;
  idSpan.style.cssText = 'font-size: 11px; color: var(--text-secondary); font-family: var(--font-mono, monospace);';
  title.appendChild(idSpan);
  info.appendChild(title);

  if (template.description) {
    const desc = document.createElement('div');
    desc.textContent = template.description;
    desc.style.cssText = 'font-size: 12px; color: var(--text-secondary);';
    info.appendChild(desc);
  }
  row.appendChild(info);

  const actions = document.createElement('div');
  actions.className = 'd-flex gap-2';
  actions.style.whiteSpace = 'nowrap';
  actions.appendChild(ptmActionButton('Reveal', () => ptmReveal(template.id)));
  actions.appendChild(ptmActionButton('Edit', () => {
    projectTemplatesManageState.editingId = template.id;
    ptmRenderList();
  }));
  actions.appendChild(ptmActionButton('Delete', () => ptmDelete(template), true));
  row.appendChild(actions);
  return row;
}

function ptmBuildEditRow(template) {
  const row = ptmRowShell();

  const form = document.createElement('div');
  form.className = 'flex-grow-1 d-flex flex-column gap-1';
  const nameInput = document.createElement('input');
  nameInput.className = 'modern-input w-100';
  nameInput.placeholder = `Display name (defaults to ${template.id})`;
  nameInput.value = template.name === template.id ? '' : (template.name || '');
  const descInput = document.createElement('input');
  descInput.className = 'modern-input w-100';
  descInput.placeholder = 'Description';
  descInput.value = template.description || '';
  form.appendChild(nameInput);
  form.appendChild(descInput);
  row.appendChild(form);

  const actions = document.createElement('div');
  actions.className = 'd-flex gap-2';
  actions.style.whiteSpace = 'nowrap';
  actions.appendChild(ptmActionButton('Save', async () => {
    try {
      await ptmFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: nameInput.value.trim(), description: descInput.value.trim() })
      });
      projectTemplatesManageState.editingId = '';
      await ptmRefresh();
      ptmNotifyChanged();
    } catch (error) {
      ptmToast(error.message || 'Failed to update template', 'error');
    }
  }));
  actions.appendChild(ptmActionButton('Cancel', () => {
    projectTemplatesManageState.editingId = '';
    ptmRenderList();
  }));
  row.appendChild(actions);
  return row;
}

function ptmActionButton(label, onClick, danger) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'modern-btn modern-btn-secondary';
  button.style.fontSize = '12px';
  if (danger) button.style.color = '#ef4444';
  button.textContent = label;
  button.addEventListener('click', onClick);
  return button;
}

async function ptmReveal(id) {
  try {
    await ptmFetchJSON('/api/project-templates/reveal', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: id || '' })
    });
  } catch (error) {
    ptmToast(error.message || 'Failed to open folder', 'error');
  }
}

async function ptmDelete(template) {
  const label = template.name || template.id;
  if (!window.confirm(`Delete the template "${label}"? It will be moved to the Trash when possible.`)) {
    return;
  }
  try {
    const result = await ptmFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, { method: 'DELETE' });
    ptmToast(result.trashed ? `"${label}" moved to Trash.` : `"${label}" deleted.`, 'success');
    await ptmRefresh();
    ptmNotifyChanged();
  } catch (error) {
    ptmToast(error.message || 'Failed to delete template', 'error');
  }
}

async function ptmImport() {
  try {
    const picked = await ptmFetchJSON('/api/folder-picker/select-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Select a Folder to Import as a Template' })
    });
    if (!picked.selected || !picked.path) return;

    const result = await ptmFetchJSON('/api/project-templates/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: picked.path })
    });
    const imported = result.template || {};
    ptmToast(`Imported "${imported.name || imported.id || 'template'}".`, 'success');
    await ptmRefresh();
    ptmNotifyChanged();
  } catch (error) {
    ptmToast(error.message || 'Failed to import folder', 'error');
  }
}

function ptmOpenModal(options) {
  projectTemplatesManageState.onChanged = options && typeof options.onChanged === 'function' ? options.onChanged : null;
  projectTemplatesManageState.editingId = '';
  const modalElement = document.getElementById('projectTemplatesManageModal');
  if (!modalElement) {
    console.warn('Project templates manage modal markup is missing on this page.');
    return;
  }
  void ptmRefresh();
  new bootstrap.Modal(modalElement).show();

  // This modal regularly opens on top of another one (the Create Workspace
  // modal, the workspace project dialog). Bootstrap gives every modal the
  // same z-index, so lift this one — and its backdrop — above the stack.
  requestAnimationFrame(() => {
    const openModals = document.querySelectorAll('.modal.show');
    if (openModals.length <= 1) return;
    const lift = 1055 + openModals.length * 10;
    modalElement.style.zIndex = String(lift);
    const backdrops = document.querySelectorAll('.modal-backdrop');
    const lastBackdrop = backdrops[backdrops.length - 1];
    if (lastBackdrop) lastBackdrop.style.zIndex = String(lift - 5);
  });
}

function ptmRestoreStackingOnClose() {
  const modalElement = document.getElementById('projectTemplatesManageModal');
  if (!modalElement) return;
  modalElement.addEventListener('hidden.bs.modal', () => {
    modalElement.style.zIndex = '';
    // Bootstrap drops body.modal-open when any modal hides; restore it while
    // an underlying modal (e.g. Create Workspace) is still showing.
    if (document.querySelector('.modal.show')) {
      document.body.classList.add('modal-open');
    }
  });
}

// --- Settings page section (present only on /settings) ---

async function ptmSettingsLoad() {
  const statusText = document.getElementById('templatesRootStatusText');
  const statusDetails = document.getElementById('templatesRootStatusDetails');
  const input = document.getElementById('templatesRootInput');
  if (!input) return;

  try {
    const state = await ptmFetchJSON('/api/settings/templates-root');
    input.value = state.templates_root || '';
    if (statusText) {
      const sourceLabel = { settings: 'custom directory', environment: 'environment variable', default: 'built-in default' }[state.source] || state.source;
      statusText.textContent = `Using ${sourceLabel}`;
    }
    if (statusDetails) statusDetails.textContent = state.effective_templates_root || '';
  } catch (error) {
    if (statusText) statusText.textContent = 'Failed to load templates directory';
    console.error('Failed to load templates root:', error);
  }
}

async function ptmSettingsSave(value) {
  try {
    await ptmFetchJSON('/api/settings/templates-root', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ templates_root: value })
    });
    ptmToast('Templates directory saved.', 'success');
    await ptmSettingsLoad();
  } catch (error) {
    ptmToast(error.message || 'Failed to save templates directory', 'error');
  }
}

// --- Create-workspace "Project (optional)" card ---
// The markup ships in create-workspace-modal.tmpl, which appears on several
// pages driven by different modules (sessions.js on the live hub/home,
// workspace-create.js on the legacy hub). The card binds its own behavior
// here so it works wherever the markup exists; host modules only merge
// getPayloadFields() into their create payload.

function ptcElements() {
  return {
    card: document.getElementById('projectTemplateCard'),
    select: document.getElementById('projectTemplateSelect'),
    description: document.getElementById('projectTemplateDescription'),
    emptyHint: document.getElementById('projectTemplateEmptyHint'),
    pathInput: document.getElementById('projectTemplatePathInput'),
    nameRow: document.getElementById('projectNameRow'),
    nameInput: document.getElementById('projectNameInput'),
    browseBtn: document.getElementById('projectTemplateBrowseBtn'),
    manageLink: document.getElementById('projectTemplateManageLink'),
    importToggle: document.getElementById('folderImportToggle')
  };
}

function ptcGetPayloadFields() {
  const els = ptcElements();
  const fields = {};
  const templateId = els.select?.value?.trim() || '';
  const templatePath = els.pathInput?.value?.trim() || '';
  if (templateId) {
    fields.template_id = templateId;
  } else if (templatePath) {
    fields.template_path = templatePath;
  }
  const projectName = els.nameInput?.value?.trim() || '';
  if ((fields.template_id || fields.template_path) && projectName) {
    fields.project_name = projectName;
  }
  return fields;
}

function ptcUpdateUI() {
  const els = ptcElements();
  if (els.description) {
    const selected = els.select?.selectedOptions?.[0];
    const text = selected?.dataset?.description || '';
    els.description.textContent = text;
    els.description.hidden = !text;
  }
  if (els.nameRow) {
    const active = Boolean(els.select?.value?.trim() || els.pathInput?.value?.trim());
    els.nameRow.hidden = !active;
  }
}

function ptcReset() {
  const els = ptcElements();
  if (els.select) els.select.value = '';
  if (els.pathInput) els.pathInput.value = '';
  if (els.nameInput) els.nameInput.value = '';
  ptcUpdateUI();
}

function ptcSyncImportVisibility() {
  const els = ptcElements();
  if (!els.card) return;
  // Project templates scaffold a new project; they don't apply when
  // importing an existing folder as the workspace.
  const importMode = Boolean(els.importToggle?.checked);
  els.card.hidden = importMode;
}

async function ptcPopulate() {
  const els = ptcElements();
  if (!els.select) return;

  els.select.innerHTML = '<option value="" selected>None</option>';
  if (els.emptyHint) els.emptyHint.hidden = true;

  try {
    const data = await ptmFetchJSON('/api/project-templates');
    const templates = Array.isArray(data.templates) ? data.templates : [];
    for (const template of templates) {
      if (!template || !template.id) continue;
      const option = document.createElement('option');
      option.value = template.id;
      option.textContent = template.name || template.id;
      option.dataset.description = template.description || '';
      els.select.appendChild(option);
    }
    if (templates.length === 0 && els.emptyHint) {
      els.emptyHint.textContent = data.templates_root
        ? `No templates yet. Drop a template folder into ${data.templates_root} to add one, or use any folder below.`
        : 'No templates yet. Use any folder below as a template.';
      els.emptyHint.hidden = false;
    }
  } catch (error) {
    console.error('Failed to load project templates:', error);
    if (els.emptyHint) {
      els.emptyHint.textContent = 'Could not load the template library. You can still use any folder below as a template.';
      els.emptyHint.hidden = false;
    }
  }
  ptcUpdateUI();
}

async function ptcBrowse() {
  const els = ptcElements();
  if (!els.pathInput) return;
  if (els.browseBtn) els.browseBtn.disabled = true;
  try {
    const picked = await ptmFetchJSON('/api/folder-picker/select-path', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: 'Select a Template Folder' })
    });
    if (picked.selected && picked.path) {
      els.pathInput.value = picked.path;
      if (els.select) els.select.value = '';
      ptcUpdateUI();
    }
  } catch (error) {
    ptmToast(error.message || 'Failed to open folder picker', 'error');
  } finally {
    if (els.browseBtn) els.browseBtn.disabled = false;
  }
}

function ptcInit() {
  const els = ptcElements();
  if (!els.card) return;

  if (els.select) {
    els.select.addEventListener('change', () => {
      // Library pick and ad-hoc folder are mutually exclusive.
      if (els.select.value && els.pathInput) els.pathInput.value = '';
      ptcUpdateUI();
    });
  }
  if (els.pathInput) {
    els.pathInput.addEventListener('input', () => {
      if (els.pathInput.value.trim() && els.select) els.select.value = '';
      ptcUpdateUI();
    });
  }
  if (els.browseBtn) {
    els.browseBtn.addEventListener('click', () => void ptcBrowse());
  }
  if (els.manageLink) {
    els.manageLink.addEventListener('click', () => {
      ptmOpenModal({ onChanged: () => void ptcPopulate() });
    });
  }
  if (els.importToggle) {
    els.importToggle.addEventListener('change', ptcSyncImportVisibility);
  }

  // Reset and (re)load the library every time the create modal opens,
  // whichever module opened it. Runs after the host module's own show
  // handler, so import-mode state is already settled.
  const createModal = document.getElementById('addFolderModal');
  if (createModal) {
    createModal.addEventListener('show.bs.modal', () => {
      ptcReset();
      ptcSyncImportVisibility();
      void ptcPopulate();
    });
  }
  ptcSyncImportVisibility();
}

window.ProjectTemplateCard = {
  populate: ptcPopulate,
  reset: ptcReset,
  getPayloadFields: ptcGetPayloadFields
};

function ptmInitListeners() {
  ptcInit();
  ptmRestoreStackingOnClose();
  const openRootBtn = document.getElementById('ptmOpenRootBtn');
  if (openRootBtn) openRootBtn.addEventListener('click', () => void ptmReveal(''));
  const importBtn = document.getElementById('ptmImportBtn');
  if (importBtn) importBtn.addEventListener('click', () => void ptmImport());

  // Workspace-detail project modal: refresh its picker after library changes.
  const detailManageBtn = document.getElementById('workspace-detail-project-template-manage');
  if (detailManageBtn) {
    detailManageBtn.addEventListener('click', () => {
      ptmOpenModal({
        onChanged: () => document.getElementById('workspace-detail-project-template-refresh')?.click()
      });
    });
  }

  // Settings page section.
  const settingsInput = document.getElementById('templatesRootInput');
  if (settingsInput) {
    void ptmSettingsLoad();
    document.getElementById('saveTemplatesRootBtn')?.addEventListener('click', () => {
      void ptmSettingsSave(settingsInput.value.trim());
    });
    document.getElementById('resetTemplatesRootBtn')?.addEventListener('click', () => {
      settingsInput.value = '';
      void ptmSettingsSave('');
    });
    document.getElementById('browseTemplatesRootBtn')?.addEventListener('click', async () => {
      try {
        const picked = await ptmFetchJSON('/api/folder-picker/select-path', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ title: 'Select Project Templates Directory' })
        });
        if (picked.selected && picked.path) settingsInput.value = picked.path;
      } catch (error) {
        ptmToast(error.message || 'Failed to open folder picker', 'error');
      }
    });
    document.getElementById('openTemplatesRootBtn')?.addEventListener('click', () => void ptmReveal(''));
    document.getElementById('manageTemplatesBtn')?.addEventListener('click', () => {
      ptmOpenModal({ onChanged: null });
    });
  }
}

window.ProjectTemplatesManage = { open: ptmOpenModal, refresh: ptmRefresh };

document.addEventListener('DOMContentLoaded', ptmInitListeners);
