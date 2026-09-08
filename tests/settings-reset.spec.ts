import { test, expect, type Page } from '@playwright/test';
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join } from 'node:path';

// Destructive reset owns one process and one installation. It never uses the
// shared Playwright base URL, native credentials, external CLIs, or inherited
// provider variables.
test.describe.serial('Settings reset on an owned installation', () => {
  let root = '';
  let dataDir = '';
  let binary = '';
  let origin = '';
  let child: ChildProcess | null = null;
  let output = '';

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

  async function startServer() {
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
    child = spawn(binary, [], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
    const collect = (chunk: Buffer) => {
      output = (output + chunk.toString()).slice(-20_000);
    };
    child.stdout?.on('data', collect);
    child.stderr?.on('data', collect);
    await waitFor('/', async response => response.status < 500);
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

  async function openSettings(page: Page) {
    await page.goto(origin + '/settings', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#startFreshBtn')).toBeVisible();
  }

  test.beforeAll(async () => {
    root = mkdtempSync(join(tmpdir(), 'ori-settings-reset-e2e-'));
    dataDir = join(root, 'data');
    binary = join(root, 'ori-agent');
    for (const dir of [
      dataDir,
      join(root, 'home'),
      join(root, 'tmp'),
      join(dataDir, 'workspace-staging')
    ]) {
      mkdirSync(dir, { recursive: true, mode: 0o750 });
    }
    execFileSync('go', ['build', '-o', binary, './cmd/server'], {
      cwd: process.cwd(),
      stdio: 'pipe'
    });
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
    const beforeVault = digest(vaultPath);

    await page.addInitScript(() => {
      localStorage.setItem('ori-theme', 'dark');
      localStorage.setItem('ori_chat_session_stale', 'stale');
      localStorage.setItem('unrelated-app-key', 'keep');
      sessionStorage.setItem('oriSetupWizardResume:stale', 'stale');
    });
    await openSettings(page);
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

    await page.locator('#selectAllResetBtn').click();
    await expect(page.locator('.reset-category-checkbox:checked')).toHaveCount(4);
    await page.locator('#resetAppBtn').click();
    await expect(page.locator('#resetConfirmTitleText')).toHaveText('Confirm reviewed data reset');
    await expect(page.locator('#resetItemsList')).not.toContainText('Identity, setup & progress');
    await page.locator('#resetCancelBtn').click();

    await page.locator('#startFreshBtn').click();
    await expect(page.locator('#resetConfirmModal')).toHaveClass(/show/);
    await expect(page.locator('#resetConfirmTitleText')).toHaveText('Confirm Start Fresh');
    await expect(page.locator('#resetItemsList')).toContainText('Start Fresh impact: 9 categories');
    await expect(page.locator('#resetItemsList')).toContainText('Keep:');
    await page.locator('#resetCancelBtn').click();
    await expect(page.locator('#resetConfirmModal')).not.toHaveClass(/show/);

    await page.locator('#startFreshBtn').click();
    await page.locator('#resetConfirmInput').fill('reset');
    await expect(page.locator('#confirmResetBtn')).toBeDisabled();
    await page.locator('#resetConfirmInput').fill('RESET');
    await expect(page.locator('#confirmResetBtn')).toContainText('Start Fresh after full relaunch');
    await page.locator('#confirmResetBtn').click();
    await expect(page.locator('#resetOperationStatus')).toContainText('Restart required');
    const operationID = await page.evaluate(() => localStorage.getItem('ori.reset.operation_id'));
    expect(operationID).toBeTruthy();
    expect(await page.evaluate(() => localStorage.getItem('ori-theme'))).toBe('dark');

    await stopServer();
    await startServer();
    await waitFor(`/api/reset/operations/${operationID}`, async response => {
      if (!response.ok) return false;
      const payload = await response.json();
      return payload?.operation?.state === 'completed';
    });
    await openSettings(page);
    await expect(page.locator('#resetOperationStatus')).toContainText('Reset completed');
    await expect(page.locator('#openStartFreshSetupBtn')).toBeVisible();
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
});
