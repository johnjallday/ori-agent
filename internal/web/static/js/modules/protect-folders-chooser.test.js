import test from 'node:test';
import assert from 'node:assert/strict';

import {
  chooserCards,
  closeChooser,
  confirmChoice,
  fetchProtectionOptions,
  openChooser,
  openChooserFromLocation,
  PRESELECT_KEY
} from './protect-folders-chooser.js';

class FakeNode {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.listeners = {};
    this.textContent = '';
    this.open = false;
  }
  appendChild(node) {
    this.children.push(node);
    node.parentNode = this;
    return node;
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
  }
  addEventListener(key, fn) {
    (this.listeners[key] ||= []).push(fn);
  }
  click() {
    for (const fn of this.listeners.click || []) fn({ preventDefault() {} });
  }
  remove() {
    if (this.parentNode)
      this.parentNode.children = this.parentNode.children.filter(c => c !== this);
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  focus() {
    this.doc.activeElement = this;
  }
  all() {
    return [this, ...this.children.flatMap(child => child.all())];
  }
  text() {
    return [this.textContent, ...this.children.map(child => child.text())].join(' ');
  }
  querySelector(selector) {
    return (
      this.all().find(node => selector === '[data-protect-choice]' && node.dataset.protectChoice) ||
      null
    );
  }
}

function fakeWindow({ options, href = 'http://localhost:8765/', modal = true } = {}) {
  const store = new Map();
  const doc = { activeElement: null };
  doc.createElement = tag => Object.assign(new FakeNode(tag), { doc });
  doc.body = doc.createElement('body');
  doc.getElementById = id => (id === 'addFolderModal' && modal ? {} : null);
  const opened = [];
  const calls = [];
  const win = {
    document: doc,
    location: { href },
    history: { replaceState: (_, __, url) => (win.location.href = 'http://localhost:8765' + url) },
    sessionStorage: {
      setItem: (k, v) => store.set(k, v),
      removeItem: k => store.delete(k),
      getItem: k => store.get(k) ?? null
    },
    sessionManager: { showAddWorkspaceModal: request => opened.push(request) },
    fetch: async (url, init) => {
      calls.push({ url, init });
      return { ok: true, json: async () => ({ protection_options: options }) };
    }
  };
  return { win, doc, store, opened, calls };
}

test('Time Machine leads only on a Mac that can read it; restic always lists its needs', () => {
  const mac = chooserCards({ time_machine: { platform_supported: true } });
  assert.deepEqual(
    mac.map(card => card.id),
    ['time_machine', 'restic']
  );
  assert.match(mac[0].does, /only after you approve one read/);
  assert.match(mac[0].does, /never changes Time Machine settings or starts a backup/);
  const other = chooserCards({ time_machine: { platform_supported: false } });
  assert.deepEqual(
    other.map(card => card.id),
    ['restic']
  );
  assert.deepEqual(other[0].needs, [
    'An external drive with free space',
    'restic 0.19.1, installed by you',
    'An encrypted repository you prepare on that drive',
    'A Vault secret that holds its password',
    'About 30 minutes'
  ]);
  for (const card of [...mac, ...other]) assert.doesNotMatch(card.does, /Ori backs up|verified/i);
  assert.deepEqual(
    chooserCards(null).map(card => card.id),
    ['restic'],
    'unknown options never claim Time Machine'
  );
});

test('the options read is one GET and fails to nothing', async () => {
  const { win, calls } = fakeWindow({ options: { time_machine: { platform_supported: true } } });
  assert.deepEqual(await fetchProtectionOptions(win.fetch), {
    time_machine: { platform_supported: true }
  });
  assert.equal(calls[0].url, '/api/personal-assistant/protection-options');
  assert.equal(calls[0].init.method, undefined);
  assert.equal(await fetchProtectionOptions(async () => ({ ok: false })), null);
  assert.equal(
    await fetchProtectionOptions(async () => {
      throw new Error('offline');
    }),
    null
  );
});

test('confirming hands a one-time preselect to the existing create flow', () => {
  const { win, store, opened } = fakeWindow();
  assert.equal(confirmChoice('time_machine', win), 'opened');
  assert.equal(store.get(PRESELECT_KEY), 'time_machine');
  assert.deepEqual(opened, [
    { entryPoint: 'backup_and_recovery_cta', blueprint: 'backup-and-recovery' }
  ]);
  assert.equal(confirmChoice('restic', win), 'opened');
  assert.equal(store.has(PRESELECT_KEY), false, 'restic enters the flow unchanged');

  const away = fakeWindow({ modal: false });
  assert.equal(confirmChoice('restic', away.win), 'navigated');
  assert.equal(away.win.location.href, '/?create=1&blueprint=backup-and-recovery');
});

test('the chooser renders both cards, and cancel creates nothing and posts nothing', async () => {
  const { win, doc, opened, calls } = fakeWindow({
    options: { time_machine: { platform_supported: true } }
  });
  assert.equal(await openChooser(win), true);
  const dialog = doc.body.children.find(
    node => node.attributes['aria-labelledby'] === 'protectFoldersTitle'
  );
  assert.ok(dialog?.open, 'chooser did not open');
  const text = dialog.text();
  assert.match(text, /Protect your folders/);
  assert.ok(
    text.indexOf('Time Machine (built into this Mac)') <
      text.indexOf('A restic repository on an external drive')
  );
  assert.match(text, /What you'll need/);
  assert.match(text, /never backs anything up itself/);
  assert.equal(doc.activeElement?.dataset.protectChoice, 'time_machine');
  dialog
    .all()
    .find(node => node.id === 'protectFoldersCancel')
    .click();
  assert.equal(doc.body.children.includes(dialog), false);
  assert.equal(opened.length, 0, 'cancel opened the create flow');
  assert.ok(calls.every(call => !call.init?.method || call.init.method === 'GET'));
  closeChooser(doc);
});

test('a protect=folders link opens the chooser once and is scrubbed', () => {
  const { win } = fakeWindow({
    href: 'http://localhost:8765/?protect=folders&panel=today',
    options: {}
  });
  assert.equal(openChooserFromLocation(win), true);
  assert.equal(win.location.href, 'http://localhost:8765/?panel=today');
  assert.equal(openChooserFromLocation(win), false, 'a refresh reopened the chooser');
  closeChooser(win.document);
});
