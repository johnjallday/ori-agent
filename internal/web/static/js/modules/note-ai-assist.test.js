// Tests for note-ai-assist.js helpers that do not require a browser DOM.
// Focus: the "Extract → note" action — title derivation, the wikilink splice,
// and the create-note + replace-selection orchestration through the bridge.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const assist = (await import('./note-ai-assist.js')).default;
const { deriveNoteTitle, buildExtractedContent, extractSelectionToNote } =
  await import('./note-ai-assist.js');

// makeFakeBridge wires a stand-in sessionsApi over a mutable note body and
// records the calls the extract flow makes. createNote echoes the requested
// title back as the saved name unless `savedName` overrides it (collision case).
function makeFakeBridge(initialContent, { savedName = null, failCreate = false } = {}) {
  let content = initialContent;
  const events = [];
  const bridge = {
    createNote: async (title, body, opts) => {
      events.push({ kind: 'create', title, body, opts });
      if (failCreate) return null;
      return { id: 'note-new', name: savedName || title };
    },
    getNoteContent: paneId => {
      events.push({ kind: 'get', paneId });
      return content;
    },
    setNoteContent: (value, paneId) => {
      events.push({ kind: 'set', value, paneId });
      content = value;
    },
    pushUndo: paneId => events.push({ kind: 'undo', paneId }),
    scheduleAutoSave: paneId => events.push({ kind: 'save', paneId }),
    showToast: (msg, kind) => events.push({ kind: 'toast', msg, level: kind })
  };
  return { bridge, events, getContent: () => content };
}

// seed sets the shared singleton state for one extract run.
function seed({ bridge, workspaceId = 'ws-1', selection }) {
  assist._state.sessionsApi = bridge || null;
  assist._state.workspaceId = workspaceId;
  assist._state.pendingSelection = selection || null;
}

// ---------------------------------------------------------------------------
// deriveNoteTitle
// ---------------------------------------------------------------------------

test('deriveNoteTitle: prefers the first markdown heading, stripped of markers', () => {
  assert.equal(deriveNoteTitle('## Description\ntesting'), 'Description');
  assert.equal(deriveNoteTitle('intro\n# Real Title\nbody'), 'Real Title');
});

test('deriveNoteTitle: falls back to first non-empty line, else Untitled', () => {
  assert.equal(deriveNoteTitle('\n\n  hello world  \nmore'), 'hello world');
  assert.equal(deriveNoteTitle('   \n\t\n'), 'Untitled');
  assert.equal(deriveNoteTitle(''), 'Untitled');
});

test('deriveNoteTitle: caps very long titles at 80 chars', () => {
  const long = 'x'.repeat(200);
  assert.equal(deriveNoteTitle(long).length, 80);
});

// ---------------------------------------------------------------------------
// buildExtractedContent
// ---------------------------------------------------------------------------

test('buildExtractedContent: replaces the char range with a wikilink', () => {
  const content = 'Hello SELECTED world';
  const range = { start: 6, end: 14 }; // "SELECTED"
  assert.equal(buildExtractedContent(content, range, 'Spun Off'), 'Hello [[Spun Off]] world');
});

test('buildExtractedContent: clamps out-of-bounds / reversed ranges', () => {
  assert.equal(buildExtractedContent('abc', { start: -5, end: 999 }, 'T'), '[[T]]');
  assert.equal(buildExtractedContent('abc', { start: 2, end: 1 }, 'T'), 'ab[[T]]c');
  assert.equal(buildExtractedContent('abc', {}, 'T'), '[[T]]abc');
});

// ---------------------------------------------------------------------------
// extractSelectionToNote orchestration
// ---------------------------------------------------------------------------

test('extractSelectionToNote: creates a note and swaps the selection for a wikilink', async () => {
  const body = '## Description\ntesting';
  const { bridge, events, getContent } = makeFakeBridge(`${body}\nkeep me`);
  seed({
    bridge,
    selection: { text: body, range: { start: 0, end: body.length }, paneId: 'primary' }
  });

  await extractSelectionToNote();

  const create = events.find(e => e.kind === 'create');
  assert.deepEqual(
    { title: create.title, body: create.body, ws: create.opts.workspaceId },
    { title: 'Description', body, ws: 'ws-1' }
  );
  // Source note now links to the new note where the selection used to be.
  assert.equal(getContent(), '[[Description]]\nkeep me');
  // Undo pushed before the edit; autosave scheduled after; routed to the pane.
  assert.ok(events.some(e => e.kind === 'undo' && e.paneId === 'primary'));
  assert.ok(events.some(e => e.kind === 'save' && e.paneId === 'primary'));
  assert.ok(events.some(e => e.kind === 'toast' && e.level === 'success'));
});

test('extractSelectionToNote: links to the backend-saved name on a collision', async () => {
  const body = 'Notes';
  const { bridge, getContent } = makeFakeBridge(`x ${body} y`, { savedName: 'Notes 2' });
  seed({
    bridge,
    selection: { text: body, range: { start: 2, end: 2 + body.length }, paneId: '' }
  });

  await extractSelectionToNote();

  assert.equal(getContent(), 'x [[Notes 2]] y');
});

test('extractSelectionToNote: surfaces an error and leaves content intact when create fails', async () => {
  const { bridge, events, getContent } = makeFakeBridge('alpha beta', { failCreate: true });
  seed({
    bridge,
    selection: { text: 'beta', range: { start: 6, end: 10 }, paneId: '' }
  });

  await extractSelectionToNote();

  assert.equal(getContent(), 'alpha beta'); // unchanged
  assert.ok(!events.some(e => e.kind === 'set'));
  assert.ok(events.some(e => e.kind === 'toast' && e.level === 'error'));
});

test('extractSelectionToNote: no-op without a usable selection', async () => {
  const { bridge, events } = makeFakeBridge('content');
  seed({ bridge, selection: null });
  await extractSelectionToNote();
  assert.equal(events.length, 0);

  seed({ bridge, selection: { text: '   ', range: { start: 0, end: 3 } } });
  await extractSelectionToNote();
  assert.equal(events.length, 0);
});

test('extractSelectionToNote: warns when the surface has no createNote host', async () => {
  const events = [];
  seed({
    bridge: { showToast: (msg, level) => events.push({ msg, level }) },
    selection: { text: 'beta', range: { start: 0, end: 4 } }
  });
  await extractSelectionToNote();
  assert.equal(events.length, 1);
  assert.equal(events[0].level, 'warning');
});
