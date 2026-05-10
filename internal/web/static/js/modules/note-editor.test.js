// Tests for note-editor.js pure helpers — run with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  parseHeadingLevel,
  parseTaskLine,
  lineKindClass,
  normalizeCompactTaskListMarkdown,
  isUndoShortcut,
  isRedoShortcut,
  isPrintableKey,
} from './note-editor.js';

// =============================================================================
// parseHeadingLevel
// =============================================================================

test('parseHeadingLevel: returns 0 for empty / null / non-heading', () => {
  assert.equal(parseHeadingLevel(''), 0);
  assert.equal(parseHeadingLevel(null), 0);
  assert.equal(parseHeadingLevel('plain text'), 0);
  assert.equal(parseHeadingLevel('#NoSpace'), 0);
});

test('parseHeadingLevel: returns level for valid ATX headings', () => {
  assert.equal(parseHeadingLevel('# H1'), 1);
  assert.equal(parseHeadingLevel('## H2'), 2);
  assert.equal(parseHeadingLevel('### H3'), 3);
  assert.equal(parseHeadingLevel('#### H4'), 4);
  assert.equal(parseHeadingLevel('##### H5'), 5);
  assert.equal(parseHeadingLevel('###### H6'), 6);
});

test('parseHeadingLevel: 7+ hashes return 0', () => {
  // The regex `^(#{1,6})\s+` requires whitespace after up to 6 hashes. With 7
  // hashes followed by a space, the 7th hash sits where whitespace must be, so
  // the match fails. Documented for parity with the original sessionManager code.
  assert.equal(parseHeadingLevel('####### Seven'), 0);
});

// =============================================================================
// parseTaskLine
// =============================================================================

test('parseTaskLine: returns null for non-task lines', () => {
  assert.equal(parseTaskLine(''), null);
  assert.equal(parseTaskLine('plain text'), null);
  assert.equal(parseTaskLine('- bullet'), null);
  assert.equal(parseTaskLine('* bullet'), null);
});

test('parseTaskLine: parses unchecked task', () => {
  const got = parseTaskLine('- [ ] do the thing');
  assert.ok(got);
  assert.equal(got.bullet, '-');
  assert.equal(got.checked, false);
  assert.equal(got.compactUnchecked, false);
  assert.equal(got.text, 'do the thing');
});

test('parseTaskLine: parses checked task with lowercase x', () => {
  const got = parseTaskLine('- [x] done');
  assert.ok(got);
  assert.equal(got.checked, true);
  assert.equal(got.text, 'done');
});

test('parseTaskLine: parses checked task with uppercase X', () => {
  const got = parseTaskLine('- [X] done');
  assert.ok(got);
  assert.equal(got.checked, true);
});

test('parseTaskLine: parses compact `[]` form (parity quirk)', () => {
  // The original sessionManager.parseNoteTaskLine has a latent oddity here:
  // `compactUnchecked: match[4] === ''` always evaluates false because an
  // unmatched optional capture is `undefined`, not `''`. The compact-form
  // detection is effectively dead — but normalize-on-save rewrites `[]` to
  // `[ ]` before the user notices. Keeping parity to avoid behavior change
  // in this extraction; can be fixed in a follow-up that audits all callers.
  const got = parseTaskLine('- [] terse form');
  assert.ok(got);
  assert.equal(got.checked, false);
  assert.equal(got.compactUnchecked, false);
});

test('parseTaskLine: preserves indent and bullet variants', () => {
  const got = parseTaskLine('  * [ ] indented with star');
  assert.ok(got);
  assert.equal(got.indent, '  ');
  assert.equal(got.bullet, '*');
});

// =============================================================================
// lineKindClass
// =============================================================================

test('lineKindClass: empty line yields empty class', () => {
  assert.equal(lineKindClass(''), '');
  assert.equal(lineKindClass('plain text'), '');
});

test('lineKindClass: heading takes precedence over list', () => {
  assert.equal(lineKindClass('# Heading'), 'is-heading-1');
  assert.equal(lineKindClass('## Heading'), 'is-heading-2');
});

test('lineKindClass: task-list class wins over plain list', () => {
  assert.equal(lineKindClass('- [ ] task'), 'is-task-list');
  assert.equal(lineKindClass('- bullet'), 'is-list');
});

test('lineKindClass: ordered list and blockquote', () => {
  assert.equal(lineKindClass('1. ordered'), 'is-list');
  assert.equal(lineKindClass('> quote'), 'is-quote');
});

// =============================================================================
// normalizeCompactTaskListMarkdown
// =============================================================================

test('normalizeCompactTaskListMarkdown: rewrites compact form', () => {
  assert.equal(
    normalizeCompactTaskListMarkdown('- [] one\n- [ ] two\n- [x] three\n'),
    '- [ ] one\n- [ ] two\n- [x] three\n',
  );
});

test('normalizeCompactTaskListMarkdown: leaves non-task lines alone', () => {
  assert.equal(
    normalizeCompactTaskListMarkdown('# Heading\nbody\n[] not a task\n'),
    '# Heading\nbody\n[] not a task\n',
  );
});

test('normalizeCompactTaskListMarkdown: preserves indent and bullet flavor', () => {
  assert.equal(
    normalizeCompactTaskListMarkdown('  * [] indented\n+ [] plus'),
    '  * [ ] indented\n+ [ ] plus',
  );
});

// =============================================================================
// isUndoShortcut / isRedoShortcut / isPrintableKey
// =============================================================================

const evt = (overrides = {}) => ({
  key: '',
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...overrides,
});

test('isUndoShortcut: matches Cmd+Z / Ctrl+Z', () => {
  assert.equal(isUndoShortcut(evt({ key: 'z', metaKey: true })), true);
  assert.equal(isUndoShortcut(evt({ key: 'z', ctrlKey: true })), true);
  assert.equal(isUndoShortcut(evt({ key: 'Z', metaKey: true })), true); // case-insensitive
});

test('isUndoShortcut: rejects when Shift or Alt is pressed', () => {
  assert.equal(isUndoShortcut(evt({ key: 'z', metaKey: true, shiftKey: true })), false);
  assert.equal(isUndoShortcut(evt({ key: 'z', metaKey: true, altKey: true })), false);
});

test('isUndoShortcut: rejects unmodified Z', () => {
  assert.equal(isUndoShortcut(evt({ key: 'z' })), false);
});

test('isUndoShortcut: rejects null/undefined event', () => {
  assert.equal(isUndoShortcut(null), false);
  assert.equal(isUndoShortcut(undefined), false);
});

test('isRedoShortcut: matches Cmd+Shift+Z and Ctrl+Y', () => {
  assert.equal(isRedoShortcut(evt({ key: 'z', metaKey: true, shiftKey: true })), true);
  assert.equal(isRedoShortcut(evt({ key: 'y', ctrlKey: true })), true);
  assert.equal(isRedoShortcut(evt({ key: 'Y', metaKey: true })), true);
});

test('isRedoShortcut: rejects Cmd+Z without Shift', () => {
  assert.equal(isRedoShortcut(evt({ key: 'z', metaKey: true })), false);
});

test('isRedoShortcut: rejects Alt-modified', () => {
  assert.equal(isRedoShortcut(evt({ key: 'y', ctrlKey: true, altKey: true })), false);
});

test('isPrintableKey: single-char without modifiers', () => {
  assert.equal(isPrintableKey(evt({ key: 'a' })), true);
  assert.equal(isPrintableKey(evt({ key: ' ' })), true);
  assert.equal(isPrintableKey(evt({ key: '1' })), true);
});

test('isPrintableKey: rejects multi-char keys (Enter, ArrowLeft, etc.)', () => {
  assert.equal(isPrintableKey(evt({ key: 'Enter' })), false);
  assert.equal(isPrintableKey(evt({ key: 'ArrowLeft' })), false);
  assert.equal(isPrintableKey(evt({ key: 'Backspace' })), false);
});

test('isPrintableKey: rejects single-char with Cmd/Ctrl/Alt', () => {
  assert.equal(isPrintableKey(evt({ key: 'a', metaKey: true })), false);
  assert.equal(isPrintableKey(evt({ key: 'a', ctrlKey: true })), false);
  assert.equal(isPrintableKey(evt({ key: 'a', altKey: true })), false);
});
