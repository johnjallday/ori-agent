import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// Shared acceptance cases for already-finished, unread, stored first looks.
// A caller owns provisioning and explicitly labels live versus scripted models.
export function folderHireReportCases() {
  type Item = {
    kind: string;
    state?: string;
    title: string;
    attribution?: string;
    workspace?: string;
    suggestion?: { text: string; evidence?: string };
    report_route?: string;
    ref: { entity_id: string; workspace_id: string };
  };
  let startable: Item | undefined;
  let other: Item | undefined;
  async function today(request: APIRequestContext) {
    const response = await request.get('/api/personal-assistant/today');
    expect(response.ok()).toBeTruthy();
    return (await response.json()).today;
  }
  async function openDrawer(page: Page) {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel.open(
        document.getElementById('personalAssistantLauncher')
      )
    );
  }

  test('a finished look is a Needs you card on Home and in the drawer elsewhere', async ({
    page,
    request
  }) => {
    const all: Item[] = ((await today(request)).needs_you?.items || []).filter(
      (item: Item) => item.kind === 'folder_first_look' && item.state === 'finished'
    );
    startable = all.find(item => item.suggestion?.text);
    other = all.find(item => item !== startable);
    // Provisioning is required, not an all-skipped green acceptance run.
    expect(startable, 'needs a finished unread report with a suggestion').toBeTruthy();
    expect(other, 'needs a second finished unread report').toBeTruthy();
    await page.goto('/');
    await openDrawer(page);
    const strip = page.locator('#personalAssistantTodayStripToggle');
    if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
    const home = page.locator(
      `#personalAssistantNeedsYouReports article[data-look-id="${startable!.ref.entity_id}"]`
    );
    await expect(home).toBeVisible({ timeout: 30_000 });
    await expect(home).toContainText('Finished its first look');
    await expect(home).toContainText(startable!.attribution || '');
    await expect(home).toContainText('Suggests first');
    await expect(home).toContainText(startable!.suggestion!.text);
    await expect(home.getByRole('button', { name: 'Start this' })).toBeVisible();
    await expect(home.getByRole('button', { name: 'Not now' })).toBeVisible();
    await expect(home.getByRole('link', { name: 'Read the full report' })).toHaveAttribute(
      'href',
      startable!.report_route || ''
    );
    expect(
      Number(await page.locator('#personalAssistantNeedsYouCount').innerText())
    ).toBeGreaterThanOrEqual(2);
    await page.goto('/agents');
    await openDrawer(page);
    const drawer = page.locator(
      `#personalAssistantNeedsReports article[data-look-id="${startable!.ref.entity_id}"]`
    );
    await expect(drawer).toBeVisible({ timeout: 30_000 });
    await expect(drawer.getByRole('button', { name: 'Start this' })).toBeVisible();
    await page.setViewportSize({ width: 375, height: 800 });
    expect(await drawer.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth
      )
    ).toBeLessThanOrEqual(1);
  });

  test('Start this starts the suggestion in its workspace and the card moves to Working on', async ({
    page,
    request
  }) => {
    expect(startable).toBeTruthy();
    await page.goto('/agents');
    await openDrawer(page);
    const card = page.locator(
      `#personalAssistantNeedsReports article[data-look-id="${startable!.ref.entity_id}"]`
    );
    const started = page.waitForResponse(
      response =>
        response.url().endsWith(`/first-look/${startable!.ref.entity_id}/start-suggestion`) &&
        response.request().method() === 'POST'
    );
    await card.getByRole('button', { name: 'Start this' }).click();
    const body = await (await started).json();
    expect(body.task?.task_id).toBeTruthy();
    await expect(card).toHaveCount(0, { timeout: 30_000 });
    const after = await today(request);
    expect(
      (after.needs_you?.items || []).some(
        (item: Item) => item.ref?.entity_id === startable!.ref.entity_id
      )
    ).toBe(false);
    const moved = [...(after.working_on?.items || []), ...(after.done?.items || [])].find(
      (item: Item) =>
        item.kind === 'first_look_suggestion' && item.title === startable!.suggestion!.text
    );
    expect(moved, 'the started task is under Working on (or Done)').toBeTruthy();
  });

  test('Not now puts the other report away and its full report stays readable', async ({
    page,
    request
  }) => {
    expect(other).toBeTruthy();
    await page.goto('/');
    await openDrawer(page);
    const strip = page.locator('#personalAssistantTodayStripToggle');
    if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
    const card = page.locator(
      `#personalAssistantNeedsYouReports article[data-look-id="${other!.ref.entity_id}"]`
    );
    await expect(card).toBeVisible({ timeout: 30_000 });
    await card.getByRole('button', { name: 'Not now' }).click();
    await expect(card).toHaveCount(0, { timeout: 30_000 });
    const after = await today(request);
    for (const section of ['needs_you', 'working_on', 'done']) {
      expect(
        (after[section]?.items || []).some(
          (item: Item) => item.ref?.entity_id === other!.ref.entity_id
        )
      ).toBe(false);
    }
    expect((await request.get(other!.report_route || '/')).ok()).toBeTruthy();
    // A 200 ticket page alone does not prove that its report is readable.
    const tasks = async () => {
      const response = await request.get(
        `/api/orchestration/tasks?workspace_id=${encodeURIComponent(other!.ref.workspace_id)}`
      );
      expect(response.ok()).toBeTruthy();
      return (await response.json()).tasks;
    };
    const before = await tasks();
    await page.goto(other!.report_route!);
    const report = new URL(other!.report_route!, page.url());
    expect(report.pathname).toMatch(/^\/workspaces\/[a-z0-9-]+\/task\/[A-Za-z0-9_-]+$/);
    const taskId = report.pathname.split('/task/')[1];
    const task = before.find((item: { id: string }) => item.id === taskId);
    expect(task?.status).toBe('completed');
    expect(task?.result).toBeTruthy();
    const output = page.locator('#workspace-task-output');
    await expect(output).toBeVisible();
    const plain = (text: string) =>
      text
        .replace(/^\s*[-*_]{3,}\s*$/gm, '')
        .replace(/[`*#]/g, '')
        .replace(/\s+/g, ' ')
        .trim();
    for (const paragraph of task.result.split(/\n\s*\n/).filter(Boolean)) {
      await expect.poll(async () => plain(await output.innerText())).toContain(plain(paragraph));
    }
    const executions = (items: { id: string; execution_count?: number }[]) =>
      items.map(item => [item.id, item.execution_count || 0]).sort();
    expect(executions(await tasks())).toEqual(executions(before));
  });
}

// An inbox hire's first batch as Today reports it (folder-inbox-hire FR19,
// FR20): kind folder_first_batch, keyed by its setup's offer.
export type FirstBatchItem = {
  kind: string;
  state?: string;
  title: string;
  detail?: string;
  attribution?: string;
  workspace?: string;
  actions?: string[];
  report_route?: string;
  ref: { entity_id: string; workspace_id: string; entity_type?: string };
};

// The three inbox cases: a batch to review, nothing to file, and a first scan
// that did not complete. Each is told apart by the primary the card offers.
export const INBOX_FIRST_BATCH_CASES = {
  batch: { action: 'review_batch', primary: 'Review batch', link: true },
  empty: { action: 'open_janitor', primary: 'Open File Janitor', link: true },
  failed: { action: 'retry', primary: 'Retry', link: false }
} as const;

export async function inboxFirstBatchItems(request: APIRequestContext): Promise<FirstBatchItem[]> {
  const response = await request.get('/api/personal-assistant/today');
  expect(response.ok()).toBeTruthy();
  return ((await response.json()).today?.needs_you?.items || []).filter(
    (item: FirstBatchItem) => item.kind === 'folder_first_batch'
  );
}

// expectFirstBatchCard asserts one inbox report on Home's Today and in the
// drawer on another page: its sentence, its one primary (a link to the File
// Janitor review, or Retry), Not now beside a link, and no Start this.
export async function expectFirstBatchCard(
  page: Page,
  item: FirstBatchItem,
  which: keyof typeof INBOX_FIRST_BATCH_CASES
) {
  const want = INBOX_FIRST_BATCH_CASES[which];
  expect(item.actions || []).toContain(want.action);
  expect(item.title).not.toMatch(/\/Users\/|\/private\/|\/var\//);
  const open = async () => {
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel.open(
        document.getElementById('personalAssistantLauncher')
      )
    );
  };
  for (const [path, host] of [
    ['/', '#personalAssistantNeedsYouReports'],
    ['/agents', '#personalAssistantNeedsReports']
  ]) {
    await page.goto(path);
    await open();
    if (path === '/') {
      const strip = page.locator('#personalAssistantTodayStripToggle');
      if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
    }
    const card = page.locator(`${host} article[data-look-id="${item.ref.entity_id}"]`);
    await expect(card).toBeVisible({ timeout: 30_000 });
    await expect(card).toContainText(item.title);
    await expect(card).toContainText(item.attribution || 'File Curator');
    await expect(card.getByRole('button', { name: 'Start this' })).toHaveCount(0);
    if (want.link) {
      await expect(card.getByRole('link', { name: want.primary })).toHaveAttribute(
        'href',
        item.report_route || ''
      );
      await expect(card.getByRole('button', { name: 'Not now' })).toBeVisible();
    } else {
      await expect(card.getByRole('button', { name: want.primary })).toBeVisible();
      await expect(card.getByRole('button', { name: 'Not now' })).toHaveCount(0);
    }
  }
}
