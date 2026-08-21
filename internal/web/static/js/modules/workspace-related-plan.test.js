import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  fetchRelatedPlan,
  relatedPlanLink,
  relatedPlanProvenance,
  relatedPlanSummary,
  renderRelatedPlan
} from './workspace-related-plan.js';

function relatedFixture(overrides = {}) {
  return {
    plan_id: 'plan_1',
    studio_id: 'ws-1',
    title: 'Migrate reporting',
    status: 'executing',
    status_label: 'Executing',
    plan_version: 2,
    url: '/workspaces/ws-1/plans/plan_1',
    provenance: { role: 'item', approved_by: 'jj' },
    ...overrides
  };
}

// --- The summary answers "which plan" --------------------------------------

test('the summary names the plan, its status, and its version', () => {
  const summary = relatedPlanSummary(relatedFixture());
  assert.match(summary, /Migrate reporting/);
  assert.match(summary, /Executing/);
  assert.match(summary, /version 2/);
});

test('a plan with no title still reads as something', () => {
  const summary = relatedPlanSummary(relatedFixture({ title: '' }));
  assert.match(summary, /Untitled plan/);
});

test('nothing renders for a task with no plan behind it', () => {
  assert.equal(relatedPlanSummary(null), '');
  assert.equal(relatedPlanSummary({}), '');
});

// --- Provenance answers "which part of it" ---------------------------------

test('provenance says which part of the plan this is, and who approved it', () => {
  const provenance = relatedPlanProvenance(relatedFixture());
  assert.match(provenance, /A step from this plan/);
  assert.match(provenance, /approved by jj/);
});

test('a group task reads as a group rather than a step', () => {
  const provenance = relatedPlanProvenance(
    relatedFixture({ provenance: { role: 'group', approved_by: 'jj' } })
  );
  assert.match(provenance, /task group/);
});

// --- The link keeps route identity separate from API identity --------------

test('an explicit workspace slug overrides the legacy server URL', () => {
  const related = relatedFixture({
    studio_id: 'workspace-uuid',
    url: '/workspaces/workspace-uuid/plans/plan_1'
  });
  assert.equal(
    relatedPlanLink(related, 'marketing-site'),
    '/workspaces/marketing-site/plans/plan_1'
  );
  // Legacy callers can still display a server-provided canonical URL.
  assert.equal(relatedPlanLink(relatedFixture()), '/workspaces/ws-1/plans/plan_1');
  assert.equal(relatedPlanLink(relatedFixture({ url: '' })), '');
});

// --- Rendering -------------------------------------------------------------

function fakeContainer() {
  return { hidden: false, innerHTML: '' };
}

test('rendering produces a slug-based link to the canonical surface', () => {
  const container = fakeContainer();
  renderRelatedPlan(container, relatedFixture(), value => value, 'marketing-site');

  assert.equal(container.hidden, false);
  assert.match(container.innerHTML, /href="\/workspaces\/marketing-site\/plans\/plan_1"/);
  assert.match(container.innerHTML, /Migrate reporting/);
});

// Most tasks are created directly and have no plan. That is the ordinary case,
// so the panel hides rather than showing an empty box implying breakage.
test('a task with no plan hides the panel entirely', () => {
  const container = fakeContainer();
  renderRelatedPlan(container, null);

  assert.equal(container.hidden, true);
  assert.equal(container.innerHTML, '');
});

test('a related plan with no url hides rather than rendering a dead link', () => {
  const container = fakeContainer();
  renderRelatedPlan(container, relatedFixture({ url: '' }));
  assert.equal(container.hidden, true);
});

// The summary is read-only. A second place to approve from is exactly the
// duplication this feature removes.
//
// The assertion is about CONTROLS, not words: "approved by jj" is legitimate
// provenance text, so matching the word would fail on correct output.
test('the rendered summary offers no lifecycle controls', () => {
  const container = fakeContainer();
  renderRelatedPlan(container, relatedFixture());

  for (const control of ['<button', '<form', '<input', 'onclick']) {
    assert.ok(
      !container.innerHTML.toLowerCase().includes(control),
      `the related-plan summary contains ${control}`
    );
  }
  // Exactly one thing to do: follow the link.
  assert.equal((container.innerHTML.match(/<a /g) || []).length, 1);
});

// --- Lookup ----------------------------------------------------------------

test('a missing link resolves to null rather than throwing', async () => {
  const notFound = async () => ({ ok: false, status: 404, json: async () => ({}) });
  assert.equal(await fetchRelatedPlan('ws-1', 'task', 'task-1', notFound), null);
});

test('a network failure resolves to null rather than breaking the page', async () => {
  const boom = async () => {
    throw new Error('offline');
  };
  assert.equal(await fetchRelatedPlan('ws-1', 'task', 'task-1', boom), null);
});

test('a run lookup uses the run endpoint', async () => {
  let requested = '';
  const capture = async url => {
    requested = url;
    return { ok: true, json: async () => relatedFixture() };
  };

  await fetchRelatedPlan('ws-1', 'run', 'run-1', capture);
  assert.match(requested, /plan-for-run\/run-1/);
});

test('a task lookup uses the task endpoint', async () => {
  let requested = '';
  const capture = async url => {
    requested = url;
    return { ok: true, json: async () => relatedFixture() };
  };

  await fetchRelatedPlan('ws-1', 'task', 'task-1', capture);
  assert.match(requested, /plan-for-task\/task-1/);
});
