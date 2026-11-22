import { test, expect, type Locator } from '@playwright/test';
import { join } from 'node:path';

const assets = 'internal/web/static';
const modules = `${assets}/js/modules`;

test('Home map tiles keep portraits, buildings and update labels in separate slots', async ({
  page
}, info) => {
  // Exercise production markup and CSS in the browser without a server, saved
  // workspaces, or provider calls. Use saved positions exactly one cell apart.
  await page.route('https://map.test/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/') {
      return route.fulfill({
        contentType: 'text/html',
        body: '<!doctype html><main class="home-cockpit"><div class="cockpit-map"><div class="ws-map"><div class="ws-map-canvas" style="height: 510px"><div class="ws-map-world"></div></div></div></div></main>'
      });
    }
    const asset = [
      '/workspace-update-signals.js',
      '/workspace-routes.js',
      '/ticket-count-line.js'
    ].find(p => p === path);
    return asset
      ? route.fulfill({ path: join(modules, asset), contentType: 'text/javascript' })
      : route.abort();
  });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 620, height: 540 });
  await page.goto('https://map.test/');
  for (const path of [
    'node_modules/bootstrap/dist/css/bootstrap.min.css',
    `${assets}/css/variables.css`,
    `${assets}/css/workspace-map.css`,
    `${assets}/css/home-workspace-cockpit.css`
  ]) {
    await page.addStyleTag({ path });
  }
  for (const name of ['workspace-building-art', 'workspace-agent-portrait', 'workspace-map']) {
    await page.addScriptTag({ path: `${modules}/${name}.js` });
  }
  await page.evaluate(async () => {
    await import('/workspace-update-signals.js');
    await import('/ticket-count-line.js');
    const map = (window as any).OriWorkspaceMap;
    map.setHQStatus({ valid: true, workspace_id: 'hq' });
    const common = { ops_mode: 'guided', ticket_counts: { review: 11, in_progress: 3, ready: 24 } };
    const workspaces = [
      {
        ...common,
        id: 'hq',
        name: 'My HQ',
        agent_count: 2,
        entry_agent_name: 'Assistant',
        agents: ['Journal']
      },
      {
        ...common,
        id: 'downloads',
        name: 'Downloads',
        shape: 'anchored',
        agent_count: 4,
        entry_agent_name: 'Filing',
        agents: ['Notes', 'Research', 'Reports']
      },
      { ...common, id: 'beside', name: 'Next door' }
    ];
    const updates = {
      workspace_id: 'downloads',
      folder_slug: 'downloads',
      name: 'Downloads',
      welcome: {
        state: 'ready',
        unread_count: 1,
        session_id: 'welcome-session',
        agent_instance_id: 'filing',
        messages: [{ message_id: 'hello' }]
      },
      review: { state: 'ready', pending_count: 24, root_id: 'root', batch_id: 'batch' }
    };
    const signals = (window as any).OriWorkspaceUpdateSignals;
    map.workspaceUpdates.setSnapshot({
      downloads: signals.view({ state: 'ready', data: updates })
    });
    map.songFacts.setSnapshot({ hq: '12 tracks · 128.5 BPM · 1:02:05' });
    const layout = map.computeWorldLayout(workspaces, {
      positions: { hq: { x: 80, y: 40 }, downloads: { x: 80, y: 210 }, beside: { x: 256, y: 210 } }
    });
    document.querySelector('.ws-map-world')!.innerHTML = layout.nodes
      .map(({ id, x, y }) =>
        map.tileHTML({ ws: workspaces.find(ws => ws.id === id), left: x, top: y }, 'downloads', 0, {
          agentPortraitHTML: map.agentPortraitHTML
        })
      )
      .join('');
  });

  const hq = page.locator('[data-ws-id="hq"]');
  const downloads = page.locator('[data-ws-id="downloads"]');
  const beside = page.locator('[data-ws-id="beside"]');
  await expect(hq.locator('.ws-map-tile-team .ws-map-av')).toHaveCount(2);
  await expect(downloads.locator('.ws-map-tile-team .ws-map-av')).toHaveCount(1);
  await expect(downloads.locator('.ws-map-tile-team-more')).toHaveText('+3');
  await expect(downloads.locator('[data-map-update-route]')).toHaveCount(2);
  await expect(downloads).toHaveAttribute('aria-label', /24 ready.*24 files to review/);

  const box = async (locator: Locator) => (await locator.boundingBox())!;
  for (const [theme, zoom] of [
    ['dark', 1],
    ['dark', 0.65],
    ['light', 1.25]
  ] as const) {
    await page.evaluate(
      ([theme, zoom]) => {
        document.documentElement.dataset.bsTheme = theme;
        (document.querySelector('.ws-map-world') as HTMLElement).style.transform = `scale(${zoom})`;
      },
      [theme, zoom]
    );
    for (const tile of [hq, downloads]) {
      const flag = await box(tile.locator('.ws-map-tile-flag'));
      const art = await box(tile.locator('.ws-map-struct'));
      const team = await box(tile.locator('.ws-map-tile-team'));
      const name = await box(tile.locator('.ws-map-tile-name'));
      expect(flag.y + flag.height).toBeLessThanOrEqual(team.y + 0.1);
      expect(art.x + art.width).toBeLessThan(team.x);
      expect(team.y + team.height).toBeLessThan(name.y);
    }
    const flag = await box(hq.locator('.ws-map-tile-status'));
    const hqBadge = await box(hq.locator('.ws-map-tile-hq-badge'));
    expect(flag.x + flag.width).toBeLessThan(hqBadge.x);
    expect(hqBadge.y + hqBadge.height).toBeLessThan((await box(hq.locator('.ws-map-tile-team'))).y);
    const review = await box(downloads.getByText('24 files to review', { exact: true }));
    const caption = await box(downloads.locator('.ws-map-tile-meta'));
    expect(caption.y + caption.height).toBeLessThan(review.y);
    const above = await box(hq);
    const below = await box(downloads);
    expect(above.y + above.height).toBeLessThan(below.y);
    expect(below.height / zoom).toBeLessThan(170);
    expect(below.x + below.width).toBeLessThan((await box(beside)).x);
  }
  await page.screenshot({ path: info.outputPath('workspace-tile-spacing.png') });
});
