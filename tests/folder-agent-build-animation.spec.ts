import { test, expect } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import fs from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Real folder, accepted hire, workspace and agent. Only the look's GET/start
// projection is held at looking, so a fast model-free report cannot race the
// decorative entrance assertions. No provider or real user folder is needed.
const sandbox = process.env.ORI_FOLDER_AGENT_SANDBOX || '';
const live = process.env.ORI_BUILD_LIVE === '1';
test.afterEach(async ({ page }) => page.unrouteAll({ behavior: 'wait' }));
for (const mode of ['light', 'dark', 'reduced', 'reload-during']) {
  test(`a folder hire builds once on Home — ${mode}`, async ({ page, request }) => {
    test.skip(!sandbox, 'run e2e-fresh --sandbox-env ORI_FOLDER_AGENT_SANDBOX');
    test.setTimeout(live ? 180_000 : 60_000);
    await installLocalCdn(page);
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.emulateMedia({ reducedMotion: mode === 'reduced' ? 'reduce' : 'no-preference' });
    const name = `Build ${mode} ${Date.now()}`;
    const folder = join(sandbox, 'Music', name);
    await mkdir(join(folder, 'Audio'), { recursive: true, mode: 0o750 });
    await writeFile(join(folder, 'Song.rpp'), '<REAPER_PROJECT\n>\n', { mode: 0o600 });
    await request.post('/api/onboarding/skip');
    if (live) {
      const configured = await request.post('/api/settings/system-model', {
        data: { provider: 'claude', model: 'claude-sonnet-4-6' }
      });
      expect(configured.ok(), await configured.text()).toBeTruthy();
    }
    let assistant = (await (await request.get('/api/personal-assistant')).json())
      .personal_assistant;
    if (assistant.state === 'needs_hire') {
      const hire = await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: `assembly-hire-${mode}`,
          if_version: assistant.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help me with my songs.',
          focus_areas: ['plan_my_day']
        }
      });
      expect(hire.ok(), await hire.text()).toBeTruthy();
      assistant = (await hire.json()).personal_assistant;
    }
    if (!assistant.hq_workspace_id) {
      const hq = await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: `assembly-hq-${mode}`,
          if_version: assistant.state_version,
          name: 'My HQ',
          timezone: 'UTC'
        }
      });
      expect(hq.ok(), await hq.text()).toBeTruthy();
    }
    let hold = true;
    let id = '';
    await page.route(
      /\/api\/personal-assistant\/folder-agent(?:\/[^/?]+)?(?:\?.*)?$/,
      async route => {
        const response = await route.fetch();
        const body = await response.json();
        const keepLooking = (view: any) =>
          hold && view?.folder_name === name ? { ...view, state: 'looking' } : view;
        if (body.folder_agent) {
          if (route.request().method() === 'POST' && route.request().url().endsWith('/start'))
            id = body.folder_agent.workspace_id;
          body.folder_agent = keepLooking(body.folder_agent);
        }
        if (body.folder_agents) body.folder_agents = body.folder_agents.map(keepLooking);
        await route.fulfill({ response, json: body });
      }
    );
    await page.goto('/?panel=today&folder=show');
    await page.evaluate(
      theme => document.documentElement.setAttribute('data-bs-theme', theme),
      mode === 'dark' ? 'dark' : 'light'
    );
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await expect(entry).toBeVisible();
    await entry.locator('input').fill(folder);
    await entry.getByRole('button').click();
    const host = page.locator('#personalAssistantFolderAgent');
    const start = host.locator('[data-folder-agent-action="start"]');
    await expect(start).toBeEnabled();
    const evidence = `tasks/evidence-folder-build-animation/group-2/${live ? 'live-claude/' : ''}${mode}`;
    fs.mkdirSync(evidence, { recursive: true });
    await page.screenshot({ path: `${evidence}/before-hire.png` });
    await page.clock.install({ time: new Date('2030-01-01T00:00:00Z') });
    await page.clock.pauseAt(new Date('2030-01-01T00:00:01Z'));
    await start.click();
    await expect.poll(() => id).not.toBe('');
    const tile = page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${id}"]`);
    await expect(tile).toHaveAttribute(
      'data-build-reveal',
      mode === 'reduced' ? 'settled' : 'playing'
    );
    // Let the arrival camera's next frame run even while the decorative clock
    // is held for captures. Otherwise the screenshots precede the real pan.
    await page.clock.runFor(32);
    const face = tile.locator('.ws-map-tile-face');
    if (mode !== 'reduced') {
      await expect(face).toHaveCount(1);
      await expect(face).toBeHidden();
      expect(await tile.evaluate(el => getComputedStyle(el).animationName)).toBe('none');
      expect(await tile.evaluate(el => getComputedStyle(el).outlineStyle)).toBe('none');
      await expect(
        page
          .locator('.commander-balloon__headline:visible, .commander-balloon-line:visible')
          .filter({ hasText: `Building ${name}…` })
      ).toBeVisible();
      for (const offset of [400, 900, 1400]) {
        const style = await page.addStyleTag({
          content: `.is-assembling [data-build-part] { animation-play-state: paused !important; animation-delay: -${offset}ms !important; }`
        });
        await page.screenshot({ path: `${evidence}/assembly-${offset}.png` });
        await style.evaluate(el => el.remove());
      }
      if (mode === 'reload-during') {
        await page.clock.resume();
        await page.reload();
        await expect(tile).toBeVisible();
        await expect(page.locator('.is-assembling')).toHaveCount(0);
        await expect(tile).not.toHaveAttribute('data-build-reveal', 'playing');
        return;
      }
      // A real cockpit status remount during the build inherits elapsed time.
      await page.clock.runFor(800);
      await page.evaluate(() => window.dispatchEvent(new CustomEvent('ori:folder-agent-changed')));
      await expect(tile).toHaveAttribute('data-build-reveal', 'playing');
      await page.clock.runFor(800);
    }
    await expect(tile).toHaveAttribute('data-build-reveal', 'settled');
    await expect(face).toBeVisible();
    await expect(
      page
        .locator('.commander-balloon__headline:visible, .commander-balloon-line:visible')
        .filter({ hasText: /Looking (around|in)|Opening |Thinking it through/ })
        .last()
    ).toBeVisible();
    await page.screenshot({ path: `${evidence}/finished.png` });
    hold = false;
    await page.clock.resume();
    await page.evaluate(() => window.dispatchEvent(new CustomEvent('ori:folder-agent-changed')));
    await expect(
      page.locator('.folder-agent-card__run-head:visible').filter({ hasText: /Plan/ }).last()
    ).toBeVisible({ timeout: live ? 120_000 : 20_000 });
    if (live) {
      const views = (await (await request.get('/api/personal-assistant/folder-agent')).json())
        .folder_agents;
      expect(views.find((view: any) => view.workspace_id === id)?.plan?.source).toBe('agent');
    }
    await page.screenshot({ path: `${evidence}/plan.png` });
    if (mode === 'dark' && !live) {
      const firstID = id;
      const secondFolder = join(sandbox, 'Music', `Second ${name}`);
      await mkdir(secondFolder, { recursive: true, mode: 0o750 });
      await writeFile(join(secondFolder, 'Song.rpp'), '<REAPER_PROJECT\\n>\\n', { mode: 0o600 });
      await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
      await page.getByRole('button', { name: 'Hire an agent', exact: true }).click();
      await expect(entry).toBeVisible();
      await entry.locator('input').fill(secondFolder);
      await entry.getByRole('button').click();
      const now = await page.evaluate(() => Date.now());
      await page.clock.pauseAt(new Date(now + 1000));
      await start.click();
      await expect.poll(() => id).not.toBe(firstID);
      await expect(page.locator(`.ws-map-tile[data-ws-id="${id}"]`)).toHaveAttribute(
        'data-build-reveal',
        'playing'
      );
      await page.clock.runFor(32);
      await expect(tile).toHaveAttribute('data-build-reveal', 'settled');
      await expect(page.locator('.ws-map-tile[data-build-reveal="playing"]')).toHaveCount(1);
      await page.screenshot({ path: `${evidence}/second-hire-only-new-tile.png` });
      await page.clock.resume();
    }
    await page.reload();
    await expect(tile).toBeVisible();
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await expect(tile).not.toHaveAttribute('data-build-reveal', 'playing');
  });
}
