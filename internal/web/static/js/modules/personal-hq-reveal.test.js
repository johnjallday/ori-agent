import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHQPresentation } from './personal-hq-reveal.js';
import { createCreationTransition } from './workspace-creation-transition.js';

function fixture(overrides = {}) {
  const events = [];
  let foreground = true;
  let interrupt;
  const transition = createCreationTransition({
    panel: {
      suspend: () => {
        events.push('suspend');
        return true;
      },
      resume: options => {
        events.push(['resume', options]);
        return true;
      }
    },
    status: { show: view => events.push(['show', view]), hide: () => events.push('hide') }
  });
  const flow = createHQPresentation({
    transition,
    foreground: () => foreground,
    prepareMap: () => events.push('map'),
    frame: id => events.push(['frame', id]),
    focusPending: () => events.push('focus'),
    commitFolded: () => events.push('folded'),
    listen: fn => {
      interrupt = fn;
      return () => events.push('cleanup');
    },
    ...overrides
  });
  return {
    flow,
    transition,
    events,
    begin: () => flow.begin({ token: 'reviewed-request', route: '/' }),
    hide: () => {
      foreground = false;
      interrupt?.();
    },
    interrupt: options => interrupt?.(options)
  };
}

test('only the current verified result can adopt the existing first-note surface', () => {
  const f = fixture();
  const intent = f.begin();
  assert.equal(f.flow.adoptResult(intent), false);
  f.flow.ready(intent, { workspaceId: 'hq' });
  assert.equal(f.flow.adoptResult(intent), true);
  assert.equal(intent.externalResult, true);
  assert.equal(f.events.at(-1), 'hide');
  const newer = f.transition.enter({ owner: 'folder', token: 'new' });
  assert.equal(f.flow.adoptResult(intent), false);
  assert.equal(f.transition.isCurrent(newer), true);
});

test('reload preserves only the optional receipt marker, never foreground ownership', () => {
  const f = fixture();
  const intent = f.begin();
  f.flow.ready(intent, { workspaceId: 'hq' });
  f.interrupt({ preserveResult: true });
  assert.equal(intent.preserveResult, true);
  assert.equal(f.flow.current(intent), false);
  assert.equal(f.flow.ready(intent, { workspaceId: 'hq' }), false);
  const dismissed = f.begin();
  f.interrupt();
  assert.equal(dismissed.preserveResult, false);
  f.interrupt({ preserveResult: true });
  assert.equal(dismissed.preserveResult, false, 'pagehide cannot revive a dismissed result');
});

test('focus origin is captured before the disabled build control loses browser focus', () => {
  let captured;
  const f = fixture({
    captureFocus: value => {
      captured = value;
    }
  });
  f.flow.begin({ token: 'reviewed', focusOwned: true });
  assert.equal(captured, true);
});

test('explicit HQ intent steps chat aside without doing domain work or waiting on map', () => {
  const f = fixture();
  const intent = f.begin();
  assert.equal(f.flow.current(intent), true);
  assert.equal(f.transition.phase(), 'dispatching');
  assert.ok(f.events.includes('suspend'));
  assert.ok(f.events.includes('map'));
  assert.ok(f.events.includes('focus'));
  assert.equal(f.events.find(e => e[0] === 'show')[1].title, 'Setting up your HQ…');
});

test('known pre-acceptance refusal restores the held review and focus', () => {
  const f = fixture();
  assert.equal(f.flow.refused(f.begin()), true);
  assert.deepEqual(f.events.at(-1), ['resume', { reveal: true, restoreFocus: true }]);
  assert.ok(!f.events.includes('folded'));
});

test('uncertainty is not ready or a returnable refusal', () => {
  const f = fixture();
  const intent = f.begin();
  f.flow.unresolved(intent, { check: () => {} });
  assert.equal(intent.outcome, 'unresolved');
  assert.equal(f.flow.refused(intent), false);
  assert.ok(!f.events.includes('folded'));
  assert.ok(!f.events.some(e => e[0] === 'frame'));
});

test('ready frames only the canonical ID and commits HQ-specific folded preference', () => {
  const f = fixture();
  const intent = f.begin();
  f.flow.ready(intent, { workspaceId: 'canonical-hq', route: '/workspaces/hq', explore: () => {} });
  assert.equal(intent.outcome, 'ready');
  assert.ok(f.events.includes('folded'));
  assert.ok(f.events.some(e => e[0] === 'frame' && e[1] === 'canonical-hq'));
  assert.equal(f.events.at(-1)[1].actions[0].label, 'Explore HQ');
});

test('stale active HQ offers refresh and never a second build or frame', () => {
  const f = fixture();
  f.flow.ready(f.begin(), { workspaceId: 'real', stale: true, refresh: () => {} });
  const view = f.events.at(-1)[1];
  assert.match(view.title, /view needs refreshing/);
  assert.deepEqual(
    view.actions.map(a => a.label),
    ['Refresh view', 'Dismiss']
  );
  assert.ok(!f.events.some(e => e[0] === 'frame'));
});

for (const phase of ['pending', 'uncertain', 'ready'])
  test(`explicit chat reopen permanently yields during ${phase}`, () => {
    const f = fixture();
    const intent = f.begin();
    if (phase === 'uncertain') f.flow.unresolved(intent);
    if (phase === 'ready') f.flow.ready(intent, { workspaceId: 'hq' });
    f.transition.requestOpen();
    assert.equal(intent.yielded, true);
    const snapshot = [...f.events];
    assert.equal(f.flow.ready(intent, { workspaceId: 'late' }), false);
    assert.deepEqual(f.events, snapshot);
    assert.equal(f.transition.blocksReveal(), false);
  });

test('a newer shared creation owner cannot be cleared or framed by late HQ results', () => {
  const f = fixture();
  const intent = f.begin();
  const newer = f.transition.enter({ owner: 'folder', token: 'new' });
  const snapshot = [...f.events];
  assert.equal(f.flow.ready(intent, { workspaceId: 'late' }), false);
  f.flow.yield(intent);
  assert.equal(f.transition.isCurrent(newer), true);
  assert.deepEqual(
    f.events.filter(e => e !== 'cleanup'),
    snapshot.filter(e => e !== 'cleanup')
  );
});

test('hidden document and user map interaction yield without restoring the review', () => {
  for (const action of ['hide', 'interrupt']) {
    const f = fixture();
    const intent = f.begin();
    f[action]();
    assert.equal(f.flow.current(intent), false);
    assert.deepEqual(f.events.at(-1), ['resume', { reveal: false }]);
  }
});

test('missing transition and renderer failure cannot throw into domain creation', () => {
  assert.equal(createHQPresentation().begin({ token: 'request' }), null);
  const f = fixture({
    prepareMap: () => {
      throw new Error('renderer failed');
    }
  });
  const intent = f.begin();
  assert.equal(intent.yielded, false);
  assert.equal(f.transition.blocksReveal(), true);
  assert.equal(intent.outcome, 'pending');
});

test('missing map readiness is bounded and repeated ready refresh never reframes', () => {
  const timer = globalThis.setTimeout;
  let attempts = 0;
  globalThis.setTimeout = fn => {
    fn();
    return 0;
  };
  try {
    const f = fixture({
      frame: () => {
        attempts++;
        return false;
      }
    });
    const intent = f.begin();
    f.flow.ready(intent, { workspaceId: 'hq' });
    assert.equal(attempts, 20);
    f.flow.ready(intent, { workspaceId: 'hq' });
    assert.equal(attempts, 20);
    assert.equal(intent.outcome, 'ready');
  } finally {
    globalThis.setTimeout = timer;
  }
});

test('verified intent plays once; yielding skips motion and late completion cannot render', async () => {
  let finish;
  let plays = 0;
  let skips = 0;
  const done = new Promise(resolve => {
    finish = resolve;
  });
  const f = fixture({
    play: () => {
      plays++;
      return {
        done,
        skip: () => {
          skips++;
          finish();
        }
      };
    }
  });
  const intent = f.begin();
  f.flow.ready(intent, { workspaceId: 'hq' });
  assert.equal(plays, 1);
  assert.ok(f.events.at(-1)[1].actions.some(a => a.label === 'Skip animation'));
  f.flow.ready(intent, { workspaceId: 'hq' });
  assert.equal(plays, 1);
  f.interrupt();
  assert.equal(skips, 1);
  const snapshot = [...f.events];
  await done;
  await Promise.resolve();
  assert.deepEqual(f.events, snapshot);
});

test('status-only recovery does not acquire playback eligibility', () => {
  let plays = 0;
  const f = fixture({
    play: () => {
      plays++;
    }
  });
  const intent = f.begin();
  intent.eligible = false;
  f.flow.ready(intent, { workspaceId: 'hq' });
  assert.equal(plays, 0);
});

test('ordinary refresh has no eligible intent, and new controller instances cannot replay', () => {
  const f = fixture();
  assert.equal(f.flow.ready(null, { workspaceId: 'existing' }), false);
  assert.deepEqual(f.events, []);
  assert.equal(f.flow.begin({}), null);
});
