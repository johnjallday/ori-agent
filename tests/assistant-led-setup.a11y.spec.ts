import { test, expect } from '@playwright/test';

const proposal = {
  schema_version: 1,
  capability_id: 'file-janitor',
  view_state: 'proposed',
  stale: false,
  status_message: 'Review the exact workspace and File Curator setup before anything is created.',
  relationship: {
    assistant_id: 'assistant-a11y',
    display_name: 'Ari',
    state: 'needs_hq',
    eligible: true,
    proactive: true
  },
  proposal: {
    revision: 'review-a11y',
    mode: 'create',
    blueprint_id: 'file-janitor',
    blueprint_version: 2,
    blueprint_digest: 'blueprint-a11y',
    team_plan_revision: 'team-a11y',
    team: [
      {
        role_id: 'file-curator',
        name: 'File Curator',
        action: 'create',
        model_configured: false,
        config_digest: 'role-a11y'
      }
    ],
    targets: [],
    metadata_disclosure:
      'File Janitor classifies files from names, types, sizes, and dates only. It does not read file contents or send them to a model.',
    folder_disclosure:
      'You will choose one folder next. Granting it allows Ori to list immediate-child metadata and create or reuse Filed inside that folder; it does not start monitoring.',
    monitoring_disclosure:
      'Monitoring is a separate decision. Ori waits five minutes and runs a daily catch-up at 09:00 local time.',
    file_review_disclosure:
      'Every move or send-to-Trash still requires your separate approval in File Janitor review.',
    default_schedule: '09:00',
    timezone: 'local time',
    no_automatic_tasks: true,
    no_file_actions: true
  },
  actions: [
    { id: 'accept', label: 'Set up for me', enabled: true },
    { id: 'defer_recommendation', label: 'Not now', enabled: true },
    {
      id: 'manual',
      label: 'Customize manually',
      enabled: true,
      route: '/workspaces/new?template=file-janitor'
    }
  ]
};

async function openMockedSetup(page: any) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.route('**/api/personal-assistant', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: 'needs_hq',
          assistant_id: 'assistant-a11y',
          display_name: 'Ari',
          state_version: 2
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/setup/file-janitor**', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ setup: proposal })
    })
  );
  await page.goto('/?quest=tidy-downloads');
  const card = page.locator('#assistantLedSetup');
  await expect(card).toBeVisible({ timeout: 15000 });
  return card;
}

test('setup review has labelled structure, live status, and keyboard-reachable decisions', async ({
  page
}) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const card = await openMockedSetup(page);
  await expect(card).toHaveAttribute('aria-labelledby', 'assistantLedSetupTitle');
  await expect(page.locator('#assistantLedSetupStatus')).toHaveAttribute('aria-live', 'polite');
  await expect(card.getByRole('heading', { name: 'File Janitor' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Set up for me' })).toBeEnabled();
  await expect(card.getByRole('button', { name: 'Not now' })).toBeEnabled();
  await expect(card.getByRole('link', { name: 'Customize manually' })).toHaveAttribute(
    'href',
    '/workspaces/new?template=file-janitor'
  );

  await card.getByRole('button', { name: 'Set up for me' }).focus();
  await expect(card.getByRole('button', { name: 'Set up for me' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(card.getByRole('button', { name: 'Not now' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(card.getByRole('link', { name: 'Customize manually' })).toBeFocused();
});

// A partial failure: the workspace receipt exists, the File Curator profile
// could not be proven attached. The server answers the accept with the safe
// code and this stopped projection.
const stoppedAtRole = {
  ...proposal,
  view_state: 'needs_attention',
  status_message:
    'Ori saved the setup claim but cannot prove the workspace result. Review it before retrying.',
  proposal: undefined,
  run: {
    id: 'run-a11y',
    revision: 3,
    lifecycle: 'reconcile_required',
    current_step: 'workspace',
    target_mode: 'create',
    target_workspace_id: 'workspace-a11y',
    team_role: { role_id: 'file-curator', name: 'File Curator', action: 'create' }
  },
  milestones: [
    {
      id: 'workspace',
      kind: 'workspace',
      name: 'File Janitor',
      action: 'create',
      status: 'created',
      blueprint_id: 'file-janitor',
      blueprint_version: 2,
      placement: 'workspace_directory',
      resource_id: 'workspace-a11y',
      ownership: 'created',
      recorded_at: '2026-09-21T10:00:00Z'
    },
    {
      id: 'role:file-curator',
      kind: 'agent_role',
      name: 'File Curator',
      role_id: 'file-curator',
      action: 'create',
      status: 'needs_review',
      blueprint_id: 'file-janitor',
      blueprint_version: 2,
      error_code: 'agent_not_attached'
    }
  ],
  actions: [
    { id: 'manual', label: 'Review manually', enabled: true, route: '/workspaces/workspace-a11y' }
  ]
};

async function stopAcceptAtRole(page: any) {
  await page.route('**/api/personal-assistant/setup/file-janitor/accept', route =>
    route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({
        error:
          'Ori cannot safely prove the last setup result. Review the saved workspace before retrying.',
        code: 'reconcile_required',
        setup: stoppedAtRole
      })
    })
  );
}

test('a stopped setup walkthrough stops at the failed step and keeps earlier receipts', async ({
  page
}, testInfo) => {
  const card = await openMockedSetup(page);
  await stopAcceptAtRole(page);
  const writes: string[] = [];
  page.on('request', outgoing => {
    if (outgoing.method() !== 'GET') writes.push(new URL(outgoing.url()).pathname);
  });

  await card.getByRole('button', { name: 'Set up for me' }).click();
  const dialog = page.locator('#assistantLedSetupDialog');
  await expect(dialog).toBeVisible();
  await expect(page.locator('#assistantLedSetupDialogMode')).toHaveText(
    'Stopped — needs your attention'
  );
  // The timer walks to the stopped step and stops there; it never runs past it.
  await expect(page.locator('#assistantLedSetupDialogStep')).toHaveText('Step 2 of 3', {
    timeout: 5000
  });
  const agentPanel = dialog.locator('[data-panel-id="agents"]');
  await expect(agentPanel).toBeVisible();
  await expect(agentPanel).toContainText('Needs review');
  await expect(agentPanel).toContainText(
    'The File Curator profile exists but is not attached to the workspace.'
  );
  await expect(agentPanel).not.toContainText('Created');
  await expect(page.locator('#assistantLedSetupDialogNext')).toBeHidden();
  await expect(page.locator('#assistantLedSetupDialogContinue')).toBeEnabled();
  await page.waitForTimeout(2500);
  await expect(page.locator('#assistantLedSetupDialogStep')).toHaveText('Step 2 of 3');
  await page.screenshot({ path: testInfo.outputPath('stopped-walkthrough-agent.png') });

  // The earlier receipt is still there.
  await page.locator('#assistantLedSetupDialogBack').click();
  const workspacePanel = dialog.locator('[data-panel-id="workspace"]');
  await expect(workspacePanel).toContainText('Created');
  await expect(workspacePanel).toContainText('workspace-a11y');
  await page.screenshot({ path: testInfo.outputPath('stopped-walkthrough-workspace.png') });

  // Continue hands focus to the card's server-projected action and sends nothing.
  await page.locator('#assistantLedSetupDialogNext').click();
  await page.locator('#assistantLedSetupDialogContinue').click();
  await expect(dialog).toBeHidden();
  await expect(card.getByRole('link', { name: 'Review manually' })).toBeFocused();
  await expect(card.locator('[data-milestone-id="role:file-curator"]')).toHaveAttribute(
    'data-status',
    'needs_review'
  );
  expect(writes).toEqual(['/api/personal-assistant/setup/file-janitor/accept']);
});

test('under reduced motion the stopped walkthrough shows every reachable step at once', async ({
  page
}) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const card = await openMockedSetup(page);
  await stopAcceptAtRole(page);
  await card.getByRole('button', { name: 'Set up for me' }).click();
  const dialog = page.locator('#assistantLedSetupDialog');
  await expect(page.locator('#assistantLedSetupDialogStep')).toHaveText('2 of 3 steps available');
  await expect(dialog.locator('[data-panel-id="workspace"]')).toBeVisible();
  await expect(dialog.locator('[data-panel-id="agents"]')).toBeVisible();
  await expect(dialog.locator('[data-panel-id="receipt"]')).toBeHidden();
  await expect(page.locator('#assistantLedSetupDialogNext')).toBeHidden();
  await expect(page.locator('#assistantLedSetupDialogContinue')).toBeVisible();
});

test('the card remains usable at narrow width without page-level horizontal scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const card = await openMockedSetup(page);
  await expect(card).toContainText('does not read file contents');
  await expect(card).toContainText('separate approval');
  const widths = await page.evaluate(() => ({
    viewport: window.innerWidth,
    page: document.documentElement.scrollWidth
  }));
  expect(widths.page).toBeLessThanOrEqual(widths.viewport + 1);
});
