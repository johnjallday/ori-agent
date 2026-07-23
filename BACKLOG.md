# Backlog

Quick capture, one line per idea — no commitment implied. The feature-discovery run
(`docs/feature-discovery/PLAYBOOK.md`) reads this, ranks Ideas entries against its other
signals, and does the bookkeeping here (promotes picks to Doing, files shipped/dropped).

Conventions, all optional: date prefix (`2026-07-18 …`), `!` = I really want this,
`#small` / `#large` = effort hint.

Kept in sync with git by `scripts/wt.sh`: `wt backlog add "<idea>"` appends here and pushes;
`wt backlog sync` pushes pending edits; `wt start` promotes a picked PRD to `## Doing`; and
`wt done` retires it to `## Shipped / dropped` with the merged PR number. Each is a scoped
`docs(backlog):` commit on dev, so this file never drifts from origin.

## Ideas
- 2026-07-18 #dogfood `backlog` skill in Ori: add/list/prune/promote on workspace BACKLOG.md (DOGFOODING.md stage 1) #small
- 2026-07-18 #dogfood Weekly discovery playbook as scheduled Workspace Run; audit web-research MCP availability first (stage 2)
- 2026-07-18 #dogfood Discovery shortlist as Action Center items; forces pending periodic delivery wiring (stage 3)
- 2026-07-18 #dogfood PRD-drafting workflow/orchestration template writing tasks/prd-<feature>.md (stage 4)
- 2026-07-19 Spin off email station → dedicated Mail workspace; HQ station becomes portal (mailbox runtime already workspace-scoped, OAuth vault is global; cheapest before first OAuth) #large
- 2026-07-19 Specialized-workspace starter templates as capability "installs" — hub-and-spoke: HQ aggregates via snapshots, stations link out; Mail spin-off pilots the pattern
- 2026-07-19 Construct wizard tiering: 2–3 recommended blueprints up front + "More" expander; defer addons/roster to post-create #small
- 2026-07-19 "First Mission" tutorial workspace as quest-log pull invite (pre-lived-in, disposable) — likely superseded by specialized templates teaching through real use
- 2026-07-19 Template: Meeting Notes digester — file-watch a transcripts folder → summaries as notes, action items as tasks #small
- 2026-07-19 Template: Repo Watcher — GitHub MCP; PR/issue digest + weekly changelog mission #dogfood #small
- 2026-07-19 Template: Reading List — webhook trigger from bookmarklet/share-sheet → weekly summarized digest #small
- 2026-07-19 Template: Downloads Janitor — file-watch ~/Downloads, classify + file documents (local-first showcase) #small
- 2026-07-19 Template: Calendar Ops — daily agenda + meeting prep; blocked on net-new GCal integration #large
- 2026-07-19 Template: Client Ops (CRM-lite) — per-client notes, follow-up cadence, invoice reminders for freelancers
- 2026-07-20 commander allow codex models #small
- 2026-07-20 "workspace-details-unit-sheetallow edit models" #small
- 2026-07-21 multiple-hq persisted in directory investigate why they are not imported
- 2026-07-22 google-account easy connection setup
- 2026-07-22 wt.sh create new worktree below dev

## Doing
- Email Ops workspace spin-off → PRD at tasks/prd-email-ops-workspace.md (template Email Ops; personal-ops v5 drops Inbox; station → portal; follow-ups re-keyed) — task list next
- calendar-ops-mcp -> PRD at tasks/prd-calendar-ops-mcp.md (started 2026-07-20)
- workspace-backlog -> PRD at tasks/prd-workspace-backlog.md (started 2026-07-22)
- herdr-devflow-bridge -> PRD at tasks/prd-herdr-devflow-bridge.md (started 2026-07-23)

## Shipped / dropped
- 2026-07-21 unit-sheet-model-editing - PR #246 merged to dev (2026-07-21)
- 2026-07-19 HQ cross-workspace visibility (hq_overview + Watchtower) — PR #240 merged to dev; post-merge live demo verified (badge/panel/navigation/all-clear + Chief hq_overview call on local model)
