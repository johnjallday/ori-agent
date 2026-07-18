# Feature Discovery Playbook

Weekly routine that produces a ranked shortlist of 3–5 feature candidates for Ori Agent.
Output feeds the existing PRD → task generation → build workflow. This doc is the single
source of truth for the routine — tune the routine by editing this file, not the schedule.

**Rules for the executing agent:**
- Read-only pass over the codebase. The only writes allowed are the report file and
  backlog bookkeeping (source E).
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

### E. User backlog (`BACKLOG.md` at repo root)
John's own captured ideas — the highest-signal source. Treat every entry under `## Ideas`
as a candidate and score it with the same rubric: `!` means he explicitly wants it (weight
Impact up), `#small`/`#large` are effort hints. Bookkeeping duties (the only writes allowed
outside the report): move entries that shipped or became pointless to `## Shipped / dropped`
with a one-word reason; promote a candidate to `## Doing` only when John picked it in a
prior session. Never delete or reword entries under `## Ideas` — his phrasing is the record.

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
- What: one paragraph
- Why now: evidence from sources A–E with file paths / URLs (tag backlog-originated candidates)
- First PRD questions: 2–3 questions the PRD must answer

(3–5 candidates total)

## Watchlist
Candidates considered but not ranked, one line each, with reason.

## Changes since last report
Shipped / promoted / dropped candidates from the previous report.

## Source log
Which searches ran, which docs were read, git/mtime status.
```

End the run by summarizing the shortlist in chat with a pointer to the report file.
