import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('../mcp.js', import.meta.url), 'utf8');
function harness(response = { ok: false, status: 503 }) {
  const nodes = new Map();
  const requests = [];
  const document = {
    activeElement: null,
    addEventListener() {},
    getElementById: id => node(id),
    createElement: tag => node(Symbol(tag))
  };
  function node(id) {
    if (nodes.has(id)) return nodes.get(id);
    const events = new Map();
    const element = {
      innerHTML: '',
      dataset: {},
      style: {},
      attrs: {},
      children: [],
      isConnected: true,
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      appendChild(child) {
        this.children.push(child);
      },
      querySelectorAll(selector) {
        if (selector !== '[data-mcp-action]') return [];
        if (!this.actions) {
          this.actions = Array.from(this.innerHTML.matchAll(/data-mcp-action="(\w+)"/g), match => {
            const button = node(Symbol('button'));
            button.dataset.mcpAction = match[1];
            return button;
          });
        }
        return this.actions;
      },
      querySelector: () => null,
      contains: target => target === nodes.get('inside-panel'),
      focus() {
        document.activeElement = this;
      },
      addEventListener(name, callback, options) {
        const listeners = events.get(name) || [];
        listeners.push({ callback, options });
        events.set(name, listeners);
      },
      dispatch(name) {
        for (const listener of [...(events.get(name) || [])]) {
          if (listener.options?.once)
            events.set(
              name,
              events.get(name).filter(item => item !== listener)
            );
          listener.callback();
        }
      }
    };
    nodes.set(id, element);
    return element;
  }
  const sandbox = {
    document,
    window: { addEventListener() {} },
    console: { error() {} },
    fetch: async url => {
      requests.push(url);
      return response;
    },
    bootstrap: {
      Modal: { getOrCreateInstance: modal => ({ show: () => modal.dispatch('shown.bs.modal') }) }
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      '\nglobalThis.subject = { toggleSourcesPanel, showMcpModal, loadServers, loadRegistrySources, createServerCard, setSources: sources => { registrySources = sources; } };',
    sandbox
  );
  return { subject: sandbox.subject, document, node, requests, sandbox };
}

test('Sources exposes native hidden/expanded state and restores focus when collapsing its focused region', () => {
  const { subject, node, document, requests } = harness();
  subject.setSources([{ name: 'Fictional source', is_builtin: true }]);
  subject.toggleSourcesPanel();
  assert.equal(node('sourcesPanel').hidden, false);
  assert.equal(node('mcpSourcesToggle').attrs['aria-expanded'], 'true');
  document.activeElement = node('inside-panel');
  subject.toggleSourcesPanel(false);
  assert.equal(node('sourcesPanel').hidden, true);
  assert.equal(node('mcpSourcesToggle').attrs['aria-expanded'], 'false');
  assert.equal(document.activeElement, node('mcpSourcesToggle'));
  assert.deepEqual(requests, []);
});

test('failed server reads render an error, not a false empty configuration', async () => {
  const { subject, node, requests } = harness();
  await subject.loadServers();
  assert.match(node('mcpServersList').innerHTML, /Failed to load servers/);
  assert.equal(node('emptyState').style.display, 'none');
  assert.deepEqual(requests, ['/api/mcp/servers']);
});

test('failed source reads stay an error inside the open disclosure', async () => {
  const { subject, node } = harness();
  subject.setSources([{ name: 'Previous source', is_builtin: true }]);
  subject.toggleSourcesPanel();
  await subject.loadRegistrySources();
  assert.match(node('sourcesList').innerHTML, /Could not load registry sources/);
  assert.doesNotMatch(node('sourcesList').innerHTML, /No sources configured/);
});

test('MCP dialogs restore the actual pointer invoker and focus only after showing', () => {
  const { subject, document, node } = harness();
  document.activeElement = node('unrelated');
  subject.showMcpModal('modal', node('trigger'), node('field'));
  assert.equal(document.activeElement, node('field'));
  node('modal').dispatch('hidden.bs.modal');
  assert.equal(document.activeElement, node('trigger'));
  document.activeElement = node('unrelated');
  node('modal').dispatch('hidden.bs.modal');
  assert.equal(document.activeElement, node('unrelated'));
});

test('native card actions retain exact controller arguments without executing a server', () => {
  const { subject, sandbox, requests } = harness();
  const calls = [];
  for (const name of [
    'viewServerDetails',
    'toggleServerEnabled',
    'testConnection',
    'retryConnection',
    'confirmRemoveServer'
  ]) {
    sandbox[name] = (...args) => calls.push({ name, args });
  }
  const name = "fictional server's name";
  const card = subject.createServerCard({
    name,
    command: 'fictional',
    enabled: false,
    status: 'error'
  });
  const buttons = card.querySelectorAll('[data-mcp-action]');
  for (const button of buttons) button.dispatch('click');
  assert.deepEqual(calls, [
    { name: 'viewServerDetails', args: [encodeURIComponent(name), buttons[0]] },
    { name: 'toggleServerEnabled', args: [encodeURIComponent(name), false] },
    { name: 'testConnection', args: [name] },
    { name: 'retryConnection', args: [name] },
    { name: 'confirmRemoveServer', args: [name, buttons[4]] }
  ]);
  assert.deepEqual(requests, []);
});

test('server labels are literal text, actions do not interpolate command data, and plugin removal stays unavailable', () => {
  const { subject, requests } = harness();
  const card = subject.createServerCard({
    name: '<fictional-name>',
    command: '<fictional-command>',
    args: [],
    enabled: false
  });
  assert.match(card.innerHTML, /&lt;fictional-name&gt;/);
  assert.match(card.innerHTML, /&lt;fictional-command&gt;/);
  assert.doesNotMatch(card.innerHTML, /onclick=/);
  const plugin = subject.createServerCard({
    name: 'plugin/server',
    command: 'fictional',
    enabled: false
  });
  assert.match(plugin.innerHTML, /Managed by plugin/);
  assert.doesNotMatch(plugin.innerHTML, /data-mcp-action="remove"/);
  assert.deepEqual(requests, []);
});
