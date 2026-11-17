import test from 'node:test';
import assert from 'node:assert/strict';

import {
  attachFolderAgentChatCard,
  chatRefOf,
  chatShowsFolderAgentReport,
  FOLDER_AGENT_CHAT_EVENT,
  FOLDER_AGENT_CHAT_SHOWN_EVENT,
  folderAgentChat,
  folderAgentChatUnread,
  folderReportReload,
  folderReportReloadKey,
  markFolderAgentChatSeen,
  postFolderAgentChatSeen,
  publishFolderAgentChat,
  resetFolderAgentChat,
  syncFolderReport
} from './folder-agent-chat.js';

// A DOM just big enough for the module: a tree, dataset, attribute
// selectors ([attr] and [attr="value"]), after(), remove() and focus.
function fakeDoc() {
  const doc = { activeElement: null };
  const camel = name => name.replace(/^data-/, '').replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  const matches = (node, selector) =>
    [...selector.matchAll(/\[([a-z-]+)(?:="([^"]*)")?\]/g)].every(([, attr, value]) => {
      const have = attr.startsWith('data-') ? node.dataset[camel(attr)] : node.attributes[attr];
      return value === undefined ? have !== undefined : have === value;
    });
  const make = tag => {
    const node = {
      tagName: tag.toUpperCase(),
      className: '',
      textContent: '',
      dataset: {},
      attributes: {},
      children: [],
      parent: null,
      disabled: false,
      setAttribute(name, value) {
        if (name.startsWith('data-')) node.dataset[camel(name)] = String(value);
        else node.attributes[name] = String(value);
      },
      append(...kids) {
        kids.forEach(kid => {
          kid.remove();
          kid.parent = node;
          node.children.push(kid);
        });
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
      replaceChildren(...kids) {
        [...node.children].forEach(kid => kid.remove());
        node.append(...kids);
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

function fakeWin() {
  const events = [];
  return {
    events,
    CustomEvent: class {
      constructor(type, init) {
        this.type = type;
        this.detail = init?.detail;
      }
    },
    dispatchEvent(event) {
      events.push(event);
    }
  };
}

function transcriptWith(doc, ids) {
  const transcript = doc.createElement('div');
  ids.forEach(id => {
    const row = doc.createElement('div');
    row.dataset.leadMessageId = id;
    transcript.append(row);
  });
  return transcript;
}

const raw = (chat, state = 'reported') => ({ workspace_id: 'ws-1', state, chat });

function cardFactory(doc, label = 'Set it up') {
  let drawn = 0;
  const card = compact => {
    drawn += 1;
    const el = doc.createElement('article');
    el.dataset.compact = String(compact);
    const button = doc.createElement('button');
    button.textContent = label;
    el.append(button);
    return el;
  };
  card.count = () => drawn;
  return card;
}

test('chatRefOf names the report only when both its thread and message are known', () => {
  assert.equal(chatRefOf(null), null);
  assert.equal(chatRefOf(raw({ session_id: 's' })), null);
  assert.deepEqual(chatRefOf(raw({ session_id: 's', message_id: 'm', unread: true })), {
    sessionID: 's',
    messageID: 'm',
    unread: true,
    lead: ''
  });
  // The lead-in the drawer shows for the question's message rides on the ref.
  assert.equal(
    chatRefOf(raw({ session_id: 's', message_id: 'm', lead: '  I looked around Albums.  ' })).lead,
    'I looked around Albums.'
  );
});

const REF = { sessionID: 's', messageID: 'report', unread: true, lead: '' };

test('the thread reloads when it lacks the report, whichever way the chat was opened', () => {
  const base = { ref: REF, shownSessionID: 's', rowIDs: ['welcome'] };
  // The welcome is loaded, the question landed after: reload (finding 1.3).
  assert.equal(folderReportReload(base), 'reload');
  // Nothing remembered yet: the report's thread is loaded.
  assert.equal(folderReportReload({ ...base, shownSessionID: '' }), 'reload');
  assert.equal(folderReportReload({ ...base, rowIDs: [] }), 'reload');
  // Unread or not does not matter: a stale thread is wrong either way.
  assert.equal(folderReportReload({ ...base, ref: { ...REF, unread: false } }), 'reload');
  // The row is there: nothing to do.
  assert.equal(folderReportReload({ ...base, rowIDs: ['welcome', 'report'] }), 'none');
  // Another thread is shown (the owner started a new conversation): never change it.
  assert.equal(folderReportReload({ ...base, shownSessionID: 'other' }), 'none');
  // No report at all.
  assert.equal(folderReportReload({ ...base, ref: null }), 'none');
  assert.equal(folderReportReload({ ...base, ref: { sessionID: 's', messageID: '' } }), 'none');
  assert.equal(folderReportReload(), 'none');
});

test('a reply in flight or a pending confirmation waits, and a message reloaded for is not reloaded again', () => {
  const base = { ref: REF, shownSessionID: 's', rowIDs: ['welcome'] };
  assert.equal(folderReportReload({ ...base, busy: true }), 'defer');
  assert.equal(folderReportReload({ ...base, locked: true }), 'defer');
  // Nothing is owed when the row is already in the thread, busy or not.
  assert.equal(folderReportReload({ ...base, busy: true, rowIDs: ['report'] }), 'none');
  // Reloaded once and the transcript still lacks the row (it cut it off): no loop.
  assert.equal(folderReportReload({ ...base, attempted: folderReportReloadKey(REF) }), 'none');
  assert.equal(
    folderReportReload({ ...base, attempted: folderReportReloadKey({ ...REF, messageID: 'old' }) }),
    'reload',
    'a newer message is reloaded for'
  );
});

test('syncing reloads the same thread exactly, keeps nothing else, and remembers the message', async () => {
  const calls = [];
  const hydrate = async options => {
    calls.push(options);
    return true;
  };
  const done = await syncFolderReport({
    ref: REF,
    shownSessionID: 's',
    rowIDs: ['welcome'],
    hydrate
  });
  assert.deepEqual(calls, [{ force: true, exact: true }]);
  assert.deepEqual(done, {
    loaded: true,
    reloaded: true,
    deferred: false,
    attempted: folderReportReloadKey(REF)
  });
  // With no thread remembered, the load is not exact (a "New conversation" mark is respected).
  calls.length = 0;
  await syncFolderReport({ ref: REF, shownSessionID: '', rowIDs: [], hydrate });
  assert.deepEqual(calls, [{ force: true, exact: false }]);
  // Already there: no load, and the carried message is unchanged.
  calls.length = 0;
  const present = await syncFolderReport({
    ref: REF,
    shownSessionID: 's',
    rowIDs: ['report'],
    attempted: 'x',
    hydrate
  });
  assert.equal(calls.length, 0);
  assert.deepEqual(present, { loaded: true, reloaded: false, deferred: false, attempted: 'x' });
});

test('a reload that cannot run now or fails stays owed, and a reply ending can retry it', async () => {
  let loads = 0;
  const hydrate = async () => {
    loads += 1;
    return false;
  };
  const base = { ref: REF, shownSessionID: 's', rowIDs: ['welcome'], hydrate };
  for (const blocked of [{ busy: true }, { locked: true }]) {
    const result = await syncFolderReport({ ...base, ...blocked });
    assert.deepEqual(result, { loaded: false, reloaded: false, deferred: true, attempted: '' });
  }
  assert.equal(loads, 0, 'nothing is loaded over a reply or a pending confirmation');
  // A load that fails is owed again, and is not remembered as done.
  const failed = await syncFolderReport(base);
  assert.equal(loads, 1);
  assert.deepEqual(failed, { loaded: false, reloaded: false, deferred: true, attempted: '' });
  // The reply ended: the same call now loads.
  const retried = await syncFolderReport({ ...base, hydrate: async () => true });
  assert.equal(retried.loaded, true);
  assert.equal(retried.attempted, folderReportReloadKey(REF));
  // Without a way to load there is only the debt.
  assert.equal((await syncFolderReport({ ...base, hydrate: undefined })).deferred, true);
});

// A message row as the drawer draws it: a bubble holding an optional author line
// and the message's words as a text node.
function messageRow(doc, id, words, { author = '' } = {}) {
  const row = doc.createElement('div');
  row.dataset.leadMessageId = id;
  const bubble = doc.createElement('div');
  const nodes = [];
  if (author) {
    const line = doc.createElement('p');
    line.textContent = author;
    nodes.push(line);
  }
  nodes.push({ nodeType: 3, textContent: words });
  bubble.childNodes = nodes;
  row.append(bubble);
  row.firstElementChild = bubble;
  return row;
}

const wordsOf = row => row.firstElementChild.childNodes.at(-1).textContent;

test('while the question is asked the drawer shows the lead-in, and the stored words come back after', () => {
  resetFolderAgentChat();
  const doc = fakeDoc();
  const win = fakeWin();
  const lead = 'I looked around Albums. One question first:';
  const stored = `${lead}\n\nThis looks like a music folder.\n\nAnswer it in the card below.`;
  const transcript = doc.createElement('div');
  transcript.append(
    messageRow(doc, 'welcome', 'Hi, I’m Albums Manager.', { author: 'Albums Manager' }),
    messageRow(doc, 'q', stored, { author: 'Albums Manager' })
  );
  const card = cardFactory(doc);
  const publish = (chat, state = 'reported') =>
    publishFolderAgentChat({ workspaceID: 'ws-1', raw: raw(chat, state), card }, { win });
  const shown = () => win.events.filter(e => e.type === FOLDER_AGENT_CHAT_SHOWN_EVENT).length;

  publish({ session_id: 's', message_id: 'q', unread: true, lead });
  const row = attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(wordsOf(row), lead);
  assert.equal(
    row.firstElementChild.childNodes[0].textContent,
    'Albums Manager',
    'the author line stays'
  );
  assert.equal(
    wordsOf(transcript.children[0]),
    'Hi, I’m Albums Manager.',
    'other messages are untouched'
  );
  assert.equal(row.dataset.folderAgentStored, stored, 'the stored words stay on the row');
  assert.equal(shown(), 1);

  // Drawn again, nothing changes.
  attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(wordsOf(row), lead);
  assert.equal(row.dataset.folderAgentStored, stored);

  // The owner answered: the message is drawn as stored again, and a lead change
  // alone fires no second "card shown" event.
  publish({ session_id: 's', message_id: 'q', unread: false, lead: '' }, 'setting_up');
  attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(wordsOf(row), stored);
  assert.equal(row.dataset.folderAgentStored, undefined);
  assert.equal(shown(), 1);
});

test('the lead-in is plain text and goes back when a newer report moves the card on', () => {
  resetFolderAgentChat();
  const doc = fakeDoc();
  const win = fakeWin();
  const transcript = doc.createElement('div');
  transcript.append(
    messageRow(doc, 'q', 'stored words'),
    messageRow(doc, 'later', 'a newer message')
  );
  const card = cardFactory(doc);
  const publish = chat =>
    publishFolderAgentChat({ workspaceID: 'ws-1', raw: raw(chat), card }, { win });

  publish({
    session_id: 's',
    message_id: 'q',
    lead: 'I looked around <b>Albums</b>. One question first:'
  });
  const row = attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(wordsOf(row), 'I looked around <b>Albums</b>. One question first:');
  assert.equal(row.firstElementChild.innerHTML, undefined, 'never set as markup');
  assert.equal(row.firstElementChild.querySelector('b'), null);

  // A newer look's report is another message: the old row gets its words back.
  publish({ session_id: 's', message_id: 'later', lead: '' });
  attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(wordsOf(row), 'stored words');
  assert.equal(row.dataset.folderAgentStored, undefined);
  assert.equal(wordsOf(transcript.children[1]), 'a newer message');

  // A report with no message in this thread changes no row.
  publish({ session_id: 's', message_id: 'missing', lead: 'lead' });
  assert.equal(attachFolderAgentChatCard(transcript, 'ws-1', { doc, win }), null);
  assert.equal(wordsOf(transcript.children[0]), 'stored words');
});

test('the report card sits under its message, follows the report and leaves with it', () => {
  resetFolderAgentChat();
  const doc = fakeDoc();
  const win = fakeWin();
  const transcript = transcriptWith(doc, ['intro', 'report']);
  const card = cardFactory(doc);
  publishFolderAgentChat(
    {
      workspaceID: 'ws-1',
      raw: raw({ session_id: 's', message_id: 'report', unread: true }),
      card
    },
    { win }
  );
  assert.equal(win.events.at(-1).type, FOLDER_AGENT_CHAT_EVENT);

  const row = attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(row.dataset.leadMessageId, 'report');
  const host = transcript.children[2];
  assert.equal(host.className, 'folder-agent-chat-card');
  assert.equal(host.dataset.messageId, 'report');
  assert.equal(host.children[0].dataset.compact, 'true');
  assert.equal(win.events.at(-1).type, FOLDER_AGENT_CHAT_SHOWN_EVENT);

  // The same report again: nothing is redrawn.
  attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(card.count(), 1);

  // The report moved on (the owner pressed Set it up): the card is redrawn in
  // place, and focus comes back to the same button.
  host.children[0].children[0].focus();
  publishFolderAgentChat(
    {
      workspaceID: 'ws-1',
      raw: raw({ session_id: 's', message_id: 'report' }, 'setting_up'),
      card
    },
    { win }
  );
  attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(card.count(), 2);
  assert.equal(transcript.children.length, 3, 'one card, redrawn in place');
  assert.equal(doc.activeElement, host.children[0].children[0]);

  // A newer look's report is another message: the old card goes.
  publishFolderAgentChat(
    { workspaceID: 'ws-1', raw: raw({ session_id: 's', message_id: 'later' }), card },
    { win }
  );
  assert.equal(attachFolderAgentChatCard(transcript, 'ws-1', { doc, win }), null);
  assert.equal(transcript.querySelectorAll('[data-folder-agent-chat-card]').length, 0);
});

test('a report waits unread until the drawer shows it, and a new one waits again', () => {
  resetFolderAgentChat();
  const doc = fakeDoc();
  const win = fakeWin();
  const card = cardFactory(doc);
  const publish = (id, unread = true) =>
    publishFolderAgentChat(
      { workspaceID: 'ws-1', raw: raw({ session_id: 's', message_id: id, unread }), card },
      { win }
    );
  publish('m1');
  assert.equal(folderAgentChatUnread('ws-1'), true);
  markFolderAgentChatSeen('ws-1', 'other');
  assert.equal(folderAgentChatUnread('ws-1'), true, 'another message changes nothing');
  markFolderAgentChatSeen('ws-1', 'm1');
  assert.equal(folderAgentChatUnread('ws-1'), false);
  // A poll that still says unread does not bring the dot back.
  publish('m1');
  assert.equal(folderAgentChatUnread('ws-1'), false);
  publish('m2');
  assert.equal(folderAgentChatUnread('ws-1'), true);
  publish('m2', false);
  assert.equal(folderAgentChatUnread('ws-1'), false);
  assert.equal(folderAgentChat('ws-1').workspaceID, 'ws-1');
});

test('the chat holds the report only while the open drawer shows its card', () => {
  const shown = new Set();
  const doc = { querySelector: selector => (shown.has(selector) ? {} : null) };
  const selector =
    '#homeAssistantConversation [data-folder-agent-chat-card][data-workspace-id="ws-1"]';
  const panel = { open: true, suspended: false };
  const win = {
    PersonalAssistantPanel: { isOpen: () => panel.open, isSuspended: () => panel.suspended }
  };
  assert.equal(chatShowsFolderAgentReport('ws-1', { doc, win }), false);
  shown.add(selector);
  assert.equal(chatShowsFolderAgentReport('ws-1', { doc, win }), true);
  panel.suspended = true;
  assert.equal(chatShowsFolderAgentReport('ws-1', { doc, win }), false);
  panel.suspended = false;
  panel.open = false;
  assert.equal(chatShowsFolderAgentReport('ws-1', { doc, win }), false);
});

test('a stale thread keeps the report unread and the balloon open until the reload brings the message in', () => {
  resetFolderAgentChat();
  const doc = fakeDoc();
  const win = fakeWin();
  const transcript = doc.createElement('div');
  transcript.append(messageRow(doc, 'welcome', 'Hi, I’m Albums Manager.'));
  const open = {
    PersonalAssistantPanel: { isOpen: () => true, isSuspended: () => false }
  };
  const holds = () =>
    chatShowsFolderAgentReport('ws-1', {
      doc: {
        querySelector: s => transcript.querySelector(s.replace('#homeAssistantConversation ', ''))
      },
      win: open
    });
  const card = cardFactory(doc);
  publishFolderAgentChat(
    {
      workspaceID: 'ws-1',
      raw: raw({ session_id: 's', message_id: 'report', unread: true, lead: 'Lead:' }),
      card
    },
    { win }
  );

  // The thread was loaded before the question landed: no row, no card. The
  // report stays unread (nothing was in view to mark seen) and the map's
  // balloon is not folded away, because the chat does not show the card.
  assert.equal(attachFolderAgentChatCard(transcript, 'ws-1', { doc, win }), null);
  assert.equal(folderAgentChatUnread('ws-1'), true);
  assert.equal(holds(), false);
  const shownEvents = () => win.events.filter(e => e.type === FOLDER_AGENT_CHAT_SHOWN_EVENT).length;
  assert.equal(shownEvents(), 0);

  // The reload brings the message in and the card is drawn under it: the chat
  // now holds the plan, so the balloon folds to its line (it is told), and once
  // the message has been in view the report is no longer unread.
  transcript.append(messageRow(doc, 'report', 'stored question words'));
  const row = attachFolderAgentChatCard(transcript, 'ws-1', { doc, win });
  assert.equal(row.dataset.leadMessageId, 'report');
  assert.equal(shownEvents(), 1, 'the balloon is told the chat shows the card');
  assert.equal(holds(), true);
  assert.equal(folderAgentChatUnread('ws-1'), true, 'still unread until it has been in view');
  markFolderAgentChatSeen('ws-1', 'report');
  assert.equal(folderAgentChatUnread('ws-1'), false);
  assert.equal(holds(), true, 'seen does not unfold the balloon');
});

test('seen is told to the server for the exact message', async () => {
  const calls = [];
  const ok = await postFolderAgentChatSeen('ws 1', 'm1', async (url, init) => {
    calls.push([url, init.method, init.body]);
    return { ok: true };
  });
  assert.equal(ok, true);
  assert.deepEqual(calls, [
    ['/api/personal-assistant/folder-agent/ws%201/chat/seen', 'POST', '{"message_id":"m1"}']
  ]);
  assert.equal(await postFolderAgentChatSeen('', 'm1', async () => ({ ok: true })), false);
  assert.equal(
    await postFolderAgentChatSeen('ws', 'm', async () => {
      throw new Error('offline');
    }),
    false
  );
});
