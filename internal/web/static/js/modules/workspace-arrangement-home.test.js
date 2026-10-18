import test from 'node:test';
import assert from 'node:assert/strict';
import { ProjectLibraryPanel } from './project-library.js';

function setup(t) {
  const old = globalThis.document;
  const elements = new Map();
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      events: {},
      textContent: '',
      isConnected: true,
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
        this.textContent = '';
      },
      setAttribute() {},
      addEventListener(name, callback) {
        this.events[name] = callback;
      }
    }),
    getElementById: id => elements.get(id) || null
  };
  for (const id of ['projectLibraryArrangements', 'projectLibraryArrangementRows'])
    elements.set(id, document.createElement('section'));
  globalThis.document = document;
  t.after(() => {
    globalThis.document = old;
  });
  return elements;
}
const refs = {
  arrangements: [
    {
      id: 'record',
      revision: 1,
      kind: 'clarification',
      catalog_current: false,
      review_available: false
    }
  ]
};
const allNodes = root => [root, ...root.children.flatMap(allNodes)];

test('Home history uses only saved GETs, even when the provider is read-only', async t => {
  const elements = setup(t);
  const calls = [];
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { initialized: true, provider_read_only: true };
  panel.request = async (path, options) => {
    calls.push({ path, options });
    return refs;
  };
  await panel.renderArrangementHistory();
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], { path: '/arrangements', options: { cache: 'no-store' } });
  assert.equal(elements.get('projectLibraryArrangements').hidden, false);
  const nodes = allNodes(elements.get('projectLibraryArrangementRows'));
  assert.ok(nodes.some(node => node.textContent.includes('Saved clarification')));
  assert.ok(nodes.some(node => node.textContent.includes('Historical catalog')));
  assert.equal(nodes.filter(node => node.tag === 'button').length, 1);
  assert.ok(!nodes.some(node => /Apply|Confirm|Install|Scan/.test(node.textContent)));
  await nodes.find(node => node.tag === 'button').events.click();
  assert.equal(calls[1].path, '/arrangements/record');
  assert.equal(calls[1].options.method, undefined);
});

test('empty, unreadable, malformed and stale Home reads stay honest', async t => {
  const elements = setup(t);
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const section = elements.get('projectLibraryArrangements');
  const rows = elements.get('projectLibraryArrangementRows');
  panel.request = async () => ({ arrangements: [] });
  await panel.renderArrangementHistory();
  assert.equal(section.hidden, true);
  for (const response of [
    { arrangements: Array(33).fill(refs.arrangements[0]) },
    { arrangements: [{ ...refs.arrangements[0], review_available: true }] }
  ]) {
    panel.request = async () => response;
    await panel.renderArrangementHistory();
    assert.equal(section.hidden, false);
    assert.match(rows.children[0].textContent, /could not be loaded/);
    assert.equal(allNodes(rows).filter(node => node.tag === 'button').length, 0);
  }
  let finish;
  panel.request = () =>
    new Promise(resolve => {
      finish = resolve;
    });
  const loading = panel.renderArrangementHistory();
  panel.workspaceId = 'another-home';
  finish(refs);
  await loading;
  assert.equal(rows.children.length, 0);
});
