import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// A real sandbox filesystem failure after confirmation, not a mocked resume.
// Run with e2e-fresh --sandbox-env ORI_HQ_REVEAL_SANDBOX.
test('a paused HQ resumes the same operation after sandbox permissions recover', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_HQ_REVEAL_SANDBOX;
  expect(sandbox, 'requires the fresh-server sandbox variable').toBeTruthy();
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } });
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(initial.state).toBe('needs_hire');
  expect(
    (
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'resume-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my week moving.',
          focus_areas: ['plan_my_day']
        }
      })
    ).ok()
  ).toBeTruthy();
  const rootState = await (await request.get('/api/settings/workspace-root')).json();
  const root =
    rootState.workspace_root ||
    rootState.effective_workspace_root ||
    rootState.default_workspace_root;
  expect(path.resolve(root).startsWith(`${fs.realpathSync(sandbox!)}/`)).toBeTruthy();
  const permissions = fs.statSync(root).mode & 0o777;
  await page.goto('/');
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await page.locator('#personalAssistantHQNotesOptions summary').click();
  await page.locator('#personalAssistantHQNotesInclude').uncheck();
  const bodies: any[] = [];
  page.on('request', r => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/personal-assistant/hq')
      bodies.push(r.postDataJSON());
  });
  try {
    fs.chmodSync(root, 0o500);
    await page.locator('#personalAssistantHQBuild').click();
    await expect
      .poll(
        async () =>
          (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
      )
      .toBe('provisioning_hq');
    await expect(page.locator('[data-creation-map-status]')).toContainText('paused');
  } finally {
    fs.chmodSync(root, permissions);
  }
  const paused = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  await page
    .locator('[data-creation-map-status]')
    .getByRole('button', { name: 'Resume setup', exact: true })
    .click();
  await expect(page.locator('[data-creation-map-status]')).toContainText('Your HQ is ready');
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toEqual({ mode: 'resume' });
  const active = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(active.state).toBe('active');
  expect(active.assistant_id).toBe(paused.assistant_id);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-2', { recursive: true });
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-2/resumed.png',
    fullPage: true
  });
});
