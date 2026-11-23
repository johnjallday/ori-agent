import { expect, type APIRequestContext } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

// Only callers holding a disposable sandbox may provision the synthetic HQ.
export async function seedQuestionFolder(
  request: APIRequestContext,
  sandbox: string,
  folder: string,
  music: boolean
) {
  const api = '/api/personal-assistant/folder-agent';
  const post = async (url: string, data: object) => {
    const r = await request.post(url, { data });
    expect(r.ok(), await r.text()).toBe(true);
    return r.json();
  };
  await request.post('/api/onboarding/skip');
  let a = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (a.state === 'needs_hire')
    a = (
      await post('/api/personal-assistant/hire', {
        request_id: 'cue-hire',
        if_version: a.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help with folders.',
        focus_areas: ['plan_my_day']
      })
    ).personal_assistant;
  if (a.state === 'needs_hq')
    await post('/api/personal-assistant/hq', {
      request_id: 'cue-hq',
      if_version: a.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  const root = join(sandbox, folder);
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
