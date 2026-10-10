import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

test('wt demo: bounded historical context survives an actual isolated server restart', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_DISCOVERY_CONTINUITY_SANDBOX;
  const fixture = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !fixture, 'Use scripts/assistant-workspace-demo.py --discovery-continuity');
  test.setTimeout(210_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence =
    process.env.ORI_ASSISTANT_EVIDENCE_DIR ||
    join(process.cwd(), 'tasks/evidence/assistant-conversation-discovery/group-3');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const stateFile = join(fixture!, 'continuity-conversation.json');
  const phase = process.env.ORI_DISCOVERY_CONTINUITY_PHASE || 'seed';
  let saved: any;
  if (phase === 'seed') {
    await request.post('/api/onboarding/skip');
    for (const [path, data] of [
      ['/api/settings/workspace-root', { workspace_root: '' }],
      ['/api/settings/system-model', { provider: 'ollama', model: 'ori-workspace-fixture' }]
    ] as const)
      expect((await request.post(path, { data })).ok()).toBeTruthy();
    let state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'continuity-hire',
        if_version: state.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Discuss ideas and evidence, not automatic setup.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    state = (await hire.json()).personal_assistant;
    expect(
      (
        await request.post('/api/personal-assistant/hq', {
          data: {
            request_id: 'continuity-hq',
            if_version: state.state_version,
            name: 'Fictional continuity HQ',
            timezone: 'UTC'
          }
        })
      ).status()
    ).toBe(201);
    // Startup intentionally re-seeds an empty profile and updates its stamp.
    // Seed reviewed fictional fixture data once so the restart assertion stays
    // strict (including updated_at), rather than masking possible mutations.
    const existing = (await (await request.get('/api/user/profile')).json()).profile;
    expect(
      (
        await request.put('/api/user/profile', {
          data: { ...existing, display_name: 'Fictional Jules' }
        })
      ).ok()
    ).toBeTruthy();
  } else {
    saved = JSON.parse(await readFile(stateFile, 'utf8'));
  }
  const personal = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const memoryPath = `/api/workspaces/${personal.hq_workspace_id}/memory`;
  const profileBefore = await (await request.get('/api/user/profile')).json();
  const memoryBefore = await (await request.get(memoryPath)).json();
  const count = async () =>
    ((await (await request.get('/api/workspaces')).json()).folders || []).length;
  const before = await count();
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
  await page.locator('#personalAssistantLauncher').click();
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
    expect(response.status(), await response.text()).toBe(200);
    const body = await response.json();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    expect(body.conversation.stored).toBe(true);
    return body;
  };
  let id: string;
  if (phase === 'seed') {
    const first = await send(
      "My goal is community membership. No, I do not want to develop anyone's talent."
    );
    id = first.conversation.id;
    // Current user turns through real Ask/canonical saves, not SQL seeding or a
    // browser-supplied transcript. These push the early correction beyond BOTH
    // previous limits; responses and recaps use the actual Ollama adapter.
    for (let i = 0; i < 45; i++) {
      const response = await request.post('/api/home-assistant/ask', {
        data: {
          prompt:
            `Continuity fixture topic ${i}. ` +
            'A separate gardening discussion with recipes and seasonal observations. '.repeat(28),
          intent: 'assistant_conversation',
          conversation: { id },
          context: { origin: 'personal_assistant_panel', page_path: '/settings' }
        }
      });
      expect(response.ok(), await response.text()).toBeTruthy();
      expect((await response.json()).conversation.stored).toBe(true);
    }
    await send('No, I want recurring membership only, not one-off sales or talent coaching.');
    const continued = await send('Return to community membership options.');
    expect(continued.conversation.id).toBe(id);
    expect(continued.conversation.continuity.recap_used).toBe(true);
    expect(continued.conversation.continuity.older_omitted).toBe(true);
    const audit = JSON.parse(await readFile(join(fixture!, 'continuity-input.json'), 'utf8'));
    expect(audit.early_correction_present).toBe(true);
    expect(audit.latest_correction_present).toBe(true);
    expect(audit.recap_user_role).toBe(true);
    expect(audit.history_runes).toBeLessThanOrEqual(24000);
    await writeFile(stateFile, JSON.stringify({ id, before, profileBefore, memoryBefore }), {
      mode: 0o600
    });
    await drawer.screenshot({ path: join(evidence, 'early-correction-recap.png') });
    const notice = page.locator('#personalAssistantConversationNote');
    await expect(notice).toContainText('source-grounded historical references');
    await notice.scrollIntoViewIfNeeded();
    await drawer.screenshot({ path: join(evidence, 'continuity-notice.png') });
  } else {
    id = saved.id;
    expect(
      await page.evaluate(async id => (window as any).PersonalAssistantConversation.resume(id), id)
    ).toBe(true);
    const continued = await send('Return to community membership options after reopening.');
    expect(continued.conversation.id).toBe(id);
    expect(continued.conversation.continuity.recap_used).toBe(true);
    const audit = JSON.parse(await readFile(join(fixture!, 'continuity-input.json'), 'utf8'));
    expect(audit.early_correction_present).toBe(true);
    expect(audit.recap_user_role).toBe(true);
    expect(audit.history_runes).toBeLessThanOrEqual(24000);
    await drawer.screenshot({ path: join(evidence, 'reopened-after-restart.png') });
    await page.evaluate(() => (window as any).PersonalAssistantConversation.startNew());
    const fresh = await send('A clean new discussion about cooking.');
    expect(fresh.conversation.id).not.toBe(id);
    expect(fresh.conversation.continuity?.recap_used).not.toBe(true);
    const clean = JSON.parse(await readFile(join(fixture!, 'continuity-input.json'), 'utf8'));
    expect(clean.recap_present).toBe(false);
    expect(clean.early_correction_present).toBe(false);
    await drawer.screenshot({ path: join(evidence, 'new-thread-clean.png') });
    await writeFile(
      join(evidence, 'continuity-outcomes.json'),
      JSON.stringify(
        {
          evidence_kind:
            'actual built host and Ollama adapter; deterministic model, not reasoning quality',
          actual_server_restart: true,
          early_correction_in_provider_input: audit.early_correction_present,
          recap_user_role: audit.recap_user_role,
          history_runes: audit.history_runes,
          new_thread_clean: true,
          no_extra_workspace: true,
          no_memory_write: true
        },
        null,
        2
      ),
      { mode: 0o600 }
    );
    expect(await count()).toBe(saved.before);
    expect(await (await request.get('/api/user/profile')).json()).toEqual(saved.profileBefore);
    expect(await (await request.get(memoryPath)).json()).toEqual(saved.memoryBefore);
  }
  expect(await count()).toBe(before);
  expect(await (await request.get('/api/user/profile')).json()).toEqual(profileBefore);
  expect(await (await request.get(memoryPath)).json()).toEqual(memoryBefore);
});
