import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
import { folderResponseFixtures } from './fixtures/assistant-folder-response.js';

// Baseline uses the real built Ori drawer, with explicitly injected typed
// controller fixtures. Standalone prototype is NOT production integration.
// Runner: python3 scripts/assistant-workspace-demo.py --folder-response-baseline
// This mode has no provider calls, native picker, content reading or setup.
test('baseline drawer and standalone prototype at desktop and phone widths', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_FOLDERBASELINE_SANDBOX;
  test.skip(!sandbox, 'Use assistant-workspace-demo.py --folder-response-baseline');
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks/evidence/assistant-folder-response-ux/group-1');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-response-baseline-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-response-baseline-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const measurements: Record<string, unknown> = {};
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/settings');
  await page.locator('#personalAssistantLauncher').click();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  // Production renderer, fixture event + prose only. No Route/Ask/persistence claim.
  await page.evaluate(observation => {
    (window as any).OriAskRouting.resetConversation();
    (window as any).PersonalAssistantFolderContext.renderEvent('fixture-event', {
      version: 1,
      observation
    });
    (window as any).OriAskRouting.appendMessage('user', 'Explore this folder');
    (window as any).OriAskRouting.appendMessage(
      'assistant',
      'Fixture baseline report: The observed folder contains several projects and shared material. Aurora has a REAPER marker with four observed files, Tide has a Logic Pro marker with three, Artwork has two, and Notes has three. These counts overlap with the twelve files reported for the whole collection. File contents have not been read.\n\n' +
        'You could discuss the whole collection or focus on an individual folder. The snapshot is bounded and may omit deeper or inaccessible entries. Markers alone do not establish project contents, progress or completion.\n\n' +
        'For setup, a whole-folder workspace can provide collection context, while an individual-project workspace can focus on one child. Review suggested setup to choose the exact scope and see the effects before confirming. Nothing is created from this reply. Which scope would you like to explore first?'
    );
  }, folderResponseFixtures.album);
  for (const [name, width, height] of [
    ['desktop', 1440, 900],
    ['phone', 390, 844]
  ] as const) {
    await page.setViewportSize({ width, height });
    await page.locator('#personalAssistantScroll').evaluate(el => (el.scrollTop = 0));
    const panel = page.locator('#personalAssistantPanel');
    await expect(panel).toBeVisible();
    measurements[`baseline-${name}`] = await panel.evaluate(el => ({
      width: el.getBoundingClientRect().width,
      scrollHeight: el.querySelector('#personalAssistantScroll')!.scrollHeight,
      clientHeight: el.querySelector('#personalAssistantScroll')!.clientHeight
    }));
    await panel.screenshot({ path: join(evidence, `baseline-${name}-controller-fixture.png`) });
  }
  const prototype = await readFile(
    join(process.cwd(), 'docs/design/assistant-folder-response-prototype.html'),
    'utf8'
  );
  for (const [name, width, height] of [
    ['desktop', 1440, 900],
    ['phone', 390, 844]
  ] as const) {
    await page.setViewportSize({ width, height });
    await page.goto('about:blank');
    await page.setContent(prototype);
    const drawer = page.locator('.drawer');
    await expect(drawer).toBeVisible();
    expect(await drawer.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy();
    measurements[`prototype-${name}`] = await drawer.evaluate(el => ({
      width: el.getBoundingClientRect().width,
      scrollHeight: el.querySelector('.scroll')!.scrollHeight,
      clientHeight: el.querySelector('.scroll')!.clientHeight
    }));
    await drawer.screenshot({ path: join(evidence, `prototype-${name}-collapsed.png`) });
    await page.locator('#details > summary').click();
    await drawer.screenshot({ path: join(evidence, `prototype-${name}-scan-details.png`) });
    await page.locator('#details > summary').click();
    await page.locator('#choose').click();
    await expect(page.locator('#project')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(page.locator('#choose')).toBeFocused();
    await expect(page.locator('#chooser')).toBeHidden();
    await page.locator('#input').fill('  Keep my exact draft 🎼  ');
    await page.locator('#discuss').click();
    await expect(page.locator('#input')).toHaveValue('  Keep my exact draft 🎼  ');
    await page.locator('#input').fill('');
    await page.locator('#choose').click();
    await page.locator('#project').selectOption({ label: 'Tide' });
    await page.locator('#draft').click();
    await expect(page.locator('#input')).toHaveValue(
      'Let’s discuss the observed folder “Tide” in “Album collection”, without setting anything up.'
    );
  }
  await writeFile(
    join(evidence, 'layout-comparison.json'),
    JSON.stringify(
      {
        evidence:
          'Built Ori controller fixture baseline vs standalone synthetic prototype; not provider/persistence proof',
        measurements
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
