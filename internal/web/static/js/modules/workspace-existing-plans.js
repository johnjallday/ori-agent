/**
 * workspace-existing-plans.js — the secondary "Existing plans" link under
 * Tickets.
 *
 * Ori no longer starts new Plans, so a workspace with none has no Plans
 * destination at all. A workspace that does have some (active or in History,
 * however they were made) keeps one quiet way to reach them: a link under
 * Tickets to its Plans list, shown only when the server says there is at least
 * one. Pure helpers plus one small renderer; the page owns the request and the
 * stale-answer rules.
 *
 * "Unknown is not zero." A read that failed is not an empty workspace: the
 * link area says it could not check and offers to try again, instead of
 * quietly hiding a way to reach a plan that exists.
 */

import { workspacePageURL } from './workspace-routes.js';

export const PLAN_PRESENCE_PATH = workspaceId =>
  `/api/workspaces/${encodeURIComponent(workspaceId)}/plan-presence`;

function count(value) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

/**
 * The state of what the server said. Anything that is not a clear answer is
 * 'unavailable', never 'none'.
 */
export function planPresenceFromResponse(body) {
  if (!body || typeof body !== 'object' || typeof body.has_plans !== 'boolean') {
    return { state: 'unavailable', active: 0, history: 0 };
  }
  const active = count(body.active);
  const history = count(body.history);
  // The counts and the flag must agree; a body that contradicts itself is not
  // an answer.
  if (body.has_plans !== active + history > 0) {
    return { state: 'unavailable', active: 0, history: 0 };
  }
  return { state: body.has_plans ? 'present' : 'none', active, history };
}

/**
 * Reads the presence of Plans for one workspace. Resolves to a presence state;
 * it never rejects, so a caller cannot forget the failure branch. `signal` lets
 * the page cancel a read it no longer wants.
 */
export async function readPlanPresence({ workspaceId, fetchImpl, signal } = {}) {
  const id = String(workspaceId || '').trim();
  const doFetch = fetchImpl || (typeof fetch === 'function' ? fetch.bind(globalThis) : null);
  if (!id || !doFetch) return { state: 'unavailable', active: 0, history: 0 };
  try {
    const response = await doFetch(PLAN_PRESENCE_PATH(id), {
      headers: { Accept: 'application/json' },
      signal
    });
    if (!response || !response.ok) return { state: 'unavailable', active: 0, history: 0 };
    return planPresenceFromResponse(await response.json());
  } catch {
    return { state: 'unavailable', active: 0, history: 0 };
  }
}

/**
 * What the link area shows for a presence state.
 *
 *   loading/none  nothing (a workspace with no Plans has no Plans destination)
 *   present       a link to this workspace's Plans list
 *   unavailable   an explained gap with a retry, never a silent absence
 */
export function existingPlansView(presence, { workspaceSlug = '' } = {}) {
  const state = presence && presence.state;
  const slug = String(workspaceSlug || '').trim();
  if (state === 'present' && slug) {
    const total = count(presence.active) + count(presence.history);
    const parts = [];
    if (count(presence.active)) parts.push(`${count(presence.active)} active`);
    if (count(presence.history)) parts.push(`${count(presence.history)} in history`);
    return {
      visible: true,
      kind: 'link',
      label: 'Existing plans',
      href: workspacePageURL(slug, ['plans']),
      title: `${total} ${total === 1 ? 'plan' : 'plans'} (${parts.join(', ')})`
    };
  }
  if (state === 'unavailable') {
    return {
      visible: true,
      kind: 'unavailable',
      message: 'Could not check for existing plans.',
      retryLabel: 'Try again'
    };
  }
  return { visible: false, kind: 'none' };
}

/**
 * Draws a view into `mount`. Text only: nothing here is parsed as markup.
 * `onRetry` runs when Try again is pressed.
 */
export function renderExistingPlans(mount, view, { onRetry, doc } = {}) {
  if (!mount) return;
  const document_ =
    doc || mount.ownerDocument || (typeof document !== 'undefined' ? document : null);
  while (mount.firstChild) mount.removeChild(mount.firstChild);
  mount.hidden = !view.visible;
  if (!view.visible || !document_) return;

  if (view.kind === 'link') {
    const link = document_.createElement('a');
    link.className = 'hub-tickets-existing-plans-link';
    link.href = view.href;
    link.textContent = view.label;
    link.title = view.title;
    mount.appendChild(link);
    return;
  }

  const note = document_.createElement('span');
  note.className = 'hub-tickets-existing-plans-note';
  note.textContent = `${view.message} `;
  const retry = document_.createElement('button');
  retry.type = 'button';
  retry.className = 'btn btn-link btn-sm p-0 align-baseline';
  retry.textContent = view.retryLabel;
  retry.setAttribute('data-existing-plans-retry', '');
  if (typeof onRetry === 'function') retry.addEventListener('click', () => onRetry());
  mount.appendChild(note);
  mount.appendChild(retry);
}
