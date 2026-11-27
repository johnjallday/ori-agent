import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
  unlinkSync,
  writeFileSync
} from 'node:fs';
import { dirname, join, sep } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Run against wt demo with ORI_SANDBOX set to its printed disposable root.
const evidence = 'tasks/evidence-workspace-files-view';
const row = (page: Page, name: string) => page.getByRole('treeitem', { name, exact: true });
const reader = (page: Page) => page.locator('#workspace-files-view');
const created: string[] = [];
test.beforeEach(async ({ page }) => {
  expect(process.env.ORI_SANDBOX, 'provide the disposable demo sandbox').toBeTruthy();
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
});
test.afterEach(async ({ request }) => {
  while (created.length) await request.delete(`/api/workspaces/${created.pop()}?confirm=true`);
});

async function workspace(page: Page, name: string, parent_id = '', group = false) {
  let data: Record<string, unknown> = {
    name,
    parent_id,
    blank: true,
    create_template_agents: true
  };
  if (group) {
    const plan = await (
      await page.request.post('/api/workspaces/template-agent-plan', {
        data: { group_roster: true, group_name: name }
      })
    ).json();
    const { name: manager, action } = plan.agents[0];
    data = {
      name,
      parent_id,
      kind: 'group',
      group_roster: true,
      create_template_agents: true,
      template_agent_review: {
        version: 1,
        plan_revision: plan.revision,
        expectations: [{ index: 0, name: manager, action }]
      }
    };
  }
  data.folder_slug = `reader-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
  const res = await page.request.post('/api/workspaces', { data });
  expect(res.ok(), await res.text()).toBe(true);
  const { folder } = await res.json();
  created.push(folder.id);
  return folder;
}
async function files(page: Page, id: string, folder = '') {
  const { output_dir } = await (await page.request.get(`/api/workspaces/${id}/output-dir`)).json();
  const root = realpathSync(process.env.ORI_SANDBOX!);
  const workspaceDir = realpathSync(dirname(output_dir));
  expect(workspaceDir.startsWith(root + sep)).toBe(true);
  const target = join(workspaceDir, 'files', folder);
  mkdirSync(target, { recursive: true });
  expect(realpathSync(target).startsWith(root + sep)).toBe(true);
  return target;
}
async function shot(page: Page, name: string) {
  await page.screenshot({ path: join(evidence, `${name}.png`), animations: 'disabled' });
}

const linkedEvidence = 'tasks/evidence-workspace-linked-project-files';

async function linkedFixture(page: Page, owner: string, name: string, path: string) {
  const res = await page.request.post(`/api/workspaces/${owner}/directories`, {
    data: { name, path }
  });
  expect(res.ok(), await res.text()).toBe(true);
  return (await res.json()).directory.id as string;
}

const keyed = (page: Page, key: string) => reader(page).locator(`[data-key="${key}"]`);
const mode = (page: Page, name: string) => page.locator(`[data-cmd-view-mode="${name}"]`);
async function ready(page: Page) {
  await page.waitForFunction(() => !!(window as any).workspaceDetail?.commanderPresenter);
}

async function musicGroup(page: Page) {
  const home = await workspace(page, 'Music Production Home', '', true);
  const demo = await workspace(page, 'Ori Music Demo', home.id, true);
  const albums = [];
  for (const n of [4, 5]) {
    const album = await workspace(page, `ALBUM-${n}`, demo.id);
    albums.push(album);
    writeFileSync(
      join(await files(page, album.id), 'Mix notes.md'),
      `# Listening notes\n\n${n === 4 ? 'Lift the chorus.' : 'Watch the bass.'}`
    );
  }
  return { home, demo, albums };
}

for (const phone of [false, true])
  test(`a group lists its fed folder once and preserves nested link ownership (${phone ? 'phone, dark' : 'desktop, light'})`, async ({
    page
  }) => {
    test.setTimeout(60000);
    await page.setViewportSize(phone ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
    await page.addInitScript(
      theme => localStorage.setItem('ori-theme', theme),
      phone ? 'dark' : 'light'
    );
    const home = await workspace(page, 'Music Production Home', '', true);
    const demo = await workspace(page, 'Ori Music Demo', home.id, true);
    const track = await workspace(page, 'Track-2', demo.id);
    await files(page, track.id); // verify this server belongs to the supplied sandbox
    const fixtures = mkdtempSync(join(realpathSync(process.env.ORI_SANDBOX!), 'linked-'));
    execFileSync('./scripts/folder-agent-fixtures.sh', [fixtures]);
    const project = join(fixtures, 'Ori Music Demo', 'Picker Songs', track.name);
    const ref = await linkedFixture(page, track.id, 'Project', project);
    const sibling = await workspace(page, 'Track-1', demo.id);
    const siblingRef = await linkedFixture(
      page,
      sibling.id,
      'Project',
      join(dirname(project), 'Track-1')
    );
    await linkedFixture(page, demo.id, 'Picker Songs', dirname(project));
    const absent = join(fixtures, 'Filed');
    mkdirSync(absent);
    const missing = await linkedFixture(page, track.id, 'Archive', absent);
    renameSync(absent, `${absent}-offline`);
    await page.goto(`/workspaces/${home.folder_slug}?mode=map`);
    await ready(page);
    const viewport = page.locator('[data-ws-map-viewport]');
    await viewport.press('+');
    await viewport.press('ArrowRight');
    const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
    const moved = await camera();
    await mode(page, 'files').click();
    const reads: string[] = [];
    page.on('request', r => {
      const path = new URL(r.url()).pathname;
      if (
        path.includes('/directories/') ||
        (path.endsWith('/open') && !path.endsWith('/lead-conversation/open'))
      )
        reads.push(`${r.method()} ${path}`);
    });
    await expect(row(page, 'Folders on this Mac')).toBeVisible();
    await expect(row(page, 'Stored in Ori')).toBeVisible();
    await expect(row(page, 'Project Linked folder')).toHaveCount(0);
    await row(page, sibling.name).press('Enter');
    await keyed(page, `${sibling.id}/lf/${siblingRef}/Song.rpp`).press('Enter');
    await expect(reader(page).locator('.wf-crumb')).toHaveText(
      'Track-1 / Files / Project / Song.rpp'
    );
    if (phone) await reader(page).getByRole('button', { name: 'Back to files' }).press('Enter');
    await row(page, sibling.name).click();
    await row(page, track.name).click();
    await keyed(page, `${track.id}/lf/${ref}/Song.rpp`).press('Enter');
    await expect(reader(page).locator('.wf-crumb')).toHaveText(
      'Track-2 / Files / Project / Song.rpp'
    );
    await expect(page.locator('.ws-cmd-title h2')).toHaveText(home.name);
    await page.screenshot({
      path: join(linkedEvidence, phone ? 'track-2-phone.png' : 'track-2.png')
    });
    if (phone) {
      await reader(page).getByRole('button', { name: 'Back to files' }).press('Enter');
      await expect(row(page, 'Song.rpp')).toBeFocused();
    }
    await expect(row(page, 'Archive Linked folder')).toHaveAttribute('aria-expanded', 'true');
    await expect(row(page, 'Folder unavailable. Retry')).toBeVisible();
    await row(page, 'Folder unavailable. Retry').scrollIntoViewIfNeeded();
    await page.screenshot({
      path: join(linkedEvidence, phone ? 'unavailable-phone.png' : 'unavailable.png')
    });
    expect(reads).toContain(`GET /api/workspaces/${track.id}/directories/${ref}/entries`);
    expect(reads).not.toContain(`POST /api/workspaces/${track.id}/directories/${missing}/open`);
    await mode(page, 'map').click();
    await expect(viewport).toBeVisible();
    expect(await camera()).toEqual(moved);
  });

test('a linked Python file previews, opens in an editor, and moves to Trash only after confirmation', async ({
  page
}) => {
  const ws = await workspace(page, 'Downloads');
  await files(page, ws.id); // verify this server is isolated before touching files
  const root = mkdtempSync(join(realpathSync(process.env.ORI_SANDBOX!), 'downloads-'));
  const folder = join(root, 'example-project');
  mkdirSync(folder);
  const name = `cards-${randomUUID()}.py`;
  const code = 'print("<b>not markup</b>")';
  writeFileSync(join(folder, name), code, { mode: 0o700 });
  writeFileSync(join(folder, 'keep.txt'), 'still here');
  const ref = await linkedFixture(page, ws.id, 'Downloads', root);
  const listing = await (
    await page.request.get(
      `/api/workspaces/${ws.id}/directories/${ref}/entries?path=example-project`
    )
  ).json();
  expect(listing.desktop_disabled).toBe(true);
  test.skip(process.platform !== 'darwin', 'native file actions require the local Mac');
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await ready(page);
  await row(page, 'example-project').click();
  await row(page, name).press('Enter');
  await expect(reader(page).locator('pre')).toHaveText(code);
  await reader(page).getByRole('button', { name: 'Open in editor' }).click();
  await expect(reader(page)).toContainText('No application was opened.');
  const trash = reader(page).getByRole('button', { name: 'Move to Trash…' });
  page.once('dialog', async dialog => {
    await dialog.dismiss();
  });
  await trash.click();
  expect(readFileSync(join(folder, name), 'utf8')).toBe(code);
  let confirmation = '';
  page.once('dialog', async dialog => {
    confirmation = dialog.message();
    await dialog.accept();
  });
  await trash.click();
  await expect(row(page, name)).toHaveCount(0);
  await expect(row(page, 'keep.txt')).toBeVisible();
  expect(confirmation).toContain(`example-project/${name}`);
  expect(readdirSync(folder)).toEqual(['keep.txt']);
  const sandbox = realpathSync(process.env.ORI_SANDBOX!);
  expect(
    readFileSync(
      join(
        process.env.ORI_DEMO_SPLIT_PROFILE === '1' ? join(sandbox, 'home') : sandbox,
        '.Trash',
        name
      ),
      'utf8'
    )
  ).toBe(code);
});

test('Trash explains which permission to check, without retrying the move', async ({ page }) => {
  test.skip(
    process.platform !== 'darwin' || process.getuid?.() === 0,
    'requires unprivileged Mac permissions'
  );
  const ws = await workspace(page, 'Downloads');
  await files(page, ws.id);
  const root = mkdtempSync(join(realpathSync(process.env.ORI_SANDBOX!), 'denied-'));
  const name = 'test.txt';
  writeFileSync(join(root, name), 'keep');
  await linkedFixture(page, ws.id, 'Downloads', root);
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await ready(page);
  await row(page, name).press('Enter');
  const preview = reader(page).getByRole('article');
  const trash = preview.getByRole('button', { name: 'Move to Trash…' });
  const attempt = async () => {
    page.once('dialog', d => d.accept());
    await trash.click();
  };
  chmodSync(root, 0o500);
  try {
    await attempt();
    const settings = preview.getByRole('link', { name: 'Open Files & Folders settings' });
    await expect(settings).toHaveAttribute('href', /Privacy_FilesAndFolders$/);
    await expect(preview).toContainText('Downloads Folder');
  } finally {
    chmodSync(root, 0o700);
  }
  await page.route('**/trash-file', r =>
    r.fulfill({
      status: 403,
      json: {
        code: 'trash_directory_denied',
        message: 'macOS denied access to Trash. This item was not moved.'
      }
    })
  );
  await attempt();
  await expect(preview.getByRole('link', { name: 'Open Files & Folders settings' })).toHaveCount(0);
  await expect(preview).toContainText('select it in Finder');
  await preview.getByText('Allow Ori to use Trash', { exact: true }).click();
  await expect(
    preview.getByRole('link', { name: 'Open Full Disk Access settings' })
  ).toHaveAttribute('href', /Privacy_AllFiles$/);
  expect(readFileSync(join(root, name), 'utf8')).toBe('keep');
  await page.unroute('**/trash-file');
});

test('Contents supports right-click, individual picks and a range without including a stale file', async ({
  page
}) => {
  test.skip(process.platform !== 'darwin', 'native file actions require the local Mac');
  const ws = await workspace(page, 'Downloads');
  await files(page, ws.id);
  const folder = mkdtempSync(join(realpathSync(process.env.ORI_SANDBOX!), 'multi-'));
  for (const n of ['a', 'b', 'c', 'd']) writeFileSync(join(folder, `${n}.txt`), n);
  const ref = await linkedFixture(page, ws.id, 'Downloads', folder);
  const posted: string[] = [];
  page.on('request', r => {
    if (r.url().endsWith(`/directories/${ref}/trash-file`)) posted.push(r.postDataJSON().path);
  });
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await ready(page);
  const a = row(page, 'a.txt'),
    b = row(page, 'b.txt'),
    c = row(page, 'c.txt'),
    d = row(page, 'd.txt');
  const count = () => reader(page).locator('[aria-selected=true]').count();
  await a.click();
  await c.click({ modifiers: ['Meta'] });
  expect(await count()).toBe(2);
  await a.click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Move 2 files to Trash…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Move 2 files to Trash?' });
  await expect(dialog.locator('li')).toHaveText(['a.txt', 'c.txt']);
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(posted).toEqual([]);
  // A right-click outside the current selection acts only on the clicked row.
  await d.click({ button: 'right' });
  page.once('dialog', async dialog => {
    await dialog.accept();
  });
  await page.getByRole('menuitem', { name: 'Move to Trash…', exact: true }).click();
  await expect(d).toHaveCount(0);
  expect(posted).toEqual(['d.txt']);
  await a.click();
  await c.click({ modifiers: ['Shift'] });
  await expect(b).toHaveAttribute('aria-selected', 'true');
  expect(await count()).toBe(3);
  await c.press('Delete');
  const range = page.getByRole('dialog', { name: 'Move 3 files to Trash?' });
  await expect(range.locator('li')).toHaveText(['a.txt', 'b.txt', 'c.txt']);
  writeFileSync(join(folder, 'b.txt'), 'replacement bytes');
  await range.getByRole('button', { name: 'Move to Trash', exact: true }).click();
  await expect(reader(page).getByRole('status')).toContainText(
    '1 of 3 confirmed moved. Stopped at b.txt'
  );
  expect(posted).toEqual(['d.txt', 'a.txt', 'b.txt']);
  expect(readdirSync(folder).sort()).toEqual(['b.txt', 'c.txt']);
  expect(readFileSync(join(folder, 'b.txt'), 'utf8')).toBe('replacement bytes');
  // New listings require a new selection, never silently upgrade a token.
  await reader(page).getByRole('button', { name: 'Refresh folder' }).click();
  expect(await count()).toBe(0);
  await b.click();
  await c.click({ modifiers: ['Meta'] });
  await c.press('Shift+F10');
  await page.getByRole('menuitem', { name: 'Move 2 files to Trash…' }).click();
  await dialog.getByRole('button', { name: 'Move to Trash', exact: true }).click();
  await expect(reader(page)).toContainText('2 files moved to Trash.');
  expect(readdirSync(folder)).toEqual([]);
});

test('color and move linked folders and files in Contents, then trash a whole folder', async ({
  page
}) => {
  test.skip(process.platform !== 'darwin', 'native file actions require the local Mac');
  const ws = await workspace(page, 'Downloads');
  await files(page, ws.id);
  const sandbox = realpathSync(process.env.ORI_SANDBOX!);
  const root = mkdtempSync(join(sandbox, 'arrange-'));
  const src = `Cards-${randomUUID().slice(0, 8)}`,
    dst = 'Projects';
  mkdirSync(join(root, src));
  mkdirSync(join(root, dst));
  writeFileSync(join(root, src, 'notes.txt'), 'keep this');
  writeFileSync(join(root, 'cover.txt'), 'cover');
  await linkedFixture(page, ws.id, 'Downloads', root);
  const visit = async () => {
    await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
    await ready(page);
    await expect(row(page, 'Downloads Linked folder')).toHaveAttribute('aria-expanded', 'true');
  };
  await visit();
  await row(page, src).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Folder color…' }).click();
  await page
    .getByRole('dialog', { name: `Folder color: ${src}` })
    .getByRole('button', { name: 'Purple' })
    .click();
  await expect(row(page, src)).toHaveAttribute('data-folder-color', 'Purple');
  await visit();
  await expect(row(page, src)).toHaveAttribute('data-folder-color', 'Purple');
  // A drag is reviewed. Cancel first, then move a folder and a sibling file together.
  await row(page, src).dragTo(row(page, dst));
  await page
    .getByRole('dialog', { name: 'Move 1 folder' })
    .getByRole('button', { name: 'Cancel' })
    .click();
  expect(readdirSync(join(root, dst))).toEqual([]);
  await row(page, src).click();
  await row(page, 'cover.txt').click({ modifiers: ['Meta'] });
  await row(page, src).dragTo(row(page, dst));
  const review = page.getByRole('dialog', { name: 'Move 2 items' });
  await expect(review.locator('li')).toHaveText([`${src}/`, 'cover.txt']);
  await expect(review.getByRole('combobox')).toHaveValue(/\d+/);
  await review.getByRole('button', { name: 'Move here' }).click();
  await expect(reader(page)).toContainText('2 items moved to Downloads / Projects.');
  expect(readdirSync(root)).toEqual([dst]);
  await row(page, dst).click();
  await expect(row(page, src)).toHaveAttribute('data-folder-color', 'Purple');
  await row(page, 'cover.txt').click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Move to…' }).click();
  const picker = page.getByRole('dialog', { name: 'Move 1 file' });
  await picker.getByRole('combobox').selectOption({ label: 'Downloads' });
  await picker.getByRole('button', { name: 'Move here' }).click();
  await expect(reader(page)).toContainText('cover.txt moved to Downloads.');
  expect(readFileSync(join(root, 'cover.txt'), 'utf8')).toBe('cover');
  await row(page, src).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Folder color…' }).click();
  await page.getByRole('button', { name: 'Clear color' }).click();
  await expect(row(page, src)).not.toHaveAttribute('data-folder-color', 'Purple');
  // The linked root can be colored, never trashed.
  await row(page, 'Downloads Linked folder').click({ button: 'right' });
  await expect(page.getByRole('menuitem', { name: 'Move to Trash…' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await row(page, src).click({ button: 'right' });
  let warning = '';
  page.once('dialog', async d => {
    warning = d.message();
    await d.accept();
  });
  await page.getByRole('menuitem', { name: 'Move to Trash…' }).click();
  await expect(row(page, src)).toHaveCount(0);
  expect(warning).toContain('all of its contents');
  expect(
    readFileSync(
      join(
        process.env.ORI_DEMO_SPLIT_PROFILE === '1' ? join(sandbox, 'home') : sandbox,
        '.Trash',
        src,
        'notes.txt'
      ),
      'utf8'
    )
  ).toBe('keep this');
});

test('a linked alternate opens only by explicit sandbox dispatch; disconnect refuses replay', async ({
  page
}) => {
  const sandbox = realpathSync(process.env.ORI_SANDBOX!);
  const sidecar = mkdtempSync(join(sandbox, 'reader-export-'));
  const project = mkdtempSync(join(sandbox, 'reader-project-'));
  const id = randomUUID(),
    ref = randomUUID(),
    slug = `project-${id.slice(0, 8)}`;
  const now = new Date().toISOString();
  const locator = {
    schema_version: 1,
    kind: 'directory_reference',
    directory_reference_id: ref,
    relative_path: 'Song.rpp'
  };
  const originals = ['Song.rpp', 'Alternate.rpp'];
  const content = (name: string) => `<REAPER_PROJECT 0.1 "7.0" 0\n  NOTES 0 0\n  |${name}\n>\n`;
  for (const name of originals) writeFileSync(join(project, name), content(name), { mode: 0o600 });
  writeFileSync(
    join(sidecar, 'workspace.json'),
    JSON.stringify({
      id,
      name: 'Album sessions',
      folder_slug: slug,
      status: 'active',
      created_at: now,
      updated_at: now,
      directory_references: [
        {
          id: ref,
          name: 'Session originals',
          workspace_id: id,
          path: project,
          created_at: now,
          updated_at: now
        }
      ],
      shared_data: { project_entry: locator }
    }),
    { mode: 0o600 }
  );
  const imported = await page.request.post('/api/workspaces/import', { data: { path: sidecar } });
  expect(imported.ok(), await imported.text()).toBe(true);
  created.push(id);
  const owned = await files(page, id);
  writeFileSync(join(owned, 'Credits.txt'), 'Credits');
  const api = `/api/workspaces/${id}/directories/${ref}`;
  const listed = await (await page.request.get(`${api}/entries`)).json();
  expect(
    listed.desktop_disabled,
    'refuse to dispatch unless desktop launches are disabled in the server'
  ).toBe(true);
  test.skip(
    !listed.entries.find((entry: { path: string }) => entry.path === 'Alternate.rpp')?.selection,
    'native open is Mac-only'
  );
  await page.goto(`/workspaces/${slug}?mode=files`);
  await page
    .getByRole('dialog', { name: 'Hire a Commander for this workspace?' })
    .getByRole('button', { name: 'Not now' })
    .click();
  const sent: string[] = [];
  page.on('request', r => {
    if (r.url().endsWith(`${api}/open`)) sent.push(r.postDataJSON().path);
  });
  await expect(row(page, 'Stored in Ori')).toBeVisible();
  await row(page, 'Song.rpp').click();
  await row(page, 'Alternate.rpp').press('Enter');
  const open = reader(page).getByRole('button', { name: 'Open in default app' });
  await expect(open).toBeEnabled();
  expect(sent).toEqual([]);
  await open.click();
  await expect(reader(page)).toContainText('Sandbox dispatch accepted. No application was opened.');
  expect(sent).toEqual(['Alternate.rpp']);
  await page.screenshot({ path: join(linkedEvidence, 'alternate-dispatch.png') });
  const persisted = JSON.parse(readFileSync(join(dirname(owned), 'workspace.json'), 'utf8'));
  expect(persisted.shared_data.project_entry).toEqual(locator);
  // Disconnect after the user clicks but before the queued POST reaches the host.
  await page.route(
    `**${api}/open`,
    async route => {
      expect((await page.request.delete(api)).ok()).toBe(true);
      await route.continue();
    },
    { times: 1 }
  );
  const refused = page.waitForResponse(r => r.url().endsWith(`${api}/open`));
  await open.click();
  expect((await refused).status()).toBe(404);
  await expect
    .poll(async () => await reader(page).innerText())
    .toMatch(/Linked folder unavailable|Select a file or note/);
  await page.screenshot({ path: join(linkedEvidence, 'disconnected.png') });
  expect(readdirSync(owned)).toEqual(['Credits.txt']);
  expect(readdirSync(project).sort()).toEqual([...originals].sort());
  for (const name of originals)
    expect(readFileSync(join(project, name), 'utf8')).toBe(content(name));
});

test('group keeps its identity while reading same-named files in linked albums', async ({
  page
}) => {
  const { home, demo, albums } = await musicGroup(page);
  const outsider = await workspace(page, 'Not a linked project');
  writeFileSync(join(await files(page, home.id), 'Project index.txt'), 'Album credits');
  await page.goto(`/workspaces/${home.folder_slug}?mode=files`);
  await ready(page);
  const seen: string[] = [];
  page.on('request', r => {
    const p = decodeURIComponent(new URL(r.url()).pathname);
    if (
      p.startsWith('/api/') &&
      (r.method() !== 'GET' || /\/files(?:\/|$)|\/notes(?:\/|$)|\/directories|\/outputs/.test(p))
    )
      seen.push(`${r.method()} ${p}`);
  });
  await keyed(page, `${home.id}/s/files`).click();
  await expect(row(page, 'Project index.txt')).toBeVisible();
  await row(page, 'Linked workspaces').click();
  await row(page, demo.name).click();
  await expect(row(page, outsider.name)).toHaveCount(0);
  const expected = [`GET /api/workspaces/${home.id}/files/tree`];
  for (const [i, album] of albums.entries()) {
    await keyed(page, album.id).click();
    await keyed(page, `${album.id}/s/files`).click();
    await keyed(page, `${album.id}/f/Mix notes.md`).click();
    await expect(reader(page).locator('.wf-crumb')).toHaveText(
      `${album.name} / Files / Mix notes.md`
    );
    await expect(reader(page).locator('.wf-body')).toContainText(
      i ? 'Watch the bass.' : 'Lift the chorus.'
    );
    await expect(page.locator('.ws-cmd-title h2')).toHaveText(home.name);
    await shot(page, `group-album-${i + 4}`);
    expected.push(
      `GET /api/workspaces/${album.id}/files/tree`,
      `GET /api/workspaces/${album.id}/files/Mix notes.md`
    );
  }
  expect(seen).toEqual(expected);
});

test('the scoped map keeps its panned center and zoom across Files', async ({ page }) => {
  const { home, demo, albums } = await musicGroup(page);
  await page.goto(`/workspaces/${home.folder_slug}?mode=map`);
  await ready(page);
  const viewport = page.locator('[data-ws-map-viewport]');
  await expect(viewport).toBeVisible();
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  const writes: string[] = [];
  page.on('request', r => {
    // The existing assistant independently enrolls the page's lead conversation.
    // It is not a Files action; still reject every file, member and layout write.
    if (
      r.method() !== 'GET' &&
      r.url().includes('/api/') &&
      !r.url().endsWith('/lead-conversation/open')
    )
      writes.push(r.url());
  });
  const initial = await camera();
  await viewport.press('+');
  await viewport.press('ArrowRight');
  const moved = await camera();
  expect(moved.zoom).not.toBe(initial.zoom);
  expect(moved.centerX).not.toBe(initial.centerX);
  await shot(page, 'map-before');
  await mode(page, 'files').click();
  await row(page, 'Linked workspaces').click();
  await row(page, demo.name).click();
  await row(page, albums[0].name).click();
  await keyed(page, `${albums[0].id}/s/files`).click();
  await row(page, 'Mix notes.md').click();
  await expect(reader(page)).toContainText('Lift the chorus.');
  await mode(page, 'map').click();
  await expect(viewport).toBeVisible();
  expect(await camera()).toEqual(moved);
  await shot(page, 'map-after');
  await page.goBack();
  await expect(mode(page, 'files')).toHaveAttribute('aria-pressed', 'true');
  await expect(reader(page)).toContainText('Lift the chorus.');
  await expect(row(page, 'Mix notes.md')).toHaveAttribute('aria-selected', 'true');
  await shot(page, 'reopened');
  await page.goForward();
  await expect(viewport).toBeVisible();
  expect(await camera()).toEqual(moved);
  await expect(reader(page).locator('[role=tree]')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('phone returns to a keyboard-selected row, then reconciles a removed file', async ({
  page
}) => {
  const ws = await workspace(page, 'Production journal');
  const dir = await files(page, ws.id, 'Sessions');
  for (let i = 0; i < 110; i++) writeFileSync(join(dir, `Take ${i}.txt`), `Session ${i}`);
  await page.setViewportSize({ width: 375, height: 812 });
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await ready(page);
  await row(page, ws.name).focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowRight');
  await row(page, 'Sessions').waitFor();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  const last = row(page, 'Take 109.txt');
  await last.waitFor();
  await row(page, 'Sessions').press('End');
  await page.keyboard.press('ArrowUp');
  await expect(last).toBeFocused();
  const scroll = await reader(page)
    .locator('.wf-tree')
    .evaluate(el => el.scrollTop);
  expect(scroll).toBeGreaterThan(0);
  await page.keyboard.press('Enter');
  await expect(reader(page).locator('.wf-body')).toHaveText('Session 109');
  await expect(reader(page).locator('.wf-preview h2')).toBeFocused();
  await expect(last).toBeHidden();
  await shot(page, 'phone-preview');
  await page.setViewportSize({ width: 1280, height: 800 });
  await expect(last).toBeVisible();
  await page.setViewportSize({ width: 375, height: 812 });
  const back = reader(page).getByRole('button', { name: 'Back to files' });
  await back.press('Enter');
  await expect(last).toBeFocused();
  await expect(back).toBeHidden();
  await shot(page, 'phone-tree');
  expect(
    await reader(page)
      .locator('.wf-tree')
      .evaluate(el => el.scrollTop)
  ).toBe(scroll);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await mode(page, 'details').click();
  unlinkSync(join(dir, 'Take 109.txt'));
  await mode(page, 'files').click();
  await expect(row(page, 'Sessions')).toHaveAttribute('aria-selected', 'true');
  await expect(last).toHaveCount(0);
  await expect(mode(page, 'files')).toBeFocused();
  await mode(page, 'tickets').click();
  await expect(page.locator('#workspace-detail-tickets-surface')).toBeVisible();
  await expect(reader(page)).toBeHidden();
  await mode(page, 'details').click();
  await expect(page.locator('#workspaceNextActionRegion')).toBeVisible();
});

test('a member removed from the live group loses its open preview', async ({ page }) => {
  const home = await workspace(page, 'Engineering', '', true);
  const member = await workspace(page, 'Field notes', home.id);
  const res = await page.request.post(`/api/workspaces/${member.id}/notes`, {
    data: { name: 'Lab log', content: 'One clean run.' }
  });
  expect(res.ok()).toBeTruthy();
  await page.goto(`/workspaces/${home.folder_slug}?mode=files`);
  await ready(page);
  await row(page, 'Linked workspaces').click();
  await keyed(page, member.id).click();
  await keyed(page, `${member.id}/s/notes`).click();
  await row(page, 'Lab log').click();
  await expect(reader(page)).toContainText('One clean run.');
  // A different client moves this member, not the reader. Consume the page’s live
  // membership refresh without another navigation or a second tree expansion.
  expect(
    (await page.request.patch(`/api/workspaces/${member.id}`, { data: { parent_id: '' } })).ok()
  ).toBeTruthy();
  await page.evaluate(() => (window as any).workspaceDetail.loadWorkspace());
  await expect(reader(page)).not.toContainText('One clean run.');
  await expect(keyed(page, member.id)).toHaveCount(0);
  await expect(row(page, 'No linked workspaces')).toBeVisible();
});

test('a failed note read clears content and retries without an editor', async ({ page }) => {
  const ws = await workspace(page, 'Session review');
  const { note } = await (
    await page.request.post(`/api/workspaces/${ws.id}/notes`, {
      data: {
        name: 'Cue sheet',
        content: '# Cue 1\n\n<form><input value="No editing"></form>Start here.'
      }
    })
  ).json();
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await ready(page);
  await row(page, 'Notes').click();
  await page.route(
    `**/api/notes/${note.id}`,
    route => route.fulfill({ status: 503, body: 'Temporarily unavailable' }),
    { times: 1 }
  );
  await row(page, 'Cue sheet').click();
  const body = reader(page).locator('.wf-body');
  await expect(body).toContainText('Temporarily unavailable');
  await body.getByRole('button', { name: 'Retry preview' }).click();
  await expect(body).toContainText('Start here.');
  await expect(body.locator('form,input,[contenteditable]')).toHaveCount(0);
});

test('empty group distinguishes unavailable membership and retries in place', async ({ page }) => {
  const home = await workspace(page, 'Empty group', '', true);
  await page.goto(`/workspaces/${home.folder_slug}?mode=files`);
  await ready(page);
  await page.route(
    '**/api/workspaces?tree=true',
    route => route.fulfill({ status: 503, body: 'Unavailable' }),
    { times: 1 }
  );
  await row(page, 'Linked workspaces').click();
  await row(page, 'Could not load linked workspaces. Retry').click();
  await expect(row(page, 'No linked workspaces')).toBeVisible();
});

test('ordinary workspace previews nested Markdown and an owned note without opening a WAV', async ({
  page
}) => {
  const ws = await workspace(page, 'Mix session');
  const dir = await files(page, ws.id, 'Sessions');
  writeFileSync(join(dir, 'Mix notes.md'), '# First listen\n\nKeep the vocal close.');
  writeFileSync(join(dir, 'Mix v03.wav'), 'Not fetched');
  const note = await (
    await page.request.post(`/api/workspaces/${ws.id}/notes`, {
      data: { name: 'Session log', content: '# Tracking day\n\nTwo takes.' }
    })
  ).json();
  await page.goto(`/workspaces/${ws.folder_slug}?mode=files`);
  await expect(row(page, 'Files')).toBeVisible();
  // The detail page activates the workspace and loads its shared managers on boot,
  // regardless of tab. Measure the reader interaction, not page initialization.
  await ready(page);
  const requests: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (
      path.startsWith('/api/') &&
      (/\/files(?:\/|$)|\/notes(?:\/|$)/.test(path) || req.method() !== 'GET')
    )
      requests.push(`${req.method()} ${decodeURIComponent(path)}`);
  });
  await row(page, 'Files').click();
  await row(page, 'Sessions').click();
  await row(page, 'Mix notes.md').click();
  await expect(reader(page).locator('.wf-body')).toContainText('Keep the vocal close.');
  await shot(page, 'ordinary-file');
  await row(page, 'Notes').click();
  await row(page, 'Session log').click();
  await expect(reader(page).locator('.wf-body')).toContainText('Two takes.');
  await shot(page, 'ordinary-note');
  await row(page, 'Mix v03.wav').click();
  await expect(reader(page)).toContainText('No preview available');
  await shot(page, 'ordinary-unsupported');
  await expect(reader(page).locator('[contenteditable],audio,a[download]')).toHaveCount(0);
  expect(requests).toEqual([
    `GET /api/workspaces/${ws.id}/files/tree`,
    `GET /api/workspaces/${ws.id}/files/Sessions/Mix notes.md`,
    `GET /api/workspaces/${ws.id}/notes`,
    `GET /api/notes/${note.note.id}`
  ]);
});
