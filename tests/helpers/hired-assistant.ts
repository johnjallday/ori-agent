// Present a hired personal assistant to the page, without touching the server.
//
// Why: until the assistant is hired, the Agents page's New Agent opens Mission
// 01's assistant preset, and Ori walks the user through it
// (tasks/prd-meet-your-assistant-mission.md). A spec about the ordinary create
// form, or about the guide's own marks on /agents, runs as a user who has
// already hired one, which is who sees that form. A fresh demo sandbox has not
// hired anyone, so these specs say so here rather than hiring for real.
import type { Page } from '@playwright/test';

/**
 * mockHiredAssistant answers GET /api/personal-assistant with an active
 * relationship. Call it before page.goto().
 */
export async function mockHiredAssistant(page: Page): Promise<void> {
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: 'active',
          state_version: 1,
          assistant_id: 'spec-assistant',
          display_name: 'Atlas',
          hq_workspace_id: 'spec-hq',
          next_action: 'ask',
          availability: { model: { status: 'not_configured', available: false } }
        }
      })
    })
  );
}
