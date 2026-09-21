import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.className = '';
    this.children = [];
    this._text = '';
    this._listeners = {};
    this.disabled = false;
    this.checked = false;
    this.files = [];
    this.value = '';
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent || '').join('');
  }
  set textContent(value) {
    this._text = String(value || '');
    this.children = [];
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  append(...children) {
    this.children.push(...children);
  }
  setAttribute(name, value) {
    this[name] = value;
  }
  addEventListener(name, callback) {
    (this._listeners[name] = this._listeners[name] || []).push(callback);
  }
  fire(name) {
    return Promise.all((this._listeners[name] || []).map(callback => callback()));
  }
  all() {
    return this.children.flatMap(child => [child, ...(child.all ? child.all() : [])]);
  }
}

function response(payload, ok = true) {
  return { ok, json: async () => payload };
}

function setup() {
  globalThis.document = {
    createElement: tag => new FakeElement(tag),
    createTextNode: text => {
      const node = new FakeElement('#text');
      node._text = String(text);
      return node;
    },
    addEventListener() {}
  };
  globalThis.window = globalThis;
  globalThis.window.SetupWizard = {
    registered: {},
    registerStepRenderer(kind, renderer) {
      this.registered[kind] = renderer;
    }
  };
}

function context(workspaceId, overrides = {}) {
  return {
    workspaceId,
    step: {
      id: 'materials',
      kind: 'intake',
      status: 'active',
      intake_key: 'course-materials',
      intake_files: true,
      intake_accepted_extensions: ['.pdf', '.txt'],
      ...overrides.step
    },
    setBusy() {},
    setError: overrides.setError || (() => {}),
    announce() {},
    confirm: overrides.confirm || (async () => {})
  };
}

const tick = () => new Promise(resolve => setImmediate(resolve));

setup();
await import('./blueprint-intake-step.js');
const renderer = window.SetupWizard.registered.intake;

test('registers the intake renderer', () => {
  assert.ok(renderer);
  assert.equal(renderer.primaryLabel(), 'Continue');
});

test('renders file statuses and the host consent statement as text', async () => {
  const hostile = '<img src=x onerror=alert(1)> sent to OpenAI';
  globalThis.fetch = async () =>
    response({
      files: [
        { name: 'syllabus.pdf', status: 'parsed' },
        { name: 'malware.exe', status: 'skipped', message: 'Unsupported type.' }
      ],
      consent: { statement: hostile, provider: 'openai', accepted: false }
    });
  const host = new FakeElement('div');
  const ctx = context('workspace-render');
  renderer.render(host, ctx);
  await tick();
  await tick();

  assert.match(host.textContent, /syllabus\.pdfParsed/);
  assert.match(host.textContent, /malware\.exeSkippedUnsupported type/);
  assert.match(host.textContent, /<img src=x onerror=alert\(1\)> sent to OpenAI/);
  assert.equal('innerHTML' in host, false);
  const picker = host.all().find(node => node.type === 'file');
  assert.equal(picker.accept, '.pdf,.txt');
  assert.equal(renderer.disablePrimary(ctx), true, 'consent is required before Continue');
});

test('accepts consent before enabling Continue and then confirms through the wizard', async () => {
  let calls = 0;
  globalThis.fetch = async (_url, options = {}) => {
    calls += 1;
    if (options.method === 'POST') {
      return response({
        consent: {
          statement: 'Ori sends text to OpenAI. Nothing is created until review.',
          provider: 'openai',
          accepted: true,
          accepted_by: 'local-user'
        }
      });
    }
    return response({
      files: [{ name: 'notes.txt', status: 'parsed' }],
      consent: { statement: 'Ori sends text to OpenAI.', provider: 'openai', accepted: false }
    });
  };
  let confirmed = false;
  const ctx = context('workspace-consent', { confirm: async () => (confirmed = true) });
  const host = new FakeElement('div');
  renderer.render(host, ctx);
  await tick();
  await tick();

  const checkbox = host.all().find(node => node.type === 'checkbox');
  checkbox.checked = true;
  await checkbox.fire('change');
  await tick();
  assert.equal(calls, 2);
  assert.equal(renderer.disablePrimary(ctx), false);
  await renderer.onPrimary(ctx);
  assert.equal(confirmed, true);
});
