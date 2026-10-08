// Browser references and display-only historical attribution. Only the host
// resolves these references; a visible label is never permission or ownership.

export function collectWorkspaceContext({
  pathname = '/',
  workspaceId = '',
  workspaceSlug = '',
  selectionWorkspaceId = '',
  taskId = '',
  sessionId = '',
  origin = 'personal_assistant_panel'
} = {}) {
  const path = String(pathname || '/');
  const match = path.match(/^\/workspaces\/([^/]+)(?:\/([^/]+))?(?:\/([^/]+))?/);
  const out = {
    context_version: 1,
    surface: 'app',
    page_path: path,
    workspace_id: '',
    workspace_slug: '',
    selection_workspace_id: '',
    task_id: '',
    session_id: String(sessionId || ''),
    origin
  };
  if (match) {
    try {
      out.workspace_slug = decodeURIComponent(match[1]);
    } catch (_) {
      out.workspace_slug = match[1];
    }
    // A route token is a slug, never an ID. Drop a stale page publication when
    // pushState/back-forward points at a different workspace.
    if (String(workspaceSlug) === out.workspace_slug) out.workspace_id = String(workspaceId || '');
    // The app's task page is /workspaces/<slug>/task/<id>; the plural is kept
    // for older references.
    out.surface =
      match[2] === 'canvas'
        ? 'workspace_canvas'
        : ['task', 'tasks'].includes(match[2]) && match[3]
          ? 'workspace_task'
          : 'workspace_detail';
    if (out.surface === 'workspace_task') {
      try {
        out.task_id = decodeURIComponent(match[3]);
      } catch (_) {
        out.task_id = match[3];
      }
    }
  } else if (path === '/') {
    out.surface = 'home';
    out.selection_workspace_id = String(selectionWorkspaceId || '');
    out.workspace_id = out.selection_workspace_id; // Canonical ID for legacy local Guide routing.
    if (out.workspace_id) out.task_id = String(taskId || '');
  } else if (path === '/workspaces' || path === '/workspaces/') out.surface = 'workspace_hub';
  return out;
}

export function workspaceContextLabel(context, { historical = false } = {}) {
  if (!context || context.version !== 1)
    return historical ? 'Earlier workspace unknown' : 'Checking workspace context…';
  if (!['available', 'historical'].includes(context.status)) return 'Workspace context unavailable';
  const subject = context.subject;
  if (!subject) return 'App-wide';
  const parent = context.parent || context.overview?.parent;
  const kind = subject.kind === 'home' ? 'Home' : subject.kind === 'group' ? 'Group' : 'Project';
  const parts = [kind + ': ' + String(subject.name || 'Unnamed workspace')];
  if (parent) parts.push(String(parent.name || 'Parent'));
  if (context.selected_task_id || context.overview?.selected_task) parts.push('Selected task');
  if (context.location && context.location.id !== subject.id)
    parts.push('from ' + String(context.location.name || 'current page'));
  return parts.join(' · ');
}

export function renderTurnWorkspace(row, attribution, { historical = false } = {}) {
  if (!row?.ownerDocument || !row.append) return false;
  let label = row.querySelector?.('.personal-assistant-message__context');
  if (!label) {
    label = row.ownerDocument.createElement('p');
    label.className = 'personal-assistant-message__context';
    // The existing row is a horizontal flex container. Keep attribution in
    // its bubble rather than shrinking the answer beside a long label.
    (row.firstElementChild || row).append(label);
  }
  label.textContent = workspaceContextLabel(attribution, { historical });
  if (attribution?.subject?.id) label.dataset.workspaceId = String(attribution.subject.id);
  return true;
}

const SOURCE_KINDS = { note: 'Note', task: 'Task', file: 'File', attachment: 'Attachment' };
// Only an in-app page of a workspace is linked. The server writes these; a
// model cannot, and nothing else is turned into a link. A file has no page of
// its own, so it is named with where it lives instead of being linked.
const SOURCE_HREF = /^\/workspaces\/[A-Za-z0-9][A-Za-z0-9._:-]*\/(?:notes|task)\/[^/?#\s]+$/;
// A range is given for text that is read in parts; a task is read as one record.
const RANGED_KINDS = new Set(['note', 'file', 'attachment']);

function sourceCoverage(source) {
  if (source.coverage === 'full') return 'read in full';
  const total = Number(source.total) || 0;
  const end = Number(source.end) || 0;
  if (RANGED_KINDS.has(source.kind) && total > 0 && end > 0)
    return `part read (characters ${(Number(source.start) || 0) + 1}–${end} of ${total})`;
  return 'part read';
}

// Where a file lives, in words the server wrote: a linked folder's name and the
// path inside it. Anything that looks like an absolute path is dropped.
function sourceWhere(source) {
  const where = String(source.detail || '').trim();
  return /^(?:\/|~|[A-Za-z]:\\|file:)/.test(where) ? '' : where;
}

/** The sources Ori actually delivered to the model for one reply. A reply that
 * pointed at some of them lists those as used; otherwise they are listed as
 * read. A saved reply's sources are what it read then, not a fresh read. */
export function turnSourcesView(
  attribution,
  { historical = false, formatTime = value => new Date(value).toLocaleString() } = {}
) {
  const all = (Array.isArray(attribution?.sources) ? attribution.sources : []).filter(
    source => source && typeof source === 'object' && source.key && source.label
  );
  if (!all.length) return { visible: false, summary: '', note: '', rows: [] };
  const cited = all.filter(source => source.cited === true);
  const shown = cited.length ? cited : all;
  const when = value => {
    const date = new Date(value || '');
    return Number.isNaN(date.getTime()) || date.getFullYear() < 2000 ? '' : formatTime(value);
  };
  return {
    visible: true,
    summary: `${cited.length ? 'Sources used' : 'Sources read'} (${shown.length})`,
    note: historical
      ? 'What this reply read at the time. It is not a fresh read of these sources.'
      : cited.length
        ? ''
        : 'The reply read these sources but did not point at a specific one.',
    rows: shown.map(source => {
      const updated = when(source.updated_at);
      const read = when(source.read_at);
      return {
        key: String(source.key),
        title: `${SOURCE_KINDS[source.kind] || 'Source'}: ${String(source.label)}`,
        href: SOURCE_HREF.test(String(source.href || '')) ? String(source.href) : '',
        detail: [
          sourceWhere(source),
          String(source.workspace || '').trim(),
          sourceCoverage(source),
          updated ? `updated ${updated}` : '',
          read ? `read ${read}` : ''
        ]
          .filter(Boolean)
          .join(' · ')
      };
    })
  };
}

export function renderTurnSources(row, attribution, options = {}) {
  if (!row?.ownerDocument || !row.querySelector) return false;
  const bubble = row.firstElementChild || row;
  bubble.querySelector?.('.personal-assistant-message__sources')?.remove();
  const view = turnSourcesView(attribution, options);
  if (!view.visible) return false;
  const doc = row.ownerDocument;
  const details = doc.createElement('details');
  details.className = 'personal-assistant-message__sources';
  const summary = doc.createElement('summary');
  summary.textContent = view.summary;
  const list = doc.createElement('ul');
  for (const source of view.rows) {
    const item = doc.createElement('li');
    const key = doc.createElement('span');
    key.className = 'personal-assistant-message__source-key';
    key.textContent = `[${source.key}]`;
    const title = doc.createElement(source.href ? 'a' : 'span');
    if (source.href) title.href = source.href;
    title.textContent = source.title;
    item.append(key, ' ', title);
    if (source.detail) item.append(` · ${source.detail}`);
    list.append(item);
  }
  details.append(summary, list);
  if (view.note) {
    const note = doc.createElement('p');
    note.textContent = view.note;
    details.append(note);
  }
  const actions = bubble.querySelector?.('.personal-assistant-message__actions');
  if (actions) bubble.insertBefore(details, actions);
  else bubble.append(details);
  return true;
}

/** How long the drawer waits for one reply. A reply can take several model
 * calls (up to four rounds of readers, then the answer), and a CLI provider
 * such as Codex takes ten seconds or more for each. The request helper's
 * general 30-second limit cut such replies off before they arrived. */
export const ASK_REPLY_TIMEOUT_MS = 5 * 60 * 1000;

/** What to tell the user when a reply did not arrive. The request helper
 * reports its own time limit as a cancelled request with no HTTP status; any
 * other failure without a status never reached Ori. Nothing here claims the
 * turn was or was not saved: a reply that finished late may have been. */
export function askFailureView(error, { timeoutMs = ASK_REPLY_TIMEOUT_MS } = {}) {
  const status = Number(error?.status) || 0;
  const text = String(error?.message || '');
  if (status === 0 && /cancel|abort/i.test(text)) {
    const minutes = Math.max(1, Math.round(timeoutMs / 60000));
    return {
      kind: 'timeout',
      message: `That is taking longer than I wait for a reply (${minutes} ${minutes === 1 ? 'minute' : 'minutes'}), so I stopped waiting. Your message is kept. Send it again, or ask about one thing at a time.`,
      summary: 'The reply did not arrive in time.'
    };
  }
  if (status === 0)
    return {
      kind: 'unreachable',
      message:
        'I could not reach Ori to answer that. Check that it is still running, then send your message again. It is kept.',
      summary: 'Ori could not be reached.'
    };
  return {
    kind: 'error',
    message: 'I could not answer that right now. Please retry.',
    summary: 'Could not complete the request.'
  };
}

if (typeof window !== 'undefined')
  window.PersonalAssistantWorkspaceContext = {
    collect: collectWorkspaceContext,
    label: workspaceContextLabel,
    renderTurn: renderTurnWorkspace,
    renderSources: renderTurnSources,
    askTimeoutMs: ASK_REPLY_TIMEOUT_MS,
    askFailure: askFailureView
  };
