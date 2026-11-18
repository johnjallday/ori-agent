// Tag filtering and focus-preserving native control behavior.

import { test } from 'node:test';
import assert from 'node:assert/strict';

const { itemTags, collectTags, matchesActiveTags, filterItems, createTagFilterBar } =
  await import('./tag-filter-bar.js');

const items = [
  { id: 'a', tags: ['music', 'client'] },
  { id: 'b', tags: ['music'] },
  { id: 'c', tags: [] },
  { id: 'd' },
  { id: 'e', tags: [' archive '] }
];

test('itemTags trims and drops empty entries', () => {
  assert.deepEqual(itemTags({ tags: [' music ', '', null] }), ['music']);
  assert.deepEqual(itemTags(null), []);
  assert.deepEqual(itemTags({}), []);
});

test('collectTags returns sorted unique tags across items', () => {
  assert.deepEqual(collectTags(items), ['archive', 'client', 'music']);
});

test('matchesActiveTags uses AND logic', () => {
  assert.equal(matchesActiveTags(['music', 'client'], ['music']), true);
  assert.equal(matchesActiveTags(['music', 'client'], ['music', 'client']), true);
  assert.equal(matchesActiveTags(['music'], ['music', 'client']), false);
  assert.equal(matchesActiveTags([], []), true);
});

test('filterItems with no active tags returns all items', () => {
  assert.equal(filterItems(items, []).length, items.length);
  assert.equal(filterItems(items, new Set()).length, items.length);
});

test('filterItems narrows to items carrying every active tag', () => {
  const one = filterItems(items, ['music']);
  assert.deepEqual(
    one.map(item => item.id),
    ['a', 'b']
  );

  const both = filterItems(items, new Set(['music', 'client']));
  assert.deepEqual(
    both.map(item => item.id),
    ['a']
  );
});

test('filterItems supports a custom tag getter', () => {
  const rows = [
    { name: 'x', labels: ['alpha'] },
    { name: 'y', labels: ['beta'] }
  ];
  const filtered = filterItems(rows, ['beta'], row => row.labels || []);
  assert.deepEqual(
    filtered.map(row => row.name),
    ['y']
  );
});

test('toggling keeps the focused chip alive; Clear restores focus before hiding itself', t => {
  const original = globalThis.document;
  t.after(() => {
    globalThis.document = original;
  });
  function node() {
    const events = {};
    const classes = new Set();
    return {
      children: [],
      attrs: {},
      hidden: false,
      classList: {
        add: name => classes.add(name),
        toggle: (name, on) => (on ? classes.add(name) : classes.delete(name))
      },
      set innerHTML(_value) {
        this.children = [];
      },
      get firstElementChild() {
        return this.children[0];
      },
      appendChild(child) {
        this.children.push(child);
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      addEventListener(name, fn) {
        events[name] = fn;
      },
      click() {
        events.click();
      },
      focus() {
        globalThis.document.activeElement = this;
      }
    };
  }
  globalThis.document = { createElement: node, activeElement: null };
  const changes = [];
  const bar = createTagFilterBar({ container: node(), onChange: active => changes.push(active) });
  bar.setAvailableTags(['notes', 'research']);
  const chips = bar.element.children[1];
  const clear = bar.element.children[2];
  const chip = chips.children[0];
  chip.focus();
  chip.click();
  assert.equal(chips.children[0], chip);
  assert.equal(globalThis.document.activeElement, chip);
  assert.equal(chip.attrs['aria-pressed'], 'true');
  clear.focus();
  clear.click();
  assert.equal(globalThis.document.activeElement, chip);
  assert.equal(clear.hidden, true);
  assert.equal(chip.attrs['aria-pressed'], 'false');
  assert.deepEqual(changes, [['notes'], []]);
});
