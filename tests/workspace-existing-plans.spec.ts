import { test, expect, Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// The plans a workspace already has stay reachable after Ori stopped starting
// new ones: one quiet "Existing plans" link under Tickets, shown only when the
// workspace has at least one (active or in History). Workspaces and plans are
// made through the public API, the way an external client or an upgrade leaves
// them, and removed again afterwards. Isolated demo only.
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
});

const made: string[] = [];
test.afterEach(async ({ page }) => {
  for (const id of made.splice(0)) await page.request.delete(`/api/workspaces/${id}`);
});

async function workspace(page: Page, name: string) {
  const response = await page.request.post('/api/workspaces', {
    data: { name: `${name} ${Date.now()}`, blank: true }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  const created = body.folder ?? body.workspace ?? body;
  made.push(created.id);
  return created as { id: string; folder_slug: string };
}

async function plan(page: Page, workspaceId: string, request: string) {
  const response = await page.request.post(`/api/workspaces/${workspaceId}/plans`, {
    data: { request, source: 'api' }
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string };
}

async function openTickets(page: Page, slug: string) {
  await page.goto(`/workspaces/${slug}?tickets=all`);
  await expect(page.locator('#workspace-detail-tickets-surface')).toBeVisible({ timeout: 20000 });
  await page.waitForFunction(() => (window as any).workspaceDetail?.initialListsLoaded === true);
}

test('a workspace with no plan has no Plans destination under Tickets', async ({ page }) => {
  const ws = await workspace(page, 'No plans');
  await openTickets(page, ws.folder_slug);
  await expect(page.locator('#hubTicketsExistingPlans')).toBeHidden();
  await expect(page.getByRole('link', { name: 'Existing plans' })).toHaveCount(0);
});

test('a plan made over the API, with no ticket, is reachable from Tickets and opens under its own gate', async ({
  page
}) => {
  const ws = await workspace(page, 'API plan');
  const created = await plan(page, ws.id, 'Created by an external client');
  await openTickets(page, ws.folder_slug);

  const link = page.getByRole('link', { name: 'Existing plans' });
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute('href', `/workspaces/${ws.folder_slug}/plans`);
  await link.click();
  await page.waitForURL(`**/workspaces/${ws.folder_slug}/plans`);
  await expect(page.locator(`.plan-card[data-plan-id="${created.id}"]`)).toBeVisible();
  // The collection offers no way to start another one.
  for (const gone of ['#plan-create-panel', '#plan-create-form', '#plan-disabled-banner'])
    await expect(page.locator(gone)).toHaveCount(0);

  // Opening a plan changes nothing: it is still a draft, and nothing was made.
  await page.locator(`.plan-card[data-plan-id="${created.id}"] a.plan-card-link`).click();
  await expect(page.locator('#plan-content')).toBeVisible();
  const after = await (
    await page.request.get(`/api/workspaces/${ws.id}/plans/${created.id}`)
  ).json();
  expect(after.status).toBe('draft');
  expect(after.task_links ?? []).toEqual([]);
  expect(after.run_links ?? []).toEqual([]);
});

test('a workspace whose only plan is in History keeps the way in', async ({ page }) => {
  const ws = await workspace(page, 'History only');
  const created = await plan(page, ws.id, 'Finished long ago');
  const archived = await page.request.post(`/api/workspaces/${ws.id}/plans/${created.id}/archive`, {
    data: { reason: 'Fixture', actor: 'spec' }
  });
  expect(archived.ok(), await archived.text()).toBeTruthy();

  await openTickets(page, ws.folder_slug);
  await expect(page.getByRole('link', { name: 'Existing plans' })).toBeVisible();
  await page.getByRole('link', { name: 'Existing plans' }).click();
  await expect(page.locator('#plan-history-section')).toBeVisible();
  await expect(
    page.locator(`#plan-history-list .plan-card[data-plan-id="${created.id}"]`)
  ).toBeVisible();
});

test('a failed check says so with a retry, never as no plans', async ({ page }) => {
  const ws = await workspace(page, 'Presence failure');
  await plan(page, ws.id, 'There is one');
  let calls = 0;
  await page.route(`**/api/workspaces/${ws.id}/plan-presence`, route => {
    calls += 1;
    return calls === 1
      ? route.fulfill({ status: 503, json: { message: 'down' } })
      : route.continue();
  });
  await openTickets(page, ws.folder_slug);

  const area = page.locator('#hubTicketsExistingPlans');
  await expect(area).toBeVisible();
  await expect(area).toContainText('Could not check for existing plans');
  await expect(page.getByRole('link', { name: 'Existing plans' })).toHaveCount(0);
  await area.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('link', { name: 'Existing plans' })).toBeVisible();
  expect(calls).toBe(2);
});

test('the old bookmark of an empty workspace explains the retirement and links to Tickets', async ({
  page
}) => {
  const ws = await workspace(page, 'Empty bookmark');
  await page.goto(`/workspaces/${ws.folder_slug}/plans`);
  const note = page.locator('#plan-retired-note');
  await expect(note).toBeVisible();
  await expect(note).toContainText('Ori no longer starts new plans');
  await note.getByRole('link', { name: 'Open Tickets' }).click();
  await page.waitForURL(`**/workspaces/${ws.folder_slug}?tickets=all`);
  await expect(page.locator('#workspace-detail-tickets-surface')).toBeVisible({ timeout: 20000 });
});
