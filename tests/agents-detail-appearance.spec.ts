import { test, expect, Page } from '@playwright/test';

/**
 * The Appearance section on the full agent detail page. It used to live in the
 * Edit Agent Profile modal; it is now its own "Changes save immediately"
 * section, because its writes never belonged to the modal's Save and Cancel.
 *
 * This exists because the shared Appearance editor once shipped broken in two
 * ways that a "is it visible?" assertion would have passed:
 *
 *   - It emitted `.btn-ghost`, a class defined only in agents-roster.css, which
 *     this page does not load. The buttons rendered unstyled.
 *   - Its content overflowed the modal's 240px left grid column, and the second
 *     column — later in the DOM — painted over the overflow. "Change character"
 *     was on screen and *unclickable*.
 *
 * So the assertions here are about hit-testing and geometry, not visibility.
 */

const AGENT = `DetailAppearance${Date.now()}`;

async function openAppearanceSection(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.goto(`/agents-detail.html?name=${encodeURIComponent(AGENT)}&tab=appearance`, {
    waitUntil: 'domcontentloaded'
  });
  await expect(page.locator('#appearanceSection')).toContainText('Changes save immediately');
  await expect(page.locator('#agentAppearance-root')).toBeVisible();
  await page.locator('#appearanceSection').scrollIntoViewIfNeeded();
}

test.beforeAll(async ({ request }) => {
  const res = await request.post('/api/agents', {
    data: { name: AGENT, model: 'gpt-4o-mini' }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  await request.delete(`/api/agents?name=${encodeURIComponent(AGENT)}`).catch(() => undefined);
});

test('every Appearance control on the detail page is actually clickable', async ({ page }) => {
  await openAppearanceSection(page);

  // Choosing a character first, so the Change/Remove pair exists — those are
  // the widest labels and the ones that were being covered.
  await page.locator('#agentAppearance-character-choose').click();
  await expect(page.locator('#charPicker')).toBeVisible();
  await page.locator('.char-card', { hasText: 'Research Archivist' }).click();
  await page.locator('#charPickerConfirm').click();
  await expect(page.locator('#agentAppearance-mode-character')).toBeChecked({ timeout: 10000 });

  // Hit-testing: the element at each control's centre must be the control
  // itself (or inside it). This is what "visible but covered" fails.
  const controls = [
    'agentAppearance-mode-generated',
    'agentAppearance-mode-character',
    'agentAppearance-color',
    'agentAppearance-color-reset',
    'agentAppearance-character-choose',
    'agentAppearance-character-remove',
    'agentAppearance-file'
  ];
  for (const id of controls) {
    const covered = await page.evaluate(controlId => {
      const el = document.getElementById(controlId);
      if (!el) return `${controlId}: missing`;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) return `${controlId}: zero-sized`;
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      if (!hit) return `${controlId}: nothing at its centre`;
      return hit === el || el.contains(hit) || hit.contains(el)
        ? ''
        : `${controlId}: covered by <${hit.tagName.toLowerCase()} class="${hit.className}">`;
    }, id);
    expect(covered, covered).toBe('');
  }

  // Playwright's own actionability check is the second opinion: it refuses to
  // click an element another element intercepts.
  await page.locator('#agentAppearance-character-remove').click({ timeout: 5000 });
  await expect(page.locator('#agentAppearance-mode-generated')).toBeChecked();
});

test('the Appearance panel stays inside its section', async ({ page }) => {
  await openAppearanceSection(page);

  // The overflow itself, measured — so a future label or control that does not
  // fit fails here rather than silently covering something.
  const spill = () =>
    page.evaluate(() => {
      const panel = document.querySelector('.agent-appearance-panel');
      const editor = document.getElementById('agentAppearance-root');
      if (!panel || !editor) return 'missing panel or editor';
      const p = panel.getBoundingClientRect();
      const e = editor.getBoundingClientRect();
      const over = Math.round(e.right - p.right);
      if (over > 1) return `editor overflows its panel by ${over}px`;
      // Every visible control and card inside the editor stays inside it too.
      for (const el of Array.from(editor.querySelectorAll('*'))) {
        const r = el.getBoundingClientRect();
        if (r.width === 0) continue;
        const spill = Math.round(r.right - e.right);
        if (spill > 1) return `${el.tagName.toLowerCase()}.${el.className} spills ${spill}px`;
      }
      const page = document.documentElement.scrollWidth - window.innerWidth;
      return page > 0 ? `the page scrolls sideways by ${page}px` : '';
    });
  expect(await spill()).toBe('');

  // The same at phone width, where the section is the full content width.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#agentAppearance-root')).toBeVisible();
  expect(await spill()).toBe('');
});

test('the profile modal no longer holds appearance controls', async ({ page }) => {
  await openAppearanceSection(page);
  await page.locator('#editProfileButton').click();
  await expect(page.locator('#profileEditModal')).toBeVisible();
  await expect(page.locator('#profileEditModal [id^="agentAppearance-"]')).toHaveCount(0);
  await expect(page.locator('#profileEditModal')).toContainText('saves on its own');
});

test('the editor styles its own buttons rather than borrowing the roster stylesheet', async ({
  page
}) => {
  await openAppearanceSection(page);

  // agents-roster.css is not loaded here, so a button relying on `.btn-ghost`
  // would fall back to the browser default. Asserting a real border and
  // padding pins that the shared stylesheet is carrying its own weight.
  const style = await page.evaluate(() => {
    const btn = document.getElementById('agentAppearance-character-choose');
    if (!btn) return null;
    const cs = getComputedStyle(btn);
    return {
      classes: btn.className,
      borderWidth: cs.borderTopWidth,
      borderStyle: cs.borderTopStyle,
      paddingLeft: cs.paddingLeft,
      radius: cs.borderTopLeftRadius
    };
  });
  expect(style).not.toBeNull();
  expect(style!.classes).toContain('appearance-btn');
  expect(style!.classes).not.toContain('btn-ghost');
  expect(style!.borderStyle).toBe('solid');
  expect(parseFloat(style!.borderWidth)).toBeGreaterThan(0);
  expect(parseFloat(style!.paddingLeft)).toBeGreaterThan(4);
  expect(parseFloat(style!.radius)).toBeGreaterThan(0);
});
