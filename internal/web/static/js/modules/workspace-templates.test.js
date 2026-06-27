import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// workspace-templates.js is a non-module IIFE that assigns
// window.WorkspaceTemplates. Load it into an isolated context with a minimal
// window/document, the same pattern used by workspace-group-options.test.js.
function loadWorkspaceTemplates() {
  const window = {};
  const context = { console, document: {}, window };
  const source = readFileSync(new URL('./workspace-templates.js', import.meta.url), 'utf8');
  vm.runInNewContext(source, context, { filename: 'workspace-templates.js' });
  return window.WorkspaceTemplates;
}

test('every starting-point template declares a valid behaviorProfile', () => {
  const WT = loadWorkspaceTemplates();
  // Spread into this realm — arrays from the vm context have a different
  // prototype, which deepStrictEqual would otherwise reject.
  assert.deepEqual([...WT.validBehaviorProfiles], ['general', 'research', 'software_project']);
  for (const t of WT.list) {
    assert.ok(
      WT.validBehaviorProfiles.includes(t.behaviorProfile),
      `template "${t.id}" has invalid behaviorProfile: ${JSON.stringify(t.behaviorProfile)}`
    );
  }
});

test('starting-point -> agent behavior mapping matches the PRD table', () => {
  const WT = loadWorkspaceTemplates();
  const expected = {
    blank: 'general',
    travels: 'general',
    'daily-briefings': 'general',
    'content-production': 'general',
    'research-project': 'research',
    'personal-ops': 'general'
  };
  for (const [id, profile] of Object.entries(expected)) {
    assert.equal(WT.getById(id).behaviorProfile, profile, `mapping for "${id}"`);
  }
});

test('getById falls back to the first template (Blank) for unknown ids', () => {
  const WT = loadWorkspaceTemplates();
  assert.equal(WT.list[0].id, 'blank');
  assert.equal(WT.getById('does-not-exist').id, 'blank');
});

test('Blank is the default starting point and seeds no starter tasks', () => {
  const WT = loadWorkspaceTemplates();
  const blank = WT.getById('blank');
  assert.equal(blank.behaviorProfile, 'general');
  assert.ok(Array.isArray(blank.starterTasks));
  assert.equal(blank.starterTasks.length, 0);
  assert.equal(blank.defaultName, '');
  assert.equal(blank.defaultDescription, '');
});
