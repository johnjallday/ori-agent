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
    out.surface =
      match[2] === 'canvas'
        ? 'workspace_canvas'
        : match[2] === 'tasks' && match[3]
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

if (typeof window !== 'undefined')
  window.PersonalAssistantWorkspaceContext = {
    collect: collectWorkspaceContext,
    label: workspaceContextLabel,
    renderTurn: renderTurnWorkspace
  };
