import { test, expect, type Page } from '@playwright/test';
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

async function mountReview(page: Page, offer = reviewOffer()) {
  await mockHiredAssistant(page);
  await page.goto('/?panel=today');
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

test('fixture preview and disclosure stay metadata-only, with neutral destination and agent', async ({
  page
}) => {
  const { card, forbidden, errors } = await preview(page);
  await expect(card.getByRole('figure', { name: 'Folder setup relationship' })).toBeVisible();
  await expect(card).toContainText('Destination not yet reviewed');
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
    await expect(card.locator('.folder-visual h4').first()).toHaveText(hostile);
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
