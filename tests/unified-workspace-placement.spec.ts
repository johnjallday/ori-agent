import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import { prepareFolderCreationRuntime, abandonOpenBuild } from './helpers/folder-first-runtime';
import { mkdtempSync, writeFileSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, sep } from 'node:path';

// Real server acceptance. No successful create/receipt/layout response is
// fabricated. Run only against the isolated branch demo via scripts/e2e.sh.
async function resources(request: APIRequestContext) {
  const workspaces = await (await request.get('/api/workspaces')).json();
  const agents = await (await request.get('/api/agents')).json();
  return {
    workspaces: workspaces.folders.map((workspace: { id: string }) => workspace.id).sort(),
    agents: agents.agents.map((agent: { name: string }) => agent.name).sort()
  };
}

async function stageBlueprintRole(page: Page, role: string, name: string) {
  const create = page
    .locator('#workspaceRoleRoster')
    .getByRole('button', { name: `Create an agent for ${role}`, exact: true });
  await expect(create).toBeVisible();
  await create.click();
  const form = page.locator('#addAgentModal');
  await expect(form).toBeVisible();
  await form.locator('[data-agent-create-field="name"]').fill(name);
  await form.locator('[data-agent-create-field="model"]').selectOption('ori-synthetic-model');
  await form.getByRole('button', { name: 'Add to team', exact: true }).click();
  await expect(form).toBeHidden();
  await expect(page.locator('#wizardStep3')).toBeVisible();
}

async function captureOrdinaryReview(
  page: Page,
  review: ReturnType<Page['locator']>,
  name: string
) {
  await expect(page.locator('#toastContainer').locator(':scope > *')).toHaveCount(0, {
    timeout: 12000
  });
  await page.locator('#addFolderModal .modal-body').evaluate(element => {
    element.scrollTop = 0;
  });
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-2/${name}-review.png`
  });
  await review.locator('.assisted-placement__slots').evaluate(element => {
    element.scrollIntoView({ block: 'start' });
  });
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-2/${name}-review-team.png`
  });
  await review
    .getByRole('button', { name: 'Create workspace', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-2/${name}-review-actions.png`
  });
}

test.describe('ordinary blueprint review with a local catalog-only model fixture', () => {
  test.skip(
    process.env.ORI_TEST_REVIEW_BLUEPRINTS !== '1',
    'requires isolated LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1; no paid provider'
  );
  let model: Awaited<ReturnType<typeof startJanitorBuildModel>> | undefined;
  test.beforeAll(async ({ request }) => {
    model = await startJanitorBuildModel();
    const configured = await request.post('/api/settings/system-model', {
      data: { provider: 'lmstudio', model: 'ori-synthetic-model' }
    });
    expect(configured.ok(), await configured.text()).toBeTruthy();
    expect(
      (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
    ).toBe('needs_hire');
  });
  test.afterAll(async () => {
    await model?.();
  });

  test('multi-role Research preserves saved definitions and stages new agents until explicit automatic confirmation without HQ', async ({
    page,
    request
  }) => {
    const suffix = randomUUID().slice(0, 8);
    const savedName = `Saved Scout ${suffix}`;
    // A separately confirmed prerequisite, not an effect of review/placement.
    const saved = await request.post('/api/agents', {
      data: {
        name: savedName,
        model: 'ori-synthetic-model',
        llm_provider: 'lmstudio',
        system_prompt: 'Keep this global saved definition exact. 日本語',
        role: 'specialist'
      }
    });
    expect(saved.ok(), await saved.text()).toBeTruthy();
    const savedBefore = await (
      await request.get(`/api/agents/${encodeURIComponent(savedName)}`)
    ).json();
    expect(savedBefore.model).toBe('ori-synthetic-model');
    expect(savedBefore.system_prompt).toBe('Keep this global saved definition exact. 日本語');
    const before = await resources(request);
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.locator('.workspace-template-card', { hasText: 'Research Project' }).first().click();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(`Research review ${suffix}`);
    await page.locator('#wizardNextBtn').click();
    for (const role of ['Research Lead', 'Synthesis Writer']) {
      await stageBlueprintRole(page, role, `${role} ${suffix}`);
    }
    await page
      .locator('#workspaceRoleRoster')
      .getByRole('button', { name: 'Assign an agent to Source Scout', exact: true })
      .click();
    await page
      .getByRole('button', { name: `Assign ${savedName} to Source Scout`, exact: true })
      .click();
    await page.locator('#wizardNextBtn').click();
    const review = page.locator('#workspaceCreationReviewPanel');
    await expect(review).toBeVisible();
    await expect(review).toContainText(savedName);
    await expect(review).toContainText('Research Lead');
    await expect(review).toContainText('Source Scout');
    await expect(review.getByRole('region', { name: 'Source Scout', exact: true })).toContainText(
      'ori-synthetic-model'
    );
    await expect(review).toContainText('Synthesis Writer');
    expect(await resources(request)).toEqual(before);
    expect(model!.totalCalls()).toBe(0);
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await captureOrdinaryReview(page, review, 'research');
    await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible();
    const id = await page.evaluate(
      () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
    );
    const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
    await review.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-2/research-result.png'
    });
    expect(canonical.agent_instances.map((agent: { name: string }) => agent.name).sort()).toEqual(
      [savedName, `Research Lead ${suffix}`, `Synthesis Writer ${suffix}`].sort()
    );
    expect(canonical.tags).toContain('research');
    expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
    const after = await resources(request);
    expect(after.workspaces).toEqual([...before.workspaces, id].sort());
    expect(after.agents).toEqual(
      [...before.agents, `Research Lead ${suffix}`, `Synthesis Writer ${suffix}`].sort()
    );
    const savedAfter = await (
      await request.get(`/api/agents/${encodeURIComponent(savedName)}`)
    ).json();
    // Runtime status/presentation are not the global definition; its version,
    // model, prompt, capabilities, metadata and appearance must stay identical.
    for (const key of Object.keys(savedBefore).filter(
      key => !['status', 'presentation_role'].includes(key)
    )) {
      expect(savedAfter[key], `unchanged saved definition: ${key}`).toEqual(savedBefore[key]);
    }
    expect(model!.totalCalls()).toBe(0);
  });

  test('Calendar Ops from Tree leaves connection and first work for later with an optional vacant role', async ({
    page,
    request
  }) => {
    const before = await resources(request);
    const suffix = randomUUID().slice(0, 8);
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('[data-cockpit-view="tree"]').click();
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.locator('.workspace-template-card', { hasText: 'Calendar Ops' }).first().click();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(`Calendar review ${suffix}`);
    await page.locator('#wizardNextBtn').click();
    await stageBlueprintRole(page, 'Scheduler', `Scheduler ${suffix}`);
    await page.locator('#wizardNextBtn').click();
    const review = page.locator('#workspaceCreationReviewPanel');
    await expect(review).toContainText('Connect your calendar');
    await expect(review).toContainText('Meeting Prep');
    await expect(review).toContainText('No account or runtime access');
    expect(await resources(request)).toEqual(before);
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await captureOrdinaryReview(page, review, 'calendar');
    await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible();
    await expect(review).toContainText('Next setup:');
    await expect(review).toContainText('Connect your calendar');
    await expect(review).toContainText('setup readiness has not been verified');
    await review.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-2/calendar-result.png'
    });
    const id = await page.evaluate(
      () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
    );
    const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
    expect(canonical.agent_instances.map((agent: { name: string }) => agent.name)).toEqual([
      `Scheduler ${suffix}`
    ]);
    const bindings = canonical.mcp_bindings || [];
    expect(bindings).toHaveLength(1);
    expect(bindings[0].alias).toBe('workspace-files');
    expect(bindings[0].server_name).toBe('filesystem');
    const directories = (await (await request.get(`/api/workspaces/${id}/directories`)).json())
      .directories;
    const primary = directories.find(
      (directory: { id: string }) => directory.id === canonical.primary_directory_id
    );
    expect(primary).toBeTruthy();
    expect(bindings[0].config.roots).toEqual([primary.path]);
    expect(canonical.tasks.length).toBe(3);
    expect(
      canonical.tasks.every((task: { status: string }) => task.status === 'pending')
    ).toBeTruthy();
    const setupResponse = await request.get(`/api/workspaces/${id}/setup-wizard`);
    expect(setupResponse.ok(), await setupResponse.text()).toBeTruthy();
    const setupBody = await setupResponse.json();
    expect((setupBody.setup || setupBody.data?.setup).state).toBe('not_started');
    expect(await resources(request)).toEqual({
      workspaces: [...before.workspaces, id].sort(),
      agents: [...before.agents, `Scheduler ${suffix}`].sort()
    });
    expect(model!.totalCalls()).toBe(0);
  });

  for (const [blueprint, role] of [
    ['Code Project', 'Development Assistant'],
    ['Writing Project', 'Writing Coach']
  ] as const) {
    test(`${blueprint} saves the edited goal but leaves its first-work owner untouched until opening`, async ({
      page,
      request
    }) => {
      const before = await resources(request);
      const suffix = randomUUID().slice(0, 8);
      const goal = `Keep this first-work brief exact. 日本語 ${suffix}`;
      await request.post('/api/onboarding/skip');
      await page.goto('/');
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      await page.locator('.workspace-template-card', { hasText: blueprint }).first().click();
      await page.locator('#wizardNextBtn').click();
      await page.locator('#folderNameInput').fill(`${blueprint} review ${suffix}`);
      await page.locator('#folderGoalInput').fill(goal);
      await page.locator('#wizardNextBtn').click();
      await stageBlueprintRole(page, role, `${role} ${suffix}`);
      await page.locator('#wizardNextBtn').click();
      const review = page.locator('#workspaceCreationReviewPanel');
      await expect(review).toContainText('existing goal and first-work flow');
      await expect(review.locator('[data-placement-field="mission"]')).toHaveValue(goal);
      expect(await resources(request)).toEqual(before);
      await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
      await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible();
      const id = await page.evaluate(
        () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
      );
      const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
      expect(canonical.mission_state.mission).toBe(goal);
      expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
      expect(canonical.mission_state.goal_first_run_state || '').toBe('');
      expect(canonical.mission_state.last_mission_run_at).toBeUndefined();
      expect(await resources(request)).toEqual({
        workspaces: [...before.workspaces, id].sort(),
        agents: [...before.agents, `${role} ${suffix}`].sort()
      });
      expect(model!.totalCalls()).toBe(0);
    });
  }

  test('an unavailable deep link cannot silently create Blank; an explicit replacement remains usable', async ({
    page,
    request
  }) => {
    const before = await resources(request);
    await request.post('/api/onboarding/skip');
    await page.goto('/workspaces?create=1&blueprint=missing-unified-blueprint');
    await expect(page.locator('#workspaceBlueprintRequestStatus')).toContainText(
      'not create a Blank substitute'
    );
    await expect(page.locator('#wizardStep1')).toBeVisible();
    expect(await resources(request)).toEqual(before);
    await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
    await expect(page.locator('#workspaceBlueprintRequestStatus')).toBeHidden();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(`Explicit replacement ${randomUUID().slice(0, 8)}`);
    await page.locator('#wizardNextBtn').click();
    await page.getByRole('checkbox', { name: 'Create without agents' }).check();
    await page.locator('#wizardNextBtn').click();
    const review = page.locator('#workspaceCreationReviewPanel');
    expect(await resources(request)).toEqual(before);
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible();
    const after = await resources(request);
    expect(after.workspaces).toHaveLength(before.workspaces.length + 1);
    expect(after.agents).toEqual(before.agents);
    expect(model!.totalCalls()).toBe(0);
  });

  for (const entry of ['catalog', 'folder', 'deep-link'] as const) {
    test(`custom ${entry} declaration keeps typed values through advanced editing and creates actual scaffold bytes without agents`, async ({
      page,
      request
    }) => {
      const sandbox = process.env.ORI_REVIEW_SANDBOX || '';
      test.skip(!sandbox, 'requires the exact disposable demo root for filesystem assertions');
      const sandboxRoot = realpathSync(sandbox);
      expect(sandboxRoot.startsWith(realpathSync(tmpdir()) + sep)).toBeTruthy();
      const source = realpathSync(mkdtempSync(join(tmpdir(), 'ori-review-blueprint-')));
      const suffix = randomUUID().slice(0, 8);
      const title = `Neutral blueprint ${suffix}`;
      const bytes = 'RATE {{input.rate}} MODE {{input.mode}}\n';
      let openRequests = 0;
      page.on('request', request => {
        if (request.method() === 'POST' && /\/project\/open$/.test(new URL(request.url()).pathname))
          openRequests++;
      });
      writeFileSync(join(source, '{{name}}.cfg'), bytes, { mode: 0o600 });
      writeFileSync(
        join(source, 'template.json'),
        JSON.stringify({
          name: title,
          description: 'A scoped project with typed document options.',
          agents: [],
          project_entry:
            entry === 'catalog'
              ? { relative_path: '{{name}}.cfg', open_after_create_default: false }
              : undefined,
          inputs: {
            schema_version: 1,
            title: 'Document options',
            apply_to: ['{{name}}.cfg'],
            fields: [
              {
                id: 'rate',
                label: 'Rate',
                type: 'number',
                min: 1,
                max: 100,
                step: 1,
                default: 24,
                unit: 'units'
              },
              {
                id: 'mode',
                label: 'Document mode',
                type: 'select',
                default: 'draft',
                options: [
                  { value: 'draft', label: 'Draft' },
                  { value: 'final', label: 'Final' }
                ]
              },
              { id: 'brief', label: 'Brief', type: 'text', default: '' },
              {
                id: 'reference',
                label: 'Reference link',
                type: 'url',
                default: 'https://example.invalid/default'
              }
            ]
          }
        }),
        { mode: 0o600 }
      );
      const manifestBytes = readFileSync(join(source, 'template.json'), 'utf8');
      let templateID = '';
      if (entry !== 'folder') {
        const imported = await request.post('/api/project-templates/import', {
          data: { path: source, name: title }
        });
        expect(imported.ok(), await imported.text()).toBeTruthy();
        templateID = (await imported.json()).template.id;
      }
      const before = await resources(request);
      await request.post('/api/onboarding/skip');
      if (entry === 'deep-link') {
        await page.goto(`/workspaces?create=1&blueprint=${encodeURIComponent(templateID)}`);
        await expect(page.getByRole('radio', { name: title, exact: true })).toHaveAttribute(
          'aria-checked',
          'true'
        );
        await expect(page).not.toHaveURL(/[?&](create|blueprint)=/);
      } else {
        await page.goto('/');
        await page.locator('#cockpitCreateWorkspaceBtn').click();
        if (entry === 'catalog')
          await page.getByRole('radio', { name: title, exact: true }).click();
      }
      await page.locator('#wizardNextBtn').click();
      if (entry === 'folder') {
        await page.locator('#folderAdvancedDisclosure > summary').click();
        await page.locator('#projectTemplatePathInput').fill(source);
        await expect(page.getByLabel('Rate', { exact: true })).toBeVisible();
      }
      await page.locator('#folderNameInput').fill(`Typed workspace ${suffix}`);
      if (entry === 'catalog') await page.locator('#projectTemplateOpenAfterCreateToggle').check();
      await page.getByLabel('Rate', { exact: true }).fill('1000');
      await page.locator('#wizardNextBtn').click();
      await expect(page.locator('#wizardStep2')).toBeVisible();
      await expect(
        page
          .getByRole('region', { name: 'Document options', exact: true })
          .getByText('Rate must be between 1 and 100.', { exact: true })
      ).toBeVisible();
      expect(await resources(request)).toEqual(before);
      await page.getByLabel('Rate', { exact: true }).fill('48');
      await page.getByLabel('Document mode', { exact: true }).selectOption('final');
      await page.getByLabel('Brief', { exact: true }).fill('Keep this exact brief 日本語');
      await page
        .getByLabel('Reference link', { exact: true })
        .fill('https://example.invalid/reference');
      await page.locator('#wizardNextBtn').click();
      await page.locator('#wizardNextBtn').click();
      const review = page.locator('#workspaceCreationReviewPanel');
      await expect(review).toContainText('Rate: 48 units');
      await expect(review).toContainText('Document mode: Final');
      await expect(review).toContainText('Keep this exact brief 日本語');
      await expect(review).toContainText('https://example.invalid/reference');
      await review
        .getByRole('button', { name: 'Edit details & advanced settings', exact: true })
        .click();
      await expect(page.getByLabel('Rate', { exact: true })).toHaveValue('48');
      if (entry === 'catalog')
        await expect(page.locator('#projectTemplateOpenAfterCreateToggle')).toBeChecked();
      await page.locator('#wizardNextBtn').click();
      await page.locator('#wizardNextBtn').click();
      expect(await resources(request)).toEqual(before);
      await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
      await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible();
      const id = await page.evaluate(
        () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
      );
      const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
      expect(canonical.blueprint_inputs.values).toMatchObject({
        rate: '48',
        mode: 'final',
        brief: 'Keep this exact brief 日本語',
        reference: 'https://example.invalid/reference'
      });
      const directories = (await (await request.get(`/api/workspaces/${id}/directories`)).json())
        .directories;
      const project = directories.find(
        (directory: { id: string }) => directory.id === canonical.primary_directory_id
      );
      expect(project).toBeTruthy();
      const projectPath = realpathSync(project.path);
      expect(projectPath.startsWith(sandboxRoot + sep)).toBeTruthy();
      const file = readdirSync(projectPath).find(name => name.endsWith('.cfg'));
      expect(file).toBeTruthy();
      expect(readFileSync(join(projectPath, file!), 'utf8')).toBe('RATE 48 MODE final\n');
      expect(readFileSync(join(source, '{{name}}.cfg'), 'utf8')).toBe(bytes);
      expect(readFileSync(join(source, 'template.json'), 'utf8')).toBe(manifestBytes);
      expect(canonical.agent_instances || []).toHaveLength(0);
      expect(canonical.tasks || []).toHaveLength(0);
      expect(canonical.scheduled_tasks || []).toHaveLength(0);
      expect(openRequests).toBe(entry === 'catalog' ? 1 : 0);
      await page.reload();
      await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeVisible();
      expect(openRequests).toBe(entry === 'catalog' ? 1 : 0);
      expect(await resources(request)).toEqual({
        workspaces: [...before.workspaces, id].sort(),
        agents: before.agents
      });
      expect(model!.totalCalls()).toBe(0);
    });
  }
});

test.describe('ordinary assistant draft review with a real host and local model', () => {
  test.skip(
    process.env.ORI_TEST_REVIEW_BUILDS !== '1',
    'requires the isolated loopback-only build runtime'
  );
  let model: Awaited<ReturnType<typeof startJanitorBuildModel>> | undefined;
  test.beforeAll(async ({ request }) => {
    model = await startJanitorBuildModel();
    await prepareFolderCreationRuntime(request);
  });
  test.afterAll(async () => {
    await model?.();
  });
  for (const scenario of ['ordinary', 'failure'] as const) {
    test(`${scenario} model response cannot create; the explicit reviewed action owns completion`, async ({
      page,
      request
    }) => {
      await abandonOpenBuild(request);
      const name = `Assisted notebook ${randomUUID().slice(0, 8)}`;
      model!.scenario({ mode: scenario, createNow: true, name });
      const before = await resources(request);
      const calls = model!.calls();
      await request.post('/api/onboarding/skip');
      await page.goto('/');
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      const composer = page.locator('#workspaceBuildComposerInput');
      await expect(composer).toBeVisible();
      await composer.fill('Propose an agentless notebook. Do not start any work.');
      const turn = page.waitForResponse(response =>
        /\/build-sessions\/[^/]+\/turns$/.test(new URL(response.url()).pathname)
      );
      await composer.press('Enter');
      await turn;
      if (scenario === 'failure') {
        await page
          .locator('#workspaceBuildPane')
          .getByRole('button', { name: 'Set up manually', exact: true })
          .click();
        await expect(page.locator('#wizardStep2')).toBeVisible();
        await page.locator('#folderNameInput').fill(name);
        await page.locator('#wizardNextBtn').click();
        await page.getByRole('checkbox', { name: 'Create without agents' }).check();
        await page.locator('#wizardNextBtn').click();
      }
      if (scenario === 'ordinary') await page.locator('#workspaceBuildEditDraftBtn').click();
      const review = page.locator('#workspaceCreationReviewPanel');
      await expect(review).toBeVisible();
      await expect(
        review.getByRole('textbox', { name: 'Workspace name', exact: true })
      ).toHaveValue(name);
      expect(await resources(request)).toEqual(before);
      expect(model!.calls()).toBe(calls + 1);
      const buildID = await page.evaluate(
        () => (window as any).sessionManager.workspaceBuild.session.id
      );
      await review
        .getByRole('textbox', { name: 'Goal', exact: true })
        .fill('Keep this exact assisted brief 日本語');
      await review.getByRole('textbox', { name: 'Goal', exact: true }).press('Tab');
      await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
      await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible();
      const id = await page.evaluate(
        () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
      );
      const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
      expect(canonical.mission_state.mission).toBe('Keep this exact assisted brief 日本語');
      expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
      expect(canonical.agent_instances || []).toHaveLength(0);
      const finished = (
        await (await request.get(`/api/workspaces/build-sessions/${buildID}`)).json()
      ).session;
      expect(finished.status).toBe('created');
      expect(finished.created_workspace_id).toBe(id);
      expect(await resources(request)).toEqual({
        workspaces: [...before.workspaces, id].sort(),
        agents: before.agents
      });
      expect(model!.calls()).toBe(calls + 1);
    });
  }
});

async function reviewBlank(request: APIRequestContext) {
  const id = randomUUID();
  const response = await request.post('/api/workspaces/create-operations', {
    data: {
      id,
      request: {
        name: `Reviewed notebook ${id.slice(0, 8)}`,
        blank: true,
        create_template_agents: false,
        existing_agent_names: []
      }
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  expect(body.operation.state).toBe('reviewed');
  expect(body.result).toBeUndefined();
  expect(body.operation.workspace_id).toBeUndefined();
  return { id, digest: body.operation.review_digest as string };
}

async function openBlankReview(page: Page, name: string, recovery = false) {
  await page.request.post('/api/onboarding/skip');
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toBeVisible();
  if (!recovery)
    await expect(
      review.getByRole('button', { name: 'Use automatic placement', exact: true })
    ).toBeVisible();
  return review;
}

test('Blank full review closes for placement and one canvas Enter creates the reviewed workspace', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const name = `Placed notebook ${randomUUID().slice(0, 8)}`;
  const review = await openBlankReview(page, name);
  await review
    .getByRole('textbox', { name: 'Goal', exact: true })
    .fill('Keep this exact reviewed purpose.');
  await review.getByRole('textbox', { name: 'Goal', exact: true }).press('Tab');
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-review.png',
    fullPage: true
  });
  expect(await resources(request)).toEqual(before);
  await review
    .getByRole('button', { name: 'Place & create on map', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-review-actions.png',
    fullPage: true
  });
  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  expect(await resources(request)).toEqual(before);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-map.png',
    fullPage: true
  });
  await page.locator('[data-ws-map-viewport]').focus();
  for (
    let index = 0;
    index < 60 && /is-invalid/.test((await preview.getAttribute('class')) || '');
    index++
  )
    await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(page.locator('#cockpitContextModal')).toBeHidden();
  const operation = await page.evaluate(() => {
    const review = (window as any).sessionManager.workspaceCreatorContext.ordinaryReview;
    return { reference: review.reference, candidate: review.candidate };
  });
  const observed = await (
    await request.get(`/api/workspaces/create-operations/${operation.reference.id}`)
  ).json();
  const workspace = observed.result.folder;
  expect(workspace.name).toBe(name);
  const canonical = await (await request.get(`/api/workspaces/${workspace.id}`)).json();
  expect(canonical.mission_state.mission).toBe('Keep this exact reviewed purpose.');
  expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, workspace.id].sort(),
    agents: before.agents
  });
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[workspace.id]).toEqual(operation.candidate);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-result.png',
    fullPage: true
  });
});

test('Blank automatic confirmation works without a mounted map and cancellation is inert', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const review = await openBlankReview(page, `Automatic notebook ${randomUUID().slice(0, 8)}`);
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect(await resources(request)).toEqual(before);
  await page.locator('[data-cockpit-view="tree"]').click();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(`Tree notebook ${randomUUID().slice(0, 8)}`);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  await expect(
    review.getByRole('button', { name: 'Place & create on map', exact: true })
  ).toBeDisabled();
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(await resources(request)).toEqual(before);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-automatic-review.png',
    fullPage: true
  });
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(page.locator('#createFolderBtn')).toBeHidden();
  await expect(page.locator('#workspaceCreatorKindChoice')).toBeHidden();
  const after = await resources(request);
  expect(after.workspaces).toHaveLength(before.workspaces.length + 1);
  expect(after.agents).toEqual(before.agents);
  await page.evaluate(() => (window as any).sessionManager.createFolder());
  expect(await resources(request)).toEqual(after);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-automatic-result.png',
    fullPage: true
  });
});

test('Blank Escape and automatic fallback preserve edits but clear a manual candidate without creation', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const name = `Returned notebook ${randomUUID().slice(0, 8)}`;
  const review = await openBlankReview(page, name);
  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  await expect(review.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    name
  );
  expect(await resources(request)).toEqual(before);
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceMapPlacement)
  ).toBeNull();
  expect(await resources(request)).toEqual(before);
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  const id = await page.evaluate(
    () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
  );
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[id]).toBeUndefined();
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, id].sort(),
    agents: before.agents
  });
});

test('Blank UI observes lost confirmation delivery and reloads the original result instead of resubmitting', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const review = await openBlankReview(page, `Recovered notebook ${randomUUID().slice(0, 8)}`);
  let id = '',
    posts = 0;
  await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
    posts++;
    const response = await route.fetch();
    expect(response.status()).toBe(201);
    id = (await response.json()).folder.id;
    await route.abort('failed');
  });
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await openBlankReview(page, `Never created ${randomUUID().slice(0, 8)}`, true);
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  expect(posts).toBe(1);
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, id].sort(),
    agents: before.agents
  });
});

test('ordinary Blank review is inert and repeated confirmation observes one canonical resource', async ({
  request
}) => {
  const before = await resources(request);
  const operation = await reviewBlank(request);
  expect(await resources(request)).toEqual(before);
  const endpoint = `/api/workspaces/create-operations/${operation.id}`;
  for (let index = 0; index < 2; index++) {
    const response = await request.get(endpoint);
    expect(response.ok(), await response.text()).toBeTruthy();
    expect((await response.json()).operation.state).toBe('reviewed');
  }
  expect(await resources(request)).toEqual(before);
  const confirms = await Promise.all(
    Array.from({ length: 4 }, () =>
      request.post(`${endpoint}/confirm`, { data: { review_digest: operation.digest } })
    )
  );
  for (const response of confirms) expect([201, 202]).toContain(response.status());
  const observed = await (await request.get(endpoint)).json();
  expect(observed.operation.state).toBe('complete');
  const workspace = observed.result.folder;
  expect(workspace.shared_data.workspace_creation_operation_id).toBe(operation.id);
  expect(workspace.shared_data.workspace_creation_operation_digest).toBe(operation.digest);
  const canonical = await (await request.get(`/api/workspaces/${workspace.id}`)).json();
  expect(canonical.id).toBe(workspace.id);
  expect(canonical.shared_data.workspace_creation_operation_id).toBe(operation.id);
  const after = await resources(request);
  expect(after.workspaces).toEqual([...before.workspaces, workspace.id].sort());
  expect(after.agents).toEqual(before.agents);
  for (let index = 0; index < 2; index++) {
    const response = await request.post(`${endpoint}/confirm`, {
      data: { review_digest: operation.digest }
    });
    expect((await response.json()).folder.id).toBe(workspace.id);
  }
  expect(await resources(request)).toEqual(after);
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[workspace.id]).toBeUndefined();
});

test('lost real confirmation delivery and reload read the original receipt without creating again', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const operation = await reviewBlank(request);
  const endpoint = `/api/workspaces/create-operations/${operation.id}`;
  await request.post('/api/onboarding/skip');
  await page.goto('/');
  let acceptedID = '';
  await page.route(`**${endpoint}/confirm`, async route => {
    const response = await route.fetch();
    expect(response.status()).toBe(201);
    acceptedID = (await response.json()).folder.id;
    // Real effects completed; lose only delivery to the browser. Never fulfill
    // a synthetic success or execute another confirmation to recover it.
    await route.abort('failed');
  });
  const delivered = await page.evaluate(
    async ({ endpoint, digest }) => {
      try {
        await fetch(`${endpoint}/confirm`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ review_digest: digest })
        });
        return true;
      } catch {
        return false;
      }
    },
    { endpoint, digest: operation.digest }
  );
  expect(delivered).toBe(false);
  expect(acceptedID).not.toBe('');
  await page.reload();
  for (let index = 0; index < 3; index++) {
    const response = await request.get(endpoint);
    const observed = await response.json();
    expect(observed.operation.state).toBe('complete');
    expect(observed.result.folder.id).toBe(acceptedID);
  }
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, acceptedID].sort(),
    agents: before.agents
  });
});
