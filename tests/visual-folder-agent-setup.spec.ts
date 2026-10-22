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

test.beforeEach(async ({ request }) => {
  await request.post('/api/onboarding/skip');
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
