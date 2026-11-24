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
  return { status: SEARCH_STATUS_FAILED, byLink: new Map() };
}

/**
 * The list as read (`readSources` in home-files-search.js), keyed by the link
 * each source is connected to. A source with no workspace (a search-only
 * folder) has no row here.
 */
export function folderSearchFromSources(sources) {
  const byLink = new Map();
  (Array.isArray(sources) ? sources : []).forEach(source => {
    if (!source || !source.id || !source.workspaceId || !source.link) return;
    byLink.set(linkKey(source.workspaceId, source.link), source);
  });
  return { status: SEARCH_STATUS_READY, byLink };
}

/** Whether two answers would draw the same marks (so a redraw can be skipped). */
export function sameFolderSearch(a, b) {
  if (!a || !b || a.status !== b.status || a.byLink.size !== b.byLink.size) return false;
  for (const [key, source] of a.byLink) {
    const other = b.byLink.get(key);
    if (
      !other ||
      other.id !== source.id ||
      other.state !== source.state ||
      other.reason !== source.reason ||
      other.count !== source.count ||
      other.mappedAt !== source.mappedAt ||
      other.generation !== source.generation
    ) {
      return false;
    }
  }
  return true;
}

/**
 * The search state of one tree row, or null when the row says nothing: it is
 * not a linked folder, this is not the Mac that runs Ori, or the list has not
 * answered yet.
 *
 * `{ key, label, tone, source }`; `source` is the matched source, or null.
 */
export function folderSearchState(row, folderSearch) {
  if (!row || row.kind !== 'linked') return null;
  const status = folderSearch && folderSearch.status;
  if (status === SEARCH_STATUS_FAILED) return stateOf(FOLDER_STATUS_UNKNOWN, null);
  if (status !== SEARCH_STATUS_READY) return null;
  const dirId = row.meta && row.meta.dirId;
  const source = dirId ? folderSearch.byLink.get(linkKey(row.workspaceId, dirId)) : undefined;
  if (!source) return stateOf(FOLDER_NOT_SEARCHABLE, null);
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
