import { expect, test, type Page, type Route } from '@playwright/test';

// The install split end to end in the browser, against mocked setup routes:
// the host-generated install quest, its handoff into the plugin's two-screen
// quest, the integration precondition panel, and Start over.

const installRoot = '/api/host-setup-quests/install_ori_reaper';
const pluginRoot = '/api/setup-quests/reaper-plugin/reaper_setup';

type Journey = Record<string, any>;

function installQuest(ready: boolean): Journey {
  return {
    run_id: 'install-root',
    run_kind: 'root',
    state_revision: ready ? 5 : 2,
    lifecycle_state: ready ? 'ready' : 'in_progress',
    current_step_id: ready ? '' : 'integration',
    journey: {
      source: 'host',
      id: 'install_ori_reaper',
      title: 'Install Ori REAPER Plugin',
      description: "Install and verify Ori's reviewed REAPER integration before setting it up."
    },
    receipts: ready ? { integration_plugin_id: 'reaper-plugin', integration_version: '0.6.0' } : {},
    steps: [
      {
        id: 'integration',
        kind: 'integration_install',
        title: 'Install Ori REAPER Plugin',
        description: 'An Ori integration, not an audio plug-in.',
        status: ready ? 'complete' : 'active',
        integration: {
          plugin_id: 'reaper-plugin',
          expected_version: '0.6.0',
          installed_version: ready ? '0.6.0' : '',
          enabled: ready,
          verified: ready,
          release_ready: true
        },
        actions: ready
          ? [{ id: 'manage_integration', label: 'Manage integration', effect: 'navigation' }]
          : [{ id: 'review_install', label: 'Install plugin', effect: 'review' }]
      },
      {
        id: 'summary',
        kind: 'summary',
        title: 'REAPER plugin ready',
        description: "Setup continues in the REAPER plugin's own guided setup.",
        status: ready ? 'complete' : 'pending',
        actions: ready
          ? [
              { id: 'continue_integration_setup', label: 'Continue setup', effect: 'navigation' },
              { id: 'open_plugins', label: 'Open Plugins', effect: 'navigation' }
            ]
          : [],
        ...(ready
          ? {
              handoff: {
                source: 'plugin',
                plugin_id: 'reaper-plugin',
                id: 'reaper_setup',
                title: 'Set up REAPER'
              }
            }
          : {})
      }
    ]
  };
}

function pluginQuest(options: { groupExists?: boolean; runID?: string } = {}): Journey {
  return {
    run_id: options.runID || 'plugin-root',
    run_kind: 'root',
    state_revision: 3,
    lifecycle_state: 'in_progress',
    current_step_id: 'project',
    journey: {
      plugin_id: 'reaper-plugin',
      id: 'reaper_setup',
      version: 2,
      title: 'Set up REAPER',
      workspace_launch: {
        group_title: 'Build Your Music Production Group',
        group_name: 'Music Production'
      }
    },
    receipts: { integration_plugin_id: 'reaper-plugin', integration_version: '0.6.0' },
    steps: [
      {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect a project',
        status: 'active',
        preparation: options.groupExists
          ? { exists: true, name: 'Music Production', group_id: 'existing-home' }
          : { exists: false, name: 'Music Production' },
        actions: [{ id: 'review_create_group', label: 'Review Group', effect: 'review' }]
      },
      { id: 'workspace', kind: 'workspace_setup', title: 'Choose a mode', status: 'pending' },
      { id: 'staffing', kind: 'assistant_program_staffing', title: 'Add roles', status: 'pending' },
      { id: 'summary', kind: 'summary', title: 'Review setup', status: 'pending' }
    ]
  };
}

function preconditionQuest(): Journey {
  const journey = pluginQuest();
  journey.lifecycle_state = 'needs_attention';
  journey.receipts = {};
  journey.precondition = {
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
  };
  journey.steps = journey.steps.map((step: Journey) => ({
    ...step,
    status: 'blocked',
    reason_code: 'integration_disabled',
    actions: []
  }));
  return journey;
}

function incompatibleQuest(): Journey {
  const journey = pluginQuest({ runID: 'retired-root' });
  journey.lifecycle_state = 'needs_attention';
  journey.declaration_incompatible = true;
  journey.steps = journey.steps.map((step: Journey, index: number) => ({
    ...step,
    status: index === 0 ? 'blocked' : 'pending',
    reason_code: index === 0 ? 'declaration_invalid' : '',
    guidance:
      index === 0
        ? 'This setup definition changed and needs a supported upgrade before setup can continue.'
        : '',
    preparation: undefined,
    actions: []
  }));
  return journey;
}

type Server = {
  install: Journey;
  plugin: Journey;
  installed: boolean;
  calls: string[];
  restarts: number;
};

async function mockSetup(page: Page, server: Server) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, current_step: 'complete' } })
  );
  await page.route(
    url => url.pathname.startsWith('/api/'),
    async (route: Route) => {
      const url = new URL(route.request().url());
      const path = url.pathname;
      const method = route.request().method();
      if (path === '/api/plugins') {
        return route.fulfill({
          json: {
            plugins: server.installed
              ? [{ name: 'reaper-plugin', version: '0.6.0', format: 'claude', enabled: true }]
              : []
          }
        });
      }
      if (path === '/api/setup-quests') {
        return route.fulfill({
          json: {
            quests: server.installed
              ? [
                  {
                    source: 'plugin',
                    plugin_id: 'reaper-plugin',
                    id: 'reaper_setup',
                    title: 'Set up REAPER',
                    ownership: 'plugin'
                  }
                ]
              : [
                  {
                    source: 'host',
                    id: 'install_ori_reaper',
                    title: 'Install Ori REAPER Plugin',
                    ownership: 'host',
                    integration_key: 'ori_reaper',
                    display_name: 'REAPER',
                    publisher_label: 'Ori'
                  }
                ]
          }
        });
      }
      if (path.startsWith(installRoot)) {
        server.calls.push(`${method} ${path}`);
        if (path.endsWith('/actions/review_install')) {
          return route.fulfill({
            json: {
              setup_journey: server.install,
              review: {
                token: 'install-review',
                commit_action: 'install',
                expires_at: '2035-01-01T00:00:00Z',
                integration: {
                  ...server.install.steps[0].integration,
                  publisher: 'Ori',
                  source_label: 'johnjallday/reaper-plugin',
                  supported_platforms: ['darwin/arm64']
                }
              }
            }
          });
        }
        return route.fulfill({ json: { setup_journey: server.install } });
      }
      if (path.startsWith(pluginRoot)) {
        server.calls.push(`${method} ${path}`);
        if (path === `${pluginRoot}/restart`) {
          server.restarts++;
          server.plugin = pluginQuest({ groupExists: true, runID: 'fresh-root' });
        }
        return route.fulfill({ json: { setup_journey: server.plugin } });
      }
      return route.fallback();
    }
  );
}

function newServer(overrides: Partial<Server> = {}): Server {
  return {
    install: installQuest(false),
    plugin: pluginQuest(),
    installed: false,
    calls: [],
    restarts: 0,
    ...overrides
  };
}

// At phone width the fixed Ori Help root (#oriGuideRoot) is a full-width strip
// that intercepts pointer events at the bottom of the viewport. That overlay is
// outside this spec, so let clicks pass through it.
async function clearGuideOverlay(page: Page) {
  await page.addStyleTag({ content: '#oriGuideRoot { pointer-events: none !important; }' });
}

for (const width of [1280, 390]) {
  test(`before install, Plugins lists the integration and its quest has one actionable step at ${width}px`, async ({
    page
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const server = newServer();
    await mockSetup(page, server);
    await page.goto('/plugins');
    await clearGuideOverlay(page);

    const available = page.locator('#availableIntegrationsCard');
    await expect(available).toBeVisible();
    const row = available.locator('[data-integration-key="ori_reaper"]');
    await expect(row).toContainText('REAPER');
    await expect(row).toContainText('Reviewed by Ori');
    const guided = row.getByRole('link', { name: 'Guided Setup', exact: true });
    await expect(guided).toHaveAttribute(
      'href',
      '/?setup=quest&source=host&quest=install_ori_reaper'
    );
    await page.screenshot({ path: testInfo.outputPath(`available-integrations-${width}.png`) });

    await guided.click();
    await page.waitForURL('**/?setup=quest&source=host&quest=install_ori_reaper');
    await clearGuideOverlay(page);
    const dialog = page.locator('#specialistSetupJourneyModal');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('#specialistSetupJourneyTitle')).toHaveText(
      'Install Ori REAPER Plugin'
    );
    const rail = dialog.locator('.setup-journey__step-button');
    await expect(rail).toHaveCount(2);
    await expect(rail.nth(0)).toHaveAttribute('aria-current', 'step');
    await expect(dialog.locator('#specialistSetupJourneyStepState')).toHaveText(
      'Step 1 of 2 · Next step'
    );
    // Before install the receipt names only the version to install.
    const receipt = dialog.locator('#specialistSetupJourneyReceipt');
    await expect(receipt).toContainText('Installed: No');
    await expect(receipt).toContainText('Version to install: 0.6.0');
    await expect(receipt).not.toContainText('Enabled');
    await expect(receipt).not.toContainText('Verification');
    const install = dialog
      .locator('#specialistSetupJourneyActions')
      .getByRole('button', { name: 'Install plugin', exact: true });
    await expect(install).toBeVisible();

    // The install button still reviews first; nothing is committed here.
    await install.click();
    await expect(dialog.getByRole('heading', { name: 'Install Ori REAPER Plugin?' })).toBeFocused();
    const review = dialog.locator('#specialistSetupJourneyReview');
    await expect(review).toContainText(
      'Ori downloads the reviewed 0.6.0 release from johnjallday/reaper-plugin, checks its fingerprint, and installs it. Nothing runs until you enable it.'
    );
    await expect(review.getByRole('button', { name: 'Install', exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`install-review-${width}.png`) });
    await review.getByRole('button', { name: 'Back', exact: true }).click();
    expect(server.calls.some(call => call.endsWith('/actions/install'))).toBe(false);

    // The summary step offers nothing to do until the plugin is installed.
    await rail.nth(1).click();
    await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
      'REAPER plugin ready'
    );
    await expect(dialog.locator('#specialistSetupJourneyActions button')).toHaveCount(0);
    await expect(dialog).not.toContainText('Build Your Music Production Group');
    expect(server.calls.every(call => call.includes(installRoot))).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`install-quest-${width}.png`) });
  });
}

test('an installed integration hands off into the two-screen plugin quest in place', async ({
  page
}, testInfo) => {
  const server = newServer({ install: installQuest(true), installed: true });
  await mockSetup(page, server);
  await page.goto('/?setup=quest&source=host&quest=install_ori_reaper');
  const dialog = page.locator('#specialistSetupJourneyModal');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
    'REAPER plugin ready'
  );
  await expect(dialog.locator('#specialistSetupJourneyReceipt')).toContainText(
    'Installed: reaper-plugin 0.6.0'
  );
  const continueButton = dialog.getByRole('button', { name: 'Continue: Set up REAPER' });
  await expect(continueButton).toHaveAttribute('data-primary', 'true');
  await expect(dialog.getByRole('button', { name: 'Open Plugins', exact: true })).toBeVisible();
  // A ready install quest is not reopened or dismissed by viewing it.
  expect(server.calls.filter(call => call.startsWith('POST'))).toEqual([]);

  await continueButton.click();
  await expect(dialog.locator('#specialistSetupJourneyTitle')).toHaveText('Set up REAPER');
  await expect(dialog.locator('#specialistSetupJourneyDescription')).toHaveText(
    'Create your group, then create a workspace.'
  );
  const rail = dialog.locator('.setup-journey__step-button');
  await expect(rail).toHaveCount(2);
  await expect(rail.nth(0)).toContainText('Build Your Music Production Group');
  await expect(rail.nth(1)).toContainText('Create New Workspace');
  await expect(dialog.locator('#specialistSetupJourneyStepState')).toHaveText('Step 1 of 2');
  await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toBeVisible();
  await expect(dialog).not.toContainText('Set Up REAPER');
  expect(server.calls).toContain(`GET ${pluginRoot}`);
  expect(server.calls.some(call => call.includes('preparation'))).toBe(false);
  await page.screenshot({ path: testInfo.outputPath('plugin-quest-handoff.png') });
});

test('a disabled integration shows only the precondition panel and opens the install quest', async ({
  page
}, testInfo) => {
  const server = newServer({ plugin: preconditionQuest(), installed: true });
  await mockSetup(page, server);
  await page.goto('/?setup=quest&plugin=reaper-plugin&quest=reaper_setup');
  const dialog = page.locator('#specialistSetupJourneyModal');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
    'The integration needs attention'
  );
  await expect(dialog.locator('#specialistSetupJourneyStepDescription')).toHaveText(
    'Review and enable the required integration to continue.'
  );
  await expect(dialog.locator('#specialistSetupJourneyReceipt')).toContainText('Enabled: Not yet');
  const rail = dialog.locator('.setup-journey__step-button');
  await expect(rail).toHaveCount(2);
  for (const index of [0, 1]) await expect(rail.nth(index)).toBeDisabled();
  const actions = dialog.locator('#specialistSetupJourneyActions button');
  await expect(actions).toHaveCount(1);
  await expect(actions.first()).toHaveText('Open install quest');
  await page.screenshot({ path: testInfo.outputPath('precondition-panel.png') });

  await actions.first().click();
  await expect(dialog.locator('#specialistSetupJourneyTitle')).toHaveText(
    'Install Ori REAPER Plugin'
  );
  expect(server.calls.some(call => call.startsWith(`GET ${installRoot}`))).toBe(true);
});

test('an incompatible saved setup offers Start over and reopens a fresh quest', async ({
  page
}, testInfo) => {
  const server = newServer({ plugin: incompatibleQuest(), installed: true });
  await mockSetup(page, server);
  await page.goto('/?setup=quest&plugin=reaper-plugin&quest=reaper_setup');
  const dialog = page.locator('#specialistSetupJourneyModal');
  await expect(dialog).toBeVisible();
  const receipt = dialog.locator('#specialistSetupJourneyReceipt');
  await expect(receipt).toContainText(
    'This setup definition changed and needs a supported upgrade before setup can continue.'
  );
  await expect(receipt).toContainText(
    'Your group, project and team stay. Only your setup progress is reset.'
  );
  const startOver = dialog.getByRole('button', { name: 'Start over', exact: true });
  await expect(startOver).toHaveAttribute('data-primary', 'true');
  await page.screenshot({ path: testInfo.outputPath('start-over.png') });

  let dialogs = 0;
  page.on('dialog', browserDialog => {
    dialogs++;
    void browserDialog.dismiss();
  });
  await startOver.click();
  await expect.poll(() => server.restarts).toBe(1);
  expect(server.calls).toContain(`POST ${pluginRoot}/restart`);
  expect(dialogs).toBe(0);
  // The fresh root keeps the existing group, so the workspace screen is next.
  await expect(dialog.locator('#specialistSetupJourneyStepState')).toHaveText('Step 2 of 2');
  await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
    'Create New Workspace'
  );
  await expect(dialog.getByRole('button', { name: 'Start over', exact: true })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('after-start-over.png') });
});
