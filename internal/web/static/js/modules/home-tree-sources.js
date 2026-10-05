// home-tree-sources.js — where the Home file tree gets a workspace's contents.
//
// PRD: tasks/prd-home-file-tree.md, sections 4.B, 4.C and 7 ("Data sources").
//
// The Tree view on Home expands a workspace or a group into sections: Notes,
// Backlog, Files, Outputs, Memory and Agents. Each section comes from its own
// endpoint, and each endpoint answers in its own format. This module is the
// only place that knows those formats. It fetches a section and turns the
// answer into rows of ONE common shape, so the tree renderer never reads a
// response:
//
//   { id, kind, label, meta, children, workspaceId }
//
//   id          unique key for the row; it is also the key of the row's tab
//   kind        'note' | 'ticket' | 'file' | 'folder' | 'output' |
//               'outputFolder' | 'agent' | 'more'
//   label       the text the row shows
//   meta        facts the row or the pane needs (a ticket's state, a file's path)
//   children    child rows; only a folder has any
//   workspaceId the workspace or group the row belongs to
//
// A group is a workspace of kind `group`, and every endpoint here accepts a
// group's id exactly as it accepts a workspace's (checked in task 1.1 of each
// release).
//
// A new section is an entry in SECTIONS and LOADERS and a shaper; the renderer
// does not change.
//
// Everything except the fetch itself is a pure function, so
// home-tree-sources.test.js runs under plain Node with `fetch` stubbed.

export const SECTION_NOTES = 'notes';
export const SECTION_BACKLOG = 'backlog';
export const SECTION_FILES = 'files';
export const SECTION_OUTPUTS = 'outputs';
export const SECTION_LINKED = 'linked';
export const SECTION_CHATS = 'chats';
export const SECTION_MEMORY = 'memory';
export const SECTION_AGENTS = 'agents';

/**
 * The sections of a workspace, in the order the tree shows them (FR9).
 *
 * `expandable: false` marks a section that is a single row you open rather
 * than a list you expand (Memory). `empty` is the dimmed line a workspace
 * shows when the section holds nothing (FR13). `optional` marks a section
 * FR9 shows "only when not empty": a workspace hides it while it holds
 * nothing, the way a group hides every empty section.
 */
export const SECTIONS = [
  { id: SECTION_NOTES, label: 'Notes', empty: 'No notes yet', expandable: true },
  { id: SECTION_BACKLOG, label: 'Backlog', empty: 'No tickets yet', expandable: true },
  { id: SECTION_FILES, label: 'Files', empty: 'No files yet', expandable: true },
  {
    id: SECTION_OUTPUTS,
    label: 'Outputs',
    empty: 'No outputs yet',
    expandable: true,
    optional: true
  },
  {
    id: SECTION_LINKED,
    label: 'Linked folders',
    empty: 'No linked folders',
    expandable: true,
    optional: true
  },
  { id: SECTION_CHATS, label: 'Chats', empty: 'No chats yet', expandable: true, optional: true },
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
  // A file and a folder under outputs/. They have prefixes of their own so an
  // output never shares a key with a file of the same path under files/.
  output: 'o',
  outputFolder: 'od',
  // A folder outside the workspace that it links to, and the folders and
  // files inside one. Their item id starts with the linked folder's own id:
  // `ws1/lf/<directory id>/docs/plan.md`.
  linked: 'l',
  linkedFolder: 'ld',
  linkedFile: 'lf',
  chat: 'c',
  memory: 'm',
  agent: 'a',
  more: 'more',
  loading: 'loading',
  failed: 'failed',
  empty: 'empty',
  // The row where a new note or ticket is being named.
  draft: 'draft'
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
  if (kind === 'output' || kind === 'outputFolder') return SECTION_OUTPUTS;
  if (kind === 'linked' || kind === 'linkedFolder' || kind === 'linkedFile') return SECTION_LINKED;
  if (kind === 'chat') return SECTION_CHATS;
  if (kind === 'memory') return SECTION_MEMORY;
  if (kind === 'agent') return SECTION_AGENTS;
  return '';
}

// Each kind of file row and the kind of folder it sits in. Files, outputs and
// the contents of a linked folder are the same nested listing under different
// kinds. (A linked folder's own row, `linked`, is not one of these: it is a
// row of its section, and its contents load when it is opened.)
const FOLDER_KIND_OF = {
  file: 'folder',
  folder: 'folder',
  output: 'outputFolder',
  outputFolder: 'outputFolder',
  linkedFile: 'linkedFolder',
  linkedFolder: 'linkedFolder'
};

/** Whether a row is a folder you expand, in whichever section it lives. */
export function isFolderKind(kind) {
  return !!FOLDER_KIND_OF[kind] && FOLDER_KIND_OF[kind] === kind;
}

/** The folder kind a file or folder row's parents have; '' for other rows. */
export function folderKindOf(kind) {
  return FOLDER_KIND_OF[kind] || '';
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
    (sum, entry) => sum + (isFolderKind(entry.kind) ? countFiles(entry.children) : 1),
    0
  );
}

/**
 * A flat file listing → nested folder and file rows of one kind.
 *
 * The listings here are FLAT: each entry carries its `relative_path`
 * (`docs/plan.md`) and `is_dir`. A folder may be listed on its own or only
 * implied by a file's path, so every ancestor of every entry is created on
 * demand. Folders sort before files, each by name (FR10, FR12).
 *
 * `fileKind` is the kind of the file rows ('file', 'output', 'linkedFile');
 * their folders get the matching folder kind. `scope` goes in front of every
 * row's path in its key, and `extra` into every row's `meta`: a linked
 * folder's rows carry its directory id in both.
 */
function nestFileRows(workspaceId, entries, fileKind, { scope = '', extra = {} } = {}) {
  const folderKind = FOLDER_KIND_OF[fileKind];
  const idOf = path => (scope ? `${scope}/${path}` : path);
  const top = [];
  const folders = new Map();

  const folderFor = path => {
    if (path === '') return null;
    if (folders.has(path)) return folders.get(path);
    const cut = path.lastIndexOf('/');
    const folder = row(workspaceId, folderKind, idOf(path), path.slice(cut + 1), {
      ...extra,
      path,
      fileCount: 0
    });
    folders.set(path, folder);
    const parent = folderFor(cut < 0 ? '' : path.slice(0, cut));
    (parent ? parent.children : top).push(folder);
    return folder;
  };

  (Array.isArray(entries) ? entries : []).forEach(entry => {
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
      row(workspaceId, fileKind, idOf(path), text(entry.name) || path.slice(cut + 1), {
        ...extra,
        path,
        url: text(entry.url),
        size: Number.isFinite(Number(entry.size)) ? Number(entry.size) : null
      })
    );
  });

  const finish = rows => {
    rows.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === folderKind ? -1 : 1;
      return byName(a, b);
    });
    rows.forEach(entry => {
      if (entry.kind !== folderKind) return;
      finish(entry.children);
      entry.meta.fileCount = countFiles(entry.children);
    });
    return rows;
  };

  const rows = finish(top);
  return { rows, count: countFiles(rows) };
}

/** `GET /api/workspaces/{id}/files/tree` → nested folder and file rows. */
export function filesToRows(workspaceId, payload) {
  return nestFileRows(workspaceId, payload && payload.files, 'file');
}

/**
 * `GET /api/workspaces/{id}/outputs/tree` → nested folder and file rows.
 *
 * The answer has the Files listing's shape, rooted at the workspace's
 * `outputs/` folder (what task runs saved). The rows are the Files rows under
 * kinds of their own, `output` and `outputFolder`.
 */
export function outputsToRows(workspaceId, payload) {
  return nestFileRows(workspaceId, payload && payload.files, 'output');
}

// --- Linked folders -------------------------------------------------------

// A path as it is compared: forward slashes, no slash at the end.
function comparablePath(path) {
  return String(path || '')
    .replace(/\\/g, '/')
    .replace(/\/+$/, '');
}

/** Whether `path` is `folder` itself or somewhere inside it. */
export function isInsideFolder(path, folder) {
  const inner = comparablePath(path);
  const outer = comparablePath(folder);
  if (!inner || !outer) return false;
  return inner === outer || inner.startsWith(`${outer}/`);
}

/**
 * The workspace's own folder, worked out from where it keeps its outputs
 * (`GET …/output-dir` answers `<workspace folder>/outputs`). '' when the
 * answer does not have that shape.
 */
export function ownFolderOf(payload) {
  const outputs = comparablePath(payload && payload.output_dir);
  const cut = outputs.lastIndexOf('/');
  return cut > 0 ? outputs.slice(0, cut) : '';
}

/**
 * `GET /api/workspaces/{id}/directories` → one row per folder the workspace
 * links to, by name.
 *
 * Every workspace is created with a directory reference to its OWN folder (a
 * group: to its own `files/`). That is not an outside folder, and shown here
 * it would repeat Notes, Files and Outputs, so any reference at or inside
 * `ownFolder` is left out. A linked folder's files are NOT part of this: they
 * load when its row is opened (`loadLinkedFolder`), so the section's count is
 * the number of folders.
 */
export function linkedToRows(workspaceId, payload, ownFolder = '') {
  const directories = Array.isArray(payload && payload.directories) ? payload.directories : [];
  const rows = directories
    .filter(dir => dir && dir.id && !isInsideFolder(dir.path, ownFolder))
    .map(dir => {
      const path = text(dir.path);
      const name = text(dir.name) || comparablePath(path).split('/').pop() || 'Folder';
      return row(workspaceId, 'linked', String(dir.id), name, { dirId: String(dir.id), path });
    })
    .sort(byName);
  return { rows, count: rows.length };
}

/**
 * `GET …/directories/{dirId}/files` → the nested rows inside one linked
 * folder.
 *
 * The answer is the whole folder in one flat list, with nothing hidden, so
 * what Files hides is hidden here: dot-files and dot-folders (`.git`),
 * `workspace.json` and lock files. A folder in it that is itself a registered
 * workspace is an ordinary folder here. `dirName` is the linked folder's
 * name, kept on each row for the pane's breadcrumb.
 */
export function linkedFilesToRows(workspaceId, dirId, payload, dirName = '') {
  return nestFileRows(workspaceId, payload && payload.files, 'linkedFile', {
    scope: String(dirId),
    extra: { dirId: String(dirId), dirName: text(dirName) }
  });
}

// --- Chats ----------------------------------------------------------------

/**
 * How many chats the Chats section asks for: one more than it shows, which is
 * how "more than 100" is told from "exactly 100" (FR14). The list's own
 * default is 50.
 */
export const CHAT_LIST_LIMIT = SECTION_ROW_LIMIT + 1;

/**
 * `GET /api/sessions?folder_id={id}` → one row per chat, in the API's order,
 * which is newest first by when it was last updated (FR9).
 *
 * `total` is every chat the workspace has, and can exceed the rows returned;
 * that is what the "Open workspace to see all N" row reports.
 */
export function chatsToRows(workspaceId, payload) {
  const sessions = Array.isArray(payload && payload.sessions) ? payload.sessions : [];
  const rows = sessions
    .filter(session => session && session.id)
    .map(session =>
      row(workspaceId, 'chat', String(session.id), text(session.title) || 'Untitled chat', {
        chatId: String(session.id),
        agentName: text(session.agent_name),
        updatedAt: text(session.updated_at)
      })
    );
  const total = Number(payload && payload.total);
  return {
    rows,
    count: Number.isFinite(total) && total > rows.length ? Math.trunc(total) : rows.length
  };
}

/** A chat tab shows this many messages: the last ones (decision D12). */
export const CHAT_MESSAGE_LIMIT = 20;

// The roles a chat tab shows. A stored chat can also hold `system` messages
// (instructions, not conversation); those are left out.
const SHOWN_CHAT_ROLES = new Set(['user', 'assistant']);

/**
 * One whole chat (`GET /api/sessions/{id}`) → what its tab shows.
 *
 * The last `CHAT_MESSAGE_LIMIT` messages that someone wrote — you, or the
 * agent — in the order they were sent. `earlier` says there were more before
 * them; the rest is behind "Open chat".
 */
export function chatToView(session, fallbackId = '') {
  const data = session || {};
  const written = (Array.isArray(data.messages) ? data.messages : []).filter(
    message => message && SHOWN_CHAT_ROLES.has(text(message.role)) && text(message.content)
  );
  return {
    id: String(data.id || fallbackId),
    workspaceId: text(data.folder_id),
    title: text(data.title) || 'Untitled chat',
    agentName: text(data.agent_name),
    updatedAt: text(data.updated_at),
    earlier: written.length > CHAT_MESSAGE_LIMIT,
    messages: written.slice(-CHAT_MESSAGE_LIMIT).map(message => ({
      id: String(message.id || ''),
      role: text(message.role),
      text: String(message.content),
      at: text(message.created_at)
    }))
  };
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

// The sections whose rows are files nested in folders. Their items are the
// files, however deep, not the rows at the top.
const NESTED_SECTIONS = new Set([SECTION_FILES, SECTION_OUTPUTS]);

/**
 * Keep the first `limit` files of a nested file list, in the order the tree
 * shows them. Folders are kept only as far as they are needed to reach a kept
 * file, plus empty folders that come before the limit is reached.
 */
function capFileRows(rows, budget) {
  const kept = [];
  for (const entry of rows) {
    if (budget.left <= 0) break;
    if (!isFolderKind(entry.kind)) {
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
  const nested = NESTED_SECTIONS.has(sectionId);
  const held = nested ? countFiles(result.rows) : result.rows.length;
  const count = Math.max(Number(result.count) || 0, held);
  result.count = count;
  if (held <= limit && count <= held) return result;

  let rows = result.rows;
  if (held > limit) {
    rows = nested ? capFileRows(rows, { left: limit }) : rows.slice(0, limit);
  }
  result.rows = [...rows, moreRow(workspaceId, sectionId, sectionId, count)];
  return result;
}

// The row that ends a list cut short. `scope` makes its key: the section's id,
// or `linked/<directory id>` for the files of one linked folder.
function moreRow(workspaceId, scope, sectionId, count) {
  return row(workspaceId, 'more', scope, `Open workspace to see all ${count}`, {
    section: sectionId,
    total: count
  });
}

/**
 * Apply the row cap to the files of ONE linked folder: the first `limit`
 * files, then "Open workspace to see all N". The workspace page has the full
 * explorer for a linked folder.
 */
export function capLinkedFolder(workspaceId, dirId, shaped, limit = SECTION_ROW_LIMIT) {
  const held = countFiles(shaped.rows);
  if (held <= limit) return { ...shaped, count: held };
  return {
    ...shaped,
    count: held,
    rows: [
      ...capFileRows(shaped.rows, { left: limit }),
      moreRow(workspaceId, `${SECTION_LINKED}/${dirId}`, SECTION_LINKED, held)
    ]
  };
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

const enc = encodeURIComponent;

// A section is loaded from one address and shaped (`url` + `shape`), or — when
// it needs more than one answer — by a `load` function of its own.
const LOADERS = {
  [SECTION_NOTES]: { url: id => `/api/workspaces/${enc(id)}/notes`, shape: notesToRows },
  [SECTION_BACKLOG]: { url: id => `/api/workspaces/${enc(id)}/tickets`, shape: ticketsToRows },
  [SECTION_FILES]: { url: id => `/api/workspaces/${enc(id)}/files/tree`, shape: filesToRows },
  [SECTION_OUTPUTS]: {
    url: id => `/api/workspaces/${enc(id)}/outputs/tree`,
    shape: outputsToRows
  },
  // The linked folders, and where the workspace's own folder is so that the
  // reference to it can be left out. Both are small answers; neither lists a
  // file.
  [SECTION_LINKED]: {
    load: async (id, fetchImpl) => {
      const [directories, outputDir] = await Promise.all([
        getJSON(`/api/workspaces/${enc(id)}/directories`, fetchImpl),
        getJSON(`/api/workspaces/${enc(id)}/output-dir`, fetchImpl)
      ]);
      return linkedToRows(id, directories, ownFolderOf(outputDir));
    }
  },
  [SECTION_CHATS]: {
    url: id => `/api/sessions?folder_id=${enc(id)}&limit=${CHAT_LIST_LIMIT}`,
    shape: chatsToRows
  },
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

/**
 * The error for a response that was not OK: the server's reason as its
 * message, and the HTTP status as `status`, so a caller can tell an item that
 * is gone (404) from a request that failed.
 */
export async function responseError(response) {
  const error = new Error(await responseErrorMessage(response, `HTTP ${response.status}`));
  error.status = response.status;
  return error;
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
  const shaped = loader.load
    ? await loader.load(workspaceId, fetchImpl)
    : loader.shape(workspaceId, await getJSON(loader.url(workspaceId), fetchImpl));
  return capSection(workspaceId, sectionId, shaped);
}

/**
 * Fetch the files of ONE linked folder and return its capped, nested rows.
 *
 * This is the request that walks the whole outside folder, so it is made only
 * for a folder whose row has been opened — never when a workspace is
 * expanded, and never by the filter.
 */
export async function loadLinkedFolder(workspaceId, dirId, { name = '', fetchImpl } = {}) {
  const payload = await getJSON(
    `/api/workspaces/${enc(workspaceId)}/directories/${enc(dirId)}/files`,
    fetchImpl
  );
  return capLinkedFolder(workspaceId, dirId, linkedFilesToRows(workspaceId, dirId, payload, name));
}

/**
 * The least time between two reloads caused by coming back to the browser tab
 * (FR21). Nothing is pushed from the server, so returning is when the tree
 * catches up — but flicking between windows must not reload it every time.
 */
export const RETURN_RELOAD_INTERVAL = 30000;

/** Whether enough time has passed since the last reload-on-return. */
export function shouldReloadOnReturn(now, last, interval = RETURN_RELOAD_INTERVAL) {
  if (!Number.isFinite(now)) return false;
  if (!Number.isFinite(last)) return true;
  return now - last >= interval;
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
    throw await responseError(response);
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

/**
 * `GET /api/sessions/{id}` → what the chat tab shows.
 *
 * The whole chat comes back in this one answer, messages included, and a chat
 * that has been deleted answers 404 — which is how its remembered tab is
 * dropped. (`GET /api/sessions/{id}/messages` answers 200 with no messages
 * for a chat that does not exist, so it cannot tell a deleted chat from an
 * empty one.)
 */
export async function loadChat(chatId, { fetchImpl } = {}) {
  return chatToView(await getJSON(`/api/sessions/${enc(chatId)}`, fetchImpl), chatId);
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
  return `/api/workspaces/${enc(workspaceId)}/files/${encodedPath(path)}`;
}

function encodedPath(path) {
  return String(path || '')
    .split('/')
    .filter(Boolean)
    .map(enc)
    .join('/');
}

/** The address one of a workspace's outputs is read from. */
export function workspaceOutputURL(workspaceId, path) {
  return `/api/workspaces/${enc(workspaceId)}/outputs/${encodedPath(path)}`;
}

/**
 * What a file tab needs to draw a preview of the file at `url`.
 *
 * Only Markdown and other text are fetched; an image is shown straight from
 * its address and anything else has nothing to fetch. `tooLarge` is set
 * instead of fetching a text file over the preview limit.
 */
async function loadPreviewFrom(url, path, { size = null, fetchImpl, kind: givenKind } = {}) {
  const kind = givenKind || filePreviewKind(path);
  const preview = { kind, url, text: '', tooLarge: false };
  if (kind !== PREVIEW_MARKDOWN && kind !== PREVIEW_TEXT) return preview;
  if (Number.isFinite(size) && size > FILE_PREVIEW_LIMIT) {
    return { ...preview, tooLarge: true };
  }
  const response = await resolveFetch(fetchImpl)(url);
  if (!response.ok) {
    throw await responseError(response);
  }
  const body = await response.text();
  if (body.length > FILE_PREVIEW_LIMIT) return { ...preview, tooLarge: true };
  return { ...preview, text: body };
}

/** The preview of a file under `files/` (FR34). */
export function loadFilePreview(workspaceId, path, options = {}) {
  return loadPreviewFrom(workspaceFileURL(workspaceId, path), path, options);
}

/** The preview of a file under `outputs/`: the Files rules, another address (FR35). */
export function loadOutputPreview(workspaceId, path, options = {}) {
  return loadPreviewFrom(workspaceOutputURL(workspaceId, path), path, options);
}

/** The address a file inside a linked folder is read from. */
export function linkedFileURL(workspaceId, dirId, path) {
  return `/api/workspaces/${enc(workspaceId)}/directories/${enc(dirId)}/files/${encodedPath(path)}`;
}

/**
 * How a file inside a linked folder is previewed: the Files rules, except
 * that an SVG has no preview. The linked-folder endpoint serves every image
 * as plain bytes; a browser still draws a PNG or a JPEG from those, but it
 * draws an SVG only when it is served as one.
 */
export function linkedPreviewKind(path) {
  const kind = filePreviewKind(path);
  return kind === PREVIEW_IMAGE && /\.svg$/i.test(String(path || '')) ? PREVIEW_NONE : kind;
}

/**
 * The preview of a file inside a linked folder (FR35).
 *
 * The file is only ever fetched as text and shown as text, or drawn by an
 * `<img>`. Its address must never be navigated to or put in a frame: that
 * endpoint serves an `.html` file as a page.
 */
export function loadLinkedPreview(workspaceId, dirId, path, options = {}) {
  return loadPreviewFrom(linkedFileURL(workspaceId, dirId, path), path, {
    ...options,
    kind: linkedPreviewKind(path)
  });
}

/**
 * Show a workspace's outputs folder in the file manager
 * (`POST …/output-dir/open`). Like opening a file, it acts on the machine the
 * server runs on. The server creates the folder if it is not there yet.
 */
export async function showOutputsFolder(workspaceId, { fetchImpl } = {}) {
  const response = await resolveFetch(fetchImpl)(
    `/api/workspaces/${enc(workspaceId)}/output-dir/open`,
    { method: 'POST', headers: { Accept: 'application/json' } }
  );
  if (!response.ok) {
    throw await responseError(response);
  }
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
    throw await responseError(response);
  }
}

// ---------------------------------------------------------------------------
// Creating from the tree (FR47-FR49)
//
// Each of these rejects with the server's own reason, which is what the tree
// shows when a create or an upload is refused (FR51). "Workspace" below means a
// workspace or a group: all three endpoints accept either id.
// ---------------------------------------------------------------------------

async function postJSON(url, body, fetchImpl) {
  const response = await resolveFetch(fetchImpl)(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body)
  });
  if (!response.ok) {
    throw await responseError(response);
  }
  return response.json();
}

/**
 * `POST /api/workspaces/{id}/notes` → the new note's row.
 *
 * An empty name becomes "Untitled" (FR47); the server's own default would be
 * "Untitled Note".
 */
export async function createNote(workspaceId, name, { fetchImpl } = {}) {
  const data = await postJSON(
    `/api/workspaces/${enc(workspaceId)}/notes`,
    { name: text(name) || 'Untitled', content: '' },
    fetchImpl
  );
  const note = (data && data.note) || {};
  if (!note.id) throw new Error('The server did not return the new note.');
  return notesToRows(workspaceId, { notes: [note] }).rows[0];
}

/**
 * `POST /api/workspaces/{id}/tickets` → the new ticket's row.
 *
 * Tickets made from the tree start in the backlog and use the existing
 * `manual` source (decision D13).
 */
export async function createTicket(workspaceId, title, { fetchImpl } = {}) {
  const ticket = await postJSON(
    `/api/workspaces/${enc(workspaceId)}/tickets`,
    { title: text(title), state: 'backlog', source: 'manual' },
    fetchImpl
  );
  if (!ticket || !ticket.id) throw new Error('The server did not return the new ticket.');
  return ticketsToRows(workspaceId, { tickets: [ticket] }).rows[0];
}

/**
 * `POST /api/workspaces/{id}/files` (multipart) → the uploaded file's row.
 *
 * The file goes in the `file` field and the destination folder, when there is
 * one, in `folder_path` — the first name the server's
 * `workspaceFolderFormValue` looks for. With no folder it lands at the top of
 * `files/`. The server stores an upload under a prefixed name, so the row is
 * built from the path it reports, not from the name that was picked.
 */
export async function uploadFile(workspaceId, file, folderPath = '', { fetchImpl } = {}) {
  const form = new FormData();
  form.append('file', file);
  const folder = String(folderPath || '').replace(/^\/+|\/+$/g, '');
  if (folder) form.append('folder_path', folder);
  const response = await resolveFetch(fetchImpl)(`/api/workspaces/${enc(workspaceId)}/files`, {
    method: 'POST',
    body: form
  });
  if (!response.ok) {
    throw await responseError(response);
  }
  const data = await response.json();
  const meta = (data && data.attachment && data.attachment.file_meta) || {};
  const path = text(meta.relative_path);
  if (!path) throw new Error('The server did not return the uploaded file.');
  return filesToRowsFlat(workspaceId, path, meta);
}

// One file row, without the folder nesting `filesToRows` builds: it is opened
// as a tab, and the tree gets its folders from the reloaded listing.
function filesToRowsFlat(workspaceId, path, meta) {
  return row(workspaceId, 'file', path, path.split('/').pop(), {
    path,
    url: text(meta.url),
    size: Number.isFinite(Number(meta.size)) ? Number(meta.size) : null
  });
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
    throw await responseError(response);
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
    throw await responseError(response);
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
