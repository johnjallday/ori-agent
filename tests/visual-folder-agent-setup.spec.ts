import { test, expect, type Page, type Locator } from '@playwright/test';
import { mockHiredAssistant } from './helpers/hired-assistant';

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
  await page.locator('#personalAssistantFolderChip:enabled').click();
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
  subject: { name: 'Session' },
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
  await expect(card.getByText('Before you start', { exact: true })).toBeVisible();
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
        state: index === 0 ? (stopped ? 'failed' : 'done') : 'waiting'
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
    'Integration · Did not finish'
  );
  await expect(card.locator('.folder-visual')).toBeVisible();
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
      path: `${process.env.ORI_VISUAL_EVIDENCE}/03-fixture-update-blocked.png`
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
  expect(requests).toHaveLength(2); // Reopening is observational, not another setup.
});

test('fixture bind and pending reviews do not invent a completed hire', async ({ page }) => {
  const card = await mountReview(page, reviewOffer('bind'));
  await expect(card.getByRole('button', { name: 'Assign & start', exact: true })).toBeVisible();
  await expect(card.locator('.folder-visual__agent')).toContainText('Proposed assignment');
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    reviewOffer('pending')
  );
  await expect(card.locator('.folder-visual__agent')).toContainText('Identity pending');
  await expect(card.locator('.folder-visual__agent')).not.toContainText('Ari');
  await expect(card.getByRole('button', { name: 'Hire & start', exact: true })).toBeVisible();
});

test('fixture preview and disclosure stay compact and metadata-only, with a model-free review route', async ({
  page
}) => {
  const { card, forbidden, errors } = await preview(page);
  await expect(card.getByRole('figure', { name: 'Folder setup relationship' })).toHaveCount(0);
  await expect(card).toContainText('No model is needed to review');
  await expect(card.getByRole('button', { name: 'Review workspace setup' })).toBeVisible();
  await expect(card).toContainText('contents not read');
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
  await expect(page.locator('#personalAssistantReviewFolderSetup')).toBeHidden();
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

async function keyboardReach(page: Page, target: Locator) {
  // macOS Safari's default Tab visits text inputs; Option-Tab includes buttons
  // and links without changing the user's system keyboard-access preference.
  const key = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+Tab' : 'Tab';
  for (let n = 0; n < 80; n++) {
    if (await target.evaluate(node => node === document.activeElement)) break;
    await page.keyboard.press(key);
  }
  await expect(target, 'reachable by keyboard').toBeFocused();
  expect(
    await target.evaluate(node => {
      const scroll = node.closest('.personal-assistant-panel__scroll')!;
      const viewport = scroll.getBoundingClientRect();
      const bounds = node.getBoundingClientRect();
      return bounds.top >= viewport.top - 1 && bounds.bottom <= viewport.bottom + 1;
    }),
    'focused control is fully visible, not clipped by the pinned composer'
  ).toBe(true);
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
        if (width === 640) {
          // Presentation-only stress: a configured assistant adds another chip.
          // Do not shrink these controls until their labels overlap each other.
          const separated = await page.evaluate(() => {
            document.getElementById('personalAssistantWatchRow')!.hidden = false;
            const controls = Array.from(
              document.querySelector('#personalAssistantChips')!.children
            ).filter(node => !node.hasAttribute('hidden'));
            const chips = controls.map(node => node.getBoundingClientRect());
            return (
              chips.every((box, n) => !n || box.left >= chips[n - 1].right) &&
              controls.every(node => node.scrollWidth <= node.clientWidth + 1)
            );
          });
          expect(separated, 'compact composer controls do not overlap').toBe(true);
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
            decisionFirst: Boolean(
              decision.compareDocumentPosition(diagram) & Node.DOCUMENT_POSITION_FOLLOWING
            ),
            diagramBelow:
              diagram.getBoundingClientRect().top >= decision.getBoundingClientRect().bottom
          };
        });
        expect(layout.overflow).toBeLessThanOrEqual(1);
        expect(layout.decisionFirst).toBe(true);
        if (width <= 768) expect(layout.diagramBelow).toBe(true);
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
        await expect(card).not.toContainText('Confirmed workspace member');
        const retry = card.getByRole('button', { name: 'Try again', exact: true });
        await keyboardReach(page, retry);
        await page.keyboard.press('Enter');
        await expect(card.locator('.folder-visual')).toContainText('Confirmed workspace member');
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
