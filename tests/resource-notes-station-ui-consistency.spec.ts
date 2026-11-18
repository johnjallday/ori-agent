import { test, expect, type APIRequestContext } from '@playwright/test';
import { readFileSync, realpathSync } from 'node:fs';
import { relative, join } from 'node:path';
import { preparePage, capture } from './helpers/ui-consistency';
import { guardWorkspaceReadTour } from './fixtures/ui-consistency-workspace';
import { resourceFileInventory, journalResourceRead } from './helpers/ui-consistency-resource';
import { boundedCanvasControl as bounded, assertCanvasAxe } from './helpers/ui-consistency-canvas';

let fixture: any,
  before: any,
  check: (() => Promise<void>) | undefined,
  ready = false;
async function json(request: APIRequestContext, path: string) {
  const response = await request.get(path);
  expect(response.ok()).toBe(true);
  return response.json();
}
async function snapshot(request: APIRequestContext) {
  const model = await json(request, '/api/settings/system-model');
  expect(model.configured).toBe(false);
  expect(model.provider).toBe('');
  expect(model.model).toBe('');
  const brief = await json(request, '/api/personal-hq/brief/config');
  expect(brief.configured).toBe(true);
  expect(brief.config.schedule_enabled).toBe(false);
  const look = await json(request, '/api/personal-assistant/second-brain/look');
  expect(look.look.state).toBe('finished');
  expect(look.look.result.headline_by).toBe('rules');
  const workspace = await json(request, `/api/workspaces/${fixture.hq.hq_workspace_id}`);
  const original = fixture.starter_retirement.workspace_after.tasks;
  expect(workspace.tasks.map((task: any) => task.id)).toEqual(original.map((task: any) => task.id));
  const numbers: number[] = [];
  for (const task of workspace.tasks) {
    const expected = structuredClone(original.find((value: any) => value.id === task.id));
    // Canonical ticket reads settle legacy template rows once. Validate that
    // exact metadata migration; never adopt changed content or runtime fields.
    if (task.ticket_state) {
      expect(task.state_history).toEqual([
        {
          actor: 'migration',
          from: '',
          to: 'ready',
          reason: 'migrated from legacy task status',
          timestamp: task.updated_at
        }
      ]);
      expect(Date.parse(task.updated_at)).toBeGreaterThanOrEqual(Date.parse(task.created_at));
      expect(Date.parse(task.updated_at)).toBeLessThanOrEqual(Date.now());
      expect([1, 2]).toContain(task.ticket_number);
      numbers.push(task.ticket_number);
      Object.assign(expected, {
        source_type: 'migration',
        state_history: task.state_history,
        ticket_number: task.ticket_number,
        ticket_state: 'ready',
        ticket_version: 1,
        updated_at: task.updated_at
      });
    }
    expect(task).toEqual(expected);
    expect(task.current_run_id || '').toBe('');
    expect(task.context?.execution_retry).toBeUndefined();
    expect(task.context?.template_setup).not.toBe(true);
  }
  if (numbers.length) expect(numbers.sort()).toEqual([1, 2]);
  return {
    model,
    brief,
    tasks: workspace.tasks,
    look: look.look.result,
    files: resourceFileInventory(fixture.vault)
  };
}
test.beforeEach(async ({ request }, info) => {
  ready = false;
  check = undefined;
  const receipt = process.env.ORI_NOTES_STATION_RECEIPT || '';
  expect(receipt).toBeTruthy();
  const path = realpathSync(receipt);
  expect(relative(realpathSync('.tmp/ui-consistency'), path).startsWith('..')).toBe(false);
  fixture = JSON.parse(readFileSync(path, 'utf8'));
  expect(fixture.phase).toBe('prepared-explicitly-unscheduled');
  expect(realpathSync(fixture.root)).toBe(fixture.root);
  expect(readFileSync(join(fixture.root, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  expect(relative(fixture.root, realpathSync(fixture.vault)).startsWith('..')).toBe(false);
  const status = (await json(request, '/api/personal-hq/status')).status;
  expect(status.workspace.id).toBe(fixture.hq.hq_workspace_id);
  fixture.slug = status.workspace.folder_slug;
  fixture.workspace = status.workspace;
  before = await snapshot(request);
  journalResourceRead(info, 'before', { root: fixture.root, ...before });
  ready = true;
});
test.afterEach(async ({ page, request }, info) => {
  await page.goto('about:blank');
  if (!ready) return;
  const after = await snapshot(request);
  journalResourceRead(info, 'after', { root: fixture.root, ...after });
  if (check) await check();
  expect(after).toEqual(before);
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`real unscheduled Notes station and handled disclosure ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      check = await guardWorkspaceReadTour(page, [{ workspace: fixture.workspace }]);
      await page.goto(`/workspaces/${fixture.slug}`);
      const panel = page.locator('[data-cmd-hq-host="notes"]');
      await expect(panel).toBeVisible();
      await expect(panel.locator('.ws-cmd-modal-title')).toHaveText('Notes');
      await expect(panel.locator('[data-notes-one-liner]')).toHaveText(
        'Your notes, read-only. Ori keeps the open items it found, never your notes.'
      );
      await expect(panel).toContainText(before.look.headline);
      const open = panel.locator('[data-cmd-modal-action="notes-open-folder"]');
      await bounded(open);
      await assertCanvasAxe(page, '[data-cmd-hq-host="notes"]');
      await capture(
        page,
        info,
        'workspace-notes-station-real-header',
        {
          workspaceId: fixture.hq.hq_workspace_id,
          fixture:
            'Actual normally hired/activated HQ with schedule_enabled:false from the first configuration write. Real fixed repository vault, rules-only look. Source bytes, look, model and disabled schedule exact before/after; no mutation during this tour.'
        },
        { fullPage: false }
      );
      const summary = panel.locator('details.ws-notes-handled > summary');
      await bounded(summary);
      await summary.press('Enter');
      await expect(panel.locator('details.ws-notes-handled')).toHaveAttribute('open', '');
      await expect(panel.locator('details.ws-notes-handled')).toContainText('Follow-up made');
      const again = panel.getByRole('button', { name: 'Look again', exact: true });
      await bounded(again);
      await capture(
        page,
        info,
        'workspace-notes-station-real-footer',
        {
          workspaceId: fixture.hq.hq_workspace_id,
          fixture:
            'Native handled disclosure and bounded footer; Look again/Open folder not invoked. No schedule/model or fabricated successful writes.'
        },
        { fullPage: false }
      );
    });
