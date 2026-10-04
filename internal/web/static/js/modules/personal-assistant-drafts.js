// Save to HQ backlog: the reviewed save of one assistant reply.
//
// Two steps, both owned by the server: a review that writes nothing, then a
// save of exactly what the user sees in the form. The review's operation ID is
// the save's only retry identity. It is sent unchanged on every attempt and
// this module never mints another for the same review, so a retry can never
// become a second Ticket.

const REVIEW_ENDPOINT = '/api/home-assistant/drafts/review';
const SAVE_ENDPOINT = '/api/home-assistant/drafts/save';
const REQUEST_TIMEOUT_MS = 30000;

export const SAVE_ACTION_LABEL = 'Save to HQ backlog';

/** Character counts the way the server counts them: by code point. */
export function characterCount(value) {
  return Array.from(String(value ?? '')).length;
}

/** The form's state against the canonical Ticket limits. */
export function draftCounts(title, body, limits) {
  const titleLimit = Number(limits?.title_chars) || 300;
  const bodyLimit = Number(limits?.body_chars) || 100000;
  const titleChars = characterCount(String(title ?? '').trim());
  const bodyChars = characterCount(String(body ?? '').trim());
  return {
    titleChars,
    bodyChars,
    titleLimit,
    bodyLimit,
    titleEmpty: titleChars === 0,
    bodyEmpty: bodyChars === 0,
    titleOver: titleChars > titleLimit,
    bodyOver: bodyChars > bodyLimit,
    titleMultiline: /[\r\n]/.test(String(title ?? '').trim())
  };
}

/** Whether the form may be submitted. The server validates again. */
export function canSaveDraft(title, body, limits, saving) {
  const counts = draftCounts(title, body, limits);
  return (
    saving !== true &&
    !counts.titleEmpty &&
    !counts.bodyEmpty &&
    !counts.titleOver &&
    !counts.bodyOver &&
    !counts.titleMultiline
  );
}

/** The save request for a review: what is in the form, under the review's operation ID. */
export function draftSavePayload(review, title, body) {
  return {
    operation_id: String(review?.operation_id || ''),
    title: String(title ?? ''),
    body: String(body ?? ''),
    target_workspace_id: String(review?.target?.workspace_id || ''),
    source: {
      conversation_id: String(review?.source?.conversation_id || ''),
      message_id: String(review?.source?.message_id || '')
    }
  };
}

/** One sentence describing a verified receipt. It claims only what the Ticket shows. */
export function receiptSummary(receipt) {
  const workspace = String(receipt?.workspace_name || '').trim() || 'Personal HQ';
  const number = String(receipt?.display_number || '').trim();
  const label = number ? ` as ${number}` : '';
  const state = String(receipt?.state_label || '').trim() || 'Backlog';
  if (receipt?.created) {
    const placement =
      !receipt.assigned && !receipt.scheduled
        ? ' It is unassigned and not scheduled.'
        : ' Check its assignment and schedule in Personal HQ.';
    return `Saved to ${workspace} ${state.toLowerCase()}${label}.${placement}`;
  }
  const since = receipt?.changed_since ? ' It has been edited since it was saved.' : '';
  return `Already saved to ${workspace}${label}. Nothing was saved twice.${since}`;
}

/**
 * How a failed save should be shown.
 *   field    a value in the form needs fixing; the form stays editable
 *   retry    nothing was saved, or the outcome is unknown; the same save can be sent again
 *   blocked  this review can no longer be saved; the text stays so it can be copied
 */
export function saveFailure(status, body) {
  const code = String(body?.error || '');
  const message = String(body?.message || '').trim();
  if (code === 'invalid_draft') {
    return {
      kind: 'field',
      field: String(body?.field || ''),
      message: message || 'Check the title and draft.'
    };
  }
  if (code === 'operation_conflict') {
    const saved = body?.saved;
    const where = saved?.display_number ? ` as ${saved.display_number}` : '';
    return {
      kind: 'blocked',
      saved: saved || null,
      message: `An earlier version of this review is already saved${where}. Your changes here were not saved.`
    };
  }
  if (status === 0 || status >= 500 || code === 'draft_save_unavailable') {
    return {
      kind: 'retry',
      message:
        status === 0
          ? 'Could not confirm whether this was saved. Try again: the same save is never applied twice.'
          : message || 'The backlog could not be written right now. Nothing was saved; try again.'
    };
  }
  return { kind: 'blocked', message: message || 'This draft cannot be saved from here.' };
}

const state = {
  review: null,
  saving: false,
  blocked: false,
  trigger: null,
  els: null
};

async function postJSON(url, payload) {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(payload),
      signal: controller.signal
    });
    let body = null;
    try {
      body = await response.json();
    } catch (_) {
      body = null;
    }
    return { status: response.status, ok: response.ok, body };
  } catch (_) {
    // No response: the request may or may not have reached the server.
    return { status: 0, ok: false, body: null };
  } finally {
    window.clearTimeout(timer);
  }
}

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function showView(name) {
  const els = state.els;
  els.edit.hidden = name !== 'edit';
  els.receipt.hidden = name !== 'receipt';
}

function syncForm() {
  const els = state.els;
  if (!els || !state.review) return;
  const counts = draftCounts(els.title.value, els.body.value, state.review.limits);
  els.titleHint.textContent = counts.titleMultiline
    ? 'The title must be a single line.'
    : `${counts.titleChars} of ${counts.titleLimit} characters`;
  els.bodyHint.textContent = `${counts.bodyChars.toLocaleString()} of ${counts.bodyLimit.toLocaleString()} characters`;
  els.title.setAttribute(
    'aria-invalid',
    counts.titleEmpty || counts.titleOver || counts.titleMultiline ? 'true' : 'false'
  );
  els.body.setAttribute('aria-invalid', counts.bodyEmpty || counts.bodyOver ? 'true' : 'false');
  els.save.disabled =
    state.blocked ||
    !canSaveDraft(els.title.value, els.body.value, state.review.limits, state.saving);
  els.cancel.disabled = state.saving;
}

function renderNotes(notes) {
  const items = (notes || []).map(note => {
    const item = document.createElement('li');
    item.textContent = String(note);
    return item;
  });
  state.els.notes.replaceChildren(...items);
  state.els.notes.hidden = items.length === 0;
}

/** Opens the review. Nothing has been saved at this point. */
function open(review, options = {}) {
  const els = state.els;
  if (!els || !review?.operation_id) return false;
  state.review = review;
  state.saving = false;
  state.blocked = false;
  state.trigger = options.trigger || document.activeElement;
  els.target.textContent = `Saves to ${review.target?.name || 'Personal HQ'} · ${review.placement || 'Backlog'}`;
  els.title.value = String(review.title || '');
  els.body.value = String(review.body || '');
  els.title.readOnly = false;
  els.body.readOnly = false;
  els.save.textContent = 'Save to backlog';
  renderNotes(review.notes);
  setStatus('Review the title and draft. Nothing is saved until you choose Save to backlog.');
  showView('edit');
  els.form.hidden = false;
  syncForm();
  els.form.scrollIntoView?.({ block: 'nearest' });
  els.title.focus();
  els.title.select?.();
  return true;
}

function restoreFocus() {
  const trigger = state.trigger;
  state.trigger = null;
  if (trigger && document.contains(trigger) && typeof trigger.focus === 'function') {
    trigger.focus();
    return;
  }
  document.getElementById('personalAssistantInput')?.focus();
}

/** Closes the review without saving. */
function close() {
  if (!state.els || state.saving) return false;
  state.els.form.hidden = true;
  state.review = null;
  state.blocked = false;
  setStatus('');
  restoreFocus();
  return true;
}

function showReceipt(receipt) {
  const els = state.els;
  els.receiptText.textContent = receiptSummary(receipt);
  const href = String(receipt?.href || '').trim();
  els.open.hidden = !href;
  if (href) els.open.href = href;
  showView('receipt');
  setStatus('');
  markSaved(state.review?.source, receipt);
  els.done.focus();
}

/** Shows on the source message that it has been saved, linking to the Ticket. */
function markSaved(source, receipt) {
  const messageId = String(source?.message_id || '');
  if (!messageId || !receipt?.ticket_id) return;
  const row = document.querySelector(
    `#homeAssistantConversation [data-message-id="${CSS.escape(messageId)}"]`
  );
  const actions = row?.querySelector('.personal-assistant-message__actions');
  if (!actions) return;
  let badge = actions.querySelector('.personal-assistant-message__saved');
  if (!badge) {
    badge = document.createElement('a');
    badge.className = 'personal-assistant-message__saved';
    actions.append(badge);
  }
  badge.textContent = `Saved${receipt.display_number ? ` as ${receipt.display_number}` : ''}`;
  badge.dataset.ticketId = String(receipt.ticket_id);
  if (receipt.href) badge.href = String(receipt.href);
}

async function submit(event) {
  event?.preventDefault();
  const els = state.els;
  if (!els || !state.review || state.saving || state.blocked) return false;
  if (!canSaveDraft(els.title.value, els.body.value, state.review.limits, false)) {
    syncForm();
    return false;
  }
  state.saving = true;
  syncForm();
  setStatus('Saving…');
  const result = await postJSON(
    SAVE_ENDPOINT,
    draftSavePayload(state.review, els.title.value, els.body.value)
  );
  state.saving = false;
  if (result.ok && result.body?.receipt) {
    showReceipt(result.body.receipt);
    return true;
  }
  // Every failure keeps what the user typed and the same operation ID.
  const failure = saveFailure(result.status, result.body);
  setStatus(failure.message);
  if (failure.kind === 'field') {
    (failure.field === 'title' ? els.title : els.body).focus();
  } else if (failure.kind === 'retry') {
    els.save.textContent = 'Try again';
  } else {
    state.blocked = true;
    els.title.readOnly = true;
    els.body.readOnly = true;
    if (failure.saved?.href) {
      const link = document.createElement('a');
      link.href = String(failure.saved.href);
      link.textContent = ' Open the saved item.';
      els.status.append(link);
    }
  }
  syncForm();
  return false;
}

/** Asks the server to prepare a review of one stored reply. */
async function requestReview(conversationId, messageId, trigger) {
  if (!state.els || state.saving) return false;
  if (trigger) trigger.disabled = true;
  const result = await postJSON(REVIEW_ENDPOINT, {
    conversation_id: String(conversationId || ''),
    message_id: String(messageId || '')
  });
  if (trigger) trigger.disabled = false;
  if (result.ok && result.body?.review) return open(result.body.review, { trigger });
  const message =
    String(result.body?.message || '').trim() ||
    'The review could not be opened right now. Nothing was saved.';
  window.PersonalAssistantConversation?.notify?.(message);
  return false;
}

/**
 * Adds the message actions to a stored assistant reply. A row without a
 * canonical message ID gets none: there is nothing exact to save.
 */
function decorate(row) {
  if (!row?.dataset?.messageId || row.dataset.messageRole !== 'assistant') return null;
  const bubble = row.firstElementChild;
  if (!bubble) return null;
  let actions = bubble.querySelector('.personal-assistant-message__actions');
  if (!actions) {
    actions = document.createElement('div');
    actions.className = 'personal-assistant-message__actions';
    bubble.append(actions);
  }
  if (!actions.querySelector('[data-message-action="save-draft"]')) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'personal-assistant-message__action';
    button.dataset.messageAction = 'save-draft';
    button.textContent = SAVE_ACTION_LABEL;
    button.addEventListener('click', () => {
      void requestReview(row.dataset.conversationId, row.dataset.messageId, button);
    });
    actions.append(button);
  }
  return actions;
}

function init() {
  const form = document.getElementById('personalAssistantDraftReview');
  if (!form) return;
  state.els = {
    form,
    target: document.getElementById('personalAssistantDraftTarget'),
    edit: form.querySelector('[data-draft-view="edit"]'),
    receipt: form.querySelector('[data-draft-view="receipt"]'),
    title: document.getElementById('personalAssistantDraftTitle'),
    titleHint: document.getElementById('personalAssistantDraftTitleHint'),
    body: document.getElementById('personalAssistantDraftBody'),
    bodyHint: document.getElementById('personalAssistantDraftBodyHint'),
    notes: document.getElementById('personalAssistantDraftNotes'),
    save: document.getElementById('personalAssistantDraftSave'),
    cancel: document.getElementById('personalAssistantDraftCancel'),
    receiptText: document.getElementById('personalAssistantDraftReceipt'),
    open: document.getElementById('personalAssistantDraftOpen'),
    done: document.getElementById('personalAssistantDraftDone'),
    status: document.getElementById('personalAssistantDraftStatus')
  };
  form.addEventListener('submit', event => void submit(event));
  state.els.title.addEventListener('input', syncForm);
  state.els.body.addEventListener('input', syncForm);
  state.els.cancel.addEventListener('click', close);
  state.els.done.addEventListener('click', close);
  // Escape cancels the review and stops there: the drawer underneath stays open.
  form.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    event.preventDefault();
    close();
  });
}

const api = { init, open, close, decorate, requestReview, _state: state };
if (typeof window !== 'undefined') window.PersonalAssistantDrafts = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
