import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// scripts/e2e-goal-suggestions.sh --upgrade starts two real server processes
// with one marked, disposable HOME/database. No provider or agent is needed.
const sandbox = process.env.ORI_GOAL_E2E_SANDBOX;
const phase = process.env.ORI_GOAL_PHASE;
const endpoint = '/api/personal-assistant/second-brain';
test.skip(
  !sandbox || !['before', 'after'].includes(phase || ''),
  'requires the isolated --upgrade wrapper'
);

test('Inbox review restart: lost confirmation, opposite destination and dismissal survive', async ({
  page,
  request,
  baseURL
}) => {
  expect(new URL(baseURL!).hostname).toMatch(/^(localhost|127\.0\.0\.1)$/);
  expect(readFileSync(join(sandbox!, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync(process.cwd())}`
  );
  const root = await (await request.get('/api/settings/workspace-root')).json();
  expect(root.default_workspace_root).toBe(join(sandbox!, 'Ori Workspaces'));
  const pid = Number(
    execFileSync('lsof', ['-nP', '-t', `-iTCP:${new URL(baseURL!).port}`, '-sTCP:LISTEN'], {
      encoding: 'utf8'
    }).trim()
  );
  expect(pid).toBeGreaterThan(0);
  await installLocalCdn(page);
  const get = async (path: string) => {
    const res = await request.get(path);
    expect(res.ok(), path).toBe(true);
    return res.json();
  };
  const post = async (path: string, data: unknown) => {
    const res = await request.post(path, { data });
    expect(res.ok(), `${path}: ${res.status()}`).toBe(true);
    return res.json();
  };
  const statePath = join(sandbox!, 'inbox-review-test.json');
  if (phase === 'before') {
    await post('/api/onboarding/complete', {});
    await post('/api/settings/workspace-root', {
      workspace_root: join(sandbox!, 'Ori Workspaces')
    });
    await post('/api/personal-assistant/hire', {
      request_id: 'review-hire',
      if_version: 0,
      display_name: 'Atlas',
      mandate: 'Keep track of my projects.',
      focus_areas: ['keep_projects_moving']
    });
    const assistant = (await get('/api/personal-assistant')).personal_assistant;
    await post('/api/personal-assistant/hq', {
      request_id: 'review-hq',
      if_version: assistant.state_version,
      name: 'Personal HQ',
      timezone: 'Asia/Seoul',
      schedule_days: ['mon'],
      schedule_time: '08:00',
      scope: 'all',
      include_future_workspaces: true,
      notify_on_ready: false
    });
    await post(endpoint + '/create', { request_id: 'review-setup', name: 'Review notebook' });
    const ref = (await get(endpoint + '?view=connection')).second_brain.browse_ref;
    const house = (await post('/api/workspaces', { name: 'House', blank: true })).folder;
    const garden = (await post('/api/workspaces', { name: 'Garden', blank: true })).folder;
    const { capture } = await post(endpoint + '/capture', {
      request_id: 'review-note',
      source_ref: ref,
      text: 'Compare both quotes. Ask Maya about disposal.'
    });
    const name = capture.note.slice('Inbox/'.length);
    const url = '/my-notes#' + new URLSearchParams({ ref, note: name });
    await page.goto(url);
    const cards = page.locator('.my-notes-suggestion');
    await expect(cards).toHaveCount(2);
    await cards.nth(1).getByRole('button', { name: 'Keep as note' }).click();
    await expect(cards).toHaveCount(1);
    await cards.getByRole('combobox').selectOption(house.id);
    let decision, ticket;
    await page.route('**/inbox/decision', async route => {
      decision = route.request().postDataJSON();
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      ticket = (await response.json()).suggestion.ticket_id;
      await route.abort('failed');
    });
    await cards.getByRole('button', { name: 'Create ticket' }).click();
    await expect.poll(() => Boolean(ticket)).toBe(true);
    writeFileSync(
      statePath,
      JSON.stringify({ pid, decision, ticket, url, house: house.id, garden: garden.id }),
      { mode: 0o600 }
    );
  } else {
    const state = JSON.parse(readFileSync(statePath, 'utf8'));
    expect(pid).not.toBe(state.pid);
    const replay = await post(endpoint + '/inbox/decision', {
      ...state.decision,
      workspace_id: state.garden
    });
    expect(replay.suggestion).toMatchObject({
      state: 'accepted',
      workspace_id: state.house,
      ticket_id: state.ticket
    });
    for (const ws of [state.house, state.garden]) {
      const list = await get(`/api/orchestration/backlog?workspace_id=${ws}`);
      expect(
        (list.items || []).filter(entry => entry.task.source_id === state.decision.action_id)
      ).toHaveLength(ws === state.house ? 1 : 0);
      const { runs } = await get(`/api/workspaces/${ws}/runs`);
      expect(runs || []).toHaveLength(0);
    }
    await page.goto(state.url);
    await expect(page.locator('.my-notes-suggestion')).toHaveCount(1);
    const link = page.getByRole('link', { name: 'Open ticket in House' });
    await expect(link).toHaveAttribute('href', new RegExp(`ticket=${state.ticket}$`));
    await expect(page.getByRole('button', { name: 'Keep as note' })).toHaveCount(0);
    if (process.env.MY_NOTES_EVIDENCE)
      await page.screenshot({
        path: join(process.env.MY_NOTES_EVIDENCE, 'review-restarted.png'),
        fullPage: true
      });
  }
});
