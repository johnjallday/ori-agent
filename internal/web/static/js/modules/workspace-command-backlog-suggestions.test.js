import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView, goalSuggestionOf } from './workspace-command.js';

// Goal-run suggestions in the Backlog drawer (workspace-goal-suggestions FR20).
// Same headless fixture as workspace-command-backlog.test.js.
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function makeView(backlogItems, extraPage = {}) {
  const view = new WorkspaceCommandView(null);
  view.page = {
    backlogItems,
    backlogSync: null,
    backlogIncludeDescendants: false,
    workspaceId: 'w1',
    ...extraPage
  };
  view.render = () => {};
  view.ensureBacklogDrawer = () => null;
  view.renderBacklogDrawerBody = () => {};
  return view;
}

const suggested = {
  task: {
    id: 's1',
    description: 'Draft a brand voice guide',
    details: '**Why:** Three posts drift in tone\n\n**Evidence:** notes/posts.md',
    source_type: 'goal_suggestion',
    source_id: 'run-1',
    context: {
      goal_suggestion: {
        why: 'Three posts drift in tone',
        evidence: 'notes/<posts>.md',
        first_step: 'Collect five on-brand posts',
        suggested_agent: 'Writer',
        effort: 'small',
        confidence: 'medium',
        run_id: 'run-1'
      }
    }
  },
  owning_workspace_id: 'w1',
  owning_workspace_name: 'Brand'
};

const ordinary = {
  task: { id: 'o1', description: 'My own idea', details: 'Some **markdown** details' },
  owning_workspace_id: 'w1',
  owning_workspace_name: 'Brand'
};

test('goalSuggestionOf reads a suggestion and ignores other tickets', () => {
  assert.deepEqual(goalSuggestionOf(suggested.task), {
    why: 'Three posts drift in tone',
    evidence: 'notes/<posts>.md',
    firstStep: 'Collect five on-brand posts',
    suggestedAgent: 'Writer',
    effort: 'small',
    confidence: 'medium'
  });
  assert.equal(goalSuggestionOf(ordinary.task), null);
  assert.equal(goalSuggestionOf(null), null);
});

test('drawer rows badge a suggestion and show its why as the second line; other rows are unchanged', () => {
  const view = makeView([suggested, ordinary]);
  const html = view.backlogDrawerListHTML([suggested, ordinary]);
  const [suggestedRow, ordinaryRow] = html.split('</button>');
  assert.match(suggestedRow, /class="ws-cmd-drawer-row is-suggested"/);
  assert.match(suggestedRow, /<span class="ws-cmd-drawer-row-suggested">Suggested<\/span>/);
  assert.match(
    suggestedRow,
    /<span class="ws-cmd-drawer-row-why">Three posts drift in tone<\/span>/
  );
  assert.ok(!ordinaryRow.includes('Suggested'), 'an ordinary ticket carries no badge');
  assert.ok(!ordinaryRow.includes('ws-cmd-drawer-row-why'));
});

test('a suggestion preview shows why, an Evidence expander and Not useful instead of raw Markdown', () => {
  const view = makeView([suggested, ordinary]);
  view.backlogDrawerSelectedId = 's1';
  const html = view.backlogDrawerPreviewHTML();
  assert.match(html, /Backlog · Suggested/);
  assert.match(html, /ws-cmd-suggestion-why">Three posts drift in tone</);
  assert.match(html, /<details class="ws-cmd-suggestion-details"><summary>Evidence<\/summary>/);
  assert.match(html, /notes\/&lt;posts&gt;\.md/);
  assert.match(html, /First step:<\/strong> Collect five on-brand posts/);
  assert.match(html, /Suggested agent: Writer · Effort: small · Confidence: medium/);
  assert.match(html, /data-cmd-backlog-not-useful data-cmd-backlog-item="s1">Not useful</);
  assert.match(html, /data-cmd-backlog-promote data-cmd-backlog-item="s1"/, 'Promote stays');
  assert.ok(!html.includes('**Why:**'), 'no raw Markdown for a suggestion');

  view.backlogDrawerSelectedId = 'o1';
  const plain = view.backlogDrawerPreviewHTML();
  assert.ok(!plain.includes('Not useful'), 'Not useful is only offered on suggestions');
  assert.ok(!plain.includes('Suggested'));
  assert.match(plain, /ws-cmd-drawer-preview-brief">Some \*\*markdown\*\* details</);
});

test('Not useful asks for an optional reason, then removes the suggestion once', async () => {
  const calls = [];
  const view = makeView([suggested], {
    dismissBacklogSuggestion: (id, owner, reason) => {
      calls.push({ id, owner, reason });
      return Promise.resolve(true);
    }
  });
  view.backlogDrawerSelectedId = 's1';
  view.openBacklogNotUseful('s1');
  const form = view.backlogDrawerPreviewHTML();
  assert.match(form, /data-cmd-backlog-not-useful-form data-cmd-backlog-item="s1"/);
  assert.match(form, /Why isn’t it useful\? \(optional\)/);
  assert.match(form, /maxlength="200"/);
  assert.match(form, /data-cmd-backlog-not-useful-cancel/);

  view.backlogNotUsefulReason = '  We already have one  ';
  await view.runBacklogNotUseful();
  await view.runBacklogNotUseful(); // a second submit after success does nothing
  assert.deepEqual(calls, [{ id: 's1', owner: 'w1', reason: 'We already have one' }]);
  assert.equal(view.backlogNotUsefulId, '', 'the form closes after removal');

  view.openBacklogNotUseful('s1');
  view.closeBacklogNotUseful();
  assert.equal(view.backlogNotUsefulId, '');
});

test('a failed Not useful keeps the form open with the reason', async () => {
  const view = makeView([suggested], { dismissBacklogSuggestion: () => Promise.resolve(false) });
  view.openBacklogNotUseful('s1');
  view.backlogNotUsefulReason = 'keep me';
  await view.runBacklogNotUseful();
  assert.equal(view.backlogNotUsefulId, 's1');
  assert.equal(view.backlogNotUsefulReason, 'keep me');
  assert.equal(view.backlogNotUsefulBusy, false);
});
