// Tests for ori-guide.js and ori-guide-coachmarks.js
// (cozy-character-experience FR-23–FR-25, FR-30, FR-36–FR-43, FR-47–FR-50).
//   node --test internal/web/static/js/modules/ori-guide.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const guideSrc = readFileSync(new URL('./ori-guide.js', import.meta.url), 'utf8');
const coachSrc = readFileSync(new URL('./ori-guide-coachmarks.js', import.meta.url), 'utf8');

// Minimal DOM good enough to exercise the controller's logic. Only what the
// module actually touches is modelled; anything it reaches for that is missing
// would surface as a failure rather than being silently stubbed.
function makeElement(id, overrides = {}) {
  return {
    id,
    hidden: false,
    value: '',
    disabled: false,
    dataset: {},
    classList: {
      _set: new Set(),
      add(c) {
        this._set.add(c);
      },
      remove(c) {
        this._set.delete(c);
      },
      contains(c) {
        return this._set.has(c);
      }
    },
    attributes: {},
    innerHTML: '',
    focused: false,
    setAttribute(k, v) {
      this.attributes[k] = v;
    },
    getAttribute(k) {
      return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null;
    },
    focus() {
      this.focused = true;
    },
    scrollIntoView() {},
    getClientRects() {
      return [{}];
    },
    addEventListener() {},
    dispatchEvent() {
      return true;
    },
    insertAdjacentHTML(_pos, html) {
      this.innerHTML += html;
    },
    ...overrides
  };
}

function load({ route = '/', elements = {} } = {}) {
  const registry = {};
  const sandbox = {
    window: { location: { pathname: route } },
    document: {
      _els: elements,
      getElementById(id) {
        return elements[id] || null;
      },
      querySelector(sel) {
        return registry[sel] || null;
      },
      addEventListener() {},
      contains() {
        return true;
      },
      readyState: 'complete',
      activeElement: null
    },
    console: { warn() {}, debug() {} },
    Object,
    Number,
    String,
    Array,
    Boolean,
    Promise,
    Error,
    Event: function Event() {},
    JSON,
    fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve({}) })
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(coachSrc, sandbox);
  vm.runInContext(guideSrc, sandbox);
  return {
    guide: sandbox.window.OriGuide,
    coachmarks: sandbox.window.OriGuideCoachmarks,
    sandbox,
    registerSelector(sel, el) {
      registry[sel] = el;
    }
  };
}

/* ---- action validation (FR-36/FR-49) ---------------------------------------- */

test('unknown action types are dropped rather than rendered', () => {
  const { guide } = load();
  for (const type of ['create', 'delete', 'send', 'grant', 'execute', 'run', '', null]) {
    assert.equal(
      guide._validateAction({ type, label: 'x' }),
      null,
      `type ${type} should be dropped`
    );
  }
});

test('a navigate action requires a safe same-origin path', () => {
  const { guide } = load();
  const unsafe = [
    'https://evil.example.com',
    '//evil.example.com',
    'javascript:alert(1)',
    'agents',
    '',
    null
  ];
  for (const href of unsafe) {
    assert.equal(
      guide._validateAction({ type: 'navigate', label: 'Go', href }),
      null,
      `href ${href} should be rejected`
    );
  }
  const ok = guide._validateAction({ type: 'navigate', label: 'Open Agents', href: '/agents' });
  assert.equal(ok.href, '/agents');
});

test('isSafeHref rejects anything that is not an absolute internal path', () => {
  const { guide } = load();
  assert.ok(guide._isSafeHref('/agents'));
  assert.ok(guide._isSafeHref('/'));
  assert.ok(!guide._isSafeHref('//x'));
  assert.ok(!guide._isSafeHref('http://x'));
  assert.ok(!guide._isSafeHref('/a b'));
  assert.ok(!guide._isSafeHref('x/y'));
  // Hyphens must survive — /action-center is a real registered route.
  assert.ok(guide._isSafeHref('/action-center'));
});

test('a coachmark action is dropped unless the browser itself knows the key', () => {
  const { guide } = load({ route: '/agents' });
  assert.equal(guide._validateAction({ type: 'coachmark', coachmark: 'not_a_key' }), null);
  // Selectors must never be honoured, however they arrive.
  assert.equal(guide._validateAction({ type: 'coachmark', coachmark: '#newAgentBtn' }), null);
  assert.equal(guide._validateAction({ type: 'coachmark', coachmark: 'body' }), null);

  const ok = guide._validateAction({ type: 'coachmark', coachmark: 'new_agent' });
  assert.equal(ok.coachmark, 'new_agent');
});

test('a coachmark valid on another route is dropped on this one', () => {
  // new_agent belongs to /agents; on Home it must not survive validation.
  const { guide } = load({ route: '/' });
  assert.equal(guide._validateAction({ type: 'coachmark', coachmark: 'new_agent' }), null);
});

/* ---- coachmark registry (FR-41/FR-43) ---------------------------------------- */

test('every registered coachmark key is a plain token, never a selector', () => {
  const { coachmarks } = load();
  for (const key of coachmarks.keys()) {
    assert.match(key, /^[a-z0-9_]+$/, `key ${key} must be a plain token`);
  }
});

test('a key is only supported on the routes that own its control', () => {
  const { coachmarks } = load();
  assert.ok(coachmarks.supports('new_agent', '/agents'));
  assert.ok(!coachmarks.supports('new_agent', '/'));
  assert.ok(coachmarks.supports('workspace_manager', '/'));
  assert.ok(!coachmarks.supports('workspace_manager', '/agents'));
  assert.ok(!coachmarks.supports('nope', '/'));
  assert.ok(!coachmarks.supports('', '/'));
});

test('resolve returns null for a missing or hidden target', () => {
  const ctx = load({ route: '/agents' });
  // Not registered in the fake DOM at all.
  assert.equal(ctx.coachmarks.resolve('new_agent', '/agents', ctx.sandbox.document), null);

  const hidden = makeElement('newAgentBtn', { hidden: true });
  ctx.registerSelector('#newAgentBtn', hidden);
  assert.equal(ctx.coachmarks.resolve('new_agent', '/agents', ctx.sandbox.document), null);

  const offscreen = makeElement('newAgentBtn', { getClientRects: () => [] });
  ctx.registerSelector('#newAgentBtn', offscreen);
  assert.equal(ctx.coachmarks.resolve('new_agent', '/agents', ctx.sandbox.document), null);

  const visible = makeElement('newAgentBtn');
  ctx.registerSelector('#newAgentBtn', visible);
  assert.equal(ctx.coachmarks.resolve('new_agent', '/agents', ctx.sandbox.document), visible);
});

test('a coachmark marks and focuses its target but never activates it', () => {
  const ctx = load({ route: '/agents', elements: guideEls() });
  const target = makeElement('newAgentBtn', {
    click() {
      throw new Error('a coachmark must never click its target');
    },
    submit() {
      throw new Error('a coachmark must never submit');
    }
  });
  ctx.registerSelector('#newAgentBtn', target);

  const applied = ctx.guide._applyCoachmark('new_agent');
  assert.equal(applied, true);
  assert.ok(target.classList.contains('is-ori-coachmark'));
  assert.ok(target.focused, 'the control should receive focus');
  assert.equal(target.value, '', 'a coachmark must not change a value');
});

test('a stale coachmark target degrades to an explanation instead of failing silently', () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });
  // No element registered, so the target is absent.
  const applied = ctx.guide._applyCoachmark('new_agent');
  assert.equal(applied, false);
  assert.match(els.oriGuideReply.innerHTML, /cannot point at that control/i);
});

/* ---- work handoff (FR-40/FR-84) ------------------------------------------------ */

function guideEls() {
  return {
    oriGuidePanel: makeElement('oriGuidePanel'),
    oriGuideLauncher: makeElement('oriGuideLauncher'),
    oriGuideInput: makeElement('oriGuideInput'),
    oriGuideSend: makeElement('oriGuideSend'),
    oriGuideForm: makeElement('oriGuideForm'),
    oriGuideReply: makeElement('oriGuideReply'),
    oriGuideClose: makeElement('oriGuideClose')
  };
}

test('a handoff fills the work surface but never submits it', () => {
  const els = guideEls();
  let submitted = false;
  els.homeAssistantInput = makeElement('homeAssistantInput', {
    form: {
      submit() {
        submitted = true;
      }
    }
  });
  const { guide } = load({ route: '/', elements: els });

  const ok = guide._handoff('summarize the launch notes');
  assert.equal(ok, true);
  assert.equal(els.homeAssistantInput.value, 'summarize the launch notes');
  assert.ok(
    els.homeAssistantInput.focused,
    'the work surface should be focused for the user to send'
  );
  assert.equal(submitted, false, 'the guide must never submit the work surface');
});

test('a handoff away from Home routes there rather than pretending it worked', () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });
  const ok = ctx.guide._handoff('do the thing');
  assert.equal(ok, false);
  assert.equal(ctx.sandbox.window.location.href, '/');
});

/* ---- escaping (FR-45) ------------------------------------------------------------ */

test('rendered text is escaped, so a response cannot inject markup', () => {
  const { guide } = load();
  const escaped = guide._esc('<img src=x onerror="alert(1)">');
  assert.ok(!escaped.includes('<img'));
  assert.ok(escaped.includes('&lt;img'));
  assert.ok(!escaped.includes('"'));
});

test('a hostile label cannot break out of an action attribute', () => {
  const { guide } = load();
  const action = guide._validateAction({
    type: 'navigate',
    label: '" onmouseover="alert(1)',
    href: '/agents'
  });
  assert.ok(action, 'the action itself is valid; only its label is hostile');
  assert.ok(!guide._esc(action.label).includes('" onmouseover'));
});

/* ---- open / close (FR-24/FR-25/FR-26) --------------------------------------------- */

test('the guide starts closed and opening it exposes expanded state', () => {
  const els = guideEls();
  const { guide } = load({ elements: els });
  assert.equal(guide.isOpen(), false);

  guide.open(els.oriGuideLauncher);
  assert.equal(guide.isOpen(), true);
  assert.equal(els.oriGuidePanel.hidden, false);
  assert.equal(els.oriGuideLauncher.getAttribute('aria-expanded'), 'true');
});

test('closing returns focus to whatever opened the guide', () => {
  const els = guideEls();
  const { guide } = load({ elements: els });
  const trigger = makeElement('someTrigger');

  guide.open(trigger);
  guide.close();

  assert.equal(guide.isOpen(), false);
  assert.equal(els.oriGuidePanel.hidden, true);
  assert.equal(els.oriGuideLauncher.getAttribute('aria-expanded'), 'false');
  assert.ok(trigger.focused, 'focus should return to the opening control');
});

test('closing clears any active coachmark so the page is left as it was', () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });
  const target = makeElement('newAgentBtn');
  ctx.registerSelector('#newAgentBtn', target);

  ctx.guide.open(els.oriGuideLauncher);
  ctx.guide._applyCoachmark('new_agent');
  assert.ok(target.classList.contains('is-ori-coachmark'));

  ctx.guide.close();
  assert.ok(!target.classList.contains('is-ori-coachmark'), 'the mark must be removed on close');
});

test('toggling twice returns to the closed state', () => {
  const els = guideEls();
  const { guide } = load({ elements: els });
  guide.toggle(els.oriGuideLauncher);
  assert.equal(guide.isOpen(), true);
  guide.toggle(els.oriGuideLauncher);
  assert.equal(guide.isOpen(), false);
});

/* ---- resilience (FR-47/FR-50) ------------------------------------------------------ */

test('a failed guide request leaves the page usable and says so', async () => {
  const els = guideEls();
  const ctx = load({ elements: els });
  ctx.sandbox.fetch = () => Promise.reject(new Error('offline'));

  await ctx.guide.ask('where are agents');

  assert.equal(els.oriGuideReply.dataset.status, 'unavailable');
  assert.match(els.oriGuideReply.innerHTML, /still works/i);
  assert.equal(
    els.oriGuideSend.disabled,
    false,
    'the control must not stay disabled after a failure'
  );
});

// A browser will not submit a form whose submit button is disabled. If the
// panel's own opening request disabled it, a user who opens the guide and
// immediately types a question and presses Enter has it silently swallowed.
test('opening the guide never disables the send control', () => {
  const els = guideEls();
  const ctx = load({ elements: els });
  // A request that never resolves, so the pending state is observable.
  ctx.sandbox.fetch = () => new ctx.sandbox.Promise(() => {});

  ctx.guide.open(els.oriGuideLauncher);

  assert.equal(
    els.oriGuideSend.disabled,
    false,
    'the opening request must leave the send control usable'
  );
});

test('a question the user asked does show a busy control', () => {
  const els = guideEls();
  const ctx = load({ elements: els });
  ctx.sandbox.fetch = () => new ctx.sandbox.Promise(() => {});

  ctx.guide.ask('what is a workspace');
  assert.equal(els.oriGuideSend.disabled, true);
});

// The newest question wins. Opening the panel fires its own request, so a user
// who types faster than that request returns must not have their actual
// question silently dropped — which is exactly what an in-flight guard did.
test('a newer question supersedes one still in flight', async () => {
  const els = guideEls();
  const ctx = load({ elements: els });

  const resolvers = [];
  ctx.sandbox.fetch = () =>
    new ctx.sandbox.Promise(resolve => {
      resolvers.push(resolve);
    });

  const first = ctx.guide.ask('stale question');
  const second = ctx.guide.ask('real question');

  // Resolve them out of order: the stale reply lands last and must be ignored.
  resolvers[1]({
    ok: true,
    json: () => Promise.resolve({ status: 'answered', answer: 'REAL ANSWER' })
  });
  await second;
  resolvers[0]({
    ok: true,
    json: () => Promise.resolve({ status: 'answered', answer: 'STALE ANSWER' })
  });
  await first;

  assert.match(els.oriGuideReply.innerHTML, /REAL ANSWER/);
  assert.doesNotMatch(els.oriGuideReply.innerHTML, /STALE ANSWER/);
});

test('a superseded failure does not overwrite a newer answer', async () => {
  const els = guideEls();
  const ctx = load({ elements: els });

  const controls = [];
  ctx.sandbox.fetch = () =>
    new ctx.sandbox.Promise((resolve, reject) => {
      controls.push({ resolve, reject });
    });

  const first = ctx.guide.ask('doomed');
  const second = ctx.guide.ask('good');

  controls[1].resolve({
    ok: true,
    json: () => Promise.resolve({ status: 'answered', answer: 'GOOD ANSWER' })
  });
  await second;
  controls[0].reject(new Error('offline'));
  await first;

  assert.match(els.oriGuideReply.innerHTML, /GOOD ANSWER/);
  assert.notEqual(els.oriGuideReply.dataset.status, 'unavailable');
});
