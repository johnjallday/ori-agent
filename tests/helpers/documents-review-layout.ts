import { expect, type Locator, type Page } from '@playwright/test';
import { resolve } from 'node:path';

// Optional acceptance evidence, from the real rendered card in its chat drawer.
export async function checkReviewLayout(page: Page, card: Locator, phase: string) {
  if (process.env.ORI_DOCUMENT_REVIEW_LAYOUT !== '1') return;
  const size = page.viewportSize();
  const initial = await page.evaluate(() =>
    ['data-theme', 'data-bs-theme'].map(a => document.documentElement.getAttribute(a))
  );
  try {
    for (const [theme, width] of [
      ['light', 1280],
      ['dark', 390]
    ] as const) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(theme => {
        for (const attr of ['data-theme', 'data-bs-theme'])
          document.documentElement.setAttribute(attr, theme);
      }, theme);
      await expect(card).toBeVisible();
      await expect.poll(() => card.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await expect(card.locator(':scope > [role=status]')).toBeVisible();
      const anchor =
        phase === 'filing-selection'
          ? card.getByRole('link', { name: 'Open workspace in a new tab' })
          : phase === 'placement-receipts'
            ? card.getByText('Show those decisions', { exact: true }).first()
            : phase === 'groups'
              ? card.getByRole('heading', { name: /kitchen remodel/i })
              : card.getByRole('button', { name: 'Approve this read' });
      if (await anchor.count()) {
        await anchor.evaluate(el =>
          el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' })
        );
        await expect(anchor).toBeInViewport();
      }
      await page.screenshot({
        path: resolve(
          'tasks/evidence-documents-organization-review',
          `${phase}-${theme}-${width}.png`
        )
      });
    }
  } finally {
    if (size) await page.setViewportSize(size);
    await page.evaluate(
      values =>
        ['data-theme', 'data-bs-theme'].forEach((attr, i) => {
          const v = values[i];
          if (v === null) document.documentElement.removeAttribute(attr);
          else document.documentElement.setAttribute(attr, v);
        }),
      initial
    );
  }
}
