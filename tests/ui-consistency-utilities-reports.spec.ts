import { test, expect, type Page, type TestInfo } from '@playwright/test';
import { readFileSync } from 'node:fs';
import {
  assertSandbox,
  preparePage,
  guardReadTour,
  capture as captureSurface,
  reachable,
  contrast
} from './helpers/ui-consistency';
import {
  usageSummaryScene,
  usageDetailsScene,
  reviewIssuesScene,
  reviewRunsScene,
  themes
} from './fixtures/ui-consistency-scenes';

async function capture(page: Page, info: TestInfo, name: string) {
  await captureSurface(
    page,
    info,
    name,
    { fixture: 'fictional recorded usage/issues/runs; never a live model review' },
    { fullPage: false }
  );
}
async function reportReads(page: Page, state = () => 'populated') {
  await page.route('**/api/usage/**', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const url = new URL(route.request().url());
    if (!['/api/usage/summary', '/api/usage/stats/all'].includes(url.pathname) || url.search)
      return route.abort('blockedbyclient');
    if (state() === 'error')
      return route.fulfill({ status: 503, body: 'Fictional usage unavailable' });
    const empty = state() === 'empty';
    if (url.pathname.endsWith('/summary'))
      return route.fulfill({
        json: empty ? { today: {}, this_month: {}, all_time: {} } : usageSummaryScene
      });
    return route.fulfill({ json: empty ? {} : usageDetailsScene });
  });
  await page.route('**/api/agents', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    if (state() === 'error')
      return route.fulfill({ status: 503, body: 'Fictional agent list unavailable' });
    return route.fulfill({
      json: { agents: reviewIssuesScene.issues.map(issue => issue.agent_name) }
    });
  });
  await page.route('**/api/review/**', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const url = new URL(route.request().url());
    // Export is a reviewed storage read, not a review trigger. Leave it real.
    if (url.pathname === '/api/review/export') return route.fallback();
    if (!['/api/review/issues', '/api/review/runs'].includes(url.pathname))
      return route.abort('blockedbyclient');
    if (state() === 'error')
      return route.fulfill({ status: 503, body: 'Fictional stored review unavailable' });
    if (url.pathname.endsWith('/runs'))
      return route.fulfill({ json: state() === 'empty' ? { runs: [] } : reviewRunsScene });
    const agent = url.searchParams.get('agent_name');
    const type = url.searchParams.get('issue_type');
    return route.fulfill({
      json: {
        issues:
          state() === 'empty'
            ? []
            : reviewIssuesScene.issues.filter(
                issue =>
                  (!agent || issue.agent_name === agent) && (!type || issue.issue_type === type)
              )
      }
    });
  });
}
test.beforeAll(async ({ request }) => {
  await assertSandbox(request);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
});

for (const theme of themes) {
  for (const width of [320, 390, 768, 1440]) {
    test(`Usage precise summaries, readable long breakdowns and bounded table: ${theme} ${width}`, async ({
      page
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await reportReads(page);
      await page.goto('/usage');
      await expect(page.locator('#usageRefreshBtn')).toBeEnabled();
      await expect(page.locator('#todayStats')).toContainText('$12.3456');
      await expect(page.locator('#allTimeStats')).toContainText('123,456,789');
      await reachable(page.locator('#usageRefreshBtn'), width < 768 ? 44 : 0);
      const value = page.locator('#todayStats .ui-metric-value');
      expect((await contrast(value)).ratio).toBeGreaterThanOrEqual(4.5);
      expect(
        (await contrast(page.locator('#providerStats .usage-cost'))).ratio
      ).toBeGreaterThanOrEqual(4.5);
      for (const panel of await page.locator('.ui-metric').all()) {
        const box = await panel.boundingBox();
        expect(box!.x).toBeGreaterThanOrEqual(0);
        expect(box!.x + box!.width).toBeLessThanOrEqual(width);
      }
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
      await capture(page, info, 'usage-summary');
      const table = page.getByRole('region', { name: 'Usage by model', exact: true });
      await reachable(table);
      await expect(table.getByRole('rowheader')).toHaveText(
        usageDetailsScene.by_model.fictional.model
      );
      expect(await table.evaluate(node => node.scrollWidth)).toBeGreaterThanOrEqual(670);
      // Reach the region through actual Tab navigation. A programmatically
      // focused div need not become WebKit's native keyboard scroll target.
      await page.locator('#usageRefreshBtn').focus();
      await page.keyboard.press('Tab');
      await expect(table).toBeFocused();
      if (width < 768) {
        // WebKit schedules native scrolling between keydown and keyup; an
        // instantaneous synthetic tap can end before its first scroll frame.
        await page.keyboard.press('ArrowRight', { delay: 100 });
        await expect.poll(() => table.evaluate(node => node.scrollLeft)).toBeGreaterThan(0);
      }
      await capture(page, info, 'usage-models');
      expect(writes).toEqual([]);
      expect(errors).toEqual([]);
    });

    test(`Review native filters, full context, export and confirmation cancellation: ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      await preparePage(page, theme, width);
      const writes = await guardReadTour(page);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await reportReads(page);
      await page.goto('/review');
      await expect(page.locator('#issuesTable tbody tr')).toHaveCount(2);
      await expect(page.locator('#runsTable tbody tr')).toHaveCount(2);
      await reachable(page.locator('#triggerBtn'), width < 768 ? 44 : 0);
      await capture(page, info, 'review-summary');
      for (const selector of [
        '#totalIssuesCard .review-warning',
        '#totalIssuesCard .review-danger',
        '#lastReviewCard .badge'
      ]) {
        expect((await contrast(page.locator(selector).first())).ratio).toBeGreaterThanOrEqual(4.5);
      }
      const issuesRegion = page.getByRole('region', { name: 'Detected conversation issues' });
      await reachable(issuesRegion);
      await issuesRegion.evaluate(node => (node.scrollLeft = 500));
      const context = page.locator('#issuesTable details').first();
      await context.locator('summary').focus();
      await page.keyboard.press('Enter');
      await expect(context.locator('p')).toHaveText(reviewIssuesScene.issues[0].context_summary);
      await capture(page, info, 'review-context');
      await page.locator('#filterAgent').selectOption(reviewIssuesScene.issues[0].agent_name);
      const filterResponse = page.waitForRequest(
        req =>
          req.url().includes('/api/review/issues?') &&
          new URL(req.url()).searchParams.get('issue_type') === 'user_retry'
      );
      await page.locator('#filterType').selectOption('user_retry');
      const query = new URL((await filterResponse).url()).searchParams;
      expect(query.get('agent_name')).toBe(reviewIssuesScene.issues[0].agent_name);
      expect(query.get('limit')).toBe('100');
      await expect(page.locator('#issuesTable tbody tr')).toHaveCount(1);
      const exportMenu = page.locator('#reviewExportMenu');
      await exportMenu.locator('summary').focus();
      await page.keyboard.press('Enter');
      for (const [format, id] of [
        ['json', 'reviewExportJSON'],
        ['jsonl', 'reviewExportJSONL']
      ]) {
        const link = page.locator(`#${id}`);
        const href = await link.getAttribute('href');
        const url = new URL(href!, page.url());
        expect(url.pathname).toBe('/api/review/export');
        expect(url.searchParams.get('format')).toBe(format);
        expect(url.searchParams.get('agent_name')).toBe(reviewIssuesScene.issues[0].agent_name);
        expect(url.searchParams.get('issue_type')).toBe('user_retry');
        await reachable(link, width < 768 ? 44 : 0);
        // The real sandbox store has no issues for this fictional agent. These
        // downloads prove the native storage/export contract, not fixture rows.
        const real = await request.get(
          '/api/review/issues?' +
            new URLSearchParams({
              agent_name: reviewIssuesScene.issues[0].agent_name,
              issue_type: 'user_retry'
            })
        );
        expect(real.ok()).toBeTruthy();
        expect((await real.json()).issues).toEqual([]);
        const downloaded = page.waitForEvent('download');
        await link.focus();
        await page.keyboard.press('Enter');
        const file = await downloaded;
        expect(file.suggestedFilename()).toBe(`review_issues.${format}`);
        const content = readFileSync((await file.path())!, 'utf8');
        expect(format === 'json' ? JSON.parse(content) : content).toEqual(
          format === 'json' ? [] : ''
        );
      }
      await page.keyboard.press('Escape');
      await expect(exportMenu.locator('summary')).toBeFocused();
      await expect(exportMenu).not.toHaveAttribute('open', '');
      await page.locator('#triggerBtn').click();
      const modal = page.locator('#triggerModal');
      await expect(modal).toHaveAttribute('aria-modal', 'true');
      await expect(page.locator('#triggerSensitivity')).toBeFocused();
      await page.locator('#triggerSensitivity').selectOption('high');
      await reachable(
        modal.getByRole('button', { name: 'Cancel', exact: true }),
        width < 768 ? 44 : 0
      );
      await capture(page, info, 'review-confirmation');
      await page.keyboard.press('Escape');
      await expect(modal).toBeHidden();
      await expect(page.locator('#triggerBtn')).toBeFocused();
      await page.locator('#triggerBtn').click();
      await expect(page.locator('#triggerSensitivity')).toHaveValue('high');
      await modal.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(page.locator('#triggerBtn')).toBeFocused();
      expect(writes).toEqual([]);
      expect(errors).toEqual([]);
    });
  }

  test(`Usage/Review explicit failed reads, refresh recovery and consolidated emptiness: ${theme}`, async ({
    page
  }, info) => {
    await preparePage(page, theme, 390);
    const writes = await guardReadTour(page);
    let state = 'error';
    await reportReads(page, () => state);
    await page.goto('/usage');
    await expect(page.locator('#usageFeedback')).toContainText('Use Refresh');
    await expect(page.locator('#todayStats')).toHaveText('Unavailable');
    await expect(page.locator('#usageEmpty')).toBeHidden();
    await capture(page, info, 'usage-error');
    state = 'empty';
    await page.locator('#usageRefreshBtn').click();
    await expect(page.locator('#usageEmpty')).toBeVisible();
    await expect(page.locator('#usageDetails')).toBeHidden();
    await expect(page.locator('#todayStats')).toContainText('$0.0000');
    await expect(page.locator('#usageFeedback')).toBeHidden();
    await capture(page, info, 'usage-empty');
    state = 'error';
    await page.goto('/review');
    await expect(page.locator('#issuesTable [role="alert"]')).toContainText('Use Refresh');
    await expect(page.locator('#totalIssuesCard')).toHaveText('Unavailable');
    await expect(page.locator('#lastReviewCard')).toHaveText('Unavailable');
    await expect(page.locator('#reviewAgentFeedback')).toContainText('Agent list unavailable');
    await capture(page, info, 'review-error');
    state = 'empty';
    await page.getByRole('button', { name: 'Refresh', exact: true }).click();
    await expect(page.locator('#issuesTable')).toContainText('No issues match');
    await expect(page.locator('#runsTable')).toContainText('No review runs yet');
    await expect(page.locator('#totalIssuesCard .ui-metric-value')).toHaveText('0');
    await expect(page.locator('#reviewAgentFeedback')).toBeHidden();
    await capture(page, info, 'review-empty');
    expect(writes).toEqual([]);
  });
}
