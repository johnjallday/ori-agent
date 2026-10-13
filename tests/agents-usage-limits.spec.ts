import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { mockHiredAssistant } from './helpers/hired-assistant';

const provider = (level = 'ok') => ({
  provider: 'codex',
  display_name: 'Codex',
  state: 'ok',
  level,
  plan: 'prolite',
  fetched_at: new Date().toISOString(),
  source: 'codex_session_log',
  detail: '',
  windows: [
    {
      id: 'weekly',
      label: 'Weekly',
      window_minutes: 10080,
      used_percent: level === 'ok' ? 16 : level === 'warn' ? 85 : 100,
      resets_at: Math.floor(Date.now() / 1000) + 187200,
      level
    }
  ]
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await mockHiredAssistant(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
  // The usage UI contract is independent of whether CI has CLI binaries.
  await page.route('**/api/agents/dashboard/list**', route =>
    route.fulfill({
      json: {
        agents: [
          { name: 'Codex', source: 'cli', role: 'cli_agent', status: 'active' },
          { name: 'Gemini CLI', source: 'cli', role: 'cli_agent', status: 'active' }
        ]
      }
    })
  );
  await page.route('**/api/agents/*/detail', route =>
    route.fulfill({ json: { name: 'Codex', provider: 'codex', model: 'codex' } })
  );
});

for (const level of ['ok', 'warn', 'exhausted']) {
  test(`${level} pill selects the CLI inspector and exposes reset and source`, async ({ page }) => {
    await page.route('**/api/usage/limits', route =>
      route.fulfill({ json: { providers: [provider(level)] } })
    );
    await page.goto('/agents');
    const pill = page.locator(`.roster-limit--${level}`);
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(
      level === 'exhausted' ? 'limit reached' : level === 'warn' ? '85%' : '16%'
    );
    if (level === 'warn') await expect(pill.locator('strong')).toHaveText('85%');
    await expect(pill).toHaveAttribute('aria-label', /Weekly.*resets in 2d 4h.*session log/);
    await pill.click();
    await expect(page.locator('#stageName')).toHaveText('Codex');
    await expect(page.locator('#stageProgression')).toContainText('Usage limits');
    await expect(page.locator('#stageProgression')).toContainText('session log');
    await expect(page.getByRole('meter', { name: 'Weekly usage' })).toBeVisible();
    await page.locator('.roster-card[data-name="Gemini CLI"] .roster-card__open').click();
    await expect(page.locator('#stageProgression')).toBeEmpty();
  });
}

for (const theme of ['light', 'dark']) {
  test(`pills and usage meters are accessible (${theme})`, async ({ page }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.route('**/api/usage/limits', route =>
      route.fulfill({ json: { providers: [provider('warn')] } })
    );
    await page.goto('/agents?agent=Codex');
    await expect(page.getByRole('meter')).toBeVisible();
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const violations = await page.evaluate(async () => {
      const result = await (window as any).axe.run(document.querySelector('.roster-layout'), {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
      });
      return result.violations;
    });
    expect(violations).toEqual([]);
  });
}

test('no providers hides the entire group', async ({ page }) => {
  await page.route('**/api/usage/limits', route => route.fulfill({ json: { providers: [] } }));
  await page.goto('/agents');
  await expect(page.locator('#providerLimits')).toBeHidden();
  await expect(page.locator('#providerLimits')).toHaveAttribute('hidden', '');
});

test('signed out has no pill and shows the login instruction in the inspector', async ({
  page
}) => {
  await page.route('**/api/usage/limits', route =>
    route.fulfill({
      json: {
        providers: [
          { ...provider(), state: 'not_signed_in', windows: [], detail: 'Run `codex login`' }
        ]
      }
    })
  );
  await page.goto('/agents?agent=Codex');
  await expect(page.locator('#providerLimits')).toBeHidden();
  await expect(page.locator('#stageProgression')).toContainText('Run `codex login`');
  await expect(page.getByRole('meter')).toHaveCount(0);
});

test('pills and meters fit a 375px screen', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  const claude = {
    ...provider('warn'),
    provider: 'claude_code',
    display_name: 'Claude Code',
    windows: [
      {
        ...provider().windows[0],
        id: 'five_hour',
        label: '5-hour',
        used_percent: 85,
        level: 'warn'
      },
      provider().windows[0]
    ]
  };
  await page.route('**/api/usage/limits', route =>
    route.fulfill({ json: { providers: [provider(), claude] } })
  );
  await page.goto('/agents');
  await expect(page.locator('.roster-limit')).toHaveCount(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.locator('.roster-limit').first().click();
  await expect(page.getByRole('meter')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
