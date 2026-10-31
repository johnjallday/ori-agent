import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceMCPManager } from './workspace-detail-mcp.js';

test('anchored workspace never displays synthesized filesystem access after binding removal', () => {
  for (const metadata of [
    { shape: 'anchored' },
    { shared_data: { anchor_directory_reference_id: 'anchor' } }
  ]) {
    const host = {
      workspace: {
        ...metadata,
        directory_references: [{ id: 'anchor', path: '/external' }],
        mcp_bindings: []
      }
    };
    const manager = new WorkspaceMCPManager(host);
    assert.deepEqual(manager.getWorkspaceMCPBindings(), []);
    host.workspace.mcp_bindings = [
      { id: 'explicit', server_name: 'filesystem', alias: 'anchor-folder', enabled: true }
    ];
    const [binding] = manager.getWorkspaceMCPBindings();
    assert.equal(binding.source, 'workspace');
    assert.equal(binding.alias, 'anchor-folder');
  }
});

test('MCP normalization preserves explicit empty and narrow allowlists, distinct from all tools', () => {
  const manager = new WorkspaceMCPManager({});
  for (const allowed of [[], ['read_file'], null, undefined]) {
    const normalized = manager.normalizeWorkspaceMCPBinding({
      server_name: 'filesystem',
      allowed_tools: allowed
    });
    assert.deepEqual(normalized.allowedTools, allowed ?? null);
  }
});

test('legacy managed workspace retains synthesized filesystem display', () => {
  const manager = new WorkspaceMCPManager({
    workspace: { directory_references: [{ path: '/managed' }] }
  });
  assert.equal(manager.getWorkspaceMCPBindings()[0].source, 'synthesized');
});
