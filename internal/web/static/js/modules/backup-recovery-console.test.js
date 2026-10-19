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
    for (const child of this.children) child.parentNode = null;
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
  contains(node) {
    return this.all(candidate => candidate === node).length > 0;
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
function fixture(overrides = {}, initialHref = 'http://localhost:8765/workspaces/owned-ws') {
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
    location: { href: initialHref },
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
test('console captures its deep link before the workspace router normalizes the URL', async () => {
  const f = fixture(
    {},
    'http://localhost:8765/workspaces/owned-ws?panel=backup-recovery&tab=recovery'
  );
  f.context.WorkspaceCapabilities.load = async () => {
    f.context.location.href = 'http://localhost:8765/workspaces/owned-ws';
  };
  await f.console.init();
  assert.equal(f.console.isOpen(), true);
  assert.equal(f.console.activeTab(), 'recovery');
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
  f.console.close();
  await f.console.init();
  assert.equal(f.console.isOpen(), false, 'boot intent was replayed after closing');
});

test('Escape closes the coordinated dialog and restores the live opener without a check', async () => {
  const f = fixture();
  f.context.workspaceOverlayCoordinator = {
    open() {},
    close() {},
    activeModal: () => ({ id: 'backup-recovery-console' })
  };
  await f.console.refresh();
  const original = findButton(f.document, 'Open backup evidence');
  f.console.open({ trigger: original });
  await settle();
  const dialog = f.document.getElementById('backupRecoveryConsole');
  await dialog.fire('keydown', { key: 'Escape' });
  assert.equal(f.console.isOpen(), false);
  assert.equal(f.document.activeElement, findButton(f.document, 'Open backup evidence'));
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

const settle = () => new Promise(resolve => setImmediate(resolve));
function findButton(doc, label) {
  return doc.body.all(node => node.tagName === 'button' && node.textContent === label)[0];
}

test('stored-read busy state settles without claiming a provider check', async () => {
  const f = fixture();
  f.console.open();
  await settle();
  const dialog = f.document.getElementById('backupRecoveryConsole');
  let release;
  const pending = new Promise(resolve => {
    release = resolve;
  });
  const fetch = f.context.fetch;
  f.context.fetch = async (url, options) => {
    await pending;
    return fetch(url, options);
  };
  const read = f.console.refresh();
  assert.equal(dialog.getAttribute('aria-busy'), 'true');
  release();
  await read;
  assert.equal(dialog.getAttribute('aria-busy'), 'false');
  assert.ok(f.calls.every(call => call.options.method === 'GET'));
});

test('stored rerender retains modal focus when an unkeyed control is replaced', async () => {
  const f = fixture();
  f.settings.configured = true;
  f.console.open({ tab: 'settings' });
  await settle();
  const control = findButton(f.document, 'Revoke metadata access');
  control.focus();
  const calls = f.calls.length;
  f.console._test.render();
  assert.equal(control.isConnected, false);
  assert.equal(f.document.activeElement, f.document.getElementById('brTab-settings'));
  assert.equal(f.calls.length, calls, 'focus restoration started IO');
});

test('changing saved selection retains the live checkbox focus without granting access', async () => {
  const f = fixture();
  f.settings.configured = true;
  f.console.open({ tab: 'settings' });
  await settle();
  const saved = f.document.getElementById('brUseSavedSelection');
  assert.ok(saved);
  saved.focus();
  saved.checked = false;
  const calls = f.calls.length;
  await saved.fire('change');
  assert.equal(saved.isConnected, false);
  assert.equal(f.document.activeElement, f.document.getElementById('brUseSavedSelection'));
  assert.equal(f.calls.length, calls, 'selection-only change started IO');
});

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

test('recovery renders only labelled stored references and vetted guide links, with no action', async () => {
  const f = fixture();
  const original = f.context.fetch;
  f.context.fetch = async (url, options) => {
    if (url.endsWith('/recovery')) {
      f.calls.push({ url, options });
      return {
        ok: true,
        json: async () => ({
          success: true,
          recovery: {
            observation: 'stale',
            source_paths: ['<untrusted intended path>'],
            snapshots: [
              {
                id: 'a'.repeat(64),
                evidence: 'historical',
                declared_path_count: 1,
                exclusion_count: 2
              }
            ],
            guide: {
              boundary: 'External execution only.',
              steps: [
                { title: 'Restore outside Ori', instructions: ['Choose a separate empty target.'] }
              ],
              sources: [
                { label: 'Untrusted link', url: 'javascript:evil()' },
                {
                  label: 'Pinned recovery',
                  url: 'https://github.com/restic/restic/blob/v0.19.1/doc/050_restore.rst'
                }
              ]
            }
          }
        })
      };
    }
    return original(url, options);
  };
  f.console.open({ tab: 'recovery' });
  await settle();
  assert.match(f.document.body.textContent, /Historical configuration/);
  assert.match(f.document.body.textContent, /<untrusted intended path>/);
  assert.match(f.document.body.textContent, /separate empty target/);
  assert.doesNotMatch(f.document.body.textContent, /Untrusted link/);
  assert.ok(f.calls.every(call => call.options.method === 'GET'));
  assert.equal(findButton(f.document, 'Restore'), undefined);
});

test('a dated user report requires an unchecked assertion and preserves earlier edit versions', async () => {
  const f = fixture();
  const original = f.context.fetch;
  const writes = [];
  let operation = 0;
  f.context.crypto.randomUUID = () => 'invented-report-' + ++operation;
  const view = {
    generation: 'g1',
    revision: 1,
    server_time: '2026-01-02T03:04:05Z',
    can_report: true,
    source_paths: [],
    snapshots: [],
    reports: [],
    note_choices: [
      { code: '', label: 'No additional note' },
      { code: 'sample_compared', label: 'Compared outside Ori' },
      { code: 'sample_mismatch', label: 'Sample did not match' }
    ],
    guide: { steps: [], sources: [] }
  };
  f.context.fetch = async (url, options) => {
    if (url.endsWith('/recovery')) {
      f.calls.push({ url, options });
      return { ok: true, json: async () => ({ success: true, recovery: view }) };
    }
    if (url.endsWith('/recovery-report')) {
      const body = JSON.parse(options.body);
      writes.push(body);
      if (body.edit_id) view.reports.find(report => report.id === body.edit_id).superseded = true;
      view.reports.unshift({
        id: body.operation_id,
        generation: body.generation,
        tested_at: body.tested_at,
        reported_at: view.server_time,
        result: body.result,
        note_code: body.note_code,
        supersedes: body.edit_id
      });
      view.revision++;
      f.status.revision++;
      return {
        ok: true,
        json: async () => ({ success: true, operation: { state: 'complete', outcome: 'applied' } })
      };
    }
    return original(url, options);
  };
  f.console.open({ tab: 'recovery' });
  await settle();
  assert.match(f.document.body.textContent, /Not tested — no current user-reported result/);
  const field = async (id, value, event = 'change') => {
    const element = f.document.getElementById(id);
    element.value = value;
    await element.fire(event);
  };
  await field('brReportDate', '2026-01-01', 'input');
  await field('brReportResult', 'user_reported_passed');
  await field('brReportNote', 'sample_compared');
  assert.ok(!f.document.getElementById('brReportConsent').checked);
  assert.equal(findButton(f.document, 'Record user-reported result').disabled, true);
  let consent = f.document.getElementById('brReportConsent');
  consent.checked = true;
  await consent.fire('change');
  await findButton(f.document, 'Record user-reported result').fire('click');
  await settle();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].tested_at, '2026-01-01T00:00:00Z');
  assert.equal(writes[0].note_code, 'sample_compared');
  assert.match(f.document.body.textContent, /User reports passed/);
  await findButton(f.document, 'Edit report dated 2026-01-01').fire('click');
  assert.ok(!f.document.getElementById('brReportConsent').checked);
  await findButton(f.document, 'Cancel report edit').fire('click');
  assert.equal(writes.length, 1);
  await findButton(f.document, 'Edit report dated 2026-01-01').fire('click');
  await field('brReportResult', 'user_reported_failed');
  await field('brReportNote', 'sample_mismatch');
  consent = f.document.getElementById('brReportConsent');
  consent.checked = true;
  await consent.fire('change');
  await findButton(f.document, 'Save edited user report').fire('click');
  await settle();
  assert.equal(writes[1].edit_id, writes[0].operation_id);
  assert.match(f.document.body.textContent, /User reports failed/);
  assert.match(f.document.body.textContent, /earlier version/);
  assert.ok(writes.every(body => !('verified_by_ori' in body) && !('note' in body)));
});
test('lost recovery-report response retries exactly and invalid note/date choices stay disabled', async () => {
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
        json: async () => ({ success: true, operation: { state: 'complete', outcome: 'applied' } })
      };
    }
    return original(url, options);
  };
  const body = {
    generation: 'g1',
    revision: 1,
    operation_id: 'one-report-key',
    result: 'user_reported_failed',
    tested_at: '2026-01-01T00:00:00Z',
    note_code: 'restore_failed'
  };
  await f.console._test.mutate('/recovery-report', body);
  await f.console._test.mutate('', null, 'POST', true);
  assert.equal(writes[0], writes[1]);
  const valid = f.console._test.validReportDraft;
  const view = { can_report: true, note_choices: [{ code: 'sample_compared' }] };
  assert.equal(
    valid(
      { testedDate: '2026-02-30', result: 'user_reported_passed', noteCode: 'sample_compared' },
      view
    ),
    false
  );
  assert.equal(
    valid({ testedDate: '2026-01-01', result: 'ori_verified', noteCode: 'sample_compared' }, view),
    false
  );
  assert.equal(
    valid(
      { testedDate: '2026-01-01', result: 'user_reported_failed', noteCode: 'password=invented' },
      view
    ),
    false
  );
});

const bothProviders = [
  { id: 'time_machine', label: 'Time Machine', available: true },
  { id: 'restic', label: 'restic', available: true }
];

test('without a choice an unsaved console keeps restic; Time Machine is listed first', async () => {
  const f = fixture({ connection: 'not_configured' });
  f.settings.providers = bothProviders;
  await f.console._test.loadStored();
  f.console.open({ tab: 'settings' });
  await settle();
  assert.equal(f.console._test.state.provider, 'restic');
  assert.ok(f.document.getElementById('brVaultID'));
  const radios = f.document.body.all(node => node.getAttribute('role') === 'radio');
  assert.deepEqual(
    radios.map(node => node.id),
    ['brProvider-time_machine', 'brProvider-restic']
  );
});

test('a Time Machine preselect hides every restic credential control', async () => {
  const f = fixture(
    { connection: 'not_configured' },
    'http://localhost:8765/workspaces/owned-ws?panel=backup-recovery&tab=settings&provider=time_machine'
  );
  f.settings.providers = bothProviders;
  await f.console._test.loadStored();
  f.console.open({ tab: 'settings' });
  await settle();
  assert.equal(f.console._test.state.provider, 'time_machine');
  assert.ok(findButton(f.document, 'Review Time Machine check'));
  assert.ok(findButton(f.document, 'Choose a folder to check'));
  for (const id of ['brVaultID', 'brRecordID']) assert.equal(f.document.getElementById(id), null);
  assert.equal(findButton(f.document, 'Choose external volume root'), undefined);
  assert.doesNotMatch(f.document.body.textContent, /Describe selected reference/);

  await findButton(f.document, 'restic').fire('click');
  assert.equal(f.console._test.state.provider, 'restic');
  assert.ok(f.document.getElementById('brVaultID'), 'restic keeps its Vault reference');
  assert.match(f.document.body.textContent, /restic 0\.19\.1, installed by you/);
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

test('an unavailable provider says why and a chooser preselect cannot pick it', async () => {
  const f = fixture({ connection: 'not_configured' });
  f.settings.providers = [
    { id: 'time_machine', label: 'Time Machine', available: false, reason: 'platform_unsupported' },
    { id: 'restic', label: 'restic', available: true }
  ];
  assert.equal(f.console._test.defaultProvider(f.settings, 'time_machine'), 'restic');
  assert.equal(
    f.console._test.defaultProvider({ ...f.settings, providers: bothProviders }, 'time_machine'),
    'time_machine'
  );
  assert.equal(
    f.console._test.defaultProvider(
      { configured: true, provider: 'restic', providers: bothProviders },
      'time_machine'
    ),
    'restic',
    'a saved provider wins over a preselect'
  );
  await f.console._test.loadStored();
  f.console.open({ tab: 'settings' });
  await settle();
  assert.match(f.document.body.textContent, /Time Machine is unavailable: it is only on macOS\./);
  assert.equal(findButton(f.document, 'Time Machine').disabled, true);
});

test('a provider whose reader is missing stays choosable, so its setup path is visible', async () => {
  const f = fixture(
    { connection: 'not_configured' },
    'http://localhost:8765/workspaces/owned-ws?panel=backup-recovery&tab=settings&provider=time_machine'
  );
  f.settings.providers = [
    { id: 'time_machine', label: 'Time Machine', available: true },
    { id: 'restic', label: 'restic', available: false, reason: 'reader_unavailable' }
  ];
  await f.console._test.loadStored();
  f.console.open({ tab: 'settings' });
  await settle();
  assert.equal(f.console._test.state.provider, 'time_machine');
  const restic = findButton(f.document, 'restic');
  assert.equal(restic.disabled, false);
  assert.match(
    f.document.body.textContent,
    /restic's metadata reader is not available on this server yet/
  );
  await restic.fire('click');
  assert.equal(f.console._test.state.provider, 'restic');
  assert.match(f.document.body.textContent, /restic 0\.19\.1, installed by you/);
  assert.ok(f.document.getElementById('brVaultID'));
});

test('Time Machine recovery links only Apple’s restore guide and runs nothing', async () => {
  const f = fixture({ provider: 'time_machine' });
  f.settings.configured = true;
  f.settings.provider = 'time_machine';
  f.settings.providers = [{ id: 'time_machine', label: 'Time Machine', available: true }];
  f.console.open({ tab: 'recovery' });
  await settle();
  const link = f.document.body.all(
    node =>
      node.tagName === 'a' &&
      node.textContent.startsWith('Restore items backed up with Time Machine')
  )[0];
  assert.equal(
    link?.href,
    'https://support.apple.com/guide/mac-help/restore-items-backed-up-with-time-machine-mh11422/mac'
  );
  assert.match(f.document.body.textContent, /Ori opens nothing and restores nothing/);
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

test('the chooser preselect is used once, before anything is saved', async () => {
  const f = fixture({ connection: 'not_configured' });
  const store = new Map([['ori.backupRecovery.preselect', 'time_machine']]);
  f.context.sessionStorage = {
    getItem: key => store.get(key) ?? null,
    removeItem: key => store.delete(key)
  };
  f.settings.providers = [
    { id: 'restic', label: 'restic', available: true },
    { id: 'time_machine', label: 'Time Machine', available: true }
  ];
  await f.console._test.loadStored();
  assert.equal(f.console._test.state.provider, 'time_machine');
  assert.equal(store.size, 0, 'the preselect was not consumed');
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

test('a Time Machine review sends sources only and approves with "Check Time Machine once"', async () => {
  const f = fixture({ connection: 'not_configured' });
  f.settings.providers = bothProviders;
  await f.console._test.loadStored();
  f.console._test.chooseProvider('time_machine');
  f.console._test.state.sources = ['opaque-token'];
  await f.console._test.reviewSelection();
  const review = f.calls.find(call => call.url.endsWith('/review'));
  const body = JSON.parse(review.options.body);
  assert.equal(body.provider, 'time_machine');
  assert.deepEqual(body.source_selections, ['opaque-token']);
  for (const key of ['credential', 'volume_selection', 'repository_selection'])
    assert.equal(key in body, false, key + ' was sent for Time Machine');

  f.console._test.state.review = {
    provider: 'time_machine',
    generation: 'g1',
    revision: 1,
    review_token: 'review',
    source_paths: ['/Invented/Songs']
  };
  f.console.open({ tab: 'settings' });
  await settle();
  assert.doesNotMatch(f.document.body.textContent, /External volume/);
  assert.match(f.document.body.textContent, /Do not start, stop or change a backup/);
  assert.equal(findButton(f.document, 'Check Time Machine once').disabled, true);
});

test('a Time Machine read with no destination says "not set up" and offers restic instead', async () => {
  const f = fixture({
    provider: 'time_machine',
    destination: 'not_configured',
    time_machine: {
      permission_required: false,
      destinations: 0,
      mounted_destinations: 0,
      source_states: ['included']
    }
  });
  f.settings.providers = bothProviders;
  f.settings.configured = true;
  f.settings.provider = 'time_machine';
  f.settings.source_paths = ['/Invented/Songs'];
  f.console.open();
  await settle();
  assert.match(f.document.body.textContent, /Time Machine is not set up on this Mac/);
  assert.match(f.document.body.textContent, /Ori created nothing on any drive/);
  const link = f.document.body.all(
    node => node.tagName === 'a' && node.textContent === 'Open Time Machine settings'
  )[0];
  assert.equal(link.href, 'x-apple.systempreferences:com.apple.Time-Machine-Settings.extension');
  assert.match(f.document.body.textContent, /An encrypted repository you prepare on that drive/);
  await findButton(f.document, 'Use a restic repository instead').fire('click');
  assert.equal(f.console.activeTab(), 'settings');
  assert.equal(f.console._test.state.provider, 'restic');
  assert.ok(f.calls.every(call => !call.options?.method || call.options.method === 'GET'));
});

test('without Full Disk Access completions are unknown and the exact setting is named', async () => {
  const f = fixture({
    provider: 'time_machine',
    destination: 'present',
    snapshot_count: undefined,
    time_machine: {
      permission_required: true,
      destinations: 1,
      mounted_destinations: 1,
      source_states: ['included']
    }
  });
  f.settings.providers = bothProviders;
  f.settings.configured = true;
  f.settings.provider = 'time_machine';
  f.settings.source_paths = ['/Invented/Songs'];
  f.console.open();
  await settle();
  assert.match(f.document.body.textContent, /Time Machine needs Full Disk Access/);
  assert.match(f.document.body.textContent, /Privacy & Security → Full Disk Access/);
  assert.match(f.document.body.textContent, /Unknown — Full Disk Access is needed/);
  assert.match(f.document.body.textContent, /Inclusion unknown without Full Disk Access/);
  assert.doesNotMatch(f.document.body.textContent, /not set up/);
  const link = f.document.body.all(
    node => node.tagName === 'a' && node.textContent === 'Open Full Disk Access settings'
  )[0];
  assert.match(link.href, /Privacy_AllFiles$/);
});

test('the coverage deep link opens the Overview at each folder’s stored inclusion', async () => {
  const f = fixture(
    {
      provider: 'time_machine',
      destination: 'present',
      snapshot_count: 2,
      last_completed_at: '2026-01-01T19:00:00Z',
      time_machine: {
        permission_required: false,
        destinations: 1,
        mounted_destinations: 1,
        source_states: ['included', 'source_excluded']
      }
    },
    'http://localhost:8765/workspaces/owned-ws?panel=backup-recovery&tab=overview&section=coverage'
  );
  f.settings.providers = bothProviders;
  f.settings.configured = true;
  f.settings.provider = 'time_machine';
  f.settings.source_paths = ['/Invented/Songs', '/Invented/Scratch'];
  await f.console.init();
  await settle();
  const section = f.document.getElementById('brCoverage');
  assert.ok(section, 'no coverage section');
  assert.match(section.textContent, /\/Invented\/Songs .* Included in Time Machine/);
  assert.match(section.textContent, /\/Invented\/Scratch .* Excluded from Time Machine/);
  assert.match(section.textContent, /not proof of a recoverable copy/);
  assert.equal(f.document.activeElement, section);
  assert.ok(findButton(f.document, 'Check Time Machine now'));
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
