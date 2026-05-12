// Tests for note-presence.js — cross-tab "is this note open elsewhere?"
// coordination. We fake BroadcastChannel with an in-process bus so two
// "tabs" can talk to each other inside one Node process.

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeBus {
  constructor() { this.channels = new Map(); }
  register(name, ch) {
    if (!this.channels.has(name)) this.channels.set(name, new Set());
    this.channels.get(name).add(ch);
  }
  post(name, sender, message) {
    const peers = this.channels.get(name);
    if (!peers) return;
    for (const ch of peers) {
      if (ch === sender) continue;
      // structuredClone-ish copy via JSON to mirror the real BroadcastChannel
      const cloned = JSON.parse(JSON.stringify(message));
      // Deliver async so it mirrors real BroadcastChannel ordering.
      setImmediate(() => {
        ch.listeners.forEach((fn) => fn({ data: cloned }));
      });
    }
  }
}

class FakeChannel {
  constructor(name, bus) {
    this.name = name;
    this.bus = bus;
    this.listeners = new Set();
    bus.register(name, this);
  }
  addEventListener(type, fn) { if (type === 'message') this.listeners.add(fn); }
  postMessage(msg) { this.bus.post(this.name, this, msg); }
}

async function setupTab(bus) {
  // Each "tab" is its own fresh module instance.
  globalThis.BroadcastChannel = function (name) { return new FakeChannel(name, bus); };
  globalThis.window = globalThis;
  // Bust the module cache by appending a unique query (ESM doesn't allow this
  // directly — instead, we re-import via a dynamic import + tiny eval-style
  // trick: use a unique data URL re-export).
  const mod = await import(`./note-presence.js?tab=${Math.random()}`);
  return mod;
}

test('isOpenElsewhere resolves false when no other tab holds the note', async () => {
  const bus = new FakeBus();
  const tabA = await setupTab(bus);
  tabA._resetForTesting();
  const result = await tabA.isOpenElsewhere('note-1', 80);
  assert.equal(result.open, false);
});

test('isOpenElsewhere finds a holder in another tab', async () => {
  const bus = new FakeBus();
  const tabA = await setupTab(bus);
  const tabB = await setupTab(bus);
  tabA._resetForTesting();
  tabB._resetForTesting();

  tabB.claimOpenNote('note-shared', 'page');
  const result = await tabA.isOpenElsewhere('note-shared', 200);
  assert.equal(result.open, true);
  assert.equal(result.surface, 'page');
});

test('releaseOpenNote stops the tab from replying', async () => {
  const bus = new FakeBus();
  const tabA = await setupTab(bus);
  const tabB = await setupTab(bus);
  tabA._resetForTesting();
  tabB._resetForTesting();

  tabB.claimOpenNote('note-x', 'page');
  tabB.releaseOpenNote('note-x');
  const result = await tabA.isOpenElsewhere('note-x', 100);
  assert.equal(result.open, false);
});

test('claimOpenNote is idempotent', async () => {
  const bus = new FakeBus();
  const tabA = await setupTab(bus);
  const tabB = await setupTab(bus);
  tabA._resetForTesting();
  tabB._resetForTesting();

  tabB.claimOpenNote('note-y', 'page');
  tabB.claimOpenNote('note-y', 'page'); // second call is a no-op
  const result = await tabA.isOpenElsewhere('note-y', 100);
  assert.equal(result.open, true);
});

test('isOpenElsewhere resolves false when BroadcastChannel is unsupported', async () => {
  // Strip BroadcastChannel before importing.
  delete globalThis.BroadcastChannel;
  globalThis.window = globalThis;
  const mod = await import(`./note-presence.js?tab=nochannel-${Math.random()}`);
  mod._resetForTesting();
  const result = await mod.isOpenElsewhere('note-z', 50);
  assert.equal(result.open, false);
});
