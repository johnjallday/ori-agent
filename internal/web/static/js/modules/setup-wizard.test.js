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
import { readdirSync, readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./setup-wizard.js', import.meta.url), 'utf8');

const ELEMENT_IDS = [
  'setupWizardDialog',
  'setupWizardBanner',
  'setupWizardBannerState',
  'setupWizardBannerDetail',
  'setupWizardBannerAction',
  'setupWizardStatusChip',
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
  'setupWizardStepLive',
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
function load({
  status,
  routes = {},
  runtimeStatus = null,
  runtimeRoutes = {},
  pathname = '/workspaces/ws-1',
  search = '',
  currentWorkspaceId = ''
} = {}) {
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
    currentWorkspaceId,
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
      const isRuntime = url.includes('/runtime-capabilities');
      const suffix = isRuntime
        ? url.replace(/^\/api\/workspaces\/[^/]+\/runtime-capabilities/, '') || ''
        : url.replace(/^\/api\/workspaces\/[^/]+\/setup-wizard/, '') || '';
      const key = `${method} ${suffix}`;
      calls.push({ key, url, body: options?.body, runtime: isRuntime });
      const route = (isRuntime ? runtimeRoutes : routes)[key];
      const payload = typeof route === 'function' ? route(calls.length) : route;
      if (payload && payload.__error) {
        return {
          ok: false,
          json: async () => ({ code: payload.code, message: payload.message })
        };
      }
      return {
        ok: true,
        json: async () =>
          isRuntime ? { runtime: payload ?? runtimeStatus } : { setup: payload ?? status }
      };
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

test('the station chip carries the same state as the banner', async () => {
  const { api, elements } = load({
    status: status({ state: 'needs_attention', auto_open: false })
  });
  await api.init();

  assert.equal(elements.setupWizardStatusChip.hidden, false);
  assert.equal(elements.setupWizardStatusChip.textContent, 'Setup: Needs attention');
  assert.match(elements.setupWizardStatusChip.className, /is-attention/);

  // A workspace with no wizard leaves the strip alone.
  const { api: plain, elements: plainEls } = load({
    status: { workspace_id: 'ws-1', applicable: false, state: 'not_applicable' }
  });
  await plain.init();
  assert.equal(plainEls.setupWizardStatusChip.hidden, true);
});

test('runtime-aware banner and single chip distinguish File-only, Configured, Offline, Wrong project, and Connected', async () => {
  const runtimeWizard = status({
    state: 'ready',
    current_step_id: '',
    steps: [
      step({
        id: 'mode',
        kind: 'runtime_mode',
        status: 'complete',
        selected_option: 'ori_assisted'
      }),
      step({
        id: 'live-control',
        kind: 'runtime_readiness',
        runtime_requirement_key: 'reaper_live_control',
        status: 'complete'
      }),
      step({ id: 'summary', kind: 'summary', status: 'complete' })
    ]
  });
  const cases = [
    [
      {
        applicable: true,
        selected_mode_id: 'file_only',
        durable_state: 'configured',
        live_state: 'not_applicable'
      },
      'File-only'
    ],
    [
      {
        applicable: true,
        selected_mode_id: 'ori_assisted',
        durable_state: 'configured',
        live_state: 'not_checked'
      },
      'Configured'
    ],
    [
      {
        applicable: true,
        selected_mode_id: 'ori_assisted',
        durable_state: 'configured',
        live_state: 'offline'
      },
      'Configured · REAPER offline'
    ],
    [
      {
        applicable: true,
        selected_mode_id: 'ori_assisted',
        durable_state: 'configured',
        live_state: 'wrong_target'
      },
      'Wrong project'
    ],
    [
      {
        applicable: true,
        selected_mode_id: 'ori_assisted',
        durable_state: 'configured',
        live_state: 'available'
      },
      'Connected now'
    ]
  ];

  for (const [runtimeStatus, expected] of cases) {
    const { api, elements, calls } = load({ status: runtimeWizard, runtimeStatus });
    await api.init();
    assert.equal(elements.setupWizardBannerState.textContent, expected);
    assert.equal(elements.setupWizardStatusChip.textContent, `Live control: ${expected}`);
    assert.equal(
      calls.filter(call => call.runtime).length,
      1,
      'without prior verification, initial status must not make a live recheck'
    );
    assert.equal(calls.find(call => call.runtime).key, 'GET ');
  }
});

test('a previously verified assisted workspace refreshes current connectivity automatically on reload', async () => {
  const runtimeWizard = status({
    state: 'ready',
    current_step_id: '',
    steps: [
      step({ id: 'mode', kind: 'runtime_mode', status: 'complete' }),
      step({ id: 'live-control', kind: 'runtime_readiness', status: 'complete' })
    ]
  });
  const verifiedAt = '2026-08-18T20:54:05Z';
  const durable = {
    applicable: true,
    selected_mode_id: 'ori_assisted',
    durable_state: 'configured',
    live_state: 'not_checked',
    first_verified_at: verifiedAt,
    last_verified_at: verifiedAt,
    requirements: [{ key: 'reaper_live_control', first_verified_at: verifiedAt }]
  };
  const connected = {
    ...durable,
    live_state: 'available',
    requirements: [
      {
        key: 'reaper_live_control',
        durable_state: 'configured',
        live_state: 'available',
        first_verified_at: verifiedAt,
        summary: 'Local REAPER control prerequisites are configured.'
      }
    ]
  };
  const { api, elements, calls } = load({
    status: runtimeWizard,
    runtimeStatus: durable,
    runtimeRoutes: { 'POST /recheck': connected }
  });

  await api.init();

  assert.deepEqual(
    calls.filter(call => call.runtime).map(call => call.key),
    ['GET ', 'POST /recheck']
  );
  assert.equal(elements.setupWizardBannerState.textContent, 'Connected now');
  assert.equal(elements.setupWizardStatusChip.textContent, 'Live control: Connected now');
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
  assert.equal(elements.setupWizardError.focused, true, 'the actionable error receives focus');
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

test('?runtime_setup=1 opens the authoritative runtime requirement step for blocked-task repairs', async () => {
  const runtimeWizard = status({
    state: 'ready',
    current_step_id: '',
    steps: [
      step({ id: 'mode', kind: 'runtime_mode', status: 'complete' }),
      step({
        id: 'live-control',
        kind: 'runtime_readiness',
        title: 'Set up local REAPER control',
        runtime_requirement_key: 'reaper_live_control',
        status: 'complete'
      }),
      step({ id: 'summary', kind: 'summary', status: 'complete' })
    ]
  });
  const { api, elements } = load({
    status: runtimeWizard,
    runtimeStatus: {
      applicable: true,
      selected_mode_id: 'ori_assisted',
      durable_state: 'configured',
      live_state: 'offline'
    },
    search: '?runtime_setup=1&action=open_check_reaper'
  });
  await api.init();
  assert.equal(elements.setupWizardDialog.showModalCalls, 1);
  assert.equal(elements.setupWizardStepTitle.textContent, 'Set up local REAPER control');
});

test('the workspace id falls back to the URL in legacy embeds', async () => {
  const { api, calls } = load({ status: status(), pathname: '/workspaces/ws-from-url' });
  await api.init();
  assert.ok(calls[0].url.startsWith('/api/workspaces/ws-from-url/setup-wizard'));
});

test('the server-resolved UUID wins over the browser slug for setup APIs', async () => {
  const { api, calls } = load({
    status: status(),
    pathname: '/workspaces/readable-slug',
    currentWorkspaceId: 'workspace-uuid'
  });
  await api.init();
  assert.ok(calls[0].url.startsWith('/api/workspaces/workspace-uuid/setup-wizard'));
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

test('a step whose own control is the action does not offer a Continue that must fail', async () => {
  const { api, elements } = load({ status: status({ state: 'in_progress' }) });
  await api.init();
  // A renderer that reports its own action is still outstanding.
  api.registerStepRenderer('directory', {
    render() {},
    primaryLabel: () => 'Choose a folder to continue',
    disablePrimary: () => true
  });
  api.open();

  assert.equal(elements.setupWizardPrimary.textContent, 'Choose a folder to continue');
  assert.equal(elements.setupWizardPrimary.disabled, true);

  // Once the renderer says its action is done, the shell's Continue works again.
  api.registerStepRenderer('directory', {
    render() {},
    primaryLabel: () => 'Continue',
    disablePrimary: () => false
  });
  api.open();
  assert.equal(elements.setupWizardPrimary.disabled, false);
});

test('a step that offers a choice renders it, and choosing is the action', async () => {
  const withOptions = status({
    state: 'in_progress',
    steps: [
      step({
        id: 'mode',
        kind: 'plugin_readiness',
        title: 'Choose how this works',
        options: [
          { id: 'file_only', label: 'File only', description: 'No plugin, no permissions.' },
          { id: 'assisted', label: 'Assisted', description: 'Installs a plugin.' }
        ]
      })
    ]
  });
  const chosen = status({
    state: 'ready',
    steps: [
      step({
        id: 'mode',
        kind: 'plugin_readiness',
        title: 'Choose how this works',
        status: 'complete',
        action: '',
        selected_option: 'file_only',
        options: [
          { id: 'file_only', label: 'File only', description: 'No plugin.', selected: true },
          { id: 'assisted', label: 'Assisted', description: 'Installs a plugin.' }
        ]
      })
    ]
  });
  let confirmed = null;
  const { api, elements, calls } = load({
    status: withOptions,
    routes: {
      'POST /steps/mode/confirm': () => {
        confirmed = calls.at(-1)?.body;
        return chosen;
      }
    }
  });
  await api.init();
  api.open();

  // Each option states its consequence next to the button that takes it.
  const rendered = elements.setupWizardStepContent.text;
  assert.match(rendered, /File only/);
  assert.match(rendered, /No plugin, no permissions\./);
  assert.match(rendered, /Assisted/);

  // Until one is chosen, Continue has nothing to do.
  assert.equal(elements.setupWizardPrimary.textContent, 'Choose an option to continue');
  assert.equal(elements.setupWizardPrimary.disabled, true);

  // Choosing sends the option id — the only value a client may send back.
  const optionButtons = elements.setupWizardStepContent.children[0].children.map(
    item => item.children[0]
  );
  await click(optionButtons[0]);
  assert.equal(confirmed, JSON.stringify({ option: 'file_only' }));

  // And the chosen one is shown as chosen rather than offered again.
  assert.match(elements.setupWizardStepContent.text, /File only \(chosen\)/);
});

test("the summary step shows the server's verdict, not just a list of ticks", async () => {
  const { api, elements } = load({
    status: status({
      state: 'in_progress',
      current_step_id: 'done',
      steps: [
        step({ id: 'folder', title: 'Choose a folder', status: 'complete' }),
        step({
          id: 'done',
          kind: 'summary',
          title: 'Ready',
          status: 'active',
          summary: 'Set up for file-only work. Ori has not checked whether REAPER is running.'
        })
      ]
    })
  });
  await api.init();
  api.open();
  const text = elements.setupWizardStepContent.text;
  // The limits of "ready" are stated where the user reads that they are ready.
  assert.match(text, /has not checked whether REAPER is running/);
  // And the per-step recap is still there.
  assert.match(text, /Choose a folder — done/);
});

test('each step says where the user is, for someone who cannot see the list', async () => {
  const two = status({
    state: 'in_progress',
    steps: [
      step({ id: 'folder', title: 'Choose a folder' }),
      step({ id: 'done', kind: 'summary', title: 'Ready', status: 'pending' })
    ]
  });
  const { api, elements } = load({ status: two });
  await api.init();
  api.open();

  // Position, name, and state — the three things the sighted user reads off the
  // step list and the progress marks.
  assert.match(elements.setupWizardStepLive.textContent, /Step 1 of 2: Choose a folder/);
  assert.match(elements.setupWizardStepLive.textContent, /current/);
  // Announced, not drawn: the visible progress line stays free for transient
  // "applying…" feedback.
  assert.equal(elements.setupWizardLive.textContent, '');

  // The same step re-rendering does not talk over the user again.
  elements.setupWizardStepLive.textContent = '';
  api.open();
  assert.equal(elements.setupWizardStepLive.textContent, '');
});

test('setup state has exactly one authoritative writer', () => {
  // Four blueprints kept their own setup cards before this feature, each with
  // its own idea of how far along the user was. The point of the shared wizard
  // is that there is now one answer, and it comes from the server. A domain
  // module that posted its own transitions would recreate the divergence.
  const dir = new URL('./', import.meta.url);
  const modules = readdirSync(dir).filter(
    name => name.endsWith('.js') && !name.endsWith('.test.js') && name !== 'setup-wizard.js'
  );
  const offenders = [];
  for (const name of modules) {
    const text = readFileSync(new URL(name, dir), 'utf8');
    // A GET of the status endpoint is fine — other surfaces show the same state.
    // Writing to it is not.
    const writes = /setup-wizard\/(open|dismiss|recheck|complete|steps)/.test(text);
    if (writes) offenders.push(name);
  }
  assert.deepEqual(offenders, [], 'these modules write setup state directly');
});

test('opening the dialog re-asks the server instead of trusting the last render', async () => {
  // Setup moves outside this dialog: a folder picked in the domain panel, a
  // permission granted in Settings, a second tab. Opening on a stale render
  // showed a step as outstanding that the user had already satisfied.
  const stale = status({
    state: 'in_progress',
    current_step_id: 'folder',
    steps: [
      step({ id: 'folder', title: 'Choose a folder', status: 'active', action: 'confirm' }),
      step({ id: 'done', kind: 'summary', title: 'Ready', status: 'pending' })
    ]
  });
  const fresh = status({
    state: 'in_progress',
    current_step_id: 'done',
    steps: [
      step({ id: 'folder', title: 'Choose a folder', status: 'complete', action: '' }),
      step({ id: 'done', kind: 'summary', title: 'Ready', status: 'active', action: 'confirm' })
    ]
  });
  const { api, elements, calls } = load({
    status: stale,
    routes: { 'POST /open': () => fresh }
  });
  await api.init();
  // Not auto-opened (auto_open defaults false in this fixture) — this is the
  // user pressing the banner's Continue setup.
  api.open();
  assert.equal(elements.setupWizardStepTitle.textContent, 'Choose a folder');

  await new Promise(resolve => setTimeout(resolve, 0));
  assert.ok(
    calls.some(call => call.key === 'POST /open'),
    'opening must record the open and re-read state'
  );
  // The step the user already satisfied is not re-asked.
  assert.equal(elements.setupWizardStepTitle.textContent, 'Ready');
});
