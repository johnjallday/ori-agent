import { test, expect, type APIRequestContext, type Page, type Request } from '@playwright/test';
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync
} from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join } from 'node:path';

// Destructive reset owns one process and one installation. It never uses the
// shared Playwright base URL, native credentials, external CLIs, or inherited
// provider variables.
test.describe.serial('Settings reset on an owned installation', () => {
  // Each journey builds the server, drives the reviewed surface, stops the
  // actual process, relaunches it, and waits for durable recovery. That does not
  // fit Playwright's 30s default, and a timeout here reports as an unexplained
  // failure rather than the real result.
  test.describe.configure({ timeout: 240_000 });

  let root = '';
  let dataDir = '';
  let binary = '';
  let origin = '';
  let child: ChildProcess | null = null;
  let output = '';
  let previousCompletedURL = '';

  const digest = (path: string) => createHash('sha256').update(readFileSync(path)).digest('hex');

  async function freePort(): Promise<number> {
    return await new Promise((resolve, reject) => {
      const listener = createServer();
      listener.once('error', reject);
      listener.listen(0, '127.0.0.1', () => {
        const address = listener.address();
        const port = typeof address === 'object' && address ? address.port : 0;
        listener.close(error => (error ? reject(error) : resolve(port)));
      });
    });
  }

  async function waitFor(path: string, predicate: (response: Response) => Promise<boolean>) {
    const deadline = Date.now() + 60_000;
    let lastError: unknown;
    while (Date.now() < deadline) {
      try {
        const response = await fetch(origin + path, {
          headers: { 'X-Requested-With': 'XMLHttpRequest' }
        });
        if (await predicate(response)) return;
      } catch (error) {
        lastError = error;
      }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error(
      `owned reset server did not become ready: ${String(lastError || output.slice(-1000))}`
    );
  }

  function waitForRequestEnd(page: Page, request: Request): Promise<'finished' | 'failed'> {
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        clearTimeout(timer);
        page.off('requestfinished', onFinished);
        page.off('requestfailed', onFailed);
      };
      const onFinished = (candidate: Request) => {
        if (candidate !== request) return;
        cleanup();
        resolve('finished');
      };
      const onFailed = (candidate: Request) => {
        if (candidate !== request) return;
        cleanup();
        resolve('failed');
      };
      const timer = setTimeout(() => {
        cleanup();
        reject(new Error(`stream request did not end after reset fence: ${request.url()}`));
      }, 15_000);
      page.on('requestfinished', onFinished);
      page.on('requestfailed', onFailed);
    });
  }

  function startChild(managed = false) {
    if (child) throw new Error('owned reset server is already running');
    const port = Number(new URL(origin).port);
    const emptyPath = join(root, 'bin');
    mkdirSync(emptyPath, { recursive: true, mode: 0o750 });
    const env: NodeJS.ProcessEnv = {
      HOME: join(root, 'home'),
      USERPROFILE: join(root, 'home'),
      XDG_CONFIG_HOME: join(root, 'home', '.config'),
      XDG_DATA_HOME: join(root, 'home', '.local', 'share'),
      TMPDIR: join(root, 'tmp'),
      TMP: join(root, 'tmp'),
      TEMP: join(root, 'tmp'),
      PATH: emptyPath,
      PORT: String(port),
      ORI_DATA_DIR: dataDir,
      ORI_DISABLE_NATIVE_SECRET_STORE: '1',
      ORI_VAULT_PASSPHRASE: 'synthetic-reset-suite-passphrase',
      ORI_DISABLE_EXTERNAL_MCP_IMPORT: 'true',
      NO_BROWSER: '1'
    };
    child = spawn(binary, managed ? ['-managed-restart'] : [], {
      cwd: root,
      env,
      stdio: ['ignore', 'pipe', 'pipe']
    });
    const collect = (chunk: Buffer) => {
      output = (output + chunk.toString()).slice(-20_000);
    };
    child.stdout?.on('data', collect);
    child.stderr?.on('data', collect);
    return child;
  }

  async function startServer(managed = false) {
    startChild(managed);
    await waitFor('/health', async response => response.ok);
  }

  async function stopServer() {
    const owned = child;
    if (!owned) return;
    child = null;
    if (owned.exitCode !== null) return;
    owned.kill('SIGTERM');
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => {
        owned.kill('SIGKILL');
        reject(new Error(`owned reset server did not stop cleanly: ${output.slice(-1000)}`));
      }, 30_000);
      owned.once('exit', () => {
        clearTimeout(timer);
        resolve();
      });
    });
  }

  // Settings opens the first-run onboarding modal whenever onboarding is
  // incomplete, and that modal is static/backdrop-blocking, so it swallows every
  // click on the reset controls behind it. A fresh installation and Replay Setup
  // both leave onboarding incomplete, so mark it complete through the same API
  // the wizard uses before driving the reset surface.
  async function completeOnboarding(request: APIRequestContext) {
    const headers = { 'X-Requested-With': 'XMLHttpRequest' };
    const names = await request.post(origin + '/api/onboarding/names', {
      headers,
      data: { user_name: 'Reset Suite', assistant_name: 'Ori' }
    });
    expect(names.ok()).toBeTruthy();
    const complete = await request.post(origin + '/api/onboarding/complete', { headers });
    expect(complete.ok()).toBeTruthy();
  }

  // Bootstrap ignores hide() while a dialog is still fading in, so a click on
  // Cancel during the transition is silently dropped. The reviewed dialog moves
  // focus into the confirmation field on shown.bs.modal, so a focused field is
  // the signal that it is fully open and will accept dismissal.
  async function reviewDialogReady(page: Page) {
    await expect(page.locator('#resetConfirmModal')).toHaveClass(/show/);
    await expect(page.locator('#resetConfirmInput')).toBeFocused();
  }

  async function cancelReviewDialog(page: Page) {
    await reviewDialogReady(page);
    await page.locator('#resetCancelBtn').click();
    await expect(page.locator('#resetConfirmModal')).not.toHaveClass(/show/);
  }

  async function openSettings(page: Page, request: APIRequestContext) {
    await completeOnboarding(request);
    await page.goto(origin + '/settings', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#startFreshBtn')).toBeVisible();
    await expect(page.locator('#onboardingModal.show')).toHaveCount(0);
    // Allow initial Settings GETs to finish. They remain ordinary admitted
    // work; a confirmation must not cancel in-flight requests.
    await page.waitForLoadState('networkidle');
  }

  async function stagedStatus(page: Page) {
    await expect(page.getByRole('heading', { name: 'Restart required' })).toBeVisible();
    const statusURL = page.url();
    const operationID = await page.evaluate(() => localStorage.getItem('ori.reset.operation_id'));
    expect(operationID).toBeTruthy();
    expect(statusURL).toBe(`${origin}/reset/status?operation=${operationID}`);
    return statusURL;
  }

  test.beforeAll(async () => {
    root = mkdtempSync(join(tmpdir(), 'ori-settings-reset-e2e-'));
    dataDir = join(root, 'data');
    const suppliedBinary = process.env.ORI_SETTINGS_RESET_TEST_BINARY;
    binary = suppliedBinary || join(root, 'ori-agent');
    if (suppliedBinary && (!isAbsolute(suppliedBinary) || !existsSync(suppliedBinary))) {
      throw new Error('ORI_SETTINGS_RESET_TEST_BINARY must name an existing absolute path');
    }
    for (const dir of [
      dataDir,
      join(root, 'home'),
      join(root, 'tmp'),
      join(dataDir, 'workspace-staging')
    ]) {
      mkdirSync(dir, { recursive: true, mode: 0o750 });
    }
    if (!suppliedBinary) {
      execFileSync('go', ['build', '-o', binary, './cmd/server'], {
        cwd: process.cwd(),
        stdio: 'pipe'
      });
    }
    origin = `http://127.0.0.1:${await freePort()}`;
    await startServer();
  });

  test.afterAll(async () => {
    await stopServer();
    if (root) rmSync(root, { recursive: true, force: true });
  });

  test('reviews, restarts, verifies, and reattaches without touching retained bytes', async ({
    page,
    request
  }) => {
    const workspaceSentinel = join(dataDir, 'workspace-staging', 'retained.txt');
    writeFileSync(workspaceSentinel, 'owned reset browser fixture\n', { mode: 0o600 });

    const createVault = await request.post(origin + '/api/vault/vaults', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: {
        name: 'Retained Browser Vault',
        vault_password: 'synthetic-vault-password',
        storage: { mode: 'managed' }
      }
    });
    expect(createVault.status()).toBe(201);
    const created = await createVault.json();
    const vaultID = String(created.vault.id);
    const vaultPath = isAbsolute(created.vault.file_path)
      ? created.vault.file_path
      : join(dataDir, created.vault.file_path);
    const packageDirectory = dirname(vaultPath);
    const beforeWorkspace = digest(workspaceSentinel);

    // The user's agents are folders in the Workspace Directory (the staging
    // root until one is confirmed). Start Fresh removes each agent's folder and
    // keeps every other file there.
    const createAgent = await request.post(origin + '/api/agents', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { name: 'Reset Scout', system_prompt: 'You scout ahead.' }
    });
    expect(createAgent.ok()).toBeTruthy();
    const agentsFolder = join(dataDir, 'workspace-staging', 'Agents');
    const agentDefinition = join(agentsFolder, 'Reset Scout', 'agent_settings.json');
    expect(existsSync(agentDefinition)).toBe(true);
    const agentsNote = join(agentsFolder, 'README.txt');
    writeFileSync(agentsNote, 'kept next to my agents\n', { mode: 0o600 });

    await page.addInitScript(() => {
      localStorage.setItem('ori-theme', 'dark');
      localStorage.setItem('ori_chat_session_stale', 'stale');
      localStorage.setItem('unrelated-app-key', 'keep');
      sessionStorage.setItem('oriSetupWizardResume:stale', 'stale');
    });
    await openSettings(page, request);
    page.on('dialog', dialog => dialog.accept());

    const emptySelection = await request.get(origin + '/api/reset/preview?intent=selected_data');
    expect(emptySelection.status()).toBe(400);
    const legacyExecute = await request.post(origin + '/api/reset', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { settings: true, confirmation: 'RESET' }
    });
    expect(legacyExecute.status()).toBe(409);

    await page.locator('#replaySetupBtn').click();
    await expect(page.locator('#replaySetupStatus')).toContainText(
      'identity and existing work were kept'
    );
    await page.locator('#resetGettingStartedBtn').click();
    await expect(page.locator('#resetGettingStartedStatus')).toContainText(
      'Setup and user data were kept'
    );

    // Replay Setup deliberately left onboarding incomplete, which re-opens the
    // blocking first-run modal. Re-establish a clean settings surface before
    // driving the destructive controls.
    await openSettings(page, request);

    await page.locator('#selectAllResetBtn').click();
    await expect(page.locator('.reset-category-checkbox:checked')).toHaveCount(5);
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await expect(page.locator('#resetConfirmTitleText')).toHaveText('Confirm reviewed data reset');
    await expect(page.locator('#resetItemsList')).not.toContainText('Identity, setup & progress');
    await expect(page.locator('#resetItemsList')).toContainText('Installed plugins');
    await cancelReviewDialog(page);
    await page.locator('#clearAllResetBtn').click();
    await expect(page.locator('.reset-category-checkbox:checked')).toHaveCount(0);

    await page.locator('#startFreshBtn').click();
    await reviewDialogReady(page);
    await expect(page.locator('#resetConfirmTitleText')).toHaveText('Confirm Start Fresh');
    await expect(page.locator('#resetItemsList')).toContainText('Start Fresh impact: 9 categories');
    await expect(page.locator('#resetItemsList')).toContainText('Keep:');
    await cancelReviewDialog(page);

    await page.locator('#startFreshBtn').click();
    await reviewDialogReady(page);
    await page.locator('#resetConfirmInput').fill('reset');
    await expect(page.locator('#confirmResetBtn')).toBeDisabled();
    await page.locator('#resetConfirmInput').fill('RESET');
    await expect(page.locator('#confirmResetBtn')).toContainText('Start Fresh after full relaunch');
    // Start Fresh removes the agents from the Workspace Directory, which the
    // user can see and may sync, so RESET alone is not enough: the agents
    // folder is named and needs its own confirmation.
    await expect(page.locator('#resetAgentsFolderConfirm')).toBeVisible();
    await expect(page.locator('#resetAgentsFolderPath')).toContainText('/Agents');
    await expect(page.locator('#confirmResetBtn')).toBeDisabled();
    await page.locator('#resetAgentsFolderCheck').check();
    await expect(page.locator('#confirmResetBtn')).toBeEnabled();
    // Sampled immediately before the destructive step. Ordinary browsing can
    // touch a managed vault package's own metadata, so an earlier baseline would
    // measure normal app activity rather than what reset preserved. The reset
    // contract is that the post-drain bytes survive the staged application.
    const beforeVault = digest(vaultPath);
    await page.locator('#confirmResetBtn').click();
    const statusURL = await stagedStatus(page);
    const operationID = new URL(statusURL).searchParams.get('operation');
    expect(await page.evaluate(() => localStorage.getItem('ori-theme'))).toBe('dark');

    await page.reload();
    await expect(page).toHaveURL(statusURL);
    await expect(page.getByRole('heading', { name: 'Restart required' })).toBeVisible();
    await stopServer();
    await expect(page.getByText('Waiting to reconnect.', { exact: false })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Restart required' })).toBeVisible();
    await startServer();
    await expect(page.getByRole('heading', { name: 'Reset completed' })).toBeVisible({
      timeout: 20_000
    });
    const continueSetup = page.getByRole('button', { name: 'Continue setup' });
    await expect(continueSetup).toBeVisible();
    await expect(page).toHaveURL(statusURL);
    previousCompletedURL = statusURL;
    expect(await page.evaluate(() => localStorage.getItem('ori-theme'))).toBeNull();
    expect(await page.evaluate(() => localStorage.getItem('ori_chat_session_stale'))).toBeNull();
    expect(
      await page.evaluate(() => sessionStorage.getItem('oriSetupWizardResume:stale'))
    ).toBeNull();
    expect(await page.evaluate(() => localStorage.getItem('unrelated-app-key'))).toBe('keep');
    expect(await page.evaluate(() => localStorage.getItem('ori.reset.operation_id'))).toBe(
      operationID
    );
    expect(digest(workspaceSentinel)).toBe(beforeWorkspace);
    expect(digest(vaultPath)).toBe(beforeVault);
    expect(existsSync(join(agentsFolder, 'Reset Scout'))).toBe(false);
    expect(readFileSync(agentsNote, 'utf8')).toBe('kept next to my agents\n');
    await continueSetup.click();
    await expect(page).toHaveURL(origin + '/');
    await expect(page.locator('#onboardingModal')).toBeVisible();

    const vaultsAfterReset = await request.get(origin + '/api/vault/vaults');
    expect((await vaultsAfterReset.json()).count).toBe(0);
    const attach = await request.post(origin + '/api/vault/vaults/attach', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { package_directory: packageDirectory }
    });
    expect(attach.status()).toBe(201);
    expect((await attach.json()).vault.id).toBe(vaultID);
    const unlock = await request.post(origin + '/api/vault/unlock', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { vault_id: vaultID, vault_password: 'synthetic-vault-password' }
    });
    expect(unlock.ok()).toBeTruthy();
    expect((await unlock.json()).locked).toBe(false);
  });

  test('reviews, cancels, then removes installed plugins and keeps everything else', async ({
    page,
    request
  }) => {
    // A domain-neutral linked plugin: its skill is read in place and its MCP
    // server is namespaced. Reset removes installation registration, not the
    // linked external bundle or personal skills. Nothing is downloaded/executed.
    const source = join(root, 'external', 'e2e-demo-plugin');
    mkdirSync(join(source, '.claude-plugin'), { recursive: true, mode: 0o750 });
    mkdirSync(join(source, 'skills', 'e2e-demo-skill'), { recursive: true, mode: 0o750 });
    writeFileSync(
      join(source, '.claude-plugin', 'plugin.json'),
      JSON.stringify({
        name: 'e2e-demo-plugin',
        version: '1.0.0',
        description: 'reset e2e fixture'
      }),
      { mode: 0o600 }
    );
    writeFileSync(
      join(source, '.mcp.json'),
      JSON.stringify({ tools: { command: 'never-executed-by-this-test', args: [] } }),
      { mode: 0o600 }
    );
    writeFileSync(
      join(source, 'skills', 'e2e-demo-skill', 'SKILL.md'),
      '---\nname: e2e-demo-skill\ndescription: reset e2e fixture skill\n---\n',
      { mode: 0o600 }
    );
    const sourceSentinel = join(source, '.claude-plugin', 'plugin.json');
    const beforeSource = digest(sourceSentinel);

    // A separate personal skill Ori did not install.
    const personalSkills = join(root, 'home', '.agents', 'skills');
    mkdirSync(join(personalSkills, 'e2e-user-skill'), { recursive: true, mode: 0o750 });
    const userSkill = join(personalSkills, 'e2e-user-skill', 'SKILL.md');
    writeFileSync(userSkill, '---\nname: e2e-user-skill\n---\n', { mode: 0o600 });
    const beforeUserSkill = digest(userSkill);

    const install = await request.post(origin + '/api/plugins/install', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { source, format: 'claude', confirm: true }
    });
    expect(install.ok()).toBeTruthy();
    const installed = (await install.json()).plugin;
    expect(installed.name).toBe('e2e-demo-plugin');
    expect(installed.skills).toEqual(['e2e-demo-skill']);
    const bundledSkill = join(source, 'skills', 'e2e-demo-skill', 'SKILL.md');
    expect(readFileSync(bundledSkill, 'utf8')).toContain('e2e-demo-skill');
    expect(existsSync(join(personalSkills, 'e2e-demo-skill'))).toBe(false);

    const marketplacesBefore = await request.get(origin + '/api/plugins/marketplaces');
    expect(marketplacesBefore.ok()).toBeTruthy();
    const marketplaceCount = (await marketplacesBefore.json())?.marketplaces?.length ?? 0;

    await openSettings(page, request);
    page.on('dialog', dialog => dialog.accept());

    // Review only Installed plugins, then cancel: nothing may change.
    await page.locator('#resetInstalledPlugins').check();
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await expect(page.locator('#resetConfirmTitleText')).toHaveText('Confirm reviewed data reset');
    await expect(page.locator('#resetItemsList')).toContainText('Item: e2e-demo-plugin');
    await expect(page.locator('#resetItemsList')).toContainText('e2e-demo-plugin/tools');
    await expect(page.locator('#resetItemsList')).toContainText('e2e-demo-skill');
    await expect(page.locator('#resetItemsList')).toContainText('linked source (kept)');
    await expect(page.locator('#resetItemsList')).toContainText('marketplaces.json');
    await cancelReviewDialog(page);
    expect(readFileSync(bundledSkill, 'utf8')).toContain('e2e-demo-skill');
    expect((await (await request.get(origin + '/api/plugins')).json()).plugins?.length ?? 0).toBe(
      1
    );

    // Confirm: the wrong casing must not enable the destructive button.
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await page.locator('#resetConfirmInput').fill('reset');
    await expect(page.locator('#confirmResetBtn')).toBeDisabled();
    await page.locator('#resetConfirmInput').fill('RESET');
    await expect(page.locator('#confirmResetBtn')).toContainText('Reset reviewed data');
    await page.locator('#confirmResetBtn').click();
    const statusURL = await stagedStatus(page);
    await page.evaluate(() => localStorage.setItem('unrelated-selective', 'keep'));
    expect(statusURL).not.toBe(previousCompletedURL);
    const independent = await page.context().browser()!.newContext();
    try {
      const freshTab = await independent.newPage();
      await freshTab.goto(origin + '/');
      await expect(freshTab).toHaveURL(statusURL);
      await expect(freshTab.getByRole('heading', { name: 'Restart required' })).toBeVisible();
      expect(
        await freshTab.evaluate(() => localStorage.getItem('ori.reset.operation_id'))
      ).toBeNull();
      await freshTab.goto(previousCompletedURL);
      await expect(
        freshTab.getByRole('heading', { name: 'Reset status unavailable' })
      ).toBeVisible();
      await expect(
        freshTab.getByRole('button', { name: /Continue setup|Back to Ori/ })
      ).toHaveCount(0);
      await expect(freshTab).toHaveURL(previousCompletedURL);
    } finally {
      await independent.close();
    }
    // Nothing is deleted by the accepting process.
    expect(readFileSync(bundledSkill, 'utf8')).toContain('e2e-demo-skill');

    await stopServer();
    await startServer();
    await expect(page.getByRole('heading', { name: 'Reset completed' })).toBeVisible({
      timeout: 20_000
    });
    await expect(page).toHaveURL(statusURL);
    await page.getByText('Reviewed categories and checks').click();
    await expect(page.getByText('e2e-demo-plugin: completed')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Continue setup' })).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole('button', { name: 'Back to Ori' })).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem('unrelated-selective'))).toBe('keep');
    await page.getByRole('button', { name: 'Back to Ori' }).click();
    await expect(page).toHaveURL(origin + '/');

    expect((await (await request.get(origin + '/api/plugins')).json()).plugins?.length ?? 0).toBe(
      0
    );
    expect(readFileSync(bundledSkill, 'utf8')).toContain('e2e-demo-skill');
    expect(digest(userSkill)).toBe(beforeUserSkill);
    expect(digest(sourceSentinel)).toBe(beforeSource);
    const marketplacesAfter = await request.get(origin + '/api/plugins/marketplaces');
    expect((await marketplacesAfter.json())?.marketplaces?.length ?? 0).toBe(marketplaceCount);
  });

  test('resets setup steps while a second Home tab has open event streams', async ({
    page,
    request,
    context
  }) => {
    await completeOnboarding(request);
    const created = await request.post(origin + '/api/workspaces', {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { name: 'Idle Stream Reset' }
    });
    expect(created.status()).toBe(201);
    const workspaceID = String((await created.json()).folder.id);

    const idlePage = await context.newPage();
    await idlePage.goto(origin + '/', { waitUntil: 'domcontentloaded' });
    const mapResponsePromise = idlePage.waitForResponse(
      response =>
        response.url().endsWith('/api/workspace-map/activity/stream') && response.status() === 200
    );
    const workflowResponsePromise = idlePage.waitForResponse(
      response =>
        response.url().includes('/api/orchestration/workflow/stream?') && response.status() === 200
    );
    await idlePage.evaluate(id => {
      (window as any).__resetTestStreams = [
        new EventSource('/api/workspace-map/activity/stream'),
        new EventSource(`/api/orchestration/workflow/stream?workspace_id=${encodeURIComponent(id)}`)
      ];
    }, workspaceID);
    const streamResponses = await Promise.all([mapResponsePromise, workflowResponsePromise]);
    const streamEnds = streamResponses.map(response =>
      waitForRequestEnd(idlePage, response.request())
    );

    await openSettings(page, request);
    await page.locator('#resetOnboarding').check();
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await page.locator('#resetConfirmInput').fill('RESET');
    await expect(page.locator('#confirmResetBtn')).toBeEnabled();
    await page.locator('#confirmResetBtn').click();

    await stagedStatus(page);
    await Promise.all(streamEnds);

    const fenced = await idlePage.evaluate(async () => {
      const response = await fetch('/api/workspace-map/activity/stream');
      return { status: response.status, body: await response.text() };
    });
    expect(fenced.status).toBe(503);
    expect(fenced.body).toContain('reset_pending');
    await idlePage.close();
  });

  test('a lost confirmation reply reconnects to the managed replacement', async ({
    page,
    request
  }) => {
    await stopServer();
    await startServer(true);
    await openSettings(page, request);
    let stagedID = '';
    await page.route(origin + '/api/reset', async route => {
      const response = await route.fetch();
      const payload = await response.json();
      expect(response.status()).toBe(202);
      expect(payload.operation.state).toBe('awaiting_restart');
      expect(payload.operation.restart.managed).toBe(true);
      stagedID = payload.operation.id;
      await route.abort(); // Only the delivery is lost; the real process staged and drained.
    });
    await page.locator('#resetOnboarding').check();
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await page.locator('#resetConfirmInput').fill('RESET');
    await expect(page.locator('#confirmResetBtn')).toHaveText('Reset and restart Ori');
    await page.locator('#confirmResetBtn').click();
    await expect(page.getByRole('button', { name: 'Back to Ori' })).toBeVisible({
      timeout: 20_000
    });
    expect(stagedID).toBeTruthy();
    expect(page.url()).toBe(`${origin}/reset/status?operation=${stagedID}`);
    expect(await page.evaluate(() => localStorage.getItem('ori.reset.operation_id'))).toBe(
      stagedID
    );
    const ready = await request.get(`${origin}/api/reset/operations/${stagedID}`);
    expect(ready.headers()['x-ori-reset-ready']).toBe('1');
    expect((await ready.json()).success).toBe(true);
  });

  test('an intentional managed stop does not replace the child or consume its staged receipt', async ({
    request
  }) => {
    const query = 'intent=selected_data&category=setup_steps';
    const preview = await request.get(`${origin}/api/reset/preview?${query}`);
    expect(preview.ok()).toBeTruthy();
    const reviewed = await preview.json();
    expect(reviewed.restart.managed).toBe(true);
    const staged = await request.post(`${origin}/api/reset`, {
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      data: { preview_id: reviewed.id, request_id: 'intentional-shutdown', confirmation: 'RESET' }
    });
    expect(staged.status()).toBe(202);
    await stopServer(); // Stop the supervisor while the child is still delivering its reply.
    await expect(request.get(origin + '/health', { timeout: 1000 })).rejects.toThrow();
    const journal = JSON.parse(readFileSync(join(dataDir, '.ori-reset', 'operation.json'), 'utf8'));
    expect(journal.operation).toMatchObject({
      id: reviewed.operation_id,
      state: 'awaiting_restart'
    });
    await startServer();
    const recovered = await request.get(`${origin}/api/reset/operations/${reviewed.operation_id}`);
    expect((await recovered.json()).success).toBe(true);
  });

  test('managed startup does not evict an unrelated occupied port', async () => {
    await stopServer();
    const address = new URL(origin);
    const unrelated = createServer(socket => {
      socket.once('data', () =>
        socket.end('HTTP/1.1 200 OK\r\nContent-Length: 9\r\n\r\nunrelated')
      );
    });
    await new Promise<void>((resolve, reject) => {
      unrelated.once('error', reject);
      unrelated.listen(Number(address.port), address.hostname, resolve);
    });
    try {
      const owned = startChild(true);
      const code = await new Promise(resolve => owned.once('exit', resolve));
      expect(code).toBe(1);
      expect(output).toContain('Cannot bind managed Ori server');
      expect(await (await fetch(origin + '/health')).text()).toBe('unrelated');
    } finally {
      await new Promise<void>(resolve => unrelated.close(() => resolve()));
    }
  });

  test('partial recovery keeps completed categories visible and never offers entry', async ({
    page,
    request
  }) => {
    test.skip(
      process.getuid?.() === 0,
      'requires ordinary file permissions for the injected offline removal fault'
    );
    await stopServer();
    await startServer();
    const name = 'partial-reset-fixture';
    const source = join(root, 'external', name);
    mkdirSync(join(source, '.claude-plugin'), { recursive: true, mode: 0o750 });
    const manifest = join(source, '.claude-plugin', 'plugin.json');
    writeFileSync(
      manifest,
      JSON.stringify({ name, version: '1.0.0', description: 'Retained linked source' }),
      { mode: 0o600 }
    );
    const headers = { 'X-Requested-With': 'XMLHttpRequest' };
    const installed = await request.post(origin + '/api/plugins/install', {
      headers,
      data: { source, format: 'claude', confirm: true }
    });
    expect(installed.ok()).toBeTruthy();
    await openSettings(page, request);
    await page.locator('#resetInstalledPlugins').check();
    await page.locator('#resetOnboarding').check();
    await page.locator('#resetAppBtn').click();
    await reviewDialogReady(page);
    await page.locator('#resetConfirmInput').fill('RESET');
    await page.locator('#confirmResetBtn').click();
    const statusURL = await stagedStatus(page);
    const id = new URL(statusURL).searchParams.get('operation');
    await stopServer();

    // Block one owned plugin removal at the apply boundary, not the preview
    // or receipt. The other category can still finish and verify normally.
    const locked = join(dataDir, 'plugins', 'state', name, 'locked');
    mkdirSync(locked, { recursive: true, mode: 0o750 });
    writeFileSync(join(locked, 'fixture.txt'), 'offline fault\n', { mode: 0o600 });
    chmodSync(locked, 0);
    const hash = digest(manifest);
    try {
      startChild();
      await expect(page.getByRole('heading', { name: 'Reset needs attention' })).toBeVisible({
        timeout: 20_000
      });
      await expect(page).toHaveURL(statusURL);
      await page.getByText('Reviewed categories and checks').click();
      await expect(page.getByText('Installed plugins: failed')).toBeVisible();
      await expect(page.getByText('Setup steps: completed')).toBeVisible();
      await expect(page.getByRole('button', { name: /Back to Ori|Continue setup/ })).toHaveCount(0);
      const receipt = await request.get(`${origin}/api/reset/operations/${id}`);
      expect(receipt.headers()['x-ori-reset-ready']).toBeUndefined();
      expect(await receipt.json()).toMatchObject({
        success: false,
        operation: { id, state: 'partial_failure' }
      });
      expect(digest(manifest)).toBe(hash);
    } finally {
      chmodSync(locked, 0o750);
    }
    await stopServer();
    await startServer();
    await expect(page.getByRole('button', { name: 'Back to Ori' })).toBeVisible({
      timeout: 20_000
    });
    await expect(page).toHaveURL(statusURL);
    expect(digest(manifest)).toBe(hash);
  });
});
