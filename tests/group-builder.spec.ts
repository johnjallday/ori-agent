import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';

async function settled(page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, current_step: 'complete' } })
  );
}
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
        group_name: 'Studio',
        runtime_title: 'Prepare Tools',
        runtime_instructions: 'Check application prerequisites separately.'
      }
    },
    receipts: {} as Record<string, string>,
    steps: [
      {
        id: 'integration',
        kind: 'integration_install',
        title: 'Install plugin',
        status: 'complete'
      },
      {
        id: 'project',
        kind: 'project_connect',
        title: 'Connect project',
        status: 'current',
        preparation: {
          exists: false,
          acknowledged: false,
          name: 'Studio',
          group_id: '',
          template_id: 'neutral-template'
        },
        actions: [{ id: 'review_create_group', label: 'Review Group', effect: 'review' }]
      }
    ]
  };
}
function evidence(page, name) {
  const dir = process.env.ORI_REAPER_DEMO_EVIDENCE_DIR || 'test-results/group-builder';
  mkdirSync(dir, { recursive: true });
  return page.screenshot({ path: `${dir}/${name}.png` });
}

test('map Build Group reviews an empty group and refreshes the map without adding members', async ({
  page,
  request
}) => {
  await settled(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Switch to dark mode' }).click();
  await page.locator('#cockpitBuildGroupBtn').click();
  const builder = page.locator('#buildGroupModal');
  await expect(builder).toBeVisible();
  await expect
    .poll(() =>
      builder.locator('.modal-content').evaluate(node => getComputedStyle(node).backgroundColor)
    )
    .toMatch(/^rgb\(/);
  await expect(builder).toContainText('No agents, projects, schedules, or access permissions');
  const name = `Map Group ${Date.now()}`;
  await builder.getByRole('textbox', { name: 'Group name' }).fill(name);
  await builder.getByRole('button', { name: 'Review Group' }).click();
  await expect(builder.getByRole('heading', { name: `Build “${name}”?` })).toBeVisible();
  await evidence(page, '24-map-build-group');
  const response = page.waitForResponse(
    res => new URL(res.url()).pathname === '/api/workspaces' && res.request().method() === 'POST'
  );
  await builder.getByRole('button', { name: 'Build Group', exact: true }).click();
  const result = await response;
  expect(result.ok(), await result.text()).toBeTruthy();
  const payload = await result.json();
  const id = payload.folder.id;
  try {
    expect(result.request().postDataJSON()).toEqual({
      name,
      kind: 'group',
      create_template_agents: false
    });
    await expect(builder).toBeHidden();
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
    expect(record.agent_instances || []).toHaveLength(0);
    expect(record.assistant_program_state).toBeFalsy();
  } finally {
    await request.delete(`/api/workspaces/${id}?confirm=true`);
  }
});

test('Step 2 uses the shared builder with cancellation, exact retry and commit-only Escape lock', async ({
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
      Object.assign(current.steps[1].preparation!, {
        exists: true,
        group_id: 'canonical-home',
        name: 'My Studio'
      });
    }
    return route.fulfill({ json: { setup_journey: current } });
  });
  await page.goto('/?setup=specialist');
  const setup = page.locator('#specialistSetupJourneyModal');
  const builder = page.locator('#buildGroupModal');
  await setup.getByRole('button', { name: 'Build Group', exact: true }).click();
  await expect(setup).toBeHidden();
  await expect(builder.getByRole('textbox', { name: 'Group name' })).toHaveValue('Studio');
  await builder.getByRole('textbox', { name: 'Group name' }).fill('My Studio');
  await builder.getByRole('button', { name: 'Review Group' }).click();
  await expect(builder.getByRole('heading', { name: 'Build “My Studio”?' })).toBeVisible();
  await builder.getByRole('button', { name: 'Cancel', exact: true }).click();
  await setup.getByRole('button', { name: 'Build Group', exact: true }).click();
  await expect(builder.getByRole('textbox', { name: 'Group name' })).toHaveValue('My Studio');
  await expect(builder.locator('#buildGroupCommit')).toBeHidden();
  expect(commits).toHaveLength(0);
  await builder.getByRole('button', { name: 'Review Group' }).click();
  await expect(builder.getByRole('heading', { name: 'Build “My Studio”?' })).toBeVisible();
  await evidence(page, '25-guided-build-group-narrow');
  const footer = await builder.locator('.modal-footer').boundingBox();
  const button = await builder.locator('#buildGroupCommit').boundingBox();
  expect(button!.y + button!.height).toBeLessThanOrEqual(footer!.y + footer!.height);
  await builder.getByRole('button', { name: 'Build Group', exact: true }).click();
  await expect.poll(() => commits.length).toBe(1);
  await page.keyboard.press('Escape');
  await expect(builder).toBeVisible();
  await expect(builder.getByRole('status')).toContainText('Finish the confirmed group change');
  release();
  await builder.getByRole('button', { name: 'Retry Confirmed Change' }).click();
  expect(commits).toHaveLength(2);
  expect(commits[0]).toEqual(commits[1]);
  expect((commits[1] as any).review_token).toBe('review-2');
  await expect(builder).toBeHidden();
  await expect(setup.locator('#specialistSetupJourneyStepTitle')).toHaveText('Prepare Tools');
  expect(genericCreates).toBe(0);
});

test('a historical project with an unavailable group never offers replacement creation', async ({
  page
}) => {
  await settled(page);
  const current = journey();
  current.receipts = { home_workspace_id: 'old-home', project_workspace_id: 'old-project' };
  current.steps[1].status = 'complete';
  current.steps[1].actions = [];
  await page.route('**/api/personal-assistant/setup-journey**', route =>
    route.fulfill({ json: { setup_journey: current } })
  );
  await page.goto('/?setup=specialist');
  const setup = page.locator('#specialistSetupJourneyModal');
  await expect(setup).toContainText('existing setup group could not be verified');
  await expect(setup.getByRole('button', { name: 'Build Group', exact: true })).toHaveCount(0);
  await expect(setup.locator('.setup-journey__step-button').nth(3)).not.toHaveAttribute(
    'data-status',
    'complete'
  );
  await expect(setup.getByRole('button', { name: 'Open Existing Workspace' })).toBeVisible();
});
