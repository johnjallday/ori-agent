import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

const finished = {
  first_task: {
    recipe: 'software-assessment',
    state: 'finished',
    next_request: 'Add a health check endpoint.',
    folder_name: 'widget'
  }
};

// A workspace page opened at `search`, with a fetch whose answer the test
// controls, a window whose history records every replaceState, and a page that
// records every Give work opening instead of drawing one.
function setup(t, search, { answer = async () => finished, dialogOpen = false } = {}) {
  const saved = {
    window: globalThis.window,
    document: globalThis.document,
    localStorage: globalThis.localStorage,
    fetch: globalThis.fetch
  };
  const routes = [];
  const opened = [];
  const reads = [];
  const toasts = [];
  globalThis.document = {
    readyState: 'complete',
    getElementById: () => null,
    querySelector: selector => (dialogOpen && selector === 'dialog.give-work' ? {} : null),
    activeElement: null
  };
  globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
  globalThis.window = {
    location: {
      search,
      pathname: '/workspaces/widget',
      hash: '#kept',
      href: `http://localhost/workspaces/widget${search}#kept`
    },
    history: {
      state: { keep: 'state' },
      replaceState(state, _title, route) {
        routes.push({ state, route });
      }
    },
    Toast: { info: message => toasts.push(message) }
  };
  globalThis.fetch = async path => {
    reads.push(path);
    return answer(path);
  };
  const command = new WorkspaceCommandView(null);
  command.page = {
    workspaceId: 'ws-1',
    workspaceSlug: 'widget',
    workspace: { id: 'ws-1', name: 'Widget', agents: ['Writer'] },
    tasks: [],
    showAddTaskModal: options => {
      opened.push(options);
      return {};
    }
  };
  command.active = true;
  command.render = () => {};
  t.after(() => {
    globalThis.window = saved.window;
    globalThis.document = saved.document;
    globalThis.localStorage = saved.localStorage;
    globalThis.fetch = saved.fetch;
  });
  return { command, routes, opened, reads, toasts };
}

const ok = body => ({ ok: true, json: async () => body });

test('arriving takes the parameter off the address at once and keeps the rest', t => {
  const f = setup(t, '?mode=map&next=assessment', { answer: async () => ok(finished) });
  assert.equal(f.routes.length, 1, 'one history replacement, no new entry');
  assert.equal(f.routes[0].route, '/workspaces/widget?mode=map#kept');
  assert.deepEqual(f.routes[0].state, { keep: 'state' });
  assert.deepEqual(f.command._suggestedTicketArrival, { kind: 'assessment' });
});

test('an address without the arrival does nothing, however often the view refreshes', async t => {
  const f = setup(t, '?mode=map');
  assert.equal(f.command._suggestedTicketArrival, null);
  assert.equal(f.routes.length, 0);
  await f.command.consumeSuggestedTicketArrival();
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.reads, []);
  assert.deepEqual(f.opened, []);
});

test('the suggestion opens Give work once with plain text; a second refresh does not open it again', async t => {
  const f = setup(t, '?next=assessment', { answer: async () => ok(finished) });
  await f.command.consumeSuggestedTicketArrival();
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.reads, ['/api/workspaces/ws-1/folder-first-task']);
  assert.equal(f.opened.length, 1);
  assert.equal(f.opened[0].what, 'Add a health check endpoint.');
  assert.equal(f.opened[0].details, 'Suggested by the first assessment of widget.');
  // The view hands the page no assignee and no start: it opens a form only.
  assert.equal('assignee' in f.opened[0], false);
  assert.equal('startNow' in f.opened[0], false);
  assert.equal(f.command._suggestedTicketArrival, null, 'consumed');
});

test('nothing happens until the workspace has loaded, then it still happens once', async t => {
  const f = setup(t, '?next=assessment', { answer: async () => ok(finished) });
  const page = f.command.page;
  f.command.page = { ...page, workspace: null };
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.reads, []);
  assert.deepEqual(f.command._suggestedTicketArrival, { kind: 'assessment' });
  f.command.page = page;
  await f.command.consumeSuggestedTicketArrival();
  assert.equal(f.opened.length, 1);
});

test('no finished assessment opens nothing and says why', async t => {
  const f = setup(t, '?next=assessment', { answer: async () => ok({ first_task: null }) });
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.opened, []);
  assert.match(f.command.workNotice, /no finished assessment/);
  assert.match(f.toasts[0], /no finished assessment/);
});

test('a failed read opens nothing, says it could not be read, and is not retried by a refresh', async t => {
  const f = setup(t, '?next=assessment', {
    answer: async () => ({ ok: false, status: 500, json: async () => ({}) })
  });
  await f.command.consumeSuggestedTicketArrival();
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.opened, []);
  assert.equal(f.reads.length, 1);
  assert.match(f.command.workNotice, /could not be read/);
});

test('a Give work dialog already open is left exactly as the person has it', async t => {
  const f = setup(t, '?next=assessment', {
    answer: async () => ok(finished),
    dialogOpen: true
  });
  await f.command.consumeSuggestedTicketArrival();
  assert.deepEqual(f.opened, []);
  assert.match(f.command.workNotice, /already open/);
});

test('an answer that arrives after the workspace changed is dropped without a notice', async t => {
  let release;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  const f = setup(t, '?next=assessment', {
    answer: async () => {
      await gate;
      return ok(finished);
    }
  });
  const pending = f.command.consumeSuggestedTicketArrival();
  f.command.page = { ...f.command.page, workspaceId: 'ws-2', workspace: { id: 'ws-2' } };
  release();
  await pending;
  assert.deepEqual(f.opened, []);
  assert.equal(f.command.workNotice, undefined);
  assert.deepEqual(f.toasts, []);
});
