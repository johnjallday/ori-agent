import { test, expect, type Page } from '@playwright/test';
import { Buffer } from 'node:buffer';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

async function captureImplementationScreenshot(page, name: string) {
  const directory = process.env.ORI_CAPTURE_SCREENSHOTS;
  if (!directory) return;
  mkdirSync(directory, { recursive: true });
  await page.screenshot({ path: join(directory, name), fullPage: false });
}

async function installActivePersonalAssistant(page: Page) {
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: 'active',
          state_version: 7,
          assistant_id: 'assistant-smoke',
          display_name: 'Atlas',
          hq_workspace_id: 'hq-smoke',
          availability: { model: { status: 'available', available: true } }
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
          state: 'active',
          relationship_state: 'active',
          display_name: 'Atlas',
          model: { status: 'available', available: true },
          brief: { health: { status: 'healthy_empty' }, items: [] },
          decisions: { health: { status: 'healthy_empty' }, items: [] },
          priorities: { health: { status: 'healthy_empty' }, items: [] },
          follow_ups: { health: { status: 'healthy_empty' }, items: [] },
          results: { health: { status: 'healthy_empty' }, items: [] },
          links: { advanced: '/agents' }
        }
      })
    })
  );
}

async function openPersonalAssistantAsk(page: Page) {
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantAskTab').click();
  await expect(page.locator('#personalAssistantAskPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toBeFocused();
}

/**
 * Smoke tests to verify basic frontend functionality.
 * Run with: npx playwright test tests/smoke.spec.ts
 */

test.describe('Smoke Tests', () => {
  test('homepage loads successfully', async ({ page }) => {
    await page.goto('/');

    // Check page title or main content loads
    await expect(page.locator('body')).toBeVisible();

    // Verify navbar is present
    await expect(page.locator('nav, .navbar, [role="navigation"]').first()).toBeVisible();

    // Check no console errors (optional - remove if noisy)
    const errors: string[] = [];
    page.on('console', msg => {
      if (msg.type() === 'error') {
        errors.push(msg.text());
      }
    });

    // Filter out expected errors (add patterns as needed)
    const unexpectedErrors = errors.filter(e => !e.includes('favicon') && !e.includes('404'));

    expect(unexpectedErrors).toHaveLength(0);
  });

  test('theme toggle works', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('html')).toBeAttached();

    // Get initial theme
    const html = page.locator('html');
    const initialTheme = await html.getAttribute('data-bs-theme');

    // Find and click theme toggle (adjust selector based on your UI)
    const themeToggle = page.locator('[data-theme-toggle], #themeToggle, .theme-toggle').first();

    if (await themeToggle.isVisible()) {
      await themeToggle.click();

      // Verify theme changed
      const newTheme = await html.getAttribute('data-bs-theme');
      expect(newTheme).not.toBe(initialTheme);
    }
  });

  test('home exposes the shared sidebar navigation', async ({ page }) => {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      })
    );
    await page.goto('/');

    await expect(page.locator('.navbar').first()).toBeVisible();
    const sidebar = page.locator('#sidebar');
    const sidebarToggle = page.locator('#sidebarToggle');

    await expect(sidebar).toHaveCount(1);
    await expect(sidebarToggle).toBeVisible();
    await expect(sidebarToggle).toHaveAttribute('aria-expanded', 'false');

    await sidebarToggle.click();

    await expect(sidebar).toBeVisible();
    await expect(sidebarToggle).toHaveAttribute('aria-expanded', 'true');
    await expect(sidebar.getByRole('link', { name: 'Workflows' })).toBeVisible();
  });
});

test.describe('Onboarding', () => {
  async function installBaseOnboardingRoutes(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          current_step: 0,
          completed: false,
          skipped: false,
          steps_completed: [],
          user_name: '',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route('**/api/onboarding/names', async route => {
      const body = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          user_name: body.user_name || '',
          assistant_name: body.assistant_name || 'Ori'
        })
      });
    });
    await page.route('**/api/onboarding/step', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });
    await page.route('**/api/settings/workspace-root', async route => {
      const confirmed = route.request().method() === 'POST';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: confirmed || undefined,
          workspace_root: confirmed ? '/tmp/ori-test-workspaces' : '',
          effective_workspace_root: confirmed ? '/tmp/ori-test-workspaces' : '',
          default_workspace_root: '/tmp/ori-test-workspaces',
          source: confirmed ? 'settings' : 'unconfirmed',
          confirmed
        })
      });
    });
  }

  test('collects identity and explicit workspace-directory consent on the first step', async ({
    page
  }) => {
    await installBaseOnboardingRoutes(page);
    await page.goto('/');

    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 1 of 3');
    await expect(page.locator('#onboardingWorkspaceRootInput')).toHaveValue(
      '/tmp/ori-test-workspaces'
    );
    await expect(page.locator('#onboardingVaultRoot')).toHaveCount(0);
    await expect(
      page.getByText(
        'Ori only scans the workspace directory you confirm. You can change it later in Settings.'
      )
    ).toBeVisible();
    await expect(page.locator('#onboardingWorkspaceRootStatus')).toContainText('will not scan');
    await expect(page.getByRole('button', { name: 'Set Up Later' })).toBeVisible();
  });

  test('auto-selects a recommended model before continuing', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: [{ name: 'ollama', display_name: 'Ollama', available: true }]
        })
      });
    });
    await page.route('**/api/settings/available-models?provider=ollama', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          available: true,
          model_options: [
            { id: 'llama-small', label: 'Llama Small', description: 'Fast', recommended: false },
            {
              id: 'llama-balanced',
              label: 'Llama Balanced',
              description: 'Recommended',
              recommended: true
            }
          ]
        })
      });
    });

    await page.goto('/');
    await page.locator('#onboardingUserName').fill('Jamie');
    await page.locator('#welcomeNextBtn').click();

    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 2 of 3');
    await expect(page.locator('#onboardingSystemProvider')).toHaveValue('ollama');
    await expect(page.locator('#onboardingSystemModel')).toHaveValue('llama-balanced');
    await expect(page.locator('#modelNextBtn')).toBeEnabled();
    await expect(page.locator('#modelBackBtn')).toBeVisible();
  });

  test('offers a deterministic onboarding path when no model is configured', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ providers: [] })
      });
    });
    await page.route('**/api/project-templates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ templates: [] })
      });
    });

    await page.goto('/');
    await page.locator('#welcomeNextBtn').click();

    await expect(page.locator('#onboardingApiKeySection')).toBeVisible();
    await expect(page.locator('#modelNextBtn')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Set Up Later' })).toBeVisible();
    await page.getByRole('button', { name: 'Continue without a model' }).click();
    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 3 of 3');
    await expect(page.locator('#onboardingPersonalAssistantHire')).toBeVisible();
    await expect(page.locator('#onboardingPersonalAssistantHire')).toContainText(
      'Hire your personal assistant'
    );
  });

  test('moves from model setup to the domain-neutral personal assistant hire', async ({ page }) => {
    await installBaseOnboardingRoutes(page);
    await page.route('**/api/providers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          providers: [{ name: 'ollama', display_name: 'Ollama', available: true }]
        })
      });
    });
    await page.route('**/api/settings/available-models?provider=ollama', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          available: true,
          model_options: [{ id: 'local-model', label: 'Local Model', recommended: true }]
        })
      });
    });
    await page.route('**/api/settings/system-model', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });
    await page.route('**/api/project-templates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          templates: [
            {
              id: 'research-project',
              name: 'Research Project',
              description: 'Sources and synthesis.',
              tags: ['research'],
              readiness: { state: 'ready' }
            },
            {
              id: 'plugin:audio:session',
              name: 'REAPER Song',
              description: 'A recording and production workspace.',
              tags: ['music', 'reaper'],
              readiness: { state: 'ready' }
            }
          ]
        })
      });
    });

    await page.goto('/');
    await page.locator('#welcomeNextBtn').click();
    await expect(page.locator('#modelNextBtn')).toBeEnabled();
    await page.locator('#modelNextBtn').click();

    await expect(page.locator('#onboardingStepLabel')).toHaveText('Step 3 of 3');
    const hireModal = page.locator('#onboardingPersonalAssistantHire');
    await expect(hireModal).toBeVisible();
    await expect(hireModal).toContainText('Hire your personal assistant');
    await expect(hireModal).not.toContainText(/REAPER|music|producer/i);
  });
});

test.describe('What Ori knows', () => {
  test('separates global, workspace, proposed, and reviewed knowledge', async ({ page }) => {
    let learningRequests = 0;
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      });
    });
    await page.route('**/api/user/profile', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          profile: {
            id: 'local',
            display_name: 'Jamie',
            timezone: 'UTC',
            preferences: { response_style: 'concise' }
          }
        })
      });
    });
    await page.route('**/api/onboarding/user-profile', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, profile: null })
      });
    });
    const assistantProgram = { link: { station_workspace_id: 'station-1' } };
    await page.route('**/api/workspaces', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          folders: [
            {
              id: 'ws-a',
              name: 'Alpha Project',
              folder_slug: 'alpha-project',
              assistant_program: assistantProgram
            },
            {
              id: 'ws-b',
              name: 'Beta Project',
              folder_slug: 'beta-project',
              assistant_program: assistantProgram
            }
          ]
        })
      });
    });
    await page.route('**/api/workspaces/ws-a/memory', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            {
              index: 0,
              type: 'feedback',
              text: '<img src=x onerror="window.knowledgeXSS=true"> Keep changes reversible.'
            }
          ],
          managed_learnings: [],
          unstructured: []
        })
      });
    });
    await page.route('**/api/workspaces/ws-b/memory', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ entries: [], managed_learnings: [], unstructured: [] })
      });
    });
    await page.route('**/api/workspaces/*/assistant-program/learnings', async route => {
      learningRequests += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          candidates: [
            {
              id: 'candidate-1',
              type: 'preference',
              text: 'Review a short checklist before project changes.',
              confidence: 'medium',
              evidence: [{}, {}, {}]
            }
          ],
          learnings: [
            {
              id: 'learning-1',
              revisions: [
                {
                  id: 'revision-1',
                  type: 'preference',
                  text: 'Prefer reviewable and reversible changes.',
                  confidence: 'high',
                  evidence: [{}, {}, {}]
                }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/profile');
    await expect(page.locator('#userKnowledgeOverview')).toHaveAttribute('aria-busy', 'false');
    await expect(page.getByRole('heading', { name: 'What Ori knows about you' })).toBeVisible();
    await expect(page.locator('#userKnowledgeWorkspaceList')).toContainText(
      'Keep changes reversible.'
    );
    await expect(page.locator('#userKnowledgeLearningList')).toContainText('Needs review');
    await expect(page.locator('#userKnowledgeLearningList')).toContainText('Reviewed learning');
    await expect(page.locator('#userKnowledgeLearningList')).toContainText(
      'Prefer reviewable and reversible changes.'
    );
    await expect(page.locator('#userKnowledgeSummary')).toContainText('Workspace memories');
    await expect(page.locator('#userKnowledgeWorkspaceList img')).toHaveCount(0);
    expect(
      await page.evaluate(() => (window as unknown as { knowledgeXSS?: boolean }).knowledgeXSS)
    ).toBeUndefined();
    expect(learningRequests).toBe(1);
  });
});

test.describe('Home First Run', () => {
  test('makes workspace creation the primary next step when no workspaces exist', async ({
    page,
    request
  }) => {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      })
    );
    await installActivePersonalAssistant(page);
    const response = await request.get('/api/workspaces');
    const data = await response.json();
    test.skip((data.workspaces || []).length !== 0, 'requires an empty workspace store');

    await page.goto('/');
    await expect(page.locator('body.home-command-page')).toBeVisible();
    await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('#homeCockpit')).toBeVisible();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'empty-map');
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.getByText('No workspaces yet.', { exact: true })).toHaveCount(0);
    const emptyActions = page.locator('.cockpit-empty-map-actions');
    await expect(emptyActions.getByRole('button', { name: 'New Workspace' })).toBeVisible();
    await expect(emptyActions.getByRole('button', { name: 'Import Folder' })).toBeVisible();
    await expect(page.locator('#sidebar')).toHaveCount(1);
    await expect(page.locator('#sidebarToggle')).toBeVisible();

    await page.goto('/workspaces');
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-state', 'empty-map');
    await expect(page.locator('.cockpit-empty-map-actions')).toBeVisible();
  });

  test('keeps the Map-first launcher interaction contract on Home', async ({ page }) => {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      })
    );
    await installActivePersonalAssistant(page);
    await page.goto('/');

    await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('#homeAssistantCard')).toBeHidden();
    await expect(page.locator('#homeCockpit')).toBeVisible();
    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantTodayPanel')).toBeVisible();
  });

  test('preserves an Ask draft across the on-demand drawer lifecycle', async ({ page }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await installActivePersonalAssistant(page);

    await page.goto('/');
    await openPersonalAssistantAsk(page);
    const input = page.locator('#personalAssistantInput');
    await input.fill('Summarize current operations');
    await page.keyboard.press('Escape');
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('#personalAssistantLauncher')).toBeFocused();

    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantTodayPanel')).toBeVisible();
    await page.locator('#personalAssistantAskTab').click();
    await expect(input).toHaveValue('Summarize current operations');
  });

  test('renders the bridge without browser console errors', async ({ page }) => {
    const errors: string[] = [];
    const failedResources: string[] = [];
    page.on('console', message => {
      if (message.type() === 'error') errors.push(message.text());
    });
    page.on('response', response => {
      if (response.status() === 404) failedResources.push(new URL(response.url()).pathname);
    });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    // The system assistant is created on demand, so Home's "does it exist yet"
    // probe 404s on a fresh install and the browser logs it. Stubbed so this
    // test can assert on real console errors.
    // Matches the new name and the legacy one: the cozy-character-experience
    // feature renamed the agent to Workspace Manager, freeing "Ori" for the
    // navigation guide.
    await page.route(
      /\/api\/agents\?name=(Ask(%20|\+| )Ori|Ori|Workspace(%20|\+| )Manager)$/,
      async route => {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ allow_web_search: false })
        });
      }
    );

    await page.goto('/');
    await expect(page.locator('.home-command-bridge')).toBeVisible();
    expect(errors, `404 responses: ${failedResources.join(', ')}`).toEqual([]);
  });

  test('surfaces Mission 01 in progression before the HQ tier unlocks', async ({ page }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 6,
          total_count: 3,
          completed_count: 0,
          resolved_count: 0,
          dismissed: false,
          all_complete: false,
          next_quest: {
            id: 't1-first-message',
            title: 'Say hello to Ori',
            why: 'Send Ori a message on the home page.',
            status: 'available'
          },
          tiers: [
            {
              tier: 1,
              name: 'First Contact',
              complete: false,
              quests: [
                { id: 't1-first-message', title: 'Say hello to Ori', status: 'available' },
                { id: 't1-personalize', title: 'Personalize Ori', status: 'available' }
              ]
            },
            {
              tier: 2,
              name: 'Establish a Base',
              complete: false,
              quests: [
                {
                  id: 't2-build-hq',
                  title: 'Build My HQ',
                  why: 'Give Ori a home base for your daily brief and follow-ups.',
                  status: 'locked-tier',
                  action_url: '/workspaces?view=map&focus=personal-hq',
                  action_label: 'Build My HQ',
                  optional: true
                }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/');

    // Issue #334: Progression is compact-only until the Quests trigger is
    // activated — Mission 01 lives inside that flyout now, not an
    // auto-opened rail.
    const questsTrigger = page.locator('#cockpitQuestsToggle');
    await expect(questsTrigger).toBeVisible();
    await expect(questsTrigger).toHaveAttribute('aria-expanded', 'false');
    await questsTrigger.click();

    const mission = page.locator('[data-role="first-mission"]');
    await expect(mission).toBeVisible();
    await expect(mission).toContainText('Mission 01');
    await expect(mission).toContainText('Build My HQ');
    await expect(mission.locator('[data-role="first-mission-status"]')).toHaveText('Ready');
    await expect(mission.locator('[data-role="first-mission-action"]')).toHaveAttribute(
      'href',
      '/workspaces?view=map&focus=personal-hq'
    );

    await page.setViewportSize({ width: 720, height: 800 });
    await expect(mission).toBeVisible();
    const width = await page.evaluate(() => ({
      page: document.documentElement.scrollWidth,
      viewport: window.innerWidth
    }));
    expect(width.page).toBeLessThanOrEqual(width.viewport + 1);
  });

  test('keeps maximum bridge readouts inside a desktop viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });

    const workspaces = Array.from({ length: 6 }, (_, index) => ({
      id: `bridge-${index + 1}`,
      name: `Bridge workspace ${index + 1}`,
      updated_at: `2026-07-${String(11 - index).padStart(2, '0')}T12:00:00Z`,
      agent_count: index + 1,
      open_task_count: index === 1 ? 2 : 0,
      needs_attention_count: index === 0 ? 1 : 0
    }));
    await page.route('**/api/workspaces', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces })
      });
    });
    await page.route('**/api/workspaces?tree=true', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces, folders: workspaces })
      });
    });
    await page.route('**/api/orchestration/scheduled-tasks/upcoming**', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          upcoming: Array.from({ length: 5 }, (_, index) => ({
            task_name: `Scheduled operation ${index + 1}`,
            workspace_id: `bridge-${index + 1}`,
            workspace_name: `Bridge workspace ${index + 1}`,
            agent_name: `Agent ${index + 1}`,
            next_run: `2026-07-12T${String(13 + index).padStart(2, '0')}:00:00Z`
          }))
        })
      });
    });
    await page.route('**/api/activity/recent**', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          events: Array.from({ length: 5 }, (_, index) => ({
            kind: index === 0 ? 'task_completed' : 'note_edited',
            description: `Operation event ${index + 1}`,
            workspace_id: `bridge-${index + 1}`,
            workspace_name: `Bridge workspace ${index + 1}`,
            timestamp: `2026-07-12T${String(11 + index).padStart(2, '0')}:00:00Z`
          }))
        })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 3,
          total_count: 3,
          completed_count: 1,
          dismissed: false,
          all_complete: false,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [
                { id: 'bridge-q1', title: 'Establish a workspace', status: 'completed' },
                {
                  id: 'bridge-q2',
                  title: 'Plan an operation',
                  status: 'pending',
                  action_url: '/workspaces'
                },
                { id: 'bridge-q3', title: 'Run a review', status: 'pending', action_url: '/review' }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/');
    // The Operations Board's workspace cards are retired; the Map is Home's
    // single workspace overview now (PRD FR22).
    await expect(page.locator('#cockpitMap')).toBeVisible();
    await expect(page.locator('[data-role="workspace-card"]')).toHaveCount(0);
    // Issue #334: open Quests to reach the real quest content.
    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#questLog')).toBeVisible();

    for (const [width, height] of [
      [1280, 800],
      [1512, 805]
    ]) {
      await page.setViewportSize({ width, height });
      const dimensions = await page.evaluate(() => ({
        viewport: window.innerHeight,
        documentHeight: document.documentElement.scrollHeight,
        bodyHeight: document.body.scrollHeight
      }));
      expect(dimensions.documentHeight).toBeLessThanOrEqual(dimensions.viewport + 1);
      expect(dimensions.bodyHeight).toBeLessThanOrEqual(dimensions.viewport + 1);
    }
  });

  test('keeps Daily Brief in Personal Assistant Today while Updates retains its own sections', async ({
    page
  }) => {
    await page.setViewportSize({ width: 1512, height: 805 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ current_tier: 1, total_tiers: 1, tiers: [] })
      });
    });

    await page.goto('/');
    await expect(page.locator('#homeHQResume')).toHaveCount(0);
    const ownership = await page.evaluate(() => {
      const brief = document.getElementById('homeDailyBrief');
      const updates = document.getElementById('cockpitUpdatesFlyoutBody');
      return {
        briefCount: document.querySelectorAll('#homeDailyBrief').length,
        inToday: Boolean(brief?.closest('#personalAssistantTodayPanel')),
        inUpdates: Boolean(brief?.closest('#cockpitUpdatesFlyoutBody')),
        updatesOwnSections: [
          'cockpitTodayAttention',
          'cockpitTodayScheduled',
          'homePluginUpdates',
          'homeCalendarOpsPortal',
          'homeRecentActivity'
        ].every(id => updates?.contains(document.getElementById(id)))
      };
    });

    expect(ownership.briefCount).toBe(1);
    expect(ownership.inToday).toBe(true);
    expect(ownership.inUpdates).toBe(false);
    expect(ownership.updatesOwnSections).toBe(true);
  });

  test('surfaces cached plugin updates in Updates without applying them', async ({ page }) => {
    await page.setViewportSize({ width: 1512, height: 805 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route(/\/api\/workspaces\?tree=true$/, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [], folders: [] })
      });
    });
    await page.route('**/api/plugins/updates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          checking: false,
          last_successful_check_at: '2026-08-31T12:00:00Z',
          updates: [
            {
              name: 'smoke-demo',
              installed_version: '1.0.0',
              available_version: '2.0.0',
              components_changed: false,
              available: true
            }
          ]
        })
      });
    });

    const updateMutations: string[] = [];
    page.on('request', request => {
      if (/\/api\/plugins\/[^/]+\/update$/.test(new URL(request.url()).pathname)) {
        updateMutations.push(request.method());
      }
    });

    await page.goto('/');
    const trigger = page.locator('#cockpitRailToggle');
    await expect(trigger.locator('[data-cockpit-rail-toggle-count]')).toHaveText('1');
    await trigger.click();

    const notice = page.locator('#homePluginUpdates');
    await expect(notice).toBeVisible();
    await expect(notice.locator('#homePluginUpdatesTitle')).toHaveText('1 plugin update');
    await expect(notice.locator('.home-plugin-update-name')).toHaveText('smoke-demo');
    await expect(notice.locator('.home-plugin-update-detail')).toHaveText(
      'Source version 2.0.0 is available.'
    );
    await expect(notice.getByRole('link', { name: 'Review plugins' })).toHaveAttribute(
      'href',
      '/plugins'
    );
    expect(updateMutations).toEqual([]);
    await captureImplementationScreenshot(page, 'home-plugin-updates-desktop.png');

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(notice).toBeVisible();
    const widths = await page.evaluate(() => ({
      page: document.documentElement.scrollWidth,
      viewport: window.innerWidth
    }));
    expect(widths.page).toBeLessThanOrEqual(widths.viewport + 1);
    await captureImplementationScreenshot(page, 'home-plugin-updates-narrow.png');
  });

  test('Quests starts compact and opens/collapses without persisting an open state (Issue #334)', async ({
    page
  }) => {
    const status = {
      current_tier: 1,
      total_tiers: 3,
      total_count: 2,
      completed_count: 1,
      all_complete: false,
      tiers: [
        {
          tier: 1,
          name: 'First contact',
          quests: [
            { id: 'collapse-q1', title: 'Say hello to Ori', status: 'completed' },
            {
              id: 'collapse-q2',
              title: 'Personalize Ori',
              status: 'pending',
              action_url: '/profile'
            }
          ]
        }
      ]
    };
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(status)
      });
    });

    await page.goto('/');

    // FR27/FR32: compact-only on a fresh load, with a real tier/count summary.
    const trigger = page.locator('#cockpitQuestsToggle');
    await expect(trigger).toBeVisible();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await expect(trigger).toContainText('Tier 1');
    await expect(trigger).toContainText('1/2');
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();

    await trigger.click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    await expect(page.locator('#questLog')).toBeVisible();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('1/2');

    // FR31: the quest log's own Collapse control closes the flyout and
    // returns focus to the compact trigger — it no longer persists a
    // dismissed preference server-side.
    const collapse = page.locator('[data-role="dismiss"]');
    await collapse.focus();
    await expect(collapse).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.locator('#cockpitQuestsFlyout')).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');

    // Reopening shows the exact same real content again — nothing was lost.
    await trigger.click();
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('1/2');
  });

  test('a prior server-side dismissed value never hides the compact Quests trigger (FR33)', async ({
    page
  }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 2,
          total_count: 1,
          dismissed: true,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [{ id: 'q1', title: 'Say hello', status: 'pending' }]
            }
          ]
        })
      });
    });

    await page.goto('/');

    const trigger = page.locator('#cockpitQuestsToggle');
    await expect(trigger).toBeVisible();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await trigger.click();
    await expect(page.locator('#questLog')).toBeVisible();
    await expect(page.locator('#questLogRestore')).toBeHidden();
  });

  test('Skip resolves an optional quest and offers Resume without dismissing the flyout', async ({
    page
  }) => {
    let skipped = false;
    const questBase = {
      id: 'skip-hq',
      title: 'Build My HQ',
      status: 'available',
      optional: true,
      action_url: '/workspaces?view=map&focus=personal-hq',
      action_label: 'Build My HQ'
    };
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression/skip', async route => {
      skipped = true;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 2,
          total_tiers: 2,
          total_count: 1,
          tiers: [
            { tier: 2, name: 'Establish a Base', quests: [{ ...questBase, status: 'skipped' }] }
          ]
        })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 2,
          total_tiers: 2,
          total_count: 1,
          tiers: [
            {
              tier: 2,
              name: 'Establish a Base',
              quests: [skipped ? { ...questBase, status: 'skipped' } : questBase]
            }
          ]
        })
      });
    });

    await page.goto('/');
    await page.locator('#cockpitQuestsToggle').click();
    const row = page.locator('.quest-item', { hasText: 'Build My HQ' });
    await row.getByRole('button', { name: /^Skip/ }).click();

    await expect(row).toContainText('Skipped');
    await expect(row.getByRole('link', { name: 'Build My HQ' })).toBeVisible();
    await expect(row.getByRole('button', { name: /^Skip/ })).toHaveCount(0);
    // A skip is a resolution, not a dismissal — the flyout stays open.
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
  });

  test('the all-complete state shows a compact congratulatory summary and expands to the real content', async ({
    page
  }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 6,
          total_tiers: 6,
          total_count: 9,
          resolved_count: 9,
          all_complete: true,
          tiers: []
        })
      });
    });

    await page.goto('/');

    const trigger = page.locator('#cockpitQuestsToggle');
    await expect(trigger).toContainText('All complete');
    await expect(trigger).not.toHaveText(/^\d+$/);

    await trigger.click();
    await expect(page.locator('[data-role="tier-name"]')).toHaveText('All quests complete');
    await expect(page.locator('[data-role="progress-count"]')).toHaveText('9/9');
  });

  test('a failed Progression response fails quietly and leaves Updates/Map fully usable (FR39)', async ({
    page
  }) => {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
    });

    await page.goto('/');

    await expect(page.locator('#cockpitQuestsToggle')).toBeHidden();
    // Nothing else on Home is blocked by the failure.
    await expect(page.locator('#cockpitMap')).toBeVisible();
    const updates = page.locator('#cockpitRailToggle');
    await expect(updates).toBeVisible();
    await updates.click();
    await expect(page.locator('#cockpitUpdatesFlyout')).toBeVisible();
  });

  test('Progression stays gated with the rest of workspace hydration until onboarding consent allows it (FR38)', async ({
    page
  }) => {
    let progressionRequests = 0;
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          current_step: 0,
          completed: false,
          skipped: false,
          steps_completed: [],
          user_name: '',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route('**/api/progression', async route => {
      progressionRequests += 1;
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    });

    await page.goto('/');

    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.locator('#cockpitQuestsToggle')).toBeHidden();
    expect(progressionRequests).toBe(0);
  });

  test('a poll refresh updates the compact summary and an open flyout in place, toasts once, and never duplicates the trigger', async ({
    page
  }) => {
    const tierOf = quests => ({
      current_tier: 1,
      total_tiers: 2,
      total_count: 2,
      all_complete: false,
      tiers: [{ tier: 1, name: 'First contact', quests }]
    });
    const pending = tierOf([
      { id: 'poll-q1', title: 'Say hello to Ori', status: 'completed' },
      { id: 'poll-q2', title: 'Create a workspace', status: 'pending' }
    ]);
    const bothDone = tierOf([
      { id: 'poll-q1', title: 'Say hello to Ori', status: 'completed' },
      { id: 'poll-q2', title: 'Create a workspace', status: 'completed' }
    ]);
    let calls = 0;
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      calls += 1;
      // The FIRST load already carries poll-q1 as completed — Issue #334/FR36:
      // a historical completion must never toast on initial load.
      const status = calls === 1 ? pending : bothDone;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(status)
      });
    });

    await page.goto('/');
    const trigger = page.locator('#cockpitQuestsToggle');
    await expect(trigger).toContainText('1/2');
    await expect(page.locator('#toastContainer .toast')).toHaveCount(0);

    await trigger.click();
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();

    // Force the next poll (20s is too slow for a test) via the same
    // visibilitychange hook the module already listens for.
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await expect(page.locator('#toastContainer .toast-title')).toHaveText('Quest complete');
    await expect(page.locator('#toastContainer .toast-message')).toHaveText('Create a workspace');
    // The refresh updated content in place — the flyout stayed open and the
    // summary now reflects both quests resolved.
    await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
    await expect(trigger).toContainText('2/2');

    // A cockpit-level refresh (e.g. after a workspace mutation) must not
    // duplicate the trigger/flyout or re-toast an already-known completion.
    await page.evaluate(() => window.dispatchEvent(new Event('ori:workspaces-changed')));
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await expect(page.locator('#toastContainer .toast')).toHaveCount(1);
    await expect(page.locator('#cockpitQuestsToggle')).toHaveCount(1);
    await expect(page.locator('#cockpitQuestsFlyout')).toHaveCount(1);
  });

  test('stacks bridge zones below desktop width without horizontal overflow', async ({ page }) => {
    await page.setViewportSize({ width: 960, height: 720 });
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
    await page.route('**/api/progression', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          current_tier: 1,
          total_tiers: 3,
          total_count: 2,
          completed_count: 0,
          dismissed: false,
          all_complete: false,
          tiers: [
            {
              tier: 1,
              name: 'First contact',
              quests: [
                { id: 'responsive-q1', title: 'Say hello to Ori', status: 'pending' },
                { id: 'responsive-q2', title: 'Create a workspace', status: 'pending' }
              ]
            }
          ]
        })
      });
    });

    await page.goto('/');
    await expect(page.locator('#homeCockpit')).toBeVisible();

    // Progression used to be a sibling stacked below the Operations Board,
    // then a Today section inside the cockpit's rail; it is now its own
    // Quests flyout, opened here explicitly rather than auto-opened
    // (Issue #334 — general workspace-area/context-rail stacking at narrow
    // widths is covered by home-workspace-cockpit.spec.ts). What still
    // matters below desktop width: the page never scrolls horizontally with
    // the flyout open, even with real quest content (PRD FR135).
    await page.locator('#cockpitQuestsToggle').click();
    await expect(page.locator('#questLog')).toBeVisible();
    const layout = await page.evaluate(() => ({
      pageWidth: document.documentElement.scrollWidth,
      viewportWidth: window.innerWidth
    }));
    expect(layout.pageWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
  });
});

test.describe('Agent Management', () => {
  test('can open create agent modal', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();

    // Look for create agent button (adjust selector based on your UI)
    const createBtn = page
      .locator('button:has-text("Create"), [data-bs-target="#addAgentModal"]')
      .first();

    if (await createBtn.isVisible()) {
      await createBtn.click();

      // Verify modal opens
      const modal = page.locator('#addAgentModal');
      await expect(modal).toBeVisible();

      // Verify form fields exist
      await expect(page.locator('#agentName')).toBeVisible();
      await expect(page.locator('#agentType')).toBeVisible();
    }
  });

  test('agent form validation works', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();

    // Open create modal
    const createBtn = page.locator('[data-bs-target="#addAgentModal"]').first();

    if (await createBtn.isVisible()) {
      await createBtn.click();
      await page.waitForSelector('#addAgentModal.show');

      // Try to submit without name
      const submitBtn = page.locator('#createAgentBtn');
      await submitBtn.click();

      // Check that form didn't submit (modal still visible) or validation message shown
      await expect(page.locator('#addAgentModal')).toBeVisible();
    }
  });
});

test.describe('Workspace Import Flow', () => {
  async function installEmptyHomeRoutes(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: [0, 1, 2],
          user_name: 'Smoke Tester',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route('**/api/workspaces?tree=true', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [], folders: [] })
      });
    });
  }

  test('import modal supports picker selection and duplicate override', async ({ page }) => {
    await installEmptyHomeRoutes(page);
    await page.route('**/api/folder-picker/select-path', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          selected: true,
          path: '/tmp/demo-project'
        })
      });
    });

    await page.route('**/api/workspaces/import/check*', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          duplicate: {
            found: true,
            workspace_id: 'existing-ws',
            workspace_name: 'Existing Workspace'
          }
        })
      });
    });

    await page.route('**/api/workspaces/import/duplicate-action', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });

    let importAttemptCount = 0;
    await page.route('**/api/workspaces/import', async route => {
      importAttemptCount += 1;
      const body = route.request().postDataJSON();

      if (!body.allow_duplicate) {
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            success: false,
            error: 'Folder is already imported in another workspace',
            duplicate: {
              found: true,
              workspace_id: 'existing-ws',
              workspace_name: 'Existing Workspace'
            }
          })
        });
        return;
      }

      await route.fulfill({
        status: 201,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          folder: {
            id: 'new-imported-ws',
            name: body.name || 'demo-project'
          },
          directory: {
            id: 'dir-ref-1',
            workspace_id: 'new-imported-ws',
            name: 'demo-project',
            path: '/tmp/demo-project'
          },
          duplicate: { found: false }
        })
      });
    });

    await page.goto('/');
    const importButton = page.locator('[data-workspace-entry-point="home_cockpit_import"]');
    await expect(importButton).toBeVisible();
    const modal = page.locator('#addFolderModal');
    await importButton.click();
    await expect(modal).toBeVisible();

    const importToggle = page.locator('#folderImportToggle');
    await expect(importToggle).toBeChecked();
    await expect(page.locator('#folderImportSection')).toBeVisible();

    await page.locator('#folderImportBrowseBtn').click();
    await expect(page.locator('#folderImportPathInput')).toHaveValue('/tmp/demo-project');
    await expect(page.locator('#folderImportDuplicateWarning')).toBeVisible();
    await page.locator('#folderDescriptionInput').fill('Imported demo project for smoke coverage.');

    await page.locator('#folderImportProceedDuplicateBtn').click();
    await page.locator('#createFolderBtn').click();

    await expect(page.locator('#addFolderModal.show')).toHaveCount(0);
    expect(importAttemptCount).toBeGreaterThan(0);
  });

  test('import controls are keyboard and mobile friendly', async ({ page }) => {
    await installEmptyHomeRoutes(page);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/');
    const importButton = page.locator('[data-workspace-entry-point="home_cockpit_import"]');
    await expect(importButton).toBeVisible();

    const modal = page.locator('#addFolderModal');
    await importButton.focus();
    await page.keyboard.press('Enter');
    await expect(modal).toBeVisible();

    const importToggle = page.locator('#folderImportToggle');
    await expect(importToggle).toBeChecked();
    await expect(page.locator('#folderImportSection')).toBeVisible();

    const pathBox = await page.locator('#folderImportPathInput').boundingBox();
    const browseBox = await page.locator('#folderImportBrowseBtn').boundingBox();
    expect(pathBox).not.toBeNull();
    expect(browseBox).not.toBeNull();
    if (pathBox && browseBox) {
      expect(browseBox.y).toBeGreaterThanOrEqual(pathBox.y);
    }
  });
});

test.describe('API Health', () => {
  test('health endpoint returns OK', async ({ request }) => {
    const response = await request.get('/health');
    expect(response.ok()).toBeTruthy();
  });

  test('agents API is accessible', async ({ request }) => {
    const response = await request.get('/api/agents');
    expect(response.ok()).toBeTruthy();

    const data = await response.json();
    expect(Array.isArray(data) || typeof data === 'object').toBeTruthy();
  });

  test('plugins APIs stay backward-compatible and update status is read-only', async ({
    request
  }) => {
    const beforeResponse = await request.get('/api/plugins');
    expect(beforeResponse.ok()).toBeTruthy();
    const before = await beforeResponse.json();
    expect(Array.isArray(before.plugins)).toBeTruthy();

    const updatesResponse = await request.get('/api/plugins/updates');
    expect(updatesResponse.ok()).toBeTruthy();
    const updates = await updatesResponse.json();
    expect(Array.isArray(updates.updates)).toBeTruthy();
    expect(typeof updates.checking).toBe('boolean');

    const writeAttempt = await request.post('/api/plugins/updates');
    expect(writeAttempt.ok()).toBeFalsy();
    const after = await (await request.get('/api/plugins')).json();
    expect(after.plugins).toEqual(before.plugins);
  });
});

test.describe('Plugin Update Notifications', () => {
  test('renders cached notices without automatically applying an update', async ({ page }) => {
    const updateRequests: Array<{ confirm?: boolean }> = [];
    await page.route('**/api/plugins', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          plugins: [
            {
              name: 'smoke-demo',
              version: '1.0.0',
              format: 'claude',
              install_dir: '/tmp/Ori Demo/<escaped>',
              enabled: false
            }
          ]
        })
      });
    });
    await page.route('**/api/plugins/updates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          checking: false,
          last_successful_check_at: '2026-08-31T12:00:00Z',
          updates: [
            {
              name: 'smoke-demo',
              installed_version: '1.0.0',
              available_version: '2.0.0',
              components_changed: false,
              available: true
            }
          ]
        })
      });
    });
    await page.route('**/api/plugins/marketplaces', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ marketplaces: [], official: {} })
      });
    });
    await page.route('**/api/plugins/smoke-demo/update', async route => {
      updateRequests.push(route.request().postDataJSON() || {});
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ updated: false, changed: false, trust: {} })
      });
    });

    await page.goto('/plugins');
    await expect(page.locator('#pluginUpdateNotice')).toHaveAttribute('role', 'status');
    await expect(page.locator('#pluginUpdateNoticeTitle')).toHaveText(
      '1 plugin update is available'
    );
    await expect(page.locator('#pluginList')).toContainText('Update available · 2.0.0');
    await expect(page.locator('.plugin-install-directory')).toHaveText('/tmp/Ori Demo/<escaped>');
    await expect(page.locator('#pluginList img, #pluginList script')).toHaveCount(0);
    await expect(
      page.locator('[data-plugin-action="update"][data-plugin-name="smoke-demo"]')
    ).toBeVisible();
    expect(updateRequests).toHaveLength(0);
  });
});

test.describe('Workspace Agent Character Roster', () => {
  async function installRosterRoutes(page, options = {}) {
    const createResponse = await page.request.post('/api/workspaces', {
      data: { name: `Roster Workspace ${Date.now()}`, blank: true }
    });
    expect(createResponse.ok()).toBeTruthy();
    const createdWorkspace = (await createResponse.json()).folder;
    const workspaceId = createdWorkspace.id;
    const workspaceSlug = createdWorkspace.folder_slug;
    const workspace = {
      id: workspaceId,
      folder_slug: workspaceSlug,
      name: 'Roster Workspace',
      description: 'Workspace for roster UI smoke coverage',
      entry_agent_name: 'Roster Manager',
      agents: ['Roster Manager', 'Research Analyst'],
      agent_instances: [
        {
          id: 'manager-1',
          name: 'Roster Manager',
          instance_number: 1,
          node_id: 'Roster Manager-node-1',
          role: 'Coordinator',
          entry_point: true
        },
        {
          id: 'analyst-1',
          name: 'Research Analyst',
          instance_number: 1,
          node_id: 'Research Analyst-node-1',
          role: 'Research'
        },
        {
          id: 'analyst-2',
          name: 'Research Analyst',
          instance_number: 2,
          node_id: 'Research Analyst-node-2',
          role: 'Synthesis'
        }
      ],
      shared_data: {},
      skill_bindings: [
        { id: 'skill-planning', skill_name: 'workspace-planning', enabled: true, trusted: true },
        {
          id: 'skill-research',
          skill_name: 'browser:control-in-app-browser',
          enabled: true,
          trusted: true
        }
      ],
      agent_skill_access: [
        { agent_instance_id: 'manager-1', enabled_binding_ids: ['skill-planning'] },
        {
          agent_instance_id: 'analyst-1',
          enabled_binding_ids: ['skill-research', 'skill-planning']
        },
        {
          agent_instance_id: 'analyst-2',
          enabled_binding_ids: ['skill-research', 'skill-planning']
        }
      ],
      mcp_bindings: [
        {
          id: 'mcp-notes',
          server_name: 'notes-server',
          alias: 'team_notes',
          enabled: true,
          scope: { notebook: 'workspace' }
        }
      ],
      agent_mcp_access: [],
      directory_references: [{ path: '/tmp/roster-workspace' }],
      attachments: [],
      tasks: [],
      status: 'active'
    };
    const tasks = [
      {
        id: 'task-1',
        workspace_id: workspaceId,
        to: 'Roster Manager',
        status: 'pending',
        description: 'Plan the work'
      },
      {
        id: 'task-2',
        workspace_id: workspaceId,
        to: 'Research Analyst',
        status: 'waiting_for_choice',
        description: 'Choose source'
      },
      {
        id: 'task-3',
        workspace_id: workspaceId,
        to: 'Research Analyst',
        status: 'in_progress',
        description: 'Read source',
        parent_task_id: 'task-1'
      }
    ];
    const catalogAgents = [
      ...(options.omitRosterManagerFromCatalog
        ? []
        : [
            {
              name: 'Roster Manager',
              type: 'general',
              source: 'user',
              model: 'claude-opus-4',
              provider: 'anthropic',
              capabilities: ['files']
            }
          ]),
      {
        name: 'Research Analyst',
        type: 'research',
        source: 'user',
        model: 'claude-sonnet-4',
        provider: 'anthropic',
        allow_web_search: true
      }
    ];
    const snapshotAgents = Array.isArray(options.snapshotAgents) ? options.snapshotAgents : [];

    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: [0, 1, 2],
          user_name: 'Tester',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route(`**/api/orchestration/workspace?id=${workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(workspace)
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/agent-snapshots`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ agents: snapshotAgents })
      });
    });
    await page.route('**/api/agents/dashboard/list', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ agents: catalogAgents })
      });
    });
    await page.route('**/api/skills?agent=default', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          skills: [
            { name: 'workspace-planning', enabled: true },
            { name: 'browser:control-in-app-browser', enabled: true },
            { name: 'reviewer', enabled: true }
          ]
        })
      });
    });
    let workspacePlanningDetailAttempts = 0;
    await page.route('**/api/skills/*', async route => {
      const requestURL = new URL(route.request().url());
      const skillName = decodeURIComponent(requestURL.pathname.split('/').pop() || '');
      if (skillName === 'workspace-planning' && workspacePlanningDetailAttempts++ === 0) {
        await route.fulfill({
          status: 503,
          contentType: 'text/plain',
          body: 'Deterministic skill detail failure'
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          name: skillName,
          description:
            skillName === 'workspace-planning'
              ? 'Plans workspace delivery with deliberate checkpoints.'
              : skillName === 'reviewer'
                ? 'Reviews workspace changes before delivery.'
                : 'Controls the approved in-app browser for research.',
          prompt:
            'Inspect the request carefully.\n\nUse workspace evidence before making recommendations.\n'.repeat(
              12
            ),
          source: 'agent',
          path: `/tmp/skills/${skillName}`,
          allowed_tools: ['read_file', 'browser_search'],
          disallowed_tools: ['delete_workspace'],
          required_mcp_servers: ['notes-server'],
          model: 'claude-sonnet-4',
          enabled: true,
          trusted: true,
          has_scripts: false,
          validation_errors: []
        })
      });
    });
    await page.route('**/api/mcp/servers', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          servers: [
            { name: 'notes-server', enabled: true },
            { name: 'calendar-server', enabled: true }
          ]
        })
      });
    });
    await page.route('**/api/mcp/servers/*/details*', async route => {
      const requestURL = new URL(route.request().url());
      const pathSegments = requestURL.pathname.split('/').filter(Boolean);
      const serverName = decodeURIComponent(
        pathSegments[pathSegments.length - 2] || 'notes-server'
      );
      const serverTitle = serverName
        .split('-')
        .map(part => part.charAt(0).toUpperCase() + part.slice(1))
        .join(' ');
      const started = requestURL.searchParams.get('start') === 'true';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          server: serverName,
          status: started ? 'running' : 'stopped',
          command: 'npx',
          args: ['-y', '@ori/notes-server'],
          transport: 'stdio',
          enabled: true,
          env_keys: ['NOTES_TOKEN'],
          instructions: `Use ${serverName} only for approved workspace operations.`,
          server_info: started
            ? { name: 'notes-server', title: 'Ori Notes', version: '1.0.0' }
            : null,
          readme: {
            markdown: `# ${serverTitle}\n\nLong-form deterministic fixture documentation.`,
            source: 'github',
            source_url: 'https://example.com/notes-server'
          },
          tools: started
            ? [
                {
                  name: 'create_note',
                  description: 'Create an approved workspace note.',
                  inputSchema: {
                    properties: {
                      title: { type: 'string', description: 'Note title' },
                      body: { type: 'string', description: 'Note content' }
                    },
                    required: ['title', 'body']
                  }
                }
              ]
            : []
        })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/agent-skill-access/*`, async route => {
      const instanceId = decodeURIComponent(route.request().url().split('/').pop() || '');
      if (route.request().method() === 'DELETE') {
        workspace.agent_skill_access = workspace.agent_skill_access.filter(
          entry => entry.agent_instance_id !== instanceId
        );
      } else {
        const body = route.request().postDataJSON();
        const nextEntry = {
          agent_instance_id: instanceId,
          enabled_binding_ids: body.enabled_binding_ids || []
        };
        const index = workspace.agent_skill_access.findIndex(
          entry => entry.agent_instance_id === instanceId
        );
        if (index >= 0) workspace.agent_skill_access[index] = nextEntry;
        else workspace.agent_skill_access.push(nextEntry);
      }
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    });
    await page.route(`**/api/orchestration/tasks?workspace_id=${workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ tasks })
      });
    });
    await page.route(`**/api/sessions?folder_id=${workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ sessions: [] })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/notes`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ notes: [] })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/mission`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          mission: '',
          mission_enabled: false,
          cadence: null,
          mission_execution_count: 0,
          mission_failure_count: 0,
          open_findings_count: 0
        })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}/agents/*/effective-prompt`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          base_system_prompt: 'You are a focused workspace agent.',
          effective_prompt: 'You are a focused workspace agent.'
        })
      });
    });
    await page.route(`**/api/workspaces/${workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(workspace)
      });
    });
    await page.route('**/api/workspaces?tree=true', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ workspaces: [workspace], folders: [workspace] })
      });
    });
    await page.route(`**/api/orchestration/workspace/activate?id=${workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true })
      });
    });
    await page.route('**/api/project-templates', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ templates: [] })
      });
    });
    return workspaceSlug;
  }

  test('command deck selects roster characters and updates the shared agent overview', async ({
    page
  }) => {
    const workspaceSlug = await installRosterRoutes(page);
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/workspaces/${workspaceSlug}`);

    const roster = page.locator('#workspaceCommandView .ws-cmd-roster-item');
    await expect(roster).toHaveCount(2);
    await expect(roster.first()).toHaveAttribute('aria-pressed', 'true');
    await expect(roster.first()).toContainText('Roster Manager');
    await expect(roster.first().locator('.ws-cmd-roster-entry')).toHaveText('CMD');
    const commanderPortrait = roster.first().locator('.ws-cmd-character.ws-map-av');
    await expect(commanderPortrait).toBeVisible();
    await expect(commanderPortrait).toHaveClass(/is-keeper/);
    await expect(commanderPortrait.locator('.ws-map-av-figure')).toHaveAttribute(
      'aria-hidden',
      'true'
    );
    await expect(commanderPortrait.locator('.ws-map-av-label')).toHaveText('Roster Manager');
    await expect(roster.nth(1).locator('.ws-cmd-character.ws-map-av')).toBeVisible();
    await expect(roster.nth(1)).toContainText('Working');
    await expect(roster.nth(1)).toContainText('2×');

    const stage = page.locator('#workspaceCommandView .ws-cmd-agent-stage');
    await expect(stage.locator('h3')).toHaveText('Roster Manager');
    await expect(stage.locator('.ws-cmd-character.is-stage.ws-map-av.is-keeper')).toBeVisible();
    await expect(stage.locator('.ws-map-av-label')).toHaveText('Roster Manager');
    await expect(stage).toContainText('Commander');
    await expect(stage).toContainText('Idle');

    await roster.nth(1).click();
    await expect(stage.locator('h3')).toHaveText('Research Analyst');
    await expect(stage.locator('.ws-cmd-character.is-stage.ws-map-av')).not.toHaveClass(
      /is-keeper/
    );
    await expect(stage.locator('.ws-map-av-label')).toHaveText('Research Analyst');
    await expect(stage).toContainText('Working');

    await page.getByRole('tab', { name: 'Tasks' }).click();
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Choose source');

    await page.getByRole('tab', { name: 'Toolbox' }).click();
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Model');
    await expect(page.locator('.ws-cmd-agent-tabpanel.is-active')).toContainText('Skills');
    await expect(page.locator('.ws-cmd-loadout-prompt')).toContainText(
      'You are a focused workspace agent.'
    );
  });

  test('command deck uses the required desktop, tablet, and mobile layouts without page overflow', async ({
    page
  }) => {
    const workspaceSlug = await installRosterRoutes(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`/workspaces/${workspaceSlug}`);
    await expect(page.locator('.ws-cmd-roster-item')).toHaveCount(2);
    await expect(page.locator('.ws-cmd-deck')).toBeVisible();

    const mission = page.locator('#workspace-command-mission-card');
    await expect(mission).toBeVisible();
    // Chromium can report the exact 160px CSS cap a few ten-thousandths over
    // after device-pixel conversion; keep the assertion at the same visual bound.
    expect((await mission.boundingBox())?.height || 0).toBeLessThanOrEqual(160.1);

    const desktopGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const roster = element.querySelector('.ws-cmd-roster')?.getBoundingClientRect();
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return {
        rosterTop: roster?.top,
        stageTop: stage?.top,
        overviewTop: overview?.top,
        rosterRight: roster?.right,
        stageLeft: stage?.left,
        stageRight: stage?.right,
        overviewLeft: overview?.left
      };
    });
    expect(
      Math.abs((desktopGeometry.rosterTop || 0) - (desktopGeometry.stageTop || 0))
    ).toBeLessThan(2);
    expect(
      Math.abs((desktopGeometry.stageTop || 0) - (desktopGeometry.overviewTop || 0))
    ).toBeLessThan(2);
    expect(desktopGeometry.rosterRight).toBeLessThanOrEqual(desktopGeometry.stageLeft || 0);
    expect(desktopGeometry.stageRight).toBeLessThanOrEqual(desktopGeometry.overviewLeft || 0);

    await page.setViewportSize({ width: 1024, height: 768 });
    const tabletGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const roster = element.querySelector('.ws-cmd-roster')?.getBoundingClientRect();
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return { rosterBottom: roster?.bottom, stageTop: stage?.top, overviewTop: overview?.top };
    });
    expect(tabletGeometry.rosterBottom).toBeLessThanOrEqual(tabletGeometry.stageTop || 0);
    expect(
      Math.abs((tabletGeometry.stageTop || 0) - (tabletGeometry.overviewTop || 0))
    ).toBeLessThan(2);

    await page.setViewportSize({ width: 390, height: 844 });
    const mobileGeometry = await page.locator('.ws-cmd-deck').evaluate(element => {
      const stage = element.querySelector('.ws-cmd-agent-stage')?.getBoundingClientRect();
      const overview = element.querySelector('.ws-cmd-agent-overview')?.getBoundingClientRect();
      return {
        stageBottom: stage?.bottom,
        overviewTop: overview?.top,
        pageWidth: document.documentElement.scrollWidth,
        viewportWidth: window.innerWidth
      };
    });
    expect(mobileGeometry.stageBottom).toBeLessThanOrEqual(mobileGeometry.overviewTop || 0);
    expect(mobileGeometry.pageWidth).toBeLessThanOrEqual(mobileGeometry.viewportWidth);
  });

  test('operations map switches from details, selects agents, and opens inventory', async ({
    page
  }) => {
    const workspaceSlug = await installRosterRoutes(page);
    await page.addInitScript(() => {
      window.localStorage.removeItem('oriWorkspaceCommandViewMode');
    });
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/workspaces/${workspaceSlug}`);

    await page.locator('#workspaceCommandView [data-cmd-view-mode="map"]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-opmap')).toBeVisible();
    await expect(page.locator('#workspaceCommandView [data-map-zone="mission"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="tasks"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="tools"]')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView [data-map-zone="agents"]')).toContainText(
      'Research Analyst'
    );
    const beltGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-map-belt')
      .evaluate(node => {
        const style = window.getComputedStyle(node);
        const rect = node.getBoundingClientRect();
        const mapRect = node.closest('.ws-cmd-opmap').getBoundingClientRect();
        return {
          flexDirection: style.flexDirection,
          rightGap: Math.round(mapRect.right - rect.right),
          topGap: Math.round(rect.top - mapRect.top)
        };
      });
    expect(beltGeometry.flexDirection).toBe('row');
    expect(beltGeometry.rightGap).toBeLessThanOrEqual(24);
    expect(beltGeometry.topGap).toBeLessThanOrEqual(24);
    const beltLabelGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn')
      .evaluateAll(buttons =>
        buttons.map(button => {
          const label = button.querySelector('.sr-only');
          const labelStyle = label ? window.getComputedStyle(label) : null;
          const labelRect = label ? label.getBoundingClientRect() : null;
          const buttonRect = button.getBoundingClientRect();
          return {
            ariaLabel: button.getAttribute('aria-label'),
            buttonHeight: Math.round(buttonRect.height),
            buttonWidth: Math.round(buttonRect.width),
            labelHeight: labelRect ? Math.round(labelRect.height) : 0,
            labelOverflow: labelStyle?.overflow || '',
            labelText: label?.textContent?.trim() || '',
            labelWidth: labelRect ? Math.round(labelRect.width) : 0
          };
        })
      );
    expect(beltLabelGeometry).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          ariaLabel: 'Workspace Mission',
          labelText: 'Workspace Mission'
        })
      ])
    );
    expect(
      beltLabelGeometry.every(
        item =>
          item.buttonHeight >= 44 &&
          item.buttonWidth >= 44 &&
          item.labelHeight <= 1 &&
          item.labelWidth <= 1 &&
          item.labelOverflow === 'hidden'
      )
    ).toBeTruthy();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-station-node')).toHaveCount(0);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-station-route')).toHaveCount(0);
    const entryUnit = page
      .locator('#workspaceCommandView .ws-cmd-map-agent')
      .filter({ hasText: 'Roster Manager' });
    await expect(entryUnit).toHaveClass(/is-command-node/);
    await expect(entryUnit.locator('.ws-cmd-map-command-role')).toContainText('Commander');
    await expect(entryUnit.locator('.ws-cmd-map-agent-status')).toBeVisible();
    await expect(entryUnit).toHaveClass(/waiting/);
    await expect(entryUnit).toHaveAttribute('aria-label', /Commander/);

    await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn[data-cmd-map-window="inventory"]')
      .click();
    const inventoryWindow = page.locator('#workspaceCommandView .ws-cmd-map-window');
    const activeInventoryGroup = page.locator(
      '#workspaceCommandView .ws-cmd-map-inventory-group.is-active'
    );
    await expect(inventoryWindow).toBeVisible();
    await expect(inventoryWindow).toContainText('Inventory');
    await expect(activeInventoryGroup).toContainText('Notes');
    await expect(activeInventoryGroup.locator('.ws-cmd-map-inventory-grid')).toBeVisible();
    await expect(activeInventoryGroup.locator('.ws-cmd-map-inventory-slot').first()).toBeVisible();
    await expect(activeInventoryGroup.locator('.ws-cmd-map-slot-type').first()).toContainText(
      'Note'
    );
    const inventoryGeometry = await inventoryWindow.evaluate(node => {
      const rect = node.getBoundingClientRect();
      return {
        top: rect.top,
        bottom: rect.bottom,
        viewportHeight: window.innerHeight
      };
    });
    expect(inventoryGeometry.top).toBeGreaterThanOrEqual(0);
    expect(inventoryGeometry.bottom).toBeLessThanOrEqual(inventoryGeometry.viewportHeight);
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-inventory-badge')).toContainText([
      'Notes',
      'Schedules',
      'Sessions',
      'Linked Folders',
      'Files',
      'Systems'
    ]);
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);

    await page
      .locator('#workspaceCommandView .ws-cmd-map-belt-btn[data-cmd-map-window="objectives"]')
      .click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toContainText(
      'Plan the work'
    );
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();

    await page
      .locator('#workspaceCommandView .ws-cmd-map-agent')
      .filter({ hasText: 'Research Analyst' })
      .click();
    const inspector = page.locator('#workspaceCommandView .ws-cmd-map-window');
    await expect(inspector).toContainText('Research Analyst');
    await expect(inspector).toContainText('Needs input');
    await expect(inspector).toContainText('Role');
    await expect(inspector).toContainText('Toolbox');
    await expect(inspector).toContainText('Current Quest');
    await expect(inspector).toContainText('Command Menu');
    await expect(inspector).toContainText('Resolve Quest');
    await expect(inspector).toContainText('Start Session');
    await expect(inspector).toContainText('Open the Workshop');
    await expect(inspector).toContainText('Quests');
    await expect(inspector).toContainText('Skills');
    const inspectorGeometry = await inspector.evaluate(node => {
      const rect = node.getBoundingClientRect();
      const body = node.querySelector('.ws-cmd-map-window-body')?.getBoundingClientRect();
      const menu = node.querySelector('.ws-cmd-rpg-command-panel')?.getBoundingClientRect();
      return {
        bottom: Math.round(rect.bottom),
        bodyBottom: body ? Math.round(body.bottom) : 0,
        bodyTop: body ? Math.round(body.top) : 0,
        menuBottom: menu ? Math.round(menu.bottom) : 0,
        menuTop: menu ? Math.round(menu.top) : 0,
        top: Math.round(rect.top),
        viewportHeight: window.innerHeight
      };
    });
    expect(inspectorGeometry.top).toBeGreaterThanOrEqual(0);
    expect(inspectorGeometry.bottom).toBeLessThanOrEqual(inspectorGeometry.viewportHeight);
    expect(inspectorGeometry.menuTop).toBeGreaterThanOrEqual(inspectorGeometry.bodyTop);
    expect(inspectorGeometry.menuBottom).toBeLessThanOrEqual(inspectorGeometry.bodyBottom);

    const addSkillButton = inspector.getByRole('button', { name: 'Add Skill' });
    await addSkillButton.click();
    const addSkillDialog = page.getByRole('dialog', { name: 'Add Skill' });
    await expect(addSkillDialog).toBeVisible();
    await expect(addSkillDialog).toContainText('reviewer');
    await expect(addSkillDialog).toContainText('Research Analyst');
    const closeAddSkillButton = addSkillDialog.getByRole('button', { name: 'Close Add Skill' });
    const reviewerOption = addSkillDialog.getByRole('button', {
      name: 'View details for skill reviewer'
    });
    await expect(closeAddSkillButton).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(reviewerOption).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(closeAddSkillButton).toBeFocused();
    await reviewerOption.click();
    await expect(addSkillDialog).toContainText('Reviews workspace changes before delivery');
    await expect(
      addSkillDialog.getByRole('button', { name: 'Add Skill', exact: true })
    ).toBeVisible();
    await addSkillDialog.getByRole('tab', { name: 'Instructions' }).click();
    await expect(addSkillDialog).toContainText('Inspect the request carefully');
    await captureImplementationScreenshot(page, 'unit-sheet-add-skill-modal.png');
    await addSkillDialog.getByRole('button', { name: /Back to available skills/ }).click();
    await expect(reviewerOption).toBeFocused();
    await closeAddSkillButton.click();
    await expect(addSkillDialog).toHaveCount(0);
    await expect(addSkillButton).toBeFocused();

    const addToolButton = inspector.getByRole('button', { name: 'Add Tool' });
    await addToolButton.click();
    const addToolDialog = page.getByRole('dialog', { name: 'Add Tool' });
    const calendarOption = addToolDialog.getByRole('button', {
      name: 'View details for tool calendar-server'
    });
    await calendarOption.click();
    await expect(addToolDialog).toContainText('calendar-server');
    await expect(addToolDialog).toContainText('stopped');
    await expect(
      addToolDialog.getByRole('button', { name: 'Add Tool', exact: true })
    ).toBeVisible();
    await expect(addToolDialog.locator('[data-cmd-capability-start]')).toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(addToolDialog).toBeVisible();
    await expect(calendarOption).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(addToolDialog).toHaveCount(0);
    await expect(addToolButton).toBeFocused();
    await expect(inspector).toBeVisible();

    const skillRow = inspector
      .locator('.ws-cmd-loadout-row')
      .filter({ hasText: 'browser:control-in-app-browser' });
    const skillInspect = skillRow.getByRole('button', {
      name: /Inspect skill browser:control-in-app-browser/
    });
    const skillRemove = skillRow.getByRole('button', {
      name: /Remove skill browser:control-in-app-browser from Research Analyst/
    });
    await expect(skillRemove).toBeVisible();
    await skillInspect.click();
    const capabilityInspector = inspector.locator('[data-cmd-capability-inspector]');
    await expect(capabilityInspector).toContainText('Controls the approved in-app browser');
    await expect(skillRemove).toBeVisible();
    await expect(inspector.locator('.ws-cmd-rpg-command-panel')).toHaveCount(0);
    await expect(capabilityInspector.locator('[role="dialog"]')).toHaveCount(0);
    await expect(inspector.locator('[role="dialog"]')).toHaveCount(0);
    await expect(page.locator('[role="dialog"][aria-label="Unit Sheet"]')).toHaveCount(1);

    await skillRemove.click();
    await expect(skillRow).toHaveCount(0);
    await expect(capabilityInspector).toContainText('Not assigned to Research Analyst');
    await capabilityInspector.getByRole('tab', { name: 'Instructions' }).click();
    await expect(capabilityInspector.locator('.ws-cmd-capability-prompt')).toContainText(
      'Use workspace evidence'
    );
    await captureImplementationScreenshot(page, 'unit-sheet-skill-inspector-desktop.png');
    await capabilityInspector.getByRole('button', { name: /Back to Command Menu/ }).click();
    await expect(inspector.locator('.ws-cmd-rpg-command-panel')).toContainText('Command Menu');
    await expect(addSkillButton).toBeFocused();
    if (process.env.ORI_CAPTURE_SCREENSHOTS) await page.waitForTimeout(450);
    await captureImplementationScreenshot(page, 'unit-sheet-capability-removed.png');

    await addSkillButton.click();
    const readdSkillDialog = page.getByRole('dialog', { name: 'Add Skill' });
    const removedSkillOption = readdSkillDialog.getByRole('button', {
      name: 'View details for skill browser:control-in-app-browser'
    });
    await expect(removedSkillOption).toBeVisible();
    await removedSkillOption.click();
    await expect(readdSkillDialog).toContainText('Controls the approved in-app browser');
    await readdSkillDialog.getByRole('button', { name: 'Add Skill', exact: true }).click();
    await expect(readdSkillDialog).toHaveCount(0);
    await expect(skillRow).toBeVisible();

    const mcpRow = inspector.locator('.ws-cmd-loadout-row').filter({ hasText: 'notes-server' });
    await mcpRow.getByRole('button', { name: /Inspect MCP server notes-server/ }).click();
    await expect(capabilityInspector).toContainText('stopped');
    await expect(capabilityInspector).toContainText('NOTES_TOKEN');
    await expect(capabilityInspector).not.toContainText('secret');
    await captureImplementationScreenshot(page, 'unit-sheet-mcp-inspector-passive.png');
    await capabilityInspector.getByRole('tab', { name: 'Tools' }).click();
    await capabilityInspector
      .getByRole('button', { name: /Start MCP server notes-server and load tools/ })
      .click();
    await expect(capabilityInspector).toContainText('create_note');
    await expect(capabilityInspector).toContainText('Required');
    await capabilityInspector.getByRole('tab', { name: 'Docs' }).click();
    await expect(capabilityInspector).toContainText('Notes Server');
    await captureImplementationScreenshot(page, 'unit-sheet-mcp-inspector-desktop.png');
    await capabilityInspector.getByRole('button', { name: /Back to Command Menu/ }).click();

    const filesystemRow = inspector
      .locator('.ws-cmd-loadout-row')
      .filter({ hasText: 'filesystem' });
    await filesystemRow.getByRole('button', { name: /Inspect MCP server filesystem/ }).click();
    await expect(capabilityInspector).toContainText('Workspace-native capability');
    await expect(capabilityInspector).toContainText('/tmp/roster-workspace');
    await expect(capabilityInspector.locator('[data-cmd-capability-start]')).toHaveCount(0);
    await capabilityInspector.getByRole('button', { name: /Back to Command Menu/ }).click();

    const planningRow = inspector
      .locator('.ws-cmd-loadout-row')
      .filter({ hasText: 'workspace-planning' });
    await planningRow.getByRole('button', { name: /Inspect skill workspace-planning/ }).click();
    await expect(capabilityInspector).toContainText('Deterministic skill detail failure');
    await capabilityInspector.getByRole('button', { name: 'Retry' }).click();
    await expect(capabilityInspector).toContainText('Plans workspace delivery');
    await capabilityInspector.getByRole('button', { name: /Back to Command Menu/ }).click();

    await skillInspect.focus();
    await page.keyboard.press('Enter');
    await expect(capabilityInspector).toBeVisible();
    const overviewTab = capabilityInspector.getByRole('tab', { name: 'Overview' });
    await overviewTab.focus();
    await page.keyboard.press('ArrowRight');
    await expect(capabilityInspector.getByRole('tab', { name: 'Instructions' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    await captureImplementationScreenshot(page, 'unit-sheet-keyboard-focused.png');
    await page.keyboard.press('Escape');
    await expect(inspector.locator('.ws-cmd-rpg-command-panel')).toContainText('Command Menu');
    await page.keyboard.press('Escape');
    await expect(page.locator('#workspaceCommandView .ws-cmd-map-window')).toHaveCount(0);

    await page.setViewportSize({ width: 390, height: 844 });
    const mobileGeometry = await page
      .locator('#workspaceCommandView .ws-cmd-opmap')
      .evaluate(() => {
        return {
          pageWidth: document.documentElement.scrollWidth,
          viewportWidth: window.innerWidth
        };
      });
    expect(mobileGeometry.pageWidth).toBeLessThanOrEqual(mobileGeometry.viewportWidth);

    await page
      .locator('#workspaceCommandView .ws-cmd-map-agent')
      .filter({ hasText: 'Research Analyst' })
      .click();
    const mobileUnitSheet = page.locator('#workspaceCommandView .ws-cmd-map-window-inspector');
    await mobileUnitSheet.getByRole('button', { name: 'Add Skill' }).click();
    const mobileAddDialog = page.getByRole('dialog', { name: 'Add Skill' });
    await mobileAddDialog.getByRole('button', { name: 'View details for skill reviewer' }).click();
    await expect(mobileAddDialog).toContainText('Reviews workspace changes before delivery');
    const mobileAddGeometry = await mobileAddDialog.evaluate(node => {
      const rect = node.getBoundingClientRect();
      return {
        top: rect.top,
        right: rect.right,
        bottom: rect.bottom,
        left: rect.left,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
        pageWidth: document.documentElement.scrollWidth
      };
    });
    expect(mobileAddGeometry.top).toBeGreaterThanOrEqual(0);
    expect(mobileAddGeometry.left).toBeGreaterThanOrEqual(0);
    expect(mobileAddGeometry.right).toBeLessThanOrEqual(mobileAddGeometry.viewportWidth);
    expect(mobileAddGeometry.bottom).toBeLessThanOrEqual(mobileAddGeometry.viewportHeight);
    expect(mobileAddGeometry.pageWidth).toBeLessThanOrEqual(mobileAddGeometry.viewportWidth);
    await captureImplementationScreenshot(page, 'unit-sheet-add-skill-modal-mobile.png');
    await mobileAddDialog.getByRole('button', { name: 'Close Add Skill' }).click();

    const mobileSkillRow = mobileUnitSheet
      .locator('.ws-cmd-loadout-row')
      .filter({ hasText: 'browser:control-in-app-browser' });
    await mobileSkillRow.getByRole('button', { name: /Inspect skill/ }).click();
    await mobileUnitSheet.getByRole('tab', { name: 'Instructions' }).click();
    await expect(mobileUnitSheet.locator('.ws-cmd-rpg-sheet')).toBeHidden();
    const mobileInspectorGeometry = await mobileUnitSheet.evaluate(node => {
      const panel = node.querySelector('.ws-cmd-capability-inspector')?.getBoundingClientRect();
      const content = node.querySelector('.ws-cmd-capability-body');
      return {
        left: panel?.left || 0,
        right: panel?.right || 0,
        viewportWidth: window.innerWidth,
        pageWidth: document.documentElement.scrollWidth,
        contentScrollable: content ? content.scrollHeight >= content.clientHeight : false
      };
    });
    expect(mobileInspectorGeometry.left).toBeGreaterThanOrEqual(0);
    expect(mobileInspectorGeometry.right).toBeLessThanOrEqual(
      mobileInspectorGeometry.viewportWidth
    );
    expect(mobileInspectorGeometry.pageWidth).toBeLessThanOrEqual(
      mobileInspectorGeometry.viewportWidth
    );
    expect(mobileInspectorGeometry.contentScrollable).toBeTruthy();
    await captureImplementationScreenshot(page, 'unit-sheet-skill-inspector-mobile.png');
    await mobileUnitSheet.getByRole('button', { name: /Back to Command Menu/ }).click();
    await page.locator('#workspaceCommandView [data-cmd-map-window-close]').click();

    await page.locator('#workspaceCommandView [data-cmd-view-mode="details"]').click();
    await expect(page.locator('#workspaceCommandView .ws-cmd-deck')).toBeVisible();
  });
});

test.describe('Task Output Contracts', () => {
  test('opens append-to-CSV contract editor from task details', async ({ page, request }) => {
    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright Output Contract ${Date.now()}`,
        description: 'Temporary workspace for output contract smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;
    const workspaceSlug = workspaceData.workspace_slug;
    expect(workspaceSlug).toBeTruthy();

    try {
      const taskResp = await request.post('/api/orchestration/tasks', {
        data: {
          workspace_id: workspaceId,
          description: 'Track NYC pollen daily',
          to: 'Ori',
          result_storage: {
            enabled: true,
            file_path: `/tmp/ori-output-contract-${workspaceId}.csv`,
            format: 'csv',
            write_mode: 'append'
          },
          output_contract: {
            source: 'manual',
            columns: [
              { name: 'date', type: 'date', required: true },
              { name: 'location', type: 'string', required: true },
              { name: 'pollen_count', type: 'number', required: true }
            ]
          }
        }
      });
      expect(taskResp.ok()).toBeTruthy();
      const taskData = await taskResp.json();
      const taskId = taskData.task?.id;
      expect(taskId).toBeTruthy();

      await page.goto(`/workspaces/${workspaceSlug}/task/${taskId}`);
      await expect(page.locator('#workspace-task-automation-storage')).toContainText(
        'Storage destination'
      );
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'date, location, pollen_count'
      );

      await page.locator('.workspace-task-advanced-summary').click();
      await expect(
        page.locator(
          '#workspace-task-automation-storage [data-action="open-automation-storage-modal"]'
        )
      ).toBeVisible();
      await page
        .locator('#workspace-task-automation-storage [data-action="open-automation-storage-modal"]')
        .click();
      await expect(page.locator('#taskModalOutputContractSection')).toBeVisible();
      await expect(page.locator('#taskModalAutoSaveWriteMode')).toHaveValue('append');
      await expect(
        page.locator('#taskModalOutputContractRows [data-output-contract-name]').first()
      ).toHaveValue('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });

  test('shows storage destination immediately after enabling CSV storage', async ({
    page,
    request
  }) => {
    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright CSV Storage ${Date.now()}`,
        description: 'Temporary workspace for CSV storage smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;
    const workspaceSlug = workspaceData.workspace_slug;
    expect(workspaceSlug).toBeTruthy();

    await page.route('**/api/orchestration/tasks/output-spec/suggest', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          output_contract: {
            source: 'ai_suggested',
            columns: [
              { name: 'date', type: 'date', required: true },
              { name: 'summary', type: 'string', required: true }
            ]
          },
          reasoning: 'Use one row per scheduled run.'
        })
      });
    });

    try {
      const taskResp = await request.post('/api/orchestration/tasks', {
        data: {
          workspace_id: workspaceId,
          description: 'Summarize the daily operations report',
          to: 'Ori',
          schedule_enabled: true,
          schedule_name: 'Daily report',
          schedule: {
            type: 'daily',
            time: '09:00'
          }
        }
      });
      expect(taskResp.ok()).toBeTruthy();
      const taskData = await taskResp.json();
      const taskId = taskData.task?.id;
      expect(taskId).toBeTruthy();

      await page.goto(`/workspaces/${workspaceSlug}/task/${taskId}`);
      await page.locator('.workspace-task-advanced-summary').click();
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'Save each run of this task to a dataset'
      );

      await page
        .locator('#workspace-task-automation-columns [data-action="toggle-csv-storage"]')
        .check();

      await expect(page.locator('#workspace-task-automation-storage')).toContainText(
        'Storage destination'
      );
      await expect(page.locator('#workspace-task-automation-storage')).toContainText('Custom path');
      await expect(page.locator('#workspace-task-automation-columns')).toContainText(
        'What each run returns'
      );
      await expect(page.locator('#workspace-task-automation-columns')).toContainText('date');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Workspace File Folders', () => {
  test('creates a folder, uploads into it, browses it, and moves the file', async ({
    page,
    request
  }) => {
    test.setTimeout(60000);

    let workspaceId = '';
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `Playwright File Folders ${Date.now()}`,
        description: 'Temporary workspace for workspace file folder smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    workspaceId = workspaceData.workspace_id;
    const workspaceSlug = workspaceData.workspace_slug;
    expect(workspaceSlug).toBeTruthy();

    try {
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
        })
      );
      await page.addInitScript(id => {
        window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
      }, workspaceId);
      await page.goto(`/workspaces/${workspaceSlug}`);
      await expect(page.locator('#workspaceCommandView .ws-cmd-files-panel')).toBeVisible();
      await page.waitForFunction(() =>
        Boolean((window as any).workspaceDetail?.fileModalManager?.fileModalElements?.modal)
      );

      await page
        .locator('#workspaceCommandView .ws-cmd-files-panel [data-cmd-primary-section="files"]')
        .click();
      await expect(page.locator('#hubAddFileModal')).toBeVisible();

      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('prompt');
        await dialog.accept('research');
      });
      await page.locator('#hubCreateUploadFolderBtn').click();
      await expect(page.locator('#hubFileFolderSelect')).toHaveValue('research');

      await page.locator('#hubFileInput').setInputFiles({
        name: 'folder-smoke-report.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('workspace folder smoke test')
      });
      await expect(page.locator('#hubSelectedFilesPreview')).toBeVisible();

      const uploadResponse = page.waitForResponse(
        response =>
          response.url().includes(`/api/workspaces/${workspaceId}/files`) &&
          response.request().method() === 'POST'
      );
      await page.locator('#hubAddFileSubmitBtn').click();
      expect((await uploadResponse).ok()).toBeTruthy();
      await expect(page.locator('#hubAddFileModal.show')).toHaveCount(0);
      await expect(page.locator('#workspaceCommandView .ws-cmd-files-panel')).toContainText(
        'folder-smoke-report.txt'
      );

      await page
        .locator(
          '#workspaceCommandView .ws-cmd-files-panel [data-cmd-open-section="files"][data-cmd-item-id]'
        )
        .filter({ hasText: 'folder-smoke-report.txt' })
        .click();
      const explorer = page.locator('#workspace-directory-explorer-modal');
      await expect(explorer).toBeVisible();
      await expect(
        explorer.locator('.workspace-directory-tree-main', { hasText: 'research' })
      ).toBeVisible();

      await expect(explorer.locator('.workspace-directory-preview-code')).toContainText(
        'workspace folder smoke test'
      );

      page.once('dialog', async dialog => {
        expect(dialog.type()).toBe('prompt');
        await dialog.accept('archive');
      });
      await explorer.locator('[data-action="move-workspace-file"]').click();
      await expect(
        explorer.locator('.workspace-directory-tree-main', { hasText: 'archive' })
      ).toBeVisible();
      await expect(explorer.locator('.workspace-directory-preview-subtitle')).toContainText(
        'archive/'
      );

      const treeResp = await request.get(`/api/workspaces/${workspaceId}/files/tree`);
      expect(treeResp.ok()).toBeTruthy();
      const treeData = await treeResp.json();
      const movedFile = (treeData.files || []).find(
        (item: any) =>
          item.relative_path?.includes('archive/') &&
          item.relative_path?.endsWith('folder-smoke-report.txt')
      );
      expect(movedFile).toBeTruthy();

      let revealPayload: any = null;
      await page.route(`**/api/workspaces/${workspaceId}/files/reveal`, async route => {
        revealPayload = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'ok' })
        });
      });
      await explorer.locator('.workspace-directory-tree-main', { hasText: 'archive' }).click();
      await expect(explorer.getByRole('button', { name: 'Open in File Manager' })).toBeVisible();
      await explorer.getByRole('button', { name: 'Open in File Manager' }).click();
      await expect.poll(() => revealPayload?.relative_path).toBe('archive');
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Personal Assistant surface ownership', () => {
  async function createTemporaryWorkspace(request, namePrefix: string) {
    const workspaceResp = await request.post('/api/orchestration/workspace', {
      data: {
        name: `${namePrefix} ${Date.now()}`,
        description: 'Temporary workspace for floating assistant smoke coverage'
      }
    });
    expect(workspaceResp.ok()).toBeTruthy();
    const workspaceData = await workspaceResp.json();
    return workspaceData.workspace_id as string;
  }

  async function suppressOnboarding(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: ['names', 'model', 'personalization'],
          user_name: 'Playwright',
          assistant_name: 'Ori'
        })
      });
    });
  }

  test('keeps the Home drawer and workspace canvas assistant surfaces distinct', async ({
    page,
    request
  }) => {
    let workspaceId = '';
    workspaceId = await createTemporaryWorkspace(request, 'Playwright Inline Assistant Regression');
    const workspaceResp = await request.get(`/api/workspaces/${workspaceId}`);
    const workspaceData = await workspaceResp.json();
    const workspaceSlug =
      workspaceData.folder_slug ||
      workspaceData.folder?.folder_slug ||
      workspaceData.workspace?.folder_slug;
    expect(workspaceSlug).toBeTruthy();

    try {
      await suppressOnboarding(page);
      await installActivePersonalAssistant(page);
      await page.goto('/');
      await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
      await expect(page.locator('#personalAssistantPanel')).toBeHidden();
      await expect(page.locator('#homeAssistantCard.modern-card')).toHaveCount(0);
      await expect(page.locator('#hubSupportChat')).toHaveCount(0);

      await page.goto(`/workspaces/${workspaceSlug}/canvas`);
      await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
      await page.locator('#personalAssistantLauncher').click();
      await expect(page.locator('#personalAssistantPanel')).toBeVisible();
      await expect(page.locator('#personalAssistantTodayTab')).toHaveCount(0);
      await expect(page.locator('#personalAssistantInput')).toBeVisible();
      await expect(page.locator('#hubSupportChat')).toHaveCount(0);
    } finally {
      if (workspaceId) {
        await request.delete(`/api/orchestration/workspace?id=${workspaceId}`);
      }
    }
  });
});

test.describe('Home Advisory Routing', () => {
  test.beforeEach(async ({ page }) => installActivePersonalAssistant(page));

  async function installCompletedOnboarding(page) {
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      });
    });
  }

  async function installAssistantPreferredOriRoute(page) {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_preferred',
          context_mode: 'direct',
          handoff_policy: 'assistant',
          matched_agent: 'Ori',
          score: 5,
          requires_creation: false,
          workspace_recommended: false,
          route_mode: 'specialist_handoff',
          target_surface: 'chat',
          reasons: ['fallback to system assistant'],
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general'
        })
      });
    });
  }

  test('answers implementation advisory questions inline with Ori', async ({ page }) => {
    let chatCalls = 0;
    let chatQuestion = '';
    await installCompletedOnboarding(page);
    await installAssistantPreferredOriRoute(page);
    await page.route('**/api/chat', async route => {
      chatCalls += 1;
      chatQuestion = String(route.request().postDataJSON().question || '');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response:
            'Use the existing task and worktree records as the source of truth, then add a read-only implementation status projection.'
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).__assistantChatOpens = 0;
      (window as any).chatPanel = {
        open() {
          (window as any).__assistantChatOpens += 1;
        }
      };
      (window as any).sessionManager = {
        sessions: [],
        activeSessionId: '',
        async createSessionWithAgent(agentName: string) {
          const session = { id: 'sess-ori-advisory', agent_name: agentName };
          this.sessions.push(session);
          this.activeSessionId = session.id;
          return session;
        }
      };
      (window as any).sendMessageToChat = async () => {};
    });

    const prompt =
      'How should we improve visibility into ongoing implementations in the Ori DevOps workspace?';
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill(prompt);
    await page.locator('#personalAssistantSend').click();

    await expect.poll(() => chatCalls).toBe(1);
    expect(chatQuestion).toContain(prompt);
    await expect(page.locator('#homeAssistantConversation')).toContainText(
      'Use the existing task and worktree records as the source of truth'
    );
    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText(
      'Answered inline with "Ori".'
    );

    const actions = page.locator('#homeAssistantActions');
    await expect(actions.getByText('Create Workspace', { exact: true })).toHaveCount(0);
    const continueButton = actions.getByText('Continue in Chat', { exact: true });
    await expect(continueButton).toBeEnabled();
    await continueButton.click();
    await expect.poll(() => page.evaluate(() => (window as any).__assistantChatOpens)).toBe(1);
  });

  test('keeps explicit implementation commands in the workspace capability flow', async ({
    page
  }) => {
    let chatCalls = 0;
    await installCompletedOnboarding(page);
    await installAssistantPreferredOriRoute(page);
    await page.route('**/api/chat', async route => {
      chatCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ response: 'unexpected' })
      });
    });

    await page.goto('/');
    await openPersonalAssistantAsk(page);
    await page
      .locator('#personalAssistantInput')
      .fill('Implement support for deployment status in Ori');
    await page.locator('#personalAssistantSend').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Workspace Available');
    await expect(
      page.locator('#homeAssistantActions').getByText('Create Workspace', { exact: true })
    ).toBeVisible();
    expect(chatCalls).toBe(0);
  });
});

test.describe('Home Workspace Routing', () => {
  test.beforeEach(async ({ page }) => {
    await installActivePersonalAssistant(page);
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
      })
    );
  });

  async function installWorkspaceAssistantMocks(
    page,
    options: {
      workspaceId: string;
      entryAgentName: string;
      onChat?: () => void;
    }
  ) {
    await page.route(`**/api/workspaces/${options.workspaceId}`, async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: options.workspaceId,
          entry_agent_name: options.entryAgentName
        })
      });
    });
    await page.route('**/api/chat', async route => {
      options.onChat?.();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Workspace manager is ready.'
        })
      });
    });
  }

  test('asks the user to choose when workspace routing is ambiguous', async ({ page }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          score: 0,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'ambiguous',
            candidates: [
              {
                id: 'ws-alpha',
                name: 'Launch Alpha',
                score: 8,
                reasons: ['matched workspace goal']
              },
              { id: 'ws-beta', name: 'Launch Beta', score: 7, reasons: ['matched workspace goal'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('ship the launch plan');
    await page.locator('#personalAssistantSend').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Choose Workspace');
    await expect(page.getByText('Launch Alpha', { exact: true })).toBeVisible();
    await expect(page.getByText('Launch Beta', { exact: true })).toBeVisible();
    await expect(page.getByText('Create New Workspace', { exact: true })).toBeVisible();
  });

  test('offers workspace creation when no existing workspace fits', async ({ page }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Ori',
          score: 4,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'no_fit',
            candidates: []
          }
        })
      });
    });

    await page.goto('/');
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('build a robotics dashboard from scratch');
    await page.locator('#personalAssistantSend').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Workspace Needed');
    const actions = page.locator('#homeAssistantActions');
    await expect(actions.getByText('Create Workspace', { exact: true })).toBeVisible();
    await expect(actions.getByText('Continue in Chat', { exact: true })).toBeVisible();
  });

  test('hands a confident workspace match to the workspace assistant', async ({ page }) => {
    let chatCalls = 0;
    await installWorkspaceAssistantMocks(page, {
      workspaceId: 'ws-cabinet',
      entryAgentName: 'Cabinet Manager',
      onChat: () => {
        chatCalls += 1;
      }
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Cabinet Manager',
          score: 8,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'confident',
            selected_workspace_id: 'ws-cabinet',
            selected_workspace_name: 'Cabinet',
            confidence: 0.99,
            reasons: ['matched workspace name'],
            candidates: [
              { id: 'ws-cabinet', name: 'Cabinet', score: 12, reasons: ['matched workspace name'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-cabinet', agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('build the cabinet roadmap');
    await page.locator('#personalAssistantSend').click();

    await expect.poll(() => chatCalls).toBe(1);
    await expect(page.locator('#homeAssistantConversation')).toContainText(
      'Workspace manager is ready.'
    );
    await expect(page.locator('#homeAssistantActions')).toContainText('Choose Another Workspace');
  });

  test('lets the user override a confident workspace match', async ({ page }) => {
    await page.route('**/api/workspaces/ws-cabinet', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-cabinet',
          entry_agent_name: 'Cabinet Manager'
        })
      });
    });
    await page.route('**/api/workspaces/ws-ops', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'ws-ops',
          entry_agent_name: 'Ops Manager'
        })
      });
    });
    await page.route('**/api/chat', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          response: 'Workspace manager is ready.'
        })
      });
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Cabinet Manager',
          score: 8,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'confident',
            selected_workspace_id: 'ws-cabinet',
            selected_workspace_name: 'Cabinet',
            confidence: 0.99,
            reasons: ['matched workspace name'],
            candidates: [
              { id: 'ws-cabinet', name: 'Cabinet', score: 12, reasons: ['matched workspace name'] },
              { id: 'ws-ops', name: 'Ops Hub', score: 9, reasons: ['matched workspace goal'] }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).__handoffWorkspaceIds = [];
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          (window as any).__handoffWorkspaceIds.push(folderId);
          return { id: `sess-${folderId}`, agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('build the cabinet roadmap');
    await page.locator('#personalAssistantSend').click();

    await expect
      .poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds))
      .toEqual(['ws-cabinet']);
    await page
      .locator('#homeAssistantActions')
      .getByText('Choose Another Workspace', { exact: true })
      .click();
    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Choose Workspace');
    await page.locator('#homeAssistantActions').getByText('Ops Hub', { exact: true }).click();
    await expect
      .poll(() => page.evaluate(() => (window as any).__handoffWorkspaceIds))
      .toEqual(['ws-cabinet', 'ws-ops']);
  });

  test('shows a repair-required state when the matched workspace has no runnable entry agent', async ({
    page
  }) => {
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          score: 0,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'needs_repair',
            selected_workspace_id: 'ws-broken',
            selected_workspace_name: 'Broken Ops',
            repair_reason: 'workspace has no entry agent',
            candidates: [
              {
                id: 'ws-broken',
                name: 'Broken Ops',
                score: 12,
                reasons: ['matched workspace name']
              }
            ]
          }
        })
      });
    });

    await page.goto('/');
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('build the broken ops roadmap');
    await page.locator('#personalAssistantSend').click();

    await expect(page.locator('#homeAssistantRoutingSummary')).toContainText('Commander Required');
    await expect(
      page.locator('#homeAssistantActions').getByText('Open Workspace Setup', { exact: true })
    ).toBeVisible();
  });

  test('resumes a created workspace prompt once the new workspace is ready', async ({ page }) => {
    let chatCalls = 0;
    await installWorkspaceAssistantMocks(page, {
      workspaceId: 'ws-new',
      entryAgentName: 'New Workspace Manager',
      onChat: () => {
        chatCalls += 1;
      }
    });
    await page.route('**/api/home-assistant/route', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          intent: 'general_task',
          intent_label: 'general task',
          routing_policy: 'assistant_only',
          context_mode: 'workspace',
          handoff_policy: 'assistant',
          matched_agent: 'Ori',
          score: 4,
          requires_creation: false,
          workspace_recommended: true,
          route_mode: 'workspace_task',
          target_surface: 'workspace',
          suggested_agent_name: 'Task Assistant',
          suggested_agent_type: 'general',
          workspace_resolution: {
            state: 'no_fit',
            candidates: []
          }
        })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-new', agent_name: agentName, folder_id: folderId };
        }
      };
    });
    await openPersonalAssistantAsk(page);
    await page.locator('#personalAssistantInput').fill('build a robotics dashboard from scratch');
    await page.locator('#personalAssistantSend').click();
    await page
      .locator('#homeAssistantActions')
      .getByText('Create Workspace', { exact: true })
      .click();

    await page.evaluate(() => {
      window.dispatchEvent(
        new CustomEvent('ori:workspace-created', {
          detail: { workspaceId: 'ws-new', workspaceName: 'New Workspace' }
        })
      );
      return (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-new',
        page_path: '/workspaces/ws-new',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      });
    });

    await expect.poll(() => chatCalls).toBe(1);
  });

  test('waits for repair before resuming a preserved workspace prompt', async ({ page }) => {
    let workspaceReady = false;
    let chatCalls = 0;
    await page.route('**/api/workspaces/ws-broken', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          workspaceReady
            ? {
                id: 'ws-broken',
                entry_agent_name: 'Broken Ops Manager'
              }
            : {
                id: 'ws-broken'
              }
        )
      });
    });
    await page.route('**/api/chat', async route => {
      chatCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ response: 'Workspace repaired and ready.' })
      });
    });

    await page.goto('/');
    await page.evaluate(() => {
      window.sessionStorage.setItem(
        'ori.homeAssistant.pendingWorkspacePrompt',
        JSON.stringify({
          prompt: 'finish the broken ops roadmap',
          routeContext: {
            surface: 'dashboard',
            page_path: '/',
            workspace_id: '',
            session_id: '',
            origin: 'ask_ori'
          },
          expectedWorkspaceId: 'ws-broken',
          intentKey: 'general_task',
          source: 'repair',
          createdAt: Date.now()
        })
      );
      (window as any).sessionManager = {
        sessions: [],
        async createSessionWithAgentInFolder(agentName: string, folderId: string) {
          return { id: 'sess-repaired', agent_name: agentName, folder_id: folderId };
        }
      };
    });

    await page.evaluate(() =>
      (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-broken',
        page_path: '/workspaces/ws-broken',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      })
    );
    expect(chatCalls).toBe(0);

    workspaceReady = true;
    await page.evaluate(() =>
      (window as any).OriAskRouting.refreshWorkspaceIdentity({
        workspace_id: 'ws-broken',
        page_path: '/workspaces/ws-broken',
        surface: 'workspace_detail',
        origin: 'ask_ori'
      })
    );
    await expect.poll(() => chatCalls).toBe(1);
  });
});

test.describe('Settings Workspace Directory', () => {
  // Saving a directory now applies it to the running app (Issue #353), so the
  // Settings control has to report what that actually changed rather than a
  // bare "saved".
  async function installWorkspaceRootRoutes(
    page,
    { refresh, failSave = false, saveDelayMs = 0 } = {}
  ) {
    // Settings is only reachable past onboarding; its modal would otherwise
    // sit over the page and swallow every click.
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: [0, 1, 2],
          user_name: 'Smoke Tester',
          assistant_name: 'Ori'
        })
      });
    });
    await page.route('**/api/settings/workspace-root', async route => {
      if (route.request().method() !== 'POST') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            workspace_root: '/tmp/ori-root-a',
            effective_workspace_root: '/tmp/ori-root-a',
            default_workspace_root: '/tmp/ori-default',
            source: 'settings',
            confirmed: true
          })
        });
        return;
      }

      if (saveDelayMs > 0) {
        await new Promise(resolve => setTimeout(resolve, saveDelayMs));
      }

      if (failSave) {
        await route.fulfill({
          status: 400,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Unable to use workspace directory' })
        });
        return;
      }

      const requested = route.request().postDataJSON().workspace_root;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          workspace_root: requested,
          effective_workspace_root: requested,
          default_workspace_root: '/tmp/ori-default',
          source: 'settings',
          confirmed: true,
          refresh
        })
      });
    });
  }

  test('reports the live refresh after a save, without the missing-folder wording', async ({
    page
  }) => {
    await installWorkspaceRootRoutes(page, {
      refresh: { imported: 2, reparented: 0, orphaned: 1, restored: 0, warnings: [] },
      saveDelayMs: 300
    });
    await page.goto('/settings');

    const input = page.locator('#workspaceRootInput');
    await expect(input).toHaveValue('/tmp/ori-root-a');

    await input.fill('/tmp/ori-root-b');
    const saveBtn = page.locator('#saveWorkspaceRootBtn');
    await saveBtn.click();

    // The control announces that it is working and cannot be double-submitted.
    await expect(saveBtn).toBeDisabled();
    await expect(saveBtn).toContainText('Saving...');

    const toast = page.locator('#toastContainer .toast').first();
    await expect(toast).toContainText('2 workspaces added');
    await expect(toast).toContainText('1 workspace hidden');
    // Those folders are exactly where they were — this is not the Rescan case.
    await expect(toast).not.toContainText('missing on disk');
    await expect(saveBtn).toBeEnabled();
    await expect(page.locator('#workspaceRootStatusDetails')).toContainText('/tmp/ori-root-b');
  });

  test('surfaces refresh warnings instead of a clean success', async ({ page }) => {
    await installWorkspaceRootRoutes(page, {
      refresh: {
        imported: 1,
        reparented: 0,
        orphaned: 0,
        restored: 0,
        warnings: ['Failed to import Notes']
      }
    });
    await page.goto('/settings');

    await page.locator('#workspaceRootInput').fill('/tmp/ori-root-b');
    await page.locator('#saveWorkspaceRootBtn').click();

    const toast = page.locator('#toastContainer .toast').first();
    await expect(toast).toContainText('1 warning: Failed to import Notes');
  });

  test('a rejected directory leaves the editor usable and claims no success', async ({ page }) => {
    await installWorkspaceRootRoutes(page, { failSave: true });
    await page.goto('/settings');

    const input = page.locator('#workspaceRootInput');
    await input.fill('/tmp/ori-not-a-directory');
    const saveBtn = page.locator('#saveWorkspaceRootBtn');
    await saveBtn.click();

    const toast = page.locator('#toastContainer .toast').first();
    await expect(toast).toContainText('Failed to save workspace directory');
    await expect(toast).not.toContainText('workspace list is unchanged');

    // Still editable, still submittable, and the draft is intact.
    await expect(saveBtn).toBeEnabled();
    await expect(input).toBeEditable();
    await expect(input).toHaveValue('/tmp/ori-not-a-directory');
  });
});
