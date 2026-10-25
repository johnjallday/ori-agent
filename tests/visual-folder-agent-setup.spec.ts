import { test, expect, chromium, type Page, type Locator } from '@playwright/test';
import { chooseAttach } from './helpers/assistant-menus';
import { mockHiredAssistant } from './helpers/hired-assistant';
import { writeFile } from 'node:fs/promises';

// Deterministic presentation fixtures against the real application. These prove
// rendering/no unintended browser requests, not native selection or live setup.
const observation = {
  version: 1,
  id: 'visual-fixture',
  folder: 'Session',
  scanned_at: '2026-10-05T10:00:00Z',
  files: 1,
  entries: 1,
  kinds: [{ name: '.rpp', count: 1 }],
  projects: [{ id: 'candidate-0', root: true, name: 'Session', marker: '*.rpp', files: 1 }],
  coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: false }
};

async function preview(page: Page, value = observation) {
  const forbidden: string[] = [];
  const errors: string[] = [];
  await mockHiredAssistant(page);
  page.on('pageerror', error => errors.push(error.message));
  // No setup, model, install or file-read request is allowed by this fixture.
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (
      /\/home-assistant\/(ask|route)$|\/folder-context\/(review|inspect)|\/folder-digest\/offers\/.*\/(setup|decide)|\/files\//.test(
        path
      ) ||
      (request.method() !== 'GET' && /\/plugins\/.*(install|enable|update)/.test(path))
    ) {
      forbidden.push(`${request.method()} ${path}`);
      return route.abort('blockedbyclient');
    }
    await route.fallback();
  });
  await page.route('**/api/home-assistant/folder-context/choices', route =>
    route.fulfill({
      json: { chips: [{ id: 'desktop', label: 'Desktop' }], picker_available: false }
    })
  );
  await page.route('**/api/home-assistant/folder-context/select', route =>
    route.fulfill({ json: { observation: value, revision: '' } })
  );
  await page.goto('/?panel=today');
  await chooseAttach(page, '#personalAssistantFolderChip:enabled');
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Desktop', exact: true })
    .click();
  const card = page.locator('#personalAssistantFolderPreview');
  await expect(card).toBeVisible();
  return { card, forbidden, errors };
}

const reviewOffer = (mode = 'create') => ({
  id: 'visual-review-fixture',
  conversation_id: 'visual-conversation-fixture',
  verdict: 'project',
  status: 'pending',
  folder: 'Session',
  subject: { name: 'Session', is_root: true, marker: 'REAPER project', observed_files: 1 },
  scanned_at: '2026-10-05T10:00:00Z',
  partial: false,
  reason: 'A REAPER project marker was observed in this folder.',
  capability: {
    recognized: 'REAPER song',
    workspace: 'REAPER Song workspace',
    evidence: 'Session.rpp marker'
  },
  plan: {
    digest: 'fixture-reviewed-digest',
    brief_default: 'Understand the song before suggesting work.',
    visual: {
      entity: { kind: 'workspace', name: 'Session', operation: 'create' },
      plugins: [
        {
          kind: 'integration',
          name: 'REAPER',
          operation: 'update',
          installed_version: 'fixture-old',
          version: 'fixture-new'
        }
      ]
    },
    destination: { status: 'existing', name: 'Music Home', kind: 'plugin' },
    hire: {
      mode,
      agent_name: mode === 'pending' ? '' : 'Ari',
      role_label: 'Producer',
      model: 'Fixture model'
    },
    lines: [
      {
        kind: 'integration',
        name: 'Update the reviewed REAPER integration',
        detail: 'Reviewed publisher and release.'
      },
      { kind: 'workspace', name: 'Creates a Session workspace' },
      { kind: 'folder', name: 'Linked where it is; nothing is moved, copied or run' },
      {
        kind: 'agents',
        name: 'Adds the required agent',
        detail: 'The same agent is shared with later projects in this Home.'
      },
      { kind: 'task', name: 'One read-only first look', detail: 'Reads permitted project content.' }
    ]
  }
});

async function mountReview(page: Page, offer = reviewOffer(), route = '/?panel=today') {
  await mockHiredAssistant(page);
  await page.goto(route);
  await page.waitForFunction(() =>
    Boolean((window as any).PersonalAssistantFolder?.showConversationReview)
  );
  await page.evaluate(offer => {
    const slot = document.createElement('div');
    slot.id = 'visual-review-fixture-slot';
    document.getElementById('personalAssistantScroll')!.append(slot);
    (window as any).PersonalAssistantFolder.showConversationReview(slot, offer);
    (window as any).PersonalAssistantTranscript?.revealControl(slot, true);
  }, offer);
  return page.locator('#personalAssistantFolderOffer');
}

test.beforeEach(async ({ request }) => {
  await request.post('/api/onboarding/skip');
});

for (const theme of ['light', 'dark']) {
  test(`fixture inline package review preserves keyboard focus and newer typing ${theme}`, async ({
    page
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    const offer: any = reviewOffer();
    offer.prerequisites_available = true;
    const writes: any[] = [];
    let release: (() => void) | null = null;
    let hold = false;
    await page.route(
      '**/api/personal-assistant/folder-digest/offers/*/prerequisites',
      async route => {
        const body = route.request().postDataJSON();
        writes.push(body);
        if (hold)
          await new Promise<void>(resolve => {
            release = resolve;
          });
        await route.fulfill({
          json: {
            prerequisite: {
              kind: 'integration',
              operation: 'update',
              name: 'REAPER',
              version: 'fixture-reviewed',
              source: 'owner/repository',
              run_id: 'fixture-canonical-run',
              revision: 7,
              review_token: 'fixture-review-token',
              action: 'update',
              enabled_after: true,
              disclosure: {},
              details: { literal: '<img src=x onerror=alert(1)>' }
            }
          }
        });
      }
    );
    const card = await mountReview(page, offer);
    const input = page.locator('#personalAssistantInput');
    await input.fill('  Inline 🎼 draft\nkept exact  ');
    const update = card.getByRole('button', { name: 'Update REAPER', exact: true });
    await update.focus();
    await page.keyboard.press('Enter');
    const heading = card.getByRole('heading', { name: 'Update REAPER', exact: true });
    await expect(heading).toBeFocused();
    await keyboardReach(page, card.getByRole('button', { name: 'Confirm update', exact: true }));
    await page.keyboard.press('Escape');
    await expect(card).toBeVisible();
    await expect(update).toBeFocused();
    expect(writes).toHaveLength(1);
    expect(writes[0].confirm).toBeUndefined();
    hold = true;
    await update.click();
    await expect.poll(() => Boolean(release)).toBe(true);
    await input.fill('  Newer 界 draft\nnot stolen  ');
    release!();
    await expect(heading).toBeVisible();
    await expect(input).toBeFocused();
    await expect(input).toHaveValue('  Newer 界 draft\nnot stolen  ');
    await card.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(update).toBeFocused();
    expect(writes).toHaveLength(2);
    expect(writes.some(body => body.confirm)).toBe(false);
    expect(await card.locator('img[src="x"]').count()).toBe(0);
  });
}

test('fixture review preserves exact Unicode drafts and only submits the reviewed digest', async ({
  page
}) => {
  const requests: Record<string, unknown>[] = [];
  let stale = false;
  await page.route('**/folder-digest/offers/visual-review-fixture/setup', route => {
    requests.push(route.request().postDataJSON());
    return route.fulfill(
      stale
        ? {
            status: 409,
            json: {
              plan_changed: true,
              error: 'Review the changed plan.',
              offer: { ...reviewOffer(), plan: { ...reviewOffer().plan, digest: 'changed-digest' } }
            }
          }
        : { status: 503, json: { error: 'Fixture failure' } }
    );
  });
  const card = await mountReview(page);
  const brief = card.getByLabel('What should it do with it?');
  const confirm = card.getByRole('button', { name: 'Hire & start', exact: true });
  await expect(card.getByRole('heading', { name: 'Confirm setup', exact: true })).toBeVisible();
  await expect(card.getByText('Setup effects', { exact: true })).toBeVisible();
  await expect(card.locator('#personalAssistantFolderConsequences')).toContainText(
    'shares permitted folder content'
  );
  await expect(card.locator('#personalAssistantFolderConsequences')).toContainText(
    'shared with later projects'
  );
  await expect(card.locator('#personalAssistantFolderPlan')).toBeHidden();
  await card.getByText('Full setup details', { exact: true }).click();
  await expect(card.locator('#personalAssistantFolderPlan')).toBeVisible();
  await brief.fill('🎼'.repeat(301));
  await confirm.click();
  expect(requests).toHaveLength(0);
  await expect(brief).toHaveValue('🎼'.repeat(301));
  await brief.fill('🎼'.repeat(300));
  await confirm.click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0].brief).toBe('🎼'.repeat(300));
  await expect(brief).toHaveValue('🎼'.repeat(300));
  await brief.fill('  A precise\nbrief  ');
  stale = true;
  await confirm.click();
  await expect(card.getByRole('alert')).toContainText('changed plan');
  await expect(brief).toHaveValue('  A precise\nbrief  ');
  expect(requests).toHaveLength(2);
  expect(Object.keys(requests[1]).sort()).toEqual(['brief', 'plan_digest', 'request_id']);
  expect(requests[1].plan_digest).toBe('fixture-reviewed-digest');
  expect(requests[1].brief).toBe('  A precise\nbrief  ');
  await expect(confirm).toBeEnabled(); // Stale response never automatically reconfirms.
});

test('fixture late confirmation response cannot reopen a parked review or replace its draft', async ({
  page
}) => {
  let respond: (() => Promise<void>) | undefined;
  let calls = 0;
  await page.route('**/folder-digest/offers/visual-review-fixture/setup', route => {
    calls++;
    respond = () =>
      route.fulfill({
        json: {
          offer: {
            ...reviewOffer(),
            status: 'awaiting_outcome',
            setup: { status: 'running', lines: [] }
          }
        }
      });
  });
  const card = await mountReview(page);
  await card.getByLabel('What should it do with it?').fill('Exact unsent goal');
  const confirm = card.getByRole('button', { name: 'Hire & start', exact: true });
  await confirm.click();
  await expect.poll(() => calls).toBe(1);
  await expect(confirm).toBeDisabled();
  await page.evaluate(() => (window as any).PersonalAssistantFolder.parkConversation());
  await respond!();
  await expect(card).toBeHidden();
  await expect(page.locator('#folderSetupRunModal')).toBeHidden();
  expect(calls).toBe(1);
  expect(
    await page.evaluate(
      offer => (window as any).PersonalAssistantFolder.brief(offer),
      reviewOffer()
    )
  ).toBe('Exact unsent goal');
});

test('fixture review cannot mutate during conversation hydration and keeps the exact draft', async ({
  page
}) => {
  const offer = reviewOffer();
  const requests: any[] = [];
  await page.route('**/folder-digest/offers/visual-review-fixture/setup', route => {
    requests.push(route.request().postDataJSON());
    return route.fulfill({ json: { offer } });
  });
  const card = await mountReview(page, offer);
  const brief = card.getByLabel('What should it do with it?');
  await brief.fill(' Exact draft during reload ');
  const hydrate = async (loading: boolean) =>
    page.evaluate(loading => {
      (window as any).PersonalAssistantConversation.isLoading = () => loading;
      document.dispatchEvent(new CustomEvent('personal-assistant:conversation-loading'));
    }, loading);
  await hydrate(true);
  const confirm = card.getByRole('button', { name: 'Hire & start', exact: true });
  await expect(confirm).toBeDisabled();
  await confirm.dispatchEvent('click');
  expect(requests).toHaveLength(0);
  await hydrate(false);
  await expect(confirm).toBeEnabled();
  await expect(brief).toHaveValue(' Exact draft during reload ');
  await confirm.click();
  await expect.poll(() => requests.length).toBe(1);
  expect(requests[0].brief).toBe(' Exact draft during reload ');
});

test('fixture a late recovery conversation read cannot reopen the folder picker', async ({
  page
}) => {
  const offer: any = {
    ...reviewOffer(),
    status: 'awaiting_outcome',
    needs_pick: true,
    setup: { status: 'stopped', stop_reason: 'needs_pick', lines: [] }
  };
  const card = await mountReview(page, offer);
  await page.evaluate(id => {
    const app = window as any;
    app.__pickerOpens = 0;
    app.PersonalAssistantFolderContext.open = () => {
      app.__pickerOpens++;
    };
    app.PersonalAssistantConversation.currentId = () => 'another-conversation';
    app.PersonalAssistantConversation.resume = () =>
      new Promise(resolve => {
        app.__finishRecoveryRead = () => {
          app.PersonalAssistantConversation.currentId = () => id;
          resolve(true);
        };
      });
  }, offer.conversation_id);
  await card.getByRole('button', { name: 'Pick it again', exact: true }).click();
  await page.waitForFunction(() => Boolean((window as any).__finishRecoveryRead));
  await page.locator('#personalAssistantClose').click();
  await page.evaluate(() => (window as any).__finishRecoveryRead());
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  expect(await page.evaluate(() => (window as any).__pickerOpens)).toBe(0);
});

test('fixture Home shortcuts read only when opened and ignore a late open after closure', async ({
  page
}) => {
  const rows = [
    {
      id: 'song-1',
      name: 'Song one',
      can_open: true,
      connection: 'catalog_only',
      last_saved_at: new Date().toISOString()
    }
  ];
  let reads = 0;
  const writes: any[] = [];
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/assistant-program/library/projects?*', route => {
    reads++;
    return route.fulfill({ json: { rows, total: 1 } });
  });
  await page.route('**/assistant-program/library/projects/song-1/open', async route => {
    writes.push(route.request().postDataJSON());
    await held;
    await route.fulfill({ json: { route: '/workspaces/song-one' } });
  });
  const card = await mountReview(page);
  await page.locator('#personalAssistantClose').click();
  const home = {
    ...reviewOffer(),
    status: 'resolved',
    outcome: {
      kind: 'home',
      workspace_id: 'fixture-home',
      route: '/workspaces/music-home',
      receipt: [
        {
          kind: 'home',
          name: 'Music Home',
          operation: 'create',
          route: '/workspaces/music-home/assistant#projectLibraryPanel'
        },
        { kind: 'library', name: 'Listed one music project' }
      ]
    }
  };
  await page.evaluate(
    offer =>
      (window as any).PersonalAssistantFolder.showConversationReview(
        document.getElementById('visual-review-fixture-slot'),
        offer
      ),
    home
  );
  expect(reads).toBe(0);
  await page.locator('#personalAssistantLauncher').click();
  await expect(card.locator('#personalAssistantFolderSongs li')).toHaveCount(1);
  await expect(card.getByRole('link', { name: 'Browse all 1 song', exact: true })).toHaveAttribute(
    'href',
    '/workspaces/music-home/assistant#projectLibraryPanel'
  );
  expect(reads).toBe(1);
  const open = card.locator('[data-folder-run-open]');
  await open.click();
  await expect.poll(() => writes.length).toBe(1);
  const before = page.url();
  await page.locator('#personalAssistantClose').click();
  const response = page.waitForResponse('**/assistant-program/library/projects/song-1/open');
  release();
  await (await response).finished();
  await page.waitForTimeout(100);
  await expect(page).toHaveURL(before);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await page.locator('#personalAssistantLauncher').click();
  await expect(open).toBeEnabled();
  expect(reads).toBe(1);
  expect(writes).toHaveLength(1);
  expect(Object.keys(writes[0])).toEqual(['request_id']);
  await expect(card.locator('.folder-visual')).not.toContainText('Ari');
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.evaluate(() =>
      (window as any).PersonalAssistantTranscript?.revealControl(
        document.getElementById('personalAssistantFolderOfferHeadline'),
        true
      )
    );
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/04-fixture-home-result-neutral.png`
    });
  }
});

test('fixture non-Home drawer keeps the same facts and one review owner', async ({ page }) => {
  const home = await mountReview(page);
  const summary = await home.locator('.folder-visual figcaption').textContent();
  const drawer = await mountReview(page, reviewOffer(), '/agents?panel=today');
  await expect(drawer.locator('.folder-visual figcaption')).toHaveText(summary!);
  await expect(page.locator('#personalAssistantFolderBrief')).toHaveCount(1);
  await drawer.getByRole('button', { name: 'Hire & start', exact: true }).focus();
  await expect(drawer.getByRole('button', { name: 'Hire & start', exact: true })).toBeFocused();
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.evaluate(() =>
      (window as any).PersonalAssistantTranscript?.revealControl(
        document.getElementById('personalAssistantFolderOfferHeadline'),
        true
      )
    );
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/04-fixture-non-home-review.png`
    });
  }
});

test('fixture New Workspace uses identical facts without another confirmation or brief truncation', async ({
  page
}) => {
  const offer = reviewOffer();
  const home = await mountReview(page, offer);
  const summary = await home.locator('.folder-visual figcaption').textContent();
  await page.locator('#personalAssistantClose').click();
  const forbidden: string[] = [];
  const session = {
    id: 'visual-build-fixture',
    version: 1,
    status: 'open',
    transcript: [],
    applied: ['name'],
    draft: { name: 'Session' },
    folder: { observation, recognition: { verdict: 'project', candidate_id: 'candidate-0' } },
    plan: { ...offer.plan, ready: true, home: offer.plan.destination },
    folder_expires_at: new Date(Date.now() + 15 * 60_000).toISOString()
  };
  await page.route('**/api/workspaces/build-sessions/availability', route =>
    route.fulfill({ json: { available: true, folders: true } })
  );
  await page.route('**/api/workspaces/build-sessions', route =>
    route.fulfill({ json: { session } })
  );
  await page.route('**/api/workspaces/build-sessions/visual-build-fixture/**', route => {
    if (/\/(setup|turn|create)$/.test(new URL(route.request().url()).pathname)) {
      forbidden.push(route.request().url());
      return route.abort('blockedbyclient');
    }
    return route.fulfill({ json: { session } });
  });
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const review = page.locator('#workspaceBuildPlanReview');
  await expect(review).toBeVisible();
  await expect(review.locator('.folder-visual figcaption')).toHaveText(summary!);
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toHaveText('Hire & start');
  await expect(
    page.locator('#workspaceBuildPane button:visible').filter({ hasText: /^Hire & start$/ })
  ).toHaveCount(0);
  const brief = page.locator('#workspaceBuildPlanReviewBrief');
  const exact = `  ${'😀'.repeat(297)} `;
  await brief.fill(exact);
  await expect(brief).toHaveValue(exact);
  expect(await brief.evaluate((node: HTMLInputElement) => node.checkValidity())).toBe(true);
  await review.getByText('Full setup details', { exact: true }).click();
  await expect(page.locator('#workspaceBuildPlanReviewLines')).toContainText('Linked where it is');
  await expect(brief).toHaveValue(exact);
  expect(forbidden).toEqual([]);
  await brief.fill('  Review the groove before suggesting changes.  ');
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.locator('#workspaceBuildPlanReviewTitle').scrollIntoViewIfNeeded();
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/04-fixture-build-review.png`
    });
  }
  await page.locator('#workspaceBuildPlanReviewBack').click();
  await expect(review).toBeHidden();
  await expect(
    page.locator('#addFolderModal').getByRole('button', { name: 'Hire & start', exact: true })
  ).toHaveCount(1);
  await page.locator('#workspaceBuildReturnToReview').click();
  await expect(review).toBeVisible();
  await expect(brief).toHaveValue('  Review the groove before suggesting changes.  ');
  expect(forbidden).toEqual([]);
});

test('fixture New Workspace rechecks only its own requirements and preserves both hosts', async ({
  page
}) => {
  const home = await mountReview(page);
  const homeDraft = '  Assistant 界 draft  ';
  await page.locator('#personalAssistantInput').fill(homeDraft);
  await home.getByLabel('What should it do with it?').fill('  Assistant 🎼 brief  ');
  await page.locator('#personalAssistantClose').click();
  const offer = reviewOffer();
  const session: any = {
    id: 'readiness-build-fixture',
    version: 1,
    status: 'open',
    transcript: [],
    applied: ['name'],
    draft: { name: 'Session' },
    folder: {
      observation,
      recognition: { verdict: 'project', candidate_id: 'candidate-0', integration_key: 'reaper' }
    },
    plan: {
      ...offer.plan,
      ready: true,
      home: offer.plan.destination,
      hire: { ...offer.plan.hire, model: '' },
      visual: { ...offer.plan.visual, model: { state: 'missing', settings: 'system' } }
    },
    folder_expires_at: new Date(Date.now() + 15 * 60_000).toISOString()
  };
  const checkedPlan = session.plan;
  session.plan = { ready: false, unavailable: 'plan_unavailable', lines: [] };
  const writes: string[] = [];
  let rechecks = 0;
  await page.route('**/api/workspaces/build-sessions/availability', route =>
    route.fulfill({ json: { available: true, folders: true } })
  );
  await page.route('**/api/workspaces/build-sessions', route =>
    route.fulfill({ json: { session } })
  );
  await page.route('**/api/workspaces/build-sessions/readiness-build-fixture**', route => {
    const request = route.request();
    if (request.method() !== 'GET') {
      writes.push(new URL(request.url()).pathname);
      if (request.method() === 'PATCH')
        session.draft = { ...session.draft, ...request.postDataJSON()?.draft };
    }
    if (new URL(request.url()).searchParams.get('recheck') === '1') rechecks++;
    return route.fulfill({ json: { session } });
  });
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const review = page.locator('#workspaceBuildPlanReview');
  const requirements = page.locator('#workspaceBuildPlanReviewRequirements');
  await expect(review).toBeVisible();
  await expect(page.locator('#workspaceBuildPlanReviewRecheck')).toHaveText('Check requirements');
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toHaveText('Review prerequisites');
  expect(session.plan.digest).toBeUndefined();
  expect(rechecks).toBe(0);
  session.plan = checkedPlan;
  await page.locator('#workspaceBuildPlanReviewRecheck').click();
  await expect(requirements).toContainText('Configuration needed');
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeHidden();
  const brief = page.locator('#workspaceBuildPlanReviewBrief');
  await brief.fill('  Creator 🎼 exact\nbrief  ');
  await page.locator('#workspaceBuildPlanName').fill('Creator 界 name');
  await page.evaluate(async () => (window as any).sessionManager.flushWorkspaceBuildDraft());
  const name = await page.locator('#workspaceBuildPlanName').inputValue();
  const before = [...writes];
  const popup = page.waitForEvent('popup');
  await page.locator('#workspaceBuildPlanReviewModel').click();
  const settings = await popup;
  await expect(settings).toHaveURL(/\/settings#system-model$/);
  await settings.close(); // cancelling configuration does not promote readiness
  await page.locator('#workspaceBuildPlanReviewRecheck').click();
  await expect.poll(() => rechecks).toBe(2);
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeHidden();
  session.plan.visual.model = {
    state: 'configured',
    provider: 'fixture',
    name: 'controlled-model'
  };
  session.plan.hire.model = 'controlled-model';
  await page.locator('#workspaceBuildPlanReviewRecheck').click();
  await expect(requirements).toContainText('controlled-model');
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(name);
  await expect(brief).toHaveValue('  Creator 🎼 exact\nbrief  ');
  expect(writes).toEqual(before);
  expect(writes.some(path => /\/(setup|turns|create)$/.test(path))).toBe(false);
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/group4-build-requirements.png`
    });
  await page
    .locator('#addFolderModal')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('#personalAssistantInput')).toHaveValue(homeDraft);
  await expect(home.getByLabel('What should it do with it?')).toHaveValue('  Assistant 🎼 brief  ');
});

test('fixture lost response can retry; a late acceptance never reopens the closed drawer', async ({
  page
}) => {
  const offer = reviewOffer();
  const accepted = {
    ...offer,
    plan: undefined,
    status: 'awaiting_outcome',
    setup: {
      status: 'running',
      plan_digest: offer.plan.digest,
      visual: offer.plan.visual,
      hire: offer.plan.hire,
      lines: offer.plan.lines.map(line => ({ ...line, state: 'waiting' }))
    }
  };
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  const requests: any[] = [];
  await page.route('**/folder-digest/offers/visual-review-fixture/setup', async route => {
    requests.push(route.request().postDataJSON());
    if (requests.length === 1) return route.abort('connectionreset');
    await held;
    await route.fulfill({ json: { offer: accepted } });
  });
  await page.route('**/folder-digest?*', route =>
    route.fulfill({ json: { folder_digest: { offer: accepted } } })
  );
  const card = await mountReview(page, offer);
  const confirm = card.getByRole('button', { name: 'Hire & start', exact: true });
  await confirm.click();
  await expect(card.locator('#personalAssistantFolderOfferError')).toContainText(
    'could not be started'
  );
  await confirm.click();
  await expect.poll(() => requests.length).toBe(2);
  await page.locator('#personalAssistantClose').click();
  release();
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Workspace · Waiting'
  );
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('#folderSetupRunModal')).toBeHidden();
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(card.locator('#personalAssistantFolderProgress')).toBeVisible();
  expect(requests.map(row => row.plan_digest)).toEqual([offer.plan.digest, offer.plan.digest]);
});

test('fixture update failure and retry keep one surface and the same reviewed plan', async ({
  page
}) => {
  const requests: any[] = [];
  const offer = reviewOffer();
  offer.plan.visual.plugins.push({
    kind: 'provider',
    name: 'Music Project Management',
    operation: 'update',
    installed_version: 'provider-old',
    version: 'provider-reviewed'
  });
  offer.plan.lines.splice(1, 0, {
    kind: 'provider',
    name: 'Update Music Project Management to its reviewed release.'
  });
  const run = (stopped: boolean) => ({
    ...offer,
    plan: undefined,
    status: 'awaiting_outcome',
    setup: {
      plan_digest: offer.plan.digest,
      status: stopped ? 'stopped' : 'running',
      stop_reason: stopped ? 'install_failed' : '',
      visual: offer.plan.visual,
      destination: offer.plan.destination,
      hire: offer.plan.hire,
      lines: offer.plan.lines.map((line, index) => ({
        ...line,
        state: index === 0 ? 'done' : index === 1 ? (stopped ? 'failed' : 'done') : 'waiting'
      }))
    }
  });
  await page.route('**/folder-digest/offers/visual-review-fixture/setup', route => {
    requests.push(route.request().postDataJSON());
    return route.fulfill({ json: { offer: run(requests.length === 1) } });
  });
  await page.route('**/folder-digest?*', route =>
    route.fulfill({ json: { folder_digest: { offer: run(false) } } })
  );
  const card = await mountReview(page, offer);
  await card.getByRole('button', { name: 'Hire & start', exact: true }).click();
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Home plugin · Did not finish'
  );
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Integration · Complete'
  );
  await expect(card).toContainText('Stopped at Music Project Management.');
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
    'Session needs you to check a plugin that did not install'
  );
  // The plugin it stopped on is still the user's to settle, so the requirements
  // stay in view below the way forward; the diagram waits under the details.
  const requirements = card.getByRole('region', { name: 'Setup requirements', exact: true });
  await expect(requirements).toBeVisible();
  await expect(requirements).toContainText('Reviewed: provider-old → provider-reviewed');
  await expect(card).toContainText('Completed work is kept.');
  await expect(card).not.toContainText('network');
  await expect(card.locator('#personalAssistantFolderDetails')).not.toHaveAttribute('open', '');
  await expect(card.locator('#personalAssistantFolderDetails .folder-visual')).toHaveCount(1);
  await expect(page.locator('#folderSetupRunModal')).toBeHidden();
  await expect(card).not.toContainText('Assigned to workspace');
  await expect(page.locator('#homeAssistantConversation')).not.toContainText('Hired.');
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.evaluate(() =>
      (window as any).PersonalAssistantTranscript?.revealControl(
        document.getElementById('personalAssistantFolderOfferHeadline'),
        true
      )
    );
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/group4-fixture-provider-stopped.png`
    });
  }
  await card.getByRole('button', { name: 'Try again', exact: true }).click();
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Integration · Complete'
  );
  expect(requests).toHaveLength(2);
  expect(requests.map(row => row.plan_digest)).toEqual([offer.plan.digest, offer.plan.digest]);
  await page.locator('#personalAssistantClose').click();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Integration · Complete'
  );
  await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
    'Home plugin · Complete'
  );
  expect(requests).toHaveLength(2); // Reopening is observational, not another setup.
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/group4-fixture-resumed.png`
    });
});

test('fixture bind and pending reviews do not invent a completed hire', async ({ page }) => {
  const card = await mountReview(page, reviewOffer('bind'));
  await expect(card.getByRole('button', { name: 'Assign & start', exact: true })).toBeVisible();
  await expect(card.locator('.folder-visual__agent')).toContainText('Already hired');
  await expect(card.locator('.folder-visual__agent')).toContainText('Assign on setup');
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    reviewOffer('pending')
  );
  await expect(card.locator('.folder-visual__agent')).toContainText('Identity pending');
  await expect(card.locator('.folder-visual__agent')).not.toContainText('Ari');
  await expect(card.getByRole('button', { name: 'Hire & start', exact: true })).toBeVisible();
});

test('fixture agent existence and exact workspace membership stay distinct on reload', async ({
  page
}) => {
  const card = await mountReview(page);
  const agent = card.locator('.folder-visual__agent');
  await expect(agent).toContainText('Needs hiring');
  await expect(agent).toContainText('Hire & assign on setup');
  await expect(card.locator('.pa-folder__hire-agent')).toBeHidden();
  const capture = async (name: string) => {
    if (!process.env.ORI_VISUAL_EVIDENCE) return;
    await page.setViewportSize({ width: 1440, height: 1100 });
    await card.locator('.folder-visual').scrollIntoViewIfNeeded();
    await card
      .locator('.folder-visual')
      .screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/${name}.png` });
  };
  await capture('agent-needs-hiring');
  const brief = card.getByLabel('What should it do with it?');
  await brief.fill('');
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    reviewOffer('bind')
  );
  await expect(agent).toContainText('Already hired');
  await expect(agent).toContainText('Assign on setup');
  await capture('agent-already-hired');
  const pending = reviewOffer('pending');
  pending.plan.hire.model = '';
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    pending
  );
  await expect(agent).toContainText('Identity pending');
  await expect(agent).toContainText('Model not yet known');
  await capture('agent-pending');
  const reused: any = reviewOffer('bind');
  reused.plan.visual.entity.operation = 'reuse';
  reused.plan.hire.assigned_here = true;
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    reused
  );
  await expect(agent).toContainText('Assigned here');
  await expect(agent).toContainText('On this workspace');
  await expect(brief).toHaveValue('');
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await expect(agent).toContainText('Assigned here');
  await expect(brief).toHaveValue('');
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await card.locator('.folder-visual').scrollIntoViewIfNeeded();
    await card
      .locator('.folder-visual')
      .screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/agent-assigned-here.png` });
  }
});

test('fixture requirements keep independently known operations ahead of the proposed outcome', async ({
  page
}) => {
  for (const [name, integration, provider, checked] of [
    ['fresh', 'install', 'install', true],
    ['update', 'ready', 'update', true],
    ['disabled', 'ready', 'enable', true],
    ['ready', 'ready', 'ready', true],
    ['partial', 'ready', 'unavailable', false]
  ] as const) {
    const offer = reviewOffer();
    offer.plan.visual = {
      ...offer.plan.visual,
      requirements_checked: checked,
      plugins: [
        {
          kind: 'integration',
          name: 'REAPER',
          operation: integration,
          version: 'fixture-target',
          installed_version: integration === 'install' ? '' : 'fixture-target',
          source: 'fixture/publisher',
          purpose: 'Adds project tools to Ori.'
        },
        {
          kind: 'provider',
          name: 'Music Project Management',
          operation: provider,
          version: provider === 'unavailable' ? '' : 'fixture-target',
          installed_version: provider === 'update' ? 'fixture-old' : '',
          source: '<literal> fixture/publisher',
          purpose: 'Provides the project Home.'
        }
      ]
    } as typeof offer.plan.visual;
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 844 });
      const card = await mountReview(page, offer);
      const requirements = card.locator('#personalAssistantFolderRequirements');
      await expect(requirements.locator('li')).toHaveCount(2);
      const ready = requirements.locator('.folder-requirements__ready');
      if (process.env.ORI_VISUAL_EVIDENCE && ['ready', 'partial'].includes(name)) {
        await requirements.scrollIntoViewIfNeeded();
        await page.screenshot({
          path: `${process.env.ORI_VISUAL_EVIDENCE}/group3-${name}-collapsed-${width}.png`
        });
      }
      if (await ready.count()) await ready.locator(':scope > summary').click();
      await requirements.scrollIntoViewIfNeeded();
      for (const plugin of ['REAPER', 'Music Project Management']) {
        const label = requirements.getByText(plugin, { exact: true });
        await expect(label).toBeVisible();
        if (name === 'fresh') {
          const bounds = await label.boundingBox();
          const viewport = await page.locator('#personalAssistantScroll').boundingBox();
          expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport!.y + viewport!.height);
        }
      }
      await expect(requirements).toContainText(
        name === 'ready' || name === 'disabled'
          ? 'Nothing to download'
          : name === 'partial'
            ? 'not fully checked'
            : name === 'fresh'
              ? '2 plugin downloads'
              : '1 plugin download'
      );
      const action = card.locator('#personalAssistantFolderOfferActions');
      const diagram = card.locator('#personalAssistantFolderVisual');
      expect(
        await diagram.evaluate(
          (node, other) =>
            Boolean(node.compareDocumentPosition(other as Node) & Node.DOCUMENT_POSITION_FOLLOWING),
          await action.elementHandle()
        )
      ).toBe(true);
      if (process.env.ORI_VISUAL_EVIDENCE)
        await page.screenshot({
          path: `${process.env.ORI_VISUAL_EVIDENCE}/group2-${name}-${width}.png`
        });
      await requirements.locator('summary').last().click();
      await expect(requirements).toContainText('<literal> fixture/publisher');
      expect(await requirements.locator('literal').count()).toBe(0);
      if (process.env.ORI_VISUAL_EVIDENCE && name === 'fresh' && width === 1440) {
        await requirements
          .locator('.folder-requirements__source p')
          .last()
          .scrollIntoViewIfNeeded();
        await page.screenshot({
          path: `${process.env.ORI_VISUAL_EVIDENCE}/group2-source-disclosure.png`
        });
      }
      await card.getByText('Full setup details', { exact: true }).click();
      await expect(card.locator('#personalAssistantFolderPlan')).toBeVisible();
      if (process.env.ORI_VISUAL_EVIDENCE && name === 'fresh' && width === 1440) {
        await card.locator('#personalAssistantFolderConsequences').scrollIntoViewIfNeeded();
        await page.screenshot({
          path: `${process.env.ORI_VISUAL_EVIDENCE}/group2-material-effects.png`
        });
      }
    }
  }
});

test('fixture findings precede recommendation and stay bound through replace and reopen', async ({
  page
}) => {
  const writes: string[] = [];
  page.on('request', request => {
    if (
      request.method() !== 'GET' &&
      /\/(ask|route|setup|decide|prerequisites|install|enable|update|files)(\/|$)/.test(
        new URL(request.url()).pathname
      )
    )
      writes.push(request.url());
  });
  const card = await mountReview(page);
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await page.setViewportSize({ width: 1440, height: 1100 });
    await card.locator('#personalAssistantFolderFindings').scrollIntoViewIfNeeded();
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/findings-reaper-desktop.png`
    });
    await page.setViewportSize({ width: 390, height: 1400 });
    await card.locator('.folder-visual').scrollIntoViewIfNeeded();
    await card.locator('.folder-visual').screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/findings-reaper-narrow-diagram.png`
    });
  }
  const findings = card.getByRole('region', { name: 'What I found', exact: true });
  const recommendation = card.getByRole('region', { name: 'Recommendation', exact: true });
  await expect(findings).toContainText('1 file observed in this scope');
  await expect(findings).toContainText('No file contents were read for this snapshot');
  await expect(findings).toContainText('REAPER project marker');
  expect(
    await findings.evaluate(
      (node, other) =>
        Boolean(node.compareDocumentPosition(other as Node) & Node.DOCUMENT_POSITION_FOLLOWING),
      await recommendation.elementHandle()
    )
  ).toBe(true);
  const updated = {
    ...reviewOffer(),
    folder: '<literal> very long parent '.repeat(8),
    partial: true,
    subject: { name: 'Ambiguous child '.repeat(12), is_root: false, marker: '', observed_files: 0 },
    reason: 'No supported project marker was recorded in this scope.'
  };
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    updated
  );
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(findings).toContainText('Partial, bounded look');
    await expect(findings).toContainText('0 files observed');
    await expect(findings).not.toContainText('REAPER');
    expect(await card.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(1);
    if (process.env.ORI_VISUAL_EVIDENCE) {
      await findings.scrollIntoViewIfNeeded();
      await page.screenshot({
        path: `${process.env.ORI_VISUAL_EVIDENCE}/findings-partial-${width}.png`
      });
      await card
        .locator('.folder-visual')
        .screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/findings-diagram-${width}.png` });
    }
  }
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await expect(findings).toContainText(updated.subject.name);
  expect(writes).toEqual([]);
  await page.evaluate(() => (window as any).PersonalAssistantFolder.parkConversation());
  await expect(card).toBeHidden();
});

test('fixture saved findings use the same single card in Needs you', async ({ page }) => {
  await mockHiredAssistant(page);
  const offer = { ...reviewOffer(), conversation_id: '' };
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({
      json: {
        today: {
          state: 'healthy_empty',
          display_name: 'Atlas',
          needs_you: { items: [] },
          working_on: { items: [] },
          done: { items: [] }
        }
      }
    })
  );
  await page.route('**/api/personal-assistant/folder-digest**', route =>
    route.fulfill({ json: { folder_digest: { offer, chips: [] } } })
  );
  await page.goto('/?panel=today');
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toHaveAttribute('data-placement', 'needs');
  await expect(card.locator('#personalAssistantFolderFindings')).toContainText('Saved metadata');
  await expect(card.locator('.folder-visual')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderFindings')).toHaveCount(1);
  if (process.env.ORI_VISUAL_EVIDENCE) {
    await card.locator('#personalAssistantFolderFindings').scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/findings-needs-you.png` });
  }
});

test('fixture preview and disclosure stay compact and metadata-only, with a model-free review route', async ({
  page
}) => {
  const { card, forbidden, errors } = await preview(page);
  await expect(card.getByRole('figure', { name: 'Folder setup relationship' })).toHaveCount(0);
  await expect(card).toContainText('No model is needed to review');
  await expect(card.getByRole('button', { name: 'Review workspace setup' })).toBeVisible();
  await expect(card).toContainText('File contents were not opened.');
  await expect(card).toContainText('30 minutes');
  const details = card.locator('summary', { hasText: 'Scan details' });
  await details.focus();
  await page.keyboard.press('Enter');
  await expect(card).toContainText('5000 entries');
  await expect(card).toContainText('Markers do not prove file contents');
  expect(forbidden).toEqual([]);
  expect(errors).toEqual([]);
});

test('fixture unsupported and multiple candidates do not invent a setup choice', async ({
  page
}) => {
  const unsupported = await preview(page, { ...observation, projects: [] });
  await expect(unsupported.card).toContainText('No setup candidate was recorded');
  await expect(page.locator('#personalAssistantReviewFolderSetup')).toHaveJSProperty(
    'hidden',
    true
  );
  expect(unsupported.forbidden).toEqual([]);
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({ path: `${process.env.ORI_VISUAL_EVIDENCE}/group1-unsupported.png` });

  const multiple = await preview(page, {
    ...observation,
    projects: [
      ...observation.projects,
      { id: 'candidate-1', root: false, name: 'Second session', marker: '*.rpp', files: 1 }
    ]
  });
  await page.locator('#personalAssistantReviewFolderSetup').click();
  const chooser = page.locator('#personalAssistantFolderSetupCandidate');
  await expect(chooser).toBeVisible();
  await expect(chooser).toHaveValue('');
  await expect(chooser.locator('option')).toHaveCount(3);
  expect(multiple.forbidden).toEqual([]);
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/group1-explicit-candidates.png`
    });
  await page.keyboard.press('Escape');
  await expect(chooser).toBeHidden();
  await expect(page.locator('#personalAssistantReviewFolderSetup')).toBeFocused();
});

test('fixture partial snapshot keeps its limits visible before details', async ({ page }) => {
  const { card, forbidden, errors } = await preview(page, {
    ...observation,
    coverage: { ...observation.coverage, partial: true }
  });
  await page.setViewportSize({ width: 390, height: 1000 });
  await page.evaluate(() =>
    (window as any).PersonalAssistantTranscript?.revealControl(
      document.querySelector('#personalAssistantFolderPreview h3'),
      true
    )
  );
  await expect(card).toContainText('Partial, bounded look');
  if (process.env.ORI_VISUAL_EVIDENCE)
    await page.screenshot({
      path: `${process.env.ORI_VISUAL_EVIDENCE}/01-fixture-partial-snapshot.png`
    });
  expect(forbidden).toEqual([]);
  expect(errors).toEqual([]);
});

test('fixture partial and hostile names remain literal at narrow widths', async ({ page }) => {
  const hostile = '<img src=x onerror=alert(1)> 長い名前'.repeat(8);
  const { card, forbidden, errors } = await preview(page, {
    ...observation,
    folder: hostile,
    coverage: { ...observation.coverage, partial: true }
  });
  for (const width of [320, 390, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 1000 });
    await expect(card).toContainText('Partial, bounded look');
    await expect(card.locator('h3')).toHaveText(hostile);
    expect(await page.locator('img[src="x"]').count()).toBe(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth
      )
    ).toBeLessThanOrEqual(1);
    expect(await card.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
    if (process.env.ORI_VISUAL_EVIDENCE && [390, 1440].includes(width))
      await page.screenshot({
        path: `${process.env.ORI_VISUAL_EVIDENCE}/01-fixture-partial-${width}.png`
      });
  }
  expect(forbidden).toEqual([]);
  expect(errors).toEqual([]);
});

for (const theme of ['light', 'dark']) {
  test(`fixture optional summary remains separate, disclosed and keyboard reachable ${theme}`, async ({
    page
  }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    const offer: any = reviewOffer();
    const longName = 'Saved session 長い名前 '.repeat(6);
    const agentName = 'Project assistant '.repeat(5);
    offer.folder = offer.subject.name = offer.plan.visual.entity.name = longName;
    offer.status = 'resolved';
    offer.plan.optional_task = true;
    offer.outcome = {
      kind: 'project',
      workspace_id: 'summary-ws',
      route: '/workspaces/session',
      receipt: [
        {
          kind: 'workspace',
          name: longName,
          detail: 'created',
          route: '/workspaces/session',
          operation: 'create'
        },
        { kind: 'folder', name: longName, detail: 'linked where it is; source files stay here' },
        {
          kind: 'agent',
          name: agentName,
          detail: 'added',
          assigned: true,
          model: 'configured-model'
        }
      ]
    };
    offer.first_task = {
      optional: true,
      recipe: 'folder_summary',
      state: 'seeded',
      can_start: true,
      task_id: 'summary-task',
      workspace_id: 'summary-ws',
      workspace_route: '/workspaces/session',
      workspace_name: longName,
      agent: agentName,
      description: 'Summarize the REAPER session: tracks, overview and one next step',
      input_scope: 'Saved session file only, up to 64 KiB; no audio analysis',
      executor: {
        agent: agentName,
        provider: 'scripted-provider',
        model: 'configured-model-with-a-long-version-'.repeat(4),
        digest: 'fixture-summary-digest'
      }
    };
    const writes: string[] = [];
    page.on('request', request => {
      if (
        request.method() !== 'GET' &&
        /\/folder-first-task\/start$|\/tasks\/execute$|\/offers\/[^/]+\/setup$/.test(
          new URL(request.url()).pathname
        )
      )
        writes.push(request.url());
    });
    const card = await mountReview(page, offer);
    const panel = card.locator('#personalAssistantFolderOptionalTask');
    await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText(
      'Workspace ready'
    );
    await expect(panel).toContainText('Configuration only; connection not verified');
    await expect(panel).toContainText('Each explicit attempt uses model tokens');
    await expect(panel).toContainText('no model tools, source-file changes or external actions');
    await expect(card.locator('[aria-label="Folder setup relationship"]')).toHaveCount(1);
    await expect(card.locator('.folder-visual__agent')).toContainText('Assigned here');
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: width > 400 ? 1100 : 844 });
      const open = card.getByRole('link', { name: 'Open workspace', exact: true });
      await open.focus();
      const start = panel.getByRole('button', { name: 'Start summary', exact: true });
      await keyboardReach(page, start);
      expect(await panel.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(
        1
      );
      expect(await card.evaluate(node => node.scrollWidth - node.clientWidth)).toBeLessThanOrEqual(
        1
      );
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
      ).toBeLessThanOrEqual(1);
      expect(
        await card
          .locator('[id]')
          .evaluateAll(nodes => new Set(nodes.map(node => node.id)).size === nodes.length)
      ).toBe(true);
      await expect(page.locator('#personalAssistantFolderOffer')).toHaveCount(1);
      if (process.env.ORI_VISUAL_EVIDENCE)
        await page.screenshot({
          path: `${process.env.ORI_VISUAL_EVIDENCE}/optional-${theme}-${width}.png`
        });
    }
    expect(writes).toEqual([]);
  });
}

async function expectUnobscured(target: Locator) {
  const visibility = await target.evaluate(node => {
    const rect = node.getBoundingClientRect();
    const x = rect.x + rect.width / 2;
    const y = rect.y + rect.height / 2;
    const points = [
      [x, y],
      [x, rect.top + 2],
      [x, rect.bottom - 2],
      [rect.left + 2, y],
      [rect.right - 2, y]
    ];
    const coveredBy = points
      .map(([left, top]) => document.elementFromPoint(left, top))
      .filter(hit => !hit || !node.contains(hit))
      .map(hit =>
        hit ? `${hit.tagName}#${hit.id}.${Array.from(hit.classList).join('.')}` : 'outside viewport'
      );
    return { coveredBy, control: rect.toJSON() };
  });
  expect(
    visibility.coveredBy,
    `control is not covered by page chrome: ${JSON.stringify(visibility)}`
  ).toEqual([]);
}

async function keyboardReach(page: Page, target: Locator) {
  // macOS Safari's default Tab visits text inputs; Option-Tab includes buttons
  // and links without changing the user's system keyboard-access preference.
  const key = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+Tab' : 'Tab';
  for (let n = 0; n < 80; n++) {
    if (await target.evaluate(node => node === document.activeElement)) break;
    await page.keyboard.press(key);
  }
  await expect(target, 'reachable by keyboard').toBeFocused();
  const geometry = await target.evaluate(node => {
    const scroll = node.closest('.personal-assistant-panel__scroll')!;
    const viewport = scroll.getBoundingClientRect();
    const bounds = node.getBoundingClientRect();
    return {
      visible: bounds.top >= viewport.top - 1 && bounds.bottom <= viewport.bottom + 1,
      viewport: viewport.toJSON(),
      control: bounds.toJSON(),
      window: { width: innerWidth, height: innerHeight },
      scrollTop: scroll.scrollTop,
      scrollHeight: scroll.scrollHeight
    };
  });
  expect(
    geometry.visible,
    `focused control is fully visible, not clipped by the pinned composer: ${JSON.stringify(geometry)}`
  ).toBe(true);
  await expectUnobscured(target);
}

for (const theme of ['light', 'dark']) {
  test(`fixture requirements at actual Chromium 200% browser zoom ${theme}`, async ({
    browserName
  }, testInfo) => {
    test.skip(browserName !== 'chromium', 'uses Chromium Settings in a disposable browser profile');
    const context = await chromium.launchPersistentContext(testInfo.outputPath('zoom-profile'), {
      channel: 'chromium',
      headless: true,
      viewport: null,
      deviceScaleFactor: undefined, // Do not inherit Desktop Chrome's emulated scale.
      baseURL: testInfo.project.use.baseURL,
      args: ['--window-size=1280,900']
    });
    try {
      const page = await context.newPage();
      const forbidden: string[] = [];
      page.on('request', request => {
        if (
          request.method() === 'POST' &&
          /\/(setup|decide|ask|route|install|enable|prerequisites)$/.test(
            new URL(request.url()).pathname
          )
        )
          forbidden.push(request.url());
      });
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      const offer: any = reviewOffer();
      offer.prerequisites_available = true;
      offer.plan.visual.requirements_checked = true;
      offer.plan.visual.plugins = [
        { kind: 'integration', name: 'REAPER', operation: 'install', version: 'fixture-target' },
        {
          kind: 'provider',
          name: 'Music Project Management',
          operation: 'install',
          version: 'fixture-target'
        }
      ];
      await mountReview(page, offer);
      const metrics = () =>
        page.evaluate(() => ({ innerWidth, innerHeight, outerWidth, devicePixelRatio }));
      const before = await metrics();
      const settings = await context.newPage();
      await settings.goto('chrome://settings/appearance');
      // This is the browser's own page-zoom setting, not CSS scaling, a
      // narrower viewport, device-scale emulation or a user's browser profile.
      const zoom = settings.getByRole('combobox', { name: 'Page zoom' });
      await zoom.selectOption('2');
      const card = await mountReview(page, offer);
      await page.evaluate(() => document.fonts.ready);
      const after = await metrics();
      console.log('Native browser zoom:', JSON.stringify({ theme, before, after }));
      expect(after.outerWidth).toBe(before.outerWidth);
      expect(after.innerWidth).toBeCloseTo(before.innerWidth / 2, 0);
      expect(after.devicePixelRatio).toBeCloseTo(before.devicePixelRatio * 2, 2);
      await testInfo.attach('native-browser-zoom.json', {
        contentType: 'application/json',
        body: JSON.stringify({ browser: context.browser()?.version(), before, after })
      });
      // Geometry alone can pass with the navbar painted over the taller sheet.
      // Check its real hit-testing before scrolling or moving focus.
      await expectUnobscured(page.locator('#personalAssistantClose'));
      const overflow = await card.evaluate(node =>
        Math.max(
          node.scrollWidth - node.clientWidth,
          document.documentElement.scrollWidth - innerWidth
        )
      );
      expect(overflow).toBeLessThanOrEqual(1);
      const controls = await page.locator('#personalAssistantChips').evaluate(node =>
        Array.from(node.children)
          .filter(child => !child.hasAttribute('hidden'))
          .map(child => ({
            left: child.getBoundingClientRect().left,
            right: child.getBoundingClientRect().right,
            overflow: child.scrollWidth - child.clientWidth
          }))
      );
      expect(
        controls.every(
          (control, n) => control.overflow <= 1 && (!n || control.left >= controls[n - 1].right)
        )
      ).toBe(true);
      // The newly direct per-package controls must also remain keyboard
      // reachable and unobscured at real browser zoom, without executing them.
      await keyboardReach(
        page,
        card.getByRole('button', { name: 'Install Music Project Management', exact: true })
      );
      const draft = '  Native zoom 🎼\nexact draft  ';
      await page.locator('#personalAssistantInput').fill(draft);
      await card.getByLabel('What should it do with it?').focus();
      const confirm = card.getByRole('button', { name: 'Install 2 plugins & set up', exact: true });
      await expect(confirm).toBeVisible();
      await keyboardReach(page, confirm);
      expect(
        await confirm.evaluate(
          node =>
            getComputedStyle(node).boxShadow !== 'none' ||
            getComputedStyle(node).outlineStyle !== 'none'
        )
      ).toBe(true);
      await keyboardReach(page, card.locator('#personalAssistantFolderDetails > summary'));
      await page.keyboard.press('Enter');
      await expect(card.locator('#personalAssistantFolderPlan')).toBeVisible();
      await keyboardReach(page, confirm);
      await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
      if (process.env.ORI_VISUAL_EVIDENCE) {
        // Capture the native viewport without Playwright's CSS-sized clip,
        // which does not account for a browser-controlled zoom factor.
        const capture = await context.newCDPSession(page);
        const image = await capture.send('Page.captureScreenshot', {
          format: 'png',
          captureBeyondViewport: false
        });
        await writeFile(
          `${process.env.ORI_VISUAL_EVIDENCE}/group5-native-zoom-${theme}.png`,
          Buffer.from(image.data, 'base64')
        );
        await capture.detach();
      }
      await page.keyboard.press('Escape');
      await expect(page.locator('#personalAssistantPanel')).toBeHidden();
      const launcher = page.locator('#personalAssistantLauncher');
      await launcher.focus();
      await page.keyboard.press('Enter');
      await expect(card).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(launcher).toBeFocused();
      expect(forbidden).toEqual([]);
      await zoom.selectOption('1');
    } finally {
      await context.close();
    }
  });
}

// Explicit presentation fixtures, including a canonical-shaped saved receipt.
// No live integration/model or native-picker claim is made by this matrix.
for (const theme of ['light', 'dark']) {
  for (const motion of ['reduce', 'no-preference'] as const) {
    for (const width of [320, 390, 768, 1024, 1440, 640]) {
      test(`fixture full journey layout ${theme} ${motion} ${width}`, async ({
        page,
        browserName
      }, testInfo) => {
        test.skip(
          width === 640 && browserName !== 'chromium',
          'CDP 200%-equivalent reflow is Chromium-only'
        );
        await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
        await page.emulateMedia({ reducedMotion: motion });
        const size = { width, height: width === 640 ? 450 : 1000 };
        await page.setViewportSize(size);
        if (width === 640) {
          const client = await page.context().newCDPSession(page);
          await client.send('Emulation.setDeviceMetricsOverride', {
            ...size,
            deviceScaleFactor: 2,
            mobile: false,
            screenWidth: 1280,
            screenHeight: 900
          });
        }
        const errors: string[] = [];
        page.on('pageerror', error => errors.push(error.message));
        const offer = reviewOffer();
        const stopped = {
          ...offer,
          plan: undefined,
          status: 'awaiting_outcome',
          setup: {
            status: 'stopped',
            stop_reason: 'install_failed',
            plan_digest: offer.plan.digest,
            visual: offer.plan.visual,
            destination: offer.plan.destination,
            hire: offer.plan.hire,
            lines: offer.plan.lines.map((line, n) => ({ ...line, state: n ? 'waiting' : 'failed' }))
          }
        };
        const resolved = {
          ...offer,
          plan: undefined,
          status: 'resolved',
          outcome: {
            kind: 'project',
            route: '/workspaces/fixture-session',
            receipt: [
              { kind: 'workspace', name: 'Session', operation: 'create', placement: 'standalone' },
              { kind: 'agent', name: 'Ari', assigned: true, model: 'Fixture model' }
            ]
          }
        };
        const writes: any[] = [];
        await page.route('**/folder-digest/offers/visual-review-fixture/setup', route => {
          writes.push(route.request().postDataJSON());
          return route.fulfill({ json: { offer: writes.length === 1 ? stopped : resolved } });
        });
        const card = await mountReview(page);
        // Geometry is measured after the shipped fonts settle, without scrolling
        // or resizing to repair the layout under test.
        await page.evaluate(() => document.fonts.ready);
        if (width === 640) {
          // Presentation-only stress: a configured assistant adds another chip.
          // Do not shrink these controls until their labels overlap each other.
          const separated = await page.evaluate(() => {
            document.getElementById('personalAssistantWatchRow')!.hidden = false;
            const controls = Array.from(
              document.querySelector('#personalAssistantChips')!.children
            ).filter(node => !node.hasAttribute('hidden'));
            const chips = controls.map(node => node.getBoundingClientRect());
            return {
              separated:
                chips.every((box, n) => !n || box.left >= chips[n - 1].right) &&
                controls.every(node => node.scrollWidth <= node.clientWidth + 1),
              controls: controls.map((node, i) => ({
                id: node.id,
                box: chips[i].toJSON(),
                scroll: node.scrollWidth,
                client: node.clientWidth,
                display: getComputedStyle(node).display,
                classes: node.className,
                width: getComputedStyle(node).width,
                minWidth: getComputedStyle(node).minWidth,
                maxWidth: getComputedStyle(node).maxWidth,
                shrink: getComputedStyle(node).flexShrink,
                basis: getComputedStyle(node).flexBasis,
                transition: getComputedStyle(node).transition
              }))
            };
          });
          expect(
            separated.separated,
            `compact composer controls do not overlap: ${JSON.stringify(separated.controls)}`
          ).toBe(true);
        }
        const brief = card.getByLabel('What should it do with it?');
        await brief.focus();
        const confirm = card.getByRole('button', { name: 'Hire & start', exact: true });
        await keyboardReach(page, confirm);
        await expect(confirm).toBeFocused();
        const layout = await card.evaluate(node => {
          const decision = node.querySelector('.folder-setup-decision')!;
          const diagram = node.querySelector('.folder-setup-diagram')!;
          return {
            overflow: Math.max(
              node.scrollWidth - node.clientWidth,
              document.documentElement.scrollWidth - innerWidth
            ),
            storyOrder:
              Boolean(
                node
                  .querySelector('#personalAssistantFolderFindings')!
                  .compareDocumentPosition(diagram) & Node.DOCUMENT_POSITION_FOLLOWING
              ) &&
              Boolean(diagram.compareDocumentPosition(decision) & Node.DOCUMENT_POSITION_FOLLOWING),
            decisionBelow:
              decision.getBoundingClientRect().top >= diagram.getBoundingClientRect().bottom
          };
        });
        expect(layout.overflow).toBeLessThanOrEqual(1);
        expect(layout.storyOrder).toBe(true);
        if (width <= 768) expect(layout.decisionBelow).toBe(true);
        const target = await confirm.boundingBox();
        expect(target!.height).toBeGreaterThanOrEqual(24);
        expect(target!.x).toBeGreaterThanOrEqual(0);
        expect(target!.x + target!.width).toBeLessThanOrEqual(width);
        expect(
          await confirm.evaluate(
            node =>
              getComputedStyle(node).boxShadow !== 'none' ||
              getComputedStyle(node).outlineStyle !== 'none'
          )
        ).toBe(true);
        await keyboardReach(page, card.locator('#personalAssistantFolderDetails > summary'));
        await page.keyboard.press('Enter');
        await expect(card.locator('#personalAssistantFolderPlan')).toBeVisible();
        expect(writes).toHaveLength(0);
        await keyboardReach(page, confirm);
        await page.keyboard.press('Enter');
        await expect(card.locator('#personalAssistantFolderProgress')).toContainText(
          'Integration · Did not finish'
        );
        await expect(card.locator('.folder-visual__agent')).not.toContainText('Assigned here');
        const retry = card.getByRole('button', { name: 'Try again', exact: true });
        await keyboardReach(page, retry);
        await page.keyboard.press('Enter');
        await expect(card.locator('.folder-visual__agent')).toContainText('Assigned here');
        await expect(card.locator('.folder-visual')).toContainText('Separate workspace');
        const open = card.getByRole('link', { name: 'Open Session', exact: true });
        await expect(open).toHaveAttribute('href', '/workspaces/fixture-session');
        await keyboardReach(page, open);
        await expect(open).toBeFocused();
        await expect(card.getByRole('figure', { name: 'Folder setup relationship' })).toHaveCount(
          1
        );
        await expect(card.locator('figcaption')).toContainText('Confirmed member: Ari');
        await expect(page.locator('#folderSetupRunModal')).toHaveCount(0);
        await expect(page.locator('#personalAssistantFolderOffer')).toHaveCount(1);
        await expect(card.locator('#personalAssistantFolderOfferNote')).toHaveAttribute(
          'aria-live',
          'polite'
        );
        expect(writes.map(body => body.plan_digest)).toEqual([
          offer.plan.digest,
          offer.plan.digest
        ]);
        expect(errors).toEqual([]);
        await page.screenshot({ path: testInfo.outputPath('fixture-ready.png') });
        await page.keyboard.press('Escape');
        await expect(page.locator('#personalAssistantPanel')).toBeHidden();
        const launcher = page.locator('#personalAssistantLauncher');
        await launcher.focus();
        await page.keyboard.press('Enter');
        await expect(open).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(launcher).toBeFocused();
        expect(writes).toHaveLength(2);
      });
    }
  }
}
