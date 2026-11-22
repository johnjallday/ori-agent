import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  confirmFilingExpansion,
  filingExpansionView,
  filingPurchaseNotice,
  renderFilingExpansion
} from './filing-expansion.js';

const viewOf = (quote, missions) => filingExpansionView({ filing_expansion: quote, missions });
const quote = { state: 'affordable', base_cost: 7, effective_cost: 7, balance: 7 };
const short = {
  ...quote,
  state: 'insufficient',
  balance: 2,
  manual_earn_url: '/workspaces/hq?panel=tasks'
};
const free = '/?quest=show-folder';

function node() {
  return {
    dataset: {},
    children: [],
    append(item) {
      this.children.push(item);
    },
    replaceChildren(...children) {
      this.children = children;
    }
  };
}

function offerBox() {
  const roles = new Map();
  return {
    ...node(),
    ownerDocument: { createElement: node },
    querySelector(selector) {
      if (!roles.has(selector)) roles.set(selector, node());
      return roles.get(selector);
    }
  };
}

test('no build offer until real HQ, and the economy off hides only the expansion', () => {
  assert.equal(viewOf({ state: 'needs_hq' }).visible, false);
  assert.equal(viewOf({ state: 'disabled' }).visible, false);
  const available = viewOf(quote);
  assert.match(available.detail, /Choose Downloads or another folder later/);
  assert.match(available.detail, /separately/);
  assert.equal(available.price, '7 Craft to build.');
  assert.equal(available.free.href, free);
  assert.equal(
    viewOf({ ...quote, effective_cost: 0, balance: 0, creative: true }).price,
    '0 Craft in Creative Mode (normally 7).'
  );
});

test('short balance names a reachable paying mission or the manual ticket fallback', () => {
  const missions = [
    { locked: true, reward_craft: 7, title: 'Locked', action_url: '/locked' },
    { status: 'completed', reward_craft: 7, title: 'Already done', action_url: '/done' },
    { in_progress: true, reward_craft: 7, title: 'Running', action_url: '/running' },
    { reward_craft: 0, title: 'Unpaid', action_url: '/unpaid' },
    { reward_craft: 7, title: 'Show your assistant a folder', action_url: free }
  ];
  const view = viewOf(short, missions);
  assert.equal(view.message, 'You need 5 more Craft.');
  assert.equal(view.earn.href, free);
  assert.equal(view.action, undefined);
  assert.equal(view.earn.label, 'Earn Craft: Show your assistant a folder');
  assert.equal(viewOf(short, missions.slice(0, -1)).earn.href, '/workspaces/hq?panel=tasks');
});

test('unknown balance is not zero or a quote; reopening a Filing workspace is free', () => {
  const unknown = viewOf({ state: 'unavailable' });
  assert.equal(unknown.balance, '');
  assert.equal(unknown.price, undefined);
  assert.equal(unknown.free.href, free);
  const destinations = [
    { name: 'Inbox', route: '/workspaces/inbox?panel=file-janitor' },
    { name: 'Scans', route: '/workspaces/scans?panel=file-janitor' }
  ];
  const built = viewOf({ state: 'already_built', destinations });
  assert.equal(built.title, 'Choose a Filing workspace');
  assert.deepEqual(built.destinations, destinations);
  assert.equal(built.price, undefined);
  assert.equal(
    viewOf({ state: 'already_built', destinations: [{ route: '//other.example/' }] }).destinations,
    undefined
  );
});

test('the same card updates as HQ and the ledger change, without replacing unchanged links', () => {
  const box = offerBox();
  const render = q => renderFilingExpansion(box, viewOf(q), () => {}, '/?quest=folder-first-look');
  const balance = box.querySelector('[data-role="expansion-price"]');
  render({ state: 'needs_hq' });
  assert.equal(box.hidden, true);
  render(short);
  assert.equal(box.hidden, false);
  assert.match(balance.textContent, /need 5 more/);
  render(quote);
  assert.equal(balance.textContent, '7 Craft to build. You have 7 Craft.');
  assert.equal(box.querySelector('[data-role="expansion-earn"]').hidden, true);
  render({
    state: 'already_built',
    destinations: [{ name: 'Inbox', route: '/workspaces/inbox?panel=file-janitor' }]
  });
  const list = box.querySelector('[data-role="expansion-destinations"]');
  const [first] = list.children;
  assert.equal(first.children[0].textContent, 'Open Inbox');
  render({
    state: 'already_built',
    balance: 4,
    destinations: [{ name: 'Inbox', route: '/workspaces/inbox?panel=file-janitor' }]
  });
  assert.equal(list.children[0], first);
});

test('an explicit confirmation sends only the displayed price; decline and short balance do not submit', async () => {
  const requests = [];
  let prompt = '';
  const dependencies = {
    ask(message) {
      prompt = message;
      return false;
    },
    newID: () => 'fixture-request',
    request: body => requests.push(body)
  };
  await confirmFilingExpansion(viewOf(quote).action, dependencies);
  assert.match(prompt, /for 7 Craft/);
  assert.match(prompt, /does not grant access/);
  assert.deepEqual(requests, []);
  await confirmFilingExpansion(viewOf(short).action, dependencies);
  assert.deepEqual(requests, []);
  dependencies.ask = () => true;
  await confirmFilingExpansion(viewOf(quote).action, dependencies);
  assert.deepEqual(requests, [{ request_id: 'fixture-request', cost: 7, creative: false }]);
});

test('pending recovery sends a check only and renders real receipt or removal', async () => {
  const pending = viewOf({ state: 'pending' });
  const pendingResult = await confirmFilingExpansion(pending.action, { request: body => body });
  assert.deepEqual(pendingResult, { check_only: true });
  const box = offerBox();
  renderFilingExpansion(box, pending, () => {}, '', { busy: true });
  assert.equal(box.querySelector('[data-role="expansion-action"]').disabled, true);
  assert.match(filingPurchaseNotice('stale_price'), /Review the new price/);
  assert.match(filingPurchaseNotice('refunded'), /returned/);
  assert.equal(viewOf({ state: 'destination_unavailable' }).action, undefined);
  const built = viewOf({
    state: 'already_built',
    charged: 0,
    waived: 7,
    balance: 0,
    destinations: [{ name: 'Filing', route: '/workspaces/filing?panel=file-janitor' }]
  });
  assert.equal(built.price, '7 Craft waived in Creative Mode.');
  assert.equal(built.balance, 'You have 0 Craft.');
});
