import { test } from 'node:test';
import assert from 'node:assert/strict';

const originalWindow = globalThis.window;
const originalDocument = globalThis.document;
const originalFetch = globalThis.fetch;
const originalSetInterval = globalThis.setInterval;
const originalClearInterval = globalThis.clearInterval;
const originalCustomEvent = globalThis.CustomEvent;

const listeners = new Map();
const timers = [];
globalThis.document = {
  visibilityState: 'visible',
  readyState: 'complete',
  addEventListener(type, listener) {
    listeners.set(type, listener);
  },
  dispatchEvent() {}
};
globalThis.window = {
  location: { pathname: '/workspaces/ws-reaper' },
  currentWorkspaceId: 'ws-reaper'
};
globalThis.fetch = async () => ({
  ok: true,
  json: async () => ({ applies: true, connected: false, reason: 'reaper_unreachable' })
});
globalThis.setInterval = (callback, delay) => {
  const timer = { callback, delay, cleared: false };
  timers.push(timer);
  return timer;
};
globalThis.clearInterval = timer => {
  if (timer) timer.cleared = true;
};
globalThis.CustomEvent = class {
  constructor(type, options) {
    this.type = type;
    this.detail = options && options.detail;
  }
};

await import('./reaper-console.js');
const consolePanel = globalThis.window.ReaperConsole;

test('station state reports connected and honest offline states', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  assert.equal(consolePanel.stationState().value, 'Checking…');
  consolePanel._setState({ applies: true, connected: true });
  assert.equal(consolePanel.stationState().value, 'Connected');
  consolePanel._setState({ applies: true, connected: false, reason: 'web_remote_off' });
  assert.equal(consolePanel.stationState().value, 'Web Remote off');
  assert.equal(consolePanel.stationState().tone, 'degraded');
});

test('polling runs no faster than five seconds and pauses off-map or in a hidden tab', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  globalThis.document.visibilityState = 'visible';
  consolePanel.setMapVisible(true);
  const timer = timers.at(-1);
  assert.ok(timer);
  assert.equal(timer.delay, 5000);
  consolePanel.setMapVisible(false);
  assert.equal(timer.cleared, true);
  assert.equal(consolePanel._polling(), false);
});

test.after(() => {
  consolePanel._resetForTest();
  globalThis.window = originalWindow;
  globalThis.document = originalDocument;
  globalThis.fetch = originalFetch;
  globalThis.setInterval = originalSetInterval;
  globalThis.clearInterval = originalClearInterval;
  globalThis.CustomEvent = originalCustomEvent;
});
