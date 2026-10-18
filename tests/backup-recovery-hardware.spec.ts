import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve, dirname, basename } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

/** Opt-in real physical swap. The separate owned coordinator checks actual
 * mount identities before each phase; a person alone ejects/renames/reconnects.
 * This test never mounts, renames, reads drive contents, or writes either drive. */
test('physical hardware: one explicit refresh preserves identity and dated evidence', async ({
  page,
  request
}, info) => {
  test.skip(
    !process.env.ORI_BACKUP_HARDWARE_DEMO,
    'Requires an owned physical-drive acceptance run.'
  );
  test.setTimeout(160000);
  const root = resolve(import.meta.dirname, '..');
  const file = resolve(process.env.ORI_BACKUP_HARDWARE_DEMO!);
  expect(dirname(file)).toBe(resolve(root, '.tmp/backup-recovery-delivery'));
  expect(basename(file)).toMatch(/^hardware-drive-control-[0-9]+\.json$/);
  const control = JSON.parse(readFileSync(file, 'utf8'));
  expect(['absent', 'wrong', 'reconnected']).toContain(control.hardware_phase);
  expect(control.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(control.slug).toMatch(/^[a-z0-9][a-z0-9-]{0,79}$/);
  expect(new URL(info.project.use.baseURL as string).port).toBe(String(control.server_port));
  const endpoint = '/api/workspaces/' + control.workspace_id + '/backup-recovery';
  const status = async () => {
    const response = await request.get(endpoint);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).status;
  };
  const before = await status();
  expect(before).toMatchObject({
    connection: 'configured',
    monitoring: 'manual_only',
    snapshot_count: 1
  });
  expect(before.generation).toBe(control.baseline.generation);
  expect(before.last_successful_read).toBe(control.baseline.last_successful_read);
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() !== 'GET' && new URL(r.url()).pathname.startsWith(endpoint))
      mutations.push(new URL(r.url()).pathname);
  });
  await installLocalCdn(page);
  await page.goto(
    '/workspaces/' + control.slug + '?mode=details&panel=backup-recovery&tab=overview'
  );
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  // Opening/reading is not a metadata check.
  expect((await status()).sequence).toBe(before.sequence);
  const completed = page.waitForResponse(
    r => new URL(r.url()).pathname === endpoint + '/refresh' && r.request().method() === 'POST',
    { timeout: 130000 }
  );
  await dialog.getByRole('button', { name: 'Check metadata now', exact: true }).click();
  const response = await completed;
  expect(response.ok(), await response.text()).toBeTruthy();
  const operation = (await response.json()).operation;
  expect(operation.state).toBe('complete');
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  const after = await status();
  expect(after.generation).toBe(before.generation);
  expect(after.revision).toBe(before.revision);
  // Refresh reserves an admission sequence, then publishes one attempt.
  expect(after.sequence).toBe(before.sequence + 2);
  const historyResponse = await request.get(endpoint + '/history');
  expect(historyResponse.ok()).toBeTruthy();
  const history = (await historyResponse.json()).history.items;
  expect(
    history.filter((item: { sequence: number }) => item.sequence > before.sequence)
  ).toHaveLength(1);
  expect(after.monitoring).toBe('manual_only');
  expect(after.snapshot_count).toBe(1);
  expect(after.last_completed_at).toBe(before.last_completed_at);
  if (control.hardware_phase === 'reconnected') {
    expect(operation.outcome).toBe('read_ok');
    expect(after.destination).toBe('present');
    expect(Date.parse(after.last_successful_read)).toBeGreaterThan(
      Date.parse(before.last_successful_read)
    );
  } else {
    if (control.hardware_phase === 'absent') {
      expect(operation.outcome).toBe('destination_absent');
      expect(after.destination).toBe('absent');
      await expect(dialog.getByText('Disconnected', { exact: true })).toBeVisible();
    } else {
      // A different physical volume with no selected private repository must
      // fail unavailable, not manufacture a match or silently adopt the disk.
      expect(['identity_mismatch', 'read_unavailable']).toContain(operation.outcome);
      expect(['identity_mismatch', 'unavailable']).toContain(after.destination);
    }
    expect(after.last_successful_read).toBe(before.last_successful_read);
  }
  expect(mutations).toEqual([endpoint + '/refresh']);
  await page.screenshot({
    path: info.outputPath('actual-physical-' + control.hardware_phase + '.png')
  });
  await test.step('stored tabs do not repeat the metadata operation', async () => {
    await dialog.getByRole('tab', { name: 'History', exact: true }).click();
    await expect(dialog).toContainText(
      control.hardware_phase === 'absent'
        ? 'Destination disconnected'
        : control.hardware_phase === 'reconnected'
          ? 'Metadata observed'
          : operation.outcome === 'identity_mismatch'
            ? 'Identity mismatch'
            : 'Metadata unavailable'
    );
    expect((await status()).sequence).toBe(after.sequence);
  });
});

/** Reconcile an already-completed real check after a harness assertion failed.
 * Stored API/UI reads only: never repeat the metadata action to repair a test. */
test('physical hardware: stored outcome review without another metadata read', async ({
  page,
  request
}, info) => {
  test.skip(
    !process.env.ORI_BACKUP_HARDWARE_STORED_REVIEW,
    'Requires a completed owned physical check.'
  );
  const root = resolve(import.meta.dirname, '..');
  const file = resolve(process.env.ORI_BACKUP_HARDWARE_STORED_REVIEW!);
  expect(dirname(file)).toBe(resolve(root, '.tmp/backup-recovery-delivery'));
  expect(basename(file)).toMatch(/^hardware-drive-control-[0-9]+\.json$/);
  const control = JSON.parse(readFileSync(file, 'utf8'));
  expect(control.hardware_phase).toBe('absent');
  expect(control.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(control.slug).toMatch(/^[a-z0-9][a-z0-9-]{0,79}$/);
  expect(new URL(info.project.use.baseURL as string).port).toBe(String(control.server_port));
  expect(control.review_sequence).toBe(3);
  const endpoint = '/api/workspaces/' + control.workspace_id + '/backup-recovery';
  const readStatus = async () => {
    const response = await request.get(endpoint);
    expect(response.ok()).toBeTruthy();
    return (await response.json()).status;
  };
  const expected = {
    generation: control.baseline.generation,
    sequence: control.review_sequence,
    connection: 'configured',
    monitoring: 'manual_only',
    in_progress: false,
    destination: 'absent',
    snapshot_count: 1,
    last_successful_read: control.baseline.last_successful_read,
    last_attempt: { outcome: 'destination_absent', sequence: control.review_sequence }
  };
  expect(await readStatus()).toMatchObject(expected);
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() !== 'GET' && new URL(r.url()).pathname.startsWith(endpoint))
      mutations.push(r.method());
  });
  await installLocalCdn(page);
  await page.goto(
    '/workspaces/' + control.slug + '?mode=details&panel=backup-recovery&tab=overview'
  );
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  await expect(dialog.getByText('Disconnected', { exact: true })).toBeVisible();
  await expect(dialog).toContainText('A disconnected drive is not automatically a failed backup');
  await page.screenshot({ path: info.outputPath('actual-physical-absent-stored-review.png') });
  await dialog.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(dialog).toContainText('Destination disconnected');
  const historyResponse = await request.get(endpoint + '/history');
  expect(historyResponse.ok()).toBeTruthy();
  const history = (await historyResponse.json()).history.items;
  expect(history).toHaveLength(2);
  expect(history.map((item: { outcome: string }) => item.outcome)).toEqual([
    'destination_absent',
    'read_ok'
  ]);
  expect(await readStatus()).toMatchObject(expected);
  expect(mutations).toEqual([]);
});
