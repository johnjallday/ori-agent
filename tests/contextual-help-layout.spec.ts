import { test, expect, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { mockHiredAssistant } from './helpers/hired-assistant';

// Isolated demo only. Help responses are real except the explicitly labelled
// long-copy and delayed-response fixtures. The relationship is fictional;
// manual task records have no agent, schedule, provider or execution request.
async function prepare(page: Page, theme = 'light') {
  await installLocalCdn(page);
  await mockHiredAssistant(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
}

async function openHelp(page: Page) {
  await page.locator('#oriGuideLauncher').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#oriGuideInput')).toBeFocused();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', /.+/);
}

async function axe(page: Page, selector: string) {
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const violations = await page.evaluate(async selector => {
    const result = await (window as any).axe.run(document.querySelector(selector), {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
    return result.violations;
  }, selector);
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}

async function bounded(page: Page, selector: string) {
  const geometry = await page.locator(selector).evaluate(el => {
    const rect = el.getBoundingClientRect();
    return {
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      width: innerWidth,
      height: innerHeight,
      overflow: el.scrollWidth - el.clientWidth
    };
  });
  expect(geometry.left).toBeGreaterThanOrEqual(0);
  expect(geometry.right).toBeLessThanOrEqual(geometry.width);
  expect(geometry.top).toBeGreaterThanOrEqual(0);
  expect(geometry.bottom).toBeLessThanOrEqual(geometry.height);
  expect(geometry.overflow).toBeLessThanOrEqual(1);
}

for (const theme of ['light', 'dark']) {
  for (const screen of ['home', 'agents', 'task']) {
    test(`${theme} keyboard Help on ${screen}, narrow reflow and scoped axe`, async ({
      page,
      request
    }, testInfo) => {
      await prepare(page, theme);
      let workspaceId = '';
      let path = screen === 'agents' ? '/agents' : '/';
      try {
        if (screen === 'task') {
          const workspace = await request.post('/api/orchestration/workspace', {
            data: {
              name: `Help layout fixture ${Date.now()}`,
              description: 'Disposable manual fixture'
            }
          });
          expect(workspace.ok()).toBeTruthy();
          const data = await workspace.json();
          workspaceId = data.workspace_id;
          const task = await request.post('/api/orchestration/tasks', {
            data: { workspace_id: workspaceId, description: 'Manual layout fixture; do not run' }
          });
          expect(task.ok()).toBeTruthy();
          const record = (await task.json()).task;
          expect(record.to || '').toBe('');
          path = `/workspaces/${data.workspace_slug}/task/${record.id}`;
        }
        await page.setViewportSize({ width: 1440, height: 900 });
        await page.goto(path);
        await openHelp(page);
        await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'true');
        await expect(page.locator('#oriGuidePanel')).not.toHaveAttribute('aria-modal', 'true');
        await bounded(page, '#oriGuidePanel');
        await axe(page, '#oriGuidePanel');
        await page.screenshot({ path: testInfo.outputPath(`help-${theme}-${screen}-desktop.png`) });

        // 720x450 CSS pixels is the reflow viewport of a 1440x900 display at
        // 200% browser zoom; do not confuse device scale factor with zoom.
        for (const viewport of [
          { width: 720, height: 450 },
          { width: 390, height: 580 }
        ]) {
          await page.setViewportSize(viewport);
          if (screen === 'agents') {
            // The existing selected-agent inspector becomes a modal sheet on
            // resize. Escape must dismiss it without also closing Help.
            const inspector = page.locator('#inspector');
            if ((await inspector.getAttribute('aria-modal')) === 'true') {
              await page.keyboard.press('Escape');
              await expect(inspector).not.toHaveAttribute('aria-modal', 'true');
              await expect(page.locator('#oriGuidePanel')).toBeVisible();
            }
          }
          await bounded(page, '#oriGuidePanel');
          await page.locator('#oriGuideInput').focus();
          await expect(page.locator('#oriGuideInput')).toBeInViewport();
          await expect(page.locator('#oriGuideClose')).toBeInViewport();
          await expect(page.locator('#oriGuidePanel')).not.toHaveAttribute('aria-modal', 'true');
        }
        await axe(page, '#oriGuidePanel');
        await page.screenshot({ path: testInfo.outputPath(`help-${theme}-${screen}-mobile.png`) });
        await page.keyboard.press('Escape');
        // A route's greeting may mark an app control. First dismiss that mark.
        if (await page.locator('#oriGuidePanel').isVisible()) await page.keyboard.press('Escape');
        await expect(page.locator('#oriGuidePanel')).toBeHidden();
        await expect(page.locator('#oriGuideLauncher')).toBeFocused();
      } finally {
        if (workspaceId) {
          const cleanup = await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
          expect(cleanup.ok()).toBeTruthy();
        }
      }
    });
  }
}

test('non-modal focus, actual invoker and native upper dialog dismissal', async ({ page }) => {
  await prepare(page);
  await page.goto('/agents');
  await openHelp(page);
  await page.locator('#oriGuideInput').fill('model setup');
  await page.locator('#oriGuideSend').click();
  await expect(page.locator('#oriGuideReply')).toContainText('model');
  await page.locator('#oriGuidePanel button, #oriGuidePanel a').last().focus();
  await page.keyboard.press('Tab');
  expect(await page.evaluate(() => !!document.activeElement?.closest('#oriGuidePanel'))).toBe(
    false
  );

  // Exercise a contextual invoker through the existing public API. This
  // fixture button does not add an app entry point or change a work control.
  await page.evaluate(() => {
    (window as any).OriGuide.close();
    const invoker = document.createElement('button');
    invoker.id = 'explain-fixture';
    invoker.textContent = 'Explain this screen fixture';
    invoker.onclick = () => (window as any).OriGuide.open(invoker);
    document.querySelector('main')!.prepend(invoker);
  });
  await page.locator('#explain-fixture').click();
  await expect(page.locator('#oriGuideInput')).toBeFocused();
  await page.evaluate(() => {
    const dialog = document.createElement('dialog');
    dialog.id = 'upper-dialog-fixture';
    dialog.setAttribute('aria-label', 'Upper review fixture');
    dialog.innerHTML = '<button autofocus>Review fixture</button>';
    document.body.append(dialog);
    dialog.showModal();
  });
  await page.keyboard.press('Escape');
  await expect(page.locator('#upper-dialog-fixture')).not.toBeVisible();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect(page.locator('#explain-fixture')).toBeFocused();
});

test('long fictional copy and confirmed name reflow without hiding controls', async ({
  page
}, testInfo) => {
  await prepare(page, 'dark');
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      json: {
        personal_assistant: {
          state: 'active',
          state_version: 1,
          assistant_id: 'long-name-fixture',
          display_name: 'ExtraordinarilyLongUnbrokenAssistantName'.repeat(8),
          hq_workspace_id: 'fixture-hq',
          next_action: 'ask',
          availability: { model: { status: 'not_configured', available: false } }
        }
      }
    })
  );
  await page.route('**/api/ori-guide', route =>
    route.fulfill({
      json: {
        status: 'answered',
        answer: 'LongUnbrokenReviewedCopyFixture'.repeat(90),
        location: 'Long screen title fixture',
        about: 'LongAboutFixture'.repeat(60),
        actions: [
          { type: 'navigate', href: '/settings', label: 'LongDestinationLabelFixture'.repeat(18) }
        ],
        suggested: [{ key: 'model_setup', label: 'LongTopicLabelFixture'.repeat(18) }]
      }
    })
  );
  await page.setViewportSize({ width: 390, height: 580 });
  await page.goto('/agents');
  await openHelp(page);
  await bounded(page, '#oriGuidePanel');
  await page.locator('#oriGuideInput').focus();
  await expect(page.locator('#oriGuideClose')).toBeInViewport();
  await expect(page.locator('#oriGuideSend')).toBeInViewport();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await bounded(page, '#personalAssistantPanel');
  await axe(page, '#personalAssistantPanel');
  await page.screenshot({ path: testInfo.outputPath('assistant-long-name-fixture-mobile.png') });
  await page.keyboard.press('Escape');
  await expect(page.locator('#personalAssistantLauncher')).toBeFocused();
});

test('delayed fictional answers cannot restore old app-wide route actions', async ({ page }) => {
  await prepare(page);
  let release!: () => void;
  let started!: () => void;
  const pending = new Promise<void>(resolve => {
    release = resolve;
  });
  const requested = new Promise<void>(resolve => {
    started = resolve;
  });
  let workCalls = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/(route|ask)/.test(request.url())) workCalls++;
  });
  await page.route('**/api/ori-guide', async route => {
    if (route.request().postDataJSON().question === 'delayed fixture') {
      started();
      await pending;
      await route.fulfill({
        json: {
          status: 'answered',
          answer: 'Stale fictional answer',
          actions: [{ type: 'navigate', href: '/agents', label: 'Stale fictional action' }]
        }
      });
    } else await route.continue();
  });
  await page.goto('/agents');
  await openHelp(page);
  await page.locator('#oriGuideInput').fill('delayed fixture');
  await page.locator('#oriGuideSend').click();
  await requested;
  // A same-document app-wide navigation is a fixture, not a Settings render.
  await page.evaluate(() => {
    history.pushState({}, '', '/settings');
    dispatchEvent(new PopStateEvent('popstate'));
  });
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'context-changed');
  release();
  await page.locator('#oriGuideInput').fill('model setup');
  await page.locator('#oriGuideSend').click();
  await expect(page.locator('#oriGuideReply')).toContainText('model');
  await expect(page.locator('#oriGuideReply')).not.toContainText('Stale fictional');
  await expect(page.getByText('Stale fictional action')).toHaveCount(0);
  expect(workCalls).toBe(0);
});
