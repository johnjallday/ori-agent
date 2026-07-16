import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  upgradeView,
  emailStatusView,
  chipStateLabel,
  replyProposalView,
  followUpView,
  followUpCategoryLabel,
  journalPromptView,
  hqWorkspaceRootView
} from './personal-hq-onboarding.js';

test('hqWorkspaceRootView requires confirmation before Build My HQ', () => {
  const view = hqWorkspaceRootView({
    default_workspace_root: '/Users/test/Ori Workspaces',
    source: 'unconfirmed',
    confirmed: false
  });
  assert.equal(view.path, '/Users/test/Ori Workspaces');
  assert.equal(view.confirmed, false);
  assert.match(view.status, /confirm/i);
});

test('hqWorkspaceRootView shows an active confirmed path', () => {
  const view = hqWorkspaceRootView({
    workspace_root: '/Volumes/Studio/Ori',
    effective_workspace_root: '/Volumes/Studio/Ori',
    source: 'settings',
    confirmed: true
  });
  assert.equal(view.path, '/Volumes/Studio/Ori');
  assert.equal(view.confirmed, true);
  assert.match(view.status, /scan only/i);
});

test('upgradeView: no plan hides the card', () => {
  assert.equal(upgradeView(null).show, false);
  assert.equal(upgradeView(undefined).show, false);
});

test('upgradeView: an up-to-date HQ shows nothing (never nag a current HQ)', () => {
  const view = upgradeView({ up_to_date: true, missing_roles: [] });
  assert.equal(view.show, false);
  assert.equal(view.upToDate, true);
});

test('upgradeView: a plan with additions is actionable and lists additions + preserved state', () => {
  const view = upgradeView({
    missing_roles: ['Inbox', 'Journal'],
    additions: ['Add the Inbox specialist', 'Add the Journal specialist'],
    preserved_customizations: ['Your other agents: My Assistant']
  });
  assert.equal(view.show, true);
  assert.equal(view.canApply, true);
  assert.equal(view.blocked, false);
  assert.equal(view.applyLabel, 'Apply upgrade');
  assert.deepEqual(view.additions, ['Add the Inbox specialist', 'Add the Journal specialist']);
  assert.match(view.preserved[0], /My Assistant/);
});

test('upgradeView: a retryable prior failure reframes as Resume/Retry', () => {
  const view = upgradeView({ missing_roles: ['Inbox'], retryable_prior_failure: true });
  assert.equal(view.canApply, true);
  assert.equal(view.retry, true);
  assert.equal(view.applyLabel, 'Retry upgrade');
  assert.match(view.heading, /resume/i);
});

test('upgradeView: a blocked plan is read-only with reasons and no apply', () => {
  const view = upgradeView({ blockers: ['group workspaces cannot be a Personal HQ'] });
  assert.equal(view.show, true);
  assert.equal(view.blocked, true);
  assert.equal(view.canApply, false);
  assert.deepEqual(view.reasons, ['group workspaces cannot be a Personal HQ']);
});

test('emailStatusView: a connected account shows the address and a Disconnect action', () => {
  const view = emailStatusView({ connected: true, email_address: 'me@example.com' });
  assert.equal(view.state, 'connected');
  assert.equal(view.detail, 'me@example.com');
  assert.equal(view.action, 'disconnect');
});

test('emailStatusView: a stale binding (account_id but not connected) offers Reconnect', () => {
  const view = emailStatusView({ connected: false, account_id: 'acct-1', health: 'disconnected' });
  assert.equal(view.state, 'repair');
  assert.equal(view.action, 'connect');
  assert.match(view.actionLabel, /reconnect/i);
});

test('emailStatusView: never-connected offers Connect and promises read-only + confirmation', () => {
  const view = emailStatusView(null);
  assert.equal(view.state, 'disconnected');
  assert.equal(view.action, 'connect');
  assert.match(view.detail, /read-only/i);
  assert.match(view.detail, /confirmation/i);
});

test('emailStatusView: no OAuth client on the server routes to Settings (setup state)', () => {
  const view = emailStatusView(null, false);
  assert.equal(view.state, 'setup');
  assert.equal(view.action, 'settings');
  assert.equal(view.chipState, 'empty');
  assert.match(view.detail, /Settings/i);
});

test('emailStatusView: connected is an equipped loadout chip', () => {
  const view = emailStatusView({ connected: true, email_address: 'me@x.com' }, true);
  assert.equal(view.chipState, 'equipped');
  assert.equal(view.chip, 'Email');
  assert.equal(view.action, 'disconnect');
});

test('chipStateLabel maps loadout states to short words', () => {
  assert.equal(chipStateLabel('equipped'), 'Connected');
  assert.equal(chipStateLabel('repair'), 'Needs repair');
  assert.equal(chipStateLabel('empty'), 'Not set up');
  assert.equal(chipStateLabel(undefined), 'Not set up');
});

test('replyProposalView: a draft is sendable and carries the exact reviewed payload hash', () => {
  const view = replyProposalView({
    id: 'p1',
    status: 'draft',
    payload_hash: 'abc',
    payload: { to: ['dana@x.com', 'sam@x.com'], subject: 'Re: hi', body: 'Sounds good.' }
  });
  assert.equal(view.canSend, true);
  assert.equal(view.actionLabel, 'Send');
  assert.equal(view.to, 'dana@x.com, sam@x.com');
  assert.equal(view.subject, 'Re: hi');
  assert.equal(view.body, 'Sounds good.');
  assert.equal(view.payloadHash, 'abc');
});

test('replyProposalView: a failed draft is retryable', () => {
  const view = replyProposalView({ id: 'p1', status: 'failed', payload: {} });
  assert.equal(view.canSend, true);
  assert.equal(view.actionLabel, 'Retry send');
  assert.match(view.statusNote, /failed/i);
});

test('replyProposalView: a sent proposal is terminal (not sendable)', () => {
  const view = replyProposalView({ id: 'p1', status: 'sent', payload: {} });
  assert.equal(view.canSend, false);
  assert.equal(view.statusNote, 'Sent');
});

test('followUpCategoryLabel maps v1 categories to friendly labels', () => {
  assert.equal(followUpCategoryLabel('i_owe'), 'You owe');
  assert.equal(followUpCategoryLabel('waiting_on'), 'Waiting on');
  assert.equal(followUpCategoryLabel('needs_decision'), 'Needs decision');
  assert.equal(followUpCategoryLabel('recurring_check_in'), 'Check-in');
  assert.equal(followUpCategoryLabel('unknown'), 'Follow-up');
});

test('followUpView: an active item is actionable (Done/Snooze), not a candidate', () => {
  const view = followUpView({
    id: 'f1',
    status: 'active',
    category: 'waiting_on',
    title: "Dana's quote",
    counterparty: 'Dana'
  });
  assert.equal(view.isCandidate, false);
  assert.equal(view.category, 'Waiting on');
  assert.equal(view.counterparty, 'Dana');
  assert.equal(view.title, "Dana's quote");
});

test('followUpView: a candidate is flagged for confirm/dismiss', () => {
  const view = followUpView({
    id: 'f2',
    status: 'candidate',
    category: 'i_owe',
    title: 'Maybe send deck'
  });
  assert.equal(view.isCandidate, true);
});

test('journalPromptView: carries the editable draft and degraded flag', () => {
  const view = journalPromptView({
    local_date: '2026-07-15',
    draft: '# End of day',
    degraded: true,
    gaps: ['x']
  });
  assert.equal(view.localDate, '2026-07-15');
  assert.equal(view.draft, '# End of day');
  assert.equal(view.degraded, true);
  assert.deepEqual(view.gaps, ['x']);
});

test('journalPromptView: degrades safely with no proposal', () => {
  const view = journalPromptView(null);
  assert.equal(view.draft, '');
  assert.equal(view.degraded, false);
});
