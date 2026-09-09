import { expect, test } from '@playwright/test';

const root = '/api/setup-quests/reaper-plugin/reaper_setup';
const templateID = 'plugin:reaper-plugin:reaper-song';

for (const width of [1280, 390]) {
  test(`Plugins, template picker, and Templates page resume one quest at ${width}px`, async ({
    page
  }, testInfo) => {
    await page.setViewportSize({ width, height: 850 });
    const current = {
      run_id: 'saved-quest-root',
      run_kind: 'root',
      state_revision: 3,
      lifecycle_state: 'in_progress',
      current_step_id: 'project',
      dismissed: false,
      receipts: { home_workspace_id: 'existing-group' },
      journey: {
        plugin_id: 'reaper-plugin',
        id: 'reaper_setup',
        title: 'Set up REAPER',
        workspace_launch: {
          group_title: 'Build Your Music Production Group',
          group_name: 'Music Production',
          runtime_title: 'Set Up REAPER',
          runtime_instructions:
            'Prepare Web Remote when you want live control. Permission belongs to each project.'
        }
      },
      steps: [
        {
          id: 'integration',
          kind: 'integration_install',
          title: 'Install Ori REAPER Plugin',
          status: 'complete'
        },
        {
          id: 'project',
          kind: 'project_connect',
          title: 'Connect a project',
          status: 'active',
          preparation: {
            exists: true,
            name: 'My Music Production',
            group_id: 'existing-group',
            acknowledged: false
          },
          actions: [
            { id: 'acknowledge_preparation', label: 'Set up later', effect: 'local_persistence' }
          ]
        }
      ]
    };
    const scopedCalls: string[] = [];
    const unexpectedWrites: string[] = [];
    let assistantCalls = 0;
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { completed: true, current_step: 'complete' } })
    );
    await page.route('**/api/plugins', route =>
      route.fulfill({
        json: {
          plugins: [
            {
              name: 'reaper-plugin',
              version: '0.5.0',
              format: 'claude',
              enabled: true,
              description: 'Music production integration.'
            }
          ]
        }
      })
    );
    await page.route('**/api/project-templates', route =>
      route.fulfill({
        json: {
          templates: [
            {
              id: templateID,
              name: 'Reaper Song',
              description: 'A project workspace for a song.',
              builtin: false,
              plugin_owner: { plugin_id: 'reaper-plugin', blueprint_id: 'reaper-song' },
              setup_quest: 'reaper_setup'
            }
          ]
        }
      })
    );
    await page.route('**/api/personal-assistant/setup-journey**', route => {
      assistantCalls++;
      return route.fulfill({
        status: 409,
        json: { error: { guidance: 'No assistant relationship accepted.' } }
      });
    });
    await page.route('**/api/setup-quests**', async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/setup-quests') {
        await route.fulfill({
          json: {
            quests: [
              {
                plugin_id: 'reaper-plugin',
                id: 'reaper_setup',
                title: 'Set up REAPER',
                template_id: templateID,
                ownership: 'host_compatibility'
              }
            ]
          }
        });
        return;
      }
      scopedCalls.push(path);
      expect(path.startsWith(root)).toBeTruthy();
      if (path.endsWith('/actions/acknowledge_preparation')) {
        current.steps[1].preparation!.acknowledged = true;
        current.state_revision++;
      } else if (path.endsWith('/dismiss')) {
        current.dismissed = true;
      } else if (path.endsWith('/open')) {
        current.dismissed = false;
      } else if (route.request().method() !== 'GET') unexpectedWrites.push(path);
      await route.fulfill({ json: { setup_journey: current } });
    });

    await page.goto('/plugins');
    const guided = page.getByRole('link', { name: 'Guided Setup', exact: true });
    await expect(guided).toHaveAttribute(
      'href',
      '/?setup=quest&plugin=reaper-plugin&quest=reaper_setup'
    );
    await expect(page.getByText('Ori compatibility setup for this plugin version.')).toBeVisible();
    await page
      .locator('#pluginList')
      .screenshot({ path: testInfo.outputPath(`plugin-entry-${width}.png`) });
    await guided.click();
    const dialog = page.locator('#specialistSetupJourneyModal');
    await expect(dialog).toBeVisible();
    await expect(
      dialog.getByText('Not checked. Live project control is not enabled.')
    ).toBeVisible();
    await dialog.getByRole('button', { name: /Build Your Music Production Group/ }).click();
    await expect(
      dialog.getByText(
        'Using your existing group: My Music Production. No duplicate group will be created.'
      )
    ).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Build Group', exact: true })).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Continue', exact: true }).click();
    await dialog.getByRole('button', { name: 'Set up later', exact: true }).click();
    await expect(
      dialog.getByText(
        'Live access remains a separate workspace approval and project-specific check.'
      )
    ).toBeVisible();
    await dialog.getByRole('button', { name: 'Do this later', exact: true }).last().click();
    await expect(dialog).toBeHidden();

    // A different entry point reopens that same run and skipped preparation,
    // without creating a Home, workspace, or granting live access.
    await page.goto('/');
    await page.evaluate(() => {
      const modal = document.getElementById('addFolderModal');
      (window as any).bootstrap.Modal.getOrCreateInstance(modal).show();
    });
    const creator = page.locator('#addFolderModal');
    await expect(creator).toBeVisible();
    await creator.locator(`[data-template-id="${templateID}"]`).click();
    await expect(
      creator.getByRole('link', { name: 'Open Guided Setup', exact: true })
    ).toBeVisible();
    await creator
      .locator('#templateBriefing')
      .screenshot({ path: testInfo.outputPath(`template-entry-${width}.png`) });
    await creator.getByRole('link', { name: 'Open Guided Setup', exact: true }).click();
    await expect(creator).toBeHidden();
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
      'Create New Workspace'
    );
    await expect(
      dialog.getByText(
        'Live access remains a separate workspace approval and project-specific check.'
      )
    ).toBeVisible();
    await page.goto('/templates');
    await page.locator('#tplList [role="listitem"]').filter({ hasText: 'Reaper Song' }).click();
    await expect(page.locator('#tplQuestHeading')).toHaveText('Set up REAPER');
    await expect(page.locator('#tplQuestOwnership')).toContainText('Ori compatibility');
    await expect(page.locator('#tplDetailBuiltinBadge')).toHaveText('Plugin-owned · read-only');
    await expect(page.locator('#tplDetailBuiltinBadge')).toBeVisible();
    for (const id of [
      'tplEditName',
      'tplSaveBtn',
      'tplDeleteBtn',
      'tplFileNewBtn',
      'tplEditorSaveBtn',
      'tplToolsSaveBtn',
      'tplAgentsSaveBtn'
    ]) {
      await expect(page.locator(`#${id}`)).toBeDisabled();
    }
    await expect(page.locator('#tplQuestOpen')).toHaveAttribute(
      'href',
      '/?setup=quest&plugin=reaper-plugin&quest=reaper_setup'
    );
    await page
      .locator('#tplSetupQuest')
      .screenshot({ path: testInfo.outputPath(`templates-page-entry-${width}.png`) });
    await page.locator('#tplQuestOpen').click();
    await page.waitForURL('**/?setup=quest&plugin=reaper-plugin&quest=reaper_setup');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('#specialistSetupJourneyStepTitle')).toHaveText(
      'Create New Workspace'
    );
    expect(assistantCalls).toBe(0);
    expect(unexpectedWrites).toEqual([]);
    expect(scopedCalls).toContain(`${root}/runs/saved-quest-root/actions/acknowledge_preparation`);
    await expect
      .poll(async () => {
        const bounds = await dialog.locator('.modal-dialog').boundingBox();
        return Boolean(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width);
      })
      .toBe(true);
    await dialog.screenshot({ path: testInfo.outputPath(`shared-quest-${width}.png`) });
  });
}
