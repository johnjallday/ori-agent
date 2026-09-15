// Shared bulk actions (delete + group) for the Home cockpit's two peer views.
//
// Map and Tree need the same operations: delete one item, delete a checked set,
// and group a checked set. Tree carried a private copy while Map borrowed them
// from `window.WorkspaceHub` — a global defined only in workspace-hub.js, which
// loads on exactly one page (`pages/workspaces.tmpl`). That page has redirected
// to Home since the cockpit landed, so the global was never present where the
// Map actually renders and every Map delete/group click was a silent no-op.
//
// Keeping one implementation here means Map depends on a module it genuinely
// loads, and the two views cannot drift apart again.
//
// Everything the operations touch — the row set, the dialog, fetch, and the
// host's announce/toast/refresh callbacks — arrives through `ctx`, so these run
// under plain Node in tests with no DOM and no network.
//
// Deletion runs inside one in-app dialog (workspace-delete-dialog.js) from the
// first question to the final outcome. This module is the controller: it asks
// the dialog for each decision and talks to the API; the dialog only renders.
// A deletion the server refuses because an Assistant Home is involved is
// resolved in that same dialog — review, impact, commit — instead of bouncing
// the user to another page through a native confirm.

import { workspacePageURL } from './workspace-routes.js';
import { openDeleteDialog } from './workspace-delete-dialog.js';

const REVIEW_REQUIRED = 'assistant_program_review_required';
const REVIEW_HOME_REMOVAL = 'Review Home removal';
// A group can hold more than one protected Home. Each reviewed removal is a
// separate consent, so the retry loop is bounded rather than open-ended.
const MAX_REVIEW_ROUNDS = 3;

/** A row is a group when its kind says so (matches home-workspace-cockpit). */
export function isGroupRow(row) {
  return (
    String((row && row.kind) || '')
      .trim()
      .toLowerCase() === 'group'
  );
}

/**
 * Reduce a selection to the ids that have no selected ancestor.
 *
 * Grouping is the operation that needs this. "Group selected" reparents every
 * id onto the new group, so a selection holding both a group and something
 * inside it would lift the child OUT of its parent and drop it beside it —
 * silently reshaping a hierarchy the user only meant to move as a unit. Keeping
 * top-level ids only means the parent moves and its contents ride along.
 *
 * Deletion deliberately does NOT use this: a group deletes as `group_only` by
 * default (the server un-nests its members rather than destroying them), so a
 * separately checked child is a genuine second deletion, not a duplicate.
 */
export function topLevelIds(ids, rows) {
  const selected = new Set((Array.isArray(ids) ? ids : []).filter(Boolean));
  const byId = new Map((Array.isArray(rows) ? rows : []).filter(Boolean).map(row => [row.id, row]));
  const hasSelectedAncestor = id => {
    // Walk up by parent_id. `seen` guards against a cycle in malformed data,
    // which would otherwise hang the click that triggered this.
    const seen = new Set([id]);
    let current = byId.get(id);
    while (current && current.parent_id && !seen.has(current.parent_id)) {
      if (selected.has(current.parent_id)) return true;
      seen.add(current.parent_id);
      current = byId.get(current.parent_id);
    }
    return false;
  };
  return Array.from(selected).filter(id => !hasSelectedAncestor(id));
}

// ---------------------------------------------------------------------------
// Context plumbing
// ---------------------------------------------------------------------------

function ctxRows(ctx) {
  return Array.isArray(ctx && ctx.rows) ? ctx.rows : [];
}

function findRow(ctx, id) {
  return ctxRows(ctx).find(row => row && row.id === id) || null;
}

/**
 * Open the delete dialog for one operation. Hosts and tests may supply their
 * own `ctx.openDialog`; the browser default renders the real `<dialog>`.
 * No dialog means no way to ask, and no way to ask means nothing is deleted.
 */
function openSession(ctx, title) {
  const open = ctx && typeof ctx.openDialog === 'function' ? ctx.openDialog : openDeleteDialog;
  try {
    return open({ title }) || null;
  } catch (_) {
    return null;
  }
}

function ctxFetch(ctx) {
  if (ctx && typeof ctx.fetch === 'function') return ctx.fetch;
  return (...args) => globalThis.fetch(...args);
}

function announce(ctx, message) {
  if (ctx && typeof ctx.announce === 'function') ctx.announce(message);
}

function toast(ctx, message, variant) {
  if (ctx && typeof ctx.toast === 'function') {
    ctx.toast(message, variant);
    return;
  }
  if (typeof window === 'undefined') return;
  if (window.Toast && typeof window.Toast[variant] === 'function') window.Toast[variant](message);
}

async function changed(ctx) {
  if (ctx && typeof ctx.onChanged === 'function') await ctx.onChanged();
}

function trashed(ctx, id, name) {
  if (ctx && typeof ctx.onTrashed === 'function') ctx.onTrashed(id, name);
}

/**
 * The human-readable reason a request failed.
 *
 * The workspace API reports failures as `{"error": "...", "success": false}`,
 * with a few endpoints using `message` instead. Reading only `message` (as the
 * older copy of this helper did) meant a slug conflict — the most common real
 * failure here — reached the user as a wall of raw JSON in a toast.
 */
async function requestError(response, fallback) {
  let message = fallback;
  let payload;
  try {
    const text = await response.text();
    if (text) {
      message = text;
      try {
        payload = JSON.parse(text);
        if (typeof payload?.error === 'string' && payload.error) message = payload.error;
        else if (typeof payload?.message === 'string' && payload.message) message = payload.message;
      } catch (_) {
        // Non-JSON errors still carry a useful explanation.
      }
    }
  } catch (_) {
    // Preserve the fallback when the body cannot be read.
  }
  const error = new Error(message);
  if (response.status === 409 && payload?.code === REVIEW_REQUIRED) {
    // The server names the Home that blocks this delete and which review
    // resolves it. `review_home_slug` is present only when that Home is live;
    // a trashed or missing Home has no page to review on.
    const details = payload.details || {};
    const str = value => (typeof value === 'string' ? value.trim() : '');
    error.review = {
      workspaceId: str(details.workspace_id),
      stationId: str(details.station_workspace_id),
      action: str(details.review_action),
      homeSlug: str(details.review_home_slug)
    };
  }
  return error;
}

/** Report a failure the same way on every path: SR announcement + toast. */
function fail(ctx, err, fallback) {
  const message = err && err.message ? err.message : fallback;
  announce(ctx, message);
  toast(ctx, message, 'error');
}

/** A JSON request whose failure carries the API's own explanation. */
async function requestJSON(ctx, url, { method = 'GET', body } = {}) {
  const response = await ctxFetch(ctx)(url, {
    method,
    headers: {
      Accept: 'application/json',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {})
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {})
  });
  if (!response.ok) throw await requestError(response, `Request failed (${response.status})`);
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

function assistantProgramURL(stationId, suffix = '') {
  return `/api/workspaces/${encodeURIComponent(stationId)}/assistant-program${suffix}`;
}

function deleteURL(id, mode) {
  const query = mode ? `&delete_mode=${encodeURIComponent(mode)}` : '';
  return `/api/workspaces/${encodeURIComponent(id)}?confirm=true${query}`;
}

/** Every workspace nested anywhere under `id`, for the group-delete copy. */
export function descendantCount(rows, id) {
  const children = new Map();
  for (const row of (Array.isArray(rows) ? rows : []).filter(Boolean)) {
    if (!row.parent_id) continue;
    if (!children.has(row.parent_id)) children.set(row.parent_id, []);
    children.get(row.parent_id).push(row.id);
  }
  const seen = new Set([id]);
  const queue = [id];
  let count = 0;
  while (queue.length) {
    for (const child of children.get(queue.shift()) || []) {
      if (seen.has(child)) continue;
      seen.add(child);
      queue.push(child);
      count += 1;
    }
  }
  return count;
}

// ---------------------------------------------------------------------------
// Assistant Home review
// ---------------------------------------------------------------------------

/**
 * Resolve a delete the server refused because an Assistant Home is involved,
 * without leaving the dialog.
 *
 * The server's contract is review-then-commit with a token bound to the Home's
 * state revision, so a stale page can never remove something it did not look
 * at. That stays exactly as it is. What changes is where the review happens:
 * here, in the dialog the user already has open, instead of on the Home's own
 * page after a native confirm and a navigation.
 *
 * Only "Review Home removal" is resolved in place. "Review disconnect" (a
 * linked project being deleted on its own) still belongs to the Home page, and
 * the dialog offers that page as a link rather than a redirect.
 *
 * Resolves true once the Home was removed; false when nothing changed. The
 * caller owns closing the session either way.
 */
async function resolveRemovalReview(ctx, session, row, error) {
  const review = error.review || {};
  const homeHref = review.homeSlug ? workspacePageURL(review.homeSlug, ['assistant']) : '';
  announce(ctx, error.message);
  if (review.action !== REVIEW_HOME_REMOVAL || !review.stationId || !homeHref) {
    await session.notice({
      message: error.message,
      action: homeHref ? { label: 'Open Assistant Home', href: homeHref } : null
    });
    return false;
  }

  session.busy('Checking what this removal affects…');
  let program;
  let impact;
  try {
    program = await requestJSON(ctx, assistantProgramURL(review.stationId));
    if (!program || !program.available || !program.is_station) {
      throw new Error('The Assistant Home could not be read. Nothing was deleted.');
    }
    impact = await requestJSON(ctx, assistantProgramURL(review.stationId, '/remove-home/review'), {
      method: 'POST',
      body: { state_revision: Number(program.state_revision || 0) }
    });
  } catch (err) {
    announce(ctx, err.message);
    await session.notice({ message: err.message || 'The removal could not be reviewed.' });
    return false;
  }

  const isSelf = review.stationId === row.id;
  const homeName =
    (isSelf ? row.name : findRow(ctx, review.stationId)?.name) ||
    String(program.declaration?.station_name || '').trim() ||
    'this Assistant Home';
  const linked = Math.max(0, Number(impact.linked_project_count) || 0);
  const roles = Math.max(0, Number(impact.home_role_count) || 0);

  // The summary carries the two facts the impact list does not lead with: what
  // happens to linked projects (kept, never deleted) and that a Home removal
  // is permanent rather than a trip to the Trash.
  const sentences = [];
  sentences.push(
    isSelf
      ? `"${homeName}" is an Assistant Home.`
      : `"${row.name}" contains the Assistant Home "${homeName}", which has to be removed first.`
  );
  if (linked === 0) {
    sentences.push(
      roles > 0
        ? `It has no linked projects, so removing it deletes only the group and its ${roles} Home role${roles === 1 ? '' : 's'}.`
        : 'It has no linked projects, so removing it deletes only the group.'
    );
  } else {
    sentences.push(
      `Its ${linked} linked project${linked === 1 ? '' : 's'} will be kept as standalone workspace${linked === 1 ? '' : 's'}.`
    );
  }
  sentences.push('The Home is removed permanently rather than moved to the Trash.');
  if (!isSelf) sentences.push(`Deleting "${row.name}" then continues.`);

  const removed = await session.review({
    heading: isSelf ? `Remove "${homeName}"?` : `Remove the Assistant Home in "${row.name}"?`,
    summary: sentences.join(' '),
    // An empty Home has nothing to preserve, so the server's preservation
    // notes would only make a plain delete read as a complicated one.
    impact: linked > 0 ? impact.impact : [],
    confirmLabel: 'Remove Home',
    progress: 'Removing Home…',
    confirm: () =>
      requestJSON(ctx, assistantProgramURL(review.stationId, '/remove-home/commit'), {
        method: 'POST',
        body: { token: impact.token }
      })
  });
  if (!removed) return false;
  announce(ctx, `${homeName} removed.`);
  return true;
}

// ---------------------------------------------------------------------------
// Operations
// ---------------------------------------------------------------------------

/**
 * Run one DELETE for `row` inside an open session and settle the dialog.
 *
 * A review-required refusal is resolved in place. When the reviewed Home was
 * the row itself, the removal IS the delete. When it was nested inside the
 * row, the original delete is retried with the same mode the user chose.
 */
async function performDelete(ctx, session, row, mode, round = 0) {
  session.busy('Deleting…');
  let error;
  try {
    const res = await ctxFetch(ctx)(deleteURL(row.id, mode), { method: 'DELETE' });
    if (res.ok) {
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        if (data && data.trashed) trashed(ctx, row.id, row.name);
      }
      session.close();
      announce(ctx, `${row.name} deleted.`);
      await changed(ctx);
      return true;
    }
    error = await requestError(res, 'Failed to delete');
  } catch (err) {
    error = err;
  }

  if (error && error.review && round < MAX_REVIEW_ROUNDS) {
    const removed = await resolveRemovalReview(ctx, session, row, error);
    if (!removed) {
      session.close();
      return false;
    }
    if (error.review.stationId === row.id) {
      session.close();
      await changed(ctx);
      return true;
    }
    return performDelete(ctx, session, row, mode, round + 1);
  }

  session.close();
  fail(ctx, error, 'Failed to delete.');
  return false;
}

/** Delete a single workspace or group, with its own confirmation. */
export async function deleteWorkspace(id, ctx) {
  const row = findRow(ctx, id);
  if (!row) return false;
  const group = isGroupRow(row);
  const session = openSession(ctx, `Delete "${row.name}"?`);
  if (!session) return false;
  const choice = await session.chooseDelete({
    name: row.name,
    group,
    memberCount: group ? descendantCount(ctxRows(ctx), id) : 0
  });
  if (!choice) {
    session.close();
    return false;
  }
  return performDelete(ctx, session, row, group ? choice.mode : '');
}

/**
 * Delete a checked set after one batch confirmation.
 *
 * A single id defers to the per-item flow so a lone group still gets its
 * two-mode choice instead of a silent default.
 */
export async function deleteWorkspaces(ids, ctx) {
  const selected = (Array.isArray(ids) ? ids : []).filter(Boolean);
  if (selected.length === 0) return 0;
  if (selected.length === 1) return (await deleteWorkspace(selected[0], ctx)) ? 1 : 0;
  const session = openSession(ctx, `Delete ${selected.length} selected items?`);
  if (!session) return 0;
  if (!(await session.confirmCount({ count: selected.length }))) {
    session.close();
    return 0;
  }
  session.busy('Deleting…');

  let deleted = 0;
  const failures = [];
  const reviews = [];
  for (const id of selected) {
    const row = findRow(ctx, id);
    if (!row) continue;
    try {
      // No delete_mode: the server's safe default un-nests a group's members
      // rather than destroying them implicitly.
      const res = await ctxFetch(ctx)(deleteURL(id, ''), { method: 'DELETE' });
      if (!res.ok) {
        const error = await requestError(res, 'Failed to delete');
        failures.push(`${row.name}: ${error.message}`);
        if (error.review) reviews.push({ row, error });
        continue;
      }
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        if (data && data.trashed) trashed(ctx, id, row.name);
      }
      deleted += 1;
    } catch (_) {
      failures.push(row.name);
    }
  }

  // Report what actually happened. The previous Tree implementation always
  // claimed the full count, so a server-side failure looked like a success and
  // only the background reload hinted otherwise.
  if (failures.length === 0) {
    announce(ctx, `${deleted} items deleted.`);
  } else {
    const message = `Deleted ${deleted} of ${selected.length}. Could not delete: ${failures.join(', ')}.`;
    announce(ctx, message);
    toast(ctx, message, 'error');
  }
  await changed(ctx);

  // One Home blocked the batch: review it right here. Several: the toast has
  // already named each, and choosing one to review would be guessing.
  const stations = [...new Set(reviews.map(entry => entry.error.review.stationId).filter(Boolean))];
  if (stations.length === 1) {
    const first = reviews.find(entry => entry.error.review.stationId === stations[0]);
    const removed = await resolveRemovalReview(ctx, session, first.row, first.error);
    session.close();
    if (removed) {
      if (first.error.review.stationId === first.row.id) deleted += 1;
      await changed(ctx);
    }
    return deleted;
  }
  session.close();
  return deleted;
}

/**
 * Move a reviewed member snapshot into an already-created group.
 *
 * Creation belongs to the shared creator, so this helper deliberately owns no
 * name prompt or POST. It only performs the existing membership PATCHes and
 * returns enough truth for the caller to frame a Map district or report a
 * partial result without ever creating a second group.
 *
 * A rejected response is known failure. A lost response is different: after an
 * authoritative refresh, `ctx.verifyMembership` can confirm it; otherwise it
 * remains explicitly uncertain rather than being falsely reported as failed.
 *
 * @returns {Promise<{groupId: string, name: string, placed: string[], failed:
 *   string[], uncertain: string[], partial: boolean}>}
 */
export async function moveMembersIntoGroup(group, memberIds, ctx) {
  const groupId = String(group?.groupId || group?.id || '').trim();
  const name = String(group?.name || 'Group').trim() || 'Group';
  if (!groupId)
    throw new Error('Created group identity is unavailable; refresh before moving members.');

  // Callers should have snapped the selection at dialog open. Normalize again
  // against that same captured row set as a defensive boundary, never against a
  // later live checkbox selection.
  const members = topLevelIds(memberIds, ctxRows(ctx));
  if (members.length === 0) {
    return { groupId, name, placed: [], failed: [], uncertain: [], partial: false };
  }

  const outcomes = await Promise.all(
    members.map((id, index) =>
      ctxFetch(ctx)(`/api/workspaces/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ parent_id: groupId, order_index: index + 1 })
      })
        .then(response => ({ id, state: response?.ok ? 'placed' : 'failed' }))
        .catch(() => ({ id, state: 'uncertain' }))
    )
  );

  const placed = outcomes.filter(outcome => outcome.state === 'placed').map(outcome => outcome.id);
  const failed = outcomes.filter(outcome => outcome.state === 'failed').map(outcome => outcome.id);
  let uncertain = outcomes
    .filter(outcome => outcome.state === 'uncertain')
    .map(outcome => outcome.id);

  // Reconcile a lost PATCH before reporting it. A refresh error leaves the
  // member uncertain; it never authorizes an automatic mutation retry.
  if (uncertain.length) {
    try {
      await changed(ctx);
      if (typeof ctx?.verifyMembership === 'function') {
        const confirmed = await ctx.verifyMembership({ groupId, memberIds: uncertain });
        const confirmedSet = confirmed instanceof Set ? confirmed : new Set(confirmed || []);
        const confirmedIds = uncertain.filter(id => confirmedSet.has(id));
        placed.push(...confirmedIds);
        uncertain = uncertain.filter(id => !confirmedSet.has(id));
      }
    } catch (_) {
      // The durable group still exists. The caller receives uncertainty and can
      // offer refresh/open-group guidance instead of asserting a false result.
    }
  }

  if (!uncertain.length) await changed(ctx);

  const result = {
    groupId,
    name,
    // Promise settlement and lost-response reconciliation can complete out of
    // order; preserve the reviewed input order in the observable outcome.
    placed: members.filter(id => placed.includes(id)),
    failed: members.filter(id => failed.includes(id)),
    uncertain: members.filter(id => uncertain.includes(id)),
    partial: failed.length > 0 || uncertain.length > 0
  };
  if (result.partial) {
    const pieces = [];
    if (failed.length) pieces.push(`${failed.length} could not be moved`);
    if (uncertain.length) pieces.push(`${uncertain.length} could not be verified after refresh`);
    const message = `Group "${name}" was created, but ${pieces.join('; ')}.`;
    announce(ctx, message);
    toast(ctx, message, 'error');
  } else {
    announce(
      ctx,
      `Moved ${placed.length} workspace${placed.length === 1 ? '' : 's'} into "${name}".`
    );
  }
  return result;
}
