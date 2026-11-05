import { expect, test } from '@playwright/test';
import {
  libraryJSON,
  seedProjectBrowser,
  sourceHashes
} from './helpers/project-library-browser-fixture';

// Real host + real canonical storage + real filesystem. The initial baseline
// deliberately has no installed provider: historical metadata stays readable.
// ./scripts/e2e-fresh.sh --port 8948 --sandbox-env ORI_LIBRARY_BROWSER_SANDBOX tests/project-library-browser.spec.ts
const sandbox = process.env.ORI_LIBRARY_BROWSER_SANDBOX || '';
test.skip(!sandbox, 'requires an explicitly owned disposable browser sandbox');
test.setTimeout(180_000);
test.use({ actionTimeout: 15_000 });

test('baseline: 105 saved projects remain paged and inspection is inert', async ({
  page,
  request
}) => {
  const fixture = await seedProjectBrowser(request, sandbox);
  const hashes = sourceHashes(fixture.source_root);
  const before = await libraryJSON(await request.get(`${fixture.api}/projects`));
  const posts: string[] = [];
  const sourceRequests: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/assistant-program/library'))
      posts.push(req.url());
    if (/\/directories\/|\/files\?|\/preview\?/.test(req.url())) sourceRequests.push(req.url());
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(fixture.route);
  await expect(page.locator('#projectLibraryPanel')).toBeVisible();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(25);
  await page.locator('#projectLibrarySearch').fill('Zebra');
  await page
    .locator('#projectLibrarySearchForm')
    .evaluate(form => (form as HTMLFormElement).requestSubmit());
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(1);
  await expect(page.locator('#projectLibraryRows')).toContainText('Zebra beyond page four');
  await page
    .locator('#projectLibraryPanel')
    .screenshot({
      path: 'tasks/evidence-project-library-browser/01-baseline/late-page-search.png'
    });
  await page.locator('#projectLibrarySearch').fill('Project 000');
  await page
    .locator('#projectLibrarySearchForm')
    .evaluate(form => (form as HTMLFormElement).requestSubmit());
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(1);
  await page
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Project 000', exact: true })
    .click();
  await expect(page.getByRole('dialog')).toContainText('Connected');
  await page
    .getByRole('dialog')
    .screenshot({
      path: 'tasks/evidence-project-library-browser/01-baseline/connected-details.png'
    });
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click();
  await page.locator('#projectLibrarySearch').fill('Project 002');
  await page
    .locator('#projectLibrarySearchForm')
    .evaluate(form => (form as HTMLFormElement).requestSubmit());
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(1);
  await page
    .locator('#projectLibraryRows')
    .getByRole('button', { name: 'Review Project 002', exact: true })
    .click();
  await expect(page.getByRole('dialog')).toContainText('catalog only');
  await page
    .getByRole('dialog')
    .screenshot({
      path: 'tasks/evidence-project-library-browser/01-baseline/catalog-only-details.png'
    });
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click();
  await page.locator('#projectLibrarySearch').fill('');
  await page
    .locator('#projectLibrarySearchForm')
    .evaluate(form => (form as HTMLFormElement).requestSubmit());
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(25);
  await page.locator('#projectLibraryMore').click();
  await expect(page.locator('#projectLibraryRows tr')).toHaveCount(50);
  expect(posts).toEqual([]);
  expect(sourceRequests).toEqual([]);
  expect(sourceHashes(fixture.source_root)).toEqual(hashes);
  const after = await libraryJSON(await request.get(`${fixture.api}/projects`));
  expect(after.revision).toBe(before.revision);
  expect(after.total).toBe(105);
});
