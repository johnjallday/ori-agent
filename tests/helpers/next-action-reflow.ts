import { expect, type Page } from '@playwright/test';

// Model desktop 200% zoom with its effective 720px CSS viewport and 2x DPR.
// Unlike CSS zoom, this also exercises the correct responsive breakpoints.
// This is Chromium reflow evidence, not a browser-chrome/AT certification.
export async function checkNextActionReflow(page: Page, selector: string) {
  await page.setViewportSize({ width: 720, height: 550 });
  const client = await page.context().newCDPSession(page);
  await client.send('Emulation.setDeviceMetricsOverride', {
    width: 720,
    height: 550,
    deviceScaleFactor: 2,
    mobile: false,
    screenWidth: 1440,
    screenHeight: 1100
  });
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
