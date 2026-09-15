/*
 * Drives the Email Ops host setup quest (#455) against a running, isolated demo
 * server and saves screenshots. It uses only real UI and real endpoints; the
 * one shortcut is skipping first-run onboarding through its own API, which the
 * demo sandbox owns.
 *
 *   node scripts/demo-455-email-quest.mjs <baseUrl> <outDir> <stage>
 *
 * Stages:
 *   team     Templates → Open Guided Setup → step 1 → creator Team step (Inbox
 *            lock) → create through POST /api/workspaces → quest re-reads.
 *   resume   Reopen the quest by URL after a restart and report its state.
 *   narrow   Reopen the quest at 400px width.
 *
 * Every stage prints the quest status it observed and any console errors or
 * failed requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import { join, resolve } from "node:path";

const [baseUrl, outDir, stage = "team"] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error("usage: node scripts/demo-455-email-quest.mjs <baseUrl> <outDir> <team|resume|narrow>");
  process.exit(1);
}
const out = resolve(outDir);
mkdirSync(out, { recursive: true });
const questURL = "/?setup=quest&source=host&quest=email_ops_setup";

const browser = await chromium.launch();
const problems = [];
let exitCode = 0;

async function newPage(width, height) {
  const page = await browser.newPage({ viewport: { width, height } });
  page.on("console", m => {
    if (m.type() === "error") problems.push(`console: ${m.text()}`);
  });
  page.on("pageerror", e => problems.push(`pageerror: ${e.message}`));
  page.on("response", r => {
    if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.request().method()} ${r.url()}`);
  });
  return page;
}

async function shot(page, name) {
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file });
  console.log(`screenshot ${file}`);
}

async function status(page) {
  return page.evaluate(async () => {
    const response = await fetch("/api/host-setup-quests/email_ops_setup/status");
    return response.json();
  });
}

function summarize(label, body) {
  const journey = body?.setup_journey;
  const steps = (journey?.steps || []).map(s => `${s.id}=${s.status}${s.reason_code ? `(${s.reason_code})` : ""}`);
  console.log(
    `${label}: exists=${body?.exists} lifecycle=${journey?.lifecycle_state || "-"} current=${journey?.current_step_id || "-"} ` +
      `workspace=${journey?.receipts?.project_workspace_id || "-"} steps=[${steps.join(", ")}]`
  );
}

async function waitForJourney(page) {
  await page.locator("#specialistSetupJourneyModal.show").waitFor({ state: "visible", timeout: 15000 });
  await page.waitForTimeout(500);
}

try {
  const setup = await newPage(1440, 900);
  await setup.goto(`${baseUrl}/templates`, { waitUntil: "domcontentloaded" });
  await setup.evaluate(async () => {
    await fetch("/api/onboarding/skip", { method: "POST" });
  });

  if (stage === "team") {
    const page = setup;
    await page.goto(`${baseUrl}/templates`, { waitUntil: "domcontentloaded" });
    await page.locator('#tplList button[role="listitem"]', { hasText: "Email Ops" }).first().click();
    const open = page.locator("#tplQuestOpen");
    await open.waitFor({ state: "visible", timeout: 15000 });
    console.log(`templates: ownership="${await page.locator("#tplQuestOwnership").textContent()}" href=${await open.getAttribute("href")}`);
    summarize("status before open", await status(page));
    await page.locator("#tplSetupQuest").scrollIntoViewIfNeeded();
    await shot(page, "01-templates-open-guided-setup");

    await open.click();
    await waitForJourney(page);
    summarize("status after open", await status(page));
    await shot(page, "02-quest-step1-review-team");

    await page.locator('#specialistSetupJourneyActions [data-action="review_team"]').click();
    await page.locator("#addFolderModal.show").waitFor({ state: "visible", timeout: 15000 });
    await page.locator("#wizardStep3:not([hidden])").waitFor({ state: "visible", timeout: 20000 });
    await page.waitForTimeout(800);
    console.log(`creator: name="${await page.locator("#folderNameInput").inputValue()}"`);
    await shot(page, "03-creator-team-step");

    // The quest stages both roles; Inbox cannot be cleared or renamed.
    const roles = await page.evaluate(() =>
      [...document.querySelectorAll("#workspaceRoleRoster [data-role-id]")].map(row => ({
        id: row.dataset.roleId,
        text: row.textContent.replace(/\s+/g, " ").trim().slice(0, 80)
      }))
    );
    console.log(`roles: ${JSON.stringify(roles)}`);
    const inboxRow = page.locator('#workspaceRoleRoster [data-role-id="inbox"]');
    await inboxRow.scrollIntoViewIfNeeded();
    await inboxRow.getByRole("button", { name: "Clear" }).click();
    await page.waitForTimeout(600);
    const stillFilled = await inboxRow.evaluate(row => /New agent/.test(row.textContent));
    const toast = await page.evaluate(() =>
      [...document.querySelectorAll(".toast, [role='status'], [role='alert']")]
        .map(node => node.textContent.replace(/\s+/g, " ").trim())
        .filter(text => /Inbox/.test(text))
        .slice(0, 2)
    );
    console.log(`inbox clear refused: stillFilled=${stillFilled} messages=${JSON.stringify(toast)}`);
    await shot(page, "04-inbox-role-locked");

    await page.locator("#wizardNextBtn").click();
    await page.locator("#createFolderBtn:not([hidden])").waitFor({ state: "visible", timeout: 15000 });
    await page.waitForTimeout(500);
    await shot(page, "05-creator-review");
    await page.locator("#createFolderBtn").click();

    // The creator closes and the quest re-reads and reopens on the same page.
    await page.locator("#addFolderModal.show").waitFor({ state: "hidden", timeout: 30000 });
    await waitForJourney(page);
    console.log(`page after create: ${page.url()}`);
    summarize("status after create", await status(page));
    await shot(page, "06-quest-after-create");
  } else if (stage === "resume") {
    await setup.goto(`${baseUrl}${questURL}`, { waitUntil: "domcontentloaded" });
    await waitForJourney(setup);
    summarize("status after restart", await status(setup));
    const roster = await setup.evaluate(async () => {
      const quest = await (await fetch("/api/host-setup-quests/email_ops_setup/status")).json();
      const id = quest?.setup_journey?.receipts?.project_workspace_id;
      if (!id) return null;
      const folder = await (await fetch(`/api/workspaces/${encodeURIComponent(id)}`)).json();
      return (folder?.agent_instances || []).map(agent => agent.name);
    });
    console.log(`roster after restart: ${JSON.stringify(roster)}`);
    await shot(setup, "07-quest-after-restart");
  } else if (stage === "narrow") {
    const page = await newPage(400, 860);
    await page.goto(`${baseUrl}${questURL}`, { waitUntil: "domcontentloaded" });
    await waitForJourney(page);
    await shot(page, "08-quest-400px");
  }
} catch (error) {
  exitCode = 1;
  console.error(`demo failed: ${error.message}`);
} finally {
  await browser.close();
}

const unique = [...new Set(problems)];
if (unique.length) {
  console.log(`\n${unique.length} problem(s):`);
  for (const problem of unique.slice(0, 20)) console.log(`  ${problem}`);
}
process.exit(exitCode);
