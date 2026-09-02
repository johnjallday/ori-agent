import { chromium } from "playwright";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const designDir = dirname(fileURLToPath(import.meta.url));
const prototypeURL = pathToFileURL(join(designDir, "building-blueprint-prototype.html")).href;

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const browser = await chromium.launch({ headless: true });

try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  await page.goto(prototypeURL);
  await page.waitForTimeout(500);

  assert(
    (await page.locator("#campusTitle").textContent()) === "Your Ori Campus",
    "Prototype should open on the workspace campus"
  );
  assert(await page.locator("#catalogPanel").isVisible(), "Build Catalog should be visible initially");
  assert(
    await page.locator('[data-blueprint="calendar"]').evaluate(element =>
      element.classList.contains("is-selected")
    ),
    "Schedule Station should be the initial recommended selection"
  );
  assert(await page.locator("#buildPreview").isVisible(), "Selected blueprint should preview on the map");

  await page.locator('[data-category="utilities"]').click();
  assert(
    (await page.locator("#blueprintGrid .blueprint-card").count()) === 2,
    "Utilities category should show the two utility blueprints"
  );
  assert(
    (await page.locator("#blueprintGrid").textContent()).includes("Sorting Depot"),
    "Utilities should include Sorting Depot"
  );

  await page.locator('[data-category="all"]').click();
  await page.locator('[data-blueprint="calendar"]').click();
  await page.locator("#blueprintDetails").click();
  assert(
    (await page.locator("#toastStack").textContent()).includes("Scheduler + Meeting Prep"),
    "Crew and access review should explain the selected blueprint"
  );

  await page.screenshot({
    path: join(designDir, "building-blueprint-prototype.png"),
    fullPage: true
  });

  await page.locator("#buildSelected").click();
  assert(await page.locator("#contextPanel").isVisible(), "Building should return to station context");
  assert(
    (await page.locator("#contextTitle").textContent()) === "Schedule Station",
    "The built station should become the selected context"
  );
  assert(
    (await page.locator('[data-facility="calendar"] .facility-status').textContent()) ===
      "Setup required",
    "A new station must be explicit that guided setup is still required"
  );
  assert(
    (await page.locator("#toastStack").textContent()).includes("connections stay off"),
    "Build receipt should state that connections were not enabled"
  );

  await page.locator("#contextBuild").click();
  assert(await page.locator("#catalogPanel").isVisible(), "Context should reopen the Build Catalog");
  await page.locator("#closeCatalog").click();
  assert(await page.locator("#contextPanel").isVisible(), "Closing catalog should restore station context");
  assert(!(await page.locator("#buildPreview").isVisible()), "Closing catalog should clear placement preview");

  const desktopViewport = await page.evaluate(() => ({
    horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth,
    verticalOverflow: document.documentElement.scrollHeight > window.innerHeight
  }));
  assert(!desktopViewport.horizontalOverflow, "Desktop prototype should not overflow horizontally");
  assert(!desktopViewport.verticalOverflow, "Desktop prototype should not overflow vertically");

  const mobile = await browser.newPage({ viewport: { width: 430, height: 900 } });
  await mobile.goto(prototypeURL);
  await mobile.waitForTimeout(400);
  assert(await mobile.locator("#catalogPanel").isVisible(), "Mobile should open on the Build Catalog");
  assert(
    (await mobile.locator("#blueprintGrid .blueprint-card").count()) === 8,
    "Mobile catalog should retain every blueprint"
  );
  await mobile.screenshot({
    path: join(designDir, "building-blueprint-prototype-mobile.png"),
    fullPage: true
  });
  await mobile.locator("#closeCatalog").click();
  assert(!(await mobile.locator("#sidePanel").isVisible()), "Mobile close should uncover the map");
  await mobile.locator("#openCatalog").click();
  assert(await mobile.locator("#sidePanel").isVisible(), "Mobile Build should restore the catalog");

  const mobileViewport = await mobile.evaluate(() => ({
    horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth
  }));
  assert(!mobileViewport.horizontalOverflow, "Mobile prototype should not overflow horizontally");

  console.log("PASS: building blueprint catalog, placement, build receipt, and responsive layout");
} finally {
  await browser.close();
}
