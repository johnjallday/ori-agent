// Tests for personal-hq-email-setup.js — Email Ops inside the shared Setup
// Wizard, and the legacy Personal HQ modal it must not disturb.
//
// Run with: node --test internal/web/static/js/modules/personal-hq-email-setup.test.js
//
// The module serves two kinds of workspace. Email Ops routes through the
// wizard's account-link step; the designated Personal HQ, which has no
// blueprint wizard, keeps its own modal. These tests pin that split, and pin
// that the three states which look alike from outside — no account, no mail
// permission, no link *here* — lead to different actions.
import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag = 'div') {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = {};
    this.classList = {
      _set: new Set(),
      add(name) {
        this._set.add(name);
      },
      remove(name) {
        this._set.delete(name);
      },
      contains(name) {
        return this._set.has(name);
      }
    };
    this.listeners = {};
    this.hidden = false;
    this.disabled = false;
    this._text = '';
    this.innerHTML = '';
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
  append(...nodes) {
    nodes.forEach(node => this.appendChild(node));
  }
  setAttribute(name, value) {
    this.attributes[name] = value;
  }
  hasAttribute(name) {
    return name in this.attributes;
  }
  addEventListener(type, handler) {
    (this.listeners[type] = this.listeners[type] || []).push(handler);
  }
  querySelector() {
    return new FakeElement();
  }
  async click() {
    for (const handler of this.listeners.click || []) await handler({ preventDefault() {} });
  }
}

function findButton(node) {
  if (node.tagName === 'BUTTON') return node;
  for (const child of node.children || []) {
    const found = findButton(child);
    if (found) return found;
  }
  return null;
}

/**
 * Loads the module against a scripted server.
 *
 * The module is an ES module with a top-level import, so it is imported once
 * and re-driven per test through the globals it reads — the same way it runs in
 * the browser, where it also loads once per page.
 */
async function load({ mailboxStatus, hq = false, wizardApplicable = true, personalHQ } = {}) {
  const elements = {
    hqEmailSetupBtn: new FakeElement('button'),
    hqEmailSetupModal: new FakeElement('div'),
    workspaceEmailConnectMount: new FakeElement('div')
  };
  const calls = [];
  const opened = [];
  const toasts = [];

  globalThis.window = {
    location: { pathname: '/workspaces/ws-1', href: '' },
    currentWorkspaceId: 'ws-1',
    Toast: {
      success: message => toasts.push(message),
      danger: message => toasts.push(message),
      info: message => toasts.push(message)
    },
    open: url => opened.push(url),
    SetupWizard: {
      getStatus: () => ({ applicable: wizardApplicable, state: 'in_progress' }),
      registerStepRenderer: () => {}
    }
  };
  globalThis.document = {
    body: new FakeElement('body'),
    getElementById: id => elements[id] || null,
    createElement: tag => new FakeElement(tag),
    addEventListener() {}
  };
  globalThis.fetch = async (url, options) => {
    calls.push({ url: String(url), method: options?.method || 'GET' });
    const path = String(url);
    let payload = {};
    if (path.includes('/api/personal-hq/status')) {
      payload = personalHQ || { status: { valid: hq, workspace_id: 'ws-1' } };
    } else if (path.includes('/api/personal-hq/email-ops')) {
      payload = { status: { exists: !hq, workspace_id: 'ws-1' } };
    } else if (path.includes('/email/status') || path.includes('/personal-hq/email/status')) {
      payload = { status: mailboxStatus };
    }
    return { ok: true, json: async () => payload };
  };

  const module = await import('./personal-hq-email-setup.js?t=' + Math.random());
  void module;
  // Let the module's own resolveScope()/status fetch settle.
  await new Promise(resolve => setTimeout(resolve, 20));
  return { elements, calls, opened, toasts, steps: globalThis.window.OriEmailOpsSetupSteps };
}

function stepCtx(overrides = {}) {
  return {
    step: { id: 'mailbox', kind: 'account_link', adapter: 'email_ops' },
    setBusy() {},
    setError() {},
    announce() {},
    refresh: async () => {},
    confirm: async () => {},
    rememberReturn() {},
    ...overrides
  };
}

const noAccount = {
  connected: false,
  setup: {
    ready: false,
    reason: 'connection_required',
    message: 'Connect an account in Settings first.',
    action: 'connect_google',
    action_label: 'Connect account',
    action_url: '/settings#google-account'
  }
};

const notLinkedHere = {
  connected: false,
  setup: {
    ready: false,
    reason: 'not_linked_to_workspace',
    message: 'Connect your email account to this workspace to start triaging the inbox.',
    action: 'link_account',
    action_label: 'Connect email'
  }
};

const linked = {
  connected: true,
  email_address: 'someone@example.test',
  health: 'healthy',
  setup: { ready: true }
};

test('an account that was never connected sends the user to settings, and records the way back', async () => {
  const { steps, opened } = await load({ mailboxStatus: noAccount });
  assert.ok(steps, 'the link step renderer is registered');

  let remembered = 0;
  const container = new FakeElement('div');
  const context = stepCtx({ rememberReturn: () => (remembered += 1) });
  steps.link.render(container, context);
  await new Promise(resolve => setTimeout(resolve, 20));

  assert.match(container.textContent, /Connect an account in Settings first/);
  const action = findButton(container);
  assert.equal(action.textContent, 'Connect account');
  await action.click();
  // Settings is another page: the step to resume at is recorded before leaving.
  assert.equal(remembered, 1);
  assert.equal(globalThis.window.location.href, '/settings#google-account');
  void opened;
});

test('a healthy account that is simply not linked here offers to link it', async () => {
  const { steps } = await load({ mailboxStatus: notLinkedHere });
  const container = new FakeElement('div');
  steps.link.render(container, stepCtx());
  await new Promise(resolve => setTimeout(resolve, 20));

  assert.match(container.textContent, /Connect your email account to this workspace/);
  assert.equal(findButton(container).textContent, 'Link a mailbox');
});

test('a linked mailbox is named, and can be relinked', async () => {
  const { steps } = await load({ mailboxStatus: linked });
  const container = new FakeElement('div');
  steps.link.render(container, stepCtx());
  await new Promise(resolve => setTimeout(resolve, 20));

  assert.match(container.textContent, /someone@example\.test/);
  assert.equal(findButton(container).textContent, 'Relink this mailbox');
});

test('the wizard’s banner replaces the workspace connect CTA rather than sitting beside it', async () => {
  const { elements } = await load({ mailboxStatus: notLinkedHere, wizardApplicable: true });
  assert.equal(elements.workspaceEmailConnectMount.hidden, true);

  const withoutWizard = await load({ mailboxStatus: notLinkedHere, wizardApplicable: false });
  assert.equal(withoutWizard.elements.workspaceEmailConnectMount.hidden, false);
  assert.match(withoutWizard.elements.workspaceEmailConnectMount.innerHTML || '', /^$/);
});

test('the Personal HQ workspace keeps its own modal and gets no second link', async () => {
  const { steps, elements } = await load({ mailboxStatus: linked, hq: true });
  const container = new FakeElement('div');
  steps.link.render(container, stepCtx());
  await new Promise(resolve => setTimeout(resolve, 30));

  // Personal HQ's email lives on its own binding and its onboarding is not
  // migrated. The step says so rather than linking a second mailbox to the same
  // workspace.
  assert.match(container.textContent, /Personal HQ/);
  assert.equal(findButton(container), null, 'no second link action is offered');
  assert.equal(elements.hqEmailSetupBtn.hidden, false, 'the HQ button still appears');
});

test('another blueprint’s account-link step is not drawn by this module', async () => {
  const { steps } = await load({ mailboxStatus: notLinkedHere });
  const container = new FakeElement('div');
  steps.link.render(container, {
    ...stepCtx(),
    step: { id: 'mailbox', kind: 'account_link', adapter: 'calendar_ops' }
  });
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(container.textContent, '');
  assert.equal(
    steps.link.primaryLabel({ step: { kind: 'account_link', adapter: 'calendar_ops' } }),
    ''
  );
});
