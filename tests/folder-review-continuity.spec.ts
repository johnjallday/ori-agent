import { test, expect } from '@playwright/test';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { chooseAttach, chooseConversation } from './helpers/assistant-menus';

// Two explicit phases against the same disposable wt demo sandbox. The caller
// restarts only its owned server between phases; no user folders or models are
// used. Normal smoke runs skip this isolated lifecycle acceptance.
const sandbox = process.env.ORI_FOLDER_REVIEW_CONTINUITY_SANDBOX;
const phase = process.env.ORI_FOLDER_REVIEW_CONTINUITY_PHASE;
const evidence = resolve('tasks/evidence-folder-review-continuity');

test('an opened folder review survives a server restart without a new pick', async ({
  page,
  request
}) => {
  test.skip(
    !sandbox || !['seed', 'reopen'].includes(phase || ''),
    'requires an isolated demo and explicit phase'
  );
  test.setTimeout(300_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const temporaryRoot = await realpath(tmpdir());
  expect((await realpath(sandbox!)).startsWith(`${temporaryRoot}/ori-demo.`)).toBe(true);
  expect(await readFile(join(sandbox!, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath('.')}\n`
  );
  await mkdir(evidence, { recursive: true });
  const stateFile = join(sandbox!, '.folder-review-continuity-state.json');
  const carried = await readFile(stateFile, 'utf8')
    .then(JSON.parse)
    .catch(() => null);
  const api = async (method: string, path: string, data?: unknown) => {
    const response = await request.fetch(path, { method, data });
    expect(response.ok(), `${method} ${path}: ${await response.text()}`).toBeTruthy();
    return response.json();
  };
  const roots = await api('GET', '/api/settings/workspace-root');
  expect(
    (roots.effective_workspace_root || roots.default_workspace_root).startsWith(
      `${await realpath(sandbox!)}/`
    ),
    'server must belong to the supplied sandbox'
  ).toBe(true);
  if (phase === 'seed' && !carried) {
    await api('POST', '/api/onboarding/skip');
    await api('POST', '/api/settings/workspace-root', { workspace_root: '' });
    let assistant = (await api('GET', '/api/personal-assistant')).personal_assistant;
    if (assistant.state === 'needs_hire') {
      assistant = (
        await api('POST', '/api/personal-assistant/hire', {
          request_id: 'folder-continuity-hire',
          if_version: assistant.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help me plan.',
          focus_areas: ['plan_my_day']
        })
      ).personal_assistant;
      await api('POST', '/api/personal-assistant/hq', {
        request_id: 'folder-continuity-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      });
    }
  }
  await page.goto('/');
  await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
  if (!(await page.locator('#personalAssistantPanel').isVisible())) {
    await page.locator('#personalAssistantLauncher').click();
  }
  let state: { conversation: string; offer: string };
  if (phase === 'seed' && !carried) {
    await chooseAttach(page, '#personalAssistantFolderChip');
    const chooser = page.locator('#personalAssistantFolderChoices');
    await chooser.getByLabel('or paste a folder path').fill(join(sandbox!, 'Albums', 'Album-5'));
    const reviewed = page.waitForResponse(
      response =>
        response.url().endsWith('/folder-context/review') && response.request().method() === 'POST'
    );
    await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
    const response = await reviewed;
    expect(response.ok(), await response.text()).toBeTruthy();
    const body = await response.json();
    state = { conversation: body.conversation.id, offer: body.folder_context.offer_id };
    expect(state.offer).toBeTruthy();
    await writeFile(stateFile, JSON.stringify(state), { mode: 0o600 });
  } else {
    expect(carried, 'seed the sandbox before reopening').toBeTruthy();
    state = carried;
    await chooseConversation(page, '#personalAssistantConversationContinue');
    await page.locator(`button[data-conversation-id="${state.conversation}"]`).click();
  }
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Album-5');
  await expect(card.getByRole('button', { name: 'Pick it again', exact: true })).toHaveCount(0);
  if (phase === 'seed') {
    // These reviewed packages install only inside the disposable server's
    // plugin store. Each download uses its real disclosure/confirmation UI.
    for (const name of ['REAPER', 'Music Project Management']) {
      const install = card.getByRole('button', { name: `Install ${name}`, exact: true });
      if (await install.count()) {
        await install.click();
        const confirm = card.getByRole('button', { name: 'Confirm installation', exact: true });
        await expect(confirm).toBeEnabled({ timeout: 30_000 });
        await confirm.click();
        await expect(install).toHaveCount(0, { timeout: 120_000 });
      }
    }
  }
  // Explicit Review again must also work using the restored authority, while
  // preserving the original dated observation and offer reference.
  const recheck = card.getByRole('button', { name: 'Check again', exact: true });
  if (await recheck.count()) {
    const refreshed = page.waitForResponse(
      response =>
        response.url().endsWith('/folder-context/review') && response.request().method() === 'POST'
    );
    await recheck.click();
    const response = await refreshed;
    expect(response.ok(), await response.text()).toBeTruthy();
    if (phase === 'reopen') {
      const fresh = await response.json();
      expect(fresh.folder_context.historical).toBe(true);
      expect(fresh.folder_context.offer_id).toBe(state.offer);
    }
  }
  const stored = await api('GET', `/api/home-assistant/conversations/${state.conversation}`);
  const offer = stored.folder_reviews[state.offer];
  expect(offer.id).toBe(state.offer);
  expect(offer.needs_pick).not.toBe(true);
  expect(stored.folder_review_context.status).toBe('awaiting_confirmation');
  expect(stored.folder_review_context.historical).not.toBe(true);
  const setupButton = card
    .getByRole('button', { name: /set up|hire and start|^Add .* to /i })
    .first();
  await setupButton.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, `conversation-${phase}.png`) });
  // A complete plan depends on the reviewed REAPER/Music packages. Do not call
  // their absence a passed Set up demo; the screenshot still records continuity.
  expect(offer.plan, 'reviewed integration packages must make Set up reachable').toBeTruthy();
  if (phase === 'reopen') {
    const button = setupButton;
    await expect(button).toBeEnabled();
    await button.click();
    await card.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    const confirm = card.locator('[data-placement-action="confirm"]');
    await expect(confirm).toBeEnabled();
    const started = page.waitForResponse(
      response => response.url().endsWith('/setup') && response.request().method() === 'POST'
    );
    await confirm.click();
    const response = await started;
    expect(response.ok(), await response.text()).toBeTruthy();
    await expect
      .poll(async () => {
        const current = await api('GET', `/api/home-assistant/conversations/${state.conversation}`);
        return current.folder_reviews[state.offer]?.setup?.status;
      })
      .toBe('stopped');
    const statusButton = card.getByRole('button', { name: 'Read saved status', exact: true });
    if (await statusButton.count()) await statusButton.click();
    await page.screenshot({ path: join(evidence, 'conversation-run-started.png') });
  }
});
