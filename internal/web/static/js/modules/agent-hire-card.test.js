import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const read = name => readFileSync(new URL(name, import.meta.url), 'utf8');

function loadHire() {
  const context = { console };
  context.globalThis = context;
  vm.runInNewContext(read('./agent-hire-card.js'), context);
  return context.OriAgentHire;
}

const hire = loadHire();

// The catalog as GET /api/agents/catalog serves it (agentcatalog + hireCatalog).
const catalog = {
  entries: [
    {
      slug: 'orchestrator',
      display_name: 'Commander',
      tagline: 'Routes work',
      emblem: 'compass',
      accent_color: '#111'
    },
    {
      slug: 'researcher',
      display_name: 'Researcher',
      tagline: 'Finds things',
      emblem: 'search',
      accent_color: '#222'
    },
    {
      slug: 'validator',
      display_name: 'Reviewer',
      tagline: 'Checks work',
      emblem: 'check',
      accent_color: '#333'
    },
    {
      slug: 'specialist',
      display_name: 'Specialist',
      tagline: 'Knows a domain',
      emblem: 'star',
      accent_color: '#444',
      supports_domain: true
    }
  ],
  hire: {
    roles: {
      orchestrator: {
        default_brief: 'Take requests for this workspace and route them to the right work.',
        blueprint: 'blank',
        blueprint_label: 'Blank',
        open_roles: 0
      },
      researcher: {
        default_brief: "Find and summarize what's known about this topic, with sources.",
        blueprint: 'blank',
        blueprint_label: 'Blank',
        open_roles: 0
      },
      validator: {
        default_brief: 'Check the work here for mistakes and flag problems before they ship.',
        blueprint: 'blank',
        blueprint_label: 'Blank',
        open_roles: 0
      },
      specialist: {
        default_brief: 'Act as the expert this workspace needs.',
        blueprint: 'blank',
        blueprint_label: 'Blank',
        open_roles: 0
      }
    },
    domain_brief: 'Act as the {domain} expert for this workspace.',
    brief_max: 300,
    workspace_name_max: 80
  }
};

const plain = value => JSON.parse(JSON.stringify(value));

test('the workspace name is derived from the brief exactly as the server derives it', () => {
  // The same cases as personalassistant.TestHireWorkspaceName_TitleCasesTheBriefsFirstWords.
  const cases = {
    "Compare the three note-taking apps I'm considering and recommend one":
      'Compare the Three Note-taking Apps',
    "Find and summarize what's known about this topic, with sources.":
      "Find and Summarize What's Known",
    'Check the work here for mistakes and flag problems before they ship.': 'Check the Work Here',
    'Take requests for this workspace and route them to the right work.':
      'Take Requests for This Workspace',
    'Act as the mixing expert for this workspace.': 'Act as the Mixing Expert',
    '   "review   the API docs" ': 'Review the API Docs',
    '…': '',
    '': '',
    'the and of': '',
    'Écrire un résumé net': 'Écrire Un Résumé Net'
  };
  for (const [brief, want] of Object.entries(cases)) {
    assert.equal(hire.hireWorkspaceName(brief), want, brief);
  }
  const long = 'Supercalifragilisticexpialidocious '.repeat(5);
  assert.ok(Array.from(hire.hireWorkspaceName(long)).length <= 80);
});

test('Hire is offered only once a personal assistant is hired', () => {
  for (const state of ['needs_hq', 'provisioning_hq', 'active', 'paused']) {
    assert.equal(hire.assistantAllowsHire({ state }), true, state);
    assert.equal(hire.assistantAllowsHire(state), true, state);
  }
  for (const state of ['needs_hire', 'hiring', 'repair_needed', '', undefined]) {
    assert.equal(hire.assistantAllowsHire({ state }), false, String(state));
  }
  assert.equal(hire.assistantAllowsHire(null), false);
});

test('each role starts from its own default brief, and a Specialist names its domain', () => {
  const [commander, researcher, reviewer, specialist] = catalog.entries;
  assert.equal(
    hire.defaultBriefFor(catalog, commander, ''),
    'Take requests for this workspace and route them to the right work.'
  );
  assert.equal(
    hire.defaultBriefFor(catalog, researcher, 'ignored'),
    "Find and summarize what's known about this topic, with sources."
  );
  assert.equal(
    hire.defaultBriefFor(catalog, reviewer, ''),
    'Check the work here for mistakes and flag problems before they ship.'
  );
  assert.equal(
    hire.defaultBriefFor(catalog, specialist, ''),
    'Act as the expert this workspace needs.'
  );
  assert.equal(
    hire.defaultBriefFor(catalog, specialist, '  music   mixing '),
    'Act as the music mixing expert for this workspace.'
  );
});

test('the view lists the roles, shows the domain only for a Specialist, and says what Hire and start does', () => {
  const state = {
    role: 'validator',
    name: 'Rita',
    brief: 'Check the release notes.',
    workspaceName: 'Release Check',
    phase: 'idle'
  };
  const view = plain(hire.hireCardView(catalog, state));
  assert.deepEqual(
    view.roles.map(role => [role.slug, role.label, role.selected]),
    [
      ['orchestrator', 'Commander', false],
      ['researcher', 'Researcher', false],
      ['validator', 'Reviewer', true],
      ['specialist', 'Specialist', false]
    ]
  );
  assert.equal(view.showDomain, false);
  assert.deepEqual(view.lines, [
    { kind: 'agents', name: 'Hire Rita (Reviewer)' },
    {
      kind: 'workspace',
      name: 'A Blank workspace named Release Check',
      detail: 'Goal: Check the release notes.'
    },
    { kind: 'task', name: 'First look: read the brief and suggest the first task' }
  ]);
  assert.equal(view.canSubmit, true);
  assert.equal(view.buttonLabel, 'Hire and start');

  assert.equal(hire.hireCardView(catalog, { ...state, role: 'specialist' }).showDomain, true);
});

test('a blueprint with other roles says they stay open', () => {
  const withRoles = plain(catalog);
  withRoles.hire.roles.researcher = {
    ...withRoles.hire.roles.researcher,
    blueprint: 'research-project',
    blueprint_label: 'Research Project',
    open_roles: 2
  };
  const view = plain(
    hire.hireCardView(withRoles, {
      role: 'researcher',
      name: 'Rosa',
      brief: 'Compare note apps.',
      workspaceName: 'Compare Note Apps',
      phase: 'idle'
    })
  );
  assert.deepEqual(
    view.lines.map(line => line.name),
    [
      'Hire Rosa (Researcher)',
      'A Research Project workspace named Compare Note Apps',
      'Other roles stay open to fill later',
      'First look: read the brief and suggest the first task'
    ]
  );
});

test('the view refuses a card it cannot send and says why', () => {
  const base = {
    role: 'validator',
    name: 'Rita',
    brief: 'Check it.',
    workspaceName: 'Check It',
    phase: 'idle'
  };
  assert.deepEqual(plain(hire.hireCardView(catalog, { ...base, role: '' }).problems), [
    'Choose a role.'
  ]);
  assert.deepEqual(plain(hire.hireCardView(catalog, { ...base, name: '  ' }).problems), [
    'Give them a name.'
  ]);
  assert.match(
    hire.hireCardView(catalog, { ...base, name: 'Rita/..' }).problems[0],
    /letters, numbers/
  );
  assert.match(
    hire.hireCardView(catalog, { ...base, brief: 'x'.repeat(301) }).problems[0],
    /300 characters/
  );
  assert.match(
    hire.hireCardView(catalog, { ...base, workspaceName: 'n'.repeat(81) }).problems[0],
    /80 characters/
  );
  for (const view of [hire.hireCardView(catalog, { ...base, role: '' })])
    assert.equal(view.canSubmit, false);
});

test('progress is shown line by line: working, done, or stopped at the failed step', () => {
  const lines = [
    { kind: 'agents', name: 'Hire Rita (Reviewer)' },
    { kind: 'workspace', name: 'A Blank workspace named X' },
    { kind: 'task', name: 'First look' }
  ];
  const states = (phase, step) => hire.progressLines(lines, phase, step).map(line => line.state);
  assert.deepEqual(states('running'), ['working', 'waiting', 'waiting']);
  assert.deepEqual(states('done'), ['done', 'done', 'done']);
  assert.deepEqual(states('failed', 'agent'), ['failed', 'waiting', 'waiting']);
  assert.deepEqual(states('failed', 'workspace'), ['done', 'failed', 'waiting']);
  assert.deepEqual(states('failed', 'record'), ['done', 'done', 'failed']);
  // A refusal before any step leaves the lines unmarked.
  assert.deepEqual(states('failed', ''), [undefined, undefined, undefined]);
  assert.deepEqual(states('idle'), [undefined, undefined, undefined]);

  const running = hire.hireCardView(catalog, {
    role: 'validator',
    name: 'Rita',
    brief: 'b',
    workspaceName: 'w',
    phase: 'running'
  });
  assert.equal(running.busy, true);
  assert.equal(running.canSubmit, false);
  const failed = hire.hireCardView(catalog, {
    role: 'validator',
    name: 'Rita',
    brief: 'b',
    workspaceName: 'w',
    phase: 'failed',
    failedStep: 'workspace',
    error: 'Could not create the workspace: disk full'
  });
  assert.equal(failed.buttonLabel, 'Try again');
  assert.equal(failed.error, 'Could not create the workspace: disk full');
  assert.equal(failed.canSubmit, true);
});

test('Hire and start sends one request with the card and keeps the request id for Try again', () => {
  const state = {
    requestId: 'hire-1',
    role: 'specialist',
    name: ' Mix  Master ',
    domain: ' mixing ',
    brief: ' Balance the stems. ',
    workspaceName: ' Stem Balance '
  };
  assert.deepEqual(plain(hire.hireRequestBody(state, { mode: 'generated' })), {
    request_id: 'hire-1',
    role: 'specialist',
    name: 'Mix Master',
    brief: 'Balance the stems.',
    workspace_name: 'Stem Balance',
    domain: 'mixing',
    appearance: { mode: 'generated' }
  });
  assert.equal(
    hire.hiredMessage({ agent_name: 'Rita' }),
    'Hired. Rita is taking a first look; your assistant will report back.'
  );
});

test('a suggested name avoids the names already taken', () => {
  assert.equal(hire.suggestAgentName('Reviewer', []), 'Reviewer');
  assert.equal(hire.suggestAgentName('Reviewer', ['reviewer', 'Reviewer 2']), 'Reviewer 3');
});

test('the roster modal and the create page mount the same card and send the same request', () => {
  const agents = read('./agents.js');
  const page = read('./agents-catalog.js');
  const roster = read('../agents-roster.js');
  for (const source of [agents, page]) {
    assert.match(source, /OriAgentHire/);
    assert.match(source, /mountHireCard\(/);
    assert.match(source, /assistantAllowsHire\(/);
  }
  // The page no longer creates a bare agent from the catalog.
  assert.doesNotMatch(page, /catalog_role/);
  assert.doesNotMatch(page, /fetch\('\/api\/agents',/);
  // The roster asks for Hire with the relationship it already read, and still
  // announces a standard create as before.
  assert.match(roster, /hire: \{\s*assistant: assistant\.known \? assistant\.state : undefined/);
  assert.match(roster, /'ori:agent-create-opened', \{ detail: \{ mode: 'standard' \} \}/);
  assert.match(roster, /addEventListener\('ori:agent-hired'/);
});

test('the modal markup offers Hire and Advanced tabs, hidden until a page asks for them', () => {
  const modals = read('../../../templates/components/modals.tmpl');
  assert.match(modals, /id="agentCreateTabs"[^>]*hidden/);
  assert.match(modals, /id="agentCreateHireTab"[^>]*>Hire</);
  assert.match(
    modals,
    /id="agentCreateAdvancedTab"[^>]*aria-controls="addAgentForm"[^>]*>Advanced</
  );
  assert.match(modals, /id="hireAgentBtn"[^>]*hidden>Hire and start</);
  // Advanced keeps today's label.
  assert.match(modals, /Create Agent\s*<\/button>/);
  const roster = read('../../../templates/pages/agents-roster.tmpl');
  assert.match(roster, /id="newAgentBtn"[\s\S]*?<\/span> Hire\s*<\/button>/);
  assert.match(roster, /agent-hire-card\.js/);
});
