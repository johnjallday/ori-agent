// library-setup.js — the one "set up projects" flow a Home's library offers,
// shared by the dashboard shelf (project-library.js, "Connect selected") and
// the Home map's setup sheet (map-library-sheet.js).
//
// PRD: tasks/prd-map-library-setup.md FR-6, FR-7, FR-12, FR-13. Nothing here
// decides anything the server does not: every project is reviewed and
// committed on its own, with its own token and idempotency key, and only when
// the server's review names the file the person confirmed. The first failure
// stops the rest; projects already committed stay on the map.
//
// Vocabulary (FR-19): "to set up", "on the map", "needs a file choice",
// "needs the dashboard". Never "ready" — the workspace tiles say that about
// tickets.

import { RECENT_SONGS_QUERY, libraryRequestError, readLibraryPayload } from './library-open.js';

/** The four buckets a scanned project can be in, from the sheet's point of view. */
export const SETUP_KINDS = Object.freeze({
  SETUP: 'setup', // one file, can be reviewed and committed now
  FILE_CHOICE: 'file_choice', // several candidate files, person picks one
  CONNECTED: 'connected', // already a workspace
  DASHBOARD: 'dashboard' // needs a review only the dashboard offers
});

const KIND_LABELS = Object.freeze({
  setup: 'to set up',
  file_choice: 'needs a file choice',
  connected: 'on the map',
  dashboard: 'needs the dashboard'
});

/** The short label a row shows for its bucket. */
export function setupLabel(kind) {
  return Object.hasOwn(KIND_LABELS, kind) ? KIND_LABELS[kind] : KIND_LABELS.dashboard;
}

function fileList(eligibility) {
  const files = Array.isArray(eligibility?.project_files) ? eligibility.project_files : [];
  return files.filter(file => typeof file === 'string' && file !== '');
}

/**
 * Which bucket one project is in, from its list row (or detail row) and its
 * eligibility answer. Pure.
 *
 * - connected: the row says so, or the eligibility does.
 * - setup: `review_available` with exactly one project file.
 * - file_choice: `file_choice_required`, or `review_available` with several
 *   files. The person must choose before the row can be set up.
 * - dashboard: everything else, with the server's own reason kept for display.
 */
export function bucketProject(row, eligibility) {
  const state = String(eligibility?.state || '');
  const files = fileList(eligibility);
  if (row?.connection === 'connected' || state === 'connected') {
    return {
      kind: SETUP_KINDS.CONNECTED,
      files: [],
      workspaceId: String(eligibility?.workspace_id || row?.workspace_id || ''),
      reason: ''
    };
  }
  if (state === 'review_available' && files.length === 1) {
    return { kind: SETUP_KINDS.SETUP, files, workspaceId: '', reason: '' };
  }
  if ((state === 'file_choice_required' || state === 'review_available') && files.length > 1) {
    return { kind: SETUP_KINDS.FILE_CHOICE, files, workspaceId: '', reason: '' };
  }
  return {
    kind: SETUP_KINDS.DASHBOARD,
    files: [],
    workspaceId: '',
    state,
    reason: String(eligibility?.reason || ''),
    integration: eligibility?.integration_offer || null
  };
}

/**
 * A fetch client for one Home's library, the same shape project-library.js
 * uses: request(path) resolves the JSON body or throws a libraryRequestError;
 * post(path, body) is a JSON POST.
 */
export function createLibraryClient(homeID, fetchImpl = (...args) => globalThis.fetch(...args)) {
  const base = `/api/workspaces/${encodeURIComponent(String(homeID || ''))}/assistant-program/library`;
  async function request(path, options = {}) {
    const response = await fetchImpl(base + path, {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const result = await readLibraryPayload(response);
    if (!response.ok) throw libraryRequestError(response, result);
    return result;
  }
  return {
    request,
    post: (path, body = {}) => request(path, { method: 'POST', body: JSON.stringify(body) })
  };
}

function entryPath(id) {
  return `/projects/${encodeURIComponent(id)}`;
}

/**
 * The rows a setup sheet shows: one page of the Home's library (newest save
 * first), each row with its bucket. Reads only; never scans. A row whose
 * eligibility read fails goes to the dashboard bucket with that error as its
 * reason rather than being dropped, so the totals stay honest (FR-6).
 */
export async function loadSetupRows(client) {
  const page = await client.request(`/projects?${RECENT_SONGS_QUERY}`);
  const rows = (Array.isArray(page?.rows) ? page.rows : []).filter(
    row => row && typeof row.id === 'string' && row.id !== ''
  );
  const result = await Promise.all(
    rows.map(async row => {
      const name = String(row.name || '').trim() || 'This project';
      let bucket;
      try {
        // Even a connected row is asked: the list row carries no workspace id,
        // and the eligibility answer names the exact linked workspace.
        const eligibility = await client.request(`${entryPath(row.id)}/activation`);
        bucket = bucketProject(row, eligibility);
      } catch (error) {
        bucket = {
          kind: SETUP_KINDS.DASHBOARD,
          files: [],
          workspaceId: '',
          state: 'unreadable',
          reason: error?.message || 'Its setup status could not be read.',
          integration: null
        };
      }
      return { id: row.id, name, ...bucket };
    })
  );
  return {
    rows: result,
    readOnly: page?.provider_read_only !== false,
    totals: setupTotals(result)
  };
}

/** Counts per bucket for the sheet's subtitle (FR-8). */
export function setupTotals(rows) {
  const totals = { found: 0, setup: 0, file_choice: 0, connected: 0, dashboard: 0 };
  for (const row of Array.isArray(rows) ? rows : []) {
    if (!row || !Object.hasOwn(totals, row.kind)) continue;
    totals.found++;
    totals[row.kind]++;
  }
  return totals;
}

/**
 * The subtitle line: "6 found by the last scan · 1 already on the map · 1
 * needs the dashboard", zero parts left out.
 */
export function setupSubtitle(totals) {
  const t = totals || {};
  const parts = [`${t.found || 0} found by the last scan`];
  if (t.connected > 0) parts.push(`${t.connected} already on the map`);
  if (t.dashboard > 0)
    parts.push(`${t.dashboard} ${t.dashboard === 1 ? 'needs' : 'need'} the dashboard`);
  return parts.join(' · ');
}

function defaultOperationKey(action) {
  return `library-${action}-${globalThis.crypto.randomUUID()}`;
}

/**
 * Set up the given items, one at a time, in order. Each item is
 * { id, name, file }: the project and the exact file the person confirmed.
 *
 * Per item: re-read the entry; skip it if it connected meanwhile; re-read
 * eligibility and require the confirmed file to still be offered; review with
 * that file; require the review to name it; commit. The first failure stops
 * the loop. Returns { done, failed, remaining } where done items carry the
 * workspace id the server reported.
 *
 * onProgress(item, status, detail) is told 'working', 'done' or 'failed'.
 */
export async function runSetup({
  client,
  items,
  onProgress = () => {},
  operationKey = defaultOperationKey
}) {
  const done = [];
  let failed = null;
  const queue = Array.isArray(items) ? items.filter(Boolean) : [];
  let index = 0;
  for (; index < queue.length; index++) {
    const item = queue[index];
    const path = entryPath(item.id);
    onProgress(item, 'working');
    try {
      const detail = await client.request(path);
      let workspaceId = String(detail?.row?.workspace_id || '');
      if (detail?.row?.connection !== 'connected') {
        const eligibility = await client.request(`${path}/activation`);
        const ok =
          (eligibility?.state === 'review_available' ||
            eligibility?.state === 'file_choice_required') &&
          fileList(eligibility).includes(item.file);
        if (!ok) throw new Error('it changed after you confirmed, so it was not set up');
        const review = await client.post(`${path}/activation/review`, {
          workspace_name: String(detail?.row?.name || item.name || '').slice(0, 128),
          project_file: item.file,
          if_revision: detail?.revision
        });
        if (review?.project_file !== item.file) {
          throw new Error('its reviewed file differs from the one you confirmed');
        }
        const committed = await client.post(`${path}/activation/commit`, {
          review_token: review.token,
          idempotency_key: operationKey('batch-activation'),
          confirm: true
        });
        workspaceId = String(committed?.workspace_id || '');
      }
      const result = { ...item, workspaceId };
      done.push(result);
      onProgress(item, 'done', result);
    } catch (error) {
      failed = { ...item, message: error?.message || 'could not be set up' };
      onProgress(item, 'failed', failed);
      break;
    }
  }
  const remaining = failed ? queue.slice(index + 1) : [];
  return { done, failed, remaining };
}
