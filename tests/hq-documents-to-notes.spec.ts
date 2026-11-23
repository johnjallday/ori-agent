import { test, expect } from '@playwright/test';
import { lstat, mkdir, readFile, readdir, realpath, symlink, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { tmpdir } from 'node:os';
import { installLocalCdn } from './helpers/offline-cdn';

test('the confirmed guided hire returns only its own Documents evidence', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox, 'requires an isolated HOME');
  const home = await realpath(sandbox!);
  expect(home.startsWith((await realpath(tmpdir())) + '/')).toBeTruthy();
  expect(basename(home)).toMatch(/^ori-(demo|e2e)\./);
  // The sandbox also serves as ORI_DATA_DIR. Stage the physical Documents
  // tree separately rather than exempting application state from the guard.
  const docs = info.outputPath('Documents');
  await mkdir(join(docs, 'Thesis'), { recursive: true });
  const empty = process.env.ORI_DOCUMENTS_EMPTY === '1';
  const mode = process.env.ORI_DOCUMENTS_MODE || 'writable';
  if (mode === 'vault') {
    await mkdir(join(docs, 'Research', '.obsidian'), { recursive: true });
    await writeFile(join(docs, 'Research', 'Ideas.md'), 'Read only fixture.\n');
  }
  if (!empty)
    await writeFile(join(docs, 'Thesis', 'main.tex'), 'fixture manuscript\n', { mode: 0o600 });
  await symlink(docs, join(home, 'Documents'), 'dir');
  const post = async (url: string, data: unknown) => {
    const r = await request.post(url, { data });
    expect(r.ok(), url).toBeTruthy();
    return r.json();
  };
  await post('/api/onboarding/skip', {});
  const { default_workspace_root: root } = await (
    await request.get('/api/settings/workspace-root')
  ).json();
  await post('/api/settings/workspace-root', { workspace_root: root });
  const assistant = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const before = await assistant();
  expect(before.state).toBe('needs_hire');
  await post('/api/personal-assistant/hire', {
    request_id: 'documents-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Help with my notes.',
    focus_areas: ['plan_my_day']
  });
  await post('/api/personal-assistant/hq', {
    request_id: 'documents-hq',
    if_version: (await assistant()).state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: false }
  });
  if (!empty && mode === 'writable')
    await post('/api/personal-assistant/second-brain/create', { request_id: 'documents-notes' });
  const original = empty ? null : await lstat(join(docs, 'Thesis', 'main.tex'));
  const { status } = await (await request.get('/api/personal-hq/status')).json();
  await installLocalCdn(page);
  if (mode === 'setup') {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  }
  await page.goto(`/workspaces/${encodeURIComponent(status.workspace.folder_slug)}?mode=details`);
  const card = page.locator('[data-hq-firstvisit]');
  await card.getByRole('button', { name: /Explore Documents/ }).click();
  await card.getByRole('link', { name: 'Show me where to start' }).click();
  const attach = page.getByRole('button', { name: 'Attach', exact: true });
  await expect(attach).toBeFocused({ timeout: 20000 });
  await attach.press('ArrowDown');
  await page.getByRole('menuitem', { name: 'Add folder', exact: true }).press('Enter');
  await page.locator('#personalAssistantFolderChips [data-chip="documents"]').click();
  await page.locator('[data-folder-agent-action="start"]').click();
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant/folder-agent')).json())
          .folder_agents?.[0]?.state,
      { timeout: 30000 }
    )
    .toBe('reported');
  await page.locator('#personalAssistantPanel [data-guided-return]').click();
  const receipt = page.locator('[data-guided-receipt]');
  if (empty) {
    await expect(receipt).toContainText('first look at Documents is ready');
    await expect(receipt.locator('details')).toHaveCount(0);
    await expect(receipt.getByRole('link', { name: 'Open folder workspace' })).toBeVisible();
    await page.screenshot({ path: info.outputPath('documents-empty.png'), animations: 'disabled' });
    await expect(lstat(join(root, 'My Notes'))).rejects.toMatchObject({ code: 'ENOENT' });
    return;
  }
  if (mode === 'vault') {
    await receipt.getByRole('button', { name: 'Connect Documents/Research read-only' }).click();
    const consent = page.locator('#personalAssistantPanel');
    await expect(consent).toContainText('Read Research?');
    await expect(
      consent.getByText('Read-only. Nothing is moved, edited, or deleted.')
    ).toBeVisible();
    await page.screenshot({ path: info.outputPath('documents-vault.png') });
    await consent.getByRole('button', { name: 'Connect and look' }).click();
    await expect
      .poll(
        async () =>
          (await (await request.get('/api/personal-assistant/second-brain')).json()).second_brain
            ?.source?.name
      )
      .toBe('Research');
    await page.reload();
    await expect(receipt.getByRole('link', { name: 'Open Notes' })).toBeVisible();
    await expect(receipt.getByRole('button', { name: /Draft|Connect/ })).toHaveCount(0);
    expect(await readdir(join(docs, 'Research'))).toEqual(['.obsidian', 'Ideas.md']);
    return;
  }
  await expect(receipt).toContainText('I found a manuscript in Documents/Thesis');
  await receipt.getByText('Review Documents/Thesis').click();
  await expect(receipt.getByText(/LaTeX manuscript/)).toBeVisible();
  await expect(receipt.getByRole('link', { name: 'Open folder workspace' })).toBeVisible();
  await page.screenshot({ path: info.outputPath('documents-offer.png'), animations: 'disabled' });
  const interviewState = async () => {
    const res = await request.get('/api/personal-assistant/knowledge/interview');
    expect(res.ok()).toBeTruthy();
    const { status, state_version, profile, remembered_project } = await res.json();
    return { status, state_version, profile, remembered_project };
  };
  const beforeInterview = await interviewState();
  const rel = 'Projects/Thesis/README.md';
  const destination = join(root, 'My Notes', rel);
  await expect(lstat(destination)).rejects.toMatchObject({ code: 'ENOENT' });
  if (mode === 'writable') {
    await receipt.getByRole('button', { name: 'Not now' }).click();
    await expect(receipt.getByText('Review project choices')).toBeVisible();
    await page.reload();
    await expect(receipt).not.toContainText('Start a project note?');
    await expect(receipt.getByRole('button', { name: 'Draft Thesis note' })).toBeHidden();
    await page.screenshot({ path: info.outputPath('documents-dismissed.png') });
    await receipt.getByText('Review project choices').click();
  }
  await receipt.getByRole('button', { name: 'Draft Thesis note' }).click();
  const dialog = page.locator('[data-documents-review]');
  const answer = 'Send the methods chapter to my advisor';
  await expect(dialog.getByLabel('What are you trying to finish?')).toBeFocused();
  if (mode === 'setup') {
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(receipt.getByRole('button', { name: 'Draft Thesis note' })).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
  }
  await dialog.getByLabel('What are you trying to finish?').fill(answer);
  await dialog.getByRole('button', { name: 'Review note' }).click();
  if (mode === 'setup' || mode === 'changed') {
    const setup = dialog.getByRole('button', { name: 'Set up My Notes' });
    await expect(setup).toBeVisible();
    await expect(dialog).toContainText(join(root, 'My Notes'));
    await page.screenshot({ path: info.outputPath('documents-setup.png') });
    if (mode === 'changed') {
      await post('/api/personal-assistant/second-brain/create', { request_id: 'other-setup' });
      await setup.click();
      await expect(dialog).toContainText('changed');
      await page.screenshot({ path: info.outputPath('documents-refused.png') });
      await expect(lstat(destination)).rejects.toMatchObject({ code: 'ENOENT' });
      return;
    }
    await setup.click();
    await expect(dialog.getByRole('button', { name: 'Save to My Notes' })).toBeVisible();
    await expect(lstat(destination)).rejects.toMatchObject({ code: 'ENOENT' });
  }
  await expect(dialog).toContainText('My Notes/' + rel);
  const bounds = await dialog.boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1);
  const body = await dialog.locator('pre').textContent();
  expect(body).toContain(answer);
  expect(body).toContain('Source: Documents/Thesis');
  await page.screenshot({ path: info.outputPath('documents-review.png') });
  const sent = page.waitForRequest(r => r.url().endsWith('/documents-draft/save'));
  if (mode === 'writable')
    await page.route(
      '**/documents-draft/save',
      async route => {
        const saved = await route.fetch();
        expect(saved.ok()).toBeTruthy();
        await route.abort('failed');
      },
      { times: 1 }
    );
  await dialog.getByRole('button', { name: 'Save to My Notes' }).click();
  if (mode === 'writable') {
    await expect(dialog.getByRole('link', { name: 'Open Notes' })).toBeVisible();
    await page.reload();
    await expect(receipt.getByRole('button', { name: 'Draft Thesis note' })).toHaveCount(0);
    await receipt.getByRole('button', { name: 'Open Thesis note' }).click();
  } else await dialog.getByRole('button', { name: 'Open Thesis note' }).click();
  await expect(dialog.locator('pre')).toHaveText(body!);
  await page.screenshot({ path: info.outputPath('documents-open.png') });
  expect(await readFile(destination, 'utf8')).toMatch(/ori: documents_project/);
  expect((await readFile(destination, 'utf8')).endsWith(body!)).toBeTruthy();
  const save = await sent;
  expect((await post(save.url(), save.postDataJSON())).draft.saved).toBeTruthy();
  expect(await readdir(join(root, 'My Notes', 'Projects', 'Thesis'))).toEqual(['README.md']);
  expect(await interviewState()).toEqual(beforeInterview);
  const facts = await request.get('/api/personal-assistant/knowledge');
  expect(facts.ok()).toBeTruthy();
  expect(await facts.text()).not.toContain(answer);
  const after = await lstat(join(docs, 'Thesis', 'main.tex'));
  expect([after.mode, after.mtimeMs]).toEqual([original!.mode, original!.mtimeMs]);
  expect(await readFile(join(docs, 'Thesis', 'main.tex'), 'utf8')).toBe('fixture manuscript\n');
  if (mode === 'writable') {
    const { second_brain: state } = await (
      await request.get('/api/personal-assistant/second-brain')
    ).json();
    await post('/api/personal-assistant/second-brain/disconnect', {
      if_version: state.version,
      request_id: 'test-disconnect'
    });
    await page.reload();
    await expect(receipt).toContainText('could not be verified');
    await expect(receipt.getByRole('button', { name: 'Open Thesis note' })).toHaveCount(0);
    await page.screenshot({ path: info.outputPath('documents-disconnected.png') });
  }
});
