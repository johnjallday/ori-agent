import { test } from 'node:test';
import assert from 'node:assert/strict';

import { formatTaskOutputStatus } from './workspace-run.js';

test('formatTaskOutputStatus maps task output validation states to PRD labels', () => {
  assert.equal(formatTaskOutputStatus({ validation_status: 'passed', storage_status: 'appended' }), 'Saved');
  assert.equal(formatTaskOutputStatus({ validation_status: 'needs_review', storage_status: 'skipped_invalid' }), 'Needs Review');
  assert.equal(formatTaskOutputStatus({ validation_status: 'dismissed' }), 'Dismissed');
  assert.equal(formatTaskOutputStatus({ validation_status: 'manually_approved', storage_status: 'manually_appended' }), 'Manually Approved');
});
