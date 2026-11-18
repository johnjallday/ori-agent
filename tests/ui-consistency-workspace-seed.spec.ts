import { expect, test } from '@playwright/test';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';

// Credential-free API contract spike before any workspace styling changes.
// The later visual suites reuse these owned records rather than nonexistent IDs.
let seeds: WorkspaceSeed[] = [];
test.afterEach(async ({ request }) => {
  test.setTimeout(120_000);
  for (const seed of [...seeds].reverse()) await retireWorkspaceSeed(request, seed);
  seeds = [];
});

test('workspace seeds preserve real local definitions without starting work', async ({
  request
}) => {
  test.setTimeout(120_000);
  for (const kind of ['workspace', 'group'] as const) {
    const seed = await createWorkspaceSeed(request, kind);
    seeds.push(seed);
    const data = await populateWorkspaceSeed(request, seed);
    await freezeWorkspaceSeed(request, seed);
    expect(data.ticket.state).toBe('backlog');
    expect(data.execution.status).toBe('pending');
    expect(data.plans.draft.status).toBe('draft');
    expect(data.plans.review.status).toBe('in_review');
    expect(data.plans.review.current_version).toBe(1);
    expect(data.plans.history.archived).toBe(true);
    for (const plan of Object.values(data.plans)) {
      expect(plan.task_links || []).toEqual([]);
      expect(plan.run_links || []).toEqual([]);
      expect(plan.draft.execution.mode).toBe('step_through');
    }
    const runs = await request.get(`/api/workspaces/${seed.workspace.id}/runs`);
    expect(runs.ok()).toBe(true);
    expect((await runs.json()).runs || []).toEqual([]);
  }
});
