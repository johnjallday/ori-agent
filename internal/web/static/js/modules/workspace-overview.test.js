import test from 'node:test';
import assert from 'node:assert/strict';
import {
  compactTeamHTML,
  linkedWorkspacesHTML,
  resourceAccessHTML,
  bindOverview
} from './workspace-overview.js';
import { WorkspaceCommandView } from './workspace-command.js';

function view(count = 1) {
  const c = Object.create(WorkspaceCommandView.prototype);
  c.page = { workspaceId: 'w', workspace: { id: 'w' }, initialListsLoaded: true };
  c.agentGroups = () => ({
    agents: Array.from({ length: count }, (_, i) => ({ name: 'Agent ' + i })),
    unassigned: null
  });
  c.agentViewModel = group => ({
    ...group,
    encodedName: encodeURIComponent(group.name),
    entry: group.name === 'Agent 0',
    role: { label: 'Specialist' },
    status: { label: 'Ready to help' }
  });
  c.agentCharacterHTML = () => '<span aria-hidden="true">Face</span>';
  c.renderGarrison = () => '<div data-real-agent-controls>Existing controls</div>';
  return c;
}

test('Team is compact for zero, one and many agents; existing controls are on demand', () => {
  for (const count of [0, 1, 8]) {
    const c = view(count);
    const html = compactTeamHTML(c);
    assert.doesNotMatch(html, /data-real-agent-controls/);
    assert.equal((html.match(/data-cmd-team-agent=/g) || []).length, count);
    if (!count) assert.match(html, /Hire an agent/);
    else assert.match(html, /Lead · Specialist/);
    c.teamExpanded = true;
    assert.match(compactTeamHTML(c), /data-real-agent-controls/);
  }
});

test('unavailable roster/resource data is not empty, and labels remain literal', () => {
  const c = view();
  c.agentGroups = () => {
    throw new Error('unavailable');
  };
  assert.match(compactTeamHTML(c), /not yet available/);
  assert.doesNotMatch(compactTeamHTML(c), /Hire an agent/);
  c.teamExpanded = true;
  assert.doesNotMatch(compactTeamHTML(c), /data-real-agent-controls|Hire an agent/);
  assert.match(compactTeamHTML(c), /data-cmd-team-toggle aria-expanded="false" disabled/);
  c.page.notesLoadFailed = true;
  c.page.notes = [];
  assert.match(
    resourceAccessHTML(c),
    /data-cmd-resource-section="notes"><span>Notes<\/span><span>Unknown/
  );
  c.agentGroups = () => ({ agents: [{ name: '<script>team</script>' }] });
  assert.match(compactTeamHTML(c), /&lt;script&gt;team&lt;\/script&gt;/);
  assert.doesNotMatch(compactTeamHTML(c), /<script>/);
});

test('only a group gets linked-workspace controls, backed by the existing members host', () => {
  const c = view();
  assert.equal(linkedWorkspacesHTML(c), '');
  c.page.workspace.kind = 'group';
  const html = linkedWorkspacesHTML(c);
  assert.match(html, /data-cmd-members-host/);
  assert.match(html, /data-cmd-group-create>Create new/);
  assert.match(html, /data-cmd-group-link>Add existing/);
});

test('opening a team row chooses the same agent and exposes its real controls', () => {
  const c = view();
  const listeners = {};
  c.viewMode = 'details';
  c.container = {
    querySelector: selector =>
      selector === '.ws-cmd-overview'
        ? {
            addEventListener: (name, fn) => {
              listeners[name] = fn;
            }
          }
        : null
  };
  const calls = [];
  c.selectAgent = (...args) => calls.push(args);
  bindOverview(c);
  const agent = { getAttribute: () => 'Agent%200' };
  listeners.click({
    target: { closest: selector => (selector === '[data-cmd-team-agent]' ? agent : null) }
  });
  assert.equal(c.teamExpanded, true);
  assert.deepEqual(calls, [['Agent%200', { focus: false }]]);
});

test('pure Overview mounting does not reload the tree or rollups', async () => {
  const c = view();
  c.page.workspace.kind = 'group';
  c.viewMode = 'details';
  c.active = true;
  const calls = [];
  c.page.membersPanel = {
    syncWorkspace: () => calls.push('tree'),
    loadRollups: () => calls.push('rollups')
  };
  c.container = { querySelector: () => ({}) };
  c.mountSharedSurface = () => ({ removeAttribute() {} });
  await c.syncDetachmentSurface();
  await c.syncDetachmentSurface();
  assert.deepEqual(calls, []);
});
