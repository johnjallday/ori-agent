import test from 'node:test';
import assert from 'node:assert/strict';
import { workspaceFolderFacts } from './workspace-folder-shape.js';
import {
  createMapFolderSelection,
  renderMapFolderDetails
} from './workspace-map-folder-details.js';
import { renderWorkspaceRailHTML, workspaceRailView } from './home-workspace-cockpit.js';
import { WorkspaceCommandView } from './workspace-command.js';

const own = '/Users/demo/relocated/studio';
const base = { id: 'w', shape: 'managed', home_directory: '/Users/demo', sidecar_path: own };
const response = workspace => ({ ok: true, json: async () => workspace });

test('A–D folder facts distinguish storage, exact anchor and primary reference', () => {
  const a = workspaceFolderFacts(base);
  assert.equal(a.locations[0].label, 'Ori workspace folder');
  assert.equal(a.locations[0].displayPath, '~/relocated/studio');
  const b = workspaceFolderFacts({
    ...base,
    shape: 'anchored',
    anchor: { directory_reference_id: 'anchor', path: '/work', missing: true }
  });
  assert.deepEqual(
    b.locations.map(row => row.label),
    ['Works in', 'Ori’s notes and settings']
  );
  assert.equal(b.locations[0].path, '/work');
  assert.equal(b.locations[0].missing, true);
  const c = workspaceFolderFacts({
    ...base,
    primary_directory_id: 'external',
    directory_references: [
      { id: 'own', path: own },
      { id: 'output', path: own + '/files' },
      { id: 'private', path: '/samples', purpose: 'sample_library' },
      { id: 'external', name: '<Reference>', path: '/reference', is_primary: true },
      { id: 'duplicate', path: '/reference/' }
    ]
  });
  assert.equal(c.locations[0].path, own);
  assert.deepEqual(
    c.linked.map(row => row.path),
    ['/reference']
  );
  const d = workspaceFolderFacts({
    ...base,
    shape: 'imported',
    directory_references: [{ id: 'own', path: own }]
  });
  assert.equal(d.locations.length, 1);
  assert.equal(d.locations[0].label, 'Imported folder');
  assert.equal(d.locations[0].note, 'Ori keeps its notes and settings here.');
  assert.equal(d.linked.length, 0);
});

test('missing and unavailable are different; no path inferred from a name or another reference', () => {
  for (const missing of [false, undefined, true]) {
    const facts = workspaceFolderFacts({
      shape: 'anchored',
      name: 'Never guess',
      shared_data: { anchor_directory_reference_id: 'absent' },
      directory_references: [{ id: 'other', path: '/wrong' }],
      anchor: { missing }
    });
    assert.equal(facts.locations[0].path, '');
    assert.equal(facts.locations[1].path, '');
    assert.equal(facts.locations[0].missing, missing === true);
  }
  for (const shape of [undefined, 'future']) assert.equal(workspaceFolderFacts({ shape }), null);
  assert.equal(workspaceFolderFacts({ ...base, sidecar_path: '' }).locations[0].path, '');
});

test('shared folder HTML escapes names/paths and Home places it in selected context', () => {
  const detail = {
    id: 'w',
    status: 'ready',
    workspace: { ...base, directory_references: [{ id: 'x', name: '<script>', path: '/a<&"' }] }
  };
  const html = renderMapFolderDetails(detail);
  assert.match(html, /Linked folders/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /\/a&lt;&amp;&quot;/);
  assert.doesNotMatch(html, /<script>/);
  const home = renderWorkspaceRailHTML({
    ...workspaceRailView({ id: 'w', name: 'Studio' }),
    folderDetails: detail
  });
  assert.match(home, /data-map-folder-details/);
  assert.match(home, /Ori workspace folder/);
  assert.match(
    renderMapFolderDetails({ id: 'w', status: 'unavailable' }),
    /Folder details unavailable/
  );
});

test('selection reads once, rejects late A after B and after A → B → A, and clears immediately', async () => {
  const reads = [];
  const selection = createMapFolderSelection({
    fetchImpl: url => new Promise(resolve => reads.push({ url, resolve }))
  });
  const firstA = selection.select('a');
  assert.equal(selection.select('a'), firstA);
  const b = selection.select('b');
  reads[0].resolve(response({ ...base, id: 'a' }));
  await firstA;
  assert.equal(selection.read().id, 'b');
  assert.equal(selection.read().workspace, null);
  reads[1].resolve(response({ ...base, id: 'b' }));
  await b;
  await selection.select('b');
  assert.equal(reads.length, 2);
  const oldA = selection.select('a');
  const oldB = selection.select('b');
  const newA = selection.select('a');
  reads[2].resolve(response({ ...base, id: 'a', sidecar_path: '/old' }));
  await oldA;
  assert.equal(selection.read().status, 'loading');
  reads[4].resolve(response({ ...base, id: 'a', sidecar_path: '/new' }));
  await newA;
  assert.equal(selection.read().workspace.sidecar_path, '/new');
  await selection.select('');
  reads[3].resolve({ ok: false });
  await oldB;
  assert.equal(selection.read().id, '');
  assert.equal(selection.read().workspace, null);
});

test('failed reads and identity mismatch stay usable; refresh gets current relink facts', async () => {
  let value = { ok: false };
  const selection = createMapFolderSelection({ fetchImpl: async () => value });
  await selection.select('w');
  assert.equal(selection.read().status, 'unavailable');
  value = response({ ...base, id: 'wrong' });
  await selection.refresh();
  assert.equal(selection.read().status, 'unavailable');
  value = response({ ...base, shape: 'anchored', anchor: { path: '/old', missing: true } });
  await selection.refresh();
  assert.match(renderMapFolderDetails(selection.read()), /Folder not found/);
  value = response({ ...base, shape: 'anchored', anchor: { path: '/new', missing: false } });
  await selection.refresh();
  assert.doesNotMatch(renderMapFolderDetails(selection.read()), /Folder not found/);
  assert.equal(workspaceFolderFacts(selection.read().workspace).locations[0].path, '/new');
});

test('ordinary group members get folder facts; owner changes and non-workspace selection clear them', async () => {
  const previousFetch = globalThis.fetch;
  globalThis.fetch = async () => response(base);
  const slot = { hidden: true, innerHTML: '', querySelector: () => null };
  const command = Object.create(WorkspaceCommandView.prototype);
  command.page = { workspaceId: 'g', workspace: { id: 'g', kind: 'group' } };
  command.container = {
    querySelector: selector => (selector === '[data-cmd-member-folders]' ? slot : null)
  };
  try {
    command.selectDetachmentMember('w', { id: 'w', name: '<Member>', parent_id: 'g' });
    await command.memberFolderSelection().select('w');
    assert.equal(slot.hidden, false);
    assert.match(slot.innerHTML, /&lt;Member&gt;/);
    assert.match(slot.innerHTML, /Ori workspace folder/);
    assert.match(slot.innerHTML, /Open Workspace/);
    command.selectDetachmentMember('', null);
    assert.equal(slot.hidden, true);
    assert.equal(command.memberFolderSelection().read().workspace, null);
    command.selectDetachmentMember('w', { id: 'w', parent_id: 'g' });
    command.page.workspaceId = 'other';
    command.memberFolderSelection();
    assert.equal(slot.hidden, true);
    assert.equal(command.memberFolderSelection().read().id, '');
  } finally {
    globalThis.fetch = previousFetch;
  }
});
