import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { execFileSync } from 'node:child_process';
import { installLocalCdn } from './helpers/offline-cdn';

test.afterEach(() => {
  if (!process.env.ORI_BACKUP_DRIVE_DEMO) return;
  const serverPID = process.env.ORI_BACKUP_DEMO_SERVER_PID || '';
  if (!/^\d+$/.test(serverPID)) return;
  const root = resolve(import.meta.dirname, '..');
  let children: string[];
  try {
    if (
      execFileSync('/bin/ps', ['-p', serverPID, '-o', 'comm='], { encoding: 'utf8' }).trim() !==
      resolve(root, 'bin/ori-agent')
    )
      return;
    children = execFileSync('/usr/bin/pgrep', ['-P', serverPID, '-x', 'osascript'], {
      encoding: 'utf8'
    })
      .trim()
      .split(/\s+/)
      .filter(Boolean);
  } catch (error) {
    if ((error as { status?: number }).status === 1) return;
    throw error;
  }
  for (const child of children)
    execFileSync(
      '/usr/bin/osascript',
      [resolve(root, 'tests/helpers/backup-native-picker.applescript'), child, 'cancel', ''],
      { timeout: 15000, stdio: ['ignore', 'pipe', 'pipe'] }
    );
});

/** OPT-IN physical product demo. The operator prepares an invented backup in
 * the approved disposable folder and an encrypted sandbox Vault reference.
 * No password is present in the control file, browser form or test output.
 * Native chooser automation is real OS UI, not a forged picker token. */
test('designated drive: real choices, cancellation, scoped reference failure and one approved product read', async ({
  page,
  request
}, testInfo) => {
  test.skip(
    !process.env.ORI_BACKUP_DRIVE_DEMO,
    'Needs the explicitly approved disposable-drive operator control.'
  );
  test.setTimeout(200000);
  const root = resolve(import.meta.dirname, '..');
  const control = JSON.parse(readFileSync(process.env.ORI_BACKUP_DRIVE_DEMO!, 'utf8'));
  const serverPID = process.env.ORI_BACKUP_DEMO_SERVER_PID || '';
  expect(serverPID).toMatch(/^\d+$/);
  expect(
    execFileSync('/bin/ps', ['-p', serverPID, '-o', 'comm='], { encoding: 'utf8' }).trim()
  ).toBe(resolve(root, 'bin/ori-agent'));
  expect(control.volume).toMatch(/^\/Volumes\/[^/\r\n]+$/);
  const sourceParent = resolve(control.source, '../..');
  expect([
    resolve(root, 'tasks/evidence-backup-and-recovery/external-restic-probe'),
    resolve(root, '.tmp/backup-recovery-delivery/external-restic-probe')
  ]).toContain(sourceParent);
  expect(control.source.slice(sourceParent.length)).toMatch(/^\/invented-[a-z0-9_]+\/source$/);
  expect(control.repository.startsWith(control.volume + '/Ori Backup Test/invented-')).toBeTruthy();
  expect(control.repository.slice(control.volume.length)).toMatch(
    /^\/Ori Backup Test\/invented-[a-z0-9_]+\/repository$/
  );
  expect(new URL(testInfo.project.use.baseURL as string).port).toBe(String(control.server_port));
  const nativeChild = () => {
    try {
      const ids = execFileSync('/usr/bin/pgrep', ['-P', serverPID, '-x', 'osascript'], {
        encoding: 'utf8'
      })
        .trim()
        .split(/\s+/)
        .filter(Boolean);
      expect(ids).toHaveLength(1);
      return ids[0];
    } catch (error) {
      if ((error as { status?: number }).status === 1) return '';
      throw error;
    }
  };
  const native = async (action: 'cancel' | 'choose', path = '') => {
    await expect.poll(nativeChild, { timeout: 10000 }).not.toBe('');
    execFileSync(
      '/usr/bin/osascript',
      [
        resolve(root, 'tests/helpers/backup-native-picker.applescript'),
        nativeChild(),
        action,
        path
      ],
      { timeout: 15000, stdio: ['ignore', 'pipe', 'pipe'] }
    );
    await expect.poll(nativeChild, { timeout: 5000 }).toBe('');
  };
  await installLocalCdn(page);
  await page.goto(
    '/workspaces/' +
      encodeURIComponent(control.slug) +
      '?mode=details&panel=backup-recovery&tab=settings'
  );
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  const base = '/api/workspaces/' + control.workspace_id + '/backup-recovery';
  const getStatus = async () => {
    const response = await request.get(base);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).status;
  };
  expect(await getStatus()).toMatchObject({
    connection: 'not_configured',
    runtime_available: true
  });
  await dialog.getByRole('button', { name: 'Choose source folder', exact: true }).click();
  await native('cancel');
  await expect(dialog).toContainText('0 source selections');
  await dialog.screenshot({ path: testInfo.outputPath('actual-native-choice-cancelled.png') });
  for (const [label, path] of [
    ['Choose source folder', control.source],
    ['Choose external volume root', control.volume],
    ['Choose existing repository', control.repository]
  ]) {
    const selected = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === base + '/selection' &&
        response.request().method() === 'POST'
    );
    await dialog.getByRole('button', { name: label, exact: true }).click();
    await native('choose', path);
    const response = await selected;
    expect(response.ok(), await response.text()).toBeTruthy();
    expect((await response.json()).selection.selected).toBe(true);
  }
  await dialog.getByLabel('Selected Vault ID — not a password').fill(control.vault_id);
  await dialog.getByLabel('Selected workspace secret record ID').fill('missing-invented-record');
  await dialog.getByRole('button', { name: 'Describe selected reference' }).click();
  await expect(dialog.getByRole('alert')).toBeVisible();
  expect((await getStatus()).connection).toBe('not_configured');
  await dialog.screenshot({
    path: testInfo.outputPath('actual-selected-reference-unavailable.png'),
    mask: [dialog.locator('#brVaultID'), dialog.locator('#brRecordID')]
  });
  await dialog.getByLabel('Selected workspace secret record ID').fill(control.record_id);
  await dialog.getByRole('button', { name: 'Describe selected reference' }).click();
  const reviewed = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === base + '/review' &&
      response.request().method() === 'POST'
  );
  await dialog.getByRole('button', { name: 'Review metadata access', exact: true }).click();
  expect((await (await reviewed).json()).review).toMatchObject({
    source_paths: [control.source],
    volume_path: control.volume,
    repository_path: control.repository
  });
  const consent = dialog.getByLabel(
    'Allow one bounded metadata read of this exact selection. Do not run a backup or restore.',
    { exact: true }
  );
  await expect(consent).not.toBeChecked();
  await dialog.getByRole('button', { name: 'Cancel review', exact: true }).click();
  expect((await getStatus()).connection).toBe('not_configured');
  await dialog.getByRole('button', { name: 'Review metadata access', exact: true }).click();
  await expect(consent).not.toBeChecked();
  const completed = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === base + '/connect' &&
      response.request().method() === 'POST',
    { timeout: 130000 }
  );
  await consent.check();
  await dialog.getByRole('button', { name: 'Approve one metadata read', exact: true }).click();
  const response = await completed;
  expect(response.ok(), await response.text()).toBeTruthy();
  expect((await response.json()).operation).toMatchObject({
    state: 'complete',
    outcome: 'read_ok'
  });
  await dialog.getByRole('tab', { name: 'Overview', exact: true }).click();
  await expect(dialog).toContainText('Metadata connection configured');
  const status = await getStatus();
  expect(status).toMatchObject({
    connection: 'configured',
    destination: 'present',
    monitoring: 'manual_only',
    snapshot_count: 1,
    evidence_current_generation: true
  });
  expect(status.last_successful_read).toBeTruthy();
  expect(status.last_completed_at).toBeTruthy();
  await dialog.screenshot({ path: testInfo.outputPath('actual-provider-first-observation.png') });
  await dialog.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(dialog).toContainText('Metadata observed');
  await dialog.screenshot({ path: testInfo.outputPath('actual-provider-history.png') });
  await testInfo.attach('actual-product-observation', {
    body: JSON.stringify({
      kind: 'actual product read of operator-prepared invented snapshot on designated drive',
      connection: status.connection,
      destination: status.destination,
      snapshot_count: status.snapshot_count,
      last_successful_read: status.last_successful_read,
      last_completed_at: status.last_completed_at,
      monitoring: status.monitoring,
      recovery_verified: false
    }),
    contentType: 'application/json'
  });
});

/** The separate operator must have actually restored and byte-compared one
 * invented sample first. This case records its explicitly user-reported result;
 * the product/browser never runs restic or reads recovered contents. */
test('external sample recovery: record a dated operator report without another metadata read', async ({
  page,
  request
}, info) => {
  test.skip(
    !process.env.ORI_BACKUP_EXTERNAL_RECOVERY_DEMO,
    'Needs a completed external sample recovery.'
  );
  const root = resolve(import.meta.dirname, '..');
  const file = resolve(process.env.ORI_BACKUP_EXTERNAL_RECOVERY_DEMO!);
  expect(resolve(file, '..')).toBe(resolve(root, '.tmp/backup-recovery-delivery'));
  const control = JSON.parse(readFileSync(file, 'utf8'));
  expect(control.recovery).toMatchObject({
    executor: 'external_restic_operator',
    invented_sample_compared: true,
    separate_empty_target: true,
    repository_unchanged: true,
    source_unchanged: true
  });
  expect(control.recovery.snapshot_id).toMatch(/^[a-f0-9]{64}$/);
  expect(control.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(control.slug).toMatch(/^[a-z0-9][a-z0-9-]{0,79}$/);
  expect(new URL(info.project.use.baseURL as string).port).toBe(String(control.server_port));
  const endpoint = '/api/workspaces/' + control.workspace_id + '/backup-recovery';
  const getStatus = async () => {
    const response = await request.get(endpoint);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).status;
  };
  const before = await getStatus();
  expect(before).toMatchObject({
    connection: 'configured',
    runtime_available: true,
    destination: 'present',
    monitoring: 'manual_only',
    snapshot_count: 1
  });
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() !== 'GET' && new URL(r.url()).pathname.startsWith(endpoint))
      mutations.push(new URL(r.url()).pathname);
  });
  await installLocalCdn(page);
  await page.goto(
    '/workspaces/' + control.slug + '?mode=details&panel=backup-recovery&tab=recovery'
  );
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('aria-busy', 'false');
  await dialog.getByText('External recovery guide · no model required', { exact: true }).click();
  await expect(dialog).toContainText('separate empty recovery target');
  await expect(dialog.locator('#brReportConsent')).not.toBeChecked();
  await dialog
    .getByLabel('Test date (UTC)', { exact: true })
    .fill(control.recovery.tested_at.slice(0, 10));
  await dialog
    .getByRole('combobox', { name: 'Your reported result', exact: true })
    .selectOption('user_reported_passed');
  await dialog
    .getByRole('combobox', { name: 'Snapshot association', exact: true })
    .selectOption(control.recovery.snapshot_id);
  await dialog
    .getByRole('combobox', { name: 'Optional note — predefined choices only', exact: true })
    .selectOption('sample_compared');
  await dialog.locator('#brReportConsent').check();
  await dialog.getByRole('button', { name: 'Record user-reported result', exact: true }).click();
  await expect(dialog).toContainText('User report saved. Ori did not perform or verify a restore.');
  await expect(dialog.getByLabel('Test date (UTC)', { exact: true })).toBeEnabled();
  await expect(dialog.locator('.br-reports')).toContainText('User reports passed');
  await dialog
    .locator('.br-reports')
    .screenshot({ path: info.outputPath('actual-external-sample-user-report.png') });
  const response = await request.get(endpoint + '/recovery');
  expect(response.ok(), await response.text()).toBeTruthy();
  const recovery = (await response.json()).recovery;
  expect(recovery.reports).toHaveLength(1);
  expect(recovery.reports[0]).toMatchObject({
    result: 'user_reported_passed',
    snapshot_id: control.recovery.snapshot_id,
    note_code: 'sample_compared'
  });
  const after = await getStatus();
  expect(after.sequence).toBe(before.sequence);
  expect(after.last_successful_read).toBe(before.last_successful_read);
  expect(after.monitoring).toBe('manual_only');
  expect(mutations).toEqual([endpoint + '/recovery-report']);
});
