import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  hqCardView,
  hqHeadline,
  hqReceiptRows,
  hqReviewDraft,
  hqReviewSummary
} from './personal-assistant-hq-card.js';
import {
  hqBuildRequestPayload,
  hqBuildTarget,
  defaultHQTimezone
} from './personal-hq-onboarding.js';
import { hqNotesReviewView, hqNotesPayload } from './personal-hq-notes-review.js';
import { readFileSync } from 'node:fs';

test('the HQ review has a short named title', () => {
  assert.equal(hqHeadline('Atlas'), 'A home base for Atlas');
  assert.equal(hqHeadline(''), 'A home base for Your assistant');
});

test('review markup keeps consent visible and labels the confirmation boundary', () => {
  const markup = readFileSync(
    new URL('../../../templates/components/personal-assistant-today.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(markup, /<h4 id="personalAssistantHQHeadline"/);
  assert.match(markup, />Build my HQ<\/button>/);
  assert.match(markup, />Customize setup<\/button>/);
  for (const id of ['RootPath', 'BriefSummary', 'Scope', 'NotesPath', 'NotesScope']) {
    assert.ok(markup.includes(`id="personalAssistantHQ${id}"`));
  }
  assert.ok(
    markup.indexOf('id="personalAssistantHQNotesScope"') <
      markup.indexOf('<details id="personalAssistantHQNotesOptions"')
  );
});

test('customized review preserves every schedule field and does not copy presentation state', () => {
  const values = {
    name: 'Long custom HQ',
    timezone: 'Pacific/Auckland',
    schedule_days: ['sat', 'sun'],
    schedule_time: '15:45',
    scope: 'selected',
    include_future_workspaces: false,
    notify_on_ready: true
  };
  const draft = hqReviewDraft({ ...values, receipt: ['not a request field'] });
  assert.deepEqual(draft, values);
  assert.notEqual(draft.schedule_days, values.schedule_days);
  assert.deepEqual(hqReviewDraft(draft), values);
  const summary = hqReviewSummary(draft);
  assert.match(summary.schedule, /sat, sun · 15:45 · Pacific\/Auckland/);
  assert.match(summary.scope, /Selected workspaces.*future workspaces are not included/);
  assert.match(summary.notification, /Notify in Action Center/);
  for (const [status, choice, expected] of [
    [{ setup_default: { path: '/notes' } }, {}, { include: true, witness: '/notes' }],
    [{ setup_default: { path: '/notes' } }, { include: false }, { include: false }],
    [{ source: { path: '/mine', origin: 'connected' } }, {}, { include: false }],
    [
      {},
      { chosen: { path: '/chosen', name: 'Notes', selectionToken: 'token' } },
      { include: true, selection_token: 'token', name: 'Notes' }
    ]
  ]) {
    const payload = hqBuildRequestPayload(
      hqBuildTarget({ state: 'needs_hq', state_version: 3 }),
      {
        ...draft,
        notes: hqNotesPayload(hqNotesReviewView(status, choice))
      },
      'same-request'
    );
    assert.deepEqual(payload, {
      ...values,
      notes: expected,
      request_id: 'same-request',
      if_version: 3
    });
  }
  assert.equal(
    'notes' in hqBuildRequestPayload(hqBuildTarget({ state: 'needs_hq' }), draft, 'standalone'),
    false
  );
});

test('review and alternate form share profile timezone defaults', async () => {
  assert.equal(
    await defaultHQTimezone(async () => ({
      ok: true,
      json: async () => ({ profile: { timezone: 'Asia/Tokyo' } })
    })),
    'Asia/Tokyo'
  );
  assert.ok(
    await defaultHQTimezone(async () => {
      throw new Error('offline');
    })
  );
});

const assistant = state => ({ state, display_name: '<Atlas>' });
const root = { workspace_root: '/workspaces', confirmed: true };

test('HQ proposal shows a recoverable paused setup, or its receipt', () => {
  assert.equal(hqCardView(assistant('needs_hire'), root).visible, false);
  assert.equal(hqCardView(assistant('needs_hq'), root).visible, true);
  const paused = hqCardView(assistant('provisioning_hq'), root);
  assert.equal(paused.paused, true);
  assert.equal(paused.building, false);
  assert.equal(paused.canBuild, true);
  assert.equal(hqCardView(assistant('active'), root).visible, false);
  assert.equal(hqCardView(assistant('active'), root, { receipt: [{}] }).visible, true);
});

test('a new build needs the confirmed root; a paused setup can resume without it', () => {
  const missing = hqCardView(assistant('needs_hq'), { workspace_root: '/suggested' });
  assert.equal(missing.canBuild, false);
  assert.match(missing.status, /Confirm this directory/);
  assert.equal(hqCardView(assistant('needs_hq'), root).canBuild, true);
  assert.equal(hqCardView(assistant('provisioning_hq'), null).canBuild, true);
  assert.equal(hqCardView(assistant('provisioning_hq'), root, { busy: true }).canBuild, false);
  assert.equal(hqCardView(assistant('provisioning_hq'), root, { failed: true }).canBuild, true);
});

test('Not now collapses to a one-line Build My HQ action', () => {
  const view = hqCardView(assistant('needs_hq'), root, { collapsed: true });
  assert.equal(view.collapsed, true);
  assert.equal(view.visible, true);
  assert.equal(view.name, '<Atlas>', 'the DOM must insert the name as text, not markup');
});

test('HQ receipt uses only canonical workspace facts, schedule and selected directory', () => {
  assert.deepEqual(
    hqReceiptRows(
      { valid: true, workspace: { name: 'My HQ', folder_slug: 'my hq' } },
      { time: '08:00' },
      '/workspaces'
    ),
    [
      { kind: 'workspace', name: 'My HQ', detail: '', route: '/workspaces/my%20hq' },
      { kind: 'schedule', name: 'Daily Brief at 08:00', detail: '' },
      { kind: 'directory', name: '/workspaces', detail: '' }
    ]
  );
  assert.deepEqual(
    hqReceiptRows(
      { valid: true, workspace: { name: 'My HQ', folder_slug: 'my hq' } },
      { time: '09:30' },
      ''
    ),
    [
      { kind: 'workspace', name: 'My HQ', detail: '', route: '/workspaces/my%20hq' },
      { kind: 'schedule', name: 'Daily Brief at 09:30', detail: '' }
    ],
    'reloaded receipts must not misidentify the current workspace root as the setup directory'
  );
  assert.deepEqual(hqReceiptRows({ valid: false }, {}, ''), []);
});
