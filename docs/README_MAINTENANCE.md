# README Maintenance

This guide is the source of truth for maintaining Ori's root `README.md` and its
product screenshots. It is designed for the workspace-scoped **README Steward**
agent, but every command is versioned in the repository so a maintainer can run
and inspect it directly.

## What is maintained

The README shows four dark, desktop product scenes from one fictional product
launch scenario:

| ID | Product surface | README asset | Display width |
| --- | --- | --- | --- |
| `hero` | Home command bridge and Personal HQ Daily Brief | `docs/images/hero.webp` | 820 px |
| `action-center` | Action Center with three findings | `docs/images/action-center.webp` | 820 px |
| `workspace-map` | Workspace Map with Personal HQ | `docs/images/workspace-map.webp` | 420 px |
| `workspace` | Workspace Command with active work | `docs/images/workspace.webp` | 420 px |

`docs/readme-screenshots.json` is the machine-readable contract for these
scenes. It defines their routes, fixture state, required visible elements,
rendering environment, captions, alt text, and accepted-output metadata.

The old Onboarding screenshot is not part of this portfolio. It remains covered
by product tests, but it is intentionally not recurring README imagery.

## Prerequisites

- A clean checkout of the intended source commit.
- Go and the repository's Node version.
- Dependencies installed with `npm ci` and Chromium available to Playwright.
- Permission to run a local Ori server in an isolated temporary directory.

The capture workflow does **not** require API keys, provider accounts, OAuth
sessions, external MCP servers, network access, or real Ori workspace data.
It starts the server with a temporary `HOME` and `ORI_DATA_DIR`, from inside
that sandbox, and uses only version-controlled fictional fixtures.

## Commands

| Command | Effect |
| --- | --- |
| `make readme-audit` | Read-only drift and contract report. It creates no worktree, screenshots, tracked changes, commits, or remote calls. |
| `make readme-capture` | Builds the current worktree, captures all four scenes into a new ignored staging run, optimizes proposed images, and writes a comparison report. It never writes `README.md` or `docs/images/`. |
| `make readme-check` | Validates the manifest, README image coverage, local links, image metadata, size limits, checksums, and forbidden staging references. |
| `make readme-accept RUN_ID=<run-id>` | Applies one previously reviewed staged run only after Checkpoint 1 approval. It is branch-guarded and validates the run again before changing tracked README files. |

The commands are introduced in stages with this feature. Until the first
accepted portfolio exists, the manifest is in `bootstrap` state and the full
repository-conformance form of `make readme-check` is expected to report that
the final WebP files have not yet been accepted.

## Modes

### Audit

Use Audit for a report only. README Steward must read this guide and the
manifest, then run `make readme-audit` and `make readme-check` as applicable.
It reports the audited commit range, relevant product changes, broken links or
contract errors, and whether a refresh is recommended.

Audit must not:

- create a worktree or staging directory;
- launch a server or browser capture;
- modify tracked or generated files;
- commit, push, call GitHub, or make external network requests.

### Refresh

Use Refresh when a maintainer asks for updated README screenshots or copy.
Routine Refresh runs start from `dev` in their own worktree:

```bash
source scripts/wt.sh
wt new docs/readme-refresh-YYYY-MM
```

README Steward then captures into `test-results/readme-refresh/<run-id>/` and
audits the proposed README copy. The directory is ignored by Git and contains:

- raw PNGs and optimized proposed WebPs;
- `run.json` with source commit, environment, scene statuses, and fingerprints;
- visible-text/privacy sidecars and logs;
- a current-versus-proposed comparison report;
- the proposed README content and text diff.

The one-time initial portfolio is a bootstrap exception: it is accepted on the
`feature/readme-steward` branch so the tooling and its first verified output
ship in one feature PR. Every later refresh uses a `docs/readme-refresh-YYYY-MM`
worktree and never edits `ori-agent-dev` directly.

## Approval checkpoints

### Checkpoint 1 — Apply staged refresh?

README Steward presents the staged report, proposed README diff, file sizes,
checksums, privacy scan, visual review results, and the exact files that would
change. Before explicit approval, it must not replace images, edit `README.md`,
update accepted metadata, or remove old image assets.

After approval, `make readme-accept RUN_ID=<run-id>` may copy the reviewed
WebPs, apply the reviewed README candidate, record acceptance metadata, and
remove an old asset only after a repository-wide reference check confirms it is
unused. It then reruns `make readme-check` and shows the tracked diff.

### Checkpoint 2 — Commit and open PR?

After acceptance and validation, README Steward presents the final diff. Before
separate explicit approval, it must not commit, push, call GitHub, or open a PR.
After approval, it creates one focused documentation commit and runs `wt pr`
against `dev`. It never merges the PR automatically.

## Determinism and CI

Two consecutive captures must produce identical optimized checksums when they
use the same source commit, fictional fixtures, lockfile, Chromium version,
operating system, and architecture. The accepted manifest records the rendered
product-source commit plus README/image checksums; it does not attempt the
impossible self-reference of storing the later documentation commit hash.

CI is a validator, not the canonical image publisher. It may run capture tests
into disposable CI paths and compare two runs on the same runner. It must never
copy images to `docs/images/`, update the manifest, create a branch, commit,
push, open a PR, or compare Linux-generated bytes with an accepted output from
another platform.

## Visual and privacy review

Every staged image needs human visual inspection at full resolution and at its
README display width. Reject any loading/error state, clipped UI, unreadable
text or borders, inconsistent theme, provider warning, first-run/onboarding
state, local path, real name, account data, secret, or misleading composition.

Screenshots must be captured from the running Ori UI. Do not use AI-generated,
retouched, composited, or presentation-only imagery or DOM/CSS.

## Monthly reminder

The Ori development workspace's **Workspace Manager** runs the monthly
read-only Audit under Watch policy. The default cadence is day 1 at 09:00 in the
workspace timezone, and the maintainer may change the day or time in existing
mission controls. The mission has external effects denied and may only run the
deterministic audit command.

On meaningful drift, it creates one Action Center finding titled **README
product documentation needs review** with the recommended action **Ask README
Steward to refresh the README.** Existing title-based deduplication keeps
repeat monthly findings together. Clean audits produce no Action Center noise.

README Steward owns interactive Refresh; the Workspace Manager owns the
scheduled Audit. Their shared boundary is `make readme-audit`.

## Failure handling and cleanup

Capture failures keep their ignored run directory and identify the failed scene
or rule, whether tracked files changed, and an exact safe retry command. The
capture lifecycle owns one recorded server PID and stops only that PID; it never
uses broad process matching such as `pkill`.

Do not remove a staging directory until the run has been reviewed, is confirmed
to be a no-op, or is superseded. Cleanup validates the exact temporary path and
runs separately from build, capture, and validation commands.

```sh
bash scripts/readme-refresh.sh cleanup --run-id <run-id>
```

## Release backstop

The release check retains `scripts/update-readme.sh` for version-badge updates.
It also runs the lightweight read-only `make readme-check` backstop once that
command is available. The release flow does not capture or accept screenshots.
