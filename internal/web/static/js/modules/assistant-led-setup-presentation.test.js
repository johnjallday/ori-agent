import test from 'node:test';
import assert from 'node:assert/strict';

import {
  announcementFor,
  assistantSetupMilestoneViews,
  availablePanelCount,
  buildPanels,
  createStepScheduler,
  entryFacts,
  modeLine,
  presentationMode,
  receiptTime,
  requestedMilestones,
  stopMessage,
  windowSpec
} from './assistant-led-setup-presentation.js';

const RECORDED = '2026-09-21T10:00:00Z';

function receipted(overrides = {}) {
  return [
    {
      id: 'workspace',
      kind: 'workspace',
      name: 'File Janitor',
      action: 'create',
      status: 'created',
      ownership: 'created',
      resource_id: 'workspace-1',
      recorded_at: RECORDED,
      blueprint_id: 'file-janitor',
      blueprint_version: 2,
      ...overrides.workspace
    },
    {
      id: 'role:file-curator',
      kind: 'agent_role',
      name: 'File Curator',
      role_id: 'file-curator',
      action: 'create',
      status: 'created',
      ownership: 'created',
      resource_id: 'instance-1',
      recorded_at: RECORDED,
      needs_model: true,
      ...overrides.role
    }
  ];
}

test('mode selection: nothing reported is requested, in-flight is live, receipts are a walkthrough', () => {
  assert.equal(presentationMode(undefined), 'requested');
  assert.equal(presentationMode([]), 'requested');
  assert.equal(presentationMode(requestedMilestones({ mode: 'create', team: [] })), 'requested');
  assert.equal(presentationMode(receipted()), 'walkthrough');
  assert.equal(presentationMode(receipted({ role: { status: 'creating' } })), 'live');
  assert.equal(presentationMode(receipted({ role: { status: 'reusing' } })), 'live');
  assert.equal(presentationMode(receipted({ role: { status: 'needs_review' } })), 'stopped');
  assert.equal(presentationMode(receipted({ role: { status: 'failed' } })), 'stopped');
  assert.equal(presentationMode(receipted({ role: { status: 'never-heard-of-it' } })), 'stopped');
});

test('a receipted run never reads as Creating in any label or announcement', () => {
  const milestones = receipted();
  const mode = presentationMode(milestones);
  const panels = buildPanels(milestones);
  assert.equal(mode, 'walkthrough');
  assert.match(modeLine(mode, milestones), /^Receipt walkthrough · recorded /);
  assert.doesNotMatch(announcementFor(mode, panels), /Creating|Reusing|Waiting/);
  for (const panel of panels) assert.match(panel.status, /^(created|reused)$/);
});

test('requested milestones show reviewed values as pending, never as done', () => {
  const milestones = requestedMilestones({
    mode: 'create',
    blueprint_id: 'file-janitor',
    blueprint_version: 2,
    team: [
      { role_id: 'file-curator', name: 'File Curator', action: 'create', model_configured: false }
    ]
  });
  assert.deepEqual(
    milestones.map(milestone => [milestone.id, milestone.status]),
    [
      ['workspace', 'pending'],
      ['role:file-curator', 'pending']
    ]
  );
  const adopt = requestedMilestones({ mode: 'adopt' });
  assert.deepEqual(
    adopt.map(milestone => [milestone.id, milestone.action]),
    [
      ['workspace', 'reuse'],
      ['existing_team', 'reuse']
    ]
  );
  assert.deepEqual(requestedMilestones(null), []);
});

test('panels are list-driven for several roles and title create versus reuse', () => {
  const several = [
    ...receipted(),
    {
      id: 'role:second',
      kind: 'agent_role',
      name: 'Second',
      role_id: 'second',
      action: 'reuse',
      status: 'reused',
      ownership: 'adopted',
      resource_id: 'instance-2',
      recorded_at: RECORDED
    }
  ];
  const panels = buildPanels(several);
  assert.deepEqual(
    panels.map(panel => panel.id),
    ['workspace', 'agents', 'receipt']
  );
  assert.equal(panels[0].title, 'Create Workspace');
  assert.equal(panels[1].title, 'Create Agents');
  assert.equal(panels[1].entries.length, 2);
  assert.equal(panels[2].entries.length, 3);

  const reuseOnly = buildPanels([
    {
      id: 'workspace',
      kind: 'workspace',
      action: 'create',
      status: 'created',
      ownership: 'created'
    },
    { id: 'role:a', kind: 'agent_role', action: 'reuse', status: 'reused', ownership: 'adopted' }
  ]);
  assert.equal(reuseOnly[1].title, 'Reuse Agent');
  assert.equal(reuseOnly[1].status, 'reused');

  const adopted = buildPanels([
    { id: 'workspace', kind: 'workspace', action: 'reuse', status: 'reused', ownership: 'adopted' },
    {
      id: 'existing_team',
      kind: 'existing_team',
      action: 'reuse',
      status: 'reused',
      ownership: 'adopted'
    }
  ]);
  assert.equal(adopted[0].title, 'Reuse Workspace');
  assert.equal(adopted[1].title, 'Existing team');
});

test('a panel opens only after its own step is reported; walkthrough opens all', () => {
  const pending = requestedMilestones({
    mode: 'create',
    team: [{ role_id: 'a', name: 'A', action: 'create' }]
  });
  assert.equal(availablePanelCount('requested', buildPanels(pending)), 1);
  assert.equal(availablePanelCount('walkthrough', buildPanels(receipted())), 3);
  const workspaceOnly = [
    { id: 'workspace', kind: 'workspace', status: 'needs_review' },
    { id: 'role:a', kind: 'agent_role', status: 'pending' }
  ];
  assert.equal(availablePanelCount('stopped', buildPanels(workspaceOnly)), 1);
});

test('a stopped walkthrough ends on the step that stopped and keeps earlier receipts', () => {
  const stoppedAtRole = receipted({
    role: {
      status: 'needs_review',
      resource_id: '',
      ownership: '',
      recorded_at: '',
      error_code: 'agent_not_attached'
    }
  });
  const mode = presentationMode(stoppedAtRole);
  const panels = buildPanels(stoppedAtRole);
  assert.equal(mode, 'stopped');
  // Create Workspace (receipted) and Create Agent (stopped) are reachable; the
  // walkthrough ends on the stopped panel instead of running past it.
  assert.equal(availablePanelCount(mode, panels), 2);
  assert.equal(panels[0].status, 'created');
  assert.equal(panels[0].entries[0].resourceID, 'workspace-1');
  assert.equal(panels[1].status, 'needs_review');
  assert.equal(panels[1].entries[0].resourceID, '', 'an unproven role has no id');

  const facts = Object.fromEntries(entryFacts(panels[1].entries[0]));
  assert.equal(facts['Reason code'], 'agent_not_attached');
  assert.equal(
    facts['What happened'],
    'The File Curator profile exists but is not attached to the workspace.'
  );
  const reachable = panels.slice(0, availablePanelCount(mode, panels));
  assert.equal(
    announcementFor(mode, reachable),
    'Stopped — needs your attention. Create Workspace: Created. Create Agent: Needs review'
  );

  const failedFirst = [
    {
      id: 'workspace',
      kind: 'workspace',
      status: 'failed',
      error_code: 'agent_root_unavailable'
    },
    { id: 'role:file-curator', kind: 'agent_role', status: 'pending' }
  ];
  assert.equal(availablePanelCount('stopped', buildPanels(failedFirst)), 1);
});

test('every server stop code has a plain explanation and unknown codes stay neutral', () => {
  const neutral = 'Ori stopped at this step, so it will not retry it for you.';
  for (const code of [
    'agent_root_unavailable',
    'team_plan_changed',
    'preparation_interrupted',
    'workspace_outcome_unresolved',
    'agent_not_attached'
  ]) {
    assert.notEqual(stopMessage(code), neutral, code);
  }
  for (const code of ['', 'constructor', '__proto__', 'made_up']) {
    assert.equal(stopMessage(code), neutral);
  }
});

test('windows show only reviewed facts: no path, prompt, digest, or input', () => {
  const views = assistantSetupMilestoneViews(receipted());
  const workspace = windowSpec(views[0], [views[1]]);
  assert.equal(workspace.title, 'Create Workspace');
  assert.deepEqual(workspace.steps, ['Blueprint', 'Details', 'Team', 'Review']);
  assert.deepEqual(
    workspace.fields.map(([label]) => label),
    ['Blueprint', 'Name', 'Team', 'Location']
  );
  assert.equal(workspace.fields[0][1], 'file-janitor v2');
  assert.equal(workspace.fields[2][1], 'Create File Curator');
  assert.equal(workspace.button, 'Create Workspace');

  const agent = windowSpec(views[1]);
  assert.equal(agent.title, 'Create Agent');
  assert.deepEqual(
    agent.fields.map(([label]) => label),
    ['Name', 'Role', 'Source', 'Model']
  );
  assert.equal(agent.fields[3][1], 'Configured; chat needs a model');
  assert.equal(agent.button, 'Create Agent');

  const everything = JSON.stringify([workspace, agent]);
  assert.doesNotMatch(everything, /prompt|digest|provenance|\/Users|\\\\/i);
});

test('reuse and adopt windows say Reuse, never Create', () => {
  const reuseAgent = windowSpec(
    assistantSetupMilestoneViews([
      { id: 'role:a', kind: 'agent_role', name: 'My Curator', role_id: 'a', action: 'reuse' }
    ])[0]
  );
  assert.equal(reuseAgent.title, 'Reuse Agent');
  assert.equal(reuseAgent.button, 'Reuse Agent');
  assert.equal(reuseAgent.fields[2][1], 'Existing agent in your roster');

  const [adoptedWorkspace, team] = assistantSetupMilestoneViews(
    requestedMilestones({ mode: 'adopt' })
  );
  const workspace = windowSpec(adoptedWorkspace, [team]);
  assert.equal(workspace.title, 'Reuse Workspace');
  assert.equal(workspace.button, 'Use workspace');
  assert.equal(workspace.fields[2][1], 'Keep Existing team kept as is');
  const existing = windowSpec(team);
  assert.equal(existing.title, 'Existing team');
  assert.equal(existing.button, 'Keep team');
  assert.doesNotMatch(JSON.stringify([workspace, existing, reuseAgent]), /Create/);
});

test('timers only move the view: advancing them never changes a status', () => {
  const milestones = receipted();
  const before = JSON.stringify(assistantSetupMilestoneViews(milestones));
  const timers = [];
  const advanced = [];
  const scheduler = createStepScheduler({
    setTimer: (fn, ms) => timers.push({ fn, ms }) && timers.length,
    clearTimer: () => {},
    intervalMs: 1000,
    onAdvance: index => advanced.push(index)
  });
  assert.equal(scheduler.start({ current: 0, available: 3 }), true);
  timers.shift().fn();
  assert.deepEqual(advanced, [1]);
  assert.equal(scheduler.start({ current: 1, available: 3 }), true);
  timers.shift().fn();
  assert.deepEqual(advanced, [1, 2]);
  assert.equal(
    scheduler.start({ current: 2, available: 3 }),
    false,
    'the last panel schedules nothing'
  );
  assert.equal(JSON.stringify(assistantSetupMilestoneViews(milestones)), before);
  assert.deepEqual(
    presentationMode(milestones),
    'walkthrough',
    'the mode is a function of receipts, not of elapsed time'
  );
});

test('the scheduler never advances past what is available and stop cancels it', () => {
  const cleared = [];
  const scheduler = createStepScheduler({
    setTimer: () => 7,
    clearTimer: id => cleared.push(id),
    onAdvance: () => assert.fail('must not advance')
  });
  assert.equal(scheduler.start({ current: 0, available: 1 }), false);
  assert.equal(scheduler.start({ current: 0, available: 2 }), true);
  assert.equal(scheduler.pending, true);
  scheduler.stop();
  assert.deepEqual(cleared, [7]);
  assert.equal(scheduler.pending, false);
});

test('reduced motion makes the scheduler inert', () => {
  let scheduled = 0;
  const scheduler = createStepScheduler({
    reducedMotion: true,
    setTimer: () => {
      scheduled += 1;
      return 1;
    },
    onAdvance: () => assert.fail('must not advance')
  });
  assert.equal(scheduler.start({ current: 0, available: 3 }), false);
  assert.equal(scheduled, 0);
  assert.equal(scheduler.pending, false);
});

test('long display text is kept whole and identity follows the server id, not the name', () => {
  const long = 'Reviewed role name with many words '.repeat(20).trim();
  const views = assistantSetupMilestoneViews([
    { id: 'role:one', kind: 'agent_role', name: long, status: 'created' },
    { id: 'role:two', kind: 'agent_role', name: long, status: 'created' }
  ]);
  assert.equal(views[0].name, long);
  assert.deepEqual(
    views.map(view => view.key),
    ['role:one', 'role:two']
  );
  const panels = buildPanels(
    views.map(view => ({ id: view.key, kind: view.kind, name: view.name, status: view.status }))
  );
  assert.deepEqual(
    panels[1].entries.map(entry => entry.key),
    ['role:one', 'role:two']
  );
});

test('receipt time is the latest recorded time and absent without receipts', () => {
  const later = receipted({ role: { recorded_at: '2026-09-21T10:05:00Z' } });
  assert.equal(receiptTime(later).toISOString(), '2026-09-21T10:05:00.000Z');
  assert.equal(receiptTime(requestedMilestones({ mode: 'create', team: [] })), null);
});

test('entry facts show ids and reviewed values but never prompts, paths, or digests', () => {
  const facts = receipted().flatMap(milestone =>
    entryFacts(assistantSetupMilestoneViews([milestone])[0])
  );
  const text = JSON.stringify(facts);
  assert.match(text, /workspace-1/);
  assert.match(text, /instance-1/);
  assert.match(text, /Created by this setup/);
  assert.doesNotMatch(text, /prompt|digest|provenance|\/Users|\\\\/i);
});
