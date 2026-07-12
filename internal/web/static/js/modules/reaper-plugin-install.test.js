// Tests for reaper-plugin-install.js — the shared inline install flow used by
// both the create card and the workspace-detail repair card. Inline DOM stub.
//
// Run with: node --test internal/web/static/js/modules/reaper-plugin-install.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this._text = '';
    this.className = '';
    this.style = {};
    this.disabled = false;
    this.type = '';
    this.placeholder = '';
    this.value = '';
    this.children = [];
    this._listeners = {};
    this._attrs = {};
  }
  get textContent() {
    if (this.children.length) return this._text + this.children.map(c => c.textContent).join('');
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v === '') this.children = [];
  }
  appendChild(el) {
    this.children.push(el);
    return el;
  }
  addEventListener(ev, fn) {
    (this._listeners[ev] ||= []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  setAttribute(k, v) {
    this._attrs[k] = v;
  }
  focus() {}
  querySelectorAll(sel) {
    const want = String(sel).toUpperCase();
    const out = [];
    const walk = el =>
      el.children.forEach(c => {
        if (c.tagName === want) out.push(c);
        walk(c);
      });
    walk(this);
    return out;
  }
}

class FakeDocument {
  createElement(tag) {
    return new FakeElement(tag);
  }
}

globalThis.document = new FakeDocument();
globalThis.window = globalThis;
await import('./reaper-plugin-install.js');
const flush = async () => {
  for (let i = 0; i < 8; i++) await new Promise(r => setTimeout(r, 0));
};
const buttons = host => host.querySelectorAll('button').map(b => b.textContent);

test('marketplace path: trust preview then install + enable calls onComplete', async () => {
  const host = new FakeElement('div');
  const calls = [];
  globalThis.fetch = async (url, opts) => {
    const method = (opts && opts.method) || 'GET';
    calls.push(method + ' ' + url);
    if (url === '/api/plugins/marketplaces') {
      return {
        ok: true,
        json: async () => ({ marketplaces: [{ name: 'mp', plugins: [{ name: 'reaper-plugin' }] }] })
      };
    }
    if (url === '/api/plugins/marketplaces/install') {
      const body = JSON.parse(opts.body);
      return body.confirm
        ? { ok: true, json: async () => ({ installed: true }) }
        : {
            ok: true,
            json: async () => ({
              trust: { Name: 'reaper-plugin', Skills: ['reaper-session-setup'] }
            })
          };
    }
    if (url === '/api/plugins/reaper-plugin/enable')
      return { ok: true, json: async () => ({ enabled: true }) };
    throw new Error('unexpected ' + url);
  };

  let done = false;
  window.ReaperPluginInstall.begin({ host, onComplete: () => (done = true) });
  await flush();
  assert.match(host.textContent, /Skills: reaper-session-setup/);
  const confirm = host.querySelectorAll('button').find(b => b.textContent === 'Install & enable');
  assert.ok(confirm, 'expected trust confirm');
  confirm.click();
  await flush();
  assert.ok(done, 'onComplete should fire after install + enable');
  assert.ok(calls.includes('POST /api/plugins/reaper-plugin/enable'), 'must enable after install');
});

test('declared source: installs straight from source, no marketplace call', async () => {
  const host = new FakeElement('div');
  const SRC = 'https://github.com/johnjallday/reaper-plugin.git';
  const calls = [];
  globalThis.fetch = async (url, opts) => {
    calls.push(url);
    if (url === '/api/plugins/install') {
      const body = JSON.parse(opts.body);
      assert.equal(body.source, SRC);
      return body.confirm
        ? { ok: true, json: async () => ({ installed: true }) }
        : { ok: true, json: async () => ({ trust: { Name: 'reaper-plugin', Skills: [] } }) };
    }
    if (url === '/api/plugins/reaper-plugin/enable') return { ok: true, json: async () => ({}) };
    throw new Error('unexpected ' + url);
  };
  let done = false;
  window.ReaperPluginInstall.begin({ host, declaredSource: SRC, onComplete: () => (done = true) });
  await flush();
  host
    .querySelectorAll('button')
    .find(b => b.textContent === 'Install & enable')
    .click();
  await flush();
  assert.ok(done);
  assert.ok(
    !calls.includes('/api/plugins/marketplaces'),
    'declared source must skip the marketplace'
  );
});

test('no marketplace: falls back to a source input', async () => {
  const host = new FakeElement('div');
  globalThis.fetch = async url => {
    if (url === '/api/plugins/marketplaces')
      return { ok: true, json: async () => ({ marketplaces: [] }) };
    throw new Error('unexpected ' + url);
  };
  window.ReaperPluginInstall.begin({ host, onComplete: () => {} });
  await flush();
  assert.ok(host.querySelectorAll('input').length > 0, 'expected a source input');
  assert.ok(buttons(host).includes('Preview install'));
});
