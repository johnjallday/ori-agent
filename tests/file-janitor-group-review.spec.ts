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
  readdirSync,
  readFileSync,
  statSync
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

// A file's content, and optionally the time it was last changed. Most fixtures do not
// care and share one short text; the pair and bundle cases choose sizes and days.
type Fixture = string | { text: string; mtime?: Date };

function syntheticRoot(names: string[], contents: Record<string, Fixture> = {}) {
  const temp = realpathSync(tmpdir());
  const root = realpathSync(mkdtempSync(join(temp, 'ori-group-review-')));
  if (!root.startsWith(temp) || root === temp) throw new Error('Non-temporary fixture');
  for (const name of names) {
    const chosen = contents[name];
    const text =
      typeof chosen === 'string' ? chosen : (chosen?.text ?? 'Synthetic group review fixture');
    const mtime = typeof chosen === 'object' && chosen.mtime ? chosen.mtime : old;
    writeFileSync(join(root, name), text, { mode: 0o600 });
    utimesSync(join(root, name), mtime, mtime);
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
  useAlias = false,
  contents: Record<string, Fixture> = {}
) {
  const ws = await workspace(request, label);
  const root = syntheticRoot(names, contents);
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
  // No tick box for a question group (Ori never ticks one); each row has one menu,
  // starting on "Leave here", and the question files stay loose.
  await expect(console.locator('.fj-loose-row input')).toHaveCount(0);
  const menus = console.locator('.fj-loose-row select');
  await expect(menus).toHaveCount(4);
  for (const menu of await menus.all()) await expect(menu).toHaveValue('leave');
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
  // The heading counts what is listed under it (the three still loose); the row's
  // note says where the fourth went.
  await expect(console.locator('.fj-loose-label').nth(2)).toHaveText('Music and audio · 3');
  const audioRow = console
    .locator('.fj-loose-row')
    .filter({ has: page.locator('#fileJanitorRowMenu-audio') });
  await expect(audioRow.locator('.fj-row-note:not(.fj-bundle-note)')).toHaveText('1 in Trash');
  // The four song.* files are one bundle card, which says it is split: the one in
  // Trash is no longer with the others.
  await expect(audioRow.locator('.fj-bundle-title')).toHaveText(
    'song · 4 files · moves or stays together'
  );
  await expect(audioRow.locator('.fj-bundle-note')).toHaveText('Split: 3 stay here, 1 in Trash');
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
    // The file that did not move is named on the receipt, with its reason, and the
    // title counts only what was filed.
    await expect(console.locator('.fj-receipt-notmoved-title')).toHaveText('1 file was not moved');
    await expect(console.locator('.fj-receipt-notmoved')).toContainText('second.png');
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
  // An unticked box is an empty outline, never an invisible one: the footer is not
  // inside the picture, and a border that names an undefined colour is not drawn.
  await expect(tick).toHaveCSS('border-top-width', '2px');
  await expect(tick).toHaveCSS('border-top-style', 'solid');
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

// ------------------------------------------------ answers on the loose rows
//
// Each question row has one menu; any listed file can be selected for a bar that
// decides that one file. Every choice is a saved edit: nothing moves until File.

// The one menu on a question row, by the row's rule word.
const rowMenu = (console: ReturnType<Page['locator']>, key: string) =>
  console.locator(`#fileJanitorRowMenu-${key}`);

// A listed file, by its name: the button that selects it for the one-file bar.
const fileNamed = (console: ReturnType<Page['locator']>, name: string) =>
  console.getByRole('button', { name, exact: true });

const draftPosts = (page: Page) => {
  const edits: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && /\/batches\/[^/]+\/draft$/.test(new URL(req.url()).pathname))
      edits.push(req.postDataJSON().action);
  });
  return edits;
};

test('one menu choice sends the sensitive-looking row to Documents, and one press files it with the rest', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const sensitive = ['passport-a.pdf', 'passport-b.pdf', 'tax-record.pdf'];
  const confident = ['one.png', 'two.png'];
  const names = [...sensitive, ...confident];
  const ws = await manualReview(request, names, 'Row to Documents');
  const batchID = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  const edits = draftPosts(page);
  const filePosts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) filePosts.push(req.url());
  });
  // Only the confident folder is ticked; the question row is loose and unplaced.
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(rowMenu(console, 'sensitive')).toHaveValue('leave');
  await expect(rowMenu(console, 'sensitive').locator('option')).toHaveText([
    'Leave here',
    'Leave here, stop asking',
    'Move to Documents',
    'Move to Images',
    'Move to Audio',
    'Move to Video',
    'Move to Archives',
    'Move to Installers',
    'Move to Data',
    'Move to Other'
  ]);
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/03-e2e-row-menu.png`, fullPage: true });

  // One choice, one request.
  await rowMenu(console, 'sensitive').selectOption({ label: 'Move to Documents' });
  await expect(fileButton(console)).toHaveText(filesLabel(5));
  expect(edits).toEqual(['select_group']);
  const row = console
    .locator('.fj-loose-row')
    .filter({ has: page.locator('#fileJanitorRowMenu-sensitive') });
  await expect(row.locator('.fj-row-note')).toHaveText('All in Filed/Documents');
  // The heading keeps the row's count; the note says where the files went.
  await expect(row.locator('.fj-loose-label')).toHaveText('Sensitive-looking names · 3');
  await expect(rowMenu(console, 'sensitive')).toHaveValue('move:documents');
  // The files are drawn in the Documents card, first, tagged as the person's choice.
  const documents = console.locator('[data-fj-folder="documents"]');
  await expect(documents).toContainText('Documents');
  for (const name of sensitive) await expect(documents).toContainText(name);
  await expect(documents.locator('.fj-tag').filter({ hasText: 'your choice' })).toHaveCount(3);
  // The picture still adds up, and nothing has moved: this was a choice, not a move.
  const picture = await pictureOf(request, ws.base, batchID);
  expect(drawn(picture)).toBe(5);
  expect(picture.loose.find((item: { key: string }) => item.key === 'sensitive')).toMatchObject({
    count: 3,
    placed_count: 3,
    placed_category: 'documents',
    loose_count: 0
  });
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);
  expect(filePosts).toEqual([]);
  await page.screenshot({ path: `${evidence}/03-e2e-row-placed.png`, fullPage: true });

  // The choice is saved: it survives a reload and can be changed back.
  await page.reload();
  await expect(rowMenu(console, 'sensitive')).toHaveValue('move:documents');
  await expect(fileButton(console)).toHaveText(filesLabel(5));
  await expect(fileButton(console)).toBeEnabled();

  // One press files exactly those files and the ticked folder's, and nothing else.
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('5 files filed.', {
    timeout: 60_000
  });
  expect(filePosts).toHaveLength(1);
  for (const name of sensitive) {
    expect(existsSync(join(ws.root, name)), name).toBe(false);
    expect(existsSync(join(ws.root, 'Filed', 'Documents', name)), name).toBe(true);
  }
  for (const name of confident) {
    expect(existsSync(join(ws.root, name)), name).toBe(false);
    expect(existsSync(join(ws.root, 'Filed', 'Images', name)), name).toBe(true);
  }
  expect(destinations(ws.root).sort()).toEqual(['Documents', 'Images']);
});

test('"Leave here" takes a placed row back, and a row can be sent to another folder', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, ['passport-a.pdf', 'passport-b.pdf', 'one.png']);
  const console = await openReview(page, ws.slug);
  const edits = draftPosts(page);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  // Nothing to take back yet: choosing it again asks the server for nothing.
  await rowMenu(console, 'sensitive').selectOption({ label: 'Move to Documents' });
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await rowMenu(console, 'sensitive').selectOption({ label: 'Move to Archives' });
  await expect(console.locator('.fj-row-note')).toHaveText('All in Filed/Archives');
  await expect(console.locator('[data-fj-folder="documents"]')).toHaveCount(0);
  await expect(console.locator('[data-fj-folder="archives"]')).toContainText('passport-a.pdf');
  await rowMenu(console, 'sensitive').selectOption({ label: 'Leave here' });
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(console.locator('[data-fj-folder="archives"]')).toHaveCount(0);
  await expect(console.locator('.fj-row-note')).toHaveCount(0);
  await expect(console.locator('.fj-loose-label').first()).toHaveText(
    'Sensitive-looking names · 2'
  );
  expect(edits).toEqual(['select_group', 'select_group', 'clear_group']);
  // The files never left the folder, and "Leave here" remembers nothing: they are
  // still waiting in this scan.
  const picture = await pictureOf(request, ws.base);
  expect(picture.loose.find((item: { key: string }) => item.key === 'sensitive')).toMatchObject({
    placed_count: 0,
    loose_count: 2
  });
  expect(destinations(ws.root)).toEqual([]);
  for (const name of ['passport-a.pdf', 'passport-b.pdf', 'one.png'])
    expect(existsSync(join(ws.root, name)), name).toBe(true);
});

test('"Leave here, stop asking" keeps a row out of the next scan, and Review again brings it back', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = ['passport-a.pdf', 'passport-b.pdf', 'one.png'];
  const ws = await manualReview(request, names, 'Stop asking');
  const console = await openReview(page, ws.slug);
  const edits = draftPosts(page);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await rowMenu(console, 'sensitive').selectOption({ label: 'Leave here, stop asking' });
  // The row says so, and offers the way back; its files are not waiting any more.
  const kept = console.locator('[data-fj-kept="sensitive"]');
  await expect(kept).toContainText('Sensitive-looking names · 2');
  await expect(kept).toContainText('Ori will not ask about these again.');
  await expect(rowMenu(console, 'sensitive')).toHaveCount(0);
  expect(edits).toEqual(['keep_group']);
  await page.screenshot({ path: `${evidence}/03-e2e-stop-asking.png`, fullPage: true });
  // Nothing moved, and the files are still in the folder.
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);

  // A new scan does not suggest those exact files again (nothing else is new).
  const scanned = await request.post(`${ws.base}/scan`);
  expect(scanned.ok(), await scanned.text()).toBe(true);
  expect((await scanned.json()).created).toBe(false);

  // Review again forgets the choice; the files come back in the next scan.
  await kept.getByRole('button', { name: 'Review again' }).click();
  await expect(kept).toHaveCount(0);
  // Said beside the File button, where the person is looking and where it is announced.
  await expect(console.locator('#fileJanitorSelection')).toHaveText(
    'Ori will offer these again the next time it scans.'
  );
  expect(edits).toEqual(['keep_group', 'reconsider_files']);
  const rescanned = await request.post(`${ws.base}/scan`);
  expect(rescanned.ok(), await rescanned.text()).toBe(true);
  expect((await rescanned.json()).created).toBe(true);
  const again = await pictureOf(request, ws.base);
  expect(again.loose.find((item: { key: string }) => item.key === 'sensitive')).toMatchObject({
    count: 2,
    loose_count: 2
  });
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
});

test('one file can be left out of a ticked folder, or sent to Trash and put back, from the one-file bar', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const names = ['a.png', 'b.png', 'c.png'];
  const ws = await manualReview(request, names, 'One-file bar');
  const console = await openReview(page, ws.slug);
  const edits = draftPosts(page);
  const filePosts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) filePosts.push(req.url());
  });
  const bar = console.locator('#fileJanitorFileBar');
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await expect(bar).toHaveText('Click any file to move just that one.');

  // Selecting a file names it in the bar and offers one menu for that file only.
  await fileNamed(console, 'b.png').click();
  await expect(console.locator('#fileJanitorFileBarName')).toHaveText('b.png');
  await expect(fileNamed(console, 'b.png')).toHaveAttribute('aria-pressed', 'true');
  await expect(console.locator('#fileJanitorFileMenu')).toBeFocused();
  await expect(console.locator('#fileJanitorFileMenu option')).toHaveText([
    'What should happen to this file?',
    'Move to Documents',
    'Move to Audio',
    'Move to Video',
    'Move to Archives',
    'Move to Installers',
    'Move to Data',
    'Move to Other',
    'Leave here',
    'Leave here, stop asking',
    'Move to Trash'
  ]);
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/03-e2e-one-file-bar.png`, fullPage: true });
  // Cancel closes it without asking the server for anything.
  await console.locator('#fileJanitorFileBarCancel').click();
  await expect(bar).toHaveText('Click any file to move just that one.');
  expect(edits).toEqual([]);

  // Leave one file out of its ticked folder.
  await fileNamed(console, 'b.png').click();
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Leave here' });
  await expect(console.locator('.fj-loose-label')).toHaveText(['Left out by you · 1']);
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(folder(console, 'images')).toHaveJSProperty('indeterminate', true);
  await expect(bar).toHaveText('Click any file to move just that one.');
  expect(edits).toEqual(['exclude_files']);

  // Send one file to Trash from the bar: the Trash box shows it, the button says so
  // and waits for the Trash tick. Nothing is pressed.
  await fileNamed(console, 'c.png').click();
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Move to Trash' });
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  await expect(console.locator('.fj-trash')).toContainText('c.png');
  await expect(fileButton(console)).toHaveText(filesLabel(1, 1));
  await expect(console.locator('.fj-trash-ack')).toContainText('Yes, move 1 file to Trash');
  await expect(console.locator('#fileJanitorTrashAck')).not.toBeChecked();
  await expect(fileButton(console)).toBeDisabled();
  expect(edits).toEqual(['exclude_files', 'mark_trash']);
  await page.screenshot({ path: `${evidence}/03-e2e-left-out-and-trash.png`, fullPage: true });

  // A file in Trash is not offered Trash again; "Leave here" puts it back.
  await fileNamed(console, 'c.png').click();
  await expect(
    console.locator('#fileJanitorFileMenu option').filter({ hasText: 'Move to Trash' })
  ).toHaveCount(0);
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Leave here' });
  await expect(console.locator('.fj-trash')).toHaveCount(0);
  await expect(console.locator('#fileJanitorTrashAck')).toHaveCount(0);
  await expect(console.locator('.fj-loose-label')).toHaveText(['Left out by you · 2']);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  expect(edits).toEqual(['exclude_files', 'mark_trash', 'unmark_trash']);

  // Move a single file to another folder: the person's choice, listed first, tagged.
  await fileNamed(console, 'a.png').click();
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Move to Archives' });
  const archives = console.locator('[data-fj-folder="archives"]');
  await expect(archives).toContainText('a.png');
  await expect(archives.locator('.fj-tag').filter({ hasText: 'your choice' })).toHaveCount(1);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  expect(edits.at(-1)).toBe('select_files');

  // Every file is still where it was: this was all choices.
  const picture = await pictureOf(request, ws.base);
  expect(drawn(picture)).toBe(3);
  expect(filePosts).toEqual([]);
  expect(destinations(ws.root)).toEqual([]);
  for (const name of names) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('a row menu and a file button work by keyboard, and focus stays in the picture afterwards', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const ws = await manualReview(request, [
    'passport-a.pdf',
    'passport-b.pdf',
    'one.png',
    'two.png'
  ]);
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  await expect(fileButton(console)).toBeEnabled();
  const tabTo = async (target: ReturnType<Page['locator']>) => {
    for (let i = 0; i < 120; i++) {
      if (await target.evaluate(node => node === document.activeElement).catch(() => false)) return;
      await page.keyboard.press('Tab');
    }
    throw new Error('Keyboard target unreachable');
  };
  const insideConsole = () =>
    page.evaluate(() => {
      const active = document.activeElement;
      return Boolean(active && active !== document.body && active.closest('#fileJanitorConsole'));
    });

  // Tab to the row menu and choose a folder from the keyboard.
  const menu = rowMenu(console, 'sensitive');
  await tabTo(menu);
  await expect(menu).toBeFocused();
  await menu.selectOption({ label: 'Move to Documents' });
  await expect(fileButton(console)).toHaveText(filesLabel(4));
  // The picture redrew; focus is still on the row's menu, not thrown to the top.
  await expect(menu).toBeFocused();
  expect(await insideConsole()).toBe(true);

  // Tab to a file, press Enter: the bar opens on that file with focus on its menu.
  const one = fileNamed(console, 'one.png');
  await tabTo(one);
  await page.keyboard.press('Enter');
  await expect(console.locator('#fileJanitorFileBarName')).toHaveText('one.png');
  await expect(console.locator('#fileJanitorFileMenu')).toBeFocused();
  // Decide it from the bar. The bar closes with the choice, so focus must land
  // somewhere sensible in the picture rather than on the page.
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Move to Trash' });
  await expect(console.locator('.fj-trash')).toContainText('one.png');
  await expect(console.locator('#fileJanitorFileBar')).toHaveText(
    'Click any file to move just that one.'
  );
  const active = await page.evaluate(() => {
    const node = document.activeElement;
    return node ? `${node.tagName.toLowerCase()}${node.id ? '#' + node.id : ''}` : 'none';
  });
  expect(
    await insideConsole(),
    `focus fell out of the picture after the bar closed (on ${active})`
  ).toBe(true);
});

test('a row menu and the one-file bar fit at 390px with a long file name', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const long = 'passport-scan-of-a-very-long-descriptive-file-name-for-the-overflow-check-2026.pdf';
  const ws = await manualReview(request, [long, 'passport-b.pdf', 'one.png'], 'Narrow answers');
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  const fits = async () => {
    expect(await console.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)
    ).toBe(true);
  };
  await fits();
  const menu = rowMenu(console, 'sensitive');
  await menu.scrollIntoViewIfNeeded();
  const menuBox = await menu.boundingBox();
  expect(menuBox!.x).toBeGreaterThanOrEqual(0);
  expect(menuBox!.x + menuBox!.width).toBeLessThanOrEqual(391);
  expect(menuBox!.height).toBeGreaterThanOrEqual(44);
  // Placed, the row keeps its note and the files move into a card; still no overflow.
  await menu.selectOption({ label: 'Move to Documents' });
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await fits();
  // Select the long-named file: the bar names it and shows its menu inside the page.
  await fileNamed(console, long).click();
  const bar = console.locator('#fileJanitorFileBar');
  await expect(console.locator('#fileJanitorFileBarName')).toHaveText(long);
  await fits();
  const barBox = await bar.boundingBox();
  expect(barBox!.x).toBeGreaterThanOrEqual(0);
  expect(barBox!.x + barBox!.width).toBeLessThanOrEqual(391);
  const fileMenuBox = await console.locator('#fileJanitorFileMenu').boundingBox();
  expect(fileMenuBox!.x + fileMenuBox!.width).toBeLessThanOrEqual(391);
  mkdirSync(evidence, { recursive: true });
  await bar.evaluate(node => node.scrollIntoView({ block: 'center' }));
  await page.screenshot({ path: `${evidence}/03-e2e-bar-390.png`, fullPage: true });
});

// ------------------------------------------- possible copies and music bundles
//
// Possible copies are drawn as pair cards (the file with no ending first, one hint
// line from size and date) and music that shares a name apart from the extension as
// bundle cards. "Trash the copy" is a per-file action offered under one rule; a
// bundle has one menu whose choice covers every file in it. Every choice is a saved
// edit: nothing moves, and nothing goes to the Trash, until File is pressed.

const bytes = (count: number) => 'x'.repeat(count);

type Root = ReturnType<Page['locator']>;

const pairCard = (console: Root, name: string) =>
  console.locator('.fj-pair').filter({ hasText: name });

// A bundle's card, found by the name in its heading ("Song 130 · 3 files · ...").
const bundleCard = (console: Root, name: string) =>
  console.locator('.fj-bundle').filter({ hasText: `${name} · ` });

// The Trash of the isolated server, found only through the sandbox path the e2e
// runner hands the spec (`--sandbox-env ORI_E2E_SANDBOX`): the server runs with HOME
// inside that sandbox, so its Trash is <sandbox>/.Trash. The real Trash is never read.
function sandboxTrash(): string | null {
  const sandbox = process.env.ORI_E2E_SANDBOX;
  if (!sandbox) return null;
  const real = realpathSync(sandbox);
  const temp = realpathSync(tmpdir());
  if (!real.startsWith(temp) || real === temp) throw new Error('The e2e sandbox is not temporary');
  return join(real, '.Trash');
}

const focusedElement = (page: Page) =>
  page.evaluate(() => {
    const node = document.activeElement;
    return node ? `${node.tagName.toLowerCase()}${node.id ? '#' + node.id : ''}` : 'none';
  });

const pairFiles = [
  'Budget.pdf',
  'Budget (2).pdf',
  'Plan.pdf',
  'Plan copy.pdf',
  'Memo.pdf',
  'Memo-v2.pdf'
];
const pairContents: Record<string, string> = {
  'Budget.pdf': bytes(20),
  'Budget (2).pdf': bytes(20),
  'Plan.pdf': bytes(20),
  'Plan copy.pdf': bytes(35), // a different size: never offered "Trash the copy"
  'Memo.pdf': bytes(20),
  'Memo-v2.pdf': bytes(20) // the same size, but a version ending: never offered it
};

test('three kinds of pair: only a copy of the same size with a copy ending has "Trash the copy"', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(request, pairFiles, 'Three pairs', false, pairContents);
  const console = await openReview(page, ws.slug);
  // Possible copies are all questions: nothing is ticked, nothing to file.
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  await expect(console.locator('.fj-loose-label')).toHaveText(['Possible copies · 6']);
  await expect(console.locator('.fj-pair')).toHaveCount(3);

  // The file with no ending comes first in every card, then the one with an ending.
  await expect(pairCard(console, 'Budget.pdf').locator('.fj-pair-line .fj-file-name')).toHaveText([
    'Budget.pdf',
    'Budget (2).pdf'
  ]);
  await expect(pairCard(console, 'Plan.pdf').locator('.fj-pair-line .fj-file-name')).toHaveText([
    'Plan.pdf',
    'Plan copy.pdf'
  ]);
  await expect(pairCard(console, 'Memo.pdf').locator('.fj-pair-line .fj-file-name')).toHaveText([
    'Memo.pdf',
    'Memo-v2.pdf'
  ]);
  // Each line shows the size and the day it last changed.
  const facts = /^\d+ B · [A-Z][a-z]{2} \d{1,2}, \d{4}$/;
  for (const fact of await console.locator('.fj-pair-facts').all())
    await expect(fact).toHaveText(facts);
  await expect(pairCard(console, 'Plan.pdf').locator('.fj-pair-facts').nth(1)).toHaveText(
    /^35 B · /
  );
  // One hint line per card, from size and date alone.
  await expect(pairCard(console, 'Budget.pdf').locator('.fj-pair-hint')).toHaveText(
    'Same size, same day'
  );
  await expect(pairCard(console, 'Plan.pdf').locator('.fj-pair-hint')).toHaveText('Different size');
  await expect(pairCard(console, 'Memo.pdf').locator('.fj-pair-hint')).toHaveText(
    'Same size, same day'
  );
  // Ori does not compare what is inside files, so it never says "duplicate".
  await expect(console).not.toContainText(/duplicate|identical/i);

  // The one button: the bracketed copy of the same size as its original.
  const trashButtons = console.locator('[id^="fileJanitorTrashCopy-"]');
  await expect(trashButtons).toHaveCount(1);
  await expect(trashButtons).toHaveAttribute('aria-label', 'Trash the copy Budget (2).pdf');
  await expect(
    pairCard(console, 'Budget.pdf').locator('[id^="fileJanitorTrashCopy-"]')
  ).toHaveCount(1);
  await expect(pairCard(console, 'Plan.pdf').getByRole('button', { name: /trash/i })).toHaveCount(
    0
  );
  await expect(pairCard(console, 'Memo.pdf').getByRole('button', { name: /trash/i })).toHaveCount(
    0
  );
  // No Trash for a whole row or a whole pair: no menu offers it.
  for (const menu of await console.locator('select.fj-row-menu').all())
    await expect(menu.locator('option').filter({ hasText: /trash/i })).toHaveCount(0);

  mkdirSync(evidence, { recursive: true });
  await console.locator('.fj-loose').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-e2e-pairs.png`, fullPage: true });
  // Looking at pairs moves nothing.
  for (const name of pairFiles) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);
});

test('Trash the copy marks that one file, Put the copy back undoes it, and nothing is trashed until File', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(
    request,
    pairFiles.slice(0, 2),
    'Trash the copy',
    false,
    pairContents
  );
  const console = await openReview(page, ws.slug);
  const edits = draftPosts(page);
  const filePosts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) filePosts.push(req.url());
  });
  const card = pairCard(console, 'Budget.pdf');
  const trash = card.getByRole('button', { name: 'Trash the copy Budget (2).pdf', exact: true });
  await expect(fileButton(console)).toHaveText(filesLabel(0));

  await trash.click();
  // The line says so, the Trash box holds the one file, and the button waits for the tick.
  await expect(card.locator('.fj-pair-line').nth(1)).toContainText('in Trash');
  await expect(
    card.getByRole('button', { name: 'Put Budget (2).pdf back', exact: true })
  ).toBeVisible();
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  await expect(console.locator('.fj-trash')).toContainText('Budget (2).pdf');
  await expect(fileButton(console)).toHaveText(filesLabel(0, 1));
  await expect(fileButton(console)).toBeDisabled();
  await expect(console.locator('.fj-trash-ack')).toContainText('Yes, move 1 file to Trash');
  await expect(console.locator('#fileJanitorTrashAck')).not.toBeChecked();
  // Only the copy is marked: the original has no button and is still loose.
  await expect(card.locator('.fj-pair-line').nth(0)).not.toContainText('in Trash');
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/04-e2e-trash-copy.png`, fullPage: true });

  // Put it back: the Trash box goes, the button is back, nothing else changed.
  await card.getByRole('button', { name: 'Put Budget (2).pdf back', exact: true }).click();
  await expect(console.locator('.fj-trash')).toHaveCount(0);
  await expect(console.locator('#fileJanitorTrashAck')).toHaveCount(0);
  await expect(trash).toBeVisible();
  await expect(fileButton(console)).toHaveText(filesLabel(0));
  // And mark it again.
  await trash.click();
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  expect(edits).toEqual(['mark_trash', 'unmark_trash', 'mark_trash']);

  // A mark is a saved choice, not an action: both files are still in the folder.
  for (const name of ['Budget.pdf', 'Budget (2).pdf'])
    expect(existsSync(join(ws.root, name))).toBe(true);
  expect(filePosts).toEqual([]);
  expect((await (await request.get(`${ws.base}/history`)).json()).total).toBe(0);
});

test('pressing File with the Trash tick sends the marked copy to the Trash and leaves the original alone', async ({
  page,
  request
}) => {
  const trashDir = sandboxTrash();
  test.skip(
    !trashDir,
    'NOT RUN: the runner did not pass the server sandbox (use e2e-fresh.sh --sandbox-env ORI_E2E_SANDBOX), so the isolated Trash cannot be located and the real one is never touched'
  );
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(
    request,
    pairFiles.slice(0, 2),
    'Trash the copy press',
    false,
    pairContents
  );
  const console = await openReview(page, ws.slug);
  const filePosts: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && isFileRequest(req.url())) filePosts.push(req.url());
  });
  await pairCard(console, 'Budget.pdf')
    .getByRole('button', { name: 'Trash the copy Budget (2).pdf', exact: true })
    .click();
  await expect(fileButton(console)).toHaveText(filesLabel(0, 1));
  await expect(fileButton(console)).toBeDisabled();
  await console.locator('#fileJanitorTrashAck').check();
  await expect(fileButton(console)).toBeEnabled();
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toContainText('1 moved to Trash', {
    timeout: 60_000
  });
  expect(filePosts).toHaveLength(1);
  // The copy left the folder and is in the isolated Trash; the original did not move.
  expect(existsSync(join(ws.root, 'Budget (2).pdf'))).toBe(false);
  expect(existsSync(join(ws.root, 'Budget.pdf'))).toBe(true);
  expect(readFileSync(join(ws.root, 'Budget.pdf'), 'utf8')).toBe(pairContents['Budget.pdf']);
  const trashed = existsSync(trashDir!) ? readdirSync(trashDir!) : [];
  const copies = trashed.filter(name => name.startsWith('Budget (2)'));
  expect(copies, `the Trash held ${JSON.stringify(trashed)}`).toHaveLength(1);
  expect(readFileSync(join(trashDir!, copies[0]), 'utf8')).toBe(pairContents['Budget (2).pdf']);
  expect(trashed.filter(name => name.startsWith('Budget.pdf'))).toEqual([]);
  // Trash only: no destination folder was made.
  expect(destinations(ws.root)).toEqual([]);
});

const songFiles = [
  'Song 130.wav',
  'Song 130.als',
  'Song 130.mp3',
  'Take 2.wav',
  'Take 2.als',
  'solo.mp3',
  'one.png'
];

test('one menu choice sends a bundle to Filed/Audio, and one press files all of its files side by side', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(request, songFiles, 'Song bundle');
  const console = await openReview(page, ws.slug);
  const drafts: { action: string; candidate_ids?: string[]; category?: string }[] = [];
  const filePosts: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'POST') return;
    if (/\/batches\/[^/]+\/draft$/.test(new URL(req.url()).pathname))
      drafts.push(req.postDataJSON());
    if (isFileRequest(req.url())) filePosts.push(req.url());
  });
  // Only the confident png is ticked; the music is questions.
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(console.locator('.fj-bundle')).toHaveCount(2);
  const song = bundleCard(console, 'Song 130');
  const take = bundleCard(console, 'Take 2');
  await expect(song.locator('.fj-bundle-title')).toHaveText(
    'Song 130 · 3 files · moves or stays together'
  );
  await expect(take.locator('.fj-bundle-title')).toHaveText(
    'Take 2 · 2 files · moves or stays together'
  );
  for (const name of ['Song 130.wav', 'Song 130.als', 'Song 130.mp3'])
    await expect(song.getByRole('button', { name, exact: true })).toBeVisible();
  // The song with no partner is a single, listed beside the bundles in the same row.
  const audio = console
    .locator('.fj-loose-row')
    .filter({ has: page.locator('#fileJanitorRowMenu-audio') });
  await expect(audio.getByRole('button', { name: 'solo.mp3', exact: true })).toBeVisible();
  await expect(console.locator('.fj-bundle').getByRole('button', { name: 'solo.mp3' })).toHaveCount(
    0
  );
  await expect(song.locator('select option')).toHaveText([
    'Leave here',
    'Leave here, stop asking',
    'Move to Documents',
    'Move to Images',
    'Move to Audio',
    'Move to Video',
    'Move to Archives',
    'Move to Installers',
    'Move to Data',
    'Move to Other'
  ]);
  await expect(song.locator('select')).toHaveValue('leave');

  // One choice, one request, covering all three files and nothing else.
  await song.locator('select').selectOption({ label: 'Move to Audio' });
  await expect(fileButton(console)).toHaveText(filesLabel(4));
  await expect(song.locator('.fj-bundle-note')).toHaveText('All in Filed/Audio');
  await expect(song.locator('select')).toHaveValue('move:audio');
  await expect(take.locator('.fj-bundle-note')).toHaveCount(0);
  expect(drafts.map(edit => edit.action)).toEqual(['select_files']);
  expect(drafts[0].category).toBe('audio');
  expect(drafts[0].candidate_ids).toHaveLength(3);
  // The files are in the Audio card, tagged as the person's choice; nothing moved yet.
  const audioCard = console.locator('[data-fj-folder="audio"]');
  for (const name of ['Song 130.wav', 'Song 130.als', 'Song 130.mp3'])
    await expect(audioCard).toContainText(name);
  await expect(audioCard.locator('.fj-tag').filter({ hasText: 'your choice' })).toHaveCount(3);
  for (const name of songFiles) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);
  expect(filePosts).toEqual([]);
  mkdirSync(evidence, { recursive: true });
  await console.locator('.fj-loose').evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/04-e2e-bundles.png`, fullPage: true });

  // One press files the bundle and the png.
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('4 files filed.', {
    timeout: 60_000
  });
  expect(filePosts).toHaveLength(1);
  const audioDir = join(ws.root, 'Filed', 'Audio');
  expect(readdirSync(audioDir).sort()).toEqual(['Song 130.als', 'Song 130.mp3', 'Song 130.wav']);
  // Side by side: files, not subfolders.
  for (const name of readdirSync(audioDir))
    expect(statSync(join(audioDir, name)).isFile(), name).toBe(true);
  for (const name of ['Song 130.wav', 'Song 130.als', 'Song 130.mp3'])
    expect(existsSync(join(ws.root, name)), name).toBe(false);
  // The other bundle, the lone track and nothing else moved.
  for (const name of ['Take 2.wav', 'Take 2.als', 'solo.mp3'])
    expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(existsSync(join(ws.root, 'Filed', 'Images', 'one.png'))).toBe(true);
  expect(destinations(ws.root).sort()).toEqual(['Audio', 'Images']);
});

test('a bundle split by single-file choices says how it is split, and its own "Leave here" takes the placed file back', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(request, songFiles, 'Split bundle');
  const console = await openReview(page, ws.slug);
  const drafts: { action: string; candidate_ids?: string[]; category?: string }[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && /\/batches\/[^/]+\/draft$/.test(new URL(req.url()).pathname))
      drafts.push(req.postDataJSON());
  });
  const song = bundleCard(console, 'Song 130');
  const menu = song.locator('select');
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  await expect(song.locator('.fj-bundle-note')).toHaveCount(0);

  // One file of the bundle goes to Documents from the one-file bar.
  await fileNamed(song, 'Song 130.als').click();
  await expect(console.locator('#fileJanitorFileBarName')).toHaveText('Song 130.als');
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Move to Documents' });
  await expect(song.locator('.fj-bundle-note')).toHaveText(
    'Split: 2 stay here, 1 in Filed/Documents'
  );
  // No one folder describes the bundle now: a disabled "Mixed choices" line is selected.
  await expect(menu).toHaveValue('mixed');
  await expect(menu.locator('option:checked')).toHaveText('Mixed choices');
  await expect(fileButton(console)).toHaveText(filesLabel(2));
  mkdirSync(evidence, { recursive: true });
  await song.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${evidence}/04-e2e-bundle-split.png`, fullPage: true });

  // Bundle-level "Leave here" takes the placed file back; the bundle is whole again.
  await menu.selectOption({ label: 'Leave here' });
  await expect(song.locator('.fj-bundle-note')).toHaveCount(0);
  await expect(menu).toHaveValue('leave');
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  expect(drafts.map(edit => edit.action)).toEqual(['select_files', 'exclude_files']);
  expect(drafts[1].candidate_ids).toHaveLength(1);

  // A file already in Trash is never overwritten by a bundle choice: it is left out
  // of the request, stays in the Trash box, and the card counts it.
  await fileNamed(song, 'Song 130.wav').click();
  await console.locator('#fileJanitorFileMenu').selectOption({ label: 'Move to Trash' });
  await expect(console.locator('.fj-trash')).toContainText('Song 130.wav');
  await menu.selectOption({ label: 'Move to Audio' });
  await expect(song.locator('.fj-bundle-note')).toHaveText('Split: 2 in Filed/Audio, 1 in Trash');
  expect(drafts.map(edit => edit.action)).toEqual([
    'select_files',
    'exclude_files',
    'mark_trash',
    'select_files'
  ]);
  expect(drafts[3].candidate_ids).toHaveLength(2);
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  // Everything above was a choice: nothing moved.
  for (const name of songFiles) expect(existsSync(join(ws.root, name)), name).toBe(true);
  expect(destinations(ws.root)).toEqual([]);
});

test('a bundle menu and "Trash the copy" work by keyboard, and focus stays in the picture afterwards', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(
    request,
    ['Budget.pdf', 'Budget (2).pdf', 'Song 130.wav', 'Song 130.als', 'one.png'],
    'Keyboard cards',
    false,
    { 'Budget.pdf': bytes(20), 'Budget (2).pdf': bytes(20) }
  );
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  const tabTo = async (target: Root) => {
    for (let i = 0; i < 150; i++) {
      if (await target.evaluate(node => node === document.activeElement).catch(() => false)) return;
      await page.keyboard.press('Tab');
    }
    throw new Error('Keyboard target unreachable');
  };
  const insideConsole = () =>
    page.evaluate(() => {
      const active = document.activeElement;
      return Boolean(active && active !== document.body && active.closest('#fileJanitorConsole'));
    });

  // Tab to the bundle menu and choose a folder.
  const menu = bundleCard(console, 'Song 130').locator('select');
  await tabTo(menu);
  await expect(menu).toBeFocused();
  await menu.selectOption({ label: 'Move to Audio' });
  await expect(fileButton(console)).toHaveText(filesLabel(3));
  await expect(bundleCard(console, 'Song 130').locator('.fj-bundle-note')).toHaveText(
    'All in Filed/Audio'
  );
  const afterMenu = await focusedElement(page);
  expect(
    await insideConsole(),
    `focus fell out of the picture after the bundle menu (on ${afterMenu})`
  ).toBe(true);
  await expect(menu).toBeFocused();

  // Tab to "Trash the copy" and press Enter. The button becomes "Put the copy back",
  // so focus has to land on something sensible in the picture, not on the page.
  const trash = console.locator('[id^="fileJanitorTrashCopy-"]');
  await tabTo(trash);
  await expect(trash).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(console.locator('.fj-trash')).toContainText('Budget (2).pdf');
  await expect(fileButton(console)).toHaveText(filesLabel(3, 1));
  const afterTrash = await focusedElement(page);
  expect(
    await insideConsole(),
    `focus fell out of the picture after Trash the copy (on ${afterTrash})`
  ).toBe(true);
});

test('a pair card and a bundle card fit at 390px with long file names', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const stem = 'quarterly-budget-report-final-for-the-overflow-check-with-a-long-name-2026';
  const song = 'a-very-long-song-title-for-the-overflow-check-take-one-hundred-and-thirty';
  const ws = await manualReview(
    request,
    [`${stem}.pdf`, `${stem} (2).pdf`, `${song}.wav`, `${song}.als`, 'one.png'],
    'Narrow cards',
    false,
    { [`${stem}.pdf`]: bytes(20), [`${stem} (2).pdf`]: bytes(20) }
  );
  const console = await openReview(page, ws.slug);
  await expect(fileButton(console)).toHaveText(filesLabel(1));
  const fits = async () => {
    expect(await console.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)
    ).toBe(true);
  };
  await fits();
  for (const card of [console.locator('.fj-pair'), console.locator('.fj-bundle')]) {
    await card.scrollIntoViewIfNeeded();
    const box = await card.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(391);
  }
  const menu = console.locator('.fj-bundle select');
  const menuBox = await menu.boundingBox();
  expect(menuBox!.x + menuBox!.width).toBeLessThanOrEqual(391);
  expect(menuBox!.height).toBeGreaterThanOrEqual(44);
  // Trash the copy, and place the bundle: still no overflow, and the lines wrap.
  await console.locator('[id^="fileJanitorTrashCopy-"]').click();
  await expect(console.locator('.fj-trash')).toContainText('Trash · 1');
  await menu.selectOption({ label: 'Move to Audio' });
  await expect(console.locator('.fj-bundle-note')).toHaveText('All in Filed/Audio');
  await fits();
  mkdirSync(evidence, { recursive: true });
  await console.locator('.fj-pair').evaluate(node => node.scrollIntoView({ block: 'center' }));
  await page.screenshot({ path: `${evidence}/04-e2e-pairs-390.png`, fullPage: true });
});

// ------------------------------------------------ the receipt, and Undo all
//
// After a press the receipt says what the journal says happened: the count filed,
// where the files are, how many stay loose, one row per destination folder and
// every file that did not move. Undo all puts every file back in one request, and
// Ori scans again only when the person presses the one button for it.

// Every file below a folder, as paths relative to it. Empty folders count for nothing.
function filesUnder(dir: string, prefix = ''): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap(entry =>
    entry.isDirectory()
      ? filesUnder(join(dir, entry.name), `${prefix}${entry.name}/`)
      : [`${prefix}${entry.name}`]
  );
}

// The files still loose in the folder itself: not the Filed root, not what is in it.
const looseFiles = (root: string) =>
  readdirSync(root, { withFileTypes: true })
    .filter(entry => entry.isFile())
    .map(entry => entry.name)
    .sort();

// A different content per file, so "back where it was" means the same bytes too.
const ownBytes = (names: string[]) =>
  Object.fromEntries(names.map(name => [name, `original bytes of ${name}`]));

const receiptRows = (console: Root) => console.locator('#fileJanitorResultsHost .fj-receipt-row');
const undoAllButton = (console: Root) => console.locator('#fileJanitorUndoAll');
const undoneTitle = (console: Root) => console.locator('#fileJanitorUndoneTitle');

// The POSTs to this workspace's Filing routes, in the order the browser sent them.
function posts(page: Page) {
  const seen: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST' && /\/file-janitor\//.test(req.url()))
      seen.push(new URL(req.url()).pathname.replace(/^.*\/file-janitor\//, ''));
  });
  return seen;
}

const filedNames = ['a.png', 'b.png', 'c.png', 'setup.dmg'];

test('after a press the receipt says what the journal says: the count, where, how many stay loose, one row per destination', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const stay = ['passport-a.pdf', 'passport-b.pdf'];
  const names = [...filedNames, ...stay];
  const ws = await manualReview(request, names, 'Receipt totals', false, ownBytes(names));
  const console = await openReview(page, ws.slug);
  const folderName = basename(ws.root);
  // The picture's count is the one the button states before the press.
  await expect(fileButton(console)).toHaveText(filesLabel(4));
  await fileButton(console).click();
  const receipt = console.locator('#fileJanitorResultsHost .fj-receipt');
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('4 files filed.', {
    timeout: 60_000
  });
  // The same number is what is now under Filed on disk, and what the journal holds.
  const onDisk = filesUnder(join(ws.root, 'Filed'));
  expect(onDisk).toHaveLength(4);
  const history = await (await request.get(`${ws.base}/history?limit=100`)).json();
  expect(history.total).toBe(4);

  // Where they are, and how many stay loose: the files still in the folder.
  const loose = looseFiles(ws.root);
  expect(loose).toEqual([...stay].sort());
  await expect(receipt.locator('.fj-receipt-where')).toHaveText(
    `They are in ${folderName}/Filed. ${loose.length} files stay loose in ${folderName}.`
  );

  // One row per destination folder that exists on disk, each with its own count.
  const folders = destinations(ws.root).sort();
  expect(folders).toEqual(['Images', 'Installers']);
  await expect(receiptRows(console)).toHaveCount(folders.length);
  for (const dir of folders) {
    const count = filesUnder(join(ws.root, 'Filed', dir)).length;
    await expect(receiptRows(console).filter({ hasText: `Filed/${dir}` })).toHaveText(
      `${count} → Filed/${dir}`
    );
  }

  // The server's own receipt agrees with what the page shows.
  const listed = (await (await request.get(`${ws.base}/filings?limit=1`)).json()).filings;
  expect(listed).toHaveLength(1);
  expect(listed[0]).toMatchObject({ filed: 4, trashed: 0, undo_available: true, undone: false });
  const served = (await (await request.get(`${ws.base}/filings/${listed[0].filing_id}`)).json())
    .receipt;
  expect(served).toMatchObject({ filed: 4, trashed: 0, not_moved_total: 0, loose_now: 2 });
  expect(served.by_destination).toHaveLength(2);

  // No file-by-file list and no second screen: Undo all and Done, nothing else.
  await expect(receipt.locator('.fj-results-item')).toHaveCount(0);
  await expect(receipt.locator('.fj-receipt-notmoved')).toHaveCount(0);
  await expect(receipt.getByRole('button')).toHaveText(['Undo all', 'Done']);
  await expect(receipt.getByRole('button', { name: 'View History' })).toHaveCount(0);
  await expect(receipt).toBeFocused();
  mkdirSync(evidence, { recursive: true });
  await receipt.evaluate(node => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: `${evidence}/05-e2e-receipt.png`, fullPage: true });
});

test('Undo all puts every file back under its own name with its own bytes, and Ori scans again only on the button', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const contents = ownBytes(filedNames);
  const ws = await manualReview(request, filedNames, 'Undo all', false, contents);
  const firstBatch = (await pictureOf(request, ws.base)).batch_id;
  const console = await openReview(page, ws.slug);
  const sent = posts(page);
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('4 files filed.', {
    timeout: 60_000
  });
  for (const name of filedNames) expect(existsSync(join(ws.root, name)), name).toBe(false);
  const batchesBefore = (await (await request.get(`${ws.base}/batches`)).json()).batches.length;

  await undoAllButton(console).click();
  await expect(undoneTitle(console)).toHaveText('4 files are back where they were.', {
    timeout: 60_000
  });
  // Every file is back in the folder, with the bytes it had; none is left under Filed.
  for (const name of filedNames) {
    expect(existsSync(join(ws.root, name)), name).toBe(true);
    expect(readFileSync(join(ws.root, name), 'utf8'), name).toBe(contents[name]);
  }
  expect(filesUnder(join(ws.root, 'Filed'))).toEqual([]);
  // One request for all of it, and the receipt no longer offers it.
  expect(sent.filter(path => /^filings\/[^/]+\/undo$/.test(path))).toHaveLength(1);
  await expect(undoAllButton(console)).toHaveCount(0);
  await expect(console.locator('.fj-receipt-notmoved')).toHaveCount(0);
  const listed = (await (await request.get(`${ws.base}/filings?limit=1`)).json()).filings;
  expect(listed[0]).toMatchObject({ undone: true, undo_available: false });
  await page.screenshot({ path: `${evidence}/05-e2e-undone.png`, fullPage: true });

  // Putting files back never scans: no scan request, no new review, until the press.
  await page.waitForTimeout(1500);
  expect(sent.filter(path => path === 'scan')).toEqual([]);
  expect((await (await request.get(`${ws.base}/batches`)).json()).batches).toHaveLength(
    batchesBefore
  );
  // Nothing is waiting to be reviewed: every file of the old review was filed.
  expect(drawn(await pictureOf(request, ws.base))).toBe(0);

  // The one button forgets that Ori put these back, then runs the ordinary scan.
  await console.locator('#fileJanitorScanAgain').click();
  await expect(fileButton(console)).toHaveText(filesLabel(4), { timeout: 60_000 });
  const order = sent.filter(path => path === 'scan' || /\/forget-restored$/.test(path));
  expect(order).toHaveLength(2);
  expect(order[0]).toMatch(/^filings\/[^/]+\/forget-restored$/);
  expect(order[1]).toBe('scan');
  await expect(console.locator('#fileJanitorUndone')).toHaveCount(0);
  // The restored files are in a new review, ticked and ready to file again.
  const again = await pictureOf(request, ws.base);
  expect(again.batch_id).not.toBe(firstBatch);
  expect(drawn(again)).toBe(4);
  for (const name of filedNames) expect(existsSync(join(ws.root, name)), name).toBe(true);
});

test('with one original name taken, Undo all lists that file with the reason and where it is, and puts the rest back', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const contents = ownBytes(filedNames);
  const ws = await manualReview(request, filedNames, 'Undo all, name taken', false, contents);
  const console = await openReview(page, ws.slug);
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('4 files filed.', {
    timeout: 60_000
  });
  // Something new takes the original name after the file was filed.
  const newcomer = 'A different file that now has this name';
  writeFileSync(join(ws.root, 'b.png'), newcomer, { mode: 0o600 });

  await undoAllButton(console).click();
  await expect(undoneTitle(console)).toHaveText('3 files are back where they were.', {
    timeout: 60_000
  });
  const notBack = console.locator('#fileJanitorUndone .fj-receipt-notmoved');
  await expect(notBack.locator('.fj-receipt-notmoved-title')).toHaveText(
    '1 file could not be put back'
  );
  await expect(notBack.locator('.fj-results-item')).toHaveCount(1);
  await expect(notBack.locator('.fj-results-item')).toContainText('b.png');
  await expect(notBack.locator('.fj-results-item')).toContainText(
    /already using the original name/
  );
  await expect(notBack.locator('.fj-results-item')).toContainText(
    'It is still in Filed/Images/b.png.'
  );
  // The rest are back, with their own bytes; the newcomer is untouched; the filed
  // copy stays where it is rather than overwriting anything.
  for (const name of ['a.png', 'c.png', 'setup.dmg']) {
    expect(readFileSync(join(ws.root, name), 'utf8'), name).toBe(contents[name]);
  }
  expect(readFileSync(join(ws.root, 'b.png'), 'utf8')).toBe(newcomer);
  expect(readFileSync(join(ws.root, 'Filed', 'Images', 'b.png'), 'utf8')).toBe(contents['b.png']);
  expect(filesUnder(join(ws.root, 'Filed'))).toEqual(['Images/b.png']);
  // The files that did come back can be reviewed again.
  await expect(console.locator('#fileJanitorScanAgain')).toBeVisible();
  await notBack.evaluate(node => node.scrollIntoView({ block: 'center' }));
  await page.screenshot({ path: `${evidence}/05-e2e-undo-partial.png`, fullPage: true });
});

test('Undo all from History works after the receipt was closed, and the line then says the files were put back', async ({
  page,
  request
}) => {
  await page.setViewportSize({ width: 1440, height: 1100 });
  const contents = ownBytes(filedNames);
  const ws = await manualReview(request, filedNames, 'Undo all from History', false, contents);
  const console = await openReview(page, ws.slug);
  await fileButton(console).click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('4 files filed.', {
    timeout: 60_000
  });
  await console.locator('#fileJanitorReceiptDone').click();
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveCount(0);

  // History: one line for the last filing above the per-file list.
  await console.getByRole('tab', { name: 'History', exact: true }).click();
  const line = console.locator('#fileJanitorLastFiling');
  await expect(line).toContainText('Last filing · 4 files');
  await expect(console.locator('.fj-history-item')).toHaveCount(4);
  const lineBox = await line.boundingBox();
  const listBox = await console.locator('.fj-history-list').boundingBox();
  expect(lineBox!.y).toBeLessThan(listBox!.y);
  await page.screenshot({ path: `${evidence}/05-e2e-history-line.png`, fullPage: true });

  const sent = posts(page);
  await console.locator('#fileJanitorLastFilingUndo').click();
  // The answer is shown on the Review tab, with the files back on disk.
  await expect(undoneTitle(console)).toHaveText('4 files are back where they were.', {
    timeout: 60_000
  });
  await expect(console.getByRole('tab', { name: 'Review', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  for (const name of filedNames) {
    expect(readFileSync(join(ws.root, name), 'utf8'), name).toBe(contents[name]);
  }
  expect(filesUnder(join(ws.root, 'Filed'))).toEqual([]);
  expect(sent.filter(path => /^filings\/[^/]+\/undo$/.test(path))).toHaveLength(1);
  expect(sent.filter(path => path === 'scan')).toEqual([]);

  await console.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(console.locator('#fileJanitorLastFiling')).toContainText('put back');
  await expect(console.locator('#fileJanitorLastFilingUndo')).toHaveCount(0);
});

test('a file sent to the Trash is counted apart on the receipt, and Undo all brings it back from the Trash', async ({
  page,
  request
}) => {
  const trashDir = sandboxTrash();
  test.skip(
    !trashDir,
    'NOT RUN: the runner did not pass the server sandbox (use e2e-fresh.sh --sandbox-env ORI_E2E_SANDBOX), so the isolated Trash cannot be located and the real one is never touched'
  );
  await page.setViewportSize({ width: 1440, height: 1200 });
  const ws = await manualReview(
    request,
    pairFiles.slice(0, 2),
    'Receipt Trash and Undo all',
    false,
    pairContents
  );
  const console = await openReview(page, ws.slug);
  await pairCard(console, 'Budget.pdf')
    .getByRole('button', { name: 'Trash the copy Budget (2).pdf', exact: true })
    .click();
  await expect(fileButton(console)).toHaveText(filesLabel(0, 1));
  await console.locator('#fileJanitorTrashAck').check();
  await expect(fileButton(console)).toBeEnabled();
  await fileButton(console).click();
  // Counted apart from "filed", with its own row, and no destination folder made.
  await expect(console.locator('#fileJanitorResultsSummary')).toHaveText('1 moved to Trash.', {
    timeout: 60_000
  });
  await expect(receiptRows(console)).toHaveCount(1);
  await expect(receiptRows(console)).toHaveText('1 → Trash');
  await expect(console.locator('#fileJanitorResultsHost .fj-receipt-where')).not.toContainText(
    'They are in'
  );
  expect(destinations(ws.root)).toEqual([]);
  expect(existsSync(join(ws.root, 'Budget (2).pdf'))).toBe(false);
  // Other cases in this run send a file of the same name to the same sandbox Trash
  // and leave it there, so this case counts only what it added.
  const trashed = () =>
    (existsSync(trashDir!) ? readdirSync(trashDir!) : []).filter(name =>
      name.startsWith('Budget (2)')
    );
  const inTrashNow = trashed().length;
  expect(inTrashNow).toBeGreaterThanOrEqual(1);

  await undoAllButton(console).click();
  await expect(undoneTitle(console)).toHaveText('1 file is back where it was.', {
    timeout: 60_000
  });
  expect(readFileSync(join(ws.root, 'Budget (2).pdf'), 'utf8')).toBe(
    pairContents['Budget (2).pdf']
  );
  expect(readFileSync(join(ws.root, 'Budget.pdf'), 'utf8')).toBe(pairContents['Budget.pdf']);
  // Exactly the one file this case sent to the Trash left it.
  expect(trashed()).toHaveLength(inTrashNow - 1);
});
