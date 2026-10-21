import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// A hired agent reports back (folder-hire-brief FR16-FR21): its finished first
// look is a card under Needs you on Home and in the drawer on every other page,
// with Start this, Not now and Read the full report.
//
// It drives a sandboxed demo server whose hires have already finished their
// first looks (a finished look needs a real or canned model run). Run it after
// the live acceptance, against that sandbox:
//
//   ./scripts/e2e.sh --env ORI_HIRE_REPORTS=1 tests/folder-hire-brief.spec.ts
//
// It needs two unread reports, at least one with a suggestion; with fewer it is
// skipped. It never runs a model itself: Start this starts the suggested task,
// whose run is the server's business.
test.skip(
  !process.env.ORI_HIRE_REPORTS,
  'requires a sandbox with finished hired looks (ORI_HIRE_REPORTS)'
);
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

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

async function today(request: APIRequestContext) {
  const response = await request.get('/api/personal-assistant/today');
  expect(response.ok()).toBeTruthy();
  return (await response.json()).today;
}

async function reports(request: APIRequestContext): Promise<Item[]> {
  return ((await today(request)).needs_you?.items || []).filter(
    (item: Item) => item.kind === 'folder_first_look' && item.state === 'finished'
  );
}

async function openDrawer(page: Page) {
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher')
    )
  );
}

let startable: Item | undefined;
let other: Item | undefined;

test('a finished look is a Needs you card on Home and in the drawer elsewhere', async ({
  page,
  request
}) => {
  const all = await reports(request);
  startable = all.find(item => item.suggestion?.text);
  other = all.find(item => item !== startable);
  test.skip(!startable || !other, 'needs two unread reports, one with a suggestion');

  await page.goto('/');
  await openDrawer(page);
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
  // The Needs you count includes it.
  const count = Number(await page.locator('#personalAssistantNeedsYouCount').innerText());
  expect(count).toBeGreaterThanOrEqual(2);

  // Another page: the drawer draws the same card under its Needs you line.
  await page.goto('/agents');
  await openDrawer(page);
  const drawer = page.locator(
    `#personalAssistantNeedsReports article[data-look-id="${startable!.ref.entity_id}"]`
  );
  await expect(drawer).toBeVisible({ timeout: 30_000 });
  await expect(drawer.getByRole('button', { name: 'Start this' })).toBeVisible();

  // Phone width: the card wraps instead of scrolling sideways.
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
  test.skip(!startable, 'the first test found no report to start');
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
  test.skip(!other, 'the first test found no second report');
  await page.goto('/');
  await openDrawer(page);
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
  const report = await request.get(other!.report_route || '/');
  expect(report.ok()).toBeTruthy();
});
