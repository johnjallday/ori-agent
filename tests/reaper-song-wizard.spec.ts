import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Reaper Song runtime setup, in a browser.
 *
 * Run with:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test tests/reaper-song-wizard.spec.ts
 *
 * This fixture-backed journey never claims to prove a real REAPER process. It
 * proves the honest browser contract: creation is not blocked, File-only is a
 * complete no-nag mode, assisted setup shows one authoritative checklist, and
 * choosing a mode installs/grants/tests nothing by itself.
 */

function dialog(page: Page) {
  return page.locator('#setupWizardDialog');
}

async function createReaperWorkspace(request: APIRequestContext, label: string): Promise<string> {
  const res = await request.post('/api/workspaces', {
    data: {
      name: `${label} ${Date.now().toString(36)}`,
      description: '',
      template_id: 'reaper-song',
      create_template_agents: true
    }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return (body.folder?.id || body.workspace?.id) as string;
}

async function setupState(request: APIRequestContext, workspaceId: string) {
  const res = await request.get(`/api/workspaces/${workspaceId}/setup-wizard`);
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return body.setup || body.data?.setup;
}

const LIVE_CLAIMS =
  /connected to reaper|web remote is ready|reaper session is open|is controlling reaper/i;

test.describe.configure({ mode: 'serial' });

test.describe('Reaper Song setup wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/onboarding/skip').catch(() => {});
  });

  test('File-only completes setup with no plugin, grant, or model setup task', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper File Only');
    expect((await setupState(request, workspaceId)).state).toBe('not_started');

    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await expect(page.locator('#setupWizardStepTitle')).toHaveText(
      'Choose how Ori works with REAPER'
    );

    const content = page.locator('#setupWizardStepContent');
    await expect(content).toContainText('Live REAPER control is not configured or tested');
    await expect(content).toContainText('Ori-assisted REAPER');
    await expect(page.locator('#setupWizardPrimary')).toBeDisabled();

    await page.getByRole('button', { name: 'File-only', exact: true }).click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Set up local REAPER control', {
      timeout: 15000
    });
    await expect(content).toContainText('not configured or tested');
    await expect(content).not.toHaveText(LIVE_CLAIMS);

    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review Reaper Song setup', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });

    const ready = await setupState(request, workspaceId);
    expect(ready.state).toBe('ready');
    expect(ready.steps[0].selected_option).toBe('file_only');

    const readiness = await (
      await request.get(`/api/workspaces/${workspaceId}/reaper-setup`)
    ).json();
    expect(readiness.plugin_installed).toBeFalsy();
    expect(readiness.workspace_native_cli_enabled).toBeFalsy();
    expect(readiness.agent_native_cli_enabled).toBeFalsy();

    const workspace = await (await request.get(`/api/workspaces/${workspaceId}`)).json();
    const record = workspace.folder || workspace.workspace || workspace;
    const tasks = record.tasks || [];
    expect(
      tasks.find((task: { description?: string }) =>
        (task.description || '').includes('REAPER setup choices')
      ),
      'new workspaces do not seed a setup-help task'
    ).toBeUndefined();
    expect(tasks).toHaveLength(2);
    expect(
      tasks.find((task: { description?: string }) =>
        (task.description || '').includes('Adjust the new REAPER session')
      )?.status
    ).not.toBe('completed');
    expect(record.runtime_state?.grants || []).toHaveLength(0);
  });

  test('a reloaded File-only workspace stays finished and stops asking', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Persisted');
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await page.getByRole('button', { name: 'File-only', exact: true }).click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Set up local REAPER control', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });

    await page.goto(`/workspaces/${workspaceId}`);
    await expect(page.locator('#setupWizardBannerState')).toHaveText('File-only', {
      timeout: 15000
    });
    await expect(page.locator('#setupWizardBanner')).toContainText(
      'Live REAPER was not configured or tested.'
    );
    await expect(dialog(page)).toBeHidden();
    expect((await setupState(request, workspaceId)).state).toBe('ready');
  });

  test('a blocked live task creates no run/model trace and exposes file fallback only as an explicit choice', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Blocked Task');
    const mode = await request.put(`/api/workspaces/${workspaceId}/runtime-capabilities/mode`, {
      data: { mode_id: 'file_only' }
    });
    expect(mode.ok(), await mode.text()).toBeTruthy();

    const create = await request.post('/api/orchestration/tasks', {
      data: {
        workspace_id: workspaceId,
        from: 'user',
        to: 'Reaper Producer',
        description: 'Apply one live REAPER change',
        required_capabilities: ['reaper_live_control'],
        file_fallback_for: ['reaper_live_control']
      }
    });
    expect(create.ok(), await create.text()).toBeTruthy();
    const taskId = (await create.json()).task.id as string;

    const execute = await request.post('/api/orchestration/tasks/execute', {
      data: { task_id: taskId }
    });
    expect(execute.status()).toBe(202);

    await expect
      .poll(async () => {
        const response = await request.get(`/api/orchestration/tasks?id=${taskId}`);
        const task = await response.json();
        return task.context?.human_loop?.reason_code || '';
      })
      .toBe('runtime_mode_not_enabled');

    const blocked = await (await request.get(`/api/orchestration/tasks?id=${taskId}`)).json();
    expect(blocked.started_at).toBeFalsy();
    expect(blocked.current_run_id || '').toBe('');
    expect(blocked.execution_trace || []).toHaveLength(0);
    expect(blocked.execution_history || []).toHaveLength(0);
    expect(blocked.context.human_loop.repair.url).toContain('runtime_setup=1');

    await page.goto(`/workspaces/${workspaceId}/task/${taskId}`);
    await expect(page.getByRole('button', { name: /project-file fallback/i })).toBeVisible({
      timeout: 15000
    });
    const stillBlocked = await (await request.get(`/api/orchestration/tasks?id=${taskId}`)).json();
    expect(stillBlocked.started_at).toBeFalsy();
    expect(stillBlocked.current_run_id || '').toBe('');
  });

  test('fixture-backed verification distinguishes Configured, Connected, Offline, Wrong project, and regression', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper State Fixture');
    let setupReady = false;
    let durableReadsAsNotChecked = false;
    let automaticLiveRechecks = 0;
    let runtime: any = {
      workspace_id: workspaceId,
      applicable: true,
      contract_version: 1,
      selected_mode_id: 'ori_assisted',
      modes: [
        { id: 'file_only', label: 'File-only', description: 'Use files.' },
        {
          id: 'ori_assisted',
          label: 'Ori-assisted REAPER',
          description: 'Use live control.',
          selected: true
        }
      ],
      durable_state: 'in_progress',
      live_state: 'not_checked',
      first_blocker: {
        requirement_key: 'reaper_live_control',
        reason_code: 'verification_required',
        summary: 'Run the harmless REAPER connection test.',
        action: {
          token: 'test_reaper_connection',
          code: 'test_reaper_connection',
          label: 'Test REAPER connection'
        }
      },
      requirements: [
        {
          key: 'reaper_live_control',
          label: 'Local REAPER control',
          durable_state: 'in_progress',
          live_state: 'not_checked',
          reason_code: 'verification_required',
          summary: 'Run the harmless REAPER connection test.',
          verification_needed: true
        }
      ]
    };
    const setupPayload = () => ({
      workspace_id: workspaceId,
      applicable: true,
      state: setupReady ? 'ready' : 'needs_attention',
      blueprint_id: 'reaper-song',
      blueprint_name: 'Reaper Song',
      title: 'Set up Reaper Song',
      current_step_id: setupReady ? '' : 'live-control',
      auto_open: false,
      dismissed: true,
      completed_at: '2026-08-17T09:00:00Z',
      steps: [
        {
          id: 'mode',
          kind: 'runtime_mode',
          required: true,
          title: 'Choose how Ori works with REAPER',
          status: 'complete',
          selected_option: 'ori_assisted',
          options: [
            { id: 'file_only', label: 'File-only', description: 'Use files.' },
            {
              id: 'ori_assisted',
              label: 'Ori-assisted REAPER',
              description: 'Use live control.',
              selected: true
            }
          ]
        },
        {
          id: 'live-control',
          kind: 'runtime_readiness',
          runtime_requirement_key: 'reaper_live_control',
          required: true,
          title: 'Set up local REAPER control',
          status: setupReady ? 'complete' : 'blocked',
          action: setupReady ? '' : 'recheck',
          summary: setupReady ? 'Configured.' : 'Finish verification.'
        },
        {
          id: 'summary',
          kind: 'summary',
          required: true,
          title: 'Review Reaper Song setup',
          status: 'complete'
        }
      ]
    });

    await page.route(`**/api/workspaces/${workspaceId}/setup-wizard**`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, setup: setupPayload() })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/runtime-capabilities**`, async route => {
      const requestURL = route.request().url();
      const method = route.request().method();
      if (requestURL.endsWith('/verify')) {
        setupReady = true;
        runtime = {
          ...runtime,
          durable_state: 'configured',
          live_state: 'available',
          first_blocker: null,
          first_verified_at: '2026-08-17T10:00:00Z',
          last_verified_at: '2026-08-17T10:00:00Z',
          requirements: [
            {
              key: 'reaper_live_control',
              label: 'Local REAPER control',
              durable_state: 'configured',
              live_state: 'available',
              first_verified_at: '2026-08-17T10:00:00Z',
              summary: 'REAPER is connected to this workspace project now.'
            }
          ]
        };
      }
      if (method === 'POST' && requestURL.endsWith('/recheck')) {
        automaticLiveRechecks += 1;
      }
      let responseRuntime = runtime;
      if (
        durableReadsAsNotChecked &&
        method === 'GET' &&
        requestURL.endsWith('/runtime-capabilities')
      ) {
        responseRuntime = {
          ...runtime,
          live_state: 'not_checked',
          requirements: runtime.requirements.map((requirement: any) => ({
            ...requirement,
            live_state: 'not_checked'
          }))
        };
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, runtime: responseRuntime })
      });
    });

    await page.goto(`/workspaces/${workspaceId}?runtime_setup=1`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await page.getByRole('button', { name: 'Test REAPER connection' }).click();
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Connected now');
    await expect(page.locator('#setupWizardStepContent')).toContainText('First verified');

    durableReadsAsNotChecked = true;
    await page.reload();
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Connected now');
    expect(automaticLiveRechecks).toBe(1);
    durableReadsAsNotChecked = false;

    runtime = {
      ...runtime,
      live_state: 'offline',
      requirements: [
        { ...runtime.requirements[0], live_state: 'offline', summary: 'REAPER is offline.' }
      ]
    };
    await page.evaluate(() => (window as any).SetupWizard.refreshRuntime());
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Configured · REAPER offline');

    runtime = {
      ...runtime,
      live_state: 'wrong_target',
      requirements: [
        {
          ...runtime.requirements[0],
          live_state: 'wrong_target',
          summary: 'REAPER has a different project open.'
        }
      ]
    };
    await page.evaluate(() => (window as any).SetupWizard.refreshRuntime());
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Wrong project');

    runtime = {
      ...runtime,
      durable_state: 'needs_attention',
      live_state: 'unavailable',
      first_blocker: {
        requirement_key: 'reaper_live_control',
        reason_code: 'reaper_plugin_disabled',
        summary: 'The REAPER plugin is installed but disabled.',
        action: {
          token: 'enable_reaper_plugin',
          code: 'enable_reaper_plugin',
          label: 'Enable REAPER plugin'
        }
      },
      requirements: [
        {
          ...runtime.requirements[0],
          durable_state: 'needs_attention',
          live_state: 'unavailable',
          reason_code: 'reaper_plugin_disabled',
          summary: 'The REAPER plugin is installed but disabled.'
        }
      ]
    };
    await page.evaluate(() => (window as any).SetupWizard.refreshRuntime());
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Needs attention');
    await expect(page.getByRole('button', { name: 'Enable REAPER plugin' })).toBeVisible();
    await expect(page.locator('#reaperReadinessCard')).toHaveCount(0);
    await expect(page.locator('#reaperReadinessChip')).toHaveCount(0);
  });

  test('migrated assisted attention stays visible without surprise auto-open', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Migrated Fixture');
    const migrated = {
      workspace_id: workspaceId,
      applicable: true,
      state: 'needs_attention',
      blueprint_id: 'reaper-song',
      blueprint_name: 'Reaper Song',
      title: 'Set up Reaper Song',
      current_step_id: 'live-control',
      auto_open: false,
      dismissed: true,
      completed_at: '2026-08-17T09:00:00Z',
      steps: [
        {
          id: 'mode',
          kind: 'runtime_mode',
          required: true,
          title: 'Choose how Ori works with REAPER',
          status: 'complete',
          selected_option: 'ori_assisted'
        },
        {
          id: 'live-control',
          kind: 'runtime_readiness',
          runtime_requirement_key: 'reaper_live_control',
          required: true,
          title: 'Set up local REAPER control',
          status: 'blocked',
          action: 'recheck'
        },
        {
          id: 'summary',
          kind: 'summary',
          required: true,
          title: 'Review Reaper Song setup',
          status: 'complete'
        }
      ]
    };
    const runtime = {
      applicable: true,
      selected_mode_id: 'ori_assisted',
      durable_state: 'in_progress',
      live_state: 'not_checked',
      first_blocker: {
        reason_code: 'verification_required',
        summary: 'Finish REAPER verification.'
      },
      requirements: [
        {
          key: 'reaper_live_control',
          durable_state: 'in_progress',
          live_state: 'not_checked',
          reason_code: 'verification_required',
          summary: 'Finish REAPER verification.'
        }
      ]
    };
    await page.route(`**/api/workspaces/${workspaceId}/setup-wizard**`, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ setup: migrated })
      })
    );
    await page.route(`**/api/workspaces/${workspaceId}/runtime-capabilities**`, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ runtime })
      })
    );

    await page.goto(`/workspaces/${workspaceId}`);
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Needs attention', {
      timeout: 15000
    });
    await expect(page.locator('#setupWizardBanner')).toContainText('Finish REAPER verification');
    await expect(dialog(page)).toBeHidden();
  });

  test('compatible-agent repair opens the workspace snapshot picker and returns to setup', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Agent Repair');
    const runtime = {
      workspace_id: workspaceId,
      applicable: true,
      contract_version: 1,
      selected_mode_id: 'ori_assisted',
      modes: [
        { id: 'file_only', label: 'File-only', description: 'Use project files.' },
        {
          id: 'ori_assisted',
          label: 'Ori-assisted REAPER',
          description: 'Use verified live control.',
          selected: true
        }
      ],
      durable_state: 'in_progress',
      live_state: 'not_checked',
      first_blocker: {
        requirement_key: 'reaper_live_control',
        reason_code: 'cli_agent_required',
        summary: 'Choose a Codex or Claude Code workspace agent for local REAPER control.',
        action: {
          token: 'choose_reaper_agent',
          code: 'choose_reaper_agent',
          label: 'Choose compatible agent'
        }
      },
      requirements: [
        {
          key: 'reaper_live_control',
          durable_state: 'in_progress',
          live_state: 'not_checked',
          reason_code: 'cli_agent_required',
          summary: 'Choose a Codex or Claude Code workspace agent for local REAPER control.'
        }
      ]
    };
    let agentSave: { model?: string; llm_provider?: string } | null = null;
    await page.route(`**/api/workspaces/${workspaceId}/runtime-capabilities**`, route => {
      if (route.request().method() !== 'GET') return route.continue();
      const projected = agentSave
        ? {
            ...runtime,
            first_blocker: {
              requirement_key: 'reaper_live_control',
              reason_code: 'reaper_access_required',
              summary: 'The selected workspace agent does not have REAPER access.',
              action: {
                token: 'grant_reaper_access',
                code: 'grant_reaper_access',
                label: 'Grant REAPER access'
              }
            },
            requirements: [
              {
                ...runtime.requirements[0],
                reason_code: 'reaper_access_required',
                summary: 'The selected workspace agent does not have REAPER access.'
              }
            ]
          }
        : runtime;
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ runtime: projected })
      });
    });
    page.on('request', request => {
      if (
        request.method() === 'PATCH' &&
        request.url().includes(`/api/workspaces/${workspaceId}/agents/`)
      ) {
        agentSave = request.postDataJSON();
      }
    });
    await page.route('**/api/providers', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: [
            {
              name: 'codex',
              display_name: 'OpenAI Codex (CLI)',
              available: true,
              models: [
                { value: 'gpt-5.4', label: 'gpt-5.4', provider: 'codex', type: 'tool-calling' }
              ]
            },
            {
              name: 'claude_code',
              display_name: 'Claude Code (CLI)',
              available: true,
              models: [
                { value: 'haiku', label: 'Haiku', provider: 'claude_code', type: 'tool-calling' }
              ]
            },
            {
              name: 'ollama',
              display_name: 'Ollama',
              available: true,
              models: [
                { value: 'gemma4:26b', label: 'Gemma', provider: 'ollama', type: 'tool-calling' }
              ]
            }
          ]
        })
      })
    );

    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await page.getByRole('button', { name: 'Ori-assisted REAPER', exact: true }).click();
    await page.getByRole('button', { name: 'Choose compatible agent', exact: true }).click();

    const picker = page.locator('#workspace-detail-agent-model-modal');
    await expect(picker).toBeVisible({ timeout: 15000 });
    await expect(picker.locator('#workspace-detail-agent-model-title')).toContainText(
      'Choose a compatible model for Reaper Producer'
    );
    const offeredProviders = await picker
      .locator('#workspace-detail-agent-model-select option')
      .evaluateAll(options =>
        options.map(option => option.getAttribute('data-provider')).filter(Boolean)
      );
    expect(offeredProviders.length).toBeGreaterThan(0);
    expect(new Set(offeredProviders)).toEqual(new Set(['codex', 'claude_code']));

    await picker.locator('#workspace-detail-agent-model-select').selectOption('gpt-5.4');
    await picker.getByRole('button', { name: 'Save Model', exact: true }).click();
    await expect.poll(() => agentSave?.llm_provider || '').toBe('codex');
    expect(agentSave?.model).toBe('gpt-5.4');
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardStepContent')).toContainText(
      'The selected workspace agent does not have REAPER access'
    );
    const agents = await (await request.get(`/api/workspaces/${workspaceId}/agents`)).json();
    expect(agents.agents[0].provider).toBe('codex');
  });

  test('Ori-assisted shows one authoritative checklist and grants nothing', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Assisted');
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });

    await page.getByRole('button', { name: 'Ori-assisted REAPER', exact: true }).click();

    const content = page.locator('#setupWizardStepContent');
    await expect(content).toContainText('REAPER application', { timeout: 15000 });
    await expect(content).toContainText('Web Remote');
    await expect(content).toContainText('REAPER plugin and skills');
    const pluginLink = content.getByRole('link', {
      name: 'REAPER plugin and skills (opens plugin repository)'
    });
    await expect(pluginLink).toHaveAttribute(
      'href',
      'https://github.com/johnjallday/reaper-plugin'
    );
    await expect(pluginLink).toHaveAttribute('target', '_blank');
    await expect(content).toContainText('Ori REAPER runner');
    await expect(content).toContainText('Compatible workspace agent');
    await expect(content).toContainText('REAPER access');
    await expect(content).toContainText('Project-specific connection test');
    await expect(content).not.toHaveText(LIVE_CLAIMS);
    await expect(content.locator('.reaper-runtime-check.is-attention')).toHaveCount(1);
    await expect(content.locator('.reaper-runtime-actions')).toBeVisible();

    const state = await setupState(request, workspaceId);
    expect(state.state).not.toBe('ready');
    expect(state.steps[0].selected_option).toBe('ori_assisted');
    const workspace = await (await request.get(`/api/workspaces/${workspaceId}`)).json();
    const record = workspace.folder || workspace.workspace || workspace;
    expect(record.runtime_state?.grants || []).toHaveLength(0);

    // Changing mode uses the same surface and does not revoke or mutate tools.
    await page.locator('#setupWizardBack').click();
    await page.getByRole('button', { name: 'File-only', exact: true }).click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Set up local REAPER control', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });
    expect((await setupState(request, workspaceId)).state).toBe('ready');
  });
});
