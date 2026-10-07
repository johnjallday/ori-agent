import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Exact installed-candidate baseline, not release verification or a live model.
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
  await expect(card).toBeVisible();
  const current = await page.evaluate(() =>
    (window as any).PersonalAssistantFolderContext.current()
  );
  const before = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  const offer = before.folder_reviews[current.offerId];
  expect(offer.status).toBe('pending');
  expect(offer.subject.name).toBe('Album-5 fixture');
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
  await card.screenshot({ path: join(evidence, 'compatible-review-control.png') });
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
  expect(controls[0].signalToken).toBe('');
  await page.screenshot({ path: join(evidence, 'compatible-send-reaches-host.png') });
  const after = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(after.folder_reviews[current.offerId].status).toBe('pending');
  expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(
    workspaces.length
  );
  expect(await readFile(source, 'utf8')).toBe(sourceText);
  await writeFile(
    join(evidence, 'compatible-baseline.json'),
    JSON.stringify(
      {
        evidence:
          'Exact locally staged candidates, real host; no model, confirmation or live REAPER',
        reaperRevision: process.env.ORI_REAPER_PLUGIN_REVISION,
        musicRevision: process.env.ORI_MUSIC_PLUGIN_REVISION,
        originalFadedControl:
          'Faded primary styling reproduced on compatible Home; button is enabled, not a busy/disabled gate',
        pageAPI: apiType,
        sendOutcome: 'Real Route/Ask reached; no configured model, review remains pending',
        hierarchy: {
          home: home.name,
          child: 'Album-1 fixture',
          childAuthority: 'physical grouping only'
        },
        payloads,
        controls,
        review: { status: offer.status, subject: offer.subject.name, needsPick: offer.needs_pick }
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
