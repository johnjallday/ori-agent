import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import {
  mkdtempSync,
  writeFileSync,
  utimesSync,
  existsSync,
  mkdirSync,
  realpathSync,
  chmodSync,
  symlinkSync,
  readdirSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';

// Real APIs and saved state, credential-free. No picker/provider/group endpoint
// is mocked. Only unrelated first-run onboarding is suppressed. Every managed
// folder is a new synthetic OS-temp directory, never the user's Downloads.
//
// The Review tab is a picture of the folder as it will look after filing: the
// confident folders are ticked when it opens, the question groups never are, and
// opening or ticking moves nothing. These journeys drive that picture against the
// real server. One main button files exactly the plan the picture shows (a
// server-side plan check backs it), with no separate confirm screen.
const evidence = 'tasks/evidence-filing-picture-review';
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
    await console.getByRole('button', { name: 'Close Filing', exact: true }).click();
    await nudge.getByRole('button', { name: 'Not now', exact: true }).click();
    await expect(nudge).toBeHidden();
    await page.goto(url);
    await expect(console).toBeVisible();
  }
  return console;
}

// The tick box of one destination folder in the picture.
const folder = (console: ReturnType<Page['locator']>, category: string) =>
  console.locator(`#fileJanitorFolder-${category}`);

// The one main button. Its label states exactly what pressing it will do.
const fileButton = (console: ReturnType<Page['locator']>) => console.locator('#fileJanitorApprove');

// The label for N files to move (and M to Trash), as the button words it.
const filesLabel = (moves: number, trash = 0) => {
  const files = (count: number) => `${count} ${count === 1 ? 'file' : 'files'}`;
  if (moves && trash) return `File ${files(moves)} · ${trash} to Trash`;
  if (moves) return `File ${files(moves)}`;
  if (trash) return `Move ${files(trash)} to Trash`;
  return 'Nothing to file';
};

// The destination folders that exist under the Filed root. Setting the review up
// makes the empty Filed root; a destination (Filed/Images) is made only by filing.
const destinations = (root: string) =>
  existsSync(join(root, 'Filed')) ? readdirSync(join(root, 'Filed')) : [];

// The POSTs that file something: the one File request and nothing older.
const isFileRequest = (url: string) => /\/batches\/[^/]+\/file$/.test(new URL(url).pathname);

// What the server says is waiting and where each file is drawn.
async function pictureOf(request: APIRequestContext, base: string, batch = 'latest') {
  const response = await request.get(`${base}/batches/${batch}/picture`);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).picture;
}

const drawn = (picture: {
  folders: { count: number; unfiled_count: number }[];
  loose: { loose_count: number }[];
  left_out: { count: number };
  trash: { count: number };
}) =>
  picture.folders.reduce((sum, item) => sum + item.count + item.unfiled_count, 0) +
  picture.loose.reduce((sum, row) => sum + row.loose_count, 0) +
  picture.left_out.count +
  picture.trash.count;

test('agentless real entry draws a 206-file folder ticked, unticks one folder, keeps it across reload, and moves nothing', async ({
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

  // The lead line, then the frame: nothing else comes before the first folder.
  // The folder is named by its own folder name, never a path.
  const folderName = basename(ws.root);
  await expect(console.locator('#fileJanitorLead')).toHaveText(
    `This is how ${folderName} will look. Nothing moves until you press File. Ori reads names, types, sizes and dates, never what is inside.`
  );
  await expect(console.locator('#fileJanitorFrameTitle')).toHaveText(`${folderName}, after filing`);
  await expect(folder(console, 'images')).toBeChecked();
  await expect(folder(console, 'images')).toHaveAccessibleName('Images, 206 files, will be filed');
  await expect(console.locator('.fj-folder-card .fj-folder-files li')).toHaveCount(5);
  await expect(console.locator('#fileJanitorMore-filed-images')).toHaveText('and 201 more');
  await expect(console.locator('.fj-row-item')).toHaveCount(0);
  // Ticked on open: the one button is ready to file the folder as it is drawn.
  await expect(fileButton(console)).toHaveText(filesLabel(206));
  await expect(fileButton(console)).toBeEnabled();
  // The old plan sidebar, select-all, counting line and Not now are gone.
  for (const gone of ['Your selected changes', 'Select all filing groups', 'candidates in this']) {
    await expect(console).not.toContainText(gone);
  }
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/01-picture-on-open.png`, fullPage: true });

  // "and N more" opens the whole folder, 50 at a time, and gives focus back.
  await console.locator('#fileJanitorMore-filed-images').click();
  const more = console.locator('#fileJanitorMorePanel');
  await expect(more).toContainText('Showing 1–50 of 206');
  await expect(more.locator('li')).toHaveCount(50);
  for (const range of ['51–100', '101–150', '151–200', '201–206']) {
    await more.getByRole('button', { name: 'Next files', exact: true }).click();
    await expect(more).toContainText(`Showing ${range} of 206`);
  }
  await expect(more.locator('li')).toHaveCount(6);
  await expect(more.getByRole('button', { name: 'Next files', exact: true })).toBeDisabled();
  await page.screenshot({ path: `${evidence}/01-untick-and-more.png`, fullPage: true });
  await more.getByRole('button', { name: 'Close list', exact: true }).click();
  await expect(console.locator('#fileJanitorMore-filed-images')).toBeFocused();

  // Opening, reading and paging wrote nothing, and made no folder.
  expect(mutations).toEqual([]);
  expect(existsSync(join(ws.root, 'Filed', 'Images'))).toBe(false);
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);

  // Unticking is one saved edit; the card stays, dashed, and says Not filed.
  await folder(console, 'images').uncheck();
  await expect(fileButton(console)).toHaveText('Nothing to file');
  await expect(console.locator('.fj-folder-card.is-unticked')).toContainText('Not filed');
  await expect(folder(console, 'images')).toHaveAccessibleName('Images, 206 files, not filed');
  await expect(fileButton(console)).toBeDisabled();
  expect(mutations.filter(url => url.endsWith('/draft'))).toHaveLength(1);
  const saved = (await (await request.get(`${ws.base}/batches/latest/groups`)).json()).review;
  const draft = (await (await request.get(`${ws.base}/batches/${saved.batch_id}/draft`)).json())
    .draft;
  expect(draft.state).toBe('saved');
  expect(draft.choices).toEqual([]);

  // The saved state survives a reload, and the folder is not ticked again.
  await page.reload();
  await expect(folder(console, 'images')).not.toBeChecked();
  await expect(console.locator('.fj-folder-card.is-unticked')).toContainText('Not filed');
  await expect(fileButton(console)).toHaveText('Nothing to file');
  await page.screenshot({ path: `${evidence}/01-unticked-after-reload.png`, fullPage: true });
  await folder(console, 'images').check();
  await expect(fileButton(console)).toHaveText(filesLabel(206));

  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(existsSync(join(ws.root, 'Filed', 'Images'))).toBe(false);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('206 mixed files are each drawn once, question groups are never ticked, and deep links still reach All files', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
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
  const picture = await pictureOf(request, ws.base);
  expect(picture.counts).toMatchObject({ candidates: 206, filing: 149, questions: 57 });
  expect(picture.waiting).toBe(206);
  // Every waiting file is drawn exactly once: the sections add up to the scan.
  expect(drawn(picture)).toBe(206);
  expect(picture.draft_state).toBe('default');
  expect(picture.folders.map((item: { category: string }) => item.category)).toEqual([
    'documents',
    'images',
    'installers'
  ]);
  expect(picture.loose.map((row: { key: string }) => row.key).sort()).toEqual([
    'audio',
    'sensitive',
    'unresolved',
    'versions'
  ]);
  const draft = (await (await request.get(`${ws.base}/batches/${picture.batch_id}/draft`)).json())
    .draft;
  expect(draft.choices).toHaveLength(149);
  expect(
    draft.choices.every((choice: { source_group_id: string }) => choice.source_group_id !== '')
  ).toBe(true);

  const console = await openReview(page, ws.slug);
  await expect(console.locator('.fj-folder-card')).toHaveCount(3);
  await expect(console.locator('.fj-folder-card .fj-folder-name')).toHaveText([
    'Documents',
    'Images',
    'Installers'
  ]);
  await expect(console.locator('.fj-loose-label')).toHaveText([
    'Sensitive-looking names · 7',
    'Possible copies · 10',
    'Music and audio · 20',
    'Unknown type · 20'
  ]);
  // No tick box and no menu for a question group; the question files stay loose.
  await expect(console.locator('.fj-loose-row input, .fj-loose-row select')).toHaveCount(0);
  await expect(fileButton(console)).toHaveText(filesLabel(149));
  await expect(console.getByRole('heading', { name: /^Still loose in .* · 57$/ })).toBeVisible();
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/01-mixed-picture.png`, fullPage: true });

  const detail = await (
    await request.get(`${ws.base}/batches/${picture.batch_id}?limit=1&offset=205`)
  ).json();
  await openReview(page, ws.slug, `&batch_id=${picture.batch_id}&item=${detail.candidates[0].id}`);
  await expect(console.locator('#fileJanitorView-all')).toHaveAttribute('aria-pressed', 'true');
  await expect(console.locator('#fileJanitorLinkedRow')).toBeVisible();
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
});

test('two folders open ticked and stay pinned when a newer review arrives', async ({
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
  await expect(folder(console, 'installers')).toBeChecked();
  await expect(folder(console, 'images')).toBeChecked();
  await expect(fileButton(console)).toHaveText(filesLabel(49));
  await expect(fileButton(console)).toBeEnabled();
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const draft = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  expect(draft.choices).toHaveLength(49);
  expect(draft.choices.every((choice: { operation: string }) => choice.operation === 'move')).toBe(
    true
  );
  writeFileSync(join(ws.root, 'later-arrival.png'), 'Synthetic later arrival', { mode: 0o600 });
  utimesSync(join(ws.root, 'later-arrival.png'), old, old);
  await console.getByRole('button', { name: 'Scan now', exact: true }).click();
  await expect(console).toContainText('A newer saved review is available');
  // The open picture is never swapped by itself, and the later arrival is not in it.
  await expect(fileButton(console)).toHaveText(filesLabel(49));
  await expect(page).toHaveURL(new RegExp(`batch_id=${batchID}`));
  await page.reload();
  await expect(fileButton(console)).toHaveText(filesLabel(49));
  const newest = (await pictureOf(request, ws.base)).batch_id;
  expect(newest).not.toBe(batchID);
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await expect(console.locator('#fileJanitorBatchChoice')).toHaveValue(batchID);
  await console.locator('#fileJanitorBatchChoice').selectOption(newest);
  await console.locator('#fileJanitorView-groups').click();
  // The newer scan is its own picture, ticked on open with its one new file.
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(console.locator('.fj-folder-card .fj-folder-count')).toHaveText('1');
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.locator('#fileJanitorBatchChoice').selectOption(batchID);
  await console.locator('#fileJanitorView-groups').click();
  await expect(fileButton(console)).toHaveText(filesLabel(49));
  for (const name of [...names, 'later-arrival.png'])
    expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('a file left out of a ticked folder is kept across reload and shown as left out', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = Array.from({ length: 206 }, (_, i) => `image-${String(i).padStart(4, '0')}.png`);
  const ws = await manualReview(request, names, 'Group review durable choices');
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const members = await (await request.get(`${ws.base}/batches/${batchID}?limit=200`)).json();
  const target = members.candidates.find(
    (candidate: { name: string }) => candidate.name === 'image-0051.png'
  );
  // Deep-link to a file that is not on the first page of All files and leave it out.
  const console = await openReview(page, ws.slug, `&batch_id=${batchID}&item=${target.id}`);
  await expect(console.locator('#fileJanitorLinkedRow')).toBeVisible();
  await console.getByRole('checkbox', { name: 'Select image-0051.png', exact: true }).uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(205));
  await console.locator('#fileJanitorView-groups').click();
  await expect(folder(console, 'images')).toHaveJSProperty('indeterminate', true);
  await expect(folder(console, 'images')).toBeChecked();
  await expect(console.locator('.fj-loose-label')).toHaveText(['Left out by you · 1']);
  await expect(console.locator('.fj-loose-files li')).toHaveText(['image-0051.png']);
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/02-left-out.png`, fullPage: true });
  const picture = await pictureOf(request, ws.base, batchID);
  expect(picture.left_out.count).toBe(1);
  expect(picture.folders[0]).toMatchObject({ group_state: 'partial', count: 205 });
  expect(drawn(picture)).toBe(206);
  const draft = (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft;
  expect(draft.choices).toHaveLength(205);
  expect(
    draft.choices.every((choice: { candidate_id: string }) => choice.candidate_id !== target.id)
  ).toBe(true);
  // Reopen Review plainly (the deep link above opens All files by design).
  await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(205));
  await expect(folder(console, 'images')).toHaveJSProperty('indeterminate', true);
  await expect(console.locator('.fj-loose-label')).toHaveText(['Left out by you · 1']);
  expect(
    (await (await request.get(`${ws.base}/batches/${batchID}/draft`)).json()).draft.choices
  ).toEqual(draft.choices);
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('injected save failure is unsaved and two real windows resolve a stale press and a conflict by reloading', async ({
  page,
  request,
  context
}) => {
  const ws = await manualReview(request, ['one.png', 'two.png']);
  const console = await openReview(page, ws.slug);
  const other = await context.newPage();
  const second = await openReview(other, ws.slug);
  await expect(folder(console, 'images')).toBeEnabled();
  await expect(folder(second, 'images')).toBeEnabled();
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(second)).toHaveText(filesLabel(2));
  await expect(fileButton(second)).toBeEnabled();

  // The first window's untick is lost on the wire: it says so, blocks the button
  // ("Not saved. Retry"), and the same request can be retried.
  let fail = true;
  await page.route('**/file-janitor/batches/*/draft', route => {
    if (route.request().method() === 'POST' && fail) {
      fail = false;
      return route.abort('failed');
    }
    return route.continue();
  });
  // A click, not uncheck(): the unconfirmed edit leaves the box as it was.
  await folder(console, 'images').click();
  await expect(console.locator('#fileJanitorDraftStatus')).toContainText('not confirmed saved');
  await expect(folder(console, 'images')).toBeDisabled();
  await expect(fileButton(console)).toBeDisabled();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('Not saved. Retry');
  await console.getByRole('button', { name: 'Retry exact edit', exact: true }).click();
  await expect(fileButton(console)).toHaveText(filesLabel(0));

  // The second window still shows both ticked. Its button is stale, so the press
  // is refused (nothing is filed) and the window reads the saved choices again
  // instead of filing what is no longer chosen.
  const stalePress = other.waitForResponse(
    res => res.request().method() === 'POST' && isFileRequest(res.url())
  );
  await fileButton(second).click();
  const refusal = await stalePress;
  expect(refusal.status()).toBe(409);
  expect((await refusal.json()).code).toBe('plan_changed');
  await expect(second.locator('#fileJanitorSelection')).toContainText('Nothing was filed.');
  await other.screenshot({ path: `${evidence}/02-save-conflict.png`, fullPage: true });
  await expect(fileButton(second)).toHaveText(filesLabel(0));
  await expect(folder(second, 'images')).not.toBeChecked();

  // The second window ticks again; the first window's picture is now behind, and
  // its own edit is refused as a conflict rather than overwriting.
  await folder(second, 'images').check();
  await expect(fileButton(second)).toHaveText(filesLabel(2));
  await folder(console, 'images').click();
  await expect(console.locator('#fileJanitorDraftStatus')).toContainText('Review changed');
  await expect(fileButton(console)).toBeDisabled();
  await console.getByRole('button', { name: 'Reload saved choices', exact: true }).click();
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(folder(console, 'images')).toBeChecked();
  expect(existsSync(join(ws.root, 'one.png'))).toBe(true);
  expect(existsSync(join(ws.root, 'two.png'))).toBe(true);
  await other.close();
});

test('question groups stay loose and unticked, are described truthfully, and name files as text', async ({
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
  const console = await openReview(page, ws.slug);
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  // Only the one confident file is ticked; every question file stays loose.
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(console.locator('.fj-loose-label')).toHaveText([
    'Sensitive-looking names · 1',
    'Possible copies · 2',
    'Music and audio · 4',
    'Unknown type · 1'
  ]);
  await expect(console.locator('.fj-loose-row input')).toHaveCount(0);
  await expect(console.locator('.fj-loose')).toContainText(hostile);
  expect(await console.locator('.fj-loose img').count()).toBe(0);
  // "Why?" says what Ori actually checked, and no more.
  const sensitive = console.locator('.fj-loose-row').filter({ hasText: 'Sensitive-looking names' });
  await sensitive.locator('summary').click();
  await expect(sensitive.locator('.fj-why-text')).toHaveText(
    'A name has a word like passport or tax in it. Ori has not looked inside the files.'
  );
  const copies = console.locator('.fj-loose-row').filter({ hasText: 'Possible copies' });
  await copies.locator('summary').click();
  await expect(copies.locator('.fj-why-text')).toContainText(
    'Ori has not compared what is inside.'
  );
  mkdirSync(evidence, { recursive: true });
  await console.locator('.fj-loose').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/03-focused-questions.png`, fullPage: true });

  // A per-file Trash mark (made in All files) appears in the Trash box and leaves
  // its row; nothing goes to the Trash until its own box is ticked and File pressed.
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.getByRole('button', { name: 'Mark song.rpp for Trash', exact: true }).click();
  await expect(fileButton(console)).toHaveText(filesLabel(1, 1));
  await expect(fileButton(console)).toBeDisabled();
  await console.locator('#fileJanitorView-groups').click();
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  await expect(console.locator('.fj-trash')).toContainText('song.rpp');
  await expect(console.locator('.fj-loose-label').nth(2)).toHaveText('Music and audio · 4');
  const picture = await pictureOf(request, ws.base);
  expect(drawn(picture)).toBe(9);
  expect(picture.trash.count).toBe(1);
  expect(picture.loose.find((row: { key: string }) => row.key === 'audio')).toMatchObject({
    count: 4,
    trash_count: 1,
    loose_count: 3
  });
  expect(posts.every(url => url.endsWith('/draft'))).toBe(true);
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('one press files 49 chosen files through MCP, preserves an exclusion, and supports replay and Undo', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = [
    ...Array.from({ length: 18 }, (_, i) => `installer-${i}.dmg`),
    ...Array.from({ length: 31 }, (_, i) => `image-${i}.png`),
    'a-leave.png'
  ];
  const ws = await manualReview(request, names, 'Exact grouped execution');
  mkdirSync(join(ws.root, 'Filed', 'Images'), { recursive: true });
  writeFileSync(join(ws.root, 'Filed', 'Images', 'image-0.png'), 'Existing synthetic collision', {
    mode: 0o600
  });
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  // Both folders open ticked: 18 installers and 32 images.
  await expect(fileButton(console)).toHaveText(filesLabel(50));
  // Leave one image out of its ticked folder, from All files.
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.getByRole('checkbox', { name: 'Select a-leave.png', exact: true }).uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(49));
  // The picture draws the file that will be renamed under its new name, before
  // anything moves.
  await console.locator('#fileJanitorView-groups').click();
  await expect(console.locator('#fileJanitorPicture')).toContainText('image-0 (2).png');
  await expect(
    console.locator('#fileJanitorPicture .fj-tag').filter({ hasText: 'renamed' })
  ).toBeVisible();
  for (const name of names) expect(existsSync(join(ws.root, name))).toBe(true);
  await console
    .locator('#fileJanitorPicture')
    .evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-before-press.png`, fullPage: true });
  // One press, one request: there is no preview and no confirm screen.
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  const sent = page.waitForRequest(
    req =>
      req.method() === 'POST' &&
      req.url() === new URL(`${ws.base}/batches/${batchID}/file`, page.url()).href
  );
  await expect(fileButton(console)).toBeEnabled();
  await fileButton(console).click();
  const fileRequest = await sent;
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('49 files filed.', {
    timeout: 60_000
  });
  expect(posts.filter(url => /\/(preview|apply)$/.test(url))).toEqual([]);
  expect(posts.filter(isFileRequest)).toHaveLength(1);
  expect(existsSync(join(ws.root, 'a-leave.png'))).toBe(true);
  for (const name of names.filter(name => name !== 'a-leave.png'))
    expect(existsSync(join(ws.root, name))).toBe(false);
  expect(existsSync(join(ws.root, 'Filed', 'Images', 'image-0 (2).png'))).toBe(true);
  const history = await (await request.get(`${ws.base}/history?limit=100`)).json();
  expect(history.total).toBe(49);
  expect(history.actions.every((item: { result: string }) => item.result === 'applied')).toBe(true);
  // The same request again (a lost answer, a retry) reports the first result and
  // files nothing more.
  const replay = await request.post(`${ws.base}/batches/${batchID}/file`, {
    data: fileRequest.postDataJSON()
  });
  expect(replay.ok(), await replay.text()).toBe(true);
  expect((await replay.json()).filing).toMatchObject({ replayed: true, filed: 49 });
  expect((await (await request.get(`${ws.base}/history?limit=100`)).json()).total).toBe(49);
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

test('real filesystem partial failure requires a new unselected retry review, and a changed file refuses the press', async ({
  page,
  request
}) => {
  test.skip(process.platform === 'win32', 'POSIX directory-mode failure fixture');
  const ws = await manualReview(request, ['first.dmg', 'second.png'], 'Partial grouped execution');
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();
  const destination = join(ws.root, 'Filed', 'Images');
  mkdirSync(destination, { recursive: true });
  chmodSync(destination, 0o500);
  try {
    await fileButton(console).click();
    await expect(console.locator('#fileJanitorResultsSummary')).toContainText('1 file filed', {
      timeout: 60_000
    });
    await expect(console.locator('#fileJanitorResultsSummary')).toContainText(
      '1 could not be moved'
    );
    expect(existsSync(join(ws.root, 'first.dmg'))).toBe(false);
    expect(existsSync(join(ws.root, 'second.png'))).toBe(true);
    await expect(fileButton(console)).toHaveText(filesLabel(0));
  } finally {
    chmodSync(destination, 0o750);
  }
  // The file that could not be filed is drawn as such, and is not ticked again.
  await expect(console.locator('.fj-could-not')).toContainText('second.png');
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.getByRole('button', { name: 'Review retry for second.png', exact: true }).click();
  await expect(
    console.getByRole('checkbox', { name: 'Select second.png', exact: true })
  ).toBeEnabled();
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  await console.getByRole('checkbox', { name: 'Select second.png', exact: true }).check();
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(fileButton(console)).toBeEnabled();
  // The file changes after the person looked: pressing files nothing and says why.
  writeFileSync(join(ws.root, 'second.png'), 'Changed synthetic input after the plan check', {
    mode: 0o600
  });
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorSelection')).toContainText(
    'The folder changed since you looked. Nothing was filed.'
  );
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  expect(existsSync(join(ws.root, 'second.png'))).toBe(true);
  const history = await (await request.get(`${ws.base}/history`)).json();
  expect(history.total).toBe(2);
  await console
    .locator('#fileJanitorSelection')
    .evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-stale-or-partial.png`, fullPage: true });
});

test('real protection departure returns to the pinned older batch and current canonical choices without replay', async ({
  page,
  request
}) => {
  const ws = await manualReview(request, ['one.png', 'two.png'], 'Pinned protection return');
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();
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
  const latest = (await pictureOf(request, ws.base)).batch_id;
  expect(latest).not.toBe(batchID);
  await returning.click();
  await expect(console).toBeVisible();
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(console.locator('#fileJanitorResultsHost .fj-results')).toHaveCount(0);
  await expect(console.locator('.fj-batch-scan-status')).toContainText(
    'No old selections or approval were replayed'
  );
  // The pinned older review, not the newest one, is what was restored.
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await expect(console.locator('#fileJanitorBatchChoice')).toHaveValue(batchID);
  await console.locator('#fileJanitorView-groups').click();
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(fileButton(console)).toBeEnabled();
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
  await expect(folder(console, 'images')).toBeChecked();
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  // The picture names no path, and the alias spelling is not widened.
  await expect(console).not.toContainText(ws.referencePath);
  await folder(console, 'images').uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  await page.reload();
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  expect(existsSync(join(ws.root, 'alias.png'))).toBe(true);
});

test('1000 saved files keep a bounded picture, paged lists and a plan for the whole 1000', async ({
  page,
  request
}) => {
  test.setTimeout(90_000);
  const ws = await manualReview(
    request,
    Array.from({ length: 1000 }, (_, i) => `scale-${String(i).padStart(4, '0')}.png`),
    'Bounded 1000 review'
  );
  const raw = await (await request.get(`${ws.base}/batches/latest/picture`)).text();
  // The first screenful is one bounded read however many files wait.
  expect(Buffer.byteLength(raw)).toBeLessThan(30_000);
  const picture = JSON.parse(raw).picture;
  expect(picture.counts).toMatchObject({ candidates: 1000, filing: 1000, questions: 0 });
  expect(picture.folders).toHaveLength(1);
  expect(picture.folders[0].files).toHaveLength(5);
  expect(drawn(picture)).toBe(1000);
  const console = await openReview(page, ws.slug);
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  // 1,000 is the most one press files: the button states it and is enabled.
  await expect(fileButton(console)).toHaveText(filesLabel(1000), { timeout: 20_000 });
  await expect(fileButton(console)).toBeEnabled();
  expect(posts).toHaveLength(0);
  expect(await console.locator('.fj-row-item').count()).toBe(0);
  await console.locator('#fileJanitorMore-filed-images').click();
  await expect(console.locator('#fileJanitorMorePanel li')).toHaveCount(50);
  await expect(console.locator('#fileJanitorMorePanel')).toContainText('Showing 1–50 of 1000');
  const draftText = await (
    await request.get(`${ws.base}/batches/${picture.batch_id}/draft`)
  ).text();
  expect(Buffer.byteLength(draftText)).toBeLessThan(256 * 1024);
  const draft = JSON.parse(draftText).draft;
  expect(draft.choices).toHaveLength(1000);
  // The plan check covers all 1000 (counts and digest only; the picture asked for
  // the names it shows), without opening a file or creating a folder.
  const planResponse = await request.get(
    `${ws.base}/batches/${picture.batch_id}/plan?revision=${draft.revision}&group_revision=${draft.group_revision}`
  );
  expect(planResponse.ok(), await planResponse.text()).toBe(true);
  const plan = (await planResponse.json()).plan;
  expect(plan).toMatchObject({ move_count: 1000, trash_count: 0, over_limit: false });
  expect(plan.plan_digest).toMatch(/^[0-9a-f]{64}$/);
  expect(destinations(ws.root)).toEqual([]);
  globalThis.console.info(
    `Picture scale: 1000 files, first screenful ${Buffer.byteLength(raw)} bytes, draft ${Buffer.byteLength(draftText)} bytes, 0 writes to open, 50 list rows, plan of 1000 (local Chromium sandbox).`
  );
});

test('more files than one press can file are drawn, the button says what to do, and unticking brings it back', async ({
  page,
  request
}) => {
  test.setTimeout(120_000);
  const message = 'Ori files up to 1,000 files at a time. Untick a folder to continue.';
  const ws = await manualReview(
    request,
    Array.from({ length: 1001 }, (_, i) => `over-${String(i).padStart(4, '0')}.png`),
    'Over the limit'
  );
  const console = await openReview(page, ws.slug);
  // The scan opens with all 1,001 ticked; it is drawn, not "unavailable".
  await expect(folder(console, 'images')).toBeChecked({ timeout: 30_000 });
  await expect(fileButton(console)).toHaveText(message, { timeout: 30_000 });
  await expect(fileButton(console)).toBeDisabled();
  await expect(console).not.toContainText('Saved choices are unavailable');
  // Unticking the folder leaves nothing to file: that edit is accepted.
  await folder(console, 'images').uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  // Ticking it again would be 1,001 again: refused, said in the same words, and not
  // shown as a failed save.
  await folder(console, 'images').click();
  await expect(console.locator('#fileJanitorSelection')).toHaveText(message);
  await expect(folder(console, 'images')).not.toBeChecked();
  await expect(console).not.toContainText('Not saved');
  await expect(folder(console, 'images')).toBeEnabled();
  expect(destinations(ws.root)).toEqual([]);
});

for (const theme of ['light', 'dark']) {
  test(`picture keyboard, partial selection, 390px, zoom and reduced motion (${theme})`, async ({
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
    const tick = folder(console, 'images');
    await expect(tick).toBeChecked();
    await expect(fileButton(console)).toHaveText(filesLabel(3));
    // The folder card, one per row at 390px, is reachable and operable by keyboard.
    await tabTo(tick);
    await page.keyboard.press('Space');
    await expect(fileButton(console)).toHaveText(filesLabel(0));
    await expect(console.locator('.fj-folder-card.is-unticked')).toContainText('Not filed');
    await tabTo(tick);
    await page.keyboard.press('Space');
    await expect(fileButton(console)).toHaveText(filesLabel(3));
    // Leave one file out (All files), then come back: the folder is partly ticked.
    await tabTo(console.getByRole('button', { name: 'All files', exact: true }));
    await page.keyboard.press('Enter');
    const file = console.getByRole('checkbox', { name: 'Select first.png', exact: true });
    await tabTo(file);
    await page.keyboard.press('Space');
    await expect(fileButton(console)).toHaveText(filesLabel(2));
    await console.locator('#fileJanitorView-groups').click();
    await expect(tick).toHaveJSProperty('indeterminate', true);
    // The one main button is reachable by keyboard and states what Enter will do.
    await expect(fileButton(console)).toBeEnabled();
    await tabTo(fileButton(console));
    await expect(fileButton(console)).toBeFocused();
    await expect(fileButton(console)).toHaveText(filesLabel(2));
    expect(await console.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    await console.locator('.fj-frame').evaluate(node => node.scrollIntoView({ block: 'start' }));
    mkdirSync(evidence, { recursive: true });
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
    // Enter on the focused button files what its label says, and focus lands on the
    // receipt rather than being lost behind the button that no longer exists.
    await tabTo(fileButton(console));
    await page.keyboard.press('Enter');
    await expect(console.locator('.fj-results')).toBeFocused({ timeout: 30_000 });
    expect(existsSync(join(ws.root, 'first.png'))).toBe(true);
    expect(existsSync(join(ws.root, 'second.png'))).toBe(false);
    expect(existsSync(join(ws.root, 'tax-record.pdf'))).toBe(true);
  });
}

test('installed setup-required entry is not an empty saved review', async ({ page, request }) => {
  const ws = await workspace(request);
  const console = await openReview(page, ws.slug);
  await expect(console.locator('.fj-folder-card')).toHaveCount(0);
  await expect(console.locator('#fileJanitorPicture')).toHaveCount(0);
  await expect(console).not.toContainText('Nothing is set to be filed');
  await expect(console).toContainText(/folder/i);
  const response = await request.get(`/api/workspaces/${ws.id}/file-janitor/batches/latest/groups`);
  expect(response.status()).toBe(409);
  expect((await response.json()).code).toBe('review_unavailable');
  const picture = await request.get(`/api/workspaces/${ws.id}/file-janitor/batches/latest/picture`);
  expect(picture.status()).toBe(409);
  expect((await picture.json()).code).toBe('review_unavailable');
});

// ------------------------------------------------------------- the one press
//
// One main button files exactly the plan the picture shows, or nothing. These four
// journeys drive that against the real server and real files.

test('one press files exactly the ticked files and nothing else', async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(
    request,
    ['a.png', 'b.png', 'c.png', 'd.png', 'x.pdf', 'y.pdf', 'song.wav'],
    'One press exact'
  );
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  // Both confident folders open ticked; the music question never does.
  await expect(fileButton(console)).toHaveText(filesLabel(6));
  // Untick the documents folder, and leave one image out of its ticked folder.
  await folder(console, 'documents').uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(4));
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.getByRole('checkbox', { name: 'Select d.png', exact: true }).uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await console.locator('#fileJanitorView-groups').click();
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await expect(fileButton(console)).toBeEnabled();
  // Looking, ticking and leaving files out moved nothing and made no folder.
  for (const name of ['a.png', 'b.png', 'c.png', 'd.png', 'x.pdf', 'y.pdf', 'song.wav'])
    expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);

  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().includes('/file-janitor')) posts.push(req.url());
  });
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('3 files filed.', {
    timeout: 60_000
  });
  await console.locator('.fj-results').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/02-e2e-one-press.png`, fullPage: true });

  // Exactly the ticked three moved, to the folder the picture drew them in.
  for (const name of ['a.png', 'b.png', 'c.png']) {
    expect(existsSync(join(ws.root, name)), `${name} left the folder`).toBe(false);
    expect(existsSync(join(ws.root, 'Filed', 'Images', name)), `${name} was filed`).toBe(true);
  }
  // Everything else is exactly where it was, and the unticked folder was never made.
  for (const name of ['d.png', 'x.pdf', 'y.pdf', 'song.wav'])
    expect(existsSync(join(ws.root, name)), `${name} untouched`).toBe(true);
  expect(existsSync(join(ws.root, 'Filed', 'Documents'))).toBe(false);
  // One request, and none of the old two-step flow.
  expect(posts.filter(isFileRequest)).toHaveLength(1);
  expect(posts.filter(url => /\/(preview|apply)$/.test(url))).toEqual([]);
  const history = await (await request.get(`${ws.base}/history?limit=100`)).json();
  expect(history.total).toBe(3);
  expect(history.actions.every((item: { result: string }) => item.result === 'applied')).toBe(true);
  // What is left is still waiting, drawn exactly once, and not ticked again.
  const after = await pictureOf(request, ws.base, batchID);
  expect(after.waiting).toBe(4);
  expect(drawn(after)).toBe(4);
  await expect(fileButton(console)).toHaveText(filesLabel(0));
});

test('a ticked file that changed after you looked files nothing, is marked, and a second press files the rest', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, ['one.png', 'two.png', 'three.png'], 'Changed since');
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await expect(fileButton(console)).toBeEnabled();
  // The person has looked at the picture. Then one ticked file changes on disk.
  writeFileSync(join(ws.root, 'two.png'), 'Changed after the picture was drawn', { mode: 0o600 });

  const refused = page.waitForResponse(
    res => res.request().method() === 'POST' && isFileRequest(res.url())
  );
  await fileButton(console).click();
  const response = await refused;
  expect(response.status()).toBe(409);
  expect((await response.json()).code).toBe('plan_changed');
  // Nothing moved, not even the two files that had not changed; no folder was made.
  await expect(console.locator('#fileJanitorSelection')).toContainText(
    'The folder changed since you looked. Nothing was filed.'
  );
  for (const name of ['one.png', 'two.png', 'three.png'])
    expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
  // The picture refreshed and marks the file that changed instead of filing it.
  await expect(console.locator('.fj-could-not')).toContainText('two.png');
  await expect(console.locator('.fj-could-not')).toContainText('changed');
  const picture = await pictureOf(request, ws.base);
  expect(picture.changed).toBe(1);
  expect(picture.could_not_file.count).toBe(1);
  expect(drawn(picture) + picture.could_not_file.count).toBe(3);
  await console
    .locator('#fileJanitorSelection')
    .evaluate(node => node.scrollIntoView({ block: 'center' }));
  await page.screenshot({ path: `${evidence}/02-e2e-changed-since.png`, fullPage: true });

  // The plan is now the two files that did not change; pressing again files them.
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('2 files filed.', {
    timeout: 60_000
  });
  expect(existsSync(join(ws.root, 'Filed', 'Images', 'one.png'))).toBe(true);
  expect(existsSync(join(ws.root, 'Filed', 'Images', 'three.png'))).toBe(true);
  // The changed file is still where it was: Ori left it alone.
  expect(existsSync(join(ws.root, 'two.png'))).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(2);
});

test('files marked for Trash need their own tick, which is never restored and clears when anything changes', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, ['one.png', 'two.png', 'three.png'], 'Trash tick');
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  const posts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) posts.push(req.url());
  });

  // Mark one file for Trash (per-file control, in All files).
  await console.getByRole('button', { name: 'All files', exact: true }).click();
  await console.getByRole('button', { name: 'Mark three.png for Trash', exact: true }).click();
  await expect(fileButton(console)).toHaveText(filesLabel(2, 1));
  const tick = console.locator('#fileJanitorTrashAck');
  // The tick names the count, is never ticked for the person, and gates the button.
  await expect(console.locator('.fj-trash-ack')).toContainText('Yes, move 1 file to Trash');
  await expect(tick).not.toBeChecked();
  await expect(fileButton(console)).toBeDisabled();
  await tick.check();
  await expect(fileButton(console)).toBeEnabled();
  await page.screenshot({ path: `${evidence}/02-e2e-trash-tick.png`, fullPage: true });
  await tick.uncheck();
  await expect(fileButton(console)).toBeDisabled();
  await tick.check();
  await expect(fileButton(console)).toBeEnabled();

  // Any saved change clears the tick: the person confirms the plan they now see.
  await console.getByRole('checkbox', { name: 'Select one.png', exact: true }).uncheck();
  await expect(fileButton(console)).toHaveText(filesLabel(1, 1));
  await expect(tick).not.toBeChecked();
  await expect(fileButton(console)).toBeDisabled();
  await tick.check();
  await expect(fileButton(console)).toBeEnabled();

  // And it is never restored after a reload: the choices are saved, the tick is not.
  await page.reload();
  await expect(fileButton(console)).toHaveText(filesLabel(1, 1));
  await expect(console.locator('#fileJanitorTrashAck')).not.toBeChecked();
  await expect(fileButton(console)).toBeDisabled();

  expect(posts).toEqual([]);
  for (const name of ['one.png', 'two.png', 'three.png'])
    expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

// The plan check lands a moment after every saved change and redraws the review. A
// keyboard user who just pressed Space on a tick must still be on that tick after
// the redraw, not thrown back to the top of the page.
test('keyboard focus stays on the folder tick after the plan check redraws', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, ['a.png', 'b.png'], 'Focus after plan');
  const console = await openReview(page, ws.slug);
  const tick = folder(console, 'images');
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();

  const planned = page.waitForResponse(res => /\/plan\?/.test(res.url()) && res.ok());
  await tick.focus();
  await page.keyboard.press('Space');
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  await planned;
  // The plan has answered and the button has settled; focus is still on the tick.
  await expect(fileButton(console)).toBeDisabled();
  await expect(console.locator('#fileJanitorSelection')).toHaveText('');
  await expect(tick).toBeFocused();
  // And the next keypress lands where the person is, not at the start of the page.
  await page.keyboard.press('Space');
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(tick).toBeFocused();
});

test('pressing twice, or again after a lost answer, files once and reports the first result', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, ['one.png', 'two.png'], 'Double press');
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();
  const requests: { key: string; body: Record<string, unknown> }[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) {
      const body = req.postDataJSON();
      requests.push({ key: body.client_key, body });
    }
  });

  // A double click is two presses in a row.
  await fileButton(console).dblclick();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('2 files filed.', {
    timeout: 60_000
  });
  expect(requests.length).toBeGreaterThanOrEqual(1);
  // However many requests went out, they are one press: one retry key.
  expect(new Set(requests.map(item => item.key)).size).toBe(1);
  for (const name of ['one.png', 'two.png']) {
    expect(existsSync(join(ws.root, name)), `${name} left the folder`).toBe(false);
    expect(existsSync(join(ws.root, 'Filed', 'Images', name)), `${name} was filed`).toBe(true);
  }
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(2);

  // The answer was lost and the person presses again: the same request reports the
  // first result, as a replay, and files nothing more.
  const again = await request.post(`${ws.base}/batches/${batchID}/file`, {
    data: requests[0].body
  });
  expect(again.ok(), await again.text()).toBe(true);
  const filing = (await again.json()).filing;
  expect(filing).toMatchObject({ replayed: true, filed: 2, trashed: 0, failed: 0 });
  expect(filing.filing_id).toBeTruthy();
  // And once more, to show it is stable: still one filing, still two files.
  const third = await request.post(`${ws.base}/batches/${batchID}/file`, {
    data: requests[0].body
  });
  expect((await third.json()).filing).toMatchObject({
    replayed: true,
    filed: 2,
    filing_id: filing.filing_id
  });
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(2);
});
