import assert from 'node:assert/strict';
import test from 'node:test';

import {
  approvedSourceChip,
  copiesSummary,
  copyTargetWorkspaces,
  dossierEmptyGuidance,
  groupReviewedKnowledge,
  memoryStationHref,
  promotedFromWorkspace,
  reviewTextValid,
  workspaceProposalSource
} from './personal-hq-knowledge.js';

test('the copy picker offers active workspaces only, never the Personal HQ', () => {
  const targets = copyTargetWorkspaces({
    workspaces: [
      { id: 'hq', name: 'My HQ', designation: 'personal_hq', status: 'active' },
      { id: 'b', name: 'Beta', status: 'active', folder_slug: 'beta' },
      { id: 'a', name: 'Album 3' },
      { id: 'old', name: 'Finished', status: 'completed' },
      { id: '', name: 'Broken' },
      null
    ]
  });
  assert.deepEqual(targets, [
    { id: 'a', name: 'Album 3' },
    { id: 'b', name: 'Beta' }
  ]);
  assert.deepEqual(copyTargetWorkspaces({}), []);
});

test('a copied fact links to the target workspace Memory station the server names (FR26)', () => {
  assert.equal(
    memoryStationHref('/workspaces/album-3?station=memory'),
    '/workspaces/album-3?station=memory'
  );
  assert.equal(memoryStationHref(''), '');
  assert.equal(memoryStationHref(undefined), '');
  assert.equal(memoryStationHref('/workspaces/album-3'), '', 'the station must be named');
  assert.equal(memoryStationHref('/workspaces/album-3?station=daily-brief'), '', 'only Memory');
  assert.equal(memoryStationHref('https://evil.example/workspaces/a?station=memory'), '');
});

test('the copies line names every workspace holding a copy', () => {
  assert.match(copiesSummary([{ name: 'Album 3' }, { name: 'EP' }]), /^Copies in: Album 3, EP\./);
  assert.match(copiesSummary([{ name: 'Album 3' }]), /does not change with this fact/);
  assert.equal(copiesSummary([]), 'No workspace holds a copy of this fact.');
});

test('a fact promoted from a workspace names and links that workspace', () => {
  assert.deepEqual(
    promotedFromWorkspace({
      source: {
        kind: 'explicit',
        workspace_id: 'album-3',
        workspace_name: 'Album 3',
        workspace_slug: 'album-3'
      }
    }),
    { workspace: 'Album 3', href: '/workspaces/album-3' }
  );
  assert.deepEqual(promotedFromWorkspace({ source: { kind: 'explicit', workspace_id: 'gone' } }), {
    workspace: 'a workspace',
    href: ''
  });
  assert.equal(promotedFromWorkspace({ source: { kind: 'explicit' } }), null);
  assert.equal(
    promotedFromWorkspace({ source: { kind: 'workspace_agent', workspace_id: 'x' } }),
    null
  );
});

test('every approved global fact names its origin', () => {
  const chip = kind => approvedSourceChip({ source: { kind } });
  assert.equal(chip('explicit'), 'From you');
  assert.equal(chip('interview'), 'From your interview');
  assert.equal(chip('workspace_agent'), 'From a workspace');
  assert.equal(chip('folder_scan'), 'From a folder scan');
  assert.equal(chip('saved_app'), 'From saved apps');
  assert.equal(chip('file_janitor'), 'From File Janitor');
  // An older server without the source object still labels by source_kind.
  assert.equal(approvedSourceChip({ source_kind: 'saved_app' }), 'From saved apps');
  assert.equal(chip('something_new'), '');
});

test('a workspace proposal names its agent, workspace link and task or chat', () => {
  const fromTask = workspaceProposalSource({
    source: {
      kind: 'workspace_agent',
      workspace_id: 'album-3',
      workspace_name: 'Album 3',
      workspace_slug: 'album 3',
      agent: 'Scout',
      task_id: '8fffe1ea-5c66-402f-b6e2-90aa95e0bd0d',
      observed_at: '2026-10-17T12:00:00Z'
    }
  });
  assert.deepEqual(fromTask, {
    agent: 'Scout',
    workspace: 'Album 3',
    href: '/workspaces/album%203',
    task: '8fffe1ea',
    taskHref: '/workspaces/album%203?panel=backlog&task=8fffe1ea-5c66-402f-b6e2-90aa95e0bd0d',
    observedAt: '2026-10-17T12:00:00Z'
  });
  const fromChat = workspaceProposalSource({
    source: { kind: 'workspace_agent', workspace_name: 'Album 3', agent: 'Scout' }
  });
  assert.equal(fromChat.task, '');
  // A removed workspace keeps its recorded name and gets no link.
  assert.equal(fromChat.href, '');
  const removedWithTask = workspaceProposalSource({
    source: { kind: 'workspace_agent', task_id: '8fffe1ea-5c66' }
  });
  assert.equal(removedWithTask.taskHref, '');
  const anonymous = workspaceProposalSource({ source: { kind: 'workspace_agent' } });
  assert.equal(anonymous.agent, 'An agent');
  assert.equal(anonymous.workspace, 'a workspace');
  assert.equal(workspaceProposalSource({ source: { kind: 'saved_app' } }), null);
  assert.equal(workspaceProposalSource({}), null);
});

test('review ledger keeps pending and suspended separate from current canonical facts', () => {
  const { queue, approved } = groupReviewedKnowledge([
    { id: 'candidate', version: 1, state: 'candidate', text: 'Maybe use Obsidian' },
    { id: 'approved', version: 2, state: 'approved', text: 'I use Obsidian' },
    { id: 'stale', version: 3, state: 'needs_review', text: 'Old wording' },
    {
      id: 'invalid-source',
      version: 2,
      state: 'approved',
      review_unavailable: 'source_unavailable',
      text: ''
    },
    { id: 'forgotten', version: 2, state: 'forgotten', text: 'Should be erased' },
    { id: 'rejected', version: 2, state: 'rejected', text: 'Should be erased' },
    { id: 'missing-version', state: 'candidate', text: 'untrusted' }
  ]);
  assert.deepEqual(
    queue.map(item => item.id),
    ['candidate', 'stale']
  );
  assert.deepEqual(
    approved.map(item => item.id),
    ['approved']
  );
});

test('empty-section setup nudges stop when connected and never promise future learning', () => {
  const calendar = {
    status: 'not_configured',
    action_route: '/?create=1&blueprint=calendar-ops',
    action_label: 'Set up Calendar Ops'
  };
  const setup = dossierEmptyGuidance('routines', calendar);
  assert.equal(setup.route, calendar.action_route);
  assert.match(setup.message, /not automatic reviewed-memory learning/);
  const connected = dossierEmptyGuidance('routines', { ...calendar, status: 'available' });
  assert.equal(connected.route, undefined);
  assert.match(connected.message, /Calendar is connected/);
  assert.match(connected.message, /not enabled/);
  const healthy = dossierEmptyGuidance('routines', { ...calendar, status: 'healthy_empty' });
  assert.equal(healthy.route, undefined);
  assert.match(healthy.message, /Calendar is connected/);
  const revoked = dossierEmptyGuidance('routines', { ...calendar, status: 'revoked' });
  assert.equal(revoked.route, calendar.action_route);
  const unavailable = dossierEmptyGuidance('routines', { ...calendar, status: 'unavailable' });
  assert.equal(unavailable.route, '#addPersonalHQFact');
  assert.match(unavailable.message, /could not be verified/);
  const unsafe = dossierEmptyGuidance('routines', {
    ...calendar,
    action_route: '//outside.example'
  });
  assert.notEqual(unsafe.route, '//outside.example');
  const people = dossierEmptyGuidance('people', { ...calendar, status: 'available' });
  assert.equal(people.route, '#addPersonalHQFact');
  assert.doesNotMatch(people.message, /connect.*email/i);
});

test('a reviewed line has a strict UTF-8 byte limit and no blank, multiline, control or bidi text', () => {
  assert.equal(reviewTextValid('My priority is launch readiness'), true);
  assert.equal(reviewTextValid(''), false);
  assert.equal(reviewTextValid('  padded'), false);
  assert.equal(reviewTextValid('two  spaces'), false);
  assert.equal(reviewTextValid('non\u00a0breaking'), false);
  assert.equal(reviewTextValid('line\nsecond line'), false);
  assert.equal(reviewTextValid('é'.repeat(251)), false);
  assert.equal(reviewTextValid('é'.repeat(250)), true);
  assert.equal(reviewTextValid('hidden\u0000byte'), false);
  assert.equal(reviewTextValid('visual\u202espoof'), false);
});
