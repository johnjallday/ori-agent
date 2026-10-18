import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname, basename } from 'node:path';
import { execFileSync } from 'node:child_process';
import { installLocalCdn } from './helpers/offline-cdn';

/** OPT-IN actual product acceptance, not an intercepted prototype. The owned
 * operator coordinator creates only an invented repository/Vault on the approved
 * disposable drive, retains passwords in memory, restarts Ori and unlocks the
 * fixture Vault through its normal user API. No test password reaches the page.
 * Run the two phases in order through the coordinator, never normal CI. */
function control() {
  test.skip(!process.env.ORI_BACKUP_DRIVE_DEMO, 'Needs the designated disposable-drive operator.');
  const root = resolve(import.meta.dirname, '..');
  const path = resolve(process.env.ORI_BACKUP_DRIVE_DEMO!);
  expect(dirname(path)).toBe(resolve(root, 'tasks/evidence-backup-and-recovery'));
  expect(basename(path)).toMatch(/^monitor-drive-demo-control-[0-9]+\.json$/);
  const data = JSON.parse(readFileSync(path, 'utf8'));
  expect(data.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(data.volume).toMatch(/^\/Volumes\/[^/\r\n]+$/);
  expect(
    data.source.startsWith(
      resolve(root, 'tasks/evidence-backup-and-recovery/external-restic-probe/invented-')
    )
  ).toBeTruthy();
  expect(data.repository.startsWith(data.volume + '/Ori Backup Test/invented-')).toBeTruthy();
  expect(data.repository.slice(data.volume.length)).toMatch(
    /^\/Ori Backup Test\/invented-[a-z0-9_]+\/repository$/
  );
  const url = new URL(test.info().project.use.baseURL as string);
  expect(['localhost', '127.0.0.1']).toContain(url.hostname);
  expect(url.port).toBe(String(data.server_port));
  const pid = process.env.ORI_BACKUP_DEMO_SERVER_PID || '';
  expect(pid).toMatch(/^[0-9]+$/);
  expect(execFileSync('/bin/ps', ['-p', pid, '-o', 'comm='], { encoding: 'utf8' }).trim()).toBe(
    resolve(root, 'bin/ori-agent')
  );
  return { data, path };
}
async function status(request: APIRequestContext, id: string) {
  const response = await request.get('/api/workspaces/' + id + '/backup-recovery');
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).status;
}
async function open(page: Page, slug: string) {
  await installLocalCdn(page);
  await page.goto(
    '/workspaces/' + encodeURIComponent(slug) + '?mode=details&panel=backup-recovery&tab=settings'
  );
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  return dialog;
}

test('live monitoring: opt-in, observed history, pause and fresh resume', async ({
  page,
  request
}, info) => {
  const { data, path } = control();
  test.setTimeout(200000);
  page.setDefaultTimeout(15000);
  const before = await status(request, data.workspace_id);
  expect(before).toMatchObject({
    connection: 'configured',
    monitoring: 'manual_only',
    snapshot_count: 1
  });
  const dialog = await open(page, data.slug);
  await dialog.getByRole('combobox', { name: 'Check cadence', exact: true }).selectOption('900');
  const enable = async () => {
    await dialog
      .getByRole('button', { name: 'Review periodic metadata policy', exact: true })
      .click();
    const consent = dialog.locator('#brPeriodicConsent');
    await expect(consent).not.toBeChecked();
    await expect(
      dialog.getByRole('button', { name: 'Enable periodic metadata checks' })
    ).toBeDisabled();
    await consent.check();
    await dialog.getByRole('button', { name: 'Enable periodic metadata checks' }).click();
    await expect(dialog).toContainText('Periodic checks active');
  };
  await enable();
  await expect
    .poll(
      async () => {
        const value = await status(request, data.workspace_id);
        return (
          !value.in_progress &&
          value.sequence === before.sequence + 2 &&
          value.last_attempt?.outcome === 'read_ok'
        );
      },
      { timeout: 100000, intervals: [500, 1000, 2000] }
    )
    .toBeTruthy();
  const first = await status(request, data.workspace_id);
  expect(first).toMatchObject({ monitoring: 'active', destination: 'present', snapshot_count: 1 });
  expect(first.last_successful_read).not.toBe(before.last_successful_read);
  await page.reload();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(dialog.locator('.br-history li')).toHaveCount(2);
  await dialog.screenshot({ path: info.outputPath('actual-periodic-observed-history.png') });
  await dialog.getByRole('tab', { name: 'Settings', exact: true }).click();
  await dialog.getByRole('button', { name: 'Pause periodic checks', exact: true }).click();
  await expect(dialog).toContainText('Periodic checks paused');
  await dialog
    .locator('.br-monitoring')
    .screenshot({ path: info.outputPath('actual-periodic-paused.png') });
  await page.waitForTimeout(35000); // Cross one real poll, not a fixture clock.
  const paused = await status(request, data.workspace_id);
  expect(paused.sequence).toBe(first.sequence);
  expect(paused.last_successful_read).toBe(first.last_successful_read);
  await enable();
  await expect
    .poll(
      async () => {
        const value = await status(request, data.workspace_id);
        return (
          !value.in_progress &&
          value.sequence === first.sequence + 2 &&
          value.last_attempt?.outcome === 'read_ok'
        );
      },
      { timeout: 100000, intervals: [500, 1000, 2000] }
    )
    .toBeTruthy();
  const resumed = await status(request, data.workspace_id);
  const settings = await (
    await request.get('/api/workspaces/' + data.workspace_id + '/backup-recovery/settings')
  ).json();
  expect(settings.settings.monitoring).toMatchObject({
    intent: 'active',
    consent_valid: true,
    policy: { cadence_seconds: 900 }
  });
  writeFileSync(
    path + '.monitor-state.json',
    JSON.stringify({
      generation: resumed.generation,
      sequence: resumed.sequence,
      last_successful_read: resumed.last_successful_read
    }),
    { mode: 0o600 }
  );
  await test.step('stored facts retain explicit proof limits', async () => {
    expect(resumed).toMatchObject({ snapshot_count: 1, evidence_current_generation: true });
    expect(resumed.health.coverage).toBe('provider_declared_historical');
  });
});

test('live monitoring: restart retains consent without duplicate read; revoke stops access', async ({
  page,
  request
}, info) => {
  const { data, path } = control();
  test.setTimeout(100000);
  page.setDefaultTimeout(15000);
  const prior = JSON.parse(readFileSync(path + '.monitor-state.json', 'utf8'));
  const restarted = await status(request, data.workspace_id);
  expect(restarted).toMatchObject({ ...prior, connection: 'configured', monitoring: 'active' });
  const dialog = await open(page, data.slug);
  await expect(dialog).toContainText('Periodic checks active');
  await page.waitForTimeout(35000);
  expect(await status(request, data.workspace_id)).toMatchObject(prior);
  await dialog
    .locator('.br-monitoring')
    .screenshot({ path: info.outputPath('actual-periodic-restarted.png') });
  await dialog.getByRole('button', { name: 'Revoke metadata access', exact: true }).click();
  await dialog.getByRole('button', { name: 'Confirm revocation', exact: true }).click();
  await expect
    .poll(async () => (await status(request, data.workspace_id)).connection)
    .toBe('review_required');
  const revoked = await status(request, data.workspace_id);
  expect(revoked).toMatchObject({
    monitoring: 'manual_only',
    evidence_current_generation: false,
    last_successful_read: prior.last_successful_read
  });
  await expect(dialog.getByRole('button', { name: 'Confirm revocation', exact: true })).toHaveCount(
    0
  );
  await dialog.getByRole('tab', { name: 'Overview', exact: true }).click();
  await expect(dialog).toContainText('Local review required');
  await dialog.screenshot({ path: info.outputPath('actual-periodic-revoked.png') });
  await page.waitForTimeout(35000);
  expect(await status(request, data.workspace_id)).toMatchObject({
    generation: revoked.generation,
    sequence: revoked.sequence,
    last_successful_read: prior.last_successful_read
  });
  await test.info().attach('actual-periodic-acceptance', {
    body: JSON.stringify({
      kind: 'actual isolated product, invented snapshot on designated disposable drive',
      periodic_checks: 2,
      restart_duplicate_checks: 0,
      post_revoke_checks: 0,
      restore_performed: false
    }),
    contentType: 'application/json'
  });
});
