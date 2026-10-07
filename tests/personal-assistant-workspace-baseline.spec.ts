import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Exact installed-candidate baseline regression, not release verification or a live model.
// Run via reaper-demo.sh test --suite awareness with explicit clean sources.
// This fixture does not substitute for paired-guidance restart acceptance.
test('compatible baseline: Music Home, Album-1 and pending Album-5 review', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_MUSIC_REAPER_SANDBOX;
  test.skip(
    process.env.ORI_MUSIC_REAPER_ACCEPTANCE !== '1' || !sandbox,
    'Requires exact candidates staged in disposable HOME by reaper-demo.sh'
  );
  expect(basename(sandbox!)).toMatch(/^ori-reaper-demo\./);
  if (process.env.ORI_WORKSPACE_SETUP_ACCEPTANCE === '1') test.setTimeout(180_000);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'awareness-compatible-hire',
      if_version: initial.state_version,
      display_name: 'Atlas',
      mandate: 'Help review my projects.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hired = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'awareness-compatible-hq',
      if_version: hired.state_version,
      name: 'My HQ'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  expect(
    (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
  ).toBeTruthy();

  const templates = (await (await request.get('/api/workspaces/group-templates')).json())
    .group_templates;
  const homeTemplate = templates.find(
    (row: any) =>
      row.kind === 'managed_home' && row.provider?.plugin_id === 'music-project-management'
  );
  expect(homeTemplate).toBeTruthy();
  const selection = {
    group_template_id: homeTemplate.id,
    revision: homeTemplate.revision,
    name: 'Music Home fixture'
  };
  const review = await request.post('/api/workspaces/group-templates/review', { data: selection });
  expect(review.ok(), await review.text()).toBeTruthy();
  const reviewed = (await review.json()).group_template_review;
  const commit = await request.post('/api/workspaces/group-templates/commit', {
    data: {
      ...selection,
      group_review_token: reviewed.review_token,
      idempotency_key: 'awareness-compatible-home'
    }
  });
  expect(commit.ok(), await commit.text()).toBeTruthy();
  const homeID = (await commit.json()).group_template.home_workspace_id;
  const homeState = await (await request.get(`/api/workspaces/${homeID}/assistant-program`)).json();
  expect(homeState).toMatchObject({ is_station: true, home_provider_available: true });

  // Physical grouping is separate from an Assistant Project Link. This fixture
  // child supplies hierarchy, not a project-agent, filesystem or runtime grant.
  const album = await request.post('/api/workspaces', {
    data: { name: 'Album-1 fixture', parent_id: homeID }
  });
  expect(album.ok(), await album.text()).toBeTruthy();
  const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
  const home = workspaces.find((row: any) => row.id === homeID);
  expect(workspaces.find((row: any) => row.name === 'Album-1 fixture').parent_id).toBe(homeID);

  const folder = join(sandbox!, 'Documents', 'Album-5 fixture');
  await mkdir(folder, { recursive: true, mode: 0o750 });
  const source = join(folder, 'Album-5.rpp');
  const sourceText = '<REAPER_PROJECT 0.1 "7.0" 1234\n  TEMPO 120 4 4\n>\n';
  await writeFile(source, sourceText, { mode: 0o600 });
  const payloads: { path: string; body: unknown }[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (req.method() === 'POST' && /^\/api\/home-assistant\/(route|ask)$/.test(path))
      payloads.push({ path, body: req.postDataJSON() });
  });
  await page.goto(`/workspaces/${home.folder_slug}/assistant`);
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  const candidate = await page
    .locator('#personalAssistantFolderSetupCandidate option')
    .filter({ hasText: 'Album-5 fixture' })
    .getAttribute('value');
  expect(candidate).toBeTruthy();
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption(candidate!);
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  await expect(card).toBeVisible({ timeout: 60_000 });
  const current = await page.evaluate(() =>
    (window as any).PersonalAssistantFolderContext.current()
  );
  const before = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  const offer = before.folder_reviews[current.offerId];
  expect(offer.status).toBe('pending');
  expect(offer.subject.name).toBe('Album-5 fixture');
  expect(offer.destination).toMatchObject({
    status: 'existing',
    workspace_id: homeID,
    name: 'Music Home fixture',
    kind: 'home'
  });
  expect(before.folder_review_context).toMatchObject({
    status: 'awaiting_confirmation',
    destination_name: 'Music Home fixture',
    destination_kind: 'home'
  });
  await expect(card).toContainText('Destination: Home “Music Home fixture” · separate project.');
  const controls = await card.locator('button').evaluateAll(buttons =>
    buttons.map(button => ({
      label: button.textContent,
      disabled: (button as HTMLButtonElement).disabled,
      opacity: getComputedStyle(button).opacity,
      color: getComputedStyle(button).color,
      background: getComputedStyle(button).backgroundColor,
      signalToken: getComputedStyle(button).getPropertyValue('--hc-signal'),
      classes: button.className
    }))
  );
  await card.screenshot({ path: join(evidence, 'compatible-review-control-fixed.png') });
  await page.locator('#personalAssistantInput').fill('Add this to my workspace');
  await page.locator('#personalAssistantSend').click();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  // API is a classic-script global lexical binding, not window.API. The
  // original missing page dependency is fixed; now both real host hops run.
  const apiType = await page.evaluate('typeof API');
  expect(apiType).toBe('object');
  expect(payloads.map(value => value.path)).toEqual([
    '/api/home-assistant/route',
    '/api/home-assistant/ask'
  ]);
  await expect(card).toBeVisible();
  expect(controls[0].disabled).toBe(false);
  expect(controls[0].signalToken.trim()).not.toBe('');
  expect(controls[0].background).not.toBe('rgba(0, 0, 0, 0)');
  await page.screenshot({ path: join(evidence, 'compatible-send-reaches-host-fixed.png') });
  const after = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(after.folder_reviews[current.offerId].status).toBe('pending');
  expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(
    workspaces.length
  );
  expect(await readFile(source, 'utf8')).toBe(sourceText);

  const child = workspaces.find((row: any) => row.name === 'Album-1 fixture');
  await page.goto(`/workspaces/${child.folder_slug}/assistant`);
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantWorkspaceContext')).toContainText('Album-1 fixture');
  await expect(card).toContainText('Destination: Home “Music Home fixture” · separate project.');
  const navigated = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(navigated.folder_reviews[current.offerId].destination.workspace_id).toBe(homeID);

  // Rename only this disposable Home. Reading must retain the old witness and
  // block confirmation; an explicit Review then refreshes the material digest.
  const renamed = await request.put(`/api/workspaces/${homeID}`, {
    data: { name: 'Renamed Music Home fixture' }
  });
  expect(renamed.ok(), await renamed.text()).toBeTruthy();
  await page.evaluate(
    (id: string) => (window as any).PersonalAssistantConversation.resume(id),
    current.conversationId
  );
  await expect(card).toContainText('The reviewed destination has changed.');
  await expect(card).toContainText('Review workspace setup');
  await expect(card.locator('#personalAssistantFolderOfferActions button')).toHaveText([
    'Keep chatting'
  ]);
  await card.screenshot({ path: join(evidence, 'compatible-destination-changed.png') });
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption(candidate!);
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  await expect(card).toContainText(
    'Destination: Home “Renamed Music Home fixture” · separate project.'
  );
  const refreshed = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(refreshed.folder_context.offer_id).toBe(current.offerId);
  expect(refreshed.folder_reviews[current.offerId].destination.workspace_id).toBe(homeID);
  expect(refreshed.folder_reviews[current.offerId].status).toBe('pending');
  expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(
    workspaces.length
  );
  expect(await readFile(source, 'utf8')).toBe(sourceText);
  await card.screenshot({ path: join(evidence, 'compatible-destination-refreshed.png') });
  if (process.env.ORI_WORKSPACE_SETUP_ACCEPTANCE === '1') {
    expect(
      (
        await request.post('/api/settings/system-model', {
          data: { provider: 'ollama', model: 'ori-workspace-fixture' }
        })
      ).ok()
    ).toBeTruthy();
    const currentReview = await request.post('/api/home-assistant/folder-context/review', {
      data: {
        conversation_id: current.conversationId,
        revision: refreshed.folder_context.revision,
        selection_id: current.observation.id,
        candidate_id: candidate,
        context: {
          context_version: 1,
          origin: 'personal_assistant_panel',
          surface: 'workspace_detail',
          workspace_id: child.id,
          workspace_slug: child.folder_slug,
          page_path: `/workspaces/${child.folder_slug}/assistant`
        }
      }
    });
    expect(currentReview.ok(), await currentReview.text()).toBeTruthy();
    const preparedHistory = await (
      await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
    ).json();
    const ready = preparedHistory.folder_reviews[current.offerId];
    const candidatePlugins = (await (await request.get('/api/plugins')).json()).plugins.map(
      (plugin: any) => ({
        name: plugin.name,
        version: plugin.version,
        format: plugin.format,
        generation: plugin.generation,
        fingerprint: plugin.component_fingerprint
      })
    );
    await writeFile(
      join(evidence, 'compatible-confirmed-preparation.json'),
      JSON.stringify(
        {
          subject: ready.subject,
          destination: ready.destination,
          plan: ready.plan,
          capability: ready.capability,
          status: ready.status,
          setupUnavailableReason: ready.setup_unavailable_reason,
          plugins: candidatePlugins,
          boundary:
            'Exact candidate preparation with loopback deterministic provider; no vendor model or live REAPER'
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
    expect(
      ready.plan,
      'Exact installed candidate must provide a witnessed one-click plan'
    ).toBeTruthy();
    await page.evaluate(
      (id: string) => (window as any).PersonalAssistantConversation.resume(id),
      current.conversationId
    );
    await card.getByRole('button', { name: 'Set up', exact: true }).click();
    await expect
      .poll(
        async () => {
          const saved = await (
            await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
          ).json();
          return saved.folder_review_context?.status;
        },
        { timeout: 120_000 }
      )
      .toBe('completed');
    const completed = await (
      await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
    ).json();
    const result = completed.folder_reviews[current.offerId];
    expect(result.outcome.parent).toMatchObject({
      status: 'existing',
      workspace_id: homeID,
      name: 'Renamed Music Home fixture'
    });
    expect(result.outcome.workspace_id).not.toBe(child.id);
    const finalWorkspaces = (await (await request.get('/api/workspaces')).json()).folders;
    expect(
      finalWorkspaces.find((row: any) => row.id === result.outcome.workspace_id).parent_id
    ).toBe(homeID);
    expect(finalWorkspaces.find((row: any) => row.id === child.id).name).toBe(child.name);
    expect(await readFile(source, 'utf8')).toBe(sourceText);
    await page.screenshot({ path: join(evidence, 'compatible-confirmed-project.png') });

    const supportingRoot = join(sandbox!, 'Documents', 'Supporting references fixture');
    await mkdir(supportingRoot, { recursive: true, mode: 0o750 });
    const supportingBytes = 'Supporting-folder fixture: source must remain unchanged.';
    await writeFile(join(supportingRoot, 'references.md'), supportingBytes, { mode: 0o600 });
    const originalChild = await (await request.get(`/api/workspaces/${child.id}`)).json();
    await page.locator('#personalAssistantFolderChip').click();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
    await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
    const supportingCandidate = await page
      .locator('#personalAssistantFolderSetupCandidate option')
      .filter({ hasText: 'Supporting references fixture' })
      .getAttribute('value');
    expect(supportingCandidate).toBeTruthy();
    await page.locator('#personalAssistantFolderSetupCandidate').selectOption(supportingCandidate!);
    await page.getByRole('button', { name: 'Review selection', exact: true }).click();
    const placement = page.getByRole('region', { name: 'Choose folder placement' });
    await expect(placement).toContainText('supporting source');
    expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(
      finalWorkspaces.length
    );
    await placement
      .getByRole('button', { name: 'Review supporting folder in Album-1 fixture', exact: true })
      .click();
    await expect(card).toContainText(
      'primary project entry, blueprint, mode, agents and tasks stay unchanged'
    );
    await card.getByRole('button', { name: 'Link supporting folder', exact: true }).click();
    await expect(card).toContainText('Linked Supporting references fixture as a supporting source');
    const linkedChild = await (await request.get(`/api/workspaces/${child.id}`)).json();
    expect(linkedChild.shared_data).toEqual(originalChild.shared_data);
    expect(linkedChild.agent_instances).toEqual(originalChild.agent_instances);
    expect(linkedChild.tasks).toEqual(originalChild.tasks);
    expect(linkedChild.parent_id).toBe(originalChild.parent_id);
    expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(
      finalWorkspaces.length
    );
    expect(await readFile(join(supportingRoot, 'references.md'), 'utf8')).toBe(supportingBytes);
    await expect(page.locator('#personalAssistantInput')).toHaveValue('Add this to my workspace');
    await page.screenshot({ path: join(evidence, 'compatible-confirmed-supporting-folder.png') });
    await writeFile(
      join(evidence, 'compatible-confirmed-project.json'),
      JSON.stringify(
        {
          boundary:
            'Explicit drawer confirmation with exact local candidates and deterministic loopback provider; no task opened, vendor model or live REAPER',
          reaperRevision: process.env.ORI_REAPER_PLUGIN_REVISION,
          musicRevision: process.env.ORI_MUSIC_PLUGIN_REVISION,
          status: completed.folder_review_context.status,
          receipt: result.outcome.receipt,
          resultingParent: {
            id: result.outcome.parent.workspace_id,
            name: result.outcome.parent.name
          },
          sourceUnchanged: true,
          originalProjectUnchanged: true
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
  }
  await writeFile(
    join(evidence, 'compatible-baseline-fixed.json'),
    JSON.stringify(
      {
        evidence:
          'Exact locally staged candidates, real host; no model, confirmation or live REAPER',
        reaperRevision: process.env.ORI_REAPER_PLUGIN_REVISION,
        musicRevision: process.env.ORI_MUSIC_PLUGIN_REVISION,
        originalFadedControl:
          'Previously reproduced enabled-but-faded styling is fixed: drawer supplies its own signal token; original uncaptured screenshot remains distinct',
        pageAPI: apiType,
        sendOutcome: 'Real Route/Ask reached; no configured model, review remains pending',
        hierarchy: {
          home: home.name,
          child: 'Album-1 fixture',
          childAuthority: 'physical grouping only'
        },
        payloads,
        controls,
        review: {
          status: offer.status,
          subject: offer.subject.name,
          needsPick: offer.needs_pick,
          destination: { name: offer.destination.name, kind: offer.destination.kind },
          placementEvidence: 'Canonical existing Home disclosure; no confirmed setup acceptance'
        }
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
