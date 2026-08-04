/*
 * Screenshots pages from a running demo server, for the per-group Demo:
 * checkpoints in tasks/tasks-cozy-character-experience.md.
 *
 *   node scripts/demo-shot.mjs <baseUrl> <outDir> <spec> [<spec> ...]
 *
 * A spec is "name:path[:width x height]", e.g.
 *   agents:/agents               -> agents.png at 1440x900
 *   agents-narrow:/agents:390x844
 *
 * Prints any console errors and failed requests it sees, so a page that renders
 * but is quietly broken does not pass as a clean demo.
 */
import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const [baseUrl, outDir, ...specs] = process.argv.slice(2);
if (!baseUrl || !outDir || specs.length === 0) {
  console.error("usage: node scripts/demo-shot.mjs <baseUrl> <outDir> <name:path[:WxH]> ...");
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const browser = await chromium.launch();
let failures = 0;

try {
  for (const spec of specs) {
    const [name, path, size] = spec.split(":");
    const [w, h] = (size || "1440x900").split("x").map(Number);

    const page = await browser.newPage({ viewport: { width: w, height: h } });
    const problems = [];
    page.on("console", m => {
      if (m.type() === "error") problems.push(`console: ${m.text()}`);
    });
    page.on("requestfailed", r => problems.push(`request failed: ${r.url()}`));
    page.on("response", r => {
      if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.url()}`);
    });

    await page.goto(`${baseUrl}${path}`, { waitUntil: "networkidle" });
    await page.waitForTimeout(900);

    const file = join(resolve(outDir), `${name}.png`);
    await page.screenshot({ path: file, fullPage: true });

    if (problems.length) {
      failures++;
      console.log(`\n${name} (${path}) — ${problems.length} problem(s):`);
      for (const p of [...new Set(problems)].slice(0, 12)) console.log(`  ${p}`);
    } else {
      console.log(`${name} (${path}) — clean`);
    }
    await page.close();
  }
} finally {
  await browser.close();
}

console.log(failures ? `\n${failures} page(s) reported problems` : "\nall pages clean");
