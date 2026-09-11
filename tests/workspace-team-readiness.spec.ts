import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

const SHOTS = 'tasks/screenshots/workspace-team-readiness';
const SAVED_AGENT = 'Downloads Curator';
const RUN = Date.now().toString(36);
const createdWorkspaceIds: string[] = [];
let createdSavedAgent = false;

function cardByLabel(page: Page, label: string) {
  return page.locator('#templatePicker .workspace-template-card').filter({
    has: page.locator('.workspace-template-card-label', { hasText: new RegExp(`^${label}$`) })
  });
}

async function openCreateModal(page: Page) {
  await page.evaluate(() => {
    const element = document.getElementById('addFolderModal');
    // @ts-expect-error bootstrap is a page global
    window.bootstrap.Modal.getOrCreateInstance(element).show();
  });
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(cardByLabel(page, 'Blank')).toBeVisible();
}

async function savedAgentNames(request: APIRequestContext) {
  const response = await request.get('/api/agents/dashboard/list?sort_by=name&order=asc');
  expect(response.ok(), await response.text()).toBe(true);
  const body = await response.json();
  const agents = Array.isArray(body) ? body : body.agents || [];
  return agents.map((agent: { name?: string }) => String(agent.name || ''));
}

test.describe.configure({ mode: 'serial' });

test.beforeAll(async ({ request }) => {
  mkdirSync(SHOTS, { recursive: true });
  if (!(await savedAgentNames(request)).includes(SAVED_AGENT)) {
    const response = await request.post('/api/agents', {
      data: { name: SAVED_AGENT, model: 'gpt-5-nano', role: 'specialist' }
    });
    expect(response.ok(), await response.text()).toBe(true);
    createdSavedAgent = true;
  }
});

test.afterAll(async ({ request }) => {
  for (const id of createdWorkspaceIds) {
    await request.delete(`/api/workspaces/${id}?confirm=true`).catch(() => {});
  }
  if (createdSavedAgent) {
    await request.delete(`/api/agents/${encodeURIComponent(SAVED_AGENT)}`).catch(() => {});
  }
});

test.beforeEach(async ({ page }) => {
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  await page.goto('/workspaces');
});

test('a saved Downloads Curator is suggested, assigned, and persisted without duplication', async ({
  page,
  request
}) => {
  const beforeNames = await savedAgentNames(request);
  await openCreateModal(page);
  await cardByLabel(page, 'Downloads Janitor').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill(`Downloads Readiness ${RUN}`);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();

  const role = page.locator('#workspaceRoleRoster .ws-role-row').first();
  await expect(role.locator('.ws-role-tag')).toHaveText('Missing');
  const suggestion = page.getByRole('button', {
    name: 'Use Downloads Curator for Downloads Curator',
    exact: true
  });
  await expect(suggestion).toBeVisible();
  await expect(page.locator('#workspaceSavedAgentSuggestions')).toContainText(
    "Matches this role's name"
  );
  await page.locator('#addFolderModal .modal-dialog').screenshot({
    path: `${SHOTS}/downloads-curator-suggested.png`
  });

  await suggestion.click();
  await expect(role).toContainText('Your saved agent');
  await expect(role).toContainText(SAVED_AGENT);
  await expect(suggestion).toHaveCount(0);
  await expect(page.locator('#wizardNextBtn')).toBeEnabled();
  await page.locator('#addFolderModal .modal-dialog').screenshot({
    path: `${SHOTS}/downloads-curator-assigned.png`
  });

  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewSummary')).toContainText(SAVED_AGENT);
  const created = page.waitForResponse(
    response => response.url().endsWith('/api/workspaces') && response.request().method() === 'POST'
  );
  await page.locator('#createFolderBtn').click();
  const response = await created;
  expect(response.ok(), await response.text()).toBe(true);
  const requestBody = response.request().postDataJSON();
  expect(requestBody.team_intent).toEqual(expect.objectContaining({ version: 1, mode: 'staffed' }));
  expect(requestBody.role_staffing).toEqual([
    expect.objectContaining({
      role_id: 'downloads-curator',
      mode: 'assign',
      name: SAVED_AGENT
    })
  ]);
  const body = await response.json();
  createdWorkspaceIds.push(body.folder.id);
  expect(body.folder.agent_instances).toEqual([
    expect.objectContaining({
      name: SAVED_AGENT,
      role_id: 'downloads-curator',
      role_source: 'assigned',
      entry_point: true
    })
  ]);
  const afterNames = await savedAgentNames(request);
  expect(afterNames.filter(name => name === SAVED_AGENT)).toHaveLength(1);
  expect(afterNames).toHaveLength(beforeNames.length);
});

test('Blank creates agentless only after the explicit choice', async ({ page }) => {
  await openCreateModal(page);
  await cardByLabel(page, 'Blank').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill(`Agentless Readiness ${RUN}`);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#wizardNextBtn')).toBeDisabled();

  await page.locator('#workspaceBlankAgentlessToggle').check();
  await expect(page.locator('#wizardNextBtn')).toBeEnabled();
  await expect(page.locator('#workspaceTeamIssues')).toContainText(
    'Chat and agent work require adding one later'
  );
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Create without agents');

  const created = page.waitForResponse(
    response => response.url().endsWith('/api/workspaces') && response.request().method() === 'POST'
  );
  await page.locator('#createFolderBtn').click();
  const response = await created;
  expect(response.ok(), await response.text()).toBe(true);
  const requestBody = response.request().postDataJSON();
  expect(requestBody.team_intent).toEqual(
    expect.objectContaining({ version: 1, mode: 'agentless' })
  );
  expect(requestBody.role_staffing).toEqual([]);
  const body = await response.json();
  createdWorkspaceIds.push(body.folder.id);
  expect(body.folder.agent_instances || []).toEqual([]);
});
