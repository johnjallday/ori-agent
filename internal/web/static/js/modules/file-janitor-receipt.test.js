// Tests for file-janitor-receipt.js, the receipt and the Undone view. Inline DOM
// stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-receipt.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BANNED_WORDS,
  filesPhrase,
  lastFilingLine,
  locationText,
  receiptModelFromReceipt,
  receiptModelFromResult,
  receiptTitle,
  receiptWhere,
  renderLastFiling,
  renderReceipt,
  renderUndone,
  undoneTitle
} from './file-janitor-receipt.js';

class El {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.className = '';
    this._text = '';
    this.disabled = false;
  }
  get textContent() {
    return [this._text, ...this.children.map(child => child.textContent)].filter(Boolean).join(' ');
  }
  set textContent(value) {
    this._text = value;
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  setAttribute(key, value) {
    this.attrs[key] = value;
  }
  getAttribute(key) {
    return this.attrs[key];
  }
  addEventListener(event, fn) {
    (this.listeners[event] ||= []).push(fn);
  }
  fire(event) {
    (this.listeners[event] || []).forEach(fn => fn());
  }
  all(predicate, out = []) {
    if (predicate(this)) out.push(this);
    this.children.forEach(child => child.all(predicate, out));
    return out;
  }
}
const doc = { createElement: tag => new El(tag) };
const find = (node, predicate) => node.all(predicate);
const byId = (node, id) => find(node, item => item.id === id)[0];

const ctx = (over = {}) => ({ folderName: 'Downloads', filingRoot: 'Filed', ...over });
const model = (over = {}) => ({
  filingId: 'filing-1',
  state: 'done',
  filed: 63,
  trashed: 0,
  byDestination: [
    { destination: 'Filed/Documents', count: 30 },
    { destination: 'Filed/Images', count: 33 }
  ],
  notMoved: [],
  notMovedTotal: 0,
  notAttempted: 0,
  looseNow: 11,
  undoAvailable: true,
  historyTrimmed: false,
  ...over
});

test('the title says how many files were actually filed, and the Trash apart (R43)', () => {
  assert.equal(receiptTitle(model()), '63 files filed');
  assert.equal(receiptTitle(model({ filed: 1 })), '1 file filed');
  assert.equal(receiptTitle(model({ filed: 52, trashed: 3 })), '52 files filed · 3 moved to Trash');
  assert.equal(receiptTitle(model({ filed: 0, trashed: 1 })), '1 moved to Trash');
  assert.equal(receiptTitle(model({ filed: 0, trashed: 0 })), 'Nothing was filed');
  // An answer that cannot say what kind of change it was is never called "filed".
  assert.equal(
    receiptTitle(model({ filed: 1, trashed: 0, completed: 2 })),
    '1 file filed · 2 changes completed'
  );
  assert.equal(receiptTitle(model({ filed: 0, completed: 1 })), '1 change completed');
  assert.equal(filesPhrase(1), '1 file');
});

test('one line says where the files are and how many stay loose (R43)', () => {
  assert.equal(
    receiptWhere(model(), ctx()),
    'They are in Downloads/Filed. 11 files stay loose in Downloads.'
  );
  assert.equal(
    receiptWhere(model({ looseNow: 1 }), ctx()),
    'They are in Downloads/Filed. 1 file stays loose in Downloads.'
  );
  assert.equal(
    receiptWhere(model({ looseNow: 0 }), ctx()),
    'They are in Downloads/Filed. No files stay loose in Downloads.'
  );
  // Only Trash: nothing is "in Filed".
  assert.equal(
    receiptWhere(model({ filed: 0, trashed: 2, looseNow: 4 }), ctx()),
    '4 files stay loose in Downloads.'
  );
  // When the loose count is not known (the receipt could not be read) it is left out.
  assert.equal(receiptWhere(model({ looseNow: null }), ctx()), 'They are in Downloads/Filed.');
});

test('the receipt has one row per destination, Trash on its own row, and Undo all and Done (R43, R45)', () => {
  const calls = [];
  const root = renderReceipt(
    doc,
    model({ trashed: 1 }),
    ctx({
      onUndoAll: item => calls.push(['undo', item.filingId]),
      onDone: () => calls.push(['done'])
    })
  );
  assert.equal(root.getAttribute('aria-labelledby'), 'fileJanitorResultsSummary');
  assert.equal(
    byId(root, 'fileJanitorResultsSummary').textContent,
    '63 files filed · 1 moved to Trash.'
  );
  assert.deepEqual(
    find(root, node => node.className === 'fj-receipt-row').map(node => node.textContent),
    ['30 → Filed/Documents', '33 → Filed/Images', '1 → Trash']
  );
  assert.equal(find(root, node => node.className === 'fj-receipt-notmoved').length, 0);
  byId(root, 'fileJanitorUndoAll').fire('click');
  byId(root, 'fileJanitorReceiptDone').fire('click');
  assert.deepEqual(calls, [['undo', 'filing-1'], ['done']]);
});

test('Undo all is not offered when nothing can be put back, and both buttons wait while it runs', () => {
  assert.equal(
    byId(renderReceipt(doc, model({ undoAvailable: false }), ctx()), 'fileJanitorUndoAll'),
    undefined
  );
  const busy = renderReceipt(doc, model(), ctx({ busy: true }));
  assert.equal(byId(busy, 'fileJanitorUndoAll').disabled, true);
  assert.equal(byId(busy, 'fileJanitorUndoAll').textContent, 'Putting files back…');
  assert.equal(byId(busy, 'fileJanitorReceiptDone').disabled, true);
});

test('files that were not moved are listed with their reason, and counted (R43)', () => {
  const root = renderReceipt(
    doc,
    model({
      filed: 61,
      notMoved: [
        { name: 'a.pdf', reason: 'Ori could not move this file.' },
        { name: 'b.pdf', reason: 'This file changed after you looked, so Ori left it alone.' }
      ],
      notMovedTotal: 3
    }),
    ctx()
  );
  assert.equal(byId(root, 'fileJanitorResultsSummary').textContent, '61 files filed.');
  const section = find(root, node => node.className === 'fj-receipt-notmoved')[0];
  assert.equal(
    find(section, node => node.className === 'fj-receipt-notmoved-title')[0].textContent,
    '3 files were not moved'
  );
  assert.deepEqual(
    find(section, node => node.className.startsWith('fj-results-item')).map(
      node => node.textContent
    ),
    [
      'a.pdf  — Ori could not move this file.',
      'b.pdf  — This file changed after you looked, so Ori left it alone.'
    ]
  );
  assert.match(section.textContent, /and 1 more/);
  const single = renderReceipt(
    doc,
    model({ notMoved: [{ name: 'a.pdf', reason: 'x' }], notMovedTotal: 1 }),
    ctx()
  );
  assert.equal(
    find(single, node => node.className === 'fj-receipt-notmoved-title')[0].textContent,
    '1 file was not moved'
  );
});

test('an interrupted run says so and counts what it never tried', () => {
  const root = renderReceipt(doc, model({ state: 'interrupted', notAttempted: 4 }), ctx());
  assert.match(
    find(root, node => node.className === 'fj-receipt-note')[0].textContent,
    /Ori stopped before it finished: 4 files were not tried\./
  );
  const trimmed = renderReceipt(doc, model({ historyTrimmed: true }), ctx());
  assert.match(trimmed.textContent, /Ori no longer keeps the whole record/);
});

test('the receipt is read from the server or, failing that, built from the answer to the press', () => {
  const fromServer = receiptModelFromReceipt({
    filing_id: 'filing-9',
    state: 'done',
    filed: 2,
    trashed: 1,
    by_destination: [{ category: 'images', destination: 'Filed/Images', count: 2 }],
    not_moved: [{ name: 'x.pdf', reason: 'because' }],
    not_moved_total: 1,
    loose_now: 5,
    undo_available: true
  });
  assert.deepEqual(
    [
      fromServer.filingId,
      fromServer.filed,
      fromServer.trashed,
      fromServer.looseNow,
      fromServer.undoAvailable,
      fromServer.notMovedTotal
    ],
    ['filing-9', 2, 1, 5, true, 1]
  );
  const built = receiptModelFromResult({
    filing_id: 'filing-3',
    outcomes: [
      { name: 'a.png', operation: 'move', result: 'applied', destination: 'Filed/Images/a.png' },
      {
        name: 'b.png',
        operation: 'move',
        result: 'applied',
        destination: 'Filed/Images/b (2).png'
      },
      { name: 'c.zip', operation: 'trash', result: 'applied' },
      { name: 'd.pdf', operation: 'move', result: 'failed', message: 'Permission denied.' },
      { name: 'e.pdf', operation: 'move', result: 'stale' }
    ]
  });
  assert.deepEqual(
    [built.filed, built.trashed, built.notMovedTotal, built.looseNow, built.undoAvailable],
    [2, 1, 2, null, true]
  );
  assert.deepEqual(built.byDestination, [{ destination: 'Filed/Images', count: 2 }]);
  // A reply that cannot say what kind of change it was is not counted as filed, and
  // offers no Undo all when nothing is known to have moved or gone to the Trash.
  const unknown = receiptModelFromResult({
    filing_id: 'filing-4',
    replayed: true,
    outcomes: [{ name: 'old.dat', result: 'applied' }]
  });
  assert.deepEqual(
    [unknown.filed, unknown.completed, unknown.undoAvailable, unknown.replayed],
    [0, 1, false, true]
  );
  assert.deepEqual(
    built.notMoved.map(item => item.reason),
    ['Permission denied.', 'This file changed after you looked, so Ori left it alone.']
  );
});

test('the Undone view says how many are back and lists each file that is not, with why and where it is (R49)', () => {
  const calls = [];
  const result = {
    filing_id: 'filing-1',
    back: 2,
    not_back: 2,
    outcomes: [
      { name: 'a.png', result: 'undone' },
      { name: 'b.png', result: 'already_undone' },
      {
        name: 'c.png',
        result: 'failed',
        message: 'Something else is already using the original name, so Ori did not overwrite it.',
        location: 'filed',
        destination: 'Filed/Images/c.png'
      },
      {
        name: 'd.zip',
        result: 'failed',
        message: 'Ori could not put the file back.',
        location: 'trash'
      }
    ]
  };
  const root = renderUndone(
    doc,
    result,
    ctx({ onScanAgain: () => calls.push('scan'), onDone: () => calls.push('done') })
  );
  assert.equal(
    byId(root, 'fileJanitorUndoneTitle').textContent,
    '2 files are back where they were.'
  );
  assert.equal(
    find(root, node => node.className === 'fj-receipt-notmoved-title')[0].textContent,
    '2 files could not be put back'
  );
  const lines = find(root, node => node.className.startsWith('fj-results-item')).map(
    node => node.textContent
  );
  assert.match(
    lines[0],
    /^c\.png\s+— Something else is already using the original name.*It is still in Filed\/Images\/c\.png\.$/
  );
  assert.match(lines[1], /It is still in the Trash\.$/);
  // One button to review them again, and Done.
  assert.equal(byId(root, 'fileJanitorScanAgain').textContent, 'Scan again to review them');
  byId(root, 'fileJanitorScanAgain').fire('click');
  byId(root, 'fileJanitorUndoneDone').fire('click');
  assert.deepEqual(calls, ['scan', 'done']);
});

test('the Undone view when nothing came back offers no scan, and says so', () => {
  const none = renderUndone(
    doc,
    { back: 0, outcomes: [{ name: 'a', result: 'failed', message: 'no', location: 'unknown' }] },
    ctx()
  );
  assert.equal(byId(none, 'fileJanitorUndoneTitle').textContent, 'No files were put back.');
  assert.equal(byId(none, 'fileJanitorScanAgain'), undefined);
  assert.match(none.textContent, /Ori cannot find it\./);
  const replay = renderUndone(doc, { back: 1, replayed: true, outcomes: [] }, ctx());
  assert.equal(byId(replay, 'fileJanitorUndoneTitle').textContent, '1 file is back where it was.');
  assert.match(replay.textContent, /already done; nothing ran again/);
  assert.equal(
    byId(renderUndone(doc, { back: 1, outcomes: [] }, ctx({ busy: true })), 'fileJanitorScanAgain')
      .disabled,
    true
  );
});

test('where a file is, in words', () => {
  assert.equal(
    locationText({ location: 'filed', destination: 'Filed/Docs/x.pdf' }),
    'It is still in Filed/Docs/x.pdf.'
  );
  assert.equal(locationText({ location: 'filed' }), 'It is still where it was filed.');
  assert.equal(locationText({ location: 'trash' }), 'It is still in the Trash.');
  assert.equal(
    locationText({ location: 'folder' }),
    'A file with the original name is in the folder.'
  );
  assert.equal(locationText({ location: 'unknown' }), 'Ori cannot find it.');
  assert.equal(locationText({}), 'Ori cannot find it.');
  assert.equal(undoneTitle({ back: 0 }), 'No files were put back');
});

test('History gets one line for the last filing, with Undo all while it is on offer (R46)', () => {
  assert.equal(
    lastFilingLine({ filed: 62, trashed: 1 }, '3 min ago'),
    'Last filing · 63 files · 3 min ago'
  );
  assert.equal(lastFilingLine({ filed: 1, trashed: 0 }, ''), 'Last filing · 1 file');
  assert.equal(
    lastFilingLine({ filed: 5, undone: true }, '1 h ago'),
    'Last filing · 5 files · 1 h ago · put back'
  );
  assert.equal(lastFilingLine(null, 'x'), '');
  assert.equal(renderLastFiling(doc, null), null);
  const calls = [];
  const line = renderLastFiling(
    doc,
    { filing_id: 'filing-1', filed: 4, trashed: 0, undo_available: true },
    { when: 'just now', onUndoAll: summary => calls.push(summary.filing_id) }
  );
  assert.equal(line.id, 'fileJanitorLastFiling');
  byId(line, 'fileJanitorLastFilingUndo').fire('click');
  assert.deepEqual(calls, ['filing-1']);
  assert.equal(
    byId(
      renderLastFiling(doc, { filed: 4, undo_available: false }, {}),
      'fileJanitorLastFilingUndo'
    ),
    undefined
  );
});

test('no word on the receipt or the Undone view is a retired word', () => {
  const texts = [
    renderReceipt(
      doc,
      model({
        trashed: 2,
        state: 'interrupted',
        notAttempted: 2,
        historyTrimmed: true,
        notMoved: [{ name: 'a', reason: 'Ori could not move this file.' }],
        notMovedTotal: 4
      }),
      ctx()
    ).all(() => true),
    renderUndone(
      doc,
      {
        back: 1,
        replayed: true,
        interrupted: true,
        outcomes: [{ name: 'a', result: 'failed', message: 'x', location: 'folder' }]
      },
      ctx()
    ).all(() => true)
  ]
    .flat()
    .map(node => node._text)
    .join(' ')
    .toLowerCase();
  for (const word of BANNED_WORDS)
    assert.doesNotMatch(texts, new RegExp('\\b' + word + '\\b'), word);
});
