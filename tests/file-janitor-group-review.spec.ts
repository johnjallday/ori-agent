import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import {
  mkdtempSync,
  writeFileSync,
  utimesSync,
  existsSync,
  mkdirSync,
  realpathSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Real APIs and saved state, credential-free. No picker/provider/group endpoint
// is mocked. Only unrelated first-run onboarding is suppressed. Every managed
// folder is a new synthetic OS-temp directory, never the user's Downloads.
const evidence = 'tasks/evidence-file-janitor-group-review';
const old = new Date(Date.now() - 60 * 60 * 1000);

test.describe.configure({ mode: 'serial' });

function syntheticRoot(names: string[]) {
  const temp = realpathSync(tmpdir());
  const root = realpathSync(mkdtempSync(join(temp, 'ori-group-review-')));
  if (!root.startsWith(temp) || root === temp) throw new Error('Non-temporary fixture');
  for (const name of names) {
    writeFileSync(join(root, name), 'Synthetic group review fixture', { mode: 0o600 });
    utimesSync(join(root, name), old, old);
  }
  return root;
}

async function workspace(request: APIRequestContext) {
  const created = await request.post('/api/workspaces', {
    data: { name: `Group review ${Date.now()} ${Math.random().toString(36).slice(2, 6)}` }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const body = await created.json();
  const folder = body.folder || body.workspace;
  const installed = await request.post(
    `/api/workspaces/${folder.id}/capabilities/file-janitor/install`,
    {
      data: { source: 'in-place' }
    }
  );
  expect(installed.ok(), await installed.text()).toBeTruthy();
  return { id: folder.id as string, slug: folder.folder_slug as string };
}

async function manualReview(request: APIRequestContext, names: string[]) {
  const ws = await workspace(request);
  const root = syntheticRoot(names);
  const link = await request.post(`/api/workspaces/${ws.id}/directories`, {
    data: { name: 'Synthetic group inbox', path: root }
  });
  expect(link.ok(), await link.text()).toBeTruthy();
  const ref = (await link.json()).directory.id;
  const base = `/api/workspaces/${ws.id}/file-janitor`;
  const review = await request.post(`${base}/setup-review`, {
    data: { reference_id: ref, monitoring: false }
  });
  expect(review.ok(), await review.text()).toBeTruthy();
  const proposal = (await review.json()).review;
  expect(existsSync(join(root, 'Filed'))).toBe(false);
  const prepare = await request.post(`${base}/prepare-review`, {
    data: { reference_id: ref, monitoring: false, review_revision: proposal.review_revision }
  });
  expect(prepare.ok(), await prepare.text()).toBeTruthy();
  const status = (await (await request.get(base)).json()).status;
  expect(status.settings.setup_decision.mode).toBe('manual');
  expect(status.settings.paused).toBe(true);
  return { ...ws, root, base };
}

async function openReview(page: Page, slug: string, extra = '') {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
  const url = `/workspaces/${encodeURIComponent(slug)}?view=details&panel=file-janitor&tab=review${extra}`;
  await page.goto(url);
  const console = page.locator('#fileJanitorConsole');
  await expect(console).toBeVisible({ timeout: 20_000 });
  // A no-agent workspace may show the unrelated Commander nudge. Respect native
  // dialog ownership: close Review, decline the nudge, reopen the saved route.
  const nudge = page.locator('#workspace-detail-task-confirm-modal');
  if (await nudge.isVisible()) {
    await console.getByRole('button', { name: 'Close File Janitor', exact: true }).click();
    await nudge.getByRole('button', { name: 'Not now', exact: true }).click();
    await expect(nudge).toBeHidden();
    await page.goto(url);
    await expect(console).toBeVisible();
  }
  return console;
}

test('agentless real grouped entry pages a 206-member group without selecting or moving files', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const names = Array.from({ length: 206 }, (_, i) => `image-${String(i).padStart(4, '0')}.png`);
  const ws = await manualReview(request, names);
  const mutations: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/file-janitor')) mutations.push(req.url());
  });
  const console = await openReview(page, ws.slug);
  await expect(console.locator('#fileJanitorView-groups')).toHaveAttribute('aria-pressed', 'true');
  await expect(console.locator('.fj-group-accounting')).toContainText('206 candidates');
  await expect(console.locator('.fj-group-card')).toHaveCount(1);
  await expect(console.locator('.fj-group-samples li')).toHaveCount(3);
  await expect(console.locator('.fj-group-count')).toHaveText('206 files');
  await expect(console.locator('.fj-row-item')).toHaveCount(0);
  await expect(console.locator('#fileJanitorApprove')).toBeDisabled();
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/01-groups.png`, fullPage: true });
  await console.getByRole('button', { name: 'See 206 files in Images', exact: true }).click();
  const details = console.locator('#fileJanitorGroup-details');
  await expect(details).toContainText('Showing 1–50 of 206');
  await expect(details.locator('.fj-row-item')).toHaveCount(50);
  await expect(details.locator('input.fj-select:checked')).toHaveCount(0);
  await details.getByRole('button', { name: 'Next group files', exact: true }).click();
  await expect(details).toContainText('Showing 51–100 of 206');
  await details.locator('.fj-group-title').scrollIntoViewIfNeeded();
  await expect(details.locator('.fj-group-title')).toBeInViewport();
  await page.screenshot({ path: `${evidence}/01-group-details.png`, fullPage: true });
  for (const expected of ['101–150', '151–200', '201–206']) {
    await details.getByRole('button', { name: 'Next group files', exact: true }).click();
    await expect(details).toContainText(`Showing ${expected} of 206`);
  }
  await expect(details.locator('.fj-row-item')).toHaveCount(6);
  await expect(
    details.getByRole('button', { name: 'Next group files', exact: true })
  ).toBeDisabled();
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await expect(console.locator('.fj-row-item')).toHaveCount(50);
  await console.getByRole('button', { name: 'Groups', exact: true }).click();
  await expect(details).toContainText('Showing 201–206 of 206');
  await details.getByRole('button', { name: 'Close file list', exact: true }).click();
  await expect(
    console.getByRole('button', { name: 'See 206 files in Images', exact: true })
  ).toBeFocused();
  expect(mutations).toEqual([]);
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
  const history = await (await request.get(`${ws.base}/history`)).json();
  expect(history.total).toBe(0);
});

test('206 mixed candidates have exclusive whole-batch accounting and reachable deep links', async ({
  page,
  request
}) => {
  const names = [
    ...Array.from({ length: 18 }, (_, i) => `installer-${i}.dmg`),
    ...Array.from({ length: 31 }, (_, i) => `photo-${i}.png`),
    ...Array.from({ length: 100 }, (_, i) => `report-${i}.pdf`),
    ...Array.from({ length: 20 }, (_, i) => `track-${i}.wav`),
    ...Array.from({ length: 5 }, (_, i) => [`notes-${i}-v1.pdf`, `notes-${i}-v2.pdf`]).flat(),
    ...Array.from({ length: 20 }, (_, i) => `unknown-${i}.zzzz`),
    ...Array.from({ length: 7 }, (_, i) => `tax-${i}.csv`)
  ];
  expect(names).toHaveLength(206);
  const ws = await manualReview(request, names);
  const groups = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review;
  expect(groups.counts).toMatchObject({ candidates: 206, filing: 149, questions: 57 });
  expect(
    groups.groups.reduce((sum: number, group: { count: number }) => sum + group.count, 0)
  ).toBe(206);
  const console = await openReview(page, ws.slug);
  await expect(console.locator('.fj-group-card')).toHaveCount(groups.groups.length);
  await expect(console.locator('.fj-group-accounting')).toContainText('206 candidates');
  const detail = await (
    await request.get(`${ws.base}/batches/${groups.batch_id}?limit=1&offset=205`)
  ).json();
  await openReview(page, ws.slug, `&batch_id=${groups.batch_id}&item=${detail.candidates[0].id}`);
  await expect(console.locator('#fileJanitorView-all')).toHaveAttribute('aria-pressed', 'true');
  await expect(console.locator('#fileJanitorLinkedRow')).toBeVisible();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
});

test('installed setup-required entry is not an empty saved review', async ({ page, request }) => {
  const ws = await workspace(request);
  const console = await openReview(page, ws.slug);
  await expect(console.locator('.fj-group-card')).toHaveCount(0);
  await expect(console).not.toContainText('No pending proposal groups');
  await expect(console).toContainText(/folder/i);
  const response = await request.get(`/api/workspaces/${ws.id}/file-janitor/batches/latest/groups`);
  expect(response.status()).toBe(409);
  expect((await response.json()).code).toBe('review_unavailable');
});
