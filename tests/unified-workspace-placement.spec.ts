import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';

// Real server acceptance. No successful create/receipt/layout response is
// fabricated. Run only against the isolated branch demo via scripts/e2e.sh.
async function resources(request: APIRequestContext) {
  const workspaces = await (await request.get('/api/workspaces')).json();
  const agents = await (await request.get('/api/agents')).json();
  return {
    workspaces: workspaces.folders.map((workspace: { id: string }) => workspace.id).sort(),
    agents: agents.agents.map((agent: { name: string }) => agent.name).sort()
  };
}

async function reviewBlank(request: APIRequestContext) {
  const id = randomUUID();
  const response = await request.post('/api/workspaces/create-operations', {
    data: {
      id,
      request: {
        name: `Reviewed notebook ${id.slice(0, 8)}`,
        blank: true,
        create_template_agents: false,
        existing_agent_names: []
      }
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json();
  expect(body.operation.state).toBe('reviewed');
  expect(body.result).toBeUndefined();
  expect(body.operation.workspace_id).toBeUndefined();
  return { id, digest: body.operation.review_digest as string };
}

async function openBlankReview(page: Page, name: string, recovery = false) {
  await page.request.post('/api/onboarding/skip');
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toBeVisible();
  if (!recovery)
    await expect(
      review.getByRole('button', { name: 'Use automatic placement', exact: true })
    ).toBeVisible();
  return review;
}

test('Blank full review closes for placement and one canvas Enter creates the reviewed workspace', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const name = `Placed notebook ${randomUUID().slice(0, 8)}`;
  const review = await openBlankReview(page, name);
  await review
    .getByRole('textbox', { name: 'Goal', exact: true })
    .fill('Keep this exact reviewed purpose.');
  await review.getByRole('textbox', { name: 'Goal', exact: true }).press('Tab');
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-review.png',
    fullPage: true
  });
  expect(await resources(request)).toEqual(before);
  await review
    .getByRole('button', { name: 'Place & create on map', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-review-actions.png',
    fullPage: true
  });
  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  expect(await resources(request)).toEqual(before);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-map.png',
    fullPage: true
  });
  await page.locator('[data-ws-map-viewport]').focus();
  for (
    let index = 0;
    index < 60 && /is-invalid/.test((await preview.getAttribute('class')) || '');
    index++
  )
    await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(page.locator('#cockpitContextModal')).toBeHidden();
  const operation = await page.evaluate(() => {
    const review = (window as any).sessionManager.workspaceCreatorContext.ordinaryReview;
    return { reference: review.reference, candidate: review.candidate };
  });
  const observed = await (
    await request.get(`/api/workspaces/create-operations/${operation.reference.id}`)
  ).json();
  const workspace = observed.result.folder;
  expect(workspace.name).toBe(name);
  const canonical = await (await request.get(`/api/workspaces/${workspace.id}`)).json();
  expect(canonical.mission_state.mission).toBe('Keep this exact reviewed purpose.');
  expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, workspace.id].sort(),
    agents: before.agents
  });
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[workspace.id]).toEqual(operation.candidate);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-result.png',
    fullPage: true
  });
});

test('Blank automatic confirmation works without a mounted map and cancellation is inert', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const review = await openBlankReview(page, `Automatic notebook ${randomUUID().slice(0, 8)}`);
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect(await resources(request)).toEqual(before);
  await page.locator('[data-cockpit-view="tree"]').click();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(`Tree notebook ${randomUUID().slice(0, 8)}`);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  await expect(
    review.getByRole('button', { name: 'Place & create on map', exact: true })
  ).toBeDisabled();
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(await resources(request)).toEqual(before);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-automatic-review.png',
    fullPage: true
  });
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(page.locator('#createFolderBtn')).toBeHidden();
  await expect(page.locator('#workspaceCreatorKindChoice')).toBeHidden();
  const after = await resources(request);
  expect(after.workspaces).toHaveLength(before.workspaces.length + 1);
  expect(after.agents).toEqual(before.agents);
  await page.evaluate(() => (window as any).sessionManager.createFolder());
  expect(await resources(request)).toEqual(after);
  await page.screenshot({
    path: 'tasks/evidence-unified-workspace-placement/group-1/blank-automatic-result.png',
    fullPage: true
  });
});

test('Blank Escape and automatic fallback preserve edits but clear a manual candidate without creation', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const name = `Returned notebook ${randomUUID().slice(0, 8)}`;
  const review = await openBlankReview(page, name);
  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  await expect(review.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    name
  );
  expect(await resources(request)).toEqual(before);
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceMapPlacement)
  ).toBeNull();
  expect(await resources(request)).toEqual(before);
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  const id = await page.evaluate(
    () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id
  );
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[id]).toBeUndefined();
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, id].sort(),
    agents: before.agents
  });
});

test('Blank UI observes lost confirmation delivery and reloads the original result instead of resubmitting', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const review = await openBlankReview(page, `Recovered notebook ${randomUUID().slice(0, 8)}`);
  let id = '',
    posts = 0;
  await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
    posts++;
    const response = await route.fetch();
    expect(response.status()).toBe(201);
    id = (await response.json()).folder.id;
    await route.abort('failed');
  });
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await openBlankReview(page, `Never created ${randomUUID().slice(0, 8)}`, true);
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  expect(posts).toBe(1);
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, id].sort(),
    agents: before.agents
  });
});

test('ordinary Blank review is inert and repeated confirmation observes one canonical resource', async ({
  request
}) => {
  const before = await resources(request);
  const operation = await reviewBlank(request);
  expect(await resources(request)).toEqual(before);
  const endpoint = `/api/workspaces/create-operations/${operation.id}`;
  for (let index = 0; index < 2; index++) {
    const response = await request.get(endpoint);
    expect(response.ok(), await response.text()).toBeTruthy();
    expect((await response.json()).operation.state).toBe('reviewed');
  }
  expect(await resources(request)).toEqual(before);
  const confirms = await Promise.all(
    Array.from({ length: 4 }, () =>
      request.post(`${endpoint}/confirm`, { data: { review_digest: operation.digest } })
    )
  );
  for (const response of confirms) expect([201, 202]).toContain(response.status());
  const observed = await (await request.get(endpoint)).json();
  expect(observed.operation.state).toBe('complete');
  const workspace = observed.result.folder;
  expect(workspace.shared_data.workspace_creation_operation_id).toBe(operation.id);
  expect(workspace.shared_data.workspace_creation_operation_digest).toBe(operation.digest);
  const canonical = await (await request.get(`/api/workspaces/${workspace.id}`)).json();
  expect(canonical.id).toBe(workspace.id);
  expect(canonical.shared_data.workspace_creation_operation_id).toBe(operation.id);
  const after = await resources(request);
  expect(after.workspaces).toEqual([...before.workspaces, workspace.id].sort());
  expect(after.agents).toEqual(before.agents);
  for (let index = 0; index < 2; index++) {
    const response = await request.post(`${endpoint}/confirm`, {
      data: { review_digest: operation.digest }
    });
    expect((await response.json()).folder.id).toBe(workspace.id);
  }
  expect(await resources(request)).toEqual(after);
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  expect(layout.layout.positions[workspace.id]).toBeUndefined();
});

test('lost real confirmation delivery and reload read the original receipt without creating again', async ({
  page,
  request
}) => {
  const before = await resources(request);
  const operation = await reviewBlank(request);
  const endpoint = `/api/workspaces/create-operations/${operation.id}`;
  await request.post('/api/onboarding/skip');
  await page.goto('/');
  let acceptedID = '';
  await page.route(`**${endpoint}/confirm`, async route => {
    const response = await route.fetch();
    expect(response.status()).toBe(201);
    acceptedID = (await response.json()).folder.id;
    // Real effects completed; lose only delivery to the browser. Never fulfill
    // a synthetic success or execute another confirmation to recover it.
    await route.abort('failed');
  });
  const delivered = await page.evaluate(
    async ({ endpoint, digest }) => {
      try {
        await fetch(`${endpoint}/confirm`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ review_digest: digest })
        });
        return true;
      } catch {
        return false;
      }
    },
    { endpoint, digest: operation.digest }
  );
  expect(delivered).toBe(false);
  expect(acceptedID).not.toBe('');
  await page.reload();
  for (let index = 0; index < 3; index++) {
    const response = await request.get(endpoint);
    const observed = await response.json();
    expect(observed.operation.state).toBe('complete');
    expect(observed.result.folder.id).toBe(acceptedID);
  }
  expect(await resources(request)).toEqual({
    workspaces: [...before.workspaces, acceptedID].sort(),
    agents: before.agents
  });
});
