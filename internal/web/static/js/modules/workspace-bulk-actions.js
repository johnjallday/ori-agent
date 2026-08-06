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

function ctxPrompt(ctx) {
  if (ctx && typeof ctx.prompt === 'function') return ctx.prompt;
  if (typeof window !== 'undefined' && typeof window.prompt === 'function') {
    return message => window.prompt(message);
  }
  return () => null;
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
async function errorText(response, fallback) {
  try {
    const text = await response.text();
    if (!text) return fallback;
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed.error === 'string' && parsed.error) return parsed.error;
      if (parsed && typeof parsed.message === 'string' && parsed.message) return parsed.message;
      return text;
    } catch (_) {
      return text;
    }
  } catch (_) {
    return fallback;
  }
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
    if (!res.ok) throw new Error(await errorText(res, 'Failed to delete'));
    if (res.status !== 204) {
      const data = await res.json().catch(() => ({}));
      if (data && data.trashed) trashed(ctx, id, row.name);
    }
    announce(ctx, `${row.name} deleted.`);
    await changed(ctx);
    return true;
  } catch (err) {
    fail(ctx, err, 'Failed to delete.');
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
        failures.push(row.name);
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
  return deleted;
}

/**
 * Create a group and move the checked set into it.
 *
 * The selection is reduced to top-level ids first — see topLevelIds for why a
 * nested child must not be reparented out of a parent that is moving too.
 */
export async function createGroupFrom(memberIds, ctx) {
  const members = topLevelIds(memberIds, ctxRows(ctx));
  const name = ctxPrompt(ctx)('Name for the new group:');
  if (!name || !String(name).trim()) return null;
  const trimmed = String(name).trim();

  try {
    const res = await ctxFetch(ctx)('/api/workspaces', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: trimmed, kind: 'group' })
    });
    if (!res.ok) throw new Error(await errorText(res, 'Failed to create group'));
    const body = await res.json().catch(() => ({}));
    const groupId = body && body.folder && body.folder.id;

    if (groupId && members.length) {
      const responses = await Promise.all(
        members.map((id, index) =>
          ctxFetch(ctx)(`/api/workspaces/${encodeURIComponent(id)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ parent_id: groupId, order_index: index + 1 })
          })
        )
      );
      const failed = responses.find(response => !response.ok);
      // The group exists either way, so say so rather than reporting a clean
      // failure that leaves an empty group behind with no explanation.
      if (failed)
        throw new Error(await errorText(failed, 'Group created, but moving items failed'));
    }

    announce(ctx, `Group "${trimmed}" created.`);
    await changed(ctx);
    return groupId || null;
  } catch (err) {
    fail(ctx, err, 'Failed to create group.');
    await changed(ctx);
    return null;
  }
}
