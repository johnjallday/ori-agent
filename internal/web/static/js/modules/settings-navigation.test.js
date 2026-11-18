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
    setAttribute: (name, value) => (attrs[name] = value),
    removeAttribute: name => delete attrs[name],
    focus: options => (attrs.focused = options),
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
function loadSettings({ narrow = false, hash = '', reduced = true, fontsReady } = {}) {
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
  apiKeysSection.id = 'api-keys';
  apiKeysSection.getBoundingClientRect = () => ({ top: 600 });
  const heading = fakeElement();
  apiKeysSection.querySelector = () => heading;
  const noResults = fakeElement();
  const disclosure = fakeElement();
  const content = fakeElement();
  content.getBoundingClientRect = () => ({ top: 100 });
  content.scrollTop = 200;
  const contentScrolls = [];
  content.scrollTo = options => contentScrolls.push(options);
  const media = { matches: narrow, addEventListener: (_, fn) => (media.change = fn) };
  const windowScrolls = [];
  const pushes = [];
  const windowListeners = {};
  const layoutObservers = [];

  const document = {
    readyState: 'complete',
    documentElement: {},
    fonts: fontsReady ? { ready: fontsReady } : undefined,
    getElementById: id =>
      ({
        settingsSearch: search,
        settingsNoResults: noResults,
        settingsNavDisclosure: disclosure,
        'api-keys': apiKeysSection
      })[id] || null,
    querySelectorAll: selector =>
      ({
        '.settings-nav-item': items,
        '.settings-nav-group': [accountGroup, hubGroup],
        '.settings-section': [apiKeysSection]
      })[selector] || [],
    querySelector: () => content,
    addEventListener() {}
  };
  const timers = [];
  const sandbox = {
    document,
    window: {
      location: { hash },
      scrollY: 120,
      scrollTo: options => windowScrolls.push(options),
      addEventListener: (type, fn) => (windowListeners[type] = fn),
      removeEventListener: (type, fn) => {
        if (windowListeners[type] === fn) delete windowListeners[type];
      },
      matchMedia: query => (query.includes('max-width') ? media : { matches: reduced })
    },
    getComputedStyle: () => ({ getPropertyValue: () => '76px' }),
    history: {
      pushState: (_, __, hash) => {
        pushes.push(hash);
        sandbox.window.location.hash = hash;
      }
    },
    console: { warn() {} },
    setTimeout: fn => timers.push(fn),
    clearTimeout() {},
    requestAnimationFrame: fn => fn(),
    ResizeObserver: class {
      constructor(callback) {
        this.callback = callback;
        this.disconnected = false;
        layoutObservers.push(this);
      }
      observe() {}
      disconnect() {
        this.disconnected = true;
      }
    },
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
  return {
    type,
    sectionItem,
    skillsItem,
    pluginsItem,
    hubGroup,
    accountGroup,
    noResults,
    disclosure,
    media,
    heading,
    apiKeysSection,
    contentScrolls,
    windowScrolls,
    pushes,
    search,
    window: sandbox.window,
    hashchange: () => windowListeners.hashchange(),
    fireWindow: type => windowListeners[type]?.({}),
    resize: () =>
      layoutObservers
        .filter(observer => !observer.disconnected)
        .forEach(observer => observer.callback()),
    flush: () => timers.splice(0).forEach(fn => fn())
  };
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
  assert.equal(nav.sectionItem.attrs['aria-current'], 'location');
  assert.equal(nav.contentScrolls[0].top, 684);
  assert.equal(nav.contentScrolls[0].behavior, 'auto');
  assert.equal(nav.windowScrolls.length, 0);
});

test('mobile search opens native sections; selecting closes them and focuses the destination', () => {
  const nav = loadSettings({ narrow: true });
  assert.equal(nav.disclosure.open, false);
  nav.type('provider');
  assert.equal(nav.disclosure.open, true);
  nav.sectionItem.fire('click', { preventDefault() {} });
  assert.equal(nav.disclosure.open, false);
  assert.equal(nav.heading.attrs.tabindex, '-1');
  assert.equal(nav.heading.attrs.focused.preventScroll, true);
  assert.equal(nav.windowScrolls[0].top, 628);
  assert.equal(nav.contentScrolls.length, 0);
  assert.deepEqual(nav.pushes, ['#api-keys']);
});

test('resizing to desktop restores sections even after a mobile disclosure was closed', () => {
  const nav = loadSettings({ narrow: true });
  nav.media.matches = false;
  nav.media.change();
  assert.equal(nav.disclosure.open, true);
});

test('deep links and history navigation scroll without appending entries or stealing focus', () => {
  const nav = loadSettings({ narrow: true, hash: '#api-keys', reduced: false });
  nav.flush();
  nav.hashchange();
  assert.equal(nav.windowScrolls.length, 2);
  assert.equal(nav.windowScrolls[0].behavior, 'smooth');
  assert.deepEqual(nav.pushes, []);
  assert.equal(nav.heading.attrs.focused, undefined);
});

test('initial deep links measure after font settlement instead of a fixed timer', () => {
  let settled;
  const nav = loadSettings({
    narrow: true,
    hash: '#api-keys',
    fontsReady: { then: callback => (settled = callback) }
  });
  nav.flush();
  assert.equal(nav.windowScrolls.length, 0, 'a 100ms timer is not font readiness');
  nav.apiKeysSection.getBoundingClientRect = () => ({ top: 900 });
  settled();
  assert.equal(nav.windowScrolls[0].top, 928);
  assert.equal(nav.sectionItem.attrs['aria-current'], 'location');
  assert.deepEqual(nav.pushes, []);
  assert.equal(nav.heading.attrs.focused, undefined);
});

test('late initial font alignment never overrides a user interaction', () => {
  for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown', 'focusin']) {
    let settled;
    const nav = loadSettings({
      narrow: true,
      hash: '#api-keys',
      fontsReady: { then: callback => (settled = callback) }
    });
    nav.fireWindow(event);
    settled();
    assert.equal(nav.windowScrolls.length, 0, event);
  }
});

test('the initial fragment follows late section hydration until the user interacts', () => {
  let settled;
  const nav = loadSettings({
    narrow: true,
    hash: '#api-keys',
    fontsReady: { then: callback => (settled = callback) }
  });
  settled();
  nav.apiKeysSection.getBoundingClientRect = () => ({ top: 900 });
  nav.resize();
  assert.equal(nav.windowScrolls.at(-1).top, 928);
  assert.deepEqual(nav.pushes, []);
  assert.equal(nav.heading.attrs.focused, undefined);
});

test('the settled initial fragment stops following layout after user intent', () => {
  let settled;
  const nav = loadSettings({
    narrow: true,
    hash: '#api-keys',
    fontsReady: { then: callback => (settled = callback) }
  });
  settled();
  const count = nav.windowScrolls.length;
  nav.fireWindow('wheel');
  nav.apiKeysSection.getBoundingClientRect = () => ({ top: 900 });
  nav.resize();
  assert.equal(nav.windowScrolls.length, count);
});

test('late initial font alignment cannot restore a superseded fragment', () => {
  let settled;
  const nav = loadSettings({
    narrow: true,
    hash: '#api-keys',
    fontsReady: { then: callback => (settled = callback) }
  });
  nav.window.location.hash = '#another-section';
  settled();
  assert.equal(nav.windowScrolls.length, 0);
  assert.deepEqual(nav.pushes, []);
});

test('Escape clears a search without blurring its keyboard focus', () => {
  const nav = loadSettings({ narrow: true });
  nav.search.blur = () => assert.fail('search must retain focus');
  nav.type('nonexistent');
  assert.equal(nav.noResults.classList.contains('visible'), true);
  nav.search.fire('keydown', { key: 'Escape' });
  assert.equal(nav.search.value, '');
  assert.equal(nav.noResults.classList.contains('visible'), false);
});
