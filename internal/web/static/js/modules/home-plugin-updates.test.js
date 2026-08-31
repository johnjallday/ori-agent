import test from 'node:test';
import assert from 'node:assert/strict';

import { homePluginUpdatesView } from './home-plugin-updates.js';

test('homePluginUpdatesView exposes only available cached plugin updates', () => {
  const first = {
    name: '<alpha>',
    available: true,
    installedVersion: '1.0.0',
    sourceVersion: '2.0.0'
  };
  const view = homePluginUpdatesView({
    lastSuccessfulCheckAt: '2026-08-31T20:00:00Z',
    updates: [first, { name: 'current', available: false }]
  });

  assert.equal(view.count, 1);
  assert.equal(view.visible, true);
  assert.equal(view.title, '1 plugin update');
  assert.deepEqual(view.updates, [first]);
});

test('homePluginUpdatesView hides an empty or malformed snapshot', () => {
  assert.deepEqual(homePluginUpdatesView(null), {
    count: 0,
    visible: false,
    title: '0 plugin updates',
    updates: []
  });

  const view = homePluginUpdatesView({ updates: [{ available: false }, null] });
  assert.equal(view.visible, false);
  assert.equal(view.count, 0);
});

test('homePluginUpdatesView uses plural copy for multiple updates', () => {
  const updates = [
    { name: 'alpha', available: true },
    { name: 'beta', available: true }
  ];
  assert.equal(homePluginUpdatesView({ updates }).title, '2 plugin updates');
});
