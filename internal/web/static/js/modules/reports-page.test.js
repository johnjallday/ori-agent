import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function harness(page) {
  const html = readFileSync(
    new URL(`../../../templates/pages/${page}.tmpl`, import.meta.url),
    'utf8'
  );
  const source = html.match(/<script>\s*([\s\S]*?)<\/script>/)[1];
  const nodes = new Map();
  const events = new Map();
  function node(id) {
    if (!nodes.has(id))
      nodes.set(id, {
        id,
        innerHTML: '',
        textContent: '',
        value: '',
        hidden: false,
        disabled: false,
        style: {},
        attributes: new Map(),
        setAttribute(name, value) {
          this.attributes.set(name, value);
        },
        removeAttribute(name) {
          this.attributes.delete(name);
        },
        addEventListener() {},
        focus() {}
      });
    return nodes.get(id);
  }
  const notifications = [];
  const sandbox = {
    console: { error() {} },
    document: {
      getElementById: node,
      addEventListener: (type, listener) => events.set(type, listener)
    },
    window: { location: { href: '' }, Toast: { show: (...args) => notifications.push(args) } },
    escapeHtml: value =>
      String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;'),
    escapeAttr: value =>
      String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;'),
    setInterval: () => 1,
    clearInterval() {},
    setTimeout() {}
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      (page === 'review'
        ? '\nglobalThis.reviewStatusIntent = reviewStatusIntent; globalThis.reviewBadge = reviewBadge;'
        : ''),
    sandbox
  );
  return { sandbox, node, events, notifications };
}

test('Usage renders precise costs, tabular totals and literal long metadata', () => {
  const { sandbox, node } = harness('usage');
  sandbox.renderSummaryCard('todayStats', { requests: 1234, tokens: 987654, cost: 12.3456 });
  assert.match(node('todayStats').innerHTML, /\$12\.3456/);
  assert.match(node('todayStats').innerHTML, /1,234/);
  assert.match(node('todayStats').innerHTML, /<dl>/);
  sandbox.renderAgentStats({
    fake: { agent_name: '<Fictional agent>', requests: 1, total_tokens: 42, total_cost: 0 }
  });
  assert.match(node('agentStats').innerHTML, /&lt;Fictional agent&gt;/);
  sandbox.renderModelStats({
    fake: {
      model: '<Fictional model>',
      provider: '<provider>',
      requests: 1,
      total_tokens: 42,
      total_cost: 0
    }
  });
  assert.match(
    node('modelStats').innerHTML,
    /role="region" aria-label="Usage by model" tabindex="0"/
  );
  assert.doesNotMatch(node('modelStats').innerHTML, /<Fictional model>/);
});

test('Usage failed reads are visible and never left as loading or false zero', async () => {
  const { sandbox, node } = harness('usage');
  sandbox.fetch = async () => ({ ok: false });
  await sandbox.refreshUsageData();
  assert.equal(node('usageFeedback').hidden, false);
  assert.match(node('usageFeedback').textContent, /summary.*breakdowns/);
  assert.equal(node('todayStats').textContent, 'Unavailable');
  assert.equal(node('modelStats').textContent, 'Unavailable');
  assert.equal(node('usageEmpty').hidden, true);
  assert.equal(node('usageRefreshBtn').disabled, false);
});

test('Usage empty breakdowns collapse equivalent panels into one explanation', async () => {
  const { sandbox, node } = harness('usage');
  sandbox.fetch = async () => ({ ok: true, json: async () => ({}) });
  await sandbox.loadDetailedStats();
  assert.equal(node('usageEmpty').hidden, false);
  assert.equal(node('usageDetails').hidden, true);
});

test('Usage malformed breakdowns are not mistaken for an empty library', async () => {
  const { sandbox, node } = harness('usage');
  sandbox.fetch = async () => ({ ok: true, json: async () => ({ by_model: 'not-a-map' }) });
  await assert.rejects(sandbox.loadDetailedStats(), /breakdowns are unavailable/);
  assert.equal(node('usageEmpty').hidden, true);
  assert.equal(node('modelStats').textContent, 'Unavailable');
});

test('Review failed agent reads are explicit in the toolbar and confirmation', async () => {
  const { sandbox, node } = harness('review');
  sandbox.fetch = async () => ({ ok: false });
  await sandbox.loadAgents();
  for (const id of ['reviewAgentFeedback', 'triggerAgentHelp']) {
    assert.equal(node(id).hidden, false);
    assert.match(node(id).textContent, /Agent list unavailable/);
  }
});

test('Review native export destinations retain both encoded filters', () => {
  const { sandbox, node } = harness('review');
  node('filterAgent').value = 'Fictional & notes';
  node('filterType').value = 'ignored_error';
  sandbox.syncReviewExports();
  assert.equal(
    node('reviewExportJSONL').href,
    '/api/review/export?format=jsonl&agent_name=Fictional%20%26%20notes&issue_type=ignored_error'
  );
  assert.equal(
    sandbox.reviewExportURL('json'),
    '/api/review/export?format=json&agent_name=Fictional%20%26%20notes&issue_type=ignored_error'
  );
});

test('Review long contexts are keyboard disclosures with literal complete content', () => {
  const { sandbox } = harness('review');
  const markup = sandbox.reviewContext('<Fictional full context> '.repeat(12));
  assert.match(markup, /<details class="review-context"><summary>/);
  assert.match(markup, /<p>&lt;Fictional full context&gt;/);
  assert.doesNotMatch(markup, /<Fictional full context>/);
});

test('Review uses shared semantic status foregrounds, not data-derived inline colors', () => {
  const { sandbox } = harness('review');
  assert.equal(sandbox.reviewStatusIntent('failed'), 'review-danger');
  assert.equal(sandbox.reviewStatusIntent('completed'), 'review-success');
  assert.match(
    sandbox.reviewBadge('<fictional>', 'review-warning'),
    /class="badge review-warning"/
  );
  assert.doesNotMatch(sandbox.reviewBadge('<fictional>'), /<fictional>/);
});

test('Review trigger keeps its payload and restores busy state after rejection without starting a job', async () => {
  const { sandbox, node, notifications } = harness('review');
  node('triggerSensitivity').value = 'high';
  node('triggerAgent').value = 'Fictional notes';
  let call;
  sandbox.fetch = async (url, options) => {
    call = { url, options };
    return { ok: false };
  };
  await sandbox.triggerReview();
  assert.equal(call.url, '/api/review/trigger');
  assert.equal(call.options.method, 'POST');
  assert.deepEqual(JSON.parse(call.options.body), {
    sensitivity: 'high',
    agent_name: 'Fictional notes'
  });
  assert.equal(node('reviewStartBtn').disabled, false);
  assert.equal(notifications.length, 1);
  assert.equal(notifications[0][1], 'error');
});

test('Review failed reads clear stale numeric summaries instead of claiming emptiness', async () => {
  const { sandbox, node } = harness('review');
  sandbox.fetch = async () => ({ ok: false });
  await Promise.all([sandbox.loadIssues(), sandbox.loadRuns()]);
  assert.equal(node('totalIssuesCard').textContent, 'Unavailable');
  assert.equal(node('lastReviewCard').textContent, 'Unavailable');
  assert.match(node('issuesTable').innerHTML, /role="alert"/);
  assert.match(node('runsTable').innerHTML, /Use Refresh/);
});

test('Review polling completion retains stable progress hooks for a later run', async () => {
  const { sandbox, node } = harness('review');
  vm.runInContext('activeJobId = "fictional-read-only-status";', sandbox);
  sandbox.fetch = async () => ({
    ok: true,
    json: async () => ({
      status: 'failed',
      sessions_reviewed: 1,
      issues_found: 0,
      error_message: '<Fictional failure>'
    })
  });
  await sandbox.pollJobStatus();
  assert.equal(node('reviewProgressSection').hidden, true);
  assert.equal(node('reviewResult').hidden, false);
  assert.match(node('reviewResult').innerHTML, /&lt;Fictional failure&gt;/);
  assert.equal(node('activeReviewCard').innerHTML, '');
  vm.runInContext('activeJobId = "fictional-second-status";', sandbox);
  sandbox.pollJobStatus = () => {};
  sandbox.startPolling();
  assert.equal(node('reviewProgressSection').hidden, false);
  assert.equal(node('reviewResult').hidden, true);
  assert.equal(node('activeJobId').textContent, 'fictional-second-status');
});
