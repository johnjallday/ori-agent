// Regression guard: the Plugins page and the wizard's readiness panel must
// describe a plugin's lifecycle position in the same words. They are
// separate modules for good reasons (one is a legacy page IIFE, the other is
// tested standalone against a DOM stub with no page dependencies), so nothing
// stops the two from silently drifting apart except this test.
//
// Run with: node --test internal/web/static/js/modules/plugin-lifecycle-labels.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const LIFECYCLE_SOURCE = readFileSync(new URL('./plugin-lifecycle.js', import.meta.url), 'utf8');
const READINESS_SOURCE = readFileSync(new URL('./blueprint-readiness.js', import.meta.url), 'utf8');
const PLUGINS_PAGE_SOURCE = readFileSync(new URL('../plugins.js', import.meta.url), 'utf8');

function loadLifecycleLabels() {
  const sandbox = { window: {}, document: {}, fetch: async () => ({}) };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(LIFECYCLE_SOURCE, sandbox, { filename: 'plugin-lifecycle.js' });
  return sandbox.window.PluginLifecycle.LIFECYCLE_LABELS;
}

test('the Plugins page reads the shared labels rather than its own copy', () => {
  // Asserting on source text, not behavior: the point is that plugins.js
  // cannot describe "enabled"/"disabled" in words of its own choosing, so a
  // future edit to one wording is structurally forced to update both
  // surfaces at once rather than merely being reminded to.
  assert.match(PLUGINS_PAGE_SOURCE, /PluginLifecycle\.LIFECYCLE_LABELS\.ENABLED/);
  assert.match(PLUGINS_PAGE_SOURCE, /PluginLifecycle\.LIFECYCLE_LABELS\.DISABLED/);
});

test('the wizard readiness panel states the same two phrases the shared labels define', () => {
  const labels = loadLifecycleLabels();
  // blueprint-readiness.js stays runtime-independent of plugin-lifecycle.js
  // (its own tests load it standalone), so agreement here is enforced by
  // this literal-text comparison rather than a shared function call.
  assert.match(READINESS_SOURCE, new RegExp(labels.ENABLED.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  assert.match(
    READINESS_SOURCE,
    new RegExp(labels.DISABLED.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  );
});

test('capitalize renders the label as it appears at the start of a sentence', () => {
  const labels = loadLifecycleLabels();
  const sandbox = { window: {}, document: {}, fetch: async () => ({}) };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(LIFECYCLE_SOURCE, sandbox, { filename: 'plugin-lifecycle.js' });
  const PL = sandbox.window.PluginLifecycle;
  assert.equal(PL.capitalize(labels.ENABLED), 'Installed and enabled');
  assert.equal(PL.capitalize(labels.DISABLED), 'Installed, still disabled');
  assert.equal(PL.capitalize(''), '');
});
