// One map/group surface. A setup context uses only its canonical reviewed owner;
// it must never fall back to the generic group POST. Drafts/consent stay in memory.
const ROOT = '/api/personal-assistant/setup-journey';
let active = null;
let initialized = false;
let retained = null;
const el = id => document.getElementById(id);
const key = () => crypto.randomUUID();

export function groupBuildState(journey) {
  const project = journey?.steps?.find(step => step.kind === 'project_connect');
  const preparation = project?.preparation;
  if (preparation?.exists) return 'existing';
  if (journey?.busy) return 'busy';
  if (
    !preparation ||
    journey?.receipts?.home_workspace_id ||
    journey?.receipts?.project_workspace_id
  )
    return 'unavailable';
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
const runURL = state => `${ROOT}/runs/${encodeURIComponent(state.journey.run_id)}`;
function message(value = '') {
  el('buildGroupError').textContent = value;
  el('buildGroupError').hidden = !value;
}
function render() {
  const state = active;
  if (!state) return;
  const existing = state.journey && groupBuildState(state.journey) === 'existing';
  const reviewed = Boolean(state.review);
  el('buildGroupContext').textContent = state.journey
    ? `${state.journey.journey.workspace_launch.group_title}. This is the setup's canonical group, not an additional wrapper.`
    : 'An empty group for related workspaces. This does not create or replace a specialist setup group.';
  el('buildGroupForm').hidden = reviewed || existing || state.uncertain;
  el('buildGroupReviewPanel').hidden = !reviewed && !existing;
  if (existing) {
    const preparation = state.journey.steps.find(
      step => step.kind === 'project_connect'
    ).preparation;
    el('buildGroupReviewTitle').textContent = `Use “${preparation.name}”`;
    el('buildGroupReviewDescription').textContent =
      'Your existing group will be reused unchanged. No duplicate or rename is needed.';
  } else if (reviewed) {
    el('buildGroupReviewTitle').textContent = `Build “${state.review.name}”?`;
    el('buildGroupReviewDescription').textContent =
      'Create one empty group on the map. Existing workspaces stay where they are; teams and access are separate decisions.';
  }
  el('buildGroupBack').hidden = !reviewed || Boolean(state.pending);
  el('buildGroupCommit').hidden = (!reviewed && !existing) || (state.uncertain && !state.pending);
  el('buildGroupCommit').textContent = existing
    ? 'Use This Group'
    : state.pending
      ? 'Retry Confirmed Change'
      : 'Build Group';
  el('buildGroupCheck').hidden = !state.journey || (!state.pending && !state.uncertain);
  el('buildGroupCancel').textContent =
    state.uncertain && !state.journey ? 'Close and Check Map' : 'Cancel';
  el('buildGroupModal').setAttribute('aria-busy', String(state.busy));
  el('buildGroupModal')
    .querySelectorAll('button, input')
    .forEach(control => {
      control.disabled =
        state.busy &&
        (state.committing || !['buildGroupClose', 'buildGroupCancel'].includes(control.id));
    });
}
function updateJourney(state, journey) {
  if (!journey || journey.run_id !== state.journey?.run_id)
    throw new Error('The group setup changed. Return to setup to continue.');
  state.journey = journey;
  state.onJourneyChange?.(journey);
}
async function review() {
  const state = active;
  if (!state || state.busy || !el('buildGroupForm').reportValidity()) return;
  const name = el('buildGroupName').value.trim();
  if (!name) return;
  state.name = name;
  state.busy = true;
  message();
  render();
  try {
    if (state.journey) {
      const current = await request(runURL(state));
      if (active !== state) return;
      updateJourney(state, current.setup_journey);
      const status = groupBuildState(state.journey);
      if (status === 'existing') return;
      if (status !== 'create')
        throw new Error(
          'The existing setup group could not be verified. Check setup status; no replacement will be built.'
        );
      const input = { name };
      const payload = await request(`${runURL(state)}/actions/review_create_group`, {
        method: 'POST',
        body: JSON.stringify({
          if_revision: state.journey.state_revision,
          idempotency_key: key(),
          input
        })
      });
      if (active !== state) return;
      if (
        !payload.review?.group ||
        payload.review.commit_action !== 'create_group' ||
        !payload.review.token
      )
        throw new Error('The exact group review is unavailable. Nothing was built by this review.');
      state.review = {
        name: payload.review.group.name,
        token: payload.review.token,
        input,
        revision: state.journey.state_revision
      };
    } else state.review = { name };
  } catch (error) {
    if (active === state) message(error.message);
  } finally {
    if (active === state) {
      state.busy = false;
      render();
      if (state.review) el('buildGroupReviewTitle').focus();
    }
  }
}
async function commit() {
  const state = active;
  if (!state || state.busy) return;
  if (state.journey && groupBuildState(state.journey) === 'existing' && !state.pending) {
    close();
    return;
  }
  if (!state.review) return;
  state.busy = state.committing = true;
  message();
  render();
  let succeeded = false;
  try {
    if (state.journey) {
      // This envelope is created once, from the visible review, and retained for
      // uncertain retries. Never fetch replacement consent under the same click.
      state.pending ||= {
        if_revision: state.review.revision,
        idempotency_key: key(),
        review_token: state.review.token,
        input: state.review.input
      };
      const payload = await request(`${runURL(state)}/actions/create_group`, {
        method: 'POST',
        body: JSON.stringify(state.pending)
      });
      updateJourney(state, payload.setup_journey);
      if (groupBuildState(state.journey) !== 'existing')
        throw new Error(
          'The confirmed group is not yet observable. Check setup status before continuing.'
        );
    } else {
      const payload = await request('/api/workspaces', {
        method: 'POST',
        body: JSON.stringify({
          name: state.review.name,
          kind: 'group',
          create_template_agents: false
        })
      });
      if (!payload.folder?.id) throw new Error('The group result could not be confirmed.');
    }
    state.pending = null;
    state.uncertain = false;
    succeeded = true;
    window.dispatchEvent(new CustomEvent('ori:workspaces-changed'));
  } catch (error) {
    if (error.current && state.journey) updateJourney(state, error.current);
    const definite = error.definite && (!state.journey || (error.current && !error.current.busy));
    state.uncertain = !definite;
    if (definite) {
      state.pending = null;
      state.review = null;
    }
    message(
      `${error.message} ${state.uncertain ? (state.journey ? 'Retry Confirmed Change resends the same approval, or check setup status.' : 'Check the map before building another group; the first request may have succeeded.') : 'Your name is kept. Review the current group state again.'}`
    );
  } finally {
    state.busy = state.committing = false;
    render();
  }
  if (succeeded) close();
}
async function check() {
  const state = active;
  if (!state?.journey || state.busy) return;
  state.busy = true;
  render();
  message();
  try {
    const payload = await request(runURL(state));
    if (active !== state) return;
    updateJourney(state, payload.setup_journey);
    if (!state.journey.busy) {
      state.pending = null;
      state.review = null;
      state.uncertain = false;
    }
    if (groupBuildState(state.journey) !== 'existing')
      message(
        'The group is not confirmed yet. This status check did not build or replace anything.'
      );
  } catch (error) {
    if (active === state) message(error.message);
  } finally {
    if (active === state) {
      state.busy = false;
      render();
    }
  }
}
function close() {
  if (!active || active.committing) {
    el('buildGroupStatus').textContent = 'Finish the confirmed group change before closing.';
    return;
  }
  active.modal.hide();
}
function initialize() {
  if (initialized) return;
  const root = el('buildGroupModal');
  if (!root) throw new Error('The shared group builder is unavailable.');
  initialized = true;
  // Same stacking-context boundary as the shared workspace creator.
  document.body.appendChild(root);
  el('buildGroupForm').addEventListener('submit', event => {
    event.preventDefault();
    void review();
  });
  el('buildGroupCommit').addEventListener('click', () => void commit());
  el('buildGroupCheck').addEventListener('click', () => void check());
  ['buildGroupClose', 'buildGroupCancel'].forEach(id => el(id).addEventListener('click', close));
  el('buildGroupBack').addEventListener('click', () => {
    if (!active || active.busy || active.pending) return;
    active.review = null;
    message();
    render();
    el('buildGroupName').focus();
  });
  root.addEventListener('hide.bs.modal', event => {
    if (active?.committing) {
      event.preventDefault();
      el('buildGroupStatus').textContent = 'Finish the confirmed group change before closing.';
    }
  });
  root.addEventListener('hidden.bs.modal', () => {
    const state = active;
    if (!state) return;
    // Cancellation discards ordinary review consent. Only an already-submitted,
    // uncertain operation retains its original envelope for an exact retry.
    retained = state.journey
      ? {
          runID: state.journey.run_id,
          name: el('buildGroupName').value,
          pending: state.pending,
          review: state.pending ? state.review : null
        }
      : null;
    active = null;
    if (!state.journey && state.uncertain)
      window.dispatchEvent(new CustomEvent('ori:workspaces-changed'));
    state.onClose?.(state.journey);
    state.invoker?.focus?.();
  });
  window.addEventListener(
    'keydown',
    event => {
      if (event.key !== 'Escape' || !active || !root.classList.contains('show')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      close();
    },
    true
  );
}
export function isGroupBuilderOpen() {
  return active !== null;
}

export function openGroupBuilder({ journey = null, onJourneyChange, onClose } = {}) {
  if (active) return false;
  initialize();
  const saved = journey && retained?.runID === journey.run_id ? retained : null;
  active = {
    journey,
    onJourneyChange,
    onClose,
    invoker: document.activeElement,
    name: saved?.name || journey?.journey?.workspace_launch?.group_name || '',
    pending: saved?.pending || null,
    review: saved?.review || null,
    uncertain: Boolean(saved?.pending),
    busy: false,
    committing: false,
    modal: bootstrap.Modal.getOrCreateInstance(el('buildGroupModal'))
  };
  el('buildGroupName').value = active.name;
  el('buildGroupStatus').textContent = '';
  message();
  render();
  el('buildGroupModal').addEventListener(
    'shown.bs.modal',
    () => {
      (el('buildGroupForm').hidden ? el('buildGroupCommit') : el('buildGroupName')).focus();
    },
    { once: true }
  );
  active.modal.show();
  return true;
}
