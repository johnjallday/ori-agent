import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createTranscript,
  replyViewportTop,
  survivingAnchor
} from './personal-assistant-transcript.js';

function fixture(heights = [100, 100]) {
  const frames = new Map();
  let id = 0;
  let prefix = 0;
  let open = true;
  const doc = { activeElement: null };
  const listeners = new Map();
  const events = name => ({
    addEventListener(type, fn) {
      listeners.set(`${name}:${type}`, fn);
    },
    removeEventListener(type) {
      listeners.delete(`${name}:${type}`);
    }
  });
  const pane = {
    ...events('pane'),
    scrollTop: 0,
    clientHeight: 300,
    offsetHeight: 300,
    contains: row => transcript.contains(row),
    get scrollHeight() {
      return prefix + transcript.children.reduce((sum, row) => sum + row.height, 0);
    },
    getBoundingClientRect: () => ({ top: 50, bottom: 350, height: 300 })
  };
  const transcript = {
    children: [],
    ownerDocument: doc,
    contains: row => transcript.children.includes(row),
    append(row) {
      transcript.children.push(row);
    }
  };
  const row = (height = 100, role = 'assistant') => ({
    dataset: { messageRole: role },
    height,
    getBoundingClientRect() {
      const top =
        50 +
        prefix +
        transcript.children
          .slice(0, transcript.children.indexOf(this))
          .reduce((sum, row) => sum + row.height, 0) -
        pane.scrollTop;
      return { top, bottom: top + this.height, height: this.height };
    },
    remove() {
      transcript.children.splice(transcript.children.indexOf(this), 1);
    },
    setAttribute() {},
    focus() {
      doc.activeElement = this;
    }
  });
  doc.createElement = () => row(30, 'pending');
  heights.forEach(height => transcript.append(row(height)));
  const jump = { ...events('jump'), hidden: true, ownerDocument: doc };
  const announce = { textContent: '' };
  const owner = createTranscript({
    pane,
    transcript,
    jump,
    announce,
    frame(fn) {
      frames.set(++id, fn);
      return id;
    },
    cancel: key => frames.delete(key),
    isOpen: () => open
  });
  return {
    owner,
    pane,
    transcript,
    jump,
    announce,
    doc,
    row,
    listeners,
    prefix(value) {
      prefix = value;
    },
    open(value) {
      open = value;
    },
    flush() {
      for (let i = 0; frames.size && i < 20; i++) {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach(fn => fn());
      }
      assert.equal(frames.size, 0);
    },
    scroll(top) {
      listeners.get('pane:wheel')();
      pane.scrollTop = top;
      listeners.get('pane:scroll')();
    },
    click() {
      doc.activeElement = jump;
      listeners.get('jump:click')();
    }
  };
}

test('short reply follows the batch; long reply reveals its beginning, not its last paragraph', () => {
  assert.equal(replyViewportTop({ top: 250, height: 100, viewportHeight: 300, bottom: 50 }), 50);
  assert.equal(replyViewportTop({ top: 250, height: 1000, viewportHeight: 300, bottom: 950 }), 242);
  const f = fixture();
  f.owner.send();
  f.owner.prepareAppend();
  const short = f.row(100);
  f.transcript.append(short);
  f.owner.reply(short);
  f.flush();
  assert.equal(f.pane.scrollTop, 0);
  f.owner.send();
  f.owner.prepareAppend();
  const long = f.row(1000);
  f.transcript.append(long);
  f.owner.reply(long);
  f.flush();
  assert.equal(f.pane.scrollTop, 292);
  assert.equal(f.jump.hidden, true);
  // Late cards before that answer preserve the readable beginning.
  f.owner.beforeChange();
  f.transcript.children[0].height += 150;
  f.owner.changed();
  f.flush();
  assert.equal(f.pane.scrollTop, 442);
  assert.equal(f.doc.activeElement, null);
});

test('intentional scroll-away preserves a row/offset and focus; only a reply becomes unread', () => {
  const f = fixture(Array(8).fill(100));
  f.owner.send();
  f.flush();
  assert.equal(f.pane.scrollTop, 500);
  f.scroll(25);
  const input = {};
  f.doc.activeElement = input;
  f.owner.beforeChange();
  const answer = f.row();
  f.transcript.append(answer);
  f.owner.reply(answer);
  f.flush();
  assert.equal(f.pane.scrollTop, 25);
  assert.equal(f.jump.hidden, false);
  assert.equal(f.announce.textContent, 'New reply available.');
  assert.equal(f.doc.activeElement, input);
  // Independent metadata/Today reflow has no new unread identity.
  f.prefix(150);
  f.owner.changed();
  f.flush();
  assert.equal(f.pane.scrollTop, 175);
  assert.equal(f.jump.hidden, false);
  f.click();
  assert.equal(f.jump.hidden, true);
  assert.equal(f.doc.activeElement, answer);
  assert.equal(f.pane.scrollTop, 750);
});

test('history trimming uses a surviving neighbour, not a removed anchor or a bottom snap', () => {
  const a = {},
    b = {};
  assert.equal(survivingAnchor([{ row: a }, { row: b }], row => row === b).row, b);
  const f = fixture(Array(8).fill(100));
  f.owner.send();
  f.flush();
  f.scroll(125);
  const neighbour = f.transcript.children[2];
  const offset = neighbour.getBoundingClientRect().top;
  f.owner.beforeChange();
  f.transcript.children[1].remove();
  f.owner.changed();
  f.flush();
  assert.equal(neighbour.getBoundingClientRect().top, offset);
  assert.equal(f.jump.hidden, true);
});

test('hydration reveals newest exchange once; close/reopen preserves intentional reading', () => {
  const f = fixture();
  f.owner.reset();
  f.owner.beginHydration();
  f.owner.prepareAppend();
  const answer = f.row(1000);
  f.transcript.append(answer);
  f.owner.reply(answer);
  f.owner.endHydration();
  f.flush();
  assert.equal(f.pane.scrollTop, 192);
  assert.equal(f.jump.hidden, true);
  f.scroll(40);
  f.open(false);
  f.owner.closed();
  f.flush();
  f.open(true);
  f.owner.opened();
  f.flush();
  assert.equal(f.pane.scrollTop, 40);
});

test('revealing an unread target never hides its focused jump control or steals focus', () => {
  const f = fixture(Array(8).fill(100));
  f.owner.send();
  f.flush();
  f.scroll(25);
  const answer = f.row();
  f.owner.prepareAppend();
  f.transcript.append(answer);
  f.owner.reply(answer);
  f.flush();
  f.doc.activeElement = f.jump;
  f.scroll(600);
  assert.equal(f.jump.hidden, false);
  assert.equal(f.doc.activeElement, f.jump);
  f.doc.activeElement = {};
  f.listeners.get('jump:blur')();
  assert.equal(f.jump.hidden, true);
  assert.equal(f.announce.textContent, '');
});

test('one pending row is retired before answer; reset/teardown cancel old callbacks', () => {
  const f = fixture();
  f.owner.pendingReply(true);
  f.owner.pendingReply(true);
  f.flush();
  assert.equal(f.transcript.children.filter(row => 'pendingReply' in row.dataset).length, 1);
  f.owner.prepareAppend();
  assert.equal(f.transcript.children.length, 2);
  const answer = f.row(1000);
  f.transcript.append(answer);
  f.owner.reply(answer);
  f.owner.reset();
  f.transcript.children = [];
  f.owner.reply(answer); // a disconnected callback from the previous thread
  f.flush();
  assert.equal(f.jump.hidden, true);
  assert.equal(f.pane.scrollTop, 0);
  f.owner.dispose();
  assert.equal(f.listeners.size, 0);
});
