import { expect as baseExpect, type APIRequestContext, type Page } from '@playwright/test';
import path from 'node:path';
import { promises as fs } from 'node:fs';
import { createHash } from 'node:crypto';
const expect = baseExpect.configure({ timeout: 60_000 });

// Fingerprint synthetic source bytes as well as file/directory membership.
// Never follow links out of the explicit fixture tree.
export async function musicSourceSnapshot(root: string) {
  const files: Record<string, string> = {};
  async function walk(directory: string, prefix = '') {
    for (const entry of await fs.readdir(directory, { withFileTypes: true })) {
      const relative = `${prefix}${entry.name}`;
      if (entry.isDirectory()) {
        files[`${relative}/`] = 'directory';
        await walk(path.join(directory, entry.name), `${relative}/`);
      } else if (entry.isFile()) {
        files[relative] = createHash('sha256')
          .update(await fs.readFile(path.join(directory, entry.name)))
          .digest('hex');
      } else throw new Error(`Unexpected non-file fixture entry: ${relative}`);
    }
  }
  await walk(root);
  return files;
}

export async function musicHomeSnapshot(request: APIRequestContext) {
  const body = await (await request.get('/api/workspaces')).json();
  const agents = await (await request.get('/api/agents')).json();
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  return {
    workspaces: body.workspaces.sort((a: any, b: any) => a.id.localeCompare(b.id)),
    agents: agents.agents.sort((a: any, b: any) => a.name.localeCompare(b.name)),
    positions: layout.layout.positions || {}
  };
}

export async function musicHomeResourceCounts(request: APIRequestContext, homeID: string) {
  const state = await musicHomeSnapshot(request);
  const base = `/api/workspaces/${homeID}/assistant-program/library`;
  const roots = await (await request.get(`${base}/roots`)).json();
  const projects = await (await request.get(`${base}/projects`)).json();
  return {
    workspaces: state.workspaces.map((w: any) => [w.id, w.kind, w.parent_id]),
    agents: state.agents.map((a: any) => a.name),
    roots: (roots.roots || []).map((root: any) => root.id).sort(),
    projects: projects.rows.map((row: any) => [row.id, row.connection]).sort()
  };
}

export async function openMusicHomeReview(page: Page, song: string, tree = false) {
  await page.goto(`/?panel=today&folder=show${tree ? '&view=tree' : ''}`);
  await page.locator('.folder-path-entry__input:visible').first().fill(song);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  const existing = page.getByRole('button', {
    name: `Open ${path.basename(song)} review`,
    exact: true
  });
  const first = page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'Review setup', exact: true });
  await expect(first.or(existing)).toBeVisible();
  if (await existing.isVisible()) await existing.click();
  await first.click();
  const dialog = page.locator('#addFolderModal[data-source-setup-owner]');
  await expect(dialog).toBeVisible();
  const review = dialog.locator('[data-source-creation-review]:visible');
  await expect(review).toBeVisible();
  return review;
}
