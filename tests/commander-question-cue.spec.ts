import { expect, test, type APIRequestContext } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Run with scripts/e2e-fresh.sh --sandbox-env ORI_QUESTION_SANDBOX.
// No provider, plugin installation or real music folder needed.
const sandbox = process.env.ORI_QUESTION_SANDBOX;
const api = '/api/personal-assistant/folder-agent';
const evidence = 'tasks/evidence-commander-question-cue';

async function seed(request: APIRequestContext, folder: string, music: boolean) {
  const post = async (url: string, data: object) => {
    const r = await request.post(url, { data });
    expect(r.ok(), await r.text()).toBe(true);
    return r.json();
  };
  await request.post('/api/onboarding/skip');
  let a = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (a.state === 'needs_hire') {
    a = (
      await post('/api/personal-assistant/hire', {
        request_id: 'cue-hire',
        if_version: a.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help with folders.',
        focus_areas: ['plan_my_day']
      })
    ).personal_assistant;
  }
  if (a.state === 'needs_hq')
    await post('/api/personal-assistant/hq', {
      request_id: 'cue-hq',
      if_version: a.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  const existing = (await (await request.get(api)).json()).folder_agents.find(
    (v: { folder_name: string }) => v.folder_name === folder
  );
  if (existing) return existing;
  const root = join(sandbox!, folder);
  for (let n = 0; n < 5; n++) {
    const dir = join(root, `Project-${n}`);
    await mkdir(dir, { recursive: true });
    await writeFile(
      join(dir, music ? 'Song.rpp' : 'notes.md'),
      music ? '<REAPER_PROJECT\n>\n' : '# Notes\n'
    );
  }
  const picked = await post('/api/folder-picker/select-path', { path: root });
  const chosen = await post(`${api}/choose`, { selection_token: picked.selection_token });
  const started = await post(`${api}/start`, { choice_token: chosen.card.choice_token, brief: '' });
  const read = async () =>
    (await (await request.get(`${api}/${started.folder_agent.workspace_id}`)).json()).folder_agent;
  await expect.poll(async () => (await read()).chat?.message_id).toBeTruthy();
  return read();
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
  // A credential-free look reports its cause first. Exercise the successful
  // look's host phase deterministically; all chat refs and actions remain real.
  await page.route(`**${api}/${music.workspace_id}`, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.folder_agent.model_sentence = '';
    await route.fulfill({ response, json: body });
  });
  await page.goto(music.route);
  await expect(launcher).toHaveAccessibleName(/Cue Albums Manager.*Has a question for you/i);
  await expect(badge).toBeVisible();
  await expect(launcher.locator('#personalAssistantUnread')).toBeVisible();
  await page.screenshot({ path: join(evidence, '01-premise.png') });
  await page.goto(other.route);
  await expect(launcher).toHaveAccessibleName(/Cue Research Manager/i);
  await expect(badge).toHaveCount(0);
  await page.screenshot({ path: join(evidence, '02-no-question.png') });
});
