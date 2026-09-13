// Guided canonical-Home preparation adapter.
//
// The shared Workspace / Group creator owns all presentation. This module owns
// only the setup-journey review/commit protocol, including its exact retry
// envelope. It deliberately has no generic workspace POST fallback.
import { setupJourneyAPIRoot } from './setup-quest-links.js';

let active = null;
let retained = null;

export function groupBuildState(journey) {
  const project = journey?.steps?.find(step => step.kind === 'project_connect');
  const preparation = project?.preparation;
  if (preparation?.exists) return 'existing';
  if (journey?.busy) return 'busy';
  if (!preparation || journey?.receipts?.home_workspace_id || journey?.receipts?.project_workspace_id) {
    return 'unavailable';
  }
  return project.actions?.some(action => action.id === 'review_create_group')
    ? 'create'
    : 'unavailable';
}

async function request(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {})
    }
  });
  const body = await response.json().catch(() => null);
  if (!response.ok || !body) {
    const error = new Error(
      body?.error?.guidance ||
        (typeof body?.error === 'string' ? body.error : '') ||
        'The group result could not be confirmed.'
    );
    error.current = body?.current;
    error.definite = response.status >= 400 && response.status < 500 && response.status !== 408;
    throw error;
  }
  return body;
}

function runURL(state) {
  return `${setupJourneyAPIRoot(state.journey)}/runs/${encodeURIComponent(state.journey.run_id)}`;
}

function requestedName(state) {
  return String(document.getElementById('folderNameInput')?.value || state.name || '').trim();
}

function publish(state) {
  const manager = window.sessionManager;
  const context = manager?.workspaceCreatorContext;
  if (!context?.guided || context.guided.state !== state) return false;
  context.guided.review = state.review
    ? {
        name: state.review.input.name,
        existing: Boolean(state.review.existing),
        pending: Boolean(state.pending)
      }
    : null;
  context.guided.error = state.error || '';
  context.submitting = Boolean(state.committing);
  manager.refreshWizardChrome?.();
  return true;
}

function updateJourney(state, journey) {
  if (!journey || journey.run_id !== state.journey?.run_id) {
    throw new Error('The group setup changed. Return to setup to continue.');
  }
  state.journey = journey;
  state.onJourneyChange?.(journey);
}

function closeSharedCreator() {
  const modal = document.getElementById('addFolderModal');
  bootstrap.Modal.getInstance(modal)?.hide();
}

async function refreshSurfaces() {
  try {
    await window.sessionManager?.refreshWorkspaceSurfacesAfterGroupPreparation?.();
  } catch (error) {
    // A committed Home remains durable even when a mounted screen cannot draw
    // it. The setup owner reconciles its canonical state independently.
    console.warn('Canonical group was prepared but a workspace surface did not refresh:', error);
  }
}

async function review(state, name) {
  state.busy = true;
  state.error = '';
  publish(state);
  try {
    const current = await request(runURL(state));
    if (active !== state) return;
    updateJourney(state, current.setup_journey);
    const status = groupBuildState(state.journey);
    if (status === 'existing') {
      const preparation = state.journey.steps.find(step => step.kind === 'project_connect')?.preparation;
      state.review = {
        existing: true,
        input: { name: String(preparation?.name || name).trim() },
        revision: state.journey.state_revision,
        token: ''
      };
      return;
    }
    if (status !== 'create') {
      throw new Error(
        'The existing setup group could not be verified. Check setup status; no replacement will be built.'
      );
    }
    const payload = await request(`${runURL(state)}/actions/review_create_group`, {
      method: 'POST',
      body: JSON.stringify({
        if_revision: state.journey.state_revision,
        idempotency_key: crypto.randomUUID(),
        input: { name }
      })
    });
    if (active !== state) return;
    updateJourney(state, payload.setup_journey);
    if (
      !payload.review?.group ||
      payload.review.commit_action !== 'create_group' ||
      !payload.review.token
    ) {
      throw new Error('The exact group review is unavailable. Nothing was built by this review.');
    }
    state.review = {
      existing: false,
      input: { name: String(payload.review.group.name || name).trim() },
      revision: state.journey.state_revision,
      token: payload.review.token
    };
  } catch (error) {
    if (error.current && active === state) updateJourney(state, error.current);
    if (active === state) {
      state.review = null;
      state.error = error.message || 'The group review could not be completed.';
    }
  } finally {
    if (active === state) {
      state.busy = false;
      publish(state);
    }
  }
}

async function commit(state) {
  state.busy = true;
  state.committing = true;
  state.error = '';
  publish(state);
  try {
    if (!state.review?.existing) {
      state.pending ||= {
        if_revision: state.review.revision,
        idempotency_key: crypto.randomUUID(),
        review_token: state.review.token,
        input: state.review.input
      };
      const payload = await request(`${runURL(state)}/actions/create_group`, {
        method: 'POST',
        body: JSON.stringify(state.pending)
      });
      if (active !== state) return;
      updateJourney(state, payload.setup_journey);
      if (groupBuildState(state.journey) !== 'existing') {
        throw new Error('The confirmed group is not yet observable. Check setup status before continuing.');
      }
    }
    state.pending = null;
    state.committing = false;
    state.busy = false;
    publish(state);
    window.dispatchEvent(new CustomEvent('ori:workspaces-changed'));
    await refreshSurfaces();
    closeSharedCreator();
  } catch (error) {
    if (error.current && active === state) updateJourney(state, error.current);
    if (active !== state) return;
    const definite = error.definite && (!error.current || !error.current.busy);
    if (definite) {
      state.pending = null;
      state.review = null;
    }
    state.committing = false;
    state.busy = false;
    state.error = `${error.message || 'The group result could not be confirmed.'} ${
      definite
        ? 'Review the current group state again.'
        : 'Retry Confirmed Change resends the same approval, or check setup status.'
    }`;
    publish(state);
  }
}

async function submit(state) {
  if (state.busy || active !== state) return;
  const name = requestedName(state);
  if (!name) {
    window.sessionManager?.setWorkspaceNameError?.('Group name is required');
    document.getElementById('folderNameInput')?.focus();
    return;
  }
  if (!state.review || state.review.input.name !== name) {
    state.pending = null;
    await review(state, name);
    return;
  }
  await commit(state);
}

export function isGroupBuilderOpen() {
  return active !== null;
}

/**
 * Open the shared creator in a fixed, guided Group context. The setup journey
 * remains its sole mutation owner; ordinary parent/team/template fields never
 * reach this adapter or its reviewed actions.
 */
export function openGroupBuilder({ journey = null, onJourneyChange, onClose } = {}) {
  if (active) return false;
  if (!journey) throw new Error('Guided group preparation is unavailable. Return to setup and try again.');
  const status = groupBuildState(journey);
  if (status === 'busy') throw new Error('Group setup is already in progress. Check setup status first.');
  if (status === 'unavailable') {
    throw new Error('The existing setup group could not be verified. No replacement will be created.');
  }
  const saved = retained?.runID === journey.run_id ? retained : null;
  const existing = status === 'existing';
  const preparation = journey.steps.find(step => step.kind === 'project_connect')?.preparation;
  const state = {
    journey,
    onJourneyChange,
    onClose,
    invoker: document.activeElement,
    name: saved?.name || String(preparation?.name || journey?.journey?.workspace_launch?.group_name || ''),
    review: existing
      ? {
          existing: true,
          input: { name: String(preparation?.name || '').trim() },
          revision: journey.state_revision,
          token: ''
        }
      : saved?.review || null,
    pending: saved?.pending || null,
    error: '',
    busy: false,
    committing: false
  };
  active = state;
  const manager = window.sessionManager;
  if (!manager?.showAddWorkspaceModal) {
    active = null;
    throw new Error('Create Group is unavailable. Refresh the page and try again.');
  }
  const guided = { state, submit: () => submit(state), review: null, error: '' };
  manager.showAddWorkspaceModal({
    mode: 'guided',
    kind: 'group',
    fixedKind: 'group',
    entryPoint: 'setup_journey_group',
    invoker: state.invoker,
    drafts: { group: { name: state.name } },
    guided
  });
  // The shared controller clears generic values as it opens. Restore only the
  // supported guided name afterward; no parent, team, or ordinary-group draft
  // crosses this boundary.
  manager.restoreWorkspaceCreatorDetailsDraft?.('group');
  manager.refreshWizardChrome?.();
  const modal = document.getElementById('addFolderModal');
  const blockCommittedClose = event => {
    if (active === state && state.committing) event.preventDefault();
  };
  modal?.addEventListener('hide.bs.modal', blockCommittedClose);
  modal?.addEventListener(
    'hidden.bs.modal',
    () => {
      modal?.removeEventListener('hide.bs.modal', blockCommittedClose);
      if (active !== state) return;
      retained = {
        runID: state.journey.run_id,
        name: requestedName(state),
        pending: state.pending,
        review: state.pending ? state.review : null
      };
      active = null;
      state.onClose?.(state.journey);
      state.invoker?.focus?.();
    },
    { once: true }
  );
  publish(state);
  return true;
}
