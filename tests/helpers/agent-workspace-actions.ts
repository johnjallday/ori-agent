import type { Page } from '@playwright/test';

// Synthetic read projections only. Persisted assignment journeys live in
// agent-workspaces.spec.ts and use real membership endpoints.
export const actionAgents = [
  { name: 'Action Library', workspace_count: 0, workspaces: [], statistics: { message_count: 0 } },
  {
    name: 'Action Used',
    workspace_count: 1,
    workspaces: [{ id: 'one', name: 'One', folder_slug: 'action-one' }],
    statistics: { message_count: 90 },
    evolution: { level: 4, experience: 500 }
  },
  {
    name: 'Action Shared',
    workspace_count: 2,
    workspaces: [
      { id: 'one', name: 'One', folder_slug: 'action-one' },
      { id: 'two', name: 'Two', folder_slug: 'action-two' }
    ]
  },
  {
    name: 'Action Partial',
    workspace_count: 3,
    workspaces: [
      { id: 'one', name: 'One', folder_slug: 'action-one' },
      { id: 'two', name: 'No slug' }
    ]
  },
  { name: 'Action Unknown' },
  { name: 'Action Incomplete', workspace_count: 0, workspaces: [{}] },
  { name: 'Action Invalid Count', workspace_count: -1, workspaces: [] },
  {
    name: 'Action Missing Slug',
    workspace_count: 1,
    workspaces: [{ id: 'uuid-not-a-slug', name: 'Missing slug' }]
  },
  {
    name: 'Action Owned',
    workspace_count: 0,
    workspaces: [],
    origin: { source: 'workspace', workspace_name: 'Hidden owner' }
  },
  { name: 'Action Unreadable', state: 'unreadable', workspace_count: 0 },
  { name: 'Action CLI', source: 'cli', role: 'cli_agent', workspace_count: 0 },
  { name: 'Ask Ori', workspace_count: 0 }
].map(agent => ({ model: 'gpt-4o-mini', status: 'active', role: 'general', ...agent }));

export async function mockWorkspaceActionRoster(page: Page) {
  await page.route('**/api/agents/dashboard/list?**', route =>
    route.fulfill({ json: { agents: actionAgents } })
  );
  await page.route(/\/api\/agents\/[^/]+\/detail$/, route => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/')[3]);
    const agent = actionAgents.find(candidate => candidate.name === name);
    return route.fulfill({ json: { ...agent, version: 'fixture' } });
  });
}
