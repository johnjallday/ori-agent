// Tests for note-wikilinks.js — mirrors note_links_test.go to enforce
// parser parity between the Go and JS implementations.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  parseWikilinks,
  renderWikilinkHTML,
  applyWikilinksToHtml,
  invalidateNotesCache
} from './note-wikilinks.js';

// invalidateNotesCache is the contract note-page.js calls after creating an
// extracted note so the new wikilink resolves instead of looking broken.
test('invalidateNotesCache: callable for a workspace id and as a clear-all', () => {
  assert.equal(typeof invalidateNotesCache, 'function');
  assert.doesNotThrow(() => invalidateNotesCache('ws-1'));
  assert.doesNotThrow(() => invalidateNotesCache());
});

// =============================================================================
// parseWikilinks — should mirror the Go parser in internal/session/note_links.go
// =============================================================================

test('parseWikilinks: empty input returns []', () => {
  assert.deepEqual(parseWikilinks(''), []);
});

test('parseWikilinks: simple link', () => {
  const got = parseWikilinks('See [[Brand Kit]] for details.');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'Brand Kit');
  assert.equal(got[0].display, '');
  assert.equal(got[0].position, 4);
});

test('parseWikilinks: pipe form uses display text', () => {
  const got = parseWikilinks('See [[brand-kit-2026|the 2026 update]].');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'brand-kit-2026');
  assert.equal(got[0].display, 'the 2026 update');
});

test('parseWikilinks: multiple on same line', () => {
  const got = parseWikilinks('[[A]] and [[B]] both relevant.');
  assert.equal(got.length, 2);
  assert.equal(got[0].target, 'A');
  assert.equal(got[1].target, 'B');
});

test('parseWikilinks: excludes matches inside fenced code blocks', () => {
  const src = '[[Real]]\n```\n[[Inside fence]]\n[[Also fake]]\n```\n[[Other]]\n';
  const got = parseWikilinks(src);
  assert.equal(got.length, 2);
  assert.equal(got[0].target, 'Real');
  assert.equal(got[1].target, 'Other');
});

test('parseWikilinks: tilde fence also excluded', () => {
  const got = parseWikilinks('[[Outer]]\n~~~\n[[Hidden]]\n~~~\n[[Last]]\n');
  assert.equal(got.length, 2);
  assert.equal(got[0].target, 'Outer');
  assert.equal(got[1].target, 'Last');
});

test('parseWikilinks: rejects empty target', () => {
  const got = parseWikilinks('[[]] [[ ]] [[real]]');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'real');
});

test('parseWikilinks: unbalanced brackets ignored', () => {
  const got = parseWikilinks('[[Open without close \nor [[]] empty');
  assert.equal(got.length, 0);
});

test('parseWikilinks: trims whitespace from target and display', () => {
  const got = parseWikilinks('[[  Padded  |  Display label  ]]');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'Padded');
  assert.equal(got[0].display, 'Display label');
});

test('parseWikilinks: position points at the leading [[', () => {
  const src = 'preamble [[Heading here]] suffix';
  const got = parseWikilinks(src);
  assert.equal(got.length, 1);
  assert.equal(src.slice(got[0].position, got[0].position + 2), '[[');
});

test('parseWikilinks: handles missing trailing newline', () => {
  const got = parseWikilinks('no newline [[Target]]');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'Target');
});

test('parseWikilinks: unicode targets', () => {
  const got = parseWikilinks('Reference [[日本語ノート]] etc.');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, '日本語ノート');
});

test('parseWikilinks: nested brackets reject the outer match', () => {
  // The inner `[` aborts the parse of the outer; only the second wikilink
  // survives. Matches Go's TestParseWikilinks_NestedBracketsRejected.
  const got = parseWikilinks('[[outer [inner] outer]] [[good]]');
  assert.equal(got.length, 1);
  assert.equal(got[0].target, 'good');
});

// =============================================================================
// renderWikilinkHTML
// =============================================================================

test('renderWikilinkHTML: default class for resolved links', () => {
  const html = renderWikilinkHTML('Brand Kit', '');
  assert.match(html, /class="note-wikilink"/);
  assert.match(html, /data-wikilink-target="Brand Kit"/);
  assert.match(html, />Brand Kit<\/a>/);
});

test('renderWikilinkHTML: broken-link class + tooltip', () => {
  const html = renderWikilinkHTML('Missing', '', true);
  assert.match(html, /class="note-wikilink note-wikilink-broken"/);
  assert.match(html, /title="Click to create &quot;Missing&quot;"/);
});

test('renderWikilinkHTML: uses display text when provided', () => {
  const html = renderWikilinkHTML('slug-2026', 'the 2026 update');
  assert.match(html, />the 2026 update<\/a>/);
  // Target stays the same in the data attribute.
  assert.match(html, /data-wikilink-target="slug-2026"/);
});

test('renderWikilinkHTML: escapes attributes and text', () => {
  const html = renderWikilinkHTML('Quote"X', '<b>bold</b>');
  assert.match(html, /data-wikilink-target="Quote&quot;X"/);
  assert.match(html, /&lt;b&gt;bold&lt;\/b&gt;/);
});

// =============================================================================
// applyWikilinksToHtml
// =============================================================================

test('applyWikilinksToHtml: rewrites all [[…]] occurrences', () => {
  const html = '<p>See [[Brand Kit]] and [[Roadmap]].</p>';
  const out = applyWikilinksToHtml(html, t => (t === 'Brand Kit' ? 'note-1' : null));
  assert.match(out, /class="note-wikilink"[^>]*>Brand Kit<\/a>/);
  assert.match(out, /class="note-wikilink note-wikilink-broken"[^>]*>Roadmap<\/a>/);
});

test('applyWikilinksToHtml: no-op when there are no wikilinks', () => {
  const html = '<p>Plain text without any references.</p>';
  assert.equal(
    applyWikilinksToHtml(html, () => null),
    html
  );
});

test('applyWikilinksToHtml: handles pipe form', () => {
  const html = '<p>See [[slug-2026|the 2026 update]].</p>';
  const out = applyWikilinksToHtml(html, () => null);
  assert.match(out, />the 2026 update<\/a>/);
  assert.match(out, /data-wikilink-target="slug-2026"/);
});

test('applyWikilinksToHtml: skips empty targets', () => {
  const html = '<p>[[]] and [[real]] here.</p>';
  const out = applyWikilinksToHtml(html, () => 'note-1');
  // Empty wikilink is dropped entirely (no <a> emitted).
  assert.equal((out.match(/<a/g) || []).length, 1);
  assert.match(out, />real<\/a>/);
});
