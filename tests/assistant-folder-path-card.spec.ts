import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

// A folder path written to the assistant comes back as one card on the reply,
// and confirming it links the folder without the browser ever sending the path.
//
// The sandbox has no model, so the assistant's two endpoints are answered here
// with the shapes the server returns. Everything the card touches is real: the
// folder on disk, the selection the server issued for it, the link, and the
// workspace's own list of linked folders.
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_FOLDER_CARD_SANDBOX tests/assistant-folder-path-card.spec.ts

const sandbox = process.env.ORI_FOLDER_CARD_SANDBOX || '';

test('a pasted folder path becomes one card, and confirming it links the folder', async ({
  page,
  request,
  baseURL
}) => {
  test.skip(!sandbox, 'needs the sandbox path: --sandbox-env ORI_FOLDER_CARD_SANDBOX');
  const folder = join(sandbox, 'Music', 'Card Stems');
  await mkdir(folder, { recursive: true, mode: 0o750 });
  await writeFile(join(folder, 'take-one.txt'), 'first take', { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-card-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me with my songs.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-card-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const created = await request.post('/api/workspaces', { data: { name: 'Card Song' } });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await (await request.get('/api/workspaces')).json()).folders.find(
    (row: { name: string }) => row.name === 'Card Song'
  );
  expect(workspace?.id).toBeTruthy();

  // The selection the server issues for this folder and this workspace: what
  // the assistant's reply would carry. The typed-path rules apply to it.
  const selection = await request.post('/api/folder-picker/select-path', {
    headers: { Origin: baseURL || '' },
    data: { workspace_id: workspace.id, title: 'Link a folder', path: folder }
  });
  expect(selection.ok(), await selection.text()).toBeTruthy();
  const token = (await selection.json()).selection_token as string;
  expect(token).toBeTruthy();
  expect(token).not.toContain('Card Stems');

  const conversation = { id: 'folder-card-conversation', stored: true, title: 'Card Stems' };
  const prompts: string[] = [];
  await page.route('**/api/home-assistant/route', route =>
    route.fulfill({
      json: {
        intent: 'assistant_conversation',
        route_mode: 'home_inline',
        target_surface: 'current'
      }
    })
  );
  await page.route('**/api/home-assistant/ask', async route => {
    const prompt = String(route.request().postDataJSON()?.prompt || '');
    prompts.push(prompt);
    const turn = prompts.length;
    const reply = {
      intent: 'assistant_conversation',
      conversation: {
        ...conversation,
        user_message_id: `user-${turn}`,
        assistant_message_id: `assistant-${turn}`
      }
    };
    if (turn === 1) {
      await route.fulfill({
        json: {
          ...reply,
          response:
            "I can read Card Stems once it is linked to Card Song. Confirm the card below and I'll take it from there.",
          folder_proposal: {
            kind: 'link',
            selection_token: token,
            folder_name: 'Card Stems',
            path: folder,
            workspace_id: workspace.id,
            workspace_name: 'Card Song'
          }
        }
      });
      return;
    }
    await route.fulfill({
      json: { ...reply, response: 'Card Stems holds one file: take-one.txt.' }
    });
  });

  // The drawer is the same on every page. Home is used because a workspace
  // with no agent yet opens its own setup dialog over the page.
  await page.goto('/');
  await page.waitForFunction(() =>
    Boolean(
      (window as unknown as { PersonalAssistantFolderProposal?: unknown })
        .PersonalAssistantFolderProposal
    )
  );
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await page
    .locator('#personalAssistantInput')
    .fill(`My stems are in ${folder}. What is in there?`);
  await page.locator('#personalAssistantSend').click();

  const card = page.locator('.personal-assistant-folder-proposal');
  await expect(card).toHaveCount(1);
  await expect(card.getByRole('heading', { name: 'Link Card Stems to Card Song?' })).toBeVisible();
  await expect(card.locator('.personal-assistant-folder-proposal__path')).toHaveText(folder);
  await expect(card).toContainText('Nothing is moved or changed.');
  await expect(card.getByRole('button')).toHaveCount(1);

  // Nothing is linked until the card is confirmed.
  const linkedNames = async (): Promise<string[]> => {
    const body = await (await request.get(`/api/workspaces/${workspace.id}/directories`)).json();
    return (body.directories || []).map((row: { name: string }) => row.name);
  };
  expect(await linkedNames()).not.toContain('Card Stems');

  const linked = page.waitForRequest(
    candidate =>
      candidate.method() === 'POST' &&
      new URL(candidate.url()).pathname === `/api/workspaces/${workspace.id}/directories`
  );
  await card.getByRole('button', { name: 'Link folder', exact: true }).click();
  // The confirmation is the server's selection. The path never leaves the page.
  expect((await linked).postDataJSON()).toEqual({ selection_token: token });

  await expect(card).toContainText('Linked Card Stems to Card Song.');
  await expect(card.getByRole('button')).toHaveCount(0);
  await expect.poll(linkedNames).toContain('Card Stems');

  // The assistant carries on by itself, with the turn the user would have sent.
  await expect
    .poll(() => prompts)
    .toEqual([
      `My stems are in ${folder}. What is in there?`,
      'I linked Card Stems. Take a look and answer my last message.'
    ]);
  const transcript = page.locator('#homeAssistantConversation');
  await expect(transcript).toContainText(
    'I linked Card Stems. Take a look and answer my last message.'
  );
  await expect(transcript).toContainText('Card Stems holds one file: take-one.txt.');

  // One press, one link: the card's button is gone, so it cannot link twice.
  expect((await linkedNames()).filter(name => name === 'Card Stems')).toHaveLength(1);
});
