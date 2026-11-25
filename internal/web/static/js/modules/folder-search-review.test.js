import test from 'node:test';
import assert from 'node:assert/strict';
import { folderWorkspaceRequest } from './folder-search-review.js';

test('compact setup sends only the selected handle, workspace name and explicit search choice', () => {
  const review = {
    id: 'choice',
    label: 'Downloads',
    path: '/not-authority',
    source: 'prior-grant'
  };
  for (const consent of [true, false]) {
    assert.deepEqual(
      folderWorkspaceRequest(review, '  Reading inbox ', consent, 'blank-revision'),
      {
        name: 'Reading inbox',
        folder_selection: 'choice',
        allow_filename_search: consent,
        blank: true,
        create_template_agents: false,
        existing_agent_names: [],
        team_intent: { version: 1, mode: 'agentless', plan_revision: 'blank-revision' },
        role_staffing: []
      }
    );
  }
});
