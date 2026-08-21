// Tests for github-proposals.js — the panel that shows GitHub changes awaiting
// approval. Inline DOM stub, no jsdom.
//
// Run with: node --test internal/web/static/js/modules/github-proposals.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.hidden = false;
    this._text = '';
    this.className = '';
    this.disabled = false;
    this.type = '';
    this.href = '';
    this.children = [];
    this.dataset = {};
    this._listeners = {};
  }
  get textContent() {
    if (this.children.length) {
      return this._text + this.children.map(c => c.textContent).join(' ');
    }
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v === '') this.children = [];
  }
  set innerHTML(v) {
    if (v === '') this.children = [];
  }
  get innerHTML() {
    return this.children.length ? '<children>' : '';
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  append(...nodes) {
    nodes.forEach(n => this.children.push(n));
  }
  insertBefore(node, ref) {
    const i = this.children.indexOf(ref);
    if (i < 0) this.children.push(node);
    else this.children.splice(i, 0, node);
    return node;
  }
  addEventListener(name, fn) {
    (this._listeners[name] = this._listeners[name] || []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  // Depth-first walk over this subtree.
  all() {
    return this.children.flatMap(c => [c, ...c.all()]);
  }
  querySelectorAll(selector) {
    if (selector === 'button') return this.all().filter(n => n.tagName === 'BUTTON');
    const cls = selector.replace('.', '');
    return this.all().filter(n => String(n.className).split(/\s+/).includes(cls));
  }
  querySelector(selector) {
    const attr = selector.match(/^\[data-proposal-id="(.*)"\]$/);
    if (attr) return this.all().find(n => n.dataset.proposalId === attr[1]) || null;
    return this.querySelectorAll(selector)[0] || null;
  }
}

function setup() {
  const mount = new FakeElement('div');
  globalThis.document = {
    createElement: tag => new FakeElement(tag),
    getElementById: id => (id === 'githubProposalsMount' ? mount : null),
    addEventListener() {},
    readyState: 'complete'
  };
  globalThis.window = globalThis;
  globalThis.window.location = { pathname: '/workspaces/readable-slug' };
  globalThis.window.currentWorkspaceId = 'ws-1';
  globalThis.CSS = { escape: v => v };
  return mount;
}

// calls records every fetch the panel makes, so tests can assert on the exact
// request that would reach the server.
function stubFetch(responses) {
  const calls = [];
  globalThis.fetch = async (url, options = {}) => {
    calls.push({ url, method: options.method || 'GET', body: options.body });
    const next = responses.shift() || { ok: true, payload: {} };
    return { ok: next.ok, json: async () => next.payload };
  };
  return calls;
}

const commentProposal = {
  id: 'p-1',
  status: 'draft',
  hash: 'hash-of-what-the-user-read',
  summary: 'Comment on #6',
  change: {
    kind: 'comment',
    repo: 'octocat/demo',
    issue: 6,
    body: 'This looks like a duplicate of #1.',
    rationale: 'Same stack trace as #1.'
  }
};

async function loadPanel(mount) {
  await import('./github-proposals.js');
  const panel = globalThis.window.GitHubProposalsPanel;
  panel.mount = mount;
  panel.workspaceId = 'ws-1';
  return panel;
}

// --- rendering ---------------------------------------------------------------

test('renders the literal comment text, not just the summary', async () => {
  const mount = setup();
  stubFetch([]);
  const panel = await loadPanel(mount);

  panel.render([commentProposal]);

  assert.equal(mount.hidden, false, 'the panel should be visible when something is pending');
  const text = mount.textContent;
  assert.ok(
    text.includes('This looks like a duplicate of #1.'),
    'the exact comment text must be shown; approving a summary is not informed consent'
  );
  assert.ok(text.includes('Comment on #6'), 'the summary should still head the entry');
  assert.ok(text.includes('Same stack trace as #1.'), 'the rationale should be shown');
  assert.ok(text.includes('octocat/demo'), 'the target repo should be named');
});

test('states plainly that nothing has reached GitHub yet', async () => {
  const mount = setup();
  stubFetch([]);
  const panel = await loadPanel(mount);

  panel.render([commentProposal]);

  assert.ok(
    mount.textContent.includes('Nothing below has been sent to GitHub'),
    'the panel must say nothing has been sent'
  );
});

test('renders label changes as add/remove chips', async () => {
  const mount = setup();
  stubFetch([]);
  const panel = await loadPanel(mount);

  panel.render([
    {
      id: 'p-2',
      status: 'draft',
      hash: 'h',
      summary: 'Labels on #2',
      change: {
        kind: 'labels',
        repo: 'octocat/demo',
        issue: 2,
        add_labels: ['bug'],
        remove_labels: ['needs-triage'],
        rationale: 'Reproducible crash.'
      }
    }
  ]);

  const adds = mount.querySelectorAll('.ghp-label-add');
  const removes = mount.querySelectorAll('.ghp-label-remove');
  assert.equal(adds.length, 1);
  assert.equal(adds[0].textContent, 'bug');
  assert.equal(removes.length, 1);
  assert.equal(removes[0].textContent, 'needs-triage');
});

test('stays hidden when there is nothing to approve', async () => {
  const mount = setup();
  stubFetch([]);
  const panel = await loadPanel(mount);

  panel.render([{ id: 'p-3', status: 'rejected', hash: 'h', summary: 'x', change: {} }]);

  assert.equal(mount.hidden, true, 'a workspace with nothing pending should show no panel at all');
});

// --- approval ----------------------------------------------------------------

test('approving sends the hash of what was rendered', async () => {
  const mount = setup();
  const panel = await loadPanel(mount);
  panel.render([commentProposal]);

  const calls = stubFetch([
    { ok: true, payload: { proposal: { ...commentProposal, status: 'applied' } } },
    { ok: true, payload: { proposals: [] } }
  ]);

  mount.querySelectorAll('.ghp-approve')[0].click();
  await new Promise(resolve => setImmediate(resolve));

  const confirm = calls.find(c => c.url.includes('/confirm'));
  assert.ok(confirm, 'approving should call the confirm endpoint');
  assert.equal(confirm.method, 'POST');
  assert.equal(
    JSON.parse(confirm.body).expected_hash,
    'hash-of-what-the-user-read',
    'the approval must carry the hash of the content the user saw'
  );
});

test('a refused approval surfaces the reason and leaves the row', async () => {
  const mount = setup();
  const panel = await loadPanel(mount);
  panel.render([commentProposal]);

  stubFetch([
    {
      ok: false,
      payload: {
        error: 'proposal_changed',
        message: 'This proposal changed since you reviewed it. Read it again before approving.'
      }
    }
  ]);

  mount.querySelectorAll('.ghp-approve')[0].click();
  await new Promise(resolve => setImmediate(resolve));

  assert.ok(
    mount.textContent.includes('changed since you reviewed it'),
    'the refusal reason must be shown to the user'
  );
});

test('rejecting calls reject, never confirm', async () => {
  const mount = setup();
  const panel = await loadPanel(mount);
  panel.render([commentProposal]);

  const calls = stubFetch([
    { ok: true, payload: { proposal: { ...commentProposal, status: 'rejected' } } },
    { ok: true, payload: { proposals: [] } }
  ]);

  // The reject button is the second in the row's actions.
  const buttons = mount.querySelectorAll('button');
  buttons[buttons.length - 1].click();
  await new Promise(resolve => setImmediate(resolve));

  assert.ok(
    calls.some(c => c.url.includes('/reject')),
    'rejecting should call the reject endpoint'
  );
  assert.ok(
    !calls.some(c => c.url.includes('/confirm')),
    'rejecting must never reach the confirm path'
  );
});

test('an applied proposal links to the real GitHub result', async () => {
  const mount = setup();
  stubFetch([]);
  const panel = await loadPanel(mount);

  panel.render([
    {
      id: 'p-4',
      status: 'applied',
      hash: 'h',
      summary: 'Comment on #6',
      applied_url: 'https://github.com/octocat/demo/issues/6#issuecomment-1',
      change: { kind: 'comment', repo: 'octocat/demo', issue: 6, body: 'x' }
    }
  ]);

  const link = mount.querySelectorAll('.ghp-link')[0];
  assert.ok(link, 'an applied proposal should link to its result');
  assert.equal(link.href, 'https://github.com/octocat/demo/issues/6#issuecomment-1');
});
