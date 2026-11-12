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

## Ordinary fixture adoption

`assistantops.newFixture` and `setupjourney.openTestStore` now also use private
clones. Their service/CRUD assertions and test identities are unchanged;
explicit lifecycle/reopen/connection/storage-mode fixtures still use real fresh
initialization. These consumers do not share a connection or mutable state.

A local Darwin arm64 Go 1.26.0 comparison used fresh `-short -race -count=1
-covermode=atomic` runs before and after this adoption:

| Package | Before | After |
| --- | ---: | ---: |
| `internal/assistantops` | 351.101 s | 14.415 s |
| `internal/setupjourney` | 391.744 s | 62.629 s |

All 343 test/subtest completion identities and outcomes matched. Statement
inventories were unchanged, with no previously covered blocks lost in this
sample (assistantops 754 → 756 of 880 statements; setupjourney 3431 → 3432 of
4314). These are single-run local package timings, not Ubuntu CI or whole-job
speedup evidence. Compilation is excluded and package executions overlap.
Hosted cold/warm measurements and repeated samples remain necessary.

`backuprecovery.newLocalStoreFixture`, `workspacecontinuity.localFixture` and
the SQLite-primary fixtures in `projectlibrary` followed on 2026-10-22, after
the macOS unit leg's profile showed `database.(*DB).migrate` taking ~3 s of
every ~3.4 s test under the race detector. `workspacecontinuity` keeps a
`freshLocalFixture` for its three close/reopen and reset tests; `projectlibrary`
keeps real opens for its reopen round trip and the cross-process fence
contract. Same local Darwin arm64 Go 1.26.0 method, one package per run:

| Package | Before | After |
| --- | ---: | ---: |
| `internal/backuprecovery` | 596.571 s | 93.931 s |
| `internal/workspacecontinuity` | 117.603 s | 31.255 s |
| `internal/projectlibrary` | 519.473 s | 396.085 s |

All 495, 92 and 440 test/subtest outcomes matched, and covered coverage blocks
were unchanged (backuprecovery 2074 → 2075 of 3137, projectlibrary 3412 of
4357). projectlibrary's remaining time is in catalog and arrangement stress
tests that never open a database.

### Service fixtures (`ci-test-throughput`, 2026-11-12)

`personalassistant`, `personalhq` and `learningmissions` paid the full migration
chain for every ordinary service fixture (125, 44 and 105 matched `Running
database migrations` events per race run on the base). Their ordinary fixtures now
use private clones:

- `personalassistant`: `openTestDB(t)` (a `testdb.Open` plus a checked close
  registered as the sole owner) backs `newTestStore`, the hire-record and
  hire-proposal helpers and the second-brain harness. The harness keeps its
  knowledge database and its follow-up database as two separate clones, and
  joins the follow-up service's workers (`t.Cleanup(service.Wait)`) before the
  database closes.
- `personalhq`: `newTestHarness` opens one clone per harness.
- `learningmissions`: `testStore`, `testService`, `workFixture` and
  `reviewFixture` take a clone and re-apply the original 5,000 ms busy timeout
  (`fixtureBusyTimeoutMillis`) because clones default to 0 ms. Production
  connection setup still runs; only that one pragma is added.

**Deliberately still fresh.** `freshTestStore`, `freshTestService`,
`freshWorkFixture` and `freshReviewFixture` keep the real `database.Open` on
the original path for the restart/reopen matrix, the full-app reset matrix and
the reload test. `requireFreshlyInitialized` asserts that a caller routed to
the fresh path really has the original (non-WAL) storage, so a lifecycle test
cannot drift onto a clone unnoticed. `TestFixtureDatabasesKeepTheirStorageContract`
and `TestOrdinaryFixturesAreIndependentDatabases` pin both routes; the other two
packages each have a test that two fixtures never share a database.

Single interleaved before/after samples on a local Darwin arm64 machine with Go
1.26.0 (`-short -race -count=1 -covermode=atomic`, base `292a4e41b` exported with
`git archive` versus this tree, same lock and background conditions):

| Package | Migrations before → after | Package time before → after | Identities | Covered blocks (of total) |
| --- | ---: | ---: | ---: | ---: |
| `internal/personalassistant` | 125 → 1 | 522.4 s → 239.8 s | 1379 → 1380 | 8680 → 8681 (11445) |
| `internal/personalhq` | 44 → 1 | 119.7 s → 11.6 s | 59 → 60 | 259 → 259 (334) |
| `internal/learningmissions` | 105 → 7 | 305.1 s → 49.1 s | 215 → 219 | 1296 → 1296 (1843) |
| `internal/testutil/testdb` | 4 → 4 | 16.2 s → 15.2 s | 25 → 25 | 24 → 24 (30) |

No test identity disappeared and no outcome changed; the added identities are the
guard tests named above. The remaining migrations are the per-process template
builds and `learningmissions`' fresh lifecycle fixtures. These are one-run local
timings, not Ubuntu CI evidence and not whole-job savings: package executions
overlap and compilation is excluded. The hosted comparison stays a review
criterion.

**Follow-up candidates, not changed here:** `internal/database`,
`internal/settingsreset`, `internal/workspacemap`, `internal/chathttp`,
`internal/assistantsetup` and `internal/server` also show migration events in
their logs. Each needs its own caller audit before conversion; none was touched.

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
