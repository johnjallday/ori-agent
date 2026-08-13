import { test } from 'node:test';
import assert from 'node:assert/strict';

import { activityLine, clarificationLine, WorkspacePlanPage } from './workspace-plan-detail.js';

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
