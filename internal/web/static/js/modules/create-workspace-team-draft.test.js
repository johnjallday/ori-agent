import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./create-workspace-team-draft.js', import.meta.url), 'utf8');

// Loaded into THIS realm rather than a fresh vm context so the module's arrays
// and objects share the test realm's prototypes; deepStrictEqual compares those,
// and a cross-realm load makes every structural assertion fail spuriously.
function loadDraftModule() {
  globalThis.window = globalThis.window || {};
  vm.runInThisContext(source, { filename: 'create-workspace-team-draft.js' });
  return globalThis.window.CreateWorkspaceTeamDraft;
}

const Draft = loadDraftModule();

// Server plan shape (snake_case), as /api/workspaces/template-agent-plan returns it.
function planResponse(agents, extra = {}) {
  return {
    has_agents: agents.length > 0,
    agents,
    warnings: [],
    ...extra
  };
}

function planAgent(name, overrides = {}) {
  return {
    name,
    scope: 'reusable',
    action: 'create',
    entry_point: false,
    model_source: 'agent_default',
    ...overrides
  };
}

function assistantProgramPlan() {
  return {
    id: 'studio-guide',
    station_name: 'Studio Home',
    station_description: 'One team shared across linked projects.',
    default_primary_name: 'Producer',
    hire_title: 'Hire your producer',
    hire_description: 'Name the producer and review the bounded room.',
    roles: [
      {
        id: 'producer',
        label: 'Producer',
        description: 'Coordinates the room.',
        primary: true
      },
      {
        id: 'engineer',
        label: 'Mix Engineer',
        description: 'Handles technical session concerns.',
        primary: false
      },
      {
        id: 'writer',
        label: 'Songwriter',
        description: 'Handles composition and arrangement.',
        primary: false
      }
    ],
    stages: [
      { id: 'helper', label: 'Helper', description: 'Guides requested work.' },
      { id: 'collaborator', label: 'Collaborator', description: 'Forms recommendations.' }
    ]
  };
}

function readyDraft(agents, blueprintKey = 'template:downloads-janitor') {
  const draft = Draft.createDraft();
  Draft.setPlanReady(draft, blueprintKey, planResponse(agents));
  return draft;
}

function withSavedRoster(draft, agents) {
  Draft.setSavedRosterReady(draft, agents);
  return draft;
}

test('a fresh draft includes the blueprint team and stages nothing', () => {
  const draft = Draft.createDraft();
  assert.equal(draft.plan.status, 'idle');
  assert.equal(draft.includeBlueprintTeam, true);
  assert.equal(draft.overrides.size, 0);
  assert.deepEqual(draft.savedSelections, []);
  assert.equal(draft.explicitPrimary, '');

  const view = Draft.derive(draft);
  assert.deepEqual(view.roster, []);
  assert.equal(view.primaryName, '');
  assert.deepEqual(view.payload, {}, 'an untouched draft contributes no request fields');
});

test('loading is its own state and is never reported as a confirmed empty team (FR18, FR92)', () => {
  const draft = Draft.createDraft();
  Draft.setPlanLoading(draft, 'template:reaper-song');
  const view = Draft.derive(draft);

  assert.equal(view.isLoading, true);
  assert.equal(view.blueprintSummary.status, 'loading');
  assert.equal(view.blueprintSummary.isEmpty, false, 'loading is not emptiness');
  const ids = view.issues.map(issue => issue.id);
  assert.deepEqual(ids, ['plan-loading']);
  assert.equal(view.issues[0].severity, 'loading');
  assert.ok(
    !view.issues.some(issue => issue.id === 'empty-team'),
    'must not warn about an empty team while the plan is still loading'
  );
});

test('a ready blueprint with no agents is a confirmed empty state (FR19)', () => {
  const draft = readyDraft([]);
  const view = Draft.derive(draft);

  assert.equal(view.blueprintSummary.status, 'ready');
  assert.equal(view.blueprintSummary.isEmpty, true);
  assert.equal(view.blueprintSummary.count, 0);
  const empty = view.issues.find(issue => issue.id === 'empty-team');
  assert.equal(empty.severity, 'advisory', 'an agent-less team is allowed (FR55)');
  assert.equal(view.canContinueFromTeam, true);
});

test('assistant programs become a named shared roster in Team instead of an empty state', () => {
  const draft = Draft.createDraft();
  Draft.setPlanReady(
    draft,
    'template:studio',
    planResponse([], { assistant_program: assistantProgramPlan() })
  );
  const view = Draft.derive(draft);

  assert.equal(view.isAssistantProgram, true);
  assert.equal(view.assistantHire.name, 'Producer');
  assert.equal(view.blueprintSummary.count, 3);
  assert.equal(view.blueprintSummary.isEmpty, false);
  assert.deepEqual(
    view.roster.map(entry => [entry.name, entry.designation, entry.lifecycle]),
    [
      ['Producer', 'primary', 'assistant-create'],
      ['Mix Engineer', 'specialist', 'assistant-create'],
      ['Songwriter', 'specialist', 'assistant-create']
    ]
  );
  assert.ok(!view.issues.some(issue => issue.id === 'empty-team'));
  assert.deepEqual(view.payload.assistant_hire, { name: 'Producer', provider: '', model: '' });
});

test('an existing shared roster is linked without offering a second identity', () => {
  const draft = Draft.createDraft();
  const program = assistantProgramPlan();
  program.existing_hired = true;
  program.existing_provider = 'ollama';
  program.existing_model = 'gemma4:e4b';
  program.roles[0].agent_name = 'June';
  program.roles[1].agent_name = 'Mix Engineer';
  program.roles[2].agent_name = 'Songwriter';
  Draft.setPlanReady(draft, 'template:studio', planResponse([], { assistant_program: program }));

  Draft.setAssistantHire(draft, { name: 'Ignored rename', provider: 'other', model: 'other' });
  const view = Draft.derive(draft);
  assert.equal(view.assistantProgram.existingHired, true);
  assert.equal(view.primaryName, 'June');
  assert.ok(view.roster.every(entry => entry.lifecycle === 'assistant-link'));
  assert.deepEqual(view.payload.assistant_hire, {
    name: 'June',
    provider: 'ollama',
    model: 'gemma4:e4b'
  });
  assert.equal(view.isModifiedFromBlueprint, false);
});

test('assistant hire choices are staged, validated, and reset with the blueprint', () => {
  const draft = Draft.createDraft();
  Draft.setPlanReady(
    draft,
    'template:studio',
    planResponse([], { assistant_program: assistantProgramPlan() })
  );
  Draft.setAssistantHire(draft, {
    name: 'June',
    provider: 'ollama',
    model: 'gemma4:e4b'
  });
  let view = Draft.derive(draft);
  assert.equal(view.primaryName, 'June');
  assert.equal(view.roster[0].name, 'June');
  assert.deepEqual(view.payload.assistant_hire, {
    name: 'June',
    provider: 'ollama',
    model: 'gemma4:e4b'
  });
  assert.equal(view.isModifiedFromBlueprint, true);

  Draft.setAssistantHire(draft, { name: 'Songwriter' });
  view = Draft.derive(draft);
  assert.equal(view.canContinueFromTeam, false);
  assert.equal(
    view.issues.find(issue => issue.id === 'duplicate-names').anchor,
    'assistantProgramCreateName'
  );

  Draft.setAssistantHire(draft, { name: '' });
  view = Draft.derive(draft);
  assert.equal(view.issues.find(issue => issue.id === 'assistant-name').severity, 'blocking');

  Draft.setPlanReady(draft, 'template:other', planResponse([]));
  assert.equal(Draft.derive(draft).isAssistantProgram, false);
  assert.equal(draft.assistantHire.name, '');
});

test('blueprint summary reports count, names, and declared primary (FR15-FR17)', () => {
  const draft = readyDraft([
    planAgent('Downloads Curator', { entry_point: true, action: 'reuse' }),
    planAgent('Filing Scout')
  ]);
  const view = Draft.derive(draft);

  assert.equal(view.blueprintSummary.count, 2);
  assert.deepEqual(view.blueprintSummary.names, ['Downloads Curator', 'Filing Scout']);
  assert.equal(view.blueprintSummary.declaredPrimary, 'Downloads Curator');
});

test('the declared primary sorts first and every other member is a specialist (FR34, FR35)', () => {
  const draft = readyDraft([
    planAgent('Filing Scout'),
    planAgent('Downloads Curator', { entry_point: true }),
    planAgent('Report Writer')
  ]);
  const view = Draft.derive(draft);

  assert.deepEqual(
    view.roster.map(entry => entry.name),
    ['Downloads Curator', 'Filing Scout', 'Report Writer']
  );
  assert.deepEqual(
    view.roster.map(entry => entry.designation),
    ['primary', 'specialist', 'specialist']
  );
  assert.equal(view.primaryName, 'Downloads Curator');
  assert.deepEqual(
    view.specialists.map(entry => entry.name),
    ['Filing Scout', 'Report Writer']
  );
});

test('lifecycle copy describes future behavior, never past attachment (FR37-FR39)', () => {
  const draft = readyDraft([
    planAgent('Saved One', { action: 'reuse', entry_point: true }),
    planAgent('Brand New', { action: 'create' })
  ]);
  const view = Draft.derive(draft);

  assert.equal(view.roster[0].lifecycle, 'reuse');
  assert.equal(view.roster[0].lifecycleLabel, 'Saved agent · will be attached');
  assert.equal(view.roster[1].lifecycle, 'create');
  assert.equal(view.roster[1].lifecycleLabel, 'New reusable agent · will be created and attached');
  view.roster.forEach(entry => {
    assert.ok(
      !/attached\s*$/i.test(entry.lifecycleLabel) || /will be/.test(entry.lifecycleLabel),
      `lifecycle label must not claim completed attachment: ${entry.lifecycleLabel}`
    );
  });
});

test('renaming a reused agent stages a customized copy and leaves the original untouched (FR40-FR43)', () => {
  const draft = readyDraft([planAgent('Shared Scout', { action: 'reuse', entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'Shared Scout copy' });
  const view = Draft.derive(draft);

  assert.equal(view.roster[0].name, 'Shared Scout copy');
  assert.equal(view.roster[0].lifecycle, 'customized-copy');
  assert.equal(view.roster[0].lifecycleLabel, 'Customized copy · will be created and attached');
  assert.equal(view.roster[0].originalName, 'Shared Scout', 'the shared definition is identified');
  assert.deepEqual(view.payload.template_agent_overrides, [
    { index: 0, name: 'Shared Scout copy' }
  ]);
});

test('an unmodified reused agent stages no override at all (FR41)', () => {
  const draft = readyDraft([planAgent('Shared Scout', { action: 'reuse', entry_point: true })]);
  const view = Draft.derive(draft);

  assert.equal(view.roster[0].lifecycle, 'reuse');
  assert.equal(view.payload.template_agent_overrides, undefined);
  assert.equal(view.isModifiedFromBlueprint, false);
});

test('customizing a new blueprint agent edits it in place instead of adding a row (FR46)', () => {
  const draft = readyDraft([planAgent('Brand New', { action: 'create', entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'Renamed New', systemPrompt: 'Be brief.' });
  Draft.stageOverride(draft, 0, { model: 'claude-opus-5', provider: 'anthropic' });
  const view = Draft.derive(draft);

  assert.equal(view.roster.length, 1, 'still one roster entry');
  assert.equal(view.roster[0].name, 'Renamed New');
  assert.equal(view.roster[0].lifecycle, 'create', 'a new agent stays a create, not a copy');
  assert.deepEqual(view.payload.template_agent_overrides, [
    {
      index: 0,
      name: 'Renamed New',
      model: 'claude-opus-5',
      provider: 'anthropic',
      system_prompt: 'Be brief.'
    }
  ]);
});

test('an explicitly emptied model is staged as empty, not dropped (inherit survives conversion, FR69)', () => {
  const draft = readyDraft([
    planAgent('Tuned', { action: 'create', model: 'gpt-5', provider: 'openai' })
  ]);
  Draft.stageOverride(draft, 0, { model: '', provider: '' });
  const view = Draft.derive(draft);

  assert.deepEqual(
    view.payload.template_agent_overrides,
    [{ index: 0, model: '', provider: '' }],
    'present-but-empty must survive: the server reads it as "inherit the default"'
  );
  assert.equal(view.roster[0].modelLabel, 'App default');
  assert.equal(view.roster[0].inheritsModel, true);
});

test('re-staging the blueprint name clears the override rather than sending a no-op', () => {
  const draft = readyDraft([planAgent('Steady', { action: 'create', entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'Changed' });
  assert.equal(draft.overrides.size, 1);
  Draft.stageOverride(draft, 0, { name: 'Steady' });
  assert.equal(draft.overrides.size, 0, 'returning to the original name is not a customization');
  assert.equal(Draft.derive(draft).payload.template_agent_overrides, undefined);
});

test('a case-only rename of a reused agent is not treated as a copy', () => {
  const draft = readyDraft([planAgent('Shared Scout', { action: 'reuse', entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'shared scout' });
  const view = Draft.derive(draft);

  assert.equal(draft.overrides.size, 0, 'case alone would not produce a different agent');
  assert.equal(view.roster[0].lifecycle, 'reuse');
  assert.equal(view.roster[0].name, 'Shared Scout');
});

test('a customized name colliding with another roster member blocks continuation (FR45)', () => {
  const draft = readyDraft([
    planAgent('Curator', { action: 'reuse', entry_point: true }),
    planAgent('Scout', { action: 'create' })
  ]);
  Draft.stageOverride(draft, 1, { name: 'curator' });
  const view = Draft.derive(draft);

  // Regression guard: promoting the primary by name-key rather than by position
  // silently dropped the colliding member here, which hid the duplicate entirely.
  assert.equal(view.roster.length, 2, 'the colliding member must stay visible in the roster');

  const collision = view.issues.find(issue => issue.id === 'duplicate-names');
  assert.ok(collision, 'a case-insensitive collision must be reported');
  assert.equal(collision.severity, 'blocking');
  assert.equal(collision.anchor, 'team-agent-name-1', 'anchored to the offending field (FR104)');
  assert.equal(collision.templateAgentIndex, 1, 'points at the customized entry, not the original');
  assert.equal(view.canContinueFromTeam, false);
});

test('excluding the blueprint team empties the roster and recomputes the primary (FR48-FR50)', () => {
  const draft = readyDraft([planAgent('Blueprint Lead', { entry_point: true })]);
  withSavedRoster(draft, [{ name: 'Research Scout', model: 'gpt-5' }]);
  Draft.addSavedAgent(draft, 'Research Scout');

  let view = Draft.derive(draft);
  assert.equal(
    view.primaryName,
    'Blueprint Lead',
    'the blueprint owns the primary slot by default'
  );

  Draft.setIncludeBlueprintTeam(draft, false);
  view = Draft.derive(draft);
  assert.deepEqual(
    view.roster.map(entry => entry.name),
    ['Research Scout'],
    'blueprint entries leave the roster'
  );
  assert.equal(view.primaryName, 'Research Scout', 'the first saved agent becomes primary');
  assert.equal(view.primaryIsAutomatic, true, 'an automatic promotion is flagged for announcement');
  assert.equal(view.payload.create_template_agents, false);
  assert.equal(view.payload.entry_agent_name, 'Research Scout');
});

test('an unavailable blueprint plan blocks Team while its team is included (FR94, FR95)', () => {
  const draft = Draft.createDraft();
  Draft.setPlanError(draft, 'template:broken', 'Could not load blueprint agents.');

  let view = Draft.derive(draft);
  const blocker = view.issues.find(issue => issue.id === 'plan-error');
  assert.equal(blocker.severity, 'blocking');
  assert.deepEqual(blocker.recovery, ['retry-plan', 'edit-blueprint', 'exclude-blueprint-team']);
  assert.equal(view.canContinueFromTeam, false);

  // Taking the documented recovery path clears the blocker (FR95).
  Draft.setIncludeBlueprintTeam(draft, false);
  view = Draft.derive(draft);
  assert.ok(!view.issues.some(issue => issue.id === 'plan-error'));
  assert.equal(view.canContinueFromTeam, true);
});

test('a saved-agent roster failure stays advisory when the team is otherwise valid (FR66, FR96)', () => {
  const draft = readyDraft([planAgent('Blueprint Lead', { entry_point: true, action: 'reuse' })]);
  Draft.setSavedRosterError(draft, 'Your saved agents could not be loaded.');
  const view = Draft.derive(draft);

  const issue = view.issues.find(issue => issue.id === 'saved-roster-error');
  assert.equal(issue.severity, 'advisory');
  assert.deepEqual(issue.recovery, ['retry-saved-roster']);
  assert.equal(view.canContinueFromTeam, true, 'creation may still continue');
});

test('saved agents attach in selection order and are refused when already included (FR62)', () => {
  const draft = readyDraft([planAgent('Curator', { entry_point: true, action: 'reuse' })]);
  withSavedRoster(draft, [
    { name: 'Research Scout' },
    { name: 'Data Miner' },
    { name: 'Curator' },
    { name: 'Claude Code', source: 'cli' }
  ]);

  assert.equal(Draft.addSavedAgent(draft, 'Data Miner'), true);
  assert.equal(Draft.addSavedAgent(draft, 'Research Scout'), true);
  assert.equal(Draft.addSavedAgent(draft, 'data miner'), false, 'no case-insensitive duplicate');
  assert.equal(Draft.addSavedAgent(draft, 'Curator'), false, 'already included by the blueprint');
  assert.equal(Draft.addSavedAgent(draft, 'Claude Code'), false, 'CLI agents are not attachable');
  assert.equal(Draft.addSavedAgent(draft, 'Nobody'), false, 'unknown agents are refused');

  const view = Draft.derive(draft);
  assert.deepEqual(view.payload.existing_agent_names, ['Data Miner', 'Research Scout']);
});

test('choosing a saved agent as primary demotes the previous primary to specialist (FR51, FR52)', () => {
  const draft = readyDraft([planAgent('Blueprint Lead', { entry_point: true, action: 'reuse' })]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.addSavedAgent(draft, 'Research Scout');

  assert.equal(Draft.setExplicitPrimary(draft, 'Research Scout'), true);
  const view = Draft.derive(draft);

  assert.equal(view.roster[0].name, 'Research Scout');
  assert.equal(view.roster[0].designation, 'primary');
  const demoted = view.roster.find(entry => entry.name === 'Blueprint Lead');
  assert.equal(demoted.designation, 'specialist', 'the previous primary stays attached');
  assert.equal(view.primaryIsAutomatic, false);
  assert.equal(view.payload.entry_agent_name, 'Research Scout');
});

test('only a selected saved agent may be made primary', () => {
  const draft = readyDraft([planAgent('Blueprint Lead', { entry_point: true })]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);

  assert.equal(
    Draft.setExplicitPrimary(draft, 'Research Scout'),
    false,
    'an unselected agent cannot hold the primary slot'
  );
  assert.equal(
    Draft.setExplicitPrimary(draft, 'Blueprint Lead'),
    false,
    'a blueprint primary is derived from roster order, not entry_agent_name'
  );
  assert.equal(draft.explicitPrimary, '');
});

test('removing a saved agent updates the roster and hands back the primary slot (FR53)', () => {
  const draft = readyDraft([]);
  withSavedRoster(draft, [{ name: 'Research Scout' }, { name: 'Data Miner' }]);
  Draft.addSavedAgent(draft, 'Research Scout');
  Draft.addSavedAgent(draft, 'Data Miner');
  Draft.setExplicitPrimary(draft, 'Data Miner');

  assert.equal(Draft.removeSavedAgent(draft, 'Data Miner'), true);
  assert.equal(draft.explicitPrimary, '', 'no dangling primary reference survives removal');

  const view = Draft.derive(draft);
  assert.deepEqual(
    view.roster.map(entry => entry.name),
    ['Research Scout']
  );
  assert.equal(view.primaryName, 'Research Scout');
  assert.ok(!view.issues.some(issue => issue.id === 'invalid-primary'));
});

test('changing blueprint drops staged overrides but keeps still-valid saved agents (FR21, FR22)', () => {
  const draft = readyDraft([planAgent('Old Lead', { entry_point: true, action: 'create' })]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.addSavedAgent(draft, 'Research Scout');
  Draft.stageOverride(draft, 0, { name: 'Renamed Lead' });
  Draft.setIncludeBlueprintTeam(draft, false);

  Draft.setPlanLoading(draft, 'template:other');
  assert.equal(draft.overrides.size, 0, 'overrides belonged to the previous blueprint');
  assert.equal(draft.includeBlueprintTeam, true, 'include-team returns to the blueprint default');
  assert.deepEqual(draft.savedSelections, ['Research Scout'], 'manual selections are retained');

  Draft.setPlanReady(
    draft,
    'template:other',
    planResponse([planAgent('New Lead', { entry_point: true })])
  );
  const view = Draft.derive(draft);
  assert.deepEqual(
    view.roster.map(entry => entry.name),
    ['New Lead', 'Research Scout']
  );
});

test('retrying the same blueprint preserves staged customizations', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true, action: 'create' })]);
  Draft.stageOverride(draft, 0, { name: 'Renamed Lead' });

  Draft.setPlanLoading(draft, 'template:downloads-janitor');
  assert.equal(draft.overrides.size, 1, 'a retry of the same blueprint is not a blueprint change');
});

test('a retained saved agent the new blueprint also includes is attached once (FR23)', () => {
  const draft = Draft.createDraft();
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.setPlanReady(draft, 'template:a', planResponse([]));
  Draft.addSavedAgent(draft, 'Research Scout');

  Draft.setPlanReady(
    draft,
    'template:b',
    planResponse([planAgent('Research Scout', { entry_point: true, action: 'reuse' })])
  );
  const view = Draft.derive(draft);

  assert.equal(view.roster.length, 1, 'one attachment, not two');
  assert.equal(view.roster[0].source, 'blueprint', 'the blueprint owns the roster entry');
  assert.deepEqual(view.shadowedSelections, [{ name: 'Research Scout', ownedBy: 'blueprint' }]);
  const notice = view.issues.find(issue => issue.id === 'shadowed-selection');
  assert.equal(notice.severity, 'advisory');
  assert.equal(view.payload.existing_agent_names, undefined, 'not sent twice');
  assert.equal(
    draft.savedSelections.length,
    1,
    'the selection is retained in the draft in case the blueprint changes again'
  );
});

test('roster rows expose the right per-source actions (FR53, FR54)', () => {
  const draft = readyDraft([planAgent('Blueprint Lead', { entry_point: true })]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.addSavedAgent(draft, 'Research Scout');
  const view = Draft.derive(draft);

  const blueprintRow = view.roster.find(entry => entry.source === 'blueprint');
  const savedRow = view.roster.find(entry => entry.source === 'saved');
  assert.equal(blueprintRow.customizable, true);
  assert.equal(
    blueprintRow.removable,
    false,
    'blueprint members have no individual removal (FR54)'
  );
  assert.equal(savedRow.removable, true);
  assert.equal(savedRow.canMakePrimary, true);
});

test('a resolved model label and inherited-model note are available for display (FR36, FR97)', () => {
  const draft = Draft.createDraft();
  Draft.setPlanReady(
    draft,
    'template:x',
    planResponse(
      [planAgent('Tuned', { model: 'gpt-5', provider: 'openai', model_source: 'template' })],
      {
        system_model_configured: true,
        system_provider: 'anthropic',
        system_model: 'claude-opus-5'
      }
    )
  );
  const view = Draft.derive(draft);

  assert.equal(view.roster[0].modelLabel, 'openai / gpt-5');
  assert.equal(view.roster[0].modelSourceLabel, 'Template model');
  assert.equal(
    view.inheritedModelNote,
    'Agents without their own model use anthropic / claude-opus-5.'
  );
  assert.ok(
    !view.issues.some(issue => issue.id === 'inherited-model'),
    'default model use is informational, never an issue (FR97)'
  );
});

test('reset discards every staged edit so a cancelled wizard persists nothing (FR13, FR67)', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true, action: 'create' })]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.addSavedAgent(draft, 'Research Scout');
  Draft.stageOverride(draft, 0, { name: 'Renamed Lead' });
  Draft.setExplicitPrimary(draft, 'Research Scout');
  Draft.setIncludeBlueprintTeam(draft, false);

  Draft.resetDraft(draft);

  assert.equal(draft.plan.status, 'idle');
  assert.equal(draft.plan.data, null);
  assert.equal(draft.overrides.size, 0);
  assert.deepEqual(draft.savedSelections, []);
  assert.equal(draft.explicitPrimary, '');
  assert.equal(draft.includeBlueprintTeam, true);
  assert.deepEqual(Draft.derive(draft).payload, {});
});

test('derive is pure: repeated calls neither mutate the draft nor differ', () => {
  const draft = readyDraft([
    planAgent('Lead', { entry_point: true, action: 'reuse' }),
    planAgent('Helper', { action: 'create' })
  ]);
  withSavedRoster(draft, [{ name: 'Research Scout' }]);
  Draft.addSavedAgent(draft, 'Research Scout');
  Draft.stageOverride(draft, 1, { model: '' });

  const before = JSON.stringify({
    plan: draft.plan,
    include: draft.includeBlueprintTeam,
    overrides: [...draft.overrides],
    saved: draft.savedSelections,
    primary: draft.explicitPrimary
  });
  const first = Draft.derive(draft);
  const second = Draft.derive(draft);
  const after = JSON.stringify({
    plan: draft.plan,
    include: draft.includeBlueprintTeam,
    overrides: [...draft.overrides],
    saved: draft.savedSelections,
    primary: draft.explicitPrimary
  });

  assert.equal(after, before, 'derive must not mutate the draft');
  assert.deepEqual(JSON.parse(JSON.stringify(first)), JSON.parse(JSON.stringify(second)));
});

test('a legacy request shape is preserved when no saved agent is selected (compatibility)', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true, action: 'reuse' })]);
  const payload = Draft.toCreatePayload(draft);

  assert.equal(payload.create_template_agents, true);
  assert.ok(
    !Object.prototype.hasOwnProperty.call(payload, 'existing_agent_names'),
    'omitted entirely so the server keeps its legacy entry-agent path'
  );
  assert.ok(!Object.prototype.hasOwnProperty.call(payload, 'entry_agent_name'));
});

test('create_template_agents is omitted when the blueprint declares no agents', () => {
  const draft = readyDraft([]);
  assert.deepEqual(Draft.toCreatePayload(draft), {});
});

test('the roster keeps a stable order as members are added and promoted', () => {
  const draft = readyDraft([
    planAgent('Lead', { entry_point: true, action: 'reuse' }),
    planAgent('Helper A'),
    planAgent('Helper B')
  ]);
  withSavedRoster(draft, [{ name: 'Scout' }, { name: 'Miner' }]);
  Draft.addSavedAgent(draft, 'Scout');
  Draft.addSavedAgent(draft, 'Miner');

  assert.deepEqual(
    Draft.derive(draft).roster.map(entry => entry.name),
    ['Lead', 'Helper A', 'Helper B', 'Scout', 'Miner'],
    'blueprint order first, then selection order'
  );

  // Promoting a saved agent moves only that entry; everything else holds its place.
  Draft.setExplicitPrimary(draft, 'Miner');
  assert.deepEqual(
    Draft.derive(draft).roster.map(entry => entry.name),
    ['Miner', 'Lead', 'Helper A', 'Helper B', 'Scout']
  );
});

test('a customized copy of a reused agent leaves the blueprint entry identifiable', () => {
  const draft = readyDraft([
    planAgent('Shared Lead', {
      entry_point: true,
      action: 'reuse',
      model: 'gpt-5',
      provider: 'openai'
    })
  ]);
  Draft.stageOverride(draft, 0, {
    name: 'Shared Lead Studio',
    model: 'claude-opus-5',
    provider: 'anthropic',
    systemPrompt: 'Be terse.',
    role: 'orchestrator'
  });
  const view = Draft.derive(draft);
  const row = view.roster[0];

  assert.equal(row.name, 'Shared Lead Studio');
  assert.equal(row.originalName, 'Shared Lead', 'the source definition stays identifiable');
  assert.equal(row.lifecycle, 'customized-copy');
  assert.equal(row.isCustomized, true);
  assert.equal(row.modelLabel, 'anthropic / claude-opus-5');
  assert.deepEqual(view.payload.template_agent_overrides, [
    {
      index: 0,
      name: 'Shared Lead Studio',
      model: 'claude-opus-5',
      provider: 'anthropic',
      system_prompt: 'Be terse.',
      role: 'orchestrator'
    }
  ]);
});

test('overrides serialize in index order regardless of the order they were staged', () => {
  const draft = readyDraft([
    planAgent('Zero', { entry_point: true }),
    planAgent('One'),
    planAgent('Two')
  ]);
  Draft.stageOverride(draft, 2, { name: 'Two Renamed' });
  Draft.stageOverride(draft, 0, { name: 'Zero Renamed' });
  Draft.stageOverride(draft, 1, { name: 'One Renamed' });

  assert.deepEqual(
    Draft.derive(draft).payload.template_agent_overrides.map(override => override.index),
    [0, 1, 2]
  );
});

test('excluding the blueprint team drops its overrides from the request but not the draft', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'Renamed Lead' });
  Draft.setIncludeBlueprintTeam(draft, false);

  const view = Draft.derive(draft);
  assert.equal(view.payload.create_template_agents, false);
  assert.equal(
    view.payload.template_agent_overrides,
    undefined,
    'no point sending overrides for a team that is not being created'
  );
  assert.equal(draft.overrides.size, 1, 'the staged edit survives so re-enabling restores it');

  Draft.setIncludeBlueprintTeam(draft, true);
  assert.deepEqual(Draft.derive(draft).payload.template_agent_overrides, [
    { index: 0, name: 'Renamed Lead' }
  ]);
});

test('an agent-less team is advisory and still produces a valid request', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true })]);
  Draft.setIncludeBlueprintTeam(draft, false);
  const view = Draft.derive(draft);

  assert.deepEqual(view.roster, []);
  assert.equal(view.primaryName, '');
  const empty = view.issues.find(issue => issue.id === 'empty-team');
  assert.equal(empty.severity, 'advisory');
  assert.match(empty.message, /may remain unassigned/);
  assert.equal(view.canContinueFromTeam, true, 'never blocks creation (FR55)');
  assert.equal(view.payload.create_template_agents, false);
});

test('a saved roster row carries the canonical appearance the shared renderer needs', () => {
  const draft = readyDraft([]);
  const appearance = {
    mode: 'character',
    generated: { color: '#3498db' },
    uploaded: { image: 'scout.png' },
    character: { catalog_id: 'sable', catalog_version: 2 }
  };
  withSavedRoster(draft, [
    {
      name: 'Research Scout',
      model: 'gpt-5',
      role: 'researcher',
      source: 'user',
      appearance
    }
  ]);
  Draft.addSavedAgent(draft, 'Research Scout');

  const entry = Draft.derive(draft).roster.find(row => row.name === 'Research Scout');
  // The canonical object is passed through whole. Unpacking it here would mean
  // this module gets its own opinion about which source wins (FR-81/FR-82).
  assert.deepEqual(entry.identity, {
    name: 'Research Scout',
    source: 'user',
    role: 'researcher',
    appearance,
    characterId: 'sable'
  });
});

test('a blueprint agent that does not exist yet gets a name-seeded identity', () => {
  const draft = readyDraft([planAgent('Brand New', { entry_point: true })]);

  const entry = Draft.derive(draft).roster.find(row => row.name === 'Brand New');
  assert.equal(entry.identity.name, 'Brand New');
  assert.equal(entry.identity.characterId, '', 'no character until the agent exists');
  // No appearance at all resolves to Generated in the renderer, which is
  // exactly what the agent will look like once it is created (FR-13).
  assert.equal(entry.identity.appearance, null);
});

test('an unrenamed blueprint row shows the saved agent it actually reuses (FR41)', () => {
  const draft = readyDraft([planAgent('File Curator', { action: 'reuse', entry_point: true })]);
  withSavedRoster(draft, [
    {
      name: 'File Curator',
      appearance: { mode: 'character', character: { catalog_id: 'pebble', catalog_version: 1 } }
    }
  ]);

  const entry = Draft.derive(draft).roster.find(row => row.name === 'File Curator');
  assert.equal(entry.identity.characterId, 'pebble');
  assert.equal(entry.identity.appearance.mode, 'character');
});

test('renaming a blueprint agent re-seeds its identity to the new name', () => {
  const draft = readyDraft([planAgent('Lead', { entry_point: true })]);
  Draft.stageOverride(draft, 0, { name: 'Renamed Lead' });

  const entry = Draft.derive(draft).roster.find(row => row.name === 'Renamed Lead');
  assert.equal(entry.identity.name, 'Renamed Lead');
});

test('identityFrom reads no retired avatar/character metadata', () => {
  // A record still carrying the retired fields contributes nothing: this module
  // has no legacy adapter, so the renderer shows Generated rather than
  // resurrecting the old inference rule (FR-81).
  const legacy = Draft.identityFrom('Legacy', {
    metadata: { avatar_image: 'legacy.png', avatar_color: '#123456' }
  });
  assert.equal(legacy.appearance, null);
  assert.equal(legacy.characterId, '');

  const cli = Draft.identityFrom('Claude Code', { source: 'CLI' });
  assert.equal(cli.source, 'cli', 'source is normalized for the renderer');
  assert.deepEqual(Draft.identityFrom('Bare', null).name, 'Bare');
});

test('issue ordering puts blockers ahead of advisories', () => {
  const draft = Draft.createDraft();
  Draft.setPlanError(draft, 'template:x', 'plan unavailable');
  Draft.setSavedRosterError(draft, 'roster unavailable');
  const view = Draft.derive(draft);

  assert.equal(view.blockingIssues.length, 1);
  assert.equal(view.blockingIssues[0].id, 'plan-error');
  assert.ok(view.advisoryIssues.some(issue => issue.id === 'saved-roster-error'));
  assert.equal(view.canContinueFromTeam, false, 'a blocker stops Team continuation');
});
