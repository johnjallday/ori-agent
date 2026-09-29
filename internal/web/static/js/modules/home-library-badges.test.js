import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  LIBRARY_BADGE_MIN_REFRESH_MS,
  createLibraryBadgeLoader,
  libraryBadgeView,
  libraryHomeIds
} from './home-library-badges.js';

const ROUTE = '/workspaces/music-home/assistant#projectLibraryProposals';
const HOME = { id: 'home-1', name: 'Music Home', group_template: { kind: 'managed_home' } };

function summary(overrides = {}) {
  return {
    initialized: true,
    route: ROUTE,
    ready_proposals: 0,
    digest: { scan_id: 's', new: 6, activatable: 5, unsupported_format: 1 },
    ...overrides
  };
}

test('badge text names only the nonzero parts in a fixed order', () => {
  assert.equal(libraryBadgeView(summary(), 'Music Home').text, '6 new · 5 ready');
  assert.equal(
    libraryBadgeView(summary({ ready_proposals: 2 }), 'Music Home').text,
    '6 new · 5 ready · 2 to review'
  );
  assert.equal(
    libraryBadgeView(
      summary({ ready_proposals: 1, digest: { new: 0, activatable: 0 } }),
      'Music Home'
    ).text,
    '1 to review'
  );
  const view = libraryBadgeView(summary({ ready_proposals: 1 }), 'Music Home');
  assert.equal(view.route, ROUTE);
  assert.equal(
    view.label,
    'Music Home library: 6 new projects, 5 ready to set up, 1 suggestion to review. Open the suggestions shelf.'
  );
});

test('badge hides when nothing is ready, setup is unchecked or the route is not the shelf', () => {
  assert.equal(libraryBadgeView(null), null);
  assert.equal(libraryBadgeView(summary({ digest: null })), null);
  assert.equal(
    libraryBadgeView(summary({ digest: { new: 6, activatable: 0 } })),
    null,
    'new projects alone are not a reason to interrupt'
  );
  assert.equal(
    libraryBadgeView(
      summary({ digest: { new: 6, activatable: 5, setup_note: 'project_provider_unavailable' } })
    ),
    null
  );
  for (const route of [
    'https://example.com/workspaces/x/assistant#projectLibraryProposals',
    '/workspaces/x/assistant',
    '/workspaces/../x/assistant#projectLibraryProposals/extra',
    'javascript:alert(1)'
  ])
    assert.equal(libraryBadgeView(summary({ route })), null, route);
  assert.equal(
    libraryBadgeView(summary({ digest: { new: -1, activatable: '5' }, ready_proposals: 1.5 })),
    null
  );
});

test('only managed program Homes are asked for a summary, at most twenty', () => {
  const many = Array.from({ length: 25 }, (_, i) => ({
    id: `home-${i}`,
    group_template: { kind: 'managed_home' }
  }));
  assert.deepEqual(
    libraryHomeIds([HOME, { id: 'plain-group' }, { id: 'ws', group_template: { kind: 'other' } }]),
    ['home-1']
  );
  assert.equal(libraryHomeIds(many).length, 20);
});

test('loader reuses its answer inside thirty seconds unless forced, and never polls', async () => {
  let clock = 1_000;
  const requests = [];
  const loader = createLibraryBadgeLoader({
    now: () => clock,
    fetchImpl: async url => {
      requests.push(url);
      return { ok: true, json: async () => summary() };
    }
  });
  const first = await loader.load([HOME]);
  assert.deepEqual(Object.keys(first), ['home-1']);
  assert.equal(first['home-1'].text, '6 new · 5 ready');
  assert.deepEqual(requests, ['/api/workspaces/home-1/assistant-program/library/summary']);
  clock += LIBRARY_BADGE_MIN_REFRESH_MS - 1;
  await loader.load([HOME]);
  assert.equal(requests.length, 1, 'a visibility flip inside the window reuses the answer');
  await loader.load([HOME], { force: true });
  assert.equal(requests.length, 2, 'a back/forward return may force a fresh read');
  clock += LIBRARY_BADGE_MIN_REFRESH_MS;
  await loader.load([HOME]);
  assert.equal(requests.length, 3);
  const source = readFileSync(new URL('./home-library-badges.js', import.meta.url), 'utf8');
  assert.equal(/setInterval|setTimeout/.test(source), false, 'the badge never runs on a timer');
});

test('loader shares one request between concurrent calls and hides failed Homes', async () => {
  let release;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  let calls = 0;
  const loader = createLibraryBadgeLoader({
    fetchImpl: async url => {
      calls++;
      await gate;
      if (url.includes('broken')) return { ok: false, status: 404, json: async () => ({}) };
      return { ok: true, json: async () => summary() };
    }
  });
  const homes = [HOME, { id: 'broken', group_template: { kind: 'managed_home' } }];
  const a = loader.load(homes);
  const b = loader.load(homes);
  release();
  const [first, second] = await Promise.all([a, b]);
  assert.equal(calls, 2, 'one summary read per Home, not per caller');
  assert.equal(first, second);
  assert.deepEqual(Object.keys(first), ['home-1']);
});
