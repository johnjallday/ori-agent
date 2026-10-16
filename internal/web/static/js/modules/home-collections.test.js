import test from 'node:test';
import assert from 'node:assert/strict';
import { collectionCatalogPage, mountHomeCollections } from './home-collections.js';
const pin = 'a'.repeat(64);
const card = { id: 'collection-a', name: '<img onerror=alert(1)>', revision: 1, members: 1 };
const list = () => ({
  context_pin: pin,
  revision: 2,
  offset: 0,
  total: 1,
  collections: [{ ...card }]
});
const members = () => ({
  context_pin: pin,
  revision: 2,
  offset: 0,
  total: 1,
  collection: { ...card },
  members: [
    {
      id: 'entry-a',
      name: '<script>',
      format: 'reaper',
      last_observed_availability: 'revoked_source'
    }
  ]
});
function dom() {
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      events: {},
      textContent: '',
      attributes: {},
      isConnected: true,
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
        this.textContent = '';
      },
      setAttribute(k, v) {
        this.attributes[k] = v;
      },
      addEventListener(k, fn) {
        this.events[k] = fn;
      }
    })
  };
  return document.createElement('div');
}
const nodes = root => [root, ...root.children.flatMap(nodes)];
const find = (root, text) => nodes(root).find(n => n.textContent === text);
const settle = () => new Promise(resolve => setImmediate(resolve));

test('collection browsing validates exact bounded pages without granting mutation authority', () => {
  assert.ok(collectionCatalogPage(list()));
  assert.ok(collectionCatalogPage(members(), { pin, id: card.id }));
  for (const value of [
    { ...list(), total: 2 },
    { ...list(), next_offset: 1 },
    { ...list(), context_pin: 'bad' },
    { ...list(), collections: [{ ...card, members: 5001 }] }
  ])
    assert.equal(collectionCatalogPage(value), null);
  assert.equal(collectionCatalogPage(members(), { pin: 'b'.repeat(64), id: card.id }), null);
  assert.equal(
    collectionCatalogPage(
      { ...members(), collection: { ...card, members: 2 } },
      { pin, id: card.id }
    ),
    null
  );
});
test('catalog browsing uses literal names, pinned owner GETs and no activation controls', async () => {
  const root = dom(),
    calls = [];
  const api = mountHomeCollections(root, {
    isCurrent: () => true,
    request: async (path, options) => {
      calls.push([path, options]);
      return calls.length === 1 ? list() : members();
    }
  });
  assert.equal(calls.length, 0);
  await api.refresh();
  assert.ok(find(root, card.name));
  find(root, 'View members').events.click();
  await settle();
  assert.equal(calls[1][0], `/collections/collection-a?context_pin=${pin}&offset=0`);
  assert.ok(find(root, '<script>'));
  assert.ok(nodes(root).some(n => n.textContent.includes('revoked source')));
  assert.ok(!nodes(root).some(n => n.type === 'checkbox'));
  assert.ok(calls.every(([, options]) => options.cache === 'no-store' && options.method == null));
});
test('stale or failed browsing clears membership and requires explicit refresh', async () => {
  const root = dom();
  let calls = 0;
  const api = mountHomeCollections(root, {
    isCurrent: () => true,
    request: async () => {
      if (++calls === 1) return list();
      throw new Error('private path');
    }
  });
  await api.refresh();
  find(root, 'View members').events.click();
  await settle();
  assert.ok(!find(root, card.name));
  assert.ok(nodes(root).some(n => n.textContent.includes('Refresh collections to read')));
  assert.ok(!nodes(root).some(n => n.textContent.includes('private path')));
  assert.equal(calls, 2);
});
test('late replies cannot repopulate an invalidated or different Home', async () => {
  const root = dom();
  let finish,
    active = true;
  const api = mountHomeCollections(root, {
    isCurrent: () => active,
    request: () =>
      new Promise(resolve => {
        finish = resolve;
      })
  });
  const loading = api.refresh();
  api.invalidate();
  finish(list());
  await loading;
  assert.ok(!find(root, card.name));
  const again = api.refresh();
  active = false;
  finish(list());
  await again;
  assert.ok(!find(root, card.name));
});
