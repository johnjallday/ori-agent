import { test } from 'node:test';
import assert from 'node:assert/strict';

import { formatTaskOutputStatus, WorkspaceRunPage } from './workspace-run.js';

test('formatTaskOutputStatus maps task output validation states to PRD labels', () => {
  assert.equal(formatTaskOutputStatus({ validation_status: 'passed', storage_status: 'appended' }), 'Saved');
  assert.equal(formatTaskOutputStatus({ validation_status: 'needs_review', storage_status: 'skipped_invalid' }), 'Needs Review');
  assert.equal(formatTaskOutputStatus({ validation_status: 'dismissed' }), 'Dismissed');
  assert.equal(formatTaskOutputStatus({ validation_status: 'manually_approved', storage_status: 'manually_appended' }), 'Manually Approved');
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
