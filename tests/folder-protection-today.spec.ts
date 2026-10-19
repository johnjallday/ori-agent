import { test, expect, type Page } from '@playwright/test';
import { mkdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ensurePersonalHQ, openNeedsYou, skipOnboarding } from './helpers/folder-protection';

// Folder protection M1: adopting a project folder with no backup shows exactly
// one Today line; "Not now" hides it across reloads; it revives once when five
// adopted folders are uncovered; a second "Not now" is final. The workspace's
// Protection fact is state, not an offer, and stays throughout.
//
// Runs against a FRESH isolated real server (`wt demo`, `scripts/demo-server.sh`
// or `./scripts/e2e-fresh.sh`): it hires the assistant and records declines.
// No model and no backup provider are used. The folders are real directories
// in this run's temp dir, linked through the ordinary workspace APIs.
test.describe.serial('Folder protection: one Today line with a sticky Not now', () => {
  const root = mkdtempSync(join(tmpdir(), 'ori-folder-protection-'));
  const adopted: Array<{ id: string; name: string; folder_slug: string }> = [];

  // adoptFolder links a real directory as a workspace's primary project folder.
  async function adoptFolder(page: Page, name: string) {
    const folder = join(root, name);
    mkdirSync(folder, { recursive: true });
    const created = await page.request.post('/api/workspaces', { data: { name } });
    expect(created.status(), await created.text()).toBeLessThan(300);
    const ws = (await created.json()).folder as { id: string; name: string; folder_slug: string };
    const dir = await page.request.post(`/api/workspaces/${ws.id}/directories`, {
      data: { name, path: folder }
    });
    expect(dir.status(), await dir.text()).toBe(201);
    const linked = await page.request.patch(`/api/workspaces/${ws.id}`, {
      data: { primary_directory_id: (await dir.json()).directory.id }
    });
    expect(linked.status(), await linked.text()).toBeLessThan(300);
    adopted.push(ws);
    return ws;
  }

  async function coverageItems(page: Page) {
    const response = await page.request.get('/api/personal-assistant/today');
    expect(response.status(), await response.text()).toBe(200);
    const today = (await response.json()).today;
    const backups = (today.backups?.items || []).filter(item => item.kind === 'backup_coverage');
    const needs = (today.needs_you?.items || []).filter(item => item.kind === 'backup_coverage');
    expect(needs.length, 'Needs you and the backup section disagree').toBe(backups.length);
    expect(JSON.stringify(today)).not.toContain(root);
    return backups;
  }

  async function expectFact(page: Page, workspaceID: string, sentence: RegExp) {
    const response = await page.request.get(`/api/workspaces/${workspaceID}/protection`);
    expect(response.status()).toBe(200);
    const fact = (await response.json()).protection;
    expect(fact?.sentence).toMatch(sentence);
    expect(JSON.stringify(fact)).not.toContain(root);
  }

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('adopting one folder shows exactly one line; Not now hides it across reloads', async ({
    page
  }) => {
    await ensurePersonalHQ(page);
    expect(await coverageItems(page), 'a line before any folder was adopted').toHaveLength(0);
    const song = await adoptFolder(page, 'Night Drive');

    const [line] = await coverageItems(page);
    expect(line.title).toBe('1 project folder has no backup Ori can see');
    expect(line.detail).toBe('Night Drive');
    expect(line.attribution).toBe('Backup & Recovery');
    expect(line.route).toBe('/?protect=folders');

    await openNeedsYou(page);
    const row = page.locator('li.personal-assistant-today__coverage');
    await expect(row).toHaveCount(1);
    await expect(row).toBeVisible();
    await expect(row).toContainText('1 project folder has no backup Ori can see');
    await expect(row.getByRole('link', { name: 'Check protection' })).toHaveAttribute(
      'href',
      '/?protect=folders'
    );
    await row.getByRole('button', { name: 'Not now' }).click();
    await expect(row).toHaveCount(0);
    await openNeedsYou(page);
    await expect(page.locator('li.personal-assistant-today__coverage')).toHaveCount(0);
    expect(await coverageItems(page)).toHaveLength(0);
    // The fact is state, not an offer: no decline hides it.
    await expectFact(page, song.id, /^Not in any backup Ori observes$/);
  });

  test('five uncovered folders revive the line once; a second Not now is final', async ({
    page
  }) => {
    for (const name of ['Garden Essay', 'Album Notes', 'Field Recordings'])
      await adoptFolder(page, name);
    expect(await coverageItems(page), 'revived below portfolio scale').toHaveLength(0);
    await adoptFolder(page, 'Lecture Slides');

    const [line] = await coverageItems(page);
    expect(line.title).toBe('5 project folders have no backup Ori can see');
    expect(line.detail).toMatch(/\+2 more$/);
    const declined = await page.request.post(
      '/api/personal-assistant/today/backup-coverage/decline',
      { data: {} }
    );
    expect(declined.status(), await declined.text()).toBe(200);

    await adoptFolder(page, 'Sixth Project');
    expect(await coverageItems(page), 'a second decline was not final').toHaveLength(0);
    for (const ws of adopted) await expectFact(page, ws.id, /^Not in any backup Ori observes$/);
  });
});
