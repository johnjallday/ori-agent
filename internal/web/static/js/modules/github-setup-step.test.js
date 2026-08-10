// Tests for github-setup-step.js — the GitHub Ops wizard step renderers.
// Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/github-setup-step.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.className = '';
    this.hidden = false;
    this.id = '';
    this.href = '';
    this.type = '';
    this.value = '';
    this.placeholder = '';
    this.children = [];
    this._text = '';
    this._listeners = {};
  }
  get textContent() {
    return this._text + this.children.map(c => c.textContent).join('');
  }
  set textContent(v) {
    this._text = v;
    this.children = [];
  }
  appendChild(c) {
    this.children.push(c);
    return c;
  }
  append(...nodes) {
    nodes.forEach(n => this.children.push(n));
  }
  setAttribute(name, value) {
    this[name] = value;
  }
  addEventListener(name, fn) {
    (this._listeners[name] = this._listeners[name] || []).push(fn);
  }
  fire(name) {
    (this._listeners[name] || []).forEach(fn => fn());
  }
  all() {
    return this.children.flatMap(c => [c, ...c.all()]);
  }
  querySelector(sel) {
    return this.querySelectorAll(sel)[0] || null;
  }
  querySelectorAll(sel) {
    const cls = sel.replace('.', '');
    return this.all().filter(n => String(n.className).split(/\s+/).includes(cls));
  }
  get parentNode() {
    return this._parent || null;
  }
  insertBefore(node, ref) {
    const i = this.children.indexOf(ref);
    if (i < 0) this.children.push(node);
    else this.children.splice(i, 0, node);
    return node;
  }
}

function setup() {
  globalThis.document = {
    createElement: tag => new FakeElement(tag),
    createTextNode: text => {
      const n = new FakeElement('#text');
      n._text = text;
      return n;
    },
    getElementById: id => globalThis.__byId?.[id] || null,
    addEventListener() {},
    readyState: 'complete'
  };
  globalThis.__byId = {};
  globalThis.window = globalThis;
  // A wizard stub that records what gets registered.
  globalThis.window.SetupWizard = {
    registered: {},
    registerStepRenderer(kind, renderer) {
      this.registered[kind] = renderer;
    }
  };
}

// A container whose children link back to it, so insertBefore works the way
// the renderer's filter needs.
function container() {
  const node = new FakeElement('div');
  const originalAppend = node.appendChild.bind(node);
  node.appendChild = child => {
    child._parent = node;
    return originalAppend(child);
  };
  return node;
}

function ctxFor(step, options = {}) {
  return {
    step,
    renderDefault: options.renderDefault || (() => {}),
    confirm: options.confirm || (async () => {}),
    recheck: options.recheck || (async () => {}),
    setError: options.setError || (() => {}),
    setBusy: options.setBusy || (() => {}),
    announce: options.announce || (() => {})
  };
}

const githubStep = { id: 'account', kind: 'account_link', adapter: 'github_ops', status: 'active' };
const emailStep = { id: 'mailbox', kind: 'account_link', adapter: 'email_ops', status: 'active' };

setup();
await import('./github-setup-step.js');
const wizard = globalThis.window.SetupWizard;

// --- ownership ---------------------------------------------------------------

// account_link and capability_configure are shared kinds. Claiming only this
// blueprint's steps is what keeps Email Ops and Calendar Ops working.
test('registers renderers that claim only GitHub Ops steps', () => {
  const connect = wizard.registered.account_link;
  const repository = wizard.registered.capability_configure;

  assert.ok(connect, 'the connect step renderer should be registered');
  assert.ok(repository, 'the repository step renderer should be registered');

  assert.equal(connect.owns(githubStep), true);
  assert.equal(connect.owns(emailStep), false, "must not claim Email Ops' account_link step");
  assert.equal(
    repository.owns({ kind: 'capability_configure', adapter: 'calendar_ops' }),
    false,
    "must not claim Calendar Ops' configure step"
  );
});

test('hands the primary label back for steps it does not own', () => {
  const connect = wizard.registered.account_link;
  assert.equal(
    connect.primaryLabel(ctxFor(emailStep)),
    '',
    'a foreign step keeps the generic label'
  );
  assert.equal(connect.primaryLabel(ctxFor(githubStep)), 'Connect GitHub');
});

// --- connect step ------------------------------------------------------------

test('the connect step offers a token field and the token page link', () => {
  const connect = wizard.registered.account_link;
  const host = container();
  connect.render(host, ctxFor(githubStep));

  const input = host.all().find(n => n.id === 'githubSetupToken');
  assert.ok(input, 'a token field must be offered — confirming alone cannot connect');
  assert.equal(input.type, 'password', 'the token must be masked');

  const link = host.all().find(n => n.tagName === 'A');
  assert.ok(link.href.startsWith('https://github.com/settings/personal-access-tokens'));
});

test('an already-connected step draws nothing of its own', () => {
  const connect = wizard.registered.account_link;
  const host = container();
  let usedDefault = false;
  connect.render(
    host,
    ctxFor(
      { ...githubStep, status: 'complete' },
      {
        renderDefault: () => {
          usedDefault = true;
        }
      }
    )
  );
  assert.equal(usedDefault, true, 'a satisfied step should fall back to the wizard');
  assert.equal(
    host.all().some(n => n.id === 'githubSetupToken'),
    false
  );
});

// --- repository step ---------------------------------------------------------

function optionList(count) {
  // Mimics what the wizard's own renderer produces.
  const list = new FakeElement('ul');
  list.className = 'setup-wizard-options';
  for (let i = 0; i < count; i += 1) {
    const item = new FakeElement('li');
    item.className = 'setup-wizard-option';
    item.textContent = `johnjallday/repo-${i}`;
    list.appendChild(item);
  }
  return list;
}

test('the repository step links to where token access is edited', () => {
  const repository = wizard.registered.capability_configure;
  const host = container();
  const step = { kind: 'capability_configure', adapter: 'github_ops', options: [{ id: 'a' }] };

  repository.render(host, ctxFor(step, { renderDefault: c => c.appendChild(optionList(1)) }));

  const link = host.all().find(n => n.tagName === 'A');
  assert.ok(link, 'the step should offer a way to fix repository access');
  assert.equal(
    link.href,
    'https://github.com/settings/personal-access-tokens',
    'it must point at the token list, where repository access is edited'
  );
  assert.ok(
    host.textContent.includes('Issues (read and write)'),
    'it should name the permission to grant'
  );
});

test('a long repository list gets a filter; a short one does not', () => {
  const repository = wizard.registered.capability_configure;

  const short = container();
  repository.render(
    short,
    ctxFor(
      {
        kind: 'capability_configure',
        adapter: 'github_ops',
        options: new Array(3).fill({ id: 'x' })
      },
      { renderDefault: c => c.appendChild(optionList(3)) }
    )
  );
  assert.equal(
    short.querySelectorAll('github-setup-filter').length,
    0,
    'a short list needs no filter'
  );

  const long = container();
  repository.render(
    long,
    ctxFor(
      {
        kind: 'capability_configure',
        adapter: 'github_ops',
        options: new Array(10).fill({ id: 'x' })
      },
      { renderDefault: c => c.appendChild(optionList(10)) }
    )
  );
  assert.equal(
    long.querySelectorAll('github-setup-filter').length,
    1,
    'a long list should be filterable'
  );
});

test('the filter hides only the repositories that do not match', () => {
  const repository = wizard.registered.capability_configure;
  const host = container();
  repository.render(
    host,
    ctxFor(
      {
        kind: 'capability_configure',
        adapter: 'github_ops',
        options: new Array(10).fill({ id: 'x' })
      },
      { renderDefault: c => c.appendChild(optionList(10)) }
    )
  );

  const filter = host.querySelector('.github-setup-filter');
  const input = filter.children.find(c => c.tagName === 'INPUT');
  input.value = 'repo-3';
  input.fire('input');

  const items = host.querySelectorAll('.setup-wizard-option');
  const visible = items.filter(i => !i.hidden);
  assert.equal(visible.length, 1, 'only the matching repository stays visible');
  assert.ok(visible[0].textContent.includes('repo-3'));

  // Clearing restores every option.
  input.value = '';
  input.fire('input');
  assert.equal(host.querySelectorAll('.setup-wizard-option').filter(i => !i.hidden).length, 10);
});
