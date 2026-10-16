import { mountArrangementConsent } from './workspace-arrangement-consent.js';
import {
  arrangementID,
  arrangementPresentation,
  renderArrangementHistory
} from './workspace-arrangement.js';

// The server decides these refusals before its single canonical save, so
// nothing was saved. A cancellation, timeout, lost response or any other
// failure may have raced a save and still needs canonical history.
const DEFINITE_REFUSALS = new Set([
  'upgrade_required',
  'model_unavailable',
  'busy',
  'stale_context',
  'limit_reached',
  'invalid_result',
  'unavailable'
]);

export function homeArrangementRefusalIsDefinite(error) {
  return error?.status === 409 && DEFINITE_REFUSALS.has(error?.payload?.arrangement_error?.code);
}

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
      'Plan using saved catalog metadata. Revisions also share the current saved proposal and pending question with the disclosed model. No scan or model call runs when this panel opens; saving a plan does not apply it.',
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
  const targetLabel = node('p', 'New proposal', 'small');
  const newProposal = node('button', 'Start a new proposal', 'btn btn-sm btn-link');
  newProposal.type = 'button';
  details.append(
    label,
    targetLabel,
    newProposal,
    refresh,
    history,
    status,
    consentBody,
    resultBody
  );
  container.append(details);
  let target = null;
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
    getContext: () => {
      const view = homeArrangementContext(homeID, context, goal.value.trim(), current());
      if (target) view.contextKey += `:${target.id}:${target.revision}`;
      return view;
    },
    onSend: async body => {
      if (!current() || pending || !context) throw new Error('Not ready');
      const run = generation;
      const pin = context.context_pin;
      const revising = target ? { ...target } : null;
      pending = true;
      refresh.disabled = true;
      history.disabled = true;
      status.textContent = 'Investigating saved metadata. No arrangement has been applied.';
      try {
        const result = await request('/arrangements/plan', {
          method: 'POST',
          body: JSON.stringify({
            ...body,
            expected_context: pin,
            ...(revising
              ? { revise_arrangement_id: revising.id, expected_record_revision: revising.revision }
              : {})
          })
        });
        if (run !== generation || !current()) throw new Error('Context changed');
        const view = arrangementPresentation(result);
        if (
          !view ||
          (result.revision_discussion?.goal || view.goal) !== body.goal ||
          (revising && (view.id !== revising.id || view.revision !== revising.revision + 1))
        )
          throw new Error('Invalid canonical outcome');
        if (revising) {
          target = { id: view.id, revision: view.revision };
          targetLabel.textContent = `Revising proposal ${target.id} · Revision ${target.revision}`;
        }
        renderArrangementHistory(resultBody, result);
        status.textContent =
          'Saved to canonical Home history. Nothing was applied. Refresh the context before requesting another plan.';
        context = null; // Its successful save advanced the catalog revision.
        await onSaved();
        if (run !== generation || !current()) throw new Error('Context changed');
        return { canonicalSaved: true };
      } catch (error) {
        if (run === generation && current()) status.textContent = homeArrangementFailure(error);
        const outcome = new Error('Planning outcome requires canonical history');
        outcome.definite = homeArrangementRefusalIsDefinite(error);
        throw outcome;
      } finally {
        pending = false;
        refresh.disabled = false;
        history.disabled = false;
      }
    }
  });
  newProposal.addEventListener('click', () => {
    if (pending || !current()) return;
    generation++;
    context = null;
    target = null;
    targetLabel.textContent = 'New proposal';
    consent.refresh();
    status.textContent = 'New proposal selected. Refresh planning context before Send.';
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
      if (target) {
        const latest = saved.arrangements.find(ref => ref.id === target.id);
        if (
          !latest ||
          latest.revision !== target.revision ||
          !['draft', 'needs_more_evidence', 'awaiting_user_review'].includes(latest.state)
        ) {
          context = null;
          consent.refresh();
          status.textContent =
            'The revision target changed. Choose Revise with assistant on the current saved proposal, or explicitly start a new proposal. Your goal is kept.';
          return;
        }
      }
      await readContext();
    } catch (_) {
      if (current() && run === generation)
        status.textContent =
          'Saved history could not be reopened. Your goal is kept; no retry was sent.';
    }
  });
  return {
    revise: async (ref, initiator = null) => {
      if (pending || !current() || !arrangementID(ref?.id)) return;
      const previousTarget = target?.id;
      const run = ++generation;
      context = null;
      consent.refresh();
      try {
        const saved = await request(`/arrangements/${encodeURIComponent(ref.id)}`, {
          cache: 'no-store'
        });
        if (run !== generation || !current()) return;
        const view = arrangementPresentation(saved);
        if (
          !view ||
          view.id !== ref.id ||
          view.revision !== ref.revision ||
          saved.kind !== 'proposal' ||
          !['draft', 'needs_more_evidence', 'awaiting_user_review'].includes(view.state)
        )
          throw new Error('Stale proposal');
        target = { id: ref.id, revision: ref.revision };
        if (previousTarget !== ref.id || !goal.value.trim())
          goal.value = saved.revision_discussion?.goal || view.goal;
        targetLabel.textContent = `Revising proposal ${target.id} · Revision ${target.revision}`;
        details.open = true;
        await readContext();
        if (
          current() &&
          context &&
          target?.id === ref.id &&
          initiator &&
          document.activeElement === initiator
        )
          goal.focus?.();
      } catch (_) {
        if (run === generation && current())
          status.textContent =
            'This proposal could not be reopened for revision. Reopen saved history; no model was called.';
      }
    },
    invalidate: ({ clearResult = false } = {}) => {
      generation++;
      context = null;
      if (clearResult) {
        resultBody.replaceChildren();
        status.textContent = 'Catalog changed. Refresh planning context before another request.';
      }
      if (!pending) refresh.disabled = false;
      consent.refresh();
    }
  };
}
