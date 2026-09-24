import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { groupBuildState } from './group-builder.js';

// The module's initializer is a no-op without a browser document.
globalThis.document ||= { readyState: 'complete', getElementById: () => null };
globalThis.window ||= { addEventListener() {}, location: { search: '' } };

const {
  PLUGINS_PAGE_URL,
  RELEASE_UNCHECKED_NOTE,
  START_OVER_EXPLANATION,
  WORKSPACE_LAUNCH_DESCRIPTION,
  homeProviderDisclosureIntro,
  homeProviderDisclosureRows,
  homeProviderOffer,
  homeProviderReceiptRow,
  homeProviderRecoveryFailure,
  integrationHandoffNavigation,
  integrationReleaseCheckNote,
  integrationReviewPresentation,
  setupJourneyActionLabel,
  setupJourneyPreconditionView,
  workspaceLaunchStages,
  projectDraftInput,
  projectReviewPresentation,
  projectFailureGuidance,
  integrationReviewRows,
  reviewRows,
  safeExternalLink,
  newJourneyIdempotencyKey,
  setupJourneyCloseDismisses,
  setupJourneyControlDisabled,
  setupJourneyCurrentStep,
  setupJourneyReceiptRows,
  setupJourneyStartOverView,
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

test('before install the receipt shows only what will be installed', () => {
  const rows = setupJourneyReceiptRows(
    {},
    {
      integration: {
        plugin_id: 'reaper-plugin',
        installed_version: '',
        expected_version: '0.5.0',
        enabled: false,
        verified: false
      }
    }
  );
  assert.deepEqual(rows, [
    ['Integration', 'reaper-plugin'],
    ['Installed', 'No'],
    ['Version to install', '0.5.0']
  ]);
});

test('an unchecked latest release is explained only where a release is offered', () => {
  const offer = (id, integration) => ({
    integration: { plugin_id: 'reaper-plugin', expected_version: '0.6.1', ...integration },
    actions: [{ id }, { id: 'manage_integration' }]
  });
  assert.equal(
    integrationReleaseCheckNote(offer('review_install', { release_checked: false })),
    RELEASE_UNCHECKED_NOTE
  );
  assert.equal(
    RELEASE_UNCHECKED_NOTE,
    'The latest release could not be checked. Ori will install the minimum reviewed version.'
  );
  assert.equal(
    integrationReleaseCheckNote(offer('review_update', { release_checked: false })),
    RELEASE_UNCHECKED_NOTE
  );
  // Hidden when the release was checked, or when an older server omits the field.
  assert.equal(integrationReleaseCheckNote(offer('review_install', { release_checked: true })), '');
  assert.equal(integrationReleaseCheckNote(offer('review_install', {})), '');
  // A verified step offers no release, so it never mentions one.
  assert.equal(
    integrationReleaseCheckNote(
      offer('review_enable', { release_checked: false, verified: true, installed_version: '0.6.1' })
    ),
    ''
  );
  assert.equal(integrationReleaseCheckNote({ actions: [{ id: 'review_install' }] }), '');
  assert.equal(integrationReleaseCheckNote(null), '');
});

test('integration reviews are named by their outcome', () => {
  const integration = {
    plugin_id: 'reaper-plugin',
    expected_version: '0.5.0',
    source_label: 'johnjallday/reaper-plugin'
  };
  assert.deepEqual(
    integrationReviewPresentation(
      { commit_action: 'install', integration },
      'Install Ori REAPER Plugin'
    ),
    {
      title: 'Install Ori REAPER Plugin?',
      description:
        'Ori downloads the reviewed 0.5.0 release from johnjallday/reaper-plugin, checks its fingerprint, and installs it. Nothing runs until you enable it.',
      confirm: 'Install'
    }
  );
  assert.equal(
    integrationReviewPresentation({ commit_action: 'install', integration: { plugin_id: 'p' } })
      .description,
    'Ori downloads the reviewed release, checks its fingerprint, and installs it. Nothing runs until you enable it.'
  );
  assert.equal(
    integrationReviewPresentation({ commit_action: 'install', integration }).title,
    'Install this plugin?'
  );
  const enable = integrationReviewPresentation({ commit_action: 'enable', integration });
  assert.equal(enable.title, 'Enable this plugin?');
  assert.equal(enable.confirm, 'Enable');
  for (const review of [
    { commit_action: 'update', integration },
    { commit_action: 'install', integration: { ...integration, replacement_required: true } }
  ]) {
    const replace = integrationReviewPresentation(review, 'Install Ori REAPER Plugin');
    assert.equal(replace.title, 'Replace installed integration?');
    assert.equal(replace.confirm, 'Replace with reviewed version');
  }
  const enabledAfter = review =>
    reviewRows(review).find(([label]) => label === 'Enabled after this action')[1];
  assert.equal(enabledAfter({ commit_action: 'install', integration }), 'No');
  assert.equal(enabledAfter({ commit_action: 'enable', integration }), 'Yes');
  assert.equal(
    enabledAfter({ commit_action: 'update', integration: { ...integration, enabled: true } }),
    'Already enabled'
  );
  // The decision facts stay visible; the trust disclosure is technical detail.
  const split = integrationReviewRows({
    commit_action: 'install',
    integration: {
      ...integration,
      publisher: 'Ori',
      supported_platforms: ['darwin/arm64'],
      required_host_features: ['assistant_program_v1'],
      trust: { skills: ['tidy'], artifacts: [{ sha256: 'abc', size: 1 }], empty: [] }
    }
  });
  assert.deepEqual(
    split.summary.map(([label]) => label),
    ['Publisher', 'Source', 'Release version', 'Enabled after this action']
  );
  // The minimum reviewed version appears beside the release it bounds.
  const withMinimum = integrationReviewRows({
    commit_action: 'update',
    integration: { ...integration, expected_version: '0.6.2', minimum_version: '0.6.1' }
  });
  assert.deepEqual(withMinimum.summary.slice(1, 3), [
    ['Release version', '0.6.2'],
    ['Minimum reviewed version', '0.6.1']
  ]);
  assert.deepEqual(
    split.details.map(([label]) => label),
    ['Integration', 'Platform', 'Required host features', 'Skills', 'Artifacts']
  );
  // The flat row list still carries every integration row, summary first.
  const flat = { commit_action: 'install', integration, expires_at: '2035-01-01T00:00:00Z' };
  const parts = integrationReviewRows(flat);
  assert.deepEqual(reviewRows(flat).slice(0, parts.summary.length + parts.details.length), [
    ...parts.summary,
    ...parts.details
  ]);
  assert.equal(integrationReviewRows({ project_connection: {} }), null);

  // The reviewed repository shows as its full URL and links out.
  const linked = integrationReviewRows({
    commit_action: 'install',
    integration: { ...integration, source_url: 'https://github.com/johnjallday/reaper-plugin' }
  });
  assert.deepEqual(linked.summary.find(([label]) => label === 'Source')[1], {
    text: 'https://github.com/johnjallday/reaper-plugin',
    href: 'https://github.com/johnjallday/reaper-plugin'
  });
  // An unsafe URL falls back to the plain label, never a link.
  for (const unsafe of [
    'javascript:alert(1)',
    'http://github.com/x',
    'https://u:p@github.com/x',
    'https://github.com/x?q=1',
    'https://github.com/x#readme',
    'not a url'
  ]) {
    assert.equal(safeExternalLink(unsafe), '', unsafe);
    const fallback = integrationReviewRows({
      commit_action: 'install',
      integration: { ...integration, source_url: unsafe }
    });
    assert.equal(
      fallback.summary.find(([label]) => label === 'Source')[1],
      'johnjallday/reaper-plugin'
    );
  }
  assert.equal(integrationReviewPresentation({ commit_action: 'install' }), null);
  assert.equal(integrationReviewPresentation({ commit_action: 'other', integration }), null);
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

function installQuestJourney(summary = {}) {
  return {
    run_id: 'run-install',
    lifecycle_state: 'ready',
    journey: { source: 'host', id: 'install_ori_reaper', title: 'Install Ori REAPER Plugin' },
    receipts: { integration_plugin_id: 'reaper-plugin', integration_version: '0.6.0' },
    steps: [
      {
        id: 'integration',
        kind: 'integration_install',
        title: 'Install Ori REAPER Plugin',
        status: 'complete'
      },
      {
        id: 'summary',
        kind: 'summary',
        title: 'REAPER plugin ready',
        status: 'complete',
        ...summary
      }
    ]
  };
}

test('an install quest renders its two steps with nothing disabled', () => {
  const journey = installQuestJourney();
  journey.lifecycle_state = 'in_progress';
  journey.current_step_id = 'integration';
  journey.steps[0].status = 'active';
  journey.steps[1].status = 'pending';
  assert.equal(journey.journey.workspace_launch, undefined);
  assert.equal(setupJourneyCurrentStep(journey).id, 'integration');
  assert.equal(setupJourneyCurrentStep(journey, 'summary').id, 'summary');
});

test('an install summary continues into the handed-off plugin quest in place', () => {
  const handoff = {
    source: 'plugin',
    plugin_id: 'reaper-plugin',
    id: 'reaper_setup',
    title: 'Set up REAPER'
  };
  const summary = installQuestJourney({ handoff }).steps[1];
  assert.deepEqual(integrationHandoffNavigation(summary, 'continue_integration_setup'), {
    kind: 'open_quest',
    detail: { plugin_id: 'reaper-plugin', quest_id: 'reaper_setup' }
  });
  assert.equal(
    setupJourneyActionLabel(summary, {
      id: 'continue_integration_setup',
      label: 'Continue setup'
    }),
    'Continue: Set up REAPER'
  );
  assert.equal(
    setupJourneyActionLabel(
      { kind: 'summary' },
      { id: 'continue_integration_setup', label: 'Continue setup' }
    ),
    'Continue setup'
  );
  assert.deepEqual(
    setupJourneyReceiptRows(installQuestJourney({ handoff }), summary).find(
      row => row[0] === 'Installed'
    ),
    ['Installed', 'reaper-plugin 0.6.0']
  );

  // Without a valid plugin handoff there is nothing to open.
  for (const step of [
    { kind: 'summary' },
    { kind: 'summary', handoff: { ...handoff, source: 'host' } },
    { kind: 'summary', handoff: { ...handoff, plugin_id: '../other' } },
    { kind: 'summary', handoff: { ...handoff, id: '' } }
  ]) {
    assert.equal(integrationHandoffNavigation(step, 'continue_integration_setup'), null);
  }
  assert.equal(integrationHandoffNavigation({ kind: 'summary', handoff }, 'open_project'), null);
});

test('an install summary always offers the Plugins page', () => {
  assert.equal(PLUGINS_PAGE_URL, '/plugins');
  assert.deepEqual(integrationHandoffNavigation({ kind: 'summary' }, 'open_plugins'), {
    kind: 'location',
    url: '/plugins'
  });
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

test('two launch screens separate the group from canonical project readiness', () => {
  const journey = {
    journey: { workspace_launch: { group_title: 'Create Group', group_name: 'Group' } },
    receipts: {},
    steps: [
      {
        kind: 'project_connect',
        status: 'active',
        preparation: { exists: false }
      }
    ]
  };
  assert.equal(WORKSPACE_LAUNCH_DESCRIPTION, 'Create your group, then create a workspace.');
  assert.deepEqual(
    workspaceLaunchStages(journey).map(stage => [stage.id, stage.title]),
    [
      ['group', 'Create Group'],
      ['workspace', 'Create New Workspace']
    ]
  );
  assert.equal(workspaceLaunchStages(journey)[0].enabled, true);
  assert.equal(workspaceLaunchStages(journey)[1].enabled, false);
  // The group existing is the only gate on the workspace screen.
  journey.steps[0].preparation.exists = true;
  assert.equal(workspaceLaunchStages(journey)[0].complete, true);
  assert.equal(workspaceLaunchStages(journey)[1].enabled, true);
  // Historical resource IDs do not establish readiness after a regression.
  journey.receipts.project_workspace_id = 'previous';
  assert.equal(workspaceLaunchStages(journey)[1].complete, false);
  journey.steps[0].status = 'complete';
  assert.equal(workspaceLaunchStages(journey)[1].complete, true);
});

test('Recommended and None launch paths can reach an explicit standalone review without a Home', () => {
  const base = policy => ({
    journey: { workspace_launch: { group_title: 'Create Group', group_name: 'Group' } },
    receipts: {},
    steps: [
      {
        kind: 'project_connect',
        status: 'active',
        preparation: {
          exists: false,
          group_policy: policy,
          available_compositions: policy === 'none' ? ['standalone'] : ['grouped', 'standalone']
        }
      }
    ]
  });
  const recommended = workspaceLaunchStages(base('recommended'));
  assert.equal(recommended.length, 2);
  assert.equal(recommended.find(stage => stage.id === 'group').complete, false);
  assert.equal(recommended.find(stage => stage.id === 'workspace').enabled, true);
  const none = workspaceLaunchStages(base('none'));
  assert.equal(none.find(stage => stage.id === 'group').complete, true);
  assert.equal(none.find(stage => stage.id === 'workspace').enabled, true);
});

test('an unmet integration precondition offers only the install quest', () => {
  const journey = {
    journey: { workspace_launch: { group_title: 'Create Group', group_name: 'Group' } },
    precondition: {
      reason_code: 'integration_disabled',
      guidance: 'Review and enable the required integration to continue.',
      install_quest_id: 'install_ori_reaper',
      integration: {
        plugin_id: 'reaper-plugin',
        installed_version: '0.6.0',
        expected_version: '0.6.0',
        enabled: false,
        verified: true
      }
    },
    steps: []
  };
  const view = setupJourneyPreconditionView(journey);
  assert.equal(view.guidance, 'Review and enable the required integration to continue.');
  assert.deepEqual(view.installDetail, { source: 'host', quest_id: 'install_ori_reaper' });
  assert.ok(view.rows.some(([label, value]) => label === 'Enabled' && value === 'Not yet'));
  assert.equal(setupJourneyPreconditionView({ ...journey, precondition: undefined }), null);
  assert.equal(
    setupJourneyPreconditionView({
      ...journey,
      precondition: { ...journey.precondition, install_quest_id: '../other' }
    }).installDetail,
    null
  );
});

const missingMusicProvider = () => ({
  plugin_id: 'music-project-management',
  display_name: 'Music Project Management',
  reviewed: true,
  minimum_version: '0.1.0',
  template_id: 'plugin:reaper-plugin:reaper-song',
  installed: false,
  enabled: false,
  reason: 'plugin_install_required',
  summary: 'Reaper Song needs Music Production Home, which comes from a separate plugin.',
  detail:
    "Install Music Project Management to add it. Installing this blueprint's plugin did not add it.",
  actions: ['install_plugin', 'manage_plugins', 'change_blueprint']
});

test('the group screen names a missing Home provider in its receipt', () => {
  const step = { kind: 'project_connect', home_provider: missingMusicProvider() };
  const rows = setupJourneyReceiptRows({ receipts: {} }, step);
  assert.deepEqual(rows, [['Home provider', 'music-project-management · Not installed']]);
  assert.equal(homeProviderReceiptRow(null), null);
  // A version is shown only once the provider is installed.
  assert.deepEqual(homeProviderReceiptRow({ ...missingMusicProvider(), version: '0.1.0' }), [
    'Home provider',
    'music-project-management · Not installed'
  ]);
});

test('only a reviewed provider with an install action gets an in-quest install', () => {
  assert.deepEqual(homeProviderOffer(missingMusicProvider()), {
    action: 'install_plugin',
    label: 'Install Music Project Management…',
    confirm: 'Install',
    disclose: true
  });
  const unreviewed = {
    ...missingMusicProvider(),
    reviewed: false,
    display_name: '',
    actions: ['manage_plugins', 'change_blueprint']
  };
  assert.equal(homeProviderOffer(unreviewed), null);
  // A reviewed flag alone is not an offer: the derivation must name the action.
  assert.equal(homeProviderOffer({ ...missingMusicProvider(), actions: ['manage_plugins'] }), null);
});

test('a switched-off provider offers Enable and an incompatible one Review update', () => {
  const disabled = {
    ...missingMusicProvider(),
    installed: true,
    enabled: false,
    version: '0.1.0',
    reason: 'plugin_enable_required',
    summary: 'Music Project Management is installed but switched off.',
    detail: 'Enable it so Reaper Song can use Music Production Home.',
    actions: ['enable_plugin', 'manage_plugins']
  };
  // Enable applies directly: its components were disclosed at install.
  assert.deepEqual(homeProviderOffer(disabled), {
    action: 'enable_plugin',
    label: 'Enable Music Project Management',
    confirm: '',
    disclose: false
  });
  assert.deepEqual(homeProviderReceiptRow(disabled), [
    'Home provider',
    'music-project-management 0.1.0 · Installed · Switched off'
  ]);
  const incompatible = {
    ...disabled,
    enabled: true,
    reason: 'plugin_update_required',
    actions: ['review_plugin_update', 'manage_plugins']
  };
  assert.deepEqual(homeProviderOffer(incompatible), {
    action: 'review_plugin_update',
    label: 'Review update',
    confirm: 'Update',
    disclose: true
  });
  assert.deepEqual(homeProviderReceiptRow(incompatible), [
    'Home provider',
    'music-project-management 0.1.0 · Installed · Enabled'
  ]);
  // An unreviewed provider is sent to Plugins in every state.
  assert.equal(homeProviderOffer({ ...disabled, reviewed: false, display_name: '' }), null);
  assert.equal(homeProviderOffer({ ...incompatible, reviewed: false, display_name: '' }), null);
});

test('an update that asks for nothing new says so, in the card words', () => {
  const provider = missingMusicProvider();
  assert.equal(
    homeProviderDisclosureIntro(provider, 'review_plugin_update', { changed: false }),
    'Music Project Management asks for nothing new. Updating changes only its version.'
  );
  assert.equal(
    homeProviderDisclosureIntro(provider, 'review_plugin_update', { changed: true }),
    'Music Project Management will be able to do the following on this computer:'
  );
  assert.equal(
    homeProviderDisclosureIntro(provider, 'install_plugin', {}),
    'Music Project Management will be able to do the following on this computer:'
  );
});

test('an older payload without home_provider still reads as unverifiable', () => {
  // The step shape Ori served before home_provider existed: blocked, a partial
  // preparation or none, no actions. It must render the old fallback.
  const step = {
    kind: 'project_connect',
    status: 'blocked',
    reason_code: 'owner_unavailable',
    preparation: { exists: false, name: 'Music Production Home', group_policy: 'required' },
    actions: []
  };
  const journey = {
    journey: { workspace_launch: { group_title: 'Create Group', group_name: 'Group' } },
    receipts: {},
    steps: [step]
  };
  assert.equal(groupBuildState(journey), 'unavailable');
  assert.equal(homeProviderOffer(step.home_provider), null);
  assert.deepEqual(setupJourneyReceiptRows(journey, step), []);
});

test('the install disclosure states the release, the reviewed floor, and the source', () => {
  const rows = homeProviderDisclosureRows(missingMusicProvider(), {
    release: '0.1.0',
    source: 'https://github.com/johnjallday/music-project-management@f92c919'
  });
  assert.deepEqual(rows, [
    ['Release', '0.1.0'],
    ['Minimum reviewed version', '0.1.0'],
    ['Installed from', 'https://github.com/johnjallday/music-project-management@f92c919']
  ]);
  assert.deepEqual(homeProviderDisclosureRows({}, {}), []);
});

test('a refused install shows the endpoint outcome; a transport failure its own message', () => {
  const refused = homeProviderRecoveryFailure({
    ok: false,
    status: 409,
    data: {
      outcome: {
        summary: 'This plugin changed while you were reviewing it.',
        detail: 'Nothing was applied. Review the current details and confirm again.'
      }
    },
    error: 'This changed while you were working. Review it and try again.'
  });
  assert.equal(refused.message, 'This plugin changed while you were reviewing it.');
  assert.equal(
    refused.detail,
    'Nothing was applied. Review the current details and confirm again.'
  );
  assert.equal(refused.reread, true);
  const failedInstall = homeProviderRecoveryFailure({
    ok: false,
    status: 409,
    data: {
      outcome: {
        summary: 'Ori could not find a release of Music Project Management to install.',
        detail: 'Nothing was installed. Try again later, or update Ori.',
        steps: [{ name: 'preview', succeeded: false, message: 'Ori could not verify a release.' }]
      }
    }
  });
  assert.equal(
    failedInstall.detail,
    'Ori could not verify a release. Nothing was installed. Try again later, or update Ori.'
  );
  const offline = homeProviderRecoveryFailure({
    ok: false,
    status: 0,
    data: {},
    error: 'Ori could not reach the server. Check your connection and try again.'
  });
  assert.equal(
    offline.message,
    'Ori could not reach the server. Check your connection and try again.'
  );
  assert.equal(offline.reread, false);
});

test('a missing provider keeps the workspace screen closed', () => {
  const journey = {
    journey: { workspace_launch: { group_title: 'Create Group', group_name: 'Group' } },
    receipts: {},
    steps: [
      {
        kind: 'project_connect',
        status: 'blocked',
        reason_code: 'home_provider_missing',
        preparation: { exists: false, name: 'Music Production Home', group_policy: 'required' },
        home_provider: missingMusicProvider()
      }
    ]
  };
  const stages = workspaceLaunchStages(journey);
  assert.equal(stages[0].complete, false);
  assert.equal(stages[1].enabled, false);
});

test('an incompatible root offers Start over on its own restart route', () => {
  const incompatible = {
    run_kind: 'root',
    declaration_incompatible: true,
    journey: { source: 'plugin', plugin_id: 'reaper-plugin', id: 'reaper_setup' }
  };
  assert.equal(
    START_OVER_EXPLANATION,
    'Your group, project and team stay. Only your setup progress is reset.'
  );
  assert.deepEqual(setupJourneyStartOverView(incompatible), {
    label: 'Start over',
    explanation: START_OVER_EXPLANATION,
    url: '/api/setup-quests/reaper-plugin/reaper_setup/restart'
  });
  assert.equal(
    setupJourneyStartOverView({
      ...incompatible,
      journey: { source: 'host', id: 'email_ops_setup' }
    }).url,
    '/api/host-setup-quests/email_ops_setup/restart'
  );
  assert.equal(
    setupJourneyStartOverView({ ...incompatible, journey: { id: 'music_setup' } }).url,
    '/api/personal-assistant/setup-journey/restart'
  );
  // Compatible roots, children and user-template quests have no Start over.
  assert.equal(
    setupJourneyStartOverView({ ...incompatible, declaration_incompatible: false }),
    null
  );
  assert.equal(setupJourneyStartOverView({ ...incompatible, run_kind: 'child' }), null);
  assert.equal(
    setupJourneyStartOverView({
      ...incompatible,
      journey: { source: 'user_template', template_id: 'song', attachment_id: 'quest', id: 'x' }
    }),
    null
  );
});

test('the launch view carries no preparation screen or acknowledgement', async () => {
  const source = await readFile(new URL('./setup-journey.js', import.meta.url), 'utf8');
  for (const retired of [
    'checkPreparation',
    'acknowledgePreparation',
    'preparationCheck',
    'acknowledge_preparation',
    'runtime_instructions',
    "'acknowledged'"
  ]) {
    assert.equal(source.includes(retired), false, `${retired} is still referenced`);
  }
});

test('idempotency keys are non-empty and distinct', () => {
  const first = newJourneyIdempotencyKey();
  const second = newJourneyIdempotencyKey();
  assert.ok(first.length >= 16);
  assert.notEqual(first, second);
});
