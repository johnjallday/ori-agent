import { expect, type APIRequestContext } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

type BrowserFixture = {
  home_id: string;
  home_folder: string;
  child_folders: string[];
  source_root: string;
  entry_count: number;
};

export async function libraryJSON(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// Guarded test-only export, then the same real import API used for portable
// workspaces. No route-fulfilled library responses, paid models or installs.
export async function seedProjectBrowser(request: APIRequestContext, sandbox: string) {
  expect(sandbox, 'use e2e-fresh --sandbox-env ORI_LIBRARY_BROWSER_SANDBOX').toBeTruthy();
  execFileSync(
    'go',
    [
      'test',
      '-short',
      './internal/projectlibrary',
      '-run',
      '^TestBrowserFixtureExport$',
      '-count=1'
    ],
    {
      env: { ...process.env, ORI_LIBRARY_BROWSER_SANDBOX: sandbox },
      timeout: 120_000,
      stdio: 'pipe'
    }
  );
  const fixture: BrowserFixture = JSON.parse(
    readFileSync(join(sandbox, 'project-library-browser-fixture', 'fixture.json'), 'utf8')
  );
  await libraryJSON(await request.post('/api/onboarding/skip'));
  for (const path of [...fixture.child_folders, fixture.home_folder]) {
    await libraryJSON(await request.post('/api/workspaces/import', { data: { path } }));
  }
  const api = `/api/workspaces/${fixture.home_id}/assistant-program/library`;
  const projects = await libraryJSON(await request.get(`${api}/projects`));
  expect(projects.total).toBe(fixture.entry_count);
  expect(projects.rows).toHaveLength(25);
  const workspace = await libraryJSON(await request.get(`/api/workspaces/${fixture.home_id}`));
  const slug = workspace.folder_slug;
  expect(slug, 'use the real slug, not an ID URL').toBeTruthy();
  return { ...fixture, api, route: `/workspaces/${slug}/assistant#projectLibraryPanel` };
}

export function sourceHashes(root: string): Record<string, string> {
  const hashes: Record<string, string> = {};
  const visit = (relative: string) => {
    for (const entry of readdirSync(join(root, relative), { withFileTypes: true })) {
      const path = join(relative, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (entry.isFile())
        hashes[path] = createHash('sha256')
          .update(readFileSync(join(root, path)))
          .digest('hex');
      // Never follow a fixture symlink into a non-fixture source.
    }
  };
  visit('');
  return hashes;
}
