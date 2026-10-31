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
// the user to another page through a native confirm. That covers a linked
// project (disconnected from its Home, then deleted), a Home with everything
// inside it (members first, the Home last), and a Home nested in a group.

import { workspacePageURL } from './workspace-routes.js';
import { openDeleteDialog } from './workspace-delete-dialog.js';

const REVIEW_REQUIRED = 'assistant_program_review_required';
const GROUP_REQUIREMENT_REVIEW_REQUIRED = 'group_requirement_review_required';
const GROUP_REQUIREMENT_REVIEW_HEADER = 'X-Ori-Group-Requirement-Review';
const REVIEW_HOME_REMOVAL = 'Review Home removal';
const REVIEW_DISCONNECT = 'Review disconnect';
const DELETE_CONTENTS = 'contents';
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
  if (response.status === 409 && payload?.code === GROUP_REQUIREMENT_REVIEW_REQUIRED) {
    const details = payload.details || {};
    error.groupRequirementReview = {
      workspaceId: typeof details.workspace_id === 'string' ? details.workspace_id.trim() : '',
      deleteSessions: details.delete_sessions === true
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

function plural(count, noun) {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

function idempotencyKey(prefix) {
  const uuid = globalThis.crypto?.randomUUID?.();
  return uuid
    ? `${prefix}-${uuid}`
    : `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

/** The Home's live program, so a review can be bound to its current revision. */
async function readStation(ctx, stationId) {
  const program = await requestJSON(ctx, assistantProgramURL(stationId));
  if (!program || !program.available || !program.is_station) {
    throw new Error('The Assistant Home could not be read. Nothing was deleted.');
  }
  return program;
}

function stationName(ctx, stationId, program) {
  return (
    findRow(ctx, stationId)?.name ||
    String(program?.declaration?.station_name || '').trim() ||
    'its Assistant Home'
  );
}

async function readDeleteResult(response) {
  if (response.status === 204) return { trashed: false };
  const data = await response.json().catch(() => ({}));
  return { trashed: Boolean(data && data.trashed) };
}

/**
 * DELETE one workspace that no Home protects any more.
 *
 * A Required template contract still asks for its own lifecycle review. It is
 * obtained and consumed right here, so the one confirmation the user already
 * gave covers the whole deletion; the server still verifies every step.
 * Resolves `{ trashed }` and throws with the server's reason otherwise.
 */
async function deleteDetached(ctx, row, mode) {
  const first = await ctxFetch(ctx)(deleteURL(row.id, mode), { method: 'DELETE' });
  if (first.ok) return readDeleteResult(first);
  const error = await requestError(first, 'Failed to delete');
  const requested = error.groupRequirementReview;
  if (!requested || requested.workspaceId !== row.id) throw error;
  const payload = await requestJSON(
    ctx,
    `/api/workspaces/${encodeURIComponent(row.id)}/group-requirement/delete/review`,
    { method: 'POST', body: { delete_sessions: requested.deleteSessions } }
  );
  const token = String(payload?.group_requirement_review?.review_token || '').trim();
  if (!token) throw new Error('The Required template contract review was incomplete.');
  const second = await ctxFetch(ctx)(deleteURL(row.id, mode), {
    method: 'DELETE',
    headers: { Accept: 'application/json', [GROUP_REQUIREMENT_REVIEW_HEADER]: token }
  });
  if (!second.ok) throw await requestError(second, 'Reviewed deletion failed');
  return readDeleteResult(second);
}

/**
 * Disconnect `row` from the Home `stationId`. The review is taken fresh, bound
 * to the Home's live revision, and committed at once. `state` remembers a
 * finished disconnect so a retry after a later failure never repeats it.
 */
async function disconnect(ctx, session, row, stationId, state) {
  if (state.disconnected) return;
  session.busy(`Disconnecting ${row.name}…`);
  const program = await readStation(ctx, stationId);
  const review = await requestJSON(ctx, assistantProgramURL(stationId, '/disconnect/review'), {
    method: 'POST',
    body: { project_workspace_id: row.id, state_revision: Number(program.state_revision || 0) }
  });
  await requestJSON(ctx, assistantProgramURL(stationId, '/disconnect/commit'), {
    method: 'POST',
    body: { token: review.token, idempotency_key: idempotencyKey('disconnect') }
  });
  state.disconnected = true;
}

async function disconnectAndDelete(ctx, session, row, stationId, mode, state) {
  await disconnect(ctx, session, row, stationId, state);
  session.busy(`Deleting ${row.name}…`);
  const result = await deleteDetached(ctx, row, mode);
  if (result.trashed) trashed(ctx, row.id, row.name);
}

/** Remove a Home whose revision moved since it was previewed: fresh review, then commit. */
async function removeHome(ctx, session, stationId, homeName) {
  session.busy(`Removing ${homeName}…`);
  const program = await readStation(ctx, stationId);
  const impact = await requestJSON(ctx, assistantProgramURL(stationId, '/remove-home/review'), {
    method: 'POST',
    body: { state_revision: Number(program.state_revision || 0) }
  });
  const receipt = await requestJSON(ctx, assistantProgramURL(stationId, '/remove-home/commit'), {
    method: 'POST',
    body: { token: impact.token }
  });
  if (receipt && receipt.trashed) trashed(ctx, stationId, homeName);
}

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
 * "Review Home removal" removes the Home; with the "everything inside it"
 * mode its members go first. "Review disconnect" disconnects the linked
 * project and, unless it is only being un-nested, deletes it too.
 *
 * Resolves 'done' when the row itself is gone, 'retry' when a blocker inside
 * the row was cleared and the original delete should run again, and false
 * when nothing changed. The caller owns closing the session either way.
 */
async function resolveRemovalReview(ctx, session, row, error, mode = '') {
  const review = error.review || {};
  const homeHref = review.homeSlug ? workspacePageURL(review.homeSlug, ['assistant']) : '';
  announce(ctx, error.message);
  // A Home without a page is trashed or missing: there is nothing live to
  // review against, so the server's recovery instructions are the answer.
  const resolvable =
    Boolean(review.stationId && homeHref) &&
    (review.action === REVIEW_HOME_REMOVAL || review.action === REVIEW_DISCONNECT);
  if (!resolvable) {
    await session.notice({
      message: error.message,
      action: homeHref ? { label: 'Open Assistant Home', href: homeHref } : null
    });
    return false;
  }
  if (review.action === REVIEW_DISCONNECT) {
    return resolveDisconnectReview(ctx, session, row, review, mode, homeHref);
  }
  return resolveHomeRemovalReview(ctx, session, row, review, mode);
}

async function resolveDisconnectReview(ctx, session, row, review, mode, homeHref) {
  const nested = Boolean(review.workspaceId) && review.workspaceId !== row.id;
  const target = nested ? findRow(ctx, review.workspaceId) : row;
  if (!target) {
    await session.notice({
      message: 'The linked project could not be found on this page. Refresh and try again.',
      action: { label: 'Open Assistant Home', href: homeHref }
    });
    return false;
  }
  // Un-nesting a group keeps its members, so a linked member is only
  // disconnected; deleting the project itself, or a group with its contents,
  // deletes the member as well.
  const deleteTarget = !nested || mode === DELETE_CONTENTS;

  session.busy('Checking what this deletion affects…');
  let program;
  let preview;
  try {
    program = await readStation(ctx, review.stationId);
    preview = await requestJSON(ctx, assistantProgramURL(review.stationId, '/disconnect/review'), {
      method: 'POST',
      body: { project_workspace_id: target.id, state_revision: Number(program.state_revision || 0) }
    });
  } catch (err) {
    announce(ctx, err.message);
    await session.notice({ message: err.message || 'The disconnect could not be reviewed.' });
    return false;
  }
  const homeName = stationName(ctx, review.stationId, program);

  const sentences = [];
  sentences.push(
    nested
      ? `"${row.name}" contains "${target.name}", which is linked to the Assistant Home "${homeName}".`
      : `"${target.name}" is linked to the Assistant Home "${homeName}".`
  );
  sentences.push(
    deleteTarget
      ? 'It will be disconnected from the Home first, then moved to the Trash. Restoring it with Undo does not reconnect it.'
      : 'It will be disconnected from the Home and kept as a standalone workspace.'
  );
  if (nested) sentences.push(`Deleting "${row.name}" then continues.`);
  // The server's disconnect notes promise the project is preserved, which is
  // true of the disconnect alone; when the deletion follows, say what the
  // whole step does instead.
  const impact = deleteTarget
    ? [
        `It stops appearing in the portfolio of "${homeName}", and Home handoffs to it stop.`,
        'The workspace, its team, tasks and files move to the Trash together and can be restored with Undo.',
        `"${homeName}" itself is not removed.`
      ]
    : Array.isArray(preview.impact)
      ? preview.impact
      : [];

  const state = {};
  const done = await session.review({
    heading: nested
      ? `Disconnect the linked project in "${row.name}"?`
      : `Delete "${target.name}"?`,
    summary: sentences.join(' '),
    impact,
    confirmLabel: deleteTarget ? 'Disconnect and delete' : 'Disconnect',
    progress: 'Disconnecting…',
    confirm: async () => {
      if (deleteTarget) {
        await disconnectAndDelete(ctx, session, target, review.stationId, '', state);
      } else {
        await disconnect(ctx, session, target, review.stationId, state);
      }
    }
  });
  if (!done) return false;
  announce(
    ctx,
    deleteTarget ? `${target.name} deleted.` : `${target.name} disconnected from ${homeName}.`
  );
  return nested ? 'retry' : 'done';
}

async function resolveHomeRemovalReview(ctx, session, row, review, mode) {
  session.busy('Checking what this removal affects…');
  let program;
  let impact;
  try {
    program = await readStation(ctx, review.stationId);
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
  const homeName = isSelf ? row.name : stationName(ctx, review.stationId, program);
  const linked = Math.max(0, Number(impact.linked_project_count) || 0);
  const roles = Math.max(0, Number(impact.home_role_count) || 0);

  // "Delete the group and everything inside it" on a Home: the members go
  // first (linked ones disconnected, each through its own server review) and
  // the Home last, all under this one confirmation.
  if (isSelf && mode === DELETE_CONTENTS) {
    return cascadeHomeDeletion(ctx, session, row, program, homeName, linked);
  }

  // The server says whether the commit will move the Home to the Trash (the
  // normal case) or delete it outright (a platform with no trash). The dialog
  // only promises an Undo when it will actually work.
  const trashes = Boolean(impact.trashes);

  // The summary carries the two facts the impact list does not lead with: what
  // happens to linked projects (kept, never deleted) and whether the Home
  // itself is recoverable.
  const sentences = [];
  sentences.push(
    isSelf
      ? `"${homeName}" is an Assistant Home.`
      : `"${row.name}" contains the Assistant Home "${homeName}", which has to be removed first.`
  );
  if (linked === 0) {
    sentences.push(
      roles > 0
        ? `It has no linked projects, so removing it affects only the group and its ${plural(roles, 'Home role')}.`
        : 'It has no linked projects, so removing it affects only the group.'
    );
  } else {
    sentences.push(
      `Its ${plural(linked, 'linked project')} will be kept as standalone workspace${linked === 1 ? '' : 's'}.`
    );
  }
  sentences.push(
    trashes
      ? 'The Home moves to the Trash and can be restored with Undo, without its projects.'
      : 'The Home is removed permanently rather than moved to the Trash.'
  );
  if (!isSelf) sentences.push(`Deleting "${row.name}" then continues.`);

  let receipt = null;
  const removed = await session.review({
    heading: isSelf ? `Remove "${homeName}"?` : `Remove the Assistant Home in "${row.name}"?`,
    summary: sentences.join(' '),
    // An empty Home has nothing to preserve, so the server's preservation
    // notes would only make a plain delete read as a complicated one.
    impact: linked > 0 ? impact.impact : [],
    confirmLabel: 'Remove Home',
    progress: 'Removing Home…',
    confirm: async () => {
      receipt = await requestJSON(
        ctx,
        assistantProgramURL(review.stationId, '/remove-home/commit'),
        {
          method: 'POST',
          body: { token: impact.token }
        }
      );
    }
  });
  if (!removed) return false;
  // A trashed Home is an undo entry like any other trashed workspace: the
  // restore endpoint puts it back as a Home. The receipt, not the review, is
  // authoritative, because only the commit knows what the store actually did.
  if (receipt && receipt.trashed) trashed(ctx, review.stationId, homeName);
  announce(ctx, `${homeName} removed.`);
  return isSelf ? 'done' : 'retry';
}

/**
 * Delete a Home and everything inside it under one confirmation.
 *
 * Members are deleted one at a time, linked projects disconnected first, each
 * through the server's own review-then-commit; the Home is removed last with a
 * review taken after its membership changed. Progress survives a failure: a
 * second click skips what already went.
 */
async function cascadeHomeDeletion(ctx, session, row, program, homeName, linked) {
  const members = ctxRows(ctx).filter(member => member && member.parent_id === row.id);
  const linkedIds = new Set(
    (Array.isArray(program.projects) ? program.projects : []).map(project => project && project.id)
  );
  const linkedMembers = members.filter(member => linkedIds.has(member.id));
  const otherMembers = members.filter(member => !linkedIds.has(member.id));
  const outside = Math.max(0, linked - linkedMembers.length);
  const names = list => list.map(member => `"${member.name}"`).join(', ');

  const sentences = [`"${homeName}" is an Assistant Home.`];
  if (members.length === 0) {
    sentences.push('Nothing is inside it, so only the Home itself is removed.');
  } else {
    const parts = [];
    if (linkedMembers.length) {
      parts.push(
        `${plural(linkedMembers.length, 'linked project')} will be disconnected and moved to the Trash`
      );
    }
    if (otherMembers.length) {
      parts.push(`${plural(otherMembers.length, 'other workspace')} will move to the Trash`);
    }
    sentences.push(`${parts.join(', and ')}, then the Home is removed.`);
  }
  if (outside > 0) {
    sentences.push(
      `${plural(outside, 'linked project')} outside this group ${outside === 1 ? 'is' : 'are'} kept as standalone workspace${outside === 1 ? '' : 's'}.`
    );
  }
  sentences.push(
    'Each item is restored separately with Undo; restoring the Home does not reconnect its projects.'
  );

  const impact = [];
  if (linkedMembers.length) impact.push(`Linked projects: ${names(linkedMembers)}.`);
  if (otherMembers.length) impact.push(`Other workspaces inside: ${names(otherMembers)}.`);

  const done = new Set();
  const states = new Map();
  const stateFor = id => {
    if (!states.has(id)) states.set(id, {});
    return states.get(id);
  };
  const memberMode = member => (isGroupRow(member) ? DELETE_CONTENTS : '');
  const removed = await session.review({
    heading: `Delete "${homeName}" and everything inside it?`,
    summary: sentences.join(' '),
    impact,
    confirmLabel: 'Delete everything',
    progress: 'Deleting…',
    confirm: async () => {
      for (const member of linkedMembers) {
        if (done.has(member.id)) continue;
        await disconnectAndDelete(
          ctx,
          session,
          member,
          row.id,
          memberMode(member),
          stateFor(member.id)
        );
        done.add(member.id);
      }
      for (const member of otherMembers) {
        if (done.has(member.id)) continue;
        session.busy(`Deleting ${member.name}…`);
        const result = await deleteDetached(ctx, member, memberMode(member));
        if (result.trashed) trashed(ctx, member.id, member.name);
        done.add(member.id);
      }
      await removeHome(ctx, session, row.id, homeName);
    }
  });
  if (!removed) return false;
  announce(ctx, `${homeName} and ${plural(members.length, 'workspace')} inside it deleted.`);
  return 'done';
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
async function resolveGroupRequirementDeletionReview(ctx, session, row, error, mode) {
  const requested = error.groupRequirementReview || {};
  if (!requested.workspaceId || requested.workspaceId !== row.id) {
    await session.notice({ message: error.message });
    return false;
  }

  session.busy('Reviewing the Required template contract…');
  let payload;
  try {
    payload = await requestJSON(
      ctx,
      `/api/workspaces/${encodeURIComponent(row.id)}/group-requirement/delete/review`,
      { method: 'POST', body: { delete_sessions: requested.deleteSessions } }
    );
  } catch (reviewError) {
    await session.notice({
      message: reviewError.message || 'The Required template contract could not be reviewed.'
    });
    return false;
  }
  const review = payload?.group_requirement_review || {};
  const token = String(review.review_token || '').trim();
  if (!token) {
    await session.notice({ message: 'The Required template contract review was incomplete.' });
    return false;
  }

  let result = {};
  const removed = await session.review({
    heading: `Delete “${row.name}” after contract review?`,
    summary:
      review.summary ||
      'This workspace is detached from its required group. Review deletion before continuing.',
    impact: Array.isArray(review.impact) ? review.impact : [],
    confirmLabel: review.trashes ? 'Move to Trash' : 'Delete workspace',
    progress: review.trashes ? 'Moving to Trash…' : 'Deleting workspace…',
    confirm: async () => {
      const response = await ctxFetch(ctx)(deleteURL(row.id, mode), {
        method: 'DELETE',
        headers: { Accept: 'application/json', [GROUP_REQUIREMENT_REVIEW_HEADER]: token }
      });
      if (!response.ok) throw await requestError(response, 'Reviewed deletion failed');
      if (response.status !== 204) result = await response.json().catch(() => ({}));
    }
  });
  if (!removed) return false;
  if (result?.trashed) trashed(ctx, row.id, row.name);
  return true;
}

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
    const outcome = await resolveRemovalReview(ctx, session, row, error, mode);
    if (!outcome) {
      session.close();
      return false;
    }
    if (outcome === 'done') {
      session.close();
      await changed(ctx);
      return true;
    }
    return performDelete(ctx, session, row, mode, round + 1);
  }
  if (error && error.groupRequirementReview) {
    const removed = await resolveGroupRequirementDeletionReview(ctx, session, row, error, mode);
    session.close();
    if (!removed) return false;
    announce(ctx, `${row.name} deleted.`);
    await changed(ctx);
    return true;
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
  const blocked = [];
  for (const id of selected) {
    const row = findRow(ctx, id);
    if (!row) continue;
    try {
      // No delete_mode: the server's safe default un-nests a group's members
      // rather than destroying them implicitly.
      const res = await ctxFetch(ctx)(deleteURL(id, ''), { method: 'DELETE' });
      if (!res.ok) {
        const error = await requestError(res, 'Failed to delete');
        if (error.review || error.groupRequirementReview) blocked.push({ row, error });
        else failures.push(`${row.name}: ${error.message}`);
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
  if (deleted > 0) await changed(ctx);

  // Every refusal that a review can resolve is reviewed here, one after the
  // other, in the order the items were selected. A Home removed for one entry
  // also unblocks the others it protected, so those are simply retried.
  const removedStations = new Set();
  for (const { row, error } of blocked) {
    let gone = false;
    try {
      if (error.review) {
        const stationId = error.review.stationId;
        let outcome = 'retry';
        if (!removedStations.has(stationId)) {
          outcome = await resolveRemovalReview(ctx, session, row, error, '');
          if (outcome && error.review.action === REVIEW_HOME_REMOVAL)
            removedStations.add(stationId);
        }
        if (outcome === 'done') {
          gone = true;
        } else if (outcome === 'retry') {
          session.busy(`Deleting ${row.name}…`);
          const result = await deleteDetached(ctx, row, '');
          if (result.trashed) trashed(ctx, row.id, row.name);
          gone = true;
        }
      } else {
        gone = await resolveGroupRequirementDeletionReview(ctx, session, row, error, '');
      }
    } catch (err) {
      failures.push(`${row.name}: ${err && err.message ? err.message : 'Failed to delete'}`);
      continue;
    }
    if (gone) {
      deleted += 1;
      await changed(ctx);
    } else {
      failures.push(`${row.name}: not deleted`);
    }
  }
  session.close();

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
