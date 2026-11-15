import { expect as baseExpect, type APIRequestContext, type Page } from '@playwright/test';
import path from 'node:path';
import { promises as fs } from 'node:fs';
import { createHash } from 'node:crypto';
import {
  chooseRetainedHomeTeam,
  openRetainedMusicDialog,
  resumeRetainedMusicReview,
  returnToRetainedMusicReview,
  seedRetainedMusicReview
} from './retained-music-review';
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

export async function openMusicHomeReview(page: Page, song: string, tree = false, library = false) {
  // Placement acceptance exercises an existing retained owner. Add folder is
  // now the independently tested hire-first entry, not this source review.
  const reviewed = await seedRetainedMusicReview(page.request, song, library);
  await resumeRetainedMusicReview(page, reviewed.conversation.id, tree);
  await openRetainedMusicDialog(page);
  const settings = await (await page.request.get('/api/settings/system-model')).json();
  expect(settings.configured, 'the placement sandbox must select its fixture model').toBe(true);
  await chooseRetainedHomeTeam(page, { provider: settings.provider, model: settings.model });
  return returnToRetainedMusicReview(page);
}
