import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname, basename } from 'node:path';
import { execFileSync } from 'node:child_process';
import { installLocalCdn } from './helpers/offline-cdn';

/** OPT-IN actual product acceptance, not an intercepted prototype. The owned
 * operator coordinator creates only an invented repository/Vault on the approved
 * disposable drive, keeps invented credentials outside browser controls,
 * restarts Ori and unlocks the fixture Vault through its normal user API.
 * No test password reaches the page.
 * Run the two phases in order through the coordinator, never normal CI. */
function control() {
  test.skip(!process.env.ORI_BACKUP_DRIVE_DEMO, 'Needs the designated disposable-drive operator.');
  const root = resolve(import.meta.dirname, '..');
  const path = resolve(process.env.ORI_BACKUP_DRIVE_DEMO!);
  expect([
    resolve(root, 'tasks/evidence-backup-and-recovery'),
    resolve(root, '.tmp/backup-recovery-delivery')
  ]).toContain(dirname(path));
  expect(basename(path)).toMatch(/^monitor-drive-demo-control-[0-9]+\.json$/);
  const data = JSON.parse(readFileSync(path, 'utf8'));
  expect(data.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(data.volume).toMatch(/^\/Volumes\/[^/\r\n]+$/);
  const sourceParent = resolve(data.source, '../..');
  expect([
    resolve(root, 'tasks/evidence-backup-and-recovery/external-restic-probe'),
    resolve(root, '.tmp/backup-recovery-delivery/external-restic-probe')
  ]).toContain(sourceParent);
  expect(data.source.slice(sourceParent.length)).toMatch(/^\/invented-[a-z0-9_]+\/source$/);
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
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  return dialog;
}

test('live final walkthrough: review retained selections and open recovery without inventing a report', async ({
  page,
  request
}, info) => {
  test.skip(!process.env.ORI_BACKUP_FINAL_DEMO, 'Requires the owned final walkthrough.');
  const { data } = control();
  test.setTimeout(160000);
  const before = await status(request, data.workspace_id);
  expect(before.connection).toBe('review_required');
  const dialog = await open(page, data.slug);
  await dialog
    .getByLabel('Use the saved folder selection (known identity bindings are retained)', {
      exact: true
    })
    .check();
  await dialog.getByRole('button', { name: 'Describe selected reference', exact: true }).click();
  const reviewed = page.waitForResponse(
    r =>
      new URL(r.url()).pathname.endsWith('/backup-recovery/review') &&
      r.request().method() === 'POST'
  );
  await dialog.getByRole('button', { name: 'Review metadata access', exact: true }).click();
  const reviewResponse = await reviewed;
  expect(reviewResponse.ok(), await reviewResponse.text()).toBeTruthy();
  expect((await reviewResponse.json()).review).toMatchObject({
    source_paths: [data.source],
    volume_path: data.volume,
    repository_path: data.repository
  });
  const consent = dialog.locator('#brReadConsent');
  await expect(consent).not.toBeChecked();
  const completed = page.waitForResponse(
    r =>
      new URL(r.url()).pathname.endsWith('/backup-recovery/connect') &&
      r.request().method() === 'POST',
    { timeout: 130000 }
  );
  await consent.check();
  await dialog.getByRole('button', { name: 'Approve one metadata read', exact: true }).click();
  const response = await completed;
  expect(response.ok(), await response.text()).toBeTruthy();
  expect((await response.json()).operation.outcome).toBe('read_ok');
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  const connected = await status(request, data.workspace_id);
  expect(connected).toMatchObject({
    connection: 'configured',
    monitoring: 'manual_only',
    destination: 'present',
    snapshot_count: 1
  });
  expect(connected.last_successful_read).not.toBe(before.last_successful_read);
  await dialog.getByRole('tab', { name: 'Recovery', exact: true }).click();
  await expect(dialog).toContainText('Ori does not restore or inspect recovered contents.');
  const recoveryResponse = await request.get(
    '/api/workspaces/' + data.workspace_id + '/backup-recovery/recovery'
  );
  expect(recoveryResponse.ok()).toBeTruthy();
  const recovery = (await recoveryResponse.json()).recovery;
  expect(recovery.reports).toEqual([]);
  expect((await status(request, data.workspace_id)).sequence).toBe(connected.sequence);
  await dialog.screenshot({ path: info.outputPath('actual-final-recovery-not-tested.png') });
});

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
  const initialHistoryResponse = await request.get(
    '/api/workspaces/' + data.workspace_id + '/backup-recovery/history'
  );
  expect(initialHistoryResponse.ok()).toBeTruthy();
  const initialHistoryCount = (await initialHistoryResponse.json()).history.items.length;
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
  await expect(dialog.locator('.br-history li')).toHaveCount(Math.min(25, initialHistoryCount + 1));
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

test('live final walkthrough: remove Ori configuration without another metadata check', async ({
  page,
  request
}, info) => {
  test.skip(!process.env.ORI_BACKUP_FINAL_DEMO, 'Requires the owned final walkthrough.');
  const { data } = control();
  const before = await status(request, data.workspace_id);
  expect(before).toMatchObject({
    connection: 'review_required',
    monitoring: 'manual_only',
    in_progress: false
  });
  const dialog = await open(page, data.slug);
  const endpoint = '/api/workspaces/' + data.workspace_id + '/backup-recovery';
  const completed = page.waitForResponse(
    r => new URL(r.url()).pathname === endpoint && r.request().method() === 'DELETE'
  );
  await dialog.getByRole('button', { name: 'Remove Ori configuration', exact: true }).click();
  await dialog.getByRole('button', { name: 'Confirm removal', exact: true }).click();
  const response = await completed;
  expect(response.ok(), await response.text()).toBeTruthy();
  expect((await response.json()).operation.outcome).toBe('applied');
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  expect(await status(request, data.workspace_id)).toMatchObject({
    connection: 'not_configured',
    monitoring: 'manual_only',
    sequence: before.sequence,
    last_successful_read: before.last_successful_read
  });
  const settingsResponse = await request.get(endpoint + '/settings');
  expect(settingsResponse.ok()).toBeTruthy();
  const settings = (await settingsResponse.json()).settings;
  expect(settings.source_paths).toEqual([]);
  expect(settings.credential).toBeUndefined();
  await dialog.getByRole('tab', { name: 'Overview', exact: true }).click();
  await dialog.screenshot({ path: info.outputPath('actual-final-configuration-removed.png') });
});
