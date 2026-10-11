import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function creatorState() {
  const window = {};
  const source = readFileSync(new URL('./workspace-creator-state.js', import.meta.url), 'utf8');
  vm.runInNewContext(source, { window }, { filename: 'workspace-creator-state.js' });
  return window.WorkspaceCreatorState;
}

const {
  buildOrdinaryGroupPayload,
  createCreatorContext,
  creatorSteps,
  switchCreatorKind,
  teamLockReason,
  lockedRenameRefusal
} = creatorState();

test('a conversational proposal only prefills editable text, without a build or target', () => {
  const api = creatorState();
  const context = api.createCreatorContext({
    entryPoint: 'assistant_workspace_review',
    name: ' Membership ',
    description: ' Membership only, not talent coaching. ',
    stayAfterCreate: true
  });
  assert.equal(context.name, 'Membership');
  assert.equal(context.description, 'Membership only, not talent coaching.');
  assert.equal(context.parentId, '');
  assert.equal(context.blueprint, '');
  assert.equal(context.folderOfferId, '');
  assert.equal(context.stageBlueprintRoles, false);
  assert.equal(context.buildFirstMessage, '');
  assert.equal(api.buildEntryPointEligible(context), false);
  assert.equal(createCreatorContext({}).description, '');
});

test('only a caller that asks for it gets a connection-only creator', () => {
  assert.equal(createCreatorContext({ connectionOnly: true }).connectionOnly, true);
  for (const options of [{}, { connectionOnly: false }, { stayAfterCreate: true }, undefined]) {
    assert.equal(createCreatorContext(options).connectionOnly, false, JSON.stringify(options));
  }
  // It stays what it was across a kind switch, like the other caller options.
  const context = createCreatorContext({ connectionOnly: true, stayAfterCreate: true });
  assert.equal(switchCreatorKind(context, 'group').context.connectionOnly, true);
});

test('a team lock names agents to keep and survives a kind switch; ordinary creators have none', () => {
  const reason = 'Mail access is granted to the agent named Inbox.';
  const locked = createCreatorContext({
    entryPoint: 'host_setup_quest',
    blueprint: 'email-ops',
    stayAfterCreate: true,
    stageBlueprintRoles: true,
    teamLock: { agentNames: [' Inbox ', ''], reason }
  });
  assert.equal(locked.mode, 'ordinary');
  assert.equal(locked.stayAfterCreate, true);
  assert.equal(locked.stageBlueprintRoles, true);
  assert.equal(locked.blueprintRolesStaged, false);
  assert.deepEqual(asData(locked.teamLock), { agentNames: ['Inbox'], reason });
  assert.equal(teamLockReason(locked, 'inbox'), reason);
  assert.equal(teamLockReason(locked, 'Postmaster'), '');
  assert.equal(lockedRenameRefusal(locked, 'Inbox', 'Mailroom'), reason);
  assert.equal(lockedRenameRefusal(locked, 'Inbox', ' inbox '), '');
  assert.equal(lockedRenameRefusal(locked, 'Postmaster', 'Head of Mail'), '');
  assert.equal(teamLockReason(switchCreatorKind(locked, 'group').context, 'Inbox'), reason);

  const ordinary = createCreatorContext({});
  assert.equal(ordinary.teamLock, null);
  assert.equal(ordinary.stayAfterCreate, false);
  assert.equal(lockedRenameRefusal(ordinary, 'Inbox', 'Mailroom'), '');
  for (const teamLock of [
    { agentNames: ['Inbox'] },
    { reason },
    { agentNames: [], reason },
    'Inbox'
  ]) {
    assert.equal(createCreatorContext({ teamLock }).teamLock, null);
  }
});
const asData = value => JSON.parse(JSON.stringify(value));

test('ordinary creator defaults to the complete Workspace sequence and can preselect Group', () => {
  const workspace = createCreatorContext({ generation: 7 });
  assert.equal(workspace.kind, 'workspace');
  assert.equal(workspace.fixedKind, '');
  assert.deepEqual(asData(creatorSteps(workspace)), ['blueprint', 'details', 'team', 'review']);

  const group = createCreatorContext({
    generation: 8,
    kind: 'group',
    entryPoint: 'tree_new_group'
  });
  assert.equal(group.kind, 'group');
  assert.equal(group.fixedKind, '');
  assert.deepEqual(asData(creatorSteps(group)), ['details', 'roster', 'review']);
});

test('import, guided, and selected-member creators retain their fixed operation kind', () => {
  const imported = createCreatorContext({ importMode: true, kind: 'group' });
  assert.equal(imported.mode, 'import');
  assert.equal(imported.kind, 'workspace');
  assert.equal(imported.fixedKind, 'workspace');
  assert.deepEqual(asData(creatorSteps(imported)), ['details']);
  assert.equal(switchCreatorKind(imported, 'group').changed, false);

  const guided = createCreatorContext({ mode: 'guided', fixedKind: 'group', kind: 'workspace' });
  assert.equal(guided.kind, 'group');
  assert.equal(guided.fixedKind, 'group');
  assert.deepEqual(asData(creatorSteps(guided)), ['details', 'review']);
  assert.equal(switchCreatorKind(guided, 'workspace').changed, false);

  const selected = createCreatorContext({
    mode: 'selected-members',
    kind: 'group',
    selection: { ids: ['parent', 'child'], names: ['Parent', 'Child'] }
  });
  assert.equal(selected.kind, 'group');
  assert.equal(selected.fixedKind, 'group');
  assert.deepEqual(asData(selected.selection), {
    ids: ['parent', 'child'],
    names: ['Parent', 'Child']
  });
  assert.deepEqual(asData(creatorSteps(selected)), ['details', 'roster', 'review']);
  assert.equal(switchCreatorKind(selected, 'workspace').changed, false);
});

test('ordinary kind switching restores isolated drafts and invalidates final review', () => {
  const initial = createCreatorContext({
    kind: 'workspace',
    drafts: {
      workspace: { name: 'Project Atlas', description: 'Strict team remains staged.' },
      group: { name: 'Atlas homes', description: 'Managed separately.' }
    },
    review: { token: 'stale-review' }
  });

  const switched = switchCreatorKind(initial, 'group');
  assert.equal(switched.changed, true);
  assert.equal(switched.context.kind, 'group');
  assert.equal(switched.context.review, null);
  assert.deepEqual(switched.context.drafts.workspace, initial.drafts.workspace);
  assert.deepEqual(switched.context.drafts.group, initial.drafts.group);

  const returned = switchCreatorKind(switched.context, 'workspace');
  assert.equal(returned.context.kind, 'workspace');
  assert.deepEqual(returned.context.drafts.workspace, initial.drafts.workspace);
  assert.equal(returned.context.review, null);
});

test('ordinary Group payload declares its reviewed roster without leaking Workspace-only fields', () => {
  const payload = buildOrdinaryGroupPayload({
    name: 'Client homes',
    description: 'A home for related workspaces.',
    parent_id: 'parent-group',
    color: '#22c55e',
    template_id: 'plugin:project',
    template_path: '/tmp/project-template',
    blank: true,
    workspace_bootstrap: { goal: 'leak' },
    workspace_preset: 'software_project',
    team_intent: { version: 1, mode: 'strict' },
    role_staffing: [{ role_id: 'lead', mode: 'create' }],
    existing_agent_names: ['Coordinator'],
    entry_agent_name: 'Coordinator',
    template_agent_overrides: [{ name: 'Coordinator' }],
    template_agent_review: { expectations: [] },
    project_open: true,
    assistant_program_membership: { program_id: 'home' },
    tags: ['must-not-leak']
  });

  assert.deepEqual(asData(payload), {
    name: 'Client homes',
    description: 'A home for related workspaces.',
    parent_id: 'parent-group',
    color: '#22c55e',
    kind: 'group',
    group_roster: true,
    create_template_agents: true
  });
  for (const key of [
    'template_id',
    'template_path',
    'blank',
    'workspace_bootstrap',
    'workspace_preset',
    'team_intent',
    'role_staffing',
    'existing_agent_names',
    'entry_agent_name',
    'template_agent_overrides',
    'template_agent_review',
    'project_open',
    'assistant_program_membership',
    'tags'
  ]) {
    assert.equal(key in payload, false, `${key} must not be sent for an ordinary Group`);
  }
});

const buildApi = creatorState();

test('build mode is offered only from its four openers, for a hired assistant with a model', () => {
  const ready = {
    entryPoint: 'home_cockpit_create',
    assistantState: 'active',
    availability: { available: true }
  };
  assert.equal(buildApi.buildModeEligible(ready), true);
  for (const entryPoint of [
    'workspace_map_build',
    'workspace_hub_create',
    'personal_assistant_ask'
  ]) {
    assert.equal(buildApi.buildModeEligible({ ...ready, entryPoint }), true, entryPoint);
  }
  assert.equal(buildApi.buildModeEligible({ ...ready, assistantState: 'paused' }), true);
  for (const entryPoint of ['folder_digest', 'specialist_setup', 'workspace_hub_import', '']) {
    assert.equal(buildApi.buildModeEligible({ ...ready, entryPoint }), false, entryPoint);
  }
  for (const assistantState of ['needs_hire', 'needs_hq', 'hiring', 'repair_needed', '']) {
    assert.equal(buildApi.buildModeEligible({ ...ready, assistantState }), false, assistantState);
  }
  assert.equal(buildApi.buildModeEligible({ ...ready, availability: { available: false } }), false);
  assert.equal(buildApi.buildModeEligible({ ...ready, availability: null }), false);
});

test('manual openers keep the wizard: import, guided, specialist, groups, locks, deep links', () => {
  const ready = {
    entryPoint: 'home_cockpit_create',
    assistantState: 'active',
    availability: { available: true }
  };
  const refused = [
    { importMode: true },
    { mode: 'guided' },
    { mode: 'specialist' },
    { kind: 'group' },
    { fixedKind: 'group' },
    { parentLocked: true, parentId: 'group-1' },
    { blueprint: 'content-production' },
    { folderOfferId: 'offer-1' }
  ];
  for (const extra of refused) {
    assert.equal(buildApi.buildModeEligible({ ...ready, ...extra }), false, JSON.stringify(extra));
  }
});

test('a build draft advances the wizard only as far as the gates allow, never backwards', () => {
  const { buildStepFor } = buildApi;
  assert.equal(buildStepFor({}, { current: 1 }), 1);
  assert.equal(buildStepFor({ template_id: 'content-production' }, { current: 1 }), 2);
  assert.equal(buildStepFor({ blank: true }, { current: 1 }), 2);
  assert.equal(
    buildStepFor({ template_id: 'content-production' }, { current: 1, blueprintReady: false }),
    1
  );
  const named = { template_id: 'content-production', name: 'Desk', description: 'Drafts' };
  assert.equal(buildStepFor(named, { current: 1 }), 3);
  assert.equal(buildStepFor({ ...named, description: '' }, { current: 1 }), 2);
  assert.equal(buildStepFor(named, { current: 1, nameValid: false }), 2);
  assert.equal(buildStepFor(named, { current: 1, inputsValid: false }), 2);
  assert.equal(buildStepFor(named, { current: 1, teamSet: true }), 4);
  assert.equal(buildStepFor(named, { current: 1, agentless: true }), 4);
  assert.equal(
    buildStepFor({ template_id: 'x' }, { current: 3 }),
    3,
    'never below the current step'
  );
});

test('a session patch carries only the fields the turn set, or the whole draft to resume', () => {
  const session = {
    draft: {
      template_id: 'content-production',
      name: 'Newsletter Desk',
      description: 'Drafts the Monday newsletter',
      blueprint_inputs: { cadence: 'weekly' },
      tags: ['writing']
    },
    applied: ['blueprint', 'name']
  };
  assert.deepEqual(asData(buildApi.buildPatchFromSession(session, session.applied)), {
    template_id: 'content-production',
    name: 'Newsletter Desk'
  });
  // Resuming restores what the draft holds; an absent key is left alone.
  assert.deepEqual(asData(buildApi.buildPatchFromSession(session)), {
    template_id: 'content-production',
    name: 'Newsletter Desk',
    description: 'Drafts the Monday newsletter',
    blueprint_inputs: { cadence: 'weekly' },
    tags: ['writing']
  });
  // The server omits an emptied field, so a turn that set it cleared it.
  assert.deepEqual(
    asData(buildApi.buildPatchFromSession(session, ['parent', 'color', 'description'])),
    { parent_id: '', color: '', description: 'Drafts the Monday newsletter' }
  );
  assert.deepEqual(
    asData(buildApi.buildPatchFromSession({ draft: { blank: true } }, ['blueprint'])),
    { blank: true }
  );
  assert.deepEqual(asData(buildApi.buildPatchFromSession(null, ['name'])), {});
});

test('a creator context carries the first sentence for a build, and no session yet', () => {
  const context = buildApi.createCreatorContext({
    entryPoint: 'personal_assistant_ask',
    buildFirstMessage: '  a newsletter from my notes  '
  });
  assert.equal(context.buildFirstMessage, 'a newsletter from my notes');
  assert.equal(context.buildSession, null);
});
