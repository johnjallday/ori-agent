const API_ROOT = '/api/personal-assistant/setup/file-janitor';
const EXPLICIT_STATES = new Set(['needs_hq', 'provisioning_hq', 'active', 'paused']);
const PROACTIVE_STATES = new Set(['needs_hq', 'provisioning_hq', 'active']);
const SAFE_ACTIONS = new Set([
  'accept',
  'defer_recommendation',
  'choose_target',
  'choose_folder',
  'finish_later',
  'resume',
  'retry',
  'manual',
  'open_workspace',
  'review_batch',
  'pause_monitoring',
  'manage_access',
  'history'
]);

export function assistantSetupEligible(state, { explicit = false } = {}) {
  const normalized = String(state || '');
  return explicit ? EXPLICIT_STATES.has(normalized) : PROACTIVE_STATES.has(normalized);
}

export function assistantSetupModelLabel(role) {
  const action = role?.action === 'reuse' ? 'Reuse' : 'Create';
  const name = String(role?.name || 'File Curator');
  const model = String(role?.model || '').trim();
  return `${action} ${name} · ${model || 'Configured; chat needs a model'}`;
}

export function safeAssistantSetupRoute(route) {
  const value = String(route || '').trim();
  if (
    value.startsWith('/workspaces/') ||
    value.startsWith('/workspaces?') ||
    value === '/workspaces'
  ) {
    return value;
  }
  return '';
}

export function assistantSetupRequest(action, projection) {
  const proposal = projection?.proposal || null;
  const run = projection?.run || null;
  switch (action) {
    case 'accept':
      return {
        url: `${API_ROOT}/accept`,
        body: {
          proposal_revision: proposal?.revision || '',
          ...(proposal?.selected_workspace_id
            ? { target_workspace_id: proposal.selected_workspace_id }
            : {})
        }
      };
    case 'defer_recommendation':
      return { url: `${API_ROOT}/defer`, body: { proposal_revision: proposal?.revision || '' } };
    case 'finish_later':
      return {
        url: `${API_ROOT}/runs/${encodeURIComponent(run?.id || '')}/defer`,
        body: { if_version: run?.revision || 0 }
      };
    case 'resume':
      return {
        url: `${API_ROOT}/runs/${encodeURIComponent(run?.id || '')}/resume`,
        body: { if_version: run?.revision || 0 }
      };
    default:
      return null;
  }
}

function unwrapSetup(payload) {
  if (!payload || typeof payload !== 'object') return null;
  return payload.setup || payload.data?.setup || null;
}

function responsePayload(response) {
  return response
    .json()
    .catch(() => ({}))
    .then(payload => {
      if (!response.ok) {
        const error = new Error(
          String(payload?.error || `Setup request failed (${response.status})`)
        );
        error.code = String(payload?.code || 'assistant_setup_unavailable');
        error.setup = unwrapSetup(payload);
        throw error;
      }
      return payload;
    });
}

function createController({ document: doc, fetch: fetchImpl, window: win }) {
  const root = doc?.getElementById?.('assistantLedSetup');
  if (!root || typeof fetchImpl !== 'function') return null;
  const els = {
    root,
    title: doc.getElementById('assistantLedSetupTitle'),
    state: doc.getElementById('assistantLedSetupState'),
    identity: doc.getElementById('assistantLedSetupIdentity'),
    status: doc.getElementById('assistantLedSetupStatus'),
    choices: doc.getElementById('assistantLedSetupChoices'),
    plan: doc.getElementById('assistantLedSetupPlan'),
    target: doc.getElementById('assistantLedSetupTarget'),
    team: doc.getElementById('assistantLedSetupTeam'),
    details: doc.getElementById('assistantLedSetupDetails'),
    metadata: doc.getElementById('assistantLedSetupMetadata'),
    folder: doc.getElementById('assistantLedSetupFolder'),
    monitoring: doc.getElementById('assistantLedSetupMonitoring'),
    review: doc.getElementById('assistantLedSetupReview'),
    error: doc.getElementById('assistantLedSetupError'),
    actions: doc.getElementById('assistantLedSetupActions')
  };
  const state = { projection: null, pending: false, explicit: false, relationshipState: '' };

  function setError(message) {
    els.error.textContent = String(message || '');
    els.error.hidden = !els.error.textContent;
  }

  function actionButton(action) {
    if (!SAFE_ACTIONS.has(action?.id)) return null;
    const route = safeAssistantSetupRoute(action?.route);
    if (
      route &&
      ['manual', 'open_workspace', 'review_batch', 'manage_access', 'history'].includes(action.id)
    ) {
      const link = doc.createElement('a');
      link.className = 'assistant-led-setup__secondary';
      link.href = route;
      link.textContent = String(action.label || 'Open');
      link.dataset.setupAction = action.id;
      if (!action.enabled) {
        link.setAttribute('aria-disabled', 'true');
        link.removeAttribute('href');
      }
      return link;
    }
    const button = doc.createElement('button');
    button.type = 'button';
    button.className =
      action.id === 'accept' ? 'assistant-led-setup__primary' : 'assistant-led-setup__secondary';
    button.textContent = String(action?.label || action.id);
    button.dataset.setupAction = action.id;
    button.disabled = action?.enabled !== true || state.pending || action.id === 'choose_folder';
    if (action.id === 'choose_folder') {
      button.title = 'Folder permission is the next step.';
      button.setAttribute('aria-describedby', 'assistantLedSetupStatus');
    }
    if (action?.reason) button.title = String(action.reason);
    button.addEventListener('click', () => void act(action.id));
    return button;
  }

  function renderChoices(proposal) {
    els.choices.replaceChildren();
    const choose = proposal?.mode === 'choose' && Array.isArray(proposal.targets);
    els.choices.hidden = !choose;
    if (!choose) return;
    proposal.targets.forEach(target => {
      const button = doc.createElement('button');
      button.type = 'button';
      button.className = 'assistant-led-setup__choice';
      button.textContent = `${String(target?.name || 'File Janitor workspace')}${target?.supported ? '' : ' · Manual review required'}`;
      button.dataset.workspaceId = String(target?.workspace_id || '');
      button.addEventListener(
        'click',
        () => void load(button.dataset.workspaceId, { focus: true })
      );
      els.choices.append(button);
    });
  }

  function render(projection, { announce = true, focus = false } = {}) {
    if (!projection || typeof projection !== 'object') {
      root.hidden = true;
      state.projection = null;
      return;
    }
    const focusedAction = doc.activeElement?.dataset?.setupAction || '';
    state.projection = projection;
    root.hidden = false;
    root.dataset.viewState = String(projection.view_state || 'unavailable');
    els.state.textContent = String(projection.view_state || '').replaceAll('_', ' ');
    const relationship = projection.relationship || {};
    els.identity.textContent = relationship.display_name
      ? `${relationship.display_name} will guide this setup.`
      : 'Your personal assistant will guide this setup.';
    els.status.textContent = String(projection.status_message || '');

    const proposal = projection.proposal || null;
    const run = projection.run || null;
    renderChoices(proposal);
    els.plan.hidden = !proposal && !run;
    els.team.replaceChildren();
    if (proposal) {
      if (proposal.mode === 'create') {
        els.target.textContent = 'Workspace: create one File Janitor workspace after this review.';
      } else if (proposal.mode === 'adopt') {
        const target = proposal.targets?.[0];
        els.target.textContent = `Workspace: continue ${String(target?.name || 'the selected File Janitor workspace')} without changing it by previewing.`;
      } else {
        els.target.textContent = 'Workspace: choose one of your File Janitor workspaces.';
      }
      (proposal.team || []).forEach(role => {
        const item = doc.createElement('li');
        item.textContent = assistantSetupModelLabel(role);
        els.team.append(item);
      });
      els.details.hidden = false;
      els.metadata.textContent = String(proposal.metadata_disclosure || '');
      els.folder.textContent = String(proposal.folder_disclosure || '');
      els.monitoring.textContent = String(proposal.monitoring_disclosure || '');
      els.review.textContent = String(proposal.file_review_disclosure || '');
    } else if (run) {
      const targetName = projection.target?.name || 'File Janitor';
      const targetMode = run.target_mode === 'create' ? 'created' : 'reused';
      els.target.textContent = `Workspace: ${targetName} ${targetMode} from the reviewed setup.`;
      if (run.team_role?.role_id) {
        const item = doc.createElement('li');
        item.textContent = assistantSetupModelLabel(run.team_role);
        els.team.append(item);
      }
      els.details.hidden = true;
    }

    els.actions.replaceChildren();
    (projection.actions || []).forEach(action => {
      const control = actionButton(action);
      if (control) els.actions.append(control);
    });
    if (announce) setError('');
    if (projection.view_state === 'recommendation_deferred' && !state.explicit) {
      root.hidden = true;
      return;
    }
    const replacement = focusedAction
      ? els.actions.querySelector?.(`[data-setup-action="${focusedAction}"]`)
      : null;
    const nextAction = focusedAction
      ? els.actions.querySelector?.('button:not([disabled]), a[href]')
      : null;
    if (replacement && !replacement.disabled) replacement.focus();
    else if (nextAction) nextAction.focus();
    else if (focus) els.title?.focus();
  }

  async function load(targetWorkspaceID = '', { focus = false } = {}) {
    const query = targetWorkspaceID
      ? `?target_workspace_id=${encodeURIComponent(targetWorkspaceID)}`
      : '';
    try {
      const response = await fetchImpl(`${API_ROOT}${query}`, {
        headers: { Accept: 'application/json' }
      });
      const payload = await responsePayload(response);
      render(unwrapSetup(payload), { focus });
      return state.projection;
    } catch (error) {
      if (error.setup) render(error.setup, { announce: false, focus });
      if (
        state.explicit ||
        state.projection ||
        assistantSetupEligible(state.relationshipState, { explicit: false })
      ) {
        root.hidden = false;
        root.dataset.viewState = 'unavailable';
        els.state.textContent = 'unavailable';
        els.status.textContent = 'File Janitor setup could not be refreshed. Nothing was changed.';
        setError(error.message || 'Assistant setup is unavailable.');
      }
      return null;
    }
  }

  async function act(actionID) {
    if (state.pending) return false;
    const request = assistantSetupRequest(actionID, state.projection);
    if (!request) return false;
    state.pending = true;
    setError('');
    render(state.projection, { announce: false });
    try {
      const response = await fetchImpl(request.url, {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify(request.body)
      });
      const payload = await responsePayload(response);
      const previousHadRun = Boolean(state.projection?.run);
      render(unwrapSetup(payload));
      if (!previousHadRun && state.projection?.run) {
        win.dispatchEvent?.(new win.CustomEvent('ori:workspaces-changed'));
      }
      return true;
    } catch (error) {
      if (error.setup) render(error.setup, { announce: false });
      setError(error.message || 'Setup could not continue. Nothing else was changed.');
      return false;
    } finally {
      state.pending = false;
      render(state.projection, { announce: false });
    }
  }

  async function startFromMission() {
    state.explicit = true;
    const panel = win.PersonalAssistantPanel;
    const onHome = Boolean(doc.getElementById('personalAssistantTodayPanel'));
    panel?.open?.(doc.getElementById('personalAssistantLauncher'), {
      view: onHome ? 'today' : 'ask',
      focusTab: false,
      focusComposer: false
    });
    await load('', { focus: true });
    return !root.hidden;
  }

  function onRelationship(event) {
    const personalAssistant = event?.detail?.personalAssistant || null;
    state.relationshipState = String(personalAssistant?.state || '');
    if (!assistantSetupEligible(state.relationshipState, { explicit: state.explicit })) {
      root.hidden = true;
      return;
    }
    void load('', { focus: false });
  }

  doc.addEventListener('personal-assistant:status', onRelationship);
  return { load, act, render, startFromMission, state, els };
}

let controller = null;

export function initAssistantLedSetup(options = {}) {
  if (controller) return controller;
  const doc = options.document || globalThis.document;
  const win = options.window || globalThis.window;
  const fetchImpl = options.fetch || globalThis.fetch;
  if (!doc || !win) return null;
  controller = createController({ document: doc, fetch: fetchImpl, window: win });
  if (controller) win.AssistantLedSetup = controller;
  return controller;
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => initAssistantLedSetup());
  } else {
    initAssistantLedSetup();
  }
}
