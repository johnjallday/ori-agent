import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve, dirname, basename } from 'node:path';
import { execFileSync } from 'node:child_process';
import { installLocalCdn } from './helpers/offline-cdn';

/** Actual product HTTP/UI over explicitly INVENTED encrypted stored metadata.
 * The worktree-local operator seeds it with a Go test overlay while Ori is off.
 * No provider read, native picker, backup-password lookup, drive or restore.
 * This is not real-provider/physical/recovery acceptance. No product API is
 * intercepted; only CDN assets are supplied locally. */
test('stored product fixture: user reports and scoped Today/Brief return to the owning console', async ({
  page,
  request
}, info) => {
  test.skip(
    !process.env.ORI_BACKUP_STORED_DEMO_CONTROL,
    'Needs the owned stored-fixture coordinator.'
  );
  test.setTimeout(120000);
  page.setDefaultTimeout(15000);
  const root = resolve(import.meta.dirname, '..');
  const file = resolve(process.env.ORI_BACKUP_STORED_DEMO_CONTROL!);
  expect(dirname(file)).toBe(resolve(root, 'tasks/evidence-backup-and-recovery'));
  expect(basename(file)).toMatch(/^backup-stored-demo-control-[0-9]+\.json$/);
  const data = JSON.parse(readFileSync(file, 'utf8'));
  expect(data.kind).toBe('invented stored metadata only');
  expect(data.workspace_id).toMatch(/^[a-f0-9-]{36}$/);
  expect(data.slug).toMatch(/^[a-z0-9][a-z0-9-]{0,79}$/);
  const base = new URL(info.project.use.baseURL as string);
  expect(['localhost', '127.0.0.1']).toContain(base.hostname);
  expect(base.port).toBe(String(data.server_port));
  const pid = process.env.ORI_BACKUP_DEMO_SERVER_PID || '';
  expect(pid).toMatch(/^[0-9]+$/);
  expect(execFileSync('/bin/ps', ['-p', pid, '-o', 'comm='], { encoding: 'utf8' }).trim()).toBe(
    resolve(root, 'bin/ori-agent')
  );
  await installLocalCdn(page);
  const endpoint = '/api/workspaces/' + data.workspace_id + '/backup-recovery';
  const status = async () => {
    const response = await request.get(endpoint);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).status;
  };
  const before = await status();
  expect(before).toMatchObject({
    connection: 'configured',
    runtime_available: false,
    monitoring: 'manual_only',
    snapshot_count: 1
  });
  expect(before.health.observation).toBe('stale');
  expect(before.health.recency).toBe('overdue');
  const requests: Array<{ path: string; body: Record<string, unknown> }> = [];
  page.on('request', r => {
    const path = new URL(r.url()).pathname;
    if (r.method() !== 'GET' && path.startsWith(endpoint))
      requests.push({ path, body: r.postDataJSON() });
  });
  const caption = async () => {
    await page.evaluate(() => {
      if (document.getElementById('storedFixtureCaption')) return;
      const caption = document.createElement('div');
      caption.id = 'storedFixtureCaption';
      caption.textContent =
        'INVENTED STORED FIXTURE · illustrative user report · no provider read or restore performed';
      caption.style.cssText =
        'position:fixed;bottom:0;left:0;right:0;z-index:999999;background:#ffe6aa;color:#332500;padding:7px;font:12px system-ui;text-align:center';
      document.body.append(caption);
    });
  };
  await page.goto('/workspaces/' + data.slug + '?mode=details&panel=backup-recovery&tab=recovery');
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('Stored observation: stale');
  await dialog.getByText('External recovery guide · no model required', { exact: true }).click();
  await expect(dialog).toContainText('separate empty recovery target');
  const initialReports = (await (await request.get(endpoint + '/recovery')).json()).recovery
    .reports;
  const expectedWrites = initialReports.length === 0 ? 1 : 0;
  expect(initialReports.length).toBeLessThanOrEqual(1);
  await expect(dialog.locator('#brReportConsent')).not.toBeChecked();
  if (expectedWrites) {
    await dialog
      .getByLabel('Test date (UTC)', { exact: true })
      .fill(before.server_time.slice(0, 10));
    await dialog
      .getByRole('combobox', { name: 'Your reported result', exact: true })
      .selectOption('user_reported_failed');
    await dialog.locator('#brReportConsent').check();
    await dialog.getByRole('button', { name: 'Record user-reported result', exact: true }).click();
    await expect(dialog).toContainText(
      'User report saved. Ori did not perform or verify a restore.'
    );
  }
  await expect(dialog.getByLabel('Test date (UTC)', { exact: true })).toBeEnabled();
  await caption();
  await expect(
    dialog.getByRole('button', {
      name: 'Edit report dated ' + before.server_time.slice(0, 10),
      exact: true
    })
  ).toBeVisible();
  await page.screenshot({
    path: info.outputPath('stored-fixture-user-report.png'),
    fullPage: true
  });
  expect(requests).toHaveLength(expectedWrites);
  if (expectedWrites) {
    expect(requests[0].path).toBe(endpoint + '/recovery-report');
    const replay = await request.post(endpoint + '/recovery-report', {
      data: requests[0].body,
      headers: { Origin: base.origin, 'X-Ori-Backup-Action': 'user-control' }
    });
    expect(replay.ok(), await replay.text()).toBeTruthy();
  }
  const date = before.server_time.slice(0, 10);
  await dialog.getByRole('button', { name: 'Edit report dated ' + date, exact: true }).click();
  await expect(dialog.locator('#brReportConsent')).not.toBeChecked();
  await dialog.getByRole('button', { name: 'Cancel report edit', exact: true }).click();
  expect(requests).toHaveLength(expectedWrites);

  // Explicit fixture preparation for the EXISTING Personal Assistant surfaces,
  // not an extra coordinator/home created by the backup template or reader.
  let relationship = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  if (relationship.state === 'needs_hire') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'stored-backup-demo-hire',
        if_version: 0,
        display_name: 'Atlas',
        mandate: 'Review my day.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    relationship = (await hire.json()).personal_assistant;
  }
  if (!relationship.hq_workspace_id) {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'stored-backup-demo-hq',
        if_version: relationship.state_version,
        name: 'Stored-fixture HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }
  const scope = await request.put('/api/personal-hq/brief/config', {
    data: {
      timezone: 'UTC',
      schedule_days: ['mon'],
      schedule_time: '08:00',
      schedule_enabled: false,
      scope: 'selected',
      selected_workspace_ids: [data.workspace_id],
      include_future_workspaces: false,
      notify_on_ready: false
    }
  });
  expect(scope.ok(), await scope.text()).toBeTruthy();
  const readToday = async () =>
    (await (await request.get('/api/personal-assistant/today')).json()).today;
  const today = await readToday();
  const backupItems = today.needs_you.items.filter(
    (item: { ref?: { entity_type?: string } }) => item.ref?.entity_type === 'backup_observation'
  );
  expect(backupItems).toHaveLength(1);
  expect(backupItems[0].detail).toContain('overdue observed completion');
  expect(
    (await readToday()).needs_you.items.filter(
      (item: { ref?: { entity_type?: string } }) => item.ref?.entity_type === 'backup_observation'
    )
  ).toHaveLength(1);
  const capabilities = await (await request.get('/api/personal-assistant/capabilities')).json();
  expect(
    capabilities.capabilities.cards.find((card: { key: string }) => card.key === 'backup-recovery')
  ).toMatchObject({ mapped_write: false, action_route: backupItems[0].route });
  const generation = await request.post('/api/personal-hq/brief/refresh');
  expect(generation.ok(), await generation.text()).toBeTruthy();
  let revision: { content_json: string } | undefined;
  await expect
    .poll(
      async () => {
        const result = await (await request.get('/api/personal-hq/brief/current')).json();
        revision = result.revision;
        return !!revision?.content_json.includes('backup_host_rendered');
      },
      { timeout: 30000 }
    )
    .toBeTruthy();
  const content = JSON.parse(revision!.content_json);
  expect(content.backup_host_rendered).toBe(true);
  expect(JSON.stringify(content)).toContain('user reports sample recovery failed');
  expect(content.data_gaps.join(' ')).toContain('Backup & Recovery');
  for (const forbidden of [
    '/Invented/Project',
    '/Volumes/Invented',
    'invented-vault',
    'invented-record',
    'a'.repeat(64)
  ])
    expect(JSON.stringify(content)).not.toContain(forbidden);

  await page.goto('/?panel=today');
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
  // The unrelated first-folder invitation records its own shown receipt;
  // reload after it has been shown, without scanning/choosing any folder.
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant/folder-digest')).json()).folder_digest
          .prompt_first_folder
    )
    .toBe(false);
  await page.reload();
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
  const summary = page.locator('#personalAssistantSummaryToggle');
  await expect(summary).toBeVisible();
  if ((await summary.getAttribute('aria-expanded')) !== 'true') await summary.click();
  await expect(page.locator('#personalAssistantTodaySections')).toBeVisible();
  const queue = page.locator('#personalAssistantNeedsYouQueue');
  await expect(queue).toBeVisible();
  if ((await queue.getAttribute('open')) === null)
    await queue.locator('#personalAssistantNeedsYouQueueTitle').click();
  const link = page
    .locator('#personalAssistantNeedsYouItems a')
    .filter({ hasText: /sample-recovery report/ });
  await expect(link).toHaveCount(1);
  await expect(link).toBeVisible();
  await link.focus();
  await link.scrollIntoViewIfNeeded();
  await caption();
  await page
    .locator('#personalAssistantPanel')
    .screenshot({ path: info.outputPath('stored-fixture-today-needs-you.png') });
  console.log('OWNED_BACKUP_LINK', await link.getAttribute('href'));
  await link.click();
  try {
    await expect(dialog).toBeVisible();
  } catch (error) {
    console.log(
      'OWNED_BACKUP_NAV_DIAG',
      await page.evaluate(() => ({
        url: location.href,
        navigation: (performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming)
          ?.name,
        controller: !!(window as any).BackupRecoveryConsole,
        open: (window as any).BackupRecoveryConsole?.isOpen(),
        workspace: (window as any).currentWorkspaceId,
        installed: (window as any).WorkspaceCapabilities?.isInstalled('backup-recovery')
      }))
    );
    throw error;
  }
  await expect(dialog.getByRole('tab', { name: 'Overview', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  await page.goto('/?panel=today');
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
  await expect(summary).toBeVisible();
  if ((await summary.getAttribute('aria-expanded')) !== 'true') await summary.click();
  await expect(page.locator('#personalAssistantBriefRow')).toBeVisible();
  await page.locator('#personalAssistantBriefRow').click();
  const brief = page.locator('.daily-brief-station');
  await expect(brief).toBeVisible();
  await expect(brief).toContainText('user reports sample recovery failed');
  await caption();
  await page.screenshot({
    path: info.outputPath('stored-fixture-daily-brief-gap.png'),
    fullPage: true
  });
  await brief.locator('a[href*="panel=backup-recovery"]').first().click();
  await expect(dialog).toBeVisible();
  const after = await status();
  expect(after.sequence).toBe(before.sequence);
  expect(after.last_successful_read).toBe(before.last_successful_read);
  expect(after.monitoring).toBe('manual_only');
  const reports = (await (await request.get(endpoint + '/recovery')).json()).recovery.reports;
  expect(reports).toHaveLength(1);
  expect(reports[0].result).toBe('user_reported_failed');
  expect(requests).toHaveLength(expectedWrites);
});
