// Tests for quick-task.js — the kept create request body.
//
//   node --test internal/web/static/js/modules/quick-task.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { taskCreateBody } from './quick-task.js';

test('a new task with no assignee leaves the choice to the server', () => {
  const body = taskCreateBody('ws-1', '  Compare the launch notes  ');
  assert.equal(body.workspace_id, 'ws-1');
  assert.equal(body.description, 'Compare the launch notes');
  assert.equal(body.status, 'pending');
  assert.equal(body.to, undefined, 'no assignee: the entry-agent default applies');
  assert.equal(body.required_capabilities, undefined);
});
