import { expect, test, type APIRequestContext } from '@playwright/test';
import { seedQuestionFolder } from './helpers/commander-question-fixture';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Run with scripts/e2e-fresh.sh --sandbox-env ORI_QUESTION_SANDBOX.
// No provider, plugin installation or real music folder needed.
const sandbox = process.env.ORI_QUESTION_SANDBOX;
const api = '/api/personal-assistant/folder-agent';
const evidence = 'tasks/evidence-commander-question-cue';

test.describe.configure({ mode: 'serial' });

async function seed(request: APIRequestContext, folder: string, music: boolean) {
  return seedQuestionFolder(request, sandbox!, folder, music);
}

test('pending question on the workspace launcher', async ({ page, request }) => {
  test.skip(!sandbox, 'requires disposable demo HOME');
  test.setTimeout(120_000);
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true });
  const music = await seed(request, 'Cue Albums', true);
  const other = await seed(request, 'Cue Research', false);
  const launcher = page.locator('#personalAssistantLauncher');
  const badge = launcher.locator('[data-question]');
  await page.addInitScript(() => {
    for (const name of ['animationstart', 'animationiteration', 'animationend']) {
      document.addEventListener(name, event => {
        const e = event as AnimationEvent;
        if (e.animationName !== 'commander-question-bob') return;
        document.documentElement.dataset.bobs =
          (document.documentElement.dataset.bobs || '') + `${e.type}:${e.elapsedTime};`;
      });
    }
  });
  const trace = () => page.evaluate(() => document.documentElement.dataset.bobs || '');
  const doubleBob = 'animationstart:0;animationiteration:0.34;animationend:0.68;';
  let songs = false;
  let noChat = false;
  // A credential-free look reports its cause first. Exercise the successful
  // look's host phase deterministically; all chat refs and actions remain real.
  await page.route(`**${api}/${music.workspace_id}`, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.folder_agent.model_sentence = '';
    if (noChat) delete body.folder_agent.chat;
    if (songs)
      Object.assign(body.folder_agent, {
        state: 'set_up',
        songs_offer: { ask: 'Learn the songs?' }
      });
    await route.fulfill({ response, json: body });
  });
  await page.goto(music.route);
  await expect(launcher).toHaveAccessibleName(/Cue Albums Manager.*Has a question for you/i);
  await expect(badge).toBeVisible();
  await expect(launcher.locator('#personalAssistantUnread')).toBeVisible();
  await page.evaluate(() => (window as any).PersonalAssistantPanel.repaintFace());
  await expect.poll(trace).toBe(doubleBob);
  await writeFile(join(evidence, 'first-motion.txt'), await trace());
  await page.screenshot({ path: join(evidence, '01-premise.png') });
  await page.reload();
  await expect(badge).toBeVisible();
  expect(await trace()).toBe('');
  await page.goto(other.route);
  await expect(launcher).toHaveAccessibleName(/Cue Research Manager/i);
  await expect(badge).toHaveCount(0);
  await expect(launcher.locator('#personalAssistantUnread')).toBeVisible();
  await page.screenshot({ path: join(evidence, '02-no-question.png') });
  await launcher.press('Space');
  await expect(
    page.locator(`#homeAssistantConversation [data-message-id="${other.chat.message_id}"]`)
  ).toBeVisible();
  await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  await page.goto(music.route);
  await expect(badge).toBeVisible();
  expect(await trace()).toBe('');
  songs = true;
  await page.reload();
  await expect.poll(trace).toBe(doubleBob);
  await writeFile(join(evidence, 'songs-motion.txt'), await trace());
  // New tab memory: a reduced-motion presentation counts even if the user
  // changes their preference before returning to the question.
  await page.evaluate(() => sessionStorage.removeItem('ori.commander.presentedQuestions.v1'));
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.reload();
  await expect(badge).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => sessionStorage.getItem('ori.commander.presentedQuestions.v1')))
    .toContain('songs');
  expect(await trace()).toBe('');
  await page.screenshot({ path: join(evidence, '03-static-reduced.png') });
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.reload();
  await expect(badge).toBeVisible();
  expect(await trace()).toBe('');

  songs = false;
  noChat = true;
  await page.reload();
  await expect(badge).toBeVisible();
  await launcher.press('Enter');
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.getByRole('button', { name: music.premise.confirm }).first()).toBeVisible();

  noChat = false;
  await page.reload();
  await expect(badge).toBeVisible();
  const draft = await request.post('/api/sessions', {
    data: { title: 'Draft', folder_id: music.workspace_id, agent_name: music.agent_name }
  });
  expect(draft.ok()).toBe(true);
  await page.evaluate(
    ({ workspace, session }) => {
      (window as any).PersonalAssistantRecipient.rememberSession(workspace, { id: session });
      const input = document.querySelector<HTMLTextAreaElement>('#personalAssistantInput')!;
      input.value = 'Keep this draft';
      input.dispatchEvent(new Event('input', { bubbles: true }));
    },
    { workspace: music.workspace_id, session: (await draft.json()).id }
  );
  await launcher.press('Enter');
  await expect(page.locator('#personalAssistantPanelStatus')).toContainText(
    'Your draft has been kept'
  );
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this draft');
  await page.locator('#personalAssistantInput').fill('');
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  await close.click();
  await launcher.press('Enter');
  const answer = page.locator(
    `[data-folder-agent-chat-card][data-message-id="${music.chat.message_id}"] [data-folder-agent-action="premise_confirm"]`
  );
  await expect(answer).toBeVisible();
  await page.screenshot({ path: join(evidence, '04-open-question.png') });
  await expect
    .poll(
      async () =>
        (await (await request.get(`${api}/${music.workspace_id}`)).json()).folder_agent.chat.unread
    )
    .toBe(false);
  await close.click();
  await expect(badge).toBeVisible();
  await expect(launcher).toBeFocused();
  await launcher.press('Enter');
  await expect(answer).toBeVisible();
  await answer.click();
  await expect(answer).toHaveCount(0);
  await close.click();
  await expect(badge).toHaveCount(0);
  await expect(launcher).toHaveAccessibleName(/Cue Albums Manager Commander/i);
  await page.screenshot({ path: join(evidence, '05-answered.png') });
  await launcher.press('Space');
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
});

test('non-music premise, refused answer, normal unread and Home', async ({ page, request }) => {
  test.skip(!sandbox, 'requires disposable demo HOME');
  await installLocalCdn(page);
  const other = await seed(request, 'Field Notes and Research Reference Library', false);
  // There is no non-music Home provider in the current recognition table.
  // Project the same host state onto an ordinary notes workspace; don't add
  // a provider or teach the launcher to infer questions from folder content.
  const question = {
    ...other,
    model_sentence: '',
    premise: {
      state: 'asked',
      sentence: 'This looks like a research folder.',
      confirm: 'Yes, show me the plan',
      other: 'Something else'
    }
  };
  let pending = true;
  await page.route(`**${api}/${other.workspace_id}`, route =>
    route.fulfill({ json: { folder_agent: pending ? question : other } })
  );
  await page.route(`**${api}/${other.workspace_id}/premise`, route =>
    route.fulfill({ status: 409, json: { folder_agent: question, error: 'Please try again.' } })
  );
  await page.goto(other.route);
  const launcher = page.locator('#personalAssistantLauncher');
  const badge = launcher.locator('[data-question]');
  await expect(launcher).toHaveAccessibleName(
    new RegExp(`${other.agent_name}.*Has a question`, 'i')
  );
  await expect(badge).toBeVisible();
  for (const [width, theme] of [
    [1280, 'light'],
    [390, 'dark']
  ] as const) {
    await page.setViewportSize({ width, height: 850 });
    await page.evaluate(theme => {
      document.documentElement.dataset.bsTheme = theme;
      document.documentElement.dataset.theme = theme;
    }, theme);
    await page.keyboard.press('Tab');
    await launcher.focus();
    await expect(launcher).toBeFocused();
    expect(
      await launcher.evaluate(
        el =>
          el.matches(':focus-visible') &&
          el.getBoundingClientRect().right <= innerWidth &&
          el.getBoundingClientRect().left >= 0
      )
    ).toBe(true);
    await page.screenshot({ path: join(evidence, `06-long-name-${width}-${theme}.png`) });
  }
  await launcher.press('Enter');
  const report = page.locator('#homeAssistantConversation [data-folder-agent-chat-card]');
  await report.getByRole('button', { name: question.premise.confirm }).click();
  await expect(report).toContainText('Please try again.');
  await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  await expect(badge).toBeVisible();
  pending = false;
  await page.reload();
  await expect(launcher).toContainText('Commander');
  await expect(badge).toHaveCount(0);
  await page.goto('/');
  await expect(launcher).toContainText('Atlas');
  await expect(badge).toHaveCount(0);
  await expect(launcher.locator('#personalAssistantLauncherRole')).toHaveText('Personal Assistant');
});
