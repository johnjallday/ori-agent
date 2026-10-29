import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView, coveredSentence, goalSuggestionOf } from './workspace-command.js';

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
    confidence: 'medium',
    action: ''
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

test('the drawer says a goal run is looking for work while one is in flight (FR27)', () => {
  const view = makeView([suggested]);
  globalThis.window = { workspaceMission: { isRunning: () => true } };
  try {
    assert.match(
      view.backlogDrawerHTML(),
      /<div class="ws-cmd-drawer-goal-run" role="status">Looking for things to work on…<\/div>/
    );
    globalThis.window.workspaceMission.isRunning = () => false;
    assert.ok(!view.backlogDrawerHTML().includes('ws-cmd-drawer-goal-run'));
  } finally {
    delete globalThis.window;
  }
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

// A suggestion that meets the backup precondition (backup-first FR15).
const backup = {
  task: {
    id: 'b1',
    description: 'Back up Downloads before the first filing batch',
    source_type: 'goal_suggestion',
    source_id: 'run-2',
    context: {
      goal_suggestion: {
        why: 'Nothing protects the originals',
        first_step: 'Open Check protection and approve one metadata read.',
        precondition: 'backup_protection',
        action: 'check_protection',
        source: 'host',
        run_id: 'run-2'
      }
    }
  },
  owning_workspace_id: 'w1',
  owning_workspace_name: 'Downloads'
};

test('only a suggestion with the check_protection action leads with Check protection', () => {
  const view = makeView([backup, suggested]);
  assert.equal(goalSuggestionOf(backup.task).action, 'check_protection');
  view.backlogDrawerSelectedId = 'b1';
  const html = view.backlogDrawerPreviewHTML();
  const actions = html.slice(html.indexOf('ws-cmd-drawer-preview-actions'));
  assert.match(
    actions,
    /^ws-cmd-drawer-preview-actions"><button type="button" class="ws-cmd-drawer-action" data-cmd-backlog-check-protection data-cmd-backlog-item="b1">Check protection<\/button>/
  );
  assert.ok(
    actions.indexOf('Check protection') < actions.indexOf('Not useful'),
    'Check protection comes before Not useful'
  );
  view.backlogDrawerSelectedId = 's1';
  assert.ok(!view.backlogDrawerPreviewHTML().includes('Check protection'));
  // An unknown action id offers no button.
  const unknown = structuredClone(backup);
  unknown.task.context.goal_suggestion.action = 'launch_rockets';
  const other = makeView([unknown]);
  other.backlogDrawerSelectedId = 'b1';
  assert.ok(!other.backlogDrawerPreviewHTML().includes('data-cmd-backlog-check-protection'));
});

test('Check protection reads the owning workspace fact and opens its destination', async () => {
  const steps = [];
  const view = makeView([backup]);
  view.protection = {
    readProtection: async id => {
      steps.push(['read', id]);
      return { workspace_id: id, state: 'unknown', destination: { kind: 'chooser' } };
    },
    openDestination: async target => steps.push(['open', target.kind])
  };
  view.backlogDrawerSelectedId = 'b1';
  await view.runBacklogCheckProtection('b1');
  assert.deepEqual(steps, [
    ['read', 'w1'],
    ['open', 'chooser']
  ]);
  assert.deepEqual(view.backlogSuggestionAction, { id: 'b1', busy: false, error: '' });
  assert.ok(!view.backlogDrawerPreviewHTML().includes('role="alert"'));
});

test('a covered folder says Covered with its evidence time instead of the button, and opens nothing', async () => {
  const observed = '2026-10-28T08:30:00Z';
  const view = makeView([backup]);
  view.protection = {
    readProtection: async id => ({
      workspace_id: id,
      state: 'covered',
      observed_at: observed,
      destination: { kind: 'chooser' }
    }),
    openDestination: async () => assert.fail('a covered folder opened protection')
  };
  view.backlogDrawerSelectedId = 'b1';
  await view.runBacklogCheckProtection('b1');
  const html = view.backlogDrawerPreviewHTML();
  const local = new Date(observed).toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short'
  });
  assert.ok(
    html.includes(
      '<span class="ws-cmd-backlog-action-covered" role="status">Covered · evidence observed ' +
        local +
        '</span>'
    ),
    html
  );
  assert.ok(!html.includes('data-cmd-backlog-check-protection'), 'the button is replaced');
  // The ticket is left alone: Not useful and Delete are still offered.
  assert.match(html, /data-cmd-backlog-not-useful data-cmd-backlog-item="b1">Not useful</);
  assert.equal(coveredSentence(''), 'Covered');
});

test('a Check protection that cannot open says so on the card and changes nothing', async () => {
  const view = makeView([backup]);
  view.protection = {
    readProtection: async () => {
      throw new Error('Protection is unavailable for this folder.');
    },
    openDestination: async () => assert.fail('opened without a fact')
  };
  view.backlogDrawerSelectedId = 'b1';
  await view.runBacklogCheckProtection('b1');
  const html = view.backlogDrawerPreviewHTML();
  assert.match(
    html,
    /<p class="ws-cmd-backlog-action-error" role="alert">Protection is unavailable for this folder\.<\/p>/
  );
  assert.match(html, /data-cmd-backlog-check-protection data-cmd-backlog-item="b1">/);
  assert.ok(!/data-cmd-backlog-check-protection[^>]*disabled/.test(html), 'it can be tried again');
  // Without the protection module at all, the same sentence.
  const bare = makeView([backup]);
  await bare.runBacklogCheckProtection('b1');
  assert.equal(bare.backlogSuggestionAction.error, 'Protection is unavailable for this folder.');
});
