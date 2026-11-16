import { test, expect, type Page, type TestInfo, type APIRequestContext } from '@playwright/test';
import { realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  assertSandbox,
  createWorkspace,
  removeOwnedWorkspace,
  preparePage,
  guardReadTour,
  capture as captureSurface,
  reachable,
  contrast
} from './helpers/ui-consistency';
import {
  actionCenterScene,
  actionCenterLibraryScene,
  themes
} from './fixtures/ui-consistency-scenes';

const cleanups = new Map<string, (request: APIRequestContext) => Promise<void>>();
test.afterEach(async ({ request }, info) => {
  try {
    await cleanups.get(info.testId)?.(request);
  } finally {
    cleanups.delete(info.testId);
  }
});
test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});
async function capture(page: Page, info: TestInfo, name: string, facts = {}) {
  await captureSurface(
    page,
    info,
    name,
    { fixture: 'fictional finding/library reads; no seen GET or triage writes', ...facts },
    { fullPage: false }
  );
}
async function actionReads(page: Page, state = () => 'populated') {
  const unexpected: string[] = [];
  await page.route('**/api/action-center/**', route => {
    const req = route.request();
    if (req.method() !== 'GET') return route.fallback();
    const url = new URL(req.url());
    if (url.pathname === '/api/action-center/library' && !url.search)
      return route.fulfill({ json: actionCenterLibraryScene });
    if (url.pathname !== '/api/action-center/opportunities') {
      // The single-finding GET marks seen. It is a mutation despite its verb.
      unexpected.push(url.pathname + url.search);
      return route.abort('blockedbyclient');
    }
    if (state() === 'error')
      return route.fulfill({ status: 503, json: { error: 'Fictional findings unavailable' } });
    const status = url.searchParams.get('status') || 'new';
    const scope = url.searchParams.get('workspace');
    return route.fulfill({
      json: {
        items:
          state() === 'empty'
            ? []
            : actionCenterScene.items.filter(
                item =>
                  (status === 'all' || item.status === status) &&
                  (!scope || item.workspace_id === scope)
              )
      }
    });
  });
  return unexpected;
}

for (const theme of themes) {
  for (const width of [320, 1440])
    test(`Action Center long findings, native details and cancelled dialogs: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const unknown = await actionReads(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto('/action-center?status=all');
      await expect(page.locator('.action-center-row')).toHaveCount(3);
      await expect(page.locator('h1')).toHaveText('Action Center');
      await expect(page.locator('.navbar-brand')).toHaveText('Ori Agent');
      await reachable(page.locator('#action-center-refresh'), width < 768 ? 44 : 0);
      await reachable(page.locator('#action-center-status-filter'), width < 768 ? 44 : 0);
      await reachable(page.locator('#action-center-sort'), width < 768 ? 44 : 0);
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'action-center-library');
      const first = page.locator('.action-center-row').first();
      await expect(first.locator('[data-action="open"]')).toHaveAttribute(
        'href',
        '/workspaces/fictional-review-workspace'
      );
      expect(await first.evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
      expect(
        (await contrast(first.locator('.action-center-priority-danger'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        (await contrast(first.locator('.action-center-summary'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      for (const action of ['Add to Backlog', 'Resolve', 'Snooze', 'Dismiss'])
        await reachable(
          first.getByRole('button', { name: action, exact: true }),
          width < 768 ? 44 : 0
        );
      await first.locator('summary').focus();
      await page.keyboard.press('Enter');
      await expect(first.locator('.action-center-details-body').first()).toContainText(
        '<metadata>'
      );
      await capture(page, info, 'action-center-row');
      const dismiss = first.getByRole('button', { name: 'Dismiss', exact: true });
      await dismiss.click();
      const dismissModal = page.getByRole('dialog', { name: 'Dismiss finding' });
      await expect(dismissModal.locator('#dismiss-not-useful')).toBeFocused();
      for (const label of await dismissModal.locator('.form-check-label').all())
        await reachable(label, width < 768 ? 44 : 0);
      await capture(page, info, 'action-center-dismiss');
      await dismissModal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(dismiss).toBeFocused();
      const snooze = first.getByRole('button', { name: 'Snooze', exact: true });
      await snooze.click();
      const snoozeModal = page.getByRole('dialog', { name: 'Snooze finding' });
      await expect(
        snoozeModal.getByRole('button', { name: 'Tomorrow at 9am', exact: true })
      ).toBeFocused();
      const date = page.locator('#action-center-snooze-custom');
      await expect(date).not.toHaveAttribute('aria-invalid', 'true');
      await snoozeModal
        .getByRole('button', { name: 'Snooze until selected time', exact: true })
        .click();
      await expect(date).toBeFocused();
      await expect(date).toHaveAttribute('aria-invalid', 'true');
      await expect(page.locator('#action-center-snooze-error')).toHaveText(
        'Choose a valid date and time.'
      );
      await capture(page, info, 'action-center-snooze');
      await snoozeModal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(snooze).toBeFocused();
      await snooze.click();
      await expect(
        snoozeModal.getByRole('button', { name: 'Tomorrow at 9am', exact: true })
      ).toBeFocused();
      await expect(date).not.toHaveAttribute('aria-invalid', 'true');
      await page.keyboard.press('Escape');
      await expect(snooze).toBeFocused();
      await page.locator('#action-center-status-filter').selectOption('planned');
      await expect(page.locator('.action-center-row')).toHaveCount(1);
      await expect(
        page.getByRole('link', { name: 'View in Backlog', exact: true })
      ).toHaveAttribute(
        'href',
        '/workspaces/fictional-review-workspace?panel=backlog&task=fictional-ticket'
      );
      expect(writes).toEqual([]);
      expect(unknown).toEqual([]);
      expect(errors).toEqual([]);
    });

  test(`Action Center failed refresh, retry, retained data and scoped empty recovery: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    let state = 'error';
    const unknown = await actionReads(page, () => state);
    await page.goto('/action-center?workspace=fictional-workspace');
    await expect(page.getByRole('heading', { name: 'Unable to load findings' })).toBeVisible();
    await expect(page.locator('#action-center-empty')).toBeHidden();
    await capture(page, info, 'action-center-error');
    state = 'populated';
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page.locator('.action-center-row')).toHaveCount(2);
    await expect(page.locator('#action-center-refresh')).toBeFocused();
    await expect(page.locator('#action-center-filter-banner')).toContainText(
      actionCenterScene.items[0].workspace_name
    );
    state = 'error';
    await page.locator('#action-center-status-filter').selectOption('resolved');
    await expect(page.locator('#action-center-retained')).toContainText('last-loaded findings');
    await expect(page.locator('.action-center-row')).toHaveCount(2);
    state = 'empty';
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'No matching findings' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Clear filters', exact: true })).toHaveAttribute(
      'href',
      '/action-center?status=all'
    );
    await capture(page, info, 'action-center-empty');
    expect(writes).toEqual([]);
    expect(unknown).toEqual([]);
  });

  test(`Action Center real owned seen and resolve persistence, filtering and focus: ${theme}`, async ({
    page,
    request
  }, info) => {
    expect(info.config.workers).toBe(1);
    const sandbox = await assertSandbox(request);
    const database = realpathSync(join(sandbox, 'sessions.db'));
    expect(dirname(database)).toBe(sandbox);
    const workspace = await createWorkspace(request);
    cleanups.set(info.testId, async client => {
      if (!page.isClosed()) await page.close();
      await removeOwnedWorkspace(client, workspace);
    });
    const firstID = randomUUID(),
      secondID = randomUUID(),
      now = new Date().toISOString();
    const finding = {
      id: firstID,
      workspace_id: workspace.id,
      title: `UI finding ${firstID}`,
      summary: 'Fictional owned finding; no model or routine was run.',
      status: 'new',
      priority: 'high',
      created_at: now,
      updated_at: now
    };
    const second = { ...finding, id: secondID, title: `Second UI finding ${secondID}` };
    // No finding-create endpoint exists. Seed only this newly created workspace
    // after proving its existing opportunities field is empty, as the existing
    // action-center-ux real-endpoint contract does. Never touch another row.
    execFileSync('python3', [
      '-c',
      'import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); old=db.execute("SELECT opportunities_json FROM workspaces WHERE id=?",(sys.argv[2],)).fetchone(); assert old is not None and old[0] in (None,"","[]"); c=db.execute("UPDATE workspaces SET opportunities_json=? WHERE id=?",(sys.argv[3],sys.argv[2])); assert c.rowcount==1; db.commit()',
      database,
      workspace.id,
      JSON.stringify([finding, second])
    ]);
    const stored = () =>
      JSON.parse(
        execFileSync(
          'python3',
          [
            '-c',
            'import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); print(db.execute("SELECT opportunities_json FROM workspaces WHERE id=?",(sys.argv[2],)).fetchone()[0])',
            database,
            workspace.id
          ],
          { encoding: 'utf8' }
        )
      );
    await preparePage(page, theme, 390);
    const unexpected: string[] = [];
    const allowed: string[] = [];
    const seenPath = `/api/action-center/opportunities/${workspace.id}/${firstID}`;
    // This GET has a declared mutation side effect; it is never a layout read.
    // Its genuine persistence is verified independently of the response lifetime.
    let nativeActivation = false;
    const transport: string[] = [];
    page.on('requestfailed', req => {
      if (new URL(req.url()).pathname === seenPath)
        transport.push(req.failure()?.errorText || 'failed');
    });
    page.on('response', response => {
      if (new URL(response.url()).pathname === seenPath)
        transport.push(`status ${response.status()}`);
    });
    page.on('request', req => {
      const url = new URL(req.url());
      if (
        nativeActivation &&
        url.pathname.startsWith('/api/') &&
        !['GET', 'HEAD'].includes(req.method()) &&
        url.pathname !== '/api/orchestration/workspace/activate'
      )
        unexpected.push(`Native activation: ${req.method()} ${url.pathname}`);
      if (url.pathname !== seenPath) return;
      expect(req.method()).toBe('GET');
      expect(url.search).toBe('');
      expect(req.postData()).toBeNull();
      allowed.push(`GET ${seenPath}`);
    });
    const guardMutations = () =>
      page.route(
        url => url.pathname.startsWith('/api/'),
        async route => {
          const req = route.request();
          const url = new URL(req.url());
          if (req.method() === 'GET') {
            if (
              !url.pathname.startsWith('/api/action-center/') ||
              ['/api/action-center/library', '/api/action-center/opportunities'].includes(
                url.pathname
              )
            )
              return route.continue();
          } else if (
            req.method() === 'POST' &&
            url.pathname === '/api/orchestration/workspace/activate'
          ) {
            return route.continue(); // Existing watcher registration, as in guardReadTour.
          } else if (
            req.method() === 'POST' &&
            [firstID, secondID].some(
              id =>
                url.pathname === `/api/action-center/opportunities/${workspace.id}/${id}/resolve`
            ) &&
            !url.search
          ) {
            expect(req.postData()).toBeNull();
            allowed.push(`POST ${url.pathname}`);
            return route.continue();
          }
          unexpected.push(`${req.method()} ${url.pathname}`);
          return route.abort('blockedbyclient');
        }
      );
    await guardMutations();
    const scoped = `/action-center?workspace=${workspace.id}`;
    await page.goto(scoped);
    const title = page.getByRole('link', { name: finding.title, exact: true });
    await expect(title).toBeVisible();
    const href = await title.getAttribute('href');
    expect(href).toBe(`/workspaces/${workspace.folder_slug}`);
    // Even unmatched routes enable WebKit protocol interception globally.
    // Temporarily run only this reviewed native link activation without it;
    // the sole intended side effect is the exact owned seen GET above.
    await page.unrouteAll({ behavior: 'wait' });
    nativeActivation = true;
    await title.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp(href! + '$'));
    try {
      await expect
        .poll(() => stored().find((item: any) => item.id === firstID).seen_at)
        .toBeTruthy();
    } finally {
      nativeActivation = false;
      await info.attach('owned-seen-transport', {
        body: JSON.stringify({ allowed, transport }),
        contentType: 'application/json'
      });
    }
    await preparePage(page, theme, 390);
    await guardMutations();
    await page.goto(scoped);
    const firstRow = page.locator(`.action-center-row[data-id="${firstID}"]`);
    await expect(firstRow).not.toHaveAttribute('data-unread', 'true');
    await firstRow.getByRole('button', { name: 'Resolve', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('link', { name: second.title, exact: true })).toBeFocused();
    expect(stored().find((item: any) => item.id === firstID).status).toBe('resolved');
    await page.locator('#action-center-status-filter').selectOption('resolved');
    await expect(title).toBeVisible();
    await page.locator('#action-center-status-filter').selectOption('');
    await expect(page.getByRole('link', { name: second.title, exact: true })).toBeVisible();
    await expect(title).toHaveCount(0);
    const resolve = page
      .locator(`.action-center-row[data-id="${secondID}"]`)
      .getByRole('button', { name: 'Resolve', exact: true });
    await resolve.focus();
    await page.keyboard.press('Enter');
    await expect(
      page.getByRole('heading', { name: 'No active findings in this workspace' })
    ).toBeVisible();
    await expect(page.locator('#action-center-status')).toBeFocused();
    expect(stored().map((item: any) => item.status)).toEqual(['resolved', 'resolved']);
    const persisted = await request.get(
      `/api/action-center/opportunities?workspace=${workspace.id}&status=all`
    );
    expect(persisted.ok()).toBeTruthy();
    expect((await persisted.json()).items.every((item: any) => item.status === 'resolved')).toBe(
      true
    );
    await capture(page, info, 'action-center-persisted', {
      journey:
        'real owned workspace, synthetic database seed, genuine seen GET and two resolves; no routines/provider/backlog creation'
    });
    expect(allowed).toEqual([
      `GET /api/action-center/opportunities/${workspace.id}/${firstID}`,
      `POST /api/action-center/opportunities/${workspace.id}/${firstID}/resolve`,
      `POST /api/action-center/opportunities/${workspace.id}/${secondID}/resolve`
    ]);
    expect(unexpected).toEqual([]);
  });
}
