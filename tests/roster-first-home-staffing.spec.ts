import { test, expect, type Page } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
import { installLocalCdn } from './helpers/offline-cdn';
import { prepareFolderCreationRuntime } from './helpers/folder-first-runtime';
import { startRosterStaffingModel } from './helpers/roster-staffing-model';

// Run only against music-home-demo's exact clean export in disposable HOME/data,
// with LM_STUDIO_BASE_URL=http://127.0.0.1:8944/v1. The sole scripted boundary is
// the model; the Home catalog, review, removal, source guards and bindings are real.
test('roster-first Music Home reuses its retained renamed manager over three removal cycles', async ({
  page,
  request
}, testInfo) => {
  test.skip(
    process.env.ORI_STAFFING_DEMO !== '1' || process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1',
    'requires isolated exact Music candidate'
  );
  test.setTimeout(180_000);
  page.setDefaultTimeout(15_000);
  const model = await startRosterStaffingModel();
  model.candidatePurpose('music-project-management');
  const managerName = `Aster ${Date.now()}`;
  const profileNames = async () =>
    ((await (await request.get('/api/agents')).json()).agents as { name: string }[])
      .map(agent => agent.name)
      .sort();
  const profileVersion = async () =>
    (await (await request.get(`/api/agents/${encodeURIComponent(managerName)}/detail`)).json())
      .version;
  let roleWrites = 0;
  page.on('request', outgoing => {
    if (outgoing.method() === 'PUT' && /\/roles\//.test(new URL(outgoing.url()).pathname))
      roleWrites++;
  });
  async function openTeam(name: string) {
    await page.goto('/');
    await page.getByRole('button', { name: 'Tree', exact: true }).click();
    await page.locator('[data-tree-new-group]').click();
    const creator = page.locator('#addFolderModal');
    await creator
      .locator('.workspace-group-template-option', { hasText: 'Music Production Home' })
      .locator('input')
      .check();
    await creator.locator('#wizardNextBtn').click();
    await creator.locator('#folderNameInput').fill(name);
    await creator.locator('#wizardNextBtn').click();
    await expect(creator.locator('#wizardStep3')).toBeVisible();
    return creator;
  }
  async function confirm(page: Page) {
    const creator = page.locator('#addFolderModal');
    await creator.locator('#wizardNextBtn').click();
    const review = creator.locator('#workspaceCreationReviewPanel');
    await expect(review).toContainText(managerName);
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await expect(review.locator('[data-placement-action="confirm"]')).toBeEnabled();
    await review.getByText(managerName).first().scrollIntoViewIfNeeded();
    await page.screenshot({
      path: testInfo.outputPath(`03-exact-review-${model.calls()}.png`),
      fullPage: true
    });
    const response = page.waitForResponse(
      reply =>
        reply.request().method() === 'POST' &&
        new URL(reply.url()).pathname === '/api/workspaces/group-templates/commit'
    );
    await review.locator('[data-placement-action="confirm"]').click();
    const committed = await response;
    expect(committed.ok(), await committed.text()).toBeTruthy();
    const result = (await committed.json()).group_template;
    expect(result.home_staffing.state).toBe('staffed');
    expect(result.home_staffing.roles).toHaveLength(1);
    expect(result.home_staffing.roles[0].name).toBe(managerName);
    await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
    await review.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(creator).toBeHidden();
    return result;
  }
  try {
    await request.post('/api/onboarding/skip');
    await prepareFolderCreationRuntime(request);
    const catalog = (await (await request.get('/api/workspaces/group-templates')).json())
      .group_templates;
    expect(
      catalog.some((entry: { name: string }) => entry.name === 'Music Production Home')
    ).toBeTruthy();
    for (const name of ['Portfolio Manager 2', 'Portfolio Manager 3']) {
      const created = await request.post('/api/agents', {
        data: {
          name,
          llm_provider: 'lmstudio',
          model: 'ori-synthetic-model',
          description: 'Records warehouse inventories and invoices.'
        }
      });
      expect(created.ok(), await created.text()).toBeTruthy();
    }
    await installLocalCdn(page);
    let creator = await openTeam(`Music first ${Date.now()}`);
    const primary = creator.locator('[data-role-id="portfolio_manager"]');
    await expect(primary).toContainText('Missing');
    await primary
      .getByRole('button', { name: 'Create an agent for Music Portfolio Manager', exact: true })
      .click();
    const form = page.locator('#addAgentModal');
    await expect(form).toBeVisible();
    await form.locator('[data-agent-create-field="name"]').fill(managerName);
    await form.getByRole('button', { name: 'Use for this role', exact: true }).click();
    await expect(form).toBeHidden();
    let result = await confirm(page);
    expect(result.home_staffing.roles[0].outcome).toBe('created');
    const retainedNames = await profileNames();
    const version = await profileVersion();
    const cycles = [];
    for (let cycle = 1; cycle <= 3; cycle++) {
      const oldID = result.home_workspace_id;
      const override = await request.patch(
        `/api/workspaces/${oldID}/agents/${encodeURIComponent(managerName)}/instance-settings`,
        { data: { custom_instructions: 'OLD_HOME_PRIVATE_CONTEXT_CANARY' } }
      );
      expect(override.ok(), await override.text()).toBeTruthy();
      const home = await (await request.get(`/api/workspaces/${oldID}`)).json();
      const removal = await request.post(
        `/api/workspaces/${oldID}/assistant-program/remove-home/review`,
        { data: { state_revision: home.assistant_program.state.state_revision } }
      );
      expect(removal.ok(), await removal.text()).toBeTruthy();
      const removed = await request.post(
        `/api/workspaces/${oldID}/assistant-program/remove-home/commit`,
        { data: { token: (await removal.json()).token } }
      );
      expect(removed.ok(), await removed.text()).toBeTruthy();
      expect(await profileNames()).toEqual(retainedNames);
      expect(await profileVersion()).toBe(version);
      creator = await openTeam(`Music return ${cycle} ${Date.now()}`);
      await creator.getByRole('button', { name: 'Recommend saved agents', exact: true }).click();
      const use = creator.getByRole('button', {
        name: `Use ${managerName} for Music Portfolio Manager`,
        exact: true
      });
      await expect(use).toBeEnabled();
      expect(await profileNames()).toEqual(retainedNames);
      await use.scrollIntoViewIfNeeded();
      await page.screenshot({
        path: testInfo.outputPath(`03-retained-choice-${cycle}.png`),
        fullPage: true
      });
      await use.click();
      result = await confirm(page);
      expect(result.home_staffing.roles[0].action).toBe('assign');
      expect(result.home_staffing.roles[0].outcome).toBe('assigned');
      expect(result.home_workspace_id).not.toBe(oldID);
      expect(await profileNames()).toEqual(retainedNames);
      expect(await profileVersion()).toBe(version);
      const current = await (
        await request.get(`/api/workspaces/${result.home_workspace_id}`)
      ).json();
      expect(current.agent_instances).toHaveLength(1);
      expect(current.agent_instances[0].name).toBe(managerName);
      expect(current.tasks || []).toHaveLength(0);
      expect(current.scheduled_tasks || []).toHaveLength(0);
      expect(current.directory_references || []).toHaveLength(0);
      expect(current.mcp_bindings || []).toHaveLength(0);
      expect(current.assistant_program.state.linked_project_ids || []).toHaveLength(0);
      expect(current.assistant_program.state.project_staffing_consent).toBeFalsy();
      expect(current.agent_instances[0].custom_instructions || '').toBe('');
      cycles.push({
        cycle,
        workspaceID: current.id,
        manager: managerName,
        profileCount: retainedNames.length,
        action: 'assign',
        profileUnchanged: true,
        priorInstanceContextAbsent: true
      });
    }
    expect(roleWrites).toBe(0);
    expect(model.calls()).toBe(3);
    await writeFile(
      testInfo.outputPath('03-home-recreation-evidence.json'),
      JSON.stringify(
        {
          providerBoundary: 'scripted',
          pluginInput: 'exact local Music export; not reviewed-release acceptance',
          cycles,
          recommendationCalls: model.calls(),
          rolePUTs: roleWrites,
          numberedProfilesPreserved: true
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
  } finally {
    await model.stop();
  }
});
