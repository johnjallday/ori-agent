import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-hub-tickets.js', import.meta.url), 'utf8');
const ticket = (id, title = id) => ({
  id,
  title,
  state: 'ready',
  number: 1,
  version: 1,
  owning_workspace_id: 'ws-one'
});
const list = counts => ({ tickets: [], counts, count: 0, total: 0 });
function deferred() {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
}

// Exercise the public controller and a minimal row DOM. Full browser rendering
// is covered by the journeys; no private view state is exported for tests.
function harness(responder) {
  const events = [];
  const requests = [];
  class RowNode {
    constructor() {
      this.children = [];
      this.dataset = {};
      this.className = '';
    }
    get childNodes() {
      return this.children;
    }
    get firstElementChild() {
      return this.children[0];
    }
    setAttribute() {}
    addEventListener() {}
    appendChild(node) {
      node.parentElement = this;
      this.children.push(node);
    }
    contains(node) {
      return this === node || this.children.some(child => child.contains(node));
    }
    replaceChildren() {
      if (this.contains(document.activeElement)) document.activeElement = null;
      this.children = [];
    }
    matches(selector) {
      return selector.startsWith('.') && this.className.split(' ').includes(selector.slice(1));
    }
    closest() {
      return this.dataset.ticketId ? this : this.parentElement?.closest();
    }
    querySelector(selector) {
      if (selector.includes('data-ticket-id')) {
        const id = /data-ticket-id="([^"]+)"/.exec(selector)?.[1];
        return (
          this.children
            .find(child => child.dataset.ticketId === id)
            ?.querySelector('.ticket-card-open') || null
        );
      }
      return (
        this.children.find(child => child.matches(selector)) ||
        this.children.map(child => child.querySelector(selector)).find(Boolean) ||
        null
      );
    }
    focus() {
      document.activeElement = this;
    }
  }
  const nodes = {
    'workspace-detail-tickets-surface': {},
    hubTicketDetail: { hidden: true },
    hubTicketDetailTitle: { textContent: '' },
    hubTicketsList: new RowNode(),
    hubTicketsEmpty: { hidden: true, textContent: '' },
    hubTicketsCount: { textContent: '' },
    hubTicketsStatus: { textContent: '', dataset: {}, hidden: true }
  };
  const window = {
    currentWorkspaceId: 'ws-one',
    location: { search: '' },
    dispatchEvent: event => events.push(event),
    fetch: async (url, options = {}) => {
      requests.push({ url, method: options.method || 'GET' });
      const result = await responder(url, options);
      return {
        ok: result.ok !== false,
        status: result.status || 200,
        json: async () => result.body ?? result
      };
    }
  };
  class CustomEvent {
    constructor(type, { detail }) {
      this.type = type;
      this.detail = detail;
    }
  }
  const document = {
    readyState: 'loading',
    activeElement: null,
    createElement: () => new RowNode(),
    addEventListener() {},
    getElementById: id => nodes[id] || null
  };
  vm.runInNewContext(source, {
    window,
    document,
    CustomEvent,
    URLSearchParams,
    console,
    setTimeout,
    clearTimeout
  });
  const api = window.WorkspaceHubTickets;
  api.init();
  return { api, window, document, nodes, events, requests };
}
const snapshot = api => JSON.parse(JSON.stringify(api.getPresentationState()));

test('Tickets owns selection/filter presentation and history restore emits no synthetic navigation', async () => {
  const h = harness(url =>
    url.includes('/tickets/') ? ticket(url.split('/').at(-1)) : list({ ready: 1 })
  );
  await h.api.setFilterState('review');
  await h.api.openDetail('ws-one', 't-one');
  assert.deepEqual(snapshot(h.api), { ticket: 't-one', tickets: 'review' });
  const before = h.events.filter(event => event.type === 'ori:ticket-view').length;
  await h.api.applyPresentationState({ ticket: 't-two', tickets: 'ready,review' });
  assert.deepEqual(snapshot(h.api), { ticket: 't-two', tickets: 'ready,review' });
  assert.equal(h.events.filter(event => event.type === 'ori:ticket-view').length, before);
  assert.ok(h.requests.every(request => request.method === 'GET'));
});

test('a late list cannot publish older counts over the current query', async () => {
  const old = deferred();
  let reads = 0;
  const h = harness(() => (++reads === 1 ? old.promise : list({ ready: 2 })));
  const first = h.api.load();
  await h.api.load();
  old.resolve(list({ ready: 1 }));
  await first;
  const counts = h.events.filter(event => event.type === 'ori:ticket-counts');
  assert.equal(counts.length, 1);
  assert.equal(counts[0].detail.counts.ready, 2);
});

test('a late detail response cannot replace the selected ticket', async () => {
  const old = deferred();
  const h = harness(url => (url.endsWith('/old') ? old.promise : ticket('new', 'Current detail')));
  const first = h.api.openDetail('ws-one', 'old');
  await h.api.openDetail('ws-one', 'new');
  old.resolve(ticket('old', 'Stale private detail'));
  await first;
  assert.equal(snapshot(h.api).ticket, 'new');
  assert.match(h.nodes.hubTicketDetailTitle.textContent, /Current detail/);
  assert.doesNotMatch(h.nodes.hubTicketDetailTitle.textContent, /Stale/);
});

test('changing workspace clears selection and rejects the old workspace response', async () => {
  const old = deferred();
  const h = harness(url => (url.endsWith('/old') ? old.promise : list({ ready: 0 })));
  const first = h.api.openDetail('ws-one', 'old');
  h.window.currentWorkspaceId = 'ws-two';
  assert.equal(h.api.getPresentationState(), null);
  await h.api.load();
  old.resolve(ticket('old', 'Old workspace detail'));
  await first;
  assert.deepEqual(snapshot(h.api), { ticket: '', tickets: 'all' });
  assert.equal(h.nodes.hubTicketDetail.hidden, true);
  assert.equal(h.nodes.hubTicketDetailTitle.textContent, '');
});

test('background list refresh retains the exact focused ticket without stealing unrelated focus', async () => {
  const h = harness(() => ({ tickets: [ticket('one'), ticket('two')], count: 2, total: 2 }));
  await h.api.load();
  const selector = '[data-ticket-id="two"] .ticket-card-open';
  h.nodes.hubTicketsList.querySelector(selector).focus();
  await h.api.load();
  assert.equal(h.document.activeElement, h.nodes.hubTicketsList.querySelector(selector));
  const elsewhere = {};
  h.document.activeElement = elsewhere;
  await h.api.load();
  assert.equal(h.document.activeElement, elsewhere);
});

test('an unreadable list is unknown, not a zero-ticket empty state', async () => {
  const h = harness(() => ({ ok: false, status: 503, body: { error: 'Unavailable' } }));
  await h.api.load();
  assert.equal(h.nodes.hubTicketsCount.textContent, '—');
  assert.equal(h.nodes.hubTicketsEmpty.hidden, true);
  assert.match(h.nodes.hubTicketsStatus.textContent, /Could not load tickets/);
});
