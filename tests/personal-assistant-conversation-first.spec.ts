import { test, expect, type Page } from '@playwright/test';
import { mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { folderContentSentinel } from './fixtures/assistant-folder-response.js';

// Real host/provider/store characterization. Geometry is observed BEFORE any
// screenshot/action can scroll it. Group 2 adds the new visibility assertions;
// baseline observations must not encode the broken viewport as a requirement.
const evidence =
  process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
  join(process.cwd(), 'tasks/evidence/assistant-conversation-first/baseline');

async function settled(page: Page) {
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  await page.waitForFunction(
    () => (window as any).PersonalAssistantTranscript?.isSettled?.() ?? true
  );
  await page.evaluate(
    () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
  );
}

async function geometry(page: Page, messageId = '') {
  return page.evaluate(id => {
    const pane = document.getElementById('personalAssistantScroll')!;
    const bounds = pane.getBoundingClientRect();
    const row = Array.from(
      document.querySelectorAll<HTMLElement>('#homeAssistantConversation > *')
    ).find(row => row.dataset.messageId === id);
    const box = row?.getBoundingClientRect();
    const input = document.getElementById('personalAssistantInput')!.getBoundingClientRect();
    const tree = document.getElementById('personalAssistantFolderExplorer')!;
    const active = document.activeElement as HTMLElement;
    return {
      scrollTop: pane.scrollTop,
      scrollHeight: pane.scrollHeight,
      viewportHeight: pane.clientHeight,
      rowHeight: box?.height,
      rowStartVisible: Boolean(box && box.top >= bounds.top && box.top < bounds.bottom),
      rowEndVisible: Boolean(box && box.bottom <= bounds.bottom && box.bottom > bounds.top),
      rowOffset: box ? box.top - bounds.top : null,
      composerVisible: input.top >= 0 && input.bottom <= innerHeight,
      treeTop: tree.scrollTop,
      pageTop: scrollY,
      activeId: active?.id || '',
      activeRole: active?.dataset.messageRole || ''
    };
  }, messageId);
}

test('real host: conversation-first viewport, delayed reading, Tree + Chat and canonical review', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_CONVERSATIONFIRST_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use assistant-workspace-demo.py --conversation-first');
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const files = [
    ...Array.from({ length: 5 }, (_, i) => `Documents/Albums/Album-${i + 1}/notes.txt`),
    'Documents/Logic Sketch/demo.logicx/metadata.txt'
  ];
  for (const relative of files) {
    const path = join(sandbox!, relative);
    await mkdir(dirname(path), { recursive: true, mode: 0o750 });
    await writeFile(path, folderContentSentinel, { mode: 0o600 });
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
      request_id: 'conversation-first-hire',
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
      request_id: 'conversation-first-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const resourcesBefore = (await (await request.get('/api/workspaces')).json()).folders.length;
  // Only Today failure presentation is a browser fixture. Chat/selection/review
  // requests, canonical history and held generation remain the real host path.
  await page.route('**/api/personal-assistant/today', async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.today.state = 'partial';
    body.today.unavailable_sources = ['daily_brief'];
    await route.fulfill({ response, json: body });
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  const observations: unknown[] = [];
  const capture = async (name: string, id = '') => {
    await page.waitForFunction(
      () => (window as any).PersonalAssistantTranscript?.isSettled?.() ?? true
    );
    const measured = await geometry(page, id);
    observations.push({ name, ...measured });
    if (id && !name.includes('scroll-away')) {
      expect(measured.rowStartVisible, `${name}: beginning visible without corrective scroll`).toBe(
        true
      );
      if (!name.endsWith('-long'))
        expect(measured.rowEndVisible, `${name}: complete short response and controls`).toBe(true);
    }
    if (name.endsWith('-pending')) {
      await expect(page.locator('[data-pending-reply]')).toHaveCount(1);
      await expect(page.locator('[data-pending-reply]')).toBeInViewport();
      await expect(
        page.locator('#homeAssistantThinkingModal .ask-ori-activity__head')
      ).toBeHidden();
      await expect(page.locator('#homeAssistantRoutingSummary')).toBeHidden();
    }
    await page.screenshot({ path: join(evidence, `${name}.png`) });
    return measured;
  };
  const send = async (prompt: string) => {
    await page.locator('#personalAssistantInput').fill(prompt);
    const response = page.waitForResponse(
      r => new URL(r.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const body = await (await response).json();
    await settled(page);
    expect(body.conversation?.stored, JSON.stringify(body)).toBe(true);
    return body;
  };
  const audit = async () =>
    JSON.parse(await readFile(join(provider!, 'provider-audit.json'), 'utf8'));
  for (const [path, prefix] of [
    ['/', 'home'],
    ['/settings', 'settings']
  ]) {
    await page.goto(path);
    await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    if (await page.locator('#personalAssistantConversationNew').isEnabled())
      await page.locator('#personalAssistantConversationNew').click();
    if (path === '/') await capture('home-today-error-compact');
    const ordinary = await send('Tell me about this workspace');
    await capture(`${prefix}-ordinary-first`, ordinary.conversation.assistant_message_id);
    if (path === '/') {
      await expect(page.locator('#personalAssistantTodayFooter')).toBeHidden();
      await expect(page.locator('#personalAssistantSummaryText')).toContainText(
        'Today sources unavailable'
      );
      await page.locator('#personalAssistantSummaryToggle').click();
      await expect(page.locator('#personalAssistantTodayFooter')).toBeVisible();
      await expect(page.locator('#personalAssistantTodayRetry')).toBeVisible();
      await page.screenshot({ path: join(evidence, 'home-today-error-expanded.png') });
      await page.locator('#personalAssistantSummaryToggle').click();
    }
    const repeated = await send('What should we discuss next?');
    await capture(`${prefix}-ordinary-repeated`, repeated.conversation.assistant_message_id);
    const long = await send('Conversation fixture: long answer. Explain the next steps.');
    expect(long.response).toContain('Beginning of the long fixture reply.');
    expect(
      (await geometry(page, long.conversation.assistant_message_id)).rowHeight
    ).toBeGreaterThan(300);
    await capture(`${prefix}-ordinary-long`, long.conversation.assistant_message_id);

    await page.locator('#personalAssistantFolderChip').click();
    const selection = page.waitForResponse(r => r.url().endsWith('/folder-context/select'));
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    const selected = (await (await selection).json()).observation;
    await expect(page.locator('#personalAssistantPanel')).toHaveClass(/--exploring/);
    const topic = selected.tree.nodes.find(
      (node: any) => node.kind === 'folder' && node.name === 'Albums'
    );
    expect(topic).toBeTruthy();
    await page.locator(`[data-tree-focus="${topic.id}"]`).check();
    const beforeSend = await audit();
    const refs = await page.evaluate(() =>
      (window as any).PersonalAssistantFolderContext.request()
    );
    expect(refs.focus_ids).toEqual([topic.id]);
    const folderAsk = page.waitForRequest(r => r.url().endsWith('/home-assistant/ask'));
    const folder = await send('Explore this folder');
    expect((await folderAsk).postDataJSON().folder_context).toEqual(refs);
    expect(folder.conversation.folder_focus.topics[0].names).toEqual(['Albums']);
    expect(folder.folder_context.observation).toEqual(selected);
    expect(
      folder.folder_setup_suggestion.options.every((option: any) =>
        selected.projects.some((p: any) => p.id === option.candidate_id)
      )
    ).toBe(true);
    await capture(`${prefix}-tree-first`, folder.conversation.assistant_message_id);
    expect((await audit()).hits).toEqual([]);
    expect((await audit()).requests).toBeGreaterThan(beforeSend.requests);
    const treeLong = await send('Conversation fixture: long answer. Explain this folder.');
    await capture(`${prefix}-tree-long`, treeLong.conversation.assistant_message_id);

    await rm(join(provider!, 'release'), { force: true });
    await rm(join(provider!, 'accepted.json'), { force: true });
    await writeFile(join(provider!, 'hold-next'), '', { mode: 0o600 });
    await page.locator('#personalAssistantInput').fill('What should we discuss next?');
    const delayed = page.waitForResponse(r => r.url().endsWith('/home-assistant/ask'));
    await page.locator('#personalAssistantSend').click();
    await expect
      .poll(async () => {
        try {
          return Boolean(JSON.parse(await readFile(join(provider!, 'accepted.json'), 'utf8')));
        } catch {
          return false;
        }
      })
      .toBe(true);
    await capture(`${prefix}-pending`);
    // Intentional reading, NOT corrective arrival scrolling. Track an actual
    // connected history row and its viewport offset across the held reply.
    await page.locator('#personalAssistantScroll').hover();
    await page.mouse.wheel(0, -10000);
    await page.waitForFunction(
      () => document.getElementById('personalAssistantScroll')!.scrollTop < 100
    );
    const anchorId = await page.evaluate(() => {
      const pane = document.getElementById('personalAssistantScroll')!.getBoundingClientRect();
      return (
        Array.from(
          document.querySelectorAll<HTMLElement>('#homeAssistantConversation [data-message-id]')
        ).find(row => row.getBoundingClientRect().bottom > pane.top)?.dataset.messageId || ''
      );
    });
    await page.locator('#personalAssistantInput').fill('  Preserve my exact 🎼 draft\n');
    const reading = await geometry(page, anchorId);
    const draft = await page.locator('#personalAssistantInput').inputValue();
    await writeFile(join(provider!, 'release'), '', { mode: 0o600 });
    const arrival = await (await delayed).json();
    await settled(page);
    expect(arrival.conversation.stored).toBe(true);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    const after = await geometry(page, anchorId);
    expect(after.activeId).toBe(reading.activeId);
    expect(after.treeTop).toBe(reading.treeTop);
    expect(after.pageTop).toBe(reading.pageTop);
    expect(Math.abs(after.rowOffset! - reading.rowOffset!)).toBeLessThan(3);
    await expect(page.getByRole('button', { name: 'New reply', exact: true })).toBeVisible();
    observations.push({ name: `${prefix}-reading-anchor`, before: reading, after });
    await capture(`${prefix}-scroll-away-arrival`, arrival.conversation.assistant_message_id);
    await page.getByRole('button', { name: 'New reply', exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(
      page.locator(`[data-message-id="${arrival.conversation.assistant_message_id}"]`)
    ).toBeFocused();
    const revealed = await geometry(page, arrival.conversation.assistant_message_id);
    expect(revealed.rowStartVisible).toBe(true);
    expect(revealed.rowEndVisible).toBe(true);
    await expect(page.getByRole('button', { name: 'New reply', exact: true })).toBeHidden();
    const proposal = page.locator('[data-folder-setup-suggestion]');
    await expect(proposal.locator('[data-proposal-kind="unknown"]')).toContainText(
      'No folder selected'
    );
    const beforeChoice = await audit();
    await proposal.getByRole('button', { name: 'Choose setup scope', exact: true }).click();
    const localChoice = page.locator('#personalAssistantFolderSetupCandidate');
    await expect(localChoice).toHaveValue('');
    await localChoice.selectOption(
      selected.projects.find((project: any) => project.name === 'Albums').id
    );
    await expect(page.locator('[data-setup-choice-preview]')).toHaveAttribute(
      'data-proposal-kind',
      'workspace'
    );
    await expect(page.locator('[data-setup-choice-preview]')).toContainText('Proposed workspace');
    await capture(`${prefix}-proposal-workspace-selected`);
    expect((await audit()).requests).toBe(beforeChoice.requests);
    expect(
      (await (await request.get('/api/personal-assistant/folder-digest')).json()).folder_digest
        .offer
    ).toBeFalsy();
    await page.locator('#personalAssistantFolderSetupCancel').click();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);

    const history = (
      await (
        await request.get(`/api/home-assistant/conversations/${arrival.conversation.id}`)
      ).json()
    ).messages;
    const shownIds = await page
      .locator('#homeAssistantConversation > [data-message-id]')
      .evaluateAll(rows => rows.map(row => (row as HTMLElement).dataset.messageId));
    expect(shownIds).toEqual(
      history.filter((row: any) => row.role !== 'folder_context').map((row: any) => row.id)
    );
    // Unchanged observations deliberately deduplicate their factual card, not
    // the canonical context events stored for each turn.
    await expect(
      page.locator('#homeAssistantConversation [data-folder-observation-id]')
    ).toHaveCount(1);
    const beforeReload = await audit();
    await page.reload();
    await page.waitForFunction(
      () =>
        !(window as any).PersonalAssistantConversation?.isLoading() &&
        Boolean((window as any).PersonalAssistantConversation?.currentId())
    );
    await expect(page.locator('#homeAssistantConversation > [data-message-id]')).toHaveCount(
      history.filter((row: any) => row.role !== 'folder_context').length
    );
    expect((await audit()).requests).toBe(beforeReload.requests);
    await capture(`${prefix}-reload`, arrival.conversation.assistant_message_id);
    await page.locator('#personalAssistantClose').click();
    await page.locator('#personalAssistantLauncher').click();
    await capture(`${prefix}-reopen`, arrival.conversation.assistant_message_id);
    // Fresh explicit select recovers process-local authority for a Review;
    // reloading history itself did not scan or prepare an offer.
    await page.locator('#personalAssistantFolderChip').click();
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    await page.locator('#personalAssistantExplorerBack').click();
    const directReview = page.getByRole('button', { name: 'Review workspace setup', exact: true });
    const offersBefore = (await (await request.get('/api/personal-assistant/folder-digest')).json())
      .folder_digest;
    expect(offersBefore.offer).toBeFalsy();
    await directReview.click();
    const chooser = page.locator('#personalAssistantFolderSetupCandidate');
    await expect(chooser).toHaveValue('');
    const candidate = selected.projects.find((p: any) => p.name === 'Albums');
    await chooser.selectOption(candidate.id);
    await page.locator('#personalAssistantFolderSetupReview').click();
    await expect(
      page.locator('#homeAssistantConversation #personalAssistantFolderOffer')
    ).toBeVisible();
    await capture(`${prefix}-active-review`);
    const current = await page.evaluate(() =>
      (window as any).PersonalAssistantFolderContext.current()
    );
    const review = (
      await (
        await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
      ).json()
    ).folder_reviews[current.offerId];
    expect(review.status).toBe('pending');
    expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(
      resourcesBefore
    );
    await page
      .locator('#personalAssistantFolderOffer')
      .getByRole('button', { name: 'Keep chatting', exact: true })
      .click();
    await settled(page);
  }
  expect((await audit()).hits).toEqual([]);
  expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(
    resourcesBefore
  );
  await writeFile(
    join(evidence, 'viewport-results.json'),
    JSON.stringify(
      {
        revision: 'feature working tree; see runner log and evidence index',
        level: 'Real Ori host/store + loopback provider; Today failure only is a browser fixture',
        observations,
        provider: await audit(),
        sourceContentsNotSent: true,
        resourcesUnchanged: true,
        limitations: [
          'Native picker',
          'Vendor model quality',
          'Specialized Home/portfolio execution',
          'Manual screen reader/zoom'
        ]
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
