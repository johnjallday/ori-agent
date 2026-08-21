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
  renderContent
} from './home-daily-brief.js';

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
