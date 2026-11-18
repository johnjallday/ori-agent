import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { assertCanvasAxe, boundedCanvasControl as bounded } from './helpers/ui-consistency-canvas';
import { capture, preparePage, contrast } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  populateWorkspaceSeed,
  seedCanvasWorkflow,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 390, 768, 1440])
    test(`Canvas Add disclosure keeps node choices and cancels without writes ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await populateWorkspaceSeed(request, seed);
      const workflow = await seedCanvasWorkflow(request, seed);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      await page.goto(`/workspaces/${seed.workspace.folder_slug}/canvas`);
      await expect(page.locator('#canvas-workspace-name')).toHaveText(seed.workspace.name);
      await page.waitForFunction(() => Boolean((window as any).agentCanvas?.workspace?.id));
      await capture(
        page,
        info,
        'workspace-canvas-toolbar',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real pending definitions/manual Ticket; no execution, layout writes or node creation'
        },
        { fullPage: false }
      );
      const disclosure = page.locator('#canvas-add-disclosure');
      const trigger = disclosure.locator('summary');
      await bounded(trigger);
      await trigger.press('Enter');
      await expect(disclosure).toHaveAttribute('open', '');
      for (const name of ['Agent', 'Attachment', 'Ticket', 'Store', 'Directory'])
        await bounded(disclosure.getByRole('button', { name, exact: true }));
      await capture(
        page,
        info,
        'workspace-canvas-add-options',
        {
          workspaceId: seed.workspace.id,
          fixture: 'Native Add tool group with all five existing node actions; no action submitted'
        },
        { fullPage: false }
      );
      await page.keyboard.press('Escape');
      await expect(disclosure).not.toHaveAttribute('open');
      await expect(trigger).toBeFocused();
      await trigger.press('Enter');
      await page.locator('#canvas-workspace-name').click();
      await expect(disclosure).not.toHaveAttribute('open');
      await trigger.focus();
      await trigger.press('Enter');
      await disclosure.getByRole('button', { name: 'Agent', exact: true }).click();
      const modal = page.locator('#addAgentNodeModal');
      await expect(modal).toBeVisible();
      await expect(disclosure).not.toHaveAttribute('open');
      await bounded(modal.getByRole('button', { name: 'Close', exact: true }));
      await capture(
        page,
        info,
        'workspace-canvas-agent-choice',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real existing-agent Canvas chooser, not the shared hiring form; canceled without adding a node or hiring'
        },
        { fullPage: false }
      );
      await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(modal).toBeHidden();
      await expect(trigger).toBeFocused();
      for (const [name, selector, field] of [
        ['Attachment', '#addAttachmentNodeModal', '#attachmentTitle'],
        ['Ticket', 'dialog.give-work', '[name="what"]'],
        ['Store', '#addStoreNodeModal', '#store-name'],
        ['Directory', '#addDirectoryModal', '#directory-name']
      ]) {
        await trigger.press('Enter');
        await disclosure.getByRole('button', { name, exact: true }).click();
        const dialog = page.locator(selector);
        await expect(dialog).toBeVisible();
        await dialog.locator(field).fill(`Unsent ${name.toLowerCase()} layout draft`);
        const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
        await bounded(cancel);
        await cancel.press('Enter');
        await expect(dialog).toBeHidden();
        await expect(trigger, `Return from ${name} cancellation`).toBeFocused();
      }
      const filter = page.locator('.workflow-selector-trigger');
      await bounded(filter);
      await filter.press('Enter');
      const choice = page.locator(`[data-workflow-choice="${workflow.parentId}"]`);
      await bounded(choice);
      await page.keyboard.press('Escape');
      await expect(filter).toBeFocused();
      await expect(filter).toHaveAttribute('aria-expanded', 'false');
      await filter.press('Enter');
      await choice.press('Enter');
      await expect(page).toHaveURL(new RegExp(`workflow=${workflow.parentId}`));
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.selectedWorkflowId))
        .toBe(workflow.parentId);
      await expect(filter).toBeFocused();
      expect((await contrast(page.locator('#canvas-workflow-name'))).ratio).toBeGreaterThanOrEqual(
        4.5
      );
      expect(
        (await contrast(page.getByText('Live Updates', { exact: true }))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      await filter.press('Enter');
      await expect(choice).toHaveAttribute('aria-pressed', 'true');
      await capture(
        page,
        info,
        'workspace-canvas-workflow-filter',
        {
          workspaceId: seed.workspace.id,
          fixture: 'Real pending parent/child definitions; native keyboard filter, no execution'
        },
        { fullPage: false }
      );
      const all = page.locator('[data-workflow-choice=""]');
      await bounded(all);
      await all.press('Enter');
      await expect(page).not.toHaveURL(/workflow=/);
      await expect(filter).toBeFocused();
      await filter.press('Enter');
      await page.getByRole('button', { name: 'Create Workflow', exact: true }).press('Enter');
      const work = page.locator('dialog.give-work');
      await expect(work).toBeVisible();
      await work.locator('[name="what"]').fill('Unsent workflow layout draft');
      await work.getByRole('button', { name: 'Cancel', exact: true }).press('Enter');
      await expect(work).toBeHidden();
      await expect(filter).toBeFocused();
      const canvas = page.locator('#agent-canvas');
      await canvas.focus();
      await expect(canvas).toBeFocused();
      const rect = (await canvas.boundingBox())!;
      const beforePan = await page.evaluate(() => ({
        x: (window as any).agentCanvas.state.offsetX,
        y: (window as any).agentCanvas.state.offsetY
      }));
      await page.keyboard.down('Space');
      await page.mouse.move(rect.x + rect.width * 0.6, rect.y + 120);
      await page.mouse.down();
      await page.mouse.move(rect.x + rect.width * 0.6 + 24, rect.y + 108, { steps: 4 });
      await page.mouse.up();
      await page.keyboard.up('Space');
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.offsetX))
        .toBeCloseTo(beforePan.x + 24, 0);
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.offsetY))
        .toBeCloseTo(beforePan.y - 12, 0);
      const wheel = await page.evaluate(() => ({
        scale: (window as any).agentCanvas.state.scale,
        scroll: scrollY
      }));
      await page.mouse.wheel(0, wheel.scale < 2 ? -120 : 120);
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.scale))
        .not.toBe(wheel.scale);
      expect(await page.evaluate(() => scrollY)).toBe(wheel.scroll);
      const documentScroll = await page.evaluate(() => ({
        y: scrollY,
        max: document.documentElement.scrollHeight - innerHeight
      }));
      if (documentScroll.max > 0) {
        await page.mouse.move(8, 450);
        await page.mouse.wheel(0, documentScroll.y > 0 ? -160 : 160);
        await expect.poll(() => page.evaluate(() => scrollY)).not.toBe(documentScroll.y);
      }
      const beforeFit = await page.evaluate(() => (window as any).agentCanvas.state.scale);
      await page.getByTitle('Zoom to Fit (R)', { exact: true }).click();
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.scale))
        .not.toBe(beforeFit);
      const exported = page.waitForEvent('download', { timeout: 5000 });
      await page.getByTitle('Export as Image', { exact: true }).click();
      const download = await exported;
      expect(download.suggestedFilename()).toMatch(/^agent-canvas-.*\.png$/);
      expect(await download.failure()).toBeNull();
      const png = await readFile((await download.path())!);
      expect([...png.subarray(0, 8)]).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
      const dimensions = await canvas.evaluate((node: HTMLCanvasElement) => ({
        width: node.width,
        height: node.height
      }));
      expect(png.readUInt32BE(16)).toBe(dimensions.width);
      expect(png.readUInt32BE(20)).toBe(dimensions.height);
      await canvas.focus();
      const point = await page.evaluate(id => {
        const engine = (window as any).agentCanvas;
        const task = engine.state.tasks.find((entry: any) => entry.id === id);
        if (!task) throw new Error('Owned child is missing from the rendered Canvas');
        const bounds = task.bounds || { x: task.x - 80, y: task.y - 30, width: 160, height: 60 };
        const buttons = ['deleteBtnBounds', 'executeBtnBounds', 'rerunBtnBounds', 'assignBtnBounds']
          .map(key => task[key])
          .filter(Boolean);
        const rect = engine.canvas.getBoundingClientRect();
        for (const dx of [-40, 0, 40])
          for (const dy of [-10, 0, 10]) {
            const x = task.x + dx,
              y = task.y + dy;
            if (
              x < bounds.x ||
              x > bounds.x + bounds.width ||
              y < bounds.y ||
              y > bounds.y + bounds.height
            )
              continue;
            if (
              buttons.some(
                (b: any) =>
                  x >= b.x - 4 && x <= b.x + b.width + 4 && y >= b.y - 4 && y <= b.y + b.height + 4
              )
            )
              continue;
            const clientX = rect.left + engine.state.offsetX + x * engine.state.scale;
            const clientY = rect.top + engine.state.offsetY + y * engine.state.scale;
            if (document.elementFromPoint(clientX, clientY) === engine.canvas)
              return { x: clientX, y: clientY };
          }
        throw new Error('No safe owned task header hit point; never click an action as a fallback');
      }, workflow.childId);
      await page.keyboard.down('Shift');
      await page.mouse.click(point.x, point.y);
      await page.keyboard.up('Shift');
      await expect
        .poll(() =>
          page.evaluate(
            id => (window as any).agentCanvas.state.selectedNodes.has(id),
            workflow.childId
          )
        )
        .toBe(true);
      const details = page.locator('#task-details-panel');
      await expect(details).toBeVisible();
      await expect(details).toContainText('child review point');
      const closeDetails = details.getByRole('button', {
        name: 'Close ticket details',
        exact: true
      });
      // Inspect every enabled action's complete hit area, never activate Run,
      // Delete, Edit, assignment or input wiring during this read-only tour.
      for (const button of await details.getByRole('button').all()) {
        if ((await button.isVisible()) && (await button.isEnabled())) await bounded(button);
      }
      expect(await details.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
      await bounded(closeDetails);
      await assertCanvasAxe(page, '#task-details-panel');
      await capture(
        page,
        info,
        'workspace-canvas-selected-ticket',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Native Shift+click on a safe real child header, no drag persistence or execution'
        },
        { fullPage: false }
      );
      await closeDetails.press('Enter');
      await expect(details).toBeHidden();
      await expect(canvas).toBeFocused();
      await page.keyboard.press('Escape');
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas.state.selectedNodes.size))
        .toBe(0);
      await check();
    });

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440])
    test(`Canvas without Tickets keeps workflow unavailability explicit ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      await page.goto(`/workspaces/${seed.workspace.folder_slug}/canvas`);
      const filter = page.locator('.workflow-selector-trigger');
      await expect(filter).toBeDisabled();
      await expect(filter).toContainText('All Tickets');
      await expect(filter).toContainText('(0)');
      await expect(filter).toHaveAttribute('title', 'No workflows available');
      await expect(page.locator('#workflow-selector-menu')).toBeHidden();
      await expect
        .poll(() => page.evaluate(() => (window as any).agentCanvas?.state?.tasks?.length))
        .toBe(0);
      await capture(
        page,
        info,
        'workspace-canvas-empty',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real empty Ticket inventory; default assistant may remain visible, no fabricated empty response'
        },
        { fullPage: false }
      );
      await check();
    });

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440])
    test(`Canvas loading and refused definition reads are presentation-only ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      let release!: () => void;
      const paused = new Promise<void>(resolve => {
        release = resolve;
      });
      await page.route(`**/api/workspaces/${seed.workspace.id}`, async route => {
        if (route.request().method() !== 'GET') return route.fallback();
        await paused;
        await route.fulfill({
          status: 503,
          json: { error: 'Presentation-only unavailable definition read' }
        });
      });
      try {
        await page.goto(`/workspaces/${seed.workspace.folder_slug}/canvas`, {
          waitUntil: 'domcontentloaded'
        });
        await expect(page.locator('#canvas-workspace-name')).toHaveText(seed.workspace.name);
        await expect(page.locator('#canvas-info')).toContainText('Loading');
        await capture(
          page,
          info,
          'workspace-canvas-loading',
          {
            workspaceId: seed.workspace.id,
            fixture: 'Presentation-only delayed GET; no successful state mutation mocked'
          },
          { fullPage: false }
        );
      } finally {
        release();
      }
      await expect(page.locator('#canvas-info')).toHaveText('Error loading workspace');
      await capture(
        page,
        info,
        'workspace-canvas-unavailable',
        {
          workspaceId: seed.workspace.id,
          fixture: 'Presentation-only 503 definition GET over a real unchanged workspace'
        },
        { fullPage: false }
      );
      await check();
    });
