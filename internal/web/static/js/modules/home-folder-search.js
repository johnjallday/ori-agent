// home-folder-search.js — whether a linked folder is searchable, said on its row.
//
// Merge Tree into Files, PR 2 (requirements 32-33).
//
// The filename index lives on the Mac that runs Ori. Its `sources` answer lists
// the folders it searches; one of them belongs to a linked folder's row in Files
// when the source's saved connection names that row's workspace AND that row's
// link (`workspace.id` + `workspace.link`, the directory reference's id). Names
// and labels are never compared: two folders may share a name, and a workspace
// may be renamed.
//
// A linked folder with no source is not searchable. Nothing is said at all
// until the list has been read, nowhere but on the local Mac, and a list that
// could not be read says so: it never reads as "Not searchable".
//
// Pure parts only, exported for home-folder-search.test.js; no DOM, no fetch.

/** A row's search state: the mark after its name and the menu's status line. */
export const FOLDER_SEARCHABLE = 'searchable';
export const FOLDER_INDEXING = 'indexing';
export const FOLDER_PARTIAL = 'partial';
export const FOLDER_UNAVAILABLE = 'unavailable';
export const FOLDER_NOT_SEARCHABLE = 'not-searchable';
export const FOLDER_VERIFY = 'verify';
/** The list of searchable folders could not be read. */
export const FOLDER_STATUS_UNKNOWN = 'unknown';

/** Where the list of searchable folders stands for this Files visit. */
export const SEARCH_STATUS_OFF = 'off'; // not the Mac that runs Ori: no marks
export const SEARCH_STATUS_LOADING = 'loading'; // asked, not answered: no marks yet
export const SEARCH_STATUS_READY = 'ready';
export const SEARCH_STATUS_FAILED = 'failed';

// The short label after the row's name, and the tone its dot is drawn in.
const MARKS = {
  [FOLDER_SEARCHABLE]: { label: 'Searchable', tone: 'on' },
  [FOLDER_INDEXING]: { label: 'Indexing…', tone: 'busy' },
  [FOLDER_PARTIAL]: { label: 'Partly indexed', tone: 'part' },
  [FOLDER_UNAVAILABLE]: { label: 'Folder unavailable', tone: 'alert' },
  [FOLDER_NOT_SEARCHABLE]: { label: 'Not searchable', tone: 'off' },
  [FOLDER_VERIFY]: { label: 'Needs verifying', tone: 'part' },
  [FOLDER_STATUS_UNKNOWN]: { label: 'Status unavailable', tone: 'unknown' }
};

// The index's own `state` values (internal/filesearch) and the row state each
// one reads as. `interrupted` (Ori restarted mid-scan) kept what it had found,
// so it is a partial index whose reason says to refresh.
const ROW_STATE_OF = {
  ready: FOLDER_SEARCHABLE,
  queued: FOLDER_INDEXING,
  mapping: FOLDER_INDEXING,
  partial: FOLDER_PARTIAL,
  interrupted: FOLDER_PARTIAL,
  unavailable: FOLDER_UNAVAILABLE
};

// The index states whose filenames a search can find (filesearch QueryLocation).
const SEARCHABLE_INDEX_STATES = new Set(['ready', 'partial', 'mapping', 'interrupted']);

/** The key a source and a linked-folder row are matched by. */
export function linkKey(workspaceId, link) {
  return `${String(workspaceId || '')}\u0000${String(link || '')}`;
}

/** Before the list is asked for: nothing to say, or nothing yet. */
export function emptyFolderSearch({ local = false } = {}) {
  return { status: local ? SEARCH_STATUS_LOADING : SEARCH_STATUS_OFF, byLink: new Map() };
}

/** The list could not be read: every linked folder says its status is unknown. */
export function failedFolderSearch() {
  return { status: SEARCH_STATUS_FAILED, byLink: new Map(), bySource: new Map() };
}

/**
 * The list as read (`readSources` in home-files-search.js): every source by its
 * id, in the list's order, and those connected to a workspace link also by
 * that link. A source with no workspace is a search-only folder: it has a row
 * of its own in the Search-only folders group, never under a workspace.
 *
 * `verify` holds the keys of links the host has said need verifying before
 * they can be searched (an older link whose folder identity was not recorded).
 * The listing cannot tell; a Make searchable review does, and the row keeps
 * saying so for the rest of the visit.
 */
export function folderSearchFromSources(sources, { verify = null } = {}) {
  const byLink = new Map();
  const bySource = new Map();
  (Array.isArray(sources) ? sources : []).forEach(source => {
    if (!source || !source.id) return;
    bySource.set(source.id, source);
    if (source.workspaceId && source.link) {
      byLink.set(linkKey(source.workspaceId, source.link), source);
    }
  });
  return { status: SEARCH_STATUS_READY, byLink, bySource, verify: new Set(verify || []) };
}

/** The searchable folders with no workspace, in the list's order. */
export function searchOnlySources(folderSearch) {
  if (!folderSearch || folderSearch.status !== SEARCH_STATUS_READY || !folderSearch.bySource) {
    return [];
  }
  return [...folderSearch.bySource.values()].filter(source => !source.workspaceId);
}

/** Whether two answers would draw the same rows and marks (so a redraw can be skipped). */
export function sameFolderSearch(a, b) {
  if (!a || !b || a.status !== b.status) return false;
  const sourcesA = a.bySource || new Map();
  const sourcesB = b.bySource || new Map();
  if (sourcesA.size !== sourcesB.size) return false;
  const verifyA = a.verify || new Set();
  const verifyB = b.verify || new Set();
  if (verifyA.size !== verifyB.size || [...verifyA].some(key => !verifyB.has(key))) return false;
  const idsA = [...sourcesA.keys()];
  const idsB = [...sourcesB.keys()];
  if (idsA.some((id, index) => idsB[index] !== id)) return false;
  for (const [id, source] of sourcesA) {
    const other = sourcesB.get(id);
    if (
      !other ||
      other.label !== source.label ||
      other.state !== source.state ||
      other.reason !== source.reason ||
      other.count !== source.count ||
      other.mappedAt !== source.mappedAt ||
      other.generation !== source.generation ||
      other.workspaceId !== source.workspaceId ||
      other.link !== source.link
    ) {
      return false;
    }
  }
  return true;
}

/**
 * The search state of one tree row, or null when the row says nothing: it is
 * not a linked or search-only folder, this is not the Mac that runs Ori, or
 * the list has not answered yet.
 *
 * `{ key, label, tone, source, searchOnly }`; `source` is the matched source,
 * or null.
 */
export function folderSearchState(row, folderSearch) {
  if (!row) return null;
  if (row.kind === 'searchOnly') {
    const ready = folderSearch && folderSearch.status === SEARCH_STATUS_READY;
    const source =
      ready && folderSearch.bySource && folderSearch.bySource.get(row.meta && row.meta.sourceId);
    if (!source) return null;
    return {
      ...stateOf(ROW_STATE_OF[source.state] || FOLDER_STATUS_UNKNOWN, source),
      searchOnly: true
    };
  }
  if (row.kind !== 'linked') return null;
  const status = folderSearch && folderSearch.status;
  if (status === SEARCH_STATUS_FAILED) return stateOf(FOLDER_STATUS_UNKNOWN, null);
  if (status !== SEARCH_STATUS_READY) return null;
  const dirId = row.meta && row.meta.dirId;
  const key = linkKey(row.workspaceId, dirId);
  const source = dirId ? folderSearch.byLink.get(key) : undefined;
  if (!source) {
    const verify = !!dirId && !!folderSearch.verify && folderSearch.verify.has(key);
    return stateOf(verify ? FOLDER_VERIFY : FOLDER_NOT_SEARCHABLE, null);
  }
  return stateOf(ROW_STATE_OF[source.state] || FOLDER_STATUS_UNKNOWN, source);
}

function stateOf(key, source) {
  const mark = MARKS[key];
  return { key, label: mark.label, tone: mark.tone, source };
}

/** Whether "Search in this folder" can find anything in it right now. */
export function canSearchFolder(state) {
  return !!(state && state.source && SEARCHABLE_INDEX_STATES.has(state.source.state));
}

/** What a linked folder's menu can do about its search (requirement 33). */
export const FOLDER_ACTION_SEARCH_IN = 'search-in';
export const FOLDER_ACTION_MAKE_SEARCHABLE = 'make-searchable';
export const FOLDER_ACTION_REFRESH = 'refresh-index';
export const FOLDER_ACTION_REMOVE = 'remove-from-search';
/** A search-only folder only: link it to a workspace, keeping its index. */
export const FOLDER_ACTION_CONNECT = 'connect-workspace';

/**
 * The actions that apply to a row's state, in menu order; none for a row with
 * no state or an unknown one. A folder being indexed cannot be refreshed (the
 * index refuses a second scan), and a folder that is unavailable has nothing
 * to search but can be refreshed once it is back, or removed.
 *
 * A search-only folder (requirement 38) is searchable by definition: its menu
 * swaps Make searchable for Connect to workspace, which the host can only
 * review while the folder is there.
 */
export function folderActions(state) {
  if (!state) return [];
  const actions = [];
  if (canSearchFolder(state)) actions.push(FOLDER_ACTION_SEARCH_IN);
  if (state.searchOnly) {
    if (state.key === FOLDER_STATUS_UNKNOWN) return [];
    if (state.key !== FOLDER_UNAVAILABLE) actions.push(FOLDER_ACTION_CONNECT);
    if (state.key !== FOLDER_INDEXING) actions.push(FOLDER_ACTION_REFRESH);
    actions.push(FOLDER_ACTION_REMOVE);
    return actions;
  }
  switch (state.key) {
    case FOLDER_NOT_SEARCHABLE:
    case FOLDER_VERIFY:
      actions.push(FOLDER_ACTION_MAKE_SEARCHABLE);
      break;
    case FOLDER_SEARCHABLE:
    case FOLDER_PARTIAL:
    case FOLDER_UNAVAILABLE:
      actions.push(FOLDER_ACTION_REFRESH, FOLDER_ACTION_REMOVE);
      break;
    case FOLDER_INDEXING:
      actions.push(FOLDER_ACTION_REMOVE);
      break;
    default:
      break;
  }
  return actions;
}

/** Whether a source is still being indexed, so its row is asked again soon. */
export function isIndexing(source) {
  return !!source && (source.state === 'queued' || source.state === 'mapping');
}

/**
 * What is said once a folder this page started indexing has finished:
 * searchable, partly indexed and why, or unavailable and what to do.
 */
export function indexOutcome(name, source, locale) {
  const reason = String((source && source.reason) || '').trim();
  const count = filenameCount(source && source.count, locale);
  switch (source && source.state) {
    case 'ready':
      return `${name} is searchable: ${count}.`;
    case 'partial':
    case 'interrupted':
      return `${name} is partly indexed: ${count}.${reason ? ` ${reason}` : ''}`;
    case 'unavailable':
      return `${name} is unavailable.${reason ? ` ${reason}` : ''}`;
    default:
      return '';
  }
}

/** "1 filename", "1,284 filenames". */
export function filenameCount(count, locale) {
  const n = Math.max(0, Math.floor(Number(count) || 0));
  return `${n.toLocaleString(locale)} ${n === 1 ? 'filename' : 'filenames'}`;
}

/**
 * When a folder was last indexed, from the index's Unix seconds: "22 Nov,
 * 09:14", with the year when it is not this year. '' when it never finished.
 */
export function indexedAt(seconds, { locale, timeZone, now = Date.now() } = {}) {
  const at = Number(seconds);
  if (!Number.isFinite(at) || at <= 0) return '';
  const date = new Date(at * 1000);
  const options = { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' };
  if (timeZone) options.timeZone = timeZone;
  const yearOf = d =>
    Number(new Intl.DateTimeFormat('en', { year: 'numeric', timeZone }).format(d));
  if (yearOf(date) !== yearOf(new Date(now))) options.year = 'numeric';
  return new Intl.DateTimeFormat(locale, options).format(date);
}

/**
 * The status line that starts a linked folder's menu: `title` is the state in
 * words, `detail` the file count and when it was last indexed (or what to do
 * next), and `reason` why an index is partial. Empty strings are left out.
 */
export function folderStatusLines(state, options = {}) {
  if (!state) return null;
  const source = state.source;
  const counted = () => {
    const when = indexedAt(source.mappedAt, options);
    return [filenameCount(source.count, options.locale), when ? `indexed ${when}` : '']
      .filter(Boolean)
      .join(' · ');
  };
  switch (state.key) {
    case FOLDER_SEARCHABLE:
      return { title: state.label, detail: counted(), reason: '' };
    case FOLDER_PARTIAL:
      return { title: state.label, detail: counted(), reason: String(source.reason || '') };
    case FOLDER_INDEXING:
      return {
        title: state.label,
        detail:
          source.state === 'queued'
            ? 'Waiting to start'
            : `${filenameCount(source.count, options.locale)} so far`,
        reason: ''
      };
    case FOLDER_UNAVAILABLE:
      return {
        title: state.label,
        detail: String(source.reason || 'Ori cannot read this folder right now.'),
        reason: ''
      };
    case FOLDER_NOT_SEARCHABLE:
      return { title: state.label, detail: 'Files search does not look inside it.', reason: '' };
    case FOLDER_VERIFY:
      return {
        title: state.label,
        detail: 'Confirm this is the folder the workspace links to.',
        reason: ''
      };
    default:
      return {
        title: 'Search status unavailable',
        detail: 'Ori could not read which folders are searchable. Open Files again to retry.',
        reason: ''
      };
  }
}
