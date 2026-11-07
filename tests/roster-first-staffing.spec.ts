import { test, expect } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
import { installLocalCdn } from './helpers/offline-cdn';
import { prepareFolderCreationRuntime } from './helpers/folder-first-runtime';
import { startRosterStaffingModel } from './helpers/roster-staffing-model';

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
          renderedStaffingControls: false
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
