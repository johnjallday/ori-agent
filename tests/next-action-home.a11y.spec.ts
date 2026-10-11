import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { mockHiredAssistant } from './helpers/hired-assistant';
import { checkNextActionReflow } from './helpers/next-action-reflow';

// Synthetic read projections. Real task navigation is exercised separately in
// next-action-journey.spec.ts; no provider or runtime readiness is inferred here.
for (const theme of ['light', 'dark']) {
  test(`Home attention and assistant-owner requests are accessible (${theme})`, async ({
    page
  }, info) => {
    await installLocalCdn(page);
    await mockHiredAssistant(page);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { needs_onboarding: false, completed: true } })
    );
    await page.route('**/api/workspaces?tree=true', route =>
      route.fulfill({
        json: {
          folders: [
            {
              id: 'launch',
              name: 'Launch review',
              folder_slug: 'launch-review',
              kind: 'workspace',
              open_task_count: 2,
              needs_attention_count: 2,
              task_summary_available: true
            },
            {
              id: 'unknown',
              name: 'Unreadable source',
              kind: 'workspace',
              task_summary_available: false
            }
          ]
        }
      })
    );
    await page.route('**/api/personal-hq/status', route =>
      route.fulfill({
        json: { status: { valid: true, workspace_id: 'spec-hq', folder_slug: 'personal-hq' } }
      })
    );
    await page.route('**/api/personal-assistant/setup/file-janitor', route =>
      route.fulfill({ json: { setup: { run: null } } })
    );
    await page.route('**/api/personal-assistant/today', route =>
      route.fulfill({
        json: {
          today: {
            state: 'partial',
            display_name: 'Atlas',
            needs_you: {
              health: { status: 'unavailable' },
              items: [
                {
                  id: 'decision',
                  title: 'Choose tomorrow’s focus',
                  kind: 'decision',
                  route: '/workspaces/personal-hq?follow_up=decision',
                  ref: { workspace_id: 'spec-hq', entity_type: 'follow_up', entity_id: 'decision' }
                }
              ]
            },
            unavailable_sources: ['meetings']
          }
        }
      })
    );
    await page.goto('/');
    const updates = page.getByRole('button', { name: 'Show Updates', exact: true });
    await updates.focus();
    await page.keyboard.press('Enter');
    const attention = page.locator('#cockpitTodayAttention a');
    await expect(attention).toContainText('2 tasks needing attention');
    await attention.focus();
    await expect(attention).toHaveCSS('outline-style', 'solid');
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const scan = async (selector: string) => {
      const result = await page.evaluate(async selector => {
        const result = await (window as any).axe.run(selector, {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
        return {
          violations: result.violations,
          contrast:
            result.passes.find((rule: any) => rule.id === 'color-contrast')?.nodes.length || 0
        };
      }, selector);
      expect(result.violations).toEqual([]);
      expect(result.contrast).toBeGreaterThan(0);
    };
    await scan('#cockpitTodayAttention');
    await page.screenshot({ path: info.outputPath(`home-attention-${theme}.png`), fullPage: true });
    await checkNextActionReflow(page, '#cockpitTodayAttention');
    await page.screenshot({
      path: info.outputPath(`home-attention-zoom-${theme}.png`),
      fullPage: true
    });
    await page.getByRole('button', { name: 'Close Updates', exact: true }).click();
    await page.evaluate(() => {
      document.documentElement.style.zoom = '';
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(async () => {
      (window as any).PersonalAssistantPanel.open();
      await (window as any).PersonalAssistantToday.refresh();
      (window as any).PersonalAssistantToday.expand();
    });
    await page.locator('#personalAssistantNeedsYouQueueTitle').click();
    const request = page.locator('#personalAssistantNeedsYouItems a');
    await expect(request).toBeVisible();
    await page.keyboard.press('Tab');
    await request.focus();
    await expect(request).toHaveCSS('outline-style', 'solid');
    await scan('#personalAssistantToday');
    await page.screenshot({
      path: info.outputPath(`assistant-owner-${theme}.png`),
      fullPage: true
    });
    await checkNextActionReflow(page, '#personalAssistantToday');
    await page.screenshot({
      path: info.outputPath(`assistant-owner-zoom-${theme}.png`),
      fullPage: true
    });
  });
}
