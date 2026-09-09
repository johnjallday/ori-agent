import { expect, test } from '@playwright/test';

function installedJourney() {
  return {
    run_id: 'integration-recovery',
    state_revision: 3,
    lifecycle_state: 'in_progress',
    current_step_id: 'integration',
    receipts: {},
    journey: {
      id: 'reaper_setup',
      title: 'Set up REAPER',
      workspace_launch: {
        group_title: 'Build Your Music Production Group',
        group_name: 'Music Production',
        runtime_title: 'Set Up REAPER',
        runtime_instructions: 'Prepare Web Remote. Live access is approved per workspace.'
      }
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
        id: 'project',
        kind: 'project_connect',
        status: 'pending',
        preparation: { exists: false, acknowledged: false }
      }
    ]
  };
}

for (const width of [1280, 390]) {
  test(`installed integration recovery requires review and safely resumes at ${width}px`, async ({
    page
  }, testInfo) => {
    await page.setViewportSize({ width, height: 820 });
    const current = installedJourney();
    let reviews = 0;
    let reads = 0;
    const commits: unknown[] = [];
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { completed: true, current_step: 'complete' } })
    );
    await page.route('**/api/personal-assistant/setup-journey**', async route => {
      const path = new URL(route.request().url()).pathname;
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
        current.steps[0].integration!.verified = true;
        current.steps[0].integration!.replacement_required = false;
        current.steps[0].status = 'complete';
        current.steps[0].actions = [
          { id: 'manage_integration', label: 'Manage integration', effect: 'navigation' }
        ];
        current.steps[1].status = 'active';
        current.steps[1].actions = [
          { id: 'review_create_group', label: 'Review Group', effect: 'review' }
        ];
        current.current_step_id = 'project';
        current.state_revision++;
      }
      if (route.request().method() === 'GET') reads++;
      await route.fulfill({ json: { setup_journey: current } });
    });
    await page.goto('/?setup=specialist');
    const dialog = page.locator('#specialistSetupJourneyModal');
    await expect(dialog).toBeVisible();
    const receipt = dialog.locator('#specialistSetupJourneyReceipt');
    await expect(receipt).toContainText('Installed: Yes');
    await expect(receipt).toContainText('Enabled: Yes');
    await expect(receipt).toContainText('Not verified for guided setup');
    const groupStep = dialog.getByRole('button', { name: /Build Your Music Production Group/ });
    await expect(groupStep).toBeDisabled();
    await expect(dialog.getByRole('button', { name: 'Continue', exact: true })).toHaveCount(0);
    const before = reads;
    await dialog.getByRole('button', { name: 'Check Again', exact: true }).click();
    await expect.poll(() => reads).toBeGreaterThan(before);
    await expect(groupStep).toBeDisabled();
    await page.screenshot({ path: testInfo.outputPath('installed-recovery.png') });
    await dialog.getByRole('button', { name: 'Review verified replacement' }).click();
    await expect(
      dialog.getByRole('heading', { name: 'Replace installed integration?' })
    ).toBeFocused();
    const review = dialog.locator('#specialistSetupJourneyReview');
    await expect(review).toContainText('even if the version number is unchanged');
    await expect(review).toContainText('Installed version: 0.5.0');
    await expect(review).toContainText('Reviewed version: 0.5.0');
    await expect(review).toContainText(
      '2bbf6b77418119cb21e827a407c8d5886e3effdb593ec0ad274e20d7d69c2ca9'
    );
    expect(commits).toHaveLength(0);
    await review.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(receipt).toContainText('Not verified for guided setup');
    expect(commits).toHaveLength(0);
    await dialog.getByRole('button', { name: 'Review verified replacement' }).click();
    await review.getByRole('button', { name: 'Replace with reviewed version' }).click();
    await expect(dialog.getByRole('button', { name: 'Retry Confirmed Change' })).toBeVisible();
    await expect(groupStep).toBeDisabled();
    await dialog.getByRole('button', { name: 'Retry Confirmed Change' }).click();
    expect(commits).toHaveLength(2);
    expect(commits[1]).toEqual(commits[0]);
    await expect(
      dialog.getByRole('heading', { name: 'Build Your Music Production Group' })
    ).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toBeEnabled();
    await dialog.getByRole('button', { name: /Install Ori REAPER Plugin/ }).click();
    await expect(receipt).toContainText('Verified release');
    await expect(dialog.getByRole('button', { name: 'Continue', exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('verified-integration.png') });
  });
}
