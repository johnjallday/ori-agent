# Feature Discovery Playbook

Weekly routine that produces a ranked shortlist of 3–5 feature candidates for Ori Agent.
Output feeds the existing PRD → task generation → build workflow. This doc is the single
source of truth for the routine — tune the routine by editing this file, not the schedule.

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
- Docs promising features: `docs/premium_features.md`, `docs/plugins.md`, `docs/api/API_REFERENCE.md` vs. actual code.

### B. Momentum (git usually unavailable — use mtime fallback)
This folder is a git worktree; `.git` points outside the sandbox mount, so `git log` normally
fails here. Try it once; if it fails, use file mtimes:

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
Read `docs/features/*.md` (13+ plan docs incl. `AI_FEATURES_ROADMAP.md`, `create-modal-backlog.md`,
`multi-agent-support.md`, `personal-hq-assistant.md`) and `docs/design/*.md`. For each plan doc:
classify as shipped / partially shipped / not started by spot-checking the code it describes.
Partially-shipped plans with recent momentum are prime candidates.

### E. User backlog (open GitHub Issues authored by the repository owner)

John's own captured ideas — the highest-signal source. Read them fresh at the start of every
run:

```bash
./scripts/issue.sh --json          # open Issues in this repository authored by the current gh user
./scripts/issue.sh view <number>   # one Issue's full body, when the title is not enough
```

**Read Issues, not the board.** `./scripts/backlog.sh` reads the project board's `Ready` column — only the Issues a grooming agent has already researched. Discovery scores *every* open Issue, including the ones nobody has looked at yet, which are exactly the ones most likely to be missed. Using the board here would narrow this source silently and produce a shortlist that looks complete while omitting the newest ideas.

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

Write to `docs/feature-discovery/reports/YYYY-MM-DD.md`:

```markdown
# Feature Discovery — YYYY-MM-DD

## Shortlist
### 1. <Candidate name>  [Impact: H | Effort: M | Fit: H]
- Issue: #<number> — <url>   (omit only for candidates that came from sources A–D)
- What: one paragraph
- Why now: evidence from sources A–E with file paths / URLs
- First PRD questions: 2–3 questions the PRD must answer

(3–5 candidates total)

## Watchlist
Candidates considered but not ranked, one line each, with reason. Keep `#<number>` on any
line that came from an Issue.

## Changes since last report
Shipped / promoted / dropped candidates from the previous report.

## Source log
Which searches ran, which docs were read, git/mtime status, and for source E: the repository,
the number of open Issues read, each Issue number and URL considered, and whether the source
was complete. If GitHub was unavailable, say so here and mark the run incomplete.
```

An Issue number and URL are required on every Issue-derived entry. They are what lets a later
run — or a later person — join this report to the original idea by exact identity rather than
by a title that may since have been rewritten.

End the run by summarizing the shortlist in chat with a pointer to the report file.
