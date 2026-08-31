import test from 'node:test';
import assert from 'node:assert/strict';

import {
  personalAssistantTodayView,
  safeTodayRoute,
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
