# Dogfooding Gameplan — run Ori's feature loop inside Ori

Migrate the capture → discover → decide → build loop from Cowork into Ori Agent, one stage
at a time, harvesting product gaps as backlog entries while doing it.

**Prime directive:** every friction point hit while dogfooding becomes a GitHub Issue tagged
`#dogfood` in its title or body. The loop's job is to feed itself.

```bash
./scripts/devops.sh new "#dogfood <the friction, in one line>" --body "<what you were doing>"
./scripts/devops.sh                 # picker: inspect, decide, capture, or approve
./scripts/devops.sh view <number>   # the full idea before deciding anything
```

Capture it while you are annoyed, not later. One line is enough; the body is optional and the
Issue needs no label, milestone, assignee, or Project to count.

**Selecting the day's work stays manual.** `./scripts/devops.sh` captures and reads Issues,
filters them by workflow label, records marked answers to open questions, and owns the explicit
`approved` gate. The grooming routine still owns triage, sizing, and bundling; recording a
decision does not silently perform those transitions.

**Safety rule:** Cowork stays the fallback at every stage. Dogfooding must never block real
development. A stage's Ori version becomes the default only when it meets its graduation
criterion; start stage N+1 only after N graduates.

## Stage 0 — Product HQ workspace (foundation)

Create an "Ori DevOps" workspace pointed at this repo folder. `docs/feature-discovery/` is
then already inside it via folder sync, and the backlog itself is read from GitHub. Personal HQ
(shipped 2026-07-16, `internal/personalhq/`) proves the HQ-workspace pattern; this is its
product-development sibling.

- Exercises: workspace creation UX, folder sync, MCP bindings.
- Graduates when: the workspace agent can quote the backlog and playbook on demand.
- **Decision (2026-07-18): separate workspace, not inside Personal HQ.** Different mission,
  folder root, tools, cadence, and blast radius (unattended research run ≠ send-capable
  mailbox workspace). HQ is where results *land* (Action Center in stage 3, Daily Brief
  later), not where the loop runs. Bonus: a second workspace is the first real exercise of
  the multi-workspace story. If workspace setup proves heavy, run v0 inside HQ and file the
  friction as `#dogfood`.

## Stage 1 — Backlog skill (smallest surface first)

Author a `backlog` skill in Ori's skills system (`internal/skills/`) that captures and reads
GitHub Issues — the same list/filter/view workflow `./scripts/devops.sh` provides, reachable
from a chat. Bind it to the HQ workspace.

The skill stays read-and-capture, like the command it mirrors: no promoting, closing, or
Project writes, because GitHub owns the lifecycle and no local copy of it exists to maintain.

- Exercises: skill authoring ergonomics, workspace skill bindings, GitHub access from a skill.
- Graduates when: "add X to the backlog" works correctly in an Ori chat 5 times out of 5, and
  the Issue it creates is the one you meant.

## Stage 2 — Discovery run as a scheduled Workspace Run

Port `PLAYBOOK.md` into a weekly scheduled task executed as a Workspace Run
(`docs/features/workspace-runs-harness-model.md` — scoped, observable, validated).
Report lands in workspace files; run metadata could use an output contract
(`docs/features/task-output-contracts.md`).

- Exercises: scheduler, long autonomous runs, web-research MCP availability (predicted gap),
  cost tracking on unattended runs (`usagehttp`).
- Graduates when: one unattended weekly run produces a report matching Cowork quality.

## Stage 3 — Shortlist → Action Center

The weekly run's shortlist surfaces as Action Center items (`actioncenterhttp` — its literal
mission is cross-workspace triage of workspace findings), each carrying the Issue number and
URL it came from. What "picking" an item should do to the Issue is exactly the open question
this dogfooding is meant to answer, so for now picking is a human action taken in GitHub.
Note: Action Center's periodic delivery wiring is already a known pending gap (see
personal-hq-assistant.md deferred list) — this stage forces it.

- Exercises: Action Center end-to-end, event bus, scheduled delivery wiring.
- Graduates when: you triage Monday's shortlist from Action Center, not from the report file.

## Stage 4 — PRD handoff

Picked candidate → orchestration template or custom workflow (`workflowhttp`) drafts the PRD
into `tasks/prd-<issue-number>-<slug>.md` (see the naming convention in `scripts/README.md`)
plus a task breakdown. Carrying the Issue number into the filename is what lets a later run
tell a planned Issue from a fresh one without comparing titles. Optionally two-agent: drafter
+ critic via agent delegation.

- Exercises: orchestration templates, multi-agent delegation, task generation.
- Graduates when: one real feature's PRD is produced in Ori and used for the actual build.

**Out of scope for now:** the build itself stays in Claude Code/Cowork. Revisit at Stage 4
whether `cliagenthttp` can hand builds off to a CLI agent.

## Cadence & scorecard

One stage per week alongside normal work. The weekly discovery run doubles as the test
harness — each run stress-tests whatever stage is newest. Track per stage: `#dogfood` Issues
filed, features shipped because of them, manual steps remaining.

## Predicted first findings (verify early)

Web-search MCP for research runs; markdown report rendering in the workspace UI;
notifications when unattended runs finish; cost caps for scheduled runs; skill authoring
friction. If a prediction is wrong, that's a finding too.
