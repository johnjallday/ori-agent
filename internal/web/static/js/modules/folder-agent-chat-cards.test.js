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
  return { doc, drawn };
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
  assert.equal(
    cardOf(drawn()).querySelector('.folder-agent-card__learning-line').attributes.role,
    'status'
  );
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
