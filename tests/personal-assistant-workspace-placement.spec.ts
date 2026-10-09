import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Production browser, Route/Ask/Review/Decide, Ollama adapter and canonical
// stores under `wt demo`. Only the loopback provider's reply text is fixed, so
// this proves host and drawer behavior, not what a vendor model would say.
// Every workspace, folder and file here lives in the demo's own sandbox.
test('wt demo: named review, blocked refresh, confirmed setup, project choice and existing project', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_PLACEMENT_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --placement');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const album = join(sandbox!, 'Documents', 'Album-5 placement fixture');
  const references = join(sandbox!, 'Documents', 'Supporting references fixture');
  await mkdir(album, { recursive: true, mode: 0o750 });
  await mkdir(references, { recursive: true, mode: 0o750 });
  const albumBytes = 'Album-5 placement fixture: the source must stay unchanged.';
  const referenceBytes = 'Supporting references fixture: the source must stay unchanged.';
  await writeFile(join(album, 'notes.txt'), albumBytes, { mode: 0o600 });
  await writeFile(join(references, 'references.md'), referenceBytes, { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'workspace-placement-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'workspace-placement-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const workspaces = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const create = async (name: string, extra: Record<string, unknown> = {}) => {
    if (extra.kind === 'group') {
      const reviewed = await request.post('/api/workspaces/template-agent-plan', {
        data: { group_roster: true, group_name: name }
      });
      expect(reviewed.ok(), await reviewed.text()).toBeTruthy();
      const plan = await reviewed.json();
      extra = {
        ...extra,
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: plan.revision,
          expectations: plan.agents.map((agent: any, index: number) => ({
            index,
            name: agent.name,
            action: agent.action
          }))
        }
      };
    }
    const response = await request.post('/api/workspaces', { data: { name, ...extra } });
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await workspaces()).find((row: any) => row.name === name);
  };
  const group = await create('Music portfolio placement fixture', { kind: 'group' });
  const album1 = await create('Album-1 placement fixture', { parent_id: group.id });
  const original = await (await request.get(`/api/workspaces/${album1.id}`)).json();
  const initial = await workspaces();

  const label = page.locator('#personalAssistantWorkspaceContext');
  const status = page.locator('#personalAssistantFolderReviewStatus');
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  const openDrawer = async () => {
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
  };
  const attachDocuments = async () => {
    await page.locator('#personalAssistantFolderChip').click();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    await expect(page.locator('#personalAssistantFolderExplorer')).toBeVisible();
    await page.locator('#personalAssistantExplorerBack').click();
    await expect(page.locator('#personalAssistantFolderPreview')).toBeVisible();
  };
  const chooseCandidate = async (name: string) => {
    await expect(page.locator('#personalAssistantFolderSetupCandidate')).toBeVisible();
    await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label: name });
    await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  };
  const send = async (prompt: string) => {
    await page.locator('#personalAssistantInput').fill(prompt);
    const response = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const body = await (await response).json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(body.conversation.stored).toBe(true);
    return body;
  };
  const current = () =>
    page.evaluate(() => (window as any).PersonalAssistantFolderContext.current());
  const history = async (id: string) =>
    (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  const shot = (name: string) =>
    page.locator('#personalAssistantPanel').screenshot({ path: join(evidence, name) });

  // 1. On the group's page, "add this" proposes a separate project in that
  // named group. Nothing is prepared until the user asks for the review.
  await page.goto(`/workspaces/${group.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toHaveText('Context · Group: Music portfolio placement fixture');
  await attachDocuments();
  const asked = await send('Add this to my workspace');
  expect(asked.workspace_context.subject.id).toBe(group.id);
  expect(asked.folder_setup_suggestion.options.length).toBeGreaterThan(0);
  expect(asked.folder_setup_suggestion.subject).toBeUndefined();
  expect(asked.folder_review_context).toBeUndefined();
  expect(await workspaces()).toHaveLength(initial.length);
  await page.getByRole('button', { name: 'Choose setup scope', exact: true }).click();
  await chooseCandidate('Album-5 placement fixture');
  await expect(card).toBeVisible();
  await expect(card).toContainText(
    'Destination: Group “Music portfolio placement fixture” · separate project.'
  );
  const review = await current();
  const pending = (await history(review.conversationId)).folder_reviews[review.offerId];
  expect(pending).toMatchObject({ status: 'pending', operation: 'create_project_workspace' });
  expect(pending.destination).toMatchObject({ workspace_id: group.id, kind: 'group' });
  await expect(status).toContainText(
    'Setup review for Album-5 placement fixture in Music portfolio placement fixture is waiting for your confirmation.'
  );
  // The card keeps polling its own state; an unchanged review keeps the sentence.
  await page.waitForTimeout(1500);
  await status.scrollIntoViewIfNeeded();
  await expect(status).toBeVisible();
  await shot('placement-named-review.png');

  // 2. Saying "add this" again reuses that review: the model and the drawer get
  // the same canonical summary, and chat is not a confirmation.
  const again = await send('Add this to my workspace');
  expect(again.folder_setup_suggestion.offer_id).toBe(review.offerId);
  expect(again.folder_setup_suggestion.options ?? null).toBeNull();
  expect(again.folder_review_context).toMatchObject({
    status: 'awaiting_confirmation',
    subject: 'Album-5 placement fixture',
    destination_status: 'existing',
    destination_name: 'Music portfolio placement fixture'
  });
  await expect(
    page.getByRole('button', { name: 'Show existing setup review', exact: true })
  ).toBeVisible();
  await expect(status).toContainText('is waiting for your confirmation');
  expect((await history(review.conversationId)).folder_reviews[review.offerId]).toEqual(pending);
  expect(await workspaces()).toHaveLength(initial.length);

  // 3. Moving to Album-1's page does not move the review: it still names the
  // group. A blocked review then says why and how to recover. Renaming the group
  // makes the reviewed destination stale; only an explicit Review refreshes it,
  // and it follows the saved review, not the page now on screen.
  await page.goto(`/workspaces/${album1.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toContainText('Project: Album-1 placement fixture');
  await expect(card).toContainText(
    'Destination: Group “Music portfolio placement fixture” · separate project.'
  );
  const renamed = await request.put(`/api/workspaces/${group.id}`, {
    data: { name: 'Renamed portfolio placement fixture' }
  });
  expect(renamed.ok(), await renamed.text()).toBeTruthy();
  await page.evaluate(
    (id: string) => (window as any).PersonalAssistantConversation.resume(id),
    review.conversationId
  );
  await expect(card).toContainText('The reviewed destination has changed.');
  await expect(card).not.toContainText('Confirming creates');
  await expect(card.locator('#personalAssistantFolderOfferActions button')).toHaveText([
    'Keep chatting'
  ]);
  await expect(status).toContainText(
    'The reviewed destination for Album-5 placement fixture has changed. Use Review setup to refresh the review.'
  );
  await shot('placement-blocked-review.png');
  // The reason is announced text, not a faded button alone, and the recovery
  // it names can be reached and used from the keyboard.
  await expect(status).toHaveAttribute('role', 'status');
  await expect(status).toHaveAttribute('aria-live', 'polite');
  const recover = page.getByRole('button', { name: 'Review workspace setup', exact: true });
  await recover.focus();
  await expect(recover).toBeFocused();
  await page.keyboard.press('Enter');
  await chooseCandidate('Album-5 placement fixture');
  await expect(card).toContainText(
    'Destination: Group “Renamed portfolio placement fixture” · separate project.'
  );
  const refreshed = await history(review.conversationId);
  expect(refreshed.folder_context.offer_id).toBe(review.offerId);
  expect(refreshed.folder_reviews[review.offerId].status).toBe('pending');
  expect(await workspaces()).toHaveLength(initial.length);
  await shot('placement-before-confirmation.png');

  // 4. Only the reviewed control sets anything up; the result is read from the
  // canonical receipt. Album-1 and the source files are untouched.
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect
    .poll(async () => (await history(review.conversationId)).folder_review_context?.status, {
      timeout: 60_000
    })
    .toBe('completed');
  const completed = (await history(review.conversationId)).folder_reviews[review.offerId];
  expect(completed.outcome.parent).toMatchObject({
    workspace_id: group.id,
    name: 'Renamed portfolio placement fixture'
  });
  const afterCreate = await workspaces();
  expect(afterCreate).toHaveLength(initial.length + 1);
  const created = afterCreate.find((row: any) => row.id === completed.outcome.workspace_id);
  expect(created.parent_id).toBe(group.id);
  expect(created.id).not.toBe(album1.id);
  const unchanged = await (await request.get(`/api/workspaces/${album1.id}`)).json();
  for (const key of ['name', 'parent_id', 'shared_data', 'agent_instances', 'tasks'])
    expect(unchanged[key]).toEqual(original[key]);
  expect(await readFile(join(album, 'notes.txt'), 'utf8')).toBe(albumBytes);
  await expect(card).toContainText("Here's what I set up:");
  await shot('placement-confirmed-project.png');

  // 5. Inside Album-1 the same request is a question first: link the folder
  // there as a supporting source, or create a separate project.
  await page.goto(`/workspaces/${album1.folder_slug}/canvas`);
  await openDrawer();
  await expect(label).toContainText('Project: Album-1 placement fixture');
  await attachDocuments();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await chooseCandidate('Supporting references fixture');
  const placement = page.getByRole('region', { name: 'Choose folder placement' });
  await expect(placement).toContainText('supporting source');
  await expect(placement.getByRole('button')).toHaveText([
    'Review supporting folder in Album-1 placement fixture',
    'Review a separate project in Renamed portfolio placement fixture',
    'Keep chatting'
  ]);
  expect(await workspaces()).toHaveLength(afterCreate.length);
  // Nothing was prepared, so no "preparing" notice is left behind.
  await expect(page.locator('#personalAssistantContextStatus')).toHaveText('');
  await expect(placement.getByRole('button', { name: 'Keep chatting' })).toBeInViewport();
  await shot('placement-project-choice.png');
  await placement
    .getByRole('button', {
      name: 'Review supporting folder in Album-1 placement fixture',
      exact: true
    })
    .click();
  await expect(card).toContainText(
    'primary project entry, blueprint, mode, agents and tasks stay unchanged'
  );
  await card.getByRole('button', { name: 'Link supporting folder', exact: true }).click();
  await expect(card).toContainText('Linked Supporting references fixture as a supporting source');
  const linked = await (await request.get(`/api/workspaces/${album1.id}`)).json();
  for (const key of ['name', 'parent_id', 'shared_data', 'agent_instances', 'tasks'])
    expect(linked[key]).toEqual(original[key]);
  expect(await workspaces()).toHaveLength(afterCreate.length);
  expect(await readFile(join(references, 'references.md'), 'utf8')).toBe(referenceBytes);
  await shot('placement-supporting-folder.png');

  // 6. In a new conversation, the folder that already became a project is
  // pointed at, not set up a second time.
  await page.locator('#personalAssistantConversationNew').click();
  await attachDocuments();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await chooseCandidate('Album-5 placement fixture');
  await placement
    .getByRole('button', {
      name: 'Review a separate project in Renamed portfolio placement fixture',
      exact: true
    })
    .click();
  const existing = page.getByRole('region', { name: 'Existing project' });
  await expect(existing).toContainText(
    'Album-5 placement fixture is already set up as the project “Album-5 placement fixture”. Nothing new has been prepared.'
  );
  await expect(
    existing.getByRole('link', { name: 'Open Album-5 placement fixture', exact: true })
  ).toHaveAttribute('href', `/workspaces/${created.folder_slug}`);
  expect(await workspaces()).toHaveLength(afterCreate.length);
  await expect(page.locator('#personalAssistantContextStatus')).toHaveText('');
  await shot('placement-existing-project.png');
  await existing.getByRole('button', { name: 'Keep chatting', exact: true }).click();

  // 7. Naming a workspace wins over the page for that request: the suggestion
  // carries the named group into Review instead of asking about Album-1.
  const named = await send('Add this folder to Renamed portfolio placement fixture');
  expect(named.workspace_context.location.id).toBe(album1.id);
  expect(named.workspace_context.subject.id).toBe(group.id);
  expect(named.folder_setup_suggestion.subject).toMatchObject({
    workspace_id: group.id,
    name: 'Renamed portfolio placement fixture',
    kind: 'group'
  });
  await expect(page.locator('[data-folder-setup-suggestion]')).toContainText(
    'Workspace you named: Renamed portfolio placement fixture. Placement and operation are rechecked in Review'
  );
  await page.getByRole('button', { name: 'Choose setup scope', exact: true }).click();
  await chooseCandidate('Documents (whole folder)');
  await expect(card).toBeVisible();
  await expect(card).toContainText(
    'Destination: Group “Renamed portfolio placement fixture” · separate project.'
  );
  await expect(placement).toHaveCount(0);
  const namedReview = await current();
  const namedOffer = (await history(namedReview.conversationId)).folder_reviews[
    namedReview.offerId
  ];
  expect(namedOffer.destination.workspace_id).toBe(group.id);
  expect(namedOffer.status).toBe('pending');
  await shot('placement-named-workspace.png');
  // Keep chatting closes the review without creating or declining anything.
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await history(namedReview.conversationId)).folder_reviews[namedReview.offerId]?.status
    )
    .toBe('closed');
  expect(await workspaces()).toHaveLength(afterCreate.length);
  expect(await readFile(join(album, 'notes.txt'), 'utf8')).toBe(albumBytes);

  await writeFile(
    join(evidence, 'placement-demo.json'),
    JSON.stringify(
      {
        evidence:
          'wt demo real host + production Ollama adapter + loopback deterministic provider; no vendor model, plugin or live application',
        namedReview: { subject: pending.subject.name, destination: pending.destination.name },
        blockedThenRefreshed: {
          sameOffer: refreshed.folder_context.offer_id === review.offerId,
          destination: refreshed.folder_reviews[review.offerId].destination.name
        },
        confirmed: {
          receipt: completed.outcome.receipt,
          resultingParent: completed.outcome.parent.name
        },
        projectChoice:
          'supporting folder linked to Album-1; primary entry, agents, tasks unchanged',
        existingProject: 'second review pointed at the completed project; no workspace created',
        namedWorkspace: {
          page: 'Album-1 placement fixture',
          destination: namedOffer.destination.name
        },
        workspaceCount: { before: initial.length, after: afterCreate.length },
        sourcesUnchanged: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
