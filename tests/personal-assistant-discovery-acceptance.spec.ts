import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

test('opt-in public smoke: the same restarted long conversation investigates, then reviews a harmless action', async ({
  page,
  request
}) => {
  const fixture = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !fixture ||
      process.env.ORI_DISCOVERY_CONTINUITY_PHASE !== 'reopen' ||
      process.env.ORI_DISCOVERY_ACCEPTANCE_PUBLIC_SMOKE !== '1',
    'Explicit public smoke on the owned restarted continuity sandbox only'
  );
  test.setTimeout(150_000);
  const evidence = process.env.ORI_ASSISTANT_EVIDENCE_DIR!;
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const saved = JSON.parse(await readFile(join(fixture!, 'continuity-conversation.json'), 'utf8'));
  const id = saved.id;
  const personal = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const count = async () =>
    ((await (await request.get('/api/workspaces')).json()).folders || []).length;
  const skillsBefore = await (await request.get('/api/skills')).json();
  await installLocalCdn(page);
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
  await page.locator('#personalAssistantLauncher').click();
  expect(
    await page.evaluate(async id => (window as any).PersonalAssistantConversation.resume(id), id)
  ).toBe(true);
  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  const send = async (prompt: string) => {
    await expect(input).toBeEnabled();
    await input.fill(prompt);
    const pending = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await page.locator('#personalAssistantSend').click();
    const response = await pending;
    expect(response.ok(), await response.text()).toBe(true);
    const result = await response.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    return result;
  };
  const draft = 'Keep my unsent correction: membership, not talent coaching.';
  const reviews: any[] = [];
  const outcomes: any[] = [];
  const lookup = async (prompt: string, operation: string) => {
    const proposed = await send(prompt);
    expect(proposed.conversation.id).toBe(id);
    expect(proposed.research_review.lookup.operation).toBe(operation);
    reviews.push(proposed.research_review);
    const box = drawer.getByRole('region', { name: 'Review exact public lookup' }).last();
    await expect(box).toBeVisible();
    await input.fill(draft);
    const pending = page.waitForResponse(
      response => new URL(response.url()).pathname === '/api/home-assistant/ask'
    );
    await box.getByRole('button', { name: 'Approve exact lookup' }).click();
    const response = await pending;
    expect(response.ok(), await response.text()).toBe(true);
    const result = await response.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(result.conversation.id).toBe(id);
    expect(result.conversation.stored).toBe(true);
    await expect(input).toHaveValue(draft);
    outcomes.push({
      operation,
      result: result.research_result,
      sources: result.workspace_context?.research || []
    });
    return result;
  };
  const catalog = await lookup(
    'Is there a Telegram skill for community membership? No talent coaching.',
    'skills_catalog'
  );
  expect(['available', 'empty', 'partial', 'unavailable', 'malformed_output']).toContain(
    catalog.research_result.availability
  );
  for (const candidate of catalog.research_result.candidates || []) {
    expect(candidate.receipt.level).toBe('metadata');
    expect(candidate.readiness.verified).toBe('unknown');
    expect(candidate.readiness.granted).toBe('unknown');
  }
  // Public transport/extraction smoke, not evidence of Telegram compatibility.
  const document = await lookup(
    'Inspect this public document for a bounded source-read smoke only: https://example.com/',
    'public_document'
  );
  if (document.research_result.availability === 'available') {
    expect(document.research_result.candidates).toHaveLength(1);
    const receipt = document.research_result.candidates[0].receipt;
    expect(receipt.level).toBe('document');
    expect(document.workspace_context.research[0].level).toBe('document');
    expect(Array.from(receipt.excerpt).length).toBeLessThanOrEqual(4000);
    expect(receipt.content_hash).toMatch(/^[a-f0-9]{64}$/);
    expect(receipt.url).toBe('https://example.com/');
  } else {
    expect(document.research_result.candidates).toHaveLength(0);
    expect(document.workspace_context?.research || []).toHaveLength(0);
  }
  if (document.workspace_context?.research?.length) {
    await drawer.locator('.personal-assistant-research__sources').last().locator('summary').click();
  }
  await drawer.locator('.personal-assistant-research__note').last().scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'final-actual-document-outcome.png') });
  const unavailable = await lookup(
    'Inspect this exact public document missing-page smoke: https://skills.sh/ori-public-document-smoke-not-found',
    'public_document'
  );
  expect(unavailable.research_result.availability).toBe('unavailable');
  expect(unavailable.research_result.candidates).toHaveLength(0);
  expect(unavailable.workspace_context?.research || []).toHaveLength(0);
  await drawer.locator('.personal-assistant-research__note').last().scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'final-unavailable-source.png') });
  const metadata = await send('Could you compare two cached MCP capabilities?');
  expect(metadata.conversation.id).toBe(id);
  expect(metadata.research_result.candidates.length).toBeGreaterThanOrEqual(2);
  expect(metadata.research_result.scope).toContain('no external refresh');
  for (const candidate of metadata.research_result.candidates)
    expect(candidate.readiness.verified).toBe('unknown');
  const recalled = await send('Return to community membership options after the investigation.');
  expect(recalled.conversation.id).toBe(id);
  expect(recalled.conversation.continuity.recap_used).toBe(true);
  const audit = JSON.parse(await readFile(join(fixture!, 'continuity-input.json'), 'utf8'));
  expect(audit.early_correction_present).toBe(true);
  expect(audit.latest_correction_present).toBe(true);
  expect(audit.history_runes).toBeLessThanOrEqual(24000);
  await drawer.screenshot({ path: join(evidence, 'final-same-thread-recall.png') });
  const creation = await send('Create a workspace called Fictional Community');
  expect(creation.requires_confirmation).toBe(true);
  await drawer.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(await count()).toBe(saved.before);
  await input.fill(draft);
  const tickets = async () =>
    (
      await (
        await request.get(
          `/api/workspaces/${personal.hq_workspace_id}/tickets?source=assistant&archive=all&limit=200`
        )
      ).json()
    ).tickets || [];
  const beforeTickets = (await tickets()).length;
  const reply = drawer.locator('[data-message-role="assistant"][data-message-id]').last();
  const messageID = await reply.getAttribute('data-message-id');
  const canonical = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
  const sourceText = canonical.messages.find((message: any) => message.id === messageID).content;
  await reply.locator('summary').filter({ hasText: 'Message actions' }).click();
  await reply.locator('[data-message-action="save-draft"]').click();
  const review = page.locator('#personalAssistantDraftReview');
  await expect(review).toBeVisible();
  expect((await tickets()).length).toBe(beforeTickets);
  await page
    .locator('#personalAssistantDraftTitle')
    .fill('Fictional acceptance — explicitly reviewed');
  await page.locator('#personalAssistantDraftSave').click();
  await expect(review.locator('[data-draft-view="receipt"]')).toBeVisible();
  const afterTickets = await tickets();
  expect(afterTickets.length).toBe(beforeTickets + 1);
  expect(
    afterTickets.find(
      (ticket: any) => ticket.title === 'Fictional acceptance — explicitly reviewed'
    ).description
  ).toBe(sourceText);
  await drawer.screenshot({ path: join(evidence, 'final-explicit-review-receipt.png') });
  await expect(input).toHaveValue(draft);
  expect(await count()).toBe(saved.before);
  expect(await (await request.get('/api/skills')).json()).toEqual(skillsBefore);
  expect(await (await request.get('/api/user/profile')).json()).toEqual(saved.profileBefore);
  expect(
    await (await request.get(`/api/workspaces/${personal.hq_workspace_id}/memory`)).json()
  ).toEqual(saved.memoryBefore);
  const history = JSON.stringify(
    (await (await request.get(`/api/home-assistant/conversations/${id}`)).json()).messages
  );
  for (const review of reviews) {
    expect(history).not.toContain(review.token);
    expect(history).not.toContain(review.digest);
  }
  // Actual no-model production gate, not an injected browser response. This
  // changes only this invocation's disposable settings and restores them.
  expect(
    (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
  ).toBe(true);
  try {
    await page.reload();
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
    if (!(await drawer.isVisible())) await page.locator('#personalAssistantLauncher').click();
    await expect(
      drawer.getByRole('link', { name: 'Configure a model for conversational replies' })
    ).toBeVisible();
    await expect(input).toHaveValue(draft);
    const refused = await request.post('/api/home-assistant/ask', {
      data: {
        prompt: 'Continue this discussion without a model.',
        intent: 'assistant_conversation',
        conversation: { id },
        context: { origin: 'personal_assistant_panel', page_path: '/' }
      }
    });
    expect((await refused.json()).model_unavailable).toBe(true);
    expect(
      JSON.stringify(
        (await (await request.get(`/api/home-assistant/conversations/${id}`)).json()).messages
      )
    ).toBe(history);
    await drawer.screenshot({ path: join(evidence, 'final-model-unavailable.png') });
  } finally {
    expect(
      (
        await request.post('/api/settings/system-model', {
          data: { provider: 'ollama', model: 'ori-workspace-fixture' }
        })
      ).ok()
    ).toBe(true);
  }
  await writeFile(
    join(evidence, 'final-acceptance-outcomes.json'),
    JSON.stringify(
      {
        evidence_kind:
          'actual restarted host/source/Session owners; deterministic model, not live reasoning quality',
        same_long_thread: true,
        actual_source_outcomes: outcomes,
        recap_user_role: audit.recap_user_role,
        history_runes: audit.history_runes,
        no_integration_install: true,
        no_extra_workspace: true,
        no_memory_write: true,
        explicit_ticket_receipt: true,
        actual_unavailable_source: true,
        actual_model_unavailable: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
