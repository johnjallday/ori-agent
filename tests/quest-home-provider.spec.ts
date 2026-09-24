import { expect, test, type Page } from '@playwright/test';

// The Set up REAPER group screen when the split blueprint's Home provider
// (Music Project Management) is not ready. The quest run and the blueprint
// recovery endpoint are mocked; the page, the quest modal, and the shared
// recovery client are the real ones.

const questRoot = '/api/setup-quests/reaper-plugin/reaper_setup';
const templateID = 'plugin:reaper-plugin:reaper-song';
const questURL = '/?setup=quest&plugin=reaper-plugin&quest=reaper_setup';

type Provider = Record<string, unknown>;

const missingProvider = (): Provider => ({
  plugin_id: 'music-project-management',
  display_name: 'Music Project Management',
  reviewed: true,
  minimum_version: '0.1.0',
  template_id: templateID,
  installed: false,
  enabled: false,
  reason: 'plugin_install_required',
  summary: 'Reaper Song needs Music Production Home, which comes from a separate plugin.',
  detail:
    "Install Music Project Management to add it. Installing this blueprint's plugin did not add it.",
  actions: ['install_plugin', 'manage_plugins', 'change_blueprint']
});

const disabledProvider = (): Provider => ({
  ...missingProvider(),
  installed: true,
  enabled: false,
  version: '0.1.0',
  generation: 12,
  reason: 'plugin_enable_required',
  summary: 'Music Project Management is installed but switched off.',
  detail: 'Enable it so Reaper Song can use Music Production Home.',
  actions: ['enable_plugin', 'manage_plugins']
});

const updateProvider = (): Provider => ({
  ...missingProvider(),
  installed: true,
  enabled: true,
  version: '0.1.0',
  generation: 14,
  reason: 'plugin_update_required',
  summary: 'Music Project Management does not accept Reaper Song projects yet.',
  detail:
    "Updating it or this blueprint's plugin may add that. Ori never assumes it from matching names.",
  actions: ['review_plugin_update', 'manage_plugins']
});

function journey(provider: Provider | null) {
  const project = provider
    ? {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect a project',
        status: 'blocked',
        reason_code: 'home_provider_missing',
        guidance: 'Install the plugin that provides this Home, then check again.',
        preparation: {
          exists: false,
          name: 'Music Production Home',
          template_id: templateID,
          group_policy: 'required',
          available_compositions: ['grouped']
        },
        home_provider: provider
      }
    : {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect a project',
        status: 'active',
        preparation: {
          exists: false,
          name: 'Music Production Home',
          template_id: templateID,
          group_policy: 'required',
          available_compositions: ['grouped']
        },
        actions: [{ id: 'review_create_group', label: 'Review Group', effect: 'review' }]
      };
  return {
    run_id: 'provider-quest-root',
    run_kind: 'root',
    state_revision: 4,
    lifecycle_state: 'needs_attention',
    current_step_id: 'project',
    dismissed: false,
    receipts: { integration_plugin_id: 'reaper-plugin', integration_version: '0.8.0' },
    journey: {
      plugin_id: 'reaper-plugin',
      id: 'reaper_setup',
      title: 'Set up REAPER',
      workspace_launch: {
        group_title: 'Build Your Music Production Group',
        group_name: 'Music Production'
      }
    },
    steps: [
      project,
      { id: 'workspace', kind: 'workspace_setup', title: 'Choose a mode', status: 'pending' },
      { id: 'staffing', kind: 'assistant_program_staffing', title: 'Add roles', status: 'pending' },
      { id: 'summary', kind: 'summary', title: 'Review setup', status: 'pending' }
    ]
  };
}

type RecoveryCall = { url: string; body: Record<string, unknown> };

// mockQuest serves the quest with `provider` until a recovery confirmation
// succeeds, then with the provider ready. `confirm` answers confirmations.
async function mockQuest(
  page: Page,
  provider: Provider,
  confirm: () => { status: number; json: unknown } = () => ({
    status: 200,
    json: {
      outcome: { action: 'install_plugin', completed: true, summary: 'Installed and enabled.' }
    }
  })
) {
  const state = { ready: false, reads: 0 };
  const recoveries: RecoveryCall[] = [];
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, current_step: 'complete' } })
  );
  await page.route('**/api/plugins', route =>
    route.fulfill({
      json: {
        plugins: [
          {
            name: 'reaper-plugin',
            version: '0.8.0',
            format: 'claude',
            enabled: true,
            description: 'Music production integration.'
          }
        ]
      }
    })
  );
  await page.route('**/api/personal-assistant/setup-journey**', route =>
    route.fulfill({
      status: 409,
      json: { error: { guidance: 'No assistant relationship accepted.' } }
    })
  );
  await page.route('**/api/setup-quests**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/setup-quests') {
      await route.fulfill({ json: { quests: [] } });
      return;
    }
    expect(path.startsWith(questRoot)).toBeTruthy();
    if (route.request().method() === 'GET') state.reads++;
    await route.fulfill({ json: { setup_journey: journey(state.ready ? null : provider) } });
  });
  await page.route('**/api/project-templates/*/plugin-recovery', async route => {
    const body = JSON.parse(route.request().postData() || '{}');
    recoveries.push({ url: new URL(route.request().url()).pathname, body });
    if (!body.confirm) {
      await route.fulfill({
        json: {
          readiness: {},
          release: '0.1.0',
          source:
            'https://github.com/johnjallday/music-project-management#sha=5f748d2de4457ac9dd02ea1ec31e34e1493744cf',
          changed: false,
          trust: {
            Name: 'music-project-management',
            Skills: ['music-project-management'],
            AssistantProgramHomes: ['music-producer-assistant — Music Production Home']
          }
        }
      });
      return;
    }
    const answer = confirm();
    if (answer.status === 200) state.ready = true;
    await route.fulfill({ status: answer.status, json: answer.json });
  });
  return { state, recoveries };
}

async function openQuest(page: Page) {
  await page.goto(questURL);
  const dialog = page.locator('#specialistSetupJourneyModal');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
    'Build Your Music Production Group'
  );
  return dialog;
}

test('a missing reviewed Home provider is installed from the group screen', async ({
  page
}, testInfo) => {
  const { state, recoveries } = await mockQuest(page, missingProvider());
  const dialog = await openQuest(page);

  await expect(dialog.locator('#specialistSetupJourneyStepDescription')).toHaveText(
    "Reaper Song needs Music Production Home, which comes from a separate plugin. Install Music Project Management to add it. Installing this blueprint's plugin did not add it."
  );
  await expect(dialog).not.toContainText('could not be verified');
  await expect(dialog.locator('#specialistSetupJourneyReceipt')).toContainText(
    'Home provider: music-project-management · Not installed'
  );
  const install = dialog.getByRole('button', { name: 'Install Music Project Management…' });
  await expect(install).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Open Plugins', exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Check Again', exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toHaveCount(0);
  await dialog.screenshot({ path: testInfo.outputPath('provider-offer.png') });

  await install.click();
  const review = dialog.locator('.setup-journey__provider-review');
  await expect(review).toContainText(
    'Music Project Management will be able to do the following on this computer:'
  );
  await expect(review).toContainText('Release: 0.1.0');
  await expect(review).toContainText('Minimum reviewed version: 0.1.0');
  await expect(review).toContainText(
    'Installed from: https://github.com/johnjallday/music-project-management#sha=5f748d2de4457ac9dd02ea1ec31e34e1493744cf'
  );
  await expect(review).toContainText('music-producer-assistant — Music Production Home');
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
  expect(recoveries).toHaveLength(1);
  expect(recoveries[0].url).toBe(
    '/api/project-templates/plugin%3Areaper-plugin%3Areaper-song/plugin-recovery'
  );
  expect(recoveries[0].body).toEqual({
    action: 'install_plugin',
    plugin: 'music-project-management',
    confirm: false,
    generation: 0
  });
  await dialog.screenshot({ path: testInfo.outputPath('provider-disclosure.png') });

  const readsBeforeConfirm = state.reads;
  await dialog.getByRole('button', { name: 'Install', exact: true }).click();
  // The step re-reads on its own; nobody pressed Check Again.
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toBeVisible();
  expect(recoveries[1].body).toEqual({
    action: 'install_plugin',
    plugin: 'music-project-management',
    confirm: true,
    generation: 0,
    release: '0.1.0'
  });
  expect(state.reads).toBeGreaterThan(readsBeforeConfirm);
  await expect(dialog.locator('#specialistSetupJourneyLiveStatus')).toHaveText(
    'Installed and enabled.'
  );
  await expect(
    page.locator('#toastContainer .toast-success', {
      hasText: 'Music Project Management installed and enabled.'
    })
  ).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('provider-installed.png') });
});

test('cancelling the disclosure installs nothing', async ({ page }) => {
  const { recoveries } = await mockQuest(page, missingProvider());
  const dialog = await openQuest(page);
  await dialog.getByRole('button', { name: 'Install Music Project Management…' }).click();
  await expect(dialog.locator('.setup-journey__provider-review')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog.locator('.setup-journey__provider-review')).toHaveCount(0);
  await expect(
    dialog.getByRole('button', { name: 'Install Music Project Management…' })
  ).toBeVisible();
  expect(recoveries.map(call => call.body.confirm)).toEqual([false]);
});

test('a refused confirmation keeps the offer and says why', async ({ page }) => {
  const { recoveries } = await mockQuest(page, missingProvider(), () => ({
    status: 409,
    json: {
      readiness: {},
      outcome: {
        action: 'install_plugin',
        summary: 'This plugin changed while you were reviewing it.',
        detail: 'Nothing was applied. Review the current details and confirm again.'
      }
    }
  }));
  const dialog = await openQuest(page);
  await dialog.getByRole('button', { name: 'Install Music Project Management…' }).click();
  await dialog.getByRole('button', { name: 'Install', exact: true }).click();
  await expect(
    dialog.getByRole('alert').filter({ hasText: 'changed while you were reviewing' })
  ).toHaveText('This plugin changed while you were reviewing it.');
  await expect(dialog).toContainText(
    'Nothing was applied. Review the current details and confirm again.'
  );
  await expect(
    dialog.getByRole('button', { name: 'Install Music Project Management…' })
  ).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toHaveCount(0);
  // A refusal never claims success.
  await expect(page.locator('#toastContainer .toast-success')).toHaveCount(0);
  expect(recoveries).toHaveLength(2);
});

test('a switched-off provider is enabled from the group screen', async ({ page }, testInfo) => {
  const { recoveries } = await mockQuest(page, disabledProvider(), () => ({
    status: 200,
    json: { outcome: { action: 'enable_plugin', completed: true, summary: 'Enabled.' } }
  }));
  const dialog = await openQuest(page);
  await expect(dialog.locator('#specialistSetupJourneyStepDescription')).toHaveText(
    'Music Project Management is installed but switched off. Enable it so Reaper Song can use Music Production Home.'
  );
  await expect(dialog.locator('#specialistSetupJourneyReceipt')).toContainText(
    'Home provider: music-project-management 0.1.0 · Installed · Switched off'
  );
  await dialog.screenshot({ path: testInfo.outputPath('provider-disabled.png') });
  await dialog
    .getByRole('button', { name: 'Enable Music Project Management', exact: true })
    .click();
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toBeVisible();
  await expect(
    page.locator('#toastContainer .toast-success', {
      hasText: 'Music Project Management enabled.'
    })
  ).toBeVisible();
  // Enable needs no disclosure: one confirmed call, at the read's generation.
  expect(recoveries.map(call => call.body)).toEqual([
    { action: 'enable_plugin', plugin: 'music-project-management', confirm: true, generation: 12 }
  ]);
});

test('a provider that needs an update is reviewed before it is updated', async ({ page }) => {
  const { recoveries } = await mockQuest(page, updateProvider(), () => ({
    status: 200,
    json: { outcome: { action: 'review_plugin_update', completed: true, summary: 'Updated.' } }
  }));
  const dialog = await openQuest(page);
  await expect(dialog.locator('#specialistSetupJourneyStepDescription')).toContainText(
    'Music Project Management does not accept Reaper Song projects yet.'
  );
  await dialog.getByRole('button', { name: 'Review update', exact: true }).click();
  await expect(dialog.locator('.setup-journey__provider-review')).toContainText(
    'Music Project Management asks for nothing new. Updating changes only its version.'
  );
  await dialog.getByRole('button', { name: 'Update', exact: true }).click();
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toBeVisible();
  expect(
    recoveries.map(call => [call.body.action, call.body.confirm, call.body.generation])
  ).toEqual([
    ['review_plugin_update', false, 14],
    ['review_plugin_update', true, 14]
  ]);
});

test('a provider Ori has not reviewed is left to the Plugins page', async ({ page }) => {
  const unreviewed = {
    ...missingProvider(),
    plugin_id: 'community-home',
    display_name: '',
    reviewed: false,
    minimum_version: '',
    summary: 'Reaper Song needs Music Production Home, which comes from a separate plugin.',
    detail:
      "Install and enable community-home from the Plugins page, then come back. Installing this blueprint's plugin did not add it.",
    actions: ['manage_plugins', 'change_blueprint']
  };
  const { recoveries } = await mockQuest(page, unreviewed);
  const dialog = await openQuest(page);
  await expect(dialog.locator('#specialistSetupJourneyStepDescription')).toContainText(
    'Install and enable community-home from the Plugins page'
  );
  await expect(dialog.getByRole('button', { name: /^Install / })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /^Enable / })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Check Again', exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: 'Open Plugins', exact: true }).click();
  await page.waitForURL('**/plugins');
  expect(recoveries).toHaveLength(0);
});
