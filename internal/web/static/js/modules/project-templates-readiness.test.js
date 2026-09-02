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
  'blueprintRecoveryPanel',
  'addFolderModal'
];

let elements;
let fetchQueue;
let fetchCalls;
let documentListeners;

function installDom() {
  elements = new Map();
  ELEMENT_IDS.forEach(id => {
    const el = new FakeElement('div');
    el.id = id;
    elements.set(id, el);
  });
  documentListeners = {};
  globalThis.document = {
    createElement: tag => new FakeElement(tag),
    getElementById: id => elements.get(id) || null,
    // Recorded rather than dropped: the module wires its modal handlers from
    // DOMContentLoaded, and a stub that swallows it silently skips half the
    // behavior under test.
    addEventListener: (name, fn) => {
      (documentListeners[name] = documentListeners[name] || []).push(fn);
    },
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
const BUILDING_ART_SOURCE = readFileSync(
  new URL('./workspace-building-art.js', import.meta.url),
  'utf8'
);
const LIFECYCLE_SOURCE = readFileSync(new URL('./plugin-lifecycle.js', import.meta.url), 'utf8');
const PICKER_SOURCE = readFileSync(
  new URL('./project-templates-manage.js', import.meta.url),
  'utf8'
);

// A fresh vm context per test: the modules declare top-level consts, so
// re-running them in one realm would collide. Every assertion here is on a
// primitive, so the cross-realm prototypes a new context brings are harmless.
let sandbox;

// Every request the picker makes, so a test can assert what was sent — which
// for recovery is the whole point: an action name and a plugin name, never a
// source.
let requests;

function loadModules() {
  installDom();
  requests = [];
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
    fetch: async (url, options) => {
      fetchCalls.push(url);
      requests.push({
        url,
        method: options?.method || 'GET',
        body: options?.body ? JSON.parse(options.body) : null
      });
      const next = fetchQueue.shift();
      if (!next) throw new Error(`unexpected fetch: ${url}`);
      if (next instanceof Error) throw next;
      const status = next.__status || 200;
      const body = next.__body === undefined ? next : next.__body;
      return {
        ok: status >= 200 && status < 300,
        status,
        json: async () => body,
        text: async () => JSON.stringify(body)
      };
    }
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(READINESS_SOURCE, sandbox, { filename: 'blueprint-readiness.js' });
  vm.runInContext(BUILDING_ART_SOURCE, sandbox, { filename: 'workspace-building-art.js' });
  vm.runInContext(LIFECYCLE_SOURCE, sandbox, { filename: 'plugin-lifecycle.js' });
  vm.runInContext(PICKER_SOURCE, sandbox, { filename: 'project-templates-manage.js' });
  // The page's own startup, so the modal's show/hidden handlers are wired the
  // way they are in the browser.
  (documentListeners.DOMContentLoaded || []).forEach(fn => fn());
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

test('a built-in blueprint card previews the same specialized building as the map', async () => {
  await setup([template('calendar-ops')]);
  const card = optionById('calendar-ops');
  const icon = card.querySelector('.workspace-template-card-icon');

  assert.ok(icon.classList.contains('has-building-art'));
  assert.match(icon.innerHTML, /data-building-variant="calendar"/);
  assert.match(icon.innerHTML, /data-building-emblem="calendar"/);
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

// --- in-wizard recovery ----------------------------------------------------

// A recovery step chains several promises (fetch → text → parse → state →
// render), so a fixed number of microticks is fragile. Drain the queue instead.
async function flush() {
  for (let i = 0; i < 50; i++) await Promise.resolve();
}

function recoveryHost() {
  return elements.get('blueprintRecoveryPanel');
}

function recoveryButton(label) {
  return recoveryHost()
    .querySelectorAll('.workspace-blueprint-recovery-action')
    .find(node => node.textContent === label);
}

function readinessAction(name) {
  return readinessPanel()
    ?.descendants()
    .find(node => node.dataset.readinessAction === name);
}

function disabledPluginTemplate(overrides = {}) {
  return template('needs-plugin', {
    name: 'Needs Plugin',
    builtin: false,
    readiness: {
      state: 'action_required',
      ownership: 'user',
      reason: 'plugin_install_required',
      summary: 'This blueprint needs a plugin that is not installed yet.',
      dependency: { plugin_name: 'owner-plugin', installed: false, source_declared: true },
      actions: ['install_plugin', 'manage_plugins'],
      generation: 0
    },
    ...overrides
  });
}

const TRUST_REPORT = {
  MCPCommands: ['/usr/local/bin/owner --serve'],
  Skills: ['owner-setup'],
  Warnings: []
};

// The central security property: the browser names an action and a plugin.
// It never sends a source, so a tampered client cannot install from anywhere
// the selected blueprint does not already point at.
test('a recovery request carries an action and a plugin name, never a source', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];

  readinessAction('install_plugin').fire('click');
  await flush();

  const request = requests[requests.length - 1];
  assert.equal(request.method, 'POST');
  assert.match(request.url, /\/api\/project-templates\/needs-plugin\/plugin-recovery$/);
  assert.equal(request.body.action, 'install_plugin');
  assert.equal(request.body.plugin, 'owner-plugin');
  assert.equal(request.body.confirm, false, 'the first call must be a preview');
  assert.equal('source' in request.body, false, 'the client sent a source');
});

test('the trust disclosure is shown in full before anything is confirmed', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];

  readinessAction('install_plugin').fire('click');
  await flush();

  const text = recoveryHost().textContent;
  assert.match(text, /will be able to do the following/);
  assert.match(text, /\/usr\/local\/bin\/owner --serve/);
  assert.match(text, /owner-setup/);
  // Both halves of what the button does are named.
  assert.ok(recoveryButton('Install and enable'), 'no confirm control');
  assert.ok(recoveryButton('Cancel'), 'no cancel control');
  // Only one request so far: the preview. Nothing has been installed.
  assert.equal(requests.filter(r => r.url.includes('plugin-recovery')).length, 1);
});

// The catalog withholds the source; the disclosure is where it finally
// appears, above the commands it will be allowed to run.
test('the disclosure names where the plugin comes from', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT, source: 'https://example.test/owner.git' }];

  readinessAction('install_plugin').fire('click');
  await flush();

  assert.match(recoveryHost().textContent, /Installed from/);
  assert.match(recoveryHost().textContent, /https:\/\/example\.test\/owner\.git/);
});

test('a disclosure with no source simply omits the line', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];

  readinessAction('install_plugin').fire('click');
  await flush();

  assert.doesNotMatch(recoveryHost().textContent, /Installed from/);
  // The disclosure itself is still there.
  assert.match(recoveryHost().textContent, /Runs these commands/);
});

test('a completed recovery moves focus to its result', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  const ready = template('needs-plugin', {
    name: 'Needs Plugin',
    builtin: false,
    readiness: { state: 'ready', ownership: 'user', reason: '' }
  });
  fetchQueue = [
    {
      outcome: { completed: true, summary: 'Installed and enabled.' },
      blueprint_id: 'needs-plugin'
    },
    catalog([ready])
  ];
  recoveryButton('Install and enable').fire('click');
  await flush();

  // The button that was pressed is gone, so focus would otherwise fall to the
  // top of the document.
  assert.equal(FakeElement.lastFocused, recoveryHost());
});

test('cancelling returns focus to the readiness panel', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  recoveryButton('Cancel').fire('click');
  assert.equal(FakeElement.lastFocused, readinessPanel());
});

test('cancelling a disclosure sends nothing and clears the panel', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  const before = requests.length;
  recoveryButton('Cancel').fire('click');
  assert.equal(requests.length, before, 'cancelling made a request');
  assert.equal(recoveryHost().textContent, '');
});

test('confirming installs, then reports the outcome and reloads the catalog', async () => {
  const Picker = await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  const ready = template('needs-plugin', {
    name: 'Needs Plugin',
    builtin: false,
    readiness: { state: 'ready', ownership: 'user', reason: '' }
  });
  fetchQueue = [
    {
      outcome: { action: 'install_plugin', completed: true, summary: 'Installed and enabled.' },
      blueprint_id: 'needs-plugin'
    },
    catalog([ready])
  ];
  recoveryButton('Install and enable').fire('click');
  await flush();

  const applied = requests.find(r => r.body && r.body.confirm === true);
  assert.ok(applied, 'the confirmed call was never sent');
  assert.match(recoveryHost().textContent, /Installed and enabled/);
  // The catalog was re-read and the blueprint is now ready.
  assert.equal(Picker.isSelectionBlocked(), false);
  assert.equal(readinessPanel(), null, 'the blocked panel survived a successful recovery');
});

// Regression: Ready() sets no Dependency at all, so a session-recovery lookup
// keyed off the CURRENT readiness projection goes blind at exactly the moment
// recovery succeeds — the one moment Review most needs to say something. The
// lookup must read the template's own declared plugin names instead.
test('the session recovery record is still found once the blueprint reads ready', async () => {
  const Picker = await setup([disabledPluginTemplate({ tools: { plugins: ['owner-plugin'] } })]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  const ready = template('needs-plugin', {
    name: 'Needs Plugin',
    builtin: false,
    tools: { plugins: ['owner-plugin'] },
    readiness: { state: 'ready', ownership: 'user', reason: '' }
  });
  fetchQueue = [
    {
      outcome: { action: 'install_plugin', completed: true, summary: 'Installed and enabled.' },
      blueprint_id: 'needs-plugin'
    },
    catalog([ready])
  ];
  recoveryButton('Install and enable').fire('click');
  await flush();

  assert.equal(Picker.isSelectionBlocked(), false);
  const record = Picker.getSelectedSessionRecovery();
  assert.ok(record, 'the completed recovery was not found for the now-ready blueprint');
  assert.equal(record.pluginName, 'owner-plugin');
  assert.equal(record.completed, true);
});

test('getSelectedSessionRecovery is null when nothing was recovered this session', async () => {
  const Picker = await setup([template('a', { tools: { plugins: ['owner-plugin'] } })]);
  optionById('a').click();
  assert.equal(Picker.getSelectedSessionRecovery(), null);
});

// The honest-partial-outcome case: install worked, enable did not.
test('a partial outcome says installed, still disabled', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();

  const stillDisabled = disabledPluginTemplate({
    readiness: {
      state: 'action_required',
      ownership: 'plugin',
      reason: 'plugin_enable_required',
      summary: "This blueprint's plugin is installed but disabled.",
      dependency: { plugin_name: 'owner-plugin', installed: true, enabled: false },
      actions: ['enable_plugin']
    }
  });
  fetchQueue = [
    {
      outcome: {
        action: 'install_plugin',
        completed: false,
        summary: 'Installed, still disabled.',
        detail: 'The plugin is on this computer but could not be switched on.'
      },
      blueprint_id: 'needs-plugin'
    },
    catalog([stillDisabled])
  ];
  recoveryButton('Install and enable').fire('click');
  await flush();

  const host = recoveryHost();
  assert.match(host.textContent, /Installed, still disabled/);
  assert.match(host.textContent, /could not be switched on/);
  // A partial result is not styled as a success.
  const message = host.querySelectorAll('.workspace-blueprint-recovery-partial');
  assert.equal(message.length, 1, 'a partial outcome was rendered as a success');
  // And the card still offers the one remaining action.
  assert.ok(readinessAction('enable_plugin'), 'no way to finish the job');
});

test('a failed recovery says nothing changed and offers an escape route', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ __status: 409, __body: { error: 'Ori could not read this plugin.' } }];

  readinessAction('install_plugin').fire('click');
  await flush();

  const host = recoveryHost();
  assert.match(host.textContent, /could not read this plugin/);
  assert.ok(recoveryButton('Manage plugins'), 'no escape route offered');
  // Nothing to confirm: a failed preview must not leave a confirm button.
  assert.equal(recoveryButton('Install and enable'), undefined);
});

test('a pending confirmation is dropped when the blueprint changes', async () => {
  await setup([disabledPluginTemplate(), template('other', { name: 'Other' })]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();
  assert.ok(recoveryButton('Install and enable'), 'expected a pending confirmation');

  // Consent given for one blueprint must never be applied to another.
  optionById('other').click();
  assert.equal(recoveryHost().textContent, '');
});

test('a pending confirmation is dropped when the modal closes', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }];
  readinessAction('install_plugin').fire('click');
  await flush();
  assert.ok(recoveryButton('Install and enable'));

  elements.get('addFolderModal').fire('hidden.bs.modal');
  assert.equal(recoveryHost().textContent, '');
});

test('rapid repeated presses start one flow, not several', async () => {
  await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  fetchQueue = [{ trust: TRUST_REPORT }, { trust: TRUST_REPORT }, { trust: TRUST_REPORT }];

  const action = readinessAction('install_plugin');
  action.fire('click');
  action.fire('click');
  action.fire('click');
  await flush();

  // Each press invalidates the last, so only the final flow renders — and no
  // press can produce a second confirm control alongside the first.
  const confirms = recoveryHost()
    .querySelectorAll('.workspace-blueprint-recovery-action')
    .filter(node => node.textContent === 'Install and enable');
  assert.equal(confirms.length, 1, 'more than one confirmation was on screen');
});

test('a completed recovery selects the replacement the server names', async () => {
  const stale = template('song-production', {
    name: 'Song Production',
    builtin: false,
    readiness: {
      state: 'action_required',
      ownership: 'plugin',
      reason: 'plugin_enable_required',
      summary: 'Installed but disabled.',
      dependency: { plugin_name: 'owner-plugin', installed: true, enabled: false },
      actions: ['enable_plugin'],
      generation: 3
    }
  });
  const Picker = await setup([stale]);
  optionById('song-production').click();

  // Enable confirms itself: there is nothing new to disclose.
  const replacement = template('plugin:owner-plugin:song-production', {
    name: 'Renamed Entirely',
    builtin: false,
    plugin_owner: { plugin_id: 'owner-plugin', blueprint_id: 'song-production' },
    readiness: { state: 'ready', ownership: 'plugin', reason: '' }
  });
  fetchQueue = [
    { trust: null },
    {
      outcome: { action: 'enable_plugin', completed: true, summary: 'Enabled.' },
      blueprint_id: 'plugin:owner-plugin:song-production'
    },
    catalog([replacement])
  ];
  readinessAction('enable_plugin').fire('click');
  await flush();

  // Matched by the ID the server reported, not by the display text — which
  // changed completely.
  assert.equal(Picker.getSelectedTemplate().id, 'plugin:owner-plugin:song-production');
  assert.equal(Picker.isSelectionBlocked(), false);
  assert.match(recoveryHost().textContent, /Enabled/);
});

test('the confirmed request carries the generation the disclosure was read at', async () => {
  const withGeneration = disabledPluginTemplate({
    readiness: {
      state: 'action_required',
      ownership: 'plugin',
      reason: 'plugin_enable_required',
      summary: 'Installed but disabled.',
      dependency: { plugin_name: 'owner-plugin', installed: true, enabled: false },
      actions: ['enable_plugin'],
      generation: 7
    }
  });
  await setup([withGeneration]);
  optionById('needs-plugin').click();
  fetchQueue = [
    { trust: null },
    {
      outcome: { action: 'enable_plugin', completed: true, summary: 'Enabled.' },
      blueprint_id: 'needs-plugin'
    },
    catalog([withGeneration])
  ];
  readinessAction('enable_plugin').fire('click');
  await flush();

  const applied = requests.find(r => r.body && r.body.confirm === true);
  assert.ok(applied, 'nothing was confirmed');
  assert.equal(applied.body.generation, 7);
});

test('recheckSelection re-reads the catalog and reports the current state', async () => {
  const Picker = await setup([disabledPluginTemplate()]);
  optionById('needs-plugin').click();
  assert.equal(Picker.isSelectionBlocked(), true);

  const ready = template('needs-plugin', {
    name: 'Needs Plugin',
    builtin: false,
    readiness: { state: 'ready', ownership: 'user', reason: '' }
  });
  fetchQueue = [catalog([ready])];
  const current = await Picker.recheckSelection();
  assert.equal(current.state, 'ready');
  assert.equal(Picker.isSelectionBlocked(), false);
});

test('recheckSelection is a no-op for a selection with no readiness', async () => {
  const Picker = await setup([template('a')]);
  // Blank is selected; there is nothing to re-check and no request to make.
  const before = requests.length;
  assert.equal(await Picker.recheckSelection(), null);
  assert.equal(requests.length, before);
});
