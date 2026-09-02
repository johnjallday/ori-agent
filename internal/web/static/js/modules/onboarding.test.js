import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  GENERIC_ASSIGNMENT_LABELS,
  GENERIC_ASSIGNMENT_STEPS,
  GENERIC_FOCUS_AREAS,
  OnboardingManager,
  assignmentLabelsFor,
  assignmentStepsFor,
  buildFirstAssignmentApplyPayload,
  buildFirstAssignmentPreviewPayload,
  buildPersonalAssistantHirePayload,
  canSubmitFirstAssignment,
  firstAssignmentResultView,
  firstAssignmentResumeView,
  HQ_QUEST_ROUTE,
  normalizeFirstAssignmentRows,
  personalAssistantNeedsHQ,
  personalAssistantResumeMessage,
  specialistFocusOptions,
  specialistOfferView,
  workspaceRootSetupView
} from './onboarding.js';
import { resetOnboardingGateForTests } from './onboarding-gate.js';

test('workspaceRootSetupView presents the default path as an unconfirmed suggestion', () => {
  const view = workspaceRootSetupView({
    workspace_root: '',
    effective_workspace_root: '',
    default_workspace_root: '/Users/test/Ori Workspaces',
    source: 'unconfirmed',
    confirmed: false
  });

  assert.equal(view.path, '/Users/test/Ori Workspaces');
  assert.equal(view.confirmed, false);
  assert.match(view.status, /will not scan/i);
});

test('workspaceRootSetupView prefers a confirmed custom path', () => {
  const view = workspaceRootSetupView({
    workspace_root: '/Volumes/Work/Ori',
    effective_workspace_root: '/Volumes/Work/Ori',
    default_workspace_root: '/Users/test/Ori Workspaces',
    source: 'settings',
    confirmed: true
  });

  assert.equal(view.path, '/Volumes/Work/Ori');
  assert.equal(view.confirmed, true);
  assert.match(view.status, /scan only this directory/i);
});

test('workspaceRootSetupView treats an operator WORKSPACE_DIR as confirmed', () => {
  const view = workspaceRootSetupView({
    effective_workspace_root: '/srv/ori-workspaces',
    source: 'environment',
    confirmed: true
  });

  assert.equal(view.path, '/srv/ori-workspaces');
  assert.equal(view.confirmed, true);
});

test('personalAssistantResumeMessage names durable assistant and HQ without claiming total failure', () => {
  assert.equal(
    personalAssistantResumeMessage({
      display_name: 'Atlas',
      assistant_id: 'assistant-1',
      hq_workspace_id: 'hq-1'
    }),
    'Atlas and Personal HQ are already saved. Retry to finish the remaining setup step.'
  );
  assert.match(personalAssistantResumeMessage({ hq_workspace_id: 'hq-1' }), /already saved/);
});

test('buildPersonalAssistantHirePayload normalizes one bounded confirmed hire', () => {
  const payload = buildPersonalAssistantHirePayload({
    requestId: ' request-1 ',
    ifVersion: 3,
    displayName: ' Assistant ',
    appearance: { mode: 'generated', generated: { color: '#225588' } },
    mandate: ' Keep today realistic. ',
    focusAreas: ['plan_my_day', 'plan_my_day', 'keep_projects_moving']
  });

  assert.deepEqual(payload, {
    request_id: 'request-1',
    if_version: 3,
    display_name: 'Assistant',
    appearance: { mode: 'generated', generated: { color: '#225588' } },
    mandate: 'Keep today realistic.',
    focus_areas: ['plan_my_day', 'keep_projects_moving']
  });
});

test('buildPersonalAssistantHirePayload carries no Daily Brief rhythm', () => {
  // The rhythm moved to the Map's Build My HQ form, where a real workspace ID
  // exists to write it against. Hiring must not collect or promise it.
  const payload = buildPersonalAssistantHirePayload({
    requestId: 'request-1',
    displayName: 'Atlas',
    focusAreas: ['plan_my_day'],
    timezone: 'America/New_York',
    scheduleDays: ['mon'],
    scheduleTime: '08:00',
    notifyOnReady: true
  });

  for (const key of ['timezone', 'schedule_days', 'schedule_time', 'notify_on_ready']) {
    assert.equal(key in payload, false, `hire payload still carries ${key}`);
  }
});

test('personalAssistantNeedsHQ recognizes the hired-but-unbuilt stages only', () => {
  assert.equal(personalAssistantNeedsHQ({ state: 'needs_hq' }), true);
  assert.equal(personalAssistantNeedsHQ({ state: 'provisioning_hq' }), true);
  for (const state of ['needs_hire', 'hiring', 'active', 'paused', 'repair_needed', '']) {
    assert.equal(personalAssistantNeedsHQ({ state }), false, `${state} misread as pre-HQ`);
  }
  assert.equal(personalAssistantNeedsHQ(), false);
});

test('the guided HQ quest route lets the user select the site themselves', () => {
  // A focus parameter would preselect the landmark. The quest highlights it and
  // waits for a real user selection instead.
  assert.equal(HQ_QUEST_ROUTE, '/?quest=build-hq');
});

test('first assignment row normalization keeps explicit categories and honest empty input', () => {
  assert.deepEqual(
    normalizeFirstAssignmentRows([
      { type: ' PRIORITY ', title: ' Ship draft ', due: '2026-10-20' },
      { type: 'i_owe', title: ' ' },
      { type: 'unknown', title: 'Never infer this' },
      { type: 'waiting_on', title: ' Reply ', counterparty: ' Maya ' }
    ]),
    [
      {
        type: 'priority',
        title: 'Ship draft',
        action: '',
        detail: '',
        counterparty: '',
        due: '2026-10-20'
      },
      {
        type: 'waiting_on',
        title: 'Reply',
        action: '',
        detail: '',
        counterparty: 'Maya',
        due: ''
      }
    ]
  );
  assert.deepEqual(buildFirstAssignmentPreviewPayload(4, []), { if_version: 4, rows: [] });
});

test('edited first assignment requires a replacement preview identity and version', () => {
  const oldPreview = { preview_id: 'old', assignment_version: 1, payload_hash: 'old-hash' };
  const replacement = { preview_id: 'new', assignment_version: 2, payload_hash: 'new-hash' };
  assert.notEqual(replacement.preview_id, oldPreview.preview_id);
  assert.ok(replacement.assignment_version > oldPreview.assignment_version);
  assert.deepEqual(
    buildFirstAssignmentApplyPayload({
      preview: replacement,
      stateVersion: 7,
      applyRequestId: ' apply-1 '
    }),
    {
      preview_id: 'new',
      preview_version: 2,
      payload_hash: 'new-hash',
      if_version: 7,
      apply_request_id: 'apply-1'
    }
  );
});

test('first assignment submit requires confirmation and suppresses a double click', () => {
  const preview = { preview_id: 'preview-1' };
  assert.equal(canSubmitFirstAssignment({ confirmed: false, inFlight: false, preview }), false);
  assert.equal(canSubmitFirstAssignment({ confirmed: true, inFlight: true, preview }), false);
  assert.equal(canSubmitFirstAssignment({ confirmed: true, inFlight: false, preview }), true);
});

test('first assignment reload resumes preview and partial apply from durable IDs', () => {
  const preview = { preview_id: 'preview-1', count: 2 };
  assert.deepEqual(firstAssignmentResumeView({ state_version: 3, preview }).stage, 'preview');
  const partial = firstAssignmentResumeView({
    state_version: 4,
    status: 'applying',
    preview,
    apply_request_id: 'apply-1'
  });
  assert.equal(partial.stage, 'partial');
  assert.equal(partial.applyRequestId, 'apply-1');
  assert.equal(partial.preview, preview);
});

test('first assignment result distinguishes honest empty and partial brief states', () => {
  const empty = firstAssignmentResultView({ outcome: 'complete_empty', total_count: 0, brief: {} });
  assert.equal(empty.complete, true);
  assert.equal(empty.empty, true);
  assert.match(empty.summary, /Nothing was created/);

  const partial = firstAssignmentResultView({
    outcome: 'records_saved_brief_failed',
    applied_count: 2,
    total_count: 2,
    retryable: true,
    brief: { status: 'failed', top_items: [] }
  });
  assert.equal(partial.partialBrief, true);
  assert.equal(partial.retryable, true);
});

test('OnboardingManager opens the first quest for an active incomplete relationship', () => {
  const priorWindow = globalThis.window;
  globalThis.window = { location: { search: '?quest=plan-first-day' } };
  try {
    const manager = new OnboardingManager();
    manager.personalAssistantState = {
      state: 'active',
      first_assignment_status: 'not_started'
    };
    assert.equal(manager.shouldOpenFirstAssignmentQuest(), true);

    manager.personalAssistantState.first_assignment_status = 'completed';
    assert.equal(manager.shouldOpenFirstAssignmentQuest(), false);
  } finally {
    globalThis.window = priorWindow;
  }
});

test('?quest=plan-first-day never opens while a hired assistant has no HQ', () => {
  const priorWindow = globalThis.window;
  globalThis.window = { location: { search: '?quest=plan-first-day' } };
  try {
    const manager = new OnboardingManager();
    for (const state of ['needs_hq', 'provisioning_hq', 'needs_hire', 'hiring', 'repair_needed']) {
      manager.personalAssistantState = { state, first_assignment_status: 'not_started' };
      assert.equal(
        manager.shouldOpenFirstAssignmentQuest(),
        false,
        `${state} incorrectly opened the first-day quest`
      );
    }
  } finally {
    globalThis.window = priorWindow;
  }
});

// stubHireDom gives the OnboardingManager just enough DOM to run the hire and
// onboarding-completion paths without a browser.
function stubHireDom() {
  const elements = new Map();
  const make = () => ({
    textContent: '',
    innerHTML: '',
    disabled: false,
    checked: false,
    value: '',
    classList: { toggle() {}, add() {}, remove() {} },
    setAttribute() {},
    removeAttribute() {},
    focus() {}
  });
  for (const id of ['pafHireBtn', 'pafHireStatus', 'pafHireError']) {
    elements.set(id, make());
  }
  const priorDocument = globalThis.document;
  const priorWindow = globalThis.window;
  const navigations = [];
  globalThis.document = {
    getElementById: id => elements.get(id) || null,
    querySelectorAll: () => [],
    querySelector: () => null
  };
  globalThis.window = {
    location: {
      search: '',
      set href(value) {
        navigations.push(value);
      },
      get href() {
        return navigations.at(-1) || '';
      }
    },
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} }
  };
  return {
    elements,
    navigations,
    restore() {
      globalThis.document = priorDocument;
      globalThis.window = priorWindow;
    }
  };
}

test('a durable needs_hq relationship closes onboarding instead of hiring again', async () => {
  const dom = stubHireDom();
  let hirePosts = 0;
  let completePosts = 0;
  const priorFetch = globalThis.fetch;
  globalThis.fetch = async url => {
    if (String(url).includes('/api/personal-assistant/hire')) hirePosts += 1;
    if (String(url).includes('/api/onboarding/complete')) completePosts += 1;
    return { ok: true, json: async () => ({}) };
  };
  try {
    const manager = new OnboardingManager();
    manager.personalAssistantState = { state: 'needs_hq', display_name: 'Atlas' };
    manager.completeStep = async () => {};
    await manager.hireAssistant();

    assert.equal(hirePosts, 0, 'a second hire was posted for a durable relationship');
    assert.equal(completePosts, 1);
    assert.equal(dom.navigations.at(-1), HQ_QUEST_ROUTE);
  } finally {
    globalThis.fetch = priorFetch;
    dom.restore();
  }
});

test('onboarding completion failure after hire offers Continue to HQ quest', async () => {
  const dom = stubHireDom();
  let hirePosts = 0;
  let statePolls = 0;
  const priorFetch = globalThis.fetch;
  globalThis.fetch = async url => {
    const target = String(url);
    if (target.includes('/api/personal-assistant/hire')) {
      hirePosts += 1;
      return {
        ok: true,
        json: async () => ({
          personal_assistant: {
            state: 'needs_hq',
            display_name: 'Atlas',
            assistant_id: 'assistant-1',
            state_version: 2
          }
        })
      };
    }
    if (target.includes('/api/onboarding/complete')) {
      return { ok: false, json: async () => ({}) };
    }
    if (target.includes('/api/personal-assistant')) {
      statePolls += 1;
      return {
        ok: true,
        json: async () => ({
          personal_assistant: { state: 'needs_hq', display_name: 'Atlas', state_version: 2 }
        })
      };
    }
    return { ok: true, json: async () => ({}) };
  };
  try {
    const manager = new OnboardingManager();
    manager.personalAssistantState = { state: 'needs_hire', state_version: 0 };
    manager.completeStep = async () => {};
    manager.personalAssistantHirePayload = () => ({ request_id: 'request-1' });
    await manager.hireAssistant();

    assert.equal(hirePosts, 1, 'the hire should be posted exactly once');
    assert.ok(statePolls >= 1, 'authoritative state should be reloaded before recovery');
    // The recovery action continues the quest; it never re-runs the hire.
    assert.equal(dom.elements.get('pafHireBtn').textContent, 'Continue to HQ quest');
    assert.equal(dom.elements.get('pafHireBtn').disabled, false);
    assert.match(dom.elements.get('pafHireError').textContent, /not hire a second assistant/i);
    assert.equal(dom.navigations.length, 0, 'a failed completion must not navigate away');
  } finally {
    globalThis.fetch = priorFetch;
    dom.restore();
  }
});

test('OnboardingManager consumes the shared memoized status gate', async () => {
  let calls = 0;
  resetOnboardingGateForTests(async () => {
    calls += 1;
    return {
      ok: true,
      json: async () => ({ needs_onboarding: true, assistant_name: 'Ori' })
    };
  });
  const manager = new OnboardingManager();
  const first = await manager.checkOnboardingStatus();
  const second = await manager.checkOnboardingStatus();
  assert.equal(first.needs_onboarding, true);
  assert.equal(second, first);
  assert.equal(calls, 1);
});

// --- Domain specialist offer -------------------------------------------

const musicEntry = Object.freeze({
  slug: 'music_production',
  display_name: 'music projects',
  offer_copy: {
    headline: 'I found REAPER on this Mac.',
    question: 'Want me to help with your music projects?',
    accept_label: 'Yes, help with my music',
    decline_label: 'No thanks',
    accepted_note: 'Your assistant will keep an eye on your music projects.',
    manual_label: 'I work on music'
  },
  focus_areas: [
    { value: 'plan_my_day', label: 'Plan my studio day', selected: true },
    { value: 'track_songs_in_progress', label: 'Track songs in progress', selected: true }
  ],
  assignment_labels: [
    {
      type: 'priority',
      label: 'Song or project in progress',
      placeholder: 'Which track are you on?',
      add_label: 'Add a song or project'
    }
  ],
  assignment_steps: [{ index: 0, title: 'Songs in progress', legend: 'What are you on now?' }]
});

test('specialistOfferView offers help rather than asking whether the app is used', () => {
  const view = specialistOfferView(musicEntry);
  assert.equal(view.visible, true);
  assert.equal(view.decision, 'unanswered');
  assert.equal(view.showActions, true);
  assert.equal(view.headline, 'I found REAPER on this Mac.');
  assert.equal(view.question, 'Want me to help with your music projects?');
  // The install is already known; the copy must never put it to the user as a
  // question of fact.
  assert.doesNotMatch(view.question, /do you use|are you a/i);
});

test('specialistOfferView keeps a declined offer from ever coming back', () => {
  const declined = specialistOfferView(musicEntry, 'declined');
  assert.equal(declined.visible, false);
  assert.equal(declined.showActions, false);
});

test('specialistOfferView confirms acceptance without implying it can direct the specialist', () => {
  const accepted = specialistOfferView(musicEntry, 'accepted');
  assert.equal(accepted.visible, true);
  assert.equal(accepted.decision, 'accepted');
  assert.equal(accepted.showActions, false);
  assert.equal(accepted.acceptedNote, 'Your assistant will keep an eye on your music projects.');
});

test('specialistOfferView renders nothing when no specialist matched', () => {
  for (const entry of [null, undefined, {}, { slug: '' }]) {
    const view = specialistOfferView(entry);
    assert.equal(view.visible, false, `expected no offer for ${JSON.stringify(entry)}`);
    assert.equal(view.showActions, false);
  }
});

// PRD FR 32: a user with no detected specialist must see today's flow. These
// three assertions are the regression guard — focus areas, assignment labels,
// and step wording must all resolve to the shipped generic values.
test('the generic path is byte-for-byte unchanged when no specialist matches', () => {
  assert.deepEqual(
    specialistFocusOptions(null),
    GENERIC_FOCUS_AREAS.map(option => ({ ...option }))
  );
  assert.deepEqual(
    assignmentLabelsFor(null),
    GENERIC_ASSIGNMENT_LABELS.map(label => ({ ...label }))
  );
  assert.deepEqual(
    assignmentStepsFor(null),
    GENERIC_ASSIGNMENT_STEPS.map(step => ({ ...step }))
  );

  // The exact shipped strings, spelled out so a silent copy edit fails here.
  assert.deepEqual(
    GENERIC_FOCUS_AREAS.map(option => option.value),
    [
      'plan_my_day',
      'track_commitments_and_follow_ups',
      'prepare_for_meetings',
      'keep_projects_moving',
      'help_with_email',
      'something_else'
    ]
  );
  assert.deepEqual(
    GENERIC_ASSIGNMENT_LABELS.map(label => label.label),
    ['Priority', 'I owe', 'Waiting on', 'Commitment']
  );
  assert.deepEqual(
    GENERIC_ASSIGNMENT_STEPS.map(step => step.title),
    ['Today’s priorities', 'Owed and waiting', 'Fixed commitments']
  );
});

test('specialistFocusOptions uses the domain options and falls back to the generic six', () => {
  assert.deepEqual(specialistFocusOptions(musicEntry), [
    { value: 'plan_my_day', label: 'Plan my studio day', selected: true },
    { value: 'track_songs_in_progress', label: 'Track songs in progress', selected: true }
  ]);
  // A mapping row with unusable focus areas must not empty the step.
  assert.deepEqual(
    specialistFocusOptions({ slug: 'x', focus_areas: [{ value: '', label: '' }] }),
    GENERIC_FOCUS_AREAS.map(option => ({ ...option }))
  );
});

test('assignment labels are re-worded while the durable item types are not', () => {
  const labels = assignmentLabelsFor(musicEntry);
  assert.deepEqual(
    labels.map(label => label.type),
    GENERIC_ASSIGNMENT_LABELS.map(label => label.type)
  );
  assert.equal(labels[0].label, 'Song or project in progress');
  assert.equal(labels[0].placeholder, 'Which track are you on?');
  // Types the domain did not override keep the generic wording.
  assert.equal(labels[1].label, 'I owe');
  assert.equal(labels[3].placeholder, 'Commitment or time to keep visible');

  const steps = assignmentStepsFor(musicEntry);
  assert.equal(steps[0].title, 'Songs in progress');
  assert.equal(steps[1].title, 'Owed and waiting');
});

// The hire itself carries no specialist: the offer is answered after hiring,
// on Home, against the durable relationship.
test('the hire payload never carries a specialist', () => {
  const payload = buildPersonalAssistantHirePayload({ requestId: 'r' });
  assert.equal('specialist_slug' in payload, false);
});

test('only an accepted offer counts as the active specialist', () => {
  const manager = new OnboardingManager();
  assert.equal(manager.activeSpecialist(), null);

  manager.specialistOffer = musicEntry;
  // A pending offer is not an answer.
  assert.equal(manager.activeSpecialist(), null);

  manager.specialistDecision = 'accepted';
  assert.equal(manager.activeSpecialist().slug, 'music_production');

  manager.specialistDecision = 'declined';
  assert.equal(manager.activeSpecialist(), null);
});

test('declining resolves assignment copy back to the generic labels', () => {
  const manager = new OnboardingManager();
  manager.applySpecialistAssignmentCopy(musicEntry);
  assert.equal(manager.assignmentLabelFor('priority').label, 'Song or project in progress');

  manager.applySpecialistAssignmentCopy(null);
  assert.equal(manager.assignmentLabelFor('priority').label, 'Priority');
  assert.deepEqual(
    manager.assignmentLabels,
    GENERIC_ASSIGNMENT_LABELS.map(label => ({ ...label }))
  );
});

test('detection failure, an empty scan, and no match all resolve to no offer', async () => {
  const priorFetch = globalThis.fetch;
  const cases = [
    { name: 'network error', impl: async () => Promise.reject(new Error('offline')) },
    {
      name: 'server error',
      impl: async () => ({ ok: false, status: 500, json: async () => ({}) })
    },
    {
      name: 'empty scan',
      impl: async () => ({ ok: true, json: async () => ({ success: true, apps: [] }) })
    },
    {
      name: 'no match',
      impl: async () => ({
        ok: true,
        json: async () => ({ success: true, apps: [{ name: 'Safari' }] })
      })
    },
    {
      name: 'malformed specialist',
      impl: async () => ({ ok: true, json: async () => ({ specialist: { slug: '' } }) })
    }
  ];
  try {
    for (const testCase of cases) {
      globalThis.fetch = testCase.impl;
      const manager = new OnboardingManager();
      assert.equal(await manager.detectSpecialist(), null, testCase.name);
      assert.equal(specialistOfferView(await manager.detectSpecialist()).visible, false);
    }
  } finally {
    globalThis.fetch = priorFetch;
  }
});

test('a slow scan never blocks the wizard and still renders when it lands', async () => {
  const priorFetch = globalThis.fetch;
  const priorDocument = globalThis.document;
  let releaseScan = null;
  globalThis.fetch = async url => {
    if (String(url).includes('/specialists')) {
      return { ok: true, json: async () => ({ specialists: [musicEntry] }) };
    }
    await new Promise(resolve => {
      releaseScan = resolve;
    });
    return { ok: true, json: async () => ({ specialist: musicEntry }) };
  };
  // The wizard is DOM-driven; render is a no-op without a document, which is
  // exactly the "nothing on screen yet" state this test is about.
  globalThis.document = { getElementById: () => null, querySelector: () => null };
  try {
    const manager = new OnboardingManager();
    const detection = manager.startSpecialistDetection();
    // The step is reachable and answerable while the scan is outstanding.
    manager.hireStep = 1;
    assert.equal(manager.specialistOffer, null);
    assert.equal(specialistOfferView(manager.specialistOffer).visible, false);
    assert.equal(manager.activeSpecialist(), null);

    releaseScan();
    const offer = await detection;
    assert.equal(offer.slug, 'music_production');
    assert.equal(manager.specialistDecision, 'unanswered');
  } finally {
    globalThis.fetch = priorFetch;
    globalThis.document = priorDocument;
  }
});

test('the first-assignment quest recovers the domain from the persisted slug', async () => {
  const priorFetch = globalThis.fetch;
  let catalogReads = 0;
  globalThis.fetch = async url => {
    if (!String(url).includes('/specialists')) throw new Error(`unexpected fetch ${url}`);
    catalogReads += 1;
    return { ok: true, json: async () => ({ specialists: [musicEntry] }) };
  };
  try {
    // The quest is normally opened from Home, in a session where no detection
    // ran at all. The wording has to come from the slug on the relationship.
    const manager = new OnboardingManager();
    manager.personalAssistantState = { state: 'active', specialist_slug: 'music_production' };
    const resolved = await manager.resolvePersistedSpecialist();
    assert.equal(resolved.slug, 'music_production');
    assert.equal(catalogReads, 1);

    // The catalog is read once, then reused.
    await manager.resolvePersistedSpecialist();
    assert.equal(catalogReads, 1);

    // A relationship with no specialist stays on the generic wording, and does
    // not even read the catalog.
    const generic = new OnboardingManager();
    generic.personalAssistantState = { state: 'active' };
    assert.equal(await generic.resolvePersistedSpecialist(), null);
    assert.equal(catalogReads, 1);

    // A persisted slug the mapping no longer knows degrades to generic rather
    // than breaking the quest.
    const stale = new OnboardingManager();
    stale.personalAssistantState = { state: 'active', specialist_slug: 'retired_domain' };
    assert.equal(await stale.resolvePersistedSpecialist(), null);
  } finally {
    globalThis.fetch = priorFetch;
  }
});

test('a scan landing after the user answered never overrides their answer', async () => {
  const priorFetch = globalThis.fetch;
  const priorDocument = globalThis.document;
  globalThis.fetch = async url =>
    String(url).includes('/specialists')
      ? { ok: true, json: async () => ({ specialists: [] }) }
      : { ok: true, json: async () => ({ specialist: musicEntry }) };
  globalThis.document = { getElementById: () => null, querySelector: () => null };
  try {
    const manager = new OnboardingManager();
    manager.specialistDecision = 'declined';
    await manager.startSpecialistDetection();
    assert.equal(manager.specialistOffer, null);
    assert.equal(manager.activeSpecialist(), null);
  } finally {
    globalThis.fetch = priorFetch;
    globalThis.document = priorDocument;
  }
});
