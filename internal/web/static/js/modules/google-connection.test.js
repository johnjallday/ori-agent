import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./google-connection.js', import.meta.url), 'utf8');

// --- Minimal DOM ------------------------------------------------------------
// Enough of the shape google-connection.js touches to exercise the vault
// preflight states: element trees, class toggling, radio groups, and clicks.

function makeElement(tag = 'div') {
  const el = {
    tagName: String(tag).toUpperCase(),
    type: '',
    name: '',
    value: '',
    checked: false,
    textContent: '',
    disabled: false,
    className: '',
    style: {},
    children: [],
    attributes: {},
    listeners: {},
    focused: false,
    classList: {
      _set: new Set(),
      add(...c) {
        c.forEach(x => this._set.add(x));
      },
      remove(...c) {
        c.forEach(x => this._set.delete(x));
      },
      contains(c) {
        return this._set.has(c);
      }
    },
    set innerHTML(v) {
      if (v === '') this.children = [];
    },
    get innerHTML() {
      return '';
    },
    appendChild(child) {
      this.children.push(child);
      return child;
    },
    setAttribute(k, v) {
      this.attributes[k] = v;
    },
    removeAttribute(k) {
      delete this.attributes[k];
    },
    getAttribute(k) {
      return this.attributes[k];
    },
    addEventListener(type, fn) {
      (this.listeners[type] ||= []).push(fn);
    },
    focus() {
      this.focused = true;
    },
    click() {
      (this.listeners.click || []).forEach(fn => fn());
    },
    querySelector(sel) {
      return descendants(el).find(d => matches(d, sel)) || null;
    },
    querySelectorAll(sel) {
      return descendants(el).filter(d => matches(d, sel));
    }
  };
  return el;
}

function descendants(el) {
  return el.children.flatMap(c => [c, ...descendants(c)]);
}

function matches(el, sel) {
  return sel
    .split(',')
    .map(s => s.trim())
    .some(s => {
      if (s === 'input:checked') return el.tagName === 'INPUT' && el.checked;
      if (s === 'input') return el.tagName === 'INPUT';
      if (s === 'button') return el.tagName === 'BUTTON';
      return false;
    });
}

const CARD_IDS = [
  'googleConnStatus',
  'googleConnStatusText',
  'googleConnConnected',
  'googleConnDisconnected',
  'googleConnEmail',
  'googleConnName',
  'googleConnAvatar',
  'googleConnBadge',
  'googleConnProducts',
  'googleConnVault',
  'googleConnMigrate',
  'googleConnDriveSetup',
  'googleConnConfirm',
  'googleConnConnectBtn',
  'googleConnDisconnectBtn',
  'googleConnSwitchBtn',
  'googleConnError'
];

/**
 * Loads the module against a fake DOM and a scripted fetch. `responses` maps a
 * URL substring to {status, body}; every call is recorded in calls[].
 */
function loadCard({ responses = {}, search = '' } = {}) {
  const elements = {};
  CARD_IDS.forEach(id => {
    elements[id] = makeElement();
  });

  const calls = [];
  const fetchImpl = async (url, opts = {}) => {
    calls.push({ url, method: opts.method || 'GET' });
    const key = Object.keys(responses).find(k => url.includes(k));
    const spec = key ? responses[key] : { status: 200, body: {} };
    return {
      ok: spec.status >= 200 && spec.status < 300,
      status: spec.status,
      json: async () => spec.body
    };
  };

  const assigned = [];
  const context = {
    console,
    URLSearchParams,
    document: {
      readyState: 'complete',
      addEventListener() {},
      getElementById: id => elements[id] || null,
      createElement: tag => makeElement(tag),
      querySelector: () => null,
      querySelectorAll: () => []
    },
    window: {
      location: {
        search,
        pathname: '/settings',
        hash: '#google-account',
        assign: u => assigned.push(u)
      },
      history: { replaceState() {} },
      addEventListener() {},
      open: () => null
    },
    fetch: fetchImpl
  };
  context.window.document = context.document;
  vm.createContext(context);
  vm.runInContext(source, context);
  return { mgr: context.window.googleConnectionManager, elements, calls, assigned };
}

const vaultPanel = elements => elements.googleConnVault;
const panelText = panel =>
  panel.children
    .map(c => c.textContent)
    .filter(Boolean)
    .join(' ');
const buttonNamed = (panel, label) =>
  descendants(panel).find(c => c.tagName === 'BUTTON' && c.textContent === label);

// --- Preflight states -------------------------------------------------------

test('a locked vault opens the unlock step instead of navigating to Google', async () => {
  const { mgr, elements, assigned } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'unlock',
          message: 'Unlock the vault that stores your Google credentials.',
          vault_id: 'v-1',
          vault_name: 'Personal'
        }
      }
    }
  });

  await mgr.enableGmail(null, null);

  const panel = vaultPanel(elements);
  assert.equal(panel.classList.contains('d-none'), false, 'vault panel must be visible');
  assert.match(panelText(panel), /Unlock your vault/);
  assert.ok(buttonNamed(panel, 'Unlock and continue'), 'must offer the unlock action');
  assert.deepEqual(assigned, [], 'must not open Google while the vault is locked');
});

test('unlocking resumes the remembered enable and then navigates to Google', async () => {
  const { mgr, elements, calls, assigned } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'unlock',
          message: 'Unlock it.',
          vault_id: 'v-1'
        }
      },
      '/api/vault/unlock': { status: 200, body: {} }
    }
  });

  await mgr.enableGmail(null, 'send');
  // Second enable succeeds now that the vault is open.
  const panel = vaultPanel(elements);
  const password = descendants(panel).find(c => c.tagName === 'INPUT');
  password.value = 'hunter2';

  let resolveEnable;
  const enabled = new Promise(r => {
    resolveEnable = r;
  });
  mgr.enableGmail = async (btn, scope, vaultId) => {
    resolveEnable({ scope, vaultId });
  };

  buttonNamed(panel, 'Unlock and continue').click();
  const resumed = await enabled;

  assert.equal(resumed.scope, 'send', 'the pending send-upgrade intent must survive the unlock');
  assert.equal(resumed.vaultId, 'v-1', 'must resume with the unlocked vault');
  assert.ok(calls.some(c => c.url === '/api/vault/unlock' && c.method === 'POST'));
  assert.deepEqual(assigned, [], 'unlock alone must not navigate');
});

test('cancelling a vault prompt leaves Gmail disabled and Google unopened', async () => {
  const { mgr, elements, assigned } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'choose',
          message: 'Choose one.',
          vaults: [
            { id: 'v-1', name: 'Personal' },
            { id: 'v-2', name: 'Work' }
          ]
        }
      }
    }
  });

  await mgr.enableGmail(null, null);
  const panel = vaultPanel(elements);
  buttonNamed(panel, 'Cancel').click();

  assert.equal(panel.classList.contains('d-none'), true, 'panel must close');
  assert.equal(mgr.pendingEnable, null, 'the pending intent must be dropped');
  assert.deepEqual(assigned, [], 'cancelling must never open Google');
});

test('choosing among several vaults resumes with the selected id', async () => {
  const { mgr, elements } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'choose',
          message: 'Choose one.',
          vaults: [
            { id: 'v-1', name: 'Personal' },
            { id: 'v-2', name: 'Work' }
          ]
        }
      }
    }
  });

  await mgr.enableGmail(null, null);
  const panel = vaultPanel(elements);
  const radios = descendants(panel).filter(c => c.tagName === 'INPUT');
  assert.equal(radios.length, 2, 'one option per vault');
  radios[0].checked = false;
  radios[1].checked = true;

  let picked;
  mgr.enableGmail = async (btn, scope, vaultId) => {
    picked = vaultId;
  };
  buttonNamed(panel, 'Use this vault').click();

  assert.equal(picked, 'v-2');
});

test('choosing a locked vault routes through unlock rather than failing later', async () => {
  const { mgr, elements } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'choose',
          message: 'Choose one.',
          vaults: [
            { id: 'v-1', name: 'Personal', locked: true },
            { id: 'v-2', name: 'Work' }
          ]
        }
      }
    }
  });

  await mgr.enableGmail(null, null);
  const panel = vaultPanel(elements);
  buttonNamed(panel, 'Use this vault').click();

  assert.match(panelText(vaultPanel(elements)), /Unlock your vault/);
});

test('no vault at all offers inline creation and resumes with the new vault', async () => {
  const { mgr, elements } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: { error: 'vault_action_required', action: 'create', message: 'Create one.' }
      },
      '/api/vault/vaults': { status: 201, body: { success: true, vault: { id: 'v-new' } } }
    }
  });

  await mgr.enableGmail(null, null);
  const panel = vaultPanel(elements);
  const inputs = descendants(panel).filter(c => c.tagName === 'INPUT');
  assert.equal(inputs.length, 2, 'name + password');
  inputs[1].value = 'hunter2';

  let resolveEnable;
  const created = new Promise(r => {
    resolveEnable = r;
  });
  mgr.enableGmail = async (btn, scope, vaultId) => {
    resolveEnable(vaultId);
  };
  buttonNamed(panel, 'Create vault and continue').click();

  assert.equal(await created, 'v-new');
});

// --- Accessibility ----------------------------------------------------------

test('vault prompts are keyboard reachable and announce their state', async () => {
  const { mgr, elements } = loadCard({
    responses: {
      'gmail/enable': {
        status: 409,
        body: {
          error: 'vault_action_required',
          action: 'choose',
          message: 'Choose one.',
          vaults: [{ id: 'v-1', name: 'Personal' }]
        }
      }
    }
  });

  await mgr.enableGmail(null, null);
  const panel = vaultPanel(elements);

  const group = panel.children.find(c => c.getAttribute('role') === 'radiogroup');
  assert.ok(group, 'options must form a labelled radio group');
  assert.ok(group.getAttribute('aria-label'), 'the group needs an accessible name');

  const buttons = descendants(panel).filter(c => c.tagName === 'BUTTON');
  assert.ok(buttons.length > 0);
  buttons.forEach(b => {
    assert.equal(b.type, 'button', 'buttons must be real buttons, not click handlers on divs');
    assert.ok(b.getAttribute('aria-label'), `"${b.textContent}" needs an action-specific label`);
  });

  const error = descendants(panel).find(c => c.getAttribute('role') === 'alert');
  assert.ok(error, 'validation errors must be announced');

  const focused = descendants(panel).filter(c => c.focused);
  assert.equal(focused.length, 1, 'focus must move into the prompt exactly once');
});

// --- Callback repair hint ---------------------------------------------------

test('returning from a vault_locked callback re-opens the unlock step', async () => {
  const { mgr, elements, assigned } = loadCard({ search: '?gc_action=unlock&gc_vault=v-9' });

  mgr.render({ subject: 'sub-1', state: 'connected', email: 'a@b.c', grants: [] });

  const panel = vaultPanel(elements);
  assert.match(panelText(panel), /Unlock your vault/);
  assert.deepEqual(assigned, [], 'the repair hint must not re-contact Google');
});

test('a choose-vault callback hint uses the read-only preflight, not a new authorization', async () => {
  const { mgr, elements, calls } = loadCard({
    search: '?gc_action=choose',
    responses: {
      '/api/connections/google/vault': {
        status: 200,
        body: {
          action: 'choose',
          message: 'Choose one.',
          vaults: [{ id: 'v-1', name: 'Personal' }]
        }
      }
    }
  });

  mgr.render({ subject: 'sub-1', state: 'connected', email: 'a@b.c', grants: [] });
  await new Promise(r => setImmediate(r));

  assert.ok(calls.some(c => c.url === '/api/connections/google/vault' && c.method === 'GET'));
  assert.ok(!calls.some(c => c.url.includes('gmail/enable')), 'must not start an authorization');
  assert.match(panelText(vaultPanel(elements)), /Choose a vault/);
});

// --- Scope separation -------------------------------------------------------

test('enable requests read-only Gmail; the send upgrade is a separate explicit action', async () => {
  const { mgr, calls } = loadCard({
    responses: {
      'gmail/enable': { status: 200, body: { authorize_url: 'https://accounts.google.com/x' } }
    }
  });

  await mgr.enableGmail(null, null);
  await mgr.enableGmail(null, 'send');

  const enables = calls.filter(c => c.url.includes('gmail/enable'));
  assert.equal(
    enables[0].url,
    '/api/connections/google/gmail/enable',
    'plain enable asks for no send scope'
  );
  assert.equal(enables[1].url, '/api/connections/google/gmail/enable?scope=send');
});
