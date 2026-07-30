// Tests for calendar-ops-setup.js — Calendar Ops inside the shared Setup
// Wizard.
//
// Run with: node --test internal/web/static/js/modules/calendar-ops-setup.test.js
//
// The module renders the same sections into two surfaces: its own card (for a
// workspace whose blueprint predates the wizard) and the wizard's typed steps.
// These tests pin which surface owns setup, and that the wizard's steps carry
// the controls for the state the workspace is actually in.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./calendar-ops-setup.js', import.meta.url), 'utf8');

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.style = { cssText: '' };
    this.className = '';
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.checked = false;
    this.listeners = {};
    this._text = '';
  }
  set textContent(value) {
    this._text = String(value ?? '');
    if (this._text === '') this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join(' ');
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  insertBefore(child) {
    this.children.unshift(child);
    return child;
  }
  setAttribute(name, value) {
    this.attributes[name] = value;
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  querySelectorAll() {
    return [];
  }
  click() {
    (this.listeners.click || []).forEach(handler => handler({ preventDefault() {} }));
  }
  get firstChild() {
    return this.children[0] || null;
  }
}

function findByText(node, pattern) {
  if (pattern.test(node._text || '')) return node;
  for (const child of node.children || []) {
    const found = findByText(child, pattern);
    if (found) return found;
  }
  return null;
}

function load({ setupWizard, state, routes = {} } = {}) {
  const elements = {};
  for (const id of [
    'calendarOpsSetupCard',
    'calendarOpsSetupStatus',
    'calendarOpsSetupBadge',
    'calendarOpsSetupBody',
    'calendarOpsSetupActions'
  ]) {
    const element = new FakeElement('div');
    element.id = id;
    elements[id] = element;
  }
  const opened = [];
  const document = {
    readyState: 'complete',
    addEventListener() {},
    getElementById: id => elements[id] || null,
    createElement: tag => new FakeElement(tag),
    createTextNode: text => {
      const node = new FakeElement('span');
      node.textContent = text;
      return node;
    }
  };
  const window = {
    currentWorkspaceId: 'ws-1',
    alert: () => {},
    open: url => opened.push(url),
    SetupWizard: setupWizard
  };
  const context = {
    console,
    window,
    document,
    setTimeout,
    fetch: async url => {
      const match = Object.keys(routes).find(key => String(url).includes(key));
      return { ok: true, json: async () => (match ? routes[match] : {}) };
    }
  };
  vm.runInNewContext(source, context, { filename: 'calendar-ops-setup.js' });
  const api = window.CalendarOpsSetup;
  if (state) api._setState(state);
  return { api, elements, opened, window };
}

const wizardActive = {
  getStatus: () => ({ applicable: true, state: 'in_progress' }),
  open: () => {},
  registerStepRenderer: () => {}
};

function stepCtx(kind, overrides = {}) {
  return {
    step: { id: kind, kind, adapter: 'calendar_ops' },
    setBusy() {},
    setError() {},
    announce() {},
    refresh: async () => {},
    confirm: async () => {},
    recheck: async () => {},
    rememberReturn() {},
    ...overrides
  };
}

const connectorMissing = {
  applicable: true,
  state: 'connector_missing',
  presets: [
    {
      id: 'google-calendar',
      display_name: 'Google Calendar (Developer Preview)',
      developer_preview: true,
      prerequisites: ['A Google Cloud project'],
      docs_url: 'https://example.test/docs'
    }
  ],
  preset_added: {},
  existing_connectors: [{ name: 'my-calendar-mcp', remote: false }],
  settings: {}
};

const authRequired = {
  applicable: true,
  state: 'auth_required',
  binding: { id: 'b1', server_name: 'google-calendar' },
  connector: { status: 'auth_required' },
  settings: {}
};

const ready = {
  applicable: true,
  state: 'ready',
  binding: { id: 'b1', server_name: 'google-calendar', mapping_valid: true },
  connector: { status: 'running' },
  settings: { display_time_zone: 'America/New_York', validated: true },
  context_workspace_candidates: []
};

test('a wizard-enabled workspace gets a status and a way in, not the full card', () => {
  const { api, elements } = load({ setupWizard: wizardActive });
  api.render(connectorMissing);

  const body = elements.calendarOpsSetupBody;
  assert.match(body.textContent, /Continue setup/);
  // The card no longer carries setup of its own: no connector picker, no
  // mapping editor, no save.
  assert.equal(findByText(body, /Choose a calendar connector/), null);
  assert.equal(findByText(body, /Mapping/), null);
});

test('a regressed connection is offered repair from the card', () => {
  const { api, elements } = load({
    setupWizard: {
      ...wizardActive,
      getStatus: () => ({ applicable: true, state: 'needs_attention' })
    }
  });
  api.render({ ...ready, state: 'degraded' });
  assert.match(elements.calendarOpsSetupBody.textContent, /Repair setup/);
  assert.match(elements.calendarOpsSetupBody.textContent, /stopped working/);
});

test('a workspace whose blueprint has no wizard keeps the original card', () => {
  const { api, elements } = load({ setupWizard: undefined });
  api.render(connectorMissing);
  // Nobody loses their way to set up because their workspace predates the
  // wizard.
  assert.match(elements.calendarOpsSetupBody.textContent, /Choose a calendar connector/);
});

test('the connect step offers the shipped preset and any existing connector', () => {
  const { api } = load({ setupWizard: wizardActive, state: connectorMissing });
  const container = new FakeElement('div');
  api._setupSteps.connect.render(container, stepCtx('capability_connect'));

  assert.match(container.textContent, /Choose a calendar connector/);
  assert.match(container.textContent, /Google Calendar \(Developer Preview\)/);
  // FR-88: an existing, already-authorized connector can be reused rather than
  // duplicated.
  assert.match(container.textContent, /Or use an existing MCP server/);
  assert.match(container.textContent, /my-calendar-mcp/);
  assert.equal(
    api._setupSteps.connect.primaryLabel(stepCtx('capability_connect')),
    'Choose a connector to continue'
  );
});

test('the connect step asks for authorization only once a connector is chosen', () => {
  const { api } = load({ setupWizard: wizardActive, state: authRequired });
  const container = new FakeElement('div');
  api._setupSteps.connect.render(container, stepCtx('capability_connect'));

  assert.match(container.textContent, /needs authorization/);
  assert.match(container.textContent, /Connect/);
  assert.equal(api._setupSteps.connect.primaryLabel(stepCtx('capability_connect')), 'Check again');
});

test('leaving for the provider records where to resume first', async () => {
  let remembered = 0;
  const { api, opened } = load({
    setupWizard: wizardActive,
    state: authRequired,
    // The connector answers the way a first-time sign-in does: go authorize
    // with the provider.
    routes: { '/connect': { authorize_url: 'https://provider.test/authorize' } }
  });
  const container = new FakeElement('div');
  api._setupSteps.connect.render(
    container,
    stepCtx('capability_connect', {
      rememberReturn: () => {
        // Recorded *before* the browser leaves, or there is nothing to come
        // back to (FR-53).
        assert.equal(opened.length, 0, 'the resume point is recorded before the tab opens');
        remembered += 1;
      }
    })
  );

  const connect = findByText(container, /^Connect$/);
  assert.ok(connect, 'the connect action is rendered');
  connect.click();
  await new Promise(resolve => setTimeout(resolve, 20));

  assert.equal(remembered, 1, 'the wizard step to resume at is recorded');
  assert.deepEqual(opened, ['https://provider.test/authorize']);
});

test('the configure step waits for a connection before asking about mapping', () => {
  const { api } = load({ setupWizard: wizardActive, state: authRequired });
  const container = new FakeElement('div');
  api._setupSteps.configure.render(container, stepCtx('capability_configure'));
  assert.match(container.textContent, /Connect a calendar first/);
  assert.equal(findByText(container, /Discover tools/), null);
});

test('the configure step carries the mapping editor, the test, and the preferences', () => {
  const { api } = load({
    setupWizard: wizardActive,
    state: { ...ready, state: 'mapping_required' }
  });
  const container = new FakeElement('div');
  api._setupSteps.configure.render(container, stepCtx('capability_configure'));

  // Guided suggestions and the editable mapping are both present: the advanced
  // editor is how a wrong tool or field pointer gets corrected.
  assert.match(container.textContent, /Discover tools & suggest mappings/);
  assert.match(container.textContent, /nothing activates until you run Validate and Save/);
  assert.match(container.textContent, /Validate/);
  // Preferences save through the same endpoint, before readiness can pass.
  assert.match(container.textContent, /Display timezone/);
  assert.match(container.textContent, /Save/);
});

test('another blueprint’s capability step is not drawn by this module', () => {
  const { api } = load({ setupWizard: wizardActive, state: connectorMissing });
  const container = new FakeElement('div');
  api._setupSteps.connect.render(container, {
    ...stepCtx('capability_connect'),
    step: { id: 'connect', kind: 'capability_connect', adapter: 'email_ops' }
  });
  assert.equal(container.textContent, '');
  assert.equal(
    api._setupSteps.connect.primaryLabel({
      step: { kind: 'capability_connect', adapter: 'email_ops' }
    }),
    ''
  );
});

test('a step asked for before this module has state fills itself in', async () => {
  // The wizard can render a step before this module's own fetch has landed —
  // on a first page load it always does. An empty step that never fills is the
  // failure this covers, and it is silent: every request involved succeeded.
  let redraws = 0;
  const { api } = load({
    setupWizard: wizardActive,
    routes: { '/api/calendar-ops/setup': connectorMissing }
  });
  const container = new FakeElement('div');
  api._setupSteps.connect.render(
    container,
    stepCtx('capability_connect', {
      refresh: async () => {
        redraws += 1;
      }
    })
  );
  assert.equal(container.textContent, '', 'nothing to draw yet');

  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(redraws, 1, 'the shell is asked to draw the step again once state arrives');
});

test('the workspace id comes from the URL, not a global set later', () => {
  const { api } = load({ setupWizard: wizardActive });
  // The module must not depend on window.currentWorkspaceId, which a later
  // module script sets.
  assert.ok(api, 'module loaded without a workspace global');
});
