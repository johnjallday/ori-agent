import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  HQ_LABELS,
  TACTICAL_TERMS,
  hqLabel,
  portabilityWord,
  tacticalTermsIn
} from './hq-labels.js';

test('the map carries every plain name the HQ page needs', () => {
  const expected = {
    lead: 'Lead',
    team: 'Team',
    tasks: 'Tickets',
    openTasks: 'Open tickets',
    giveTask: 'Give work',
    backlog: 'Backlog',
    workspaceNotes: 'Workspace notes',
    sessions: 'Sessions',
    files: 'Files',
    setup: 'Setup',
    stations: 'Stations',
    needsYou: 'Needs you',
    followUps: 'Follow-ups',
    // The Map (PRD 767)
    mapArrangement: 'Map arrangement',
    flow: 'Flow',
    free: 'Free',
    whatOriReads: 'What Ori reads',
    yourLeadAgent: 'Your lead agent',
    whatNeedsYou: 'What needs you',
    email: 'Email',
    calendar: 'Calendar',
    memory: 'Memory',
    otherWorkspaces: 'Other workspaces',
    todaysBrief: "Today's brief",
    waitingOnYou: 'Waiting on you',
    specialists: 'Specialists',
    noSpecialists: 'No specialists yet',
    workspaceGoal: 'Workspace goal',
    inventory: 'Inventory',
    newTask: 'New ticket',
    notSetUp: 'Not set up',
    ready: 'Ready',
    running: 'Running',
    error: 'Error'
  };
  for (const [key, label] of Object.entries(expected)) {
    assert.equal(HQ_LABELS[key], label, key);
  }
});

test('no label contains a tactical term', () => {
  for (const [key, label] of Object.entries(HQ_LABELS)) {
    assert.deepEqual(tacticalTermsIn(label), [], `${key}: ${label}`);
  }
});

test('the map cannot be changed at runtime', () => {
  assert.ok(Object.isFrozen(HQ_LABELS));
  assert.ok(Object.isFrozen(TACTICAL_TERMS));
});

test('hqLabel falls back for an unknown key', () => {
  assert.equal(hqLabel('team'), 'Team');
  assert.equal(hqLabel('nope'), 'nope');
  assert.equal(hqLabel('nope', ''), '');
  // Inherited object keys are not labels.
  assert.equal(hqLabel('toString', 'x'), 'x');
});

test('portabilityWord is a short state word, never a timestamp', () => {
  assert.equal(portabilityWord(null), '');
  assert.equal(
    portabilityWord({ state: 'ready', label: 'Ready to move as of 11/3 9:00' }),
    'Ready to move'
  );
  assert.equal(portabilityWord({ state: 'preparing' }), 'Preparing');
  assert.equal(portabilityWord({ state: 'update_needed' }), 'Saving latest work');
  assert.equal(portabilityWord({ state: 'unavailable' }), 'Not ready to move');
  assert.equal(
    portabilityWord({ state: 'ready', imported: true, backgroundAllowed: false }),
    'Imported · routines off'
  );
  assert.equal(
    portabilityWord({ state: 'ready', imported: true, backgroundAllowed: true }),
    'Imported · ready to move'
  );
});

test('tacticalTermsIn finds each term, ignoring case', () => {
  assert.deepEqual(tacticalTermsIn('Your HQ, Team and Stations'), []);
  assert.deepEqual(tacticalTermsIn('the garrison and the QUEST BOARD'), [
    'Garrison',
    'Quest Board'
  ]);
  assert.deepEqual(tacticalTermsIn('aria-label="Commander portrait"'), ['Commander']);
  assert.deepEqual(tacticalTermsIn(null), []);
});
