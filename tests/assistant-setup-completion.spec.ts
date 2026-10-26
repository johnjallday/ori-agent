import { test, expect, type Page } from '@playwright/test';
import { mockHiredAssistant } from './helpers/hired-assistant';
import { revealFolderSetupDetails } from './helpers/folder-setup-card';

// Injected presentation fixtures on the real isolated host: not evidence of
// native folder selection, setup execution, or a live model/report.
const completed = () => ({
  id: 'completion-fixture',
  conversation_id: 'completion-chat',
  verdict: 'project',
  status: 'resolved',
  subject: { name: 'Album-3' },
  outcome: {
    kind: 'project',
    workspace_id: 'album-three',
    route: '/workspaces/album-three',
    receipt: [
      { kind: 'workspace', name: 'Album-3' },
      { kind: 'folder', name: 'Album-3', detail: 'Linked where it is' },
      { kind: 'agent', name: 'Ari', assigned: true }
    ]
  }
});

async function mount(page: Page, offer: any = completed(), url = '/?panel=today') {
  const effects: string[] = [];
  await mockHiredAssistant(page);
  await page.route('**/api/**', async route => {
    const request = route.request();
    if (
      /\/(ask|route)$|\/folder-context\/(review|select)|\/folder-digest\/offers\/.*\/(setup|decide|resolve|first-look|prerequisites)|\/plugins\/.*(install|enable|update)/.test(
        new URL(request.url()).pathname
      ) &&
      request.method() !== 'GET'
    ) {
      effects.push(`${request.method()} ${new URL(request.url()).pathname}`);
      return route.abort('blockedbyclient');
    }
    return route.fallback();
  });
  await page.goto(url);
  await page.waitForFunction(() =>
    Boolean((window as any).PersonalAssistantFolder?.showConversationReview)
  );
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await page.evaluate(offer => {
    const slot = document.createElement('div');
    slot.id = 'completion-fixture-slot';
    document.getElementById('personalAssistantScroll')!.append(slot);
    (window as any).PersonalAssistantFolder.showConversationReview(slot, offer);
    (window as any).PersonalAssistantTranscript.revealControl(slot, true);
  }, offer);
  return { card: page.locator('#personalAssistantFolderOffer'), effects };
}

async function redraw(page: Page, offer: any) {
  await page.evaluate(
    offer => (window as any).PersonalAssistantFolder.updateConversationReview(offer),
    offer
  );
}

const observation = {
  version: 1,
  id: 'completion-observation',
  folder: 'Album-3',
  files: 1,
  projects: [{ id: 'candidate-0', name: 'Album-3', root: true }],
  coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: false }
};

function conversationBody(offer: any = completed(), context: any = null, reviews: any = null) {
  const binding = context || { revision: 'completion-event', observation, offer_id: offer.id };
  return {
    conversation: { id: offer.conversation_id, title: 'Keep this original title' },
    messages: [
      {
        id: 'completion-event',
        role: 'folder_context',
        folder_context: { version: 1, observation, offer_id: offer.id }
      }
    ],
    saved: [],
    folder_context: binding,
    folder_reviews: reviews || { [offer.id]: offer }
  };
}

async function restore(
  page: Page,
  offer: any = completed(),
  context: any = null,
  reviews: any = null
) {
  const body = conversationBody(offer, context, reviews);
  await page.route(`**/api/home-assistant/conversations/${offer.conversation_id}`, route =>
    route.fulfill({ json: body })
  );
  await page.evaluate(
    id => (window as any).PersonalAssistantConversation.resume(id),
    offer.conversation_id
  );
}

async function evidence(page: Page, name: string) {
  if (name.startsWith('02-')) await page.setViewportSize({ width: 1440, height: 1100 });
  await page.screenshot({ path: `tasks/evidence-assistant-setup-completion/${name}.png` });
}

test.beforeEach(async ({ request }) => {
  await request.post('/api/onboarding/skip');
});

for (const theme of ['light', 'dark']) {
  test(`fixture compact receipt and inert keyboard details ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
    const { card, effects } = await mount(page);
    await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
      '✓ Album-3 is ready'
    );
    await expect(card.locator('#personalAssistantFolderCompletionFacts')).toHaveText(
      'Folder linked · Ari assigned'
    );
    await expect(card.locator('.folder-visual')).toBeHidden();
    await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
    await expect(card.getByRole('link', { name: 'Open workspace', exact: true })).toHaveAttribute(
      'href',
      '/workspaces/album-three'
    );
    const details = card.getByText('Setup details', { exact: true });
    await details.focus();
    await page.keyboard.press('Enter');
    await expect(card.locator('#personalAssistantFolderReceipt')).toBeVisible();
    await redraw(page, completed());
    await expect(details).toBeFocused();
    await expect(card.locator('#personalAssistantFolderDetails')).toHaveAttribute('open', '');
    expect(effects).toEqual([]);
    expect(await card.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await evidence(page, `01-expanded-${theme}`);
    await details.press('Enter');
    await evidence(page, `01-compact-${theme}`);
  });
}

test('fixture completed Home and group retain canonical actions', async ({ page }) => {
  const home: any = completed();
  home.outcome = {
    kind: 'home',
    receipt: [
      { kind: 'home', name: 'Music Home', route: '/workspaces/music/assistant#projectLibraryPanel' }
    ]
  };
  const { card, effects } = await mount(page, home);
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
    '✓ Music Home is ready'
  );
  await expect(card.getByRole('link', { name: 'Open Music Home', exact: true })).toHaveAttribute(
    'href',
    '/workspaces/music/assistant#projectLibraryPanel'
  );
  await evidence(page, '01-home');
  const group: any = completed();
  group.outcome.kind = 'group';
  group.outcome.receipt = [
    { kind: 'workspace', name: 'One', route: '/workspaces/one' },
    { kind: 'workspace', name: 'Two', route: '/workspaces/two' }
  ];
  await redraw(page, group);
  await expect(card.locator('#personalAssistantFolderCompletionFacts')).toHaveText('2 workspaces');
  await revealFolderSetupDetails(card);
  await expect(card.getByRole('link', { name: 'Two', exact: true })).toHaveAttribute(
    'href',
    '/workspaces/two'
  );
  expect(effects).toEqual([]);
});

test('fixture deleted and awaiting-receipt setup cannot become ready', async ({ page }) => {
  const gone = { ...completed(), workspace_gone: 'trashed' };
  const { card, effects } = await mount(page, gone);
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText('was deleted');
  await expect(card.getByRole('button', { name: 'Restore Album-3', exact: true })).toBeVisible();
  await expect(card.getByRole('link', { name: 'Open workspace', exact: true })).toHaveCount(0);
  await evidence(page, '01-deleted');
  const pending: any = {
    ...completed(),
    status: 'awaiting_outcome',
    setup: { status: 'done', lines: [] }
  };
  delete pending.outcome;
  await redraw(page, pending);
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).not.toContainText('is ready');
  await expect(card).toContainText('checking the saved result');
  expect(effects).toEqual([]);
});

test('fixture finished result appears once and opens its saved report without execution', async ({
  page
}) => {
  const offer: any = completed();
  offer.first_task = {
    state: 'finished',
    recipe: 'folder_summary',
    optional: true,
    description: 'Read the session',
    result_excerpt: '<img src=x onerror=alert(1)> Saved overview',
    next_request: 'Review the chorus',
    report_route: '/workspaces/album-three/task/summary',
    next_route: '/workspaces/album-three/plans?next=assessment',
    executor: { agent: 'Ari', model: 'Fixture model', provider: 'loopback' }
  };
  const { card, effects } = await mount(page, offer);
  const result = card.locator('#personalAssistantFolderOptionalTask');
  await expect(result.getByRole('heading', { name: 'Summary', exact: true })).toBeVisible();
  await expect(result.locator('#personalAssistantFolderTaskDisclosure')).toBeHidden();
  await expect(result.locator('#personalAssistantFolderTaskLabel')).toBeHidden();
  await expect(card.getByText(offer.first_task.result_excerpt, { exact: false })).toHaveCount(1);
  await expect(result.locator('img')).toHaveCount(0);
  await expect(result).toContainText('Review the chorus');
  await expect(result.getByRole('button')).toHaveCount(0);
  await redraw(page, offer);
  await redraw(page, offer);
  await expect(card.locator('#personalAssistantFolderFirstLookResult')).toHaveCount(1);
  await evidence(page, '02-finished');
  await page.route('**/workspaces/album-three/task/summary', route =>
    route.fulfill({ contentType: 'text/html', body: '<h1>Saved report navigation fixture</h1>' })
  );
  await result.getByRole('link', { name: 'Open summary', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Saved report navigation fixture' })
  ).toBeVisible();
  expect(effects).toEqual([]);
});

for (const state of ['seeded', 'running', 'waiting', 'failed']) {
  test(`fixture compact setup retains actionable ${state} task`, async ({ page }) => {
    const offer: any = completed();
    offer.first_task = {
      state,
      recipe: 'folder_summary',
      optional: true,
      can_start: true,
      can_retry: state === 'failed',
      description: 'Summarize this project',
      message:
        state === 'waiting'
          ? 'Which section should I read?'
          : state === 'failed'
            ? 'The summary did not finish.'
            : '',
      executor: { agent: 'Ari', model: 'Fixture model', provider: 'loopback' }
    };
    const { card, effects } = await mount(page, offer);
    await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
      '✓ Album-3 is ready'
    );
    await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
    const task = card.locator('#personalAssistantFolderOptionalTask');
    if (state === 'seeded' || state === 'failed') {
      await expect(task).toContainText('Each explicit attempt uses model tokens');
      await expect(
        task.getByRole('button', {
          name: state === 'seeded' ? 'Start summary' : 'Try again',
          exact: true
        })
      ).toBeVisible();
    } else if (state === 'running')
      await expect(task.getByRole('button', { name: 'Stop', exact: true })).toBeVisible();
    else await expect(task).toContainText('Which section should I read?');
    await evidence(page, `02-${state}`);
    expect(effects).toEqual([]);
  });
}

test('fixture ticket-only output and File Janitor preserve truthful link labels', async ({
  page
}) => {
  const offer: any = completed();
  offer.first_task = { state: 'finished', ticket_route: '/workspaces/album-three?ticket=legacy' };
  const { card, effects } = await mount(page, offer);
  await expect(card.getByRole('link', { name: 'Open task', exact: true })).toBeVisible();
  await expect(card.getByRole('link', { name: /summary|report/i })).toHaveCount(0);
  delete offer.first_task;
  offer.outcome.first_look = {
    batch: { outcome: 'batch', route: '/workspaces/album-three?panel=file-janitor&batch_id=one' }
  };
  await redraw(page, offer);
  await expect(card.getByRole('link', { name: 'Open workspace', exact: true })).toHaveAttribute(
    'href',
    '/workspaces/album-three'
  );
  await expect(card.getByRole('link', { name: 'Review batch', exact: true })).toHaveAttribute(
    'href',
    '/workspaces/album-three?panel=file-janitor&batch_id=one'
  );
  await expect(card.locator('#personalAssistantFolderOptionalTask')).toBeHidden();
  expect(effects).toEqual([]);
});

test('fixture completed composer preserves draft, ignores empty Send and routes only typed follow-up', async ({
  page
}) => {
  const offer: any = completed();
  offer.first_task = {
    state: 'failed',
    recipe: 'folder_summary',
    optional: true,
    message: 'Summary unavailable'
  };
  const { card, effects } = await mount(page, offer);
  const input = page.locator('#personalAssistantInput');
  const draft = '  界 🎼 Exact\n draft  ';
  await input.fill(draft);
  await restore(page, offer);
  await expect(input).toHaveValue(draft);
  await expect(input).toHaveAttribute('placeholder', 'What would you like to do next?');
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Album-3');
  await expect(page.locator('#personalAssistantConversationTitle')).toHaveText(
    'Keep this original title'
  );
  for (const value of ['', '  \n\t ']) {
    await input.fill(value);
    await page.locator('#personalAssistantSend').click();
    await expect(input).toHaveValue(value);
  }
  expect(effects).toEqual([]);
  const sent: any[] = [];
  await page.route('**/api/home-assistant/route', route => {
    sent.push({ stage: 'route', ...route.request().postDataJSON() });
    return route.fulfill({
      json: {
        intent: 'assistant_conversation',
        route_mode: 'home_inline',
        target_surface: 'current'
      }
    });
  });
  await page.route('**/api/home-assistant/ask', route => {
    sent.push({ stage: 'ask', ...route.request().postDataJSON() });
    return route.fulfill({
      json: {
        response: 'Controlled follow-up fixture response.',
        intent: 'assistant_conversation',
        conversation: {
          id: offer.conversation_id,
          stored: true,
          title: 'Keep this original title'
        },
        folder_context: conversationBody(offer).folder_context,
        folder_reviews: { [offer.id]: offer }
      }
    });
  });
  await input.fill('What should we discuss next?');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => sent.filter(row => row.stage === 'ask').length).toBe(1);
  const ask = sent.find(row => row.stage === 'ask');
  expect(ask.prompt).toBe('What should we discuss next?');
  expect(ask.folder_context.selection_id).toBe(observation.id);
  expect(ask.conversation.id).toBe(offer.conversation_id);
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Controlled follow-up fixture response.'
  );
  await page.setViewportSize({ width: 1440, height: 1100 });
  await evidence(page, '03-composer');
  await page.route('**/workspaces/album-three', route =>
    route.fulfill({
      contentType: 'text/html',
      body: '<h1>Existing workspace navigation fixture</h1>'
    })
  );
  await card.getByRole('link', { name: 'Open workspace', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Existing workspace navigation fixture' })
  ).toBeVisible();
  expect(effects).toEqual([]);
});

test('fixture replacement, detach, New and a newer review revoke composer completion', async ({
  page
}) => {
  await mount(page);
  await restore(page);
  const input = page.locator('#personalAssistantInput');
  await expect(input).toHaveAttribute('placeholder', 'What would you like to do next?');
  await page.evaluate(() => {
    const app = window as any;
    const context = app.PersonalAssistantFolderContext;
    context._controller().state.preview = true;
    app.PersonalAssistantFolderSetup.contextChanged(context.current());
  });
  await expect(input).toHaveAttribute('placeholder', 'Explore this folder');
  const newer = { ...completed(), id: 'new-pending', status: 'pending', outcome: null };
  await restore(page, newer, null, { [completed().id]: completed(), [newer.id]: newer });
  await page.evaluate(
    offer =>
      document.dispatchEvent(
        new CustomEvent('personal-assistant:folder-offer', { detail: { offer } })
      ),
    completed()
  );
  await expect(input).toHaveAttribute('placeholder', 'Explore this folder');
  await page.evaluate(() => {
    const app = window as any;
    app.PersonalAssistantFolderContext.hydrate('completion-chat', { revision: 'detached' });
  });
  await expect(input).not.toHaveAttribute('placeholder', 'What would you like to do next?');
  await page.evaluate(() => (window as any).PersonalAssistantConversation.startNew());
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(input).not.toHaveAttribute('placeholder', 'What would you like to do next?');
});

for (const url of ['/?panel=today', '/settings?panel=today']) {
  test(`fixture replay and detached historical receipt stay compact and inert on ${url}`, async ({
    page
  }) => {
    const offer: any = completed();
    offer.first_task = {
      state: 'finished',
      result_excerpt: 'Saved historical result',
      report_route: '/workspaces/album-three/task/result'
    };
    const { card, effects } = await mount(page, offer, url);
    await restore(page, offer);
    await revealFolderSetupDetails(card);
    await page.locator('#personalAssistantClose').click();
    await page.locator('#personalAssistantLauncher').click();
    await expect(card.locator('#personalAssistantFolderDetails')).toHaveAttribute('open', '');
    await page.reload();
    await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
      '✓ Album-3 is ready'
    );
    await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
    await page.evaluate(() =>
      (window as any).PersonalAssistantFolderContext.hydrate('completion-chat', {
        revision: 'detached'
      })
    );
    const historical = page.locator('[data-saved-completion="completion-fixture"]');
    await expect(historical).toBeVisible();
    await expect(historical.getByText('Saved historical result', { exact: true })).toHaveCount(1);
    await expect(historical.getByRole('button')).toHaveCount(0);
    const details = historical.locator('details').first();
    await expect(details).not.toHaveAttribute('open', '');
    const summary = details.locator('summary').first();
    await summary.focus();
    await page.keyboard.press('Enter');
    await page.evaluate(offer => {
      document.dispatchEvent(
        new CustomEvent('personal-assistant:folder-offer', { detail: { offer } })
      );
    }, offer);
    await expect(summary).toBeFocused();
    await expect(details).toHaveAttribute('open', '');
    await page.setViewportSize({ width: 1440, height: 1100 });
    await evidence(page, url.startsWith('/settings') ? '04-settings-history' : '04-home-history');
    expect(effects).toEqual([]);
  });
}

test('fixture late first-task read from A cannot replace B or its exact draft', async ({
  page
}) => {
  const a: any = completed();
  a.first_task = { state: 'running', offer_id: a.id, recipe: 'folder_summary', optional: true };
  const { card, effects } = await mount(page, a);
  await restore(page, a);
  let release!: () => void;
  let reads = 0;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route(
    '**/api/personal-assistant/folder-digest?offer_id=completion-fixture',
    async route => {
      reads++;
      await held;
      await route.fulfill({
        json: {
          folder_digest: {
            offer: {
              ...a,
              first_task: { ...a.first_task, state: 'finished', result_excerpt: 'Late A result' }
            }
          }
        }
      });
    }
  );
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await expect.poll(() => reads).toBeGreaterThan(0);
  const b = {
    ...completed(),
    id: 'review-b',
    conversation_id: 'chat-b',
    status: 'pending',
    outcome: null,
    subject: { name: 'New B project' }
  };
  await restore(page, b);
  const input = page.locator('#personalAssistantInput');
  const draft = '  B 界 draft\n stays exact  ';
  await input.fill(draft);
  const response = page.waitForResponse(
    '**/api/personal-assistant/folder-digest?offer_id=completion-fixture'
  );
  release();
  await (await response).finished();
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(draft);
  await expect(input).toHaveAttribute('placeholder', 'Explore this folder');
  await expect(card).toContainText('New B project');
  await expect(page.locator('#homeAssistantConversation')).not.toContainText('Late A result');
  expect(await page.evaluate(() => (window as any).PersonalAssistantFolder.current().id)).toBe(
    'review-b'
  );
  await evidence(page, '04-late-a');
  expect(effects).toEqual([]);
});

test('fixture task completion while typing preserves expansion, focus and saved output once', async ({
  page
}) => {
  const offer: any = completed();
  offer.first_task = { state: 'running', recipe: 'folder_summary', optional: true };
  const { card, effects } = await mount(page, offer);
  await restore(page, offer);
  await revealFolderSetupDetails(card);
  const input = page.locator('#personalAssistantInput');
  await input.fill('  Still typing 🎼  ');
  offer.first_task = {
    ...offer.first_task,
    state: 'finished',
    result_excerpt: 'One new saved result'
  };
  await page.evaluate(offer => {
    for (let n = 0; n < 3; n++)
      document.dispatchEvent(
        new CustomEvent('personal-assistant:folder-offer', { detail: { offer } })
      );
  }, offer);
  await expect(input).toBeFocused();
  await expect(input).toHaveValue('  Still typing 🎼  ');
  await expect(card.locator('#personalAssistantFolderDetails')).toHaveAttribute('open', '');
  await expect(card.getByText('One new saved result', { exact: true })).toHaveCount(1);
  await evidence(page, '04-focused-expanded');
  expect(effects).toEqual([]);
});

test('fixture first completion never collapses focused evidence', async ({ page }) => {
  const waiting: any = {
    ...completed(),
    status: 'awaiting_outcome',
    setup: { status: 'running', lines: [] }
  };
  delete waiting.outcome;
  const { card, effects } = await mount(page, waiting);
  await revealFolderSetupDetails(card);
  const summary = card.locator('#personalAssistantFolderDetails > summary');
  await summary.focus();
  await redraw(page, completed());
  await expect(summary).toBeFocused();
  await expect(card.locator('#personalAssistantFolderDetails')).toHaveAttribute('open', '');
  await redraw(page, completed());
  await expect(summary).toBeFocused();
  expect(effects).toEqual([]);
});
