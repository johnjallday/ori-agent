/*
 * Renders every tracked character asset over the surfaces it has to survive, so
 * transparent art can be judged without booting the app. Used by the per-group
 * Demo: checkpoints of the map-ready asset work.
 *
 *   node scripts/character-preview.mjs [outDir]
 *
 * Writes one contact sheet per surface plus a size ladder, and prints the
 * machine-checkable contract report beside them so the picture and the verdict
 * are produced by the same run.
 *
 * Output defaults to a temp directory: generated evidence must never land in a
 * tracked source path. Pass an explicit directory to keep a set for a PR.
 *
 * Reads internal/web/static/characters/<id>/{portrait,sprite,static}.svg and
 * inlines them, so a broken or missing file shows up as a visible gap rather
 * than a silently blank cell.
 */
import { chromium } from 'playwright';
import { existsSync, readFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { ASSET_CONTRACT, inspectAsset, formatFindings } from './lib/character-asset-contract.mjs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const charDir = join(repoRoot, 'internal/web/static/characters');
const catalogPath = join(repoRoot, 'internal/charactercatalog/catalog.json');
const outDir = resolve(process.argv[2] || join(tmpdir(), 'ori-character-preview'));

if (!existsSync(charDir)) {
  console.error(`no character directory at ${charDir}`);
  process.exit(1);
}
mkdirSync(outDir, { recursive: true });

const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
const ids = catalog.characters.map(c => c.id);

const inline = p => (existsSync(p) ? readFileSync(p, 'utf8') : `<em class="missing">missing</em>`);
const assetPath = (id, variant) => join(charDir, id, `${variant}.svg`);

/* The surfaces a map-ready asset has to stay legible on. The checkerboard is
   the diagnostic one: any baked background shows up as a solid block against
   it, which is the whole point of the transparency work. */
const SURFACES = [
  {
    key: 'checkerboard',
    label: 'Checkerboard (transparency diagnostic)',
    ink: '#111',
    css: `background-color:#fff;
          background-image:
            linear-gradient(45deg,#c9ced6 25%,transparent 25%,transparent 75%,#c9ced6 75%),
            linear-gradient(45deg,#c9ced6 25%,transparent 25%,transparent 75%,#c9ced6 75%);
          background-size:16px 16px; background-position:0 0,8px 8px;`
  },
  { key: 'light', label: 'Light app surface', ink: '#1b2230', css: 'background:#f7f8fa;' },
  { key: 'dark', label: 'Dark app surface', ink: '#e9edf3', css: 'background:#12161c;' },
  { key: 'warm-card', label: 'Warm card', ink: '#3b2a12', css: 'background:#f4ead8;' },
  {
    key: 'map',
    label: 'Workspace map (dark panel, world grid, faction glow)',
    ink: '#dfe7f2',
    // Mirrors .ws-map-canvas: panel gradient, 38px world grid, green glow.
    css: `background-color:#141a23;
          background-image:
            radial-gradient(circle at 50% 30%, rgba(70,211,154,.18), transparent 70%),
            linear-gradient(rgba(255,255,255,.05) 1px, transparent 1px),
            linear-gradient(90deg, rgba(255,255,255,.038) 1px, transparent 1px);
          background-size:100% 100%,38px 38px,38px 38px;`
  }
];

/* Native size, the three named avatar sizes (54/72/88), and the two sizes the
   Ori Guide launcher and panel header actually request (32/40). */
const LADDER = {
  portrait: [48, 54, 72, 88, 160],
  sprite: [32, 40, 48, 54, 72],
  static: [32, 40, 48, 54, 72]
};

const shell = (surface, body) => `<!doctype html><meta charset="utf-8"><style>
  body { margin:0; padding:24px; ${surface.css} color:${surface.ink};
         font:13px/1.4 ui-sans-serif,system-ui,sans-serif; }
  h1 { font-size:14px; margin:0 0 16px; letter-spacing:.02em; }
  figure { margin:0 0 14px; padding:10px 14px; border-radius:10px;
           border:1px solid ${surface.ink}33; }
  figcaption { font-weight:700; margin-bottom:8px; font-size:12px; }
  .row { display:flex; gap:22px; align-items:flex-end; flex-wrap:wrap; }
  .cell { display:flex; flex-direction:column; gap:5px; align-items:center; }
  .cell span { font-size:9px; opacity:.6; text-transform:uppercase; letter-spacing:.07em; }
  .missing { color:#ff5c5c; font-size:11px; }
  svg { display:block; }
</style><body>${body}</body>`;

/* Native-size sheet: every character, every variant, on one surface. */
const nativeSheet = surface =>
  shell(
    surface,
    `<h1>${surface.label}</h1>` +
      ids
        .map(id => {
          const cell = variant =>
            `<div class="cell"><span>${variant}</span>${inline(assetPath(id, variant))}</div>`;
          return `<figure><figcaption>${id}</figcaption><div class="row">
            ${cell('portrait')}${cell('sprite')}${cell('static')}
          </div></figure>`;
        })
        .join('\n')
  );

/* Size ladder: the same art at every size a real surface requests it at. */
const ladderSheet = surface =>
  shell(
    surface,
    `<h1>${surface.label} — size ladder</h1>` +
      ids
        .map(id => {
          const rowFor = variant =>
            LADDER[variant]
              .map(px => {
                const raw = inline(assetPath(id, variant));
                const sized = raw.replace(
                  /<svg([^>]*)\swidth="[\d.]+"\sheight="[\d.]+"/,
                  `<svg$1 width="${px}" height="${px}"`
                );
                return `<div class="cell"><span>${variant.slice(0, 4)} ${px}</span>${sized}</div>`;
              })
              .join('');
          return `<figure><figcaption>${id}</figcaption>
            <div class="row">${rowFor('portrait')}</div>
            <div class="row" style="margin-top:12px">${rowFor('sprite')}${rowFor('static')}</div>
          </figure>`;
        })
        .join('\n')
  );

/* The verdict, printed beside the pictures so a demo cannot show clean-looking
   art while the contract is still failing. */
async function report() {
  const rows = [];
  for (const id of ids) {
    for (const variant of Object.keys(ASSET_CONTRACT)) {
      const { findings } = await inspectAsset(assetPath(id, variant), variant);
      rows.push({ id, variant, findings });
    }
  }
  const failing = rows.filter(r => r.findings.length > 0);
  console.log(
    `\ncontract: ${rows.length - failing.length}/${rows.length} assets transparent and map-ready`
  );
  if (failing.length > 0) {
    console.log(`still carrying a baked background or breaking the safe perimeter:`);
    for (const r of failing) console.log(formatFindings(r.id, r.variant, r.findings));
  }
}

/* Rasterizing a file only ever shows the idle's resting pose, so the safe
   perimeter could still be breached mid-animation by a bill or an ear that
   swings out. This walks each sprite through its own cycle in a real browser,
   freezing the animation at sampled phases and checking the perimeter at each
   one. It is the only check that sees the extremes. */
const MOTION_PHASES = 16;

async function motionReport(browser) {
  const page = await browser.newPage({ viewport: { width: 48, height: 48 } });
  const breaches = [];
  let checked = 0;

  try {
    for (const id of ids) {
      const src = inline(assetPath(id, 'sprite'));
      if (!src.includes('@keyframes')) continue;

      // Longest declared duration is the cycle worth sampling.
      const durations = [...src.matchAll(/animation:\s*[\w-]+\s+([\d.]+)s/g)].map(m =>
        parseFloat(m[1])
      );
      const cycle = durations.length > 0 ? Math.max(...durations) : 1;
      checked++;

      for (let i = 0; i < MOTION_PHASES; i++) {
        const phase = (cycle * i) / MOTION_PHASES;
        await page.setContent(
          `<!doctype html><meta charset="utf-8"><style>
             html,body{margin:0;padding:0;width:48px;height:48px;background:transparent}
             *{animation-delay:-${phase}s !important;animation-play-state:paused !important}
           </style><body>${src}</body>`
        );
        const shot = await page.screenshot({ omitBackground: true });
        const { findings } = await inspectAsset(shot, 'sprite');
        const perimeter = findings.filter(f => f.code === 'perimeter');
        if (perimeter.length > 0) {
          breaches.push(`  ${id}/sprite.svg at ${phase.toFixed(2)}s — ${perimeter[0].detail}`);
          break; // one report per character is enough to send someone to the file
        }
      }
    }
  } finally {
    await page.close();
  }

  console.log(
    `\nmotion: ${checked - breaches.length}/${checked} animated sprites stay inside the safe ` +
      `perimeter across ${MOTION_PHASES} phases of their own cycle`
  );
  for (const line of breaches) console.log(line);
}

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 760, height: 900 } });
  for (const surface of SURFACES) {
    await page.setContent(nativeSheet(surface));
    await page.waitForTimeout(200);
    await page.screenshot({ path: join(outDir, `surface-${surface.key}.png`), fullPage: true });

    await page.setContent(ladderSheet(surface));
    await page.waitForTimeout(200);
    await page.screenshot({ path: join(outDir, `ladder-${surface.key}.png`), fullPage: true });
  }
  console.log(
    `rendered ${ids.length} character(s) over ${SURFACES.length} surface(s) -> ${outDir}`
  );
  await motionReport(browser);
} finally {
  await browser.close();
}

await report();
