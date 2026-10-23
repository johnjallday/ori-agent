// Tests for the Settings left navigation: search and the full-page links that
// make Settings the hub for the configuration pages (nav-collapse FR16, FR17).
// Run with:
//   node --test internal/web/static/js/modules/settings-navigation.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('./settings-navigation.js', import.meta.url), 'utf8');

function fakeClassList() {
  const names = new Set();
  return {
    add: name => names.add(name),
    remove: name => names.delete(name),
    toggle: (name, force) => (force ? names.add(name) : names.delete(name)),
    contains: name => names.has(name)
  };
}

function fakeElement(attrs = {}) {
  const listeners = {};
  return {
    attrs,
    dataset: attrs.dataset || {},
    classList: fakeClassList(),
    textContent: attrs.text || '',
    getAttribute: name => (name in attrs ? attrs[name] : null),
    addEventListener: (type, fn) => {
      listeners[type] = fn;
    },
    querySelector: () => ({ textContent: attrs.text || '' }),
    fire(type, event) {
      listeners[type].call(this, event);
    },
    getBoundingClientRect: () => ({ top: 0 })
  };
}

// A Settings page with one in-page section item and one hub item per page.
function loadSettings() {
  const sectionItem = fakeElement({ 'data-section': 'api-keys', text: 'API Keys' });
  const skillsItem = fakeElement({ text: 'Skills', dataset: { externalLink: 'true' } });
  const pluginsItem = fakeElement({ text: 'Plugins', dataset: { externalLink: 'true' } });
  const items = [sectionItem, skillsItem, pluginsItem];
  const accountGroup = fakeElement();
  const hubGroup = fakeElement();
  accountGroup.querySelectorAll = () =>
    items.slice(0, 1).filter(i => !i.classList.contains('hidden'));
  hubGroup.querySelectorAll = () => items.slice(1).filter(i => !i.classList.contains('hidden'));
  const search = fakeElement();
  const apiKeysSection = fakeElement({ text: 'Provider keys for OpenAI and Anthropic' });
  const noResults = fakeElement();

  const document = {
    readyState: 'complete',
    getElementById: id =>
      ({ settingsSearch: search, settingsNoResults: noResults, 'api-keys': apiKeysSection })[id] ||
      null,
    querySelectorAll: selector =>
      ({
        '.settings-nav-item': items,
        '.settings-nav-group': [accountGroup, hubGroup],
        '.settings-section': [apiKeysSection]
      })[selector] || [],
    querySelector: () => null,
    addEventListener() {}
  };
  const timers = [];
  const sandbox = {
    document,
    window: { location: { hash: '' }, addEventListener() {} },
    history: { pushState() {} },
    console: { warn() {} },
    setTimeout: fn => timers.push(fn),
    clearTimeout() {},
    requestAnimationFrame: fn => fn(),
    Array
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'settings-navigation.js' });

  const type = value => {
    search.fire('input', undefined);
    search.value = value;
    // The debounce reads this.value when its timer fires.
    timers.splice(0).forEach(fn => fn.call(search));
  };
  search.value = '';
  return { type, sectionItem, skillsItem, pluginsItem, hubGroup, accountGroup, noResults };
}

test('search finds a hub page by its label and hides the groups without a match', () => {
  const nav = loadSettings();
  nav.type('skills');
  assert.equal(nav.skillsItem.classList.contains('hidden'), false);
  assert.equal(nav.pluginsItem.classList.contains('hidden'), true);
  assert.equal(nav.sectionItem.classList.contains('hidden'), true);
  assert.equal(nav.hubGroup.classList.contains('hidden'), false);
  assert.equal(nav.accountGroup.classList.contains('hidden'), true);
  assert.equal(nav.noResults.classList.contains('visible'), false);
});

test('clearing the search shows every item again', () => {
  const nav = loadSettings();
  nav.type('skills');
  nav.type('');
  for (const item of [nav.sectionItem, nav.skillsItem, nav.pluginsItem]) {
    assert.equal(item.classList.contains('hidden'), false);
  }
});

test('a hub item click is left to the browser, so it opens the page', () => {
  const nav = loadSettings();
  let prevented = false;
  nav.skillsItem.fire('click', { preventDefault: () => (prevented = true) });
  assert.equal(prevented, false);
});

test('an in-page section click is still handled inside Settings', () => {
  const nav = loadSettings();
  let prevented = false;
  nav.sectionItem.fire('click', { preventDefault: () => (prevented = true) });
  assert.equal(prevented, true);
});
