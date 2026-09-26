# Unit-test feedback and cache evidence

`../run-unit-tests.sh` runs the existing package selector once, captures one
fresh `go test -json -short -race -timeout 30m -count=1 -covermode=atomic` run,
and summarizes it with this standard-library-only tool. `RUNNER_OS=macOS`
selects `--platform`; all other values select the full suite. Required matrix
labels and downstream dependencies remain unchanged. Linux alone uploads
coverage to Codecov. There is no package sharding, new exclusion or test retry.

## Reporting and failure behavior

The wrapper builds the reporter, uses `run-test-command.sh` for sandbox
ownership/cleanup, and creates a **separate, new, private** `ori-unit.*` evidence
directory (mode 0700) under `ORI_UNIT_ARTIFACT_PARENT`, `RUNNER_TEMP`, or
`TMPDIR` (in that order). It does not change the caller's umask or test
filesystem permissions. It prints that path and sets the `artifact-dir` action
output. It never overwrites/removes a caller's directory. Raw JSON, stderr, coverage, package
selection, metadata and summaries are retained; the reporter executable is not
uploaded. CI attempts evidence-artifact upload for seven days; GitHub storage
failure is non-gating, while the inline step summary remains. Local evidence
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
make test-unit-tools                  # reporter, shell wrapper and YAML contracts
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

Only the Unit Tests job disables setup-go caching. Other Go jobs retain their
existing behavior. Explicit restore/save actions own two non-overlapping paths:

- `GOMODCACHE`: `ori-unit-mod-v1`, keyed by OS/architecture/image family, actual
  `go env GOVERSION`, and both dependency files. No broad restore fallback.
- `GOCACHE`: `ori-unit-build-v1`, with those same compatibility dimensions plus
  `race-cover-atomic`. The primary key ends in source SHA / run ID / attempt.
  Restore uses exactly one compatible prefix, never another Go version, image
  family, dependency hash, instrumentation or workflow namespace. Each successful
  run can publish new compilation instead of hitting an immutable key forever.

`ImageOS` identifies the image family (for example ubuntu24), with `RUNNER_OS`
as a fallback; it is not the changing weekly `ImageVersion`. Go's own content
hashing still validates individual build entries. All tests execute freshly.
Both saves require successful tests **and** reporting, and skip exact-key hits.
Cache restore/save and evidence-artifact upload are best-effort: service/quota
failure cannot turn a correct test result into a red check. Saves also require
a successful restore step, so they cannot run with a missing primary key.
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
