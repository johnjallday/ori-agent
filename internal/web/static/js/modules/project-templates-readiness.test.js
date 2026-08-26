// Tests for the blueprint picker's readiness behavior in
// project-templates-manage.js: badges on cards, the briefing panel, keyboard
// navigation, selection preservation across a reload, stale responses, and the
// guarantee that Blank can never be blocked.
//
// Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/project-templates-readiness.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag || 'div').toUpperCase();
    this.className = '';
    this.type = '';
    this.id = '';
    this.hidden = false;
    this.checked = false;
    this.value = '';
    this.tabIndex = 0;
    this.dataset = {};
    this.attributes = {};
    this.children = [];
    this.style = {};
    this._text = '';
    this._listeners = {};
    this.focused = false;
    this.classList = {
      add: (...names) => this._setClasses(names, true),
      remove: (...names) => this._setClasses(names, false),
      toggle: (name, on) => this._setClasses([name], Boolean(on)),
      contains: name => this._classes().includes(name)
    };
  }
  _classes() {
    return String(this.className).split(/\s+/).filter(Boolean);
  }
  _setClasses(names, on) {
    const set = new Set(this._classes());
    names.forEach(n => (on ? set.add(n) : set.delete(n)));
    this.className = [...set].join(' ');
  }
  get textContent() {
    return this._text + this.children.map(c => c.textContent).join('');
  }
  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }
  get innerHTML() {
    return this.textContent;
  }
  set innerHTML(value) {
    this._text = String(value);
    this.children = [];
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  append(...nodes) {
    nodes.forEach(n => this.children.push(n));
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  getAttribute(name) {
    return Object.hasOwn(this.attributes, name) ? this.attributes[name] : null;
  }
  removeAttribute(name) {
    delete this.attributes[name];
  }
  addEventListener(name, fn) {
    (this._listeners[name] = this._listeners[name] || []).push(fn);
  }
  dispatchEvent(event) {
    (this._listeners[event.type] || []).forEach(fn => fn(event));
    return true;
  }
  fire(name, event) {
    (this._listeners[name] || []).forEach(fn => fn(event || { type: name }));
  }
  focus() {
    this.focused = true;
    FakeElement.lastFocused = this;
  }
  click() {
    this.fire('click');
  }
  descendants() {
    return this.children.flatMap(c => [c, ...c.descendants()]);
  }
  matchesSelector(selector) {
    return selector
      .split(',')
      .map(part => part.trim())
      .some(part => {
        if (part.startsWith('.')) return this.classList.contains(part.slice(1));
        if (part.startsWith('[') && part.includes('data-template-id')) {
          return this.dataset.templateId === '';
        }
        return this.tagName === part.toUpperCase();
      });
  }
  querySelectorAll(selector) {
    return this.descendants().filter(node => node.matchesSelector(selector));
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
}

const ELEMENT_IDS = [
  'templatePicker',
  'templateBuiltinGrid',
  'templateUserSection',
  'templateUserList',
  'projectTemplateDescription',
  'projectTemplateEmptyHint',
  'projectTemplatePathInput',
  'projectTemplateBrowseBtn',
  'projectTemplateManageLink',
  'folderImportToggle',
  'projectTemplateOpenAfterCreate',
  'projectTemplateOpenAfterCreateToggle',
  'templateBriefingReadiness',
  'blueprintReadinessLive',
  'workspaceCreateBlueprintHeader',
  'workspaceCreateBriefingHeader',
  'templateBriefing',
  'templateBriefingDefault',
  'templateBriefingDeploys',
  'templateBriefingAgentsRow',
  'templateBriefingAgentsValue',
  'templateBriefingNoCommanderNudge',
  'templateBriefingScaffoldRow',
  'templateBriefingScaffoldValue',
  'templateBriefingAddonsRow',
  'templateBriefingAddonsList',
  'addFolderModal'
];

let elements;
let fetchQueue;
let fetchCalls;

function installDom() {
  elements = new Map();
  ELEMENT_IDS.forEach(id => {
    const el = new FakeElement('div');
    el.id = id;
    elements.set(id, el);
  });
  globalThis.document = {
    createElement: tag => new FakeElement(tag),
    getElementById: id => elements.get(id) || null,
    addEventListener: () => {},
    querySelectorAll: () => [],
    querySelector: () => null,
    body: new FakeElement('body')
  };
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = init?.detail;
    }
  };
  globalThis.Event = class {
    constructor(type) {
      this.type = type;
    }
  };
}

const READINESS_SOURCE = readFileSync(new URL('./blueprint-readiness.js', import.meta.url), 'utf8');
const PICKER_SOURCE = readFileSync(
  new URL('./project-templates-manage.js', import.meta.url),
  'utf8'
);

// A fresh vm context per test: both modules declare top-level consts, so
// re-running them in one realm would collide. Every assertion here is on a
// primitive, so the cross-realm prototypes a new context brings are harmless.
let sandbox;

function loadModules() {
  installDom();
  sandbox = {
    window: { open: () => {} },
    document: globalThis.document,
    console,
    CustomEvent: globalThis.CustomEvent,
    Event: globalThis.Event,
    Object,
    Array,
    Number,
    String,
    Boolean,
    Set,
    Map,
    Promise,
    JSON,
    Error,
    encodeURIComponent,
    requestAnimationFrame: () => {},
    fetch: async url => {
      fetchCalls.push(url);
      const next = fetchQueue.shift();
      if (!next) throw new Error(`unexpected fetch: ${url}`);
      if (next instanceof Error) throw next;
      return { ok: true, json: async () => next };
    }
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(READINESS_SOURCE, sandbox, { filename: 'blueprint-readiness.js' });
  vm.runInContext(PICKER_SOURCE, sandbox, { filename: 'project-templates-manage.js' });
  return sandbox.window.ProjectTemplateCard;
}

function template(id, overrides = {}) {
  return {
    id,
    name: id,
    builtin: true,
    has_skeleton: false,
    readiness: { state: 'ready', ownership: 'builtin', reason: '' },
    ...overrides
  };
}

function catalog(templates, root = '/tmp/templates') {
  return { templates, templates_root: root };
}

function options() {
  const grid = elements.get('templateBuiltinGrid');
  const list = elements.get('templateUserList');
  return [
    ...grid.querySelectorAll('.workspace-template-card'),
    ...list.querySelectorAll('.workspace-template-row')
  ];
}

function optionById(id) {
  return options().find(el => el.dataset.templateId === id) || null;
}

function readinessPanel() {
  return elements.get('templateBriefingReadiness').querySelector('.workspace-blueprint-readiness');
}

async function setup(templates) {
  fetchQueue = [catalog(templates)];
  fetchCalls = [];
  const Picker = loadModules();
  await Picker.populate();
  return Picker;
}

// --- card states -----------------------------------------------------------

test('a ready blueprint card carries no badge and no description', async () => {
  await setup([template('research-project')]);
  const card = optionById('research-project');
  assert.equal(card.querySelector('.workspace-template-readiness-badge'), null);
  assert.equal(card.getAttribute('aria-describedby'), null);
  assert.equal(card.dataset.readinessState, 'ready');
});

test('a recoverable card says setup is required and describes why', async () => {
  await setup([
    template('needs-plugin', {
      readiness: {
        state: 'action_required',
        ownership: 'user',
        reason: 'plugin_install_required',
        summary: 'This blueprint needs a plugin that is not installed yet.',
        actions: ['install_plugin']
      }
    })
  ]);
  const card = optionById('needs-plugin');
  assert.match(card.textContent, /Setup required/);
  assert.equal(card.dataset.readinessState, 'action_required');
  // The why is available to a screen reader without selecting the card.
  const describedBy = card.getAttribute('aria-describedby');
  assert.ok(describedBy, 'expected an accessible description');
  const help = card.descendants().find(node => node.id === describedBy);
  assert.match(help.textContent, /not installed yet/);
});

test('an unavailable card says it cannot be used', async () => {
  await setup([
    template('retired', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        summary: 'This blueprint is no longer included in Ori.',
        actions: ['change_blueprint']
      }
    })
  ]);
  const card = optionById('retired');
  assert.match(card.textContent, /Unavailable/);
  assert.equal(card.dataset.readinessState, 'unavailable');
});

test('user templates render as rows and carry the same badges', async () => {
  await setup([
    template('mine', {
      builtin: false,
      readiness: {
        state: 'action_required',
        ownership: 'user',
        reason: 'plugin_enable_required',
        summary: 'Installed but disabled.',
        actions: ['enable_plugin']
      }
    })
  ]);
  const row = elements.get('templateUserList').querySelector('.workspace-template-row');
  assert.ok(row, 'expected a user-template row');
  assert.match(row.textContent, /Setup required/);
  assert.equal(elements.get('templateUserSection').hidden, false);
});

// --- the briefing ----------------------------------------------------------

test('selecting a blocked blueprint renders its state in the briefing', async () => {
  await setup([
    template('needs-plugin', {
      readiness: {
        state: 'action_required',
        ownership: 'user',
        reason: 'plugin_install_required',
        summary: 'This blueprint needs a plugin that is not installed yet.',
        detail: 'Installing it shows what it asks for first.',
        dependency: { plugin_name: 'owner-plugin', installed: false, source_declared: true },
        actions: ['install_plugin', 'manage_plugins']
      }
    })
  ]);
  optionById('needs-plugin').click();

  const panel = readinessPanel();
  assert.ok(panel, 'expected a readiness panel');
  assert.match(panel.textContent, /not installed yet/);
  assert.match(panel.textContent, /owner-plugin — not installed/);
});

test('selecting a ready blueprint leaves the briefing panel empty', async () => {
  await setup([
    template('research-project'),
    template('needs-plugin', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        actions: ['change_blueprint']
      }
    })
  ]);
  optionById('needs-plugin').click();
  assert.ok(readinessPanel(), 'expected a panel for the blocked blueprint');
  optionById('research-project').click();
  assert.equal(readinessPanel(), null);
});

// --- Blank is never blocked ------------------------------------------------

test('Blank stays selectable and ready even when the catalog fails to load', async () => {
  fetchQueue = [new Error('network down')];
  fetchCalls = [];
  const Picker = loadModules();
  await Picker.populate();

  assert.equal(Picker.getSelectedReadiness(), null);
  assert.equal(Picker.isSelectionBlocked(), false);
  assert.equal(elements.get('projectTemplateEmptyHint').hidden, false);
  // Blank is still the selected option.
  assert.equal(Picker.getSelectedTemplate().blank, true);
});

test('an unrelated blueprint being unavailable does not block Blank', async () => {
  const Picker = await setup([
    template('retired', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        actions: ['change_blueprint']
      }
    })
  ]);
  // Populate leaves Blank selected.
  assert.equal(Picker.isSelectionBlocked(), false);
  optionById('retired').click();
  assert.equal(Picker.isSelectionBlocked(), true);
  // Returning to Blank clears the block.
  optionById('').click();
  assert.equal(Picker.isSelectionBlocked(), false);
});

test('an ad-hoc template folder overrides readiness entirely', async () => {
  const Picker = await setup([
    template('retired', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        actions: ['change_blueprint']
      }
    })
  ]);
  optionById('retired').click();
  assert.equal(Picker.isSelectionBlocked(), true);
  // Advanced → "use any folder as a template" is its own path; a library
  // blueprint's state must not follow the user into it.
  elements.get('projectTemplatePathInput').value = '/tmp/some-folder';
  assert.equal(Picker.getSelectedReadiness(), null);
  assert.equal(Picker.isSelectionBlocked(), false);
});

test('import mode reports no readiness at all', async () => {
  const Picker = await setup([
    template('retired', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        actions: ['change_blueprint']
      }
    })
  ]);
  optionById('retired').click();
  elements.get('folderImportToggle').checked = true;
  assert.equal(Picker.getSelectedReadiness(), null);
  assert.equal(Picker.isSelectionBlocked(), false);
});

// --- navigation ------------------------------------------------------------

test('double-clicking a blocked blueprint announces instead of advancing', async () => {
  await setup([
    template('needs-plugin', {
      name: 'Needs Plugin',
      readiness: {
        state: 'action_required',
        ownership: 'user',
        reason: 'plugin_install_required',
        summary: 'This blueprint needs a plugin that is not installed yet.',
        actions: ['install_plugin']
      }
    })
  ]);
  let advanced = 0;
  elements.get('addFolderModal').addEventListener('workspace-template-advance', () => advanced++);

  optionById('needs-plugin').fire('dblclick');
  assert.equal(advanced, 0, 'the shortcut carried the user past a blocker');
  assert.match(
    elements.get('blueprintReadinessLive').textContent,
    /Cannot continue with Needs Plugin/
  );
  assert.match(elements.get('blueprintReadinessLive').textContent, /not installed yet/);
  // Focus lands on the explanation, not on the card that refused.
  assert.equal(FakeElement.lastFocused, readinessPanel());
});

test('double-clicking a ready blueprint still advances', async () => {
  await setup([template('research-project')]);
  let advanced = 0;
  elements.get('addFolderModal').addEventListener('workspace-template-advance', () => advanced++);
  optionById('research-project').fire('dblclick');
  assert.equal(advanced, 1);
  assert.equal(elements.get('blueprintReadinessLive').textContent, '');
});

test('choosing another blueprint clears a stale refusal announcement', async () => {
  await setup([
    template('research-project'),
    template('needs-plugin', {
      readiness: {
        state: 'action_required',
        ownership: 'user',
        reason: 'plugin_install_required',
        summary: 'Needs a plugin.',
        actions: ['install_plugin']
      }
    })
  ]);
  optionById('needs-plugin').fire('dblclick');
  assert.notEqual(elements.get('blueprintReadinessLive').textContent, '');
  optionById('research-project').click();
  assert.equal(elements.get('blueprintReadinessLive').textContent, '');
});

// --- keyboard --------------------------------------------------------------

test('the radiogroup has one tab stop that follows the selection', async () => {
  await setup([template('a'), template('b')]);
  const [blank, cardA, cardB] = options();
  assert.equal(blank.tabIndex, 0, 'Blank is the default tab stop');
  assert.equal(cardA.tabIndex, -1);

  cardB.click();
  assert.equal(cardB.tabIndex, 0);
  assert.equal(blank.tabIndex, -1);
  assert.equal(cardA.tabIndex, -1);
});

test('arrow keys move the selection and wrap', async () => {
  await setup([template('a'), template('b')]);
  const all = options();
  const preventDefault = () => {};

  all[0].fire('keydown', { key: 'ArrowRight', currentTarget: all[0], preventDefault });
  assert.equal(all[1].getAttribute('aria-checked'), 'true');

  all[1].fire('keydown', { key: 'ArrowLeft', currentTarget: all[1], preventDefault });
  assert.equal(all[0].getAttribute('aria-checked'), 'true');

  // Wraps from the first option backwards to the last.
  all[0].fire('keydown', { key: 'ArrowUp', currentTarget: all[0], preventDefault });
  assert.equal(all[2].getAttribute('aria-checked'), 'true');

  all[2].fire('keydown', { key: 'Home', currentTarget: all[2], preventDefault });
  assert.equal(all[0].getAttribute('aria-checked'), 'true');

  all[0].fire('keydown', { key: 'End', currentTarget: all[0], preventDefault });
  assert.equal(all[2].getAttribute('aria-checked'), 'true');
});

test('an unavailable blueprint is reachable by keyboard like any other', async () => {
  await setup([
    template('retired', {
      readiness: {
        state: 'unavailable',
        ownership: 'builtin',
        reason: 'blueprint_retired',
        summary: 'Retired.',
        actions: ['change_blueprint']
      }
    })
  ]);
  const all = options();
  all[0].fire('keydown', { key: 'ArrowRight', currentTarget: all[0], preventDefault: () => {} });
  // Selecting it explains it; it never becomes unreachable or unfocusable.
  assert.equal(all[1].getAttribute('aria-checked'), 'true');
  assert.equal(all[1].tabIndex, 0);
  assert.ok(readinessPanel());
});

// --- reloads ---------------------------------------------------------------

test('a reload preserves the selected blueprint when asked to', async () => {
  const Picker = await setup([template('a'), template('b')]);
  optionById('b').click();
  assert.equal(Picker.getSelectedTemplate().id, 'b');

  fetchQueue = [catalog([template('a'), template('b')])];
  await Picker.populate({ preserveSelection: true });
  assert.equal(Picker.getSelectedTemplate().id, 'b');
  assert.equal(optionById('b').getAttribute('aria-checked'), 'true');
});

test('a reload matches a plugin blueprint by owner, not by display text', async () => {
  const disabled = template('plugin:owner:starter', {
    name: 'Plugin starter',
    builtin: false,
    plugin_owner: { plugin_id: 'owner', blueprint_id: 'starter', blueprint_version: 1 },
    readiness: {
      state: 'action_required',
      ownership: 'plugin',
      reason: 'plugin_enable_required',
      summary: 'Installed but disabled.',
      actions: ['enable_plugin']
    }
  });
  const Picker = await setup([disabled]);
  optionById('plugin:owner:starter').click();
  assert.equal(Picker.isSelectionBlocked(), true);

  // After enabling, the same blueprint comes back renamed, at a new qualified
  // ID, and ready. Matching on either would lose the selection.
  const enabled = template('plugin:owner:starter@2', {
    name: 'Completely Different Name',
    builtin: false,
    plugin_owner: { plugin_id: 'owner', blueprint_id: 'starter', blueprint_version: 2 },
    readiness: { state: 'ready', ownership: 'plugin', reason: '' }
  });
  fetchQueue = [catalog([enabled])];
  await Picker.populate({ preserveSelection: true });

  assert.equal(Picker.getSelectedTemplate().id, 'plugin:owner:starter@2');
  assert.equal(Picker.isSelectionBlocked(), false);
});

test('a reload without preserveSelection returns to Blank', async () => {
  const Picker = await setup([template('a')]);
  optionById('a').click();
  fetchQueue = [catalog([template('a')])];
  await Picker.populate();
  assert.equal(Picker.getSelectedTemplate().blank, true);
});

test('a superseded catalog response never repaints the picker', async () => {
  const Picker = await setup([template('a')]);

  // Two loads in flight; the first resolves last. Its result is stale and must
  // be discarded, or a slow initial load would overwrite the state a completed
  // recovery just produced.
  let releaseFirst;
  const first = new Promise(resolve => {
    releaseFirst = resolve;
  });
  const responses = [first, catalog([template('b')])];
  let call = 0;
  sandbox.fetch = async () => {
    const body = responses[call++];
    return { ok: true, json: async () => await body };
  };

  const slow = Picker.populate();
  const fast = Picker.populate();
  await fast;
  releaseFirst(catalog([template('stale')]));
  await slow;

  assert.ok(optionById('b'), 'the newer catalog should be on screen');
  assert.equal(optionById('stale'), null, 'a stale response repainted the picker');
});
