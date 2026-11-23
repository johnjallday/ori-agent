import { expect, test } from '@playwright/test';
import { mkdir, symlink, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { imageOnlyDocument } from './helpers/image-only-document';
import { installLocalCdn } from './helpers/offline-cdn';

// scripts/e2e-fresh.sh --sandbox-env ORI_KNOWN_FOLDERS_SANDBOX tests/documents-organization-review.spec.ts
const sandbox = process.env.ORI_KNOWN_FOLDERS_SANDBOX;
const evidence = resolve('tasks/evidence-documents-organization-review');

test('return directly to the Documents lead after a normal hire', async ({ page, request }) => {
  test.skip(!sandbox, 'requires fresh isolated server HOME');
  const root = test.info().outputPath('Documents');
  await mkdir(root, { recursive: true, mode: 0o750 });
  await symlink(root, join(sandbox!, 'Documents'), 'dir');
  const liveModel = process.env.ORI_DOCUMENT_REVIEW_LIVE_MODEL;
  await writeFile(join(root, 'scan-0010.pdf'), imageOnlyDocument(), { mode: 0o600 });
  const fixtures = {
    'estimate.txt': 'Kitchen remodel. Estimate for Maple Street. Cabinet order PO-4387.',
    'packing-slip.txt':
      'Kitchen remodel. Cabinet order PO-4387. Deliver the oak cabinets to Maple Street.',
    'scan-0008.txt': 'Invoice for PO-4387. Kitchen remodel. Balance due for the oak cabinets.',
    'scan-0009.txt': 'Invoice for AC-5580. Replace the air conditioner at Oak Avenue.'
  };
  for (const [name, body] of Object.entries(fixtures))
    await writeFile(join(root, name), body, { mode: 0o600 });
  if (liveModel) {
    expect(
      (
        await request.post('/api/settings/system-model', {
          data: { provider: 'claude', model: liveModel }
        })
      ).ok()
    ).toBe(true);
  }
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  const { personal_assistant } = await (await request.get('/api/personal-assistant')).json();
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'review-hire',
      if_version: personal_assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'review-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status()).toBe(201);
  await page.goto('/?panel=today&folder=show');
  const picker = page.locator('#personalAssistantFolderPathEntry');
  await picker.locator('input').fill(root);
  await picker.getByRole('button').click();
  const card = page.locator('#personalAssistantFolderAgent');
  await expect(card).toContainText('Documents is where you keep things.');
  await card.locator('[data-folder-agent-action="start"]').click();
  const view = async () =>
    (await (await request.get('/api/personal-assistant/folder-agent')).json()).folder_agents.find(
      (v: { folder_name: string }) => v.folder_name === 'Documents'
    );
  await expect.poll(async () => (await view())?.state).toBe('reported');
  await page.goto((await view()).route);
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await close.isVisible()) await close.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  await expect(report).toContainText('Documents stays a folder with its agent');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await report.screenshot({ path: join(evidence, 'direct-return.png') });
  const offer = report.getByRole('button', { name: 'Read documents and suggest groups' });
  await offer.click();
  await expect(report.getByText('estimate.txt', { exact: true })).toBeVisible();
  await expect(report).toContainText('Nothing has been read or sent.');
  await report.getByRole('button', { name: 'Not now', exact: true }).click();
  await expect(offer).toBeFocused();
  await offer.click();
  const scope = report.locator('.folder-agent-card__organization');
  const other = scope.getByRole('checkbox', { name: 'scan-0009.txt' });
  await other.uncheck();
  await expect(scope).toContainText('Scope preview · 4 files');
  await scope.screenshot({ path: join(evidence, 'narrowed-preview.png') });
  if (liveModel) {
    test.setTimeout(120_000);
    await expect(scope).toContainText(`Anthropic (api.anthropic.com) using ${liveModel}`);
    await other.check();
    await expect(scope).toContainText('Scope preview · 5 files');
    await scope.getByRole('button', { name: 'Approve this read' }).click();
    await expect(scope).toContainText('4 readable · 1 skipped', { timeout: 65_000 });
    await expect(scope).toContainText('scan-0010.pdf — no readable text');
    await expect(scope.getByRole('heading', { name: /kitchen remodel/i })).toBeVisible();
    await scope.screenshot({ path: join(evidence, 'live-model-result.png') });
    await scope.getByRole('button', { name: 'Keep as a view' }).click();
    await expect(scope).toContainText('Saved view · no files moved');
    await page.reload();
    // Direct reload restores the drawer that was already open.
    await expect(report).toBeVisible();
    await report.getByRole('button', { name: 'Read documents and suggest groups' }).click();
    await expect(report).toContainText('4 readable · 1 skipped');
  }
});
