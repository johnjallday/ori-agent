import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OnboardingManager,
  buildPersonalAssistantHirePayload,
  onboardingStartDestination,
  personalAssistantResumeMessage,
  recommendOnboardingStart,
  workspaceRootSetupView
} from './onboarding.js';
import { resetOnboardingGateForTests } from './onboarding-gate.js';

test('workspaceRootSetupView presents the default path as an unconfirmed suggestion', () => {
  const view = workspaceRootSetupView({
    workspace_root: '',
    effective_workspace_root: '',
    default_workspace_root: '/Users/test/Ori Workspaces',
    source: 'unconfirmed',
    confirmed: false
  });

  assert.equal(view.path, '/Users/test/Ori Workspaces');
  assert.equal(view.confirmed, false);
  assert.match(view.status, /will not scan/i);
});

test('workspaceRootSetupView prefers a confirmed custom path', () => {
  const view = workspaceRootSetupView({
    workspace_root: '/Volumes/Work/Ori',
    effective_workspace_root: '/Volumes/Work/Ori',
    default_workspace_root: '/Users/test/Ori Workspaces',
    source: 'settings',
    confirmed: true
  });

  assert.equal(view.path, '/Volumes/Work/Ori');
  assert.equal(view.confirmed, true);
  assert.match(view.status, /scan only this directory/i);
});

test('workspaceRootSetupView treats an operator WORKSPACE_DIR as confirmed', () => {
  const view = workspaceRootSetupView({
    effective_workspace_root: '/srv/ori-workspaces',
    source: 'environment',
    confirmed: true
  });

  assert.equal(view.path, '/srv/ori-workspaces');
  assert.equal(view.confirmed, true);
});

test('personalAssistantResumeMessage names durable assistant and HQ without claiming total failure', () => {
  assert.equal(
    personalAssistantResumeMessage({
      display_name: 'Atlas',
      assistant_id: 'assistant-1',
      hq_workspace_id: 'hq-1'
    }),
    'Atlas and Personal HQ are already saved. Retry to finish the remaining setup step.'
  );
  assert.match(personalAssistantResumeMessage({ hq_workspace_id: 'hq-1' }), /already saved/);
});

test('buildPersonalAssistantHirePayload normalizes one bounded confirmed hire', () => {
  const payload = buildPersonalAssistantHirePayload({
    requestId: ' request-1 ',
    ifVersion: 3,
    displayName: ' Assistant ',
    appearance: { mode: 'generated', generated: { color: '#225588' } },
    mandate: ' Keep today realistic. ',
    focusAreas: ['plan_my_day', 'plan_my_day', 'keep_projects_moving'],
    timezone: ' America/New_York ',
    scheduleDays: ['MON', 'tue', 'mon'],
    scheduleTime: '08:00',
    notifyOnReady: false
  });

  assert.deepEqual(payload, {
    request_id: 'request-1',
    if_version: 3,
    display_name: 'Assistant',
    appearance: { mode: 'generated', generated: { color: '#225588' } },
    mandate: 'Keep today realistic.',
    focus_areas: ['plan_my_day', 'keep_projects_moving'],
    timezone: 'America/New_York',
    schedule_days: ['mon', 'tue'],
    schedule_time: '08:00',
    notify_on_ready: false
  });
});

test('recommendOnboardingStart maps existing work to the import flow', () => {
  const recommendation = recommendOnboardingStart({ intent: 'existing' });
  assert.equal(recommendation.kind, 'import');
  assert.equal(onboardingStartDestination(recommendation), '/?import=1');
});

test('recommendOnboardingStart matches a ready blueprint from live catalog metadata', () => {
  const recommendation = recommendOnboardingStart({
    intent: 'new',
    description: 'I am producing a song in REAPER',
    templates: [
      {
        id: 'research-project',
        name: 'Research Project',
        description: 'Sources and synthesis.',
        tags: ['research'],
        readiness: { state: 'ready' }
      },
      {
        id: 'plugin:audio:session',
        name: 'REAPER Song',
        description: 'A recording and production workspace.',
        tags: ['music', 'reaper'],
        readiness: { state: 'ready' }
      }
    ]
  });

  assert.equal(recommendation.kind, 'template');
  assert.equal(recommendation.templateId, 'plugin:audio:session');
  assert.equal(
    onboardingStartDestination(recommendation),
    '/?create=1&blueprint=plugin%3Aaudio%3Asession'
  );
});

test('recommendOnboardingStart maps recurring organization to a personal operations blueprint', () => {
  const recommendation = recommendOnboardingStart({
    intent: 'organize',
    templates: [
      {
        id: 'personal-ops',
        name: 'Personal HQ',
        description: 'A personal command center for daily briefs and follow-ups.',
        tags: ['personal'],
        readiness: { state: 'ready' }
      },
      {
        id: 'research-project',
        name: 'Research Project',
        description: 'Sources and synthesis.',
        tags: ['research'],
        readiness: { state: 'ready' }
      }
    ]
  });
  assert.equal(recommendation.kind, 'template');
  assert.equal(recommendation.templateId, 'personal-ops');
});

test('recommendOnboardingStart never recommends an unavailable blueprint', () => {
  const recommendation = recommendOnboardingStart({
    intent: 'new',
    description: 'I am producing a song',
    templates: [
      {
        id: 'unavailable-song',
        name: 'Song',
        tags: ['music'],
        readiness: { state: 'blocked' }
      }
    ]
  });

  assert.equal(recommendation.kind, 'blank');
  assert.equal(onboardingStartDestination(recommendation), '/?create=1');
});

test('recommendOnboardingStart asks for context before guessing a new project', () => {
  const recommendation = recommendOnboardingStart({
    intent: 'new',
    templates: [{ id: 'personal', name: 'Personal HQ', tags: ['personal'] }]
  });
  assert.equal(recommendation.kind, 'pending');
});

test('recommendOnboardingStart lets the user explore without creating a project', () => {
  const recommendation = recommendOnboardingStart({ intent: 'explore' });
  assert.equal(recommendation.kind, 'home');
  assert.equal(onboardingStartDestination(recommendation), '/');
});

test('OnboardingManager consumes the shared memoized status gate', async () => {
  let calls = 0;
  resetOnboardingGateForTests(async () => {
    calls += 1;
    return {
      ok: true,
      json: async () => ({ needs_onboarding: true, assistant_name: 'Ori' })
    };
  });
  const manager = new OnboardingManager();
  const first = await manager.checkOnboardingStatus();
  const second = await manager.checkOnboardingStatus();
  assert.equal(first.needs_onboarding, true);
  assert.equal(second, first);
  assert.equal(calls, 1);
});
