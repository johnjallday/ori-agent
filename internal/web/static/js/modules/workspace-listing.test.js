import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function loadModule() {
  const listeners = new Map();
  const context = {
    console,
    escapeHtml,
    window: {
      addEventListener: () => {},
      location: { href: '' }
    },
    document: {
      addEventListener: (type, handler) => listeners.set(type, handler),
      getElementById: () => null,
      createElement: () => ({ style: {}, innerHTML: '', appendChild: () => {}, remove: () => {} }),
      head: { appendChild: () => {} },
      body: { appendChild: () => {} }
    },
    fetch: async () => ({ json: async () => ({}) }),
    alert: () => {},
    confirm: () => false,
    setInterval: () => 1,
    clearInterval: () => {},
    setTimeout: () => 1,
    clearTimeout: () => {},
    AbortController: class {
      constructor() {
        this.signal = {};
      }
      abort() {}
    }
  };
  context.globalThis = context;
  vm.createContext(context);
  const source = readFileSync(new URL('./workspace-listing.js', import.meta.url), 'utf8');
  vm.runInContext(source, context, { filename: 'workspace-listing.js' });
  return context.window.WorkspaceListing.__test;
}

test('workspace listing renders read-only escaped tag chips', () => {
  const { renderWorkspaceCard } = loadModule();

  const markup = renderWorkspaceCard({
    id: 'workspace-1',
    name: 'Tagged',
    status: 'active',
    description: 'Has tags',
    tags: ['music', '<script>', 'Client & Research'],
    task_count: 1,
    session_count: 2,
    note_count: 3
  });

  assert.match(markup, /workspace-card-tags/);
  assert.match(markup, /title="music">music<\/span>/);
  assert.match(markup, /title="&lt;script&gt;">&lt;script&gt;<\/span>/);
  assert.match(markup, /title="Client &amp; Research">Client &amp; Research<\/span>/);
});

test('workspace listing omits tag row when there are no tags', () => {
  const { renderWorkspaceCard } = loadModule();

  const markup = renderWorkspaceCard({
    id: 'workspace-2',
    name: 'Untagged',
    status: 'active',
    tags: ['   ', null]
  });

  assert.doesNotMatch(markup, /workspace-card-tags/);
});
