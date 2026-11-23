import { expect, test } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real creator, stores and filesystem. Pass ORI_HQ_RECOVERY_SANDBOX from
// `wt demo` (or e2e-fresh) so the spec cannot seed a normal installation.
test('a paused My HQ survives reload and builds only in the confirmed alternate folder', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_HQ_RECOVERY_SANDBOX;
  expect(sandbox, 'requires a disposable Ori demo sandbox').toBeTruthy();
  const guard = fs.realpathSync(sandbox!);
  expect(fs.existsSync(path.join(guard, '.ori-demo-owner'))).toBeTruthy();
  const rootState = await (await request.get('/api/settings/workspace-root')).json();
  const suggested = path.resolve(
    rootState.effective_workspace_root || rootState.default_workspace_root
  );
  expect(suggested.startsWith(`${guard}${path.sep}`)).toBeTruthy();

  await installLocalCdn(page);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const root = fs.realpathSync(suggested);
  expect(root.startsWith(`${guard}${path.sep}`)).toBeTruthy();
  const relationship = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const initial = await relationship();
  expect(initial.state).toBe('needs_hire');
  expect(
    (
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'collision-demo-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my week moving.',
          focus_areas: ['plan_my_day']
        }
      })
    ).ok()
  ).toBeTruthy();
  const detached = ['my-hq', 'my-hq-2'];
  for (const slug of detached) {
    fs.mkdirSync(path.join(root, slug));
    fs.writeFileSync(path.join(root, slug, 'keep.txt'), `retained ${slug}\n`);
  }

  const evidence = 'tasks/evidence-hq-reset-recovery';
  const openSetup = () =>
    page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  const alternates: Array<{ folder_slug: string }> = [];
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      new URL(req.url()).pathname === '/api/personal-assistant/hq/folder'
    )
      alternates.push(req.postDataJSON());
  });
  await page.goto('/');
  await openSetup();
  await page.locator('#personalAssistantHQNotesOptions summary').click();
  await page.locator('#personalAssistantHQNotesInclude').uncheck();
  await page.locator('#personalAssistantHQTime').fill('07:45');
  await page.locator('#personalAssistantHQBuild').click();
  const status = page.locator('[data-creation-map-status]');
  await expect(status).toContainText('my-hq-3');
  fs.mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/01-hq-conflict.png` });
  const paused = await relationship();
  expect(paused.state).toBe('provisioning_hq');
  expect(paused.hq_workspace_id).toBeFalsy();

  await status.getByRole('button', { name: 'Dismiss', exact: true }).click();
  expect(alternates).toHaveLength(0);
  await page.evaluate(() => {
    sessionStorage.clear();
    localStorage.clear();
  });
  await page.reload();
  await openSetup();
  await expect(page.locator('#personalAssistantHQFolderConflict')).toContainText('my-hq-3');
  await expect(page.locator('#personalAssistantHQBuild')).toHaveText('Resume setup');
  expect((await relationship()).state_version).toBe(paused.state_version);
  expect(alternates).toHaveLength(0);
  await page.screenshot({ path: `${evidence}/02-hq-paused-after-reload.png` });

  await page.locator('#personalAssistantHQFolderConfirm').click();
  await expect(page.locator('#personalHQResultHost')).toContainText('Your HQ is ready');
  expect(alternates).toHaveLength(1);
  expect(alternates[0].folder_slug).toBe('my-hq-3');
  const active = await relationship();
  expect(active.state).toBe('active');
  expect(active.assistant_id).toBe(paused.assistant_id);
  expect(active.daily_brief.schedule_time).toBe('07:45');
  const hq = (await (await request.get('/api/personal-hq/status')).json()).status;
  expect(hq.valid).toBeTruthy();
  expect(hq.workspace).toMatchObject({
    id: active.hq_workspace_id,
    name: 'My HQ',
    folder_slug: 'my-hq-3'
  });
  await expect(page.locator('#personalAssistantHQFolderConfirm')).toBeHidden();
  for (const slug of detached) {
    expect(fs.readdirSync(path.join(root, slug))).toEqual(['keep.txt']);
    expect(fs.readFileSync(path.join(root, slug, 'keep.txt'), 'utf8')).toBe(`retained ${slug}\n`);
  }
  await page.screenshot({ path: `${evidence}/03-hq-ready.png` });
});
