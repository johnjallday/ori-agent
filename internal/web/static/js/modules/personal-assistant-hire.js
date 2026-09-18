/*
 * personal-assistant-hire.js — the one path that hires the personal assistant.
 *
 * Mission 01, "Meet your assistant", hires the assistant from the Agents
 * page's own New Agent panel, opened in a personal-assistant preset. This
 * module is everything that preset needs that is not markup: the hire payload,
 * the durable request id, the hire and repair requests, and which view a
 * relationship state calls for. The server owns every consequence; nothing
 * here decides that a hire happened without the server saying so.
 *
 * The roster controller is a classic script, so the same functions are
 * published on window.OriAssistantHire at the end of this file.
 */

// The route Ori's deterministic Personal HQ walkthrough activates on.
export const HQ_QUEST_ROUTE = '/?quest=build-hq';

// Mission 01's action URL: the walkthrough from its first step, on Home, where
// Ori points at the Agents nav entry for the user to click. Every entry that
// starts the hire resolves here: the Home mission card, Today's banner, Ask
// Ori's hand-off, and the retired /?hire=1 link.
export const MEET_ASSISTANT_QUEST_ROUTE = '/?quest=meet-assistant';

// The Agents page leg of the same walkthrough, for an entry that is already on
// its way there: the first step's "Take me there", the repair banners (a
// reconnect or resume view opens straight away on arrival), and the pointer on
// /agents/create.
export const MEET_ASSISTANT_AGENTS_ROUTE = '/agents?quest=meet-assistant';

// Set in sessionStorage the moment a hire succeeds, read (and cleared) by the
// Build My HQ walkthrough so its first step can say the hand-over line.
export const JUST_HIRED_FLAG = 'ori:assistant-just-hired';

// The browser's copy of the in-flight hire request id. One key, as the retired
// onboarding wizard used, so a hire started there resumes here.
export const HIRE_REQUEST_STORAGE_KEY = 'ori.personalAssistantHireRequestId';

// The server's default name for a new assistant (personalassistant.DefaultAssistantName).
export const DEFAULT_ASSISTANT_NAME = 'Assistant';

export const ASSISTANT_NAME_MAX_LENGTH = 100;
export const ASSISTANT_MANDATE_MAX_LENGTH = 1000;

export const MANDATE_PLACEHOLDER =
  'For example: Keep the week realistic and surface anything I may drop.';

// The promise the hire makes, kept verbatim: it sits directly above the one
// button that confirms the hire.
export const HIRE_BOUNDARY_COPY =
  'Hiring creates your assistant. It does not create a workspace, change any permission, or connect an account. Personal HQ — your assistant’s home base — is the next step, and nothing is created there until you confirm it.';

// The focus areas a hire offers, with their defaults. The values are the
// durable payload values the server validates.
export const GENERIC_FOCUS_AREAS = Object.freeze([
  { value: 'plan_my_day', label: 'Plan my day', selected: true },
  {
    value: 'track_commitments_and_follow_ups',
    label: 'Track commitments and follow-ups',
    selected: true
  },
  { value: 'prepare_for_meetings', label: 'Prepare for meetings', selected: false },
  { value: 'keep_projects_moving', label: 'Keep projects moving', selected: true },
  { value: 'help_with_email', label: 'Help with email', selected: false },
  { value: 'something_else', label: 'Something else', selected: false }
]);

export function personalAssistantResumeMessage(state = {}) {
  const name = String(state.display_name || '').trim();
  const hasAssistant = Boolean(state.assistant_id || name);
  const hasHQ = Boolean(state.hq_workspace_id);
  if (hasAssistant && hasHQ) {
    return `${name || 'Your assistant'} and Personal HQ are already saved. Retry to finish the remaining setup step.`;
  }
  if (hasHQ) return 'Personal HQ is already saved. Retry to finish the remaining setup step.';
  if (hasAssistant) {
    return `${name || 'Your assistant'} is already saved. Retry to finish the remaining setup step.`;
  }
  return 'This hire is already in progress. Retry to finish the remaining setup step.';
}

// buildPersonalAssistantHirePayload builds the hire request body.
//
// It carries no Daily Brief rhythm: hiring creates the assistant profile and the
// relationship only. The schedule belongs to the Map's Build My HQ form, where
// it can be written against a real workspace ID.
export function buildPersonalAssistantHirePayload({
  requestId,
  ifVersion = 0,
  displayName = DEFAULT_ASSISTANT_NAME,
  appearance = null,
  mandate = '',
  focusAreas = []
} = {}) {
  return {
    request_id: String(requestId || '').trim(),
    if_version: Number(ifVersion) || 0,
    display_name: String(displayName || '').trim(),
    appearance: appearance || { mode: 'generated', generated: {} },
    mandate: String(mandate || '').trim(),
    focus_areas: Array.from(new Set((focusAreas || []).map(value => String(value).trim()))).filter(
      Boolean
    )
  };
}

// personalAssistantNeedsHQ reports whether a relationship is hired but has no
// Personal HQ yet, in either the idle or the resumable-setup stage.
export function personalAssistantNeedsHQ(state = {}) {
  return ['needs_hq', 'provisioning_hq'].includes(String(state?.state || '').trim());
}

export function personalAssistantRecoveryView(state = {}) {
  const relationshipState = String(state?.state || '').trim();
  const repairStep = String(state?.repair_step || '').trim();
  const repair =
    relationshipState === 'repair_needed' && repairStep.startsWith('relationship_recovery');
  return {
    repair,
    available: repair && repairStep === 'relationship_recovery',
    blocked: repair && repairStep === 'relationship_recovery_blocked'
  };
}

export function personalAssistantCanOpenHireFlow(state = {}) {
  return [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'active',
    'repair_needed'
  ].includes(String(state?.state || '').trim());
}

// newRequestId makes an id the server has never seen.
function newRequestId() {
  return globalThis.crypto && typeof globalThis.crypto.randomUUID === 'function'
    ? globalThis.crypto.randomUUID()
    : `hire-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

// getOrCreateHireRequestId returns the id this hire attempt must use, so a
// retry after a network failure replays the same request and never creates a
// second assistant. The server's own record of an in-flight hire wins over the
// browser's; the browser's wins over a new id. Storage failures are tolerated:
// the durable server operation stays authoritative.
export function getOrCreateHireRequestId(storage, serverRequestId = '') {
  const fromServer = String(serverRequestId || '').trim();
  let stored = '';
  try {
    stored = String(storage?.getItem(HIRE_REQUEST_STORAGE_KEY) || '').trim();
  } catch (_) {
    stored = '';
  }
  const id = fromServer || stored || newRequestId();
  if (id !== stored) {
    try {
      storage?.setItem(HIRE_REQUEST_STORAGE_KEY, id);
    } catch (_) {
      // Nothing to do: the next attempt simply cannot replay from the browser.
    }
  }
  return id;
}

// clearHireRequestId forgets the browser's copy. Call it only after the server
// has returned the durable relationship, or a retry could not replay the hire.
export function clearHireRequestId(storage) {
  try {
    storage?.removeItem(HIRE_REQUEST_STORAGE_KEY);
  } catch (_) {
    // Storage cleanup is not part of the durable hire transaction.
  }
}

const HIRE_NETWORK_ERROR =
  'Ori could not reach the server. Retry — this sends the same request and will not hire a second assistant.';

// submitHire posts one hire and reports what the server said.
//
// { ok, state, resumed, needsHQ, error, status, code }. A replay of a durable
// hire (resumed) is a success, and so is a 409 personal_hq_required, which means
// "already hired" and never "hire again".
export async function submitHire({ fetchImpl = globalThis.fetch, payload } = {}) {
  let response;
  try {
    response = await fetchImpl('/api/personal-assistant/hire', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(payload || {})
    });
  } catch (_) {
    return { ok: false, state: null, resumed: false, needsHQ: false, error: HIRE_NETWORK_ERROR };
  }
  const body = await response.json().catch(() => ({}));
  if (response.ok) {
    const state = body?.personal_assistant || null;
    return {
      ok: true,
      state,
      resumed: state?.resumed === true,
      needsHQ: personalAssistantNeedsHQ(state),
      error: ''
    };
  }
  if (response.status === 409 && body?.code === 'personal_hq_required') {
    return { ok: true, state: null, resumed: true, needsHQ: true, error: '' };
  }
  return {
    ok: false,
    state: null,
    resumed: false,
    needsHQ: false,
    status: response.status,
    code: String(body?.code || ''),
    error: String(body?.error || '').trim() || 'Could not finish hiring. Retry this same request.'
  };
}

// submitRepair reconnects a server-validated orphan identity. The request
// carries only the version the reconnect view was rendered from: the server
// re-inspects the identity itself, so the browser never names one.
export async function submitRepair({ fetchImpl = globalThis.fetch, stateVersion = 0 } = {}) {
  let response;
  try {
    response = await fetchImpl('/api/personal-assistant/repair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ if_version: Number(stateVersion) || 0 })
    });
  } catch (_) {
    return {
      ok: false,
      state: null,
      needsHQ: false,
      error: 'Ori could not reach the server. Nothing was changed.'
    };
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    return {
      ok: false,
      state: null,
      needsHQ: false,
      status: response.status,
      error:
        String(body?.error || '').trim() ||
        'Ori could not prove that these records belong to one assistant. Nothing was changed.'
    };
  }
  const state = body?.personal_assistant || null;
  return { ok: true, state, needsHQ: personalAssistantNeedsHQ(state), error: '' };
}

// presetView says what the New Agent panel shows for a relationship state.
//
//   form       the hire preset (not hired yet, or a hire in flight)
//   resume     a partial hire: one button that replays the same request
//   reconnect  an orphan identity the server can prove: one Reconnect button
//   blocked    contradictory records: the status, and no button at all
//   standard   anything else, including every hired state: the ordinary form
export function presetView(state) {
  const relationshipState = String(state?.state || '').trim();
  const recovery = personalAssistantRecoveryView(state);
  const name = String(state?.display_name || '').trim() || 'your assistant';
  if (recovery.available) {
    const withHQ = Boolean(String(state?.hq_workspace_id || '').trim());
    return {
      mode: 'reconnect',
      title: 'Reconnect your assistant',
      message: withHQ
        ? `I found ${name} and Personal HQ. Their stable IDs agree, so Ori can reconnect the missing relationship.`
        : `I found the existing profile for ${name}. Its durable ownership marker is valid, so Ori can reconnect it before you build Personal HQ.`,
      detail:
        'This restores only the missing relationship record. It does not create an agent or workspace, change permissions, connect an account, or recover a lost working agreement.',
      buttonLabel: 'Reconnect assistant'
    };
  }
  if (recovery.blocked) {
    return {
      mode: 'blocked',
      title: 'Automatic repair unavailable',
      message:
        'Ori found Personal Assistant records that do not agree. Nothing can be reconnected automatically.',
      detail:
        'Ori will not guess from names or choose between conflicting identities. No records have been changed.',
      buttonLabel: ''
    };
  }
  if (relationshipState === 'needs_hire' || relationshipState === 'hiring') {
    return {
      mode: 'form',
      title: 'Hire your personal assistant',
      message: 'This is the one agent that owns your ongoing work.',
      detail: '',
      buttonLabel: 'Hire assistant'
    };
  }
  if (relationshipState === 'repair_needed' && String(state?.hire_request_id || '').trim()) {
    return {
      mode: 'resume',
      title: 'Finish hiring your assistant',
      message: personalAssistantResumeMessage(state),
      detail: 'This replays the same hire. It never creates a second assistant.',
      buttonLabel: 'Finish setup'
    };
  }
  return { mode: 'standard', title: '', message: '', detail: '', buttonLabel: '' };
}

if (typeof window !== 'undefined') {
  window.OriAssistantHire = Object.freeze({
    HQ_QUEST_ROUTE,
    MEET_ASSISTANT_QUEST_ROUTE,
    MEET_ASSISTANT_AGENTS_ROUTE,
    JUST_HIRED_FLAG,
    DEFAULT_ASSISTANT_NAME,
    ASSISTANT_NAME_MAX_LENGTH,
    ASSISTANT_MANDATE_MAX_LENGTH,
    MANDATE_PLACEHOLDER,
    HIRE_BOUNDARY_COPY,
    FOCUS_AREAS: GENERIC_FOCUS_AREAS,
    buildPersonalAssistantHirePayload,
    personalAssistantNeedsHQ,
    personalAssistantRecoveryView,
    getOrCreateHireRequestId,
    clearHireRequestId,
    submitHire,
    submitRepair,
    presetView
  });
}
