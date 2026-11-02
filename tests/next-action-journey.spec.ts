import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Persisted synthetic task state via real endpoints. Never a provider run.
test('persisted attention tasks project once and open their real owner', async ({
  page
}, testInfo) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false } })
  );
  const response = await page.request.post('/api/workspaces', {
    data: { name: `Next action ${Date.now()}` }
  });
  expect(response.ok()).toBe(true);
  const { folder } = await response.json();
  const agentName = `Next-action Writer ${Date.now()}`;
  expect(
    (
      await page.request.post('/api/agents', { data: { name: agentName, type: 'orchestration' } })
    ).ok()
  ).toBe(true);
  expect(
    (
      await page.request.post(`/api/workspaces/${folder.id}/agents`, {
        data: { agent_name: agentName }
      })
    ).ok()
  ).toBe(true);
  try {
    for (const task of [
      {
        status: 'waiting_for_choice',
        description: 'Choose the launch outline',
        context: {
          human_loop: {
            state: 'waiting_for_choice',
            question: 'Which outline should we use?',
            choices: [{ id: 'short', label: 'Short outline' }]
          }
        }
      },
      {
        status: 'in_progress',
        description: 'Connect the review source',
        context: {
          human_loop: { state: 'blocked', repair: { label: 'Review connection', url: '/agents' } }
        }
      },
      { status: 'in_progress', description: 'Drafting the launch note' },
      {
        status: 'completed',
        description: 'Available launch checklist',
        result: 'Launch checklist ready.'
      }
    ]) {
      const created = await page.request.post('/api/orchestration/tasks', {
        data: { workspace_id: folder.id, description: task.description }
      });
      expect(created.ok(), await created.text()).toBe(true);
      const { task: record } = await created.json();
      const started = await page.request.post(
        `/api/workspaces/${folder.id}/tickets/${record.id}/transition`,
        { data: { to: 'in_progress' } }
      );
      expect(started.ok(), await started.text()).toBe(true);
      if (task.status !== 'in_progress') {
        const updated = await page.request.put('/api/orchestration/tasks', {
          data: { task_id: record.id, status: task.status, result: task.result }
        });
        expect(updated.ok(), await updated.text()).toBe(true);
      }
      if (task.status === 'completed') {
        const reviewed = await page.request.post(
          `/api/workspaces/${folder.id}/tickets/${record.id}/transition`,
          { data: { to: 'review' } }
        );
        expect(reviewed.ok(), await reviewed.text()).toBe(true);
      }
      if (task.context) {
        const contextual = await page.request.put('/api/orchestration/tasks', {
          data: { task_id: record.id, context: task.context }
        });
        expect(contextual.ok(), await contextual.text()).toBe(true);
      }
    }
    const flat = await (await page.request.get('/api/workspaces')).json();
    const summary = flat.folders.find((ws: { id: string }) => ws.id === folder.id);
    expect(summary.needs_attention_count).toBe(2);
    expect(summary.open_task_count).toBe(3);
    expect(summary.active).toBe(true);
    expect(summary.task_summary_available).toBe(true);
    await page.goto('/');
    await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
    const owner = page.locator('#cockpitTodayAttention a').filter({ hasText: folder.name });
    await expect(owner).toContainText('2 tickets needing attention');
    await page.screenshot({ path: testInfo.outputPath('home-real-attention.png') });
    await owner.focus();
    await page.keyboard.press('Enter');
    // Home's link still says panel=tasks; it lands on the board (PRD 760 FR-33).
    await expect(page).toHaveURL(new RegExp(`/workspaces/${folder.folder_slug}`));
    await expect(page.locator('#hubTicketsList')).toContainText('Choose the launch outline');
    await page.screenshot({ path: testInfo.outputPath('workspace-real-attention.png') });
    await page.locator('[data-cmd-view-mode="details"]').click();
    const work = page.getByRole('region', { name: 'Next action', exact: true });
    await expect(work).toContainText('Choose the launch outline');
    await expect(work.getByRole('button', { name: 'All tickets', exact: true })).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: testInfo.outputPath('workspace-work-first.png') });
    // FR-32: a Next action about a ticket opens that ticket on the board.
    await work.getByRole('button', { name: 'Open ticket', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/[?&]ticket=/);
    await expect(page.locator('#hubTicketDetail')).toBeVisible();
    await expect(page.locator('#hubTicketDetailTitle')).toContainText('Choose the launch outline');
    const ticketId = new URL(page.url()).searchParams.get('ticket');
    // The Runs page: a ticket shows its state, and a failed run is a separate signal.
    await page.goto(`/workspaces/${folder.folder_slug}/task/${ticketId}`);
    await expect(page).toHaveTitle(/Choose the launch outline · Runs/);
    await page.getByRole('button', { name: 'Mark as Failed', exact: true }).click();
    await expect(page.locator('#workspace-task-status')).toHaveText('In Progress');
    await expect(page.locator('#workspace-task-run-signal')).toHaveText('Latest run failed');
    await page.screenshot({ path: testInfo.outputPath('workspace-real-failed.png') });
    await page.goto(`/workspaces/${folder.folder_slug}`);
    await page.locator('[data-cmd-view-mode="details"]').click();
    await expect(work).toBeVisible();
    await page.route('**/api/orchestration/tasks?workspace_id=*', route =>
      route.fulfill({ status: 503, body: 'Injected read failure' })
    );
    await page.evaluate(() => (window as any).workspaceDetail.loadTasks());
    await expect(work).toContainText('Some workspace information is unavailable');
    await expect(work).not.toContainText('No open work');
    await page.unroute('**/api/orchestration/tasks?workspace_id=*');
    await work.getByRole('button', { name: 'Retry workspace status' }).click();
    await expect(work.getByRole('button', { name: 'Open ticket', exact: true })).toBeVisible();
  } finally {
    await page.request.delete(`/api/workspaces/${folder.id}`);
  }
});
