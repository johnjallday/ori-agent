/*
 * Drives the character identity surfaces on a running demo server, for the
 * per-group Demo: checkpoints of the map-ready asset work.
 *
 *   node scripts/demo-characters.mjs <baseUrl> <outDir> [characterId ...]
 *
 * Seeds one real agent per named character (all assignable ones by default) via
 * the same POST /api/agents contract the UI uses, then screenshots the Agents
 * gallery, list, and inspector plus the Ori Guide launcher and panel, in both
 * themes and at both widths.
 *
 * Written because scripts/demo-shot.mjs and scripts/demo-themes.mjs both hang
 * on Playwright's implicit document.fonts.ready wait against this build; the
 * shot() helper below races that wait instead of blocking on it.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, ...only] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-characters.mjs <baseUrl> <outDir> [characterId ...]');
  process.exit(1);
}
const dir = resolve(outDir);
mkdirSync(dir, { recursive: true });

const problems = [];

/* Seed an agent per character so the identity surfaces have real data to show.
   Ori is deliberately not seedable: the API rejects the reserved guide ID, which
   is itself worth exercising. */
async function seedAgents() {
  const catalog = await fetch(`${baseUrl}/api/characters`).then(r => r.json());
  const entries = (catalog.characters || catalog || []).filter(
    c => c.kind !== 'guide' && (only.length === 0 || only.includes(c.id))
  );

  const existing = await fetch(`${baseUrl}/api/agents`)
    .then(r => r.json())
    .then(list => new Set((Array.isArray(list) ? list : list.agents || []).map(a => a.name)))
    .catch(() => new Set());

  const seeded = [];
  for (const entry of entries) {
    const name = `Demo ${entry.name}`;
    if (existing.has(name)) {
      seeded.push(name);
      continue;
    }
    const res = await fetch(`${baseUrl}/api/agents`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        name,
        model: 'gpt-5.3',
        character: { catalog_id: entry.id, display_mode: 'character' }
      })
    });
    if (!res.ok) {
      problems.push(`seed ${entry.id}: HTTP ${res.status} ${await res.text()}`);
      continue;
    }
    seeded.push(name);
  }
  console.log(`seeded ${seeded.length} character agent(s): ${seeded.join(', ')}`);
  return seeded;
}

const VIEWS = [
  { name: 'wide', width: 1440, height: 950 },
  { name: 'narrow', width: 390, height: 844 }
];

/* page.screenshot() blocks on document.fonts.ready, which never settles on this
   build, so every existing demo driver times out here. Capture through CDP
   instead: it takes the frame as it currently stands, which is exactly what a
   visual check wants. */
async function shot(page, name) {
  const client = await page.context().newCDPSession(page);
  try {
    const { data } = await client.send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(join(dir, `${name}.png`), Buffer.from(data, 'base64'));
  } finally {
    await client.detach().catch(() => {});
  }
}

const browser = await chromium.launch();
try {
  await seedAgents();

  for (const theme of ['light', 'dark']) {
    for (const view of VIEWS) {
      const page = await browser.newPage({
        viewport: { width: view.width, height: view.height }
      });
      page.on('console', m => {
        if (m.type() === 'error') problems.push(`${theme}/${view.name}: ${m.text()}`);
      });
      page.on('requestfailed', r => {
        // The stalled update check is this build's own noise, not the feature's.
        if (!r.url().includes('api/update')) problems.push(`failed: ${r.url()}`);
      });

      await page.addInitScript(t => window.localStorage.setItem('ori-theme', t), theme);
      await page.goto(`${baseUrl}/agents`, { waitUntil: 'commit', timeout: 20000 });
      // The roster renders client-side; shooting before it lands captures the
      // "Loading your agents…" placeholder instead of the identities.
      await page
        .locator('.agent-card__name')
        .first()
        .waitFor({ state: 'visible', timeout: 20000 })
        .catch(() => problems.push(`${theme}/${view.name}: agent cards never rendered`));
      await page.waitForTimeout(700);
      await shot(page, `agents-gallery-${theme}-${view.name}`);

      // The list view renders the same identities at a denser size.
      const listToggle = page.locator('[data-view="list"], #agentsViewList').first();
      if (await listToggle.count()) {
        await listToggle.click().catch(() => {});
        await page.waitForTimeout(900);
        await shot(page, `agents-list-${theme}-${view.name}`);
      }

      // Inspector: the largest portrait any surface shows.
      const card = page.locator('.agent-card__name').first();
      if (await card.count()) {
        await card.click().catch(() => {});
        await page.waitForTimeout(1200);
        await shot(page, `agents-inspector-${theme}-${view.name}`);
      }

      // Ori Guide: launcher pill closed, then the panel open.
      const launcher = page.locator('#oriGuideLauncher');
      if ((await launcher.count()) && (await launcher.isVisible().catch(() => false))) {
        await shot(page, `ori-launcher-${theme}-${view.name}`);
        await launcher.click().catch(() => {});
        await page.waitForTimeout(700);
        await shot(page, `ori-panel-${theme}-${view.name}`);
      }

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth
      );
      if (overflow > 1)
        problems.push(`${theme}/${view.name}: page scrolls sideways by ${overflow}px`);

      await page.close();
    }
  }

  // Reduced motion: every sprite must fall back to its motionless variant.
  const rm = await browser.newPage({
    viewport: { width: 1440, height: 950 },
    reducedMotion: 'reduce'
  });
  await rm.goto(`${baseUrl}/agents`, { waitUntil: 'commit', timeout: 20000 });
  await rm.waitForTimeout(1800);
  await shot(rm, 'agents-reduced-motion');
  await rm.close();

  console.log(
    problems.length ? `problems:\n  ${problems.join('\n  ')}` : 'no console/network errors'
  );
} finally {
  await browser.close();
}
