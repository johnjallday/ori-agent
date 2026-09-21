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
  requestedMilestones
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
  const stoppedAtRole = receipted({
    role: { status: 'needs_review', resource_id: '', ownership: '' }
  });
  // The workspace is receipted, the role needs review: both step panels and the
  // receipt summary of what was kept are viewable.
  assert.equal(availablePanelCount('stopped', buildPanels(stoppedAtRole)), 3);
  const workspaceOnly = [
    { id: 'workspace', kind: 'workspace', status: 'needs_review' },
    { id: 'role:a', kind: 'agent_role', status: 'pending' }
  ];
  assert.equal(availablePanelCount('stopped', buildPanels(workspaceOnly)), 1);
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
