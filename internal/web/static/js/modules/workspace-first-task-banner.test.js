import test from 'node:test';
import assert from 'node:assert/strict';

import { firstTaskBannerView } from './workspace-first-task-banner.js';

test('a running first task says the agent is working and that it is read-only', () => {
  const view = firstTaskBannerView({
    state: 'in_progress',
    agentName: 'REAPER Assistant',
    taskTitle: 'Summarize the session',
    href: '/workspaces/session/task/t1'
  });
  assert.equal(view.phase, 'working');
  assert.equal(view.heading, 'REAPER Assistant is working on its first task');
  assert.match(view.detail, /Summarize the session/);
  assert.match(view.detail, /read-only/);
  assert.equal(view.linkLabel, 'Watch the task');
  assert.equal(view.href, '/workspaces/session/task/t1');
});

test('the banner follows the task to its end', () => {
  const at = state => firstTaskBannerView({ state, agentName: 'Atlas' });
  assert.equal(at('completed').phase, 'done');
  assert.equal(at('completed').heading, 'Atlas finished its first task');
  assert.equal(at('blocked').phase, 'needs-you');
  assert.equal(at('waiting_for_choice').phase, 'needs-you');
  for (const failed of ['failed', 'timeout', 'cancelled']) {
    assert.equal(at(failed).phase, 'failed');
  }
  assert.equal(at('pending').phase, 'working');
  assert.equal(at('something-new').phase, 'working');
});

test('without an agent name the banner still reads, and only same-site links are kept', () => {
  assert.equal(
    firstTaskBannerView({ state: 'pending' }).heading,
    'Your agent is working on its first task'
  );
  assert.equal(firstTaskBannerView({ state: 'pending', href: '//evil.example/x' }).href, '');
  assert.equal(firstTaskBannerView({ state: 'pending', href: 'https://evil.example/x' }).href, '');
});
