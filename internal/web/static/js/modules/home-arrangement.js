import { mountArrangementConsent } from './workspace-arrangement-consent.js';
import {
  arrangementID,
  arrangementPresentation,
  renderArrangementHistory
} from './workspace-arrangement.js';

export function homeArrangementFailure(error, { readOnly = false } = {}) {
  const code = error?.payload?.arrangement_error?.code;
  const messages = {
    upgrade_required:
      'Saving this turn needs explicit library-upgrade consent. Refresh the planning context and review the compatibility notice.',
    model_unavailable:
      'The bound Manager needs a supported API model. No provider was switched; manual setup remains available.',
    busy: 'A planning turn is already running. Wait before requesting another.',
    stale_context:
      'The Home, catalog or runtime changed. Reopen saved history and refresh the planning context.',
    cancelled: 'Planning was cancelled. Reopen saved history before sending again.',
    timed_out: 'Planning timed out. Reopen saved history, then consider a narrower goal.',
    limit_reached:
      'A planning or storage limit was reached. Keep your goal and narrow the request.',
    invalid_result: 'The result could not be accepted. No unsaved proposal is offered.',
    unavailable:
      'Planning prerequisites are unavailable. Reopen this Home and check its installed package, bound Manager and trusted skill/Toolbox access. Saved history and manual setup remain available; nothing is initialized, trusted or installed automatically.'
  };
  return Object.hasOwn(messages, code)
    ? messages[code]
    : readOnly
      ? 'The current planning context could not be checked. No model was called or planning record saved. Reopen this Home and try again.'
      : 'The request or its canonical save could not be confirmed. Reopen saved history before sending again.';
}

export function homeArrangementContext(homeID, context, goal, active = true) {
  const valid =
    active && context?.home_id === homeID && /^[a-f0-9]{64}$/.test(context?.context_pin || '');
  return {
    kind: 'home_library',
    homeID,
    goal,
    available: Boolean(valid),
    expectedRevision: context?.revision,
    libraryVersion: context?.schema_version,
    agentName: context?.agent_name,
    providerName: context?.provider,
    modelName: context?.model,
    contextKey: valid ? context.context_pin : ''
  };
}

// The request callback is bound to the exact Home by its owner. This never uses
// global/system-model settings or a PA identity to impersonate the Manager.
export function mountHomeArrangementPlanner(
  container,
  { homeID, request, isCurrent, onSaved = async () => {} }
) {
  const document = container.ownerDocument;
  const node = (tag, text = '', className = '') => {
    const element = document.createElement(tag);
    element.textContent = text;
    element.className = className;
    return element;
  };
  const details = node('details');
  details.append(node('summary', 'Organize library'));
  details.append(
    node(
      'p',
      'Plan using saved catalog metadata. No scan or model call runs when this panel opens; saving a plan does not apply it.',
      'small'
    )
  );
  const label = node('label', 'Organizing goal', 'd-block');
  const goal = node('input', '', 'form-control my-2');
  goal.type = 'text';
  goal.maxLength = 2000;
  label.append(goal);
  const refresh = node('button', 'Refresh planning context', 'btn btn-sm btn-outline-secondary');
  refresh.type = 'button';
  const history = node('button', 'Reopen saved history before retrying', 'btn btn-sm btn-link');
  history.type = 'button';
  const status = node('p', '', 'small text-body-secondary');
  status.setAttribute('role', 'status');
  const consentBody = node('div');
  const resultBody = node('div');
  details.append(label, refresh, history, status, consentBody, resultBody);
  container.append(details);
  let context = null;
  let generation = 0;
  let pending = false;
  const current = () => isCurrent() && container.isConnected !== false;
  const readContext = async () => {
    if (pending || !current()) return;
    const run = ++generation;
    context = null;
    consent.refresh();
    refresh.disabled = true;
    status.textContent = 'Checking the exact Home Manager and saved catalog…';
    try {
      const next = await request('/arrangements/context', { cache: 'no-store' });
      if (run !== generation || !current()) return;
      if (next?.home_id !== homeID || !/^[a-f0-9]{64}$/.test(next?.context_pin || ''))
        throw new Error('Invalid context');
      context = next;
      status.textContent =
        'Context checked. Review sharing and any storage-upgrade choice before Send.';
    } catch (error) {
      if (run === generation && current())
        status.textContent = homeArrangementFailure(error, { readOnly: true });
    } finally {
      if (run === generation) {
        refresh.disabled = false;
        consent.refresh();
      }
    }
  };
  const consent = mountArrangementConsent(consentBody, {
    kind: 'home_library',
    enabled: true,
    getContext: () => homeArrangementContext(homeID, context, goal.value.trim(), current()),
    onSend: async body => {
      if (!current() || pending || !context) throw new Error('Not ready');
      const run = generation;
      const pin = context.context_pin;
      pending = true;
      refresh.disabled = true;
      history.disabled = true;
      status.textContent = 'Investigating saved metadata. No arrangement has been applied.';
      try {
        const result = await request('/arrangements/plan', {
          method: 'POST',
          body: JSON.stringify({ ...body, expected_context: pin })
        });
        if (run !== generation || !current()) throw new Error('Context changed');
        const view = arrangementPresentation(result);
        if (!view || view.goal !== body.goal) throw new Error('Invalid canonical outcome');
        renderArrangementHistory(resultBody, result);
        status.textContent =
          'Saved to canonical Home history. Nothing was applied. Refresh the context before requesting another plan.';
        context = null; // Its successful save advanced the catalog revision.
        await onSaved();
        if (run !== generation || !current()) throw new Error('Context changed');
        return { canonicalSaved: true };
      } catch (error) {
        if (run === generation && current()) status.textContent = homeArrangementFailure(error);
        throw new Error('Planning outcome requires canonical history');
      } finally {
        pending = false;
        refresh.disabled = false;
        history.disabled = false;
      }
    }
  });
  goal.addEventListener('input', () => consent.refresh());
  refresh.addEventListener('click', () => void readContext());
  details.addEventListener('toggle', () => {
    if (details.open) void readContext();
    else consent.clearChoices();
  });
  history.addEventListener('click', async () => {
    if (pending || !current()) return;
    const run = generation;
    try {
      const saved = await request('/arrangements', { cache: 'no-store' });
      if (run !== generation || !current()) return;
      if (
        !Array.isArray(saved.arrangements) ||
        saved.arrangements.length > 32 ||
        !saved.arrangements.every(ref => arrangementID(ref.id) && ref.review_available === false)
      )
        throw new Error('Invalid history');
      await onSaved();
      if (!current() || run !== generation) return;
      consent.historyReopened();
      await readContext();
    } catch (_) {
      if (current() && run === generation)
        status.textContent =
          'Saved history could not be reopened. Your goal is kept; no retry was sent.';
    }
  });
  return {
    invalidate: () => {
      generation++;
      context = null;
      if (!pending) refresh.disabled = false;
      consent.refresh();
    }
  };
}
