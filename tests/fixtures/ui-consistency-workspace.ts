import { expect, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readFixture } from '../helpers/ui-consistency';
import { modelProviders } from './ui-consistency-scenes';
import type { WorkspaceSeed } from '../helpers/ui-consistency-workspace';

/** Named external reads only. Do not intercept real workspace/ticket/note/plan
 * definitions or manufacture successful writes. */
export async function workspaceMetadataReads(page: Page) {
  await readFixture(page, '**/api/providers', modelProviders);
  await readFixture(page, '**/api/updates/check', { updateAvailable: false });
  await readFixture(page, '**/api/plugins/updates', { updates: [] });
}

export async function guardWorkspaceReadTour(
  page: Page,
  seeds: Pick<WorkspaceSeed, 'workspace'>[]
) {
  const violations: string[] = [];
  const ids = new Set(seeds.map(seed => seed.workspace.id));
  const setupResponses: Promise<void>[] = [];
  // The template-start endpoint is execution-capable in general. Verify the
  // actual owned records have no template setup marker before allowing its
  // native first-open no-op; a pending manual definition is not a setup task.
  for (const seed of seeds) {
    const response = await page.request.get(`/api/workspaces/${seed.workspace.id}`);
    expect(response.ok()).toBe(true);
    const workspace = await response.json();
    for (const task of workspace.tasks || []) expect(task.context?.template_setup).not.toBe(true);
  }
  page.on('response', response => {
    const url = new URL(response.url());
    if (!url.pathname.endsWith('/template-setup/start')) return;
    setupResponses.push(
      (async () => {
        expect(response.ok()).toBe(true);
        const body = await response.json();
        expect(body.started, 'owned first-open must never start setup work').toBe(false);
        expect(['no_setup_task', 'local_activation_required']).toContain(body.reason);
      })()
    );
  });
  await page.route('**/api/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const setupId = /^\/api\/workspaces\/([^/]+)\/template-setup\/start$/.exec(path)?.[1];
    const watcher =
      path === '/api/orchestration/workspace/activate' && ids.has(url.searchParams.get('id') || '');
    const noOpSetup = !!setupId && ids.has(setupId);
    const unsafeRead =
      request.method() === 'GET' &&
      (/^\/api\/mcp\/.*(?:query|details)/.test(path) ||
        path === '/api/action-center/opportunities' ||
        path === '/api/ori-guide');
    if (
      unsafeRead ||
      (!['GET', 'HEAD'].includes(request.method()) &&
        !(request.method() === 'POST' && (watcher || noOpSetup)))
    ) {
      violations.push(`${request.method()} ${path}`);
      return route.abort('blockedbyclient');
    }
    return route.fallback();
  });
  return async () => {
    await Promise.all(setupResponses);
    expect(violations, 'no unexpected writes or execution-capable reads').toEqual([]);
  };
}

/** A coherent run response in the current workspacerun schema. It is a GET
 * presentation fixture, not a persisted/executed run or approval authority. */
export async function simulatedRunReads(
  page: Page,
  workspaceId: string,
  taskId: string,
  status: 'executing' | 'awaiting_approval' | 'succeeded' | 'failed' = 'succeeded'
) {
  const id = randomUUID();
  const timestamp = '2026-01-15T12:00:00Z';
  const run = {
    id,
    workspace_id: workspaceId,
    profile_id: 'general',
    profile_version: '1',
    executor: { kind: 'ori_agent', ref: 'fictional-presentation-only' },
    scope: { target_task_id: taskId },
    policy: { approval: 'final_only', mutation: 'denied', external_effects: 'denied' },
    prompt: 'Fictional run presentation: compare the workshop brief and retain the original notes.',
    status,
    created_at: timestamp,
    started_at: timestamp,
    ...(status === 'succeeded' || status === 'failed' ? { finished_at: timestamp } : {}),
    ...(status === 'failed'
      ? { error: 'Fictional validation refusal: the supplied draft lacks an explicit source.' }
      : {}),
    report: {
      summary: 'Presentation fixture only. No agent, provider, tool or workflow ran.',
      validation_status: status === 'succeeded' ? 'passed' : 'needs_review',
      changed_files: [],
      follow_ups: ['Review the proposed wording before taking any action.'],
      human_review_needed: status === 'awaiting_approval'
    },
    validation_result: {
      checks: [
        { name: 'Preserve source notes', status: 'passed', evidence: 'Fictional read-only result.' }
      ]
    }
  };
  const base = `/api/workspaces/${workspaceId}/runs/${id}`;
  await readFixture(page, `**${base}`, run);
  await readFixture(page, `**${base}/artifacts`, { artifacts: [] });
  await readFixture(page, `**${base}/trace?*`, {
    events: [
      {
        id: `${id}-trace-1`,
        run_id: id,
        sequence: 1,
        kind: 'message',
        message: 'Fictional trace text for bounded layout inspection. '.repeat(120),
        created_at: timestamp
      }
    ],
    next_since: 1,
    has_more: false
  });
  return run;
}

/** Execution lifecycle reads over one real saved definition, never a start or
 * fake mutation result. Keep canonical Ticket state distinct from run outcome. */
export async function simulatedExecutionReads(
  page: Page,
  seed: WorkspaceSeed,
  original: Record<string, any>,
  state: 'running' | 'blocked' | 'completed' | 'failed' | 'review'
) {
  const status = {
    running: 'in_progress',
    blocked: 'waiting_for_choice',
    completed: 'completed',
    failed: 'failed',
    review: 'completed'
  }[state];
  const task = {
    ...original,
    status,
    ticket_state: state === 'completed' ? 'done' : state === 'review' ? 'review' : 'in_progress',
    started_at: '2026-01-15T12:00:00Z',
    context:
      state === 'blocked'
        ? {
            human_loop: {
              state: 'blocked',
              block_id: `fixture-${original.id}`,
              reason: 'The fictional brief needs an explicit audience before it can continue.',
              question: 'Who should read the proposed workshop summary?',
              workflow_step: {
                step_type: 'ask_choice',
                title: 'Choose the audience',
                summary: 'Keep the original notes while choosing a fictional audience.',
                free_text_allowed: false,
                choices: [
                  { id: 'writers', label: 'Workshop writers' },
                  { id: 'researchers', label: 'Workshop researchers' }
                ]
              },
              agent_response: 'The source notes are retained; no further work has been performed.',
              suggested_actions: ['continue_with_instruction', 'retry', 'mark_failed']
            }
          }
        : {},
    result: ['completed', 'review'].includes(state)
      ? 'Fictional result text. No agent, model, workflow or tool ran. '.repeat(80)
      : '',
    error: state === 'failed' ? 'Fictional failed attempt; the Ticket remains In Progress.' : '',
    execution_history: ['completed', 'review', 'failed'].includes(state)
      ? [
          {
            task_id: original.id,
            executed_at: '2026-01-15T12:00:00Z',
            status: state === 'failed' ? 'failed' : 'success',
            result: 'Fictional saved attempt for presentation only.',
            validation_result: {
              validation_status:
                state === 'review'
                  ? 'needs_review'
                  : state === 'failed'
                    ? 'not_applicable'
                    : 'passed',
              storage_status: state === 'review' ? 'skipped_invalid' : 'not_attempted',
              errors:
                state === 'review'
                  ? [
                      {
                        code: 'required',
                        column: 'audience',
                        message: 'Fictional missing audience; review before saving.'
                      }
                    ]
                  : []
            }
          }
        ]
      : []
  };
  const response = await page.request.get(`/api/workspaces/${seed.workspace.id}`);
  expect(response.ok()).toBe(true);
  const workspace = await response.json();
  await readFixture(page, `**/api/orchestration/tasks?id=${original.id}`, task);
  await readFixture(page, `**/api/workspaces/${seed.workspace.id}`, {
    ...workspace,
    tasks: (workspace.tasks || []).map((item: { id: string }) =>
      item.id === original.id ? task : item
    )
  });
  return task;
}

/** Designation-only projection over a real ordinary workspace. This does not
 * create a canonical hired/HQ relationship or mount server-only HQ dialogs. */
export async function simulatedHQRead(page: Page, seed: WorkspaceSeed) {
  await page.route(`**/api/orchestration/workspace?id=${seed.workspace.id}`, async route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const response = await route.fetch();
    expect(response.ok()).toBe(true);
    return route.fulfill({ json: { ...(await response.json()), designation: 'personal_hq' } });
  });
}
