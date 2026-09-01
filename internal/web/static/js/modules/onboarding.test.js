import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OnboardingManager,
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
