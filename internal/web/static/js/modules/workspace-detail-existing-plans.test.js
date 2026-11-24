import { test } from 'node:test';
import assert from 'node:assert/strict';

// A DOM just real enough for the secondary "Existing plans" link: one mount,
// elements that record what was appended and clicked.
function makeElement(tag) {
  return {
    tag,
    children: [],
    listeners: {},
    attributes: {},
    textContent: '',
    className: '',
    hidden: true,
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
  };
}

const mount = makeElement('p');
mount.ownerDocument = { createElement: makeElement };

global.window = { workspaceDetail: null };
global.document = {
  createElement: makeElement,
  getElementById: id => (id === 'hubTicketsExistingPlans' ? mount : null)
};

const { WorkspaceDetailPage } = await import('./workspace-detail.js');

function pageFor(workspaceId, slug = 'widget') {
  const page = Object.create(WorkspaceDetailPage.prototype);
  page.workspaceId = workspaceId;
  page.workspaceSlug = slug;
  return page;
}

function reset() {
  mount.children = [];
  mount.hidden = true;
}

const answer = body => ({ ok: true, json: async () => body });
const withFetch = async (impl, run) => {
  const saved = global.fetch;
  global.fetch = impl;
  try {
    return await run();
  } finally {
    global.fetch = saved;
  }
};

const shown = () => ({
  visible: !mount.hidden,
  tags: mount.children.map(child => child.tag),
  href: mount.children[0]?.href,
  text: mount.children.map(child => child.textContent).join('')
});

test('a workspace with plans gets the link under Tickets, scoped to its own slug', async () => {
  reset();
  const page = pageFor('ws-1', 'widget');
  const seen = [];
  await withFetch(
    async path => {
      seen.push(path);
      return answer({ has_plans: true, active: 1, history: 0 });
    },
    () => page.loadExistingPlansPresence()
  );
  assert.deepEqual(seen, ['/api/workspaces/ws-1/plan-presence']);
  assert.deepEqual(shown(), {
    visible: true,
    tags: ['a'],
    href: '/workspaces/widget/plans',
    text: 'Existing plans'
  });
});

test('a workspace with none, or a history-only one, is told apart', async () => {
  reset();
  const none = pageFor('ws-1');
  await withFetch(
    async () => answer({ has_plans: false, active: 0, history: 0 }),
    () => none.loadExistingPlansPresence()
  );
  assert.equal(shown().visible, false, 'no plans, no Plans destination');

  const historyOnly = pageFor('ws-2');
  await withFetch(
    async () => answer({ has_plans: true, active: 0, history: 2 }),
    () => historyOnly.loadExistingPlansPresence()
  );
  assert.equal(shown().visible, true, 'a history-only workspace keeps the way in');
  assert.equal(shown().tags[0], 'a');
});

test('a failed read is shown as unavailable with a retry that works, never as no plans', async () => {
  reset();
  const page = pageFor('ws-1');
  let calls = 0;
  const impl = async () => {
    calls += 1;
    if (calls === 1) return { ok: false, status: 503, json: async () => ({}) };
    return answer({ has_plans: true, active: 3, history: 0 });
  };
  await withFetch(impl, async () => {
    await page.loadExistingPlansPresence();
    assert.equal(shown().visible, true);
    assert.deepEqual(shown().tags, ['span', 'button']);
    assert.match(shown().text, /Could not check for existing plans/);
    // Pressing Try again reads again and replaces the gap with the link.
    mount.children[1].listeners.click();
    await new Promise(resolve => setImmediate(resolve));
  });
  assert.equal(calls, 2);
  assert.deepEqual(shown().tags, ['a']);
});

test('an answer for a workspace the page has left is dropped', async () => {
  reset();
  const page = pageFor('ws-old', 'old');
  let release;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  const pending = withFetch(
    async () => {
      await gate;
      return answer({ has_plans: true, active: 1, history: 0 });
    },
    () => page.loadExistingPlansPresence()
  );
  // The person moves to another workspace before the answer arrives.
  page.workspaceId = 'ws-new';
  page.workspaceSlug = 'new';
  release();
  assert.equal(await pending, null);
  assert.equal(
    shown().visible,
    false,
    'the old workspace answered; nothing is drawn for the new one'
  );
});

test('an older read cannot overwrite a newer one', async () => {
  reset();
  const page = pageFor('ws-1');
  const releases = [];
  const impl = () =>
    new Promise(resolve => {
      releases.push(body => resolve(answer(body)));
    });
  await withFetch(impl, async () => {
    const first = page.loadExistingPlansPresence();
    const second = page.loadExistingPlansPresence();
    // The newer read answers first (no plans); the older one then answers late
    // (has plans) and must not undo it.
    releases[1]({ has_plans: false, active: 0, history: 0 });
    assert.notEqual(await second, null);
    releases[0]({ has_plans: true, active: 1, history: 0 });
    assert.equal(await first, null);
  });
  assert.equal(shown().visible, false);
});

test('without a workspace there is nothing to ask', async () => {
  reset();
  const page = pageFor('', '');
  let asked = false;
  const result = await withFetch(
    async () => {
      asked = true;
      return answer({});
    },
    () => page.loadExistingPlansPresence()
  );
  assert.equal(result, null);
  assert.equal(asked, false);
});
