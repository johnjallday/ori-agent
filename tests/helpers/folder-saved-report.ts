import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

// Read-only tail of the live REAPER journey. Reusable after slow inference or a
// browser interruption; it cannot provision, retry, or manufacture a report.
export async function verifySavedFirstLook(
  page: Page,
  request: APIRequestContext,
  sandbox: string,
  offerID: string,
  expectedWorkspaceRoute?: string,
  sourceFixture = { subject: 'Third', folder: join('Downloads', 'Third'), file: 'Third.rpp' }
) {
  expect(offerID).toBeTruthy();
  const get = async (url: string) => {
    const response = await request.get(url);
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const offer = (
    await get(`/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(offerID)}`)
  ).folder_digest.offer;
  expect(offer.id).toBe(offerID);
  expect(offer.subject.name).toBe(sourceFixture.subject);
  expect(offer.first_task.state).toBe('finished');
  expect(offer.first_task.workspace_id).toBe(offer.outcome.workspace_id);
  expect(offer.outcome.route).toMatch(/^\/workspaces\/[a-z0-9-]+$/);
  if (expectedWorkspaceRoute) expect(offer.outcome.route).toBe(expectedWorkspaceRoute);
  const look = offer.first_task;
  const listTasks = async () =>
    (await get(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(look.workspace_id)}`))
      .tasks;
  const before = await listTasks();
  const task = before.find((item: { id: string }) => item.id === look.task_id);
  expect(task?.status).toBe('completed');
  expect(task?.execution_count).toBe(1);
  expect(task?.result).toBeTruthy();
  const workspaceIDs = async () => {
    const result = await get('/api/workspaces');
    return (Array.isArray(result) ? result : result.workspaces || result.folders || [])
      .map((row: { id: string }) => row.id)
      .sort();
  };
  const ids = await workspaceIDs();
  const source = join(sandbox, sourceFixture.folder);
  const sourceUnchanged = () => {
    const entries = readdirSync(source, { withFileTypes: true });
    expect(entries.map(entry => entry.name)).toEqual([sourceFixture.file]);
    expect(entries[0].isFile(), 'original file, not a new directory or symlink').toBe(true);
    expect(
      createHash('sha256')
        .update(readFileSync(join(source, sourceFixture.file)))
        .digest('hex')
    ).toBe(createHash('sha256').update('<REAPER_PROJECT 0.1 "7.0"\n>\n').digest('hex'));
  };
  sourceUnchanged();
  const starts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && /\/execute$|\/start|\/retry$|\/setup$/.test(req.url()))
      starts.push(req.url());
  });
  await page.goto(offer.outcome.route);
  // The live journey arrives with the drawer open; the standalone saved-tail
  // check may not. Close it normally before using the workspace behind it.
  const panel = page.locator('#personalAssistantPanel');
  if (await panel.isVisible()) {
    await page.locator('#personalAssistantClose').click();
    await expect(panel).toBeHidden();
  }
  await page.getByRole('button', { name: 'Tickets', exact: true }).click();
  await expect(page.locator(`[data-ticket-id="${look.task_id}"]`).first()).toBeVisible();
  // Recipe-backed optional work remains discoverable after completion.
  if (look.recipe) await expect(page.locator('#workspaceFirstTaskBanner')).toBeVisible();
  else await expect(page.locator('#workspaceFirstTaskBanner')).toBeHidden();
  expect(look.report_route).toMatch(/^\/workspaces\/[a-z0-9-]+\/task\/[A-Za-z0-9_-]+$/);
  expect(look.report_route).toBe(`${offer.outcome.route}/task/${encodeURIComponent(look.task_id)}`);
  // Select this existing offer through the real scoped GET, not a mocked body
  // or a guessed default offer. The store can also contain older pending offers.
  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.continue({
      url: new URL(
        `/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(offerID)}`,
        route.request().url()
      ).href
    })
  );
  await page.goto('/');
  if (!(await panel.isVisible())) await page.locator('#personalAssistantLauncher').click();
  await expect(panel).toBeVisible();
  const strip = page.locator('#personalAssistantTodayStripToggle');
  if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
  const report = page.locator('#personalAssistantFolderOffer').getByRole('link', {
    name:
      look.recipe === 'folder_summary'
        ? 'Open summary'
        : look.recipe === 'software_assessment'
          ? 'Open the assessment'
          : 'Open the full report',
    exact: true
  });
  await expect(report).toHaveAttribute('href', look.report_route);
  await report.click();
  const output = page.locator('#workspace-task-output');
  await expect(output).toBeVisible();
  // Fence language tags are Markdown metadata, not visible report content.
  // Keep every body paragraph/code line in the comparison.
  const plain = (text: string) =>
    text
      .replace(/^[\t ]{0,3}(?:`{3,}|~{3,})[^\n]*$/gm, '')
      .replace(/^[\t ]{0,3}(?:-{3,}|\*{3,}|_{3,})[\t ]*$/gm, '')
      .replace(/[`*#]/g, '')
      .replace(/\s+/g, ' ')
      .trim();
  const expectReportBody = async () => {
    await expect(output).toBeVisible();
    for (const paragraph of task.result.split(/\n\s*\n/).filter(Boolean)) {
      await expect.poll(async () => plain(await output.innerText())).toContain(plain(paragraph));
    }
  };
  await expectReportBody();
  await page.reload();
  await expectReportBody();
  await page.evaluate(() => document.fonts.ready);
  const executions = (items: { id: string; execution_count?: number }[]) =>
    items.map(item => [item.id, item.execution_count || 0]).sort();
  expect(executions(await listTasks())).toEqual(executions(before));
  expect(await workspaceIDs()).toEqual(ids);
  sourceUnchanged();
  expect(starts).toEqual([]);
  return look.report_route as string;
}
