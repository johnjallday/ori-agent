/*
 * song-card-suggestions.js — an agent's waiting fields suggestions on the
 * project card, with the owner's Accept and Dismiss (PRD #792 FR24).
 *
 * A suggestion is inert until the owner answers. Accept asks the Home for a
 * review of the *saved* suggestion (the patch never comes from this page),
 * shows its before and after per field, and commits only on a second
 * confirmation; Dismiss marks only that suggestion dismissed. Both use the
 * Home's own owner-only proposal routes. Wording stays claim-free: a
 * suggestion is "Suggested by …", never "<agent> did …", and its reason is an
 * untrusted note.
 */

import { lowerNoun, projectWords } from './library-open.js';

const text = value => (typeof value === 'string' ? value.trim() : '');
const lines = value => (Array.isArray(value) ? value.map(text).filter(Boolean) : []);

/** "just now", "5 minutes ago", "3 hours ago", "yesterday", "4 days ago". */
export function relativeTime(isoTime, now = new Date()) {
  const at = new Date(isoTime || '');
  if (Number.isNaN(at.getTime())) return '';
  const minutes = Math.max(0, Math.round((now.getTime() - at.getTime()) / 60000));
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'} ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`;
  const days = Math.round(hours / 24);
  return days === 1 ? 'yesterday' : `${days} days ago`;
}

const milestoneLine = milestone =>
  [
    text(milestone?.label),
    text(milestone?.due_date) ? `due ${text(milestone.due_date)}` : '',
    milestone?.complete ? 'done' : ''
  ]
    .filter(Boolean)
    .join(' · ');

const countLines = (count, word = 'line') => `${count} ${word}${count === 1 ? '' : 's'}`;

/**
 * One row per proposed field, in the card's order: "Next action → value", or
 * a list ("Blockers → 2 lines") expanded in place. An emptied list reads
 * "None". Plugin fields read under their declared labels.
 */
export function suggestionRows(fields, words) {
  const proposed = fields && typeof fields === 'object' ? fields : {};
  const rows = [];
  if (typeof proposed.next_action === 'string') {
    rows.push({ label: 'Next action', value: text(proposed.next_action) || 'None' });
  }
  for (const [key, label] of [
    ['blockers', 'Blockers'],
    ['deliverables', 'Deliverables']
  ]) {
    if (!Array.isArray(proposed[key])) continue;
    const items = lines(proposed[key]);
    rows.push({ label, value: items.length ? countLines(items.length) : 'None', lines: items });
  }
  if (Array.isArray(proposed.milestones)) {
    const items = proposed.milestones.map(milestoneLine).filter(Boolean);
    rows.push({
      label: 'Milestones',
      value: items.length ? countLines(items.length, 'milestone') : 'None',
      lines: items
    });
  }
  const extra = proposed.extra && typeof proposed.extra === 'object' ? proposed.extra : {};
  for (const field of projectWords(words).fields) {
    if (!Object.hasOwn(extra, field.id)) continue;
    const items = lines(extra[field.id]);
    if (field.kind === 'list') {
      rows.push({ label: field.label, value: countLines(items.length), lines: items });
    } else {
      rows.push({ label: field.label, value: items.join(' ') });
    }
  }
  return rows;
}

/** The card's view of one suggestion from the song-card response. */
export function suggestionView(suggestion, words, now = new Date()) {
  const agent = text(suggestion?.agent_name) || 'An assistant';
  const when = relativeTime(suggestion?.created_at, now);
  return {
    id: text(suggestion?.id),
    byline: `Suggested by ${agent}${when ? ` · ${when}` : ''}`,
    rows: suggestionRows(suggestion?.proposed_fields, words),
    fields:
      suggestion?.proposed_fields && typeof suggestion.proposed_fields === 'object'
        ? suggestion.proposed_fields
        : {},
    reason: text(suggestion?.reason)
  };
}

function element(doc, tag, className, content) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

/**
 * The suggestion block, or null when there are none. Accept and Dismiss are
 * native buttons, and appear only when the host passes their handlers.
 */
export function renderSuggestions(doc, suggestions, { onAccept, onDismiss } = {}) {
  if (!Array.isArray(suggestions) || !suggestions.length) return null;
  const block = element(doc, 'div', 'song-card-suggestions');
  for (const suggestion of suggestions) {
    const item = element(doc, 'section', 'song-card-suggestion');
    item.setAttribute('aria-label', suggestion.byline);
    item.append(element(doc, 'p', 'song-card-suggestion-by', suggestion.byline));
    const list = element(doc, 'ul', 'song-card-suggestion-rows');
    for (const row of suggestion.rows) {
      const li = element(doc, 'li', 'song-card-suggestion-row');
      li.append(doc.createTextNode(`${row.label} → ${row.value}`));
      if (row.lines?.length) {
        const sub = element(doc, 'ul', 'song-card-suggestion-lines');
        for (const line of row.lines) sub.append(element(doc, 'li', '', line));
        li.append(sub);
      }
      list.append(li);
    }
    item.append(list);
    if (suggestion.reason) {
      item.append(
        element(
          doc,
          'p',
          'song-card-suggestion-reason',
          `Reason (untrusted note): ${suggestion.reason}`
        )
      );
    }
    if (typeof onAccept === 'function' || typeof onDismiss === 'function') {
      const actions = element(doc, 'div', 'song-card-suggestion-actions');
      if (typeof onAccept === 'function') {
        const accept = element(
          doc,
          'button',
          'modern-btn modern-btn-primary song-card-accept',
          'Accept'
        );
        accept.setAttribute('type', 'button');
        accept.addEventListener('click', () => onAccept(suggestion, accept));
        actions.append(accept);
      }
      if (typeof onDismiss === 'function') {
        const dismiss = element(
          doc,
          'button',
          'modern-btn modern-btn-secondary song-card-dismiss',
          'Dismiss'
        );
        dismiss.setAttribute('type', 'button');
        dismiss.addEventListener('click', () => onDismiss(suggestion, dismiss));
        actions.append(dismiss);
      }
      item.append(actions);
    }
    block.append(item);
  }
  return block;
}

const shown = (value, fallback = 'Not set') => {
  if (Array.isArray(value)) {
    const items = value
      .map(item => (item && typeof item === 'object' ? milestoneLine(item) : text(item)))
      .filter(Boolean);
    return items.length ? items.join('; ') : 'None';
  }
  return text(value) || fallback;
};

/**
 * The confirm dialog's lines for an Accept: before and after for every field
 * the suggestion proposes, from the Home's review (which derived them from the
 * saved suggestion), then the reason as an untrusted note.
 */
export function acceptLines(review, suggestion, words) {
  const before = review?.before || {};
  const after = review?.after || {};
  const proposed = suggestion?.fields || suggestion?.proposed_fields || {};
  const out = [];
  for (const [key, label] of [
    ['next_action', 'Next action'],
    ['blockers', 'Blockers'],
    ['deliverables', 'Deliverables'],
    ['milestones', 'Milestones']
  ]) {
    if (proposed[key] === undefined) continue;
    out.push(`${label}: ${shown(before[key])} → ${shown(after[key])}`);
  }
  const extra = proposed.extra && typeof proposed.extra === 'object' ? proposed.extra : {};
  for (const field of projectWords(words).fields) {
    if (!Object.hasOwn(extra, field.id)) continue;
    out.push(
      `${field.label}: ${shown(before.extra?.[field.id])} → ${shown(after.extra?.[field.id])}`
    );
  }
  if (suggestion?.reason) out.push(`Reason (untrusted note): ${suggestion.reason}`);
  out.push('Only these Home notes change. No project file, ticket or agent is opened or changed.');
  return out;
}

/** The Home's proposal route for one suggestion. */
export function proposalURL(homeID, proposalID, action) {
  return `/api/workspaces/${encodeURIComponent(String(homeID || ''))}/assistant-program/library/proposals/${encodeURIComponent(String(proposalID || ''))}/${action}`;
}

async function postJSON(fetchImpl, url, body) {
  const response = await fetchImpl(url, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  let result = null;
  try {
    result = await response.json();
  } catch {
    result = null;
  }
  if (!response.ok) {
    const message =
      text(result?.error) || text(result?.message) || `Request failed (${response.status})`;
    throw new Error(message);
  }
  return result;
}

let keyCounter = 0;
const idempotencyKey = prefix => `${prefix}-${Date.now().toString(36)}-${(keyCounter += 1)}`;

/**
 * Accept: review the saved suggestion on the Home, confirm before and after,
 * then commit with the review token. Resolves "accepted" or "canceled".
 */
export async function acceptSuggestion({
  homeID,
  suggestion,
  words,
  title,
  fetchImpl = globalThis.fetch,
  confirm
}) {
  const review = await postJSON(fetchImpl, proposalURL(homeID, suggestion.id, 'review'), {});
  const ok = await confirm({
    title: `Save this suggestion for ${title || `this ${lowerNoun(words)}`}?`,
    lines: acceptLines(review, suggestion, words),
    action: 'Save'
  });
  if (!ok) return 'canceled';
  await postJSON(fetchImpl, proposalURL(homeID, suggestion.id, 'commit'), {
    review_token: review.token,
    idempotency_key: idempotencyKey('card-accept'),
    confirm: true
  });
  return 'accepted';
}

/** Dismiss: one confirmation, then only that suggestion is dismissed. */
export async function dismissSuggestion({
  homeID,
  suggestion,
  fetchImpl = globalThis.fetch,
  confirm
}) {
  const ok = await confirm({
    title: 'Dismiss this suggestion?',
    lines: [
      'Only this suggestion is marked dismissed. No notes, sessions, folders or workspaces change.'
    ],
    action: 'Dismiss'
  });
  if (!ok) return 'canceled';
  await postJSON(fetchImpl, proposalURL(homeID, suggestion.id, 'dismiss'), {
    confirm: true,
    idempotency_key: idempotencyKey('card-dismiss')
  });
  return 'dismissed';
}

/** A native modal confirmation; resolves true only on the action button. */
export function confirmDialog({
  title,
  lines: dialogLines = [],
  action = 'Confirm',
  trigger
} = {}) {
  const doc = globalThis.document;
  if (!doc?.body) return Promise.resolve(false);
  return new Promise(resolve => {
    const dialog = element(doc, 'dialog', 'song-card-confirm');
    const form = element(doc, 'form');
    form.method = 'dialog';
    const heading = element(doc, 'h2', 'song-card-confirm-title', title);
    heading.id = idempotencyKey('song-card-confirm');
    dialog.setAttribute('aria-labelledby', heading.id);
    const list = element(doc, 'ul', 'song-card-confirm-lines');
    for (const line of dialogLines) list.append(element(doc, 'li', '', line));
    const actions = element(doc, 'div', 'song-card-confirm-actions');
    const cancel = element(doc, 'button', 'modern-btn modern-btn-secondary', 'Cancel');
    cancel.type = 'button';
    const accept = element(doc, 'button', 'modern-btn modern-btn-primary', action);
    accept.type = 'submit';
    actions.append(cancel, accept);
    form.append(heading, list, actions);
    dialog.append(form);
    doc.body.append(dialog);
    let accepted = false;
    cancel.addEventListener('click', () => dialog.close());
    form.addEventListener('submit', () => {
      accepted = true;
    });
    dialog.addEventListener(
      'close',
      () => {
        dialog.remove();
        trigger?.focus?.();
        resolve(accepted);
      },
      { once: true }
    );
    dialog.showModal();
    cancel.focus();
  });
}

/**
 * The handlers a card mount passes to renderSongCard: Accept and Dismiss
 * against the card's Home, then `onChanged` (the mount re-fetches its card).
 * A failure is reported through `onError` and changes nothing on the page.
 */
export function suggestionHandlers(
  view,
  { fetchImpl = globalThis.fetch, confirm = confirmDialog, onChanged, onError } = {}
) {
  if (!view?.homeID || !Array.isArray(view.suggestions) || !view.suggestions.length) return {};
  const run = async (button, action) => {
    if (button) button.disabled = true;
    try {
      const outcome = await action();
      if (outcome !== 'canceled') await onChanged?.(outcome);
    } catch (error) {
      onError?.(error);
    } finally {
      if (button) button.disabled = false;
    }
  };
  const confirmFrom = button => options => confirm({ ...options, trigger: button });
  return {
    onAccept: (suggestion, button) =>
      run(button, () =>
        acceptSuggestion({
          homeID: view.homeID,
          suggestion,
          words: view.words,
          title: view.title,
          fetchImpl,
          confirm: confirmFrom(button)
        })
      ),
    onDismiss: (suggestion, button) =>
      run(button, () =>
        dismissSuggestion({
          homeID: view.homeID,
          suggestion,
          fetchImpl,
          confirm: confirmFrom(button)
        })
      )
  };
}
