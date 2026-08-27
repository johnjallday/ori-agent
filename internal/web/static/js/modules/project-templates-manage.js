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
  idSpan.style.cssText =
    'font-size: 11px; color: var(--text-secondary); font-family: var(--font-mono, monospace);';
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
  actions.appendChild(
    ptmActionButton('Edit', () => {
      projectTemplatesManageState.editingId = template.id;
      ptmRenderList();
    })
  );
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
  nameInput.value = template.name === template.id ? '' : template.name || '';
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
  actions.appendChild(
    ptmActionButton('Save', async () => {
      try {
        await ptmFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: nameInput.value.trim(),
            description: descInput.value.trim()
          })
        });
        projectTemplatesManageState.editingId = '';
        await ptmRefresh();
        ptmNotifyChanged();
      } catch (error) {
        ptmToast(error.message || 'Failed to update template', 'error');
      }
    })
  );
  actions.appendChild(
    ptmActionButton('Cancel', () => {
      projectTemplatesManageState.editingId = '';
      ptmRenderList();
    })
  );
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
  if (
    !window.confirm(`Delete the template "${label}"? It will be moved to the Trash when possible.`)
  ) {
    return;
  }
  try {
    const result = await ptmFetchJSON(`/api/project-templates/${encodeURIComponent(template.id)}`, {
      method: 'DELETE'
    });
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
  projectTemplatesManageState.onChanged =
    options && typeof options.onChanged === 'function' ? options.onChanged : null;
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
      const sourceLabel =
        {
          settings: 'custom directory',
          environment: 'environment variable',
          default: 'built-in default'
        }[state.source] || state.source;
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

// --- Create-workspace unified Template picker ---
// The markup ships in create-workspace-modal.tmpl (live on sessions.js). The
// picker renders built-in templates as a card grid plus user-authored
// templates as a compact list, all one selection. Host modules merge
// getPayloadFields() into their create payload, read getSelectedTemplate() for
// name/description prefill + starter-task seeding, and listen for the
// 'workspace-template-selected' event dispatched on #addFolderModal to apply
// the behavior default.

// The synthetic "Blank" entry maps to no template (no template_id submitted).
//
// Its readiness is stated explicitly rather than left absent: Blank depends on
// nothing, so no catalog failure, missing plugin, or unreadable manifest may
// ever make it unavailable. It is the escape hatch the whole picker falls back
// to.
const PTC_BLANK = {
  id: '',
  name: '',
  description: '',
  tagline: 'Start from an empty workspace.',
  icon: '✍',
  label: 'Blank',
  behavior_profile: 'general',
  starter_tasks: [],
  has_skeleton: false,
  blank: true,
  readiness: { state: 'ready', ownership: 'builtin', reason: '' }
};

// A monotonically increasing token for catalog loads. A response that arrives
// after a newer request started is discarded rather than painted, so a slow
// first load cannot overwrite the state a completed recovery just produced.
let ptcCatalogGeneration = 0;

function ptcReadiness(template) {
  return window.BlueprintReadiness?.normalize(template?.readiness) || null;
}

function ptcIsBlocked(template) {
  const readiness = ptcReadiness(template);
  return Boolean(readiness && readiness.state !== 'ready');
}

let ptcSelected = PTC_BLANK;

function ptcElements() {
  return {
    picker: document.getElementById('templatePicker'),
    grid: document.getElementById('templateBuiltinGrid'),
    userSection: document.getElementById('templateUserSection'),
    userList: document.getElementById('templateUserList'),
    description: document.getElementById('projectTemplateDescription'),
    emptyHint: document.getElementById('projectTemplateEmptyHint'),
    pathInput: document.getElementById('projectTemplatePathInput'),
    browseBtn: document.getElementById('projectTemplateBrowseBtn'),
    manageLink: document.getElementById('projectTemplateManageLink'),
    importToggle: document.getElementById('folderImportToggle'),
    openAfterCreate: document.getElementById('projectTemplateOpenAfterCreate'),
    openAfterCreateToggle: document.getElementById('projectTemplateOpenAfterCreateToggle'),
    readinessPanel: document.getElementById('templateBriefingReadiness'),
    readinessLive: document.getElementById('blueprintReadinessLive'),
    // Briefing panel: the selected template's description + a "deploys" readout.
    blueprintHeader: document.getElementById('workspaceCreateBlueprintHeader'),
    briefingHeader: document.getElementById('workspaceCreateBriefingHeader'),
    briefing: document.getElementById('templateBriefing'),
    briefingDefault: document.getElementById('templateBriefingDefault'),
    briefingDeploys: document.getElementById('templateBriefingDeploys'),
    briefingAgentsRow: document.getElementById('templateBriefingAgentsRow'),
    briefingAgentsValue: document.getElementById('templateBriefingAgentsValue'),
    briefingNoCommanderNudge: document.getElementById('templateBriefingNoCommanderNudge'),
    briefingScaffoldRow: document.getElementById('templateBriefingScaffoldRow'),
    briefingScaffoldValue: document.getElementById('templateBriefingScaffoldValue'),
    briefingAddonsRow: document.getElementById('templateBriefingAddonsRow'),
    briefingAddonsList: document.getElementById('templateBriefingAddonsList')
  };
}

// ptcTagline returns the one-line summary for a template's picker card:
// the explicit tagline, else the first sentence of the description
// (truncated), else empty.
function ptcTagline(template) {
  if (!template) return '';
  const tagline = String(template.tagline || '').trim();
  if (tagline) return tagline;
  const desc = String(template.description || '').trim();
  if (!desc) return '';
  // First sentence (up to a period+space), capped so cards stay uniform.
  const match = desc.match(/^.*?[.!?](?:\s|$)/);
  let out = (match ? match[0] : desc).trim();
  const MAX = 80;
  if (out.length > MAX) out = out.slice(0, MAX - 1).trimEnd() + '…';
  return out;
}

function ptcGetPayloadFields() {
  const els = ptcElements();
  const fields = {};
  const templatePath = els.pathInput?.value?.trim() || '';
  const templateId = ptcSelected && !ptcSelected.blank ? String(ptcSelected.id || '').trim() : '';
  if (templatePath) {
    // The ad-hoc folder (Advanced) overrides the picked template.
    fields.template_path = templatePath;
  } else if (templateId) {
    fields.template_id = templateId;
  }
  return fields;
}

// getSelectedTemplate returns the picked template (or the Blank sentinel) so the
// host can prefill name/description, apply the behavior default, and seed the
// template's starter tasks after creation.
function ptcGetSelectedTemplate() {
  return ptcSelected;
}

function ptcEmitSelection() {
  const modal = document.getElementById('addFolderModal');
  modal?.dispatchEvent(
    new CustomEvent('workspace-template-selected', {
      detail: { template: ptcSelected }
    })
  );
}

// Double-click on a blueprint: select it, then ask the wizard to advance to the
// Construct step (the host listens for this on #addFolderModal).
function ptcEmitAdvance() {
  document
    .getElementById('addFolderModal')
    ?.dispatchEvent(new CustomEvent('workspace-template-advance'));
}

// Every selectable blueprint control, grid then list, in the order a user
// arrows through them.
function ptcOptions() {
  const els = ptcElements();
  return [els.grid, els.userList]
    .filter(Boolean)
    .flatMap(container =>
      Array.from(container.querySelectorAll('.workspace-template-card, .workspace-template-row'))
    );
}

// A radiogroup has one tab stop, and arrow keys move within it. Without the
// roving tabindex the grid announces itself as a radiogroup and then behaves
// like a list of buttons — a promise the keyboard does not keep.
function ptcMarkSelectedAcross(selectedEl) {
  const options = ptcOptions();
  let selected = null;
  options.forEach(el => {
    const on = el === selectedEl;
    el.classList.toggle('is-selected', on);
    el.setAttribute('aria-checked', on ? 'true' : 'false');
    if (on) selected = el;
  });
  const stop = selected || options[0] || null;
  options.forEach(el => {
    el.tabIndex = el === stop ? 0 : -1;
  });
}

// Arrow keys move the selection; Home/End jump to the ends. Selection follows
// focus, which is the expected radiogroup behavior — and safe here because
// selecting an unavailable blueprint only explains it, never acts on it.
function ptcHandleOptionKeydown(event) {
  const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'Home', 'End'];
  if (!keys.includes(event.key)) return;
  const options = ptcOptions();
  const current = options.indexOf(event.currentTarget);
  if (current < 0 || options.length === 0) return;
  event.preventDefault();

  let next = current;
  if (event.key === 'Home') next = 0;
  else if (event.key === 'End') next = options.length - 1;
  else if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
    next = (current + 1) % options.length;
  } else {
    next = (current - 1 + options.length) % options.length;
  }

  const target = options[next];
  if (!target) return;
  target.focus();
  target.click();
}

function ptcSelect(template, cardEl) {
  ptcSelected = template || PTC_BLANK;
  const els = ptcElements();
  // A refusal message belongs to the attempt that produced it. Choosing a
  // different blueprint ends that attempt, so the announcement is cleared
  // rather than left to be re-read as advice about a card nobody is on. A
  // pending trust confirmation ends with it — consent given for one blueprint
  // must never be applied to another.
  ptcClearAnnouncement();
  ptcSyncRecoveryToSelection();
  ptcMarkSelectedAcross(cardEl);
  // Picking a library template clears the ad-hoc folder (mutually exclusive);
  // selecting Blank keeps a typed path so it can act as the override.
  if (els.pathInput && !ptcSelected.blank) els.pathInput.value = '';
  ptcUpdateUI();
  ptcEmitSelection();
}

// Attaches the shared behavior every blueprint control has, whichever shape it
// takes: a readiness badge, an accessible description of its state, selection,
// keyboard navigation, and the double-click shortcut.
//
// Double-click advances only from a ready blueprint. An unavailable one stays
// selectable — the user needs to be able to read why — but the shortcut must
// not carry them past a blocker they have not seen.
function ptcDecorateOption(element, template, kind) {
  const readiness = ptcReadiness(template);
  element.dataset.templateId = template.id || '';
  if (readiness) element.dataset.readinessState = readiness.state;

  const badge = window.BlueprintReadiness?.renderBadge(readiness);
  if (badge) {
    badge.classList.add(`workspace-template-${kind}-readiness`);
    element.append(badge);
  }

  const description = window.BlueprintReadiness?.describe(readiness) || '';
  if (description) {
    // Described-by rather than appended text: the visible badge already says
    // "Setup required"; the description adds the why for a screen reader
    // without repeating the label on screen.
    const help = document.createElement('span');
    help.className = 'visually-hidden';
    help.id = `blueprintReadinessDesc-${kind}-${(template.id || 'blank').replace(/[^\w:-]/g, '')}`;
    help.textContent = description;
    element.append(help);
    element.setAttribute('aria-describedby', help.id);
  } else {
    element.removeAttribute('aria-describedby');
  }

  element.addEventListener('click', () => ptcSelect(template, element));
  element.addEventListener('keydown', ptcHandleOptionKeydown);
  element.addEventListener('dblclick', () => {
    ptcSelect(template, element);
    if (ptcIsBlocked(template)) {
      ptcAnnounceBlocked(template);
      ptcFocusReadinessPanel();
      return;
    }
    ptcEmitAdvance();
  });
}

function ptcCard(template) {
  const card = document.createElement('button');
  card.type = 'button';
  card.className = 'workspace-template-card';
  card.setAttribute('role', 'radio');
  card.setAttribute('aria-checked', 'false');
  card.tabIndex = -1;
  const icon = document.createElement('span');
  icon.className = 'workspace-template-card-icon';
  icon.setAttribute('aria-hidden', 'true');
  icon.textContent = template.icon || '📁';
  const label = document.createElement('span');
  label.className = 'workspace-template-card-label';
  label.textContent = template.label || template.name || template.id;
  card.append(icon, label);
  const taglineText = ptcTagline(template);
  if (taglineText) {
    const tagline = document.createElement('span');
    tagline.className = 'workspace-template-card-desc';
    tagline.textContent = taglineText;
    card.append(tagline);
  }
  ptcDecorateOption(card, template, 'card');
  return card;
}

function ptcRow(template) {
  const row = document.createElement('button');
  row.type = 'button';
  row.className = 'workspace-template-row';
  row.setAttribute('role', 'radio');
  row.setAttribute('aria-checked', 'false');
  row.tabIndex = -1;
  const icon = document.createElement('span');
  icon.className = 'workspace-template-row-icon';
  icon.setAttribute('aria-hidden', 'true');
  icon.textContent = template.icon || '📁';
  const label = document.createElement('span');
  label.className = 'workspace-template-row-label';
  label.textContent = template.name || template.id;
  row.append(icon, label);
  ptcDecorateOption(row, template, 'row');
  return row;
}

function ptcUpdateUI() {
  const els = ptcElements();
  const templatePath = els.pathInput?.value?.trim() || '';
  const importMode = Boolean(els.importToggle?.checked);

  ptcRenderBriefing(els, importMode, templatePath);

  const entry = ptcSelected?.project_entry;
  const hasEntry = Boolean(
    !importMode &&
    !templatePath &&
    ptcSelected &&
    !ptcSelected.blank &&
    entry &&
    typeof entry === 'object' &&
    typeof entry.relative_path === 'string' &&
    entry.relative_path.trim()
  );
  if (els.openAfterCreate) els.openAfterCreate.hidden = !hasEntry;
  if (els.openAfterCreateToggle) {
    els.openAfterCreateToggle.checked = hasEntry ? Boolean(entry.open_after_create_default) : false;
  }
}

// ptcRenderBriefing fills the Briefing panel from the current selection: the
// full description plus a "deploys" readout (agents, project folder, add-on
// chips). When nothing meaningful is selected — Blank, an ad-hoc folder path,
// or import mode — it falls back to the default helper text. Each deploys row
// is hidden individually when the template declares nothing for it.
function ptcRenderBriefing(els, importMode, templatePath) {
  if (!els.briefing) return;

  const template = ptcSelected;
  // A meaningful briefing needs a real, non-Blank template with no ad-hoc
  // folder override and outside import mode (which hides the picker entirely).
  const showBriefing = Boolean(
    !importMode && !templatePath && template && !template.blank && template.id
  );

  const description = showBriefing ? String(template.description || '').trim() : '';
  if (els.description) {
    els.description.textContent = description;
    els.description.hidden = !description;
  }
  if (els.briefingDefault) els.briefingDefault.hidden = showBriefing;

  const agentSpecs = showBriefing && Array.isArray(template.agents) ? template.agents : [];
  const briefingAgents = agentSpecs
    .map(a => ({
      name: String(a?.name || '').trim(),
      role: String(a?.role || '')
        .trim()
        .toLowerCase()
    }))
    .filter(a => a.name);
  const showAgents = briefingAgents.length > 0;
  if (els.briefingAgentsRow) els.briefingAgentsRow.hidden = !showAgents;
  if (els.briefingAgentsValue) {
    els.briefingAgentsValue.innerHTML = '';
    // One chip per agent: a role-hued dot + name. The first roster entry is the
    // workspace Commander (solid dot, brighter chip); the rest are specialists.
    briefingAgents.forEach((agent, index) => {
      const isEntry = index === 0;
      const chip = document.createElement('span');
      chip.className = 'workspace-template-briefing-agent-chip';
      if (isEntry) chip.classList.add('is-entry');
      if (agent.role) chip.dataset.role = agent.role;
      // Commander-slot label (PRD FR21/FR22): "Commander" when this agent's
      // own role is orchestrator, "Acting Commander" otherwise.
      const commanderLabel =
        String(agent.role || '')
          .trim()
          .toLowerCase() === 'orchestrator'
          ? 'Commander'
          : 'Acting Commander';
      const roleLabel = isEntry
        ? `${commanderLabel}${agent.role ? ` · ${agent.role}` : ''}`
        : agent.role;
      if (roleLabel) chip.title = `${agent.name} — ${roleLabel}`;
      const dot = document.createElement('span');
      dot.className = 'workspace-template-briefing-agent-dot';
      dot.setAttribute('aria-hidden', 'true');
      chip.appendChild(dot);
      chip.appendChild(document.createTextNode(agent.name));
      els.briefingAgentsValue.appendChild(chip);
    });
  }

  // No-Commander nudge (PRD FR23): non-blocking — the blueprint remains
  // selectable and creatable either way; this only informs the choice.
  if (els.briefingNoCommanderNudge) {
    const needsNudge =
      briefingAgents.length >= 3 && !briefingAgents.some(a => a.role === 'orchestrator');
    els.briefingNoCommanderNudge.hidden = !needsNudge;
  }

  const showScaffold = Boolean(showBriefing && template.has_skeleton);
  if (els.briefingScaffoldRow) els.briefingScaffoldRow.hidden = !showScaffold;
  if (els.briefingScaffoldValue && showScaffold) {
    els.briefingScaffoldValue.textContent = 'Scaffolds a starter project folder';
  }

  const addons =
    showBriefing && Array.isArray(template.addons)
      ? template.addons.map(a => String(a || '').trim()).filter(Boolean)
      : [];
  const showAddons = addons.length > 0;
  if (els.briefingAddonsRow) els.briefingAddonsRow.hidden = !showAddons;
  if (els.briefingAddonsList) {
    els.briefingAddonsList.innerHTML = '';
    if (showAddons) {
      for (const addon of addons) {
        const chip = document.createElement('span');
        chip.className = 'workspace-template-briefing-chip';
        chip.textContent = addon;
        els.briefingAddonsList.appendChild(chip);
      }
    }
  }

  if (els.briefingDeploys) {
    els.briefingDeploys.hidden = !(showAgents || showScaffold || showAddons);
  }

  ptcRenderReadiness(els, showBriefing ? template : null);
}

// ptcRenderReadiness paints the selected blueprint's state into the briefing.
// A ready blueprint (and Blank, and an ad-hoc folder path, and import mode)
// leaves the panel empty: there is nothing to fix and nothing to say.
function ptcRenderReadiness(els, template) {
  const host = els.readinessPanel;
  if (!host) return;
  host.textContent = '';
  const readiness = template ? ptcReadiness(template) : null;
  const panel = readiness
    ? window.BlueprintReadiness?.renderPanel(readiness, {
        blueprintName: template?.name || template?.id || '',
        onAction: action => ptcHandleReadinessAction(action, template, readiness)
      })
    : null;
  if (panel) host.appendChild(panel);
}

// ptcHandleReadinessAction dispatches one allowlisted recovery action.
//
// Routing actions are handled here. The three that change the machine —
// install, enable, review update — go through the confirm-gated flow below,
// which discloses first and acts only after the user says so.
function ptcHandleReadinessAction(action, template, readiness) {
  const handler = ptcReadinessActionHandlers[action];
  if (typeof handler === 'function') {
    handler(template, readiness);
    return;
  }
  if (PTC_LIFECYCLE_ACTIONS.has(action)) {
    void ptcStartRecovery(action, template, readiness);
    return;
  }
  ptcOpenPluginsPage();
}

function ptcOpenPluginsPage() {
  window.open('/plugins', '_blank', 'noopener');
}

const PTC_LIFECYCLE_ACTIONS = new Set(['install_plugin', 'enable_plugin', 'review_plugin_update']);

const ptcReadinessActionHandlers = {
  change_blueprint: () => {
    ptcSelect(PTC_BLANK, ptcBlankCard());
    ptcBlankCard()?.focus();
  },
  manage_plugins: () => ptcOpenPluginsPage(),
  edit_template_manifest: template => {
    // Reveals the template's own folder so the author can open its
    // template.json. Only ever offered for a template the user owns.
    if (template?.id) void ptmReveal(template.id);
  },
  retry: () => void ptcPopulate({ preserveSelection: true })
};

// ---- in-wizard plugin recovery ----
//
// One flow at a time, held here rather than in the readiness panel because the
// panel re-renders whenever the selection changes and a disclosure the user is
// reading must survive that.

let ptcRecoveryFlow = null;
// The blueprint the visible recovery panel belongs to, so selecting a
// different one clears it and selecting the same one (as happens on the
// catalog reload that follows a success) leaves the result on screen.
let ptcRecoveryBlueprintKey = '';

const PTC_RECOVERY_COPY = {
  install_plugin: {
    heading: 'Install this blueprint’s plugin',
    // The label names both halves, because both happen: the server installs and
    // then enables under this one press. Saying only "Install" would leave the
    // enable unaccounted for.
    confirm: 'Install and enable',
    working: 'Installing…',
    preparing: 'Checking what this plugin will do…'
  },
  enable_plugin: {
    heading: 'Enable this blueprint’s plugin',
    confirm: 'Enable plugin',
    working: 'Enabling…',
    preparing: 'Enabling…'
  },
  review_plugin_update: {
    heading: 'Review this plugin’s update',
    confirm: 'Update plugin',
    working: 'Updating…',
    preparing: 'Checking for an update…'
  }
};

function ptcRecoveryHost() {
  return document.getElementById('blueprintRecoveryPanel');
}

// ptcCancelRecovery ends a pending flow the user decided against.
function ptcCancelRecovery() {
  if (ptcRecoveryFlow) ptcRecoveryFlow.cancel();
  ptcRecoveryFlow = null;
  ptcRecoveryBlueprintKey = '';
  const host = ptcRecoveryHost();
  if (host) host.textContent = '';
  // Return focus to the readiness panel the action was offered from, rather
  // than dropping it to the top of the document.
  ptcFocusReadinessPanel();
}

// ptcInvalidateRecovery drops a pending flow silently.
//
// Used when the surface goes away — the modal closed, the selected blueprint
// changed. The user made no decision, so nothing is announced, and crucially a
// confirmation they left on screen can no longer be applied to a blueprint
// they are no longer looking at.
function ptcInvalidateRecovery() {
  if (ptcRecoveryFlow) ptcRecoveryFlow.invalidate();
  ptcRecoveryFlow = null;
  ptcRecoveryBlueprintKey = '';
  const host = ptcRecoveryHost();
  if (host) host.textContent = '';
}

// ptcSyncRecoveryToSelection clears the recovery panel when the user moves to
// a different blueprint. It deliberately does nothing when the selection is
// unchanged: the catalog reload that follows a successful action re-selects
// the same blueprint, and wiping the result there would delete the only
// on-screen record of what just happened.
function ptcSyncRecoveryToSelection() {
  if (ptcRecoveryReloading) return;
  if (!ptcRecoveryBlueprintKey) return;
  if (ptcSelectionKey(ptcSelected) === ptcRecoveryBlueprintKey) return;
  ptcInvalidateRecovery();
}

function ptcRecoveryEndpoint(template) {
  return `/api/project-templates/${encodeURIComponent(template.id)}/plugin-recovery`;
}

// ptcStartRecovery runs one lifecycle action end to end.
//
// The client sends an action name and a plugin name — never a source, a path,
// or a command. The blueprint the user selected is what tells the server where
// the plugin comes from, and the trust preview is where that finally becomes
// visible, immediately before the user agrees to it.
async function ptcStartRecovery(action, template, readiness) {
  const lifecycle = window.PluginLifecycle;
  const host = ptcRecoveryHost();
  if (!lifecycle || !host || !template?.id) {
    ptcOpenPluginsPage();
    return;
  }
  const pluginName = readiness?.dependency?.pluginName || '';
  if (!pluginName) {
    ptcOpenPluginsPage();
    return;
  }

  // A second press while one flow is live must not start another.
  ptcInvalidateRecovery();
  ptcRecoveryBlueprintKey = ptcSelectionKey(template);

  const url = ptcRecoveryEndpoint(template);
  const generation = Number(readiness?.generation) || 0;
  const copy = PTC_RECOVERY_COPY[action] || PTC_RECOVERY_COPY.install_plugin;

  const flow = lifecycle.createFlow({
    preview: () =>
      lifecycle.request('POST', url, { action, plugin: pluginName, confirm: false, generation }),
    apply: () =>
      lifecycle.request('POST', url, { action, plugin: pluginName, confirm: true, generation }),
    onState: (state, payload) =>
      ptcRenderRecovery(state, payload, { action, template, copy, pluginName })
  });
  ptcRecoveryFlow = flow;

  const previewed = await flow.start();
  if (!previewed || flow !== ptcRecoveryFlow) return;

  // Enable needs no disclosure: the components were disclosed when the plugin
  // was installed, and enabling registers exactly those. The server still
  // returns a preview shape, so the flow stays uniform; only the copy differs.
  if (action === 'enable_plugin' && previewed.ok) {
    await ptcConfirmRecovery();
  }
}

async function ptcConfirmRecovery() {
  const flow = ptcRecoveryFlow;
  if (!flow) return;
  const result = await flow.confirm();
  if (!result || flow !== ptcRecoveryFlow) return;
  if (!result.ok) return;

  const data = result.data || {};
  // Match the trusted replacement by the ID the server just told us it has.
  // Display text can change across an update; the qualified ID the server
  // reports is what the next catalog will actually contain.
  ptcRecoveryPreferredId = String(data.blueprint_id || '').trim();
  // The reload re-selects the blueprint — possibly a replacement with a new
  // key. Hold the result on screen across it; the selection change is one this
  // flow caused, not one the user made.
  ptcRecoveryReloading = true;
  try {
    await ptcPopulate({ preserveSelection: true });
  } finally {
    ptcRecoveryReloading = false;
    ptcRecoveryPreferredId = '';
    ptcRecoveryBlueprintKey = ptcSelectionKey(ptcSelected);
  }
}

// True while a completed recovery is reloading the catalog, so the reload's
// own re-selection does not read as the user leaving the blueprint.
let ptcRecoveryReloading = false;

// The blueprint ID a completed recovery says to select next, consulted once by
// the following catalog load.
let ptcRecoveryPreferredId = '';

function ptcRecoveryButton(label, variant, onClick) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = `modern-btn modern-btn-${variant} workspace-blueprint-recovery-action`;
  button.textContent = label;
  button.addEventListener('click', onClick);
  return button;
}

// ptcRenderRecovery paints whichever state the flow is in. It is the only
// writer of the recovery host, so a state cannot half-overwrite another.
function ptcRenderRecovery(state, payload, context) {
  const host = ptcRecoveryHost();
  if (!host) return;
  const lifecycle = window.PluginLifecycle;
  const states = lifecycle.STATES;
  host.textContent = '';
  host.dataset.state = state;

  if (state === states.CANCELLED) return;

  const panel = document.createElement('div');
  panel.className = 'workspace-blueprint-recovery-body';
  // A group rather than a live region: the disclosure is long, and reading it
  // aloud in full on every transition would bury the one line that changed.
  panel.setAttribute('role', 'group');
  panel.setAttribute('aria-label', context.copy.heading);

  const heading = document.createElement('p');
  heading.className = 'workspace-blueprint-recovery-heading';
  heading.textContent = context.copy.heading;
  panel.appendChild(heading);

  if (state === states.PREVIEWING || state === states.APPLYING) {
    const status = document.createElement('p');
    status.className = 'workspace-blueprint-recovery-status';
    status.textContent = state === states.APPLYING ? context.copy.working : context.copy.preparing;
    panel.appendChild(status);
    host.appendChild(panel);
    return;
  }

  if (state === states.FAILED) {
    const message = document.createElement('p');
    message.className = 'workspace-blueprint-recovery-error';
    message.setAttribute('role', 'alert');
    message.textContent = (payload && payload.error) || 'That did not work. Nothing was changed.';
    panel.appendChild(message);
    const outcome = payload?.data?.outcome;
    if (outcome?.detail) {
      const detail = document.createElement('p');
      detail.className = 'workspace-blueprint-recovery-detail';
      detail.textContent = outcome.detail;
      panel.appendChild(detail);
    }
    const actions = document.createElement('div');
    actions.className = 'workspace-blueprint-recovery-actions';
    actions.appendChild(
      ptcRecoveryButton('Manage plugins', 'secondary', () => ptcOpenPluginsPage())
    );
    actions.appendChild(ptcRecoveryButton('Close', 'secondary', () => ptcCancelRecovery()));
    panel.appendChild(actions);
    host.appendChild(panel);
    ptcAnnounceRecovery(message.textContent);
    host.focus();
    return;
  }

  if (state === states.AWAITING_CONFIRMATION) {
    // Enable confirms itself immediately, so its disclosure never renders.
    if (context.action === 'enable_plugin') {
      const status = document.createElement('p');
      status.className = 'workspace-blueprint-recovery-status';
      status.textContent = context.copy.working;
      panel.appendChild(status);
      host.appendChild(panel);
      return;
    }

    const intro = document.createElement('p');
    intro.className = 'workspace-blueprint-recovery-intro';
    intro.textContent =
      context.action === 'review_plugin_update' && payload?.changed === false
        ? `${context.pluginName} asks for nothing new. Updating changes only its version.`
        : `${context.pluginName} will be able to do the following on this computer:`;
    panel.appendChild(intro);

    // Where it comes from, shown here and only here. The catalog withholds a
    // template-declared source on purpose; asking someone to trust software
    // without telling them its origin would be the opposite mistake.
    const source = String(payload?.source || '').trim();
    if (source) {
      const origin = document.createElement('p');
      origin.className = 'workspace-blueprint-recovery-source';
      const label = document.createElement('span');
      label.textContent = 'Installed from ';
      const value = document.createElement('code');
      value.textContent = source;
      origin.append(label, value);
      panel.appendChild(origin);
    }

    panel.appendChild(lifecycle.renderTrustReport(payload?.trust));

    const actions = document.createElement('div');
    actions.className = 'workspace-blueprint-recovery-actions';
    const confirm = ptcRecoveryButton(context.copy.confirm, 'primary', () => {
      // The flow's own state machine refuses a second apply, so a double press
      // cannot install twice; disabling the button makes that visible too.
      confirm.disabled = true;
      void ptcConfirmRecovery();
    });
    actions.appendChild(confirm);
    actions.appendChild(ptcRecoveryButton('Cancel', 'secondary', () => ptcCancelRecovery()));
    panel.appendChild(actions);
    host.appendChild(panel);
    // Focus the disclosure, not the confirm button: the user should arrive at
    // what they are agreeing to, not at the button that agrees.
    host.focus();
    return;
  }

  if (state === states.DONE) {
    const outcome = payload?.outcome || {};
    const message = document.createElement('p');
    message.className = outcome.completed
      ? 'workspace-blueprint-recovery-success'
      : 'workspace-blueprint-recovery-partial';
    message.textContent = outcome.summary || 'Done.';
    panel.appendChild(message);
    if (outcome.detail) {
      const detail = document.createElement('p');
      detail.className = 'workspace-blueprint-recovery-detail';
      detail.textContent = outcome.detail;
      panel.appendChild(detail);
    }
    host.appendChild(panel);
    ptcAnnounceRecovery(
      outcome.detail ? `${message.textContent} ${outcome.detail}` : message.textContent
    );
    // The control that was pressed no longer exists, so focus would otherwise
    // fall to the top of the document. Put it on the result instead — which is
    // also where the remaining action lives when the outcome was partial.
    host.focus();
    return;
  }
}

// One announcement per completed action, through the same region the blocked
// message uses so the two can never overlap each other.
function ptcAnnounceRecovery(message) {
  const els = ptcElements();
  if (els.readinessLive) els.readinessLive.textContent = message;
}

// ptcAnnounceBlocked states, once and concisely, why the wizard did not
// advance. It writes to the dedicated live region rather than the panel so the
// message is heard when it is caused, not every time a card is browsed.
function ptcAnnounceBlocked(template) {
  const els = ptcElements();
  if (!els.readinessLive) return;
  const readiness = ptcReadiness(template);
  const name = template?.name || template?.id || 'This blueprint';
  const summary = readiness?.summary || window.BlueprintReadiness?.badgeLabel(readiness) || '';
  els.readinessLive.textContent = summary
    ? `Cannot continue with ${name}. ${summary}`
    : `Cannot continue with ${name}.`;
}

function ptcClearAnnouncement() {
  const els = ptcElements();
  if (els.readinessLive) els.readinessLive.textContent = '';
}

// Moves focus into the readiness panel so the reason and its action are the
// next thing reached, rather than leaving focus on a Next button that just
// refused to do anything.
function ptcFocusReadinessPanel() {
  const panel = ptcElements().readinessPanel?.querySelector('.workspace-blueprint-readiness');
  if (panel) panel.focus();
  return Boolean(panel);
}

// The selected blueprint's readiness, or null when the current selection has
// none to speak of: Blank depends on nothing, an ad-hoc folder path overrides
// the picker entirely, and import mode does not use blueprints at all. Those
// three must never be blocked by a catalog problem.
function ptcSelectedReadiness() {
  const els = ptcElements();
  if (els.importToggle?.checked) return null;
  if (els.pathInput?.value?.trim()) return null;
  if (!ptcSelected || ptcSelected.blank) return null;
  return ptcReadiness(ptcSelected);
}

function ptcShouldOpenAfterCreate() {
  const els = ptcElements();
  return Boolean(
    els.openAfterCreate && !els.openAfterCreate.hidden && els.openAfterCreateToggle?.checked
  );
}

function ptcBlankCard() {
  return ptcElements().grid?.querySelector('.workspace-template-card[data-template-id=""]') || null;
}

function ptcReset() {
  const els = ptcElements();
  if (els.pathInput) els.pathInput.value = '';
  ptcSelect(PTC_BLANK, ptcBlankCard());
}

function ptcSyncImportVisibility() {
  const els = ptcElements();
  const importMode = Boolean(els.importToggle?.checked);
  // Templates scaffold/seed a new workspace; they don't apply when importing an
  // existing folder as the workspace itself — hide the whole blueprint/briefing
  // block, headers included, so nothing dangles above the import form.
  if (els.picker) els.picker.hidden = importMode;
  if (els.blueprintHeader) els.blueprintHeader.hidden = importMode;
  if (els.briefingHeader) els.briefingHeader.hidden = importMode;
  if (els.briefing) els.briefing.hidden = importMode;
  ptcUpdateUI();
}

// ptcPopulate (re)loads the catalog and rebuilds the picker.
//
// options.preserveSelection keeps the user on the blueprint they had chosen
// across a reload — the case that matters after a recovery action, where
// silently dropping them back to Blank would discard the choice they were part
// way through fixing. The match is by blueprint identity, never display text.
async function ptcPopulate(options) {
  const els = ptcElements();
  if (!els.grid) return;
  const settings = options || {};
  const generation = ++ptcCatalogGeneration;
  const previousId = settings.preserveSelection ? ptcSelectionKey(ptcSelected) : '';

  let data = null;
  let loadFailed = false;
  try {
    data = await ptmFetchJSON('/api/project-templates');
  } catch (error) {
    console.error('Failed to load project templates:', error);
    loadFailed = true;
  }
  // A response from a superseded request must not repaint the picker: the
  // newer load is the one that reflects what the user just did.
  if (generation !== ptcCatalogGeneration) return;

  els.grid.innerHTML = '';
  if (els.userList) els.userList.innerHTML = '';
  if (els.userSection) els.userSection.hidden = true;
  if (els.emptyHint) els.emptyHint.hidden = true;

  // Blank is always the first built-in card and the default selection.
  const blankCard = ptcCard({ ...PTC_BLANK });
  els.grid.appendChild(blankCard);

  let restored = null;
  let restoredTemplate = null;
  if (loadFailed) {
    if (els.emptyHint) {
      els.emptyHint.textContent =
        'Could not load the template library. You can still use any folder in Advanced as a template.';
      els.emptyHint.hidden = false;
    }
  } else {
    const templates = Array.isArray(data.templates) ? data.templates : [];
    const builtins = templates.filter(t => t && t.id && t.builtin);
    const userTemplates = templates.filter(t => t && t.id && !t.builtin);

    const register = (template, element) => {
      // A completed recovery names the blueprint's current qualified ID. It
      // wins over the remembered key: an install can replace a stale built-in
      // with the plugin-owned blueprint that supersedes it, and only the
      // server knows which entry that turned out to be.
      if (ptcRecoveryPreferredId && template.id === ptcRecoveryPreferredId) {
        restored = element;
        restoredTemplate = template;
        return;
      }
      if (restoredTemplate && ptcRecoveryPreferredId) return;
      if (previousId && ptcSelectionKey(template) === previousId) {
        restored = element;
        restoredTemplate = template;
      }
    };
    for (const template of builtins) {
      const card = ptcCard(template);
      els.grid.appendChild(card);
      register(template, card);
    }
    if (userTemplates.length > 0 && els.userList && els.userSection) {
      for (const template of userTemplates) {
        const row = ptcRow(template);
        els.userList.appendChild(row);
        register(template, row);
      }
      els.userSection.hidden = false;
    } else if (builtins.length === 0 && els.emptyHint) {
      els.emptyHint.textContent = data.templates_root
        ? `No templates yet. Drop a template folder into ${data.templates_root} to add one, or use any folder in Advanced.`
        : 'No templates yet. Use any folder in Advanced as a template.';
      els.emptyHint.hidden = false;
    }
  }

  if (restoredTemplate) ptcSelect(restoredTemplate, restored);
  else ptcSelect(PTC_BLANK, blankCard);
}

// ptcSelectionKey identifies a blueprint across a catalog reload. A
// plugin-contributed blueprint is keyed by its owner and blueprint ID rather
// than its qualified ID or display name, so the trusted replacement that
// appears after an install or enable is recognized as the same choice even
// though its identity in the catalog changed.
function ptcSelectionKey(template) {
  if (!template || template.blank) return '';
  const owner = template.plugin_owner;
  if (owner && owner.plugin_id && owner.blueprint_id) {
    return `plugin:${owner.plugin_id}:${owner.blueprint_id}`;
  }
  return template.id ? `template:${template.id}` : '';
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
      // The ad-hoc folder overrides the picked template: deselect cards (Blank
      // keeps the typed path) and let getPayloadFields prefer template_path.
      ptcSelect(PTC_BLANK, ptcBlankCard());
      els.pathInput.value = picked.path;
      ptcUpdateUI();
      els.pathInput.dispatchEvent(new Event('input', { bubbles: true }));
    }
  } catch (error) {
    ptmToast(error.message || 'Failed to open folder picker', 'error');
  } finally {
    if (els.browseBtn) els.browseBtn.disabled = false;
  }
}

function ptcInit() {
  const els = ptcElements();
  if (!els.picker) return;

  if (els.pathInput) {
    els.pathInput.addEventListener('input', () => {
      // A typed path overrides the card selection without re-prefilling fields.
      if (els.pathInput.value.trim()) {
        ptcSelected = PTC_BLANK;
        ptcMarkSelectedAcross(ptcBlankCard());
        ptcEmitSelection();
      }
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

  // (Re)load the library every time the create modal opens, whichever module
  // opened it. Runs after the host module's own show handler.
  const createModal = document.getElementById('addFolderModal');
  if (createModal) {
    createModal.addEventListener('show.bs.modal', () => {
      ptcInvalidateRecovery();
      ptcClearAnnouncement();
      ptcSyncImportVisibility();
      void ptcPopulate();
    });
    // Closing the modal ends any pending confirmation. The user walked away
    // from a disclosure without answering it, so there is no consent to carry
    // into the next time the wizard opens.
    createModal.addEventListener('hidden.bs.modal', () => {
      ptcInvalidateRecovery();
      ptcClearAnnouncement();
    });
  }
  ptcSyncImportVisibility();
}

window.ProjectTemplateCard = {
  populate: ptcPopulate,
  reset: ptcReset,
  // The selected blueprint's readiness, normalized, or null when the current
  // selection has none (Blank, an ad-hoc folder, import mode). The wizard uses
  // it to decide whether advancing is allowed and what to announce; the server
  // decides whether creating is.
  getSelectedReadiness: ptcSelectedReadiness,
  // Re-reads the catalog and returns the selected blueprint's current
  // readiness. The wizard calls it before advancing and before Create, because
  // the catalog on screen can be minutes old and a plugin can be disabled in
  // another tab. It is still only guidance — the server refuses again anyway.
  recheckSelection: async () => {
    const readiness = ptcSelectedReadiness();
    if (!readiness) return null;
    await ptcPopulate({ preserveSelection: true });
    return ptcSelectedReadiness();
  },
  cancelRecovery: ptcCancelRecovery,
  invalidateRecovery: ptcInvalidateRecovery,
  isSelectionBlocked: () => {
    const readiness = ptcSelectedReadiness();
    return Boolean(readiness && readiness.state !== 'ready');
  },
  announceBlocked: () => ptcAnnounceBlocked(ptcSelected),
  focusReadiness: ptcFocusReadinessPanel,
  clearAnnouncement: ptcClearAnnouncement,
  // syncState re-syncs import-mode visibility (which hides the blueprint grid,
  // briefing, and their headers) as well as the per-selection UI, so callers
  // like setImportModeEnabled don't depend on show-handler ordering.
  syncState: ptcSyncImportVisibility,
  getPayloadFields: ptcGetPayloadFields,
  getSelectedTemplate: ptcGetSelectedTemplate,
  shouldOpenAfterCreate: ptcShouldOpenAfterCreate
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
        onChanged: () =>
          document.getElementById('workspace-detail-project-template-refresh')?.click()
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
    document
      .getElementById('openTemplatesRootBtn')
      ?.addEventListener('click', () => void ptmReveal(''));
    document.getElementById('manageTemplatesBtn')?.addEventListener('click', () => {
      ptmOpenModal({ onChanged: null });
    });
  }
}

window.ProjectTemplatesManage = { open: ptmOpenModal, refresh: ptmRefresh };

document.addEventListener('DOMContentLoaded', ptmInitListeners);
