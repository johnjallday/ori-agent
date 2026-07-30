import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

/**
 * Shared blueprint Setup Wizard — the journey only a browser proves.
 *
 * Run with:
 *   PLAYWRIGHT_BASE_URL=http://localhost:8931 npx playwright test tests/blueprint-setup-wizards.spec.ts
 *
 * against an isolated smoke server (`wt demo 8931`, or the HOME/ORI_DATA_DIR
 * recipe in CLAUDE.md). The spec writes a blueprint into the server's own
 * templates library, so it must run on the same machine as the server and
 * never against a real install.
 *
 * Why a hand-written blueprint: version 1 ships no visual wizard authoring, so
 * a `setup_wizard` block can only reach the server as manifest bytes. Writing
 * one is also what keeps this suite honest about scope — it exercises the
 * shared shell (auto-open, resume, dismissal, completion, status), not any one
 * blueprint's domain steps, which each migration covers in its own spec.
 *
 * What server-side tests cannot see, and this can: the dialog is loaded as a
 * classic `defer` script and mounts a <dialog> that starts closed. A wizard
 * that never opens, or opens on every reload, looks identical to a passing API
 * from the outside.
 */

const TEMPLATE_ID = 'blueprint-wizard-shell-demo';

const MANIFEST = {
  name: 'Wizard Shell Demo',
  // A built-in, like every blueprint this feature migrates: it puts the card in
  // the picker's built-in grid, which is the path the preview really takes.
  builtin: true,
  builtin_version: 1,
  description: 'A blueprint used by the Setup Wizard browser journey.',
  icon: '🧭',
  tagline: 'Shell coverage for the shared Setup Wizard.',
  behavior_profile: 'general',
  tags: ['demo'],
  directory_requirements: [
    {
      key: 'inbox-root',
      label: 'Inbox folder',
      suggested_path: '~/Downloads',
      access_disclosure:
        'Ori lists the files directly inside this folder and reads their names, types, sizes, and dates.'
    }
  ],
  automation_recipes: [
    {
      directory_key: 'inbox-root',
      watch: {
        events: ['create', 'rename'],
        debounce_seconds: 300,
        exclude_subdirectories: ['Filed']
      },
      daily_scan: { local_time: '09:00' }
    }
  ],
  agents: [
    {
      name: 'Wizard Shell Manager',
      role: 'orchestrator',
      system_prompt: 'You help with this workspace.'
    }
  ],
  starter_tasks: [
    {
      description: 'Set up this workspace',
      details: 'Optional help; the wizard owns setup.',
      setup: true
    },
    { description: 'Do the actual work' }
  ],
  setup_wizard: {
    version: 1,
    title: 'Set up Wizard Shell Demo',
    steps: [
      {
        id: 'folder',
        kind: 'directory',
        requirement_key: 'inbox-root',
        required: true,
        title: 'Choose the folder to tidy',
        description: 'Pick one folder. Nothing outside it is ever read or changed.'
      },
      {
        id: 'automation',
        kind: 'automation_review',
        requirement_key: 'inbox-root',
        required: true,
        title: 'Review what runs after setup',
        description: 'A folder watcher and a daily catch-up scan.',
        disclosure:
          'Ori watches for files created or renamed directly in this folder, waits five minutes, and skips the Filed folder. It also runs one catch-up scan a day at 09:00 in your timezone.'
      },
      {
        id: 'extras',
        kind: 'summary',
        required: false,
        title: 'Optional extras',
        description: 'You can skip this and come back later.'
      },
      {
        id: 'summary',
        kind: 'summary',
        required: true,
        title: 'You are set up',
        description: 'Here is what this workspace can do now.'
      }
    ]
  }
};

let templateDir = '';

async function installBlueprint(request: APIRequestContext) {
  const res = await request.get('/api/project-templates');
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  const root = (body.templates_root || body.data?.templates_root) as string;
  expect(root, 'server did not report its templates root').toBeTruthy();
  templateDir = join(root, TEMPLATE_ID);
  mkdirSync(templateDir, { recursive: true });
  writeFileSync(join(templateDir, 'template.json'), JSON.stringify(MANIFEST, null, 2));
}

async function createWorkspace(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post('/api/workspaces', {
    data: { name, description: '', template_id: TEMPLATE_ID, create_template_agents: true }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return (body.folder?.id || body.workspace?.id) as string;
}

function dialog(page: Page) {
  return page.locator('#setupWizardDialog');
}

async function setupState(request: APIRequestContext, workspaceId: string) {
  const res = await request.get(`/api/workspaces/${workspaceId}/setup-wizard`);
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  return body.setup || body.data?.setup;
}

test.describe.configure({ mode: 'serial' });

test.describe('Blueprint Setup Wizard', () => {
  test.beforeAll(async ({ request }) => {
    await request.post('/api/onboarding/skip').catch(() => {});
    await installBlueprint(request);
  });

  test.afterAll(() => {
    if (templateDir) rmSync(templateDir, { recursive: true, force: true });
  });

  test.beforeEach(async ({ page }) => {
    await page.request.post('/api/onboarding/skip').catch(() => {});
  });

  test('creation review previews the blueprint’s setup without doing any of it', async ({
    page,
    request
  }) => {
    await page.goto('/workspaces');
    // The create launcher lives in a toolbar the Map view can collapse; click it
    // through the DOM so this test is about the preview, not about chrome.
    await page.evaluate(() => document.getElementById('launcherCreateWorkspaceBtn')?.click());
    await expect(page.locator('#addFolderModal')).toBeVisible({ timeout: 15000 });
    const card = page.locator(`[data-template-id="${TEMPLATE_ID}"]`).first();
    await expect(card).toBeVisible({ timeout: 15000 });
    await card.click();

    // The preview lives in the review step, where the user decides to create.
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(`Preview Only ${Date.now().toString(36)}`);
    await page.locator('#wizardNextBtn').click();

    const preview = page.locator('#workspaceSetupPreview');
    await expect(preview).toBeVisible({ timeout: 15000 });
    await expect(preview).toContainText('Setup continues after you create the workspace');
    await expect(preview).toContainText('Choose the folder to tidy');
    await expect(preview).toContainText('Optional');

    // Disclosure only: previewing must not have created anything.
    const list = await request.get('/api/workspaces');
    const body = await list.json();
    const workspaces = body.workspaces || body.folders || body.data?.workspaces || [];
    expect(
      workspaces.some((ws: { name?: string }) => ws?.name === 'Wizard Shell Demo'),
      'previewing a blueprint must not create a workspace'
    ).toBeFalsy();
  });

  test('auto-opens once, resumes after dismissal, and completes', async ({ page, request }) => {
    const workspaceId = await createWorkspace(request, `Wizard Journey ${Date.now().toString(36)}`);

    // A created workspace is visibly unfinished before anything is opened.
    expect((await setupState(request, workspaceId)).state).toBe('not_started');

    // ---- first open: the dialog opens itself, at the first required step ----
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardTitle')).toHaveText('Set up Wizard Shell Demo');
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');
    await expect(page.locator('#setupWizardSteps')).toContainText(
      '1. Choose the folder to tidy (current)'
    );

    // ---- dismissal: recorded, and it does not make the workspace ready ----
    await page.locator('#setupWizardClose').click();
    await expect(dialog(page)).toBeHidden();
    const dismissed = await setupState(request, workspaceId);
    expect(dismissed.dismissed).toBe(true);
    expect(dismissed.state).not.toBe('ready');

    // ---- reload: a dismissed wizard does not ambush the user again ----
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(page.locator('#setupWizardBanner')).toBeVisible({ timeout: 15000 });
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Setup required');
    await expect(dialog(page)).toBeHidden();

    // ---- explicit resume from the persistent banner ----
    await page.locator('#setupWizardBannerAction').click();
    await expect(dialog(page)).toBeVisible();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Choose the folder to tidy');

    // ---- required steps: each one advances only after the server confirms ----
    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review what runs after setup', {
      timeout: 15000
    });
    // The disclosure is shown before the action that commits it.
    await expect(page.locator('#setupWizardDisclosure')).toBeVisible();
    await expect(page.locator('#setupWizardDisclosureBody')).toContainText(
      'skips the Filed folder'
    );
    await page.locator('#setupWizardPrimary').click();

    // ---- an optional step can be skipped and stays visible ----
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Optional extras', {
      timeout: 15000
    });
    await page.locator('#setupWizardSkip').click();
    await expect(page.locator('#setupWizardSteps')).toContainText('3. Optional extras (skipped)');

    // ---- finish: confirming the last step completes the wizard ----
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('You are set up', {
      timeout: 15000
    });
    await page.locator('#setupWizardPrimary').click();

    await expect(dialog(page)).toBeHidden({ timeout: 15000 });
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Ready');

    const ready = await setupState(request, workspaceId);
    expect(ready.state).toBe('ready');
    expect(ready.completed_at).toBeTruthy();

    // ---- a completed wizard does not reopen on the next visit ----
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(page.locator('#setupWizardBannerState')).toHaveText('Ready', { timeout: 15000 });
    await expect(dialog(page)).toBeHidden();

    // ---- the blueprint's setup help task was completed without an agent run ----
    const tasksRes = await request.get(`/api/workspaces/${workspaceId}`);
    const tasksBody = await tasksRes.json();
    const tasks = (tasksBody.folder || tasksBody.workspace || tasksBody).tasks || [];
    const setupTask = tasks.find((task: { description?: string }) =>
      String(task?.description || '').startsWith('Set up this workspace')
    );
    expect(setupTask, 'the blueprint seeds a setup help task').toBeTruthy();
    expect(setupTask.status).toBe('completed');
    expect(String(setupTask.result || '')).toContain('Setup Wizard');
    // The other starter task is untouched: setup being ready says nothing about
    // the actual work.
    const workTask = tasks.find((task: { description?: string }) =>
      String(task?.description || '').startsWith('Do the actual work')
    );
    expect(workTask?.status).toBe('pending');
  });

  test('a workspace with no wizard shows no setup surface at all', async ({ page, request }) => {
    const res = await request.post('/api/workspaces', {
      data: { name: `Plain ${Date.now().toString(36)}`, description: '' }
    });
    expect(res.ok(), await res.text()).toBeTruthy();
    const body = await res.json();
    const workspaceId = (body.folder?.id || body.workspace?.id) as string;

    await page.goto(`/workspaces/${workspaceId}`);
    // The dialog and banner are in the page either way; a workspace with no
    // wizard must leave both dormant rather than showing an empty setup surface.
    await expect(dialog(page)).toBeAttached({ timeout: 15000 });
    await expect(dialog(page)).toBeHidden();
    await expect(page.locator('#setupWizardBanner')).toBeHidden();
  });

  // ---- accessibility (FR140) ----
  //
  // The dialog is a native <dialog> precisely so these hold without being
  // hand-rolled, but "we used the right element" is a claim about the code, not
  // about the page. These drive it the way someone using a keyboard and a
  // screen reader would.

  test('the whole wizard is completable with the keyboard alone', async ({ page, request }) => {
    const workspaceId = await createWorkspace(
      request,
      `Wizard Keyboard ${Date.now().toString(36)}`
    );
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });

    // Focus lands inside the dialog, not behind it.
    const focusedInsideDialog = () =>
      page.evaluate(() => {
        const active = document.activeElement;
        const dialogEl = document.getElementById('setupWizardDialog');
        return !!(active && dialogEl && dialogEl.contains(active));
      });
    expect(await focusedInsideDialog()).toBe(true);

    // Tab must never reach a control behind the dialog. Chromium parks focus on
    // <body> when it wraps around a modal's tab ring, which is not an escape —
    // what would be one is landing on a button, link, or field on the page.
    const focusedBehindDialog = () =>
      page.evaluate(() => {
        const active = document.activeElement as HTMLElement | null;
        const dialogEl = document.getElementById('setupWizardDialog');
        if (!active || !dialogEl || dialogEl.contains(active)) return '';
        if (active === document.body || active === document.documentElement) return '';
        return active.tagName + '#' + (active.id || '') + '.' + (active.className || '');
      });
    for (let i = 0; i < 12; i++) {
      await page.keyboard.press('Tab');
      expect(await focusedBehindDialog(), `focus escaped after ${i + 1} tabs`).toBe('');
    }

    // Every step is reachable and confirmable from the keyboard.
    for (let step = 0; step < 6; step++) {
      if (!(await dialog(page).isVisible())) break;
      await page.locator('#setupWizardPrimary').focus();
      await page.keyboard.press('Enter');
      await page.waitForTimeout(700);
    }
    await expect(dialog(page)).toBeHidden({ timeout: 15000 });
    expect((await setupState(request, workspaceId)).state).toBe('ready');
  });

  test('Escape dismisses, and focus returns to what opened the dialog', async ({
    page,
    request
  }) => {
    const workspaceId = await createWorkspace(request, `Wizard Escape ${Date.now().toString(36)}`);
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });

    await page.keyboard.press('Escape');
    await expect(dialog(page)).toBeHidden();
    // Dismissal is recorded server-side, and it does not make the workspace
    // ready — closing a dialog is not finishing setup.
    const dismissed = await setupState(request, workspaceId);
    expect(dismissed.dismissed).toBe(true);
    expect(dismissed.state).not.toBe('ready');

    // Reopening from the banner and closing again returns focus to the control
    // the user pressed, instead of dropping them at the top of the page.
    await page.locator('#setupWizardBannerAction').click();
    await expect(dialog(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog(page)).toBeHidden();
    const focusedId = await page.evaluate(() => document.activeElement?.id || '');
    expect(focusedId).toBe('setupWizardBannerAction');
  });

  test('state is announced and never carried by color alone', async ({ page, request }) => {
    const workspaceId = await createWorkspace(request, `Wizard A11y ${Date.now().toString(36)}`);
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });

    // The step list carries a word per step, not a colored dot.
    await expect(page.locator('#setupWizardSteps')).toContainText('(current)');
    await expect(page.locator('#setupWizardSteps')).toContainText('(not started)');

    // Where the user is is announced, not left to a changed heading nobody
    // hears — and it is announced without being drawn next to the heading that
    // already says it.
    const stepLive = page.locator('#setupWizardStepLive');
    await expect(stepLive).toHaveAttribute('aria-live', 'polite');
    await expect(stepLive).toHaveAttribute('role', 'status');
    await expect(stepLive).toContainText(/Step 1 of \d+/, { timeout: 15000 });
    // Present for assistive technology, not painted: an sr-only region keeps a
    // 1px box (removing it from the layout would also remove it from some
    // screen readers), so the check is on its size rather than visibility.
    const box = await stepLive.boundingBox();
    expect(
      box && box.width <= 1 && box.height <= 1,
      `step announcer is drawn: ${JSON.stringify(box)}`
    ).toBe(true);
    await page.locator('#setupWizardPrimary').click();
    await expect(stepLive).toContainText(/Step 2 of \d+/, { timeout: 15000 });

    // And the footer's progress line stays free for transient feedback rather
    // than permanently repeating the step name.
    await expect(page.locator('#setupWizardLive')).toBeEmpty();

    // The dialog names itself for assistive technology.
    await expect(dialog(page)).toHaveAttribute('aria-labelledby', 'setupWizardTitle');
    await expect(page.locator('#setupWizardClose')).toHaveAttribute('aria-label', /close/i);
  });

  test('on a small screen the dialog scrolls instead of stranding its buttons', async ({
    page,
    request
  }) => {
    const workspaceId = await createWorkspace(request, `Wizard Narrow ${Date.now().toString(36)}`);
    await page.setViewportSize({ width: 390, height: 620 });
    await page.goto(`/workspaces/${workspaceId}`);
    await expect(dialog(page)).toBeVisible({ timeout: 15000 });

    // The primary action is reachable without leaving the viewport — the failure
    // that makes a mobile dialog unusable rather than merely cramped is a
    // Continue button pushed off the bottom.
    const primary = page.locator('#setupWizardPrimary');
    await primary.scrollIntoViewIfNeeded();
    await expect(primary).toBeInViewport();

    // The dialog itself never scrolls sideways: long author text wraps and the
    // body scrolls vertically instead. (The workspace page behind it does
    // overflow horizontally at this width — that predates this feature and
    // redesigning that page is out of scope here.)
    const dialogOverflow = await page.evaluate(() => {
      const el = document.getElementById('setupWizardDialog');
      if (!el) return null;
      const body = el.querySelector('.setup-wizard-body');
      return {
        sideways: el.scrollWidth > el.clientWidth + 1,
        fitsViewport: el.getBoundingClientRect().width <= window.innerWidth + 1,
        bodyScrollsVertically: !!body && body.scrollHeight >= body.clientHeight
      };
    });
    expect(dialogOverflow?.sideways, 'the dialog scrolls sideways').toBe(false);
    expect(dialogOverflow?.fitsViewport, 'the dialog is wider than the screen').toBe(true);
    expect(dialogOverflow?.bodyScrollsVertically).toBe(true);
  });
});
