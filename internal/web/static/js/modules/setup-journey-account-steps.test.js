import test from 'node:test';
import assert from 'node:assert/strict';

import {
  ACCOUNT_STEP_KINDS,
  EMAIL_OPS_DEFAULT_NAME,
  INBOX_LOCK_REASON,
  accountStepReceiptRows,
  accountStepWorkspaceRoute,
  advanceCreatorToTeam,
  isAccountStepKind,
  teamReviewCreatorOptions
} from './setup-journey-account-steps.js';

test('only the three host account-link kinds are handled here', () => {
  assert.deepEqual(
    [...ACCOUNT_STEP_KINDS],
    ['workspace_create', 'account_connect', 'account_link']
  );
  for (const kind of ['workspace_create', 'account_connect', 'account_link']) {
    assert.equal(isAccountStepKind(kind), true);
  }
  for (const kind of ['summary', 'project_connect', 'integration_install', '', undefined]) {
    assert.equal(isAccountStepKind(kind), false);
  }
});

test('team step rows describe the blueprint and whether the workspace exists', () => {
  assert.deepEqual(
    accountStepReceiptRows({
      kind: 'workspace_create',
      workspace_create: { template_title: 'Email Ops' }
    }),
    [
      ['Blueprint', 'Email Ops'],
      ['Workspace', 'Not created yet']
    ]
  );
  assert.deepEqual(
    accountStepReceiptRows({
      kind: 'workspace_create',
      workspace_create: {
        template_title: 'Email Ops',
        workspace_id: 'ws-1',
        workspace_label: 'My inbox'
      }
    }),
    [
      ['Blueprint', 'Email Ops'],
      ['Workspace', 'My inbox']
    ]
  );
  assert.deepEqual(accountStepReceiptRows({ kind: 'project_connect' }), []);
  assert.deepEqual(accountStepReceiptRows(null), []);
});

test('the creator opens on Email Ops with the Inbox rename locked and the page kept', () => {
  const onCreated = () => {};
  const options = teamReviewCreatorOptions(onCreated);
  assert.deepEqual(
    { ...options, onCreated: undefined },
    {
      blueprint: 'email-ops',
      entryPoint: 'host_setup_quest',
      stayAfterCreate: true,
      stageBlueprintRoles: true,
      teamLock: { agentNames: ['Inbox'], reason: INBOX_LOCK_REASON },
      onCreated: undefined
    }
  );
  assert.equal(options.onCreated, onCreated);
  assert.equal(teamReviewCreatorOptions('not a function').onCreated, undefined);
  // `guided` and `mode: 'guided'` belong to the Group-preparation creator; the
  // quest opens an ordinary Workspace creator.
  assert.equal('guided' in options, false);
  assert.equal('mode' in options, false);
});

test('workspace routes come only from a validated server projection', () => {
  const journey = route => ({
    steps: [{ kind: 'workspace_create', workspace_create: { workspace_route: route } }]
  });
  assert.equal(
    accountStepWorkspaceRoute(journey('/workspaces/email-ops')),
    '/workspaces/email-ops'
  );
  assert.equal(
    accountStepWorkspaceRoute(journey('/workspaces/email-ops'), '?panel=tasks'),
    '/workspaces/email-ops?panel=tasks'
  );
  for (const bad of [
    '',
    'https://evil.test/workspaces/x',
    '//evil.test/workspaces/x',
    '/workspaces/a/b',
    '/workspaces/a?x=1',
    '/workspaces/',
    '/settings',
    '/workspaces/a%2Fb',
    '/workspaces/has space'
  ]) {
    assert.equal(accountStepWorkspaceRoute(journey(bad)), '', `route ${JSON.stringify(bad)}`);
  }
  assert.equal(accountStepWorkspaceRoute({ steps: [] }), '');
  assert.equal(accountStepWorkspaceRoute(null), '');
});

test('advancing to Team waits for the blueprint and fills only an empty name', async () => {
  const calls = [];
  const events = [];
  let selected = null;
  const manager = {
    blueprintSelectionBlocked: () => false,
    goToWizardStep: step => calls.push(step)
  };
  const nameInput = { value: '', dispatchEvent: event => events.push(event.type) };
  const waits = [];
  const moved = await advanceCreatorToTeam({
    manager,
    getSelectedTemplate: () => selected,
    nameInput,
    wait: async () => {
      waits.push(1);
      if (waits.length === 2) selected = { id: 'email-ops' };
    }
  });
  assert.equal(moved, true);
  assert.deepEqual(calls, [3]);
  assert.equal(waits.length, 2);
  assert.equal(nameInput.value, EMAIL_OPS_DEFAULT_NAME);
  assert.deepEqual(events, ['input']);

  const typed = {
    value: 'My inbox',
    dispatchEvent: () => assert.fail('no event for a typed name')
  };
  await advanceCreatorToTeam({
    manager,
    getSelectedTemplate: () => ({ id: 'email-ops' }),
    nameInput: typed,
    wait: async () => {}
  });
  assert.equal(typed.value, 'My inbox');
});

test('a blocked or foreign blueprint, or a closed creator, never advances', async () => {
  const noAdvance = {
    blueprintSelectionBlocked: () => true,
    goToWizardStep: () => assert.fail('advanced past a blocked blueprint')
  };
  assert.equal(
    await advanceCreatorToTeam({
      manager: noAdvance,
      getSelectedTemplate: () => ({ id: 'email-ops' }),
      wait: async () => {},
      attempts: 3
    }),
    false
  );
  assert.equal(
    await advanceCreatorToTeam({
      manager: { goToWizardStep: () => assert.fail('advanced on a foreign blueprint') },
      getSelectedTemplate: () => ({ id: 'calendar-ops' }),
      wait: async () => {},
      attempts: 3
    }),
    false
  );
  assert.equal(
    await advanceCreatorToTeam({
      manager: { goToWizardStep: () => assert.fail('advanced after close') },
      getSelectedTemplate: () => ({ id: 'email-ops' }),
      isCurrent: () => false
    }),
    false
  );
});
