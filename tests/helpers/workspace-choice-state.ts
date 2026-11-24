import { expect, type APIRequestContext } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

// Only called after the isolated runner's sandbox checks. Read the hire ledger
// itself: the conversation's proposal is not proof of a hire or a first look.
export async function choiceInventory(request: APIRequestContext, dataDir: string) {
  const get = async (path: string) => {
    const response = await request.get(path, { headers: { 'X-Ori-Search': '1' } });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const rows = execFileSync(
    'sqlite3',
    [
      '-readonly',
      '-json',
      join(dataDir, 'sessions.db'),
      'SELECT id, workspace_id, agent_name, state, run_id, task_id FROM agent_hires ORDER BY id'
    ],
    { encoding: 'utf8' }
  );
  return {
    workspaces: (await get('/api/workspaces')).folders.map((w: { id: string }) => w.id).sort(),
    sources: (await get('/api/file-search/sources')).map((s: { id: string }) => s.id).sort(),
    hires: JSON.parse(rows.trim() || '[]')
  };
}

export async function arrivalState(request: APIRequestContext, id: string) {
  const base = '/api/workspaces/' + encodeURIComponent(id);
  const read = async (suffix: string) => {
    const response = await request.get(base + suffix);
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const [detail, { roles }, mission] = await Promise.all([
    read(''),
    read('/roles'),
    read('/mission')
  ]);
  const workspace = detail.workspace || detail;
  return {
    id,
    agents: workspace.agents || [],
    roles: roles.total_count,
    filled: roles.filled_count,
    mission: mission.mission,
    pending: mission.first_run_pending,
    goalRuns: mission.mission_execution_count,
    inFlight: mission.run_in_flight
  };
}
