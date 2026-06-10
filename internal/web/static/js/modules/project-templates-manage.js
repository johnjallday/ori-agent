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

function ptmInitListeners() {
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
