import { expect, test, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Browser component checks, not an authenticated-server or live-model demo.
// The production module and Bootstrap utilities run against synthetic history;
// every request is intercepted. No application state or provider is contacted.
const origin = 'http://arrangement.test';
const summary = () => ({
  kind: 'proposal',
  catalog_current: false,
  arrangement: {
    id: 'saved-record',
    revision: 1,
    state: 'draft',
    origin: 'model',
    created_at: '2026-02-01T12:00:00Z',
    goal: 'Keep experiments catalog-only; group the selected client songs.',
    rationale: 'Collection membership is independent of workspace activation.',
    unknowns: ['The authoritative version is unknown.'],
    operations: 1,
    proposed_operations: [
      { kind: 'create_collection', name: '<img src=x onerror=alert(1)>', members: 1000 }
    ],
    explicit_unchanged: 20,
    explicit_needs_review: 1,
    review_questions: [{ reason: 'Which version should be used?', members: 1 }],
    coverage: {
      partial: true,
      note: 'Saved catalog metadata only.',
      max_depth: 3,
      tree_omitted: 12
    },
    review_available: false
  }
});

async function openHarness(page: Page) {
  const script = await readFile('internal/web/static/js/modules/workspace-arrangement.js', 'utf8');
  const consent = await readFile(
    'internal/web/static/js/modules/workspace-arrangement-consent.js',
    'utf8'
  );
  const css =
    (await readFile('node_modules/bootstrap/dist/css/bootstrap.min.css', 'utf8')) +
    '\n' +
    (await readFile('internal/web/static/css/workspace-arrangement.css', 'utf8'));
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname === '/consent.js')
      return route.fulfill({ contentType: 'text/javascript', body: consent });
    if (['/module.js', '/workspace-arrangement.js'].includes(url.pathname))
      return route.fulfill({ contentType: 'text/javascript', body: script });
    if (url.pathname === '/style.css') return route.fulfill({ contentType: 'text/css', body: css });
    if (url.pathname === '/')
      return route.fulfill({
        contentType: 'text/html',
        body: `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/style.css"><title>Arrangement history component fixture</title></head><body><main class="container p-3" style="max-width:760px"><h1 class="h4">Saved arrangements</h1><p>Component fixture — no live model or application state.</p><div id="history"></div><button id="next" type="button">After history</button></main><script type="module">import * as ui from '/module.js'; window.historyUI = ui; window.contextCurrent = true;</script></body></html>`
      });
    return route.abort();
  });
  await page.goto(origin);
  await page.waitForFunction(() => Boolean((window as any).historyUI));
}

async function mount(page: Page) {
  await page.evaluate(() => {
    const host = window as any;
    host.historyUI.mountArrangementHistory(document.getElementById('history'), {
      id: 'saved-record',
      load: () => fetch('/history').then(response => response.json()),
      isCurrent: () => host.contextCurrent
    });
  });
}

test('a redacted Home receipt stays a receipt without offering another apply', async ({
  page
}, info) => {
  await openHarness(page);
  const history = summary();
  history.arrangement.state = 'stale';
  history.arrangement.goal = 'Catalog history was forgotten. Request a new arrangement.';
  history.arrangement.rationale = '';
  history.arrangement.operations = 0;
  history.arrangement.proposed_operations = [];
  history.arrangement.unknowns = [];
  history.arrangement.review_questions = [];
  history.arrangement.explicit_unchanged = 0;
  history.arrangement.explicit_needs_review = 0;
  const saved = {
    ...history,
    collection_receipt: {
      digest: 'a'.repeat(64),
      catalog_revision: 3,
      applied_at: '2026-02-02T12:00:00Z',
      effects: [
        {
          kind: 'create_collection',
          collection_id: 'collection-id',
          before_revision: 0,
          after_revision: 1
        }
      ]
    }
  };
  await page.route(`${origin}/history`, route => route.fulfill({ json: saved }));
  await mount(page);
  await page.getByRole('button', { name: 'Open saved arrangement', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Saved collection metadata receipt' })
  ).toBeVisible();
  await expect(page.locator('#history')).toContainText(
    'Canonical receipt: 1 logical-collection metadata change(s)'
  );
  await expect(page.locator('#history')).not.toContainText('not applied');
  await expect(
    page.locator('#history').getByRole('button', { name: /apply|confirm/i })
  ).toHaveCount(0);
  await page.screenshot({
    path: info.outputPath('redacted-collection-receipt.png'),
    fullPage: true
  });
});

test('keyboard opens inert history and every intention, without an HTML injection or mutation', async ({
  page
}, info) => {
  await openHarness(page);
  let reads = 0;
  const history = summary();
  history.arrangement.operations = 32;
  history.arrangement.proposed_operations = Array.from({ length: 32 }, (_, index) => ({
    ...history.arrangement.proposed_operations[0],
    members: index === 0 ? 1000 : 1,
    name: `Collection ${index}: <img src=x onerror=alert(1)>`
  }));
  const methods: string[] = [];
  await page.route(`${origin}/history`, route => {
    reads++;
    methods.push(route.request().method());
    return route.fulfill({ json: history });
  });
  await mount(page);
  expect(reads).toBe(0);
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Open saved arrangement' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('heading', { name: 'Saved arrangement — not applied' })
  ).toBeVisible();
  await expect(page.getByText('Historical context has changed.', { exact: false })).toBeVisible();
  await expect(page.getByText('Recorded depth limit: 3', { exact: false })).toBeVisible();
  const disclosure = page.locator('summary').filter({ hasText: 'Proposed intentions' });
  // Focus must remain on the original trigger while its canonical read settles.
  await expect(page.getByRole('button', { name: 'Refresh saved arrangement' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(disclosure).toBeFocused();
  await page.keyboard.press('Enter');
  const intentions = page.locator('li').filter({ hasText: 'Proposed logical collection:' });
  await expect(intentions).toHaveCount(32);
  await expect(intentions.last()).toBeVisible();
  await expect(intentions.first()).toContainText('1000 member(s)');
  await expect(intentions.last()).toContainText(
    'Collection 31: <img src=x onerror=alert(1)> · 1 member(s)'
  );
  await expect(page.locator('#history img, #history a, #history input')).toHaveCount(0);
  expect(methods).toEqual(['GET']);
  await expect(page.getByRole('button', { name: /apply|confirm|activate/i })).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('history-keyboard.png'), fullPage: true });
});

test('narrow-screen long clarification is fully readable without horizontal overflow', async ({
  page
}, info) => {
  await page.setViewportSize({ width: 360, height: 740 });
  await openHarness(page);
  const history = summary();
  const clarification = 'Which version? ' + 'x'.repeat(2000) + ' END OF SAVED DISCUSSION';
  await page.evaluate(
    ({ history, clarification }) => {
      (window as any).historyUI.renderArrangementHistory(document.getElementById('history'), {
        ...history,
        kind: 'clarification',
        clarification,
        arrangement: {
          ...history.arrangement,
          state: 'needs_more_evidence',
          rationale: '',
          unknowns: [],
          operations: 0,
          proposed_operations: [],
          explicit_unchanged: 0,
          explicit_needs_review: 0,
          review_questions: []
        }
      });
    },
    { history, clarification }
  );
  const disclosure = page.locator('summary', { hasText: 'Read full saved clarification' });
  await disclosure.focus();
  await page.keyboard.press('Space');
  await expect(page.getByText(clarification, { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await expect(page.locator('#history button')).toHaveCount(0);
  await page.screenshot({
    path: info.outputPath('history-narrow-clarification.png'),
    fullPage: true
  });
});

test('a pending read rejects duplicate activation without trapping or stealing keyboard focus', async ({
  page
}) => {
  await openHarness(page);
  let reads = 0;
  let release!: () => void;
  const pending = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route(`${origin}/history`, async route => {
    reads++;
    await pending;
    await route.fulfill({ json: summary() });
  });
  await mount(page);
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(page.getByText('Reading saved history…')).toBeVisible();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'After history' })).toBeFocused();
  release();
  await expect(
    page.getByRole('heading', { name: 'Saved arrangement — not applied' })
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'After history' })).toBeFocused();
  expect(reads).toBe(1);
});

test('a context switch while reading cannot display another context or keep reporting progress', async ({
  page
}) => {
  await openHarness(page);
  let release!: () => void;
  const pending = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route(`${origin}/history`, async route => {
    await pending;
    await route.fulfill({ json: summary() });
  });
  await mount(page);
  await page.getByRole('button', { name: 'Open saved arrangement' }).click();
  await expect(page.getByText('Reading saved history…')).toBeVisible();
  await page.evaluate(() => {
    (window as any).contextCurrent = false;
  });
  release();
  await expect(page.locator('[aria-busy="false"]')).toBeVisible();
  await expect(page.getByText('Reading saved history…')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Saved arrangement — not applied' })).toHaveCount(
    0
  );
  await expect(page.getByRole('button', { name: 'Open saved arrangement' })).toBeDisabled();
});

// Send is enabled ONLY inside this component fixture. Product entrypoints remain
// gated until both real adapters are wired; no synthetic result enters the app.
test('sharing and upgrade require separate keyboard choices; Send consumes both without losing focus', async ({
  page
}, info) => {
  await openHarness(page);
  await page.evaluate(async () => {
    const module = await import('/consent.js');
    const host = window as any;
    host.sentRequests = [];
    host.consentContext = {
      kind: 'home_library',
      homeID: 'home-a',
      contextKey: 'owner/manager/runtime',
      agentName: 'Portfolio Manager',
      providerName: 'ollama',
      modelName: 'fixture-model',
      available: true,
      expectedRevision: 12,
      libraryVersion: 1,
      goal: 'Keep experiments catalog-only'
    };
    module.mountArrangementConsent(document.getElementById('history'), {
      kind: 'home_library',
      getContext: () => host.consentContext,
      enabled: true,
      onSend: async (request: unknown) => {
        host.sentRequests.push(request);
        throw new Error('/private/secret');
      }
    });
  });
  await expect(page.getByText('64 KiB', { exact: false })).toBeVisible();
  await expect(
    page.getByText('Planning recipient: Portfolio Manager · ollama / fixture-model')
  ).toBeVisible();
  const send = page.getByRole('button', { name: 'Send organizing goal' });
  await expect(send).toBeDisabled();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('checkbox').nth(0)).toBeFocused();
  await page.keyboard.press('Space');
  await expect(send).toBeDisabled();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('checkbox').nth(1)).toBeFocused();
  await page.keyboard.press('Space');
  await expect(send).toBeEnabled();
  await page.keyboard.press('Tab');
  await expect(send).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('status')).toContainText('Reopen saved history');
  await expect(send).toBeFocused();
  await expect(send).toBeDisabled();
  expect(await send.evaluate(element => getComputedStyle(element).outlineStyle)).toBe('solid');
  expect(await send.evaluate(element => getComputedStyle(element).outlineWidth)).toBe('2px');
  await expect(page.getByRole('checkbox').nth(0)).not.toBeChecked();
  await expect(page.getByRole('checkbox').nth(1)).not.toBeChecked();
  expect(await page.evaluate(() => (window as any).sentRequests)).toEqual([
    {
      goal: 'Keep experiments catalog-only',
      expected_revision: 12,
      share_catalog_metadata: true,
      allow_upgrade: true
    }
  ]);
  await expect(page.getByText('/private/secret', { exact: false })).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('consent-uncertain-save.png'), fullPage: true });
});
