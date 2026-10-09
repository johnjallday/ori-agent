import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import {
  syntheticFolderFiles,
  folderContentSentinel
} from './fixtures/assistant-folder-response.js';

// Real Ori selection -> Route/Ask -> canonical persistence -> replay. Only the
// loopback provider's short prose is scripted: NOT vendor-model quality evidence.
// Run: python3 scripts/assistant-workspace-demo.py --folder-response --port 8931
// Native picker is not used; all synthetic files live in the owned demo HOME.
test('real host: compact metadata on Home/Settings, follow-up and canonical reload', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_FOLDERRESPONSE_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use assistant-workspace-demo.py --folder-response');
  test.setTimeout(120_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence =
    process.env.ORI_FOLDER_RESPONSE_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence/assistant-folder-response-ux/group-3');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  for (const [relative, content] of syntheticFolderFiles) {
    // Put projects directly under the known-folder chip for a multi-row scan.
    const path = join(
      sandbox!,
      relative
        .replace('Music/Album collection/', 'Documents/')
        .replace('Documents/Research papers/', 'Desktop/')
    );
    await mkdir(dirname(path), { recursive: true, mode: 0o750 });
    await writeFile(path, content, { mode: 0o600 });
  }
  await writeFile(join(provider!, 'sentinels.json'), JSON.stringify([folderContentSentinel]), {
    mode: 0o600
  });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-response-hire',
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
      request_id: 'folder-response-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const resourcesBefore = (await (await request.get('/api/workspaces')).json()).folders.length;
  const audit = async () =>
    JSON.parse(await readFile(join(provider!, 'provider-audit.json'), 'utf8'));
  const outcomes: unknown[] = [];
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const [path, chip] of [
    ['/', 'Documents'],
    ['/settings', 'Desktop']
  ]) {
    await page.goto(path);
    await expect(page.locator('#personalAssistantLauncher')).toBeAttached();
    await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
    if (path !== '/') await page.locator('#personalAssistantConversationNew').click();
    const protectedDraft = '  Keep my exact 🎼 draft\n';
    await page.locator('#personalAssistantInput').fill(protectedDraft);
    const beforeAttachment = (await audit()).requests;
    await page.locator('#personalAssistantFolderChip').click();
    const selected = page.waitForResponse(response =>
      response.url().endsWith('/folder-context/select')
    );
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: chip, exact: true })
      .click();
    const observation = (await (await selected).json()).observation;
    const explorer = page.locator('#personalAssistantFolderExplorer');
    await expect(explorer).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toHaveClass(/--exploring/);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(protectedDraft);
    expect(observation.tree.nodes.length).toBeGreaterThan(0);
    expect(observation.tree.nodes.length).toBeLessThanOrEqual(64);
    const coverageSummary = page.locator('#personalAssistantFolderExplorerCoverageSummary');
    await coverageSummary.click();
    const treeCoverage = page.locator('#personalAssistantFolderExplorerCoverage');
    await expect(treeCoverage).toBeVisible();
    await expect(treeCoverage).toContainText('3 directory levels, 5,000 entries, 3 seconds');
    await expect(treeCoverage).toContainText('Expanding reads nothing.');
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-coverage.png`) });
    await coverageSummary.click();
    const folderNode = observation.tree.nodes.find(
      (node: any) => node.kind === 'folder' && !node.parent_id
    );
    const fileNode = observation.tree.nodes.find(
      (node: any) => node.kind === 'file' && node.parent_id === folderNode.id
    );
    const folderCheck = explorer.locator(`[data-tree-focus="${folderNode.id}"]`);
    await folderCheck.check();
    await explorer.locator(`[data-tree-toggle="${folderNode.id}"]`).click();
    const fileCheck = explorer.locator(`[data-tree-focus="${fileNode.id}"]`);
    await expect(fileCheck).not.toBeChecked();
    await fileCheck.check();
    await expect(page.locator('#personalAssistantFolderFocusText')).toHaveText(
      'Next message · 2 topics'
    );
    await expect(page.locator('#personalAssistantInput')).toHaveValue(protectedDraft);
    expect((await audit()).requests).toBe(beforeAttachment);
    const selectAll = page.locator('#personalAssistantFolderSelectAll');
    const allFits = observation.tree.nodes.length <= 8;
    let expectedFocusIDs = [folderNode.id, fileNode.id];
    await expect(selectAll).toHaveText(allFits ? 'Select all' : 'Select all · Whole folder');
    await selectAll.focus();
    await selectAll.press('Enter');
    await expect(explorer.locator('input:checked')).toHaveCount(
      allFits ? observation.tree.nodes.length : 0
    );
    await expect(page.locator('#personalAssistantInput')).toHaveValue(protectedDraft);
    expect((await audit()).requests).toBe(beforeAttachment);
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-select-all.png`) });
    if (allFits) expectedFocusIDs = observation.tree.nodes.map((node: any) => node.id);
    else {
      await expect(page.locator('#personalAssistantFolderFocusText')).toHaveText(
        'Next message · Whole folder'
      );
      await folderCheck.check();
      await fileCheck.check();
    }
    expect(
      await page.evaluate(() => (window as any).PersonalAssistantFolderContext.request().focus_ids)
    ).toEqual(expectedFocusIDs);
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-light-desktop.png`) });
    await page.evaluate(() => {
      document.documentElement.dataset.bsTheme = 'dark';
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-dark-desktop.png`) });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(explorer).toBeVisible();
    await folderCheck.focus();
    await expect(folderCheck).toBeInViewport({ ratio: 1 });
    await expect(page.locator('#personalAssistantInput')).toBeInViewport();
    expect(
      await page.locator('#personalAssistantPanel').evaluate(el => el.scrollWidth <= el.clientWidth)
    ).toBeTruthy();
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-dark-phone.png`) });
    await page.locator('#personalAssistantExplorerChatTab').click();
    await expect(explorer).toBeHidden();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(protectedDraft);
    await page.locator('#personalAssistantExplorerTreeTab').click();
    await expect(folderCheck).toBeChecked();
    await page.evaluate(() => {
      document.documentElement.dataset.bsTheme = 'light';
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-light-phone.png`) });
    await page.locator('#personalAssistantExplorerBack').click();
    await expect(explorer).toBeHidden();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(protectedDraft);
    await page.locator('#personalAssistantExploreAttachedFolder').click();
    await expect(folderCheck).toBeChecked();
    await expect(fileCheck).toBeChecked();
    await page.locator('#personalAssistantExplorerBack').click();
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.locator('#personalAssistantExploreAttachedFolder').click();
    await page.evaluate(() => {
      document.body.style.zoom = '2';
    });
    await expect(page.locator('#personalAssistantPanel')).toHaveClass(/--explorer-narrow/);
    await expect(page.locator('#personalAssistantInput')).toBeInViewport({ ratio: 1 });
    await expect(page.locator('#personalAssistantSend')).toBeInViewport({ ratio: 1 });
    expect(
      await page.locator('#personalAssistantPanel').evaluate(el => el.scrollWidth <= el.clientWidth)
    ).toBeTruthy();
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-tree-css-zoom-2.png`) });
    await page.evaluate(() => {
      document.body.style.zoom = '';
    });
    await expect(page.locator('#personalAssistantPanel')).not.toHaveClass(/--explorer-narrow/);
    await page.locator('#personalAssistantExplorerBack').click();
    await page.locator('#personalAssistantInput').fill('');
    const preview = page.locator('#personalAssistantFolderPreview');
    await expect(preview).toBeVisible();
    await expect(preview).toContainText('Local preview · not sent');
    await expect(preview.getByRole('heading')).toHaveText(observation.folder);
    await expect(preview).toContainText('Attached folder: contents not read.');
    await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
    const ask = page.waitForResponse(response => response.url().endsWith('/home-assistant/ask'));
    await page.locator('#personalAssistantSend').click();
    const reply = await (await ask).json();
    expect(reply.conversation.stored).toBe(true);
    expect(reply.folder_context.observation).toEqual(observation);
    expect(reply.conversation.folder_focus.topics).toHaveLength(expectedFocusIDs.length);
    expect(reply.conversation.folder_focus.topics[0].names).toEqual([folderNode.name]);
    expect(reply.conversation.folder_focus.topics[1].names).toEqual([
      folderNode.name,
      fileNode.name
    ]);
    const firstSentFocus = page.locator(
      `[data-message-id="${reply.conversation.user_message_id}"] [data-folder-turn-focus]`
    );
    await expect(firstSentFocus).toContainText(fileNode.name);
    await page.locator('#personalAssistantExploreAttachedFolder').click();
    await fileCheck.uncheck();
    await expect(firstSentFocus).toContainText(fileNode.name);
    await page.locator('#personalAssistantExplorerBack').click();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    await expect(preview).toBeHidden();
    const card = page.locator('#homeAssistantConversation [data-folder-observation-id]');
    await expect(card).toHaveCount(1);
    await expect(card.getByRole('heading')).toHaveText(observation.folder);
    const children = observation.projects.filter((row: any) => !row.root);
    await expect(
      card.locator(
        '.personal-assistant-folder-context__details > .personal-assistant-folder-context__rows > li'
      )
    ).toHaveCount(Math.min(3, children.length));
    const details = card.locator('.personal-assistant-folder-context__details');
    expect(await details.getAttribute('open')).toBeNull();
    const firstCounts = await audit();
    await card.getByText('Scan details', { exact: true }).click();
    await expect(details).toContainText('counts overlap');
    await expect(details).toContainText('not a complete tree');
    await expect(details).toContainText('5000 entries');
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-details.png`) });
    await card.getByText('Scan details', { exact: true }).click();
    expect((await audit()).requests).toBe(firstCounts.requests);
    await firstSentFocus.scrollIntoViewIfNeeded();
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-sent-focus.png`) });
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-initial.png`) });
    const strip = page.locator('[data-folder-discussion]');
    await expect(strip).toHaveCount(1);
    const stripId = await strip.getAttribute('data-folder-discussion');
    expect(stripId).toBe(reply.conversation.assistant_message_id);
    const input = page.locator('#personalAssistantInput');
    const whole = strip.getByRole('button').first();
    const choose = strip.getByRole('button').nth(1);
    const candidate = page.locator('#personalAssistantFolderDiscussionCandidate');
    await input.fill('  Keep this exact 🎼 draft\n');
    const exactDraft = await input.inputValue();
    await whole.click();
    await expect(input).toHaveValue(exactDraft);
    await expect(input).toBeFocused();
    await expect(page.locator('#personalAssistantPanelStatus')).toContainText('Send or clear');
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-draft-protected.png`) });
    const setup = page
      .locator('[data-folder-setup-suggestion]')
      .getByRole('button', { name: 'Optional: review setup' });
    await setup.click();
    await expect(page.locator('#personalAssistantFolderSetupChoices')).toBeVisible();
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-optional-setup-chooser.png`) });
    await page.locator('#personalAssistantFolderSetupCancel').click();
    await expect(input).toHaveValue(exactDraft);
    await choose.click();
    await expect(candidate).toBeFocused();
    await expect(candidate).toHaveValue('');
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-local-project-chooser.png`) });
    expect(await candidate.locator('option').count()).toBe(
      observation.projects.filter((item: any) => !item.root).length + 1
    );
    await page.keyboard.press('Escape');
    await expect(choose).toBeFocused();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(input).toHaveValue(exactDraft);
    expect((await audit()).requests).toBe(firstCounts.requests);
    await input.fill('');
    await choose.click();
    const child = observation.projects.find((item: any) => !item.root);
    await candidate.selectOption(child.id);
    await page.locator('#personalAssistantFolderDiscussionDraft').click();
    await expect(input).toBeFocused();
    expect(await input.inputValue()).toContain(child.name);
    expect(await input.inputValue()).toContain('without setting anything up');
    await page.locator('#personalAssistantPanel').screenshot({
      path: join(evidence, `${chip.toLowerCase()}-editable-discussion-before-send.png`)
    });
    expect((await audit()).requests).toBe(firstCounts.requests);
    expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(
      resourcesBefore
    );
    const unsent = await (
      await request.get(`/api/home-assistant/conversations/${reply.conversation.id}`)
    ).json();
    expect(unsent.messages.filter((row: any) => row.role === 'assistant')).toHaveLength(1);
    const next = page.waitForResponse(response => response.url().endsWith('/home-assistant/ask'));
    await page.locator('#personalAssistantSend').click();
    const followup = await (await next).json();
    expect(followup.conversation.stored).toBe(true);
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    await expect(card).toHaveCount(1);
    await expect(strip).toHaveCount(1);
    expect(await strip.getAttribute('data-folder-discussion')).toBe(
      followup.conversation.assistant_message_id
    );
    expect(await strip.getAttribute('data-folder-discussion')).not.toBe(stripId);
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-followup.png`) });
    const id = reply.conversation.id;
    const history = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
    expect(history.messages.filter((row: any) => row.role === 'folder_context')).toHaveLength(2);
    expect(history.messages.filter((row: any) => row.role === 'assistant')).toHaveLength(2);
    expect(history.folder_context.observation).toEqual(observation);
    expect(
      history.messages.find((row: any) => row.id === reply.conversation.user_message_id)
        .folder_focus
    ).toEqual(reply.conversation.folder_focus);
    expect(
      history.messages.filter((row: any) => row.role === 'folder_context')[0].folder_context
        .focus_ids
    ).toEqual(expectedFocusIDs);
    const beforeReload = (await audit()).requests;
    await page.reload();
    await expect(card).toHaveCount(1);
    await expect(page.locator('#personalAssistantPanel')).not.toHaveClass(/--exploring/);
    await expect(firstSentFocus).toContainText(fileNode.name);
    await expect(page.locator('#personalAssistantFolderFocusText')).toHaveText(
      'Next message · Whole folder'
    );
    await expect(card.getByRole('heading')).toHaveText(observation.folder);
    await expect(card).toContainText('Bounded look');
    await expect(
      page.locator('#homeAssistantConversation [data-message-role="assistant"]')
    ).toHaveCount(2);
    await expect(strip).toHaveCount(1);
    await expect(strip.getByRole('button').first()).toHaveText('Discuss saved observations');
    expect((await audit()).requests).toBe(beforeReload);
    expect((await audit()).hits).toEqual([]);
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-reloaded.png`) });
    outcomes.push({
      page: path,
      folder: observation.folder,
      observationCount: 1,
      canonicalFolderEvents: 2,
      savedAnswers: 2,
      unchangedSnapshotAfterReload: true,
      providerCallsOnReload: 0
    });
  }
  expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(
    resourcesBefore
  );
  const digest = (await (await request.get('/api/personal-assistant/folder-digest')).json())
    .folder_digest;
  expect(digest.offer).toBeFalsy();
  await page.setViewportSize({ width: 390, height: 844 });
  const panel = page.locator('#personalAssistantPanel');
  expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy();
  await expect(page.locator('#personalAssistantInput')).toBeInViewport();
  await page.locator('#personalAssistantScroll').evaluate(el => {
    el.scrollTop = el.scrollHeight;
  });
  await panel.screenshot({ path: join(evidence, 'desktop-folder-phone.png') });
  await writeFile(
    join(evidence, 'real-host-results.json'),
    JSON.stringify(
      {
        evidence:
          'Real built Ori with scripted loopback Ollama provider; not live model/native picker',
        outcomes,
        provider: await audit(),
        resourcesUnchanged: true,
        setupOfferCreated: false
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
