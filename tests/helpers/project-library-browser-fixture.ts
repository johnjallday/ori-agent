import { expect, type APIRequestContext } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

type BrowserFixture = {
  provider_source: string;
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
// workspaces. No route-fulfilled library responses or paid models. Writable
// acceptance optionally installs an inert generic fixture ONLY in this sandbox.
export async function seedProjectBrowser(
  request: APIRequestContext,
  sandbox: string,
  { writable = false } = {}
) {
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
  if (writable) {
    expect((await request.get(`/api/workspaces/${fixture.home_id}`)).status()).toBe(404);
    const source = fixture.provider_source;
    expect(source).toBe(join(sandbox, 'project-library-browser-fixture', 'provider'));
    await libraryJSON(await request.post('/api/plugins/install', { data: { source } }));
    await libraryJSON(
      await request.post('/api/plugins/install', { data: { source, confirm: true } })
    );
    await libraryJSON(await request.post('/api/plugins/browser-fixture/enable', { data: {} }));
    execFileSync(
      'go',
      [
        'test',
        '-short',
        './internal/projectlibrary',
        '-run',
        '^TestBrowserFixtureBindProvider$',
        '-count=1'
      ],
      {
        env: {
          ...process.env,
          ORI_LIBRARY_BROWSER_SANDBOX: sandbox,
          ORI_LIBRARY_BROWSER_BIND_PROVIDER: '1'
        },
        timeout: 120_000,
        stdio: 'pipe'
      }
    );
  }
  for (const path of [...fixture.child_folders, fixture.home_folder]) {
    await libraryJSON(await request.post('/api/workspaces/import', { data: { path } }));
  }
  const api = `/api/workspaces/${fixture.home_id}/assistant-program/library`;
  const projects = await libraryJSON(await request.get(`${api}/projects`));
  expect(projects.total).toBe(fixture.entry_count);
  expect(projects.rows).toHaveLength(25);
  expect(projects.provider_read_only).toBe(!writable);
  const workspace = await libraryJSON(await request.get(`/api/workspaces/${fixture.home_id}`));
  const slug = workspace.folder_slug;
  expect(slug, 'use the real slug, not an ID URL').toBeTruthy();
  return { ...fixture, api, route: `/workspaces/${slug}/assistant#projectLibraryPanel` };
}

// Keep complete authority snapshots in memory, not browser storage or evidence
// files (directory references contain absolute paths). Compare before/after.
export async function browserAuthoritySnapshot(request: APIRequestContext, api: string) {
  const catalog = await libraryJSON(await request.get(`${api}/projects`));
  const roots = await libraryJSON(await request.get(`${api}/roots`));
  const { folders } = await libraryJSON(await request.get('/api/workspaces'));
  const { agents } = await libraryJSON(await request.get('/api/agents'));
  const workspaces = [];
  for (const { id } of folders.sort((a: { id: string }, b: { id: string }) =>
    a.id.localeCompare(b.id)
  )) {
    const workspace = await libraryJSON(await request.get(`/api/workspaces/${id}`));
    workspaces.push({
      id,
      agents: workspace.agent_instances || [],
      directories: workspace.directory_references || []
    });
  }
  return {
    revision: catalog.revision,
    entries: catalog.total,
    roots,
    workspaces,
    agentNames: agents.map((agent: { name: string }) => agent.name).sort()
  };
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
