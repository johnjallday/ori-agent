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
 *   connect  Step 2 against the server's real connection state: the Settings
 *            action opens a new tab and Check again re-reads.
 *   seed     Write a connected-Google fixture into the sandbox data dir given as
 *            the 4th argument (identity + healthy Gmail grant, no vault).
 *   link     MOCKED: replays fixture journey responses to show the link review
 *            card and the ready summary. The real commit is covered by the Go
 *            integration tests; nothing here reaches Google or a vault.
 *
 * Every stage prints the quest status it observed and any console errors or
 * failed requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from "playwright";
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const [baseUrl, outDir, stage = "team", dataDir = ""] = process.argv.slice(2);
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

// Fixture projections in the server's exact response shape. They exist only to
// show the step 3 and summary UI without a live Google account.
function fixtureJourney(stage) {
  const ready = stage === "ready";
  const step = (id, kind, title, status, extra = {}) => ({ id, kind, title, description: "", status, ...extra });
  return {
    run_id: "demo-run", run_kind: "root", state_revision: ready ? 6 : 4,
    journey: { source: "host", template_id: "email-ops", id: "email_ops_setup", schema_version: 1, version: 1, title: "Set up Email Ops", description: "Create your inbox command post, connect Gmail, and link the mailbox. Ori reads and drafts; it never sends without you." },
    lifecycle_state: ready ? "ready" : "in_progress", current_step_id: ready ? "" : "mailbox", dismissed: false,
    receipts: { project_workspace_id: "demo-workspace" },
    first_opened_at: "2026-09-15T10:00:00Z", ...(ready ? { first_completed_at: "2026-09-15T10:05:00Z" } : {}),
    updated_at: "2026-09-15T10:05:00Z",
    steps: [
      step("team", "workspace_create", "Review your Email Ops team", "complete", { workspace_create: { template_title: "Email Ops", workspace_id: "demo-workspace", workspace_label: "Email Ops", workspace_route: "/workspaces/email-ops" }, actions: [{ id: "open_workspace", label: "Open workspace", effect: "navigation" }] }),
      step("connect", "account_connect", "Connect Gmail", "complete", { account_connect: { configured: true, identity_email: "demo.user@example.com", gmail_health: "healthy" } }),
      step("mailbox", "account_link", "Link the mailbox", ready ? "complete" : "active", {
        description: "Link the connected account to this workspace. Email Ops will read and search this mailbox and prepare drafts. It never sends a message without your confirmation of that specific message.",
        account_link: { workspace_label: "Email Ops", account_email: "demo.user@example.com", linked: ready, ready },
        ...(ready ? {} : { actions: [{ id: "review_mailbox_link", label: "Review mailbox link", effect: "review" }] })
      }),
      step("summary", "summary", "Email Ops is ready", ready ? "complete" : "pending", {
        description: "Your mailbox is linked and Ori can read and search it. Triaging the inbox is a separate task you start when you are ready.",
        ...(ready ? { actions: [{ id: "open_workspace", label: "Open Email Ops", effect: "navigation" }, { id: "open_model_settings", label: "Set up a model", effect: "navigation" }] } : {})
      })
    ]
  };
}

async function mockLinkFlow(page) {
  const root = "**/api/host-setup-quests/email_ops_setup";
  let linked = false;
  const json = body => ({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  await page.route(root, route => route.fulfill(json({ setup_journey: fixtureJourney(linked ? "ready" : "link") })));
  await page.route(`${root}/open`, route => route.fulfill(json({ setup_journey: fixtureJourney(linked ? "ready" : "link") })));
  await page.route(`${root}/runs/demo-run`, route => route.fulfill(json({ setup_journey: fixtureJourney(linked ? "ready" : "link") })));
  await page.route(`${root}/runs/demo-run/actions/review_mailbox_link`, route =>
    route.fulfill(json({
      setup_journey: fixtureJourney("link"),
      review: { token: "demo-review-token", commit_action: "link_mailbox", expires_at: "2026-09-15T10:20:00Z", account_link: { workspace_label: "Email Ops", account_email: "demo.user@example.com", linked: false, ready: false } }
    }))
  );
  await page.route(`${root}/runs/demo-run/actions/link_mailbox`, route => {
    const body = route.request().postDataJSON();
    console.log(`MOCK commit received review_token=${body?.review_token} input=${JSON.stringify(body?.input)}`);
    linked = true;
    return route.fulfill(json({ setup_journey: fixtureJourney("ready") }));
  });

  await page.goto(`${baseUrl}${questURL}`, { waitUntil: "domcontentloaded" });
  await waitForJourney(page);
  await shot(page, "10-link-step-mocked");
  await page.locator('#specialistSetupJourneyActions [data-action="review_mailbox_link"]').click();
  await page.locator("#specialistSetupJourneyReview:not([hidden])").waitFor({ state: "visible", timeout: 10000 });
  console.log(`review card: ${JSON.stringify((await page.locator("#specialistSetupJourneyReview").innerText()).replace(/\s+/g, " "))}`);
  await shot(page, "11-link-review-mocked");
  await page.setViewportSize({ width: 400, height: 860 });
  await page.waitForTimeout(300);
  await shot(page, "12-link-review-400px-mocked");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.locator("#specialistSetupJourneyReview .setup-journey__review-confirm").click();
  await page.waitForTimeout(800);
  console.log(`summary receipt: ${JSON.stringify((await page.locator("#specialistSetupJourneyReceipt").innerText()).replace(/\s+/g, " "))}`);
  console.log(`summary actions: ${JSON.stringify(await page.locator("#specialistSetupJourneyActions button").allTextContents())}`);
  await shot(page, "13-summary-ready-mocked");
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
  } else if (stage === "connect") {
    await setup.goto(`${baseUrl}${questURL}`, { waitUntil: "domcontentloaded" });
    await waitForJourney(setup);
    summarize("step 2 state", await status(setup));
    const actions = await setup.locator("#specialistSetupJourneyActions button").allTextContents();
    console.log(`step 2 actions: ${JSON.stringify(actions)}`);
    console.log(`step 2 receipt: ${JSON.stringify((await setup.locator("#specialistSetupJourneyReceipt").textContent())?.trim())}`);
    await shot(setup, `09-connect-${dataDir || "state"}`);
    const [settings] = await Promise.all([
      setup.context().waitForEvent("page"),
      setup.locator('#specialistSetupJourneyActions [data-action="open_account_settings"]').click()
    ]);
    await settings.waitForLoadState("domcontentloaded");
    console.log(`settings tab: ${settings.url()} (quest still open: ${await setup.locator("#specialistSetupJourneyModal.show").isVisible()})`);
    console.log(`live note: ${await setup.locator("#specialistSetupJourneyLiveStatus").textContent()}`);
    await settings.close();
    const writesBefore = problems.length;
    await setup.locator('#specialistSetupJourneyActions [data-action="recheck_connection"]').click();
    await setup.waitForTimeout(800);
    summarize("after Check again", await status(setup));
    if (problems.length !== writesBefore) console.log("Check again produced request problems");
  } else if (stage === "seed") {
    if (!dataDir) throw new Error("seed needs the sandbox data dir");
    const file = join(resolve(dataDir), "connections", "google.json");
    mkdirSync(join(resolve(dataDir), "connections"), { recursive: true });
    writeFileSync(
      file,
      JSON.stringify({
        id: "demo-connection", provider: "google", subject: "demo-subject", email: "demo.user@example.com",
        vault_id: "demo-missing-vault",
        grants: { gmail: { connection_id: "demo-connection", product: "gmail", transport: "native", credential_ref: "demo-missing-credential", health: "healthy" } }
      }),
      { mode: 0o600 }
    );
    console.log(`seeded ${file}`);
  } else if (stage === "link") {
    await mockLinkFlow(setup);
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
