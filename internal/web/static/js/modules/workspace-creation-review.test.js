import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import vm from 'node:vm';
import {
  workspaceCreationReviewHTML,
  escapeReviewText,
  creationTeamReviewRows,
  creationReviewSlotsHTML,
  creationReviewNavigationHTML,
  setupCreationReviewMaterial
} from './workspace-creation-review.js';
import { placementPanelHTML } from './assisted-workspace-placement.js';

const blank = {
  ownerKind: 'ordinary',
  name: 'My notebook',
  goal: 'Keep notes',
  destination: 'Standalone workspace · Position does not join a district.',
  rows: [
    {
      label: 'Team',
      value: 'No agents. You can add teammates later.',
      action: 'team',
      actionLabel: 'Edit team'
    }
  ],
  facts: [
    { label: 'Now', value: 'Create one workspace. No tasks run.' },
    { label: 'Later', value: 'Add notes, files or teammates when you need them.' }
  ],
  reviewed: true,
  canPlace: true,
  canConfirm: true,
  confirmLabel: 'Create workspace',
  mapHint:
    'Click a valid position or press Enter to create this workspace. Escape returns to review.'
};

function normalizedRoleRows(roles) {
  const window = {};
  vm.runInNewContext(readFileSync(new URL('./workspace-role-roster.js', import.meta.url), 'utf8'), {
    window
  });
  return window.WorkspaceRoleRoster.rowsFrom({ roles });
}

test('team projection preserves canonical required, optional, auto-filled, assigned and shared states without edits', () => {
  const roles = [
    {
      role_id: 'lead',
      label: 'Lead',
      primary: true,
      required: true,
      scope: 'project',
      state: 'filled',
      source: 'assigned',
      agent: { name: 'Saved lead', model: 'saved-model' }
    },
    {
      role_id: 'auto',
      label: 'Helper',
      required: true,
      scope: 'project',
      state: 'filled',
      source: 'auto',
      agent: { name: 'New helper' }
    },
    { role_id: 'optional', label: 'Editor', required: false, scope: 'project', state: 'empty' },
    {
      role_id: 'shared',
      label: 'Shared coordinator',
      scope: 'home',
      required: true,
      state: 'filled',
      source: 'shared',
      read_only: true,
      read_only_reason: 'Home roster is managed separately.',
      agent: { name: 'Home member' }
    }
  ];
  const rows = normalizedRoleRows(roles);
  const view = { roleRoster: { unassigned: [{ name: 'Extra teammate' }] } };
  const before = JSON.stringify({ roles, rows, view });
  const projected = creationTeamReviewRows(view, rows);
  const html = workspaceCreationReviewHTML({ ...blank, rows: projected });
  for (const value of [
    'Saved lead',
    'saved-model',
    'Your saved agent',
    'No agent assigned',
    'Created for you',
    'Required',
    'Optional',
    'Already on this Home',
    'Home roster is managed separately.',
    'Extra teammate'
  ])
    assert.ok(html.includes(value), value);
  assert.equal(projected.find(row => row.label === 'Shared coordinator').action, undefined);
  assert.equal(
    JSON.stringify({ roles, rows, view }),
    before,
    'projection cannot mutate global profiles or canonical role choices'
  );
  const projectOnly = creationTeamReviewRows({ ...view, isAssistantProgram: true }, rows);
  assert.equal(
    projectOnly.some(row => row.label === 'Shared coordinator'),
    false,
    'existing Home holders are not a project staffing side effect'
  );
});

test('team projection retains same-name rows and truthfully distinguishes loading from a settled zero-role roster', () => {
  const rows = normalizedRoleRows([
    {
      role_id: 'first',
      label: 'First seat',
      state: 'filled',
      source: 'assigned',
      agent: { name: 'Same profile' }
    },
    {
      role_id: 'second',
      label: 'Second seat',
      state: 'filled',
      source: 'assigned',
      agent: { name: 'Same profile' }
    }
  ]);
  assert.equal(creationTeamReviewRows({}, rows).length, 2);
  assert.match(creationTeamReviewRows({ planStatus: 'loading' })[0].value, /not yet reviewed/);
  assert.match(creationTeamReviewRows({ planStatus: 'ready' })[0].value, /No agents/);
  assert.match(creationTeamReviewRows({ agentless: true }, rows)[0].value, /No agents/);
});

const starterRoot = new URL('../../../../projecttemplates/starter/', import.meta.url);
for (const entry of readdirSync(starterRoot, { withFileTypes: true }).filter(entry =>
  entry.isDirectory()
)) {
  test(`built-in declaration projects every actual team name as inert review text: ${entry.name}`, () => {
    const template = JSON.parse(
      readFileSync(new URL(`${entry.name}/template.json`, starterRoot), 'utf8')
    );
    // Rendering-only declaration coverage, not a fabricated server plan or
    // domain creation. Real normalized staffing/creation has separate tests.
    const view = {
      planStatus: 'ready',
      roster: (template.agents || []).map(agent => ({
        name: agent.name,
        lifecycle: 'create',
        modelLabel: 'Reviewed model'
      }))
    };
    const before = JSON.stringify(view);
    const html = workspaceCreationReviewHTML({ ...blank, rows: creationTeamReviewRows(view) });
    for (const agent of template.agents || [])
      assert.ok(html.includes(escapeReviewText(agent.name)), agent.name);
    assert.equal(JSON.stringify(view), before);
    assert.doesNotMatch(html, /data-placement-field="monitoring"|Filed\//);
  });
}

test('Blank uses the same review language without Janitor permissions, slots or inferred actions', () => {
  const html = placementPanelHTML(blank);
  for (const label of [
    'Make room for My notebook',
    'Workspace name',
    'Goal',
    'No agents',
    'Standalone workspace',
    'Before you confirm',
    'No tasks run.',
    'Use automatic placement',
    'Place &amp; create on map'
  ])
    assert.ok(html.includes(label), label);
  assert.doesNotMatch(
    html,
    /File Curator|scan|monitoring|Filed\/|Choose folder|data-placement-slot=/i
  );
  assert.doesNotMatch(html, /data-placement-action="confirm"|Use selected/);
  assert.equal(html, workspaceCreationReviewHTML(blank));
});

test('ordinary field bounds come from their existing owner and reviewed rows are never silently dropped', () => {
  const html = workspaceCreationReviewHTML({
    ...blank,
    nameMaxLength: 80,
    goalMaxLength: 5000,
    rows: Array.from({ length: 70 }, (_, index) => ({
      label: `Declared seat ${index}`,
      value: 'Reviewed choice'
    }))
  });
  assert.match(html, /data-placement-field="name" maxlength="80"/);
  assert.match(html, /data-placement-field="mission" rows="2" maxlength="5000"/);
  assert.match(html, /Declared seat 69/);
  assert.doesNotMatch(
    workspaceCreationReviewHTML(blank),
    /maxlength=/,
    'do not impose File Janitor bounds on another owner'
  );
});

test('automatic selection exposes exactly one explicit action and never implies a completed operation', () => {
  const html = workspaceCreationReviewHTML({ ...blank, automatic: true });
  assert.match(html, /data-placement-action="confirm">Create workspace</);
  assert.match(html, /Automatic placement selected. Confirm below/);
  assert.equal((html.match(/data-placement-action="confirm"/g) || []).length, 1);
  assert.doesNotMatch(html, /Workspace created/);
});

test('absent review, pending edits, unavailable maps and conflicts cannot offer an enabled final gesture', () => {
  for (const state of [
    { reviewed: false },
    { dirty: true },
    { busy: true },
    { conflict: true },
    { closed: true }
  ]) {
    const html = workspaceCreationReviewHTML({ ...blank, automatic: true, ...state });
    assert.match(html, /data-placement-action="confirm" disabled/);
    assert.match(html, /data-placement-action="place" disabled/);
  }
  const html = workspaceCreationReviewHTML({ ...blank, automatic: true, canPlace: false });
  assert.match(html, /data-placement-action="place" disabled/);
  assert.match(html, /data-placement-action="confirm">Create workspace</);
});

test('teams and resources are declaration-driven text rows, not a fixed one-agent recipe', () => {
  const rows = [
    { label: 'Research lead', value: 'Ada · saved agent · required' },
    { label: 'Editor', value: 'Lin · new agent · optional' },
    { label: 'Coordinator', value: 'Shared Home role · host-filled' },
    { label: 'Repository', value: 'Reviewed link', detail: 'https://example.invalid/project' }
  ];
  const html = workspaceCreationReviewHTML({ ...blank, rows });
  for (const row of rows) assert.ok(html.includes(row.label) && html.includes(row.value));
  assert.equal((html.match(/class="assisted-placement__slot"/g) || []).length, 4);
  assert.doesNotMatch(html, /File Curator|scan|monitoring/i);
  assert.doesNotMatch(creationReviewSlotsHTML([]), /<section/);
});

test('untrusted names, resources, labels and action copy are escaped, not executable plans or markup', () => {
  const hostile = '<img src=x onerror="alert(1)">';
  const html = workspaceCreationReviewHTML({
    ...blank,
    name: hostile,
    goal: hostile,
    destination: hostile,
    mapHint: hostile,
    message: hostile,
    rows: [
      { label: hostile, value: hostile, detail: hostile, action: hostile, actionLabel: hostile }
    ],
    facts: [{ label: hostile, value: hostile }]
  });
  assert.doesNotMatch(html, /<img|onerror="alert/);
  assert.match(html, /&lt;img/);
  assert.match(html, /&quot;/);
});

test('a proven result can disclose later setup without claiming readiness or offering automatic execution', () => {
  const html = workspaceCreationReviewHTML({
    ...blank,
    result: { name: 'Calendar workspace' },
    nextSetup: 'Connect <calendar>',
    accepted: true
  });
  assert.match(html, /Next setup:/);
  assert.match(html, /Connect &lt;calendar&gt;/);
  assert.match(html, /setup readiness has not been verified/);
  assert.doesNotMatch(html, /data-placement-action="(?:connect|start|scan|setup)"/);
  const uncertain = workspaceCreationReviewHTML({
    ...blank,
    accepted: true,
    nextSetup: 'Unproven setup'
  });
  assert.doesNotMatch(uncertain, /Next setup:|Unproven setup/);
});

test('the map has no competing editor; accepted progress and separate position results remain readable', () => {
  assert.equal(workspaceCreationReviewHTML({ ...blank, onMap: true }), '');
  const pending = workspaceCreationReviewHTML({ ...blank, onMap: true, accepted: true });
  assert.match(pending, /Reading status never continues creation/);
  assert.match(pending, /Read saved status/);
  assert.doesNotMatch(
    pending,
    /Cancel draft|data-placement-field=|data-placement-action="confirm"/
  );
  const result = workspaceCreationReviewHTML({
    ...blank,
    result: { name: 'Actual notebook' },
    positionFailed: true
  });
  assert.match(result, /Workspace created/);
  assert.match(result, /Actual notebook/);
  assert.match(result, /Retry position only/);
  assert.doesNotMatch(result, /data-placement-action="confirm"/);
});

test('result navigation waits for the accepted owner to finish instead of swallowing an early Open click', () => {
  const result = { id: 'proven', name: 'Notebook' };
  const busy = workspaceCreationReviewHTML({ result, accepted: true, busy: true });
  const ready = workspaceCreationReviewHTML({ result, accepted: true, busy: false });
  assert.match(busy, /data-placement-action="open" disabled/);
  assert.doesNotMatch(ready, /data-placement-action="open" disabled/);
  assert.match(ready, /data-placement-action="open">Open workspace/);
});

test('recognized setup projects fixed plan roles and optional first-task boundaries without borrowing ordinary authority', () => {
  const session = {
    plan: {
      hire: {
        mode: 'bind',
        agent_name: 'Saved Developer',
        role_label: 'Development Assistant',
        model: 'local'
      },
      first_task_deferred: true,
      lines: [
        { kind: 'workspace', name: 'Create the workspace', detail: 'Only the reviewed project' }
      ]
    },
    folder: { observation: { id: 'held', folder: '<project>' } },
    folder_path: '/display-only/project'
  };
  const material = setupCreationReviewMaterial(session, 'Exact brief 日本語');
  assert.equal(material.rows[0].label, 'Development Assistant');
  assert.match(material.rows[0].value, /Saved assignment/);
  assert.ok(material.rows.every(row => !row.action));
  assert.match(material.later, /prepared, not started/);
  assert.deepEqual(material.facts, [
    { label: 'Workspace', value: 'Create the workspace — Only the reviewed project' }
  ]);
  assert.match(material.destination, /Standalone/);
  const html = workspaceCreationReviewHTML({
    ...blank,
    ...material,
    idPrefix: 'sourceSetup',
    canChangeBlueprint: false,
    canEditName: false,
    detailsLabel: 'Review the full plan'
  });
  assert.match(html, /id="sourceSetupName"/);
  assert.match(html, /readonly/);
  assert.match(html, /&lt;project&gt;/);
  assert.doesNotMatch(html, /data-placement-action="blueprint"|File Curator|metadata scan/);
  assert.match(html, /Review the full plan/);
});

test('the navigation shared with File Janitor preserves its distinct disclosure without baking it into Blank', () => {
  const html = creationReviewNavigationHTML({
    ...blank,
    mapHint: 'Click creates the workspace and scans.'
  });
  assert.match(html, /Click creates the workspace and scans/);
  assert.doesNotMatch(creationReviewNavigationHTML(blank), /scan/);
  const janitor = placementPanelHTML({
    draft: { name: 'Inbox' },
    team: [],
    folder: {},
    review: {},
    canPlace: true
  });
  for (const css of [
    'assisted-placement__header',
    'assisted-placement__slots',
    'assisted-placement__navigation'
  ]) {
    assert.ok(janitor.includes(css) && workspaceCreationReviewHTML(blank).includes(css));
  }
  assert.match(janitor, /aria-label="File Curator role"/);
  assert.match(janitor, /Prepare a filing review/);
});
