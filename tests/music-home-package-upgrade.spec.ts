import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { cpSync, mkdirSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';

// Reviewed Home package upgrade (docs/architecture/independent-program-homes.md
// §6.1) between two exact Music candidates. Run only through
//   scripts/music-home-demo.sh test --suite package-upgrade --source OLD --upgrade-source NEW
// so HOME, ORI_DATA_DIR, both package exports and screenshots stay in its sandbox.
const ENABLED =
  process.env.ORI_MUSIC_HOME_ACCEPTANCE === '1' && Boolean(process.env.ORI_MUSIC_UPGRADE_PATH);
const SHOTS = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || 'test-results/music-home-package-upgrade';
const PLUGIN_PATH = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const VERSION = process.env.ORI_MUSIC_PLUGIN_VERSION || '';
const UPGRADE_PATH = process.env.ORI_MUSIC_UPGRADE_PATH || '';
const UPGRADE_VERSION = process.env.ORI_MUSIC_UPGRADE_VERSION || '';
const PLUGIN = 'music-project-management';
const RUN = Date.now().toString(36);
const HOME_NAME = `Upgrade Studio ${RUN}`;
const MANAGER_NAME = 'Portfolio Manager';

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires scripts/music-home-demo.sh --suite package-upgrade');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}.png` });
}

function homeDeclaration(root: string) {
  const manifest = JSON.parse(readFileSync(join(root, '.ori-plugin', 'plugin.json'), 'utf8'));
  return manifest.assistant_program_homes[0];
}

function managerPrompt(root: string): string {
  return homeDeclaration(root).roles[0].system_prompt;
}

// A developer replaces the local package with a newer export in place: the
// installed plugin's recorded source now holds the newer release.
function swapInstalledExport() {
  for (const entry of readdirSync(PLUGIN_PATH)) {
    rmSync(join(PLUGIN_PATH, entry), { recursive: true, force: true });
  }
  cpSync(UPGRADE_PATH, PLUGIN_PATH, { recursive: true });
}

async function installedVersion(request: APIRequestContext): Promise<string> {
  const body = await json(await request.get('/api/plugins'));
  const plugins = Array.isArray(body) ? body : body.plugins || [];
  return plugins.find((plugin: { name: string }) => plugin.name === PLUGIN)?.version || '';
}

test('a Home-pinned package upgrades through the Home, not the Plugins page', async ({
  page,
  request
}) => {
  expect(VERSION).toMatch(/^\d+\.\d+\.\d+$/);
  expect(UPGRADE_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
  expect(UPGRADE_VERSION).not.toBe(VERSION);
  const oldPrompt = managerPrompt(PLUGIN_PATH);
  const newPrompt = managerPrompt(UPGRADE_PATH);
  // The two accepted kinds of change: new guidance, and a release that adds
  // the Home's profile card. A pair of candidates may carry either or both.
  const promptChanged = newPrompt !== oldPrompt;
  const profileTitle: string = homeDeclaration(PLUGIN_PATH).home_profile
    ? ''
    : homeDeclaration(UPGRADE_PATH).home_profile?.title || '';
  const addsProfile = profileTitle !== '';
  expect(promptChanged || addsProfile).toBeTruthy();
  await json(await request.post('/api/onboarding/skip'));

  // An ordinary staffed Music Home on the installed release.
  await page.goto('/');
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const creator = page.locator('#addFolderModal');
  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(music).toContainText(`Plugin: ${PLUGIN} ${VERSION}`);
  await music.locator('input').check();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.locator('#folderNameInput').fill(HOME_NAME);
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('.ws-role-row[data-role-id="portfolio_manager"]')).toContainText(
    MANAGER_NAME
  );
  await creator.getByRole('button', { name: 'Review →' }).click();
  await creator.locator('#createFolderBtn').click();
  await expect(creator).toBeHidden();
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const home = homes[0];
  const programURL = `/api/workspaces/${home.id}/assistant-program`;
  expect((await json(await request.get(programURL))).provider_upgrade).toMatchObject({
    plugin_id: PLUGIN,
    installed_version: VERSION,
    available: false
  });
  const profileURL = `${programURL}/profile`;
  if (addsProfile) {
    // The installed release declares no card, so this Home has none.
    expect(await json(await request.get(profileURL))).toMatchObject({ available: false });
    // The page asks for the card and, told there is none, leaves it hidden.
    await Promise.all([
      page.waitForResponse(response => new URL(response.url()).pathname === profileURL),
      page.goto(`/workspaces/${home.folder_slug}/assistant`)
    ]);
    await expect(page.locator('#assistantProgramSuggestionsPanel')).toBeVisible();
    await expect(page.locator('#homeProfilePanel')).toBeHidden();
  }

  swapInstalledExport();

  // The Plugins page refuses to strand the Home and points to it instead.
  await page.goto('/plugins');
  const update = page.locator(`[data-plugin-action="update"][data-plugin-name="${PLUGIN}"]`);
  await expect(update).toBeVisible({ timeout: 30_000 });
  await update.click();
  const toast = page.locator('.toast', { hasText: 'A Home uses this plugin' });
  await expect(toast).toBeVisible();
  await expect(page.locator('#pluginUpdateModal')).toBeHidden();
  await expect(toast).toContainText(`Upgrade from ${HOME_NAME}`);
  const open = toast.getByRole('button', { name: 'Open Home' });
  await expect(open).toBeVisible();
  await evidence(page, '01-plugins-page-refusal');
  expect(await installedVersion(request)).toBe(VERSION);

  // The hand-off opens the Home's review of exactly the new release.
  await open.click();
  await page.waitForURL(url => url.pathname === `/workspaces/${home.folder_slug}/assistant`);
  const dialog = page.locator('dialog.assistant-program-action-dialog');
  await expect(dialog.getByRole('heading', { level: 2 })).toHaveText(
    `Upgrade ${PLUGIN} to ${UPGRADE_VERSION}?`,
    { timeout: 30_000 }
  );
  await expect(dialog).toContainText(`move from ${VERSION} to ${UPGRADE_VERSION}`);
  if (promptChanged) {
    await expect(dialog).toContainText('Music Portfolio Manager');
    await expect(dialog.locator('pre').first()).toHaveText(oldPrompt);
    await expect(dialog.locator('pre').nth(1)).toHaveText(newPrompt);
    await expect(dialog).toContainText(
      `${MANAGER_NAME} (Music Portfolio Manager): gets the new guidance`
    );
  } else {
    await expect(dialog).toContainText('Role prompts are unchanged.');
    await expect(dialog.locator('pre')).toHaveCount(0);
    await expect(dialog).not.toContainText('gets the new guidance');
  }
  if (addsProfile) {
    // The review names the card the release adds, and does not call the
    // upgrade guidance-only.
    await expect(dialog).toContainText('What this release adds');
    await expect(dialog).toContainText(
      `Adds a ${profileTitle} card to this Home. Nothing is detected or read until you open it.`
    );
    await expect(dialog).toContainText('adds what is listed below');
    await expect(dialog).not.toContainText('Only Home guidance changes');
  } else {
    await expect(dialog).toContainText('Only Home guidance changes');
    await expect(dialog).not.toContainText('What this release adds');
  }
  await expect(dialog).toContainText(`${HOME_NAME}: no linked projects`);
  await expect(dialog).toContainText('Approved project library folders stay approved.');
  await evidence(page, '02-home-upgrade-review');
  expect(new URL(page.url()).search).toBe('');
  expect(await installedVersion(request)).toBe(VERSION);

  await dialog.getByRole('button', { name: 'Upgrade Home' }).click();
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  const card = page.locator('#assistantProgramUpgrade');
  await expect(card).toHaveAttribute('data-state', 'succeeded');
  await expect(card).toContainText(`Upgraded to ${UPGRADE_VERSION}`);
  if (promptChanged) await expect(card).toContainText('1 staffed agent got the new guidance');
  await expect(page.locator('#assistantProgramDisabled')).toBeHidden();
  await evidence(page, '03-home-upgraded');

  if (addsProfile) {
    // The upgraded Home has the card, empty: nothing was looked for or read,
    // and nothing is until the owner presses Detect.
    const profilePanel = page.locator('#homeProfilePanel');
    await expect(profilePanel).toBeVisible();
    await expect(page.locator('#homeProfileTitle')).toHaveText(profileTitle);
    await expect(page.locator('#homeProfileDetect')).toHaveText('Detect');
    await expect(profilePanel.locator('li[data-app-id]')).toHaveCount(0);
    const profile = await json(await request.get(profileURL));
    expect(profile).toMatchObject({
      available: true,
      read_only: false,
      title: profileTitle,
      revision: 0
    });
    expect(profile.profile ?? null).toBeNull();
    expect(profile.templates.consented ?? false).toBe(false);
    await profilePanel.scrollIntoViewIfNeeded();
    await evidence(page, '04-profile-card-after-upgrade');
  }

  expect(await installedVersion(request)).toBe(UPGRADE_VERSION);
  const program = await json(await request.get(programURL));
  expect(program).toMatchObject({
    home_provider_available: true,
    plugin_available: true,
    provider_upgrade: { installed_version: UPGRADE_VERSION, available: false }
  });
  expect(program.declaration.roles[0].system_prompt).toBe(newPrompt);
  const again = await request.post(`${programURL}/provider-upgrade/review`, { data: {} });
  expect(again.status()).toBe(409);
  expect((await again.json()).code).toBe('home_upgrade_current');
});
