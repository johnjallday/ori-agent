// Remember…: the reviewed save of one personal fact from a conversation.
//
// It is a different action from Save to HQ backlog. A draft is kept as a
// backlog item; a fact is one line the assistant may use again later. Opening
// the review is local and writes nothing. Saving sends the user's final wording
// to the existing reviewed Personal HQ memory API — the same one the remembered
// facts page uses — and never calls a model.

import {
  MAX_FACT_BYTES,
  factByteLength,
  newRequestID,
  reviewTextValid
} from './personal-hq-fact.js';

import { messageActions, messageReviewTrigger } from './personal-assistant-message-actions.js';

const KNOWLEDGE_API = '/api/personal-assistant/knowledge';
const FACTS_HREF = '/profile#personalHQKnowledge';
const REQUEST_TIMEOUT_MS = 30000;
const PREFILL_LIMIT_BYTES = 200;

export const REMEMBER_ACTION_LABEL = 'Remember…';

export const MEMORY_CATEGORIES = [
  { id: 'people', label: 'People' },
  { id: 'how_you_work', label: 'How you work' },
  { id: 'projects', label: 'Projects' },
  { id: 'routines', label: 'Routines' }
];

const REQUEST_LEAD =
  /^(please\s+)?(write|draft|compose|rewrite|rephrase|translate|make|give|turn|convert|change|shorten|save|keep|add|put|remind|remember|tell|show|can|could|would|will|what|when|where|who|why|how|is|are|do|does|did)\b/i;

/**
 * The fact to start the review with, taken from the message the user chose.
 * Only a short statement the user wrote themselves is offered. A reply the
 * assistant wrote — a greeting, a draft, an example — is never offered as a
 * fact: the review starts empty and the user says what to remember.
 */
export function factCandidate(text, role) {
  if (role !== 'user') return '';
  const line = String(text ?? '')
    .trim()
    .split(/\s+/u)
    .join(' ')
    .replace(/[.!]+$/u, '');
  if (!line || /[\r\n]/.test(String(text ?? '').trim())) return '';
  if (line.endsWith('?') || REQUEST_LEAD.test(line)) return '';
  if (factByteLength(line) > PREFILL_LIMIT_BYTES || !reviewTextValid(line)) return '';
  return line;
}

/**
 * A hint when the chosen message talks about a date-bound fact but gives no
 * date. Ori does not supply one: the user adds it or leaves it out.
 */
export function missingValueHint(sourceText) {
  const text = String(sourceText ?? '');
  if (!/birthday|anniversary|생일|기념일/i.test(text)) return '';
  const hasDate =
    /\b\d{1,2}[/.-]\d{1,2}\b/.test(text) ||
    /\b(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?\s+\d{1,2}(st|nd|rd|th)?\b/i.test(
      text
    ) ||
    /\b\d{1,2}(st|nd|rd|th)?\s+(of\s+)?(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*/i.test(
      text
    ) ||
    /\d{1,2}\s*월\s*\d{1,2}\s*일/.test(text);
  return hasDate
    ? ''
    : 'This message does not say the date. Ori will not guess one: add it yourself if you want it remembered.';
}

/** The review field's state: byte count and why it cannot be saved, if it cannot. */
export function factReviewState(text) {
  const value = String(text ?? '');
  const bytes = factByteLength(value);
  let problem = '';
  if (!value.trim()) problem = 'Write the one fact you want remembered.';
  else if (bytes > MAX_FACT_BYTES)
    problem = `That is ${bytes} bytes. A remembered fact holds at most ${MAX_FACT_BYTES}; shorten it yourself — it is never cut for you.`;
  else if (!reviewTextValid(value))
    problem = 'Use one line with single spaces and no leading or trailing space.';
  return { bytes, limit: MAX_FACT_BYTES, valid: !problem, problem };
}

/**
 * The retry key for a save. The same wording, category, and assistant state is
 * the same action and keeps its key, so a retry after a lost response is never
 * saved twice. Changed wording is a new action.
 */
export function memoryRequest(prior, text, category, stateVersion, generate = newRequestID) {
  if (
    prior &&
    prior.text === text &&
    prior.category === category &&
    prior.stateVersion === stateVersion
  ) {
    return prior;
  }
  return { id: generate(), text, category, stateVersion };
}

/** The remembered item, if the canonical list holds this exact fact. */
export function findRememberedFact(items, text) {
  return (
    (Array.isArray(items) ? items : []).find(
      item => item?.state === 'approved' && item.text === text && !item.review_unavailable
    ) || null
  );
}

/**
 * What to tell the user after a save attempt, decided from the response and,
 * when the response is not a clear success, from a fresh read of the canonical
 * list (`readback`: { ok, items, stateVersion }).
 *
 *   saved      verified: the fact is in Personal HQ memory
 *   existing   it was already remembered; nothing was added
 *   invalid    the wording was refused; it is still in the form
 *   stale      the assistant's state changed; review and save again
 *   refused    Personal HQ memory would not take it now (full, paused, needs repair)
 *   unknown    it could not be confirmed either way; the same save can be retried
 *
 * `reason` is the server's own wording for a refusal, when it gave one.
 */
export function memoryOutcome(status, item, readback, text, usedVersion, reason = '') {
  if (status >= 200 && status < 300 && item?.id) return { kind: 'saved', item };
  const found = readback?.ok ? findRememberedFact(readback.items, text) : null;
  if (found) return { kind: status === 409 ? 'existing' : 'saved', item: found };
  if (status === 400) {
    return {
      kind: 'invalid',
      message:
        'That wording was not accepted. Use one plain line, and keep passwords or keys in Vault. Nothing was remembered.'
    };
  }
  if (status === 409 && readback?.ok) {
    if (readback.stateVersion !== usedVersion) {
      return { kind: 'stale', message: STALE_REVIEW_MESSAGE };
    }
    const why = String(reason || '').trim() || 'Personal HQ memory needs attention first';
    return {
      kind: 'refused',
      message: `${why.replace(/[.\s]+$/u, '')}. Nothing was remembered; your wording is still here.`
    };
  }
  return {
    kind: 'unknown',
    message:
      'Could not confirm whether this was remembered. Try again: the same save is never applied twice.'
  };
}

/** Why Personal HQ memory could not be read before a save, in its own words when it gave any. */
export function memoryUnavailableMessage(status, reason) {
  const why =
    String(reason || '').trim() ||
    (status === 409
      ? 'Personal HQ memory needs attention before a fact can be remembered'
      : 'Personal HQ memory is unavailable right now');
  return `${why.replace(/[.\s]+$/u, '')}. Nothing was remembered; your wording is still here.`;
}

/**
 * Whether the assistant's state moved between opening the review and saving.
 * The review was read against `openedVersion`; a save is only sent for that
 * same state, so a hire, pause, or HQ change while it was open is noticed
 * instead of being saved into whatever exists now.
 */
export function reviewIsStale(openedVersion, currentVersion) {
  return (
    Number.isSafeInteger(openedVersion) && openedVersion > 0 && openedVersion !== currentVersion
  );
}

const STALE_REVIEW_MESSAGE =
  'Your assistant’s setup changed while this was open. Nothing was remembered; your wording is still here — check it, then save again to use the current setup.';

const state = {
  open: false,
  saving: false,
  retry: null,
  trigger: null,
  // The assistant state version the panel last reported, and the one this
  // review was opened against.
  knownVersion: 0,
  openedVersion: 0,
  els: null
};

async function requestJSON(url, options = {}) {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(url, {
      credentials: 'same-origin',
      ...options,
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
    return { status: 0, ok: false, body: null };
  } finally {
    window.clearTimeout(timer);
  }
}

async function readKnowledge() {
  const result = await requestJSON(KNOWLEDGE_API, { headers: { Accept: 'application/json' } });
  const stateVersion = Number(result.body?.state_version);
  return {
    ok: result.ok && Number.isSafeInteger(stateVersion) && stateVersion > 0,
    status: result.status,
    stateVersion,
    reason: result.ok ? '' : String(result.body?.message || ''),
    items: Array.isArray(result.body?.items) ? result.body.items : []
  };
}

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function showView(name) {
  state.els.edit.hidden = name !== 'edit';
  state.els.receipt.hidden = name !== 'receipt';
}

function sync() {
  const els = state.els;
  if (!els) return;
  const review = factReviewState(els.text.value);
  els.limit.textContent = `${review.bytes} / ${review.limit} UTF-8 bytes`;
  els.text.setAttribute('aria-invalid', review.valid || !els.text.value ? 'false' : 'true');
  els.problem.textContent = els.text.value ? review.problem : '';
  els.save.disabled = state.saving || !review.valid;
  els.cancel.disabled = state.saving;
}

/**
 * Opens the review. This is local: nothing is requested, written, or queued.
 * `review.text` is the starting wording (possibly empty); `review.source` is
 * the chosen message, shown for context and never saved.
 */
function open(review = {}, options = {}) {
  const els = state.els;
  if (!els || state.saving) return false;
  window.PersonalAssistantDrafts?.close?.();
  state.open = true;
  state.retry = null;
  state.openedVersion = state.knownVersion;
  state.trigger = messageReviewTrigger(options.trigger || document.activeElement);
  const source = String(review.source || '').trim();
  els.source.hidden = !source;
  els.source.textContent = source
    ? `From: “${Array.from(source).slice(0, 160).join('')}${Array.from(source).length > 160 ? '…' : ''}” — shown for context; only the fact below is remembered.`
    : '';
  els.text.value = String(review.text || '');
  els.category.value = MEMORY_CATEGORIES.some(item => item.id === review.category)
    ? review.category
    : 'people';
  els.hint.textContent = missingValueHint(source || review.text);
  els.hint.hidden = !els.hint.textContent;
  showView('edit');
  els.form.hidden = false;
  setStatus(
    els.text.value
      ? 'Check the wording. Nothing is remembered until you choose Remember this fact.'
      : 'Write one fact in your own words. Nothing is remembered until you choose Remember this fact.'
  );
  sync();
  els.form.scrollIntoView?.({ block: 'nearest' });
  els.text.focus();
  return true;
}

function close() {
  const els = state.els;
  if (!els || state.saving) return false;
  if (els.form.hidden) return true;
  els.form.hidden = true;
  state.open = false;
  state.retry = null;
  setStatus('');
  const trigger = state.trigger;
  state.trigger = null;
  if (trigger && document.contains(trigger) && typeof trigger.focus === 'function') trigger.focus();
  else document.getElementById('personalAssistantInput')?.focus();
  return true;
}

function showReceipt(outcome, text) {
  const els = state.els;
  els.receiptText.textContent =
    outcome.kind === 'existing'
      ? `Already remembered in Personal HQ: “${text}”. Nothing was added.`
      : `Remembered in Personal HQ: “${text}”.`;
  showView('receipt');
  setStatus('');
  els.done.focus();
}

async function submit(event) {
  event?.preventDefault();
  const els = state.els;
  if (!els || state.saving) return false;
  const text = els.text.value;
  const category = els.category.value;
  if (!factReviewState(text).valid) {
    sync();
    els.text.focus();
    return false;
  }
  state.saving = true;
  sync();
  setStatus('Remembering…');
  try {
    // What is remembered now, and the assistant's current state. The server
    // refuses a save made against an older state.
    const current = await readKnowledge();
    if (!current.ok) {
      setStatus(memoryUnavailableMessage(current.status, current.reason));
      return false;
    }
    if (reviewIsStale(state.openedVersion, current.stateVersion)) {
      // Said once: the next save is the user's choice against the current state.
      state.openedVersion = current.stateVersion;
      state.retry = null;
      setStatus(STALE_REVIEW_MESSAGE);
      return false;
    }
    state.openedVersion = current.stateVersion;
    const existing = findRememberedFact(current.items, text);
    if (existing) {
      showReceipt({ kind: 'existing', item: existing }, text);
      return true;
    }
    try {
      state.retry = memoryRequest(state.retry, text, category, current.stateVersion);
    } catch (error) {
      setStatus(String(error?.message || 'A retry key could not be made. Nothing was remembered.'));
      return false;
    }
    const result = await requestJSON(`${KNOWLEDGE_API}/explicit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({
        state_version: state.retry.stateVersion,
        request_id: state.retry.id,
        category,
        text
      })
    });
    const item = result.body?.item || result.body?.data?.item;
    const readback = result.ok && item?.id ? null : await readKnowledge();
    const outcome = memoryOutcome(
      result.status,
      item,
      readback,
      text,
      state.retry.stateVersion,
      result.body?.message
    );
    if (outcome.kind === 'saved' || (outcome.kind === 'existing' && outcome.item)) {
      state.retry = null;
      showReceipt(outcome, text);
      return true;
    }
    // Told once; the next save is against the state just read.
    if (outcome.kind === 'stale') state.openedVersion = readback.stateVersion;
    setStatus(outcome.message);
    if (outcome.kind === 'invalid') els.text.focus();
    return false;
  } finally {
    state.saving = false;
    sync();
  }
}

/** Adds Remember… to a stored message. It only opens the review. */
function decorate(row) {
  if (!row?.dataset?.messageId) return null;
  const bubble = row.firstElementChild;
  if (!bubble) return null;
  const actions = messageActions(row);
  if (!actions) return null;
  if (actions.querySelector('[data-message-action="remember"]')) return actions;
  // The message text, read before the actions were added beneath it.
  const text = String(bubble.firstChild?.textContent || '').trim();
  const role = row.dataset.messageRole === 'user' ? 'user' : 'assistant';
  const button = document.createElement('button');
  button.type = 'button';
  button.className =
    'personal-assistant-message__action personal-assistant-message__action--secondary';
  button.dataset.messageAction = 'remember';
  button.textContent = REMEMBER_ACTION_LABEL;
  button.addEventListener('click', () => {
    open({ text: factCandidate(text, role), source: text }, { trigger: button });
  });
  actions.append(button);
  return actions;
}

function init() {
  const form = document.getElementById('personalAssistantMemoryReview');
  if (!form) return;
  state.els = {
    form,
    edit: form.querySelector('[data-memory-view="edit"]'),
    receipt: form.querySelector('[data-memory-view="receipt"]'),
    source: document.getElementById('personalAssistantMemorySource'),
    text: document.getElementById('personalAssistantMemoryText'),
    limit: document.getElementById('personalAssistantMemoryLimit'),
    problem: document.getElementById('personalAssistantMemoryProblem'),
    category: document.getElementById('personalAssistantMemoryCategory'),
    hint: document.getElementById('personalAssistantMemoryHint'),
    save: document.getElementById('personalAssistantMemorySave'),
    cancel: document.getElementById('personalAssistantMemoryCancel'),
    receiptText: document.getElementById('personalAssistantMemoryReceipt'),
    facts: document.getElementById('personalAssistantMemoryFacts'),
    done: document.getElementById('personalAssistantMemoryDone'),
    status: document.getElementById('personalAssistantMemoryStatus')
  };
  state.els.category.replaceChildren(
    ...MEMORY_CATEGORIES.map(item => {
      const option = document.createElement('option');
      option.value = item.id;
      option.textContent = item.label;
      return option;
    })
  );
  state.els.facts.href = FACTS_HREF;
  // The panel reports the relationship it loaded; a review opened later is
  // bound to that state.
  document.addEventListener('personal-assistant:status', event => {
    const version = Number(event.detail?.personalAssistant?.state_version);
    if (Number.isSafeInteger(version) && version > 0) state.knownVersion = version;
  });
  form.addEventListener('submit', event => void submit(event));
  state.els.text.addEventListener('input', sync);
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

const api = { init, open, close, decorate, _state: state };
if (typeof window !== 'undefined') window.PersonalAssistantMemory = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
