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
// Everything the operations touch — the row set, confirm/prompt, fetch, and the
// host's announce/toast/refresh callbacks — arrives through `ctx`, so these run
// under plain Node in tests with no DOM and no network.

import { workspacePageURL } from './workspace-routes.js';

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

function ctxConfirm(ctx) {
  if (ctx && typeof ctx.confirm === 'function') return ctx.confirm;
  if (typeof window !== 'undefined' && typeof window.confirm === 'function') {
    return message => window.confirm(message);
  }
  // No way to ask, so no way to proceed: never delete without confirmation.
  return () => false;
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
  if (
    response.status === 409 &&
    payload?.code === 'assistant_program_review_required' &&
    typeof payload.details?.review_home_slug === 'string' &&
    payload.details.review_home_slug.trim()
  ) {
    error.reviewHomeSlug = payload.details.review_home_slug;
  }
  return error;
}

// Navigating to a review is not permission to disconnect or remove anything.
// Ask once after the batch, and never redirect to a missing/trashed Home.
function offerRemovalReview(ctx, errors) {
  const homes = [...new Set(errors.map(error => error?.reviewHomeSlug).filter(Boolean))];
  if (homes.length !== 1) return;
  if (
    !ctxConfirm(ctx)(
      'This removal needs an assistant review. Open the Assistant Home now? Nothing will be deleted by opening it.'
    )
  )
    return;
  const url = workspacePageURL(homes[0], ['assistant']);
  if (typeof ctx?.navigate === 'function') ctx.navigate(url);
  else if (typeof window !== 'undefined') window.location.assign(url);
}

/** Report a failure the same way on every path: SR announcement + toast. */
function fail(ctx, err, fallback) {
  const message = err && err.message ? err.message : fallback;
  announce(ctx, message);
  toast(ctx, message, 'error');
}

// ---------------------------------------------------------------------------
// Confirmations
// ---------------------------------------------------------------------------

/**
 * Delete confirmation. Groups keep their two-mode choice (group and contents,
 * or group only), and nothing is ever deleted without a confirmation.
 * Returns `{ mode }` to proceed, or null to abort.
 */
export function confirmDelete(row, isGroup, ask) {
  if (!isGroup) {
    return ask(`Delete "${row.name}"?\n\nIt moves to the Trash and can be restored with Undo.`)
      ? { mode: '' }
      : null;
  }
  const withContents = ask(
    `Delete the group "${row.name}"?\n\n` +
      'OK — delete the group AND everything inside it.\n' +
      'Cancel — choose to keep the workspaces instead.'
  );
  if (withContents) return { mode: 'contents' };
  const groupOnly = ask(
    `Delete only the group "${row.name}" and move its workspaces back to the top level?`
  );
  return groupOnly ? { mode: 'group_only' } : null;
}

export function confirmBulkDelete(count, ask) {
  return ask(
    `Delete ${count} selected item${count === 1 ? '' : 's'}?\n\n` +
      'They move to the Trash and can be restored with Undo.'
  );
}

// ---------------------------------------------------------------------------
// Operations
// ---------------------------------------------------------------------------

/** Delete a single workspace or group, with its own confirmation. */
export async function deleteWorkspace(id, ctx) {
  const row = findRow(ctx, id);
  if (!row) return false;
  const group = isGroupRow(row);
  const confirmed = confirmDelete(row, group, ctxConfirm(ctx));
  if (!confirmed) return false;

  const query = group && confirmed.mode ? `&delete_mode=${encodeURIComponent(confirmed.mode)}` : '';
  try {
    const res = await ctxFetch(ctx)(
      `/api/workspaces/${encodeURIComponent(id)}?confirm=true${query}`,
      { method: 'DELETE' }
    );
    if (!res.ok) throw await requestError(res, 'Failed to delete');
    if (res.status !== 204) {
      const data = await res.json().catch(() => ({}));
      if (data && data.trashed) trashed(ctx, id, row.name);
    }
    announce(ctx, `${row.name} deleted.`);
    await changed(ctx);
    return true;
  } catch (err) {
    fail(ctx, err, 'Failed to delete.');
    offerRemovalReview(ctx, [err]);
    return false;
  }
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
  if (!confirmBulkDelete(selected.length, ctxConfirm(ctx))) return 0;

  let deleted = 0;
  const failures = [];
  const errors = [];
  for (const id of selected) {
    const row = findRow(ctx, id);
    if (!row) continue;
    try {
      // No delete_mode: the server's safe default un-nests a group's members
      // rather than destroying them implicitly.
      const res = await ctxFetch(ctx)(`/api/workspaces/${encodeURIComponent(id)}?confirm=true`, {
        method: 'DELETE'
      });
      if (!res.ok) {
        const error = await requestError(res, 'Failed to delete');
        failures.push(`${row.name}: ${error.message}`);
        errors.push(error);
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
  offerRemovalReview(ctx, errors);
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
  if (!groupId) throw new Error('Created group identity is unavailable; refresh before moving members.');

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
    announce(ctx, `Moved ${placed.length} workspace${placed.length === 1 ? '' : 's'} into "${name}".`);
  }
  return result;
}
