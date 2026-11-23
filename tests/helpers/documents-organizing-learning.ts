import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, readFile, realpath, rename, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './offline-cdn';

// Revisit only a disposable, already-hired examples sandbox owned by this
// worktree. Moving files here simulates a Finder/sync placement; it never
// invokes Filing or turns on monitoring.
export async function verifyOrganizingLearning(
  page: Page,
  request: APIRequestContext,
  sandbox: string
) {
  const worktree = await realpath('.');
  expect((await readFile(join(sandbox, '.ori-demo-owner'), 'utf8')).trim()).toBe(
    `worktree=${worktree}`
  );
  const root = await realpath(join(sandbox, 'Documents'));
  expect(
    root.startsWith(`${worktree}/test-results/`) ||
      root.startsWith(`${worktree}/bin/doc-review-spike/`)
  ).toBe(true);
  await installLocalCdn(page);
  const list = (await (await request.get('/api/personal-assistant/folder-agent')).json())
    .folder_agents;
  const view = list.find((v: { folder_name: string }) => v.folder_name === 'Documents');
  expect(view).toBeTruthy();
  await page.goto(view.route);
  const report = page.locator('.folder-agent-card--report:visible');
  const open = page.getByRole('button', { name: 'Open in chat', exact: true });
  await expect(report.or(open).first()).toBeVisible();
  if (!(await report.isVisible())) {
    const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
    if (await close.isVisible()) await close.click();
    await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  }
  const scope = report.locator('.folder-agent-card__organization');
  await scope.getByRole('button', { name: 'Read documents and suggest groups' }).click();
  await expect(scope).toContainText('Saved view');
  const renameField = scope.getByRole('textbox', { name: 'Group name' }).first();
  await renameField.fill('Renovation cabinet orders');
  await scope.getByRole('button', { name: 'Rename group' }).first().click();
  await expect(scope.getByRole('heading', { name: 'Renovation cabinet orders' })).toBeVisible();
  const firstHistory = scope
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: 'Organizing history' }) });
  await firstHistory.locator('summary').filter({ hasText: 'Organizing history' }).first().click();
  await expect(scope.getByText(/You chose Renovation cabinet orders/)).toBeVisible();
  const evidence = resolve('tasks/evidence-documents-organization-review');
  await scope.screenshot({ path: join(evidence, 'corrected-view.png') });
  for (const property of ['Maple Street', 'Oak Avenue'])
    await mkdir(join(root, property, 'Invoices'), { recursive: true });
  async function moveInvoice(file: string, property: string) {
    const from = join(root, file);
    const to = join(root, property, 'Invoices');
    if (process.env.ORI_DOCUMENT_REVIEW_FINDER === '1') {
      expect(process.platform).toBe('darwin');
      await promisify(execFile)(
        '/usr/bin/osascript',
        [join(worktree, 'tests/helpers/documents-review-move.applescript'), from, to],
        { timeout: 10_000 }
      );
    } else await rename(from, join(to, file));
    expect(await readFile(join(to, file), 'utf8')).toContain(property);
  }
  async function invoice(file: string, property: string) {
    await writeFile(join(root, file), `Invoice for ${property}. One maintenance visit.`, {
      flag: 'wx',
      mode: 0o600
    });
  }
  async function batch() {
    await scope.getByRole('button', { name: 'Preview another batch' }).click();
    for (const property of ['Maple Street', 'Oak Avenue']) {
      await scope.getByRole('checkbox', { name: property, exact: true }).check();
      await expect(
        scope.getByRole('checkbox', { name: `${property}/example.txt`, exact: true })
      ).toBeVisible();
    }
    await scope.getByRole('button', { name: 'Approve this read' }).click();
    await expect(scope).toContainText('Organizing reviews', { timeout: 70_000 });
  }
  async function confirmPlacement(prop: string) {
    const latest = scope.locator(':scope > div > details').first();
    await latest.locator('summary').filter({ hasText: 'Organizing history' }).click();
    await expect(latest).toContainText(`→ ${prop}/Invoices`);
    await latest.getByRole('button', { name: 'I placed this intentionally' }).click();
    await expect(scope).toContainText('confirmed as your placement');
  }
  await invoice('one.txt', 'Maple Street');
  await batch();
  await moveInvoice('one.txt', 'Maple Street');
  await invoice('two.txt', 'Oak Avenue');
  await batch();
  await confirmPlacement('Maple Street');
  await moveInvoice('two.txt', 'Oak Avenue');
  await invoice('three.txt', 'Pine Street');
  await batch();
  await confirmPlacement('Oak Avenue');
  await expect(scope).toContainText('Pine Street/Invoices');
  const latest = scope.locator(':scope > div > details').first();
  await latest.getByText('Show those decisions', { exact: true }).click();
  await expect(latest.getByText(/one.txt → Maple Street/).first()).toBeVisible();
  await expect(latest.getByText(/two.txt → Oak Avenue/).first()).toBeVisible();
  await latest.screenshot({ path: join(evidence, 'supporting-decisions.png') });
  await latest.getByText('Organizing history', { exact: true }).click();
  await latest.getByRole('button', { name: 'This was an exception' }).click();
  await expect(scope).not.toContainText('Suggested for three.txt:');
  await scope.getByRole('button', { name: 'Preview another batch' }).click();
  await scope.getByText('Remember for Documents', { exact: true }).first().click();
  await scope
    .getByRole('textbox', { name: 'Organizing convention in your words' })
    .fill('Keep property invoices under each property’s Invoices folder.');
  await scope.getByRole('button', { name: 'Remember for Documents' }).click();
  await expect(scope).toContainText(
    'Organizing convention shared with this batch: Keep property invoices'
  );
  await scope.getByText('Remember for Documents', { exact: true }).first().click();
  await scope.getByRole('button', { name: 'Forget convention' }).click();
  await expect(scope).not.toContainText('Organizing convention shared with this batch:');
}
