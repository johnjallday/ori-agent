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
    route.fulfill({
      json: {
        name: 'Codex',
        provider: 'codex',
        role: 'cli_agent',
        model: 'codex',
        codex_sync: { config: { model: 'codex' } }
      }
    })
  );
});

for (const level of ['ok', 'warn', 'exhausted']) {
  test(`${level} pill selects the CLI inspector and exposes reset and source`, async ({ page }) => {
    await page.route('**/api/usage/limits*', route =>
      route.fulfill({ json: { providers: [provider(level)] } })
    );
    await page.goto('/agents');
    const pill = page.locator(`.roster-limit--${level}`);
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(
      level === 'exhausted' ? 'limit reached' : level === 'warn' ? '85%' : '16%'
    );
    if (level === 'warn') await expect(pill.locator('strong')).toHaveText('85% used');
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
    await page.route('**/api/usage/limits*', route =>
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

test('inspector Refresh requests live data and updates pills without replacing cards', async ({
  page
}) => {
  let reads = 0;
  await page.route('**/api/usage/limits*', async route => {
    expect(route.request().url()).toContain('refresh=1');
    const live = ++reads > 1;
    await route.fulfill({
      json: {
        providers: [
          {
            ...provider(live ? 'warn' : 'ok'),
            source: live ? 'codex_app_server' : 'codex_session_log'
          }
        ]
      }
    });
  });
  await page.goto('/agents?agent=Codex');
  await expect(page.getByRole('meter')).toHaveAttribute('aria-valuenow', '16');
  await page.locator('#rosterList').evaluate(element => element.setAttribute('data-kept', 'yes'));
  await page.locator('#stageProgression [data-limits-refresh]').click();
  await expect(page.getByRole('meter')).toHaveAttribute('aria-valuenow', '85');
  await expect(page.locator('.roster-limit--warn')).toContainText('85%');
  await expect(page.locator('#stageProgression')).toContainText('Codex app server');
  await expect(page.locator('#rosterList')).toHaveAttribute('data-kept', 'yes');
  await expect(page.locator('#stageProgression [data-limits-refresh]')).toBeFocused();
});

test('Codex detail card shows credits and both refresh controls fetch live limits', async ({
  page
}) => {
  await page.clock.install();
  let refreshes = 0;
  await page.route('**/api/usage/limits*', route => {
    if (route.request().url().includes('refresh=1')) refreshes++;
    return route.fulfill({
      json: {
        providers: [
          { ...provider(), credits: { has_credits: false, unlimited: false, balance: '0' } }
        ]
      }
    });
  });
  await page.route('**/api/external-agents/refresh', route =>
    route.fulfill({ json: { success: true } })
  );
  await page.goto('/agents/codex');
  const card = page.locator('[data-provider-limits]');
  await expect(card).toContainText('Usage limits');
  await expect(card).toContainText('Credits: none');
  await expect(card.getByRole('meter')).toBeVisible();
  await expect.poll(() => refreshes).toBe(1); // Live on page open.
  await card.locator('[data-limits-refresh]').click();
  await expect.poll(() => refreshes).toBe(2);
  await expect(card.locator('[data-limits-refresh]')).toBeEnabled();
  await page.getByRole('button', { name: 'Refresh from ~/.codex' }).click();
  await expect.poll(() => refreshes).toBe(4); // Explicit refresh, then remounted card.
  await page.clock.fastForward(180000);
  await expect.poll(() => refreshes).toBe(5); // Old mount must not keep polling.
});

test('detail refresh during initial load is queued rather than dropped', async ({ page }) => {
  let release: () => void;
  let initialStarted: () => void;
  const waiting = new Promise<void>(resolve => {
    release = resolve;
  });
  const started = new Promise<void>(resolve => {
    initialStarted = resolve;
  });
  let refreshes = 0;
  await page.route('**/api/usage/limits*', async route => {
    expect(route.request().url()).toContain('refresh=1');
    refreshes++;
    if (refreshes === 1) {
      initialStarted();
      await waiting;
    }
    await route.fulfill({ json: { providers: [provider()] } });
  });
  await page.route('**/api/external-agents/refresh', route =>
    route.fulfill({ json: { success: true } })
  );
  await page.goto('/agents/codex');
  await started;
  await page.getByRole('button', { name: 'Refresh from ~/.codex' }).click();
  release!();
  await expect.poll(() => refreshes).toBe(3); // Initial, queued explicit, remounted card.
});

test('Claude shares the header and inspector but keeps model windows on its detail page', async ({
  page
}) => {
  const claude = {
    ...provider(),
    provider: 'claude_code',
    display_name: 'Claude Code',
    source: 'claude_oauth_usage',
    plan: 'max',
    windows: [
      {
        ...provider().windows[0],
        id: 'five_hour',
        label: '5-hour',
        window_minutes: 300,
        used_percent: 23
      },
      provider().windows[0],
      {
        ...provider().windows[0],
        id: 'weekly_sonnet',
        label: 'Weekly (Sonnet)',
        model_specific: true
      }
    ]
  };
  await page.route('**/api/agents/dashboard/list**', route =>
    route.fulfill({
      json: {
        agents: [
          { name: 'Claude Code', source: 'cli', role: 'cli_agent', status: 'active' },
          { name: 'Codex', source: 'cli', role: 'cli_agent', status: 'active' }
        ]
      }
    })
  );
  await page.route('**/api/agents/*/detail', route =>
    route.fulfill({
      json: {
        name: 'Claude Code',
        provider: 'claude',
        role: 'cli_agent',
        model: 'opus',
        claude_sync: { settings: { model: 'opus' } }
      }
    })
  );
  let refreshes = 0;
  await page.route('**/api/usage/limits*', route => {
    if (route.request().url().includes('refresh=1')) refreshes++;
    return route.fulfill({ json: { providers: [provider(), claude] } });
  });
  await page.route('**/api/external-agents/refresh', route =>
    route.fulfill({ json: { success: true } })
  );
  await page.goto('/agents');
  await expect(page.locator('.roster-limit')).toHaveCount(2);
  const pill = page.locator('.roster-limit').filter({ hasText: 'Claude Code' });
  await expect(pill).toContainText('5h 23% used');
  await expect(pill).not.toContainText('Sonnet');
  await pill.click();
  await expect(page.locator('#stageName')).toHaveText('Claude Code');
  await expect(page.locator('#stageProgression')).toContainText('Anthropic usage endpoint');
  await expect(page.locator('#stageProgression [role="meter"]')).toHaveCount(2);
  await page.goto('/agents/Claude%20Code');
  await expect(page.locator('[data-provider-limits] [role="meter"]')).toHaveCount(3);
  await expect(page.locator('[data-provider-limits]')).toContainText('Weekly (Sonnet)');
  await expect.poll(() => refreshes).toBe(2); // Roster and detail both open live.
  await page.getByRole('button', { name: 'Refresh from ~/.claude' }).click();
  await expect.poll(() => refreshes).toBe(4);
});

for (const path of ['/agents?agent=Codex', '/agents/codex', '/agents/Claude%20Code']) {
  test(`live-on-open and visible-only cadence preserve the last reading on failure: ${path}`, async ({
    page
  }) => {
    await page.clock.install();
    const claude = path.includes('Claude');
    if (claude) {
      await page.route('**/api/agents/*/detail', route =>
        route.fulfill({
          json: {
            name: 'Claude Code',
            provider: 'claude',
            role: 'cli_agent',
            claude_sync: { settings: {} }
          }
        })
      );
    }
    let requests = 0;
    let failed = false;
    const warnings: string[] = [];
    page.on('console', message => {
      if (message.type() === 'warning' && message.text().includes('usage limits unavailable')) {
        warnings.push(message.text());
      }
    });
    await page.route('**/api/usage/limits*', route => {
      expect(route.request().url()).toContain('refresh=1');
      requests++;
      return route.fulfill({
        status: failed ? 503 : 200,
        json: {
          providers: [
            {
              ...provider(requests === 1 ? 'ok' : 'warn'),
              ...(claude ? { provider: 'claude_code', display_name: 'Claude Code' } : {})
            }
          ]
        }
      });
    });
    await page.goto(path);
    const meter = page.getByRole('meter');
    await expect(meter).toHaveAttribute('aria-valuenow', '16');
    expect(requests).toBe(1);
    const roster = path.includes('?');
    if (roster) {
      await page
        .locator('.roster-limit')
        .evaluate(element => element.setAttribute('data-kept', 'yes'));
      await page.locator('.roster-limit').focus();
      await page
        .locator('#rosterList')
        .evaluate(element => element.setAttribute('data-kept', 'yes'));
    }
    await page.clock.fastForward(180000);
    await expect(meter).toHaveAttribute('aria-valuenow', '85');
    expect(requests).toBe(2);
    if (roster) {
      await expect(page.locator('.roster-limit')).toHaveAttribute('data-kept', 'yes');
      await expect(page.locator('.roster-limit')).toBeFocused();
      await expect(page.locator('#rosterList')).toHaveAttribute('data-kept', 'yes');
    }
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await page.clock.fastForward(720000);
    expect(requests).toBe(2);
    failed = true;
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await expect.poll(() => requests).toBe(3);
    await expect.poll(() => warnings.length).toBe(1);
    await expect(meter).toHaveAttribute('aria-valuenow', '85');
    await page.clock.fastForward(180000);
    await expect.poll(() => requests).toBe(4);
    await expect(meter).toHaveAttribute('aria-valuenow', '85');
    expect(warnings).toHaveLength(1);
    failed = false;
    await page.clock.fastForward(180000);
    await expect.poll(() => requests).toBe(5);
    await expect(meter).toHaveAttribute('aria-valuenow', '85');
  });
}

test('Claude mirror is opt-in, preserves failure feedback, and can be removed', async ({
  page
}) => {
  await page.route('**/api/agents/*/detail', route =>
    route.fulfill({
      json: {
        name: 'Claude Code',
        provider: 'claude',
        role: 'cli_agent',
        claude_sync: { settings: {} }
      }
    })
  );
  let installed = false;
  let fail = false;
  const actions: string[] = [];
  await page.route('**/api/usage/limits?*', route =>
    route.fulfill({
      json: {
        providers: [
          {
            ...provider(),
            provider: 'claude_code',
            display_name: 'Claude Code',
            state: 'unavailable',
            detail: 'Claude usage endpoint unavailable',
            windows: [],
            mirror: { installed, previous_command_present: true }
          }
        ]
      }
    })
  );
  await page.route('**/api/usage/limits/claude-mirror', route => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().headers()['content-type']).toBe('application/json');
    const { action } = route.request().postDataJSON();
    actions.push(action);
    if (fail)
      return route.fulfill({
        status: 400,
        json: { error: "Claude's status line changed outside Ori" }
      });
    installed = action === 'install';
    return route.fulfill({ json: { installed, previous_command_present: true } });
  });
  await page.goto('/agents/Claude%20Code');
  const control = page.getByRole('region', { name: 'Status-line fallback' });
  await expect(control).toContainText('keeping your existing command');
  await expect(control).toContainText('Headless Ori turns do not update it');
  expect(actions).toEqual([]);
  await control.getByRole('button', { name: 'Mirror the status line to Ori' }).click();
  await expect(control).toContainText('Installed');
  await expect(control.getByRole('button', { name: 'Remove', exact: true })).toBeFocused();
  fail = true;
  await control.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(control.getByRole('alert')).toContainText('changed outside Ori');
  await expect(control.getByRole('button', { name: 'Remove', exact: true })).toBeEnabled();
  fail = false;
  await control.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(
    control.getByRole('button', { name: 'Mirror the status line to Ori' })
  ).toBeVisible();
  expect(actions).toEqual(['install', 'remove', 'remove']);
});

for (const theme of ['light', 'dark']) {
  for (const width of [375, 1280]) {
    test(`Claude limits and mirror controls fit and are accessible: ${theme}, ${width}px`, async ({
      page
    }) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await page.route('**/api/agents/*/detail', route =>
        route.fulfill({
          json: {
            name: 'Claude Code',
            provider: 'claude',
            role: 'cli_agent',
            claude_sync: { settings: {} }
          }
        })
      );
      await page.route('**/api/usage/limits*', route =>
        route.fulfill({
          json: {
            providers: [
              {
                ...provider('warn'),
                provider: 'claude_code',
                display_name: 'Claude Code',
                mirror: { installed: false, previous_command_present: true }
              }
            ]
          }
        })
      );
      await page.goto('/agents/Claude%20Code');
      const card = page.locator('[data-provider-limits]');
      await expect(
        card.getByRole('button', { name: 'Mirror the status line to Ori' })
      ).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true
      );
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const violations = await card.evaluate(async element => {
        const result = await (window as any).axe.run(element, {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
        return result.violations;
      });
      expect(violations).toEqual([]);
    });
  }
}

test('no providers hides the entire group', async ({ page }) => {
  await page.route('**/api/usage/limits*', route => route.fulfill({ json: { providers: [] } }));
  await page.goto('/agents');
  await expect(page.locator('#providerLimits')).toBeHidden();
  await expect(page.locator('#providerLimits')).toHaveAttribute('hidden', '');
});

test('signed out has no pill and shows the login instruction in the inspector', async ({
  page
}) => {
  await page.route('**/api/usage/limits*', route =>
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
  await page.route('**/api/usage/limits*', route =>
    route.fulfill({ json: { providers: [provider(), claude] } })
  );
  await page.goto('/agents');
  await expect(page.locator('.roster-limit')).toHaveCount(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.locator('.roster-limit').first().click();
  await expect(page.getByRole('meter')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
