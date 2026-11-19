import { expect, type APIRequestContext } from '@playwright/test';
import { randomUUID, createHash } from 'node:crypto';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  unlinkSync,
  writeFileSync
} from 'node:fs';
import { join, relative } from 'node:path';
import { assertSandbox, createWorkspace, removeOwnedWorkspace } from './ui-consistency';

type Workspace = {
  id: string;
  name: string;
  folder_slug: string;
  kind?: 'workspace' | 'group';
  parent?: { id: string; name: string; folder_slug: string };
};
type RecordSnapshot = {
  path: string;
  value: Record<string, any> & { id: string };
  kind: 'note' | 'ticket' | 'execution' | 'plan';
};
export type WorkspaceSeed = {
  workspace: Workspace;
  sandbox: string;
  sidecar: string;
  manifest: string;
  protectedHash: string;
  protectedValue?: Record<string, unknown>;
  records: RecordSnapshot[];
  libraryAgents?: {
    name: string;
    root: string;
    folder: string;
    expected: Record<string, any>;
    inventory: Record<string, string>;
  }[];
  localAgentInventory?: Record<string, string>;
  localAgentPrompts?: { name: string; value: Record<string, any> }[];
  generatedFiles?: { attachmentId: string; path: string; hash: string }[];
  savedWorkflows?: {
    root: string;
    baselineRecords: Record<string, any>[];
    baselineInventory: Record<string, string>;
    records: { value: Record<string, any>; hash: string }[];
  };
};

// Deliberately exclude only store-maintained version/time and the records this
// helper tracks separately. Everything else in the owned definition must stay
// unchanged during a read-only tour, including agents, bindings and schedules.
function protectedValue(sidecar: string) {
  const definition = JSON.parse(readFileSync(join(sidecar, 'workspace.json'), 'utf8'));
  for (const key of [
    'version',
    'updated_at',
    'tasks',
    'ticket_sequence',
    'ticket_migration_version'
  ])
    delete definition[key];
  return definition;
}

function contentHash(value: unknown) {
  // Go's two serializers may reorder object members without changing content.
  // Keep array order and every value, but make object-key order irrelevant.
  return createHash('sha256')
    .update(
      JSON.stringify(value, (_key, item) =>
        item && typeof item === 'object' && !Array.isArray(item)
          ? Object.fromEntries(
              Object.keys(item)
                .sort()
                .map(key => [key, item[key]])
            )
          : item
      )
    )
    .digest('hex');
}

function agentProjection(agent: Record<string, any>) {
  const projected = Object.fromEntries(
    [
      'name',
      'role',
      'model',
      'provider',
      'temperature',
      'max_output_tokens',
      'system_prompt',
      'metadata',
      'appearance',
      'version',
      'origin'
    ]
      .filter(key => key in agent)
      .map(key => [key, agent[key]])
  );
  // CompositeStore computes this list from its cached workspace-copy view,
  // not the definition. Validate its exact owned ids separately below.
  if (projected.origin) {
    projected.origin = { ...projected.origin };
    delete projected.origin.customised_in;
  }
  return projected;
}

function folderInventory(folder: string): Record<string, string> {
  expect(lstatSync(folder).isSymbolicLink()).toBe(false);
  expect(realpathSync(folder)).toBe(folder);
  const entries: Record<string, string> = {};
  const walk = (directory: string) => {
    for (const name of readdirSync(directory).sort()) {
      const path = join(directory, name),
        stat = lstatSync(path);
      expect(stat.isSymbolicLink(), 'never retire an agent folder containing a symlink').toBe(
        false
      );
      const key = relative(folder, path);
      if (stat.isDirectory()) {
        entries[key] = 'directory';
        walk(path);
      } else {
        expect(stat.isFile()).toBe(true);
        entries[key] = createHash('sha256').update(readFileSync(path)).digest('hex');
      }
    }
  };
  walk(folder);
  return entries;
}

function agentInventory(folder: string) {
  const entries = folderInventory(folder);
  expect(entries['agent_settings.json']).toMatch(/^[0-9a-f]{64}$/);
  return entries;
}

async function verifySeedAgents(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  attached: boolean
) {
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  for (const agent of seed.libraryAgents ?? []) {
    expect(realpathSync(settings.effective_workspace_root)).toBe(agent.root);
    expect(relative(seed.sandbox, agent.root).startsWith('..')).toBe(false);
    expect(agent.name).toBe(`${seed.workspace.name} Manager`);
    expect(agent.folder).toBe(join(agent.root, 'Agents', agent.name));
    expect(lstatSync(join(agent.root, 'Agents')).isSymbolicLink()).toBe(false);
    expect(
      agentInventory(agent.folder),
      'unexpected agent inventory/content; preserve entire fixture'
    ).toEqual(agent.inventory);
    const response = await request.get(`/api/agents/${encodeURIComponent(agent.name)}/detail`);
    expect(response.ok()).toBe(true);
    const detail = await response.json();
    expect(agentProjection(detail)).toEqual(agent.expected);
    const copies = detail.origin?.customised_in ?? [];
    expect(copies.length).toBeLessThanOrEqual(1);
    for (const copy of copies) {
      expect(copy.id).toBe(seed.workspace.id);
      expect(copy.name).toBe(seed.workspace.name);
    }
    expect(detail.workspace_count).toBe(attached ? 1 : 0);
    expect((detail.workspaces ?? []).map(workspace => workspace.id)).toEqual(
      attached ? [seed.workspace.id] : []
    );
  }
}

function protectedHash(sidecar: string) {
  return contentHash(protectedValue(sidecar));
}

function journal(seed: WorkspaceSeed) {
  // Called only while seeding through the fixed, reviewed API writes below.
  // Saves can normalize timestamps/shape in the sidecar. The final baseline
  // becomes strict before a read tour; cleanup never adopts changed content.
  seed.protectedValue = protectedValue(seed.sidecar);
  seed.protectedHash = contentHash(seed.protectedValue);
  writeFileSync(seed.manifest, JSON.stringify(seed, null, 2), { mode: 0o600 });
}

export async function createWorkspaceSeed(
  request: APIRequestContext,
  kind: 'workspace' | 'group' = 'workspace',
  parent?: WorkspaceSeed
): Promise<WorkspaceSeed> {
  const sandbox = await assertSandbox(request);
  const initialSettings = await (await request.get('/api/settings/workspace-root')).json();
  const initialRoot = realpathSync(initialSettings.effective_workspace_root);
  let createdLibraryName: string | undefined;
  let workspace: Workspace;
  if (parent) {
    expect(kind).toBe('workspace');
    expect(parent.sandbox).toBe(sandbox);
    expect(parent.workspace.kind).toBe('group');
    const created = await request.post('/api/workspaces', {
      data: {
        name: `UI consistency ${randomUUID()}`,
        blank: true,
        create_template_agents: true,
        parent_id: parent.workspace.id
      }
    });
    expect(created.ok(), await created.text()).toBe(true);
    const body = await created.json();
    workspace = {
      ...(body.folder ?? body.workspace ?? body),
      parent: {
        id: parent.workspace.id,
        name: parent.workspace.name,
        folder_slug: parent.workspace.folder_slug
      }
    };
  } else if (kind === 'group') {
    const name = `UI consistency ${randomUUID()}`;
    // This deterministic roster preview builds metadata only; no planner,
    // assistant hire, setup task, provider or group schedule is invoked.
    const preview = await request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: name }
    });
    expect(preview.ok(), await preview.text()).toBe(true);
    const plan = await preview.json();
    expect(plan.agents).toHaveLength(1);
    const manager = plan.agents[0];
    expect(manager.name).toBe(`${name} Manager`);
    expect(manager.action).toBe('create');
    expect(
      (await request.get(`/api/agents/${encodeURIComponent(manager.name)}/detail`)).status()
    ).toBe(404);
    expect(existsSync(join(initialRoot, 'Agents', manager.name))).toBe(false);
    createdLibraryName = manager.name;
    const created = await request.post('/api/workspaces', {
      data: {
        name,
        kind,
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: plan.revision,
          expectations: [{ index: 0, name: manager.name, action: manager.action }]
        }
      }
    });
    expect(created.ok(), await created.text()).toBe(true);
    const body = await created.json();
    workspace = { ...(body.folder ?? body.workspace ?? body), kind };
  } else workspace = await createWorkspace(request);
  const response = await request.get(`/api/workspaces/${workspace.id}`);
  expect(response.ok()).toBe(true);
  const current = await response.json();
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  const root = realpathSync(settings.effective_workspace_root);
  expect(root).toBe(initialRoot);
  expect(lstatSync(current.sidecar_path).isSymbolicLink()).toBe(false);
  const sidecar = realpathSync(current.sidecar_path);
  const expectedPath = parent
    ? join(parent.workspace.folder_slug, 'sub-workspaces', workspace.folder_slug)
    : workspace.folder_slug;
  expect(relative(root, sidecar)).toBe(expectedPath);
  if (parent) expect(current.parent_id).toBe(parent.workspace.id);
  expect(current.tasks || []).toEqual([]);
  const directory = join(sandbox, 'ui-consistency-owned-workspaces');
  mkdirSync(directory, { recursive: true, mode: 0o750 });
  const seed: WorkspaceSeed = {
    workspace,
    sandbox,
    sidecar,
    manifest: join(directory, `${workspace.id}.json`),
    protectedHash: protectedHash(sidecar),
    records: []
  };
  journal(seed);
  if (createdLibraryName) {
    const response = await request.get(
      `/api/agents/${encodeURIComponent(createdLibraryName)}/detail`
    );
    expect(response.ok()).toBe(true);
    const detail = await response.json();
    seed.libraryAgents = [
      {
        name: createdLibraryName,
        root,
        folder: join(root, 'Agents', createdLibraryName),
        expected: agentProjection(detail),
        inventory: agentInventory(join(root, 'Agents', createdLibraryName))
      }
    ];
    journal(seed);
    await verifySeedAgents(request, seed, true);
  }
  return seed;
}

async function remember(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  path: string,
  kind: RecordSnapshot['kind']
) {
  const response = await request.get(path);
  expect(response.ok(), await response.text()).toBe(true);
  const value = await response.json();
  expect(value.id).toBeTruthy();
  seed.records.push({ path, value, kind });
  journal(seed);
  return value;
}

/** Local definitions only. Never call generate-draft, materialize, execute,
 * ticket start/transition, output repair, or the Review-center trigger. */
// Real metadata-only parent/child definitions for Canvas filtering. Creating
// a child never dispatches the pending parent or enables its schedule.
export async function seedCanvasWorkflow(request: APIRequestContext, seed: WorkspaceSeed) {
  await assertSandbox(request);
  const parentRecord = seed.records.find(record => record.kind === 'execution')!;
  const parent = parentRecord.value;
  const currentParent = await request.get(parentRecord.path);
  expect(currentParent.ok()).toBe(true);
  expect(await currentParent.json()).toEqual(parent);
  expect(parent.workspace_id).toBe(seed.workspace.id);
  expect(parent.status).toBe('pending');
  const response = await request.post('/api/orchestration/tasks', {
    data: {
      workspace_id: seed.workspace.id,
      to: 'unassigned',
      assignment_mode: 'manual',
      parent_task_id: parent.id,
      description: `${seed.workspace.name} child review point`,
      details: 'Pending metadata only; do not execute this workflow.',
      schedule_enabled: false
    }
  });
  expect(response.ok(), await response.text()).toBe(true);
  const created = (await response.json()).task;
  // As in populateWorkspaceSeed, settle the supported legacy-ticket read
  // migration before freezing the exact execution definition.
  const tickets = await request.get(`/api/workspaces/${seed.workspace.id}/tickets`);
  expect(tickets.ok(), await tickets.text()).toBe(true);
  const saved = await remember(
    request,
    seed,
    `/api/orchestration/tasks?id=${encodeURIComponent(created.id)}`,
    'execution'
  );
  expect(saved.parent_task_id).toBe(parent.id);
  expect(saved.status).toBe('pending');
  expect(saved.schedule_enabled ?? false).toBe(false);
  return { parentId: parent.id, childId: created.id };
}

export async function seedCanvasSavedWorkflow(request: APIRequestContext, seed: WorkspaceSeed) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  expect(seed.savedWorkflows).toBeUndefined();
  expect(JSON.parse(readFileSync(seed.manifest, 'utf8'))).toEqual(seed);
  expect(protectedValue(seed.sidecar)).toEqual(seed.protectedValue);
  // start-main-server.py pins this exact directory; never discover/adopt a
  // library by a historical filename prefix or delete the whole directory.
  const root = join(seed.sandbox, 'workflow_templates', 'custom');
  const before = await request.get('/api/workflows');
  expect(before.ok()).toBe(true);
  const baselineRecords = (await before.json()).workflows;
  expect(Array.isArray(baselineRecords)).toBe(true);
  seed.savedWorkflows = {
    root,
    baselineRecords,
    baselineInventory: folderInventory(root),
    records: []
  };
  journal(seed);
  const response = await request.post('/api/workflows', {
    data: {
      name: `${seed.workspace.name} saved local review`,
      description:
        'Fictional local workflow definition. Preserve this outline without instantiating any nodes. '.repeat(
          4
        ),
      category: 'Local review',
      nodes: [
        {
          id: 'review-1',
          type: 'task',
          config: { description: 'Read the fictional local brief', to: 'unassigned' },
          relative_x: 0,
          relative_y: 0
        }
      ]
    }
  });
  expect(response.status(), await response.text()).toBe(201);
  const { id } = await response.json();
  expect(id).toMatch(/^[0-9a-f-]{36}$/);
  const detail = await request.get(`/api/workflows/${id}`);
  expect(detail.ok()).toBe(true);
  const value = await detail.json();
  const path = join(root, `${id}.json`);
  expect(lstatSync(path).isSymbolicLink()).toBe(false);
  expect(JSON.parse(readFileSync(path, 'utf8'))).toEqual(value);
  seed.savedWorkflows.records.push({
    value,
    hash: createHash('sha256').update(readFileSync(path)).digest('hex')
  });
  journal(seed);
  expect(value.source).toBe('custom');
  expect(value.nodes).toHaveLength(1);
  await verifySavedWorkflows(request, seed);
  return value;
}

async function verifySavedWorkflows(request: APIRequestContext, seed: WorkspaceSeed) {
  const library = seed.savedWorkflows;
  if (!library) return;
  expect(library.root).toBe(join(seed.sandbox, 'workflow_templates', 'custom'));
  const expected = { ...library.baselineInventory };
  for (const record of library.records) {
    expect(record.value.name.startsWith(`${seed.workspace.name} `)).toBe(true);
    expect(record.value.source).toBe('custom');
    expect(library.baselineRecords.some(item => item.id === record.value.id)).toBe(false);
    expected[`${record.value.id}.json`] = record.hash;
    const detail = await request.get(`/api/workflows/${record.value.id}`);
    expect(detail.ok()).toBe(true);
    expect(await detail.json()).toEqual(record.value);
  }
  expect(folderInventory(library.root), 'preserve unexpected saved workflow files').toEqual(
    expected
  );
  const response = await request.get('/api/workflows');
  expect(response.ok()).toBe(true);
  const sort = (items: Record<string, any>[]) =>
    [...items].sort((a, b) => a.id.localeCompare(b.id));
  expect(
    sort((await response.json()).workflows),
    'preserve unexpected saved workflow records'
  ).toEqual(sort([...library.baselineRecords, ...library.records.map(record => record.value)]));
}

export async function populateWorkspaceSeed(request: APIRequestContext, seed: WorkspaceSeed) {
  await assertSandbox(request);
  const base = `/api/workspaces/${seed.workspace.id}`;
  const marker = seed.workspace.name;
  const note = await request.post(`${base}/notes`, {
    data: {
      name: `${marker} field notes`,
      content:
        '# Studio observations\n\nFictional local notes.\n\n- Keep the chosen artwork.\n- Save deliberately; never run an agent for photography.'
    }
  });
  expect(note.ok(), await note.text()).toBe(true);
  const noteBody = await note.json();
  const noteId = noteBody.note?.id ?? noteBody.id;
  await remember(request, seed, `/api/notes/${noteId}`, 'note');
  const ticket = await request.post(`${base}/tickets`, {
    data: {
      state: 'backlog',
      title: `${marker} review the long workshop brief and preserve the notes`,
      description: 'Manual fixture. No assignee, schedule, run or automatic plan.',
      linked_note_ids: [noteId]
    }
  });
  expect(ticket.status(), await ticket.text()).toBe(201);
  const ticketBody = await ticket.json();
  await remember(request, seed, `${base}/tickets/${ticketBody.id}`, 'ticket');
  const execution = await request.post('/api/orchestration/tasks', {
    data: {
      workspace_id: seed.workspace.id,
      to: 'unassigned',
      assignment_mode: 'manual',
      description: `${marker} saved execution definition`,
      details: 'Unstarted definition. Any running/result presentation is a labelled GET fixture.',
      schedule_enabled: false
    }
  });
  expect(execution.ok(), await execution.text()).toBe(true);
  const executionBody = (await execution.json()).task;
  expect(executionBody.id).toBeTruthy();
  // Settle the backend's supported legacy-ticket read migration while seeding,
  // before freezing read-tour snapshots. This never starts the definition.
  const tickets = await request.get(`${base}/tickets`);
  expect(tickets.ok(), await tickets.text()).toBe(true);
  const savedExecution = await remember(
    request,
    seed,
    `/api/orchestration/tasks?id=${encodeURIComponent(executionBody.id)}`,
    'execution'
  );
  expect(savedExecution.status).toBe('pending');
  // The canonical create endpoint defaults even explicit 'unassigned' to the
  // entry agent. Preserve that contract, but never dispatch this definition.
  expect(savedExecution.to).toBe(executionBody.to);
  expect(savedExecution.schedule_enabled ?? false).toBe(false);
  expect(savedExecution.context?.template_setup).not.toBe(true);

  const plans: Record<string, any> = {};
  for (const state of ['draft', 'review', 'history']) {
    const created = await request.post(`${base}/plans`, {
      data: {
        title: `${marker} ${state} plan`,
        request: `Inspect the fictional ${state} studio brief without executing it.`,
        objective: 'Keep an explicit, reviewable local outline.'
      }
    });
    expect(created.status(), await created.text()).toBe(201);
    const plan = await created.json();
    // Journal the ID before subsequent assertions or writes can fail.
    await remember(request, seed, `${base}/plans/${plan.id}`, 'plan');
    const edited = await request.patch(`${base}/plans/${plan.id}/draft`, {
      data: {
        revision: plan.draft_revision,
        objective: plan.objective,
        content: {
          rationale: 'Fictional layout fixture authored locally; no model generation.',
          groups: [
            {
              id: 'g1',
              title: 'Inspect the brief',
              items: [
                {
                  id: 'i1',
                  description: 'Read the local notes and compare the proposed wording.',
                  expected_result: 'A reviewed summary with no runtime side effects.',
                  author: 'user'
                }
              ]
            }
          ],
          execution: { mode: 'step_through' }
        }
      }
    });
    expect(edited.ok(), await edited.text()).toBe(true);
    const snapshot = seed.records.find(record => record.path === `${base}/plans/${plan.id}`)!;
    snapshot.value = await (await request.get(snapshot.path)).json();
    journal(seed);
    if (state === 'review') {
      const version = await request.post(`${base}/plans/${plan.id}/versions`, { data: {} });
      expect(version.status(), await version.text()).toBe(201);
    }
    if (state === 'history') {
      const archived = await request.post(`${base}/plans/${plan.id}/archive`, {
        data: { reason: 'Owned layout fixture; keep history presentation.', actor: 'UI fixture' }
      });
      expect(archived.ok(), await archived.text()).toBe(true);
    }
    snapshot.value = await (await request.get(snapshot.path)).json();
    journal(seed);
    plans[state] = snapshot.value;
  }
  // Creating a manual ticket legitimately advances the sequence/migration
  // bookkeeping; it is excluded above, not permission to change other fields.
  expect(protectedHash(seed.sidecar)).toBe(seed.protectedHash);
  return { noteId, ticket: ticketBody, execution: savedExecution, plans };
}

/** Seed a second retained review version through metadata-only request-changes,
 * edit and review APIs. No approval is granted and no work is materialized. */
export async function reviseOwnedPlanFixture(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  id: string
) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  const path = `/api/workspaces/${seed.workspace.id}/plans/${id}`;
  const snapshot = seed.records.find(record => record.kind === 'plan' && record.path === path);
  expect(snapshot).toBeDefined();
  expect(snapshot!.value.status).toBe('in_review');
  expect(snapshot!.value.current_version).toBe(1);
  expect(snapshot!.value.approved_version).toBe(0);
  const retained = await (await request.get(`${path}/versions/1`)).json();
  const refresh = async () => {
    const value = await (await request.get(path)).json();
    expect(value.id).toBe(id);
    expect(value.studio_id).toBe(seed.workspace.id);
    expect(value.approved_version).toBe(0);
    expect(value.task_links ?? []).toEqual([]);
    expect(value.run_links ?? []).toEqual([]);
    snapshot!.value = value;
    journal(seed);
    return value;
  };
  const decision = await request.post(`${path}/decision`, {
    data: {
      decision: 'request_changes',
      version: 1,
      actor: 'UI fixture',
      reason: 'Retain version one while extending a fictional metadata-only outline.'
    }
  });
  expect(decision.ok(), await decision.text()).toBe(true);
  const draft = await refresh();
  expect(draft.status).toBe('draft');
  const content = structuredClone(draft.draft);
  for (let index = 2; index <= 25; index++)
    content.groups[0].items.push({
      id: `i${index}`,
      description: `Fictional review point ${index}: retain the original notes, compare wording and record no execution results.`,
      expected_result: 'A human-reviewed outline only.',
      author: 'user'
    });
  const edited = await request.patch(`${path}/draft`, {
    data: { revision: draft.draft_revision, objective: draft.objective, content }
  });
  expect(edited.ok(), await edited.text()).toBe(true);
  await refresh();
  const reviewed = await request.post(`${path}/versions`, { data: { actor: 'UI fixture' } });
  expect(reviewed.status(), await reviewed.text()).toBe(201);
  const value = await refresh();
  expect(value.status).toBe('in_review');
  expect(value.current_version).toBe(2);
  const original = await (await request.get(`${path}/versions/1`)).json();
  expect(original.content_hash).toBe(retained.content_hash);
  expect(original.groups).toEqual(retained.groups);
  return value;
}

/** Checkpoint only an explicitly reviewed manual-ticket write, after exact
 * server readback. Never use this to adopt an unexplained cleanup difference. */
export async function checkpointOwnedTicket(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  expected: {
    id: string;
    title: string;
    description: string;
    state: 'backlog' | 'ready';
    version: number;
  }
) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  expect(expected.title.startsWith(`${seed.workspace.name} `)).toBe(true);
  const path = `/api/workspaces/${seed.workspace.id}/tickets/${expected.id}`;
  const response = await request.get(path);
  expect(response.ok()).toBe(true);
  const value = await response.json();
  for (const key of ['id', 'title', 'description', 'state', 'version'] as const)
    expect(value[key]).toBe(expected[key]);
  expect(value.owning_workspace_id).toBe(seed.workspace.id);
  expect(value.schedule_enabled ?? false).toBe(false);
  expect(value.current_run_id ?? '').toBe('');
  const existing = seed.records.find(record => record.path === path);
  if (existing) {
    expect(existing.kind).toBe('ticket');
    existing.value = value;
  } else seed.records.push({ path, value, kind: 'ticket' });
  journal(seed);
  await freezeWorkspaceSeed(request, seed);
  return value;
}

/** Accept only a reviewed same-name note-content save, never a move/rename or
 * unexplained workspace-definition change. Canonical Note deletion owns its file. */
export async function checkpointOwnedNoteContent(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  expected: { id: string; content: string }
) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  const path = `/api/notes/${expected.id}`;
  const snapshot = seed.records.find(record => record.kind === 'note' && record.path === path);
  expect(snapshot).toBeDefined();
  const before = snapshot!.value;
  const response = await request.get(path);
  expect(response.ok()).toBe(true);
  const value = await response.json();
  expect(value).toEqual({ ...before, content: expected.content, updated_at: value.updated_at });
  expect(Date.parse(value.updated_at)).toBeGreaterThanOrEqual(Date.parse(before.updated_at));
  expect(Date.parse(value.updated_at)).toBeLessThanOrEqual(Date.now() + 1000);
  expect(protectedValue(seed.sidecar)).toEqual(seed.protectedValue);
  snapshot!.value = value;
  journal(seed);
  return value;
}

/** Checkpoint one reviewed draft PATCH only. All non-draft Plan fields and
 * workspace task inventory must remain exactly owned and unchanged. */
export async function checkpointOwnedPlanDraft(
  request: APIRequestContext,
  seed: WorkspaceSeed,
  expected: { id: string; draft: Record<string, any>; revision: number }
) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  const path = `/api/workspaces/${seed.workspace.id}/plans/${expected.id}`;
  const snapshot = seed.records.find(record => record.kind === 'plan' && record.path === path);
  expect(snapshot).toBeDefined();
  const original = snapshot!.value;
  expect(original.studio_id).toBe(seed.workspace.id);
  expect(original.status).toBe('draft');
  expect(expected.revision).toBe(original.draft_revision + 1);
  expect(expected.draft.execution).toEqual(original.draft.execution);
  const response = await request.get(path);
  expect(response.ok()).toBe(true);
  const value = await response.json();
  for (const field of ['updated_at', 'last_activity_at']) {
    const time = Date.parse(value[field]);
    expect(time).toBeGreaterThanOrEqual(Date.parse(original[field]));
    expect(time).toBeLessThanOrEqual(Date.now() + 1000);
  }
  expect(value).toEqual({
    ...original,
    draft: expected.draft,
    draft_revision: expected.revision,
    updated_at: value.updated_at,
    last_activity_at: value.last_activity_at
  });
  const workspace = await (await request.get(`/api/workspaces/${seed.workspace.id}`)).json();
  expect((workspace.tasks ?? []).map(task => task.id).sort()).toEqual(
    seed.records
      .filter(record => ['ticket', 'execution'].includes(record.kind))
      .map(record => record.value.id)
      .sort()
  );
  snapshot!.value = value;
  journal(seed);
  return value;
}

/** Prime the two native reads that maintain local metadata: Backlog sync and
 * files/tree indexing. Record only the server-generated owned BACKLOG.md;
 * these GETs are not evidence that arbitrary reads are side-effect-free. */
export async function freezeWorkspaceSeed(request: APIRequestContext, seed: WorkspaceSeed) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  for (const path of [
    `/api/orchestration/backlog?workspace_id=${seed.workspace.id}`,
    `/api/workspaces/${seed.workspace.id}/files/tree`
  ]) {
    const response = await request.get(path);
    expect(response.ok(), await response.text()).toBe(true);
  }
  // Native priming initializes referenced local snapshots, including the
  // built-in assistant on ordinary workspaces. Freeze their exact files/prompts
  // after those known reads; never adopt later drift or own the global assistant.
  if (existsSync(join(seed.sidecar, 'agents'))) {
    const response = await request.get(`/api/workspaces/${seed.workspace.id}/agent-snapshots`);
    expect(response.ok()).toBe(true);
    const snapshots = await response.json();
    expect(snapshots.workspace_id).toBe(seed.workspace.id);
    const prompts: NonNullable<WorkspaceSeed['localAgentPrompts']> = [];
    for (const name of [...snapshots.agents].sort()) {
      const response = await request.get(
        `/api/workspaces/${seed.workspace.id}/agents/${encodeURIComponent(name)}/system-prompt`
      );
      expect(response.ok()).toBe(true);
      prompts.push({ name, value: await response.json() });
    }
    const inventory = folderInventory(join(seed.sidecar, 'agents'));
    if (seed.localAgentInventory) {
      expect(inventory).toEqual(seed.localAgentInventory);
      expect(prompts).toEqual(seed.localAgentPrompts);
    } else {
      seed.localAgentInventory = inventory;
      seed.localAgentPrompts = prompts;
    }
  }
  await verifySeedAgents(request, seed, true);
  const current = await (await request.get(`/api/workspaces/${seed.workspace.id}`)).json();
  seed.generatedFiles = [];
  for (const attachment of current.attachments || []) {
    expect(attachment.title).toBe('BACKLOG.md');
    expect(attachment.workspace_id).toBe(seed.workspace.id);
    expect(attachment.file_meta.relative_path).toBe('BACKLOG.md');
    const candidates = [
      join(seed.sidecar, 'BACKLOG.md'),
      join(seed.sidecar, 'files', 'BACKLOG.md')
    ].filter(existsSync);
    expect(candidates).toHaveLength(1);
    const path = candidates[0];
    expect(lstatSync(path).isSymbolicLink()).toBe(false);
    const hash = createHash('sha256').update(readFileSync(path)).digest('hex');
    expect(hash).toBe(attachment.file_meta.checksum);
    seed.generatedFiles.push({ attachmentId: attachment.id, path, hash });
  }
  journal(seed);
}

export async function retireWorkspaceSeed(request: APIRequestContext, seed: WorkspaceSeed) {
  expect(await assertSandbox(request)).toBe(seed.sandbox);
  expect(lstatSync(seed.manifest).isSymbolicLink()).toBe(false);
  expect(JSON.parse(readFileSync(seed.manifest, 'utf8'))).toEqual(seed);
  expect(lstatSync(seed.sidecar).isSymbolicLink()).toBe(false);
  expect(realpathSync(seed.sidecar)).toBe(seed.sidecar);
  if (seed.protectedValue)
    expect(protectedValue(seed.sidecar), 'preserve changed non-record definition content').toEqual(
      seed.protectedValue
    );
  expect(
    protectedHash(seed.sidecar),
    'preserve fixture if non-record workspace content changed'
  ).toBe(seed.protectedValue ? contentHash(seed.protectedValue) : seed.protectedHash);
  for (const file of seed.generatedFiles || []) {
    expect(lstatSync(file.path).isSymbolicLink()).toBe(false);
    expect(createHash('sha256').update(readFileSync(file.path)).digest('hex')).toBe(file.hash);
  }
  // Inventory is checked before ANY deletion, including records missing from
  // an interrupted seed journal. Per-record readback alone cannot detect them.
  const base = `/api/workspaces/${seed.workspace.id}`;
  const response = await request.get(base);
  expect(response.ok()).toBe(true);
  const workspace = await response.json();
  expect(workspace.id).toBe(seed.workspace.id);
  expect(workspace.name).toBe(seed.workspace.name);
  expect(workspace.kind).toBe(seed.workspace.kind ?? 'workspace');
  expect(workspace.folder_slug).toBe(seed.workspace.folder_slug);
  expect(realpathSync(workspace.sidecar_path)).toBe(seed.sidecar);
  const settings = await (await request.get('/api/settings/workspace-root')).json();
  const root = realpathSync(settings.effective_workspace_root);
  let expectedPath = seed.workspace.folder_slug;
  if (seed.workspace.parent) {
    const parent = seed.workspace.parent;
    const response = await request.get(`/api/workspaces/${parent.id}`);
    expect(response.ok()).toBe(true);
    const current = await response.json();
    expect(current.id).toBe(parent.id);
    expect(current.name).toBe(parent.name);
    expect(current.kind).toBe('group');
    expect(relative(root, realpathSync(current.sidecar_path))).toBe(parent.folder_slug);
    expect(workspace.parent_id).toBe(parent.id);
    expectedPath = join(parent.folder_slug, 'sub-workspaces', seed.workspace.folder_slug);
  }
  expect(relative(root, seed.sidecar)).toBe(expectedPath);
  const ids = (records: { id: string }[]) => records.map(record => record.id).sort();
  const expectedTasks = seed.records
    .filter(record => ['ticket', 'execution'].includes(record.kind))
    .map(record => record.value);
  expect(ids(workspace.tasks ?? []), 'unexpected workspace tasks; preserve entire fixture').toEqual(
    ids(expectedTasks)
  );
  const definition = JSON.parse(readFileSync(join(seed.sidecar, 'workspace.json'), 'utf8'));
  expect(ids(definition.tasks ?? []), 'unexpected sidecar tasks; preserve entire fixture').toEqual(
    ids(expectedTasks)
  );
  expect(ids(workspace.attachments ?? []), 'unexpected workspace attachments').toEqual(
    (seed.generatedFiles ?? []).map(file => file.attachmentId).sort()
  );
  for (const key of ['scheduled_tasks', 'messages', 'folders'])
    expect(workspace[key] ?? [], `unexpected workspace ${key}`).toEqual([]);
  for (const [kind, path, key] of [
    ['note', `${base}/notes`, 'notes'],
    ['plan', `${base}/plans?scope=all`, 'plans']
  ] as const) {
    const listed = await request.get(path);
    expect(listed.ok()).toBe(true);
    const body = await listed.json();
    expect(body).toHaveProperty(key);
    expect(ids(body[key] ?? []), `unexpected workspace ${key}; preserve entire fixture`).toEqual(
      ids(seed.records.filter(record => record.kind === kind).map(record => record.value))
    );
  }
  // Verify all owned contents before deleting any; unexpected edits preserve
  // the fixture/journal for inspection instead of authorizing a wider sweep.
  for (const record of seed.records) {
    const current = await request.get(record.path);
    expect(current.ok()).toBe(true);
    expect(await current.json(), `owned ${record.kind} changed during read-only tour`).toEqual(
      record.value
    );
  }
  await verifySavedWorkflows(request, seed);
  // Library definitions created by the reviewed group roster outlive workspace
  // deletion. Prove their complete ownership/inventory BEFORE the first DELETE.
  await verifySeedAgents(request, seed, true);
  if (existsSync(join(seed.sidecar, 'agents')))
    expect(
      seed.localAgentInventory,
      'unfrozen local agent configuration must be preserved'
    ).toBeDefined();
  if (seed.localAgentInventory) {
    expect(
      folderInventory(join(seed.sidecar, 'agents')),
      'unexpected workspace-local agent configuration; preserve entire fixture'
    ).toEqual(seed.localAgentInventory);
    for (const prompt of seed.localAgentPrompts ?? []) {
      const response = await request.get(
        `/api/workspaces/${seed.workspace.id}/agents/${encodeURIComponent(prompt.name)}/system-prompt`
      );
      expect(response.ok()).toBe(true);
      expect(await response.json()).toEqual(prompt.value);
    }
  }
  for (const record of [...seed.records].reverse()) {
    const path =
      record.kind === 'execution'
        ? `${record.path}&workspace_id=${encodeURIComponent(seed.workspace.id)}`
        : record.path;
    const removed = await request.delete(path);
    expect(removed.ok(), await removed.text()).toBe(true);
    expect((await request.get(record.path)).status()).toBe(404);
  }
  for (const file of seed.generatedFiles || []) {
    const removed = await request.delete(
      `/api/workspaces/${seed.workspace.id}/attachments/${file.attachmentId}`
    );
    expect(removed.ok(), await removed.text()).toBe(true);
  }
  await removeOwnedWorkspace(request, seed.workspace);
  expect(existsSync(seed.sidecar)).toBe(false);
  if (seed.savedWorkflows) {
    await verifySavedWorkflows(request, seed);
    for (const record of [...seed.savedWorkflows.records].reverse()) {
      const path = `/api/workflows/${record.value.id}`;
      const removed = await request.delete(path);
      expect(removed.ok(), await removed.text()).toBe(true);
      expect((await request.get(path)).status()).toBe(404);
      expect(existsSync(join(seed.savedWorkflows.root, `${record.value.id}.json`))).toBe(false);
    }
    expect(folderInventory(seed.savedWorkflows.root)).toEqual(
      seed.savedWorkflows.baselineInventory
    );
    const baseline = await request.get('/api/workflows');
    expect(baseline.ok()).toBe(true);
    expect(
      (await baseline.json()).workflows.sort((a: any, b: any) => a.id.localeCompare(b.id))
    ).toEqual([...seed.savedWorkflows.baselineRecords].sort((a, b) => a.id.localeCompare(b.id)));
  }
  for (const agent of seed.libraryAgents ?? []) {
    const path = `/api/agents/${encodeURIComponent(agent.name)}/detail`;
    if ((await request.get(path)).status() === 404) {
      expect(existsSync(agent.folder)).toBe(false); // Canonical workspace cleanup already retired it.
      continue;
    }
    await verifySeedAgents(request, seed, false);
    const removed = await request.delete(`/api/agents?name=${encodeURIComponent(agent.name)}`);
    expect(removed.ok(), await removed.text()).toBe(true);
    await expect
      .poll(async () => (await request.get(path)).status(), {
        message: 'canonical agent deletion must converge to missing'
      })
      .toBe(404);
    expect(existsSync(agent.folder)).toBe(false);
  }
  unlinkSync(seed.manifest);
}
