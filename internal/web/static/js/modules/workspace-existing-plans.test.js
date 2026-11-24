import test from 'node:test';
import assert from 'node:assert/strict';
import {
  PLAN_PRESENCE_PATH,
  existingPlansView,
  planPresenceFromResponse,
  readPlanPresence,
  renderExistingPlans
} from './workspace-existing-plans.js';

const ok = body => ({ ok: true, json: async () => body });

test('only a clear, self-consistent answer is an answer', () => {
  assert.deepEqual(planPresenceFromResponse({ has_plans: false, active: 0, history: 0 }), {
    state: 'none',
    active: 0,
    history: 0
  });
  assert.deepEqual(planPresenceFromResponse({ has_plans: true, active: 2, history: 1 }), {
    state: 'present',
    active: 2,
    history: 1
  });
  // A history-only workspace still has plans.
  assert.equal(
    planPresenceFromResponse({ has_plans: true, active: 0, history: 3 }).state,
    'present'
  );
  // Anything else is unknown, never "none".
  for (const body of [
    null,
    undefined,
    {},
    'yes',
    { has_plans: 'true', active: 1, history: 0 },
    { active: 1, history: 0 },
    { has_plans: false, active: 2, history: 0 },
    { has_plans: true, active: 0, history: 0 }
  ])
    assert.equal(planPresenceFromResponse(body).state, 'unavailable', JSON.stringify(body));
});

test('the read asks for this workspace only, and never throws', async () => {
  const seen = [];
  const fetchImpl = async (path, options) => {
    seen.push({ path, signal: options.signal });
    return ok({ has_plans: true, active: 1, history: 0 });
  };
  const controller = new AbortController();
  const got = await readPlanPresence({ workspaceId: 'a/b', fetchImpl, signal: controller.signal });
  assert.equal(got.state, 'present');
  assert.deepEqual(
    seen.map(call => call.path),
    ['/api/workspaces/a%2Fb/plan-presence']
  );
  assert.equal(seen[0].signal, controller.signal);
  assert.equal(PLAN_PRESENCE_PATH('ws-1'), '/api/workspaces/ws-1/plan-presence');

  for (const failing of [
    async () => ({ ok: false, status: 500, json: async () => ({}) }),
    async () => {
      throw new Error('offline');
    },
    async () => ({ ok: true, json: async () => Promise.reject(new Error('bad json')) }),
    async () => null
  ])
    assert.equal(
      (await readPlanPresence({ workspaceId: 'ws', fetchImpl: failing })).state,
      'unavailable'
    );
  assert.equal((await readPlanPresence({ workspaceId: '', fetchImpl })).state, 'unavailable');
  assert.equal(
    (await readPlanPresence({ workspaceId: 'ws', fetchImpl: null })).state,
    'unavailable'
  );
});

test('a workspace with no plans shows nothing; one with plans shows a link to its list', () => {
  assert.deepEqual(existingPlansView({ state: 'none' }, { workspaceSlug: 'widget' }), {
    visible: false,
    kind: 'none'
  });
  // Not yet read: nothing, rather than a flash of a link that may not apply.
  assert.equal(existingPlansView(null, { workspaceSlug: 'widget' }).visible, false);
  assert.equal(existingPlansView({ state: 'loading' }, { workspaceSlug: 'widget' }).visible, false);

  const link = existingPlansView(
    { state: 'present', active: 1, history: 2 },
    { workspaceSlug: 'my widget' }
  );
  assert.equal(link.visible, true);
  assert.equal(link.kind, 'link');
  assert.equal(link.label, 'Existing plans');
  assert.equal(link.href, '/workspaces/my%20widget/plans');
  assert.equal(link.title, '3 plans (1 active, 2 in history)');
  assert.equal(
    existingPlansView({ state: 'present', active: 0, history: 1 }, { workspaceSlug: 'w' }).title,
    '1 plan (1 in history)'
  );
  // No slug, no honest link.
  assert.equal(
    existingPlansView({ state: 'present', active: 1 }, { workspaceSlug: '' }).visible,
    false
  );
});

test('a failed check is an explained gap with a retry, not a hidden link', () => {
  const view = existingPlansView({ state: 'unavailable' }, { workspaceSlug: 'widget' });
  assert.equal(view.visible, true);
  assert.equal(view.kind, 'unavailable');
  assert.match(view.message, /Could not check for existing plans/);
  assert.equal(view.retryLabel, 'Try again');
  assert.equal(
    existingPlansView({ state: 'unavailable' }, { workspaceSlug: '' }).visible,
    true,
    'the gap is shown even before the slug is known'
  );
});

// --- Rendering, against a minimal DOM ----------------------------------------

function fakeDoc() {
  const make = tag => ({
    tag,
    children: [],
    attributes: {},
    listeners: {},
    textContent: '',
    className: '',
    get firstChild() {
      return this.children[0] || null;
    },
    appendChild(child) {
      this.children.push(child);
      return child;
    },
    removeChild(child) {
      this.children = this.children.filter(item => item !== child);
    },
    setAttribute(name, value) {
      this.attributes[name] = value;
    },
    addEventListener(type, handler) {
      this.listeners[type] = handler;
    }
  });
  return { createElement: make, make };
}

test('a link renders as plain text in an anchor, and hides the area when there is none', () => {
  const doc = fakeDoc();
  const mount = doc.make('div');
  renderExistingPlans(
    mount,
    {
      visible: true,
      kind: 'link',
      label: 'Existing plans',
      href: '/workspaces/w/plans',
      title: '1 plan (1 active)'
    },
    { doc }
  );
  assert.equal(mount.hidden, false);
  assert.equal(mount.children.length, 1);
  const link = mount.children[0];
  assert.equal(link.tag, 'a');
  assert.equal(link.href, '/workspaces/w/plans');
  assert.equal(link.textContent, 'Existing plans');

  renderExistingPlans(mount, { visible: false, kind: 'none' }, { doc });
  assert.equal(mount.hidden, true);
  assert.equal(mount.children.length, 0, 'the old link does not linger');
});

test('the unavailable state renders its message and a working retry', () => {
  const doc = fakeDoc();
  const mount = doc.make('div');
  let retried = 0;
  renderExistingPlans(
    mount,
    {
      visible: true,
      kind: 'unavailable',
      message: 'Could not check for existing plans.',
      retryLabel: 'Try again'
    },
    { doc, onRetry: () => retried++ }
  );
  assert.equal(mount.hidden, false);
  const [note, retry] = mount.children;
  assert.match(note.textContent, /Could not check for existing plans/);
  assert.equal(retry.tag, 'button');
  assert.equal(retry.type, 'button');
  assert.equal(retry.textContent, 'Try again');
  retry.listeners.click();
  assert.equal(retried, 1);
});

test('rendering never needs a mount', () => {
  assert.doesNotThrow(() => renderExistingPlans(null, { visible: true, kind: 'link' }));
});
