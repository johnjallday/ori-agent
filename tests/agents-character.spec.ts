import { test, expect, Page } from '@playwright/test';

/**
 * Curated character selection on the Agents page
 * (PRD cozy-character-experience FR-51–FR-75, FR-88–FR-102).
 *
 * Not part of CI — run against an isolated demo server:
 *   source scripts/wt.sh; wt demo 8931
 *   ./scripts/e2e.sh --port 8931 tests/agents-character.spec.ts
 *
 * The unit tests cover the picker's own logic. What only a browser shows is
 * that a choice survives a round trip through the real API and reload, that
 * cancelling genuinely changes nothing, and that switching identity modes never
 * costs the user their uploaded avatar.
 */

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function makeAgent(page: Page, name: string) {
  const res = await page.request.post('/api/agents', {
    data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
  });
  expect(res.ok()).toBeTruthy();
}

async function openAgent(page: Page, name: string) {
  await skipOnboarding(page);
  await page.goto(`/agents?agent=${encodeURIComponent(name)}`, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#stageName')).toHaveText(name);
}

async function openPickerFromInspector(page: Page) {
  await page.locator('#ov-character-btn').click();
  await expect(page.locator('#charPicker')).toBeVisible();
}

const unique = (p: string) => `${p}${Date.now()}${Math.floor(Math.random() * 1000)}`;

test.describe('choosing a character', () => {
  test('a chosen character persists through a reload', async ({ page }) => {
    const name = unique('PWChar');
    await makeAgent(page, name);
    await openAgent(page, name);

    await openPickerFromInspector(page);
    await page.locator('.char-card', { hasText: 'Research Archivist' }).click();
    await page.locator('#charPickerConfirm').click();

    await expect(page.locator('#stageCharacter')).toContainText('Research Archivist');

    // The real assertion: it survives a round trip, not just a local re-render.
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#stageCharacter')).toContainText('Research Archivist');
    await expect(page.locator('#ov-appearance-mode')).toContainText('Research Archivist');
  });

  test('cancelling changes nothing', async ({ page }) => {
    const name = unique('PWCancel');
    await makeAgent(page, name);
    await openAgent(page, name);

    const before = await page.locator('#ov-appearance-mode').innerText();

    await openPickerFromInspector(page);
    await page.locator('.char-card', { hasText: 'Product Builder' }).click();
    await page.locator('#charPickerCancel').click();
    await expect(page.locator('#charPicker')).toBeHidden();

    await expect(page.locator('#ov-appearance-mode')).toHaveText(before);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#ov-appearance-mode')).toHaveText(before);
  });

  test('choosing a character is appearance only — no voice control anywhere', async ({
    page,
    request
  }) => {
    const name = unique('PWVisualOnly');
    await makeAgent(page, name);
    await openAgent(page, name);

    const before = await (
      await request.get(`/api/agents/${encodeURIComponent(name)}/detail`)
    ).json();

    await openPickerFromInspector(page);
    // The control is gone from the DOM, not merely hidden: a toggle implying a
    // character changes how an agent speaks is the promise this feature removed
    // (FR-19/FR-23).
    await expect(page.locator('#charPickerVoice')).toHaveCount(0);
    await expect(page.locator('#charPicker')).toContainText('Appearance only');

    await page.locator('.char-card', { hasText: 'Team Caretaker' }).click();
    await page.locator('#charPickerConfirm').click();
    await expect(page.locator('#stageCharacter')).toContainText('Team Caretaker');

    const after = await (
      await request.get(`/api/agents/${encodeURIComponent(name)}/detail`)
    ).json();
    // Nothing about how the agent works may have moved (FR-17).
    expect(after.system_prompt).toBe(before.system_prompt);
    expect(after.model).toBe(before.model);
    expect(after.role).toBe(before.role);
    expect(JSON.stringify(after)).not.toContain('voice_enabled');
    expect(JSON.stringify(after)).not.toContain('character_tone');
  });

  test('removing a character returns the agent to its generated appearance', async ({ page }) => {
    const name = unique('PWRemove');
    await makeAgent(page, name);
    await openAgent(page, name);

    await openPickerFromInspector(page);
    await page.locator('.char-card', { hasText: 'Operations Keeper' }).click();
    await page.locator('#charPickerConfirm').click();
    await expect(page.locator('#stageCharacter')).toContainText('Operations Keeper');

    await page.locator('#ov-character-clear').click();
    await expect(page.locator('#ov-appearance-mode')).toContainText('Generated');

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#ov-appearance-mode')).toContainText('Generated');
  });
});

test.describe('the picker itself', () => {
  test('it never offers Ori', async ({ page }) => {
    const name = unique('PWNoOri');
    await makeAgent(page, name);
    await openAgent(page, name);
    await openPickerFromInspector(page);

    // Ori is the guide. Offering it here would be a UI lying about what the
    // server will accept (FR-19/FR-71).
    await expect(page.locator('.char-card', { hasText: /^Ori$/ })).toHaveCount(0);
    const ids = await page
      .locator('.char-card')
      .evaluateAll(els => els.map(el => el.getAttribute('data-char-id')));
    expect(ids).not.toContain('ori-guide');
    expect(ids.length).toBeGreaterThanOrEqual(8);
  });

  test('the preview shows what the user is committing to', async ({ page }) => {
    const name = unique('PWPreview');
    await makeAgent(page, name);
    await openAgent(page, name);
    await openPickerFromInspector(page);

    await page.locator('.char-card', { hasText: 'Automation Specialist' }).click();
    const preview = page.locator('#charPickerPreview');

    // FR-28: silhouette, prop, and idle behaviour — all visual.
    await expect(preview).toContainText('Narrow bird profile');
    await expect(preview).toContainText('Wind-up key');
    // No tone facts and no sample speech: copy describing how a character talks
    // would keep the removed promise alive (FR-23).
    await expect(preview).not.toContainText('Tone');
    await expect(preview).not.toContainText('forty-two seconds');
    // FR-29: the prop is appearance, not a capability.
    await expect(preview).toContainText('Appearance only');
  });

  test('family filters and search narrow the grid', async ({ page }) => {
    const name = unique('PWFilter');
    await makeAgent(page, name);
    await openAgent(page, name);
    await openPickerFromInspector(page);

    const all = await page.locator('.char-card').count();

    await page.locator('.char-picker__family[data-family="construct"]').click();
    const constructs = await page.locator('.char-card').count();
    expect(constructs).toBeGreaterThan(0);
    expect(constructs).toBeLessThan(all);

    await page.locator('.char-picker__family[data-family=""]').click();
    await page.locator('#charPickerSearch').fill('builder');
    await expect(page.locator('.char-card')).toHaveCount(1);
    await expect(page.locator('.char-card')).toContainText('Product Builder');
  });

  test('Escape closes the picker and restores focus to its trigger', async ({ page }) => {
    const name = unique('PWEsc');
    await makeAgent(page, name);
    await openAgent(page, name);
    await openPickerFromInspector(page);

    await page.keyboard.press('Escape');
    await expect(page.locator('#charPicker')).toBeHidden();
    await expect(page.locator('#ov-character-btn')).toBeFocused();
  });
});

test.describe('creating an agent with a character', () => {
  test('a character chosen during creation is saved with the agent', async ({ page }) => {
    await skipOnboarding(page);
    await page.goto('/agents', { waitUntil: 'domcontentloaded' });

    const name = unique('PWCreate');
    await page.locator('#newAgentBtn').click();
    await page.locator('#cr-name').fill(name);

    await page.locator('#cr-character-btn').click();
    await expect(page.locator('#charPicker')).toBeVisible();
    await page.locator('.char-card', { hasText: 'Insight Researcher' }).click();
    await page.locator('#charPickerConfirm').click();

    await expect(page.locator('#cr-character-state')).toContainText('Insight Researcher');
    await page.locator('#createSubmit').click();

    // Persisted in the same successful creation, not a follow-up write (FR-93).
    await expect(page.locator('#stageName')).toHaveText(name);
    await expect(page.locator('#stageCharacter')).toContainText('Insight Researcher');
  });

  test('Skip for now is a first-class path', async ({ page }) => {
    await skipOnboarding(page);
    await page.goto('/agents', { waitUntil: 'domcontentloaded' });

    const name = unique('PWSkip');
    await page.locator('#newAgentBtn').click();
    await page.locator('#cr-name').fill(name);

    await page.locator('#cr-character-btn').click();
    await page.locator('#charPickerSkip').click();
    await expect(page.locator('#cr-character-state')).toContainText('No character chosen');

    await page.locator('#createSubmit').click();
    await expect(page.locator('#stageName')).toHaveText(name);
    // No character means the generated identity, not a broken portrait.
    await expect(page.locator('#ov-appearance-mode')).toContainText('Generated');
    await expect(page.locator('#stageCharacter')).toBeHidden();
  });
});

test.describe('identity does not disturb the rest of the page', () => {
  test('choosing a character leaves checked agents alone', async ({ page }) => {
    const a = unique('PWCheckA');
    const b = unique('PWCheckB');
    await makeAgent(page, a);
    await makeAgent(page, b);
    await openAgent(page, a);

    await page.locator(`.roster-card[data-name="${b}"] .roster-card__check`).check();
    await expect(page.locator('#bulkBar')).toBeVisible();

    await openPickerFromInspector(page);
    await page.locator('.char-card', { hasText: 'Project Coordinator' }).click();
    await page.locator('#charPickerConfirm').click();
    await expect(page.locator('#stageCharacter')).toContainText('Project Coordinator');

    // Opening a picker is not a selection change (FR-95).
    await expect(page.locator(`.roster-card[data-name="${b}"] .roster-card__check`)).toBeChecked();
    await expect(page.locator('#bulkBar')).toBeVisible();
    // ...and the focused agent is still the one we opened.
    await expect(page.locator('#stageName')).toHaveText(a);
  });

  test('a built-in agent offers no character controls', async ({ page }) => {
    await openAgent(page, 'Claude Code');

    // The server would reject an edit, so the UI must not offer one (FR-70/FR-92).
    await expect(page.locator('#ov-character-btn')).toHaveCount(0);
    await expect(page.locator('#ov-character-clear')).toHaveCount(0);
  });

  test('the same identity renders in Gallery and List', async ({ page }) => {
    const name = unique('PWViews');
    await makeAgent(page, name);
    await openAgent(page, name);

    await openPickerFromInspector(page);
    await page.locator('.char-card', { hasText: 'Decision Strategist' }).click();
    await page.locator('#charPickerConfirm').click();

    const gallerySrc = await page
      .locator(`.roster-card[data-name="${name}"] .agent-avatar__portrait`)
      .getAttribute('src');
    expect(gallerySrc).toContain('decision-strategist');

    await page.locator('[data-view="list"]').click();
    const listSrc = await page
      .locator(`.roster-card[data-name="${name}"] .agent-avatar__portrait`)
      .getAttribute('src');
    // Both views resolve through the one renderer, so they cannot disagree
    // about what an agent looks like (FR-99).
    expect(listSrc).toBe(gallerySrc);
  });

  // The fixed Ori launcher has now covered a page control four separate times
  // during this feature (the Agents bulk bar, the map's retry button, the
  // Inspector hero actions, and the roster's last row). Every one of those was
  // found by looking at a screenshot, which is not a repeatable check — so the
  // rule gets asserted instead of re-discovered.
  test('the Ori launcher never covers the end of the roster', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/agents');
    await expect(page.locator('.roster-card').first()).toBeVisible();

    // Scroll to the true end of the page. mouse.wheel is used rather than
    // window.scrollTo because it goes through the browser's real scrolling
    // machinery, which is what a user's last flick does too.
    for (let i = 0; i < 40; i++) await page.mouse.wheel(0, 2000);
    await page.waitForTimeout(300);

    // Measured in the page with getBoundingClientRect so both rects are in the
    // same viewport-relative space as the fixed launcher.
    const geom = await page.evaluate(() => {
      const cards = document.querySelectorAll('.roster-card');
      const last = cards[cards.length - 1].getBoundingClientRect();
      const launcher = document.querySelector('.ori-guide__launcher')!.getBoundingClientRect();
      return { cardBottom: last.bottom, launcherTop: launcher.top };
    });

    // Scrolled to the very bottom there is nowhere further to go, so anything
    // still under the launcher is permanently unclickable.
    expect(geom.cardBottom).toBeLessThanOrEqual(geom.launcherTop);
  });

  // 320px is the narrowest layout the PRD commits to, and a 200%-zoomed 1440
  // desktop lands in roughly the same place — both are where a warm restyle
  // most easily reintroduces a sideways scroll (FR-115–FR-119).
  for (const [label, width, height] of [
    ['320px', 320, 800],
    ['1440 at 200% zoom', 720, 475]
  ] as const) {
    test(`the collection never scrolls sideways at ${label}`, async ({ page }) => {
      await page.setViewportSize({ width, height });
      await page.goto('/agents');
      await expect(page.locator('.roster-card').first()).toBeVisible();

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth
      );
      expect(overflow, `page overflows by ${overflow}px`).toBeLessThanOrEqual(1);

      // The Inspector is the widest thing on the page, so check it too rather
      // than only the collection behind it.
      await page.locator('.roster-card').first().locator('.roster-card__open').click();
      await expect(page.locator('#inspector')).toBeVisible();
      const withInspector = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth
      );
      expect(withInspector, `Inspector overflows by ${withInspector}px`).toBeLessThanOrEqual(1);
    });
  }
});
