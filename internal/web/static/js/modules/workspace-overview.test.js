import test from 'node:test';
import assert from 'node:assert/strict';
import {
  compactTeamHTML,
  linkedTeamHTML,
  linkedWorkspacesHTML,
  resourceAccessHTML,
  capabilityAccessHTML,
  bindOverview,
  setupProjectsChipHTML
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
    else assert.match(html, /Chat contact · Specialist/);
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
  assert.match(resourceAccessHTML(c), /ws-cmd-resource-row modern-btn modern-btn-secondary/);
  c.mapStationRegistry = () => [{ key: 'fixture-station', label: 'Declared station' }];
  assert.match(
    capabilityAccessHTML(c),
    /modern-btn modern-btn-secondary" data-cmd-hq-station="fixture-station"/
  );
  assert.match(
    resourceAccessHTML(c),
    /data-cmd-resource-section="notes"><span>Notes<\/span><span>Unknown/
  );
  c.agentGroups = () => ({ agents: [{ name: '<script>team</script>' }] });
  assert.match(compactTeamHTML(c), /&lt;script&gt;team&lt;\/script&gt;/);
  assert.doesNotMatch(compactTeamHTML(c), /<script>/);
});

test('a group’s Team names the agents of the workspaces inside it, once each, as text', () => {
  const c = view();
  const song = n => ({ id: 's' + n, name: 'Album-' + n, agents: ['REAPER Assistant'] });
  const group = {
    id: 'w',
    kind: 'group',
    agents: ['Agent 0'],
    children: [
      {
        id: 'albums',
        name: 'Albums',
        kind: 'group',
        agents: ['Albums Manager'],
        children: [1, 2, 3].map(song)
      }
    ]
  };
  c.page.membersPanel = { group };
  const faces = [];
  c.agentCharacterHTML = agent => {
    faces.push(agent);
    return '<span aria-hidden="true">Face</span>';
  };

  // A leaf workspace shows nothing more, whatever the tree holds.
  assert.equal(linkedTeamHTML(c), '');
  assert.doesNotMatch(compactTeamHTML(c), /In linked workspaces/);

  c.page.workspace.kind = 'group';
  const html = compactTeamHTML(c);
  assert.match(
    html,
    /<h4 id="ws-cmd-team-linked-title">In linked workspaces <span>2<\/span><\/h4>/
  );
  assert.match(html, /<strong>Albums Manager<\/strong><small>Albums<\/small>/);
  assert.match(html, /<strong>REAPER Assistant<\/strong><small>3 workspaces<\/small>/);
  assert.equal((html.match(/REAPER Assistant/g) || []).length, 1, 'a shared agent is listed once');
  // The group's own team keeps its count and its controls; a linked agent has none.
  assert.match(html, /Team <span>1<\/span>/);
  assert.equal((html.match(/data-cmd-team-agent=/g) || []).length, 1);
  const linked = linkedTeamHTML(c);
  assert.doesNotMatch(linked, /<button|<a |href=|data-cmd-team-agent/);
  assert.deepEqual(
    faces.filter(agent => agent.tone).map(agent => agent.name),
    ['Albums Manager', 'REAPER Assistant', 'Albums Manager', 'REAPER Assistant']
  );

  // It stays under the open controls, and survives an unreadable own roster.
  c.teamExpanded = true;
  assert.match(compactTeamHTML(c), /data-real-agent-controls[\s\S]*In linked workspaces/);
  c.agentGroups = () => {
    throw new Error('unavailable');
  };
  assert.match(compactTeamHTML(c), /not yet available[\s\S]*In linked workspaces/);

  // No agents inside, or no tree yet: no heading at all.
  c.page.membersPanel = { group: { id: 'w', children: [{ id: 'm', name: 'Empty' }] } };
  assert.equal(linkedTeamHTML(c), '');
  c.page.membersPanel = { group: null };
  assert.equal(linkedTeamHTML(c), '');
  delete c.page.membersPanel;
  assert.equal(linkedTeamHTML(c), '');

  c.page.membersPanel = {
    group: { id: 'w', children: [{ id: 'm', name: '<b>M</b>', agents: ['<script>x</script>'] }] }
  };
  assert.match(linkedTeamHTML(c), /&lt;script&gt;x&lt;\/script&gt;<\/strong><small>&lt;b&gt;M/);
  assert.doesNotMatch(linkedTeamHTML(c), /<script>|<b>/);
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

test('a managed Home adds workspaces from its own shelf; a plain group does not', () => {
  const c = view();
  c.page.workspace.kind = 'group';
  assert.doesNotMatch(linkedWorkspacesHTML(c), /data-cmd-setup-projects/);
  c.groupTemplateStatus = { kind: 'managed_home' };
  assert.match(
    linkedWorkspacesHTML(c),
    /data-cmd-setup-projects>Set up projects from the shelf…<\/button>/
  );
});

test('the Home map’s “to set up” button shows only with members and projects waiting', () => {
  assert.equal(setupProjectsChipHTML(null, 3), '');
  assert.equal(setupProjectsChipHTML({ toSetUp: 4 }, 0), '', 'an empty Home’s lot has the button');
  assert.equal(setupProjectsChipHTML({ toSetUp: 0 }, 3), '');
  assert.equal(setupProjectsChipHTML({ toSetUp: 1.5 }, 3), '');
  assert.match(
    setupProjectsChipHTML({ shelf: 6, toSetUp: 4 }, 1),
    /data-cmd-setup-projects><b>4<\/b> to set up · Set up projects…<\/button>/
  );
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
