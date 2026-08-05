/*
 * Renders every tracked character asset into one contact sheet so the art can be
 * eyeballed without booting the app. Used by the per-group Demo: checkpoints.
 *
 *   node scripts/character-preview.mjs [outfile.png]
 *
 * Reads internal/web/static/characters/<id>/{portrait,sprite,static}.svg and
 * inlines them, so a broken or missing file shows up as a visible gap rather
 * than a silently blank cell.
 */
import { chromium } from "playwright";
import { readdirSync, existsSync, readFileSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const charDir = join(repoRoot, "internal/web/static/characters");
const out = resolve(process.argv[2] || join(repoRoot, "character-preview.png"));

if (!existsSync(charDir)) {
  console.error(`no character directory at ${charDir}`);
  process.exit(1);
}

const ids = readdirSync(charDir)
  .filter(d => existsSync(join(charDir, d)))
  .sort();

const inline = p =>
  existsSync(p) ? readFileSync(p, "utf8") : `<em class="missing">missing</em>`;

const cards = ids
  .map(id => {
    const dir = join(charDir, id);
    return `<figure>
      <figcaption>${id}</figcaption>
      <div class="row">
        <div class="cell"><span>portrait</span>${inline(join(dir, "portrait.svg"))}</div>
        <div class="cell"><span>sprite</span>${inline(join(dir, "sprite.svg"))}</div>
        <div class="cell"><span>static</span>${inline(join(dir, "static.svg"))}</div>
      </div>
    </figure>`;
  })
  .join("\n");

const html = `<!doctype html><meta charset="utf-8"><style>
  body { margin:0; padding:24px; background:#20242b; color:#e9edf3;
         font:13px/1.4 ui-sans-serif,system-ui,sans-serif; }
  figure { margin:0 0 18px; background:#2a2f38; border-radius:10px; padding:12px 16px; }
  figcaption { font-weight:700; margin-bottom:10px; letter-spacing:.02em; }
  .row { display:flex; gap:24px; align-items:flex-end; }
  .cell { display:flex; flex-direction:column; gap:6px; align-items:center; }
  .cell span { font-size:10px; opacity:.55; text-transform:uppercase; letter-spacing:.08em; }
  .missing { color:#ff9c9c; font-size:11px; }
  svg { display:block; }
</style><body>${cards}</body>`;

const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 620, height: 900 } });
  await page.setContent(html);
  await page.waitForTimeout(250);
  await page.screenshot({ path: out, fullPage: true });
  console.log(`rendered ${ids.length} character(s) -> ${out}`);
} finally {
  await browser.close();
}
