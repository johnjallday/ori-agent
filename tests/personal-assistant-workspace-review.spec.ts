import { test, expect } from '@playwright/test';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

test('a restarted long conversation supplies bounded historical constraints to a tool-free workspace proposal', async ({
  request
}) => {
  const fixture = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !fixture || process.env.ORI_DISCOVERY_CONTINUITY_PHASE !== 'reopen',
    'Owned offline continuity reopen only'
  );
  const saved = JSON.parse(await readFile(join(fixture!, 'continuity-conversation.json'), 'utf8'));
  const response = await request.post('/api/home-assistant/ask', {
    data: {
      prompt: 'Prepare a workspace review for this membership idea',
      intent: 'assistant_conversation',
      conversation: { id: saved.id },
      context: { origin: 'personal_assistant_panel', page_path: '/' }
    }
  });
  const prepared = await response.json();
  expect(prepared.conversation.id).toBe(saved.id);
  expect(prepared.conversation.stored).toBe(true);
  expect(prepared.conversation.continuity.recap_used).toBe(true);
  expect(prepared.confirmation.action_type).toBe('prepare_workspace');
  const audit = JSON.parse(await readFile(join(fixture!, 'workspace-proposal-input.json'), 'utf8'));
  expect(audit.tool_free).toBe(true);
  expect(audit.membership_present).toBe(true);
  expect(audit.correction_present).toBe(true);
  expect(audit.early_correction_present).toBe(true);
  expect(audit.latest_correction_present).toBe(true);
  expect(audit.history_runes).toBeLessThanOrEqual(24000);
  // No UI action or final Create occurs in this continuity-only fixture.
  expect(((await (await request.get('/api/workspaces')).json()).folders || []).length).toBe(
    saved.before
  );
  await writeFile(
    join(process.env.ORI_ASSISTANT_EVIDENCE_DIR!, 'workspace-proposal-continuity-outcome.json'),
    JSON.stringify(
      {
        evidence:
          'actual restarted host and canonical Session, deterministic tool-free system model; not live reasoning quality',
        ...audit,
        recap_used: prepared.conversation.continuity.recap_used,
        workspace_created: false
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});

// Actual Route/Ask/system-provider/Session/manual creator owners; deterministic
// model only. No public research, installer, native tools or paid model.
test('discussion becomes a bounded editable workspace proposal, then actual creation only after review', async ({
  page,
  request
}) => {
  const fixture = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(
    !fixture || !process.env.ORI_DISCOVERY_DISCUSSION_SANDBOX,
    'Use the isolated discovery-discussion runner with this extra spec'
  );
  test.setTimeout(120_000);
  const evidence = process.env.ORI_ASSISTANT_EVIDENCE_DIR!;
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  const folders = async () => (await (await request.get('/api/workspaces')).json()).folders;
  const before = (await folders()).length;
  const personal = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const profileBefore = await (await request.get('/api/user/profile')).json();
  const memoryBefore = await (
    await request.get(`/api/workspaces/${personal.hq_workspace_id}/memory`)
  ).json();
  const writes: string[] = [];
  const builds: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (request.method() === 'POST' && path === '/api/workspaces') writes.push(path);
    if (request.method() === 'POST' && path.startsWith('/api/workspaces/build-sessions'))
      builds.push(path);
  });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantConversation));
  await page.locator('#personalAssistantLauncher').click();
  const drawer = page.locator('#personalAssistantPanel');
  const input = page.locator('#personalAssistantInput');
  const send = async (prompt: string) => {
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
  const discussion = await send('I want recurring community membership, not talent coaching.');
  const id = discussion.conversation.id;
  const offered = await send('Suggest a useful next step for our membership plan.');
  expect(offered.response).toContain('Would you like a workspace proposal?');
  expect(offered.requires_confirmation).not.toBe(true);
  const policy = JSON.parse(await readFile(join(fixture!, 'workspace-offer-input.json'), 'utf8'));
  expect(policy.optional_review_allowed).toBe(true);
  expect(policy.decline_respected).toBe(true);
  const ambiguous = await send('yes');
  expect(ambiguous.requires_confirmation).not.toBe(true);
  expect(writes).toHaveLength(0);
  const prepared = await send('Prepare a workspace review for this idea');
  expect(prepared.conversation.id).toBe(id);
  expect(prepared.conversation.stored).toBe(true);
  expect(prepared.confirmation.action_type).toBe('prepare_workspace');
  expect(prepared.confirmation.arguments.description).toContain('not talent coaching');
  const audit = JSON.parse(await readFile(join(fixture!, 'workspace-proposal-input.json'), 'utf8'));
  expect(audit.tool_free).toBe(true);
  expect(audit.correction_present).toBe(true);
  expect(audit.membership_present).toBe(true);
  expect(audit.history_runes).toBeLessThanOrEqual(24000);
  expect(await folders()).toHaveLength(before);
  await input.fill('Keep my unsent follow-up about membership pricing.');
  const reviewButton = drawer.getByRole('button', { name: 'Review workspace setup', exact: true });
  await drawer
    .locator(`[data-message-id="${prepared.conversation.assistant_message_id}"]`)
    .scrollIntoViewIfNeeded();
  await drawer.screenshot({ path: join(evidence, 'workspace-proposal-in-conversation.png') });
  await reviewButton.scrollIntoViewIfNeeded();
  await reviewButton.click();
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  expect(builds).toHaveLength(0);
  await expect(page.locator('#workspaceBuildPane')).toBeHidden();
  await expect(page.locator('#folderNameInput')).toHaveValue('Fictional Membership Pilot');
  await expect(page.locator('#folderDescriptionInput')).toHaveValue(
    prepared.confirmation.arguments.description
  );
  expect(writes).toHaveLength(0);
  expect(await folders()).toHaveLength(before);
  await modal.getByRole('button', { name: 'Close create workspace', exact: true }).click();
  await expect(modal).toBeHidden();
  await page.locator('#personalAssistantLauncher').click();
  await expect(input).toHaveValue('Keep my unsent follow-up about membership pricing.');
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    id
  );
  expect(writes).toHaveLength(0);

  // A fresh explicit preparation still does not create. The ordinary wizard
  // owns blueprint, placement, team and the final human Create control.
  const next = await send("Let's set up a workspace for this plan");
  expect(next.confirmation.action_type).toBe('prepare_workspace');
  await drawer.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await expect(modal).toBeVisible();
  const name = 'Fictional Membership — reviewed';
  const description = `${next.confirmation.arguments.description}\nUser-reviewed edit: a small pilot first.`;
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#folderDescriptionInput').fill(description);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  // This smoke explicitly chooses the existing empty-team exception; no
  // profile, capability binding or runnable starter task is silently added.
  await modal.getByRole('checkbox', { name: /Create without agents/ }).check();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  expect(writes).toHaveLength(0);
  await modal.screenshot({ path: join(evidence, 'workspace-proposal-final-review.png') });
  await page.locator('#createFolderBtn').click();
  await expect.poll(async () => (await folders()).length).toBe(before + 1);
  const created = (await folders()).find((folder: any) => folder.name === name);
  expect(created.description).toBe(description);
  expect(writes).toHaveLength(1);
  expect(builds).toHaveLength(0);
  expect(created.agents || []).toHaveLength(0);
  expect(created.tasks || []).toHaveLength(0);
  expect(await (await request.get('/api/user/profile')).json()).toEqual(profileBefore);
  expect(
    await (await request.get(`/api/workspaces/${personal.hq_workspace_id}/memory`)).json()
  ).toEqual(memoryBefore);
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    id
  );
  await writeFile(
    join(evidence, 'workspace-proposal-creation-outcome.json'),
    JSON.stringify(
      {
        evidence:
          'actual host/Session/manual creator owners, deterministic system model; not live reasoning quality',
        canonical_thread_preserved: true,
        edited_brief_preserved: true,
        actual_workspace_writes: writes.length,
        builder_session_calls: builds.length,
        no_implicit_agents_or_tasks: true,
        profile_and_reviewed_memory_unchanged: true
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
