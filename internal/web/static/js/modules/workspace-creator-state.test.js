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

const { buildOrdinaryGroupPayload, createCreatorContext, creatorSteps, switchCreatorKind } =
  creatorState();
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
  assert.deepEqual(asData(creatorSteps(group)), ['details', 'review']);
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
  assert.equal(switchCreatorKind(selected, 'workspace').changed, false);
});

test('ordinary kind switching restores isolated drafts and invalidates final review', () => {
  const initial = createCreatorContext({
    kind: 'workspace',
    drafts: {
      workspace: { name: 'Project Atlas', description: 'Strict team remains staged.' },
      group: { name: 'Atlas homes', description: 'No agents.' }
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

test('ordinary Group payload is an allowlist and cannot leak Workspace-only fields', () => {
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
    create_template_agents: false
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
