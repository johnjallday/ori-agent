import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  LIBRARY_BADGE_MIN_REFRESH_MS,
  createLibraryBadgeLoader,
  libraryBadgeView,
  libraryHomeIds,
  libraryToSetUp
} from './home-library-badges.js';
import { setupProjectsChipHTML } from './workspace-overview.js';
import { bucketProject, setupTotals } from './library-setup.js';

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

test('badge text names only the nonzero parts in a fixed order, never "new" or "ready"', () => {
  assert.equal(libraryBadgeView(summary(), 'Music Home').text, '5 to set up');
  assert.equal(
    libraryBadgeView(summary({ ready_proposals: 2 }), 'Music Home').text,
    '5 to set up · 2 to review'
  );
  assert.equal(
    libraryBadgeView(summary({ digest: { new: 0, activatable: 1 } }), 'Music Home').label,
    'Music Home library: 1 project to set up. Open the suggestions shelf.'
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
  assert.equal(view.count, 5, 'the district menu counts what the sheet can set up');
  assert.equal(
    libraryBadgeView(summary({ ready_proposals: 1, digest: { new: 0, activatable: 0 } })).count,
    0
  );
  assert.equal(
    view.label,
    'Music Home library: 5 projects to set up, 1 suggestion to review. Open the suggestions shelf.'
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

test('a library scanned more than a day ago says its age on the badge, still navigation only', () => {
  const now = Date.parse('2026-11-05T12:00:00Z');
  const daysAgo = days => new Date(now - days * 86_400_000).toISOString();
  // Fresh: the badge is exactly what it was.
  const fresh = libraryBadgeView(
    summary({ last_complete_scan_at: daysAgo(0.5) }),
    'Music Home',
    now
  );
  assert.equal(fresh.text, '5 to set up');
  // Stale, with counts: the age comes first, the counts stay.
  const stale = libraryBadgeView(summary({ last_complete_scan_at: daysAgo(3) }), 'Music Home', now);
  assert.equal(stale.text, 'Scanned 3 days ago · 5 to set up');
  assert.equal(stale.count, 5);
  assert.equal(stale.route, ROUTE);
  assert.equal(
    stale.label,
    'Music Home library: last scanned 3 days ago, 5 projects to set up. Open the suggestions shelf.'
  );
  // Stale with nothing to do still shows the age, so the map tells the truth.
  const quiet = libraryBadgeView(
    summary({ last_complete_scan_at: daysAgo(2), digest: { new: 0, activatable: 0 } }),
    'Music Home',
    now
  );
  assert.equal(quiet.text, 'Scanned 2 days ago');
  assert.equal(quiet.count, 0, 'the age never offers a setup');
  // No complete scan, or a bad timestamp: no age, so no badge without counts.
  for (const at of [undefined, 'not a date', 42])
    assert.equal(
      libraryBadgeView(
        summary({ last_complete_scan_at: at, digest: { new: 0, activatable: 0 } }),
        'Music Home',
        now
      ),
      null
    );
  // The age never invents a route.
  assert.equal(
    libraryBadgeView(summary({ last_complete_scan_at: daysAgo(5), route: '/x' }), 'Home', now),
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
  assert.equal(first['home-1'].text, '5 to set up');
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

// The demo fixture after a folder run that installed the tools: Album-1..4
// with one Song.rpp, Album-5 with Song.rpp and Alternate.rpp, one Logic
// project the installed tools cannot open. The chip in the Home, the badge on
// the outer map and the sheet's rows must all count the same 5.
test('chip, badge and sheet agree on one library summary', () => {
  const after = summary({
    ready_proposals: 1,
    digest: {
      scan_id: 's',
      projects: 6,
      activatable: 5,
      needs_file_choice: 1,
      unsupported_format: 1
    }
  });
  assert.equal(libraryToSetUp(after), 5);
  assert.match(setupProjectsChipHTML({ toSetUp: libraryToSetUp(after) }, 2), /<b>5<\/b> to set up/);
  assert.equal(libraryBadgeView(after, 'Music Production Home').text, '5 to set up · 1 to review');

  const one = { state: 'review_available', project_files: ['Song.rpp'] };
  const rows = [
    ...[1, 2, 3, 4].map(() => bucketProject({}, one)),
    bucketProject(
      {},
      { state: 'file_choice_required', project_files: ['Alternate.rpp', 'Song.rpp'] }
    ),
    bucketProject(
      {},
      { state: 'unsupported_format', reason: 'No observed project file uses the installed format.' }
    )
  ];
  const totals = setupTotals(rows);
  assert.equal(totals.setup + totals.file_choice, libraryToSetUp(after));
  assert.equal(totals.file_choice, after.digest.needs_file_choice);
  assert.equal(totals.dashboard, after.digest.unsupported_format);

  // A note that is still true (no project tools) hides both, as before.
  const noTools = summary({
    digest: {
      scan_id: 's',
      projects: 6,
      activatable: 0,
      setup_note: 'project_provider_unavailable'
    }
  });
  assert.equal(libraryToSetUp(noTools), 0);
  assert.equal(setupProjectsChipHTML({ toSetUp: libraryToSetUp(noTools) }, 2), '');
  assert.equal(libraryBadgeView(noTools), null);
  // A malformed digest never counts.
  assert.equal(libraryToSetUp({ digest: { activatable: '5' } }), 0);
  assert.equal(libraryToSetUp(null), 0);
});

// A second folder joined the Home: the last scan's digest describes only that
// folder (5), but the shelf also holds six songs listed earlier, now settable
// because the tools arrived. The sheet offers all of them; so must the chip
// and the badge, from the server's whole-shelf count.
test('the whole-shelf count wins over the last scan, even over a stale note', () => {
  const joined = summary({
    ready_proposals: 3,
    to_set_up: 11,
    needs_file_choice: 1,
    digest: {
      scan_id: 's',
      projects: 6,
      activatable: 5,
      needs_file_choice: 1,
      unsupported_format: 1
    }
  });
  assert.equal(libraryToSetUp(joined), 11);
  assert.equal(libraryBadgeView(joined).text, '11 to set up · 3 to review');
  assert.equal(libraryBadgeView(joined).count, 11);
  assert.match(setupProjectsChipHTML({ toSetUp: libraryToSetUp(joined) }, 3), /<b>11<\/b>/);
  const staleNote = summary({
    to_set_up: 2,
    digest: { scan_id: 's', activatable: 0, setup_note: 'project_provider_unavailable' }
  });
  assert.equal(libraryToSetUp(staleNote), 2);
  assert.equal(libraryToSetUp(summary({ to_set_up: 0 })), 0);
});
