import { expect, test } from '@playwright/test';
import {
  createWorkspace,
  removeOwnedWorkspace,
  preparePage,
  guardReadTour,
  capture
} from './helpers/ui-consistency';
import {
  createOwnedAgent,
  ownedAgentDetail,
  recordOwnedAgent,
  removeOwnedAgent,
  type OwnedAgent
} from './helpers/ui-consistency-owned-agent';

let owned: OwnedAgent | null = null;
let workspace: Awaited<ReturnType<typeof createWorkspace>> | null = null;
test.afterEach(async ({ request }, info) => {
  info.setTimeout(30_000);
  if (owned) {
    const detached = await request.put(`/api/agents/${owned.name}/workspaces`, {
      data: { workspace_ids: [] }
    });
    expect(detached.ok()).toBe(true);
    const current = await ownedAgentDetail(request, owned);
    for (const key of [
      'name',
      'role',
      'model',
      'provider',
      'temperature',
      'system_prompt',
      'metadata',
      'appearance'
    ]) {
      expect(current[key], `membership/context inspection must preserve ${key}`).toEqual(
        owned.expected[key]
      );
    }
    await recordOwnedAgent(request, owned);
    await removeOwnedAgent(request, owned);
    owned = null;
  }
  if (workspace) {
    await removeOwnedWorkspace(request, workspace);
    workspace = null;
  }
});

for (const theme of ['light', 'dark'] as const) {
  test(`Real owned library/workspace context stays read-only and preserves a library draft: ${theme}`, async ({
    page,
    request
  }, info) => {
    owned = await createOwnedAgent(request);
    workspace = await createWorkspace(request);
    const assigned = await request.put(`/api/agents/${owned.name}/workspaces`, {
      data: { workspace_ids: [workspace.id] }
    });
    expect(assigned.ok(), await assigned.text()).toBe(true);
    const projection = await request.get(
      `/api/agents/${owned.name}/settings-ownership?studio_id=${workspace.id}`
    );
    expect(projection.ok()).toBe(true);
    const context = await projection.json();
    expect(context.context.workspace.id).toBe(workspace.id);
    expect(context.definition.state).toBe('ok');
    await preparePage(page, theme, 390);
    const unexpected = await guardReadTour(page); // All browser writes forbidden.
    await page.goto(`/agents/${owned.name}?tab=ownership`);
    const picker = page.locator('#settingsOwnershipContext');
    await expect(picker).toBeEnabled();
    await expect(picker).toHaveValue('library');
    await page.locator('#editPromptBtn').click();
    const draft = `${owned.marker}: unsent library draft`;
    await page.locator('#editSystemPrompt').fill(draft);
    await picker.selectOption(`workspace:${workspace.id}`);
    await expect(page.locator('.settings-ownership-heading')).toContainText(workspace.name);
    await expect(page.locator('[data-ownership-status]')).toHaveAttribute('data-state', 'ready');
    await expect(page.locator('#settingsOwnershipSection')).toContainText('Read only');
    await expect(page.locator('#editSystemPrompt')).toHaveValue(draft);
    await expect(page.locator('#agentName')).toHaveText(owned.name);
    await picker.selectOption('library');
    await expect(page.locator('.settings-ownership-heading')).toHaveText('Library profile');
    await expect(page.locator('[data-ownership-status]')).toHaveAttribute('data-state', 'ready');
    await expect(page.locator('#editSystemPrompt')).toHaveValue(draft);
    const persisted = await request.get(`/api/agents/${owned.name}/detail`);
    expect((await persisted.json()).system_prompt).toBe(owned.expected.system_prompt);
    await capture(
      page,
      info,
      'agent-detail-owned-context',
      {
        persistence:
          'Real UUID definition and workspace membership, real read-only context switch; prompt draft unsaved; no execution'
      },
      { fullPage: false }
    );
    expect(unexpected).toEqual([]);
  });
}
