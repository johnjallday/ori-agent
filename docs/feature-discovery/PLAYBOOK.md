# Feature Discovery Playbook

Weekly routine that produces a ranked shortlist of 3–5 feature candidates for Ori Agent.
Output feeds the existing PRD → task generation → build workflow. This doc is the single
source of truth for the routine — tune the routine by editing this file, not the schedule.

**Running it locally.** `.agents/skills/feature-explore/SKILL.md` runs this routine on a
checkout with git and the `gh` CLI (Claude: the `feature-explore` agent or `/feature-explore`;
Codex: `$feature-explore`). `./scripts/feature-explore-snapshot.sh` gathers the previous
report, worktrees, PRs, open Issues and recent commits in one read-only command. The cloud
routine follows this document directly.

**Rules for the executing agent:**
- Read-only pass over everything. The report file is the only thing a run writes. In
  particular source E — the user's open GitHub Issues — is read and never edited, labelled,
  assigned, commented on, closed, reopened, or added to a Project.
- Read the most recent report in `docs/feature-discovery/reports/` first. Carry forward
  still-valid candidates, note status changes, don't re-pitch things already rejected or shipped.
- Timebox web research to ~10 searches. Evidence over speculation.
- Every candidate must cite evidence (file paths, doc names, or URLs).

## Signal sources

### A. Codebase signals (structural gaps, not comments)
This repo has ~zero TODO/FIXME comments (verified 2026-07) — do not grep for them. Instead:

- Stubs and placeholders: `grep -rn "not implemented\|panic(\|placeholder\|coming soon" --include="*.go" internal/ cmd/`
- Handlers registered but thin: compare `internal/server/server.go` route registrations against handler implementations in `internal/*http/`.
- UI surfaces without backend (or vice versa): scan `internal/web/` templates/JS for buttons or tabs that call missing endpoints.
- Docs promising features: `docs/plugins.md`, `docs/api/API_REFERENCE.md` vs. actual code.
  (`docs/premium_features.md` is a pricing proposal that still describes the removed plugin
  system; it is not a feature plan.)

### B. Momentum (git when available, mtime fallback otherwise)
Locally, `git log origin/dev --since=<last report date>` works, and the snapshot script prints
the commits, the most-changed paths and the longest-untouched packages. In the cloud sandbox
this folder is a git worktree whose `.git` points outside the mount, so `git log` normally
fails there. Try it once; if it fails, use file mtimes:

- Hot areas (last 2 weeks): `find internal cmd -name "*.go" -mtime -14`
- Stale areas (untouched 90+ days): `find internal -name "*.go" -mtime +90 | sed 's|/[^/]*$||' | sort | uniq -c | sort -rn | head`

Hot areas suggest natural follow-ups; stale areas that map to advertised features suggest neglect worth flagging.

### C. Ecosystem research (web)
Search for developments since the last report in:

- Competing agent frameworks: LangGraph, CrewAI, AutoGen, OpenAI Agents SDK, Claude Agent SDK, smolagents.
- MCP ecosystem: spec changes, registry growth, popular new servers, MCP client feature parity.
- Skills/plugin ecosystems in comparable tools (Claude Code, Cursor, Windsurf).

Ask: what are competitors shipping that Ori Agent's architecture (MCP + skills, multi-agent
workspaces, Go single-binary) is well positioned to match or beat?

### D. Vision docs vs. reality
Read `docs/features/*.md`, `docs/architecture/*.md` (the contracts) and `docs/design/*.md`. The
13 older plan docs were removed in `bb5285af8`, so list the folders rather than trusting a
remembered name. For each plan doc or contract: classify as shipped / partially shipped / not
started by spot-checking the code it describes.
Partially-shipped plans with recent momentum are prime candidates.

### E. User backlog (all open GitHub Issues)

John's own captured ideas — the highest-signal source. Read them fresh at the start of every
run:

```bash
gh issue list --state open --limit 1000 --json number,title,author,labels,url,createdAt,updatedAt
gh issue view <number> --json number,title,body,author,labels,url,createdAt,updatedAt,comments
```

**Read the unfiltered Issue list.** `./scripts/devops.sh` offers convenient
`needs-decision`, `backlog`, and `feature-proposal` label views for a human, but
discovery scores *every* open Issue, including unlabelled ones. Starting from a
filtered view would silently omit the newest or not-yet-triaged ideas.

Treat **every** open Issue as a candidate and score it with the same rubric. An Issue counts
even when it has no label, no milestone, no assignee, no Project, and no acceptance criteria —
a ten-second capture is exactly what this source is for, and requiring ceremony would hide
the ideas most worth finding. Use the title, body, labels, creation time, update time, and URL
as evidence; nothing else about an Issue is available or needed.

**This source is read-only. Full stop.** Discovery does not edit a title or body, add or remove
a label, add an Issue to a Project, assign it, comment on it, close or reopen it, or open a
replacement Issue. The bookkeeping this section used to prescribe no longer exists: GitHub owns
the lifecycle, and a discovery run is an opinion, not a decision. If a run concludes that an
Issue is obsolete, it says so in the report and leaves the Issue alone for John to close.

**Skip Issues that are already selected or delivered.** Work is selected when an artifact
carries the Issue's number in its identity — `tasks/prd-<number>-<slug>.md`,
`tasks/tasks-<number>-<slug>.md`, a worktree named `<number>-<slug>`, a branch whose suffix is
`<number>-<slug>`, or a pull request for that branch. Match on the **exact number** and never
on similar titles: two Issues can be worded almost identically, and a title can be rewritten
after planning starts while the number never changes. An Issue with no matching artifact is a
fresh candidate; an Issue with one is already in flight or shipped.

**Every candidate derived from an Issue keeps its number and URL** in the source log and in the
shortlist entry, so a later reader can go straight to the original rather than to a paraphrase.

**If GitHub is unavailable**, say so: record source E as unavailable, mark the run incomplete,
and do not present the shortlist as complete. There is no local copy of this backlog to fall
back on — the file that used to hold one was deleted deliberately — and a shortlist that
quietly omits the user's own ideas is worse than one that admits it could not read them.

## Scoring rubric

Score each candidate H/M/L on:

| Axis | Question |
|------|----------|
| Impact | Does this materially improve a real user workflow? |
| Effort | S (≤1 week) / M (1–3 weeks) / L (3+ weeks) given existing code |
| Strategic fit | Consistent with MCP + skills philosophy (no plugin-era regressions)? |
| Momentum fit | Builds on recently touched code or an in-flight plan doc? |
| Differentiation | Does the ecosystem scan show demand or a gap competitors miss? |

Rank by Impact and Strategic fit first; prefer S/M effort. At most one L-effort candidate per report.

## Report format

Write to `docs/feature-discovery/reports/YYYY-MM-DD.md` (a local run scoped to one Kind or
topic appends `-<kind>` or `-<topic-slug>` to the name):

```markdown
# Feature Discovery — YYYY-MM-DD

## Shortlist
### 1. <Candidate name>  [Impact: H | Effort: M | Fit: H]
- Kind: Feature | Finish | Quick win | Reliability
- Issue: #<number> — <url>   (omit only for candidates that came from sources A–D)
- Ready: yes | no — the label state, plus the `size:*` label if any (Issue-derived entries only)
- What: one paragraph
- Why now: evidence from sources A–E with file paths / URLs
- Open questions: 2–3 questions the PRD must answer (for a Finish candidate, the ones the
  person must settle to close it out)
- First step: the one concrete action for the person; a suggestion the run prints, never performs

(3–5 candidates total; a full run mixes Kinds and never pads a Kind to reach a count)

## Watchlist
Candidates considered but not ranked, one line each, with reason. Keep `#<number>` on any
line that came from an Issue.

## Changes since last report
Shipped / promoted / dropped candidates from the previous report.

## In flight (not candidates)
Work seen and deliberately not pitched: worktrees ahead of dev, open PRs, half-ticked checklists.
A run without local worktrees lists open PRs only and says so.

## Housekeeping (not candidates)
Local runs only: owed cleanup verified against `git worktree list`, such as `wt done` still
owed, stranded branches, stale docs. Omit the section when worktrees are not visible.

## Source log
Which searches ran, which docs were read, git/mtime status, and for source E: the repository,
the number of open Issues read, each Issue number and URL considered, and whether the source
was complete. If GitHub was unavailable, say so here and mark the run incomplete.
```

An Issue number and URL are required on every Issue-derived entry. They are what lets a later
run — or a later person — join this report to the original idea by exact identity rather than
by a title that may since have been rewritten.

End the run by summarizing the shortlist in chat with a pointer to the report file.
