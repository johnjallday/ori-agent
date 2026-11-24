// Tests for home-folder-search-dialogs.js — the words a linked folder's row
// uses before it changes search. The dialogs themselves need a document and are
// driven in tests/folder-backed-search-locations.spec.ts.
//   node --test internal/web/static/js/modules/home-folder-search-dialogs.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CONSENT_SCOPE,
  FOLDER_CHOICES,
  REMOVE_KEEPS,
  VERIFY_SENTENCE,
  consentTitle,
  removeTitle
} from './home-folder-search-dialogs.js';

const read = path => readFileSync(new URL(path, import.meta.url), 'utf8');
const squash = text => text.replace(/\s+/g, ' ').trim();

test("Make searchable asks with the standalone page's own consent sentence", () => {
  // search-desk.html's #folderConsent is the source of truth (requirement 34).
  const page = read('../../search-desk.html');
  const scope = page.match(/<p id="consentScope">([\s\S]*?)<\/p>/);
  assert.ok(scope, 'the standalone page still has its consent sentence');
  assert.equal(squash(scope[1]), CONSENT_SCOPE);
  assert.match(page, />Allow search</);
  // The same title the page builds, naming the folder.
  assert.match(read('./search-desk.js'), /`Allow Ori to search \$\{/);
  assert.equal(consentTitle('Album masters'), 'Allow Ori to search Album masters?');
});

test('Files and ordinary assistant Explore present the same folder choices', () => {
  const page = read('../../../templates/components/personal-assistant-folder.tmpl');
  const offered = [...page.matchAll(/data-explore-kind="([a-z]+)">([^<]+)</g)].map(m => ({
    kind: m[1],
    label: m[2]
  }));
  assert.deepEqual(FOLDER_CHOICES, offered);
});

test('an older link is verified with the existing sentence', () => {
  assert.ok(read('./folder-search-review.js').includes(VERIFY_SENTENCE));
});

test('Remove from search says only the filename index goes, and what stays (requirement 36)', () => {
  assert.equal(removeTitle('Album masters'), 'Remove Album masters from search?');
  assert.match(REMOVE_KEEPS, /filename index is cleared/);
  for (const kept of ['workspace', 'link', 'notes', 'tickets', 'files', 'folder itself']) {
    assert.ok(REMOVE_KEEPS.includes(kept), kept);
  }
  // Nothing here grants access or starts work.
  for (const words of [CONSENT_SCOPE, REMOVE_KEEPS]) {
    assert.doesNotMatch(words, /\b(hire|Filing|model)\b/i);
  }
  assert.match(CONSENT_SCOPE, /won’t read file contents or give agents access/);
});
