// home-tree-sources.js — where the Home file tree gets a workspace's contents.
//
// PRD: tasks/prd-home-file-tree.md, sections 4.B, 4.C and 7 ("Data sources").
//
// The Tree view on Home expands a workspace or a group into sections: Notes,
// Backlog, Files, Memory and Agents. Each section comes from its own endpoint,
// and each endpoint answers in its own format. This module is the only place
// that knows those formats. It fetches a section and turns the answer into
// rows of ONE common shape, so the tree renderer never reads a response:
//
//   { id, kind, label, meta, children, workspaceId }
//
//   id          unique key for the row; it is also the key of the row's tab
//   kind        'note' | 'ticket' | 'file' | 'folder' | 'agent' | 'more'
//   label       the text the row shows
//   meta        facts the row or the pane needs (a ticket's state, a file's path)
//   children    child rows; only a folder has any
//   workspaceId the workspace or group the row belongs to
//
// A group is a workspace of kind `group`, and every endpoint here accepts a
// group's id exactly as it accepts a workspace's (checked in task 1.1).
//
// Release 2 (Outputs, Linked folders, Chats) adds entries to SECTIONS and
// LOADERS; the renderer does not change.
//
// Everything except the fetch itself is a pure function, so
// home-tree-sources.test.js runs under plain Node with `fetch` stubbed.

export const SECTION_NOTES = 'notes';
export const SECTION_BACKLOG = 'backlog';
export const SECTION_FILES = 'files';
export const SECTION_MEMORY = 'memory';
export const SECTION_AGENTS = 'agents';

/**
 * The sections of a workspace, in the order the tree shows them (FR9).
 *
 * `expandable: false` marks a section that is a single row you open rather
 * than a list you expand (Memory). `empty` is the dimmed line a workspace
 * shows when the section holds nothing (FR13).
 */
export const SECTIONS = [
  { id: SECTION_NOTES, label: 'Notes', empty: 'No notes yet', expandable: true },
  { id: SECTION_BACKLOG, label: 'Backlog', empty: 'No tickets yet', expandable: true },
  { id: SECTION_FILES, label: 'Files', empty: 'No files yet', expandable: true },
  { id: SECTION_MEMORY, label: 'Memory', empty: '', expandable: false },
  { id: SECTION_AGENTS, label: 'Agents', empty: 'No agents yet', expandable: true }
];

export const SECTION_IDS = SECTIONS.map(section => section.id);

/** A section shows at most this many rows, then an "Open workspace" row (FR14). */
export const SECTION_ROW_LIMIT = 100;

export function sectionInfo(sectionId) {
  return SECTIONS.find(section => section.id === sectionId) || null;
}

// ---------------------------------------------------------------------------
// Row keys
// ---------------------------------------------------------------------------

// One letter per kind keeps a key short. A workspace or group row's key is the
// workspace id on its own, as it always has been; every row INSIDE a workspace
// starts with that id, so a key alone says which workspace to expand.
const KEY_PREFIX = {
  section: 's',
  note: 'n',
  ticket: 't',
  file: 'f',
  folder: 'd',
  memory: 'm',
  agent: 'a',
  more: 'more',
  loading: 'loading',
  failed: 'failed',
  empty: 'empty'
};

const KIND_BY_PREFIX = Object.fromEntries(
  Object.entries(KEY_PREFIX).map(([kind, prefix]) => [prefix, kind])
);

/** The key of a row inside a workspace, for example `ws1/n/note-9`. */
export function itemKey(workspaceId, kind, itemId = '') {
  const prefix = KEY_PREFIX[kind];
  if (!prefix) throw new Error(`home-tree-sources: unknown row kind "${kind}"`);
  const base = `${workspaceId}/${prefix}`;
  return itemId === '' ? base : `${base}/${itemId}`;
}

export function sectionKey(workspaceId, sectionId) {
  return itemKey(workspaceId, 'section', sectionId);
}

/**
 * Split a row key back into its parts.
 *
 * A key with no `/` is a workspace or group row. Anything this module did not
 * produce returns null, so a stored key from an older build is dropped rather
 * than guessed at.
 */
export function parseItemKey(key) {
  const text = String(key || '');
  if (!text) return null;
  const first = text.indexOf('/');
  if (first < 0) return { workspaceId: text, kind: 'workspace', itemId: '' };
  const workspaceId = text.slice(0, first);
  const rest = text.slice(first + 1);
  const second = rest.indexOf('/');
  const prefix = second < 0 ? rest : rest.slice(0, second);
  const kind = KIND_BY_PREFIX[prefix];
  if (!workspaceId || !kind) return null;
  return { workspaceId, kind, itemId: second < 0 ? '' : rest.slice(second + 1) };
}

/** The section a row kind lives in, or '' when it lives in none. */
export function sectionOfKind(kind) {
  if (kind === 'note') return SECTION_NOTES;
  if (kind === 'ticket') return SECTION_BACKLOG;
  if (kind === 'file' || kind === 'folder') return SECTION_FILES;
  if (kind === 'memory') return SECTION_MEMORY;
  if (kind === 'agent') return SECTION_AGENTS;
  return '';
}

// ---------------------------------------------------------------------------
// Shaping: one function per response format
// ---------------------------------------------------------------------------

function row(workspaceId, kind, itemId, label, meta, children = []) {
  return { id: itemKey(workspaceId, kind, itemId), kind, label, meta, children, workspaceId };
}

function byName(a, b) {
  return String(a.label).localeCompare(String(b.label), undefined, {
    numeric: true,
    sensitivity: 'base'
  });
}

function text(value) {
  return String(value ?? '').trim();
}

/** `GET /api/workspaces/{id}/notes` → rows sorted by name (FR9). */
export function notesToRows(workspaceId, payload) {
  const notes = Array.isArray(payload && payload.notes) ? payload.notes : [];
  const rows = notes
    .filter(note => note && note.id)
    .map(note =>
      row(workspaceId, 'note', String(note.id), text(note.name) || 'Untitled', {
        noteId: String(note.id),
        updatedAt: text(note.updated_at)
      })
    )
    .sort(byName);
  return { rows, count: rows.length };
}

// Tickets in these states are finished: the tree dims them (FR11, decision D7).
const FINISHED_TICKET_STATES = new Set(['done', 'cancelled']);

const TICKET_STATE_LABELS = {
  backlog: 'Backlog',
  ready: 'Ready',
  in_progress: 'In progress',
  review: 'Review',
  done: 'Done',
  cancelled: 'Cancelled'
};

/**
 * The words for a ticket state.
 *
 * The six known states use the PRD's wording ("In progress"); a state this
 * build has never heard of falls back to the server's own label, then to the
 * raw value, so a new state still shows as something.
 */
export function ticketStateLabel(state, serverLabel = '') {
  return TICKET_STATE_LABELS[text(state)] || text(serverLabel) || text(state) || 'Unknown';
}

export function isFinishedTicketState(state) {
  return FINISHED_TICKET_STATES.has(text(state));
}

const TICKET_SOURCE_LABELS = {
  manual: 'Added by you',
  assistant: 'Suggested by the assistant',
  home_quick_capture: 'Quick capture on Home',
  action_center: 'From the Action Center',
  backlog_markdown: 'Written in BACKLOG.md',
  note: 'Created from a note',
  blueprint_intake: 'Created during workspace setup',
  migration: 'Carried over from an earlier version'
};

/** Where a ticket came from, in words; an unknown source is shown as it is. */
export function ticketSourceLabel(source) {
  return TICKET_SOURCE_LABELS[text(source)] || text(source) || 'Unknown';
}

/**
 * `GET /api/workspaces/{id}/tickets` → rows in the API's own order (FR9).
 *
 * The list holds top-level tickets only; sub-tickets are out of scope. `total`
 * can exceed the rows returned when the server cut the list short, which is
 * what the "Open workspace to see all N" row reports.
 */
export function ticketsToRows(workspaceId, payload) {
  const tickets = Array.isArray(payload && payload.tickets) ? payload.tickets : [];
  const rows = tickets
    .filter(ticket => ticket && ticket.id)
    .map(ticket =>
      row(workspaceId, 'ticket', String(ticket.id), text(ticket.title) || 'Untitled ticket', {
        ticketId: String(ticket.id),
        state: text(ticket.state),
        stateLabel: ticketStateLabel(ticket.state, ticket.state_label),
        finished: isFinishedTicketState(ticket.state),
        number: text(ticket.display_number)
      })
    );
  const total = Number(payload && payload.total);
  return {
    rows,
    count: Number.isFinite(total) && total > rows.length ? Math.trunc(total) : rows.length
  };
}

/**
 * Whether a path must never appear in the tree (FR15).
 *
 * The listing endpoint already leaves out what the workspace page hides. This
 * is a second guard for the names the PRD lists by hand: anything inside a
 * dot-folder such as `.ori/`, dot-files, `workspace.json`, and lock files.
 */
export function isHiddenPath(relativePath) {
  const segments = String(relativePath || '')
    .split('/')
    .filter(Boolean);
  if (segments.length === 0) return true;
  if (segments.some(segment => segment.startsWith('.'))) return true;
  const name = segments[segments.length - 1].toLowerCase();
  return name === 'workspace.json' || name.endsWith('.lock');
}

/** The number of files under a list of file/folder rows, counting sub-folders. */
export function countFiles(rows) {
  return (Array.isArray(rows) ? rows : []).reduce(
    (sum, entry) => sum + (entry.kind === 'folder' ? countFiles(entry.children) : 1),
    0
  );
}

/**
 * `GET /api/workspaces/{id}/files/tree` → nested folder and file rows.
 *
 * The endpoint returns a FLAT list: each entry carries its `relative_path`
 * (`docs/plan.md`) and `is_dir`. A folder may be listed on its own or only
 * implied by a file's path, so every ancestor of every entry is created on
 * demand. Folders sort before files, each by name (FR10, FR12).
 */
export function filesToRows(workspaceId, payload) {
  const entries = Array.isArray(payload && payload.files) ? payload.files : [];
  const top = [];
  const folders = new Map();

  const folderFor = path => {
    if (path === '') return null;
    if (folders.has(path)) return folders.get(path);
    const cut = path.lastIndexOf('/');
    const folder = row(workspaceId, 'folder', path, path.slice(cut + 1), { path, fileCount: 0 });
    folders.set(path, folder);
    const parent = folderFor(cut < 0 ? '' : path.slice(0, cut));
    (parent ? parent.children : top).push(folder);
    return folder;
  };

  entries.forEach(entry => {
    if (!entry) return;
    const path = String(entry.relative_path || '').replace(/^\/+|\/+$/g, '');
    if (isHiddenPath(path)) return;
    if (entry.is_dir) {
      folderFor(path);
      return;
    }
    const cut = path.lastIndexOf('/');
    const parent = folderFor(cut < 0 ? '' : path.slice(0, cut));
    (parent ? parent.children : top).push(
      row(workspaceId, 'file', path, text(entry.name) || path.slice(cut + 1), {
        path,
        url: text(entry.url),
        size: Number.isFinite(Number(entry.size)) ? Number(entry.size) : null
      })
    );
  });

  const finish = rows => {
    rows.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === 'folder' ? -1 : 1;
      return byName(a, b);
    });
    rows.forEach(entry => {
      if (entry.kind !== 'folder') return;
      finish(entry.children);
      entry.meta.fileCount = countFiles(entry.children);
    });
    return rows;
  };

  const rows = finish(top);
  return { rows, count: countFiles(rows) };
}

/**
 * `GET /api/workspaces/{id}/memory` → no rows, one list for the pane.
 *
 * Memory is a single row in the tree (FR9), so it has no child rows. `entries`
 * is what its tab lists (FR37): the entries, then the learnings the user has
 * approved. The payload's `unstructured` lines are left out, as they are on
 * the workspace's Memory tab ("not injected as entries"): they are whatever
 * else MEMORY.md holds, such as its "# Workspace Memory" heading.
 */
export function memoryToRows(workspaceId, payload) {
  const data = payload || {};
  const entries = [];
  (Array.isArray(data.entries) ? data.entries : []).forEach(entry => {
    if (!entry || !text(entry.text)) return;
    entries.push({ text: text(entry.text), type: text(entry.type), date: text(entry.date) });
  });
  (Array.isArray(data.managed_learnings) ? data.managed_learnings : []).forEach(entry => {
    if (!entry || !text(entry.text)) return;
    entries.push({ text: text(entry.text), type: text(entry.type) || 'learned', date: '' });
  });
  return { rows: [], count: entries.length, entries };
}

/** `GET /api/workspaces/{id}/agents` → one row per agent, in the API's order. */
export function agentsToRows(workspaceId, payload) {
  const agents = Array.isArray(payload && payload.agents) ? payload.agents : [];
  const rows = agents
    .filter(agent => agent && text(agent.name))
    .map(agent =>
      row(workspaceId, 'agent', text(agent.name), text(agent.name), {
        name: text(agent.name),
        role: text(agent.role),
        model: text(agent.model),
        provider: text(agent.provider)
      })
    );
  return { rows, count: rows.length };
}

// ---------------------------------------------------------------------------
// The 100-row cap (FR14)
// ---------------------------------------------------------------------------

/**
 * Keep the first `limit` files of a nested file list, in the order the tree
 * shows them. Folders are kept only as far as they are needed to reach a kept
 * file, plus empty folders that come before the limit is reached.
 */
function capFileRows(rows, budget) {
  const kept = [];
  for (const entry of rows) {
    if (budget.left <= 0) break;
    if (entry.kind !== 'folder') {
      kept.push(entry);
      budget.left -= 1;
      continue;
    }
    kept.push({ ...entry, children: capFileRows(entry.children, budget) });
  }
  return kept;
}

/**
 * Apply the row cap to a shaped section.
 *
 * A section with more than `limit` items shows the first `limit` and a final
 * row, "Open workspace to see all N", which the tree turns into a link to the
 * workspace page. The same row appears when the server itself returned fewer
 * items than it has (the ticket list is paged), so the tree never looks
 * complete when it is not. `count` always stays the true number, so the
 * section's badge is honest about what the workspace holds.
 */
export function capSection(workspaceId, sectionId, shaped, limit = SECTION_ROW_LIMIT) {
  const result = { ...shaped };
  // A section that is one row (Memory) has no list to cut short.
  const info = sectionInfo(sectionId);
  if (info && !info.expandable) return result;
  const nested = sectionId === SECTION_FILES;
  const held = nested ? countFiles(result.rows) : result.rows.length;
  const count = Math.max(Number(result.count) || 0, held);
  result.count = count;
  if (held <= limit && count <= held) return result;

  let rows = result.rows;
  if (held > limit) {
    rows = nested ? capFileRows(rows, { left: limit }) : rows.slice(0, limit);
  }
  result.rows = [
    ...rows,
    row(workspaceId, 'more', sectionId, `Open workspace to see all ${count}`, {
      section: sectionId,
      total: count
    })
  ];
  return result;
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

const enc = encodeURIComponent;

const LOADERS = {
  [SECTION_NOTES]: { url: id => `/api/workspaces/${enc(id)}/notes`, shape: notesToRows },
  [SECTION_BACKLOG]: { url: id => `/api/workspaces/${enc(id)}/tickets`, shape: ticketsToRows },
  [SECTION_FILES]: { url: id => `/api/workspaces/${enc(id)}/files/tree`, shape: filesToRows },
  [SECTION_MEMORY]: { url: id => `/api/workspaces/${enc(id)}/memory`, shape: memoryToRows },
  [SECTION_AGENTS]: { url: id => `/api/workspaces/${enc(id)}/agents`, shape: agentsToRows }
};

/**
 * The reason a request failed, in the server's own words when it gave any.
 *
 * The APIs here answer errors as `{"message": …}` or `{"error": …}`; a body
 * that is neither is shown as it is, and an empty one falls back to the status.
 */
export async function responseErrorMessage(response, fallback) {
  let body = '';
  try {
    body = await response.text();
  } catch (_) {
    return fallback;
  }
  if (!body) return fallback;
  try {
    const parsed = JSON.parse(body);
    if (parsed && typeof parsed.message === 'string' && parsed.message) return parsed.message;
    if (parsed && typeof parsed.error === 'string' && parsed.error) return parsed.error;
  } catch (_) {
    // Not JSON: the text itself is the reason.
  }
  return body.length > 200 ? fallback : body;
}

function resolveFetch(fetchImpl) {
  const impl = fetchImpl || (typeof fetch === 'function' ? fetch : null);
  if (!impl) throw new Error('home-tree-sources: no fetch available');
  return impl;
}

/** Fetch one section of one workspace and return its capped rows. */
export async function loadSection(workspaceId, sectionId, { fetchImpl } = {}) {
  const loader = LOADERS[sectionId];
  if (!loader) throw new Error(`home-tree-sources: unknown section "${sectionId}"`);
  const response = await resolveFetch(fetchImpl)(loader.url(workspaceId), {
    headers: { Accept: 'application/json' }
  });
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
  const payload = await response.json();
  return capSection(workspaceId, sectionId, loader.shape(workspaceId, payload));
}

// The three states a section can be in. `count` is null until it is known, so
// a section that has not answered never claims to be empty.
export const SECTION_LOADING = 'loading';
export const SECTION_READY = 'ready';
export const SECTION_FAILED = 'failed';

export function loadingSection() {
  return { status: SECTION_LOADING, rows: [], count: null, error: '' };
}

function readySection(result) {
  return { ...result, status: SECTION_READY, error: '' };
}

function failedSection(err) {
  const message = err && err.message ? String(err.message) : 'Request failed';
  return { status: SECTION_FAILED, rows: [], count: null, error: message };
}

/**
 * Load a workspace's (or group's) sections in parallel.
 *
 * Every section is reported on its own through `onSection(sectionId, state)`:
 * once at the start as loading, then once more as ready or failed. One section
 * failing never delays or hides another (FR18, FR19). The returned promise
 * settles when all of them have answered and never rejects; it resolves to
 * `{ [sectionId]: state }`.
 *
 * `sections` narrows the load to a few sections, which is how one section is
 * retried or reloaded after a create.
 */
export function loadSections(workspace, { fetchImpl, sections, onSection } = {}) {
  const workspaceId = String((workspace && workspace.id) || workspace || '');
  const wanted = (Array.isArray(sections) && sections.length ? sections : SECTION_IDS).filter(
    sectionId => LOADERS[sectionId]
  );
  const states = {};
  const report = (sectionId, state) => {
    states[sectionId] = state;
    if (typeof onSection === 'function') onSection(sectionId, state);
  };

  if (!workspaceId) return Promise.resolve(states);
  wanted.forEach(sectionId => report(sectionId, loadingSection()));

  return Promise.all(
    wanted.map(sectionId =>
      loadSection(workspaceId, sectionId, { fetchImpl }).then(
        result => report(sectionId, readySection(result)),
        err => report(sectionId, failedSection(err))
      )
    )
  ).then(() => states);
}

// ---------------------------------------------------------------------------
// One item, for the pane
// ---------------------------------------------------------------------------

async function getJSON(url, fetchImpl) {
  const response = await resolveFetch(fetchImpl)(url, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
  return response.json();
}

/** `GET /api/workspaces/{id}/tickets/{ticketId}` → what the ticket tab shows. */
export async function loadTicket(workspaceId, ticketId, { fetchImpl } = {}) {
  const ticket = await getJSON(
    `/api/workspaces/${enc(workspaceId)}/tickets/${enc(ticketId)}`,
    fetchImpl
  );
  return {
    id: String(ticket.id || ticketId),
    title: text(ticket.title) || 'Untitled ticket',
    number: text(ticket.display_number),
    state: text(ticket.state),
    stateLabel: ticketStateLabel(ticket.state, ticket.state_label),
    finished: isFinishedTicketState(ticket.state),
    description: String(ticket.description ?? '').trim(),
    tags: Array.isArray(ticket.tags) ? ticket.tags.map(text).filter(Boolean) : [],
    source: text(ticket.source),
    sourceLabel: ticketSourceLabel(ticket.source)
  };
}

/** `GET /api/workspaces/{id}/memory` → the entries the Memory tab lists. */
export async function loadMemory(workspaceId, { fetchImpl } = {}) {
  const shaped = memoryToRows(
    workspaceId,
    await getJSON(`/api/workspaces/${enc(workspaceId)}/memory`, fetchImpl)
  );
  return { entries: shaped.entries };
}

// --- Files ---------------------------------------------------------------

const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown', 'mdx']);
const IMAGE_EXTENSIONS = new Set([
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'avif',
  'bmp',
  'ico',
  'svg'
]);
const TEXT_EXTENSIONS = new Set([
  'txt',
  'text',
  'log',
  'csv',
  'tsv',
  'json',
  'jsonl',
  'yaml',
  'yml',
  'toml',
  'ini',
  'conf',
  'cfg',
  'env',
  'xml',
  'html',
  'htm',
  'css',
  'js',
  'mjs',
  'ts',
  'tsx',
  'jsx',
  'go',
  'py',
  'rb',
  'rs',
  'java',
  'c',
  'h',
  'cpp',
  'sh',
  'bash',
  'zsh',
  'sql',
  'lua',
  'tex',
  'srt',
  'vtt',
  'rst'
]);

/** A text file larger than this is not fetched for preview. */
export const FILE_PREVIEW_LIMIT = 512 * 1024;

export const PREVIEW_MARKDOWN = 'markdown';
export const PREVIEW_TEXT = 'text';
export const PREVIEW_IMAGE = 'image';
export const PREVIEW_NONE = 'none';

/**
 * How a file is previewed, decided by its extension (FR34): Markdown is
 * rendered, other text (CSV and JSON included) is shown as it is, an image is
 * shown inline, and anything else has no preview.
 */
export function filePreviewKind(path) {
  const name = String(path || '')
    .split('/')
    .pop()
    .toLowerCase();
  const dot = name.lastIndexOf('.');
  const extension = dot > 0 ? name.slice(dot + 1) : '';
  if (MARKDOWN_EXTENSIONS.has(extension)) return PREVIEW_MARKDOWN;
  if (IMAGE_EXTENSIONS.has(extension)) return PREVIEW_IMAGE;
  if (TEXT_EXTENSIONS.has(extension)) return PREVIEW_TEXT;
  return PREVIEW_NONE;
}

/**
 * The address a workspace file is served from. Built here rather than taken
 * from the listing so that a name with a space, `#` or `?` is always encoded.
 */
export function workspaceFileURL(workspaceId, path) {
  const segments = String(path || '')
    .split('/')
    .filter(Boolean)
    .map(enc);
  return `/api/workspaces/${enc(workspaceId)}/files/${segments.join('/')}`;
}

/**
 * What the file tab needs to draw a preview.
 *
 * Only Markdown and other text are fetched; an image is shown straight from
 * its address and anything else has nothing to fetch. `tooLarge` is set
 * instead of fetching a text file over the preview limit.
 */
export async function loadFilePreview(workspaceId, path, { size = null, fetchImpl } = {}) {
  const kind = filePreviewKind(path);
  const url = workspaceFileURL(workspaceId, path);
  const preview = { kind, url, text: '', tooLarge: false };
  if (kind !== PREVIEW_MARKDOWN && kind !== PREVIEW_TEXT) return preview;
  if (Number.isFinite(size) && size > FILE_PREVIEW_LIMIT) {
    return { ...preview, tooLarge: true };
  }
  const response = await resolveFetch(fetchImpl)(url);
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
  const body = await response.text();
  if (body.length > FILE_PREVIEW_LIMIT) return { ...preview, tooLarge: true };
  return { ...preview, text: body };
}

/**
 * Open a file in its default application, or reveal it in the file manager
 * (`POST …/files/open`, `POST …/files/reveal`). Both act on the machine the
 * server runs on, which for Ori is the user's own.
 */
export async function openWorkspaceFile(workspaceId, path, { reveal = false, fetchImpl } = {}) {
  const response = await resolveFetch(fetchImpl)(
    `/api/workspaces/${enc(workspaceId)}/files/${reveal ? 'reveal' : 'open'}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ relative_path: path })
    }
  );
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
}

/**
 * `PUT /api/notes/{id}` with the note's text — the save the pane's editor
 * makes. Only `content` is sent, so the note's name and tags are left alone.
 *
 * `keepalive` lets the request outlive the page, for a save sent while the
 * page is closing; there is then no answer to wait for. Throws with the
 * server's reason when the save is refused.
 */
export async function saveNoteContent(noteId, content, { keepalive = false, fetchImpl } = {}) {
  const request = resolveFetch(fetchImpl)(`/api/notes/${enc(noteId)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content: String(content ?? '') }),
    keepalive
  });
  if (keepalive) return null;
  const response = await request;
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
  const data = await response.json();
  return { updatedAt: text(data && data.note && data.note.updated_at) };
}

/** `GET /api/notes/{id}` → the whole note, content included. */
export async function loadNote(noteId, { fetchImpl } = {}) {
  const response = await resolveFetch(fetchImpl)(`/api/notes/${enc(noteId)}`, {
    headers: { Accept: 'application/json' }
  });
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  }
  const note = await response.json();
  return {
    id: String(note.id || noteId),
    workspaceId: text(note.workspace_id),
    name: text(note.name) || 'Untitled',
    content: String(note.content ?? ''),
    tags: Array.isArray(note.tags) ? note.tags.map(text).filter(Boolean) : [],
    updatedAt: text(note.updated_at)
  };
}
