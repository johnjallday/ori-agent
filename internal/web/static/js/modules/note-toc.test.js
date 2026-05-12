// Tests for note-toc.js — run with `node --test internal/web/static/js/modules/note-toc.test.js`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  parseHeadings,
  buildOutline,
  sliceHeadingRange,
  moveHeadingRange,
} from './note-toc.js';

// =============================================================================
// parseHeadings — should mirror the Go parser in internal/session/note_headings.go
// =============================================================================

test('parseHeadings: empty input returns []', () => {
  assert.deepEqual(parseHeadings(''), []);
});

test('parseHeadings: simple ATX', () => {
  const src = '# Title\n\n## Subhead\nbody\n### Detail\n';
  assert.deepEqual(parseHeadings(src), [
    { level: 1, text: 'Title', position: 0 },
    { level: 2, text: 'Subhead', position: 9 },
    { level: 3, text: 'Detail', position: 25 },
  ]);
});

test('parseHeadings: all six levels', () => {
  const src = '# 1\n## 2\n### 3\n#### 4\n##### 5\n###### 6\n####### 7\n';
  const got = parseHeadings(src);
  assert.equal(got.length, 6);
  got.forEach((h, i) => assert.equal(h.level, i + 1));
});

test('parseHeadings: excludes headings inside fenced code blocks', () => {
  const src = '# Real\n```\n# Not a heading\n## Also not\n```\n## Real two\n';
  const got = parseHeadings(src);
  assert.equal(got.length, 2);
  assert.equal(got[0].text, 'Real');
  assert.equal(got[1].text, 'Real two');
});

test('parseHeadings: tilde fences also excluded', () => {
  const src = '~~~\n# inside tildes\n~~~\n# outside\n';
  const got = parseHeadings(src);
  assert.equal(got.length, 1);
  assert.equal(got[0].text, 'outside');
});

test('parseHeadings: requires whitespace after #', () => {
  const src = '#NoSpace\n# Yes\n#\n';
  const got = parseHeadings(src);
  assert.equal(got.length, 1);
  assert.equal(got[0].text, 'Yes');
});

test('parseHeadings: trims trailing whitespace from text', () => {
  const got = parseHeadings('#   Padded   \n');
  assert.equal(got.length, 1);
  assert.equal(got[0].text, 'Padded');
});

test('parseHeadings: handles missing trailing newline', () => {
  const got = parseHeadings('# No newline');
  assert.equal(got.length, 1);
  assert.equal(got[0].text, 'No newline');
});

test('parseHeadings: position points at the # char', () => {
  const src = 'preamble\n## Heading\nmore\n';
  const got = parseHeadings(src);
  assert.equal(got.length, 1);
  assert.equal(src[got[0].position], '#');
});

// =============================================================================
// buildOutline
// =============================================================================

test('buildOutline: nests h2 under h1', () => {
  const tree = buildOutline('# A\n## A1\n## A2\n# B\n');
  assert.equal(tree.length, 2);
  assert.equal(tree[0].text, 'A');
  assert.equal(tree[0].children.length, 2);
  assert.equal(tree[0].children[0].text, 'A1');
  assert.equal(tree[1].text, 'B');
  assert.equal(tree[1].children.length, 0);
});

test('buildOutline: handles deep nesting and pops correctly', () => {
  const tree = buildOutline('# A\n## A1\n### A1a\n## A2\n');
  assert.equal(tree[0].children.length, 2);
  assert.equal(tree[0].children[0].children.length, 1);
  assert.equal(tree[0].children[0].children[0].text, 'A1a');
});

test('buildOutline: empty input returns []', () => {
  assert.deepEqual(buildOutline(''), []);
});

// =============================================================================
// sliceHeadingRange
// =============================================================================

test('sliceHeadingRange: simple sibling', () => {
  const src = '# A\nbody A\n# B\nbody B\n';
  const aStart = src.indexOf('# A');
  const range = sliceHeadingRange(src, aStart);
  assert.deepEqual(range, { start: aStart, end: src.indexOf('# B') });
});

test('sliceHeadingRange: nested heading carries its children', () => {
  const src = '## A\n### A.1\nbody\n## B\n';
  const range = sliceHeadingRange(src, src.indexOf('## A'));
  assert.equal(range.end, src.indexOf('## B'));
});

test('sliceHeadingRange: last heading runs to end of source', () => {
  const src = '# A\n# B\nlast body';
  const range = sliceHeadingRange(src, src.indexOf('# B'));
  assert.equal(range.end, src.length);
});

test('sliceHeadingRange: returns null for unknown position', () => {
  assert.equal(sliceHeadingRange('# A\n# B\n', 99), null);
});

test('sliceHeadingRange: heading inside fenced block is invisible to slicer', () => {
  const src = '# Real\n```\n# Fake\n```\n# Other\n';
  const range = sliceHeadingRange(src, src.indexOf('# Fake'));
  assert.equal(range, null, 'fake heading inside code fence should not be slice-able');
});

// =============================================================================
// moveHeadingRange
// =============================================================================

test('moveHeadingRange: swap two siblings (move B before A)', () => {
  const src = '# A\nbody A\n# B\nbody B\n';
  const result = moveHeadingRange(src, src.indexOf('# B'), 0);
  assert.equal(result, '# B\nbody B\n# A\nbody A\n');
});

test('moveHeadingRange: move first heading to end', () => {
  const src = '# A\nbody A\n# B\nbody B\n';
  const result = moveHeadingRange(src, 0, src.length);
  assert.equal(result, '# B\nbody B\n# A\nbody A\n');
});

test('moveHeadingRange: nested heading carries children when moved', () => {
  const src = '## A\n### A.1\na1 body\n## B\nb body\n';
  const result = moveHeadingRange(src, src.indexOf('## A'), src.length);
  assert.equal(result, '## B\nb body\n## A\n### A.1\na1 body\n');
});

test('moveHeadingRange: dropping inside the block itself is a no-op', () => {
  const src = '# A\nbody A\n# B\nbody B\n';
  const inside = src.indexOf('body A');
  assert.equal(moveHeadingRange(src, 0, inside), src);
});

test('moveHeadingRange: unknown source position returns null', () => {
  assert.equal(moveHeadingRange('# A\n', 99, 0), null);
});
