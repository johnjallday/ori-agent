import { expect, test } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, sep } from 'node:path';
import {
  attachPlacementFolder as attach,
  placementJSON as json
} from './helpers/assisted-workspace-placement';

// Two explicit passes on one owned, continuously running inbox sandbox:
// ORI_PLACEMENT_EXPIRY_PHASE=seed, then =review AFTER the real 30-minute TTL.
// No clock changes, test-only API, fake picker response, or revived saved hold.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
const phase = process.env.ORI_PLACEMENT_EXPIRY_PHASE || '';
test.skip(
  !sandbox || !['seed', 'review'].includes(phase),
  'requires an explicit isolated real-expiry pass'
);
test.setTimeout(60_000);

test(`real held-folder expiry: ${phase}`, async ({ page }) => {
  expect(realpathSync(sandbox).startsWith(realpathSync(tmpdir()) + sep)).toBeTruthy();
  expect(sandbox).toContain('ori-inbox-demo-');
  expect(readFileSync(join(sandbox, '.ori-demo-owner'), 'utf8')).toContain(
    `worktree=${realpathSync(process.cwd())}`
  );
  expect(realpathSync(join(sandbox, 'Desktop'))).toBe(join(realpathSync(sandbox), 'Desktop'));
  const file = join(sandbox, 'placement-expiry-fixture.json');
  const evidence = 'tasks/evidence-assisted-workspace-placement/group-4';
  mkdirSync(evidence, { recursive: true });
  expect(await json(page, '/api/workspaces/build-sessions/availability')).toMatchObject({
    available: false,
    reason: 'no_model'
  });
  const effects: string[] = [];
  page.on('request', request => {
    if (
      request.method() === 'POST' &&
      /\/(janitor-prepare|turns|ask)$/.test(new URL(request.url()).pathname)
    )
      effects.push(request.url());
  });
  const sheet = page.locator('#assistedWorkspacePlacementPanel');
  const confirm = sheet.getByRole('button', { name: 'Create workspace & scan', exact: true });
  if (phase === 'seed') {
    expect(existsSync(file), 'use a fresh expiry sandbox').toBe(false);
    const before = await json(page, '/api/workspaces');
    const profiles = await json(page, '/api/agents');
    await attach(page, 'Desktop');
    await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await expect(confirm).toBeEnabled({ timeout: 30_000 });
    await sheet.getByLabel('Workspace name').fill('Expiry keeps my draft');
    await sheet.getByLabel('Goal', { exact: true }).fill('보관 — keep this exact goal.');
    await sheet.getByLabel('Goal', { exact: true }).press('Tab');
    await expect(confirm).toBeEnabled();
    await sheet.getByRole('checkbox').check();
    await expect(confirm).toBeEnabled();
    const session = await page.evaluate(
      () => (window as any).sessionManager.workspaceBuild.session
    );
    expect(session.placement_monitoring).toBe(true);
    const expires = Date.parse(session.folder.observation.scanned_at) + 30 * 60_000;
    expect(Number.isFinite(expires)).toBe(true);
    writeFileSync(file, JSON.stringify({ session, expires, before, profiles }), { mode: 0o600 });
    console.log('REAL_HOLD_EXPIRES_AT', new Date(expires).toISOString());
    await sheet.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(sheet).toBeHidden();
    expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
    expect(await json(page, '/api/agents')).toEqual(profiles);
  } else {
    const saved = JSON.parse(readFileSync(file, 'utf8'));
    expect(
      Date.now(),
      `wait until ${new Date(saved.expires).toISOString()} without restarting this server`
    ).toBeGreaterThan(saved.expires);
    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.locator('#personalAssistantLauncher').click();
    await page.locator('#personalAssistantConversationMenu').click();
    await page.locator('#personalAssistantConversationContinue').click();
    await page
      .locator(`[data-conversation-id="${saved.session.placement_source.target.conversation_id}"]`)
      .click();
    await page.getByRole('button', { name: 'Return to workspace draft', exact: true }).click();
    await expect(sheet).toBeVisible();
    await expect(sheet).toContainText(/pick|select|expired|unavailable/i);
    await expect(confirm).toBeDisabled();
    await expect(sheet.getByLabel('Workspace name')).toHaveValue(saved.session.draft.name);
    await expect(sheet.getByLabel('Goal', { exact: true })).toHaveValue(
      saved.session.draft.mission
    );
    await expect(sheet.getByRole('checkbox')).toBeChecked();
    await page.screenshot({ path: `${evidence}/expired-selection.png` });
    await sheet.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await page
      .locator('#workspaceBuildFolderChooser')
      .getByRole('button', { name: 'Desktop', exact: true })
      .click();
    await expect(confirm).toBeEnabled({ timeout: 30_000 });
    const session = await page.evaluate(
      () => (window as any).sessionManager.workspaceBuild.session
    );
    expect(session.id).toBe(saved.session.id);
    expect(session.draft).toEqual(saved.session.draft);
    expect(session.placement_monitoring).toBe(true);
    expect(session.folder.observation.id).not.toBe(saved.session.folder.observation.id);
    expect(Date.parse(session.folder.observation.scanned_at)).toBeGreaterThan(saved.expires);
    expect((await json(page, '/api/workspaces')).folders).toEqual(saved.before.folders);
    expect(await json(page, '/api/agents')).toEqual(saved.profiles);
    await confirm.scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${evidence}/expired-selection-repicked.png` });
    await sheet.getByRole('button', { name: 'Cancel draft', exact: true }).click();
    await expect(sheet).toBeHidden();
  }
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  expect(effects).toEqual([]);
});
