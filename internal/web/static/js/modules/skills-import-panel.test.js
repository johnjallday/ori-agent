// Tests for the Skills page's one-time import panel rendering.
// Run with: node --test internal/web/static/js/modules/skills-import-panel.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('./skills-import-panel.js', import.meta.url), 'utf8');

function loadModule() {
  const sandbox = { window: {}, Set, Array, String, Object };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'skills-import-panel.js' });
  return sandbox.window.SkillsImportPanel;
}

const candidates = [
  { name: 'writing-helper', description: 'Helps <b>write</b>', source_folder: '/h/.agents/skills' },
  { name: 'already-mine', description: 'Imported before', already_present: true },
  null,
  { name: '  ' }
];

test('rows start unticked, and a skill already in the Skills folder is greyed out', () => {
  const panel = loadModule();
  const html = panel.render({ candidates, selected: new Set() });

  assert.match(html, /Import skills you already have/);
  assert.match(html, /The originals are left untouched\./);
  assert.match(html, /data-import-name="writing-helper"\s+ /);
  assert.doesNotMatch(html, /checked/);
  assert.match(html, /data-import-name="already-mine"\s+ disabled/);
  assert.match(html, /Already in your Skills folder/);
  assert.match(html, /Helps &lt;b&gt;write&lt;\/b&gt;/);
  assert.equal((html.match(/data-import-name=/g) || []).length, 2);
  // Nothing selected: Import selected is disabled, Not now is not.
  assert.match(html, /data-import-action="import" disabled>Import selected/);
  assert.match(html, /data-import-action="dismiss" >Not now/);
});

test('a ticked row enables Import selected; an already-present one can never be ticked', () => {
  const panel = loadModule();
  const html = panel.render({ candidates, selected: new Set(['writing-helper', 'already-mine']) });
  assert.match(html, /data-import-name="writing-helper" checked/);
  assert.doesNotMatch(html, /data-import-name="already-mine" checked/);
  assert.match(html, /data-import-action="import" >Import selected/);
});

test('after an import the panel lists each failure with its reason', () => {
  const panel = loadModule();
  const html = panel.render({
    candidates,
    result: {
      imported: ['writing-helper'],
      failed: [{ name: 'broken<skill>', reason: 'copy: dangling link' }]
    }
  });
  assert.match(html, /Imported 1 skill into your Skills folder\./);
  assert.match(html, /<strong>broken&lt;skill&gt;<\/strong>: copy: dangling link/);
  assert.match(html, /data-import-action="done"/);
  assert.doesNotMatch(html, /data-import-name=/);
});

test('an on-demand panel with nothing to offer says so', () => {
  const panel = loadModule();
  const html = panel.render({ candidates: [] });
  assert.match(html, /No skills were found in ~\/\.agents\/skills\./);
});
