import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdtempSync, writeFileSync, utimesSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * File Janitor console accessibility (PRD task 8.10, FR-118–FR-123).
 *
 * Run with:
 *   ./scripts/e2e.sh tests/file-janitor.a11y.spec.ts
 *
 * The console is a modal that opens over a live Map. Everything below is a
 * property a keyboard or screen-reader user depends on and a sighted mouse user
 * would never notice:
 *
 *   - It announces itself as a dialog, named by the title on screen.
 *   - Focus goes into it, stays in it, and comes back out to the control that
 *     opened it.
 *   - Escape closes it — except while a File request is in flight.
 *   - Its tabs, filters, and row controls are real controls with real state.
 *   - Status changes are announced rather than only redrawn.
 *
 * These are asserted against the real surface rather than a snapshot, because
 * an axe scan cannot tell whether focus RETURNED to the right element — and
 * that is exactly where the bug was. The console held the button the user
 * pressed, and a status repaint had since replaced it with an identical one, so
 * focusing the original did nothing at all: .focus() on a detached node is a
 * silent no-op. The keyboard was left on <body>, behind a dialog that had just
 * closed.
 *
 * One thing deliberately NOT asserted: that the page does not scroll
 * horizontally at phone width. It already does, before this console exists —
 * .ws-cmd-main is ~670px against a 390px viewport. That belongs to the
 * workspace command page, not here. What is asserted instead is containment of
 * the console's own boxes, which is what this feature is answerable for.
 */

const RUN = Date.now().toString(36);
const OLD = new Date(Date.now() - 6 * 60 * 60 * 1000);
const workspaceSlugs = new Map<string, string>();

function inbox(label: string, names: string[]): string {
  const root = mkdtempSync(join(tmpdir(), `fj-a11y-${label}-`));
  if (!root.startsWith(tmpdir()) || root === tmpdir()) {
    throw new Error(`refusing to use ${root} as a fixture: not a temp directory`);
  }
  for (const name of names) {
    const path = join(root, name);
    writeFileSync(path, 'x');
    utimesSync(path, OLD, OLD);
  }
  return root;
}

async function workspaceWithFolder(
  request: APIRequestContext,
  name: string,
  root: string
): Promise<string> {
  const created = await request.post('/api/workspaces', { data: { name, description: '' } });
  expect(created.ok(), await created.text()).toBeTruthy();
  const body = await created.json();
  const workspaceId = (body.folder?.id || body.workspace?.id) as string;
  const workspaceSlug = (body.folder?.folder_slug || body.workspace?.folder_slug) as string;
  workspaceSlugs.set(workspaceId, workspaceSlug || workspaceId);

  // An agent keeps the unrelated Commander prompt off the screen; see
  // tests/file-janitor.spec.ts for why that matters.
  const agentName = `${name} Manager`;
  const agent = await request.post('/api/agents', {
    data: { name: agentName, type: 'general', model: 'gpt-4o-mini' }
  });
  expect(agent.ok(), await agent.text()).toBeTruthy();
  await request.put(`/api/agents/${encodeURIComponent(agentName)}/workspaces`, {
    data: { workspace_ids: [workspaceId] }
  });

  const install = await request.post(
    `/api/workspaces/${workspaceId}/capabilities/file-janitor/install`,
    { data: { source: 'in-place' } }
  );
  expect(install.ok(), await install.text()).toBeTruthy();

  const setup = await request.post(`/api/workspaces/${workspaceId}/file-janitor/setup`, {
    data: { path: root }
  });
  expect(setup.ok(), await setup.text()).toBeTruthy();
  return workspaceId;
}

async function openConsole(page: Page, workspaceId: string) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
  const workspaceSlug = workspaceSlugs.get(workspaceId) || workspaceId;
  await page.goto(`/workspaces/${encodeURIComponent(workspaceSlug)}`);
  await page.locator('#fileJanitorCardOpen').click();
  await expect(page.locator('#fileJanitorConsole')).toBeVisible({ timeout: 15000 });
}

test.describe('File Janitor console accessibility', () => {
  for (const theme of ['light', 'dark']) {
    test(`optional protection reflows, dismisses and preserves keyboard filing (${theme})`, async ({
      page,
      request
    }) => {
      test.setTimeout(60000);
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      const id = await workspaceWithFolder(
        request,
        `Protection layout ${theme} ${RUN}`,
        inbox('protection', ['important-original.pdf'])
      );
      await openConsole(page, id);
      await page.locator('#fileJanitorScan').click();
      await page.locator('#fileJanitorView-all').click();
      const note = page.getByRole('region', { name: 'Optional folder protection' });
      await expect(note).toBeVisible();
      await expect(note).toContainText('Filing is not a backup');
      await expect(page.locator('#fileJanitorCheckProtection')).toBeEnabled();
      // The plan check answers a moment after the review opens and redraws it; the
      // File button is enabled only once it has, so wait for that before measuring.
      await expect(page.locator('#fileJanitorApprove')).toBeEnabled();
      // Scope the audit to the new surface; unrelated page chrome has its own suite.
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const evidence = 'tasks/evidence-file-curator-backup-guidance/group-4';
      mkdirSync(evidence, { recursive: true });
      for (const width of [320, 390, 768, 1280]) {
        await page.setViewportSize({ width, height: 900 });
        await note.scrollIntoViewIfNeeded();
        const bounds = await note.boundingBox();
        expect(bounds!.x).toBeGreaterThanOrEqual(0);
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1);
        expect(await note.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBeTruthy();
        const violations = await page.evaluate(
          async () =>
            (
              await (window as any).axe.run('.fj-protection', {
                runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }
              })
            ).violations
        );
        expect(violations).toEqual([]);
        await note.screenshot({
          path: join(evidence, `protection-${width}-${theme}.png`),
          animations: 'disabled'
        });
      }
      // Deterministic presentation fixtures only: no provider check or backup is
      // claimed by these states. Each uncovered state has one sentence (R53); a
      // covered folder, or one Ori cannot read, has no strip at all (R54).
      const sentences: Record<string, string | null> = {
        not_covered: 'this folder is not in the backups Ori can see',
        unknown: 'Ori does not know of one for this folder',
        configured: 'A backup is set up, but Ori has not seen it finish',
        covered: null,
        unavailable: null
      };
      for (const [state, sentence] of Object.entries(sentences)) {
        await page.route(`**/api/workspaces/${id}/file-janitor/protection`, async route => {
          const fact = await (await route.fetch()).json();
          fact.state = state;
          if (state === 'unavailable') {
            delete fact.root_generation;
            delete fact.binding_id;
            delete fact.destination;
          }
          await route.fulfill({ json: fact });
        });
        await page.evaluate(() => (window as any).FileJanitorConsole._loadProtection());
        if (sentence) {
          await expect(note).toContainText('Filing is not a backup');
          await expect(note).toContainText(sentence);
          // One sentence and two buttons, nothing else.
          await expect(note.getByRole('button')).toHaveText(['Check protection', 'Not now']);
          await note.screenshot({
            path: join(evidence, `protection-${state}-${theme}.png`),
            animations: 'disabled'
          });
        } else {
          await expect(note).toHaveCount(0);
        }
        await page.unroute(`**/api/workspaces/${id}/file-janitor/protection`);
      }
      await page.evaluate(() => (window as any).FileJanitorConsole._loadProtection());
      const notNow = note.getByRole('button', { name: 'Not now' });
      await notNow.focus();
      await page.keyboard.press('Enter');
      // Focus stays in the review, not the top of the page, and nothing about backups
      // is left on the Review tab (R54).
      await expect(page.locator('#fileJanitorTab-review')).toBeFocused();
      await expect(note).toHaveCount(0);
      await expect(page.locator('#fileJanitorCheckProtection')).toHaveCount(0);
      await expect(page.locator('#fileJanitorConsole')).not.toContainText('Filing is not a backup');
      await page.reload();
      await page.locator('#fileJanitorView-all').click();
      await expect(page.locator('#fileJanitorCheckProtection')).toHaveCount(0);
      await expect(note).toHaveCount(0);
      // Check protection stays on the Settings tab.
      await page.locator('[data-fj-tab="settings"]').click();
      await expect(page.locator('#fileJanitorCheckProtectionSettings')).toBeVisible();
      await page.locator('[data-fj-tab="review"]').click();
      await page.locator('#fileJanitorView-all').click();
      const select = page.getByRole('checkbox', {
        name: 'Select important-original.pdf',
        exact: true
      });
      await select.check();
      await expect(page.locator('#fileJanitorApprove')).toBeEnabled();
      const changed = await request.post(`/api/workspaces/${id}/file-janitor/relink`, {
        data: { path: inbox('protection-relinked', ['new-original.pdf']) }
      });
      expect(changed.ok(), await changed.text()).toBeTruthy();
      await request.post(`/api/workspaces/${id}/file-janitor/scan`);
      await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
      await expect(note).toContainText('Filing is not a backup');
    });
  }

  for (const theme of ['light', 'dark']) {
    test(`shared review guidance reflows and returns keyboard focus (${theme})`, async ({
      page,
      request
    }, testInfo) => {
      test.setTimeout(60000);
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      const root = inbox('guidance-long-folder-name', [
        'long-synthetic-review-filename.pdf',
        'second.csv'
      ]);
      const id = await workspaceWithFolder(request, `Guidance ${theme} ${RUN}`, root);
      await openConsole(page, id);
      await page.locator('#fileJanitorScan').click();
      await page.locator('#fileJanitorView-all').click();
      await expect(page.locator('.fj-select')).toHaveCount(2);
      await page.keyboard.press('Escape');
      const area = page.getByRole('region', { name: 'Next action', exact: true });
      for (const mode of ['details', 'map']) {
        await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
        for (const width of [320, 390, 768, 1280]) {
          await page.setViewportSize({ width, height: 900 });
          await expect(area).toContainText('2 files ready for review');
          await expect(area.locator('.is-primary')).toHaveCount(1);
          const primary = page.locator('#workspaceNextAction');
          const box = await primary.boundingBox();
          expect(box!.x).toBeGreaterThanOrEqual(0);
          expect(box!.x + box!.width).toBeLessThanOrEqual(width + 1);
          await primary.focus();
          await page.keyboard.press('Enter');
          await expect(page.locator('#fileJanitorConsole')).toBeVisible();
          await page.keyboard.press('Escape');
          await expect(primary).toBeFocused();
          await expect
            .poll(() =>
              page.evaluate(() => {
                const state = (window as any).FileJanitorConsole.getActionState();
                return [state.status, state.batch, state.history].every(
                  source => source.state === 'ready'
                );
              })
            )
            .toBe(true);
          await page.screenshot({
            path: testInfo.outputPath(`next-action-review-${mode}-${width}-${theme}.png`),
            fullPage: true,
            animations: 'disabled'
          });
        }
      }
    });
  }

  // Nothing may paint over the console.
  //
  // At the workspace-scoped modal layer the global nav (z 10002) clipped the
  // console's own header — Scan now, Pause and Close were half-hidden behind it
  // — and the Workspace Assistant pill (z 20100) floated over the review table.
  // Both are application furniture that the workspace page's layer scale never
  // accounted for.
  //
  // This asserts by hit-testing rather than by comparing z-index numbers: what
  // matters is what the user's click actually lands on.
  test('no app chrome paints over the console', async ({ page, request }) => {
    const root = inbox('stack', ['a.pdf', 'b.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Stack ${RUN}`, root);
    await page.setViewportSize({ width: 1400, height: 900 });
    await openConsole(page, workspaceId);
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    await expect(page.locator('.fj-row-item').first()).toBeVisible({ timeout: 15000 });

    // Compare the stacking directly rather than hit-testing a few points.
    //
    // A point test is too easy to pass by luck: the Close button's centre sits
    // one pixel below the nav's bottom edge, so elementFromPoint missed the
    // overlap entirely while the nav really was clipping the header. What
    // matters is whether anything that OVERLAPS the console is painted above it.
    const above = await page.evaluate(() => {
      const host = document.getElementById('fileJanitorConsole');
      if (!host) return [{ tag: 'MISSING', id: '', z: 0 }];
      const hostZ = Number(getComputedStyle(host).zIndex) || 0;
      const hostRect = host.getBoundingClientRect();
      const dialogRect = document
        .getElementById('fileJanitorConsoleDialog')
        ?.getBoundingClientRect();

      const offenders: Array<{ tag: string; id: string; cls: string; z: number }> = [];
      document.querySelectorAll('*').forEach(el => {
        if (host.contains(el) || el.contains(host)) return;
        const cs = getComputedStyle(el);
        if (cs.position !== 'fixed' && cs.position !== 'sticky') return;
        if (cs.zIndex === 'auto') return;
        const z = Number(cs.zIndex);
        if (!Number.isFinite(z) || z <= hostZ) return;
        if (cs.visibility === 'hidden' || cs.display === 'none') return;

        const r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) return;
        // Only things that actually overlap the console matter.
        const target = dialogRect ?? hostRect;
        const overlaps =
          r.right > target.left &&
          r.left < target.right &&
          r.bottom > target.top &&
          r.top < target.bottom;
        if (!overlaps) return;

        offenders.push({
          tag: el.tagName,
          id: (el as HTMLElement).id,
          cls: (el.className || '').toString().slice(0, 40),
          z
        });
      });
      return offenders;
    });

    expect(above, 'app chrome is painted above the console').toEqual([]);
  });

  test('is a dialog named by its visible title', async ({ page, request }) => {
    const root = inbox('name', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Name ${RUN}`, root);
    await openConsole(page, workspaceId);

    const dialog = page.getByRole('dialog', { name: 'Filing' });
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAttribute('aria-modal', 'true');

    // The accessible name is the heading on screen, not a separate string that
    // could drift from what a sighted user reads.
    const labelledBy = await dialog.getAttribute('aria-labelledby');
    expect(labelledBy).toBeTruthy();
    await expect(page.locator(`#${labelledBy}`)).toHaveText('Filing');
  });

  test('the depot card and station expose name, folder, state, and one keyboard entry', async ({
    page,
    request
  }) => {
    const root = inbox('depot-semantics-with-a-long-folder-name', ['review-me.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Depot Semantics ${RUN}`, root);
    const scanned = await request.post(`/api/workspaces/${workspaceId}/file-janitor/scan`);
    expect(scanned.ok(), await scanned.text()).toBeTruthy();

    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
    const workspaceSlug = workspaceSlugs.get(workspaceId) || workspaceId;
    await page.goto(`/workspaces/${encodeURIComponent(workspaceSlug)}`);

    const card = page.getByRole('group', { name: 'Filing' });
    await expect(card).toBeVisible({ timeout: 15000 });
    await expect(card.locator('dt')).toHaveText([
      'Current status',
      'Managed folder',
      'Review queue',
      'Privacy mode'
    ]);
    await expect(card).toContainText(basename(root));
    await expect(page.locator('#fileJanitorStats')).toContainText('1 file waiting for review');
    await expect(card.getByRole('button', { name: 'Review files · 1', exact: true })).toBeVisible();

    await page.getByRole('button', { name: 'Map', exact: true }).click();
    const station = page.getByRole('button', {
      name: new RegExp(`Filing station, managed folder ${basename(root)}, 1 file ready for review`)
    });
    await expect(station).toBeVisible();
    await station.focus();
    await expect(station).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('dialog', { name: 'Filing' })).toBeVisible();

    await page.locator('[data-fj-console-close]').click();
    await expect(station).toBeFocused();
  });

  test('puts focus inside on open and returns it to the opener on close', async ({
    page,
    request
  }) => {
    const root = inbox('focus', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Focus ${RUN}`, root);
    await openConsole(page, workspaceId);

    // Focus lands on the dialog itself, which is what puts a screen reader at
    // the title rather than partway into the controls.
    await expect(page.locator('#fileJanitorConsoleDialog')).toBeFocused();

    await page.locator('[data-fj-console-close]').click();
    await expect(page.locator('#fileJanitorConsole')).toBeHidden();
    // Back to the control that opened it, not to the top of the document.
    await expect(page.locator('#fileJanitorCardOpen')).toBeFocused();
  });

  test('keeps Tab inside the console', async ({ page, request }) => {
    const root = inbox('trap', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Trap ${RUN}`, root);
    await openConsole(page, workspaceId);

    // Tab a good number of times; focus must never escape into the page behind.
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press('Tab');
      const inside = await page.evaluate(() => {
        const host = document.getElementById('fileJanitorConsole');
        return Boolean(host && document.activeElement && host.contains(document.activeElement));
      });
      expect(inside, `focus escaped the console after ${i + 1} tabs`).toBe(true);
    }
  });

  test('Escape closes it and returns focus', async ({ page, request }) => {
    const root = inbox('escape', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Escape ${RUN}`, root);
    await openConsole(page, workspaceId);

    await page.keyboard.press('Escape');
    await expect(page.locator('#fileJanitorConsole')).toBeHidden({ timeout: 15000 });
    await expect(page.locator('#fileJanitorCardOpen')).toBeFocused();
  });

  // There is no confirm screen to protect any more: with files ticked and the
  // File button enabled, Escape just closes the console, and files nothing.
  test('Escape closes a review with files ticked and files nothing', async ({ page, request }) => {
    const root = inbox('escape-ticked', ['invoice.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Escape Ticked ${RUN}`, root);
    await openConsole(page, workspaceId);

    const filed: string[] = [];
    page.on('request', req => {
      if (req.method() === 'POST' && /\/batches\/[^/]+\/file$/.test(req.url()))
        filed.push(req.url());
    });
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    const row = page.locator('.fj-row-item').filter({ hasText: 'invoice.pdf' });
    await expect(row).toBeVisible({ timeout: 15000 });
    await row.locator('.fj-select').check();
    await expect(page.locator('#fileJanitorApprove')).toBeEnabled();

    await page.keyboard.press('Escape');
    await expect(page.locator('#fileJanitorConsole')).toBeHidden({ timeout: 15000 });
    expect(filed).toEqual([]);
    expect(existsSync(join(root, 'invoice.pdf'))).toBe(true);
  });

  // A File request that is in flight owns Escape: closing the console under it
  // would leave the person without the receipt of what was just filed.
  test('Escape does not close the console while a File request is in flight', async ({
    page,
    request
  }) => {
    const root = inbox('escape-press', ['invoice.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Escape Press ${RUN}`, root);
    await openConsole(page, workspaceId);

    let release: () => void = () => {};
    const gate = new Promise<void>(resolve => (release = resolve));
    await page.route('**/batches/*/file', async route => {
      await gate;
      await route.continue();
    });
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    const row = page.locator('.fj-row-item').filter({ hasText: 'invoice.pdf' });
    await expect(row).toBeVisible({ timeout: 15000 });
    await row.locator('.fj-select').check();
    await expect(page.locator('#fileJanitorApprove')).toBeEnabled();
    await page.locator('#fileJanitorApprove').click();

    await page.keyboard.press('Escape');
    await expect(page.locator('#fileJanitorConsole')).toBeVisible();
    release();
    await expect(page.locator('.fj-results')).toBeVisible({ timeout: 30000 });
    expect(existsSync(join(root, 'Filed', 'Documents', 'invoice.pdf'))).toBe(true);
  });

  test('tabs and filters are real controls that report their state', async ({ page, request }) => {
    const root = inbox('controls', ['a.pdf', 'b.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Controls ${RUN}`, root);
    await openConsole(page, workspaceId);

    // Scoped to the console: the workspace page behind it has tablists of its
    // own, and counting those would make this pass or fail for the wrong reason.
    const tabs = page.locator('#fileJanitorConsole').getByRole('tab');
    await expect(tabs).toHaveCount(3);
    await expect(page.locator('[data-fj-tab="review"]')).toHaveAttribute('aria-selected', 'true');

    const reviewTab = page.locator('[data-fj-tab="review"]');
    await expect(reviewTab).toHaveAttribute('aria-controls', 'fileJanitorConsoleBody');
    await expect(page.locator('#fileJanitorConsoleBody')).toHaveAttribute('role', 'tabpanel');
    await expect(page.locator('#fileJanitorConsoleBody')).toHaveAttribute(
      'aria-labelledby',
      'fileJanitorTab-review'
    );

    await page.locator('[data-fj-tab="history"]').click();
    await expect(page.locator('[data-fj-tab="history"]')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[data-fj-tab="history"]')).toBeFocused();
    await expect(reviewTab).toHaveAttribute('aria-selected', 'false');

    await page.keyboard.press('End');
    await expect(page.locator('[data-fj-tab="settings"]')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[data-fj-tab="settings"]')).toBeFocused();
    await page.keyboard.press('Home');
    await expect(reviewTab).toHaveAttribute('aria-selected', 'true');
    await expect(reviewTab).toBeFocused();
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    await expect(page.locator('.fj-row-item').first()).toBeVisible({ timeout: 15000 });

    // Filters report pressed state, and every row control is labelled with the
    // file it acts on — "Select" alone is useless in a list of twelve.
    const filters = page.locator('.fj-filter');
    expect(await filters.count()).toBeGreaterThan(0);
    for (let i = 0; i < (await filters.count()); i++) {
      await expect(filters.nth(i)).toHaveAttribute('aria-pressed', /true|false/);
    }
    const firstBox = page.locator('.fj-select').first();
    await expect(firstBox).toHaveAttribute('aria-label', /Select .+/);
  });

  test('status is announced, not only redrawn', async ({ page, request }) => {
    const root = inbox('live', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Live ${RUN}`, root);
    await openConsole(page, workspaceId);

    // The header status and the selection summary are both live regions, so a
    // scan finishing or a selection changing reaches a screen reader.
    await expect(page.locator('#fileJanitorConsoleStatus')).toHaveAttribute('aria-live', 'polite');
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    await expect(page.locator('.fj-row-item').first()).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#fileJanitorSelection')).toHaveAttribute('aria-live', 'polite');
  });

  test('stays operable at a short 200%-zoom-equivalent viewport', async ({ page, request }) => {
    const root = inbox(
      'zoom',
      Array.from({ length: 60 }, (_, i) => `zoom-file-${i}.pdf`)
    );
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Zoom ${RUN}`, root);
    // At 200% browser zoom a 1440×900 display exposes roughly a 720×450 CSS
    // viewport. Driving that viewport directly is deterministic in headless
    // Chromium while exercising the same responsive layout boundary.
    await page.setViewportSize({ width: 720, height: 450 });

    await openConsole(page, workspaceId);
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    const firstRow = page.locator('.fj-row-item').first();
    await expect(firstRow).toBeVisible({ timeout: 15000 });
    await expect(page.locator('.fj-batch-progress')).toContainText('60 remaining of 60 files');

    const containment = await page.evaluate(() => {
      const dialog = document.querySelector('#fileJanitorConsoleDialog');
      const body = document.querySelector('#fileJanitorConsoleBody');
      const progress = document.querySelector('.fj-batch-progress');
      return {
        viewport: innerWidth,
        dialog: dialog?.getBoundingClientRect().width || 0,
        body: body?.getBoundingClientRect().width || 0,
        progress: progress?.getBoundingClientRect().width || 0,
        bodyScrollsSideways: body ? body.scrollWidth > body.clientWidth : false
      };
    });
    expect(containment.dialog).toBeLessThanOrEqual(containment.viewport);
    expect(containment.progress).toBeLessThanOrEqual(containment.body);
    expect(containment.bodyScrollsSideways).toBe(false);

    // The confident folder opens ticked, so all 60 are selected; leaving one out
    // changes the count and the footer button stays in view.
    await expect(page.locator('#fileJanitorApprove')).toHaveText('File 60 files');
    await firstRow.locator('.fj-select').uncheck();
    await expect(page.locator('#fileJanitorApprove')).toHaveText('File 59 files');
    await expect(page.locator('.fj-footer')).toBeInViewport();
    await expect(page.locator('[data-fj-console-close]')).toBeInViewport();
  });

  test('scrolls inside itself and never sideways at phone width', async ({ page, request }) => {
    const root = inbox(
      'narrow',
      Array.from({ length: 30 }, (_, i) => `file-${i}.pdf`)
    );
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Narrow ${RUN}`, root);
    await page.setViewportSize({ width: 390, height: 780 });

    await openConsole(page, workspaceId);
    await page.locator('#fileJanitorScan').click();
    await page.locator('#fileJanitorView-all').click();
    await expect(page.locator('.fj-row-item').first()).toBeVisible({ timeout: 15000 });

    // Containment is asserted on the console's own boxes rather than on the
    // document's scrollWidth.
    //
    // The workspace command page already scrolls sideways at 390px before this
    // console exists — .ws-cmd-main is ~670px against a 390px viewport — so a
    // document-level assertion would fail on somebody else's bug. And measuring
    // the document before/after is worse still: the page is still laying out,
    // so the "growth" is the page finishing, not the table.
    //
    // What File Janitor is answerable for is that the review rows reflow inside
    // the console instead of creating a second sideways scrolling surface.
    const widths = await page.evaluate(() => {
      const box = (sel: string) => {
        const el = document.querySelector(sel);
        return el ? Math.round(el.getBoundingClientRect().width) : -1;
      };
      const scroller = document.querySelector('.fj-table-scroll');
      return {
        viewport: window.innerWidth,
        dialog: box('#fileJanitorConsoleDialog'),
        body: box('#fileJanitorConsoleBody'),
        scroller: box('.fj-table-scroll'),
        table: box('.fj-table'),
        row: box('.fj-row-item'),
        scrollerScrolls: scroller ? scroller.scrollWidth > scroller.clientWidth : false
      };
    });

    expect(widths.dialog, 'the console is wider than the screen').toBeLessThanOrEqual(
      widths.viewport
    );
    expect(widths.body, 'the console body is wider than the screen').toBeLessThanOrEqual(
      widths.viewport
    );
    expect(widths.scroller, 'the table container is wider than the screen').toBeLessThanOrEqual(
      widths.viewport
    );
    expect(widths.table, 'the table did not reflow inside its container').toBeLessThanOrEqual(
      widths.scroller
    );
    expect(widths.row, 'a review row did not reflow inside its container').toBeLessThanOrEqual(
      widths.scroller
    );
    expect(widths.scrollerScrolls, 'the table still creates sideways scrolling').toBe(false);
    await expect(page.locator('.fj-row-item').first().locator('.fj-cell-mobile-label')).toHaveText([
      'Destination',
      'Why / Status',
      'Actions'
    ]);

    const details = page.locator('.fj-file-details-toggle').first();
    await expect(details).toHaveAttribute('aria-expanded', 'false');
    await details.focus();
    await page.keyboard.press('Enter');
    await expect(details).toHaveAttribute('aria-expanded', 'true');
    await expect(page.locator('.fj-file-details').first()).toBeVisible();

    // The header — and its Close — stay reachable however far the body scrolls.
    await page.locator('#fileJanitorConsoleBody').evaluate(el => el.scrollTo(0, el.scrollHeight));
    await expect(page.locator('[data-fj-console-close]')).toBeInViewport();
  });
});

// The picture of the folder after filing (R65 to R69): keyboard order, what a screen
// reader hears, the narrow and zoomed layouts, both themes and long names.
test.describe('Filing picture accessibility', () => {
  const LONG_NAME =
    'A very long scanned contract for the whole of the northern region with every annex attached 2026.pdf';
  const names = [LONG_NAME, 'photo-0.png', 'photo-1.png', 'photo-2.png'];

  async function openPicture(page: Page, request: APIRequestContext, label: string) {
    const root = inbox(label, names);
    const workspaceId = await workspaceWithFolder(request, `FJ Picture ${label} ${RUN}`, root);
    await openConsole(page, workspaceId);
    await page.locator('#fileJanitorScan').click();
    await expect(page.locator('#fileJanitorPicture')).toBeVisible({ timeout: 20000 });
    await expect(page.locator('#fileJanitorApprove')).toBeEnabled({ timeout: 20000 });
    return workspaceId;
  }

  async function axeViolations(page: Page, selector: string) {
    return page.evaluate(
      async scope =>
        (
          await (window as any).axe.run(scope, {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }
          })
        ).violations.map((v: { id: string; nodes: unknown[] }) => `${v.id} x${v.nodes.length}`),
      selector
    );
  }

  for (const theme of ['light', 'dark']) {
    test(`the picture, the receipt and the Undone view pass axe, with reduced motion (${theme})`, async ({
      page,
      request
    }) => {
      test.setTimeout(90000);
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await openPicture(page, request, `axe-${theme}`);
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      expect(await axeViolations(page, '.fj-picture-host')).toEqual([]);
      // Reduced motion (openConsole asks for it): nothing in the picture animates.
      const moving = await page.evaluate(() =>
        [...document.querySelectorAll('.fj-picture-host, .fj-picture-host *')]
          .filter(node => {
            // The app's own reduced-motion rule makes every duration 0.01 ms, which
            // is no motion a person can see: only a visible duration counts.
            const milliseconds = (value: string) =>
              value.trim().endsWith('ms') ? parseFloat(value) : parseFloat(value) * 1000;
            const style = getComputedStyle(node);
            return (
              (style.animationName !== 'none' &&
                style.animationDuration.split(',').some(value => milliseconds(value) > 1)) ||
              style.transitionDuration.split(',').some(value => milliseconds(value) > 1)
            );
          })
          .map(node => `${node.tagName.toLowerCase()}.${(node as HTMLElement).className}`)
      );
      expect([...new Set(moving)], 'something in the picture still moves').toEqual([]);

      await page.locator('#fileJanitorApprove').click();
      await expect(page.locator('#fileJanitorReceipt')).toBeVisible({ timeout: 30000 });
      expect(await axeViolations(page, '#fileJanitorReceipt')).toEqual([]);
      await page.locator('#fileJanitorUndoAll').click();
      await expect(page.locator('#fileJanitorUndone')).toBeVisible({ timeout: 30000 });
      expect(await axeViolations(page, '#fileJanitorUndone')).toEqual([]);
    });
  }

  test.describe('Possible-copy decision controls', () => {
    test.use({ hasTouch: true });
    for (const theme of ['light', 'dark']) {
      test(`stay reachable with a coarse pointer and zoom-equivalent reflow (${theme})`, async ({ page, request }) => {
        const names = ['Reader guide.pdf', 'Reader guide (2).pdf', 'photo.png'];
        await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
        await page.setViewportSize({ width: 390, height: 844 });
        const root = inbox(`pair-${theme}`, names);
        const ws = await workspaceWithFolder(request, `FJ Pair ${theme} ${RUN}`, root);
        await openConsole(page, ws);
        await page.locator('#fileJanitorScan').click();
        const picture = page.locator('#fileJanitorPicture');
        const review = page.locator('#fileJanitorConsole');
        const jump = review.getByRole('button', { name: '2 need a decision' });
        await expect(jump).toBeEnabled();
        expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true);
        expect((await jump.boundingBox())!.height).toBeGreaterThanOrEqual(44);
        await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
        await page.addScriptTag({ path: fileURLToPath(new URL('../node_modules/axe-core/axe.min.js', import.meta.url)) });
        expect(await axeViolations(page, '#fileJanitorPicture')).toEqual([]);

        await jump.focus();
        await page.keyboard.press('Enter');
        await expect(review.locator('#fileJanitorLooseTitle')).toBeFocused();
        const mark = review.getByRole('button', { name: `Mark for Trash: ${names[1]}` });
        await mark.scrollIntoViewIfNeeded();
        expect((await mark.boundingBox())!.height).toBeGreaterThanOrEqual(44);
        await mark.focus();
        await page.keyboard.press('Enter');
        const choice = review.getByRole('combobox', { name: `Filing choice for ${names[1]}` });
        await expect(choice).toBeFocused();
        await expect(review.locator('#fileJanitorApprove')).toBeDisabled();
        expect(await axeViolations(page, '#fileJanitorPicture')).toEqual([]);

        for (const width of [390, 720]) {
          await page.setViewportSize({ width, height: width === 390 ? 844 : 450 });
          await choice.evaluate(node => node.scrollIntoView({ block: 'center' }));
          await expect(choice).toBeInViewport({ ratio: 1 });
          const focused = await choice.boundingBox();
          const footer = await review.locator('.fj-footer').boundingBox();
          expect(focused!.y + focused!.height).toBeLessThanOrEqual(footer!.y);
          expect(await picture.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
        }
        const evidence = 'tasks/evidence-downloads-review-decisions';
        mkdirSync(evidence, { recursive: true });
        await page.screenshot({ path: join(evidence, `04-zoom-equivalent-${theme}.png`), mask: [review.locator('.fj-console-folder')], maskColor: '#888' });
      });
    }
  });

  test('Tab moves through the picture in reading order and reaches the File button', async ({
    page,
    request
  }) => {
    await openPicture(page, request, 'order');
    await page.locator('#fileJanitorFolder-documents').focus();
    const order: number[] = [];
    let reached = false;
    for (let step = 0; step < 60 && !reached; step++) {
      const at = await page.evaluate(() => {
        const node = document.activeElement as HTMLElement;
        return {
          index: [...document.querySelectorAll('*')].indexOf(node),
          id: node?.id || '',
          dialog: Boolean(node?.closest('#fileJanitorConsoleDialog'))
        };
      });
      expect(at.dialog, 'focus left the console').toBe(true);
      order.push(at.index);
      if (at.id === 'fileJanitorApprove') reached = true;
      else await page.keyboard.press('Tab');
    }
    expect(reached, 'the File button was not reached by Tab').toBe(true);
    // Each stop is after the one before it in the page: reading order.
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });

  test('a change is said to a screen reader', async ({ page, request }) => {
    await openPicture(page, request, 'announce');
    const live = page.locator('#fileJanitorAnnounce');
    await expect(live).toHaveAttribute('aria-live', 'polite');
    await page.locator('#fileJanitorFolder-images').click();
    await expect(live).toHaveText('3 files no longer set to be filed in Images.');
    await page.locator('#fileJanitorFolder-images').click();
    await expect(live).toHaveText('3 files moved to Images.');
  });

  test('at 390 px the folder cards stack, nothing scrolls sideways and the button is reachable', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openPicture(page, request, 'narrow');
    const layout = await page.evaluate(() => {
      const cards = [...document.querySelectorAll('.fj-folder-card')].map(card => {
        const box = card.getBoundingClientRect();
        return {
          left: Math.round(box.left),
          top: Math.round(box.top),
          right: Math.round(box.right)
        };
      });
      const host = document.querySelector('.fj-picture-host') as HTMLElement;
      const limit = host.getBoundingClientRect().right + 1;
      const past = [...host.querySelectorAll('*')]
        .filter(node => node.getBoundingClientRect().right > limit)
        .slice(0, 6)
        .map(node => `${node.tagName.toLowerCase()}.${(node as HTMLElement).className}`);
      return {
        cards,
        viewport: innerWidth,
        hostScrolls: host.scrollWidth > host.clientWidth + 1,
        past
      };
    });
    expect(layout.past, 'these reach past the picture').toEqual([]);
    expect(layout.cards.length).toBeGreaterThanOrEqual(2);
    // One per row: the same left edge, each below the one before, none past the screen.
    expect(new Set(layout.cards.map(card => card.left)).size).toBe(1);
    expect(layout.cards.map(card => card.top)).toEqual(
      [...layout.cards.map(card => card.top)].sort((a, b) => a - b)
    );
    expect(new Set(layout.cards.map(card => card.top)).size).toBe(layout.cards.length);
    expect(Math.max(...layout.cards.map(card => card.right))).toBeLessThanOrEqual(layout.viewport);
    expect(layout.hostScrolls).toBe(false);
    await page.locator('#fileJanitorApprove').scrollIntoViewIfNeeded();
    await expect(page.locator('#fileJanitorApprove')).toBeInViewport();
  });

  test('at a 200%-zoom-equivalent viewport the picture still works and the button is in view', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 720, height: 450 });
    await openPicture(page, request, 'zoom');
    await page.locator('#fileJanitorApprove').scrollIntoViewIfNeeded();
    await expect(page.locator('#fileJanitorApprove')).toBeInViewport();
    await expect(page.locator('[data-fj-console-close]')).toBeInViewport();
    const sideways = await page
      .locator('#fileJanitorConsoleBody')
      .evaluate(body => body.scrollWidth > body.clientWidth + 1);
    expect(sideways).toBe(false);
  });

  test('a long file name is shortened in the middle, keeps its extension and opens out on focus and hover (R69)', async ({
    page,
    request
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openPicture(page, request, 'long');
    const name = page.locator('.fj-mid').first();
    await expect(name).toHaveAttribute('title', LONG_NAME);
    // The whole name is in the page: it is what a screen reader reads.
    await expect(name).toHaveText(LONG_NAME);
    const end = name.locator('.fj-mid-end');
    await expect(end).toBeInViewport();
    expect(await end.textContent()).toMatch(/\.pdf$/);
    const shown = await name.evaluate(node => {
      const start = node.querySelector('.fj-mid-start') as HTMLElement;
      return {
        clipped: start.scrollWidth > start.clientWidth,
        fits: node.scrollWidth <= innerWidth
      };
    });
    expect(shown.clipped, 'the start of a long name is cut with an ellipsis').toBe(true);
    expect(shown.fits).toBe(true);
    // Hover and focus show it in full.
    const control = page.locator('.fj-file-name', { has: name });
    await control.hover();
    expect(
      await name.locator('.fj-mid-start').evaluate(node => getComputedStyle(node).whiteSpace)
    ).toBe('normal');
    await page.mouse.move(0, 0);
    await control.focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    expect(
      await name.locator('.fj-mid-start').evaluate(node => getComputedStyle(node).whiteSpace)
    ).toBe('normal');
  });
});
