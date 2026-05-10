// Tests for note-editor.js pure helpers — run with `node --test`.
import { test, mock } from 'node:test';
import assert from 'node:assert/strict';

import {
  parseHeadingLevel,
  parseTaskLine,
  lineKindClass,
  normalizeCompactTaskListMarkdown,
  isUndoShortcut,
  isRedoShortcut,
  isPrintableKey,
  normalizeVaultReference,
  NoteHistory,
  NoteAutoSaveTimer,
  pointerDragged,
  escapeHtml,
  renderEditingLine,
  renderEditingRange,
  renderMarkdown,
  renderMarkdownLine,
  renderInlineMarkdown,
  renderHeadingLine,
  renderTaskLine,
  renderRenderedLine,
  lineIndexAtPosition,
  startOfLine,
  pruneCollapsedHeadings,
  buildLiveEditorHTML,
  NoteLiveEditorState,
  NoteLiveEditor,
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

// =============================================================================
// normalizeVaultReference
// =============================================================================

test('normalizeVaultReference: returns null for null/undefined/non-object', () => {
  assert.equal(normalizeVaultReference(null), null);
  assert.equal(normalizeVaultReference(undefined), null);
  assert.equal(normalizeVaultReference('not an object'), null);
  assert.equal(normalizeVaultReference(42), null);
});

test('normalizeVaultReference: snake_case fields are accepted', () => {
  const got = normalizeVaultReference({
    vault_name: 'Personal',
    record_label: 'Login',
    record_id: 'rec_123',
  });
  assert.deepEqual(got, { vaultName: 'Personal', recordLabel: 'Login', recordId: 'rec_123' });
});

test('normalizeVaultReference: camelCase fields also work', () => {
  const got = normalizeVaultReference({
    vaultName: 'Personal',
    recordLabel: 'Login',
    recordId: 'rec_123',
  });
  assert.deepEqual(got, { vaultName: 'Personal', recordLabel: 'Login', recordId: 'rec_123' });
});

test('normalizeVaultReference: missing record id returns null', () => {
  assert.equal(normalizeVaultReference({ vault_name: 'Personal' }), null);
  assert.equal(normalizeVaultReference({ record_id: '' }), null);
  assert.equal(normalizeVaultReference({ record_id: '   ' }), null);
});

test('normalizeVaultReference: trims whitespace from each field', () => {
  const got = normalizeVaultReference({
    vault_name: '  Personal  ',
    record_label: '\tLogin\t',
    record_id: '  rec_123  ',
  });
  assert.deepEqual(got, { vaultName: 'Personal', recordLabel: 'Login', recordId: 'rec_123' });
});

// =============================================================================
// NoteHistory
// =============================================================================

test('NoteHistory: push records new entries', () => {
  const h = new NoteHistory();
  assert.equal(h.push('a'), true);
  assert.equal(h.push('b'), true);
  assert.deepEqual(h.undoStack, ['a', 'b']);
});

test('NoteHistory: push de-dupes consecutive identical values', () => {
  const h = new NoteHistory();
  h.push('a');
  assert.equal(h.push('a'), false);
  assert.deepEqual(h.undoStack, ['a']);
});

test('NoteHistory: push respects the limit by dropping oldest', () => {
  const h = new NoteHistory({ limit: 3 });
  h.push('a'); h.push('b'); h.push('c'); h.push('d');
  assert.deepEqual(h.undoStack, ['b', 'c', 'd']);
});

test('NoteHistory: push is suppressed while applying', () => {
  const h = new NoteHistory();
  h.applying = true;
  assert.equal(h.push('a'), false);
  assert.deepEqual(h.undoStack, []);
});

test('NoteHistory: push clears the redo stack', () => {
  const h = new NoteHistory();
  h.push('a');
  h.undo('current');
  assert.equal(h.redoStack.length, 1);
  h.push('b');
  assert.deepEqual(h.redoStack, []);
});

test('NoteHistory: undo returns previous and shifts redo', () => {
  const h = new NoteHistory();
  h.push('a'); h.push('b');
  const prev = h.undo('c'); // current is 'c', stacks become undo=[a], redo=[c]
  assert.equal(prev, 'b');
  assert.deepEqual(h.undoStack, ['a']);
  assert.deepEqual(h.redoStack, ['c']);
});

test('NoteHistory: undo returns null when empty', () => {
  const h = new NoteHistory();
  assert.equal(h.undo('current'), null);
});

test('NoteHistory: redo replays correctly', () => {
  const h = new NoteHistory();
  h.push('a'); h.push('b');
  const prev = h.undo('c'); // prev = 'b', undo=[a], redo=[c]
  assert.equal(prev, 'b');
  const next = h.redo('b'); // next = 'c', undo=[a, b], redo=[]
  assert.equal(next, 'c');
  assert.deepEqual(h.undoStack, ['a', 'b']);
  assert.deepEqual(h.redoStack, []);
});

test('NoteHistory: redo returns null when empty', () => {
  const h = new NoteHistory();
  assert.equal(h.redo('current'), null);
});

test('NoteHistory: reset clears everything', () => {
  const h = new NoteHistory();
  h.push('a'); h.push('b'); h.undo('c');
  h.applying = true;
  h.reset();
  assert.deepEqual(h.undoStack, []);
  assert.deepEqual(h.redoStack, []);
  assert.equal(h.applying, false);
});

// =============================================================================
// NoteAutoSaveTimer
// =============================================================================

test('NoteAutoSaveTimer: schedule fires onFlush after delayMs', () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.schedule();
  assert.equal(flushed, 0);
  mock.timers.tick(999);
  assert.equal(flushed, 0);
  mock.timers.tick(1);
  assert.equal(flushed, 1);
  mock.timers.reset();
});

test('NoteAutoSaveTimer: schedule replaces a pending timer', () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.schedule();
  mock.timers.tick(500);
  t.schedule(); // resets the clock
  mock.timers.tick(500);
  assert.equal(flushed, 0, 'should not have fired — timer was reset');
  mock.timers.tick(500);
  assert.equal(flushed, 1);
  mock.timers.reset();
});

test('NoteAutoSaveTimer: schedule emits unsaved status and marks dirty', () => {
  const statuses = [];
  const t = new NoteAutoSaveTimer({
    delayMs: 1000,
    onFlush: () => {},
    onStatusChange: (s) => statuses.push(s),
  });
  t.schedule();
  assert.deepEqual(statuses, ['unsaved']);
  assert.equal(t.isDirty(), true);
  t.cancel();
});

test('NoteAutoSaveTimer: flushImmediate fires onFlush right away when dirty', () => {
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.schedule();
  t.flushImmediate();
  assert.equal(flushed, 1);
});

test('NoteAutoSaveTimer: flushImmediate is a no-op when clean', () => {
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.flushImmediate();
  assert.equal(flushed, 0);
});

test('NoteAutoSaveTimer: cancel stops a pending timer without flushing', () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.schedule();
  t.cancel();
  mock.timers.tick(2000);
  assert.equal(flushed, 0);
  mock.timers.reset();
});

test('NoteAutoSaveTimer: markClean clears dirty and emits saved status', () => {
  const statuses = [];
  const t = new NoteAutoSaveTimer({
    delayMs: 1000,
    onFlush: () => {},
    onStatusChange: (s) => statuses.push(s),
  });
  t.schedule(); // dirty + 'unsaved'
  t.markClean();
  assert.deepEqual(statuses, ['unsaved', 'saved']);
  assert.equal(t.isDirty(), false);
  t.cancel();
});

test('NoteAutoSaveTimer: reset cancels timer and clears dirty', () => {
  mock.timers.enable({ apis: ['setTimeout'] });
  let flushed = 0;
  const t = new NoteAutoSaveTimer({ delayMs: 1000, onFlush: () => flushed++ });
  t.schedule();
  t.reset();
  mock.timers.tick(2000);
  assert.equal(flushed, 0);
  assert.equal(t.isDirty(), false);
  mock.timers.reset();
});

// =============================================================================
// pointerDragged
// =============================================================================

test('pointerDragged: returns false when origin is null', () => {
  assert.equal(pointerDragged(null, { clientX: 10, clientY: 10 }), false);
  assert.equal(pointerDragged(undefined, { clientX: 10, clientY: 10 }), false);
});

test('pointerDragged: returns false for movement within threshold', () => {
  const origin = { x: 100, y: 100 };
  assert.equal(pointerDragged(origin, { clientX: 100, clientY: 100 }), false);
  assert.equal(pointerDragged(origin, { clientX: 102, clientY: 103 }), false);
  assert.equal(pointerDragged(origin, { clientX: 104, clientY: 104 }), false);
});

test('pointerDragged: returns true once movement exceeds 4px in either axis', () => {
  const origin = { x: 100, y: 100 };
  assert.equal(pointerDragged(origin, { clientX: 105, clientY: 100 }), true);
  assert.equal(pointerDragged(origin, { clientX: 100, clientY: 105 }), true);
  assert.equal(pointerDragged(origin, { clientX: 95, clientY: 95 }), true);
});

test('pointerDragged: respects custom threshold', () => {
  const origin = { x: 100, y: 100 };
  assert.equal(pointerDragged(origin, { clientX: 105, clientY: 100 }, 10), false);
  assert.equal(pointerDragged(origin, { clientX: 111, clientY: 100 }, 10), true);
});

test('pointerDragged: returns false for null event', () => {
  assert.equal(pointerDragged({ x: 0, y: 0 }, null), false);
});

// =============================================================================
// escapeHtml
// =============================================================================

test('escapeHtml: escapes the five HTML special characters', () => {
  assert.equal(escapeHtml('& < > " \''), '&amp; &lt; &gt; &quot; &#39;');
});

test('escapeHtml: leaves plain text alone', () => {
  assert.equal(escapeHtml('Hello world 123'), 'Hello world 123');
});

test('escapeHtml: handles null and undefined as empty string', () => {
  assert.equal(escapeHtml(null), '');
  assert.equal(escapeHtml(undefined), '');
});

test('escapeHtml: stringifies non-string inputs', () => {
  assert.equal(escapeHtml(42), '42');
  assert.equal(escapeHtml(true), 'true');
});

test('escapeHtml: prevents XSS via injected tags', () => {
  assert.equal(
    escapeHtml('<script>alert("x")</script>'),
    '&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;',
  );
});

// =============================================================================
// renderEditingLine / renderEditingRange
// =============================================================================

test('renderEditingLine: contains data-line-index and the kind class', () => {
  const html = renderEditingLine('# Heading', 5);
  assert.match(html, /data-line-index="5"/);
  assert.match(html, /is-heading-1/);
  assert.match(html, /<textarea/);
});

test('renderEditingLine: escapes HTML in the line content', () => {
  const html = renderEditingLine('<script>', 0);
  assert.match(html, /&lt;script&gt;/);
  // Make sure no unescaped <script> tag leaked in.
  assert.doesNotMatch(html, /<script>/);
});

test('renderEditingLine: empty line still emits a textarea', () => {
  const html = renderEditingLine('', 0);
  assert.match(html, /<textarea/);
  assert.match(html, /data-line-index="0"/);
});

test('renderEditingRange: includes start and end indices on textarea', () => {
  const html = renderEditingRange('line one\nline two', 3, 4);
  assert.match(html, /data-line-start="3"/);
  assert.match(html, /data-line-end="4"/);
  assert.match(html, /is-block-editing/);
});

test('renderEditingRange: escapes HTML in the markdown content', () => {
  const html = renderEditingRange('<b>bold</b>', 0, 0);
  assert.match(html, /&lt;b&gt;bold&lt;\/b&gt;/);
});

// =============================================================================
// renderMarkdown* (fallback path — node has no window.marked)
// =============================================================================

test('renderMarkdown: empty text returns the No-content placeholder', () => {
  assert.match(renderMarkdown(''), /No content/);
  assert.match(renderMarkdown(null), /No content/);
});

test('renderMarkdown fallback: converts headings and bold/italic', () => {
  const html = renderMarkdown('# Title\n\nSome **bold** and *italic*.');
  assert.match(html, /<h1>Title<\/h1>/);
  assert.match(html, /<strong>bold<\/strong>/);
  assert.match(html, /<em>italic<\/em>/);
});

test('renderMarkdown fallback: produces inline code', () => {
  const html = renderMarkdown('Use `foo()` to call.');
  assert.match(html, /<code>foo\(\)<\/code>/);
});

test('renderMarkdownLine: empty line returns <br>', () => {
  assert.equal(renderMarkdownLine(''), '<br>');
  assert.equal(renderMarkdownLine(null), '<br>');
});

test('renderMarkdownLine fallback: delegates to renderMarkdown', () => {
  // No window.marked in node, so fallback path runs.
  const html = renderMarkdownLine('# Hello');
  assert.match(html, /<h1>Hello<\/h1>/);
});

test('renderInlineMarkdown: empty input returns empty string', () => {
  assert.equal(renderInlineMarkdown(''), '');
  assert.equal(renderInlineMarkdown(null), '');
});

test('renderInlineMarkdown fallback: just escapes HTML', () => {
  assert.equal(renderInlineMarkdown('<b>bold</b>'), '&lt;b&gt;bold&lt;/b&gt;');
});

// =============================================================================
// renderHeadingLine / renderTaskLine / renderRenderedLine
// =============================================================================

test('renderHeadingLine: returns empty string for non-headings', () => {
  assert.equal(renderHeadingLine('plain text', 0, false), '');
  assert.equal(renderHeadingLine('- bullet', 0, false), '');
});

test('renderHeadingLine: emits expanded chevron when not collapsed', () => {
  const html = renderHeadingLine('# Heading', 5, false);
  assert.match(html, /aria-expanded="true"/);
  assert.match(html, /Collapse section/);
  assert.match(html, /⌄/);
  assert.doesNotMatch(html, /note-heading-fold-summary/);
});

test('renderHeadingLine: emits collapsed chevron + summary when collapsed', () => {
  const html = renderHeadingLine('## Heading', 5, true);
  assert.match(html, /aria-expanded="false"/);
  assert.match(html, /Expand section/);
  assert.match(html, /›/);
  assert.match(html, /note-heading-fold-summary/);
});

test('renderTaskLine: returns empty string for non-task lines', () => {
  assert.equal(renderTaskLine('plain', 0), '');
  assert.equal(renderTaskLine('- bullet', 0), '');
});

test('renderTaskLine: unchecked task has no checked attribute', () => {
  const html = renderTaskLine('- [ ] do thing', 3);
  assert.match(html, /<input[^>]*type="checkbox"/);
  assert.doesNotMatch(html, /<input[^>]*\schecked\s/);
  assert.match(html, /data-line-index="3"/);
});

test('renderTaskLine: checked task includes the checked attribute', () => {
  const html = renderTaskLine('- [x] done', 0);
  assert.match(html, /\schecked\s/);
});

test('renderRenderedLine: empty line collapses to <br> wrapper', () => {
  const html = renderRenderedLine('', 0, false);
  assert.match(html, /<br>/);
  assert.match(html, /is-empty/);
});

test('renderRenderedLine: heading wins over task / plain', () => {
  const html = renderRenderedLine('# H', 0, false);
  assert.match(html, /note-heading-line/);
  assert.doesNotMatch(html, /note-task-line/);
});

test('renderRenderedLine: task line precedes plain', () => {
  const html = renderRenderedLine('- [ ] task', 0, false);
  assert.match(html, /note-task-line/);
  assert.doesNotMatch(html, /note-heading-line/);
});

test('renderRenderedLine: data-line-index propagated', () => {
  const html = renderRenderedLine('plain text', 7, false);
  assert.match(html, /data-line-index="7"/);
});

// =============================================================================
// lineIndexAtPosition / startOfLine — round-trip helpers for TOC nav
// =============================================================================

test('lineIndexAtPosition: position 0 returns line 0', () => {
  assert.equal(lineIndexAtPosition('# A\nbody\n# B', 0), 0);
});

test('lineIndexAtPosition: position on second line returns 1', () => {
  const src = '# A\nbody';
  assert.equal(lineIndexAtPosition(src, src.indexOf('body')), 1);
});

test('lineIndexAtPosition: position past end returns last line', () => {
  const src = '# A\n# B\n# C';
  assert.equal(lineIndexAtPosition(src, src.length), 2);
});

test('lineIndexAtPosition: counts \\n correctly across multiple lines', () => {
  const src = '0\n1\n2\n3\n4';
  assert.equal(lineIndexAtPosition(src, src.indexOf('3')), 3);
});

test('startOfLine: line 0 returns 0', () => {
  assert.equal(startOfLine('# A\nbody', 0), 0);
});

test('startOfLine: returns offset of first char on requested line', () => {
  const src = '# A\nbody\n# B';
  assert.equal(startOfLine(src, 1), 4);
  assert.equal(startOfLine(src, 2), 9);
});

test('startOfLine: lineIndex past last line clamps to start of final line', () => {
  // Behavior: walk newlines until we run out, then return wherever the cursor
  // landed. Effectively "start of the last reachable line". Used by the TOC
  // active-section lookup which queries by data-position — the final line's
  // start matches the last TOC entry, which is what we want.
  const src = '# A\n# B';
  assert.equal(startOfLine(src, 99), 4); // start of "# B"
});

test('lineIndexAtPosition + startOfLine round-trip on heading positions', () => {
  const src = '# Title\nbody\n## Sub\nmore\n### Detail\n';
  for (const heading of ['# Title', '## Sub', '### Detail']) {
    const pos = src.indexOf(heading);
    const line = lineIndexAtPosition(src, pos);
    assert.equal(startOfLine(src, line), pos, `round-trip failed for ${heading}`);
  }
});

// =============================================================================
// pruneCollapsedHeadings
// =============================================================================

test('pruneCollapsedHeadings: drops indexes outside the array', () => {
  const lines = ['# A', 'body', '# B'];
  const set = new Set([0, 2, 5, 99]);
  pruneCollapsedHeadings(lines, set);
  assert.deepEqual([...set].sort((a, b) => a - b), [0, 2]);
});

test('pruneCollapsedHeadings: drops indexes that no longer point at headings', () => {
  // Index 1 used to be a heading; current content has plain text there.
  const lines = ['# A', 'body', '# B'];
  const set = new Set([0, 1, 2]);
  pruneCollapsedHeadings(lines, set);
  assert.deepEqual([...set].sort((a, b) => a - b), [0, 2]);
});

test('pruneCollapsedHeadings: keeps valid heading indexes intact', () => {
  const lines = ['# A', '## B', '### C'];
  const set = new Set([0, 1, 2]);
  pruneCollapsedHeadings(lines, set);
  assert.equal(set.size, 3);
});

test('pruneCollapsedHeadings: handles missing set argument gracefully', () => {
  // No assertion — just shouldn't throw.
  pruneCollapsedHeadings(['# A'], null);
  pruneCollapsedHeadings(['# A'], undefined);
});

test('pruneCollapsedHeadings: drops non-integer entries', () => {
  const lines = ['# A'];
  const set = new Set([0, '0', 1.5, NaN]);
  pruneCollapsedHeadings(lines, set);
  assert.deepEqual([...set], [0]);
});

// =============================================================================
// buildLiveEditorHTML
// =============================================================================

test('buildLiveEditorHTML: every line gets a wrapper when nothing is folded or active', () => {
  const html = buildLiveEditorHTML(['# A', 'body', '# B']);
  assert.match(html, /data-line-index="0"/);
  assert.match(html, /data-line-index="1"/);
  assert.match(html, /data-line-index="2"/);
  // No editing inputs since no active line/range was passed.
  assert.doesNotMatch(html, /<textarea/);
});

test('buildLiveEditorHTML: hides lines under a collapsed heading until next sibling-or-shallower', () => {
  const lines = ['# A', 'body A', '## A.1', 'body A1', '# B', 'body B'];
  const collapsed = new Set([0]); // collapse the first heading
  const html = buildLiveEditorHTML(lines, { collapsedHeadings: collapsed });
  // Indices 0 (the heading itself) and 4 (next # heading) are visible;
  // indices 1, 2, 3 are hidden (under the collapsed h1).
  assert.match(html, /data-line-index="0"/);
  assert.doesNotMatch(html, /data-line-index="1"/);
  assert.doesNotMatch(html, /data-line-index="2"/);
  assert.doesNotMatch(html, /data-line-index="3"/);
  assert.match(html, /data-line-index="4"/);
  assert.match(html, /data-line-index="5"/);
});

test('buildLiveEditorHTML: activeLineIndex renders that line as a textarea', () => {
  const html = buildLiveEditorHTML(['# A', 'body'], { activeLineIndex: 1 });
  // Active line gets a single-line input; the other stays rendered.
  assert.match(html, /<textarea[^>]*data-line-index="1"/);
  assert.doesNotMatch(html, /<textarea[^>]*data-line-index="0"/);
});

test('buildLiveEditorHTML: activeRange renders one block textarea covering the range', () => {
  const html = buildLiveEditorHTML(['line0', 'line1', 'line2', 'line3'], {
    activeRange: { start: 1, end: 2 },
  });
  // Block textarea spans lines 1..2.
  assert.match(html, /data-line-start="1"[^>]*data-line-end="2"/);
  // Lines 0 and 3 stay rendered (not in editing mode).
  assert.match(html, /note-live-line-rendered[^"]*"\s+data-line-index="0"/);
  assert.match(html, /note-live-line-rendered[^"]*"\s+data-line-index="3"/);
});

test('buildLiveEditorHTML: collapsed heading at the same level reopens visibility on the next sibling', () => {
  // h1 collapsed → all under it hidden until the next h1.
  const lines = ['# A', 'body A', '## A.1', 'body A1', '# B'];
  const collapsed = new Set([0]);
  const html = buildLiveEditorHTML(lines, { collapsedHeadings: collapsed });
  assert.match(html, /data-line-index="0"/);
  assert.match(html, /data-line-index="4"/);
  // Anything between is hidden.
  assert.doesNotMatch(html, /data-line-index="1"/);
  assert.doesNotMatch(html, /data-line-index="2"/);
  assert.doesNotMatch(html, /data-line-index="3"/);
});

test('buildLiveEditorHTML: deeper-than-collapsed heading stays hidden, shallower-or-equal reopens', () => {
  // h2 collapsed → its h3 child is hidden, but the next h1 is still visible.
  const lines = ['## A', 'body', '### A.1', 'body A1', '# Top'];
  const collapsed = new Set([0]);
  const html = buildLiveEditorHTML(lines, { collapsedHeadings: collapsed });
  assert.match(html, /data-line-index="0"/);
  assert.doesNotMatch(html, /data-line-index="1"/);
  assert.doesNotMatch(html, /data-line-index="2"/);
  assert.doesNotMatch(html, /data-line-index="3"/);
  assert.match(html, /data-line-index="4"/);
});

// =============================================================================
// NoteLiveEditorState
// =============================================================================

test('NoteLiveEditorState: defaults are all null/empty', () => {
  const s = new NoteLiveEditorState();
  assert.equal(s.activeLineIndex, null);
  assert.equal(s.activeRange, null);
  assert.equal(s.selectionAnchorIndex, null);
  assert.equal(s.selectionFocusIndex, null);
  assert.equal(s.pointerDown, null);
  assert.ok(s.collapsedHeadings instanceof Set);
  assert.equal(s.collapsedHeadings.size, 0);
});

test('NoteLiveEditorState: reset clears every field', () => {
  const s = new NoteLiveEditorState();
  s.activeLineIndex = 3;
  s.activeRange = { start: 1, end: 4 };
  s.selectionAnchorIndex = 1;
  s.selectionFocusIndex = 4;
  s.pointerDown = { x: 10, y: 20, lineIndex: 0 };
  s.collapsedHeadings.add(0);
  s.collapsedHeadings.add(2);

  s.reset();

  assert.equal(s.activeLineIndex, null);
  assert.equal(s.activeRange, null);
  assert.equal(s.selectionAnchorIndex, null);
  assert.equal(s.selectionFocusIndex, null);
  assert.equal(s.pointerDown, null);
  assert.equal(s.collapsedHeadings.size, 0);
});

test('NoteLiveEditorState: clearSelectionFocus clears focus only, not anchor', () => {
  const s = new NoteLiveEditorState();
  s.selectionAnchorIndex = 2;
  s.selectionFocusIndex = 5;
  s.clearSelectionFocus();
  assert.equal(s.selectionAnchorIndex, 2);
  assert.equal(s.selectionFocusIndex, null);
});

test('NoteLiveEditorState: toggleHeadingFold flips and returns the new state', () => {
  const s = new NoteLiveEditorState();
  assert.equal(s.toggleHeadingFold(3), true);
  assert.ok(s.collapsedHeadings.has(3));
  assert.equal(s.toggleHeadingFold(3), false);
  assert.equal(s.collapsedHeadings.has(3), false);
});

test('NoteLiveEditorState: hasActiveEdit checks both activeLineIndex and activeRange', () => {
  const s = new NoteLiveEditorState();
  assert.equal(s.hasActiveEdit(), false);
  s.activeLineIndex = 0;
  assert.equal(s.hasActiveEdit(), true);
  s.activeLineIndex = null;
  s.activeRange = { start: 1, end: 2 };
  assert.equal(s.hasActiveEdit(), true);
});

// =============================================================================
// NoteLiveEditor — mock-host tests
// =============================================================================

function mockHost(initialLines) {
  let lines = [...initialLines];
  const calls = { pushUndo: 0, scheduleAutoSave: 0, clearWindowSelection: 0, render: [] };
  return {
    host: {
      getContent: () => lines.join('\n'),
      getContentLines: () => lines,
      setContentLines: (next) => { lines = [...next]; },
      pushUndo: () => { calls.pushUndo += 1; },
      scheduleAutoSave: () => { calls.scheduleAutoSave += 1; },
      render: (opts) => { calls.render.push(opts || null); },
      clearWindowSelection: () => { calls.clearWindowSelection += 1; },
    },
    get lines() { return lines; },
    calls,
  };
}

test('NoteLiveEditor: activate sets active line and renders with focus opts', () => {
  const { host, calls } = mockHost(['# A', 'body', '# B']);
  const ed = new NoteLiveEditor(host);
  ed.activate(2, 3);
  assert.equal(ed.state.activeLineIndex, 2);
  assert.equal(ed.state.selectionAnchorIndex, 2);
  assert.equal(ed.state.activeRange, null);
  assert.deepEqual(calls.render, [{ focusLineIndex: 2, cursorPosition: 3 }]);
});

test('NoteLiveEditor: toggleHeadingFold flips on heading lines, no-op on plain lines', () => {
  const { host, calls } = mockHost(['# A', 'plain', '## B']);
  const ed = new NoteLiveEditor(host);
  ed.toggleHeadingFold(0);
  assert.ok(ed.state.collapsedHeadings.has(0));
  assert.equal(calls.render.length, 1);

  ed.toggleHeadingFold(1); // not a heading
  assert.equal(ed.state.collapsedHeadings.size, 1);
  assert.equal(calls.render.length, 1, 'render should not have fired');
});

test('NoteLiveEditor: toggleTaskLine flips marker and pushes undo', () => {
  const { host, lines, calls } = mockHost(['- [ ] task', 'plain']);
  const ed = new NoteLiveEditor(host);
  ed.toggleTaskLine(0, true);
  assert.equal(host.getContentLines()[0], '- [x] task');
  assert.equal(calls.pushUndo, 1);
  assert.equal(calls.scheduleAutoSave, 1);
});

test('NoteLiveEditor: toggleTaskLine no-ops on non-task lines', () => {
  const { host, calls } = mockHost(['plain']);
  const ed = new NoteLiveEditor(host);
  ed.toggleTaskLine(0, true);
  assert.equal(calls.pushUndo, 0);
  assert.equal(host.getContentLines()[0], 'plain');
});

test('NoteLiveEditor: deleteRange removes lines and reactivates start', () => {
  const { host, calls } = mockHost(['line0', 'line1', 'line2', 'line3']);
  const ed = new NoteLiveEditor(host);
  ed.deleteRange({ start: 1, end: 2 });
  assert.deepEqual(host.getContentLines(), ['line0', 'line3']);
  assert.equal(calls.pushUndo, 1);
  // activate at clamped start with cursorPosition 0
  assert.equal(ed.state.activeLineIndex, 1);
});

test('NoteLiveEditor: deleteRange leaves at least one (empty) line', () => {
  const { host } = mockHost(['only']);
  const ed = new NoteLiveEditor(host);
  ed.deleteRange({ start: 0, end: 0 });
  assert.deepEqual(host.getContentLines(), ['']);
});

test('NoteLiveEditor: replaceRange swaps multi-line span for single replacement', () => {
  const { host, calls } = mockHost(['a', 'b', 'c', 'd']);
  const ed = new NoteLiveEditor(host);
  ed.replaceRange({ start: 1, end: 2 }, 'replacement');
  assert.deepEqual(host.getContentLines(), ['a', 'replacement', 'd']);
  assert.equal(calls.pushUndo, 1);
  assert.equal(ed.state.activeLineIndex, 1);
});

test('NoteLiveEditor: editRange enters block-edit mode without mutating content', () => {
  const { host, calls } = mockHost(['a', 'b', 'c']);
  const ed = new NoteLiveEditor(host);
  ed.editRange({ start: 0, end: 1 });
  assert.deepEqual(host.getContentLines(), ['a', 'b', 'c']);
  assert.deepEqual(ed.state.activeRange, { start: 0, end: 1 });
  assert.equal(ed.state.activeLineIndex, null);
  assert.equal(calls.pushUndo, 0);
  // render fires with cursorPosition at end of joined content ("a\nb" length = 3)
  assert.equal(calls.render[0]?.cursorPosition, 3);
});

test('NoteLiveEditor: clearSelection clears window selection AND state focus', () => {
  const { host, calls } = mockHost(['a']);
  const ed = new NoteLiveEditor(host);
  ed.state.selectionFocusIndex = 5;
  ed.clearSelection();
  assert.equal(ed.state.selectionFocusIndex, null);
  assert.equal(calls.clearWindowSelection, 1);
});
