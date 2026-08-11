import { test, expect, Page } from '@playwright/test';

/**
 * The Edit Agent Profile modal on the full agent detail page.
 *
 * This exists because the shared Appearance editor shipped there broken in two
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

async function openProfileModal(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.goto(`/agents-detail.html?name=${encodeURIComponent(AGENT)}`, {
    waitUntil: 'domcontentloaded'
  });
  await page.locator('#editProfileButton').click();
  await expect(page.locator('#profileEditModal')).toBeVisible();
  await expect(page.locator('#profileAppearance-root')).toBeVisible();
}

test.beforeAll(async ({ request }) => {
  const res = await request.post('/api/agents', {
    data: { name: AGENT, type: 'tool-calling', model: 'gpt-4o-mini' }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  await request.delete(`/api/agents?name=${encodeURIComponent(AGENT)}`).catch(() => undefined);
});

test('every Appearance control in the profile modal is actually clickable', async ({ page }) => {
  await openProfileModal(page);

  // Choosing a character first, so the Change/Remove pair exists — those are
  // the widest labels and the ones that were being covered.
  await page.locator('#profileAppearance-character-choose').click();
  await expect(page.locator('#charPicker')).toBeVisible();
  await page.locator('.char-card', { hasText: 'Research Archivist' }).click();
  await page.locator('#charPickerConfirm').click();
  await expect(page.locator('#profileAppearance-mode-character')).toBeChecked({ timeout: 10000 });

  // Hit-testing: the element at each control's centre must be the control
  // itself (or inside it). This is what "visible but covered" fails.
  const controls = [
    'profileAppearance-mode-generated',
    'profileAppearance-mode-character',
    'profileAppearance-color',
    'profileAppearance-color-reset',
    'profileAppearance-character-choose',
    'profileAppearance-character-remove',
    'profileAppearance-file'
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
  await page.locator('#profileAppearance-character-remove').click({ timeout: 5000 });
  await expect(page.locator('#profileAppearance-mode-generated')).toBeChecked();
});

test('the Appearance panel stays inside its grid column', async ({ page }) => {
  await openProfileModal(page);

  // The overflow itself, measured — so a future label or control that does not
  // fit fails here rather than silently covering something.
  const overflow = await page.evaluate(() => {
    const panel = document.querySelector('.agent-profile-avatar-panel');
    const editor = document.getElementById('profileAppearance-root');
    if (!panel || !editor) return 'missing panel or editor';
    const p = panel.getBoundingClientRect();
    const e = editor.getBoundingClientRect();
    const spill = Math.round(e.right - p.right);
    return spill > 1 ? `editor overflows its panel by ${spill}px` : '';
  });
  expect(overflow, overflow).toBe('');

  // And the panel itself must not overlap the form column beside it.
  const overlap = await page.evaluate(() => {
    const cols = document.querySelectorAll('.agent-profile-modal-grid > *');
    if (cols.length < 2) return 'expected two columns';
    const a = cols[0].getBoundingClientRect();
    const b = cols[1].getBoundingClientRect();
    const gap = Math.round(b.left - a.right);
    return gap < 0 ? `columns overlap by ${-gap}px` : '';
  });
  expect(overlap, overlap).toBe('');
});

test('the editor styles its own buttons rather than borrowing the roster stylesheet', async ({
  page
}) => {
  await openProfileModal(page);

  // agents-roster.css is not loaded here, so a button relying on `.btn-ghost`
  // would fall back to the browser default. Asserting a real border and
  // padding pins that the shared stylesheet is carrying its own weight.
  const style = await page.evaluate(() => {
    const btn = document.getElementById('profileAppearance-character-choose');
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
