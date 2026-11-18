import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(new URL('../agents-roster.js', import.meta.url), 'utf8');
function load() {
  let document;
  function node(parent = null) {
    const classes = new Set();
    const result = {
      parentElement: parent,
      children: [],
      inert: false,
      hidden: false,
      tabIndex: 0,
      disabled: false,
      attrs: {},
      dataset: {},
      style: {
        setProperty(name, value) {
          this[name] = value;
        },
        getPropertyValue(name) {
          return this[name] || '';
        }
      },
      getBoundingClientRect: () => ({ top: 250 }),
      classList: {
        toggle: (name, value) => (value ? classes.add(name) : classes.delete(name)),
        contains: name => classes.has(name)
      },
      setAttribute(name, value) {
        this.attrs[name] = value;
      },
      removeAttribute(name) {
        delete this.attrs[name];
      },
      contains(target) {
        return target === this || this.children.some(child => child.contains(target));
      },
      getClientRects: () => [{}],
      focus() {
        document.activeElement = this;
      },
      querySelectorAll() {
        return this.children;
      },
      querySelector() {
        return null;
      }
    };
    if (parent) parent.children.push(result);
    return result;
  }
  const body = node();
  const navbar = node(body),
    main = node(body),
    preInert = node(body);
  preInert.inert = true;
  const shell = node(main),
    pane = node(shell),
    backdrop = node(shell),
    inspector = node(shell);
  const first = node(inspector),
    last = node(inspector);
  const search = node(pane),
    stats = node(pane);
  const list = node(pane);
  let higherDialog = false;
  let launcher = null;
  document = {
    body,
    documentElement: body,
    getElementById: id => (id === 'personalAssistantLauncher' ? launcher : null),
    activeElement: first,
    addEventListener() {},
    contains: value => body.contains(value),
    querySelector: () => (higherDialog ? {} : null)
  };
  const window = {
    innerWidth: 390,
    innerHeight: 450,
    addEventListener() {},
    requestAnimationFrame() {
      return 1;
    }
  };
  const sandbox = {
    document,
    window,
    console,
    URLSearchParams,
    setTimeout,
    clearTimeout,
    getComputedStyle: () => ({
      getPropertyValue: key =>
        ({ '--space-4': '16px', '--navbar-offset': '76px', '--ori-launcher-clearance-y': '88px' })[
          key
        ] || ''
    })
  };
  const code = SOURCE.replace(
    /\}\)\(\);\s*$/,
    `window.collectionTest = {
    state, setElements(value) { els = value; }, syncInspectorInert,
    reflectInspectorPresentation, onInspectorKeydown, onInspectorFocus,
    positionFilters, renderStatusTiles, statTile, rememberCollectionDialog, restoreCollectionDialog, updateInspectorViewport
  }; })();`
  );
  vm.runInNewContext(code, sandbox);
  const api = window.collectionTest;
  api.setElements({
    shell,
    inspector,
    inspectorBackdrop: backdrop,
    inspectorClose: first,
    search,
    stats,
    list
  });
  api.state.inspectorOpen = true;
  return {
    api,
    document,
    window,
    navbar,
    main,
    preInert,
    pane,
    backdrop,
    inspector,
    first,
    last,
    search,
    stats,
    node,
    higher: value => (higherDialog = value),
    setLauncher: value => (launcher = value)
  };
}

test('modal sheet inerts only exterior siblings and restores pre-existing inert state', () => {
  const x = load();
  x.api.syncInspectorInert(true);
  assert.equal(x.navbar.inert, true);
  assert.equal(x.pane.inert, true);
  assert.equal(x.inspector.inert, false);
  assert.equal(x.main.inert, false);
  assert.equal(x.backdrop.inert, false, 'the click-to-close backdrop stays interactive');
  x.api.syncInspectorInert(true);
  x.api.syncInspectorInert(false);
  assert.equal(x.navbar.inert, false);
  assert.equal(x.pane.inert, false);
  assert.equal(x.preInert.inert, true);
});

test('crossing to desktop removes modal semantics without losing selected identity', () => {
  const x = load();
  x.api.state.selected = 'Lumen';
  x.api.reflectInspectorPresentation();
  assert.equal(x.inspector.attrs['aria-modal'], 'true');
  assert.equal(x.pane.inert, true);
  x.window.innerWidth = 1440;
  x.api.reflectInspectorPresentation();
  assert.equal(x.inspector.attrs['aria-modal'], undefined);
  assert.equal(x.pane.inert, false);
  assert.equal(x.api.state.selected, 'Lumen');
  x.document.activeElement = x.search;
  x.window.innerWidth = 390;
  x.api.reflectInspectorPresentation();
  assert.equal(x.document.activeElement, x.first);
  assert.equal(x.pane.inert, true);
});

test('desktop inspector uses its actual top and launcher clearance, not a guessed header height', () => {
  const x = load();
  x.window.innerWidth = 1440;
  x.window.innerHeight = 900;
  x.api.updateInspectorViewport();
  assert.equal(x.inspector.style.getPropertyValue('--inspector-available-height'), '544px');
  x.inspector.getBoundingClientRect = () => ({ top: -20 });
  x.api.updateInspectorViewport();
  assert.equal(x.inspector.style.getPropertyValue('--inspector-available-height'), '702px');
  x.window.innerWidth = 768;
  x.api.updateInspectorViewport();
  assert.equal(
    x.inspector.style.getPropertyValue('--inspector-available-height'),
    '702px',
    'sheet CSS owns its full viewport'
  );
});

test('a larger loaded assistant portrait raises both the desktop pane and bulk-bar floor', () => {
  const x = load();
  x.window.innerWidth = 1440;
  x.window.innerHeight = 900;
  const launcher = x.node(x.document.body);
  launcher.getBoundingClientRect = () => ({ top: 760 });
  x.setLauncher(launcher);
  x.api.updateInspectorViewport();
  assert.equal(x.inspector.style.getPropertyValue('--inspector-available-height'), '492px');
  assert.equal(
    x.inspector.parentElement.parentElement.style.getPropertyValue(
      '--collection-launcher-clearance'
    ),
    '140px'
  );
});

test('sheet Tab wraps both directions and ignores hidden/disabled controls', () => {
  const x = load();
  const hidden = x.node(x.inspector);
  hidden.getClientRects = () => [];
  const disabled = x.node(x.inspector);
  disabled.disabled = true;
  let prevented = 0;
  x.api.onInspectorKeydown({
    key: 'Tab',
    shiftKey: true,
    preventDefault() {
      prevented++;
    }
  });
  assert.equal(x.document.activeElement, x.last);
  x.api.onInspectorKeydown({
    key: 'Tab',
    shiftKey: false,
    preventDefault() {
      prevented++;
    }
  });
  assert.equal(x.document.activeElement, x.first);
  assert.equal(prevented, 2);
});

test('a higher native or Bootstrap dialog retains its keyboard/focus authority', () => {
  const x = load();
  x.higher(true);
  x.document.activeElement = x.search;
  x.api.onInspectorFocus({ target: x.search });
  x.api.onInspectorKeydown({
    key: 'Escape',
    preventDefault() {
      assert.fail('higher dialog owns Escape');
    }
  });
  assert.equal(x.document.activeElement, x.search);
  assert.equal(x.api.state.inspectorOpen, true);
  x.higher(false);
  x.api.onInspectorFocus({ target: x.search });
  assert.equal(x.document.activeElement, x.first);
});

test('filter popover is clamped vertically as well as horizontally at short viewports', () => {
  const x = load();
  const panel = { style: {}, offsetWidth: 460, offsetHeight: 300 };
  x.window.innerWidth = 720;
  x.api.setElements({
    filtersPanel: panel,
    filtersButton: { getBoundingClientRect: () => ({ right: 700, bottom: 430 }) }
  });
  x.api.positionFilters();
  assert.equal(panel.style.top, '138px');
  assert.ok(Number.parseInt(panel.style.left) + 460 <= 708);
  x.window.innerWidth = 320;
  x.api.positionFilters();
  assert.equal(panel.style.top, '');
  assert.equal(panel.style.left, '');
});

test('native dialog cancellation restores its invoker and deletion can prefer a stable fallback', () => {
  const x = load();
  const dialog = x.node(x.main);
  x.api.rememberCollectionDialog(dialog, x.last);
  x.api.restoreCollectionDialog(dialog, x.search);
  assert.equal(x.document.activeElement, x.last);
  x.api.rememberCollectionDialog(dialog, x.last);
  x.api.restoreCollectionDialog(dialog, x.search, true);
  assert.equal(x.document.activeElement, x.search);
});

test('a missing or hidden dialog invoker never moves focus to body', () => {
  const x = load();
  const dialog = x.node(x.main);
  x.last.getClientRects = () => [];
  x.api.rememberCollectionDialog(dialog, x.last);
  x.api.restoreCollectionDialog(dialog, x.search);
  assert.equal(x.document.activeElement, x.search);
  x.api.rememberCollectionDialog(dialog, x.document.body);
  x.api.restoreCollectionDialog(dialog, x.first);
  assert.equal(x.document.activeElement, x.first);
});

test('health filter redraw restores the actual focused bucket, not body', () => {
  const x = load();
  const old = x.node(x.stats),
    fresh = x.node();
  old.dataset.health = 'needs';
  x.stats.querySelector = selector => (selector === '[data-health="needs"]' ? fresh : null);
  x.document.activeElement = old;
  x.api.state.agents = [{ status: 'error', model: 'fictional-model' }];
  x.api.renderStatusTiles();
  assert.equal(x.document.activeElement, fresh);
  assert.match(x.stats.innerHTML, /modern-btn-secondary ui-filter/);
});
