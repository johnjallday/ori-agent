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
    // Both themes run at once; the theme keeps their names apart when they
    // start in the same millisecond (the create then collided and failed).
    const stamp = `${theme} ${Date.now()}`;
    const response = await page.request.post('/api/workspaces', {
      data: { name: `Work-first accessibility ${stamp}` }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const { folder } = await response.json();
    const agentName = `Work-first Manager ${stamp}`;
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
      await expect(area.getByRole('button', { name: 'Give work', exact: true })).toBeVisible();
      await page.keyboard.press('Tab');
      await area.getByRole('button', { name: 'All tickets', exact: true }).focus();
      await expect(area.getByRole('button', { name: 'All tickets', exact: true })).toHaveCSS(
        'outline-style',
        'solid'
      );
      // All tickets opens the board (the legacy tasks drawer is gone, PRD 760 FR-33).
      await page.keyboard.press('Enter');
      await expect(page.locator('#hubTicketsGiveWork')).toBeVisible();
      await page.locator('[data-cmd-view-mode="details"]').click();
      await expect(area.getByRole('button', { name: 'All tickets', exact: true })).toBeVisible();
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
  // PRD 766 FR29: the Personal HQ's Details page, header first and station-first.
  test(`Personal HQ Details: header first, attention strip, main column and rail (${theme})`, async ({
    page
  }, testInfo) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 1280, height: 900 });
    // The theme is in the names: both themes run at once and can share a stamp.
    const stamp = `${theme} ${Date.now()}`;
    const response = await page.request.post('/api/workspaces', {
      data: { name: `HQ layout ${stamp}`, blank: true }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const { folder } = await response.json();
    const agentName = `HQ Lead ${stamp}`;
    expect(
      (await page.request.post('/api/agents', { data: { name: agentName, type: 'general' } })).ok()
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
      await expect(page.locator('#workspaceCommandView .ws-cmd-title h2')).toBeVisible();
      await page.waitForFunction(() => Boolean(window.workspaceDetail?.workspace?.id));
      // Display fixture only, as above: the canonical HQ designation on this
      // workspace, without building an HQ. The goal's own status line is
      // silenced so the strip starts empty, and every top mount is given
      // content so "nothing above the header" is tested with all of them full.
      await page.evaluate(() => {
        if (window.workspaceMission) window.workspaceMission.getState = () => ({});
        for (const id of [
          'workspaceGroupRequirementStatus',
          'workspaceEmailConnectMount',
          'workspaceNeedsYouMount',
          'workspaceBriefHistoryMount',
          'fileJanitorMount',
          'backupRecoveryMount',
          'githubProposalsMount',
          'blueprintIntakePending'
        ]) {
          const el = document.getElementById(id);
          if (!el) continue;
          el.hidden = false;
          el.textContent = 'Fixture mount ' + id;
        }
        window.workspaceDetail.workspace.designation = 'personal_hq';
        window.workspaceCommand.setCommandViewMode('details', { focus: false });
      });
      const header = page.locator('.ws-hq-header');
      await expect(header).toBeVisible();
      await expect(page.locator('body')).toHaveAttribute('data-hq-details', '1');

      const strip = page.locator('[data-cmd-hq-attention]');
      // The fixture mounts other than the setup ones say nothing in the strip;
      // the setup mounts would, so start with them hidden again.
      await page.evaluate(() => {
        for (const id of [
          'workspaceGroupRequirementStatus',
          'workspaceEmailConnectMount',
          'blueprintIntakePending'
        ]) {
          document.getElementById(id).hidden = true;
        }
        window.workspaceCommand.render();
      });
      await expect(strip).toBeHidden();
      // A first-task banner is a setup line: the strip appears under the header.
      await page.evaluate(() => {
        const banner = document.getElementById('workspaceFirstTaskBanner');
        banner.textContent = 'Your lead is reading the folder you added.';
        banner.hidden = false;
      });
      await expect(strip).toBeVisible();
      await expect(strip).toContainText('Needs your attention');
      await expect(strip).toContainText('Your lead is reading the folder you added.');

      for (const width of [375, 768, 1280]) {
        await page.setViewportSize({ width, height: 900 });
        const layout = await page.evaluate(() => {
          const box = el => el.getBoundingClientRect();
          const header = document.querySelector('.ws-hq-header');
          const content = document.querySelector('.main-content .container-fluid');
          // Every element before the command view in the content column takes
          // no room: nothing renders above the header.
          const above = [];
          for (const child of content.children) {
            if (child.id === 'workspaceCommandView') break;
            if (child.tagName === 'SCRIPT' || child.tagName === 'STYLE') continue;
            // The screen-reader live region is meant to be there, unseen.
            if (child.classList.contains('visually-hidden')) continue;
            if (box(child).height > 0) above.push(child.id || child.className);
          }
          const order = [
            '[data-cmd-hq-needs]',
            '[data-cmd-hq-host="notes"]',
            '[data-cmd-followups-host]',
            '.ws-hq-stations',
            '.ws-hq-rail'
          ].map(selector => document.querySelector(selector));
          const inOrder = order
            .filter(Boolean)
            .every(
              (node, index, nodes) =>
                index === 0 ||
                Boolean(
                  nodes[index - 1].compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING
                )
            );
          const commandFirst = document.querySelector('#workspaceCommandView').firstElementChild;
          const stripNode = document.querySelector('[data-cmd-hq-attention]');
          return {
            above,
            headerTop: box(header).top,
            headerIsFirst: commandFirst === header,
            stripAfterHeader: Boolean(
              header.compareDocumentPosition(stripNode) & Node.DOCUMENT_POSITION_FOLLOWING
            ),
            stripBeforeMain: Boolean(
              stripNode.compareDocumentPosition(order[0]) & Node.DOCUMENT_POSITION_FOLLOWING
            ),
            inOrder,
            present: order.map(Boolean),
            overflow: document.documentElement.scrollWidth > innerWidth + 1
          };
        });
        expect(layout.above, `nothing above the header at ${width}`).toEqual([]);
        expect(layout.headerIsFirst).toBe(true);
        expect(layout.headerTop).toBeLessThan(900);
        expect(layout.stripAfterHeader && layout.stripBeforeMain).toBe(true);
        expect(layout.present[0], 'Needs you').toBe(true);
        expect(layout.present[1], 'Notes host').toBe(true);
        expect(layout.present[2], 'Follow-ups host').toBe(true);
        expect(layout.present[4], 'rail').toBe(true);
        expect(layout.inOrder, `main column then rail, in order, at ${width}`).toBe(true);
        expect(layout.overflow, `no horizontal scroll at ${width}`).toBe(false);
        await page.screenshot({
          path: testInfo.outputPath(`hq-details-${width}-${theme}.png`),
          fullPage: true,
          animations: 'disabled'
        });
      }
      await page.setViewportSize({ width: 1280, height: 900 });

      // Team is the rail's first group and is open: its rows are visible
      // without any toggle, and it has none.
      const team = page.locator('.ws-hq-team');
      await expect(team).toBeVisible();
      await expect(team.getByRole('heading', { name: /^Team/ })).toBeVisible();
      await expect(team.getByRole('button', { name: `Give work to ${agentName}` })).toBeVisible();
      await expect(page.locator('[data-cmd-manage-section="team"]')).toHaveCount(0);
      await expect(page.locator('[data-cmd-manage-section="stations"]')).toHaveCount(0);
      await expect(page.getByRole('button', { name: /^Workspace notes/ })).toBeVisible();

      // More views is a keyboard menu that closes on Escape (FR6).
      const more = page.getByRole('button', { name: 'More views' });
      await expect(more).toHaveAttribute('aria-haspopup', 'menu');
      await more.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('menuitem', { name: 'Canvas' })).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('menu', { name: 'More views' })).toBeHidden();
      await expect(more).toBeFocused();

      // Plain names only, in what is shown and in what is announced (FR23).
      const words = await page.evaluate(() => {
        const root = document.querySelector('#workspaceCommandView');
        const labels = [...root.querySelectorAll('[aria-label]')].map(el =>
          el.getAttribute('aria-label')
        );
        return [root.innerText, ...labels].join('\n');
      });
      for (const term of ['Outpost', 'Commander', 'Garrison', 'Quest Board', 'Loadout', 'Keeper']) {
        expect(words, term).not.toContain(term);
      }

      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const scan = await page.evaluate(async () => {
        const result = await window.axe.run('#workspaceCommandView', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
        return result.violations;
      });
      expect(scan, JSON.stringify(scan, null, 2)).toEqual([]);
    } finally {
      await page.request.delete(`/api/workspaces/${folder.id}`);
    }
  });

  // PRD 767: the Personal HQ's Map opens in Flow; Free is the old field.
  test(`Personal HQ Map: Flow lanes, Free field and the remembered arrangement (${theme})`, async ({
    page
  }, testInfo) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 1440, height: 900 });
    const stamp = `${theme} ${Date.now()}`;
    const response = await page.request.post('/api/workspaces', {
      data: { name: `HQ map ${stamp}`, blank: true }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const { folder } = await response.json();
    const agentName = `HQ Map Lead ${stamp}`;
    expect(
      (await page.request.post('/api/agents', { data: { name: agentName, type: 'general' } })).ok()
    ).toBeTruthy();
    expect(
      (
        await page.request.post(`/api/workspaces/${folder.id}/agents`, {
          data: { agent_name: agentName }
        })
      ).ok()
    ).toBeTruthy();
    // Display fixture only, as above: the HQ designation without building an
    // HQ. It is set again after a reload, which the server never stored.
    const openHQMap = async () => {
      await page.goto(`/workspaces/${folder.folder_slug}`);
      await expect(page.locator('#workspaceCommandView .ws-cmd-title h2')).toBeVisible();
      await page.waitForFunction(() => Boolean(window.workspaceDetail?.workspace?.id));
      await page.evaluate(() => {
        window.workspaceDetail.workspace.designation = 'personal_hq';
        window.workspaceCommand.setCommandViewMode('map', { focus: false });
        window.workspaceCommand.render();
      });
    };
    const scanMap = async label => {
      const violations = await page.evaluate(async () => {
        const result = await window.axe.run('.ws-cmd-map-shell', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
        return result.violations;
      });
      expect(violations, label + ' ' + JSON.stringify(violations, null, 2)).toEqual([]);
    };
    // The whole command view: the header, the map and any open window or
    // modal it hosts, not only the map region.
    const tacticalWords = () =>
      page.evaluate(() => {
        const root = document.querySelector('#workspaceCommandView');
        const labels = [...root.querySelectorAll('[aria-label], [title]')].map(
          el => (el.getAttribute('aria-label') || '') + ' ' + (el.getAttribute('title') || '')
        );
        return [root.innerText, ...labels].join('\n');
      });
    try {
      await openHQMap();
      const operationsMap = page.getByRole('region', { name: 'Workspace operations map' });
      await expect(page.locator('.ws-cmd-map-shell.is-hq.is-flow')).toBeVisible();
      const arrangement = page.getByRole('group', { name: 'Map arrangement' });
      await expect(arrangement.getByRole('button', { name: 'Flow' })).toHaveAttribute(
        'aria-pressed',
        'true'
      );

      // Three labelled lanes, in order, and no belt in Flow (FR5, FR15).
      const lanes = operationsMap.locator('section.ws-cmd-flow-lane');
      await expect(lanes).toHaveCount(3);
      const laneNames = await lanes.evaluateAll(nodes =>
        nodes.map(node => document.getElementById(node.getAttribute('aria-labelledby')).textContent)
      );
      expect(laneNames).toEqual(['What Ori reads', 'Your lead agent', 'What needs you']);
      await expect(operationsMap.locator('.ws-cmd-map-belt')).toHaveCount(0);
      await expect(operationsMap.locator('[style*="--station-x"]')).toHaveCount(0);
      for (const name of ['Memory', 'Needs you', 'Follow-ups', "Today's brief", 'Tickets']) {
        await expect(
          operationsMap.getByRole('button', { name: new RegExp('^' + name) })
        ).toBeVisible();
      }
      await expect(operationsMap.locator('.ws-cmd-flow-lead')).toContainText(agentName);
      await expect(operationsMap.locator('.ws-cmd-flow-lead')).toContainText('Lead');
      for (const term of ['Outpost', 'Commander', 'Garrison', 'Quest Board', 'Loadout', 'Keeper']) {
        expect(await tacticalWords(), term).not.toContain(term);
      }
      // The lead card's Agents window speaks plainly too.
      await operationsMap.locator('.ws-cmd-flow-lead').click();
      await expect(page.locator('.ws-cmd-modal:not([hidden])')).toBeVisible();
      await expect(page.locator('.ws-cmd-modal .ws-cmd-mchip.is-keeper')).toHaveText('★ Lead');
      for (const term of ['Outpost', 'Commander', 'Garrison', 'Quest Board', 'Loadout', 'Keeper']) {
        expect(await tacticalWords(), 'Agents window: ' + term).not.toContain(term);
      }
      await page.keyboard.press('Escape');
      await expect(page.locator('.ws-cmd-modal:not([hidden])')).toHaveCount(0);

      // At 1280 every node is visible without scrolling the map (success metric).
      await page.setViewportSize({ width: 1280, height: 900 });
      const nodesFit = await page.evaluate(() => {
        const shell = document.querySelector('.ws-cmd-opmap').getBoundingClientRect();
        return [...document.querySelectorAll('.ws-cmd-flow-node, .ws-cmd-flow-lead')].every(
          node => {
            const box = node.getBoundingClientRect();
            return box.left >= shell.left && box.right <= shell.right && box.bottom <= shell.bottom;
          }
        );
      });
      expect(nodesFit).toBe(true);

      // A node opens its panel with the map still there behind it, and
      // closing hands focus back to the node (FR14).
      const memory = operationsMap.getByRole('button', { name: /^Memory/ });
      await memory.click();
      await expect(page.locator('.ws-cmd-modal:not([hidden])')).toBeVisible();
      await expect(page.locator('.ws-cmd-opmap')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.locator('.ws-cmd-modal:not([hidden])')).toHaveCount(0);
      await expect(operationsMap.getByRole('button', { name: /^Memory/ })).toBeFocused();
      const needsYou = operationsMap.getByRole('button', { name: /^Needs you/ });
      await needsYou.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('dialog', { name: 'Needs you' })).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('dialog', { name: 'Needs you' })).toHaveCount(0);
      await expect(operationsMap.getByRole('button', { name: /^Needs you/ })).toBeFocused();

      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      for (const width of [1440, 768, 390]) {
        await page.setViewportSize({ width, height: 900 });
        const layout = await page.evaluate(() => ({
          overflow: document.documentElement.scrollWidth > innerWidth + 1,
          linksShown: [...document.querySelectorAll('.ws-cmd-flow-links')].some(
            node => getComputedStyle(node).display !== 'none'
          )
        }));
        expect(layout.overflow, `no horizontal scroll at ${width}`).toBe(false);
        // Below the rail breakpoint the lanes stack with no lines (FR19).
        expect(layout.linksShown, `connectors at ${width}`).toBe(width > 900);
        await scanMap(`flow ${width}`);
        await page.screenshot({
          path: testInfo.outputPath(`hq-map-flow-${width}-${theme}.png`),
          fullPage: true,
          animations: 'disabled'
        });
      }
      await page.setViewportSize({ width: 1440, height: 900 });

      // Free: the field, its buildings and the belt with plain names (FR18).
      await arrangement.getByRole('button', { name: 'Free' }).click();
      await expect(page.locator('.ws-cmd-map-shell.is-hq:not(.is-flow)')).toBeVisible();
      await expect(operationsMap.locator('.ws-cmd-map-belt')).toBeVisible();
      await expect(operationsMap.locator('.ws-cmd-map-hq-station').first()).toBeFocused();
      for (const label of ['Workspace goal', 'Tickets', 'Backlog', 'Inventory', 'Stations']) {
        await expect(
          operationsMap.locator(`.ws-cmd-map-belt-btn[aria-label="${label}"]`)
        ).toHaveCount(1);
      }
      for (const term of ['Outpost', 'Commander', 'Garrison', 'Quest Board', 'Loadout', 'Keeper']) {
        expect(await tacticalWords(), term).not.toContain(term);
      }
      const freeGeometry = await page.evaluate(() => {
        const rects = selector =>
          [...document.querySelectorAll('.ws-cmd-map-shell ' + selector)].map(node => {
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
          stations: rects('.ws-cmd-map-hq-station'),
          switches: rects('.ws-cmd-map-arrangement'),
          fixed: rects('.ws-cmd-map-quest-fab, .ws-cmd-map-belt')
        };
      });
      const overlaps = (a, b) =>
        a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
      for (const agent of freeGeometry.agents) {
        for (const station of freeGeometry.stations) {
          expect(overlaps(agent, station), `${agent.label} overlaps ${station.label}`).toBeFalsy();
        }
      }
      // The switch covers nothing on the field (demo 1.8, defect B).
      for (const control of freeGeometry.switches) {
        for (const other of [
          ...freeGeometry.agents,
          ...freeGeometry.stations,
          ...freeGeometry.fixed
        ]) {
          expect(overlaps(control, other), `switch overlaps ${other.label}`).toBeFalsy();
        }
      }
      await scanMap('free 1440');
      await page.screenshot({
        path: testInfo.outputPath(`hq-map-free-1440-${theme}.png`),
        fullPage: true,
        animations: 'disabled'
      });

      // The choice survives a reload (FR3), and Flow comes back on request.
      await openHQMap();
      await expect(page.locator('.ws-cmd-map-shell.is-hq:not(.is-flow)')).toBeVisible();
      await expect(
        page.getByRole('group', { name: 'Map arrangement' }).getByRole('button', { name: 'Free' })
      ).toHaveAttribute('aria-pressed', 'true');
      await page
        .getByRole('group', { name: 'Map arrangement' })
        .getByRole('button', { name: 'Flow' })
        .click();
      await expect(page.locator('.ws-cmd-map-shell.is-flow')).toBeVisible();
      await expect(page.locator('.ws-cmd-flow-node').first()).toBeFocused();
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
