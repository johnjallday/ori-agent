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
  normalizeFirstAssignmentRows,
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
    focusAreas: ['plan_my_day', 'plan_my_day', 'keep_projects_moving'],
    timezone: ' America/New_York ',
    scheduleDays: ['MON', 'tue', 'mon'],
    scheduleTime: '08:00',
    notifyOnReady: false
  });

  assert.deepEqual(payload, {
    request_id: 'request-1',
    if_version: 3,
    display_name: 'Assistant',
    appearance: { mode: 'generated', generated: { color: '#225588' } },
    mandate: 'Keep today realistic.',
    focus_areas: ['plan_my_day', 'keep_projects_moving'],
    timezone: 'America/New_York',
    schedule_days: ['mon', 'tue'],
    schedule_time: '08:00',
    notify_on_ready: false,
    specialist_slug: ''
  });
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

test('the hire payload carries the accepted slug and nothing when unanswered', () => {
  const manager = new OnboardingManager();
  assert.equal(manager.activeSpecialist(), null);

  manager.specialistOffer = musicEntry;
  // A pending offer is not an answer.
  assert.equal(manager.activeSpecialist(), null);

  manager.specialistDecision = 'accepted';
  assert.equal(manager.activeSpecialist().slug, 'music_production');

  manager.specialistDecision = 'declined';
  assert.equal(manager.activeSpecialist(), null);

  assert.equal(buildPersonalAssistantHirePayload({ requestId: 'r' }).specialist_slug, '');
  assert.equal(
    buildPersonalAssistantHirePayload({ requestId: 'r', specialistSlug: 'music_production' })
      .specialist_slug,
    'music_production'
  );
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
    { name: 'server error', impl: async () => ({ ok: false, status: 500, json: async () => ({}) }) },
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
