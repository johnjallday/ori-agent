import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, realpathSync } from 'node:fs';
import { join, dirname, resolve, relative } from 'node:path';
import { createHash } from 'node:crypto';
import { resourceFileInventory, resourcePreservationSnapshot } from './ui-consistency-resource.ts';

const hash = (bytes: string) => createHash('sha256').update(bytes).digest('hex');
test('resource snapshots permit only verified publication metadata, not changed coverage or broken pointers', () => {
  const parent = resolve('.tmp/ui-consistency');
  mkdirSync(parent, { recursive: true, mode: 0o750 });
  assert.equal(realpathSync(parent), parent);
  const root = mkdtempSync(join(parent, 'resource-snapshot-unit-'));
  const expected: Record<string, string> = {};
  const put = (path: string, value: unknown) => {
    const bytes = JSON.stringify(value);
    let parent = dirname(path);
    while (parent !== '.') {
      expected[parent] = 'directory';
      parent = dirname(parent);
    }
    mkdirSync(dirname(join(root, path)), { recursive: true, mode: 0o750 });
    writeFileSync(join(root, path), bytes, { mode: 0o600 });
    expected[path] = hash(bytes);
    return hash(bytes);
  };
  const base = 'home/.ori/continuity';
  const manifest = {
    version: 1,
    generation: '00000000-0000-0000-0000-000000000001',
    workspace_id: 'owned-unit',
    checkpoint_at: '2026-11-17T00:00:00Z',
    source_revision: 1,
    source_fingerprint: 'unchanged-fingerprint',
    children: [] as string[],
    files: [],
    components: []
  };
  const publish = () => {
    const digest = put(`${base}/generations/${manifest.generation}.json`, manifest);
    put(`${base}/current.json`, { version: 1, generation: manifest.generation, digest });
  };
  try {
    assert.equal(relative(resolve('.tmp/ui-consistency'), root).startsWith('..'), false);
    put('home/workspace.json', { id: 'owned-unit' });
    publish();
    const before = resourcePreservationSnapshot(root);
    manifest.checkpoint_at = '2026-11-17T00:01:00Z';
    manifest.source_revision++;
    publish();
    assert.deepEqual(resourcePreservationSnapshot(root).comparable, before.comparable);
    manifest.children.push('unexpected-member');
    publish();
    assert.notDeepEqual(resourcePreservationSnapshot(root).comparable, before.comparable);
    put(`${base}/current.json`, { version: 1, generation: manifest.generation, digest: 'invalid' });
    assert.throws(() => resourcePreservationSnapshot(root));
  } finally {
    // Synthetic helper-only fixture, never an app workspace: verify complete
    // nonsymlink inventory and exact reviewed bytes before retiring anything.
    assert.deepEqual(resourceFileInventory(root), expected);
    rmSync(root, { recursive: true });
  }
});
