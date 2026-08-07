/*
 * Drives the GitHub Account card in Settings against a running demo server and
 * screenshots each state, for the per-group Demo: checkpoints in
 * tasks/tasks-github-ops-template.md.
 *
 *   node scripts/demo-github.mjs <baseUrl> <outDir>
 *
 * The settings page switches sections with JS rather than plain anchors, and a
 * first-run onboarding overlay sits on top of everything, so a bare navigate to
 * /settings#github-account screenshots the wrong thing. This dismisses the
 * overlay, clicks into the section, and captures the card itself.
 *
 * Prints console errors and failed requests, so a card that renders but is
 * quietly broken does not pass as a clean demo.
 */
import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const [baseUrl, outDir] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error("usage: node scripts/demo-github.mjs <baseUrl> <outDir>");
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const browser = await chromium.launch();
const problems = [];
let failures = 0;

const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.on("console", m => {
  if (m.type() === "error") problems.push(`console: ${m.text()}`);
});
page.on("requestfailed", r => problems.push(`request failed: ${r.url()}`));
page.on("response", r => {
  if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.url()}`);
});

// dismissOverlays clears the first-run onboarding wizard, which otherwise
// covers the settings content and swallows clicks.
async function dismissOverlays() {
  for (const label of ["Set Up Later", "Skip", "Close"]) {
    const button = page.getByRole("button", { name: label });
    if (await button.count()) {
      await button.first().click({ timeout: 2000 }).catch(() => {});
      await page.waitForTimeout(300);
    }
  }
  // Belt and braces: hide anything still painting over the page.
  await page.evaluate(() => {
    document
      .querySelectorAll(".onboarding-overlay, .modal-backdrop, [data-onboarding-overlay]")
      .forEach(el => el.remove());
  });
}

async function shot(name) {
  const card = page.locator("#github-account");
  await card.scrollIntoViewIfNeeded();
  await page.waitForTimeout(400);
  const file = join(resolve(outDir), `${name}.png`);
  await card.screenshot({ path: file });
  console.log(`  wrote ${name}.png`);
}

try {
  await page.goto(`${baseUrl}/settings`, { waitUntil: "networkidle" });
  await dismissOverlays();

  // The nav item drives the section switch.
  await page.getByRole("link", { name: "GitHub Account" }).click();
  await page.waitForTimeout(800);

  await shot("settings-github");

  if (problems.length) {
    failures += 1;
    console.log("  problems:");
    for (const p of problems) console.log(`    ${p}`);
  } else {
    console.log("  clean");
  }
} catch (err) {
  failures += 1;
  console.error(`  FAILED: ${err.message}`);
} finally {
  await browser.close();
}

process.exit(failures > 0 ? 1 : 0);
