import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

// The module's initializer is a no-op without a browser document.
globalThis.document ||= { readyState: 'complete', getElementById: () => null };
globalThis.window ||= { addEventListener() {}, location: { search: '' } };

const {
  workspaceLaunchStages,
  projectDraftInput,
  projectReviewPresentation,
  projectFailureGuidance,
  newJourneyIdempotencyKey,
  setupJourneyCloseDismisses,
  setupJourneyControlDisabled,
  setupJourneyCurrentStep,
  setupJourneyReceiptRows,
  setupQuestSelectionFromParams
} = await import('./setup-journey.js');

test('quest deep links select a host, user-template, or plugin quest by explicit source', () => {
  const select = query => setupQuestSelectionFromParams(new URLSearchParams(query));
  assert.deepEqual(select('setup=quest&source=host&quest=email_ops_setup'), {
    source: 'host',
    quest_id: 'email_ops_setup'
  });
  // A host link never borrows plugin or template identity from other params.
  assert.deepEqual(select('setup=quest&source=host&quest=email_ops_setup&plugin=reaper-plugin'), {
    source: 'host',
    quest_id: 'email_ops_setup'
  });
  assert.deepEqual(select('setup=quest&source=user_template&template=local&attachment=uqatt_x'), {
    source: 'user_template',
    template_id: 'local',
    attachment_id: 'uqatt_x'
  });
  assert.deepEqual(select('setup=quest&plugin=reaper-plugin&quest=reaper_setup'), {
    plugin_id: 'reaper-plugin',
    quest_id: 'reaper_setup'
  });
  // The Build-HQ walkthrough's own parameter is not a setup quest link.
  assert.deepEqual(select('quest=build-hq'), { plugin_id: '', quest_id: 'build-hq' });
});

test('current step follows server identity and preserves an explicit rail selection', () => {
  const journey = {
    current_step_id: 'project',
    steps: [
      { id: 'integration', status: 'complete' },
      { id: 'project', status: 'current' },
      { id: 'staffing', status: 'pending' }
    ]
  };
  assert.equal(setupJourneyCurrentStep(journey).id, 'project');
  assert.equal(setupJourneyCurrentStep(journey, 'integration').id, 'integration');
  assert.equal(
    setupJourneyCurrentStep({ steps: [{ id: 'pending', status: 'pending' }] }).id,
    'pending'
  );
  // A ready run has no current step and shows its summary, not its first step.
  assert.equal(
    setupJourneyCurrentStep({
      current_step_id: '',
      steps: [
        { id: 'team', status: 'complete' },
        { id: 'summary', status: 'complete' }
      ]
    }).id,
    'summary'
  );
});

test('a permitted development integration stays visibly distinct from a reviewed release', () => {
  const rows = setupJourneyReceiptRows(
    {},
    {
      integration: {
        plugin_id: 'local-plugin',
        expected_version: '1.0.0',
        installed_version: '1.0.0',
        enabled: true,
        development_copy: true
      }
    }
  );
  assert.deepEqual(rows, [
    ['Integration', 'local-plugin'],
    ['Installed', 'Yes'],
    ['Version', '1.0.0'],
    ['Enabled', 'Yes'],
    ['Verification', 'Local development copy — not release-verified']
  ]);
});

test('installation and enablement never imply verified release provenance', () => {
  const integration = {
    plugin_id: 'installed-plugin',
    installed_version: '0.5.0',
    expected_version: '0.5.0',
    enabled: true,
    release_ready: true,
    replacement_required: true
  };
  let rows = setupJourneyReceiptRows({}, { integration });
  assert.ok(rows.some(([label, value]) => label === 'Installed' && value === 'Yes'));
  assert.ok(
    rows.some(
      ([label, value]) => label === 'Verification' && value === 'Not verified for guided setup'
    )
  );
  assert.ok(rows.some(([label, value]) => label === 'Next step' && /replacement/.test(value)));
  rows = setupJourneyReceiptRows(
    {},
    { integration: { ...integration, verified: true, replacement_required: false, enabled: false } }
  );
  assert.ok(
    rows.some(([label, value]) => label === 'Verification' && value === 'Verified release')
  );
  assert.ok(rows.some(([label, value]) => label === 'Enabled' && value === 'Not yet'));
  assert.equal(
    rows.some(([label]) => label === 'Next step'),
    false
  );
});

test('file-only receipt stays honest about unconfigured and untested live control', () => {
  const rows = setupJourneyReceiptRows(
    {},
    {
      kind: 'workspace_setup',
      workspace_setup: {
        mode_label: 'File-only',
        files_connected: true,
        live_control_configured: false,
        live_control_tested: false
      }
    }
  );
  assert.deepEqual(rows, [
    ['Files connected', 'Yes'],
    ['Operating mode', 'File-only'],
    ['Live control configured', 'No'],
    ['Live control tested', 'No']
  ]);
});

test('closing a host quest only hides it; other journeys keep close-as-dismiss', () => {
  assert.equal(
    setupJourneyCloseDismisses({ journey: { source: 'host', id: 'email_ops_setup' } }),
    false
  );
  assert.equal(
    setupJourneyCloseDismisses({ journey: { source: 'plugin', id: 'reaper_setup' } }),
    true
  );
  assert.equal(setupJourneyCloseDismisses({ journey: { source: 'user_template' } }), true);
  assert.equal(setupJourneyCloseDismisses({ journey: {} }), true);
});

test('busy work keeps close available except during the atomic commit section', () => {
  assert.equal(setupJourneyControlDisabled(true, false, true), false);
  assert.equal(setupJourneyControlDisabled(true, false, false), true);
  assert.equal(setupJourneyControlDisabled(true, true, true), true);
  assert.equal(setupJourneyControlDisabled(false, true, false), false);
});

test('journey shell has labelled progress, associated errors, live status, focus targets, and responsive CSS', async () => {
  const template = await readFile(
    new URL('../../../templates/components/modals.tmpl', import.meta.url),
    'utf8'
  );
  const css = await readFile(new URL('../../css/setup-journey.css', import.meta.url), 'utf8');
  assert.match(template, /aria-label="Setup progress"/);
  assert.match(template, /data-bs-keyboard="false"/);
  assert.match(
    template,
    /aria-describedby="specialistSetupJourneyStepDescription specialistSetupJourneyError"/
  );
  assert.match(template, /id="specialistSetupJourneyError"[^>]+tabindex="-1"/);
  assert.match(template, /id="specialistSetupJourneyLiveStatus"[^>]+aria-live="polite"/);
  assert.match(css, /:focus-visible/);
  assert.match(css, /@media \(max-width: 760px\)/);
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/);
});

test('one project name defaults the Ori name without changing the exact review input', () => {
  const draft = { kind: 'new', projectName: ' First Idea ', workspaceName: '' };
  assert.deepEqual(projectDraftInput(draft), {
    mode_id: 'new_project',
    project_name: 'First Idea',
    workspace_name: 'First Idea'
  });
  assert.equal(
    projectDraftInput({ ...draft, workspaceName: ' Display Name ' }).workspace_name,
    'Display Name'
  );
  assert.equal(draft.projectName, ' First Idea ');
  assert.equal(
    projectDraftInput({ ...draft, groupComposition: 'standalone' }).group_composition,
    'standalone'
  );
  assert.deepEqual(
    projectDraftInput({
      kind: 'existing',
      workspaceName: ' Existing ',
      selectionToken: 'opaque',
      entryName: 'chosen.project'
    }),
    {
      mode_id: 'existing_project',
      workspace_name: 'Existing',
      selection_token: 'opaque',
      entry_name: 'chosen.project'
    }
  );
});

test('project reviews describe outcomes and commit errors never claim that nothing changed', () => {
  assert.equal(
    projectReviewPresentation({ mode_id: 'new_project', workspace_name: 'Idea' }).confirm,
    'Create Project'
  );
  assert.equal(
    projectReviewPresentation({ mode_id: 'existing_project', workspace_name: 'Idea' }).confirm,
    'Import Project'
  );
  assert.match(projectFailureGuidance('review', 'input_invalid'), /without slashes/);
  assert.match(
    projectFailureGuidance('review', 'input_invalid', true),
    /select the project folder again/
  );
  assert.match(projectFailureGuidance('commit'), /some files may already exist/);
  assert.doesNotMatch(projectFailureGuidance('commit'), /nothing|did not create/i);
});

test('four launch screens separate group preparation from canonical project readiness', () => {
  const journey = {
    journey: { workspace_launch: { group_title: 'Create Group', runtime_title: 'Set Up App' } },
    receipts: {},
    steps: [
      { kind: 'integration_install', title: 'Install Plugin', status: 'complete' },
      {
        kind: 'project_connect',
        status: 'active',
        preparation: { exists: false, acknowledged: false }
      }
    ]
  };
  assert.deepEqual(
    workspaceLaunchStages(journey).map(step => step.title),
    ['Install Plugin', 'Create Group', 'Set Up App', 'Create New Workspace']
  );
  assert.equal(workspaceLaunchStages(journey)[3].enabled, false);
  journey.steps[1].preparation.exists = true;
  assert.equal(workspaceLaunchStages(journey)[2].enabled, true);
  assert.equal(workspaceLaunchStages(journey)[3].enabled, false);
  journey.steps[1].preparation.acknowledged = true;
  assert.equal(workspaceLaunchStages(journey)[3].enabled, true);
  // Historical resource IDs do not establish readiness after a regression.
  journey.receipts.project_workspace_id = 'previous';
  assert.equal(workspaceLaunchStages(journey)[3].complete, false);
  journey.steps[0].status = 'blocked';
  assert.equal(workspaceLaunchStages(journey)[3].enabled, false);
});

test('Recommended and None launch paths can reach an explicit standalone review without a Home', () => {
  const base = policy => ({
    journey: { workspace_launch: { group_title: 'Create Group', runtime_title: 'Set Up App' } },
    receipts: {},
    steps: [
      { kind: 'integration_install', title: 'Install Plugin', status: 'complete' },
      {
        kind: 'project_connect',
        status: 'active',
        preparation: {
          exists: false,
          acknowledged: false,
          group_policy: policy,
          available_compositions: policy === 'none' ? ['standalone'] : ['grouped', 'standalone']
        }
      }
    ]
  });
  const recommended = workspaceLaunchStages(base('recommended'));
  assert.equal(recommended.find(stage => stage.id === 'group').complete, false);
  assert.equal(recommended.find(stage => stage.id === 'workspace').enabled, true);
  const none = workspaceLaunchStages(base('none'));
  assert.equal(none.find(stage => stage.id === 'group').complete, true);
  assert.equal(none.find(stage => stage.id === 'preparation').complete, true);
  assert.equal(none.find(stage => stage.id === 'workspace').enabled, true);
});

test('idempotency keys are non-empty and distinct', () => {
  const first = newJourneyIdempotencyKey();
  const second = newJourneyIdempotencyKey();
  assert.ok(first.length >= 16);
  assert.notEqual(first, second);
});
