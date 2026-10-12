import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

import {
  FOLDER_PROPOSAL_EXPIRED,
  folderProposalView,
  renderFolderProposal
} from './personal-assistant-folder-proposal.js';

const explore = {
  kind: 'explore',
  selection_token: 'tok-1',
  folder_name: 'Song Projects',
  path: '/Users/riley/Music/Song Projects'
};
const link = {
  kind: 'link',
  selection_token: 'tok-2',
  folder_name: 'Stems',
  path: '/Users/riley/Music/Night Drive/Stems',
  workspace_id: 'ws-1',
  workspace_name: 'Night Drive'
};

test('an explore card says what is looked at, and a link card says where it lands', () => {
  const peek = folderProposalView(explore);
  assert.equal(peek.title, 'Explore Song Projects?');
  assert.equal(peek.confirm, 'Explore folder');
  assert.match(peek.detail, /^A read-only peek\. Nothing moves\./);
  assert.match(peek.detail, /not at what is inside the files/);
  assert.equal(peek.path, '/Users/riley/Music/Song Projects');
  assert.equal(peek.followUp, 'Take a look at Song Projects and answer my last message.');

  const linked = folderProposalView(link);
  assert.equal(linked.title, 'Link Stems to Night Drive?');
  assert.equal(linked.confirm, 'Link folder');
  assert.match(linked.detail, /read the files in this folder from Night Drive/);
  assert.equal(linked.workspaceId, 'ws-1');
  assert.equal(linked.done, 'Linked Stems to Night Drive.');
});

test('a folder outside the home folder carries its warning onto the card', () => {
  const note = 'This folder is outside your home folder. Check that it is the one you mean.';
  assert.equal(folderProposalView({ ...explore, warning_note: note }).warning, note);
  assert.equal(folderProposalView(explore).warning, '');
});

test('a proposal the card cannot honour draws nothing', () => {
  assert.equal(folderProposalView(null), null);
  assert.equal(folderProposalView({ ...explore, selection_token: '' }), null);
  assert.equal(folderProposalView({ ...explore, path: '' }), null);
  // A link has to know which workspace it links to.
  assert.equal(folderProposalView({ ...link, workspace_id: '' }), null);
});

// A document small enough to hold the card: elements, text, one listener each.
class Node {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.listeners = {};
    this.className = '';
    this.textContent = '';
    this.disabled = false;
    this.parent = null;
  }
  get ownerDocument() {
    return documentStub;
  }
  get firstElementChild() {
    return this.children[0] || null;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  append(...nodes) {
    for (const node of nodes) {
      node.parent = this;
      this.children.push(node);
    }
  }
  remove() {
    if (this.parent) this.parent.children = this.parent.children.filter(node => node !== this);
  }
  addEventListener(type, handler) {
    this.listeners[type] = handler;
  }
  querySelector(selector) {
    const wanted = selector.replace(/^\./, '');
    for (const child of this.children) {
      if (child.className.split(/\s+/).includes(wanted)) return child;
      const nested = child.querySelector(selector);
      if (nested) return nested;
    }
    return null;
  }
  find(className) {
    return this.querySelector(`.${className}`);
  }
}
const documentStub = { createElement: tag => new Node(tag) };

function replyRow() {
  const row = new Node('div');
  row.append(new Node('div'));
  return row;
}

test('one press confirms with the held selection and leaves the card as a receipt', async () => {
  const row = replyRow();
  const calls = [];
  const card = renderFolderProposal(row, explore, {
    explore: async view => calls.push(['explore', view.token]),
    link: async view => calls.push(['link', view.token])
  });
  assert.equal(card.attributes['aria-label'], 'Explore Song Projects?');
  assert.equal(
    card.find('personal-assistant-folder-proposal__path').textContent,
    explore.path,
    'the path is shown'
  );
  const actions = card.find('personal-assistant-folder-proposal__actions');
  assert.equal(actions.children.length, 1, 'one decision, one button');
  const button = actions.children[0];
  assert.equal(button.textContent, 'Explore folder');

  await button.listeners.click();
  assert.deepEqual(calls, [['explore', 'tok-1']]);
  assert.equal(card.find('personal-assistant-folder-proposal__actions'), null);
  assert.equal(card.dataset.state, 'done');
  assert.equal(
    card.find('personal-assistant-folder-proposal__status').textContent,
    'Looking at Song Projects.'
  );
});

test('a link card calls link, and a refused confirmation says why and can be retried', async () => {
  const row = replyRow();
  let attempts = 0;
  const card = renderFolderProposal(row, link, {
    link: async () => {
      attempts++;
      if (attempts === 1) throw new Error('That folder selection expired. Paste the path again.');
    }
  });
  const button = card.find('personal-assistant-folder-proposal__actions').children[0];
  const status = card.find('personal-assistant-folder-proposal__status');

  await button.listeners.click();
  assert.equal(status.textContent, 'That folder selection expired. Paste the path again.');
  assert.equal(button.disabled, false, 'the button comes back');
  assert.notEqual(card.dataset.state, 'done');

  await button.listeners.click();
  assert.equal(attempts, 2);
  assert.equal(status.textContent, 'Linked Stems to Night Drive.');
});

test('a failure with no sentence of its own still says something true', async () => {
  const card = renderFolderProposal(replyRow(), explore, {
    explore: async () => {
      throw new Error('');
    }
  });
  await card.find('personal-assistant-folder-proposal__actions').children[0].listeners.click();
  assert.equal(
    card.find('personal-assistant-folder-proposal__status').textContent,
    FOLDER_PROPOSAL_EXPIRED
  );
});

test('a reply that no longer belongs to the open conversation opens nothing', async () => {
  const calls = [];
  const card = renderFolderProposal(replyRow(), explore, {
    explore: async () => calls.push('explore'),
    isCurrent: () => false
  });
  await card.find('personal-assistant-folder-proposal__actions').children[0].listeners.click();
  assert.deepEqual(calls, []);
  assert.match(
    card.find('personal-assistant-folder-proposal__status').textContent,
    /Nothing was opened/
  );
});

test('drawing the card again replaces it rather than stacking a second one', () => {
  const row = replyRow();
  renderFolderProposal(row, explore, {});
  renderFolderProposal(row, link, {});
  const bubble = row.firstElementChild;
  assert.equal(bubble.children.length, 1);
  assert.equal(bubble.children[0].dataset.kind, 'link');
});

// The whole point of the card: the confirmation is a token. This module holds
// the path only to show it.
test('the module never sends anything anywhere', () => {
  const source = readFileSync(
    new URL('./personal-assistant-folder-proposal.js', import.meta.url),
    'utf8'
  );
  assert.doesNotMatch(source, /fetch\(|XMLHttpRequest|API\.post|sendBeacon/);
});
