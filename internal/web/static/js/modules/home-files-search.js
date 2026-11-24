// home-files-search.js — Files' one search box: finding Ori's own items by name.
//
// PRD: tasks/prd-merge-tree-into-files.md, requirements 12-28.
//
// The box sits above the tree and the pane. What it finds is shown as a Results
// tab in the pane, first in the tab strip; the tree beside it is not filtered
// and not touched. This module owns three things and nothing else:
//
//   1. the request: what is sent to POST /api/name-search, and what comes back
//      read into one plain model (`snapshot`);
//   2. the controller: debounce, one request at a time that counts, a slower
//      earlier answer never overwriting a newer one, "More results" per kind,
//      Retry per kind;
//   3. the markup of the Results tab and the pure helpers around it.
//
// The term is private. It lives only in this controller's closure and in the
// request body. It is never written to the address, to localStorage or
// sessionStorage, to the remembered tree state, or to window.name, and the
// request asks not to be cached. The cockpit holds the latest snapshot in
// memory and nothing else.
//
// Pure parts are exported for home-files-search.test.js and run without a DOM.

import { iconHTML } from './home-tree-icons.js';

/** The Results tab is virtual: it is never in the list of open tabs or stored. */
export const RESULTS_TAB_KEY = 'search:results';

export const SEARCH_ENDPOINT = '/api/name-search';

/** How long typing must pause before a request is sent. */
export const DEBOUNCE_MS = 250;

/** Each kind shows this many results, then "More results". The server fixes it. */
export const PAGE_SIZE = 25;

/** The longest term the server takes, in characters. The input stops here. */
export const MAX_TERM_LENGTH = 128;

/** The kinds, in the order the Results tab lists them. */
export const KIND_ORDER = ['workspaces', 'notes', 'tickets', 'chats', 'agents', 'files'];

const KIND_LABELS = {
  workspaces: 'Workspaces',
  notes: 'Notes',
  tickets: 'Tickets',
  chats: 'Chats',
  agents: 'Agents',
  files: 'Files'
};

// What each kind is called when one of them could not be read.
const KIND_UNAVAILABLE_NAMES = {
  workspaces: 'Workspaces',
  notes: 'Notes',
  tickets: 'Tickets',
  chats: 'Chats',
  agents: 'Agents',
  files: 'Files'
};

// The icon each kind of result wears (names drawn by home-tree-icons.js).
const RESULT_ICONS = {
  workspace: 'workspace',
  group: 'group',
  note: 'note',
  ticket: 'ticket',
  chat: 'chats',
  agent: 'agent',
  file: 'file',
  output: 'file'
};

export function kindLabel(kind) {
  return KIND_LABELS[kind] || String(kind || '');
}

/** The box's text as it is searched: trimmed, and nothing typed is no search. */
export function normalizeTerm(text) {
  return String(text == null ? '' : text).trim();
}

/** The body of one request. The term goes here and nowhere else. */
export function requestBody({ term, scope = null, kinds = null, cursors = null } = {}) {
  const body = { term: normalizeTerm(term) };
  if (scope && scope.workspace_id) body.scope = scope;
  if (Array.isArray(kinds) && kinds.length) body.kinds = kinds.slice();
  if (cursors && Object.keys(cursors).length) body.cursors = { ...cursors };
  return body;
}

function emptyKind() {
  return {
    status: 'loading',
    count: null,
    items: [],
    nextCursor: '',
    partialReason: '',
    loadingMore: false,
    moreFailed: false
  };
}

/** The model before the first answer: every kind waiting. */
export function searchingSnapshot(term) {
  const kinds = {};
  KIND_ORDER.forEach(kind => {
    kinds[kind] = emptyKind();
  });
  return { term: normalizeTerm(term), status: 'searching', error: '', kinds };
}

function readItems(list) {
  return (Array.isArray(list) ? list : [])
    .filter(item => item && typeof item === 'object' && item.kind && item.id && item.row_key)
    .map(item => ({
      kind: String(item.kind),
      id: String(item.id),
      label: String(item.label || ''),
      rowKey: String(item.row_key),
      workspaceId: String((item.workspace && item.workspace.id) || ''),
      workspaceName: String((item.workspace && item.workspace.name) || ''),
      location: Array.isArray(item.location) ? item.location.map(String) : [],
      modified: item.modified ? String(item.modified) : '',
      path: item.path ? String(item.path) : '',
      size: Number.isFinite(item.size) ? item.size : null
    }));
}

function readKind(entry, previous, { append }) {
  const status = ['ok', 'partial', 'unavailable'].includes(entry && entry.status)
    ? entry.status
    : 'unavailable';
  if (status === 'unavailable') return { ...emptyKind(), status: 'unavailable' };
  const items = readItems(entry.items);
  return {
    status,
    count: Number.isFinite(entry.count) ? entry.count : items.length,
    items: append && previous ? previous.items.concat(items) : items,
    nextCursor: entry.next_cursor ? String(entry.next_cursor) : '',
    partialReason: entry.partial_reason ? String(entry.partial_reason) : '',
    loadingMore: false,
    moreFailed: false
  };
}

/**
 * Fold a server answer into the snapshot, returning a new one.
 *
 * `append` adds the answer's items to the kind's existing ones ("More
 * results"); otherwise the kind is replaced. A kind the answer does not mention
 * is left as it was.
 */
export function applyAnswer(snapshot, answer, { append = false } = {}) {
  const kinds = { ...snapshot.kinds };
  (answer && Array.isArray(answer.kinds) ? answer.kinds : []).forEach(entry => {
    const kind = entry && entry.kind;
    if (!KIND_ORDER.includes(kind)) return;
    kinds[kind] = readKind(entry, snapshot.kinds[kind], { append });
  });
  return { ...snapshot, status: 'done', error: '', kinds };
}

/** Every kind unavailable, the request itself having failed. */
export function failedSnapshot(snapshot, message) {
  return {
    ...snapshot,
    status: 'failed',
    error: message || 'Search is unavailable right now.',
    kinds: Object.fromEntries(KIND_ORDER.map(kind => [kind, emptyKind()]))
  };
}

/** How many results are shown right now, across every kind. */
export function resultCount(snapshot) {
  if (!snapshot) return 0;
  return KIND_ORDER.reduce(
    (sum, kind) => sum + (snapshot.kinds[kind] ? snapshot.kinds[kind].items.length : 0),
    0
  );
}

/**
 * How many results the search found, across every kind: the groups' own counts,
 * not just the rows loaded so far (a group shows 25 until "More results").
 * `atLeast` is true when a kind was only partly searched, so the number is a
 * floor.
 */
export function totalFound(snapshot) {
  if (!snapshot) return { total: 0, atLeast: false };
  let total = 0;
  let atLeast = false;
  KIND_ORDER.forEach(kind => {
    const state = snapshot.kinds[kind];
    if (!state || state.status === 'unavailable' || state.status === 'loading') return;
    total += Number.isFinite(state.count) ? state.count : state.items.length;
    if (state.status === 'partial') atLeast = true;
  });
  return { total, atLeast };
}

// ---------------------------------------------------------------------------
// The controller
// ---------------------------------------------------------------------------

/**
 * One search box's behaviour.
 *
 *   input(text)    the box's text changed; shows "searching" now, asks the
 *                  server after a pause, and drops whatever was in flight
 *   clear()        the box was cleared; nothing is searched, nothing shown
 *   loadMore(kind) "More results" for one kind
 *   retry(kind)    Retry for one kind that could not be read; 'all' for the
 *                  whole request
 *   snapshot()     the latest model, or null with no term
 *   dispose()      cancel everything
 *
 * `onChange(snapshot, reason)` is called with reason 'typed' (the term just
 * changed: show the Results tab now), 'results', 'more', 'retry' or 'cleared'.
 *
 * A request counts only if nothing newer has started: each search takes a
 * generation number and an AbortController, and an answer from an older
 * generation is thrown away whether or not the abort reached it.
 */
export function createFilesSearch({
  onChange,
  getScope = () => null,
  fetchImpl = (...args) => fetch(...args),
  setTimeoutImpl = (fn, ms) => setTimeout(fn, ms),
  clearTimeoutImpl = id => clearTimeout(id),
  debounceMs = DEBOUNCE_MS
} = {}) {
  let term = '';
  let current = null;
  let generation = 0;
  let timer = null;
  let inflight = new Set();

  function emit(reason) {
    if (typeof onChange === 'function') onChange(current, reason);
  }

  function cancelAll() {
    generation += 1;
    if (timer !== null) {
      clearTimeoutImpl(timer);
      timer = null;
    }
    inflight.forEach(controller => controller.abort());
    inflight = new Set();
  }

  async function post(body, gen) {
    const controller = new AbortController();
    inflight.add(controller);
    try {
      const response = await fetchImpl(SEARCH_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        // The term must not be kept anywhere: not in the HTTP cache, not in a
        // referrer, not in the address.
        cache: 'no-store',
        credentials: 'same-origin',
        referrerPolicy: 'no-referrer',
        signal: controller.signal
      });
      if (!response.ok) throw new Error('search failed');
      const answer = await response.json();
      return gen === generation ? { answer } : { stale: true };
    } catch (err) {
      if (gen !== generation || (err && err.name === 'AbortError')) return { stale: true };
      return { error: true };
    } finally {
      inflight.delete(controller);
    }
  }

  async function run() {
    timer = null;
    const gen = generation;
    const sent = term;
    const result = await post(requestBody({ term: sent, scope: getScope() }), gen);
    if (result.stale) return;
    current = result.error ? failedSnapshot(current, '') : applyAnswer(current, result.answer);
    emit('results');
  }

  function clear() {
    const had = term !== '' || current !== null;
    cancelAll();
    term = '';
    current = null;
    if (had) emit('cleared');
  }

  function input(text) {
    const next = normalizeTerm(text);
    if (next === '') {
      clear();
      return;
    }
    if (next === term && current) return; // only spaces changed
    cancelAll();
    term = next;
    current = searchingSnapshot(term);
    emit('typed');
    const gen = generation;
    timer = setTimeoutImpl(() => {
      if (gen === generation) void run();
    }, debounceMs);
  }

  return {
    input,
    clear,

    async loadMore(kind) {
      const state = current && current.kinds[kind];
      if (!state || !state.nextCursor || state.loadingMore) return;
      const gen = generation;
      current = {
        ...current,
        kinds: { ...current.kinds, [kind]: { ...state, loadingMore: true, moreFailed: false } }
      };
      emit('more');
      const result = await post(
        requestBody({
          term,
          scope: getScope(),
          kinds: [kind],
          cursors: { [kind]: state.nextCursor }
        }),
        gen
      );
      if (result.stale) return;
      if (result.error) {
        const now = current.kinds[kind];
        current = {
          ...current,
          kinds: { ...current.kinds, [kind]: { ...now, loadingMore: false, moreFailed: true } }
        };
      } else {
        current = applyAnswer(current, result.answer, { append: true });
      }
      emit('more');
    },

    async retry(kind) {
      if (!current) return;
      const gen = generation;
      if (kind === 'all') {
        current = searchingSnapshot(term);
        emit('retry');
        const result = await post(requestBody({ term, scope: getScope() }), gen);
        if (result.stale) return;
        current = result.error ? failedSnapshot(current, '') : applyAnswer(current, result.answer);
        emit('retry');
        return;
      }
      if (!KIND_ORDER.includes(kind)) return;
      current = { ...current, kinds: { ...current.kinds, [kind]: emptyKind() } };
      emit('retry');
      const result = await post(requestBody({ term, scope: getScope(), kinds: [kind] }), gen);
      if (result.stale) return;
      if (result.error) {
        current = {
          ...current,
          kinds: { ...current.kinds, [kind]: { ...emptyKind(), status: 'unavailable' } }
        };
      } else {
        current = applyAnswer(current, result.answer);
      }
      emit('retry');
    },

    snapshot: () => current,
    term: () => term,
    dispose() {
      cancelAll();
      term = '';
      current = null;
    }
  };
}

// ---------------------------------------------------------------------------
// The Results tab
// ---------------------------------------------------------------------------

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** The label on the Results tab: `Results for "term"`. */
export function resultsTabLabel(snapshot) {
  return `Results for “${snapshot ? snapshot.term : ''}”`;
}

/** The tab strip entry for the Results tab. Selected whenever the user types. */
export function renderResultsTabHTML(snapshot, active) {
  const title = esc(resultsTabLabel(snapshot));
  return (
    `<div class="cockpit-pane-tab cockpit-pane-tab--results${active ? ' is-active' : ''}" role="presentation">` +
    `<button type="button" class="nav-link cockpit-pane-tab-label${active ? ' active' : ''}" role="tab" ` +
    `data-pane-tab="${esc(RESULTS_TAB_KEY)}" aria-selected="${active ? 'true' : 'false'}" ` +
    `aria-controls="cockpitPanePanel" tabindex="${active ? '0' : '-1'}" title="${title}">${title}</button>` +
    '</div>'
  );
}

/** A date for a result row, or '' when it has none or it does not read. */
export function formatResultDate(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

const PARTIAL_WORDS = {
  too_many_items: 'There is more here than one search can read, so some may be missing.',
  too_many_files: 'There are more files than one search can read, so some may be missing.',
  timed_out: 'The search ran out of time, so some may be missing.',
  unreadable: 'Some of this could not be read, so some may be missing.'
};

/** What to say about a kind that was only partly searched. */
export function partialWords(reason) {
  return PARTIAL_WORDS[reason] || 'This was only partly searched, so some may be missing.';
}

/**
 * The line that says what the search is doing or found, for the live region and
 * the top of the tab. Never an empty list standing in for a state.
 */
export function statusLine(snapshot) {
  if (!snapshot) return '';
  if (snapshot.status === 'searching') return 'Searching…';
  if (snapshot.status === 'failed') return snapshot.error;
  const { total, atLeast } = totalFound(snapshot);
  const unavailable = KIND_ORDER.filter(kind => snapshot.kinds[kind].status === 'unavailable');
  if (total === 0) {
    return unavailable.length
      ? `Nothing named “${snapshot.term}” in what could be searched.`
      : `Nothing named “${snapshot.term}”`;
  }
  // What was found, not what is on screen: a group shows 25 until More results.
  if (total === 1 && !atLeast) return '1 result';
  return `${total}${atLeast ? '+' : ''} results`;
}

// The results are walked with the arrow keys, so exactly one open button is a
// tab stop (the first), as in the tree.
function resultRowHTML(item, tabbable) {
  const icon = RESULT_ICONS[item.kind] || 'file';
  const date = formatResultDate(item.modified);
  // A top-level workspace or group has no groups above it: say what it is
  // rather than leaving the column empty.
  const where =
    item.location.join(' › ') ||
    (item.kind === 'group' ? 'Group' : item.kind === 'workspace' ? 'Workspace' : '');
  return (
    `<li class="cockpit-result" data-result-kind="${esc(item.kind)}" data-result-key="${esc(item.rowKey)}">` +
    `<button type="button" class="cockpit-result-open" data-result-open data-result-kind="${esc(item.kind)}" ` +
    `data-result-key="${esc(item.rowKey)}" tabindex="${tabbable ? '0' : '-1'}">` +
    `<span class="cockpit-result-icon">${iconHTML(icon, { size: 15 })}</span>` +
    `<span class="cockpit-result-name">${esc(item.label)}</span>` +
    `<span class="cockpit-result-where">${esc(where)}</span>` +
    `<span class="cockpit-result-date">${date ? `<time datetime="${esc(item.modified)}">${esc(date)}</time>` : ''}</span>` +
    '</button>' +
    `<button type="button" class="modern-btn modern-btn-quiet modern-btn-sm cockpit-result-reveal" ` +
    `data-result-reveal data-result-kind="${esc(item.kind)}" data-result-key="${esc(item.rowKey)}" ` +
    `aria-label="Show ${esc(item.label)} in tree">Show in tree</button>` +
    '</li>'
  );
}

function groupHTML(kind, state, firstTabbable) {
  const label = kindLabel(kind);
  const partial = state.status === 'partial';
  const count = `${state.count}${partial ? '+' : ''}`;
  let html =
    `<section class="cockpit-results-group" data-results-group="${esc(kind)}" aria-label="${esc(label)}">` +
    `<h4 class="cockpit-results-heading">${esc(label)} <span class="cockpit-results-count">${esc(count)}</span></h4>` +
    '<ul class="cockpit-results-list" role="list">' +
    state.items.map((item, index) => resultRowHTML(item, firstTabbable && index === 0)).join('') +
    '</ul>';
  if (partial) {
    html += `<p class="cockpit-results-note" role="note">${esc(partialWords(state.partialReason))}</p>`;
  }
  if (state.nextCursor) {
    html += state.loadingMore
      ? '<p class="cockpit-results-note" role="status">Loading more…</p>'
      : '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm cockpit-results-more" ' +
        `data-result-more="${esc(kind)}">More results</button>`;
    if (state.moreFailed) {
      html += `<p class="cockpit-results-note" role="alert">Couldn’t load more ${esc(label.toLowerCase())}. Try again.</p>`;
    }
  }
  return html + '</section>';
}

function unavailableHTML(kind) {
  const name = KIND_UNAVAILABLE_NAMES[kind] || kindLabel(kind);
  return (
    `<p class="cockpit-results-unavailable" data-results-unavailable="${esc(kind)}" role="status">` +
    `${esc(name)} couldn’t be searched right now. ` +
    `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-retry="${esc(kind)}">Retry</button></p>`
  );
}

/**
 * The Results tab's panel.
 *
 * `options.note` is one line shown under the status when part of what could be
 * searched is not covered, for example that linked-folder filenames are only
 * searched on the Mac that runs Ori.
 */
export function renderResultsHTML(snapshot, options = {}) {
  if (!snapshot) return '';
  const title = esc(resultsTabLabel(snapshot));
  let html =
    '<div class="cockpit-results" data-results>' +
    `<h3 class="cockpit-results-title" data-pane-title tabindex="-1">${title}</h3>` +
    `<p class="cockpit-results-status" role="status" aria-live="polite">${esc(statusLine(snapshot))}</p>`;
  if (options.note) html += `<p class="cockpit-results-note">${esc(options.note)}</p>`;

  if (snapshot.status === 'failed') {
    html +=
      '<p class="cockpit-results-unavailable" role="alert">' +
      `${esc(snapshot.error)} ` +
      '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-retry="all">Retry</button></p>';
    return html + '</div>';
  }
  if (snapshot.status === 'searching') return html + '</div>';

  let first = true;
  KIND_ORDER.forEach(kind => {
    const state = snapshot.kinds[kind];
    if (state.status === 'unavailable') {
      html += unavailableHTML(kind);
    } else if (state.items.length) {
      html += groupHTML(kind, state, first);
      first = false;
    }
  });
  return html + '</div>';
}

/** The result a click or key named, or null (the snapshot may have moved on). */
export function findResult(snapshot, kind, rowKey) {
  if (!snapshot) return null;
  for (const group of KIND_ORDER) {
    const hit = snapshot.kinds[group].items.find(
      item => item.kind === kind && item.rowKey === rowKey
    );
    if (hit) return hit;
  }
  return null;
}

/**
 * The next result button to focus. `index` is where focus is now (-1 for the
 * search box), `count` how many there are. Stepping up from the first result
 * returns to the box (-1); stepping down from the last stays on it.
 */
export function stepResult(index, delta, count) {
  if (count <= 0) return -1;
  const next = index + delta;
  if (next < -1) return -1;
  if (next >= count) return count - 1;
  return next;
}

/**
 * The tab a result opens: the same tab its tree row opens (the key is the row's
 * own key), carrying what that tab's loader needs.
 */
export function tabFromResult(item) {
  const base = {
    key: item.rowKey,
    kind: item.kind,
    workspaceId: item.workspaceId,
    label: item.label,
    meta: {}
  };
  switch (item.kind) {
    case 'workspace':
    case 'group':
      return {
        ...base,
        key: item.workspaceId || item.id,
        workspaceId: item.workspaceId || item.id
      };
    case 'note':
      return { ...base, meta: { noteId: item.id, updatedAt: item.modified } };
    case 'ticket':
      return { ...base, meta: { ticketId: item.id } };
    case 'chat':
      return { ...base, meta: { chatId: item.id, updatedAt: item.modified } };
    case 'agent':
      return { ...base, meta: { name: item.id } };
    case 'file':
    case 'output':
      return {
        ...base,
        meta: { path: item.path || item.id, ...(item.size == null ? {} : { size: item.size }) }
      };
    default:
      return base;
  }
}
