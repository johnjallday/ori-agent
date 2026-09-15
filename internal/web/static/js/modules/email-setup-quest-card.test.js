import test from 'node:test';
import assert from 'node:assert/strict';

// The module initializer is a no-op without the card element.
globalThis.document ||= { readyState: 'complete', getElementById: () => null };

const {
  EMAIL_SETUP_DISMISS_URL,
  EMAIL_SETUP_STATUS_URL,
  createEmailSetupQuestCard,
  dismissRequestBody,
  featuredMissionCoversQuest,
  resumeCardView
} = await import('./email-setup-quest-card.js');

function journey(overrides = {}) {
  return {
    run_id: 'run-1',
    state_revision: 3,
    lifecycle_state: 'in_progress',
    current_step_id: 'connect',
    dismissed: false,
    journey: { source: 'host', id: 'email_ops_setup', title: 'Set up Email Ops' },
    steps: [
      { id: 'team', title: 'Review your Email Ops team' },
      { id: 'connect', title: 'Connect Gmail' },
      { id: 'mailbox', title: 'Link the mailbox' },
      { id: 'summary', title: 'Email Ops is ready' }
    ],
    ...overrides
  };
}

test('the card renders only for a started, unfinished, undismissed Email Ops quest', () => {
  assert.deepEqual(resumeCardView({ exists: true, setup_journey: journey() }), {
    title: 'Set up Email Ops',
    progress: 'Step 2 of 4 · Connect Gmail',
    attention: false,
    revision: 3
  });
  assert.equal(
    resumeCardView({ exists: true, setup_journey: journey({ lifecycle_state: 'needs_attention' }) })
      .attention,
    true
  );
  for (const [name, status] of [
    ['never started', { exists: false }],
    ['missing body', null],
    [
      'not started lifecycle',
      { exists: true, setup_journey: journey({ lifecycle_state: 'not_started' }) }
    ],
    [
      'ready',
      { exists: true, setup_journey: journey({ lifecycle_state: 'ready', current_step_id: '' }) }
    ],
    ['dismissed', { exists: true, setup_journey: journey({ dismissed: true }) }],
    [
      'completed once, now regressed',
      {
        exists: true,
        setup_journey: journey({
          lifecycle_state: 'needs_attention',
          first_completed_at: '2026-09-15T10:00:00Z'
        })
      }
    ],
    [
      'another quest',
      {
        exists: true,
        setup_journey: journey({ journey: { source: 'plugin', id: 'reaper_setup' } })
      }
    ],
    ['unknown current step', { exists: true, setup_journey: journey({ current_step_id: 'other' }) }]
  ]) {
    assert.equal(resumeCardView(status), null, name);
  }
});

function fakeRoot() {
  const listeners = {};
  const parts = {};
  const part = role => {
    parts[role] ||= {
      textContent: '',
      addEventListener: (type, handler) => {
        listeners[`${role}:${type}`] = handler;
      }
    };
    return parts[role];
  };
  return {
    hidden: true,
    classes: new Set(),
    classList: {
      toggle(name, on) {
        on ? this.owner.classes.add(name) : this.owner.classes.delete(name);
      }
    },
    querySelector: selector => part(selector.match(/data-role="([^"]+)"/)[1]),
    parts,
    fire: (key, event) => listeners[key]?.(event),
    init() {
      this.classList.owner = this;
      return this;
    }
  }.init();
}

function recordingFetch(responses) {
  const calls = [];
  const fetchImpl = async (url, options = {}) => {
    calls.push({ url, method: options.method || 'GET', body: options.body });
    const next = responses.shift() || { ok: true, body: { exists: false } };
    return { ok: next.ok !== false, json: async () => next.body };
  };
  return { calls, fetchImpl };
}

const ready = async () => ({ needs_onboarding: false });

test('a never-started user gets one status read, no writes, and no card', async () => {
  const root = fakeRoot();
  const { calls, fetchImpl } = recordingFetch([{ body: { exists: false } }]);
  const card = createEmailSetupQuestCard(root, { fetchImpl, loadOnboarding: ready });
  assert.equal(await card.refresh(), null);
  assert.equal(root.hidden, true);
  assert.deepEqual(
    calls.map(call => `${call.method} ${call.url}`),
    [`GET ${EMAIL_SETUP_STATUS_URL}`]
  );
});

test('onboarding in progress renders nothing and reads no quest state', async () => {
  const root = fakeRoot();
  const { calls, fetchImpl } = recordingFetch([]);
  const card = createEmailSetupQuestCard(root, {
    fetchImpl,
    loadOnboarding: async () => ({ needs_onboarding: true })
  });
  await card.refresh();
  assert.equal(root.hidden, true);
  assert.equal(calls.length, 0);
});

test('Resume opens the quest in place and Not now persists the dismissal', async () => {
  const root = fakeRoot();
  const dispatched = [];
  const { calls, fetchImpl } = recordingFetch([
    { body: { exists: true, setup_journey: journey() } },
    { body: { exists: true, setup_journey: journey() } },
    { body: { setup_journey: journey({ dismissed: true }) } }
  ]);
  const card = createEmailSetupQuestCard(root, {
    fetchImpl,
    loadOnboarding: ready,
    dispatch: detail => dispatched.push(detail)
  });
  await card.refresh();
  assert.equal(root.hidden, false);
  assert.equal(root.parts['email-setup-title'].textContent, 'Set up Email Ops');
  assert.equal(root.parts['email-setup-progress'].textContent, 'Step 2 of 4 · Connect Gmail');

  let prevented = false;
  root.fire('email-setup-resume:click', { preventDefault: () => (prevented = true) });
  assert.equal(prevented, true);
  assert.deepEqual(dispatched, [{ source: 'host', quest_id: 'email_ops_setup' }]);
  assert.equal(root.hidden, true);

  // A modified click keeps normal link behaviour (new tab) and changes nothing.
  await card.refresh();
  root.fire('email-setup-resume:click', { metaKey: true, preventDefault: () => assert.fail() });

  assert.equal(await card.dismiss(), true);
  assert.equal(root.hidden, true);
  const dismissCall = calls.find(call => call.method === 'POST');
  assert.equal(dismissCall.url, EMAIL_SETUP_DISMISS_URL);
  const body = JSON.parse(dismissCall.body);
  assert.equal(body.if_revision, 3);
  assert.ok(body.idempotency_key);
});

test('a failed dismissal re-reads instead of pretending the card is gone', async () => {
  const root = fakeRoot();
  const { calls, fetchImpl } = recordingFetch([
    { body: { exists: true, setup_journey: journey() } },
    { ok: false, body: {} },
    { body: { exists: true, setup_journey: journey() } }
  ]);
  const card = createEmailSetupQuestCard(root, { fetchImpl, loadOnboarding: ready });
  await card.refresh();
  assert.equal(await card.dismiss(), false);
  assert.equal(root.hidden, false);
  assert.equal(calls.length, 3);
});

test('a late status response never repaints over a newer read', async () => {
  const root = fakeRoot();
  let release;
  const slow = new Promise(resolve => {
    release = resolve;
  });
  let firstFetchStarted;
  const started = new Promise(resolve => {
    firstFetchStarted = resolve;
  });
  let call = 0;
  const fetchImpl = async () => {
    call++;
    if (call === 1) {
      firstFetchStarted();
      await slow;
      return { ok: true, json: async () => ({ exists: true, setup_journey: journey() }) };
    }
    return { ok: true, json: async () => ({ exists: false }) };
  };
  const card = createEmailSetupQuestCard(root, { fetchImpl, loadOnboarding: ready });
  // The first read is in flight when a newer read starts and finishes.
  const first = card.refresh();
  await started;
  await card.refresh();
  release();
  assert.equal(await first, null);
  assert.equal(root.hidden, true);
});

test('showing the card never opens the quest or dispatches anything by itself', async () => {
  const root = fakeRoot();
  const dispatched = [];
  const { calls, fetchImpl } = recordingFetch([
    { body: { exists: true, setup_journey: journey({ lifecycle_state: 'needs_attention' }) } }
  ]);
  const card = createEmailSetupQuestCard(root, {
    fetchImpl,
    loadOnboarding: ready,
    dispatch: detail => dispatched.push(detail)
  });
  await card.refresh();
  assert.equal(root.hidden, false);
  assert.equal(root.classes.has('is-paused'), true);
  assert.deepEqual(dispatched, [], 'only an explicit Resume click opens the quest');
  assert.deepEqual(
    calls.map(call => call.method),
    ['GET'],
    'rendering the card is read-only'
  );
});

test('dismiss request bodies carry only the revision and a fresh key', () => {
  assert.deepEqual(dismissRequestBody('7', 'key-1'), { if_revision: 7, idempotency_key: 'key-1' });
});

// Mission 03's email branch offers the same Resume on the featured card, so
// this card must not show a second one for the same setup.
test('featuredMissionCoversQuest matches only an open featured mission on this setup', () => {
  const url = '/?setup=quest&source=host&quest=email_ops_setup';
  assert.equal(featuredMissionCoversQuest({ visible: true, actionURL: url }), true);
  assert.equal(
    featuredMissionCoversQuest({ visible: true, completed: true, actionURL: url }),
    false
  );
  assert.equal(featuredMissionCoversQuest({ visible: false, actionURL: url }), false);
  assert.equal(
    featuredMissionCoversQuest({ visible: true, actionURL: '/?quest=plan-first-day' }),
    false
  );
  assert.equal(featuredMissionCoversQuest(undefined), false);
});

test('the card stays hidden while the featured mission offers this setup, and returns after', async () => {
  const root = fakeRoot();
  let featured = { visible: true, actionURL: '/?setup=quest&source=host&quest=email_ops_setup' };
  const { fetchImpl } = recordingFetch([{ body: { exists: true, setup_journey: journey() } }]);
  const card = createEmailSetupQuestCard(root, {
    fetchImpl,
    loadOnboarding: ready,
    featuredMission: () => featured
  });

  const view = await card.refresh();
  assert.ok(view, 'the quest is still resumable');
  assert.equal(root.hidden, true, 'a second Resume for the same setup was shown');

  // The mission moves on (for example, the user deferred it).
  featured = { visible: true, actionURL: '/' };
  card.featuredMissionChanged();
  assert.equal(root.hidden, false);
  assert.equal(root.parts['email-setup-progress'].textContent, 'Step 2 of 4 · Connect Gmail');

  featured = { visible: true, actionURL: '/?setup=quest&source=host&quest=email_ops_setup' };
  card.featuredMissionChanged();
  assert.equal(root.hidden, true);
});

test('a dismissed card never comes back when the featured mission changes', async () => {
  const root = fakeRoot();
  const { fetchImpl } = recordingFetch([
    { body: { exists: true, setup_journey: journey() } },
    { ok: true, body: {} }
  ]);
  const card = createEmailSetupQuestCard(root, {
    fetchImpl,
    loadOnboarding: ready,
    featuredMission: () => ({ visible: false })
  });
  await card.refresh();
  assert.equal(root.hidden, false);
  assert.equal(await card.dismiss(), true);
  card.featuredMissionChanged();
  assert.equal(root.hidden, true);
});
