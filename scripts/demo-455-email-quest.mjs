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
 *   card     The quiet Home resume card on a fresh sandbox: hire + HQ through
 *            the assistant API (the sandbox owns it), the capability card's
 *            "Set up email" route, no card before the quest starts, the card
 *            after closing mid-quest (desktop and 400px), Not now, reopening
 *            from Templates, Resume, and (MOCKED status only) a completed
 *            quest that never shows the card again.
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
    `${label}: exists=${body?.exists} lifecycle=${journey?.lifecycle_state || "-"} current=${journey?.current_step_id || "-"} dismissed=${journey?.dismissed ?? "-"} ` +
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

async function api(page, method, url, body) {
  return page.evaluate(
    async ({ method, url, body }) => {
      const response = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        ...(body ? { body: JSON.stringify(body) } : {})
      });
      let json = null;
      try {
        json = await response.json();
      } catch (_) {}
      return { status: response.status, json };
    },
    { method, url, body }
  );
}

// openQuestsFlyout opens Home's Quests flyout, where the quest log and the
// resume card live, and reports whether the card is showing.
async function openQuestsFlyout(page) {
  await page.goto(`${baseUrl}/`, { waitUntil: "domcontentloaded" });
  const toggle = page.locator("#cockpitQuestsToggle");
  await toggle.waitFor({ state: "visible", timeout: 20000 });
  if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
  await page.locator("#cockpitQuestsFlyout").waitFor({ state: "visible", timeout: 10000 });
  // The card's own status read settles after the page's first paint.
  await page.waitForTimeout(1200);
  const card = page.locator("#emailSetupQuestCard");
  const visible = await card.isVisible();
  if (visible) await card.scrollIntoViewIfNeeded();
  const text = visible ? (await card.innerText()).replace(/\s+/g, " ").trim() : "";
  console.log(`quests flyout: card visible=${visible}${text ? ` text="${text}"` : ""}`);
  return visible;
}

async function closeJourney(page) {
  await page.locator("#specialistSetupJourneyClose").click();
  await page.locator("#specialistSetupJourneyModal").waitFor({ state: "hidden", timeout: 10000 });
}

async function cardStage(page) {
  // A hired assistant with an HQ makes the capability cards real.
  const assistant = async () => (await api(page, "GET", "/api/personal-assistant")).json?.personal_assistant || {};
  let current = await assistant();
  if (current.state === "needs_hire") {
    const hire = await api(page, "POST", "/api/personal-assistant/hire", {
      request_id: `demo-hire-${Date.now()}`,
      if_version: current.state_version ?? 0,
      display_name: "Atlas",
      mandate: "Keep my week organised.",
      focus_areas: []
    });
    console.log(`hire: HTTP ${hire.status}`);
    current = await assistant();
  }
  if (current.state === "needs_hq") {
    const hq = await api(page, "POST", "/api/personal-assistant/hq", {
      request_id: `demo-hq-${Date.now()}`,
      if_version: current.state_version ?? 0,
      name: "Demo HQ"
    });
    console.log(`hq: HTTP ${hq.status} ${hq.json?.code || ""}`);
    current = await assistant();
  }
  console.log(`assistant state: ${current.state}`);
  const capabilities = (await api(page, "GET", "/api/personal-assistant/capabilities")).json?.capabilities;
  const email = (capabilities?.cards || []).find(card => card.key === "email");
  console.log(
    `capabilities: state=${capabilities?.state} email card=${JSON.stringify(
      email ? { status: email.status, label: email.action_label, route: email.action_route } : null
    )}`
  );

  summarize("status before any quest", await status(page));
  const never = await openQuestsFlyout(page);
  await shot(page, "20-quests-flyout-never-started");
  if (never) throw new Error("card showed before the quest was started");

  // Start from the assistant's capability card, the goal-first entry point.
  await page.goto(`${baseUrl}/?personal-assistant=working-agreement`, { waitUntil: "domcontentloaded" });
  const setUpEmail = page.locator("#personalAssistantCapabilities a", { hasText: "Set up email" });
  await setUpEmail.waitFor({ state: "visible", timeout: 20000 });
  await setUpEmail.scrollIntoViewIfNeeded();
  console.log(`capability link href=${await setUpEmail.getAttribute("href")}`);
  await shot(page, "19-capability-set-up-email");
  await setUpEmail.click();
  await waitForJourney(page);
  await shot(page, "19b-quest-opened-from-capability");
  await closeJourney(page);
  summarize("status after closing on step 1", await status(page));

  if (!(await openQuestsFlyout(page))) throw new Error("card missing after closing mid-quest");
  await shot(page, "21-card-after-close-desktop");
  await page.setViewportSize({ width: 400, height: 860 });
  await page.waitForTimeout(400);
  await page.locator("#emailSetupQuestCard").evaluate(card => card.scrollIntoView({ block: "center" }));
  await page.waitForTimeout(300);
  // Report whether anything floats over the card's own controls at this width.
  const covered = await page.evaluate(() =>
    ["email-setup-resume", "email-setup-dismiss"].map(role => {
      const control = document.querySelector(`#emailSetupQuestCard [data-role="${role}"]`);
      const box = control.getBoundingClientRect();
      const top = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
      return `${role}: y=${Math.round(box.top)} ${control.contains(top) ? "reachable" : `covered by ${top?.id || top?.className || top?.tagName}`}`;
    })
  );
  console.log(`400px controls: ${covered.join("; ")}`);
  await shot(page, "22-card-after-close-400px");
  await page.setViewportSize({ width: 1440, height: 900 });

  await page.locator('#emailSetupQuestCard [data-role="email-setup-dismiss"]').click();
  await page.waitForTimeout(800);
  console.log(`after Not now: card visible=${await page.locator("#emailSetupQuestCard").isVisible()}`);
  summarize("status after Not now", await status(page));
  await shot(page, "23-card-not-now");
  if (await openQuestsFlyout(page)) throw new Error("card came back after Not now");

  await page.goto(`${baseUrl}/templates`, { waitUntil: "domcontentloaded" });
  await page.locator('#tplList button[role="listitem"]', { hasText: "Email Ops" }).first().click();
  await page.locator("#tplQuestOpen").waitFor({ state: "visible", timeout: 15000 });
  await page.locator("#tplQuestOpen").click();
  await waitForJourney(page);
  summarize("status after reopening from Templates", await status(page));
  await closeJourney(page);

  if (!(await openQuestsFlyout(page))) throw new Error("card missing after reopening and closing");
  await shot(page, "24-card-returns-after-reopen");
  await page.locator('#emailSetupQuestCard [data-role="email-setup-resume"]').click();
  await waitForJourney(page);
  console.log(`Resume opened: ${await page.locator("#specialistSetupJourneyTitle, #specialistSetupJourneyModal .modal-title").first().innerText()}`);
  await shot(page, "25-resume-opens-quest");
  await closeJourney(page);

  // MOCKED status only: a completed quest (even one that later regressed)
  // never brings the card back.
  for (const [name, overrides] of [
    ["ready", { lifecycle_state: "ready", current_step_id: "", first_completed_at: "2026-09-15T10:05:00Z" }],
    ["regressed", { lifecycle_state: "needs_attention", current_step_id: "mailbox", first_completed_at: "2026-09-15T10:05:00Z" }]
  ]) {
    await page.route("**/api/host-setup-quests/email_ops_setup/status", route =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ exists: true, setup_journey: { ...fixtureJourney("ready"), ...overrides } })
      })
    );
    const shown = await openQuestsFlyout(page);
    console.log(`completed (${name}, mocked status): card visible=${shown}`);
    if (name === "ready") await shot(page, "26-completed-no-card-mocked");
    await page.unroute("**/api/host-setup-quests/email_ops_setup/status");
    if (shown) throw new Error(`card showed for a completed quest (${name})`);
  }
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
  } else if (stage === "card") {
    await cardStage(setup);
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
