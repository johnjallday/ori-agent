import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import {
  mkdtempSync,
  writeFileSync,
  utimesSync,
  existsSync,
  mkdirSync,
  realpathSync,
  chmodSync,
  symlinkSync
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

async function workspace(request: APIRequestContext, label = 'Group review') {
  const created = await request.post('/api/workspaces', {
    data: { name: `${label} ${Date.now()} ${Math.random().toString(36).slice(2, 6)}` }
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

async function manualReview(
  request: APIRequestContext,
  names: string[],
  label?: string,
  useAlias = false
) {
  const ws = await workspace(request, label);
  const root = syntheticRoot(names);
  const referencePath = useAlias
    ? join(
        realpathSync(mkdtempSync(join(realpathSync(tmpdir()), 'ori-group-alias-'))),
        'approved-inbox'
      )
    : root;
  if (useAlias) symlinkSync(root, referencePath, 'dir');
  const link = await request.post(`/api/workspaces/${ws.id}/directories`, {
    data: { name: 'Synthetic group inbox', path: referencePath }
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
  return { ...ws, root, base, referencePath };
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
  await expect(console.locator('.fj-group-card .fj-group-count')).toHaveText('206 files');
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

test('two group choices stage 49 exact moves and stay pinned when a newer review arrives', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = [
    ...Array.from({ length: 18 }, (_, i) => `installer-${String(i).padStart(3, '0')}.dmg`),
    ...Array.from({ length: 31 }, (_, i) => `image-${String(i).padStart(3, '0')}.png`)
  ];
  const ws = await manualReview(request, names, 'Group review 49 choices');
  const console = await openReview(page, ws.slug);
  const group = (label: string) =>
    console.getByRole('checkbox', { name: `Select filing proposals in ${label}`, exact: true });
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  await group('Installers').click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('18 files selected.');
  await expect(group('Installers')).toBeChecked();
  await group('Images').click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  await expect(console.locator('#fileJanitorApprove')).toHaveText('Review 49 moves');
  await expect(console.locator('#fileJanitorApprove')).toBeEnabled();
  mkdirSync(evidence, { recursive: true });
  await console
    .locator('.fj-group-plan-layout')
    .evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/02-group-selection.png`, fullPage: true });
  const batchID = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review
    .batch_id;
  const draft = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  expect(draft.choices).toHaveLength(49);
  expect(draft.choices.every((choice: { operation: string }) => choice.operation === 'move')).toBe(
    true
  );
  writeFileSync(join(ws.root, 'later-arrival.png'), 'Synthetic later arrival', { mode: 0o600 });
  utimesSync(join(ws.root, 'later-arrival.png'), old, old);
  await console.getByRole('button', { name: 'Scan now', exact: true }).click();
  await expect(console).toContainText('A newer saved review is available');
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  await expect(console.locator('#fileJanitorBatchChoice')).toHaveValue(batchID);
  await expect(page).toHaveURL(new RegExp(`batch_id=${batchID}`));
  await page.reload();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  const newest = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review
    .batch_id;
  expect(newest).not.toBe(batchID);
  await console.locator('#fileJanitorBatchChoice').selectOption(newest);
  await expect(console.locator('.fj-group-accounting')).toContainText('1 candidates');
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  await console.locator('#fileJanitorBatchChoice').selectOption(batchID);
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  for (const name of [...names, 'later-arrival.png'])
    expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('large saved group choices preserve off-page exclusions, destination exceptions and group Undo', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = Array.from({ length: 206 }, (_, i) => `image-${String(i).padStart(4, '0')}.png`);
  const ws = await manualReview(request, names, 'Group review durable choices');
  const console = await openReview(page, ws.slug);
  const selectGroup = console.getByRole('checkbox', {
    name: 'Select filing proposals in Images',
    exact: true
  });
  await console.getByRole('button', { name: 'See 206 files in Images', exact: true }).click();
  const details = console.locator('#fileJanitorGroup-details');
  const prior = details.getByRole('checkbox', { name: 'Select image-0000.png', exact: true });
  await details
    .getByRole('combobox', { name: 'Category for image-0000.png', exact: true })
    .selectOption('documents');
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  await expect(prior).toBeEnabled();
  await prior.click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await selectGroup.click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('206 files selected.');
  await details.getByRole('button', { name: 'Next group files', exact: true }).click();
  await expect(details).toContainText('Showing 51–100 of 206');
  await details.getByRole('checkbox', { name: 'Select image-0051.png', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('205 files selected.');
  await expect(selectGroup).toHaveJSProperty('indeterminate', true);
  await details.locator('.fj-group-title').scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${evidence}/02-member-exception.png`, fullPage: true });
  await console
    .getByRole('combobox', { name: 'Destination for selected group files in Images', exact: true })
    .selectOption('archives');
  await expect(console.locator('.fj-review-plan')).toContainText('204 → Filed/Archives');
  const batchID = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review
    .batch_id;
  let draft = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  expect(
    draft.choices.filter((choice: { category: string }) => choice.category === 'documents')
  ).toHaveLength(1);
  expect(
    draft.choices.filter((choice: { category: string }) => choice.category === 'archives')
  ).toHaveLength(204);
  await console
    .getByRole('button', {
      name: 'Undo group mark for Images, keeping per-file choices',
      exact: true
    })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await selectGroup.click(); // a new, explicit snapshot selection
  await expect(console.locator('#fileJanitorSelection')).toHaveText('206 files selected.');
  await details.getByRole('checkbox', { name: 'Select image-0051.png', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('205 files selected.');
  for (const range of ['101–150', '151–200']) {
    await details.getByRole('button', { name: 'Next group files', exact: true }).click();
    await expect(details).toContainText(`Showing ${range} of 206`);
  }
  await details.getByRole('checkbox', { name: 'Select image-0151.png', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('204 files selected.');
  draft = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  expect(draft.choices).toHaveLength(204);
  const members = await (await request.get(`${ws.base}/batches/${batchID}?limit=200`)).json();
  const excludedIDs = members.candidates
    .filter((candidate: { name: string }) =>
      ['image-0051.png', 'image-0151.png'].includes(candidate.name)
    )
    .map((candidate: { id: string }) => candidate.id);
  expect(excludedIDs).toHaveLength(2);
  expect(
    draft.choices.every(
      (choice: { candidate_id: string }) => !excludedIDs.includes(choice.candidate_id)
    )
  ).toBe(true);
  await page.reload();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('204 files selected.');
  await expect(selectGroup).toHaveJSProperty('indeterminate', true);
  expect(
    (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft.choices
  ).toEqual(draft.choices);
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('injected save failure is unsaved and two real tabs resolve a canonical conflict by reloading', async ({
  page,
  request,
  context
}) => {
  const ws = await manualReview(request, ['one.png', 'two.png']);
  const console = await openReview(page, ws.slug);
  const other = await context.newPage();
  const second = await openReview(other, ws.slug);
  const group = console.getByRole('checkbox', {
    name: 'Select filing proposals in Images',
    exact: true
  });
  await expect(group).toBeEnabled();
  await expect(
    second.getByRole('checkbox', { name: 'Select filing proposals in Images', exact: true })
  ).toBeEnabled();
  let fail = true;
  await page.route('**/file-janitor/batches/*/draft', route => {
    if (route.request().method() === 'POST' && fail) {
      fail = false;
      return route.abort('failed');
    }
    return route.continue();
  });
  await group.click();
  await expect(console.locator('#fileJanitorDraftStatus')).toContainText('not confirmed saved');
  await expect(console.locator('#fileJanitorApprove')).toBeDisabled();
  await console.getByRole('button', { name: 'Retry exact edit', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
  await second
    .getByRole('checkbox', { name: 'Select filing proposals in Images', exact: true })
    .click();
  await expect(second.locator('#fileJanitorDraftStatus')).toContainText('Review changed');
  await expect(second.locator('#fileJanitorApprove')).toBeDisabled();
  await other.screenshot({ path: `${evidence}/02-save-conflict.png`, fullPage: true });
  await second.getByRole('button', { name: 'Reload saved choices', exact: true }).click();
  await expect(second.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
  await second.getByRole('button', { name: 'See 2 files in Images', exact: true }).click();
  await second.getByRole('checkbox', { name: 'Select one.png', exact: true }).click();
  await expect(second.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await console.locator('#fileJanitorApprove').click();
  await expect(console.locator('#fileJanitorDraftStatus')).toContainText('unavailable');
  await expect(console.locator('#fileJanitorConfirmApply')).toHaveCount(0);
  await console.getByRole('button', { name: 'Reload saved choices', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  expect(existsSync(join(ws.root, 'one.png'))).toBe(true);
  expect(existsSync(join(ws.root, 'two.png'))).toBe(true);
  await other.close();
});

test('real focused questions keep exact music states and reconsider only one item before explicit scan', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const hostile = 'tax-<img onerror=alert(1)>.csv';
  const names = [
    'song.wav',
    'song.zip',
    'song.pdf',
    'song.rpp',
    'notes-v1.pdf',
    'notes-v2.pdf',
    hostile,
    'photo.png',
    'mystery.zzzz'
  ];
  const ws = await manualReview(request, names, 'Focused questions');
  const review = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review;
  expect(review.counts).toMatchObject({ candidates: 9, filing: 1, questions: 8 });
  const batchID = review.batch_id;
  const console = await openReview(page, ws.slug);
  const card = (label: string) =>
    console
      .locator('.fj-group-card')
      .filter({ has: page.getByRole('heading', { name: label, exact: true }) });
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  await console.getByRole('button', { name: 'Select all filing groups', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  const versions = card('Possible copies or versions');
  await versions
    .getByRole('button', { name: 'Not now: Possible copies or versions', exact: true })
    .click();
  await expect(versions).toContainText('Still pending');
  await expect(console.locator('.fj-group-accounting')).toContainText('8 need a decision');
  expect(posts).toHaveLength(1);
  await versions
    .getByRole('button', { name: 'Reopen question: Possible copies or versions', exact: true })
    .click();
  await expect(versions).toContainText('not been compared');
  await card('Sensitive-looking names')
    .getByRole('button', { name: /See .* files in Sensitive-looking names/ })
    .click();
  await expect(console.locator('#fileJanitorGroup-details')).toContainText(hostile);
  expect(await console.locator('#fileJanitorGroup-details img').count()).toBe(0);
  await console
    .locator('#fileJanitorGroup-details')
    .getByRole('button', { name: `Show file details for ${hostile}`, exact: true })
    .click();
  await expect(console.locator('#fileJanitorGroup-details')).toContainText(
    'Saved metadata classification'
  );
  mkdirSync(evidence, { recursive: true });
  await console.locator('.fj-group-grid').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/03-focused-questions.png`, fullPage: true });
  const music = card('Audio and related material');
  await expect(music).toContainText('4 files');
  await expect(music).toContainText('not verified projects or duplicates');
  await music.getByRole('button', { name: /See .* files in Audio and related material/ }).click();
  await console.getByRole('checkbox', { name: 'Select song.wav', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
  await console.getByRole('button', { name: 'Mark song.rpp for Trash', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toContainText('1 file marked for Trash');
  await music
    .getByRole('button', { name: 'Keep Audio and related material here', exact: true })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await expect(console.locator('.fj-groups')).toContainText('4 kept here');
  await expect(music).toHaveCount(0);
  const skips = (await (await request.get(`${ws.base}/skipped`)).json()).skipped;
  expect(skips.map((item: { name: string }) => item.name).sort()).toEqual([
    'song.pdf',
    'song.rpp',
    'song.wav',
    'song.zip'
  ]);
  await console.getByRole('button', { name: 'See kept files', exact: true }).click();
  await expect(console.locator('.fj-row-item')).toHaveCount(4);
  await console.getByRole('button', { name: 'Review song.wav again', exact: true }).click();
  await expect(console.locator('.fj-batch-scan-status')).toContainText(
    'Nothing scanned or selected'
  );
  await expect(
    console.getByRole('button', { name: 'Review song.wav again', exact: true })
  ).toHaveCount(0);
  await expect(
    console.getByRole('button', { name: 'Review song.zip again', exact: true })
  ).toBeVisible();
  expect((await (await request.get(`${ws.base}/skipped`)).json()).skipped).toHaveLength(3);
  const oldBatch = await (await request.get(`${ws.base}/batches/${batchID}?filter=skipped`)).json();
  expect(oldBatch.candidates).toHaveLength(4);
  expect(oldBatch.candidates.every((item: { state: string }) => item.state === 'skipped')).toBe(
    true
  );
  expect(posts.every(url => url.endsWith('/draft'))).toBe(true);
  await console
    .locator('.fj-batch-scan-status')
    .evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/03-kept-and-reconsider.png`, fullPage: true });
  await console.getByRole('button', { name: 'Scan now', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review.batch_id
    )
    .not.toBe(batchID);
  const fresh = (await (await request.get(`${ws.base}/batches/latest`)).json()).batch;
  const freshBody = await (await request.get(`${ws.base}/batches/${fresh.id}`)).json();
  expect(freshBody.candidates.map((item: { name: string }) => item.name)).toEqual(['song.wav']);
  expect(
    (await (await request.get(`${ws.base}/batches/${fresh.id}/draft`)).json()).draft.choices
  ).toEqual([]);
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  const history = await (await request.get(`${ws.base}/history`)).json();
  expect(history.total).toBe(0);
});

test('real exact grouped confirmation moves 49 chosen files through MCP, preserves an exclusion, and supports replay and Undo', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = [
    ...Array.from({ length: 18 }, (_, i) => `installer-${i}.dmg`),
    ...Array.from({ length: 31 }, (_, i) => `image-${i}.png`),
    'leave.png'
  ];
  const ws = await manualReview(request, names, 'Exact grouped execution');
  mkdirSync(join(ws.root, 'Filed', 'Images'), { recursive: true });
  writeFileSync(join(ws.root, 'Filed', 'Images', 'image-0.png'), 'Existing synthetic collision', {
    mode: 0o600
  });
  const console = await openReview(page, ws.slug);
  await console
    .getByRole('checkbox', { name: 'Select filing proposals in Installers', exact: true })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('18 files selected.');
  await console
    .getByRole('checkbox', { name: 'Select filing proposals in Images', exact: true })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('50 files selected.');
  await console.getByRole('button', { name: 'See 32 files in Images', exact: true }).click();
  await console.getByRole('checkbox', { name: 'Select leave.png', exact: true }).uncheck();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  await console.locator('#fileJanitorApprove').click();
  await expect(console.locator('.fj-confirm-lead')).toContainText('move 49 files');
  await expect(console.locator('.fj-confirm-list')).toContainText('image-0 (2).png');
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  await console.locator('.fj-confirm').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('49 files selected.');
  await console.locator('#fileJanitorApprove').click();
  await console.locator('.fj-confirm').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-final-preview.png`, fullPage: true });
  const sent = page.waitForRequest(
    req => req.method() === 'POST' && req.url() === new URL(`${ws.base}/apply`, page.url()).href
  );
  await console.locator('#fileJanitorConfirmApply').click();
  const applyRequest = await sent;
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('49 files filed.', {
    timeout: 60_000
  });
  expect(existsSync(join(ws.root, 'leave.png'))).toBe(true);
  for (const name of names.filter(name => name !== 'leave.png'))
    expect(existsSync(join(ws.root, name))).toBe(false);
  expect(existsSync(join(ws.root, 'Filed', 'Images', 'image-0 (2).png'))).toBe(true);
  const history = await (await request.get(`${ws.base}/history?limit=100`)).json();
  expect(history.total).toBe(49);
  expect(history.actions.every((item: { result: string }) => item.result === 'applied')).toBe(true);
  const replay = await request.post(`${ws.base}/apply`, { data: applyRequest.postDataJSON() });
  expect(replay.ok(), await replay.text()).toBe(true);
  expect((await replay.json()).result).toMatchObject({ replayed: true, applied: 49 });
  await console.locator('.fj-results').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-actual-results.png`, fullPage: true });
  await console.getByRole('tab', { name: 'History', exact: true }).click();
  const first = console.locator('.fj-history-item').first();
  const restoredName = await first.locator('.fj-history-name').textContent();
  await first.getByRole('button', { name: 'Undo move', exact: true }).click();
  await expect(console.locator('#fileJanitorHistoryStatus')).toContainText('Put back', {
    timeout: 30_000
  });
  expect(existsSync(join(ws.root, restoredName!))).toBe(true);
});

test('real filesystem partial failure requires a new unselected retry review, and changed input refuses old confirmation', async ({
  page,
  request
}) => {
  test.skip(process.platform === 'win32', 'POSIX directory-mode failure fixture');
  const ws = await manualReview(request, ['first.dmg', 'second.png'], 'Partial grouped execution');
  const console = await openReview(page, ws.slug);
  await console.getByRole('button', { name: 'Select all filing groups', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
  await console.locator('#fileJanitorApprove').click();
  const destination = join(ws.root, 'Filed', 'Images');
  mkdirSync(destination, { recursive: true });
  chmodSync(destination, 0o500);
  try {
    await console.locator('#fileJanitorConfirmApply').click();
    await expect(console.locator('#fileJanitorResultsSummary')).toContainText('1 file filed', {
      timeout: 60_000
    });
    await expect(console.locator('#fileJanitorResultsSummary')).toContainText(
      '1 could not be moved'
    );
    expect(existsSync(join(ws.root, 'first.dmg'))).toBe(false);
    expect(existsSync(join(ws.root, 'second.png'))).toBe(true);
    await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  } finally {
    chmodSync(destination, 0o750);
  }
  await console.locator('#fileJanitorView-all').click();
  await console.getByRole('button', { name: 'Review retry for second.png', exact: true }).click();
  await expect(
    console.getByRole('checkbox', { name: 'Select second.png', exact: true })
  ).toBeEnabled();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  await console.getByRole('checkbox', { name: 'Select second.png', exact: true }).check();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await console.locator('#fileJanitorApprove').click();
  await expect(console.locator('#fileJanitorConfirmApply')).toBeVisible();
  writeFileSync(join(ws.root, 'second.png'), 'Changed synthetic input after the fresh preview', {
    mode: 0o600
  });
  await console.locator('#fileJanitorConfirmApply').click();
  await expect(console.locator('#fileJanitorError')).not.toHaveText('');
  await expect(console.locator('#fileJanitorConfirmApply')).toHaveCount(0);
  await expect(console.locator('#fileJanitorSelection')).toHaveText('No files selected.');
  expect(existsSync(join(ws.root, 'second.png'))).toBe(true);
  const history = await (await request.get(`${ws.base}/history`)).json();
  expect(history.total).toBe(2);
  await console
    .locator('#fileJanitorError')
    .evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-stale-or-partial.png`, fullPage: true });
});

test('real protection departure returns to the pinned older batch and current canonical choices without replay', async ({
  page,
  request
}) => {
  const ws = await manualReview(request, ['one.png', 'two.png'], 'Pinned protection return');
  const review = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review;
  const batchID = review.batch_id;
  const console = await openReview(page, ws.slug);
  await console.getByRole('button', { name: 'Select all filing groups', exact: true }).click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
  await console.locator('#fileJanitorApprove').click();
  const protection = await (await request.get(`${ws.base}/protection`)).json();
  await console.locator('#fileJanitorCheckProtection').click();
  const returning =
    protection.destination.kind === 'chooser'
      ? page.locator('#protectFoldersCancel')
      : page
          .locator('#backupRecoveryConsole')
          .getByRole('button', { name: 'Return to filing review', exact: true });
  await expect(returning).toBeVisible();
  const saved = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  const exclude = await request.post(`${ws.base}/batches/${batchID}/draft`, {
    data: {
      action: 'exclude_files',
      candidate_ids: [saved.choices[0].candidate_id],
      expected_revision: saved.revision,
      expected_group_revision: saved.group_revision
    }
  });
  expect(exclude.ok(), await exclude.text()).toBe(true);
  writeFileSync(join(ws.root, 'later.png'), 'Synthetic later arrival', { mode: 0o600 });
  utimesSync(join(ws.root, 'later.png'), old, old);
  const scan = await request.post(`${ws.base}/scan`);
  expect(scan.ok(), await scan.text()).toBe(true);
  const latest = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review
    .batch_id;
  expect(latest).not.toBe(batchID);
  await returning.click();
  await expect(console).toBeVisible();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await expect(console.locator('#fileJanitorBatchChoice')).toHaveValue(batchID);
  await expect(console.locator('#fileJanitorConfirmApply')).toHaveCount(0);
  await expect(console.locator('.fj-batch-scan-status')).toContainText(
    'No old selections or approval were replayed'
  );
  await console.locator('#fileJanitorApprove').click();
  await expect(console.locator('.fj-confirm-lead')).toContainText('move 1 file');
  for (const name of ['one.png', 'two.png', 'later.png'])
    expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('explicit setup of an adopted alias keeps saved group scope without probing or widening reference paths', async ({
  page,
  request
}) => {
  const ws = await manualReview(request, ['alias.png'], 'Approved alias reference', true);
  const status = (await (await request.get(ws.base)).json()).status;
  expect(status.settings.root_path).toBe(ws.root);
  expect(status.settings.approved_reference.path).toBe(ws.referencePath);
  const response = await request.get(`${ws.base}/batches/latest/groups`);
  expect(response.ok(), await response.text()).toBe(true);
  const review = (await response.json()).review;
  expect(review.counts).toMatchObject({ candidates: 1, filing: 1 });
  const console = await openReview(page, ws.slug);
  await console
    .getByRole('checkbox', { name: 'Select filing proposals in Images', exact: true })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  await page.reload();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1 file selected.');
  expect(existsSync(join(ws.root, 'alias.png'))).toBe(true);
});

test('1000 saved candidates keep bounded cards, paged DOM and one exact selection request', async ({
  page,
  request
}) => {
  test.setTimeout(90_000);
  const ws = await manualReview(
    request,
    Array.from({ length: 1000 }, (_, i) => `scale-${String(i).padStart(4, '0')}.png`),
    'Bounded 1000 review'
  );
  const raw = await (await request.get(`${ws.base}/batches/latest/groups`)).text();
  expect(Buffer.byteLength(raw)).toBeLessThan(4000);
  const review = JSON.parse(raw).review;
  expect(review.counts).toMatchObject({ candidates: 1000, filing: 1000, questions: 0 });
  expect(review.groups).toHaveLength(1);
  expect(review.groups[0].samples).toHaveLength(3);
  const console = await openReview(page, ws.slug);
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  const started = Date.now();
  await console
    .getByRole('checkbox', { name: 'Select filing proposals in Images', exact: true })
    .click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('1000 files selected.', {
    timeout: 20_000
  });
  const elapsed = Date.now() - started;
  expect(posts.filter(url => url.endsWith('/draft'))).toHaveLength(1);
  expect(posts).toHaveLength(1);
  expect(await console.locator('.fj-row-item').count()).toBe(0);
  await console.getByRole('button', { name: 'See 1,000 files in Images', exact: true }).click();
  await expect(console.locator('.fj-row-item')).toHaveCount(50);
  const draftText = await (await request.get(`${ws.base}/batches/${review.batch_id}/draft`)).text();
  expect(Buffer.byteLength(draftText)).toBeLessThan(256 * 1024);
  expect(JSON.parse(draftText).draft.choices).toHaveLength(1000);
  await console.locator('#fileJanitorApprove').click();
  await expect(console.locator('.fj-confirm-item')).toHaveCount(50);
  await expect(console.locator('.fj-confirm')).toContainText('of 1000 exact selected operations');
  globalThis.console.info(
    `Grouped review scale: 1000 candidates, card JSON ${Buffer.byteLength(raw)} bytes, draft JSON ${Buffer.byteLength(draftText)} bytes, select-and-save ${elapsed}ms, 1 POST, 50 member/preview rows (local Chromium sandbox).`
  );
});

for (const theme of ['light', 'dark']) {
  test(`grouped review keyboard, partial selection, 390px, zoom and reduced motion (${theme})`, async ({
    page,
    request
  }) => {
    test.setTimeout(90_000);
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 390, height: 844 });
    const ws = await manualReview(
      request,
      ['first.png', 'second.png', 'third.png', 'tax-record.pdf'],
      `Keyboard ${theme}`
    );
    const console = await openReview(page, ws.slug);
    const tabTo = async (target: ReturnType<Page['locator']>, reverse = false) => {
      for (let i = 0; i < 350; i++) {
        if (await target.evaluate(node => node === document.activeElement).catch(() => false))
          return;
        await page.keyboard.press(reverse ? 'Shift+Tab' : 'Tab');
      }
      throw new Error('Keyboard target unreachable');
    };
    const select = console.getByRole('checkbox', {
      name: 'Select filing proposals in Images',
      exact: true
    });
    await tabTo(select);
    await page.keyboard.press('Space');
    await expect(console.locator('#fileJanitorSelection')).toHaveText('3 files selected.');
    const details = console.getByRole('button', { name: 'See 3 files in Images', exact: true });
    await tabTo(details);
    await page.keyboard.press('Enter');
    await expect(console.locator('.fj-row-item')).toHaveCount(3);
    const file = console.getByRole('checkbox', { name: 'Select first.png', exact: true });
    await tabTo(file);
    await page.keyboard.press('Space');
    await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
    const close = console.getByRole('button', { name: 'Close file list', exact: true });
    await tabTo(close, true);
    await page.keyboard.press('Enter');
    await expect(select).toHaveJSProperty('indeterminate', true);
    await tabTo(console.locator('#fileJanitorApprove'));
    await page.keyboard.press('Enter');
    await expect(console.locator('#fileJanitorConfirmApply')).toBeFocused();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Enter');
    await expect(console.locator('#fileJanitorConfirmApply')).toHaveCount(0);
    await expect(console.locator('#fileJanitorSelection')).toHaveText('2 files selected.');
    await expect(console.locator('#fileJanitorApprove')).toBeFocused();
    expect(await console.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    await console
      .locator('.fj-group-grid')
      .evaluate(node => node.scrollIntoView({ block: 'start' }));
    await page.screenshot({ path: `${evidence}/05-${theme}-390.png`, fullPage: true });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '200%';
    });
    const bounds = await console.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(1441);
    expect(await console.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    await page.screenshot({ path: `${evidence}/05-${theme}-zoom.png`, fullPage: true });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '';
    });
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const violations = await page.evaluate(
      async () =>
        (
          await (
            window as unknown as {
              axe: { run: (scope: string, options: object) => Promise<{ violations: unknown[] }> };
            }
          ).axe.run('#fileJanitorConsole', { runOnly: ['wcag2a', 'wcag2aa', 'wcag21aa'] })
        ).violations
    );
    expect(violations).toEqual([]);
  });
}

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
