import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';

async function settled(page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, current_step: 'complete' } })
  );
}
const guidedTemplate = {
  id: `group-template:${'a'.repeat(32)}`,
  kind: 'managed_home',
  revision: 'r1',
  name: 'Studio Program Home',
  provider: { kind: 'plugin', plugin_id: 'neutral-studio', plugin_version: '1.0.0' },
  home_roles: [{ role_id: 'coordinator', label: 'Studio Coordinator', required: true }],
  project_roles_note: ['Project Lead'],
  availability: { state: 'creatable' },
  home: { state: 'absent' }
};
function journey() {
  return {
    run_id: 'group-builder-run',
    state_revision: 4,
    lifecycle_state: 'in_progress',
    current_step_id: 'project',
    journey: {
      title: 'Set up your studio',
      workspace_launch: {
        group_title: 'Build Your Studio Group',
        group_name: 'Studio'
      }
    },
    receipts: {} as Record<string, string>,
    steps: [
      {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect project',
        status: 'current',
        preparation: {
          exists: false,
          name: 'Studio',
          group_id: '',
          template_id: 'neutral-template',
          group_template_id: guidedTemplate.id
        },
        actions: [{ id: 'review_create_group', label: 'Review Group', effect: 'review' }]
      },
      { id: 'workspace', kind: 'workspace_setup', title: 'Choose a mode', status: 'pending' },
      { id: 'staffing', kind: 'assistant_program_staffing', title: 'Add roles', status: 'pending' },
      { id: 'summary', kind: 'summary', title: 'Review setup', status: 'pending' }
    ]
  };
}
function evidence(page, name) {
  const dir = process.env.ORI_REAPER_DEMO_EVIDENCE_DIR || 'test-results/group-builder';
  mkdirSync(dir, { recursive: true });
  return page.screenshot({ path: `${dir}/${name}.png` });
}

async function reviewGroupRoster(page, creator) {
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  await creator.locator('[data-team-agent-setup]').click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(creator).toBeVisible();
  await creator.getByRole('button', { name: 'Review →' }).click();
  await expect(creator.locator('#wizardStep4')).toBeVisible();
}

// Ordinary Tree creation is a Group-prefilled instance of the one creator; it
// presents a customizable roster and creates its reviewed Manager only at final Create.
test('Tree Create Group reviews its Manager and refreshes the workspace views without adding members', async ({
  page,
  request
}) => {
  await settled(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Switch to dark mode' }).click();
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await page
    .locator('#cockpitTree')
    .getByRole('button', { name: 'Create Group', exact: true })
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  // Create Group opens on its Blueprint step with General selected.
  await expect(creator.locator('#wizardStep1Title')).toHaveText('Choose a group blueprint');
  await expect(
    creator.locator('#workspaceGroupTemplateOptions [data-group-template-id="general"] input')
  ).toBeChecked();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator).toContainText('Groups organize related workspaces');
  const name = `Map Group ${Date.now()}`;
  await creator.getByRole('textbox', { name: 'Group name' }).fill(name);
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await reviewGroupRoster(page, creator);
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    'One organizational group and its reviewed roster'
  );
  await evidence(page, '24-tree-create-group');
  const response = page.waitForResponse(
    res => new URL(res.url()).pathname === '/api/workspaces' && res.request().method() === 'POST'
  );
  await creator.getByRole('button', { name: `Create group “${name}”` }).click();
  const result = await response;
  expect(result.ok(), await result.text()).toBeTruthy();
  const payload = await result.json();
  const id = payload.folder.id;
  try {
    expect(result.request().postDataJSON()).toMatchObject({
      name,
      kind: 'group',
      group_roster: true,
      create_template_agents: true,
      template_agent_review: expect.objectContaining({
        version: 1,
        expectations: [expect.objectContaining({ name: `${name} Manager`, action: 'create' })]
      })
    });
    expect(result.request().postDataJSON()).not.toHaveProperty('team_intent');
    expect(result.request().postDataJSON()).not.toHaveProperty('role_staffing');
    await expect(creator).toBeHidden();
    await expect
      .poll(() =>
        page.evaluate(
          groupID =>
            (window as any).OriHomeCockpit?.getState()?.flattened?.some(row => row.id === groupID),
          id
        )
      )
      .toBeTruthy();
    const group = await (await request.get(`/api/workspaces/${id}`)).json();
    const record = group.folder || group;
    expect(record.kind).toBe('group');
    expect(record.agent_instances || []).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ name: `${name} Manager`, entry_point: true })
      ])
    );
    expect(record.assistant_program_state).toBeFalsy();
  } finally {
    await request.delete(`/api/workspaces/${id}?confirm=true`);
  }
});

// Guided creation uses identical presentation but no generic POST. Its owner
// records a review token once and resends exactly that envelope on uncertainty.
test('guided setup uses the shared creator with cancellation, exact retry and commit-only Escape lock', async ({
  page
}) => {
  await settled(page);
  await page.setViewportSize({ width: 390, height: 740 });
  const current = journey();
  let reviews = 0;
  const commits: unknown[] = [];
  let genericCreates = 0;
  let release: () => void = () => {};
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/api/workspaces', route => {
    if (route.request().method() === 'POST') genericCreates++;
    return route.continue();
  });
  await page.route('**/api/personal-assistant/setup-journey**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/actions/review_create_group')) {
      reviews++;
      return route.fulfill({
        json: {
          setup_journey: current,
          review: {
            token: `review-${reviews}`,
            commit_action: 'create_group',
            group: { name: route.request().postDataJSON().input.name }
          }
        }
      });
    }
    if (path.endsWith('/actions/create_group')) {
      commits.push(route.request().postDataJSON());
      if (commits.length === 1) {
        await held;
        await route.abort('failed');
        return;
      }
      current.receipts.home_workspace_id = 'canonical-home';
      Object.assign(current.steps[0].preparation!, {
        exists: true,
        group_id: 'canonical-home',
        name: 'My Studio'
      });
    }
    return route.fulfill({ json: { setup_journey: current } });
  });
  // The catalog only describes the fixed template; setup stays the review and
  // commit owner, so no Group Template review or commit may be sent.
  const groupTemplateWrites: string[] = [];
  await page.route('**/api/workspaces/group-templates**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path !== '/api/workspaces/group-templates') {
      groupTemplateWrites.push(path);
      return route.abort('failed');
    }
    return route.fulfill({ json: { group_templates: [guidedTemplate] } });
  });
  await page.goto('/?setup=specialist');
  const setup = page.locator('#specialistSetupJourneyModal');
  const creator = page.locator('#addFolderModal');
  await expect(setup.locator('#specialistSetupJourneyReceipt')).toContainText(
    'Group template: Studio Program Home · Plugin: neutral-studio 1.0.0'
  );
  await expect(setup.locator('#specialistSetupJourneyReceipt')).toContainText(
    'Set up after: Studio Coordinator (required)'
  );
  await expect(setup).not.toContainText('workspace map');
  await setup.getByRole('button', { name: 'Build Group', exact: true }).click();
  await expect(setup).toBeHidden();
  await expect(creator.getByRole('textbox', { name: 'Group name' })).toHaveValue('Studio');
  // Guided setup has no Blueprint step; its fixed blueprint is described on Details.
  await expect(creator.locator('#wizardStep1')).toBeHidden();
  const fixed = creator.locator('#workspaceGroupTemplateFixedOptions');
  await expect(fixed).toContainText('Studio Program Home');
  await expect(fixed).toContainText('Creates one group only');
  await expect(fixed).toContainText('Set up after: Studio Coordinator (required)');
  await expect(fixed.locator('input')).toHaveCount(0);
  await creator.getByRole('textbox', { name: 'Group name' }).fill('My Studio');
  // An unsubmitted close preserves the supported name draft but no consent.
  await page.waitForTimeout(200);
  await creator.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(creator).toBeHidden();
  await expect(setup).toBeVisible();

  await setup.getByRole('button', { name: 'Build Group', exact: true }).click();
  await expect(creator).toBeVisible();
  await expect(creator.getByRole('textbox', { name: 'Group name' })).toHaveValue('My Studio');
  await creator.getByRole('button', { name: 'Review →' }).click();
  await creator.getByRole('button', { name: 'Review group', exact: true }).click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    'Setup reviewed this exact group'
  );
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    'Template: Studio Program Home · Plugin: neutral-studio 1.0.0'
  );
  await evidence(page, '25-guided-shared-creator-narrow');
  await creator.getByRole('button', { name: 'Create group “My Studio” only' }).click();
  await expect.poll(() => commits.length).toBe(1);
  await page.keyboard.press('Escape');
  await expect(creator).toBeVisible();
  release();
  await creator.getByRole('button', { name: 'Retry Confirmed Change' }).click();
  expect(commits).toHaveLength(2);
  expect(commits[0]).toEqual(commits[1]);
  expect((commits[1] as any).review_token).toBe('review-1');
  await expect(creator).toBeHidden();
  // With the group built, the launch view moves straight to the workspace screen.
  await expect(setup.locator('#specialistSetupJourneyStepTitle')).toHaveText(
    'Create New Workspace'
  );
  expect(genericCreates).toBe(0);
  expect(groupTemplateWrites).toEqual([]);
});

test('a historical project with an unavailable group never offers replacement creation', async ({
  page
}) => {
  await settled(page);
  const current = journey();
  current.receipts = { home_workspace_id: 'old-home', project_workspace_id: 'old-project' };
  current.steps[0].status = 'complete';
  current.steps[0].actions = [];
  await page.route('**/api/personal-assistant/setup-journey**', route =>
    route.fulfill({ json: { setup_journey: current } })
  );
  await page.goto('/?setup=specialist');
  const setup = page.locator('#specialistSetupJourneyModal');
  await expect(setup).toContainText('existing setup group could not be verified');
  await expect(setup.getByRole('button', { name: 'Build Group', exact: true })).toHaveCount(0);
  await expect(setup.getByRole('button', { name: 'Open Existing Workspace' })).toBeVisible();
});
