import { test, expect, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync, realpathSync, lstatSync, existsSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { createWorkspaceSeed } from './helpers/ui-consistency-workspace';
import {
  resourcePreservationSnapshot,
  journalResourceRead
} from './helpers/ui-consistency-resource';
import { preparePage, capture, guardReadTour, contrast } from './helpers/ui-consistency';
import { boundedCanvasControl as bounded, assertCanvasAxe } from './helpers/ui-consistency-canvas';

const location = '.tmp/ui-consistency/group-5-plans-host-location.json';
const verification = process.env.ORI_PLANS_UI_VERIFY_FRESH === '1';
const receipt = verification
  ? '.tmp/ui-consistency/group-5-plans-verified-retained.json'
  : '.tmp/ui-consistency/group-5-plans-retained.json';
function owner() {
  const root = process.env.ORI_UI_SANDBOX || '';
  expect(root).toBeTruthy();
  expect(realpathSync(root)).toBe(root);
  expect(lstatSync(root).isSymbolicLink()).toBe(false);
  expect(relative(realpathSync('.tmp/ui-consistency'), root).startsWith('..')).toBe(false);
  expect(JSON.parse(readFileSync(location, 'utf8')).sandbox).toBe(root);
  expect(readFileSync(join(root, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  return root;
}
async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  expect(response.ok(), `${response.status()} ${response.url()}`).toBe(true);
  return response.json();
}
if (process.env.ORI_PLANS_UI_PREPARE === '1' || verification) {
  test('prepare explained retained Plan approval and one pending manual item plus its group, never execution', async ({
    request,
    page
  }) => {
    test.setTimeout(120_000);
    const root = owner();
    expect(existsSync(receipt)).toBe(false);
    const prior = verification
      ? JSON.parse(readFileSync('.tmp/ui-consistency/group-5-plans-retained.json', 'utf8'))
      : null;
    const folders = (await json(await request.get('/api/workspaces'))).folders;
    if (prior) {
      expect(prior.root).toBe(root);
      expect(prior.phase).toBe('prepared-retained-approved-lifetime');
      expect(folders.map((value: any) => value.id)).toEqual([prior.seed.workspace.id]);
    } else expect(folders).toHaveLength(0);
    async function protectedPrior() {
      if (!prior) return null;
      const records: Record<string, any> = {},
        base = `/api/workspaces/${prior.seed.workspace.id}/plans`;
      const paths = [`/api/workspaces/${prior.seed.workspace.id}`, `${base}?scope=all`];
      for (const plan of Object.values(prior.plans) as any[])
        paths.push(
          `${base}/${plan.id}`,
          `${base}/${plan.id}/versions`,
          `${base}/${plan.id}/approvals`
        );
      for (const path of paths) records[path] = await json(await request.get(path));
      const comparable = structuredClone(records),
        progress = comparable[paths[0]].workspace_progress;
      expect(progress.in_progress_tasks).toBe(0);
      expect(progress.active_agents).toBe(0);
      expect(progress.pending_tasks).toBe(2);
      progress.elapsed_time_ms = '[computed workspace age]';
      return { raw: records, comparable, files: resourcePreservationSnapshot(prior.seed.sidecar) };
    }
    const priorBefore = await protectedPrior();
    const seed = await createWorkspaceSeed(request);
    const plans: Record<string, any> = {};
    const checkpoint: any = {
      root,
      seed,
      plans,
      phase: 'created-owned-workspace',
      approvals: [],
      materialization: null,
      protectedPriorBefore: priorBefore,
      intent:
        'Explicit UI fixture approval of exact immutable metadata only. Retain approved records; no DELETE, generation, execution, provider calls or schedules. One UI materialization may create one unassigned pending manual item and its parent group (two underlying records), never execution.'
    };
    const journal = () =>
      writeFileSync(receipt, JSON.stringify(checkpoint, null, 2), { mode: 0o600 });
    journal();
    const base = `/api/workspaces/${seed.workspace.id}/plans`;
    for (const state of ['draft', 'review', 'approved', 'materialized', 'history']) {
      const created = await json(
        await request.post(base, {
          data: {
            title: `Owned ${state} UI acceptance plan`,
            request: 'Local layout fixture only. Never execute or call a provider.',
            objective:
              'Review a fictional brief; any created ticket must stay unassigned, pending and unscheduled.'
          }
        })
      );
      plans[state] = created;
      checkpoint.phase = `created-${state}`;
      journal();
      await json(
        await request.patch(`${base}/${created.id}/draft`, {
          data: {
            revision: created.draft_revision,
            objective: created.objective,
            content: {
              rationale: 'Reviewed local acceptance metadata, not a model draft.',
              groups: [
                {
                  id: 'g1',
                  title: 'Inspect the fictional brief',
                  items: [
                    {
                      id: 'i1',
                      description:
                        'UI fixture only: read the fictional notes. Do not execute this ticket.',
                      expected_result: 'A pending manual fixture ticket with no runtime effects.',
                      author: 'user'
                    }
                  ]
                }
              ],
              execution: { mode: 'step_through' }
            }
          }
        })
      );
      if (['review', 'approved', 'materialized'].includes(state)) {
        await json(
          await request.post(`${base}/${created.id}/versions`, {
            data: { actor: 'Owned sandbox UI fixture' }
          })
        );
        const contract = await json(await request.get(`${base}/${created.id}/versions/1`));
        expect(contract.starts_execution).toBe(false);
        expect(contract.effect).toBe('create_tasks');
        expect(contract.approvable).toBe(true);
        checkpoint[`${state}_contract`] = contract;
        journal();
        if (state === 'approved') {
          const payload = {
            version: contract.version,
            content_hash: contract.content_hash,
            effect: contract.effect,
            user_id: 'local',
            user_name: 'Owned UI fixture — metadata approval only',
            idempotency_key: `owned-ui-fixture:${created.id}:${contract.content_hash}`
          };
          checkpoint.planned_metadata_approval = payload;
          journal();
          const approval = await json(
            await request.post(`${base}/${created.id}/approvals`, { data: payload })
          );
          checkpoint.approvals.push(approval);
          checkpoint.phase = 'approved-exact-metadata-not-materialized';
          journal();
        }
      }
      if (state === 'history')
        await json(
          await request.post(`${base}/${created.id}/archive`, {
            data: {
              reason: 'Owned UI history fixture; preserve its audit trail.',
              actor: 'UI fixture'
            }
          })
        );
      plans[state] = await json(await request.get(`${base}/${created.id}`));
      journal();
    }
    await preparePage(page, 'light', 1440);
    const target = plans.materialized;
    const contract = checkpoint.materialized_contract;
    await page.route('**/api/**', async route => {
      const request = route.request(),
        path = new URL(request.url()).pathname;
      if (['GET', 'HEAD'].includes(request.method())) return route.fallback();
      if (request.method() === 'POST' && path === `${base}/${target.id}/approvals`) {
        const payload = request.postDataJSON();
        expect(payload).toEqual({
          version: contract.version,
          content_hash: contract.content_hash,
          effect: 'create_tasks',
          user_id: '',
          user_name: '',
          idempotency_key: `${target.id}:${contract.version}:${contract.content_hash}`
        });
        checkpoint.planned_ui_approval = payload;
        journal();
        const response = await route.fetch();
        const approval = await json(response);
        checkpoint.approvals.push(approval);
        journal();
        return route.fulfill({ response });
      }
      if (request.method() === 'POST' && path === `${base}/${target.id}/materialize`) {
        const payload = request.postDataJSON();
        expect(payload).toEqual({ approval_id: checkpoint.approvals[1].id });
        checkpoint.planned_materialization = payload;
        journal();
        const response = await route.fetch();
        const result = await json(response);
        checkpoint.materialization = result;
        journal();
        expect(contract.task_count).toBe(1);
        expect(contract.group_count).toBe(1);
        expect(result.task_ids).toHaveLength(contract.task_count + contract.group_count);
        checkpoint.phase = 'materialized-one-pending-manual-item-plus-group';
        journal();
        return route.fulfill({ response });
      }
      throw new Error(`Unapproved fixture operation ${request.method()} ${path}`);
    });
    await page.goto(`/workspaces/${seed.workspace.folder_slug}/plans/${target.id}`);
    const approve = page.locator('#plan-approve');
    await expect(approve).toBeEnabled();
    await expect(approve).toHaveAttribute('data-starts-execution', 'false');
    await approve.click();
    await expect(page.locator('#plan-materialization-summary')).toContainText('Created 2 ticket');
    const workspace = await json(await request.get(`/api/workspaces/${seed.workspace.id}`));
    expect(workspace.tasks).toHaveLength(2);
    expect(workspace.tasks.map((task: any) => task.id).sort()).toEqual(
      [...checkpoint.materialization.task_ids].sort()
    );
    for (const task of workspace.tasks) {
      expect(task.status).toBe('pending');
      expect(task.schedule_enabled ?? false).toBe(false);
      expect(task.assigned_agent ?? '').toBe('');
      expect(task.assigned_agents ?? []).toEqual([]);
    }
    expect(workspace.agent_instances || []).toEqual(seed.protectedValue?.agent_instances || []);
    for (const state of ['approved', 'materialized']) {
      plans[state] = await json(await request.get(`${base}/${plans[state].id}`));
      expect(plans[state].status).toBe(state === 'approved' ? 'in_review' : 'approved');
      const approvals = await json(await request.get(`${base}/${plans[state].id}/approvals`));
      expect(approvals.approvals).toHaveLength(1);
      if (state === 'materialized') {
        expect(approvals.approvals[0].consumed_result.started).toBe(false);
        expect(approvals.approvals[0].consumed_result.task_ids).toEqual(
          checkpoint.materialization.task_ids
        );
      } else expect(approvals.approvals[0].consumed_at).toBeUndefined();
    }
    checkpoint.workspace = workspace;
    const priorAfter = await protectedPrior();
    checkpoint.protectedPriorAfter = priorAfter;
    journal();
    if (priorBefore && priorAfter) {
      expect(priorAfter.comparable).toEqual(priorBefore.comparable);
      expect(priorAfter.files.comparable).toEqual(priorBefore.files.comparable);
    }
    checkpoint.phase = 'prepared-retained-approved-lifetime';
    checkpoint.files = resourcePreservationSnapshot(dirname(seed.sidecar));
    journal();
  });
} else if (process.env.ORI_PLANS_UI_RECONCILE === '1') {
  test('read back the already-committed owned materialization without approving or spending again', async ({
    request
  }) => {
    const root = owner(),
      checkpoint = JSON.parse(readFileSync(receipt, 'utf8'));
    expect(checkpoint.root).toBe(root);
    expect(['created-history', 'materialized-one-pending-manual-item-plus-group']).toContain(
      checkpoint.phase
    );
    const seed = checkpoint.seed,
      base = `/api/workspaces/${seed.workspace.id}/plans`;
    const folders = await json(await request.get('/api/workspaces'));
    expect(folders.folders.map((value: any) => value.id)).toEqual([seed.workspace.id]);
    const contract = checkpoint.materialized_contract;
    expect(contract.task_count).toBe(1);
    expect(contract.group_count).toBe(1);
    expect(contract.starts_execution).toBe(false);
    for (const state of Object.keys(checkpoint.plans))
      checkpoint.plans[state] = await json(
        await request.get(`${base}/${checkpoint.plans[state].id}`)
      );
    for (const state of ['approved', 'materialized']) {
      const value = await json(
        await request.get(`${base}/${checkpoint.plans[state].id}/approvals`)
      );
      expect(value.approvals).toHaveLength(1);
      const approval = value.approvals[0];
      expect(approval.content_hash).toBe(checkpoint[`${state}_contract`].content_hash);
      expect(approval.effect).toBe('create_tasks');
      expect(checkpoint.approvals.some((known: any) => known.id === approval.id)).toBe(true);
      expect(checkpoint.plans[state].status).toBe(state === 'approved' ? 'in_review' : 'approved');
      if (state === 'approved') expect(approval.consumed_at).toBeUndefined();
      else {
        expect(approval.consumed_at).toBeTruthy();
        checkpoint.materialization = approval.consumed_result;
      }
    }
    expect(checkpoint.materialization.started).toBe(false);
    expect(checkpoint.materialization.task_ids).toHaveLength(2);
    const workspace = await json(await request.get(`/api/workspaces/${seed.workspace.id}`));
    expect(workspace.sidecar_path).toBe(seed.sidecar);
    expect(workspace.tasks).toHaveLength(2);
    expect(workspace.tasks.map((task: any) => task.id).sort()).toEqual(
      [...checkpoint.materialization.task_ids].sort()
    );
    expect(workspace.tasks.map((task: any) => task.description).sort()).toEqual(
      [contract.groups[0].title, contract.groups[0].items[0].description].sort()
    );
    for (const task of workspace.tasks) {
      expect(task.status).toBe('pending');
      expect(task.schedule_enabled ?? false).toBe(false);
      expect(task.assigned_agent ?? '').toBe('');
      expect(task.assigned_agents ?? []).toEqual([]);
    }
    expect(workspace.agent_instances || []).toEqual(seed.protectedValue?.agent_instances || []);
    checkpoint.workspace = workspace;
    checkpoint.phase = 'prepared-retained-approved-lifetime';
    checkpoint.files = resourcePreservationSnapshot(dirname(seed.sidecar));
    checkpoint.reconciliation =
      'Pure GET readback only: one parent-group record plus one manual item, exactly the approved content; never execute, approve twice, spend again or DELETE.';
    writeFileSync(receipt, JSON.stringify(checkpoint, null, 2), { mode: 0o600 });
  });
} else {
  let prepared: any,
    beforeApi: Awaited<ReturnType<typeof snapshot>>,
    files: ReturnType<typeof resourcePreservationSnapshot>,
    violations: string[],
    ready = false;
  async function snapshot(request: APIRequestContext) {
    const id = prepared.seed.workspace.id,
      base = `/api/workspaces/${id}/plans`;
    const paths = [`/api/workspaces/${id}`, `${base}?scope=all`];
    for (const plan of Object.values(prepared.plans) as any[])
      paths.push(
        `${base}/${plan.id}`,
        `${base}/${plan.id}/versions`,
        `${base}/${plan.id}/approvals`
      );
    const records: Record<string, unknown> = {};
    for (const path of paths) records[path] = await json(await request.get(path));
    const comparable: any = structuredClone(records);
    const progress = comparable[`/api/workspaces/${id}`].workspace_progress;
    expect(progress.in_progress_tasks).toBe(0);
    expect(progress.active_agents).toBe(0);
    expect(progress.pending_tasks).toBe(2);
    // A computed workspace age is not evidence that pending work executed.
    expect(Number.isFinite(progress.elapsed_time_ms)).toBe(true);
    progress.elapsed_time_ms = '[computed workspace age]';
    return { raw: records, comparable };
  }
  test.beforeEach(async ({ request }, info) => {
    ready = false;
    violations = [];
    const root = owner();
    prepared = JSON.parse(readFileSync(receipt, 'utf8'));
    expect(prepared.root).toBe(root);
    expect(prepared.phase).toBe('prepared-retained-approved-lifetime');
    const workspace = await json(
      await request.get(`/api/workspaces/${prepared.seed.workspace.id}`)
    );
    expect(workspace.sidecar_path).toBe(prepared.seed.sidecar);
    beforeApi = await snapshot(request);
    files = resourcePreservationSnapshot(dirname(prepared.seed.sidecar));
    journalResourceRead(info, 'before', { root, api: beforeApi, files });
    ready = true;
  });
  test.afterEach(async ({ page, request }, info) => {
    await page.goto('about:blank');
    if (!ready) return;
    const api = await snapshot(request),
      after = resourcePreservationSnapshot(dirname(prepared.seed.sidecar));
    journalResourceRead(info, 'after', { root: prepared.root, api, files: after });
    expect(violations).toEqual([]);
    expect(api.comparable).toEqual(beforeApi.comparable);
    expect(after.comparable).toEqual(files.comparable);
  });
  for (const theme of ['light', 'dark'] as const)
    for (const width of [320, 390, 768, 1440])
      test(`real retained versions and approved-draft authority ${theme} ${width}`, async ({
        page
      }, info) => {
        await preparePage(page, theme, width);
        violations = await guardReadTour(page);
        const base = `/workspaces/${prepared.seed.workspace.folder_slug}/plans`;
        await page.goto(base);
        await expect(page.locator('#plan-active-list .plan-card')).toHaveCount(4);
        for (const state of ['approved', 'materialized']) {
          const plan = prepared.plans[state];
          const link = page.locator(`.plan-card[data-plan-id="${plan.id}"] a.plan-card-link`);
          await bounded(link);
          await Promise.all([
            page.waitForURL(`**${base}/${plan.id}`, { waitUntil: 'domcontentloaded' }),
            link.press('Enter')
          ]);
          await expect(page.locator('#plan-title')).toHaveText(plan.title);
          await expect(page.locator('#plan-status-badge')).toHaveText(
            state === 'approved' ? /review/i : /Approved/i
          );
          if (state === 'approved') {
            await expect(page.locator('#plan-editor-readonly')).toBeVisible();
            for (const id of ['plan-save', 'plan-add-group', 'plan-request-review'])
              await expect(page.locator(`#${id}`)).toBeDisabled();
          } else {
            // The server permits a new working draft after approval. Keep that
            // affordance; only the retained version and its effects are immutable.
            await expect(page.locator('#plan-editor-readonly')).toBeHidden();
            for (const id of ['plan-save', 'plan-add-group', 'plan-request-review'])
              await expect(page.locator(`#${id}`)).toBeEnabled();
          }
          expect((await contrast(page.locator('#plan-status-badge'))).ratio).toBeGreaterThanOrEqual(
            4.5
          );
          await assertCanvasAxe(page, '#plan-content');
          await capture(
            page,
            info,
            `workspace-plan-real-${state === 'approved' ? 'approval-recorded' : 'approved-materialized'}`,
            {
              workspaceId: prepared.seed.workspace.id,
              planId: plan.id,
              fixture:
                state === 'approved'
                  ? 'Real exact-hash approval record only, never materialized; Plan truthfully remains in_review. Isolated retained fixture, no execution.'
                  : 'Real exact-hash approval spent for one pending unassigned manual item and its parent group, never started. Complete API/file readback; retained, never DELETE approved records.'
            },
            { fullPage: false }
          );
          await page.locator(`a[href="${base}"]`).click();
        }
      });
}
