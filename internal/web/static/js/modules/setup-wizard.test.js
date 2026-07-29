// Tests for setup-wizard.js — the shared blueprint Setup Wizard shell.
//
// Run with: node --test internal/web/static/js/modules/setup-wizard.test.js
//
// The module is loaded into a minimal fake DOM so the tests can assert what the
// shell does, not how the browser paints it: which requests it makes, what it
// writes as text, when the dialog opens, and — most importantly — that it never
// decides readiness for itself.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./setup-wizard.js', import.meta.url), 'utf8');

const ELEMENT_IDS = [
  'setupWizardDialog',
  'setupWizardBanner',
  'setupWizardBannerState',
  'setupWizardBannerDetail',
  'setupWizardBannerAction',
  'setupWizardIcon',
  'setupWizardBlueprint',
  'setupWizardTitle',
  'setupWizardSteps',
  'setupWizardStepTitle',
  'setupWizardStepDescription',
  'setupWizardStepContent',
  'setupWizardDisclosure',
  'setupWizardDisclosureBody',
  'setupWizardError',
  'setupWizardLive',
  'setupWizardBack',
  'setupWizardSkip',
  'setupWizardPrimary',
  'setupWizardClose'
];

function makeElement(id) {
  const element = {
    id,
    tagName: 'DIV',
    textContent: '',
    hidden: false,
    disabled: false,
    className: '',
    open: false,
    children: [],
    listeners: {},
    classList: {
      toggle(name, on) {
        element.classes = element.classes || new Set();
        if (on) element.classes.add(name);
        else element.classes.delete(name);
      }
    },
    attributes: {},
    setAttribute(name, value) {
      element.attributes[name] = value;
    },
    removeAttribute(name) {
      delete element.attributes[name];
      if (name === 'open') element.open = false;
    },
    appendChild(child) {
      element.children.push(child);
      return child;
    },
    addEventListener(type, handler) {
      element.listeners[type] = element.listeners[type] || [];
      element.listeners[type].push(handler);
    },
    focus() {
      element.focused = true;
    },
    showModal() {
      element.open = true;
      element.showModalCalls = (element.showModalCalls || 0) + 1;
    },
    close() {
      element.open = false;
      element.closeCalls = (element.closeCalls || 0) + 1;
    },
    // The shell only ever writes text; a test that reads it must flatten the
    // tree the same way a screen reader would.
    get text() {
      return (
        element.textContent +
        element.children.map(child => child.text ?? child.textContent).join(' ')
      );
    }
  };
  return element;
}

function click(element) {
  const handlers = element.listeners.click || [];
  return Promise.all(handlers.map(handler => handler({ preventDefault() {} })));
}

/**
 * Loads the module with a fake DOM and a scripted fetch.
 *
 * routes maps "METHOD /path-suffix" to a status payload (or a function of the
 * call index), so a test can make the server change its mind between calls —
 * which is the only way a step ever becomes complete.
 */
function load({ status, routes = {}, pathname = '/workspaces/ws-1', search = '' } = {}) {
  const elements = {};
  for (const id of ELEMENT_IDS) elements[id] = makeElement(id);

  const calls = [];
  const document = {
    readyState: 'complete',
    addEventListener() {},
    dispatchEvent(event) {
      calls.push({ event: event?.type });
      return true;
    },
    getElementById: id => elements[id] || null,
    createElement: tag => {
      const element = makeElement('');
      element.tagName = String(tag).toUpperCase();
      return element;
    }
  };

  const toasts = [];
  const storage = new Map();
  const window = {
    location: { pathname, search },
    sessionStorage: {
      getItem: key => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: key => storage.delete(key)
    },
    Toast: {
      info: message => toasts.push({ level: 'info', message }),
      success: message => toasts.push({ level: 'success', message })
    }
  };

  const context = {
    console,
    window,
    document,
    URLSearchParams,
    CustomEvent: class {
      constructor(type, init) {
        this.type = type;
        this.detail = init?.detail;
      }
    },
    fetch: async (url, options) => {
      const method = options?.method || 'GET';
      const suffix = url.replace(/^\/api\/workspaces\/[^/]+\/setup-wizard/, '') || '';
      const key = `${method} ${suffix}`;
      calls.push({ key, url, body: options?.body });
      const route = routes[key];
      const payload = typeof route === 'function' ? route(calls.length) : route;
      if (payload && payload.__error) {
        return {
          ok: false,
          json: async () => ({ code: payload.code, message: payload.message })
        };
      }
      return { ok: true, json: async () => ({ setup: payload ?? status }) };
    }
  };

  vm.runInNewContext(source, context, { filename: 'setup-wizard.js' });
  return { api: window.SetupWizard, elements, calls, toasts, storage, window };
}

function step(overrides = {}) {
  return {
    id: 'folder',
    kind: 'directory',
    required: true,
    title: 'Choose the folder to tidy',
    description: 'Pick one folder.',
    status: 'pending',
    action: 'confirm',
    ...overrides
  };
}

function status(overrides = {}) {
  return {
    workspace_id: 'ws-1',
    applicable: true,
    state: 'not_started',
    blueprint_id: 'demo',
    blueprint_name: 'Demo Blueprint',
    title: 'Set up Demo',
    current_step_id: 'folder',
    dismissed: false,
    auto_open: false,
    steps: [step(), step({ id: 'summary', kind: 'summary', required: false, title: 'Summary' })],
    ...overrides
  };
}

test('a workspace with no wizard shows no setup surface and opens nothing', async () => {
  const { api, elements } = load({
    status: { workspace_id: 'ws-1', applicable: false, state: 'not_applicable' }
  });
  await api.init();

  assert.equal(elements.setupWizardBanner.hidden, true);
  assert.equal(elements.setupWizardDialog.showModalCalls, undefined);
});

test('a never-opened wizard opens itself once and records the open', async () => {
  const { api, elements, calls } = load({
    status: status({ auto_open: true }),
    routes: { 'POST /open': status({ auto_open: false }) }
  });
  await api.init();

  assert.equal(elements.setupWizardDialog.showModalCalls, 1);
  assert.ok(
    calls.some(call => call.key === 'POST /open'),
    'the one auto-open is spent server-side, not in the tab'
  );
  // A second init must not re-open it: init is idempotent.
  await api.init();
  assert.equal(elements.setupWizardDialog.showModalCalls, 1);
});

test('a dismissed wizard does not auto-open, but its banner stays', async () => {
  const { api, elements } = load({
    status: status({ state: 'in_progress', dismissed: true, auto_open: false })
  });
  await api.init();

  assert.equal(elements.setupWizardDialog.showModalCalls, undefined);
  assert.equal(elements.setupWizardBanner.hidden, false);
  assert.equal(elements.setupWizardBannerState.textContent, 'Setup required');
  assert.equal(elements.setupWizardBannerAction.textContent, 'Continue setup');
});

test('a regressed workspace invites repair without ambushing the user', async () => {
  const { api, elements } = load({
    status: status({
      state: 'needs_attention',
      auto_open: false,
      completed_at: '2026-07-01T10:00:00Z'
    })
  });
  await api.init();

  assert.equal(elements.setupWizardDialog.showModalCalls, undefined);
  assert.equal(elements.setupWizardBannerState.textContent, 'Needs attention');
  assert.equal(elements.setupWizardBannerAction.textContent, 'Repair setup');
});

test('author text is rendered as text, never as markup', async () => {
  const hostile = '<img src=x onerror=alert(1)>';
  const { api, elements } = load({
    status: status({
      title: hostile,
      steps: [step({ title: hostile, description: hostile, disclosure: hostile })]
    })
  });
  await api.init();
  api.open();

  // textContent, not innerHTML: the string survives verbatim and nothing in the
  // fake DOM was ever asked to parse it.
  assert.equal(elements.setupWizardTitle.textContent, hostile);
  assert.equal(elements.setupWizardStepTitle.textContent, hostile);
  assert.equal(elements.setupWizardDisclosureBody.textContent, hostile);
  assert.equal(elements.setupWizardDisclosure.hidden, false);
  assert.equal('innerHTML' in elements.setupWizardStepContent, false);
});

test('progress carries a mark and a word for every step, not just a color', async () => {
  const { api, elements } = load({
    status: status({
      current_step_id: 'summary',
      steps: [
        step({ status: 'complete' }),
        step({ id: 'blocked-step', title: 'Connect', status: 'blocked' }),
        step({ id: 'skipped', title: 'Extras', required: false, status: 'optional_skipped' }),
        step({ id: 'summary', kind: 'summary', title: 'Summary', status: 'pending' })
      ]
    })
  });
  await api.init();
  api.open();

  const rendered = elements.setupWizardSteps.children.map(child => child.text);
  assert.match(rendered[0], /1\. Choose the folder to tidy \(done\)/);
  assert.match(rendered[0], /✓/);
  assert.match(rendered[1], /2\. Connect \(needs attention\)/);
  assert.match(rendered[2], /3\. Extras \(skipped\)/);
  assert.match(rendered[3], /4\. Summary \(current\)/);
});

test('the primary control does what the server says the step needs', async () => {
  const { api, elements } = load({
    status: status({
      current_step_id: 'readiness',
      steps: [step({ id: 'readiness', kind: 'readiness', title: 'Readiness', action: 'recheck' })]
    })
  });
  await api.init();
  api.open();
  assert.equal(elements.setupWizardPrimary.textContent, 'Check again');
});

test('confirming a step posts it and advances only when the server agrees', async () => {
  const pending = status({ state: 'in_progress' });
  const confirmed = status({
    state: 'in_progress',
    current_step_id: 'summary',
    steps: [
      step({ status: 'complete', action: '' }),
      step({ id: 'summary', kind: 'summary', required: false, title: 'Summary' })
    ]
  });
  let confirmedYet = false;
  const { api, elements, calls } = load({
    status: pending,
    routes: {
      'POST /steps/folder/confirm': () => {
        confirmedYet = true;
        return confirmed;
      },
      GET: () => (confirmedYet ? confirmed : pending)
    }
  });
  await api.init();
  api.open();

  assert.equal(elements.setupWizardStepTitle.textContent, 'Choose the folder to tidy');
  await click(elements.setupWizardPrimary);

  assert.ok(calls.some(call => call.key === 'POST /steps/folder/confirm'));
  assert.equal(elements.setupWizardStepTitle.textContent, 'Summary');
});

test('a step the server did not confirm keeps the wizard where it is', async () => {
  const blocked = status({
    state: 'in_progress',
    steps: [
      step({ status: 'blocked', summary: 'Ori could not read that folder.' }),
      step({ id: 'summary', kind: 'summary', required: false, title: 'Summary' })
    ]
  });
  const { api, elements } = load({
    status: status({ state: 'in_progress' }),
    routes: { 'POST /steps/folder/confirm': blocked }
  });
  await api.init();
  api.open();
  await click(elements.setupWizardPrimary);

  assert.equal(elements.setupWizardStepTitle.textContent, 'Choose the folder to tidy');
  assert.equal(elements.setupWizardPrimary.textContent, 'Try again');
});

test('an expected failure is explained in plain language with a stable category', async () => {
  const { api, elements } = load({
    status: status({ state: 'in_progress' }),
    routes: {
      'POST /steps/folder/confirm': { __error: true, code: 'adapter_unavailable', message: 'nope' }
    }
  });
  await api.init();
  api.open();
  await click(elements.setupWizardPrimary);

  assert.equal(elements.setupWizardError.hidden, false);
  assert.match(elements.setupWizardError.textContent, /unavailable in this build/);
  // The step is unchanged: a failed action must not look like progress.
  assert.equal(elements.setupWizardStepTitle.textContent, 'Choose the folder to tidy');
});

test('closing an unfinished wizard records the dismissal and says where to resume', async () => {
  const { api, elements, calls, toasts } = load({
    status: status({ state: 'in_progress' }),
    routes: { 'POST /dismiss': status({ state: 'in_progress', dismissed: true }) }
  });
  await api.init();
  api.open();
  await click(elements.setupWizardClose);

  assert.equal(elements.setupWizardDialog.closeCalls, 1);
  assert.ok(calls.some(call => call.key === 'POST /dismiss'));
  assert.match(toasts.at(-1).message, /not finished/);
  // Dismissal never claims readiness.
  assert.equal(elements.setupWizardBannerState.textContent, 'Setup required');
});

test('a finished wizard closes without a dismissal', async () => {
  const { api, elements, calls } = load({ status: status({ state: 'ready', auto_open: false }) });
  await api.init();
  api.open();
  await click(elements.setupWizardClose);

  assert.equal(elements.setupWizardDialog.closeCalls, 1);
  assert.equal(
    calls.some(call => call.key === 'POST /dismiss'),
    false,
    'a completed wizard has nothing to dismiss'
  );
  assert.equal(elements.setupWizardBannerState.textContent, 'Ready');
});

test('only an unresolved optional step offers Skip', async () => {
  const { api, elements } = load({
    status: status({
      current_step_id: 'extras',
      steps: [step({ id: 'extras', kind: 'summary', required: false, title: 'Extras' })]
    })
  });
  await api.init();
  api.open();
  assert.equal(elements.setupWizardSkip.hidden, false);

  const { api: required, elements: requiredEls } = load({ status: status() });
  await required.init();
  required.open();
  assert.equal(requiredEls.setupWizardSkip.hidden, true);
});

test('returning from an external authorization resumes the same step', async () => {
  const { api, elements, storage } = load({
    status: status({
      state: 'in_progress',
      current_step_id: 'folder',
      steps: [
        step({ status: 'complete' }),
        step({ id: 'connect', kind: 'capability_connect', title: 'Connect the calendar' })
      ]
    })
  });
  storage.set('oriSetupWizardResume:ws-1', 'connect');
  await api.init();

  assert.equal(elements.setupWizardDialog.showModalCalls, 1);
  assert.equal(elements.setupWizardStepTitle.textContent, 'Connect the calendar');
  assert.equal(
    storage.has('oriSetupWizardResume:ws-1'),
    false,
    'the resume point is consumed once'
  );
});

test('?setup=1 opens the wizard for an entry point on another page', async () => {
  const { api, elements } = load({
    status: status({ state: 'in_progress', auto_open: false }),
    search: '?setup=1'
  });
  await api.init();
  assert.equal(elements.setupWizardDialog.showModalCalls, 1);
});

test('the workspace id comes from the URL, not a global set later', async () => {
  const { api, calls } = load({ status: status(), pathname: '/workspaces/ws-from-url' });
  await api.init();
  assert.ok(calls[0].url.startsWith('/api/workspaces/ws-from-url/setup-wizard'));
});

test('banner wording matches the three states a user can be in', () => {
  const { api } = load({ status: status() });
  const { bannerPresentation } = api._internals;
  assert.equal(bannerPresentation({ state: 'ready' }).state, 'Ready');
  assert.equal(bannerPresentation({ state: 'needs_attention' }).action, 'Repair setup');
  assert.equal(bannerPresentation({ state: 'in_progress' }).state, 'Setup required');
  assert.equal(bannerPresentation({ state: 'not_started' }).action, 'Continue setup');
});

test('stable error codes map to actionable sentences', () => {
  const { api } = load({ status: status() });
  const { friendlyError } = api._internals;
  assert.match(
    friendlyError({ code: 'unsupported_setup_wizard' }),
    /cannot be run by this version/
  );
  assert.match(friendlyError({ code: 'unknown_step' }), /no longer part of/);
  assert.match(friendlyError({ message: 'boom' }), /boom/);
});
