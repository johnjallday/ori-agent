import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseContent,
  hrefForRef,
  humanizeReason,
  localDateInZone,
  formatMeta,
  computeBanner,
  isQuietDay,
  renderContent,
  formatMeetingTime,
  meetingPrepText
} from './home-daily-brief.js';

test('Daily Brief has one stable Today mount and no Updates copy', () => {
  const todayTemplate = readFileSync(
    new URL('../../../templates/components/personal-assistant-today.tmpl', import.meta.url),
    'utf8'
  );
  const dashboardTemplate = readFileSync(
    new URL('../../../templates/components/dashboard.tmpl', import.meta.url),
    'utf8'
  );
  assert.equal((todayTemplate.match(/id="homeDailyBrief"/g) || []).length, 1);
  assert.equal((dashboardTemplate.match(/id="homeDailyBrief"/g) || []).length, 0);
  assert.doesNotMatch(todayTemplate, /personalAssistantTodayBriefMount|moveDailyBrief/);
});

test('parseContent decodes a revision content_json, degrading to {} on garbage', () => {
  assert.deepEqual(parseContent({ content_json: '{"opening_summary":"hi"}' }), {
    opening_summary: 'hi'
  });
  assert.deepEqual(parseContent({ content_json: 'not json' }), {});
  assert.deepEqual(parseContent({ content_json: '' }), {});
  assert.deepEqual(parseContent(null), {});
  assert.deepEqual(parseContent({ content_json: '"just a string"' }), {});
});

test('hrefForRef routes tasks to their deep-link page and everything else to the owning workspace', () => {
  assert.equal(
    hrefForRef({
      workspace_id: 'workspace-uuid',
      workspace_slug: 'marketing-site',
      entity_type: 'task',
      entity_id: 't-1'
    }),
    '/workspaces/marketing-site/task/t-1'
  );
  assert.equal(
    hrefForRef({
      workspace_id: 'workspace-uuid',
      workspace_slug: 'marketing-site',
      entity_type: 'session',
      entity_id: 's-1'
    }),
    '/workspaces/marketing-site'
  );
  assert.equal(
    hrefForRef({
      workspace_id: 'workspace-uuid',
      workspace_slug: 'marketing-site',
      entity_type: 'scheduled_task',
      entity_id: 'sc-1'
    }),
    '/workspaces/marketing-site'
  );
  assert.equal(
    hrefForRef({
      workspace_id: 'workspace-uuid',
      workspace_slug: 'ws 1',
      entity_type: 'task',
      entity_id: 't/1'
    }),
    '/workspaces/ws%201/task/t%2F1'
  );
});

test('hrefForRef routes follow-ups to the exact record in their owning workspace', () => {
  assert.equal(
    hrefForRef({
      workspace_id: 'email-workspace-id',
      workspace_slug: 'email-ops',
      entity_type: 'follow_up',
      entity_id: 'follow-up_1'
    }),
    '/workspaces/email-ops?follow_up=follow-up_1'
  );
  for (const ref of [
    { workspace_slug: 'email-ops', entity_type: 'follow_up', entity_id: 'follow-1' },
    { workspace_id: 'email', entity_type: 'follow_up', entity_id: 'follow-1' },
    {
      workspace_id: 'email',
      workspace_slug: '../email',
      entity_type: 'follow_up',
      entity_id: 'follow-1'
    },
    {
      workspace_id: 'email',
      workspace_slug: 'email ops',
      entity_type: 'follow_up',
      entity_id: 'follow-1'
    },
    { workspace_id: 'email', workspace_slug: 'email-ops', entity_type: 'follow_up' },
    {
      workspace_id: 'email',
      workspace_slug: 'email-ops',
      entity_type: 'follow_up',
      entity_id: '../follow'
    }
  ]) {
    assert.equal(hrefForRef(ref), '#', JSON.stringify(ref));
  }
});

test('hrefForRef falls back to # for a ref with no workspace', () => {
  assert.equal(hrefForRef(null), '#');
  assert.equal(hrefForRef({}), '#');
});

test('hrefForRef opens an email thread in Gmail by its thread id (no token, fixed host)', () => {
  const href = hrefForRef({ entity_type: 'email_thread', entity_id: 'abc123' });
  assert.equal(href, 'https://mail.google.com/mail/u/0/#all/abc123');
  // Email refs need no workspace_id and must not fall through to '#'.
  assert.notEqual(href, '#');
  // The thread id is URL-encoded (no arbitrary-destination injection).
  assert.equal(
    hrefForRef({ entity_type: 'email_thread', entity_id: 'a/b#c' }),
    'https://mail.google.com/mail/u/0/#all/a%2Fb%23c'
  );
});

test('humanizeReason maps email reasons to friendly labels and passes others through', () => {
  assert.equal(humanizeReason('email_waiting_on_user'), 'Waiting on your reply');
  assert.equal(humanizeReason('email_unread'), 'Unread email');
  assert.equal(humanizeReason('waiting_for_choice'), 'Waiting for your choice');
  assert.equal(humanizeReason('future_status'), 'Future status');
  assert.equal(
    humanizeReason('This is a model-written sentence.'),
    'This is a model-written sentence.'
  );
  assert.equal(humanizeReason('failed'), 'failed');
  assert.equal(humanizeReason(undefined), '');
});

test('localDateInZone matches the server LocalDateKey convention (YYYY-MM-DD) for a fixed instant', () => {
  const instant = new Date('2026-07-14T02:30:00Z');
  assert.equal(localDateInZone('UTC', instant), '2026-07-14');
  // America/New_York is UTC-4 in July (EDT): 02:30 UTC is still the 13th locally.
  assert.equal(localDateInZone('America/New_York', instant), '2026-07-13');
});

test('formatMeta always includes the timezone and never blanks for a real revision', () => {
  const revision = { generated_at: '2026-07-14T12:00:00Z', local_date: '2026-07-14' };
  const text = formatMeta(revision, { timezone: 'UTC' }, () => '2h ago');
  assert.match(text, /UTC/);
  assert.match(text, /2h ago/);
});

test('formatMeta returns empty string with no revision', () => {
  assert.equal(formatMeta(null, { timezone: 'UTC' }), '');
});

test('formatMeta flags a revision whose local_date is not today in its own timezone', () => {
  const yesterday = { generated_at: '2026-07-13T12:00:00Z', local_date: '2026-07-13' };
  const text = formatMeta(yesterday, { timezone: 'UTC' }, null);
  assert.match(text, /earlier day/);
});

test('computeBanner surfaces a failed-latest-attempt banner with retry, preserving the last successful revision', () => {
  const revision = { status: 'succeeded' };
  const banner = computeBanner(revision, { status: 'failed' });
  assert.equal(banner.kind, 'failed');
  assert.equal(banner.showRetry, true);
  assert.match(banner.text, /last successful/);
});

test('computeBanner reports full failure with no prior revision at all', () => {
  const banner = computeBanner(null, { status: 'failed' });
  assert.equal(banner.kind, 'failed');
  assert.doesNotMatch(banner.text, /last successful/);
});

test('computeBanner flags a partial revision without a retry action', () => {
  const banner = computeBanner({ status: 'partial' }, null);
  assert.equal(banner.kind, 'partial');
  assert.equal(banner.showRetry, false);
});

test('computeBanner flags a degraded (fallback) revision even when its status is succeeded', () => {
  const revision = { status: 'succeeded', content_json: JSON.stringify({ degraded: true }) };
  const banner = computeBanner(revision, null);
  assert.equal(banner.kind, 'degraded');
});

test('computeBanner is null for a clean successful revision with no active/failed claim', () => {
  const revision = { status: 'succeeded', content_json: JSON.stringify({ degraded: false }) };
  assert.equal(computeBanner(revision, null), null);
  assert.equal(computeBanner(revision, { status: 'succeeded' }), null);
});

test('isQuietDay is true only when every content-bearing section is empty', () => {
  assert.equal(isQuietDay({}), true);
  assert.equal(isQuietDay({ needs_attention: [] }), true);
  assert.equal(isQuietDay({ needs_attention: [{ title: 'x' }] }), false);
  assert.equal(isQuietDay({ suggested_actions: [{ label: 'x' }] }), false);
});

test('renderContent renders a quiet-day confirmation instead of empty section headers', () => {
  const html = renderContent({ opening_summary: 'A quiet day.' });
  assert.match(html, /A quiet day\./);
  assert.match(html, /Nothing else needs your attention/);
  assert.doesNotMatch(html, /Needs Attention/);
});

test('renderContent renders Needs Attention items with a real link built from the stable ref', () => {
  const html = renderContent({
    needs_attention: [
      {
        ref: {
          workspace_id: 'workspace-uuid',
          workspace_slug: 'marketing-site',
          entity_type: 'task',
          entity_id: 't-1'
        },
        title: 'Approve deploy',
        workspace_name: 'Ops',
        reason: 'Waiting on your approval.'
      }
    ]
  });
  assert.match(html, /href="\/workspaces\/marketing-site\/task\/t-1"/);
  assert.match(html, /Approve deploy/);
  assert.match(html, /Waiting on your approval\./);
});

test('renderContent visually distinguishes suggestion text (why_suggested\\/next_step) from facts', () => {
  const html = renderContent({
    todays_plan: [
      {
        ref: { workspace_id: 'workspace-uuid', workspace_slug: 'marketing-site' },
        title: 'Ship the release',
        workspace_name: 'Ops',
        reason: 'In progress',
        why_suggested: 'Blocks two dependents'
      }
    ]
  });
  assert.match(html, /is-suggestion">Blocks two dependents/);
});

test('renderContent surfaces data gaps distinctly and omits sections with no items', () => {
  const html = renderContent({
    opening_summary: 'Hi',
    data_gaps: ['workspace-x unavailable'],
    needs_attention: [{ ref: {}, title: 'A', workspace_name: 'W', reason: 'R' }]
  });
  assert.match(html, /Data gaps: workspace-x unavailable/);
  assert.match(html, /Needs Attention/);
  assert.doesNotMatch(html, /Since Last Brief/);
});

test('renderContent renders suggested actions as real links carrying their ref, not the label alone', () => {
  const html = renderContent({
    suggested_actions: [
      {
        ref: {
          workspace_id: 'workspace-uuid-2',
          workspace_slug: 'release-ops',
          entity_type: 'task',
          entity_id: 't-9'
        },
        label: 'Retry the failed run',
        action_type: 'retry'
      }
    ]
  });
  assert.match(html, /href="\/workspaces\/release-ops\/task\/t-9"/);
  assert.match(html, /Retry the failed run/);
  assert.match(html, /data-action-type="retry"/);
});

test('renderContent escapes untrusted title/reason text', () => {
  const html = renderContent({
    needs_attention: [
      { ref: {}, title: '<script>alert(1)</script>', workspace_name: 'W', reason: 'x' }
    ]
  });
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /&lt;script&gt;/);
});

// --- Today's meetings (Issue #533) -------------------------------------------

const meetingRef = (id, extra = {}) => ({
  workspace_id: 'cal-ws',
  workspace_slug: 'calendar-ops',
  entity_type: 'calendar_event',
  entity_id: id,
  calendar_id: 'primary',
  ...extra
});

test('hrefForRef opens a meeting in its Calendar Ops console with the drawer deep link', () => {
  assert.equal(
    hrefForRef(meetingRef('design-review')),
    '/workspaces/calendar-ops?panel=calendar&event=design-review&calendar=primary'
  );
  assert.equal(
    hrefForRef(meetingRef('evt 1', { calendar_id: 'team@group.calendar.google.com' })),
    '/workspaces/calendar-ops?panel=calendar&event=evt+1&calendar=team%40group.calendar.google.com'
  );
  assert.equal(
    hrefForRef(meetingRef('design', { calendar_id: '' })),
    '/workspaces/calendar-ops?panel=calendar&event=design'
  );
  for (const bad of [
    meetingRef(''),
    meetingRef('x', { workspace_slug: '' }),
    meetingRef('x', { workspace_slug: '../evil' }),
    meetingRef('x\nInjected'),
    meetingRef('x'.repeat(600))
  ]) {
    assert.equal(hrefForRef(bad), '#', JSON.stringify(bad).slice(0, 80));
  }
});

test('humanizeReason names the calendar attention reasons', () => {
  assert.equal(humanizeReason('calendar_conflict'), 'Overlaps another meeting');
  assert.equal(humanizeReason('meeting_needs_prep'), 'No prep note yet');
});

test('isQuietDay counts today_meetings as content', () => {
  assert.equal(isQuietDay({ todays_meetings: [{ ref: meetingRef('a') }] }), false);
  assert.equal(isQuietDay({ todays_meetings: [] }), true);
});

test('formatMeetingTime renders a range in the brief timezone, or All day', () => {
  const item = { start_time: '2026-09-24T02:00:00Z', end_time: '2026-09-24T02:30:00Z' };
  const seoul = formatMeetingTime(item, 'Asia/Seoul');
  assert.match(seoul, /^11:00\sAM – 11:30\sAM$/);
  assert.equal(formatMeetingTime({ all_day: true }, 'UTC'), 'All day');
  assert.equal(formatMeetingTime({ start_time: 'garbage' }, 'UTC'), '');
});

test('meetingPrepText states prep facts and says nothing for private or all-day meetings', () => {
  assert.equal(meetingPrepText({ prep_status: 'ready' }), 'Prep note ready');
  assert.equal(meetingPrepText({ prep_status: 'pending' }), 'Preparing…');
  assert.equal(meetingPrepText({ prep_status: 'stale' }), 'Prep note is out of date');
  assert.equal(meetingPrepText({ prep_status: 'failed' }), 'Prep did not finish — try again');
  assert.equal(meetingPrepText({}), 'No prep note yet');
  assert.equal(meetingPrepText({ private: true }), '');
  assert.equal(meetingPrepText({ all_day: true }), '');
});

test("renderContent leads with Today's Meetings: badges, prep, suggestion, and a private meeting unnamed", () => {
  const html = renderContent(
    {
      calendar_connected: true,
      todays_meetings: [
        {
          ref: meetingRef('design'),
          title: 'Design <b>review</b>',
          start_time: '2026-09-24T02:00:00Z',
          end_time: '2026-09-24T03:00:00Z',
          location: 'Room 4',
          conflict: true,
          why_prepare: 'Bring the setup notes.'
        },
        {
          ref: meetingRef('lunch'),
          title: 'Team lunch',
          start_time: '2026-09-24T03:30:00Z',
          end_time: '2026-09-24T04:30:00Z',
          back_to_back: true,
          prep_status: 'ready'
        },
        {
          ref: meetingRef('private'),
          title: 'Dentist',
          location: 'Clinic',
          private: true,
          start_time: '2026-09-24T05:00:00Z',
          end_time: '2026-09-24T06:00:00Z',
          why_prepare: 'Should never render.'
        }
      ],
      needs_attention: [
        {
          ref: meetingRef('design'),
          title: 'Design review',
          workspace_name: 'Calendar',
          reason: 'calendar_conflict'
        }
      ]
    },
    { timeZone: 'Asia/Seoul' }
  );
  assert.ok(html.indexOf("Today's Meetings") < html.indexOf('Needs Attention'));
  assert.match(html, /Design &lt;b&gt;review&lt;\/b&gt;/);
  assert.match(html, /data-state="conflict">Overlaps</);
  assert.match(html, /data-state="back_to_back">Back-to-back</);
  assert.match(html, /11:00\sAM – 12:00\sPM · Room 4/);
  assert.match(html, /class="home-daily-brief-item-why is-suggestion">Bring the setup notes\./);
  assert.match(html, /Prep note ready/);
  assert.match(html, /Private event/);
  assert.doesNotMatch(html, /Dentist|Clinic|Should never render/);
  assert.match(html, /Overlaps another meeting/);
  assert.match(
    html,
    /href="\/workspaces\/calendar-ops\?panel=calendar&event=design&calendar=primary"/
  );
});

test("renderContent says how many of today's meetings are not listed", () => {
  const html = renderContent({
    calendar_connected: true,
    todays_meetings_more: 4,
    todays_meetings: [{ ref: meetingRef('a'), title: 'A' }]
  });
  assert.match(html, /4 more meetings today\.<\/p><\/section>/);
  assert.doesNotMatch(
    renderContent({
      calendar_connected: true,
      todays_meetings: [{ ref: meetingRef('a'), title: 'A' }]
    }),
    /more meeting/
  );
});

test('a meeting id cannot break out of the link attribute', () => {
  const html = renderContent({
    calendar_connected: true,
    todays_meetings: [{ ref: meetingRef('x" onmouseover="alert(1)'), title: 'T' }]
  });
  assert.doesNotMatch(html, /onmouseover="/);
  assert.match(html, /event=x%22\+onmouseover%3D%22alert%281%29/);
});

test('renderContent says No meetings today only when a calendar was read', () => {
  const connected = renderContent({ calendar_connected: true, todays_meetings: [] });
  assert.match(connected, /Today's Meetings/);
  assert.match(connected, /No meetings today\./);
  const none = renderContent({});
  assert.doesNotMatch(none, /Meetings|meetings today/);
});
