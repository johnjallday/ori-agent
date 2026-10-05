// Save to HQ backlog, and Update saved draft: the reviewed actions on one
// assistant reply.
//
// Each is two steps owned by the server: a review that writes nothing, then a
// write of exactly what the user sees in the form.
//
// - A save's operation ID is its only retry identity. It is sent unchanged on
//   every attempt and this module never mints another for the same review, so
//   a retry can never become a second Ticket.
// - An update carries the Ticket version the user reviewed against. A Ticket
//   that changed since is never overwritten; its current text is shown for a
//   fresh review instead.

import { messageActions, messageReviewTrigger } from './personal-assistant-message-actions.js';

const DRAFTS_ENDPOINT = '/api/home-assistant/drafts';
const REVIEW_ENDPOINT = `${DRAFTS_ENDPOINT}/review`;
const SAVE_ENDPOINT = `${DRAFTS_ENDPOINT}/save`;
const REQUEST_TIMEOUT_MS = 30000;
const WORKING_KEY = 'ori.personalAssistant.draft';
const RESUME_PARAM = 'assistant_draft';

export const SAVE_ACTION_LABEL = 'Save to HQ backlog';
export const UPDATE_ACTION_LABEL = 'Update saved draft';

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

/**
 * The update request: the form's text against the saved draft the user
 * reviewed — its version and the digest of the text they were shown. Both are
 * sent because some editors change a Ticket's text without moving its version.
 */
export function draftUpdatePayload(update, title, body) {
  return {
    if_version: Number(update?.current?.version) || 0,
    if_digest: String(update?.current?.digest || ''),
    title: String(title ?? ''),
    body: String(body ?? ''),
    target_workspace_id: String(update?.target?.workspace_id || '')
  };
}

/** One sentence describing a verified update. */
export function updateReceiptSummary(receipt) {
  const number = String(receipt?.display_number || '').trim() || 'the saved draft';
  const workspace = String(receipt?.workspace_name || '').trim() || 'Personal HQ';
  return receipt?.applied
    ? `Updated ${number} in ${workspace}. Only its title and text changed.`
    : `${number} already has this text. Nothing was changed twice.`;
}

/**
 * How a failed update should be shown.
 *   field    a value in the form needs fixing
 *   stale    the saved draft changed elsewhere; `current` is what is stored now,
 *            the user's proposal is kept, and nothing was overwritten
 *   retry    nothing was changed, or the outcome is unknown; the same update
 *            can be sent again and is never applied twice
 *   blocked  the saved draft can no longer be updated from here
 */
export function updateFailure(status, body) {
  const code = String(body?.error || '');
  const message = String(body?.message || '').trim();
  if (code === 'invalid_draft') {
    return {
      kind: 'field',
      field: String(body?.field || ''),
      message: message || 'Check the title and draft.'
    };
  }
  if (code === 'saved_draft_changed' && body?.current) {
    return {
      kind: 'stale',
      current: body.current,
      message:
        'This saved draft was changed in Personal HQ after you opened the review. Nothing was overwritten. What is saved now is shown above under “Currently saved”; check it, then update again if you still want to.'
    };
  }
  if (status === 0 || status >= 500 || code === 'draft_save_unavailable') {
    return {
      kind: 'retry',
      message:
        status === 0
          ? 'Could not confirm whether this was updated. Try again: the same update is never applied twice.'
          : message || 'The backlog could not be written right now. Nothing was changed; try again.'
    };
  }
  return { kind: 'blocked', message: message || 'This saved draft cannot be updated from here.' };
}

/** A saved draft can be updated from a conversation until work on it starts. */
export function isEditableDraftState(state) {
  return state === 'backlog' || state === 'ready';
}

/** The label on a reply that was saved, including how the saved copy has moved on. */
export function savedDraftLabel(saved) {
  const number = String(saved?.display_number || '').trim();
  const parts = [`Saved${number ? ` as ${number}` : ''}`];
  const state = String(saved?.state || '');
  if (state && state !== 'backlog') parts.push(String(saved?.state_label || state));
  if (saved?.matches_source === false) parts.push('changed in Personal HQ');
  return parts.join(' · ');
}

/**
 * Which saved draft this tab is working on: the one it already had if that is
 * still one of the conversation's saved drafts, otherwise the one saved from
 * the latest reply. Null when the conversation has none.
 */
export function pickWorkingDraft(savedList, currentTicketId) {
  const list = Array.isArray(savedList) ? savedList.filter(item => item?.ticket_id) : [];
  if (!list.length) return null;
  const current = list.find(item => item.ticket_id === currentTicketId);
  return current || list[list.length - 1];
}

/** The line describing the saved draft the tab is working on. */
export function workingDraftNote(working) {
  if (!working?.ticket_id) return '';
  const number = String(working.display_number || '').trim() || 'a saved draft';
  const title = String(working.title || '').trim();
  const name = title ? `${number} “${title}”` : number;
  if (working.editable === false) {
    return `Working from ${name}. It is ${String(working.state_label || 'in progress')} now, so it is edited in Personal HQ, not from here.`;
  }
  const newer = Number(working.newer_replies) || 0;
  if (newer > 0) {
    return `Working on ${name}. ${newer} newer ${newer === 1 ? 'reply here is' : 'replies here are'} not saved until you choose ${UPDATE_ACTION_LABEL}.`;
  }
  if (working.matches_source === false) {
    return `Working on ${name}. Its saved text was changed in Personal HQ and differs from the reply it came from.`;
  }
  return `Working on ${name}. Replies are not saved until you choose ${UPDATE_ACTION_LABEL}.`;
}

/**
 * What to do with a saved draft opened from its Ticket: continue the
 * conversation it came from, or — when that conversation is gone — offer a new
 * one. A deleted conversation is never recreated.
 */
export function resumePlan(payload) {
  const draft = payload?.draft;
  if (!draft?.ticket_id) return { kind: 'unavailable' };
  const conversation = payload?.conversation || {};
  if (conversation.available && conversation.id) {
    return { kind: 'conversation', conversationId: String(conversation.id), draft };
  }
  const reason = String(conversation.reason || '');
  return {
    kind: 'new',
    draft,
    message:
      reason === 'conversation_unavailable'
        ? 'The conversation this draft came from could not be read right now. The saved draft is still in Personal HQ.'
        : 'The conversation this draft came from is gone. The saved draft is still in Personal HQ; you can start a new conversation about it.'
  };
}

const state = {
  mode: 'save',
  review: null,
  saving: false,
  blocked: false,
  trigger: null,
  // The saved draft this tab is working on, and a draft waiting for its
  // conversation to be opened or for the user to start a new one.
  working: null,
  pending: null,
  els: null
};

async function requestJSON(url, method, payload) {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const options = { method, headers: { Accept: 'application/json' }, signal: controller.signal };
    if (payload !== undefined) {
      options.headers['Content-Type'] = 'application/json';
      options.body = JSON.stringify(payload);
    }
    const response = await fetch(url, options);
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

const postJSON = (url, payload) => requestJSON(url, 'POST', payload);

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function notify(message) {
  window.PersonalAssistantConversation?.notify?.(message);
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

function showCurrent(current) {
  const els = state.els;
  els.current.hidden = !current;
  if (!current) return;
  els.current.open = true;
  els.currentLabel.textContent = `Currently saved — ${String(current.title || '').trim()}`;
  els.currentText.textContent = String(current.body || '');
}

function present(mode, review, options) {
  const els = state.els;
  // One review at a time: a draft review and a fact review are different
  // actions and are never open together.
  window.PersonalAssistantMemory?.close?.();
  state.mode = mode;
  state.review = review;
  state.saving = false;
  state.blocked = false;
  state.trigger = messageReviewTrigger(options.trigger || document.activeElement);
  els.title.value = String(review.title || '');
  els.body.value = String(review.body || '');
  els.title.readOnly = false;
  els.body.readOnly = false;
  renderNotes(review.notes);
  showView('edit');
  els.form.hidden = false;
  syncForm();
  els.form.scrollIntoView?.({ block: 'nearest' });
}

/** Opens the save review. Nothing has been saved at this point. */
function open(review, options = {}) {
  const els = state.els;
  // A save in flight keeps its form: its receipt must land on its own review.
  if (!els || !review?.operation_id || state.saving) return false;
  present('save', review, options);
  els.heading.textContent = SAVE_ACTION_LABEL;
  els.target.textContent = `Saves to ${review.target?.name || 'Personal HQ'} · ${review.placement || 'Backlog'}`;
  els.bodyLabel.textContent = 'Draft';
  els.save.textContent = 'Save to backlog';
  showCurrent(null);
  setStatus('Review the title and draft. Nothing is saved until you choose Save to backlog.');
  els.title.focus();
  els.title.select?.();
  return true;
}

/** Opens the update review: what is saved now, and the proposed revision. */
function openUpdate(update, options = {}) {
  const els = state.els;
  if (!els || !update?.current?.ticket_id || state.saving) return false;
  present('update', update, options);
  const number = update.current.display_number || 'saved draft';
  els.heading.textContent = `${UPDATE_ACTION_LABEL} ${number}`;
  els.target.textContent = `Updates ${number} in ${update.target?.name || 'Personal HQ'} · title and text only`;
  els.bodyLabel.textContent = 'Proposed text';
  els.save.textContent = UPDATE_ACTION_LABEL;
  showCurrent(update.current);
  setStatus(
    `Compare the saved text with the proposal. Nothing changes until you choose ${UPDATE_ACTION_LABEL}.`
  );
  els.body.focus();
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

/** Closes the review without writing. */
function close() {
  if (!state.els || state.saving) return false;
  if (state.els.form.hidden) return true;
  state.els.form.hidden = true;
  state.review = null;
  state.blocked = false;
  setStatus('');
  restoreFocus();
  return true;
}

function showReceipt(text, href) {
  const els = state.els;
  els.receiptText.textContent = text;
  const link = String(href || '').trim();
  els.open.hidden = !link;
  if (link) els.open.href = link;
  showView('receipt');
  setStatus('');
  els.done.focus();
}

function blockForm() {
  state.blocked = true;
  state.els.title.readOnly = true;
  state.els.body.readOnly = true;
}

async function submitSave() {
  const els = state.els;
  const review = state.review;
  const result = await postJSON(
    SAVE_ENDPOINT,
    draftSavePayload(review, els.title.value, els.body.value)
  );
  state.saving = false;
  if (result.ok && result.body?.receipt) {
    const receipt = result.body.receipt;
    showReceipt(receiptSummary(receipt), receipt.href);
    setWorking({
      ...receipt,
      // A retried save can return a Ticket whose work has since started.
      editable: isEditableDraftState(receipt.state),
      matches_source: !receipt.changed_since,
      newer_replies: 0,
      conversation_id: review.source?.conversation_id,
      message_id: review.source?.message_id
    });
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
    blockForm();
    if (failure.saved?.href) {
      const link = document.createElement('a');
      link.href = String(failure.saved.href);
      link.textContent = ' Open the saved item.';
      els.status.append(link);
    }
  }
  return false;
}

async function submitUpdate() {
  const els = state.els;
  const update = state.review;
  const ticketId = update.current.ticket_id;
  const result = await postJSON(
    `${DRAFTS_ENDPOINT}/${encodeURIComponent(ticketId)}/update`,
    draftUpdatePayload(update, els.title.value, els.body.value)
  );
  state.saving = false;
  if (result.ok && result.body?.receipt) {
    const receipt = result.body.receipt;
    showReceipt(updateReceiptSummary(receipt), receipt.href);
    setWorking({ ...state.working, ...receipt, matches_source: false, newer_replies: 0 });
    return true;
  }
  const failure = updateFailure(result.status, result.body);
  setStatus(failure.message);
  if (failure.kind === 'field') {
    (failure.field === 'title' ? els.title : els.body).focus();
  } else if (failure.kind === 'stale') {
    // The proposal stays in the form. Only the saved side is refreshed, and the
    // next attempt is reviewed against that current version.
    state.review = { ...update, current: failure.current };
    showCurrent(failure.current);
    els.current.scrollIntoView?.({ block: 'nearest' });
  } else if (failure.kind === 'retry') {
    els.save.textContent = 'Try again';
  } else {
    blockForm();
  }
  return false;
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
  setStatus(state.mode === 'update' ? 'Updating…' : 'Saving…');
  const done = state.mode === 'update' ? await submitUpdate() : await submitSave();
  if (!done) syncForm();
  return done;
}

/** Asks the server to prepare a save review of one stored reply. */
async function requestReview(conversationId, messageId, trigger) {
  if (!state.els || state.saving) return false;
  if (trigger) trigger.disabled = true;
  const result = await postJSON(REVIEW_ENDPOINT, {
    conversation_id: String(conversationId || ''),
    message_id: String(messageId || '')
  });
  if (trigger) trigger.disabled = false;
  if (result.ok && result.body?.review) return open(result.body.review, { trigger });
  notify(
    String(result.body?.message || '').trim() ||
      'The review could not be opened right now. Nothing was saved.'
  );
  return false;
}

/** Asks the server to prepare an update of the working draft from one stored reply. */
async function requestUpdateReview(conversationId, messageId, trigger) {
  if (!state.els || state.saving || !state.working?.ticket_id) return false;
  if (trigger) trigger.disabled = true;
  const result = await postJSON(
    `${DRAFTS_ENDPOINT}/${encodeURIComponent(state.working.ticket_id)}/review`,
    { conversation_id: String(conversationId || ''), message_id: String(messageId || '') }
  );
  if (trigger) trigger.disabled = false;
  if (result.ok && result.body?.update) return openUpdate(result.body.update, { trigger });
  const code = String(result.body?.error || '');
  notify(
    String(result.body?.message || '').trim() ||
      'The update review could not be opened right now. Nothing was changed.'
  );
  // A saved draft that is gone is no longer something to work on.
  if (code === 'saved_draft_not_found') setWorking(null);
  return false;
}

function conversationRows() {
  return Array.from(
    document.querySelectorAll(
      '#homeAssistantConversation [data-message-role="assistant"][data-message-id]'
    )
  );
}

function actionButton(actions, name, label, onClick) {
  let button = actions.querySelector(`[data-message-action="${name}"]`);
  if (!button) {
    button = document.createElement('button');
    button.type = 'button';
    button.className = 'personal-assistant-message__action';
    button.dataset.messageAction = name;
    button.addEventListener('click', () => onClick(button));
    // Draft actions stay ahead of the secondary Remember… action.
    const remember = actions.querySelector('[data-message-action="remember"]');
    if (remember) actions.insertBefore(button, remember);
    else actions.append(button);
  }
  button.textContent = label;
  return button;
}

/**
 * Adds the message actions to a stored assistant reply. A row without a
 * canonical message ID gets none: there is nothing exact to save.
 */
function decorate(row) {
  if (!row?.dataset?.messageId || row.dataset.messageRole !== 'assistant') return null;
  const actions = messageActions(row);
  if (!actions) return null;
  actionButton(actions, 'save-draft', SAVE_ACTION_LABEL, button => {
    void requestReview(row.dataset.conversationId, row.dataset.messageId, button);
  });
  // Update is offered on every reply except the one the working draft was
  // saved from, and only while that draft can still be updated from here.
  const working = state.working;
  const canUpdate =
    working?.ticket_id &&
    working.editable !== false &&
    working.message_id !== row.dataset.messageId;
  const existing = actions.querySelector('[data-message-action="update-draft"]');
  if (canUpdate) {
    actionButton(
      actions,
      'update-draft',
      `${UPDATE_ACTION_LABEL} ${working.display_number || ''}`.trim(),
      button => {
        void requestUpdateReview(row.dataset.conversationId, row.dataset.messageId, button);
      }
    );
  } else if (existing) {
    existing.remove();
  }
  return actions;
}

function setBadge(row, saved) {
  const actions = row?.querySelector('.personal-assistant-message__actions');
  if (!actions) return;
  let badge = actions.querySelector('.personal-assistant-message__saved');
  if (!saved) {
    badge?.remove();
    return;
  }
  if (!badge) {
    badge = document.createElement('a');
    badge.className = 'personal-assistant-message__saved';
    actions.append(badge);
  }
  badge.textContent = savedDraftLabel(saved);
  badge.dataset.ticketId = String(saved.ticket_id);
  if (saved.href) badge.href = String(saved.href);
}

function renderWorking() {
  const els = state.els;
  if (!els?.working) return;
  const working = state.working;
  const pending = state.pending;
  els.working.hidden = !working && !pending;
  els.workingText.textContent = pending ? pending.message : workingDraftNote(working);
  const href = String((pending?.draft || working)?.href || '').trim();
  els.workingOpen.hidden = !href;
  if (href) els.workingOpen.href = href;
  els.workingStart.hidden = !pending?.message;
  conversationRows().forEach(row => decorate(row));
}

/** Called when a new reply is stored: it is not part of the saved draft yet. */
function replyAdded() {
  if (!state.working?.ticket_id) return;
  state.working = {
    ...state.working,
    newer_replies: (Number(state.working.newer_replies) || 0) + 1
  };
  renderWorking();
}

function storeWorking(ticketId) {
  try {
    if (ticketId) window.sessionStorage.setItem(WORKING_KEY, ticketId);
    else window.sessionStorage.removeItem(WORKING_KEY);
  } catch (_) {
    // Without storage the working draft is simply forgotten on reload.
  }
}

function storedWorking() {
  try {
    return String(window.sessionStorage.getItem(WORKING_KEY) || '');
  } catch (_) {
    return '';
  }
}

/** Sets, or clears, the saved draft this tab is working on. */
function setWorking(saved) {
  state.working = saved?.ticket_id ? saved : null;
  state.pending = null;
  storeWorking(state.working?.ticket_id || '');
  if (state.working?.message_id) {
    const row = conversationRows().find(
      item => item.dataset.messageId === state.working.message_id
    );
    if (row) {
      decorate(row);
      setBadge(row, state.working);
    }
  }
  renderWorking();
}

/**
 * Called when a conversation is opened, with the drafts saved from it. Labels
 * the replies they came from and picks the one this tab is working on.
 */
function applySaved(savedList) {
  const list = Array.isArray(savedList) ? savedList : [];
  const rows = conversationRows();
  rows.forEach(row => decorate(row));
  for (const saved of list) {
    const row = rows.find(item => item.dataset.messageId === saved.message_id);
    if (row) setBadge(row, saved);
  }
  const wanted = state.pending?.draft?.ticket_id || state.working?.ticket_id || storedWorking();
  setWorking(pickWorkingDraft(list, wanted));
}

/** Called when the tab leaves its conversation. */
function clearWorking() {
  state.working = null;
  state.pending = null;
  storeWorking('');
  renderWorking();
}

/** The saved draft a turn should read, by canonical Ticket ID. */
function workingRef() {
  return state.working?.ticket_id ? { ticket_id: state.working.ticket_id } : null;
}

/** Called with a turn's draft_context: a draft that is gone stops being worked on. */
function applyContext(context) {
  if (!context || context.available !== false) return;
  if (state.working?.ticket_id !== context.ticket_id) return;
  clearWorking();
  notify(
    'The saved draft is no longer in your Personal HQ backlog. This reply did not use it, and it was not recreated.'
  );
}

/** Opens a saved draft by its Ticket ID and continues the conversation it came from. */
async function resumeFromTicket(ticketId) {
  const id = String(ticketId || '').trim();
  if (!id) return false;
  const result = await requestJSON(`${DRAFTS_ENDPOINT}/${encodeURIComponent(id)}`, 'GET');
  const plan = result.ok ? resumePlan(result.body) : { kind: 'unavailable' };
  if (plan.kind === 'unavailable') {
    notify(
      String(result.body?.message || '').trim() ||
        'That saved draft could not be opened right now. Nothing was changed.'
    );
    return false;
  }
  if (plan.kind === 'conversation') {
    state.pending = { draft: plan.draft, message: '' };
    const resumed = await window.PersonalAssistantConversation?.resume?.(plan.conversationId);
    if (resumed) return true;
    state.pending = null;
  }
  // The conversation is gone or could not be opened: keep the saved draft in
  // view and let the user decide to start a new conversation about it.
  state.pending = {
    draft: plan.draft,
    message: plan.message || resumePlan({ draft: plan.draft }).message
  };
  state.working = null;
  renderWorking();
  return true;
}

/** The user's explicit choice to start a new conversation about a saved draft. */
function startFromPending() {
  const draft = state.pending?.draft;
  if (!draft) return false;
  if (window.PersonalAssistantConversation?.startNew?.() === false) return false;
  setWorking({ ...draft, message_id: '', newer_replies: 0 });
  notify(
    `New conversation about ${draft.display_number || 'your saved draft'}. Its saved text is given to your assistant with each message; nothing is saved until you choose ${UPDATE_ACTION_LABEL}.`
  );
  document.getElementById('personalAssistantInput')?.focus();
  return true;
}

function openFromLocation() {
  let ticketId = '';
  try {
    const url = new URL(window.location.href);
    ticketId = String(url.searchParams.get(RESUME_PARAM) || '').trim();
    if (!ticketId) return;
    url.searchParams.delete(RESUME_PARAM);
    window.history.replaceState(null, '', url.pathname + url.search + url.hash);
  } catch (_) {
    return;
  }
  const start = () => {
    const panel = window.PersonalAssistantPanel;
    if (!panel?._state?.view?.available) return false;
    panel.open(document.getElementById('personalAssistantLauncher'));
    void resumeFromTicket(ticketId);
    return true;
  };
  if (start()) return;
  // The relationship is read asynchronously; open once it is known to be ready.
  const onStatus = () => {
    if (start()) document.removeEventListener('personal-assistant:status', onStatus);
  };
  document.addEventListener('personal-assistant:status', onStatus);
}

function init() {
  const form = document.getElementById('personalAssistantDraftReview');
  if (!form) return;
  state.els = {
    form,
    heading: document.getElementById('personalAssistantDraftReviewHeading'),
    target: document.getElementById('personalAssistantDraftTarget'),
    edit: form.querySelector('[data-draft-view="edit"]'),
    receipt: form.querySelector('[data-draft-view="receipt"]'),
    current: document.getElementById('personalAssistantDraftCurrent'),
    currentLabel: document.getElementById('personalAssistantDraftCurrentLabel'),
    currentText: document.getElementById('personalAssistantDraftCurrentText'),
    title: document.getElementById('personalAssistantDraftTitle'),
    titleHint: document.getElementById('personalAssistantDraftTitleHint'),
    body: document.getElementById('personalAssistantDraftBody'),
    bodyLabel: document.getElementById('personalAssistantDraftBodyLabel'),
    bodyHint: document.getElementById('personalAssistantDraftBodyHint'),
    notes: document.getElementById('personalAssistantDraftNotes'),
    save: document.getElementById('personalAssistantDraftSave'),
    cancel: document.getElementById('personalAssistantDraftCancel'),
    receiptText: document.getElementById('personalAssistantDraftReceipt'),
    open: document.getElementById('personalAssistantDraftOpen'),
    done: document.getElementById('personalAssistantDraftDone'),
    status: document.getElementById('personalAssistantDraftStatus'),
    working: document.getElementById('personalAssistantSavedDraft'),
    workingText: document.getElementById('personalAssistantSavedDraftText'),
    workingOpen: document.getElementById('personalAssistantSavedDraftOpen'),
    workingStart: document.getElementById('personalAssistantSavedDraftStart')
  };
  form.addEventListener('submit', event => void submit(event));
  state.els.title.addEventListener('input', syncForm);
  state.els.body.addEventListener('input', syncForm);
  state.els.cancel.addEventListener('click', close);
  state.els.done.addEventListener('click', close);
  state.els.workingStart?.addEventListener('click', startFromPending);
  // Escape cancels the review and stops there: the drawer underneath stays open.
  form.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    event.preventDefault();
    close();
  });
  openFromLocation();
}

const api = {
  init,
  open,
  openUpdate,
  close,
  decorate,
  requestReview,
  applySaved,
  replyAdded,
  clearWorking,
  workingRef,
  applyContext,
  resumeFromTicket,
  _state: state
};
if (typeof window !== 'undefined') window.PersonalAssistantDrafts = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
