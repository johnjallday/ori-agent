import { expect, test } from '@playwright/test';
import {
  libraryJSON,
  seedProjectBrowser,
  sourceHashes
} from './helpers/project-library-browser-fixture';

// Real host, canonical SQLite/folder storage and source files. Only the two
// explicitly labelled late-response/error cases below control HTTP timing.
// ./scripts/e2e-fresh.sh --port 8948 --sandbox-env ORI_LIBRARY_BROWSER_SANDBOX tests/project-library-browser.spec.ts
const sandbox = process.env.ORI_LIBRARY_BROWSER_SANDBOX || '';
test.skip(!sandbox, 'requires an explicitly owned disposable browser sandbox');
test.setTimeout(180_000);
test.use({ actionTimeout: 15_000 });

test('105-project browser: lazy inspection, paging, workspace return and narrow keyboard path', async ({
  page,
  request
}) => {
  const fixture = await seedProjectBrowser(request, sandbox);
  const hashes = sourceHashes(fixture.source_root);
  const before = await libraryJSON(await request.get(`${fixture.api}/projects`));
  const posts: string[] = [],
    sourceRequests: string[] = [],
    reads: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (req.method() !== 'GET' && path.includes('/assistant-program/library')) posts.push(path);
    if (/\/directories\/|\/files$|\/preview$/.test(path)) sourceRequests.push(path);
    if (path.includes('/assistant-program/library')) reads.push(path);
  });
  const search = async (text: string) => {
    await page.locator('#projectLibrarySearch').fill(text);
    await page
      .locator('#projectLibrarySearchForm')
      .getByRole('button', { name: 'Find', exact: true })
      .click();
  };
  const inspect = (name: string) =>
    page.getByRole('button', { name: `Inspect ${name}`, exact: true });
  const pane = page.locator('#projectLibraryPane');
  const evidence = 'tasks/evidence-project-library-browser/02-browser';
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(fixture.route);
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(25);
  expect(
    reads.filter(path => /\/projects\/[^/]+\/(sessions|activation|files|preview)$/.test(path))
  ).toEqual([]);
  await search('Zebra');
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(1);
  await inspect('Zebra beyond page four').click();
  await expect(pane).toContainText('Zebra beyond page four');
  await expect(pane).toContainText('catalog only');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(reads.filter(path => /\/(activation|sessions)$/.test(path))).toEqual([]);
  await page
    .locator('#projectLibraryBrowser')
    .screenshot({ path: `${evidence}/late-page-pane.png` });
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(25);
  await page.locator('#projectLibraryMore').click();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(50);
  await inspect('Project 000').click();
  await expect(pane).toContainText('connected');
  await expect(pane.getByRole('link', { name: 'Open connected workspace' })).toBeVisible();
  await page.getByRole('tab', { name: 'Sessions', exact: true }).click();
  await expect(pane).toContainText('No saved goals yet');
  expect(reads.filter(path => /\/browser-000\/sessions$/.test(path))).toHaveLength(1);
  await page.reload();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(50);
  await expect(page.getByRole('tab', { name: 'Sessions', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  await expect(pane).toContainText('No saved goals yet');
  await page.getByRole('tab', { name: 'Overview', exact: true }).click();
  await pane.getByRole('link', { name: 'Open connected workspace' }).click();
  await page.waitForURL(/\/workspaces\/connected-fixture-0$/);
  await page.goBack();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(50);
  await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Project 000');
  await expect(page.getByRole('tab', { name: 'Overview', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  await page
    .locator('#projectLibraryBrowser')
    .screenshot({ path: `${evidence}/workspace-return.png` });

  // Explicitly route-controlled stale response. The underlying detail is a real
  // host response; only its delivery is delayed, never a fake authorization.
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route(`**${fixture.api}/projects/browser-001`, async route => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response }).catch(() => {}); // Abort may close the route.
  });
  await inspect('Project 001').click();
  await inspect('Project 002').click();
  await expect(pane).toContainText('catalog only');
  await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Project 002');
  release();
  await page.unroute(`**${fixture.api}/projects/browser-001`);
  await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Project 002');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#projectLibraryResults')).toBeHidden();
  await page.getByRole('button', { name: 'Back to projects', exact: true }).click();
  await expect(inspect('Project 002')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#projectLibraryPaneTitle')).toBeFocused();
  await page.getByRole('tab', { name: 'Overview', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('tab', { name: 'Files', exact: true })).toBeFocused();
  await expect(pane).toContainText('Discovery metadata is not permission');
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('tab', { name: 'Sessions', exact: true })).toBeFocused();
  await expect(pane).toContainText('No saved goals yet');
  await page
    .locator('#projectLibraryBrowser')
    .screenshot({ path: `${evidence}/narrow-sessions.png` });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Back to projects', exact: true }).click();
  await search('does-not-exist');
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(0);
  await expect(page.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  await page
    .locator('#projectLibraryBrowser')
    .screenshot({ path: `${evidence}/empty-results.png` });
  await search('Project 000');
  // Explicitly mocked unavailable detail, separate from the real-host journey.
  await page.route(`**${fixture.api}/projects/browser-000`, route =>
    route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Project unavailable' })
    })
  );
  await inspect('Project 000').click();
  await expect(pane).toContainText('Project unavailable');
  await expect(pane.getByRole('link')).toHaveCount(0);
  await page
    .locator('#projectLibraryBrowser')
    .screenshot({ path: `${evidence}/unavailable-detail.png` });
  await page.unroute(`**${fixture.api}/projects/browser-000`);
  expect(posts).toEqual([]);
  expect(sourceRequests).toEqual([]);
  expect(sourceHashes(fixture.source_root)).toEqual(hashes);
  const after = await libraryJSON(await request.get(`${fixture.api}/projects`));
  expect(after.revision).toBe(before.revision);
  expect(after.total).toBe(105);
});
