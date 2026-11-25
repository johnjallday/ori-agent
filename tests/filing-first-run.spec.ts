import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, readdir, readFile, realpath, utimes, writeFile } from 'node:fs/promises';
import { basename, join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Run only against an isolated demo HOME, never the user's folders.
const sandbox = process.env.ORI_FILING_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-filing-first-run', process.env.ORI_FILING_CASE || '.');
const folderName = process.env.ORI_FILING_FOLDER || 'Downloads';
const entryPoint = process.env.ORI_FILING_ENTRY || 'home';
const theme = process.env.ORI_FILING_THEME || 'light';

async function requireSandbox() {
  test.skip(!sandbox, 'requires the isolated server HOME');
  expect(basename(await realpath(sandbox)), 'disposable demo HOME').toMatch(/^ori-(e2e|demo)[.-]/);
  expect(await readFile(join(sandbox, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath(process.cwd())}\n`
  );
}

async function fedFolder(page: Page, request: APIRequestContext, shots: string, variant = '') {
  await requireSandbox();
  expect(basename(folderName), 'fixture folder must be a single name').toBe(folderName);
  expect(['', '.', '..']).not.toContain(folderName);
  test.setTimeout(120_000);
  await mkdir(shots, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
  for (const known of ['Downloads', 'Desktop', 'Documents'])
    await mkdir(join(sandbox, known), { recursive: true, mode: 0o750 });
  // This journey installs Filing from the folder agent’s setup plan. The OS
  // Downloads root instead has its own Review Downloads → Install Filing flow
  // (folder-agent-downloads-inbox.spec.ts); identity is by path, not basename.
  const root = join(sandbox, variant || 'filing-case', folderName);
  const files: string[] = [];
  await mkdir(root, { recursive: true, mode: 0o750 });
  const old = new Date(Date.now() - 3_600_000);
  for (let n = 0; n < 36; n++) {
    const path = join(root, `filing-${n}.${['pdf', 'png', 'zip', 'csv', 'txt', 'qqq'][n % 6]}`);
    files.push(path);
    await writeFile(path, 'synthetic fixture', { mode: 0o600 });
    await utimes(path, old, old);
  }
  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (!assistant.assistant_id) {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'filing-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me understand my folders.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    assistant = (await hire.json()).personal_assistant;
  }
  if (!assistant.hq_workspace_id) {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'filing-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }
  const list = async () => (await (await request.get(endpoint)).json()).folder_agents || [];
  const existing = new Set(
    (await list()).map((view: { workspace_id: string }) => view.workspace_id)
  );
  await page.goto(entryPoint === 'composer' ? '/' : '/?view=tree');
  if (entryPoint === 'composer') {
    if (!(await page.locator('#personalAssistantInput').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    await page.locator('#personalAssistantAttachMenu').click();
    await page.getByRole('menuitem', { name: 'Add folder', exact: true }).click();
    await page.getByRole('button', { name: 'Hire an agent or explore a file instead…' }).click();
  } else {
    await page.locator('#cockpitPrimaryActionBtn').click();
  }
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await expect(entry).toBeVisible();
  await entry.locator('input').fill(root);
  await entry.getByRole('button').click();
  const hire = page.locator('#personalAssistantFolderAgent');
  await expect(hire).toContainText(`Hire an agent for ${folderName}`);
  await hire.locator('[data-folder-agent-action="start"]').click();
  const get = async () =>
    (await list()).find(
      (view: { folder_name: string; workspace_id: string }) =>
        view.folder_name === folderName && !existing.has(view.workspace_id)
    );
  await expect.poll(async () => (await get())?.state).toBe('reported');
  const view = await get();
  await page.goto(`${view.route}?view=details`);
  const closeAssistant = page.getByRole('button', {
    name: 'Close personal assistant',
    exact: true
  });
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  await expect(report).toContainText(
    `Organize loose files in ${folderName} (you approve every move)`
  );
  await expect(report).toContainText(`Adds filing to ${folderName}`);
  await page.screenshot({ path: join(shots, '01-folder-hire.png'), fullPage: true });
  await report.getByRole('button', { name: 'Set it up', exact: true }).click();
  await expect.poll(async () => (await get())?.setup?.status).toBe('done');
  const api = `/api/workspaces/${view.workspace_id}/file-janitor`;
  const status = async () => (await (await request.get(api)).json()).status;
  return { view, root, files, closeAssistant, status, api };
}

test(`a fed ${folderName} folder asks permission once (${entryPoint}, ${theme})`, async ({
  page,
  request
}) => {
  const { view, root, closeAssistant, status } = await fedFolder(page, request, evidence);
  await page.goto('/?panel=today');
  await page.locator('#personalAssistantProgressRow').click();
  await expect(page.locator('#personalAssistantDoneItems')).toContainText(
    `${folderName} Manager is hired. One step left: allow filing.`
  );
  await page.screenshot({
    path: join(evidence, '14-today-remaining-permission.png'),
    fullPage: true
  });
  await page.goto(`${view.route}?view=details`);
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  const summary = page.locator('#fileJanitorMount');
  await expect(summary).toBeHidden();
  await expect(page.locator('#workspaceFolderAgentReport')).toContainText(
    `${folderName} Manager is hired. One step left: allow filing.`
  );
  await expect(page.locator('#workspaceNextAction')).toHaveText('Review permission');
  await expect(
    page.getByText(`Let ${folderName} Manager file ${folderName}`, { exact: true })
  ).toBeVisible();
  await page.screenshot({ path: join(evidence, '09-single-ask.png'), fullPage: true });
  const mutations: string[] = [];
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      /file-janitor\/(scan|preview|apply|setup|prepare-review)/.test(req.url())
    )
      mutations.push(req.url());
  });
  await page.locator('#workspaceNextAction').click();
  const console = page.getByRole('dialog', { name: 'Filing', exact: true });
  await expect(console).toBeVisible();
  await expect(console.locator('.fj-console-title')).toHaveText('Filing');
  const permissions = page.getByRole('region', { name: 'Review Filing permissions' });
  await expect(permissions.getByRole('heading')).toHaveText(`Allow filing in ${folderName}`);
  await page.screenshot({ path: join(evidence, '05-named-folder-consent.png'), fullPage: true });
  expect((await status()).settings.root_path || '').toBe('');
  expect(mutations).toEqual([]);
  await permissions
    .getByRole('button', { name: `Allow filing in ${folderName}`, exact: true })
    .click();
  await expect(console).toContainText('36 files ready for review. Nothing moved.');
  expect((await status()).settings.root_path).toBe(root);
  expect(mutations.filter(url => /\/(apply|preview|scan|setup)$/.test(url))).toEqual([]);
  expect(mutations.filter(url => url.endsWith('/prepare-review'))).toHaveLength(1);
  await page.screenshot({ path: join(evidence, '06-first-review.png'), fullPage: true });
  await console.getByRole('button', { name: 'Close Filing', exact: true }).click();
  await expect(page.locator('#workspaceFolderAgentReport')).toContainText(
    `${folderName} is set up`
  );
  await expect(summary).toBeVisible();
  await expect(summary).toContainText(folderName);
  await expect(summary).toContainText('36 files waiting for review');
  await page.screenshot({ path: join(evidence, '10-after-permission.png'), fullPage: true });
  const note = summary.locator('.fj-card-fact-note');
  for (const width of [360, 1120, 1440, 2240]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(note).toHaveText('No file contents are read. Names, types, sizes and dates only.');
    const unclipped = await note.evaluate(node => {
      const range = document.createRange();
      range.selectNodeContents(node);
      const rects = [...range.getClientRects()];
      return ['.fj-card-fact', '.fj-card-facts', '.fj-card'].every(selector => {
        const parent = node.closest(selector)!;
        const bounds = parent.getBoundingClientRect();
        return (
          parent.scrollWidth <= parent.clientWidth + 1 &&
          rects.every(
            rect =>
              rect.left >= bounds.left - 1 &&
              rect.right <= bounds.right + 1 &&
              rect.top >= bounds.top - 1 &&
              rect.bottom <= bounds.bottom + 1
          )
        );
      });
    });
    expect(unclipped, `privacy note clipped at ${width}px`).toBe(true);
    await page.screenshot({ path: join(evidence, `11-privacy-${width}.png`), fullPage: true });
  }
});

const mapEvidence = resolve(
  'tasks/evidence-filing-preview-handoff',
  process.env.ORI_FILING_CASE || '.'
);

async function openFilingMap(page: Page, fixture: Awaited<ReturnType<typeof fedFolder>>) {
  await page.goto(`${fixture.view.route}?mode=map`);
  if (await fixture.closeAssistant.isVisible()) await fixture.closeAssistant.click();
  const guidance = page.getByRole('region', { name: 'Next action', exact: true });
  const invite = guidance.getByRole('button', { name: 'Review folder access', exact: true });
  const station = page.locator('.ws-cmd-opmap [data-cmd-hq-station="file-janitor"]');
  const panel = page.getByRole('dialog', { name: 'Filing', exact: true });
  const access = panel.getByRole('region', { name: 'Review Filing permissions' });
  const allow = access.getByRole('button', { name: `Allow filing in ${folderName}`, exact: true });
  return { guidance, invite, station, panel, access, allow };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}

function recordFilingWrites(page: Page, api: string) {
  const writes: string[] = [];
  page.on('request', req => {
    const { pathname } = new URL(req.url());
    if (req.method() !== 'GET' && pathname.startsWith(`${api}/`))
      writes.push(pathname.slice(api.length));
  });
  return writes;
}

test(`a fed ${folderName} preview hands off on the map (${entryPoint}, ${theme})`, async ({
  page,
  request
}) => {
  const late = entryPoint === 'composer';
  await page.emulateMedia({ reducedMotion: late ? 'reduce' : 'no-preference' });
  await page.setViewportSize({ width: late ? 390 : 1440, height: late ? 844 : 1050 });
  const fixture = await fedFolder(page, request, mapEvidence, 'map-case');
  const { guidance, invite, station, panel, access, allow } = await openFilingMap(page, fixture);
  const writes = recordFilingWrites(page, fixture.api);
  const snapshot = (state: string) =>
    page.screenshot({ path: join(mapEvidence, state + '.png'), animations: 'disabled' });
  await expect(guidance.getByRole('heading')).toHaveText(
    `Prepare a filing preview for ${folderName}?`
  );
  await expect(guidance).toContainText(`A note from ${folderName} Manager`);
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
  await expect(guidance.getByRole('button', { name: 'All tickets' })).toHaveCount(0);
  const tickets = page.locator('[data-cmd-view-mode="tickets"]');
  await expect(tickets).toBeVisible();
  await expect(page.locator('.ws-cmd-map-agent.is-command-node')).toContainText(
    'Waiting for permission'
  );
  await expect(page.getByText('See what I made', { exact: false })).toHaveCount(0);
  await expect(station).toBeVisible();
  expect(await guidance.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
  await snapshot('02-map-before');
  for (const trigger of [invite, station]) {
    await trigger.focus();
    await page.keyboard.press('Enter');
    await expect(access.getByRole('heading')).toHaveText(`Allow filing in ${folderName}`);
    await expect(access).toContainText('This changes the folder on disk');
    await expect(access).toContainText('No file contents are opened');
    await expect(access).toContainText('Both require separate approval');
    await expect(access.getByRole('checkbox', { name: /watch new arrivals/i })).not.toBeChecked();
    await snapshot(`03-access-${trigger === invite ? 'note' : 'station'}`);
    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(trigger).toBeFocused();
    expect((await fixture.status()).settings.root_path || '').toBe('');
    expect(await readdir(fixture.root)).not.toContain('Filed');
    expect(writes).toEqual([]);
  }

  await page.evaluate(() => {
    document.documentElement.dataset.filingMotion = '';
    document.addEventListener('animationstart', event => {
      if (event.animationName.startsWith('ws-filing-preview-'))
        document.documentElement.dataset.filingMotion += event.animationName + ' ';
    });
  });
  const release = deferred<void>();
  const accepted = deferred<number>();
  await page.route(`**${fixture.api}/prepare-review`, async route => {
    const response = await route.fetch();
    accepted.resolve(response.status());
    await release.promise;
    await route.fulfill({ response });
  });
  let dismissedUrl = '';
  try {
    await invite.click();
    await allow.click();
    await expect(panel.getByText('Preparing your preview…', { exact: true })).toBeVisible();
    expect(await accepted.promise).toBe(200);
    if (late) {
      await page.keyboard.press('Escape');
      await expect(panel).toBeHidden();
      await expect(guidance).toContainText('Preparing your preview…');
      await tickets.focus();
      dismissedUrl = page.url();
    }
  } finally {
    release.resolve();
  }
  const ready = '36 files ready for review. Nothing moved.';
  if (!late) {
    await expect(panel.getByText(ready, { exact: true })).toBeVisible();
    await expect(panel.locator('.fj-console-title')).toBeFocused();
    await snapshot('04-panel-ready');
    await panel.getByRole('button', { name: 'Back to map' }).click();
  }
  await expect(panel).toBeHidden();
  // When the station owns this exact batch it replaces the duplicate note.
  await expect(guidance).toHaveCount(0);
  await expect(station).toHaveAccessibleName(/Review 36 files, Nothing moved/);
  await expect(page.locator('.ws-cmd-map-agent.is-command-node')).not.toContainText(
    'Waiting for permission'
  );
  if (late) {
    expect(page.url()).toBe(dismissedUrl);
    await expect(tickets).toBeFocused();
  }
  const motions = () => page.locator('html').getAttribute('data-filing-motion');
  if (late) expect(await motions()).toBe('');
  else await expect.poll(motions).toContain('ws-filing-preview-mark');
  await snapshot('05-map-ready');

  const saved = (await (await request.get(`${fixture.api}/setup-review`)).json()).review.result;
  expect(saved.outcome).toBe('batch');
  expect(saved.eligible_count).toBe(36);
  // Returning and reopening the same batch must not repeat the handoff.
  for (let visit = 0; visit < 2; visit++) {
    await station.click();
    await expect(panel).toContainText(ready);
    await expect(page).toHaveURL(new RegExp(`[?&]batch_id=${saved.batch_id}(?:&|$)`));
    await panel.getByRole('button', { name: 'Back to map' }).click();
    await expect(panel).toBeHidden();
  }
  expect(((await motions()) || '').match(/ws-filing-preview-mark/g)?.length || 0).toBe(
    late ? 0 : 1
  );
  expect(writes).toEqual(['/prepare-review']);
  const observed = await fixture.status();
  expect(observed.settings.root_path).toBe(fixture.root);
  expect(observed.settings.setup_decision.mode).toBe('manual');
  expect(await readdir(fixture.root)).toContain('Filed');
  expect(await Promise.all(fixture.files.map(file => readFile(file, 'utf8')))).toEqual(
    fixture.files.map(() => 'synthetic fixture')
  );
  await snapshot('06-map-returned');
  await page.reload();
  await expect(station).toHaveAccessibleName(/Review 36 files, Nothing moves until you file/);
  await expect(station).not.toHaveClass(/is-preview-handoff/);
});

for (const scenario of ['empty', 'retry', 'urgent'] as const) {
  test(`map preview edge: ${scenario}`, async ({ page, request }) => {
    const shots = join(mapEvidence, scenario);
    const fixture = await fedFolder(page, request, shots, `map-${scenario}`);
    if (scenario === 'urgent') {
      const created = await request.post('/api/orchestration/tasks', {
        data: { workspace_id: fixture.view.workspace_id, description: 'Review the urgent draft' }
      });
      expect(created.ok(), await created.text()).toBe(true);
      const { task } = await created.json();
      const started = await request.post(
        `/api/workspaces/${fixture.view.workspace_id}/tickets/${task.id}/transition`,
        { data: { to: 'in_progress' } }
      );
      expect(started.ok(), await started.text()).toBe(true);
      const updated = await request.put('/api/orchestration/tasks', {
        data: {
          task_id: task.id,
          context: {
            human_loop: { state: 'blocked', repair: { label: 'Review connection', url: '/agents' } }
          }
        }
      });
      expect(updated.ok(), await updated.text()).toBe(true);
      const { guidance, invite, station } = await openFilingMap(page, fixture);
      await expect(guidance).toContainText('Review the urgent draft');
      await expect(invite).toHaveCount(0);
      await expect(station).toBeVisible();
      await page.screenshot({ path: join(shots, 'urgent.png'), animations: 'disabled' });
      await guidance.getByRole('button', { name: 'Open ticket', exact: true }).click();
      await expect(page.locator('#hubTicketDetailTitle')).toContainText('Review the urgent draft');
      return;
    }
    const { guidance, invite, station, panel, allow } = await openFilingMap(page, fixture);
    const writes = recordFilingWrites(page, fixture.api);
    await invite.click();
    if (scenario === 'empty') {
      const now = new Date();
      await Promise.all(fixture.files.map(file => utimes(file, now, now)));
    } else {
      // Simulate an unavailable request before it reaches the server. The next
      // attempt is real and must be explicitly confirmed again.
      await page.route(
        `**${fixture.api}/prepare-review`,
        route =>
          route.fulfill({
            status: 503,
            json: { error: { message: 'Preview preparation is unavailable. Try again.' } }
          }),
        { times: 1 }
      );
      await allow.click();
      await expect(panel.locator('#fileJanitorError')).toHaveText(
        'Preview preparation is unavailable. Try again.'
      );
      expect((await fixture.status()).settings.root_path || '').toBe('');
      expect(writes).toEqual(['/prepare-review']);
      await page.screenshot({ path: join(shots, 'failure.png'), animations: 'disabled' });
    }
    await allow.click();
    if (scenario === 'empty') {
      await expect(panel).toContainText('No eligible files are ready for review');
      await panel.getByRole('button', { name: 'Close Filing', exact: true }).click();
      await expect(guidance).toContainText('No eligible files for this preview');
      await expect(station).not.toHaveClass(/is-preview-handoff/);
      const saved = (await (await request.get(`${fixture.api}/setup-review`)).json()).review.result;
      expect(saved.outcome).toBe('no_eligible');
      expect(saved.batch_id).toBeUndefined();
      expect(writes).toEqual(['/prepare-review']);
    } else {
      await expect(panel).toContainText('36 files ready for review. Nothing moved.');
      expect(writes).toEqual(['/prepare-review', '/prepare-review']);
    }
    await page.screenshot({ path: join(shots, 'outcome.png'), animations: 'disabled' });
    expect(await Promise.all(fixture.files.map(file => readFile(file, 'utf8')))).toEqual(
      fixture.files.map(() => 'synthetic fixture')
    );
  });
}

test('capability-only filing asks for a folder and never prefills access', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const created = await request.post('/api/workspaces', {
    data: { name: 'Filing without a folder' }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await created.json()).folder;
  const installed = await request.post(
    `/api/workspaces/${workspace.id}/capabilities/file-janitor/install`,
    { data: {} }
  );
  expect(installed.ok(), await installed.text()).toBeTruthy();
  await page.goto(`/workspaces/${workspace.folder_slug}?view=details`);
  const gate = page.locator('#workspace-detail-task-confirm-modal');
  await expect(gate).toBeVisible();
  await gate.getByRole('button', { name: 'Not now' }).click();
  await expect(gate).toBeHidden();
  await expect(page.getByText('Choose the folder to file', { exact: true })).toBeVisible();
  await page.locator('#workspaceNextAction').click();
  await expect(page.locator('#fileJanitorPath')).toHaveValue('');
  await expect(page.getByLabel('Folder to file', { exact: true })).toBeVisible();
  await page.screenshot({ path: join(evidence, '07-no-attached-folder.png'), fullPage: true });
});

test('Templates lists Filing and its new workspace opens the renamed wizard', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  await page.goto('/templates');
  const item = page.locator('#tplList [role="listitem"]').filter({
    has: page.getByText('file-janitor', { exact: true })
  });
  await expect(item).toContainText('Filing');
  await expect(item).toHaveCount(1);
  await item.click();
  await expect(page.locator('#tplEditName')).toHaveValue('Filing');
  await page.screenshot({ path: join(evidence, '12-templates-filing.png'), fullPage: true });
  const created = await request.post('/api/workspaces', {
    data: { name: 'Filing wizard demo', template_id: 'file-janitor', create_template_agents: true }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await created.json()).folder;
  await page.goto(`/workspaces/${workspace.folder_slug}`);
  await expect(page.locator('#setupWizardDialog')).toBeVisible();
  await expect(page.locator('#setupWizardTitle')).toHaveText('Set up filing');
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to file');
  await expect(page.locator('#setupWizardPrimary')).toBeDisabled();
  await page.screenshot({ path: join(evidence, '13-filing-wizard.png'), fullPage: true });
});
