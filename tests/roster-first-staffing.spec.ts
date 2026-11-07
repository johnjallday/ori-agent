import { test, expect } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
import { installLocalCdn } from './helpers/offline-cdn';
import { prepareFolderCreationRuntime } from './helpers/folder-first-runtime';
import { startRosterStaffingModel } from './helpers/roster-staffing-model';

function staffingFillsForEvidence(roles: Record<string, unknown>[] | undefined) {
  return (roles || [])
    .slice(0, 16)
    .map(role =>
      Object.fromEntries(
        ['role_id', 'mode', 'name', 'recommendation', 'provider', 'model'].map(key => [
          key,
          role[key]
        ])
      )
    );
}

// Explicit sandbox opt-in. Start wt demo with
// LM_STUDIO_BASE_URL=http://127.0.0.1:8944/v1 LM_STUDIO_MODEL=ori-synthetic-model.
// Only the model boundary is scripted; no build/roster/commit endpoint mocks.
test('real builder returns inert renamed-profile advice and unavailable recovery', async ({
  page,
  request
}, testInfo) => {
  test.skip(process.env.ORI_STAFFING_DEMO !== '1', 'Requires an explicitly isolated staffing demo');
  test.setTimeout(120_000);
  const model = await startRosterStaffingModel();
  try {
    const initial = (await (await request.get('/api/personal-assistant')).json())
      .personal_assistant;
    expect(initial.state, 'Use a fresh disposable demo').toBe('needs_hire');
    await request.post('/api/onboarding/skip');
    await prepareFolderCreationRuntime(request);
    const name = `Cedar ${Date.now()}`;
    const created = await request.post('/api/agents', {
      data: {
        name,
        llm_provider: 'lmstudio',
        model: 'ori-synthetic-model',
        description: 'Builds and reviews software projects',
        system_prompt: 'PRIVATE_STAFFING_CANARY'
      }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const agentsBefore = (await (await request.get('/api/agents')).json()).agents
      .map((a: { name: string }) => a.name)
      .sort();
    const workspacesBefore = (await (await request.get('/api/workspaces')).json()).folders
      .map((w: { id: string }) => w.id)
      .sort();
    await installLocalCdn(page);
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    const pane = page.locator('#workspaceBuildPane');
    await expect(pane).toBeVisible();
    const composer = page.locator('#workspaceBuildComposerInput');
    const say = async (text: string) => {
      await expect(composer).toBeEnabled();
      await composer.fill(text);
      const response = page.waitForResponse(r =>
        /\/build-sessions\/[^/]+\/turns$/.test(new URL(r.url()).pathname)
      );
      await composer.press('Enter');
      const result = await response;
      expect(result.ok()).toBeTruthy();
      return (await result.json()).session;
    };
    let session = await say(
      'Prepare a Code Project and suggest a saved software specialist. Do not create it.'
    );
    expect(session.staffing.state).toBe('recommended');
    expect(session.staffing.roles[0].choices[0].name).toBe(name);
    expect(session.staffing.roles[0].choices[0].source_witness).toBeUndefined();
    expect(session.team_patch).toBeUndefined();
    expect(session.draft.role_staffing).toBeUndefined();
    expect(JSON.stringify(session.staffing)).not.toContain('PRIVATE_STAFFING_CANARY');
    await expect(pane).toContainText('nothing has been assigned');
    await expect(
      pane.getByRole('button', { name: `Use ${name} for Development Assistant` })
    ).toBeEnabled();
    await page.screenshot({ path: testInfo.outputPath('01-advisory-pane.png'), fullPage: true });
    const evidence: unknown[] = [{ case: 'renamed', staffing: session.staffing }];
    const calls = model.calls();
    for (let i = 0; i < 3; i++)
      expect((await request.get(`/api/workspaces/build-sessions/${session.id}`)).ok()).toBeTruthy();
    expect(model.calls()).toBe(calls);
    model.mode('needs_choice');
    session = await say('Please compare the saved choices; keep this a draft.');
    expect(session.staffing.state).toBe('needs_choice');
    evidence.push({ case: 'ambiguous', staffing: session.staffing });
    model.mode('unavailable');
    session = await say('Refresh the suggestion without creating anything.');
    expect(session.staffing.state).toBe('unavailable');
    evidence.push({ case: 'unavailable', staffing: session.staffing });
    await expect(pane).toContainText('couldn’t reach my model');
    await expect(pane.getByRole('button', { name: 'Set up manually' })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('01-unavailable-pane.png'), fullPage: true });
    const agentsAfter = (await (await request.get('/api/agents')).json()).agents
      .map((a: { name: string }) => a.name)
      .sort();
    const workspacesAfter = (await (await request.get('/api/workspaces')).json()).folders
      .map((w: { id: string }) => w.id)
      .sort();
    expect(agentsAfter).toEqual(agentsBefore);
    expect(workspacesAfter).toEqual(workspacesBefore);
    const evidencePath = testInfo.outputPath('advisory-endpoint-evidence.json');
    await writeFile(
      evidencePath,
      JSON.stringify(
        {
          evidence,
          agentCount: agentsAfter.length,
          workspaceCount: workspacesAfter.length,
          unchanged: true,
          renderedStaffingControls: true
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
    await testInfo.attach('advisory-endpoint-evidence', {
      path: evidencePath,
      contentType: 'application/json'
    });
  } finally {
    await model.stop();
  }
});

for (const choiceMode of [
  'recommended reuse',
  'manual assignment',
  'explicit creation',
  'no-fit creation'
] as const) {
  test(`ordinary inline ${choiceMode} commits only the reviewed team`, async ({
    page,
    request
  }, testInfo) => {
    test.skip(
      process.env.ORI_STAFFING_DEMO !== '1',
      'Requires an explicitly isolated staffing demo'
    );
    test.setTimeout(90_000);
    page.setDefaultTimeout(15_000);
    const creates = choiceMode.endsWith('creation');
    const model = await startRosterStaffingModel();
    try {
      const initial = (await (await request.get('/api/personal-assistant')).json())
        .personal_assistant;
      if (initial.state === 'needs_hire') {
        await request.post('/api/onboarding/skip');
        await prepareFolderCreationRuntime(request);
      }
      const name = `Aster ${Date.now()}`;
      const purpose = `Builds and reviews software for ordinary reuse fixture ${Date.now()}`;
      model.candidatePurpose(purpose);
      model.workspaceName(`Reviewed Reuse ${Date.now()}`);
      const created = await request.post('/api/agents', {
        data: {
          name,
          llm_provider: 'lmstudio',
          model: 'ori-synthetic-model',
          description: purpose,
          system_prompt: 'Keep these authored instructions unchanged.'
        }
      });
      expect(created.ok(), await created.text()).toBeTruthy();
      const selectedName =
        choiceMode === 'manual assignment'
          ? `Harbor ${Date.now()}`
          : creates
            ? `Workshop ${Date.now()}`
            : name;
      if (choiceMode === 'manual assignment') {
        const other = await request.post('/api/agents', {
          data: {
            name: selectedName,
            llm_provider: 'lmstudio',
            model: 'ori-synthetic-model',
            description: 'Reviews software test plans',
            system_prompt: 'Keep this separate saved profile unchanged.'
          }
        });
        expect(other.ok(), await other.text()).toBeTruthy();
      }
      const profile = async (agentName = name) =>
        await (await request.get(`/api/agents/${encodeURIComponent(agentName)}/detail`)).json();
      if (choiceMode === 'no-fit creation') {
        // Deliberate sandbox fixture preparation, before preservation snapshots:
        // make this corpus wholly unrelated to software, including the synthetic
        // HQ's reusable profiles. Never alter an unrecognized/user-owned corpus.
        const roster = (await (await request.get('/api/agents')).json()).agents;
        for (const agent of roster) {
          if (
            !/^(Aster|Harbor|Workshop|Cedar) \d+$/.test(agent.name) &&
            !['Journal', 'Synthetic Assistant'].includes(agent.name)
          )
            continue;
          const current = await profile(agent.name);
          const updated = await request.patch(`/api/agents/${encodeURIComponent(agent.name)}`, {
            data: {
              expected_version: current.version,
              confirm_shared_edit: true,
              description:
                'Records physical warehouse stocktakes and invoices; no software development remit.'
            }
          });
          expect(updated.ok(), await updated.text()).toBeTruthy();
        }
        model.mode('no_suitable_match');
      }
      let otherWorkspaceID = '';
      if (choiceMode === 'recommended reuse') {
        const previous = await request.post('/api/workspaces', {
          data: {
            name: `Existing assignment ${Date.now()}`,
            create_template_agents: false,
            existing_agent_names: [name],
            entry_agent_name: name
          }
        });
        expect(previous.ok(), await previous.text()).toBeTruthy();
        otherWorkspaceID = (await previous.json()).folder.id;
        const scoped = await request.patch(
          `/api/workspaces/${otherWorkspaceID}/agents/${encodeURIComponent(name)}/instance-settings`,
          { data: { custom_instructions: 'PRIVATE_OTHER_ASSIGNMENT_CANARY' } }
        );
        expect(scoped.ok(), await scoped.text()).toBeTruthy();
      }
      const before = await profile();
      const otherBefore = choiceMode === 'manual assignment' ? await profile(selectedName) : null;
      expect(before.version).toBeTruthy();
      const count = async () => (await (await request.get('/api/agents')).json()).agents.length;
      const profilesBefore = await count();
      const workspaceCount = async () =>
        (await (await request.get('/api/workspaces')).json()).folders.length;
      const workspacesBefore = await workspaceCount();
      let reviewedRequest: Record<string, unknown> = {};
      page.on('request', request => {
        if (
          request.method() === 'POST' &&
          new URL(request.url()).pathname === '/api/workspaces/create-operations'
        ) {
          const material = request.postDataJSON()?.request || {};
          reviewedRequest = Object.fromEntries(
            [
              'name',
              'description',
              'mission',
              'template_id',
              'build_session_id',
              'build_review_version',
              'role_staffing',
              'existing_agent_names',
              'entry_agent_name',
              'create_template_agents',
              'parent_id',
              'color',
              'tags',
              'blueprint_inputs',
              'workspace_preset',
              'workspace_bootstrap',
              'project_connection'
            ].map(key => [key, material[key]])
          );
          reviewedRequest.role_staffing = staffingFillsForEvidence(material.role_staffing);
        }
      });
      await installLocalCdn(page);
      await page.goto('/');
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      const pane = page.locator('#workspaceBuildPane');
      await expect(pane).toBeVisible();
      // Each acceptance starts a fresh draft, not the preceding read-only
      // case's paused build. No endpoint is mocked or selected implicitly.
      await pane.getByRole('button', { name: 'Start over', exact: true }).first().click();
      const composer = page.locator('#workspaceBuildComposerInput');
      await expect(composer).toBeEnabled();
      await composer.fill('Prepare a software workspace and recommend a saved specialist.');
      const turn = page.waitForResponse(r =>
        /\/build-sessions\/[^/]+\/turns$/.test(new URL(r.url()).pathname)
      );
      await composer.press('Enter');
      const response = await turn;
      expect(response.ok()).toBeTruthy();
      const session = (await response.json()).session;
      const use = pane.getByRole('button', { name: `Use ${name} for Development Assistant` });
      if (choiceMode === 'no-fit creation') {
        expect(session.staffing.state).toBe('no_suitable_match');
        await expect(pane).toContainText('No suitable saved match was found.');
        await expect(use).toHaveCount(0);
      } else {
        expect(session.staffing.roles[0].choices[0].name).toBe(name);
        await expect(use).toBeEnabled();
      }
      expect(await count()).toBe(profilesBefore);
      await page.screenshot({ path: testInfo.outputPath('02-inline-choice.png'), fullPage: true });
      if (choiceMode === 'recommended reuse') {
        const selected = page.waitForResponse(
          r =>
            /\/build-sessions\/[^/]+\/draft$/.test(new URL(r.url()).pathname) &&
            r.request().method() === 'PATCH' &&
            r
              .request()
              .postDataJSON()
              ?.draft?.role_staffing?.some(
                (role: { recommendation?: string }) => role.recommendation
              )
        );
        await use.click();
        const saved = await selected;
        expect(saved.ok(), await saved.text()).toBeTruthy();
        const savedSession = (await saved.json()).session;
        expect(savedSession.draft.role_staffing[0]).toMatchObject({
          mode: 'assign',
          name,
          recommendation: session.staffing.roles[0].choices[0].handle
        });
        await expect(pane).toContainText('Recommended · saved agent');
        await pane.getByRole('button', { name: 'Set up manually' }).click();
      } else if (choiceMode === 'manual assignment') {
        await pane
          .getByRole('button', { name: 'Assign an agent to Development Assistant', exact: true })
          .click();
        await expect(page.locator('#wizardStep3')).toBeVisible();
        await page.locator(`[data-existing-agent-add="${selectedName}"]`).click();
        await expect(page.locator('#workspaceRoleRoster')).toContainText(selectedName);
      } else {
        await pane
          .getByRole('button', { name: 'Create an agent for Development Assistant', exact: true })
          .click();
        const form = page.locator('#addAgentModal');
        await expect(form).toBeVisible();
        await form.locator('[data-agent-create-field="name"]').fill(selectedName);
        await form.getByRole('button', { name: 'Add to team', exact: true }).click();
        await expect(form).toBeHidden();
        await expect(page.locator('#workspaceRoleRoster')).toContainText(selectedName);
      }
      expect(await count(), 'selection and the Create form remain draft-only').toBe(profilesBefore);
      expect(await workspaceCount(), 'selection creates no workspace').toBe(workspacesBefore);
      await page.locator('#wizardNextBtn').click();
      const review = page.locator('#workspaceCreationReviewPanel');
      await expect(review).toBeVisible();
      await expect(review).toContainText(selectedName);
      await review.getByText(selectedName).first().scrollIntoViewIfNeeded();
      await page.screenshot({ path: testInfo.outputPath('02-exact-team.png'), fullPage: true });
      await review.getByRole('button', { name: 'Use automatic placement' }).click();
      const confirm = review.getByRole('button', { name: 'Create workspace', exact: true });
      await expect(confirm).toBeEnabled();
      await page.screenshot({ path: testInfo.outputPath('02-exact-review.png'), fullPage: true });
      const operationReview = page.waitForResponse(
        r =>
          new URL(r.url()).pathname === '/api/workspaces/create-operations' &&
          r.request().method() === 'POST'
      );
      const committed = page
        .waitForResponse(
          r => /\/create-operations\/[^/]+\/confirm$/.test(new URL(r.url()).pathname),
          { timeout: 15_000 }
        )
        .catch(() => null);
      await confirm.click();
      const checked = await operationReview;
      if (!checked.ok()) {
        const current = (
          await (await request.get(`/api/workspaces/build-sessions/${session.id}`)).json()
        ).session;
        const contextPath = testInfo.outputPath('refused-review-context.json');
        await writeFile(
          contextPath,
          JSON.stringify(
            {
              reviewedRequest,
              draft: Object.fromEntries(
                Object.keys(reviewedRequest).map(key => [
                  key,
                  key === 'role_staffing'
                    ? staffingFillsForEvidence(current?.draft?.role_staffing)
                    : current?.draft?.[key]
                ])
              ),
              currentVersion: current?.version,
              response: await checked.json()
            },
            null,
            2
          ),
          { mode: 0o600 }
        );
        await testInfo.attach('refused-review-context', {
          path: contextPath,
          contentType: 'application/json'
        });
      }
      expect(checked.ok(), await checked.text()).toBeTruthy();
      const result = await committed;
      expect(result).not.toBeNull();
      expect(result!.status(), await result!.text()).toBe(201);
      const body = await result!.json();
      const workspace = await (await request.get(`/api/workspaces/${body.folder.id}`)).json();
      expect(workspace.agent_instances).toHaveLength(1);
      expect(workspace.agent_instances[0]).toMatchObject({
        name: selectedName,
        role_source: creates ? 'created' : 'assigned'
      });
      expect(await count()).toBe(profilesBefore + (creates ? 1 : 0));
      expect(await workspaceCount()).toBe(workspacesBefore + 1);
      expect((await profile()).version).toBe(before.version);
      if (otherBefore) expect((await profile(selectedName)).version).toBe(otherBefore.version);
      expect(model.calls()).toBe(1);
      if (otherWorkspaceID) {
        const previous = await (await request.get(`/api/workspaces/${otherWorkspaceID}`)).json();
        expect(previous.agent_instances).toHaveLength(1);
        expect(previous.agent_instances[0]).toMatchObject({
          name,
          custom_instructions: 'PRIVATE_OTHER_ASSIGNMENT_CANARY'
        });
        expect(workspace.agent_instances[0].custom_instructions || '').toBe('');
        expect(JSON.stringify(session.staffing)).not.toContain('PRIVATE_OTHER_ASSIGNMENT_CANARY');
      }
      await page.screenshot({ path: testInfo.outputPath('02-reused-result.png'), fullPage: true });
      const evidencePath = testInfo.outputPath('reviewed-reuse-evidence.json');
      await writeFile(
        evidencePath,
        JSON.stringify(
          {
            workspaceID: body.folder.id,
            action: choiceMode,
            selectedName,
            otherAssignmentPreserved: otherWorkspaceID ? true : undefined,
            bindings: workspace.agent_instances.map(
              (member: { name: string; role_id: string; role_source: string }) => ({
                name: member.name,
                roleID: member.role_id,
                source: member.role_source
              })
            ),
            profilesBefore,
            profilesAfter: await count(),
            profileUnchanged: true,
            modelCalls: model.calls()
          },
          null,
          2
        ),
        { mode: 0o600 }
      );
      await testInfo.attach('reviewed-reuse-evidence', {
        path: evidencePath,
        contentType: 'application/json'
      });
    } finally {
      await model.stop();
    }
  });
}
