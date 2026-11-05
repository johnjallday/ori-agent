// Tests for agent-settings-ownership.js — the read-only "where settings come
// from" section of the full agent page.
//   node --test internal/web/static/js/modules/agent-settings-ownership.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-settings-ownership.js', import.meta.url), 'utf8');

// ---- Minimal DOM: text-only nodes. Assigning innerHTML throws, so any
// accidental HTML rendering of server data fails the test.
function makeDocument() {
  const doc = {
    createElement(tag) {
      return makeNode(tag, doc);
    },
    createTextNode(value) {
      return { nodeType: 3, textContent: String(value) };
    }
  };
  return doc;
}

function makeNode(tag, doc) {
  const listeners = {};
  const attributes = {};
  const node = {
    tagName: tag.toUpperCase(),
    ownerDocument: doc,
    children: [],
    dataset: {},
    className: '',
    hidden: true,
    disabled: false,
    value: '',
    type: '',
    _text: '',
    get textContent() {
      return this.children.length ? this.children.map(c => c.textContent).join('') : this._text;
    },
    set textContent(value) {
      this.children = [];
      this._text = String(value);
    },
    set innerHTML(_value) {
      throw new Error('innerHTML must not be used');
    },
    append(...nodes) {
      this.children.push(...nodes);
    },
    replaceChildren(...nodes) {
      this.children = nodes;
      this._text = '';
    },
    setAttribute(k, v) {
      attributes[k] = String(v);
    },
    getAttribute(k) {
      return attributes[k] ?? null;
    },
    removeAttribute(k) {
      delete attributes[k];
    },
    addEventListener(type, handler) {
      (listeners[type] ||= []).push(handler);
    },
    fire(type) {
      for (const handler of listeners[type] || []) handler({ target: node });
    },
    find(predicate) {
      if (predicate(this)) return this;
      for (const child of this.children) {
        const hit = child.find?.(predicate);
        if (hit) return hit;
      }
      return null;
    }
  };
  return node;
}

function makeWindow(search = '') {
  const listeners = {};
  const win = {
    location: { pathname: '/agents/Writer', search, hash: '' },
    history: {
      pushed: [],
      pushState(state, _title, url) {
        this.pushed.push(url);
        const q = url.indexOf('?');
        win.location.search = q >= 0 ? url.slice(q) : '';
      }
    },
    addEventListener(type, handler) {
      (listeners[type] ||= []).push(handler);
    },
    back(search) {
      win.location.search = search;
      for (const handler of listeners.popstate || []) handler({});
    }
  };
  return win;
}

function load(win) {
  const sandbox = {
    window: win || {},
    URLSearchParams,
    AbortController,
    console
  };
  vm.runInNewContext(source, sandbox);
  return sandbox.window.AgentSettingsOwnership;
}

const api = load();

function makeRoot() {
  const doc = makeDocument();
  const root = makeNode('section', doc);
  const select = makeNode('select', doc);
  const status = makeNode('p', doc);
  const body = makeNode('div', doc);
  root.querySelector = selector =>
    ({
      '[data-ownership-context]': select,
      '[data-ownership-status]': status,
      '[data-ownership-body]': body
    })[selector] || null;
  return { root, select, status, body };
}

const field = (key, value, extra = {}) => ({
  key,
  label: key,
  value,
  empty: false,
  comparison: 'not_compared',
  ...extra
});

function projection({
  kind = 'library',
  state = 'ok',
  ws = null,
  model = 'm',
  source = 'library',
  def = 'ok',
  libraryState = 'present',
  contexts = []
} = {}) {
  return {
    agent: 'Writer',
    library: { state: libraryState },
    contexts: { state: 'ok', workspaces: contexts },
    context: { kind, state, workspace: ws, message: state === 'ok' ? '' : 'Gone.' },
    definition: { state: def, source, fields: def === 'ok' ? [field('model', model)] : [] },
    tracking: { state: kind === 'workspace' ? 'untracked' : 'not_applicable' },
    instances: { state: 'none', items: [] },
    excluded: 'API keys … are not shown here.'
  };
}

function jsonResponse(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function deferred() {
  let resolve;
  const promise = new Promise(r => (resolve = r));
  return { promise, resolve };
}

const tick = () => new Promise(r => setImmediate(r));

// ---- Pure helpers ----------------------------------------------------------

test('URL context round-trips and keeps unrelated parameters', () => {
  const ctx = api.parseContext('?tab=configuration&ctx=workspace&studio_id=ws%201&instance_id=i-2');
  assert.deepEqual({ ...ctx }, { kind: 'workspace', studioId: 'ws 1', instanceId: 'i-2' });
  const search = api.writeContext('?tab=configuration', ctx);
  assert.match(search, /tab=configuration/);
  assert.deepEqual({ ...api.parseContext(search) }, { ...ctx });
  assert.equal(
    api.writeContext('?tab=prompt&ctx=workspace&studio_id=x', { kind: 'library' }),
    '?tab=prompt'
  );
});

test('malformed or oversized URL context falls back to the library view, never another workspace', () => {
  for (const search of [
    '?ctx=workspace',
    '?studio_id=ws-1',
    `?ctx=workspace&studio_id=${'x'.repeat(201)}`,
    '?ctx=nope&studio_id=ws'
  ]) {
    assert.equal(api.parseContext(search).kind, 'library', search);
  }
  assert.equal(
    api.parseContext(`?ctx=workspace&studio_id=ws&instance_id=${'y'.repeat(201)}`).instanceId,
    ''
  );
});

test('request URLs encode names and IDs', () => {
  assert.equal(
    api.requestURL('Writer Ü/x', { kind: 'library' }),
    '/api/agents/Writer%20%C3%9C%2Fx/settings-ownership'
  );
  assert.equal(
    api.requestURL('Writer', { kind: 'workspace', studioId: 'ws&1', instanceId: 'i 2' }),
    '/api/agents/Writer/settings-ownership?studio_id=ws%261&instance_id=i+2'
  );
});

test('view model shows values only for a verified definition', () => {
  const ok = api.viewModel(
    projection({ kind: 'workspace', ws: { name: 'Studio' }, source: 'workspace_copy' })
  );
  assert.equal(ok.heading, 'In workspace “Studio”');
  assert.equal(ok.source, "This workspace's own saved copy");
  assert.equal(ok.fields.length, 1);
  assert.match(ok.tracking, /independent copy/);

  const degraded = api.viewModel(
    projection({ kind: 'workspace', ws: { name: 'S' }, def: 'degraded', source: 'unknown' })
  );
  assert.equal(degraded.fields.length, 0);
  assert.equal(degraded.source, 'Could not be verified');

  const gone = api.viewModel(projection({ kind: 'workspace', state: 'not_found' }));
  assert.equal(gone.heading, 'In an unavailable workspace');
  assert.equal(gone.fields.length, 0);
  assert.equal(gone.source, '');
  assert.match(gone.message, /^Gone\. Choose another place/);
});

test('empty values read as not set; explicit zero stays a value', () => {
  assert.equal(
    api.formatValue({ key: 'max_output_tokens', value: 0, empty: true }),
    'Not set (provider default)'
  );
  assert.equal(api.formatValue({ key: 'reasoning_effort', value: '', empty: true }), 'Not set');
  assert.equal(api.formatValue({ key: 'temperature', value: 0, empty: false }), '0');
});

test('a workspace-owned agent gets no fake library option; an unknown URL workspace stays visible', () => {
  const options = api.contextOptions(
    projection({ libraryState: 'absent', contexts: [{ id: 'a', name: 'A' }] }),
    { kind: 'workspace', studioId: 'gone' }
  );
  assert.deepEqual([...options.map(o => o.value)], ['workspace:a', 'workspace:gone']);
  assert.equal(options[1].label, 'Unavailable workspace');
  const withLibrary = api.contextOptions(projection({ contexts: [{ id: 'a', name: 'A' }] }), {
    kind: 'library'
  });
  assert.deepEqual([...withLibrary.map(o => o.label)], ['Library profile', 'Workspace: A']);
});

// ---- Mounted behavior ------------------------------------------------------

test('mount renders the library context as text and offers workspaces', async () => {
  const win = makeWindow('');
  const mod = load(win);
  const { root, select, body } = makeRoot();
  const calls = [];
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    fetchImpl: async url => {
      calls.push(url);
      return jsonResponse(
        projection({
          model: '<img src=x onerror=alert(1)>',
          contexts: [{ id: 'ws-1', name: 'One' }]
        })
      );
    }
  });
  await tick();
  assert.deepEqual(calls, ['/api/agents/Writer/settings-ownership']);
  assert.equal(root.hidden, false);
  assert.match(body.textContent, /<img src=x onerror=alert\(1\)>/);
  assert.equal(select.children.length, 2);
  assert.equal(select.value, 'library');
  // Demo 1.8 defect: every row has exactly three cells, compared or not, so
  // the grid can never pair a label with the next row's value.
  const list = body.find(n => n.tagName === 'DL');
  for (const row of list.children) assert.equal(row.children.length, 3, row.dataset.field);
});

test('switching context pushes history, clears the old values at once and ignores a late answer', async () => {
  const win = makeWindow('');
  const mod = load(win);
  const { root, select, body } = makeRoot();
  const pending = [];
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    fetchImpl: url => {
      const d = deferred();
      pending.push({ url, d });
      return d.promise;
    }
  });
  pending[0].d.resolve(
    jsonResponse(
      projection({
        model: 'library-model',
        contexts: [
          { id: 'ws-1', name: 'One' },
          { id: 'ws-2', name: 'Two' }
        ]
      })
    )
  );
  await tick();
  assert.match(body.textContent, /library-model/);

  select.value = 'workspace:ws-1';
  select.fire('change');
  assert.equal(body.textContent, '', 'old context values cleared before the new answer');
  assert.deepEqual(win.history.pushed, ['/agents/Writer?ctx=workspace&studio_id=ws-1']);

  select.value = 'workspace:ws-2';
  select.fire('change');
  // ws-2 answers first; ws-1's late answer must not replace it.
  pending[2].d.resolve(
    jsonResponse(
      projection({
        kind: 'workspace',
        ws: { id: 'ws-2', name: 'Two' },
        model: 'two-model',
        source: 'workspace_copy'
      })
    )
  );
  await tick();
  pending[1].d.resolve(
    jsonResponse(
      projection({
        kind: 'workspace',
        ws: { id: 'ws-1', name: 'One' },
        model: 'one-model',
        source: 'workspace_copy'
      })
    )
  );
  await tick();
  assert.match(body.textContent, /two-model/);
  assert.doesNotMatch(body.textContent, /one-model/);
  assert.equal(root.dataset.context, 'workspace:ws-2:');

  // Back re-reads the URL's context without pushing again.
  win.back('');
  assert.equal(win.history.pushed.length, 2);
  assert.equal(pending.at(-1).url, '/api/agents/Writer/settings-ownership');
});

test('a failed read shows a retry in place and selects nothing else', async () => {
  const win = makeWindow('?ctx=workspace&studio_id=ws-9');
  const mod = load(win);
  const { root, body, status } = makeRoot();
  let attempt = 0;
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    fetchImpl: async url => {
      attempt++;
      assert.match(url, /studio_id=ws-9/);
      return attempt === 1
        ? jsonResponse({}, 500)
        : jsonResponse(
            projection({
              kind: 'workspace',
              ws: { id: 'ws-9', name: 'Nine' },
              source: 'workspace_copy'
            })
          );
    }
  });
  await tick();
  assert.equal(status.dataset.state, 'error');
  assert.match(body.textContent, /could not be loaded/);
  const retry = body.find(n => n.tagName === 'BUTTON');
  assert.ok(retry, 'retry button offered');
  retry.fire('click');
  await tick();
  assert.equal(attempt, 2);
  assert.match(body.textContent, /Nine/);
  assert.equal(root.getAttribute('aria-busy'), null);
});
