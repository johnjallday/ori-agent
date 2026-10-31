import { test, expect } from '@playwright/test';
import {
  mkdirSync,
  realpathSync,
  writeFileSync,
  readFileSync,
  existsSync,
  renameSync
} from 'node:fs';
import { join, sep } from 'node:path';
import { tmpdir } from 'node:os';

// Real host/creator/picker/storage only. No model calls, fabricated grants or
// real user directories. Run with e2e-fresh.sh --sandbox-env ORI_ANCHOR_SANDBOX.
const sandbox = process.env.ORI_ANCHOR_SANDBOX || '';
const evidence = 'tasks/evidence-anchored-folder-workspace';
test.skip(!sandbox, 'requires an isolated e2e-fresh sandbox');
test.setTimeout(120_000);

test('existing-folder creation, scoped rows, duplicate recovery, relink and sidecar-only deletion', async ({
  page,
  request
}) => {
  expect(realpathSync(sandbox).startsWith(realpathSync(tmpdir()) + sep)).toBe(true);
  expect(sandbox).toContain('ori-e2e.');
  const root = join(sandbox, 'client-fixture');
  mkdirSync(root, { mode: 0o750 });
  writeFileSync(join(root, 'source.txt'), 'external source stays untouched', { mode: 0o600 });
  mkdirSync(evidence, { recursive: true });
  expect((await request.post('/api/onboarding/skip')).ok()).toBe(true);
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBe(true);
  const agent = await request.post('/api/agents', {
    data: {
      name: 'Anchor Reader',
      llm_provider: 'lmstudio',
      model: 'ori-synthetic-model',
      system_prompt: 'Read only the explicitly granted folder.'
    }
  });
  expect(agent.ok(), await agent.text()).toBe(true);
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('Anchored client');
  await page.locator('#workspaceFolderModeAnchored').check();
  await page.locator('#workspaceAnchorPath').fill(root);
  await page.locator('#workspaceAnchorPath').blur();
  await expect(page.locator('#workspaceAnchorStatus')).toBeHidden();
  await page.locator('#wizardNextBtn').click();
  await page
    .locator('#workspaceRoleRoster')
    .getByRole('button', { name: 'Assign an agent to Ask Ori', exact: true })
    .click();
  await page.getByRole('button', { name: 'Assign Anchor Reader to Ask Ori', exact: true }).click();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  const id = await page.evaluate(
    () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
  );
  const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
  expect(canonical.shape).toBe('anchored');
  expect(canonical.directory_references).toHaveLength(1);
  expect(canonical.mcp_bindings).toHaveLength(1);
  expect(canonical.mcp_bindings[0]).toMatchObject({
    alias: 'anchor-folder',
    config: { roots: [root] },
    default_side_effect: 'read'
  });
  expect(canonical.mcp_bindings[0].allowed_tools).not.toContain('write_file');
  expect(existsSync(join(canonical.sidecar_path, 'files'))).toBe(false);
  expect(existsSync(join(root, 'workspace.json'))).toBe(false);
  // Acknowledge the durable result through its real owner before starting
  // another creation (a raw navigation leaves that recoverable review active).
  await review.getByRole('button', { name: 'Open workspace', exact: true }).click();
  await expect(page).toHaveURL(/\/workspaces\/anchored-client$/);
  await expect(page.locator('.ws-cmd-anchor-details')).toContainText(
    'Anchored to ~/client-fixture'
  );
  await expect(page.locator('.ws-cmd-anchor-details')).toContainText(
    'Ori’s notes and settings: ~/Ori Workspaces/anchored-client'
  );
  await page.getByText(/^Linked folders$/i).click();
  await expect(page.locator('.ws-cmd-rail-role', { hasText: 'Anchored folder' })).toHaveCount(1);
  await page.getByText(/^Toolbox$/i).click();
  const scope = page.locator('.ws-cmd-binding-scope');
  await expect(scope).toHaveText('anchor-folder · ~/client-fixture · read-only');
  await scope.scrollIntoViewIfNeeded();
  for (const theme of ['light', 'dark']) {
    if ((await page.locator('html').getAttribute('data-bs-theme')) !== theme)
      await page.locator('#darkModeToggle').click();
    await expect(scope).toBeVisible();
    await page.screenshot({ path: `${evidence}/05-scope-${theme}.png`, animations: 'disabled' });
  }

  // Ordinary managed workspaces keep their scaffold and unrestricted grant.
  const managedResponse = await request.post('/api/workspaces', {
    data: { name: 'Managed control', existing_agent_names: ['Anchor Reader'] }
  });
  expect(managedResponse.ok(), await managedResponse.text()).toBe(true);
  const managed = (await managedResponse.json()).folder;
  expect(managed.shape).toBe('managed');
  expect(managed.mcp_bindings[0].alias).toBe('workspace-files');

  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('Duplicate');
  await page.locator('#workspaceFolderModeAnchored').check();
  await page.locator('#workspaceAnchorPath').fill(root);
  await page.locator('#workspaceAnchorPath').blur();
  await expect(page.locator('#workspaceAnchorStatus')).toContainText(
    'already belongs to the workspace "Anchored client"'
  );
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceAnchorFields')).toBeVisible();
  await expect(page.locator('#workspaceAnchorOpenExisting')).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#workspaceAnchorOpenExisting').scrollIntoViewIfNeeded();
  await expect(page.locator('#workspaceAnchorOpenExisting')).toBeInViewport();
  await page.screenshot({ path: `${evidence}/05-duplicate-narrow.png` });
  await page.locator('#workspaceAnchorOpenExisting').click();
  await expect(page).toHaveURL(/\/workspaces\/anchored-client$/);
  await page.setViewportSize({ width: 1440, height: 1000 });

  // A moved folder is not recreated; explicit typed relink keeps authority.
  const moved = root + '-moved';
  renameSync(root, moved);
  await page.reload();
  await expect(page.locator('.ws-cmd-anchor-details')).toContainText('Folder not found');
  await page
    .locator('.ws-cmd-anchor-details')
    .getByRole('button', { name: 'Relink', exact: true })
    .click();
  const picker = page.getByRole('dialog', { name: 'Relink anchored folder' });
  await picker.getByLabel('or paste a folder path', { exact: true }).fill(moved);
  await picker.getByRole('button', { name: 'Use this path', exact: true }).click();
  await expect(picker).toHaveCount(0);
  await expect(page.locator('.ws-cmd-anchor-details')).not.toContainText('Folder not found');
  const relinked = await (await request.get(`/api/workspaces/${id}`)).json();
  expect(relinked.anchor.path).toBe(moved);
  expect(relinked.anchor.directory_reference_id).toBe(canonical.anchor.directory_reference_id);
  expect(relinked.primary_directory_id).toBe(canonical.anchor.directory_reference_id);
  expect(relinked.mcp_bindings).toHaveLength(1);
  expect(relinked.mcp_bindings[0].id).toBe(canonical.mcp_bindings[0].id);
  expect(relinked.mcp_bindings[0].allowed_tools).toEqual(canonical.mcp_bindings[0].allowed_tools);
  expect(existsSync(root)).toBe(false);

  await page.goto('/?view=tree');
  const row = page.locator(`#cockpitTreeNav [data-tree-row="${id}"]`);
  await row.click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Delete', exact: true }).click();
  const dialog = page.locator('dialog.ws-delete-dialog');
  await expect(dialog).toContainText("Ori's notes, settings and agents");
  await expect(dialog).toContainText('Your folder ~/client-fixture-moved is not touched');
  await page.screenshot({ path: `${evidence}/05-delete-disclosure.png` });
  await dialog.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(row).toHaveCount(0);
  expect(existsSync(canonical.sidecar_path)).toBe(false);
  expect(readFileSync(join(moved, 'source.txt'), 'utf8')).toBe('external source stays untouched');
  expect(errors).toEqual([]);
});
