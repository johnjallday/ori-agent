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
  assert.equal(progressSummary(plan()).text, 'No tasks yet');
  assert.equal(progressSummary(plan({ progress: { total: 0 } })).text, 'No tasks yet');

  const running = progressSummary(
    plan({
      progress: { total: 4, completed: 1, running: 1, blocked: 1, failed: 1 }
    })
  );
  assert.equal(running.text, '1 of 4 tasks complete');
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

test('planDetailPath builds the canonical route and escapes its ids', () => {
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

test('the plans page splits active from history and hides an empty history', async () => {
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
    fetch: async url => {
      if (url.includes('scope=active')) {
        return jsonResponse({ plans: [plan({ id: 'plan_a', title: 'Active plan' })] });
      }
      return jsonResponse({ plans: [] });
    }
  });

  await page.reload();

  assert.match(elements['#plan-active-list'].innerHTML, /Active plan/);
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

// Planning being off hides creation and never hides history, and nothing on
// this page turns planning back on (FR-159).
test('disabled planning hides creation but keeps history readable', async () => {
  const elements = {
    '#plan-disabled-banner': fakeEl(),
    '#plan-create-panel': fakeEl(),
    '#plan-active-list': fakeEl(),
    '#plan-active-empty': fakeEl(),
    '#plan-history-list': fakeEl(),
    '#plan-history-empty': fakeEl(),
    '#plan-history-section': fakeEl(),
    '#plan-status': fakeEl()
  };

  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async url => {
      if (url.endsWith('/settings')) {
        return jsonResponse({ settings: { planning: { enabled: false } } });
      }
      if (url.includes('scope=history')) {
        return jsonResponse({ plans: [plan({ id: 'plan_old', status: 'cancelled' })] });
      }
      return jsonResponse({ plans: [] });
    }
  });

  await page.loadPlanningPolicy();
  await page.reload();

  assert.equal(
    elements['#plan-disabled-banner'].hidden,
    false,
    'the disabled notice must be visible'
  );
  assert.equal(elements['#plan-create-panel'].hidden, true, 'creation must be hidden');
  assert.equal(elements['#plan-history-section'].hidden, false, 'history must stay readable');
  assert.match(elements['#plan-history-list'].innerHTML, /plan_old/);
});

test('enabled planning shows creation and hides the disabled notice', async () => {
  const elements = {
    '#plan-disabled-banner': fakeEl(),
    '#plan-create-panel': fakeEl()
  };

  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () => jsonResponse({ settings: { planning: { enabled: true } } })
  });

  await page.loadPlanningPolicy();

  assert.equal(elements['#plan-disabled-banner'].hidden, true);
  assert.equal(elements['#plan-create-panel'].hidden, false);
});

test('creating a plan navigates to its canonical detail route', async () => {
  const elements = {
    '#plan-create-request': fakeEl({ value: 'Plan the migration' }),
    '#plan-status': fakeEl()
  };
  const doc = fakeDocument(elements);

  let posted = null;
  const page = new WorkspacePlansPage('ws-1', {
    document: doc,
    fetch: async (url, options) => {
      posted = { url, body: JSON.parse(options.body) };
      return jsonResponse({ id: 'plan_new' }, true, 201);
    }
  });

  await page.createPlan();

  assert.equal(posted.url, '/api/workspaces/ws-1/plans');
  assert.equal(posted.body.request, 'Plan the migration');
  assert.equal(doc.defaultView.location.href, '/workspaces/ws-1/plans/plan_new');
  assert.equal(elements['#plan-create-request'].value, '');
});

test('creating a plan with an empty request asks for one instead of calling the API', async () => {
  const elements = {
    '#plan-create-request': fakeEl({ value: '   ' }),
    '#plan-status': fakeEl()
  };

  let called = false;
  const page = new WorkspacePlansPage('ws-1', {
    document: fakeDocument(elements),
    fetch: async () => {
      called = true;
      return jsonResponse({});
    }
  });

  const result = await page.createPlan();

  assert.equal(result, null);
  assert.equal(called, false, 'an empty request must not reach the API');
  assert.match(elements['#plan-status'].textContent, /Describe what you want planned/);
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
