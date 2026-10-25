import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { checkNextActionReflow } from './helpers/next-action-reflow';

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
});

for (const theme of ['light', 'dark']) {
  test(`next action is keyboard accessible in both views at all supported widths (${theme})`, async ({
    page
  }, testInfo) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 390, height: 844 });
    const response = await page.request.post('/api/workspaces', {
      data: { name: `Work-first accessibility ${Date.now()}` }
    });
    expect(response.ok()).toBeTruthy();
    const { folder } = await response.json();
    const agentName = `Work-first Manager ${Date.now()}`;
    expect(
      (
        await page.request.post('/api/agents', { data: { name: agentName, type: 'orchestration' } })
      ).ok()
    ).toBeTruthy();
    expect(
      (
        await page.request.post(`/api/workspaces/${folder.id}/agents`, {
          data: { agent_name: agentName }
        })
      ).ok()
    ).toBeTruthy();
    try {
      await page.goto(`/workspaces/${folder.folder_slug}`);
      const area = page.getByRole('region', { name: 'Next action', exact: true });
      await expect(area.getByRole('button', { name: 'Give Task' })).toBeVisible();
      await page.keyboard.press('Tab');
      await area.getByRole('button', { name: 'All Tasks' }).focus();
      await expect(area.getByRole('button', { name: 'All Tasks' })).toHaveCSS(
        'outline-style',
        'solid'
      );
      await page.keyboard.press('Enter');
      await page.getByRole('button', { name: 'Close tasks', exact: true }).click();
      await expect(area.getByRole('button', { name: 'All Tasks' })).toBeFocused();
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      await expect(page.locator('#onboardingModal')).toBeHidden();
      // Response fixture only: no task or agent execution, and an unbroken name
      // exercises wrapping rather than relying on conveniently short copy.
      const longTitle = 'LongUnbrokenTaskName'.repeat(9);
      await page.route('**/api/orchestration/tasks?**', route =>
        route.fulfill({
          json: { tasks: [{ id: 'reflow-task', status: 'blocked', description: longTitle }] }
        })
      );
      await page.evaluate(() => window.workspaceDetail.loadTasks());
      await expect(area).toContainText(longTitle);
      for (const mode of ['details', 'map']) {
        await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
        for (const width of [320, 390, 768, 1280]) {
          await page.setViewportSize({ width, height: 900 });
          const primary = area.locator('.is-primary');
          await expect(primary).toHaveCount(1);
          await expect(primary).toBeVisible();
          const boxes = await area.locator('button').evaluateAll(nodes =>
            nodes.map(node => {
              const box = node.getBoundingClientRect();
              return { left: box.left, right: box.right };
            })
          );
          for (const box of boxes) {
            expect(box.left).toBeGreaterThanOrEqual(-1);
            expect(box.right).toBeLessThanOrEqual(width + 1);
          }
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
          ).toBe(false);
          if (width === 390) {
            const scan = await page.evaluate(async () => {
              const result = await window.axe.run('.ws-cmd-work-first', {
                runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
              });
              return {
                violations: result.violations,
                contrastNodes:
                  result.passes.find(rule => rule.id === 'color-contrast')?.nodes.length || 0
              };
            });
            expect(scan.violations).toEqual([]);
            expect(scan.contrastNodes).toBeGreaterThan(0);
          }
          await area.screenshot({
            path: testInfo.outputPath(`next-action-${mode}-${width}-${theme}.png`),
            animations: 'disabled'
          });
        }
      }
      await checkNextActionReflow(page, '.ws-cmd-work-first, .ws-cmd-work-admin');
      await page.screenshot({
        path: testInfo.outputPath(`workspace-work-first-zoom-${theme}.png`),
        fullPage: true
      });
      // Display fixture only: exercise the canonical HQ designation without
      // building an HQ or invoking brief/model generation in this layout test.
      await page.evaluate(() => {
        window.workspaceDetail.workspace.designation = 'personal_hq';
        window.workspaceCommand.refresh();
      });
      for (const mode of ['map', 'details']) {
        await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
        await expect(area).toHaveCount(0);
        if (mode === 'map') await expect(page.locator('.ws-cmd-map-shell.is-hq')).toBeVisible();
        await page.screenshot({
          path: testInfo.outputPath(`next-action-hq-fixture-${mode}-${theme}.png`),
          fullPage: true,
          animations: 'disabled'
        });
      }
    } finally {
      await page.request.delete(`/api/workspaces/${folder.id}`);
    }
  });
  test(`workspace detail accessibility (${theme})`, async ({ page }) => {
    const baseUrl =
      process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(selectedTheme => {
      window.localStorage.setItem('ori-theme', selectedTheme);
    }, theme);
    await page.route('**/api/onboarding/status', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: false,
          current_step: 3,
          completed: true,
          skipped: false,
          steps_completed: [0, 1, 2],
          user_name: 'Tester',
          assistant_name: 'Ori'
        })
      });
    });
    // Always isolate the generic journey: selecting an arbitrary prior fixture
    // makes this depend on which plugin/folder suites ran before it.
    const stamp = Date.now();
    const created = await page.request.post(`${baseUrl}/api/workspaces`, {
      data: { name: `Accessibility Workspace ${stamp}`, blank: true }
    });
    expect(created.ok()).toBeTruthy();
    const workspace = (await created.json()).folder;
    const agentName = `Accessibility Manager ${stamp}`;
    const agent = await page.request.post(`${baseUrl}/api/agents`, {
      data: { name: agentName, type: 'general' }
    });
    expect(agent.ok()).toBeTruthy();
    const attached = await page.request.post(`${baseUrl}/api/workspaces/${workspace.id}/agents`, {
      data: { agent_name: agentName }
    });
    expect(attached.ok()).toBeTruthy();
    const workspaceId = workspace.id;
    const workspaceSlug = workspace.folder_slug;
    expect(workspaceId).toBeTruthy();
    expect(workspaceSlug).toBeTruthy();

    const workspaceActivated = page.waitForResponse(response =>
      response.url().includes('/api/orchestration/workspace/activate?id=')
    );
    await page.goto(`${baseUrl}/workspaces/${encodeURIComponent(workspaceSlug)}`, {
      waitUntil: 'domcontentloaded'
    });
    await expect(page.locator('#workspaceCommandView .ws-cmd-title h2')).toBeVisible();
    await workspaceActivated;
    await page.evaluate(() => {
      window.workspaceDetail.workspace.project_path = '';
      window.workspaceDetail.workspace.shared_data = {};
      window.workspaceCommand.refresh();
    });
    await expect(page.locator('[data-cmd-open-project]')).toHaveCount(0);

    await page.evaluate(() => {
      window.workspaceDetail.workspace.project_path = 'accessibility-project';
      window.workspaceDetail.workspace.shared_data = { project_entry_path: 'project.rpp' };
      window.workspaceCommand.refresh();
    });
    const openProject = page.getByRole('button', {
      name: 'Open project using the system default application'
    });
    await expect(openProject).toBeVisible();
    await expect(openProject).toHaveAttribute('aria-busy', 'false');
    await page.getByRole('button', { name: 'Map', exact: true }).click();
    await expect(openProject).toBeVisible();

    const operationsMap = page.getByRole('region', { name: 'Workspace operations map' });
    const html = page.locator('html');
    const toggleTheme = async expectedTheme => {
      await page.locator('#darkModeToggle').click();
      await expect(html).toHaveAttribute('data-bs-theme', expectedTheme);
    };
    const expectMapTheme = async expectedTheme => {
      const expectedNameColor =
        expectedTheme === 'light' ? 'rgb(10, 10, 10)' : 'rgb(238, 240, 243)';
      const expectedQuestColor =
        expectedTheme === 'light' ? 'rgb(31, 33, 40)' : 'rgb(214, 217, 222)';
      await expect(operationsMap.locator('.ws-cmd-map-agent-copy strong').first()).toHaveCSS(
        'color',
        expectedNameColor
      );
      await expect(page.locator('.ws-cmd-map-quest-fab')).toHaveCSS('color', expectedQuestColor);
      const style = await operationsMap.evaluate(element => {
        const read = selector => getComputedStyle(element.parentElement.querySelector(selector));
        const computed = getComputedStyle(element);
        const agent = read('.ws-cmd-map-agent');
        const agentName = read('.ws-cmd-map-agent-copy strong');
        const agentStatus = read('.ws-cmd-map-agent-status');
        const belt = read('.ws-cmd-map-belt');
        const beltButton = read('.ws-cmd-map-belt-btn');
        const questButton = read('.ws-cmd-map-quest-fab');
        const viewSwitch = getComputedStyle(document.querySelector('.ws-cmd-view-switch'));
        const inactiveView = getComputedStyle(
          document.querySelector('[data-cmd-view-mode="details"]')
        );
        return {
          backgroundImage: computed.backgroundImage,
          backgroundSize: computed.backgroundSize,
          agentBackgroundImage: agent.backgroundImage,
          agentNameColor: agentName.color,
          agentStatusBackground: agentStatus.backgroundColor,
          beltBackgroundImage: belt.backgroundImage,
          beltButtonColor: beltButton.color,
          questButtonColor: questButton.color,
          viewSwitchBackground: viewSwitch.backgroundColor,
          inactiveViewColor: inactiveView.color
        };
      });
      const endpoint = expectedTheme === 'light' ? 'rgb(223, 228, 232) 78%' : 'rgb(12, 13, 17) 78%';

      expect(style.backgroundImage).toContain(endpoint);
      expect(style.backgroundImage.match(/(?:radial|linear)-gradient\(/g)).toHaveLength(5);
      expect(style.backgroundSize).toBe('auto, auto, 42px 42px, 42px 42px, auto');
      if (expectedTheme === 'light') {
        expect(style.backgroundImage).toContain('rgb(251, 252, 253)');
        expect(style.backgroundImage).not.toContain('rgb(12, 13, 17) 78%');
        expect(style.agentBackgroundImage).toContain('rgba(255, 255, 255, 0.98)');
        expect(style.agentBackgroundImage).not.toContain('rgba(0, 0, 0, 0.16)');
        expect(style.agentNameColor).toBe(expectedNameColor);
        expect(style.agentStatusBackground).toBe('rgba(255, 255, 255, 0.88)');
        expect(style.beltBackgroundImage).toContain('rgba(255, 255, 255, 0.97)');
        expect(style.beltButtonColor).toBe('rgb(92, 92, 94)');
        expect(style.questButtonColor).toBe(expectedQuestColor);
        expect(style.viewSwitchBackground).toBe('rgba(255, 255, 255, 0.92)');
        expect(style.inactiveViewColor).toBe('rgb(31, 33, 40)');
      } else {
        expect(style.agentBackgroundImage).toContain('rgba(0, 0, 0, 0.16)');
        expect(style.agentNameColor).toBe(expectedNameColor);
        expect(style.agentStatusBackground).toBe('rgba(0, 0, 0, 0.24)');
        expect(style.beltBackgroundImage).toContain('rgba(17, 22, 29, 0.9)');
        expect(style.beltButtonColor).toBe('rgb(139, 144, 154)');
        expect(style.questButtonColor).toBe(expectedQuestColor);
        expect(style.viewSwitchBackground).toBe('rgba(0, 0, 0, 0.2)');
        expect(style.inactiveViewColor).toBe('rgb(125, 131, 142)');
      }
      return style;
    };

    await expect(operationsMap).toBeVisible();
    const mapGeometry = await operationsMap.evaluate(element => {
      const rects = selector =>
        [...element.querySelectorAll(selector)].map(node => {
          const rect = node.getBoundingClientRect();
          return {
            label: node.getAttribute('aria-label') || node.textContent.trim(),
            left: rect.left,
            right: rect.right,
            top: rect.top,
            bottom: rect.bottom
          };
        });
      return {
        agents: rects('.ws-cmd-map-agent'),
        stations: rects('.ws-cmd-map-hq-station')
      };
    });
    for (const agent of mapGeometry.agents) {
      for (const station of mapGeometry.stations) {
        const overlaps =
          agent.left < station.right &&
          agent.right > station.left &&
          agent.top < station.bottom &&
          agent.bottom > station.top;
        expect(overlaps, `${agent.label} overlaps ${station.label}`).toBeFalsy();
      }
    }
    await expect(html).toHaveAttribute('data-bs-theme', theme);
    const startingStyle = await expectMapTheme(theme);

    // Exercise the application's real switch path. The dark-starting case first
    // moves to light, then both cases cover the light -> dark -> light round trip.
    if (theme === 'dark') await toggleTheme('light');
    const lightBefore = await expectMapTheme('light');
    await toggleTheme('dark');
    await expectMapTheme('dark');
    await toggleTheme('light');
    expect(await expectMapTheme('light')).toEqual(lightBefore);

    // Leave the existing Details-mode accessibility assertions in their original theme.
    if (theme === 'dark') await toggleTheme('dark');
    expect(await expectMapTheme(theme)).toEqual(startingStyle);
    await page.getByRole('button', { name: 'Details', exact: true }).click();
    await expect(openProject).toBeVisible();
    await page.locator('#workspaceCommandView').evaluate(async root => {
      const finiteAnimations = root
        .getAnimations({ subtree: true })
        .filter(animation => animation.effect?.getTiming().iterations !== Infinity);
      await Promise.all(
        finiteAnimations.map(animation => animation.finished.catch(() => undefined))
      );
    });

    await page.addScriptTag({
      url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js'
    });

    const results = await page.evaluate(async () => {
      const root = document.querySelector('#workspaceCommandView');
      if (!root) {
        throw new Error('workspaceCommandView root not found');
      }

      return await window.axe.run(root, {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
      });
    });

    expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
  });
}
