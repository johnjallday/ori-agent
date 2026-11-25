import { currentHQAttempt } from './hq-guided-attempt.js';

// Keep the material for one confirmed operation in this tab. It lets the
// ordinary result survive a reload without treating a saved result as a visit.
const pending = 'ori.hq.workspaceOpen.v1';
const completed = hq => `ori.hq.workspaceVisit.v1:${hq}`;
const current = hint => hint && currentHQAttempt(hint.hq, 'workspace', hint.attempt)?.arrived;
function read() {
  try {
    return JSON.parse(sessionStorage.getItem(pending));
  } catch {
    return null;
  }
}
function write(value) {
  try {
    sessionStorage.setItem(pending, JSON.stringify(value));
  } catch {
    /* guidance is optional */
  }
}
export function rememberWorkspaceReview(hint, reference, path) {
  if (reference?.id && reference.digest && path && current(hint)) write({ hint, reference, path });
}
export function openingReviewedWorkspace(reference, id) {
  const saved = read();
  if (
    id &&
    reference?.id &&
    saved?.reference?.id === reference.id &&
    saved.reference.digest === reference.digest &&
    current(saved.hint)
  )
    write({ ...saved, id });
}
export function openingExistingWorkspace(hint, id, path) {
  if (id && path && current(hint)) write({ hint, id, path });
}

// Called only with the live detail page's server snapshot. A route, a create
// response, and even the right display name are not evidence of arrival.
export async function verifyWorkspaceVisit(pageID, workspace) {
  const saved = read();
  if (!saved?.id || !workspace?.id || workspace.id !== pageID) return false;
  try {
    if (!current(saved.hint) || saved.id !== pageID) {
      sessionStorage.removeItem(pending);
      return false;
    }
    // The orchestration snapshot identifies the page but the workspace owner
    // supplies the normalized anchor and its current availability.
    const response = await fetch(`/api/workspaces/${encodeURIComponent(pageID)}`, {
      cache: 'no-store'
    });
    if (!response.ok) return false;
    workspace = await response.json();
    const active = read();
    if (
      active?.hint?.attempt !== saved.hint.attempt ||
      active.id !== pageID ||
      !current(saved.hint)
    )
      return false;
    sessionStorage.removeItem(pending);
    if (
      workspace.id !== pageID ||
      workspace.shape !== 'anchored' ||
      !workspace.anchor?.directory_reference_id ||
      workspace.anchor.missing ||
      workspace.anchor.path !== saved.path ||
      (saved.reference &&
        workspace.shared_data?.workspace_creation_operation_id !== saved.reference.id)
    )
      return false;
    localStorage.setItem(completed(saved.hint.hq), '1');
    return true;
  } catch {
    return false;
  }
}
export function hasWorkspaceVisit(hq) {
  try {
    return localStorage.getItem(completed(hq)) === '1';
  } catch {
    return false;
  }
}
