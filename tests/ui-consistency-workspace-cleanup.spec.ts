import { expect, test, type APIRequestContext } from '@playwright/test';
import { lstatSync, readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  seedCanvasSavedWorkflow,
  freezeWorkspaceSeed,
  retireWorkspaceSeed
} from './helpers/ui-consistency-workspace';

for (const kind of ['extra file', 'changed owned file'] as const)
  test(`saved-workflow cleanup refuses ${kind} before any DELETE`, async ({ request }) => {
    const seed = await createWorkspaceSeed(request);
    const workflow = await seedCanvasSavedWorkflow(request, seed);
    await freezeWorkspaceSeed(request, seed);
    const file = join(
      seed.savedWorkflows!.root,
      kind === 'extra file' ? 'owned-negative.txt' : `${workflow.id}.json`
    );
    const original = kind === 'extra file' ? undefined : readFileSync(file, 'utf8');
    const changed =
      original === undefined ? `Exact negative fixture ${seed.workspace.id}` : `${original}\n`;
    writeFileSync(file, changed, { flag: original === undefined ? 'wx' : 'w', mode: 0o600 });
    try {
      const deletes: string[] = [];
      const observed = {
        get: request.get.bind(request),
        delete: async (path: string) => {
          deletes.push(path);
          throw new Error('DELETE before complete saved-workflow inventory validation');
        }
      } as unknown as APIRequestContext;
      await expect(retireWorkspaceSeed(observed, seed)).rejects.toThrow(
        /unexpected saved workflow files/
      );
      expect(deletes).toEqual([]);
      expect((await request.get(`/api/workspaces/${seed.workspace.id}`)).ok()).toBe(true);
      expect(await (await request.get(`/api/workflows/${workflow.id}`)).json()).toEqual(workflow);
    } finally {
      expect(lstatSync(file).isSymbolicLink()).toBe(false);
      expect(readFileSync(file, 'utf8')).toBe(changed);
      if (original === undefined) unlinkSync(file);
      else writeFileSync(file, original, { mode: 0o600 });
      await retireWorkspaceSeed(request, seed);
    }
  });

for (const location of ['library', 'workspace-local'] as const)
  test(`group cleanup refuses an unjournaled ${location} agent file before any DELETE`, async ({
    request
  }) => {
    const seed = await createWorkspaceSeed(request, 'group');
    await populateWorkspaceSeed(request, seed);
    await freezeWorkspaceSeed(request, seed);
    expect(seed.libraryAgents).toHaveLength(1);
    const agent = seed.libraryAgents![0];
    const extra = join(
      location === 'library' ? agent.folder : join(seed.sidecar, 'agents'),
      'owned-cleanup-negative.txt'
    );
    const content = `Exact negative fixture ${seed.workspace.id}`;
    writeFileSync(extra, content, { flag: 'wx', mode: 0o600 });
    try {
      const deletes: string[] = [];
      const observed = {
        get: request.get.bind(request),
        delete: async (path: string) => {
          deletes.push(path);
          throw new Error('DELETE before complete library-agent inventory validation');
        }
      } as unknown as APIRequestContext;
      await expect(retireWorkspaceSeed(observed, seed)).rejects.toThrow(
        /unexpected (agent inventory\/content|workspace-local agent configuration)/
      );
      expect(deletes).toEqual([]);
      for (const record of seed.records) {
        const response = await request.get(record.path);
        expect(response.ok()).toBe(true);
        expect(await response.json()).toEqual(record.value);
      }
      expect(
        (await request.get(`/api/agents/${encodeURIComponent(agent.name)}/detail`)).status()
      ).toBe(200);
    } finally {
      // Only this exact test-created file is removed, after readback; the journal
      // is never expanded to adopt the unexpected contents.
      expect(lstatSync(extra).isSymbolicLink()).toBe(false);
      expect(readFileSync(extra, 'utf8')).toBe(content);
      unlinkSync(extra);
      await retireWorkspaceSeed(request, seed);
      expect(
        (await request.get(`/api/agents/${encodeURIComponent(agent.name)}/detail`)).status()
      ).toBe(404);
    }
  });

test('group cleanup rejects an unexpected origin-copy workspace before any DELETE', async ({
  request
}) => {
  const seed = await createWorkspaceSeed(request, 'group');
  try {
    await freezeWorkspaceSeed(request, seed);
    const agent = seed.libraryAgents![0];
    const detailPath = `/api/agents/${encodeURIComponent(agent.name)}/detail`;
    const deletes: string[] = [];
    const observed = {
      get: async (path: string) => {
        const response = await request.get(path);
        if (path !== detailPath) return response;
        const value = await response.json();
        return {
          ok: () => true,
          json: async () => ({
            ...value,
            origin: {
              ...value.origin,
              customised_in: [{ id: 'unowned-workspace', name: 'Never adopt this reference' }]
            }
          })
        };
      },
      delete: async (path: string) => {
        deletes.push(path);
        throw new Error('DELETE before origin-copy validation');
      }
    } as unknown as APIRequestContext;
    await expect(retireWorkspaceSeed(observed, seed)).rejects.toThrow(/unowned-workspace/);
    expect(deletes).toEqual([]);
    expect((await request.get(`/api/workspaces/${seed.workspace.id}`)).ok()).toBe(true);
    expect((await request.get(detailPath)).ok()).toBe(true);
  } finally {
    await retireWorkspaceSeed(request, seed);
  }
});

for (const kind of ['task', 'note', 'plan'] as const) {
  test(`cleanup refuses an unjournaled ${kind} before any DELETE`, async ({ request }) => {
    const seed = await createWorkspaceSeed(request);
    let extra: { path: string; value: Record<string, unknown> } | undefined;
    try {
      await populateWorkspaceSeed(request, seed);
      await freezeWorkspaceSeed(request, seed);
      const base = `/api/workspaces/${seed.workspace.id}`;
      let path: string;
      if (kind === 'task') {
        const response = await request.post('/api/orchestration/tasks', {
          data: {
            workspace_id: seed.workspace.id,
            description: `${seed.workspace.name} interrupted seed`,
            schedule_enabled: false
          }
        });
        expect(response.status()).toBe(201);
        path = `/api/orchestration/tasks?id=${(await response.json()).task.id}`;
      } else if (kind === 'note') {
        const response = await request.post(`${base}/notes`, {
          data: {
            name: `${seed.workspace.name} interrupted note`,
            content: 'Owned negative cleanup fixture.'
          }
        });
        expect(response.ok()).toBe(true);
        const body = await response.json();
        path = `/api/notes/${body.note?.id ?? body.id}`;
      } else {
        const response = await request.post(`${base}/plans`, {
          data: {
            title: `${seed.workspace.name} interrupted plan`,
            request: 'Owned negative cleanup fixture. Never execute.'
          }
        });
        expect(response.status()).toBe(201);
        path = `${base}/plans/${(await response.json()).id}`;
      }
      const created = await request.get(path);
      expect(created.ok()).toBe(true);
      extra = { path, value: await created.json() };
      const deletes: string[] = [];
      const observed = {
        get: request.get.bind(request),
        delete: async (path: string) => {
          deletes.push(path);
          throw new Error('DELETE attempted before complete inventory validation');
        }
      } as unknown as APIRequestContext;
      await expect(retireWorkspaceSeed(observed, seed)).rejects.toThrow(
        /unexpected workspace (tasks|notes|plans)/
      );
      expect(deletes).toEqual([]);
      // All tracked definitions and the unexpected record must remain intact.
      for (const record of [...seed.records, extra]) {
        const response = await request.get(record.path);
        expect(response.ok()).toBe(true);
        expect(await response.json()).toEqual(record.value);
      }
    } finally {
      if (extra) {
        // Retire only this explicitly created extra, after exact readback proof;
        // this is not baseline adoption or an automatic cleanup bypass.
        const current = await request.get(extra.path);
        expect(current.ok()).toBe(true);
        expect(await current.json()).toEqual(extra.value);
        const path =
          kind === 'task' ? `${extra.path}&workspace_id=${seed.workspace.id}` : extra.path;
        const removed = await request.delete(path);
        expect(removed.ok()).toBe(true);
        expect((await request.get(extra.path)).status()).toBe(404);
      }
      await retireWorkspaceSeed(request, seed);
    }
  });
}
