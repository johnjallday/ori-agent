import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  GENERIC_ASSIGNMENT_LABELS,
  GENERIC_ASSIGNMENT_STEPS,
  OnboardingManager,
  assignmentLabelsFor,
  assignmentStepsFor,
  buildFirstAssignmentApplyPayload,
  buildFirstAssignmentPreviewPayload,
  canSubmitFirstAssignment,
  firstAssignmentResultView,
  firstAssignmentResumeView,
  normalizeFirstAssignmentRows,
  workspaceRootSetupView
} from './onboarding.js';
import {
  GENERIC_FOCUS_AREAS,
  buildPersonalAssistantHirePayload
} from './personal-assistant-hire.js';
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

test('Do this later on the first-day plan defers Mission 03, Connect one source', async () => {
  const priorWindow = globalThis.window;
  const priorDocument = globalThis.document;
  const priorFetch = globalThis.fetch;
  const posts = [];
  const events = [];
  globalThis.document = { getElementById: () => null };
  globalThis.window = {
    location: { search: '?quest=plan-first-day' },
    history: { replaceState() {} },
    dispatchEvent: event => events.push(event.type)
  };
  globalThis.fetch = async (url, options = {}) => {
    posts.push({ url: String(url), body: JSON.parse(options.body || '{}') });
    return { ok: true, json: async () => ({}) };
  };
  try {
    const manager = new OnboardingManager();
    manager.assignmentQuestMode = true;
    manager.modalInstance = { hide() {} };
    manager.showAssignmentError = () => {};
    await manager.deferFirstAssignmentQuest();

    assert.deepEqual(posts, [
      { url: '/api/progression/skip', body: { quest_id: 'pa-connect-source' } }
    ]);
    assert.deepEqual(events, ['ori:progression-refresh']);
    assert.equal(manager.assignmentQuestMode, false);
  } finally {
    globalThis.window = priorWindow;
    globalThis.document = priorDocument;
    globalThis.fetch = priorFetch;
  }
});

// stubModalDom gives the OnboardingManager just enough DOM to run the modal's
// phase changes and its completion path without a browser.
function stubModalDom({ search = '' } = {}) {
  const elements = new Map();
  const make = () => ({
    textContent: '',
    innerHTML: '',
    disabled: false,
    checked: false,
    value: '',
    style: {},
    parentElement: { setAttribute() {} },
    classList: {
      _set: new Set(),
      toggle() {},
      add(c) {
        this._set.add(c);
      },
      remove(c) {
        this._set.delete(c);
      },
      contains(c) {
        return this._set.has(c);
      }
    },
    setAttribute() {},
    removeAttribute() {},
    focus() {}
  });
  for (const id of [
    'onboardingStepLabel',
    'onboardingProgressBar',
    'onboardingModelError',
    'onboarding-phase-0',
    'onboarding-phase-1',
    'onboarding-phase-2'
  ]) {
    elements.set(id, make());
  }
  const priorDocument = globalThis.document;
  const priorWindow = globalThis.window;
  const navigations = [];
  const replacements = [];
  globalThis.document = {
    getElementById: id => elements.get(id) || null,
    querySelectorAll: () => [],
    querySelector: () => null
  };
  globalThis.window = {
    location: {
      search,
      set href(value) {
        navigations.push(value);
      },
      get href() {
        return navigations.at(-1) || '';
      },
      replace(value) {
        replacements.push(value);
      }
    },
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} }
  };
  return {
    elements,
    navigations,
    replacements,
    restore() {
      globalThis.document = priorDocument;
      globalThis.window = priorWindow;
    }
  };
}

// Onboarding is two phases now. Leaving Model closes it: no hire is ever sent
// from the modal, and the browser goes Home to Ori's Mission 01 briefing (PRD
// FR2).
test('leaving the Model phase completes onboarding and hands over to the Mission 01 briefing', async () => {
  for (const skipModel of [true, false]) {
    const dom = stubModalDom();
    const requests = [];
    const priorFetch = globalThis.fetch;
    globalThis.fetch = async url => {
      requests.push(String(url));
      return { ok: true, json: async () => ({}) };
    };
    try {
      const manager = new OnboardingManager();
      let hidden = 0;
      manager.modalInstance = { hide: () => (hidden += 1) };
      manager.saveSystemModel = async () => true;
      await manager.advanceFromModel({ skipModel });

      assert.deepEqual(requests, ['/api/onboarding/step', '/api/onboarding/complete']);
      assert.equal(
        requests.some(url => url.includes('/api/personal-assistant')),
        false,
        'the modal sent a hire request'
      );
      assert.equal(hidden, 1);
      assert.deepEqual(dom.navigations, ['/?quest=meet-assistant&briefing=1']);
      assert.equal(manager.modelConfigured, !skipModel);
    } finally {
      globalThis.fetch = priorFetch;
      dom.restore();
    }
  }
});

test('a failed completion keeps the modal open on Model and says so', async () => {
  const dom = stubModalDom();
  const priorFetch = globalThis.fetch;
  globalThis.fetch = async url => ({
    ok: !String(url).includes('/api/onboarding/complete'),
    status: String(url).includes('/api/onboarding/complete') ? 500 : 200,
    json: async () => ({})
  });
  try {
    const manager = new OnboardingManager();
    let hidden = 0;
    manager.modalInstance = { hide: () => (hidden += 1) };
    await manager.advanceFromModel({ skipModel: true });

    assert.equal(hidden, 0);
    assert.deepEqual(dom.navigations, []);
    assert.match(dom.elements.get('onboardingModelError').textContent, /could not finish/i);
  } finally {
    globalThis.fetch = priorFetch;
    dom.restore();
  }
});

test('the progress shell counts two onboarding phases', () => {
  const dom = stubModalDom();
  try {
    const manager = new OnboardingManager();
    manager.updateProgress(0);
    assert.equal(dom.elements.get('onboardingStepLabel').textContent, 'Step 1 of 2');
    assert.equal(dom.elements.get('onboardingProgressBar').style.width, '50%');
    manager.updateProgress(1);
    assert.equal(dom.elements.get('onboardingStepLabel').textContent, 'Step 2 of 2');
    assert.equal(dom.elements.get('onboardingProgressBar').style.width, '100%');
  } finally {
    dom.restore();
  }
});

// /?hire=1 opened the retired wizard. Old links land where the hire happens now.
test('an old /?hire=1 link goes to Mission 01’s first step', async () => {
  const dom = stubModalDom({ search: '?hire=1' });
  const priorFetch = globalThis.fetch;
  const priorBootstrap = globalThis.bootstrap;
  globalThis.bootstrap = { Modal: function Modal() {} };
  globalThis.fetch = async () => ({ ok: true, json: async () => ({}) });
  try {
    const manager = new OnboardingManager();
    manager.modal = { addEventListener() {} };
    dom.elements.set('onboardingModal', manager.modal);
    manager.setupEventListeners = () => {};
    manager.checkOnboardingStatus = async () => ({ needs_onboarding: false });
    manager.loadPersonalAssistantState = async () => ({ state: 'needs_hire' });
    manager.populateTimezoneSelect = () => {};
    await manager.init();

    assert.deepEqual(dom.replacements, ['/?quest=meet-assistant']);
    assert.deepEqual(dom.navigations, []);
  } finally {
    globalThis.fetch = priorFetch;
    globalThis.bootstrap = priorBootstrap;
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

// A user with no accepted specialist must see today's flow. Focus areas,
// assignment labels, and step wording must all resolve to the shipped
// generic values.
test('the generic path is byte-for-byte unchanged when no specialist is accepted', () => {
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
