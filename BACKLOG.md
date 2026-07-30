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
- 2026-07-18 #dogfood Weekly discovery playbook as scheduled Workspace Run; audit web-research MCP availability first (stage 2)
- 2026-07-18 #dogfood Discovery shortlist as Action Center items; forces pending periodic delivery wiring (stage 3)
- 2026-07-18 #dogfood PRD-drafting workflow/orchestration template writing tasks/prd-<feature>.md (stage 4)
- 2026-07-19 Generalize specialized-workspace starter templates into capability "installs" — standardize the hub-and-spoke conventions now piloted by Email Ops, Calendar Ops, and Downloads Janitor
- 2026-07-19 Template: Meeting Notes digester — file-watch a transcripts folder → summaries as notes, action items as tasks #small
- 2026-07-19 Template: Reading List — webhook trigger from bookmarklet/share-sheet → weekly summarized digest #small
- 2026-07-19 Template: Client Ops (CRM-lite) — per-client notes, follow-up cadence, invoice reminders for freelancers
- 2026-07-21 multiple-hq persisted in directory investigate why they are not imported

## Doing
- 2026-07-19 Construct wizard tiering: 2–3 recommended blueprints up front + "More" expander; defer addons/roster to post-create #small (started 2026-07-28)
- 2026-07-18 #dogfood `backlog` skill in Ori: add/list/prune/promote on workspace BACKLOG.md (DOGFOODING.md stage 1) #small (started 2026-07-29)
- 2026-07-19 Template: Repo Watcher — GitHub MCP; PR/issue digest + weekly changelog mission #dogfood #small (started 2026-07-30)
- create-workspace-team-step -> PRD at tasks/prd-create-workspace-team-step.md (started 2026-07-30)

## Shipped / dropped
- 2026-07-30 blueprint-setup-wizards -> PRD at tasks/prd-blueprint-setup-wizards.md (started 2026-07-28) - PR #278 merged to dev (2026-07-30)
- 2026-07-30 herdr-overnight-agent-completion -> PRD at tasks/prd-herdr-overnight-agent-completion.md (started 2026-07-29) - PR #277 merged to dev (2026-07-30)
- 2026-07-29 dropped: disposable "First Mission" tutorial workspace — superseded by the Home-first Mission 01 pull invite (PRs #214 and #215) and specialized workspaces that teach through real use
- 2026-07-28 google-account easy connection setup — unified Google Account Connection Hub (PR #265), ship-readiness and migration (PR #266), then Email Ops stabilization (PR #272)
- 2026-07-28 google-account-email-ops-stabilization -> PRD at tasks/prd-google-account-email-ops-stabilization.md (started 2026-07-27) - PR #272 merged to dev (2026-07-28)
- 2026-07-28 wt-guided-tab-start -> PRD at tasks/prd-wt-guided-tab-start.md (started 2026-07-27) - PR #273 merged to dev (2026-07-28); feature worktrees now open as tabs inside the focused Herdr workspace
- 2026-07-27 vault-modal-loading - PR #271 merged to dev (2026-07-27)
- 2026-07-27 path-based-agent-binding -> PRD at tasks/prd-path-based-agent-binding.md (started 2026-07-26) - PR #270 merged to dev (2026-07-27)
- 2026-07-27 downloads-janitor -> PRD at tasks/prd-downloads-janitor.md (started 2026-07-24) - PR #268 merged to dev (2026-07-27)
- 2026-07-26 wt-herd-feature-overview -> PRD at tasks/prd-wt-herd-feature-overview.md (started 2026-07-25) - PR #267 merged to dev (2026-07-26)
- 2026-07-26 workspace-backlog -> PRD at tasks/prd-workspace-backlog.md (started 2026-07-22) - PR #254 merged to dev (2026-07-23)
- 2026-07-26 calendar-ops-mcp -> PRD at tasks/prd-calendar-ops-mcp.md (started 2026-07-20) - PR #248 merged to dev (2026-07-22)
- 2026-07-26 email-ops-workspace -> PRD at tasks/prd-email-ops-workspace.md - Mail spin-off shipped in PR #244, with one-click workspace connection in PR #245 (2026-07-21)
- 2026-07-24 herdr-start-kind - PR #260 merged to dev (2026-07-24)
- 2026-07-24 herdr-devflow-cleanup-guard - PR #259 merged to dev (2026-07-24)
- 2026-07-24 herdr-devflow-bridge -> PRD at tasks/prd-herdr-devflow-bridge.md (started 2026-07-23) - PR #258 merged to dev (2026-07-24)
