import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  GENERIC_FOCUS_AREAS,
  HIRE_BOUNDARY_COPY,
  HIRE_REQUEST_STORAGE_KEY,
  HQ_QUEST_ROUTE,
  HQ_CARD_ROUTE,
  JUST_HIRED_FLAG,
  MEET_ASSISTANT_AGENTS_ROUTE,
  MEET_ASSISTANT_BRIEFING_ROUTE,
  MEET_ASSISTANT_GUIDED_FLAG,
  MEET_ASSISTANT_QUEST_ROUTE,
  buildPersonalAssistantHirePayload,
  clearHireRequestId,
  getOrCreateHireRequestId,
  personalAssistantCanOpenHireFlow,
  personalAssistantNeedsHQ,
  personalAssistantRecoveryView,
  personalAssistantResumeMessage,
  presetView,
  submitHire,
  submitRepair
} from './personal-assistant-hire.js';

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key)
  };
}

function jsonResponse(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

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

test('personalAssistantCanOpenHireFlow never reopens creation for a paused relationship', () => {
  assert.equal(personalAssistantCanOpenHireFlow({ state: 'paused' }), false);
  assert.equal(personalAssistantCanOpenHireFlow({ state: 'repair_needed' }), true);
  assert.equal(personalAssistantCanOpenHireFlow({ state: 'needs_hire' }), true);
});

test('personalAssistantRecoveryView distinguishes reconnectable and blocked orphan evidence', () => {
  assert.deepEqual(
    personalAssistantRecoveryView({
      state: 'repair_needed',
      repair_step: 'relationship_recovery'
    }),
    { repair: true, available: true, blocked: false }
  );
  assert.deepEqual(
    personalAssistantRecoveryView({
      state: 'repair_needed',
      repair_step: 'relationship_recovery_blocked'
    }),
    { repair: true, available: false, blocked: true }
  );
  assert.deepEqual(personalAssistantRecoveryView({ state: 'needs_hire' }), {
    repair: false,
    available: false,
    blocked: false
  });
});

test('the routes and flags are the ones the rest of the app links to', () => {
  // A focus parameter would preselect the landmark. The quest highlights it and
  // waits for a real user selection instead.
  assert.equal(HQ_QUEST_ROUTE, '/?quest=build-hq');
  assert.equal(HQ_CARD_ROUTE, '/?panel=today');
  const roster = readFileSync(new URL('../agents-roster.js', import.meta.url), 'utf8');
  assert.equal((roster.match(/api\.HQ_CARD_ROUTE/g) || []).length, 3);
  assert.equal(roster.includes('api.HQ_QUEST_ROUTE'), false);
  // Mission 01's action URL, the same string the server's quest carries: the
  // walkthrough from its first step, on Home.
  assert.equal(MEET_ASSISTANT_QUEST_ROUTE, '/?quest=meet-assistant');
  // Where onboarding hands over: Ori's briefing, then that same first step.
  assert.equal(MEET_ASSISTANT_BRIEFING_ROUTE, '/?quest=meet-assistant&briefing=1');
  // Its Agents page leg, for entries already on their way there.
  assert.equal(MEET_ASSISTANT_AGENTS_ROUTE, '/agents?quest=meet-assistant');
  assert.equal(MEET_ASSISTANT_GUIDED_FLAG, 'ori:meet-assistant-guided');
  assert.equal(JUST_HIRED_FLAG, 'ori:assistant-just-hired');
  // The retired wizard's key, so a hire started there resumes here.
  assert.equal(HIRE_REQUEST_STORAGE_KEY, 'ori.personalAssistantHireRequestId');
});

// Classic scripts that cannot import the constant name the route literally.
// Pinned here so a change to the constant cannot leave one of them behind.
test('every classic script names the same Mission 01 routes', () => {
  for (const file of ['./ori-guide.js']) {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8');
    for (const route of [MEET_ASSISTANT_QUEST_ROUTE, MEET_ASSISTANT_AGENTS_ROUTE]) {
      assert.ok(source.includes(`'${route}'`), `${file} does not name ${route}`);
    }
  }
  // The Agents page walkthrough reads the flag the Home step sets.
  const quest = readFileSync(new URL('./meet-assistant-quest.js', import.meta.url), 'utf8');
  assert.ok(quest.includes(`'${MEET_ASSISTANT_GUIDED_FLAG}'`));
});

test('the hire offers the six focus areas with the wizard’s values and defaults', () => {
  assert.deepEqual(
    GENERIC_FOCUS_AREAS.map(option => [option.value, option.selected]),
    [
      ['plan_my_day', true],
      ['track_commitments_and_follow_ups', true],
      ['prepare_for_meetings', false],
      ['keep_projects_moving', true],
      ['help_with_email', false],
      ['something_else', false]
    ]
  );
  assert.match(
    HIRE_BOUNDARY_COPY,
    /^Hiring creates your assistant\. It does not create a workspace/
  );
});

test('a hire attempt reuses one request id until the server confirms it', () => {
  const storage = memoryStorage();
  const first = getOrCreateHireRequestId(storage);
  assert.ok(first, 'no id was created');
  assert.equal(storage.getItem(HIRE_REQUEST_STORAGE_KEY), first);
  // A retry after a network failure replays the same request.
  assert.equal(getOrCreateHireRequestId(storage), first);

  // The server's record of an in-flight hire wins over the browser's copy.
  assert.equal(getOrCreateHireRequestId(storage, ' server-request '), 'server-request');
  assert.equal(storage.getItem(HIRE_REQUEST_STORAGE_KEY), 'server-request');

  clearHireRequestId(storage);
  assert.equal(storage.getItem(HIRE_REQUEST_STORAGE_KEY), null);
  assert.notEqual(getOrCreateHireRequestId(storage), 'server-request');
});

test('a hire request id survives storage that throws', () => {
  const broken = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    },
    removeItem() {
      throw new Error('denied');
    }
  };
  assert.ok(getOrCreateHireRequestId(broken));
  assert.equal(getOrCreateHireRequestId(broken, 'server-request'), 'server-request');
  assert.doesNotThrow(() => clearHireRequestId(broken));
  assert.ok(getOrCreateHireRequestId(null));
});

test('submitHire posts the payload once and reports a fresh hire', async () => {
  const calls = [];
  const payload = buildPersonalAssistantHirePayload({ requestId: 'r-1', displayName: 'Atlas' });
  const result = await submitHire({
    payload,
    fetchImpl: async (url, options) => {
      calls.push({ url, options });
      return jsonResponse(201, {
        personal_assistant: { state: 'needs_hq', display_name: 'Atlas', resumed: false }
      });
    }
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/personal-assistant/hire');
  assert.equal(calls[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(calls[0].options.body), payload);
  assert.equal(result.ok, true);
  assert.equal(result.resumed, false);
  assert.equal(result.needsHQ, true);
  assert.equal(result.state.display_name, 'Atlas');
});

test('submitHire treats a replay and personal_hq_required as success', async () => {
  const replay = await submitHire({
    payload: {},
    fetchImpl: async () =>
      jsonResponse(200, { personal_assistant: { state: 'needs_hq', resumed: true } })
  });
  assert.equal(replay.ok, true);
  assert.equal(replay.resumed, true);
  assert.equal(replay.needsHQ, true);

  const hired = await submitHire({
    payload: {},
    fetchImpl: async () =>
      jsonResponse(409, { code: 'personal_hq_required', error: 'Build Personal HQ first.' })
  });
  assert.equal(hired.ok, true, '409 personal_hq_required means already hired');
  assert.equal(hired.needsHQ, true);
});

test('submitHire reports the server’s message on any other failure', async () => {
  const conflict = await submitHire({
    payload: {},
    fetchImpl: async () =>
      jsonResponse(409, {
        code: 'hire_conflict',
        error: 'This hire conflicts. Refresh and try again.'
      })
  });
  assert.equal(conflict.ok, false);
  assert.equal(conflict.status, 409);
  assert.equal(conflict.code, 'hire_conflict');
  assert.equal(conflict.error, 'This hire conflicts. Refresh and try again.');

  const invalid = await submitHire({
    payload: {},
    fetchImpl: async () => ({
      ok: false,
      status: 400,
      json: async () => Promise.reject(new Error())
    })
  });
  assert.equal(invalid.ok, false);
  assert.match(invalid.error, /retry this same request/i);

  const offline = await submitHire({
    payload: {},
    fetchImpl: async () => {
      throw new TypeError('Failed to fetch');
    }
  });
  assert.equal(offline.ok, false);
  assert.match(offline.error, /will not hire a second assistant/i);
});

test('submitRepair names no identity and reports the reconnected state', async () => {
  let body = null;
  const repaired = await submitRepair({
    stateVersion: 4,
    fetchImpl: async (url, options) => {
      assert.equal(url, '/api/personal-assistant/repair');
      body = JSON.parse(options.body);
      return jsonResponse(200, { personal_assistant: { state: 'needs_hq' } });
    }
  });
  assert.deepEqual(body, { if_version: 4 });
  assert.equal(repaired.ok, true);
  assert.equal(repaired.needsHQ, true);

  const refused = await submitRepair({
    fetchImpl: async () =>
      jsonResponse(409, { code: 'recovery_blocked', error: 'Existing records do not agree.' })
  });
  assert.equal(refused.ok, false);
  assert.equal(refused.error, 'Existing records do not agree.');

  const offline = await submitRepair({
    fetchImpl: async () => {
      throw new TypeError('Failed to fetch');
    }
  });
  assert.equal(offline.ok, false);
  assert.match(offline.error, /nothing was changed/i);
});

test('presetView picks the preset only while there is no assistant to keep', () => {
  for (const state of ['needs_hire', 'hiring']) {
    const view = presetView({ state });
    assert.equal(view.mode, 'form', state);
    assert.equal(view.title, 'Hire your personal assistant');
    assert.equal(view.buttonLabel, 'Hire assistant');
  }
  for (const state of ['needs_hq', 'provisioning_hq', 'active', 'paused', '']) {
    assert.equal(presetView({ state }).mode, 'standard', state);
  }
  assert.equal(presetView(null).mode, 'standard');
  assert.equal(presetView(undefined).mode, 'standard');
});

test('presetView shows one Reconnect button for provable orphans and none when blocked', () => {
  const reconnect = presetView({
    state: 'repair_needed',
    repair_step: 'relationship_recovery',
    display_name: 'Atlas'
  });
  assert.equal(reconnect.mode, 'reconnect');
  assert.equal(reconnect.title, 'Reconnect your assistant');
  assert.equal(reconnect.buttonLabel, 'Reconnect assistant');
  assert.match(reconnect.message, /existing profile for Atlas/);
  assert.match(
    presetView({
      state: 'repair_needed',
      repair_step: 'relationship_recovery',
      hq_workspace_id: 'hq-1'
    }).message,
    /and Personal HQ/
  );

  const blocked = presetView({
    state: 'repair_needed',
    repair_step: 'relationship_recovery_blocked'
  });
  assert.equal(blocked.mode, 'blocked');
  assert.equal(blocked.buttonLabel, '');
  assert.match(blocked.detail, /No records have been changed/);
});

test('presetView resumes a partial hire instead of offering a second one', () => {
  const resume = presetView({
    state: 'repair_needed',
    repair_step: 'profile_creation',
    hire_request_id: 'r-1',
    display_name: 'Atlas',
    assistant_id: 'a-1'
  });
  assert.equal(resume.mode, 'resume');
  assert.equal(resume.buttonLabel, 'Finish setup');
  assert.match(resume.message, /Atlas is already saved/);
  // Without a hire on record there is nothing to replay.
  assert.equal(presetView({ state: 'repair_needed' }).mode, 'standard');
});
