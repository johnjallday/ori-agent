import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';

test('shared creator keeps the approved group, exact import choice and browser-only draft', async ({
  page
}) => {
  test.skip(
    process.env.ORI_REAPER_EXPECT_DEVELOPMENT_COPY !== '1',
    'Requires the disposable local plugin demo.'
  );
  await page.setViewportSize({ width: 390, height: 740 });
  const current = {
    run_id: 'launch-browser-review',
    state_revision: 7,
    lifecycle_state: 'in_progress',
    current_step_id: 'project',
    journey: {
      title: 'Set up REAPER',
      description: 'Prepare your group, then create a workspace.',
      workspace_launch: {
        group_title: 'Create Your Music Production Group',
        runtime_title: 'Set Up REAPER',
        runtime_instructions: 'Check prerequisites; project access stays workspace-owned.'
      }
    },
    receipts: { home_workspace_id: 'reviewed-home' },
    steps: [
      {
        id: 'integration',
        kind: 'integration_install',
        title: 'Install Ori REAPER Plugin',
        status: 'complete',
        integration: {
          plugin_id: 'reaper-plugin',
          installed_version: '0.5.0',
          development_copy: true
        }
      },
      {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect a project',
        status: 'current',
        preparation: {
          exists: true,
          acknowledged: true,
          name: 'My Studio',
          group_id: 'reviewed-home',
          template_id: 'plugin:reaper-plugin:reaper-song'
        },
        actions: []
      },
      { id: 'workspace', kind: 'workspace_setup', status: 'pending' },
      { id: 'staffing', kind: 'assistant_program_staffing', status: 'pending' },
      { id: 'summary', kind: 'summary', status: 'pending' }
    ]
  };
  const inputs: Record<string, unknown>[] = [];
  const mutations: string[] = [];
  let checks = 0;
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, current_step: 'complete' } })
  );
  await page.route('**/api/folder-picker/select-path', route =>
    route.fulfill({
      json: { selected: true, selection_token: 'approved-selection', path: '/approved/Album' }
    })
  );
  await page.route('**/api/personal-assistant/setup-journey**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/preparation')) {
      checks++;
      await route.fulfill(
        checks === 1
          ? { json: { ready: true } }
          : {
              status: 503,
              json: {
                error: {
                  reason_code: 'owner_unavailable',
                  guidance: 'Prerequisites could not be checked.'
                }
              }
            }
      );
      return;
    }
    if (path.includes('/actions/review_')) {
      const input = route.request().postDataJSON().input;
      inputs.push(input);
      await route.fulfill({
        json: {
          setup_journey: current,
          review: {
            token: 'exact-review',
            commit_action: 'attach_existing_project',
            project_connection: {
              mode_id: input.mode_id,
              workspace_name: input.workspace_name,
              parent_workspace_name: 'My Studio',
              entry_name: input.entry_name || '',
              entry_candidates: input.entry_name ? [] : ['first.rpp', 'second.rpp'],
              selected_folder: '/approved/Album',
              created_files: []
            }
          }
        }
      });
      return;
    }
    if (path.includes('/actions/')) mutations.push(path);
    await route.fulfill({ json: { setup_journey: current } });
  });
  await page.goto('/?setup=specialist');
  const setup = page.locator('#specialistSetupJourneyModal');
  const creator = page.locator('#addFolderModal');
  await setup.locator('.setup-journey__step-button').nth(2).click();
  await setup.getByRole('button', { name: 'Check Setup' }).click();
  await expect(setup).toContainText('Application prerequisites are available');
  await setup.getByRole('button', { name: 'Check Setup' }).click();
  await expect(setup.getByRole('alert')).toBeVisible();
  await expect(setup).not.toContainText('Application prerequisites are available');
  await setup.locator('.setup-journey__step-button').nth(3).click();
  await setup.getByRole('button', { name: 'Create New Workspace', exact: true }).click();
  await expect(creator.locator('#wizardStep2')).toBeVisible();
  await expect(creator.locator('#wizardStep1')).toBeHidden();
  await expect(creator.locator('#folderParentSelect')).toHaveValue('reviewed-home');
  await creator.getByRole('textbox', { name: 'Workspace name', exact: true }).fill('Imported Song');
  await creator
    .getByRole('combobox', { name: 'Project', exact: true })
    .selectOption('existing_project');
  await creator.getByRole('button', { name: 'Choose Project Folder' }).click();
  await creator.locator('#wizardNextBtn').click();
  await creator.locator('#wizardNextBtn').click();
  await creator
    .getByRole('combobox', { name: 'Project file to import' })
    .selectOption('second.rpp');
  await expect(creator.locator('#workspaceJourneyReview')).toContainText(
    'Project file: second.rpp'
  );
  expect(inputs.at(-1)).toMatchObject({
    mode_id: 'existing_project',
    workspace_name: 'Imported Song',
    selection_token: 'approved-selection',
    entry_name: 'second.rpp'
  });
  expect(JSON.stringify(inputs)).not.toContain('/approved/Album');
  const footer = await creator.locator('.modal-footer').boundingBox();
  const button = await creator.locator('#createFolderBtn').boundingBox();
  expect(button!.y + button!.height).toBeLessThanOrEqual(footer!.y + footer!.height);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
  ).toBeTruthy();
  const evidence = process.env.ORI_REAPER_DEMO_EVIDENCE_DIR || 'test-results/domain-specialist';
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/20-shared-import-review-narrow.png` });
  await creator.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(creator).toBeHidden();
  await page.evaluate(() => window.dispatchEvent(new CustomEvent('ori:open-specialist-setup')));
  await setup.getByRole('button', { name: 'Create New Workspace', exact: true }).click();
  await expect(creator.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    'Imported Song'
  );
  await expect(creator.getByRole('combobox', { name: 'Project', exact: true })).toHaveValue(
    'existing_project'
  );
  await expect(creator).toContainText('/approved/Album');
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    '/approved/Album'
  );
  expect(mutations).toEqual([]);
  await creator.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(creator).toBeHidden();
  await page.evaluate(() => (window as any).sessionManager.showAddWorkspaceModal());
  await expect(creator.locator('#wizardStep1')).toBeVisible();
  await expect(creator.locator('#workspaceJourneyProjectChoice')).toHaveCount(0);
});
