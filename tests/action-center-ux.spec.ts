import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { basename, join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';
import { checkNextActionReflow } from './helpers/next-action-reflow';

// Deterministic response/fault tests are separate from the seeded real-endpoint
// journey below. No model is involved in either kind of coverage.
const finding = {
  id: 'finding-1',
  workspace_id: 'ws-1',
  workspace_slug: 'triage-demo',
  workspace_name: 'Triage Demo',
  title: 'Review the launch checklist',
  summary: 'Check the launch checklist before handing off the workspace.',
  status: 'new',
  priority: 'high'
};
const listRoute = '**/api/action-center/opportunities?*';

async function pageSetup(page: Page) {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', r =>
    r.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
}

async function fixture(page: Page, items = [finding]) {
  await pageSetup(page);
  await page.route('**/api/action-center/library', r => r.fulfill({ json: { items: [] } }));
  await page.route(listRoute, r => {
    const status = new URL(r.request().url()).searchParams.get('status');
    const visible = items.filter(item => status === 'all' || item.status === (status || 'new'));
    return r.fulfill({ json: { items: visible, total: visible.length } });
  });
}

test('repeated empty transitions name scope and expose named filters and recovery', async ({
  page
}) => {
  await fixture(page);
  await page.goto('/action-center');
  const status = page.getByRole('combobox', { name: 'Finding status' });
  await expect(status).toHaveValue('');
  await expect(page.getByRole('combobox', { name: 'Sort findings' })).toHaveValue('priority');
  await expect(page.getByRole('button', { name: 'Refresh findings' })).toBeVisible();
  for (let i = 0; i < 2; i++) {
    await expect(page.getByRole('link', { name: finding.title })).toBeVisible();
    await status.selectOption('resolved');
    await expect(page.getByRole('heading', { name: 'No matching findings' })).toBeVisible();
    await expect(page.locator('#action-center-empty')).toContainText('no resolved findings');
    await expect(page.locator('.action-center-row')).toHaveCount(0);
    await page.getByRole('button', { name: 'Show all findings' }).click();
    await expect(status).toBeFocused();
    await expect(status).toHaveValue('all');
  }
  await page.goto('/action-center?workspace=ws-1');
  await status.selectOption('dismissed');
  await expect(page.locator('#action-center-empty')).toContainText(
    'no dismissed findings in this workspace'
  );
  await page.getByRole('link', { name: 'Clear filters' }).click();
  await expect(page).toHaveURL(/\/action-center\?status=all$/);
  await expect(status).toHaveValue('all');
  await expect(page.getByRole('link', { name: finding.title })).toBeVisible();
});

test('empty Active is not first use, and an empty All view offers the Map', async ({ page }) => {
  await fixture(page, []);
  await page.goto('/action-center');
  await expect(
    page.getByRole('heading', { name: 'No active findings', exact: true })
  ).toBeVisible();
  await expect(page.locator('#action-center-empty')).toContainText('Snoozed findings return');
  await page.getByRole('button', { name: 'Show all findings' }).click();
  await expect(page.getByRole('heading', { name: 'No findings', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Workspace Map' })).toHaveAttribute('href', '/');
  await expect(page.getByRole('button', { name: 'Show all findings' })).toHaveCount(0);
});

test('injected initial and refresh failures retain honest state and Retry recovers', async ({
  page
}, info) => {
  await fixture(page);
  let fail = true;
  await page.route(listRoute, async r => {
    if (fail) await r.fulfill({ status: 503, json: { error: 'injected outage' } });
    else await r.fallback();
  });
  await page.goto('/action-center');
  await expect(page.getByRole('heading', { name: 'Unable to load findings' })).toBeVisible();
  await expect(page.locator('#action-center-empty')).toBeHidden();
  await expect(page.locator('#action-center-retained')).toBeHidden();
  fail = false;
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByRole('link', { name: finding.title })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Refresh findings' })).toBeFocused();
  fail = true;
  await page.getByRole('combobox', { name: 'Finding status' }).selectOption('resolved');
  await expect(page.getByRole('heading', { name: 'Unable to load findings' })).toBeVisible();
  await expect(page.locator('#action-center-retained')).toContainText(
    'last-loaded findings: Active'
  );
  await page.screenshot({
    path: info.outputPath('action-center-injected-retry.png'),
    fullPage: true,
    animations: 'disabled'
  });
  await expect(page.getByRole('link', { name: finding.title })).toBeVisible();
  await expect(page.locator('#action-center-empty')).toBeHidden();
  fail = false;
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'No matching findings' })).toBeVisible();
  await expect(page.locator('#action-center-retained')).toBeHidden();
});

for (const staleFails of [false, true]) {
  test(`latest filter owns loading and results when an older response ${staleFails ? 'fails' : 'succeeds'}`, async ({
    page
  }) => {
    await fixture(page);
    let release!: () => void;
    let started!: () => void;
    const pending = new Promise<void>(done => {
      release = done;
    });
    const requested = new Promise<void>(done => {
      started = done;
    });
    await page.route(listRoute, async r => {
      if (!new URL(r.request().url()).searchParams.has('status')) {
        started();
        await pending;
        await r.fulfill(
          staleFails ? { status: 503, json: {} } : { json: { items: [finding], total: 1 } }
        );
      } else await r.fallback();
    });
    await page.goto('/action-center');
    await requested;
    await expect(page.locator('#action-center-status')).toHaveText('Loading findings…');
    await expect(page.locator('#action-center-empty')).toBeHidden();
    await page.getByRole('combobox', { name: 'Finding status' }).selectOption('resolved');
    await expect(page.getByRole('heading', { name: 'No matching findings' })).toBeVisible();
    const response = page.waitForResponse(r => r.url().includes('opportunities?sort=priority'));
    release();
    await (await response).finished();
    // Let the fetch/json continuation run without starting another load that
    // could mask an obsolete response overwriting the selected filter.
    await page.waitForTimeout(100);
    await expect(page.locator('#action-center-list')).toBeHidden();
    await expect(page.locator('#action-center-error')).toBeHidden();
    await expect(page.locator('#action-center-status')).toHaveText('0 findings');
    await expect(page.locator('#action-center-results')).toHaveAttribute('aria-busy', 'false');
  });
}

test('native title keyboard, ordinary, modified and middle clicks preserve owner routes and seen calls', async ({
  page
}) => {
  await fixture(page);
  await page
    .context()
    .route('**/workspaces/triage-demo', r =>
      r.fulfill({ contentType: 'text/html', body: '<h1>Workspace owner fixture</h1>' })
    );
  let seen = 0;
  await page.route('**/api/action-center/opportunities/ws-1/finding-1', r => {
    seen++;
    return r.fulfill({ json: { ...finding, seen_at: new Date().toISOString() } });
  });
  for (const activation of ['keyboard', 'ordinary', 'modified', 'middle']) {
    await page.goto('/action-center');
    const title = page.getByRole('link', { name: finding.title });
    await expect(title).toHaveAttribute('href', '/workspaces/triage-demo');
    if (activation === 'modified' || activation === 'middle') {
      const popup = page.context().waitForEvent('page');
      await title.click(
        activation === 'middle'
          ? { button: 'middle' }
          : { modifiers: [process.platform === 'darwin' ? 'Meta' : 'Control'] }
      );
      const destination = await popup;
      await expect(destination).toHaveURL(/\/workspaces\/triage-demo$/);
      await destination.close();
      await expect(page).toHaveURL(/\/action-center$/);
    } else if (activation === 'keyboard') {
      await page.getByRole('button', { name: 'Refresh findings' }).focus();
      await page.keyboard.press('Tab');
      await expect(title).toBeFocused();
      await page.keyboard.press('Enter');
      await expect(page).toHaveURL(/\/workspaces\/triage-demo$/);
    } else {
      await title.click();
      await expect(page).toHaveURL(/\/workspaces\/triage-demo$/);
    }
  }
  await expect.poll(() => seen).toBe(4);
});

test('missing slugs are not links and Home library cards stay read-only', async ({ page }) => {
  await fixture(page, [{ ...finding, workspace_slug: '' }]);
  await page.route('**/api/action-center/library', r =>
    r.fulfill({
      json: {
        items: [
          {
            home_id: 'home-1',
            home_name: 'Music Home',
            activatable: 2,
            route: '/workspaces/music-home/assistant#projectLibraryProposals'
          }
        ]
      }
    })
  );
  await page.goto('/action-center');
  await expect(page.locator('.action-center-row')).toContainText('Workspace unavailable');
  await expect(page.locator('.action-center-row a')).toHaveCount(0);
  const library = page.locator('#action-center-library');
  await expect(
    library.getByRole('link', { name: 'Open Music Home suggestions shelf' })
  ).toBeVisible();
  await expect(library.getByRole('button')).toHaveCount(0);
});

for (const action of ['Resolve', 'Dismiss', 'Snooze', 'Add to Backlog']) {
  test(`scripted ${action} removes the last active finding and restores keyboard focus`, async ({
    page
  }) => {
    await fixture(page);
    let removed = false;
    await page.route('**/api/action-center/opportunities/ws-1/finding-1/*', async r => {
      expect(r.request().method()).toBe('POST');
      removed = true;
      await r.fulfill({
        json: { status: 'resolved', item: { id: 'task-1' }, workspace_slug: 'triage-demo' }
      });
    });
    await page.route(listRoute, r =>
      removed ? r.fulfill({ json: { items: [], total: 0 } }) : r.fallback()
    );
    await page.goto('/action-center');
    const trigger = page
      .locator('.action-center-row')
      .getByRole('button', { name: action, exact: true });
    await trigger.focus();
    await trigger.press('Enter');
    if (action === 'Dismiss')
      await page
        .getByRole('dialog', { name: 'Dismiss finding' })
        .getByRole('button', { name: 'Dismiss', exact: true })
        .click();
    if (action === 'Snooze')
      await page
        .getByRole('dialog', { name: 'Snooze finding' })
        .getByRole('button', { name: 'Next week', exact: true })
        .click();
    await expect(
      page.getByRole('heading', { name: 'No active findings', exact: true })
    ).toBeVisible();
    await expect(page.locator('#action-center-status')).toBeFocused();
    if (action === 'Add to Backlog')
      await expect(page.getByRole('link', { name: 'Open item' })).toHaveAttribute(
        'href',
        '/workspaces/triage-demo?panel=backlog&task=task-1'
      );
  });
}

test('row removal focuses the next finding but never steals focus from a filter', async ({
  page
}) => {
  await fixture(page);
  let remaining = [finding, { ...finding, id: 'finding-2', title: 'Second finding' }];
  let release: (() => void) | undefined;
  await page.route(listRoute, r =>
    r.fulfill({ json: { items: remaining, total: remaining.length } })
  );
  await page.route('**/api/action-center/opportunities/ws-1/*/resolve', async r => {
    if (r.request().url().includes('finding-2'))
      await new Promise<void>(done => {
        release = done;
      });
    remaining = remaining.slice(1);
    await r.fulfill({ json: { status: 'resolved' } });
  });
  await page.goto('/action-center');
  await page
    .locator('.action-center-row')
    .first()
    .getByRole('button', { name: 'Resolve', exact: true })
    .press('Enter');
  await expect(page.getByRole('link', { name: 'Second finding' })).toBeFocused();
  await page.getByRole('button', { name: 'Resolve', exact: true }).press('Enter');
  await expect.poll(() => Boolean(release)).toBe(true);
  const filter = page.getByRole('combobox', { name: 'Finding status' });
  await filter.focus();
  release!();
  await expect(
    page.getByRole('heading', { name: 'No active findings', exact: true })
  ).toBeVisible();
  await expect(filter).toBeFocused();
});

test('cancelling a triage modal returns focus to its row without mutation', async ({ page }) => {
  await fixture(page);
  await page.goto('/action-center');
  for (const name of ['Dismiss', 'Snooze']) {
    const trigger = page.locator('.action-center-row').getByRole('button', { name, exact: true });
    await trigger.press('Enter');
    const dialog = page.getByRole('dialog', { name: `${name} finding` });
    await expect(dialog).toBeVisible();
    // Opening and closing animations are part of the real Bootstrap contract.
    await expect(dialog).toHaveCSS('display', 'block');
    await dialog.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  }
  await expect(page.getByRole('link', { name: finding.title })).toBeVisible();
});

test('touched rows wrap at narrow width and keep visible focus in both themes', async ({
  page
}, info) => {
  await fixture(page, [{ ...finding, title: finding.title + ' — ' + 'LongFinding'.repeat(15) }]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/action-center');
  await expect(page.locator('.action-center-row')).toBeVisible();
  for (const theme of ['light', 'dark']) {
    await page.evaluate(theme => localStorage.setItem('ori-theme', theme), theme);
    await page.reload();
    await expect(page.locator('.action-center-row')).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    const title = page.locator('[data-action="open"]');
    await title.focus();
    await expect(title).toBeFocused();
    const overflow = await page
      .locator('.action-center-row')
      .evaluate(el => el.scrollWidth > el.clientWidth);
    expect(overflow).toBe(false);
    const outline = await title.evaluate(el => getComputedStyle(el).outlineStyle);
    expect(outline).not.toBe('none');
    await page.screenshot({
      path: info.outputPath(`action-center-narrow-${theme}.png`),
      fullPage: true,
      animations: 'disabled'
    });
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const violations = await page.evaluate(
      async () =>
        (
          await (window as any).axe.run('.main-content', {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations
    );
    expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
    await checkNextActionReflow(page, '.main-content');
    await page.screenshot({
      path: info.outputPath(`action-center-zoom-${theme}.png`),
      fullPage: true
    });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '';
    });
    await page.setViewportSize({ width: 390, height: 844 });
  }
});

test('seeded real endpoints: keyboard opening marks seen; filtering and final Resolve work', async ({
  page
}, info) => {
  const sandbox = process.env.ORI_ACTION_CENTER_SANDBOX;
  test.skip(
    !sandbox,
    'Set ORI_ACTION_CENTER_SANDBOX to this isolated wt demo sandbox for the real mutation demo'
  );
  const root = resolve(sandbox!);
  expect(basename(root)).toMatch(/^ori-demo\./);
  await pageSetup(page);
  const created = await page.request.post('/api/workspaces', {
    data: { name: `Triage UX ${Date.now()}` }
  });
  expect(created.ok()).toBeTruthy();
  const id = (await created.json()).folder.id;
  const now = new Date().toISOString();
  try {
    // Synthetic finding, real persistence/read/seen/resolve. There is no public
    // finding-create API; target only the newly created ID in the demo database.
    execFileSync('python3', [
      '-c',
      'import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); c=db.execute("UPDATE workspaces SET opportunities_json=? WHERE id=?", (sys.argv[3],sys.argv[2])); assert c.rowcount == 1; db.commit()',
      join(root, 'sessions.db'),
      id,
      JSON.stringify([{ ...finding, workspace_id: id, created_at: now, updated_at: now }])
    ]);
    const scoped = `/action-center?workspace=${id}`;
    await page.goto(scoped);
    const title = page.getByRole('link', { name: finding.title });
    await expect(title).toBeVisible();
    const href = await title.getAttribute('href');
    await title.focus();
    await title.press('Enter');
    await expect(page).toHaveURL(new RegExp(href! + '$'));
    const seen = await page.request.get(`/api/action-center/opportunities?workspace=${id}`);
    expect((await seen.json()).items[0].seen_at).toBeTruthy();
    await page.goto(scoped);
    await expect(title).toBeVisible();
    await page.screenshot({
      path: info.outputPath('action-center-real-populated.png'),
      fullPage: true
    });
    await page.getByRole('combobox', { name: 'Finding status' }).selectOption('resolved');
    await expect(page.getByRole('heading', { name: 'No matching findings' })).toBeVisible();
    await page.getByRole('combobox', { name: 'Finding status' }).selectOption('');
    await expect(title).toBeVisible();
    const resolveButton = page.getByRole('button', { name: 'Resolve', exact: true });
    await resolveButton.focus();
    await resolveButton.press('Enter');
    await expect(
      page.getByRole('heading', { name: 'No active findings in this workspace' })
    ).toBeVisible();
    await expect(page.locator('#action-center-status')).toBeFocused();
    await page.screenshot({
      path: info.outputPath('action-center-real-empty.png'),
      fullPage: true
    });
    const all = await page.request.get(
      `/api/action-center/opportunities?workspace=${id}&status=all`
    );
    expect((await all.json()).items[0].status).toBe('resolved');
  } finally {
    await page.request.delete(`/api/orchestration/workspace?id=${id}`);
  }
});
