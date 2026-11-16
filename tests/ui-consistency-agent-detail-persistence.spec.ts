import { expect, test } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
import {
  createOwnedAgent,
  guardOwnedAgentEdits,
  ownedAgentDetail,
  recordOwnedAgent,
  removeOwnedAgent,
  type OwnedAgent
} from './helpers/ui-consistency-owned-agent';

let owned: OwnedAgent | null = null;
test.afterEach(async ({ request }, info) => {
  info.setTimeout(30_000); // Cleanup has its own budget, not the UI action's remainder.
  if (owned) await removeOwnedAgent(request, owned);
  owned = null;
});

for (const theme of ['light', 'dark'] as const) {
  test(`Owned definition profile/prompt save and cancel preserve section boundaries: ${theme}`, async ({
    page,
    request
  }, info) => {
    owned = await createOwnedAgent(request);
    const originalVersion = owned.expected.version;
    await preparePage(page, theme, 390);
    const unexpected = await guardOwnedAgentEdits(page, owned);
    await page.goto(`/agents/${owned.name}`);
    await expect(page.locator('#agentName')).toHaveText(owned.name);
    await expect(page.locator('#agentAvatar .agent-avatar--character')).toBeVisible();
    await page.locator('#editPromptBtn').click();
    const prompt = `${owned.marker}: confirmed prompt`;
    await page.locator('#editSystemPrompt').fill(prompt);
    await page.locator('#editConfigBtn').click();
    await page.locator('#editTemperature').fill('0.7');
    await page.locator('#editProfileButton').click();
    const description = `${owned.marker}: confirmed profile`;
    await page.locator('#profileDescriptionInput').fill(description);
    await page.locator('#profileSaveBtn').click();
    await expect(page.locator('#profileEditModal')).toBeHidden();
    await expect(page.locator('#agentDescription')).toHaveText(description);
    const profileSaved = await ownedAgentDetail(request, owned);
    expect(profileSaved.metadata.description).toBe(description);
    expect(profileSaved.system_prompt).toBe(`${owned.marker}: original prompt`);
    expect(profileSaved.temperature).toBe(0.6);
    await recordOwnedAgent(request, owned);
    const stale = await request.patch(`/api/agents/${owned.name}`, {
      data: {
        description: `${owned.marker}: stale write must be refused`,
        expected_version: originalVersion
      }
    });
    expect(stale.status()).toBe(409);
    expect((await ownedAgentDetail(request, owned)).metadata.description).toBe(description);
    await expect(page.locator('#editSystemPrompt')).toHaveValue(prompt);
    await expect(page.locator('#editTemperature')).toHaveValue('0.7');
    await page.locator('#savePromptBtn').click();
    await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
    expect((await ownedAgentDetail(request, owned)).system_prompt).toBe(prompt);
    await recordOwnedAgent(request, owned);
    // Discard the unrelated unsent configuration through its real confirmation.
    await page.locator('#editConfigBtn').click();
    await page
      .locator('#configDiscardConfirm')
      .getByRole('button', { name: 'Discard changes', exact: true })
      .click();
    await page.locator('#editProfileButton').click();
    await page.locator('#profileDescriptionInput').fill(`${owned.marker}: must not persist`);
    await page
      .locator('#profileEditModal')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    await page
      .locator('#profileDiscardConfirm')
      .getByRole('button', { name: 'Discard changes', exact: true })
      .click();
    await expect(page.locator('#profileEditModal')).toBeHidden();
    const cancelled = await ownedAgentDetail(request, owned);
    expect(cancelled.version).toBe(owned.expected.version);
    expect(cancelled.metadata.description).toBe(description);
    expect(cancelled.temperature).toBe(0.6);
    await page.reload();
    await expect(page.locator('#agentDescription')).toHaveText(description);
    await expect(page.locator('#promptDisplay')).toHaveText(prompt);
    await expect(page.locator('#agentAvatar .agent-avatar--character')).toBeVisible();
    await capture(
      page,
      info,
      'agent-detail-persisted',
      {
        persistence:
          'Real owned UUID definition; versioned profile/prompt PATCH, cancel/no-write, reload; no execution or provider call'
      },
      { fullPage: false }
    );
    expect(unexpected).toEqual([]);
  });
}
