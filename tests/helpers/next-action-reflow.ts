import { expect, type Page } from '@playwright/test';

// Check 1440×1100 at 200%'s equivalent 720×550 CSS reflow viewport in either
// engine. DPR/device-metrics emulation is not browser zoom and adds no reflow
// coverage. Native browser-chrome zoom and AT certification remain separate.
export async function checkNextActionReflow(page: Page, selector: string) {
  await page.setViewportSize({ width: 720, height: 550 });
  const bounds = await page.locator(selector).evaluateAll(roots =>
    roots.flatMap(root =>
      [...root.querySelectorAll<HTMLElement>('a, button, input, select, textarea, summary')]
        .filter(node => {
          const box = node.getBoundingClientRect();
          return box.width > 0 && box.height > 0 && getComputedStyle(node).visibility !== 'hidden';
        })
        .map(node => {
          const box = node.getBoundingClientRect();
          return {
            label: node.textContent?.trim() || node.getAttribute('aria-label') || node.tagName,
            left: box.left,
            right: box.right
          };
        })
    )
  );
  expect(bounds.length, 'the reflow check must exercise visible controls').toBeGreaterThan(0);
  for (const box of bounds) {
    expect(box.left, box.label).toBeGreaterThanOrEqual(-1);
    expect(box.right, box.label).toBeLessThanOrEqual(721);
  }
}
