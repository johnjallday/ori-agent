import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  activeKnowledgeWorkspaces,
  assistantLearningSources,
  buildUserKnowledgeView
} from './user-knowledge.js';

const workspaces = [
  {
    id: 'group-1',
    name: 'Projects',
    kind: 'group',
    folder_slug: 'projects'
  },
  {
    id: 'ws-b',
    name: 'Writing',
    folder_slug: 'writing',
    assistant_program: {
      link: { station_workspace_id: 'station-1' }
    }
  },
  {
    id: 'ws-a',
    name: 'Research',
    folder_slug: 'research',
    assistant_program: {
      link: { station_workspace_id: 'station-1' }
    }
  },
  {
    id: 'gone',
    name: 'Gone',
    folder_slug: 'gone',
    status: 'trashed'
  }
];

test('activeKnowledgeWorkspaces keeps concrete readable workspaces in deterministic order', () => {
  const active = activeKnowledgeWorkspaces({ folders: workspaces });
  assert.deepEqual(
    active.map(workspace => workspace.id),
    ['ws-a', 'ws-b']
  );
});

test('assistantLearningSources reads explicit station links and deduplicates shared stations', () => {
  const sources = assistantLearningSources(workspaces);
  assert.equal(sources.length, 1);
  assert.equal(sources[0].stationID, 'station-1');
  assert.equal(sources[0].workspaceID, 'ws-a');
  assert.equal(sources[0].route, '/workspaces/research/assistant');
});

test('buildUserKnowledgeView keeps global, workspace, candidate, and reviewed scopes separate', () => {
  const view = buildUserKnowledgeView({
    profile: {
      display_name: 'Jamie',
      timezone: 'UTC',
      specializations: ['Research'],
      preferences: { response_style: 'concise' }
    },
    workspaces,
    memories: [
      {
        workspaceID: 'ws-a',
        payload: {
          entries: [
            { index: 0, type: 'decision', text: 'Use primary sources.' },
            { index: 1, type: 'feedback', text: 'Keep summaries short.' }
          ],
          unstructured: ['Hand-written context'],
          managed_learnings: []
        }
      },
      { workspaceID: 'ws-b', payload: { entries: [], unstructured: [] } }
    ],
    learningDocuments: [
      {
        stationID: 'station-1',
        stationName: 'Research guide',
        route: '/workspaces/research/assistant',
        document: {
          candidates: [
            {
              id: 'candidate-1',
              text: 'The user reviews sources before synthesis.',
              type: 'preference',
              confidence: 'medium',
              evidence: [{}, {}, {}]
            },
            { id: 'candidate-rejected', text: 'Ignore me.', rejected_at: '2026-01-01' }
          ],
          learnings: [
            {
              id: 'learning-1',
              revisions: [
                { text: 'Old wording.', type: 'preference', evidence: [{}, {}, {}] },
                {
                  text: 'Use a short source checklist.',
                  type: 'preference',
                  confidence: 'high',
                  evidence: [{}, {}, {}]
                }
              ]
            }
          ]
        }
      }
    ]
  });

  assert.deepEqual(view.stats, {
    globalDetails: 4,
    workspaceEntries: 2,
    approvedLearnings: 1,
    pendingCandidates: 1
  });
  assert.equal(view.workspaceScopes.length, 1);
  assert.equal(view.workspaceScopes[0].workspaceName, 'Research');
  assert.deepEqual(
    view.workspaceScopes[0].entries.map(entry => entry.text),
    ['Use primary sources.', 'Keep summaries short.']
  );
  assert.equal(view.candidates[0].text, 'The user reviews sources before synthesis.');
  assert.equal(view.approvedLearnings[0].text, 'Use a short source checklist.');
});

test('unstructured MEMORY.md prose is not presented as injected context', () => {
  const view = buildUserKnowledgeView({
    workspaces: [workspaces[2]],
    memories: [
      {
        workspaceID: 'ws-a',
        payload: { entries: [], unstructured: ['# Workspace Memory', 'Hand-written note'] }
      }
    ]
  });

  assert.equal(view.stats.workspaceEntries, 0);
  assert.equal(view.workspaceScopes.length, 0);
});

test('workspace memories never become global or reviewed learnings by aggregation alone', () => {
  const view = buildUserKnowledgeView({
    profile: {},
    workspaces: [workspaces[2]],
    memories: [
      {
        workspaceID: 'ws-a',
        payload: {
          entries: [{ index: 0, type: 'feedback', text: 'Use tables in this workspace.' }]
        }
      }
    ]
  });

  assert.equal(view.stats.globalDetails, 0);
  assert.equal(view.stats.workspaceEntries, 1);
  assert.equal(view.stats.approvedLearnings, 0);
  assert.equal(view.stats.pendingCandidates, 0);
});

test('approved memory projection is a fallback when a station document is unavailable', () => {
  const view = buildUserKnowledgeView({
    workspaces: [workspaces[2]],
    memories: [
      {
        workspaceID: 'ws-a',
        payload: {
          managed_learnings: [
            {
              id: 'learning-1',
              type: 'preference',
              text: 'Use a reviewed checklist.',
              confidence: 'high',
              evidence: [{}, {}, {}]
            }
          ]
        }
      }
    ],
    failures: 1
  });

  assert.equal(view.approvedLearnings.length, 1);
  assert.equal(view.approvedLearnings[0].text, 'Use a reviewed checklist.');
  assert.equal(view.failures, 1);
});
