// The shared Create Workspace UI owns details/team/confirmation. This bridge
// changes only its project-creation transport to the exact journey owner. It
// never stores selection paths, consent, or drafts in browser storage.
const ROOT = '/api/personal-assistant/setup-journey';
let active = null;
let savedDraft = null; // One run, browser memory only; never review consent.
const el = id => document.getElementById(id);
const key = () => crypto.randomUUID();
const text = (tag, value) => {
  const node = document.createElement(tag);
  node.textContent = value;
  return node;
};

async function jsonRequest(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {})
    }
  });
  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new Error(
      body?.error?.guidance ||
        'The project change could not be confirmed. Check setup before trying again.'
    );
    error.current = body?.current;
    throw error;
  }
  return body;
}
function runURL(state, suffix = '') {
  return `${ROOT}/runs/${encodeURIComponent(state.journey.run_id)}${suffix}`;
}
function inputFor(state) {
  const name = el('folderNameInput').value.trim();
  return state.mode === 'existing_project'
    ? {
        mode_id: state.mode,
        workspace_name: name,
        selection_token: state.selectionToken,
        entry_name: state.entryName || undefined
      }
    : { mode_id: 'new_project', workspace_name: name, project_name: name };
}
function reviewError(message) {
  const box = el('workspaceJourneyReview');
  if (box) {
    box.replaceChildren(text('p', message));
    box.setAttribute('role', 'status');
  }
}

function refreshReview() {
  const state = active;
  if (!state) return;
  const openToggle = el('projectTemplateOpenAfterCreateToggle');
  if (openToggle && !state.openTouched) openToggle.checked = false;
  const input = inputFor(state);
  const signature = JSON.stringify(input);
  if (state.signature === signature) return;
  state.signature = signature;
  state.review = null;
  el('createFolderBtn').disabled = true;
  const generation = ++state.generation;
  if (!input.workspace_name) {
    reviewError('Name the workspace to review its project files.');
    return;
  }
  if (state.mode === 'existing_project' && !state.selectionToken) {
    reviewError('Choose the existing project folder first.');
    return;
  }
  reviewError('Preparing the exact project review…');
  state.preparing = (async () => {
    try {
      const current = await jsonRequest(runURL(state));
      if (active !== state || generation !== state.generation) return;
      state.journey = current.setup_journey;
      if (state.journey.receipts?.project_workspace_id) {
        reviewError(
          'This setup already has a project. Return to setup to open it; no second project will be created.'
        );
        return;
      }
      const action =
        state.mode === 'existing_project' ? 'review_existing_project' : 'review_new_project';
      const payload = await jsonRequest(runURL(state, `/actions/${action}`), {
        method: 'POST',
        body: JSON.stringify({
          if_revision: state.journey.state_revision,
          idempotency_key: key(),
          input
        })
      });
      if (active !== state || generation !== state.generation) return;
      const project = payload?.review?.project_connection;
      if (!project)
        throw new Error(
          'The exact project review is unavailable. Nothing was created by this review.'
        );
      const box = el('workspaceJourneyReview');
      box.replaceChildren(text('h3', 'Project files'));
      if (project.selected_folder) box.appendChild(text('p', `Folder: ${project.selected_folder}`));
      if (!project.entry_name && project.entry_candidates?.length) {
        const label = text('label', 'Project file to import');
        const select = document.createElement('select');
        select.className = 'modern-input w-100';
        select.appendChild(new Option('Choose a project file…', ''));
        project.entry_candidates.forEach(name => select.appendChild(new Option(name, name)));
        select.addEventListener('change', () => {
          state.entryName = select.value;
          refreshReview();
        });
        label.appendChild(select);
        box.appendChild(label);
        return;
      }
      box.append(
        text('p', `Group: ${project.parent_workspace_name}`),
        text('p', `Project file: ${project.entry_name}`)
      );
      if (project.created_files?.length) {
        const list = document.createElement('ul');
        project.created_files.forEach(name => list.appendChild(text('li', name)));
        box.appendChild(list);
      } else
        box.appendChild(text('p', 'Your existing folder and files stay in place and unchanged.'));
      box.appendChild(
        text(
          'p',
          'This workspace starts in File-only mode. Live control is not enabled by creation. Approve access and verify the correct project from workspace Settings.'
        )
      );
      state.review = {
        ...payload.review,
        input,
        signature,
        revision: state.journey.state_revision
      };
      window.sessionManager?.refreshWizardChrome();
    } catch (error) {
      if (active !== state || generation !== state.generation) return;
      state.signature = '';
      reviewError(
        `${error.message} Your entries are kept; return to Details and try the review again.`
      );
    }
  })();
}

async function submit(payload) {
  const state = active;
  if (!state) throw new Error('Workspace setup is no longer open.');
  if (
    payload.template_id !== state.templateID ||
    payload.template_path ||
    payload.parent_id !== state.homeID ||
    payload.location ||
    payload.project_path ||
    payload.blank ||
    payload.existing_agent_names?.length
  ) {
    throw new Error(
      'This setup creates one project in the reviewed group. Return to setup if you need another blueprint or group.'
    );
  }
  // Confirmation uses the review already visible to the user, never a
  // freshly fetched replacement under an earlier click.
  const review = state.review;
  const signature = JSON.stringify(inputFor(state));
  if (!review || review.signature !== signature)
    throw new Error('Wait for the exact project review before creating the workspace.');
  if (state.pending && state.pending.signature !== signature)
    throw new Error(
      'The previous project change is uncertain. Return to setup to check its status before changing the name.'
    );
  state.pending ||= {
    signature,
    action: review.commit_action,
    body: {
      if_revision: review.revision,
      idempotency_key: key(),
      review_token: review.token,
      input: review.input
    }
  };
  let result;
  try {
    result = await jsonRequest(runURL(state, `/actions/${state.pending.action}`), {
      method: 'POST',
      body: JSON.stringify(state.pending.body)
    });
  } catch (error) {
    if (error.current && !error.current.busy) {
      state.pending = null;
      state.journey = error.current;
      state.signature = '';
      state.review = null;
    }
    const status = text('button', 'Check Setup Status');
    status.type = 'button';
    status.className = 'modern-btn modern-btn-secondary';
    status.addEventListener('click', () => {
      const modal = el('addFolderModal');
      modal.addEventListener(
        'hidden.bs.modal',
        () =>
          window.dispatchEvent(
            new CustomEvent('ori:open-specialist-setup', {
              detail: { run_id: state.journey.run_id }
            })
          ),
        { once: true }
      );
      window.bootstrap.Modal.getInstance(modal)?.hide();
    });
    el('workspaceJourneyReview').appendChild(status);
    throw new Error(
      `${error.message} The project may already exist. ${state.pending ? 'Retry Confirmed Change resends your original confirmation, or check setup status.' : 'Check setup status before trying again.'}`
    );
  }
  let journey = result.setup_journey;
  let warning = '';
  const id = journey.receipts?.project_workspace_id;
  if (!id) throw new Error('Project creation needs a status check. Return to setup.');
  if (
    journey.steps.some(step => step.actions?.some(action => action.id === 'review_file_only_mode'))
  ) {
    try {
      const modeReview = await jsonRequest(runURL(state, '/actions/review_file_only_mode'), {
        method: 'POST',
        body: JSON.stringify({
          if_revision: journey.state_revision,
          idempotency_key: key(),
          input: {}
        })
      });
      const modeResult = await jsonRequest(runURL(state, '/actions/select_file_only_mode'), {
        method: 'POST',
        body: JSON.stringify({
          if_revision: modeReview.setup_journey.state_revision,
          idempotency_key: key(),
          review_token: modeReview.review.token,
          input: {}
        })
      });
      journey = modeResult.setup_journey;
    } catch (_) {
      warning =
        'The project was created. Finish its File-only setup in workspace Settings; live access is not enabled.';
    }
  }
  const folder = await jsonRequest(`/api/workspaces/${encodeURIComponent(id)}`);
  state.journey = journey;
  state.pending = null;
  state.onCreated?.(journey);
  // The existing creator continues its separately confirmed team provisioning,
  // optional OS-open request, success handling, and workspace navigation.
  return new Response(JSON.stringify({ success: true, folder, project_warning: warning }), {
    status: 201,
    headers: { 'Content-Type': 'application/json' }
  });
}

function mountProjectChoice(state) {
  const box = document.createElement('div');
  box.id = 'workspaceJourneyProjectChoice';
  box.className = 'workspace-setup-card mb-2';
  box.appendChild(text('p', `Group: ${state.groupName}`));
  const label = text('label', 'Project');
  const select = document.createElement('select');
  select.className = 'modern-input w-100';
  select.append(
    new Option('Create New Project', 'new_project'),
    new Option('Import Existing Project', 'existing_project')
  );
  select.value = state.mode;
  const pick = text('button', 'Choose Project Folder');
  pick.type = 'button';
  pick.className = 'modern-btn modern-btn-secondary';
  pick.hidden = state.mode !== 'existing_project';
  const folder = text('p', state.folderDisplay || '');
  select.addEventListener('change', () => {
    state.mode = select.value;
    pick.hidden = state.mode !== 'existing_project';
    state.entryName = '';
    refreshReview();
  });
  pick.addEventListener('click', async () => {
    pick.disabled = true;
    try {
      const selection = await jsonRequest('/api/folder-picker/select-path', {
        method: 'POST',
        body: JSON.stringify({ title: 'Choose the project folder' })
      });
      if (active !== state || !selection?.selected) return;
      if (!selection.selection_token)
        throw new Error('Choose the folder again; its selection expired.');
      state.selectionToken = selection.selection_token;
      state.entryName = '';
      state.folderDisplay = selection.path || '';
      folder.textContent = state.folderDisplay;
      refreshReview();
    } catch (error) {
      folder.textContent = error.message;
    } finally {
      pick.disabled = false;
    }
  });
  label.appendChild(select);
  box.append(label, pick, folder);
  el('wizardNameField').after(box);
  const review = document.createElement('div');
  review.id = 'workspaceJourneyReview';
  review.className = 'workspace-review-card setup-project-review';
  el('workspaceReviewSummary').after(review);
}

export async function openSetupWorkspaceCreator(journey, onCreated) {
  const manager = window.sessionManager;
  const preparation = journey.steps.find(step => step.kind === 'project_connect')?.preparation;
  if (!manager || !preparation?.exists || !preparation.acknowledged)
    throw new Error('Finish the group and preparation steps first.');
  await manager.loadFolders();
  const state = {
    journey,
    onCreated,
    homeID: preparation.group_id || journey.receipts.home_workspace_id,
    groupName: preparation.name,
    templateID: preparation.template_id,
    mode: 'new_project',
    selectionToken: '',
    entryName: '',
    generation: 0,
    signature: '',
    review: null,
    pending: null
  };
  const draft = savedDraft?.runID === journey.run_id ? savedDraft : null;
  if (draft) {
    state.mode = draft.mode;
    state.selectionToken = draft.selectionToken;
    state.entryName = draft.entryName;
    state.folderDisplay = draft.folderDisplay;
  }
  savedDraft = null;
  active = state;
  const modal = el('addFolderModal');
  modal.dataset.setupWorkspaceLaunch = 'true';
  const hidden = [];
  const labels = [];
  const openToggle = el('projectTemplateOpenAfterCreateToggle');
  const onOpenChange = event => {
    if (active === state && event.isTrusted) state.openTouched = true;
  };
  openToggle?.addEventListener('change', onOpenChange);
  const hide = node => {
    if (node) {
      hidden.push([node, node.hidden]);
      node.hidden = true;
    }
  };
  const blockCommitClose = event => {
    if (state.submitting) {
      event.preventDefault();
      const status = el('workspaceReviewError');
      status.textContent = 'Finish the confirmed workspace change before closing.';
      status.hidden = false;
    }
  };
  modal.addEventListener('hide.bs.modal', blockCommitClose);
  const onHidden = () => {
    if (modal.dataset.suspendedForAgentSetup === 'true') return;
    modal.removeEventListener('hidden.bs.modal', onHidden);
    modal.removeEventListener('hide.bs.modal', blockCommitClose);
    if (active === state) {
      if (!state.journey.receipts.project_workspace_id)
        savedDraft = {
          runID: journey.run_id,
          name: el('folderNameInput').value,
          mode: state.mode,
          selectionToken: state.selectionToken,
          entryName: state.entryName,
          folderDisplay: state.folderDisplay
        };
      active = null;
    }
    openToggle?.removeEventListener('change', onOpenChange);
    labels.forEach(([node, value]) => {
      node.textContent = value;
    });
    hidden.forEach(([node, wasHidden]) => {
      node.hidden = wasHidden;
    });
    delete modal.dataset.setupWorkspaceLaunch;
    el('workspaceJourneyProjectChoice')?.remove();
    el('workspaceJourneyReview')?.remove();
  };
  modal.addEventListener('hidden.bs.modal', onHidden);
  manager.showAddWorkspaceModal({ blueprint: state.templateID, entryPoint: 'specialist_setup' });
  mountProjectChoice(state);
  if (draft) el('folderNameInput').value = draft.name;
  for (let step = 2; step <= 4; step++) {
    const eyebrow = el(`wizardStep${step}`).querySelector('.workspace-wizard-eyebrow');
    const number = modal.querySelector(
      `.workspace-create-step[data-step="${step}"] .workspace-create-step-num`
    );
    labels.push([eyebrow, eyebrow.textContent], [number, number.textContent]);
    eyebrow.textContent = `Step ${step - 1} of 3`;
    number.textContent = String(step - 1);
  }
  hide(el('folderAdvancedDisclosure'));
  hide(el('folderDescriptionInput')?.closest('.workspace-setup-card'));
  hide(el('wizardEditBlueprintBtn'));
  const parent = el('folderParentSelect');
  if (![...parent.options].some(option => option.value === state.homeID))
    parent.add(new Option(state.groupName, state.homeID));
  parent.value = state.homeID;
  for (let i = 0; i < 60 && active === state; i++) {
    const template = window.ProjectTemplateCard?.getSelectedTemplate?.();
    if (template?.id === state.templateID && !manager.blueprintSelectionBlocked()) {
      manager.goToWizardStep(2);
      refreshReview();
      return;
    }
    await new Promise(resolve => setTimeout(resolve, 75));
  }
  if (active === state)
    reviewError(
      'The selected blueprint is unavailable. Close this dialog and check the plugin in setup.'
    );
}

function setSubmitting(submitting) {
  if (!active) return;
  active.submitting = submitting;
  const modal = el('addFolderModal');
  modal.setAttribute('aria-busy', String(submitting));
  if (submitting) {
    active.disabledControls = [...modal.querySelectorAll('button, input, select, textarea')].map(
      control => [control, control.disabled]
    );
    active.disabledControls.forEach(([control]) => {
      control.disabled = true;
    });
  } else {
    active.disabledControls?.forEach(([control, disabled]) => {
      control.disabled = disabled;
    });
    active.disabledControls = null;
  }
}

window.SetupWorkspaceCreator = {
  isActive: () => Boolean(active),
  hasPending: () => Boolean(active?.pending),
  canSubmit: () =>
    !active ||
    Boolean(
      active.review &&
      active.review.signature === JSON.stringify(inputFor(active)) &&
      (!active.pending || active.pending.signature === active.review.signature)
    ),
  refreshReview,
  submit,
  setSubmitting
};
