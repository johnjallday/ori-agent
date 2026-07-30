import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Reaper Song setup, in a browser (PRD FR105–FR116, task 6.13).
 *
 * Run with:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test tests/reaper-song-wizard.spec.ts
 *
 * against an isolated smoke server (`wt demo 8931`, or the HOME/ORI_DATA_DIR
 * recipe in CLAUDE.md).
 *
 * This blueprint used to refuse to create a workspace at all until the REAPER
 * plugin was installed and enabled — a wall in front of a user who may only
 * have wanted Ori to edit one project file. The wizard replaces it with a
 * question, and the two answers are what this spec walks:
 *
 *   file-only    a complete, finished answer that installs nothing
 *   Ori-assisted the prerequisites, listed, each granted deliberately
 *
 * Nothing here installs a plugin or grants native access: the assisted path is
 * exercised up to its first blocker and then stops, which is exactly where a
 * machine without the plugin should stop. No REAPER is running during this
 * suite, so any copy claiming a live session would be a lie the test would
 * catch.
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
  // The point of FR106/107: this succeeds on a machine with no REAPER plugin.
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

// Claims no copy in this flow may make. Deliberately affirmative phrasings:
// the truthful summaries say REAPER is running was *not* checked, and a naive
// "reaper is running" pattern would flag the honest sentence.
const LIVE_CLAIMS =
  /connected to reaper|web remote is ready|reaper session is open|is controlling reaper/i;

test.describe.configure({ mode: 'serial' });

test.describe('Reaper Song setup wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/onboarding/skip').catch(() => {});
  });

  test('file-only completes setup with no plugin installed', async ({ page, request }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper File Only');
    expect((await setupState(request, workspaceId)).state).toBe('not_started');

    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await expect(page.locator('#setupWizardStepTitle')).toHaveText(
      'Choose how Ori works with REAPER'
    );

    // Both answers are offered, each stating its consequence where it is chosen.
    const content = page.locator('#setupWizardStepContent');
    await expect(content).toContainText('No plugin, no permissions');
    await expect(content).toContainText('Ori-assisted REAPER');
    // Nothing is chosen yet, so the footer has nothing to advance.
    await expect(page.locator('#setupWizardPrimary')).toBeDisabled();

    await page.getByRole('button', { name: 'File only', exact: true }).click();

    // The check behind the choice passes immediately: file-only has no
    // prerequisites, which is the entire reason it is offered.
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Check what Ori has', {
      timeout: 15000
    });
    await expect(content).toContainText('has not checked whether REAPER is running');
    await expect(content).not.toHaveText(LIVE_CLAIMS);

    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Reaper Song is ready', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });

    // Server-side: finished, with the answer recorded rather than inferred.
    const ready = await setupState(request, workspaceId);
    expect(ready.state).toBe('ready');
    expect(ready.steps[0].selected_option).toBe('file_only');

    // And nothing was installed or granted to get there.
    const readiness = await (
      await request.get(`/api/workspaces/${workspaceId}/reaper-setup`)
    ).json();
    expect(readiness.plugin_installed).toBeFalsy();
    expect(readiness.workspace_native_cli_enabled).toBeFalsy();
    expect(readiness.agent_native_cli_enabled).toBeFalsy();

    // The wizard's own help task closes itself; the session work does not,
    // because nobody has done it (FR114/115).
    const workspace = await (await request.get(`/api/workspaces/${workspaceId}`)).json();
    const tasks = (workspace.folder || workspace.workspace || workspace).tasks || [];
    const help = tasks.find((task: { description?: string }) =>
      (task.description || '').includes('REAPER setup choices')
    );
    const adjust = tasks.find((task: { description?: string }) =>
      (task.description || '').includes('Adjust the new REAPER session')
    );
    expect(help?.status, 'the setup help task is completed by the wizard').toBe('completed');
    expect(adjust?.status, 'session work is not completed by finishing setup').not.toBe(
      'completed'
    );
  });

  test('a reloaded file-only workspace stays finished and stops asking', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Persisted');
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });
    await page.getByRole('button', { name: 'File only', exact: true }).click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Check what Ori has', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });

    await page.goto(`/workspaces/${workspaceId}`);
    // A finished workspace still offers a way back in, but it never re-opens
    // itself and it never asks again.
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Ready', { timeout: 15000 });
    await expect(page.locator('#setupWizardBanner')).toContainText('Setup is complete.');
    await expect(dialog(page)).toBeHidden();
    expect((await setupState(request, workspaceId)).state).toBe('ready');
  });

  test('Ori-assisted names its prerequisites and grants none of them', async ({
    page,
    request
  }) => {
    const workspaceId = await createReaperWorkspace(request, 'Reaper Assisted');
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 20000 });

    await page.getByRole('button', { name: 'Ori-assisted REAPER', exact: true }).click();

    // The choice is recorded, and the step it gates now reports what is missing.
    const content = page.locator('#setupWizardStepContent');
    await expect(content).toContainText('Plugin: Not installed', { timeout: 15000 });
    await expect(content).toContainText('Native CLI access');
    // The one line a user is most likely to over-read.
    await expect(content).toContainText('Live REAPER session: Not checked here');
    await expect(content).not.toHaveText(LIVE_CLAIMS);

    // The repair is offered, not performed.
    await expect(page.getByRole('button', { name: 'Install the plugin' })).toBeVisible();

    // Setup is honestly unfinished, and nothing was installed or granted by
    // choosing the mode.
    const state = await setupState(request, workspaceId);
    expect(state.state).not.toBe('ready');
    expect(state.steps[0].selected_option).toBe('ori_assisted');
    const readiness = await (
      await request.get(`/api/workspaces/${workspaceId}/reaper-setup`)
    ).json();
    expect(readiness.plugin_installed).toBeFalsy();
    expect(readiness.plugin_attached).toBeFalsy();
    expect(readiness.workspace_native_cli_enabled).toBeFalsy();

    // Changing one's mind is supported, and it finishes setup.
    await page.locator('#setupWizardBack').click();
    await page.getByRole('button', { name: 'File only', exact: true }).click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Check what Ori has', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();
    await page.locator('#setupWizardPrimary').click();
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });
    expect((await setupState(request, workspaceId)).state).toBe('ready');
  });
});
