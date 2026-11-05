// library-staffing.js — a library project's own way to get its Home's shared
// assistant (library-setup-staffing, group 3).
//
// A song put on the map before its Home could staff it has an empty roster and
// offers "Hire for REAPER Assistant". When its Home already approved sharing
// one assistant across its projects, this offers that instead, beside the
// ordinary Hire (which stays: the person may want this song's own agent).
//
// The status read is inert. Only the explicit button POSTs, and the server
// resolves Home and catalog row from the workspace itself: the browser names
// nothing else. docs/architecture/project-library.md, "Shared assistant on
// confirmed setup and repair".

function statusPath(workspaceId) {
  return `/api/workspaces/${encodeURIComponent(String(workspaceId || ''))}/library-staffing`;
}

/**
 * The project's staffing status, or null when it is not a library project of
 * the caller's Home (or the read failed: never a guess that it can be staffed).
 */
export async function loadProjectStaffing(
  workspaceId,
  fetchImpl = (...a) => globalThis.fetch(...a)
) {
  if (!workspaceId) return null;
  try {
    const response = await fetchImpl(statusPath(workspaceId), {
      headers: { Accept: 'application/json' },
      cache: 'no-store'
    });
    if (!response.ok) return null;
    const view = await response.json();
    return view && view.eligible ? view : null;
  } catch (_) {
    return null;
  }
}

/**
 * What the empty roster offers, from the status. Pure. null when there is
 * nothing to offer (not a library project, or its role is already filled).
 *   canAdd: the Home's approval can fill the role now (create or join)
 *   label / line: the button and what it does
 *   blocked: why it cannot (sharing off, older team, assistant deleted, …)
 */
export function projectStaffingOffer(view) {
  if (!view || !view.eligible || view.filled) return null;
  const role = String(view.role_label || 'assistant');
  const name = String(view.agent_name || role);
  const home = String(view.home_name || 'your Home');
  if (view.plan === 'join') {
    return {
      canAdd: true,
      label: `Add the shared ${role}`,
      line: `${home}'s shared ${role} ("${name}") joins this project. No new agent is made, no ticket runs and no model is called.`,
      blocked: ''
    };
  }
  if (view.plan === 'create') {
    return {
      canAdd: true,
      label: `Add the shared ${role}`,
      line: `Creates ${home}'s shared ${role} ("${name}") and adds it here; the Home's other projects can join the same one. No ticket runs and no model is called.`,
      blocked: ''
    };
  }
  return {
    canAdd: false,
    label: '',
    line: '',
    blocked: String(view.message || `${home}'s shared ${role} cannot be added here right now.`)
  };
}

/**
 * Add the Home's shared assistant to this project. Resolves
 * { ok: true, staffing } or { ok: false, reason, message }. A lost response is
 * reported as unknown so the caller re-reads instead of guessing.
 */
export async function addSharedAssistant(
  workspaceId,
  view,
  {
    requestId = `library-staffing-${globalThis.crypto.randomUUID()}`,
    fetchImpl = (...a) => globalThis.fetch(...a)
  } = {}
) {
  try {
    const response = await fetchImpl(statusPath(workspaceId), {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({ request_id: requestId, team_digest: String(view?.team_digest || '') })
    });
    let body = {};
    try {
      body = await response.json();
    } catch (_) {
      body = {};
    }
    if (response.ok) return { ok: true, staffing: body.staffing || null };
    return {
      ok: false,
      reason: String(body.reason || ''),
      message: String(body.error || body.message || 'The assistant could not be added.')
    };
  } catch (_) {
    return {
      ok: false,
      reason: 'unknown',
      message: 'Ori could not confirm whether the assistant was added. Showing its current state.'
    };
  }
}

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * The offer's markup for an empty roster, before the ordinary Hire button.
 * Pure. The button carries data-shared-assistant-add; the caller handles it.
 */
export function sharedAssistantOfferHTML(offer, buttonClass = 'ws-cmd-agent-action is-primary') {
  if (!offer) return '';
  if (!offer.canAdd) {
    return offer.blocked
      ? `<p class="library-staffing-offer is-blocked" data-shared-assistant-blocked>${escapeHtml(offer.blocked)}</p>`
      : '';
  }
  return (
    '<div class="library-staffing-offer" data-shared-assistant-offer>' +
    `<p class="library-staffing-offer-line">${escapeHtml(offer.line)}</p>` +
    `<button type="button" class="${escapeHtml(buttonClass)}" data-shared-assistant-add>${escapeHtml(offer.label)}</button>` +
    '<p class="library-staffing-offer-status" role="status" aria-live="polite" data-shared-assistant-status></p>' +
    '</div>'
  );
}
