import test from 'node:test';
import assert from 'node:assert/strict';
import {
  anchorFolder,
  shortenWorkspacePath,
  filesystemBindingSubtitle,
  anchoredDeletionCopy
} from './workspace-folder-shape.js';

const workspace = {
  shape: 'anchored',
  home_directory: '/Users/example',
  sidecar_path: '/Users/example/Ori Workspaces/client',
  shared_data: { anchor_directory_reference_id: 'anchor' },
  directory_references: [{ id: 'anchor', path: '/Users/example/Client', missing: true }]
};

test('shape display uses exact home boundaries without changing canonical paths', () => {
  assert.equal(shortenWorkspacePath(workspace.directory_references[0].path, workspace), '~/Client');
  assert.equal(
    shortenWorkspacePath('/Users/example-other/Client', workspace),
    '/Users/example-other/Client'
  );
  assert.equal(workspace.directory_references[0].path, '/Users/example/Client');
  assert.deepEqual(anchorFolder(workspace), {
    id: 'anchor',
    path: '/Users/example/Client',
    missing: true
  });
  assert.equal(anchorFolder({ directory_references: workspace.directory_references }), null);
});

test('filesystem labels distinguish roots and never call unrestricted or unknown tools read-only', () => {
  const item = {
    name: 'filesystem',
    alias: 'anchor-folder',
    roots: ['/Users/example/Client'],
    allowedTools: ['list_directory', 'read_text_file']
  };
  assert.equal(filesystemBindingSubtitle(item, workspace), 'anchor-folder · ~/Client · read-only');
  for (const tools of [null, undefined, ['write_file'], ['future_tool']])
    assert.doesNotMatch(
      filesystemBindingSubtitle({ ...item, allowedTools: tools }, workspace),
      /read-only/
    );
  assert.match(filesystemBindingSubtitle({ ...item, allowedTools: [] }, workspace), /read-only/);
});

test('anchor deletion names only the sidecar as removed, not the work folder', () => {
  const copy = anchoredDeletionCopy(workspace);
  assert.match(copy, /notes, settings and agents.*~\/Ori Workspaces\/client/);
  assert.match(copy, /Your folder ~\/Client is not touched/);
  assert.equal(anchoredDeletionCopy({ shape: 'managed' }), '');
});
