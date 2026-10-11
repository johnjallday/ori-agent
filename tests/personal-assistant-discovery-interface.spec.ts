import { test, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Opt-in built-host evidence. Only model decisions are a loopback fixture;
// the edited harmless public query runs through the real source/review owners.
test('wt demo: evidence, Help, manual setup navigation and an explicit draft review', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_DISCOVERY_INTERFACE_SANDBOX;
  test.skip(!sandbox, 'Use scripts/assistant-workspace-demo.py --discovery-interface');
  test.setTimeout(210_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence/assistant-conversation-discovery/group-4');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'interface-hire',
      if_version: state.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Discuss evidence before reviewed action.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  state = (await hired.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'interface-hq',
      if_version: state.state_version,
      name: 'Fictional interface HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  state = (await hq.json()).personal_assistant;
  const tickets = async () =>
    (
      await (
        await request.get(
          `/api/workspaces/${state.hq_workspace_id}/tickets?source=assistant&archive=all&limit=200`
        )
      ).json()
    ).tickets || [];
  const count = async () =>
    ((await (await request.get('/api/workspaces')).json()).folders || []).length;
  const before = await count();
  const skillsBefore = await (await request.get('/api/skills')).json();
  const memoryBefore = await (
    await request.get(`/api/workspaces/${state.hq_workspace_id}/memory`)
  ).json();
  await installLocalCdn(page);
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantResearch));
  await page.locator('#personalAssistantLauncher').click();
  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  // Same pinned scoped accessibility audit used by delivered Help coverage.
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const audit = async (selector: string) => {
    const violations = await page.evaluate(
      async selector =>
        (
          await (window as any).axe.run(document.querySelector(selector), {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations,
      selector
    );
    expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
  };
  let asks = 0;
  page.on('request', req => {
    if (new URL(req.url()).pathname === '/api/home-assistant/ask') asks++;
  });
  const send = async (prompt: string) => {
    await expect(input).toBeEnabled();
    await input.fill(prompt);
    const pending = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const response = await pending;
    expect(response.status(), await response.text()).toBe(200);
    const data = await response.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    return data;
  };
  const cached = await send('Could you compare two cached MCP capabilities?');
  const id = cached.conversation.id;
  expect(cached.research_result.candidates.length).toBeGreaterThanOrEqual(2);
  await expect(
    drawer.getByRole('list', { name: 'Candidate evidence and independent readiness' }).last()
  ).toBeVisible();
  await expect(drawer.locator('.personal-assistant-research__capability').last()).toContainText(
    'ollama / ori-workspace-fixture'
  );
  await drawer.locator('.personal-assistant-research__candidate').nth(1).scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'compiled-candidate-comparison.png') });
  await audit('.personal-assistant-research__candidates');
  await audit('.personal-assistant-research__setup');
  const draft = 'Keep my exact unsent correction: no talent coaching 🦊';
  await input.fill(draft);
  const browse = drawer.getByRole('link', { name: 'Browse MCP configuration (new tab)' }).first();
  await expect(browse).toHaveAttribute('href', '/mcp');
  await expect(browse).toHaveAttribute('rel', 'noopener noreferrer');
  const popup = page.waitForEvent('popup');
  await browse.click();
  const setup = await popup;
  await setup.waitForURL('**/mcp');
  await setup.close();
  await expect(input).toHaveValue(draft);
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    id
  );
  const proposed = await send('Is there a Telegram community-management skill?');
  const review = drawer.locator('.personal-assistant-research__review').last();
  await input.fill(draft);
  const asksBeforeHelp = asks;
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.locator('#oriGuideInput').fill('Where are skills?');
  await page.locator('#oriGuideInput').press('Enter');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', /.+/);
  await page.locator('#personalAssistantLauncher').click();
  await expect(input).toHaveValue(draft);
  expect(asks).toBe(asksBeforeHelp);
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    id
  );
  await expect(review.getByLabel('Exact public query')).toHaveValue(
    'Telegram community management'
  );
  await review.getByLabel('Exact public query').fill('community');
  const prepared = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/research/review'
  );
  await review.getByRole('button', { name: 'Review edited lookup', exact: true }).click();
  expect((await prepared).ok()).toBe(true);
  await expect(
    review.getByRole('button', { name: 'Approve exact lookup', exact: true })
  ).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await review.scrollIntoViewIfNeeded();
  const geometry = await review.evaluate(el => ({
    width: el.getBoundingClientRect().width,
    overflow: el.scrollWidth > el.clientWidth + 1
  }));
  expect(geometry.width).toBeLessThanOrEqual(390);
  expect(geometry.overflow).toBe(false);
  await expect(review.locator('[role="status"]')).toBeVisible();
  await drawer.screenshot({ path: join(evidence, 'mobile-exact-edited-review.png') });
  await audit('.personal-assistant-research__review');
  const controls = await review
    .locator('input, button')
    .evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height));
  expect(controls.every(height => height >= 44)).toBe(true);
  await page.evaluate(() => document.getElementById('darkModeToggle')?.click());
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
  await drawer.screenshot({ path: join(evidence, 'mobile-dark-exact-review.png') });
  await audit('.personal-assistant-research__review');
  const lookup = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask'
  );
  await review.getByRole('button', { name: 'Approve exact lookup', exact: true }).focus();
  await page.keyboard.press('Enter');
  const looked = await (await lookup).json();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(looked.conversation.id).toBe(id);
  await expect(input).toHaveValue(draft);
  await expect(input).toBeFocused();
  await page.setViewportSize({ width: 1280, height: 900 });
  await drawer.locator('.personal-assistant-research__note').last().scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'actual-reviewed-query-outcome.png') });
  const workspaceReview = await send('Create a workspace called Fictional Community');
  expect(workspaceReview.requires_confirmation).toBe(true);
  await drawer.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(await count()).toBe(before);
  // Positive harmless review through the existing canonical-message/Ticket
  // owner. This is not an integration install or a global-memory write.
  await input.fill(draft);
  const replies = drawer.locator('[data-message-role="assistant"][data-message-id]');
  const reply = replies.last();
  const canonicalId = await reply.getAttribute('data-message-id');
  const history = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  const sourceText = history.messages.find((message: any) => message.id === canonicalId).content;
  const beforeTickets = (await tickets()).length;
  await reply.locator('summary').filter({ hasText: 'Message actions' }).click();
  await reply.locator('[data-message-action="save-draft"]').click();
  const draftReview = page.locator('#personalAssistantDraftReview');
  await expect(draftReview).toBeVisible();
  expect((await tickets()).length).toBe(beforeTickets);
  await page
    .locator('#personalAssistantDraftTitle')
    .fill('Fictional comparison — explicitly reviewed draft');
  await page.locator('#personalAssistantDraftSave').click();
  await expect(draftReview.locator('[data-draft-view="receipt"]')).toBeVisible();
  const saved = await tickets();
  expect(saved.length).toBe(beforeTickets + 1);
  expect(
    saved.find((ticket: any) => ticket.title === 'Fictional comparison — explicitly reviewed draft')
      .description
  ).toBe(sourceText);
  await drawer.screenshot({ path: join(evidence, 'explicit-draft-review-receipt.png') });
  await expect(input).toHaveValue(draft);
  const currentInventory = await send('Read current installed capability metadata, without setup.');
  expect(currentInventory.conversation.id).toBe(id);
  expect(currentInventory.research_result.scope).toContain(
    'installed Skills folder and registered MCP'
  );
  expect(['empty', 'available', 'partial']).toContain(
    currentInventory.research_result.availability
  );
  await input.fill(draft);
  expect(await count()).toBe(before);
  expect(await (await request.get('/api/skills')).json()).toEqual(skillsBefore);
  expect(
    await (await request.get(`/api/workspaces/${state.hq_workspace_id}/memory`)).json()
  ).toEqual(memoryBefore);
  const stored = JSON.stringify(
    (await (await request.get(`/api/home-assistant/conversations/${id}`)).json()).messages
  );
  expect(stored).not.toContain(proposed.research_review.token);
  expect(stored).not.toContain(proposed.research_review.digest);
  await writeFile(
    join(evidence, 'interface-outcomes.json'),
    JSON.stringify(
      {
        evidence_kind:
          'built host; deterministic model; reviewed actual public query, not reasoning quality',
        compiled_candidates: cached.research_result.candidates.length,
        public_availability: looked.research_result.availability,
        help_calls_no_model: true,
        same_thread: true,
        retained_draft: true,
        manual_navigation_no_install: true,
        no_extra_workspace: true,
        explicitly_saved_draft: true,
        no_memory_write: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
