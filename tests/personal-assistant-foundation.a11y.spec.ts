import { test, expect, Page } from '@playwright/test';

async function contrastRatio(page: Page, selector: string) {
  return page.locator(selector).evaluate(element => {
    const rgb = (value: string) => {
      const channels = (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
      return value.startsWith('color(srgb') ? channels.map(channel => channel * 255) : channels;
    };
    const luminance = (channels: number[]) => {
      const linear = channels.map(channel => {
        const value = channel / 255;
        return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
    };
    const foreground = rgb(getComputedStyle(element).color);
    let current: Element | null = element;
    let background = [255, 255, 255];
    while (current) {
      const style = getComputedStyle(current);
      if (!style.backgroundColor.endsWith(', 0)') && style.backgroundColor !== 'transparent') {
        background = rgb(style.backgroundColor);
        break;
      }
      current = current.parentElement;
    }
    const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
    return (lighter + 0.05) / (darker + 0.05);
  });
}

async function mockCompletedOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        needs_onboarding: false,
        completed: true,
        current_step: 4,
        steps_completed: ['done']
      })
    })
  );
}

async function mockAssistantState(
  page: Page,
  relationshipState: 'active' | 'paused' | 'needs_hq' | 'repair_needed' = 'active',
  todayState = relationshipState,
  modelAvailable = true,
  displayName = 'Atlas',
  dense = false
) {
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: relationshipState,
          state_version: 7,
          assistant_id: 'assistant-stable',
          display_name: displayName,
          hq_workspace_id: 'hq-1',
          mandate: 'Keep commitments visible.',
          focus_areas: ['plan_my_day'],
          daily_brief: {
            timezone: 'UTC',
            schedule_days: ['mon', 'wed', 'fri'],
            schedule_time: '08:00',
            schedule_enabled: true,
            scope: 'all',
            include_future_workspaces: true,
            notify_on_ready: false,
            config_revision: 3
          },
          availability: {
            model: {
              status: modelAvailable ? 'available' : 'not_configured',
              available: modelAvailable
            }
          }
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        today: {
          state: todayState,
          relationship_state: relationshipState,
          display_name: displayName,
          hq_workspace_id: 'hq-1',
          hq_workspace_slug: 'personal-hq',
          model: {
            status: modelAvailable ? 'available' : 'not_configured',
            available: modelAvailable
          },
          next_check_in: new Date(Date.now() + 86400000).toISOString(),
          // Work under way and a result from today, so the progress row shows.
          working_on: {
            health: { status: 'available' },
            items: [
              {
                id: 'hq',
                kind: 'hq_status',
                title: 'Personal HQ',
                route: '/workspaces/personal-hq'
              },
              {
                id: 'w1',
                kind: 'folder_workspace',
                title: 'Song Sketches',
                route: '/workspaces/song'
              }
            ]
          },
          done: {
            health: { status: 'available' },
            items: [
              {
                id: 'd1',
                kind: 'task_result',
                title: 'Sorted the Samples folder',
                route: '/workspaces/song',
                source_at: new Date().toISOString()
              }
            ]
          },
          brief: { health: { status: 'healthy_empty' }, items: [] },
          decisions: { health: { status: 'healthy_empty' }, items: [] },
          priorities: {
            health: { status: dense ? 'available' : 'healthy_empty' },
            items: dense
              ? Array.from({ length: 10 }, (_, index) => ({
                  title: `A deliberately long priority ${index + 1} that must wrap inside the assistant drawer without widening Home`,
                  detail:
                    'This supporting detail is intentionally verbose so short desktop and phone layouts must use internal scrolling.'
                }))
              : []
          },
          follow_ups: { health: { status: 'healthy_empty' }, items: [] },
          results: { health: { status: 'healthy_empty' }, items: [] },
          links: {
            personal_hq: '/workspaces/personal-hq',
            working_agreement: '/?personal-assistant=working-agreement',
            memory: '/workspaces/personal-hq#memory',
            advanced: '/agents'
          }
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/capabilities', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ capabilities: { state: relationshipState, cards: [] } })
    })
  );
  // A Personal HQ with today's brief, so the drawer's brief row shows. With no
  // HQ (needs_hq) the designation is reported as invalid and the row stays
  // hidden.
  const hasHQ = relationshipState !== 'needs_hq';
  const json = (body: unknown) => ({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body)
  });
  await page.route('**/api/personal-hq/status', route =>
    route.fulfill(
      json({
        status: hasHQ
          ? { valid: true, workspace_id: 'hq-1', workspace: { folder_slug: 'personal-hq' } }
          : { valid: false }
      })
    )
  );
  await page.route('**/api/personal-hq/brief/config', route =>
    route.fulfill(
      json({
        config: {
          timezone: 'UTC',
          schedule_enabled: true,
          schedule_time: '08:00',
          schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']
        }
      })
    )
  );
  await page.route('**/api/personal-hq/brief/current', route =>
    route.fulfill(
      json({
        revision: {
          id: 'rev-1',
          local_date: new Date().toISOString().slice(0, 10),
          status: 'succeeded',
          generated_at: new Date().toISOString(),
          content_json: '{}'
        }
      })
    )
  );
  await page.route('**/api/personal-hq/brief/status', route =>
    route.fulfill(json({ status: 'succeeded' }))
  );
}

// Walks backwards from the composer with Shift+Tab through every control in
// the drawer, as a keyboard user would, and returns what took focus and
// whether each one drew a focus outline.
async function tabBackThroughDrawer(page: Page) {
  const visited: { name: string; outlined: boolean }[] = [];
  for (let step = 0; step < 40; step++) {
    await page.keyboard.press('Shift+Tab');
    const stop = await page.evaluate(() => {
      const active = document.activeElement as HTMLElement | null;
      if (!active || !active.closest('#personalAssistantPanel')) return null;
      const style = getComputedStyle(active);
      return {
        name:
          active.id ||
          active.getAttribute('aria-label') ||
          (active.textContent || '').trim().slice(0, 40) ||
          active.tagName,
        outlined: style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) >= 2
      };
    });
    if (!stop) break; // focus left the drawer: every control has been visited
    visited.push(stop);
  }
  return visited;
}

test.describe('Personal Assistant Foundation accessibility', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
  });

  test('active Home, assistant dialog, and working agreement are keyboard operable', async ({
    page
  }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page);
    await page.goto('/');

    const launcher = page.getByRole('button', { name: /Atlas Personal Assistant/i });
    await expect(launcher).toBeVisible();
    await launcher.focus();
    await page.keyboard.press('Enter');
    const assistantDialog = page.getByRole('dialog', { name: 'Atlas' });
    await expect(assistantDialog).toBeVisible();
    // One view, no tabs: the composer has focus as soon as the drawer opens.
    await expect(assistantDialog.getByRole('tab')).toHaveCount(0);
    await expect(page.locator('#personalAssistantInput')).toBeFocused();
    await expect(page.locator('#personalAssistantPanelStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    await expect(page.locator('#personalAssistantTodaySections')).toBeHidden();
    await page.locator('#personalAssistantSummaryToggle').press('Enter');
    await expect(page.locator('#personalAssistantBriefRow')).toBeVisible();
    await expect(page.locator('#personalAssistantProgressRow')).toBeVisible();
    await page.locator('#personalAssistantInput').focus();

    // Icon-only buttons are named.
    await expect(
      assistantDialog.getByRole('button', { name: 'Close personal assistant' })
    ).toBeVisible();
    await expect(page.locator('#personalAssistantMore > summary')).toHaveAttribute(
      'aria-label',
      'More assistant options'
    );

    // Every control is reachable with the Tab key and shows where focus is.
    // Forward from the composer is Send, the last control in the drawer.
    await page.keyboard.press('Tab');
    await expect(page.locator('#personalAssistantSend')).toBeFocused();
    expect(
      await page
        .locator('#personalAssistantSend')
        .evaluate(el => getComputedStyle(el).outlineStyle !== 'none')
    ).toBe(true);
    await page.locator('#personalAssistantInput').focus();
    const visited = await tabBackThroughDrawer(page);
    const names = visited.map(stop => stop.name);
    for (const expected of [
      'personalAssistantFolderChip',
      'personalAssistantProgressRow',
      'personalAssistantBriefRow',
      'personalAssistantClose',
      'More assistant options'
    ]) {
      expect(names, `Tab never reached ${expected}; it reached ${names.join(', ')}`).toContain(
        expected
      );
    }
    expect(
      visited.filter(stop => !stop.outlined).map(stop => stop.name),
      'controls that took focus without a visible focus outline'
    ).toEqual([]);
    await page.locator('#personalAssistantInput').focus();

    // The progress row is a real disclosure.
    const progress = page.locator('#personalAssistantProgressRow');
    await expect(progress).toHaveAttribute('aria-expanded', 'false');
    await progress.focus();
    await page.keyboard.press('Enter');
    await expect(progress).toHaveAttribute('aria-expanded', 'true');
    await expect(page.locator('#personalAssistantProgressLists')).toBeVisible();
    await page.keyboard.press('Enter');
    await expect(page.locator('#personalAssistantProgressLists')).toBeHidden();

    for (const selector of [
      '#personalAssistantLauncherName',
      '#personalAssistantTodayBanner',
      '#personalAssistantCheckIn',
      '#personalAssistantBriefRowStatus',
      '#personalAssistantBriefRow .personal-assistant-glance__action',
      '#personalAssistantProgressText'
    ]) {
      expect(await contrastRatio(page, selector), `contrast of ${selector}`).toBeGreaterThanOrEqual(
        4.5
      );
    }
    await page.keyboard.press('Escape');
    await expect(assistantDialog).toBeHidden();
    await expect(launcher).toBeFocused();

    await launcher.press('Enter');
    const more = page.locator('#personalAssistantMore > summary');
    await more.focus();
    await more.press('Enter');
    const agreementLink = page.getByRole('link', { name: 'Working agreement' });
    await expect(agreementLink).toBeVisible();
    await more.press('Escape');
    await expect(assistantDialog).toBeVisible();
    await expect(more).toBeFocused();
    await expect(agreementLink).toBeHidden();
    await more.press('Enter');
    await agreementLink.focus();
    await page.keyboard.press('Enter');
    const agreement = page.getByRole('dialog', { name: 'How your assistant works with you' });
    await expect(agreement).toBeVisible();
    // The dialog sits over the map, so it needs a background of its own: an
    // opaque colour, not the see-through default.
    const agreementBackground = await agreement.evaluate(
      el => getComputedStyle(el).backgroundColor
    );
    expect(agreementBackground).toMatch(/^(rgb|color)\(/);
    expect(await contrastRatio(page, '#personalAssistantContinuity h2')).toBeGreaterThanOrEqual(
      4.5
    );
    await expect(page.getByRole('button', { name: 'Close working agreement' })).toBeFocused();
    await expect(page.getByLabel('Mandate')).toBeVisible();
    await expect(page.getByLabel('Brief scope')).toBeVisible();
    await expect(page.locator('#personalAssistantContinuityStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    await page.keyboard.press('Escape');
    await expect(agreement).toBeHidden();
    await expect(assistantDialog).toBeVisible();
    await expect(page.locator('#personalAssistantInput')).toBeFocused();
  });

  test('the chip and the summary strip are operable from the keyboard', async ({ page }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page);
    // The folder read is the server's own; this fixture has no folders to offer.
    await page.route('**/api/personal-assistant/folder-digest', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          folder_digest: {
            offer: null,
            chips: [{ id: 'documents', label: 'Documents' }],
            picker_available: false,
            prompt_first_folder: false
          }
        })
      })
    );
    await page.goto('/');
    const launcher = page.getByRole('button', { name: /Atlas Personal Assistant/i });
    await launcher.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#personalAssistantInput')).toBeFocused();

    // The chip is the control just before the composer, and it has a name.
    const chip = page
      .getByRole('dialog', { name: 'Atlas' })
      .getByRole('button', { name: 'Explore a folder', exact: true });
    await page.keyboard.press('Shift+Tab');
    await expect(chip).toBeFocused();
    await expect(chip).toHaveAttribute('id', 'personalAssistantFolderChip');
    expect(await contrastRatio(page, '#personalAssistantFolderChip')).toBeGreaterThanOrEqual(4.5);

    // Ready attention is folded even before the first message.
    const strip = page.locator('#personalAssistantSummary');
    const toggle = page.locator('#personalAssistantSummaryToggle');
    await expect(strip).toBeVisible();
    await page.keyboard.press('Enter');

    // The request and the assistant's reply are in the conversation, focus is
    // on the first folder, and the chip waits its turn.
    await expect(page.locator('#personalAssistantFolderRequest')).toBeVisible();
    await expect(
      page.locator('#personalAssistantFolderChips button[data-chip="documents"]')
    ).toBeFocused();
    await expect(chip).toBeDisabled();

    // The strip is a real disclosure for what it folded.
    await expect(strip).toBeVisible();
    await expect(toggle).toHaveText('Show');
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(toggle).toHaveAttribute('aria-controls', 'personalAssistantTodaySections');
    await expect(page.locator('#personalAssistantTodaySections')).toBeHidden();
    for (const selector of ['#personalAssistantSummaryText', '#personalAssistantSummaryToggle']) {
      expect(await contrastRatio(page, selector), `contrast of ${selector}`).toBeGreaterThanOrEqual(
        4.5
      );
    }
    // Reached with the keyboard, it shows where focus is.
    await page.locator('#personalAssistantInput').focus();
    const stops = await tabBackThroughDrawer(page);
    expect(stops.map(stop => stop.name)).toContain('personalAssistantSummaryToggle');
    expect(stops.filter(stop => !stop.outlined).map(stop => stop.name)).toEqual([]);
    await toggle.focus();
    await page.keyboard.press('Enter');
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(toggle).toHaveText('Hide');
    await expect(page.locator('#personalAssistantBriefRow')).toBeVisible();
    await page.keyboard.press('Enter');
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(page.locator('#personalAssistantBriefRow')).toBeHidden();
  });

  test('on another page the needs-you line is a named link with readable text', async ({
    page
  }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page);
    // Two things need the user. A page that is not Home says only how many.
    await page.route('**/api/personal-assistant/today', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          today: {
            state: 'active',
            relationship_state: 'active',
            display_name: 'Atlas',
            next_check_in: new Date(Date.now() + 86400000).toISOString(),
            needs_you: {
              health: { status: 'available' },
              items: [
                { kind: 'task', title: 'Pick a release date' },
                { kind: 'follow_up', title: 'Reply to the studio' }
              ]
            },
            links: { personal_hq: '/workspaces/personal-hq', advanced: '/agents' }
          }
        })
      })
    );
    await page.goto('/settings');
    const launcher = page.getByRole('button', { name: /Atlas Personal Assistant/i });
    await launcher.focus();
    await page.keyboard.press('Enter');
    const assistantDialog = page.getByRole('dialog', { name: 'Atlas' });
    await expect(assistantDialog).toBeVisible();
    await expect(page.locator('#personalAssistantInput')).toBeFocused();

    const line = page.locator('#personalAssistantNeedsLine');
    await expect(line).toBeVisible();
    await expect(line).toHaveAttribute('href', '/?panel=today');
    await expect(assistantDialog.getByRole('link', { name: '2 need you' })).toHaveCount(1);
    expect(await contrastRatio(page, '#personalAssistantNeedsLineText')).toBeGreaterThanOrEqual(
      4.5
    );
    // Reached with the keyboard, the line and the chip show where focus is.
    const stops = await tabBackThroughDrawer(page);
    const names = stops.map(stop => stop.name);
    expect(names).toContain('personalAssistantNeedsLine');
    expect(names).toContain('personalAssistantFolderChip');
    expect(stops.filter(stop => !stop.outlined).map(stop => stop.name)).toEqual([]);
    // The header works here too: the check-in line and the More links.
    await expect(page.locator('#personalAssistantCheckIn')).not.toHaveText('Personal Assistant');
    await expect(page.locator('#personalAssistantMore > summary')).toHaveAttribute(
      'aria-label',
      'More assistant options'
    );
    // No Today of its own, and no tabs.
    await expect(page.locator('#personalAssistantToday')).toHaveCount(0);
    await expect(assistantDialog.getByRole('tab')).toHaveCount(0);
  });

  for (const scenario of [
    {
      name: 'paused',
      relationship: 'paused' as const,
      today: 'paused',
      model: true,
      copy: /Paused/i,
      cue: /Paused/i
    },
    {
      name: 'partial',
      relationship: 'active' as const,
      today: 'partial',
      model: true,
      copy: /partial|unavailable|current/i,
      cue: /Sources unavailable/i
    },
    {
      name: 'no model',
      relationship: 'active' as const,
      today: 'model_unavailable',
      model: false,
      copy: /model|deterministic/i,
      cue: /Model unavailable/i
    },
    {
      name: 'repair',
      relationship: 'repair_needed' as const,
      today: 'repair_needed',
      model: true,
      copy: /repair/i,
      cue: /Repair needed/i
    }
  ]) {
    test(`${scenario.name} state is stated in text and never color alone`, async ({ page }) => {
      await mockCompletedOnboarding(page);
      await mockAssistantState(page, scenario.relationship, scenario.today, scenario.model);
      await page.goto('/');
      const launcher = page.locator('#personalAssistantLauncher');
      await expect(launcher).toBeVisible();
      await expect(page.locator('#personalAssistantLauncherStatus')).toContainText(scenario.cue);
      await launcher.click();
      await expect(page.locator('#personalAssistantToday')).toBeVisible();
      await expect(page.locator('#personalAssistantToday')).toContainText(scenario.copy);
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth
      );
      expect(overflow).toBe(false);
    });
  }

  test('drawer and bottom sheet stay bounded with long content at supported viewports', async ({
    page
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await mockCompletedOnboarding(page);
    await mockAssistantState(
      page,
      'active',
      'active',
      true,
      'Atlas with a deliberately long assistant name that must wrap safely',
      true
    );
    await page.goto('/');
    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantToday')).toBeVisible();

    // Exercise the real shared renderer with a long, structured reply. This
    // is a layout fixture, not a model invocation. Only the outer drawer scrolls.
    await page.evaluate(() => {
      (window as any).OriAskRouting.appendMessage(
        'assistant',
        JSON.stringify({ notes: Array.from({ length: 80 }, (_, i) => `Long observation ${i}`) })
      );
    });
    await expect(page.locator('#homeAssistantThinkingModalLabel')).toBeHidden();

    for (const viewport of [
      { width: 1440, height: 900, mode: 'drawer' },
      { width: 1280, height: 600, mode: 'drawer' },
      { width: 900, height: 700, mode: 'drawer' },
      { width: 390, height: 844, mode: 'sheet' }
    ]) {
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.evaluate(
        () =>
          new Promise<void>(resolve =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
          )
      );
      await expect
        .poll(() => page.evaluate(() => document.documentElement.scrollWidth))
        .toBeLessThanOrEqual(viewport.width + 1);
      const layout = await page.evaluate(() => {
        const panel = document.getElementById('personalAssistantPanel')!.getBoundingClientRect();
        const close = document.getElementById('personalAssistantClose')!.getBoundingClientRect();
        const navbar = document.querySelector('nav.navbar')!.getBoundingClientRect();
        const view = document.getElementById('personalAssistantScroll')!;
        const composer = document.getElementById('personalAssistantForm')!.getBoundingClientRect();
        return {
          composer: { top: composer.top, bottom: composer.bottom, height: composer.height },
          panel: {
            left: panel.left,
            top: panel.top,
            right: panel.right,
            bottom: panel.bottom,
            width: panel.width
          },
          close: { top: close.top, right: close.right, bottom: close.bottom },
          navbarBottom: navbar.bottom,
          internallyScrollable: view.scrollHeight > view.clientHeight,
          viewOverflowY: getComputedStyle(view).overflowY,
          logOverflowY: getComputedStyle(document.getElementById('homeAssistantConversation')!)
            .overflowY,
          bubbleOverflowY: getComputedStyle(
            document.querySelector('#homeAssistantConversation [data-message-role] > div')!
          ).overflowY,
          pageWidth: document.documentElement.scrollWidth
        };
      });
      expect(
        layout.panel.top,
        `${viewport.width}x${viewport.height} drawer must clear navbar at ${layout.navbarBottom}`
      ).toBeGreaterThanOrEqual(layout.navbarBottom - 1);
      expect(layout.panel.right).toBeLessThanOrEqual(viewport.width + 1);
      expect(layout.panel.bottom).toBeLessThanOrEqual(viewport.height + 1);
      expect(layout.close.top).toBeGreaterThanOrEqual(layout.panel.top);
      expect(layout.close.right).toBeLessThanOrEqual(viewport.width);
      expect(layout.close.bottom).toBeLessThanOrEqual(viewport.height);
      expect(layout.pageWidth).toBeLessThanOrEqual(viewport.width + 1);
      // What is above the composer may fit without scrolling; when it grows,
      // scrolling stays inside the drawer rather than on the page.
      expect(['auto', 'scroll']).toContain(layout.viewOverflowY);
      expect(layout.internallyScrollable).toBe(true);
      expect(layout.logOverflowY).toBe('visible');
      expect(layout.bubbleOverflowY).toBe('visible');
      // The composer is always visible at the bottom of the drawer, however
      // much is above it.
      expect(layout.composer.height).toBeGreaterThan(40);
      expect(layout.composer.top).toBeGreaterThanOrEqual(layout.panel.top);
      expect(
        layout.composer.bottom,
        `${viewport.width}x${viewport.height} composer must stay inside the drawer`
      ).toBeLessThanOrEqual(layout.panel.bottom + 1);
      if (viewport.mode === 'sheet') {
        expect(layout.panel.left).toBeLessThanOrEqual(1);
        expect(layout.panel.width).toBeGreaterThanOrEqual(viewport.width - 1);
      } else {
        expect(layout.panel.left).toBeGreaterThanOrEqual(Math.min(180, viewport.width * 0.2));
      }
    }
  });

  test('needs-HQ guidance opens from the named launcher without enabling the composer', async ({
    page
  }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page, 'needs_hq');
    await page.goto('/');

    const launcher = page.locator('#personalAssistantLauncher');
    await expect(launcher).toBeVisible();
    await expect(page.locator('#personalAssistantLauncherStatus')).toHaveText('Build HQ');
    await launcher.click();
    await expect(page.locator('#personalAssistantToday')).toBeVisible();
    await expect(page.locator('#personalAssistantHQCard')).toBeVisible();
    await expect(page.locator('#personalAssistantHQHeadline')).toContainText('Atlas is hired');
    await expect(page.getByRole('link', { name: 'Build Personal HQ' })).toHaveCount(0);
    // The composer is there but closed until HQ exists, so focus went to the
    // first control in the drawer instead.
    await expect(page.locator('#personalAssistantInput')).toBeVisible();
    await expect(page.locator('#personalAssistantInput')).toBeDisabled();
    await expect(page.locator('#personalAssistantSend')).toBeDisabled();
    await expect(page.locator('#personalAssistantMore > summary')).toBeFocused();
    // The HQ confirmation is what needs the user, and the heading's number is
    // what is listed: that card, plus whatever "Also needs you" holds.
    await expect(page.locator('#personalAssistantNeedsYou')).toBeVisible();
    const queue = page.locator('#personalAssistantNeedsYouQueue');
    const queued = (await queue.isVisible())
      ? Number(
          /\((\d+)\)/.exec(
            await page.locator('#personalAssistantNeedsYouQueueTitle').innerText()
          )?.[1] ?? 0
        )
      : 0;
    await expect(page.locator('#personalAssistantNeedsYouCount')).toHaveText(String(1 + queued));
    // No HQ means no brief to point at.
    await expect(page.locator('#personalAssistantBriefRow')).toBeHidden();
  });

  test('no-model onboarding exposes named controls and no hire', async ({ page }) => {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          completed: false,
          current_step: 0,
          steps_completed: [],
          timezone: 'UTC'
        })
      })
    );
    await page.route('**/api/providers', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"providers":[]}' })
    );
    await page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{"personal_assistant":{"state":"needs_hire","state_version":1,"availability":{"model":{"status":"not_configured","available":false}}}}'
      })
    );
    await page.goto('/');
    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.getByLabel('Your name')).toBeVisible();
    // The Welcome field names Ori the guide, not the assistant hired later.
    await expect(page.getByLabel('What should Ori be called?')).toHaveValue('Ori');
    // The hire is not in the modal any more: it happens on the Agents page.
    await expect(page.locator('#pafAssistantName')).toHaveCount(0);
    await expect(page.locator('#pafHireConfirm')).toHaveCount(0);
    await expect(page.locator('#pafAssignmentStatus')).toHaveAttribute('aria-live', 'polite');
  });

  // Mission 01's preset in the Agents page's New Agent panel: every control is
  // labelled, focus starts in the name, Hire is the one confirmation and says
  // when it is busy, and a failure is announced where focus lands.
  test('the hire preset is labelled, announces busy, and focuses its error', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    let hireCalls = 0;
    const requestIDs: string[] = [];
    await mockCompletedOnboarding(page);
    await page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{"personal_assistant":{"state":"needs_hire","state_version":1,"availability":{"model":{"status":"not_configured","available":false}}}}'
      })
    );
    await page.route('**/api/personal-assistant/hire', async route => {
      hireCalls += 1;
      requestIDs.push(route.request().postDataJSON().request_id);
      await new Promise(resolve => setTimeout(resolve, 400));
      if (hireCalls === 1) {
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'hire_conflict',
            error:
              'This hire conflicts with the current assistant relationship. Refresh and try again.'
          })
        });
        return;
      }
      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({ personal_assistant: { state: 'needs_hq', display_name: 'Atlas' } })
      });
    });

    await page.goto('/agents');
    await page.locator('#newAgentBtn').click();
    const name = page.getByLabel('Name', { exact: true });
    await expect(name).toBeFocused();
    await expect(name).toHaveAttribute('maxlength', '100');
    await expect(name).toHaveAttribute('required', '');
    await expect(page.getByRole('group', { name: 'What should they help with?' })).toBeVisible();
    await expect(page.getByLabel('Plan my day')).toBeChecked();
    const mandate = page.getByLabel('What would make them useful this week? (optional)');
    await expect(mandate).toHaveAttribute('maxlength', '1000');
    // No confirmation checkbox: the boundary line sits above the one button.
    await expect(page.locator('#addAgentModal')).toContainText(
      'Hiring creates your assistant. It does not create a workspace'
    );
    const hire = page.getByRole('button', { name: 'Hire assistant' });
    await expect(hire).toBeEnabled();

    await name.fill('');
    await expect(hire).toBeDisabled();
    await name.fill('Atlas');
    await expect(hire).toBeEnabled();

    await hire.click();
    await expect(page.locator('#createSubmit')).toHaveAttribute('aria-busy', 'true');
    await expect(page.locator('#createSubmit')).toBeDisabled();
    const alert = page.getByRole('alert').filter({ hasText: 'conflicts' });
    await expect(alert).toBeVisible();
    await expect(alert).toBeFocused();
    await expect(page.locator('#createSubmit')).not.toHaveAttribute('aria-busy', 'true');
    await expect(page.locator('#createSubmit')).toBeEnabled();

    await page.locator('#createSubmit').click();
    await page.waitForURL(url => url.pathname === '/');
    expect(hireCalls).toBe(2);
    // The retry replays the same request, so it can never hire a second assistant.
    expect(requestIDs[0]).toBeTruthy();
    expect(requestIDs[1]).toBe(requestIDs[0]);
  });

  // Reduced motion is the ambient condition for this whole describe block
  // (beforeEach), so completing the guided quest here also proves it needs no
  // pulse animation: outline/focus/text carry the walkthrough on their own.
  test('the guided HQ quest is keyboard-only operable under reduced motion, with a fixed Ori identity and a restrained live region', async ({
    page
  }) => {
    let relationshipState = 'needs_hq';
    let hqValid = false;
    let hqSetupCalls = 0;
    await mockCompletedOnboarding(page);
    await page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: relationshipState,
            state_version: 2,
            next_action: relationshipState === 'needs_hq' ? 'build_hq' : 'ask',
            assistant_id: 'assistant-stable',
            display_name: 'Atlas',
            global_agent_profile_name: 'Atlas',
            hq_workspace_id: relationshipState === 'active' ? 'hq-1' : undefined,
            hq_entry_agent_instance_id: relationshipState === 'active' ? 'entry-1' : undefined,
            availability: { model: { status: 'not_configured', available: false } }
          }
        })
      })
    );
    await page.route('**/api/personal-hq/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: {
            user_id: 'local',
            valid: hqValid,
            hq_onboarding_state: hqValid ? 'completed' : 'unseen'
          }
        })
      })
    );
    await page.route('**/api/personal-assistant/hq', async route => {
      hqSetupCalls += 1;
      relationshipState = 'active';
      hqValid = true;
      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          personal_assistant: {
            state: 'active',
            next_action: 'ask',
            assistant_id: 'assistant-stable',
            display_name: 'Atlas',
            global_agent_profile_name: 'Atlas',
            hq_workspace_id: 'hq-1',
            hq_entry_agent_instance_id: 'entry-1',
            state_version: 3,
            daily_brief: { timezone: 'UTC', schedule_days: ['mon'], schedule_time: '08:00' },
            resumed: false
          }
        })
      });
    });
    await page.route('**/api/settings/workspace-root', async route => {
      const confirmed = route.request().method() === 'POST';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: confirmed || undefined,
          workspace_root: '/tmp/paf-workspaces',
          effective_workspace_root: '/tmp/paf-workspaces',
          default_workspace_root: '/tmp/paf-workspaces',
          source: confirmed ? 'settings' : 'unconfirmed',
          confirmed
        })
      });
    });
    await page.route('**/api/progression', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 6,
          total_count: 17,
          completed_count: relationshipState === 'active' ? 1 : 0,
          resolved_count: relationshipState === 'active' ? 1 : 0,
          all_complete: false,
          dismissed: false,
          tiers: [
            {
              tier: 2,
              name: 'Establish a Base',
              complete: false,
              quests: [
                {
                  id: 't2-build-hq',
                  tier: 2,
                  title: 'Build My HQ',
                  status: relationshipState === 'active' ? 'completed' : 'available',
                  action_url: '/?quest=build-hq',
                  action_label: 'Build My HQ',
                  optional: true
                }
              ]
            }
          ]
        })
      })
    );

    await page.goto('/?quest=build-hq');
    await expect(page.locator('#onboardingModal')).toBeHidden();

    // The walkthrough runs in Ori's layer: a spotlight on the site, then Ori's
    // callout beside each dialog. Fixed Ori identity: this is the deterministic
    // guide, not the hired assistant, and it says so.
    const layer = page.locator('#oriSpotlight');
    const callout = layer.locator('.ori-spotlight__callout');
    await expect(layer).toHaveAttribute('data-mode', 'spotlight');
    await expect(callout).toHaveAttribute('role', 'dialog');
    await expect(callout).toHaveAttribute('aria-labelledby', 'oriSpotlightTitle');
    await expect(callout.locator('.ori-spotlight__callout-name')).toHaveText('Ori');
    await expect(callout.locator('.ori-spotlight__callout-step')).toHaveText('Step 1 of 3');
    await expect(page.locator('#oriGuidePanel')).toBeHidden();

    // Live-region restraint: one polite status region reads each step, not an
    // assertive one that would interrupt the user for routine step copy.
    const live = layer.locator('[aria-live]');
    await expect(live).toHaveCount(1);
    await expect(live).toHaveAttribute('role', 'status');
    await expect(live).toHaveAttribute('aria-live', 'polite');

    // Step 1: focus lands on the reserved site without a click.
    const hqSite = page.locator('[data-hq-site]');
    await expect(hqSite).toBeVisible();
    await expect(hqSite).toBeFocused();
    await expect(live).toContainText('Select the Personal HQ site');
    await expect(live).toContainText('Atlas’s home base');

    // The pointer is decoration over the coachmark: hidden from assistive tech,
    // not focusable, and unable to swallow the click it points at. The outline
    // and the panel copy still carry the meaning without it.
    const hand = page.locator('.ori-pointer');
    await expect(hand).toHaveAttribute('aria-hidden', 'true');
    await expect(hand).toHaveCSS('pointer-events', 'none');
    expect(await hand.evaluate(el => el.contains(document.activeElement))).toBe(false);
    // Under reduced motion it stays as a static "here" marker, without movement.
    expect(await hand.evaluate(el => getComputedStyle(el).animationName)).toBe('none');

    // Keyboard-only from here: Enter selects the site. The site's dialog dims
    // the page itself, so Ori's callout stands beside it without a second dim.
    await page.keyboard.press('Enter');
    const buildAction = page.locator('[data-hq-action="build"]');
    await expect(buildAction).toBeVisible();
    await expect(layer).toHaveAttribute('data-mode', 'callout');
    await expect(callout.locator('.ori-spotlight__callout-title')).toHaveText('Open Build My HQ');
    await expect(buildAction).toHaveClass(/is-ori-coachmark/);

    // Keyboard-only: Tab to the Build action (or activate it directly if
    // already focused by the coachmark) and press Enter/Space to open the form.
    if (!(await buildAction.evaluate(el => el === document.activeElement))) {
      await buildAction.focus();
    }
    await page.keyboard.press('Enter');

    await expect(page.locator('#hqBuildModal')).toBeVisible();
    await expect(callout.locator('.ori-spotlight__callout-title')).toHaveText('Review and confirm');
    await expect(callout).toContainText('Nothing is created until you confirm');
    // The form is the user's: Ori marks nothing in it and offers no second way
    // out beside its own Cancel.
    await expect(page.locator('#hqBuildModal .is-ori-coachmark')).toHaveCount(0);
    await expect(callout.locator('button')).toHaveCount(0);

    // Keyboard-only completion of the form itself.
    await page.locator('#hqBuildName').focus();
    await page.keyboard.press('Control+A');
    await page.keyboard.type('Command Post');
    await page.locator('#hqBuildSubmitBtn').focus();
    await page.keyboard.press('Enter');

    await expect.poll(() => hqSetupCalls).toBe(1);
    await expect(page.locator('#hqBuildModal')).toBeHidden();

    // Nothing here relied on the pulse/scroll animation reduced motion turns
    // off: every assertion above was outline, focus, or text.
  });
});
