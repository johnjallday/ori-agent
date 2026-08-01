import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdtempSync, writeFileSync, utimesSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

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
 *   - Escape closes it — except while a destructive confirmation is waiting.
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
  await page.goto(`/workspaces/${workspaceId}`);
  await page.locator('#fileJanitorCardOpen').click();
  await expect(page.locator('#fileJanitorConsole')).toBeVisible({ timeout: 15000 });
}

// Serial: see tests/file-janitor.spec.ts. These do real filesystem work against
// one shared server, and running them fully parallel alongside the other
// janitor suites overloads it.
test.describe.configure({ mode: 'serial' });

test.describe('File Janitor console accessibility', () => {
  test('is a dialog named by its visible title', async ({ page, request }) => {
    const root = inbox('name', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Name ${RUN}`, root);
    await openConsole(page, workspaceId);

    const dialog = page.getByRole('dialog', { name: 'File Janitor' });
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAttribute('aria-modal', 'true');

    // The accessible name is the heading on screen, not a separate string that
    // could drift from what a sighted user reads.
    const labelledBy = await dialog.getAttribute('aria-labelledby');
    expect(labelledBy).toBeTruthy();
    await expect(page.locator(`#${labelledBy}`)).toHaveText('File Janitor');
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

  // A destructive confirmation owns Escape while it is on screen: the answer to
  // "are you sure" must be given, not dismissed by closing the surface asking.
  test('Escape does not dismiss a pending approval', async ({ page, request }) => {
    const root = inbox('confirm', ['invoice.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Confirm ${RUN}`, root);
    await openConsole(page, workspaceId);

    await page.locator('#downloadsJanitorScan').click();
    const row = page.locator('.dj-row-item').filter({ hasText: 'invoice.pdf' });
    await expect(row).toBeVisible({ timeout: 15000 });
    await row.locator('.dj-select').check();
    await page.locator('#downloadsJanitorApprove').click();
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Confirm', {
      timeout: 15000
    });

    await page.keyboard.press('Escape');
    await expect(page.locator('#fileJanitorConsole')).toBeVisible();
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Confirm');
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

    await page.locator('[data-fj-tab="history"]').click();
    await expect(page.locator('[data-fj-tab="history"]')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('[data-fj-tab="review"]')).toHaveAttribute('aria-selected', 'false');

    await page.locator('[data-fj-tab="review"]').click();
    await page.locator('#downloadsJanitorScan').click();
    await expect(page.locator('.dj-row-item').first()).toBeVisible({ timeout: 15000 });

    // Filters report pressed state, and every row control is labelled with the
    // file it acts on — "Select" alone is useless in a list of twelve.
    const filters = page.locator('.dj-filter');
    expect(await filters.count()).toBeGreaterThan(0);
    for (let i = 0; i < (await filters.count()); i++) {
      await expect(filters.nth(i)).toHaveAttribute('aria-pressed', /true|false/);
    }
    const firstBox = page.locator('.dj-select').first();
    await expect(firstBox).toHaveAttribute('aria-label', /Select .+/);
  });

  test('status is announced, not only redrawn', async ({ page, request }) => {
    const root = inbox('live', ['a.pdf']);
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Live ${RUN}`, root);
    await openConsole(page, workspaceId);

    // The header status and the selection summary are both live regions, so a
    // scan finishing or a selection changing reaches a screen reader.
    await expect(page.locator('#fileJanitorConsoleStatus')).toHaveAttribute('aria-live', 'polite');
    await page.locator('#downloadsJanitorScan').click();
    await expect(page.locator('.dj-row-item').first()).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#downloadsJanitorSelection')).toHaveAttribute('aria-live', 'polite');
  });

  test('scrolls inside itself and never sideways at phone width', async ({ page, request }) => {
    const root = inbox(
      'narrow',
      Array.from({ length: 30 }, (_, i) => `file-${i}.pdf`)
    );
    const workspaceId = await workspaceWithFolder(request, `FJ A11y Narrow ${RUN}`, root);
    await page.setViewportSize({ width: 390, height: 780 });

    await openConsole(page, workspaceId);
    await page.locator('#downloadsJanitorScan').click();
    await expect(page.locator('.dj-row-item').first()).toBeVisible({ timeout: 15000 });

    // Containment is asserted on the console's own boxes rather than on the
    // document's scrollWidth.
    //
    // The workspace command page already scrolls sideways at 390px before this
    // console exists — .ws-cmd-main is ~670px against a 390px viewport — so a
    // document-level assertion would fail on somebody else's bug. And measuring
    // the document before/after is worse still: the page is still laying out,
    // so the "growth" is the page finishing, not the table.
    //
    // What File Janitor is answerable for is that a review table wider than the
    // screen scrolls inside its own box instead of stretching anything.
    const widths = await page.evaluate(() => {
      const box = (sel: string) => {
        const el = document.querySelector(sel);
        return el ? Math.round(el.getBoundingClientRect().width) : -1;
      };
      const scroller = document.querySelector('.dj-table-scroll');
      return {
        viewport: window.innerWidth,
        dialog: box('#fileJanitorConsoleDialog'),
        body: box('#fileJanitorConsoleBody'),
        scroller: box('.dj-table-scroll'),
        table: box('.dj-table'),
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
    // The table really is wider than its container, and really does scroll
    // there — otherwise this test would pass on a table that simply fit.
    expect(widths.table, 'this fixture produced no overflowing table').toBeGreaterThan(
      widths.scroller
    );
    expect(widths.scrollerScrolls, 'the table does not scroll in its own box').toBe(true);

    // The header — and its Close — stay reachable however far the body scrolls.
    await page.locator('#fileJanitorConsoleBody').evaluate(el => el.scrollTo(0, el.scrollHeight));
    await expect(page.locator('[data-fj-console-close]')).toBeInViewport();
  });
});
