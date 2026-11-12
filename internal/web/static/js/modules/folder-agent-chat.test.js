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
  markFolderAgentChatSeen,
  postFolderAgentChatSeen,
  publishFolderAgentChat,
  resetFolderAgentChat
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
    unread: true
  });
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
