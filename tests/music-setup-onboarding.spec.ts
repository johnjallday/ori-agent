import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// The whole guided journey, in one disposable HOME, on PUBLISHED releases:
//   ./scripts/music-home-demo.sh test --suite onboarding --provider reviewed --source <clean Music export>
// The Music Home provider and Ori's REAPER integration are both installed through
// Ori's reviewed registry (no local export), so this is release evidence, kept
// separate from the local-candidate paired suite.
//
// Evidence labels, stated once so no screenshot is mistaken for more than it is:
//  - The collection is chosen with the Documents CHIP, not the native macOS
//    dialog. Native-chooser behaviour is NOT exercised here.
//  - No REAPER application is launched, no live control is granted, and no model
//    is called. Missing model credentials are part of the fixture on purpose.
const sandbox = process.env.ORI_MUSIC_HOME_SANDBOX || '';
const evidenceDir = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || '';
test.skip(
  process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1' ||
    process.env.ORI_MUSIC_PROVIDER_MODE !== 'reviewed' ||
    !sandbox,
  'requires music-home-demo.sh test --suite onboarding --provider reviewed'
);
test.setTimeout(420_000);

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function shot(page: Page, name: string) {
  mkdirSync(evidenceDir, { recursive: true });
  await page.screenshot({ path: join(evidenceDir, `${name}.png`) });
}

const sha = (path: string) => createHash('sha256').update(readFileSync(path)).digest('hex');

test('collection → guided catalog → reviewed REAPER install → exact song → connected, on published releases', async ({
  page,
  request
}) => {
  // Disposable fixtures only. Album-5 has two candidate project files, so which
  // one is authoritative is the person's explicit choice; Logic Sketch is a
  // format no reviewed integration supports; the symlink must be skipped.
  const documents = join(sandbox, 'Documents');
  const sources: string[] = [];
  for (let i = 1; i <= 5; i++) {
    const folder = join(documents, `Album-${i}`);
    mkdirSync(folder, { recursive: true, mode: 0o750 });
    const song = join(folder, 'Song.rpp');
    writeFileSync(song, `onboarding-fixture-${i}; metadata-only`, { mode: 0o600 });
    sources.push(song);
  }
  const alternate = join(documents, 'Album-5', 'Alternate.rpp');
  writeFileSync(alternate, 'onboarding-fixture-5-alternate; metadata-only', { mode: 0o600 });
  sources.push(alternate);
  const logicBundle = join(documents, 'Logic Sketch', 'Sketch.logicx');
  mkdirSync(logicBundle, { recursive: true, mode: 0o750 });
  const logicFile = join(logicBundle, 'projectdata');
  writeFileSync(logicFile, 'onboarding-fixture-logic; metadata-only', { mode: 0o600 });
  sources.push(logicFile);
  symlinkSync(join(documents, 'Album-1'), join(documents, 'Album-Link'));
  const before = sources.map(sha);

  const pluginsAtStart = (await json(await request.get('/api/plugins'))).plugins || [];
  expect(pluginsAtStart.some((row: { name: string }) => row.name === 'reaper-plugin')).toBe(false);
  expect(
    pluginsAtStart.some((row: { name: string }) => row.name === 'music-project-management')
  ).toBe(false);

  await json(await request.post('/api/onboarding/skip'));
  const relationship = (await json(await request.get('/api/personal-assistant')))
    .personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'onboarding-hire',
        if_version: relationship.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    })
  );
  const hired = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hq', {
      data: { request_id: 'onboarding-hq', if_version: hired.state_version, name: 'My HQ' }
    })
  );

  // ── Collection intake (chip chooser) ─────────────────────────────────────
  const scanned = await json(
    await request.post('/api/personal-assistant/folder-digest/scan', {
      data: { chip: 'documents' }
    })
  );
  expect(scanned.offer).toMatchObject({
    status: 'pending',
    portfolio: { projects: 6 },
    capability: { setup_source: 'portfolio' }
  });
  await page.goto('/?panel=today&folder=show');
  await expect(page.locator('#personalAssistantFolderOffer')).toContainText('6 music projects');
  page.on('dialog', dialog => dialog.accept()); // Only inside the disposable HOME.
  await page
    .locator('#personalAssistantFolderOffer')
    .locator('[data-folder-action="setup"]')
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible({ timeout: 90_000 });
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.getByRole('button', { name: 'Review →' }).click();
  await creator.locator('#createFolderBtn').click();
  // No "continue?" dialog: creating the Home lands in its library.
  await page.waitForURL(/\/assistant\?folder_offer_id=/, { timeout: 60_000 });
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const homeID = homes[0].id;
  const homeRoute = new URL(page.url()).pathname;
  const base = `/api/workspaces/${homeID}/assistant-program/library`;

  // ── Guided catalog: one card, each step its own review ───────────────────
  const setupCard = page.locator('#projectSetupNext');
  await expect(setupCard).toBeVisible();
  await expect(setupCard.locator('#projectSetupNextTitle')).toContainText('Documents');
  await expect(setupCard.locator('#projectSetupNextTitle')).toBeFocused();
  await shot(page, 'onb-01-fresh-home-setup-first');
  await setupCard.locator('#projectSetupNextAction').click();
  await page
    .getByRole('dialog', { name: 'Start a Home project library?' })
    .getByRole('button', { name: 'Start library' })
    .click();
  const grant = page.getByRole('dialog', { name: 'Connect this discovery folder?' });
  await expect(grant).toContainText(documents);
  await shot(page, 'onb-02-root-review');
  await grant.getByRole('button', { name: 'Connect folder' }).click();
  // The grant goes straight to its scan review (no separate "scan now?" stop).
  const scan = page.getByRole('dialog', { name: 'Scan this discovery folder once?' });
  await expect(scan).toContainText(documents);
  await scan.getByRole('button', { name: 'Scan metadata' }).click();
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('6 of 6 projects');
  await expect(setupCard).toBeHidden(); // an established Home shows no setup card
  await expect(shelf.locator('#projectLibraryProposals')).toContainText('6 projects');
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(1);
  await shot(page, 'onb-03-catalog-after-scan');

  // ── Honest blockers, per song ────────────────────────────────────────────
  const rows = (await json(await request.get(`${base}/projects`))).rows as {
    id: string;
    name: string;
  }[];
  const album5 = rows.find(row => row.name === 'Album-5')!;
  const logic = rows.find(row => row.name === 'Logic Sketch')!;
  expect(album5 && logic).toBeTruthy();
  await shelf
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Logic Sketch' })
    .click();
  const logicDialog = page.getByRole('dialog', { name: 'Logic Sketch' });
  await expect(logicDialog).toContainText('Needs a project integration');
  // No reviewed integration supports Logic, so nothing is offered as its cure.
  await expect(logicDialog.getByRole('button', { name: /integration$/ })).toHaveCount(0);
  await logicDialog.getByRole('button', { name: 'Close' }).click();

  await shelf
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Album-5' })
    .click();
  const album5Dialog = page.getByRole('dialog', { name: 'Album-5' });
  await expect(album5Dialog).toContainText('Needs a project integration');
  await shot(page, 'onb-04-album5-needs-integration');

  // ── Reviewed install: cancel first, then confirm ─────────────────────────
  const workspacesBefore = (await json(await request.get('/api/workspaces'))).folders.length;
  await album5Dialog.getByRole('button', { name: 'Review the REAPER integration' }).click();
  await page.waitForURL(/\/\?setup=quest&source=host&quest=install_ori_reaper/);
  const quest = page.locator('#specialistSetupJourneyModal');
  await expect(quest).toBeVisible({ timeout: 30_000 });
  await expect(quest).toContainText('Installed: No');
  await shot(page, 'onb-05-install-quest');
  // Leaving without confirming installs nothing.
  await page.goto(homeRoute);
  await expect(page.getByRole('dialog', { name: 'Album-5' })).toBeVisible({ timeout: 30_000 });
  expect(
    ((await json(await request.get('/api/plugins'))).plugins || []).some(
      (row: { name: string }) => row.name === 'reaper-plugin'
    )
  ).toBe(false);
  await page
    .getByRole('dialog', { name: 'Album-5' })
    .getByRole('button', { name: 'Review the REAPER integration' })
    .click();
  await page.waitForURL(/\/\?setup=quest&source=host&quest=install_ori_reaper/);
  await expect(page.locator('#specialistSetupJourneyModal')).toBeVisible({ timeout: 30_000 });
  const questModal = page.locator('#specialistSetupJourneyModal');
  await expect(questModal.getByText('Checking current setup')).toBeHidden({ timeout: 60_000 });
  await shot(page, 'onb-06-install-review');
  await questModal.getByRole('button', { name: 'Install plugin' }).click();
  const installConfirm = questModal.getByRole('button', { name: 'Install', exact: true });
  await expect(installConfirm).toBeVisible({ timeout: 90_000 });
  await shot(page, 'onb-07-install-confirm-review');
  await installConfirm.click();
  // The install itself is now confirmed: published release, through the quest.
  await expect(async () => {
    const plugins = (await json(await request.get('/api/plugins'))).plugins || [];
    expect(plugins.some((row: { name: string }) => row.name === 'reaper-plugin')).toBe(true);
  }).toPass({ timeout: 120_000 });
  // Installed is not enabled: enabling is its own reviewed step.
  await questModal.getByRole('button', { name: 'Enable plugin' }).click();
  const enableConfirm = questModal.getByRole('button', { name: 'Enable', exact: true });
  await expect(enableConfirm).toBeVisible({ timeout: 90_000 });
  await shot(page, 'onb-07b-enable-review');
  await enableConfirm.click();
  const back = questModal.getByRole('button', { name: 'Back to your song' });
  await expect(back)
    .toHaveCount(1, { timeout: 60_000 })
    .catch(() => undefined);
  await expect(back)
    .toBeVisible({ timeout: 60_000 })
    .catch(async error => {
      await shot(page, 'onb-08-debug');
      throw new Error(
        `${error.message}\nbuttons: ${(await questModal.getByRole('button').allInnerTexts()).join(' | ')}\n${await questModal.innerText()}`
      );
    });
  await shot(page, 'onb-08-installed-back-to-song');
  await back.click();
  await page.waitForURL(url => url.pathname === homeRoute);
  await expect(page.locator('#projectLibraryStatus')).toContainText('Back on your song');
  // Nothing but the install changed: no workspace, no source write.
  expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(workspacesBefore);

  // ── Same song, exact file, one connected child ───────────────────────────
  const backDialog = page.getByRole('dialog', { name: 'Album-5' });
  await expect(backDialog).toContainText('Choose the authoritative project file');
  await backDialog.getByRole('button', { name: 'Review project setup' }).click();
  const fileForm = page.getByRole('dialog', { name: 'Set up Album-5' });
  await fileForm
    .getByRole('combobox', { name: 'Authoritative project file' })
    .selectOption('Alternate.rpp');
  await fileForm.getByRole('button', { name: 'Review this project' }).click();
  const review = page.getByRole('dialog', { name: 'Connect this one project?' });
  await expect(review).toContainText('Authoritative file: Alternate.rpp');
  await expect(review).toContainText('No other catalog projects are created');
  await shot(page, 'onb-09-exact-file-review');
  const commitReply = page.waitForResponse(
    response =>
      response.url().includes(`/projects/${album5.id}/activation/commit`) &&
      response.request().method() === 'POST'
  );
  await review.getByRole('button', { name: 'Connect project' }).click();
  const committed = await json(await commitReply);
  const connected = page.getByRole('dialog', { name: 'Album-5 is connected' });
  await expect(connected).toBeVisible({ timeout: 60_000 });
  await shot(page, 'onb-10-connected');
  await connected.getByRole('button', { name: 'Stay in library' }).click();
  const after = (await json(await request.get('/api/workspaces'))).folders;
  expect(after).toHaveLength(workspacesBefore + 1);
  const connectedRows = (await json(await request.get(`${base}/projects`))).rows.filter(
    (row: { connection: string }) => row.connection === 'connected'
  );
  expect(connectedRows.map((row: { name: string }) => row.name)).toEqual(['Album-5']);
  expect(sources.map(sha)).toEqual(before); // no source project file was written
  void album5;
  void logic;

  // ── Staffing the library-created child, without Today ────────────────────
  const childID = String(committed.workspace_id);
  const childSlug = String(
    (await json(await request.get(`/api/workspaces/${childID}`))).folder_slug
  );
  await page.goto(`/workspaces/${encodeURIComponent(childSlug)}`);
  await expect(page.locator('body')).not.toContainText('404 page not found');
  await page.waitForTimeout(3000);
  await shot(page, 'onb-11-child-first-open');
  const modeWizard = page.getByRole('dialog', { name: 'Set up Reaper Song' });
  await expect(modeWizard).toBeVisible();
  await modeWizard.getByRole('button', { name: 'File-only' }).click();
  await modeWizard.getByRole('button', { name: /^(Continue|Next)/ }).click();
  await page.waitForTimeout(1500);
  await shot(page, 'onb-12-mode-review');
  await modeWizard.getByRole('button', { name: 'Approve and continue' }).click();
  await expect(modeWizard).toBeHidden({ timeout: 30_000 });
  await expect(page.getByText('Project-file work is available')).toBeVisible();
  await shot(page, 'onb-12b-file-only-chosen');
  await page.getByRole('button', { name: 'Fill the primary role' }).click();
  const roleForm = page.locator('#addAgentModal');
  await expect(roleForm).toBeVisible({ timeout: 30_000 });
  await shot(page, 'onb-13-team-form-after-mode');
  // The role's own reviewed form is the staffing consent: it names the role, this
  // workspace as the only scope, and creates nothing until submitted.
  await expect(roleForm).toContainText('this workspace only');
  expect(
    (await json(await request.get(`/api/workspaces/${childID}/roles`))).roles.filled_count
  ).toBe(0);
  await roleForm.getByLabel('Agent Name').fill('Album 5 Assistant');
  const staffed = page.waitForResponse(
    response =>
      response.url().includes(`/api/workspaces/${childID}/roles/`) &&
      response.request().method() === 'PUT'
  );
  await roleForm
    .getByRole('button', { name: /^Create/ })
    .last()
    .click();
  expect((await staffed).ok()).toBeTruthy();
  const roster = (await json(await request.get(`/api/workspaces/${childID}/roles`))).roles;
  expect(roster.filled_count).toBe(1);
  expect(roster.roles.map((role: { role_id: string }) => role.role_id)).toEqual([
    'reaper-assistant'
  ]);
  await shot(page, 'onb-14-child-staffed');
  expect(sources.map(sha)).toEqual(before);

  // ── Back to the same Home: one connected song, the rest untouched ────────
  await page.goto(homeRoute);
  const finalShelf = page.locator('#projectLibraryPanel');
  await expect(finalShelf.locator('#projectLibraryCount')).toHaveText('6 of 6 projects');
  const finalRows = (await json(await request.get(`${base}/projects`))).rows as {
    name: string;
    connection: string;
  }[];
  expect(finalRows.filter(row => row.connection === 'connected').map(row => row.name)).toEqual([
    'Album-5'
  ]);
  expect((await json(await request.get('/api/workspaces'))).folders).toHaveLength(
    workspacesBefore + 1
  );
  await shot(page, 'onb-15-home-after-journey');
  expect(sources.map(sha)).toEqual(before);
});
