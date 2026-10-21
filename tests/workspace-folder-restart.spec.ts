import { test, expect } from '@playwright/test';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import { installLocalCdn } from './helpers/offline-cdn';
import {
  prepareFolderCreationRuntime,
  attachFirst,
  currentBuild,
  resources,
  inbox
} from './helpers/folder-first-runtime';

// Two explicit invocations around an owner-controlled restart of the isolated
// demo. This spec never stops a server. State belongs in a disposable temp file.
// ORI_TEST_BUILD_CREATION=1 ORI_TEST_BUILD_RESTART_PHASE=before|after
// ORI_TEST_BUILD_RESTART_STATE=<mktemp path>
const phase = process.env.ORI_TEST_BUILD_RESTART_PHASE;
const stateFile = process.env.ORI_TEST_BUILD_RESTART_STATE;
test.skip(
  process.env.ORI_TEST_BUILD_CREATION !== '1' ||
    !stateFile ||
    !['before', 'after'].includes(phase || ''),
  'Requires explicit isolated restart phases'
);

test('server restart retains the draft but loses selection authority without replay', async ({
  page,
  request
}, testInfo) => {
  const model = await startJanitorBuildModel();
  try {
    await prepareFolderCreationRuntime(request);
    await installLocalCdn(page);
    if (phase === 'before') {
      const root = inbox('Downloads');
      await attachFirst(page, request, root);
      const primary = page.locator('#workspaceBuildPlanReviewSetup');
      await expect(primary).toBeEnabled();
      await expect(primary).toHaveText('Create & scan folder');
      const name = `Kept across restart ${Date.now()}`;
      await page.locator('#workspaceBuildPlanName').fill(name);
      await expect(primary).toBeEnabled();
      await page.getByRole('button', { name: 'Close create workspace', exact: true }).click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      const build = await currentBuild(request);
      expect(build.draft.name).toBe(name);
      expect(model.calls()).toBe(1);
      const before = await resources(request);
      writeFileSync(
        stateFile!,
        JSON.stringify({
          root,
          name,
          id: build.id,
          draft: build.draft,
          workspaces: before.workspaces,
          agents: before.agents
        }),
        { mode: 0o600 }
      );
      return;
    }
    const saved = JSON.parse(readFileSync(stateFile!, 'utf8'));
    const resumed = await currentBuild(request);
    expect(resumed.id).toBe(saved.id);
    expect(resumed.draft).toEqual(saved.draft);
    expect(Date.parse(resumed.folder_expires_at)).toBe(0);
    expect(resumed.folder_path).toBeUndefined();
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.getByRole('button', { name: 'Resume', exact: true }).click();
    await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(saved.name);
    await expect(page.locator('#workspaceBuildFolderProblem')).toContainText(
      'unavailable or expired'
    );
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeDisabled();
    await expect(page.locator('#workspaceBuildFolderRetry')).toBeDisabled();
    expect(model.calls()).toBe(0);
    const after = await resources(request);
    expect(after.workspaces).toEqual(saved.workspaces);
    expect(after.agents).toEqual(saved.agents);
    expect(existsSync(join(saved.root, 'Filed'))).toBe(false);
    await page.screenshot({ path: testInfo.outputPath('folder-restart-expired.png') });
    await page.getByRole('button', { name: 'Change folder', exact: true }).click();
    await page.getByPlaceholder('~/Projects/my-app').fill(saved.root);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
    await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(saved.name);
    expect(model.calls()).toBe(1); // only deliberate reattachment admits a new suggestion
    expect(existsSync(join(saved.root, 'Filed'))).toBe(false);
  } finally {
    await model();
  }
});
