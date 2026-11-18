import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('../agents-detail.js', import.meta.url), 'utf8');
const start = source.indexOf('let appearanceCatalogWatch = false;');
const end = source.indexOf('function appearanceUploadURL()', start);
assert.ok(start >= 0 && end > start);

test('late catalog hydration always updates header art without disturbing an active or saving editor', () => {
  let listener;
  let subscriptions = 0,
    headerPaints = 0,
    editorPaints = 0;
  let focusInside = false,
    saving = false;
  const profileInput = { value: 'An unsent profile', selectionStart: 3, selectionEnd: 7 };
  const context = {
    window: {
      CharacterCatalog: {
        onChange(fn) {
          listener = fn;
          subscriptions++;
        }
      }
    },
    renderCurrentAgentAvatar: () => headerPaints++,
    appearanceEditor: { status: () => (saving ? 'saving' : 'idle'), render: () => editorPaints++ },
    document: {
      activeElement: profileInput,
      getElementById: () => ({ contains: () => focusInside })
    }
  };
  vm.runInNewContext(
    source.slice(start, end) + '\nwatchCharacterCatalog(); watchCharacterCatalog();',
    context
  );
  assert.equal(subscriptions, 1);
  listener();
  assert.equal(headerPaints, 1);
  assert.equal(editorPaints, 1);
  focusInside = true;
  listener();
  assert.equal(headerPaints, 2);
  assert.equal(editorPaints, 1);
  focusInside = false;
  saving = true;
  listener();
  assert.equal(headerPaints, 3);
  assert.equal(editorPaints, 1);
  assert.deepEqual(profileInput, {
    value: 'An unsent profile',
    selectionStart: 3,
    selectionEnd: 7
  });
});
