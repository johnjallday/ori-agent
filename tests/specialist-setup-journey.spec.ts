import { expect, test, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

const evidenceDir = process.env.ORI_REAPER_DEMO_EVIDENCE_DIR || 'test-results/domain-specialist';

function capture(page: Page, name: string) {
  mkdirSync(evidenceDir, { recursive: true });
  return page.screenshot({ path: `${evidenceDir}/${name}.png`, fullPage: false });
}

function action(id: string, label: string, effect: 'review' | 'navigation' | 'commit') {
  return { id, label, effect };
}

function integrationStep() {
  return {
    id: 'integration',
    kind: 'integration_install',
    title: 'Prepare the REAPER integration',
    description: 'Review the publisher, source, version, platform, and host requirements.',
    status: 'complete',
    integration: {
      plugin_id: 'reaper-plugin',
      expected_version: '0.5.0',
      installed_version: '0.5.0',
      enabled: true
    },
    actions: [action('manage_integration', 'Manage integration', 'navigation')]
  };
}

function projectStep(status: string = 'current') {
  return {
    id: 'project',
    kind: 'project_connect',
    title: 'Connect your first project',
    description:
      'Choose an existing project folder or create a managed project from the reviewed blueprint.',
    guidance: 'Nothing is created or connected until you confirm an exact review.',
    status,
    actions: [
      action('review_existing_project', 'Use an existing project', 'review'),
      action('review_new_project', 'Create a new project', 'review')
    ]
  };
}

function workspaceStep(status: string = 'current') {
  return {
    id: 'workspace-setup',
    kind: 'workspace_setup',
    title: 'Choose how Ori works with the project',
    description:
      'File-only works through the authoritative project file. Live control stays optional.',
    guidance: 'File-only does not send commands to the running app.',
    status,
    workspace_setup: {
      mode_id: '',
      mode_label: 'Not selected',
      files_connected: true,
      live_control_configured: false,
      live_control_tested: false
    },
    actions: [
      action('review_file_only_mode', 'Review File-only', 'review'),
      action('open_live_setup', 'Set up live control instead', 'navigation')
    ]
  };
}

function staffingStep(status: string = 'current') {
  return {
    id: 'staffing',
    kind: 'assistant_program_staffing',
    title: 'Staff Home and this project independently',
    description:
      'Home coordination and project execution use separate, explicitly reviewed role bindings.',
    guidance: 'A role added here belongs only to the named scope.',
    status,
    staffing: {
      scopes: [
        {
          scope: 'home',
          workspace_id: 401,
          workspace_label: 'Producer Home',
          binding_revision: 3,
          runtime_ready: true,
          models_ready: false,
          authority_boundary: 'Coordinates linked projects without inheriting project execution.',
          roles: [
            {
              role_id: 'coordinator',
              label: 'Home coordinator',
              required: true,
              configured: false,
              tool_grants: ['read_portfolio']
            }
          ]
        },
        {
          scope: 'project',
          workspace_id: 402,
          workspace_label: 'Night Drive',
          binding_revision: 5,
          runtime_ready: true,
          models_ready: true,
          authority_boundary: 'Works only inside this project workspace.',
          roles: [
            {
              role_id: 'project-specialist',
              label: 'Project specialist',
              required: true,
              configured: false,
              tool_grants: ['read_project']
            }
          ]
        }
      ]
    },
    actions: [
      action('review_home_staffing', 'Review Home staffing', 'review'),
      action('review_project_staffing', 'Review project staffing', 'review')
    ]
  };
}

function journey(
  currentStep: 'project' | 'workspace-setup' | 'staffing',
  revision: number,
  lifecycle: string = 'in_progress'
) {
  const projectStatus = currentStep === 'project' ? 'current' : 'complete';
  const workspaceStatus =
    currentStep === 'project'
      ? 'pending'
      : currentStep === 'workspace-setup'
        ? 'current'
        : 'complete';
  return {
    run_id: 'browser-evidence-run',
    state_revision: revision,
    lifecycle,
    busy: false,
    current_step_id: currentStep,
    journey: {
      id: 'reaper-first-project',
      title: 'Set up your REAPER specialist',
      description: 'A guided setup with review before every lasting change.'
    },
    receipts:
      currentStep === 'project'
        ? { home_workspace_id: 401 }
        : { home_workspace_id: 401, project_workspace_id: 402 },
    steps: [
      integrationStep(),
      projectStep(projectStatus),
      workspaceStep(workspaceStatus),
      staffingStep(currentStep === 'staffing' ? 'current' : 'pending')
    ]
  };
}

test.describe('specialist setup journey browser evidence', () => {
  test('keeps every lasting setup action behind review and preserves dismiss/resume', async ({
    page
  }) => {
    let current = journey('project', 7);
    let dismissCount = 0;
    let finishProjectCommit: (() => void) | null = null;
    const pageErrors: string[] = [];
    page.on('pageerror', error => pageErrors.push(error.stack || error.message));

    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { completed: true, current_step: 'complete' } })
    );
    await page.route('**/api/personal-assistant/setup-journey**', async route => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname;

      if (path.endsWith('/dismiss')) {
        dismissCount += 1;
        current = {
          ...current,
          lifecycle: 'dismissed',
          state_revision: current.state_revision + 1
        };
        await route.fulfill({ json: { setup_journey: current } });
        return;
      }
      if (path.endsWith('/open')) {
        current = {
          ...current,
          lifecycle: 'in_progress',
          state_revision: current.state_revision + 1
        };
        await route.fulfill({ json: { setup_journey: current } });
        return;
      }
      if (path.endsWith('/actions/review_new_project')) {
        await route.fulfill({
          json: {
            review: {
              token: 'review-project-token',
              commit_action: 'create_new_project',
              expires_at: '2035-01-01T00:00:00Z',
              project_connection: {
                workspace_name: 'Night Drive',
                parent_workspace_name: 'Producer Home',
                home_will_be_created: false,
                project_name: 'Night Drive',
                entry_name: 'Night Drive Project',
                created_files: ['Project file', 'Notes'],
                defaults_statement: 'A minimal reviewed project scaffold.'
              }
            }
          }
        });
        return;
      }
      if (path.endsWith('/actions/create_new_project')) {
        await new Promise<void>(resolve => {
          finishProjectCommit = resolve;
        });
        current = journey('workspace-setup', current.state_revision + 1);
        await route.fulfill({ json: { setup_journey: current } });
        return;
      }
      if (path.endsWith('/actions/review_file_only_mode')) {
        await route.fulfill({
          json: {
            review: {
              token: 'review-mode-token',
              commit_action: 'select_file_only_mode',
              expires_at: '2035-01-01T00:00:00Z',
              workspace_setup: {
                mode_label: 'File-only',
                mode_description:
                  'Ori reads and writes reviewed project files without controlling the running app.',
                files_connected: true,
                live_control_configured: false,
                live_control_tested: false
              }
            }
          }
        });
        return;
      }
      if (path.endsWith('/actions/select_file_only_mode')) {
        current = journey('staffing', current.state_revision + 1);
        await route.fulfill({ json: { setup_journey: current } });
        return;
      }
      await route.fulfill({ json: { setup_journey: current } });
    });

    await page.goto('/?setup=specialist');

    const dialog = page.locator('#specialistSetupJourneyModal');
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAttribute('role', 'dialog');
    await expect(dialog).toHaveAttribute('aria-modal', 'true');
    await expect(page.getByRole('heading', { name: 'Connect your first project' })).toBeFocused();
    await expect(page.getByRole('navigation', { name: 'Setup progress' })).toBeVisible();
    await capture(page, '07-setup-project-choice');

    await dialog.getByRole('button', { name: 'Create a new project' }).click();
    expect(pageErrors).toEqual([]);
    const workspaceName = dialog.getByLabel('Workspace name');
    await expect(workspaceName).toBeFocused();
    await workspaceName.fill('Night Drive');
    await dialog.getByLabel('Project name').fill('Night Drive');
    await dialog.getByRole('button', { name: 'Continue to review' }).click();

    await expect(
      dialog.getByRole('heading', { name: 'Review before making changes' })
    ).toBeVisible();
    await expect(dialog).toContainText('Home: Will be reused');
    await expect(dialog).toContainText('A minimal reviewed project scaffold.');
    const confirmProject = dialog.getByRole('button', { name: 'Confirm this change' });
    await expect(dialog.getByRole('button', { name: 'Back' })).toBeFocused();
    await capture(page, '08-setup-project-review');

    await confirmProject.click();
    await expect(dialog).toHaveAttribute('aria-busy', 'true');
    await expect(dialog.locator('#specialistSetupJourneyClose')).toBeDisabled();
    await expect(dialog.locator('#specialistSetupJourneyLater')).toBeDisabled();
    await expect.poll(() => Boolean(finishProjectCommit)).toBe(true);
    await page.keyboard.press('Escape');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('status').last()).toContainText(
      'Finish the reviewed change before closing setup.'
    );
    finishProjectCommit?.();

    await expect(
      page.getByRole('heading', { name: 'Choose how Ori works with the project' })
    ).toBeVisible();
    await expect(dialog).toContainText('Live control configured: No');
    await expect(dialog).toContainText('Live control tested: No');
    await capture(page, '09-setup-file-only-choice');

    await dialog.getByRole('button', { name: 'Review File-only' }).click();
    await expect(dialog).toContainText(
      'What it means: Ori reads and writes reviewed project files'
    );
    await expect(dialog).toContainText('Live control configured: No');
    await dialog.getByRole('button', { name: 'Confirm this change' }).click();

    await expect(
      page.getByRole('heading', { name: 'Staff Home and this project independently' })
    ).toBeVisible();
    await dialog.getByRole('button', { name: 'Review Home staffing' }).click();
    await expect(dialog).toContainText('These profiles belong only to Producer Home.');
    await expect(dialog).not.toContainText('These profiles belong only to Night Drive.');
    await expect(dialog.getByLabel('Profile name')).toHaveValue('Home coordinator');
    await capture(page, '10-setup-independent-home-staffing');

    await page.setViewportSize({ width: 390, height: 844 });
    const bounds = await dialog.locator('.modal-dialog').boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
    await capture(page, '11-setup-narrow-keyboard-ready');

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    expect(dismissCount).toBe(1);

    await page.evaluate(() =>
      window.dispatchEvent(
        new CustomEvent('ori:open-specialist-setup', { detail: { intent: 'review' } })
      )
    );
    await expect(dialog).toBeVisible();
    await expect(
      page.getByRole('heading', { name: 'Staff Home and this project independently' })
    ).toBeVisible();
    await expect(dialog.getByLabel('Profile name')).toHaveCount(0);
  });

  test('renders the bounded Sample Library catalog and exact copy review at wide and narrow widths', async ({
    page,
    request
  }) => {
    const created = await request.post('/api/workspaces', {
      data: { name: `Sample Library Evidence ${Date.now()}`, description: '' }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const createdBody = await created.json();
    const workspaceId = String(createdBody.folder?.id || createdBody.workspace?.id);

    try {
      const detailsResponse = await request.get(`/api/workspaces/${workspaceId}`);
      expect(detailsResponse.ok(), await detailsResponse.text()).toBeTruthy();
      const detailsBody = await detailsResponse.json();
      const workspace = detailsBody.workspace || detailsBody.folder || detailsBody;
      const workspaceSlug = String(workspace.folder_slug || workspaceId);

      await page.route('**/api/onboarding/status', route =>
        route.fulfill({ json: { completed: true, current_step: 'complete' } })
      );
      await page.route(`**/api/workspaces/${workspaceId}/assistant-program**`, route => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/learnings')) {
          return route.fulfill({
            json: { version: 1, candidates: [], learnings: [], suggestions: [] }
          });
        }
        if (path.endsWith('/remove-home/review')) {
          return route.fulfill({
            json: {
              token: 'remove-home-review-token',
              linked_project_count: 1,
              impact: [
                'Home-scoped role bindings and optional catalog state will be removed.',
                'Night Drive workspace, team, tasks, project files, and confirmed sample copies will be retained.',
                'External project and sample folders are never deletion targets.'
              ]
            }
          });
        }
        if (path.endsWith('/disconnect/review')) {
          return route.fulfill({
            json: {
              token: 'disconnect-review-token',
              impact: [
                'Home rollup and Home-to-project handoffs will stop.',
                'Night Drive workspace, team, tasks, project files, and confirmed sample copies will be retained.'
              ]
            }
          });
        }
        return route.fulfill({
          json: {
            available: true,
            hired: true,
            is_station: true,
            plugin_available: true,
            level: 1,
            stage_id: 'assistant',
            stage_label: 'Assistant',
            roster_scope: 'home',
            state_revision: 8,
            accepted_tasks: 2,
            next_threshold: 3,
            remaining: 1,
            declaration: {
              schema_version: 2,
              station_name: 'Producer Home',
              station_description:
                'Coordinates projects while each project keeps its own execution scope.',
              roles: [
                {
                  id: 'coordinator',
                  label: 'Home coordinator',
                  description: 'Reports across linked projects without inheriting their authority.',
                  scope: 'home',
                  primary: true
                }
              ],
              stages: [
                { id: 'assistant', label: 'Assistant', description: 'Coordinates reviewed work.' },
                { id: 'collaborator', label: 'Collaborator', description: 'Suggests future work.' }
              ]
            },
            roster: [],
            role_profiles: [],
            projects: [{ id: 'project-402', name: 'Night Drive', folder_slug: 'night-drive' }],
            portfolio: [
              {
                project_workspace_id: 'project-402',
                fields: { status: 'recording', priority: 2 },
                open_ticket_count: 1
              }
            ]
          }
        });
      });
      await page.route('**/api/folder-picker/select-path', route =>
        route.fulfill({
          json: {
            success: true,
            selected: true,
            path: '/Disposable Samples',
            selection_token: 'trusted-sample-root-token'
          }
        })
      );
      await page.route(
        `**/api/workspaces/${workspaceId}/capabilities/sample-library/removal`,
        route =>
          route.fulfill({
            json: {
              removal: {
                impacts: ['Active catalog search and future indexing will stop.'],
                retained_audit: ['Confirmed project copies and bounded receipts']
              }
            }
          })
      );
      await page.route(`**/api/workspaces/${workspaceId}/sample-library**`, route => {
        const path = new URL(route.request().url()).pathname;
        if (path.endsWith('/roots/review')) {
          return route.fulfill({
            json: {
              review: {
                token: 'root-review-token',
                disclosure: [
                  'One exact folder selected through the trusted picker will be authorized.',
                  'Connecting the folder does not scan files, enable analysis, or grant a project access.'
                ]
              }
            }
          });
        }
        if (path.endsWith('/analysis/review')) {
          return route.fulfill({
            json: {
              review: {
                token: 'analysis-review-token',
                disclosure: [
                  'Only approved hashes and bounded embedded tags would be read.',
                  'No audio is uploaded, auditioned, transcribed, or analyzed for BPM or key.'
                ]
              }
            }
          });
        }
        if (path.endsWith('/revoke/review')) {
          return route.fulfill({
            json: {
              review: {
                token: 'revoke-review-token',
                disclosure: [
                  'The folder will leave the active catalog.',
                  'Source files and confirmed project copies will be preserved.'
                ]
              }
            }
          });
        }
        if (path.endsWith('/search')) {
          return route.fulfill({
            json: {
              result: {
                entries: [
                  {
                    id: 'entry-kick',
                    filename: 'Warm Kick.wav',
                    extension: '.wav',
                    size_bytes: 184320
                  },
                  {
                    id: 'entry-shaker',
                    filename: 'Dusty Shaker.aif',
                    extension: '.aif',
                    size_bytes: 92160
                  }
                ]
              }
            }
          });
        }
        if (path.endsWith('/copies/review')) {
          return route.fulfill({
            json: {
              review: {
                token: 'copy-review-token',
                disclosure: [
                  'Two catalog items will be copied into Night Drive. Source access stays with Home.'
                ],
                items: [
                  {
                    source_path: 'Warm Kick.wav',
                    destination_path: 'Samples/Warm Kick.wav',
                    collision_resolved: false
                  }
                ]
              }
            }
          });
        }
        return route.fulfill({
          json: {
            state: { catalog_revision: 4 },
            roots: [
              {
                id: 'root-12345678',
                completeness: 'complete',
                hash_enabled: false,
                tags_enabled: false
              }
            ]
          }
        });
      });

      await page.goto(
        `/workspaces/${encodeURIComponent(workspaceSlug)}/assistant#sampleLibraryPanel`
      );

      const panel = page.getByRole('region', { name: 'Sample Library' });
      await expect(panel).toBeVisible();
      await expect(panel.getByRole('status')).toHaveText('2 active catalog results.');
      await expect(panel).toContainText('Sample folder root-123');
      await expect(panel).not.toContainText('/Users/');
      await expect(panel.getByRole('cell', { name: 'Warm Kick.wav', exact: true })).toBeVisible();
      await expect(
        panel.getByRole('cell', { name: 'Dusty Shaker.aif', exact: true })
      ).toBeVisible();
      await panel.scrollIntoViewIfNeeded();
      await capture(page, '12-sample-library-bounded-catalog');

      await panel.getByRole('button', { name: 'Connect folder' }).click();
      const rootReview = page.getByRole('dialog', { name: 'Connect this sample folder?' });
      await expect(rootReview).toContainText('Connecting the folder does not scan files');
      await expect(rootReview.getByRole('button', { name: 'Cancel' })).toBeFocused();
      await capture(page, '16-sample-root-no-scan-review');
      await rootReview.getByRole('button', { name: 'Cancel' }).click();
      await expect(panel.getByRole('button', { name: 'Connect folder' })).toBeFocused();

      await panel.getByRole('button', { name: 'Index metadata' }).click();
      await expect(panel.getByRole('status')).toHaveText('2 active catalog results.');
      // Refresh replaces the root card, so focus falls back to the announced status.
      await expect(panel.getByRole('status')).toBeFocused();

      await panel.getByRole('button', { name: 'Analysis settings' }).click();
      const analysisReview = page.getByRole('dialog', { name: 'Enable content analysis?' });
      await expect(analysisReview).toContainText('No audio is uploaded, auditioned, transcribed');
      await capture(page, '17-sample-analysis-review-declined');
      await analysisReview.getByRole('button', { name: 'Cancel' }).click();
      await expect(panel).toContainText('analysis off');

      await panel.getByRole('checkbox', { name: 'Select Warm Kick.wav' }).check();
      await panel.getByLabel('Copy selected samples to').selectOption('project-402');
      await panel.getByRole('button', { name: 'Review copy' }).click();

      const review = page.getByRole('dialog', { name: 'Copy selected samples?' });
      await expect(review).toBeVisible();
      await expect(review).toContainText('Source access stays with Home.');
      await expect(review).toContainText('Warm Kick.wav → Samples/Warm Kick.wav');
      await expect(review).not.toContainText('/Users/');
      await expect(review.getByRole('button', { name: 'Cancel' })).toBeFocused();
      await capture(page, '18-sample-exact-copy-review');
      await review.getByRole('button', { name: 'Cancel' }).click();
      await expect(panel.getByRole('button', { name: 'Review copy' })).toBeFocused();

      await panel.getByRole('button', { name: 'Revoke access' }).click();
      const revokeReview = page.getByRole('dialog', { name: 'Revoke folder access?' });
      await expect(revokeReview).toContainText(
        'Source files and confirmed project copies will be preserved.'
      );
      await capture(page, '19-sample-revocation-review');
      await revokeReview.getByRole('button', { name: 'Cancel' }).click();
      await expect(panel.getByRole('button', { name: 'Revoke access' })).toBeFocused();

      await panel.getByRole('button', { name: 'Review add-on removal' }).click();
      const addOnRemoval = page.getByRole('dialog', { name: 'Remove Sample Library?' });
      await expect(addOnRemoval).toContainText(
        'Active catalog search and future indexing will stop.'
      );
      await expect(addOnRemoval).toContainText(
        'Source files and confirmed project copies are not deleted.'
      );
      await capture(page, '20-sample-addon-removal-review');
      await addOnRemoval.getByRole('button', { name: 'Cancel' }).click();
      await expect(panel.getByRole('button', { name: 'Review add-on removal' })).toBeFocused();

      await page.getByRole('button', { name: 'Review Home removal' }).click();
      const homeRemoval = page.getByRole('dialog', { name: 'Review Home removal' });
      await expect(homeRemoval).toContainText('1 linked projects will be retained.');
      await expect(homeRemoval).toContainText(
        'External project and sample folders are never deletion targets.'
      );
      await capture(page, '14-home-removal-impact-review');
      await homeRemoval.getByRole('button', { name: 'Cancel' }).click();
      await expect(page.getByRole('button', { name: 'Review Home removal' })).toBeFocused();

      await page.getByRole('button', { name: 'Review disconnect' }).click();
      const disconnect = page.getByRole('dialog', { name: 'Review disconnect for Night Drive' });
      await expect(disconnect).toContainText('Home rollup and Home-to-project handoffs will stop.');
      await expect(disconnect).toContainText(
        'workspace, team, tasks, project files, and confirmed sample copies will be retained.'
      );
      await capture(page, '15-disconnect-impact-review');
      await disconnect.getByRole('button', { name: 'Cancel' }).click();
      await expect(page.getByRole('button', { name: 'Review disconnect' })).toBeFocused();

      await page.setViewportSize({ width: 390, height: 844 });
      await expect(panel).toBeVisible();
      await panel.scrollIntoViewIfNeeded();
      const pageDoesNotOverflow = await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth
      );
      expect(pageDoesNotOverflow).toBeTruthy();
      await capture(page, '13-sample-library-narrow');
    } finally {
      await request.delete(`/api/workspaces/${workspaceId}`).catch(() => {});
    }
  });
});
