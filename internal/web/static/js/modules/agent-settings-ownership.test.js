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
  const { root, body, status, select } = makeRoot();
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
  assert.equal(select.children[0].textContent, 'Selected workspace');
  assert.equal(select.value, 'workspace:ws-9');
  assert.match(body.textContent, /could not be loaded/);
  const retry = body.find(n => n.tagName === 'BUTTON');
  assert.ok(retry, 'retry button offered');
  retry.fire('click');
  await tick();
  assert.equal(attempt, 2);
  assert.match(body.textContent, /Nine/);
  assert.equal(root.getAttribute('aria-busy'), null);
});

// ---- Group 2: instances, prompt, conversation model, drafts ---------------

const dupItems = [
  { id: 'i-1', instance_number: 1, role: 'Drafts', entry_point: true },
  { id: 'i-2', instance_number: 2, role: 'Edits' }
];

function workspaceProjection(extra = {}) {
  return {
    ...projection({ kind: 'workspace', ws: { id: 'ws-d', name: 'Dup' }, source: 'workspace_copy' }),
    ...extra
  };
}

test('several instances are listed with nothing selected; a stale instance is reported', () => {
  const amb = api.instanceChoice(
    workspaceProjection({ instances: { state: 'ambiguous', items: dupItems } })
  );
  assert.equal(amb.show, true);
  assert.equal(amb.selected, '');
  assert.equal(amb.options[0].label, 'Choose an instance');
  assert.equal(amb.options[1].label, 'Instance 1 · Drafts · workspace entry');
  assert.match(amb.message, /several instances/);

  const stale = api.instanceChoice(
    workspaceProjection({ instances: { state: 'not_found', items: dupItems.slice(0, 1) } })
  );
  assert.equal(stale.show, true);
  assert.match(stale.message, /no longer in this workspace/);

  const single = api.instanceChoice(
    workspaceProjection({
      instances: { state: 'selected', items: dupItems.slice(0, 1), selected_id: 'i-1' }
    })
  );
  assert.equal(single.show, false);

  const legacy = api.instanceChoice(
    workspaceProjection({ instances: { state: 'unidentified', items: [{ id: '' }] } })
  );
  assert.equal(legacy.show, false);
  assert.match(legacy.message, /stable instance ID/);
});

test('prompt view separates base, guidance and the together preview', () => {
  const view = api.promptView(
    workspaceProjection({
      prompt: {
        state: 'ok',
        base_source: 'workspace_copy',
        base: 'Base.',
        layers: [{ key: 'role', label: 'Role here', value: 'Edits' }],
        preview: 'Base.\n\n---\nWorkspace-specific guidance…',
        has_variables: true,
        note: 'Saved text only.'
      }
    })
  );
  assert.equal(view.show, true);
  assert.equal(view.baseSource, "This workspace's own saved copy");
  assert.equal(view.layers[0].label, 'Role here');
  assert.match(view.preview, /---/);
  assert.equal(view.variables, true);

  const needs = api.promptView(
    workspaceProjection({ prompt: { state: 'needs_instance', base: 'Base.', preview: 'x' } })
  );
  assert.equal(needs.needsInstance, true);
  assert.equal(needs.preview, '', 'never previews guidance for an unchosen instance');

  assert.equal(
    api.promptView(workspaceProjection({ prompt: { state: 'unavailable' } })).show,
    false
  );
});

test('conversation model is read only for a supported context and never for another recipient', () => {
  assert.equal(api.conversationRequest(projection()), null, 'a library card is not a reader');
  const linked = api.conversationRequest(projection(), { systemModelLinked: true });
  assert.equal(linked.context.kind, undefined);
  const ws = api.conversationRequest(workspaceProjection());
  assert.equal(ws.context.kind, 'workspace_entry');
  assert.equal(ws.context.workspaceId, 'ws-d');
  assert.equal(ws.context.name, 'Writer');
  assert.equal(
    api.conversationRequest(workspaceProjection({ definition: { state: 'degraded' } })),
    null
  );

  const describe = s => (s?.state === 'configured' ? `${s.provider} / ${s.model}` : 'unknown');
  const other = api.conversationView(
    ws,
    { state: 'configured', provider: 'openai', model: 'x', recipient: 'Commander' },
    'm',
    describe
  );
  assert.equal(other.value, 'unknown');
  const differs = api.conversationView(
    ws,
    { state: 'configured', provider: 'openai', model: 'gpt-5', recipient: 'writer' },
    'gpt-4o-mini',
    describe
  );
  assert.equal(differs.value, 'openai / gpt-5');
  assert.match(differs.note, /differs from the saved model/);
  assert.equal(api.conversationView(ws, null, 'm', describe).value, 'unknown');
});

test('choosing an instance pushes it into the URL and renders that instance only', async () => {
  const win = makeWindow('?ctx=workspace&studio_id=ws-d');
  const mod = load(win);
  const { root, body } = makeRoot();
  const urls = [];
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    modelApi: async () => ({
      readModelSelection: async () => ({ state: 'unknown' }),
      modelDescription: () => 'unknown',
      modelSettingsHref: () => ''
    }),
    fetchImpl: async url => {
      urls.push(url);
      const selected = url.includes('instance_id=i-2');
      return jsonResponse(
        workspaceProjection({
          instances: selected
            ? { state: 'selected', items: dupItems, selected_id: 'i-2' }
            : { state: 'ambiguous', items: dupItems },
          prompt: selected
            ? {
                state: 'ok',
                base: 'B',
                base_source: 'workspace_copy',
                layers: [{ label: 'Role here', value: 'Edits' }],
                preview: 'B + Edits',
                note: 'n'
              }
            : { state: 'needs_instance', base: 'B', base_source: 'workspace_copy', note: 'n' }
        })
      );
    }
  });
  await tick();
  assert.match(body.textContent, /Choose an instance to see its workspace guidance/);
  const picker = body.find(n => n.id === 'settingsOwnershipInstance');
  assert.ok(picker);
  assert.equal(picker.value, '');
  picker.value = 'i-2';
  picker.fire('change');
  await tick();
  assert.equal(
    win.history.pushed.at(-1),
    '/agents/Writer?ctx=workspace&studio_id=ws-d&instance_id=i-2'
  );
  assert.match(urls.at(-1), /instance_id=i-2/);
  assert.match(body.textContent, /B \+ Edits/);
  assert.match(body.textContent, /Workspace chat model: unknown/);
});

test('confirmed versions refresh the view; drafts only show a note', async () => {
  const win = makeWindow('');
  const mod = load(win);
  const { root } = makeRoot();
  const unsaved = makeNode('p', root.ownerDocument);
  const base = root.querySelector;
  root.querySelector = s => (s === '[data-ownership-unsaved]' ? unsaved : base(s));
  let reads = 0;
  const view = mod.mount({
    root,
    agentName: 'Writer',
    win,
    fetchImpl: async () => {
      reads++;
      return jsonResponse(projection());
    }
  });
  await tick();
  view.syncAgent({ name: 'Writer', version: 'v1' });
  view.syncAgent({ name: 'Writer', version: 'v1' });
  assert.equal(reads, 1, 'first sync and unchanged versions do not re-read');
  view.setUnsaved(true);
  assert.equal(unsaved.hidden, false);
  assert.match(unsaved.textContent, /not shown here until they are saved/);
  assert.equal(reads, 1, 'a draft never triggers a read');
  view.syncAgent({ name: 'Writer', version: 'v2' });
  await tick();
  assert.equal(reads, 2, 'a confirmed save re-reads');
  view.syncAgent({ name: 'Writer Renamed', version: 'v2' });
  await tick();
  assert.equal(reads, 3, 'a confirmed rename re-reads');
  view.setUnsaved(false);
  assert.equal(unsaved.hidden, true);
});

// ---- Group 3: Toolboxes and permission ownership ---------------------------

test('Default and workspace Toolboxes stay separate and never claim readiness', () => {
  const lib = api.toolboxView({
    ...projection(),
    direct_toolbox: {
      state: 'selected',
      version: 2,
      inventory: 'unavailable',
      skills: [{ id: 'a', name: 'A', source: 'agent_learned', availability: 'unknown' }]
    },
    workspace_toolbox: { state: 'not_applicable' },
    permissions: { workspace_native_tools: 'not_applicable', notes: ['A Toolbox only narrows.'] }
  });
  assert.equal(lib.direct.summary, 'Selected skills (version 2)');
  assert.equal(lib.direct.skills[0].availability, 'Availability unknown');
  assert.equal(lib.direct.inventoryUnknown, true);
  assert.match(lib.direct.editor, /no editor for the Default Toolbox/);
  assert.equal(lib.workspace.show, false, 'no workspace Toolbox in the library context');
  assert.equal(lib.permissions.native, '');

  for (const [state, summary] of [
    ['empty', /Explicitly empty/],
    ['legacy', /Not chosen yet/]
  ]) {
    assert.match(
      api.toolboxView({ ...projection(), direct_toolbox: { state } }).direct.summary,
      summary
    );
  }

  const ws = api.toolboxView({
    ...workspaceProjection(),
    direct_toolbox: { state: 'not_applicable', note: 'Outside workspaces only.' },
    workspace_toolbox: {
      state: 'pinned',
      toolbox_id: 'tb',
      toolbox_name: 'Research',
      pinned_version: 1,
      current_version: 2,
      skills: [
        {
          id: 'n',
          name: 'Notes',
          source: 'workspace_provided',
          availability: 'switched_off',
          required: true
        }
      ],
      connections: [
        {
          binding_id: 'gh',
          name: 'GitHub',
          state: 'available',
          tools: ['list_issues'],
          no_longer_allowed: ['create_issue'],
          repo_scope: 'acme/site'
        },
        { binding_id: 'w', name: 'Wide', state: 'available', all_tools: true, tools: [] }
      ],
      core: ['Workspace files']
    },
    permissions: { workspace_native_tools: 'allowed', notes: ['n'] }
  });
  assert.equal(ws.direct.editor, '', 'not-applicable Default Toolbox has no editor line');
  assert.match(
    ws.workspace.pin,
    /“Research”, pinned to version 1 \(the Toolbox is now at version 2/
  );
  assert.equal(ws.workspace.skills[0].availability, 'Switched off in this workspace');
  assert.equal(ws.workspace.connections[0].tools, 'Operations: list_issues');
  assert.match(ws.workspace.connections[0].noLongerAllowed, /create_issue/);
  assert.equal(ws.workspace.connections[0].repo, 'Limited to repository acme/site');
  assert.match(ws.workspace.connections[1].tools, /not pinned/);
  assert.match(ws.permissions.native, /allowed in this workspace \(each agent must also opt in\)/);

  const broken = api.toolboxView({
    ...workspaceProjection(),
    workspace_toolbox: {
      state: 'broken',
      toolbox_id: 'tb',
      pinned_version: 9,
      note: 'needs repair'
    }
  });
  assert.match(broken.workspace.pin, /version 9 does not resolve/);
  assert.equal(broken.workspace.connections.length, 0);
});

test('Toolbox sections render as text, with progressive disclosure', async () => {
  const win = makeWindow('?ctx=workspace&studio_id=ws-d');
  const mod = load(win);
  const { root, body } = makeRoot();
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    modelApi: async () => ({
      readModelSelection: async () => null,
      modelDescription: () => 'unknown'
    }),
    fetchImpl: async () =>
      jsonResponse({
        ...workspaceProjection({
          instances: { state: 'selected', items: dupItems.slice(0, 1), selected_id: 'i-1' }
        }),
        direct_toolbox: { state: 'not_applicable', note: 'Outside workspaces only.' },
        workspace_toolbox: {
          state: 'pinned',
          toolbox_name: '<b>Research</b>',
          pinned_version: 1,
          current_version: 1,
          skills: [
            { id: 'x', name: '<img src=x>', source: 'agent_learned', availability: 'missing' }
          ],
          connections: []
        },
        permissions: { workspace_native_tools: 'not_allowed', notes: ['A Toolbox only narrows.'] }
      })
  });
  await tick();
  const direct = body.find(n => n.dataset?.ownershipToolbox === 'direct');
  const wsBlock = body.find(n => n.dataset?.ownershipToolbox === 'workspace');
  assert.match(direct.textContent, /Outside workspaces only\./);
  assert.match(wsBlock.textContent, /“<b>Research<\/b>”, pinned to version 1/);
  assert.match(wsBlock.textContent, /<img src=x>/);
  assert.ok(
    wsBlock.find(n => n.tagName === 'DETAILS'),
    'long lists are collapsible'
  );
  assert.match(body.textContent, /Trusted native tools: not allowed in this workspace\./);
});

// ---- Group 4: owners, routes and impact ------------------------------------

const copyProjection = (ws, extra = {}) => ({
  ...projection({ kind: 'workspace', ws, source: 'workspace_copy' }),
  agent: 'Writer Ü',
  ...extra
});

test('library context links to this page’s editors and the app Default model only when linked', () => {
  const links = api.ownerLinks({ ...projection(), agent: 'Writer' });
  assert.deepEqual(
    [...links.map(l => `${l.family}:${l.href}`)],
    ['configuration:#agentConfigurationSection', 'profile:#agentHeader', 'prompt:#promptSection']
  );
  const linked = api.ownerLinks({ ...projection(), agent: 'Writer' }, { systemModelLinked: true });
  assert.equal(linked.at(-1).href, '/settings#system-model');
  assert.match(linked.at(-1).scope, /every conversation that uses the Default model/);
  assert.deepEqual([...api.workspaceOwnerLinks(projection())], []);
});

test('a workspace copy routes to the workspace’s editors with encoded names and per-name scope', () => {
  const ws = { id: 'ws-1', name: 'Studio', folder_slug: 'my-studio' };
  const links = api.ownerLinks(copyProjection(ws, { tracking: { state: 'in_step' } }));
  const config = links.find(l => l.family === 'configuration');
  assert.equal(config.href, '/workspaces/my-studio?editAgentModel=Writer+%C3%9C');
  assert.match(config.scope, /every instance named Writer Ü here/);
  assert.match(config.scope, /treats the copy as customized/);
  assert.match(config.scope, /no editor for a workspace copy/);
  assert.equal(
    links.find(l => l.family === 'prompt').href,
    '/workspaces/my-studio/agents/Writer%20%C3%9C#wad-prompt'
  );
  assert.equal(links.find(l => l.family === 'profile').href, undefined, 'no editor, no link');

  const instance = api.workspaceOwnerLinks(copyProjection(ws));
  assert.match(instance[0].message, /updates every instance named Writer Ü/);
  assert.equal(instance[1].href, '/workspaces/my-studio?agent=writer+%C3%BC');
  assert.match(instance[1].scope, /Opening Loadout can give an instance/);
});

test('hostile or unusable workspace identity never becomes a link', () => {
  for (const ws of [
    { id: '../../etc', folder_slug: '../x' },
    { id: 'javascript:alert(1)', folder_slug: 'Not A Slug' },
    { id: '', folder_slug: '' }
  ]) {
    const links = [
      ...api.ownerLinks(copyProjection(ws)),
      ...api.workspaceOwnerLinks(copyProjection(ws))
    ];
    for (const link of links) {
      if (link.href)
        assert.match(link.href, /^\/workspaces\/[A-Za-z0-9_-]+[?/]/, JSON.stringify(ws));
    }
    assert.ok(
      links.some(l => l.message),
      'unaddressable workspace says so'
    );
  }
  // An ID is an acceptable fallback when the slug is not canonical.
  const byId = api.ownerLinks(copyProjection({ id: 'ws_42', folder_slug: 'Bad Slug' }));
  assert.match(byId.find(l => l.family === 'configuration').href, /^\/workspaces\/ws_42\?/);
});

test('a workspace that reads the library routes to the library editors and says what else it reaches', () => {
  const links = api.ownerLinks({
    ...projection({
      kind: 'workspace',
      ws: { id: 'w', folder_slug: 'w' },
      source: 'library_fallback'
    })
  });
  assert.equal(links[0].href, '#agentConfigurationSection');
  assert.match(links[0].scope, /any other workspace without a copy/);
  assert.match(links[0].scope, /until Ori takes a copy for it/);
  assert.deepEqual([...api.ownerLinks(projection({ def: 'degraded' }))], []);
});

test('impact groups workspaces by tracking, never by equal values', () => {
  const view = api.impactView({
    ...projection(),
    impact: {
      state: 'partial',
      not_carried: 'API keys … never copied.',
      copies: [
        { workspace_id: 'a', name: 'A', effect: 'kept_in_step' },
        { workspace_id: 'b', name: 'B', effect: 'customized' },
        { workspace_id: 'c', name: 'C', effect: 'independent' },
        { workspace_id: 'd', name: 'D', effect: 'reads_library' },
        { workspace_id: 'e', name: 'E', effect: 'unknown' }
      ]
    }
  });
  assert.equal(view.show, true);
  assert.equal(view.partial, true);
  assert.deepEqual(
    [...view.groups.map(g => `${g.effect}=${g.names.join('')}`)],
    ['reads_library=D', 'kept_in_step=A', 'customized=B', 'independent=C', 'unknown=E']
  );
  assert.equal(
    api.impactView(workspaceProjection({ impact: { state: 'ok', copies: [] } })).show,
    false
  );
});

test('a disabled skill reads as turned off, never available', () => {
  const view = api.toolboxView({
    ...projection(),
    direct_toolbox: {
      state: 'selected',
      skills: [{ id: 'a', name: 'A', source: 'agent_learned', availability: 'disabled' }]
    }
  });
  assert.equal(view.direct.skills[0].availability, 'Turned off for this agent, so it does not run');
});

test('the library impact loads only when opened, then once', async () => {
  const win = makeWindow('');
  const mod = load(win);
  const { root, body } = makeRoot();
  const urls = [];
  mod.mount({
    root,
    agentName: 'Writer',
    win,
    fetchImpl: async url => {
      urls.push(url);
      if (url.includes('include=impact')) {
        return jsonResponse({
          ...projection(),
          impact: {
            state: 'ok',
            copies: [{ workspace_id: 'a', name: 'Studio A', effect: 'independent' }]
          }
        });
      }
      return jsonResponse({ ...projection(), impact: { state: 'not_loaded', copies: [] } });
    }
  });
  await tick();
  assert.deepEqual([...urls], ['/api/agents/Writer/settings-ownership'], 'no impact read on load');
  const details = body.find(n => n.dataset?.ownershipImpact !== undefined);
  assert.ok(details, 'impact disclosure offered');
  details.open = true;
  details.fire('toggle');
  await tick();
  await tick();
  assert.equal(urls.at(-1), '/api/agents/Writer/settings-ownership?include=impact');
  assert.match(details.textContent, /Studio A/);
  details.fire('toggle');
  await tick();
  assert.equal(urls.filter(u => u.includes('include=impact')).length, 1, 'loaded once');
});
