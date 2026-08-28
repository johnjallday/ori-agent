// Tests for plugin-lifecycle.js — the shared confirm-gated lifecycle client.
// Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/plugin-lifecycle.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.className = '';
    this.children = [];
    this.attributes = {};
    this._text = '';
  }
  get textContent() {
    return this._text + this.children.map(c => c.textContent).join('');
  }
  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  descendants() {
    return this.children.flatMap(c => [c, ...c.descendants()]);
  }
  hasClass(name) {
    return String(this.className).split(/\s+/).includes(name);
  }
}

const SOURCE = readFileSync(new URL('./plugin-lifecycle.js', import.meta.url), 'utf8');

function load(fetchImpl) {
  const sandbox = {
    window: {},
    document: { createElement: tag => new FakeElement(tag) },
    console,
    Object,
    Array,
    Number,
    String,
    Boolean,
    Promise,
    JSON,
    Error,
    fetch: fetchImpl
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'plugin-lifecycle.js' });
  return sandbox.window.PluginLifecycle;
}

function jsonResponse(status, body) {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body)
  };
}

// --- request parsing -------------------------------------------------------

test('a successful response comes back structured, not thrown', async () => {
  const PL = load(async () => jsonResponse(200, { installed: true }));
  const result = await PL.request('POST', '/x', { a: 1 });
  assert.equal(result.ok, true);
  assert.equal(result.status, 200);
  assert.deepEqual(result.data, { installed: true });
  assert.equal(result.error, '');
});

test('a failure reports the server message rather than an HTTP code', async () => {
  const PL = load(async () => jsonResponse(409, { error: 'required plugins are not ready' }));
  const result = await PL.request('POST', '/x');
  assert.equal(result.ok, false);
  assert.equal(result.status, 409);
  assert.equal(result.error, 'required plugins are not ready');
});

test('a failure with no usable body still explains itself', async () => {
  const PL = load(async () => ({ ok: false, status: 502, text: async () => '<html>bad gateway' }));
  const result = await PL.request('POST', '/x');
  assert.equal(result.ok, false);
  // A proxy error page must not surface as a JSON parse error the user cannot
  // act on.
  assert.match(result.error, /failed \(502\)/);
});

test('a 409 with no message says what a conflict means', async () => {
  const PL = load(async () => jsonResponse(409, {}));
  const result = await PL.request('POST', '/x');
  assert.match(result.error, /changed while you were working/);
});

test('a network failure is reported as offline, not as a crash', async () => {
  const PL = load(async () => {
    throw new TypeError('Failed to fetch');
  });
  const result = await PL.request('POST', '/x');
  assert.equal(result.ok, false);
  assert.equal(result.offline, true);
  assert.match(result.error, /could not reach the server/);
});

// --- trust disclosure ------------------------------------------------------

const TRUST = {
  MCPCommands: ['/usr/local/bin/thing --serve'],
  Skills: ['session-setup'],
  Unsupported: [{ kind: 'hook', detail: 'deferred' }],
  Warnings: ['the binary is missing']
};

test('the trust model normalizes both key styles and fills gaps', () => {
  const PL = load(async () => jsonResponse(200, {}));
  const model = PL.trustModel(TRUST);
  assert.deepEqual(model.mcpCommands, ['/usr/local/bin/thing --serve']);
  assert.deepEqual(model.skills, ['session-setup']);
  // Field-by-field: the module is loaded in its own vm context, so an object
  // it built has that realm's prototype and deepEqual compares those.
  assert.equal(model.unsupported.length, 1);
  assert.equal(model.unsupported[0].kind, 'hook');
  assert.equal(model.unsupported[0].detail, 'deferred');
  assert.deepEqual(model.warnings, ['the binary is missing']);

  const empty = PL.trustModel(undefined);
  assert.equal(empty.mcpCommands.length, 0);
  assert.equal(empty.skills.length, 0);
  assert.equal(empty.unsupported.length, 0);
  assert.equal(empty.warnings.length, 0);
  assert.equal(PL.isEmptyTrust(empty), true);
});

test('the disclosure shows every section, including the warnings', () => {
  const PL = load(async () => jsonResponse(200, {}));
  const report = PL.renderTrustReport(TRUST);
  const text = report.textContent;
  // Nothing is summarized or hidden: this is the last thing a user reads
  // before a plugin can run commands on their machine.
  assert.match(text, /Runs these commands on your computer/);
  assert.match(text, /\/usr\/local\/bin\/thing --serve/);
  assert.match(text, /Adds these skills/);
  assert.match(text, /session-setup/);
  assert.match(text, /Skipped/);
  assert.match(text, /hook: deferred/);
  assert.match(text, /Warnings/);
  assert.match(text, /the binary is missing/);
});

// A workspace-surface plugin adds background services, downloadable
// executables, browser UI, and named permission scopes. The disclosure that
// omitted them was showing the user a fraction of what they were agreeing to.
test('the disclosure covers services, artifacts, permissions, and surfaces', () => {
  const PL = load(async () => jsonResponse(200, {}));
  const report = PL.renderTrustReport({
    Services: [{ ID: 'demo', Transport: 'mcp_stdio', Executable: 'demo-service' }],
    Artifacts: [{ ID: 'svc', Platform: 'darwin/arm64', SHA256: 'abc123' }],
    SymbolicScopes: ['workspace.files.read', 'network.outbound'],
    Surfaces: [{ Label: 'Demo Console', Capability: 'demo-tools', BrowserUI: true }],
    SurfaceCapabilities: ['demo-tools'],
    Blueprints: ['demo-workspace']
  });
  const text = report.textContent;

  assert.match(text, /Runs these background services/);
  assert.match(text, /demo-service \(mcp_stdio\)/);
  assert.match(text, /Downloads and runs these files/);
  assert.match(text, /darwin\/arm64/);
  // The digest is what makes the download verifiable.
  assert.match(text, /sha256 abc123/);
  // Permissions are listed one by one, never folded into a count.
  assert.match(text, /Grants these permissions/);
  assert.match(text, /workspace\.files\.read/);
  assert.match(text, /network\.outbound/);
  assert.match(text, /Adds these screens inside Ori/);
  assert.match(text, /Demo Console \(runs its own web page inside Ori\)/);
  assert.match(text, /Adds these workspace capabilities/);
  assert.match(text, /Adds these blueprints/);
  assert.match(text, /demo-workspace/);
});

test('a report carrying only surfaces is not treated as empty', () => {
  const PL = load(async () => jsonResponse(200, {}));
  const model = PL.trustModel({ SymbolicScopes: ['network.outbound'] });
  assert.equal(PL.isEmptyTrust(model), false);
  assert.doesNotMatch(
    PL.renderTrustReport({ SymbolicScopes: ['x'] }).textContent,
    /registers nothing/
  );
});

test('a plugin that registers nothing says so', () => {
  const PL = load(async () => jsonResponse(200, {}));
  assert.match(PL.renderTrustReport({}).textContent, /registers nothing/);
});

test('command lines are rendered as text, never as markup', () => {
  const PL = load(async () => jsonResponse(200, {}));
  const hostile = '<img src=x onerror="alert(1)">';
  const report = PL.renderTrustReport({ MCPCommands: [hostile] });
  const code = report.descendants().find(node => node.tagName === 'CODE');
  assert.equal(code._text, hostile);
});

// --- the confirm gate ------------------------------------------------------

function flowHarness(overrides = {}) {
  const PL = load(async () => jsonResponse(200, {}));
  const calls = { preview: 0, apply: 0 };
  const states = [];
  const flow = PL.createFlow({
    preview: async () => {
      calls.preview++;
      return overrides.preview ? overrides.preview() : { ok: true, data: { trust: TRUST } };
    },
    apply: async () => {
      calls.apply++;
      return overrides.apply
        ? overrides.apply()
        : { ok: true, data: { outcome: { completed: true } } };
    },
    onState: state => states.push(state)
  });
  return { PL, flow, calls, states };
}

test('apply never runs before a preview has returned', async () => {
  const { flow, calls } = flowHarness();
  // Confirming from idle is refused: the user has been shown nothing.
  assert.equal(await flow.confirm(), null);
  assert.equal(calls.apply, 0);

  await flow.start();
  await flow.confirm();
  assert.equal(calls.apply, 1);
});

test('a second confirmation cannot apply the action twice', async () => {
  const { flow, calls } = flowHarness();
  await flow.start();
  const [first, second] = await Promise.all([flow.confirm(), flow.confirm()]);
  assert.equal(calls.apply, 1, 'apply ran more than once');
  assert.ok(first);
  assert.equal(second, null);
});

test('cancelling makes a pending confirmation unusable', async () => {
  const { flow, calls, PL } = flowHarness();
  await flow.start();
  flow.cancel();
  assert.equal(flow.state, PL.STATES.CANCELLED);
  assert.equal(await flow.confirm(), null);
  assert.equal(calls.apply, 0);
});

test('invalidating drops a pending confirmation without announcing anything', async () => {
  const { flow, calls, states, PL } = flowHarness();
  await flow.start();
  const before = states.length;
  flow.invalidate();
  // The user made no decision, so nothing is reported.
  assert.equal(states.length, before, 'invalidate emitted a state change');
  assert.equal(flow.state, PL.STATES.IDLE);
  assert.equal(await flow.confirm(), null);
  assert.equal(calls.apply, 0);
});

test('a preview that resolves after a cancel is discarded', async () => {
  let release;
  const pending = new Promise(resolve => {
    release = resolve;
  });
  const { flow, states, PL } = flowHarness({ preview: () => pending });

  const started = flow.start();
  flow.cancel();
  release({ ok: true, data: { trust: TRUST } });
  assert.equal(await started, null, 'a stale preview was accepted');
  // The last state is the cancel, not a confirmation prompt for a question
  // nobody is asking any more.
  assert.equal(states[states.length - 1], PL.STATES.CANCELLED);
});

test('a failed preview reports the failure and offers nothing to confirm', async () => {
  const { flow, calls, PL } = flowHarness({
    preview: () => ({ ok: false, error: 'could not read the plugin' })
  });
  const result = await flow.start();
  assert.equal(result.ok, false);
  assert.equal(flow.state, PL.STATES.FAILED);
  assert.equal(await flow.confirm(), null);
  assert.equal(calls.apply, 0);
});

test('a failed apply leaves the flow failed rather than done', async () => {
  const { flow, PL } = flowHarness({ apply: () => ({ ok: false, error: 'enable failed' }) });
  await flow.start();
  const result = await flow.confirm();
  assert.equal(result.ok, false);
  assert.equal(flow.state, PL.STATES.FAILED);
});

test('the full happy path walks preview → confirm → done', async () => {
  const { flow, states, PL } = flowHarness();
  await flow.start();
  await flow.confirm();
  assert.deepEqual(states, [
    PL.STATES.PREVIEWING,
    PL.STATES.AWAITING_CONFIRMATION,
    PL.STATES.APPLYING,
    PL.STATES.DONE
  ]);
});

test('restarting while a preview is in flight does not stack flows', async () => {
  const { flow, calls } = flowHarness();
  const [first, second] = await Promise.all([flow.start(), flow.start()]);
  assert.equal(calls.preview, 1, 'preview ran twice');
  assert.ok(first);
  assert.equal(second, null);
});
