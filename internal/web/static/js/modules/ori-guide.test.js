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

function load({ route = '/', elements = {}, session = {} } = {}) {
  const registry = {};
  const store = { ...session };
  const sandbox = {
    window: {
      location: { pathname: route },
      addEventListener() {},
      sessionStorage: {
        getItem: k => (Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null),
        setItem: (k, v) => {
          store[k] = String(v);
        },
        removeItem: k => {
          delete store[k];
        }
      }
    },
    // Exposed so tests can inspect what the module parked for the next page.
    _session: store,
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

// Mirrors registeredCoachmarkKeys in internal/agenthttp/ori_guide_topics.go.
// A key on one side and not the other means either a coachmark the browser
// cannot resolve, or a selector no topic will ever ask for.
test('the registry matches the keys the server knows about', () => {
  const { coachmarks } = load();
  const expected = [
    'new_agent',
    'workspace_manager',
    'quick_capture',
    'view_toggle',
    'new_workspace',
    'agent_toolbox',
    'action_center_review',
    'add_mcp_server'
  ].sort();

  assert.deepEqual([...coachmarks.keys()].sort(), expected);
});

test('every registry entry names at least one route and a selector', () => {
  const { coachmarks } = load();
  for (const key of coachmarks.keys()) {
    const entry = coachmarks.REGISTRY[key];
    assert.ok(Array.isArray(entry.routes) && entry.routes.length > 0, `${key} has no routes`);
    assert.ok(entry.selector && entry.selector.length > 0, `${key} has no selector`);
    // A human-readable label so the panel can name the control it is pointing
    // at, rather than relying on the highlight alone (FR-118).
    assert.ok(entry.label && entry.label.length > 0, `${key} has no label`);
    for (const route of entry.routes) {
      assert.ok(route.startsWith('/'), `${key} route ${route} is not an absolute path`);
    }
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

// Issue #350 removed the separate work surface. A handoff no longer travels
// anywhere: the composer that receives it is the one already open on this page.
test('a handoff fills the universal composer but never submits it', () => {
  const els = guideEls();
  const { guide } = load({ route: '/', elements: els });

  const ok = guide._handoff('summarize the launch notes');
  assert.equal(ok, true);
  assert.equal(els.oriGuideInput.value, 'summarize the launch notes');
  assert.ok(els.oriGuideInput.focused, 'the composer should be focused for the user to send');
});

// The property that mattered about the old handoff — it never runs anything on
// the user's behalf — still holds, and now holds without a navigation at all.
test('a handoff off Home stays on the page instead of navigating to Home', () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });

  const ok = ctx.guide._handoff('do the thing');
  assert.equal(ok, true, 'the panel is present on every page, so there is nowhere to send them');
  assert.equal(els.oriGuideInput.value, 'do the thing');
  assert.notEqual(
    ctx.sandbox.window.location.href,
    '/',
    'a handoff must no longer yank the user to Home'
  );
});

// A request parked by an older build (which still navigated) must not be lost
// when that user upgrades mid-flight.
test('a handoff parked by an older build is restored into the composer once', () => {
  const els = guideEls();
  const ctx = load({
    route: '/',
    elements: els,
    session: { 'ori-guide-handoff': 'summarize the launch notes' }
  });

  // init() runs on load and drains the parked request.
  assert.equal(els.oriGuideInput.value, 'summarize the launch notes');

  // Drained, so a later reload does not resurrect it.
  assert.equal(ctx.sandbox._session[ctx.guide.HANDOFF_KEY], undefined);
});

// The user's words are their own: they must never reach history, the address
// bar, or a referrer.
test('a handoff never travels through the URL', () => {
  const ctx = load({ route: '/agents', elements: guideEls() });
  ctx.guide._handoff('something private');
  assert.ok(!String(ctx.sandbox.window.location.href).includes('something'));
});

test('storage being unavailable never breaks a handoff', () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });
  ctx.sandbox.window.sessionStorage = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    },
    removeItem() {
      throw new Error('denied');
    }
  };

  assert.doesNotThrow(() => ctx.guide._handoff('do the thing'));
  assert.equal(els.oriGuideInput.value, 'do the thing');
});

/* ---- stale coachmarks across route changes (FR-43) ---------------------------- */

test('a coachmark made on one route is cleared when the route changes', () => {
  const ctx = load({ route: '/agents', elements: guideEls() });
  const target = makeElement('newAgentBtn');
  ctx.registerSelector('#newAgentBtn', target);

  ctx.guide._applyCoachmark('new_agent');
  assert.ok(target.classList.contains('is-ori-coachmark'));

  // The page changed the URL without reloading, as the Agents collection does
  // when filters move into history.
  ctx.sandbox.window.location.pathname = '/vaults';
  ctx.guide._clearCoachmarkIfRouteChanged();

  assert.ok(!target.classList.contains('is-ori-coachmark'));
});

test('a coachmark survives a history change that stays on the same route', () => {
  const ctx = load({ route: '/agents', elements: guideEls() });
  const target = makeElement('newAgentBtn');
  ctx.registerSelector('#newAgentBtn', target);

  ctx.guide._applyCoachmark('new_agent');
  ctx.guide._clearCoachmarkIfRouteChanged();

  // Filters change the query string, not the route; the mark is still valid.
  assert.ok(target.classList.contains('is-ori-coachmark'));
});

test('a coachmark is cleared when its element is re-rendered away', () => {
  const ctx = load({ route: '/agents', elements: guideEls() });
  const target = makeElement('newAgentBtn');
  ctx.registerSelector('#newAgentBtn', target);
  ctx.guide._applyCoachmark('new_agent');

  // The collection re-rendered and this node is no longer in the document.
  ctx.sandbox.document.contains = () => false;
  ctx.guide._clearCoachmarkIfRouteChanged();

  assert.ok(!target.classList.contains('is-ori-coachmark'));
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

/* ---- universal panel: page context (FR16-FR21, FR46) -------------------------- */

test('context is derived from the page path for every workspace surface', () => {
  const { guide } = load();
  const cases = [
    ['/', 'home', '', ''],
    ['/workspaces', 'workspace_hub', '', ''],
    ['/workspaces/launch', 'workspace_detail', 'launch', ''],
    ['/workspaces/launch/canvas', 'workspace_canvas', 'launch', ''],
    ['/workspaces/launch/tasks/t-42', 'workspace_task', 'launch', 't-42'],
    ['/agents', 'app', '', '']
  ];
  for (const [path, surface, workspaceId, taskId] of cases) {
    const ctx = guide._contextFromRoute(path);
    assert.equal(ctx.surface, surface, `${path} surface`);
    assert.equal(ctx.workspaceId, workspaceId, `${path} workspace`);
    assert.equal(ctx.taskId, taskId, `${path} task`);
  }
});

test('the submitted context carries the normalized surface and a known origin', () => {
  const { guide } = load({ route: '/workspaces/launch/tasks/t-42' });
  const ctx = guide._collectContext();
  assert.equal(ctx.surface, 'workspace_task');
  assert.equal(ctx.page_path, '/workspaces/launch/tasks/t-42');
  assert.equal(ctx.workspace_id, 'launch');
  assert.equal(ctx.task_id, 't-42');
  assert.equal(ctx.origin, 'ask_ori_panel');
});

// A page-supplied workspace fills a gap the URL left. It must never override the
// workspace the user is demonstrably looking at (FR18).
test('page-supplied context never overrides a workspace the URL already names', () => {
  const { guide } = load({ route: '/workspaces/launch' });
  guide.setContext({ workspaceId: 'other-workspace' });
  assert.equal(guide._collectContext().workspace_id, 'launch');
});

test('page-supplied context fills the gap on a page without one', () => {
  const { guide } = load({ route: '/' });
  assert.equal(guide._collectContext().workspace_id, '');
  guide.setContext({ workspaceId: 'selected-on-map' });
  assert.equal(guide._collectContext().workspace_id, 'selected-on-map');
  // Clearing the selection clears the hint rather than stranding it.
  guide.setContext({ workspaceId: '' });
  assert.equal(guide._collectContext().workspace_id, '');
});

test('the visible context label names the current target', () => {
  const { guide } = load({ route: '/workspaces/launch' });
  assert.equal(guide._contextLabel(guide._collectContext()), 'Workspace: launch');

  const hub = load({ route: '/workspaces' });
  assert.equal(hub.guide._contextLabel(hub.guide._collectContext()), 'All workspaces');

  const task = load({ route: '/workspaces/launch/tasks/t-42' });
  assert.equal(task.guide._contextLabel(task.guide._collectContext()), 'Task: t-42');
});

test('the context label is repainted into the header', () => {
  const els = guideEls();
  els.oriGuideContext = makeElement('oriGuideContext');
  const { guide } = load({ route: '/workspaces/launch', elements: els });
  guide._refreshContextLabel();
  assert.equal(els.oriGuideContext.textContent, 'Workspace: launch');
});

/* ---- universal panel: intent dispatch (FR22-FR27) ----------------------------- */

// The guide owns navigation and says so itself; the client never guesses with a
// keyword list of its own.
test('a navigation answer is not escalated to routing', () => {
  const { guide } = load();
  assert.equal(guide._needsWorkRouting({ status: 'answered', topic_key: 'workspaces' }), false);
});

test('a work request and an honest miss both escalate to routing', () => {
  const { guide } = load();
  assert.equal(
    guide._needsWorkRouting({ status: 'answered', topic_key: 'workspace-manager' }),
    true,
    'the guide identifying work must escalate'
  );
  assert.equal(
    guide._needsWorkRouting({ status: 'unknown' }),
    true,
    'an honest miss must escalate rather than render as "I do not know"'
  );
});

test('a navigation question uses only the read-only guide endpoint', async () => {
  const els = guideEls();
  const ctx = load({ route: '/', elements: els });
  const calls = [];
  ctx.sandbox.fetch = url => {
    calls.push(url);
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ status: 'answered', topic_key: 'workspaces', answer: 'Here' })
    });
  };

  await ctx.guide.ask('where do workspaces live');

  assert.deepEqual(calls, ['/api/ori-guide']);
  assert.match(els.oriGuideReply.innerHTML, /Here/);
});

test('a work request escalates to routing and shows where it is going', async () => {
  const els = guideEls();
  const ctx = load({ route: '/workspaces/launch', elements: els });
  const calls = [];
  let routedBody = null;
  ctx.sandbox.fetch = (url, opts) => {
    calls.push(url);
    if (url === '/api/ori-guide') {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({ status: 'answered', topic_key: 'workspace-manager', answer: 'work' })
      });
    }
    routedBody = JSON.parse(opts.body);
    return Promise.resolve({
      ok: true,
      json: () =>
        Promise.resolve({
          intent: 'travel_planning',
          intent_label: 'travel planning',
          matched_agent: 'Travel Planner'
        })
    });
  };

  await ctx.guide.ask('plan the launch party');

  assert.deepEqual(calls, ['/api/ori-guide', '/api/home-assistant/route']);
  assert.equal(routedBody.context.workspace_id, 'launch', 'routing must receive the page context');
  assert.match(els.oriGuideReply.innerHTML, /travel planning/);
  assert.match(els.oriGuideReply.innerHTML, /Travel Planner/);
  // Classification is not execution (FR35).
  assert.match(els.oriGuideReply.innerHTML, /Nothing has run yet/);
});

test('a failed routing call says routing failed rather than faking a guide answer', async () => {
  const els = guideEls();
  const ctx = load({ route: '/', elements: els });
  ctx.sandbox.fetch = url => {
    if (url === '/api/ori-guide') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ status: 'unknown', answer: 'I do not have an answer' })
      });
    }
    return Promise.resolve({ ok: false, status: 503, json: () => Promise.resolve({}) });
  };

  await ctx.guide.ask('do something complicated');

  // It falls back to the guide's own honest miss, never to a fabricated result.
  assert.match(els.oriGuideReply.innerHTML, /I do not have an answer/);
});

// FR26: navigation must keep working when no model is configured. The guide path
// is deterministic, so an unconfigured model cannot take it down.
test('a navigation answer still renders when routing is unavailable entirely', async () => {
  const els = guideEls();
  const ctx = load({ route: '/', elements: els });
  ctx.sandbox.fetch = url => {
    if (url === '/api/ori-guide') {
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({ status: 'answered', topic_key: 'agents', answer: 'Agents live here' })
      });
    }
    return Promise.reject(new Error('no model configured'));
  };

  await ctx.guide.ask('where are my agents');
  assert.match(els.oriGuideReply.innerHTML, /Agents live here/);
});

/* ---- universal panel: close and reopen (FR13/FR45) ---------------------------- */

test('closing keeps the draft and reopening restores it', () => {
  const els = guideEls();
  const { guide } = load({ route: '/', elements: els });

  guide.open();
  els.oriGuideInput.value = 'half-written request';
  guide.close();
  assert.equal(els.oriGuideInput.value, 'half-written request', 'the draft must survive a close');

  els.oriGuideInput.value = '';
  guide.open();
  assert.equal(els.oriGuideInput.value, 'half-written request');
});

test('reopening does not re-greet over a reply the user came back to read', async () => {
  const els = guideEls();
  const ctx = load({ route: '/', elements: els });
  let asks = 0;
  ctx.sandbox.fetch = () => {
    asks += 1;
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ status: 'answered', topic_key: 'agents', answer: 'kept' })
    });
  };

  ctx.guide.open();
  await ctx.guide.ask('where are my agents');
  const afterAsk = asks;

  ctx.guide.close();
  ctx.guide.open();

  assert.equal(asks, afterAsk, 'reopening must not fire another request');
  assert.match(els.oriGuideReply.innerHTML, /kept/);
});

/* ---- acceptance matrix (FR16-FR39) ---------------------------------------------- */

// One representative case per fulfilment family, driven through the single
// composer. The point is not to re-test each backend — they have their own
// suites — but to prove that one composer reaches each of them, and that the
// safe/ambiguous cases stay safe.
function matrixSandbox({ route = '/', guide = {}, routed = {}, elements } = {}) {
  const els = elements || guideEls();
  const ctx = load({ route, elements: els });
  const seen = { guide: 0, route: 0, payloads: [] };

  ctx.sandbox.fetch = (url, opts) => {
    if (url === '/api/ori-guide') {
      seen.guide += 1;
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ status: 'unknown', ...guide })
      });
    }
    seen.route += 1;
    seen.payloads.push(JSON.parse(opts.body));
    return Promise.resolve({ ok: true, json: () => Promise.resolve(routed) });
  };

  return { ctx, els, seen };
}

const MATRIX = [
  {
    name: 'navigation/setup stays on the read-only guide path',
    route: '/',
    guide: { status: 'answered', topic_key: 'agents', answer: 'Agents live here' },
    expect: ({ els, seen }) => {
      assert.equal(seen.route, 0, 'navigation must not reach the routing contract');
      assert.equal(els.oriGuideReply.dataset.status, 'answered');
    }
  },
  {
    name: 'direct utility routes and names its agent',
    route: '/',
    routed: {
      intent: 'utility_direct',
      intent_label: 'daily utility',
      matched_agent: 'Utility Assistant'
    },
    expect: ({ els }) => {
      assert.match(els.oriGuideReply.innerHTML, /daily utility/);
      assert.match(els.oriGuideReply.innerHTML, /Utility Assistant/);
    }
  },
  {
    name: 'specialist handoff names the specialist without running it',
    route: '/',
    routed: {
      intent: 'email_check',
      intent_label: 'email triage',
      matched_agent: 'Email Assistant',
      routing_policy: 'specialist_required'
    },
    expect: ({ els }) => {
      assert.match(els.oriGuideReply.innerHTML, /Email Assistant/);
      assert.match(els.oriGuideReply.innerHTML, /Nothing has run yet/);
    }
  },
  {
    name: 'personal calendar routes to its own agent',
    route: '/',
    routed: {
      intent: 'calendar_check',
      intent_label: 'calendar or schedule',
      matched_agent: 'Calendar Ops'
    },
    expect: ({ els }) => assert.match(els.oriGuideReply.innerHTML, /Calendar Ops/)
  },
  {
    name: 'app launch routes without inventing a destination',
    route: '/',
    routed: {
      intent: 'app_launch',
      intent_label: 'app launch',
      suggested_agent_name: 'Desktop Launcher'
    },
    expect: ({ els }) => assert.match(els.oriGuideReply.innerHTML, /Desktop Launcher/)
  },
  {
    name: 'workspace creation offers a way to create rather than guessing one',
    route: '/',
    routed: {
      intent: 'workspace_create',
      intent_label: 'workspace creation',
      workspace_recommended: true,
      workspace_resolution: { state: 'no_fit' }
    },
    expect: ({ els }) => {
      assert.match(els.oriGuideReply.innerHTML, /Choose or create a workspace/);
      assert.match(els.oriGuideReply.innerHTML, /href="\/workspaces"/);
    }
  },
  {
    name: 'an ambiguous target offers the candidates instead of picking one',
    route: '/',
    routed: {
      intent: 'general_task',
      intent_label: 'general task',
      workspace_recommended: true,
      workspace_resolution: {
        state: 'ambiguous',
        candidates: [
          { id: 'launch', name: 'Launch' },
          { id: 'research', name: 'Research' }
        ]
      }
    },
    expect: ({ els }) => {
      assert.match(els.oriGuideReply.innerHTML, /More than one workspace/);
      assert.match(els.oriGuideReply.innerHTML, /Open Launch/);
      assert.match(els.oriGuideReply.innerHTML, /Open Research/);
      // It must not claim a target it has not got.
      assert.ok(!els.oriGuideReply.innerHTML.includes('Target:'));
    }
  },
  {
    name: 'a confidently resolved workspace is named before anything runs',
    route: '/',
    routed: {
      intent: 'general_task',
      intent_label: 'general task',
      workspace_recommended: true,
      workspace_resolution: {
        state: 'confident',
        selected_workspace_id: 'launch',
        selected_workspace_name: 'Launch'
      }
    },
    expect: ({ els }) => assert.match(els.oriGuideReply.innerHTML, /Target:.*Launch/)
  },
  {
    name: 'a workspace page request carries and shows its own context',
    route: '/workspaces/launch',
    routed: { intent: 'general_task', intent_label: 'general task' },
    expect: ({ els, seen }) => {
      assert.equal(seen.payloads[0].context.workspace_id, 'launch');
      assert.equal(seen.payloads[0].context.surface, 'workspace_detail');
      assert.match(els.oriGuideReply.innerHTML, /Target:/);
    }
  },
  {
    name: 'a task page carries its task id',
    route: '/workspaces/launch/tasks/t-42',
    routed: { intent: 'general_task', intent_label: 'general task' },
    expect: ({ seen }) => {
      assert.equal(seen.payloads[0].context.task_id, 't-42');
      assert.equal(seen.payloads[0].context.surface, 'workspace_task');
    }
  }
];

for (const row of MATRIX) {
  test(`acceptance matrix: ${row.name}`, async () => {
    const harness = matrixSandbox({ route: row.route, guide: row.guide, routed: row.routed });
    await harness.ctx.guide.ask('do the thing');
    row.expect(harness);
  });
}

// Whatever the family, classification alone never mutates anything: the panel
// only ever renders navigate actions it has validated (FR27/FR35).
test('no routed outcome produces a mutating control', async () => {
  for (const row of MATRIX) {
    const harness = matrixSandbox({ route: row.route, guide: row.guide, routed: row.routed });
    await harness.ctx.guide.ask('do the thing');
    const html = harness.els.oriGuideReply.innerHTML;
    for (const forbidden of [
      'data-ori-action="delete"',
      'data-ori-action="run"',
      'data-ori-action="execute"'
    ]) {
      assert.ok(!html.includes(forbidden), `${row.name} rendered ${forbidden}`);
    }
  }
});

/* ---- explicit overrides (FR24) ------------------------------------------------- */

test('explicit commands are recognized, ordinary prompts are not', () => {
  const { guide } = load();
  for (const text of ['/task ship it', '/ask what is this', '/note buy milk', '  /NOTE x  ']) {
    assert.equal(guide._isExplicitCommand(text), true, `${text} should be an explicit command`);
  }
  for (const text of [
    'task: ship it',
    'ask about the launch',
    'note that this is fine',
    'what/ask means',
    ''
  ]) {
    assert.equal(guide._isExplicitCommand(text), false, `${text} should not be a command`);
  }
});

// The bug this guards: "/ask what is a workspace" matches the guide's own
// workspace topic, so asking the guide first answered it as navigation and
// silently discarded the override.
test('an explicit command bypasses the guide entirely', async () => {
  const els = guideEls();
  const ctx = load({ route: '/workspaces/launch', elements: els });
  const calls = [];
  ctx.sandbox.fetch = url => {
    calls.push(url);
    return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
  };

  const submitted = [];
  ctx.sandbox.window.OriAskRouting = {
    submit: (prompt, options) => {
      submitted.push({ prompt, options });
      return Promise.resolve({ handled: true });
    }
  };

  await ctx.guide.ask('/ask what is a workspace');

  assert.deepEqual(calls, [], 'no guide or route request should be made');
  assert.equal(submitted.length, 1);
  assert.equal(
    submitted[0].prompt,
    '/ask what is a workspace',
    'the command must pass through intact'
  );
  assert.equal(submitted[0].options.routeContext.workspace_id, 'launch');
});

test('an explicit command off a workspace page explains instead of guessing', async () => {
  const els = guideEls();
  const ctx = load({ route: '/agents', elements: els });
  const calls = [];
  ctx.sandbox.fetch = url => {
    calls.push(url);
    return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
  };

  await ctx.guide.ask('/note remember this');

  assert.deepEqual(calls, [], 'it must not fall back to the guide');
  assert.equal(els.oriGuideReply.dataset.status, 'unavailable');
  assert.match(els.oriGuideReply.innerHTML, /inside a workspace/);
});

/* ---- work delegation (FR31/FR36) ----------------------------------------------- */

test('work is handed to the existing controller with the page context', async () => {
  const els = guideEls();
  const ctx = load({ route: '/workspaces/launch', elements: els });
  ctx.sandbox.fetch = () =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ status: 'answered', topic_key: 'workspace-manager' })
    });

  const submitted = [];
  ctx.sandbox.window.OriAskRouting = {
    submit: (prompt, options) => {
      submitted.push({ prompt, options });
      return Promise.resolve({ handled: true });
    }
  };

  await ctx.guide.ask('plan the launch');

  assert.equal(submitted.length, 1);
  assert.equal(submitted[0].prompt, 'plan the launch');
  assert.equal(submitted[0].options.routeContext.workspace_id, 'launch');
  // The controller renders into the panel's own activity host, so it must not
  // also pop its old modal.
  assert.equal(submitted[0].options.openThinkingModal, false);
  assert.equal(els.oriGuideReply.dataset.status, 'delegated');
});

// Handing over must not wait on completion: a confirmation may sit unanswered
// indefinitely, and the composer is where the user answers it.
test('delegation acknowledges immediately and leaves the composer usable', async () => {
  const els = guideEls();
  const ctx = load({ route: '/workspaces/launch', elements: els });
  ctx.sandbox.fetch = () =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ status: 'answered', topic_key: 'workspace-manager' })
    });

  // A controller that never settles, like a pending confirmation.
  ctx.sandbox.window.OriAskRouting = { submit: () => new ctx.sandbox.Promise(() => {}) };

  await ctx.guide.ask('do something that needs confirming');

  assert.equal(els.oriGuideReply.dataset.status, 'delegated');
  assert.equal(els.oriGuideSend.disabled, false, 'the composer must stay usable');
});

/* ---- context races (FR17/FR46) -------------------------------------------------- */

test('switching workspace mid-flight drops the answer for the old one', async () => {
  const els = guideEls();
  const ctx = load({ route: '/', elements: els });

  let resolveGuide;
  ctx.sandbox.fetch = () =>
    new ctx.sandbox.Promise(resolve => {
      resolveGuide = resolve;
    });

  ctx.guide.setContext({ workspaceId: 'workspace-a' });
  const pending = ctx.guide.ask('what is in here');

  // The user selects a different workspace before the answer lands.
  ctx.guide.setContext({ workspaceId: 'workspace-b' });
  assert.equal(els.oriGuideReply.dataset.status, 'context-changed');

  resolveGuide({
    ok: true,
    json: () => Promise.resolve({ status: 'answered', answer: 'ABOUT WORKSPACE A' })
  });
  await pending;

  assert.ok(
    !els.oriGuideReply.innerHTML.includes('ABOUT WORKSPACE A'),
    'a reply about the previous workspace must not render against the new one'
  );
});

test('a context change that keeps the same workspace does not disturb a request', async () => {
  const els = guideEls();
  const ctx = load({ route: '/workspaces/launch', elements: els });

  let resolveGuide;
  ctx.sandbox.fetch = () =>
    new ctx.sandbox.Promise(resolve => {
      resolveGuide = resolve;
    });

  const pending = ctx.guide.ask('what is in here');
  // Only the label changes; the target is the same workspace.
  ctx.guide.setContext({ label: 'Workspace: Launch' });
  assert.notEqual(els.oriGuideReply.dataset.status, 'context-changed');

  resolveGuide({
    ok: true,
    json: () => Promise.resolve({ status: 'answered', answer: 'STILL VALID' })
  });
  await pending;

  assert.match(els.oriGuideReply.innerHTML, /STILL VALID/);
});
