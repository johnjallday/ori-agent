// Tests for the "Leaving" part of the picture (file-janitor-picture.js) and the
// receipt's split across workspaces (file-janitor-receipt.js). Inline DOM stub,
// no jsdom.
//
// Run with: node --test internal/web/static/js/modules/file-janitor-picture-sends.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  fileButtonModel,
  parseChoice,
  renderPicture,
  rowMenuOptions,
  rowMenuValue,
  rowNote,
  sendTotals
} from './file-janitor-picture.js';
import {
  locationText,
  receiptModelFromReceipt,
  receiptModelFromResult,
  receiptWhere,
  renderReceipt
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
    this.checked = false;
    this.indeterminate = false;
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
const byId = (node, id) => node.all(item => item.id === id)[0];
const buttonNamed = (node, label) =>
  node.all(item => item.tagName === 'BUTTON' && item.textContent === label)[0];

const BANNED =
  /\b(?:linked|batch(?:es)?|candidates?|proposals?|exact(?:ly)?|explicitly|drafts?)\b/i;

const file = (name, extra = {}) => ({
  id: 'id-' + name,
  name,
  size: 10,
  placement: 'leaving',
  ...extra
});

function leavingCard(over = {}) {
  return {
    connection_id: 'connection-1',
    receiver_workspace_id: 'ws-2',
    receiver_name: 'Documents',
    kinds: ['documents'],
    group_state: 'ticked',
    count: 14,
    files: [file('Invoice-Acme.pdf'), file('Lease-signed.pdf'), file('2025-tax-return.pdf')],
    not_sent_count: 0,
    not_sent_files: [],
    kept_count: 0,
    ...over
  };
}

function pictureFixture(over = {}) {
  return {
    batch_id: 'batch-1',
    revision: 'a'.repeat(64),
    draft_revision: 'b'.repeat(64),
    draft_state: 'default',
    content_read: 'none',
    counts: { candidates: 54, filing: 43, questions: 11, kept: 0, applied: 0 },
    waiting: 54,
    folders: [
      {
        category: 'images',
        destination: 'Filed/Images',
        group_id: 'g-img',
        group_state: 'ticked',
        count: 29,
        files: [file('shot.png', { placement: 'filed' })],
        unfiled_count: 0,
        unfiled_files: []
      }
    ],
    loose: [
      {
        group_id: 'g-q',
        key: 'unmatched',
        count: 11,
        placed_count: 0,
        trash_count: 0,
        loose_count: 11,
        singles: [file('mystery.qqq', { placement: 'loose' })],
        more: 10
      }
    ],
    left_out: { count: 0, files: [] },
    trash: { count: 0, files: [] },
    could_not_file: { count: 0, files: [] },
    leaving: [leavingCard()],
    ...over
  };
}

const plan = (over = {}) => ({
  move_count: 29,
  send_count: 14,
  trash_count: 0,
  total: 43,
  over_limit: false,
  sends: [
    {
      connection_id: 'connection-1',
      receiver_workspace_id: 'ws-2',
      receiver_name: 'Documents',
      count: 14,
      name_used: 0
    }
  ],
  ...over
});
const ctx = (over = {}) => ({
  folderName: 'Downloads',
  editsAvailable: true,
  more: null,
  filedRoot: 'Filed',
  destinations: [
    { id: 'documents', label: 'Documents' },
    { id: 'images', label: 'Images' }
  ],
  ...over
});

test('with a connection, "Leaving Downloads" is drawn above the folder (FR 20)', () => {
  const root = renderPicture(doc, pictureFixture(), ctx());
  const titles = root.all(item => item.tagName === 'H3').map(item => item.textContent);
  assert.deepEqual(titles.slice(0, 2), ['Leaving Downloads', 'Downloads, after filing']);
});

test('without a connection, nothing about sending is drawn (success measure 3)', () => {
  const root = renderPicture(doc, pictureFixture({ leaving: undefined }), ctx());
  assert.doesNotMatch(root.textContent, /Leaving|Send to|workspace/);
  const menu = root.all(item => item.tagName === 'SELECT')[0];
  assert.ok(!menu.children.some(option => /Send to/.test(option.textContent)));
});

test('a Leaving card has a tick, the receiver, the count, three names and where they land (FR 21, 22)', () => {
  const ticks = [];
  const root = renderPicture(
    doc,
    pictureFixture(),
    ctx({ onTickLeaving: (card, on) => ticks.push(on) })
  );
  const box = byId(root, 'fileJanitorLeaving-connection-1');
  assert.equal(box.type, 'checkbox');
  assert.ok(box.checked, 'ticked when the picture opens');
  assert.equal(box.getAttribute('aria-label'), 'Send 14 files to the Documents workspace');
  const card = root.all(item => item.getAttribute('data-fj-leaving') === 'connection-1')[0];
  assert.match(card.textContent, /Documents/);
  assert.match(card.textContent, /workspace/);
  assert.match(card.textContent, /14/);
  assert.match(card.textContent, /Invoice-Acme\.pdf/);
  assert.match(card.textContent, /and 11 more/);
  assert.match(
    card.textContent,
    /They land at the top level of Documents\. Nothing already there is touched\./
  );
  box.checked = false;
  box.fire('change');
  assert.deepEqual(ticks, [false]);
  assert.doesNotMatch(card.textContent, BANNED);
});

test('"Keep these in Downloads instead" is a quiet text button, then "Send them to Documents" switches back (FR 23)', () => {
  const calls = [];
  const on = ctx({
    onKeepLeaving: () => calls.push('keep'),
    onSendLeaving: () => calls.push('send')
  });
  const keep = buttonNamed(
    renderPicture(doc, pictureFixture(), on),
    'Keep these in Downloads instead'
  );
  assert.equal(keep.className, 'fj-link');
  keep.fire('click');
  const kept = renderPicture(
    doc,
    pictureFixture({
      leaving: [leavingCard({ count: 0, files: [], group_state: 'none', kept_count: 14 })]
    }),
    on
  );
  assert.match(kept.textContent, /Nothing is leaving for Documents\./);
  buttonNamed(kept, 'Send them to Documents').fire('click');
  assert.deepEqual(calls, ['keep', 'send']);
});

test('the card says how many will stay because the name is used (FR 25)', () => {
  const root = renderPicture(
    doc,
    pictureFixture(),
    ctx({ plan: plan({ sends: [{ ...plan().sends[0], name_used: 2 }] }) })
  );
  assert.match(
    root.textContent,
    /2 files will stay in Downloads: the name is already used in Documents\./
  );
});

test('the footer names both totals and the button counts both (FR 26)', () => {
  assert.deepEqual(sendTotals(pictureFixture(), plan(), 'Downloads', 'Filed'), [
    '14 to Documents',
    '29 to Downloads/Filed',
    '11 stay loose'
  ]);
  const root = renderPicture(doc, pictureFixture(), ctx({ plan: plan() }));
  assert.match(root.textContent, /14 to Documents · 29 to Downloads\/Filed · 11 stay loose/);
  assert.equal(fileButtonModel({ plan: plan(), planCurrent: true }).label, 'File 43 files');
  assert.equal(
    fileButtonModel({
      plan: { ...plan(), send_count: undefined, sends: undefined, total: 29 },
      planCurrent: true
    }).label,
    'File 29 files'
  );
});

test('a question row menu gains Send to <receiver> and, last, Send to another workspace… (FR 27)', () => {
  const leaving = [leavingCard()];
  const options = rowMenuOptions(ctx().destinations, 'leave', leaving).map(option => option.label);
  assert.deepEqual(options.slice(-2), ['Send to Documents', 'Send to another workspace…']);
  assert.deepEqual(parseChoice('send:connection-1'), {
    kind: 'send',
    connectionId: 'connection-1'
  });
  assert.deepEqual(parseChoice('connect'), { kind: 'connect' });
  assert.equal(
    rowMenuValue({ count: 3, placed_count: 0, sent_count: 3 }, leaving),
    'send:connection-1'
  );
  assert.equal(rowMenuValue({ count: 3, placed_count: 0, sent_count: 1 }, leaving), 'mixed');
  assert.equal(
    rowNote({ count: 3, placed_count: 0, sent_count: 3 }, { leaving }),
    'All going to Documents'
  );
  assert.deepEqual(
    rowMenuOptions(ctx().destinations, 'leave').map(option => option.label),
    ['Leave here', 'Leave here, stop asking', 'Move to Documents', 'Move to Images']
  );
});

test('files of a carried kind whose card is unticked are listed as not sent', () => {
  const root = renderPicture(
    doc,
    pictureFixture({
      leaving: [
        leavingCard({
          count: 0,
          files: [],
          group_state: 'unticked',
          not_sent_count: 2,
          not_sent_files: [
            file('a.pdf', { placement: 'not_sent' }),
            file('b.pdf', { placement: 'not_sent' })
          ]
        })
      ]
    }),
    ctx()
  );
  const box = byId(root, 'fileJanitorLeaving-connection-1');
  assert.equal(box.checked, false);
  assert.match(root.textContent, /Not sent/);
  assert.match(root.textContent, /a\.pdf/);
});

test('the receipt splits what was filed and has one row per receiver with Open (FR 30, 31)', () => {
  const model = receiptModelFromReceipt({
    filing_id: 'filing-1',
    state: 'done',
    filed: 43,
    sent: 14,
    sends: [
      {
        receiver_workspace_id: 'ws-2',
        receiver_name: 'Documents',
        count: 14,
        receiver_link: '/workspaces/documents?view=details&panel=file-janitor&tab=review'
      }
    ],
    by_destination: [{ category: 'images', destination: 'Filed/Images', count: 29 }],
    not_moved: [],
    not_moved_total: 0,
    loose_now: 11,
    undo_available: true
  });
  assert.equal(
    receiptWhere(model, { folderName: 'Downloads', filingRoot: 'Filed' }),
    '14 went to Documents. 29 are in Downloads/Filed. 11 files stay loose in Downloads.'
  );
  const panel = renderReceipt(doc, model, { folderName: 'Downloads', filingRoot: 'Filed' });
  const open = panel.all(item => item.tagName === 'A')[0];
  assert.equal(open.textContent, 'Open Documents');
  assert.equal(open.href, '/workspaces/documents?view=details&panel=file-janitor&tab=review');
  const rows = panel.all(item => String(item.className).split(' ').includes('fj-receipt-row'));
  assert.match(rows[0].textContent, /14 .*Documents\s+workspace, top level/);
  assert.match(rows[1].textContent, /29 → Filed\/Images/);
  assert.doesNotMatch(panel.textContent, /→ \./, 'a send is never a "." destination');
  assert.doesNotMatch(panel.textContent, BANNED);
});

test('a link that is not a workspace page is not drawn', () => {
  const model = receiptModelFromReceipt({
    filed: 1,
    sent: 1,
    sends: [
      {
        receiver_workspace_id: 'ws-2',
        receiver_name: 'Documents',
        count: 1,
        receiver_link: 'https://example.com'
      }
    ],
    by_destination: []
  });
  assert.equal(renderReceipt(doc, model, {}).all(item => item.tagName === 'A').length, 0);
});

test('a press answer counts sends as filed, and a sent file not put back says where it is', () => {
  const model = receiptModelFromResult({
    filing_id: 'filing-1',
    outcomes: [
      { result: 'applied', operation: 'send', name: 'lease.pdf' },
      { result: 'applied', operation: 'move', destination: 'Filed/Images/a.png' }
    ]
  });
  assert.equal(model.filed, 2);
  assert.equal(model.sent, 1);
  assert.equal(locationText({ location: 'sent' }), 'It is still in the workspace it was sent to.');
});
