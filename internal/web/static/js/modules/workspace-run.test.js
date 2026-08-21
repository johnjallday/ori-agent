import { test } from 'node:test';
import assert from 'node:assert/strict';

import { formatTaskOutputStatus, WorkspaceRunPage } from './workspace-run.js';

test('formatTaskOutputStatus maps task output validation states to PRD labels', () => {
  assert.equal(
    formatTaskOutputStatus({ validation_status: 'passed', storage_status: 'appended' }),
    'Saved'
  );
  assert.equal(
    formatTaskOutputStatus({
      validation_status: 'needs_review',
      storage_status: 'skipped_invalid'
    }),
    'Needs Review'
  );
  assert.equal(formatTaskOutputStatus({ validation_status: 'dismissed' }), 'Dismissed');
  assert.equal(
    formatTaskOutputStatus({
      validation_status: 'manually_approved',
      storage_status: 'manually_appended'
    }),
    'Manually Approved'
  );
});

test('workspace run keeps UUID APIs separate from slug task and run links', () => {
  const page = new WorkspaceRunPage('workspace-uuid', 'run-1', {
    workspaceSlug: 'marketing-site'
  });

  assert.equal(page.taskHref('task/1'), '/workspaces/marketing-site/task/task%2F1');
  assert.equal(page.runHref('run/2'), '/workspaces/marketing-site/runs/run%2F2');
  assert.match(page.workspaceId, /workspace-uuid/);
});

test('workspace run overview and report render reference URL details', () => {
  const page = new WorkspaceRunPage('workspace-1', 'run-1');
  page.run = {
    id: 'run-1',
    workspace_id: 'workspace-1',
    created_at: '2026-01-01T00:00:00Z',
    status: 'succeeded',
    reference_url: 'https://example.com/spec',
    report: {
      summary: 'Done',
      validation_status: 'passed',
      reference_url_inspection: {
        status: 'blocked',
        detail: 'Login required'
      }
    }
  };
  page.elements = {
    overview: { innerHTML: '' },
    reportCard: { hidden: true },
    report: { innerHTML: '' }
  };

  page.renderOverview();
  assert.match(page.elements.overview.innerHTML, /Reference URL/);
  assert.match(page.elements.overview.innerHTML, /https:\/\/example\.com\/spec/);
  assert.match(page.elements.overview.innerHTML, /target="_blank"/);

  page.renderReport();
  assert.equal(page.elements.reportCard.hidden, false);
  assert.match(page.elements.report.innerHTML, /Reference URL Inspection/);
  assert.match(page.elements.report.innerHTML, /blocked/);
  assert.match(page.elements.report.innerHTML, /Login required/);
});

// --- Wrap-up: the run's immutable toolbox and what it measurably used ---
//
// renderToolbox reads its hosts from the document rather than page.elements, so
// these stub just those two nodes.

function withToolboxHosts(run) {
  const card = { hidden: true };
  const host = { innerHTML: '' };
  const previousDocument = globalThis.document;
  globalThis.document = {
    getElementById: id => {
      if (id === 'workspace-run-toolbox-card') return card;
      if (id === 'workspace-run-toolbox') return host;
      return null;
    }
  };

  const page = new WorkspaceRunPage('workspace-1', 'run-1');
  page.run = run;
  page.renderToolbox();
  globalThis.document = previousDocument;
  return { card, host };
}

test('the wrap-up shows the exact capabilities the run was given (FR-107, FR-108)', () => {
  const { card, host } = withToolboxHosts({
    id: 'run-1',
    toolbox_snapshot: {
      toolbox_name: 'Research Kit',
      toolbox_version: 3,
      focus_state: 'Focused',
      hash: 'abcdef0123456789',
      pinned_by_goal: true,
      skills: [
        { capability_id: 'summarize', display_name: 'summarize', source: 'workspace_provided' }
      ],
      mcp_bindings: [
        { binding_id: 'mb-notes', alias: 'Notes', allowed_tools: ['read_note', 'write_note'] }
      ]
    }
  });

  assert.equal(card.hidden, false);
  assert.match(host.innerHTML, /Research Kit v3/);
  assert.match(host.innerHTML, /pinned by this goal/);
  assert.match(host.innerHTML, /Focus: Focused/);
  assert.match(host.innerHTML, /1 skills/);
  assert.match(host.innerHTML, /2 operations/);
  assert.match(host.innerHTML, /Notes: read_note, write_note/);
  // The hash is how a reader knows whether the toolbox has changed since.
  assert.match(host.innerHTML, /Snapshot abcdef012345/);
});

test('the wrap-up separates measured use from unused capability (FR-115, FR-116)', () => {
  const { host } = withToolboxHosts({
    id: 'run-1',
    toolbox_snapshot: {
      toolbox_name: 'Research Kit',
      toolbox_version: 1,
      mcp_bindings: [
        { binding_id: 'mb-notes', alias: 'Notes', allowed_tools: ['read_note', 'write_note'] }
      ]
    },
    toolbox_wrap_up: {
      operations: [{ tool: 'read_note', calls: 4, side_effect: 'read', failures: 1 }],
      unused_operations: ['write_note'],
      blocked_calls: 2,
      retries: 1,
      approval_requests: 0,
      connection_failures: ['mb-web']
    }
  });

  assert.match(host.innerHTML, /Used \(measured\)/);
  assert.match(host.innerHTML, /read_note &times;4/);
  assert.match(host.innerHTML, /1 failed/);
  assert.match(host.innerHTML, /Available but never used/);
  assert.match(host.innerHTML, /write_note/);
  assert.match(host.innerHTML, /2 blocked/);
  assert.match(host.innerHTML, /Connection failures: mb-web/);
});

test('a skill is never called unused — only "no direct evidence" (FR-116, FR-117)', () => {
  const { host } = withToolboxHosts({
    id: 'run-1',
    toolbox_snapshot: { toolbox_name: 'Kit', toolbox_version: 1, skills: [] },
    toolbox_wrap_up: {
      skill_observations: [
        {
          capability_id: 'summarize',
          display_name: 'summarize',
          evidence: 'none',
          note: 'No direct evidence either way — skills shape how the agent works, and that leaves no trace.'
        },
        {
          capability_id: 'citations',
          display_name: 'citations',
          evidence: 'measured',
          note: 'A tool with this name was invoked during the run.'
        }
      ]
    }
  });

  assert.match(host.innerHTML, /summarize &mdash; no direct evidence/);
  assert.match(host.innerHTML, /citations &mdash; measured/);
  assert.doesNotMatch(host.innerHTML, /summarize.*unused/);
});

test('suggestions describe an edit and never make one (FR-119, FR-120)', () => {
  const { host } = withToolboxHosts({
    id: 'run-1',
    toolbox_snapshot: { toolbox_id: 'tbx-1', toolbox_name: 'Research Kit', toolbox_version: 1 },
    toolbox_wrap_up: {
      suggestions: [
        {
          kind: 'remove_unused_operations',
          message: '1 operation(s) were available but never used.',
          evidence: '4 tool calls were made, none of them to these.'
        }
      ]
    }
  });

  assert.match(host.innerHTML, /Ideas from this run/);
  assert.match(host.innerHTML, /never used/);
  assert.match(host.innerHTML, /4 tool calls were made/);
  assert.match(host.innerHTML, /data-run-create-variant="tbx-1"/);
  assert.match(host.innerHTML, /aria-label="Create a variant of Research Kit"/);
  assert.match(host.innerHTML, /Nothing changes until you review and use it/);
});

test('a run that predates snapshots hides the section rather than showing an empty one', () => {
  const { card, host } = withToolboxHosts({ id: 'run-old' });

  assert.equal(card.hidden, true);
  assert.equal(host.innerHTML, '');
});
