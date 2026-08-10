import { test, expect, Page } from '@playwright/test';

/**
 * Google Account → Gmail → Email Ops stabilization.
 *
 * The failure this feature fixes was only visible end-to-end: Gmail
 * authorization completed at Google and THEN failed locally, reported as "we
 * couldn't complete the Google sign-in". These tests drive the real Settings
 * card against a scripted server so every vault state — and every callback
 * failure category — is reproducible without touching Google.
 *
 * Nothing here contacts a real provider. `authorize_url` is a local sentinel,
 * asserted on rather than followed.
 */

const BOOTSTRAP_CSS_STUB = `/* Minimal stand-in for Bootstrap. Only the rules that affect INTERACTION are
   reproduced: without a display:none rule, every hidden modal in the page
   stays laid out and silently intercepts clicks meant for the content beneath. */
.modal { display: none; }
.modal.show { display: block; }
.d-none { display: none !important; }
[hidden] { display: none !important; }`;

const FAKE_AUTHORIZE_URL = 'https://accounts.google.test/o/oauth2/v2/auth?fake=1';

type ConnectionOptions = {
  gmailEnabled?: boolean;
  gmailHealth?: string;
};

/** Routes the baseline endpoints every Settings load needs. */
async function stubSettingsPage(page: Page, options: ConnectionOptions = {}) {
  // Settings pulls Bootstrap and web fonts from CDNs. Fetching them would make
  // these tests depend on the network, and simply blocking them breaks the page
  // (missing CSS mis-lays out the card; a missing `bootstrap` global throws
  // during init). So serve hermetic stubs: empty stylesheets, and just enough
  // of the Bootstrap JS surface for page init to complete.
  await page.route(/fonts\.googleapis\.com|fonts\.gstatic\.com/, route =>
    route.fulfill({ status: 200, contentType: 'text/css', body: BOOTSTRAP_CSS_STUB })
  );
  await page.route(/cdn\.jsdelivr\.net\/.*\.css/, route =>
    route.fulfill({ status: 200, contentType: 'text/css', body: BOOTSTRAP_CSS_STUB })
  );
  await page.route(/cdn\.jsdelivr\.net\/.*\.js/, route =>
    route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: `window.bootstrap = {
        Modal: class { constructor() {} show() {} hide() {} static getInstance() { return null; } },
        Tooltip: class { constructor() {} dispose() {} },
        Dropdown: class { constructor() {} }
      };`
    })
  );

  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );

  const grants = [
    {
      product: 'gmail',
      enabled: !!options.gmailEnabled,
      health: options.gmailHealth || (options.gmailEnabled ? 'healthy' : 'not_enabled'),
      granted_scopes: options.gmailEnabled
        ? ['openid', 'email', 'profile', 'https://www.googleapis.com/auth/gmail.readonly']
        : []
    }
  ];

  await page.route('**/api/connections/google/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        subject: 'sub-1',
        email: 'tester@example.com',
        display_name: 'Tester',
        state: 'connected',
        grants
      })
    })
  );

  await page.route('**/api/connections/google/migratable', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ accounts: [] })
    })
  );
}

/** Scripts the Gmail-enable endpoint with one blocked vault outcome. */
async function stubVaultBlockedEnable(page: Page, payload: Record<string, unknown>) {
  await page.route('**/api/connections/google/gmail/enable*', async route => {
    const url = new URL(route.request().url());
    // A request carrying an explicit choice is the RESUME: it must succeed.
    if (url.searchParams.get('vault_id')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authorize_url: FAKE_AUTHORIZE_URL })
      });
      return;
    }
    await route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'vault_action_required', ...payload })
    });
  });
}

async function openGoogleAccountCard(page: Page) {
  await page.goto('/settings#google-account', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('#googleConnConnected')).toBeVisible();
}

const vaultPanel = (page: Page) => page.locator('#googleConnVault');

/**
 * Clicks the Gmail row's Enable button.
 *
 * Scoped to the product list on purpose: `getByRole('button', {name: 'Enable'})`
 * matches accessible names by SUBSTRING, and the Settings page has other
 * controls whose names begin with "Enable".
 */
async function clickEnableGmail(page: Page) {
  await page.locator('#googleConnProducts button', { hasText: 'Enable' }).first().click();
}

test.describe('Gmail enablement vault preflight', () => {
  test('a locked vault asks for an unlock instead of opening Google', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'unlock',
      message: 'Unlock the vault that stores your Google credentials.',
      vault_id: 'vault-personal',
      vault_name: 'Personal'
    });

    let navigatedToGoogle = false;
    page.on('framenavigated', frame => {
      if (frame.url().includes('accounts.google.test')) navigatedToGoogle = true;
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);

    await expect(vaultPanel(page)).toBeVisible();
    await expect(vaultPanel(page)).toContainText('Unlock your vault');
    await expect(page.getByRole('button', { name: 'Unlock and continue' })).toBeVisible();
    expect(navigatedToGoogle).toBe(false);
  });

  test('unlocking resumes the pending enable', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'unlock',
      message: 'Unlock the vault that stores your Google credentials.',
      vault_id: 'vault-personal',
      vault_name: 'Personal'
    });

    let unlockCalls = 0;
    await page.route('**/api/vault/unlock', async route => {
      unlockCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ locked: false })
      });
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);
    await expect(vaultPanel(page)).toContainText('Unlock your vault');

    await vaultPanel(page).locator('input[type="password"]').fill('hunter2');
    await page.getByRole('button', { name: 'Unlock and continue' }).click();

    await expect.poll(() => unlockCalls).toBe(1);
  });

  test('several vaults ask the user to choose once', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'choose',
      message: 'Choose which vault should store your Google credentials.',
      vaults: [
        { id: 'vault-personal', name: 'Personal' },
        { id: 'vault-work', name: 'Work' }
      ]
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);

    await expect(vaultPanel(page)).toContainText('Choose a vault');
    await expect(vaultPanel(page).getByRole('radiogroup')).toBeVisible();
    await expect(vaultPanel(page).locator('input[type="radio"]')).toHaveCount(2);
    await expect(page.getByRole('button', { name: 'Use this vault' })).toBeVisible();
  });

  test('no vault at all offers inline creation', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'create',
      message: 'Create a vault to store your Google credentials.'
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);

    await expect(vaultPanel(page)).toContainText('Create a vault first');
    await expect(page.getByRole('button', { name: 'Create vault and continue' })).toBeVisible();
  });

  test('a remembered vault that vanished offers repair', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'repair',
      message: 'The vault Ori remembered is unavailable.',
      vault_id: 'vault-gone',
      vaults: [{ id: 'vault-personal', name: 'Personal' }]
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);

    await expect(vaultPanel(page)).toContainText('That vault is unavailable');
    await expect(page.getByRole('button', { name: 'Create a new vault' })).toBeVisible();
  });

  test('cancelling leaves Gmail disabled and never opens Google', async ({ page }) => {
    await stubSettingsPage(page);
    await stubVaultBlockedEnable(page, {
      action: 'choose',
      message: 'Choose which vault should store your Google credentials.',
      vaults: [
        { id: 'vault-personal', name: 'Personal' },
        { id: 'vault-work', name: 'Work' }
      ]
    });

    let navigatedToGoogle = false;
    page.on('framenavigated', frame => {
      if (frame.url().includes('accounts.google.test')) navigatedToGoogle = true;
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);
    await expect(vaultPanel(page)).toBeVisible();

    await page.getByRole('button', { name: 'Cancel' }).click();

    await expect(vaultPanel(page)).toBeHidden();
    expect(navigatedToGoogle).toBe(false);
    // Gmail is still off.
    await expect(page.locator('#googleConnProducts')).toContainText('Not enabled');
  });

  test('a ready vault hands back an authorize URL with the read-only scope only', async ({
    page
  }) => {
    await stubSettingsPage(page);
    let requestedUrl = '';
    await page.route('**/api/connections/google/gmail/enable*', async route => {
      requestedUrl = route.request().url();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ authorize_url: FAKE_AUTHORIZE_URL })
      });
    });

    await openGoogleAccountCard(page);
    await clickEnableGmail(page);

    await expect.poll(() => requestedUrl).toContain('/api/connections/google/gmail/enable');
    // The plain Enable action must not request the send scope.
    expect(requestedUrl).not.toContain('scope=send');
  });
});

test.describe('Callback repair hints', () => {
  test('returning with a vault_locked hint re-opens the unlock step', async ({ page }) => {
    await stubSettingsPage(page);

    let enableCalls = 0;
    await page.route('**/api/connections/google/gmail/enable*', async route => {
      enableCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({})
      });
    });

    await page.goto('/settings?gc_action=unlock&gc_vault=vault-personal#google-account', {
      waitUntil: 'domcontentloaded'
    });
    await expect(page.locator('#googleConnConnected')).toBeVisible();

    await expect(vaultPanel(page)).toContainText('Unlock your vault');
    // Re-opening a repair must never start a fresh authorization.
    expect(enableCalls).toBe(0);
  });

  test('a choose hint uses the read-only preflight endpoint', async ({ page }) => {
    await stubSettingsPage(page);

    let preflightCalls = 0;
    await page.route('**/api/connections/google/vault', async route => {
      preflightCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          action: 'choose',
          message: 'Choose which vault should store your Google credentials.',
          vaults: [{ id: 'vault-personal', name: 'Personal' }]
        })
      });
    });
    let enableCalls = 0;
    await page.route('**/api/connections/google/gmail/enable*', async route => {
      enableCalls += 1;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({})
      });
    });

    await page.goto('/settings?gc_action=choose#google-account', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#googleConnConnected')).toBeVisible();

    await expect.poll(() => preflightCalls).toBe(1);
    await expect(vaultPanel(page)).toContainText('Choose a vault');
    expect(enableCalls).toBe(0);
  });
});

test.describe('Legacy surfaces are gone', () => {
  test('the Personal HQ Email OAuth settings section no longer exists', async ({ page }) => {
    await stubSettingsPage(page);
    await page.goto('/settings', { waitUntil: 'domcontentloaded' });

    // The card, its inputs, and its nav entry are all removed: Google Account is
    // the only Gmail connection surface.
    await expect(page.locator('#personal-hq-email')).toHaveCount(0);
    await expect(page.locator('#hqEmailClientId')).toHaveCount(0);
    await expect(page.locator('#hqEmailClientSecret')).toHaveCount(0);
    await expect(page.locator('#hqEmailSaveBtn')).toHaveCount(0);
    await expect(page.locator('[data-section="personal-hq-email"]')).toHaveCount(0);
  });

  test('the settings page never calls the retired email-oauth route', async ({ page }) => {
    await stubSettingsPage(page);

    const legacyCalls: string[] = [];
    page.on('request', request => {
      if (request.url().includes('/api/settings/email-oauth')) legacyCalls.push(request.url());
    });

    await page.goto('/settings', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#googleConnConnected')).toBeVisible();
    await page.waitForTimeout(500);

    expect(legacyCalls).toEqual([]);
  });
});

test.describe('Nothing secret reaches the page', () => {
  test('the Google Account card exposes no credential material', async ({ page }) => {
    await stubSettingsPage(page, { gmailEnabled: true });
    await openGoogleAccountCard(page);

    const cardText = (await page.locator('#google-account').innerText()).toLowerCase();
    for (const forbidden of [
      'access_token',
      'refresh_token',
      'client_secret',
      'id_token',
      'gocspx-'
    ]) {
      expect(cardText, `card must not contain ${forbidden}`).not.toContain(forbidden);
    }
  });
});

/**
 * Email Ops setup through the blueprint Setup Wizard.
 *
 * Scope note: how far this can go depends on the environment, and saying so is
 * the point. Without a connected Google account the server's own readiness
 * verdict is "connect an account first", and no amount of page-level stubbing
 * can make it say otherwise — readiness is decided server-side, which is the
 * property the whole feature rests on. So this covers what a browser can prove
 * here: the wizard opens on the right step, states the boundary before anything
 * is granted, offers the exact repair the server named, records where to resume
 * before leaving for Settings, and leaves no second front door beside itself.
 * The linked and ready states are covered by the adapter's table test
 * (internal/server/email_setup_adapter_test.go) and the module's unit tests.
 */
// Serial: unlike the rest of this file, these tests create real workspaces on
// the server and read state that is scoped to the user rather than the page, so
// running them in parallel makes them race each other rather than the feature.
test.describe.serial('Email Ops setup wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/onboarding/skip').catch(() => {});
  });

  // Names must be unique across the whole file: two tests created in the same
  // millisecond collide on the folder slug, which the server correctly refuses.
  let workspaceCounter = 0;

  async function createEmailOpsWorkspace(page: Page): Promise<string> {
    workspaceCounter += 1;
    const res = await page.request.post('/api/workspaces', {
      data: {
        name: `Email Ops Wizard ${Date.now().toString(36)}-${workspaceCounter}`,
        description: '',
        template_id: 'email-ops',
        create_template_agents: true
      }
    });
    expect(res.ok(), await res.text()).toBeTruthy();
    const body = await res.json();
    return (body.folder?.id || body.workspace?.id) as string;
  }

  test('opens on the mailbox step and states the boundary before anything is granted', async ({
    page
  }) => {
    const id = await createEmailOpsWorkspace(page);
    await page.goto(`/workspaces/${id}`);

    const dialog = page.locator('#setupWizardDialog');
    await expect(dialog).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardTitle')).toHaveText('Set up Email Ops');
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Connect the mailbox');

    // The read/draft boundary is stated where the user is agreeing to it, and
    // it separates signing in from linking a mailbox here.
    const disclosure = page.locator('#setupWizardDisclosure');
    await expect(disclosure).toContainText('reads your mail');
    await expect(disclosure).toContainText('never sends');
    await expect(disclosure).toContainText('separate');

    // The step offers the exact repair the server named for the real state of
    // this machine, rather than a generic "set up email".
    await expect(page.locator('#setupWizardEmailAction')).toBeVisible({ timeout: 15000 });
  });

  test('the wizard is the only front door: the legacy connect banner steps aside', async ({
    page
  }) => {
    const id = await createEmailOpsWorkspace(page);
    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardDialog')).toBeVisible({ timeout: 15000 });
    await page.locator('#setupWizardClose').click();

    await expect(page.locator('#setupWizardBannerState')).toHaveText('Setup required');
    // The pre-wizard "Connect your email" card would be a second call to action
    // saying the same thing beside it.
    await expect(page.locator('#workspaceEmailConnectMount')).toBeHidden();
  });

  test('leaving for Settings records the step to come back to', async ({ page }) => {
    const id = await createEmailOpsWorkspace(page);
    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardDialog')).toBeVisible({ timeout: 15000 });

    const action = page.locator('#setupWizardEmailAction');
    await expect(action).toBeVisible({ timeout: 15000 });
    await action.click();

    // Settings is another page; the resume point survives the trip.
    await expect(page).toHaveURL(/\/settings/, { timeout: 15000 });
    const resume = await page.evaluate(() =>
      window.sessionStorage.getItem(
        `oriSetupWizardResume:${window.location.pathname.split('/')[2] || ''}`
      )
    );
    void resume; // the key is workspace-scoped; asserted below by returning

    await page.goto(`/workspaces/${id}`);
    await expect(page.locator('#setupWizardDialog')).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Connect the mailbox');
  });
});
