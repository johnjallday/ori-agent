# Isolated pre-migrated test databases

`testdb.Open(t)` allocates a **new, independently owned** database for ordinary
CRUD/service/handler tests. Once per test process, the helper opens a private
file through production `database.Open`, runs the current migrations, verifies
a complete WAL checkpoint, closes it, reads its bytes and removes its directory.
Only immutable bytes survive. Each fixture writes a private copy and calls real
`database.Open` again, retaining production guards, pragmas and connection setup.
There is no shared mutable database, checked-in schema snapshot, driver hook,
production test-mode switch or caller-supplied path.

Schema changes need no fixture regeneration: migrations are the source of truth.
Initialization errors are shared consistently by concurrent callers; a partial
image is never published. Failed copies are removed without changing the image.
All directories/files belong to the test/helper, with directory mode 0700 and
file mode 0600. Initialization does not use HOME, CWD or ORI_DATA_DIR to select
application state, nor does it consult native credentials.

## Shutdown ownership

**The caller owns connection shutdown.** Open registers directory removal through
`t.TempDir`, but does not automatically close the database: a store may need to
join workers and flush queued changes first. Register its sole owner immediately
so early setup failures also close it, and check close errors:

```go
db := testdb.Open(t)
t.Cleanup(func() {
    if err := db.Close(); err != nil {
        t.Error(err)
    }
})
store := personalassistant.NewSQLiteStore(db) // Does not own shutdown.
```

For a hybrid store, register **only the store's close**, never both:

```go
db := testdb.Open(t)
store := session.NewHybridStoreWithDB(db, 50)
t.Cleanup(func() {
    if err := store.Close(); err != nil {
        t.Error(err)
    }
})
```

If an existing fixture also returns an explicit cleanup function, wrap it in
`sync.OnceFunc` and register the same function with `t.Cleanup`. Register writer
and request cleanup afterwards (cleanup is LIFO), so work drains before the
store closes and its directory disappears. Do not close a fixture owned by
another test or reuse its connection after cleanup.

## Eligibility and intentional differences

Use for incidental database setup, not tests whose subject is opening a database.
Keep real `database.Open` in migration/upgrade, initialization timestamp, startup,
reset, retained-vault refusal, default-path, memory-storage and close/reopen tests.
Keep `resetfixture`'s installation seeding and lifecycle isolation unchanged.
Do not blanket-parallelize environment/CWD-mutating consumer tests.

Unlike the old `InMemory: true` fixtures, clones have a real temporary path,
WAL journaling and file-backed mmap (64 MiB). The single open/idle connection,
foreign keys, busy timeout 0 (matching existing literal fixture configs),
synchronous NORMAL, cache size -8000 and temp_store MEMORY come from production
Open. This is **not** a replacement for explicitly testing memory-vs-file or
locking behavior. Seed/migration timestamps denote template initialization,
not each call; ordinary fixture writes still have their own timestamps/state.

Regression tests compare all schema objects and seed rows to a real fresh open
(normalizing initialization timestamps), plus constraints, preference triggers,
FTS insert/update/delete/search, pool/pragmas, concurrency, mutation/close
isolation, first-caller lifetime and failure cleanup. The package is included
by the existing full unit-package selector, not a manual-only gate.

## Measurement

`BenchmarkOpenFresh` retains the real production initialization path for memory
and test-owned files. `BenchmarkOpenClone` excludes one-time template creation.
Both include allocation, a local-user seed check and close; neither asserts a
wall-clock threshold. Run a bounded comparison from the module root:

```sh
ORI_SKIP_CACHE_PRUNE=1 GOTOOLCHAIN=go1.26.0 ./scripts/run-test-command.sh \
  go test -short -race -timeout 30m -run '^$' -bench '^BenchmarkOpen(Fresh|Clone)$' \
  -benchtime=3x -count=3 ./internal/testutil/testdb

ORI_SKIP_CACHE_PRUNE=1 GOTOOLCHAIN=go1.26.0 ./scripts/run-test-command.sh \
  go test -short -race -count=5 -shuffle=on ./internal/testutil/testdb
```

Compilation is outside `ns/op`; measure end-to-end command time separately.
Compare the same OS/architecture, actual Go version, instrumentation and cache
condition. Repeated `-count` samples execute afresh; cached successful results
are not evidence of faster execution. Use a newly allocated `GOCACHE` for a
cold-build comparison, never clear a shared developer cache. Microbenchmarks
are feasibility evidence, not a substitute for comparable full Ubuntu job
measurements, including compilation and cache transfer.
