import { expect, test, type Page } from '@playwright/test';

const attachmentID = 'uqatt_0123456789abcdef01234567';
const questID = 'uquest_0123456789abcdef01234567';
const kinds = [
  'integration_install',
  'project_connect',
  'workspace_setup',
  'assistant_program_staffing',
  'summary'
];

const template = {
  id: 'local-project',
  name: 'Local Project',
  agents: [{ name: 'Local Lead', role: 'orchestrator' }]
};

const draft = {
  title: 'Set up this project',
  description: 'Connect a project and choose how Ori can help.',
  integration_key: 'reviewed-audio',
  steps: kinds.map((kind, index) => ({
    kind,
    title: `Stage ${index + 1}`,
    description: `Guidance ${index + 1}`
  })),
  workspace_launch: {
    group_title: 'Choose a Home',
    group_name: 'Projects',
    runtime_title: 'Choose access',
    runtime_instructions: 'File-only remains available.'
  }
};

function authoring(saved = false, locked = false) {
  const userSetupQuest = saved
    ? {
        schema_version: 1,
        source: 'user_template',
        attachment_id: attachmentID,
        declaration: {
          schema_version: 1,
          version: 1,
          id: questID,
          title: draft.title,
          description: draft.description,
          integration_key: draft.integration_key,
          expected_blueprint_id: template.id,
          expected_assistant_program_id: 'music-project',
          steps: draft.steps.map((step, index) => ({ ...step, id: `step_${index}` })),
          workspace_launch: draft.workspace_launch
        }
      }
    : undefined;
  return {
    source: 'user_template',
    template_id: template.id,
    editable: true,
    locked,
    lock_message: locked ? 'Setup has started. Duplicate the template to edit a new copy.' : '',
    eligibility: { eligible: true },
    revision: 'a'.repeat(64),
    user_setup_quest: userSetupQuest,
    draft,
    integrations: [{ key: draft.integration_key, label: 'Reviewed audio integration' }]
  };
}

async function installRoutes(page: Page) {
  let saved = false;
  const writes: string[] = [];
  page.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/') && request.method() !== 'GET') {
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true } })
  );
  await page.route('**/api/project-templates', route =>
    route.fulfill({
      json: {
        templates: [
          { ...template, ...(saved ? { user_setup_quest: { attachment_id: attachmentID } } : {}) }
        ]
      }
    })
  );
  await page.route('**/api/project-templates/local-project/setup-quest', async route => {
    if (route.request().method() === 'PUT') saved = true;
    await route.fulfill({ json: authoring(saved) });
  });
  await page.route('**/api/project-templates/local-project/setup-quest/preview', async route => {
    const body = route.request().postDataJSON();
    await route.fulfill({
      json: {
        label: 'Preview — no setup started',
        source: 'user_template',
        template_id: template.id,
        user_setup_quest: body.user_setup_quest
      }
    });
  });
  await page.route('**/api/setup-quests', route =>
    route.fulfill({
      json: {
        quests: saved
          ? [
              {
                source: 'user_template',
                template_id: template.id,
                attachment_id: attachmentID,
                id: questID,
                title: draft.title,
                description: draft.description,
                ownership: 'user'
              }
            ]
          : []
      }
    })
  );
  return writes;
}

test('real backend persists and reloads one inert user quest', async ({ page }) => {
  test.skip(
    process.env.ORI_REAL_USER_QUEST_DEMO !== '1',
    'requires an isolated server with the eligible fixture'
  );
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true } })
  );
  const nonReadRequests: string[] = [];
  page.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/') && request.method() !== 'GET') {
      nonReadRequests.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  await page.goto('/templates');
  await page
    .locator('#tplList [role="listitem"]')
    .filter({ hasText: 'User-owned music project' })
    .click();
  await page.locator('#tplTabSetupQuest').click();
  await page.locator('#tplUserQuestCreate').click();
  await page.locator('#tplUserQuestTitle').fill('Real persisted setup quest');
  await page.locator('#tplUserQuestPreviewBtn').click();
  await expect(page.locator('#tplUserQuestPreviewTitle')).toHaveText('Real persisted setup quest');
  await page.locator('#tplUserQuestSaveBtn').click();
  await expect(page.locator('#tplUserQuestStatus')).toContainText('saved setup quest');
  await page.reload();
  await page
    .locator('#tplList [role="listitem"]')
    .filter({ hasText: 'User-owned music project' })
    .click();
  await page.locator('#tplTabSetupQuest').click();
  await expect(page.locator('#tplUserQuestTitle')).toHaveValue('Real persisted setup quest');
  expect(nonReadRequests).toEqual([
    'POST /api/project-templates/user-setup-quest-eligible/setup-quest/preview',
    'PUT /api/project-templates/user-setup-quest-eligible/setup-quest'
  ]);
  await page.locator('#tplTabOverview').click();
  const firstOpen = page.waitForResponse(
    response =>
      response.request().method() === 'GET' &&
      new URL(response.url()).pathname.startsWith('/api/user-template-setup-quests/')
  );
  await page.locator('#tplQuestOpen').click();
  expect((await firstOpen).ok()).toBe(true);
  await page.goto('/templates');
  await page
    .locator('#tplList [role="listitem"]')
    .filter({ hasText: 'User-owned music project' })
    .click();
  await page.locator('#tplTabSetupQuest').click();
  await expect(page.locator('#tplUserQuestStatus')).toContainText('locked');
  await expect(page.locator('#tplUserQuestTitle')).toBeDisabled();
  for (const request of nonReadRequests.slice(2)) {
    expect(request).toMatch(/^POST \/api\/user-template-setup-quests\/.+\/open$/);
  }
});

for (const width of [390, 1280]) {
  test(`user template quest authoring stays fixed, inert, and explicitly launchable at ${width}px`, async ({
    page
  }) => {
    await page.setViewportSize({ width, height: 900 });
    const writes = await installRoutes(page);
    await page.goto('/templates');
    await page.locator('#tplList [role="listitem"]').filter({ hasText: template.name }).click();
    await page.locator('#tplTabSetupQuest').click();
    await page.locator('#tplUserQuestCreate').click();

    const steps = page.locator('#tplUserQuestSteps [data-quest-step]');
    await expect(steps).toHaveCount(5);
    expect(
      await steps.evaluateAll(items => items.every(item => !item.hasAttribute('draggable')))
    ).toBe(true);
    await expect(page.locator('#tplUserQuestIntegration')).toHaveValue(draft.integration_key);

    await page.locator('#tplUserQuestTitle').fill('Unsaved preview title');
    await page.locator('#tplUserQuestPreviewBtn').click();
    await expect(page.locator('#tplUserQuestPreview')).toBeVisible();
    await expect(page.locator('#tplUserQuestPreviewTitle')).toHaveText('Unsaved preview title');
    expect(writes).toEqual(['POST /api/project-templates/local-project/setup-quest/preview']);

    page.once('dialog', dialog => dialog.accept());
    await page.locator('#tplUserQuestCancelBtn').click();
    await page.locator('#tplUserQuestCreate').click();
    await page.locator('#tplUserQuestSaveBtn').click();
    await expect(page.locator('#tplUserQuestStatus')).toContainText('saved setup quest');
    expect(writes).toEqual([
      'POST /api/project-templates/local-project/setup-quest/preview',
      'PUT /api/project-templates/local-project/setup-quest'
    ]);

    await page.locator('#tplTabOverview').click();
    await expect(page.locator('#tplQuestOwnership')).toContainText('User-owned');
    await expect(page.locator('#tplQuestStatus')).toContainText('permanently locks');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)
    ).toBe(true);
    await expect(page.locator('#tplQuestOpen')).toHaveAttribute(
      'href',
      `/?setup=quest&source=user_template&template=${template.id}&attachment=${attachmentID}`
    );
    if (process.env.ORI_CAPTURE_SCREENSHOTS === '1') {
      await page.screenshot({
        path: `tasks/screenshots/464-user-setup-quest-${width}px.png`,
        fullPage: true
      });
    }
  });
}
