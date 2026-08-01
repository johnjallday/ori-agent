import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  existsSync,
  utimesSync,
  readdirSync,
  realpathSync
} from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Downloads Janitor end-to-end coverage (PRD task 7.2).
 *
 * Run with:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test tests/downloads-janitor.spec.ts
 *
 * against an isolated smoke server (`wt demo 8931`, or the HOME/ORI_DATA_DIR
 * recipe in CLAUDE.md). The server must be able to read the fixture folders
 * this spec creates under the OS temp dir, so it has to run on this machine.
 *
 * NEVER point this at a real Downloads folder. Every test builds its own
 * throwaway fixture directory and passes an absolute path; the feature really
 * moves and really trashes files.
 *
 * Why this file exists, specifically:
 *
 * The panel is loaded as a classic `defer` script and mounts into a div that
 * starts `hidden`. A dependency on something a *later* module script defined
 * once left it never rendering at all, in every browser, while every
 * server-side check passed — the template installed, the status endpoint
 * answered correctly, the asset served byte-identical. Server-side
 * verification cannot see this class of bug. The first test below is the
 * regression for it, and is the reason to keep this suite even when the Go
 * tests are green.
 *
 * Scope note: classification rules, settling, approval-token binding, restart
 * recovery, the adversarial filesystem matrix and the cross-platform Trash
 * behavior are covered by fast Go tests in internal/downloadsjanitor and
 * internal/downloadsjanitorhttp. Re-proving them through a browser would be
 * slow and no more convincing. This file covers what only a real browser
 * proves: that the surface renders, that the gates actually gate, and that the
 * click path from an unconfigured workspace to a moved file works.
 */

// This suite deliberately drives the LEGACY `/downloads-janitor` routes.
//
// In-repo browser code has moved to the canonical `/file-janitor` prefix, so
// without this suite the legacy alias would have no end-to-end coverage at all
// and could be broken without any test noticing — which is exactly what FR-133
// forbids while the alias is still published. The canonical prefix gets its own
// suite (tests/file-janitor.spec.ts); the two together are what make retiring
// the alias later a deliberate act.
const TEMPLATE_ID = 'downloads-janitor';
const RUN = Date.now().toString(36);

// A file must look finished for the scanner to propose it: backdated well past
// the settling interval.
const OLD = new Date(Date.now() - 6 * 60 * 60 * 1000);

function fixtureFolder(label: string, files: Record<string, string>): string {
  const root = mkdtempSync(join(tmpdir(), `dj-${label}-`));
  // This feature really moves and really trashes files, so every fixture is
  // asserted to be a throwaway temp directory before anything is written into
  // it. The failure this prevents is not subtle: a fixture that resolved under
  // the developer's home would file their actual Downloads folder.
  if (!root.startsWith(tmpdir()) || root === tmpdir()) {
    throw new Error(`refusing to use ${root} as a fixture: not a temp directory`);
  }
  if (homedir() !== tmpdir() && root.startsWith(join(homedir(), 'Downloads'))) {
    throw new Error(`refusing to use ${root} as a fixture: it is inside a real Downloads folder`);
  }
  for (const [name, contents] of Object.entries(files)) {
    const path = join(root, name);
    mkdirSync(join(path, '..'), { recursive: true });
    writeFileSync(path, contents);
    utimesSync(path, OLD, OLD);
  }
  // The CANONICAL path, because setup resolves symlinks when it records the
  // grant (FR-47). On macOS the temp dir lives under /var, which is a symlink
  // to /private/var — so the unresolved path would never match what the server
  // stored or what the UI shows, and asserting on it would be asserting that
  // canonicalization is broken.
  return realpathSync(root);
}

/**
 * Creates a Downloads Janitor workspace.
 *
 * Workspaces are created the way the product creates them, agents included.
 * Nothing auto-starts on first open, so no task-confirmation modal lands over
 * the surface under test.
 */
async function createJanitorWorkspace(
  request: APIRequestContext,
  name: string,
  withTemplateAgents = true
): Promise<string> {
  const res = await request.post('/api/workspaces', {
    data: {
      name,
      description: '',
      template_id: TEMPLATE_ID,
      create_template_agents: withTemplateAgents
    }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return (body.folder?.id || body.workspace?.id) as string;
}

/**
 * Completes setup through the blueprint's Setup Wizard, which is itself under
 * test.
 *
 * One step cannot be automated: choosing the folder opens the operating
 * system's own picker, and no browser test can drive that dialog. The
 * confirmation the picker produces is posted directly instead — the same
 * request, with the same fixture path — and everything after it runs through
 * the wizard exactly as a user does, including the separate approval that
 * starts unattended work.
 */
async function completeSetup(page: Page, workspaceId: string, root: string) {
  const confirmed = await page.request.post(
    `/api/workspaces/${workspaceId}/downloads-janitor/setup`,
    { data: { path: root, paused: true } }
  );
  expect(confirmed.ok(), await confirmed.text()).toBeTruthy();

  await page.goto(`/workspaces/${workspaceId}`);
  const dialog = page.locator('#setupWizardDialog');
  // A fresh workspace opens the wizard itself; one whose wizard was dismissed
  // waits to be asked, which is the whole point of dismissal.
  if (!(await dialog.isVisible())) {
    // The workspace-level banner is the entry that is always there, whatever
    // state the domain panel happens to be in.
    await page.locator('#setupWizardBannerAction').click();
  }
  await expect(dialog).toBeVisible({ timeout: 15000 });

  // The folder step is already satisfied, so the wizard resumes at the first
  // unresolved one — and the folder grant on its own has started nothing.
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review what runs on its own', {
    timeout: 15000
  });
  await expect(page.locator('#setupWizardPrimary')).toHaveText('Turn this on');
  await page.locator('#setupWizardPrimary').click();

  // Readiness, then the summary, then the wizard finishes and closes.
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Check everything is working', {
    timeout: 15000
  });
  await page.locator('#setupWizardPrimary').click();
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Downloads Janitor is ready', {
    timeout: 15000
  });
  await page.locator('#setupWizardPrimary').click();
  await expect(dialog).toBeHidden({ timeout: 15000 });
  // Scan now lives in the console header, not in Workspace Details. Details
  // carries a compact card whose single action opens the console.
  await openConsole(page);
}

/**
 * Opens the File Janitor console from the compact Details card.
 *
 * A workspace whose setup is unfinished opens its Setup Wizard over the page,
 * which covers the card. That is existing wizard behavior — the same
 * "setup dialog lands on the surface it is about" pattern this codebase has hit
 * before — so the helper dismisses it first, exactly as a user would have to.
 */
async function openConsole(page: Page) {
  // Short-circuit on the CONSOLE, not on Scan: Scan is absent whenever the
  // folder is unavailable, so keying off it made an already-open console look
  // closed, and the helper then clicked the card behind it — which the
  // console's own header intercepted.
  //
  // The brief wait matters too. Once the console has been opened, the URL
  // carries ?panel=file-janitor, so a reload re-opens it — correctly, but
  // asynchronously. Checking immediately after a reload sees it closed and
  // races the deep link.
  const consoleHost = page.locator('#fileJanitorConsole');
  await consoleHost.waitFor({ state: 'visible', timeout: 2000 }).catch(() => {});
  if (await consoleHost.isVisible().catch(() => false)) return;

  const wizard = page.locator('#setupWizardDialog');
  if (await wizard.isVisible().catch(() => false)) {
    await page.locator('#setupWizardClose').click();
    await expect(wizard).toBeHidden({ timeout: 15000 });
  }

  await page.locator('#fileJanitorCardOpen').click();
  await expect(page.locator('#fileJanitorConsole')).toBeVisible({ timeout: 15000 });
}

async function scan(page: Page) {
  await page.locator('#downloadsJanitorScan').click();
  await expect(page.locator('.dj-row-item').first()).toBeVisible({ timeout: 15000 });
}

function rowFor(page: Page, name: string) {
  return page.locator('.dj-row-item').filter({ hasText: name });
}

/**
 * Presses the confirmation's apply button.
 *
 * Waits for the panel's own content first. The confirm button is created and
 * appended in the same tick as the panel, so a click issued the instant the
 * selector resolves can race the render — a real user cannot click that fast,
 * but a test can, and the result is a silent no-op indistinguishable from a
 * broken button. Asserting on the panel's text pins it down before clicking.
 */
async function confirmApply(page: Page) {
  const panel = page.locator('.dj-confirm');
  await expect(panel).toBeVisible({ timeout: 15000 });
  await expect(panel).toContainText('Confirm these moves');
  await expect(page.locator('#downloadsJanitorConfirmApply')).toBeEnabled();
  await page.locator('#downloadsJanitorConfirmApply').click();
  await expect(page.locator('.dj-results')).toBeVisible({ timeout: 30000 });
}

test.describe.configure({ mode: 'serial' });

test.describe('Downloads Janitor', () => {
  // A fresh sandbox profile shows the first-run onboarding modal, whose
  // static backdrop swallows every click. Skip it server-side once, the same
  // way create-workspace-behavior.spec.ts does.
  test.beforeAll(async ({ request }) => {
    await request.post('/api/onboarding/skip').catch(() => {});
  });

  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/onboarding/skip').catch(() => {});
  });
  // ---------------------------------------------------------------- mounting

  test('the panel renders on a Janitor workspace', async ({ page, request }) => {
    // The regression test for the defer/module script-ordering bug. If the
    // panel depends on anything a later script sets up, this fails and every
    // API-level check still passes.
    const id = await createJanitorWorkspace(request, `DJ Mount ${RUN}`);
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(String(error)));

    await page.goto(`/workspaces/${id}`);

    const mount = page.locator('#downloadsJanitorMount');
    await expect(mount).toBeVisible({ timeout: 15000 });
    await expect(mount).not.toHaveAttribute('hidden', /.*/);
    await expect(page.locator('#downloadsJanitorMount')).toContainText('Setup required');
    // The blueprint's wizard owns setup, so this surface offers a way into it
    // rather than a second folder chooser (FR-82).
    //
    // The control moved: Workspace Details now carries a compact card whose
    // single action opens the console, replacing the old inline panel's
    // "Continue setup" button (#downloadsJanitorOpenSetup). The intent asserted
    // here is unchanged — one way in, and no editable path field anywhere.
    await expect(page.locator('#fileJanitorCardOpen')).toBeVisible();
    await expect(page.locator('#downloadsJanitorPath')).toHaveCount(0);

    // The wizard opens itself, and states what access it is asking for before
    // anything is granted.
    const dialog = page.locator('#setupWizardDialog');
    await expect(dialog).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');
    const disclosure = page.locator('#setupWizardDisclosure');
    await expect(disclosure).toContainText('names, types, sizes');
    await expect(disclosure).toContainText('Trash');
    expect(errors, 'the panel must not throw while mounting').toEqual([]);
  });

  test('a workspace that is not a Janitor workspace shows nothing', async ({ page, request }) => {
    const res = await request.post('/api/workspaces', {
      data: { name: `Plain WS ${RUN}`, description: '' }
    });
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    const id = (body.folder?.id || body.workspace?.id) as string;

    await page.goto(`/workspaces/${id}`);
    await page.waitForTimeout(1500);
    await expect(page.locator('#downloadsJanitorMount')).toBeHidden();
  });

  test('setup can be dismissed and picked up again, and never reopens once ready', async ({
    page,
    request
  }) => {
    const id = await createJanitorWorkspace(request, `DJ Resume ${RUN}`);
    const root = fixtureFolder('resume', { 'invoice.pdf': 'x' });

    // Dismissing an unfinished wizard leaves the workspace visibly unfinished
    // and starts nothing.
    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardDialog')).toBeVisible({ timeout: 15000 });
    await page.locator('#setupWizardClose').click();
    await expect(page.locator('#setupWizardDialog')).toBeHidden();
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Setup required');

    // A reload does not ambush the user again, and Details still offers a way in.
    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#fileJanitorCardOpen')).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardDialog')).toBeHidden();

    // Card opens the console; the console's own entry opens the same wizard, at
    // the step it left off on. (The entry moved into the console with the rest
    // of the surface; Details is a summary now.)
    await page.locator('#fileJanitorCardOpen').click();
    await expect(page.locator('#fileJanitorConsole')).toBeVisible({ timeout: 15000 });
    await page.locator('#downloadsJanitorOpenSetup').click();
    await expect(page.locator('#setupWizardDialog')).toBeVisible();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');
    await page.locator('#setupWizardClose').click();

    await completeSetup(page, id, root);

    // A workspace that is already set up is not asked again.
    await page.goto(`/workspaces/${id}`);
    await openConsole(page);
    await expect(page.locator('#setupWizardDialog')).toBeHidden();
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Ready');
  });

  test('unattended watching cannot be started from the panel before setup approves it', async ({
    page,
    request
  }) => {
    const id = await createJanitorWorkspace(request, `DJ Gate ${RUN}`);
    const root = fixtureFolder('gate', { 'invoice.pdf': 'x' });

    // The folder is granted, but the automation step has not been approved.
    const confirmed = await request.post(`/api/workspaces/${id}/downloads-janitor/setup`, {
      data: { path: root, paused: true }
    });
    expect(confirmed.ok(), await confirmed.text()).toBeTruthy();

    await page.goto(`/workspaces/${id}`);
    // Pause and Scan live in the console header now, not in Workspace Details.
    await openConsole(page);
    await expect(page.locator('#downloadsJanitorPause')).toBeVisible({ timeout: 15000 });
    // The control points at the step that discloses what it would start.
    await expect(page.locator('#downloadsJanitorPause')).toHaveText('Approve in setup');
    await expect(page.locator('#downloadsJanitorScan')).toBeEnabled();

    const status = await (await request.get(`/api/workspaces/${id}/downloads-janitor`)).json();
    expect(status.status.settings.paused, 'nothing unattended may be running yet').toBeTruthy();
  });

  // ------------------------------------------------------------------- setup

  test('setup grants access and reports readiness', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Setup ${RUN}`);
    const root = fixtureFolder('setup', { 'invoice.pdf': 'x' });

    await completeSetup(page, id, root);

    // Every readiness row reports, and the folder is named back to the user.
    // The compact card carries that line now (.fj-card-sub), where the inline
    // panel's .dj-sub used to.
    await expect(page.locator('.fj-card-sub')).toContainText(root);
    await expect(page.locator('#downloadsJanitorActivity')).toHaveText('Watching');
    // The destination is created eagerly; category folders are not.
    expect(existsSync(join(root, 'Filed'))).toBeTruthy();
    expect(readdirSync(join(root, 'Filed'))).toEqual([]);

    // And the grant is visible where a user would look for it, and revocable
    // there — the Janitor's access is not hidden behind its own settings.
    const dirs = await (await request.get(`/api/workspaces/${id}/directories`)).json();
    const paths = (dirs.directories || []).map((d: { path: string }) => d.path);
    expect(paths).toContain(root);
  });

  // -------------------------------------------------------- scan and review

  test('a scan proposes finished files and never partial downloads', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Scan ${RUN}`);
    const root = fixtureFolder('scan', {
      'invoice-2026-07.pdf': 'pdf',
      'screenshot.png': 'png',
      'podcast.mp3': 'mp3',
      'payload.bin': 'blob',
      'big-movie.mp4.crdownload': 'partial'
    });

    await completeSetup(page, id, root);
    await scan(page);

    await expect(page.locator('.dj-row-item')).toHaveCount(4);
    await expect(rowFor(page, 'big-movie.mp4')).toHaveCount(0);

    // Nothing is selected. Opening the review surface can never move anything.
    const boxes = page.locator('.dj-select');
    for (let i = 0; i < (await boxes.count()); i += 1) {
      await expect(boxes.nth(i)).not.toBeChecked();
    }
    // The approve control is disabled and says why.
    const approve = page.locator('#downloadsJanitorApprove');
    await expect(approve).toBeDisabled();
    await expect(page.locator('#downloadsJanitorSelection')).toContainText('No files selected');

    // An unclassifiable file is flagged rather than guessed at.
    await expect(rowFor(page, 'payload.bin')).toContainText('Needs review');
  });

  test('a category can be changed and a file skipped', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Decide ${RUN}`);
    const root = fixtureFolder('decide', { 'notes.pdf': 'pdf', 'advert.png': 'png' });

    await completeSetup(page, id, root);
    await scan(page);

    // Re-categorising updates the stated destination, so the user is never
    // approving a destination they cannot see.
    const row = rowFor(page, 'notes.pdf');
    await row.locator('select').selectOption({ label: 'Archives' });
    await expect(row).toContainText('Filed/Archives');

    await rowFor(page, 'advert.png').getByRole('button', { name: 'Skip' }).click();
    await expect(rowFor(page, 'advert.png')).toContainText('Skipped');
  });

  // ------------------------------------------------------------------- apply

  test('approved files really move, and only the approved ones', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Move ${RUN}`);
    const root = fixtureFolder('move', {
      'report.pdf': 'pdf',
      'photo.png': 'png',
      'untouched.csv': 'a,b'
    });

    await completeSetup(page, id, root);
    await scan(page);

    await rowFor(page, 'report.pdf').locator('.dj-select').check();
    await rowFor(page, 'photo.png').locator('.dj-select').check();

    const approve = page.locator('#downloadsJanitorApprove');
    await expect(approve).toContainText('2 moves');
    await approve.click();

    // The confirmation names every destination before anything happens.
    const confirm = page.locator('.dj-confirm');
    await expect(confirm).toContainText('Filed/Documents');
    await expect(confirm).toContainText('Filed/Images');
    expect(existsSync(join(root, 'report.pdf')), 'nothing moves at approval').toBeTruthy();

    await confirmApply(page);

    expect(existsSync(join(root, 'Filed', 'Documents', 'report.pdf'))).toBeTruthy();
    expect(existsSync(join(root, 'Filed', 'Images', 'photo.png'))).toBeTruthy();
    // The file the user never decided about is exactly where they left it.
    expect(existsSync(join(root, 'untouched.csv'))).toBeTruthy();
  });

  test('a name already in use is renamed, never overwritten', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Collide ${RUN}`);
    const root = fixtureFolder('collide', { 'invoice.pdf': 'the new one' });
    mkdirSync(join(root, 'Filed', 'Documents'), { recursive: true });
    writeFileSync(join(root, 'Filed', 'Documents', 'invoice.pdf'), 'the original');

    await completeSetup(page, id, root);
    await scan(page);
    await rowFor(page, 'invoice.pdf').locator('.dj-select').check();
    await page.locator('#downloadsJanitorApprove').click();

    // The rename is disclosed at approval time, with the reason.
    await expect(page.locator('.dj-confirm')).toContainText('invoice (2).pdf');
    await expect(page.locator('.dj-confirm')).toContainText('already there');

    await confirmApply(page);

    expect(existsSync(join(root, 'Filed', 'Documents', 'invoice (2).pdf'))).toBeTruthy();
    // The original is untouched — that is the whole point.
    const original = join(root, 'Filed', 'Documents', 'invoice.pdf');
    expect(existsSync(original)).toBeTruthy();
  });

  // ------------------------------------------------------------------- trash

  test('removal needs its own acknowledgement, by count', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Trash ${RUN}`);
    const root = fixtureFolder('trash', { 'junk.bin': 'junk' });

    await completeSetup(page, id, root);
    await scan(page);

    await rowFor(page, 'junk.bin').getByRole('button', { name: /Trash/ }).click();
    await page.locator('#downloadsJanitorApprove').click();

    const confirmButton = page.locator('#downloadsJanitorConfirmApply');
    await expect(confirmButton).toBeDisabled();
    await expect(page.locator('.dj-confirm')).toContainText('Nothing is deleted permanently');

    const ack = page.locator('#downloadsJanitorTrashAck');
    await expect(page.locator('.dj-trash-ack')).toContainText('Yes, move 1 file to the Trash');
    // Focus is on the acknowledgement, not lost to the body behind a disabled
    // button — the destructive path is where losing your place matters most.
    await expect(ack).toBeFocused();

    await ack.check();
    await expect(confirmButton).toBeEnabled();
    await confirmButton.click();

    await expect(page.locator('.dj-results')).toBeVisible({ timeout: 15000 });
    expect(existsSync(join(root, 'junk.bin'))).toBeFalsy();
  });

  // ----------------------------------------------------------------- history

  test('history records what happened and can put it back', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Undo ${RUN}`);
    const root = fixtureFolder('undo', { 'contract.pdf': 'pdf' });

    await completeSetup(page, id, root);
    await scan(page);
    await rowFor(page, 'contract.pdf').locator('.dj-select').check();
    await page.locator('#downloadsJanitorApprove').click();
    await confirmApply(page);
    expect(existsSync(join(root, 'Filed', 'Documents', 'contract.pdf'))).toBeTruthy();

    // History is a console tab now, not a section stacked under the review
    // table. The entry and its undo control are unchanged; only where you go to
    // find them moved.
    await page.locator('#fileJanitorConsole [data-fj-tab="history"]').click();

    const entry = page.locator('.dj-history-item').filter({ hasText: 'contract.pdf' }).first();
    await expect(entry).toBeVisible({ timeout: 15000 });
    await entry.getByRole('button', { name: /Undo/ }).click();

    await expect(page.locator('#downloadsJanitorHistoryStatus')).toContainText(
      /put back|restored/i,
      {
        timeout: 15000
      }
    );
    expect(
      existsSync(join(root, 'contract.pdf')),
      'the file comes back to where it was'
    ).toBeTruthy();

    // Undo is single use: a second press is refused rather than performed.
    const again = entry.getByRole('button', { name: /Undo/ });
    if (await again.count()) {
      await again.click();
      await expect(page.locator('#downloadsJanitorHistoryStatus')).not.toContainText(
        'put back again'
      );
    }
  });

  // ---------------------------------------------------------------- lifecycle

  test('watching can be paused without losing the ability to scan', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Pause ${RUN}`);
    const root = fixtureFolder('pause', { 'thing.pdf': 'pdf' });

    await completeSetup(page, id, root);
    await page.getByRole('button', { name: 'Pause watching' }).click();
    await expect(page.locator('#downloadsJanitorActivity')).toHaveText('Paused', {
      timeout: 15000
    });
    // A paused watcher is not a disabled feature.
    await expect(page.locator('#downloadsJanitorScan')).toBeEnabled();
  });

  test('access can be given up, and history survives it', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Revoke ${RUN}`);
    const root = fixtureFolder('revoke', { 'keep.pdf': 'pdf' });

    await completeSetup(page, id, root);
    await scan(page);
    await rowFor(page, 'keep.pdf').locator('.dj-select').check();
    await page.locator('#downloadsJanitorApprove').click();
    await confirmApply(page);

    const revoked = await request.post(`/api/workspaces/${id}/downloads-janitor/revoke`);
    expect(revoked.ok()).toBeTruthy();

    await page.reload();
    await expect(page.locator('#downloadsJanitorMount')).toContainText('Setup required', {
      timeout: 15000
    });
    // Files stay where they were put; giving up access does not undo work.
    expect(existsSync(join(root, 'Filed', 'Documents', 'keep.pdf'))).toBeTruthy();
    // And the record of what happened outlives the access.
    const history = await (
      await request.get(`/api/workspaces/${id}/downloads-janitor/history`)
    ).json();
    expect((history.actions || []).length).toBeGreaterThan(0);
  });

  test('an unreachable folder is explained, not reported as a fault', async ({ page, request }) => {
    const id = await createJanitorWorkspace(request, `DJ Unlink ${RUN}`);
    const root = fixtureFolder('unlink', { 'file.pdf': 'pdf' });
    await completeSetup(page, id, root);

    // Unlink the folder from the generic Linked Folders surface, which a user
    // can do at any time without going near the Janitor's own settings.
    const dirs = await (await request.get(`/api/workspaces/${id}/directories`)).json();
    const ref = (dirs.directories || []).find((d: { path: string }) => d.path === root);
    expect(ref).toBeTruthy();
    await request.delete(`/api/workspaces/${id}/directories/${ref.id}`);

    const scanRes = await request.post(`/api/workspaces/${id}/downloads-janitor/scan`);
    expect(scanRes.status(), 'a recoverable state must not be a 500').toBe(409);
    const body = await scanRes.json();
    expect(body.code || body.error?.code).toBe('folder_unavailable');

    await page.reload();
    // Details says something is wrong; the console says what.
    //
    // That split is deliberate: the compact card is a summary, so it carries the
    // state, and the readiness rows that name the specific failure live in the
    // console the card opens. Both halves are asserted, because a card that
    // reported trouble with no way to find out what would be worse than either.
    await expect(page.locator('#downloadsJanitorMount')).toContainText('Needs attention', {
      timeout: 15000
    });
    await openConsole(page);
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText(/no longer linked/i, {
      timeout: 15000
    });
    // And the map station carries the same signal, so it is visible from the
    // surface the user actually works on rather than only in the page body.
    // The station is keyed by the capability id now that it is derived from the
    // install record rather than hardcoded (FR-93).
    await expect(page.locator('[data-cmd-hq-station="file-janitor"]').first()).toContainText(
      'Needs attention'
    );
  });

  test('setup that stops working asks to be repaired, and never re-ambushes', async ({
    page,
    request
  }) => {
    // The state this covers is the one a user actually hits months later: setup
    // was finished, and then something outside Ori changed. It must read as
    // "this broke", not as "you never set this up", and it must not throw a
    // modal over whatever they opened the workspace to do.
    const id = await createJanitorWorkspace(request, `DJ Repair ${RUN}`);
    const root = fixtureFolder('repair', { 'statement.pdf': 'pdf' });
    await completeSetup(page, id, root);

    const setup = async () => {
      const res = await request.get(`/api/workspaces/${id}/setup-wizard`);
      expect(res.ok(), await res.text()).toBeTruthy();
      return (await res.json()).setup;
    };
    expect((await setup()).state).toBe('ready');

    // A finished workspace does not reopen its wizard on the next visit.
    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Ready', { timeout: 15000 });
    await expect(page.locator('#setupWizardDialog')).toBeHidden();

    // Now break it the way reality does: the folder the user granted is gone.
    const dirs = await (await request.get(`/api/workspaces/${id}/directories`)).json();
    const ref = (dirs.directories || []).find((d: { path: string }) => d.path === root);
    expect(ref).toBeTruthy();
    await request.delete(`/api/workspaces/${id}/directories/${ref.id}`);

    // The server notices on the next look — no mutation needed to discover it.
    const degraded = await setup();
    expect(degraded.state).toBe('needs_attention');
    expect(degraded.completed_at, 'it completed once; that history is kept').toBeTruthy();
    expect(degraded.auto_open, 'a regression invites repair, it does not ambush').toBe(false);

    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Needs attention', {
      timeout: 15000
    });
    await expect(page.locator('#setupWizardBannerAction')).toHaveText('Repair setup');
    await expect(page.locator('#setupWizardDialog')).toBeHidden();

    // Repair opens at the step that broke, not back at the beginning.
    await page.locator('#setupWizardBannerAction').click();
    await expect(page.locator('#setupWizardDialog')).toBeVisible();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');
  });
});
