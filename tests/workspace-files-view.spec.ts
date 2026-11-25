import { test, expect, type Page } from '@playwright/test';
import { mkdirSync, realpathSync, writeFileSync } from 'node:fs';
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
