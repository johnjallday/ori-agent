import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  countOpenClarifications,
  escapeHtml,
  nextActionFor,
  planCardHtml,
  planDetailPath,
  progressSummary,
  statusMeta,
  versionLabel,
  WorkspacePlansPage
} from './workspace-plan.js';

function plan(overrides = {}) {
  return {
    id: 'plan_1',
    studio_id: 'ws-1',
    title: 'Ship the thing',
    status: 'draft',
    current_version: 0,
    approved_version: 0,
    last_activity_at: '2026-08-13T10:00:00Z',
    draft: { clarifications: [] },
    ...overrides
  };
}

// Every status renders a word and an icon, so state is never carried by color
// alone — including for a status this build has never heard of (FR-162).
test('statusMeta always yields a label and an icon', () => {
  for (const status of [
    'draft',
    'needs_input',
    'in_review',
    'approved',
    'executing',
    'paused',
    'completed',
    'failed',
    'cancelled',
    'superseded'
  ]) {
    const meta = statusMeta(status);
    assert.ok(meta.label && meta.label.length > 0, `${status} has no label`);
    assert.ok(meta.icon && meta.icon.length > 0, `${status} has no icon`);
  }

  const unknown = statusMeta('some_future_status');
  assert.equal(unknown.label, 'Some Future Status');
  assert.ok(unknown.icon);
});

// The list exists to answer "what does this need from me next" (FR-147).
test('nextActionFor names the action each status is waiting on', () => {
  assert.equal(nextActionFor(plan({ status: 'draft' })).kind, 'edit');
  assert.equal(nextActionFor(plan({ status: 'in_review' })).kind, 'review');
  assert.equal(nextActionFor(plan({ status: 'approved' })).label, 'Start work');
  assert.equal(nextActionFor(plan({ status: 'completed' })).label, 'Review report');
  assert.equal(nextActionFor(plan({ status: 'superseded' })).label, 'Open replacement');
});

test('a needs_input plan counts the questions still open', () => {
  const waiting = plan({
    status: 'needs_input',
    draft: {
      clarifications: [
        { id: 'c1', status: 'open' },
        { id: 'c2', status: 'answered' },
        { id: 'c3', status: 'open' }
      ]
    }
  });
  assert.equal(countOpenClarifications(waiting), 2);
  assert.equal(nextActionFor(waiting).label, 'Answer 2 questions');

  const one = plan({
    status: 'needs_input',
    draft: { clarifications: [{ id: 'c1', status: 'open' }] }
  });
  assert.equal(nextActionFor(one).label, 'Answer 1 question');
});

// "No tasks yet" and "0 of 0 complete" are different claims. A Plan that has
// materialized nothing must not read as finished work (FR-12).
test('progressSummary distinguishes no tasks from no progress', () => {
  assert.equal(progressSummary(plan()).text, 'No tickets yet');
  assert.equal(progressSummary(plan({ progress: { total: 0 } })).text, 'No tickets yet');

  const running = progressSummary(
    plan({
      progress: { total: 4, completed: 1, running: 1, blocked: 1, failed: 1 }
    })
  );
  assert.equal(running.text, '1 of 4 tickets complete');
  assert.equal(running.percent, 25);
  assert.match(running.detail, /1 running/);
  assert.match(running.detail, /1 blocked/);
  assert.match(running.detail, /1 failed/);
});

test('progressSummary surfaces a plan waiting for the execution slot', () => {
  const waiting = progressSummary(
    plan({
      progress: { total: 2, completed: 0, waiting_for_slot: 2 }
    })
  );
  assert.match(waiting.detail, /waiting for the execution slot/);
});

// Approval binds to an exact version, so "no version yet" must never render as
// a version number (FR-61).
test('versionLabel distinguishes no version, a draft version, and an approved one', () => {
  assert.equal(versionLabel(plan()), 'No review version yet');
  assert.equal(versionLabel(plan({ current_version: 2 })), 'Version 2');
  assert.equal(
    versionLabel(plan({ current_version: 2, approved_version: 2 })),
    'Version 2 · approved'
  );
  assert.equal(
    versionLabel(plan({ current_version: 3, approved_version: 2 })),
    'Version 3 · v2 approved'
  );
});

test('planDetailPath builds the canonical route from a slug and escapes its segments', () => {
  assert.equal(planDetailPath('ws-1', 'plan_1'), '/workspaces/ws-1/plans/plan_1');
  assert.equal(planDetailPath('ws 1', 'plan/1'), '/workspaces/ws%201/plans/plan%2F1');
});

test('escapeHtml neutralizes markup from plan content', () => {
  assert.equal(escapeHtml('<script>alert(1)</script>'), '&lt;script&gt;alert(1)&lt;/script&gt;');
  assert.equal(escapeHtml(null), '');
});

// A Plan title is user-authored text that reaches innerHTML, so the card must
// escape it rather than trust it.
test('planCardHtml escapes plan-authored text', () => {
  const html = planCardHtml(plan({ title: '<img src=x onerror=alert(1)>' }), 'ws-1');
  assert.ok(!html.includes('<img'), 'raw markup reached the card');
  assert.ok(html.includes('&lt;img'));
  assert.ok(html.includes('/workspaces/ws-1/plans/plan_1'));
  assert.ok(html.includes('plan-status-label'));
});

// --- Page controller -------------------------------------------------------

function fakeDocument(elements) {
  return {
    querySelector: selector => elements[selector] || null,
    defaultView: { location: { href: '' } }
  };
}

function fakeEl(extra = {}) {
  return {
    innerHTML: '',
    textContent: '',
    value: '',
    hidden: false,
    dataset: {},
    addEventListener() {},
    ...extra
  };
}

function jsonResponse(body, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => body
  };
}

test('the plans page splits active from history and keeps UUID APIs separate from slug links', async () => {
  const elements = {
    '#plan-active-list': fakeEl(),
    '#plan-active-empty': fakeEl(),
    '#plan-history-list': fakeEl(),
    '#plan-history-empty': fakeEl(),
    '#plan-history-section': fakeEl(),
    '#plan-status': fakeEl()
  };
  const requestedURLs = [];

  const page = new WorkspacePlansPage('workspace-uuid', {
    workspaceSlug: 'marketing-site',
    document: fakeDocument(elements),
    fetch: async url => {
      requestedURLs.push(url);
      if (url.includes('scope=active')) {
        return jsonResponse({ plans: [plan({ id: 'plan_a', title: 'Active plan' })] });
      }
      return jsonResponse({ plans: [] });
    }
  });

  await page.reload();

  assert.match(elements['#plan-active-list'].innerHTML, /Active plan/);
  assert.match(
    elements['#plan-active-list'].innerHTML,
    /workspaces\/marketing-site\/plans\/plan_a/
  );
  assert.ok(requestedURLs.every(url => url.includes('/api/workspaces/workspace-uuid/')));
  assert.equal(elements['#plan-active-empty'].hidden, true);
  assert.equal(elements['#plan-history-section'].hidden, true);
});

test('the plans page shows an empty state when there is nothing active', async () => {
  const elements = {
    '#plan-active-list': fakeEl(),
    '#plan-active-empty': fakeEl(),
    '#plan-history-list': fakeEl(),
    '#plan-history-empty': fakeEl(),
    '#plan-history-section': fakeEl(),
    '#plan-status': fakeEl()
  };

  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () => jsonResponse({ plans: [] })
  });

  await page.reload();

  assert.equal(elements['#plan-active-empty'].hidden, false);
  assert.equal(elements['#plan-active-list'].innerHTML, '');
});

const plansElements = () => ({
  '#plan-retired-note': fakeEl({ hidden: true }),
  '#plan-active-list': fakeEl(),
  '#plan-active-empty': fakeEl(),
  '#plan-history-list': fakeEl(),
  '#plan-history-empty': fakeEl(),
  '#plan-history-section': fakeEl(),
  '#plan-status': fakeEl()
});

// Existing plans stay listed and readable whatever the workspace's planning
// setting says, and loading the page only ever reads: it does not look at the
// setting, it creates nothing, and it turns nothing on (FR-159).
test('existing plans are listed by reads alone, active and history, with no settings lookup', async () => {
  const elements = plansElements();
  const calls = [];
  const page = new WorkspacePlansPage('ws-1', {
    workspaceSlug: 'widget',
    document: fakeDocument(elements),
    fetch: async (url, options = {}) => {
      calls.push({ url, method: options.method || 'GET' });
      if (url.includes('scope=history')) {
        return jsonResponse({ plans: [plan({ id: 'plan_old', status: 'cancelled' })] });
      }
      return jsonResponse({ plans: [plan({ id: 'plan_now', status: 'in_review' })] });
    }
  });

  await page.init();

  assert.match(elements['#plan-active-list'].innerHTML, /plan_now/);
  assert.equal(elements['#plan-history-section'].hidden, false, 'history stays readable');
  assert.match(elements['#plan-history-list'].innerHTML, /plan_old/);
  assert.equal(elements['#plan-retired-note'].hidden, true, 'plans exist, so nothing is retired');
  assert.deepEqual(
    calls.filter(call => call.method !== 'GET'),
    [],
    'opening the page only reads'
  );
  assert.equal(
    calls.some(call => call.url.endsWith('/settings')),
    false,
    'the planning setting does not decide what is listed'
  );
});

test('a history-only workspace still lists its plan and does not claim to have none', async () => {
  const elements = plansElements();
  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async url =>
      jsonResponse({
        plans: url.includes('scope=history') ? [plan({ id: 'plan_old', status: 'cancelled' })] : []
      })
  });
  await page.init();
  assert.equal(elements['#plan-active-empty'].hidden, false);
  assert.match(elements['#plan-history-list'].innerHTML, /plan_old/);
  assert.equal(elements['#plan-retired-note'].hidden, true);
});

test('a workspace with no plans says creation is retired and points at Tickets', async () => {
  const elements = plansElements();
  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () => jsonResponse({ plans: [] })
  });
  await page.init();
  assert.equal(elements['#plan-retired-note'].hidden, false);
});

// Unknown is not zero: a failed read must not tell the person they have no plans.
test('a failed read does not claim the workspace has no plans', async () => {
  const elements = plansElements();
  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () => jsonResponse({ code: 'internal_error', message: 'boom' }, false, 500)
  });
  await page.init();
  assert.equal(elements['#plan-retired-note'].hidden, true);
  assert.match(elements['#plan-status'].textContent, /Could not load plans/);
});

test('the page has no way to create a plan and ignores the retired suggestion link', async () => {
  for (const name of ['createPlan', 'loadPlanningPolicy', 'offerAssessmentSuggestion'])
    assert.equal(typeof WorkspacePlansPage.prototype[name], 'undefined', name);
  const elements = plansElements();
  const calls = [];
  const doc = fakeDocument(elements);
  doc.defaultView.location.search = '?next=assessment';
  const page = new WorkspacePlansPage('ws-1', {
    document: doc,
    fetch: async (url, options = {}) => {
      calls.push({ url, method: options.method || 'GET' });
      return jsonResponse({ plans: [] });
    }
  });
  await page.init();
  assert.equal(
    calls.some(call => call.url.endsWith('/folder-first-task')),
    false,
    'the assessment suggestion is not read here any more'
  );
  assert.deepEqual(
    calls.filter(call => call.method !== 'GET'),
    []
  );
});

// The server's stable error code is the contract; the message is for the human
// reading it (FR-166).
test('an API failure surfaces the server message and preserves its code', async () => {
  const elements = { '#plan-status': fakeEl() };

  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () =>
      jsonResponse({ code: 'plan_not_deletable', message: 'plan has effects' }, false, 409)
  });

  await page.reload();

  assert.match(elements['#plan-status'].textContent, /plan has effects/);
  assert.equal(elements['#plan-status'].dataset.tone, 'error');

  await assert.rejects(
    () => page.request('/api/workspaces/ws-1/plans'),
    error => error.code === 'plan_not_deletable' && error.status === 409
  );
});

// The first assessment's suggested next ticket no longer lands on this page: it
// opens in Give work (workspace-suggested-ticket.js, with its own tests), and the
// retired /plans?next=assessment address is redirected by the server.
