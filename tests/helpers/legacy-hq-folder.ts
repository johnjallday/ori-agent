import type { Page } from '@playwright/test';

// Resume a tab on the handoff that earlier HQ versions actually issued.
// No receipt, workspace ID, report or completion is seeded here.
export async function resumeLegacyFolderGuide(page: Page) {
  const href = await page.evaluate(async () => {
    const module = '/js/modules/hq-guided-attempt.js';
    const { beginHQAttempt, handoffURL } = await import(module);
    const hint = beginHQAttempt(document.body.dataset.guidedHq, 'folder');
    if (!hint) throw new Error('Expected a tab-local Personal HQ attempt');
    return handoffURL('/?panel=today&folder=guide', hint);
  });
  await page.goto(href);
}
