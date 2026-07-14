import { test } from 'node:test';
import assert from 'node:assert/strict';

globalThis.window = globalThis.window || {};

const { getStatusClass, getDisplayStatus } = await import('./workspace-task.js');

test('getStatusClass keeps its existing six CSS buckets unchanged (no selector breakage)', () => {
  assert.equal(getStatusClass('completed'), 'completed');
  assert.equal(getStatusClass('success'), 'completed');
  assert.equal(getStatusClass('in_progress'), 'in_progress');
  assert.equal(getStatusClass('blocked'), 'blocked');
  assert.equal(getStatusClass('waiting_for_choice'), 'blocked');
  assert.equal(getStatusClass('cancelled'), 'cancelled');
  assert.equal(getStatusClass('skipped'), 'cancelled');
  assert.equal(getStatusClass('failed'), 'failed');
  assert.equal(getStatusClass('error'), 'failed');
  assert.equal(getStatusClass('timeout'), 'failed');
  assert.equal(getStatusClass('pending'), 'pending');
  assert.equal(getStatusClass(''), 'pending');
});

test('getDisplayStatus preserves every existing known label byte-for-byte', () => {
  assert.equal(getDisplayStatus('pending'), 'Pending');
  assert.equal(getDisplayStatus('assigned'), 'Assigned');
  assert.equal(getDisplayStatus('in_progress'), 'In Progress');
  assert.equal(getDisplayStatus('waiting_for_choice'), 'Waiting for Choice');
  assert.equal(getDisplayStatus('completed'), 'Completed');
  assert.equal(getDisplayStatus('success'), 'Completed');
  assert.equal(getDisplayStatus('failed'), 'Failed');
  assert.equal(getDisplayStatus('error'), 'Failed');
  assert.equal(getDisplayStatus('blocked'), 'Blocked');
  assert.equal(getDisplayStatus('cancelled'), 'Cancelled');
  assert.equal(getDisplayStatus('skipped'), 'Skipped');
  assert.equal(getDisplayStatus('timeout'), 'Timed Out');
});

test('getDisplayStatus labels a genuinely unrecognized status "Unknown", not silently "Pending" (FR38, FR110)', () => {
  assert.equal(getDisplayStatus('frobnicating'), 'Unknown');
  assert.notEqual(getDisplayStatus('frobnicating'), 'Pending');
});

test('getDisplayStatus still treats an empty/missing status as Pending (unchanged default)', () => {
  assert.equal(getDisplayStatus(''), 'Pending');
  assert.equal(getDisplayStatus(undefined), 'Pending');
});
