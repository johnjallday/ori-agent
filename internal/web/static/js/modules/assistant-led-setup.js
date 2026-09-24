import {
  assistantSetupMilestoneView,
  assistantSetupMilestoneViews,
  createPresentation,
  requestedMilestones
} from './assistant-led-setup-presentation.js';

const API_ROOT = '/api/personal-assistant/setup/file-janitor';
const EXPLICIT_STATES = new Set(['needs_hq', 'provisioning_hq', 'active', 'paused']);
const PROACTIVE_STATES = new Set(['needs_hq', 'provisioning_hq', 'active']);
const SAFE_ACTIONS = new Set([
  'accept',
  'defer_recommendation',
  'choose_target',
  'choose_folder',
  'prepare_review',
  'finish_later',
  'resume',
  'review_again',
  'retry',
  'manual',
  'manual_takeover',
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

function durationLabel(seconds) {
  const value = Number(seconds) || 0;
  if (value < 60) return `${value} seconds`;
  const minutes = Math.round(value / 60);
  return `${minutes} minute${minutes === 1 ? '' : 's'}`;
}

export function assistantSetupModelLabel(role) {
  const action = role?.action === 'reuse' ? 'Reuse' : 'Create';
  const name = String(role?.name || 'File Curator');
  const model = String(role?.model || '').trim();
  return `${action} ${name} · ${model || 'Configured; chat needs a model'}`;
}

export { assistantSetupMilestoneView, assistantSetupMilestoneViews };

// Read-only refresh while the server reports preparation in flight. The timer
// only re-reads; it never decides a status.
const REFRESH_INTERVAL_MS = 1500;
const REFRESH_MAX_ATTEMPTS = 20;

export function assistantSetupNeedsRefresh(projection) {
  if (!projection) return false;
  if (projection.view_state === 'setting_up') return true;
  return (projection.milestones || []).some(
    milestone => milestone?.status === 'creating' || milestone?.status === 'reusing'
  );
}

export function safeAssistantSetupRoute(route) {
  const value = String(route || '').trim();
  if (
    !value.startsWith('/') ||
    value.includes('\\') ||
    Array.from(value).some(character => character.codePointAt(0) < 32)
  )
    return '';
  try {
    const parsed = new URL(value, 'http://ori.local');
    if (
      parsed.origin !== 'http://ori.local' ||
      parsed.hash ||
      (parsed.pathname !== '/workspaces' && !parsed.pathname.startsWith('/workspaces/'))
    ) {
      return '';
    }
    return `${parsed.pathname}${parsed.search}`;
  } catch {
    return '';
  }
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
    case 'manual_takeover':
      return {
        url: `${API_ROOT}/runs/${encodeURIComponent(run?.id || '')}/defer`,
        body: { if_version: run?.revision || 0 }
      };
    case 'resume':
      return {
        url: `${API_ROOT}/runs/${encodeURIComponent(run?.id || '')}/resume`,
        body: { if_version: run?.revision || 0 }
      };
    case 'prepare_review':
      return {
        url: `${API_ROOT}/runs/${encodeURIComponent(run?.id || '')}/prepare-review`,
        body: {
          if_version: run?.revision || 0,
          review_revision: projection?.monitoring_review?.revision || ''
        }
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
        const message =
          typeof payload?.error === 'string'
            ? payload.error
            : payload?.message ||
              payload?.error?.message ||
              `Setup request failed (${response.status})`;
        const error = new Error(String(message));
        error.code = String(payload?.code || payload?.error?.code || 'assistant_setup_unavailable');
        const details = payload?.details || payload?.error?.details || {};
        error.repair = String(details.repair || '');
        error.repairRoute = safeAssistantSetupRoute(details.conflict_route);
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
    milestones: doc.getElementById('assistantLedSetupMilestones'),
    replay: doc.getElementById('assistantLedSetupReplay'),
    refresh: doc.getElementById('assistantLedSetupRefresh'),
    details: doc.getElementById('assistantLedSetupDetails'),
    metadata: doc.getElementById('assistantLedSetupMetadata'),
    folder: doc.getElementById('assistantLedSetupFolder'),
    monitoringDisclosure: doc.getElementById('assistantLedSetupMonitoringDisclosure'),
    review: doc.getElementById('assistantLedSetupReview'),
    monitoring: doc.getElementById('assistantLedSetupMonitoring'),
    watch: doc.getElementById('assistantLedSetupWatch'),
    settling: doc.getElementById('assistantLedSetupSettling'),
    schedule: doc.getElementById('assistantLedSetupSchedule'),
    privacy: doc.getElementById('assistantLedSetupPrivacy'),
    health: doc.getElementById('assistantLedSetupHealth'),
    result: doc.getElementById('assistantLedSetupResult'),
    error: doc.getElementById('assistantLedSetupError'),
    actions: doc.getElementById('assistantLedSetupActions')
  };
  const state = {
    projection: null,
    pending: false,
    picking: false,
    explicit: false,
    relationshipState: '',
    refreshTimer: null,
    refreshAttempts: 0
  };

  function firstEnabledAction() {
    return els.actions.querySelector?.('button:not([disabled]), a[href]') || null;
  }

  // Continue moves focus to the card's Choose folder control without pressing
  // it: the native picker only opens from its own explicit click.
  function focusChooseFolder() {
    const target =
      els.actions.querySelector?.('[data-setup-action="choose_folder"]:not([disabled])') ||
      firstEnabledAction() ||
      els.title;
    target?.focus?.();
  }

  const presentation = createPresentation({
    document: doc,
    window: win,
    fetch: fetchImpl,
    canOpen: () => !state.picking,
    onContinue: focusChooseFolder,
    onClosed: invoker => {
      if (invoker?.isConnected && !invoker.disabled) invoker.focus();
      else (firstEnabledAction() || els.title)?.focus?.();
    }
  });

  function clearRefresh() {
    if (state.refreshTimer !== null) win.clearTimeout(state.refreshTimer);
    state.refreshTimer = null;
  }

  // Bounded, read-only refresh while the server reports an in-flight step. It
  // stops on a terminal state, a hidden card, or a hidden tab, and after the cap
  // hands control to a manual Refresh button.
  function scheduleRefresh() {
    clearRefresh();
    const needs = assistantSetupNeedsRefresh(state.projection);
    if (!needs) {
      state.refreshAttempts = 0;
      els.refresh.hidden = true;
      return;
    }
    if (root.hidden || doc.hidden || state.pending) return;
    if (state.refreshAttempts >= REFRESH_MAX_ATTEMPTS) {
      els.refresh.hidden = false;
      return;
    }
    els.refresh.hidden = true;
    state.refreshTimer = win.setTimeout(() => {
      state.refreshTimer = null;
      state.refreshAttempts += 1;
      void load('');
    }, REFRESH_INTERVAL_MS);
  }

  function setError(message, repairRoute = '') {
    els.error.textContent = String(message || '');
    const route = safeAssistantSetupRoute(repairRoute);
    if (els.error.textContent && route) {
      const link = doc.createElement('a');
      link.href = route;
      link.textContent = 'Open the workspace already using this folder';
      els.error.append(' ', link);
    }
    els.error.hidden = !els.error.textContent;
  }

  function actionButton(action) {
    if (!SAFE_ACTIONS.has(action?.id)) return null;
    const route = safeAssistantSetupRoute(action?.route);
    if (
      route &&
      [
        'manual',
        'open_workspace',
        'review_batch',
        'pause_monitoring',
        'manage_access',
        'history'
      ].includes(action.id)
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
    button.disabled = action?.enabled !== true || state.pending;
    if (action.id === 'choose_folder') {
      button.setAttribute('aria-describedby', 'assistantLedSetupStatus');
    }
    if (action?.reason) button.title = String(action.reason);
    button.addEventListener('click', () => void act(action.id));
    return button;
  }

  function milestoneItem(view) {
    const item = doc.createElement('li');
    item.className = 'assistant-led-setup__milestone';
    item.dataset.milestoneId = view.key;
    item.dataset.status = view.status;
    if (view.resourceID) item.dataset.resourceId = view.resourceID;
    const name = doc.createElement('span');
    name.className = 'assistant-led-setup__milestone-name';
    name.textContent = view.name;
    const chip = doc.createElement('span');
    chip.className = 'assistant-led-setup__milestone-status';
    chip.textContent = view.statusLabel;
    item.append(name, ' ', chip);
    view.details.forEach(detail => {
      const note = doc.createElement('span');
      note.className = 'assistant-led-setup__milestone-detail';
      note.textContent = detail;
      item.append(note);
    });
    return item;
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
    // The setup journey no longer starts from this card: "Show me a folder"
    // is the way into a tidy. The card remains only for a run already in
    // flight, which finishes here or from the File Janitor workspace itself.
    if (!projection.run) {
      root.hidden = true;
      state.projection = projection;
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
    els.milestones.replaceChildren();
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
      els.monitoringDisclosure.textContent = String(proposal.monitoring_disclosure || '');
      els.review.textContent = String(proposal.file_review_disclosure || '');
    } else if (run) {
      // Statuses come only from server milestones (receipts), never from the
      // plan's target_mode.
      els.target.textContent = 'Workspace and team from the reviewed setup';
      const views = assistantSetupMilestoneViews(projection.milestones);
      views.forEach(view => els.milestones.append(milestoneItem(view)));
      if (!views.length && run.team_role?.role_id) {
        const item = doc.createElement('li');
        item.textContent = assistantSetupModelLabel(run.team_role);
        els.team.append(item);
      }
      els.details.hidden = true;
    }
    els.milestones.hidden = !els.milestones.childElementCount;
    const anyReceipt = assistantSetupMilestoneViews(projection.milestones).some(
      view => view.status === 'created' || view.status === 'reused'
    );
    els.replay.hidden = !anyReceipt || !presentation;
    presentation?.update(projection.milestones);

    const monitoring = projection.monitoring_review || null;
    els.monitoring.hidden = !monitoring;
    if (monitoring) {
      els.watch.textContent = `Watches ${monitoring.watch_events.join(' and ')} events; waits ${durationLabel(monitoring.debounce_seconds)} to group activity.`;
      els.settling.textContent = `Files must remain settled for ${durationLabel(monitoring.settling_seconds)} before they can be proposed.`;
      els.schedule.textContent = `Daily catch-up: ${monitoring.daily_scan_local_time} (${monitoring.timezone}).`;
      els.privacy.textContent = `${String(monitoring.privacy_mode).replaceAll('_', ' ')}; no file content is read or sent to a model.`;
    }
    const health = projection.health || null;
    els.health.hidden = !health;
    if (health) {
      const monitoringState = health.monitoring_active
        ? 'Monitoring active'
        : health.paused
          ? 'Monitoring paused'
          : 'Monitoring needs attention';
      els.health.textContent = `${monitoringState} · ${String(health.privacy_mode || 'privacy unknown').replaceAll('_', ' ')}`;
    }
    const firstResult = projection.first_result || null;
    els.result.hidden = !firstResult;
    if (firstResult) {
      const completed = new Date(firstResult.completed_at || '');
      const completedLabel = Number.isNaN(completed.getTime())
        ? ''
        : ` Completed ${completed.toLocaleString()}.`;
      els.result.textContent =
        (firstResult.outcome === 'batch'
          ? `${firstResult.eligible_count} proposal${firstResult.eligible_count === 1 ? '' : 's'} ready; ${firstResult.ineligible_count} item${firstResult.ineligible_count === 1 ? '' : 's'} excluded or still settling.`
          : `No new eligible files; ${firstResult.ineligible_count} item${firstResult.ineligible_count === 1 ? '' : 's'} excluded or still settling.`) +
        completedLabel;
    }

    els.actions.replaceChildren();
    (projection.actions || []).forEach(action => {
      const control = actionButton(action);
      if (control) els.actions.append(control);
    });
    if (announce) setError('');
    if (projection.view_state === 'recommendation_deferred' && !state.explicit) {
      root.hidden = true;
      scheduleRefresh();
      return;
    }
    scheduleRefresh();
    // The modal presentation owns focus while it is open.
    if (presentation?.isOpen()) return;
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

  async function chooseFolder() {
    if (state.pending || !state.projection?.run) return false;
    const run = state.projection.run;
    const workspaceID = run.target_workspace_id;
    // Never stack the read-only presentation over the native folder picker.
    state.picking = true;
    presentation?.close({ restoreFocus: false });
    const trigger = doc.activeElement;
    state.pending = true;
    setError('');
    render(state.projection, { announce: false });
    try {
      const intentResponse = await fetchImpl(
        `${API_ROOT}/runs/${encodeURIComponent(run.id)}/folder-intent`,
        {
          method: 'POST',
          headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
          body: JSON.stringify({ if_version: run.revision })
        }
      );
      const intent = await responsePayload(intentResponse);
      const setupToken = String(intent.assistant_setup_token || '');
      if (!setupToken) throw new Error('Folder permission could not be prepared.');
      const pickerResponse = await fetchImpl('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workspace_id: workspaceID,
          title: 'Choose the folder File Janitor may tidy'
        })
      });
      const picked = await responsePayload(pickerResponse);
      if (!picked.success) throw new Error(picked.error || 'The folder picker is unavailable.');
      if (trigger?.isConnected) trigger.focus();
      if (!picked.selected) {
        state.projection = unwrapSetup(intent) || state.projection;
        return false;
      }
      const selectionToken = String(picked.selection_token || '');
      if (!selectionToken) throw new Error('The trusted folder selection is unavailable.');
      const grantResponse = await fetchImpl(
        `/api/workspaces/${encodeURIComponent(workspaceID)}/file-janitor/setup`,
        {
          method: 'POST',
          headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
          body: JSON.stringify({
            selection_token: selectionToken,
            assistant_setup_token: setupToken,
            paused: true
          })
        }
      );
      const granted = await responsePayload(grantResponse);
      render(unwrapSetup(granted));
      return true;
    } catch (error) {
      if (error.setup) render(error.setup, { announce: false });
      setError(
        error.message || 'Folder permission could not be completed. Nothing else was changed.',
        error.repairRoute
      );
      if (trigger?.isConnected) trigger.focus();
      return false;
    } finally {
      state.pending = false;
      state.picking = false;
      render(state.projection, { announce: false });
    }
  }

  async function act(actionID) {
    if (state.pending) return false;
    if (actionID === 'choose_folder') return chooseFolder();
    // Reviewing the updated plan is a read: it re-loads the fresh proposal.
    if (actionID === 'review_again') return Boolean(await load('', { focus: true }));
    const request = assistantSetupRequest(actionID, state.projection);
    if (!request) return false;
    const accepting = actionID === 'accept';
    if (accepting) {
      // Show the reviewed values at once; statuses arrive only from the server.
      presentation?.open(requestedMilestones(state.projection?.proposal), {
        invoker: doc.activeElement
      });
    }
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
      if (actionID === 'manual_takeover') {
        const route = safeAssistantSetupRoute(state.projection?.target?.route);
        if (route && typeof win.location?.assign === 'function') win.location.assign(route);
      }
      return true;
    } catch (error) {
      // A stopped accept carries the stopped projection: the walkthrough stays
      // open and stops at that step. A refusal with no run has nothing to show.
      if (error.setup) render(error.setup, { announce: false });
      if (accepting && !assistantSetupMilestoneViews(error.setup?.milestones).length) {
        presentation?.close();
      }
      setError(error.message || 'Setup could not continue. Nothing else was changed.');
      return false;
    } finally {
      state.pending = false;
      render(state.projection, { announce: false });
    }
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

  els.replay?.addEventListener('click', () => {
    presentation?.open(state.projection?.milestones, { invoker: els.replay });
  });
  els.refresh?.addEventListener('click', () => {
    state.refreshAttempts = 0;
    els.refresh.hidden = true;
    void load('');
  });
  doc.addEventListener('visibilitychange', () => {
    if (!doc.hidden) scheduleRefresh();
    else clearRefresh();
  });
  doc.addEventListener('personal-assistant:status', onRelationship);
  return { load, act, render, state, els, presentation };
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
