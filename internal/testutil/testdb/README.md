# Database fixture measurements

Keep `BenchmarkOpenFresh` as a baseline that calls the real production
`database.Open`, including all current migrations. It allocates only in-memory
state or files under `b.TempDir`; it never selects the default data directory.
Each iteration checks the seeded local user and closes the database. There are
no timing thresholds.

Run a bounded race-instrumented comparison from the module root:

```sh
ORI_SKIP_CACHE_PRUNE=1 GOTOOLCHAIN=go1.26.0 ./scripts/run-test-command.sh \
  go test -short -race -timeout 30m -run '^$' -bench '^BenchmarkOpenFresh$' \
  -benchtime=3x -count=3 ./internal/testutil/testdb
```

The benchmark measures initialization, the seed-row query, and close. File setup
includes allocation of a new temporary directory. Compilation is outside the
reported `ns/op`; measure end-to-end command time separately. Compare on the
same OS/architecture, actual Go version, instrumentation and cache condition.
`-count=3` executes fresh samples; cached successful test results are not timing
evidence. Use a newly allocated `GOCACHE` for a cold-build comparison, never
`go clean -cache` on a shared developer cache.

This package is test support only. Migration, startup, reset, retained-vault
safety and file-close/reopen tests must continue to exercise real initialization.
