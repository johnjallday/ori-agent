/**
 * workspace-url-state.js
 *
 * Isolated, unit-testable URL/history helpers for the workspace detail page
 * (PRD section F, FR80-FR96). Pure functions only — no DOM/window access —
 * so parsing, sanitizing, and diffing can be exercised from fixtures. The
 * caller (workspace-command.js) is responsible for reading `location.search`,
 * calling `history.pushState`/`replaceState`, and re-rendering.
 *
 * Canonical query params (FR80-81): `mode` (map|details, replaces the retired
 * `view` param), `panel` (`tasks` or `backlog` — PRD workspace-backlog FR59:
 * the global Workspace Map's owning-workspace deep link opens the Backlog
 * drawer this way), `task` (the selected item ID within whichever drawer
 * `panel` names), `agent`, and an optional `run`. Per the group-1 backend
 * audit, execution monitoring is TASK-ID-KEYED (there is no separate run-ID
 * surfaced to the frontend yet), so `run` is treated as a task ID naming
 * which tracked run the tray should show — documented here rather than
 * invented silently.
 */

export const MODE = Object.freeze({ MAP: 'map', DETAILS: 'details' });
const VALID_MODES = new Set([MODE.MAP, MODE.DETAILS]);
const VALID_PANELS = new Set(['tasks', 'backlog']);

/** Parse a query string (with or without a leading `?`) into raw URL state. */
export function parseWorkspaceURLState(search) {
  const params = new URLSearchParams(String(search || '').replace(/^\?/, ''));
  const mode = params.get('mode');
  return {
    mode: mode && VALID_MODES.has(mode) ? mode : null,
    panel: params.get('panel') || '',
    task: params.get('task') || '',
    agent: params.get('agent') || '',
    run: params.get('run') || ''
  };
}

/** Serialize state back into a query string (no leading `?`). Omits empty/null fields. */
export function serializeWorkspaceURLState(state) {
  const params = new URLSearchParams();
  const s = state || {};
  if (s.mode && VALID_MODES.has(s.mode)) params.set('mode', s.mode);
  if (s.panel) params.set('panel', s.panel);
  if (s.task) params.set('task', s.task);
  if (s.agent) params.set('agent', s.agent);
  if (s.run) params.set('run', s.run);
  return params.toString();
}

/**
 * Sanitize state against the currently loaded workspace (FR91): unknown,
 * stale, deleted, or unauthorized values are dropped rather than applied.
 * `context` supplies the authoritative sets to validate against.
 *
 * @param {object} state
 * @param {{validTaskIds?: Iterable<string>, validAgentKeys?: Iterable<string>, validRunTaskIds?: Iterable<string>, validBacklogIds?: Iterable<string>}} context
 * @returns {{state: object, dropped: string[]}} the sanitized state and which fields were dropped (for a concise notice).
 */
export function sanitizeWorkspaceURLState(state, context = {}) {
  const validTaskIds = new Set(context.validTaskIds || []);
  const validAgentKeys = new Set(context.validAgentKeys || []);
  const validRunTaskIds = new Set(context.validRunTaskIds || validTaskIds);
  const validBacklogIds = new Set(context.validBacklogIds || []);
  const s = state || {};
  const dropped = [];
  const out = { mode: s.mode || null, panel: '', task: '', agent: '', run: '' };

  if (s.panel && VALID_PANELS.has(s.panel)) out.panel = s.panel;
  else if (s.panel) dropped.push('panel');

  // `task` names the selected item within whichever drawer `panel` opens —
  // validate it against that drawer's own ID set (tasks and backlog items
  // are disjoint since Ready+ excludes Backlog, FR40).
  const taskIdSet = out.panel === 'backlog' ? validBacklogIds : validTaskIds;
  if (s.task && taskIdSet.has(s.task)) out.task = s.task;
  else if (s.task) dropped.push('task');

  if (s.agent && validAgentKeys.has(s.agent)) out.agent = s.agent;
  else if (s.agent) dropped.push('agent');

  if (s.run && validRunTaskIds.has(s.run)) out.run = s.run;
  else if (s.run) dropped.push('run');

  // panel=tasks without a valid task is still a meaningful "drawer open" state
  // (FR82 requires panel+task together only to restore the PREVIEW, not to
  // gate opening the drawer itself).
  return { state: out, dropped };
}

/** Whether two (sanitized) states are equivalent for history-entry purposes (FR86). */
export function statesEqual(a, b) {
  const x = a || {};
  const y = b || {};
  return (
    (x.mode || null) === (y.mode || null) &&
    (x.panel || '') === (y.panel || '') &&
    (x.task || '') === (y.task || '') &&
    (x.agent || '') === (y.agent || '') &&
    (x.run || '') === (y.run || '')
  );
}

/**
 * Resolve the effective mode: URL wins; otherwise the local preference; else
 * the default (FR85).
 */
export function resolveEffectiveMode(urlMode, localPreferenceMode, defaultMode = MODE.DETAILS) {
  if (urlMode && VALID_MODES.has(urlMode)) return urlMode;
  if (localPreferenceMode && VALID_MODES.has(localPreferenceMode)) return localPreferenceMode;
  return defaultMode;
}

/**
 * Build the safe, same-origin return target embedded in a task-page link
 * (FR92). Only ever a relative path scoped to this workspace — never an
 * absolute URL or another workspace's route, so it can't become an open
 * redirect vector.
 */
export function buildReturnTarget(workspaceId, state) {
  const id = String(workspaceId || '').trim();
  if (!id) return '';
  const query = serializeWorkspaceURLState(state);
  const path = '/workspaces/' + encodeURIComponent(id);
  return query ? path + '?' + query : path;
}

/**
 * Validate a return-target string before navigating to it (FR93): must be a
 * relative, same-workspace `/workspaces/{id}` path — reject absolute URLs,
 * protocol-relative (`//host`) links, and any other workspace's route.
 */
export function isSafeReturnTarget(raw, workspaceId) {
  const value = String(raw || '');
  const id = String(workspaceId || '').trim();
  if (!value || !id) return false;
  if (!value.startsWith('/')) return false; // rejects absolute URLs and bare hosts
  if (value.startsWith('//')) return false; // rejects protocol-relative URLs
  const expectedPrefix = '/workspaces/' + encodeURIComponent(id);
  return value === expectedPrefix || value.startsWith(expectedPrefix + '?') || value.startsWith(expectedPrefix + '/');
}

/**
 * Build the full path+query for the workspace detail page from a state object,
 * given the current pathname (used for pushState/replaceState targets).
 */
export function buildWorkspaceURL(pathname, state) {
  const query = serializeWorkspaceURLState(state);
  return query ? pathname + '?' + query : pathname;
}
