import { expect, type APIRequestContext } from '@playwright/test';
import {
  readFileSync,
  readdirSync,
  lstatSync,
  realpathSync,
  mkdirSync,
  writeFileSync
} from 'node:fs';
import { join, relative, dirname } from 'node:path';
import { createHash } from 'node:crypto';

export async function retainedResourceFixture(request: APIRequestContext) {
  expect(
    process.env.ORI_RESOURCE_UI_FIXTURE,
    'explicit retained repository-fixture opt-in required'
  ).toBe('1');
  const receipt = JSON.parse(
    readFileSync('.tmp/ui-consistency/group-5-catalog-shared-location.json', 'utf8')
  );
  const sandbox = receipt.sandbox;
  expect(process.env.ORI_UI_SANDBOX).toBe(sandbox);
  expect(realpathSync(sandbox)).toBe(sandbox);
  expect(relative(realpathSync('.tmp/ui-consistency'), sandbox).startsWith('..')).toBe(false);
  expect(lstatSync(sandbox).isSymbolicLink()).toBe(false);
  expect(readFileSync(join(sandbox, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync('.')}`
  );
  const inventory = JSON.parse(
    readFileSync('.tmp/ui-consistency/group-5-catalog-shared-retained-inventory.json', 'utf8')
  );
  expect(inventory.root).toBe(sandbox);
  const folders = [];
  for (const owned of inventory.workspaces) {
    expect(owned.id).toMatch(/^[0-9a-f-]{36}$/);
    const response = await request.get(`/api/workspaces/${owned.id}`);
    expect(response.ok()).toBe(true);
    const value = await response.json();
    expect(value.name).toBe(owned.name);
    folders.push(value);
  }
  const studios = folders.filter((folder: any) => folder.name === 'REAPER Studio');
  const homes = folders.filter((folder: any) => folder.name === 'Music Home');
  expect(studios).toHaveLength(1);
  expect(homes).toHaveLength(1);
  const studio = studios[0],
    home = homes[0];
  expect(studio.parent_id).toBe(home.id);
  const view = await request.get(`/api/workspaces/${studio.id}/resource-catalog`);
  expect(view.ok()).toBe(true);
  const catalog = await view.json();
  expect(catalog.name).toBe('REAPER Studio');
  expect(catalog.attached).toBe(true);
  expect(catalog.provider.available).toBe(true);
  expect(catalog.categories.map((category: any) => category.id)).toEqual([
    'templates',
    'scripts',
    'audio_plugins'
  ]);
  // This existing fixture intentionally used the pre-confirmation staging
  // directory. Read only its proven sidecar tree; never confirm/move a root as
  // a side effect of an inspection tour or weaken the writable-seed guard.
  expect(lstatSync(home.sidecar_path).isSymbolicLink()).toBe(false);
  expect(lstatSync(studio.sidecar_path).isSymbolicLink()).toBe(false);
  expect(realpathSync(home.sidecar_path)).toBe(home.sidecar_path);
  expect(realpathSync(studio.sidecar_path)).toBe(studio.sidecar_path);
  const workspaceRoot = dirname(realpathSync(home.sidecar_path));
  expect(relative(sandbox, workspaceRoot).startsWith('..')).toBe(false);
  expect(relative(workspaceRoot, studio.sidecar_path).startsWith('..')).toBe(false);
  return { sandbox, workspaceRoot, studio, home };
}

export function resourceFileInventory(root: string): Record<string, string> {
  expect(lstatSync(root).isSymbolicLink()).toBe(false);
  expect(realpathSync(root)).toBe(root);
  const files: Record<string, string> = {};
  const walk = (path: string) => {
    for (const name of readdirSync(path).sort()) {
      const file = join(path, name),
        stat = lstatSync(file);
      expect(stat.isSymbolicLink(), 'preserve unexpected resource-fixture links').toBe(false);
      if (stat.isDirectory()) {
        files[relative(root, file)] = 'directory';
        walk(file);
      } else {
        expect(stat.isFile()).toBe(true);
        files[relative(root, file)] = createHash('sha256').update(readFileSync(file)).digest('hex');
      }
    }
  };
  walk(root);
  return files;
}

export function resourcePreservationSnapshot(root: string) {
  const raw = resourceFileInventory(root);
  const comparable = { ...raw };
  const publications: Record<string, unknown> = {};
  for (const path of Object.keys(raw).filter(path =>
    path.endsWith('/.ori/continuity/current.json')
  )) {
    const base = path.slice(0, -'/current.json'.length);
    const pointer = JSON.parse(readFileSync(join(root, path), 'utf8'));
    expect(Object.keys(pointer).sort()).toEqual(['digest', 'generation', 'version']);
    expect(pointer.version).toBe(1);
    expect(pointer.generation).toMatch(/^[0-9a-f-]{36}$/);
    const generation = `${base}/generations/${pointer.generation}.json`;
    const bytes = readFileSync(join(root, generation));
    const digest = createHash('sha256').update(bytes).digest('hex');
    expect(digest).toBe(pointer.digest);
    expect(raw[generation]).toBe(digest);
    expect(raw[path]).toBe(
      createHash('sha256')
        .update(readFileSync(join(root, path)))
        .digest('hex')
    );
    const manifest = JSON.parse(bytes.toString('utf8'));
    expect(Object.keys(manifest).sort()).toEqual(
      [
        'version',
        'generation',
        'workspace_id',
        'checkpoint_at',
        'source_revision',
        'source_fingerprint',
        'children',
        'files',
        'components'
      ].sort()
    );
    expect(manifest.version).toBe(1);
    expect(manifest.generation).toBe(pointer.generation);
    expect(Number.isFinite(Date.parse(manifest.checkpoint_at))).toBe(true);
    expect(Number.isInteger(manifest.source_revision) && manifest.source_revision >= 0).toBe(true);
    const definition = JSON.parse(
      readFileSync(join(root, base.slice(0, -'/.ori/continuity'.length), 'workspace.json'), 'utf8')
    );
    expect(manifest.workspace_id).toBe(definition.id);
    publications[base] = { pointer, manifest };
    // Only verified publication identity/time/sequence may rotate. Component
    // digests, fingerprint, coverage, all object bytes, definitions and every
    // other path remain exact. Full unnormalized inventories are journaled.
    const {
      generation: _generation,
      checkpoint_at: _time,
      source_revision: _revision,
      ...semantic
    } = manifest;
    delete comparable[path];
    delete comparable[generation];
    comparable[`${base}/[verified-publication]`] = JSON.stringify(semantic);
  }
  return { raw, comparable, publications };
}

export function journalResourceRead(
  info: { testId: string; title: string; project: { name: string } },
  phase: 'before' | 'after',
  snapshot: unknown
) {
  const font = process.env.ORI_UI_FONTS === 'real' ? 'real' : 'fallback';
  const id = createHash('sha256')
    .update(`${font}:${info.project.name}:${info.testId}`)
    .digest('hex')
    .slice(0, 20);
  const directory = '.tmp/ui-consistency/group-5-catalog-read-journals';
  mkdirSync(directory, { recursive: true, mode: 0o750 });
  writeFileSync(
    join(directory, `${id}-${phase}.json`),
    JSON.stringify({ title: info.title, engine: info.project.name, font, snapshot }, null, 2),
    { mode: 0o600 }
  );
}

export async function resourceApiSnapshot(
  request: APIRequestContext,
  fixture: Awaited<ReturnType<typeof retainedResourceFixture>>
) {
  const paths = [
    `/api/workspaces/${fixture.home.id}`,
    `/api/workspaces/${fixture.studio.id}`,
    `/api/workspaces/${fixture.studio.id}/resource-catalog`,
    ...['templates', 'scripts', 'audio_plugins'].map(
      category =>
        `/api/workspaces/${fixture.studio.id}/resource-catalog/entries?category=${category}`
    )
  ];
  const records: Record<string, unknown> = {};
  for (const path of paths) {
    const response = await request.get(path);
    expect(response.ok()).toBe(true);
    records[path] = await response.json();
  }
  return records;
}
