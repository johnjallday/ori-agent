const ROOT = '/api/personal-assistant/setup-journey';

const state = {
  journey: null,
  selectedStepID: '',
  draft: null,
  review: null,
  reviewInput: null,
  returnFocus: null,
  busy: false,
  commitLocked: false,
  modal: null
};

function byID(id) {
  return document.getElementById(id);
}

function ui() {
  const root = byID('specialistSetupJourneyModal');
  if (!root) return null;
  return {
    root,
    title: byID('specialistSetupJourneyTitle'),
    description: byID('specialistSetupJourneyDescription'),
    steps: byID('specialistSetupJourneySteps'),
    busy: byID('specialistSetupJourneyBusy'),
    error: byID('specialistSetupJourneyError'),
    content: byID('specialistSetupJourneyContent'),
    stepState: byID('specialistSetupJourneyStepState'),
    stepTitle: byID('specialistSetupJourneyStepTitle'),
    stepDescription: byID('specialistSetupJourneyStepDescription'),
    receipt: byID('specialistSetupJourneyReceipt'),
    draft: byID('specialistSetupJourneyDraft'),
    actions: byID('specialistSetupJourneyActions'),
    review: byID('specialistSetupJourneyReview'),
    live: byID('specialistSetupJourneyLiveStatus'),
    close: byID('specialistSetupJourneyClose'),
    later: byID('specialistSetupJourneyLater')
  };
}

export function setupJourneyCurrentStep(journey, selectedID = '') {
  const steps = Array.isArray(journey?.steps) ? journey.steps : [];
  return (
    steps.find(step => step?.id === selectedID) ||
    steps.find(step => step?.id === journey?.current_step_id) ||
    steps.find(step => step?.status !== 'complete') ||
    steps[0] ||
    null
  );
}

export function setupJourneyReceiptRows(journey, step) {
  const rows = [];
  if (step?.integration) {
    rows.push(['Integration', step.integration.plugin_id]);
    rows.push(['Version', step.integration.installed_version || step.integration.expected_version]);
    rows.push(['Enabled', step.integration.enabled ? 'Yes' : 'Not yet']);
  }
  if (step?.workspace_setup) {
    rows.push(['Files connected', step.workspace_setup.files_connected ? 'Yes' : 'No']);
    rows.push(['Operating mode', step.workspace_setup.mode_label || 'Not selected']);
    rows.push([
      'Live control configured',
      step.workspace_setup.live_control_configured ? 'Yes' : 'No'
    ]);
    rows.push(['Live control tested', step.workspace_setup.live_control_tested ? 'Yes' : 'No']);
  }
  if (step?.kind === 'project_connect' && journey?.receipts?.project_workspace_id) {
    rows.push(['Project', 'Connected']);
  }
  if (journey?.lifecycle === 'ready') rows.push(['Setup', 'Ready']);
  return rows.filter(row => row[1]);
}

export function newJourneyIdempotencyKey() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `journey-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function setupJourneyControlDisabled(busy, lockClose, isCloseControl) {
  return Boolean(busy && (!isCloseControl || lockClose));
}

function setBusy(busy, message = '', { lockClose = false } = {}) {
  state.busy = busy;
  state.commitLocked = busy && lockClose;
  const elements = ui();
  if (!elements) return;
  elements.busy.hidden = !busy;
  elements.live.textContent = message;
  elements.root.setAttribute('aria-busy', busy ? 'true' : 'false');
  elements.root.querySelectorAll('button, input, select').forEach(control => {
    const isCloseControl = control === elements.close || control === elements.later;
    if (!control.closest('#specialistSetupJourneyBusy')) {
      control.disabled = setupJourneyControlDisabled(busy, lockClose, isCloseControl);
    }
  });
}

function hideJourneyPresentation() {
  const elements = ui();
  if (state.modal) state.modal.hide();
  else if (elements) {
    elements.root.classList.remove('show');
    elements.root.style.display = 'none';
    elements.root.setAttribute('aria-hidden', 'true');
  }
  state.returnFocus?.focus?.();
}

function showError(message = '') {
  const element = ui()?.error;
  if (!element) return;
  element.textContent = message;
  element.hidden = !message;
  if (message) element.focus?.();
}

async function request(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {})
    }
  });
  let payload = null;
  try {
    payload = await response.json();
  } catch (_) {
    payload = null;
  }
  if (!response.ok) {
    const failure = new Error(
      payload?.error?.guidance ||
        'Setup could not be updated. Check the current state and try again.'
    );
    failure.current = payload?.current || null;
    throw failure;
  }
  return payload;
}

function mutationBody(input = {}) {
  return {
    if_revision: Number(state.journey?.state_revision) || 0,
    idempotency_key: newJourneyIdempotencyKey(),
    ...input
  };
}

function runURL(suffix = '') {
  const id = encodeURIComponent(String(state.journey?.run_id || ''));
  return `${ROOT}/runs/${id}${suffix}`;
}

function makeText(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = text || '';
  return node;
}

function appendRows(container, rows, className = 'setup-journey__receipt-list') {
  if (!rows.length) return;
  const list = document.createElement('ul');
  list.className = className;
  rows.forEach(([label, value]) => {
    const item = document.createElement('li');
    const strong = makeText('strong', '', `${label}: `);
    item.append(strong, document.createTextNode(String(value)));
    list.appendChild(item);
  });
  container.appendChild(list);
}

function statusLabel(status) {
  return (
    {
      complete: 'Complete',
      current: 'Next step',
      blocked: 'Needs attention',
      pending: 'Not started'
    }[status] || 'Setup step'
  );
}

function render() {
  const elements = ui();
  if (!elements || !state.journey) return;
  const journey = state.journey;
  const step = setupJourneyCurrentStep(journey, state.selectedStepID);
  state.selectedStepID = step?.id || '';
  elements.title.textContent = journey.journey?.title || 'Setup';
  elements.description.textContent = journey.journey?.description || '';
  elements.steps.replaceChildren();
  (journey.steps || []).forEach((candidate, index) => {
    const item = document.createElement('li');
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'setup-journey__step-button';
    button.dataset.status = candidate.status || 'pending';
    if (candidate.id === step?.id) button.setAttribute('aria-current', 'step');
    button.append(
      makeText(
        'span',
        'setup-journey__step-number',
        candidate.status === 'complete' ? '✓' : String(index + 1)
      ),
      makeText('span', 'setup-journey__step-label', candidate.title)
    );
    button.addEventListener('click', () => selectStep(candidate.id, button));
    item.appendChild(button);
    elements.steps.appendChild(item);
  });
  elements.stepState.textContent = statusLabel(step?.status);
  elements.stepTitle.textContent = step?.title || '';
  elements.stepDescription.textContent = step?.description || '';
  elements.receipt.replaceChildren();
  if (step?.guidance) elements.receipt.appendChild(makeText('p', '', step.guidance));
  appendRows(elements.receipt, setupJourneyReceiptRows(journey, step));
  renderDraft(step);
  renderActions(step);
  renderReview();
  elements.live.textContent = journey.busy ? 'Another setup action is being reconciled.' : '';
}

function selectStep(stepID, trigger) {
  if (state.selectedStepID === stepID) return;
  // Browser drafts and uncommitted review receipts belong to one route only.
  // Switching the rail discards them without mutating server state.
  state.selectedStepID = stepID;
  state.draft = null;
  state.review = null;
  state.reviewInput = null;
  state.returnFocus = trigger;
  showError('');
  render();
  ui()?.stepTitle?.focus();
}

function renderActions(step) {
  const container = ui().actions;
  container.replaceChildren();
  (step?.actions || []).forEach(action => {
    const button = makeText('button', 'setup-journey__action', action.label);
    button.type = 'button';
    button.dataset.effect = action.effect || '';
    button.dataset.action = action.id || '';
    button.addEventListener('click', () => handleAction(action, button));
    container.appendChild(button);
  });
}

async function handleAction(action, trigger) {
  if (state.busy || !action?.id) return;
  state.returnFocus = trigger;
  showError('');
  if (action.effect === 'review') {
    if (action.id === 'review_existing_project') return beginExistingProject();
    if (action.id === 'review_new_project') return beginNewProject();
    if (
      ['review_home_staffing', 'review_project_staffing', 'review_optional_home_staffing'].includes(
        action.id
      )
    )
      return beginStaffing(action.id);
    return reviewAction(action.id, {});
  }
  if (action.effect === 'navigation') return navigateAction(action.id);
  if (action.id === 'connect_another_project') return createChildRun();
}

async function beginExistingProject() {
  setBusy(true, 'Waiting for the trusted folder picker…');
  try {
    const picked = await request('/api/folder-picker/select-path', {
      method: 'POST',
      body: JSON.stringify({ title: 'Choose the project folder' })
    });
    if (!picked?.selected) {
      state.returnFocus?.focus?.();
      return;
    }
    if (!picked.selection_token)
      throw new Error('The trusted folder selection expired. Choose the folder again.');
    const pieces = String(picked.path || '')
      .split(/[\\/]/)
      .filter(Boolean);
    state.draft = {
      kind: 'existing',
      selectionToken: picked.selection_token,
      selectedFolder: picked.path || '',
      workspaceName: pieces.at(-1) || 'Project',
      entryName: '',
      candidates: []
    };
    state.review = null;
    render();
    ui()?.draft?.querySelector('input')?.focus();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

function beginNewProject() {
  state.draft = { kind: 'new', workspaceName: '', projectName: '', candidates: [] };
  state.review = null;
  render();
  ui()?.draft?.querySelector('input')?.focus();
}

function beginStaffing(actionID) {
  const optional = actionID === 'review_optional_home_staffing';
  const wantedScope = actionID === 'review_project_staffing' ? 'project' : 'home';
  const staffing = setupJourneyCurrentStep(state.journey, state.selectedStepID)?.staffing;
  const target = (staffing?.scopes || []).find(item => item.scope === wantedScope);
  if (!target) {
    showError('Staffing details are unavailable. Refresh setup and try again.');
    return;
  }
  state.draft = {
    kind: 'staffing',
    actionID,
    scope: wantedScope,
    workspaceLabel: target.workspace_label,
    roles: (target.roles || [])
      .filter(role => (optional ? !role.required : role.required) && !role.configured)
      .map(role => ({
        roleID: role.role_id,
        label: role.label,
        name:
          wantedScope === 'project'
            ? `${role.label} · ${target.workspace_label}`.slice(0, 80)
            : role.label.slice(0, 80),
        provider: '',
        model: '',
        toolGrants: role.tool_grants || []
      }))
  };
  state.review = null;
  render();
  ui()?.draft?.querySelector('input')?.focus();
}

function renderDraft(step) {
  const container = ui().draft;
  container.replaceChildren();
  if (!state.draft) return;
  if (state.draft.kind === 'staffing') {
    renderStaffingDraft(container, step);
    return;
  }
  if (step?.kind !== 'project_connect') return;
  const form = document.createElement('form');
  form.className = 'setup-journey__form';
  if (state.draft.kind === 'existing') {
    form.appendChild(
      field(
        'Workspace name',
        'The managed child shown in Ori.',
        'text',
        state.draft.workspaceName,
        value => {
          state.draft.workspaceName = value;
        }
      )
    );
    const scope = makeText('p', '', `Selected folder: ${state.draft.selectedFolder}`);
    form.appendChild(scope);
    if (state.draft.candidates.length > 1) {
      const label = document.createElement('label');
      label.className = 'setup-journey__field';
      label.appendChild(makeText('span', '', 'Authoritative project file'));
      const select = document.createElement('select');
      select.required = true;
      select.appendChild(new Option('Choose one file', ''));
      state.draft.candidates.forEach(name => select.appendChild(new Option(name, name)));
      select.value = state.draft.entryName;
      select.addEventListener('change', () => {
        state.draft.entryName = select.value;
      });
      label.appendChild(select);
      form.appendChild(label);
    }
  } else {
    form.appendChild(
      field(
        'Workspace name',
        'The child workspace shown in Ori.',
        'text',
        state.draft.workspaceName,
        value => {
          state.draft.workspaceName = value;
        }
      )
    );
    form.appendChild(
      field(
        'Project name',
        'Used for the managed project folder and scaffold names.',
        'text',
        state.draft.projectName,
        value => {
          state.draft.projectName = value;
        }
      )
    );
  }
  const submit = makeText(
    'button',
    'setup-journey__action',
    state.draft.candidates.length > 1 ? 'Review selected file' : 'Continue to review'
  );
  submit.type = 'submit';
  submit.dataset.effect = 'review';
  form.appendChild(submit);
  form.addEventListener('submit', event => {
    event.preventDefault();
    if (!form.reportValidity()) return;
    const input = projectDraftInput(state.draft);
    reviewAction(
      state.draft.kind === 'existing' ? 'review_existing_project' : 'review_new_project',
      input
    );
  });
  container.appendChild(form);
}

function renderStaffingDraft(container, step) {
  if (step?.kind !== 'assistant_program_staffing' || !state.draft.roles.length) return;
  const form = document.createElement('form');
  form.className = 'setup-journey__form';
  form.appendChild(
    makeText(
      'p',
      'setup-journey__scope-note',
      `These profiles belong only to ${state.draft.workspaceLabel}. Names must be unique in Your Agents.`
    )
  );
  state.draft.roles.forEach(role => {
    const group = document.createElement('fieldset');
    group.className = 'setup-journey__staffing-role';
    group.appendChild(makeText('legend', '', role.label));
    group.appendChild(
      field('Profile name', 'The independently saved profile name.', 'text', role.name, value => {
        role.name = value;
      })
    );
    group.appendChild(
      field(
        'Provider',
        'Leave blank with Model to use the current system default.',
        'text',
        role.provider,
        value => {
          role.provider = value;
        },
        false
      )
    );
    group.appendChild(
      field(
        'Model',
        'Leave blank with Provider to use the current system default.',
        'text',
        role.model,
        value => {
          role.model = value;
        },
        false
      )
    );
    group.appendChild(
      makeText(
        'small',
        '',
        role.toolGrants.length ? `Tool grants: ${role.toolGrants.join(', ')}` : 'Tool grants: none'
      )
    );
    form.appendChild(group);
  });
  const submit = makeText('button', 'setup-journey__action', 'Review scoped staffing');
  submit.type = 'submit';
  submit.dataset.effect = 'review';
  form.appendChild(submit);
  form.addEventListener('submit', event => {
    event.preventDefault();
    if (!form.reportValidity()) return;
    reviewAction(state.draft.actionID, {
      roles: state.draft.roles.map(role => ({
        role_id: role.roleID,
        name: role.name.trim(),
        provider: role.provider.trim() || undefined,
        model: role.model.trim() || undefined
      }))
    });
  });
  container.appendChild(form);
}

function field(labelText, help, type, value, onInput, required = true) {
  const label = document.createElement('label');
  label.className = 'setup-journey__field';
  label.appendChild(makeText('span', '', labelText));
  const input = document.createElement('input');
  input.type = type;
  input.required = required;
  input.maxLength = 128;
  input.value = value || '';
  input.addEventListener('input', () => onInput(input.value));
  label.append(input, makeText('small', '', help));
  return label;
}

function projectDraftInput(draft) {
  if (draft.kind === 'existing') {
    return {
      mode_id: 'existing_project',
      selection_token: draft.selectionToken,
      entry_name: draft.entryName || undefined,
      workspace_name: draft.workspaceName.trim()
    };
  }
  return {
    mode_id: 'new_project',
    workspace_name: draft.workspaceName.trim(),
    project_name: draft.projectName.trim()
  };
}

async function reviewAction(actionID, input) {
  setBusy(true, 'Preparing an exact review…');
  showError('');
  try {
    const payload = await request(runURL(`/actions/${encodeURIComponent(actionID)}`), {
      method: 'POST',
      body: JSON.stringify(mutationBody({ input }))
    });
    if (!payload?.review) throw new Error('The review is unavailable. Nothing was changed.');
    const project = payload.review.project_connection;
    if (
      project &&
      !project.entry_name &&
      Array.isArray(project.entry_candidates) &&
      project.entry_candidates.length > 1
    ) {
      state.draft.candidates = project.entry_candidates.slice();
      state.review = null;
      state.reviewInput = null;
      render();
      ui()?.draft?.querySelector('select')?.focus();
      return;
    }
    state.review = payload.review;
    state.reviewInput = input;
    render();
    ui()?.review?.querySelector('button')?.focus();
  } catch (error) {
    if (error.current) state.journey = error.current;
    showError(error.message);
    render();
  } finally {
    setBusy(false);
  }
}

function renderReview() {
  const container = ui().review;
  container.replaceChildren();
  container.hidden = !state.review;
  ui().content.hidden = Boolean(state.review);
  if (!state.review) return;
  container.appendChild(makeText('h4', '', 'Review before making changes'));
  const rows = reviewRows(state.review);
  appendRows(container, rows, 'setup-journey__review-list');
  const controls = document.createElement('div');
  controls.className = 'setup-journey__review-controls';
  const cancel = makeText('button', 'btn btn-outline-secondary', 'Back');
  cancel.type = 'button';
  cancel.addEventListener('click', () => {
    state.review = null;
    state.reviewInput = null;
    render();
    state.returnFocus?.focus?.();
  });
  const confirm = makeText(
    'button',
    'setup-journey__action setup-journey__review-confirm',
    'Confirm this change'
  );
  confirm.type = 'button';
  confirm.addEventListener('click', commitReview);
  controls.append(cancel, confirm);
  container.appendChild(controls);
}

function reviewRows(review) {
  const rows = [];
  const integration = review.integration;
  if (integration) {
    rows.push(
      ['Integration', integration.plugin_id],
      ['Publisher', integration.publisher],
      ['Source', integration.source_label],
      ['Version', integration.expected_version],
      ['Platform', (integration.supported_platforms || []).join(', ')],
      ['Required host features', (integration.required_host_features || []).join(', ')],
      ['Enabled after this action', integration.enabled ? 'Already enabled' : 'No']
    );
    const trust = integration.trust || {};
    Object.entries(trust).forEach(([key, value]) => {
      if (value === null || value === '' || (Array.isArray(value) && !value.length)) return;
      rows.push([
        humanize(key),
        Array.isArray(value)
          ? value.map(item => (typeof item === 'object' ? JSON.stringify(item) : item)).join('; ')
          : String(value)
      ]);
    });
  }
  const project = review.project_connection;
  if (project) {
    rows.push(
      ['Workspace', project.workspace_name],
      ['Parent', project.parent_workspace_name],
      [
        'Home',
        project.home_will_be_created ? 'Will be created if still missing' : 'Will be reused'
      ],
      ['Project file', project.entry_name]
    );
    if (project.selected_folder) rows.push(['Authorized folder', project.selected_folder]);
    if (project.project_name) rows.push(['Project name', project.project_name]);
    if (project.created_files?.length)
      rows.push(['Files the blueprint creates', project.created_files.join(', ')]);
    if (project.defaults_statement) rows.push(['Blueprint defaults', project.defaults_statement]);
  }
  const staffing = review.staffing;
  if (staffing?.scopes?.length) {
    const target = staffing.scopes[0];
    rows.push(
      ['Staffing scope', humanize(target.scope)],
      ['Workspace', target.workspace_label],
      ['Workspace ID', target.workspace_id],
      ['Current binding revision', target.binding_revision],
      ['Runtime ready', target.runtime_ready ? 'Yes' : 'No'],
      ['Models ready', target.models_ready ? 'Yes' : 'No — deterministic features still work'],
      ['Authority boundary', target.authority_boundary],
      [
        'Operating mode',
        target.selected_mode_id ? humanize(target.selected_mode_id) : 'Not required'
      ]
    );
    (target.roles || []).forEach(role => {
      rows.push(
        [`${role.label} responsibility`, role.responsibility],
        [`${role.label} profile`, role.profile_name],
        [
          `${role.label} chat/execution`,
          role.chat_available ? 'Available' : 'Unavailable — no compatible model is configured'
        ],
        [`${role.label} model`, role.model || 'No configured model'],
        [`${role.label} provider`, role.provider || 'No configured provider'],
        [
          `${role.label} tool grants`,
          role.tool_grants?.length ? role.tool_grants.join(', ') : 'None'
        ]
      );
    });
  }
  const setup = review.workspace_setup;
  if (setup) {
    rows.push(
      ['Operating mode', setup.mode_label],
      ['What it means', setup.mode_description],
      ['Files connected', setup.files_connected ? 'Yes' : 'No'],
      ['Live control configured', setup.live_control_configured ? 'Yes' : 'No'],
      ['Live control tested', setup.live_control_tested ? 'Yes' : 'No']
    );
  }
  rows.push(['Consent expires', new Date(review.expires_at).toLocaleString()]);
  return rows.filter(row => row[1] !== undefined && row[1] !== '');
}

function humanize(value) {
  return String(value)
    .replaceAll('_', ' ')
    .replace(/\b\w/g, letter => letter.toUpperCase());
}

async function commitReview() {
  if (!state.review || state.busy) return;
  const review = state.review;
  setBusy(true, 'Applying the reviewed change…', { lockClose: true });
  try {
    const payload = await request(runURL(`/actions/${encodeURIComponent(review.commit_action)}`), {
      method: 'POST',
      body: JSON.stringify(
        mutationBody({ review_token: review.token, input: state.reviewInput || {} })
      )
    });
    state.journey = payload?.setup_journey || state.journey;
    state.review = null;
    state.reviewInput = null;
    state.draft = null;
    state.selectedStepID = state.journey?.current_step_id || '';
    render();
    ui()?.stepTitle?.focus();
  } catch (error) {
    if (error.current) state.journey = error.current;
    state.review = null;
    state.reviewInput = null;
    showError(error.message);
    render();
  } finally {
    setBusy(false);
  }
}

async function workspaceRoute(workspaceID, suffix = '') {
  if (!workspaceID) return '';
  try {
    const workspace = await request(`/api/workspaces/${encodeURIComponent(workspaceID)}`);
    const slug = String(workspace?.folder_slug || '').trim();
    if (!slug) throw new Error('That workspace route is unavailable.');
    return `/workspaces/${encodeURIComponent(slug)}${suffix}`;
  } catch (_) {
    showError('That workspace route is unavailable. Refresh setup and try again.');
    return '';
  }
}

async function navigateAction(actionID) {
  const receipts = state.journey?.receipts || {};
  switch (actionID) {
    case 'manage_integration':
      window.location.assign('/plugins');
      return;
    case 'open_project':
      if (!receipts.project_workspace_id) return;
      setBusy(true, 'Sending an operating-system open request…');
      try {
        await request(
          `/api/workspaces/${encodeURIComponent(receipts.project_workspace_id)}/project/open`,
          { method: 'POST' }
        );
        ui().live.textContent =
          'The operating system accepted the open request. Live control is not implied.';
      } catch (error) {
        showError(error.message);
      } finally {
        setBusy(false);
      }
      return;
    case 'open_workspace_setup':
    case 'open_live_setup': {
      const route = await workspaceRoute(receipts.project_workspace_id, '?panel=settings');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_home': {
      const route = await workspaceRoute(receipts.home_workspace_id, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_home_staffing': {
      const route = await workspaceRoute(receipts.home_workspace_id, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_project_staffing': {
      const route = await workspaceRoute(receipts.project_workspace_id, '/assistant');
      if (route) window.location.assign(route);
      return;
    }
    case 'open_sample_library_setup': {
      const route = await workspaceRoute(
        receipts.home_workspace_id,
        '/assistant#sampleLibraryPanel'
      );
      if (route) window.location.assign(route);
      return;
    }
    case 'refresh_workspace_setup':
    case 'review_setup':
      return refreshJourney();
    default:
      return;
  }
}

async function createChildRun() {
  setBusy(true, 'Preparing another independent project setup…');
  try {
    const payload = await request(`${ROOT}/children`, {
      method: 'POST',
      body: JSON.stringify(mutationBody())
    });
    state.journey = payload?.setup_journey || state.journey;
    state.selectedStepID = state.journey?.current_step_id || '';
    state.draft = null;
    state.review = null;
    render();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

async function refreshJourney() {
  if (!state.journey?.run_id) return;
  setBusy(true, 'Refreshing canonical setup status…');
  try {
    const payload = await request(runURL());
    state.journey = payload?.setup_journey || state.journey;
    render();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

async function dismissJourney() {
  if (!state.journey || state.commitLocked) return;
  if (state.busy) {
    hideJourneyPresentation();
    return;
  }
  setBusy(true, 'Saving your place…');
  try {
    const payload = await request(runURL('/dismiss'), {
      method: 'POST',
      body: JSON.stringify(mutationBody())
    });
    state.journey = payload?.setup_journey || state.journey;
    hideJourneyPresentation();
  } catch (error) {
    showError(error.message);
  } finally {
    setBusy(false);
  }
}

export async function openSpecialistSetupJourney(requested = null) {
  const intent = String(requested?.detail?.intent || requested?.intent || 'review');
  const elements = ui();
  if (!elements || state.busy) return false;
  state.returnFocus = document.activeElement;
  setBusy(true, 'Loading current setup…');
  showError('');
  try {
    let payload = await request(ROOT);
    state.journey = payload?.setup_journey;
    if (!state.journey) return false;
    payload = await request(`${ROOT}/open`, {
      method: 'POST',
      body: JSON.stringify(mutationBody())
    });
    state.journey = payload?.setup_journey || state.journey;
    if (intent === 'connect_another' && state.journey?.lifecycle === 'ready') {
      payload = await request(`${ROOT}/children`, {
        method: 'POST',
        body: JSON.stringify(mutationBody())
      });
      state.journey = payload?.setup_journey || state.journey;
    }
    state.selectedStepID = state.journey.current_step_id || '';
    state.draft = null;
    state.review = null;
    render();
    if (globalThis.bootstrap?.Modal) {
      state.modal ||= globalThis.bootstrap.Modal.getOrCreateInstance(elements.root);
      state.modal.show();
      elements.root.addEventListener('shown.bs.modal', () => elements.stepTitle?.focus(), {
        once: true
      });
      elements.root.addEventListener('hidden.bs.modal', () => state.returnFocus?.focus?.(), {
        once: true
      });
    } else {
      elements.root.classList.add('show');
      elements.root.style.display = 'block';
      elements.root.removeAttribute('aria-hidden');
      elements.stepTitle?.focus();
    }
    return true;
  } catch (error) {
    showError(error.message);
    return false;
  } finally {
    setBusy(false);
  }
}

function initialize() {
  const elements = ui();
  if (!elements) return;
  elements.close?.addEventListener('click', dismissJourney);
  elements.later?.addEventListener('click', dismissJourney);
  window.addEventListener(
    'keydown',
    event => {
      if (event.key !== 'Escape' || !elements.root.classList.contains('show')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      if (state.commitLocked) {
        elements.live.textContent = 'Finish the reviewed change before closing setup.';
        return;
      }
      void dismissJourney();
    },
    true
  );
  window.addEventListener('ori:open-specialist-setup', openSpecialistSetupJourney);
  const params = new URLSearchParams(window.location.search);
  if (params.get('setup') === 'specialist') openSpecialistSetupJourney();
}

if (document.readyState === 'loading')
  document.addEventListener('DOMContentLoaded', initialize, { once: true });
else initialize();
