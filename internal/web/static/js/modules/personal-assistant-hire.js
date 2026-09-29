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
// Default hand-over after a hire: the assistant proposes HQ in Today.
export const HQ_CARD_ROUTE = '/?panel=today';

// Mission 01's action URL: the walkthrough from its first step, on Home, where
// Ori dims the page around the Agents nav entry for the user to click. Every
// entry that starts the hire resolves here: the Home mission card, Today's
// banner, Ask Ori's hand-off, and the retired /?hire=1 link.
export const MEET_ASSISTANT_QUEST_ROUTE = '/?quest=meet-assistant';

// Where first-run onboarding hands over: Ori's mission briefing in the centre
// of Home, whose Start opens the same first step.
export const MEET_ASSISTANT_BRIEFING_ROUTE = '/?quest=meet-assistant&briefing=1';

// The Agents page leg of the same walkthrough, for an entry that is already on
// its way there: the repair banners (a reconnect or resume view opens straight
// away on arrival), the pointer on /agents/create, and Ask Ori on /agents.
export const MEET_ASSISTANT_AGENTS_ROUTE = '/agents?quest=meet-assistant';

// Set in sessionStorage when the user presses the Agents nav entry from the
// first step, read (and cleared) by the Agents page so its first step keeps the
// same spotlight rather than switching to Ori's panel mid-mission.
export const MEET_ASSISTANT_GUIDED_FLAG = 'ori:meet-assistant-guided';

// Set in sessionStorage the moment a hire succeeds, read (and cleared) by the
// HQ card (or the alternate Map walkthrough) for the first hand-over.
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

// The promise every recovery fix keeps, shown above the button that applies one.
export const RECOVERY_BOUNDARY_COPY =
  'Nothing is deleted. A fix changes only which agent and workspace Ori treats as your assistant and Personal HQ. A reconnected assistant starts paused.';

const RECOVERY_NETWORK_ERROR = 'Ori could not reach the server. Nothing was changed.';

// fetchRecoveryDiagnosis asks why the assistant cannot be reconnected as its
// records stand, and which fixes are safe. It never changes anything.
export async function fetchRecoveryDiagnosis({ fetchImpl = globalThis.fetch } = {}) {
  let response;
  try {
    response = await fetchImpl('/api/personal-assistant/repair/diagnosis', {
      headers: { Accept: 'application/json' }
    });
  } catch (_) {
    return { ok: false, diagnosis: null, error: RECOVERY_NETWORK_ERROR };
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    return {
      ok: false,
      diagnosis: null,
      status: response.status,
      code: String(body?.code || ''),
      error: String(body?.error || '').trim() || 'Ori could not check your assistant records.'
    };
  }
  return { ok: true, diagnosis: body?.diagnosis || null, error: '' };
}

// submitRecoveryFix applies the one fix the user chose. It names only that fix
// and the evidence it was chosen from: the server re-reads the records, refuses
// if they changed, and reconnects the assistant once they agree.
export async function submitRecoveryFix({
  fetchImpl = globalThis.fetch,
  fixId = '',
  digest = ''
} = {}) {
  let response;
  try {
    response = await fetchImpl('/api/personal-assistant/repair/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ fix_id: String(fixId || ''), evidence_digest: String(digest || '') })
    });
  } catch (_) {
    return { ok: false, reconnected: false, diagnosis: null, error: RECOVERY_NETWORK_ERROR };
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    return {
      ok: false,
      reconnected: false,
      diagnosis: null,
      status: response.status,
      code: String(body?.code || ''),
      error:
        String(body?.error || '').trim() || 'The fix could not be applied. Nothing was changed.'
    };
  }
  const state = body?.personal_assistant || null;
  return {
    ok: true,
    reconnected: body?.reconnected === true,
    applied: String(body?.applied || ''),
    state,
    needsHQ: personalAssistantNeedsHQ(state),
    diagnosis: body?.diagnosis || null,
    error: ''
  };
}

function quoted(value, fallback = '') {
  const text = String(value || '').trim();
  return text ? `“${text}”` : fallback;
}

function listOf(items) {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`;
}

function shortId(id) {
  return String(id || '').slice(0, 8);
}

function createdOn(value) {
  const date = value ? new Date(value) : null;
  if (!date || Number.isNaN(date.getTime())) return '';
  return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

function recoveryProblem(issue, { profiles, hqs, designation }) {
  const profile = quoted(profiles[0]?.name, 'your assistant');
  const hq = quoted(hqs[0]?.name, 'Personal HQ');
  const designated = quoted(designation.workspace_name, 'another workspace');
  switch (issue) {
    case 'assistant_mismatch':
      return `${hq} was set up for a different assistant than ${profile}. This happens when an assistant is hired again and the earlier Personal HQ is kept.`;
    case 'profile_missing':
      return `${hq} is a Personal HQ, but no agent is marked as your assistant.`;
    case 'profile_duplicate':
      return `${profiles.length} agents are each marked as your assistant: ${listOf(profiles.map(p => quoted(p.name)))}. Only one can be.`;
    case 'profile_incomplete':
      return `${profile} is marked as your assistant, but the mark is incomplete, so Ori cannot tell which assistant it belongs to.`;
    case 'profile_role':
      return `${profile} is marked as your assistant, but its role is not Orchestrator.`;
    case 'designation_without_hq':
      return `${designated} is set as your Personal HQ, but it has no record of which assistant it belongs to.`;
    case 'hq_duplicate':
      return `${hqs.length} workspaces are each marked as your Personal HQ: ${listOf(hqs.map(h => quoted(h.name)))}. Only one can be.`;
    case 'hq_marker_invalid':
      return `${hq}’s Personal HQ record is incomplete or unreadable.`;
    case 'hq_foreign_owner':
      return `${hq} is marked as a Personal HQ that belongs to another user.`;
    case 'designation_mismatch':
      return designation.workspace_id
        ? `${hq} belongs to your assistant, but Ori treats ${designated} as your Personal HQ.`
        : `${hq} belongs to your assistant, but no workspace is set as your Personal HQ.`;
    case 'entry_mismatch': {
      const leads = (hqs[0]?.entry_agents || []).map(name => quoted(name));
      return leads.length
        ? `${hq} is led by ${listOf(leads)}, not by your assistant ${profile}.`
        : `${hq} has no lead agent, so it cannot be your assistant’s home.`;
    }
    case 'brief_missing':
      return `${hq} has no Daily Brief settings.`;
    case 'brief_mismatch':
      return `${hq}’s Daily Brief settings belong to another user or workspace.`;
    default:
      return 'Personal Assistant records do not agree.';
  }
}

// What to do when no fix is safe to apply automatically.
function recoveryGuidance(issue, { profiles, hqs }) {
  const profile = quoted(profiles[0]?.name, 'your assistant');
  const hq = quoted(hqs[0]?.name, 'Personal HQ');
  switch (issue) {
    case 'profile_role':
      return `Open ${profile} on the Agents page, set its role to Orchestrator, then check again.`;
    case 'entry_mismatch':
      return `Make ${profile} the only lead agent of ${hq}, then check again.`;
    case 'hq_foreign_owner':
    case 'brief_mismatch':
      return 'These records belong to someone else, so Ori will not change them.';
    default:
      return 'Ori will not guess between these records, so nothing was changed. What Ori found above shows what to change by hand; then check again.';
  }
}

function recoveryFixCopy(fix, { profiles, hqs, designation }) {
  const profile = quoted(fix.profile_name, 'your assistant');
  const workspace = quoted(fix.workspace_name, 'this workspace');
  switch (fix.kind) {
    case 'link_hq':
      return {
        label: `Connect ${profile} to ${workspace}`,
        changes: [
          `${workspace}’s Personal HQ record will name ${profile} as its assistant.`,
          `${profile} is reconnected as your assistant.`
        ]
      };
    case 'keep_profile': {
      const others = profiles.filter(p => p.name !== fix.profile_name).map(p => quoted(p.name));
      return {
        label: `Keep ${profile} as your assistant`,
        changes: [
          `${listOf(others)} stop being marked as your assistant and stay as ordinary agents.`
        ]
      };
    }
    case 'keep_hq': {
      const others = hqs.filter(h => h.workspace_id !== fix.workspace_id).map(h => quoted(h.name));
      const changes = [
        `${listOf(others)} stop being marked as a Personal HQ and stay as ordinary workspaces, with everything in them.`
      ];
      if (designation.workspace_id !== fix.workspace_id)
        changes.push(`${workspace} becomes your Personal HQ.`);
      return { label: `Keep ${workspace} as your Personal HQ`, changes };
    }
    case 'designate_hq':
      return {
        label: `Make ${workspace} your Personal HQ`,
        changes: [
          designation.workspace_id
            ? `${workspace} replaces ${quoted(designation.workspace_name, 'the current one')} as your Personal HQ, which stays as an ordinary workspace.`
            : `${workspace} becomes your Personal HQ.`
        ]
      };
    case 'adopt_entry_profile':
      return {
        label: `Make ${profile} your assistant`,
        changes: [
          `${profile}, the lead agent of ${workspace}, is marked as your assistant.`,
          'Its prompt, model and tools stay as they are.'
        ]
      };
    case 'restamp_profile':
      return {
        label: `Repair ${profile}’s assistant mark`,
        changes: [`The incomplete mark is replaced with one naming ${workspace}’s assistant.`]
      };
    case 'create_brief':
      return {
        label: `Create Daily Brief settings for ${workspace}`,
        changes: ['Default settings are saved with the schedule off. You can set a time later.']
      };
    case 'clear_designation':
      return {
        label: `Stop treating ${workspace} as your Personal HQ`,
        changes: [
          `${workspace} stays as an ordinary workspace, with everything in it.`,
          'You can build or choose a Personal HQ again afterwards.'
        ]
      };
    default:
      return { label: 'Apply this fix', changes: [] };
  }
}

// describeRecoveryDiagnosis turns a server diagnosis into what the fix view
// shows: the one thing that does not match, what Ori found, the fixes (each
// with exactly what it changes), and what to do when no fix is safe.
export function describeRecoveryDiagnosis(diagnosis) {
  const issue = String(diagnosis?.issue || '');
  const facts = {
    profiles: Array.isArray(diagnosis?.profiles) ? diagnosis.profiles : [],
    hqs: Array.isArray(diagnosis?.hqs) ? diagnosis.hqs : [],
    designation: diagnosis?.designation || {}
  };
  // Assistant IDs are only worth showing when two records disagree about one.
  const showIds = ['assistant_mismatch', 'profile_duplicate', 'hq_duplicate'].includes(issue);
  const found = [];
  facts.profiles.forEach(profile => {
    const id =
      showIds && profile.assistant_id ? ` (assistant ${shortId(profile.assistant_id)})` : '';
    const created = createdOn(profile.created_at);
    let line = `Agent ${quoted(profile.name)} is marked as your assistant${id}${created ? `, created ${created}` : ''}.`;
    if (!profile.marker_valid) line += ' Its mark is incomplete.';
    if (!profile.orchestrator) line += ' Its role is not Orchestrator.';
    found.push(line);
  });
  facts.hqs.forEach(hq => {
    const id = showIds && hq.assistant_id ? ` for assistant ${shortId(hq.assistant_id)}` : '';
    const leads = (hq.entry_agents || []).map(name => quoted(name));
    let line = `Workspace ${quoted(hq.name, 'with no name')} is marked as a Personal HQ${id}, ${leads.length ? `led by ${listOf(leads)}` : 'with no lead agent'}.`;
    if (hq.designated) line += ' It is your current Personal HQ.';
    if (!hq.marker_valid) line += ' Its record is incomplete.';
    if (!hq.owned_by_user) line += ' It belongs to another user.';
    found.push(line);
  });
  if (!facts.hqs.some(hq => hq.designated)) {
    const designation = facts.designation;
    if (!designation.workspace_id) found.push('No workspace is set as your Personal HQ.');
    else if (designation.valid)
      found.push(
        `Ori treats ${quoted(designation.workspace_name, 'a workspace')} as your Personal HQ.`
      );
    else
      found.push(
        'Ori’s Personal HQ setting points to a workspace that is missing or in the Trash.'
      );
  }
  const fixes = (Array.isArray(diagnosis?.fixes) ? diagnosis.fixes : []).map(fix => ({
    id: String(fix.id || ''),
    recommended: fix.recommended === true,
    ...recoveryFixCopy(fix, facts)
  }));
  return {
    issue,
    problem: recoveryProblem(issue, facts),
    found,
    fixes,
    guidance: fixes.length ? '' : recoveryGuidance(issue, facts)
  };
}

// presetView says what the New Agent panel shows for a relationship state.
//
//   form       the hire preset (not hired yet, or a hire in flight)
//   resume     a partial hire: one button that replays the same request
//   reconnect  an orphan identity the server can prove: one Reconnect button
//   blocked    contradictory records: the fix view, which asks the server what
//              differs and offers only the fixes it names
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
      title: 'Fix your assistant’s records',
      message:
        'Ori found Personal Assistant records that do not agree, so it cannot reconnect them on its own.',
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
    HQ_CARD_ROUTE,
    MEET_ASSISTANT_QUEST_ROUTE,
    MEET_ASSISTANT_BRIEFING_ROUTE,
    MEET_ASSISTANT_AGENTS_ROUTE,
    MEET_ASSISTANT_GUIDED_FLAG,
    JUST_HIRED_FLAG,
    DEFAULT_ASSISTANT_NAME,
    ASSISTANT_NAME_MAX_LENGTH,
    ASSISTANT_MANDATE_MAX_LENGTH,
    MANDATE_PLACEHOLDER,
    HIRE_BOUNDARY_COPY,
    RECOVERY_BOUNDARY_COPY,
    FOCUS_AREAS: GENERIC_FOCUS_AREAS,
    buildPersonalAssistantHirePayload,
    personalAssistantNeedsHQ,
    personalAssistantRecoveryView,
    getOrCreateHireRequestId,
    clearHireRequestId,
    submitHire,
    submitRepair,
    fetchRecoveryDiagnosis,
    submitRecoveryFix,
    describeRecoveryDiagnosis,
    presetView
  });
}
