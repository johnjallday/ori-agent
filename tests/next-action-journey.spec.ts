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
  try {
    const taskIDs: string[] = [];
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
      taskIDs.push(record.id);
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
    await expect(owner).toContainText('2 tasks needing attention');
    await page.screenshot({ path: testInfo.outputPath('home-real-attention.png') });
    await owner.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(`/workspaces/${folder.folder_slug}\\?panel=tasks`));
    await expect(page.locator('.ws-cmd-drawer')).toBeVisible();
    await expect(page.locator('.ws-cmd-drawer')).toContainText('Choose the launch outline');
    await page.screenshot({ path: testInfo.outputPath('workspace-real-attention.png') });
    const failed = await page.request.put('/api/orchestration/tasks', {
      data: { task_id: taskIDs[0], status: 'failed' }
    });
    expect(failed.ok(), await failed.text()).toBe(true);
    await page.reload();
    await expect(page.locator('.ws-cmd-drawer')).toContainText('Failed');
    await page.screenshot({ path: testInfo.outputPath('workspace-real-failed.png') });
  } finally {
    await page.request.delete(`/api/workspaces/${folder.id}`);
  }
});
