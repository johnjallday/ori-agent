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

/** A folder still being indexed is asked again this often, a few times. */
export const INDEX_POLL_MS = 750;
const INDEX_MAX_POLLS = 8;

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

/**
 * The local filename index's side of the Files group: the files in linked and
 * searchable folders, which only the Mac that runs Ori can search. `status` is
 * 'off' where there is no such index, so the page can say so once.
 */
function emptyIndex(status) {
  return {
    status,
    items: [],
    next: '',
    sources: [],
    loadingMore: false,
    moreFailed: false,
    error: ''
  };
}

/**
 * The model before the first answer: every kind waiting. `index` says whether
 * the local filename index will be asked too (only on the Mac that runs Ori).
 */
export function searchingSnapshot(term, { index = false } = {}) {
  const kinds = {};
  KIND_ORDER.forEach(kind => {
    kinds[kind] = emptyKind();
  });
  return {
    term: normalizeTerm(term),
    status: 'searching',
    error: '',
    kinds,
    index: emptyIndex(index ? 'loading' : 'off'),
    // How many of the merged Files rows are on screen; More results adds a page.
    filesShown: PAGE_SIZE,
    // The file in a searchable folder that is selected, and what happened when
    // it was opened or revealed.
    selected: '',
    notice: '',
    // The result the notice is about, when it is about one.
    noticeAt: ''
  };
}

// ---------------------------------------------------------------------------
// Files in searchable folders (the local filename index)
// ---------------------------------------------------------------------------

/** What the filename index tells us about a folder it searches. */
function readSources(list) {
  return (Array.isArray(list) ? list : [])
    .filter(source => source && source.id)
    .map(source => ({
      id: String(source.id),
      label: String(source.label || ''),
      state: String(source.state || ''),
      generation: source.generation,
      workspaceId: String((source.workspace && source.workspace.id) || ''),
      workspaceName: String((source.workspace && source.workspace.name) || ''),
      // The linked folder's own id: how a source is matched to its tree row.
      link: String((source.workspace && source.workspace.link) || '')
    }));
}

/** A folder's name, with the start of its id when two folders share one. */
export function sourceNames(sources) {
  return new Map(
    sources.map(source => [
      source.id,
      sources.some(other => other.id !== source.id && other.label === source.label)
        ? `${source.label} · ${source.id.slice(0, 6)}`
        : source.label
    ])
  );
}

/** One filename-index hit as a result row. */
function indexItem(hit, source, names) {
  const parent = String(hit.parent || '');
  const relative = parent ? `${parent}/${hit.name}` : String(hit.name);
  const linked = source && source.workspaceId && source.link;
  return {
    kind: 'indexed',
    id: String(hit.id),
    generation: hit.generation,
    label: String(hit.name),
    // Not a tree row key: a file in a folder with no workspace has no row. This
    // only tells results apart.
    rowKey: `idx:${hit.id}`,
    // The row Show in tree goes to, when the folder is linked to a workspace.
    treeKey: linked ? `${source.workspaceId}/lf/${source.link}/${relative}` : '',
    workspaceId: source ? source.workspaceId : '',
    workspaceName: source ? source.workspaceName : '',
    location: [
      names.get(hit.source) || (source && source.label) || '',
      ...parent.split('/')
    ].filter(Boolean),
    modified: hit.modified ? String(hit.modified) : '',
    path: relative,
    size: null,
    reader: hit.reader ? String(hit.reader) : '',
    source: String(hit.source || '')
  };
}

/**
 * Fold the filename index's answer into the snapshot. `append` adds a page
 * ("More results"); otherwise the index slice is replaced.
 */
export function applyIndexAnswer(snapshot, answer, { append = false } = {}) {
  const sources = readSources(answer && answer.sources);
  const names = sourceNames(sources);
  const bySource = new Map(sources.map(source => [source.id, source]));
  const items = (answer && Array.isArray(answer.hits) ? answer.hits : [])
    .filter(hit => hit && hit.id && hit.name)
    .map(hit => indexItem(hit, bySource.get(hit.source), names));
  return {
    ...snapshot,
    index: {
      status: 'ok',
      items: append ? snapshot.index.items.concat(items) : items,
      next: answer && answer.next ? String(answer.next) : '',
      sources,
      loadingMore: false,
      moreFailed: false,
      error: ''
    }
  };
}

function time(value) {
  const at = Date.parse(value);
  return Number.isFinite(at) ? at : 0;
}

function lastTime(items) {
  return items.length ? time(items[items.length - 1].modified) : 0;
}

/**
 * The Files group: the files stored inside workspaces (from the server) and the
 * files in searchable folders (from the filename index), newest first.
 *
 * Each stream arrives newest first and a page at a time, so a page of one can
 * hold files older than the next page of the other. Only rows at least as new
 * as the oldest row already fetched from a stream that has more are final, and
 * only those are listed; the controller fetches more until a page of them is
 * ready or both streams are done.
 */
export function mergedFiles(snapshot) {
  const server = snapshot.kinds.files;
  const index = snapshot.index;
  const serverUsable = server.status === 'ok' || server.status === 'partial';
  const indexUsable = index.status === 'ok';
  const all = [...(serverUsable ? server.items : []), ...(indexUsable ? index.items : [])].sort(
    (a, b) => time(b.modified) - time(a.modified) || a.label.localeCompare(b.label)
  );
  const serverMore = serverUsable && !!server.nextCursor;
  const indexMore = indexUsable && !!index.next;
  let floor = -1;
  if (serverMore) floor = Math.max(floor, lastTime(server.items));
  if (indexMore) floor = Math.max(floor, lastTime(index.items));
  const final = floor < 0 ? all : all.filter(item => time(item.modified) >= floor);
  return { items: final, more: serverMore || indexMore, serverMore, indexMore };
}

/** How many files were found, and whether that is only a floor. */
export function filesFound(snapshot) {
  const server = snapshot.kinds.files;
  const serverUsable = server.status === 'ok' || server.status === 'partial';
  const count =
    (serverUsable ? (Number.isFinite(server.count) ? server.count : server.items.length) : 0) +
    (snapshot.index.status === 'ok' ? snapshot.index.items.length : 0);
  return {
    count,
    atLeast:
      server.status === 'partial' || (snapshot.index.status === 'ok' && !!snapshot.index.next)
  };
}

/** Whether a folder is still being indexed or was only partly indexed. */
export function partlyIndexed(snapshot) {
  return snapshot.index.sources.filter(source => source.state && source.state !== 'ready');
}

function readItems(list) {
  return (Array.isArray(list) ? list : [])
    .filter(item => item && typeof item === 'object' && item.kind && item.id && item.row_key)
    .map(item => ({
      kind: String(item.kind),
      id: String(item.id),
      label: String(item.label || ''),
      rowKey: String(item.row_key),
      // Where Show in tree goes: for these, the row's own key.
      treeKey: String(item.row_key),
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
  return (
    KIND_ORDER.reduce(
      (sum, kind) => sum + (snapshot.kinds[kind] ? snapshot.kinds[kind].items.length : 0),
      0
    ) + (snapshot.index ? snapshot.index.items.length : 0)
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
    if (kind === 'files') return; // counted below, with the filename index
    const state = snapshot.kinds[kind];
    if (!state || state.status === 'unavailable' || state.status === 'loading') return;
    total += Number.isFinite(state.count) ? state.count : state.items.length;
    if (state.status === 'partial') atLeast = true;
  });
  if (snapshot.kinds.files.status !== 'loading') {
    const files = filesFound(snapshot);
    total += files.count;
    if (files.atLeast) atLeast = true;
  }
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
  // The local filename index, on the Mac that runs Ori. Without these there is
  // no index to ask, and the page says linked-folder filenames are searched
  // only there. `indexSearch({ term, cursor })` answers `{ hits, next, sources }`;
  // `indexAct(op, { id, generation })` opens a file in its reader or reveals it;
  // `indexRefresh({ id, generation })` re-indexes one folder.
  indexSearch = null,
  indexAct = null,
  indexRefresh = null,
  fetchImpl = (...args) => fetch(...args),
  setTimeoutImpl = (fn, ms) => setTimeout(fn, ms),
  clearTimeoutImpl = id => clearTimeout(id),
  debounceMs = DEBOUNCE_MS,
  pollMs = INDEX_POLL_MS
} = {}) {
  let term = '';
  let current = null;
  let generation = 0;
  let timer = null;
  let pollTimer = null;
  let polls = 0;
  let filling = false;
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
    if (pollTimer !== null) {
      clearTimeoutImpl(pollTimer);
      pollTimer = null;
    }
    polls = 0;
    inflight.forEach(controller => controller.abort());
    inflight = new Set();
  }

  const messageOf = err =>
    err && err.message ? String(err.message) : 'That did not work. Try again.';

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

  // The filename index: one page at a time, like the server. Returns whether it
  // answered; a failed first page marks the index unavailable, a failed later
  // page only says More results did not work.
  async function queryIndex(gen, sent, { append = false } = {}) {
    if (!indexSearch) return true;
    const cursor = append ? current.index.next : '';
    if (append) current = { ...current, index: { ...current.index, loadingMore: true } };
    try {
      const answer = await indexSearch({ term: sent, cursor, scope: getScope() });
      if (gen !== generation) return false;
      current = applyIndexAnswer(current, answer, { append });
      schedulePoll(gen, sent);
      return true;
    } catch (err) {
      if (gen !== generation) return false;
      current = append
        ? { ...current, index: { ...current.index, loadingMore: false, moreFailed: true } }
        : { ...current, index: { ...emptyIndex('unavailable'), error: messageOf(err) } };
      return false;
    }
  }

  // A folder that is still being indexed answers with what it has so far; ask
  // again a few times, so the rest turns up without the user doing anything.
  function schedulePoll(gen, sent) {
    const busy = current.index.sources.some(
      source => source.state === 'mapping' || source.state === 'queued'
    );
    if (!busy || polls >= INDEX_MAX_POLLS || pollTimer !== null) return;
    polls += 1;
    pollTimer = setTimeoutImpl(async () => {
      pollTimer = null;
      if (gen !== generation) return;
      await queryIndex(gen, sent);
      if (gen !== generation) return;
      emit('results');
      await fillFiles(gen);
    }, pollMs);
  }

  // The server's next page of workspace files; true when it arrived.
  async function moreServerFiles(gen) {
    const files = current.kinds.files;
    current = {
      ...current,
      kinds: { ...current.kinds, files: { ...files, loadingMore: true, moreFailed: false } }
    };
    const result = await post(
      requestBody({
        term,
        scope: getScope(),
        kinds: ['files'],
        cursors: { files: files.nextCursor }
      }),
      gen
    );
    if (result.stale) return false;
    if (result.error) {
      const now = current.kinds.files;
      current = {
        ...current,
        kinds: { ...current.kinds, files: { ...now, loadingMore: false, moreFailed: true } }
      };
      return false;
    }
    current = applyAnswer(current, result.answer, { append: true });
    return true;
  }

  // Fetch more of either file stream until a page of final rows can be shown,
  // or both streams are done. Stops at the first page that fails, so a broken
  // stream cannot loop.
  async function fillFiles(gen) {
    if (filling) return;
    filling = true;
    try {
      for (;;) {
        if (gen !== generation || !current) return;
        const merged = mergedFiles(current);
        if (merged.items.length >= current.filesShown || !merged.more) return;
        const jobs = [];
        if (merged.serverMore) jobs.push(moreServerFiles(gen));
        if (merged.indexMore) jobs.push(queryIndex(gen, term, { append: true }));
        const arrived = await Promise.all(jobs);
        if (gen !== generation) return;
        emit('more');
        if (arrived.some(ok => !ok)) return;
      }
    } finally {
      filling = false;
    }
  }

  async function run() {
    timer = null;
    const gen = generation;
    const sent = term;
    const server = (async () => {
      const result = await post(requestBody({ term: sent, scope: getScope() }), gen);
      if (result.stale) return;
      current = result.error ? failedSnapshot(current, '') : applyAnswer(current, result.answer);
      emit('results');
    })();
    // Without a filename index (anywhere but the Mac that runs Ori) there is
    // nothing to ask, and nothing to announce.
    const index = indexSearch
      ? queryIndex(gen, sent).then(() => {
          if (gen === generation) emit('results');
        })
      : Promise.resolve();
    await Promise.all([server, index]);
    if (gen === generation) await fillFiles(gen);
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
    current = searchingSnapshot(term, { index: !!indexSearch });
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
      if (kind === 'files') {
        // The Files group is two streams merged newest first: show a page more,
        // fetching from either stream as that needs.
        if (!current) return;
        const gen = generation;
        current = { ...current, filesShown: current.filesShown + PAGE_SIZE };
        emit('more');
        await fillFiles(gen);
        if (gen === generation) emit('more');
        return;
      }
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
        current = searchingSnapshot(term, { index: !!indexSearch });
        emit('retry');
        await run();
        return;
      }
      // Retry for the filename index alone: the folders on this Mac.
      if (kind === 'index') {
        if (!indexSearch) return;
        current = { ...current, index: emptyIndex('loading') };
        emit('retry');
        await queryIndex(gen, term);
        if (gen !== generation) return;
        emit('retry');
        await fillFiles(gen);
        return;
      }
      if (!KIND_ORDER.includes(kind)) return;
      // Files is two sources: retry whichever could not be read.
      const again = [];
      if (kind !== 'files' || current.kinds.files.status === 'unavailable') {
        current = { ...current, kinds: { ...current.kinds, [kind]: emptyKind() } };
        again.push(
          (async () => {
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
          })()
        );
      }
      if (kind === 'files' && indexSearch && current.index.status === 'unavailable') {
        current = { ...current, index: emptyIndex('loading') };
        again.push(queryIndex(gen, term));
      }
      emit('retry');
      await Promise.all(again);
      if (gen !== generation) return;
      emit('retry');
      if (kind === 'files') await fillFiles(gen);
    },

    // A file in a searchable folder is selected rather than opened in a tab.
    select(rowKey) {
      if (!current) return;
      current = { ...current, selected: rowKey, notice: '', noticeAt: '' };
      emit('select');
    },

    // Open the selected file in its reader, or reveal it in Finder. The server
    // refuses a file that has moved or been replaced since it was indexed, and
    // says so in words; that is shown as it is.
    async dispatch(rowKey, op) {
      if (!current || !indexAct) return;
      const item = current.index.items.find(entry => entry.rowKey === rowKey);
      if (!item) return;
      const gen = generation;
      let notice;
      try {
        await indexAct(op, { id: item.id, generation: item.generation });
        notice = op === 'open' ? 'Sent to the document reader.' : 'Requested in Finder.';
      } catch (err) {
        notice = messageOf(err);
      }
      if (gen !== generation) return;
      current = { ...current, selected: rowKey, notice, noticeAt: rowKey };
      emit('notice');
    },

    // Re-index the folders that were only partly indexed, then ask again.
    async refreshIndex() {
      if (!current || !indexRefresh) return;
      const gen = generation;
      const stale = partlyIndexed(current);
      current = { ...current, notice: 'Refreshing the index…', noticeAt: '' };
      emit('notice');
      let failure = '';
      await Promise.all(
        stale.map(async source => {
          try {
            await indexRefresh({ id: source.id, generation: source.generation });
          } catch (err) {
            failure = messageOf(err);
          }
        })
      );
      if (gen !== generation) return;
      polls = 0;
      await queryIndex(gen, term);
      if (gen !== generation) return;
      current = { ...current, notice: failure, noticeAt: '' };
      emit('notice');
      await fillFiles(gen);
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
function resultRowHTML(item, tabbable, snapshot = null) {
  const icon = RESULT_ICONS[item.kind] || 'file';
  const date = formatResultDate(item.modified);
  const indexed = item.kind === 'indexed';
  const selected = indexed && !!snapshot && item.rowKey === snapshot.selected;
  // What opening or revealing this file did, said by the file it is about.
  const noticeHere = selected && snapshot.noticeAt === item.rowKey && snapshot.notice;
  // A top-level workspace or group has no groups above it: say what it is
  // rather than leaving the column empty.
  const where =
    item.location.join(' › ') ||
    (item.kind === 'group' ? 'Group' : item.kind === 'workspace' ? 'Workspace' : '');
  const revealButton = label =>
    `<button type="button" class="modern-btn modern-btn-quiet modern-btn-sm cockpit-result-reveal" ` +
    `data-result-reveal data-result-kind="${esc(item.kind)}" data-result-key="${esc(item.rowKey)}" ` +
    `aria-label="Show ${esc(item.label)} in tree">${label}</button>`;
  // A file in a searchable folder is selected, not opened in a tab; the actions
  // for it are shown under its row.
  const actions = selected
    ? `<div class="cockpit-result-actions" role="group" aria-label="Actions for ${esc(item.label)}">` +
      (item.reader
        ? `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-act="open" ` +
          `data-result-key="${esc(item.rowKey)}">Open in ${esc(item.reader)}</button>`
        : '') +
      `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-act="reveal" ` +
      `data-result-key="${esc(item.rowKey)}">Reveal in Finder</button>` +
      (item.treeKey ? revealButton('Show in tree') : '') +
      (noticeHere
        ? `<p class="cockpit-result-notice" role="status" data-results-notice>${esc(snapshot.notice)}</p>`
        : '') +
      '</div>'
    : '';
  return (
    `<li class="cockpit-result${selected ? ' is-selected' : ''}" data-result-kind="${esc(item.kind)}" data-result-key="${esc(item.rowKey)}">` +
    `<button type="button" class="cockpit-result-open" data-result-open${indexed ? ` data-result-indexed aria-pressed="${selected ? 'true' : 'false'}"` : ''} ` +
    `data-result-kind="${esc(item.kind)}" data-result-key="${esc(item.rowKey)}" tabindex="${tabbable ? '0' : '-1'}">` +
    `<span class="cockpit-result-icon">${iconHTML(icon, { size: 15 })}</span>` +
    `<span class="cockpit-result-name">${esc(item.label)}</span>` +
    `<span class="cockpit-result-where">${esc(where)}</span>` +
    `<span class="cockpit-result-date">${date ? `<time datetime="${esc(item.modified)}">${esc(date)}</time>` : ''}</span>` +
    '</button>' +
    // A file in a folder with no workspace has no row in the tree to show. A
    // selected file carries Show in tree in its own actions instead.
    (item.treeKey && !selected
      ? revealButton('Show in tree')
      : '<span class="cockpit-result-reveal-slot" aria-hidden="true"></span>') +
    actions +
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

function unavailableHTML(kind, name = KIND_UNAVAILABLE_NAMES[kind] || kindLabel(kind)) {
  return (
    `<p class="cockpit-results-unavailable" data-results-unavailable="${esc(kind)}" role="status">` +
    `${esc(name)} couldn’t be searched right now. ` +
    `<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-retry="${esc(kind)}">Retry</button></p>`
  );
}

/**
 * The Files group: workspace files and the files in searchable folders, merged
 * newest first, with a page shown at a time. Returns the markup and whether a
 * group was drawn (so the first result can be the tab stop).
 */
function filesBlockHTML(snapshot, firstTabbable) {
  const server = snapshot.kinds.files;
  const index = snapshot.index;
  const serverDown = server.status === 'unavailable';
  const indexDown = index.status === 'unavailable';
  const merged = mergedFiles(snapshot);
  const shown = merged.items.slice(0, snapshot.filesShown);
  let html = '';

  // Never an empty group standing in for a source that failed: say which part
  // could not be searched, and offer Retry for it.
  if (serverDown && indexDown) {
    html += unavailableHTML('files', 'Files');
  } else if (serverDown) {
    html += unavailableHTML(
      'files',
      index.status === 'off' ? 'Files' : 'Files stored in workspaces'
    );
  } else if (indexDown) {
    html += unavailableHTML('index', 'Files in your folders');
  }

  if (shown.length) {
    const { count, atLeast } = filesFound(snapshot);
    html +=
      `<section class="cockpit-results-group" data-results-group="files" aria-label="${esc(kindLabel('files'))}">` +
      `<h4 class="cockpit-results-heading">${esc(kindLabel('files'))} ` +
      `<span class="cockpit-results-count">${esc(`${count}${atLeast ? '+' : ''}`)}</span></h4>` +
      '<ul class="cockpit-results-list" role="list">' +
      shown.map((item, i) => resultRowHTML(item, firstTabbable && i === 0, snapshot)).join('') +
      '</ul>';
    if (server.status === 'partial') {
      html += `<p class="cockpit-results-note" role="note">${esc(partialWords(server.partialReason))}</p>`;
    }
    const loading = server.loadingMore || index.loadingMore;
    if (merged.items.length > snapshot.filesShown || merged.more) {
      html += loading
        ? '<p class="cockpit-results-note" role="status">Loading more…</p>'
        : '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm cockpit-results-more" ' +
          'data-result-more="files">More results</button>';
    }
    if (server.moreFailed || index.moreFailed) {
      html +=
        '<p class="cockpit-results-note" role="alert">Couldn’t load more files. Try again.</p>';
    }
    html += '</section>';
  }
  return { html, drawn: shown.length > 0 };
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
  // What opening or revealing a file did, or why it was refused.
  // It is said beside the file it is about when that file is on screen, so it
  // is never off screen above a long list; otherwise here.
  const noticeInRow =
    !!snapshot.noticeAt &&
    snapshot.noticeAt === snapshot.selected &&
    snapshot.index.items.some(item => item.rowKey === snapshot.noticeAt);
  if (snapshot.notice && !noticeInRow) {
    html += `<p class="cockpit-results-note" role="status" data-results-notice>${esc(snapshot.notice)}</p>`;
  }

  if (snapshot.status === 'failed') {
    html +=
      '<p class="cockpit-results-unavailable" role="alert">' +
      `${esc(snapshot.error)} ` +
      '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-retry="all">Retry</button></p>';
    return html + '</div>';
  }
  if (snapshot.status === 'searching') return html + '</div>';

  // Away from the Mac that runs Ori there is no filename index: say, once, what
  // that leaves out. Everything inside workspaces is still searched.
  if (snapshot.index.status === 'off')
    html += `<p class="cockpit-results-note" data-results-index-off>${esc(INDEX_OFF_WORDS)}</p>`;

  let first = true;
  KIND_ORDER.forEach(kind => {
    if (kind === 'files') {
      const files = filesBlockHTML(snapshot, first);
      html += files.html;
      if (files.drawn) first = false;
      return;
    }
    const state = snapshot.kinds[kind];
    if (state.status === 'unavailable') {
      html += unavailableHTML(kind);
    } else if (state.items.length) {
      html += groupHTML(kind, state, first);
      first = false;
    }
  });

  // The folders on this Mac, while they are still being asked.
  if (snapshot.index.status === 'loading') {
    html +=
      '<p class="cockpit-results-note" role="status" data-results-index-loading>Searching your folders…</p>';
  }
  // One notice under the results when a folder is only partly indexed.
  const partly = partlyIndexed(snapshot);
  if (snapshot.index.status === 'ok' && partly.length) {
    const names = [...sourceNames(snapshot.index.sources)]
      .filter(([id]) => partly.some(source => source.id === id))
      .map(([, name]) => name)
      .join(', ');
    html +=
      '<p class="cockpit-results-note" role="status" data-results-partly-indexed>' +
      `${partly.some(s => s.state === 'mapping' || s.state === 'queued') ? 'Still indexing' : 'Only partly indexed'}: ${esc(names)}, so some files may be missing. ` +
      '<button type="button" class="modern-btn modern-btn-secondary modern-btn-sm" data-result-index-refresh>Refresh index</button></p>';
  }
  return html + '</div>';
}

/** The one line said where there is no filename index. */
export const INDEX_OFF_WORDS =
  'Linked-folder filenames can be searched only on the Mac that runs Ori.';

/** The result a click or key named, or null (the snapshot may have moved on). */
export function findResult(snapshot, kind, rowKey) {
  if (!snapshot) return null;
  if (kind === 'indexed') return snapshot.index.items.find(item => item.rowKey === rowKey) || null;
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
