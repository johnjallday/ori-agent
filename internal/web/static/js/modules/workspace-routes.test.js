import assert from 'node:assert/strict';
import test from 'node:test';

import { workspacePageURL, workspaceRootURL } from './workspace-routes.js';

test('workspaceRootURL uses and escapes the explicit slug', () => {
  assert.equal(workspaceRootURL('marketing site'), '/workspaces/marketing%20site');
});

test('workspacePageURL escapes each descendant segment independently', () => {
  assert.equal(
    workspacePageURL('marketing-site', ['task', 'task / 1']),
    '/workspaces/marketing-site/task/task%20%2F%201'
  );
});

test('workspacePageURL preserves explicit query and fragment values', () => {
  assert.equal(
    workspacePageURL('marketing-site', [], { search: '?panel=settings', hash: 'connections' }),
    '/workspaces/marketing-site?panel=settings#connections'
  );
  assert.equal(
    workspaceRootURL('marketing-site', {
      search: new URLSearchParams({ panel: 'calendar', view: 'week' })
    }),
    '/workspaces/marketing-site?panel=calendar&view=week'
  );
});

test('workspacePageURL refuses missing slugs and route segments', () => {
  assert.throws(() => workspaceRootURL(''), /workspaceSlug is required/);
  assert.throws(() => workspacePageURL('marketing-site', ['task', '']), /cannot be empty/);
});
