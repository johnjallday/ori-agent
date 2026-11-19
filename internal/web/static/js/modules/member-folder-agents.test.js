import test from 'node:test';
import assert from 'node:assert/strict';

import { commanderOneLine, WAITING_PHASES } from './commander-speech.js';
import { folderAgentReportView } from './folder-agent-card.js';
import {
  MEMBER_POLL_FAST_MS,
  memberFolderAgentState,
  memberFolderAgentViews,
  memberSetupsFinished,
  memberTileLines,
  memberWaits,
  memberWorkspaceIds,
  shouldPollMembersFast
} from './member-folder-agents.js';

// A Music Production Home (home) with a folder (albums) that holds songs, a
// second folder (beats) beside it, and one workspace that is not under it.
const ROWS = [
  { id: 'home', name: 'Music Production Home', parent_id: '' },
  { id: 'albums', name: 'Albums', parent_id: 'home' },
  { id: 'album-1', name: 'Album-1', parent_id: 'albums' },
  { id: 'beats', name: 'Beats', parent_id: 'home' },
  { id: 'outsider', name: 'Outside', parent_id: '' }
];

const raw = (workspace, overrides = {}) => ({
  workspace_id: workspace,
  workspace_name: workspace,
  folder_name: workspace,
  agent_name: `${workspace} Manager`,
  state: 'reported',
  actions: ['set_up', 'keep', 'look_further'],
  ...overrides
});
const done = { status: 'done', lines: [] };
const songsOffer = {
  count: 5,
  noun: 'songs',
  ask: 'Want me to learn the 5 songs?',
  reads: 'I will read tempo and track names.',
  not_read: ''
};

// One raw view per waiting phase, in the order WAITING_PHASES lists them.
const WAITING = {
  premise: raw('w-premise', { premise: { state: 'asked', sentence: 'These look like songs.' } }),
  report: raw('w-report'),
  withdrawn: raw('w-withdrawn', {
    premise: { state: 'withdrawn', sentence: 'Something else.', withdrawn: true }
  }),
  cause: raw('w-cause', { state: 'failed' }),
  stopped: raw('w-stopped', { state: 'set_up', setup: { status: 'stopped', lines: [] } }),
  songs: raw('w-songs', { state: 'set_up', setup: done, songs_offer: songsOffer }),
  goal: raw('w-goal', { state: 'set_up', setup: done, goal_offer: { goal: 'Finish the album' } })
};

test('the members of a group are every workspace under it at any depth, never the group itself', () => {
  assert.deepEqual([...memberWorkspaceIds(ROWS, 'home')].sort(), ['album-1', 'albums', 'beats']);
  assert.deepEqual([...memberWorkspaceIds(ROWS, 'albums')], ['album-1']);
  assert.deepEqual([...memberWorkspaceIds(ROWS, 'album-1')], []);
  // Nothing is a member of an unknown or empty group, and bad input is ignored.
  assert.deepEqual([...memberWorkspaceIds(ROWS, 'ghost')], []);
  assert.deepEqual([...memberWorkspaceIds(ROWS, '')], []);
  assert.deepEqual([...memberWorkspaceIds(null, 'home')], []);
  assert.deepEqual([...memberWorkspaceIds([null, {}, { id: 'x' }], 'home')], []);
});

test('a malformed cycle in the rows ends instead of looping', () => {
  const rows = [
    { id: 'a', parent_id: 'b' },
    { id: 'b', parent_id: 'a' },
    { id: 'c', parent_id: 'b' }
  ];
  assert.deepEqual([...memberWorkspaceIds(rows, 'a')].sort(), ['b', 'c']);
});

test('only the listed views under the group are kept, and the group’s own is never one of them', () => {
  const views = [
    raw('home'),
    raw('albums'),
    raw('album-1'),
    raw('outsider'),
    { state: 'reported' },
    null
  ];
  assert.deepEqual(
    memberFolderAgentViews(views, ROWS, 'home').map(view => view.workspace_id),
    ['albums', 'album-1']
  );
  assert.deepEqual(memberFolderAgentViews('nothing', ROWS, 'home'), []);
  assert.deepEqual(memberFolderAgentViews(views, ROWS, ''), []);
});

test('every one of the seven waiting moments is a wait, in the Commander’s own words (rule: no second definition)', () => {
  assert.deepEqual(Object.keys(WAITING), WAITING_PHASES);
  const views = Object.values(WAITING);
  const waits = memberWaits(views);
  assert.equal(Object.keys(waits).length, 7);
  Object.entries(WAITING).forEach(([phase, view]) => {
    const line = commanderOneLine(folderAgentReportView(view));
    assert.equal(line.waiting, true, phase);
    assert.deepEqual(
      waits[view.workspace_id],
      {
        status: line.status,
        text: line.text,
        action: line.action,
        phase,
        agent: `${view.workspace_id} Manager`
      },
      phase
    );
  });
  // The songs offer, as the owner meets it.
  assert.deepEqual(waits['w-songs'], {
    status: 'Has a question for you',
    text: 'w-songs Manager asks to learn the songs',
    action: 'Answer',
    phase: 'songs',
    agent: 'w-songs Manager'
  });
  assert.equal(waits['w-goal'].status, 'Suggests a goal');
  assert.equal(waits['w-stopped'].status, 'A step did not finish');
});

test('work in progress, a finished setup and a kept folder are not waits', () => {
  const quiet = [
    raw('looking', { state: 'looking' }),
    raw('setting', { state: 'setting_up' }),
    raw('learning', {
      state: 'set_up',
      setup: done,
      learning: { status: 'running', line: 'Learning Night Drive… (2 of 7)', done: 1, total: 7 }
    }),
    raw('finished', { state: 'set_up', setup: done }),
    raw('kept', { state: 'kept' }),
    { state: 'reported' },
    null
  ];
  assert.deepEqual(memberWaits(quiet), {});
  assert.deepEqual(memberWaits(null), {});
  // Given both, the Commander asks one thing at a time: the songs, not the goal.
  const both = raw('both', {
    state: 'set_up',
    setup: done,
    songs_offer: songsOffer,
    goal_offer: { goal: 'Finish the album' }
  });
  assert.equal(memberWaits([both]).both.phase, 'songs');
});

test('a member that is working shows its live line, and a waiting or idle one shows none', () => {
  const lines = memberTileLines([
    raw('looking', { state: 'looking', progress_line: 'Looking in Album-3 · 12 folders so far' }),
    raw('setting', { state: 'setting_up' }),
    raw('learning', {
      state: 'set_up',
      setup: done,
      learning: { status: 'running', line: 'Learning Night Drive… (2 of 7)', done: 1, total: 7 }
    }),
    WAITING.songs,
    WAITING.report,
    raw('kept', { state: 'kept' }),
    { state: 'looking' },
    null
  ]);
  assert.deepEqual(lines, {
    looking: 'Looking in Album-3 · 12 folders so far',
    setting: 'Setting up…',
    learning: 'Learning Night Drive… (2 of 7)'
  });
  assert.deepEqual(memberTileLines(undefined), {});
});

test('a wait and a live line never stand on the same member, so the tile and its mark never compete', () => {
  const views = [
    ...Object.values(WAITING),
    raw('looking', { state: 'looking' }),
    raw('setting', { state: 'setting_up' }),
    raw('learning', {
      state: 'set_up',
      setup: done,
      learning: {
        status: 'summarizing',
        line: 'Putting together what I learned…',
        done: 7,
        total: 7
      }
    })
  ];
  const waits = memberWaits(views);
  const lines = memberTileLines(views);
  Object.keys(waits).forEach(id => assert.equal(lines[id], undefined, id));
  assert.deepEqual(Object.keys(lines).sort(), ['learning', 'looking', 'setting']);
});

test('the listing is read again soon only while a member’s agent is working', () => {
  assert.equal(MEMBER_POLL_FAST_MS, 2000);
  assert.equal(shouldPollMembersFast([raw('a', { state: 'looking' })]), true);
  assert.equal(shouldPollMembersFast([raw('a', { state: 'setting_up' })]), true);
  for (const status of ['running', 'summarizing']) {
    assert.equal(
      shouldPollMembersFast([raw('a', { state: 'set_up', setup: done, learning: { status } })]),
      true,
      status
    );
  }
  // Waiting is not working: the owner's answer arrives as an event, not a timer.
  assert.equal(shouldPollMembersFast(Object.values(WAITING)), false);
  assert.equal(shouldPollMembersFast([raw('a', { state: 'set_up', setup: done })]), false);
  assert.equal(shouldPollMembersFast([]), false);
  assert.equal(shouldPollMembersFast(null), false);
});

test('one read of the listing gives the page its members, waits, lines and pace', () => {
  const views = [
    // The group's own agent speaks through its own Commander: not a member.
    raw('home', { state: 'set_up', setup: done, goal_offer: { goal: 'Make an album' } }),
    raw('albums', { state: 'set_up', setup: done, songs_offer: songsOffer }),
    raw('album-1', { state: 'setting_up' }),
    raw('outsider', { state: 'looking' })
  ];
  const state = memberFolderAgentState(views, ROWS, 'home');
  assert.deepEqual(
    state.views.map(view => view.workspace_id),
    ['albums', 'album-1']
  );
  assert.deepEqual(Object.keys(state.waits), ['albums']);
  assert.equal(state.waits.albums.phase, 'songs');
  assert.deepEqual(state.lines, { 'album-1': 'Setting up…' });
  assert.equal(state.fast, true, 'a member is setting up');

  // The same listing for a group page that holds nothing in it.
  assert.deepEqual(memberFolderAgentState(views, ROWS, 'beats'), {
    views: [],
    waits: {},
    lines: {},
    fast: false
  });
});

test('a member’s setup run is finished when its finish time is new for a member the page knew', () => {
  const running = raw('albums', { state: 'setting_up', setup: { status: 'running', lines: [] } });
  const finishedAt = at =>
    raw('albums', {
      state: 'set_up',
      setup: { status: 'done', lines: [], finished_at: at }
    });
  // The first read has no past, so a page load never replays an old run.
  const first = memberSetupsFinished(null, [finishedAt('2026-11-19T08:00:00Z')]);
  assert.equal(first.finished, false);
  assert.deepEqual(first.next, { albums: '2026-11-19T08:00:00Z' });
  // A run in progress, then done with a new finish time: finished, once.
  const during = memberSetupsFinished({}, [running]);
  assert.deepEqual(during.next, { albums: '' });
  assert.equal(during.finished, false);
  const after = memberSetupsFinished(during.next, [finishedAt('2026-11-19T09:00:00Z')]);
  assert.equal(after.finished, true);
  assert.deepEqual(after.next, { albums: '2026-11-19T09:00:00Z' });
  // The same finish time on the next read is not a second run.
  assert.equal(
    memberSetupsFinished(after.next, [finishedAt('2026-11-19T09:00:00Z')]).finished,
    false
  );
  // A run that stopped is not finished, a member first seen now is not, and
  // bad input is ignored.
  const stopped = raw('albums', { state: 'set_up', setup: { status: 'stopped', finished_at: '' } });
  assert.equal(memberSetupsFinished(during.next, [stopped]).finished, false);
  assert.equal(
    memberSetupsFinished({ other: '' }, [finishedAt('2026-11-19T09:00:00Z')]).finished,
    false,
    'a member the page had not seen is not a run it watched'
  );
  assert.deepEqual(memberSetupsFinished({ a: '' }, null), { finished: false, next: {} });
  assert.deepEqual(memberSetupsFinished(null, [null, {}]), { finished: false, next: {} });
});
