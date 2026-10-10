import { test, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Deliberate harmless public-source smoke, never ordinary CI network coverage.
// The actual built host/source owners run; the loopback model is deterministic.
test('wt demo: reviewed catalog/document reads keep the thread and do not install', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_DISCOVERY_RESEARCH_SANDBOX;
  test.skip(!sandbox, 'Use scripts/assistant-workspace-demo.py --discovery-research');
  test.setTimeout(210_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence/assistant-conversation-discovery/group-2');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'discovery-research-hire',
      if_version: state.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Discuss ideas, then review useful evidence.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  state = (await hire.json()).personal_assistant;
  expect(
    (
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'discovery-research-hq',
          if_version: state.state_version,
          name: 'Fictional research HQ',
          timezone: 'UTC'
        }
      })
    ).status()
  ).toBe(201);
  const workspaceCount = async () =>
    ((await (await request.get('/api/workspaces')).json()).folders || []).length;
  const before = await workspaceCount();
  const installedBefore = await (await request.get('/api/skills')).json();
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantResearch));
  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  await page.locator('#personalAssistantLauncher').click();
  const send = async (prompt: string) => {
    await expect(input).toBeEnabled();
    await input.fill(prompt);
    const pending = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const response = await pending;
    expect(response.status(), await response.text()).toBe(200);
    const body = await response.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    return body;
  };
  const cached = await send('Could you compare cached MCP filesystem capabilities?');
  const id = cached.conversation.id;
  expect(cached.conversation.stored).toBe(true);
  expect(cached.workspace_context.research.length).toBeGreaterThan(0);
  await expect(drawer.locator('.personal-assistant-research__sources').last()).toBeVisible();
  const proposed = await send('Is there a Telegram community-management skill?');
  expect(proposed.conversation.id).toBe(id);
  expect(proposed.research_review.lookup.query).toBe('Telegram community management');
  const review = drawer.locator('.personal-assistant-research__review').last();
  await expect(review.getByLabel('Exact public query')).toHaveValue(
    'Telegram community management'
  );
  await expect(review).toContainText('does not install');
  await review
    .getByRole('button', { name: 'Approve exact lookup', exact: true })
    .scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'exact-public-query-review.png') });
  // Typed but unsent text must survive research approval and its reply.
  const draft = 'Keep this unsent correction: no talent coaching.';
  await input.fill(draft);
  const lookupResponse = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask'
  );
  await review.getByRole('button', { name: 'Approve exact lookup', exact: true }).click();
  const looked = await (await lookupResponse).json();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(looked.conversation.id).toBe(id);
  expect(looked.conversation.stored).toBe(true);
  expect(['available', 'partial', 'empty', 'unavailable', 'malformed_output']).toContain(
    looked.research_result.availability
  );
  await expect(input).toHaveValue(draft);
  for (const candidate of looked.research_result.candidates) {
    expect(candidate.readiness.granted).toBe('unknown');
    expect(candidate.readiness.verified).toBe('unknown');
  }
  const catalogSources = drawer.locator('.personal-assistant-research__sources').last();
  if (looked.workspace_context.research?.length) {
    await catalogSources.locator('summary').click();
    await catalogSources.scrollIntoViewIfNeeded();
  }
  await drawer.locator('.personal-assistant-research__note').last().scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'public-catalog-actual-outcome.png') });
  const document = await send(
    'Could you inspect the public document at https://skills.sh/ for evidence?'
  );
  expect(document.research_review.lookup.url).toBe('https://skills.sh/');
  const documentReview = drawer.locator('.personal-assistant-research__review').last();
  const documentResponse = page.waitForResponse(
    response => new URL(response.url()).pathname === '/api/home-assistant/ask'
  );
  await documentReview.getByRole('button', { name: 'Approve exact lookup', exact: true }).click();
  const inspected = await (await documentResponse).json();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(inspected.conversation.id).toBe(id);
  expect(['available', 'unavailable']).toContain(inspected.research_result.availability);
  if (inspected.research_result.availability === 'available') {
    expect(inspected.workspace_context.research[0].level).toBe('document');
    expect(inspected.research_result.candidates[0].receipt.excerpt.length).toBeLessThanOrEqual(
      4000
    );
  }
  await drawer.locator('.personal-assistant-research__note').last().scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'public-document-actual-outcome.png') });
  const cancelled = await send('Is there a Telegram community-management skill?');
  await drawer
    .locator('.personal-assistant-research__review')
    .last()
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  expect(cancelled.conversation.id).toBe(id);
  expect(await workspaceCount()).toBe(before);
  expect(await (await request.get('/api/skills')).json()).toEqual(installedBefore);
  const history = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  const text = JSON.stringify(history.messages);
  for (const reply of [proposed, document, cancelled]) {
    expect(text).not.toContain(reply.research_review.token);
    expect(text).not.toContain(reply.research_review.digest);
  }
  const outcomes = {
    evidence_kind:
      'actual public source smoke; deterministic loopback model, no live quality evaluation',
    catalog: {
      availability: looked.research_result.availability,
      reason: looked.research_result.reason,
      candidates: looked.research_result.candidates.map((c: any) => ({
        name: c.name,
        url: c.url,
        read_at: c.receipt.read_at,
        content_hash: c.receipt.content_hash,
        freshness: c.receipt.freshness
      }))
    },
    document: {
      availability: inspected.research_result.availability,
      reason: inspected.research_result.reason,
      receipts: (inspected.workspace_context.research || []).map((r: any) => ({
        url: r.url,
        level: r.level,
        read_at: r.read_at,
        content_hash: r.content_hash
      }))
    },
    no_install: true,
    same_thread: true
  };
  await writeFile(
    join(evidence, 'actual-public-source-outcomes.json'),
    JSON.stringify(outcomes, null, 2),
    { mode: 0o600 }
  );
});
