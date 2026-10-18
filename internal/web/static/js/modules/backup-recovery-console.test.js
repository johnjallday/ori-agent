import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./backup-recovery-console.js', import.meta.url), 'utf8');
class Element {
  constructor(tag, doc) {
    this.tagName = tag;
    this.doc = doc;
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this._text = '';
    this.open = false;
  }
  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(node => node.textContent).join(' ');
  }
  appendChild(node) {
    this.children.push(node);
    node.parentNode = this;
    return node;
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
    if (key === 'open') this.open = true;
  }
  getAttribute(key) {
    return this.attributes[key];
  }
  removeAttribute(key) {
    delete this.attributes[key];
    if (key === 'open') this.open = false;
  }
  addEventListener(key, fn) {
    (this.listeners[key] ||= []).push(fn);
  }
  fire(key, extra = {}) {
    return Promise.all(
      (this.listeners[key] || []).map(fn =>
        fn({ currentTarget: this, preventDefault() {}, ...extra })
      )
    );
  }
  all(predicate) {
    return [
      ...(predicate(this) ? [this] : []),
      ...this.children.flatMap(node => node.all(predicate))
    ];
  }
  querySelector(selector) {
    return (
      this.all(node =>
        selector === 'button'
          ? node.tagName === 'button'
          : node.getAttribute('data-br-tab') === selector.match(/data-br-tab="([^"]+)"/)?.[1]
      )[0] || null
    );
  }
  focus() {
    if (!this.disabled) this.doc.activeElement = this;
  }
  get isConnected() {
    let node = this;
    while (node) {
      if (node === this.doc.body) return true;
      node = node.parentNode;
    }
    return false;
  }
  showModal() {
    this.open = true;
  }
  close() {
    this.open = false;
  }
  setSelectionRange() {}
}
function fixture(overrides = {}) {
  const document = { listeners: {}, activeElement: null };
  document.createElement = tag => new Element(tag, document);
  document.body = document.createElement('body');
  document.body.dataset = { workspaceViewMode: 'details' };
  document.getElementById = id => document.body.all(node => node.id === id)[0] || null;
  document.addEventListener = (key, fn) => {
    document.listeners[key] = fn;
  };
  const mount = document.createElement('div');
  mount.id = 'backupRecoveryMount';
  document.body.appendChild(mount);
  const status = {
    generation: 'g1',
    revision: 1,
    sequence: 2,
    server_time: '2026-01-02T03:04:05Z',
    connection: 'configured',
    runtime_available: true,
    monitoring: 'manual_only',
    last_successful_read: '2026-01-02T03:00:00Z',
    snapshot_count: 0,
    evidence_current_generation: true,
    ...overrides
  };
  const settings = {
    configured: false,
    picker_available: true,
    source_paths: [],
    monitoring: {
      intent: 'manual_only',
      monitoring_revision: 0,
      consent_valid: false,
      policy: {
        cadence_seconds: 3600,
        expected_age_seconds: 172800,
        freshness_seconds: 10800,
        disconnected_grace_seconds: 86400,
        low_space_percent: 10
      }
    }
  };
  const calls = [];
  const context = {
    document,
    URL,
    AbortController,
    console,
    crypto: { randomUUID: () => 'invented-once-key' },
    currentWorkspaceId: 'owned-ws',
    location: { href: 'http://localhost:8765/workspaces/owned-ws' },
    history: {
      replaceState(_, __, value) {
        context.location.href = 'http://localhost:8765' + value;
      }
    },
    WorkspaceCapabilities: {
      isInstalled: () => true,
      load: async () => {},
      registerOpenHandler() {}
    },
    fetch: async (url, options) => {
      calls.push({ url, options });
      const payload = url.endsWith('/settings')
        ? {
            settings: {
              generation: status.generation,
              revision: status.revision,
              ...settings
            }
          }
        : url.endsWith('/guide')
          ? { guide: { prerequisites: [], steps: [], sources: [] } }
          : url.includes('/history')
            ? { history: { items: [], next_cursor: 0 } }
            : { status };
      return { ok: true, json: async () => ({ success: true, ...payload }) };
    }
  };
  context.window = context;
  vm.runInNewContext(source, context);
  return {
    context,
    document,
    mount,
    calls,
    status,
    settings,
    console: context.BackupRecoveryConsole
  };
}
const settle = () => new Promise(resolve => setImmediate(resolve));
function findButton(doc, label) {
  return doc.body.all(node => node.tagName === 'button' && node.textContent === label)[0];
}

test('opening and navigating loads stored facts only, with honest empty evidence', async () => {
  const f = fixture();
  f.console.open();
  await settle();
  assert.match(f.document.body.textContent, /None observed/);
  assert.match(f.document.body.textContent, /No completed backup observed/);
  f.console._test.selectTab('history');
  await settle();
  assert.ok(f.calls.every(call => call.options.method === 'GET'));
  assert.ok(f.calls.some(call => call.url.endsWith('/history')));
  assert.doesNotMatch(f.document.body.textContent, /backup succeeded|all files protected/i);
});
test('failed stored read preserves older success and marks it unavailable, never empty', async () => {
  const f = fixture();
  await f.console._test.loadStored();
  const old = f.console._test.state.status.last_successful_read;
  f.context.fetch = async () => {
    throw new Error('invented-private-path');
  };
  await f.console._test.loadStored();
  assert.equal(f.console._test.state.status.last_successful_read, old);
  assert.match(f.mount.textContent, /Stored evidence unavailable/);
  assert.doesNotMatch(f.mount.textContent, /invented-private-path/);
});
test('scope review is text, explicit read consent is unchecked, and cancel does not POST', async () => {
  const f = fixture();
  await f.console._test.loadStored();
  f.console._test.state.review = {
    generation: 'g1',
    revision: 1,
    review_token: 'review',
    source_paths: ['<img src=x onerror=evil()>'],
    volume_path: '/Volumes/Invented',
    repository_path: '/Volumes/Invented/Repository'
  };
  f.console.open({ tab: 'settings' });
  assert.match(f.document.body.textContent, /<img src=x onerror=evil\(\)>/);
  assert.equal(f.document.getElementById('brReadConsent').checked, undefined);
  assert.equal(findButton(f.document, 'Approve one metadata read').disabled, true);
  await findButton(f.document, 'Cancel review').fire('click');
  assert.equal(f.console._test.state.review, null);
  assert.ok(f.calls.every(call => call.options.method === 'GET'));
});
test('lost-response retry sends identical operation key, consent and payload', async () => {
  const f = fixture();
  await f.console._test.loadStored();
  const original = f.context.fetch;
  const writes = [];
  f.context.fetch = async (url, options) => {
    if (options.method !== 'GET') {
      writes.push(options.body);
      if (writes.length === 1) throw new Error('lost response');
      return {
        ok: true,
        json: async () => ({ success: true, operation: { state: 'complete', outcome: 'read_ok' } })
      };
    }
    return original(url, options);
  };
  const body = {
    generation: 'g1',
    revision: 1,
    sequence: 2,
    operation_id: 'one-key',
    allow_metadata_read: true
  };
  await f.console._test.mutate('/refresh', body);
  assert.ok(f.console._test.state.retry);
  await f.console._test.mutate('', null, 'POST', true);
  assert.equal(writes.length, 2);
  assert.equal(writes[0], writes[1]);
  assert.equal(f.console._test.state.retry, null);
});
test('late stored response cannot overwrite a newer generation', async () => {
  const f = fixture();
  let release;
  let first = true;
  const original = f.context.fetch;
  f.context.fetch = async (url, options) => {
    if (first) {
      first = false;
      await new Promise(resolve => {
        release = resolve;
      });
      return {
        ok: true,
        json: async () => ({ success: true, status: { ...f.status, generation: 'old' } })
      };
    }
    return original(url, options);
  };
  const old = f.console._test.loadStored();
  await settle();
  f.status.generation = 'new';
  await f.console._test.loadStored();
  release();
  await old;
  assert.equal(f.console._test.state.status.generation, 'new');
});
test('tabs are bounded, keyboard-addressable, and close restores the trigger', async () => {
  const f = fixture();
  const trigger = f.document.createElement('button');
  f.document.body.appendChild(trigger);
  f.console.open({ tab: '../other', trigger });
  assert.equal(f.console.activeTab(), 'overview');
  await f.document.getElementById('brTab-overview').fire('keydown', { key: 'ArrowRight' });
  assert.equal(f.console.activeTab(), 'history');
  f.console.close();
  assert.equal(f.document.activeElement, trigger);
  assert.equal(f.console.isOpen(), false);
});
test('known zero capacity differs from unavailable; hidden characters are visible', () => {
  const f = fixture();
  assert.equal(f.console._test.bytes(0), '0 bytes');
  assert.equal(f.console._test.bytes(undefined), 'Unavailable');
  assert.equal(f.console._test.text('x\u202ey'), 'x[U+202E]y');
});
test('periodic review is not consent and enabling requires a separate unchecked choice', async () => {
  const f = fixture();
  f.settings.configured = true;
  f.settings.source_paths = ['/Invented saved project'];
  f.settings.repository_path = '/Invented saved repository';
  const original = f.context.fetch;
  const writes = [];
  f.context.fetch = async (url, options) => {
    if (options.method !== 'GET') {
      const body = JSON.parse(options.body);
      writes.push({ url, body });
      if (url.endsWith('/monitoring/review'))
        return {
          ok: true,
          json: async () => ({
            success: true,
            review: { ...body, review_token: 'periodic-review', expires_at: '2026-01-02T03:09:05Z' }
          })
        };
      f.status.monitoring = 'active';
      f.status.revision++;
      f.settings.monitoring.intent = 'active';
      f.settings.monitoring.monitoring_revision++;
      return {
        ok: true,
        json: async () => ({ success: true, operation: { state: 'complete', outcome: 'applied' } })
      };
    }
    return original(url, options);
  };
  f.console.open({ tab: 'settings' });
  await settle();
  assert.ok(f.calls.every(call => call.options.method === 'GET'));
  await findButton(f.document, 'Review periodic metadata policy').fire('click');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].body.allow_periodic_metadata, undefined);
  assert.match(f.document.body.textContent, /Saved source · \/Invented saved project/);
  let consent = f.document.getElementById('brPeriodicConsent');
  assert.ok(!consent.checked);
  assert.equal(findButton(f.document, 'Enable periodic metadata checks').disabled, true);
  await findButton(f.document, 'Enable periodic metadata checks').fire('click');
  assert.equal(writes.length, 1);
  await findButton(f.document, 'Cancel periodic review').fire('click');
  assert.equal(writes.length, 1);
  await findButton(f.document, 'Review periodic metadata policy').fire('click');
  consent = f.document.getElementById('brPeriodicConsent');
  consent.checked = true;
  await consent.fire('change');
  await findButton(f.document, 'Enable periodic metadata checks').fire('click');
  await settle();
  assert.equal(writes.length, 3);
  assert.equal(writes[2].body.allow_periodic_metadata, true);
  assert.equal(writes[2].body.review_token, 'periodic-review');
  assert.equal(writes[2].body.monitoring_revision, 0);
  assert.match(f.document.body.textContent, /Periodic checks active/);
  assert.equal(f.console._test.state.status.last_successful_read, '2026-01-02T03:00:00Z');
});
test('late periodic review is discarded on close and unsupported policy stays inert', async () => {
  const f = fixture();
  f.console.open({ tab: 'settings' });
  await settle();
  const valid = f.console._test.validPolicy;
  const policy = f.console._test.state.monitorDraft;
  assert.equal(valid({ ...policy, freshness_seconds: 900 }), false);
  assert.equal(valid({ ...policy, low_space_percent: null }), false);
  assert.equal(valid({ ...policy, cadence_seconds: 60 }), false);
  let release;
  f.context.fetch = async () => {
    await new Promise(resolve => {
      release = resolve;
    });
    return {
      ok: true,
      json: async () => ({ success: true, review: { review_token: 'obsolete' } })
    };
  };
  const pending = f.console._test.reviewMonitoring();
  await settle();
  f.console.close();
  release();
  await pending;
  assert.equal(f.console._test.state.monitorReview, null);
});
test('future completion is quarantined and freshness is not rendered as backup success', async () => {
  const f = fixture({
    snapshot_count: 1,
    last_completed_at: '2099-01-01T00:00:00Z',
    health: { observation: 'stale', recency: 'clock_suspect', capacity: 'low' }
  });
  f.console.open();
  await settle();
  assert.match(f.document.body.textContent, /future timestamp quarantined/);
  assert.match(f.document.body.textContent, /Stored evidence stale/);
  assert.doesNotMatch(f.document.body.textContent, /2099|backup succeeded|all files protected/);
});

test('partial coverage and capacity remain point-in-time evidence, not protection', async () => {
  const f = fixture({
    snapshot_count: 1,
    coverage: { declared_path_count: 1, exclusion_count: 2 },
    capacity: { available_bytes: 0, total_bytes: 1024, observed_at: '2026-01-02T03:00:00Z' }
  });
  f.console.open();
  await settle();
  assert.match(f.document.body.textContent, /Historical declaration, not exhaustive coverage/);
  assert.match(f.document.body.textContent, /0 bytes available/);
  assert.match(f.document.body.textContent, /does not predict backup fit/);
});
