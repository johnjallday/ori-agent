import { test, expect, chromium, type BrowserContext } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { installLocalCdn } from './helpers/offline-cdn';

// Own fresh server: actual build at 200% browser zoom, not CSS/pinch emulation.
test('200% Chromium browser zoom: real HQ stays visible beside the result and editor', async ({
  baseURL,
  request
}) => {
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(initial.state).toBe('needs_hire');
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hq-zoom-hire',
      if_version: initial.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my week moving.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.ok(), await hired.text()).toBeTruthy();
  const evidence = 'tasks/evidence-hq-onboarding-reveal/group-4';
  fs.mkdirSync(evidence, { recursive: true });
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'ori-hq-zoom-'));
  fs.mkdirSync(path.join(profile, 'Default'));
  fs.writeFileSync(
    path.join(profile, 'Default', 'Preferences'),
    JSON.stringify({
      partition: { default_zoom_level: { x: Math.log(2) / Math.log(1.2) } }
    })
  );
  let context: BrowserContext | undefined;
  try {
    context = await chromium.launchPersistentContext(profile, {
      channel: 'chromium',
      headless: true,
      baseURL,
      viewport: null,
      deviceScaleFactor: undefined,
      isMobile: undefined,
      args: ['--window-size=1440,1000']
    });
    const page = await context.newPage();
    await installLocalCdn(page);
    await page.goto('/');
    expect(await page.evaluate(() => window.devicePixelRatio)).toBeCloseTo(2, 1);
    expect(await page.evaluate(() => window.innerWidth)).toBe(720);
    await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
    const notesPath = await page.locator('#personalAssistantHQNotesPath').innerText();
    await page.locator('#personalAssistantHQBuild').click();
    const host = page.locator('#personalHQResultHost');
    await expect(host).toBeVisible();
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    expect(await host.evaluate(n => n.scrollWidth <= n.clientWidth + 1)).toBe(true);
    const hq = page.locator('.ws-map-tile').filter({ has: page.locator('.ws-map-tile-hq-badge') });
    const checkHQ = async () => {
      await expect(hq).toBeInViewport();
      expect(
        await hq.evaluate(n => {
          const box = n.getBoundingClientRect();
          return n.contains(
            document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
          );
        })
      ).toBe(true);
    };
    await checkHQ();
    // Playwright's viewport-null screenshot clips to CSS pixels at real page
    // zoom. Capture the browser surface directly so evidence is not cropped.
    const cdp = await context.newCDPSession(page);
    const capture = async (name: string) => {
      const image = await cdp.send('Page.captureScreenshot', {
        format: 'png',
        captureBeyondViewport: false
      });
      fs.writeFileSync(
        `${evidence}/chromium-200-percent-${name}.png`,
        Buffer.from(image.data, 'base64')
      );
    };
    await capture('result');
    await page.locator('#personalAssistantHQFirstNote').click();
    const text = page.locator('#personalAssistantHQNoteText');
    await expect(text).toBeFocused();
    await expect(page.locator('#personalAssistantHQNoteSave')).toBeInViewport();
    await checkHQ();
    await text.fill('Saved at two hundred percent browser zoom.');
    // A visible/in-viewport action can still be partly covered by a duplicate
    // completion toast. Hit-test every edge, not just its clickable centre.
    for (const id of [
      'personalAssistantHQNoteText',
      'personalAssistantHQNoteSave',
      'personalAssistantHQNoteBack',
      'personalAssistantHQFirstExplore'
    ]) {
      expect(
        await page.locator(`#${id}`).evaluate(el => {
          const b = el.getBoundingClientRect();
          return [
            [b.left + b.width / 2, b.top + 2],
            [b.left + b.width / 2, b.bottom - 2],
            [b.left + 2, b.top + b.height / 2],
            [b.right - 2, b.top + b.height / 2]
          ].every(([x, y]) => el.contains(document.elementFromPoint(x, y)));
        }),
        `${id} is not covered at 200% zoom`
      ).toBe(true);
    }
    await expect(page.locator('.toast-title').filter({ hasText: 'Quest complete' })).toHaveCount(0);
    await capture('editor');
    const response = page.waitForResponse(
      r => r.url().endsWith('/second-brain/capture') && r.request().method() === 'POST'
    );
    await page.locator('#personalAssistantHQNoteSave').click();
    const saved = (await (await response).json()).capture;
    await expect(page.locator('#personalAssistantHQNoteSavedTitle')).toBeFocused();
    expect(fs.readFileSync(path.join(notesPath, saved.note), 'utf8')).toContain(
      'Saved at two hundred percent browser zoom.'
    );
    await page.locator('#personalAssistantHQNoteDone').click();
    await expect(host).toBeHidden();
  } finally {
    await context?.close();
    fs.rmSync(profile, { recursive: true, force: true });
  }
});
