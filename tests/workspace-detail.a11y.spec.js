import { test, expect } from '@playwright/test';

for (const theme of ['light', 'dark']) {
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
    const workspacesResponse = await page.request.get(`${baseUrl}/api/workspaces?tree=true`);
    expect(workspacesResponse.ok()).toBeTruthy();
    const workspaceData = await workspacesResponse.json();
    let workspace = workspaceData.workspaces?.find(
      candidate => candidate.kind === 'workspace' && candidate.agent_count > 0
    );
    if (!workspace) {
      const stamp = Date.now();
      const created = await page.request.post(`${baseUrl}/api/workspaces`, {
        data: { name: `Accessibility Workspace ${stamp}`, blank: true }
      });
      expect(created.ok()).toBeTruthy();
      workspace = (await created.json()).folder;
      const agentName = `Accessibility Manager ${stamp}`;
      const agent = await page.request.post(`${baseUrl}/api/agents`, {
        data: { name: agentName, type: 'general' }
      });
      expect(agent.ok()).toBeTruthy();
      const attached = await page.request.post(`${baseUrl}/api/workspaces/${workspace.id}/agents`, {
        data: { agent_name: agentName }
      });
      expect(attached.ok()).toBeTruthy();
    }
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
        expectedTheme === 'light' ? 'rgb(255, 255, 255)' : 'rgb(214, 217, 222)';
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
        const beltButton = read('.ws-cmd-map-belt-btn');
        const questButton = read('.ws-cmd-map-quest-fab');
        return {
          backgroundImage: computed.backgroundImage,
          backgroundSize: computed.backgroundSize,
          agentBackgroundImage: agent.backgroundImage,
          agentNameColor: agentName.color,
          agentStatusBackground: agentStatus.backgroundColor,
          beltButtonColor: beltButton.color,
          questButtonColor: questButton.color
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
        expect(style.beltButtonColor).toBe('rgb(197, 203, 210)');
        expect(style.questButtonColor).toBe(expectedQuestColor);
      } else {
        expect(style.agentBackgroundImage).toContain('rgba(0, 0, 0, 0.16)');
        expect(style.agentNameColor).toBe(expectedNameColor);
        expect(style.agentStatusBackground).toBe('rgba(0, 0, 0, 0.24)');
        expect(style.beltButtonColor).toBe('rgb(139, 144, 154)');
        expect(style.questButtonColor).toBe(expectedQuestColor);
      }
      return style;
    };

    await expect(operationsMap).toBeVisible();
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
