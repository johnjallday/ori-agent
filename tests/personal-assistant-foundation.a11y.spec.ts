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
    const todayTab = page.getByRole('tab', { name: 'Today' });
    const askTab = page.getByRole('tab', { name: 'Ask' });
    await expect(todayTab).toBeFocused();
    await expect(todayTab).toHaveAttribute('aria-selected', 'true');
    await todayTab.press('ArrowRight');
    await expect(askTab).toBeFocused();
    await expect(askTab).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('#personalAssistantInput')).not.toBeFocused();
    await askTab.press('Home');
    await expect(todayTab).toBeFocused();
    await todayTab.press('End');
    await expect(askTab).toBeFocused();
    await expect(page.locator('#personalAssistantInput')).not.toBeFocused();
    await askTab.press('Enter');
    await expect(page.locator('#personalAssistantInput')).toBeFocused();
    await expect(page.locator('#personalAssistantPanelStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    expect(await contrastRatio(page, '#personalAssistantLauncherName')).toBeGreaterThanOrEqual(4.5);
    expect(await contrastRatio(page, '#personalAssistantTodayBanner')).toBeGreaterThanOrEqual(4.5);
    await page.keyboard.press('Escape');
    await expect(assistantDialog).toBeHidden();
    await expect(launcher).toBeFocused();

    await launcher.press('Enter');
    const agreementLink = page.getByRole('link', { name: 'Working agreement' });
    await agreementLink.focus();
    await page.keyboard.press('Enter');
    const agreement = page.getByRole('dialog', { name: 'How your assistant works with you' });
    await expect(agreement).toBeVisible();
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
    await expect(todayTab).toBeFocused();
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
        const view = document.getElementById('personalAssistantTodayPanel')!;
        return {
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
      expect(layout.internallyScrollable).toBe(true);
      if (viewport.mode === 'sheet') {
        expect(layout.panel.left).toBeLessThanOrEqual(1);
        expect(layout.panel.width).toBeGreaterThanOrEqual(viewport.width - 1);
      } else {
        expect(layout.panel.left).toBeGreaterThanOrEqual(Math.min(180, viewport.width * 0.2));
      }
    }
  });

  test('needs-HQ guidance opens from the named launcher without enabling Ask', async ({ page }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page, 'needs_hq');
    await page.goto('/');

    const launcher = page.locator('#personalAssistantLauncher');
    await expect(launcher).toBeVisible();
    await expect(page.locator('#personalAssistantLauncherStatus')).toHaveText('Build HQ');
    await launcher.click();
    await expect(page.locator('#personalAssistantToday')).toBeVisible();
    await expect(page.locator('#personalAssistantTodayBanner')).toContainText('needs a home base');
    await expect(page.getByRole('link', { name: 'Build Personal HQ' })).toHaveCount(1);
    await page.locator('#personalAssistantAskTab').click();
    await expect(page.locator('#personalAssistantInput')).toBeDisabled();
    await expect(page.locator('#personalAssistantSend')).toBeDisabled();
  });

  test('no-model onboarding exposes named, associated hire and preview controls', async ({
    page
  }) => {
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
    await expect(page.locator('#pafAssistantName')).toHaveAttribute('maxlength', '100');
    await expect(page.locator('#pafHireConfirm')).toHaveAttribute('type', 'checkbox');
    await expect(page.locator('#pafAssignmentStatus')).toHaveAttribute('aria-live', 'polite');
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

    // Fixed Ori/app-guide identity: this is the deterministic guide, not the
    // hired assistant, and it says so.
    await expect(page.locator('#oriGuidePanel')).toBeVisible();
    await expect(page.locator('#oriGuideTitle')).toHaveText('Ori');
    await expect(page.locator('#oriGuideRole')).toBeVisible();
    await expect(page.locator('#oriGuideRole')).toHaveText('App Guide');

    // Live-region restraint: the reply is a single polite status region, not an
    // assertive one that would interrupt the user for routine step copy.
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('role', 'status');
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('aria-live', 'polite');

    // Step 1: focus lands on the reserved site without a click.
    const hqSite = page.locator('[data-hq-site]');
    await expect(hqSite).toBeVisible();
    await expect(hqSite).toBeFocused();
    await expect(page.locator('#oriGuideReply')).toContainText('Atlas is hired');

    // The pointer is decoration over the coachmark: hidden from assistive tech,
    // not focusable, and unable to swallow the click it points at. The outline
    // and the panel copy still carry the meaning without it.
    const hand = page.locator('.ori-pointer');
    await expect(hand).toHaveAttribute('aria-hidden', 'true');
    await expect(hand).toHaveCSS('pointer-events', 'none');
    expect(await hand.evaluate(el => el.contains(document.activeElement))).toBe(false);
    // Under reduced motion it stays as a static "here" marker, without movement.
    expect(await hand.evaluate(el => getComputedStyle(el).animationName)).toBe('none');

    // Keyboard-only from here: Enter selects the site.
    await page.keyboard.press('Enter');
    const buildAction = page.locator('[data-hq-action="build"]');
    await expect(buildAction).toBeVisible();
    await expect(page.locator('#oriGuideReply')).toContainText('open Build My HQ');

    // Keyboard-only: Tab to the Build action (or activate it directly if
    // already focused by the coachmark) and press Enter/Space to open the form.
    if (!(await buildAction.evaluate(el => el === document.activeElement))) {
      await buildAction.focus();
    }
    await page.keyboard.press('Enter');

    await expect(page.locator('#hqBuildModal')).toBeVisible();
    await expect(page.locator('#oriGuideReply')).toContainText(
      'Nothing is created until you confirm'
    );

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
