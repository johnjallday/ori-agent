import { expect, test } from '@playwright/test';

// The assistant alias resolves to the host-generated install quest while the
// integration is not verified. Its first step owns the replacement review.
function installQuestJourney() {
  return {
    run_id: 'integration-recovery',
    run_kind: 'root',
    state_revision: 3,
    lifecycle_state: 'in_progress',
    current_step_id: 'integration',
    receipts: {} as Record<string, string>,
    journey: {
      source: 'host',
      id: 'install_ori_reaper',
      title: 'Install Ori REAPER Plugin',
      description: "Install and verify Ori's reviewed REAPER integration before setting it up."
    },
    steps: [
      {
        id: 'integration',
        kind: 'integration_install',
        title: 'Install Ori REAPER Plugin',
        description: 'An Ori integration, not an audio plug-in.',
        status: 'active',
        integration: {
          plugin_id: 'reaper-plugin',
          installed_version: '0.5.0',
          expected_version: '0.5.0',
          enabled: true,
          verified: false,
          release_ready: true,
          replacement_required: true,
          publisher: 'Ori',
          source_label: 'johnjallday/reaper-plugin',
          supported_platforms: ['darwin/arm64'],
          required_host_features: ['specialist_setup_journey_v1'],
          trust: {
            artifacts: [
              {
                sha256: '2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9',
                size: 8780098
              }
            ]
          }
        },
        actions: [
          { id: 'review_update', label: 'Review verified replacement', effect: 'review' },
          { id: 'manage_integration', label: 'Manage integration', effect: 'navigation' }
        ]
      },
      {
        id: 'summary',
        kind: 'summary',
        title: 'REAPER plugin ready',
        description: "Setup continues in the REAPER plugin's own guided setup.",
        status: 'pending',
        actions: [] as Array<Record<string, string>>,
        handoff: undefined as Record<string, string> | undefined
      }
    ]
  };
}

for (const width of [1280, 390]) {
  test(`installed integration recovery requires review and hands off at ${width}px`, async ({
    page
  }, testInfo) => {
    await page.setViewportSize({ width, height: 820 });
    const current = installQuestJourney();
    let reviews = 0;
    const commits: unknown[] = [];
    const paths: string[] = [];
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { completed: true, current_step: 'complete' } })
    );
    const handler = async route => {
      const path = new URL(route.request().url()).pathname;
      paths.push(path);
      if (path.endsWith('/actions/review_update')) {
        reviews++;
        await route.fulfill({
          json: {
            review: {
              token: `review-${reviews}`,
              commit_action: 'update',
              expires_at: '2035-01-01T00:00:00Z',
              integration: current.steps[0].integration
            }
          }
        });
        return;
      }
      if (path.endsWith('/actions/update')) {
        commits.push(route.request().postDataJSON());
        if (commits.length === 1) {
          await route.abort('failed');
          return;
        }
        current.steps[0].integration.verified = true;
        current.steps[0].integration.replacement_required = false;
        current.steps[0].status = 'complete';
        current.steps[0].actions = [
          { id: 'manage_integration', label: 'Manage integration', effect: 'navigation' }
        ];
        current.steps[1].status = 'complete';
        current.steps[1].handoff = {
          source: 'plugin',
          plugin_id: 'reaper-plugin',
          id: 'reaper_setup',
          title: 'Set up REAPER'
        };
        current.steps[1].actions = [
          { id: 'continue_integration_setup', label: 'Continue setup', effect: 'navigation' },
          { id: 'open_plugins', label: 'Open Plugins', effect: 'navigation' }
        ];
        current.receipts = { integration_plugin_id: 'reaper-plugin', integration_version: '0.5.0' };
        current.lifecycle_state = 'ready';
        current.current_step_id = '';
        current.state_revision++;
      }
      await route.fulfill({ json: { setup_journey: current } });
    };
    await page.route('**/api/personal-assistant/setup-journey**', handler);
    await page.route('**/api/host-setup-quests/install_ori_reaper**', handler);

    await page.goto('/?setup=specialist');
    const dialog = page.locator('#specialistSetupJourneyModal');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('#specialistSetupJourneyTitle')).toHaveText(
      'Install Ori REAPER Plugin'
    );
    const receipt = dialog.locator('#specialistSetupJourneyReceipt');
    await expect(receipt).toContainText('Installed: Yes');
    await expect(receipt).toContainText('Enabled: Yes');
    await expect(receipt).toContainText('Not verified for guided setup');
    // The install quest is a plain two-step rail with no launch screens.
    await expect(dialog.locator('.setup-journey__step-button')).toHaveCount(2);
    await expect(
      dialog.getByRole('button', { name: /Build Your Music Production Group/ })
    ).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath('installed-recovery.png') });

    await dialog.getByRole('button', { name: 'Review verified replacement' }).click();
    await expect(
      dialog.getByRole('heading', { name: 'Replace installed integration?' })
    ).toBeFocused();
    const review = dialog.locator('#specialistSetupJourneyReview');
    await expect(review).toContainText('even if the version number is unchanged');
    await expect(review).toContainText('Installed version: 0.5.0');
    await expect(review).toContainText('Reviewed version: 0.5.0');
    // The artifact fingerprint is part of the collapsed technical details.
    const technical = review.locator('details.setup-journey__technical');
    await expect(technical).not.toHaveAttribute('open', '');
    await technical.getByText('Technical details', { exact: true }).click();
    await expect(
      technical.getByText('2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9')
    ).toBeVisible();
    expect(commits).toHaveLength(0);
    await review.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(receipt).toContainText('Not verified for guided setup');
    expect(commits).toHaveLength(0);

    await dialog.getByRole('button', { name: 'Review verified replacement' }).click();
    await review.getByRole('button', { name: 'Replace with reviewed version' }).click();
    await expect(dialog.getByRole('button', { name: 'Retry Confirmed Change' })).toBeVisible();
    await dialog.getByRole('button', { name: 'Retry Confirmed Change' }).click();
    await expect.poll(() => commits.length).toBe(2);
    expect(commits[1]).toEqual(commits[0]);
    // Mutations after the first read address the install quest's own root.
    expect(
      paths.some(path => path.startsWith('/api/host-setup-quests/install_ori_reaper/runs/'))
    ).toBe(true);

    await expect(dialog.getByRole('heading', { name: 'REAPER plugin ready' })).toBeVisible();
    const continueButton = dialog.getByRole('button', { name: 'Continue: Set up REAPER' });
    await expect(continueButton).toBeVisible();
    await expect(continueButton).toHaveAttribute('data-primary', 'true');
    await expect(dialog.getByRole('button', { name: 'Open Plugins', exact: true })).toBeVisible();
    await dialog.getByRole('button', { name: /Install Ori REAPER Plugin/ }).click();
    await expect(receipt).toContainText('Verified release');
    await page.screenshot({ path: testInfo.outputPath('verified-integration.png') });
  });
}
