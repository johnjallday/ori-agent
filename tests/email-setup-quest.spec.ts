import { expect, test, type Page } from '@playwright/test';

// Browser coverage for the Email Ops host setup quest (#455). The host quest
// routes are mocked with a small stateful fixture in the server's response
// shape, the way plugin-setup-quests.spec.ts mocks its plugin quest, so no
// test reaches Google, a vault, or a model. Templates, the creator, and the
// quest catalog are the real server's.

const QUEST_ID = 'email_ops_setup';
const ROOT = `/api/host-setup-quests/${QUEST_ID}`;
const QUEST_URL = `/?setup=quest&source=host&quest=${QUEST_ID}`;
const WORKSPACE_ROUTE = '/workspaces/email-ops';

// Endpoints that can reach an AI model. The quest must never call them.
const MODEL_ENDPOINT =
  /^\/api\/(chat$|home-assistant\/ask|orchestration\/tasks\/(execute|auto-parse|output-contract\/suggest|output-spec\/suggest)|notes\/generate|cli-agents\/tasks|workspaces\/[^/]+\/assistant-program\/suggestions\/generate)/;

type Stage = 'team' | 'connect' | 'mailbox' | 'ready';

type QuestState = {
  exists: boolean;
  stage: Stage;
  revision: number;
  dismissed: boolean;
  modelAvailable: boolean;
  completedAt: string;
  staleNextCommit: boolean;
};

function newQuestState(overrides: Partial<QuestState> = {}): QuestState {
  return {
    exists: true,
    stage: 'team',
    revision: 2,
    dismissed: false,
    modelAvailable: false,
    completedAt: '',
    staleNextCommit: false,
    ...overrides
  };
}

const ORDER: Stage[] = ['team', 'connect', 'mailbox', 'ready'];

function stepStatus(state: QuestState, id: Stage) {
  const at = ORDER.indexOf(state.stage);
  const index = ORDER.indexOf(id);
  if (state.stage === 'ready' || index < at) return 'complete';
  return index === at ? 'active' : 'pending';
}

function journeyFixture(state: QuestState) {
  const ready = state.stage === 'ready';
  const created = ORDER.indexOf(state.stage) > 0;
  const connected = ORDER.indexOf(state.stage) > 1;
  const steps = [
    {
      id: 'team',
      kind: 'workspace_create',
      title: 'Review your Email Ops team',
      description: 'Create the Email Ops workspace with its Inbox agent.',
      status: stepStatus(state, 'team'),
      workspace_create: {
        template_title: 'Email Ops',
        workspace_id: created ? 'ws-email' : '',
        workspace_label: created ? 'Email Ops' : '',
        workspace_route: created ? WORKSPACE_ROUTE : ''
      },
      actions: created
        ? [{ id: 'open_workspace', label: 'Open workspace', effect: 'navigation' }]
        : [{ id: 'review_team', label: 'Review team', effect: 'navigation' }]
    },
    {
      id: 'connect',
      kind: 'account_connect',
      title: 'Connect Gmail',
      description: 'Connect Google and enable Gmail in Settings.',
      status: stepStatus(state, 'connect'),
      account_connect: {
        configured: true,
        identity_email: connected ? 'demo.user@example.com' : '',
        gmail_health: connected ? 'healthy' : 'not_connected',
        action_label: connected ? '' : 'Connect Google',
        action_url: '/settings#google-account'
      },
      actions:
        state.stage === 'connect'
          ? [
              { id: 'open_account_settings', label: 'Open Google Account', effect: 'navigation' },
              { id: 'recheck_connection', label: 'Check again', effect: 'navigation' }
            ]
          : []
    },
    {
      id: 'mailbox',
      kind: 'account_link',
      title: 'Link the mailbox',
      description:
        'Link the connected account to this workspace. Email Ops reads and searches it and never sends without your confirmation.',
      status: stepStatus(state, 'mailbox'),
      account_link: {
        workspace_label: 'Email Ops',
        account_email: 'demo.user@example.com',
        linked: ready,
        ready
      },
      actions:
        state.stage === 'mailbox'
          ? [{ id: 'review_mailbox_link', label: 'Review mailbox link', effect: 'review' }]
          : []
    },
    {
      id: 'summary',
      kind: 'summary',
      title: 'Email Ops is ready',
      description:
        'Your mailbox is linked and Ori can read and search it. Triaging the inbox is a separate task you start when you are ready.',
      status: ready ? 'complete' : 'pending',
      actions: ready
        ? [
            { id: 'open_workspace', label: 'Open Email Ops', effect: 'navigation' },
            state.modelAvailable
              ? { id: 'start_inbox_triage', label: 'Start inbox triage', effect: 'navigation' }
              : { id: 'open_model_settings', label: 'Set up a model', effect: 'navigation' }
          ]
        : []
    }
  ];
  const lifecycle = ready ? 'ready' : 'in_progress';
  return {
    run_id: 'email-quest-root',
    run_kind: 'root',
    state_revision: state.revision,
    journey: {
      source: 'host',
      template_id: 'email-ops',
      id: QUEST_ID,
      schema_version: 1,
      version: 1,
      title: 'Set up Email Ops',
      description: 'Create your inbox command post, connect Gmail, and link the mailbox.'
    },
    lifecycle_state: lifecycle,
    current_step_id: ready ? '' : state.stage,
    dismissed: state.dismissed,
    receipts: created ? { project_workspace_id: 'ws-email' } : {},
    first_opened_at: '2026-09-15T10:00:00Z',
    ...(state.completedAt ? { first_completed_at: state.completedAt } : {}),
    updated_at: '2026-09-15T10:05:00Z',
    steps
  };
}

type QuestTraffic = { writes: string[]; modelRequests: string[]; commits: unknown[] };

async function mockQuest(page: Page, state: QuestState): Promise<QuestTraffic> {
  const traffic: QuestTraffic = { writes: [], modelRequests: [], commits: [] };
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (MODEL_ENDPOINT.test(path)) traffic.modelRequests.push(`${request.method()} ${path}`);
  });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false, current_step: 'complete' } })
  );
  await page.route(
    url => url.pathname === ROOT || url.pathname.startsWith(`${ROOT}/`),
    async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      const fulfill = (json: unknown, status = 200) => route.fulfill({ status, json });
      if (path === `${ROOT}/status`) {
        await fulfill(
          state.exists ? { exists: true, setup_journey: journeyFixture(state) } : { exists: false }
        );
        return;
      }
      if (request.method() === 'GET') {
        state.exists = true;
        await fulfill({ setup_journey: journeyFixture(state) });
        return;
      }
      traffic.writes.push(path.slice(ROOT.length));
      const body = request.postDataJSON() || {};
      if (path.endsWith('/open')) {
        state.exists = true;
        state.dismissed = false;
        state.revision++;
      } else if (path.endsWith('/dismiss')) {
        expect(body.if_revision).toBe(state.revision);
        state.dismissed = true;
        state.revision++;
      } else if (path.endsWith('/actions/review_mailbox_link')) {
        await fulfill({
          setup_journey: journeyFixture(state),
          review: {
            token: `review-${state.revision}`,
            commit_action: 'link_mailbox',
            expires_at: '2026-09-15T10:20:00Z',
            account_link: {
              workspace_label: 'Email Ops',
              account_email: 'demo.user@example.com',
              linked: false,
              ready: false
            }
          }
        });
        return;
      } else if (path.endsWith('/actions/link_mailbox')) {
        traffic.commits.push(body);
        if (state.staleNextCommit) {
          state.staleNextCommit = false;
          state.revision++;
          await fulfill(
            {
              error: {
                reason_code: 'review_stale',
                guidance: 'The mailbox link changed after review. Review it again.'
              },
              current: journeyFixture(state)
            },
            409
          );
          return;
        }
        state.stage = 'ready';
        state.completedAt ||= '2026-09-15T10:05:00Z';
        state.revision++;
      }
      await fulfill({ setup_journey: journeyFixture(state) });
    }
  );
  return traffic;
}

const dialog = (page: Page) => page.locator('#specialistSetupJourneyModal');
const stepTitle = (page: Page) => page.locator('#specialistSetupJourneyStepTitle');
const action = (page: Page, id: string) =>
  page.locator(`#specialistSetupJourneyActions [data-action="${id}"]`);

async function closeQuest(page: Page) {
  await page.locator('#specialistSetupJourneyClose').click();
  await expect(dialog(page)).toBeHidden();
}

async function openQuestsFlyout(page: Page) {
  const toggle = page.locator('#cockpitQuestsToggle');
  await expect(toggle).toBeVisible();
  if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
  await expect(page.locator('#cockpitQuestsFlyout')).toBeVisible();
}

async function expectWithinViewport(page: Page, width: number) {
  await expect
    .poll(async () => {
      const bounds = await dialog(page).locator('.modal-dialog').boundingBox();
      return Boolean(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width + 1);
    })
    .toBe(true);
}

for (const width of [1280, 400]) {
  test.describe(`Email Ops setup quest at ${width}px`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 860 });
    });

    test('every entry point opens the one saved quest and Home never auto-opens it', async ({
      page
    }, testInfo) => {
      const state = newQuestState();
      const traffic = await mockQuest(page, state);

      // Home without the quest link loads the card's status read only.
      await page.goto('/');
      await expect(page.locator('#homeCockpit')).toBeVisible();
      await page.waitForTimeout(800);
      await expect(dialog(page)).toBeHidden();
      expect(traffic.writes).toEqual([]);

      // 1. Direct URL.
      await page.goto(QUEST_URL);
      await expect(dialog(page)).toBeVisible();
      await expect(stepTitle(page)).toHaveText('Review your Email Ops team');
      await expectWithinViewport(page, width);
      await dialog(page).screenshot({ path: testInfo.outputPath(`url-entry-${width}.png`) });
      await closeQuest(page);

      // 2. The assistant's Email capability.
      await page.route(
        url => url.pathname === '/api/personal-assistant',
        route =>
          route.fulfill({
            json: {
              personal_assistant: {
                state: 'active',
                state_version: 3,
                display_name: 'Atlas',
                mandate: 'Keep my week organised.',
                next_action: 'none'
              }
            }
          })
      );
      await page.route('**/api/personal-assistant/capabilities', route =>
        route.fulfill({
          json: {
            capabilities: {
              state: 'active',
              cards: [
                {
                  key: 'email',
                  label: 'Email',
                  status: 'not_configured',
                  can_read: 'Attention signals and message context.',
                  can_propose: 'Follow-ups and draft replies.',
                  requires_confirmation: 'No external email write is mapped.',
                  mapped_write: false,
                  action_label: 'Set up email',
                  action_route: QUEST_URL
                },
                // The real projection lists the other sources after Email.
                ...['Calendar', 'Projects and workspaces', 'Approved folders'].map(label => ({
                  key: label.toLowerCase().replaceAll(' ', '_'),
                  label,
                  status: 'healthy_empty',
                  can_read: 'Bounded summaries.',
                  can_propose: 'Internal follow-ups.',
                  requires_confirmation: 'Uses the existing confirmation gate.',
                  mapped_write: false,
                  action_label: '',
                  action_route: ''
                }))
              ]
            }
          }
        })
      );
      await page.goto('/?personal-assistant=working-agreement');
      const setUpEmail = page.locator('#personalAssistantCapabilities a', {
        hasText: 'Set up email'
      });
      await expect(setUpEmail).toHaveAttribute('href', QUEST_URL);
      // The assistant dock is fixed to the bottom edge; keep the link clear of it.
      await setUpEmail.evaluate(link => link.scrollIntoView({ block: 'center' }));
      await setUpEmail.click();
      await expect(dialog(page)).toBeVisible();
      await expect(stepTitle(page)).toHaveText('Review your Email Ops team');
      await closeQuest(page);

      // 3. The Templates page, after its quest catalog fails once and retries.
      let catalogFailures = 0;
      await page.route('**/api/setup-quests', async route => {
        if (catalogFailures === 0) {
          catalogFailures++;
          await route.fulfill({ status: 503, json: { error: 'unavailable' } });
          return;
        }
        await route.continue();
      });
      await page.goto('/templates');
      await page.locator('#tplList [role="listitem"]').filter({ hasText: 'Email Ops' }).click();
      await expect(page.locator('#tplQuestStatus')).toContainText(
        'Guided setup could not be loaded'
      );
      await page.locator('#tplQuestRetry').click();
      await expect(page.locator('#tplQuestHeading')).toHaveText('Set up Email Ops');
      await expect(page.locator('#tplQuestOwnership')).toHaveText('Ori built-in · read-only.');
      await expect(page.locator('#tplQuestOpen')).toHaveAttribute('href', QUEST_URL);
      await page
        .locator('#tplSetupQuest')
        .screenshot({ path: testInfo.outputPath(`templates-entry-${width}.png`) });
      await page.locator('#tplQuestOpen').click();
      await expect(dialog(page)).toBeVisible();
      await expect(stepTitle(page)).toHaveText('Review your Email Ops team');
      await closeQuest(page);

      // 4. The Workspace creator's template picker.
      await page.goto('/');
      await page.evaluate(() => {
        const modal = document.getElementById('addFolderModal');
        (window as any).bootstrap.Modal.getOrCreateInstance(modal).show();
      });
      const creator = page.locator('#addFolderModal');
      await expect(creator).toBeVisible();
      await creator.locator('[data-template-id="email-ops"]').first().click();
      const guided = creator.getByRole('link', { name: 'Open Guided Setup', exact: true });
      await expect(guided).toBeVisible();
      await guided.click();
      await expect(creator).toBeHidden();
      await expect(dialog(page)).toBeVisible();
      await expect(stepTitle(page)).toHaveText('Review your Email Ops team');

      expect(traffic.modelRequests).toEqual([]);
      // Entry points only open the quest; closing a host quest never dismisses it.
      expect(traffic.writes.length).toBeGreaterThan(0);
      expect(traffic.writes.filter(path => !path.endsWith('/open'))).toEqual([]);
    });

    test('team, connect, and reviewed mailbox link reach the summary', async ({
      page,
      context
    }, testInfo) => {
      const state = newQuestState();
      const traffic = await mockQuest(page, state);

      // Step 1 launches the shared creator on its Team step with Inbox locked.
      await page.goto(QUEST_URL);
      await expect(dialog(page)).toBeVisible();
      await action(page, 'review_team').click();
      const creator = page.locator('#addFolderModal');
      await expect(creator).toBeVisible();
      await expect(page.locator('#wizardStep3')).toBeVisible({ timeout: 20000 });
      await expect(page.locator('#folderNameInput')).toHaveValue('Email Ops');
      const inbox = page.locator('#workspaceRoleRoster [data-role-id="inbox"]');
      await expect(inbox).toBeVisible();
      await inbox.getByRole('button', { name: 'Clear' }).click();
      // The lock refuses the change, says why, and leaves the role filled.
      await expect(page.getByText('Keep “Inbox” on this team.').first()).toBeVisible();
      await expect(inbox.getByRole('button', { name: 'Clear' })).toBeVisible();
      await creator.screenshot({ path: testInfo.outputPath(`team-step-${width}.png`) });
      // Leaving the creator returns to the quest without creating anything.
      await page.keyboard.press('Escape');
      await expect(creator).toBeHidden();
      await expect(dialog(page)).toBeVisible();
      await closeQuest(page);

      // Step 2: Settings opens in a new tab; Check again re-reads.
      state.stage = 'connect';
      await page.goto(QUEST_URL);
      await expect(stepTitle(page)).toHaveText('Connect Gmail');
      const [settings] = await Promise.all([
        context.waitForEvent('page'),
        action(page, 'open_account_settings').click()
      ]);
      await expect.poll(() => settings.url()).toContain('/settings#google-account');
      await settings.close();
      await expect(dialog(page)).toBeVisible();
      await expect(page.locator('#specialistSetupJourneyLiveStatus')).toContainText(
        'Google Account opened in a new tab'
      );
      state.stage = 'mailbox';
      await action(page, 'recheck_connection').click();
      await expect(stepTitle(page)).toHaveText('Link the mailbox');

      // Step 3: a stale review is refused and re-reviewed before linking.
      state.staleNextCommit = true;
      await action(page, 'review_mailbox_link').click();
      const review = page.locator('#specialistSetupJourneyReview');
      await expect(review).toBeVisible();
      await expect(review).toContainText('demo.user@example.com');
      await expect(review).toContainText('Email Ops');
      await expectWithinViewport(page, width);
      await dialog(page).screenshot({ path: testInfo.outputPath(`link-review-${width}.png`) });
      await review.locator('.setup-journey__review-confirm').click();
      await expect(page.locator('#specialistSetupJourneyError')).toContainText('Review it again');
      await expect(review).toBeHidden();
      await action(page, 'review_mailbox_link').click();
      await review.locator('.setup-journey__review-confirm').click();

      // Summary without a model offers model settings, never triage.
      await expect(stepTitle(page)).toHaveText('Email Ops is ready');
      await expect(action(page, 'open_model_settings')).toBeVisible();
      await expect(action(page, 'start_inbox_triage')).toHaveCount(0);
      await expect(dialog(page)).toContainText(
        'Triage needs an AI model; mailbox readiness does not.'
      );
      await dialog(page).screenshot({ path: testInfo.outputPath(`summary-no-model-${width}.png`) });
      expect(traffic.commits).toHaveLength(2);
      for (const commit of traffic.commits as Array<Record<string, unknown>>) {
        expect(Object.keys(commit).sort()).toEqual(
          ['idempotency_key', 'if_revision', 'input', 'review_token'].sort()
        );
        expect(commit.input).toEqual({});
      }
      await action(page, 'open_model_settings').click();
      await page.waitForURL('**/settings#system-model');

      // Summary with a model offers triage, which only opens the tasks panel.
      state.modelAvailable = true;
      await page.goto(QUEST_URL);
      await expect(stepTitle(page)).toHaveText('Email Ops is ready');
      await expect(action(page, 'open_model_settings')).toHaveCount(0);
      await dialog(page).screenshot({ path: testInfo.outputPath(`summary-model-${width}.png`) });
      await action(page, 'start_inbox_triage').click();
      await page.waitForURL(`**${WORKSPACE_ROUTE}?panel=tasks`);

      expect(traffic.modelRequests).toEqual([]);
    });

    test('the quiet Home card resumes, dismisses, and never returns after completion', async ({
      page
    }, testInfo) => {
      const state = newQuestState({ exists: false });
      const traffic = await mockQuest(page, state);
      const card = page.locator('#emailSetupQuestCard');

      // Never started: no card and no writes.
      await page.goto('/');
      await openQuestsFlyout(page);
      await page.waitForTimeout(600);
      await expect(card).toBeHidden();
      expect(traffic.writes).toEqual([]);

      // Started and left unfinished: the card shows the current step.
      Object.assign(state, { exists: true, stage: 'connect' });
      await page.goto(QUEST_URL);
      await expect(dialog(page)).toBeVisible();
      await closeQuest(page);
      expect(traffic.writes.some(path => path.endsWith('/dismiss'))).toBe(false);
      await page.goto('/');
      await openQuestsFlyout(page);
      await expect(card).toBeVisible();
      await expect(card).toContainText('Step 2 of 4 · Connect Gmail');
      await card.scrollIntoViewIfNeeded();
      await card.screenshot({ path: testInfo.outputPath(`resume-card-${width}.png`) });

      // Resume opens the quest in place.
      await card.getByRole('link', { name: /Resume/ }).click();
      await expect(dialog(page)).toBeVisible();
      await expect(stepTitle(page)).toHaveText('Connect Gmail');
      await closeQuest(page);

      // Not now persists the dismissal and survives a reload.
      await openQuestsFlyout(page);
      await expect(card).toBeVisible();
      const revision = state.revision;
      await card.getByRole('button', { name: 'Not now' }).click();
      await expect(card).toBeHidden();
      expect(state.dismissed).toBe(true);
      expect(state.revision).toBe(revision + 1);
      await page.goto('/');
      await openQuestsFlyout(page);
      await page.waitForTimeout(600);
      await expect(card).toBeHidden();

      // Completing the quest keeps the card away, even after a regression.
      Object.assign(state, {
        dismissed: false,
        stage: 'ready',
        completedAt: '2026-09-15T10:05:00Z'
      });
      await page.goto('/');
      await openQuestsFlyout(page);
      await page.waitForTimeout(600);
      await expect(card).toBeHidden();
      state.stage = 'connect';
      await page.goto('/');
      await openQuestsFlyout(page);
      await page.waitForTimeout(600);
      await expect(card).toBeHidden();

      expect(traffic.modelRequests).toEqual([]);
    });
  });
}
