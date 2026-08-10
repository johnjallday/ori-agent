import { test, expect, Page } from '@playwright/test';

/**
 * E2E for the "Find tools" relocation:
 *   - workspace creation is non-blocking (no marketplace round-trip),
 *   - the workspace page has the Find tools card and NOT the old setup-review
 *     surface,
 *   - Find tools searches and renders add-ons only (no Agents) with honest copy.
 *
 * Run against a running server:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8765 npx playwright test tests/find-tools.spec.ts
 * (Not part of CI — CI runs `npm run test:modules`.)
 */

function uniqueName(base: string): string {
  return `${base} ${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}

async function createWorkspace(page: Page, name: string, description: string): Promise<string> {
  const res = await page.request.post('/api/workspaces', {
    data: { name: uniqueName(name), description, workspace_preset: 'general' }
  });
  const body = await res.json().catch(() => ({}) as Record<string, unknown>);
  const id = (body as { folder?: { id?: string } }).folder?.id;
  if (!res.ok() || !id) {
    throw new Error(
      `create workspace failed (${res.status()}): ${JSON.stringify(body).slice(0, 300)}`
    );
  }
  return id;
}

// Concrete workspaces are created without an entry agent (by design), so the
// detail page would otherwise show a mandatory "Create an entry agent?" prompt.
async function suppressEntryAgentPrompt(page: Page, workspaceId: string) {
  await page.addInitScript(id => {
    window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
  }, workspaceId);
}

async function gotoWorkspaceCommand(page: Page, workspaceId: string) {
  await suppressEntryAgentPrompt(page, workspaceId);
  await page.goto(`/workspaces/${workspaceId}`);
  await expect(page.locator('#workspaceCommandView')).toBeVisible();
}

async function openFindToolsSurface(page: Page) {
  const toolsStat = page.getByRole('button', {
    name: /Open tools: MCP, skills, plugins, and find tools/i
  });
  await expect(toolsStat).toBeVisible();
  await toolsStat.click();

  const modal = page.locator('#workspaceCommandView .ws-cmd-modal:not([hidden])');
  await expect(modal).toBeVisible();
  await modal.getByRole('tab', { name: 'Find Tools' }).click();

  await expect(page.locator('#workspace-detail-tools-card')).toBeVisible();
  await expect(page.locator('#workspace-tools-find-btn')).toBeVisible();
  return modal;
}

test.beforeEach(async ({ page }) => {
  // Skip first-run onboarding server-side so its modal can't intercept clicks.
  await page.request.post('/api/onboarding/skip').catch(() => {});
});

test('creating a workspace is non-blocking and makes no marketplace calls', async ({ page }) => {
  const marketplaceHits: string[] = [];
  page.on('request', req => {
    if (/\/api\/(skills\/marketplace|mcp\/search)/.test(req.url())) marketplaceHits.push(req.url());
  });

  await page.goto('/workspaces');
  await page.evaluate(() => {
    const el = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(el).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();

  // The create modal is single-action and has no review pane / Back button.
  await expect(page.locator('#createFolderBtn')).toHaveText('Create Workspace');
  await expect(page.locator('#folderBootstrapReviewCard')).toHaveCount(0);
  await expect(page.locator('#folderReviewBackBtn')).toHaveCount(0);

  await page.fill('#folderNameInput', uniqueName('E2E NonBlocking'));
  await page.fill('#folderDescriptionInput', 'Plans and tracks trips with flights and hotels.');
  await page.click('#createFolderBtn');

  // Creating navigates straight to the new workspace.
  await page.waitForURL(/\/workspaces\/[0-9a-f-]{8,}/, { timeout: 15000 });
  expect(marketplaceHits).toEqual([]);
});

test('workspace page shows Find tools and not the old setup-review surface', async ({ page }) => {
  const wid = await createWorkspace(page, 'E2E ToolsCard', 'Plans and tracks trips.');
  await gotoWorkspaceCommand(page, wid);

  // New surface present.
  await openFindToolsSurface(page);
  await expect(page.locator('#workspace-tools-find-btn')).toHaveText('Find tools');

  // Old Settings -> Intent setup-review surface removed.
  await expect(page.locator('#workspace-detail-intent-review')).toHaveCount(0);
  await expect(page.locator('#workspace-detail-intent-review-panel')).toHaveCount(0);
  await expect(page.locator('#workspace-detail-intent-apply')).toHaveCount(0);

  // Intent editing (Save Intent) is unchanged.
  await expect(page.locator('#workspace-detail-intent-save')).toHaveCount(1);
});

test('Find tools searches and renders add-ons only with honest copy', async ({ page }) => {
  // Stub the slow marketplace/registry searches so the panel resolves fast and
  // deterministically (we assert structure + copy, not specific results).
  await page.route('**/api/skills/marketplace/search**', r =>
    r.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ skills: [], results: [] })
    })
  );
  await page.route('**/api/mcp/search**', r =>
    r.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ servers: [], results: [] })
    })
  );

  const wid = await createWorkspace(page, 'E2E Search', 'Plans and tracks trips.');
  await gotoWorkspaceCommand(page, wid);
  await openFindToolsSurface(page);

  await page.locator('#workspace-tools-find-btn').click();
  const host = page.locator('#workspace-tools-panel-host');
  await expect(host.locator('[data-tools-summary]')).toBeVisible({ timeout: 15000 });

  // Add-ons only — the three add-on sections, never an Agents section.
  const labels = await host.locator('.workspace-setup-label').allTextContents();
  expect(labels).toContain('Workspace MCPs');
  expect(labels).toContain('Workspace Skills');
  expect(labels).toContain('Workspace Plugins');
  expect(labels).not.toContain('Agents');

  // Honest copy — no AI-review framing anywhere in the panel.
  const text = (await host.textContent()) || '';
  expect(text).not.toMatch(/Ori (will|reviewed|can)/);
  expect(text).not.toMatch(/Review Setup/);
  await expect(host.locator('[data-tools-apply]')).toHaveText('Add selected');
});
