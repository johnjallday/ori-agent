import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { attachPlacementFolder, placementJSON } from './helpers/assisted-workspace-placement';

import {
  musicHomeSnapshot,
  musicHomeResourceCounts,
  musicSourceSnapshot
} from './helpers/music-home-placement';

let unchangedSource: (() => Promise<void>) | null = null;
test.beforeEach(async () => {
  const root = process.env.MUSIC_HOME_PLACEMENT_SONG;
  if (!root) return;
  const before = await musicSourceSnapshot(root);
  unchangedSource = async () => {
    expect(await musicSourceSnapshot(root)).toEqual(before);
  };
});
test.afterEach(async () => {
  await unchangedSource?.();
  unchangedSource = null;
});

const responseCase = process.env.MUSIC_HOME_WIZARD_RESPONSE || '';
const evidence = `tasks/evidence-reaper-folder-setup-wizard${responseCase ? `/${responseCase}` : ''}`;

test(`music wizard ${responseCase || 'automatic'}: explicit review, preserved creator, close and original operation`, async ({
  page,
  request
}) => {
  const song = process.env.MUSIC_HOME_PLACEMENT_SONG;
  test.skip(!song, 'requires a fresh isolated real-candidate music sandbox');
  test.setTimeout(120_000);
  mkdirSync(`${evidence}/wizard`, { recursive: true });
  const before = await musicHomeSnapshot(request);
  let setupPosts = 0;
  let setupBrief = '';
  let setupEntry = '';
  let setupRequestID = '';
  let selectedEntry = '';
  let releaseResponse!: () => void;
  let accepted!: () => void;
  let delivered!: () => void;
  const serverAccepted = new Promise<void>(resolve => {
    accepted = resolve;
  });
  const deliver = new Promise<void>(resolve => {
    releaseResponse = resolve;
  });
  const deliveryDone = new Promise<void>(resolve => {
    delivered = resolve;
  });
  if (responseCase)
    await page.route(/folder-digest\/offers\/[^/]+\/setup$/, async route => {
      const response = await route.fetch();
      expect(response.ok()).toBe(true); // real acceptance; delay/drop only delivery
      accepted();
      await deliver;
      if (responseCase === 'wizard-close-unknown') await route.abort('failed');
      else await route.fulfill({ response });
      delivered();
    });
  let inference = 0;
  page.on('request', request => {
    if (request.method() !== 'POST') return;
    if (/folder-digest\/offers\/[^/]+\/setup$/.test(request.url())) {
      setupPosts++;
      setupBrief = request.postDataJSON().brief;
      setupEntry = request.postDataJSON().entry_name;
      setupRequestID = request.postDataJSON().request_id;
    }
    if (/\/(turns|chat|completions|from-inbox)$/.test(request.url())) inference++;
  });
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(song!);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  const entry = page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'Review setup', exact: true });
  await expect(entry).toBeVisible();
  const dialog = page.locator('#addFolderModal');
  await expect(dialog).toBeHidden();
  expect(await musicHomeSnapshot(request)).toEqual(before);
  // An explicit ordinary editor already using the shared shell must win. Its
  // original state survives even an external source-entry event while it is up.
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      entryPoint: 'wizard-regression',
      name: 'Keep my draft'
    })
  );
  await expect(dialog).toBeVisible();
  await entry.dispatchEvent('click');
  await expect(dialog).not.toHaveAttribute('data-source-setup-owner');
  await expect(page.locator('[data-source-setup-summary]')).toContainText('choices are unchanged');
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceCreatorContext.name)
  ).toBe('Keep my draft');
  await dialog.getByRole('button', { name: 'Close create workspace', exact: true }).click();
  await expect(dialog).toBeHidden();
  await entry.click();
  await expect(dialog).toHaveAttribute('data-source-setup-owner', 'music-folder');
  await expect(
    dialog.getByRole('heading', { name: 'Review music folder setup', exact: true })
  ).toBeFocused();
  const review = dialog.locator('[data-source-creation-review]');
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel [data-source-creation-review]')).toHaveCount(
    0
  );
  await expect(review).toContainText('Ori Music Demo');
  await page.screenshot({ path: `${evidence}/wizard/review.png` });
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const brief = review.getByRole('textbox', { name: 'Later project goal', exact: true });
  await brief.fill('Keep this later-project arrangement brief.');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(entry).toBeFocused();
  expect(setupPosts).toBe(0);
  expect(inference).toBe(0);
  expect(await musicHomeSnapshot(request)).toEqual(before);
  await entry.click();
  await expect(
    review.getByRole('button', { name: 'Use automatic placement', exact: true })
  ).toHaveAttribute('aria-pressed', 'true');
  await expect(brief).toHaveValue('Keep this later-project arrangement brief.');
  if (process.env.MUSIC_HOME_PLACEMENT_TWO_FILES === '1') {
    await review
      .getByRole('button', { name: 'Review choices & requirements', exact: true })
      .click();
    const choice = dialog.locator('#personalAssistantFolderEntryChoice');
    const alternative = choice.getByRole('radio', { checked: false }).first();
    selectedEntry = await alternative.inputValue();
    const saved = page.waitForResponse(
      response => response.url().endsWith('/entry') && response.request().method() === 'POST'
    );
    await alternative.check();
    expect((await saved).ok()).toBe(true);
    await page.screenshot({ path: `${evidence}/wizard/choices.png` });
    expect(setupPosts).toBe(0);
    expect(await musicHomeSnapshot(request)).toEqual(before);
    await dialog.getByRole('button', { name: 'Close setup review', exact: true }).click();
    await expect(dialog).toBeHidden();
    await entry.click();
    await expect(brief).toHaveValue('Keep this later-project arrangement brief.');
    await page.screenshot({ path: `${evidence}/wizard/edited-return.png` });
  }
  // Repeated source entry is presentation only and cannot repeat confirmation.
  await entry.dispatchEvent('click');
  await expect(page.locator('.modal.show')).toHaveCount(1);
  await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
  if (responseCase) {
    await serverAccepted;
    const close = dialog.getByRole('button', { name: 'Close setup review', exact: true });
    const status = page
      .locator('[data-source-setup-summary]')
      .getByRole('button', { name: 'View setup status', exact: true });
    await close.click();
    await expect(dialog).toBeHidden();
    await expect(status).toBeFocused();
    await status.click();
    await expect(
      review.getByRole('heading', { name: 'Checking Home setup', exact: true })
    ).toBeVisible();
    await expect(review.getByRole('button', { name: 'Set up Home', exact: true })).toHaveCount(0);
    expect(setupPosts).toBe(1);
    await close.click();
    await expect(dialog).toBeHidden();
    releaseResponse();
    await deliveryDone;
    if (responseCase === 'wizard-close-unknown')
      await expect(review).toContainText('Confirmation delivery is unknown');
    else
      await expect
        .poll(() =>
          page.evaluate(() => (window as any).PersonalAssistantFolder.current()?.home_result?.state)
        )
        .toBe('ready');
    await expect(dialog).toBeHidden();
    await status.click();
    if (responseCase === 'wizard-close-unknown')
      await review.getByRole('button', { name: 'Read saved status', exact: true }).click();
  }
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  expect(setupPosts).toBe(1);
  expect(setupBrief).toBe('Keep this later-project arrangement brief.');
  if (selectedEntry) expect(setupEntry).toBe(selectedEntry);
  expect(inference).toBe(0);
  const after = await musicHomeSnapshot(request);
  const homes = after.workspaces.filter((workspace: any) => workspace.kind === 'group');
  expect(homes).toHaveLength(1);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  expect(
    after.workspaces.filter((workspace: any) => workspace.parent_id === homes[0].id)
  ).toHaveLength(0);
  const counts = await musicHomeResourceCounts(request, homes[0].id);
  expect(counts.projects).toHaveLength(1);
  const source = (await (await request.get('/api/personal-assistant/folder-digest')).json())
    .folder_digest.offer;
  expect(source.home_result.request_id).toBe(setupRequestID);
  await page.screenshot({ path: `${evidence}/wizard/result.png` });
  await dialog.getByRole('button', { name: 'Close setup review', exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.reload();
  await expect(dialog).toBeHidden();
  await page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'View setup status', exact: true })
    .click();
  await expect(dialog).toContainText('Music Production Home');
  await expect(
    dialog.getByRole('button', { name: 'Go to Music Production Home', exact: true })
  ).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Set up Home', exact: true })).toHaveCount(0);
  expect(await musicHomeResourceCounts(request, homes[0].id)).toEqual(counts);
  expect(setupPosts).toBe(1);
  expect((await musicHomeSnapshot(request)).positions).toEqual(after.positions);
  await page.screenshot({ path: `${evidence}/wizard/saved-status.png` });
});

test('music prerequisites use one sibling review, explicit approval, and a fresh source return', async ({
  page,
  request
}) => {
  const kind = process.env.MUSIC_HOME_WIZARD_PREREQUISITE;
  const song = process.env.MUSIC_HOME_PLACEMENT_SONG;
  test.skip(!kind || !song, 'requires an explicit disposable missing-prerequisite case');
  test.setTimeout(180_000);
  mkdirSync(`${evidence}/prerequisites-${kind}`, { recursive: true });
  const before = await musicHomeSnapshot(request);
  const plugins = async () =>
    (await (await request.get('/api/plugins')).json()).plugins
      .map((plugin: any) => [plugin.name, plugin.enabled, plugin.version])
      .sort();
  const installed = await plugins();
  const confirmations: any[] = [];
  let setups = 0;
  page.on('request', request => {
    if (request.method() !== 'POST') return;
    if (request.url().endsWith('/prerequisites') && request.postDataJSON()?.confirm)
      confirmations.push(request.postDataJSON());
    if (/\/(setup|decide|turns|chat|completions)$/.test(request.url())) setups++;
  });
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(song!);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  await page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'Review setup', exact: true })
    .click();
  const dialog = page.locator('#addFolderModal');
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByRole('heading', { name: 'Review music folder setup', exact: true })
  ).toBeFocused();
  const choices = dialog.getByRole('button', {
    name: 'Review choices & requirements',
    exact: true
  });
  if (await choices.isVisible()) await choices.click();
  const requirements = dialog.locator(`[data-requirement-kind="${kind}"]`);
  const install = requirements.getByRole('button', { name: /^Install / });
  await expect(install).toBeEnabled({ timeout: 30_000 });
  await install.click();
  const sibling = page.locator('#pluginSetupWizard');
  await expect(dialog).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  await expect(sibling).toBeVisible();
  await expect(
    sibling.getByRole('button', { name: 'Confirm installation', exact: true })
  ).toBeEnabled({ timeout: 60_000 });
  await page.screenshot({ path: `${evidence}/prerequisites-${kind}/review.png` });
  await sibling.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(sibling).toBeHidden();
  await expect(dialog).toBeVisible();
  expect(confirmations).toHaveLength(0);
  expect(setups).toBe(0);
  expect(await plugins()).toEqual(installed);
  expect(await musicHomeSnapshot(request)).toEqual(before);
  await install.click();
  await sibling.getByRole('button', { name: 'Confirm installation', exact: true }).click();
  if (kind === 'integration') {
    await expect(
      sibling.getByRole('button', { name: 'Confirm enablement', exact: true })
    ).toBeEnabled({ timeout: 60_000 });
    await sibling.getByRole('button', { name: 'Confirm enablement', exact: true }).click();
  }
  await expect(sibling.getByRole('button', { name: 'Back to setup', exact: true })).toBeVisible({
    timeout: 60_000
  });
  await sibling.getByRole('button', { name: 'Back to setup', exact: true }).click();
  await expect(dialog).toBeVisible();
  await expect(sibling).toBeHidden();
  await expect(dialog).toContainText('Nothing to download');
  expect(confirmations).toHaveLength(kind === 'integration' ? 2 : 1);
  expect(setups).toBe(0);
  expect(await musicHomeSnapshot(request)).toEqual(before);
  const recovered = await plugins();
  expect(recovered.every((plugin: any) => plugin[1])).toBe(true);
  expect(
    recovered.find(
      (plugin: any) =>
        plugin[0] === (kind === 'integration' ? 'reaper-plugin' : 'music-project-management')
    )?.[2]
  ).toBe(kind === 'integration' ? '0.9.0' : '0.3.0');
  await page.screenshot({ path: `${evidence}/prerequisites-${kind}/returned.png` });
  await dialog.getByRole('button', { name: 'Close setup review', exact: true }).click();
  expect(await musicHomeSnapshot(request)).toEqual(before);
  expect(setups).toBe(0);
});

test('music guided setup releases the shell and returns to the same observed source', async ({
  page,
  request
}) => {
  const song = process.env.MUSIC_HOME_PLACEMENT_SONG;
  test.skip(!song, 'requires a fresh isolated candidate music sandbox');
  test.setTimeout(120_000);
  const before = await musicHomeSnapshot(request);
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(song!);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  await page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'Review setup', exact: true })
    .click();
  const source = page.locator('#addFolderModal');
  await source.getByRole('button', { name: 'Review choices & requirements', exact: true }).click();
  const offer = await page.evaluate(() => (window as any).PersonalAssistantFolder.current().id);
  await source.getByRole('button', { name: 'Adjust…', exact: true }).click();
  const guided = page.locator('#specialistSetupJourneyModal');
  await expect(guided).toBeVisible();
  await expect(source).toBeHidden();
  await expect(page.locator('.modal.show')).toHaveCount(1);
  expect(await musicHomeSnapshot(request)).toEqual(before);
  await page.locator('#specialistSetupJourneyClose').click();
  await expect(guided).toBeHidden();
  await expect(source).toBeVisible();
  await expect(page.locator('.modal.show')).toHaveCount(1);
  expect(await page.evaluate(() => (window as any).PersonalAssistantFolder.current().id)).toBe(
    offer
  );
  expect(await musicHomeSnapshot(request)).toEqual(before);
});

test('Downloads review uses the shared dialog without creating resources', async ({ page }) => {
  test.skip(!process.env.ORI_INBOX_SANDBOX, 'requires isolated demo-inbox-folder.sh state');
  mkdirSync(`${evidence}/baseline-downloads`, { recursive: true });
  const before = await placementJSON(page, '/api/workspaces');
  const agents = await placementJSON(page, '/api/agents');
  let setups = 0;
  page.on('request', request => {
    if (
      request.method() === 'POST' &&
      /\/(setup|janitor-prepare|confirm|turns)$/.test(request.url())
    )
      setups++;
  });
  await attachPlacementFolder(page, 'Downloads');
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const dialog = page.locator('#addFolderModal');
  await expect(dialog).toBeVisible();
  const review = dialog.locator('#assistedWorkspacePlacementPanel');
  await expect(review.getByLabel('Workspace name')).toHaveValue('Downloads');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await expect(
    review.getByRole('button', { name: 'Create workspace & scan', exact: true })
  ).toBeEnabled();
  expect(await placementJSON(page, '/api/workspaces')).toEqual(before);
  expect(await placementJSON(page, '/api/agents')).toEqual(agents);
  expect(setups).toBe(0);
  await page.screenshot({ path: `${evidence}/baseline-downloads/review.png` });
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(await placementJSON(page, '/api/workspaces')).toEqual(before);
  expect(await placementJSON(page, '/api/agents')).toEqual(agents);
  expect(setups).toBe(0);
});
