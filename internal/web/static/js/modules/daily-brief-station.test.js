import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DAILY_BRIEF_STATION,
  briefPanelMeta,
  dailyBriefStationLink,
  dailyBriefStationState,
  dailyBriefStatus,
  isDailyBriefStationLink,
  nextBriefDue,
  dailyBriefRowStatus,
  scheduleClock,
  stationFromSearch
} from './daily-brief-station.js';

// Wednesday 2026-10-07, 10:30 UTC.
const NOW = new Date('2026-10-07T10:30:00Z');
const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri'];
const config = overrides => ({
  timezone: 'UTC',
  schedule_enabled: true,
  schedule_time: '08:00',
  schedule_days: WEEKDAYS,
  ...overrides
});
const revision = overrides => ({
  id: 'rev-1',
  local_date: '2026-10-07',
  generated_at: '2026-10-07T08:02:00Z',
  ...overrides
});

test('dailyBriefStationLink builds the one station address and refuses an unusable slug', () => {
  assert.equal(dailyBriefStationLink('my-hq'), '/workspaces/my-hq?station=daily-brief');
  assert.equal(dailyBriefStationLink(' my-hq '), '/workspaces/my-hq?station=daily-brief');
  for (const bad of ['', null, 'My HQ', '../etc', 'a/b', 'a?b', '-lead']) {
    assert.equal(dailyBriefStationLink(bad), '', String(bad));
  }
});

test('stationFromSearch returns only a station this page knows', () => {
  assert.equal(stationFromSearch('?station=daily-brief'), DAILY_BRIEF_STATION);
  assert.equal(stationFromSearch('?mode=map&station=daily-brief'), DAILY_BRIEF_STATION);
  assert.equal(stationFromSearch('?station=watchtower'), '');
  assert.equal(stationFromSearch('?station='), '');
  assert.equal(stationFromSearch(''), '');
  assert.equal(stationFromSearch(undefined), '');
});

test('isDailyBriefStationLink accepts exactly the station link and nothing broader', () => {
  assert.equal(isDailyBriefStationLink('/workspaces/my-hq?station=daily-brief'), true);
  assert.equal(isDailyBriefStationLink(dailyBriefStationLink('hq-2')), true);
  for (const bad of [
    '/workspaces/my-hq',
    '/workspaces/my-hq?station=watchtower',
    '/workspaces/my-hq?station=daily-brief&next=//evil.example',
    '/workspaces/my-hq/assistant?station=daily-brief',
    '/workspaces/My HQ?station=daily-brief',
    'https://evil.example/workspaces/my-hq?station=daily-brief',
    '//evil.example/workspaces/my-hq?station=daily-brief',
    '/workspaces/my-hq?station=daily-brief#x',
    ''
  ]) {
    assert.equal(isDailyBriefStationLink(bad), false, bad);
  }
});

test('scheduleClock renders a 24-hour schedule time and rejects anything else', () => {
  assert.match(scheduleClock('08:00'), /^8:00\sAM$/);
  assert.match(scheduleClock('17:45'), /^5:45\sPM$/);
  assert.equal(scheduleClock('25:00'), '');
  assert.equal(scheduleClock('soon'), '');
  assert.equal(scheduleClock(''), '');
});

test('nextBriefDue names today, tomorrow, or the next scheduled weekday', () => {
  // 10:30 on a Wednesday: today's 08:00 has passed.
  assert.match(nextBriefDue(config(), NOW), /^tomorrow at 8:00\sAM$/);
  assert.match(nextBriefDue(config({ schedule_time: '17:00' }), NOW), /^today at 5:00\sPM$/);
  // Friday afternoon skips the weekend.
  assert.match(nextBriefDue(config(), new Date('2026-10-09T15:00:00Z')), /^Mon at 8:00\sAM$/);
  // The schedule is read in the brief's own time zone: 10:30 UTC is 06:30 in
  // New York, so today's 08:00 is still ahead there.
  assert.match(nextBriefDue(config({ timezone: 'America/New_York' }), NOW), /^today at 8:00\sAM$/);
  // Only one scheduled day, and it is today but already past: a week away.
  assert.match(nextBriefDue(config({ schedule_days: ['wed'] }), NOW), /^Wed at 8:00\sAM$/);
});

test('nextBriefDue is empty when nothing is scheduled', () => {
  assert.equal(nextBriefDue(null, NOW), '');
  assert.equal(nextBriefDue(config({ schedule_enabled: false }), NOW), '');
  assert.equal(nextBriefDue(config({ schedule_days: [] }), NOW), '');
  assert.equal(nextBriefDue(config({ schedule_time: '' }), NOW), '');
  assert.equal(nextBriefDue(config({ timezone: 'Not/AZone' }), NOW), '');
});

test('the station says Preparing while a brief is being generated', () => {
  for (const generation of ['pending', 'running']) {
    const state = dailyBriefStationState(
      dailyBriefStatus({ generation, revision: revision(), config: config(), now: NOW })
    );
    assert.equal(state.value, 'Preparing…');
    assert.equal(state.tone, 'loading');
  }
});

test('the station says Failed when the latest generation failed, brief or no brief', () => {
  for (const current of [revision(), null]) {
    const state = dailyBriefStationState(
      dailyBriefStatus({ generation: 'failed', revision: current, config: config(), now: NOW })
    );
    assert.equal(state.value, 'Failed');
    assert.equal(state.tone, 'degraded');
  }
});

test('the station says Ready with the time today’s brief was generated', () => {
  for (const generation of ['idle', 'succeeded', 'partial', '']) {
    const state = dailyBriefStationState(
      dailyBriefStatus({ generation, revision: revision(), config: config(), now: NOW })
    );
    assert.match(state.value, /^Ready · 8:02\sAM$/);
    assert.equal(state.tone, 'clear');
  }
  // The time is the brief's own zone, not the viewer's.
  const eastern = dailyBriefStationState(
    dailyBriefStatus({
      generation: 'idle',
      revision: revision({ generated_at: '2026-10-07T12:02:00Z' }),
      config: config({ timezone: 'America/New_York' }),
      now: new Date('2026-10-07T15:00:00Z')
    })
  );
  assert.match(eastern.value, /^Ready · 8:02\sAM$/);
});

test('today’s brief stays Ready even when check-ins are paused or unscheduled', () => {
  const paused = dailyBriefStatus({
    revision: revision(),
    config: config(),
    paused: true,
    now: NOW
  });
  assert.equal(paused.kind, 'ready');
  const unscheduled = dailyBriefStatus({
    revision: revision(),
    config: config({ schedule_enabled: false }),
    now: NOW
  });
  assert.equal(unscheduled.kind, 'ready');
});

test('without a brief for today the station says paused before unscheduled', () => {
  const earlier = revision({ local_date: '2026-10-06', generated_at: '2026-10-06T08:02:00Z' });
  assert.deepEqual(
    dailyBriefStationState(
      dailyBriefStatus({ revision: earlier, config: config(), paused: true, now: NOW })
    ),
    { value: 'Check-ins paused', description: 'check-ins are paused', tone: '' }
  );
  assert.equal(
    dailyBriefStationState(
      dailyBriefStatus({
        revision: null,
        config: config({ schedule_enabled: false }),
        paused: true,
        now: NOW
      })
    ).value,
    'Check-ins paused'
  );
  assert.equal(
    dailyBriefStationState(
      dailyBriefStatus({ revision: earlier, config: config({ schedule_enabled: false }), now: NOW })
    ).value,
    'Not scheduled'
  );
  assert.equal(
    dailyBriefStationState(dailyBriefStatus({ revision: null, config: null, now: NOW })).value,
    'Not scheduled'
  );
});

test('an earlier brief with a live schedule is Ready with its date, not a time', () => {
  const state = dailyBriefStationState(
    dailyBriefStatus({
      generation: 'idle',
      revision: revision({ local_date: '2026-10-06', generated_at: '2026-10-06T08:02:00Z' }),
      config: config(),
      now: NOW
    })
  );
  assert.equal(state.value, 'Ready · Oct 6');
  assert.equal(state.tone, '');
});

test('a scheduled HQ with no brief yet says so and names when the first is due', () => {
  const state = dailyBriefStationState(
    dailyBriefStatus({ generation: 'idle', revision: null, config: config(), now: NOW })
  );
  assert.equal(state.value, 'No brief yet');
  assert.match(state.description, /^first Daily Brief due tomorrow at 8:00\sAM$/);
});

test('briefPanelMeta says when the brief was generated and when the next is due', () => {
  assert.match(
    briefPanelMeta({ revision: revision(), config: config(), now: NOW }),
    /^Generated at 8:02\sAM\. Next brief tomorrow at 8:00\sAM\.$/
  );
  assert.match(
    briefPanelMeta({
      revision: revision({ local_date: '2026-10-06', generated_at: '2026-10-06T08:02:00Z' }),
      config: config(),
      now: NOW
    }),
    /^Generated Oct 6 at 8:02\sAM\. Next brief tomorrow at 8:00\sAM\.$/
  );
  assert.match(
    briefPanelMeta({ revision: revision(), config: config({ schedule_enabled: false }), now: NOW }),
    /^Generated at 8:02\sAM\. No brief scheduled\.$/
  );
  assert.match(
    briefPanelMeta({ revision: revision(), config: config(), paused: true, now: NOW }),
    /^Generated at 8:02\sAM\. Check-ins paused\.$/
  );
  assert.match(
    briefPanelMeta({ revision: null, config: config(), now: NOW }),
    /^Next brief tomorrow at 8:00\sAM\.$/
  );
});

test('an earlier brief says only when it was generated, never when the next is due', () => {
  const earlier = revision({ local_date: '2026-10-05', generated_at: '2026-10-05T08:02:00Z' });
  assert.match(
    briefPanelMeta({ revision: earlier, config: config(), earlier: true, now: NOW }),
    /^Generated Oct 5 at 8:02\sAM\.$/
  );
  // Paused check-ins are today's news, not an old brief's.
  assert.match(
    briefPanelMeta({ revision: earlier, config: config(), earlier: true, paused: true, now: NOW }),
    /^Generated Oct 5 at 8:02\sAM\.$/
  );
});

// What the assistant drawer's "Today's brief" row says after its label.
test('the brief row says ready since when, being prepared, or that it could not be generated', () => {
  const row = input =>
    dailyBriefRowStatus(dailyBriefStatus({ config: config(), now: NOW, ...input }));
  assert.match(row({ generation: 'succeeded', revision: revision() }), /^ready since 8:02\sAM$/);
  assert.equal(row({ generation: 'running', revision: revision() }), 'being prepared');
  assert.equal(row({ generation: 'pending', revision: null }), 'being prepared');
  assert.equal(row({ generation: 'failed', revision: revision() }), 'couldn’t be generated');
  assert.equal(row({ generation: 'failed', revision: null }), 'couldn’t be generated');
});

test('with no brief yet the row says when the first one is due', () => {
  assert.match(
    dailyBriefRowStatus(
      dailyBriefStatus({ generation: 'idle', revision: null, config: config(), now: NOW })
    ),
    /^first one due tomorrow at 8:00\sAM$/
  );
  // A schedule that names no future time still says there is no brief.
  assert.equal(dailyBriefRowStatus({ kind: 'due', due: '' }), 'no brief yet');
  assert.equal(dailyBriefRowStatus(null), 'no brief yet');
});

test('the brief row names the other states too: an earlier brief, paused, unscheduled', () => {
  const earlier = revision({ local_date: '2026-10-06', generated_at: '2026-10-06T08:02:00Z' });
  assert.equal(
    dailyBriefRowStatus(dailyBriefStatus({ revision: earlier, config: config(), now: NOW })),
    'ready since Oct 6'
  );
  assert.equal(
    dailyBriefRowStatus(
      dailyBriefStatus({ revision: earlier, config: config(), paused: true, now: NOW })
    ),
    'check-ins paused'
  );
  assert.equal(
    dailyBriefRowStatus(
      dailyBriefStatus({ revision: null, config: config({ schedule_enabled: false }), now: NOW })
    ),
    'not scheduled'
  );
  assert.equal(dailyBriefRowStatus({ kind: 'ready', today: true, time: '' }), 'ready');
});
