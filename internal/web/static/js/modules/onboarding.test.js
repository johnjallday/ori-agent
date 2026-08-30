import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OnboardingManager,
  onboardingStartDestination,
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
