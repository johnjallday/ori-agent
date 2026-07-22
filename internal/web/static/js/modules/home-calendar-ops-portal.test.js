import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  escapeHtml,
  setupStateLabel,
  computePortalView,
  nextMeetingTimeLabel,
  renderBodyHTML
} from './home-calendar-ops-portal.js';

test('computePortalView reports no_workspace when has_workspace is false', () => {
  assert.deepEqual(computePortalView({ has_workspace: false }), { kind: 'no_workspace' });
  assert.deepEqual(computePortalView(null), { kind: 'no_workspace' });
});

test('computePortalView reports needs_setup for any non-ready state', () => {
  const view = computePortalView({
    has_workspace: true,
    workspace_id: 'ws-1',
    state: 'auth_required'
  });
  assert.deepEqual(view, { kind: 'needs_setup', state: 'auth_required', workspaceId: 'ws-1' });
});

test('computePortalView reports ready with events/conflicts/next meeting/data gap', () => {
  const view = computePortalView({
    has_workspace: true,
    workspace_id: 'ws-1',
    state: 'ready',
    next_meeting: { id: 'evt-1', title: 'Sync' },
    event_count: 3,
    conflict_count: 1,
    data_gap: true
  });
  assert.equal(view.kind, 'ready');
  assert.equal(view.workspaceId, 'ws-1');
  assert.equal(view.eventCount, 3);
  assert.equal(view.conflictCount, 1);
  assert.equal(view.dataGap, true);
  assert.deepEqual(view.nextMeeting, { id: 'evt-1', title: 'Sync' });
});

test('setupStateLabel has a friendly label for every known setup state and a safe default', () => {
  assert.equal(setupStateLabel('connector_missing'), 'Calendar Ops needs a connector.');
  assert.equal(setupStateLabel('auth_required'), 'Calendar Ops needs to be reconnected.');
  assert.equal(setupStateLabel('unknown_future_state'), 'Calendar Ops needs attention.');
});

test('nextMeetingTimeLabel formats a valid start_time and degrades to empty for garbage', () => {
  assert.equal(nextMeetingTimeLabel({ start_time: 'not-a-date' }), '');
  assert.equal(nextMeetingTimeLabel(null), '');
  assert.equal(nextMeetingTimeLabel({}), '');
  // A valid ISO timestamp formats to a non-empty, locale time string.
  const label = nextMeetingTimeLabel({ start_time: '2026-07-20T14:30:00Z' });
  assert.ok(label.length > 0, `expected a formatted time label, got ${JSON.stringify(label)}`);
});

test('renderBodyHTML for no_workspace offers a setup CTA', () => {
  const html = renderBodyHTML({ kind: 'no_workspace' });
  assert.match(html, /data-role="setup"/);
  assert.match(html, /Set up Calendar Ops/);
});

test('renderBodyHTML for needs_setup shows the state label and a finish-setup CTA', () => {
  const html = renderBodyHTML({ kind: 'needs_setup', state: 'mapping_required' });
  assert.match(html, /Calendar Ops setup is unfinished\./);
  assert.match(html, /data-role="finish-setup"/);
});

test('renderBodyHTML for ready with no next meeting says so plainly', () => {
  const html = renderBodyHTML({
    kind: 'ready',
    eventCount: 0,
    conflictCount: 0,
    nextMeeting: null
  });
  assert.match(html, /No more meetings today/);
  assert.match(html, /0 events today/);
  assert.doesNotMatch(html, /conflict/);
});

test('renderBodyHTML for ready with a next meeting and conflicts renders both, HTML-escaped', () => {
  const html = renderBodyHTML({
    kind: 'ready',
    eventCount: 1,
    conflictCount: 2,
    nextMeeting: { title: '<script>alert(1)</script>', start_time: '2026-07-20T14:30:00Z' },
    dataGap: false
  });
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /1 event today/);
  assert.match(html, /2 conflicts/);
});

test('renderBodyHTML for ready with a data gap includes a partial-data banner', () => {
  const html = renderBodyHTML({
    kind: 'ready',
    eventCount: 1,
    conflictCount: 0,
    nextMeeting: null,
    dataGap: true
  });
  assert.match(html, /Some calendars could not be read\./);
});

test('escapeHtml neutralizes every HTML-significant character', () => {
  assert.equal(
    escapeHtml(`<a href="x">'&'</a>`),
    '&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;'
  );
  assert.equal(escapeHtml(null), '');
});
