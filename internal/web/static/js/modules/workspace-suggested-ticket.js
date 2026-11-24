/**
 * workspace-suggested-ticket.js — a finished first assessment suggests one next
 * ticket. Its Home card and workspace banner link to the workspace with
 * `?next=assessment`; the workspace opens the existing Give work dialog once,
 * with that suggestion as plain text the person can edit, and nothing is saved
 * until they press Give work themselves.
 *
 * Pure helpers; workspace-command.js wires them. The suggestion is text a model
 * wrote from a project's files, so it only ever becomes an input's value: it is
 * never carried in the address, never inserted as HTML, and never submitted
 * from here.
 */

export const ARRIVAL_PARAM = 'next';
export const ARRIVAL_ASSESSMENT = 'assessment';

// Give work's "What needs doing?" input is capped at 300 characters. Programmatic
// values are not truncated by the browser, so the cap is applied here and the
// remainder moves to Details instead of being lost.
export const TITLE_LIMIT = 300;

/** The arrival a page address asks for, or null. Only the literal value counts. */
export function suggestedTicketArrivalFromSearch(search) {
  const params = new URLSearchParams(String(search || ''));
  return params.get(ARRIVAL_PARAM) === ARRIVAL_ASSESSMENT ? { kind: ARRIVAL_ASSESSMENT } : null;
}

/** The query without the arrival's parameter, so a reload or Back never asks again. */
export function searchWithoutSuggestedTicketArrival(search) {
  const params = new URLSearchParams(String(search || ''));
  params.delete(ARRIVAL_PARAM);
  const rest = params.toString();
  return rest ? `?${rest}` : '';
}

/** The canonical destination for a workspace's suggested ticket. */
export function suggestedTicketRoute(workspaceRoute) {
  const route = String(workspaceRoute || '').trim();
  return route ? `${route}?${ARRIVAL_PARAM}=${ARRIVAL_ASSESSMENT}` : '';
}

/**
 * Whether `route` is the suggested-ticket destination of THIS workspace: the
 * canonical route, or the retired `/plans?next=assessment` one that a
 * bookmark or an older server may still carry. Anything else (another
 * workspace, an extra parameter, an absolute or protocol-relative address) is
 * refused, and the caller shows no link rather than a guessed one.
 */
export function isSuggestedTicketRoute(route, workspaceRoute) {
  const value = String(route || '');
  const base = String(workspaceRoute || '').trim();
  if (!value || !base || !base.startsWith('/') || base.startsWith('//')) return false;
  for (const char of value) {
    if (char === '\\' || char.charCodeAt(0) < 0x20 || char === '\u007f') return false;
  }
  const canonical = suggestedTicketRoute(base);
  return value === canonical || value === `${base}/plans?${ARRIVAL_PARAM}=${ARRIVAL_ASSESSMENT}`;
}

/**
 * Title and details from the assessment's one suggested request. A request
 * longer than the title limit keeps its full text: the title is cut at a word
 * boundary where one is close, and the whole request leads Details.
 */
export function splitSuggestion(request, { source = '' } = {}) {
  const text = String(request || '')
    .replace(/\r\n?/g, '\n')
    .trim();
  const note = String(source || '').trim();
  const firstLine = text.split('\n', 1)[0].trim();
  let title = firstLine;
  const overflow = text.length > firstLine.length || firstLine.length > TITLE_LIMIT;
  if (title.length > TITLE_LIMIT) {
    const cut = title.slice(0, TITLE_LIMIT - 1);
    const space = cut.lastIndexOf(' ');
    title = `${(space > TITLE_LIMIT * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
  }
  const parts = [];
  if (overflow) parts.push(text);
  if (note) parts.push(note);
  return { title, details: parts.join('\n\n') };
}

/**
 * What the first-task read allows. A suggestion is offered only from a finished
 * assessment that kept one; anything else is an explained dead end.
 */
export function suggestionFromFirstTask(body) {
  const look = body && typeof body === 'object' ? body.first_task : null;
  const request = String((look && look.next_request) || '').trim();
  if (!look || !String(look.recipe || '').trim() || look.state !== 'finished' || !request) {
    return { ok: false, reason: 'missing' };
  }
  const project = String(look.folder_name || look.workspace_name || '').trim();
  return {
    ok: true,
    request,
    source: project
      ? `Suggested by the first assessment of ${project}.`
      : 'Suggested by the first assessment.'
  };
}

/**
 * Opens Give work with the workspace's suggested ticket, once.
 *
 * The suggestion is read from the server for `workspaceId` (never taken from
 * the address). The answer is dropped when the page has moved on by the time it
 * arrives, and it is never opened over a Give work dialog the person already
 * has open, so what they have typed is not overwritten. Only values are set:
 * nobody is assigned, Start now stays off, and nothing is saved here.
 *
 * Returns one of: 'opened', 'missing' (no finished assessment or no suggestion),
 * 'unavailable' (the read failed), 'busy' (Give work is already open), 'stale'
 * (the workspace changed first), 'unsupported' (the page cannot show the dialog).
 */
export async function offerSuggestedTicket({
  workspaceId,
  workspaceName = '',
  agents = [],
  trigger = null,
  onSaved,
  request,
  isCurrent = () => true,
  dialogOpen = () => false,
  openGiveWork
} = {}) {
  const id = String(workspaceId || '').trim();
  if (!id || typeof request !== 'function' || typeof openGiveWork !== 'function') {
    return 'unsupported';
  }
  let body;
  try {
    body = await request(`/api/workspaces/${encodeURIComponent(id)}/folder-first-task`);
  } catch {
    return isCurrent() ? 'unavailable' : 'stale';
  }
  if (!isCurrent()) return 'stale';
  const suggestion = suggestionFromFirstTask(body);
  if (!suggestion.ok) return 'missing';
  if (dialogOpen()) return 'busy';
  const { title, details } = splitSuggestion(suggestion.request, { source: suggestion.source });
  const dialog = openGiveWork({
    workspaceId: id,
    workspaceName,
    agents,
    what: title,
    details,
    trigger,
    onSaved
  });
  return dialog ? 'opened' : 'unsupported';
}

export const ARRIVAL_MESSAGES = Object.freeze({
  missing: 'This workspace has no finished assessment with a suggested next ticket.',
  unavailable:
    'The suggested ticket could not be read just now. Open Give work to write one yourself, or try again from the workspace banner.',
  busy: 'A suggested ticket is ready, but Give work is already open. Close it and open the banner link again to use the suggestion.',
  unsupported: 'Give work could not open here. Reload the workspace and try the banner link again.'
});
