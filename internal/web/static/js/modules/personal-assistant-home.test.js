import test from 'node:test';
import assert from 'node:assert/strict';

import {
  personalAssistantTodayView,
  safeTodayRoute,
  studioSectionView,
  todaySectionRows
} from './personal-assistant-home.js';

test('Today view distinguishes active, paused, partial, no-model, empty, and fatal states', () => {
  assert.equal(personalAssistantTodayView({ state: 'active' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'paused' }).paused, true);
  assert.equal(personalAssistantTodayView({ state: 'partial' }).partial, true);
  assert.equal(personalAssistantTodayView({ state: 'model_unavailable' }).modelUnavailable, true);
  assert.equal(personalAssistantTodayView({ state: 'healthy_empty' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'unavailable' }).unavailable, true);
});

test('Today section never turns unavailable into a healthy empty all-clear', () => {
  assert.match(
    todaySectionRows({ health: { status: 'unavailable' }, items: [] })[0].title,
    /unavailable/
  );
  assert.match(
    todaySectionRows({ health: { status: 'healthy_empty' }, items: [] })[0].title,
    /Nothing here/
  );
});

test('Today section caps records and only preserves server-owned internal routes', () => {
  const items = Array.from({ length: 15 }, (_, index) => ({
    kind: 'ticket',
    title: `Ticket ${index}`,
    route: `/workspaces/personal-hq?ticket=${index}`
  }));
  const rows = todaySectionRows({ health: { status: 'available' }, items });
  assert.equal(rows.length, 10);
  assert.ok(rows.every(row => safeTodayRoute(row.route)));

  for (const route of [
    'https://evil.example',
    '//evil.example',
    'javascript:alert(1)',
    'workspaces/x'
  ]) {
    assert.equal(safeTodayRoute(route), false, route);
  }
});

test('Today rows carry who did the work, by name, only when the record says so', () => {
  const rows = todaySectionRows({
    health: { status: 'available' },
    items: [
      { title: 'Rough mix of Ivory', attribution: 'Reaper Producer', route: '/workspaces/ivory' },
      { title: 'Archived the stems', route: '/workspaces/ivory' }
    ]
  });
  assert.equal(rows[0].attribution, 'Reaper Producer');
  assert.equal(rows[1].attribution, '');
});

test('the studio section is absent unless the server reports one', () => {
  for (const studio of [null, undefined]) {
    assert.equal(studioSectionView(studio).visible, false);
  }
});

test('the studio section reports and points at the specialist without claiming to direct it', () => {
  const view = studioSectionView({
    health: { status: 'available' },
    domain: 'music projects',
    specialist_name: 'Reaper Producer',
    workspace_name: 'Ivory',
    route: '/workspaces/ivory',
    items: [{ title: 'Rough mix', attribution: 'Reaper Producer' }]
  });

  assert.equal(view.visible, true);
  assert.equal(view.heading, 'From Ivory');
  assert.equal(view.route, '/workspaces/ivory');
  assert.equal(view.linkLabel, 'Open Ivory');
  // Addressing the specialist directly is offered plainly, and nothing claims
  // the assistant can hand it work — it cannot.
  assert.match(view.note, /ask Reaper Producer directly/i);
  assert.doesNotMatch(view.note, /assign|delegate|instruct|hand off|on your behalf/i);
  assert.doesNotMatch(view.note, /instead|workaround|advanced|fall ?back/i);
  assert.equal(view.section.items.length, 1);
});

test('the studio section degrades safely without a name, a workspace, or a usable route', () => {
  const noRoute = studioSectionView({
    health: { status: 'unavailable' },
    specialist_name: 'Reaper Producer',
    route: 'https://evil.example',
    items: []
  });
  assert.equal(noRoute.route, '');
  assert.match(noRoute.note, /works in its own workspace/i);

  const bare = studioSectionView({ health: { status: 'available' }, items: [] });
  assert.equal(bare.visible, true);
  assert.equal(bare.heading, 'From your studio');
  assert.equal(bare.linkLabel, 'Open the workspace');
  assert.match(bare.note, /your specialist/i);
});
