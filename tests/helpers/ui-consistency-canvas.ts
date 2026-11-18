import { expect, type Page, type Locator } from '@playwright/test';

/** Viewport navigation for geometry, not native Tab/AT certification. */
export async function boundedCanvasControl(control: Locator) {
  // Never let scrolling conceal a horizontal reflow defect.
  expect(
    await control.evaluate(node => {
      const r = node.getBoundingClientRect();
      return r.left >= 0 && r.right <= innerWidth + 1;
    })
  ).toBe(true);
  await control.evaluate(node =>
    node.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' })
  );
  await control.focus();
  await expect(control).toBeFocused();
  await expect
    .poll(() =>
      control.evaluate(node => {
        // Metadata/artwork can finish loading after focus. Re-navigate the
        // viewport before each measurement; never conceal horizontal reflow.
        node.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' });
        const r = node.getBoundingClientRect(),
          floor = innerWidth <= 768 ? 44 : 32;
        if (
          r.width < floor ||
          r.height < floor ||
          r.left < 0 ||
          r.top < 0 ||
          r.right > innerWidth + 1 ||
          r.bottom > innerHeight + 1 ||
          scrollX !== 0
        )
          return JSON.stringify({
            reason: 'bounds/target',
            rect: r.toJSON(),
            viewport: [innerWidth, innerHeight],
            scroll: [scrollX, scrollY],
            floor
          });
        const hits = [r.left + 8, r.right - 8].flatMap(x =>
          [r.top + 8, r.bottom - 8].map(y => {
            const hit = document.elementFromPoint(x, y);
            return { owned: hit === node || node.contains(hit), className: hit?.className };
          })
        );
        return hits.every(hit => hit.owned)
          ? 'reachable'
          : JSON.stringify({ reason: 'occluded', rect: r.toJSON(), hits });
      })
    )
    .toBe('reachable');
  await expect(control).toBeFocused();
}

/** Native platform gesture, matching docs/workspace-navigation.md. */
export async function tabToCanvasControl(control: Locator) {
  const page = control.page();
  const key = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+Tab' : 'Tab';
  for (let step = 0; step < 100; step++) {
    if (await control.evaluate(node => node === document.activeElement)) return;
    await page.keyboard.press(key);
  }
  throw new Error(`Native keyboard focus could not reach ${control.toString()}`);
}

/** Scoped DOM checks only, not certification of drawn Canvas content or AT. */
export async function assertCanvasAxe(page: Page, selector: string) {
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const violations = await page.evaluate(async selector => {
    const result = await (window as any).axe.run(document.querySelector(selector), {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
    return result.violations;
  }, selector);
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
}
