import { expect, test } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  libraryJSON,
  seedProjectBrowser,
  sourceHashes
} from './helpers/project-library-browser-fixture';

const sandbox = process.env.ORI_LIBRARY_BROWSER_SANDBOX || '';
test.skip(!sandbox, 'requires the explicitly owned disposable browser sandbox');
test.setTimeout(180_000);
test.use({ actionTimeout: 15_000 });

test('canonical collections, quick views and revision-checked contextual bulk edits', async ({
  page,
  request
}) => {
  const fixture = await seedProjectBrowser(request, sandbox, { writable: true });
  const hashes = sourceHashes(fixture.source_root);
  const before = await libraryJSON(await request.get(`${fixture.api}/projects`));
  const calls: { path: string; method: string }[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (path.includes('/assistant-program/library')) calls.push({ path, method: req.method() });
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(fixture.route);
  const rows = page.locator('#projectLibraryRows tr');
  const count = page.locator('#projectLibraryCount');
  const bulk = page.locator('#projectLibraryBulk');
  const view = (id: string) => page.locator(`[data-library-view="${id}"]`);
  const collection = async (name: string) => {
    const disclosure = page.locator('#projectLibraryCollections');
    if (!(await disclosure.evaluate(el => (el as HTMLDetailsElement).open)))
      await disclosure.locator('summary').click();
    await disclosure.getByRole('button', { name: `Browse projects: ${name}`, exact: true }).click();
  };
  const search = async (text: string) => {
    await page.locator('#projectLibrarySearch').fill(text);
    await page
      .locator('#projectLibrarySearchForm')
      .getByRole('button', { name: 'Find', exact: true })
      .click();
  };
  await expect(rows).toHaveCount(25);
  await expect(bulk).toBeHidden();
  await expect(page.locator('#projectLibraryQueueActions')).toBeHidden();
  await view('needs_attention').click();
  await expect(count).toHaveText('5 of 5 projects');
  await collection('Across the catalog');
  await expect(count).toHaveText('2 of 2 projects');
  await view('all').click();
  await expect(count).toHaveText('25 of 53 projects');
  await page.getByRole('button', { name: 'More projects', exact: true }).click();
  await expect(count).toHaveText('50 of 53 projects');
  await page.getByRole('button', { name: 'More projects', exact: true }).click();
  await expect(count).toHaveText('53 of 53 projects');
  await search('Zebra');
  await expect(
    page.getByRole('button', { name: 'Inspect Zebra beyond page four', exact: true })
  ).toBeVisible();
  await expect(count).toHaveText('1 of 1 projects');
  await search('');
  await collection('Empty collection');
  await expect(count).toHaveText('0 of 0 projects');
  await expect(rows).toHaveCount(0);
  await page.getByRole('button', { name: 'Leave collection', exact: true }).click();
  await expect(count).toHaveText('25 of 105 projects');
  await view('recently_saved').click();
  await expect(page.locator('#projectLibrarySort')).toBeDisabled();
  await expect(page.locator('#projectLibrarySort')).toHaveValue('last_saved');
  await expect(page.locator('#projectLibraryDirection')).toHaveValue('desc');
  await view('archived').click();
  await expect(count).toHaveText('10 of 10 projects');
  await view('all').click();
  await expect(count).toHaveText('25 of 105 projects');
  expect(calls.filter(call => call.method !== 'GET')).toEqual([]);
  expect(calls.filter(call => /\/(files|preview)$/.test(call.path))).toEqual([]);
  expect(sourceHashes(fixture.source_root)).toEqual(hashes);
  expect((await libraryJSON(await request.get(`${fixture.api}/projects`))).revision).toBe(
    before.revision
  );
  await page.locator('#projectLibrarySort').selectOption('name');
  await page.locator('#projectLibraryDirection').selectOption('asc');
  await search('');

  await page.getByRole('button', { name: 'Select loaded projects (25)', exact: true }).click();
  await expect(page.locator('#projectLibraryRows input:checked')).toHaveCount(25);
  await page.getByRole('button', { name: 'Clear selection', exact: true }).click();
  await expect(page.locator('#projectLibraryRows input:checked')).toHaveCount(0);
  await expect(bulk).toBeHidden();

  // Inspection and bulk choices are independent. Another real browser tab edits
  // one selected record; the original tab must send its old revision and skip it.
  await page.getByRole('checkbox', { name: 'Select Project 000', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Select Project 004', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Project 000', exact: true }).click();
  await expect(bulk).toBeVisible();
  await expect(
    page.getByRole('checkbox', { name: 'Select Project 000', exact: true })
  ).toBeChecked();
  const other = await page.context().newPage();
  await other.goto(fixture.route);
  await other.getByRole('checkbox', { name: 'Select Project 004', exact: true }).check();
  await other.locator('#projectLibraryBulkStage').selectOption('editing');
  await expect(other.locator('#projectLibraryBulkResultText')).toContainText(
    'Set stage to Editing'
  );
  await page.locator('#projectLibraryBulkStage').selectOption('mixing');
  await expect(page.locator('#projectLibraryBulkResultText')).toContainText(
    '1 skipped (changed elsewhere)'
  );
  await expect(bulk).toBeHidden();
  await expect(page.locator('#projectLibraryPane')).toContainText('mixing');
  await page
    .locator('#projectLibraryPanel')
    .screenshot({ path: 'tasks/evidence-project-library-browser/04-organize/concurrent-bulk.png' });
  await page.getByRole('button', { name: 'Undo', exact: true }).click();
  await expect(page.locator('#projectLibraryBulkResultText')).toContainText('restored 1 song');
  expect(
    (await libraryJSON(await request.get(`${fixture.api}/projects/browser-000`))).fields.stage
  ).toBe('writing');
  expect(
    (await libraryJSON(await request.get(`${fixture.api}/projects/browser-004`))).fields.stage
  ).toBe('editing');
  await other.close();

  await collection('Across the catalog');
  await expect(count).toHaveText('25 of 53 projects');
  await page.getByRole('checkbox', { name: 'Select Project 000', exact: true }).check();
  await page.getByRole('button', { name: 'More projects', exact: true }).click();
  await expect(count).toHaveText('50 of 53 projects');
  await expect(page.locator('#projectLibraryRows input:checked')).toHaveCount(1);
  await view('needs_attention').click();
  await expect(count).toHaveText('2 of 2 projects');
  await expect(page.locator('#projectLibraryRows input:checked')).toHaveCount(0);
  await expect(page.locator('#projectLibrarySelectionNotice')).toContainText('Selection cleared');
  await page.locator('#projectLibraryPanel').screenshot({
    path: 'tasks/evidence-project-library-browser/04-organize/collection-attention.png'
  });
  await page.locator('#projectLibrarySearchForm details > summary').click();
  await page.locator('#projectLibraryStatusFilter').selectOption('unknown');
  await search('');
  await expect(count).toHaveText('2 of 2 projects');
  await page.getByRole('checkbox', { name: 'Select Project 004', exact: true }).check();
  await page.getByRole('button', { name: 'Inspect Project 004', exact: true }).click();
  await page.locator('#projectLibraryBulkStatus').selectOption('archived');
  await expect(page.locator('#projectLibraryBulkResultText')).toContainText(
    'Set status to Archived'
  );
  await expect(count).toHaveText('1 of 1 projects');
  await expect(page.getByRole('button', { name: 'Inspect Project 004', exact: true })).toHaveCount(
    0
  );
  await expect(page.locator('#projectLibraryPaneTitle')).toHaveText('Select a project');
  await page.locator('#projectLibraryPanel').screenshot({
    path: 'tasks/evidence-project-library-browser/04-organize/filtered-row-removed.png'
  });
  await page.locator('#projectLibraryStatusFilter').selectOption('');
  await search('');
  await view('archived').click();
  await expect(
    page.getByRole('button', { name: 'Inspect Project 004', exact: true })
  ).toBeVisible();
  await page.locator('#projectLibraryPanel').screenshot({
    path: 'tasks/evidence-project-library-browser/04-organize/archived-metadata.png'
  });
  expect(sourceHashes(fixture.source_root)).toEqual(hashes);
  expect(calls.filter(call => /\/(files|preview)$/.test(call.path))).toEqual([]);
  const receipt = JSON.parse(
    readFileSync(join(sandbox, 'project-library-browser-fixture', 'provider-receipt.json'), 'utf8')
  );
  writeFileSync(
    'tasks/evidence-project-library-browser/04-organize/receipt.json',
    JSON.stringify(
      {
        provider: receipt,
        sourceFilesCompared: Object.keys(hashes).length,
        sourceHashesUnchanged: true,
        calls
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
