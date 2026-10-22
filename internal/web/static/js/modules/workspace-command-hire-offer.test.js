// An empty workspace roster offers a hire instead of naming what is missing:
// the blueprint's first open role when there is one, else the Add Agent modal.
//   node --test internal/web/static/js/modules/workspace-command-hire-offer.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { WorkspaceCommandView } from './workspace-command.js';

// The roster component is a classic script; load the real one so the offer
// reads rows exactly as the roster itself draws them.
function loadRoleRoster() {
  const source = readFileSync(new URL('./workspace-role-roster.js', import.meta.url), 'utf8');
  const sandbox = { window: {}, Object, Array, String, Number, Boolean, JSON };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.window.WorkspaceRoleRoster;
}

globalThis.window = { WorkspaceRoleRoster: loadRoleRoster() };
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function role(over = {}) {
  return {
    role_id: 'dev-assistant',
    label: 'Development Assistant',
    description: 'Writes the code.',
    scope: 'project',
    required: true,
    primary: true,
    state: 'empty',
    ...over
  };
}

function makeView({ roles = null, agents = [] } = {}) {
  const view = Object.create(WorkspaceCommandView.prototype);
  const calls = { addAgent: 0, roleCreate: [] };
  Object.assign(view, {
    selectedAgentKey: '',
    activeAgentTab: 'overview',
    roleRoster: roles ? { total_count: roles.length, filled_count: 0, roles } : null,
    page: {
      buildAgentGroups: () => agents,
      openAddAgentModal: () => {
        calls.addAgent += 1;
      }
    }
  });
  view.openRoleCreateModal = (roleId, row) => calls.roleCreate.push({ roleId, label: row.label });
  return { view, calls };
}

// Pulls the empty-state button out of rendered HTML.
function hireButton(html) {
  const match = html.match(/<button[^>]*data-cmd-add-agent[^>]*>([^<]*)<\/button>/);
  assert.ok(match, 'the empty roster renders its hire button');
  const role = match[0].match(/data-cmd-hire-role="([^"]*)"/);
  return { label: match[1], roleId: role ? role[1] : '' };
}

test('empty roster with an open blueprint role names that role and opens its Create', () => {
  const { view, calls } = makeView({
    roles: [
      role({ role_id: 'reviewer', label: 'Reviewer', required: false, primary: false }),
      role()
    ]
  });

  for (const html of [view.renderGarrison(), view.renderMapAgentUnits([])]) {
    assert.match(html, /No one works here yet\./);
    assert.match(html, /Hire someone for this workspace\./);
    assert.doesNotMatch(html, /No agents/);
    // The required role sorts first, as the roster lists it.
    assert.deepEqual(hireButton(html), {
      label: 'Hire for Development Assistant',
      roleId: 'dev-assistant'
    });
  }

  view.requestHire('dev-assistant');
  assert.deepEqual(calls.roleCreate, [{ roleId: 'dev-assistant', label: 'Development Assistant' }]);
  assert.equal(calls.addAgent, 0);
});

test('empty roster skips filled, read-only and stale roles when choosing the offer', () => {
  const { view } = makeView({
    roles: [
      role({ role_id: 'lead', label: 'Lead', state: 'filled', agent: { name: 'Ada' } }),
      role({ role_id: 'locked', label: 'Locked', read_only: true }),
      role({ role_id: 'stale', label: 'Stale', needs_clear: true }),
      role({ role_id: 'writer', label: 'Writer', required: false, primary: false })
    ]
  });
  assert.deepEqual(view.hireOffer(), { roleId: 'writer', label: 'Hire for Writer' });
});

test('empty roster without blueprint roles offers Hire an agent and opens Add Agent', () => {
  for (const roles of [null, []]) {
    const { view, calls } = makeView({ roles });
    const html = view.renderGarrison();
    assert.deepEqual(hireButton(html), { label: 'Hire an agent', roleId: '' });
    view.requestHire('');
    assert.equal(calls.addAgent, 1);
    assert.deepEqual(calls.roleCreate, []);
  }
});

test('a role filled since the offer was drawn falls back to Add Agent', () => {
  const { view, calls } = makeView({ roles: [role()] });
  view.roleRoster.roles[0] = role({ state: 'filled', agent: { name: 'Ada' } });
  view.requestHire('dev-assistant');
  assert.equal(calls.addAgent, 1);
  assert.deepEqual(calls.roleCreate, []);
});

test('the agents panel offers the same hire and its action routes through requestHire', () => {
  const { view, calls } = makeView({ roles: [role()] });
  const html = view.agentRowsHTML();
  assert.match(html, /No one works here yet\. Hire someone for this workspace\./);
  assert.match(
    html,
    /data-cmd-modal-action="hire" data-cmd-id="dev-assistant">Hire for Development Assistant</
  );

  view.statModalSection = 'agents';
  view.closeStatModal = () => {
    view.statModalSection = '';
  };
  view.handleStatModalAction('hire', 'dev-assistant');
  assert.equal(view.statModalSection, '');
  assert.deepEqual(calls.roleCreate, [{ roleId: 'dev-assistant', label: 'Development Assistant' }]);
});

test('a roster with agents keeps its rows and draws no hire offer', () => {
  const agents = [
    { key: 'ada', name: 'Ada', isWorkspaceAgent: true, isUnassigned: false, roles: [], tasks: [] }
  ];
  const { view } = makeView({ roles: [role()], agents });
  const rows = view.agentRowsHTML();
  assert.doesNotMatch(rows, /No one works here yet/);
  assert.match(rows, /Ada/);
  const map = view.renderMapAgentUnits([
    {
      key: 'ada',
      name: 'Ada',
      encodedName: 'Ada',
      entry: true,
      tone: 'idle',
      role: { label: 'Agent' },
      status: { label: 'Idle' }
    }
  ]);
  assert.doesNotMatch(map, /No one works here yet/);
  assert.match(map, /data-cmd-map-select-agent="Ada"/);
});

test('filling a role re-reads the workspace so the hired agent replaces the offer', async () => {
  const { view } = makeView({ roles: [role()] });
  const steps = [];
  view.page.workspaceId = 'workspace-1';
  view.page.loadWorkspace = async () => steps.push('loadWorkspace');
  view.workspaceId = () => 'workspace-1';
  view.mountRoleRoster = () => {};
  view.announceRoleChange = () => {};
  view.ensureGroupTemplateStatus = () => {};
  view.render = () => steps.push('render');
  const filled = {
    total_count: 1,
    filled_count: 1,
    roles: [role({ state: 'filled', agent: { name: 'Ada' } })]
  };
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ roles: filled }) });
  try {
    const outcome = await view.fillRole('dev-assistant', { mode: 'create', name: 'Ada' });
    assert.deepEqual(outcome, { ok: true });
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.deepEqual(steps, ['loadWorkspace', 'render']);
  assert.equal(view.firstOpenRole(), null);
});
