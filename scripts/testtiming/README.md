# Unit-test feedback and cache evidence

`../run-unit-tests.sh` runs the existing package selector once, captures one
fresh `go test -json -short -race -timeout 30m -count=1 -covermode=atomic` run,
and summarizes it with this standard-library-only tool. `RUNNER_OS=macOS`
selects `--platform`; all other values select the full suite by default.
`ORI_UNIT_SHARD=1 ORI_UNIT_SHARD_COUNT=4` selects one deterministic partition of
that full list. Both variables are required; invalid/empty partitions and
sharding the Darwin subset are errors. Race, short, fresh execution, atomic
coverage and the per-package timeout remain identical on every shard. No test
exclusion or retry is added.

CI runs four Ubuntu partitions and the existing Darwin subset concurrently in
`test-unit-run`. The `test-unit` aggregation matrix retains both original
`Unit Tests (OS, go.mod)` required names and downstream dependencies. Both gates
require all executions to succeed; failure, cancellation or skipping is not a
pass. The Linux gate additionally re-enumerates the canonical package list and
verifies each shard's exact assignment, full list, revision, flags, success
metadata and coverage before merging. It rejects absent/extra artifacts,
duplicated packages/coverage blocks, foreign coverage and malformed profiles.
Only the merged Linux profile is sent to Codecov, never partial shard/Darwin
coverage. Codecov delivery remains best-effort, but partition verification and
successful-shard artifact transfer are required parts of the gate.

`../unit-shards.py` uses deterministic longest-processing-time placement with
stable ties. `../unit-shard-weights.json` contains rounded-up package seconds
(only packages above the 10-second default) with that default for every other or
new package. Weights affect placement, not selection: the authoritative list
remains `list-unit-packages.sh`. Package times overlap and are not predicted
shard wall times.

**Provenance of the current weights (provisional, 2026-11-12).** Hosted Linux
package times from four successful runs (`dev` pushes 43253143983 and
43234435294, pull-request runs 43245027953 and 43229866686, each with the
canonical 210 packages exactly once): the median of each package's `go test
-json` Elapsed across the runs. Four packages whose fixtures this change
converted (`personalassistant`, `personalhq`, `learningmissions`,
`projectlibrary`) were then multiplied by their local before/after ratio, taken
from one interleaved race + atomic-coverage package run per side on a Darwin
arm64 machine with Go 1.26.0 (0.459, 0.097, 0.161 and 0.778). That scaling is an
estimate of the post-change cost, **not hosted evidence**: the first hosted
runs of this change measure the real times, and the weights should be refreshed
from them with the command above. The previous file (52 entries from an older
run, before the assistantops/setupjourney fixture improvement, with
`learningmissions` absent) had four shards that differ by about 1.8x under the
post-change estimate; the refreshed file brings the same estimate within about
1%. That is a placement-balance indicator computed from overlapping package
seconds, never a prediction of shard wall time or a CI saving.

### Initial local trial (2026-10-13)

The actual wrapper ran all four shards concurrently on a shared 10-core Darwin
arm64 host, Go 1.26.0, with `GOMAXPROCS=2` per shard and existing local build
caches. Every one of the 192 selected packages completed exactly once:
191 passed, one failed. Shard wall times (including compilation/reporting) were
801.61 / 643.96 / 729.59 / 610.95 seconds; overall 801.61 seconds. This is a local
correctness trial, **not** an Ubuntu CI speedup comparison.

The only failing test was
`internal/server/TestAssistantOperations_RealHostConcurrentSavesAndStartsStayAtOne`:
a background task wrote after database shutdown and temporary-directory cleanup
reported `directory not empty`. The same failure appears in pre-change CI run
[38335452618](https://github.com/johnjallday/ori-agent/actions/runs/38335452618).
No assertion was removed or retry added. Real aggregation checks rejected both
the absent failed shard and its unsuccessful metadata, emitting no coverage.
The trial is **not green**; resolve the separate shutdown failure before relying
on a green full-suite result. Hosted cold/warm cache and timing trials remain
**NOT RUN**.

## Reporting and failure behavior

The wrapper builds the reporter, uses `run-test-command.sh` for sandbox
ownership/cleanup, and creates a **separate, new, private** `ori-unit.*` evidence
directory (mode 0700) under `ORI_UNIT_ARTIFACT_PARENT`, `RUNNER_TEMP`, or
`TMPDIR` (in that order). It does not change the caller's umask or test
filesystem permissions. It prints that path and sets the `artifact-dir` action
output. It never overwrites/removes a caller's directory. Raw JSON, stderr, coverage, package
selection, metadata and summaries are retained; the reporter executable is not
uploaded. CI attempts diagnostic timing-artifact upload with seven-day
retention; its failure is non-gating, while the inline step summary remains.
The separate successful-shard result upload is mandatory for aggregation. Local evidence
remains at the printed path for inspection and explicit owner cleanup; this is
not an OS sandbox, and existing tests remain responsible for fixture isolation.

Successful migration chatter stays in JSON instead of flooding the job log.
Failed-test and package output gets bounded diagnostic tails, with raw artifacts
as the complete source. Diagnostics quote control characters so test output
cannot become GitHub workflow commands; Markdown names escape HTML/table syntax.
Records up to 16 MiB are accepted (not Scanner's default 64 KiB). Larger records,
malformed JSON, missing completions and I/O failures are errors, not fake passes.
Unknown JSON fields are tolerated. Build failures, timeouts and incomplete
streams still produce summaries. Top-level and nested timings are separate,
**not additive**; cached-result replays are excluded from execution counts and
slow-test rankings. Package times overlap and must not be summed as job time.

The original nonzero test exit status wins over a reporter failure. A reporter
failure also fails an otherwise successful command. SIGINT/SIGTERM return
130/143; the wrapper signals only the process group it created for the existing
runner and its test descendants. The test stream is captured *inside* that
runner, so cleanup messages cannot corrupt JSON. No `tee`/pipeline can hide the
Go exit status. A fresh `-count=1` execution is mandatory even with a warm build
cache; a restored successful test result is never evidence of faster execution.

```sh
make test-unit-tools                  # reporter, shell, Python partition/merge and YAML contracts
GOTOOLCHAIN=go1.26.0 go test -race ./scripts/testtiming ./scripts/ci
# Re-summarize existing, pure go-test JSON without executing tests:
go run ./scripts/testtiming < events.json > summary.md
```

The wrapper's metadata distinguishes reporter-build wall seconds from the test
command's wall seconds (which includes any compilation). Go package/test events
report execution separately. Cache action logs and GitHub job timestamps supply
restore/save/transfer/setup and whole-job time; the reporter cannot infer those.
For compile-only experiments, use a **separate** `go test -run '^$' -exec true`
invocation against the same selected packages/instrumentation. Record whether
that invocation warmed the cache before the measured execution. Use only owned
caches; never clear another session's shared cache to manufacture a cold run.

## Dedicated unit caches

Only unit execution jobs disable setup-go caching. Other Go jobs retain their
existing behavior. Explicit restore/save actions own two non-overlapping paths:

- `GOMODCACHE`: `ori-unit-mod-v1`, keyed by OS/architecture/image family, actual
  `go env GOVERSION`, and both dependency files. No broad restore fallback.
- `GOCACHE`: `ori-unit-build-v2`, with those same compatibility dimensions plus
  `race-cover-atomic` and shard identity (including `platform` for Darwin).
  The primary key ends in source SHA / run ID / attempt. Restore uses exactly
  one compatible shard prefix, never another Go version, image family,
  dependency hash, instrumentation or workflow namespace. Each successful run
  can publish new compilation instead of hitting an immutable key forever.
  Siblings cannot replace each other's useful compilation. The v2 namespace
  starts cold; measure compatible-warm runs separately, and monitor the larger
  total cache footprint/evictions before claiming a job-level speedup.

`ImageOS` identifies the image family (for example ubuntu24), with `RUNNER_OS`
as a fallback; it is not the changing weekly `ImageVersion`. Go's own content
hashing still validates individual build entries. All tests execute freshly.
Both saves require successful tests **and** reporting, and skip exact-key hits.
Cache restore/save and diagnostic timing-artifact upload are best-effort.
Successful Linux result artifacts are mandatory: without them the required
gate cannot prove completeness. `unit-results-N` names are scoped to the current
workflow run, overwritten only by successful reruns, and checked against the
aggregation checkout revision. Rerunning failed jobs may reuse successful
siblings from the same run/revision; other workflow runs are never downloaded.
Saves also require a successful restore step, so they cannot run with a missing
primary key.
Failed/cancelled jobs cannot publish partially compiled work. Cache pruning is
disabled in this ephemeral CI job so it cannot erase restored work before save.

The job has only `contents: read`; native GitHub cache ref rules govern access.
PR merge-ref caches are not promoted to the base/default branch. There is no
`pull_request_target`, privileged `workflow_run`, cross-workflow prefix, or
cache deletion. Other jobs cannot be the first writer of these unit keys.

Unique build entries trade freshness for cache quota/transfer cost. They remain
subject to the repository's existing GitHub quota and eviction policy. **Hosted
archive size, transfer time, eviction pressure and next-run persistence must be
measured before claiming a job-level benefit.** Record cold and compatible-warm
runs separately, including actual Go/runner/image, source, selected-package hash,
cache keys/hits, archive bytes, restore/save durations, compile-only timings,
package execution, test identities/skips/coverage and full-job duration. Compare
repeated matched medians; local Darwin fixture speedups are not Ubuntu evidence.
If transfer/eviction outweighs reuse, revise this unit-only policy from that
measurement rather than adding an incompatible fallback or deleting shared
caches. Actions changes alone cannot demonstrate cache persistence.

### Linux application-build cache: measured, not shipped

A dedicated cache for the Linux server build (the `build` job as the one writer, an eligible
`test-e2e` as a restore-only consumer) was designed and then measured before being added.
In an owned, empty-cache `golang:1.26.0` container (linux/arm64 VM, a bound and not hosted
evidence), `make build` took 70 s cold, 7 s warm and 17 s after a one-package edit, and the cache it
would archive was 1.14 GiB raw, about 250 MiB compressed, per distinct source. Against that: `Build`
(~150 s) is never the slowest job (the slowest Linux unit job runs 1,233-1,702 s), `test-e2e` is skipped
when no provider key is configured (none is), and the repository's cache is already near its quota
with critical-path unit archives of 0.6-1.1 GB. A new archive per pushed source would add eviction
pressure for no median critical-path gain, so none was added.

Revisit it only when provider keys are configured (then E2E's ~100 s build is on the critical path).
The safe shape: one Linux writer (`build`, `push` events only, so PR merge refs do not publish),
restore-only consumers gated on eligibility with byte-identical key and path strings, save only after a
successful build and on a miss, a key with Go version, OS/arch/image family, dependency hash, non-race
mode and source freshness, `continue-on-error` on restore and save, and no overlap with the
`ori-unit-*` namespaces. Judge it from a hosted comparison of compatible runs (archive bytes,
restore/save seconds, hit rates, and whether the unit caches still hit), and revert it if transfer or
eviction costs more than the compilation it saves.

`scripts/ci/cache_ownership_workflow_test.go` keeps the existing ownership one-sided: only
`test-unit-run` publishes caches with an explicit step, and only it uses the `ori-unit-` keys.

## Reproducible baseline and comparison procedure

Use this when a change claims to make tests or CI faster. It reuses the tools above (the
wrapper's artifacts, `go run ./scripts/testtiming`, `unit-shards.py`, the Actions API); no new
reporter is needed. Its purpose is to attribute a difference to the real source, not to machine
load, a warm cache or a lucky run.

**1. Label every number.** State the source revision (a PR run's `metadata.txt` `revision` is
the *merge* revision, not the feature head), the environment class (hosted Ubuntu amd64, hosted
macOS arm64, local native Darwin arm64, local Linux container, emulated), the actual `go version`
line (not the matrix label), the flags, the sample count, and the median and range. Never mix
classes in one comparison. A local timing is never a hosted saving, and package seconds are never
added into job time.

**2. Hosted comparison** (the only evidence for CI time). Download artifacts with
`gh run download <run-id> --dir <new empty dir>` (7-day retention; summarize before they expire)
and read job and step timestamps with
`gh api 'repos/<owner>/<repo>/actions/runs/<run-id>/jobs?per_page=100'`. Keep these apart:

- *queue delay*: a job's `started_at` minus the earliest job `created_at` in the run (a run can
  also wait in the concurrency group before any job exists, so measure from the first job);
- *active time*: `completed_at` minus `started_at`; the critical path is the slowest Linux
  `Unit execution` job, then the `Unit Tests` aggregation, then E2E;
- *compile versus execute*: the wrapper's `test_command_seconds` includes compilation, `events.json`
  has execution per package; cache restore/save/transfer come from the action logs;
- *cold versus compatible-warm cache*: record the cache keys, hit/miss and archive bytes;
- *source versus fixture versus scheduling changes*: compare per package, not only per job.

Compare **medians by event type** (pull-request runs with pull-request runs, `dev` pushes with `dev`
pushes) over at least three candidate runs against the pre-change baseline. Hosted runs vary a lot
on identical code, so a single run proves nothing.

**3. Controlled local before/after** (fixture and test changes). Pin the toolchain CI uses:
`GOTOOLCHAIN=go1.26.0` (CI follows `go.mod`'s exact patch; a newer local Go is a different
environment) and `TZ=UTC`. Take one sample per package, sequentially, never beside another
test run: hold the machine-wide unit lock (source `scripts/ci-local.sh` and call
`ci_local_with_unit_lock`, with `TZ=UTC` like the gates: the lock compares process start times as
text, so a waiter and a holder in different zones make a live owner look dead), wait until no other
`ori-test-run.*` test binary exists and the one-minute load is low, and record `uptime` and the top
CPU processes before and after. Do not trust the lock alone: also watch for a second owned
`ori-test-run.N` sandbox during the sample, and flag or repeat a sample taken under contention. Run each sample inside the owned sandbox with
`ORI_SKIP_CACHE_PRUNE=1 ./scripts/run-test-command.sh bash -c 'exec go test "$@" > events.json' sample ...`
so wrapper messages cannot corrupt the JSON, with the CI flags
`-json -short -race -count=1 -covermode=atomic -coverprofile=<file> -timeout 30m [-run <regex>] <pkg>`,
then `go run ./scripts/testtiming < events.json`. Do a compile-only warm-up first
(`go test -race -covermode=atomic -count=1 -run '^$' <pkg>`) and record its seconds, so execution
is not charged for compilation. Repeat each representative sample at least three times and report
median and range; a difference inside the range of one condition is not a result. A "cold" sample
uses an empty `GOCACHE` owned by the sample, never a cleared shared cache. A restored test result
is not execution.

**4. Parity, not only a percentage.** Compare, for the same package and flags, the set of test and
subtest identities with their outcomes and skips, the migration chatter (`Running database
migrations` / `Database initialized` events) and the covered production blocks as sets of
`file:range` entries with a nonzero count. Normalize the random digits of `t.TempDir()` that some
subtest names embed (for example `go-tmp/Test...<N>`), or every run looks different. Repeat the
baseline to learn its own noise before judging a delta, and explain any remaining difference.

**5. Sequencing.** Before opening the PR: offline contracts, controlled local comparisons and
the full `make ci-local` gate. After `wt pr` pushes, the pull-request trigger supplies the first
hosted candidate run (there is no dispatch trigger, and none is added to get one early). Collect at
least three compatible candidate runs (normal pushes, or reruns an operator authorizes), compare
medians as in step 2, and mark anything not yet observed NOT RUN. Never push early, never use
`--skip-checks`, and never turn a local estimate into a claimed percentage.

### Hosted baseline before the throughput change (2026-11-12)

Twelve successful `ci.yml` runs from 2026-11-11 and 2026-11-12, all after the last workflow edit
(2026-11-04), six `dev` pushes and six pull requests, four Linux shards each (canonical list: 210
packages, split 51/53/53/53; the macOS leg runs 9). Medians (dev / PR): slowest Linux unit job
1,569 / 1,572 s, wall (first job created to last completion) 1,735 / 1,743 s, E2E 134 / 139 s,
Integration 25 / 28 s. Observed noise on identical code: the slowest Linux unit job 1,233-1,702 s,
one shard 900-1,442 s, `personalassistant` 852-1,437 s, and covered blocks up to about 11 per shard
(shard 2 covered 42,764-42,775 of 63,964 blocks with unchanged source). The slowest Linux shard
was shard 2 in 7 of the 12 runs. One run's macOS aggregation leg waited 300 s for a runner while
Integration/E2E waited behind it (`needs: test-unit`); that is queue delay, not execution. The
curated tables and the per-run inventory are in the feature's findings and PR description.

