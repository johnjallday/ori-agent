import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  activityLine,
  approvalButtonLabel,
  clarificationLine,
  diffSummary,
  materializedMessage,
  WorkspacePlanPage
} from './workspace-plan-detail.js';

function el(extra = {}) {
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

const SELECTORS = [
  '#plan-materialization-panel',
  '#plan-materialization-summary',
  '#plan-materialization-tasks',
  '#plan-materialization-artifacts',
  '#plan-materialization-retry',
  '#plan-review-panel',
  '#plan-review-version',
  '#plan-review-objective',
  '#plan-review-counts',
  '#plan-review-effects',
  '#plan-review-blockers',
  '#plan-approve',
  '#plan-compare-panel',
  '#plan-compare-list',
  '#plan-loading',
  '#plan-missing',
  '#plan-content',
  '#plan-title',
  '#plan-breadcrumb-title',
  '#plan-objective',
  '#plan-original-request',
  '#plan-version-label',
  '#plan-next-action',
  '#plan-progress',
  '#plan-status-badge',
  '#plan-activity-list',
  '#plan-clarifications-section',
  '#plan-clarifications-list',
  '#plan-editor-groups',
  '#plan-editor-empty',
  '#plan-save-state',
  '#plan-conflict-panel',
  '#plan-conflict-summary',
  '#plan-detail-status'
];

function fakeDocument() {
  const elements = {};
  for (const selector of SELECTORS) elements[selector] = el();
  return {
    elements,
    querySelector: selector => elements[selector] || null
  };
}

function planFixture(overrides = {}) {
  return {
    id: 'plan_1',
    studio_id: 'ws-1',
    title: 'Ship the migration',
    objective: 'Migrate reporting safely',
    original_request: 'Plan the migration',
    status: 'draft',
    current_version: 0,
    approved_version: 0,
    draft_revision: 2,
    draft: {
      execution: { mode: 'step_through' },
      clarifications: [],
      groups: [
        {
          id: 'grp-1',
          title: 'Prepare',
          depends_on: [],
          items: [
            { id: 'itm-1', description: 'Snapshot staging', depends_on: [] },
            { id: 'itm-2', description: 'Verify checksums', depends_on: ['itm-1'] }
          ]
        }
      ]
    },
    ...overrides
  };
}

function jsonResponse(body, ok = true, status = 200) {
  return { ok, status, json: async () => body };
}

// makePage wires a page against canned responses and a controllable clock, so
// autosave can be tested without waiting.
function makePage({ plan = planFixture(), activity = [], onSave } = {}) {
  const doc = fakeDocument();
  const timers = [];

  const page = new WorkspacePlanPage('ws-1', 'plan_1', {
    document: doc,
    setTimeout: fn => {
      timers.push(fn);
      return timers.length;
    },
    clearTimeout: () => {},
    fetch: async (url, options = {}) => {
      if (url.endsWith('/activity')) return jsonResponse({ activity });
      if (url.endsWith('/draft') && options.method === 'PATCH') {
        return onSave ? onSave(JSON.parse(options.body)) : jsonResponse(plan);
      }
      return jsonResponse(plan);
    }
  });

  return { page, doc, runTimers: () => timers.splice(0).forEach(fn => fn()) };
}

// --- Rendering helpers -----------------------------------------------------

test('activityLine reads a transition as a move between named states', () => {
  assert.equal(
    activityLine({ kind: 'status_change', from: 'draft', to: 'in_review', actor: 'jj' }),
    'Draft → In review by jj ()'
  );
  assert.match(activityLine({ kind: 'created', actor: 'jj' }), /^Plan created by jj/);
  assert.match(activityLine({ kind: 'approval_consumed' }), /^Approval consumed/);
});

// Skipped and unanswered are different states and must read differently
// (FR-24, FR-28).
test('clarificationLine distinguishes answered, skipped, and open questions', () => {
  assert.equal(clarificationLine({ status: 'answered', answer: 'Staging' }), 'Answered: Staging');
  assert.equal(
    clarificationLine({ status: 'skipped', skip_reason: 'no deadline' }),
    'Skipped — no deadline'
  );
  assert.equal(clarificationLine({ status: 'skipped' }), 'Skipped');
  assert.equal(
    clarificationLine({ status: 'open', required: true }),
    'Required — not yet answered'
  );
  assert.equal(clarificationLine({ status: 'open' }), 'Optional — not yet answered');
});

// --- Page rendering --------------------------------------------------------

test('the detail page renders the plan and its task structure', async () => {
  const { page, doc } = makePage();
  await page.reload();

  assert.equal(doc.elements['#plan-title'].textContent, 'Ship the migration');
  assert.equal(doc.elements['#plan-original-request'].textContent, 'Plan the migration');
  assert.equal(doc.elements['#plan-version-label'].textContent, 'No review version yet');
  assert.equal(doc.elements['#plan-content'].hidden, false);
  assert.equal(doc.elements['#plan-missing'].hidden, true);

  const editor = doc.elements['#plan-editor-groups'].innerHTML;
  assert.match(editor, /Snapshot staging/);
  assert.match(editor, /Verify checksums/);
  // The status badge carries an icon and a word, not just a color.
  assert.match(doc.elements['#plan-status-badge'].innerHTML, /plan-status-label/);
  assert.match(doc.elements['#plan-status-badge'].innerHTML, /Draft/);
});

test('reorder controls are disabled at the edges rather than missing', async () => {
  const { page, doc } = makePage();
  await page.reload();

  const html = doc.elements['#plan-editor-groups'].innerHTML;
  assert.match(html, /data-action="item-up" data-item-id="itm-1" disabled/);
  assert.match(html, /data-action="item-down" data-item-id="itm-2" disabled/);
  // Reordering is done with buttons, so it never requires dragging (FR-160).
  assert.match(html, /Move up<\/button>/);
  assert.match(html, /Move down<\/button>/);
});

test('a plan with no groups shows an empty state', async () => {
  const plan = planFixture();
  plan.draft.groups = [];
  const { page, doc } = makePage({ plan });
  await page.reload();

  assert.equal(doc.elements['#plan-editor-empty'].hidden, false);
  assert.equal(doc.elements['#plan-editor-groups'].innerHTML, '');
});

test('clarifications render with their answer state and stay hidden when absent', async () => {
  const plan = planFixture();
  plan.draft.clarifications = [
    {
      id: 'c1',
      prompt: 'Which environment?',
      required: true,
      status: 'answered',
      answer: 'Staging'
    },
    { id: 'c2', prompt: 'Any deadline?', required: false, status: 'skipped', skip_reason: 'none' }
  ];
  const { page, doc } = makePage({ plan });
  await page.reload();

  assert.equal(doc.elements['#plan-clarifications-section'].hidden, false);
  const html = doc.elements['#plan-clarifications-list'].innerHTML;
  assert.match(html, /Which environment\?/);
  assert.match(html, /Answered: Staging/);
  assert.match(html, /Skipped — none/);

  const { page: empty, doc: emptyDoc } = makePage();
  await empty.reload();
  assert.equal(emptyDoc.elements['#plan-clarifications-section'].hidden, true);
});

// --- Editing ---------------------------------------------------------------

test('adding a group marks the draft unsaved and announces the change', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.addGroup('Verify');

  assert.equal(page.editor.content.groups.length, 2);
  assert.equal(doc.elements['#plan-save-state'].dataset.state, 'unsaved');
  assert.match(doc.elements['#plan-save-state'].textContent, /Unsaved changes/);
  assert.equal(doc.elements['#plan-detail-status'].textContent, 'Task group added');
});

// Removing depended-on work is refused with the specifics, and nothing changes
// (FR-51).
test('removing a depended-on task is refused and explains why', async () => {
  const { page, doc } = makePage();
  await page.reload();

  const result = page.removeItem('itm-1');

  assert.equal(result.removed, false);
  assert.equal(page.editor.content.groups[0].items.length, 2, 'the task was removed anyway');
  const message = doc.elements['#plan-detail-status'].textContent;
  assert.match(message, /Cannot remove this yet/);
  assert.match(message, /Verify checksums/, 'the message does not name the dependent task');
  assert.equal(doc.elements['#plan-detail-status'].dataset.tone, 'error');
});

test('resolving the dependency then removing succeeds', async () => {
  const { page } = makePage();
  await page.reload();

  page.resolveDependencies('itm-1');
  const result = page.removeItem('itm-1');

  assert.equal(result.removed, true);
  assert.equal(page.editor.content.groups[0].items.length, 1);
});

test('reordering through the page preserves ids', async () => {
  const { page } = makePage();
  await page.reload();

  page.moveItem('itm-2', 'up');

  assert.deepEqual(
    page.editor.content.groups[0].items.map(item => item.id),
    ['itm-2', 'itm-1']
  );
  // The dependency still points at the same task.
  assert.deepEqual(page.editor.content.groups[0].items[0].depends_on, ['itm-1']);
});

// An assignee that vanished must be surfaced on the item, not silently dropped
// (FR-48).
test('an unavailable assignee is flagged on the task', async () => {
  const plan = planFixture();
  plan.draft.groups[0].items[0].assignee = 'ghost';
  const { page, doc } = makePage({ plan });
  await page.reload();

  page.availableAgents = ['builder'];
  page.renderEditor();

  const html = doc.elements['#plan-editor-groups'].innerHTML;
  assert.match(html, /no longer available/);
  assert.match(html, /ghost/);
});

test('assignees are not flagged before the roster is known', async () => {
  const plan = planFixture();
  plan.draft.groups[0].items[0].assignee = 'builder';
  const { page, doc } = makePage({ plan });
  await page.reload();

  // availableAgents is null: "not checked", not "nothing available".
  assert.doesNotMatch(doc.elements['#plan-editor-groups'].innerHTML, /no longer available/);
});

// --- Saving ----------------------------------------------------------------

test('saving sends the current revision and returns to the saved state', async () => {
  let sent = null;
  const { page, doc } = makePage({
    onSave: body => {
      sent = body;
      return jsonResponse({ ...planFixture(), draft_revision: 3 });
    }
  });
  await page.reload();

  page.addGroup('Verify');
  await page.save();

  assert.equal(sent.revision, 2, 'the save did not carry the loaded revision');
  assert.equal(sent.autosave, false);
  assert.equal(page.editor.revision, 3);
  assert.equal(doc.elements['#plan-save-state'].dataset.state, 'saved');
  assert.equal(doc.elements['#plan-detail-status'].textContent, 'All changes saved');
});

test('an edit schedules an autosave that marks the recovery point', async () => {
  let sent = null;
  const { page, runTimers } = makePage({
    onSave: body => {
      sent = body;
      return jsonResponse({ ...planFixture(), draft_revision: 3 });
    }
  });
  await page.reload();

  page.addGroup('Verify');
  assert.equal(sent, null, 'the edit saved immediately instead of scheduling');

  runTimers();
  await new Promise(resolve => setImmediate(resolve));

  assert.ok(sent, 'the scheduled autosave never ran');
  assert.equal(sent.autosave, true, 'the autosave did not request a recovery point');
});

// A conflict keeps both versions and offers a choice; it is not a retryable
// error (FR-30, FR-151).
test('a stale save surfaces a conflict with both versions kept', async () => {
  const { page, doc } = makePage({
    onSave: () =>
      jsonResponse(
        {
          code: 'stale_draft',
          message: 'plan draft revision is stale',
          details: {
            current_revision: 7,
            current: { ...planFixture(), objective: 'Theirs' },
            recoverable_snapshots: [{ id: 'snap-1' }]
          }
        },
        false,
        409
      )
  });
  await page.reload();

  page.addGroup('Mine');
  await page.save();

  assert.equal(page.editor.state, 'conflicted');
  assert.equal(doc.elements['#plan-save-state'].dataset.state, 'conflicted');
  assert.match(doc.elements['#plan-save-state'].textContent, /Someone else saved first/);
  assert.equal(doc.elements['#plan-conflict-panel'].hidden, false);
  assert.match(doc.elements['#plan-conflict-summary'].textContent, /revision 7/);

  // The user's work is still here.
  assert.equal(page.editor.content.groups.length, 2);
  assert.equal(page.editor.conflict.mine.groups.length, 2);
  assert.equal(page.editor.conflict.current.objective, 'Theirs');
  assert.match(doc.elements['#plan-detail-status'].textContent, /Your changes are kept/);
});

test('keeping mine after a conflict retries against the winning revision', async () => {
  const sent = [];
  let attempt = 0;
  const { page } = makePage({
    onSave: body => {
      sent.push(body);
      attempt += 1;
      if (attempt === 1) {
        return jsonResponse(
          {
            code: 'stale_draft',
            message: 'stale',
            details: { current_revision: 7, current: planFixture() }
          },
          false,
          409
        );
      }
      return jsonResponse({ ...planFixture(), draft_revision: 8 });
    }
  });
  await page.reload();

  page.addGroup('Mine');
  await page.save();
  await page.keepMineAfterConflict();

  assert.equal(sent.length, 2);
  assert.equal(sent[1].revision, 7, 'the retry did not use the winning revision');
  assert.equal(page.editor.state, 'saved');
  assert.equal(page.editor.revision, 8);
});

test('a save failure that is not a conflict returns to unsaved', async () => {
  const { page, doc } = makePage({
    onSave: () => jsonResponse({ code: 'internal_error', message: 'boom' }, false, 500)
  });
  await page.reload();

  page.addGroup('Mine');
  await page.save();

  assert.equal(page.editor.state, 'unsaved', 'a failed save reported itself as saved');
  assert.match(doc.elements['#plan-detail-status'].textContent, /Could not save: boom/);
});

// A refresh must not throw away work in progress.
test('reloading does not replace an editor with unsaved changes', async () => {
  const { page } = makePage();
  await page.reload();

  page.addGroup('Mine');
  const before = page.editor.content.groups.length;

  await page.reload();

  assert.equal(page.editor.content.groups.length, before, 'a reload discarded unsaved work');
  assert.equal(page.editor.state, 'unsaved');
});

// --- Review and approval ---------------------------------------------------

// A side-effecting approval never hides behind a generic label (FR-64, FR-65).
test('the approval button is labelled by what it does', () => {
  assert.equal(
    approvalButtonLabel({ action_label: 'Approve and Start', starts_execution: true }),
    'Approve and Start'
  );
  assert.equal(
    approvalButtonLabel({ action_label: 'Approve and Create Tasks' }),
    'Approve and Create Tasks'
  );
  // A contract with no label is a bug, not a reason to render a bare
  // "Approve" over an action that starts agents.
  assert.equal(approvalButtonLabel({ starts_execution: true }), 'Approve and Start');
  assert.equal(approvalButtonLabel({}), 'Approve and Create Tasks');
  assert.equal(approvalButtonLabel(null), 'Approve and Create Tasks');
});

// The diff says what happened, not only that something did (FR-36).
test('diffSummary distinguishes added, removed, moved, and modified', () => {
  const lines = diffSummary({
    objective: { before: 'Old', after: 'New' },
    in_scope: [{ kind: 'added', value: 'archival' }],
    groups: [{ kind: 'moved', title: 'Prepare', from_index: 1, to_index: 0 }],
    items: [
      { kind: 'added', description: 'A new task' },
      { kind: 'removed', description: 'A dropped task' },
      { kind: 'modified', description: 'A changed task', fields: ['assignee'] },
      {
        kind: 'moved',
        description: 'A reordered task',
        from_index: 0,
        to_index: 1,
        group_id: 'grp-1',
        from_group_id: 'grp-1'
      }
    ],
    execution: { before: 'step_through', after: 'auto' },
    preconditions: [{ kind: 'added', value: 'safe_branch' }]
  }).join('\n');

  assert.match(lines, /Objective changed to “New”/);
  assert.match(lines, /Scope added: archival/);
  assert.match(lines, /Group moved: Prepare \(position 2 → 1\)/);
  assert.match(lines, /Task added: A new task/);
  assert.match(lines, /Task removed: A dropped task/);
  assert.match(lines, /Task modified: A changed task \(assignee\)/);
  assert.match(lines, /Task reordered: A reordered task/);
  assert.match(lines, /Execution mode changed from step_through to auto/);
  assert.match(lines, /Enforced precondition added: safe_branch/);
});

test('diffSummary reports an identical comparison plainly', () => {
  assert.deepEqual(diffSummary({ identical: true }), ['No approval-relevant changes']);
  assert.deepEqual(diffSummary(null), ['No approval-relevant changes']);
});

test('an item moving between groups reads differently from a reorder', () => {
  const across = diffSummary({
    items: [
      { kind: 'moved', description: 'Switch traffic', group_id: 'grp-1', from_group_id: 'grp-2' }
    ]
  })[0];
  assert.match(across, /moved to another group/);
});

function contractFixture(overrides = {}) {
  return {
    plan_id: 'plan_1',
    version: 2,
    content_hash: 'hash-abc',
    objective: 'Migrate reporting safely',
    task_count: 2,
    group_count: 1,
    dependency_count: 1,
    unassigned: 1,
    effect: 'create_tasks',
    action_label: 'Approve and Create Tasks',
    starts_execution: false,
    approvable: true,
    blockers: [],
    effects: [
      'Create 2 workspace task(s) in 1 group(s)',
      'Nothing starts running until you start it'
    ],
    ...overrides
  };
}

test('the review contract renders every effect approval authorizes', async () => {
  const contract = contractFixture();
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async () => jsonResponse(contract);
  await page.loadReviewContract(2);

  assert.equal(doc.elements['#plan-review-panel'].hidden, false);
  assert.equal(doc.elements['#plan-review-version'].textContent, 'Version 2');
  assert.match(doc.elements['#plan-review-counts'].textContent, /2 task\(s\) in 1 group\(s\)/);
  assert.match(doc.elements['#plan-review-effects'].innerHTML, /Create 2 workspace task\(s\)/);
  assert.equal(doc.elements['#plan-approve'].textContent, 'Approve and Create Tasks');
  assert.equal(doc.elements['#plan-approve'].disabled, false);
  assert.equal(doc.elements['#plan-review-blockers'].hidden, true);
});

// A blocked approval is disabled with a reason rather than failing on click
// (FR-48).
test('blockers disable the approval action and say why', async () => {
  const contract = contractFixture({
    approvable: false,
    blockers: ['Agent "builder" is no longer available; reassign that task or leave it unassigned']
  });
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async () => jsonResponse(contract);
  await page.loadReviewContract(2);

  assert.equal(doc.elements['#plan-approve'].disabled, true);
  assert.equal(doc.elements['#plan-review-blockers'].hidden, false);
  assert.match(doc.elements['#plan-review-blockers'].innerHTML, /no longer available/);
});

test('an auto plan warns that approval starts work', async () => {
  const contract = contractFixture({
    action_label: 'Approve and Start',
    starts_execution: true,
    effect: 'create_tasks_and_start',
    effects: [
      'Create 2 workspace task(s) in 1 group(s)',
      'Start running eligible tasks automatically'
    ]
  });
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async () => jsonResponse(contract);
  await page.loadReviewContract(2);

  assert.equal(doc.elements['#plan-approve'].textContent, 'Approve and Start');
  assert.equal(doc.elements['#plan-approve'].dataset.startsExecution, 'true');
  assert.match(doc.elements['#plan-review-effects'].innerHTML, /Start running/);
});

// Approval binds to the exact version and hash the contract carried (FR-61).
test('approving sends the reviewed version and hash', async () => {
  let sent = null;
  const { page, doc } = makePage();
  await page.reload();

  page.contract = contractFixture();
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/approvals') && options.method === 'POST') {
      sent = JSON.parse(options.body);
      return jsonResponse({ id: 'apr_1' }, true, 201);
    }
    if (url.endsWith('/materialize') && options.method === 'POST') {
      return jsonResponse({ task_ids: ['task-a'], start_execution: false });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.approve({ name: 'jj' });

  assert.equal(sent.version, 2);
  assert.equal(sent.content_hash, 'hash-abc');
  assert.equal(sent.effect, 'create_tasks');
  assert.ok(sent.idempotency_key.includes('hash-abc'), sent.idempotency_key);
  // The message that lands describes what actually happened, and still
  // answers the question the button's label was careful about.
  assert.match(
    doc.elements['#plan-detail-status'].textContent,
    /Nothing starts until you start it/
  );
});

// "Created 3 tasks" alone leaves the user guessing whether anything is now
// running — the question the approval label existed to answer (FR-64, FR-65).
test('the materialization message says what was created and what happens next', () => {
  assert.equal(
    materializedMessage({ task_ids: ['a', 'b'], start_execution: false }),
    'Created 2 task(s). Nothing starts until you start it.'
  );
  assert.equal(
    materializedMessage({ task_ids: ['a'], artifact_paths: ['x.md'], start_execution: true }),
    'Created 1 task(s) and 1 file(s). Eligible tasks will start automatically.'
  );
  assert.match(
    materializedMessage({ task_ids: ['a'], replayed: true }),
    /^This approval was already used\. It created 1 task\(s\)\./
  );
});

test('an auto approval says work will start', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.contract = contractFixture({ starts_execution: true, effect: 'create_tasks_and_start' });
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/approvals') && options.method === 'POST') {
      return jsonResponse({ id: 'apr_1' }, true, 201);
    }
    if (url.endsWith('/materialize') && options.method === 'POST') {
      return jsonResponse({ task_ids: ['task-a'], start_execution: true });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.approve();
  assert.match(doc.elements['#plan-detail-status'].textContent, /start automatically/);
});

// A stale approval reloads the real current version rather than retrying
// against one that moved (FR-69).
test('a stale approval reloads instead of retrying', async () => {
  let reloaded = false;
  const { page, doc } = makePage();
  await page.reload();

  page.contract = contractFixture();
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/approvals') && options.method === 'POST') {
      return jsonResponse(
        { code: 'approval_mismatch', message: 'this plan changed since you reviewed it.' },
        false,
        409
      );
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    if (url.includes('/versions/')) return jsonResponse(contractFixture({ version: 3 }));
    reloaded = true;
    return jsonResponse({ ...planFixture(), current_version: 3 });
  };

  const result = await page.approve();

  assert.equal(result, null);
  assert.ok(reloaded, 'a stale approval did not reload the plan');
  assert.match(doc.elements['#plan-detail-status'].textContent, /changed since you reviewed it/);
  assert.equal(page.contract.version, 3, 'the reviewer was not moved to the current version');
});

test('requesting changes clears the review panel and says the version is kept', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.contract = contractFixture();
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/decision') && options.method === 'POST') {
      return jsonResponse({ ...planFixture(), status: 'draft' });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.decide('request_changes', 'too wide');

  assert.equal(page.contract, null);
  assert.equal(doc.elements['#plan-review-panel'].hidden, true);
  assert.match(doc.elements['#plan-detail-status'].textContent, /reviewed version is kept/);
});

test('rejecting says the version stays in history', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.contract = contractFixture();
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/decision') && options.method === 'POST') {
      return jsonResponse({ ...planFixture(), status: 'draft' });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.decide('reject', 'wrong approach');
  assert.match(doc.elements['#plan-detail-status'].textContent, /kept in the plan’s history/);
});

test('comparing versions renders the change summary', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async () =>
    jsonResponse({
      from: 1,
      to: 2,
      items: [{ kind: 'added', description: 'A new task' }]
    });

  await page.compareVersions(1, 2);

  assert.equal(doc.elements['#plan-compare-panel'].hidden, false);
  assert.match(doc.elements['#plan-compare-list'].innerHTML, /Task added: A new task/);
});

test('approving without a loaded contract asks for the review first', async () => {
  const { page, doc } = makePage();
  await page.reload();
  page.contract = null;

  const result = await page.approve();
  assert.equal(result, null);
  assert.match(doc.elements['#plan-detail-status'].textContent, /Load the review before approving/);
});

// --- Materialization -------------------------------------------------------

test('materializing reports the work it created and links to it', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/materialize') && options.method === 'POST') {
      return jsonResponse({
        task_ids: ['task-a', 'task-b'],
        artifact_paths: ['tasks/prd.md'],
        replayed: false
      });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.materialize('apr-1');

  assert.equal(doc.elements['#plan-materialization-panel'].hidden, false);
  assert.match(doc.elements['#plan-materialization-summary'].textContent, /Created 2 task\(s\)/);
  assert.match(doc.elements['#plan-materialization-summary'].textContent, /1 file\(s\)/);
  // It links to the tasks rather than restating their state (FR-11).
  assert.match(
    doc.elements['#plan-materialization-tasks'].innerHTML,
    /workspaces\/ws-1\/task\/task-a/
  );
  assert.equal(doc.elements['#plan-materialization-artifacts'].hidden, false);
  assert.match(doc.elements['#plan-materialization-artifacts'].innerHTML, /tasks\/prd\.md/);
  // Nothing failed, so nothing is offered to retry.
  assert.equal(doc.elements['#plan-materialization-retry'].hidden, true);
});

test('a replayed materialization says the approval was already used', async () => {
  const { page, doc } = makePage();
  await page.reload();

  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/materialize') && options.method === 'POST') {
      return jsonResponse({ task_ids: ['task-a'], replayed: true });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.materialize('apr-1');
  assert.match(doc.elements['#plan-materialization-summary'].textContent, /already been used/);
});

// A failed materialization keeps the approval usable, so the panel offers a
// retry rather than sending the user back to re-approve (FR-99).
test('a failed materialization offers a retry', async () => {
  const { page, doc } = makePage();
  await page.reload();

  let attempts = 0;
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/materialize') && options.method === 'POST') {
      attempts += 1;
      if (attempts === 1) {
        return jsonResponse({ code: 'internal_error', message: 'disk full' }, false, 500);
      }
      return jsonResponse({ task_ids: ['task-a'], replayed: false });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.materialize('apr-1');

  assert.match(doc.elements['#plan-materialization-summary'].textContent, /disk full/);
  assert.equal(doc.elements['#plan-materialization-retry'].hidden, false);
  assert.equal(doc.elements['#plan-materialization-retry'].dataset.approvalId, 'apr-1');
  assert.match(
    doc.elements['#plan-detail-status'].textContent,
    /Could not create the approved work/
  );

  // Retrying with the same approval succeeds.
  await page.materialize('apr-1');
  assert.equal(doc.elements['#plan-materialization-retry'].hidden, true);
  assert.match(doc.elements['#plan-materialization-summary'].textContent, /Created 1 task\(s\)/);
});

// Approving and spending the approval are one user gesture: the button said it
// would create tasks, so it creates them.
test('approving materializes the approved work', async () => {
  let materialized = null;
  const { page } = makePage();
  await page.reload();

  page.contract = contractFixture();
  page.fetchImpl = async (url, options = {}) => {
    if (url.endsWith('/approvals') && options.method === 'POST') {
      return jsonResponse({ id: 'apr-9' }, true, 201);
    }
    if (url.endsWith('/materialize') && options.method === 'POST') {
      materialized = JSON.parse(options.body);
      return jsonResponse({ task_ids: ['task-a'], replayed: false });
    }
    if (url.endsWith('/activity')) return jsonResponse({ activity: [] });
    return jsonResponse(planFixture());
  };

  await page.approve({ name: 'jj' });

  assert.ok(materialized, 'approving did not create the approved work');
  assert.equal(materialized.approval_id, 'apr-9');
});

test('a missing plan renders the not-found state', async () => {
  const doc = fakeDocument();
  const page = new WorkspacePlanPage('ws-1', 'plan_1', {
    document: doc,
    fetch: async () =>
      jsonResponse({ code: 'plan_not_found', message: 'plan not found' }, false, 404)
  });

  await page.reload();

  assert.equal(doc.elements['#plan-missing'].hidden, false);
  assert.equal(doc.elements['#plan-content'].hidden, true);
});
