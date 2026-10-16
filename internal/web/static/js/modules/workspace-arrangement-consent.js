// Browser intent only: these choices never authorize source access or effects.
// Callers must bind contextKey to their current owner/conversation/Home, source,
// and displayed runtime context; the host independently revalidates all of them.
import { arrangementID } from './workspace-arrangement.js';

export function validArrangementGoal(value) {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.trim() === value &&
    new TextEncoder().encode(value).length <= 2000 &&
    !/[\p{Cc}\p{Cf}\p{Cs}]/u.test(value)
  );
}

export function arrangementDisclosure(kind) {
  if (!['attachment', 'home_library'].includes(kind)) return null;
  return {
    title:
      kind === 'attachment'
        ? 'Share more held metadata for this plan'
        : 'Share saved catalog metadata for this plan',
    source:
      kind === 'attachment'
        ? 'Planning can share names, formats, counts, observed dates and bounded tree metadata already held for this attachment—not only the eight names and 8 KiB discussion summary. Clear any discussion focus first; this choice covers the full held attachment.'
        : "Planning can share names, formats, observed dates, availability, connection metadata and logical collection membership from this Home's saved catalog with its currently bound Manager's configured model. When revising, it also shares the saved proposal's exact selections and any pending revision question.",
    budget:
      'Up to 64 KiB of metadata evidence may be shared during one turn, across at most eight model requests and twelve tool calls. Prompts and conversation context are additional; earlier evidence may be sent again. The configured API model may be local or remote.',
    boundary:
      'No new scan, file-content read, native tool, install, workspace creation, activation or source-file change is approved. A saved plan or clarification is not permission to apply it.',
    upgrade:
      kind === 'home_library'
        ? 'Saving a proposal or clarification upgrades a version-1 Home library to version 2. Older Ori versions cannot open the upgraded library. Reading history does not upgrade it.'
        : ''
  };
}

function snapshot(input) {
  if (
    !input ||
    !['attachment', 'home_library'].includes(input.kind) ||
    typeof input.contextKey !== 'string' ||
    !input.contextKey ||
    input.contextKey.length > 4096 ||
    input.available !== true ||
    !validArrangementGoal(input.goal) ||
    ![input.agentName, input.providerName, input.modelName].every(
      value => validArrangementGoal(value) && value.length <= 160
    )
  )
    return null;
  if (
    input.kind === 'attachment' &&
    (input.historical !== false || !Array.isArray(input.focusIDs) || input.focusIDs.length !== 0)
  )
    return null;
  if (
    input.kind === 'home_library' &&
    (!arrangementID(input.homeID) ||
      !Number.isSafeInteger(input.expectedRevision) ||
      input.expectedRevision < 1 ||
      ![1, 2].includes(input.libraryVersion))
  )
    return null;
  // Copy only primitives. Later mutations of caller objects cannot amend consent.
  return {
    kind: input.kind,
    contextKey: input.contextKey,
    goal: input.goal,
    agentName: input.agentName,
    providerName: input.providerName,
    modelName: input.modelName,
    ...(input.kind === 'home_library'
      ? {
          homeID: input.homeID,
          expectedRevision: input.expectedRevision,
          libraryVersion: input.libraryVersion
        }
      : {})
  };
}

// One explicit choice, one send. There is no durable/localStorage preference,
// timer, provider fallback, request, retry or approval token in this controller.
export class ArrangementConsent {
  #snapshot = null;
  #key = '';
  #epoch = 0;
  #share = false;
  #upgrade = false;

  update(input) {
    const next = snapshot(input);
    const key = next ? JSON.stringify(next) : '';
    if (key !== this.#key || !next) {
      this.#share = false;
      this.#upgrade = false;
      this.#epoch++;
    }
    this.#snapshot = next;
    this.#key = key;
    return this.view();
  }

  view() {
    const upgradeRequired = this.#snapshot?.libraryVersion === 1;
    return Object.freeze({
      epoch: this.#epoch,
      available: Boolean(this.#snapshot),
      recipient: this.#snapshot
        ? `${this.#snapshot.agentName} · ${this.#snapshot.providerName} / ${this.#snapshot.modelName}`
        : '',
      upgradeRequired,
      share: this.#share,
      upgrade: this.#upgrade,
      canSend: Boolean(this.#snapshot && this.#share && (!upgradeRequired || this.#upgrade))
    });
  }

  chooseSharing(checked, epoch) {
    if (!this.#snapshot || epoch !== this.#epoch) return false;
    this.#share = checked === true;
    return true;
  }

  chooseUpgrade(checked, epoch) {
    if (!this.#snapshot || epoch !== this.#epoch || this.#snapshot.libraryVersion !== 1)
      return false;
    this.#upgrade = checked === true;
    return true;
  }

  take(currentInput) {
    this.update(currentInput);
    if (!this.view().canSend) return null;
    const source = this.#snapshot;
    // Consume before handing off. A synchronous error, timeout or uncertain save
    // must never turn this choice into an automatic repeat request.
    this.#share = false;
    this.#upgrade = false;
    this.#epoch++;
    if (source.kind === 'attachment')
      return {
        prompt: source.goal,
        arrangement: { share_held_metadata: true }
      };
    return {
      goal: source.goal,
      expected_revision: source.expectedRevision,
      share_catalog_metadata: true,
      allow_upgrade: source.libraryVersion === 1
    };
  }
}

// This component deliberately defaults off while the PA/Home Send adapters are
// being integrated. Opening or checking it never sends metadata. The caller owns
// the goal input, actual endpoint, busy lifecycle and canonical history rendering.
export function mountArrangementConsent(
  container,
  { kind, getContext, onSend, enabled = false } = {}
) {
  const disclosure = arrangementDisclosure(kind);
  if (!disclosure || typeof getContext !== 'function' || typeof onSend !== 'function') return null;
  enabled = enabled === true;
  const document = container.ownerDocument;
  const node = (tag, text, className = '') => {
    const element = document.createElement(tag);
    element.className = className;
    if (text) element.textContent = text;
    return element;
  };
  const consent = new ArrangementConsent();
  const section = node(
    'section',
    '',
    'workspace-arrangement-consent border rounded p-3 my-2 text-break'
  );
  section.setAttribute('aria-label', disclosure.title);
  section.append(node('h4', disclosure.title, 'h6'));
  const recipient = node('p', '', 'small fw-semibold');
  section.append(recipient);
  for (const value of [disclosure.source, disclosure.budget, disclosure.boundary])
    section.append(node('p', value, 'small'));
  const checkbox = text => {
    const label = node('label', '', 'd-block my-2');
    const input = node('input');
    input.type = 'checkbox';
    label.append(input, node('span', ` ${text}`));
    return { label, input };
  };
  const share = checkbox('Share this bounded metadata for one planning turn');
  const upgrade = checkbox(disclosure.upgrade);
  const send = node('button', 'Send organizing goal', 'btn btn-sm btn-outline-secondary');
  send.type = 'button';
  const status = node('p', '', 'small text-body-secondary');
  status.setAttribute('role', 'status');
  section.append(share.label, upgrade.label, send, status);
  container.append(section);
  let pending = false;
  let needsHistory = false;
  let renderedEpoch;
  const current = () => {
    const value = getContext();
    return value?.kind === kind ? value : null;
  };
  const paint = view => {
    renderedEpoch = view.epoch;
    recipient.textContent = view.recipient
      ? `Planning recipient: ${view.recipient}`
      : 'Enter a valid goal and confirm the current source and model.';
    share.input.checked = view.share;
    upgrade.input.checked = view.upgrade;
    share.input.disabled = !enabled || pending || needsHistory || !view.available;
    upgrade.label.hidden = !view.upgradeRequired;
    // Bootstrap's d-block overrides the browser's [hidden] display rule.
    upgrade.label.className = view.upgradeRequired ? 'd-block my-2' : 'd-none';
    upgrade.input.disabled = !enabled || pending || needsHistory || !view.upgradeRequired;
    // Keep a valid-context trigger focusable through a request and its result.
    // Native disabling would blur it; aria-disabled plus the event guard blocks
    // sending without consent without trapping or later stealing keyboard focus.
    send.disabled = !enabled || !view.available;
    const blocked = !enabled || pending || needsHistory || !view.canSend;
    send.setAttribute('aria-disabled', String(blocked));
    send.className = `btn btn-sm btn-outline-secondary mb-2${blocked ? ' disabled' : ''}`;
  };
  const refresh = () => {
    const view = consent.update(current());
    paint(view);
    status.textContent = pending
      ? 'Planning request pending. Its save is not yet confirmed.'
      : needsHistory
        ? 'Reopen canonical saved history before choosing to send again. Your goal is kept.'
        : !enabled
          ? 'Planning Send is not enabled yet. Ordinary discussion and manual setup remain available.'
          : !view.available
            ? kind === 'home_library'
              ? 'Use a current saved catalog and bound Manager, and a single-line goal of at most 2,000 UTF-8 bytes. Refresh the planning context after changes.'
              : 'Use a current source and a single-line goal of at most 2,000 UTF-8 bytes. Historical or focused attachment discussion cannot authorize this plan.'
            : 'This choice is for the current source and goal only. Nothing is sent until you choose Send.';
    return view;
  };
  const choose = (field, checked) => {
    const epoch = renderedEpoch;
    const view = consent.update(current());
    if (enabled && !pending && !needsHistory && epoch === view.epoch) {
      if (field === 'share') consent.chooseSharing(checked, epoch);
      else consent.chooseUpgrade(checked, epoch);
    }
    paint(consent.view());
    if (epoch !== view.epoch)
      status.textContent = 'The source or goal changed. Read the disclosure and choose again.';
  };
  share.input.addEventListener('change', () => choose('share', share.input.checked));
  upgrade.input.addEventListener('change', () => choose('upgrade', upgrade.input.checked));
  send.addEventListener('click', async () => {
    if (!enabled || pending || needsHistory) return;
    const request = consent.take(current());
    paint(consent.view());
    if (!request) {
      status.textContent =
        'The source, goal or sharing choices changed. Review them before sending.';
      return;
    }
    pending = true;
    const operationEpoch = consent.view().epoch;
    paint(consent.view());
    status.textContent = 'Requesting an inert plan. Saving is not yet confirmed.';
    try {
      const outcome = await onSend(request);
      status.textContent =
        outcome?.canonicalSaved === true
          ? 'Saved to canonical history. Nothing was applied. Review fresh context before another request.'
          : consent.update(current()).epoch === operationEpoch
            ? 'Request finished. Check canonical saved history for the outcome before sending again.'
            : 'Context changed while the request was running. Reopen its original saved history to check the outcome.';
    } catch (_) {
      needsHistory = true;
      status.textContent =
        consent.update(current()).epoch === operationEpoch
          ? 'The request or its save could not be confirmed. Your goal is kept. Reopen saved history before choosing to send again.'
          : 'Context changed while the request was running. Reopen its original saved history to check the outcome.';
    } finally {
      pending = false;
      paint(consent.update(current()));
    }
  });
  refresh();
  return {
    refresh,
    clearChoices: () => {
      consent.update(null);
      refresh();
    },
    // Only the owning adapter calls this after a successful canonical history
    // GET, never from a model reply or a checkbox acknowledging an error.
    historyReopened: () => {
      if (pending) return false;
      needsHistory = false;
      refresh();
      return true;
    }
  };
}
