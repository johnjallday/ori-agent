import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  writeFileSync
} from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// Opt in only from a disposable test context with an interactive macOS GUI.
// Unique bundle/job, synthetic credentials, isolated HOME/data, no production
// login registration or installed app is modified.
const native = process.platform === 'darwin' && process.env.ORI_NATIVE_RESET_TEST === '1';
const plutil = '/usr/bin/plutil';
const launchctl = '/bin/launchctl';
const lsof = '/usr/sbin/lsof';

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      const port = address && typeof address !== 'string' ? address.port : 0;
      server.close(error => (error ? reject(error) : resolve(port)));
    });
  });
}

async function screenshot(page: Page, name: string) {
  const evidence = process.env.ORI_NATIVE_RESET_EVIDENCE;
  if (evidence) await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
}

function recordNativeEvidence(name: string, result: object) {
  const evidence = process.env.ORI_NATIVE_RESET_EVIDENCE;
  if (evidence)
    writeFileSync(join(evidence, `native-${name}.json`), JSON.stringify(result, null, 2) + '\n', {
      mode: 0o600
    });
}

test.describe('reset through a disposable native macOS app', () => {
  test.skip(!native, 'requires ORI_NATIVE_RESET_TEST=1 and an active macOS GUI');
  test.describe.configure({ timeout: 180_000 });
  let built: string;
  let buildRoot = '';
  test.beforeAll(() => {
    buildRoot = mkdtempSync(join(tmpdir(), 'ori-native-reset-build-'));
    built = process.env.ORI_NATIVE_RESET_TEST_BINARY || join(buildRoot, 'ori-menubar');
    if (!process.env.ORI_NATIVE_RESET_TEST_BINARY) {
      execFileSync('go', ['build', '-o', built, './cmd/menubar'], {
        env: { ...process.env, GOWORK: 'off' }
      });
    }
  });
  test.afterAll(() => {
    if (buildRoot) rmSync(buildRoot, { force: true, recursive: true });
  });

  for (const scenario of ['foreground', 'default-port', 'blocked', 'failed', 'login-agent']) {
    test(`${scenario} reset host`, async ({ page, request }) => {
      const port = scenario === 'default-port' ? 8765 : await freePort();
      const origin = `http://127.0.0.1:${port}`;
      const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-native-reset-')));
      const bundle = join(root, 'Ori Reset Fixture.app');
      const executable = join(bundle, 'Contents', 'MacOS', 'OriResetFixture');
      const label = `local.ori.reset-fixture.${root.split('/').at(-1)}`;
      const data = join(root, 'data');
      const home = join(root, 'home');
      const env = {
        PATH: '/usr/bin:/bin:/usr/sbin:/sbin',
        HOME: home,
        XDG_CONFIG_HOME: join(home, '.config'),
        ORI_DATA_DIR: data,
        NO_BROWSER: '1',
        ORI_NO_DESKTOP_OPEN: '1',
        ORI_DISABLE_NATIVE_SECRET_STORE: '1',
        ORI_DISABLE_EXTERNAL_MCP_IMPORT: 'true',
        ORI_VAULT_PASSPHRASE: 'synthetic-native-reset-test'
      };
      const domain = `gui/${process.getuid?.()}`;
      const log = join(root, 'native.log');
      let moved = false;
      let loadedJob = false;
      function listenerPID() {
        try {
          return execFileSync(lsof, ['-tiTCP:' + port, '-sTCP:LISTEN'], {
            encoding: 'utf8',
            stdio: ['ignore', 'pipe', 'ignore']
          }).trim();
        } catch {
          return '';
        }
      }
      function owned(pid: string) {
        if (!/^\d+$/.test(pid)) return false;
        try {
          const handles = execFileSync(lsof, ['-p', pid, '-a', '-d', 'cwd,txt', '-Fn'], {
            encoding: 'utf8',
            stdio: ['ignore', 'pipe', 'ignore']
          });
          return handles.includes(`\nn${data}\n`) && handles.includes(`\nn${executable}\n`);
        } catch {
          return false;
        }
      }
      async function untilListening() {
        await expect
          .poll(
            async () => {
              const pid = listenerPID();
              if (!owned(pid)) return false;
              try {
                return (await request.get(origin + '/health', { timeout: 500 })).ok();
              } catch {
                return false;
              }
            },
            { timeout: 20_000 }
          )
          .toBe(true);
      }
      const stop = async () => {
        // Find the exact app, even when listener startup failed. Never signal
        // an address-selected process without checking image and sandbox.
        let pids: string[];
        try {
          pids = execFileSync(lsof, ['-t', executable], {
            encoding: 'utf8',
            stdio: ['ignore', 'pipe', 'ignore']
          })
            .trim()
            .split('\n');
        } catch {
          return;
        }
        for (const pid of pids.filter(owned)) {
          process.kill(Number(pid), 'SIGTERM');
          await expect.poll(() => owned(pid), { timeout: 20_000 }).toBe(false);
        }
      };
      try {
        test.skip(
          Boolean(listenerPID()),
          `port ${port} is already owned; this test never evicts a process`
        );
        for (const dir of [
          data,
          home,
          join(data, 'workspace-staging'),
          resolve(executable, '..')
        ]) {
          mkdirSync(dir, { recursive: true, mode: 0o750 });
        }
        const launchSettings = {
          ...(scenario === 'default-port' ? {} : { port }),
          auto_start_on_login: true
        };
        writeFileSync(join(data, 'app_state.json'), JSON.stringify({ menubar: launchSettings }), {
          mode: 0o600
        });
        const retained = join(data, 'workspace-staging', 'retained.txt');
        writeFileSync(retained, 'retained native fixture\n', { mode: 0o600 });
        copyFileSync(built, executable);
        const info = join(bundle, 'Contents', 'Info.plist');
        execFileSync(plutil, ['-create', 'xml1', info]);
        for (const [key, value] of Object.entries({
          CFBundleExecutable: 'OriResetFixture',
          CFBundleIdentifier: label,
          CFBundleName: 'Ori Reset Fixture',
          CFBundlePackageType: 'APPL'
        })) {
          execFileSync(plutil, ['-insert', key, '-string', value, info]);
        }
        execFileSync(plutil, ['-insert', 'LSUIElement', '-bool', 'YES', info]);
        execFileSync('/usr/bin/codesign', ['--sign', '-', '--force', bundle], { stdio: 'pipe' });
        if (scenario === 'login-agent') {
          const job = join(root, label + '.plist');
          execFileSync(plutil, ['-create', 'xml1', job]);
          execFileSync(plutil, ['-insert', 'Label', '-string', label, job]);
          execFileSync(plutil, [
            '-insert',
            'ProgramArguments',
            '-json',
            JSON.stringify([executable, '--start-server']),
            job
          ]);
          execFileSync(plutil, [
            '-insert',
            'EnvironmentVariables',
            '-json',
            JSON.stringify(env),
            job
          ]);
          execFileSync(plutil, ['-insert', 'RunAtLoad', '-bool', 'YES', job]);
          execFileSync(plutil, ['-insert', 'KeepAlive', '-bool', 'NO', job]);
          execFileSync(launchctl, ['bootstrap', domain, job]);
          loadedJob = true;
        } else {
          execFileSync(
            '/usr/bin/open',
            [
              '-g',
              '-n',
              '-a',
              bundle,
              '--stdout',
              log,
              '--stderr',
              log,
              '--args',
              '--start-server'
            ],
            { env }
          );
        }
        await untilListening();
        const before = listenerPID();
        const headers = { 'X-Requested-With': 'XMLHttpRequest' };
        expect(
          (
            await request.post(origin + '/api/onboarding/names', {
              headers,
              data: { user_name: 'Native Fixture', assistant_name: 'Ori' }
            })
          ).ok()
        ).toBe(true);
        expect((await request.post(origin + '/api/onboarding/complete', { headers })).ok()).toBe(
          true
        );
        await page.goto(origin + '/settings', { waitUntil: 'networkidle' });
        if (scenario === 'foreground') {
          await page.locator('#startFreshBtn').click();
        } else {
          await page.locator('#resetOnboarding').check();
          await page.locator('#resetAppBtn').click();
        }
        await expect(page.locator('#resetConfirmInput')).toBeFocused();
        await page.locator('#resetConfirmInput').fill('RESET');
        if (await page.locator('#resetAgentsFolderCheck').isVisible())
          await page.locator('#resetAgentsFolderCheck').check();
        const confirm = page.locator('#confirmResetBtn');
        await expect(confirm).toHaveText(
          scenario === 'foreground'
            ? 'Start Fresh and restart Ori'
            : scenario === 'login-agent'
              ? 'Reset reviewed data'
              : 'Reset and restart Ori'
        );
        const staged = page.waitForResponse(
          response => response.url() === origin + '/api/reset' && response.status() === 202
        );
        await confirm.click();
        await staged;
        await expect(page).toHaveURL(/\/reset\/status\?operation=/);
        const statusURL = page.url();
        const id = new URL(statusURL).searchParams.get('operation');
        if (scenario === 'failed') {
          renameSync(bundle, bundle + '.moved');
          moved = true;
          await expect(page.getByRole('heading', { name: 'Restart required' })).toBeVisible({
            timeout: 15_000
          });
          // The prior app stayed alive and manual fallback remained reachable.
          expect(listenerPID()).toBe(before);
          await expect(
            page.getByText('Stop Server followed by Start Server', { exact: false })
          ).toBeVisible();
          await screenshot(page, 'native-failed-manual');
          recordNativeEvidence(scenario, {
            id,
            before,
            port,
            replacement: null,
            state: 'awaiting_restart'
          });
          return;
        }
        if (scenario === 'login-agent') {
          await expect(page.getByRole('heading', { name: 'Restart required' })).toBeVisible();
          expect(listenerPID()).toBe(before);
          await screenshot(page, 'native-login-manual');
          await stop();
          const loaded = execFileSync(launchctl, ['print', `${domain}/${label}`], {
            encoding: 'utf8'
          });
          expect(loaded).toMatch(/runs = 1\b/);
          expect(listenerPID()).toBe('');
          recordNativeEvidence(scenario, {
            id,
            before,
            port,
            replacement: null,
            launchAgentRuns: 1
          });
          return;
        }
        if (scenario === 'blocked') writeFileSync(retained, 'changed native fixture\n');
        await expect(page.getByRole('heading', { name: 'Restarting Ori' })).toBeVisible();
        await screenshot(page, `native-${scenario}-waiting`);
        const heading = scenario === 'blocked' ? 'Reset needs attention' : 'Reset completed';
        await expect(page.getByRole('heading', { name: heading })).toBeVisible({ timeout: 25_000 });
        const replacement = listenerPID();
        expect(replacement).not.toBe(before);
        expect(owned(replacement)).toBe(true);
        expect(page.url()).toBe(statusURL);
        const receipt = await request.get(`${origin}/api/reset/operations/${id}`);
        const result = await receipt.json();
        expect(result.operation.id).toBe(id);
        expect(receipt.headers()['x-ori-reset-ready']).toBe(
          scenario === 'blocked' ? undefined : '1'
        );
        expect(result.operation.state).toBe(scenario === 'blocked' ? 'blocked' : 'completed');
        if (scenario === 'blocked') {
          await expect(
            page.getByText('Reset scope changed before pre-start application', { exact: false })
          ).toBeVisible();
          await expect(page.getByRole('button', { name: 'Back to Ori' })).toHaveCount(0);
        } else {
          expect(JSON.parse(readFileSync(join(data, 'app_state.json'), 'utf8')).menubar).toEqual(
            launchSettings
          );
        }
        await screenshot(page, `native-${scenario}-result`);
        recordNativeEvidence(scenario, {
          id,
          before,
          replacement,
          port,
          state: result.operation.state
        });
        if (scenario === 'foreground') {
          await page.getByRole('button', { name: 'Continue setup' }).click();
          await expect(page).toHaveURL(origin + '/');
          await expect(page.getByRole('textbox', { name: 'Your name' })).toBeVisible({
            timeout: 15_000
          });
          await screenshot(page, 'native-foreground-first-run');
        }
      } catch (error) {
        try {
          const statusLines = readFileSync(log, 'utf8')
            .split('\n')
            .filter(line => /automatic|reset|reopen|server before quit/i.test(line));
          console.log(statusLines.slice(-25).join('\n'));
        } catch {
          // A launch failure can occur before the OS creates the log.
        }
        throw error;
      } finally {
        await page.close(); // Let request/stream shutdown settle before terminating the fixture.
        if (moved) renameSync(bundle + '.moved', bundle);
        try {
          await stop();
        } finally {
          if (loadedJob) execFileSync(launchctl, ['bootout', `${domain}/${label}`]);
        }
        rmSync(root, { force: true, recursive: true });
      }
    });
  }
});
