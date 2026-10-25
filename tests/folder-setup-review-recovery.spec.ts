import { expect, test, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { basename, join } from 'node:path';
import { mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { chooseAttach } from './helpers/assistant-menus';
import { stageReviewedFolderPackages } from './helpers/reviewed-folder-packages';
import { watchFolderRequests, expectNoFolderExecution } from './helpers/folder-aware-collection';

// Real isolated host, no paid provider, plugin installation or setup execution.
// ./scripts/e2e-fresh.sh --sandbox-env ORI_RECOVERY_SANDBOX tests/folder-setup-review-recovery.spec.ts
const sandbox = process.env.ORI_RECOVERY_SANDBOX || '';
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

async function hire(request: APIRequestContext) {
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  let pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (pa.state === 'needs_hire') {
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: randomUUID(),
          if_version: pa.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Keep my projects moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  }
  if (!pa.hq_workspace_id)
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: randomUUID(),
          if_version: pa.state_version,
          name: 'My HQ',
          timezone: 'UTC'
        }
      })
    );
}

function seed(name: string) {
  const root = join(sandbox, 'Music', name);
  mkdirSync(root, { recursive: true, mode: 0o750 });
  writeFileSync(join(root, 'song.rpp'), '<REAPER_PROJECT 0.1 7.0 1\n>\n', { mode: 0o600 });
  return root;
}

async function review(request: APIRequestContext, name: string) {
  const path = seed(name);
  const picked = await ok(await request.post('/api/folder-picker/select-path', { data: { path } }));
  const draft_id = randomUUID();
  const selected = await ok(
    await request.post('/api/home-assistant/folder-context/select', {
      data: {
        draft_id,
        revision: '',
        mode: 'selection',
        selection_token: picked.selection_token
      }
    })
  );
  return ok(
    await request.post('/api/home-assistant/folder-context/review', {
      data: {
        draft_id,
        revision: '',
        selection_id: selected.observation.id,
        candidate_id: 'candidate-0'
      }
    })
  );
}

async function attach(page: Page, name = 'Album-3', route = '/') {
  await page.goto(route);
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await chooseAttach(page, '#personalAssistantFolderChip');
  await page.locator('#personalAssistantFolderChoices .folder-path-entry__input').fill(seed(name));
  await page.locator('#personalAssistantFolderChoices .folder-path-entry__use').click();
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText(name);
}

async function screenshot(page: Page, info: TestInfo, name: string) {
  const dir = process.env.ORI_RECOVERY_EVIDENCE;
  if (dir) mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: dir ? join(dir, `${name}.png`) : info.outputPath(`${name}.png`) });
}

async function closeReview(request: APIRequestContext, reviewed: any) {
  return ok(
    await request.post('/api/home-assistant/folder-context/review/close', {
      data: {
        conversation_id: reviewed.conversation.id,
        revision: reviewed.folder_context.revision,
        offer_id: reviewed.folder_context.offer_id
      }
    })
  );
}

const digestPath = () => join(sandbox, 'Ori Workspaces', 'my-hq', '.ori', 'folder-digest-v1.json');
const storedDigest = () => readFileSync(digestPath(), 'utf8');

async function state(request: APIRequestContext) {
  return {
    digest: storedDigest(),
    workspaces: await ok(await request.get('/api/workspaces')),
    conversations: (await ok(await request.get('/api/home-assistant/conversations'))).conversations
      .length
  };
}

test.beforeAll(async ({ request }) => {
  expect(
    sandbox,
    'requires --sandbox-env ORI_RECOVERY_SANDBOX; never use the real HOME'
  ).toBeTruthy();
  expect(basename(realpathSync(sandbox))).toMatch(/^ori-(e2e|demo)\./);
  await hire(request);
  // Prepare published reviewed packages in this disposable host only, before
  // the request/state guard. Navigation itself must never install anything.
  const plugins = (await ok(await request.get('/api/plugins'))).plugins || [];
  if (!plugins.some((plugin: any) => plugin.name === 'reaper-plugin'))
    await stageReviewedFolderPackages(request);
});

test('Album-3 returns from Album-5 to its normal visual review, preserving the draft', async ({
  page,
  request
}, info) => {
  const blocking = await review(request, 'Album-5');
  const before = await state(request);
  const seen = watchFolderRequests(page);
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  await expect(notice).toContainText('Album-5 · Waiting for review');
  const draft = 'Keep this exact Album-3 draft unsent 🎼';
  await page.locator('#personalAssistantInput').fill(draft);
  const selection = await page.evaluate(
    () => (window as any).PersonalAssistantFolderContext.current().observation.id
  );
  await screenshot(page, info, 'blocked');
  expect(await state(request)).toEqual(before);
  await notice.getByRole('button', { name: 'Open Album-5 review', exact: true }).click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeVisible();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toContainText('Album-5');
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    blocking.conversation.id
  );
  expect(await state(request)).toEqual(before);
  await screenshot(page, info, 'opened');
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toBeHidden();
  const closed = await state(request);
  await page.locator('#personalAssistantFolderReturn').click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  await expect(
    page.getByRole('button', { name: 'Review workspace setup', exact: true })
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => (window as any).PersonalAssistantFolderContext.current().observation.id
    )
  ).toBe(selection);
  expect(await state(request)).toEqual(closed);
  await screenshot(page, info, 'returned');
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await expect(card).toBeVisible();
  await expect(card).toContainText('Album-3');
  const details = card.locator('#personalAssistantFolderDetails');
  if (!(await details.evaluate(node => (node as HTMLDetailsElement).open)))
    await details.getByText('Full setup details', { exact: true }).click();
  await expect(card.locator('.folder-visual')).toBeVisible();
  await card.locator('.folder-visual').scrollIntoViewIfNeeded();
  await screenshot(page, info, 'new-recommendation');
  expectNoFolderExecution(seen);
  expect(seen.posts.filter(item => item.path.endsWith('/folder-context/select'))).toHaveLength(1);
  expect(seen.posts.filter(item => /\/scan|\/build-sessions/.test(item.path))).toEqual([]);
  // Retire this review using its real control, keeping later cases independent.
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toBeHidden();
});

test('attach selects pending Album-5 beside an unrelated stopped Album-4 without changing collection or review policy', async ({
  page,
  request
}) => {
  // Saved-run fixture only: no setup is executed. All writes are limited to
  // this verified disposable sidecar, before/after the guarded observation.
  const prior = await review(request, 'Album-4');
  const doc = JSON.parse(storedDigest());
  const stopped = doc.offers.find((offer: any) => offer.id === prior.folder_context.offer_id);
  stopped.status = 'awaiting_outcome';
  stopped.setup = {
    status: 'stopped',
    stop_reason: 'install_failed',
    plan_digest: 'fixture-stopped',
    lines: [{ kind: 'integration', name: 'Update REAPER integration', state: 'failed' }],
    started_at: new Date().toISOString(),
    updated_at: new Date().toISOString()
  };
  doc.version++;
  writeFileSync(digestPath(), JSON.stringify(doc), { mode: 0o600 });
  const blocking = await review(request, 'Album-5');
  const before = await state(request);
  const seen = watchFolderRequests(page);
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  await expect(notice).toContainText('Album-5');
  await expect(notice).not.toContainText('Album-4');
  expect(await state(request)).toEqual(before);
  expectNoFolderExecution(seen);
  await closeReview(request, blocking);
  // Existing Review has its own awaiting-outcome handoff policy; this test
  // deliberately ends at attach instead of changing that separate policy.
  const cleanup = JSON.parse(storedDigest());
  cleanup.offers.find((offer: any) => offer.id === stopped.id).status = 'closed';
  cleanup.version++;
  writeFileSync(digestPath(), JSON.stringify(cleanup), { mode: 0o600 });
});

test('Open rechecks a review closed in another tab and stays at the attached folder', async ({
  page,
  request
}, info) => {
  const blocking = await review(request, 'Album-5');
  const seen = watchFolderRequests(page);
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  await expect(notice.getByRole('button', { name: 'Open Album-5 review' })).toBeVisible();
  await page.locator('#personalAssistantInput').fill('Do not navigate away');
  await closeReview(request, blocking); // another tab's existing Close control
  const before = await state(request);
  await notice.getByRole('button', { name: 'Open Album-5 review' }).click();
  await expect(
    page.getByRole('button', { name: 'Review workspace setup', exact: true })
  ).toBeVisible();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Do not navigate away');
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    ''
  );
  expect(await state(request)).toEqual(before);
  expectNoFolderExecution(seen);
  expect(
    seen.posts.map(item => item.path).filter(path => path.includes('/folder-context/'))
  ).toEqual([
    '/api/home-assistant/folder-context/select',
    '/api/home-assistant/folder-context/recommendation'
  ]);
  await screenshot(page, info, 'resolved-on-open');
});

for (const route of ['/', '/agents']) {
  test(`a Home blocker reveals its card or gives honest Home-only guidance on ${route}`, async ({
    page,
    request
  }, info) => {
    const picked = await ok(
      await request.post('/api/folder-picker/select-path', { data: { path: seed('Album-Home') } })
    );
    const scanned = await ok(
      await request.post('/api/personal-assistant/folder-digest/scan', {
        data: { selection_token: picked.selection_token }
      })
    );
    const offerId = scanned.offer.id;
    const before = await state(request);
    const seen = watchFolderRequests(page);
    await attach(page, 'Album-3', route);
    const strip = page.locator('#personalAssistantTodayStripToggle');
    await page.locator('#personalAssistantInput').fill('Keep my Home draft');
    if (route === '/') {
      await expect(strip).toHaveAttribute('aria-expanded', 'false');
      await page.getByRole('button', { name: 'Open Album-Home review', exact: true }).click();
      await expect(strip).toHaveAttribute('aria-expanded', 'true');
      await expect(
        page.locator('#personalAssistantNeedsYouCards #personalAssistantFolderOffer')
      ).toBeVisible();
      expect(await page.evaluate(() => (window as any).PersonalAssistantFolder.current().id)).toBe(
        offerId
      );
    } else {
      const notice = page.locator('#personalAssistantFolderStructure');
      await expect(notice).toContainText(
        'This review was opened from Home. Finish or close it there'
      );
      await expect(notice.getByRole('button')).toHaveText(['Refresh recommendation']);
    }
    expect(
      await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())
    ).toBe('');
    await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
    await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep my Home draft');
    expect(await state(request)).toEqual(before);
    expectNoFolderExecution(seen);
    expect(seen.posts.filter(item => /\/scan|\/review$|\/build-sessions/.test(item.path))).toEqual(
      []
    );
    await screenshot(page, info, route === '/' ? 'home-revealed' : 'home-revealed-off-home');
    // Fixture cleanup is a separate explicit decision, after all no-effects checks.
    await ok(
      await request.post(`/api/personal-assistant/folder-digest/offers/${offerId}/decide`, {
        data: { decision: 'later', request_id: randomUUID() }
      })
    );
  });
}

test('/clear keeps a verified blocker waiting and retires an existing return trip', async ({
  page,
  request
}) => {
  const blocking = await review(request, 'Album-5');
  await attach(page);
  await expect(page.getByRole('button', { name: 'Open Album-5 review' })).toBeVisible();
  const before = await state(request);
  const seen = watchFolderRequests(page);
  const input = page.locator('#personalAssistantInput');
  await input.fill('/clear');
  await page.locator('#personalAssistantSend').click();
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(page.locator('#personalAssistantPanelStatus')).toContainText(
    'Album-5 is still waiting in its own conversation.'
  );
  expect(await state(request)).toEqual(before);
  await attach(page);
  await page.getByRole('button', { name: 'Open Album-5 review' }).click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeVisible();
  await input.fill('/clear');
  await page.locator('#personalAssistantSend').click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
  expect(await state(request)).toEqual(before);
  expectNoFolderExecution(seen);
  expect(seen.posts.filter(item => item.path.endsWith('/review/close'))).toEqual([]);
  await closeReview(request, blocking);
});

test('literal hostile/long names fit the six-size/theme matrix with keyboard controls and status', async ({
  page,
  request
}, info) => {
  const name = `<img src=x onerror=alert(1)> ${'Long-Album-'.repeat(5)}`;
  const blocking = await review(request, name);
  const seen = watchFolderRequests(page);
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  const status = notice.getByRole('status');
  // Reuse one held selection across layout changes, respecting the host's
  // bounded 16-selection limit instead of manufacturing fresh authority.
  for (const width of [320, 390, 1280])
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(
        value => document.documentElement.setAttribute('data-bs-theme', value),
        theme
      );
      await expect(status).toContainText(name);
      await expect(notice.locator('img')).toHaveCount(0);
      const open = notice.getByRole('button', { name: `Open ${name} review`, exact: true });
      await open.focus();
      await expect(open).toBeFocused();
      await page.keyboard.press('Tab');
      const refresh = notice.getByRole('button', { name: 'Refresh recommendation' });
      await expect(refresh).toBeFocused();
      await page.keyboard.press('Enter');
      await expect(status).toContainText(name);
      await notice.scrollIntoViewIfNeeded();
      const fits = await notice.evaluate(node => {
        const panel = document.getElementById('personalAssistantPanel')!;
        const box = panel.getBoundingClientRect();
        return [node, ...node.querySelectorAll('button')].every(item => {
          const rect = item.getBoundingClientRect();
          return (
            rect.left >= box.left - 1 &&
            rect.right <= box.right + 1 &&
            item.scrollWidth <= item.clientWidth + 1
          );
        });
      });
      expect(fits).toBe(true);
      await screenshot(page, info, `notice-${width}-${theme}`);
    }
  await closeReview(request, blocking);
  await notice.getByRole('button', { name: 'Refresh recommendation' }).click();
  const reviewButton = page.getByRole('button', { name: 'Review workspace setup', exact: true });
  for (const width of [320, 390, 1280])
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 900 });
      await page.evaluate(
        value => document.documentElement.setAttribute('data-bs-theme', value),
        theme
      );
      await expect(reviewButton).toBeVisible();
      await reviewButton.focus();
      await expect(reviewButton).toBeFocused();
      await screenshot(page, info, `ready-${width}-${theme}`);
    }
  expectNoFolderExecution(seen);
});

test('presentation fixture: a verified build blocker has text and Refresh, never a resume button', async ({
  page
}, info) => {
  const seen = watchFolderRequests(page);
  await page.route('**/api/home-assistant/folder-context/select', async route => {
    const response = await route.fetch();
    const selected = await response.json();
    await route.fulfill({
      json: {
        ...selected,
        hire_candidate: '',
        hire_unavailable: 'review_pending',
        blocker: {
          kind: 'build',
          subject: 'Album-Build',
          status: 'pending',
          blocks: true,
          message:
            'A workspace creation review is open. Return to Create Workspace to finish or close it, then refresh this recommendation.'
        }
      }
    });
  });
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  await expect(notice).toContainText('Album-Build');
  await expect(notice.getByRole('button')).toHaveText(['Refresh recommendation']);
  expect(seen.posts.filter(item => /\/build-sessions|\/review$/.test(item.path))).toEqual([]);
  expectNoFolderExecution(seen);
  await screenshot(page, info, 'build-text-only');
});
