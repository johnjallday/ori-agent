/*
 * Drives the GitHub Ops setup wizard's Connect step and screenshots it, for
 * the Demo: checkpoint.
 *
 *   node scripts/demo-github-wizard.mjs <baseUrl> <outDir> <workspaceId> [token]
 *
 * With a token it also exercises the connect action, so the screenshot pair
 * shows the step before and after connecting. Without one it captures the
 * disconnected state only.
 */
import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const [baseUrl, outDir, workspaceId, token] = process.argv.slice(2);
if (!baseUrl || !outDir || !workspaceId) {
  console.error("usage: node scripts/demo-github-wizard.mjs <baseUrl> <outDir> <workspaceId> [token]");
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });

const browser = await chromium.launch();
const problems = [];
let failures = 0;

const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.on("console", m => {
  if (m.type() === "error") problems.push(`console: ${m.text()}`);
});
page.on("requestfailed", r => problems.push(`request failed: ${r.url()}`));

async function shot(name) {
  const dialog = page.locator("dialog[open]").first();
  const file = join(resolve(outDir), `${name}.png`);
  if (await dialog.count()) {
    await dialog.screenshot({ path: file });
  } else {
    await page.screenshot({ path: file });
  }
  console.log(`  wrote ${name}.png`);
}

try {
  await page.goto(`${baseUrl}/workspaces/${workspaceId}`, { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(2500);

  // Open the wizard from its banner.
  const openBtn = page.getByRole("button", { name: /continue setup|repair setup|view setup/i });
  if (await openBtn.count()) {
    await openBtn.first().click();
    await page.waitForTimeout(1200);
  }

  await shot("wizard-connect-step");

  const label = await page
    .locator("dialog[open] button")
    .filter({ hasText: /connect github|approve and continue/i })
    .first()
    .textContent()
    .catch(() => null);
  console.log(`  primary button: ${JSON.stringify((label || "").trim())}`);

  const field = page.locator("#githubSetupToken");
  console.log(`  token field present: ${(await field.count()) > 0}`);

  if (token && (await field.count())) {
    await field.fill(token);
    await page
      .locator("dialog[open] button")
      .filter({ hasText: /connect github/i })
      .first()
      .click();
    await page.waitForTimeout(4000);
    await shot("wizard-after-connect");
  }

  if (problems.length) {
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
