import { test, expect, type Page } from '@playwright/test';

// The collapsed navigation (nav-collapse PRD FR1–FR3, FR6, FR10, FR13, FR22):
// every page's top bar is Home, Agents and a Settings gear, no page renders the
// retired sidebar or its hamburger, and Vaults joins the bar only once a vault
// holds a record. The vault list is answered here so the result does not
// depend on what the sandbox happens to contain.

async function skipOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
}

async function answerVaultList(page: Page, recordCount: number) {
  const vaults = recordCount
    ? [{ id: 'vault-e2e', name: 'E2E', password_protected: true, record_count: recordCount }]
    : [];
  await page.route('**/api/vault/vaults', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ count: vaults.length, vaults })
    })
  );
}

// The link group's visible items, by their text rather than innerText: the bar
// uppercases its links with CSS.
async function visibleNavLinks(page: Page) {
  return page
    .locator('.ori-navbar-links a')
    .evaluateAll(links =>
      links
        .filter(link => !link.closest('[hidden]') && link.getClientRects().length > 0)
        .map(link => (link.querySelector('span')?.textContent || link.textContent || '').trim())
    );
}

test.describe('Collapsed navigation', () => {
  let workspaceSlug = '';

  test.beforeAll(async ({ request }) => {
    const res = await request.post('/api/workspaces', {
      data: { name: `Nav Collapse ${Date.now().toString(36)}`, description: '' }
    });
    expect(res.ok(), await res.text()).toBeTruthy();
    const body = await res.json();
    workspaceSlug = (body.folder || body.workspace).folder_slug;
  });

  for (const target of [
    { name: 'Home', path: () => '/', current: 'Home', gearCurrent: false },
    {
      name: 'a workspace',
      path: () => `/workspaces/${workspaceSlug}`,
      current: '',
      gearCurrent: false
    },
    { name: 'Agents', path: () => '/agents', current: 'Agents', gearCurrent: false },
    { name: 'Settings', path: () => '/settings', current: '', gearCurrent: true },
    { name: 'Skills', path: () => '/skills', current: '', gearCurrent: true }
  ]) {
    test(`${target.name} shows Home, Agents and the Settings gear, and no sidebar`, async ({
      page
    }) => {
      await skipOnboarding(page);
      await answerVaultList(page, 0);
      await page.goto(target.path());

      await expect(page.locator('#sidebar')).toHaveCount(0);
      await expect(page.locator('#sidebarToggle')).toHaveCount(0);
      await expect(page.locator('.ori-navbar-links a[href="/action-center"]')).toHaveCount(0);
      await expect(page.locator('#systemModelIndicator')).toHaveCount(0);
      expect(await visibleNavLinks(page)).toEqual(['Home', 'Agents']);

      const gear = page.locator('#navSettingsLink');
      await expect(gear).toBeVisible();
      await expect(gear).toHaveAttribute('href', '/settings');
      await expect(gear).toHaveAttribute('aria-label', 'Settings');
      if (target.gearCurrent) {
        await expect(gear).toHaveAttribute('aria-current', 'page');
        await expect(page.locator('.ori-navbar-links [aria-current]')).toHaveCount(0);
      } else {
        await expect(gear).not.toHaveAttribute('aria-current', 'page');
      }
      if (target.current) {
        await expect(
          page.locator('.ori-navbar-links [aria-current="page"]', { hasText: target.current })
        ).toHaveCount(1);
      }
    });
  }

  test('Home fits a 360px phone with no sideways scroll', async ({ page }) => {
    await skipOnboarding(page);
    await answerVaultList(page, 0);
    await page.setViewportSize({ width: 360, height: 740 });
    await page.goto('/');
    await expect(page.locator('#navSettingsLink')).toBeVisible();

    const widths = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      inner: window.innerWidth
    }));
    expect(widths.scroll).toBeLessThanOrEqual(widths.inner);
  });

  test('Vaults stays out of the bar until a vault holds a record', async ({ page }) => {
    await skipOnboarding(page);
    await answerVaultList(page, 0);
    await page.goto('/');
    await expect(page.locator('#navSettingsLink')).toBeVisible();
    await expect(page.locator('#vaultLauncherButton')).toBeHidden();
    await expect(page.locator('#vaultLauncherDivider')).toBeHidden();
    // The modal stays mounted so anything that opens it directly still can.
    await expect(page.locator('#vaultModal')).toHaveCount(1);
  });

  test('Vaults joins the bar with its count once a vault holds a record', async ({ page }) => {
    await skipOnboarding(page);
    await answerVaultList(page, 1);
    await page.goto('/');

    await expect(page.locator('#vaultLauncherButton')).toBeVisible();
    await expect(page.locator('#vaultNavCount')).toHaveText('1');
    expect(await visibleNavLinks(page)).toEqual(['Home', 'Agents', 'Vaults']);
  });

  test('the vault page always shows its own launcher', async ({ page }) => {
    await skipOnboarding(page);
    await answerVaultList(page, 0);
    await page.goto('/vaults');

    const launcher = page.locator('#vaultLauncherButton');
    await expect(launcher).toBeVisible();
    await expect(launcher).toHaveAttribute('aria-current', 'page');
  });
});
