import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// Deterministic browser coverage of Home's host-owned faces. The real hire and
// saved appearance were exercised separately in the isolated demo (findings).
const evidence = 'tasks/evidence-one-face-everywhere';
const folders = [
  {
    id: 'chosen',
    name: 'Albums',
    folder_slug: 'albums',
    kind: 'workspace',
    entry_agent_name: 'Albums Manager',
    agents: ['Albums Manager'],
    agent_count: 1,
    ticket_counts: { todo: 0, in_progress: 0, review: 0, done: 0 },
    task_summary_available: true
  },
  {
    id: 'generated',
    name: 'Notes',
    folder_slug: 'notes',
    kind: 'workspace',
    entry_agent_name: 'Notes Keeper',
    agents: ['Notes Keeper'],
    agent_count: 1,
    task_summary_available: true
  }
];

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      json: {
        personal_assistant: {
          state: 'active',
          state_version: 1,
          display_name: 'Atlas',
          assistant_id: 'fixture',
          hq_workspace_id: 'hq',
          availability: { model: { available: false } }
        }
      }
    })
  );
  await page.route('**/api/workspaces?tree=true', route => route.fulfill({ json: { folders } }));
  // Do not inherit the coordinate suite's saved camera: a persisted 10% view
  // can put these deterministic mock tiles outside the visible canvas.
  await page.route('**/api/workspace-map/layout', route =>
    route.fulfill({
      json: { layout: { schema_version: 1, revision: 1, snap_to_grid: true, positions: {} } }
    })
  );
  await page.route('**/api/personal-assistant/folder-agent', route =>
    route.fulfill({ json: { folder_agents: [] } })
  );
});

test('uploaded appearance survives an unavailable role catalog in the panel and idle tile', async ({
  page
}) => {
  await page.route('**/api/agents/catalog', route => route.fulfill({ status: 503, json: {} }));
  await page.route('**/avatars/photo.png', route =>
    route.fulfill({
      path: 'internal/web/static/characters/project-coordinator/portrait.svg',
      contentType: 'image/svg+xml'
    })
  );
  await page.route('**/api/agents/dashboard/list', route =>
    route.fulfill({
      json: [
        {
          name: 'Albums Manager',
          appearance: { mode: 'uploaded', uploaded: { image: 'photo.png' } }
        }
      ]
    })
  );
  await page.goto('/');
  const tile = page.locator('[data-ws-id="chosen"]');
  await expect(tile.locator('.agent-avatar--image img')).toHaveAttribute(
    'src',
    '/avatars/photo.png'
  );
  await tile.click();
  const modal = page.locator('#cockpitContextModal');
  await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
  await expect(modal.locator('.agent-avatar--image img')).toHaveAttribute(
    'src',
    '/avatars/photo.png'
  );
  await expect
    .poll(() =>
      modal
        .locator('.agent-avatar img')
        .evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)
    )
    .toBe(true);
  await page.screenshot({ path: `${evidence}/delivery-uploaded-${test.info().project.name}.png` });
});

test('failed list lookup ends pending slots and preserves the resident fallback', async ({
  page
}) => {
  let reads = 0;
  await page.route('**/api/agents/dashboard/list', route => {
    reads++;
    return route.fulfill({ status: 503, json: {} });
  });
  await page.goto('/');
  await page.locator('[data-ws-id="chosen"]').click();
  const modal = page.locator('#cockpitContextModal');
  await expect(modal.locator('.ws-map-av-figure')).toHaveCount(1);
  await expect(modal.locator('.home-agent-face-slot')).toHaveCount(0);
  await page.evaluate(() => (window as any).OriHomeCockpit.refreshQuietly());
  await expect(modal.locator('.ws-map-av-figure')).toHaveCount(1);
  expect(reads).toBe(1);
});

test('a late character catalog repaints the panel and tile without another agent read', async ({
  page
}) => {
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/api/characters', async route => {
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  let reads = 0;
  await page.route('**/api/agents/dashboard/list', route => {
    reads++;
    return route.fulfill({
      json: [
        {
          name: 'Albums Manager',
          appearance: { mode: 'character', character: { catalog_id: 'project-coordinator' } }
        }
      ]
    });
  });
  await page.goto('/');
  const tile = page.locator('[data-ws-id="chosen"]');
  await tile.click();
  const modal = page.locator('#cockpitContextModal');
  await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
  await expect(modal.locator('[data-aa-requested="character"]')).toHaveCount(1);
  release();
  await expect(modal.locator('.agent-avatar--character')).toHaveCount(1);
  await expect(tile.locator('.agent-avatar--character')).toHaveCount(1);
  expect(reads).toBe(1);
});

test('working tile wears the same chosen face and polling never repeats the agents read', async ({
  page
}) => {
  let agentReads = 0;
  let folderReads = 0;
  await page.route('**/api/agents/dashboard/list', route => {
    agentReads++;
    return route.fulfill({
      json: [
        {
          name: 'Albums Manager',
          appearance: { mode: 'character', character: { catalog_id: 'project-coordinator' } }
        }
      ]
    });
  });
  await page.route('**/api/personal-assistant/folder-agent', route => {
    folderReads++;
    return route.fulfill({
      json: {
        folder_agents: [
          {
            workspace_id: 'chosen',
            agent_name: 'Albums Manager',
            folder_name: 'Albums',
            state: 'looking',
            progress_line: 'Reading the listening notes…'
          }
        ]
      }
    });
  });
  await page.goto('/');
  const tile = page.locator('[data-ws-id="chosen"]');
  await expect(tile.locator('.agent-avatar--character')).toBeVisible();
  await expect(tile.locator('.ws-map-tile-flag')).toContainText('Reading the listening notes…');
  await expect.poll(() => folderReads).toBeGreaterThan(1);
  expect(agentReads).toBe(1);
  await page.screenshot({
    path: `${evidence}/group4-working-tile-${test.info().project.name}.png`
  });
  await tile.click();
  const modal = page.locator('#cockpitContextModal');
  await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
  await expect(modal.locator('.cockpit-rail-next-detail')).toHaveText(
    'Albums Manager: Reading the listening notes…'
  );
  const open = modal.locator('[data-cockpit-rail-open]');
  for (const state of ['normal', 'hover', 'focus']) {
    if (state === 'hover') await open.hover();
    if (state === 'focus') {
      await modal.locator('[data-cockpit-rail-back]').focus();
      const tab = page.context().browser()?.browserType().name() === 'webkit' ? 'Alt+Tab' : 'Tab';
      await page.keyboard.press(tab);
      await expect(open).toBeFocused();
    }
    await expect(open).toHaveCSS('text-decoration-line', 'none');
  }
  await expect(open).toHaveCSS('outline-style', 'solid');
  await page.screenshot({
    path: `${evidence}/group5-next-move-and-link-${test.info().project.name}.png`
  });
});

test('Team orders the commander before the rest and removes case-insensitive duplicates', async ({
  page
}) => {
  await page.route('**/api/workspaces?tree=true', route =>
    route.fulfill({
      json: { folders: [{ ...folders[0], agents: ['Scout', ' albums manager ', 'Writer'] }] }
    })
  );
  await page.route('**/api/agents/dashboard/list', route => route.fulfill({ json: [] }));
  await page.goto('/');
  await page.locator('[data-ws-id="chosen"]').click();
  await expect(page.locator('[data-cockpit-rail-back]')).toBeFocused();
  const team = page.getByRole('region', { name: 'Team', exact: true });
  await expect(team.locator('.ws-map-av-label')).toHaveText(['Albums Manager', 'Scout', 'Writer']);
  await expect(team.locator('.cockpit-rail-commander-badge')).toHaveCount(1);
  await page.screenshot({
    path: `${evidence}/group3-several-agents-${test.info().project.name}.png`
  });
});

for (const width of [640, 420]) {
  test(`document drawer overlap at saved width ${width} (placement cut line)`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.addInitScript(
      width => localStorage.setItem('ori.assistant.drawer.width', String(width)),
      width
    );
    await page.route('**/api/agents/dashboard/list', route => route.fulfill({ json: [] }));
    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() => (window as any).PersonalAssistantPanel.open());
    const drawer = page.locator('#personalAssistantPanel');
    await expect(drawer).toBeVisible();
    await page.locator('[data-ws-id="generated"]').click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
    const panelBox = await modal.locator('.modal-dialog').boundingBox();
    const drawerBox = await drawer.boundingBox();
    console.log('placement cut line', { width, panelBox, drawerBox });
    await page.screenshot({
      path: `${evidence}/group5-drawer-${width}-${test.info().project.name}.png`
    });
  });
}

for (const theme of ['light', 'dark']) {
  test(`panel chosen/generated/pending faces, stable focus (${theme})`, async ({ page }) => {
    await page.addInitScript(theme => {
      localStorage.setItem('ori-theme', theme);
    }, theme);
    let release!: () => void;
    const held = new Promise<void>(resolve => {
      release = resolve;
    });
    let reads = 0;
    await page.route('**/api/agents/dashboard/list', async route => {
      reads++;
      await held;
      await route.fulfill({
        json: {
          agents: [
            {
              name: 'Albums Manager',
              role: 'orchestrator',
              appearance: { mode: 'character', character: { catalog_id: 'project-coordinator' } }
            },
            { name: 'Notes Keeper', appearance: { mode: 'generated' } }
          ]
        }
      });
    });
    await page.goto('/');
    await page.locator('[data-ws-id="chosen"]').click();
    const modal = page.locator('#cockpitContextModal');
    await expect(modal).toBeVisible();
    await expect(modal.locator('.home-agent-face-slot')).toHaveCount(1);
    const tile = page.locator('[data-ws-id="chosen"]');
    await expect(tile.locator('.home-agent-face-slot')).toHaveCount(1);
    await expect(tile.locator('.agent-avatar, .ws-map-av-figure')).toHaveCount(0);
    await expect(modal.locator('.agent-avatar, .ws-map-av-figure')).toHaveCount(0);
    const open = modal.locator('[data-cockpit-rail-open]');
    await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
    await open.focus();
    const live = await page
      .locator('#cockpitRailLive')
      .textContent()
      .catch(() => '');
    release();
    await expect(modal.locator('.agent-avatar--character')).toHaveCount(1);
    await expect(tile.locator('.agent-avatar--character')).toHaveCount(1);
    expect(await tile.locator('.agent-avatar img').getAttribute('src')).toBe(
      await modal.locator('.agent-avatar img').getAttribute('src')
    );
    await expect(open).toBeFocused();
    if (live) await expect(page.locator('#cockpitRailLive')).toHaveText(live);
    mkdirSync(evidence, { recursive: true });
    await page.screenshot({
      path: `${evidence}/group3-team-character-${theme}-${test.info().project.name}.png`
    });
    await modal.getByRole('button', { name: 'Close context', exact: true }).click();
    await expect(modal).toBeHidden();
    await page.screenshot({
      path: `${evidence}/group4-idle-map-${theme}-${test.info().project.name}.png`
    });
    await page.locator('[data-ws-id="generated"]').click();
    await expect(modal).toBeVisible();
    await expect(modal.locator('[data-cockpit-rail-back]')).toBeFocused();
    await expect(modal.locator('.ws-map-av-figure')).toHaveCount(1);
    await expect(modal.locator('.agent-avatar')).toHaveCount(0);
    await expect(modal.getByRole('region', { name: 'Team', exact: true })).toBeVisible();
    await expect(modal.locator('.cockpit-rail-commander-badge')).toHaveText('Commander');
    expect(reads).toBe(1);
    await page.screenshot({
      path: `${evidence}/group3-team-generated-${theme}-${test.info().project.name}.png`
    });
  });
}
