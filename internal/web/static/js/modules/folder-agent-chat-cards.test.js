import test from 'node:test';
import assert from 'node:assert/strict';

import {
  chatAppRoute,
  chatDestination,
  chatFollowUp,
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
  publishFolderAgentChat,
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
  // The stack of sibling cards, and the host's one persistent live region.
  assert.equal(hosts[0].children.length, 2);
  assert.equal(hosts[0].children[0].className, 'folder-agent-chat-stack');
  assert.deepEqual(parts(hosts[0].children[0]), ['receipt', 'follow-up']);
  assert.equal(hosts[0].children[1].attributes.role, 'status');
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

// ---- Group 2: the song question and the reading, each in its own card ----

const REAPER_READS =
  'REAPER reads tempo, length, track count, track names, markers and regions from each project file, and Ori keeps only those. Nothing is changed, and audio is not opened.';
const offer = (overrides = {}) => ({
  count: 5,
  noun: 'songs',
  format: 'REAPER',
  ask: 'Want me to learn the 5 songs?',
  reads: REAPER_READS,
  not_read: "1 Logic Pro song can't be read this way.",
  ...overrides
});
const firstTicket = {
  id: 't1',
  title: 'Open Album-1 and write a short note on its tracks.',
  started: false
};
const goal = { goal: 'Keep these songs moving toward release.', max_runes: 300 };
const TICKET = { next_ticket: firstTicket, actions: ['start_next', 'look_further'] };
const reading = (status, extra = {}) => ({
  status,
  line: status === 'running' ? 'Learning Album-2… (2 of 5)' : 'Learned 4 of 5 songs.',
  done: 2,
  total: 5,
  ...extra
});

// What the drawer would draw after the receipt, as step kinds ("ticket+update"
// is a ticket card with the update behind its disclosure).
const steps = raw =>
  chatFollowUp(folderAgentReportView(albums(raw))).cards.map(card =>
    card.details ? `${card.kind}+${card.details.kind}` : card.kind
  );

test('one follow-up at a time: every row of the sequencing table', () => {
  // The song question is pending: neither the goal nor the ticket is shown.
  assert.deepEqual(steps({ ...TICKET, songs_offer: offer(), goal_offer: goal }), ['question']);
  // Reading or writing up: only its progress, the next decision waits.
  assert.deepEqual(steps({ ...TICKET, learning: reading('running'), goal_offer: goal }), [
    'learning'
  ]);
  assert.deepEqual(steps({ ...TICKET, learning: reading('summarizing') }), ['learning']);
  // Finished (fully or partly): the actual result, then the goal, else the ticket.
  assert.deepEqual(steps({ ...TICKET, learning: reading('done'), goal_offer: goal }), [
    'learning',
    'goal'
  ]);
  assert.deepEqual(steps({ ...TICKET, learning: reading('done') }), ['learning', 'ticket']);
  // Not now (nothing left of the offer): straight to the next eligible decision.
  assert.deepEqual(steps({ ...TICKET }), ['ticket']);
  assert.deepEqual(steps({ ...TICKET, goal_offer: goal }), ['goal']);
  // An update alone is informational: it never blocks an available ticket...
  assert.deepEqual(steps({ ...TICKET, songs_offer: offer({ update: 'REAPER' }) }), [
    'ticket+update'
  ]);
  // ...and when nothing else is available it is the card.
  assert.deepEqual(steps({ songs_offer: offer({ update: 'REAPER' }) }), ['update', 'further']);
  // A reading that stopped keeps its reason reachable beside a goal or a ticket...
  const stopped = reading('stopped', { reason: 'No model is set.', action: 'choose_model' });
  assert.deepEqual(steps({ ...TICKET, learning: stopped, goal_offer: goal }), ['goal+learning']);
  assert.deepEqual(steps({ ...TICKET, learning: stopped }), ['ticket+learning']);
  // ...is the card when it is all there is...
  assert.deepEqual(steps({ learning: stopped }), ['learning', 'further']);
  // ...and when consent must be asked again the decision is presented once.
  const renewed = chatFollowUp(
    folderAgentReportView(albums({ ...TICKET, learning: stopped, songs_offer: offer() }))
  );
  assert.deepEqual(
    renewed.cards.map(card => card.kind),
    ['question']
  );
  assert.equal(renewed.cards[0].stopped.reason, 'No model is set.');
  // Look further stays when it is the only action; a started ticket is shown as
  // started; with nothing to say there is no card.
  assert.deepEqual(steps({}), ['further']);
  assert.deepEqual(steps({ next_ticket: { ...firstTicket, started: true }, actions: [] }), [
    'ticket'
  ]);
  assert.deepEqual(steps({ actions: [] }), []);
  assert.equal(chatFollowUp(null).cards.length, 0);
});

const cardOf = stack => stack.children.find(card => card.dataset.chatStep);
const labelsOf = card => card.querySelectorAll('button').map(button => button.textContent);

test('the question stands alone below the receipt, with the disclosure and two answers', () => {
  const doc = stubDocument();
  const stack = renderFolderAgentChatThread(
    folderAgentReportView(albums({ ...TICKET, songs_offer: offer(), goal_offer: goal })),
    doc,
    { fetchImpl: async () => okJSON([]) }
  );
  assert.deepEqual(parts(stack), ['receipt', 'follow-up']);
  const card = cardOf(stack);
  assert.equal(card.dataset.chatStep, 'question');
  assert.equal(card.querySelector('h4').textContent, 'Learn the 5 REAPER songs?');
  assert.equal(card.querySelector('.folder-agent-card__songs-reads').textContent, REAPER_READS);
  assert.equal(
    card.querySelector('.folder-agent-card__songs-not-read').textContent,
    "1 Logic Pro song can't be read this way."
  );
  assert.deepEqual(labelsOf(card), ['Learn 5 songs', 'Not now']);
  // Neither the ticket teaser nor its actions nor the goal while this is pending.
  const everything = stack.textContent;
  for (const hidden of ['First ticket', 'Open Album-1', 'Start this', 'Look further', goal.goal]) {
    assert.equal(
      everything.includes(hidden),
      false,
      `${hidden} is shown while the question is open`
    );
  }
});

test('the question names the format only when the host does, and counts in the right form', () => {
  const doc = stubDocument();
  const heading = raw => {
    const stack = renderFolderAgentChatThread(folderAgentReportView(albums(raw)), doc, {
      fetchImpl: async () => okJSON([])
    });
    return [cardOf(stack).querySelector('h4').textContent, labelsOf(cardOf(stack))[0]];
  };
  assert.deepEqual(heading({ songs_offer: offer({ format: '' }) }), [
    'Learn the 5 songs?',
    'Learn 5 songs'
  ]);
  assert.deepEqual(heading({ songs_offer: offer({ count: 1, noun: 'song', not_read: '' }) }), [
    'Learn the 1 REAPER song?',
    'Learn 1 song'
  ]);
  assert.deepEqual(heading({ songs_offer: offer({ count: 7, format: '' }) }), [
    'Learn the 7 songs?',
    'Learn 7 songs'
  ]);
});

test('every word the host supplied is drawn as text, never as markup', () => {
  const doc = stubDocument();
  const hostile = '<img src=x onerror=alert(1)>';
  const stack = renderFolderAgentChatThread(
    folderAgentReportView(
      albums({ songs_offer: offer({ format: hostile, reads: hostile, not_read: hostile }) })
    ),
    doc,
    { fetchImpl: async () => okJSON([]) }
  );
  const card = cardOf(stack);
  assert.equal(card.querySelector('h4').textContent, `Learn the 5 ${hostile} songs?`);
  assert.equal(card.querySelector('.folder-agent-card__songs-reads').textContent, hostile);
  // Nothing was parsed into elements: the only elements are the card's own.
  assert.equal(card.all().filter(node => node.tagName === 'IMG').length, 0);
});

// harness is a server for one workspace: it answers the view, records every
// request, and lets a test decide what each write does.
function harness(initial, { onPost } = {}) {
  const state = { view: initial, log: [], failGet: false, failPost: false };
  const fetchImpl = async (url, init = {}) => {
    const method = init.method || 'GET';
    state.log.push([method, String(url), init.body || '']);
    if (String(url).includes('/api/agents')) return okJSON([]);
    if (method === 'GET') {
      if (state.failGet) throw new Error('offline');
      return okJSON({ folder_agent: state.view });
    }
    if (state.failPost) throw new Error('offline');
    return onPost ? onPost(state, String(url), JSON.parse(init.body || '{}')) : okJSON({});
  };
  const posts = () => state.log.filter(([method]) => method === 'POST');
  const gets = () => state.log.filter(([method]) => method === 'GET');
  return { state, fetchImpl, posts, gets };
}
const refuse = (status, error) => ({ ok: false, status, json: async () => ({ error }) });
const until = async condition => {
  const deadline = Date.now() + 3000;
  while (!condition()) {
    if (Date.now() > deadline) assert.fail('timed out waiting');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
};

async function mountHarness(h) {
  resetFolderAgentChat();
  const doc = stubDocument();
  const host = doc.createElement('div');
  await mountWorkspaceFolderAgent(host, 'ws-albums', {
    doc,
    fetchImpl: h.fetchImpl,
    openPlace: false,
    pollMs: 5
  });
  const drawn = () => folderAgentChat('ws-albums').card(true, { presentation: 'chat' });
  return { doc, host, drawn };
}

test('opening, expanding and polling never send an answer; Not now sends one dismiss and reads nothing', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: (state, url, body) => {
      assert.match(url, /\/folder-agent\/ws-albums\/songs$/);
      assert.deepEqual(body, { answer: 'dismiss' });
      // The host records the answer and moves on: no offer, no reading.
      state.view = albums({ ...TICKET });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  // Restoring the view, opening the receipt details and redrawing send nothing.
  drawn().querySelector('[data-chat-control="setup-details"]').click();
  drawn();
  await new Promise(resolve => setTimeout(resolve, 40));
  assert.equal(h.posts().length, 0, 'a write was sent before any answer');

  const stack = drawn();
  stack.querySelector('[data-chat-control="dismiss_songs"]').click();
  // The pair is one decision: both answers quiet while it is sent.
  assert.equal(stack.querySelector('[data-chat-control="learn_songs"]').disabled, true);
  await until(() => h.posts().length === 1 && cardOf(drawn()).dataset.chatStep === 'ticket');
  assert.equal(h.posts().length, 1);
  assert.equal(h.posts().filter(([, url]) => url.endsWith('/songs')).length, 1);
  // Immediately the next eligible decision: the ticket, with its real title.
  const next = cardOf(drawn());
  assert.equal(
    next.querySelector('.folder-agent-card__next-ticket').textContent,
    firstTicket.title
  );
  assert.deepEqual(labelsOf(next), ['Start this ticket', 'Look further']);
  assert.equal(drawn().textContent.includes('Learn 5 songs'), false);
});

test('Learn 5 songs sends exactly one learn answer, however often it is pressed', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: (state, url, body) => {
      assert.deepEqual(body, { answer: 'learn' });
      state.view = albums({ learning: reading('running') });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  const first = drawn();
  const learn = first.querySelector('[data-chat-control="learn_songs"]');
  learn.click();
  learn.click();
  // A card redrawn while the answer is in flight must not offer another write.
  const redrawn = drawn();
  redrawn.querySelector('[data-chat-control="learn_songs"]')?.click();
  await until(() => cardOf(drawn()).dataset.chatStep === 'learning');
  assert.equal(h.posts().length, 1);
  const card = cardOf(drawn());
  assert.equal(card.querySelector('h4').textContent, 'Learning your songs');
  assert.equal(
    card.querySelector('.folder-agent-card__learning-line').textContent,
    'Learning Album-2… (2 of 5)'
  );
  // No question, no ticket while it reads.
  assert.equal(
    drawn()
      .querySelectorAll('button')
      .filter(b => b.dataset.chatControl !== 'setup-details').length,
    0
  );
  // The reading ends, so no poll timer outlives the test.
  h.state.view = albums({ ...TICKET, learning: reading('done') });
  await until(() => drawn().children.length === 3);
});

test('the reading moves through real states and ends with the actual, partial result', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: state => {
      state.view = albums({ learning: reading('running', { done: 1 }) });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  drawn().querySelector('[data-chat-control="learn_songs"]').click();
  const line = () =>
    cardOf(drawn())?.querySelector('.folder-agent-card__learning-line')?.textContent;
  await until(() => line() === 'Learning Album-2… (2 of 5)' || h.state.view.learning?.done === 1);
  // Progress moves with the host's own numbers.
  h.state.view = albums({
    learning: reading('running', { done: 3, line: 'Learning Album-4… (4 of 5)' })
  });
  await until(() => line() === 'Learning Album-4… (4 of 5)');
  const bar = cardOf(drawn()).querySelector('progress');
  assert.equal(bar.value, 3);
  // The line itself is not a live region (a redrawn one is missed); the thread
  // carries the words for the drawer's one persistent region instead.
  assert.equal(
    cardOf(drawn()).querySelector('.folder-agent-card__learning-line').attributes.role,
    undefined
  );
  assert.equal(drawn().dataset.announce, 'Learning Album-4… (4 of 5)');
  // Writing up what it learned: the line only, nothing else is asked.
  h.state.view = albums({
    ...TICKET,
    learning: reading('summarizing', { line: 'Writing up what I learned…' })
  });
  await until(() => line() === 'Writing up what I learned…');
  assert.equal(cardOf(drawn()).dataset.chatStep, 'learning');
  assert.equal(drawn().textContent.includes('Start this ticket'), false);
  // It ends partly: 4 of the 5 songs, the summary, the exception, then the goal.
  h.state.view = albums({
    goal_offer: goal,
    learning: reading('done', {
      line: 'Learned 4 of 5 songs.',
      summary: 'Mostly mid-tempo songs with a vocal on each.',
      summary_by: 'agent',
      not_read: "1 song couldn't be read."
    })
  });
  await until(() => drawn().children.length === 3);
  const [, result, decision] = drawn().children;
  assert.equal(result.dataset.chatStep, 'learning');
  assert.equal(result.querySelector('h4').textContent, 'What I learned');
  assert.equal(
    result.querySelector('.folder-agent-card__learning-line').textContent,
    'Learned 4 of 5 songs.'
  );
  assert.equal(
    result.querySelector('.folder-agent-card__learning-summary').textContent,
    'Mostly mid-tempo songs with a vocal on each.'
  );
  assert.equal(decision.dataset.chatStep, 'goal');
  assert.equal(h.posts().length, 1);
});

test('a refused answer keeps its error with the decision, refreshes the run, and offers the pair again', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: () => refuse(409, "The Home's song switch is off.")
  });
  const { drawn } = await mountHarness(h);
  const getsBefore = h.gets().length;
  drawn().querySelector('[data-chat-control="learn_songs"]').click();
  await until(() => h.posts().length === 1 && cardOf(drawn()).querySelector('[role="alert"]'));
  const card = cardOf(drawn());
  assert.equal(card.dataset.chatStep, 'question');
  assert.equal(card.querySelector('[role="alert"]').textContent, "The Home's song switch is off.");
  // The run was asked for again before another write was offered.
  assert.ok(h.gets().length > getsBefore, 'the run was not refreshed after the refusal');
  assert.equal(labelsOf(card).join('|'), 'Learn 5 songs|Not now');
  assert.equal(
    card.querySelectorAll('button').every(button => !button.disabled),
    true
  );
  assert.equal(h.posts().length, 1);
});

test('an answer that may or may not have landed is checked first; if that fails the pair stays quiet', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }));
  const { drawn } = await mountHarness(h);
  // The request is lost, and the run cannot be read either.
  h.state.failPost = true;
  h.state.failGet = true;
  drawn().querySelector('[data-chat-control="dismiss_songs"]').click();
  await until(() => cardOf(drawn()).querySelector('[role="alert"]'));
  const card = cardOf(drawn());
  assert.match(
    card.querySelector('[role="alert"]').textContent,
    /That did not go through\. Reload to see where this stands\./
  );
  assert.equal(
    card.querySelectorAll('button').every(button => button.disabled),
    true
  );
  // One attempt was made, then the run was asked for again (and failed): no
  // second write is possible from a redrawn card while the outcome is unknown.
  assert.equal(h.posts().length, 1);
  assert.ok(h.gets().length > 1, 'the run was not asked for again');
  drawn()
    .querySelectorAll('button')
    .filter(button => button.dataset.chatControl !== 'setup-details')
    .forEach(button => button.click());
  assert.equal(h.posts().length, 1);
});

// ---- Group 3: the right decision at the right time ----

const transcriptFor = (doc, messageID = 'm1') => {
  const transcript = doc.createElement('div');
  const row = doc.createElement('div');
  row.dataset.leadMessageId = messageID;
  transcript.append(row);
  return transcript;
};
const attach = (transcript, doc) =>
  attachFolderAgentChatCard(transcript, 'ws-albums', { doc, win: null });
const hostOf = transcript => transcript.querySelector('[data-folder-agent-chat-card]');
const ended = () => reading('done');
const stoppedReading = reading('stopped', {
  line: 'Learning stopped.',
  reason: 'No model is set for the app assistant.',
  action: 'retry'
});
const posted = h => h.posts().map(([, url, body]) => [url.split('/').pop(), JSON.parse(body)]);

test('a stopped reading or an update-only offer stays reachable beside the decision, behind one disclosure', async () => {
  const h = harness(albums({ ...TICKET, learning: stoppedReading }), {
    onPost: state => {
      state.view = albums({ ...TICKET, learning: reading('running') });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  const card = cardOf(drawn());
  // One primary prompt (the ticket); the recovery is not on screen yet.
  assert.equal(card.dataset.chatStep, 'ticket');
  assert.deepEqual(labelsOf(card), ['Start this ticket', 'Look further', 'Learning details']);
  assert.equal(card.textContent.includes('Try again'), false);
  assert.equal(card.textContent.includes('No model is set'), false);
  card.querySelector('[data-chat-control="learning-details"]').click();
  assert.ok(card.textContent.includes('Learning stopped.'));
  assert.deepEqual(labelsOf(card), [
    'Start this ticket',
    'Look further',
    'Learning details',
    'Try again'
  ]);
  card
    .querySelectorAll('button')
    .find(button => button.textContent === 'Try again')
    .click();
  await until(() => h.posts().length === 1 && cardOf(drawn()).dataset.chatStep === 'learning');
  assert.deepEqual(posted(h), [['songs', { answer: 'retry' }]]);
  h.state.view = albums({ ...TICKET, learning: ended() });
  await until(() => drawn().children.length === 3);

  // An update-only offer is informational too: the ticket stays available.
  const update = harness(albums({ ...TICKET, songs_offer: offer({ update: 'REAPER' }) }));
  const again = await mountHarness(update);
  const ticket = cardOf(again.drawn());
  assert.equal(ticket.dataset.chatStep, 'ticket');
  assert.deepEqual(labelsOf(ticket), ['Start this ticket', 'Look further', 'Plugin update']);
  ticket.querySelector('[data-chat-control="learning-details"]').click();
  assert.equal(ticket.querySelector('a').href, '/plugins');
  assert.equal(update.posts().length, 0);
});

test('with nothing to continue to, a stopped reading or an update is the card; renewed consent is asked once', async () => {
  const doc = stubDocument();
  const draw = raw =>
    renderFolderAgentChatThread(folderAgentReportView(albums(raw)), doc, {
      fetchImpl: async () => okJSON([])
    });
  const alone = draw({ learning: stoppedReading });
  assert.deepEqual(alone.children.map(card => card.dataset.chatStep).filter(Boolean), [
    'learning',
    'further'
  ]);
  assert.deepEqual(labelsOf(alone.children[1]), ['Try again']);
  assert.ok(alone.children[1].textContent.includes('Learning stopped.'));
  // Look further stays reachable, quietly, as the next card.
  assert.deepEqual(labelsOf(alone.children[2]), ['Look further']);
  const renewed = draw({ ...TICKET, learning: stoppedReading, songs_offer: offer() });
  assert.deepEqual(renewed.children.map(card => card.dataset.chatStep).filter(Boolean), [
    'question'
  ]);
  assert.ok(renewed.textContent.includes('No model is set for the app assistant.'));
  assert.equal(renewed.textContent.includes('First ticket'), false);
  const updateOnly = draw({ songs_offer: offer({ update: 'REAPER' }) });
  assert.deepEqual(updateOnly.children.map(card => card.dataset.chatStep).filter(Boolean), [
    'update',
    'further'
  ]);
});

// goalRun presses something on the goal card and waits for the host to accept.
async function goalRun(press) {
  const h = harness(albums({ ...TICKET, goal_offer: goal }), {
    onPost: state => {
      state.view = albums({ ...TICKET });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  assert.equal(cardOf(drawn()).dataset.chatStep, 'goal');
  assert.equal(
    drawn().textContent.includes('Start this ticket'),
    false,
    'the ticket waits for the goal'
  );
  press(drawn());
  await until(() => cardOf(drawn()).dataset.chatStep === 'ticket');
  return h;
}
const goalButton = (stack, id) => stack.querySelector(`[data-folder-agent-action="${id}"]`);

test('each goal route sends one goal answer and never starts a ticket; the ticket follows once the host accepts', async () => {
  const use = await goalRun(stack => goalButton(stack, 'use_goal').click());
  assert.deepEqual(posted(use), [['goal', { answer: 'use' }]]);
  const edited = await goalRun(stack => {
    goalButton(stack, 'edit_goal').click();
    stack.querySelector('input').value = 'Ship the EP by March.';
    goalButton(stack, 'use_goal').click();
  });
  assert.deepEqual(posted(edited), [['goal', { answer: 'use', goal: 'Ship the EP by March.' }]]);
  const later = await goalRun(stack => goalButton(stack, 'dismiss_goal').click());
  assert.deepEqual(posted(later), [['goal', { answer: 'dismiss' }]]);
  for (const h of [use, edited, later]) {
    assert.equal(h.state.log.filter(([, url]) => url.endsWith('/start-next')).length, 0);
  }
});

test('an edit in progress survives incidental redraws without taking focus, and Cancel forgets it', async () => {
  const h = harness(albums({ ...TICKET, goal_offer: goal }));
  const { doc, drawn } = await mountHarness(h);
  let stack = drawn();
  goalButton(stack, 'edit_goal').click();
  const input = stack.querySelector('input');
  input.value = 'My own words';
  input.listeners.input.forEach(listener => listener());
  // Someone else holds focus (the composer) while the thread redraws.
  const composer = doc.createElement('textarea');
  composer.focus();
  stack = drawn();
  assert.equal(stack.querySelector('input').value, 'My own words');
  assert.equal(doc.activeElement, composer, 'a redraw took focus from the composer');
  goalButton(stack, 'cancel_goal').click();
  stack = drawn();
  assert.equal(stack.querySelector('input'), null);
  assert.ok(goalButton(stack, 'use_goal'));
  // Editing again starts from the offer, not from the forgotten words.
  goalButton(stack, 'edit_goal').click();
  assert.equal(stack.querySelector('input').value, goal.goal);
});

test('the first ticket is started once, shown as started, and never offered to start again', async () => {
  const h = harness(albums({ ...TICKET }), {
    onPost: state => {
      state.view = albums({
        next_ticket: { ...firstTicket, started: true },
        actions: ['look_further']
      });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { drawn } = await mountHarness(h);
  const start = drawn().querySelector('[data-chat-control="start_next"]');
  start.click();
  start.click();
  await until(() => cardOf(drawn()).querySelector('h4').textContent === 'Ticket started');
  assert.deepEqual(posted(h), [['start-next', {}]]);
  const card = cardOf(drawn());
  assert.equal(
    card.querySelector('.folder-agent-card__next-ticket').textContent,
    firstTicket.title
  );
  assert.deepEqual(labelsOf(card), ['Look further']);
  // A reopened thread draws it from the saved report: started, no second start.
  assert.deepEqual(labelsOf(cardOf(drawn())), ['Look further']);
  assert.equal(h.posts().length, 1);
});

test('a late reply never draws over a newer report or another workspace', async () => {
  let release = null;
  const view = (at, extra = {}) =>
    albums({ updated_at: `2026-11-19T10:00:0${at}Z`, actions: ['look_further'], ...extra });
  const h = harness(view(1, { learning: reading('summarizing') }), {
    onPost: () =>
      new Promise(resolve => {
        release = resolve;
      })
  });
  const { host } = await mountHarness(h);
  const entry = () => folderAgentChat('ws-albums');
  // An answer goes out from the page's card; before it lands a newer report is drawn.
  host.querySelector('[data-folder-agent-action="look_further"]').click();
  h.state.view = view(3, { learning: reading('done') });
  await until(() => entry().raw.updated_at === '2026-11-19T10:00:03Z');
  // The late reply is older than what is on screen: it is dropped.
  release(okJSON({ folder_agent: view(2, { learning: reading('running') }) }));
  await settle();
  assert.equal(entry().raw.updated_at, '2026-11-19T10:00:03Z');
  assert.equal(entry().raw.learning.status, 'done');
  // A reply about another workspace is never drawn here, however new.
  host.querySelector('[data-folder-agent-action="look_further"]').click();
  release(
    okJSON({ folder_agent: { ...view(9), workspace_id: 'ws-other', workspace_name: 'Other' } })
  );
  await settle();
  assert.equal(entry().raw.workspace_id, 'ws-albums');
  assert.equal(entry().raw.updated_at, '2026-11-19T10:00:03Z');
});

test('reopening derives the step from the saved report; a replaced report or another thread takes the controls away', async () => {
  const h = harness(albums({ ...TICKET, songs_offer: offer() }));
  const { doc } = await mountHarness(h);
  const first = transcriptFor(doc);
  const second = transcriptFor(doc);
  attach(first, doc);
  attach(second, doc);
  // A second view of the workspace draws the same decision, and sends nothing.
  assert.equal(hostOf(first).textContent, hostOf(second).textContent);
  assert.ok(hostOf(first).textContent.includes('Learn the 5 REAPER songs?'));
  assert.equal(h.posts().length, 0);
  // A row from another thread is not this report's, whatever its message id.
  const other = transcriptFor(doc);
  other.children[0].dataset.leadSessionId = 'other-session';
  assert.equal(attach(other, doc), null);
  assert.equal(other.querySelectorAll('[data-folder-agent-chat-card]').length, 0);
  const right = transcriptFor(doc);
  right.children[0].dataset.leadSessionId = 's1';
  assert.ok(attach(right, doc));
  // A newer report replaces it: no stale control stays anywhere.
  publishFolderAgentChat(
    {
      workspaceID: 'ws-albums',
      raw: albums({ chat: { session_id: 's1', message_id: 'm2' } }),
      card: () => doc.createElement('article')
    },
    { win: null }
  );
  assert.equal(attach(first, doc), null);
  assert.equal(first.querySelectorAll('[data-chat-control="learn_songs"]').length, 0);
  assert.equal(attach(right, doc), null);
  assert.equal(right.querySelectorAll('[data-chat-control="learn_songs"]').length, 0);
});

test('focus comes back by control identity, even when two controls share a label', () => {
  resetFolderAgentChat();
  const doc = stubDocument();
  const transcript = transcriptFor(doc);
  const factory = () => {
    const root = doc.createElement('div');
    ['a', 'b'].forEach(step => {
      const card = doc.createElement('article');
      card.dataset.chatStep = step;
      const button = doc.createElement('button');
      button.textContent = 'Not now';
      button.dataset.chatControl = `dismiss_${step}`;
      card.append(button);
      root.append(card);
    });
    return root;
  };
  publishFolderAgentChat({ workspaceID: 'ws-albums', raw: albums(), card: factory }, { win: null });
  attach(transcript, doc);
  transcript.querySelector('[data-chat-control="dismiss_b"]').focus();
  publishFolderAgentChat(
    { workspaceID: 'ws-albums', raw: albums({ updated_at: 'later' }), card: factory },
    { win: null }
  );
  attach(transcript, doc);
  assert.equal(doc.activeElement.dataset.chatControl, 'dismiss_b');
});

test('after an answer focus follows to the next heading, or back to the control that failed; background redraws never move it', async () => {
  // Answered: the question's buttons are gone, focus goes to the next card's heading.
  const answered = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: state => {
      state.view = albums({ ...TICKET });
      return okJSON({ folder_agent: state.view });
    }
  });
  let mounted = await mountHarness(answered);
  let transcript = transcriptFor(mounted.doc);
  attach(transcript, mounted.doc);
  const dismiss = transcript.querySelector('[data-chat-control="dismiss_songs"]');
  dismiss.focus();
  dismiss.click();
  await until(() => folderAgentChat('ws-albums').raw.songs_offer === undefined);
  mounted.doc.activeElement = null; // the pressed button went quiet and lost focus
  attach(transcript, mounted.doc);
  assert.equal(mounted.doc.activeElement.tagName, 'H4');
  assert.equal(mounted.doc.activeElement.textContent, 'First ticket');

  // Failed: the same control gets focus back.
  const refused = harness(albums({ ...TICKET, songs_offer: offer() }), {
    onPost: () => refuse(409, 'Not right now.')
  });
  mounted = await mountHarness(refused);
  transcript = transcriptFor(mounted.doc);
  attach(transcript, mounted.doc);
  const learn = transcript.querySelector('[data-chat-control="learn_songs"]');
  learn.focus();
  learn.click();
  await until(() => {
    attach(transcript, mounted.doc);
    return transcript.querySelector('[role="alert"]');
  });
  assert.equal(mounted.doc.activeElement.dataset.chatControl, 'learn_songs');

  // Background: a poll redraws the card while the composer holds focus.
  const live = harness(albums({ learning: reading('running', { done: 1 }) }));
  mounted = await mountHarness(live);
  transcript = transcriptFor(mounted.doc);
  attach(transcript, mounted.doc);
  const composer = mounted.doc.createElement('textarea');
  composer.focus();
  live.state.view = albums({ learning: reading('running', { done: 3 }) });
  await until(() => folderAgentChat('ws-albums').raw.learning.done === 3);
  attach(transcript, mounted.doc);
  assert.equal(mounted.doc.activeElement, composer);
  live.state.view = albums({ learning: ended() });
  await until(() => folderAgentChat('ws-albums').raw.learning.status === 'done');
});

test('a status change is announced once from one live region, and the card a thread opens on is not news', async () => {
  const line = text => reading('running', { done: 2, line: text });
  const h = harness(albums({ learning: line('Learning Album-2… (2 of 5)') }));
  const { doc } = await mountHarness(h);
  const transcript = transcriptFor(doc);
  attach(transcript, doc);
  const live = transcript.querySelector('[data-folder-agent-chat-live]');
  assert.equal(live.attributes.role, 'status');
  assert.equal(live.textContent, '');
  // The reading moves on: said once.
  h.state.view = albums({
    learning: reading('running', { done: 3, line: 'Learning Album-4… (4 of 5)' })
  });
  await until(() => folderAgentChat('ws-albums').raw.learning.done === 3);
  attach(transcript, doc);
  assert.equal(live.textContent, 'Learning Album-4… (4 of 5)');
  // The same words redrawn (the record changed, the words did not): not said again.
  live.textContent = 'sentinel';
  h.state.view = albums({
    learning: reading('running', { done: 4, line: 'Learning Album-4… (4 of 5)' })
  });
  await until(() => folderAgentChat('ws-albums').raw.learning.done === 4);
  attach(transcript, doc);
  assert.equal(live.textContent, 'sentinel');
  // It ends: the result is said, and the region is the same node throughout.
  h.state.view = albums({ ...TICKET, learning: reading('done', { line: 'Learned 5 songs.' }) });
  await until(() => folderAgentChat('ws-albums').raw.learning.status === 'done');
  attach(transcript, doc);
  assert.equal(live.textContent, 'Learned 5 songs.');
  assert.equal(transcript.querySelector('[data-folder-agent-chat-live]'), live);
});

test('rendering, restoring and redrawing any state never sends a setup, song, goal or ticket action', async () => {
  const states = [
    albums({ ...TICKET, songs_offer: offer() }),
    albums({ ...TICKET, goal_offer: goal }),
    albums({ ...TICKET }),
    albums({ ...TICKET, learning: stoppedReading }),
    albums({ ...TICKET, songs_offer: offer({ update: 'REAPER' }) }),
    albums({ learning: stoppedReading }),
    albums({ learning: reading('running') }),
    albums({ learning: reading('summarizing') }),
    albums({ ...TICKET, learning: reading('done'), goal_offer: goal }),
    albums({})
  ];
  for (const raw of states) {
    const h = harness(raw);
    const { doc, drawn } = await mountHarness(h);
    const transcript = transcriptFor(doc);
    attach(transcript, doc);
    attach(transcriptFor(doc), doc);
    drawn()
      .querySelectorAll(
        '[data-chat-control="setup-details"], [data-chat-control="learning-details"]'
      )
      .forEach(toggle => toggle.click());
    await new Promise(resolve => setTimeout(resolve, 15));
    assert.equal(h.posts().length, 0, `a write was sent for ${JSON.stringify(Object.keys(raw))}`);
    // End any reading so no poll outlives the test.
    h.state.view = albums({ ...TICKET });
    await new Promise(resolve => setTimeout(resolve, 15));
  }
});

test('fields the host left out do not break the cards', () => {
  const doc = stubDocument();
  const draw = raw =>
    renderFolderAgentChatThread(folderAgentReportView(albums(raw)), doc, {
      fetchImpl: async () => okJSON([])
    });
  const bare = draw({ songs_offer: { ask: 'Want me to learn the 5 songs?', count: 5 } });
  const question = cardOf(bare);
  assert.equal(question.querySelector('h4').textContent, 'Learn the 5 songs?');
  assert.deepEqual(labelsOf(question), ['Learn 5 songs', 'Not now']);
  assert.equal(question.querySelector('.folder-agent-card__songs-reads'), null);
  assert.equal(cardOf(draw({ goal_offer: { goal: 'A goal.' } })).dataset.chatStep, 'goal');
  const quiet = cardOf(draw({ next_ticket: { title: 'Just a title' }, actions: [] }));
  assert.equal(quiet.querySelector('h4').textContent, 'First ticket');
  assert.deepEqual(labelsOf(quiet), []);
  // An offer with no sentence is no offer at all; Look further is what is left.
  assert.equal(cardOf(draw({ songs_offer: { count: 5 } })).dataset.chatStep, 'further');
});

test('focus goes to the card an answer revealed, not back up to a result the owner already read', async () => {
  // The result card sits above the goal; using the goal reveals the ticket below.
  const h = harness(albums({ ...TICKET, learning: reading('done'), goal_offer: goal }), {
    onPost: state => {
      state.view = albums({ ...TICKET, learning: reading('done') });
      return okJSON({ folder_agent: state.view });
    }
  });
  const { doc } = await mountHarness(h);
  const transcript = transcriptFor(doc);
  attach(transcript, doc);
  const use = goalButton(transcript, 'use_goal');
  use.focus();
  use.click();
  await until(() => folderAgentChat('ws-albums').raw.goal_offer === undefined);
  doc.activeElement = null; // the pressed button went quiet and lost focus
  attach(transcript, doc);
  assert.equal(doc.activeElement.tagName, 'H4');
  assert.equal(doc.activeElement.textContent, 'First ticket');
  assert.equal(h.posts().length, 1);
});

test('a redraw while the owner is on a card heading keeps focus on that heading', async () => {
  const h = harness(albums({ learning: reading('running', { done: 1 }) }));
  const { doc } = await mountHarness(h);
  const transcript = transcriptFor(doc);
  attach(transcript, doc);
  const heading = transcript.querySelector('[data-chat-target]');
  heading.focus();
  assert.equal(doc.activeElement.textContent, 'Learning your songs');
  // The reading moves on: a poll replaces the card, and the heading with it.
  h.state.view = albums({ learning: reading('running', { done: 3, line: 'Learning Album-4…' }) });
  await until(() => folderAgentChat('ws-albums').raw.learning.done === 3);
  attach(transcript, doc);
  assert.notEqual(doc.activeElement, heading, 'the old heading was not replaced');
  assert.equal(doc.activeElement.textContent, 'Learning your songs');
  assert.equal(doc.activeElement.dataset.chatTarget, '');
  h.state.view = albums({ learning: ended() });
  await until(() => folderAgentChat('ws-albums').raw.learning.status === 'done');
});
