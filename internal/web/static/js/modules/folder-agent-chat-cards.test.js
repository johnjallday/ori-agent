import test from 'node:test';
import assert from 'node:assert/strict';

import {
  chatAppRoute,
  chatDestination,
  chatPlacementLine,
  chatSetupOf
} from './folder-agent-chat-flow.js';
import {
  folderAgentReportView,
  mountWorkspaceFolderAgent,
  renderFolderAgentChatThread
} from './folder-agent-card.js';
import {
  attachFolderAgentChatCard,
  folderAgentChat,
  resetFolderAgentChat
} from './folder-agent-chat.js';

// A stub DOM for the drawer's cards: a tree with dataset, attributes, click
// listeners, hidden/disabled, and the selectors the module and these tests use
// (tag, .class, [attr], [attr="value"], compounds of those).
function stubDocument() {
  const doc = { activeElement: null };
  const camel = name => name.replace(/^data-/, '').replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  const matches = (node, selector) => {
    const tag = selector.match(/^[a-z][a-z0-9]*/i)?.[0];
    if (tag && node.tagName !== tag.toUpperCase()) return false;
    const classes = [...selector.matchAll(/\.([\w-]+)/g)].map(m => m[1]);
    if (!classes.every(name => node.className.split(' ').includes(name))) return false;
    return [...selector.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)].every(([, attr, value]) => {
      const have = attr.startsWith('data-') ? node.dataset[camel(attr)] : node.attributes[attr];
      return value === undefined ? have !== undefined : have === value;
    });
  };
  const make = tag => {
    const node = {
      tagName: String(tag).toUpperCase(),
      className: '',
      children: [],
      parent: null,
      dataset: {},
      attributes: {},
      listeners: {},
      hidden: false,
      disabled: false,
      value: '',
      _text: '',
      get textContent() {
        return node._text + node.children.map(child => child.textContent).join('');
      },
      set textContent(value) {
        node._text = String(value);
        node.children.forEach(child => (child.parent = null));
        node.children = [];
      },
      get isConnected() {
        return true;
      },
      setAttribute(name, value) {
        if (name.startsWith('data-')) node.dataset[camel(name)] = String(value);
        else node.attributes[name] = String(value);
      },
      getAttribute(name) {
        return node.attributes[name] ?? null;
      },
      append(...kids) {
        kids.forEach(kid => {
          kid.remove?.();
          kid.parent = node;
          node.children.push(kid);
        });
      },
      replaceChildren(...kids) {
        [...node.children].forEach(kid => kid.remove());
        node._text = '';
        node.append(...kids);
      },
      after(kid) {
        kid.remove();
        const list = node.parent.children;
        kid.parent = node.parent;
        list.splice(list.indexOf(node) + 1, 0, kid);
      },
      remove() {
        if (!node.parent) return;
        node.parent.children.splice(node.parent.children.indexOf(node), 1);
        node.parent = null;
      },
      addEventListener(type, fn) {
        (node.listeners[type] ||= []).push(fn);
      },
      click() {
        if (node.disabled) return;
        (node.listeners.click || []).forEach(fn => fn({ target: node }));
      },
      contains(other) {
        for (let at = other; at; at = at.parent) if (at === node) return true;
        return false;
      },
      all() {
        return node.children.flatMap(child => [child, ...child.all()]);
      },
      querySelectorAll(selector) {
        return node.all().filter(child => matches(child, selector));
      },
      querySelector(selector) {
        return node.querySelectorAll(selector)[0] || null;
      },
      focus() {
        doc.activeElement = node;
      }
    };
    return node;
  };
  doc.createElement = make;
  return doc;
}

const okJSON = body => ({ ok: true, status: 200, json: async () => body });
const settle = () => new Promise(resolve => setTimeout(resolve, 40));

// The server's view of the Albums folder once Set it up finished in a Home.
const albums = (overrides = {}) => ({
  workspace_id: 'ws-albums',
  workspace_name: 'Albums Manager',
  route: '/workspaces/ws-albums',
  folder_name: 'Albums',
  agent_name: 'Albums Manager',
  state: 'set_up',
  actions: ['look_further'],
  landing: {
    route: '/workspaces/home-1?mode=map&setup_projects=1&setup_beside=ws-albums',
    name: 'Music Production Home'
  },
  chat: { session_id: 's1', message_id: 'm1', unread: false },
  setup: {
    status: 'done',
    lines: [],
    receipt: {
      installed: ['Ori REAPER 1.4.0'],
      home: { id: 'home-1', name: 'Music Production Home', route: '/workspaces/home-1' },
      home_created: true,
      hired: ['Albums Manager'],
      listed: 5,
      listed_noun: 'songs',
      unreached: 1
    }
  },
  ...overrides
});

const view = (overrides, receipt) => {
  const raw = albums(overrides);
  if (receipt !== undefined) raw.setup = { ...raw.setup, receipt };
  return folderAgentReportView(raw);
};

const parts = stack => stack.children.map(card => card.dataset.folderAgentChatPart);
const rowsOf = stack =>
  stack
    .querySelectorAll('.folder-agent-card__receipt-row')
    .map(row => [
      row.dataset.kind,
      row.querySelector('.folder-agent-card__receipt-words').textContent
    ]);
const rowWords = (stack, kind) => rowsOf(stack).find(([k]) => k === kind)?.[1] ?? null;

test('a completed Home setup is recognised from structured facts, with or without a pending offer', () => {
  const before = view({ songs_offer: { count: 5, noun: 'songs', ask: 'Learn?', reads: 'r' } });
  const after = view({});
  const expected = {
    home: { name: 'Music Production Home', route: '/workspaces/home-1' },
    created: true
  };
  assert.deepEqual(chatSetupOf(before), expected);
  // Dismissal removes the offer; the placement facts are what still say "music".
  assert.deepEqual(chatSetupOf(after), expected);
});

test('names and copy never identify a music setup; only the placement does', () => {
  // A workspace named like the Albums folder without a Home receipt is not one.
  assert.equal(chatSetupOf(view({}, {})), null);
  assert.equal(chatSetupOf(view({ setup: { status: 'done', lines: [] } })), null);
  // A Home receipt on an unfinished or unrelated report is not a completed setup.
  const homed = albums().setup.receipt;
  assert.equal(chatSetupOf(view({ state: 'setting_up' })), null);
  assert.equal(
    chatSetupOf(view({ setup: { status: 'running', lines: [], receipt: homed } })),
    null
  );
  assert.equal(
    chatSetupOf(view({ setup: { status: 'stopped', lines: [], receipt: homed } })),
    null
  );
  assert.equal(chatSetupOf(view({ state: 'reported', setup: undefined })), null);
  assert.equal(chatSetupOf(null), null);
  // A group made of several workspaces is placed, but not in a Home.
  assert.equal(
    chatSetupOf(view({}, { group: true, members: [{ name: 'A', route: '/workspaces/a' }] })),
    null
  );
});

test('a legacy receipt without a landing or a created flag is still a completed setup', () => {
  const legacy = view({ landing: undefined }, { home: { name: 'Music Production Home' } });
  const setup = chatSetupOf(legacy);
  assert.deepEqual(setup, { home: { name: 'Music Production Home', route: '' }, created: false });
  assert.equal(
    chatPlacementLine(legacy, setup),
    'Albums Manager is set up in your Music Production Home.'
  );
  // Without a landing the link goes to the folder's own workspace.
  assert.deepEqual(chatDestination(legacy), {
    route: '/workspaces/ws-albums',
    name: 'Albums Manager'
  });
});

test('the placement sentence and the link follow the actual destination', () => {
  const made = view({});
  assert.equal(
    chatPlacementLine(made, chatSetupOf(made)),
    'Albums Manager is set up in your new Music Production Home.'
  );
  const reused = view({}, { ...albums().setup.receipt, home_created: false });
  assert.equal(
    chatPlacementLine(reused, chatSetupOf(reused)),
    'Albums Manager is set up in your Music Production Home.'
  );
  assert.deepEqual(chatDestination(made), {
    route: '/workspaces/home-1?mode=map&setup_projects=1&setup_beside=ws-albums',
    name: 'Music Production Home'
  });
  // Only the app's own pages are ever linked.
  assert.equal(chatAppRoute('https://evil.example/workspaces/x'), '');
  assert.equal(chatAppRoute('/workspaces//x'), '');
  assert.equal(chatAppRoute('/workspaces/x\\y'), '');
  assert.equal(chatAppRoute('/plugins'), '');
  assert.equal(
    chatDestination(view({ landing: { route: 'javascript:alert(1)' }, route: '' })),
    null
  );
});

test('the receipt is a short collapsed card; the details open on the keyboard and tell the truth', async () => {
  const doc = stubDocument();
  const reads = [];
  const fetchImpl = async url => {
    reads.push(url);
    return okJSON([]);
  };
  const stack = renderFolderAgentChatThread(view({}), doc, {
    fetchImpl,
    state: { open: new Set() }
  });
  const receipt = stack.children[0];
  assert.equal(receipt.dataset.folderAgentChatPart, 'receipt');
  assert.equal(
    receipt.querySelector('.folder-agent-card__chat-line').textContent,
    'Albums Manager is set up in your new Music Production Home.'
  );
  const link = receipt.querySelector('.folder-agent-card__open');
  assert.equal(link.textContent, 'Open Music Production Home');
  assert.equal(link.href, '/workspaces/home-1?mode=map&setup_projects=1&setup_beside=ws-albums');
  const toggle = receipt.querySelector('button');
  assert.equal(toggle.textContent, 'View setup details');
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  const panel = receipt.querySelector('.folder-agent-card__chat-details');
  assert.equal(toggle.getAttribute('aria-controls'), panel.id);
  assert.equal(panel.hidden, true);
  // Collapsed: no rows drawn, no agent list read.
  assert.deepEqual(rowsOf(stack), []);
  assert.deepEqual(reads, []);

  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert.equal(panel.hidden, false);
  assert.equal(rowWords(stack, 'installed'), 'Ori REAPER 1.4.0Plugins');
  assert.equal(rowWords(stack, 'created'), 'Music Production HomeOpen');
  assert.match(rowWords(stack, 'hired'), /^Albums ManagerEdit agentAgents$/);
  assert.equal(rowWords(stack, 'listed'), '5 songs on the shelf · 1 folder not reached yet');
  assert.equal(
    rowWords(stack, 'placed'),
    'Albums Manager inside your Music Production HomeOpen Albums Manager'
  );
  await settle();
  assert.deepEqual(reads, ['/api/agents/dashboard/list']);

  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  assert.equal(panel.hidden, true);
});

test('details leave out receipt fields the host did not report and never invent a row', () => {
  const doc = stubDocument();
  const bare = view(
    { landing: undefined },
    { home: { name: 'Music Production Home', route: '/workspaces/home-1' } }
  );
  const stack = renderFolderAgentChatThread(bare, doc, { fetchImpl: async () => okJSON([]) });
  stack.children[0].querySelector('button').click();
  assert.deepEqual(
    rowsOf(stack).map(([kind]) => kind),
    ['placed']
  );
  // Reused Home: nothing says "Created", nothing says anything was listed.
  assert.equal(rowWords(stack, 'created'), null);
  assert.equal(rowWords(stack, 'listed'), null);
});

test('an outstanding permission stays visible outside the collapsed details', () => {
  const doc = stubDocument();
  const stack = renderFolderAgentChatThread(view({ filing_permission_required: true }), doc, {
    fetchImpl: async () => okJSON([])
  });
  const receipt = stack.children[0];
  const note = receipt.querySelector('.folder-agent-card__degraded');
  assert.equal(note.textContent, 'One step left: allow filing.');
  assert.equal(receipt.querySelector('.folder-agent-card__chat-details').contains(note), false);
});

test('anything that is not a completed Home setup keeps the single card', () => {
  const doc = stubDocument();
  const keep = raw => renderFolderAgentChatThread(folderAgentReportView(raw), doc, {});
  assert.equal(keep(albums({ state: 'setting_up' })), null);
  assert.equal(
    keep(albums({ setup: { status: 'stopped', lines: [], receipt: albums().setup.receipt } })),
    null
  );
  assert.equal(keep(albums({ state: 'reported', setup: undefined, actions: ['set_up'] })), null);
  assert.equal(
    keep({
      workspace_id: 'ws-notes',
      workspace_name: 'Notes',
      state: 'reported',
      actions: ['look_further']
    }),
    null
  );
});

// publish mounts the workspace's own report so the drawer's card factory is the
// real one, and returns what the drawer would draw.
async function mounted(rawView) {
  resetFolderAgentChat();
  const doc = stubDocument();
  const host = doc.createElement('div');
  const fetchImpl = async url =>
    String(url).includes('/folder-agent/') ? okJSON({ folder_agent: rawView }) : okJSON([]);
  await mountWorkspaceFolderAgent(host, rawView.workspace_id, {
    doc,
    fetchImpl,
    openPlace: false,
    pollMs: 5
  });
  return { doc, host, entry: folderAgentChat(rawView.workspace_id) };
}

test('the drawer asks for the chat presentation; the Map and the page keep one compact or full card', async () => {
  const { entry, host } = await mounted(albums());
  const chat = entry.card(true, { presentation: 'chat' });
  assert.equal(chat.className, 'folder-agent-chat-stack');
  // The receipt, then the follow-up (the fixture still offers Look further).
  assert.deepEqual(parts(chat), ['receipt', 'follow-up']);
  // The Map's balloon calls card(true): the same compact single card as before.
  const balloon = entry.card(true);
  assert.match(balloon.className, /folder-agent-card--compact/);
  assert.equal(balloon.dataset.folderAgentChatPart, undefined);
  // The page's own host shows the full card.
  assert.match(host.children[0].className, /folder-agent-card--report/);
  assert.doesNotMatch(host.children[0].className, /compact/);
  // A report that is not a completed Home setup keeps the single card even in the drawer.
  const other = await mounted(
    albums({ state: 'reported', setup: undefined, actions: ['set_up', 'look_further'] })
  );
  const kept = other.entry.card(true, { presentation: 'chat' });
  assert.match(kept.className, /folder-agent-card--compact/);
});

test('one host sits under the matching message and holds the cards as siblings', async () => {
  const { doc, entry } = await mounted(albums());
  const transcript = doc.createElement('div');
  ['m0', 'm1', 'm2'].forEach(id => {
    const row = doc.createElement('div');
    row.dataset.leadMessageId = id;
    transcript.append(row);
  });
  const row = attachFolderAgentChatCard(transcript, 'ws-albums', { doc, win: null });
  assert.equal(row.dataset.leadMessageId, 'm1');
  attachFolderAgentChatCard(transcript, 'ws-albums', { doc, win: null });
  const hosts = transcript.querySelectorAll('[data-folder-agent-chat-card]');
  assert.equal(hosts.length, 1);
  assert.equal(hosts[0].dataset.messageId, 'm1');
  assert.equal(transcript.children[transcript.children.indexOf(row) + 1], hosts[0]);
  assert.equal(hosts[0].children.length, 1);
  assert.equal(hosts[0].children[0].className, 'folder-agent-chat-stack');
  assert.deepEqual(parts(hosts[0].children[0]), ['receipt', 'follow-up']);
  assert.ok(entry);
});

test('an opened receipt stays open while the same report is polled and closes for a newer one', async () => {
  resetFolderAgentChat();
  const doc = stubDocument();
  const host = doc.createElement('div');
  const running = (done, extra = {}) =>
    albums({
      learning: { status: 'running', line: `Learning… ${done}`, done, total: 5 },
      ...extra
    });
  let server = running(1);
  const fetchImpl = async url =>
    String(url).includes('/folder-agent/') ? okJSON({ folder_agent: server }) : okJSON([]);
  await mountWorkspaceFolderAgent(host, 'ws-albums', {
    doc,
    fetchImpl,
    openPlace: false,
    pollMs: 5
  });
  const entry = () => folderAgentChat('ws-albums');
  const toggle = () =>
    entry()
      .card(true, { presentation: 'chat' })
      .querySelector('[data-chat-control="setup-details"]');
  assert.equal(toggle().getAttribute('aria-expanded'), 'false');
  // The owner opens the details in the drawer's card.
  entry()
    .card(true, { presentation: 'chat' })
    .querySelector('[data-chat-control="setup-details"]')
    .click();
  assert.equal(toggle().getAttribute('aria-expanded'), 'true');
  // A poll of the same report (progress moved) redraws, and it is still open.
  server = running(3);
  await settle();
  assert.equal(entry().raw.learning.done, 3);
  assert.equal(toggle().getAttribute('aria-expanded'), 'true');
  // A newer report (another message) starts closed again.
  server = running(4, { chat: { session_id: 's1', message_id: 'm2', unread: true } });
  await settle();
  assert.equal(entry().raw.chat.message_id, 'm2');
  assert.equal(toggle().getAttribute('aria-expanded'), 'false');
  // Let the poll end so no timer outlives the test.
  server = albums({ chat: { session_id: 's1', message_id: 'm2', unread: true } });
  await settle();
});
