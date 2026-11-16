import { expect, test, type Page } from '@playwright/test';
import {
  assertSandbox,
  capture,
  contrast,
  guardReadTour,
  preparePage,
  reachable,
  readFixture
} from './helpers/ui-consistency';

async function navbarReads(page: Page, count = 0) {
  // These are GET-only presentation reads. Never open Help (its greeting may
  // use a provider), the updater, or a fictional vault for photography.
  await readFixture(page, '**/api/updates/check', { updateAvailable: false });
  await readFixture(page, '**/api/vault/vaults', {
    count: count ? 1 : 0,
    vaults: count
      ? [
          {
            id: 'navbar-read-only',
            name: 'Fictional vault',
            password_protected: true,
            record_count: count
          }
        ]
      : []
  });
}

async function checkNavbar(page: Page, width: number) {
  const minimum = width <= 768 ? 44 : 40;
  const controls = page.locator('.navbar a:visible, .navbar button:visible');
  const bounds = [];
  for (const control of await controls.all()) {
    const label = (await control.getAttribute('aria-label')) || (await control.textContent());
    await test.step(`Reachable navbar control: ${label?.trim()}`, async () => {
      bounds.push(await reachable(control, minimum));
    });
  }
  const brand = page.locator('.navbar-brand');
  await expect(page.locator('.ori-navbar-brand-text')).toHaveCSS(
    'font-family',
    await brand.evaluate(node => getComputedStyle(node).fontFamily)
  );
  const brandContrast = await contrast(brand);
  expect(brandContrast.ratio).toBeGreaterThanOrEqual(4.5);
  for (const link of await page.locator('.ori-navbar-links a:visible').all()) {
    expect((await contrast(link)).ratio).toBeGreaterThanOrEqual(4.5);
  }
  expect((await contrast(page.locator('#navSettingsLink'))).ratio).toBeGreaterThanOrEqual(3);
  await expect
    .poll(() =>
      page.evaluate(() => {
        const height = document.querySelector('.navbar')!.getBoundingClientRect().height;
        const offset = Number.parseFloat(
          getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')
        );
        return Math.abs(Math.max(76, Math.ceil(height)) - offset);
      })
    )
    .toBeLessThanOrEqual(1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
    width
  );
  return { bounds, brandContrast };
}

for (const theme of ['light', 'dark'] as const) {
  for (const width of [320, 390, 768, 1440]) {
    test(`Navbar controls and conditional Vaults: ${theme} ${width}`, async ({
      page,
      request
    }, testInfo) => {
      await assertSandbox(request);
      await preparePage(page, theme, width);
      const unexpected = await guardReadTour(page);
      await navbarReads(page);
      await page.goto('/settings');
      await expect(page.locator('#navSettingsLink')).toHaveAttribute('aria-current', 'page');
      await expect(page.locator('#vaultLauncherButton')).toBeHidden();
      await capture(page, testInfo, 'navbar-quiet', await checkNavbar(page, width), {
        fullPage: false
      });

      await navbarReads(page, 12);
      await page.reload();
      await expect(page.locator('#vaultLauncherButton')).toBeVisible();
      await expect(page.locator('#vaultNavCount')).toHaveText('12');
      await expect(page.locator('#vaultLauncherButton')).toHaveAttribute('href', '/vaults');
      await expect(page.locator('#vaultLauncherButton')).toHaveAttribute(
        'data-bs-target',
        '#vaultModal'
      );
      await expect(page.locator('#vaultModal')).toHaveCount(1);
      await expect(page.locator('.ori-navbar-links a[href="/"]')).toHaveText('Home');
      await expect(page.locator('#navAgentsLink')).toHaveAttribute('href', '/agents');
      await expect(page.locator('#sidebar, #sidebarToggle, #systemModelIndicator')).toHaveCount(0);
      await capture(page, testInfo, 'navbar-conditional', await checkNavbar(page, width), {
        fullPage: false
      });
      expect(unexpected).toEqual([]);
    });
  }

  for (const width of [320, 1440]) {
    test(`Navbar long supported labels preserve artwork and controls: ${theme} ${width}`, async ({
      page,
      request
    }, testInfo) => {
      await assertSandbox(request);
      await preparePage(page, theme, width);
      const unexpected = await guardReadTour(page);
      await navbarReads(page, 12345);
      await page.goto('/settings');
      await expect(page.locator('#vaultLauncherButton')).toBeVisible();
      await page.evaluate(() => {
        // Presentation-only labels exercise the existing custom brand and
        // optional action slots. Do not install an update or activate an action.
        document.querySelector('.ori-navbar-brand-text')!.textContent =
          'Ori Agent — a very long workspace brand ' + 'abcdefghij'.repeat(8);
        document.querySelector('#oriGuideLauncher > span:last-child')!.textContent =
          'Help with a long translated workspace navigation label';
        const update = document.querySelector<HTMLButtonElement>('#updateNotificationBtn')!;
        update.style.display = 'flex';
        update.querySelector('span')!.textContent =
          'Update available for an exceptionally long local build identifier';
      });
      expect((await page.locator('.navbar-brand svg').boundingBox())!.width).toBe(32);
      await capture(
        page,
        testInfo,
        'navbar-long-labels',
        {
          ...(await checkNavbar(page, width)),
          fixture: 'DOM labels only; no Help, update or vault activation'
        },
        { fullPage: false }
      );
      expect(unexpected).toEqual([]);
    });
  }

  test(`Navbar keyboard mode and theme retain native feedback: ${theme}`, async ({
    page,
    request
  }, testInfo) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const unexpected = await guardReadTour(page);
    await navbarReads(page);
    await page.goto('/settings');
    const keyboard = page.locator('#keyboardNavigationToggle');
    await keyboard.focus();
    await keyboard.press('Enter');
    await expect(keyboard).toHaveAttribute('aria-pressed', 'true');
    expect((await contrast(keyboard)).ratio).toBeGreaterThanOrEqual(3);
    await page.keyboard.press('Escape');
    await expect(keyboard).toHaveAttribute('aria-pressed', 'false');
    const toggle = page.locator('#darkModeToggle');
    const opposite = theme === 'dark' ? 'light' : 'dark';
    await toggle.focus();
    await toggle.press('Enter');
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', opposite);
    await expect(toggle).toHaveAttribute('aria-label', `Switch to ${theme} mode`);
    // Reduced motion must not create even a one-frame theme transition on a
    // static navbar. Assert the real preference, not a sleep or test-only shim.
    await expect(page.locator('.navbar')).toHaveCSS('transition-duration', '0s');
    expect(await page.locator('.navbar').evaluate(node => node.getAnimations().length)).toBe(0);
    expect((await contrast(page.locator('.navbar-brand'))).ratio).toBeGreaterThanOrEqual(4.5);
    await toggle.press('Enter');
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    await expect(toggle).toBeFocused();
    expect(await toggle.evaluate(node => getComputedStyle(node).outlineStyle)).not.toBe('none');
    await capture(page, testInfo, 'navbar-keyboard-focus', {}, { fullPage: false });
    expect(unexpected).toEqual([]);
  });

  test(`Navbar 200% reflow equivalent keeps controls and field focus reachable: ${theme}`, async ({
    page,
    request
  }, testInfo) => {
    await assertSandbox(request);
    // Browser page zoom maps a 1440×900 viewport to 720×450 CSS pixels.
    // Test that layout equivalence, not deviceScaleFactor or pinch zoom.
    await preparePage(page, theme, 720);
    await page.setViewportSize({ width: 720, height: 450 });
    const unexpected = await guardReadTour(page);
    await navbarReads(page, 12);
    await page.goto('/settings#api-keys');
    await page.evaluate(async () => {
      await document.fonts.ready;
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
    });
    await expect(page.locator('#vaultLauncherButton')).toBeVisible();
    const facts = await checkNavbar(page, 720);
    const field = page.locator('#openaiApiKeyInput');
    await field.focus();
    await reachable(field, 44);
    await expect(field).toBeFocused();
    const top = (await field.boundingBox())!.y;
    const navbarBottom = await page
      .locator('.navbar')
      .evaluate(node => node.getBoundingClientRect().bottom);
    expect(top).toBeGreaterThanOrEqual(navbarBottom);
    await capture(
      page,
      testInfo,
      'navbar-zoom-reflow',
      {
        ...facts,
        zoomEquivalent: '200% of 1440×900; 720×450 CSS viewport; native browser zoom not automated'
      },
      { fullPage: false }
    );
    expect(unexpected).toEqual([]);
  });

  test(`Settings cold fragment settles with the active font mode: ${theme}`, async ({
    page,
    request
  }, testInfo) => {
    await assertSandbox(request);
    await preparePage(page, theme, 320);
    const unexpected = await guardReadTour(page);
    await navbarReads(page);
    let delayedSettingsReads = 0;
    await page.route('**/api/settings/system-model', async route => {
      if (route.request().method() !== 'GET') return route.fallback();
      delayedSettingsReads++;
      await new Promise(resolve => setTimeout(resolve, 450));
      await route.fallback();
    });
    let delayedFonts = 0;
    if (process.env.ORI_UI_FONTS === 'real') {
      await page.route('https://fonts.gstatic.com/**', async route => {
        delayedFonts++;
        // Read-only network latency, not a replacement font or a mocked DOM.
        await new Promise(resolve => setTimeout(resolve, 400));
        await route.continue();
      });
    }
    await page.goto('/settings#voice-language');
    const historyLength = await page.evaluate(() => history.length);
    await expect(page.locator('#systemModelStatusText')).not.toHaveText('Loading...');
    expect(delayedSettingsReads).toBeGreaterThan(0);
    await page.evaluate(async () => {
      await document.fonts.ready;
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
      await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
    });
    if (process.env.ORI_UI_FONTS === 'real') expect(delayedFonts).toBeGreaterThan(0);
    await expect(page.locator('.settings-nav-item[data-section="voice-language"]')).toHaveAttribute(
      'aria-current',
      'location'
    );
    const heading = page.locator('#voice-language h2');
    await expect
      .poll(() =>
        heading.evaluate(
          node =>
            node.getBoundingClientRect().top -
            Number.parseFloat(
              getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')
            )
        )
      )
      .toBeLessThan(100);
    const geometry = await heading.evaluate(node => ({
      top: node.getBoundingClientRect().top,
      navbar: Number.parseFloat(
        getComputedStyle(document.documentElement).getPropertyValue('--navbar-offset')
      )
    }));
    expect(geometry.top).toBeGreaterThanOrEqual(geometry.navbar);
    expect(geometry.top).toBeLessThan(geometry.navbar + 100);
    await expect(heading).not.toBeFocused();
    expect(await page.evaluate(() => history.length)).toBe(historyLength);
    await capture(
      page,
      testInfo,
      'settings-font-ready',
      { delayedFonts, delayedSettingsReads, geometry },
      { fullPage: false }
    );
    expect(unexpected).toEqual([]);
  });

  test(`Navbar native links retain route and current-page semantics: ${theme}`, async ({
    page,
    request
  }) => {
    await assertSandbox(request);
    await preparePage(page, theme, 390);
    const unexpected = await guardReadTour(page);
    await navbarReads(page);
    await page.goto('/skills');
    await page.locator('#navAgentsLink').focus();
    await page.locator('#navAgentsLink').press('Enter');
    await expect(page).toHaveURL(/\/agents$/);
    await expect(page.locator('#navAgentsLink')).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('#navSettingsLink')).not.toHaveAttribute('aria-current', 'page');
    await page.locator('#navSettingsLink').focus();
    await page.locator('#navSettingsLink').press('Enter');
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.locator('#navSettingsLink')).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('#navAgentsLink')).not.toHaveAttribute('aria-current', 'page');
    expect(unexpected).toEqual([]);
  });
}
